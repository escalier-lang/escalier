// Spike for planning/builtins/implementation_plan.md §4.1.
//
// Throwaway prototype that answers two architectural questions before
// committing to a §4 implementation shape:
//
//  Q1. Can the existing checker machinery be parameterized by a
//      per-importing-file "active augmentation set" derived from the
//      file's resolved imports?
//
//  Q2. Does indexed access `HTMLElementTagNameMap[K]` re-resolve
//      against the per-file augmentation set, or does it snapshot at
//      registry-declaration time?
//
// We model a tiny slice of the web stdlib: `web:dom` declares an empty
// `HTMLElementTagNameMap` registry and a generic
// `createElement<K extends keyof HTMLElementTagNameMap>(tag: K) ->
// HTMLElementTagNameMap[K]`. `web:canvas` augments the registry with
// `canvas: HTMLCanvasElement` and re-exports `HTMLCanvasElement` as a
// nominal class.
//
// Two scenarios:
//   - importerHasCanvas: file imports both `web:dom` and `web:canvas`,
//     calls `createElement("canvas")`.
//   - importerNoCanvas: file imports only `web:dom`, calls
//     `createElement("canvas")`.
//
// Per FR9, augmentation visibility is per-importing-file: the importer
// scenario should see `HTMLCanvasElement`; the sibling scenario should
// see `never` (or some "no such key" diagnostic).
//
// This file is committed as the §4.1 spike scaffolding. If the §4
// implementation lands and obviates it, delete it.

package tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/require"
)

// stagedStdlib lays out a tiny stdlib data tree under a temp dir:
//
//	<tmp>/std/.keep        (placeholder; looksLikeStdlibDir requires std/)
//	<tmp>/web/dom.esc      (base registry + createElement)
//	<tmp>/web/canvas.esc   (augmentation + HTMLCanvasElement)
//
// The full path is returned and pre-loaded into ESCALIER_STDLIB_DIR.
func stagedStdlib(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "std"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "web"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "std", ".keep"), nil, 0o644))

	dom := `
@js("document.createElement")
export declare fn createElement<K: keyof HTMLElementTagNameMap>(tag: K) -> HTMLElementTagNameMap[K]

export declare interface HTMLElementTagNameMap {}
`
	canvas := `
import "web:dom?flat"

@js("HTMLCanvasElement")
export declare class HTMLCanvasElement {
    width: number,
    height: number,
}

export declare interface HTMLElementTagNameMap {
    canvas: HTMLCanvasElement,
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "web", "dom.esc"), []byte(dom), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "web", "canvas.esc"), []byte(canvas), 0o644))
	t.Setenv("ESCALIER_STDLIB_DIR", root)
	return root
}

func inferSpike(t *testing.T, src string) (map[string]string, []Error) {
	t.Helper()
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: src}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	module, parseErrs := parser.ParseLibFiles(ctx, []*ast.Source{source})
	require.Empty(t, parseErrs)

	c := NewChecker(ctx)
	inferCtx := Context{Scope: Prelude(c)}
	_, inferErrs := c.InferModule(inferCtx, module)

	types := map[string]string{}
	for name, b := range inferCtx.Scope.Namespace.Values {
		types[name] = b.Type.String()
	}
	for name, ta := range inferCtx.Scope.Namespace.Types {
		types["type:"+name] = ta.Type.String()
	}
	return types, inferErrs
}

// TestSpikeAugmentation_FlatCollision pins down the FR4 ?flat
// collision behavior: two packages contributing the same interface
// name into the same file scope error out. There is no implicit
// cross-package merge under ?flat today.
func TestSpikeAugmentation_FlatCollision(t *testing.T) {
	stagedStdlib(t)
	src := `
import "web:dom?flat"
import "web:canvas?flat"

export val el = createElement("canvas")
`
	_, errs := inferSpike(t, src)
	require.NotEmpty(t, errs)
	require.Equal(t,
		`?flat name collision: "HTMLElementTagNameMap" is contributed by both "web:dom" and "web:canvas"; rename upstream or drop one import's ?flat flag`,
		errs[0].Message())
}

// TestSpikeAugmentation_NestedNoMerge confirms that two ?nested
// imports keep their HTMLElementTagNameMap declarations as separate,
// independent types — `web.dom.HTMLElementTagNameMap` and
// `web.canvas.HTMLElementTagNameMap` are distinct, no augmentation
// flows from canvas into dom.
//
// Q1 ("can the existing merge primitive support per-importing-file
// activation?") answers NO at the language level: today the loader
// produces two unrelated interface types, so any §4 implementation
// must introduce *new* machinery to compose a merged view per file.
func TestSpikeAugmentation_NestedNoMerge(t *testing.T) {
	stagedStdlib(t)
	src := `
import "web:dom?nested"
import "web:canvas?nested"

export type CanvasViaDom = web.dom.HTMLElementTagNameMap["canvas"]
export type CanvasViaCanvas = web.canvas.HTMLElementTagNameMap["canvas"]
`
	types, errs := inferSpike(t, src)
	require.Empty(t, errs)
	// Both indexed accesses print symbolically because the printer
	// keeps IndexType as-is. The relevant signal is that they refer
	// to TWO DIFFERENT registry interfaces, not a merged one.
	require.Equal(t,
		`web.dom.HTMLElementTagNameMap["canvas"]`,
		types["type:CanvasViaDom"])
	require.Equal(t,
		`web.canvas.HTMLElementTagNameMap["canvas"]`,
		types["type:CanvasViaCanvas"])
}

// TestSpikeAugmentation_CallSiteSnapshot pins down Q2: even when the
// importing file has both packages in scope, `createElement("canvas")`
// is rejected because `K: keyof HTMLElementTagNameMap` was resolved at
// `createElement`'s declaration site (inside dom.esc) against the
// empty registry — yielding `K: never`. The call-site does NOT
// re-resolve `keyof T` / `T[K]` against the caller's active import
// set. The constraint is snapshotted at declaration time.
//
// Q2 answers SNAPSHOT: indexed-access machinery would need explicit
// re-resolution support added in §4 for any augmentation to be
// observable, even if Q1's per-file merge existed.
func TestSpikeAugmentation_CallSiteSnapshot(t *testing.T) {
	stagedStdlib(t)
	src := `
import "web:dom?nested"
import "web:canvas?nested"

export val el = web.dom.createElement("canvas")
`
	_, errs := inferSpike(t, src)
	require.NotEmpty(t, errs)
	// The empty registry collapses K's bound to `never`, so the
	// string literal "canvas" can't be passed in.
	var foundNever bool
	for _, e := range errs {
		if e.Message() == `"canvas" cannot be assigned to never` {
			foundNever = true
		}
	}
	require.True(t, foundNever,
		"expected snapshot-against-empty-registry rejection; got: %v", errs)
}

// TestSpikeAugmentation_FlatThenDeclareMergesIntoDom probes the
// suggestion: have canvas.esc do `import "web:dom?flat"` first, then
// declare its own `interface HTMLElementTagNameMap`. The hope was
// that the in-namespace merge at infer_module.go:872 would mutate
// dom's shared ObjectType, turning cross-package augmentation into
// existing-mechanism merge.
//
// Result: the merge does NOT fire. `?flat` lands the import into
// the per-scheme sub-namespace (`pkgNs.Namespaces["web"].Types["..."]`,
// see bindStdlibFlat in infer_stdlib_import.go), while canvas's own
// `interface HTMLElementTagNameMap` declaration lands at
// `pkgNs.Types["HTMLElementTagNameMap"]`. The existing-alias check
// in infer_module.go reads only the current namespace, finds
// nothing, and creates a fresh ObjectType. The two interfaces stay
// at distinct pointers.
//
// Even if we *did* hoist ?flat-imports into pkgNs.Types directly
// (so the merge fired), the result would be GLOBAL augmentation —
// the shared ObjectType.Elems mutation persists in the
// PackageRegistry-cached namespace, so every later file that
// imports `web:dom` (without importing canvas) would also see the
// canvas member. That's the TS DOM-lib model, not FR9. So this
// path is a dead end for per-file activation regardless of whether
// the namespace plumbing were rewired.
func TestSpikeAugmentation_FlatThenDeclareMergesIntoDom(t *testing.T) {
	stagedStdlib(t)
	src := `
import "web:dom?nested"
import "web:canvas?nested"
`
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: src}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	module, parseErrs := parser.ParseLibFiles(ctx, []*ast.Source{source})
	require.Empty(t, parseErrs)
	c := NewChecker(ctx)
	inferCtx := Context{Scope: Prelude(c)}
	_, inferErrs := c.InferModule(inferCtx, module)
	require.Empty(t, inferErrs)

	// Walk: web -> dom -> HTMLElementTagNameMap, then prune to
	// underlying ObjectType and count elements. Same for canvas side.
	// Look at the cached package namespaces directly via the
	// PackageRegistry — they're what carry the underlying types.
	domPkg, ok := c.PackageRegistry.Lookup("web:dom")
	require.Truef(t, ok, "web:dom not in registry")
	canvasPkg, ok := c.PackageRegistry.Lookup("web:canvas")
	require.Truef(t, ok, "web:canvas not in registry")
	asObject := func(label string, ns *type_system.Namespace) *type_system.ObjectType {
		alias, ok := ns.Types["HTMLElementTagNameMap"]
		require.Truef(t, ok, "no HTMLElementTagNameMap in %s", label)
		obj, ok := type_system.Prune(alias.Type).(*type_system.ObjectType)
		require.Truef(t, ok, "%s: not an ObjectType", label)
		return obj
	}
	domObj := asObject("web:dom", domPkg)
	canvasObj := asObject("web:canvas", canvasPkg)
	require.Empty(t, domObj.Elems,
		"expected dom's HTMLElementTagNameMap to stay empty (no cross-package leak)")
	require.Len(t, canvasObj.Elems, 1,
		"expected canvas's HTMLElementTagNameMap to have only its own member")
	require.NotSame(t, domObj, canvasObj,
		"expected dom and canvas to hold distinct ObjectType instances")
}

// TestSpikeAugmentation_FlatThenDeclareSiblingLeak is the
// per-file-activation probe: a sibling file imports ONLY web:dom
// (NOT web:canvas), but the same Checker also loaded web:canvas via
// another file's import. If canvas.esc's interface decl mutated
// dom's shared ObjectType during canvas's own load, this sibling
// will see `createElement("canvas")` succeed — which violates FR9.
func TestSpikeAugmentation_FlatThenDeclareSiblingLeak(t *testing.T) {
	stagedStdlib(t)
	// Single source unit can't have two "files" in the current test
	// helper. Instead, simulate the leak by loading canvas (which is
	// what binds it into the per-Checker PackageRegistry), then
	// having the importing file import only dom.
	src := `
import "web:canvas?nested"
import "web:dom?nested"

export val el = web.dom.createElement("canvas")
`
	_, errs := inferSpike(t, src)
	// If the leak occurred, errs is empty and el: HTMLCanvasElement.
	// If FR9 is respected (current behavior), errs contains the
	// "canvas" cannot be assigned to never rejection.
	require.NotEmpty(t, errs)
	var foundNever bool
	for _, e := range errs {
		if e.Message() == `"canvas" cannot be assigned to never` {
			foundNever = true
		}
	}
	require.True(t, foundNever,
		"expected sibling-leak rejection; got: %v", errs)
}

// TestSpikeAugmentation_SiblingMatchesImporter shows the corollary:
// a sibling file without the canvas import sees the *same* `never`
// rejection. The visibility difference §4 needs (importer succeeds,
// sibling fails) is invisible to the current pipeline because neither
// scenario succeeds — both are blocked by the snapshot at declaration
// time, regardless of which packages the file imports.
func TestSpikeAugmentation_SiblingMatchesImporter(t *testing.T) {
	stagedStdlib(t)
	src := `
import "web:dom?nested"

export val el = web.dom.createElement("canvas")
`
	_, errs := inferSpike(t, src)
	require.NotEmpty(t, errs)
	require.Equal(t,
		`"canvas" cannot be assigned to never`,
		errs[0].Message())
}
