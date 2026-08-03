package solver

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// A type is regular when it has finitely many distinct subtrees. Every regular type is denoted
// exactly by a finite μ-knot, and constrain compares two knots by unfolding them under its
// coinductive seen-set. Nothing builds a knot for a recursive alias, though. The alias name serves
// as the μ-variable and unfolding ties the knot, which works as long as the unfolding comes back to
// an instantiation it already visited.
//
// `type List<T> = {head: T, tail?: List<T>}` does come back. `List<number>` unfolds to a body that
// names `List<number>` again, and the seen-set closes on the repeated pair. An alias whose recursion
// grows its own argument never comes back:
//
//	type H<T> = {a: keyof T, b: H<{c: T}>}
//
// `H<number>` unfolds to `{a: never, b: H<{c: number}>}`, then to `{a: "c", b: H<{c: {c: number}}>}`,
// and the argument gains a level every lap, so no pair ever repeats. The tree is regular all the
// same. `keyof {c: X}` is `"c"` whatever X is, so every lap below the first emits `{a: "c", b: …}`
// and the whole tree has two distinct subtrees. muKnotFor finds that knot, and evalTypeOperator
// hands constrain `μX0.{a: "c", b: X0}` in place of the `H<{c: number}>` reference.
//
// # What proves a knot
//
// The proof rests on one observation. Expand the alias with a rigid skolem in place of its type
// argument and reduce. If the skolem is nowhere in the result, the reduction never read the
// argument, so it produces that one body for every argument and one symbolic expansion settles
// infinitely many instantiations at once.
//
// Write `A<T>` for the alias and `g` for the argument its recursive reference passes, so `H`'s `g`
// maps `T` to `{c: T}`. The probe runs the check one level into the recursion rather than at the
// alias's own parameters, since `H`'s first level does read `T`. It expands `A<g(S)>` for a fresh
// skolem `S` and asks two things of the result:
//
//  1. Its shape — the body with every reference back to `A` abstracted to the μ-binder — holds no
//     skolem. Every `A<g(σ)>` therefore has that one shape, whatever σ is.
//  2. Its recursive reference passes `g(g(S))`, so the state below `A<g(σ)>` is `A<g(g(σ))>`, which
//     is again of the form `A<g(·)>`.
//
// Together those close the family `{A<g(σ)> : σ}` under the successor relation and give every member
// of it the same shape Σ. All of them are therefore the same tree, and that tree is `μX.Σ`.
//
// An alias with several recursive references, or with several type parameters, is handled the same
// way with `g` ranging over one argument pattern per reference. Condition 1 is asked of each
// pattern's expansion and every shape must agree, and condition 2 asks that the references below
// `A<g_i(S)>` pass exactly the patterns `g_1(g_i(S)), …, g_m(g_i(S))`.
//
// # What is left to the budgets
//
// The check is sound and incomplete. It proves a knot for an alias whose argument stops mattering at
// a bounded depth, and it proves nothing for one whose argument keeps mattering forever.
// `type Nest<T> = {here: T, deeper: Nest<{b: T}>}` reads `T` at every level, so its probe shape holds
// the skolem and no knot is found. That alias denotes a genuinely non-regular tree, which no finite
// knot can represent, and maxExpandDepth, maxExpandKeyChars, and maxUnwrapDepth remain the backstop
// for it. checkProductive's own doc comment describes the same split between what is decidable here
// and what a budget has to cut off.

// uniformKnot is one alias's memoized answer from muKnotFor: the μ-knot every instantiation one
// level into its recursion denotes, and that knot's body rendered under PrintQualified. An alias
// whose probe failed any of the conditions above memoizes a zero value, whose nil knot reads as "no
// knot found" at every reference to it.
//
// The knot is shared rather than rebuilt per reference, so the constraint between two references to
// one alias keys on a single pointer and constrain's seen-set closes on it.
type uniformKnot struct {
	knot *soltype.RecursiveType
	// shape is PrintQualified(knot.Body). A reference is admitted as this knot only when its own
	// expansion renders identically. That test is what keeps a reference a lap above the knot from
	// being tied to it. `H<number>` emits `{a: never, b: …}` at its own level, which is not the
	// `{a: "c", b: …}` every level below it emits.
	shape string
}

// muKnotFor returns the μ-knot a reference denotes, or nil when the reference is not one the
// regular-tree check settles. evalTypeOperator calls it in place of expandAlias, so a settled
// reference reaches constrain as a knot to unfold rather than as an alias to expand again.
//
// The level is reduced under a fresh seen-set rather than the enclosing constraint's. A conditional
// decides its branch by re-entering constrain, which can close on a pair the enclosing derivation
// assumed, so sharing that set would let the surrounding constraint decide whether this reference
// is admitted as the knot. The probe that proved the knot also used a fresh set, so this is what
// makes the two shapes comparable.
func (c *Context) muKnotFor(ref *soltype.AliasType) *soltype.RecursiveType {
	if c.knotting {
		// A level's own reduction re-entered constrain, which asks again for the alias being
		// expanded. Answering it would start a second level walk inside the first and recur without
		// bound, so the inner ask falls back to the plain expansion.
		return nil
	}
	u := c.uniformKnotOf(ref.Name)
	if u.knot == nil {
		return nil
	}
	c.knotting = true
	defer func() { c.knotting = false }()
	body, _, ok := c.knotLevel(ref, u.knot.Binder)
	if !ok || containsUnreducedOp(body) || soltype.PrintQualified(body) != u.shape {
		// This reference sits above the level at which the argument stopped mattering, so its own
		// body is not the knot's. Expanding it normally walks one lap closer, and the reference the
		// lap emits is asked again.
		return nil
	}
	return u.knot
}

// uniformKnotOf memoizes probeUniformKnot per alias. The zero value doubles as the in-progress
// marker: a reduction inside the probe can re-enter constrain, which asks for the same alias again,
// and reading the marker declines that inner ask rather than restarting the probe.
//
// An alias whose body has not been resolved yet is answered without being memoized. Its expansion is
// the ErrorType sentinel, which no knot can be read off, and memoizing that would pin the answer for
// a definition that is about to be filled in.
func (c *Context) uniformKnotOf(name string) *uniformKnot {
	if u, done := c.uniformKnots[name]; done {
		return u
	}
	def, registered := c.aliasDef(name)
	if !registered || def.Body == nil {
		return &uniformKnot{}
	}
	if c.uniformKnots == nil {
		c.uniformKnots = map[string]*uniformKnot{}
	}
	c.uniformKnots[name] = &uniformKnot{}
	outer := c.knotting
	c.knotting = true
	defer func() { c.knotting = outer }()
	u := c.probeUniformKnot(name, def)
	c.uniformKnots[name] = u
	return u
}

// probeUniformKnot runs the two-condition check this file's doc comment describes and returns the
// knot it proves, or a zero uniformKnot when it proves none.
func (c *Context) probeUniformKnot(name string, def *AliasDef) *uniformKnot {
	none := &uniformKnot{}
	if def.NotProductive || len(def.TypeParams) == 0 {
		// A non-generic alias has no argument to grow, so its instantiations repeat and the seen-set
		// already closes on them. A rejected definition names no type to normalize.
		return none
	}

	skolems := make([]soltype.Type, len(def.TypeParams))
	probeIDs := set.NewSet[int]()
	for i, p := range def.TypeParams {
		s := c.freshSkolem(p.Name)
		skolems[i] = s
		probeIDs.Add(s.ID)
	}
	probe := &soltype.AliasType{Name: name, TypeArgs: skolems}
	binder := c.freshMuBinder()

	// The references the alias's own body emits are the argument patterns g_1 … g_m.
	_, patterns, ok := c.knotLevel(probe, binder)
	if !ok || len(patterns) == 0 {
		return none
	}
	probeKey := soltype.PrintQualified(probe)
	if !slices.ContainsFunc(patterns, func(p *soltype.AliasType) bool {
		return soltype.PrintQualified(p) != probeKey
	}) {
		// Every reference passes the alias's own arguments straight through, so the instantiation
		// repeats on the next lap and constrain's seen-set closes on it with no knot. Declining here
		// keeps such an alias rendering and comparing the way it does without this check.
		return none
	}

	var shape string
	var body soltype.Type
	for _, pattern := range patterns {
		next, below, ok := c.knotLevel(pattern, binder)
		if !ok || mentionsAnySkolem(next, probeIDs) {
			// Condition 1: the argument still reaches the emitted body, so this alias's instantiations
			// denote different trees and none of them has a finite knot the probe can name.
			return none
		}
		if containsUnreducedOp(next) {
			// An operator survived the reduction with the recursive reference already abstracted away
			// beneath it. `type Sp<T> = {a: keyof T, b: {...Sp<{c: T}>}}` abstracts to
			// `{a: "c", b: {...X0}}`. A knot does exist for such an alias, since spreading one operand
			// into an otherwise empty object yields that operand, so declining is incompleteness
			// rather than a shape no knot fits. What forces it is that reduction has no rule for
			// grounding a spread whose operand is a μ-variable: the residual spread would reach
			// constrain and be compared structurally against a real object, failing a comparison that
			// should hold. Declining leaves the alias on the plain expansion instead.
			return none
		}
		if !sameInstantiations(below, applyPatterns(patterns, skolems, pattern.TypeArgs)) {
			// Condition 2: the references below this one pass something other than the alias's own
			// patterns applied again, so the family the probe closed over is not closed after all.
			return none
		}
		rendered := soltype.PrintQualified(next)
		if body == nil {
			body, shape = next, rendered
			continue
		}
		if rendered != shape {
			// Two patterns emit different bodies, so the tree below this alias depends on which
			// reference was taken and one knot cannot stand for both.
			return none
		}
	}
	if emitsNoStructure(body, binder) {
		return none
	}
	return &uniformKnot{knot: &soltype.RecursiveType{Binder: binder, Body: body}, shape: shape}
}

// emitsNoStructure reports whether a candidate knot body puts no type constructor between the knot
// and its own binder. `μX0.X0` is that shape, and so is `μX0.Id<X0>` over `type Id<X> = X`, since a
// transparent alias unfolds to whatever it was handed. Such a knot has no unfolding that mentions a
// type, so constrain would close on it against any super at all and accept vacuously. coalesce's tie
// carries the same guard for the knots it mints.
//
// A bare alias reference is rejected without asking what it expands to. A knot whose whole body is
// one reference emits nothing of its own either way, and the referenced alias is asked for its own
// knot at its own reference.
func emitsNoStructure(body soltype.Type, binder *soltype.RecursiveVarType) bool {
	if body == soltype.Type(binder) {
		return true
	}
	_, bare := body.(*soltype.AliasType)
	return bare
}

// knotLevel expands one alias reference and returns the μ-body that level emits, together with the
// references back to the same alias that the body carried before they were abstracted away.
//
// It runs two walks. The first reduces every operator the substitution left behind, so a
// `keyof {c: number}` becomes the `"c"` it stands for and two levels that denote one type render
// alike. The second replaces each remaining reference back to the alias with the knot's binder. The
// order matters: an operator can have a reference to the alias as its own operand, as the
// `Grow<{a: T}>[K]` of `type Grow<T> = {[K]: Grow<{a: T}>[K] for K in keyof T}` does. Abstracting
// first would hand that operator a bare μ-variable to read a member out of, which is not a type any
// reduction rule is written for.
//
// It reports ok=false in two cases, each one saying that what came back is not the node this level
// emits:
//
//   - the reference's definition has no resolved body, so the expansion is the ErrorType sentinel;
//   - an expansion budget cut the reduction off, so the result is a truncation. Rendering one is
//     expensive as well as meaningless, since a truncation can be arbitrarily large.
//
// A reduction that reports a diagnostic is not one of them. Such a reduction already substituted the
// ErrorType sentinel for the member it could not work out, so the level it emits is a real node with
// `error` at that position, and `error` absorbs at every constraint site. The diagnostic itself is
// carried by the plain expansion, which every reference runs before this walk is consulted, so the
// errors collected here are discarded rather than reported twice. `{x: number}["z"]` inside an alias
// body therefore yields the knot `μX0.{…, e: error, …}` and one report, not a knot-less comparison
// that runs to the unwrap budget.
//
// A shape that still carries an operator is not rejected here either, since the probe's first walk is
// over the alias's own parameters and its shape is expected to hold one. Every caller that uses a
// shape as a knot body screens it with containsUnreducedOp instead.
func (c *Context) knotLevel(ref *soltype.AliasType, binder *soltype.RecursiveVarType) (soltype.Type, []*soltype.AliasType, bool) {
	expanded := c.expandAlias(ref)
	if _, unresolved := expanded.(*soltype.ErrorType); unresolved {
		return nil, nil, false
	}
	r := &levelReducer{eval: newTypeEvaluator(c, newSeenPairs()), name: ref.Name}
	reduced := expanded.Accept(r, soltype.Positive)
	if r.eval.truncated {
		return nil, nil, false
	}
	a := &recursiveRefAbstractor{name: ref.Name, binder: binder}
	return reduced.Accept(a, soltype.Positive), a.found, true
}

// levelReducer reduces every operator in one level of an alias's unfolding. It leaves a reference
// back to the alias itself in place, and does not walk that reference's arguments, since those are
// the growing payload the knot abstracts away.
type levelReducer struct {
	eval *typeEvaluator
	name string
}

func (r *levelReducer) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if ref, ok := t.(*soltype.AliasType); ok && ref.Name == r.name {
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

// ExitType reduces bottom-up, so an operator whose operand is itself an operator reads the reduced
// form. The evaluator is shared across the walk, which gives one expansion budget to the whole level
// rather than one per node.
func (r *levelReducer) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type {
	if !unreducedOp(t) {
		return t
	}
	return r.eval.reduce(t)
}

// unreducedOp reports whether a node still stands for a value reduction has to work out. It is
// isResidualOp widened by the two spread cases. isResidualOp's object arm counts only an unsettled
// mapped member and its tuple arm counts none, since constrain compares a spread-carrying object or
// tuple structurally rather than as an operator. A knot's body instead has to be the object or tuple
// the spread merges to, so both are reduced here.
func unreducedOp(t soltype.Type) bool {
	return isResidualOp(t) || objectIsResidual(t) || tupleHasSpread(t)
}

// containsUnreducedOp reports whether any node in t is one reduction did not finish with. A shape
// carrying one is not the node its level emits, so no knot can be read off it.
func containsUnreducedOp(t soltype.Type) bool {
	f := &unreducedOpFinder{}
	t.Accept(f, soltype.Positive)
	return f.found
}

type unreducedOpFinder struct{ found bool }

func (f *unreducedOpFinder) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if f.found {
		return soltype.EnterResult{SkipChildren: true}
	}
	if unreducedOp(t) {
		f.found = true
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (f *unreducedOpFinder) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// recursiveRefAbstractor replaces each reference back to the walked alias with the knot's binder and
// records the references it replaced, in traversal order. probeUniformKnot reads the alias's
// argument patterns off that record.
type recursiveRefAbstractor struct {
	name   string
	binder *soltype.RecursiveVarType
	found  []*soltype.AliasType
}

// EnterType takes over at a reference back to the walked alias. Skipping its children is what keeps
// the walk out of the growing argument.
func (a *recursiveRefAbstractor) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	ref, ok := t.(*soltype.AliasType)
	if !ok || ref.Name != a.name {
		return soltype.EnterResult{}
	}
	a.found = append(a.found, ref)
	return soltype.EnterResult{Type: a.binder, SkipChildren: true}
}

func (a *recursiveRefAbstractor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// applyPatterns substitutes args for the probe skolems in each of the alias's own argument patterns,
// yielding the references that must sit one level below a state whose arguments are args. It is the
// `g_1(g_i(S)), …, g_m(g_i(S))` of condition 2.
func applyPatterns(patterns []*soltype.AliasType, skolems []soltype.Type, args []soltype.Type) []*soltype.AliasType {
	subst := map[int]soltype.Type{}
	for i, s := range skolems {
		if sk, ok := s.(*soltype.SkolemType); ok && i < len(args) {
			subst[sk.ID] = args[i]
		}
	}
	out := make([]*soltype.AliasType, 0, len(patterns))
	for _, p := range patterns {
		applied, ok := p.Accept(&skolemSubst{subst: subst}, soltype.Positive).(*soltype.AliasType)
		if !ok {
			continue
		}
		out = append(out, applied)
	}
	return out
}

// sameInstantiations reports whether two reference lists name the same multiset of instantiations,
// compared by their rendered form. Order is not required to match, since a reduction may emit the
// references of one level in a different order than the level above.
func sameInstantiations(got, want []*soltype.AliasType) bool {
	if len(got) != len(want) {
		return false
	}
	render := func(refs []*soltype.AliasType) []string {
		out := make([]string, len(refs))
		for i, r := range refs {
			out[i] = soltype.PrintQualified(r)
		}
		slices.Sort(out)
		return out
	}
	return slices.Equal(render(got), render(want))
}

// skolemSubst replaces the probe skolems by the types a concrete state passes in their place.
type skolemSubst struct {
	subst map[int]soltype.Type
}

func (s *skolemSubst) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	sk, ok := t.(*soltype.SkolemType)
	if !ok {
		return soltype.EnterResult{}
	}
	if replacement, found := s.subst[sk.ID]; found {
		return soltype.EnterResult{Type: replacement, SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (s *skolemSubst) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// mentionsAnySkolem reports whether t holds any of the probe's skolems, which is what says the
// reduction read the alias's argument instead of producing the same body for every argument.
func mentionsAnySkolem(t soltype.Type, ids set.Set[int]) bool {
	f := &skolemFinder{ids: ids}
	t.Accept(f, soltype.Positive)
	return f.found
}

// skolemFinder is the read-only walking visitor behind mentionsAnySkolem. It rewrites nothing, and
// prunes once it has seen one of the ids, since one occurrence is enough — the same shape
// soltype's typeVarSeeker uses.
type skolemFinder struct {
	ids   set.Set[int]
	found bool
}

func (f *skolemFinder) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if f.found {
		return soltype.EnterResult{SkipChildren: true}
	}
	if sk, ok := t.(*soltype.SkolemType); ok && f.ids.Contains(sk.ID) {
		f.found = true
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (f *skolemFinder) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }
