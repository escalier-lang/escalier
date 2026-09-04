package dts_to_esc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/stretchr/testify/require"
)

// committedDropsDeclaredIn narrows the committed overlay's root drop
// file to the names `inputs` declare, and returns an overlay holding
// just those.
//
// The staleness check holds every root drop entry to a name some input
// declares, which is what catches an entry TypeScript has removed. A
// test that routes one lib file out of the pinned set therefore cannot
// pass the committed overlay whole. It would trip on the entries the
// rest of the set declares.
func committedDropsDeclaredIn(t *testing.T, inputs []LibInput) *Overlay {
	t.Helper()
	declared := set.NewSet[string]()
	for _, in := range inputs {
		for _, stmt := range in.Module.Statements {
			declared.Add(topLevelName(stmt))
		}
	}
	var file strings.Builder
	for _, name := range sortedNames(committedOverlay(t).GlobalDrops()) {
		if declared.Contains(name) {
			fmt.Fprintf(&file, "export declare val %s\n", name)
		}
	}
	overlay, err := LoadOverlay(seedOverlay(t, map[string]string{RootDropFile: file.String()}))
	require.NoError(t, err)
	return overlay
}

// TestPartitionLib_LibES5_EndToEnd runs the round-trip gate over lib.es5.d.ts
// alone. Every `.esc` file the pipeline emits from it must parse with Escalier's
// own parser. Naming one lib file keeps a failure readable. A break in the es5
// surface fails here rather than only in
// TestPartitionLib_PinnedLibSet_Bootstraps, which runs the whole pinned set.
// This test also holds the unmapped-symbol fail-safe check at the end.
func TestPartitionLib_LibES5_EndToEnd(t *testing.T) {
	t.Parallel()

	libDir := filepath.Join("..", "..", "node_modules", "typescript", "lib")
	libPath := filepath.Join(libDir, "lib.es5.d.ts")
	if _, err := os.Stat(libPath); err != nil {
		t.Skipf("lib.es5.d.ts not present at %s; run `pnpm install`: %v", libPath, err)
	}

	// Restrict discovery to just lib.es5.d.ts by naming it directly —
	// DiscoverLibFiles would pick up the full set under the same dir.
	inputs, err := ParseLibFiles(libDir, []string{"lib.es5.d.ts"})
	require.NoError(t, err)

	overlay := committedDropsDeclaredIn(t, inputs)
	res, err := PartitionLibWithOverlay(inputs, overlay)
	require.NoError(t, err)
	require.NotEmpty(t, res.Buckets, "lib.es5 must produce at least one bucket")

	mods, err := ConvertBuckets(res)
	require.NoError(t, err)

	outDir := t.TempDir()
	written, err := WriteConvertedTree(mods, outDir)
	require.NoError(t, err)
	require.NotEmpty(t, written)

	for _, uri := range written {
		pkg, ok := PackageForURI(uri)
		require.True(t, ok, "URI %q from result must be a known package", uri)
		path := filepath.Join(outDir, filepath.FromSlash(pkg.File))
		contents, err := os.ReadFile(path)
		require.NoError(t, err, "%s should be on disk", path)
		require.NotEmpty(t, contents, "%s should not be empty", path)

		_, parseErrs := parser.ParseDecls(context.Background(), &ast.Source{
			Path:     path,
			Contents: string(contents),
		})
		require.Empty(t, parseErrs, "%s must parse back", pkg.File)
	}

	// Gate: the unmapped fail-safe trips on a synthetic missing name —
	// the same lib + one extra decl that no partition entry covers
	// must error rather than silently land in some catch-all bucket.
	bogus := parseLib(t, "lib.es99.fake.d.ts", `declare var __TotallyUnknown__: number;`)
	_, err = PartitionLibWithOverlay(append(inputs, bogus), overlay)
	require.Error(t, err)
	require.EqualError(t, err, UnmappedError("__TotallyUnknown__", "lib.es99.fake.d.ts").Error())
}

// TestPartitionLib_PinnedLibSet_RoutesConvertsAndWrites gates the whole
// pinned TypeScript lib set. Routing it must complete and write a tree
// rather than abort on the §6.1 unmapped-symbol fail-safe, and every
// file the run emits must parse with Escalier's own parser. The test
// runs route, convert, and write against node_modules/typescript/lib,
// with no overlay, so what it holds is the converter's own output.
//
// The parse assertion is the §6 gate the committed tree depends on. A
// package file is what the checker ingests, so the printer must never
// emit a construct the parser rejects.
func TestPartitionLib_PinnedLibSet_RoutesConvertsAndWrites(t *testing.T) {
	t.Parallel()

	repoRoot, err := findRepoRoot()
	require.NoError(t, err)
	libDir := filepath.Join(repoRoot, "node_modules", "typescript", "lib")
	if _, err := os.Stat(libDir); err != nil {
		t.Skipf("pinned TypeScript lib set not present at %s: %v", libDir, err)
	}

	basenames, err := DiscoverLibFiles(libDir)
	require.NoError(t, err)
	require.NotEmpty(t, basenames)

	inputs, err := ParseLibFiles(libDir, basenames)
	require.NoError(t, err)

	res, err := PartitionLibWithOverlay(inputs, committedOverlay(t))
	require.NoError(t, err)

	// Every package the routing pass knows about must be reachable
	// from a bucket URI.
	require.NotEmpty(t, res.Buckets)
	for uri := range res.Buckets {
		_, ok := PackageForURI(uri)
		require.True(t, ok, "bucket URI %q must be a known package", uri)
	}

	// lib.dom.d.ts and lib.webworker.d.ts declare `interface
	// ReadableStream` identically, and mergeDecls concatenates the
	// members of every same-named interface in a bucket. Ignoring the
	// worker host lib is what keeps the merged interface from carrying
	// two of each. `readonly locked: boolean` is declared once per copy
	// and is not overloaded, so its count tells a merge that took both
	// copies from one that took a single copy. An overloaded name like
	// `getReader` repeats for its own reasons and cannot serve here.
	var stream *dts_parser.InterfaceDecl
	for _, stmt := range res.Buckets["web:streams"] {
		if iface, ok := stmt.(*dts_parser.InterfaceDecl); ok &&
			iface.Name.Name == "ReadableStream" {
			require.Nil(t, stream, "ReadableStream must merge into one interface")
			stream = iface
		}
	}
	require.NotNil(t, stream)
	locked := 0
	for _, m := range stream.Members {
		if memberKey(m) == "locked" {
			locked++
		}
	}
	require.Equal(t, 1, locked,
		"ReadableStream.locked must appear once; a worker-host copy was merged in")

	mods, err := ConvertBuckets(res)
	require.NoError(t, err)

	outDir := t.TempDir()
	written, err := WriteConvertedTree(mods, outDir)
	require.NoError(t, err)
	require.NotEmpty(t, written)

	for _, uri := range written {
		pkg, ok := PackageForURI(uri)
		require.True(t, ok, "URI %q from result must be a known package", uri)
		path := filepath.Join(outDir, filepath.FromSlash(pkg.File))
		contents, err := os.ReadFile(path)
		require.NoError(t, err, "%s should be on disk", path)
		require.NotEmpty(t, contents, "%s should not be empty", path)

		_, parseErrs := parser.ParseDecls(context.Background(), &ast.Source{
			Path:     path,
			Contents: string(contents),
		})
		require.Empty(t, parseErrs, "%s must parse back", pkg.File)
	}
}

// TestPartitionLib_SingletonKeyDropsMatchAllowList is the gate for the
// symbol-keyed-singleton policy. Flattening emits one top-level
// declaration per member and needs a plain identifier for both the
// Escalier binding and the `@js(...)` path, so a member under a
// computed key is dropped. AllowedSingletonKeyDrops names the drops
// that are expected, and this test pins both directions over the whole
// pinned lib set: no member outside the list is dropped, and every
// entry in the list is a drop that actually happens.
//
// A TypeScript bump that adds, say, `[Symbol.dispose]` to `Atomics`
// fails here, and `generate` names it in the same run via
// ReportSingletonKeyDrops.
func TestPartitionLib_SingletonKeyDropsMatchAllowList(t *testing.T) {
	t.Parallel()

	libDir := filepath.Join("..", "..", "node_modules", "typescript", "lib")
	if _, err := os.Stat(filepath.Join(libDir, "lib.es5.d.ts")); err != nil {
		t.Skipf("TypeScript lib not present at %s; run `pnpm install`: %v", libDir, err)
	}

	basenames, err := DiscoverLibFiles(libDir)
	require.NoError(t, err)
	require.NotEmpty(t, basenames)

	inputs, err := ParseLibFiles(libDir, basenames)
	require.NoError(t, err)

	res, err := PartitionLibWithOverlay(inputs, committedOverlay(t))
	require.NoError(t, err)

	mods, err := ConvertBuckets(res)
	require.NoError(t, err)

	dropped := set.NewSet[SingletonMember]()
	for _, mod := range mods {
		for _, m := range mod.KeyDrops {
			dropped.Add(m)
		}
	}
	require.ElementsMatch(t,
		AllowedSingletonKeyDrops.ToSlice(), dropped.ToSlice(),
		"AllowedSingletonKeyDrops must name exactly the singleton members "+
			"the pinned lib set drops")

	// The report is the operator-facing half of the gate: with the
	// allow-list matching, a `generate` run prints nothing about
	// singleton keys.
	var sb strings.Builder
	require.NoError(t, ReportSingletonKeyDrops(mods, &sb))
	require.Empty(t, sb.String())
}

// TestGenerate_PinnedLibSet is the §6.6 gate over the real inputs:
// `generate` writes the whole pinned lib set through the committed
// overlay, every file it emits parses with Escalier's own parser, and a
// second run leaves the tree byte-identical.
//
// Between the two runs the test hand-edits one package file, adding a
// declaration no input produces. The second run overwrites the edit,
// which is what proves the run reads no file it wrote. Byte-identity
// alone would not: a run that read the tree and merged into it would
// also be stable, since its own output already holds everything it
// would add.
func TestGenerate_PinnedLibSet(t *testing.T) {
	t.Parallel()

	repoRoot, err := findRepoRoot()
	require.NoError(t, err)
	libDir := filepath.Join(repoRoot, "node_modules", "typescript", "lib")
	if _, err := os.Stat(libDir); err != nil {
		t.Skipf("pinned TypeScript lib set not present at %s: %v", libDir, err)
	}

	outDir := t.TempDir()
	opts := GenerateOptions{
		LibDir:       libDir,
		OverlayDir:   filepath.Join(repoRoot, "internal", "interop", "overlay"),
		OutDir:       outDir,
		HandAuthored: HandAuthoredPackages,
	}

	res, err := Generate(opts)
	require.NoError(t, err)
	require.NotEmpty(t, res.Written)

	// editedFile is the package the hand-edit below goes into. Any
	// generated file would do. std:array is named because the pinned lib
	// set always routes to it.
	const editedFile = "std/array.esc"

	first := map[string]string{}
	for _, uri := range res.Written {
		pkg, ok := PackageForURI(uri)
		require.True(t, ok, "URI %q from the run must be a known package", uri)
		path := filepath.Join(outDir, filepath.FromSlash(pkg.File))
		contents, err := os.ReadFile(path)
		require.NoError(t, err, "%s should be on disk", pkg.File)
		require.True(t, strings.HasPrefix(string(contents), GeneratedHeader),
			"%s should open with the generated-file header", pkg.File)
		first[pkg.File] = string(contents)

		_, parseErrs := parser.ParseDecls(context.Background(), &ast.Source{
			Path:     path,
			Contents: string(contents),
		})
		require.Empty(t, parseErrs, "%s must parse back", pkg.File)
	}

	// Add a declaration no input produces, the way a hand-edit to a
	// generated file would. A run that reads what the first one wrote
	// carries the edit forward; one that reads nothing overwrites it.
	// Byte-identity on its own does not tell the two apart, since a run
	// that merges into its own output is stable too.
	edited := filepath.Join(outDir, filepath.FromSlash(editedFile))
	require.NoError(t, os.WriteFile(edited,
		[]byte(first[editedFile]+"\nexport declare val __handEdited__\n"), 0o644))

	second, err := Generate(opts)
	require.NoError(t, err)
	require.Equal(t, res.Written, second.Written)
	require.Empty(t, second.Removed)
	require.Contains(t, first, editedFile, "%s must have been written for the edit above to mean anything", editedFile)
	for file, contents := range first {
		again, err := os.ReadFile(filepath.Join(outDir, filepath.FromSlash(file)))
		require.NoError(t, err)
		require.Equal(t, contents, string(again), "%s must regenerate byte-identically", file)
	}
}
