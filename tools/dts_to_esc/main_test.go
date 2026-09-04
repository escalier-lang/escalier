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

	"github.com/escalier-lang/escalier/internal/dts_to_esc"
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
// rendering of each line is pinned against a demo graph where the report is
// built. What the snapshot below adds is what a real run over the committed
// graph and the seeded lib reports, the partition summary above them included.
//
// The counts move when curated.json or cfg.json changes, which is the point:
// the diff shows what a data change did to the reports an operator reads.
func TestRun_GenerateWithCFGPrintsEveryReport(t *testing.T) {
	libDir := seedLib(t, arrayLib)

	var stderr strings.Builder
	require.NoError(t, run([]string{
		"generate", "--cfg", committedCFG, "--overlay", t.TempDir(), libDir, t.TempDir(),
	}, io.Discard, &stderr))

	snaps.MatchInlineSnapshot(t, reportSummaries(stderr.String()), snaps.Inline(`  curation: 27 fill-ins, 0 corrections, 0 redundant, 0 stale, 0 unmatched, 0 refused
  coercion filter: 4882 TypeError sites adjudicated, 362 dropped
  receivers: 194 confirmed by a heuristic, 24 redundant overrides, 0 disagreements, 48 answered by the facts alone, 37 overrides no fact answers
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
func TestRun_GenerateRejectsAnUnansweredDetermination(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "cfg.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(
		`{"specTarget":"abc","funcs":[`+
			`{"name":"Demo.prototype.opaque","kind":"builtin-method","params":[],"nodes":[`+
			`{"kind":"opaque","text":["Let _x_ be whatever the host decides."]}]}]}`), 0o644))
	outDir := t.TempDir()

	err := run([]string{
		"generate", "--cfg", cfgPath, "--overlay", t.TempDir(), seedLib(t, arrayLib), outDir,
	}, io.Discard, io.Discard)

	require.EqualError(t, err, cfgPath+" leaves determinations unanswered:\n"+
		"  Demo.prototype.opaque: no receiver determination")
	require.Empty(t, treeOf(t, outDir))
}

// A --cfg path that does not resolve fails the run before anything is written,
// so a mistyped flag leaves no half-joined tree behind.
func TestRun_GenerateRejectsABadCFGPath(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()

	err := run([]string{
		"generate", "--cfg", "no/such/cfg.json", "--overlay", t.TempDir(), seedLib(t, arrayLib), outDir,
	}, io.Discard, io.Discard)

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

// readGenerated reads one generated package file and returns it without
// the `Code generated` header. The header holds backticks, which
// go-snaps cannot write back into an inline snapshot, and
// TestGenerate_WritesTheTreeWithAHeader in internal/dts_to_esc pins it
// already.
func readGenerated(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	body, found := strings.CutPrefix(string(contents), dts_to_esc.GeneratedHeader)
	require.True(t, found, "%s should open with the generated-file header", path)
	return body
}

// Every invocation the tool cannot serve ends with the full usage, since
// `generate` is the one subcommand and anything else is read as the path
// to a `.d.ts` file.
func TestRun_RejectsBadInvocations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		args    []string
		message string
	}{
		{"generate without esc dir", []string{"generate", "lib"}, generateUsage},
		{"a retired subcommand and its arguments", []string{"check", "lib", "esc"}, usage},
		{"a retired subcommand on its own", []string{"check"},
			"reading check: open check: no such file or directory\n" + usage},
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

// TestRun_GenerateWritesTheTree covers the generating subcommand end to
// end. The snapshot holds the operator-facing report, the tree the run
// laid down, and the one package file it emitted, so a change to any of
// the three shows up in the diff rather than behind a Contains check.
//
// The overlay adds a declaration no `.d.ts` has, which is what shows the
// third generation input reaching the output.
//
// The out-dir is a fresh temp path on every run, so the report has it
// replaced by a placeholder before the comparison.
func TestRun_GenerateWritesTheTree(t *testing.T) {
	t.Parallel()
	libDir := seedLib(t, arrayLib)
	escDir := t.TempDir()
	overlayDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(overlayDir, "std"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(overlayDir, "std", "array.add.esc"),
		[]byte("@js(\"Symbol.iterator\")\nexport declare val iteratorKey: unique symbol\n"), 0o644))

	var stderr strings.Builder
	require.NoError(t, run(
		[]string{"generate", "--overlay", overlayDir, libDir, escDir}, io.Discard, &stderr))

	contents := readGenerated(t, filepath.Join(escDir, "std", "array.esc"))

	report := strings.ReplaceAll(stderr.String(), escDir, "<esc-dir>")
	snaps.MatchInlineSnapshot(t, fmt.Sprintf(
		"--- stderr ---\n%s--- tree ---\n%s\n--- std/array.esc ---\n%s",
		report, strings.Join(treeOf(t, escDir), "\n"), contents), snaps.Inline(`--- stderr ---
discovered 1 lib files
wrote 1 packages under <esc-dir>
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

@js("Symbol.iterator")
export declare val iteratorKey: unique symbol
`))
}

// TestRun_GenerateResolvesTheOverlayBesideTheTree covers the default
// overlay location. `internal/interop/data` as <esc-dir> resolves the
// overlay to `internal/interop/overlay`, so the bump workflow needs no
// flag.
func TestRun_GenerateResolvesTheOverlayBesideTheTree(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	escDir := filepath.Join(root, "data")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "overlay", "std"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "overlay", "std", "array.drop.esc"),
		[]byte("export declare interface Array {\n    isArray: unknown,\n}\n"), 0o644))

	require.NoError(t, run(
		[]string{"generate", seedLib(t, arrayLib), escDir}, io.Discard, io.Discard))

	require.NotContains(t,
		readGenerated(t, filepath.Join(escDir, "std", "array.esc")), "isArray")
}
