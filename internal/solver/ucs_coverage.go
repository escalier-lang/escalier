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
// `residual = scrutinee ∧ ~covered ; exhaustive iff residual <: ⊥`.
//
// The check also collects witnesses. A witness is a type the form leaves uncovered, which a
// message names so it can ask for the branch that would cover it. A union scrutinee's
// witnesses are the members no branch covers. An exact object or tuple scrutinee has one
// witness, the whole scrutinee. Every witness is a type some arm can name, if only by
// annotation: `n: number => n` covers a member no shape tag reaches.
//
// An open tail is reported apart from the witnesses, since a catch-all is the only arm that
// reaches it. An inexact union carries both, so its message asks for a branch per uncovered
// member and a catch-all on top. An inexact object or tuple carries only the tail. Phase 2's
// residual is that witness set directly, and it reaches nested patterns the tag-level rules
// here cannot.

// coverage is what the check reads off one normalized form. Its three tag groups partition the
// branches of the root scrutinee. A branch lands in one of them by whether it covers the
// values its test names, and when it does not, by what stands in the way. That is what lets a
// diagnostic name the edit that would cover a witness rather than always asking for a
// catch-all.
type coverage struct {
	// catchAll reports whether a value failing every tag test at the root still reaches an
	// arm body. It is the IR's reading of an unguarded catch-all arm.
	catchAll bool
	// guardedCatchAll reports whether such a value would reach an arm body were the guards on
	// the way to hold. It is the reading of a catch-all arm carrying a guard, the sole arm of
	// `match p { q if b => 0 }`. Such an arm names every value and covers none.
	guardedCatchAll bool
	// covering holds the tags of the branches that cover what they test. A union member is
	// covered when one of them names it.
	covering tagGroup
	// guarded holds the tags of the branches that would cover what they test were their
	// guard's condition to hold. A value such a tag names still falls through, so the fix is
	// an unguarded branch of the same shape rather than a catch-all.
	guarded tagGroup
	// refutable holds the tags of the remaining branches. What blocks such a branch is a
	// sub-pattern that can fail, the `1` of `{x: 1}` or the `0` of `Color.RGB(0, g, b)`. The
	// fix is a branch that binds the same tag irrefutably.
	refutable tagGroup
}

// tagGroup is the tags of one group of branches, alongside the types the annotation tests
// among them resolve to. The two are kept apart because they decide coverage differently. An
// annotation names a type and can admit a member of any kind, where a shape tag dispatches on
// the member's kind.
type tagGroup struct {
	// tests holds every branch tag in the group.
	tests []ucs.Test
	// anns holds the resolved type of every AnnTest among tests, filled in by
	// checkCondExhaustive once it has a scope to resolve against. A value fitting one of them
	// is covered, which is what credits the arm `x: number => x` with the `number` member of a
	// `number | string` scrutinee.
	anns []soltype.Type
}

// coverageWalk reads the coverage of one normalized form. root is the scrutinee the
// top-level split tests, which tells a test of the whole value apart from one of a projection
// out of it.
type coverageWalk struct {
	root          *ucs.Scrutinee
	tags          []ucs.Test
	guardedTags   []ucs.Test
	refutableTags []ucs.Test
	// strict decides coverage under the rules the check enforces. blind reads every guard's
	// condition as holding. The two disagree on exactly those branches a guard is the sole
	// reason to leave uncredited.
	strict *armReach
	blind  *armReach
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
		root:   split.Scrutinee,
		strict: newArmReach(split.Scrutinee, false),
		blind:  newArmReach(split.Scrutinee, true),
		seen:   set.NewSet[ucs.Norm](),
	}
	catchAll := w.strict.reaches(split.Default)
	w.collect(split)
	return coverage{
		catchAll:        catchAll,
		guardedCatchAll: !catchAll && w.blind.reaches(split.Default),
		covering:        tagGroup{tests: w.tags},
		guarded:         tagGroup{tests: w.guardedTags},
		refutable:       tagGroup{tests: w.refutableTags},
	}
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

// creditBranches sorts the branches of split by whether each covers what it tests, and when
// it does not, by what stands in the way. A split over a projection tests a value reached
// through the scrutinee, so its tags say nothing about which of the scrutinee's own values
// are covered.
func (w *coverageWalk) creditBranches(split *ucs.NormSplit) {
	if split.Scrutinee != w.root {
		return
	}
	for _, branch := range split.Branches {
		if branch.Test == nil {
			continue
		}
		switch {
		case w.strict.reaches(branch.Cont):
			w.tags = append(w.tags, branch.Test)
		case w.blind.reaches(branch.Cont):
			w.guardedTags = append(w.guardedTags, branch.Test)
		default:
			w.refutableTags = append(w.refutableTags, branch.Test)
		}
	}
}

// armReach decides whether control reaching a term reaches an arm body however the tests
// below it turn out. Two readings run over one normalized form.
//
// The strict reading is the coverage rule. A guard's false condition goes to the guard's own
// failure continuation, so a branch holding a guard covers its tag only when that
// continuation reaches a body too. A lone `{x} if b => x` does not.
//
// The guard-blind reading takes every condition to hold. Comparing the two says whether a
// branch would have covered its tag but for a guard, which is a different fix from a branch
// whose own pattern can fail.
type armReach struct {
	// root is the scrutinee the top-level split tests, which tells a test of the whole value
	// apart from one of a projection out of it.
	root *ucs.Scrutinee
	// ignoreGuards selects the guard-blind reading.
	ignoreGuards bool
	// memo records the verdict per term, since a tail is asked about once per branch falling
	// into it.
	memo map[ucs.Norm]bool
}

func newArmReach(root *ucs.Scrutinee, ignoreGuards bool) *armReach {
	return &armReach{root: root, ignoreGuards: ignoreGuards, memo: map[ucs.Norm]bool{}}
}

// reaches reports whether control reaching term reaches an arm body.
//
// A bind names a value and tests nothing, so it matches whenever what runs below it does.
// That covers the `other` of `match p { other => 1 }`, and a bare `...rest` arm too, which
// binds every value. Such an arm is already reported unsupported by the pass that binds it.
func (r *armReach) reaches(term ucs.Norm) bool {
	if term == nil {
		return false
	}
	if got, asked := r.memo[term]; asked {
		return got
	}
	// Seed the memo before recursing. The form has no cycle, so no caller reads the seed. It
	// bounds the recursion should a later rewrite introduce one.
	r.memo[term] = false
	got := false
	switch n := term.(type) {
	case *ucs.BodyLeaf:
		got = true
	case *ucs.NormBind:
		got = r.reaches(n.Cont)
	case *ucs.NormGuard:
		got = r.reaches(n.Cont) && (r.ignoreGuards || r.reaches(n.Default))
	case *ucs.NormSplit:
		got = r.splitReaches(n)
	case *ucs.EscapeLeaf, *ucs.FallbackLeaf:
		// The two leaves of a `val pat = init else { … }`. Its fallback runs precisely when the
		// pattern failed, so counting it as a body would make every such declaration cover its
		// scrutinee, and its escape leaf carries no body at all. `ucs.DesugarMatch` mints
		// neither.
	}
	r.memo[term] = got
	return got
}

// splitReaches reports whether control reaching a split reaches an arm body. A value passes
// one branch's test and continues below it or fails every test and continues into the tail,
// so the split matches when all of those continuations do.
//
// A split over a projection gets a second reading, since the interim rules read a structural
// sub-pattern as irrefutable: `{a: {b}}` tests `{a}` on the scrutinee and then `{b}` on the
// projection `p.a`, and the second test is taken to hold. A projected literal, class, or
// extractor test is refutable and gets no such reading. Neither does a test of the root
// scrutinee, whose coverage depends on the exactness checkCondExhaustive reads off the type.
func (r *armReach) splitReaches(split *ucs.NormSplit) bool {
	if r.reaches(split.Default) && r.branchesReach(split) {
		return true
	}
	if split.Scrutinee == r.root || len(split.Branches) != 1 {
		return false
	}
	branch := split.Branches[0]
	return structuralTest(branch.Test) && r.reaches(branch.Cont)
}

// branchesReach reports whether every branch of a split reaches an arm body once its test
// matched.
func (r *armReach) branchesReach(split *ucs.NormSplit) bool {
	for _, branch := range split.Branches {
		if !r.reaches(branch.Cont) {
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
func (c *checker) checkCondExhaustive(scope *Scope, lvl int, norm ucs.Norm, shape soltype.Type) {
	split, isSplit := norm.(*ucs.NormSplit)
	if !isSplit {
		return
	}
	cov := readCoverage(split)
	if cov.catchAll {
		return
	}
	c.resolveAnnTags(scope, lvl, &cov)
	// A transparent alias scrutinee, an enum handle or a user `type` reference, carries the
	// alias rather than the type it stands for. Expanding it first, the same unfold constrain
	// performs, is what lets `match c { Color.RGB(..) => .., Color.Hex(..) => .. }` over
	// `val c: Color` cover the variant union without a default arm.
	carrier := c.expandAliasChain(soltype.CarrierOf(shape))
	// An annotation admitting the whole scrutinee covers every value, exactly as an unguarded
	// catch-all arm does. `match u { x: number | string => x }` over a `number | string` needs
	// no arm below it. Asking about the whole carrier is what covers a scrutinee that is no
	// union, since the per-member rule below never runs for one.
	if c.annAdmits(carrier, cov.covering) {
		return
	}
	if u, isUnion := carrier.(*soltype.UnionType); isUnion {
		uncovered := c.uncoveredMembers(scope, u, cov)
		// A closed union is exhaustive once its members are covered. Openness is carried by a
		// primitive scrutinee instead, whose catch-all requirement the infiniteInhabitants rule
		// below handles.
		if uncovered.empty() {
			return
		}
		c.report(uncovered.errorAt(split.Origin))
		return
	}
	inexact, isStructural := structuralInexact(carrier)
	if !isStructural {
		// A carrier admitting infinitely many values, a bare `number` or `string` or
		// `unknown`, is covered by no finite set of value patterns, so it needs a catch-all
		// arm exactly as an inexact union does. The whole carrier is the open tail no arm
		// names, so it is the witness the diagnostic reports.
		if infiniteInhabitants(carrier) {
			c.report(&NonExhaustiveMatchError{Origin: split.Origin, OpenTail: carrier})
		}
		return
	}
	// An exact object or tuple is covered by a branch that destructures its shape. An inexact
	// one carries an open tail of values no such branch can see, so only a catch-all covers it.
	// Its shape is no witness to report alongside, since a branch naming that shape is exactly
	// what the interim rule refuses to credit.
	if inexact {
		c.report(&NonExhaustiveMatchError{Origin: split.Origin, OpenTail: carrier})
		return
	}
	if hasStructuralTag(cov.covering.tests) {
		return
	}
	// The whole scrutinee is the witness, since a structural scrutinee has no members to
	// single out. The expanded carrier stands in for it rather than the alias name a
	// `type P = {x: number}` annotation writes. That shows the fields a covering pattern
	// names, which the alias name would hide.
	var uncovered uncoveredWitnesses
	uncovered.add(carrier,
		hasStructuralTag(cov.guarded.tests) || cov.guardedCatchAll,
		hasStructuralTag(cov.refutable.tests))
	c.report(uncovered.errorAt(split.Origin))
}

// uncoveredWitnesses holds the values a form leaves uncovered, grouped by the fix each one
// calls for. A diagnostic reads the groups to name that fix.
type uncoveredWitnesses struct {
	// unmatched holds the values no branch tests for at all.
	unmatched []soltype.Type
	// guarded holds the values whose only matching branches carry a guard that can fail.
	guarded []soltype.Type
	// refutable holds the values whose only matching branches nest a sub-pattern that can
	// fail, such as the `1` of `{x: 1}`.
	refutable []soltype.Type
}

// add records t under the reason no branch covers it. A branch naming t that cannot run for
// its guard is one reason, and a branch whose own pattern can fail is another. The two ask
// for different edits, so they are kept apart. Nothing naming t at all is the remaining case.
func (u *uncoveredWitnesses) add(t soltype.Type, guarded, refutable bool) {
	switch {
	case guarded:
		u.guarded = append(u.guarded, t)
	case refutable:
		u.refutable = append(u.refutable, t)
	default:
		u.unmatched = append(u.unmatched, t)
	}
}

func (u *uncoveredWitnesses) empty() bool {
	return len(u.unmatched) == 0 && len(u.guarded) == 0 && len(u.refutable) == 0
}

// errorAt builds the diagnostic these witnesses describe, blamed on the construct that origin
// names.
func (u *uncoveredWitnesses) errorAt(origin ucs.Origin) *NonExhaustiveMatchError {
	return &NonExhaustiveMatchError{
		Origin:    origin,
		Unmatched: u.unmatched,
		Guarded:   u.guarded,
		Refutable: u.refutable,
	}
}

// uncoveredMembers collects the members of an exact union scrutinee that no branch covers,
// each under the reason it is uncovered. A member a covering tag names is left out, so an
// exhaustive form yields no witness at all.
func (c *checker) uncoveredMembers(scope *Scope, u *soltype.UnionType, cov coverage) uncoveredWitnesses {
	var uncovered uncoveredWitnesses
	for _, member := range u.Types {
		if c.memberTagged(scope, member, cov.covering) {
			continue
		}
		// A guarded catch-all arm names every value, so it is what leaves this member
		// uncovered whenever no tag of its own does.
		uncovered.add(member,
			c.memberTagged(scope, member, cov.guarded) || cov.guardedCatchAll,
			c.memberTagged(scope, member, cov.refutable))
	}
	return uncovered
}

// memberTagged reports whether some tag in group names a single union member. An annotation
// test names a type rather than a shape, so it can cover a member of any kind and is asked
// before the dispatch. The remaining tags each name a shape, so they dispatch on the member's
// kind. A member of any other kind is named by no shape tag, only by an annotation test or a
// catch-all.
func (c *checker) memberTagged(scope *Scope, member soltype.Type, group tagGroup) bool {
	if c.annAdmits(member, group) {
		return true
	}
	switch m := member.(type) {
	case *soltype.LitType:
		return c.litTagged(m, group.tests)
	case *soltype.NullType, *soltype.UndefinedType:
		return atomTagged(member, group.tests)
	case *soltype.ClassType:
		return c.nominalTagged(scope, m, group.tests)
	case *soltype.ObjectType, *soltype.TupleType:
		return structuralTagged(member, group.tests)
	default:
		return false
	}
}

// resolveAnnTags resolves the annotation of every annotation test in each of cov's groups, so
// a later coverage question can ask what those types admit. An arm such as `x: number => x`
// contributes one.
//
// Resolution runs under a discarded probe. The typing walk resolved each of these annotations
// already, through bindNarrowedIdent, and reported whatever it could not support. Resolving a
// second time here would repeat that diagnostic.
func (c *checker) resolveAnnTags(scope *Scope, lvl int, cov *coverage) {
	p := c.openProbe()
	for _, group := range []*tagGroup{&cov.covering, &cov.guarded, &cov.refutable} {
		group.anns = c.annTypes(scope, lvl, group.tests)
	}
	c.closeProbe(p, false)
}

// annTypes resolves the annotation of every annotation test among tags, dropping the ones that
// name no type. Its caller opens the probe the resolution runs under.
func (c *checker) annTypes(scope *Scope, lvl int, tags []ucs.Test) []soltype.Type {
	var anns []soltype.Type
	for _, tag := range tags {
		test, isAnn := tag.(*ucs.AnnTest)
		if !isAnn {
			continue
		}
		if t, resolved := c.resolveTypeAnn(scope, test.Ann, lvl); resolved {
			anns = append(anns, t)
		}
	}
	return anns
}

// annAdmits reports whether some annotation test in group accepts every value of t, so the arm
// making that test covers t. The `number` of `x: number => x` admits the `number` member of a
// `number | string` scrutinee and no other member.
//
// It asks typeAdmits, the same predicate bindNarrowedIdent asks to decide whether an arm's
// annotation is reachable at all, so one rule settles which members an annotation matches.
// What that arm's name binds at is a separate question, answered by the annotation itself.
func (c *checker) annAdmits(t soltype.Type, group tagGroup) bool {
	for _, ann := range group.anns {
		if c.typeAdmits(ann, t) {
			return true
		}
	}
	return false
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

// hasStructuralTag reports whether some test in tags is an object or tuple shape. Such a test
// in the covering list is what covers an exact structural scrutinee. One in the guarded or the
// refutable list instead says which of those two reasons left the scrutinee out.
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
