package compiler

import (
	"fmt"
	"os"
	"testing"

	"github.com/escalier-lang/escalier/internal/solver"
	"github.com/escalier-lang/escalier/internal/stdlibdir"
)

func TestMain(m *testing.M) {
	// The solver resolves `std:prelude` from disk, and the test binary's directory is
	// not one the executable-relative lookup finds the tree from.
	if err := stdlibdir.SetStdlibDirForTest(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Every run against a cached stdlib checker panics when it writes to state the
	// stdlib load created, so a test reaching such a write fails instead of leaking it
	// into later runs.
	solver.EnableFrozenStdlibChecks()
	os.Exit(m.Run())
}
