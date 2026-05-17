package interop

import (
	"context"
	"embed"
	"io/fs"
	"sync"
)

// BuiltinFS embeds the built-in override `.esc` files that ship with
// the compiler. The two subtrees are:
//
//   - data/builtins/ — stdlib classes (per ECMAScript spec revision)
//   - data/libs/     — third-party FP / immutability libraries
//
// See planning/interop_mutability/implementation_plan.md §6 for the
// authoring policy and per-class checklist. Tests can substitute a
// synthetic fs.FS when invoking Build directly.
//
//go:embed data
var BuiltinFS embed.FS

// BuildBuiltinStore is the production entry point that turns BuiltinFS
// into a merged OverrideStore. It is a thin wrapper around Build that
// fixes `root=""`, `deps=nil`, and `originals=nil`: at startup the
// builtin tier stands on its own without user/project overrides or
// pre-loaded original-side module shapes.
//
// Successful builds are memoized — repeated calls return the same
// *OverrideStore pointer. The prelude's global-scope cache is keyed
// by store pointer identity, so memoization keeps that cache warm.
//
// Erroring builds are NOT memoized: a later call (e.g. with a real
// TypeChecker, or after an upstream fix) is free to retry. This
// prevents a one-time failure from poisoning every subsequent call
// for the lifetime of the process.
//
// `checker` may be nil while BuiltinFS contains no `.esc` files (§6.A
// infrastructure-only state); Build only requires a TypeChecker when
// there are files to type-check. From §6.B onwards the caller must
// supply a checker that can resolve references to lib globals.
func BuildBuiltinStore(ctx context.Context, checker TypeChecker) (*OverrideStore, []error) {
	builtinStoreMu.Lock()
	defer builtinStoreMu.Unlock()
	if builtinStore != nil {
		return builtinStore, nil
	}
	store, errs := buildBuiltinStoreFn(ctx, checker, "", nil, BuiltinFS, nil)
	if len(errs) > 0 {
		return store, errs
	}
	builtinStore = store
	return store, nil
}

// buildBuiltinStoreFn is the build-function indirection. Production
// code points it at Build; tests can swap it to inject synthetic
// failures or short-circuit work.
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
