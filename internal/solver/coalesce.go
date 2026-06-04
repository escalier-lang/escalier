package solver

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/soltype"
)

// coalesce walks a bound-carrying soltype.Type and returns a *coalesced*
// soltype.Type in which every TypeVarType has been inlined to its bounds
// (Delta #1 in m1-implementation-plan §2.2): positive position ⇒ the union of
// the variable's lower bounds, negative position ⇒ the intersection of its
// upper bounds, with empty bounds collapsing to never (⊥, positive) or unknown
// (⊤, negative).
//
// It is a package-private free function in M1 — it needs no Context (no shared
// counters or occurrence state until M3 reintroduces them). Unlike the spike,
// M1's coalescer is uniformly inlining: no bipolar-variable retention, no
// occurrence-analysis input, no named-ref output node. That whole
// polymorphism-rendering bundle lands in M3 (§3.3).
//
// M1 needs no `seen` recursion guard: the M1 type set has no recursive formers
// (no aliases, no recursive types), so a uniform-inline walk terminates on the
// bound graph as-is. M3 adds the guard when bipolar retention and aliases make
// recursive structures in coalesced output possible.
func coalesce(t soltype.Type, pol soltype.Polarity) soltype.Type {
	switch t := t.(type) {
	case *soltype.PrimType, *soltype.LitType, *soltype.Void,
		*soltype.NeverType, *soltype.UnknownType:
		return t // atoms pass through
	case *soltype.FuncType:
		params := make([]*soltype.FuncParam, len(t.Params))
		for i, p := range t.Params {
			// Params are contravariant, so flip polarity.
			params[i] = &soltype.FuncParam{Pattern: p.Pattern, Type: coalesce(p.Type, pol.Flip())}
		}
		return &soltype.FuncType{Params: params, Ret: coalesce(t.Ret, pol)} // covariant return
	case *soltype.TupleType:
		elems := make([]soltype.Type, len(t.Elems))
		for i, e := range t.Elems {
			elems[i] = coalesce(e, pol) // covariant elements
		}
		return &soltype.TupleType{Elems: elems}
	case *soltype.RecordType:
		fields := make([]*soltype.RecordField, len(t.Fields))
		for i, f := range t.Fields {
			fields[i] = &soltype.RecordField{Name: f.Name, Type: coalesce(f.Type, pol)} // covariant fields
		}
		return &soltype.RecordType{Fields: fields}
	case *soltype.TypeVarType:
		// Uniform inline: drop the variable, keep only its (recursively
		// coalesced) bounds in the current polarity.
		bounds := make([]soltype.Type, 0, len(t.BoundsAt(pol)))
		for _, b := range t.BoundsAt(pol) {
			bounds = append(bounds, coalesce(b, pol))
		}
		if len(bounds) == 0 {
			if pol == soltype.Positive {
				return &soltype.NeverType{} // ⊥ — empty positive (the identity of |)
			}
			return &soltype.UnknownType{} // ⊤ — empty negative (the identity of &)
		}
		return combine(pol, dedup(bounds))
	}
	panic(fmt.Sprintf("coalesce: unhandled %T", t))
}

// combine builds a soltype.UnionType (Positive) or soltype.IntersectionType
// (Negative) of parts, returning the sole element directly when only one
// remains. The UnionType/IntersectionType nodes ship in M1 (soltype/type.go) so
// combine can always return a native soltype.Type.
func combine(pol soltype.Polarity, parts []soltype.Type) soltype.Type {
	if len(parts) == 1 {
		return parts[0]
	}
	if pol == soltype.Positive {
		return &soltype.UnionType{Types: parts}
	}
	return &soltype.IntersectionType{Types: parts}
}

// dedup removes structurally-equal parts, preserving first-occurrence order.
// The spike deduplicated by rendered string (via type_system.PrintType); M1
// has no printer in `solver` yet (it ships in PR4, in `soltype`), so M1
// deduplicates by structural equality instead.
func dedup(parts []soltype.Type) []soltype.Type {
	out := make([]soltype.Type, 0, len(parts))
	for _, p := range parts {
		dup := false
		for _, kept := range out {
			if equalType(p, kept) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, p)
		}
	}
	return out
}

// equalType is structural equality over the M1 coalesced type set. Coalesced
// output contains no TypeVarTypes (every variable has been inlined), so the
// variable case is unreachable here and intentionally omitted.
func equalType(a, b soltype.Type) bool {
	switch a := a.(type) {
	case *soltype.PrimType:
		b, ok := b.(*soltype.PrimType)
		return ok && a.Prim == b.Prim
	case *soltype.LitType:
		b, ok := b.(*soltype.LitType)
		return ok && a.Equal(b)
	case *soltype.Void:
		_, ok := b.(*soltype.Void)
		return ok
	case *soltype.NeverType:
		_, ok := b.(*soltype.NeverType)
		return ok
	case *soltype.UnknownType:
		_, ok := b.(*soltype.UnknownType)
		return ok
	case *soltype.FuncType:
		b, ok := b.(*soltype.FuncType)
		if !ok || len(a.Params) != len(b.Params) {
			return false
		}
		for i := range a.Params {
			if !equalType(a.Params[i].Type, b.Params[i].Type) {
				return false
			}
		}
		return equalType(a.Ret, b.Ret)
	case *soltype.TupleType:
		b, ok := b.(*soltype.TupleType)
		if !ok || len(a.Elems) != len(b.Elems) {
			return false
		}
		for i := range a.Elems {
			if !equalType(a.Elems[i], b.Elems[i]) {
				return false
			}
		}
		return true
	case *soltype.RecordType:
		b, ok := b.(*soltype.RecordType)
		if !ok || len(a.Fields) != len(b.Fields) {
			return false
		}
		// Records are equal up to field order — match by name (RecordType.Field),
		// not position. Well-formed records have unique field names (the solver
		// dedups on construction), so equal lengths plus every a-field matching a
		// b-field by name is a full structural match.
		for _, f := range a.Fields {
			bt, ok := b.Field(f.Name)
			if !ok || !equalType(f.Type, bt) {
				return false
			}
		}
		return true
	case *soltype.UnionType:
		b, ok := b.(*soltype.UnionType)
		return ok && equalTypeSlice(a.Types, b.Types)
	case *soltype.IntersectionType:
		b, ok := b.(*soltype.IntersectionType)
		return ok && equalTypeSlice(a.Types, b.Types)
	}
	return false
}

func equalTypeSlice(a, b []soltype.Type) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalType(a[i], b[i]) {
			return false
		}
	}
	return true
}
