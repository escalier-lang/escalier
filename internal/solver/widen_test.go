package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// M4 B3: an un-annotated `var` binding widens its initializer's literal types to
// their primitives, recursively through objects and tuples, so a mutable cell can
// later hold a different value of the same primitive. A `val` is a fixed
// singleton and keeps its literal type. These exercise the rendered binding type
// end-to-end through the real parser pipeline.
func TestInferVarLiteralWidening(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "scalar var widens to its primitive",
			src:  `var a = 5`,
			want: map[string]string{"a": "number"},
		},
		{
			name: "string and bool vars widen",
			src: `
				var s = "hi"
				var b = true
			`,
			want: map[string]string{"s": "string", "b": "boolean"},
		},
		{
			name: "val keeps its literal singleton",
			src:  `val a = 5`,
			want: map[string]string{"a": "5"},
		},
		{
			name: "object var widens each property",
			src:  `var p = {x: 0, y: 0}`,
			want: map[string]string{"p": "{x: number, y: number}"},
		},
		{
			name: "tuple var widens each element",
			src:  `var t = [1, 2]`,
			want: map[string]string{"t": "[number, number]"},
		},
		{
			name: "nesting widens through",
			src:  `var n = {p: {x: 0}}`,
			want: map[string]string{"n": "{p: {x: number}}"},
		},
		{
			name: "object val keeps its literals",
			src:  `val p = {x: 0, y: 0}`,
			want: map[string]string{"p": "{x: 0, y: 0}"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values)
		})
	}
}

// A widened `var` accepts a reassignment of a different value of the same
// primitive, while a value of a different primitive is still rejected — the
// binding is the widened primitive, not `any`.
func TestInferVarWideningReassignment(t *testing.T) {
	t.Run("same-primitive reassignment checks", func(t *testing.T) {
		values, _, errs := inferSource(t, "var a = 5\nfn f() { a = 6 }")
		require.Empty(t, errs)
		require.Equal(t, "number", values["a"])
	})
	t.Run("different-primitive reassignment rejected", func(t *testing.T) {
		src := "var a = 5\nfn f() { a = \"x\" }"
		_, _, errs := inferSource(t, src)
		requireBlame(t, src, errs, `cannot constrain "x" <: number`, `"x"`)
	})
}

// widen's structural arms are exercised directly here because no M4 source can
// yet produce a borrow-typed or inexact var initializer — C3's field-write path
// is the first consumer of the RefType arm, and inexactness only reaches a var
// binding through annotations (which take the annotation, not widening). These
// pin the helper's full contract — literal lowering, recursive object/tuple
// descent preserving Inexact, RefType peel/re-wrap preserving Mut, and
// passthrough of already-widened or still-variable types — that C3 relies on.
func TestWidenHelper(t *testing.T) {
	numLit := &soltype.LitType{Lit: &soltype.NumLit{Value: 5}}
	objLit := &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
		&soltype.PropertyElem{Name: "x", Type: &soltype.LitType{Lit: &soltype.NumLit{Value: 5}}},
	}}

	tests := []struct {
		name string
		in   soltype.Type
		want string
	}{
		{"number literal", numLit, "number"},
		{"string literal", &soltype.LitType{Lit: &soltype.StrLit{Value: "x"}}, "string"},
		{"bool literal", &soltype.LitType{Lit: &soltype.BoolLit{Value: true}}, "boolean"},
		{
			name: "inexact object preserves the tail",
			in: &soltype.ObjectType{Inexact: true, Elems: []soltype.ObjTypeElem{
				&soltype.PropertyElem{Name: "x", Type: &soltype.LitType{Lit: &soltype.NumLit{Value: 5}}},
			}},
			want: "{x: number, ...}",
		},
		{
			name: "inexact tuple preserves the tail",
			in: &soltype.TupleType{Inexact: true, Elems: []soltype.Type{
				&soltype.LitType{Lit: &soltype.NumLit{Value: 1}},
			}},
			want: "[number, ...]",
		},
		{
			name: "mut borrow widens inner and keeps Mut",
			in:   &soltype.RefType{Mut: true, Inner: objLit},
			want: "mut {x: number}",
		},
		{
			name: "object property keeps its optional flag while widening",
			in: &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
				&soltype.PropertyElem{Name: "x", Type: &soltype.LitType{Lit: &soltype.NumLit{Value: 5}}, Optional: true},
			}},
			want: "{x?: number}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, soltype.Print(widen(tt.in)))
		})
	}

	// A type with no literal to lower passes through by identity — widen neither
	// rebuilds nor mutates it. A PrimType is already widened; a TypeVarType is
	// left for the solver (widen never follows its bounds).
	t.Run("already-primitive passes through unchanged", func(t *testing.T) {
		prim := &soltype.PrimType{Prim: soltype.NumPrim}
		require.Same(t, prim, widen(prim))
	})
	t.Run("type variable passes through unchanged", func(t *testing.T) {
		tv := &soltype.TypeVarType{ID: 1}
		require.Same(t, tv, widen(tv))
	})
}

// Widening reaches a literal that flows through a binding reference, not only a
// syntactic literal initializer. `var y = x` (where x is a literal `val`) infers
// y's initializer as a type variable, but the binding var's Widenable flag rides
// through coalescing and lowers the inlined literal, so y widens to the primitive
// and a later reassignment of the same primitive checks — the reference case
// behaves like a direct literal.
func TestInferVarWideningThroughReference(t *testing.T) {
	t.Run("var from a val reference widens to the primitive", func(t *testing.T) {
		values, _, errs := inferSource(t, "val x = 5\nvar y = x")
		require.Empty(t, errs)
		require.Equal(t, "number", values["y"])
	})
	t.Run("reassigning the widened var checks", func(t *testing.T) {
		values, _, errs := inferSource(t, "val x = 5\nvar y = x\nfn f() { y = 6 }")
		require.Empty(t, errs)
		require.Equal(t, "number", values["y"])
	})
	// Reference widening rides the Widenable flag at coalesce time (display, the
	// reassignment slot, the binding's own type), but the literal still flows into
	// the bound graph unwidened, so a read of the reference-widened var into a new
	// binding keeps the precise literal: `val z = y` ⇒ z: 5. A direct literal
	// widens at the constraint level instead, so ITS reads widen (see
	// TestInferVarWideningPropagatesToReads). z: 5 is sound — z is an immutable
	// snapshot of the value 5 — and this pins the narrow remaining corner.
	t.Run("reading a reference-widened var keeps the literal", func(t *testing.T) {
		values, _, errs := inferSource(t, "val x = 5\nvar y = x\nval z = y")
		require.Empty(t, errs)
		require.Equal(t, "number", values["y"])
		require.Equal(t, "5", values["z"])
	})
}

// A read of a directly-widened `var` into another binding widens too: the eager
// constraint-level widening of a direct literal propagates through the bound
// graph, so `var a = 5; val z = a` ⇒ z: number, matching `a`'s own widened type
// (unlike the reference corner above, where the literal reaches the reader
// unwidened).
func TestInferVarWideningPropagatesToReads(t *testing.T) {
	t.Run("scalar read widens", func(t *testing.T) {
		values, _, errs := inferSource(t, "var a = 5\nval z = a")
		require.Empty(t, errs)
		require.Equal(t, "number", values["a"])
		require.Equal(t, "number", values["z"])
	})
	t.Run("object read widens", func(t *testing.T) {
		values, _, errs := inferSource(t, "var p = {x: 0}\nval q = p")
		require.Empty(t, errs)
		require.Equal(t, "{x: number}", values["q"])
	})
}

// A body-level `var` widens through the inferVarDecl path (distinct from the
// top-level inferDeclDef/SCC path the other tests exercise), so reassigning it
// inside the same function checks and the binding reads back as the primitive.
func TestInferVarWideningBodyLevel(t *testing.T) {
	t.Run("direct literal widens and reassigns", func(t *testing.T) {
		values, _, errs := inferSource(t, "fn f() { var a = 5\n  a = 6\n  return a }")
		require.Empty(t, errs)
		require.Equal(t, "fn () -> number", values["f"])
	})
	// A body-level `var` initialized from a REFERENCE widens via the wrapper var's
	// Widenable flag, so the reassignment slot is the primitive and `y = 6` checks
	// — the body-level twin of TestInferVarWideningThroughReference.
	t.Run("reference widens and reassigns", func(t *testing.T) {
		_, _, errs := inferSource(t, "fn f() { val x = 5\n  var y = x\n  y = 6 }")
		require.Empty(t, errs)
	})
}

// Widening through a reference reaches the structural carriers, not only scalars:
// `val o = {x: 0}; var p = o` widens p to {x: number}, and the tuple form to
// [number, number]. This exercises the Widenable flag driving widen's RECURSION
// at coalesce time, distinct from the eager direct-literal path that widens
// `var p = {x: 0}` at the constraint level.
func TestInferVarWideningReferenceStructural(t *testing.T) {
	t.Run("object", func(t *testing.T) {
		values, _, errs := inferSource(t, "val o = {x: 0}\nvar p = o")
		require.Empty(t, errs)
		require.Equal(t, "{x: number}", values["p"])
	})
	t.Run("tuple", func(t *testing.T) {
		values, _, errs := inferSource(t, "val o = [1, 2]\nvar p = o")
		require.Empty(t, errs)
		require.Equal(t, "[number, number]", values["p"])
	})
}

// widenVar is the shared helper both coalescers call. It is unit-tested directly
// because the plain-coalescer call site is unreachable through real source (a
// widenable var is always a binding var, which renders through coalesceScheme),
// and because the negative-polarity no-op cannot arise for a binding var either.
// It widens only a widenable var read in covariant (Positive) position.
func TestWidenVar(t *testing.T) {
	lit := func() soltype.Type { return &soltype.LitType{Lit: &soltype.NumLit{Value: 5}} }
	widenable := &soltype.TypeVarType{ID: 1, Widenable: true}
	plain := &soltype.TypeVarType{ID: 2}
	tests := []struct {
		name string
		v    *soltype.TypeVarType
		pol  soltype.Polarity
		want string
	}{
		{"widenable positive widens", widenable, soltype.Positive, "number"},
		{"widenable negative is a no-op", widenable, soltype.Negative, "5"},
		{"non-widenable positive is a no-op", plain, soltype.Positive, "5"},
		{"non-widenable negative is a no-op", plain, soltype.Negative, "5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, soltype.Print(widenVar(tt.v, tt.pol, lit())))
		})
	}
}

// The freshener copies the Widenable flag onto an instantiated binding var. This
// behavior is currently unreachable from source — a read of a widened binding
// gets the literal propagated concretely, routing around the freshened copy — so
// it is pinned here directly as the defensive contract that keeps Widenable
// parallel to Open. See the freshener note in poly.go.
func TestFreshenCopiesWidenable(t *testing.T) {
	c := newChecker()
	v := c.freshAt(1)
	v.Widenable = true
	out := c.freshenAbove(0, v, 0, map[*soltype.TypeVarType]*soltype.TypeVarType{})
	nv, ok := out.(*soltype.TypeVarType)
	require.True(t, ok)
	require.NotSame(t, v, nv) // a fresh copy, not the original
	require.True(t, nv.Widenable)
}
