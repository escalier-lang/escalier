package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// seedLib writes one synthetic `lib.*.d.ts` into a fresh directory and
// returns that directory.
func seedLib(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "lib.es5.d.ts"), []byte(contents), 0o644))
	return dir
}

const arrayLib = `
interface Array<T> { length: number; }
interface ArrayConstructor { new <T>(): Array<T>; isArray(arg: any): boolean; readonly prototype: Array<any>; }
declare var Array: ArrayConstructor;
`

// TestRun_CheckFailsOnAnEmptyTree runs `check` against a committed
// `.esc` tree with nothing in it. The two directories play opposite
// roles: `libDir` holds the `.d.ts` inputs and is seeded, while
// `escDir` is the tree being verified and stays empty. Every
// declaration the converter produces is therefore missing, which is
// what a first run against an unseeded tree looks like.
func TestRun_CheckFailsOnAnEmptyTree(t *testing.T) {
	t.Parallel()
	libDir := seedLib(t, arrayLib)
	escDir := t.TempDir()

	var stdout strings.Builder
	err := run([]string{"check", libDir, escDir}, &stdout, io.Discard)
	require.ErrorIs(t, err, errCheckFailed)
	snaps.MatchInlineSnapshot(t, stdout.String(), snaps.Inline(`std:array (std/array.esc)
  missing file
  missing declaration: Array (class)
check: 1 missing declarations, 0 extra declarations
note: signature and property-type drift are not checked yet; those compare both sides through the solver's constrain (SimpleSub M7.5)
`))
}

// TestRun_SingleFileWritesEscToStdout covers the §5 single-file mode:
// one `.d.ts` in, Escalier source out on stdout, nothing written to
// disk. The snapshot is the whole emitted module, so it also pins the
// trio fusion — `interface ArrayConstructor` and `declare var Array`
// are gone, collapsed into the one class.
func TestRun_SingleFileWritesEscToStdout(t *testing.T) {
	t.Parallel()
	path := filepath.Join(seedLib(t, arrayLib), "lib.es5.d.ts")

	var stdout strings.Builder
	require.NoError(t, run([]string{path}, &stdout, io.Discard))

	snaps.MatchInlineSnapshot(t, stdout.String(), snaps.Inline(`@js("Array")
export declare class Array<T> {
    length: number,
    constructor(mut self),
    static isArray(arg: any) -> boolean,
    static readonly prototype: Array<any>
}
`))
}

// treeOf lists every file under root as a slash-separated path relative
// to it, sorted. Dropping the root keeps the listing free of the temp
// directory the test ran in.
func treeOf(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	}))
	sort.Strings(out)
	return out
}

// TestRun_PartitionWritesTheTree covers the §6 PR A mode. The snapshot
// holds the operator-facing report, the tree the run laid down, and the
// one package file it emitted, so a change to any of the three shows up
// in the diff rather than behind a Contains check.
//
// The out-dir is a fresh temp path on every run, so the report has it
// replaced by a placeholder before the comparison.
func TestRun_PartitionWritesTheTree(t *testing.T) {
	t.Parallel()
	libDir := seedLib(t, arrayLib)
	outDir := t.TempDir()

	var stderr strings.Builder
	require.NoError(t, run([]string{"partition", libDir, outDir}, io.Discard, &stderr))

	contents, err := os.ReadFile(filepath.Join(outDir, "std", "array.esc"))
	require.NoError(t, err)

	report := strings.ReplaceAll(stderr.String(), outDir, "<out-dir>")
	snaps.MatchInlineSnapshot(t, fmt.Sprintf(
		"--- stderr ---\n%s--- tree ---\n%s\n--- std/array.esc ---\n%s",
		report, strings.Join(treeOf(t, outDir), "\n"), contents), snaps.Inline(`--- stderr ---
discovered 1 lib files
wrote 1 packages under <out-dir>
  std:array: 3 decls
--- tree ---
node/README.md
std/array.esc
--- std/array.esc ---
@js("Array")
export declare class Array<T> {
    length: number,
    constructor(mut self),
    static isArray(arg: any) -> boolean,
    static readonly prototype: Array<any>
}
`))
}

// TestRun_CheckPassesOnASeededTree is the happy path for `check`: seed
// the `.esc` tree with the converter's own output, then verify it. The
// two modes have to agree about what a package holds, so nothing comes
// back missing and the run exits zero.
func TestRun_CheckPassesOnASeededTree(t *testing.T) {
	t.Parallel()
	libDir := seedLib(t, arrayLib)
	escDir := t.TempDir()

	require.NoError(t, run([]string{"partition", libDir, escDir}, io.Discard, io.Discard))

	var stdout strings.Builder
	require.NoError(t, run([]string{"check", libDir, escDir}, &stdout, io.Discard))
	snaps.MatchInlineSnapshot(t, stdout.String(), snaps.Inline(`check: 0 missing declarations, 0 extra declarations
note: signature and property-type drift are not checked yet; those compare both sides through the solver's constrain (SimpleSub M7.5)
`))
}

func TestRun_RejectsWrongArgumentCounts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		args    []string
		message string
	}{
		{"check without esc dir", []string{"check", "lib"},
			"usage: dts_to_esc check <lib-dir> <esc-dir>"},
		{"no subcommand", nil, usage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := run(tc.args, io.Discard, io.Discard)
			require.EqualError(t, err, tc.message)
		})
	}
}
