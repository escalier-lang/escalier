package dts_to_esc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// seedOverlay writes an overlay tree from a map of overlay-relative
// paths to file contents, and returns the root it wrote them under.
func seedOverlay(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, contents := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	}
	return dir
}

// TestLoadOverlay_ReadsOperationAndPackageFromTheFilename covers the
// naming rule the overlay rests on: the operation and the target package
// are both read off the path, and the file itself is ordinary `.esc`.
func TestLoadOverlay_ReadsOperationAndPackageFromTheFilename(t *testing.T) {
	t.Parallel()
	dir := seedOverlay(t, map[string]string{
		"README.md":             "not an overlay file\n",
		"drop.esc":              "export declare val eval\n",
		"std/symbol.add.esc":    "export declare interface SymbolConstructor {\n    readonly customMatcher: unique symbol,\n}\n",
		"std/array.replace.esc": "export declare interface Array<T> {\n    length: number,\n}\n",
		"std/date.drop.esc":     "export declare interface Date {\n    getYear: unknown,\n}\n",
	})

	overlay, err := LoadOverlay(dir)
	require.NoError(t, err)

	type entry struct {
		Path   string
		Op     OverlayOp
		PkgURI string
		Decls  int
	}
	got := make([]entry, 0, len(overlay.Files))
	for _, f := range overlay.Files {
		got = append(got, entry{Path: f.Path, Op: f.Op, PkgURI: f.PkgURI, Decls: len(f.Decls)})
	}
	require.Equal(t, []entry{
		{Path: "drop.esc", Op: OverlayDrop, PkgURI: "", Decls: 1},
		{Path: "std/array.replace.esc", Op: OverlayReplace, PkgURI: "std:array", Decls: 1},
		{Path: "std/date.drop.esc", Op: OverlayDrop, PkgURI: "std:date", Decls: 1},
		{Path: "std/symbol.add.esc", Op: OverlayAdd, PkgURI: "std:symbol", Decls: 1},
	}, got)

	require.Equal(t, []string{"eval"}, overlay.GlobalDrops().ToSlice())
	require.Equal(t, []string{"std:array", "std:date", "std:symbol"}, overlay.PackageURIs())
}

// TestLoadOverlay_Rejects covers every way a committed overlay file can
// be wrong. An overlay is an input the generator parses, so each of
// these stops the run rather than being skipped.
func TestLoadOverlay_Rejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name:  "file at the overlay root that is not the root drop file",
			files: map[string]string{"symbol.add.esc": "export declare val x\n"},
			want: "overlay: symbol.add.esc sits at neither the overlay root nor a " +
				"package directory; write a package operation as " +
				"<scheme>/<package>.<add|replace|drop>.esc and package-less drops as drop.esc",
		},
		{
			name:  "no operation in the filename",
			files: map[string]string{"std/symbol.esc": "export declare val x\n"},
			want: "overlay: std/symbol.esc names no operation; the filename carries it, " +
				"as in std/symbol.replace.esc",
		},
		{
			name:  "unknown operation",
			files: map[string]string{"std/symbol.merge.esc": "export declare val x\n"},
			want: "overlay: std/symbol.merge.esc names the unknown operation \"merge\"; " +
				"the operations are add, replace, and drop",
		},
		{
			name:  "package not in the partition table",
			files: map[string]string{"std/nonesuch.add.esc": "export declare val x\n"},
			want: "overlay: std/nonesuch.add.esc targets std/nonesuch.esc, which is not a " +
				"package in the partition table (see internal/dts_to_esc/partition.go)",
		},
		{
			name:  "file that does not parse",
			files: map[string]string{"std/symbol.add.esc": "export declare fn (\n"},
			want:  "overlay: std/symbol.add.esc does not parse: Expected identifier; Expected a pattern; Expected a closing paren",
		},
		{
			name:  "drop entry carrying a type annotation",
			files: map[string]string{"drop.esc": "export declare val eval: unknown\n"},
			want: "overlay: drop.esc gives eval a type annotation, which a drop ignores; " +
				"write `export declare val <name>`",
		},
		{
			name:  "drop entry carrying a signature",
			files: map[string]string{"std/date.drop.esc": "export declare fn getYear() -> number\n"},
			want: "overlay: std/date.drop.esc drops the function getYear; write a whole " +
				"declaration as `export declare val <name>` and its members as " +
				"`export declare interface <name> {\n    <member>: unknown,\n}`",
		},
		{
			name: "member drop carrying a real type",
			files: map[string]string{
				"std/date.drop.esc": "export declare interface Date {\n    getYear: number,\n}\n",
			},
			want: "overlay: std/date.drop.esc types Date.getYear, which a drop ignores; " +
				"write `export declare interface <name> {\n    <member>: unknown,\n}`",
		},
		{
			name: "member drop with an empty body",
			files: map[string]string{
				"std/date.drop.esc": "export declare interface Date {}\n",
			},
			want: "overlay: std/date.drop.esc drops Date with an empty body; a drop naming " +
				"a whole declaration is written `export declare val <name>`",
		},
		{
			name: "member drop in the root drop file",
			files: map[string]string{
				"drop.esc": "export declare interface Date {\n    getYear: unknown,\n}\n",
			},
			want: "overlay: drop.esc drops members of Date, but the root drop file names " +
				"whole symbols that belong to no package; move it to a " +
				"<scheme>/<package>.drop.esc file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadOverlay(seedOverlay(t, tt.files))
			require.EqualError(t, err, tt.want)
		})
	}
}

// TestLoadOverlay_MissingDirectory names the path rather than returning
// an empty overlay. A typo in the path would otherwise produce a tree
// with every overlay entry silently missing.
func TestLoadOverlay_MissingDirectory(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nonesuch")
	_, err := LoadOverlay(dir)
	require.EqualError(t, err,
		"reading overlay dir "+dir+": stat "+dir+": no such file or directory")
}
