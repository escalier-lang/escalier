package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/interop"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// the key is the method name
type Overrides map[string]bool

func UpdateMethodMutability(namespace *type_system.Namespace) {
	for name := range namespace.Types {
		if strings.HasSuffix(name, "Constructor") && name != "ArrayConstructor" {
			instName := strings.TrimSuffix(name, "Constructor")
			instTypeAlias := namespace.Types[instName]
			overrides := mutabilityOverrides[instName]

			if it, ok := type_system.Prune(instTypeAlias.Type).(*type_system.ObjectType); ok {
				for _, elem := range it.Elems {
					if me, ok := elem.(*type_system.MethodElem); ok {
						mutSelf := true
						if me.Name.Kind == type_system.StrObjTypeKeyKind {
							value, exists := overrides[me.Name.Str]
							if exists {
								mutSelf = value
							}
						}
						me.MutSelf = &mutSelf
					}
				}
			} else {
				panic("Instance type is not an ObjectType: " + instTypeAlias.Type.String())
			}
		}
	}
}

func UpdateArrayMutability(namespace *type_system.Namespace) {
	arrayTypeAlias := namespace.Types["Array"]
	readonlyArrayTypeAlias := namespace.Types["ReadonlyArray"]
	arrayType := type_system.Prune(arrayTypeAlias.Type).(*type_system.ObjectType)
	readonlyArrayType := type_system.Prune(readonlyArrayTypeAlias.Type).(*type_system.ObjectType)

	readonlyArrayElems := make(set.Set[type_system.ObjTypeKey])
	for _, v := range readonlyArrayType.Elems {
		if me, ok := v.(*type_system.MethodElem); ok {
			key := type_system.ObjTypeKey{
				Kind: type_system.StrObjTypeKeyKind,
				Str:  me.Name.Str,
				Num:  0,
				Sym:  0,
			}
			readonlyArrayElems.Add(key)

			// All methods on ReadonlyArray are non-mutating
			mutSelf := false
			me.MutSelf = &mutSelf
		}
	}

	readonlyArrayType.Elems = arrayType.Elems
	for _, elem := range arrayType.Elems {
		switch me := elem.(type) {
		case *type_system.MethodElem:
			mutSelf := true
			key := type_system.ObjTypeKey{
				Kind: type_system.StrObjTypeKeyKind,
				Str:  me.Name.Str,
				Num:  0,
				Sym:  0,
			}
			if readonlyArrayElems.Contains(key) {
				mutSelf = false
			}
			me.MutSelf = &mutSelf
		}
	}
}

// the key is the interface name
var mutabilityOverrides = map[string]Overrides{
	"String": {
		"at":                false,
		"chatAt":            false,
		"charCodeAt":        false,
		"codePointAt":       false,
		"concat":            false,
		"endsWith":          false,
		"includes":          false,
		"indexOf":           false,
		"lastIndexOf":       false,
		"localeCompare":     false,
		"match":             false,
		"matchAll":          false,
		"normalize":         false,
		"padEnd":            false,
		"padStart":          false,
		"repeat":            false,
		"replace":           false,
		"replaceAll":        false,
		"search":            false,
		"slice":             false,
		"split":             false,
		"startsWith":        false,
		"substr":            false,
		"substring":         false,
		"toLocaleLowerCase": false,
		"toLocaleUpperCase": false,
		"toLowerCase":       false,
		"toUpperCase":       false,
		"trim":              false,
		"trimEnd":           false,
		"trimStart":         false,
		"valueOf":           false,
		// TODO: handle Symbol.iterator as key
	},
	"RexExp": {
		"compile":  true,
		"exec":     true, // when using global or sticky flags
		"test":     true, // when using global or sticky flags
		"toString": false,
		// TODO: handle Symbol.match, Symbol.replace, Symbol.search, Symbol.split as keys
	},
	"Number": {
		"toExponential":  false,
		"toFixed":        false,
		"toLocaleString": false,
		"toPrecision":    false,
		"toString":       false,
		"valueOf":        false,
	},
}

func TestConvertModule_LibES5(t *testing.T) {
	// Find the repo root by looking for go.mod
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Skip("Could not find repository root:", err)
	}

	libDtsPath := filepath.Join(repoRoot, "node_modules", "typescript", "lib", "lib.es5.d.ts")

	// Check if the file exists
	if _, err := os.Stat(libDtsPath); os.IsNotExist(err) {
		t.Skipf("TypeScript lib.es5.d.ts not found at: %s", libDtsPath)
	}

	// Read the file
	contents, err := os.ReadFile(libDtsPath)
	if err != nil {
		t.Fatalf("Failed to read lib.es5.d.ts: %v", err)
	}

	source := &ast.Source{
		Path:     libDtsPath,
		Contents: string(contents),
		ID:       0,
	}

	// Parse the module
	parser := dts_parser.NewDtsParser(source)
	dtsModule, parseErrors := parser.ParseModule()

	// Log statistics
	t.Logf("Parsed lib.es5.d.ts: %d bytes", len(contents))
	t.Logf("Parse errors: %d", len(parseErrors))

	if len(parseErrors) > 0 {
		// Log first 10 errors for debugging
		maxErrors := 10
		if len(parseErrors) < maxErrors {
			maxErrors = len(parseErrors)
		}
		t.Errorf("Expected no parse errors, but got %d errors. First %d:", len(parseErrors), maxErrors)
		for i := 0; i < maxErrors; i++ {
			t.Errorf("  %v", parseErrors[i])
		}
		t.FailNow()
	}

	if dtsModule != nil {
		t.Logf("Parsed %d top-level statements", len(dtsModule.Statements))
	}

	// Convert the module
	astModule, err := interop.ConvertModule(dtsModule)
	if err != nil {
		t.Fatalf("ConvertModule failed: %v", err)
	}

	// Validate the converted module
	if astModule == nil {
		t.Fatal("ConvertModule returned nil module")
	}

	// Count total declarations across all namespaces
	totalDecls := 0
	astModule.Namespaces.Scan(func(name string, namespace *ast.Namespace) bool {
		declCount := len(namespace.Decls)
		totalDecls += declCount
		if declCount > 0 {
			t.Logf("Namespace '%s': %d declarations", name, declCount)
		}
		return true
	})

	t.Logf("Total declarations: %d", totalDecls)

	// Basic sanity checks
	if totalDecls == 0 {
		t.Error("Expected at least some declarations in converted module")
	}

	// Check that root namespace exists
	rootNS, exists := astModule.Namespaces.Get("")
	if !exists {
		t.Error("Root namespace not found")
	} else {
		t.Logf("Root namespace has %d declarations", len(rootNS.Decls))
	}

	// Infer the module
	c := checker.NewChecker()
	scope := checker.NewScope()
	inferCtx := checker.Context{
		Scope:      scope,
		IsAsync:    false,
		IsPatMatch: false,
	}
	inferredScope, inferErrors := c.InferModule(inferCtx, astModule)

	t.Logf("Infer errors: %d", len(inferErrors))
	if len(inferErrors) > 0 {
		for i := 0; i < len(inferErrors); i++ {
			t.Logf("  %v at %v", inferErrors[i].Message(), inferErrors[i].Span())
			// if e, ok := inferErrors[i].(*checker.UnknownTypeError); ok {
			// 	node := checker.GetNode(e.TypeRef.Provenance())
			// 	if node != nil {
			// 		str, _ := printer.Print(node, printer.DefaultOptions())
			// 		t.Logf("    node: %s", str)
			// 		t.Logf("    node: %#v", node)
			// 	}
			// }
		}
	}

	typeCount := len(inferredScope.Types)
	valueCount := len(inferredScope.Values)
	t.Logf("Inferred scope - Types: %d, Values: %d", typeCount, valueCount)

	UpdateMethodMutability(inferredScope)
	UpdateArrayMutability(inferredScope)

	// Find and print the RegExp type
	if regExpBinding, exists := inferredScope.Types["RegExp"]; exists {
		t.Logf("Found RegExp in inferred scope:")
		t.Logf("  Type: %s", regExpBinding.Type.String())
	}

	// Find and print the String type
	if stringBinding, exists := inferredScope.Types["String"]; exists {
		t.Logf("Found String in inferred scope:")
		t.Logf("  Type: %s", stringBinding.Type.String())
	}

	// Find and print the Number type
	if stringBinding, exists := inferredScope.Types["Number"]; exists {
		t.Logf("Found Number in inferred scope:")
		t.Logf("  Type: %s", stringBinding.Type.String())
	}

	// Find and print the Array type
	if arrayBinding, exists := inferredScope.Types["Array"]; exists {
		t.Logf("Found Array in inferred scope:")
		t.Logf("  Type: %s", arrayBinding.Type.String())
	}

	// Find and print the Promise type
	if promiseBinding, exists := inferredScope.Types["Promise"]; exists {
		t.Logf("Found Promise in inferred scope:")
		t.Logf("  Type: %s", promiseBinding.Type.String())
	}
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
