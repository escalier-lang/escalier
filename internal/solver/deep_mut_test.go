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
			want: "fn (p: mut {a: {x: number}}) -> void",
		},
		{
			name: "borrowed &mut, one level",
			src:  "fn f(p: &mut {a: {x: number}}) { p.a.x = 5 }",
			want: "fn (p: mut {a: {x: number}}) -> void",
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
	require.Equal(t, "mut {a: {b: {c: number}}}", values["w"])
}

// `readonly` forbids reassigning a field. Writing `obj.a = …` on a `readonly a`
// field is rejected even when the enclosing object is owned-mutable.
func TestReadonlyRejectsFieldReassignment(t *testing.T) {
	_, _, errs := inferSource(t, "fn f(obj: mut {readonly a: number}) { obj.a = 5 }")
	require.Equal(t, []string{"cannot assign to readonly property: a"}, Messages(errs))
}

// `readonly` governs only the field slot, not the deep mutability of its value.
// The field's value is still made mutable by the enclosing `mut`, so mutating
// through the field with `obj.a.b = 5` checks while reassigning the field with
// `obj.a = …` is rejected. The rendered type shows the two axes side by side as
// `readonly a: mut {b: number}`.
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

// An explicit `&obj.f` on a borrow-typed field flows the field's own borrow
// through with its own lifetime rather than peeling and re-anchoring to the
// receiver. The owned-mutable cell that deep `mut` produces is the only carrier
// the explicit borrow re-wraps at the receiver's lifetime. An explicit `&` or
// `&mut` field is left intact. This matches the implicit-read path, which
// flat-copies an immutable borrow field and returns the field's `&mut` at its
// own lifetime.
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

// A readonly source field cannot satisfy a writable target field under a mutable
// borrow, even when no literal assignment is written. Passing a `mut {readonly
// a}` where `mut {a}` is expected, or returning one where the return type is `mut
// {a}`, is rejected with a structural message that names the field and the
// mismatch. The user wrote no assignment, so the assignment-site message would
// be misleading. The reverse direction, a writable source flowing into a readonly
// target, is fine because the target view simply chooses not to write.
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
		// A readonly target slot supports only the covariant read view. The
		// contravariant write-back constraint is skipped, so a wider source field
		// can fill a narrower readonly target through width subtyping. Without the
		// skip the writeBack would constrain target.a <: source.a and reject for
		// the missing `y` property. The target is inexact so the exact-object
		// excess check does not fire on the outer object's `a` either.
		src := "fn sink(o: mut {readonly a: {x: number, ...}}) {}\nfn f(obj: mut {a: {x: number, y: number}}) { sink(obj) }"
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
}

// An owned-mutable field read through an immutable receiver yields an immutable
// view. Without the recvMut downgrade in fieldReadBorrow, a `mut {x}` field on
// an immutable container would still admit `p.a.x = 5`. That would be unsound.
// The field is owned storage inside the container, so the container's
// immutability must reach into it. The recvMut path closes this gap and rejects
// the write.
func TestOwnedMutFieldThroughImmutableReceiverRejectsWrite(t *testing.T) {
	src := "fn f(p: {a: mut {x: number}}) { p.a.x = 5 }"
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"cannot constrain immutable object <: mutable object",
	}, Messages(errs))
}

// Chained reads through several deep-mut layers all yield mutable views, so a
// terminal write at depth 3 checks. The intermediate `.a` and `.a.b` reads each
// borrow with `recvMut` propagated, and the leaf field is then writable.
func TestDeepMutChainedReadsAllowDeepWrite(t *testing.T) {
	src := "fn f(p: mut {a: {b: {c: number}}}) { p.a.b.c = 5 }"
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {a: {b: {c: number}}}) -> void", values["f"])
}

// A `readonly` field on an IMMUTABLE container is still rejected for writes. The
// readonly check fires before any mut-receiver check, so an `obj.a = 5` against a
// `{readonly a: number}` reports the readonly error rather than the immutable-
// receiver error. The two checks are orthogonal axes of the same write rule.
func TestReadonlyFieldOnImmutableContainerStillRejectsWrite(t *testing.T) {
	src := "fn f(p: {readonly a: number}) { p.a = 5 }"
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{"cannot assign to readonly property: a"}, Messages(errs))
}

// The fresh-literal upgrade reaches into nested tuples too. A fully fresh tuple
// literal binds to a deeply-mutable tuple annotation, with every nested object
// becoming owned-mutable. This mirrors the object case and exercises the tuple
// arm of stripOwnedMut.
func TestDeepMutLowersFreshTupleLiteral(t *testing.T) {
	values, _, errs := inferSource(t, "val w: mut [number, {x: number}] = [1, {x: 0}]")
	require.Empty(t, errs)
	require.Equal(t, "mut [number, {x: number}]", values["w"])
}

// A `readonly` field's value may still be mutated through deep `mut`, even when
// the value has multiple fields. Writing each field through `.a.x = …` and
// `.a.y = …` succeeds because `readonly` only forbids reassigning `a`, not
// writing through it.
func TestReadonlyFieldValueIsDeepMutable(t *testing.T) {
	src := "fn f(obj: mut {readonly a: {x: number, y: number}}) { obj.a.x = 5\n obj.a.y = 6 }"
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {readonly a: {x: number, y: number}}) -> void", values["f"])
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
	c := newChecker()
	got := c.applyDeepMut(inner)
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
