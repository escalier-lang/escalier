package checker

import (
	"sort"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// specificity is the result of comparing two overload arms. Its
// underlying int values match the sort.Interface convention so it
// can be compared with `< 0` / `> 0` directly.
type specificity int

const (
	aMoreSpecific specificity = -1
	tie           specificity = 0
	bMoreSpecific specificity = 1
)

// compareOverloadArms compares two overload arms by specificity.
// Returns aMoreSpecific when a is strictly more specific than b,
// bMoreSpecific when b is strictly more specific than a, and tie
// when the two arms are considered equally specific (callers should
// use a stable sort so declared source order is preserved on ties).
//
// Rules in descending priority — checked in order, the first
// discriminating rule wins:
//
//  1. **Pointwise param subtype.** When arities match, arm a is more
//     specific than b iff `a.Params[i]` is a structural subtype of
//     `b.Params[i]` for every i and at least one position is a strict
//     subtype (i.e. the reverse direction fails). The check is a
//     pure, side-effect-free traversal of the pruned type shapes —
//     it never invokes the unifier and never resolves a TypeRefType
//     to its underlying alias definition (though it does recurse
//     into the type arguments of same-alias references), so it's
//     safe to call during placeholder phases where unifying
//     against unrelated overload arms would be unsound. Subsumes the
//     old literal-count heuristic and naturally handles unions,
//     object fields, and nested literals.
//
//  2. **Fewer required params is more specific.** Optional params
//     (FuncParam.Optional) and rest params (*RestSpreadType) do
//     not count as required. Matches TS's "more required args
//     before fewer" rule.
//
//  3. **Fewer type-param-typed params is more specific.** Count
//     params whose top-level type is a `TypeRefType` to one of
//     the fn's own type params. A concrete `(value: string)` ranks
//     ahead of a generic `<T>(value: T)` because `T` provides no
//     constraint on what the caller may pass.
//
//  4. Source order tiebreaker. Returns 0 here; the caller's stable
//     sort preserves declared order. Subtype is a partial order, so
//     many real arms — different literal tags, disjoint object
//     shapes, etc. — will be incomparable under rule 1 and rely on
//     this fallback.
//
// This comparator is used both for free-fn overload arms (sorted
// when the intersection is constructed in infer_module.go /
// generalize.go) and for the method-elem merge pass. Keeping
// a single comparator means free-fn and method overload dispatch
// have identical semantics.
func compareOverloadArms(a, b *type_system.FuncType) specificity {
	if a == nil || b == nil {
		// Nil sorts last — should never happen with well-formed
		// intersections, but defensive against malformed input.
		if a == nil && b == nil {
			return tie
		}
		if a == nil {
			return bMoreSpecific
		}
		return aMoreSpecific
	}

	if cmp := compareBySubtype(a, b); cmp != tie {
		return cmp
	}

	aReq, bReq := countRequiredParams(a), countRequiredParams(b)
	if aReq != bReq {
		if aReq < bReq {
			return aMoreSpecific
		}
		return bMoreSpecific
	}

	aTPRefs, bTPRefs := countTypeParamRefParams(a), countTypeParamRefParams(b)
	if aTPRefs != bTPRefs {
		if aTPRefs < bTPRefs {
			return aMoreSpecific
		}
		return bMoreSpecific
	}

	return tie
}

// sortOverloadArms returns the arms ordered most-specific-first
// using compareOverloadArms. Sort is stable, so arms that compare
// equal preserve their input order — the declared-source-order
// tiebreaker. Returns the input slice (sorted in place); callers
// that need to preserve the original slice should clone before
// calling.
func sortOverloadArms(arms []*type_system.FuncType) []*type_system.FuncType {
	sort.SliceStable(arms, func(i, j int) bool {
		return compareOverloadArms(arms[i], arms[j]) < 0
	})
	return arms
}

// compareBySubtype implements rule 1 of compareOverloadArms.
//
// For arms of equal positive arity, checks whether one arm's params
// are pointwise subtypes of the other's. Returns aMoreSpecific when
// every a.Params[i] <: b.Params[i] AND at least one position is
// strict (the reverse b.Params[i] <: a.Params[i] fails). bMoreSpecific
// symmetrically. tie otherwise — including when arities differ or
// the relation is incomparable.
//
// Owned type-param references are resolved to their upper bound:
// `<K: C>` substitutes C, `<T>` (unbounded or constrained to never)
// is treated as top. This makes bounded generics rank ahead of
// unbounded ones at the same position — `<K: string>(x: K)` beats
// `<T>(x: T)` because `string <: top` — while two unbounded TP refs
// at the same position simply skip (no information). Rule 3 still
// runs separately to order concrete-vs-generic at positions where
// rule 1 ties.
func compareBySubtype(a, b *type_system.FuncType) specificity {
	if len(a.Params) != len(b.Params) || len(a.Params) == 0 {
		return tie
	}
	aTP := typeParamBounds(a)
	bTP := typeParamBounds(b)

	allAIsSubtypeOfB := true
	allBIsSubtypeOfA := true
	sawComparable := false
	for i := range a.Params {
		ap := type_system.Prune(a.Params[i].Type)
		bp := type_system.Prune(b.Params[i].Type)
		aTop, aEff := effectiveParamType(ap, aTP)
		bTop, bEff := effectiveParamType(bp, bTP)
		if aTop && bTop {
			// Both unbounded TP refs at this position — universal on
			// both sides, no specificity signal. Skip without changing
			// the running AND state.
			continue
		}
		// Both true → equivalent shapes; exactly one true → that side
		// is strictly narrower; both false → incomparable (disjoint or
		// unrecognized shape).
		var aIsSubtypeOfB, bIsSubtypeOfA bool
		switch {
		case aTop:
			// a ranges over all types, b is something narrower:
			// b <: a holds, a <: b does not.
			aIsSubtypeOfB, bIsSubtypeOfA = false, true
		case bTop:
			aIsSubtypeOfB, bIsSubtypeOfA = true, false
		default:
			aIsSubtypeOfB = structuralSubtype(aEff, bEff, make(structuralSeen))
			bIsSubtypeOfA = structuralSubtype(bEff, aEff, make(structuralSeen))
		}
		// Any position where neither direction holds — an unrecognized
		// shape (FuncType, intersection, nominal object, …) or genuinely
		// disjoint shapes (e.g. different literal tags) — makes the arms
		// pointwise-incomparable as a whole. Bail out to the source-order
		// tiebreaker rather than letting later positions falsely decide.
		if !aIsSubtypeOfB && !bIsSubtypeOfA {
			return tie
		}
		sawComparable = true
		if !aIsSubtypeOfB {
			allAIsSubtypeOfB = false
		}
		if !bIsSubtypeOfA {
			allBIsSubtypeOfA = false
		}
	}
	if !sawComparable {
		return tie
	}
	if allAIsSubtypeOfB && !allBIsSubtypeOfA {
		return aMoreSpecific
	}
	if allBIsSubtypeOfA && !allAIsSubtypeOfB {
		return bMoreSpecific
	}
	return tie
}

// typeParamBounds returns a name→constraint map for fn's own type
// params. A nil value means the param is unbounded — either no
// constraint or a NeverType constraint (which the old comparator
// treated as equivalent to unbounded). Non-nil values are pruned.
func typeParamBounds(fn *type_system.FuncType) map[string]type_system.Type {
	if len(fn.TypeParams) == 0 {
		return nil
	}
	out := make(map[string]type_system.Type, len(fn.TypeParams))
	for _, tp := range fn.TypeParams {
		var bound type_system.Type
		if tp.Constraint != nil {
			if _, isNever := type_system.Prune(tp.Constraint).(*type_system.NeverType); !isNever {
				bound = type_system.Prune(tp.Constraint)
			}
		}
		out[tp.Name] = bound
	}
	return out
}

func isOwnedTypeParamRef(t type_system.Type, ownTP set.Set[string]) bool {
	if ownTP.Len() == 0 {
		return false
	}
	ref, ok := t.(*type_system.TypeRefType)
	if !ok {
		return false
	}
	ident, ok := ref.Name.(*type_system.Ident)
	if !ok {
		return false
	}
	return ownTP.Contains(ident.Name)
}

// effectiveParamType resolves an owned type-param reference to its
// upper bound for specificity comparison. Returns isTop=true when
// the referenced param is unbounded (so the position ranges over all
// types). For a bounded `<K: C>`, returns (false, C) — substituting
// the constraint lets `<K: string>(x: K)` rank ahead of `<T>(x: T)`
// just as `(x: string)` would. For any non-owned-TP type, returns
// the input unchanged.
func effectiveParamType(
	t type_system.Type,
	ownTP map[string]type_system.Type,
) (isTop bool, eff type_system.Type) {
	ref, ok := t.(*type_system.TypeRefType)
	if !ok {
		return false, t
	}
	ident, ok := ref.Name.(*type_system.Ident)
	if !ok {
		return false, t
	}
	bound, owned := ownTP[ident.Name]
	if !owned {
		return false, t
	}
	if bound == nil {
		return true, nil
	}
	return false, bound
}

// structuralSeen guards against unbounded recursion on cyclic types
// (e.g. recursive object types). Keyed by the (sub, sup) pointer
// pair; revisiting a pair returns true co-inductively, matching
// `unifyInner`'s seen-set discipline.
type structuralSeen map[[2]type_system.Type]bool

// structuralSubtype reports whether sub is a structural subtype of
// sup. The check is intentionally narrow: it covers the kinds the
// issue calls out (literals vs prims, narrowing unions, object
// fields, tuples) plus enough identity/equality cases to handle the
// "same shape on both sides" path that other rules rely on. Anything
// unrecognized returns false so the caller falls through to the
// syntactic tiebreakers.
//
// Both arguments must already be pruned.
//
// TODO(#658): replace with the pure `Check` predicate once that
// lands — it would subsume this hand-rolled traversal and handle
// FuncType, intersections, nominal objects, and mapped types
// without us having to grow the cases here.
func structuralSubtype(sub, sup type_system.Type, seen structuralSeen) bool {
	if sub == sup {
		return true
	}
	key := [2]type_system.Type{sub, sup}
	if seen[key] {
		return true
	}
	seen[key] = true

	// Union on sub: every member must be subtype of sup.
	if subU, ok := sub.(*type_system.UnionType); ok {
		for _, m := range subU.Types {
			if !structuralSubtype(type_system.Prune(m), sup, seen) {
				return false
			}
		}
		return true
	}
	// Union on sup: sub must be subtype of some member.
	if supU, ok := sup.(*type_system.UnionType); ok {
		for _, m := range supU.Types {
			if structuralSubtype(sub, type_system.Prune(m), seen) {
				return true
			}
		}
		return false
	}

	// Same identical primitive.
	if subP, ok := sub.(*type_system.PrimType); ok {
		if supP, ok := sup.(*type_system.PrimType); ok {
			return subP.Prim == supP.Prim
		}
		return false
	}

	// Literal on sub.
	if subL, ok := sub.(*type_system.LitType); ok {
		if supL, ok := sup.(*type_system.LitType); ok {
			return subL.Lit.Equal(supL.Lit)
		}
		if supP, ok := sup.(*type_system.PrimType); ok {
			return litMatchesPrim(subL, supP)
		}
		return false
	}

	// Tuple: pointwise equal-arity subtype.
	if subT, ok := sub.(*type_system.TupleType); ok {
		supT, ok := sup.(*type_system.TupleType)
		if !ok || len(subT.Elems) != len(supT.Elems) {
			return false
		}
		for i := range subT.Elems {
			if !structuralSubtype(type_system.Prune(subT.Elems[i]),
				type_system.Prune(supT.Elems[i]), seen) {
				return false
			}
		}
		return true
	}

	// Object: every property in sup must appear in sub with a subtype
	// value. Extra fields in sub are fine (width subtyping). Anything
	// other than plain PropertyElem makes us bail out — the issue's
	// motivating case is `{kind: "click"}` vs `{kind: string}`, not
	// methods / index signatures, so this is sufficient.
	if subO, ok := sub.(*type_system.ObjectType); ok {
		supO, ok := sup.(*type_system.ObjectType)
		if !ok {
			return false
		}
		subProps := plainProps(subO)
		supProps := plainProps(supO)
		if subProps == nil || supProps == nil {
			return false
		}
		for name, supV := range supProps {
			subV, ok := subProps[name]
			if !ok {
				return false
			}
			if !structuralSubtype(type_system.Prune(subV),
				type_system.Prune(supV), seen) {
				return false
			}
		}
		return true
	}

	// TypeRefType on both sides referring to the same alias.
	if subR, ok := sub.(*type_system.TypeRefType); ok {
		if supR, ok := sup.(*type_system.TypeRefType); ok {
			if subR.TypeAlias != nil && subR.TypeAlias == supR.TypeAlias &&
				len(subR.TypeArgs) == len(supR.TypeArgs) {
				for i := range subR.TypeArgs {
					if !structuralSubtype(type_system.Prune(subR.TypeArgs[i]),
						type_system.Prune(supR.TypeArgs[i]), seen) {
						return false
					}
				}
				return true
			}
		}
	}

	return false
}

func litMatchesPrim(lit *type_system.LitType, prim *type_system.PrimType) bool {
	switch lit.Lit.(type) {
	case *type_system.StrLit:
		return prim.Prim == type_system.StrPrim
	case *type_system.NumLit:
		return prim.Prim == type_system.NumPrim
	case *type_system.BoolLit:
		return prim.Prim == type_system.BoolPrim
	case *type_system.BigIntLit:
		return prim.Prim == type_system.BigIntPrim
	}
	return false
}

// plainProps collects PropertyElems into a name→value map. Returns
// nil if the object carries any non-PropertyElem (method, index sig,
// mapped, etc.) so the caller bails on the subtype check rather than
// silently ignoring those elems and producing a misleading answer.
func plainProps(o *type_system.ObjectType) map[string]type_system.Type {
	out := make(map[string]type_system.Type, len(o.Elems))
	for _, e := range o.Elems {
		switch e := e.(type) {
		case *type_system.PropertyElem:
			out[e.Name.String()] = e.Value
		default:
			return nil
		}
	}
	return out
}

// countTypeParamRefParams counts params whose top-level type (after Prune)
// is a TypeRefType naming one of fn's own type params. Used by rule 3 to
// rank concrete-typed params ahead of generic ones.
func countTypeParamRefParams(fn *type_system.FuncType) int {
	if len(fn.TypeParams) == 0 {
		return 0
	}
	names := set.NewSet[string]()
	for _, tp := range fn.TypeParams {
		names.Add(tp.Name)
	}
	n := 0
	for _, p := range fn.Params {
		if isOwnedTypeParamRef(type_system.Prune(p.Type), names) {
			n++
		}
	}
	return n
}

func countRequiredParams(fn *type_system.FuncType) int {
	n := 0
	for _, p := range fn.Params {
		if p.Optional {
			continue
		}
		if _, isRest := type_system.Prune(p.Type).(*type_system.RestSpreadType); isRest {
			continue
		}
		n++
	}
	return n
}
