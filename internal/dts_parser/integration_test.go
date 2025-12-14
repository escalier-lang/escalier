package dts_parser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
)

func TestParseTypeScriptLibDts(t *testing.T) {
	// Find the repo root by looking for go.mod
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Skip("Could not find repository root:", err)
	}

	testCases := []struct {
		name     string
		filename string
	}{
		{
			name:     "lib.es5.d.ts",
			filename: "lib.es5.d.ts",
		},
		{
			name:     "lib.dom.d.ts",
			filename: "lib.dom.d.ts",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			libDtsPath := filepath.Join(repoRoot, "node_modules", "typescript", "lib", tc.filename)

			// Check if the file exists
			if _, err := os.Stat(libDtsPath); os.IsNotExist(err) {
				t.Skipf("TypeScript %s not found at: %s", tc.filename, libDtsPath)
			}

			// Read the file
			contents, err := os.ReadFile(libDtsPath)
			if err != nil {
				t.Fatalf("Failed to read %s: %v", tc.filename, err)
			}

			source := &ast.Source{
				Path:     libDtsPath,
				Contents: string(contents),
				ID:       0,
			}

			parser := NewDtsParser(source)
			module, errors := parser.ParseModule()

			// Log statistics
			t.Logf("Parsed %s: %d bytes", tc.filename, len(contents))
			t.Logf("Parse errors: %d", len(errors))

			if len(errors) > 0 {
				// Log first 10 errors for debugging
				maxErrors := 10
				if len(errors) < maxErrors {
					maxErrors = len(errors)
				}
				t.Errorf("Expected no parse errors, but got %d errors. First %d:", len(errors), maxErrors)
				for i := 0; i < maxErrors; i++ {
					t.Errorf("  %v", errors[i])
				}
				t.FailNow()
			}

			if module != nil {
				t.Logf("Parsed %d top-level statements", len(module.Statements))
			}
		})
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
