package main

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/compiler"
	"github.com/escalier-lang/escalier/internal/interop"
	"github.com/escalier-lang/escalier/internal/stdlibdir"
)

func TestMain(m *testing.M) {
	if err := interop.SetBuiltinsDirForTest(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// A test that runs the solver resolves `std:prelude` from disk, and the test
	// binary's directory is not one the executable-relative lookup finds the tree from.
	if err := stdlibdir.SetStdlibDirForTest(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// The tests exercise the server as main configures it, on internal/checker. A
	// test that covers a feature already reading internal/solver's results calls
	// useSolver.
	useLegacyChecker()

	// Load and type-check the TypeScript prelude once, without a deadline, so
	// its cost is paid here rather than inside a per-test timeout. checker.Prelude
	// caches the global scope package-wide, so the completion helpers'
	// subsequent calls hit the cache and only clone it. Their 1s deadlines then
	// cover just the small user snippet, not the heavy prelude load, which is
	// what made those tests flake on a slow runner.
	checker.Prelude(checker.NewChecker(context.Background()))

	os.Exit(m.Run())
}

// useSolver runs every compiler entry point t calls on internal/solver, overriding
// the checker TestMain selects for the rest of the package.
func useSolver(t *testing.T) {
	t.Helper()
	t.Setenv(compiler.CheckerEnvVar, compiler.CheckerSolver)
}
