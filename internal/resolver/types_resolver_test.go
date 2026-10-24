package resolver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResolveTypesEntryPoint covers the places ResolveTypesEntryPoint finds a package's
// types. Each case writes files under a temporary root and resolves from the directory
// `from` names under it. `want` is the entry point's path under the root.
func TestResolveTypesEntryPoint(t *testing.T) {
	tests := []struct {
		name   string
		files  []string
		module string
		from   string
		want   string
	}{
		{
			name:   "a package that ships its own types",
			files:  []string{"node_modules/plain/index.d.ts"},
			module: "plain",
			want:   "node_modules/plain/index.d.ts",
		},
		{
			name:   "a package whose types are in @types",
			files:  []string{"node_modules/@types/react/index.d.ts"},
			module: "react",
			want:   "node_modules/@types/react/index.d.ts",
		},
		{
			name:   "a scoped package whose types are in @types",
			files:  []string{"node_modules/@types/scope__pkg/index.d.ts"},
			module: "@scope/pkg",
			want:   "node_modules/@types/scope__pkg/index.d.ts",
		},
		{
			name:   "a subpath that names a file",
			files:  []string{"node_modules/@types/react/index.d.ts", "node_modules/@types/react/jsx-runtime.d.ts"},
			module: "react/jsx-runtime",
			want:   "node_modules/@types/react/jsx-runtime.d.ts",
		},
		{
			name:   "a package that ships no types beside its @types package",
			files:  []string{"node_modules/react/index.js", "node_modules/react/package.json", "node_modules/@types/react/index.d.ts"},
			module: "react",
			want:   "node_modules/@types/react/index.d.ts",
		},
		{
			name:   "a package above the directory resolved from",
			files:  []string{"node_modules/@types/react/index.d.ts", "src/components/deep/.keep"},
			module: "react",
			from:   "src/components/deep",
			want:   "node_modules/@types/react/index.d.ts",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for _, file := range test.files {
				path := filepath.Join(root, filepath.FromSlash(file))
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
				require.NoError(t, os.WriteFile(path, nil, 0o644))
			}
			got, err := ResolveTypesEntryPoint(test.module, filepath.Join(root, filepath.FromSlash(test.from)))
			require.NoError(t, err)
			require.Equal(t, filepath.Join(root, filepath.FromSlash(test.want)), got)
		})
	}
}

// TestResolveTypesEntryPointRejectsARelativePath asserts that a module name with a `.` or
// `..` segment resolves nothing, rather than reaching outside `node_modules`.
func TestResolveTypesEntryPointRejectsARelativePath(t *testing.T) {
	for _, module := range []string{"./local", "../shared", "pkg/../other"} {
		_, err := ResolveTypesEntryPoint(module, t.TempDir())
		require.EqualError(t, err, fmt.Sprintf("%q is a relative path, not a package name", module))
	}
}

// TestResolveTypesEntryPointReportsABrokenEntryPoint asserts that a package directory whose
// `types` field names a missing file reports that, rather than a generic not-found.
func TestResolveTypesEntryPointReportsABrokenEntryPoint(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "node_modules", "broken")
	require.NoError(t, os.MkdirAll(pkgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{"types": "missing.d.ts"}`), 0o644))
	_, err := ResolveTypesEntryPoint("broken", root)
	require.EqualError(t, err, fmt.Sprintf("types entry point not found: %s (from 'types' field)",
		filepath.Join(pkgDir, "missing.d.ts")))
}

func TestGetTypesEntryPointFromTypes(t *testing.T) {
	// Create a temp package directory with package.json containing "types" field
	tmpDir := t.TempDir()

	// Create the types file
	typesPath := filepath.Join(tmpDir, "dist", "index.d.ts")
	if err := os.MkdirAll(filepath.Dir(typesPath), 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}
	if err := os.WriteFile(typesPath, []byte("// types"), 0644); err != nil {
		t.Fatalf("Failed to create types file: %v", err)
	}

	// Create package.json with "types" field
	pkgJson := `{"types": "dist/index.d.ts"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJson), 0644); err != nil {
		t.Fatalf("Failed to create package.json: %v", err)
	}

	// Test: should return path from "types" field
	result, err := GetTypesEntryPoint(tmpDir)
	if err != nil {
		t.Fatalf("Expected to find entry point, got error: %v", err)
	}
	if result != typesPath {
		t.Errorf("Expected %s, got %s", typesPath, result)
	}
}

func TestGetTypesEntryPointFromTypings(t *testing.T) {
	// Create a temp package directory with package.json containing "typings" field
	tmpDir := t.TempDir()

	// Create the types file
	typingsPath := filepath.Join(tmpDir, "lib", "types.d.ts")
	if err := os.MkdirAll(filepath.Dir(typingsPath), 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}
	if err := os.WriteFile(typingsPath, []byte("// typings"), 0644); err != nil {
		t.Fatalf("Failed to create typings file: %v", err)
	}

	// Create package.json with "typings" field (older convention)
	pkgJson := `{"typings": "lib/types.d.ts"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJson), 0644); err != nil {
		t.Fatalf("Failed to create package.json: %v", err)
	}

	// Test: should return path from "typings" field
	result, err := GetTypesEntryPoint(tmpDir)
	if err != nil {
		t.Fatalf("Expected to find entry point, got error: %v", err)
	}
	if result != typingsPath {
		t.Errorf("Expected %s, got %s", typingsPath, result)
	}
}

func TestGetTypesEntryPointFallback(t *testing.T) {
	// Create a temp package directory with no types field
	tmpDir := t.TempDir()

	// Create index.d.ts
	indexPath := filepath.Join(tmpDir, "index.d.ts")
	if err := os.WriteFile(indexPath, []byte("// index"), 0644); err != nil {
		t.Fatalf("Failed to create index.d.ts: %v", err)
	}

	// Create package.json with no types field
	pkgJson := `{"name": "test-pkg"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJson), 0644); err != nil {
		t.Fatalf("Failed to create package.json: %v", err)
	}

	// Test: should fallback to index.d.ts
	result, err := GetTypesEntryPoint(tmpDir)
	if err != nil {
		t.Fatalf("Expected to find entry point, got error: %v", err)
	}
	if result != indexPath {
		t.Errorf("Expected %s, got %s", indexPath, result)
	}
}

func TestGetTypesEntryPointFromExports(t *testing.T) {
	// Create a temp package directory with exports field containing types
	tmpDir := t.TempDir()

	// Create the types file
	typesPath := filepath.Join(tmpDir, "dist", "index.d.ts")
	if err := os.MkdirAll(filepath.Dir(typesPath), 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}
	if err := os.WriteFile(typesPath, []byte("// types"), 0644); err != nil {
		t.Fatalf("Failed to create types file: %v", err)
	}

	// Create package.json with exports field
	pkgJson := `{"exports": {".": {"types": "./dist/index.d.ts"}}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJson), 0644); err != nil {
		t.Fatalf("Failed to create package.json: %v", err)
	}

	// Test: should return path from exports
	result, err := GetTypesEntryPoint(tmpDir)
	if err != nil {
		t.Fatalf("Expected to find entry point, got error: %v", err)
	}
	if result != typesPath {
		t.Errorf("Expected %s, got %s", typesPath, result)
	}
}

func TestGetTypesEntryPointFromExportsNested(t *testing.T) {
	// Create a temp package directory with nested exports containing import/require conditions
	tmpDir := t.TempDir()

	// Create the types file
	typesPath := filepath.Join(tmpDir, "esm", "index.d.ts")
	if err := os.MkdirAll(filepath.Dir(typesPath), 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}
	if err := os.WriteFile(typesPath, []byte("// types"), 0644); err != nil {
		t.Fatalf("Failed to create types file: %v", err)
	}

	// Create package.json with nested exports
	pkgJson := `{"exports": {".": {"import": {"types": "./esm/index.d.ts"}}}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJson), 0644); err != nil {
		t.Fatalf("Failed to create package.json: %v", err)
	}

	// Test: should return types path from nested condition
	result, err := GetTypesEntryPoint(tmpDir)
	if err != nil {
		t.Fatalf("Expected to find entry point, got error: %v", err)
	}
	if result != typesPath {
		t.Errorf("Expected %s, got %s", typesPath, result)
	}
}

func TestGetTypesEntryPointFromMain(t *testing.T) {
	// Create a temp package directory with main field
	tmpDir := t.TempDir()

	// Create the types file
	typesPath := filepath.Join(tmpDir, "dist", "index.d.ts")
	if err := os.MkdirAll(filepath.Dir(typesPath), 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}
	if err := os.WriteFile(typesPath, []byte("// types"), 0644); err != nil {
		t.Fatalf("Failed to create types file: %v", err)
	}

	// Create package.json with main field (.cjs extension)
	pkgJson := `{"main": "./dist/index.cjs"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJson), 0644); err != nil {
		t.Fatalf("Failed to create package.json: %v", err)
	}

	// Test: should return .d.ts path derived from main field
	result, err := GetTypesEntryPoint(tmpDir)
	if err != nil {
		t.Fatalf("Expected to find entry point, got error: %v", err)
	}
	if result != typesPath {
		t.Errorf("Expected %s, got %s", typesPath, result)
	}
}

func TestGetTypesEntryPointFileNotFound(t *testing.T) {
	// Create a temp package directory with types field pointing to non-existent file
	tmpDir := t.TempDir()

	// Create package.json with types pointing to missing file
	pkgJson := `{"types": "./missing.d.ts"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJson), 0644); err != nil {
		t.Fatalf("Failed to create package.json: %v", err)
	}

	// Test: should return error for missing file
	_, err := GetTypesEntryPoint(tmpDir)
	if err == nil {
		t.Error("Expected error for missing types file, got nil")
	}
}

func TestGetTypesEntryPointNoPackageJson(t *testing.T) {
	// Create a temp package directory with only index.d.ts (no package.json)
	tmpDir := t.TempDir()

	// Create index.d.ts
	indexPath := filepath.Join(tmpDir, "index.d.ts")
	if err := os.WriteFile(indexPath, []byte("// index"), 0644); err != nil {
		t.Fatalf("Failed to create index.d.ts: %v", err)
	}

	// Test: should fallback to index.d.ts when no package.json
	result, err := GetTypesEntryPoint(tmpDir)
	if err != nil {
		t.Fatalf("Expected to find entry point, got error: %v", err)
	}
	if result != indexPath {
		t.Errorf("Expected %s, got %s", indexPath, result)
	}
}

func TestGetTypesEntryPointFromExportsDirectString(t *testing.T) {
	// Create a temp package directory with exports as direct string ending in .d.ts
	tmpDir := t.TempDir()

	// Create the types file
	typesPath := filepath.Join(tmpDir, "index.d.ts")
	if err := os.WriteFile(typesPath, []byte("// types"), 0644); err != nil {
		t.Fatalf("Failed to create types file: %v", err)
	}

	// Create package.json with exports as direct .d.ts string
	pkgJson := `{"exports": "./index.d.ts"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJson), 0644); err != nil {
		t.Fatalf("Failed to create package.json: %v", err)
	}

	// Test: should return path from direct exports string
	result, err := GetTypesEntryPoint(tmpDir)
	if err != nil {
		t.Fatalf("Expected to find entry point, got error: %v", err)
	}
	if result != typesPath {
		t.Errorf("Expected %s, got %s", typesPath, result)
	}
}

func TestGetTypesEntryPointTypesHasPriorityOverTypings(t *testing.T) {
	// Create a temp package directory with both types and typings fields
	tmpDir := t.TempDir()

	// Create the types file
	typesPath := filepath.Join(tmpDir, "types.d.ts")
	if err := os.WriteFile(typesPath, []byte("// types"), 0644); err != nil {
		t.Fatalf("Failed to create types file: %v", err)
	}

	// Create the typings file
	typingsPath := filepath.Join(tmpDir, "typings.d.ts")
	if err := os.WriteFile(typingsPath, []byte("// typings"), 0644); err != nil {
		t.Fatalf("Failed to create typings file: %v", err)
	}

	// Create package.json with both types and typings
	pkgJson := `{"types": "./types.d.ts", "typings": "./typings.d.ts"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJson), 0644); err != nil {
		t.Fatalf("Failed to create package.json: %v", err)
	}

	// Test: should prefer types over typings
	result, err := GetTypesEntryPoint(tmpDir)
	if err != nil {
		t.Fatalf("Expected to find entry point, got error: %v", err)
	}
	if result != typesPath {
		t.Errorf("Expected %s (from 'types'), got %s", typesPath, result)
	}
}

// TestIntegrationWithRealTypesReact tests the resolver with the actual @types/react
// package installed in the project's node_modules. This test is skipped if
// @types/react is not installed.
func TestIntegrationWithRealTypesReact(t *testing.T) {
	// Get the project root by looking for go.mod
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}

	// Walk up to find the project root (where go.mod is)
	projectRoot := cwd
	for {
		if _, err := os.Stat(filepath.Join(projectRoot, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(projectRoot)
		if parent == projectRoot {
			t.Skip("Could not find project root with go.mod")
			return
		}
		projectRoot = parent
	}

	// Try to resolve @types/react from the project root
	entryPoint, err := ResolveTypesEntryPoint("react", projectRoot)
	if err != nil {
		t.Skip("@types/react not installed, skipping integration test")
		return
	}

	t.Logf("Entry point: %s", entryPoint)

	// Verify the entry point file exists and is a .d.ts file
	if _, err := os.Stat(entryPoint); err != nil {
		t.Errorf("Entry point file does not exist: %v", err)
	}

	if !filepath.IsAbs(entryPoint) {
		t.Errorf("Entry point should be an absolute path, got: %s", entryPoint)
	}

	if !strings.HasSuffix(entryPoint, ".d.ts") {
		t.Errorf("Entry point should be a .d.ts file, got: %s", entryPoint)
	}
}

func TestResolveExportsTypes(t *testing.T) {
	tests := []struct {
		name     string
		exports  any
		expected string
	}{
		{
			name:     "direct .d.ts string",
			exports:  "./index.d.ts",
			expected: "./index.d.ts",
		},
		{
			name:     "direct .js string (not .d.ts)",
			exports:  "./index.js",
			expected: "",
		},
		{
			name:     "object with types key",
			exports:  map[string]any{"types": "./dist/index.d.ts"},
			expected: "./dist/index.d.ts",
		},
		{
			name:     "object with dot entry",
			exports:  map[string]any{".": map[string]any{"types": "./lib/index.d.ts"}},
			expected: "./lib/index.d.ts",
		},
		{
			name: "nested import condition",
			exports: map[string]any{
				".": map[string]any{
					"import": map[string]any{"types": "./esm/index.d.ts"},
				},
			},
			expected: "./esm/index.d.ts",
		},
		{
			name: "nested require condition",
			exports: map[string]any{
				".": map[string]any{
					"require": map[string]any{"types": "./cjs/index.d.ts"},
				},
			},
			expected: "./cjs/index.d.ts",
		},
		{
			name: "nested default condition",
			exports: map[string]any{
				".": map[string]any{
					"default": map[string]any{"types": "./default/index.d.ts"},
				},
			},
			expected: "./default/index.d.ts",
		},
		{
			name:     "empty object",
			exports:  map[string]any{},
			expected: "",
		},
		{
			name:     "nil",
			exports:  nil,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveExportsTypes(tt.exports)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}
