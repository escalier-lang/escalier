package compiler

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// useChecker points the compiler at internal/checker and at the repo's standard
// library tree. The tree matters on the solver path, which resolves `std:prelude`
// from disk, and the test binary runs from a temporary directory the lazy
// stdlibdir.StdlibDir("") walk cannot find the repo root from.
func useChecker(t *testing.T) {
	t.Helper()
	t.Setenv(CheckerEnvVar, "")
	t.Setenv("ESCALIER_STDLIB_DIR", filepath.Join("..", "interop", "data"))
}

// useSolver is useChecker with CheckerEnvVar set to the solver.
func useSolver(t *testing.T) {
	t.Helper()
	useChecker(t)
	t.Setenv(CheckerEnvVar, CheckerSolver)
}

// messages renders each diagnostic's message. The two checkers word the same fault
// differently, so a test asserting one names the checker whose wording it expects.
func messages(diags []Diagnostic) []string {
	out := make([]string, len(diags))
	for i, d := range diags {
		out[i] = d.Message()
	}
	return out
}

// libSources wraps one lib/ file as the source list the entry points take.
func libSources(contents string) []*ast.Source {
	return []*ast.Source{{ID: 0, Path: "lib/index.esc", Contents: contents}}
}

// binSource wraps one bin/ file.
func binSource(contents string) *ast.Source {
	return &ast.Source{ID: 1, Path: "bin/index.esc", Contents: contents}
}

// TestSelectBackend checks which checker CheckerEnvVar names. The old checker is
// the default, so an unset variable and a value the seam does not recognize both
// leave the compiler's behavior as it was before the solver path existed.
func TestSelectBackend(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  backend
	}{
		{name: "Unset", value: "", want: checkerBackend{}},
		{name: "Solver", value: CheckerSolver, want: solverBackend{}},
		{name: "Unrecognized", value: "nonesuch", want: checkerBackend{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(CheckerEnvVar, test.value)
			require.IsType(t, test.want, selectBackend())
		})
	}
}

// TestCheckLibReportsATypeError checks that a library's type error reaches the
// caller on either checker. The two word the fault differently, so each setting
// asserts its own checker's message.
func TestCheckLibReportsATypeError(t *testing.T) {
	const src = "export val x: number = \"hello\"\n"

	t.Run("Checker", func(t *testing.T) {
		useChecker(t)
		out := CheckLib(context.Background(), libSources(src))
		require.Empty(t, out.ParseErrors)
		require.Contains(t, messages(out.TypeErrors), `"hello" cannot be assigned to number`)
	})

	t.Run("Solver", func(t *testing.T) {
		useSolver(t)
		out := CheckLib(context.Background(), libSources(src))
		require.Empty(t, out.ParseErrors)
		require.Contains(t, messages(out.TypeErrors), `cannot constrain "hello" <: number`)
	})
}

// TestCheckBinScriptReadsTheLibrary checks the lib/ to bin/ seam on either checker:
// a script resolves a name the library declared, and reports one neither declared.
// Both settings run the same two scripts, since what is asserted is which names
// resolve rather than how a fault is worded.
func TestCheckBinScriptReadsTheLibrary(t *testing.T) {
	const lib = "export val greeting = \"hello\"\n"

	tests := []struct {
		name  string
		setup func(*testing.T)
	}{
		{name: "Checker", setup: useChecker},
		{name: "Solver", setup: useSolver},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.setup(t)
			ctx := context.Background()
			libOutput := CheckLib(ctx, libSources(lib))
			require.NotNil(t, libOutput.LibScope)

			resolved := CheckBinScript(ctx, libOutput.LibScope, binSource("val g = greeting\n"))
			require.Empty(t, resolved.ParseErrors)
			require.NotContains(t, messages(resolved.TypeErrors), "Unknown identifier: greeting")

			unresolved := CheckBinScript(ctx, libOutput.LibScope, binSource("val f = farewell\n"))
			require.Empty(t, unresolved.ParseErrors)
			require.Contains(t, messages(unresolved.TypeErrors), "Unknown identifier: farewell")
		})
	}
}

// TestCompileScriptImportsUsedLibSymbols checks that the emitted script imports the
// library names it used and no others, on either checker. This is what the LibScope
// interface's declaresTopLevel answers, and each checker answers it off its own
// representation of the library's surface.
func TestCompileScriptImportsUsedLibSymbols(t *testing.T) {
	const lib = "export val greeting = \"hello\"\nexport val unused = 0\n"

	tests := []struct {
		name  string
		setup func(*testing.T)
	}{
		{name: "Checker", setup: useChecker},
		{name: "Solver", setup: useSolver},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.setup(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			libOutput := CheckLib(ctx, libSources(lib))
			require.NotNil(t, libOutput.LibScope)

			out := CompileScript(libOutput.LibScope, binSource("val g = greeting\n"))
			require.Empty(t, out.ParseErrors)
			require.Equal(t,
				"import { greeting } from \"../lib/index.js\";\nconst g = greeting;\n"+
					"//# sourceMappingURL=./index.js.map\n",
				out.CompUnits["bin/index"].JS)
		})
	}
}

// TestCompileReportsTheSolverCodegenGap checks that a compile on the solver path
// says its output is not yet correct, and that the checker path says nothing extra.
// Codegen reads types only internal/checker stamps onto the tree, so the solver's
// JavaScript is wrong in ways no other diagnostic names and its .d.ts is empty.
func TestCompileReportsTheSolverCodegenGap(t *testing.T) {
	const gap = solverCodegenGap

	t.Run("Checker", func(t *testing.T) {
		useChecker(t)
		out := CompilePackage(append(libSources("export val greeting = \"hello\"\n"), binSource("val g = greeting\n")))
		require.NotContains(t, messages(out.TypeErrors), gap)
	})

	t.Run("Solver", func(t *testing.T) {
		useSolver(t)
		out := CompilePackage(append(libSources("export val greeting = \"hello\"\n"), binSource("val g = greeting\n")))
		// One for the library's compilation unit and one for the script's, since
		// each is a file whose emitted output is wrong.
		require.Equal(t, 2, countMessage(messages(out.TypeErrors), gap))
		require.Empty(t, out.CompUnits["lib/index"].DTS)

		// Each blames the file it is about, so a caller placing diagnostics puts
		// them on the library and on the script rather than both on one.
		blamed := []int{}
		for _, err := range out.TypeErrors {
			if err.Message() == gap {
				blamed = append(blamed, err.Span().SourceID)
			}
		}
		require.Equal(t, []int{0, 1}, blamed)
	})
}

// solverCodegenGap is the message the solver path reports for each file it emits.
const solverCodegenGap = "ESCALIER_CHECKER=solver does not yet emit correct output for this file: " +
	"codegen reads types internal/checker stamps onto the tree, so a constructor call, " +
	"a method reference, and an `if val` guard are emitted wrongly, and no .d.ts is written"

// countMessage returns how many of msgs equal want.
func countMessage(msgs []string, want string) int {
	n := 0
	for _, msg := range msgs {
		if msg == want {
			n++
		}
	}
	return n
}

// TestCompileAScriptWithNoLibrary checks the entry point a package with no lib/
// files takes: the script resolves what the prelude declares and nothing else, on
// either checker.
func TestCompileAScriptWithNoLibrary(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T)
	}{
		{name: "Checker", setup: useChecker},
		{name: "Solver", setup: useSolver},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.setup(t)
			out := Compile(&ast.Source{ID: 0, Path: "bin/index.esc", Contents: "val g = \"hello\"\n"})
			require.Empty(t, out.ParseErrors)
			require.NotContains(t, messages(out.TypeErrors), "Unknown identifier: g")
			require.Contains(t, out.CompUnits["index"].JS, `const g = "hello";`)
		})
	}
}

// TestCompileScriptImportsALibraryNamespace checks that a `namespace` block the
// library declares is imported by a script that reads through it, on either
// checker. A namespace is the second sort declaresTopLevel answers for, and each
// checker reads it off its own representation of the library.
func TestCompileScriptImportsALibraryNamespace(t *testing.T) {
	const lib = "namespace Geometry {\n\texport val origin = 0\n}\n"

	tests := []struct {
		name  string
		setup func(*testing.T)
	}{
		{name: "Checker", setup: useChecker},
		{name: "Solver", setup: useSolver},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.setup(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			libOutput := CheckLib(ctx, libSources(lib))
			require.NotNil(t, libOutput.LibScope)

			out := CompileScript(libOutput.LibScope, binSource("val o = Geometry.origin\n"))
			require.Empty(t, out.ParseErrors)
			require.Contains(t, out.CompUnits["bin/index"].JS,
				"import { Geometry } from \"../lib/index.js\";")
		})
	}
}

// TestCheckLibWithoutAStandardLibrary checks what the solver path reports when the
// standard library tree cannot be found. The run then resolves no package and
// reports the prelude classes its own rules name as missing, so this diagnostic is
// what says the cause is the tree rather than the code being checked.
func TestCheckLibWithoutAStandardLibrary(t *testing.T) {
	useSolver(t)
	empty := t.TempDir()
	t.Setenv("ESCALIER_STDLIB_DIR", empty)

	out := CheckLib(context.Background(), libSources("export val greeting = \"hello\"\n"))

	require.Empty(t, out.ParseErrors)
	want := "cannot find the standard library: ESCALIER_STDLIB_DIR=" + strconv.Quote(empty) +
		" does not contain a std/ subdirectory"
	require.Contains(t, messages(out.TypeErrors), want)

	// It blames the module's first file. The fault is in the run's configuration
	// rather than in anything written, and that file is what a caller has to place
	// a diagnostic against.
	for _, err := range out.TypeErrors {
		if err.Message() == want {
			require.Equal(t, 0, err.Span().SourceID)
		}
	}
}

// TestCompileScriptReportsTheGapOfTheCheckerThatChecked checks that the codegen gap
// follows the checker that checked the script rather than the one selected when the
// emit happens.
//
// A LibScope holds the checker that produced it, so a script checked against a
// solver library is checked by the solver whatever CheckerEnvVar says at the time.
// Reading the gap off the selected backend instead would emit solver-checked
// JavaScript with nothing saying it cannot be trusted.
func TestCompileScriptReportsTheGapOfTheCheckerThatChecked(t *testing.T) {
	useSolver(t)
	libOutput := CheckLib(context.Background(), libSources("export val greeting = \"hello\"\n"))
	require.NotNil(t, libOutput.LibScope)

	// The library keeps the solver. The variable goes back to the old checker.
	t.Setenv(CheckerEnvVar, "")

	out := CompileScript(libOutput.LibScope, binSource("val g = greeting\n"))
	require.Empty(t, out.ParseErrors)
	require.Equal(t, 1, countMessage(messages(out.TypeErrors), solverCodegenGap))
}
