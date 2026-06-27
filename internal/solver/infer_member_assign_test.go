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
// selection — the read-only `bar` included — fold into the mutable object. Folding
// `bar` into the `mut` object makes it invariant (#737), so its var occurs in both
// polarities and is retained as a type parameter `T0` the caller picks, rather than
// inlined to `unknown`. The written `baz` is the widened `number`.
func TestInferMemberAssignMixedReadWrite(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(obj) { val x = obj.bar\n obj.baz = 5 }")
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(obj: mut {bar: T0, baz: number}) -> void", values["foo"])
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

// A written field and a read-only field that ESCAPES (is returned) fold into one
// `mut` object: the written `x` is `number`, while the returned `y` becomes a real
// type parameter rather than collapsing to `unknown`, because it occurs in an output
// position. This is the key interplay between the C3 mut-merge and generalization.
func TestInferMemberAssignWrittenAndEscapingReadField(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(obj) { obj.x = 5\n return obj.y }")
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(obj: mut {x: number, y: T0}) -> T0", values["foo"])
}

// Write-after-read on the SAME field needs no `written`-map support: the read mints
// `T0` and constrains `obj <: {x: T0}`, the later write adds `obj <: mut {x: number}`,
// and the two upper bounds merge so the field folds to `T0 & number`. The read's
// value (returned `x`) stays `T0`. This pins the plan's claim that write-after-read
// falls out of ordinary constraint accumulation, the reverse of read-after-write.
func TestInferMemberAssignWriteAfterRead(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(obj) { val x = obj.x\n obj.x = 5\n return x }")
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(obj: mut {x: T0 & number}) -> T0", values["foo"])
}

// A write through a nested receiver marks the WHOLE container `mut` (#779): writing
// `obj.p.x` makes `obj` itself mutable rather than nesting an owned-mut cell on the
// `p` field. `mut` is deep, so `mut {p: {x: number}}` already makes `p.x` writable,
// and unlike the rejected `{p: mut {x: number}}` it is a valid annotation — the
// displayed signature round-trips. The cost is precision: a caller must pass a mutable
// container even though only the nested field is written.
func TestInferMemberAssignNestedReceiver(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(obj) { obj.p.x = 5 }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {p: {x: number}}) -> void", values["foo"])
}

// An `open` param's written object stays row-polymorphic: the C3 fold passes the
// var's Open flag to mergeObjectGroup, so the merged `mut` object is inexact and
// callers may pass an object with extra fields.
func TestInferMemberAssignOpenParam(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(open obj) { obj.x = 5 }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {x: number, ...}) -> void", values["foo"])
}

// A written receiver that ESCAPES (the whole object is returned) is not sealed: it
// occurs in an output position, so the param keeps an open row and renders as the
// written requirement intersected with the returned type parameter.
func TestInferMemberAssignWrittenObjectEscapes(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(obj) { obj.x = 5\n return obj }")
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(obj: T0 & mut {x: number}) -> T0", values["foo"])
}

// Writing a parameter's value into a field LINKS their types (#737). The write
// `obj.x = v` makes the field's type the variable `v`, and because the field is
// `mut` — hence invariant — `v` occurs in BOTH polarities, so single-polarity
// elimination retains it as a shared type parameter instead of inlining each
// occurrence to `unknown`. So `fn foo(obj, v) { obj.x = v }` infers the tighter
// `fn <T0>(obj: mut {x: T0}, v: T0) -> void`. The mut-field invariance reaches the
// occurrence analysis via recordMutWriteView (simplify.go).
func TestInferMemberAssignVariableValueLinked(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(obj, v) { obj.x = v }")
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(obj: mut {x: T0}, v: T0) -> void", values["foo"])
}

// Writing one field of a concretely-typed (annotated) mut object checks: the field
// write lowers to the inexact requirement `mut {x, ...}`, and the RefType rule's
// per-field write view pins x invariantly while tolerating the object's other
// declared fields. Before the per-field write view this reported spurious
// "missing property: y" / "inexact <: exact" errors.
//
// The annotated `mut` param originates a borrow lifetime (D2), but it is unused in
// the void result, so D4's display-time elision drops it and the param renders as
// plain owned-mutable `mut {…}`.
func TestInferMemberAssignAnnotatedMutObject(t *testing.T) {
	values, _, errs := inferSource(t, "fn f(obj: mut {x: number, y: string}) { obj.x = 5 }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {x: number, y: string}) -> void", values["f"])
}

// The named field stays invariant: storing a string into a number field of an
// annotated mut object is rejected in both directions (the read view number <:
// string and the write-back string <: number), so the relaxation is width-only.
func TestInferMemberAssignAnnotatedMutWrongType(t *testing.T) {
	_, _, errs := inferSource(t, "fn f(obj: mut {x: number, y: string}) { obj.x = \"bad\" }")
	require.Equal(t, []string{
		"1:41-1:54: cannot constrain number <: string",
		"1:41-1:54: cannot constrain string <: number",
	}, messagesWithSpan(errs))
}

// Writing a field absent from an EXACT annotated mut object still errors: the read
// view demands the object carry the written field.
func TestInferMemberAssignAnnotatedMutMissingField(t *testing.T) {
	src := "fn f(obj: mut {x: number}) { obj.z = 5 }"
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{"1:15-1:26: object is missing property: z"}, messagesWithSpan(errs))
}

// KNOWN GAP: two writes of INCOMPATIBLE types to one field produce an uninhabited
// `number & string` rather than an error — each write is an independent upper bound
// on the receiver var with no constraint relating them. Pinned so the gap is
// explicit; a future soundness pass over conflicting writes should surface an error
// here and update this assertion.
// TODO(#738): report conflicting writes to one field instead of folding to an
// uninhabited intersection.
func TestInferMemberAssignConflictingWritesNoError(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(obj) { obj.x = 5\n obj.x = \"hi\" }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {x: number & string}) -> void", values["foo"])
}
