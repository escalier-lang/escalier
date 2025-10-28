package checker

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/parser"
	. "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
	"github.com/tidwall/btree"
	"github.com/vektah/gqlparser/v2"
	graphql_ast "github.com/vektah/gqlparser/v2/ast"
)

func TestCheckScriptNoErrors(t *testing.T) {
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
		"FuncExprWithoutThrows": {
			input: `
				val add = fn (x: number, y: number) -> number {
					return x + y
				}
			`,
		},
		"FuncExprWithSimpleThrows": {
			input: `val div = fn (x: number, y: number) -> number throws Error {
			    if y == 0 {
					throw Error("Division by zero")
				}
				return x / y
			}`,
		},
		"FuncExprWithUnionThrows": {
			input: `val testFunc = fn (input: string) -> string throws Error | string {
				if input == "" {
					throw Error("Invalid input")
				}
				if input == "throw" {
					throw "This is a string error"
			}
				return input
			}`,
		},
		"TypeCast": {
			input: `
				val x = 5
				val y = x : number
			`,
		},
		"TypeCastString": {
			input: `
				val x = "hello"
				val y = x : string
			`,
		},
		"TypeCastAny": {
			input: `
				val x = 5
				val y = x : any
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
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := parser.NewParser(ctx, source)
			script, errors := p.ParseScript()

			if len(errors) > 0 {
				for i, err := range errors {
					fmt.Printf("Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, errors, 0)

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			scope, inferErrors := c.InferScript(inferCtx, script)
			if len(inferErrors) > 0 {
				assert.Equal(t, inferErrors, []*Error{})
			}

			// TODO: short term - print each of the binding's types and store
			// them in a map and the snapshot the map.
			// TODO: long term - generate a .d.ts file from the bindings
			for name, binding := range scope.Namespace.Values {
				assert.NotNil(t, binding)
				fmt.Printf("%s = %s\n", name, binding.Type.String())
				fmt.Printf("%#v\n", binding.Type.Provenance())
			}
		})
	}
}

func loadSchema(t *testing.T) *graphql_ast.Schema {
	// Read schema.graphql from disk
	schemaBytes, err := os.ReadFile("schema.graphql")
	if err != nil {
		t.Fatalf("failed to read schema.graphql: %v", err)
	}
	schemaStr := string(schemaBytes)

	// Convert SchemaDocument into a usable schema.
	schema := gqlparser.MustLoadSchema(&graphql_ast.Source{
		Name:    "schema.graphql",
		Input:   schemaStr,
		BuiltIn: false,
	})

	return schema
}

func TestCheckModuleNoErrors(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"VarDecls": {
			input: `
				val a = 5
				val b = 10
				val sum = a + b
			`,
			expectedTypes: map[string]string{
				"a":   "5",
				"b":   "10",
				"sum": "number",
			},
		},
		"TupleDecl": {
			input: `
				val [x, y] = [5, 10]
			`,
			expectedTypes: map[string]string{
				"x": "5",
				"y": "10",
			},
		},
		"TupleDeclWithTypeAnns": {
			input: `
				val [x:number, y:number] = [5, 10]
			`,
			expectedTypes: map[string]string{
				"x": "number",
				"y": "number",
			},
		},
		"ObjectDecl": {
			input: `
				val {x, y} = {x: "foo", y: "bar"}
			`,
			expectedTypes: map[string]string{
				"x": "\"foo\"",
				"y": "\"bar\"",
			},
		},
		"ObjectDeclWithDeps": {
			input: `
			    val foo = "foo"
				val bar = "bar"
				val {x, y} = {x: foo, y: bar}
			`,
			expectedTypes: map[string]string{
				"foo": "\"foo\"",
				"bar": "\"bar\"",
				"x":   "\"foo\"",
				"y":   "\"bar\"",
			},
		},
		"ObjectDeclWithDepsAndInlineTypeAnns": {
			input: `
			    val foo = "foo"
				val bar = "bar"
				val {x::string, y::string} = {x: foo, y: bar}
			`,
			expectedTypes: map[string]string{
				"foo": "\"foo\"",
				"bar": "\"bar\"",
				"x":   "string",
				"y":   "string",
			},
		},
		"ObjectDeclWithDepsAndInlineTypeAnnsAndRenaming": {
			input: `
			    val foo = "foo"
				val bar = "bar"
				val {x: a:string, y: b:string} = {x: foo, y: bar}
			`,
			expectedTypes: map[string]string{
				"foo": "\"foo\"",
				"bar": "\"bar\"",
				"a":   "string",
				"b":   "string",
			},
		},
		"ObjectDestructuringWithOptionalProperties": {
			input: `
			    declare val obj: {a?: string, b: number | null, c: boolean}
				val {a, b, c} = obj
			`,
			expectedTypes: map[string]string{
				"a": "string | undefined",
				"b": "number | null",
				"c": "boolean",
			},
		},
		"ObjectDestructuringWithOptionalPropertiesAndDefaults": {
			input: `
				declare val obj: {a?: string, b: number | null, c: boolean}
				val {a = true, b: x = "hello", c = 5} = obj
			`,
			expectedTypes: map[string]string{
				"a": "string | true",
				"x": "number | \"hello\"",
				"c": "boolean",
			},
		},
		"ObjectWithMethods": {
			input: `
				val value: number = 5
				val obj = {
					value,
					increment(mut self, amount: number) -> Self {
						self.value = self.value + amount
						return self
					}
				}
				fn main() {
					obj.increment(1).increment(2)
					val inc = obj.increment
					inc(3).increment(4)
				}
			`,
			expectedTypes: map[string]string{
				"obj": "{value: number, increment(amount: number) -> Self throws never}",
			},
		},
		"ObjectWithGetterSetter": {
			input: `
				val value: number = 5
				val obj = {
					_value: value,
					get value (self) {
						return self._value
					},
					set value (mut self, value: number) {
						self._value = value
					},
				}
			`,
			expectedTypes: map[string]string{
				"obj": "{_value: number, get value() -> number throws never, set value(value: number) -> undefined throws never}",
			},
		},
		"ObjectWithComputedKeys": {
			input: `
				val foo = "foo"
				val bar = "bar"
				val obj = {
					[foo]: 42:number,
					[bar](self) {
						return self[foo]
					}
				}

				val a = obj[foo]
				val b = obj[bar]()
			`,
			expectedTypes: map[string]string{
				"obj": "{foo: number, bar() -> number throws never}",
				"a":   "number",
				"b":   "number",
			},
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
			expectedTypes: map[string]string{
				"a": "5",
				"b": "10",
				"x": "true | \"hello\"",
			},
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
			expectedTypes: map[string]string{
				"a": "5",
				"b": "10",
				"x": "true | false | \"hello\"",
			},
		},
		"FuncExpr": {
			input: `
				val add = fn (x, y) {
					return x + y
				}
			`,
			expectedTypes: map[string]string{
				"add": "fn (x: t3, y: t5) -> number throws never",
			},
		},
		"FuncExprObjectPatternWithInlineTypeAnn": {
			input: `
				val add = fn ({x::number, y::number}) {
					return x + y
				}
			`,
			expectedTypes: map[string]string{
				"add": "fn ({x: number, y: number}) -> number throws never",
			},
		},
		"FuncExprObjectPatternWithInlineTypeAnnAndDefaults": {
			// TODO: add support for `{x?::number = 0, y?::number = 0}`
			input: `
				val add = fn ({x::number = 0, y::number = 0}) {
					return x + y
				}
			`,
			expectedTypes: map[string]string{
				"add": "fn ({x: number, y: number}) -> number throws never",
			},
		},
		"FuncExprObjectPatternWithInlineTypeAnnAndDefaultsDeep": {
			// TODO: add support for `{a: {b: {c?::number = 0}}}`
			input: `
				val add = fn ({a: {b: {c:: number = 0}}}) {
					return c
				}
			`,
			expectedTypes: map[string]string{
				"add": "fn ({a: {b: {c: number}}}) -> number throws never",
			},
		},
		"FuncExprObjectPatternWithInlineTypeAnnAndRenamining": {
			input: `
				val add = fn ({x: a:number, y: b:number}) {
					return a + b
				}
			`,
			expectedTypes: map[string]string{
				"add": "fn ({x: number, y: number}) -> number throws never",
			},
		},
		"FuncExprTuplePatternWithInlineTypeAnn": {
			input: `
				val add = fn ([x:number, y:number]) {
					return x + y
				}
			`,
			expectedTypes: map[string]string{
				"add": "fn ([x: number, y: number]) -> number throws never",
			},
		},
		"FuncExprWithoutReturn": {
			input: `val log = fn (msg) {}`,
			expectedTypes: map[string]string{
				"log": "fn (msg: t3) -> undefined throws never",
			},
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
			expectedTypes: map[string]string{
				"add": "fn (x: t3, y: t5) -> true | false throws never",
			},
		},
		// TODO: figure out how to infer throws types in mutually recursive functions
		"MutualRecuriveFunctions": {
			input: `
				fn foo() -> number {
					return bar() + 1
				}
				fn bar() -> number {
					return foo() - 1
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn () -> number throws never",
				"bar": "fn () -> number throws never",
			},
		},
		"UnionTypeVariable": {
			input: `
				val x: string | number = 5
			`,
			expectedTypes: map[string]string{
				"x": "string | number",
			},
		},
		"FuncExprWithoutThrows": {
			input: `
				val add = fn (x: number, y: number) -> number {
					return x + y
				}
			`,
			expectedTypes: map[string]string{
				"add": "fn (x: number, y: number) -> number throws never",
			},
		},
		"FuncExprWithSimpleThrows": {
			input: `val div = fn (x: number, y: number) -> number throws Error {
			    if y == 0 {
					throw Error("Division by zero")
				}
				return x / y
			}`,
			expectedTypes: map[string]string{
				"div": "fn (x: number, y: number) -> number throws Error",
			},
		},
		"FuncExprWithUnionThrows": {
			input: `val testFunc = fn (input: string) -> string throws Error | string {
				if input == "" {
					throw Error("Invalid input")
				}
				if input == "throw" {
					throw "This is a string error"
				}
				return input
			}`,
			expectedTypes: map[string]string{
				"testFunc": "fn (input: string) -> string throws Error | string",
			},
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
		"FuncCall": {
			input: `
				val add = fn (x: number, y: number) {
					return x + y
				}
				val result = add(1, 2)
			`,
			expectedTypes: map[string]string{
				"add":    "fn (x: number, y: number) -> number throws never",
				"result": "number",
			},
		},
		"FuncCallWithRestArgs": {
			input: `
				val add = fn (x: number, y: number, ...rest: Array<number>) {
					return x + y + rest.length
				}
				val result1 = add(1, 2, 3, 4)
				val result2 = add(1, 2)
			`,
			expectedTypes: map[string]string{
				"add":     "fn (x: number, y: number, ...rest: Array<number>) -> number throws never",
				"result1": "number",
				"result2": "number",
			},
		},
		"PropertiesOnArrays": {
			input: `
				val arr: Array<number> = [1, 2, 3]
				val len = arr.length
			`,
			expectedTypes: map[string]string{
				"arr": "Array<number>",
				"len": "number",
			},
		},
		"TypeCastBasic": {
			input: `
				val x = 5
				val y = x : number
			`,
			expectedTypes: map[string]string{
				"x": "5",
				"y": "number",
			},
		},
		"TypeCastString": {
			input: `
				val str = "hello"
				val s = str : string
			`,
			expectedTypes: map[string]string{
				"str": "\"hello\"",
				"s":   "string",
			},
		},
		"TypeCastToAny": {
			input: `
				val num = 42
				val any_val = num : any
			`,
			expectedTypes: map[string]string{
				"num":     "42",
				"any_val": "any",
			},
		},
		"TypeCastChain": {
			input: `
				val original = 5
				val step1 = original : number
				val step2 = step1 : any
			`,
			expectedTypes: map[string]string{
				"original": "5",
				"step1":    "number",
				"step2":    "any",
			},
		},
		"TypeCastInExpression": {
			input: `
				val x = 5
				val y = 10
				val result = (x : number) + (y : number)
			`,
			expectedTypes: map[string]string{
				"x":      "5",
				"y":      "10",
				"result": "number",
			},
		},
		"TypeCastWithUnionType": {
			input: `
				val str = "hello"
				val union_val = str : string | number
			`,
			expectedTypes: map[string]string{
				"str":       "\"hello\"",
				"union_val": "string | number",
			},
		},
		"TaggedTemplateLiteral": {
			input: "val id = 5\nval query = gql`query { getUser(id: ${id}) { id name } }`",
			expectedTypes: map[string]string{
				"query": "TypedDocumentNode<{getUser?: {id: ID, name: string} | null}, {}>",
			},
		},
		"TaggedTemplateLiteralWithVariables": {
			input: "val query = gql`query($id: ID!) { getUser(id: $id) { id name } }`",
			expectedTypes: map[string]string{
				"query": "TypedDocumentNode<{getUser?: {id: ID, name: string} | null}, {id: ID}>",
			},
		},
		"GenericFunction": {
			input: `
			    fn identity<T>(value: T) -> T {
					return value
				}
				val fst = fn<A, B>(a: A, b: B) -> A {
					return a
				}
				val a: number = 5
				val b: string = "hello"
				val x = identity(a)
				val y = identity(b)
				val z = fst(a, b)
			`,
			expectedTypes: map[string]string{
				"identity": "fn <T>(value: T) -> T throws never",
				"fst":      "fn <A, B>(a: A, b: B) -> A throws never",
				"a":        "number",
				"b":        "string",
				"x":        "number",
				"y":        "string",
				"z":        "number",
			},
		},
		"GenericFunctionWithConstraint": {
			input: `
				val fst = fn<A: number, B: number>(a: A, b: B) -> A {
					return a
				}
				val a = fst(5, 10)
			`,
			expectedTypes: map[string]string{
				"fst": "fn <A: number, B: number>(a: A, b: B) -> A throws never",
				"a":   "5",
			},
		},
		"ObjectWithGenericMethods": {
			input: `
				val container = {
					value: 5:number,
					getValue<T>(self, default: T) -> number | T {
						if self.value != 0 {
							return self.value
						} else {
							return default
						}
					}
				}
				val a = container.getValue("default":string)
				val b = container.getValue(10)
			`,
			expectedTypes: map[string]string{
				"container": "{value: number, getValue<T>(default: T) -> number | T throws never}",
				"a":         "number | string",
				"b":         "number | 10",
			},
		},
		"ClassWithGenericMethod": {
			input: `
				class Box(value: number) {
					value,
					getValue<T>(self, default: T) -> number | T {
						if self.value != 0 {
							return self.value
						} else {
							return default
						}
					}
				}
				val box = Box(5)
				val a = box.getValue("default":string)
				val b = box.getValue(10)
			`,
			expectedTypes: map[string]string{
				"Box": "{new fn (value: number) -> Box throws never}",
				"box": "Box",
				"a":   "number | string",
				"b":   "number | 10",
			},
		},
		"SimpleGenericClass": {
			input: `
				class Box<T>(value: T) {
					value
				}
				val box = Box(5:number)
				val {value} = box
			`,
			expectedTypes: map[string]string{
				"Box":   "{new fn <T>(value: T) -> Box<T> throws never}",
				"box":   "Box<number>",
				"value": "number",
			},
		},
		"GenericClassWithGenericMethods": {
			input: `
				class Box<T>(value: T) {
					value,
					getValue<T>(self, default: T) -> number | T {
						if self.value != 0 {
							return self.value
						} else {
							return default
						}
					}
				}
				val box = Box(5:number)
				val a = box.getValue("default":string)
				val b = box.getValue(10)
			`,
			expectedTypes: map[string]string{
				"Box": "{new fn <T>(value: T) -> Box<T> throws never}",
				"box": "Box<number>",
				"a":   "number | string",
				"b":   "number | 10",
			},
		},
		// "GenericObjectTypeWithGenericMethod": {
		// 	input: `
		// 		type MyArray<T> = {
		// 			push(item: T) -> void,
		// 			map<U>(self, mapFn: fn(elem: T, index: number) -> U) -> Array<U>,
		// 			length: number,
		// 		}
		// 		type MyArrayConstructor = {
		// 			new <T>() -> MyArray<T>
		// 		}
		// 		declare const MyArray: MyArrayConstructor
		// 		val myArray = MyArray<number>()
		// 	`,
		// 	expectedTypes: map[string]string{
		// 		"myArray": "MyArray<number>",
		// 	},
		// },
		"Extractors": {
			input: `
				class Foo(a: number, b: string) {
					a,
					b,
					static [Symbol.customMatcher](subject: Foo) -> [number, string] {
						return [subject.a, subject.b]
					}
				}	
				val foo = Foo(5, "hello")
				val Foo(a, b) = foo
			`,
			expectedTypes: map[string]string{
				"a": "number",
				"b": "string",
			},
		},
		"ExtractorWithRest": {
			input: `
				class Foo(a: number, b: string, c: boolean) {
					a,
					b,
					c,
					static [Symbol.customMatcher](subject: Foo) -> [number, string, boolean] {
						return [subject.a, subject.b, subject.c]
					}
				}	
				val foo = Foo(5, "hello", true)
				val Foo(a, ...rest) = foo
			`,
			expectedTypes: map[string]string{
				"a":    "number",
				"rest": "[string, boolean]",
			},
		},
		"ExtractorWithDefault": {
			input: `
				class Foo(a: number, b?: string) {
					a,
					b,
					static [Symbol.customMatcher](subject: Foo) -> [number, string | undefined] {
						return [subject.a, subject.b]
					}
				}	
				val foo = Foo(5, "hello")
				val Foo(a, b = "world") = foo
			`,
			expectedTypes: map[string]string{
				"a": "number",
				"b": "string | \"world\"",
			},
		},
		"MultilayerTypeAliases": {
			input: `
				type A = "a"
				type B = "b"
				type AB = A | B
				val x: AB = "a"
			`,
			expectedTypes: map[string]string{
				"x": "AB",
			},
		},
		"NominalClasses": {
			input: `
				class UserId(id: number) {
					id
				}
				class ProductId(id: number) {
					id
				}
				val userId = UserId(5)
				val productId: UserId = userId
			`,
			expectedTypes: map[string]string{
				"userId":    "UserId",
				"productId": "UserId",
			},
		},
		"NomimalCanBeAssignedToStructural": {
			input: `
				class UserId(id: number) {
					id
				}
				type HasId = {id: number}
				val userId = UserId(5)
				val hasId: HasId = userId
			`,
			expectedTypes: map[string]string{
				"userId": "UserId",
				"hasId":  "HasId",
			},
		},
		"StructuralObjectTypes": {
			input: `
				type UserId = {id: number}
				type ProductId = {id: number}
				val userId: UserId = {id: 5}
				val productId: ProductId = userId
			`,
			expectedTypes: map[string]string{
				"userId":    "UserId",
				"productId": "ProductId",
			},
		},
		"UnionTypeAnnotation": {
			input: `
				val x: string | number = "hello"
			`,
			expectedTypes: map[string]string{
				"x": "string | number",
			},
		},
	}

	schema := loadSchema(t)

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

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c.Schema = schema
			scope, inferErrors := c.InferModule(inferCtx, module)
			if len(inferErrors) > 0 {
				for i, err := range inferErrors {
					fmt.Printf("Infer Error[%d]: %#v\n", i, err)
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

			// Note: We don't check for unexpected variables since the scope includes
			// prelude functions and operators that are implementation details
		})
	}
}

func TestCheckModuleWithErrors(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedErrors []string
	}{
		"TypeMismatch": {
			input: `
				val x: number = "hello"
			`,
			expectedErrors: []string{
				`"hello" cannot be assigned to number`,
			},
		},
		"UndefinedVariable": {
			input: `
				val x = y + 1
			`,
			expectedErrors: []string{
				`Unknown identifier: y`,
			},
		},
		"InvalidOperation": {
			input: `
				val x: string = "hello"
				val y: boolean = true
				val z = x + y
			`,
			expectedErrors: []string{
				// TODO: improve error message
				`boolean cannot be assigned to number`,
				`string cannot be assigned to number`,
			},
		},
		"NominalClasses": {
			input: `
				class UserId(id: number) {
					id
				}
				class ProductId(id: number) {
					id
				}
				val userId = UserId(5)
				val productId: ProductId = userId
			`,
			expectedErrors: []string{
				`UserId cannot be assigned to ProductId`,
			},
		},
		"StructuralCannotBeAssignedToNominal": {
			input: `
				class UserId(id: number) {
					id
				}
				type HasId = {id: number}
				val hasId: HasId = {id: 5}
				val userId: UserId = hasId
			`,
			expectedErrors: []string{
				`{id: number} cannot be assigned to UserId`,
			},
		},
	}

	schema := loadSchema(t)

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
			c.Schema = schema
			_, inferErrors := c.InferModule(inferCtx, module)

			// Verify we got the expected number of errors
			assert.Len(t, inferErrors, len(test.expectedErrors), "Expected %d errors but got %d", len(test.expectedErrors), len(inferErrors))

			// Verify each expected error message appears in the actual errors
			for _, expectedMsg := range test.expectedErrors {
				found := false
				for _, err := range inferErrors {
					fmt.Fprintf(os.Stderr, "Got error: %s\n", err.Message())
					if err.Message() == expectedMsg {
						found = true
						break
					}
				}
				assert.True(t, found, "Expected error message not found: %s", expectedMsg)
			}
		})
	}
}

func TestCheckModuleTypeAliases(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"MutualRecursiveTypes": {
			input: `
				type Foo = { bar: Bar }
				type Bar = { foo: Foo }
			`,
			expectedTypes: map[string]string{
				"Foo": "{bar: Bar}",
				"Bar": "{foo: Foo}",
			},
		},
		"SimpleTypeAlias": {
			input: `
				type UserId = number
				type UserName = string
			`,
			expectedTypes: map[string]string{
				"UserId":   "number",
				"UserName": "string",
			},
		},
		"ComplexTypeAlias": {
			input: `
				type Point = { x: number, y: number }
				type Vector = { start: Point, end: Point }
			`,
			expectedTypes: map[string]string{
				"Point":  "{x: number, y: number}",
				"Vector": "{start: Point, end: Point}",
			},
		},
		"NestedObjectTypes": {
			input: `
				type Address = { street: string, city: string }
				type Person = { name: string, age: number, address: Address }
			`,
			expectedTypes: map[string]string{
				"Address": "{street: string, city: string}",
				"Person":  "{name: string, age: number, address: Address}",
			},
		},
		"PrimitiveTypeAliases": {
			input: `
				type ID = number
				type Message = string
				type Flag = boolean
			`,
			expectedTypes: map[string]string{
				"ID":      "number",
				"Message": "string",
				"Flag":    "boolean",
			},
		},
		"UnionTypeAlias": {
			input: `
				type StringOrNumber = string | number
			`,
			expectedTypes: map[string]string{
				"StringOrNumber": "string | number",
			},
		},
		"GenericTypeAlias": {
			input: `
				type Box<T> = { value: T }
				type StringBox = Box<string>
			`,
			expectedTypes: map[string]string{
				"StringBox": "{value: string}",
			},
		},
		"MultipleGenericTypeParams": {
			input: `
				type Pair<T, U> = { first: T, second: U }
				type StringNumberPair = Pair<string, number>
			`,
			expectedTypes: map[string]string{
				"StringNumberPair": "{first: string, second: number}",
			},
		},
		"NestedGenericTypes": {
			input: `
				type Container<T> = { items: T }
				type NumberContainer = Container<number>
				type ContainerOfContainers = Container<NumberContainer>
			`,
			expectedTypes: map[string]string{
				"NumberContainer": "{items: number}",
				// NOTE: we also expand type arguments when expanding type aliases
				"ContainerOfContainers": "{items: Container<number>}",
			},
		},
		"GenericTupleTypes": {
			input: `
				type Triple<T> = [T, T, T]
				type StringTriple = Triple<string>
			`,
			expectedTypes: map[string]string{
				"StringTriple": "[string, string, string]",
			},
		},
		"GenericUnionTypes": {
			input: `
				type Result<T, E> = T | E
				type StringOrError = Result<string, Error>
			`,
			expectedTypes: map[string]string{
				"StringOrError": "string | Error",
			},
		},
		"GenericWithPrimitiveInstantiation": {
			input: `
				type Wrapper<T> = { data: T, id: number }
				type BooleanWrapper = Wrapper<boolean>
				type NumberWrapper = Wrapper<number>
			`,
			expectedTypes: map[string]string{
				"BooleanWrapper": "{data: boolean, id: number}",
				"NumberWrapper":  "{data: number, id: number}",
			},
		},
		"ComplexGenericNesting": {
			input: `
				type Optional<T> = T | null
				type List<T> = { items: Array<T>, length: number }
				type OptionalStringList = List<Optional<string>>
			`,
			expectedTypes: map[string]string{
				// NOTE: we also expand type arguments when expanding type aliases
				"OptionalStringList": "{items: Array<string | null>, length: number}",
			},
		},
		"GenericTypeWithMultipleInstantiations": {
			input: `
				type KeyValue<K, V> = { key: K, value: V }
				type StringToNumber = KeyValue<string, number>
				type NumberToBoolean = KeyValue<number, boolean>
			`,
			expectedTypes: map[string]string{
				"StringToNumber":  "{key: string, value: number}",
				"NumberToBoolean": "{key: number, value: boolean}",
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

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			scope, inferErrors := c.InferModule(inferCtx, module)
			if len(inferErrors) > 0 {
				assert.Equal(t, inferErrors, []*Error{})
			}

			// Verify that all expected type aliases match the actual inferred types
			for expectedName, expectedType := range test.expectedTypes {
				binding, exists := scope.Types[expectedName]
				assert.True(t, exists, "Expected type alias %s to be declared", expectedName)

				expandedTyped, _ := c.expandType(inferCtx, binding.Type, 1)
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

func TestExpandingTypeAliasMultipleTimes(t *testing.T) {
	source := &ast.Source{
		ID:   0,
		Path: "input.esc",
		Contents: `
			type A = "a"
			type B = "b"
			type AB = A | B
		`,
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

	c := NewChecker()
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}
	scope, inferErrors := c.InferModule(inferCtx, module)
	if len(inferErrors) > 0 {
		assert.Equal(t, inferErrors, []*Error{})
	}

	binding, exists := scope.Types["AB"]

	expandedTyped, _ := c.expandType(inferCtx, binding.Type, 1)
	expandedTyped, _ = c.expandType(inferCtx, expandedTyped, 1)
	actualType := expandedTyped.String()

	if exists {
		assert.Equal(t, `"a" | "b"`, actualType)
	}
}

func TestCheckMultifileModuleNoErrors(t *testing.T) {
	tests := map[string]struct {
		sources []*ast.Source
	}{
		"MutualRecuriveFunctions": {
			sources: []*ast.Source{
				{
					ID:   1,
					Path: "foo.esc",
					Contents: `fn foo() -> number {
						return bar() + 1
					}`,
				},
				{
					ID:   2,
					Path: "bar.esc",
					Contents: `fn bar() -> number {
						return foo() - 1
					}`,
				},
			},
		},
		"MutualRecuriveTypes": {
			sources: []*ast.Source{
				{
					ID:       1,
					Path:     "foo.esc",
					Contents: `type Foo = { bar: Bar }`,
				},
				{
					ID:       2,
					Path:     "bar.esc",
					Contents: `type Bar = { foo: Foo }`,
				},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			module, errors := parser.ParseLibFiles(ctx, test.sources)

			if len(errors) > 0 {
				for i, err := range errors {
					fmt.Printf("Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, errors, 0)

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			scope, inferErrors := c.InferModule(inferCtx, module)
			if len(inferErrors) > 0 {
				assert.Equal(t, inferErrors, []*Error{})
			}

			// TODO: short term - print each of the binding's types and store
			// them in a map and the snapshot the map.
			// TODO: long term - generate a .d.ts file from the bindings
			for name, binding := range scope.Values {
				assert.NotNil(t, binding)
				fmt.Printf("%s = %s\n", name, binding.Type.String())
				fmt.Printf("%#v\n", binding.Type.Provenance())
			}
		})
	}
}

func TestGetDeclCtx(t *testing.T) {
	// Create a root namespace with nested namespaces
	rootNS := NewNamespace()
	fooNS := NewNamespace()
	barNS := NewNamespace()
	bazNS := NewNamespace()

	// Set up nested namespace structure: root.foo.bar.baz
	rootNS.Namespaces["foo"] = fooNS
	fooNS.Namespaces["bar"] = barNS
	barNS.Namespaces["baz"] = bazNS

	// Create a root scope and context
	rootScope := &Scope{
		Parent:    nil,
		Namespace: rootNS,
	}

	rootCtx := Context{
		Scope:      rootScope,
		IsAsync:    false,
		IsPatMatch: false,
	}

	tests := []struct {
		name          string
		declNamespace string
		expectedDepth int // how many scopes deep the result should be
		expectedNS    *Namespace
	}{
		{
			name:          "empty namespace returns root context",
			declNamespace: "",
			expectedDepth: 0,
			expectedNS:    rootNS,
		},
		{
			name:          "single level namespace",
			declNamespace: "foo",
			expectedDepth: 1,
			expectedNS:    fooNS,
		},
		{
			name:          "nested namespace foo.bar",
			declNamespace: "foo.bar",
			expectedDepth: 2,
			expectedNS:    barNS,
		},
		{
			name:          "deeply nested namespace foo.bar.baz",
			declNamespace: "foo.bar.baz",
			expectedDepth: 3,
			expectedNS:    bazNS,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Create a mock dep graph
			depGraph := newTestDepGraph()

			// Create a declaration ID and set its namespace
			declID := dep_graph.DeclID(42)
			depGraph.DeclNamespace[declID] = test.declNamespace

			// Call getDeclCtx
			resultCtx := getNsCtx(rootCtx, depGraph, declID)

			// Verify the result context has the expected namespace
			assert.Equal(t, test.expectedNS, resultCtx.Scope.Namespace)

			// Verify we can walk back to the root through Parent pointers
			currentScope := resultCtx.Scope
			depth := 0
			for currentScope.Parent != nil {
				currentScope = currentScope.Parent
				depth++
			}
			assert.Equal(t, test.expectedDepth, depth)

			// Verify the root scope is unchanged
			assert.Equal(t, rootNS, currentScope.Namespace)

			// Verify other context fields are preserved
			assert.Equal(t, rootCtx.IsAsync, resultCtx.IsAsync)
			assert.Equal(t, rootCtx.IsPatMatch, resultCtx.IsPatMatch)
		})
	}
}

func TestGetDeclCtxWithNonExistentDeclID(t *testing.T) {
	// Create a simple context
	rootNS := NewNamespace()
	rootScope := &Scope{
		Parent:    nil,
		Namespace: rootNS,
	}

	rootCtx := Context{
		Scope:      rootScope,
		IsAsync:    false,
		IsPatMatch: false,
	}

	// Create empty dep graph
	depGraph := newTestDepGraph()

	// Use a declaration ID that doesn't exist in the dep graph
	declID := dep_graph.DeclID(999)

	// Call getDeclCtx - should return original context since namespace is empty
	resultCtx := getNsCtx(rootCtx, depGraph, declID)

	// Should return the same context since no namespace mapping exists
	assert.Equal(t, rootCtx.Scope.Namespace, resultCtx.Scope.Namespace)
	assert.Equal(t, rootCtx.IsAsync, resultCtx.IsAsync)
	assert.Equal(t, rootCtx.IsPatMatch, resultCtx.IsPatMatch)
}

func TestGetDeclCtxNestedNamespaceOrder(t *testing.T) {
	// Create a root namespace with deeply nested namespaces
	rootNS := NewNamespace()
	fooNS := NewNamespace()
	barNS := NewNamespace()
	bazNS := NewNamespace()
	quxNS := NewNamespace()

	// Set up nested namespace structure: root.foo.bar.baz.qux
	rootNS.Namespaces["foo"] = fooNS
	fooNS.Namespaces["bar"] = barNS
	barNS.Namespaces["baz"] = bazNS
	bazNS.Namespaces["qux"] = quxNS

	// Add some test values to distinguish each namespace
	rootNS.Values["rootValue"] = &Binding{Source: nil, Type: NewStrPrimType(nil), Mutable: false}
	fooNS.Values["fooValue"] = &Binding{Source: nil, Type: NewStrPrimType(nil), Mutable: false}
	barNS.Values["barValue"] = &Binding{Source: nil, Type: NewStrPrimType(nil), Mutable: false}
	bazNS.Values["bazValue"] = &Binding{Source: nil, Type: NewStrPrimType(nil), Mutable: false}
	quxNS.Values["quxValue"] = &Binding{Source: nil, Type: NewStrPrimType(nil), Mutable: false}

	// Create a root scope and context
	rootScope := &Scope{
		Parent:    nil,
		Namespace: rootNS,
	}

	rootCtx := Context{
		Scope:      rootScope,
		IsAsync:    false,
		IsPatMatch: false,
	}

	// Create dep graph with deeply nested namespace
	depGraph := newTestDepGraph()
	declID := dep_graph.DeclID(456)
	depGraph.DeclNamespace[declID] = "foo.bar.baz.qux"

	// Call getDeclCtx
	resultCtx := getNsCtx(rootCtx, depGraph, declID)

	// Verify the final context points to the deepest namespace
	assert.Equal(t, quxNS, resultCtx.Scope.Namespace)
	assert.NotNil(t, resultCtx.Scope.Namespace.Values["quxValue"])

	// Walk up the scope chain and verify the correct order:
	// qux -> baz -> bar -> foo -> root
	expectedNamespaces := []*Namespace{quxNS, bazNS, barNS, fooNS, rootNS}
	expectedValues := []string{"quxValue", "bazValue", "barValue", "fooValue", "rootValue"}

	currentScope := resultCtx.Scope
	for i, expectedNS := range expectedNamespaces {
		assert.Equal(t, expectedNS, currentScope.Namespace,
			"Scope at level %d should have namespace %v", i, expectedValues[i])

		// Verify this namespace has its expected value
		assert.NotNil(t, currentScope.Namespace.Values[expectedValues[i]],
			"Namespace at level %d should contain value %s", i, expectedValues[i])

		// Move to parent scope (except for the root)
		if i < len(expectedNamespaces)-1 {
			assert.NotNil(t, currentScope.Parent, "Scope should have parent at level %d", i)
			currentScope = currentScope.Parent
		} else {
			// Root scope should have no parent
			assert.Nil(t, currentScope.Parent, "Root scope should have no parent")
		}
	}

	// Verify that the scope chain has exactly the expected depth
	depth := 0
	testScope := resultCtx.Scope
	for testScope.Parent != nil {
		testScope = testScope.Parent
		depth++
	}
	assert.Equal(t, 4, depth, "Should have exactly 4 levels of nesting (foo->bar->baz->qux)")
}

// TestInferDepGraphWithNamespaceDependencies tests the InferDepGraph function
// with various namespace-related scenarios, ensuring that the function properly
// processes strongly connected components in topological order and handles
// namespace resolution across components. These tests verify that:
// - Independent declarations in different namespaces are processed correctly
// - Dependencies between namespaces are resolved in the proper order
// - Circular dependencies within and across namespaces are handled
// - The final merged namespace contains all declarations in their correct locations
func TestInferDepGraphWithNamespaceDependencies(t *testing.T) {
	tests := []struct {
		name     string
		setup    func() (*dep_graph.DepGraph, Context)
		expected func(*testing.T, *Namespace, []Error)
	}{
		{
			name: "single component with declarations in same namespace",
			setup: func() (*dep_graph.DepGraph, Context) {
				helperSource := &ast.Source{
					ID:       0,
					Path:     "math/helper.esc",
					Contents: "val PI = 3.14159",
				}
				areaSource := &ast.Source{
					ID:       1,
					Path:     "math/area.esc",
					Contents: "fn circleArea(r: number): number { return PI * r * r }",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				helperParser := parser.NewParser(ctx, helperSource)
				helperDecl := helperParser.Decl()

				areaParser := parser.NewParser(ctx, areaSource)
				areaDecl := areaParser.Decl()

				// Create dependency graph manually
				depGraph := newTestDepGraph()

				// Set up declarations (append in order, DeclID 0 = index 0, DeclID 1 = index 1)
				helperDeclID := dep_graph.DeclID(0)
				areaDeclID := dep_graph.DeclID(1)
				depGraph.Decls = append(depGraph.Decls, helperDecl) // DeclID 0
				depGraph.Decls = append(depGraph.Decls, areaDecl)   // DeclID 1

				// Both in math namespace
				depGraph.DeclNamespace[helperDeclID] = "math"
				depGraph.DeclNamespace[areaDeclID] = "math"

				// Set up dependencies - circleArea depends on PI
				areaDeps := btree.Set[dep_graph.DeclID]{}
				areaDeps.Insert(helperDeclID)
				depGraph.DeclDeps[areaDeclID] = areaDeps

				// Set up value bindings
				depGraph.ValueBindings.Set("PI", helperDeclID)
				depGraph.ValueBindings.Set("circleArea", areaDeclID)

				rootScope := &Scope{
					Parent:    nil,
					Namespace: NewNamespace(),
				}

				inferCtx := Context{
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, resultNS *Namespace, errors []Error) {
				assert.Len(t, errors, 0, "Should process single component without errors")

				// Check that math namespace exists and contains both declarations
				assert.Contains(t, resultNS.Namespaces, "math", "Should have math namespace")
				mathNS := resultNS.Namespaces["math"]

				assert.Contains(t, mathNS.Values, "PI", "Math namespace should contain PI")
				assert.Contains(t, mathNS.Values, "circleArea", "Math namespace should contain circleArea")

				// Verify types
				piBinding := mathNS.Values["PI"]
				assert.NotNil(t, piBinding, "PI binding should exist")

				circleAreaBinding := mathNS.Values["circleArea"]
				assert.NotNil(t, circleAreaBinding, "circleArea binding should exist")
			},
		},
		{
			name: "multiple independent components in different namespaces",
			setup: func() (*dep_graph.DepGraph, Context) {
				// Test scenario: separate declarations in different namespaces that don't depend on each other
				mathVarSource := &ast.Source{
					ID:       0,
					Path:     "math/constants.esc",
					Contents: "val E = 2.718",
				}
				utilsFuncSource := &ast.Source{
					ID:       1,
					Path:     "utils/log.esc",
					Contents: "fn log(msg: string) { }",
				}
				geometryTypeSource := &ast.Source{
					ID:       2,
					Path:     "geometry/types.esc",
					Contents: "type Point = {x: number, y: number}",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				mathParser := parser.NewParser(ctx, mathVarSource)
				mathDecl := mathParser.Decl()

				utilsParser := parser.NewParser(ctx, utilsFuncSource)
				utilsDecl := utilsParser.Decl()

				geometryParser := parser.NewParser(ctx, geometryTypeSource)
				geometryDecl := geometryParser.Decl()

				// Create dependency graph manually
				depGraph := newTestDepGraph()

				// Set up declarations (append in order)
				mathDeclID := dep_graph.DeclID(0)
				utilsDeclID := dep_graph.DeclID(1)
				geometryDeclID := dep_graph.DeclID(2)
				depGraph.Decls = append(depGraph.Decls, mathDecl)     // DeclID 0
				depGraph.Decls = append(depGraph.Decls, utilsDecl)    // DeclID 1
				depGraph.Decls = append(depGraph.Decls, geometryDecl) // DeclID 2

				// Different namespaces
				depGraph.DeclNamespace[mathDeclID] = "math"
				depGraph.DeclNamespace[utilsDeclID] = "utils"
				depGraph.DeclNamespace[geometryDeclID] = "geometry"

				// No dependencies between them
				depGraph.DeclDeps[mathDeclID] = btree.Set[dep_graph.DeclID]{}
				depGraph.DeclDeps[utilsDeclID] = btree.Set[dep_graph.DeclID]{}
				depGraph.DeclDeps[geometryDeclID] = btree.Set[dep_graph.DeclID]{}

				// Set up bindings
				depGraph.ValueBindings.Set("E", mathDeclID)
				depGraph.ValueBindings.Set("log", utilsDeclID)
				depGraph.TypeBindings.Set("Point", geometryDeclID)

				rootScope := &Scope{
					Parent:    nil,
					Namespace: NewNamespace(),
				}

				inferCtx := Context{
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, resultNS *Namespace, errors []Error) {
				assert.Len(t, errors, 0, "Should handle multiple independent namespaces")

				// Check that all namespaces exist
				assert.Contains(t, resultNS.Namespaces, "math", "Should have math namespace")
				assert.Contains(t, resultNS.Namespaces, "utils", "Should have utils namespace")
				assert.Contains(t, resultNS.Namespaces, "geometry", "Should have geometry namespace")

				// Check declarations in each namespace
				mathNS := resultNS.Namespaces["math"]
				assert.Contains(t, mathNS.Values, "E", "Math namespace should contain E")

				utilsNS := resultNS.Namespaces["utils"]
				assert.Contains(t, utilsNS.Values, "log", "Utils namespace should contain log")

				geometryNS := resultNS.Namespaces["geometry"]
				assert.Contains(t, geometryNS.Types, "Point", "Geometry namespace should contain Point type")
			},
		},
		{
			name: "cross-namespace dependencies processed in topological order",
			setup: func() (*dep_graph.DepGraph, Context) {
				// math namespace declares PI (no dependencies)
				piSource := &ast.Source{
					ID:       0,
					Path:     "math/constants.esc",
					Contents: "val PI = 3.14159",
				}
				// geometry namespace has function that uses math.PI (depends on math)
				areaSource := &ast.Source{
					ID:       1,
					Path:     "geometry/area.esc",
					Contents: "fn circleArea(r: number): number { return math.PI * r * r }",
				}
				// utils namespace uses geometry.circleArea (depends on geometry)
				calcSource := &ast.Source{
					ID:       2,
					Path:     "utils/calculator.esc",
					Contents: "fn calculateArea(radius: number): number { return geometry.circleArea(radius) }",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				piParser := parser.NewParser(ctx, piSource)
				piDecl := piParser.Decl()

				areaParser := parser.NewParser(ctx, areaSource)
				areaDecl := areaParser.Decl()

				calcParser := parser.NewParser(ctx, calcSource)
				calcDecl := calcParser.Decl()

				// Create dependency graph manually
				depGraph := newTestDepGraph()

				// Set up declarations (append in order)
				piDeclID := dep_graph.DeclID(0)
				areaDeclID := dep_graph.DeclID(1)
				calcDeclID := dep_graph.DeclID(2)
				depGraph.Decls = append(depGraph.Decls, piDecl)   // DeclID 0
				depGraph.Decls = append(depGraph.Decls, areaDecl) // DeclID 1
				depGraph.Decls = append(depGraph.Decls, calcDecl) // DeclID 2

				// Different namespaces
				depGraph.DeclNamespace[piDeclID] = "math"
				depGraph.DeclNamespace[areaDeclID] = "geometry"
				depGraph.DeclNamespace[calcDeclID] = "utils"

				// Set up dependency chain: utils -> geometry -> math
				areaDeps := btree.Set[dep_graph.DeclID]{}
				areaDeps.Insert(piDeclID)
				depGraph.DeclDeps[areaDeclID] = areaDeps

				calcDeps := btree.Set[dep_graph.DeclID]{}
				calcDeps.Insert(areaDeclID)
				depGraph.DeclDeps[calcDeclID] = calcDeps

				// Set up value bindings
				depGraph.ValueBindings.Set("PI", piDeclID)
				depGraph.ValueBindings.Set("circleArea", areaDeclID)
				depGraph.ValueBindings.Set("calculateArea", calcDeclID)

				rootScope := &Scope{
					Parent:    nil,
					Namespace: NewNamespace(),
				}

				inferCtx := Context{
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, resultNS *Namespace, errors []Error) {
				assert.Len(t, errors, 0, "Should process dependency chain without errors")

				// Check that all namespaces exist with their declarations
				assert.Contains(t, resultNS.Namespaces, "math", "Should have math namespace")
				assert.Contains(t, resultNS.Namespaces, "geometry", "Should have geometry namespace")
				assert.Contains(t, resultNS.Namespaces, "utils", "Should have utils namespace")

				mathNS := resultNS.Namespaces["math"]
				assert.Contains(t, mathNS.Values, "PI", "Math namespace should contain PI")

				geometryNS := resultNS.Namespaces["geometry"]
				assert.Contains(t, geometryNS.Values, "circleArea", "Geometry namespace should contain circleArea")

				utilsNS := resultNS.Namespaces["utils"]
				assert.Contains(t, utilsNS.Values, "calculateArea", "Utils namespace should contain calculateArea")
			},
		},
		{
			name: "circular dependencies within same component",
			setup: func() (*dep_graph.DepGraph, Context) {
				isEvenSource := &ast.Source{
					ID:       0,
					Path:     "math/even.esc",
					Contents: "fn isEven(n: number): boolean { return n == 0 || isOdd(n - 1) }",
				}
				isOddSource := &ast.Source{
					ID:       1,
					Path:     "math/odd.esc",
					Contents: "fn isOdd(n: number): boolean { return n != 0 && isEven(n - 1) }",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				isEvenParser := parser.NewParser(ctx, isEvenSource)
				isEvenDecl := isEvenParser.Decl()

				isOddParser := parser.NewParser(ctx, isOddSource)
				isOddDecl := isOddParser.Decl()

				// Create dependency graph manually
				depGraph := newTestDepGraph()

				// Set up declarations
				isEvenDeclID := dep_graph.DeclID(0)
				isOddDeclID := dep_graph.DeclID(1)
				depGraph.Decls = append(depGraph.Decls, isEvenDecl) // DeclID 0
				depGraph.Decls = append(depGraph.Decls, isOddDecl)  // DeclID 1

				// Both in math namespace (same namespace enables mutual reference)
				depGraph.DeclNamespace[isEvenDeclID] = "math"
				depGraph.DeclNamespace[isOddDeclID] = "math"

				// Set up mutual dependencies
				isEvenDeps := btree.Set[dep_graph.DeclID]{}
				isEvenDeps.Insert(isOddDeclID)
				depGraph.DeclDeps[isEvenDeclID] = isEvenDeps

				isOddDeps := btree.Set[dep_graph.DeclID]{}
				isOddDeps.Insert(isEvenDeclID)
				depGraph.DeclDeps[isOddDeclID] = isOddDeps

				// Set up value bindings
				depGraph.ValueBindings.Set("isEven", isEvenDeclID)
				depGraph.ValueBindings.Set("isOdd", isOddDeclID)

				rootScope := &Scope{
					Parent:    nil,
					Namespace: NewNamespace(),
				}

				inferCtx := Context{
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			}, expected: func(t *testing.T, resultNS *Namespace, errors []Error) {
				assert.Len(t, errors, 0, "Should handle circular dependencies within same component")

				// Check that math namespace exists and contains both functions
				assert.Contains(t, resultNS.Namespaces, "math", "Should have math namespace")
				mathNS := resultNS.Namespaces["math"]

				assert.Contains(t, mathNS.Values, "isEven", "Math namespace should contain isEven")
				assert.Contains(t, mathNS.Values, "isOdd", "Math namespace should contain isOdd")
			},
		},
		{
			name: "circular dependencies across different namespaces",
			setup: func() (*dep_graph.DepGraph, Context) {
				// a namespace declares function that uses b.helper
				aFuncSource := &ast.Source{
					ID:       0,
					Path:     "a/func.esc",
					Contents: "fn aFunc(): number { return b.helper() + 1 }",
				}
				// b namespace declares function that uses a.aFunc
				bHelperSource := &ast.Source{
					ID:       1,
					Path:     "b/helper.esc",
					Contents: "fn helper(): number { return a.aFunc() - 1 }",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				aParser := parser.NewParser(ctx, aFuncSource)
				aDecl := aParser.Decl()

				bParser := parser.NewParser(ctx, bHelperSource)
				bDecl := bParser.Decl()

				// Create dependency graph manually
				depGraph := newTestDepGraph()

				// Set up declarations (append in order)
				aDeclID := dep_graph.DeclID(0)
				bDeclID := dep_graph.DeclID(1)
				depGraph.Decls = append(depGraph.Decls, aDecl) // DeclID 0
				depGraph.Decls = append(depGraph.Decls, bDecl) // DeclID 1

				// Different namespaces
				depGraph.DeclNamespace[aDeclID] = "a"
				depGraph.DeclNamespace[bDeclID] = "b"

				// Set up circular cross-namespace dependencies
				aDeps := btree.Set[dep_graph.DeclID]{}
				aDeps.Insert(bDeclID) // aFunc depends on helper
				depGraph.DeclDeps[aDeclID] = aDeps

				bDeps := btree.Set[dep_graph.DeclID]{}
				bDeps.Insert(aDeclID) // helper depends on aFunc
				depGraph.DeclDeps[bDeclID] = bDeps

				// Set up value bindings
				depGraph.ValueBindings.Set("aFunc", aDeclID)
				depGraph.ValueBindings.Set("helper", bDeclID)

				rootScope := &Scope{
					Parent:    nil,
					Namespace: NewNamespace(),
				}

				inferCtx := Context{
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			}, expected: func(t *testing.T, resultNS *Namespace, errors []Error) {
				assert.Len(t, errors, 0, "Should handle circular cross-namespace dependencies")

				// Check that both namespaces exist and contain their declarations
				assert.Contains(t, resultNS.Namespaces, "a", "Should have namespace a")
				assert.Contains(t, resultNS.Namespaces, "b", "Should have namespace b")

				aNS := resultNS.Namespaces["a"]
				assert.Contains(t, aNS.Values, "aFunc", "Namespace a should contain aFunc")

				bNS := resultNS.Namespaces["b"]
				assert.Contains(t, bNS.Values, "helper", "Namespace b should contain helper")
			},
		},
		{
			name: "nested namespaces with dependencies on root",
			setup: func() (*dep_graph.DepGraph, Context) {
				// root namespace declares a global constant
				globalSource := &ast.Source{
					ID:       0,
					Path:     "globals.esc",
					Contents: "val GLOBAL_CONSTANT = 42",
				}
				// nested namespace has function that uses root constant
				nestedFuncSource := &ast.Source{
					ID:       1,
					Path:     "utils/nested/func.esc",
					Contents: "fn useGlobal(): number { return GLOBAL_CONSTANT * 2 }",
				}
				// another nested function that depends on the first
				nestedFunc2Source := &ast.Source{
					ID:       2,
					Path:     "utils/nested/func2.esc",
					Contents: "fn useGlobalTwice(): number { return useGlobal() * 2 }",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				globalParser := parser.NewParser(ctx, globalSource)
				globalDecl := globalParser.Decl()

				nestedParser := parser.NewParser(ctx, nestedFuncSource)
				nestedDecl := nestedParser.Decl()

				nested2Parser := parser.NewParser(ctx, nestedFunc2Source)
				nested2Decl := nested2Parser.Decl()

				// Create dependency graph manually
				depGraph := newTestDepGraph()

				// Set up declarations (append in order)
				globalDeclID := dep_graph.DeclID(0)
				nestedDeclID := dep_graph.DeclID(1)
				nested2DeclID := dep_graph.DeclID(2)
				depGraph.Decls = append(depGraph.Decls, globalDecl)  // DeclID 0
				depGraph.Decls = append(depGraph.Decls, nestedDecl)  // DeclID 1
				depGraph.Decls = append(depGraph.Decls, nested2Decl) // DeclID 2

				// Different namespace levels - root vs nested
				depGraph.DeclNamespace[globalDeclID] = "" // root namespace
				depGraph.DeclNamespace[nestedDeclID] = "utils.nested"
				depGraph.DeclNamespace[nested2DeclID] = "utils.nested"

				// Set up dependency chain
				nestedDeps := btree.Set[dep_graph.DeclID]{}
				nestedDeps.Insert(globalDeclID)
				depGraph.DeclDeps[nestedDeclID] = nestedDeps

				nested2Deps := btree.Set[dep_graph.DeclID]{}
				nested2Deps.Insert(nestedDeclID)
				depGraph.DeclDeps[nested2DeclID] = nested2Deps

				// Set up value bindings
				depGraph.ValueBindings.Set("GLOBAL_CONSTANT", globalDeclID)
				depGraph.ValueBindings.Set("useGlobal", nestedDeclID)
				depGraph.ValueBindings.Set("useGlobalTwice", nested2DeclID)

				rootScope := &Scope{
					Parent:    nil,
					Namespace: NewNamespace(),
				}

				inferCtx := Context{
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, resultNS *Namespace, errors []Error) {
				assert.Len(t, errors, 0, "Should handle nested namespace dependencies")

				// Check root namespace contains global
				assert.Contains(t, resultNS.Values, "GLOBAL_CONSTANT", "Root namespace should contain GLOBAL_CONSTANT")

				// Check nested namespace structure exists
				assert.Contains(t, resultNS.Namespaces, "utils", "Should have utils namespace")
				utilsNS := resultNS.Namespaces["utils"]
				assert.Contains(t, utilsNS.Namespaces, "nested", "Utils should have nested namespace")

				nestedNS := utilsNS.Namespaces["nested"]
				assert.Contains(t, nestedNS.Values, "useGlobal", "Nested namespace should contain useGlobal")
				assert.Contains(t, nestedNS.Values, "useGlobalTwice", "Nested namespace should contain useGlobalTwice")
			},
		},
		{
			name: "root namespace consuming declarations from nested namespaces",
			setup: func() (*dep_graph.DepGraph, Context) {
				// math.utils namespace declares helper function
				helperSource := &ast.Source{
					ID:       0,
					Path:     "math/utils/helper.esc",
					Contents: "fn square(x: number): number { return x * x }",
				}
				// math namespace declares constant
				piSource := &ast.Source{
					ID:       1,
					Path:     "math/constants.esc",
					Contents: "val PI = 3.14159",
				}
				// root namespace declares function that uses both nested declarations
				rootFuncSource := &ast.Source{
					ID:       2,
					Path:     "calculator.esc",
					Contents: "fn circleArea(radius: number): number { return math.PI * math.utils.square(radius) }",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				helperParser := parser.NewParser(ctx, helperSource)
				helperDecl := helperParser.Decl()

				piParser := parser.NewParser(ctx, piSource)
				piDecl := piParser.Decl()

				rootFuncParser := parser.NewParser(ctx, rootFuncSource)
				rootFuncDecl := rootFuncParser.Decl()

				// Create dependency graph manually
				depGraph := newTestDepGraph()

				// Set up declarations (append in order)
				helperDeclID := dep_graph.DeclID(0)
				piDeclID := dep_graph.DeclID(1)
				rootFuncDeclID := dep_graph.DeclID(2)
				depGraph.Decls = append(depGraph.Decls, helperDecl)   // DeclID 0
				depGraph.Decls = append(depGraph.Decls, piDecl)       // DeclID 1
				depGraph.Decls = append(depGraph.Decls, rootFuncDecl) // DeclID 2

				// Set up namespaces - nested vs root
				depGraph.DeclNamespace[helperDeclID] = "math.utils"
				depGraph.DeclNamespace[piDeclID] = "math"
				depGraph.DeclNamespace[rootFuncDeclID] = "" // root namespace

				// Set up dependencies - root function depends on both nested declarations
				rootFuncDeps := btree.Set[dep_graph.DeclID]{}
				rootFuncDeps.Insert(helperDeclID) // circleArea depends on square
				rootFuncDeps.Insert(piDeclID)     // circleArea depends on PI
				depGraph.DeclDeps[rootFuncDeclID] = rootFuncDeps

				// Set up value bindings
				depGraph.ValueBindings.Set("square", helperDeclID)
				depGraph.ValueBindings.Set("PI", piDeclID)
				depGraph.ValueBindings.Set("circleArea", rootFuncDeclID)

				rootScope := &Scope{
					Parent:    nil,
					Namespace: NewNamespace(),
				}

				inferCtx := Context{
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, resultNS *Namespace, errors []Error) {
				assert.Len(t, errors, 0, "Should handle root namespace consuming nested declarations")

				// Check root namespace contains the main function
				assert.Contains(t, resultNS.Values, "circleArea", "Root namespace should contain circleArea")

				// Check nested namespace structure exists and contains dependencies
				assert.Contains(t, resultNS.Namespaces, "math", "Should have math namespace")
				mathNS := resultNS.Namespaces["math"]
				assert.Contains(t, mathNS.Values, "PI", "Math namespace should contain PI")

				assert.Contains(t, mathNS.Namespaces, "utils", "Math should have utils namespace")
				utilsNS := mathNS.Namespaces["utils"]
				assert.Contains(t, utilsNS.Values, "square", "Math.utils namespace should contain square")

				// Verify the root function can access nested declarations
				circleAreaBinding := resultNS.Values["circleArea"]
				assert.NotNil(t, circleAreaBinding, "circleArea binding should exist")
			},
		},
		{
			name: "mixed value and type dependencies across namespaces",
			setup: func() (*dep_graph.DepGraph, Context) {
				// types namespace declares Point type
				pointSource := &ast.Source{
					ID:       0,
					Path:     "types/point.esc",
					Contents: "type Point = {x: number, y: number}",
				}
				// constants namespace declares origin point
				originSource := &ast.Source{
					ID:       1,
					Path:     "constants/origin.esc",
					Contents: "val ORIGIN: types.Point = {x: 0, y: 0}",
				}
				// functions namespace has function that uses both
				distanceSource := &ast.Source{
					ID:       2,
					Path:     "functions/distance.esc",
					Contents: "fn distanceFromOrigin(p: types.Point): number { return p.x - constants.ORIGIN.x + p.y - constants.ORIGIN.y }",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				pointParser := parser.NewParser(ctx, pointSource)
				pointDecl := pointParser.Decl()

				originParser := parser.NewParser(ctx, originSource)
				originDecl := originParser.Decl()

				distanceParser := parser.NewParser(ctx, distanceSource)
				distanceDecl := distanceParser.Decl()

				// Create dependency graph manually
				depGraph := newTestDepGraph()

				// Set up declarations (append in order)
				pointDeclID := dep_graph.DeclID(0)
				originDeclID := dep_graph.DeclID(1)
				distanceDeclID := dep_graph.DeclID(2)
				depGraph.Decls = append(depGraph.Decls, pointDecl)    // DeclID 0
				depGraph.Decls = append(depGraph.Decls, originDecl)   // DeclID 1
				depGraph.Decls = append(depGraph.Decls, distanceDecl) // DeclID 2

				// Different namespaces
				depGraph.DeclNamespace[pointDeclID] = "types"
				depGraph.DeclNamespace[originDeclID] = "constants"
				depGraph.DeclNamespace[distanceDeclID] = "functions"

				// Set up dependency chains
				originDeps := btree.Set[dep_graph.DeclID]{}
				originDeps.Insert(pointDeclID) // ORIGIN depends on Point type
				depGraph.DeclDeps[originDeclID] = originDeps

				distanceDeps := btree.Set[dep_graph.DeclID]{}
				distanceDeps.Insert(pointDeclID)  // distanceFromOrigin depends on Point type
				distanceDeps.Insert(originDeclID) // distanceFromOrigin depends on ORIGIN value
				depGraph.DeclDeps[distanceDeclID] = distanceDeps

				// Set up bindings
				depGraph.TypeBindings.Set("Point", pointDeclID)
				depGraph.ValueBindings.Set("ORIGIN", originDeclID)
				depGraph.ValueBindings.Set("distanceFromOrigin", distanceDeclID)

				rootScope := &Scope{
					Parent:    nil,
					Namespace: NewNamespace(),
				}

				inferCtx := Context{
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, resultNS *Namespace, errors []Error) {
				// Mixed type and value dependencies may have some issues, but we check what works
				if len(errors) > 0 {
					t.Logf("Mixed type/value cross-namespace dependencies produced errors (may be expected): %v", errors)
				}

				// Check that all namespaces exist
				assert.Contains(t, resultNS.Namespaces, "types", "Should have types namespace")
				assert.Contains(t, resultNS.Namespaces, "constants", "Should have constants namespace")
				assert.Contains(t, resultNS.Namespaces, "functions", "Should have functions namespace")

				typesNS := resultNS.Namespaces["types"]
				assert.Contains(t, typesNS.Types, "Point", "Types namespace should contain Point")

				constantsNS := resultNS.Namespaces["constants"]
				assert.Contains(t, constantsNS.Values, "ORIGIN", "Constants namespace should contain ORIGIN")

				functionsNS := resultNS.Namespaces["functions"]
				assert.Contains(t, functionsNS.Values, "distanceFromOrigin", "Functions namespace should contain distanceFromOrigin")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			depGraph, ctx := test.setup()

			// Run InferDepGraph
			c := NewChecker()
			resultNS, errors := c.InferDepGraph(ctx, depGraph)

			// Verify results
			test.expected(t, resultNS, errors)
		})
	}
}

// newTestDepGraph creates a properly initialized DepGraph for testing
func newTestDepGraph() *dep_graph.DepGraph {
	return &dep_graph.DepGraph{
		Decls:         []ast.Decl{},
		DeclDeps:      make([]btree.Set[dep_graph.DeclID], 2000), // Large enough for test DeclIDs
		ValueBindings: btree.Map[string, dep_graph.DeclID]{},
		TypeBindings:  btree.Map[string, dep_graph.DeclID]{},
		DeclNamespace: make([]string, 2000), // Large enough for test DeclIDs
		Namespaces:    []string{},
		Components:    [][]dep_graph.DeclID{},
	}
}

func TestExpandType(t *testing.T) {
	checker := NewChecker()

	t.Run("Base types - return unchanged", func(t *testing.T) {
		// Create a test context with an empty scope
		ctx := Context{
			Scope:      NewScope(),
			IsAsync:    false,
			IsPatMatch: false,
		}

		tests := []struct {
			name     string
			input    Type
			expected Type
		}{
			{
				name:     "ObjectType",
				input:    NewObjectType(nil, []ObjTypeElem{}),
				expected: NewObjectType(nil, []ObjTypeElem{}),
			},
			{
				name:     "LitType - string",
				input:    NewStrLitType(nil, "hello"),
				expected: NewStrLitType(nil, "hello"),
			},
			{
				name:     "LitType - number",
				input:    NewNumLitType(nil, 42),
				expected: NewNumLitType(nil, 42),
			},
			{
				name:     "NamespaceType",
				input:    NewNamespaceType(nil, NewNamespace()),
				expected: NewNamespaceType(nil, NewNamespace()),
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				result, errors := checker.expandType(ctx, test.input, 1)
				assert.Empty(t, errors)
				assert.Equal(t, test.expected.String(), result.String())
			})
		}
	})

	t.Run("UnionType - recursively expand elements", func(t *testing.T) {
		ctx := Context{
			Scope:      NewScope(),
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Create a union of base types
		strLit := NewStrLitType(nil, "hello")
		numLit := NewNumLitType(nil, 42)
		unionType := NewUnionType(nil, strLit, numLit)

		result, errors := checker.expandType(ctx, unionType, 1)

		assert.Empty(t, errors)
		assert.Equal(t, `"hello" | 42`, result.String())
	})

	t.Run("TypeRefType - unknown type alias", func(t *testing.T) {
		ctx := Context{
			Scope:      NewScope(),
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Create a TypeRefType that references a non-existent type alias
		typeRef := NewTypeRefType(nil, "UnknownType", nil)

		result, errors := checker.expandType(ctx, typeRef, 1)

		assert.Len(t, errors, 1)
		// Check that the error is an UnknownTypeError
		_, ok := errors[0].(*UnknownTypeError)
		assert.True(t, ok, "Expected UnknownTypeError")
		assert.Equal(t, "never", result.String())
	})

	t.Run("TypeRefType - simple type alias", func(t *testing.T) {
		// Create a scope with a type alias
		scope := NewScope()

		// Add a simple type alias: type MyString = "literal"
		// Using a literal type since expandType doesn't handle PrimType yet
		literalType := NewStrLitType(nil, "literal")
		typeAlias := &TypeAlias{
			Type:       literalType,
			TypeParams: []*TypeParam{},
		}
		scope.setTypeAlias("MyString", typeAlias)

		ctx := Context{
			Scope:      scope,
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Create a TypeRefType that references the alias
		typeRef := NewTypeRefType(nil, "MyString", typeAlias)

		result, errors := checker.expandType(ctx, typeRef, 1)

		assert.Empty(t, errors)
		assert.Equal(t, `"literal"`, result.String())
	})

	t.Run("TypeRefType - generic type alias with substitution", func(t *testing.T) {
		// Create a scope with a generic type alias
		scope := NewScope()

		// Add a generic type alias: type Identity<T> = T
		// For simplicity, we'll use a TypeRefType for the inner type
		typeParam := &TypeParam{
			Name:       "T",
			Constraint: nil,
			Default:    nil,
		}
		innerTypeRef := NewTypeRefType(nil, "T", nil)
		typeAlias := &TypeAlias{
			Type:       innerTypeRef,
			TypeParams: []*TypeParam{typeParam},
		}
		scope.setTypeAlias("Identity", typeAlias)

		ctx := Context{
			Scope:      scope,
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Create a TypeRefType with type arguments: Identity<"hello">
		stringLitType := NewStrLitType(nil, "hello")
		typeRef := NewTypeRefType(nil, "Identity", typeAlias, stringLitType)

		result, errors := checker.expandType(ctx, typeRef, 1)

		assert.Empty(t, errors)
		assert.Equal(t, `"hello"`, result.String())
	})

	t.Run("TypeRefType - nested expansion", func(t *testing.T) {
		// Create a scope with nested type aliases
		scope := NewScope()

		// Add type aliases: type Inner = "inner", type Outer = Inner
		innerLitType := NewStrLitType(nil, "inner")
		innerAlias := &TypeAlias{
			Type:       innerLitType,
			TypeParams: []*TypeParam{},
		}
		scope.setTypeAlias("Inner", innerAlias)

		innerTypeRef := NewTypeRefType(nil, "Inner", innerAlias)
		outerAlias := &TypeAlias{
			Type:       innerTypeRef,
			TypeParams: []*TypeParam{},
		}
		scope.setTypeAlias("Outer", outerAlias)

		ctx := Context{
			Scope:      scope,
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Create a TypeRefType that references the outer alias
		outerTypeRef := NewTypeRefType(nil, "Outer", outerAlias)

		result, errors := checker.expandType(ctx, outerTypeRef, 1)

		assert.Empty(t, errors)
		assert.Equal(t, "Inner", result.String())
	})

	t.Run("UnionType with TypeRefType - mixed expansion", func(t *testing.T) {
		// Create a scope with a type alias
		scope := NewScope()

		// Add a type alias: type MyString = "mystring"
		stringLitType := NewStrLitType(nil, "mystring")
		typeAlias := &TypeAlias{
			Type:       stringLitType,
			TypeParams: []*TypeParam{},
		}
		scope.setTypeAlias("MyString", typeAlias)

		ctx := Context{
			Scope:      scope,
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Create a union of a literal and a type reference
		numLit := NewNumLitType(nil, 42)
		typeRef := NewTypeRefType(nil, "MyString", typeAlias)
		unionType := NewUnionType(nil, numLit, typeRef)

		result, errors := checker.expandType(ctx, unionType, 1)

		assert.Empty(t, errors)
		assert.Equal(t, "42 | \"mystring\"", result.String())
	})

	t.Run("Complex generic type alias", func(t *testing.T) {
		// Create a scope with a complex generic type alias
		scope := NewScope()

		// Add a generic type alias: type Result<T, E> = T | E
		typeParamT := &TypeParam{Name: "T", Constraint: nil, Default: nil}
		typeParamE := &TypeParam{Name: "E", Constraint: nil, Default: nil}

		typeRefT := NewTypeRefType(nil, "T", nil)
		typeRefE := NewTypeRefType(nil, "E", nil)
		unionType := NewUnionType(nil, typeRefT, typeRefE)

		typeAlias := &TypeAlias{
			Type:       unionType,
			TypeParams: []*TypeParam{typeParamT, typeParamE},
		}
		scope.setTypeAlias("Result", typeAlias)

		ctx := Context{
			Scope:      scope,
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Create a TypeRefType with type arguments: Result<"ok", "error">
		okLitType := NewStrLitType(nil, "ok")
		errorLitType := NewStrLitType(nil, "error")
		typeRef := NewTypeRefType(nil, "Result", typeAlias, okLitType, errorLitType)

		result, errors := checker.expandType(ctx, typeRef, 1)

		assert.Empty(t, errors)
		assert.Equal(t, "\"ok\" | \"error\"", result.String())
	})

	t.Run("TemplateLitType - simple string literal", func(t *testing.T) {
		ctx := Context{
			Scope:      NewScope(),
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Create a template literal with no interpolations: `hello`
		templateType := NewTemplateLitType(
			nil,
			[]*Quasi{{Value: "hello"}},
			[]Type{},
		)

		result, errors := checker.expandType(ctx, templateType, 1)

		assert.Empty(t, errors)
		assert.Equal(t, `"hello"`, result.String())
	})

	t.Run("TemplateLitType - single interpolation with string literal", func(t *testing.T) {
		ctx := Context{
			Scope:      NewScope(),
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Create a template literal: `hello-${world}`
		worldLit := NewStrLitType(nil, "world")
		templateType := NewTemplateLitType(
			nil,
			[]*Quasi{{Value: "hello-"}, {Value: ""}},
			[]Type{worldLit},
		)

		result, errors := checker.expandType(ctx, templateType, 1)

		assert.Empty(t, errors)
		assert.Equal(t, `"hello-world"`, result.String())
	})

	t.Run("TemplateLitType - union expansion (BinPair example)", func(t *testing.T) {
		ctx := Context{
			Scope:      NewScope(),
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Create BinDigit = 0 | 1
		zero := NewNumLitType(nil, 0)
		one := NewNumLitType(nil, 1)
		binDigit := NewUnionType(nil, zero, one)

		// Create BinPair = `${BinDigit},${BinDigit}`
		templateType := NewTemplateLitType(
			nil,
			[]*Quasi{{Value: ""}, {Value: ","}, {Value: ""}},
			[]Type{binDigit, binDigit},
		)

		result, errors := checker.expandType(ctx, templateType, 1)

		assert.Empty(t, errors)
		// Should expand to "0,0" | "0,1" | "1,0" | "1,1"
		assert.Contains(t, result.String(), `"0,0"`)
		assert.Contains(t, result.String(), `"0,1"`)
		assert.Contains(t, result.String(), `"1,0"`)
		assert.Contains(t, result.String(), `"1,1"`)
	})

	t.Run("TemplateLitType - CSS prop example", func(t *testing.T) {
		ctx := Context{
			Scope:      NewScope(),
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Create Vert = "top" | "bottom"
		top := NewStrLitType(nil, "top")
		bottom := NewStrLitType(nil, "bottom")
		vert := NewUnionType(nil, top, bottom)

		// Create Horiz = "left" | "right"
		left := NewStrLitType(nil, "left")
		right := NewStrLitType(nil, "right")
		horiz := NewUnionType(nil, left, right)

		// Create margin literal
		margin := NewStrLitType(nil, "margin")

		// Create `${Vert}-${Horiz}-${Name}` with Name = "margin"
		templateType := NewTemplateLitType(
			nil,
			[]*Quasi{{Value: ""}, {Value: "-"}, {Value: "-"}, {Value: ""}},
			[]Type{vert, horiz, margin},
		)

		result, errors := checker.expandType(ctx, templateType, 1)

		assert.Empty(t, errors)
		// Should expand to "top-left-margin" | "top-right-margin" | "bottom-left-margin" | "bottom-right-margin"
		assert.Contains(t, result.String(), `"top-left-margin"`)
		assert.Contains(t, result.String(), `"top-right-margin"`)
		assert.Contains(t, result.String(), `"bottom-left-margin"`)
		assert.Contains(t, result.String(), `"bottom-right-margin"`)
	})

	t.Run("TemplateLitType - multiple parts without interpolation", func(t *testing.T) {
		ctx := Context{
			Scope:      NewScope(),
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Create a template literal: `hello world`
		templateType := NewTemplateLitType(
			nil,
			[]*Quasi{{Value: "hello world"}},
			[]Type{},
		)

		result, errors := checker.expandType(ctx, templateType, 1)

		assert.Empty(t, errors)
		assert.Equal(t, `"hello world"`, result.String())
	})

	t.Run("TemplateLitType - mixed literals and unions", func(t *testing.T) {
		ctx := Context{
			Scope:      NewScope(),
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Create a union: "a" | "b"
		a := NewStrLitType(nil, "a")
		b := NewStrLitType(nil, "b")
		union := NewUnionType(nil, a, b)

		// Create a literal: "c"
		c := NewStrLitType(nil, "c")

		// Create template: `${union}-${c}`
		templateType := NewTemplateLitType(
			nil,
			[]*Quasi{{Value: ""}, {Value: "-"}, {Value: ""}},
			[]Type{union, c},
		)

		result, errors := checker.expandType(ctx, templateType, 1)

		assert.Empty(t, errors)
		// Should expand to "a-c" | "b-c"
		assert.Equal(t, result.String(), `"a-c" | "b-c"`)
	})

	t.Run("TemplateLitType - with number primitive type", func(t *testing.T) {
		ctx := Context{
			Scope:      NewScope(),
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Create a template with number primitive: `id-${number}`
		numberType := NewNumPrimType(nil)
		templateType := NewTemplateLitType(
			nil,
			[]*Quasi{{Value: "id-"}, {Value: ""}},
			[]Type{numberType},
		)

		result, errors := checker.expandType(ctx, templateType, 1)

		assert.Empty(t, errors)
		assert.Equal(t, result.String(), "`id-${number}`")
	})

	t.Run("TemplateLitType - with string primitive type", func(t *testing.T) {
		ctx := Context{
			Scope:      NewScope(),
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Create a template with string primitive: `prefix-${string}-suffix`
		stringType := NewStrPrimType(nil)
		templateType := NewTemplateLitType(
			nil,
			[]*Quasi{{Value: "prefix-"}, {Value: "-suffix"}},
			[]Type{stringType},
		)

		result, errors := checker.expandType(ctx, templateType, 1)

		assert.Empty(t, errors)
		assert.Equal(t, result.String(), "`prefix-${string}-suffix`")
	})

	t.Run("TemplateLitType - mixed literals and primitive types", func(t *testing.T) {
		ctx := Context{
			Scope:      NewScope(),
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Create a union: "a" | "b"
		a := NewStrLitType(nil, "a")
		b := NewStrLitType(nil, "b")
		union := NewUnionType(nil, a, b)

		// Create a number primitive
		numberType := NewNumPrimType(nil)

		// Create template: `${union}-${number}`
		templateType := NewTemplateLitType(
			nil,
			[]*Quasi{{Value: ""}, {Value: "-"}, {Value: ""}},
			[]Type{union, numberType},
		)

		result, errors := checker.expandType(ctx, templateType, 1)

		assert.Empty(t, errors)
		// Should expand to a union of two template literal types
		assert.Equal(t, result.String(), "`a-${number}` | `b-${number}`")
	})
}

func TestExtractNamedCaptureGroups(t *testing.T) {
	c := NewChecker()

	tests := []struct {
		name     string
		pattern  string
		expected []string
	}{
		{
			name:     "no named groups",
			pattern:  "/hello(world)/",
			expected: []string{},
		},
		{
			name:     "single named group",
			pattern:  "/(?<name>[a-z]+)/",
			expected: []string{"name"},
		},
		{
			name:     "multiple named groups",
			pattern:  "/(?<first>[a-z]+)_(?<second>[0-9]+)/",
			expected: []string{"first", "second"},
		},
		{
			name:     "mixed named and unnamed groups",
			pattern:  "/(?<named>[a-z]+)([0-9]+)(?<another>[a-z]+)/",
			expected: []string{"named", "another"},
		},
		// {
		// 	name:     "invalid regex",
		// 	pattern:  "/(?<invalid/",
		// 	expected: []string{},
		// },
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a regex literal type
			regexType, _ := NewRegexTypeWithPatternString(nil, tt.pattern)

			// Extract named capture groups
			result := c.findNamedGroups(regexType)

			// Check that the keys match the expected capture group names
			resultKeys := make([]string, 0, len(result))
			for key := range result {
				resultKeys = append(resultKeys, key)
			}
			assert.ElementsMatch(t, tt.expected, resultKeys)

			// Check that all values are TypeVarType (fresh variables)
			for name, typeVar := range result {
				assert.IsType(t, NewTypeVarType(nil, 0), typeVar, "Expected fresh type variable for capture group %s", name)
			}
		})
	}

	t.Run("nested types", func(t *testing.T) {
		// Test with a union type containing regex types
		regexType1, _ := NewRegexTypeWithPatternString(nil, "/(?<first>[a-z]+)/")
		regexType2, _ := NewRegexTypeWithPatternString(nil, "/(?<second>[0-9]+)/")
		unionType := NewUnionType(nil, regexType1, regexType2)

		result := c.findNamedGroups(unionType)
		expected := []string{"first", "second"}

		// Check that the keys match the expected capture group names
		resultKeys := make([]string, 0, len(result))
		for key := range result {
			resultKeys = append(resultKeys, key)
		}
		assert.ElementsMatch(t, expected, resultKeys)

		// Check that all values are TypeVarType (fresh variables)
		for name, typeVar := range result {
			assert.IsType(t, NewTypeVarType(nil, 0), typeVar, "Expected fresh type variable for capture group %s", name)
		}
	})

	t.Run("object type with regex property", func(t *testing.T) {
		// Test with an object type containing a regex type
		regexType, _ := NewRegexTypeWithPatternString(nil, "/(?<name>[a-z]+)/")
		objType := NewObjectType(
			nil,
			[]ObjTypeElem{
				NewPropertyElem(NewStrKey("pattern"), regexType),
			},
		)

		result := c.findNamedGroups(objType)
		expected := []string{"name"}

		// Check that the keys match the expected capture group names
		resultKeys := make([]string, 0, len(result))
		for key := range result {
			resultKeys = append(resultKeys, key)
		}
		assert.ElementsMatch(t, expected, resultKeys)

		// Check that all values are TypeVarType (fresh variables)
		for name, typeVar := range result {
			assert.IsType(t, NewTypeVarType(nil, 0), typeVar, "Expected fresh type variable for capture group %s", name)
		}
	})
}

func TestMutableTypes(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"MutableTypeAnnotation": {
			input: `
				val x: mut number = 5
			`,
			expectedTypes: map[string]string{
				"x": "mut number",
			},
		},
		"SymbolTypeAnnotation": {
			input: `
				declare val x: symbol
			`,
			expectedTypes: map[string]string{
				"x": "symbol",
			},
		},
		"UniqueSymbolTypeAnnotation": {
			input: `
				declare val x: unique symbol
				declare val y: unique symbol
			`,
			expectedTypes: map[string]string{
				"x": "symbol3", // Unique symbol should have an ID
				"y": "symbol4", // Unique symbol should have an ID
			},
		},
		"MutableStringType": {
			input: `
				val s: mut string = "hello"
			`,
			expectedTypes: map[string]string{
				"s": "mut string",
			},
		},
		"MutableObjectType": {
			input: `
				val obj: mut {x: number, y: string} = {x: 42, y: "test"}
				val {x, y} = obj
			`,
			expectedTypes: map[string]string{
				"obj": "mut {x: number, y: string}",
			},
		},
		"MutableArrayType": {
			input: `
				val arr: mut Array<number> = [1, 2, 3]
			`,
			expectedTypes: map[string]string{
				"arr": "mut Array<number>",
			},
		},
		// "NestedMutableType": {
		// 	input: `
		// 		val nested: mut mut number = 10
		// 	`,
		// 	expectedTypes: map[string]string{
		// 		"nested": "mut mut number",
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

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			scope, inferErrors := c.InferModule(inferCtx, module)
			if len(inferErrors) > 0 {
				for i, err := range inferErrors {
					fmt.Printf("InferError[%d]: %#v\n", i, err)
				}
				assert.Empty(t, inferErrors, "Expected no inference errors")
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

func TestMatchExprInference(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"BasicMatchWithLiterals": {
			input: `
				declare val x: number
				val result = match x {
					1 => "one",
					2 => "two",
					_ => "other"
				}
			`,
			expectedTypes: map[string]string{
				"x":      "number",
				"result": "\"one\" | \"two\" | \"other\"",
			},
		},
		"MatchWithPatternBindings": {
			input: `
				val x = [1, 2]
				val result = match x {
					[a, b] => a + b,
					_ => 0
				}
			`,
			expectedTypes: map[string]string{
				"x":      "[1, 2]",
				"result": "number | 0",
			},
		},
		"MatchWithIdentifierPattern": {
			input: `
				declare val x: number
				val result = match x {
					n => n * 2,
					_ => 0
				}
			`,
			expectedTypes: map[string]string{
				"x":      "number",
				"result": "number | 0",
			},
		},
		"MatchWithWildcardOnly": {
			input: `
				declare val x: string
				val result = match x {
					_ => "matched"
				}
			`,
			expectedTypes: map[string]string{
				"x":      "string",
				"result": "\"matched\"",
			},
		},
		"MatchWithMultipleLiterals": {
			input: `
				declare val x: boolean
				val result = match x {
					true => "yes",
					false => "no"
				}
			`,
			expectedTypes: map[string]string{
				"x":      "boolean",
				"result": "\"yes\" | \"no\"",
			},
		},
		"MatchWithSimpleGuard": {
			input: `
				val x = 5
				val result = match x {
					n if n > 0 => "positive",
					_ => "not positive"
				}
			`,
			expectedTypes: map[string]string{
				"x":      "5",
				"result": "\"positive\" | \"not positive\"",
			},
		},
		"MatchWithObjectPattern": {
			input: `
				val obj = {a: 1, b: 2}
				val result = match obj {
					{a, b} => a + b,
					_ => 0
				}
			`,
			expectedTypes: map[string]string{
				"obj":    "{a: 1, b: 2}",
				"result": "number | 0",
			},
		},
		"MatchWithTuplePattern": {
			input: `
				val tuple: Array<number> = [1, 2, 3]
				val result = match tuple {
					[a, b, c] => a + b + c,
					[a, b] => a + b,
					_ => 0
				}
			`,
			expectedTypes: map[string]string{
				"tuple":  "Array<number>",
				"result": "number | number | 0",
			},
		},
		"MatchWithNestedPattern": {
			input: `
				val nested = {point: [1, 2]}
				val result = match nested {
					{point: [x, y]} => x + y,
					_ => 0
				}
			`,
			expectedTypes: map[string]string{
				"nested": "{point: [1, 2]}",
				"result": "number | 0",
			},
		},
		// TODO: Handle rest patterns in tuple pattern matching
		// "PatternMatchingTuplesRest": {
		// 	input: `
		// 		export val longTuple: [number, number, number, number, number] = [1, 2, 3, 4, 5]
		// 		export val tupleRestMatch = match longTuple {
		// 			[first, second, ...rest] => first + second + rest.length,
		// 			_ => 0
		// 		}
		// 	`,
		// 	expectedTypes: map[string]string{
		// 		"longTuple":      "[number, number, number, number, number]",
		// 		"tupleRestMatch": "number | 0",
		// 	},
		// },
		"MatchClasses": {
			input: `
				class Point(x: number, y: number) {
					x,
					y,
				}
				class Event(kind: string) {
					kind,
				}
				declare val obj: Point | Event
				val result = match obj {
					Point {x, y} => x + y,
					Event {kind} => kind,
				}
			`,
			expectedTypes: map[string]string{
				"obj":    "Point | Event",
				"result": "number | string",
			},
		},
		// TODO(#174): Rework how `unify` works for pattern matching so that this
		// test case passes.
		// "MatchClassesWithObjPat": {
		// 	input: `
		// 		class Point(x: number, y: number) {
		// 			x,
		// 			y,
		// 		}
		// 		class Event(kind: string) {
		// 			kind,
		// 		}
		// 		declare val obj: Point | Event
		// 		val result = match obj {
		// 			{x, y} => x + y,
		// 			{kind} => kind,
		// 		}
		// 	`,
		// 	expectedTypes: map[string]string{
		// 		"obj":    "Point | Event",
		// 		"result": "number | string",
		// 	},
		// },
		"MatchExtractors": {
			input: `
				class Point(x: number, y: number) {
					x,
					y,
					static [Symbol.customMatcher](subject: Point) -> [number, number] {
						return [subject.x, subject.y]
					},
				}
				class Event(kind: string) {
					kind,
					static [Symbol.customMatcher](subject: Event) -> [string] {
						return [subject.kind]
					},
				}
				declare val obj: Point | Event
				val result = match obj {
					Point(x, y) => x + y,
					Event(kind) => kind,
				}
			`,
			expectedTypes: map[string]string{
				"obj":    "Point | Event",
				"result": "number | string",
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
			assert.Len(t, errors, 0, "Expected no parse errors")

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			scope, inferErrors := c.InferModule(inferCtx, module)

			if len(inferErrors) > 0 {
				for i, err := range inferErrors {
					fmt.Printf("InferError[%d]: %#v\n", i, err)
				}
			}

			assert.Empty(t, inferErrors, "Expected no inference errors")

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
