package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- M4 D2: borrow origination + active lifetime step ---
//
// These assert the RAW `'l{id}` lifetime form. D4 adds display-time naming
// (`'a`) and elision of param lifetimes that don't reach the output; until then a
// param-originated lifetime renders under its mint id and is never dropped.

// A `mut` param returned unchanged carries its originated lifetime to the result:
// the same `'l0` appears on both the parameter and the return type, so the borrow
// flows out at the lifetime it came in. This is the IdentityRefReturn acceptance —
// D4 will render the shared lifetime as `fn <'a>(p: mut 'a {x}) -> mut 'a {x}`.
func TestInferIdentityRefReturn(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: mut {x: number}) { return p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut 'l0 {x: number}) -> mut 'l0 {x: number}", values["f"])
}

// Returning a freshly-constructed owned object carries no borrow lifetime: the
// object literal is owned (Lt nil) and not mutable, so the result renders as a
// bare object `{…}` with no `mut` prefix and no `'l` lifetime annotation.
func TestInferFreshObjectReturn(t *testing.T) {
	values, _, errs := inferSource(t, `fn f() { return {x: 5} }`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> {x: 5}", values["f"])
}

// Two `mut` params with distinct annotations originate independent lifetimes:
// `'l0` on the first, `'l1` on the second.
func TestInferDistinctParamLifetimes(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: mut {x: number}, q: mut {y: number}) { return p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut 'l0 {x: number}, q: mut 'l1 {y: number}) -> mut 'l0 {x: number}", values["f"])
}

// Writing a field through an annotated `mut` borrow checks: the receiver carries a
// borrow lifetime `'l0`, and the write requirement's fresh lifetime imposes no
// obligation, so a borrowed receiver of any lifetime satisfies it.
func TestInferFieldWriteThroughBorrowParam(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: mut {x: number}) { p.x = 10 }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut 'l0 {x: number}) -> void", values["f"])
}

// Passing a borrow into a function whose parameter is an OWNED (bare) object is the
// borrow-into-owned-slot escape: the RefType<:bare arm rejects because the source
// carries a lifetime and the target owns its value. This is the only path that
// exercises the escape guard D2 activated — before D2 every Lt was nil, so it never
// fired.
func TestInferBorrowEscapesIntoOwnedArg(t *testing.T) {
	src := "fn use(o: {x: number}) -> number {\n  return o.x\n}\nfn f(p: mut {x: number}) {\n  return use(p)\n}"
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"borrowed value mut object does not live long enough to satisfy object",
	}, Messages(errs))
}

// The companion to the escape case: passing the same borrow into a function whose
// parameter is itself a `mut` borrow checks. The RefType<:RefType arm relates the
// two lifetimes via constrainLt (the now-active step 3) instead of rejecting, so the
// borrow slot — unlike the owned slot above — admits the borrow.
func TestInferBorrowIntoBorrowArg(t *testing.T) {
	src := "fn use(o: mut {x: number}) -> number {\n  return o.x\n}\nfn f(p: mut {x: number}) {\n  return use(p)\n}"
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut 'l1 {x: number}) -> number", values["f"])
}

// Reading a field after writing it through an annotated `mut` borrow returns the
// written field's type. The receiver is the concrete borrow `mut 'l0 {x: number}`,
// so valueProp peels it via CarrierOf before emitting the read requirement — without
// the peel this would trip the escape guard on the bare read requirement. Unlike the
// usage-inferred read-after-write tests (which key off the `written` map on a
// receiver VAR), this exercises the peel-and-constrain path on a concrete borrow.
func TestInferReadAfterWriteThroughBorrowParam(t *testing.T) {
	src := "fn f(p: mut {x: number}) {\n  p.x = 5\n  return p.x\n}"
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut 'l0 {x: number}) -> number", values["f"])
}

// Writing a field of a NON-`mut` owned object is rejected: the write lowers to the
// mutable requirement `mut {x, ...}`, and an immutable owned object cannot fill a
// mutable slot. This confirms the field-write requirement's fresh lifetime (D2) did
// not loosen the mutability gate — an owned-but-immutable receiver still fails.
func TestInferFieldWriteToImmutableObjectRejected(t *testing.T) {
	src := "fn g(o: {x: number}) {\n  o.x = 5\n}"
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"cannot constrain immutable object <: mutable object",
	}, Messages(errs))
}

// Joining two borrows with DISTINCT lifetimes preserves both. equalType compares
// lifetimes (D2), so dedup does not collapse `mut 'l0 {x}` and `mut 'l1 {x}` into a
// single member and silently drop a lifetime. The branch join renders the un-joined
// union here; D3 factors it into the `mut ('l0 | 'l1) {x}` form. Without the Lt
// comparison in equalType this rendered `mut 'l0 {x}`, losing 'l1.
func TestInferDistinctLifetimeBorrowsDoNotCoalesce(t *testing.T) {
	src := "fn f(p: mut {x: number}, q: mut {x: number}) {\n  if true {\n    return p\n  } else {\n    return q\n  }\n}"
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t,
		"fn (p: mut 'l0 {x: number}, q: mut 'l1 {x: number}) -> mut 'l0 {x: number} | mut 'l1 {x: number}",
		values["f"])
}
