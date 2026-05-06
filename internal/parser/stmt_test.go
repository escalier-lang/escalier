package parser

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
)

func TestParseStmtNoErrors(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"ClassWithProperties": {
			input: `class Foo {
			    private readonly id: number,
				readonly message: string,
				constructor(mut self, id: number = 1, message: string = "hello") {
					self.id = id
					self.message = message
				},
			}`,
		},
		"ClassWithGetter": {
			input: `class Foo {
				get value() -> number { return 42 },
			}`,
		},
		"ClassWithSetter": {
			input: `class Foo {
			    private _value: number,
				set value(self, x: number) { self._value = x },
			}`,
		},
		"ClassWithGetterAndSetter": {
			input: `class Foo {
			    private _value: number,
				get value(self) -> number { return self._value },
				set value(self, x: number) { self._value = x },
			}`,
		},
		"ClassWithStaticGetter": {
			input: `class Foo {
				static get answer() -> number { return 42 },
			}`,
		},
		"ClassWithPrivateGetterSetter": {
			input: `class Foo {
			    private _secret: string,
				private get secret(self) -> string { return "shh" },
				private set secret(self, x: string) { self._secret = x },
			}`,
		},
		"ClassWithPrivateField": {
			input: `class Secret {
				private secret: string,
				constructor(mut self, secret: string = "shh") {
					self.secret = secret
				},
				reveal(self) { return this.secret },
			}`,
		},
		"ClassWithPrivateMethod": {
			input: `class Secret {
				private reveal(self) { return "hidden" },
				show(self) { return this.reveal() },
			}`,
		},
		"ClassWithPrivateFieldAndMethod": {
			input: `class Secret {
				private secret: string,
				constructor(mut self, secret: string = "shh") {
					self.secret = secret
				},
				private reveal(self) { return this.secret },
				show(self) { return this.reveal() },
			}`,
		},
		"ClassWithMixedPrivateAndPublic": {
			input: `class Mixed {
				private foo: number,
				bar: number,
				constructor(mut self, foo?: number = 1, bar?: number = 2) {
					self.foo = foo
					self.bar = bar
				},
				private baz(self) { return this.foo },
				qux(self) { return this.bar },
			}`,
		},
		"ClassWithAsyncMethod": {
			input: `class Asyncer {
				async fetchData(self, url: string) -> Promise<string> {
					// fetch logic
				},
			}`,
		},
		"ClassWithAsyncStaticMethod": {
			input: `class Util {
				static async doAsyncThing() -> Promise<number> {
					// static async logic
				},
			}`,
		},
		"ClassWithMixedAsyncAndSyncMethods": {
			input: `class Mixed {
				foo(self) { return 1 },
				async bar(self) -> Promise<number> { return 2 },
				static async baz() -> Promise<void> {},
			}`,
		},
		"GenericClass": {
			input: `class Box<T> {
				value: T,
				get foo(self) -> T {
					return self.value
				},
				set foo(mut self, value: T) {
					self.value = value
				},
			}`,
		},
		"GenericClassWithConstrainedType": {
			input: `class Pair<T: number, U: string> {
				first: T,
				second: U,
			}`,
		},
		"GenericClassWithDefaultType": {
			input: "class Response<T: any = string> { data: T }",
		},
		"ClassWithGenericMethod": {
			input: `class Mapper<T> {
				value: T,
				map<U>(self, callback: fn (value: T) -> U) -> Mapper<U> {
					return Mapper(callback(self.value))
				},
			}`,
		},
		"ClassWithGenericStaticMethod": {
			input: "class Util { static identity<T>(x: T) -> T { return x } }",
		},
		"ClassDeclBasic": {
			input: "class Foo {}",
		},
		"ClassDeclWithFields": {
			input: "class Bar { x: number, y: string, }",
		},
		"ClassDeclWithFieldsAndMethods": {
			input: `class Baz {
				x: number,
				y: string,
				constructor(mut self, x: number, y: string = "hi") {
					self.x = x
					self.y = y
				},
				foo(self, a: number) -> undefined {},
			}`,
		},
		"ClassWithStaticMethod": {
			input: "class Util { static log(msg: string) { console.log(msg) } }",
		},
		"ClassWithStaticGenericMethod": {
			input: "class Util { static identity<T>(x: T) -> T { return x } }",
		},
		"ClassWithStaticAndInstanceMethods": {
			input: `class Math {
				static add(a: number, b: number) -> number {
					return a + b
				},
				sub(self, a: number, b: number) -> number {
					return a - b
				}
			}`,
		},
		"VarDecl": {
			input: "var x = 5",
		},
		"ValDeclWithUnicodeIdent": {
			input: "val π = Math.PI",
		},
		"ValDeclWithTypeAnn": {
			input: "val x: number = 5",
		},
		"ExportValDecl": {
			input: "export val x = 5",
		},
		"DeclareValDecl": {
			input: "declare val x",
		},
		"ExportDeclareValDecl": {
			input: "export declare val x",
		},
		"FunctionDecl": {
			input: "fn foo(a, b) { a + b }",
		},
		"FunctionDeclWithReturn": {
			input: "fn foo(a, b) { return a + b }",
		},
		"FunctionDeclWithMultipleStmts": {
			input: `fn foo() {
				val a = 5
				val b = 10
				return a + b
			}`,
		},
		"ExportFunctionDecl": {
			input: "export fn foo(a, b) { a + b }",
		},
		"DeclareFunctionDecl": {
			input: "declare fn foo(a, b)",
		},
		"ExportDeclareFunctionDecl": {
			input: "export declare fn foo(a, b)",
		},
		"UnicodeVarDecl": {
			input: "val längd = 5",
		},
		"UnicodeFunctionDecl": {
			input: "fn до́бра(a, b) { a + b }",
		},
		"TypeDecl": {
			input: "type MyType = { x: number, y: string }",
		},
		"TypeDeclWithTypeParams": {
			input: "type MyType<T> = Array<T>",
		},
		"TypeDeclWithMultipleTypeParams": {
			input: "type MyType<T, U: string> = { first: T, second: U }",
		},
		"TypeDeclWithConstrainedTypeParams": {
			input: "type MyType<T: number, U: string = string> = T | U",
		},
		"TypeDeclWithComments": {
			input: `type MyType = Foo
				// Comment
				| Bar
				// Comment
				| Baz`,
		},
		"TypeDeclWithLeadingPipe": {
			input: `type MyType =
				| Foo
				| Bar
				| Baz`,
		},
		"TypeDeclWithIntersectionType": {
			input: `type MyType = string & number | boolean`,
		},
		"InterfaceDecl": {
			input: "interface Point { x: number, y: number }",
		},
		"InterfaceDeclWithTypeParams": {
			input: "interface Box<T> { value: T }",
		},
		"InterfaceDeclWithMultipleTypeParams": {
			input: "interface Pair<T, U> { first: T, second: U }",
		},
		"InterfaceDeclExport": {
			input: "export interface Person { name: string, age: number }",
		},
		"InterfaceDeclDeclare": {
			input: "declare interface Global { version: string }",
		},
		"InterfaceDeclMethodWithSelf": {
			input: "interface Greeter { greet(self) -> string }",
		},
		"InterfaceDeclMethodWithMutSelf": {
			input: "interface Counter { increment(mut self) -> number }",
		},
		"InterfaceDeclMethodWithSelfAndParams": {
			input: "interface Adder { add(self, x: number, y: number) -> number }",
		},
		"InterfaceDeclMethodNoSelf": {
			input: "interface Free { make() -> number }",
		},
		"InterfaceDeclGetterWithSelf": {
			input: "interface Sized { get size(self) -> number }",
		},
		"InterfaceDeclGetterWithMutSelf": {
			input: "interface Cached { get value(mut self) -> number }",
		},
		"InterfaceDeclSetterWithMutSelf": {
			input: "interface HasValue { set value(mut self, x: number) -> undefined }",
		},
		"ImportNamedSingle": {
			input: `import { foo } from "module"`,
		},
		"ImportNamedMultiple": {
			input: `import { foo, bar, baz } from "module"`,
		},
		"ImportNamedWithAlias": {
			input: `import { foo as bar } from "module"`,
		},
		"ImportNamedMixed": {
			input: `import { foo, bar as baz, qux } from "module"`,
		},
		"ImportNamespace": {
			input: `import * as ns from "module"`,
		},
		"ForInBasic": {
			input: `for item in items { console.log(item) }`,
		},
		"ForInDestructuring": {
			input: `for [key, value] in map { }`,
		},
		"ForAwaitIn": {
			input: `for await item in asyncItems { }`,
		},
		"GeneratorFuncDecl": {
			input: "fn count() {\n\tyield 1\n\tyield 2\n\tyield 3\n}",
		},
		"AsyncGeneratorFuncDecl": {
			input: `async fn fetch() { yield await x }`,
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
			parser := NewParser(ctx, source)
			stmt := parser.stmt()

			snaps.MatchSnapshot(t, stmt)
			if len(parser.errors) > 0 {
				fmt.Printf("Error[0]: %#v", parser.errors[0])
			}
			assert.Len(t, parser.errors, 0)
		})
	}
}

func TestParseStmtErrorHandling(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"VarDeclMissingIdent": {
			input: "var = 5",
		},
		"VarDeclMissingEquals": {
			input: "var x 5",
		},
		"VarDeclMissingInitializer": {
			input: "var x = ",
		},
		"FunctionDeclMissingIdent": {
			input: `fn () {return 5}`,
		},
		"FunctionDeclMissingBoyd": {
			input: "fn foo(a, b)",
		},
		"FunctionDeclWithIncompleteStmts": {
			input: `fn foo() {
				val a =
				val b = 5
				return a +
			}`,
		},
		"ParamsMissingOpeningParen": {
			input: "fn foo a, b) { a + b }",
		},
		"ParamsMissingClosingParen": {
			input: "fn foo (a, b { a + b }",
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
			parser := NewParser(ctx, source)
			stmt := parser.stmt()

			snaps.MatchSnapshot(t, stmt)
			assert.Greater(t, len(parser.errors), 0)
			snaps.MatchSnapshot(t, parser.errors)
		})
	}
}

// TestRetiredClassSyntax verifies that the primary-constructor syntax
// (`class Foo(...) { ... }`) and the `data` modifier are no longer
// accepted by the parser. Phase 4 retired both forms; the parser no
// longer recognizes the primary-ctor parenthesized head at all and
// instead falls into its generic "expected `{` to start class body"
// error path.
func TestRetiredClassSyntax(t *testing.T) {
	tests := map[string]struct {
		input         string
		wantSubstring string
	}{
		"PrimaryConstructorSyntax": {
			input:         `class Point(x: number, y: number) { x, y, }`,
			wantSubstring: "Expected '{' to start class body",
		},
		"DataClassKeyword": {
			input:         `data class Config { host: string, }`,
			wantSubstring: "Unexpected token",
		},
		"FieldWithoutTypeAnnotation": {
			input:         `class Foo { x, }`,
			wantSubstring: "Class fields require a type annotation",
		},
		"StaticOptionalField": {
			input:         `class Foo { static x?: number, }`,
			wantSubstring: "Static fields cannot be optional",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := NewParser(ctx, source)
			_, errors := parser.ParseScript()

			found := false
			for _, err := range errors {
				if strings.Contains(err.Message, test.wantSubstring) {
					found = true
					break
				}
			}
			if !found {
				for i, err := range errors {
					fmt.Printf("Error[%d]: %#v\n", i, err)
				}
				t.Errorf("expected an error matching %q, got %d error(s)", test.wantSubstring, len(errors))
			}
		})
	}
}
