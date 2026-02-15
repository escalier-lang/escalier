package dts_parser

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
)

// ============================================================================
// File Classification Tests
// ============================================================================

func TestClassifyDTSFile_GlobalsOnly(t *testing.T) {
	tests := []struct {
		name               string
		input              string
		hasTopLevelExports bool
		globalDeclsCount   int
		packageDeclsCount  int
		namedModulesCount  int
	}{
		{
			name: "simple global declarations",
			input: `declare var x: string;
			declare function foo(): void;
			interface Bar { y: number }`,
			hasTopLevelExports: false,
			globalDeclsCount:   3,
			packageDeclsCount:  0,
			namedModulesCount:  0,
		},
		{
			name: "ambient type declarations",
			input: `declare type Foo = string;
			declare interface Bar { x: number }`,
			hasTopLevelExports: false,
			globalDeclsCount:   2,
			packageDeclsCount:  0,
			namedModulesCount:  0,
		},
		{
			name: "global namespace",
			input: `declare namespace MyLib {
				function doSomething(): void;
				const VERSION: string;
			}`,
			hasTopLevelExports: false,
			globalDeclsCount:   1,
			packageDeclsCount:  0,
			namedModulesCount:  0,
		},
		{
			name: "multiple global interfaces",
			input: `interface Array<T> { length: number }
			interface String { length: number }
			interface Number { toFixed(digits: number): string }`,
			hasTopLevelExports: false,
			globalDeclsCount:   3,
			packageDeclsCount:  0,
			namedModulesCount:  0,
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

			if classification.HasTopLevelExports != tt.hasTopLevelExports {
				t.Errorf("HasTopLevelExports = %v, expected %v", classification.HasTopLevelExports, tt.hasTopLevelExports)
			}
			if len(classification.GlobalDecls) != tt.globalDeclsCount {
				t.Errorf("GlobalDecls count = %d, expected %d", len(classification.GlobalDecls), tt.globalDeclsCount)
			}
			if len(classification.PackageDecls) != tt.packageDeclsCount {
				t.Errorf("PackageDecls count = %d, expected %d", len(classification.PackageDecls), tt.packageDeclsCount)
			}
			if len(classification.NamedModules) != tt.namedModulesCount {
				t.Errorf("NamedModules count = %d, expected %d", len(classification.NamedModules), tt.namedModulesCount)
			}
		})
	}
}

func TestClassifyDTSFile_TopLevelExports(t *testing.T) {
	tests := []struct {
		name               string
		input              string
		hasTopLevelExports bool
		globalDeclsCount   int
		packageDeclsCount  int
		namedModulesCount  int
	}{
		{
			name:               "export interface",
			input:              `export interface Foo { x: number }`,
			hasTopLevelExports: true,
			globalDeclsCount:   0,
			packageDeclsCount:  1,
			namedModulesCount:  0,
		},
		{
			name:               "export type alias",
			input:              `export type MyString = string;`,
			hasTopLevelExports: true,
			globalDeclsCount:   0,
			packageDeclsCount:  1,
			namedModulesCount:  0,
		},
		{
			name:               "export function",
			input:              `export declare function foo(): void;`,
			hasTopLevelExports: true,
			globalDeclsCount:   0,
			packageDeclsCount:  1,
			namedModulesCount:  0,
		},
		{
			name:               "export variable",
			input:              `export declare const VERSION: string;`,
			hasTopLevelExports: true,
			globalDeclsCount:   0,
			packageDeclsCount:  1,
			namedModulesCount:  0,
		},
		{
			name:               "export class",
			input:              `export declare class MyClass { constructor() }`,
			hasTopLevelExports: true,
			globalDeclsCount:   0,
			packageDeclsCount:  1,
			namedModulesCount:  0,
		},
		{
			name: "multiple exports",
			input: `export interface Foo { }
			export type Bar = Foo;
			export declare function baz(): Bar;`,
			hasTopLevelExports: true,
			globalDeclsCount:   0,
			packageDeclsCount:  3,
			namedModulesCount:  0,
		},
		{
			name: "named exports",
			input: `declare interface Foo { }
			declare const bar: string;
			export { Foo, bar }`,
			hasTopLevelExports: true,
			globalDeclsCount:   0,
			packageDeclsCount:  1,
			namedModulesCount:  0,
		},
		{
			name:               "re-export from module",
			input:              `export { something } from "other-module";`,
			hasTopLevelExports: true,
			globalDeclsCount:   0,
			packageDeclsCount:  1,
			namedModulesCount:  0,
		},
		{
			name:               "export all",
			input:              `export * from "other-module";`,
			hasTopLevelExports: true,
			globalDeclsCount:   0,
			packageDeclsCount:  1,
			namedModulesCount:  0,
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

			if classification.HasTopLevelExports != tt.hasTopLevelExports {
				t.Errorf("HasTopLevelExports = %v, expected %v", classification.HasTopLevelExports, tt.hasTopLevelExports)
			}
			if len(classification.GlobalDecls) != tt.globalDeclsCount {
				t.Errorf("GlobalDecls count = %d, expected %d", len(classification.GlobalDecls), tt.globalDeclsCount)
			}
			if len(classification.PackageDecls) != tt.packageDeclsCount {
				t.Errorf("PackageDecls count = %d, expected %d", len(classification.PackageDecls), tt.packageDeclsCount)
			}
			if len(classification.NamedModules) != tt.namedModulesCount {
				t.Errorf("NamedModules count = %d, expected %d", len(classification.NamedModules), tt.namedModulesCount)
			}
		})
	}
}

func TestClassifyDTSFile_NamedModules(t *testing.T) {
	tests := []struct {
		name               string
		input              string
		hasTopLevelExports bool
		globalDeclsCount   int
		packageDeclsCount  int
		namedModules       map[string]int // module name -> declaration count
	}{
		{
			name: "single named module",
			input: `declare module "lodash" {
				export function map<T, U>(arr: T[], fn: (item: T) => U): U[];
				export function filter<T>(arr: T[], fn: (item: T) => boolean): T[];
			}`,
			hasTopLevelExports: false,
			globalDeclsCount:   0,
			packageDeclsCount:  0,
			namedModules:       map[string]int{"lodash": 2},
		},
		{
			name: "multiple named modules",
			input: `declare module "lodash" {
				export function map<T, U>(arr: T[], fn: (item: T) => U): U[];
			}
			declare module "lodash/fp" {
				export function map<T, U>(fn: (item: T) => U): (arr: T[]) => U[];
			}`,
			hasTopLevelExports: false,
			globalDeclsCount:   0,
			packageDeclsCount:  0,
			namedModules:       map[string]int{"lodash": 1, "lodash/fp": 1},
		},
		{
			name: "named module with scoped package",
			input: `declare module "@types/node" {
				export const version: string;
			}`,
			hasTopLevelExports: false,
			globalDeclsCount:   0,
			packageDeclsCount:  0,
			namedModules:       map[string]int{"@types/node": 1},
		},
		{
			name: "named module alongside globals",
			input: `interface GlobalInterface { x: number }
			declare module "my-package" {
				export interface PackageInterface { y: string }
			}`,
			hasTopLevelExports: false,
			globalDeclsCount:   1,
			packageDeclsCount:  0,
			namedModules:       map[string]int{"my-package": 1},
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

			if classification.HasTopLevelExports != tt.hasTopLevelExports {
				t.Errorf("HasTopLevelExports = %v, expected %v", classification.HasTopLevelExports, tt.hasTopLevelExports)
			}
			if len(classification.GlobalDecls) != tt.globalDeclsCount {
				t.Errorf("GlobalDecls count = %d, expected %d", len(classification.GlobalDecls), tt.globalDeclsCount)
			}
			if len(classification.PackageDecls) != tt.packageDeclsCount {
				t.Errorf("PackageDecls count = %d, expected %d", len(classification.PackageDecls), tt.packageDeclsCount)
			}
			if len(classification.NamedModules) != len(tt.namedModules) {
				t.Errorf("NamedModules count = %d, expected %d", len(classification.NamedModules), len(tt.namedModules))
			}
			for _, nm := range classification.NamedModules {
				expectedCount, ok := tt.namedModules[nm.ModuleName]
				if !ok {
					t.Errorf("Unexpected named module: %q", nm.ModuleName)
					continue
				}
				if len(nm.Decls) != expectedCount {
					t.Errorf("Named module %q has %d decls, expected %d", nm.ModuleName, len(nm.Decls), expectedCount)
				}
			}
		})
	}
}

func TestClassifyDTSFile_GlobalAugmentation(t *testing.T) {
	tests := []struct {
		name               string
		input              string
		hasTopLevelExports bool
		globalDeclsCount   int
		packageDeclsCount  int
		namedModulesCount  int
	}{
		{
			name: "global augmentation in module file",
			input: `export interface MyInterface { }
			declare global {
				interface Window { myProp: string }
			}`,
			hasTopLevelExports: true,
			globalDeclsCount:   1,
			packageDeclsCount:  1,
			namedModulesCount:  0,
		},
		{
			name: "global augmentation with multiple declarations",
			input: `export type MyType = string;
			declare global {
				interface Window { prop1: string }
				interface Document { prop2: number }
				var globalVar: boolean;
			}`,
			hasTopLevelExports: true,
			globalDeclsCount:   3,
			packageDeclsCount:  1,
			namedModulesCount:  0,
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

			if classification.HasTopLevelExports != tt.hasTopLevelExports {
				t.Errorf("HasTopLevelExports = %v, expected %v", classification.HasTopLevelExports, tt.hasTopLevelExports)
			}
			if len(classification.GlobalDecls) != tt.globalDeclsCount {
				t.Errorf("GlobalDecls count = %d, expected %d", len(classification.GlobalDecls), tt.globalDeclsCount)
			}
			if len(classification.PackageDecls) != tt.packageDeclsCount {
				t.Errorf("PackageDecls count = %d, expected %d", len(classification.PackageDecls), tt.packageDeclsCount)
			}
			if len(classification.NamedModules) != tt.namedModulesCount {
				t.Errorf("NamedModules count = %d, expected %d", len(classification.NamedModules), tt.namedModulesCount)
			}
		})
	}
}

func TestClassifyDTSFile_ExportEquals(t *testing.T) {
	tests := []struct {
		name               string
		input              string
		hasTopLevelExports bool
		globalDeclsCount   int
		packageDeclsCount  int
		namedModulesCount  int
	}{
		{
			name: "export equals namespace",
			input: `declare namespace Foo {
				export const bar: number;
				export function baz(): string;
			}
			export = Foo;`,
			hasTopLevelExports: true,
			globalDeclsCount:   0,
			packageDeclsCount:  2,
			namedModulesCount:  0,
		},
		{
			name: "export equals with types",
			input: `declare namespace MyLib {
				export interface Options { timeout: number }
				export function configure(opts: Options): void;
				export const VERSION: string;
			}
			export = MyLib;`,
			hasTopLevelExports: true,
			globalDeclsCount:   0,
			packageDeclsCount:  3,
			namedModulesCount:  0,
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

			if classification.HasTopLevelExports != tt.hasTopLevelExports {
				t.Errorf("HasTopLevelExports = %v, expected %v", classification.HasTopLevelExports, tt.hasTopLevelExports)
			}
			if len(classification.GlobalDecls) != tt.globalDeclsCount {
				t.Errorf("GlobalDecls count = %d, expected %d", len(classification.GlobalDecls), tt.globalDeclsCount)
			}
			if len(classification.PackageDecls) != tt.packageDeclsCount {
				t.Errorf("PackageDecls count = %d, expected %d", len(classification.PackageDecls), tt.packageDeclsCount)
			}
			if len(classification.NamedModules) != tt.namedModulesCount {
				t.Errorf("NamedModules count = %d, expected %d", len(classification.NamedModules), tt.namedModulesCount)
			}
		})
	}
}

func TestClassifyDTSFile_MixedFile(t *testing.T) {
	tests := []struct {
		name               string
		input              string
		hasTopLevelExports bool
		globalDeclsCount   int
		packageDeclsCount  int
		namedModules       map[string]int // module name -> declaration count
	}{
		{
			name: "globals and named modules",
			input: `interface GlobalType { x: number }
			declare var globalVar: string;
			declare module "my-pkg" {
				export interface PkgType { y: string }
			}
			declare module "other-pkg" {
				export function fn(): void;
			}`,
			hasTopLevelExports: false,
			globalDeclsCount:   2,
			packageDeclsCount:  0,
			namedModules:       map[string]int{"my-pkg": 1, "other-pkg": 1},
		},
		{
			name: "exports and named modules",
			input: `export interface ExportedType { }
			declare module "sub-module" {
				export function subFn(): void;
			}`,
			hasTopLevelExports: true,
			globalDeclsCount:   0,
			packageDeclsCount:  1,
			namedModules:       map[string]int{"sub-module": 1},
		},
		{
			name: "complex mixed file",
			input: `// Global interface
			interface BaseType { id: string }

			// Named module
			declare module "package-a" {
				export interface TypeA extends BaseType { a: number }
			}

			// Another named module
			declare module "package-b" {
				export interface TypeB extends BaseType { b: string }
			}`,
			hasTopLevelExports: false,
			globalDeclsCount:   1,
			packageDeclsCount:  0,
			namedModules:       map[string]int{"package-a": 1, "package-b": 1},
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

			if classification.HasTopLevelExports != tt.hasTopLevelExports {
				t.Errorf("HasTopLevelExports = %v, expected %v", classification.HasTopLevelExports, tt.hasTopLevelExports)
			}
			if len(classification.GlobalDecls) != tt.globalDeclsCount {
				t.Errorf("GlobalDecls count = %d, expected %d", len(classification.GlobalDecls), tt.globalDeclsCount)
			}
			if len(classification.PackageDecls) != tt.packageDeclsCount {
				t.Errorf("PackageDecls count = %d, expected %d", len(classification.PackageDecls), tt.packageDeclsCount)
			}
			if len(classification.NamedModules) != len(tt.namedModules) {
				t.Errorf("NamedModules count = %d, expected %d", len(classification.NamedModules), len(tt.namedModules))
			}
			for _, nm := range classification.NamedModules {
				expectedCount, ok := tt.namedModules[nm.ModuleName]
				if !ok {
					t.Errorf("Unexpected named module: %q", nm.ModuleName)
					continue
				}
				if len(nm.Decls) != expectedCount {
					t.Errorf("Named module %q has %d decls, expected %d", nm.ModuleName, len(nm.Decls), expectedCount)
				}
			}
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
