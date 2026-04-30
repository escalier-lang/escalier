package tests

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
)

func TestThrowExpressionInference(t *testing.T) {
	tests := map[string]struct {
		input           string
		expectedThrows  string
		shouldHaveError bool
	}{
		"FunctionWithThrowString": {
			input: `val testFunc = fn () -> never throws _ {
				throw "error message"
			}`,
			expectedThrows:  "\"error message\"",
			shouldHaveError: false,
		},
		"FunctionWithThrowError": {
			input: `val testFunc = fn () -> never throws _ {
				throw Error("error message")
			}`,
			expectedThrows:  "Error",
			shouldHaveError: false,
		},
		"FunctionWithMultipleThrows": {
			input: `val testFunc = fn (flag: boolean) -> never throws _ {
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
			input: `val testFunc = fn () -> never throws _ {
				val innerFunc = fn () -> never throws _ {
					throw "inner error"
				}
				throw "outer error"
			}`,
			expectedThrows:  "\"outer error\"",
			shouldHaveError: false,
		},
		"FunctionWithExplicitThrowsAndImplicitThrows": {
			input: `val testFunc = fn () -> never throws "explicit" {
				throw "implicit"
			}`,
			expectedThrows:  "\"explicit\"",
			shouldHaveError: true, // Should error because "implicit" doesn't unify with "explicit"
		},
		"InheritsThrowsFromCalledFunction": {
			input: `val raise = fn () -> never throws "boom" {
				throw "boom"
			}
			val testFunc = fn () -> undefined throws _ {
				raise()
			}`,
			expectedThrows:  "\"boom\"",
			shouldHaveError: false,
		},
		"UnionsOwnThrowAndCalledFunctionThrows": {
			input: `val raise = fn () -> never throws "from-callee" {
				throw "from-callee"
			}
			val testFunc = fn (flag: boolean) -> undefined throws _ {
				if flag {
					throw "from-self"
				}
				raise()
			}`,
			expectedThrows:  "\"from-self\" | \"from-callee\"",
			shouldHaveError: false,
		},
		"InheritsThrowsFromCalledFunctionInValBinding": {
			input: `val raise = fn () -> never throws "boom" {
				throw "boom"
			}
			val testFunc = fn () -> undefined throws _ {
				val n = raise()
			}`,
			expectedThrows:  "\"boom\"",
			shouldHaveError: false,
		},
		"TryCatchSuppressesCalleeThrows": {
			input: `val raise = fn () -> never throws "boom" {
				throw "boom"
			}
			val testFunc = fn () -> undefined throws _ {
				val recovered = try {
					raise()
				} catch {
					_ => 0
				}
			}`,
			expectedThrows:  "never",
			shouldHaveError: false,
		},
		"CalleeWithoutThrowsContributesNothing": {
			input: `val pure = fn () -> number {
				return 42
			}
			val testFunc = fn () -> number throws _ {
				throw "self"
				return pure()
			}`,
			expectedThrows:  "\"self\"",
			shouldHaveError: false,
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

			checker := NewChecker(ctx)
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
			binding := scope.GetValue("testFunc")
			assert.NotNil(t, binding, "Expected testFunc to be defined")

			// Prune the type to resolve any type variables
			funcType, ok := type_system.Prune(binding.Type).(*type_system.FuncType)
			assert.True(t, ok, "Expected testFunc to be a function type, got %T", type_system.Prune(binding.Type))
			assert.NotNil(t, funcType, "Expected function type to be non-nil")

			// Check that the throws type matches expected
			throwsStr := funcType.Throws.String()
			assert.Equal(t, test.expectedThrows, throwsStr, "Expected throws type to match")
		})
	}
}

// TestNeverReturnInference pins the inferred *return* type (not the
// throws type) of functions whose bodies never fall through normally.
// `inferFuncBody` should produce `never` — not `void` — when every
// reachable path exits via `throw`/`return`/etc., so that a body like
// `{ throw "x" }` can satisfy any declared return annotation without
// reporting `void cannot be assigned to <T>`.
func TestNeverReturnInference(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedReturn string
	}{
		"BodyIsOnlyThrow": {
			input: `val testFunc = fn () -> never throws _ {
				throw "boom"
			}`,
			expectedReturn: "never",
		},
		"IfElseBothBranchesThrow": {
			input: `val testFunc = fn (flag: boolean) -> never throws _ {
				if flag {
					throw "a"
				} else {
					throw "b"
				}
			}`,
			expectedReturn: "never",
		},
		"IfWithoutElseFallsThrough": {
			input: `val testFunc = fn (flag: boolean) -> undefined throws _ {
				if flag {
					throw "a"
				}
			}`,
			expectedReturn: "undefined",
		},
		"FallThroughAfterThrowingBranch": {
			input: `val testFunc = fn (flag: boolean) -> undefined throws _ {
				if flag {
					throw "a"
				}
				val x = 1
			}`,
			expectedReturn: "undefined",
		},
		"DeclaredReturnNeverAcceptedForOnlyThrow": {
			input: `val testFunc = fn () -> never throws "boom" {
				throw "boom"
			}`,
			expectedReturn: "never",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := parser.NewParser(ctx, source)
			script, parseErrors := p.ParseScript()
			assert.Empty(t, parseErrors, "Expected no parse errors")

			checker := NewChecker(ctx)
			scope, errors := checker.InferScript(
				Context{Scope: Prelude(checker), IsAsync: false, IsPatMatch: false},
				script,
			)
			assert.Empty(t, errors, "Expected no type errors, got: %v", errors)

			binding := scope.GetValue("testFunc")
			assert.NotNil(t, binding, "Expected testFunc to be defined")

			funcType, ok := type_system.Prune(binding.Type).(*type_system.FuncType)
			assert.Truef(t, ok, "Expected FuncType, got %T", type_system.Prune(binding.Type))

			returnStr := type_system.Prune(funcType.Return).String()
			assert.Equal(t, test.expectedReturn, returnStr, "Expected return type to match")
		})
	}
}

// TestDivergingBodyRejectsNonNeverReturn covers the rule that a
// function whose body never falls through normally (e.g. only throws)
// must declare its return type as `never`. Annotating it with any
// other type would mislead callers into thinking the function might
// produce a value.
func TestDivergingBodyRejectsNonNeverReturn(t *testing.T) {
	tests := map[string]struct {
		input       string
		shouldError bool
	}{
		"OnlyThrowDeclaredAsNumber": {
			input: `val f = fn () -> number throws _ {
				throw "boom"
			}`,
			shouldError: true,
		},
		"OnlyThrowDeclaredAsUndefined": {
			input: `val f = fn () -> undefined throws _ {
				throw "boom"
			}`,
			shouldError: true,
		},
		"BothBranchesThrowDeclaredAsNumber": {
			input: `val f = fn (flag: boolean) -> number throws _ {
				if flag {
					throw "a"
				} else {
					throw "b"
				}
			}`,
			shouldError: true,
		},
		"OnlyThrowDeclaredAsNever": {
			input: `val f = fn () -> never throws _ {
				throw "boom"
			}`,
			shouldError: false,
		},
		"FallThroughAfterThrowingBranchOK": {
			input: `val f = fn (flag: boolean) -> undefined throws _ {
				if flag {
					throw "a"
				}
			}`,
			shouldError: false,
		},
		"BodyHasReturnAndThrowOK": {
			input: `val f = fn (flag: boolean) -> number throws _ {
				if flag {
					return 1
				}
				throw "x"
			}`,
			shouldError: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := parser.NewParser(ctx, source)
			script, parseErrors := p.ParseScript()
			assert.Empty(t, parseErrors, "Expected no parse errors")

			checker := NewChecker(ctx)
			_, errors := checker.InferScript(
				Context{Scope: Prelude(checker), IsAsync: false, IsPatMatch: false},
				script,
			)

			found := false
			for _, e := range errors {
				if _, ok := e.(DivergingBodyNonNeverReturnError); ok {
					found = true
					break
				}
			}
			if test.shouldError {
				assert.Truef(t, found,
					"expected DivergingBodyNonNeverReturnError; got: %v", errors)
			} else {
				assert.Falsef(t, found,
					"expected NO DivergingBodyNonNeverReturnError; got: %v", errors)
			}
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
			input: `val testFunc = fn () -> never throws string {
				throw "string error"
			}`,
			shouldHaveError: false,
			errorType:       "",
		},
		"ThrowUnionTypeMatch": {
			input: `val testFunc = fn () -> never throws string | number {
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

			checker := NewChecker(ctx)
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
