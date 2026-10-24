package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
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
		{
			name: "Dependencies",
			packageJSON: `{
				"dependencies": {"a": "1"},
				"peerDependencies": {"b": "1"},
				"optionalDependencies": {"c": "1"},
				"devDependencies": {"d": "1"}
			}`,
			want: PackageConfig{Dependencies: set.FromSlice([]string{"a", "b", "c"})},
		},
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
			// Dir is the temporary directory, so each case's want leaves it out. A case
			// that leaves Dependencies out lists none.
			want := test.want
			want.Dir = dir
			if want.Dependencies == nil {
				want.Dependencies = set.NewSet[string]()
			}
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
// `node_modules` beside the package.json ReadPackageConfig read. The import resolves only
// when the package.json lists the package, and a config with no Dir resolves no such import.
func TestCheckPackageResolvesNodeModules(t *testing.T) {
	tests := []struct {
		name string
		// dependencies is the package.json's `dependencies` field.
		dependencies string
		// devDependencies is the package.json's `devDependencies` field.
		devDependencies string
		src             string
		noDir           bool
		wantErrs        []string
	}{
		{
			name:         "ListedDependency",
			dependencies: `{"plain": "1"}`,
			src:          "import \"plain\" as p\nval v: string = p.version\n",
			wantErrs:     []string{},
		},
		{
			name:         "ListedTypesPackage",
			dependencies: `{"@types/typed": "1"}`,
			src:          "import \"typed\" as t\nval v: string = t.version\n",
			wantErrs:     []string{},
		},
		{
			name:         "Subpath",
			dependencies: `{"plain": "1"}`,
			src:          "import \"plain/sub\" as s\nval v: string = s.version\n",
			wantErrs:     []string{},
		},
		{
			name:         "ScopedTypesPackage",
			dependencies: `{"@types/scope__pkg": "1"}`,
			src:          "import \"@scope/pkg\" as pkg\nval v: string = pkg.version\n",
			wantErrs:     []string{},
		},
		{
			name:     "Unlisted",
			src:      "import \"plain\" as p\nval v: string = p.version\n",
			wantErrs: []string{`"plain" is not listed in package.json dependencies`, "Unknown identifier: p"},
		},
		{
			name:     "UnlistedSubpath",
			src:      "import \"plain/sub\" as s\nval v: string = s.version\n",
			wantErrs: []string{`"plain" is not listed in package.json dependencies`, "Unknown identifier: s"},
		},
		{
			name:            "DevDependency",
			devDependencies: `{"plain": "1"}`,
			src:             "import \"plain\" as p\nval v: string = p.version\n",
			wantErrs:        []string{`"plain" is not listed in package.json dependencies`, "Unknown identifier: p"},
		},
		{
			name:  "NoDir",
			src:   "import \"plain\" as p\nval v: string = p.version\n",
			noDir: true,
			wantErrs: []string{
				`cannot resolve import "plain": unrecognized scheme in "plain"`,
				"Unknown identifier: p",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			for path, contents := range map[string]string{
				"node_modules/plain/index.d.ts":             "export declare const version: string;\n",
				"node_modules/plain/sub.d.ts":               "export declare const version: string;\n",
				"node_modules/@types/typed/index.d.ts":      "export declare const version: string;\n",
				"node_modules/@types/scope__pkg/index.d.ts": "export declare const version: string;\n",
				"package.json": fmt.Sprintf(`{"dependencies": %s, "devDependencies": %s}`,
					orEmptyObject(test.dependencies), orEmptyObject(test.devDependencies)),
			} {
				full := filepath.Join(dir, filepath.FromSlash(path))
				require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
				require.NoError(t, os.WriteFile(full, []byte(contents), 0o644))
			}
			config, err := ReadPackageConfig(dir)
			require.NoError(t, err)
			if test.noDir {
				config = PackageConfig{}
			}
			out := CheckPackage([]*ast.Source{{ID: 1, Path: "lib/index.esc", Contents: test.src}}, config)
			require.Empty(t, out.ParseErrors)
			require.Equal(t, test.wantErrs, messages(out.TypeErrors))
		})
	}
}

// orEmptyObject returns field, or `{}` when field is empty.
func orEmptyObject(field string) string {
	if field == "" {
		return "{}"
	}
	return field
}
