package tests

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
)

func TestTryCatchInferenceScript(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedValues map[string]string // expected variable types
		expectedErr    bool
	}{
		"BasicTryCatch": {
			input: `
				val result = try {
					5
				} catch {
					_ => 0
				}
			`,
			expectedValues: map[string]string{
				"result": "5 | 0",
			},
			expectedErr: false,
		},
		"TryCatchWithStringReturn": {
			input: `
				val result = try {
					"success"
				} catch {
					_ => "error"
				}
			`,
			expectedValues: map[string]string{
				"result": "\"success\" | \"error\"",
			},
			expectedErr: false,
		},
		"TryCatchReturnsUnion": {
			input: `
				val result = try {
					5
				} catch {
					_ => "error"
				}
			`,
			expectedValues: map[string]string{
				"result": "5 | \"error\"",
			},
			expectedErr: false,
		},
		"TryCatchWithMultipleCases": {
			input: `
				val result = try {
					throw Error("fail")
				} catch {
					Error => 0,
					_ => -1,
				}
			`,
			expectedValues: map[string]string{
				"result": "0 | -1",
			},
			expectedErr: false,
		},
		"TryCatchWithPatternBindings": {
			input: `
				val result = try {
					throw "fail"
				} catch {
					msg => msg,
					_ => "unknown",
				}
			`,
			expectedValues: map[string]string{
				"result": "\"fail\" | \"unknown\"",
			},
			expectedErr: false,
		},
		"TryCatchWithGuard": {
			input: `
				val result = try {
					throw "critical"
				} catch {
					err if err == "critical" => -1,
					_ => 0,
				}
			`,
			expectedValues: map[string]string{
				"result": "-1 | 0",
			},
			expectedErr: false,
		},
		"TryCatchNoCatch": {
			input: `
				val result = try {
					42
				}
			`,
			expectedValues: map[string]string{
				"result": "42",
			},
			expectedErr: false,
		},
		"TryCatchInFunction": {
			input: `
				val safeDivide = fn (a: number, b: number) {
					return try {
						a / b
					} catch {
						_ => 0
					}
				}
			`,
			expectedValues: map[string]string{
				"safeDivide": "fn (a: number, b: number) -> number | 0 throws never",
			},
		},
		"TryCatchWithObjectPattern": {
			input: `
				val result = try {
					throw {message: "fail"}
				} catch {
					{message: msg} => msg,
					_ => "unknown",
				}
			`,
			expectedValues: map[string]string{
				"result": "\"fail\" | \"unknown\"",
			},
			expectedErr: false,
		},
		"NestedTryCatch": {
			input: `
				val result = try {
					try {
						5
					} catch {
						_ => 10
					}
				} catch {
					_ => 0
				}
			`,
			expectedValues: map[string]string{
				"result": "5 | 10 | 0",
			},
			expectedErr: false,
		},
		"TryCatchWithBlockBody": {
			input: `
				val result = try {
					val x = 5
					x + 10
				} catch {
					_ => {
						val y = 0
						y
					}
				}
			`,
			expectedValues: map[string]string{
				"result": "number | 0",
			},
			expectedErr: false,
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
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}

			resultScope, inferErrors := c.InferScript(inferCtx, script)
			if testCase.expectedErr {
				assert.NotEmpty(t, inferErrors, "Should have inference errors")
				for i, err := range inferErrors {
					t.Logf("Expected Error[%d]: %s", i, err.Message())
				}
			} else {
				// Check that inference completed without errors
				for i, err := range inferErrors {
					t.Logf("Unexpected Error[%d]: %s", i, err.Message())
				}
				assert.Empty(t, inferErrors, "Should infer without errors")

				// Check the variable types by looking them up in the result scope
				for varName, expectedType := range testCase.expectedValues {
					binding := resultScope.GetValue(varName)
					assert.NotNil(t, binding, "Variable '%s' binding should be found in scope", varName)
					if binding != nil {
						actualType := binding.Type.String()
						t.Logf("Actual type for '%s': %s", varName, actualType)
						t.Logf("Expected type for '%s': %s", varName, expectedType)
						assert.Equal(t, expectedType, actualType, "Variable '%s' type should match expected", varName)
					}
				}
			}
		})
	}
}

func TestTryCatchInferenceModule(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedValues map[string]string // expected variable types
		expectedErr    bool
	}{
		"BasicTryCatch": {
			input: `
				val result = try {
					5
				} catch {
					_ => 0
				}
			`,
			expectedValues: map[string]string{
				"result": "5 | 0",
			},
			expectedErr: false,
		},
		"TryCatchWithStringReturn": {
			input: `
				val result = try {
					"success"
				} catch {
					_ => "error"
				}
			`,
			expectedValues: map[string]string{
				"result": "\"success\" | \"error\"",
			},
			expectedErr: false,
		},
		"TryCatchReturnsUnion": {
			input: `
				val result = try {
					5
				} catch {
					_ => "error"
				}
			`,
			expectedValues: map[string]string{
				"result": "5 | \"error\"",
			},
			expectedErr: false,
		},
		"TryCatchWithMultipleCases": {
			input: `
				val result = try {
					throw Error("fail")
				} catch {
					Error => 0,
					_ => -1,
				}
			`,
			expectedValues: map[string]string{
				"result": "0 | -1",
			},
			expectedErr: false,
		},
		"TryCatchWithPatternBindings": {
			input: `
				val result = try {
					throw "fail"
				} catch {
					msg => msg,
					_ => "unknown",
				}
			`,
			expectedValues: map[string]string{
				"result": "\"fail\" | \"unknown\"",
			},
			expectedErr: false,
		},
		"TryCatchWithGuard": {
			input: `
				val result = try {
					throw "critical"
				} catch {
					err if err == "critical" => -1,
					_ => 0,
				}
			`,
			expectedValues: map[string]string{
				"result": "-1 | 0",
			},
			expectedErr: false,
		},
		"TryCatchNoCatch": {
			input: `
				val result = try {
					42
				}
			`,
			expectedValues: map[string]string{
				"result": "42",
			},
			expectedErr: false,
		},
		"TryCatchInFunction": {
			input: `
				val safeDivide = fn (a: number, b: number) {
					return try {
						a / b
					} catch {
						_ => 0
					}
				}
			`,
			expectedValues: map[string]string{
				"safeDivide": "fn (a: number, b: number) -> number | 0 throws never",
			},
		},
		"TryCatchWithObjectPattern": {
			input: `
				val result = try {
					throw {message: "fail"}
				} catch {
					{message: msg} => msg,
					_ => "unknown",
				}
			`,
			expectedValues: map[string]string{
				"result": "\"fail\" | \"unknown\"",
			},
			expectedErr: false,
		},
		"NestedTryCatch": {
			input: `
				val result = try {
					try {
						5
					} catch {
						_ => 10
					}
				} catch {
					_ => 0
				}
			`,
			expectedValues: map[string]string{
				"result": "5 | 10 | 0",
			},
			expectedErr: false,
		},
		"TryCatchWithBlockBody": {
			input: `
				val result = try {
					val x = 5
					x + 10
				} catch {
					_ => {
						val y = 0
						y
					}
				}
			`,
			expectedValues: map[string]string{
				"result": "number | 0",
			},
			expectedErr: false,
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
			module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
			assert.Empty(t, parseErrors, "Should parse without errors")

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}

			inferErrors := c.InferModule(inferCtx, module)
			scope := inferCtx.Scope.Namespace
			if testCase.expectedErr {
				assert.NotEmpty(t, inferErrors, "Should have inference errors")
				for i, err := range inferErrors {
					t.Logf("Expected Error[%d]: %s", i, err.Message())
				}
			} else {
				// Check that inference completed without errors
				for i, err := range inferErrors {
					t.Logf("Unexpected Error[%d]: %s", i, err.Message())
				}
				assert.Empty(t, inferErrors, "Should infer without errors")

				// Check the variable types by looking them up in the result scope
				for varName, expectedType := range testCase.expectedValues {
					binding := scope.Values[varName]
					assert.NotNil(t, binding, "Variable '%s' binding should be found in scope", varName)
					if binding != nil {
						actualType := binding.Type.String()
						t.Logf("Actual type for '%s': %s", varName, actualType)
						t.Logf("Expected type for '%s': %s", varName, expectedType)
						assert.Equal(t, expectedType, actualType, "Variable '%s' type should match expected", varName)
					}
				}
			}
		})
	}
}
