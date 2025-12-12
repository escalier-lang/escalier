package dts_parser

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
)

// ============================================================================
// Constructor Tests
// ============================================================================

func TestConstructorDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"simple constructor",
			"declare class Point { constructor(x: number, y: number) }",
		},
		{
			"constructor with no parameters",
			"declare class Empty { constructor() }",
		},
		{
			"constructor with optional parameters",
			"declare class Config { constructor(host: string, port?: number) }",
		},
		{
			"constructor with rest parameters",
			"declare class Variadic { constructor(...args: any[]) }",
		},
		{
			"public constructor",
			"declare class PublicCtor { public constructor(x: number) }",
		},
		{
			"protected constructor",
			"declare class ProtectedCtor { protected constructor() }",
		},
		{
			"private constructor",
			"declare class PrivateCtor { private constructor() }",
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
// Property Tests
// ============================================================================

func TestPropertyDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"simple property",
			"declare class Person { name: string }",
		},
		{
			"optional property",
			"declare class Config { port?: number }",
		},
		{
			"readonly property",
			"declare class Immutable { readonly id: number }",
		},
		{
			"static property",
			"declare class Utils { static version: string }",
		},
		{
			"public property",
			"declare class Public { public name: string }",
		},
		{
			"private property",
			"declare class Private { private secret: string }",
		},
		{
			"protected property",
			"declare class Protected { protected value: number }",
		},
		{
			"static readonly property",
			"declare class Constants { static readonly PI: number }",
		},
		{
			"public readonly property",
			"declare class Readonly { public readonly id: string }",
		},
		{
			"property with no type annotation",
			"declare class Untyped { prop }",
		},
		{
			"property with string literal name",
			`declare class StringKey { "my-prop": string }`,
		},
		{
			"property with number literal name",
			"declare class NumberKey { 42: string }",
		},
		{
			"property with computed name",
			"declare class Computed { [Symbol.iterator]: any }",
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
// Method Tests
// ============================================================================

func TestMethodDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"simple method",
			"declare class Calculator { add(a: number, b: number): number }",
		},
		{
			"method with no parameters",
			"declare class Logger { log(): void }",
		},
		{
			"method with optional parameters",
			"declare class Printer { print(msg: string, times?: number): void }",
		},
		{
			"method with rest parameters",
			"declare class Sum { sum(...nums: number[]): number }",
		},
		{
			"optional method",
			"declare class Optional { method?(): void }",
		},
		{
			"static method",
			"declare class Utils { static parse(str: string): object }",
		},
		{
			"public method",
			"declare class Public { public greet(): void }",
		},
		{
			"private method",
			"declare class Private { private encrypt(): string }",
		},
		{
			"protected method",
			"declare class Protected { protected init(): void }",
		},
		{
			"abstract method",
			"abstract class Abstract { abstract render(): void }",
		},
		{
			"async method",
			"declare class Async { async fetch(): Promise<any> }",
		},
		{
			"method with type parameters",
			"declare class Generic { map<T, U>(fn: (x: T) => U): U }",
		},
		{
			"method with type parameters and constraints",
			"declare class Constrained { process<T extends object>(obj: T): T }",
		},
		{
			"static abstract method",
			"abstract class StaticAbstract { static abstract create(): any }",
		},
		{
			"public static method",
			"declare class PublicStatic { public static getInstance(): any }",
		},
		{
			"method with no return type",
			"declare class NoReturn { doSomething(x: number) }",
		},
		{
			"method with computed name",
			"declare class ComputedMethod { [Symbol.iterator](): Iterator<any> }",
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
// Getter and Setter Tests
// ============================================================================

func TestGetterAndSetterDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"simple getter",
			"declare class Person { get name(): string }",
		},
		{
			"simple setter",
			"declare class Person { set name(value: string) }",
		},
		{
			"getter and setter pair",
			"declare class Person { get name(): string; set name(value: string) }",
		},
		{
			"static getter",
			"declare class Config { static get instance(): Config }",
		},
		{
			"static setter",
			"declare class Config { static set instance(value: Config) }",
		},
		{
			"public getter",
			"declare class Public { public get value(): number }",
		},
		{
			"private getter",
			"declare class Private { private get secret(): string }",
		},
		{
			"protected getter",
			"declare class Protected { protected get data(): any }",
		},
		{
			"public setter",
			"declare class Public { public set value(n: number) }",
		},
		{
			"private setter",
			"declare class Private { private set secret(s: string) }",
		},
		{
			"protected setter",
			"declare class Protected { protected set data(d: any) }",
		},
		{
			"getter with no return type",
			"declare class NoType { get value() }",
		},
		{
			"getter with computed name",
			"declare class Computed { get [Symbol.toStringTag](): string }",
		},
		{
			"setter with computed name",
			"declare class Computed { set [Symbol.toStringTag](value: string) }",
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
// Index Signature Tests
// ============================================================================

func TestClassIndexSignatures(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"string index signature",
			"declare class Dictionary { [key: string]: any }",
		},
		{
			"number index signature",
			"declare class Array { [index: number]: string }",
		},
		{
			"readonly index signature",
			"declare class ReadonlyDict { readonly [key: string]: number }",
		},
		{
			"symbol index signature",
			"declare class SymbolMap { [sym: symbol]: object }",
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
// Combined Modifiers Tests
// ============================================================================

func TestCombinedModifiers(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"public static readonly property",
			"declare class Constants { public static readonly MAX: number }",
		},
		{
			"private static method",
			"declare class Private { private static helper(): void }",
		},
		{
			"protected readonly property",
			"declare class Protected { protected readonly id: string }",
		},
		{
			"public abstract method",
			"abstract class Abstract { public abstract render(): void }",
		},
		{
			"protected static property",
			"declare class Protected { protected static count: number }",
		},
		{
			"public async method",
			"declare class Async { public async load(): Promise<void> }",
		},
		{
			"private readonly property",
			"declare class Private { private readonly secret: string }",
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
// Complex Class Tests
// ============================================================================

func TestComplexClasses(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"class with multiple member types",
			`declare class Person {
				constructor(name: string, age: number)
				name: string
				private age: number
				greet(): void
				static create(name: string): Person
				get fullName(): string
				set fullName(value: string)
				[key: string]: any
			}`,
		},
		{
			"abstract class with abstract and concrete members",
			`abstract class Shape {
				abstract area(): number
				abstract perimeter(): number
				protected x: number
				protected y: number
				move(dx: number, dy: number): void
				static compare(a: Shape, b: Shape): boolean
			}`,
		},
		{
			"class with generic methods",
			`declare class Container {
				add<T>(item: T): void
				get<T>(index: number): T
				map<T, U>(fn: (x: T) => U): U[]
				filter<T>(predicate: (x: T) => boolean): T[]
			}`,
		},
		{
			"class with all modifier combinations",
			`declare class AllModifiers {
				public prop1: string
				private prop2: number
				protected prop3: boolean
				static prop4: any
				readonly prop5: object
				public static prop6: string
				private readonly prop7: number
				protected static readonly prop8: boolean
				public method1(): void
				private method2(): void
				protected method3(): void
				static method4(): void
				public static method5(): void
				private static method6(): void
				protected static method7(): void
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
				t.Logf("Errors: %v", errors)
			}

			snaps.MatchSnapshot(t, module)
		})
	}
}
