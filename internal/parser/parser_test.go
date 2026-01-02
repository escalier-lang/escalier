package parser

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
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
		"FuncDecls": {
			input: `
				fn add(a, b) {
					return a + b
				}
				fn sub(a, b) {
					return a - b
				}
			`,
		},
		"FuncDeclsWithThrows": {
			input: `
				fn divide(a, b) -> number throws DivisionByZeroError {
					return a / b
				}
			`,
		},
		"AsyncFuncDecls": {
			input: `
				async fn fetchData(url: string) -> Promise<string> {
					val response = await fetch(url)
					return await response.text()
				}
			`,
		},
		"AsyncFuncExprs": {
			input: `
				val handler = async fn(event) {
					return await processEvent(event)
				}
				val nested = await foo(await bar(42))
			`,
		},
		"AwaitMethodCall": {
			input: `async fn getData(id) { return await api.getData(id) }`,
		},
		"ExportAsyncFuncDecl": {
			input: `
				export async fn fetchUser(id: number) -> Promise<User> {
					return await api.getUser(id)
				}
			`,
		},
		"DeclareAsyncFuncDecl": {
			input: `
				declare async fn fetch(url: string) -> Promise<Response>
			`,
		},
		"ExprStmts": {
			input: `
				foo()
				bar()
			`,
		},
		"SplitExprOnNewline": {
			input: `
				var a = x
				-y
			`,
		},
		"MultilineExprInParens": {
			input: `
				var a = (x
				-y)
			`,
		},
		"MultilineExprInBrackets": {
			input: `
				a[base
				+offset]
			`,
		},
		"SplitExprInNewScope": {
			input: `
				val funcs = [
					fn() {
						var a = x
						-y
					}
				]
			`,
		},
		"IfElse": {
			input: `
				val x = if cond {
					var a = 5
					-10
				} else {
				 	var b = 10
					-5
				}
			`,
		},
		"MemberAssignment": {
			input: `
				p.x = 5
				p.y = 10
			`,
		},
		"GenericFuncDecl": {
			input: `
				fn identity<T>(value: T) -> T {
					return value
				}
			`,
		},
		"EnumDecl": {
			input: `
				enum Maybe<T> {
					Some(value: T),
					None,
				}
			`,
		},
		"EnumDeclWithoutGeneric": {
			input: `
				enum Color {
					Red,
					Green,
					Blue,
				}
			`,
		},
		"EnumDeclWithMultipleParams": {
			input: `
				enum Color {
					RGB(r: number, g: number, b: number),
					HSL(h: number, s: number, l: number),
				}
			`,
		},
		"EnumDeclWithExtension": {
			input: `
				enum FutureColor {
					...Color,
					Oklab(l: number, a: number, b: number),
				}
			`,
		},
		"ExportEnumDecl": {
			input: `
				export enum Result<T, E> {
					Ok(value: T),
					Err(error: E),
				}
			`,
		},
		"InterfaceWithSingleExtends": {
			input: `
				interface Foo extends Bar {
					x: number
				}
			`,
		},
		"InterfaceWithMultipleExtends": {
			input: `
				interface Foo extends Bar, Baz {
					x: number
				}
			`,
		},
		"InterfaceWithQualifiedExtends": {
			input: `
				interface Foo extends Bar.Baz {
					x: number
				}
			`,
		},
		"InterfaceWithGenericExtends": {
			input: `
				interface Foo extends Bar<string> {
					x: number
				}
			`,
		},
		"InterfaceWithComplexExtends": {
			input: `
				interface Foo extends Bar.Baz<string>, Qux {
					x: number
				}
			`,
		},
		"GenericInterfaceWithExtends": {
			input: `
				interface Foo<T> extends Bar<T> {
					value: T
				}
			`,
		},
		"InterfaceWithMultipleGenericExtends": {
			input: `
				interface Foo<T, U> extends Bar<T>, Baz<U>, Qux {
					x: T,
					y: U
				}
			`,
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
			module, errors := parser.ParseScript()

			for _, stmt := range module.Stmts {
				snaps.MatchSnapshot(t, stmt)
			}
			if len(errors) > 0 {
				for i, err := range errors {
					fmt.Printf("Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, errors, 0)
		})
	}
}

func TestParseEnumErrorHandling(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"EnumMissingName": {
			input: `enum { Some, None }`,
		},
		"EnumMissingOpeningBrace": {
			input: `enum Result Some, Err }`,
		},
		"EnumMissingClosingBrace": {
			input: `enum Result { Some, Err`,
		},
		"EnumVariantMissingClosingParen": {
			input: `enum Result { Some(value: string, Err }`,
		},
		"EnumVariantMissingOpeningParen": {
			input: `enum Result { Some value: string), Err }`,
		},
		"EnumSpreadMissingIdent": {
			input: `enum Extended { ..., Other }`,
		},
		"EnumMissingCommaBeforeVariant": {
			input: `enum Color { Red Green Blue }`,
		},
		"EnumInvalidVariantName": {
			input: `enum Bad { 123, Good }`,
		},
		"EnumSpreadWithParens": {
			input: `enum Extended { ...Color(), Own }`,
		},
		"EnumVariantMissingCommaAfter": {
			input: `enum Result { Some(T) None }`,
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
			module, errors := parser.ParseScript()

			// Snapshot the parsed result (may be partial or nil)
			for _, stmt := range module.Stmts {
				snaps.MatchSnapshot(t, stmt)
			}

			// Verify that errors were reported
			assert.Greater(t, len(errors), 0, "Expected parsing errors but got none")
			snaps.MatchSnapshot(t, errors)
		})
	}
}

func TestParseTypeKeywordsAsIdentifiers(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"FunctionWithStringParam": {
			input: `fn parseFloat(string: string) -> number { return 0.0 }`,
		},
		"FunctionWithNumberParam": {
			input: `fn formatValue(number: number) -> string { return "" }`,
		},
		"FunctionWithBooleanParam": {
			input: `fn toggle(boolean: boolean) -> boolean { return boolean }`,
		},
		"FunctionWithBigintParam": {
			input: `fn convertToBigint(bigint: string) -> bigint { return 0n }`,
		},
		"FunctionWithMultipleTypeKeywordParams": {
			input: `fn convert(string: string, number: number, boolean: boolean) -> void {}`,
		},
		"DeclareFunction": {
			input: `declare fn parseFloat(string: string) -> number`,
		},
		"DeclareWithMultipleTypeKeywordParams": {
			input: `declare fn parseInt(string: string, radix: number) -> number`,
		},
		"ArrowFunctionWithTypeKeywordParam": {
			input: `val parseFloat = fn(string: string) -> number { return 0.0 }`,
		},
		"OptionalTypeKeywordParam": {
			input: `fn parse(string?: string) -> number { return 0 }`,
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
			module, errors := parser.ParseScript()

			// Verify no errors occurred
			assert.Empty(t, errors, "Expected no parsing errors")

			// Snapshot the parsed result
			for _, stmt := range module.Stmts {
				snaps.MatchSnapshot(t, stmt)
			}
		})
	}
}

func TestClassDeclarations(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"BasicClass": {
			input: `
				class Point(x: number, y: number) {
					x,
					y,
				}
			`,
		},
		"ClassWithExtends": {
			input: `
				class Dog extends Animal {
					bark(self) {
						return "Woof!"
					}
				}
			`,
		},
		"ClassWithExtendsAndConstructorParams": {
			input: `
				class Dog(name: string)  extends Animal{
					bark(self) {
						return "Woof!"
					}
				}
			`,
		},
		"GenericClassWithExtends": {
			input: `
				class Box<T> extends Container {
					getValue(self) -> T {
						return self.value
					}
				}
			`,
		},
		"ExportClassWithExtends": {
			input: `
				export class Manager extends Employee {
					manage(self) {
						return "Managing"
					}
				}
			`,
		},
		"DeclareClassWithExtends": {
			input: `
				declare class HTMLElement extends Element {
					click(self),
				}
			`,
		},
		"GenericClassWithExtendsAndParams": {
			input: `
				class SpecialBox<T>(value: T) extends Box<T> {
					isSpecial: true,
				}
			`,
		},
		"ClassExtendsQualifiedName": {
			input: `
				class CustomButton extends UI.Button {
					customMethod(self) {
						return "custom"
					}
				}
			`,
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
			module, errors := parser.ParseScript()

			for _, stmt := range module.Stmts {
				snaps.MatchSnapshot(t, stmt)
			}
			if len(errors) > 0 {
				for i, err := range errors {
					fmt.Printf("Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, errors, 0)
		})
	}
}
