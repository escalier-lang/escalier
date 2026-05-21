package parser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		"ExportInterface": {
			input: `
				export interface Person {
					name: string,
					age: number,
				}
			`,
		},
		"DeclareModuleEmpty": {
			input: `declare module "fp-ts" { }`,
		},
		"DeclareModuleWithDecls": {
			input: `
				declare module "fp-ts" {
					declare fn pipe(x: number) -> number
					declare class Foo { }
				}
			`,
		},
		"DeclareGlobalEmpty": {
			input: `declare global { }`,
		},
		"DeclareGlobalWithClass": {
			input: `
				declare global {
					declare class Date {
						setHours(mut self, hours: number) -> number,
					}
				}
			`,
		},
		"OverrideDeclareModule": {
			input: `
				override declare module "ramda" {
					declare fn map() -> number
				}
			`,
		},
		"OverrideDeclareGlobal": {
			input: `
				override declare global {
					declare class Foo { }
				}
			`,
		},
		"OverrideDeclareClass": {
			input: `override declare class Date { setHours(mut self, hours: number) -> number, }`,
		},
		"OverrideDeclareFn": {
			input: `override declare fn pipe(x: number) -> number`,
		},
		"OverrideDeclareInterface": {
			input: `override declare interface Foo { x: number }`,
		},
		"OverrideDeclareType": {
			input: `override declare type Foo = number`,
		},
		"OverrideDeclareVal": {
			input: `override declare val x: number`,
		},
		"NamespaceEmpty": {
			input: `declare global { namespace Math { } }`,
		},
		"NamespaceWithDecls": {
			input: `
				declare global {
					namespace Math {
						declare fn abs(x: number) -> number
						declare fn sqrt(x: number) -> number
					}
				}
			`,
		},
		"NamespaceNested": {
			input: `
				declare module "lib" {
					namespace Outer {
						namespace Inner {
							declare val x: number
						}
					}
				}
			`,
		},
		"OverrideNamespace": {
			input: `
				override declare global {
					namespace Math {
						declare fn abs(x: number) -> number
					}
				}
			`,
		},
		"ExportNamespace": {
			input: `
				declare module "lib" {
					export namespace Foo {
						declare val x: number
					}
				}
			`,
		},
	}

	// Pull in the override-file fixture so a parse failure in
	// fixtures/interop_mutability/overrides/example.esc surfaces here.
	repoRoot, err := filepath.Abs("../..")
	require.NoError(t, err)
	exampleBytes, err := os.ReadFile(filepath.Join(repoRoot,
		"fixtures", "interop_mutability", "overrides", "example.esc"))
	require.NoError(t, err)
	tests["InteropMutabilityOverrideExample"] = struct{ input string }{
		input: string(exampleBytes),
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

func TestParseDeclareBlockErrors(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedError string
	}{
		"ExportDeclareModule": {
			input:         `export declare module "x" { }`,
			expectedError: "'export' is not allowed before 'declare module'",
		},
		"ExportDeclareGlobal": {
			input:         `export declare global { }`,
			expectedError: "'export' is not allowed before 'declare global'",
		},
		"ExportOverrideDeclareModule": {
			input:         `export override declare module "x" { }`,
			expectedError: "'export' is not allowed before 'declare module'",
		},
		"ExportOverrideDeclareGlobal": {
			input:         `export override declare global { }`,
			expectedError: "'export' is not allowed before 'declare global'",
		},
		"OverrideWithoutDeclareFn": {
			input:         `override fn foo() -> number`,
			expectedError: "'override' requires 'declare'",
		},
		"OverrideWithoutDeclareClass": {
			input:         `override class Foo {}`,
			expectedError: "'override' requires 'declare'",
		},
		"DeclareModuleMissingStringLiteral": {
			input:         `declare module 42 { }`,
			expectedError: "Expected string literal after 'module'",
		},
		"DeclareGlobalNamespaceMissingIdent": {
			input:         `declare global { namespace 42 { } }`,
			expectedError: "Expected identifier after 'namespace'",
		},
		"DecoratorOnEnum": {
			input:         `@js("E") enum E { A, B }`,
			expectedError: "decorators are not allowed on enum declarations",
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

			require.NotEmpty(t, errors, "expected parse errors")
			found := false
			for _, e := range errors {
				if e.Message == test.expectedError {
					found = true
					break
				}
			}
			if !found {
				msgs := make([]string, len(errors))
				for i, e := range errors {
					msgs[i] = e.Message
				}
				t.Fatalf("expected error %q, got %v", test.expectedError, msgs)
			}
		})
	}
}

func TestParseOverrideDeclareBlockPropagates(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"OverrideDeclareModulePropagates": {
			input: `
				override declare module "ramda" {
					declare fn map() -> number
				}
			`,
		},
		"OverrideDeclareGlobalPropagates": {
			input: `
				override declare global {
					declare class Foo { }
				}
			`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := NewParser(ctx, source)
			module, errors := parser.ParseScript()
			require.Empty(t, errors, "expected no parse errors")
			require.NotEmpty(t, module.Stmts)

			declStmt, ok := module.Stmts[0].(*ast.DeclStmt)
			require.True(t, ok, "expected DeclStmt")

			var inner []ast.Decl
			switch outer := declStmt.Decl.(type) {
			case *ast.DeclareModuleDecl:
				assert.True(t, outer.Override(), "outer Override() should be true")
				inner = outer.Decls
			case *ast.DeclareGlobalDecl:
				assert.True(t, outer.Override(), "outer Override() should be true")
				inner = outer.Decls
			default:
				t.Fatalf("expected DeclareModuleDecl/DeclareGlobalDecl, got %T", declStmt.Decl)
			}

			require.NotEmpty(t, inner)
			for i, d := range inner {
				assert.True(t, d.Override(), "inner decl %d Override() should be true", i)
			}
		})
	}
}

func TestParseOverridePropagatesIntoNamespace(t *testing.T) {
	t.Parallel()
	input := `
		override declare global {
			namespace Math {
				declare fn abs(x: number) -> number
			}
		}
	`
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: input}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	parser := NewParser(ctx, source)
	module, errors := parser.ParseScript()
	require.Empty(t, errors, "expected no parse errors")
	require.Len(t, module.Stmts, 1)

	declStmt, ok := module.Stmts[0].(*ast.DeclStmt)
	require.True(t, ok)
	outer, ok := declStmt.Decl.(*ast.DeclareGlobalDecl)
	require.True(t, ok)
	assert.True(t, outer.Override(), "outer DeclareGlobalDecl.Override() should be true")
	require.Len(t, outer.Decls, 1)

	ns, ok := outer.Decls[0].(*ast.NamespaceDecl)
	require.True(t, ok, "expected NamespaceDecl inside declare global")
	assert.True(t, ns.Override(), "NamespaceDecl.Override() should be true")
	require.Len(t, ns.Decls, 1)

	innerDecl := ns.Decls[0]
	assert.True(t, innerDecl.Override(), "decl inside namespace should have Override() == true")
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
				class Point {
					x: number,
					y: number,
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
		"ClassWithExtendsAndInBodyConstructor": {
			input: `
				class Dog extends Animal {
					name: string,
					constructor(mut self, name: string) {
						self.name = name
					},
					bark(self) {
						return "Woof!"
					},
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
		"GenericClassWithExtendsAndInBodyConstructor": {
			input: `
				class SpecialBox<T> extends Box<T> {
					value: T,
					constructor(mut self, value: T) {
						self.value = value
					},
				}
			`,
		},
		"ClassWithImplements": {
			input: `
				class Dog implements Animal {
					bark(self) {
						return "Woof!"
					}
				}
			`,
		},
		"ClassWithMultipleImplements": {
			input: `
				class Dog implements Animal, Runnable {
					bark(self) {
						return "Woof!"
					}
				}
			`,
		},
		"ClassWithExtendsAndImplements": {
			input: `
				class Dog extends Canine implements Animal, Runnable {
					bark(self) {
						return "Woof!"
					}
				}
			`,
		},
		"ClassImplementsQualifiedAndGeneric": {
			input: `
				class MyList<T> implements Collections.Iterable<T>, Eq.Comparable<MyList<T>> {
					len(self) -> number {
						return 0
					}
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
		"ClassWithConstructorNoParams": {
			input: `
				class Foo {
					constructor(mut self) {}
				}
			`,
		},
		"ClassWithConstructorOneParam": {
			input: `
				class Foo {
					x: number,
					constructor(mut self, x: number) {
						self.x = x
					},
				}
			`,
		},
		"ClassWithTwoConstructors": {
			input: `
				class Foo {
					x: number,
					constructor(mut self, x: number) {
						self.x = x
					},
					constructor(mut self) {
						self.x = 0
					},
				}
			`,
		},
		"ClassWithGenericConstructor": {
			input: `
				class Box<T> {
					value: T,
					constructor<U>(mut self, value: U) {
						self.value = value
					},
				}
			`,
		},
		"ClassWithConstructorThrows": {
			input: `
				class Email {
					addr: string,
					constructor(mut self, addr: string) throws ValidationError {
						self.addr = addr
					},
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

func TestClassConstructorErrors(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"StaticConstructor": {
			input: `
				class Foo {
					static constructor(mut self) {}
				}
			`,
		},
		"AsyncConstructor": {
			input: `
				class Foo {
					async constructor(mut self) {}
				}
			`,
		},
		"GetConstructor": {
			input: `
				class Foo {
					get constructor(mut self) {}
				}
			`,
		},
		"ConstructorWithReturnType": {
			input: `
				class Foo {
					constructor(mut self) -> number {}
				}
			`,
		},
		"ConstructorMissingSelf": {
			input: `
				class Foo {
					constructor() {}
				}
			`,
		},
		"ConstructorSelfNotMut": {
			input: `
				class Foo {
					constructor(self) {}
				}
			`,
		},
		"ConstructorSelfWithTypeAnnotation": {
			input: `
				class Foo {
					constructor(mut self: Self, x: number) {
						self.x = x
					}
				}
			`,
		},
		"ConstructorSelfNotFirst": {
			input: `
				class Foo {
					constructor(x: number, mut self) {
						self.x = x
					}
				}
			`,
		},
		"ConstructorMissingOpenParen": {
			input: `
				class Foo {
					constructor {}
				}
			`,
		},
		"ImplementsFollowedByOpenBrace": {
			input: `
				class Foo implements {
					bar(self) {}
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
			assert.Greater(t, len(errors), 0, "Expected parsing errors but got none")
			snaps.MatchSnapshot(t, errors)
		})
	}
}

func TestStatementRecovery(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"ErrorBetweenValidStatements": {
			input: "val a = 1\n@@@\nval b = 2",
		},
		"MultipleErrorsBetweenStatements": {
			input: "val a = 1\n@@@\nval b = 2\n###\nval c = 3",
		},
		"ErrorAtStart": {
			input: "@@@\nval a = 1",
		},
		"MissingEqualsOnNewLine": {
			input: "val x\nval y = 10",
		},
		"ClassExtendsMissingType": {
			input: "class Foo extends { bar(self) { return 1 } }",
		},
		"IncompleteFnDecl": {
			input: "export fn\nval x = 1",
		},
		"IncompleteFnDeclWithName": {
			input: "export fn foo\nval x = 1",
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

			assert.Greater(t, len(errors), 0, "Expected parsing errors but got none")
			snaps.MatchSnapshot(t, errors)
		})
	}
}
