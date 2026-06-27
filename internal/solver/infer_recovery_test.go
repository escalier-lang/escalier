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

// An unsupported expression recovers to the ErrorType sentinel and flows on without
// cascading. Here the unsupported expression is an object spread, which is M9. The
// only error is the unsupported-node one, and the surrounding object still builds.
func TestInferUnsupportedExprRecoversWithoutCascade(t *testing.T) {
	values, _, errs := inferSource(t, `
		val o = {...xs}
	`)
	// One error for the spread itself; `xs` is never walked (so no extra
	// unknown-identifier), and the broken element does not cascade.
	require.Len(t, errs, 1)
	require.Equal(t, "2:12-2:17: Unsupported: ObjSpreadExpr", msgWithSpan(errs[0]))
	require.Equal(t, "{}", values["o"])
}
