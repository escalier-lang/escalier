package interop

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// BuiltinsDir resolves the on-disk directory that holds the built-in
// override `.esc` files shipped with the compiler. The two subtrees are:
//
//   - data/builtins/ — stdlib classes (per ECMAScript spec revision)
//   - data/libs/     — third-party FP / immutability libraries
//
// Resolution order:
//
//  1. The ESCALIER_BUILTINS_DIR environment variable, if set. Useful
//     for tests and for power users who want to point at a checkout.
//  2. Walking up from the executable's directory, looking for an
//     `escalier.toml` marker. This covers both the in-repo build
//     (`./bin/escalier`) and any distribution that ships the binary
//     next to the repo layout.
//  3. Walking up from the current working directory, same marker.
//     This is the path that `go test ./internal/interop/...` takes.
//
// On success the returned path is the `<root>/internal/interop/data`
// directory; the loader takes an `fs.FS` rooted there.
//
// See planning/interop_mutability/implementation_plan.md §6 for the
// authoring policy and per-class checklist.
func BuiltinsDir() (string, error) {
	if env := os.Getenv("ESCALIER_BUILTINS_DIR"); env != "" {
		return env, nil
	}
	var starts []string
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	if cwd, err := os.Getwd(); err == nil {
		starts = append(starts, cwd)
	}
	for _, start := range starts {
		if root := findEscalierRoot(start); root != "" {
			return filepath.Join(root, "internal", "interop", "data"), nil
		}
	}
	return "", fmt.Errorf("could not locate Escalier builtin overrides directory (set ESCALIER_BUILTINS_DIR to override)")
}

// findEscalierRoot walks up from start looking for a directory that
// has BOTH an `escalier.toml` marker AND an `internal/interop/data`
// directory. The two-key marker matters because fixture packages
// under `fixtures/<name>/` carry their own `escalier.toml` — the
// loader must walk past those and reach the actual compiler repo
// root, where the shipped overrides live. Returns "" if no such
// directory is found before reaching the filesystem root.
func findEscalierRoot(start string) string {
	dir := start
	for {
		_, tomlErr := os.Stat(filepath.Join(dir, "escalier.toml"))
		dataInfo, dataErr := os.Stat(filepath.Join(dir, "internal", "interop", "data"))
		if tomlErr == nil && dataErr == nil && dataInfo.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// BuildBuiltinStore is the production entry point that turns the
// on-disk builtins directory into a merged OverrideStore. It is a
// thin wrapper around Build that fixes `root=""`, `deps=nil`, and
// `originals=nil`: at startup the builtin tier stands on its own
// without user/project overrides or pre-loaded original-side module
// shapes.
//
// Successful builds are memoized — repeated calls return the same
// *OverrideStore pointer. The prelude's global-scope cache is keyed
// by store pointer identity, so memoization keeps that cache warm.
//
// Erroring builds are NOT memoized: a later call (e.g. with a real
// TypeChecker, or after an upstream fix) is free to retry. This
// prevents a one-time failure from poisoning every subsequent call
// for the lifetime of the process. On error the returned store is
// nil — Build's partial store is discarded so callers get either a
// usable store or just the errors, never both.
//
// `checker` may be nil while the builtins directory contains no
// `.esc` files (§6.A infrastructure-only state); Build only requires
// a TypeChecker when there are files to type-check. From §6.B onwards
// the caller must supply a checker that can resolve references to
// lib globals.
func BuildBuiltinStore(ctx context.Context, checker TypeChecker) (*OverrideStore, []error) {
	builtinStoreMu.Lock()
	defer builtinStoreMu.Unlock()
	if builtinStore != nil {
		return builtinStore, nil
	}
	dir, err := BuiltinsDir()
	if err != nil {
		return nil, []error{err}
	}
	store, errs := buildBuiltinStoreFn(ctx, checker, "", nil, os.DirFS(dir), nil)
	if len(errs) > 0 {
		return nil, errs
	}
	builtinStore = store
	return store, nil
}

// buildBuiltinStoreFn is the build-function indirection. Production
// code points it at Build; tests can swap it to inject synthetic
// failures or short-circuit work. Callers swapping this in tests
// must do so while holding builtinStoreMu if any other goroutine
// could be calling BuildBuiltinStore concurrently. The interop
// package tests are sequential, so the in-package swap is
// unsynchronized by design.
var buildBuiltinStoreFn func(
	ctx context.Context,
	checker TypeChecker,
	root string,
	deps []DepInfo,
	builtin fs.FS,
	originals map[string]*ModuleScope,
) (*OverrideStore, []error) = Build

var (
	builtinStoreMu sync.Mutex
	builtinStore   *OverrideStore
)
