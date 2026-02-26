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
