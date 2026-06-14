package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- M4 C3: field-write inference + read-after-write ---
//
// A field write `recv.prop = source` extends inferAssign's member-target branch. It
// constrains `recv <: mut {prop: widen(source), ...}` — a mutable, inexact
// one-property requirement — and the C3 coalesce fold collapses every selection on
// the receiver (reads and writes) into one `mut` object once any field is written.
// The stored value is widened (5 ⇒ number) because writing through a `mut` receiver
// is itself a mutation. These tests exercise the feature end to end through inferred
// function signatures.

// Two writes on an inferred param fold into a single `mut` object: each write
// contributes a mutable one-property requirement, and the fold unions them and wraps
// the whole object in `mut`.
func TestInferMemberAssignTwoWrites(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(obj) { obj.x = 5\n obj.y = 10 }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {x: number, y: number}) -> void", values["foo"])
}

// Read-after-write: a read of a field just written to the same receiver returns the
// recorded concrete (widened) type, not a fresh var, so `obj.x = 5; return obj.x` is
// `number`. The receiver renders `mut {x: number}` from the single write.
func TestInferMemberAssignReadAfterWrite(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(obj) { obj.x = 5\n return obj.x }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {x: number}) -> number", values["foo"])
}

// A mixed read and write folds into ONE `mut` object rather than the spike's
// `{bar: …} & mut {baz: …}` intersection: the presence of any write makes every
// selection — the read-only `bar` included — fold into the mutable object. The
// read-only field is unconstrained, so it renders `unknown`.
func TestInferMemberAssignMixedReadWrite(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(obj) { val x = obj.bar\n obj.baz = 5 }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {bar: unknown, baz: number}) -> void", values["foo"])
}

// A compound written value widens recursively via the shared widen (B3): writing
// `{x: 0}` stores `{x: number}`, not the literal `{x: 0}`.
func TestInferMemberAssignCompoundValueWidens(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(obj) { obj.p = {x: 0} }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {p: {x: number}}) -> void", values["foo"])
}

// The assignment expression evaluates to the value just stored, so its type is the
// widened source: `val r = (obj.x = 5)` ⇒ r: number, used inside a function so the
// receiver is an inferable place.
func TestInferMemberAssignValueIsStored(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(obj) { val r = (obj.x = 5)\n return r }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {x: number}) -> number", values["foo"])
}

// Writing different literal values to the same field is the same widened primitive,
// so the field stays `number` (no contradiction between `5` and `10`).
func TestInferMemberAssignSameFieldTwice(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(obj) { obj.x = 5\n obj.x = 10 }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {x: number}) -> void", values["foo"])
}
