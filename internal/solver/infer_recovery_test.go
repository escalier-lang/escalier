package solver

import (
	"testing"

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
	require.Equal(t, "Unknown identifier: missing", errs[0].Message())
	// id's call still recovers its declared return type — the error arg absorbs.
	require.Equal(t, "fn () -> number", values["f"])
}

// An unsupported expression (here an array spread, an M4 construct) recovers to the
// ErrorType sentinel and flows on without cascading: the only error is the
// unsupported-node one, and the surrounding tuple still builds.
func TestInferUnsupportedExprRecoversWithoutCascade(t *testing.T) {
	values, _, errs := inferSource(t, `
		val t = [...xs]
	`)
	// One error for the spread itself; `xs` is never walked (so no extra
	// unknown-identifier), and the broken element does not cascade.
	require.Len(t, errs, 1)
	require.Equal(t, "Unsupported in M2: ArraySpreadExpr", errs[0].Message())
	require.Equal(t, "[error]", values["t"])
}
