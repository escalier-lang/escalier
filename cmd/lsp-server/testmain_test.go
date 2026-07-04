package main

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/interop"
)

func TestMain(m *testing.M) {
	if err := interop.SetBuiltinsDirForTest(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Load and type-check the TypeScript prelude once, without a deadline, so
	// its cost is paid here rather than inside a per-test timeout. checker.Prelude
	// caches the global scope package-wide, so the completion helpers'
	// subsequent calls hit the cache and only clone it. Their 1s deadlines then
	// cover just the small user snippet, not the heavy prelude load, which is
	// what made those tests flake on a slow runner.
	checker.Prelude(checker.NewChecker(context.Background()))

	os.Exit(m.Run())
}
