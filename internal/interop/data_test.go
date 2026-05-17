package interop

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBuiltinFS_EmbedsExpectedSubtrees verifies the //go:embed directive
// in data.go captures the two subdirectories §6 expects.
func TestBuiltinFS_EmbedsExpectedSubtrees(t *testing.T) {
	for _, dir := range []string{"data/builtins", "data/libs"} {
		entries, err := fs.ReadDir(BuiltinFS, dir)
		require.NoError(t, err, "embed must include %s", dir)
		require.NotEmpty(t, entries, "%s must contain at least a placeholder entry", dir)
	}
}

// TestBuildBuiltinStore_EmptyAtPhase6A verifies the §6.A
// infrastructure-only state: BuiltinFS holds no .esc files yet, so
// BuildBuiltinStore returns a non-nil empty store with no errors,
// even when the TypeChecker callback is nil. From §6.B onward this
// test will need adjustment once content lands.
func TestBuildBuiltinStore_EmptyAtPhase6A(t *testing.T) {
	resetBuiltinStoreCache()
	t.Cleanup(resetBuiltinStoreCache)

	store, errs := BuildBuiltinStore(context.Background(), nil)
	require.Empty(t, errs, "Build must not error on empty BuiltinFS")
	require.NotNil(t, store)
	require.Empty(t, store.Modules, "no modules expected at §6.A")
}

// TestBuildBuiltinStore_Memoized verifies the memoization contract:
// the returned *OverrideStore pointer is shared across successful calls
// so that the prelude's identity-keyed global-scope cache stays warm.
func TestBuildBuiltinStore_Memoized(t *testing.T) {
	resetBuiltinStoreCache()
	t.Cleanup(resetBuiltinStoreCache)

	first, _ := BuildBuiltinStore(context.Background(), nil)
	second, _ := BuildBuiltinStore(context.Background(), nil)
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

	_, errs := BuildBuiltinStore(context.Background(), nil)
	require.NotEmpty(t, errs, "first call should surface the synthetic error")

	store, errs := BuildBuiltinStore(context.Background(), nil)
	require.Empty(t, errs, "second call must retry instead of replaying memoized errors")
	require.NotNil(t, store)
	require.Equal(t, 2, calls, "build function must be re-invoked after an erroring call")
}

// TestBuiltinFS_HasNoEscFilesYet pins the §6.A invariant. Once §6.B
// starts adding override `.esc` files this test should be deleted (or
// inverted to assert non-empty coverage).
func TestBuiltinFS_HasNoEscFilesYet(t *testing.T) {
	var escFiles []string
	walkErr := fs.WalkDir(BuiltinFS, "data", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
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
