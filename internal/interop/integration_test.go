package interop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/printer"
)

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
	astModule, err := ConvertModule(dtsModule)
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
		// Log first 10 infer errors for debugging
		maxErrors := 10
		if len(inferErrors) < maxErrors {
			maxErrors = len(inferErrors)
		}
		t.Logf("First %d infer errors:", maxErrors)
		for i := 0; i < maxErrors; i++ {
			t.Logf("  %v at %v", inferErrors[i].Message(), inferErrors[i].Span())
			if e, ok := inferErrors[i].(*checker.UnknownTypeError); ok {
				node := checker.GetNode(e.TypeRef.Provenance())
				if node != nil {
					str, _ := printer.Print(node, printer.DefaultOptions())
					t.Logf("    node: %s", str)
					t.Logf("    node: %#v", node)
				}
			}
		}
	}

	if inferredScope != nil {
		typeCount := len(inferredScope.Types)
		valueCount := len(inferredScope.Values)
		t.Logf("Inferred scope - Types: %d, Values: %d", typeCount, valueCount)

		// Find and print the Promise type
		if promiseBinding, exists := inferredScope.Types["Promise"]; exists {
			t.Logf("Found Promise in inferred scope:")
			t.Logf("  Type: %s", promiseBinding.Type.String())
		}
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
