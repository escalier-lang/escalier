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
	"github.com/gkampitakis/go-snaps/snaps"
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

// readEsc reads a regenerated `.esc` file back and asserts it still
// parses — a splice that produced unparseable source is a failure
// however good the diff looked.
func readEsc(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, escParses(t, path, string(contents)),
		"regenerated %s should parse", path)
	return string(contents)
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

	decls, members, removed := report.Counts()
	require.Equal(t, 0, decls)
	require.Equal(t, 0, members)
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
    constructor(mut self),
    static readonly prototype: Array<any>,
}

export type MyArrayHelper<T> = Array<T>
`)

	report, err := CheckPartition(res, root)
	require.NoError(t, err)

	// Nothing is missing on either side; the one finding is the `.esc`
	// declaration the `.d.ts` has no counterpart for, which the report
	// names and leaves in place.
	var sb strings.Builder
	require.NoError(t, report.Write(&sb))
	snaps.MatchInlineSnapshot(t, sb.String(), snaps.Inline(`std:array (std/array.esc)
  extra declaration: MyArrayHelper (absent from the .d.ts; not removed)
check: 0 missing declarations, 0 missing members, 1 extra declarations
note: signature and property-type drift are not checked yet; those compare both sides through the solver's constrain (SimpleSub M7.5)
`))

	// An extra declaration is informational, so it does not fail CI.
	// That verdict is the one fact the rendered report does not carry.
	require.False(t, report.Failed())
}

func TestCheckPartition_ReportsMissingMembers(t *testing.T) {
	t.Parallel()
	res := partitionOf(t, "lib.es5.d.ts", `
interface Array<T> { length: number; indexOf(searchElement: T): number; }
interface ArrayConstructor { new <T>(): Array<T>; isArray(arg: any): boolean; readonly prototype: Array<any>; }
declare var Array: ArrayConstructor;
`)
	root := t.TempDir()
	writeEsc(t, root, "std/array.esc", `@js("Array")
export declare class Array<T> {
    length: number,
}
`)

	report, err := CheckPartition(res, root)
	require.NoError(t, err)
	require.True(t, report.Failed())

	// The rendered report carries every fact this test is about: no
	// declaration is missing because the class itself is committed, and
	// each of the four members the `.d.ts` adds is named, static ones
	// marked as such. Members are listed in the order the converted
	// class declares them.
	var sb strings.Builder
	require.NoError(t, report.Write(&sb))
	snaps.MatchInlineSnapshot(t, sb.String(), snaps.Inline(`std:array (std/array.esc)
  missing member: Array.indexOf
  missing member: Array.constructor
  missing member: Array.isArray (static)
  missing member: Array.prototype (static)
check: 0 missing declarations, 4 missing members, 0 extra declarations
note: signature and property-type drift are not checked yet; those compare both sides through the solver's constrain (SimpleSub M7.5)
`))
}

func TestCheckPartition_AHandWrittenGetterFillsTheFieldSlot(t *testing.T) {
	t.Parallel()
	// A §7 hand-edit that turns a converted `readonly` field into a
	// getter is a refinement, not a gap: the check must leave it alone
	// rather than reporting the field missing beside it.
	res := partitionOf(t, "lib.es5.d.ts", `
interface Array<T> { readonly length: number; }
interface ArrayConstructor { new <T>(): Array<T>; readonly prototype: Array<any>; }
declare var Array: ArrayConstructor;
`)
	root := t.TempDir()
	writeEsc(t, root, "std/array.esc", `@js("Array")
export declare class Array<T> {
    get length(self) -> number,
    constructor(mut self),
    static readonly prototype: Array<any>,
}
`)

	report, err := CheckPartition(res, root)
	require.NoError(t, err)
	require.False(t, report.Failed())
}

// The report an operator reads, over a tree with nothing in it. The
// snapshot holds all of it, so the section heading, the per-finding
// indentation, and the summary line are pinned alongside the footer.
//
// That footer is what the test is named for. A green check means every
// `.d.ts` name has an `.esc` counterpart, not that the counterpart still
// means the same thing, and every run says so.
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
	snaps.MatchInlineSnapshot(t, sb.String(), snaps.Inline(`std:array (std/array.esc)
  missing file
  missing declaration: Array (class)
check: 1 missing declarations, 0 missing members, 0 extra declarations
note: signature and property-type drift are not checked yet; those compare both sides through the solver's constrain (SimpleSub M7.5)
`))
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

	// The precondition, shown rather than probed for. This input has to
	// reach the diff as an unfused interface/var pair. Were trio
	// detection to start firing on it, the snapshot would show one class
	// and the rest of the test would stop exercising the fusion.
	snaps.MatchInlineSnapshot(t, rendered, snaps.Inline(`export declare interface WeakRef<T: {}> {
    deref() -> T | undefined
}

export declare interface WeakRefConstructor {
    readonly prototype: WeakRef<{}>
}

@js("WeakRef")
export declare var WeakRef: WeakRefConstructor
`))

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
	require.Empty(t, diff.NewMembers)
	require.False(t, report.Failed())
}

func TestRegeneratePartition_CreatesMissingFile(t *testing.T) {
	t.Parallel()
	res := partitionOf(t, "lib.es5.d.ts", `
interface Array<T> { length: number; }
interface ArrayConstructor { new <T>(): Array<T>; isArray(arg: any): boolean; readonly prototype: Array<any>; }
declare var Array: ArrayConstructor;
`)
	root := t.TempDir()

	report, err := RegeneratePartition(res, root)
	require.NoError(t, err)
	require.Len(t, report.Packages, 1)
	require.True(t, report.Packages[0].Created)
	require.Equal(t, 1, report.Packages[0].AddedDecls)

	contents := readEsc(t, filepath.Join(root, "std", "array.esc"))
	require.Contains(t, contents, "class Array<T>")

	// A second run has nothing left to add.
	after, err := CheckPartition(res, root)
	require.NoError(t, err)
	require.False(t, after.Failed())
}

func TestRegeneratePartition_KeepsHandEditsAndAddsMembers(t *testing.T) {
	t.Parallel()
	res := partitionOf(t, "lib.es5.d.ts", `
interface JSON { parse(text: string): any; stringify(value: any): string; }
declare var JSON: JSON;
`)
	root := t.TempDir()
	// The committed file carries §7-style hand-edits on `parse`: a
	// `throws` clause, a return type narrowed from `any` to `unknown`,
	// and a doc comment the converter never produced. None of them may
	// be rewritten.
	committed := `/** Hand-written note that must survive the re-run. */
@js("JSON.parse")
export declare fn parse(text: string) -> unknown throws SyntaxError
`
	path := writeEsc(t, root, "std/json.esc", committed)

	report, err := RegeneratePartition(res, root)
	require.NoError(t, err)
	require.Len(t, report.Packages, 1)
	require.False(t, report.Packages[0].Created)
	require.Equal(t, 1, report.Packages[0].AddedDecls)

	contents := readEsc(t, path)
	require.True(t, strings.HasPrefix(contents, committed),
		"the hand-edited declaration must survive byte-for-byte")
	require.Contains(t, contents, "fn stringify(value: any) -> string")
	require.Equal(t, 1, strings.Count(contents, "fn parse"),
		"the committed parse signature must not be duplicated")
}

func TestRegeneratePartition_AddsMembersToAnEmptyBody(t *testing.T) {
	t.Parallel()
	res := partitionOf(t, "lib.es5.d.ts", `
interface Array<T> { length: number; }
interface ArrayConstructor { new <T>(): Array<T>; isArray(arg: any): boolean; readonly prototype: Array<any>; }
declare var Array: ArrayConstructor;
`)
	root := t.TempDir()
	path := writeEsc(t, root, "std/array.esc", `@js("Array")
export declare class Array<T> {}
`)

	report, err := RegeneratePartition(res, root)
	require.NoError(t, err)
	require.Equal(t, 0, report.Packages[0].AddedDecls)
	require.Equal(t, 4, report.Packages[0].AddedMembers)
	require.Empty(t, report.Packages[0].Skipped)

	contents := readEsc(t, path)
	require.Contains(t, contents, "length: number,")
	require.Contains(t, contents, "static isArray")

	after, err := CheckPartition(res, root)
	require.NoError(t, err)
	require.False(t, after.Failed())
}

func TestRegeneratePartition_AddsMissingDeclAfterExistingOnes(t *testing.T) {
	t.Parallel()
	res := partitionOf(t, "lib.es5.d.ts", `
interface Number { toFixed(digits?: number): string; }
interface NumberConstructor { new (value?: any): Number; readonly prototype: Number; }
declare var Number: NumberConstructor;
declare function parseInt(string: string, radix?: number): number;
`)
	root := t.TempDir()
	path := writeEsc(t, root, "std/number.esc", `@js("Number")
export declare class Number {
    toFixed(self, digits?: number) -> string,
}
`)

	report, err := RegeneratePartition(res, root)
	require.NoError(t, err)
	require.Equal(t, 1, report.Packages[0].AddedDecls)

	contents := readEsc(t, path)
	require.True(t, strings.HasPrefix(contents, `@js("Number")`))
	require.Contains(t, contents, "parseInt")
	require.Contains(t, contents, "}\n\n@js(\"parseInt\")")
}

func TestRegeneratePartition_LeavesMatchingFileUntouched(t *testing.T) {
	t.Parallel()
	res := partitionOf(t, "lib.es5.d.ts", `
interface Array<T> { length: number; }
interface ArrayConstructor { new <T>(): Array<T>; readonly prototype: Array<any>; }
declare var Array: ArrayConstructor;
`)
	root := t.TempDir()
	_, err := WritePartitionedTree(res, root)
	require.NoError(t, err)

	path := filepath.Join(root, "std", "array.esc")
	before, err := os.Stat(path)
	require.NoError(t, err)
	original := readEsc(t, path)

	report, err := RegeneratePartition(res, root)
	require.NoError(t, err)
	require.Equal(t, 0, report.Packages[0].AddedDecls)
	require.Equal(t, 0, report.Packages[0].AddedMembers)

	after, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, before.ModTime(), after.ModTime(),
		"a package with nothing missing should not be rewritten")
	require.Equal(t, original, readEsc(t, path))
}

func TestRegeneratePartition_NeverDeletesExtraDecls(t *testing.T) {
	t.Parallel()
	res := partitionOf(t, "lib.es5.d.ts", `
interface Array<T> { length: number; }
interface ArrayConstructor { new <T>(): Array<T>; readonly prototype: Array<any>; }
declare var Array: ArrayConstructor;
`)
	root := t.TempDir()
	path := writeEsc(t, root, "std/array.esc", `@js("Array")
export declare class Array<T> {
    length: number,
    constructor(mut self),
}

export type RemovedUpstream = number
`)

	report, err := RegeneratePartition(res, root)
	require.NoError(t, err)
	require.Equal(t, []string{"RemovedUpstream"}, report.Packages[0].Removed)

	contents := readEsc(t, path)
	require.Contains(t, contents, "export type RemovedUpstream = number")

	var sb strings.Builder
	require.NoError(t, report.Write(&sb))
	require.Contains(t, sb.String(),
		"RemovedUpstream is absent from the .d.ts; left in place for review")
}

func TestOffsetOf_CountsRunesNotBytes(t *testing.T) {
	t.Parallel()
	// Columns count code points, matching the lexer, so a multi-byte
	// character earlier on the line must not shift the offset.
	contents := "val π = 1\nval x = 2\n"
	require.Equal(t, 0, offsetOf(contents, ast.Location{Line: 1, Column: 1}))
	require.Equal(t, len("val π = 1\n"), offsetOf(contents, ast.Location{Line: 2, Column: 1}))
	require.Equal(t, len(contents), offsetOf(contents, ast.Location{Line: 9, Column: 1}))
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

	// The write mode agrees: a re-run over the same tree adds nothing.
	regen, err := RegeneratePartition(reparsed, root)
	require.NoError(t, err)
	for _, p := range regen.Packages {
		require.Equal(t, 0, p.AddedDecls, "%s should need no new declarations", p.Pkg)
		require.Equal(t, 0, p.AddedMembers, "%s should need no new members", p.Pkg)
		require.Empty(t, p.Skipped, "%s should have no unlocatable bodies", p.Pkg)
	}
	t.Logf("lib.es5 check: %d of %d packages round-tripped",
		len(reparsed.Buckets), len(written))
}
