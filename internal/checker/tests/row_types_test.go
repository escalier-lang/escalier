package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
)

func TestRowTypesPropertyAccess(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"ReadAccess": {
			input: `
				fn foo(obj) {
					return obj.bar
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: {bar: T0, ...T1}) -> T0",
			},
		},
		"MultipleReads": {
			input: `
				fn foo(obj) {
					return [obj.bar, obj.baz]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1, T2>(obj: {bar: T0, ...T1, baz: T2}) -> [T0, T2]",
			},
		},
		"WriteAccess": {
			input: `
				fn foo(obj) {
					obj.bar = "hello"
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {bar: \"hello\", ...T0}) -> void",
			},
		},
		"ReadAndWrite": {
			input: `
				fn foo(obj) {
					val x = obj.bar
					obj.baz = 5
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: mut {bar: T0, ...T1, baz: 5}) -> void",
			},
		},
		"NestedAccess": {
			input: `
				fn foo(obj) {
					return obj.foo.bar
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1, T2>(obj: {foo: {bar: T0, ...T1}, ...T2}) -> T0",
			},
		},
		"MultipleParams": {
			input: `
				fn foo(a, b) {
					return [a.x, b.y]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1, T2, T3>(a: {x: T0, ...T1}, b: {y: T2, ...T3}) -> [T0, T2]",
			},
		},
		"DeeplyNested": {
			input: `
				fn foo(obj) {
					return obj.a.b.c
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1, T2, T3>(obj: {a: {b: {c: T0, ...T1}, ...T2}, ...T3}) -> T0",
			},
		},
		"NumericIndex": {
			input: `
				fn foo(obj) {
					return obj[0]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: Array<T0>) -> T0",
			},
		},
		"StringLiteralIndex": {
			input: `
				fn foo(obj) {
					return obj["bar"]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: {bar: T0, ...T1}) -> T0",
			},
		},
		"MultipleStringLiteralIndexes": {
			input: `
				fn foo(obj) {
					return [obj["bar"], obj["baz"]]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1, T2>(obj: {bar: T0, ...T1, baz: T2}) -> [T0, T2]",
			},
		},
		"StringLiteralIndexWrite": {
			input: `
				fn foo(obj) {
					obj["bar"] = "hello"
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {bar: \"hello\", ...T0}) -> void",
			},
		},
		"StringLiteralIndexReadAndWrite": {
			input: `
				fn foo(obj) {
					val x = obj["bar"]
					obj["baz"] = 5
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: mut {bar: T0, ...T1, baz: 5}) -> void",
			},
		},
		"MixedDotAndBracketAccess": {
			input: `
				fn foo(obj) {
					return [obj.bar, obj["baz"]]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1, T2>(obj: {bar: T0, ...T1, baz: T2}) -> [T0, T2]",
			},
		},
		"MixedDotReadBracketWrite": {
			input: `
				fn foo(obj) {
					val x = obj.bar
					obj["baz"] = 10
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: mut {bar: T0, ...T1, baz: 10}) -> void",
			},
		},
		"MultipleNumericIndexes": {
			input: `
				fn foo(obj) {
					return [obj[0], obj[1]]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: Array<T0>) -> [T0, T0]",
			},
		},
		"IdempotentPropertyAccess": {
			input: `
				fn foo(obj) {
					return [obj.bar, obj.bar]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: {bar: T0, ...T1}) -> [T0, T0]",
			},
		},
		"IdempotentMixedAccess": {
			input: `
				fn foo(obj) {
					return [obj.bar, obj["bar"]]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: {bar: T0, ...T1}) -> [T0, T0]",
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
			module, errors := parser.ParseLibFiles(ctx, []*ast.Source{source})

			if len(errors) > 0 {
				for i, err := range errors {
					fmt.Printf("Parse Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, errors, 0)

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			inferErrors := c.InferModule(inferCtx, module)
			scope := inferCtx.Scope.Namespace

			if len(inferErrors) > 0 {
				for i, err := range inferErrors {
					fmt.Printf("Infer Error[%d]: %s\n", i, err.Message())
				}
				assert.Equal(t, inferErrors, []*Error{})
			}

			// Collect actual types for verification
			actualTypes := make(map[string]string)
			for name, binding := range scope.Values {
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

func TestRowTypesErrors(t *testing.T) {
	tests := map[string]struct {
		input        string
		expectedErrs []string
	}{
		"MutateAnnotatedImmutableParam": {
			input: `
				fn foo(obj: {bar: number}) {
					obj.bar = 5
				}
			`,
			expectedErrs: []string{"Cannot mutate immutable"},
		},
		"MutateAnnotatedImmutableParamIndex": {
			input: `
				fn foo(obj: {bar: number}) {
					obj["bar"] = 5
				}
			`,
			expectedErrs: []string{"Cannot mutate immutable"},
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
			module, errors := parser.ParseLibFiles(ctx, []*ast.Source{source})

			if len(errors) > 0 {
				for i, err := range errors {
					fmt.Printf("Parse Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, errors, 0)

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			inferErrors := c.InferModule(inferCtx, module)

			assert.Len(t, inferErrors, len(test.expectedErrs), "expected %d errors, got %d", len(test.expectedErrs), len(inferErrors))
			for i, expectedErr := range test.expectedErrs {
				if i < len(inferErrors) {
					assert.Contains(t, inferErrors[i].Message(), expectedErr)
				}
			}
		})
	}
}
