package tests

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
	if err := stdlibdir.SetStdlibDirForTest(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
