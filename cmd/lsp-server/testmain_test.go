package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/escalier-lang/escalier/internal/interop"
)

func TestMain(m *testing.M) {
	if err := interop.SetBuiltinsDirForTest(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
