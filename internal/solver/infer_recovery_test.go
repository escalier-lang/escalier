package solver

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// --- PR8 Part 1: the ErrorType error-recovery sentinel, end-to-end ---
//
// report mints ErrorType (not never) as the value-position recovery placeholder
// after emitting a diagnostic. ErrorType absorbs in both directions inside
// constrain, so a single reported error never cascades a spurious second one — at
// any sink the broken value later flows into. These exercise that through the real
// parser, complementing the constrain-level unit tests (constrain_test.go) and the
// if/await cascade tests (infer_async_test.go).

// A value bound to a broken (unknown-identifier) initializer flows into a call
// argument WITHOUT producing a second error: the ErrorType placeholder absorbs the
// `error <: number` parameter constraint, so only the original unknown-identifier
// error survives.
func TestInferErrorBindingFlowsIntoCallNoCascade(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn id(x: number) -> number { return x }
		fn f() {
			var a = missing
			return id(a)
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "4:12-4:19: Unknown identifier: missing", msgWithSpan(t, errs[0]))
	// id's call still recovers its declared return type — the error arg absorbs.
	require.Equal(t, "fn () -> number", values["f"])
}

// An object spread over an unknown identifier walks its operand, which recovers to the ErrorType
// sentinel. The spread absorbs that sentinel rather than layering a SpreadNotObjectError on it, so
// the only error is the unknown-identifier one and the surrounding object still builds.
func TestInferObjectSpreadOverUnknownIdentifierRecovers(t *testing.T) {
	values, _, errs := inferSource(t, `
		val o = {...xs}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "2:15-2:17: Unknown identifier: xs", msgWithSpan(t, errs[0]))
	require.Equal(t, "{}", values["o"])
}

// The parser substitutes an ErrorExpr for an expression it could not read and
// reports the parse error itself, so the walk recovers over that subtree without
// reporting a second diagnostic of its own. A missing operand and a missing
// initializer are the two shapes that reach the walk.
//
// The binding the declaration introduces still gets a type. `1 +` keeps the `+`
// signature. The walk constrains `error <: number` for the operand that is gone,
// the ErrorType sentinel absorbs that, and the declaration takes the `number` that
// `+` returns. `export val broken =` has no signature to fall back on, so its
// binding takes the sentinel itself.
func TestInferErrorExprReportsNothing(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		parseErr string
		binding  string
		want     string
	}{
		{name: "MissingOperand", src: `val x = 1 +`, parseErr: "11-11: Expected an expression",
			binding: "x", want: "number"},
		{name: "MissingInitializer", src: `export val broken =`, parseErr: "19-19: Expected an expression",
			binding: "broken", want: "error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			module, parseErrors := parser.ParseLibFiles(ctx,
				[]*ast.Source{{ID: 0, Path: "input.esc", Contents: test.src}})
			require.Len(t, parseErrors, 1)
			require.Equal(t, test.parseErr, parseErrors[0].String())
			registerTestSources(t, module.Sources)

			values, _, errs := inferModule(module)
			require.Empty(t, errs)
			require.Equal(t, test.want, values[test.binding])
		})
	}
}
