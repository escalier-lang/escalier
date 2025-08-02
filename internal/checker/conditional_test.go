package checker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
)

func TestConditionalTypeAliasBasic(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"BasicConditionalTypeAlias": {
			input: `
				type IsString<T> = if T : string { true } else { false }
				type Test1 = IsString<string>
				type Test2 = IsString<number>
			`,
			expectedTypes: map[string]string{
				"Test1": "true",
				"Test2": "false",
			},
		},
		"ConditionalTypeWithGenericResult": {
			input: `
				type ArrayOf<T> = if T : any { Array<T> } else { never }
				type StringArray = ArrayOf<string>
				type NumberArray = ArrayOf<number>
			`,
			expectedTypes: map[string]string{
				"StringArray": "Array<string>",
				"NumberArray": "Array<number>",
			},
		},
		"ConditionalTypeWithObjectResult": {
			input: `
				type Wrap<T> = if T : any { { value: T } } else { never }
				type WrappedString = Wrap<string>
				type WrappedBoolean = Wrap<boolean>
			`,
			expectedTypes: map[string]string{
				"WrappedString":  "{value: string}",
				"WrappedBoolean": "{value: boolean}",
			},
		},
		"NestedConditionalTypes": {
			input: `
				type IsArray<T> = if T : Array<any> { true } else { false }
				// type GetElement<T> = if T : Array<infer U> { U } else { never }
				type TestArray = IsArray<Array<string>>
				// type ElementType = GetElement<Array<number>>
			`,
			expectedTypes: map[string]string{
				"TestArray": "true",
				// "ElementType": "number",
			},
		},
		"ConditionalTypeWithMultipleTypeParams": {
			input: `
				type Either<T, U> = if T : null { U } else { T }
				type Result1 = Either<null, string>
				type Result2 = Either<number, string>
			`,
			expectedTypes: map[string]string{
				"Result1": "string",
				"Result2": "number",
			},
		},
		// "ConditionalTypeWithFunctionTypes": {
		// 	input: `
		// 		type GetReturnType<T> = if T : fn(...args: Array<any>) -> infer R { R } else { never }
		// 		type StringFunc = fn() -> string
		// 		type NumberFunc = fn(x: number) -> number
		// 		type ReturnType1 = GetReturnType<StringFunc>
		// 		type ReturnType2 = GetReturnType<NumberFunc>
		// 	`,
		// 	expectedTypes: map[string]string{
		// 		"ReturnType1": "string",
		// 		"ReturnType2": "number",
		// 	},
		// },
		// // "ConditionalTypeWithTupleTypes": {
		// // 	input: `
		// // 		type Head<T> = if T : [infer H, ...Array<any>] { H } else { never }
		// // 		type Tail<T> = if T : [any, ...infer Rest] { Rest } else { never }
		// // 		type FirstElement = Head<[string, number, boolean]>
		// // 		type RestElements = Tail<[string, number, boolean]>
		// // 	`,
		// // 	expectedTypes: map[string]string{
		// // 		"FirstElement": "string",
		// // 		"RestElements": "[number, boolean]",
		// // 	},
		// // },
		// "DistributiveConditionalTypes": {
		// 	input: `
		// 		type ToArray<T> = if T : any { Array<T> } else { never }
		// 		type UnionArray = ToArray<string | number>
		// 	`,
		// 	expectedTypes: map[string]string{
		// 		"UnionArray": "Array<string> | Array<number>",
		// 	},
		// },
		// "ConditionalTypeWithElseIf": {
		// 	input: `
		// 		type TypeKind<T> = if T : string {
		// 			"string"
		// 		} else if T : number {
		// 			"number"
		// 		} else if T : boolean {
		// 			"boolean"
		// 		} else {
		// 			"unknown"
		// 		}
		// 		type StringKind = TypeKind<string>
		// 		type NumberKind = TypeKind<number>
		// 		type BooleanKind = TypeKind<boolean>
		// 		type ObjectKind = TypeKind<{}>
		// 	`,
		// 	expectedTypes: map[string]string{
		// 		"StringKind":  "\"string\"",
		// 		"NumberKind":  "\"number\"",
		// 		"BooleanKind": "\"boolean\"",
		// 		"ObjectKind":  "\"unknown\"",
		// 	},
		// },
		// "ComplexConditionalWithInfer": {
		// 	input: `
		// 		type ExtractArrayElement<T> = if T : Array<infer U> {
		// 			if U : string { U } else { never }
		// 		} else {
		// 			never
		// 		}
		// 		type StringArrayElement = ExtractArrayElement<Array<string>>
		// 		type NumberArrayElement = ExtractArrayElement<Array<number>>
		// 	`,
		// 	expectedTypes: map[string]string{
		// 		"StringArrayElement": "string",
		// 		"NumberArrayElement": "never",
		// 	},
		// },
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
					fmt.Printf("Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, errors, 0)

			inferCtx := Context{
				Scope:      Prelude(),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c := NewChecker()
			scope, inferErrors := c.InferModule(inferCtx, module)
			for i, err := range inferErrors {
				fmt.Printf("Infer Error[%d]: %s\n", i, err)
			}
			if len(inferErrors) > 0 {
				assert.Equal(t, inferErrors, []*Error{})
			}

			// Verify that all expected type aliases match the actual inferred types
			for expectedName, expectedType := range test.expectedTypes {
				binding, exists := scope.Types[expectedName]
				assert.True(t, exists, "Expected type alias %s to be declared", expectedName)

				expandedTyped, _ := c.expandType(inferCtx, binding.Type)
				actualType := expandedTyped.String()

				if exists {
					assert.Equal(t, expectedType, actualType, "Type alias mismatch for %s", expectedName)
				}
			}

			// Note: We don't check for unexpected type aliases since the scope may include
			// prelude types that are implementation details
		})
	}
}

func TestConditionalTypeAliasAdvanced(t *testing.T) {
	t.Skip("Skipping until conditional types are implemented")
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"UtilityTypeExclude": {
			input: `
				type Exclude<T, U> = if T : U { never } else { T }
				type Result1 = Exclude<string | number | boolean, string>
				type Result2 = Exclude<"a" | "b" | "c", "a">
			`,
			expectedTypes: map[string]string{
				"Result1": "number | boolean",
				"Result2": "\"b\" | \"c\"",
			},
		},
		"UtilityTypeExtract": {
			input: `
				type Extract<T, U> = if T : U { T } else { never }
				type Result1 = Extract<string | number | boolean, string | number>
				type Result2 = Extract<"a" | "b" | "c", "a" | "b">
			`,
			expectedTypes: map[string]string{
				"Result1": "string | number",
				"Result2": "\"a\" | \"b\"",
			},
		},
		"UtilityTypeNonNullable": {
			input: `
				type NonNullable<T> = if T : null { never } else if T : undefined { never } else { T }
				type Result1 = NonNullable<string | null | undefined>
				type Result2 = NonNullable<number | null>
			`,
			expectedTypes: map[string]string{
				"Result1": "string",
				"Result2": "number",
			},
		},
		"UtilityTypeParameters": {
			input: `
				type Parameters<T> = if T : fn(...args: infer P) -> any { P } else { never }
				type Func1 = fn(a: string, b: number) -> boolean
				type Func2 = fn() -> void
				type Result1 = Parameters<Func1>
				type Result2 = Parameters<Func2>
			`,
			expectedTypes: map[string]string{
				"Result1": "[string, number]",
				"Result2": "[]",
			},
		},
		"UtilityTypeReturnType": {
			input: `
				type ReturnType<T> = if T : fn(...args: Array<any>) -> infer R { R } else { never }
				type Func1 = fn(x: string) -> number
				type Func2 = fn() -> Array<boolean>
				type Result1 = ReturnType<Func1>
				type Result2 = ReturnType<Func2>
			`,
			expectedTypes: map[string]string{
				"Result1": "number",
				"Result2": "Array<boolean>",
			},
		},
		"NestedDistributiveConditionals": {
			input: `
				type DeepArray<T> = if T : any { 
					if T : Array<infer U> { Array<Array<U>> } else { Array<Array<T>> }
				} else { 
					never 
				}
				type Result1 = DeepArray<string>
				type Result2 = DeepArray<Array<number>>
			`,
			expectedTypes: map[string]string{
				"Result1": "Array<Array<string>>",
				"Result2": "Array<Array<number>>",
			},
		},
		"ConditionalWithComplexInfer": {
			input: `
				type GetSecond<T> = if T : [any, infer Second, ...Array<any>] { Second } else { never }
				type GetRest<T> = if T : [any, any, ...infer Rest] { Rest } else { never }
				type Tuple1 = [string, number, boolean, symbol]
				type Result1 = GetSecond<Tuple1>
				type Result2 = GetRest<Tuple1>
			`,
			expectedTypes: map[string]string{
				"Result1": "number",
				"Result2": "[boolean, symbol]",
			},
		},
		"ConditionalWithObjectInfer": {
			input: `
				type GetValueType<T> = if T : { value: infer V } { V } else { never }
				type GetKeyType<T> = if T : { [key: infer K]: any } { K } else { never }
				type Obj1 = { value: string, id: number }
				type Obj2 = { [key: number]: boolean }
				type Result1 = GetValueType<Obj1>
				type Result2 = GetKeyType<Obj2>
			`,
			expectedTypes: map[string]string{
				"Result1": "string",
				"Result2": "number",
			},
		},
		"ChainedConditionalTypes": {
			input: `
				type IsStringOrNumber<T> = if T : string { true } else if T : number { true } else { false }
				type IsArrayOfStrings<T> = if T : Array<infer U> { 
					IsStringOrNumber<U> 
				} else { 
					false 
				}
				type Result1 = IsArrayOfStrings<Array<string>>
				type Result2 = IsArrayOfStrings<Array<boolean>>
				type Result3 = IsArrayOfStrings<string>
			`,
			expectedTypes: map[string]string{
				"Result1": "true",
				"Result2": "false",
				"Result3": "false",
			},
		},
		"ConditionalWithUnionDistribution": {
			input: `
				type ArrayOrSingle<T> = if T : Array<any> { T } else { Array<T> }
				type MixedResult = ArrayOrSingle<string | Array<number>>
			`,
			expectedTypes: map[string]string{
				"MixedResult": "Array<string> | Array<number>",
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
					fmt.Printf("Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, errors, 0)

			inferCtx := Context{
				Scope:      Prelude(),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c := NewChecker()
			scope, inferErrors := c.InferModule(inferCtx, module)
			if len(inferErrors) > 0 {
				assert.Equal(t, inferErrors, []*Error{})
			}

			// Verify that all expected type aliases match the actual inferred types
			for expectedName, expectedType := range test.expectedTypes {
				binding, exists := scope.Types[expectedName]
				assert.True(t, exists, "Expected type alias %s to be declared", expectedName)

				expandedTyped, _ := c.expandType(inferCtx, binding.Type)
				actualType := expandedTyped.String()

				if exists {
					assert.Equal(t, expectedType, actualType, "Type alias mismatch for %s", expectedName)
				}
			}
		})
	}
}

func TestConditionalTypeAliasEdgeCases(t *testing.T) {
	t.Skip("Skipping until conditional types are implemented")
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
		expectErrors  bool // Set to true for tests that might fail until conditional types are fully implemented
	}{
		"ConditionalWithNever": {
			input: `
				type FilterNever<T> = if T : never { never } else { T }
				type Result1 = FilterNever<string>
				type Result2 = FilterNever<never>
			`,
			expectedTypes: map[string]string{
				"Result1": "string",
				"Result2": "never",
			},
		},
		"ConditionalWithAny": {
			input: `
				type IsAny<T> = if T : any { true } else { false }
				type Result1 = IsAny<string>
				type Result2 = IsAny<any>
			`,
			expectedTypes: map[string]string{
				"Result1": "true",
				"Result2": "true",
			},
		},
		"ConditionalWithUnknown": {
			input: `
				type IsUnknown<T> = if T : unknown { true } else { false }
				type Result1 = IsUnknown<string>
				type Result2 = IsUnknown<unknown>
			`,
			expectedTypes: map[string]string{
				"Result1": "true",
				"Result2": "true",
			},
		},
		"RecursiveConditionalType": {
			input: `
				type DeepFlatten<T> = if T : Array<infer U> { 
					if U : Array<any> { DeepFlatten<U> } else { U }
				} else { 
					T 
				}
				type Result1 = DeepFlatten<Array<Array<string>>>
				type Result2 = DeepFlatten<Array<string>>
				type Result3 = DeepFlatten<string>
			`,
			expectedTypes: map[string]string{
				"Result1": "string",
				"Result2": "string",
				"Result3": "string",
			},
		},
		"ConditionalWithMappedType": {
			input: `
				type OptionalKeys<T> = if T : { [K in keyof T]: infer V } { 
					{ [K in keyof T]?: V } 
				} else { 
					never 
				}
				type Obj = { a: string, b: number }
				type Result = OptionalKeys<Obj>
			`,
			expectedTypes: map[string]string{
				"Result": "{ a?: string, b?: number }",
			},
			expectErrors: true, // This involves mapped types which may not be fully supported yet
		},
		"ConditionalWithTemplateLiterals": {
			input: `
				type StartsWithHello<T> = if T : ` + "`hello${infer Rest}`" + ` { Rest } else { never }
				type Result1 = StartsWithHello<"hello world">
				type Result2 = StartsWithHello<"hi there">
			`,
			expectedTypes: map[string]string{
				"Result1": "\" world\"",
				"Result2": "never",
			},
			expectErrors: true, // Template literals may not be supported yet
		},
		"ConditionalWithRestParameters": {
			input: `
				type GetRestParams<T> = if T : fn(first: any, ...rest: infer R) -> any { R } else { never }
				type TestFunc = fn(a: string, ...args: [number, boolean]) -> void
				type Result = GetRestParams<TestFunc>
			`,
			expectedTypes: map[string]string{
				"Result": "[number, boolean]",
			},
		},
		"ConditionalWithOptionalParameters": {
			input: `
				type HasOptionalParam<T> = if T : fn(required: any, optional?: infer O) -> any { O } else { never }
				type TestFunc = fn(a: string, b?: number) -> void
				type Result = HasOptionalParam<TestFunc>
			`,
			expectedTypes: map[string]string{
				"Result": "number | undefined",
			},
		},
		"DistributiveWithNestedUnions": {
			input: `
				type Distribute<T> = if T : any { { type: T } } else { never }
				type ComplexUnion = string | (number | boolean)
				type Result = Distribute<ComplexUnion>
			`,
			expectedTypes: map[string]string{
				"Result": "{ type: string } | { type: number } | { type: boolean }",
			},
		},
		"ConditionalWithConstraints": {
			input: `
				type StringableOnly<T> = if T : string | number | boolean { T } else { never }
				type Result1 = StringableOnly<string>
				type Result2 = StringableOnly<number>
				type Result3 = StringableOnly<object>
			`,
			expectedTypes: map[string]string{
				"Result1": "string",
				"Result2": "number",
				"Result3": "never",
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

			// Some tests may have parse errors if features aren't implemented yet
			if test.expectErrors && len(errors) > 0 {
				t.Skipf("Skipping test %s - feature not yet implemented (parse errors: %v)", name, errors)
				return
			}

			if len(errors) > 0 {
				for i, err := range errors {
					fmt.Printf("Parse Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, errors, 0)

			inferCtx := Context{
				Scope:      Prelude(),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c := NewChecker()
			scope, inferErrors := c.InferModule(inferCtx, module)

			// Some tests may have inference errors if conditional types aren't fully implemented
			if test.expectErrors && len(inferErrors) > 0 {
				t.Skipf("Skipping test %s - conditional type inference not yet implemented (infer errors: %v)", name, inferErrors)
				return
			}

			if len(inferErrors) > 0 {
				for i, err := range inferErrors {
					fmt.Printf("Infer Error[%d]: %#v\n", i, err)
				}
				assert.Equal(t, inferErrors, []*Error{})
			}

			// Verify that all expected type aliases match the actual inferred types
			for expectedName, expectedType := range test.expectedTypes {
				binding, exists := scope.Types[expectedName]
				assert.True(t, exists, "Expected type alias %s to be declared", expectedName)

				if exists {
					expandedTyped, _ := c.expandType(inferCtx, binding.Type)
					actualType := expandedTyped.String()
					assert.Equal(t, expectedType, actualType, "Type alias mismatch for %s", expectedName)
				}
			}
		})
	}
}
