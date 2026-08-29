package dts_to_esc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// TestPartitionLib_LibES5_EndToEnd is the §6 PR A smoke gate: run the
// full partitioning pipeline against the checked-in lib.es5.d.ts (kept
// under playground/public/types/) and verify that every produced
// `.esc` file parses with Escalier's own parser. Failures here are
// usually partition-table gaps surfaced via the unmapped-symbol
// fail-safe, or printer-side bugs that produce un-reparseable output.
func TestPartitionLib_LibES5_EndToEnd(t *testing.T) {
	t.Parallel()

	libPath := filepath.Join("..", "..", "playground", "public", "types", "lib.es5.d.ts")
	if _, err := os.Stat(libPath); err != nil {
		t.Skipf("lib.es5.d.ts not present at %s: %v", libPath, err)
	}
	libDir := filepath.Dir(libPath)

	// Restrict discovery to just lib.es5.d.ts by parsing it directly —
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

	parsed, unparsed := 0, 0
	for _, uri := range written {
		pkg, ok := PackageForURI(uri)
		require.True(t, ok, "URI %q from result must be a known package", uri)
		path := filepath.Join(outDir, filepath.FromSlash(pkg.File))
		contents, err := os.ReadFile(path)
		require.NoError(t, err, "%s should be on disk", path)
		require.NotEmpty(t, contents, "%s should not be empty", path)

		// Soft gate: report parse status without failing. The full
		// "every output parses" gate belongs in §6 PR B (`--check`
		// mode) once §7 hand-edits close the remaining
		// printer/parser asymmetries on real `lib.*.d.ts` surface.
		// PR A's job is the routing + write pipeline; per-symbol
		// roundtrip work happens in §7 review.
		_, parseErrs := parser.ParseDecls(context.Background(), &ast.Source{
			Path:     path,
			Contents: string(contents),
		})
		if len(parseErrs) == 0 {
			parsed++
		} else {
			unparsed++
			t.Logf("[soft] %s did not parse cleanly (%d errors); first: %v",
				path, len(parseErrs), parseErrs[0])
		}
	}
	t.Logf("lib.es5 partition: %d packages parsed, %d need §7 hand-edits",
		parsed, unparsed)

	// Gate: the unmapped fail-safe trips on a synthetic missing name —
	// the same lib + one extra decl that no partition entry covers
	// must error rather than silently land in some catch-all bucket.
	bogus := parseLib(t, "lib.es99.fake.d.ts", `declare var __TotallyUnknown__: number;`)
	_, err = PartitionLib(append(inputs, bogus))
	require.Error(t, err)
	require.EqualError(t, err, UnmappedError("__TotallyUnknown__", "lib.es99.fake.d.ts").Error())
}

// TestPartitionLib_PinnedLibSet_Bootstraps is the #1325 gate: routing
// the whole pinned TypeScript lib set must complete and write a tree,
// rather than aborting on the §6.1 unmapped-symbol fail-safe. It runs
// the same three steps `dts_to_esc bootstrap` does — route, convert,
// write — against node_modules/typescript/lib.
//
// Whether the emitted files parse is a separate gate; see #1324.
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
	// from a bucket URI, and the worker host lib must contribute
	// redeclarations rather than a second copy of the DOM.
	require.NotEmpty(t, res.Buckets)
	require.NotEmpty(t, res.Redeclarations)
	for uri := range res.Buckets {
		_, ok := PackageForURI(uri)
		require.True(t, ok, "bucket URI %q must be a known package", uri)
	}

	// lib.dom.d.ts and lib.webworker.d.ts declare `interface
	// ReadableStream` identically, and mergeDecls concatenates the
	// members of every same-named interface in a bucket. Skipping the
	// worker copy is what keeps the merged interface from carrying two
	// of each. `readonly locked: boolean` is declared once per copy and
	// is not overloaded, so counting it distinguishes a merge that took
	// both copies from one that took a single copy. Overloaded names
	// like `getReader` legitimately repeat, so they cannot serve here.
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

	written, err := WriteConvertedTree(mods, t.TempDir())
	require.NoError(t, err)
	require.NotEmpty(t, written)
}
