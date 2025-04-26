package checker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
)

func TestParseModuleNoErrors(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"VarDecls": {
			input: `
				val a = 5
				val b = 10
				val sum = a + b
			`,
		},
		"TupleDecl": {
			input: `
				val [x, y] = [5, 10]
			`,
		},
		"ObjectDecl": {
			input: `
				val {x, y} = {x: "foo", y: "bar"}
			`,
		},
		"IfElseExpr": {
			input: `
				val a = 5
				val b = 10
				val x = if (a > b) {
					true
				} else {
					"hello"
				}
			`,
		},
		"IfElseIfExpr": {
			input: `
				val a = 5
				val b = 10
				val x = if (a > b) {
					true
				} else if (a < b) {
					false
				} else {
				    "hello"
				}
			`,
		},
		"FuncExpr": {
			input: `
				val add = fn (x, y) {
					return x + y
				}
			`,
		},
		"FuncExprWithoutReturn": {
			input: `val log = fn (msg) {}`,
		},
		"FuncExprMultipleReturns": {
			input: `
				val add = fn (x, y) {
				    if (x > y) {
						return true
					} else {

					}
					return false
				}
			`,
		},
		// "FuncRecursion": {
		// 	input: `
		// 		val fact = fn (n) {
		// 			if (n == 0) {
		// 				return 1
		// 			} else {
		// 				return n * fact(n - 1)
		// 			}
		// 		}
		// 	`,
		// },
		// TODO:
		// - declare variables within a function body
		// - scope shadowing
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := parser.Source{
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := parser.NewParser(ctx, source)
			script, errors := parser.ParseScript()

			if len(errors) > 0 {
				for i, err := range errors {
					fmt.Printf("Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, errors, 0)

			inferCtx := Context{
				Filename:   "input.esc",
				Scope:      Prelude(),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c := NewChecker()
			bindings, inferErrors := c.InferScript(inferCtx, script)
			if len(inferErrors) > 0 {
				assert.Equal(t, inferErrors, []*Error{})
			}

			// TODO: short term - print each of the binding's types and store
			// them in a map and the snapshot the map.
			// TODO: long term - generate a .d.ts file from the bindings
			for name, binding := range bindings {
				assert.NotNil(t, binding)
				fmt.Printf("%s = %s\n", name, binding.Type.String())
				fmt.Printf("%#v\n", binding.Type.Provenance())
			}
		})
	}
}
