package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// traceStream renders `go test -json` output events for the package `pkg`, one per
// [test, output] pair. The output is a JSON string body, so `\t` and `\n` are escapes.
func traceStream(events ...[2]string) string {
	var b strings.Builder
	for _, ev := range events {
		b.WriteString(`{"Action":"output","Package":"pkg","Test":"` + ev[0] + `","Output":"` + ev[1] + `"}` + "\n")
	}
	return b.String()
}

// TestOwnershipDiffReport asserts the report the harness writes for two tiny runs:
//
//   - TestSame prints one diagnostic in each run, so it does not differ.
//   - TestOld prints a diagnostic only with the flag off, and TestNew one only with it on.
//   - TestCount prints one diagnostic twice with the flag off and once with it on.
//   - Program `bb` reports one diagnostic in each run, attributed to TestShiftA in one and
//     TestShiftB in the other. It matches, since diagnostics match by program.
//   - A script, which has no program key, reports one diagnostic in each run from different
//     tests. It differs, since a script's diagnostics match only within one test.
//   - TestTemp prints a diagnostic naming a different temporary directory in each run. It
//     does not differ, since the directory is normalized.
//
// Output that is not a traced diagnostic is skipped.
func TestOwnershipDiffReport(t *testing.T) {
	moved := `ESCALIER_DIAG\taa\t*solver.UseAfterMoveError\tinput.esc:3:5-3:6\tuse of moved value 'p'\n`
	write := `ESCALIER_DIAG\taa\t*solver.BorrowedValueWriteError\tinput.esc:4:5-4:8\tcannot assign to 'p.x' while it is borrowed as immutable\n`
	shifted := `ESCALIER_DIAG\tbb\t*solver.UseAfterMoveError\tinput.esc:2:1-2:2\tuse of moved value 'q'\n`
	script := `ESCALIER_DIAG\t\t*solver.UseAfterMoveError\t0:5-6\tuse of moved value 'r'\n`
	tmp := filepath.Clean(os.TempDir())
	temp := func(dir string) string {
		return `ESCALIER_DIAG\tcc\t*solver.UnresolvedPackageError\t` + tmp + `/` + dir + `/001/a.esc:1:1-1:2\tcannot read ` + tmp + `/` + dir + `/001/b.esc\n`
	}

	off, err := parseTrace(strings.NewReader("not json\n" + traceStream(
		[2]string{"TestSame", moved},
		[2]string{"TestOld", moved},
		[2]string{"TestOld", `=== RUN TestOld\n`},
		[2]string{"TestCount", moved},
		[2]string{"TestCount", moved},
		[2]string{"TestShiftA", shifted},
		[2]string{"TestScriptA", script},
		[2]string{"TestTemp", temp("TestTemp123")},
	)))
	require.NoError(t, err)

	on, err := parseTrace(strings.NewReader(traceStream(
		[2]string{"TestSame", moved},
		[2]string{"TestNew", write},
		[2]string{"TestCount", moved},
		[2]string{"TestShiftB", shifted},
		[2]string{"TestScriptB", script},
		[2]string{"TestTemp", temp("TestTemp456")},
	)))
	require.NoError(t, err)

	require.Empty(t, off.problems)
	require.Empty(t, on.problems)

	var out strings.Builder
	writeReport(&out, diffRuns(off.diags, on.diags))

	require.Equal(t, strings.Join([]string{
		"pkg TestCount",
		"\told\tinput.esc:3:5-3:6\t*solver.UseAfterMoveError\tuse of moved value 'p'",
		"pkg TestNew",
		"\tnew\tinput.esc:4:5-4:8\t*solver.BorrowedValueWriteError\tcannot assign to 'p.x' while it is borrowed as immutable",
		"pkg TestOld",
		"\told\tinput.esc:3:5-3:6\t*solver.UseAfterMoveError\tuse of moved value 'p'",
		"pkg TestScriptA",
		"\told\t0:5-6\t*solver.UseAfterMoveError\tuse of moved value 'r'",
		"pkg TestScriptB",
		"\tnew\t0:5-6\t*solver.UseAfterMoveError\tuse of moved value 'r'",
		"",
		"5 tests differ: 3 diagnostics only with the flag off, 2 only with it on",
		"     3  old *solver.UseAfterMoveError: use of moved value '…'",
		"     1  new *solver.BorrowedValueWriteError: cannot assign to '…' while it is borrowed as immutable",
		"     1  new *solver.UseAfterMoveError: use of moved value '…'",
		"",
	}, "\n"), out.String())
}

// TestOwnershipDiffProblems asserts that parseTrace reports a panic and a build failure,
// each prefixed with the test or package that printed it.
func TestOwnershipDiffProblems(t *testing.T) {
	tr, err := parseTrace(strings.NewReader(traceStream(
		[2]string{"TestCrash", `panic: runtime error: invalid memory address\n`},
		[2]string{"", `FAIL\tpkg [build failed]\n`},
	)))
	require.NoError(t, err)
	require.Empty(t, tr.diags)
	require.Equal(t, []string{
		"pkg TestCrash: panic: runtime error: invalid memory address",
		"pkg: FAIL\tpkg [build failed]",
	}, tr.problems)
}

// TestOwnershipDiffFailures asserts that parseTrace counts each failed package, and reports a
// package that failed while none of its tests did. Package `bad` fails that way, as a test
// binary does when it rejects a -run pattern. Package `pkg` fails because TestFail failed,
// which is not a problem.
func TestOwnershipDiffFailures(t *testing.T) {
	tr, err := parseTrace(strings.NewReader(strings.Join([]string{
		`{"Action":"output","Package":"bad","Output":"testing: invalid regexp for element 0 of -test.run\n"}`,
		`{"Action":"fail","Package":"bad"}`,
		`{"Action":"fail","Package":"pkg","Test":"TestFail"}`,
		`{"Action":"fail","Package":"pkg"}`,
		`{"Action":"pass","Package":"ok"}`,
	}, "\n")))
	require.NoError(t, err)
	require.Equal(t, 2, tr.failedPackages)
	require.Equal(t, []string{"bad: the package failed but none of its tests did"}, tr.problems)
}

// TestOwnershipDiffSplitLines asserts that parseTrace joins a traced diagnostic that
// `go test -json` split across several output events. Both runs print one long message
// split into three events. The messages share their first 1100 bytes and differ only in
// the last event, so the report shows both. An event of package `other` between the parts
// does not join the line.
func TestOwnershipDiffSplitLines(t *testing.T) {
	head := `ESCALIER_DIAG\taa\t*solver.UseAfterMoveError\tinput.esc:3:5-3:6\tuse of moved value '` + strings.Repeat("p", 1100)
	split := func(tail string) string {
		return strings.Join([]string{
			`{"Action":"output","Package":"pkg","Test":"TestLong","Output":"` + head[:600] + `"}`,
			`{"Action":"output","Package":"other","Test":"TestOther","Output":"=== RUN TestOther\n"}`,
			`{"Action":"output","Package":"pkg","Test":"TestLong","Output":"` + head[600:] + `"}`,
			`{"Action":"output","Package":"pkg","Test":"TestLong","Output":"` + tail + `'\n"}`,
		}, "\n")
	}

	off, err := parseTrace(strings.NewReader(split("old")))
	require.NoError(t, err)

	on, err := parseTrace(strings.NewReader(split("new")))
	require.NoError(t, err)

	var out strings.Builder
	writeReport(&out, diffRuns(off.diags, on.diags))

	name := strings.Repeat("p", 1100)
	require.Equal(t, strings.Join([]string{
		"pkg TestLong",
		"\told\tinput.esc:3:5-3:6\t*solver.UseAfterMoveError\tuse of moved value '" + name + "old'",
		"\tnew\tinput.esc:3:5-3:6\t*solver.UseAfterMoveError\tuse of moved value '" + name + "new'",
		"",
		"1 tests differ: 1 diagnostics only with the flag off, 1 only with it on",
		"     1  new *solver.UseAfterMoveError: use of moved value '…'",
		"     1  old *solver.UseAfterMoveError: use of moved value '…'",
		"",
	}, "\n"), out.String())
}
