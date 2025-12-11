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
		{"tuple type", "type Point = [number, number]"},
		{"function type", "type Callback = (data: string) => void"},
		{"ambient type alias", "declare type Name = string"},
		{"ambient generic type alias", "declare type Box<T> = { value: T }"},
		{"ambient union type alias", "declare type StringOrNumber = string | number"},
		{"ambient conditional type", "declare type NonNullable<T> = T extends null | undefined ? never : T"},
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
			"abstract class Animal { abstract makeSound(): void; move(): void }",
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
