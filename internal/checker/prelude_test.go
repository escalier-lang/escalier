package checker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsESNextFile(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     bool
	}{
		{"ESNext bundle", "lib.esnext.d.ts", true},
		{"ESNext array", "lib.esnext.array.d.ts", true},
		{"ESNext promise", "lib.esnext.promise.d.ts", true},
		{"ES2015 is not ESNext", "lib.es2015.d.ts", false},
		{"ES2020 is not ESNext", "lib.es2020.d.ts", false},
		{"ES5 is not ESNext", "lib.es5.d.ts", false},
		{"DOM is not ESNext", "lib.dom.d.ts", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isESNextFile(tt.filename); got != tt.want {
				t.Errorf("isESNextFile(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}

func TestIsESLibReference(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want bool
	}{
		{"ES5", "es5", true},
		{"ES2015", "es2015", true},
		{"ES2015 core", "es2015.core", true},
		{"ES2015 collection", "es2015.collection", true},
		{"ES2020", "es2020", true},
		{"ESNext", "esnext", true},
		{"Decorators is not ES", "decorators", false},
		{"Decorators.legacy is not ES", "decorators.legacy", false},
		{"DOM is not ES", "dom", false},
		{"Scripthost is not ES", "scripthost", false},
		{"Webworker is not ES", "webworker", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isESLibReference(tt.ref); got != tt.want {
				t.Errorf("isESLibReference(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

func TestParseReferenceDirectives(t *testing.T) {
	// Create a temporary file with reference directives
	content := `/// <reference no-default-lib="true"/>
/// <reference lib="es5" />
/// <reference lib="es2015.core" />
/// <reference lib="es2015.collection" />
/// <reference lib="es2015.iterable" />
`
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test-bundle.d.ts")

	err := os.WriteFile(tmpFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	refs, err := parseReferenceDirectives(tmpFile)
	if err != nil {
		t.Fatalf("parseReferenceDirectives() error = %v", err)
	}

	expected := []string{"es5", "es2015.core", "es2015.collection", "es2015.iterable"}
	if len(refs) != len(expected) {
		t.Errorf("parseReferenceDirectives() got %d refs, want %d", len(refs), len(expected))
	}

	for i, ref := range refs {
		if i < len(expected) && ref != expected[i] {
			t.Errorf("parseReferenceDirectives()[%d] = %q, want %q", i, ref, expected[i])
		}
	}
}

func TestDiscoverESLibFilesES2015(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatal("Could not find repository root:", err)
	}

	libDir := filepath.Join(repoRoot, "node_modules", "typescript", "lib")
	if _, err := os.Stat(libDir); os.IsNotExist(err) {
		t.Fatal("TypeScript lib directory not found:", libDir)
	}

	// Test with ES2015 target version
	libFiles, err := discoverESLibFiles(libDir, "es2015")
	if err != nil {
		t.Fatalf("discoverESLibFiles() error = %v", err)
	}

	// Verify we got some files
	if len(libFiles) == 0 {
		t.Error("discoverESLibFiles() returned 0 files")
	}

	// Verify no ESNext files are included
	for _, f := range libFiles {
		if isESNextFile(f) {
			t.Errorf("discoverESLibFiles() included ESNext file: %s", f)
		}
	}

	// Verify lib.es5.d.ts is included (ES2015 references ES5)
	hasES5 := false
	for _, f := range libFiles {
		if f == "lib.es5.d.ts" {
			hasES5 = true
			break
		}
	}
	if !hasES5 {
		t.Error("discoverESLibFiles() should include lib.es5.d.ts")
	}

	// Log discovered files for debugging
	t.Logf("Discovered %d ES lib files (es2015)", len(libFiles))
	for i, f := range libFiles {
		t.Logf("  [%d] %s", i, f)
	}
}

func TestDiscoverESLibFilesIncludesES2015(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatal("Could not find repository root:", err)
	}

	libDir := filepath.Join(repoRoot, "node_modules", "typescript", "lib")
	if _, err := os.Stat(libDir); os.IsNotExist(err) {
		t.Fatal("TypeScript lib directory not found:", libDir)
	}

	// Test with targetVersion "es2015" to include ES2015 files
	libFiles, err := discoverESLibFiles(libDir, "es2015")
	if err != nil {
		t.Fatalf("discoverESLibFiles() error = %v", err)
	}

	// Check for expected ES2015 sub-libraries
	expectedES2015Files := []string{
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

	fileSet := make(map[string]bool)
	for _, f := range libFiles {
		fileSet[f] = true
	}

	for _, expected := range expectedES2015Files {
		if !fileSet[expected] {
			t.Errorf("discoverESLibFiles() missing expected file: %s", expected)
		}
	}
}

func TestDiscoverESLibFilesWithTargetVersion(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatal("Could not find repository root:", err)
	}

	libDir := filepath.Join(repoRoot, "node_modules", "typescript", "lib")
	if _, err := os.Stat(libDir); os.IsNotExist(err) {
		t.Fatal("TypeScript lib directory not found:", libDir)
	}

	// Helper to check if filename contains a version prefix
	containsVersion := func(files []string, prefix string) bool {
		for _, f := range files {
			if strings.Contains(f, prefix) {
				return true
			}
		}
		return false
	}

	tests := []struct {
		name          string
		targetVersion string
		wantES5       bool
		wantES2015    bool
		wantES2016    bool
	}{
		{
			name:          "ES5 only",
			targetVersion: "es5",
			wantES5:       true,
			wantES2015:    false,
			wantES2016:    false,
		},
		{
			name:          "Up to ES2015",
			targetVersion: "es2015",
			wantES5:       true,
			wantES2015:    true,
			wantES2016:    false,
		},
		{
			name:          "Up to ES2016",
			targetVersion: "es2016",
			wantES5:       true,
			wantES2015:    true,
			wantES2016:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			libFiles, err := discoverESLibFiles(libDir, tt.targetVersion)
			if err != nil {
				t.Fatalf("discoverESLibFiles() error = %v", err)
			}

			hasES5 := containsVersion(libFiles, "es5")
			hasES2015 := containsVersion(libFiles, "es2015")
			hasES2016 := containsVersion(libFiles, "es2016")

			if hasES5 != tt.wantES5 {
				t.Errorf("ES5 files: got %v, want %v", hasES5, tt.wantES5)
			}
			if hasES2015 != tt.wantES2015 {
				t.Errorf("ES2015 files: got %v, want %v", hasES2015, tt.wantES2015)
			}
			if hasES2016 != tt.wantES2016 {
				t.Errorf("ES2016 files: got %v, want %v", hasES2016, tt.wantES2016)
			}

			t.Logf("Target %q: discovered %d files", tt.targetVersion, len(libFiles))
		})
	}
}
