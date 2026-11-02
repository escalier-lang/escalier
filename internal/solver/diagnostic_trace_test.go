package solver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// captureDiagnosticTrace sets diagnosticTraceEnv to value, sends the trace to a buffer for
// the rest of the test, and returns the buffer.
func captureDiagnosticTrace(t *testing.T, value string) *strings.Builder {
	t.Setenv(diagnosticTraceEnv, value)

	var out strings.Builder
	prev := diagnosticTraceOut
	diagnosticTraceOut = &out
	t.Cleanup(func() { diagnosticTraceOut = prev })
	return &out
}

// TestDiagnosticTrace asserts the lines traceDiagnostics prints for a module and a script
// when diagnosticTraceEnv is 1, and that it prints nothing for any other value.
//
// The module's call matches neither overload, and the message of that error spans three
// lines. The trace writes each newline as `\n` and keys the line by a hash of the module's
// one file. The script uses `p` after moving it. A script has no key, and its span is
// written as byte offsets after its SourceID.
func TestDiagnosticTrace(t *testing.T) {
	moduleSrc := `
		declare fn f(a: number) -> number
		declare fn f(a: boolean) -> string
		val r = f("z")
	`
	sum := sha256.Sum256([]byte(fmt.Sprintf("0:%d:%s", len(moduleSrc), moduleSrc)))
	moduleKey := hex.EncodeToString(sum[:])[:16]

	scriptSrc := `
		val p = {x: 1}
		val q = p
		val n = p.x
	`

	cases := []struct {
		name  string
		value string
		run   func(t *testing.T)
		trace string
	}{
		{
			name:  "Module",
			value: "1",
			run: func(t *testing.T) {
				InferModuleAgainstStdlib(parseModule(t, moduleSrc), benchTree, Options{})
			},
			trace: "ESCALIER_DIAG\t" + moduleKey + "\t*solver.NoMatchingOverloadError\tinput.esc:4:11-4:17\t" +
				`No matching overload for this call\n  fn (a: number) -> number\n  fn (a: boolean) -> string` + "\n",
		},
		{
			name:  "Script",
			value: "1",
			run: func(t *testing.T) {
				InferScriptAgainstStdlib(parseScript(t, scriptSrc), benchTree, Options{})
			},
			trace: "ESCALIER_DIAG\t\t*solver.UseAfterMoveError\t0:40-43\tuse of moved value 'p'\n",
		},
		{
			name:  "Off",
			value: "0",
			run: func(t *testing.T) {
				InferModuleAgainstStdlib(parseModule(t, moduleSrc), benchTree, Options{})
			},
			trace: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := captureDiagnosticTrace(t, tc.value)
			tc.run(t)
			require.Equal(t, tc.trace, out.String())
		})
	}
}
