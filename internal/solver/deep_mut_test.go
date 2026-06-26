package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- PR 13: deep `mut`, type-parameter inertness, and `readonly` ---

// `mut` is deep: every nested layer becomes writable, so `p.a.x = 5` is legal
// through `mut {a: {x}}` and `&mut {a: {x}}`.
func TestDeepMutEnablesNestedWrite(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "owned mut, one level",
			src:  "fn f(p: mut {a: {x: number}}) { p.a.x = 5 }",
			want: "fn (p: mut {a: {x: number}}) -> void",
		},
		{
			name: "borrowed &mut, one level",
			src:  "fn f(p: &mut {a: {x: number}}) { p.a.x = 5 }",
			want: "fn (p: &mut {a: {x: number}}) -> void",
		},
		{
			name: "owned mut, three levels",
			src:  "fn f(p: mut {a: {b: {c: number}}}) { p.a.b.c = 5 }",
			want: "fn (p: mut {a: {b: {c: number}}}) -> void",
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

// An immutable annotation stays immutable end to end, so a nested write through
// either an owned-immutable or `&` borrow receiver is rejected.
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
			// The blame names the inner field-read result var rather than `object`.
			require.Equal(t, []string{
				"cannot constrain immutable t3 <: mutable object",
			}, Messages(errs))
		})
	}
}

// A fully fresh literal binds to a deeply-mutable annotation at every level.
func TestDeepMutLowersFreshLiteral(t *testing.T) {
	values, _, errs := inferSource(t, `val w: mut {a: {b: {c: number}}} = {a: {b: {c: 0}}}`)
	require.Empty(t, errs)
	require.Equal(t, "mut {a: {b: {c: number}}}", values["w"])
}

// `readonly` rejects `obj.a = …` even on an owned-mutable enclosing object.
func TestReadonlyRejectsFieldReassignment(t *testing.T) {
	_, _, errs := inferSource(t, "fn f(obj: mut {readonly a: number}) { obj.a = 5 }")
	require.Equal(t, []string{"cannot assign to readonly property: a"}, Messages(errs))
}

// `readonly` forbids reassigning the field but not mutating through it: `obj.a.b
// = 5` checks while `obj.a = …` is rejected.
func TestReadonlyPermitsValueMutationButNotReassignment(t *testing.T) {
	t.Run("mutate the value", func(t *testing.T) {
		values, _, errs := inferSource(t, "fn f(obj: mut {readonly a: {b: number}}) { obj.a.b = 5 }")
		require.Empty(t, errs)
		require.Equal(t, "fn (obj: mut {readonly a: {b: number}}) -> void", values["f"])
	})
	t.Run("reassign the field", func(t *testing.T) {
		_, _, errs := inferSource(t, "fn f(obj: mut {readonly a: {b: number}}) { obj.a = {b: 9} }")
		require.Equal(t, []string{"cannot assign to readonly property: a"}, Messages(errs))
	})
}

// An explicit `&obj.f` on a borrow field flows the field's own borrow through
// rather than re-anchoring to the receiver, matching the implicit-read path.
func TestExplicitBorrowOfBorrowFieldFlowsFieldLifetime(t *testing.T) {
	t.Run("immutable borrow field", func(t *testing.T) {
		src := "fn f(obj: {a: &{x: number}}) { return &obj.a }"
		values, _, errs := inferSource(t, src)
		require.Empty(t, errs)
		require.Equal(t, "fn <'a>(obj: {a: &'a {x: number}}) -> &'a {x: number}", values["f"])
	})
	t.Run("mutable borrow field", func(t *testing.T) {
		src := "fn f(obj: {a: &mut {x: number}}) { return &obj.a }"
		values, _, errs := inferSource(t, src)
		require.Empty(t, errs)
		require.Equal(t, "fn <'a>(obj: {a: &'a mut {x: number}}) -> &'a mut {x: number}", values["f"])
	})
}

// A readonly source field can't fill a writable target slot, but the reverse is
// fine. A readonly target supports only the covariant read view, so a wider
// source can fill it through width subtyping.
func TestReadonlySubtypingFlowsThroughCallAndReturn(t *testing.T) {
	t.Run("call: readonly source into writable param", func(t *testing.T) {
		src := "fn sink(o: mut {a: number}) {}\nfn f(obj: mut {readonly a: number}) { sink(obj) }"
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"readonly field a cannot satisfy a writable field requirement"}, Messages(errs))
	})
	t.Run("return: readonly source as writable return", func(t *testing.T) {
		src := "fn f(obj: mut {readonly a: number}) -> mut {a: number} { return obj }"
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"readonly field a cannot satisfy a writable field requirement"}, Messages(errs))
	})
	t.Run("call: writable source into readonly param is fine", func(t *testing.T) {
		src := "fn sink(o: mut {readonly a: number}) {}\nfn f(obj: mut {a: number}) { sink(obj) }"
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("call: wider source field fills inexact readonly target", func(t *testing.T) {
		// The write-back is skipped for readonly targets, so width subtyping accepts.
		src := "fn sink(o: mut {readonly a: {x: number, ...}}) {}\nfn f(obj: mut {a: {x: number, y: number}}) { sink(obj) }"
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
}

// An owned-mutable field through an immutable receiver is read-only: the
// container's immutability reaches into the field via recvMut. The annotation
// itself is the awkward part — `{a: mut {x}}` shouldn't be writable as a user
// type at all, since interior mutability is the proper mechanism (#618). #779
// tracks rejecting the annotation outright once inference is updated to never
// produce the same shape in a return type.
func TestOwnedMutFieldThroughImmutableReceiverRejectsWrite(t *testing.T) {
	src := "fn f(p: {a: mut {x: number}}) { p.a.x = 5 }"
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"cannot constrain immutable object <: mutable object",
	}, Messages(errs))
}

// Chained reads through three deep-mut layers stay mutable, so a depth-3 write
// checks.
func TestDeepMutChainedReadsAllowDeepWrite(t *testing.T) {
	src := "fn f(p: mut {a: {b: {c: number}}}) { p.a.b.c = 5 }"
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {a: {b: {c: number}}}) -> void", values["f"])
}

// A readonly field on an immutable container still reports the readonly error,
// not the immutable-receiver one.
func TestReadonlyFieldOnImmutableContainerStillRejectsWrite(t *testing.T) {
	src := "fn f(p: {readonly a: number}) { p.a = 5 }"
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{"cannot assign to readonly property: a"}, Messages(errs))
}

// The fresh-literal upgrade reaches into tuples too.
func TestDeepMutLowersFreshTupleLiteral(t *testing.T) {
	values, _, errs := inferSource(t, "val w: mut [number, {x: number}] = [1, {x: 0}]")
	require.Empty(t, errs)
	require.Equal(t, "mut [number, {x: number}]", values["w"])
}

// A readonly field's value is still deep-mutable, so multiple writes through it
// check independently.
func TestReadonlyFieldValueIsDeepMutable(t *testing.T) {
	src := "fn f(obj: mut {readonly a: {x: number, y: number}}) { obj.a.x = 5\n obj.a.y = 6 }"
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {readonly a: {x: number, y: number}}) -> void", values["f"])
}

// `readonly a: number` round-trips through the printer.
func TestReadonlyRendersOnReadField(t *testing.T) {
	values, _, errs := inferSource(t, "fn f(obj: {readonly a: number}) -> number { return obj.a }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: {readonly a: number}) -> number", values["f"])
}

// applyDeepMut leaves a TypeVarType field untouched, the M4-expressible core of
// `mut Foo<Point>` == `(mut Foo)<Point>` — full generics land in M7.
func TestApplyDeepMutLeavesTypeParameterInert(t *testing.T) {
	tv := &soltype.TypeVarType{ID: 99}
	concrete := &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
		&soltype.PropertyElem{Name: "x", Type: &soltype.PrimType{Prim: soltype.NumPrim}},
	}}
	inner := &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
		&soltype.PropertyElem{Name: "p", Type: tv},
		&soltype.PropertyElem{Name: "q", Type: concrete},
	}}
	c := newChecker()
	got := c.applyDeepMut(inner)
	obj, ok := got.(*soltype.ObjectType)
	require.True(t, ok)

	// Type-parameter field: same pointer, no `mut` wrapper.
	pProp, ok := obj.Prop("p")
	require.True(t, ok)
	require.Same(t, soltype.Type(tv), pProp.Type)

	// Concrete object field: wrapped in owned-mutable.
	qProp, ok := obj.Prop("q")
	require.True(t, ok)
	qRef, ok := qProp.Type.(*soltype.RefType)
	require.True(t, ok)
	require.True(t, qRef.Mut)
	require.Nil(t, qRef.Lt)
}
