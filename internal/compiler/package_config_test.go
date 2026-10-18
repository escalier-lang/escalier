package compiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// TestReadPackageConfig covers the package.json shapes ReadPackageConfig reads.
func TestReadPackageConfig(t *testing.T) {
	tests := []struct {
		name string
		// packageJSON is the file's contents, or empty for a directory with no file.
		packageJSON string
		want        PackageConfig
		wantErr     bool
	}{
		{name: "NoFile", want: PackageConfig{}},
		{name: "NoEscalierField", packageJSON: `{"name": "p"}`, want: PackageConfig{}},
		{name: "AmbientTrue", packageJSON: `{"escalier": {"ambient": true}}`, want: PackageConfig{}},
		{name: "AmbientFalse", packageJSON: `{"escalier": {"ambient": false}}`, want: PackageConfig{NoAmbientNames: true}},
		{name: "Malformed", packageJSON: `{"escalier": `, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if test.packageJSON != "" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(test.packageJSON), 0o644))
			}
			got, err := ReadPackageConfig(dir)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			// Dir is the temporary directory, so each case's want leaves it out.
			want := test.want
			want.Dir = dir
			require.Equal(t, want, got)
		})
	}
}

// TestCheckPackageAmbientNames covers a package with and without the ambient names. One
// that opts out leaves them unbound in lib/ and in a bin/ script with no library, and a
// lib/ file still reaches a builtin through its import.
func TestCheckPackageAmbientNames(t *testing.T) {
	optOut := PackageConfig{NoAmbientNames: true}
	tests := []struct {
		name     string
		path     string
		src      string
		config   PackageConfig
		wantErrs []string
	}{
		{name: "LibOptedOut", path: "lib/index.esc", src: "val pi = Math.PI\n", config: optOut, wantErrs: []string{"Unknown identifier: Math"}},
		{name: "LibOptedOutImports", path: "lib/index.esc", src: "import \"std:math\"\nval pi = math.PI\n", config: optOut, wantErrs: []string{}},
		{name: "LibAmbient", path: "lib/index.esc", src: "val pi = Math.PI\n", wantErrs: []string{}},
		{name: "BinOptedOut", path: "bin/index.esc", src: "val pi = Math.PI\n", config: optOut, wantErrs: []string{"Unknown identifier: Math"}},
		{name: "BinAmbient", path: "bin/index.esc", src: "val pi = Math.PI\n", wantErrs: []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := CheckPackage([]*ast.Source{{ID: 1, Path: test.path, Contents: test.src}}, test.config)
			require.Empty(t, out.ParseErrors)
			require.Equal(t, test.wantErrs, messages(out.TypeErrors))
		})
	}
}

// TestCheckPackageResolvesNodeModules covers a lib/ file importing an npm package from the
// `node_modules` beside the package.json ReadPackageConfig read. A config with no Dir
// resolves no such import.
func TestCheckPackageResolvesNodeModules(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "node_modules", "plain")
	require.NoError(t, os.MkdirAll(pkgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "index.d.ts"),
		[]byte("export declare const version: string;\n"), 0o644))
	config, err := ReadPackageConfig(dir)
	require.NoError(t, err)

	tests := []struct {
		name     string
		path     string
		config   PackageConfig
		wantErrs []string
	}{
		{name: "Lib", path: "lib/index.esc", config: config, wantErrs: []string{}},
		{name: "NoDir", path: "lib/index.esc", config: PackageConfig{}, wantErrs: []string{
			`cannot resolve import "plain": unrecognized scheme in "plain"`,
			"Unknown identifier: p",
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			src := "import \"plain\" as p\nval v: string = p.version\n"
			out := CheckPackage([]*ast.Source{{ID: 1, Path: test.path, Contents: src}}, test.config)
			require.Empty(t, out.ParseErrors)
			require.Equal(t, test.wantErrs, messages(out.TypeErrors))
		})
	}
}
