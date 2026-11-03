// Command ownership_diff compares the diagnostics of the current ownership checks with the
// diagnostics of the dataflow pass in internal/solver/ownership_dataflow.go. It runs the
// solver tests and the fixture tests twice, once with ESCALIER_OWNERSHIP_DATAFLOW=0 and once
// with ESCALIER_OWNERSHIP_DATAFLOW=1, and prints each diagnostic that one run reports and
// the other does not. The pass runs by default, and the value 0 turns it off. A test that
// sets Options.NoOwnershipDataflow runs with the pass off in both runs, so it never differs.
// The report groups the differences by test, and ends with a count of each kind of
// difference.
//
//	go run ./tools/ownership_diff [-pkgs ./internal/solver/,./cmd/...] [-run <regexp>]
//
// Each run sets ESCALIER_DIAG_TRACE=1, which makes the solver print every diagnostic of a
// top-level run to standard output. traceDiagnostics in internal/solver/diagnostic_trace.go
// describes the format. `go test -json` attributes each printed line to the test that was
// running, and -parallel 1 keeps two tests of one package from running at once. A test's
// pass or failure does not matter to the report.
//
// The attribution can name the test before the one that printed a line, when a paused
// parallel test resumes. So two diagnostics match when they come from the same program,
// which the trace names by a hash of its sources, rather than from the same test. A script
// has no such key, and its diagnostics match only within one test.
//
// The `old` side is the run with the pass off, and the `new` side the run with it on.
package main

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/escalier-lang/escalier/internal/set"
)

// tracePrefix starts each line the solver prints for a diagnostic. It matches
// diagnosticTracePrefix in internal/solver/diagnostic_trace.go.
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

	// A test binary that panics or does not build stops printing diagnostics, so the
	// report would show what it never printed as differences. The problems come first so
	// a reader sees them before the differences they explain.
	writeProblems(out, "old", off.problems)
	writeProblems(out, "new", on.problems)
	writeReport(out, diffRuns(off.diags, on.diags))

	if n := len(off.problems) + len(on.problems); n > 0 {
		return fmt.Errorf("%d test binaries panicked, failed to build, or failed outside any test, so the report is incomplete", n)
	}
	return nil
}

// writeProblems writes each problem of the run named side to w.
func writeProblems(w io.Writer, side string, problems []string) {
	for _, p := range problems {
		fmt.Fprintf(w, "warning: %s run: %s\n", side, p)
	}
}

// traceTests runs `go test -json` over packages with ESCALIER_OWNERSHIP_DATAFLOW set to
// flagValue and returns the diagnostics the tests printed. A failing test does not stop the
// run. A non-zero exit from `go test` is an error only when no package reported a failure,
// as when a package pattern matches nothing.
func traceTests(packages []string, runPattern, flagValue string, errOut io.Writer) (trace, error) {
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
		return trace{}, err
	}

	if err := cmd.Start(); err != nil {
		return trace{}, err
	}

	tr, parseErr := parseTrace(stdout)
	waitErr := cmd.Wait()
	if parseErr != nil {
		return trace{}, parseErr
	}

	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return trace{}, waitErr
	}

	if exitErr != nil && tr.failedPackages == 0 {
		return trace{}, fmt.Errorf("go test exited with %v and reported no failed package", exitErr)
	}

	return tr, nil
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

// trace is what one run of the tests printed.
type trace struct {
	// diags lists the diagnostics in the order they were printed.
	diags []traced
	// problems lists each output line that says a test binary panicked or a package did
	// not build, prefixed with the test or package that printed it. It also lists each
	// package that failed although none of its tests failed, such as a package whose test
	// binary rejects a -run pattern.
	problems []string
	// failedPackages counts the packages that reported a failure.
	failedPackages int
}

// problemMarkers are the texts `go test` prints at the end of a line when a package fails to
// build or set up.
var problemMarkers = []string{"[build failed]", "[setup failed]"}

// isProblem reports whether line says a test binary panicked or a package did not build. Go
// prints a panic a test recovered from with a `[recovered]` suffix and then again without
// it, so only the second line counts.
func isProblem(line string) bool {
	if strings.HasPrefix(line, "panic: ") {
		return !strings.Contains(line, "[recovered]")
	}

	for _, marker := range problemMarkers {
		if strings.HasSuffix(line, marker) {
			return true
		}
	}
	return false
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

// partialLine is the start of an output line that `go test -json` split across several
// output events, and the test the first event names.
type partialLine struct {
	test string
	text strings.Builder
}

// parseTrace reads a `go test -json` stream and returns the diagnostics the tests printed
// and the problems the stream reports. Other lines are skipped.
//
// `go test -json` splits an output line longer than its buffer across several output
// events, and only the last of them ends in a newline. parseTrace joins the events of each
// package until a newline before it reads the line.
func parseTrace(r io.Reader) (trace, error) {
	var out trace
	partial := map[string]*partialLine{}
	failedTests := set.NewSet[string]()
	problemPkgs := set.NewSet[string]()

	readLine := func(pkg, test, text string) {
		if !strings.HasPrefix(text, tracePrefix) {
			if line := strings.TrimSpace(text); isProblem(line) {
				out.problems = append(out.problems, testKey(pkg, test)+": "+line)
				problemPkgs.Add(pkg)
			}
			return
		}

		fields := strings.SplitN(strings.TrimSuffix(strings.TrimPrefix(text, tracePrefix), "\n"), "\t", 4)
		if len(fields) != 4 {
			return
		}

		key := testKey(pkg, test)
		program := fields[0]
		if program == "" {
			program = key
		}

		out.diags = append(out.diags, traced{Test: key, Diag: diagnostic{
			Program: program,
			Type:    fields[1],
			Span:    normalize(fields[2]),
			Message: normalize(fields[3]),
		}})
	}

	// flush reads the unfinished line of pkg, if any.
	flush := func(pkg string) {
		if p, ok := partial[pkg]; ok {
			delete(partial, pkg)
			readLine(pkg, p.test, p.text.String())
		}
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<26)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}

		var ev testEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return trace{}, fmt.Errorf("parsing go test output: %w", err)
		}

		switch {
		case ev.Action == "output":
			p, ok := partial[ev.Package]
			if !ok {
				p = &partialLine{test: ev.Test}
				partial[ev.Package] = p
			}

			p.text.WriteString(ev.Output)
			if strings.HasSuffix(ev.Output, "\n") {
				flush(ev.Package)
			}
		case ev.Action == "fail" && ev.Test != "":
			failedTests.Add(ev.Package)
		case ev.Action == "fail":
			flush(ev.Package)
			out.failedPackages++

			if !failedTests.Contains(ev.Package) && !problemPkgs.Contains(ev.Package) {
				out.problems = append(out.problems, ev.Package+": the package failed but none of its tests did")
			}
		case ev.Test == "" && (ev.Action == "pass" || ev.Action == "skip"):
			flush(ev.Package)
		}
	}

	for _, pkg := range slices.Sorted(maps.Keys(partial)) {
		flush(pkg)
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
