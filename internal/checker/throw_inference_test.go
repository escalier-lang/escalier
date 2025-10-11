package checker

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	. "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
)

func TestThrowExpressionInference(t *testing.T) {
	tests := map[string]struct {
		input           string
		expectedThrows  string
		shouldHaveError bool
	}{
		"FunctionWithThrowString": {
			input: `val testFunc = fn () -> undefined {
				throw "error message"
			}`,
			expectedThrows:  "\"error message\"",
			shouldHaveError: false,
		},
		"FunctionWithThrowError": {
			input: `val testFunc = fn () -> undefined {
				throw Error("error message")
			}`,
			expectedThrows:  "Error",
			shouldHaveError: false,
		},
		"FunctionWithMultipleThrows": {
			input: `val testFunc = fn (flag: boolean) -> undefined {
				if flag {
					throw "string error"
				} else {
					throw 42
				}
			}`,
			expectedThrows:  "\"string error\" | 42",
			shouldHaveError: false,
		},
		"FunctionWithNestedThrows": {
			input: `val testFunc = fn () -> undefined {
				val innerFunc = fn () -> undefined {
					throw "inner error"
				}
				throw "outer error"
			}`,
			expectedThrows:  "\"outer error\"",
			shouldHaveError: false,
		},
		"FunctionWithExplicitThrowsAndImplicitThrows": {
			input: `val testFunc = fn () -> undefined throws "explicit" {
				throw "implicit"
			}`,
			expectedThrows:  "\"explicit\"",
			shouldHaveError: true, // Should error because "implicit" doesn't unify with "explicit"
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

			assert.Empty(t, parseErrors, "Expected no parse errors")
			assert.NotNil(t, script, "Expected script to be parsed successfully")

			checker := NewChecker()
			scope, errors := checker.InferScript(
				Context{Scope: Prelude(checker), IsAsync: false, IsPatMatch: false},
				script,
			)

			if test.shouldHaveError {
				assert.NotEmpty(t, errors, "Expected errors but got none")
				return
			}

			assert.Empty(t, errors, "Expected no type errors, got: %v", errors)
			assert.NotNil(t, scope, "Expected scope to be created")

			// Get the function binding
			binding := scope.getValue("testFunc")
			assert.NotNil(t, binding, "Expected testFunc to be defined")

			// Prune the type to resolve any type variables
			funcType, ok := Prune(binding.Type).(*FuncType)
			assert.True(t, ok, "Expected testFunc to be a function type, got %T", Prune(binding.Type))
			assert.NotNil(t, funcType, "Expected function type to be non-nil")

			// Check that the throws type matches expected
			throwsStr := funcType.Throws.String()
			assert.Equal(t, test.expectedThrows, throwsStr, "Expected throws type to match")
		})
	}
}

func TestThrowExpressionUnification(t *testing.T) {
	tests := map[string]struct {
		input           string
		shouldHaveError bool
		errorType       string
	}{
		"ThrowTypeMismatch": {
			input: `val testFunc = fn () -> undefined throws Error {
				throw "string error"
			}`,
			shouldHaveError: true,
			errorType:       "CannotUnifyTypesError",
		},
		"ThrowTypeMatch": {
			input: `val testFunc = fn () -> undefined throws string {
				throw "string error"
			}`,
			shouldHaveError: false,
			errorType:       "",
		},
		"ThrowUnionTypeMatch": {
			input: `val testFunc = fn () -> undefined throws string | number {
				throw "string error"
			}`,
			shouldHaveError: false,
			errorType:       "",
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

			assert.Empty(t, parseErrors, "Expected no parse errors")
			assert.NotNil(t, script, "Expected script to be parsed successfully")

			checker := NewChecker()
			_, errors := checker.InferScript(
				Context{Scope: Prelude(checker), IsAsync: false, IsPatMatch: false}, script)

			if test.shouldHaveError {
				assert.NotEmpty(t, errors, "Expected errors but got none")
				if test.errorType != "" {
					found := false
					for _, err := range errors {
						// Check the type name of the error
						errorTypeName := fmt.Sprintf("%T", err)
						if strings.Contains(errorTypeName, test.errorType) {
							found = true
							break
						}
					}
					if !found {
						t.Logf("Expected error type %s, got errors: %v", test.errorType, errors)
					}
				}
			} else {
				assert.Empty(t, errors, "Expected no type errors, got: %#v", errors)
			}
		})
	}
}
