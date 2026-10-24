package main

import (
	"os"
	"testing"

	"github.com/escalier-lang/escalier/internal/solver"
)

func TestMain(m *testing.M) {
	// Every run against a cached stdlib checker panics when it writes to state the
	// stdlib load created, so a test reaching such a write fails instead of leaking it
	// into later runs.
	solver.EnableFrozenStdlibChecks()
	os.Exit(m.Run())
}
