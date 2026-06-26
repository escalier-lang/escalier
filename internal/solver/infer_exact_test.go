package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- function exactness, source-level behaviors ---

// A written function value is EXACT, so calling it with exactly its declared arity
// type-checks. This is the regression the exact call-demand guards: an INEXACT
// synthesized demand (the FuncType Go zero value) would have accept-set [N, ∞) and
// force the callee inexact, rejecting every call to an exact function.
func TestInferCallExactArityTypeChecks(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(x: number, y: number) -> number { return x }
		val r = f(1, 2)
	`)
	require.Empty(t, errs)
	require.Equal(t, "number", values["r"])
}

// A too-many-args direct call to an exact function yields EXACTLY ONE diagnostic —
// the TooManyArgsError lint — not a doubled lint + FuncArityMismatch. The constraint
// receives only the arity-matched prefix, so its accept-set gate stays silent.
func TestInferCallExactTooManyArgsSingleDiagnostic(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(x: number, y: number) -> number { return x }
		val r = f(1, 2, 3)
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Too many arguments: expected at most 2, but got 3", errs[0].Message())
}

// An `x?` optional parameter parsed from source carries onto the function type: it
// renders as `y?: T` and lowers the function's required count (the accept-set lower
// bound) without changing arity.
func TestInferOptionalParamRenders(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(x: number, y?: number) -> number { return x }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (x: number, y?: number) -> number", values["f"])
}

// The optional parameter's lower/upper bounds at a direct call: omitting it (1 arg)
// and supplying it (2 args) both type-check; overflowing it (3 args) trips the lint.
func TestInferOptionalParamCallArity(t *testing.T) {
	t.Run("omitted and supplied both type-check", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn f(x: number, y?: number) -> number { return x }
			val a = f(1)
			val b = f(1, 2)
		`)
		require.Empty(t, errs)
	})

	t.Run("overflow trips the lint", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn f(x: number, y?: number) -> number { return x }
			val c = f(1, 2, 3)
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "Too many arguments: expected at most 2, but got 3", errs[0].Message())
	})
}

// A NON-TRAILING optional does not lower the required count: arguments bind
// positionally, so a required param after an optional is still required. `f(1)` on
// `fn(a?, b)` must be rejected (it would leave the required `b` unbound), even
// though `a` is marked optional. requiredCount counts trailing optionals only, so
// the too-few lint reports a required count of 2.
func TestInferNonTrailingOptionalRequiresAllArgs(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(a?: number, b: number) -> number { return b }
		val r = f(1)
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Not enough arguments: expected at least 2, but got 1", errs[0].Message())
}

// A function value declared with the trailing `...` marker parses and is inferred
// INEXACT: its rendered type carries the `...`, but the direct-call lint still
// rejects extra args. Inexactness governs callback subtyping, not direct calls.
func TestInferInexactFunctionValue(t *testing.T) {
	t.Run("renders with the inexact marker", func(t *testing.T) {
		values, _, errs := inferSource(t, `fn f(x: number, ...) -> number { return x }`)
		require.Empty(t, errs)
		require.Equal(t, "fn (x: number, ...) -> number", values["f"])
	})

	t.Run("direct call still rejects extra args", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn f(x: number, ...) -> number { return x }
			val r = f(1, 2)
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "Too many arguments: expected at most 1, but got 2", errs[0].Message())
	})
}

// The too-few lint reports the REQUIRED count, not the declared count: a trailing
// optional may be omitted, so `fn(x, y?)` called with no args needs at least 1.
func TestInferTooFewArgsRespectsOptional(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(x: number, y?: number) -> number { return x }
		val r = f()
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Not enough arguments: expected at least 1, but got 0", errs[0].Message())
}

// A typed rest param absorbs trailing arguments, so a direct call with more args
// than declared is NOT too-many, but too-few still applies to the fixed params.
// Rest params have no source syntax yet, since inferFunc reports them unsupported,
// so this is built by hand. This is unlike the inexact `...` marker, which is
// source-expressible and covered via inferSource in TestInferInexactFunctionValue.
func TestInferCallRestCalleeArity(t *testing.T) {
	// fn g(x: number, ...rest: number): one fixed param + a rest.
	restCallee := func() ValueBinding {
		return ValueBinding{Schemes: []TypeScheme{monoScheme(&soltype.FuncType{
			Params: []*soltype.FuncParam{
				{Pattern: &soltype.IdentPat{Name: "x"}, Type: &soltype.PrimType{Prim: soltype.NumPrim}},
				{Pattern: &soltype.IdentPat{Name: "rest"}, Type: &soltype.PrimType{Prim: soltype.NumPrim}, Rest: true},
			},
			Ret: &soltype.PrimType{Prim: soltype.NumPrim},
		})}}
	}

	t.Run("absorbs extra args (no too-many)", func(t *testing.T) {
		c := newChecker()
		scope := NewScope()
		scope.defineValue("g", restCallee())
		// g(1, 2, 3) — two args beyond the fixed param; the rest absorbs them.
		e := ast.NewCall(identExpr("g"), []ast.Expr{numExpr(1), numExpr(2), numExpr(3)}, false, testSpan())
		c.inferExpr(scope, 0, e)
		require.Empty(t, c.errs)
	})

	t.Run("still rejects too few (required fixed params)", func(t *testing.T) {
		c := newChecker()
		scope := NewScope()
		scope.defineValue("g", restCallee())
		// g() — zero args, but x is required (the rest may be empty, x may not).
		e := ast.NewCall(identExpr("g"), nil, false, testSpan())
		c.inferExpr(scope, 0, e)
		require.Len(t, c.errs, 1)
		require.Equal(t, "Not enough arguments: expected at least 1, but got 0", c.errs[0].Message())
	})
}
