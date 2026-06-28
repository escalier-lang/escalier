package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- PR 13: deep, uniform `mut` and `readonly` ---

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
		want string
	}{
		{"owned immutable", "fn f(p: {a: {x: number}}) { p.a.x = 5 }", "1:29-1:38: cannot constrain immutable object <: mutable object"},
		{"immutable borrow", "fn f(p: &{a: {x: number}}) { p.a.x = 5 }", "1:30-1:39: cannot constrain immutable object <: mutable object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			// The inner field-read result is a fresh variable; the message resolves it
			// to its concrete bound so it reads `object`, not an internal `t{N}`.
			require.Equal(t, []string{tc.want}, messagesWithSpan(errs))
		})
	}
}

// A fully fresh literal binds to a deeply-mutable annotation at every level.
func TestDeepMutLowersFreshLiteral(t *testing.T) {
	values, _, errs := inferSource(t, `val w: mut {a: {b: {c: number}}} = {a: {b: {c: 0}}}`)
	require.Empty(t, errs)
	require.Equal(t, "mut {a: {b: {c: number}}}", values["w"])
}

// An explicit nested `mut` field or element inside a non-mut container is rejected at
// the annotation site (#779): `mut {a: mut {x}}` and `mut [mut {x}]` each carry an
// owned-mut cell on a field of an inner immutable container. The cell is recovered to
// its bare inner so the surrounding annotation still resolves, and the fresh literal
// upgrades into that bare target.
func TestNestedMutFieldAnnotationRejected(t *testing.T) {
	const msg = "owned-mutable field annotation is not allowed; the enclosing context decides mutability — wrap the whole annotation in `mut` to make this field writable, or use interior mutability"
	t.Run("object", func(t *testing.T) {
		values, _, errs := inferSource(t, `val w: mut {a: mut {x: number}} = {a: {x: 0}}`)
		require.Equal(t, []string{"1:20-1:21: " + msg}, messagesWithSpan(errs))
		require.Equal(t, "mut {a: {x: number}}", values["w"])
	})
	t.Run("tuple", func(t *testing.T) {
		values, _, errs := inferSource(t, `val w: mut [mut {x: number}] = [{x: 0}]`)
		require.Equal(t, []string{"1:17-1:18: " + msg}, messagesWithSpan(errs))
		require.Equal(t, "mut [{x: number}]", values["w"])
	})
}

// `readonly` rejects `obj.a = …` even on an owned-mutable enclosing object.
func TestReadonlyRejectsFieldReassignment(t *testing.T) {
	_, _, errs := inferSource(t, "fn f(obj: mut {readonly a: number}) { obj.a = 5 }")
	require.Equal(t, []string{"1:39-1:48: cannot assign to readonly property: a"}, messagesWithSpan(errs))
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
		require.Equal(t, []string{"1:44-1:58: cannot assign to readonly property: a"}, messagesWithSpan(errs))
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

// Borrowing a field of a deep-mut container with `&mut obj.a` yields a clean
// `&mut {x: number}`: the deep-mut read view of `a` off the mutable receiver is a
// mutable borrow of its bare inner. Routing the read through the fresh check var
// instead would let the co-occurrence pass widen it to a union and strip the borrow.
func TestExplicitBorrowOfDeepMutFieldPeels(t *testing.T) {
	src := "fn f(p: mut {a: {x: number}}) { return &mut p.a }"
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {a: {x: number}}) -> &mut {x: number}", values["f"])
}

// A readonly source field can't fill a writable target field, but the reverse is
// fine. A readonly target supports only the covariant read view, so a wider
// source can fill it through width subtyping.
func TestReadonlySubtypingFlowsThroughCallAndReturn(t *testing.T) {
	t.Run("call: readonly source into writable param", func(t *testing.T) {
		src := `fn sink(o: mut {a: number}) {}
fn f(obj: mut {readonly a: number}) { sink(obj) }`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"2:39-2:48: readonly field a cannot satisfy a writable field requirement"}, messagesWithSpan(errs))
	})
	t.Run("return: readonly source as writable return", func(t *testing.T) {
		src := "fn f(obj: mut {readonly a: number}) -> mut {a: number} { return obj }"
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"1:1-1:70: readonly field a cannot satisfy a writable field requirement"}, messagesWithSpan(errs))
	})
	t.Run("call: writable source into readonly param is fine", func(t *testing.T) {
		src := `fn sink(o: mut {readonly a: number}) {}
fn f(obj: mut {a: number}) { sink(obj) }`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("call: wider source field fills inexact readonly target", func(t *testing.T) {
		// The write-back is skipped for readonly targets, so width subtyping accepts.
		src := `fn sink(o: mut {readonly a: {x: number, ...}}) {}
fn f(obj: mut {a: {x: number, y: number}}) { sink(obj) }`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
}

// An owned-mutable field inside an immutable container — `{a: mut {x}}` — is now
// rejected at the annotation site (#779): interior mutability is the proper mechanism
// for that case (#618), and inference never produces the shape either, so the type is
// no longer reachable. The annotation reports MutFieldError and recovers to the bare
// `{a: {x}}`; the body's write through the immutable receiver then fails as before.
func TestOwnedMutFieldAnnotationRejected(t *testing.T) {
	src := "fn f(p: {a: mut {x: number}}) { p.a.x = 5 }"
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"1:17-1:18: owned-mutable field annotation is not allowed; the enclosing context decides mutability — wrap the whole annotation in `mut` to make this field writable, or use interior mutability",
		"1:33-1:42: cannot constrain immutable object <: mutable object",
	}, messagesWithSpan(errs))
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
	require.Equal(t, []string{"1:33-1:40: cannot assign to readonly property: a"}, messagesWithSpan(errs))
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
	src := `fn f(obj: mut {readonly a: {x: number, y: number}}) { obj.a.x = 5
 obj.a.y = 6 }`
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

// --- PR 14: lazy deep-mut representation ---

// Under the lazy form (PR 14), the mut-context flag pins a nested field invariant
// inside a mutable wrapper. A field whose type is a strict subtype of the target's
// field passes the covariant read view but fails the contravariant write view, so a
// `mut {a: {x: number}}` value cannot fill a `mut {a: {x: number | string}}` destination —
// the same invariance the eager per-cell `mut` lowering produced.
func TestLazyDeepMutPinsNestedFieldInvariant(t *testing.T) {
	src := `fn sink(q: mut {a: {x: number | string}}) {}
fn f(p: mut {a: {x: number}}) { sink(p) }`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{"2:33-2:40: cannot constrain string <: number"}, messagesWithSpan(errs))
}

// The same shapes under an immutable wrapper are covariant, so the strict-subtype
// field is accepted through ordinary width/depth subtyping. This is the contrast
// that the mut-context flag draws: invariant inside a mutable wrapper, covariant
// outside one.
func TestImmutableWrapperKeepsNestedFieldCovariant(t *testing.T) {
	src := `fn sink(q: {a: {x: number | string}}) {}
fn f(p: {a: {x: number}}) { sink(p) }`
	_, _, errs := inferSource(t, src)
	require.Empty(t, errs)
}

// --- #779: no owned-mutable field inside a non-mut container ---

// A `&`/`&mut` borrow field inside a non-mut container stays legal: it references
// external storage, not an interior owned-mutable cell, so #779 leaves it alone.
func TestBorrowFieldInImmutableContainerIsLegal(t *testing.T) {
	t.Run("shared borrow field", func(t *testing.T) {
		values, _, errs := inferSource(t, "fn f(p: {a: &{x: number}}) -> &{x: number} { return p.a }")
		require.Empty(t, errs)
		require.Equal(t, "fn <'a>(p: {a: &'a {x: number}}) -> &'a {x: number}", values["f"])
	})
	t.Run("mutable borrow field", func(t *testing.T) {
		values, _, errs := inferSource(t, "fn f(p: {a: &mut {x: number}}) { p.a.x = 5 }")
		require.Empty(t, errs)
		require.Equal(t, "fn (p: {a: &mut {x: number}}) -> void", values["f"])
	})
}

// The #779 round-trip: a nested write infers a mut container whose displayed
// signature is a valid annotation, and a caller passing exactly that displayed type
// is accepted. Inference never produces a `mut` field inside a non-mut container, so
// what it prints can always be written back.
func TestNestedWriteInfersMutContainerAndRoundTrips(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "one level",
			src:  "fn foo(obj) { obj.p.x = 5 }",
			want: "fn (obj: mut {p: {x: number}}) -> void",
		},
		{
			name: "three levels",
			src:  "fn foo(obj) { obj.a.b.c = 5 }",
			want: "fn (obj: mut {a: {b: {c: number}}}) -> void",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Empty(t, errs)
			require.Equal(t, tc.want, values["foo"])
		})
	}

	// Re-feeding the displayed type as a caller's annotation type-checks, so the
	// signature genuinely round-trips.
	t.Run("round-trip caller", func(t *testing.T) {
		src := `fn foo(obj) { obj.p.x = 5 }
fn caller(a: mut {p: {x: number}}) { foo(a) }`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
}
