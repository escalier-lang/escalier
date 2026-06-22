package solver

import (
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// newUnion / newIntersection are the M6 PR1 smart constructors — the SINGLE
// mint path for UnionType and IntersectionType. They run the normalization the
// M6 plan enumerates in one pass, so every later milestone that mints a lattice
// node — combine's coalesced output, mergeObjectGroup's shared-property meet,
// PR2's annotation input, PR6's permissive borrow join — produces well-formed,
// canonical, deduplicated lattice nodes without re-spelling the rules.
//
// The normalization splits into a Context-free CORE — flatten, lattice
// identities, ErrorType elision, dedup, canonical order, collapse — and a
// Context-gated subsumed-member elimination step. The core is what every mint
// site needs; subsumption runs ONLY when a Context is supplied, because the
// subtype test it relies on calls c.constrain under a probe. combine and
// mergeObjectGroup pass a nil Context (their members are already coalesced, so
// dedup + identities tighten them enough). resolveTypeAnn (PR2) and
// joinBorrows (PR6) pass their checker's Context so an annotation or borrow
// union is fully subsumed.
//
// Canonical member order is imposed at construction so equalType stays
// positional and cheap: two unions over the same member set hold them in the
// same order, so the existing equalTypeSlice already returns true. The order
// is also what lets rendering be deterministic (string | number and number |
// string render identically), and what makes a canonicalized type usable as a
// stable key for caching.
func newUnion(c *Context, parts []soltype.Type, inexact bool) soltype.Type {
	// 1. Flatten nested same-kind members and detect tail-inexactness: a nested
	//    UnionType is spliced in, and an inexact member contributes to the
	//    result's inexactness (an inexact tail anywhere makes the whole union
	//    inexact).
	flat := make([]soltype.Type, 0, len(parts))
	for _, p := range parts {
		if u, ok := p.(*soltype.UnionType); ok {
			flat = append(flat, u.Types...)
			if u.Inexact {
				inexact = true
			}
			continue
		}
		flat = append(flat, p)
	}

	// 2. Lattice identity drop: never (⊥) is the identity of |, so drop it.
	// 3. ErrorType elision: drop ErrorType (the join identity / absorbing sentinel)
	//    unless it ends up the sole survivor below.
	pruned := make([]soltype.Type, 0, len(flat))
	hadError := false
	for _, p := range flat {
		if _, isNever := p.(*soltype.NeverType); isNever {
			continue
		}
		if _, isError := p.(*soltype.ErrorType); isError {
			hadError = true
			continue
		}
		pruned = append(pruned, p)
	}

	// 4. Structural dedup via equalType — order-preserving.
	pruned = dedup(pruned)

	// 5. Subsumed-member elimination (Context-gated, optional). Drop a member
	//    that is a subtype of another member, since the wider one already
	//    covers it. Concrete-gated: skip when either side carries a free type
	//    variable, to avoid speculatively pinning a var mid-walk.
	if c != nil {
		pruned = subsumeUnionMembers(c, pruned)
	}

	// 6. Canonical order, so member order is construction-order-independent.
	sortTypes(pruned)

	// 7. Collapse.
	if len(pruned) == 0 {
		if hadError {
			return &soltype.ErrorType{}
		}
		// Empty union ⇒ never (⊥, the identity of |). An inexact-but-empty union
		// is still ⊥; the inexactness flag has no carrier without members. If a
		// future caller needs an "inexact never" — i.e. a tail of unknown alone —
		// it can express that as unknown explicitly.
		return &soltype.NeverType{}
	}
	if len(pruned) == 1 && !inexact {
		// A single exact-union member collapses to that member. An inexact
		// single-member union keeps its wrapper, since the `... | T` tail makes
		// it strictly weaker than the bare T.
		return pruned[0]
	}
	return &soltype.UnionType{Types: pruned, Inexact: inexact}
}

// newIntersection is the meet twin of newUnion. An IntersectionType carries no
// exactness flag (M6 plan: "intersection has no exact/inexact variant —
// exactness is a property of its result, not the meet"), so the API is one
// argument shorter.
func newIntersection(c *Context, parts []soltype.Type) soltype.Type {
	// 1. Flatten nested intersections.
	flat := make([]soltype.Type, 0, len(parts))
	for _, p := range parts {
		if i, ok := p.(*soltype.IntersectionType); ok {
			flat = append(flat, i.Types...)
			continue
		}
		flat = append(flat, p)
	}

	// 2. Lattice identity drop: unknown (⊤) is the identity of &, so drop it.
	// 3. ErrorType elision: drop ErrorType unless it ends up the sole survivor.
	pruned := make([]soltype.Type, 0, len(flat))
	hadError := false
	for _, p := range flat {
		if _, isUnknown := p.(*soltype.UnknownType); isUnknown {
			continue
		}
		if _, isError := p.(*soltype.ErrorType); isError {
			hadError = true
			continue
		}
		pruned = append(pruned, p)
	}

	// 4. Structural dedup.
	pruned = dedup(pruned)

	// 5. Subsumed-member elimination, Context-gated. Drop an intersection
	//    member that is a SUPERTYPE of another, since the more specific
	//    sibling already implies it.
	if c != nil {
		pruned = subsumeIntersectionMembers(c, pruned)
	}

	// 6. Canonical order.
	sortTypes(pruned)

	// 7. Collapse.
	if len(pruned) == 0 {
		if hadError {
			return &soltype.ErrorType{}
		}
		// Empty intersection ⇒ unknown (⊤, the identity of &).
		return &soltype.UnknownType{}
	}
	if len(pruned) == 1 {
		return pruned[0]
	}
	return &soltype.IntersectionType{Types: pruned}
}

// subsumeUnionMembers drops a union member that is a subtype of another member.
// Concrete-gated: a member that still carries a free type variable is left
// alone, since trialling subtype against an inference variable would
// speculatively pin it.
//
// The check uses a discard-only probe so a successful trial leaves no bound
// mutation behind. With c.constrain returning the trial's errors directly
// rather than routing through c.errs, the probe just needs to roll back bound
// appends — newProbe + Discard is enough.
func subsumeUnionMembers(c *Context, parts []soltype.Type) []soltype.Type {
	if len(parts) < 2 {
		return parts
	}
	keep := make([]bool, len(parts))
	for i := range keep {
		keep[i] = true
	}
	for i, a := range parts {
		if !keep[i] || hasTypeVar(a) {
			continue
		}
		for j, b := range parts {
			if i == j || !keep[j] || hasTypeVar(b) {
				continue
			}
			// a is subsumed if a <: b for some other kept member b.
			if subtypeUnderProbe(c, a, b) {
				keep[i] = false
				break
			}
		}
	}
	return filterKept(parts, keep)
}

// subsumeIntersectionMembers drops an intersection member that is a supertype
// of another. The dropped member is the wider one — the narrower sibling
// already constrains the value below it. Symmetric to subsumeUnionMembers.
func subsumeIntersectionMembers(c *Context, parts []soltype.Type) []soltype.Type {
	if len(parts) < 2 {
		return parts
	}
	keep := make([]bool, len(parts))
	for i := range keep {
		keep[i] = true
	}
	for i, a := range parts {
		if !keep[i] || hasTypeVar(a) {
			continue
		}
		for j, b := range parts {
			if i == j || !keep[j] || hasTypeVar(b) {
				continue
			}
			// a is subsumed if b <: a for some other kept member b (a is the wider).
			if subtypeUnderProbe(c, b, a) {
				keep[i] = false
				break
			}
		}
	}
	return filterKept(parts, keep)
}

func filterKept(parts []soltype.Type, keep []bool) []soltype.Type {
	out := make([]soltype.Type, 0, len(parts))
	for i, p := range parts {
		if keep[i] {
			out = append(out, p)
		}
	}
	return out
}

// subtypeUnderProbe trials sub <: super under a discard-only probe and reports
// whether the trial succeeded. A successful trial means subsumption holds; the
// probe is always Discarded so the bound mutations it would otherwise leave
// behind never become visible. Used by the M6 PR1 subsumption pass.
func subtypeUnderProbe(c *Context, sub, super soltype.Type) bool {
	p := newProbe(c.probe)
	c.probe = p
	errs := c.constrain(sub, super, set.NewSet[constraintKey]())
	c.probe = p.parent
	p.Discard()
	return len(errs) == 0
}

// hasTypeVar reports whether t contains any TypeVarType, anywhere in its
// structure. Subsumption checks gate on this: a member with a free var would
// trial constrain against an unresolved variable and could pin it
// speculatively, which is the failure mode the M6 plan calls out.
func hasTypeVar(t soltype.Type) bool {
	switch t := t.(type) {
	case *soltype.TypeVarType:
		return true
	case *soltype.FuncType:
		for _, p := range t.Params {
			if hasTypeVar(p.Type) {
				return true
			}
		}
		return hasTypeVar(t.Ret)
	case *soltype.TupleType:
		for _, e := range t.Elems {
			if hasTypeVar(e) {
				return true
			}
		}
		return false
	case *soltype.ObjectType:
		for _, e := range t.Elems {
			if hasTypeVar(soltype.AsProperty(e).Type) {
				return true
			}
		}
		return false
	case *soltype.PromiseType:
		return hasTypeVar(t.Inner)
	case *soltype.RefType:
		return hasTypeVar(t.Inner)
	case *soltype.UnionType:
		for _, m := range t.Types {
			if hasTypeVar(m) {
				return true
			}
		}
		return false
	case *soltype.IntersectionType:
		for _, m := range t.Types {
			if hasTypeVar(m) {
				return true
			}
		}
		return false
	}
	return false
}

// sortTypes orders parts in place under compareType. Stable so that members
// already in canonical order across passes keep their pointer order, which
// keeps the rebuild identity-preserving when possible.
func sortTypes(parts []soltype.Type) {
	sort.SliceStable(parts, func(i, j int) bool {
		return compareType(parts[i], parts[j]) < 0
	})
}

// compareType is the deterministic total order canonical member order is built
// on. It is consistent with equalType: two equalType-equal types compare equal.
// The ordering ranks by a concrete-kind tag first, then tie-breaks by the
// rendered Print string within a kind. The string fallback is a pragmatic
// choice — the M6 plan flags it as such — and couples the canonical order to
// the printer, but it bottoms out deterministically and produces a stable order
// for every shape M1–M4 mints.
func compareType(a, b soltype.Type) int {
	if equalType(a, b) {
		return 0
	}
	ka, kb := typeKindOrder(a), typeKindOrder(b)
	if ka != kb {
		if ka < kb {
			return -1
		}
		return 1
	}
	// Same kind, structurally distinct: fall back to the printer for a stable
	// total order. Two equalType-equal types are already filtered above.
	return strings.Compare(soltype.Print(a), soltype.Print(b))
}

// typeKindOrder ranks a soltype concrete kind for compareType. Lattice atoms
// come first (never, unknown, error, void), then primitives and literals, then
// the structural kinds, and the lattice forms (UnionType / IntersectionType)
// last so a nested lattice node always sorts after its concrete siblings —
// useful while M6 still allows residual lattice nodes after dedup but before
// flatten finishes (it shouldn't, but the order keeps the result legible).
func typeKindOrder(t soltype.Type) int {
	switch t.(type) {
	case *soltype.NeverType:
		return 0
	case *soltype.UnknownType:
		return 1
	case *soltype.ErrorType:
		return 2
	case *soltype.Void:
		return 3
	case *soltype.PrimType:
		return 4
	case *soltype.LitType:
		return 5
	case *soltype.TypeVarType:
		return 6
	case *soltype.RefType:
		return 7
	case *soltype.TupleType:
		return 8
	case *soltype.ObjectType:
		return 9
	case *soltype.PromiseType:
		return 10
	case *soltype.FuncType:
		return 11
	case *soltype.UnionType:
		return 12
	case *soltype.IntersectionType:
		return 13
	}
	return 14
}
