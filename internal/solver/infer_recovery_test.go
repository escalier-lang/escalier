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
	require.Equal(t, "4:12-4:19: Unknown identifier: missing", msgWithSpan(errs[0]))
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
	require.Equal(t, "2:15-2:17: Unknown identifier: xs", msgWithSpan(errs[0]))
	require.Equal(t, "{}", values["o"])
}
