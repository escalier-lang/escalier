package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/escalier-lang/escalier/internal/solver/ucs"
)

// The interim coverage check, read off the UCS normalized form: the tag tests over the root
// scrutinee, and the default tail a value reaches when none of them matched. The predicates
// stay here rather than moving into `ucs`, since union exactness, an open structural tail,
// and which member a tag covers are all facts about a soltype.
//
// The rules are M4's and M6's. An inexact scrutinee takes a catch-all, an exact union is
// covered once every member has a covering branch, and an arm that can fail its guard covers
// nothing on its own. Phase 2 (#883) replaces all three with
// `residual = scrutinee ∧ ¬covered ; exhaustive iff residual <: ⊥`.

// coverage is what the check reads off one normalized form.
type coverage struct {
	// catchAll reports whether a value failing every tag test at the root still reaches an
	// arm body. It is the IR's reading of an unguarded catch-all arm.
	catchAll bool
	// tags holds the tag test of every branch that covers what it tests. A union member is
	// covered when one of these names it.
	tags []ucs.Test
}

// coverageWalk reads the coverage of one normalized form. root is the scrutinee the
// top-level split tests, which tells a test of the whole value apart from one of a projection
// out of it.
type coverageWalk struct {
	root *ucs.Scrutinee
	tags []ucs.Test
	// matched memoizes alwaysMatches, since a tail is asked about once per branch falling
	// into it.
	matched map[ucs.Norm]bool
	// seen bounds the walk to one visit per term.
	seen set.Set[ucs.Norm]
}

// readCoverage collects what the arms of the form rooted at split cover. More than the
// top-level split is read: a guarded arm whose pattern makes no test takes the whole split's
// tail, so the arms after it are branches of a split inside that tail and of no other.
// Reading the top-level split of `match v { _ if g => 0, {x} => 1, {y} => 2 }` alone would
// find no covering branch at all.
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

// collect records the covering tags every value of the scrutinee is offered, following
// default tails down from the top-level split.
//
// Leaving out a branch's continuation is what keeps the credit sound. Normalization copies
// the arms below a branch into its fallthrough, specialized against the tag that branch
// tested, so a covering branch inside one is offered only to values that already passed that
// tag. `match v { {a} if g => 0, {b} if g => 1, {a} => 2 }` puts a covering `{b}` branch
// inside the `{a}` branch's fallthrough, which no `{b: string}` value reaches.
//
// A guard's failure continuation stays on the walk. Whether a condition holds is no fact
// about the value's tag, so a value reaching the guard reaches that continuation with nothing
// yet passed.
func (w *coverageWalk) collect(term ucs.Norm) {
	for term != nil && !w.seen.Contains(term) {
		w.seen.Add(term)
		switch n := term.(type) {
		case *ucs.NormSplit:
			w.creditBranches(n)
			term = n.Default
		case *ucs.NormGuard:
			term = n.Default
		case *ucs.NormBind:
			term = n.Cont
		default:
			return
		}
	}
}

// creditBranches records the tag of every branch of split that covers what it tests. A split
// over a projection tests a value reached through the scrutinee, so its tags say nothing
// about which of the scrutinee's own values are covered.
func (w *coverageWalk) creditBranches(split *ucs.NormSplit) {
	if split.Scrutinee != w.root {
		return
	}
	for _, branch := range split.Branches {
		if branch.Test != nil && w.alwaysMatches(branch.Cont) {
			w.tags = append(w.tags, branch.Test)
		}
	}
}

// alwaysMatches reports whether control reaching term reaches an arm body however the tests
// below it turn out.
//
// This is where the guard rule lives. A false condition goes to the guard's own failure
// continuation, so a branch holding a guard covers its tag only when that continuation
// reaches a body too. A lone `{x} if b => x` does not.
//
// A bind names a value and tests nothing, so it matches whenever what runs below it does.
// That covers the `other` of `match p { other => 1 }`, and a bare `...rest` arm too, which
// binds every value. Such an arm is already reported unsupported by the pass that binds it.
func (w *coverageWalk) alwaysMatches(term ucs.Norm) bool {
	if term == nil {
		return false
	}
	if got, asked := w.matched[term]; asked {
		return got
	}
	// Seed the memo before recursing. The form has no cycle, so no caller reads the seed. It
	// bounds the recursion should a later rewrite introduce one.
	w.matched[term] = false
	got := false
	switch n := term.(type) {
	case *ucs.BodyLeaf:
		got = true
	case *ucs.NormBind:
		got = w.alwaysMatches(n.Cont)
	case *ucs.NormGuard:
		got = w.alwaysMatches(n.Cont) && w.alwaysMatches(n.Default)
	case *ucs.NormSplit:
		got = w.splitAlwaysMatches(n)
	case *ucs.EscapeLeaf, *ucs.FallbackLeaf:
		// The two leaves of a `val pat = init else { … }`. Its fallback runs precisely when the
		// pattern failed, so counting it as a body would make every such declaration cover its
		// scrutinee, and its escape leaf carries no body at all. `ucs.DesugarMatch` mints
		// neither.
	}
	w.matched[term] = got
	return got
}

// splitAlwaysMatches reports whether control reaching a split reaches an arm body. A value
// passes one branch's test and continues below it or fails every test and continues into the
// tail, so the split matches when all of those continuations do.
//
// A split over a projection gets a second reading, since the interim rules read a structural
// sub-pattern as irrefutable: `{a: {b}}` tests `{a}` on the scrutinee and then `{b}` on the
// projection `p.a`, and the second test is taken to hold. A projected literal, class, or
// extractor test is refutable and gets no such reading. Neither does a test of the root
// scrutinee, whose coverage depends on the exactness checkCondExhaustive reads off the type.
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

// checkCondExhaustive reports a NonExhaustiveMatchError when the arms of one conditional form
// leave a value of its scrutinee uncovered. shape is the scrutinee snapshot taken before any
// arm bound, which the union members and the structural exactness are read off. The message
// names the construct the top-level split lowered from rather than assuming a `match`.
//
// The inexact rule below is conservative, which is #1077. An object tag matches a value
// carrying extra fields, and a rest-relaxed tuple tag matches a longer one, so both cover an
// inexact scrutinee that this asks a catch-all for.
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
	// alias rather than the type it stands for. Expanding it first, the same unfold constrain
	// performs, is what lets `match c { Color.RGB(..) => .., Color.Hex(..) => .. }` over
	// `val c: Color` cover the variant union without a default arm.
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

// unionTagsExhaustive reports whether the covering tags cover a union scrutinee. An inexact
// union carries an open tail no tag names, so it takes the catch-all the caller has already
// ruled out. An exact one is covered when every member is.
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

// memberTagged reports whether some covering tag names a single union member, dispatching on
// the member's kind. Any kind with no arm below has no tag short of a catch-all, which the
// caller has already ruled out.
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

// litTagged reports whether some covering test is a literal equal to the literal member.
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

// atomTagged is the twin of litTagged for a `null` or `undefined` member. Neither atom
// carries a value to compare, so equalType decides the match.
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

// nominalTagged reports whether some covering test names the member's class, resolving the
// test's name through the type sort so `Color.RGB` covers the `Color.RGB` variant. An
// instance pattern lowers to a class test and an extractor pattern to an extractor test.
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

// structuralTagged reports whether some covering test is a shape the member carries, which is
// what lets the branch under that test destructure it.
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

// structuralTest reports whether a tag names an object or tuple shape. Those are the two the
// interim rules read as irrefutable below the level they test.
func structuralTest(test ucs.Test) bool {
	switch test.(type) {
	case *ucs.ObjectTest, *ucs.TupleTest:
		return true
	default:
		return false
	}
}
