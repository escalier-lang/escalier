package dts_to_esc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// writeEsc seeds a committed `.esc` file under root and returns its
// path.
func writeEsc(t *testing.T, root, rel, contents string) string {
	t.Helper()
	dest := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(dest), 0o755))
	require.NoError(t, os.WriteFile(dest, []byte(contents), 0o644))
	return dest
}

// escParses reports whether a committed `.esc` file parses with
// Escalier's own parser.
func escParses(t *testing.T, path, contents string) bool {
	t.Helper()
	_, errs := parser.ParseLibFiles(context.Background(), []*ast.Source{{
		Path: path, Contents: contents,
	}})
	return len(errs) == 0
}

// partitionOf routes one synthetic lib file through the partitioner.
func partitionOf(t *testing.T, name, src string) *PartitionResult {
	t.Helper()
	res, err := PartitionLib([]LibInput{parseLib(t, name, src)})
	require.NoError(t, err)
	return res
}

// findDiff returns the report entry for one package URI.
func findDiff(t *testing.T, report *CheckReport, uri string) PackageDiff {
	t.Helper()
	for _, p := range report.Packages {
		if p.Pkg == uri {
			return p
		}
	}
	t.Fatalf("no report entry for %s", uri)
	return PackageDiff{}
}

func TestCheckPartition_MissingFileReportsEveryDecl(t *testing.T) {
	t.Parallel()
	res := partitionOf(t, "lib.es5.d.ts", `
interface Array<T> { length: number; }
interface ArrayConstructor { new <T>(): Array<T>; isArray(arg: any): boolean; readonly prototype: Array<any>; }
declare var Array: ArrayConstructor;
`)
	report, err := CheckPartition(res, t.TempDir())
	require.NoError(t, err)
	require.True(t, report.Failed())

	diff := findDiff(t, report, "std:array")
	require.False(t, diff.Exists)
	require.Equal(t, "std/array.esc", diff.Path)
	require.Len(t, diff.NewDecls, 1)
	require.Equal(t, "Array", diff.NewDecls[0].Name)
	require.Equal(t, "class", diff.NewDecls[0].Kind)
}

func TestCheckPartition_PassesWhenCommittedTreeMatches(t *testing.T) {
	t.Parallel()
	res := partitionOf(t, "lib.es5.d.ts", `
interface Array<T> { length: number; }
interface ArrayConstructor { new <T>(): Array<T>; isArray(arg: any): boolean; readonly prototype: Array<any>; }
declare var Array: ArrayConstructor;
`)
	root := t.TempDir()

	// Seed the tree from the converter itself, then re-run the check
	// against it: a freshly written tree has nothing missing.
	_, err := WritePartitionedTree(res, root)
	require.NoError(t, err)

	report, err := CheckPartition(res, root)
	require.NoError(t, err)
	require.False(t, report.Failed())

	decls, removed := report.Counts()
	require.Equal(t, 0, decls)
	require.Equal(t, 0, removed)
}

func TestCheckPartition_ExemptsDroppedDeclarations(t *testing.T) {
	t.Parallel()
	// §6.4 check 1 exempts the declarations the converter drops on
	// purpose: `globalThis`, `eval`, and the `intrinsic`-typed aliases.
	// The committed tree below holds none of the three, and the check
	// still passes.
	res := partitionOf(t, "lib.es5.d.ts", `
declare var globalThis: any;
declare function eval(x: string): any;
type Uppercase<S extends string> = intrinsic;
interface Math { readonly PI: number; }
declare var Math: Math;
`)
	root := t.TempDir()
	writeEsc(t, root, "std/math.esc", `@js("Math.PI")
export declare val PI: number
`)

	report, err := CheckPartition(res, root)
	require.NoError(t, err)
	require.False(t, report.Failed(), "dropped declarations must not fail the check")

	// The exemption is the routing pass's own decision — every one of
	// the three names comes back as a drop from Route, so the check
	// reads no second copy of the list.
	names := make([]string, 0, len(res.Drops))
	for _, d := range res.Drops {
		names = append(names, d.Name)
	}
	require.ElementsMatch(t, []string{"globalThis", "eval", "Uppercase"}, names)
	require.NotContains(t, res.Buckets, "std:string")
}

func TestCheckPartition_ReportsExtraDecls(t *testing.T) {
	t.Parallel()
	res := partitionOf(t, "lib.es5.d.ts", `
interface Array<T> { length: number; }
interface ArrayConstructor { new <T>(): Array<T>; readonly prototype: Array<any>; }
declare var Array: ArrayConstructor;
`)
	root := t.TempDir()
	writeEsc(t, root, "std/array.esc", `@js("Array")
export declare class Array<T> {
    length: number,
}

export type MyArrayHelper<T> = Array<T>
`)

	report, err := CheckPartition(res, root)
	require.NoError(t, err)

	diff := findDiff(t, report, "std:array")
	require.True(t, diff.Exists)
	require.Empty(t, diff.NewDecls)
	require.Equal(t, []string{"MyArrayHelper"}, diff.Removed)

	// An extra declaration is informational, so it does not fail CI.
	require.False(t, report.Failed())
}

func TestCheckReport_WriteNamesTheUnimplementedDriftChecks(t *testing.T) {
	t.Parallel()
	res := partitionOf(t, "lib.es5.d.ts", `
interface Array<T> { length: number; }
interface ArrayConstructor { new <T>(): Array<T>; readonly prototype: Array<any>; }
declare var Array: ArrayConstructor;
`)
	report, err := CheckPartition(res, t.TempDir())
	require.NoError(t, err)

	var sb strings.Builder
	require.NoError(t, report.Write(&sb))
	out := sb.String()
	require.Contains(t, out, "std:array (std/array.esc)")
	require.Contains(t, out, "missing file")
	require.Contains(t, out, "missing declaration: Array (class)")
	require.Contains(t, out, "check: 1 missing declarations, 0 extra declarations")
	require.Contains(t, out,
		"note: signature and property-type drift are not checked yet; "+
			"those compare both sides through the solver's constrain "+
			"(SimpleSub M7.5)")
}

func TestCheckPartition_HandFusedTrioIsSticky(t *testing.T) {
	t.Parallel()
	// The converter emits the class-via-trio idiom as separate
	// declarations when trio detection does not fire: `interface Foo`
	// in the type space and `declare var Foo` in the value space. A §7
	// contributor may fuse the pair into one class, which binds both
	// spaces. Neither converted half may then be reported missing.
	res := partitionOf(t, "lib.es5.d.ts", `
interface WeakRef<T extends object> { deref(): T | undefined; }
interface WeakRefConstructor { readonly prototype: WeakRef<object>; }
declare var WeakRef: WeakRefConstructor;
`)
	mod, err := ConvertBucket(res.Buckets["std:weak_ref"])
	require.NoError(t, err)
	rendered, err := RenderStandaloneModule(mod)
	require.NoError(t, err)
	require.Contains(t, rendered, "interface WeakRef<T: {}>",
		"this input must reach the diff as an unfused interface/var pair")
	require.Contains(t, rendered, "var WeakRef: WeakRefConstructor")

	root := t.TempDir()
	writeEsc(t, root, "std/weak_ref.esc", `@js("WeakRef")
export declare class WeakRef<T: {}> {
    deref(self) -> T | undefined,
}

export declare interface WeakRefConstructor {
    readonly prototype: WeakRef<{}>,
}
`)

	report, err := CheckPartition(res, root)
	require.NoError(t, err)
	diff := findDiff(t, report, "std:weak_ref")
	require.Empty(t, diff.NewDecls, "the fused class covers both converted halves")
	require.False(t, report.Failed())
}

// TestCheckPartition_LibES5RoundTrips is the §6.4 idempotence gate on
// real input: write the pinned lib.es5 partition, then check the
// converter against what it just wrote. Nothing may come back missing.
//
// Packages whose output does not reparse are excluded — those are the
// printer/parser asymmetries §7's hand-edits close, tracked by the soft
// gate in TestPartitionLib_LibES5_EndToEnd. The check itself treats an
// unparseable committed file as a hard error, so including them here
// would test the §7 backlog rather than the diff.
func TestCheckPartition_LibES5RoundTrips(t *testing.T) {
	t.Parallel()

	libPath := filepath.Join("..", "..", "playground", "public", "types", "lib.es5.d.ts")
	if _, err := os.Stat(libPath); err != nil {
		t.Skipf("lib.es5.d.ts not present at %s: %v", libPath, err)
	}

	inputs, err := ParseLibFiles(filepath.Dir(libPath), []string{"lib.es5.d.ts"})
	require.NoError(t, err)
	res, err := PartitionLib(inputs)
	require.NoError(t, err)

	root := t.TempDir()
	written, err := WritePartitionedTree(res, root)
	require.NoError(t, err)

	reparsed := &PartitionResult{Buckets: map[string][]dts_parser.Statement{}}
	for _, uri := range written {
		pkg, ok := PackageForURI(uri)
		require.True(t, ok, "URI %q from result must be a known package", uri)
		path := filepath.Join(root, filepath.FromSlash(pkg.File))
		contents, err := os.ReadFile(path)
		require.NoError(t, err)
		if escParses(t, path, string(contents)) {
			reparsed.Buckets[uri] = res.Buckets[uri]
		}
	}
	require.NotEmpty(t, reparsed.Buckets, "at least one package must reparse")

	report, err := CheckPartition(reparsed, root)
	require.NoError(t, err)

	var sb strings.Builder
	require.NoError(t, report.Write(&sb))
	require.False(t, report.Failed(),
		"a freshly written tree must have nothing missing:\n%s", sb.String())
	t.Logf("lib.es5 check: %d of %d packages round-tripped",
		len(reparsed.Buckets), len(written))
}
