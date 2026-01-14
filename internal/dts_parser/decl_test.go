package dts_parser

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
)

// ============================================================================
// Variable Declarations
// ============================================================================

func TestVariableDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"var declaration", "declare var x: string"},
		{"let declaration", "declare let y: number"},
		{"const declaration", "declare const z: boolean"},
		{"var without type", "declare var x"},
		{"complex type", "declare const config: { host: string; port: number }"},
		{"union type", "declare let value: string | number"},
		{"array type", "declare var items: string[]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			module, errors := parser.ParseModule()

			if len(errors) > 0 {
				t.Logf("Errors: %v", errors)
			}

			snaps.MatchSnapshot(t, module)
		})
	}
}

func TestAmbientVariableDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"var with semicolon", "declare var foo: string;"},
		{"let with semicolon", "declare let bar: number;"},
		{"const with semicolon", "declare const baz: boolean;"},
		{"var without type with semicolon", "declare var foo;"},
		{"multiple vars with semicolons", "declare var x: string;\ndeclare var y: number;"},
		{"var with union type and semicolon", "declare var value: string | number;"},
		{"var with array type and semicolon", "declare var items: string[];"},
		{"var with object type and semicolon", "declare var config: { name: string };"},
		{"var with generic type and semicolon", "declare var map: Map<string, number>;"},
		{"var with tuple type and semicolon", "declare var tuple: [string, number];"},
		{"const with readonly and semicolon", "declare const VERSION: string;"},
		{"var with complex type and semicolon", "declare var handler: (event: Event) => void;"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			module, errors := parser.ParseModule()

			if len(errors) > 0 {
				t.Logf("Errors: %v", errors)
			}

			snaps.MatchSnapshot(t, module)
		})
	}
}

// ============================================================================
// Function Declarations
// ============================================================================

func TestFunctionDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple function", "declare function foo(): void"},
		{"function with params", "declare function add(a: number, b: number): number"},
		{"function with type params", "declare function identity<T>(value: T): T"},
		{"function with optional param", "declare function greet(name?: string): void"},
		{"function with rest params", "declare function sum(...numbers: number[]): number"},
		{"function with complex return", "declare function getData(): { id: number; name: string }"},
		{
			"function with constraints",
			"declare function pick<T, K extends keyof T>(obj: T, key: K): T[K]",
		},
		{"simple function with semicolon", "declare function foo(): void;"},
		{"function with params and semicolon", "declare function add(a: number, b: number): number;"},
		{"function with type params and semicolon", "declare function identity<T>(value: T): T;"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			module, errors := parser.ParseModule()

			if len(errors) > 0 {
				t.Logf("Errors: %v", errors)
			}

			snaps.MatchSnapshot(t, module)
		})
	}
}

// ============================================================================
// Type Alias Declarations
// ============================================================================

func TestTypeAliasDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple type alias", "type Name = string"},
		{"union type alias", "type StringOrNumber = string | number"},
		{"intersection type", "type Combined = A & B"},
		{"generic type alias", "type Box<T> = { value: T }"},
		{"conditional type", "type NonNullable<T> = T extends null | undefined ? never : T"},
		{"mapped type", "type Readonly<T> = { readonly [K in keyof T]: T[K] }"},
		{"mapped type with optional", "type Partial<T> = { [P in keyof T]?: T[P] }"},
		{"mapped type with trailing semicolon", `type Partial<T> = {[P in keyof T]?: T[P];};`},
		{"tuple type", "type Point = [number, number]"},
		{"function type", "type Callback = (data: string) => void"},
		{"ambient type alias", "declare type Name = string"},
		{"ambient generic type alias", "declare type Box<T> = { value: T }"},
		{"ambient union type alias", "declare type StringOrNumber = string | number"},
		{"ambient conditional type", "declare type NonNullable<T> = T extends null | undefined ? never : T"},
		{"simple type alias with semicolon", "type Name = string;"},
		{"union type alias with semicolon", "type StringOrNumber = string | number;"},
		{"generic type alias with semicolon", "type Box<T> = { value: T };"},
		{"ambient type alias with semicolon", "declare type Name = string;"},
		{"ambient generic type alias with semicolon", "declare type Box<T> = { value: T };"},
		{"ConstructorParameters type", "type ConstructorParameters<T extends abstract new (...args: any) => any> = T extends abstract new (...args: infer P) => any ? P : never"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			module, errors := parser.ParseModule()

			if len(errors) > 0 {
				t.Logf("Errors: %v", errors)
			}

			snaps.MatchSnapshot(t, module)
		})
	}
}

// ============================================================================
// Interface Declarations
// ============================================================================

func TestInterfaceDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"simple interface",
			"interface Person { name: string; age: number }",
		},
		{
			"interface with methods",
			"interface Calculator { add(a: number, b: number): number; subtract(a: number, b: number): number }",
		},
		{
			"generic interface",
			"interface Box<T> { value: T; getValue(): T }",
		},
		{
			"interface with extends",
			"interface Employee extends Person { employeeId: number }",
		},
		{
			"interface with multiple extends",
			"interface Manager extends Person, Employee { department: string }",
		},
		{
			"interface with optional properties",
			"interface Config { host: string; port?: number }",
		},
		{
			"interface with readonly",
			"interface Point { readonly x: number; readonly y: number }",
		},
		{
			"interface with index signature",
			"interface Dictionary { [key: string]: any }",
		},
		{
			"interface with call signature",
			"interface Callable { (x: number): string }",
		},
		{
			"interface with construct signature",
			"interface Constructable { new (name: string): Person }",
		},
		{
			"ambient interface",
			"declare interface Person { name: string; age: number }",
		},
		{
			"ambient generic interface",
			"declare interface Box<T> { value: T }",
		},
		{
			"ambient interface with extends",
			"declare interface Employee extends Person { employeeId: number }",
		},
		{
			"interface with comma separators",
			"interface Person { name: string, age: number }",
		},
		{
			"interface with mixed separators",
			"interface Config { host: string; port: number, ssl?: boolean }",
		},
		{
			"interface with trailing comma",
			"interface Point { x: number, y: number, }",
		},
		{
			"interface with line comments",
			"interface Symbol { /** Returns string */ toString(): string; /** Returns value */ valueOf(): symbol }",
		},
		{
			"interface with block comments",
			"interface Person { /* name field */ name: string; /* age field */ age: number }",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			module, errors := parser.ParseModule()

			if len(errors) > 0 {
				t.Logf("Errors: %v", errors)
			}

			snaps.MatchSnapshot(t, module)
		})
	}
}

// ============================================================================
// Enum Declarations
// ============================================================================

func TestEnumDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple enum", "enum Color { Red, Green, Blue }"},
		{"enum with values", "enum Status { Active = 1, Inactive = 0 }"},
		{"string enum", `enum Direction { Up = "UP", Down = "DOWN" }`},
		{"const enum", "const enum Size { Small, Medium, Large }"},
		{"const enum with values", "const enum HttpStatus { OK = 200, NotFound = 404 }"},
		{"ambient enum", "declare enum Color { Red, Green, Blue }"},
		{"ambient enum with values", "declare enum Status { Active = 1, Inactive = 0 }"},
		{"ambient string enum", `declare enum Direction { Up = "UP", Down = "DOWN" }`},
		{"ambient const enum", "declare const enum Size { Small, Medium, Large }"},
		{"ambient const enum with values", "declare const enum HttpStatus { OK = 200, NotFound = 404 }"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			module, errors := parser.ParseModule()

			if len(errors) > 0 {
				t.Logf("Errors: %v", errors)
			}

			snaps.MatchSnapshot(t, module)
		})
	}
}

// ============================================================================
// Class Declarations
// ============================================================================

func TestClassDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"simple class",
			"declare class Person { name: string; age: number }",
		},
		{
			"class with constructor",
			"declare class Point { constructor(x: number, y: number) }",
		},
		{
			"class with methods",
			"declare class Calculator { add(a: number, b: number): number; multiply(a: number, b: number): number }",
		},
		{
			"generic class",
			"declare class Box<T> { value: T; getValue(): T }",
		},
		{
			"class with extends",
			"declare class Employee extends Person { employeeId: number }",
		},
		{
			"class with implements",
			"declare class MyClass implements Interface1, Interface2 { prop: string }",
		},
		{
			"class with static members",
			"declare class Utils { static version: string; static log(msg: string): void }",
		},
		{
			"class with private members",
			"declare class Secret { private key: string; private encrypt(): string }",
		},
		{
			"class with protected members",
			"declare class Base { protected id: number; protected init(): void }",
		},
		{
			"class with public members",
			"declare class Public { public name: string; public greet(): void }",
		},
		{
			"class with readonly",
			"declare class Immutable { readonly id: number }",
		},
		{
			"class with optional properties",
			"declare class Config { host: string; port?: number }",
		},
		{
			"abstract class",
			"declare abstract class Animal { abstract makeSound(): void; move(): void }",
		},
		{
			"class with getters and setters",
			"declare class Person { get name(): string; set name(value: string) }",
		},
		{
			"class with index signature",
			"declare class Dictionary { [key: string]: any }",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			module, errors := parser.ParseModule()

			if len(errors) > 0 {
				t.Logf("Errors: %v", errors)
			}

			snaps.MatchSnapshot(t, module)
		})
	}
}

// ============================================================================
// Namespace Declarations
// ============================================================================

func TestNamespaceDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"simple namespace",
			"namespace MyNamespace { export function foo(): void }",
		},
		{
			"nested namespace",
			"namespace Outer { namespace Inner { export const x: number } }",
		},
		{
			"module keyword",
			"module MyModule { export interface Person { name: string } }",
		},
		{
			"ambient module",
			`declare module "my-library" { export function doSomething(): void }`,
		},
		{
			"namespace with var declarations",
			"declare namespace Intl { var Collator: CollatorConstructor }",
		},
		{
			"namespace with multiple var declarations",
			"declare namespace Intl { var Collator: CollatorConstructor; var NumberFormat: NumberFormatConstructor }",
		},
		{
			"namespace with comments before declarations",
			"declare namespace CSS { /** [MDN Reference](https://example.com) */ var highlights: HighlightRegistry; /** Another comment */ function Hz(value: number): CSSUnitValue; }",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			module, errors := parser.ParseModule()

			if len(errors) > 0 {
				t.Logf("Errors: %v", errors)
			}

			snaps.MatchSnapshot(t, module)
		})
	}
}

// TestAmbientNamespaceVariableDeclarations tests that variable declarations
// without 'declare' keyword are allowed inside ambient namespace declarations.
// This is required to properly parse TypeScript lib.es5.d.ts which contains
// constructs like:
//
//	declare namespace Intl {
//	  var Collator: CollatorConstructor;
//	}
func TestAmbientNamespaceVariableDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"var in declare namespace",
			"declare namespace Intl { var Collator: CollatorConstructor; }",
		},
		{
			"let in declare namespace",
			"declare namespace Test { let value: string; }",
		},
		{
			"const in declare namespace",
			"declare namespace Test { const PI: number; }",
		},
		{
			"multiple vars in declare namespace",
			"declare namespace Intl { var Collator: CollatorConstructor; var NumberFormat: NumberFormatConstructor; }",
		},
		{
			"function in declare namespace",
			"declare namespace Test { function foo(): void; }",
		},
		{
			"class in declare namespace",
			"declare namespace Test { class MyClass { } }",
		},
		{
			"export var in declare namespace",
			"declare namespace Test { export var x: number; }",
		},
		{
			"export function in declare namespace",
			"declare namespace Test { export function foo(): void; }",
		},
		{
			"nested namespace with var",
			"declare namespace Outer { namespace Inner { var x: number; } }",
		},
		{
			"var in declare module",
			`declare module "my-module" { var x: number; }`,
		},
		{
			"export equals in declare module",
			`declare module 'fast-deep-equal' {
    const equal: (a: any, b: any) => boolean;
    export = equal;
}`,
		},
		{
			"export equals simple",
			`declare module "simple-module" {
    const value: string;
    export = value;
}`,
		},
		{
			"module with single quotes",
			`declare module 'my-lib' {
    export function foo(): void;
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			module, errors := parser.ParseModule()

			if len(errors) > 0 {
				t.Errorf("Unexpected errors: %v", errors)
			}

			snaps.MatchSnapshot(t, module)
		})
	}
}

// TestTopLevelVariableRequiresDeclare tests that variable declarations
// at the top level (not inside a namespace) still require the 'declare' keyword
func TestTopLevelVariableRequiresDeclare(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"top-level var without declare",
			"var x: number;",
		},
		{
			"top-level let without declare",
			"let y: string;",
		},
		{
			"top-level const without declare",
			"const z: boolean;",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			_, errors := parser.ParseModule()

			if len(errors) == 0 {
				t.Error("Expected parse errors for top-level variable without 'declare', but got none")
			}
		})
	}
}

// ============================================================================
// Multiple Declarations
// ============================================================================

func TestMultipleDeclarations(t *testing.T) {
	input := `
declare var version: string
declare function init(): void
type Config = { host: string }
interface Person { name: string }
enum Color { Red, Green }
declare class App { run(): void }
namespace Utils { export const PI: number }
`

	source := &ast.Source{
		Path:     "test.d.ts",
		Contents: input,
		ID:       0,
	}
	parser := NewDtsParser(source)
	module, errors := parser.ParseModule()

	if len(errors) > 0 {
		t.Logf("Errors: %v", errors)
	}

	snaps.MatchSnapshot(t, module)
}
