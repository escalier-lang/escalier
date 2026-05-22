package interop

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBuiltinsDir_HasExpectedSubtrees verifies the on-disk builtins
// directory contains the two subdirectories §6 expects.
func TestBuiltinsDir_HasExpectedSubtrees(t *testing.T) {
	root, err := BuiltinsDir()
	require.NoError(t, err)
	for _, sub := range []string{"builtins", "libs"} {
		entries, err := os.ReadDir(filepath.Join(root, sub))
		require.NoError(t, err, "builtins dir must include %s", sub)
		require.NotEmpty(t, entries, "%s must contain at least a placeholder entry", sub)
	}
}

// TestBuiltinsDir_EnvOverride verifies that ESCALIER_BUILTINS_DIR
// short-circuits the repo-root walk. This is the seam tests and
// power users rely on to point at an alternate checkout.
func TestBuiltinsDir_EnvOverride(t *testing.T) {
	t.Setenv("ESCALIER_BUILTINS_DIR", "/some/explicit/path")
	root, err := BuiltinsDir()
	require.NoError(t, err)
	require.Equal(t, "/some/explicit/path", root)
}

// TestBuildBuiltinStore_EmptyAtPhase6A verifies the §6.A
// infrastructure-only state: the builtins directory holds no .esc
// files yet, so BuildBuiltinStore returns a non-nil empty store with
// no errors, even when the TypeChecker callback is nil. From §6.B
// onward this test will need adjustment once content lands.
func TestBuildBuiltinStore_EmptyAtPhase6A(t *testing.T) {
	resetBuiltinStoreCache()
	t.Cleanup(resetBuiltinStoreCache)

	store, errs := BuildBuiltinStore(context.Background(), nil)
	require.Empty(t, errs, "Build must not error on empty builtins dir")
	require.NotNil(t, store)
	require.Empty(t, store.Modules, "no modules expected at §6.A")
}

// TestBuildBuiltinStore_Memoized verifies the memoization contract:
// the returned *OverrideStore pointer is shared across successful calls
// so that the prelude's identity-keyed global-scope cache stays warm.
func TestBuildBuiltinStore_Memoized(t *testing.T) {
	resetBuiltinStoreCache()
	t.Cleanup(resetBuiltinStoreCache)

	first, err1 := BuildBuiltinStore(context.Background(), nil)
	require.Empty(t, err1)
	require.NotNil(t, first)
	second, err2 := BuildBuiltinStore(context.Background(), nil)
	require.Empty(t, err2)
	require.NotNil(t, second)
	require.Same(t, first, second, "BuildBuiltinStore must return the same store on repeated calls")
}

// TestBuildBuiltinStore_DoesNotMemoizeErrors verifies that a failing
// first build does not poison every subsequent call. Errors must not
// be cached: a later call (e.g. with a real TypeChecker after §6.B
// content lands, or after an upstream fix) must be free to retry and
// succeed.
func TestBuildBuiltinStore_DoesNotMemoizeErrors(t *testing.T) {
	resetBuiltinStoreCache()
	t.Cleanup(resetBuiltinStoreCache)

	prev := buildBuiltinStoreFn
	t.Cleanup(func() { buildBuiltinStoreFn = prev })

	calls := 0
	buildBuiltinStoreFn = func(ctx context.Context, checker TypeChecker, root string, deps []DepInfo, builtin fs.FS, originals map[string]*ModuleScope) (*OverrideStore, []error) {
		calls++
		if calls == 1 {
			return NewOverrideStore(), []error{fmt.Errorf("synthetic build failure")}
		}
		return NewOverrideStore(), nil
	}

	failingStore, errs := BuildBuiltinStore(context.Background(), nil)
	require.Len(t, errs, 1, "first call should surface the synthetic error")
	require.EqualError(t, errs[0], "synthetic build failure")
	require.Nil(t, failingStore, "errored build must not return a partial store")

	store, errs := BuildBuiltinStore(context.Background(), nil)
	require.Empty(t, errs, "second call must retry instead of replaying memoized errors")
	require.NotNil(t, store)
	require.Equal(t, 2, calls, "build function must be re-invoked after an erroring call")
}

// TestBuiltinsDir_HasNoEscFilesYet pins the §6.A invariant. Once §6.B
// starts adding override `.esc` files this test should be deleted (or
// inverted to assert non-empty coverage).
func TestBuiltinsDir_HasNoEscFilesYet(t *testing.T) {
	root, err := BuiltinsDir()
	require.NoError(t, err)

	var escFiles []string
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip the stdlib scheme subtrees — those belong to the
			// builtins (FR1-FR16) workstream, not the override system
			// this invariant pins.
			if p != root && isStdlibSchemeSubtree(filepath.Base(p), "") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".esc") {
			escFiles = append(escFiles, p)
		}
		return nil
	})
	require.NoError(t, walkErr)
	require.Empty(t, escFiles, "remove this test when §6.B starts authoring .esc files")
}

// resetBuiltinStoreCache clears the package-level memoized store so
// each test can exercise the build path independently. Safe because
// no test in this package calls t.Parallel() and the cache is
// process-local.
func resetBuiltinStoreCache() {
	builtinStoreMu.Lock()
	defer builtinStoreMu.Unlock()
	builtinStore = nil
}
