package interop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

func parseLib(t *testing.T, name, src string) LibInput {
	t.Helper()
	source := &ast.Source{Path: name, Contents: src}
	mod, errs := dts_parser.NewDtsParser(source).ParseModule()
	require.Empty(t, errs, "parse %s", name)
	return LibInput{SourceFile: name, Module: mod}
}

func TestPartitionLib_RoutesByName(t *testing.T) {
	t.Parallel()
	// Array → std:array (explicit map). HTMLCanvasElement → web:dom
	// (DOM residual via lib.dom.d.ts source). Request → web:fetch
	// (explicit standalone sibling, even though declared in lib.dom.d.ts).
	es5 := parseLib(t, "lib.es5.d.ts", `
interface Array<T> { length: number; }
interface ArrayConstructor { new <T>(): Array<T>; readonly prototype: Array<any>; }
declare var Array: ArrayConstructor;
`)
	dom := parseLib(t, "lib.dom.d.ts", `
interface HTMLCanvasElement { width: number; }
interface HTMLCanvasElementConstructor { new(): HTMLCanvasElement; readonly prototype: HTMLCanvasElement; }
declare var HTMLCanvasElement: HTMLCanvasElementConstructor;
interface Request { method: string; }
interface RequestConstructor { new(input: string): Request; readonly prototype: Request; }
declare var Request: RequestConstructor;
`)

	res, err := PartitionLib([]LibInput{es5, dom})
	require.NoError(t, err)

	// 3 buckets: std:array, web:dom, web:fetch.
	require.Len(t, res.Buckets, 3)
	require.Contains(t, res.Buckets, "std:array")
	require.Contains(t, res.Buckets, "web:dom")
	require.Contains(t, res.Buckets, "web:fetch")

	// std:array bucket has the Array trio (3 statements).
	require.Len(t, res.Buckets["std:array"], 3)

	// web:dom bucket has the HTMLCanvasElement trio only (3),
	// because Request is pinned to web:fetch.
	require.Len(t, res.Buckets["web:dom"], 3)
	for _, stmt := range res.Buckets["web:dom"] {
		name := topLevelName(stmt)
		require.NotEqual(t, "Request", name)
		require.NotEqual(t, "RequestConstructor", name)
	}

	require.Len(t, res.Buckets["web:fetch"], 3)
}

func TestPartitionLib_ExplicitDropsAreRecorded(t *testing.T) {
	t.Parallel()
	lib := parseLib(t, "lib.es5.d.ts", `
declare var globalThis: any;
declare function eval(x: string): any;
declare var Array: ArrayConstructor;
interface ArrayConstructor { new(): any; readonly prototype: any; }
interface Array<T> { length: number; }
`)
	res, err := PartitionLib([]LibInput{lib})
	require.NoError(t, err)

	require.Len(t, res.Drops, 2)
	dropped := set.NewSet[string]()
	for _, d := range res.Drops {
		dropped.Add(d.Name)
		require.Equal(t, "lib.es5.d.ts", d.SourceFile)
	}
	require.True(t, dropped.Contains("globalThis"))
	require.True(t, dropped.Contains("eval"))

	// Bucket should not include the dropped names.
	for _, stmt := range res.Buckets["std:array"] {
		name := topLevelName(stmt)
		require.NotEqual(t, "globalThis", name)
		require.NotEqual(t, "eval", name)
	}
}

func TestPartitionLib_UnmappedTripsFailSafe(t *testing.T) {
	t.Parallel()
	lib := parseLib(t, "lib.es2099.weirdness.d.ts", `
declare var TotallyNewSymbol: number;
`)
	_, err := PartitionLib([]LibInput{lib})
	require.Error(t, err)
	require.EqualError(t, err, UnmappedError("TotallyNewSymbol", "lib.es2099.weirdness.d.ts").Error())
}

func TestMergeDecls_InterfaceMembersConcatenate(t *testing.T) {
	t.Parallel()
	// Same-name interfaces in different lib files should fuse into
	// one before trio detection runs — otherwise the second `interface
	// Array<T>` would compete with the first for trio matching.
	a := parseLib(t, "lib.es5.d.ts", `
interface Array<T> { length: number; }
`)
	b := parseLib(t, "lib.es2015.iterable.d.ts", `
interface Array<T> { values(): IterableIterator<T>; }
`)
	merged := mergeDecls(append(
		append([]dts_parser.Statement{}, a.Module.Statements...),
		b.Module.Statements...))

	require.Len(t, merged, 1)
	iface := merged[0].(*dts_parser.InterfaceDecl)
	require.Equal(t, "Array", iface.Name.Name)
	require.Len(t, iface.Members, 2)
}

func TestMergeDecls_NamespacesConcatenate(t *testing.T) {
	t.Parallel()
	a := parseLib(t, "a.d.ts", `
declare namespace Math {
    function abs(x: number): number;
}
`)
	b := parseLib(t, "b.d.ts", `
declare namespace Math {
    function ceil(x: number): number;
}
`)
	merged := mergeDecls(append(
		append([]dts_parser.Statement{}, a.Module.Statements...),
		b.Module.Statements...))

	require.Len(t, merged, 1)
	ns := merged[0].(*dts_parser.NamespaceDecl)
	require.Equal(t, "Math", ns.Name.Name)
	require.Len(t, ns.Statements, 2)
}

func TestWritePartitionedTree_WritesToExpectedPaths(t *testing.T) {
	t.Parallel()
	es5 := parseLib(t, "lib.es5.d.ts", `
interface Array<T> { length: number; }
interface ArrayConstructor { new <T>(): Array<T>; readonly prototype: Array<any>; }
declare var Array: ArrayConstructor;
declare namespace Math {
    const PI: number;
    function abs(x: number): number;
}
`)
	res, err := PartitionLib([]LibInput{es5})
	require.NoError(t, err)

	outDir := t.TempDir()
	written, err := WritePartitionedTree(res, outDir)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"std:array", "std:math"}, written)

	arrayPath := filepath.Join(outDir, "std", "array.esc")
	mathPath := filepath.Join(outDir, "std", "math.esc")
	require.FileExists(t, arrayPath)
	require.FileExists(t, mathPath)

	arr, err := os.ReadFile(arrayPath)
	require.NoError(t, err)
	require.Contains(t, string(arr), `@js("Array")`)
	require.Contains(t, string(arr), "class Array<T>")

	mth, err := os.ReadFile(mathPath)
	require.NoError(t, err)
	// Namespace flattening lowers Math.abs to a top-level fn carrying
	// @js("Math.abs"). Math.PI is a `const` so the var binding decays
	// to a `val` with @js("Math.PI").
	require.Contains(t, string(mth), `@js("Math.abs")`)
	require.Contains(t, string(mth), `@js("Math.PI")`)
}

func TestScaffoldNodeDir(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()
	require.NoError(t, ScaffoldNodeDir(outDir))
	require.DirExists(t, filepath.Join(outDir, "node"))
	body, err := os.ReadFile(filepath.Join(outDir, "node", "README.md"))
	require.NoError(t, err)
	require.Contains(t, string(body), "node:*")

	// Idempotent — second call doesn't error or clobber.
	require.NoError(t, ScaffoldNodeDir(outDir))
}

func TestDiscoverLibFiles_FiltersAndSorts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{
		"lib.es5.d.ts",
		"lib.es2015.iterable.d.ts",
		"lib.es2018.full.d.ts", // .full.d.ts excluded
		"lib.dom.d.ts",
		"other.d.ts",   // missing lib. prefix
		"lib.es5.d.tx", // wrong suffix
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0o644))
	}
	got, err := DiscoverLibFiles(dir)
	require.NoError(t, err)
	require.Equal(t, []string{
		"lib.dom.d.ts",
		"lib.es2015.iterable.d.ts",
		"lib.es5.d.ts",
	}, got)
}

// TestPartitionLib_CrossLibInterfaceMerge_FullPipeline exercises the
// canonical TS shipping pattern documented on mergeDecls: a single
// interface (`Array<T>`) declared across multiple `lib.*.d.ts` files,
// each layering in new spec revisions. The full PartitionLib +
// ConvertBucket pipeline must collapse them so the trio detector sees
// a single merged interface and emits one class carrying every method
// from every contributing lib year. (The unit-level
// TestMergeDecls_InterfaceMembersConcatenate test below covers the
// `mergeDecls` step in isolation; this test exercises it end-to-end
// through routing, merging, trio fusion, and final emission.)
func TestPartitionLib_CrossLibInterfaceMerge_FullPipeline(t *testing.T) {
	t.Parallel()
	es5 := parseLib(t, "lib.es5.d.ts", `
interface Array<T> {
    push(...items: Array<T>): number;
    pop(): T | undefined;
}
interface ArrayConstructor {
    new <T>(): Array<T>;
    readonly prototype: Array<any>;
}
declare var Array: ArrayConstructor;
`)
	es2015Core := parseLib(t, "lib.es2015.core.d.ts", `
interface Array<T> {
    find(predicate: (value: T) => unknown): T | undefined;
    fill(value: T): this;
}
`)
	es2015Iterable := parseLib(t, "lib.es2015.iterable.d.ts", `
interface Array<T> {
    entries(): IterableIterator<[number, T]>;
    keys(): IterableIterator<number>;
    values(): IterableIterator<T>;
}
`)
	es2023 := parseLib(t, "lib.es2023.array.d.ts", `
interface Array<T> {
    findLast(predicate: (value: T) => unknown): T | undefined;
    toReversed(): Array<T>;
}
`)

	res, err := PartitionLib([]LibInput{es5, es2015Core, es2015Iterable, es2023})
	require.NoError(t, err)
	require.Contains(t, res.Buckets, "std:array")

	mod, err := ConvertBucket(res.Buckets["std:array"])
	require.NoError(t, err)

	rootNS, ok := mod.Module.Namespaces.Get("")
	require.True(t, ok)

	// Trio fusion should produce exactly one ClassDecl, named Array.
	// No surviving `interface Array` / `interface ArrayConstructor`
	// (consumed by the trio), no surviving `declare var Array` (ditto).
	var arrayClass *ast.ClassDecl
	for _, decl := range rootNS.Decls {
		switch d := decl.(type) {
		case *ast.ClassDecl:
			require.Equal(t, "Array", d.Name.Name, "only Array should be a class")
			arrayClass = d
		case *ast.InterfaceDecl:
			require.NotEqual(t, "Array", d.Name.Name)
			require.NotEqual(t, "ArrayConstructor", d.Name.Name)
		case *ast.VarDecl:
			if ip, ok := d.Pattern.(*ast.IdentPat); ok {
				require.NotEqual(t, "Array", ip.Name,
					"declare var Array should be consumed by trio fusion")
			}
		}
	}
	require.NotNil(t, arrayClass, "Array should be trio-fused")

	// Every method declared on any of the four input lib files must
	// be present on the single fused class. This is the load-bearing
	// assertion: it would fail if mergeDecls dropped a same-name
	// interface instead of concatenating its members.
	wantMethods := []string{
		"push", "pop", // lib.es5
		"find", "fill", // lib.es2015.core
		"entries", "keys", "values", // lib.es2015.iterable
		"findLast", "toReversed", // lib.es2023.array
	}
	got := set.NewSet[string]()
	for _, elem := range arrayClass.Body {
		if me, ok := elem.(*ast.MethodElem); ok && !me.Static {
			got.Add(classElemName(me.Name))
		}
	}
	for _, name := range wantMethods {
		require.True(t, got.Contains(name),
			"method %s (from one of the four lib years) must survive into the fused Array class; got %v",
			name, got)
	}
}

func TestConvertBucket_ReadonlyTwinMergeMarksReceivers(t *testing.T) {
	t.Parallel()
	// Mirrors the legacy mergeReadonlyVariant contract: methods on
	// the Readonly twin are positive evidence the method does not
	// mutate, so the merged class's receiver should be `self` for
	// any name shared with ReadonlyMap, and `mut self` for any
	// member unique to the mutable side.
	lib := parseLib(t, "lib.es2015.collection.d.ts", `
interface ReadonlyMap<K, V> {
    forEach(callbackfn: (value: V, key: K) => void): void;
    get(key: K): V | undefined;
    has(key: K): boolean;
    readonly size: number;
}
interface Map<K, V> {
    clear(): void;
    delete(key: K): boolean;
    forEach(callbackfn: (value: V, key: K) => void): void;
    get(key: K): V | undefined;
    has(key: K): boolean;
    set(key: K, value: V): this;
    readonly size: number;
}
interface MapConstructor {
    new <K, V>(entries?: ReadonlyArray<[K, V]> | null): Map<K, V>;
    readonly prototype: Map<any, any>;
}
declare var Map: MapConstructor;
`)
	res, err := PartitionLib([]LibInput{lib})
	require.NoError(t, err)

	mod, err := ConvertBucket(res.Buckets["std:map"])
	require.NoError(t, err)

	rootNS, ok := mod.Module.Namespaces.Get("")
	require.True(t, ok)

	// ReadonlyMap must not survive as an interface — it should
	// have been replaced by a `type ReadonlyMap<K, V> = Map<K, V>`.
	var mapClass *ast.ClassDecl
	var readonlyAlias *ast.TypeDecl
	for _, decl := range rootNS.Decls {
		switch d := decl.(type) {
		case *ast.ClassDecl:
			if d.Name.Name == "Map" {
				mapClass = d
			}
		case *ast.InterfaceDecl:
			require.NotEqual(t, "ReadonlyMap", d.Name.Name,
				"ReadonlyMap must be dropped, not emitted as an interface")
		case *ast.TypeDecl:
			if d.Name.Name == "ReadonlyMap" {
				readonlyAlias = d
			}
		}
	}
	require.NotNil(t, mapClass, "Map should be trio-fused into a class")
	require.NotNil(t, readonlyAlias, "ReadonlyMap should be emitted as a type alias")

	// Receivers: methods on the readonly twin are `self`; others `mut self`.
	wantReceiver := map[string]bool{ // method name → want mut?
		"clear":   true,
		"delete":  true,
		"set":     true,
		"forEach": false,
		"get":     false,
		"has":     false,
	}
	seen := set.NewSet[string]()
	for _, elem := range mapClass.Body {
		me, ok := elem.(*ast.MethodElem)
		if !ok || me.Static {
			continue
		}
		name := classElemName(me.Name)
		want, has := wantReceiver[name]
		if !has {
			continue
		}
		require.NotNil(t, me.Receiver, "method %s should have a receiver", name)
		require.Equal(t, want, me.Receiver.Mut,
			"method %s receiver mut: want %v, got %v", name, want, me.Receiver.Mut)
		seen.Add(name)
	}
	for name := range wantReceiver {
		require.True(t, seen.Contains(name), "method %s should be present on Map", name)
	}

	// Alias's RHS is Map<K, V>.
	rhs, ok := readonlyAlias.TypeAnn.(*ast.TypeRefTypeAnn)
	require.True(t, ok)
	require.Equal(t, "Map", ast.QualIdentToString(rhs.Name))
	require.Len(t, rhs.TypeArgs, 2)
}

func TestConvertBucket_ReadonlyTwinAppendsUniqueMembers(t *testing.T) {
	t.Parallel()
	// Members declared only on the readonly twin should be folded
	// onto the mutable side so they appear on the emitted class.
	lib := parseLib(t, "lib.foo.d.ts", `
interface ReadonlyFoo {
    onlyOnReadonly(): number;
    shared(): string;
}
interface Foo {
    shared(): string;
    mutating(): void;
}
interface FooConstructor {
    new (): Foo;
    readonly prototype: Foo;
}
declare var Foo: FooConstructor;
`)
	// Foo isn't in the partition table; we route Foo+ReadonlyFoo
	// manually for the test by building a bucket directly.
	stmts := lib.Module.Statements
	stmts = mergeDecls(stmts)
	mod, err := ConvertBucket(stmts)
	require.NoError(t, err)

	rootNS, _ := mod.Module.Namespaces.Get("")
	var fooClass *ast.ClassDecl
	for _, decl := range rootNS.Decls {
		if cd, ok := decl.(*ast.ClassDecl); ok && cd.Name.Name == "Foo" {
			fooClass = cd
		}
	}
	require.NotNil(t, fooClass)

	names := set.NewSet[string]()
	for _, elem := range fooClass.Body {
		if me, ok := elem.(*ast.MethodElem); ok && !me.Static {
			names.Add(classElemName(me.Name))
		}
	}
	require.True(t, names.Contains("onlyOnReadonly"),
		"readonly-only members should be folded onto the mutable class")
	require.True(t, names.Contains("shared"))
	require.True(t, names.Contains("mutating"))
}

func TestConvertBucket_ReadonlyTwinRewritesRefs(t *testing.T) {
	t.Parallel()
	// References to the readonly twin should be renamed to the
	// mutable name; references to the mutable name should be wrapped
	// in MutableTypeAnn. Both bare-name and `T[]` (and `readonly T[]`)
	// shorthands flow through the same rewrite because the converter
	// desugars them to `Array<T>` / `ReadonlyArray<T>` first.
	lib := parseLib(t, "lib.es5.d.ts", `
interface ReadonlyArray<T> {
    readonly length: number;
    concat(...items: ConcatArray<T>[]): T[];
}
interface Array<T> {
    length: number;
    push(...items: T[]): number;
    concat(items: ReadonlyArray<T>): T[];
    readArr(items: readonly T[]): void;
}
interface ArrayConstructor {
    new <T>(): Array<T>;
    readonly prototype: Array<any>;
}
declare var Array: ArrayConstructor;
`)
	res, err := PartitionLib([]LibInput{lib})
	require.NoError(t, err)

	mod, err := ConvertBucket(res.Buckets["std:array"])
	require.NoError(t, err)

	rootNS, _ := mod.Module.Namespaces.Get("")
	var arrayClass *ast.ClassDecl
	for _, decl := range rootNS.Decls {
		if cd, ok := decl.(*ast.ClassDecl); ok && cd.Name.Name == "Array" {
			arrayClass = cd
		}
	}
	require.NotNil(t, arrayClass)

	// Per-method assertions are replaced with a single inline snapshot
	// of the printed class so the param/return rewrites are reviewed
	// holistically. The expected output covers:
	//   - push(...items: T[]) → `mut Array<T>` (T[] desugared then wrapped)
	//   - concat(items: ReadonlyArray<T>) → renamed to `Array<T>`;
	//     return `T[]` wrapped to `mut Array<T>`
	//   - readArr(readonly T[]) → desugared then renamed to `Array<T>`
	printed, err := printer.Print(arrayClass, printer.DefaultOptions())
	require.NoError(t, err)
	snaps.MatchInlineSnapshot(t, printed, snaps.Inline(`@js("Array")
export declare class Array<T> {
    length: number,
    push(mut self, ...items: mut Array<T>) -> number,
    concat(self, items: Array<T>) -> mut Array<T>,
    readArr(mut self, items: Array<T>) -> void,
    constructor(mut self),
    static readonly prototype: mut Array<any>
}`))

	// The synthesised `type ReadonlyArray<T> = Array<T>` alias's RHS
	// must remain a bare TypeRef — the rewrite pass runs before
	// appendReadonlyAliases, so the alias is appended after the
	// wrapping pass and its `Array<T>` reference stays unwrapped.
	var alias *ast.TypeDecl
	for _, decl := range rootNS.Decls {
		if td, ok := decl.(*ast.TypeDecl); ok && td.Name.Name == "ReadonlyArray" {
			alias = td
		}
	}
	require.NotNil(t, alias)
	rhs, ok := alias.TypeAnn.(*ast.TypeRefTypeAnn)
	require.True(t, ok, "ReadonlyArray alias RHS should be a bare TypeRef, got %T", alias.TypeAnn)
	require.Equal(t, "Array", ast.QualIdentToString(rhs.Name))
}

func TestReportPartition_FormatsSortedSummary(t *testing.T) {
	t.Parallel()
	res := &PartitionResult{
		Buckets: map[string][]dts_parser.Statement{
			"std:array": make([]dts_parser.Statement, 3),
			"web:dom":   make([]dts_parser.Statement, 5),
		},
		Drops: []DropNote{
			{Name: "globalThis", SourceFile: "lib.es5.d.ts"},
			{Name: "eval", SourceFile: "lib.es5.d.ts"},
		},
	}
	var sb strings.Builder
	require.NoError(t, ReportPartition(res, &sb))
	out := sb.String()
	require.Contains(t, out, "std:array: 3 decls")
	require.Contains(t, out, "web:dom: 5 decls")
	require.Contains(t, out, "drops: 2 (eval, globalThis)")
	// Sorted: std comes before web.
	require.Less(t, strings.Index(out, "std:array"), strings.Index(out, "web:dom"))
}
