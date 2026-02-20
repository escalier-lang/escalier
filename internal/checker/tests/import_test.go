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
		"NamespaceImportCsstype": {
			input: `
				import * as CSS from "csstype"
				declare val alignItems: CSS.Property.AlignItems
			`,
			expectedValues: map[string]string{
				"alignItems": "Globals | DataType.SelfPosition | \"anchor-center\" | \"baseline\" | \"normal\" | \"stretch\" | string & {}",
			},
		},
		"NamespaceImportReact": {
			input: `
				import * as React from "react"
				declare val useState: React.UseState
			`,
			expectedValues: map[string]string{
				"useState": "fn (initialState: any) -> [any, fn (newState: any) -> void] throws never",
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

				// Use the resultScope for expansion, not the original inferCtx,
				// because the resultScope has the imported namespaces
				expandCtx := Context{
					Scope:                  resultScope,
					IsAsync:                false,
					IsPatMatch:             false,
					AllowUndefinedTypeRefs: false,
					TypeRefsToUpdate:       nil,
				}
				expandedTyped, _ := c.ExpandType(expandCtx, binding.Type, 1)
				actualType := expandedTyped.String()

				if exists {
					assert.Equal(t, expectedValue, actualType, "Type alias mismatch for %s", expectedName)
				}
			}
		})
	}
}
