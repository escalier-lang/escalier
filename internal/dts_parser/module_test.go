package dts_parser

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
)

// ============================================================================
// Import Declarations
// ============================================================================

func TestImportDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"default import", `import foo from "module"`},
		{"named import single", `import { foo } from "module"`},
		{"named import multiple", `import { foo, bar, baz } from "module"`},
		{"named import with alias", `import { foo as bar } from "module"`},
		{"named import mixed", `import { foo, bar as baz } from "module"`},
		{"namespace import", `import * as foo from "module"`},
		{"default and named", `import foo, { bar } from "module"`},
		{"default and namespace", `import foo, * as bar from "module"`},
		{"side effect import", `import "module"`},
		{"type-only import", `import type { foo } from "module"`},
		{"type-only namespace", `import type * as foo from "module"`},
		{"trailing comma", `import { foo, bar, } from "module"`},
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
// Export Declarations
// ============================================================================

func TestExportDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"export variable", "export declare var x: string"},
		{"export function", "export declare function foo(): void"},
		{"export type alias", "export type Foo = string"},
		{"export interface", "export interface Bar { x: number }"},
		{"export class", "export declare class Baz {}"},
		{"export enum", "export enum Color { Red, Green, Blue }"},
		{"export namespace", "export namespace Utils { }"},
		{"named export single", "export { foo }"},
		{"named export multiple", "export { foo, bar, baz }"},
		{"named export with alias", "export { foo as bar }"},
		{"named export mixed", "export { foo, bar as baz }"},
		{"export all", `export * from "module"`},
		{"export all as namespace", `export * as foo from "module"`},
		{"export from module", `export { foo } from "module"`},
		{"export from with alias", `export { foo as bar } from "module"`},
		{"export default interface", "export default interface Foo { }"},
		{"export default type", "export default type Foo = string"},
		{"export assignment", "export = foo"},
		{"export as namespace", "export as namespace MyLib"},
		{"type-only export", "export type { Foo }"},
		{"trailing comma", "export { foo, bar, }"},
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
// Complex Import/Export Scenarios
// ============================================================================

func TestComplexImportExportScenarios(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"multiple imports",
			`import { foo } from "a"
			import { bar } from "b"
			import * as c from "c"`,
		},
		{
			"imports and exports",
			`import { Foo } from "lib"
			export interface Bar extends Foo { }`,
		},
		{
			"re-export pattern",
			`export { foo, bar } from "module"
			export * from "other"`,
		},
		{
			"type-only imports and exports",
			`import type { A, B } from "types"
			export type { A, B }`,
		},
		{
			"mixed exports",
			`export interface Foo { }
			export { bar } from "lib"
			export default class Baz { }`,
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
// Error Cases
// ============================================================================

func TestModuleErrorCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"import without from", "import { foo }"},
		{"import missing braces", "import foo, bar from 'module'"},
		{"export without name", "export { }"},
		{"malformed namespace import", "import * foo from 'module'"},
		{"export = without identifier", "export = "},
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

			if len(errors) == 0 {
				t.Error("Expected errors but got none")
			}

			snaps.MatchSnapshot(t, map[string]interface{}{
				"module": module,
				"errors": errors,
			})
		})
	}
}
