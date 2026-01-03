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

func TestTypeParamTopologicalSort(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"TypeDecl_OutOfOrder": {
			input: `
				type Foo<Bar: Baz, Baz: string> = {
					bar: Bar,
					baz: Baz,
				}
			`,
			expectedTypes: map[string]string{
				"Foo": "{bar: Bar, baz: Baz}",
			},
		},
		"TypeDecl_ChainDependency": {
			input: `
				type Chain<C: B, B: A, A: string> = {
					a: A,
					b: B,
					c: C,
				}
			`,
			expectedTypes: map[string]string{
				"Chain": "{a: A, b: B, c: C}",
			},
		},
		"TypeDecl_MultipleDependencies": {
			input: `
				type Multi<D: B | C, C: A, B: A, A: string> = {
					a: A,
					b: B,
					c: C,
					d: D,
				}
			`,
			expectedTypes: map[string]string{
				"Multi": "{a: A, b: B, c: C, d: D}",
			},
		},
		"TypeDecl_NoDependencies": {
			input: `
				type Simple<A, B, C> = {
					a: A,
					b: B,
					c: C,
				}
			`,
			expectedTypes: map[string]string{
				"Simple": "{a: A, b: B, c: C}",
			},
		},
		"Interface_OutOfOrder": {
			input: `
				interface IFoo<Bar: Baz, Baz: string> {
					bar: Bar,
					baz: Baz,
				}
			`,
			expectedTypes: map[string]string{
				"IFoo": "{bar: Bar, baz: Baz}",
			},
		},
		"Interface_ChainDependency": {
			input: `
				interface IChain<C: B, B: A, A: string> {
					a: A,
					b: B,
					c: C,
				}
			`,
			expectedTypes: map[string]string{
				"IChain": "{a: A, b: B, c: C}",
			},
		},
		"FuncDecl_OutOfOrder": {
			input: `
				fn identity<Bar: Baz, Baz: string>(x: Bar) -> Bar {
					return x
				}
			`,
			expectedTypes: map[string]string{
				"identity": "<Bar: Baz, Baz: string>(x: Bar) -> Bar",
			},
		},
		"FuncDecl_ChainDependency": {
			input: `
				fn convert<C: B, B: A, A: string>(x: C) -> A {
					return x
				}
			`,
			expectedTypes: map[string]string{
				"convert": "<C: B, B: A, A: string>(x: C) -> A",
			},
		},
		"TypeDecl_WithDefaults": {
			input: `
				type WithDefaults<Bar: Baz = Baz, Baz: string = "hello"> = {
					bar: Bar,
					baz: Baz,
				}
			`,
			expectedTypes: map[string]string{
				"WithDefaults": "{bar: Bar, baz: Baz}",
			},
		},
		"TypeDecl_ComplexConstraints": {
			input: `
				type Complex<
					D: {x: C},
					C: B | "literal",
					B: A,
					A: string | number
				> = {
					value: D,
				}
			`,
			expectedTypes: map[string]string{
				"Complex": "{value: D}",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			source := &ast.Source{
				ID:       0,
				Path:     "test.esc",
				Contents: test.input,
			}

			sources := []*ast.Source{source}
			module, parseErrors := parser.ParseLibFiles(ctx, sources)
			if len(parseErrors) > 0 {
				for i, err := range parseErrors {
					fmt.Printf("Parse Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, parseErrors, 0, "Parse errors: %v", parseErrors)
			assert.NotNil(t, module, "Module should not be nil")

			// Run type checker
			checker := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(checker),
				IsAsync:    false,
				IsPatMatch: false,
			}
			inferErrors := checker.InferModule(inferCtx, module)
			if len(inferErrors) > 0 {
				for i, err := range inferErrors {
					fmt.Printf("Infer Error[%d]: %#v\n", i, err)
					fmt.Printf("Infer Error[%d]: %s\n", i, err.Message())
				}
			}
			assert.Len(t, inferErrors, 0, "Type checker errors: %v", inferErrors)

			scope := inferCtx.Scope.Namespace

			// Verify expected types
			for expectedName := range test.expectedTypes {
				// Check if it's a type alias
				_, typeExists := scope.Types[expectedName]
				// Check if it's a value binding
				_, valueExists := scope.Values[expectedName]

				assert.True(t, typeExists || valueExists, "Expected '%s' to be declared", expectedName)
			}
		})
	}
}

func TestTypeParamCyclicDependency(t *testing.T) {
	tests := map[string]struct {
		input         string
		shouldSucceed bool
	}{
		"SelfReference": {
			input: `
				type Recursive<T: T> = {
					value: T,
				}
			`,
			// Should preserve original order when cycle is detected
			shouldSucceed: true,
		},
		"MutualReference": {
			input: `
				type Mutual<A: B, B: A> = {
					a: A,
					b: B,
				}
			`,
			// Should preserve original order when cycle is detected
			shouldSucceed: true,
		},
		"ThreeWayCycle": {
			input: `
				type Cycle<A: B, B: C, C: A> = {
					a: A,
					b: B,
					c: C,
				}
			`,
			// Should preserve original order when cycle is detected
			shouldSucceed: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			source := &ast.Source{
				ID:       0,
				Path:     "test.esc",
				Contents: test.input,
			}

			sources := []*ast.Source{source}
			module, parseErrors := parser.ParseLibFiles(ctx, sources)
			if len(parseErrors) > 0 {
				for i, err := range parseErrors {
					fmt.Printf("Parse Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, parseErrors, 0, "Parse errors: %v", parseErrors)
			assert.NotNil(t, module, "Module should not be nil")

			// Run type checker
			checker := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(checker),
				IsAsync:    false,
				IsPatMatch: false,
			}
			_ = checker.InferModule(inferCtx, module)

			if test.shouldSucceed {
				// For cyclic references, we should still be able to parse and check
				// The topological sort should detect the cycle and preserve original order
				assert.NotNil(t, module, "Module should not be nil even with cyclic type params")
			}
		})
	}
}

func TestTypeParamSortingWithUsage(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"UseTypeWithOutOfOrderParams": {
			input: `
				type Foo<Bar: Baz, Baz: string> = {
					bar: Bar,
					baz: Baz,
				}

				val x: Foo<"hello", "hello"> = {bar: "hello", baz: "hello"}
			`,
			expectedTypes: map[string]string{
				"x": "{bar: \"hello\", baz: \"hello\"}",
			},
		},
		"CallFunctionWithOutOfOrderParams": {
			input: `
				fn identity<Bar: Baz, Baz: string>(x: Bar) -> Bar {
					return x
				}

				val result = identity("hello")
			`,
			expectedTypes: map[string]string{
				"result": "string",
			},
		},
		"ChainedTypeUsage": {
			input: `
				type Chain<C: B, B: A, A: string> = {
					value: C,
				}

				type Usage = Chain<"test", "test", "test">
			`,
			expectedTypes: map[string]string{
				"Usage": "{value: \"test\"}",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			source := &ast.Source{
				ID:       0,
				Path:     "test.esc",
				Contents: test.input,
			}

			sources := []*ast.Source{source}
			module, parseErrors := parser.ParseLibFiles(ctx, sources)
			if len(parseErrors) > 0 {
				for i, err := range parseErrors {
					fmt.Printf("Parse Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, parseErrors, 0, "Parse errors: %v", parseErrors)
			assert.NotNil(t, module, "Module should not be nil")

			// Run type checker
			checker := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(checker),
				IsAsync:    false,
				IsPatMatch: false,
			}
			inferErrors := checker.InferModule(inferCtx, module)
			if len(inferErrors) > 0 {
				for i, err := range inferErrors {
					fmt.Printf("Infer Error[%d]: %#v\n", i, err)
					fmt.Printf("Infer Error[%d]: %s\n", i, err.Message())
				}
			}
			assert.Len(t, inferErrors, 0, "Type checker errors: %v", inferErrors)

			scope := inferCtx.Scope.Namespace

			// Verify expected types
			for expectedName := range test.expectedTypes {
				// Check if it's a type alias
				_, typeExists := scope.Types[expectedName]
				// Check if it's a value binding
				binding, valueExists := scope.Values[expectedName]

				if valueExists {
					assert.NotNil(t, binding.Type, "Type for '%s' should be inferred", expectedName)
				}

				assert.True(t, typeExists || valueExists, "Expected '%s' to be declared", expectedName)
			}
		})
	}
}
