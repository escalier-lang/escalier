package checker

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
)

func TestMutation(t *testing.T) {
	tests := map[string]struct {
		input        string
		expectErrors bool
	}{
		"MutableObjectCanBeMutated": {
			input: `
				val obj: mut {x: number, y: string} = {x: 42, y: "hello"}
				obj.x = 100
			`,
			expectErrors: false,
		},
		"ImmutableObjectCannotBeMutated": {
			input: `
				val obj: {x: number, y: string} = {x: 42, y: "hello"}
				obj.x = 100
			`,
			expectErrors: true,
		},
		"MutableArrayCanBeMutated": {
			input: `
				val arr: mut Array<number> = [1, 2, 3]
				arr[0] = 99
			`,
			expectErrors: false,
		},
		"ImmutableArrayCannotBeMutated": {
			input: `
				val arr: Array<number> = [1, 2, 3]
				arr[0] = 99
			`,
			expectErrors: true,
		},
		"NestedMutableObjectCanBeMutated": {
			input: `
				val obj: mut {data: mut {x: number}} = {data: {x: 42}}
				obj.data.x = 100
			`,
			expectErrors: false,
		},
		"NestedImmutableObjectCannotBeMutated": {
			input: `
				val obj: {data: {x: number}} = {data: {x: 42}}
				obj.data.x = 100
			`,
			expectErrors: true,
		},
		"MixedPropertyAndIndexAccess": {
			input: `
				val obj: mut {arr: mut Array<number>} = {arr: [1, 2, 3]}
				obj.arr[0] = 99
				obj["arr"][1] = 88
			`,
			expectErrors: false,
		},
		"MutableTupleWithImmutableObjects": {
			input: `
				val tuple: mut [{x: number}, {y: string}] = [{x: 42}, {y: "hello"}]
				tuple[0] = {x: 100}
			`,
			expectErrors: false,
		},
		"MutableTupleCannotMutateImmutableObjectContents": {
			input: `
				val tuple: mut [{x: number}, {y: string}] = [{x: 42}, {y: "hello"}]
				tuple[0].x = 100
			`,
			expectErrors: true,
		},
		"MutableTupleWithMixedMutability": {
			input: `
				val tuple: mut [mut {x: number}, {y: string}] = [{x: 42}, {y: "hello"}]
				tuple[0].x = 100
				tuple[1] = {y: "world"}
			`,
			expectErrors: false,
		},
		"MutableTupleCannotMutateImmutableObjectInMixedTuple": {
			input: `
				val tuple: mut [mut {x: number}, {y: string}] = [{x: 42}, {y: "hello"}]
				tuple[1].y = "world"
			`,
			expectErrors: true,
		},
		"MutableTupleWithImmutableArrayElements": {
			input: `
				val tuple: mut [Array<number>, Array<string>] = [[1, 2, 3], ["a", "b", "c"]]
				tuple[0] = [4, 5, 6]
			`,
			expectErrors: false,
		},
		"MutableTupleCannotMutateImmutableArrayContents": {
			input: `
				val tuple: mut [Array<number>, Array<string>] = [[1, 2, 3], ["a", "b", "c"]]
				tuple[0][0] = 99
			`,
			expectErrors: true,
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

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
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
