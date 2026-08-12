package solver

import (
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// The normal-form layer of constraint solving, MLstruct's `constrainNF` /
// `annoying`. It decides a constraint whose Boolean structure the deterministic
// "for all" rules in constrain.go cannot take apart: a union in supertype
// position, an intersection in subtype position, and a negation anywhere.
//
// # What normalization buys
//
// Both operands are pushed into a normal form first, so the decision runs on
// FUSED atoms rather than on the members the source wrote. An atom is a single
// structural type the Boolean algebra does not take apart, such as one object
// type or one arrow. normal.go fuses two atoms whenever a single atom denotes
// their meet or their join exactly, so
//
//	((x: number) -> boolean) & ((x: string) -> boolean)
//
// reaches this layer as the one arrow `(x: number | string) -> boolean`. Deciding
// that against `(x: number | string) -> boolean` is then an ordinary arrow
// comparison. Trialling the two written arms one at a time, which is what a
// member-by-member search does, rejects the same constraint, because neither arm
// alone accepts a string. constrain_nf_test.go states the verdicts this layer is
// measured against, derived by hand from a types-as-values reading.
//
// # The shape of one goal
//
// `sub <: super` becomes `DNF(sub) <: CNF(super)`. A DNF is a union of conjuncts
// and a CNF an intersection of disjuncts, so the constraint holds exactly when
// every conjunct is below every disjunct. That product is the deterministic
// decomposition: no member is guessed, and each implied goal is settled on its
// own.
//
// One implied goal reads `Lc ∩ ⋂Vc ∩ ¬Rc ∩ ⋂¬Nc <: Rd ∪ ⋃Vd ∪ ¬Ld ∪ ⋃¬Nd`. Every
// negated part moves to the other side of the `<:`, which is where negation
// enters the decision without any node having to be guessed:
//
//   - `X ∩ ¬R <: Y` is `X <: Y ∪ R`, so a conjunct's negated part joins the
//     supertype side.
//   - `X <: Y ∪ ¬L` is `X ∩ L <: Y`, so a disjunct's negated part joins the
//     subtype side.
//
// The two variable sets move the same way. What is left is a meet of atoms and
// variables against a join of atoms and variables, which decideMeetJoin settles.
//
// # Where a choice still remains
//
// A meet of two atoms that did not fuse, or a join of two that did not, leaves a
// genuine choice: `{x: number} | {y: number}` is not one record, so nothing says
// which side a given subtype has to satisfy. decideMeetJoin trials the pairs in
// specificity order for exactly that residue. Every trial runs over atoms the
// normal form produced, so the fusions above have already collapsed the cases a
// member-by-member search decides wrongly.

// nfDecision is what one constrainNF call derived.
//
// committed names the supertype-side atom or variable a trial settled on, and is
// nil when no trial ran or when every trial failed. The union-super rule reads it
// to report an ambiguous commit and to record that a bare variable member was
// pinned by a union choice.
type nfDecision struct {
	errs      []SolverError
	committed soltype.Type
}

// constrainNF decides `sub <: super` through the normal forms of both operands.
//
// The whole derivation runs under one probe, so a constraint that fails records
// no bound. That is the discipline the member-by-member trials it replaces
// followed, and callers depend on it: the union-super rule reports one
// union-level diagnostic in place of whatever the decomposition produced, and a
// bound recorded on the way to that failure would outlive the derivation that
// justified it.
func (c *Context) constrainNF(sub, super soltype.Type, seen *seenPairs, mutCtx bool) nfDecision {
	lhs := c.mkDeepDNF(sub, soltype.Positive)
	rhs := c.mkDeepCNF(super, soltype.Negative)

	p := newProbe(c, c.probe)
	c.probe = p
	var out nfDecision
	for _, conj := range lhs.Conjuncts {
		for _, disj := range rhs.Disjuncts {
			implied := c.constrainImplied(conj, disj, sub, super, seen, mutCtx)
			out.errs = append(out.errs, implied.errs...)
			if implied.committed != nil {
				out.committed = implied.committed
			}
		}
	}
	c.probe = p.parent
	if hasHardError(out.errs) {
		p.Discard()
	} else {
		p.Commit()
	}
	return out
}

// constrainImplied decides one conjunct of the subtype's DNF against one disjunct
// of the supertype's CNF, the goal this file's header calls an implied goal. It
// moves both negated parts across the `<:` and hands the resulting meet and join
// to decideMeetJoin.
//
// sub and super are the operands constrainNF was called with. They name the
// failure when the goal has no pair to trial at all.
func (c *Context) constrainImplied(
	conj Conjunct, disj Disjunct, sub, super soltype.Type, seen *seenPairs, mutCtx bool,
) nfDecision {
	// The two positive parts meet on the subtype side and the two negated ones join
	// on the supertype side. Each list was fused within its own normal form, so the
	// pooled list is fused once more: an atom of the conjunct may fuse with one the
	// disjunct contributed.
	meet, inhabited := c.fuseAtoms(pooled(conj.Lnf.Atoms, disj.Lnf.Atoms), meetOfAtoms)
	if !inhabited {
		// No value inhabits the subtype side, so the goal holds however the supertype
		// side reads. `5 <: ¬number` reaches this: the negated `number` moves to the
		// subtype side, and `5 ∩ number` is `never`.
		return nfDecision{}
	}
	join, _ := c.fuseAtoms(pooled(conj.Rnf.Atoms, disj.Rnf.Atoms), joinOfAtoms)

	// A variable is opaque, so it joins the side it sits on as one more candidate
	// rather than being taken apart. A variable negated on one side stands on the
	// other, by the same two rewrites the negated structural parts follow.
	subCands := append(meet, varsAsTypes(conj.Vars)...)
	subCands = append(subCands, varsAsTypes(disj.NVars)...)
	superCands := append(join, varsAsTypes(disj.Vars)...)
	superCands = append(superCands, varsAsTypes(conj.NVars)...)

	if len(subCands) == 0 {
		// An empty meet is `unknown`, the top of the lattice. Naming it explicitly
		// gives the decision below something to constrain, and `unknown <: T` still
		// records a bound when the supertype side is a variable.
		subCands = []soltype.Type{&soltype.UnknownType{}}
	}
	return c.decideMeetJoin(subCands, superCands, sub, super, seen, mutCtx)
}

// decideMeetJoin decides `⋂subCands <: ⋃superCands`, where each candidate is an
// atom or a type variable.
//
// It proves the goal by finding one subtype candidate that is below one supertype
// candidate, since `⋂subCands <: sᵢ <: pⱼ <: ⋃superCands` for any such pair. The
// pairs are trialled in specificity order, so a concrete candidate is tried
// before a bare type variable and a variable is only ever a last resort. Each
// trial recurses into the ordinary structural rules, which is where a class tag
// meets its parent and an arrow meets an arrow.
//
// Looking for one pair is sound but incomplete. Two exact records meet to `never`
// once #1064 lands, and until then `{x: number} & {y: number} <: {x: number, y:
// number}` finds no single pair and is rejected, which is what the rule it
// replaces answered too.
//
// A pair repeating the constraint constrainNF was called with is skipped. An
// inexact union is one atom, since its open tail has no atom to stand for it, so
// normalizing `boolean <: (number | string | ...)` hands back the pair it started
// from. Trialling that pair would ask the question the caller is already
// deciding and never reach a smaller one.
func (c *Context) decideMeetJoin(
	subCands, superCands []soltype.Type, sub, super soltype.Type, seen *seenPairs, mutCtx bool,
) nfDecision {
	pairs := orderedPairs(subCands, superCands, sub, super)
	if len(pairs) == 0 {
		return nfDecision{errs: []SolverError{&CannotConstrainError{Sub: sub, Super: super}}}
	}
	order := make([]int, len(pairs))
	for i := range order {
		order[i] = i
	}
	committed, winIdx, winErrs, trialErrs := c.trialAndCommit(order, func(idx int) []SolverError {
		// A cloned seen keeps each pair's coinductive cache independent, so a failed
		// pair's entries cannot short-circuit a later pair to success.
		return c.constrain(pairs[idx].sub, pairs[idx].super, seen.Clone(), mutCtx)
	})
	if committed {
		return nfDecision{errs: winErrs, committed: pairs[winIdx].super}
	}
	// No single pair holds. An intersection of arrows can still be below an arrow
	// through the arms taken together, which is what decideArrows weighs.
	if target, ok := c.decideArrows(subCands, superCands, seen, mutCtx); ok {
		return nfDecision{committed: target}
	}
	// The last pair's diagnostics are the ones a caller that reports the
	// decomposition surfaces; the union-super rule replaces them with one
	// union-level diagnostic instead.
	return nfDecision{errs: trialErrs[len(trialErrs)-1]}
}

// maxDecomposedArms caps how many arrow atoms decideArrows weighs. It tries every
// group of arms, so the work doubles with each further arm. Six arms is 63 groups,
// past which the decomposition costs more than the constraints it settles. An
// intersection of more arrows than that is decided by the pair trial alone, which
// is sound and only ever rejects what the decomposition would have accepted.
const maxDecomposedArms = 6

// decideArrows decides `⋂arms <: target` when the subtype side holds several arrow
// atoms that no fusion merged into one. It returns the target it settled on and
// reports whether it settled the goal at all.
//
// The rule is the Frisch-Castagna-Benzaken decomposition, which weighs the arms
// together instead of collapsing them to one arrow. Writing the arms `Aᵢ -> Cᵢ`
// and the target `E -> F`, both legs must hold:
//
//  1. The target's domain is covered: `E <: ⋃ᵢ Aᵢ`. No input the target accepts
//     may fall outside every arm's domain, since no arm says what the value does
//     with such an input.
//  2. For every group P of arms, either the inputs are still covered without P —
//     `E <: ⋃_{i∉P} Aᵢ` — or the arms in P return something the target tolerates,
//     `⋂_{i∈P} Cᵢ <: F`. A value carrying every arm type returns, on a given
//     input, something in the codomain of every arm whose domain accepts that
//     input. So the goal fails exactly when some group is the only cover for part
//     of E while its combined codomain escapes F.
//
// `((x: number) -> boolean) & ((x: string) -> null) <: (x: number | string) ->
// (boolean | null)` holds under this rule: each arm alone returns something the
// target tolerates, and the group holding both returns `boolean & null`, which no
// value inhabits. Checking the arms one at a time rejects it, since neither arm
// alone accepts both a number and a string.
//
// It runs only after the pair trial in decideMeetJoin found no single arm below
// the target, so it can only add acceptances. Both rules are sound, so the order
// decides which bounds a variable in the target picks up, not the verdict.
//
// The decomposition is restricted to plain one-parameter arrows: the arms and the
// target must agree on what a call may raise, and none may carry a receiver, a
// quantifier, an optional or rest parameter, or a trailing `...`. A multi-parameter
// arrow's domain is a product of positions rather than one type, and covering a
// product needs the union of the arms' whole domains rather than a union per
// position, so those keep the pair trial's answer.
func (c *Context) decideArrows(subCands, superCands []soltype.Type, seen *seenPairs, mutCtx bool) (soltype.Type, bool) {
	arms := decomposableArrows(subCands)
	if len(arms) < 2 || len(arms) > maxDecomposedArms {
		return nil, false
	}
	for _, cand := range superCands {
		target, ok := cand.(*soltype.FuncType)
		if !ok || !decomposableArrow(target) || !sameRaises(arms, target) {
			continue
		}
		p := newProbe(c, c.probe)
		c.probe = p
		settled := c.arrowLegsHold(arms, target, seen, mutCtx)
		c.probe = p.parent
		if settled {
			p.Commit()
			return cand, true
		}
		p.Discard()
	}
	return nil, false
}

// arrowLegsHold runs the two legs of the decomposition decideArrows documents and
// reports whether both hold. The caller keeps the bounds the legs recorded only
// when they do.
func (c *Context) arrowLegsHold(arms []*soltype.FuncType, target *soltype.FuncType, seen *seenPairs, mutCtx bool) bool {
	domain := target.Params[0].Type
	// Leg 1. Every input the target accepts is accepted by some arm.
	if hasHardError(c.constrain(domain, joinDomains(arms, allArms(arms)), seen.Clone(), mutCtx)) {
		return false
	}
	// Leg 2, over every group of arms. A group is a bit set over arms, so counting
	// from 1 to 2ⁿ-1 enumerates each non-empty one exactly once.
	for group := 1; group < 1<<len(arms); group++ {
		// The inputs the arms outside this group already cover need nothing from the
		// group itself. The check is speculative, so it runs under a discarding probe
		// and records no bound.
		outside := joinDomains(arms, allArms(arms)&^group)
		if !hasHardError(c.trialUnderProbeSeen(domain, outside, seen.Clone())) {
			continue
		}
		if hasHardError(c.constrain(meetCodomains(arms, group), target.Ret, seen.Clone(), mutCtx)) {
			return false
		}
	}
	return true
}

// allArms is the bit set holding every arm, the group leg 1 unions the domains of.
func allArms(arms []*soltype.FuncType) int {
	return 1<<len(arms) - 1
}

// joinDomains is the union of the domains of the arms in group, and `never` when
// the group is empty, since an empty union covers no input at all.
func joinDomains(arms []*soltype.FuncType, group int) soltype.Type {
	parts := make([]soltype.Type, 0, len(arms))
	for i, arm := range arms {
		if group&(1<<i) != 0 {
			parts = append(parts, arm.Params[0].Type)
		}
	}
	return newUnion(nil, parts, false)
}

// meetCodomains is the intersection of the codomains of the arms in group, what a
// value carrying every arm type returns on an input only this group accepts.
func meetCodomains(arms []*soltype.FuncType, group int) soltype.Type {
	parts := make([]soltype.Type, 0, len(arms))
	for i, arm := range arms {
		if group&(1<<i) != 0 {
			parts = append(parts, arm.Ret)
		}
	}
	return newIntersection(nil, parts)
}

// decomposableArrows returns the atoms of a meet the decomposition can weigh,
// dropping every other atom. Dropping is sound: a meet is below each of its parts,
// so a decision reached from the arrows alone holds for the whole meet.
func decomposableArrows(atoms []soltype.Type) []*soltype.FuncType {
	arrows := make([]*soltype.FuncType, 0, len(atoms))
	for _, atom := range atoms {
		if f, ok := atom.(*soltype.FuncType); ok && decomposableArrow(f) {
			arrows = append(arrows, f)
		}
	}
	return arrows
}

// decomposableArrow reports whether one arrow has the plain shape the
// decomposition is restricted to, spelled out in decideArrows.
func decomposableArrow(f *soltype.FuncType) bool {
	if f.SelfParam != nil || len(f.TypeParams) > 0 || len(f.LifetimeParams) > 0 || f.Inexact {
		return false
	}
	if len(f.Params) != 1 {
		return false
	}
	return !f.Params[0].Optional && !f.Params[0].Rest
}

// sameRaises reports whether every arm raises what the target raises. The
// decomposition reasons about the domains and the codomains alone, so a call that
// could raise something the target does not name has to be ruled out here.
func sameRaises(arms []*soltype.FuncType, target *soltype.FuncType) bool {
	for _, arm := range arms {
		if !equalType(arm.ThrowsOrNever(), target.ThrowsOrNever()) {
			return false
		}
	}
	return true
}

// nfPair is one candidate pair decideMeetJoin trials.
type nfPair struct{ sub, super soltype.Type }

// orderedPairs lists the pairs to trial, supertype candidate first and subtype
// candidate second, each in specificity order. Ordering the supertype candidates
// outermost keeps the choice among union members the one a reader sees first, and
// it is the order the rule this layer replaces used.
//
// origSub and origSuper are the constraint the caller is deciding. The pair that
// repeats it is dropped, for the reason decideMeetJoin gives.
func orderedPairs(subCands, superCands []soltype.Type, origSub, origSuper soltype.Type) []nfPair {
	subOrder := specificityOrder(subCands)
	pairs := make([]nfPair, 0, len(subCands)*len(superCands))
	for _, j := range specificityOrder(superCands) {
		for _, i := range subOrder {
			if equalType(subCands[i], origSub) && equalType(superCands[j], origSuper) {
				continue
			}
			pairs = append(pairs, nfPair{sub: subCands[i], super: superCands[j]})
		}
	}
	return pairs
}

// varsAsTypes returns a variable set's members as types, ordered by id so a
// candidate list reads the same however the set was built.
func varsAsTypes(vars set.Set[*soltype.TypeVarType]) []soltype.Type {
	out := make([]soltype.Type, 0, vars.Len())
	for _, v := range sortedVars(vars) {
		out = append(out, v)
	}
	return out
}

// isNegation reports whether t is a complement, the node whose presence on either
// side of a constraint routes the constraint through the normal-form layer.
func isNegation(t soltype.Type) bool {
	_, ok := t.(*soltype.NegationType)
	return ok
}
