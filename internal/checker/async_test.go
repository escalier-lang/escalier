package checker

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
)

func TestAsyncFunctionInference(t *testing.T) {
	tests := map[string]struct {
		input        string
		expectedFn   string // expected function type
		functionName string // function name to check (defaults to "fetchData")
		expectedErr  bool
	}{
		"AsyncFuncWithInferredReturnType": {
			input: `
				async fn fetchData(url: string) {
					return "data"
				}
			`,
			expectedFn:   "fn (url: string) -> Promise<\"data\", never> throws never",
			functionName: "fetchData",
			expectedErr:  false,
		},
		"AsyncFuncWithThrow": {
			input: `
				async fn fetchData(url: string) {
					if url == "" {
						throw "error"
					}
					return "data"
				}
			`,
			expectedFn:   "fn (url: string) -> Promise<\"data\", \"error\"> throws never",
			functionName: "fetchData",
			expectedErr:  false,
		},
		"SimpleDeclareTest": {
			input: `
				declare fn fetch() -> string
			`,
			expectedFn:   "",
			functionName: "",
			expectedErr:  false,
		},
		"AsyncFuncWithAwait": {
			input: `
				declare fn fetch(url: string) -> Promise<string, string>
				
				async fn fetchData(url: string) {
					val data = await fetch(url)
					return data
				}
			`,
			expectedFn:   "fn (url: string) -> Promise<string, string> throws never",
			functionName: "fetchData",
			expectedErr:  false,
		},
		"AwaitOutsideAsyncFunction": {
			input: `
				declare fn fetch(url: string) -> Promise<string, string>
				
				fn syncFunc() {
					val data = await fetch("test")
					return data
				}
			`,
			expectedFn:   "",
			functionName: "",
			expectedErr:  true,
		},
		"AsyncFuncWithMultipleThrowTypes": {
			input: `
				async fn fetchData(flag: boolean) {
					if flag {
						throw "string error"
					} else {
						throw 42
					}
					return "data"
				}
			`,
			expectedFn:   "fn (flag: boolean) -> Promise<\"data\", \"string error\" | 42> throws never",
			functionName: "fetchData",
			expectedErr:  false,
		},
		"AsyncFuncWithAwaitAndThrow": {
			input: `
				declare fn fetch(url: string) -> Promise<string, string>
				
				async fn fetchData(url: string) {
					if url == "" {
						throw "invalid url"
					}
					val data = await fetch(url)
					return data
				}
			`,
			expectedFn:   "fn (url: string) -> Promise<string, \"invalid url\" | string> throws never",
			functionName: "fetchData",
			expectedErr:  false,
		},
		"AsyncFuncWithNestedAsync": {
			input: `
				declare fn fetch(url: string) -> Promise<string, string>
				
				async fn innerFetch(url: string) {
					throw "inner error"
					return await fetch(url)
				}
				
				async fn outerFetch(url: string) {
					val data = await innerFetch(url)
					return data
				}
			`,
			expectedFn:   "fn (url: string) -> Promise<string, \"inner error\" | string> throws never",
			functionName: "outerFetch",
			expectedErr:  false,
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

				// Check the function type by looking it up in the result scope
				if testCase.expectedFn != "" && testCase.functionName != "" {
					binding := resultScope.getValue(testCase.functionName)
					assert.NotNil(t, binding, "Function binding should be found in scope")
					if binding != nil {
						actualType := binding.Type.String()
						t.Logf("Actual function type: %s", actualType)
						t.Logf("Expected function type: %s", testCase.expectedFn)
						assert.Equal(t, testCase.expectedFn, actualType, "Function type should match expected")
					}
				}
			}
		})
	}
}
