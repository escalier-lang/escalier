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
	snaps.MatchInlineSnapshot(t, stdout.String(), snaps.Inline(`--- /dev/null
+++ b/std/array.esc
@@ -0,0 +1,7 @@
+@js("Array")
+export declare class Array<T> {
+    length: number,
+    constructor(mut self),
+    static isArray(arg: any) -> boolean,
+    static readonly prototype: Array<any>
+}
check: 1 missing declarations, 0 missing members, 0 extra declarations
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

// committedCFG is the control-flow graph tools/spec-extract commits, which the
// --cfg flag reads.
const committedCFG = "../spec-extract/cfg.json"

// Every ECMA-262 report reaches stderr, and none does without the flag. The
// rendering of each line is pinned in internal/ecma262 against a demo graph;
// what the snapshot below adds is what a real run over the committed graph and
// the seeded lib reports, the partition summary above them included.
//
// The counts move when curated.json or cfg.json changes, which is the point:
// the diff shows what a data change did to the reports an operator reads.
func TestRun_BootstrapWithCFGPrintsEveryReport(t *testing.T) {
	libDir := seedLib(t, arrayLib)

	var stderr strings.Builder
	require.NoError(t, run([]string{"bootstrap", "--cfg", committedCFG, libDir, t.TempDir()}, io.Discard, &stderr))

	snaps.MatchInlineSnapshot(t, reportSummaries(stderr.String()), snaps.Inline(`  std:array: 3 decls
  curation: 27 fill-ins, 0 corrections, 0 redundant, 0 stale, 0 unmatched, 0 refused
  coercion filter: 4882 TypeError sites adjudicated, 362 dropped, 103 methods under-reported
  join: 1 matched (1 with a receiver claim), 0 declarations without a fact, 436 facts without a declaration, 0 unkeyed declarations, 64 unjoinable facts
  returns: 1 settled as owned by the declared type, 0 left unknown`))
}

// reportSummaries keeps the summary line of each report and drops the per-name
// detail under it. A summary carries two leading spaces and a detail line four,
// so the indent is what tells them apart. The detail is every name the join
// could not match, hundreds of lines that internal/ecma262 already pins.
func reportSummaries(stderr string) string {
	var kept []string
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// A graph whose facts hold a determination neither the analysis nor the curated
// layer answered fails the run rather than writing a hole. The committed graph
// has none, so the case is built here: one method whose only step is prose the
// serializer could not lower, which withholds its receiver.
func TestRun_BootstrapRejectsAnUnansweredDetermination(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "cfg.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(
		`{"specTarget":"abc","funcs":[`+
			`{"name":"Demo.prototype.opaque","kind":"builtin-method","params":[],"nodes":[`+
			`{"kind":"opaque","text":["Let _x_ be whatever the host decides."]}]}]}`), 0o644))
	outDir := t.TempDir()

	err := run([]string{"bootstrap", "--cfg", cfgPath, seedLib(t, arrayLib), outDir}, io.Discard, io.Discard)

	require.EqualError(t, err, cfgPath+" leaves determinations unanswered:\n"+
		"  Demo.prototype.opaque: no receiver determination")
	require.Empty(t, treeOf(t, outDir))
}

// A --cfg path that does not resolve fails the run before anything is written,
// so a mistyped flag leaves no half-joined tree behind.
func TestRun_BootstrapRejectsABadCFGPath(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()

	err := run([]string{"bootstrap", "--cfg", "no/such/cfg.json", seedLib(t, arrayLib), outDir}, io.Discard, io.Discard)

	require.EqualError(t, err,
		"loading no/such/cfg.json: reading no/such/cfg.json: open no/such/cfg.json: no such file or directory")
	require.Empty(t, treeOf(t, outDir))
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

// TestRun_BootstrapWritesTheTree covers the seeding mode. The snapshot
// holds the operator-facing report, the tree the run laid down, and the
// one package file it emitted, so a change to any of the three shows up
// in the diff rather than behind a Contains check.
//
// The out-dir is a fresh temp path on every run, so the report has it
// replaced by a placeholder before the comparison.
func TestRun_BootstrapWritesTheTree(t *testing.T) {
	t.Parallel()
	libDir := seedLib(t, arrayLib)
	outDir := t.TempDir()

	var stderr strings.Builder
	require.NoError(t, run([]string{"bootstrap", libDir, outDir}, io.Discard, &stderr))

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

	require.NoError(t, run([]string{"bootstrap", libDir, escDir}, io.Discard, io.Discard))

	var stdout strings.Builder
	require.NoError(t, run([]string{"check", libDir, escDir}, &stdout, io.Discard))
	snaps.MatchInlineSnapshot(t, stdout.String(), snaps.Inline(`check: 0 missing declarations, 0 missing members, 0 extra declarations
note: signature and property-type drift are not checked yet; those compare both sides through the solver's constrain (SimpleSub M7.5)
`))
}

// TestRun_CheckPassesAfterRegenerate is the same happy path seeded by
// the write mode instead of by `bootstrap`. The two entry points share
// one diff, so what `regenerate` writes is exactly what `check` then
// finds nothing missing in.
func TestRun_CheckPassesAfterRegenerate(t *testing.T) {
	t.Parallel()
	libDir := seedLib(t, arrayLib)
	escDir := t.TempDir()

	var regenOut strings.Builder
	require.NoError(t, run([]string{"regenerate", libDir, escDir}, &regenOut, io.Discard))
	require.Contains(t, regenOut.String(), "created std:array (std/array.esc)")

	var checkOut strings.Builder
	require.NoError(t, run([]string{"check", libDir, escDir}, &checkOut, io.Discard))
	require.Contains(t, checkOut.String(),
		"check: 0 missing declarations, 0 missing members, 0 extra declarations")
}

// TestRun_RegenerateIsIdempotent pins the re-run contract at the CLI
// level: a second pass over a tree the first pass just wrote finds
// nothing to add and reports zero on both counts.
func TestRun_RegenerateIsIdempotent(t *testing.T) {
	t.Parallel()
	libDir := seedLib(t, arrayLib)
	escDir := t.TempDir()

	require.NoError(t, run([]string{"regenerate", libDir, escDir}, io.Discard, io.Discard))

	var second strings.Builder
	require.NoError(t, run([]string{"regenerate", libDir, escDir}, &second, io.Discard))
	require.Contains(t, second.String(), "regenerate: +0 declarations, +0 members")
}

func TestRun_RejectsWrongArgumentCounts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		args    []string
		message string
	}{
		{"bootstrap without out dir", []string{"bootstrap", "lib"},
			"usage: dts_to_esc bootstrap [--cfg <cfg.json>] <lib-dir> <out-dir>"},
		{"check without esc dir", []string{"check", "lib"},
			"usage: dts_to_esc check <lib-dir> <esc-dir>"},
		{"regenerate without esc dir", []string{"regenerate", "lib"},
			"usage: dts_to_esc regenerate <lib-dir> <esc-dir>"},
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

// bumpedArrayLib is arrayLib one TypeScript version on: `Array` gained
// a member and a new interface joined the same bucket.
const bumpedArrayLib = `
interface Array<T> { length: number; at(index: number): T | undefined; }
interface ArrayConstructor { new <T>(): Array<T>; isArray(arg: any): boolean; readonly prototype: Array<any>; }
declare var Array: ArrayConstructor;
interface ArrayLike<T> { readonly length: number; }
`

// TestRun_CheckDiffsACommittedFile is the §6.6 bump case: the committed
// tree is a TypeScript version behind, so `check` prints the patch a
// `regenerate` run would apply. The hand-written comment the test
// splices into the committed file stands in for the §7 edits a bump has
// to preserve, and appears in the diff as context rather than as a
// change.
func TestRun_CheckDiffsACommittedFile(t *testing.T) {
	t.Parallel()
	escDir := t.TempDir()
	require.NoError(t, run(
		[]string{"bootstrap", seedLib(t, arrayLib), escDir}, io.Discard, io.Discard))

	dest := filepath.Join(escDir, "std", "array.esc")
	committed, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dest, []byte(strings.Replace(string(committed),
		"    static readonly prototype",
		"    // Hand-written note.\n    static readonly prototype", 1)), 0o644))

	var stdout strings.Builder
	err = run([]string{"check", seedLib(t, bumpedArrayLib), escDir}, &stdout, io.Discard)
	require.ErrorIs(t, err, errCheckFailed)
	snaps.MatchInlineSnapshot(t, stdout.String(), snaps.Inline(`--- a/std/array.esc
+++ b/std/array.esc
@@ -4,5 +4,10 @@
     constructor(mut self),
     static isArray(arg: any) -> boolean,
     // Hand-written note.
-    static readonly prototype: Array<any>
+    static readonly prototype: Array<any>,
+    at(self, index: number) -> T | undefined,
 }
+
+export declare interface ArrayLike<T> {
+    readonly length: number
+}
check: 1 missing declarations, 1 missing members, 0 extra declarations
note: signature and property-type drift are not checked yet; those compare both sides through the solver's constrain (SimpleSub M7.5)
`))
}
