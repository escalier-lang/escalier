package simplesub

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResidualKeyofUsageInferredOperand is the M7 flagship: `keyof typeof x`
// where x's type is inferred from usage (field reads), so the operand is NOT
// ground during the value solve. Design A keeps the keyof inert during solving
// and reduces it post-coalescing, once x has coalesced to {a, b} — so the
// return type is the reduced key union "a" | "b". Baseline D (M5) would have
// left this as `keyof t<x>`, since the operand isn't ground until coalescing.
//
//	fn f(x) { x.a; x.b; return keyof typeof x }
//	  ==>  fn (x: {a: unknown, b: unknown}) -> "a" | "b"
//
// (The field value types render as `unknown`: the field-read results are
// otherwise-unconstrained — the spike does not generalize a bare negative-
// position variable to a type parameter, so it coalesces to unknown. keyof only
// depends on the key SET, so the reduction is unaffected. The point of the
// milestone is the return type: the residual reduced post-solve.)
func TestResidualKeyofUsageInferredOperand(t *testing.T) {
	f := &Lam{Params: []string{"x"}, Body: &Block{Exprs: []Term{
		sel(vr("x"), "a"),
		sel(vr("x"), "b"),
		&KeyofExpr{Value: vr("x")},
	}}}
	got, errs := Render(f)
	require.Empty(t, errs)
	require.Equal(t, "fn (x: {a: unknown, b: unknown}) -> \"a\" | \"b\"", got)
}

// TestResidualIndexUsageInferredOperand: `(typeof x)["a"]` where x is
// usage-inferred. After the write `x.a = 5`, x coalesces to mut {a: number},
// and the residual index reduces to number.
//
//	fn f(x) { x.a = 5; return (typeof x)["a"] }
//	  ==>  fn (x: mut {a: number}) -> number
func TestResidualIndexUsageInferredOperand(t *testing.T) {
	f := &Lam{Params: []string{"x"}, Body: &Block{Exprs: []Term{
		assign(vr("x"), "a", litNum(5)),
		&IndexExpr{Value: vr("x"), Key: "a"},
	}}}
	got, errs := Render(f)
	require.Empty(t, errs)
	require.Equal(t, "fn (x: mut {a: number}) -> number", got)
}

// TestResidualKeyofGroundOperand: when the operand IS already a concrete record
// value, the residual still reduces (the fixpoint's first round succeeds).
//
//	fn f() { return keyof typeof {a: 1, b: "s"} }  ==>  fn () -> "a" | "b"
func TestResidualKeyofGroundOperand(t *testing.T) {
	f := &Lam{Params: nil, Body: &KeyofExpr{Value: recExpr(
		"a", litNum(1), "b", litStr("s"),
	)}}
	got, errs := Render(f)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> \"a\" | \"b\"", got)
}

// TestResidualStaysSymbolicWhenIrreducible documents the fixed point: a residual
// over an operand that never gains object structure stays symbolic (Baseline-D
// behavior as the fixpoint's terminating result), rather than looping. Here x is
// used only as the keyof operand, so it coalesces to unknown and `keyof unknown`
// is the terminating (symbolic) result — the termination guard in action.
//
//	fn f(x) { return keyof typeof x }   ==>  fn (x: unknown) -> keyof unknown
func TestResidualStaysSymbolicWhenIrreducible(t *testing.T) {
	f := &Lam{Params: []string{"x"}, Body: &KeyofExpr{Value: vr("x")}}
	got, errs := Render(f)
	require.Empty(t, errs)
	require.Equal(t, "fn (x: unknown) -> keyof unknown", got)
}
