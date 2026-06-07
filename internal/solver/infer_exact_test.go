package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- PR4: function exactness (#677), source-level behaviors ---

// A written function value is EXACT, so calling it with exactly its declared arity
// type-checks. This is the regression the exact call-demand guards: an INEXACT
// synthesized demand (the FuncType Go zero value) would have accept-set [N, ∞) and
// force the callee inexact, rejecting every call to an exact function.
func TestInferCallExactArityTypeChecks(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(x: number, y: number) -> number { x }
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
		fn f(x: number, y: number) -> number { x }
		val r = f(1, 2, 3)
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Too many arguments: expected at most 2, but got 3", errs[0].Message())
}

// An `x?` optional parameter parsed from source carries onto the function type: it
// renders as `y?: T` and lowers the function's required count (the accept-set lower
// bound) without changing arity.
func TestInferOptionalParamRenders(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(x: number, y?: number) -> number { x }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (x: number, y?: number) -> number", values["f"])
}

// The optional parameter's lower/upper bounds at a direct call: omitting it (1 arg)
// and supplying it (2 args) both type-check; overflowing it (3 args) trips the lint.
func TestInferOptionalParamCallArity(t *testing.T) {
	t.Run("omitted and supplied both type-check", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn f(x: number, y?: number) -> number { x }
			val a = f(1)
			val b = f(1, 2)
		`)
		require.Empty(t, errs)
	})

	t.Run("overflow trips the lint", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn f(x: number, y?: number) -> number { x }
			val c = f(1, 2, 3)
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "Too many arguments: expected at most 2, but got 3", errs[0].Message())
	})
}

// A NON-TRAILING optional does not lower the required count: arguments bind
// positionally, so a required param after an optional is still required. `f(1)` on
// `fn(a?, b)` must be rejected (it would leave the required `b` unbound), even
// though `a` is marked optional. requiredCount counts trailing optionals only.
func TestInferNonTrailingOptionalRequiresAllArgs(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(a?: number, b: number) -> number { b }
		val r = f(1)
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot constrain function of arity 2 <: function of arity 1", errs[0].Message())
}

// The extra-arg lint fires for an INEXACT callee too (#677 §4.2.3: direct calls
// reject extras regardless of exactness). An inexact function value has no source
// syntax yet (the `...` marker rides on function-type annotations, which the solver
// does not resolve), so this is exercised over a hand-built inexact binding.
func TestInferCallInexactCalleeRejectsExtraArgs(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	scope.defineValue("g", ValueBinding{Schemes: []TypeScheme{monoScheme(&soltype.FuncType{
		Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "x"}, Type: &soltype.PrimType{Prim: soltype.NumPrim}}},
		Ret:    &soltype.PrimType{Prim: soltype.NumPrim},
		Exact:  false, // inexact: tolerates extras as a CALLBACK, but not at a direct call
	})}})
	// g(1, 2) — one extra positional argument beyond the single declared param.
	e := ast.NewCall(identExpr("g"), []ast.Expr{numExpr(1), numExpr(2)}, false, testSpan())

	c.inferExpr(scope, 0, e)
	require.Len(t, c.errs, 1)
	require.Equal(t, "Too many arguments: expected at most 1, but got 2", c.errs[0].Message())
}
