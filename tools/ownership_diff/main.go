// Command ownership_diff compares the diagnostics of the current ownership checks with the
// diagnostics of the dataflow pass in internal/solver/ownership_dataflow.go. It runs the
// solver tests and the fixture tests twice, once with ESCALIER_OWNERSHIP_DATAFLOW=0 and once
// with ESCALIER_OWNERSHIP_DATAFLOW=1, and prints each diagnostic that one run reports and
// the other does not. The report groups the differences by test, and ends with a count of
// each kind of difference.
//
//	go run ./tools/ownership_diff [-pkgs ./internal/solver/,./cmd/...] [-run <regexp>]
//
// Each run sets ESCALIER_DIAG_TRACE=1, which makes the solver print every diagnostic of a
// top-level run to standard output, as traceDiagnostics in ownership_dataflow.go describes.
// `go test -json` attributes each printed line to the test that was running, and -parallel 1
// keeps two tests of one package from running at once. A test's pass or failure does not
// matter to the report.
//
// The attribution can name the test before the one that printed a line, when a paused
// parallel test resumes. So two diagnostics match when they come from the same program,
// which the trace names by a hash of its sources, rather than from the same test. A script
// has no such key, and its diagnostics match only within one test.
//
// The `old` side is the run with the flag off, and the `new` side the run with it on.
package main

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// tracePrefix starts each line the solver prints for a diagnostic. It matches
// diagnosticTracePrefix in internal/solver/ownership_dataflow.go.
const tracePrefix = "ESCALIER_DIAG\t"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "ownership_diff:", err)
		os.Exit(1)
	}
}

// run parses args, runs the tests with the flag off and on, and writes the report to out.
// The output of `go test` other than its JSON stream goes to errOut.
func run(args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("ownership_diff", flag.ContinueOnError)
	pkgs := fs.String("pkgs", "./internal/solver/,./cmd/...", "comma-separated packages to test")
	runPattern := fs.String("run", "", "only run tests matching this regexp, as go test -run")
	if err := fs.Parse(args); err != nil {
		return err
	}

	packages := strings.Split(*pkgs, ",")

	off, err := traceTests(packages, *runPattern, "0", errOut)
	if err != nil {
		return err
	}

	on, err := traceTests(packages, *runPattern, "1", errOut)
	if err != nil {
		return err
	}

	writeReport(out, diffRuns(off, on))
	return nil
}

// traceTests runs `go test -json` over packages with ESCALIER_OWNERSHIP_DATAFLOW set to
// flagValue and returns the diagnostics the tests printed. A failing test does not stop the
// run, so a non-zero exit from `go test` is not an error.
func traceTests(packages []string, runPattern, flagValue string, errOut io.Writer) ([]traced, error) {
	args := []string{"test", "-json", "-count=1", "-parallel=1"}
	if runPattern != "" {
		args = append(args, "-run", runPattern)
	}
	args = append(args, packages...)

	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "ESCALIER_DIAG_TRACE=1", "ESCALIER_OWNERSHIP_DATAFLOW="+flagValue)
	cmd.Stderr = errOut

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	diags, parseErr := parseTrace(stdout)
	waitErr := cmd.Wait()
	if parseErr != nil {
		return nil, parseErr
	}

	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return nil, waitErr
	}

	return diags, nil
}

// diagnostic is one traced diagnostic. Two diagnostics match when they are equal.
type diagnostic struct {
	// Program is the hash of the sources that produced the diagnostic. For a script, which
	// the trace gives no hash, it is the test that printed the diagnostic.
	Program string
	// Type is the Go type of the error, such as `*solver.UseAfterMoveError`.
	Type string
	// Span is the error's location as the trace prints it.
	Span string
	// Message is the error's message.
	Message string
}

// traced is one diagnostic and the test `go test -json` attributes it to.
type traced struct {
	Test string
	Diag diagnostic
}

// testEvent is the part of one `go test -json` event the harness reads.
type testEvent struct {
	Action  string
	Package string
	Test    string
	Output  string
}

// tempDir matches a directory testing.T.TempDir creates, such as `/tmp/TestFoo123/001`, or
// its parent `/tmp/TestFoo123`. The digits differ on every run, so normalize replaces the
// match.
var tempDir = regexp.MustCompile(regexp.QuoteMeta(filepath.Clean(os.TempDir())) + `/[\w@.\-]*\d(/\d{3})?`)

// normalize replaces each temporary directory in s with `$TMP`.
func normalize(s string) string {
	return tempDir.ReplaceAllString(s, "$$TMP")
}

// parseTrace reads a `go test -json` stream and returns the diagnostics the tests printed,
// in the order they were printed. Lines that are not traced diagnostics are skipped.
func parseTrace(r io.Reader) ([]traced, error) {
	var out []traced

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<26)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}

		var ev testEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("parsing go test output: %w", err)
		}

		if ev.Action != "output" || !strings.HasPrefix(ev.Output, tracePrefix) {
			continue
		}

		fields := strings.SplitN(strings.TrimSuffix(strings.TrimPrefix(ev.Output, tracePrefix), "\n"), "\t", 4)
		if len(fields) != 4 {
			continue
		}

		test := testKey(ev.Package, ev.Test)
		program := fields[0]
		if program == "" {
			program = test
		}

		out = append(out, traced{Test: test, Diag: diagnostic{
			Program: program,
			Type:    fields[1],
			Span:    normalize(fields[2]),
			Message: normalize(fields[3]),
		}})
	}

	return out, scanner.Err()
}

// testKey names a test as its package followed by its name. Output printed outside any
// test, such as from TestMain, is keyed by the package alone.
func testKey(pkg, test string) string {
	if test == "" {
		return pkg
	}
	return pkg + " " + test
}

// testDiff is the diagnostics attributed to one test that one run reports and the other
// does not.
type testDiff struct {
	Test string
	// Old lists the diagnostics only the run with the flag off reported.
	Old []diagnostic
	// New lists the diagnostics only the run with the flag on reported.
	New []diagnostic
}

// diffRuns returns the diagnostics only one of off and on reported, grouped by the test
// each is attributed to and sorted by test. A diagnostic reported twice in one run and once
// in the other differs once. Diagnostics attributed to the same test are matched first, and
// what is left is matched across tests.
func diffRuns(off, on []traced) []testDiff {
	sameTest := func(t traced) any { return t }
	anyTest := func(t traced) any { return t.Diag }

	oldOnly := unmatched(unmatched(off, on, sameTest), unmatched(on, off, sameTest), anyTest)
	newOnly := unmatched(unmatched(on, off, sameTest), unmatched(off, on, sameTest), anyTest)

	byTest := map[string]*testDiff{}
	entry := func(test string) *testDiff {
		if td, ok := byTest[test]; ok {
			return td
		}
		td := &testDiff{Test: test}
		byTest[test] = td
		return td
	}

	for _, t := range oldOnly {
		td := entry(t.Test)
		td.Old = append(td.Old, t.Diag)
	}

	for _, t := range newOnly {
		td := entry(t.Test)
		td.New = append(td.New, t.Diag)
	}

	out := make([]testDiff, 0, len(byTest))
	for _, td := range byTest {
		out = append(out, *td)
	}

	slices.SortFunc(out, func(a, b testDiff) int { return cmp.Compare(a.Test, b.Test) })
	return out
}

// unmatched returns the entries of a that no entry of b matches, in their original order.
// Two entries match when key returns equal values for them. Each entry of b matches at most
// one entry of a.
func unmatched(a, b []traced, key func(traced) any) []traced {
	counts := map[any]int{}
	for _, t := range b {
		counts[key(t)]++
	}

	var out []traced
	for _, t := range a {
		if k := key(t); counts[k] > 0 {
			counts[k]--
			continue
		}
		out = append(out, t)
	}

	return out
}

// quotedName matches a name a message quotes, such as the `'p.x'` of
// `cannot assign to 'p.x' while it is borrowed as immutable`.
var quotedName = regexp.MustCompile(`'[^']*'`)

// category groups a diagnostic with others of its kind: its side, its type, and its message
// with each quoted name replaced by `'…'`.
func category(side string, d diagnostic) string {
	return fmt.Sprintf("%s %s: %s", side, d.Type, quotedName.ReplaceAllString(d.Message, "'…'"))
}

// writeReport writes diffs to w: each test with its differing diagnostics, then the number
// of tests that differ, the number of differences on each side, and the number of
// differences in each category, most frequent first.
func writeReport(w io.Writer, diffs []testDiff) {
	counts := map[string]int{}
	oldTotal, newTotal := 0, 0

	for _, td := range diffs {
		fmt.Fprintln(w, td.Test)
		for _, d := range td.Old {
			fmt.Fprintf(w, "\told\t%s\t%s\t%s\n", d.Span, d.Type, d.Message)
			counts[category("old", d)]++
			oldTotal++
		}
		for _, d := range td.New {
			fmt.Fprintf(w, "\tnew\t%s\t%s\t%s\n", d.Span, d.Type, d.Message)
			counts[category("new", d)]++
			newTotal++
		}
	}

	fmt.Fprintf(w, "\n%d tests differ: %d diagnostics only with the flag off, %d only with it on\n", len(diffs), oldTotal, newTotal)

	cats := make([]string, 0, len(counts))
	for c := range counts {
		cats = append(cats, c)
	}

	slices.SortFunc(cats, func(a, b string) int {
		return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(a, b))
	})

	for _, c := range cats {
		fmt.Fprintf(w, "%6d  %s\n", counts[c], c)
	}
}
