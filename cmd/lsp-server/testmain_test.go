package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/escalier-lang/escalier/internal/interop"
	"github.com/escalier-lang/escalier/internal/stdlibdir"
)

func TestMain(m *testing.M) {
	if err := interop.SetBuiltinsDirForTest(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// The solver resolves `std:prelude` from disk, and the test binary's directory is
	// not one the executable-relative lookup finds the tree from.
	if err := stdlibdir.SetStdlibDirForTest(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
