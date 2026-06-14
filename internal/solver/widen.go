package solver

import "github.com/escalier-lang/escalier/internal/soltype"

// widen lowers literal types to their primitives, recursively through the
// structural carriers, so a mutable cell can later hold a different value of the
// same primitive. A number literal `5` widens to `number`, `"x"` to `string`,
// `true` to `boolean`; an ObjectType/TupleType widens each property value /
// element in place, preserving Inexact; a RefType is peeled, its inner widened,
// and re-wrapped via NewRef. Every other type passes through unchanged.
//
// It is the new-checker analogue of internal/checker's widenLiteral /
// widenObjectLiterals / widenTupleLiterals (unify.go). M4 B3 calls it in two
// places, both for an un-annotated `var`:
//   - eagerly on a DIRECT literal initializer in inferVarDeclInit, widening at
//     the constraint level so the widened type propagates through the bound graph
//     to reads of the binding (`var a = 5; val z = a` ⇒ z: number); and
//   - at coalesce time on a Widenable binding var (see widenVar in coalesce.go),
//     which catches a literal that arrives through a REFERENCE (`var y = x`) and
//     is still a variable when inferVarDeclInit runs.
//
// A `val` keeps its fixed literal. C3's field-write path reuses it: writing
// through a `mut` receiver is itself a mutation, so the stored value widens too.
func widen(t soltype.Type) soltype.Type {
	switch t := t.(type) {
	case *soltype.LitType:
		switch t.Lit.(type) {
		case *soltype.NumLit:
			return &soltype.PrimType{Prim: soltype.NumPrim}
		case *soltype.StrLit:
			return &soltype.PrimType{Prim: soltype.StrPrim}
		case *soltype.BoolLit:
			return &soltype.PrimType{Prim: soltype.BoolPrim}
		default:
			return t
		}
	case *soltype.ObjectType:
		elems := make([]soltype.ObjTypeElem, len(t.Elems))
		for i, e := range t.Elems {
			p := soltype.AsProperty(e)
			elems[i] = &soltype.PropertyElem{Name: p.Name, Type: widen(p.Type), Optional: p.Optional}
		}
		return &soltype.ObjectType{Elems: elems, Inexact: t.Inexact}
	case *soltype.TupleType:
		elems := make([]soltype.Type, len(t.Elems))
		for i, e := range t.Elems {
			elems[i] = widen(e)
		}
		return &soltype.TupleType{Elems: elems, Inexact: t.Inexact}
	case *soltype.RefType:
		// widening preserves RefInner-ness — ObjectType/TupleType widen to
		// themselves and a TypeVarType passes through — so the assertion holds;
		// fall back to the unwidened borrow if a future inner ever breaks it.
		if inner, ok := widen(t.Inner).(soltype.RefInner); ok {
			return soltype.NewRef(t.Mut, t.Lt, inner)
		}
		return t
	default:
		return t
	}
}
