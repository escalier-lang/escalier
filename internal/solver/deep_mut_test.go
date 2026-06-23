package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- PR 13: deep `mut`, type-parameter inertness, and `readonly` ---

// `mut` is deep: a `mut {a: {x}}` annotation makes every nested layer writable, so
// reading `p.a` yields a mutable view and `p.a.x = 5` checks. Before PR 13 the inner
// `a` was immutable and the nested write was rejected. The rendered param shows the
// `mut` reaching each object layer.
func TestDeepMutEnablesNestedWrite(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "owned mut, one level",
			src:  "fn f(p: mut {a: {x: number}}) { p.a.x = 5 }",
			want: "fn (p: mut {a: mut {x: number}}) -> void",
		},
		{
			name: "borrowed &mut, one level",
			src:  "fn f(p: &mut {a: {x: number}}) { p.a.x = 5 }",
			want: "fn (p: mut {a: mut {x: number}}) -> void",
		},
		{
			name: "owned mut, three levels",
			src:  "fn f(p: mut {a: {b: {c: number}}}) { p.a.b.c = 5 }",
			want: "fn (p: mut {a: mut {b: mut {c: number}}}) -> void",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Empty(t, errs)
			require.Equal(t, tc.want, values["f"])
		})
	}
}

// An immutable annotation is shallow-immutable: `mut` is absent, so the nested field
// stays immutable and a nested write is rejected. This holds for both an owned
// immutable object and an immutable borrow, confirming deep `mut` distributes only
// the `mut`/`&mut` modifier, never adding mutability an immutable annotation withheld.
func TestImmutableAnnotationRejectsNestedWrite(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"owned immutable", "fn f(p: {a: {x: number}}) { p.a.x = 5 }"},
		{"immutable borrow", "fn f(p: &{a: {x: number}}) { p.a.x = 5 }"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			// The write fails on the field-read borrow `&{x: t3}`, whose immutable
			// wrapper cannot fill the mutable write slot. The inner is the read's
			// fresh result var, so the message names it rather than `object`.
			require.Equal(t, []string{
				"cannot constrain immutable t3 <: mutable object",
			}, Messages(errs))
		})
	}
}

// A deeply-mutable annotation lowers to nested `mut` cells, and a fully fresh literal
// is upgraded the whole way down. The rendered binding shows `mut` on every object
// layer, so the inner objects are owned-mutable rather than the shallow immutable they
// were before PR 13.
func TestDeepMutLowersFreshLiteral(t *testing.T) {
	values, _, errs := inferSource(t, `val w: mut {a: {b: {c: number}}} = {a: {b: {c: 0}}}`)
	require.Empty(t, errs)
	require.Equal(t, "mut {a: mut {b: mut {c: number}}}", values["w"])
}

// `readonly` forbids reassigning a field. Writing `obj.a = …` on a `readonly a`
// field is rejected even when the enclosing object is owned-mutable.
func TestReadonlyRejectsFieldReassignment(t *testing.T) {
	_, _, errs := inferSource(t, "fn f(obj: mut {readonly a: number}) { obj.a = 5 }")
	require.Equal(t, []string{"cannot assign to readonly property: a"}, Messages(errs))
}

// `readonly` governs only the field slot, not the deep mutability of its value. The
// field's value is still made mutable by the enclosing `mut`, so mutating THROUGH the
// field (`obj.a.b = 5`) checks while REASSIGNING the field (`obj.a = …`) is rejected.
// The rendered type shows the two axes side by side: `readonly a: mut {b: number}`.
func TestReadonlyPermitsValueMutationButNotReassignment(t *testing.T) {
	t.Run("mutate the value", func(t *testing.T) {
		values, _, errs := inferSource(t, "fn f(obj: mut {readonly a: {b: number}}) { obj.a.b = 5 }")
		require.Empty(t, errs)
		require.Equal(t, "fn (obj: mut {readonly a: mut {b: number}}) -> void", values["f"])
	})
	t.Run("reassign the field", func(t *testing.T) {
		_, _, errs := inferSource(t, "fn f(obj: mut {readonly a: {b: number}}) { obj.a = {b: 9} }")
		require.Equal(t, []string{"cannot assign to readonly property: a"}, Messages(errs))
	})
}

// A `readonly` field round-trips through the printer: a `readonly a: number` annotation
// renders back as `readonly a: number`, so the displayed type is a valid annotation.
func TestReadonlyRendersOnReadField(t *testing.T) {
	values, _, errs := inferSource(t, "fn f(obj: {readonly a: number}) -> number { return obj.a }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: {readonly a: number}) -> number", values["f"])
}

// applyDeepMut is inert at a type parameter: it sets `mut` on the concrete object and
// tuple structure but leaves a TypeVarType field untouched, the same pointer it was
// handed. This is the M4-expressible core of `mut Foo<Point>` == `(mut Foo)<Point>` —
// the full generic forms (`mut Array<Point>`, `mut Line<Point>`) need the alias and
// type-argument machinery of M7 and are deferred to that milestone.
func TestApplyDeepMutLeavesTypeParameterInert(t *testing.T) {
	tv := &soltype.TypeVarType{ID: 99}
	concrete := &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
		&soltype.PropertyElem{Name: "x", Type: &soltype.PrimType{Prim: soltype.NumPrim}},
	}}
	inner := &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
		&soltype.PropertyElem{Name: "p", Type: tv},
		&soltype.PropertyElem{Name: "q", Type: concrete},
	}}
	got := applyDeepMut(inner)
	obj, ok := got.(*soltype.ObjectType)
	require.True(t, ok)

	// The type-parameter field is returned unchanged, same pointer, no `mut` wrapper.
	pProp, ok := obj.Prop("p")
	require.True(t, ok)
	require.Same(t, soltype.Type(tv), pProp.Type)

	// The concrete object field is wrapped in owned-mutable and recurses.
	qProp, ok := obj.Prop("q")
	require.True(t, ok)
	qRef, ok := qProp.Type.(*soltype.RefType)
	require.True(t, ok)
	require.True(t, qRef.Mut)
	require.Nil(t, qRef.Lt)
}
