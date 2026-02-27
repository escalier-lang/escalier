package checker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsBundleFile(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     bool
	}{
		{"ES2015 bundle", "lib.es2015.d.ts", true},
		{"ES2016 bundle", "lib.es2016.d.ts", true},
		{"ES2017 bundle", "lib.es2017.d.ts", true},
		{"ES2020 bundle", "lib.es2020.d.ts", true},
		{"ES2023 bundle", "lib.es2023.d.ts", true},
		{"ES5 is not a bundle", "lib.es5.d.ts", false},
		{"ES2015 core is not a bundle", "lib.es2015.core.d.ts", false},
		{"ES2015 collection is not a bundle", "lib.es2015.collection.d.ts", false},
		{"DOM is not a bundle", "lib.dom.d.ts", false},
		{"ESNext is not a bundle", "lib.esnext.d.ts", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBundleFile(tt.filename); got != tt.want {
				t.Errorf("isBundleFile(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}

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

func TestExtractESVersion(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{"ES5 file", "lib.es5.d.ts", "es5"},
		{"ES2015 bundle", "lib.es2015.d.ts", "es2015"},
		{"ES2015 core", "lib.es2015.core.d.ts", "es2015"},
		{"ES2015 collection", "lib.es2015.collection.d.ts", "es2015"},
		{"ES2020 bundle", "lib.es2020.d.ts", "es2020"},
		{"ES2020 promise", "lib.es2020.promise.d.ts", "es2020"},
		{"ESNext bundle", "lib.esnext.d.ts", "esnext"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractESVersion(tt.filename); got != tt.want {
				t.Errorf("extractESVersion(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}

func TestCompareESVersions(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{"ES2015 before ES2016", "es2015", "es2016", true},
		{"ES2016 not before ES2015", "es2016", "es2015", false},
		{"ES2015 before ES2020", "es2015", "es2020", true},
		{"ES2020 before ES2021", "es2020", "es2021", true},
		{"Same version", "es2015", "es2015", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareESVersions(tt.a, tt.b); got != tt.want {
				t.Errorf("compareESVersions(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
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

func TestDiscoverESLibFiles(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatal("Could not find repository root:", err)
	}

	libDir := filepath.Join(repoRoot, "node_modules", "typescript", "lib")
	if _, err := os.Stat(libDir); os.IsNotExist(err) {
		t.Fatal("TypeScript lib directory not found:", libDir)
	}

	// Test with empty targetVersion (all versions)
	libFiles, err := discoverESLibFiles(libDir, "")
	if err != nil {
		t.Fatalf("discoverESLibFiles() error = %v", err)
	}

	// Verify we got some files
	if len(libFiles) == 0 {
		t.Error("discoverESLibFiles() returned 0 files")
	}

	// Verify lib.es5.d.ts is first
	if len(libFiles) > 0 && libFiles[0] != "lib.es5.d.ts" {
		t.Errorf("discoverESLibFiles()[0] = %q, want %q", libFiles[0], "lib.es5.d.ts")
	}

	// Verify no ESNext files are included
	for _, f := range libFiles {
		if isESNextFile(f) {
			t.Errorf("discoverESLibFiles() included ESNext file: %s", f)
		}
	}

	// Verify no bundle files are included (they only contain references)
	for _, f := range libFiles {
		if isBundleFile(f) {
			t.Errorf("discoverESLibFiles() included bundle file: %s", f)
		}
	}

	// Verify ES2015 files come before ES2016 files
	var lastES2015Index, firstES2016Index int = -1, -1
	for i, f := range libFiles {
		version := extractESVersion(f)
		if version == "es2015" {
			lastES2015Index = i
		}
		if version == "es2016" && firstES2016Index == -1 {
			firstES2016Index = i
		}
	}
	if lastES2015Index != -1 && firstES2016Index != -1 && lastES2015Index > firstES2016Index {
		t.Errorf("ES2015 files should come before ES2016 files, but last ES2015 at %d, first ES2016 at %d",
			lastES2015Index, firstES2016Index)
	}

	// Log discovered files for debugging
	t.Logf("Discovered %d ES lib files (all versions)", len(libFiles))
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
		{
			name:          "All versions (empty)",
			targetVersion: "",
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

			hasES5 := false
			hasES2015 := false
			hasES2016 := false

			for _, f := range libFiles {
				version := extractESVersion(f)
				switch version {
				case "es5":
					hasES5 = true
				case "es2015":
					hasES2015 = true
				case "es2016":
					hasES2016 = true
				}
			}

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
