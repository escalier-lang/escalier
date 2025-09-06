package checker

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
)

func TestTypeCastErrors(t *testing.T) {
	tests := map[string]struct {
		input        string
		expectErrors bool
	}{
		"ValidTypeCastNumberToNumber": {
			input: `
				val x = 5
				val y = x : number
			`,
			expectErrors: false,
		},
		"ValidTypeCastStringToString": {
			input: `
				val x = "hello"
				val y = x : string
			`,
			expectErrors: false,
		},
		"ValidTypeCastNumberToAny": {
			input: `
				val x = 5
				val y = x : any
			`,
			expectErrors: false,
		},
		"InvalidTypeCastStringToNumber": {
			input: `
				val x = "hello"
				val y = x : number
			`,
			expectErrors: true,
		},
		"InvalidTypeCastNumberToString": {
			input: `
				val x = 5
				val y = x : string
			`,
			expectErrors: true,
		},
		"InvalidTypeCastObjectToNumber": {
			input: `
				val x = {a: 1}
				val y = x : number
			`,
			expectErrors: true,
		},
		"ValidTypeCastLiteralToBaseType": {
			input: `
				val x = 42
				val y = x : number
			`,
			expectErrors: false,
		},
		"ValidTypeCastBooleanToBoolean": {
			input: `
				val x = true
				val y = x : boolean
			`,
			expectErrors: false,
		},
		"InvalidTypeCastBooleanToNumber": {
			input: `
				val x = true
				val y = x : number
			`,
			expectErrors: true,
		},
		"ValidTypeCastChaining": {
			input: `
				val x = 5
				val y = x : number : any
			`,
			expectErrors: false,
		},
		"InvalidTypeCastArrayToString": {
			input: `
				val x = [1, 2, 3]
				val y = x : string
			`,
			expectErrors: true,
		},
		"ValidTypeCastNullToAny": {
			input: `
				val x = null
				val y = x : any
			`,
			expectErrors: false,
		},
		"ValidTypeCastUndefinedToAny": {
			input: `
				val x = undefined
				val y = x : any
			`,
			expectErrors: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := parser.NewParser(ctx, source)
			script, parseErrors := p.ParseScript()

			assert.Len(t, parseErrors, 0, "Expected no parse errors")

			inferCtx := Context{
				Scope:      Prelude(),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c := NewChecker()
			_, inferErrors := c.InferScript(inferCtx, script)

			if test.expectErrors {
				assert.NotEmpty(t, inferErrors, "Expected inference errors for %s", name)
				// Print the errors to understand what's happening
				for i, err := range inferErrors {
					t.Logf("Error[%d]: %s", i, err.Message())
				}
			} else {
				if len(inferErrors) > 0 {
					// Print the errors to understand what's happening
					for i, err := range inferErrors {
						t.Logf("Unexpected Error[%d]: %s", i, err.Message())
					}
				}
				assert.Empty(t, inferErrors, "Expected no inference errors for %s", name)
			}
		})
	}
}

func TestTypeCastInferredTypes(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"BasicTypeCast": {
			input: `
				val x = 5
				val y = x : number
			`,
			expectedTypes: map[string]string{
				"x": "5",
				"y": "number",
			},
		},
		"StringTypeCast": {
			input: `
				val x = "hello"
				val y = x : string
			`,
			expectedTypes: map[string]string{
				"x": "\"hello\"",
				"y": "string",
			},
		},
		"TypeCastToAny": {
			input: `
				val x = 42
				val y = x : any
			`,
			expectedTypes: map[string]string{
				"x": "42",
				"y": "any",
			},
		},
		"BooleanTypeCast": {
			input: `
				val x = true
				val y = x : boolean
			`,
			expectedTypes: map[string]string{
				"x": "true",
				"y": "boolean",
			},
		},
		"ChainedTypeCast": {
			input: `
				val x = 5
				val y = x : number : any
			`,
			expectedTypes: map[string]string{
				"x": "5",
				"y": "any",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := parser.NewParser(ctx, source)
			script, parseErrors := p.ParseScript()

			assert.Len(t, parseErrors, 0, "Expected no parse errors")

			inferCtx := Context{
				Scope:      Prelude(),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c := NewChecker()
			scope, inferErrors := c.InferScript(inferCtx, script)

			if len(inferErrors) > 0 {
				for i, err := range inferErrors {
					t.Logf("Unexpected Error[%d]: %s", i, err.Message())
				}
			}
			assert.Empty(t, inferErrors, "Expected no inference errors for %s", name)

			// Collect actual types for verification
			actualTypes := make(map[string]string)
			for name, binding := range scope.Namespace.Values {
				assert.NotNil(t, binding)
				actualTypes[name] = binding.Type.String()
			}

			// Verify that all expected types match the actual inferred types
			for expectedName, expectedType := range test.expectedTypes {
				actualType, exists := actualTypes[expectedName]
				assert.True(t, exists, "Expected variable %s to be declared", expectedName)
				if exists {
					assert.Equal(t, expectedType, actualType, "Type mismatch for variable %s", expectedName)
				}
			}
		})
	}
}
