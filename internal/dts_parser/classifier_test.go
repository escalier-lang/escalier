package dts_parser

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
)

// ============================================================================
// File Classification Tests
// ============================================================================

func TestClassifyDTSFile_GlobalsOnly(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"simple global declarations",
			`declare var x: string;
			declare function foo(): void;
			interface Bar { y: number }`,
		},
		{
			"ambient type declarations",
			`declare type Foo = string;
			declare interface Bar { x: number }`,
		},
		{
			"global namespace",
			`declare namespace MyLib {
				function doSomething(): void;
				const VERSION: string;
			}`,
		},
		{
			"multiple global interfaces",
			`interface Array<T> { length: number }
			interface String { length: number }
			interface Number { toFixed(digits: number): string }`,
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
				t.Errorf("Unexpected parse errors: %v", errors)
				return
			}

			classification := ClassifyDTSFile(module)

			snaps.MatchSnapshot(t, map[string]interface{}{
				"hasTopLevelExports": classification.HasTopLevelExports,
				"globalDeclsCount":   len(classification.GlobalDecls),
				"packageDeclsCount":  len(classification.PackageDecls),
				"namedModulesCount":  len(classification.NamedModules),
			})
		})
	}
}

func TestClassifyDTSFile_TopLevelExports(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"export interface",
			`export interface Foo { x: number }`,
		},
		{
			"export type alias",
			`export type MyString = string;`,
		},
		{
			"export function",
			`export declare function foo(): void;`,
		},
		{
			"export variable",
			`export declare const VERSION: string;`,
		},
		{
			"export class",
			`export declare class MyClass { constructor() }`,
		},
		{
			"multiple exports",
			`export interface Foo { }
			export type Bar = Foo;
			export declare function baz(): Bar;`,
		},
		{
			"named exports",
			`declare interface Foo { }
			declare const bar: string;
			export { Foo, bar }`,
		},
		{
			"re-export from module",
			`export { something } from "other-module";`,
		},
		{
			"export all",
			`export * from "other-module";`,
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
				t.Errorf("Unexpected parse errors: %v", errors)
				return
			}

			classification := ClassifyDTSFile(module)

			snaps.MatchSnapshot(t, map[string]interface{}{
				"hasTopLevelExports": classification.HasTopLevelExports,
				"globalDeclsCount":   len(classification.GlobalDecls),
				"packageDeclsCount":  len(classification.PackageDecls),
				"namedModulesCount":  len(classification.NamedModules),
			})
		})
	}
}

func TestClassifyDTSFile_NamedModules(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"single named module",
			`declare module "lodash" {
				export function map<T, U>(arr: T[], fn: (item: T) => U): U[];
				export function filter<T>(arr: T[], fn: (item: T) => boolean): T[];
			}`,
		},
		{
			"multiple named modules",
			`declare module "lodash" {
				export function map<T, U>(arr: T[], fn: (item: T) => U): U[];
			}
			declare module "lodash/fp" {
				export function map<T, U>(fn: (item: T) => U): (arr: T[]) => U[];
			}`,
		},
		{
			"named module with scoped package",
			`declare module "@types/node" {
				export const version: string;
			}`,
		},
		{
			"named module alongside globals",
			`interface GlobalInterface { x: number }
			declare module "my-package" {
				export interface PackageInterface { y: string }
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
				t.Errorf("Unexpected parse errors: %v", errors)
				return
			}

			classification := ClassifyDTSFile(module)

			// Build a map of module names to declaration counts
			namedModules := make(map[string]int)
			for _, nm := range classification.NamedModules {
				namedModules[nm.ModuleName] = len(nm.Decls)
			}

			snaps.MatchSnapshot(t, map[string]interface{}{
				"hasTopLevelExports": classification.HasTopLevelExports,
				"globalDeclsCount":   len(classification.GlobalDecls),
				"packageDeclsCount":  len(classification.PackageDecls),
				"namedModules":       namedModules,
			})
		})
	}
}

func TestClassifyDTSFile_GlobalAugmentation(t *testing.T) {
	// NOTE: The dts_parser doesn't currently support `declare global { ... }` syntax.
	// These tests are skipped until that feature is implemented.
	// When implemented, the extractGlobalAugmentation function will handle these cases.
	t.Skip("dts_parser doesn't support 'declare global' syntax yet")

	tests := []struct {
		name  string
		input string
	}{
		{
			"global augmentation in module file",
			`export interface MyInterface { }
			declare global {
				interface Window { myProp: string }
			}`,
		},
		{
			"global augmentation with multiple declarations",
			`export type MyType = string;
			declare global {
				interface Window { prop1: string }
				interface Document { prop2: number }
				var globalVar: boolean;
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
				t.Errorf("Unexpected parse errors: %v", errors)
				return
			}

			classification := ClassifyDTSFile(module)

			snaps.MatchSnapshot(t, map[string]interface{}{
				"hasTopLevelExports": classification.HasTopLevelExports,
				"globalDeclsCount":   len(classification.GlobalDecls),
				"packageDeclsCount":  len(classification.PackageDecls),
				"namedModulesCount":  len(classification.NamedModules),
			})
		})
	}
}

func TestClassifyDTSFile_ExportEquals(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"export equals namespace",
			`declare namespace Foo {
				export const bar: number;
				export function baz(): string;
			}
			export = Foo;`,
		},
		{
			"export equals with types",
			`declare namespace MyLib {
				export interface Options { timeout: number }
				export function configure(opts: Options): void;
				export const VERSION: string;
			}
			export = MyLib;`,
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
				t.Errorf("Unexpected parse errors: %v", errors)
				return
			}

			classification := ClassifyDTSFile(module)

			snaps.MatchSnapshot(t, map[string]interface{}{
				"hasTopLevelExports": classification.HasTopLevelExports,
				"globalDeclsCount":   len(classification.GlobalDecls),
				"packageDeclsCount":  len(classification.PackageDecls),
				"namedModulesCount":  len(classification.NamedModules),
			})
		})
	}
}

func TestClassifyDTSFile_MixedFile(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"globals and named modules",
			`interface GlobalType { x: number }
			declare var globalVar: string;
			declare module "my-pkg" {
				export interface PkgType { y: string }
			}
			declare module "other-pkg" {
				export function fn(): void;
			}`,
		},
		{
			"exports and named modules",
			`export interface ExportedType { }
			declare module "sub-module" {
				export function subFn(): void;
			}`,
		},
		{
			"complex mixed file",
			`// Global interface
			interface BaseType { id: string }

			// Named module
			declare module "package-a" {
				export interface TypeA extends BaseType { a: number }
			}

			// Another named module
			declare module "package-b" {
				export interface TypeB extends BaseType { b: string }
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
				t.Errorf("Unexpected parse errors: %v", errors)
				return
			}

			classification := ClassifyDTSFile(module)

			// Build a map of module names to declaration counts
			namedModules := make(map[string]int)
			for _, nm := range classification.NamedModules {
				namedModules[nm.ModuleName] = len(nm.Decls)
			}

			snaps.MatchSnapshot(t, map[string]interface{}{
				"hasTopLevelExports": classification.HasTopLevelExports,
				"globalDeclsCount":   len(classification.GlobalDecls),
				"packageDeclsCount":  len(classification.PackageDecls),
				"namedModules":       namedModules,
			})
		})
	}
}

// ============================================================================
// Helper Function Tests
// ============================================================================

func TestIsTopLevelExport(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"export interface", "export interface Foo { }", true},
		{"export type", "export type Foo = string", true},
		{"export function", "export declare function foo(): void", true},
		{"export const", "export declare const x: number", true},
		{"export class", "export declare class Foo { }", true},
		{"named export", "export { foo }", true},
		{"export all", `export * from "module"`, true},
		{"export assignment", "export = foo", true},
		{"declare interface (no export)", "declare interface Foo { }", false},
		{"declare function (no export)", "declare function foo(): void", false},
		{"declare var (no export)", "declare var x: string", false},
		{"declare namespace (no export)", "declare namespace Foo { }", false},
		{"plain interface", "interface Foo { }", false},
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
				t.Errorf("Unexpected parse errors: %v", errors)
				return
			}

			if len(module.Statements) == 0 {
				t.Error("Expected at least one statement")
				return
			}

			result := isTopLevelExport(module.Statements[0])
			if result != tt.expected {
				t.Errorf("isTopLevelExport() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestExtractNamedModule(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectModule bool
		moduleName   string
	}{
		{
			"named module declaration",
			`declare module "lodash" { export function map(): void; }`,
			true,
			"lodash",
		},
		{
			"scoped package module",
			`declare module "@types/node" { }`,
			true,
			"@types/node",
		},
		{
			"subpath module",
			`declare module "lodash/fp" { }`,
			true,
			"lodash/fp",
		},
		{
			"regular namespace (not a module)",
			`declare namespace Foo { }`,
			false,
			"",
		},
		{
			"interface (not a module)",
			`interface Foo { }`,
			false,
			"",
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
				t.Errorf("Unexpected parse errors: %v", errors)
				return
			}

			if len(module.Statements) == 0 {
				t.Error("Expected at least one statement")
				return
			}

			result := extractNamedModule(module.Statements[0])
			if tt.expectModule {
				if result == nil {
					t.Error("Expected named module but got nil")
					return
				}
				if result.ModuleName != tt.moduleName {
					t.Errorf("ModuleName = %q, expected %q", result.ModuleName, tt.moduleName)
				}
			} else {
				if result != nil {
					t.Errorf("Expected nil but got module %q", result.ModuleName)
				}
			}
		})
	}
}
