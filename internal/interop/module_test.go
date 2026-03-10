package interop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/gkampitakis/go-snaps/snaps"
)

// Helper function to parse a full .d.ts module
func parseModule(t *testing.T, input string) *dts_parser.Module {
	t.Helper()
	source := &ast.Source{
		Path:     "test.d.ts",
		Contents: input,
		ID:       0,
	}
	parser := dts_parser.NewDtsParser(source)
	module, errors := parser.ParseModule()

	if len(errors) > 0 {
		t.Fatalf("Parse errors: %v", errors)
	}

	return module
}

func TestConvertModule_Simple(t *testing.T) {
	input := `
declare var x: number;
declare function foo(): void;
type MyType = string;
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	// Check that we have the root namespace
	rootNS, exists := astModule.Namespaces.Get("")
	if !exists {
		t.Fatalf("Root namespace not found")
	}

	// Check that we have 3 declarations
	if len(rootNS.Decls) != 3 {
		t.Errorf("Expected 3 declarations in root namespace, got %d", len(rootNS.Decls))
	}

	// Check the types of declarations
	if _, ok := rootNS.Decls[0].(*ast.VarDecl); !ok {
		t.Errorf("First declaration should be VarDecl, got %T", rootNS.Decls[0])
	}
	if _, ok := rootNS.Decls[1].(*ast.FuncDecl); !ok {
		t.Errorf("Second declaration should be FuncDecl, got %T", rootNS.Decls[1])
	}
	if _, ok := rootNS.Decls[2].(*ast.TypeDecl); !ok {
		t.Errorf("Third declaration should be TypeDecl, got %T", rootNS.Decls[2])
	}

	snaps.MatchSnapshot(t, astModule)
}

func TestConvertModule_SingleNamespace(t *testing.T) {
	input := `
declare namespace MyNamespace {
  var x: number;
  function foo(): void;
}
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	// Check that we have the MyNamespace namespace
	ns, exists := astModule.Namespaces.Get("MyNamespace")
	if !exists {
		t.Fatalf("MyNamespace namespace not found")
	}

	// Check that we have 2 declarations
	if len(ns.Decls) != 2 {
		t.Errorf("Expected 2 declarations in MyNamespace, got %d", len(ns.Decls))
	}

	// Check the root namespace should not exist or be empty
	rootNS, rootExists := astModule.Namespaces.Get("")
	if rootExists && len(rootNS.Decls) > 0 {
		t.Errorf("Expected root namespace to be empty, got %d declarations", len(rootNS.Decls))
	}

	snaps.MatchSnapshot(t, astModule)
}

func TestConvertModule_NestedNamespaces(t *testing.T) {
	input := `
declare namespace Outer {
  namespace Inner {
    var x: number;
  }
  var y: string;
}
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	// Check that we have both namespaces
	outerNS, outerExists := astModule.Namespaces.Get("Outer")
	if !outerExists {
		t.Fatalf("Outer namespace not found")
	}

	innerNS, innerExists := astModule.Namespaces.Get("Outer.Inner")
	if !innerExists {
		t.Fatalf("Outer.Inner namespace not found")
	}

	// Check declarations in outer namespace
	if len(outerNS.Decls) != 1 {
		t.Errorf("Expected 1 declaration in Outer namespace, got %d", len(outerNS.Decls))
	}

	// Check declarations in inner namespace
	if len(innerNS.Decls) != 1 {
		t.Errorf("Expected 1 declaration in Outer.Inner namespace, got %d", len(innerNS.Decls))
	}

	snaps.MatchSnapshot(t, astModule)
}

func TestConvertModule_MixedGlobalAndNamespace(t *testing.T) {
	input := `
declare var globalVar: number;

declare namespace MyNamespace {
  var nsVar: string;
}

declare function globalFunc(): void;
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	// Check root namespace
	rootNS, rootExists := astModule.Namespaces.Get("")
	if !rootExists {
		t.Fatalf("Root namespace not found")
	}
	if len(rootNS.Decls) != 2 {
		t.Errorf("Expected 2 declarations in root namespace, got %d", len(rootNS.Decls))
	}

	// Check MyNamespace
	ns, nsExists := astModule.Namespaces.Get("MyNamespace")
	if !nsExists {
		t.Fatalf("MyNamespace not found")
	}
	if len(ns.Decls) != 1 {
		t.Errorf("Expected 1 declaration in MyNamespace, got %d", len(ns.Decls))
	}

	snaps.MatchSnapshot(t, astModule)
}

func TestConvertModule_MultipleNamespaces(t *testing.T) {
	input := `
declare namespace NS1 {
  var x: number;
}

declare namespace NS2 {
  var y: string;
}
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	// Check NS1
	ns1, ns1Exists := astModule.Namespaces.Get("NS1")
	if !ns1Exists {
		t.Fatalf("NS1 namespace not found")
	}
	if len(ns1.Decls) != 1 {
		t.Errorf("Expected 1 declaration in NS1, got %d", len(ns1.Decls))
	}

	// Check NS2
	ns2, ns2Exists := astModule.Namespaces.Get("NS2")
	if !ns2Exists {
		t.Fatalf("NS2 namespace not found")
	}
	if len(ns2.Decls) != 1 {
		t.Errorf("Expected 1 declaration in NS2, got %d", len(ns2.Decls))
	}

	snaps.MatchSnapshot(t, astModule)
}

func TestConvertModule_NamespaceMerging(t *testing.T) {
	input := `
declare namespace MyNamespace {
  var x: number;
}

declare namespace MyNamespace {
  var y: string;
}
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	// Check that MyNamespace has both declarations merged
	ns, nsExists := astModule.Namespaces.Get("MyNamespace")
	if !nsExists {
		t.Fatalf("MyNamespace not found")
	}
	if len(ns.Decls) != 2 {
		t.Errorf("Expected 2 declarations in MyNamespace (merged), got %d", len(ns.Decls))
	}

	snaps.MatchSnapshot(t, astModule)
}

func TestConvertModule_AmbientDeclarations(t *testing.T) {
	input := `
declare var x: number;
declare function foo(): void;
declare class MyClass {}
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	// Check root namespace
	rootNS, rootExists := astModule.Namespaces.Get("")
	if !rootExists {
		t.Fatalf("Root namespace not found")
	}
	if len(rootNS.Decls) != 3 {
		t.Errorf("Expected 3 declarations in root namespace, got %d", len(rootNS.Decls))
	}

	snaps.MatchSnapshot(t, astModule)
}

func TestConvertModule_AmbientNamespace(t *testing.T) {
	input := `
declare namespace MyNamespace {
  var x: number;
}
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	// Check MyNamespace
	ns, nsExists := astModule.Namespaces.Get("MyNamespace")
	if !nsExists {
		t.Fatalf("MyNamespace not found")
	}
	if len(ns.Decls) != 1 {
		t.Errorf("Expected 1 declaration in MyNamespace, got %d", len(ns.Decls))
	}

	snaps.MatchSnapshot(t, astModule)
}

func TestConvertModule_ModuleDeclaration(t *testing.T) {
	input := `
declare module "my-module" {
  var x: number;
  function foo(): void;
}
`

	dtsModule := parseModule(t, input)
	_, err := ConvertModule(dtsModule)

	// Module declarations should error since Escalier doesn't support importing packages
	if err == nil {
		t.Fatalf("Expected error for module declaration, got none")
	}

	// Check that the error message mentions module declarations
	expectedMsg := "module declarations are not supported"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Expected error message to contain %q, got: %v", expectedMsg, err)
	}
}

func TestConvertModule_DeeplyNestedNamespaces(t *testing.T) {
	input := `
declare namespace A {
  namespace B {
    namespace C {
      var x: number;
    }
  }
}
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	// Check that the deeply nested namespace is created with qualified name
	nestedNS, nestedExists := astModule.Namespaces.Get("A.B.C")
	if !nestedExists {
		t.Fatalf("A.B.C namespace not found")
	}
	if len(nestedNS.Decls) != 1 {
		t.Errorf("Expected 1 declaration in A.B.C, got %d", len(nestedNS.Decls))
	}

	snaps.MatchSnapshot(t, astModule)
}

// findRepoRoot walks up the directory tree to find the repository root
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		// Check if go.mod exists in current directory
		goModPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			return dir, nil
		}

		// Move up one directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the root without finding go.mod
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

func TestConvertModule_AmbientNamespaceAutoExport(t *testing.T) {
	input := `
declare namespace MyNamespace {
  var x: number;
  function foo(): void;
  class MyClass {}
  interface MyInterface {}
  type MyType = string;
  enum MyEnum { A, B }
}
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	// Check MyNamespace
	ns, nsExists := astModule.Namespaces.Get("MyNamespace")
	if !nsExists {
		t.Fatalf("MyNamespace not found")
	}

	// All declarations inside declare namespace should be auto-exported
	for i, decl := range ns.Decls {
		if !decl.Export() {
			t.Errorf("Declaration %d (%T) should be auto-exported in ambient namespace", i, decl)
		}
	}

	snaps.MatchSnapshot(t, astModule)
}

func TestConvertModule_AmbientNamespaceNestedAutoExport(t *testing.T) {
	input := `
declare namespace Outer {
  namespace Inner {
    var x: number;
    function foo(): void;
  }
  var y: string;
}
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	// Check Outer namespace declarations are auto-exported
	outerNS, outerExists := astModule.Namespaces.Get("Outer")
	if !outerExists {
		t.Fatalf("Outer namespace not found")
	}
	for i, decl := range outerNS.Decls {
		if !decl.Export() {
			t.Errorf("Outer declaration %d (%T) should be auto-exported", i, decl)
		}
	}

	// Check Inner namespace declarations are also auto-exported (ambient propagates)
	innerNS, innerExists := astModule.Namespaces.Get("Outer.Inner")
	if !innerExists {
		t.Fatalf("Outer.Inner namespace not found")
	}
	for i, decl := range innerNS.Decls {
		if !decl.Export() {
			t.Errorf("Inner declaration %d (%T) should be auto-exported", i, decl)
		}
	}

	snaps.MatchSnapshot(t, astModule)
}

func TestConvertModule_TopLevelDeclareNotAutoExported(t *testing.T) {
	input := `
declare var x: number;
declare function foo(): void;
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	// Check root namespace
	rootNS, rootExists := astModule.Namespaces.Get("")
	if !rootExists {
		t.Fatalf("Root namespace not found")
	}

	// Top-level declare statements should NOT be auto-exported
	// (they're not inside a namespace)
	for i, decl := range rootNS.Decls {
		if decl.Export() {
			t.Errorf("Top-level declaration %d (%T) should NOT be auto-exported", i, decl)
		}
	}

	snaps.MatchSnapshot(t, astModule)
}

func TestConvertModule_ExplicitExportInAmbientNamespace(t *testing.T) {
	input := `
declare namespace MyNamespace {
  export var x: number;
  var y: string;
}
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	// Check MyNamespace
	ns, nsExists := astModule.Namespaces.Get("MyNamespace")
	if !nsExists {
		t.Fatalf("MyNamespace not found")
	}

	// Both declarations should be exported (explicit and auto)
	for i, decl := range ns.Decls {
		if !decl.Export() {
			t.Errorf("Declaration %d (%T) should be exported in ambient namespace", i, decl)
		}
	}

	snaps.MatchSnapshot(t, astModule)
}

func TestConvertModule_ExportedNamespace(t *testing.T) {
	input := `
export namespace Property {
  export type AlignItems = string;
  export type AccentColor = string;
}
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	// Check that Property namespace exists and is exported
	ns, nsExists := astModule.Namespaces.Get("Property")
	if !nsExists {
		t.Fatalf("Property namespace not found")
	}

	if !ns.Exported {
		t.Errorf("Property namespace should be exported")
	}

	// Check that declarations inside are exported
	if len(ns.Decls) != 2 {
		t.Errorf("Expected 2 declarations in Property namespace, got %d", len(ns.Decls))
	}
	for i, decl := range ns.Decls {
		if !decl.Export() {
			t.Errorf("Declaration %d (%T) should be exported", i, decl)
		}
	}
}

func TestConvertModule_NonExportedNamespace(t *testing.T) {
	input := `
namespace Internal {
  export type Helper = string;
}
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	// Check that Internal namespace exists but is NOT exported
	ns, nsExists := astModule.Namespaces.Get("Internal")
	if !nsExists {
		t.Fatalf("Internal namespace not found")
	}

	if ns.Exported {
		t.Errorf("Internal namespace should NOT be exported")
	}
}

func TestConvertModule_NestedExportedNamespaces(t *testing.T) {
	input := `
export namespace Outer {
  export namespace Inner {
    export type Foo = string;
  }
}
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	// Check that Outer namespace is exported
	outerNS, outerExists := astModule.Namespaces.Get("Outer")
	if !outerExists {
		t.Fatalf("Outer namespace not found")
	}
	if !outerNS.Exported {
		t.Errorf("Outer namespace should be exported")
	}

	// Check that Inner namespace is also exported
	innerNS, innerExists := astModule.Namespaces.Get("Outer.Inner")
	if !innerExists {
		t.Fatalf("Outer.Inner namespace not found")
	}
	if !innerNS.Exported {
		t.Errorf("Outer.Inner namespace should be exported")
	}
}

func TestConvertModule_MixedExportedNamespaces(t *testing.T) {
	input := `
export namespace Exported {
  namespace NotExported {
    export type Foo = string;
  }
}
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	// Check that Exported namespace is exported
	exportedNS, exportedExists := astModule.Namespaces.Get("Exported")
	if !exportedExists {
		t.Fatalf("Exported namespace not found")
	}
	if !exportedNS.Exported {
		t.Errorf("Exported namespace should be exported")
	}

	// Check that NotExported namespace is NOT exported
	notExportedNS, notExportedExists := astModule.Namespaces.Get("Exported.NotExported")
	if !notExportedExists {
		t.Fatalf("Exported.NotExported namespace not found")
	}
	if notExportedNS.Exported {
		t.Errorf("Exported.NotExported namespace should NOT be exported")
	}
}

func TestConvertModule_DeclareNamespaceNotExported(t *testing.T) {
	// declare namespace is ambient but NOT exported by default
	input := `
declare namespace AmbientNS {
  var x: number;
}
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	ns, nsExists := astModule.Namespaces.Get("AmbientNS")
	if !nsExists {
		t.Fatalf("AmbientNS namespace not found")
	}

	// declare namespace without export should NOT be exported
	if ns.Exported {
		t.Errorf("AmbientNS namespace should NOT be exported (no export keyword)")
	}

	// But declarations inside should be auto-exported (due to ambient)
	for i, decl := range ns.Decls {
		if !decl.Export() {
			t.Errorf("Declaration %d should be auto-exported in ambient namespace", i)
		}
	}
}

func TestConvertModule_ExportDeclareNamespace(t *testing.T) {
	// export declare namespace is both ambient AND exported
	input := `
export declare namespace ExportedAmbientNS {
  var x: number;
}
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	ns, nsExists := astModule.Namespaces.Get("ExportedAmbientNS")
	if !nsExists {
		t.Fatalf("ExportedAmbientNS namespace not found")
	}

	// export declare namespace should be exported
	if !ns.Exported {
		t.Errorf("ExportedAmbientNS namespace should be exported")
	}

	// Declarations inside should also be exported
	for i, decl := range ns.Decls {
		if !decl.Export() {
			t.Errorf("Declaration %d should be exported in ambient namespace", i)
		}
	}
}

func TestConvertModule_NamespaceMergingPreservesExport(t *testing.T) {
	// When merging namespaces, once exported should stay exported
	input := `
export namespace MergedNS {
  type First = string;
}

namespace MergedNS {
  type Second = number;
}
`

	dtsModule := parseModule(t, input)
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	ns, nsExists := astModule.Namespaces.Get("MergedNS")
	if !nsExists {
		t.Fatalf("MergedNS namespace not found")
	}

	// Should have both declarations merged
	if len(ns.Decls) != 2 {
		t.Errorf("Expected 2 declarations in MergedNS (merged), got %d", len(ns.Decls))
	}

	// Should still be exported (first declaration exported it)
	if !ns.Exported {
		t.Errorf("MergedNS namespace should be exported (was exported in first declaration)")
	}
}

func TestConvertES2015LibFiles(t *testing.T) {
	// Find the repo root by looking for go.mod
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatal("Could not find repository root:", err)
	}

	// ES2015 sub-library files (not the bundle file lib.es2015.d.ts)
	es2015Files := []string{
		"lib.es2015.core.d.ts",
		"lib.es2015.collection.d.ts",
		"lib.es2015.iterable.d.ts",
		"lib.es2015.generator.d.ts",
		"lib.es2015.promise.d.ts",
		"lib.es2015.proxy.d.ts",
		"lib.es2015.reflect.d.ts",
		"lib.es2015.symbol.d.ts",
		"lib.es2015.symbol.wellknown.d.ts",
	}

	libDir := filepath.Join(repoRoot, "node_modules", "typescript", "lib")

	for _, filename := range es2015Files {
		t.Run(filename, func(t *testing.T) {
			libPath := filepath.Join(libDir, filename)

			// Check if the file exists
			if _, err := os.Stat(libPath); os.IsNotExist(err) {
				t.Skipf("TypeScript %s not found at: %s", filename, libPath)
			}

			// Read the file
			contents, err := os.ReadFile(libPath)
			if err != nil {
				t.Fatalf("Failed to read %s: %v", filename, err)
			}

			source := &ast.Source{
				Path:     libPath,
				Contents: string(contents),
				ID:       0,
			}

			// Parse with dts_parser
			parser := dts_parser.NewDtsParser(source)
			module, parseErrors := parser.ParseModule()

			if len(parseErrors) > 0 {
				t.Fatalf("Parse errors for %s: %v", filename, parseErrors[:min(5, len(parseErrors))])
			}

			// Convert with interop
			_, convertErr := ConvertModule(module)
			if convertErr != nil {
				t.Fatalf("Interop conversion error for %s: %v", filename, convertErr)
			}

			t.Logf("Converted %s: %d statements", filename, len(module.Statements))
		})
	}
}
