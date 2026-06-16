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

// Returning a freshly-constructed mutable object carries no borrow lifetime: the
// object is owned (Lt nil), not borrowed, so the result renders as a bare
// owned-mutable `mut {…}` with no `'l`.
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
