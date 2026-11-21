package compiler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCompileStepWrapsAPanic asserts that a panic during a step is re-raised as an
// *InternalError naming the step's file and phase, and that an *InternalError from an
// inner step passes through unchanged.
func TestCompileStepWrapsAPanic(t *testing.T) {
	inner := &InternalError{File: "bin/main.esc", Phase: "checking", Value: "inner"}

	tests := map[string]struct {
		panicValue any
		want       *InternalError
		wantMsg    string
	}{
		"a panic raised in the step": {
			panicValue: "TODO - buildExpr - default case",
			want: &InternalError{
				File:  "lib/index.esc",
				Phase: "generating JavaScript for",
				Value: "TODO - buildExpr - default case",
			},
			wantMsg: "the Escalier compiler crashed while generating JavaScript for lib/index.esc: " +
				"TODO - buildExpr - default case",
		},
		"an inner step's internal error": {
			panicValue: inner,
			want:       inner,
			wantMsg:    "the Escalier compiler crashed while checking bin/main.esc: inner",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				var step compileStep
				defer step.wrapPanic()
				step.enter("lib/index.esc", "generating JavaScript for")
				panic(test.panicValue)
			}()

			internalErr, ok := recovered.(*InternalError)
			require.True(t, ok, "the recovered value is an *InternalError")
			require.Equal(t, test.want, internalErr)
			require.EqualError(t, internalErr, test.wantMsg)
		})
	}
}

// TestCompileStepWithoutAPanic asserts that a step that finishes raises nothing.
func TestCompileStepWithoutAPanic(t *testing.T) {
	require.NotPanics(t, func() {
		var step compileStep
		defer step.wrapPanic()
		step.enter("lib/index.esc", "checking")
	})
}
