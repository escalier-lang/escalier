package dts_to_esc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// TestPartitionLib_LibES5_EndToEnd runs the full partitioning pipeline over the
// pinned TypeScript's lib.es5.d.ts and requires that every `.esc` file it emits
// parses with Escalier's own parser. That is the §6 round-trip gate: the
// printer must never emit a construct the parser rejects, because `check` and
// `regenerate` both re-read every committed file before diffing it.
//
// The whole set of lib files the partition table routes produces 22 packages,
// and lib.es5 alone covers 17 of them. The gate does not yet span the rest.
// Two converter-side gaps block that: the partition table has no entry for
// names such as `FlatArray` and `Disposable`, and flattening a singleton whose
// key is computed, as in `Atomics[Symbol.toStringTag]`, is unsupported.
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
