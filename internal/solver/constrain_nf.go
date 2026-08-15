package solver

import (
	"slices"

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
// every conjunct is a subtype of every disjunct. That product is the deterministic
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
// variables against a join of atoms and variables, which constrainImplied settles.
//
// # Where a choice still remains
//
// A meet of two atoms that did not fuse, or a join of two that did not, leaves a
// genuine choice: `{x: number} | {y: number}` is not one record, so nothing says
// which side a given subtype has to satisfy. constrainImplied trials the pairs in
// specificity order for exactly that residue. Every trial runs over atoms the
// normal form produced, so the fusions above have already collapsed the cases a
// member-by-member search decides wrongly.

// nfDecision is what one constrainNF call derived. committed holds the
// supertype-side candidate each implied goal settled on, one entry per goal that
// had a choice to make, and the union-super rule reads it to name an ambiguous or
// variable-pinning commit.
type nfDecision struct {
	errs      []SolverError
	committed []soltype.Type
}

// constrainNF decides `sub <: super` through the normal forms of both operands.
//
// The whole derivation runs under one probe, so a constraint that fails records no
// bound. Callers depend on that: the union-super rule replaces the decomposition's
// diagnostics with one of its own, and a bound recorded on the way to that failure
// would outlive the derivation that justified it.
func (c *Context) constrainNF(sub, super soltype.Type, seen *seenPairs, mutCtx bool) nfDecision {
	lhs := c.mkDeepDNF(sub, soltype.Positive)
	rhs := c.mkDeepCNF(super, soltype.Negative)

	p := newProbe(c, c.probe)
	c.probe = p
	var out nfDecision
	// Every implied goal has to hold, so the first one that fails settles the whole
	// constraint. Stopping there keeps a failure from reporting the same diagnostic
	// once per goal, which two conjuncts failing the same way would otherwise do.
	for _, conj := range lhs.Conjuncts {
		for _, disj := range rhs.Disjuncts {
			implied := c.constrainImplied(conj, disj, sub, super, seen, mutCtx)
			out.errs = append(out.errs, implied.errs...)
			out.committed = append(out.committed, implied.committed...)
			if hasHardError(implied.errs) {
				c.probe = p.parent
				p.Discard()
				return nfDecision{errs: out.errs}
			}
		}
	}
	c.probe = p.parent
	p.Commit()
	return out
}

// constrainImplied decides one conjunct of the subtype's DNF against one disjunct
// of the supertype's CNF, the implied goal this file's header describes. It moves
// both negated parts across the `<:`, which leaves `⋂subCands <: ⋃superCands` over
// atoms and variables, and proves that by finding a pair, one candidate from each
// side, whose subtype-side candidate is a subtype of its supertype-side one, since
// `⋂subCands <: sᵢ <: pⱼ <: ⋃superCands` for any such pair. The pairs are trialled
// in specificity order, so a bare variable is only ever a last resort, and each
// trial recurses into the ordinary structural rules.
//
// sub and super are the operands constrainNF was called with, and name the failure
// when the goal has no pair to trial at all.
//
// Three things follow from deciding by one pair.
//
//   - It is incomplete. A type can be a subtype of a join through the join's
//     members taken together, which no single pair states. `boolean <: true |
//     false` is rejected for that reason. The two literals do not fuse into one
//     atom, and `boolean` is a subtype of neither of them on its own.
//
//   - A pair repeating the caller's own question against an inexact union is
//     skipped. Such a union is one atom, so normalizing `boolean <: (number |
//     string | ...)` hands back the pair it started from and no smaller question
//     is ever reached. Every other supertype comes apart into smaller atoms.
//
//   - A variable on the supertype side picks up the whole meet as a lower bound,
//     which is stronger than the goal asks for. The goal asks only for the meet
//     with the other supertype candidates subtracted, so `"hi" <: (T | number)`
//     records `"hi"` under T where `"hi" ∩ ¬number` would do. That is sound, and a
//     variable is reached only after every concrete candidate failed. weakestBound
//     subtracts for the one subtype side where the stronger bound does damage,
//     `unknown`, and says why it goes no further.
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
		// side reads. `5 <: ¬string` reaches this: the complement moves `string` to the
		// subtype side as a positive atom, and `5 ∩ string` is `never`, since a literal
		// and a primitive of another family are disjoint.
		//
		// `5 <: ¬number` does not reach it. That meet is `5 ∩ number`, which is `5`, so
		// the goal goes on to the trial below and fails there against an empty join.
		return nfDecision{}
	}
	join, _ := c.fuseAtoms(pooled(conj.Rnf.Atoms, disj.Rnf.Atoms), joinOfAtoms)

	// A variable is opaque, so it joins the side it sits on as one more candidate
	// rather than being taken apart. A variable negated on one side stands on the
	// other, by the same two rewrites the negated structural parts follow.
	subCands := slices.Concat(meet, varsAsTypes(conj.Vars), varsAsTypes(disj.NVars))
	superCands := slices.Concat(join, varsAsTypes(disj.Vars), varsAsTypes(conj.NVars))

	// An empty meet is `unknown`, the top of the lattice. Naming it explicitly gives the
	// trial something to constrain, and `unknown <: T` still records a bound when the
	// supertype side is a variable. topMeet remembers that the conjunct contributed no
	// positive part, which is the goal weakestBound subtracts under.
	topMeet := len(subCands) == 0
	if topMeet {
		subCands = []soltype.Type{&soltype.UnknownType{}}
	}

	pairs := orderedPairs(subCands, superCands, sub, super, topMeet)
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
		return nfDecision{errs: winErrs, committed: []soltype.Type{pairs[winIdx].super}}
	}
	// No single pair holds. An intersection of arrows can still be a subtype of an
	// arrow through the arms taken together, which is what decideArrows weighs.
	if target, ok := c.decideArrows(subCands, superCands, seen); ok {
		return nfDecision{committed: []soltype.Type{target}}
	}
	// The last pair's diagnostics are the ones a caller that reports the
	// decomposition surfaces; the union-super rule replaces them with one
	// union-level diagnostic instead.
	return nfDecision{errs: trialErrs[len(trialErrs)-1]}
}

// maxDecomposedArms caps how many arrow atoms decideArrows weighs. It tries every
// group of arms, so the work doubles with each further arm, and six arms is
// already 63 groups. An intersection of more arrows is left to the pair trial,
// which is sound and only rejects what the decomposition would have accepted.
const maxDecomposedArms = 6

// decideArrows decides `⋂arms <: target` when the subtype side holds several arrow
// atoms that no fusion merged into one, by the Frisch-Castagna-Benzaken
// decomposition. It returns the target it settled on and reports whether it
// settled the goal at all. arrowLegsHold states the two legs, and the corpus
// header in constrain_nf_test.go derives them.
//
// It runs after constrainImplied's pair trial found no single arm that is a subtype
// of the target. Both rules are sound, so which runs first decides the bounds a
// variable in the target picks up rather than the verdict.
//
// Only plain one-parameter arrows are weighed, the shape decomposableArrow admits.
// A multi-parameter arrow's domain is a product of positions rather than one type,
// and covering a product needs the union of the arms' whole domains rather than a
// union per position.
func (c *Context) decideArrows(subCands, superCands []soltype.Type, seen *seenPairs) (soltype.Type, bool) {
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
		settled := c.arrowLegsHold(arms, target, seen)
		c.probe = p.parent
		if settled {
			p.Commit()
			return cand, true
		}
		p.Discard()
	}
	return nil, false
}

// arrowLegsHold reports whether both legs of the decomposition hold for arms
// `Aᵢ -> Cᵢ` against target `E -> F`. The caller keeps the bounds they recorded
// only when they do.
//
//  1. The target's domain is covered, `E <: ⋃ᵢ Aᵢ`, since no arm says what the
//     value does with an input outside every arm's domain.
//  2. For every group P of arms, either the arms outside P still cover the inputs,
//     `E <: ⋃_{i∉P} Aᵢ`, or P returns something the target tolerates,
//     `⋂_{i∈P} Cᵢ <: F`. A value carrying every arm type returns, on a given
//     input, something in the codomain of every arm that accepts it.
//
// Every leg compares domain against domain or codomain against codomain, so each
// runs with the deep-mut context cleared, as the arrow rule in the structural
// switch clears it. A function carries its own annotation context.
func (c *Context) arrowLegsHold(arms []*soltype.FuncType, target *soltype.FuncType, seen *seenPairs) bool {
	domain := target.Params[0].Type
	// Leg 1. Every input the target accepts is accepted by some arm.
	if hasHardError(c.constrain(domain, joinDomains(arms, allArms(arms)), seen.Clone(), false)) {
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
		if hasHardError(c.constrain(meetCodomains(arms, group), target.Ret, seen.Clone(), false)) {
			return false
		}
	}
	return true
}

// allArms is the bit set holding every arm, the group leg 1 unions the domains of.
func allArms(arms []*soltype.FuncType) int {
	return 1<<len(arms) - 1
}

// joinDomains is the union of the domains of the arms in group, and `never` for an
// empty group, since an empty union covers no input.
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

// decomposableArrows returns the atoms of a meet the decomposition can weigh and
// drops the rest, which is sound because a meet is a subtype of each of its parts.
func decomposableArrows(atoms []soltype.Type) []*soltype.FuncType {
	arrows := make([]*soltype.FuncType, 0, len(atoms))
	for _, atom := range atoms {
		if f, ok := atom.(*soltype.FuncType); ok && decomposableArrow(f) {
			arrows = append(arrows, f)
		}
	}
	return arrows
}

// decomposableArrow reports whether one arrow is plain enough to decompose: one
// parameter, no receiver, no quantifier, and no optional, rest, or `...` marker.
// Everything a leg does not compare has to be absent, since no leg would see it.
func decomposableArrow(f *soltype.FuncType) bool {
	if f.SelfParam != nil || len(f.TypeParams) > 0 || len(f.LifetimeParams) > 0 || f.Inexact {
		return false
	}
	if len(f.Params) != 1 {
		return false
	}
	return !f.Params[0].Optional && !f.Params[0].Rest
}

// sameRaises reports whether every arm raises what the target raises. The legs
// compare domains and codomains alone, so an arm that could raise something the
// target does not name is ruled out here.
func sameRaises(arms []*soltype.FuncType, target *soltype.FuncType) bool {
	for _, arm := range arms {
		if !equalType(arm.ThrowsOrNever(), target.ThrowsOrNever()) {
			return false
		}
	}
	return true
}

// nfPair is one candidate pair constrainImplied trials.
type nfPair struct{ sub, super soltype.Type }

// orderedPairs lists the pairs to trial, each side in specificity order and the
// supertype candidates outermost, so the choice among union members is the one a
// reader sees first. origSub and origSuper are the constraint the caller is
// deciding; the pair repeating it against an inexact union is dropped, for the
// reason constrainImplied gives.
//
// A variable candidate gets ONE pair, whose subtype side varTrialSub decides, rather
// than one pair per subtype candidate. Every other candidate keeps a pair per subtype
// candidate, since deciding `sᵢ <: pⱼ` asks whether one shape fits another and records
// no bound of its own. topMeet says the conjunct contributed no positive part, which is
// one of the cases varTrialSub reads.
//
// specificityOrder ranks a variable below every concrete type, so a variable
// candidate's pair is trialled after every concrete candidate's.
func orderedPairs(subCands, superCands []soltype.Type, origSub, origSuper soltype.Type, topMeet bool) []nfPair {
	subOrder := specificityOrder(subCands)
	pairs := make([]nfPair, 0, len(subCands)*len(superCands))
	for _, j := range specificityOrder(superCands) {
		if _, isVar := superCands[j].(*soltype.TypeVarType); isVar {
			pairs = append(pairs, nfPair{sub: varTrialSub(subCands, superCands, j, topMeet), super: superCands[j]})
			continue
		}
		for _, i := range subOrder {
			if isInexactUnion(superCands[j]) && equalType(subCands[i], origSub) && equalType(superCands[j], origSuper) {
				continue
			}
			pairs = append(pairs, nfPair{sub: subCands[i], super: superCands[j]})
		}
	}
	return pairs
}

// varTrialSub is the subtype side of the one pair the variable at superCands[keep] is
// trialled against, and so the lower bound that variable records.
//
// The general case is the whole meet. Pairing a single candidate with the variable
// instead would drop the rest, and the trial cannot notice, since a pair against a free
// variable always holds and whichever candidate is tried first wins. Deciding
//
//	{a: 1, ...} & ((x: number) -> string) <: (string | T)
//
// that way gives T only `{a: 1, ...}`, so a later `T <: (x: number) -> string` is
// rejected even though the meet satisfies it.
//
// Two goals take something other than the meet.
//
//   - The target standing in the meet discharges the goal by reflexivity, since
//     `⋂subCands <: v` holds for any meet holding v. The goal asks nothing of v, so
//     handing constrain the bare variable lets its `v <: v` rule settle the pair
//     without recording a bound that names v.
//   - A meet of `unknown` bounds v by nothing at all, so weakestBound subtracts the
//     other supertype candidates instead. It says why the subtraction stops there.
func varTrialSub(subCands, superCands []soltype.Type, keep int, topMeet bool) soltype.Type {
	target := superCands[keep]
	if slices.ContainsFunc(subCands, func(s soltype.Type) bool { return equalType(s, target) }) {
		return target
	}
	if topMeet {
		return weakestBound(superCands, keep)
	}
	return newIntersection(nil, subCands)
}

// weakestBound is the subtype side of the goal the variable at superCands[keep] is
// trialled against when the subtype side is `unknown`. It meets the complements of the
// variable-free supertype candidates, by the move-across-the-`<:` rewrite the file
// header states for a negated part:
//
//	unknown <: p₁ ∪ … ∪ pₙ ∪ v    is    ¬p₁ ∩ … ∩ ¬pₙ <: v
//
// That asks of v what the goal asks and nothing more. Recording `unknown` instead pins v
// to the top of the lattice, so `¬T <: number`, which normalizes to
// `unknown <: number ∪ T`, would give T `unknown` and collapse `¬T` to `never` where
// `¬number` is what the goal asks for.
//
// A candidate is subtracted only when it is variable-free, and only from a subtype side
// of `unknown`. Both gates keep out a complement no later pass can clear.
// simplifyNegations decides disjointness through the meet of two atoms, so it drops
// `¬number` against `"hi"` but not `¬{b: number, ...}` against `{a: 1, ...}`, which can
// share a value, and it refuses a complement over a variable outright. Subtracting past
// either gate is sound and leaves the complement in the rendered type, which
// constrainImplied's third bullet records.
//
// A skipped candidate stays on the supertype side, so the trial asks for at least what
// the goal asks. Skipping every one leaves an empty meet, which newIntersection returns
// as `unknown`.
func weakestBound(superCands []soltype.Type, keep int) soltype.Type {
	parts := make([]soltype.Type, 0, len(superCands)-1)
	for j, p := range superCands {
		if j == keep || !concreteMember(p) {
			continue
		}
		neg := newNegation(p)
		if isNeverType(neg) {
			// p is the top of the lattice, so it covers every value on its own and the meet
			// under it is empty. `never <: v` records no bound. That is the right answer,
			// since the goal already holds without v.
			return neg
		}
		parts = append(parts, neg)
	}
	return newIntersection(nil, parts)
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

// isNegation reports whether t is a complement, the node that routes a constraint
// through the normal-form layer from either side.
func isNegation(t soltype.Type) bool {
	_, ok := t.(*soltype.NegationType)
	return ok
}

// isInexactUnion reports whether t is a union with an open tail, the one supertype
// normalization hands back whole.
func isInexactUnion(t soltype.Type) bool {
	u, ok := t.(*soltype.UnionType)
	return ok && u.Inexact
}
