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
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/stretchr/testify/require"
)

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

	res, err := PartitionLib(inputs)
	require.NoError(t, err)
	require.NotEmpty(t, res.Buckets, "lib.es5 must produce at least one bucket")

	outDir := t.TempDir()
	written, err := WritePartitionedTree(res, outDir)
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
	_, err = PartitionLib(append(inputs, bogus))
	require.Error(t, err)
	require.EqualError(t, err, UnmappedError("__TotallyUnknown__", "lib.es99.fake.d.ts").Error())
}

// TestPartitionLib_PinnedLibSet_Bootstraps gates the whole pinned
// TypeScript lib set. Routing it must complete and write a tree rather
// than abort on the §6.1 unmapped-symbol fail-safe, and every file the
// run emits must parse with Escalier's own parser. The test runs the
// three steps `dts_to_esc bootstrap` runs — route, convert, write —
// against node_modules/typescript/lib.
//
// The parse assertion is the §6 gate `check` and `regenerate` depend on,
// since both re-read every file in the tree before diffing it. A file
// they cannot re-read makes the tree unusable, so the printer must never
// emit a construct the parser rejects.
func TestPartitionLib_PinnedLibSet_Bootstraps(t *testing.T) {
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

	res, err := PartitionLib(inputs)
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
// fails here, and `bootstrap` names it in the same run via
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

	res, err := PartitionLib(inputs)
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
	// allow-list matching, a bootstrap run prints nothing about
	// singleton keys.
	var sb strings.Builder
	require.NoError(t, ReportSingletonKeyDrops(mods, &sb))
	require.Empty(t, sb.String())
}
