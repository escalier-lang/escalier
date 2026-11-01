package solver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
)

// diagnosticTraceEnv is the environment variable that makes each top-level run print its
// diagnostics to standard output, one per line, when its value is 1. The diff harness in
// tools/ownership_diff reads the lines from `go test -json` output.
const diagnosticTraceEnv = "ESCALIER_DIAG_TRACE"

// diagnosticTracePrefix starts each line traceDiagnostics prints.
const diagnosticTracePrefix = "ESCALIER_DIAG\t"

// diagnosticTraceOut receives the lines traceDiagnostics prints. It is standard output.
var diagnosticTraceOut io.Writer = os.Stdout

// traceDiagnostics prints errs to diagnosticTraceOut when diagnosticTraceEnv is set. sources
// holds the files of the run by SourceID, and is nil for a script. Each line holds these
// fields, separated by tabs:
//
//   - The prefix diagnosticTracePrefix.
//   - A key naming the program, the first 16 hex digits of a SHA-256 over the contents of
//     sources in SourceID order. It is empty for a script.
//   - The error's Go type, such as `*solver.UseAfterMoveError`.
//   - The error's span, as `<path>:<line>:<column>-<line>:<column>` when sources holds its
//     file, and as `<source>:<start>-<end>` byte offsets otherwise.
//   - The error's message, with each newline written as `\n`.
func traceDiagnostics(errs []SolverError, sources map[int]*ast.Source) {
	if os.Getenv(diagnosticTraceEnv) != "1" {
		return
	}

	ids := make([]int, 0, len(sources))
	for id := range sources {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	program := ""
	if len(ids) > 0 {
		sum := sha256.New()
		for _, id := range ids {
			fmt.Fprintf(sum, "%d:%d:%s", id, len(sources[id].Contents), sources[id].Contents)
		}
		program = hex.EncodeToString(sum.Sum(nil))[:16]
	}

	for _, e := range errs {
		span := e.Span()
		at := fmt.Sprintf("%d:%s", span.SourceID, span.String())
		if src, ok := sources[span.SourceID]; ok {
			at = src.Path + ":" + ast.SpanString(src, span)
		}

		msg := strings.ReplaceAll(e.Message(), "\n", `\n`)
		fmt.Fprintf(diagnosticTraceOut, "%s%s\t%T\t%s\t%s\n", diagnosticTracePrefix, program, e, at, msg)
	}
}
