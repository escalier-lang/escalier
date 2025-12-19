package interop

import (
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
	astModule, err := ConvertModule(dtsModule)

	if err != nil {
		t.Fatalf("ConvertModule returned error: %v", err)
	}

	// Check that the module is treated as a namespace
	moduleName := "my-module"
	moduleNS, moduleExists := astModule.Namespaces.Get(moduleName)
	if !moduleExists {
		t.Fatalf("Module namespace %s not found", moduleName)
	}
	if len(moduleNS.Decls) != 2 {
		t.Errorf("Expected 2 declarations in module namespace, got %d", len(moduleNS.Decls))
	}

	snaps.MatchSnapshot(t, astModule)
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
