package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/escalier-lang/escalier/internal/solver/ucs"
)

// The interim coverage check, read off the UCS normalized form.
//
// A conditional form is exhaustive when no value of its scrutinee falls through every arm.
// What the decision reads is the split structure the IR already carries: the tag tests over
// the root scrutinee, and the default tail a value reaches when none of those tests matched.
// The predicates it rests on stay here rather than moving into `ucs`, because each one reads
// a soltype. Whether a union is exact, whether an object or tuple carries an open tail, and
// which union member a tag test covers are all facts about the type.
//
// The rules are the interim ones M4 and M6 settled on. An inexact scrutinee takes a
// catch-all, an exact union is covered once every member has a covering branch, and an arm
// that can fail its guard covers nothing on its own. Phase 2 (#883) replaces all three with
// `residual = scrutinee ∧ ¬covered ; exhaustive iff residual <: ⊥`, and this file is the
// seam it replaces.

// coverage is what the check reads off one normalized form.
type coverage struct {
	// catchAll reports whether a value failing every tag test at the root still reaches an
	// arm body. It is the IR's reading of an unguarded catch-all arm, which normalization
	// moves out of the branches and into the split's default tail.
	catchAll bool
	// tags holds the tag test of every branch over the root scrutinee that covers what it
	// tests, meaning every path below the branch reaches an arm body. A union member is
	// covered when one of these tests names it.
	tags []ucs.Test
}

// coverageWalk reads the coverage of one normalized form. root is the scrutinee the form's
// top-level split tests, which is what tells a test of the whole value apart from a test of
// a projection out of it.
type coverageWalk struct {
	root *ucs.Scrutinee
	tags []ucs.Test
	// matched memoizes alwaysMatches per term, since a tail several branches fall into is
	// asked about once per branch.
	matched map[ucs.Norm]bool
	// seen holds every term collect has read, so a term two edges reach is read once.
	seen set.Set[ucs.Norm]
}

// readCoverage collects what the arms of the form rooted at split cover.
//
// Every split over the root scrutinee is read, not the top-level one alone. Normalization
// emits an arm again inside each earlier fallible branch's fallthrough, and it drops the
// top-level branch of an arm whose continuation such a copy already runs. An arm's tag test
// can therefore live only in a copy. `match x { 1 if g => a, 1 => b }` is one such form: the
// unguarded `1` survives as what the guard falls into, and its own branch is dropped as a
// duplicate.
func readCoverage(split *ucs.NormSplit) coverage {
	w := &coverageWalk{
		root:    split.Scrutinee,
		tags:    nil,
		matched: map[ucs.Norm]bool{},
		seen:    set.NewSet[ucs.Norm](),
	}
	catchAll := w.alwaysMatches(split.Default)
	w.collect(split)
	return coverage{catchAll: catchAll, tags: w.tags}
}

// collect records the covering tag tests of every split over the root scrutinee.
func (w *coverageWalk) collect(term ucs.Norm) {
	if term == nil || w.seen.Contains(term) {
		return
	}
	w.seen.Add(term)
	if split, ok := term.(*ucs.NormSplit); ok && split.Scrutinee == w.root {
		for _, branch := range split.Branches {
			if branch.Test != nil && w.alwaysMatches(branch.Cont) {
				w.tags = append(w.tags, branch.Test)
			}
		}
	}
	for _, next := range continuations(term) {
		w.collect(next)
	}
}

// alwaysMatches reports whether control reaching term reaches an arm body however the tests
// below it turn out. A branch whose continuation always matches covers the tag it tested,
// and a default tail that always matches covers everything the branches left.
//
// This is where the guard rule lives. A guard sends a false condition to its own failure
// continuation, so a branch holding one covers its tag only when that continuation reaches
// a body too. A lone `{x} if b => x` does not, which is what makes a guarded arm cover
// nothing.
func (w *coverageWalk) alwaysMatches(term ucs.Norm) bool {
	if term == nil {
		return false
	}
	if got, asked := w.matched[term]; asked {
		return got
	}
	// Seed the memo before recursing. The normalized form is a tree of shared subterms with
	// no cycle, so no caller reads the seed. It bounds the recursion should a later rewrite
	// introduce one.
	w.matched[term] = false
	got := false
	switch n := term.(type) {
	case *ucs.BodyLeaf:
		got = true
	case *ucs.NormBind:
		got = bindsEveryValue(n) && w.alwaysMatches(n.Cont)
	case *ucs.NormGuard:
		got = w.alwaysMatches(n.Cont) && w.alwaysMatches(n.Default)
	case *ucs.NormSplit:
		got = w.splitAlwaysMatches(n)
	case *ucs.EscapeLeaf, *ucs.FallbackLeaf:
		// The two leaves of a `val pat = init else { … }`. The fallback runs precisely when
		// the pattern failed, so counting it as a body would make every such declaration
		// cover its scrutinee. The escape leaf ends the success path and carries no body at
		// all. Neither is an arm a coverage question is asked about, and `ucs.DesugarMatch`
		// mints neither.
	}
	w.matched[term] = got
	return got
}

// splitAlwaysMatches reports whether control reaching a split reaches an arm body. A value
// either passes one branch's test and continues below it, or fails every test and continues
// into the tail, so the split matches when all of those continuations do.
//
// A split over a projection gets a second reading. `{a: {b}}` tests `{a}` on the scrutinee
// and then `{b}` on the projection `p.a`. The interim rules read a structural sub-pattern as
// irrefutable, so such a split matches whenever the continuation under its test does. A
// projected literal, class, or extractor test is refutable and gets no such reading. Neither
// does a test of the root scrutinee. Whether a structural test covers the whole value
// depends on the scrutinee's exactness, which checkCondExhaustive decides from the type.
func (w *coverageWalk) splitAlwaysMatches(split *ucs.NormSplit) bool {
	if w.alwaysMatches(split.Default) && w.branchesAlwaysMatch(split) {
		return true
	}
	if split.Scrutinee == w.root || len(split.Branches) != 1 {
		return false
	}
	branch := split.Branches[0]
	return structuralTest(branch.Test) && w.alwaysMatches(branch.Cont)
}

// branchesAlwaysMatch reports whether every branch of a split reaches an arm body once its
// test matched.
func (w *coverageWalk) branchesAlwaysMatch(split *ucs.NormSplit) bool {
	for _, branch := range split.Branches {
		if !w.alwaysMatches(branch.Cont) {
			return false
		}
	}
	return true
}

// bindsEveryValue reports whether a bind names its projection without testing it, which is
// what a wildcard or identifier pattern does. A nameless bind instead holds a pattern
// normalization kept whole rather than flattening into a split. A bare rest is the only such
// pattern flattening leaves, the `...rest` of `match p { ...rest => 1 }`, and it covers
// nothing. ast.IsCatchAllPat gives it the same verdict, and the pass that lowers the surface
// reports it unsupported on its own.
func bindsEveryValue(bind *ucs.NormBind) bool {
	return bind.Name != "" || bind.Pat == nil
}

// checkCondExhaustive reports a NonExhaustiveMatchError when the arms of one conditional
// form leave a value of its scrutinee uncovered. norm is the form's normalized IR, and
// shape the scrutinee snapshot taken before any arm bound, which is what the union members
// and the structural exactness are read off.
//
// The message names the construct the top-level split lowered from rather than assuming a
// `match`, so the wording follows the surface form the user wrote.
func (c *checker) checkCondExhaustive(scope *Scope, norm ucs.Norm, shape soltype.Type) {
	split, isSplit := norm.(*ucs.NormSplit)
	if !isSplit {
		return
	}
	cov := readCoverage(split)
	if cov.catchAll {
		return
	}
	// A transparent alias scrutinee, an enum handle or a user `type` reference, carries the
	// alias rather than the type it stands for. Expand it to that type before dispatching, the
	// same unfold constrain performs, so `match c { Color.RGB(..) => .., Color.Hex(..) => .. }`
	// over `val c: Color` covers the variant union without a default arm.
	carrier := c.expandAliasChain(soltype.CarrierOf(shape))
	if u, isUnion := carrier.(*soltype.UnionType); isUnion {
		if !c.unionTagsExhaustive(scope, u, cov.tags) {
			c.report(&NonExhaustiveMatchError{Origin: split.Origin})
		}
		return
	}
	inexact, isStructural := structuralInexact(carrier)
	if !isStructural {
		return
	}
	// An exact object or tuple is covered by a branch that destructures its shape. An inexact
	// one carries an open tail of values no such branch can see, so only a catch-all covers it.
	if !inexact && hasStructuralTag(cov.tags) {
		return
	}
	c.report(&NonExhaustiveMatchError{Origin: split.Origin})
}

// unionTagsExhaustive reports whether the covering tag tests cover a union scrutinee. An
// inexact union carries an open tail no tag names, so it takes a catch-all, which the caller
// has already ruled out. An exact one is covered when every member is.
func (c *checker) unionTagsExhaustive(scope *Scope, u *soltype.UnionType, tags []ucs.Test) bool {
	if u.Inexact {
		return false
	}
	for _, member := range u.Types {
		if !c.memberTagged(scope, member, tags) {
			return false
		}
	}
	return true
}

// memberTagged reports whether some covering tag test names a single union member,
// dispatching on the member's kind. A literal member needs an equal literal test, a `null`
// or `undefined` member a test on that same word, a nominal member a class or extractor test
// naming its class, and a structural object or tuple member a test of its shape. Any other
// member kind has no tag short of a catch-all, which the caller has already ruled out.
func (c *checker) memberTagged(scope *Scope, member soltype.Type, tags []ucs.Test) bool {
	switch m := member.(type) {
	case *soltype.LitType:
		return c.litTagged(m, tags)
	case *soltype.NullType, *soltype.UndefinedType:
		return atomTagged(member, tags)
	case *soltype.ClassType:
		return c.nominalTagged(scope, m, tags)
	case *soltype.ObjectType, *soltype.TupleType:
		return structuralTagged(member, tags)
	default:
		return false
	}
}

// litTagged reports whether some covering test is a literal equal to the given literal
// member.
func (c *checker) litTagged(member *soltype.LitType, tags []ucs.Test) bool {
	for _, tag := range tags {
		lit, isLit := tag.(*ucs.LitTest)
		if !isLit {
			continue
		}
		if lt, ok := c.litTypeOf(lit.Lit); ok && member.Equal(lt) {
			return true
		}
	}
	return false
}

// atomTagged reports whether some covering test names the given member, which is the `null`
// or the `undefined` atom. It is the atom twin of litTagged, and equalType decides the match
// since neither atom carries a value to compare.
func atomTagged(member soltype.Type, tags []ucs.Test) bool {
	for _, tag := range tags {
		lit, isLit := tag.(*ucs.LitTest)
		if !isLit {
			continue
		}
		if atom, _, isAtom := atomLitOf(lit.Lit); isAtom && equalType(atom, member) {
			return true
		}
	}
	return false
}

// nominalTagged reports whether some covering test names the member's class. An instance
// pattern lowers to a class test and an extractor pattern to an extractor test, and each
// resolves its name through the type sort so `Color.RGB` covers the `Color.RGB` variant.
// Whether the values under the tag are bound irrefutably is already settled: a refutable
// sub-pattern becomes a split of its own, which leaves the branch out of the covering tags.
func (c *checker) nominalTagged(scope *Scope, member *soltype.ClassType, tags []ucs.Test) bool {
	for _, tag := range tags {
		switch t := tag.(type) {
		case *ucs.ClassTest:
			if ct, ok := c.instancePatClass(scope, ast.QualIdentToString(t.Name)); ok && ct.Name == member.Name {
				return true
			}
		case *ucs.ExtractorTest:
			if ct, ok := c.resolveQualClassType(scope, t.Name); ok && ct.Name == member.Name {
				return true
			}
		default:
			// A structural or literal tag names no class, so it covers no nominal member.
		}
	}
	return false
}

// structuralTagged reports whether some covering test is an object or tuple shape the member
// carries, which is what lets the branch under that test destructure the member.
func structuralTagged(member soltype.Type, tags []ucs.Test) bool {
	for _, tag := range tags {
		if testMatchesMemberShape(tag, member) {
			return true
		}
	}
	return false
}

// hasStructuralTag reports whether some covering test is an object or tuple shape, which is
// what covers an exact structural scrutinee.
func hasStructuralTag(tags []ucs.Test) bool {
	for _, tag := range tags {
		if structuralTest(tag) {
			return true
		}
	}
	return false
}

// structuralTest reports whether a tag test names an object or tuple shape. Those are the
// two tests a value of a structural type can pass, and the two the interim semantics read
// as irrefutable below the level they test.
func structuralTest(test ucs.Test) bool {
	switch test.(type) {
	case *ucs.ObjectTest, *ucs.TupleTest:
		return true
	default:
		return false
	}
}
