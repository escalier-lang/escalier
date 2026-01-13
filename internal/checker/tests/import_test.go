package tests

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
)

func TestImportInferenceScript(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedValues map[string]string // expected inferred types for values
	}{
		"NamespaceImportOfPackageWithNoDeps": {
			input: `
				import * as fde from "fast-deep-equal"
				val equal = fde.equal
			`,
			expectedValues: map[string]string{
				"equal": "fn (a: any, b: any) -> boolean throws never",
			},
		},
	}

	for testName, testCase := range tests {
		t.Run(testName, func(t *testing.T) {
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: testCase.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := parser.NewParser(ctx, source)
			script, parseErrors := p.ParseScript()
			assert.Empty(t, parseErrors, "Should parse without errors")

			c := NewChecker()
			inferCtx := Context{
				Scope:                  Prelude(c),
				IsAsync:                false,
				IsPatMatch:             false,
				AllowUndefinedTypeRefs: false,
				TypeRefsToUpdate:       nil,
			}

			resultScope, inferErrors := c.InferScript(inferCtx, script)

			// Check that inference completed without errors
			for i, err := range inferErrors {
				t.Logf("Unexpected Error[%d]: %s", i, err.Message())
			}
			assert.Empty(t, inferErrors, "Should infer without errors")

			// Verify that all expected type aliases match the actual inferred types
			for expectedName, expectedValue := range testCase.expectedValues {
				binding, exists := resultScope.Namespace.Values[expectedName]
				assert.True(t, exists, "Expected type alias %s to be declared", expectedName)

				expandedTyped, _ := c.ExpandType(inferCtx, binding.Type, 1)
				actualType := expandedTyped.String()

				if exists {
					assert.Equal(t, expectedValue, actualType, "Type alias mismatch for %s", expectedName)
				}
			}
		})
	}
}
