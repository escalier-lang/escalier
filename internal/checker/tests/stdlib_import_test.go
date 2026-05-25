package tests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// makeCustomStdlibDir builds a t.TempDir-rooted stdlib data layout
// from a {relative-path → contents} map and returns the directory.
// Used by tests that need synthetic packages without polluting the
// committed `internal/interop/data/` tree.
func makeCustomStdlibDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "std"), 0o755))
	for rel, contents := range files {
		full := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(contents), 0o644))
	}
	return dir
}

// inferStdlibImportSource parses input as a single lib file, runs
// InferModule, and returns the file-scope namespace and inference
// errors. Tests in this file exercise scheme-prefixed imports, so the
// returned namespace is the importing file's scope (where ?local
// bindings land), not the package's namespace.
func inferStdlibImportSource(t *testing.T, input string) (fileNs map[int]*Scope, errs []Error) {
	t.Helper()
	source := &ast.Source{ID: 0, Path: "lib/main.esc", Contents: input}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	module, parseErrs := parser.ParseLibFiles(ctx, []*ast.Source{source})
	require.Empty(t, parseErrs, "expected no parse errors")

	c := NewChecker(ctx)
	inferCtx := Context{Scope: Prelude(c)}
	_, errs = c.InferModule(inferCtx, module)
	return c.FileScopes, errs
}

func errorMessages(errs []Error) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Message())
	}
	return out
}

func TestStdlibImport_BareLocalBindsByLastSegment(t *testing.T) {
	fileScopes, errs := inferStdlibImportSource(t, `
		import "std:math"
		val x: number = math.PI
	`)
	require.Empty(t, errorMessages(errs))

	fileScope, ok := fileScopes[0]
	require.True(t, ok, "file scope for source 0 missing")
	_, ok = fileScope.Namespace.GetNamespace("math")
	require.True(t, ok, "expected `math` namespace bound in the file scope")
}

func TestStdlibImport_ExplicitLocalFlag(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `
		import "std:math?local"
		val x: number = math.PI
	`)
	require.Empty(t, errorMessages(errs))
}

func TestStdlibImport_UnknownScheme(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import "foo:bar"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		`unknown import scheme "foo"; recognized schemes: std, web, node`,
		errs[0].Message(),
	)
}

func TestStdlibImport_UnknownPackageInKnownScheme(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import "std:nonexistent"`)
	require.Len(t, errs, 1)
	// Stdlib dir is set by TestMain via SetStdlibDirForTest; interpolate
	// it so the full message matches across machines.
	stdlibDir := os.Getenv("ESCALIER_STDLIB_DIR")
	require.Equal(t,
		fmt.Sprintf(`unknown package "nonexistent" in std: scheme (no std/nonexistent.esc under %s)`, stdlibDir),
		errs[0].Message(),
	)
}

func TestStdlibImport_NodeSchemeReserved(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import "node:fs"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		`"node:fs": node:* is reserved; not yet populated`,
		errs[0].Message(),
	)
}

func TestStdlibImport_NamedImportFromSchemeURIRejected(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import { PI } from "std:math"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		"named imports from pseudo-package \"std:math\" are not supported; "+
			"use a bare-string import (`import \"std:math\"`) and access members through the namespace",
		errs[0].Message(),
	)
}

func TestStdlibImport_NamespaceImportFromSchemeURIRejected(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import * as M from "std:math"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		"named imports from pseudo-package \"std:math\" are not supported; "+
			"use a bare-string import (`import \"std:math\"`) and access members through the namespace",
		errs[0].Message(),
	)
}

func TestStdlibImport_UnknownFlag(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import "std:math?wat"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		`unknown import flag "wat"; recognized flags: local`,
		errs[0].Message(),
	)
}

func TestStdlibImport_DuplicateFlag(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import "std:math?local&local"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		`duplicate import flag "local"`,
		errs[0].Message(),
	)
}

func TestStdlibImport_SingleClassShortcut(t *testing.T) {
	// std:array stub exposes `class Array<T>` — FR5 binds the class
	// with its original capitalization (not lowercased "array").
	fileScopes, errs := inferStdlibImportSource(t, `
		import "std:array"
		val isArr: boolean = Array.isArray(0)
		val arr: Array<number> = Array(5)
	`)
	require.Empty(t, errorMessages(errs))

	fileScope := fileScopes[0]
	_, hasValue := fileScope.Namespace.Values["Array"]
	require.True(t, hasValue, "expected Array value binding")
	_, hasType := fileScope.Namespace.Types["Array"]
	require.True(t, hasType, "expected Array type binding")

	// The lowercased fallback namespace should NOT be present when the
	// shortcut fires.
	_, hasNs := fileScope.Namespace.GetNamespace("array")
	require.False(t, hasNs, "single-class shortcut should suppress lowercased namespace")
}

func TestStdlibImport_InvalidPackageName(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import "std:Math"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		`invalid package name "Math" in std:Math; expected lowercase letters, digits, and underscores`,
		errs[0].Message(),
	)
}

// TestStdlibImport_LoaderRule_MissingJSDecorator pins loader rule §3.4(1):
// every exported value-level decl in a pseudo-package file must carry
// an `@js("...")` decorator. The error is anchored to the importing
// `import` statement, not a location inside the stdlib file.
func TestStdlibImport_LoaderRule_MissingJSDecorator(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"std/example.esc": "export val PI: number = 3.14",
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `import "std:example"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		fmt.Sprintf("exported value %q in pseudo-package file %s is missing an `@js(\"...\")` decorator",
			"PI", filepath.Join(dir, "std/example.esc")),
		errs[0].Message())
}

// TestStdlibImport_LoaderRule_UnexportedValueLevelRejected pins loader
// rule §3.4(2): unexported value-level decls in pseudo-package files
// are rejected (no runtime mapping, invisible to importers — almost
// certainly a missing `export`). The diagnostic tells the user how to
// fix it.
func TestStdlibImport_LoaderRule_UnexportedValueLevelRejected(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"std/example.esc": "@js(\"helper\")\ndeclare val helper: number",
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `import "std:example"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		fmt.Sprintf("unexported value %q in pseudo-package file %s has no runtime mapping; "+
			"add `export` (and an `@js(...)` decorator) or remove the declaration",
			"helper", filepath.Join(dir, "std/example.esc")),
		errs[0].Message())
}

// TestStdlibImport_LoaderRule_AcceptsValidPackage confirms the loader
// rules don't false-positive on a correctly-authored pseudo-package
// (every exported value-level decl has `@js("...")`; every type-level
// decl is exported).
func TestStdlibImport_LoaderRule_AcceptsValidPackage(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"std/example.esc": "@js(\"parseInt\")\nexport declare fn foo() -> number\n" +
			"export declare type Helper = number",
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `import "std:example"`)
	require.Empty(t, errorMessages(errs))
}

// TestStdlibImport_LocalBindingSharesPkgNsPointer pins the
// share-by-pointer model: a `?local` file-scope binding for a stdlib
// pkg holds the *same* `*type_system.Namespace` instance that the
// PackageRegistry caches. Combined with the §3.4 rule that forbids
// unexported decls in stdlib pkgs, this lets importers see the
// canonical declarations without an intervening filtered copy.
func TestStdlibImport_LocalBindingSharesPkgNsPointer(t *testing.T) {
	// std:math has no class whose name matches the pkg name, so the
	// single-class shortcut doesn't fire and `?local` binds the pkg as
	// a namespace under `math` — the right shape for the pointer
	// comparison. std:array would route through the shortcut and bind
	// the class directly.
	source := &ast.Source{ID: 0, Path: "lib/main.esc", Contents: `
		import "std:math"
	`}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	module, parseErrs := parser.ParseLibFiles(ctx, []*ast.Source{source})
	require.Empty(t, parseErrs)

	c := NewChecker(ctx)
	inferCtx := Context{Scope: Prelude(c)}
	_, errs := c.InferModule(inferCtx, module)
	require.Empty(t, errorMessages(errs))

	canonical, ok := c.PackageRegistry.Lookup("std:math")
	require.True(t, ok, "expected std:math in PackageRegistry")
	require.NotNil(t, canonical)

	fileScope := c.FileScopes[0]
	bound, ok := fileScope.Namespace.GetNamespace("math")
	require.True(t, ok, "expected `math` namespace from import")
	require.Same(t, canonical, bound,
		"?local binding should share the canonical pkgNs pointer, not a filtered copy")
}

// TestStdlibImport_LoaderRule_UnexportedTypeLevelRejected pins the
// extended §3.4 rule: unexported type-level decls in pseudo-package
// files are rejected. The canonical pkgNs is shared by reference into
// importers' file scopes (so they can resolve qualified refs through
// the registry-cached namespace tree), which means leaving unexported
// members in pkgNs would leak them to importers. Forbidding them at
// the loader makes the share-by-pointer model safe.
func TestStdlibImport_LoaderRule_UnexportedTypeLevelRejected(t *testing.T) {
	cases := []struct {
		name string
		src  string
		decl string // human-readable kind+name for the expected diagnostic
	}{
		{
			name: "TypeAlias",
			src:  "declare type Helper = number",
			decl: `type "Helper"`,
		},
		{
			name: "Interface",
			src:  "declare interface Helper {}",
			decl: `interface "Helper"`,
		},
		{
			name: "Enum",
			src:  "declare enum Helper {}",
			decl: `enum "Helper"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := makeCustomStdlibDir(t, map[string]string{
				"std/example.esc": tc.src,
			})
			t.Setenv("ESCALIER_STDLIB_DIR", dir)

			_, errs := inferStdlibImportSource(t, `import "std:example"`)
			require.Len(t, errs, 1)
			require.Equal(t,
				fmt.Sprintf("unexported %s in pseudo-package file %s would leak to importers "+
					"through the shared package namespace; "+
					"add `export` or remove the declaration",
					tc.decl, filepath.Join(dir, "std/example.esc")),
				errs[0].Message())
		})
	}
}

// TestStdlibImport_LoaderRule_MalformedJSDecorator pins the loader's
// shape check on `@js(...)`: the argument must be a single string
// literal. Non-string args and zero/multi-arg forms are rejected
// uniformly. The parser accepts any positional expression list to leave
// room for future decorators, so this rule lives in the loader.
func TestStdlibImport_LoaderRule_MalformedJSDecorator(t *testing.T) {
	cases := map[string]string{
		"NonStringArg": "@js(123)\nexport declare val PI: number",
		"MultipleArgs": "@js(\"a\", \"b\")\nexport declare val PI: number",
		"NoArgs":       "@js()\nexport declare val PI: number",
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			dir := makeCustomStdlibDir(t, map[string]string{
				"std/example.esc": source,
			})
			t.Setenv("ESCALIER_STDLIB_DIR", dir)

			_, errs := inferStdlibImportSource(t, `import "std:example"`)
			require.Len(t, errs, 1)
			require.Equal(t,
				fmt.Sprintf("`@js` decorator on value %q in pseudo-package file %s must take a single string-literal argument",
					"PI", filepath.Join(dir, "std/example.esc")),
				errs[0].Message())
		})
	}
}

// TestStdlibImport_LoaderRule_UnknownJSGlobal pins loader rule §3.4(4):
// the `@js("...")` argument must name a known JS runtime path. A typo
// like `@js("Mat.sin")` is caught at load time with the file,
// declaration, and decorator argument named in the diagnostic. When
// the arg is dotted, the diagnostic identifies whether the prefix or
// the member is the unknown part so the user can act on it without
// guessing.
func TestStdlibImport_LoaderRule_UnknownJSGlobal(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"std/example.esc": "@js(\"Mat.sin\")\nexport declare fn sin(x: number) -> number",
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `import "std:example"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		fmt.Sprintf("`@js(%q)` on function %q in pseudo-package file %s does not name a known JS runtime global (prefix %q is not a known top-level global)",
			"Mat.sin", "sin", filepath.Join(dir, "std/example.esc"), "Mat"),
		errs[0].Message())
}

// TestStdlibImport_LoaderRule_UnknownJSGlobalMember pins the
// member-typo flavour of rule 4: when the prefix IS a known top-level
// global but the dotted member is not on it, the diagnostic says so —
// so the user knows `Math` is fine and `sni` is the typo.
func TestStdlibImport_LoaderRule_UnknownJSGlobalMember(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"std/example.esc": "@js(\"Math.sni\")\nexport declare fn sin(x: number) -> number",
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `import "std:example"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		fmt.Sprintf("`@js(%q)` on function %q in pseudo-package file %s does not name a known JS runtime global (%q has no known runtime member %q)",
			"Math.sni", "sin", filepath.Join(dir, "std/example.esc"), "Math", "sni"),
		errs[0].Message())
}

// TestStdlibImport_LoaderRule_AllowList pins that hand-authored
// Escalier-specific names (currently `Symbol.customMatcher`) bypass
// rule 4 even though they don't appear in lib.*.d.ts.
func TestStdlibImport_LoaderRule_AllowList(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"std/example.esc": "@js(\"Symbol.customMatcher\")\nexport declare val customMatcher: symbol",
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `import "std:example"`)
	require.Empty(t, errorMessages(errs))
}

// TestStdlibImport_LoaderRule_TypeOnlyGlobalRejected pins that a name
// that exists only at the type level in the prelude (no runtime value
// counterpart) does NOT satisfy rule 4 — `@js("PromiseLike")` would
// produce a `ReferenceError` at runtime, which is exactly the failure
// mode rule 4 exists to catch.
func TestStdlibImport_LoaderRule_TypeOnlyGlobalRejected(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"std/example.esc": "@js(\"PromiseLike\")\nexport declare fn p() -> number",
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `import "std:example"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		fmt.Sprintf("`@js(%q)` on function %q in pseudo-package file %s does not name a known JS runtime global",
			"PromiseLike", "p", filepath.Join(dir, "std/example.esc")),
		errs[0].Message())
}

// TestStdlibImport_LoaderRule_KnownGlobals confirms that representative
// JS runtime paths used by current and near-future stubs validate
// against the lib-extracted globals set: a top-level function
// (`parseInt`), a namespace member (`Math.PI`), a class constructor
// (`Date`), and a class static method (`Array.isArray`).
func TestStdlibImport_LoaderRule_KnownGlobals(t *testing.T) {
	cases := map[string]string{
		"TopLevelFn":     "@js(\"parseInt\")\nexport declare fn parseInt(s: string) -> number",
		"NamespaceMem":   "@js(\"Math.PI\")\nexport declare val PI: number",
		"ClassCtor":      "@js(\"Date\")\nexport declare class Date { constructor(mut self) }",
		"ClassStaticMem": "@js(\"Array.isArray\")\nexport declare fn isArray(v: unknown) -> boolean",
		// `Intl` lands in GlobalScope.Namespace.Namespaces (a
		// sub-Namespace), not .Values — `declare namespace Intl { ... }`
		// in lib.es5. Rule 4 must walk sub-namespaces too.
		"DeclaredNs":       "@js(\"Intl\")\nexport declare val Intl: unknown",
		"DeclaredNsMember": "@js(\"Intl.Collator\")\nexport declare val Collator: unknown",
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			dir := makeCustomStdlibDir(t, map[string]string{
				"std/example.esc": source,
			})
			t.Setenv("ESCALIER_STDLIB_DIR", dir)

			_, errs := inferStdlibImportSource(t, `import "std:example"`)
			require.Empty(t, errorMessages(errs))
		})
	}
}

// TestStdlibImport_PseudoPackageCycle pins §4.3's "cycles between
// pseudo-packages are permitted" rule. Two `web:*` packages with a
// mutual import — modeled on the canonical `HTMLCanvasElement
// .getContext("webgl") -> WebGLRenderingContext` ↔ `WebGLRenderingContext
// .canvas: HTMLCanvasElement` pair — load as a single merged module
// so each side's qualified type references into the other resolve.
//
// All imports use the default binding shape (?local) and the
// cross-package type references inside the merged module use the
// flat `<pkg>.<name>` shape that matches both the file-scope binding
// and the dep_graph's canonical binding keys.
func TestStdlibImport_PseudoPackageCycle(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"web/dom.esc": `
import "web:webgl"

@js("HTMLCanvasElement")
export declare class HTMLCanvasElement {
    getContext(self, id: "webgl") -> webgl.WebGLRenderingContext,
}
`,
		"web/webgl.esc": `
import "web:dom"

@js("WebGLRenderingContext")
export declare class WebGLRenderingContext {
    canvas: dom.HTMLCanvasElement,
}
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	fileScopes, errs := inferStdlibImportSource(t, `
import "web:dom"

export declare fn make() -> dom.HTMLCanvasElement
`)
	require.Empty(t, errorMessages(errs))

	fileScope := fileScopes[0]
	domNs, ok := fileScope.Namespace.GetNamespace("dom")
	require.True(t, ok, "expected dom namespace bound at file scope (default ?local)")
	_, hasCanvas := domNs.Types["HTMLCanvasElement"]
	require.True(t, hasCanvas, "HTMLCanvasElement should be exposed via dom")
}

// TestStdlibImport_PseudoPackageCycleThreeWay verifies SCC handling
// generalizes beyond pairs: three packages in a 3-cycle still resolve
// each other's qualified references. The `@js("Map"/"Set"/"WeakMap")`
// targets are arbitrary placeholders chosen because they're on the
// known-globals allow-list; they bear no relationship to ClassA/B/C.
func TestStdlibImport_PseudoPackageCycleThreeWay(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"web/a.esc": `
import "web:b"

@js("Map")
export declare class ClassA {
    refB: b.ClassB,
}
`,
		"web/b.esc": `
import "web:c"

@js("Set")
export declare class ClassB {
    refC: c.ClassC,
}
`,
		"web/c.esc": `
import "web:a"

@js("WeakMap")
export declare class ClassC {
    refA: a.ClassA,
}
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `import "web:a"`)
	require.Empty(t, errorMessages(errs))
}

// TestStdlibImport_PseudoPackageCycle_NonCyclicImporter pins that a
// pseudo-package which imports into a cycle but isn't itself part of
// the cycle is loaded via the normal singleton path, not absorbed
// into the merged SCC load. `web:app → web:dom ↔ web:webgl`: app is a
// one-way importer of the cycle and must remain its own SCC.
func TestStdlibImport_PseudoPackageCycle_NonCyclicImporter(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"web/dom.esc": `
import "web:webgl"

@js("HTMLCanvasElement")
export declare class HTMLCanvasElement {
    getContext(self, id: "webgl") -> webgl.WebGLRenderingContext,
}
`,
		"web/webgl.esc": `
import "web:dom"

@js("WebGLRenderingContext")
export declare class WebGLRenderingContext {
    canvas: dom.HTMLCanvasElement,
}
`,
		"web/app.esc": `
import "web:dom"

@js("Map")
export declare class App {
    canvas: dom.HTMLCanvasElement,
}
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	fileScopes, errs := inferStdlibImportSource(t, `
import "web:app"
import "web:dom"
import "web:webgl"
`)
	require.Empty(t, errorMessages(errs))

	fileNs := fileScopes[0].Namespace
	// dom and webgl don't trigger the single-class shortcut (class
	// names don't match pkg names), so they bind under lowercased pkg
	// namespaces. app does trigger the shortcut (class App ↔ pkg app),
	// so the App class binds directly at file scope.
	for _, pkg := range []string{"dom", "webgl"} {
		_, ok := fileNs.GetNamespace(pkg)
		require.True(t, ok, "expected %s namespace bound at file scope", pkg)
	}
	_, hasAppType := fileNs.Types["App"]
	require.True(t, hasAppType, "expected App class type bound at file scope (single-class shortcut)")
}

// TestStdlibImport_PseudoPackageCycleMixedSchemes verifies the SCC
// loader handles cross-scheme cycles (a `std:*` package importing a
// `web:*` package and vice versa) as a single merged load, not just
// cycles confined to a single scheme.
//
// The user-side imports use the default binding shape (no flag →
// ?local). Both packages expose a single class whose name matches the
// pkg name case-insensitively (`Host`/`host`, `Client`/`client`), so
// the §FR5 single-class shortcut fires: the class names bind directly
// at file scope rather than under a `host`/`client` namespace. This
// proves the shortcut path stacks correctly on top of the cyclic
// merged load.
func TestStdlibImport_PseudoPackageCycleMixedSchemes(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"std/host.esc": `
import "web:client"

@js("Map")
export declare class Host {
    client: client.Client,
}
`,
		"web/client.esc": `
import "std:host"

@js("Set")
export declare class Client {
    host: host.Host,
}
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	fileScopes, errs := inferStdlibImportSource(t, `
import "std:host"
import "web:client"

export declare fn makeHost() -> Host
export declare fn makeClient() -> Client
`)
	require.Empty(t, errorMessages(errs))

	fileScope := fileScopes[0]
	_, hasHostType := fileScope.Namespace.Types["Host"]
	require.True(t, hasHostType, "single-class shortcut should bind Host directly at file scope")
	_, hasClientType := fileScope.Namespace.Types["Client"]
	require.True(t, hasClientType, "single-class shortcut should bind Client directly at file scope")
}

// TestStdlibImport_PseudoPackageCycle_DecoratorErrorNamesURI pins the
// diagnostic-label fix: when an SCC member fails the §3.4 `@js` rules,
// the error message must identify the offending member by URI
// (e.g. `web:webgl`) rather than by an opaque synthetic SCC label.
func TestStdlibImport_PseudoPackageCycle_DecoratorErrorNamesURI(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"web/dom.esc": `
import "web:webgl"

@js("HTMLCanvasElement")
export declare class HTMLCanvasElement {
    getContext(self, id: "webgl") -> webgl.WebGLRenderingContext,
}
`,
		"web/webgl.esc": `
import "web:dom"

@js("ThisGlobalDoesNotExist")
export declare class WebGLRenderingContext {
    canvas: dom.HTMLCanvasElement,
}
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `import "web:dom"`)
	expected := []string{
		"`@js(\"ThisGlobalDoesNotExist\")` on class \"WebGLRenderingContext\" in pseudo-package file web:webgl does not name a known JS runtime global",
	}
	require.Equal(t, expected, errorMessages(errs))
}

// TestStdlibImport_PseudoPackageCycle_RollbackOnFailure verifies that
// when an SCC load fails (decorator-rule violation in one member), the
// PackageRegistry is rolled back so a subsequent import of any member
// re-attempts the load and surfaces the same diagnostic. Without
// rollback, the second import would silently succeed against an empty
// staged namespace.
func TestStdlibImport_PseudoPackageCycle_RollbackOnFailure(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"web/dom.esc": `
import "web:webgl"

@js("HTMLCanvasElement")
export declare class HTMLCanvasElement {
    getContext(self, id: "webgl") -> webgl.WebGLRenderingContext,
}
`,
		"web/webgl.esc": `
import "web:dom"

@js("ThisGlobalDoesNotExist")
export declare class WebGLRenderingContext {
    canvas: dom.HTMLCanvasElement,
}
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	// Two imports in the same file share one Checker. The first triggers
	// the SCC load (and fails); the second targets the *other* member.
	// With rollback, both report the same underlying decorator error.
	// Without rollback, the second silently binds an empty namespace and
	// produces no diagnostic.
	_, errs := inferStdlibImportSource(t, `
import "web:dom"
import "web:webgl"
`)
	msg := "`@js(\"ThisGlobalDoesNotExist\")` on class \"WebGLRenderingContext\" in pseudo-package file web:webgl does not name a known JS runtime global"
	require.Equal(t, []string{msg, msg}, errorMessages(errs))
}

// TestStdlibImport_PseudoPackageCycle_RollbackOnParseError exercises
// the same rollback contract as RollbackOnFailure but via a different
// error branch in loadStdlibSCC: when one SCC member has a parse
// error, the merged-module ParseLibFiles fails. A second import of any
// member must re-attempt the load (and produce the same diagnostic),
// not silently bind an empty namespace.
func TestStdlibImport_PseudoPackageCycle_RollbackOnParseError(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		// dom.esc imports webgl cleanly so the SCC scanner sees the cycle.
		"web/dom.esc": `
import "web:webgl"

@js("HTMLCanvasElement")
export declare class HTMLCanvasElement {
    getContext(self, id: "webgl") -> webgl.WebGLRenderingContext,
}
`,
		// webgl.esc parses its import line, then fails on the trailing
		// garbage — that error surfaces during the SCC's real-load parse,
		// not during the graph scan.
		"web/webgl.esc": `
import "web:dom"

@js("WebGLRenderingContext")
export declare class WebGLRenderingContext {
    canvas: dom.HTMLCanvasElement,
}

@@@ this is not valid escalier
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `
import "web:dom"
import "web:webgl"
`)
	msgs := errorMessages(errs)
	require.NotEmpty(t, msgs, "expected parse-error diagnostics from both imports")
	// Each error is anchored to the importing statement's span. With
	// rollback, both imports re-attempt the load, so the parse-error
	// diagnostics surface at *both* import spans. Without rollback, the
	// second import finds a sentinel in the registry and silently skips
	// reloading, so all parse errors come from a single span.
	parseSpans := map[ast.Span]int{}
	for _, e := range errs {
		if strings.HasPrefix(e.Message(), "parse error in stdlib SCC:") {
			parseSpans[e.Span()]++
		}
	}
	require.Len(t, parseSpans, 2,
		"expected parse-error diagnostics anchored to both import spans (rollback should re-attempt the load); got spans=%v, msgs=%v", parseSpans, msgs)
}

// TestStdlibImport_ClosedRegistryNarrowing pins §4.4's closed-registry
// fixture: a `web:dom` package declares `HTMLElementTagNameMap`
// populated with two concrete entries and a generic `createElement`
// whose type parameter is bound by `keyof HTMLElementTagNameMap`. The
// importing file calls `createElement("canvas")` and gets back exactly
// `HTMLCanvasElement` via the indexed-access lookup at the call site.
// This is the type-system mechanism the single-`web:dom` design (§4.2)
// relies on for tag-keyed narrowing.
func TestStdlibImport_ClosedRegistryNarrowing(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"web/dom.esc": `
@js("HTMLCanvasElement")
export declare class HTMLCanvasElement {}

@js("HTMLDivElement")
export declare class HTMLDivElement {}

export type HTMLElementTagNameMap = {
    canvas: HTMLCanvasElement,
    div: HTMLDivElement,
}

@js("Document")
export declare class Document {
    createElement<K: keyof HTMLElementTagNameMap>(self, tag: K) -> HTMLElementTagNameMap[K],
}
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `
import "web:dom"

declare val doc: dom.Document
val c: dom.HTMLCanvasElement = doc.createElement("canvas")
val d: dom.HTMLDivElement = doc.createElement("div")
`)
	require.Empty(t, errorMessages(errs))
}

// TestStdlibImport_ClosedRegistryUnknownTag is the negative half of the
// §4.4 closed-registry fixture: passing a tag that isn't a key of
// `HTMLElementTagNameMap` is rejected by the `K: keyof
// HTMLElementTagNameMap` bound. The diagnostic identifies the offending
// string literal so the user can see which tag was unrecognized.
func TestStdlibImport_ClosedRegistryUnknownTag(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"web/dom.esc": `
@js("HTMLCanvasElement")
export declare class HTMLCanvasElement {}

@js("HTMLDivElement")
export declare class HTMLDivElement {}

export type HTMLElementTagNameMap = {
    canvas: HTMLCanvasElement,
    div: HTMLDivElement,
}

@js("Document")
export declare class Document {
    createElement<K: keyof HTMLElementTagNameMap>(self, tag: K) -> HTMLElementTagNameMap[K],
}
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `
import "web:dom"

declare val doc: dom.Document
val bogus = doc.createElement("does-not-exist")
`)
	require.Equal(t,
		[]string{`"does-not-exist" cannot be assigned to "canvas" | "div"`},
		errorMessages(errs),
	)
}

// TestStdlibImport_LiteralKeyedMethodOverloads pins the simplest
// literal-narrowed method overload case: two `createElement` arms
// keyed on a string literal, no generic. Mirrors
// TestStdlibImport_ClosedRegistryNarrowing but with the registry
// inlined as explicit overloads, exercising §4.6 PR-C's merge pass on
// purely literal-typed param arms.
func TestStdlibImport_LiteralKeyedMethodOverloads(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"web/dom.esc": `
@js("HTMLCanvasElement")
export declare class HTMLCanvasElement {}

@js("HTMLDivElement")
export declare class HTMLDivElement {}

@js("Document")
export declare class Document {
    createElement(self, tag: "canvas") -> HTMLCanvasElement,
    createElement(self, tag: "div") -> HTMLDivElement,
}
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `
import "web:dom"

declare val doc: dom.Document
val c: dom.HTMLCanvasElement = doc.createElement("canvas")
val d: dom.HTMLDivElement = doc.createElement("div")
`)
	require.Empty(t, errorMessages(errs))
}

// TestStdlibImport_AddEventListenerOverloads pins the event-map idiom:
// a small `HTMLElementEventMap` keyed by event name, plus per-event
// `addEventListener` overloads on a single class. Each overload narrows
// the handler's event parameter to the event-specific type, so a `click`
// handler sees `MouseEvent` and a `keydown` handler sees `KeyboardEvent`.
func TestStdlibImport_AddEventListenerOverloads(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"web/dom.esc": `
@js("Event")
export declare class Event {}

@js("MouseEvent")
export declare class MouseEvent {
    clientX: number,
}

@js("KeyboardEvent")
export declare class KeyboardEvent {
    key: string,
}

export type HTMLElementEventMap = {
    click: MouseEvent,
    keydown: KeyboardEvent,
}

@js("HTMLElement")
export declare class HTMLElement {
    addEventListener<K: keyof HTMLElementEventMap>(self, kind:K, listener: fn(ev:HTMLElementEventMap[K]) -> undefined) -> undefined,
    addEventListener(self, kind:string, listener: fn(ev:Event) -> undefined) -> undefined,
}
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `
import "web:dom"

declare val el: dom.HTMLElement
val _a = el.addEventListener("click", fn (ev) { val x: number = ev.clientX })
val _b = el.addEventListener("keydown", fn (ev) { val k: string = ev.key })
val _c = el.addEventListener("custom-anything", fn (ev) {})
`)
	require.Empty(t, errorMessages(errs))
}

// TestStdlibImport_OverloadReceiverMutMismatch pins the receiver-
// mutability uniformity check: two arms on the same method that
// disagree on `self` vs `mut self` produce an elaboration-time error
// naming both shapes. The merged signature keeps the first arm's
// receiver shape so downstream type-checking still proceeds.
func TestStdlibImport_OverloadReceiverMutMismatch(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"web/dom.esc": `
@js("Document")
export declare class Document {
    swap(self, tag: "a") -> number,
    swap(mut self, tag: "b") -> number,
}
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `
import "web:dom"
`)
	require.Equal(t,
		[]string{
			"Method 'swap' overload arms disagree on receiver shape: " +
				"first arm declares `self`, but a later arm declares `mut self`. " +
				"All overload arms must share the same receiver shape.",
		},
		errorMessages(errs),
	)
}

// TestStdlibImport_NSKeyedOverloads pins §4.4's `createElementNS`
// fixture in the method shape §4.2 actually wants: two overloads of a
// single `createElementNS` method on a `Document` class, each keyed on
// a namespace-URI literal and bound to a different registry. The call
// site picks the correct overload via literal narrowing on the
// namespace argument, and the indexed-access return type resolves to
// the concrete element class for the selected registry.
//
// This is the canary for §4.6 PR-C — before the method-elem merge pass
// landed the two same-named MethodElems collapsed to the last one and
// every call dispatched against the MathML arm. Methods on a
// `@js("Document")` class don't need per-arm `@js` targets.
func TestStdlibImport_NSKeyedOverloads(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"web/dom.esc": `
@js("SVGCircleElement")
export declare class SVGCircleElement {}

@js("SVGPathElement")
export declare class SVGPathElement {}

@js("MathMLElement")
export declare class MathMLElement {}

export type SVGElementTagNameMap = {
    circle: SVGCircleElement,
    path: SVGPathElement,
}

export type MathMLElementTagNameMap = {
    mfrac: MathMLElement,
}

@js("Document")
export declare class Document {
    createElementNS<K: keyof SVGElementTagNameMap>(self, ns: "http://www.w3.org/2000/svg", qualifiedName: K) -> SVGElementTagNameMap[K],
    createElementNS<K: keyof MathMLElementTagNameMap>(self, ns: "http://www.w3.org/1998/Math/MathML", qualifiedName: K) -> MathMLElementTagNameMap[K],
}
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `
import "web:dom"

declare val doc: dom.Document
val circ: dom.SVGCircleElement = doc.createElementNS("http://www.w3.org/2000/svg", "circle")
val frac: dom.MathMLElement = doc.createElementNS("http://www.w3.org/1998/Math/MathML", "mfrac")
`)
	require.Empty(t, errorMessages(errs))
}

// TestStdlibImport_CrossPackageTypeReference pins §4.4's cross-package
// type-reference fixture (§4.2b). A sibling `web:fetch` package
// references a type defined in `web:streams` via a qualified name. No
// cycle, no augmentation — `web:fetch` imports `web:streams`, and the
// imported namespace is reachable from the field type annotation.
func TestStdlibImport_CrossPackageTypeReference(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"web/streams.esc": `
@js("ReadableStream")
export declare class ReadableStream {}
`,
		"web/fetch.esc": `
import "web:streams"

@js("Response")
export declare class Response {
    body: streams.ReadableStream | null,
}
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	fileScopes, errs := inferStdlibImportSource(t, `
import "web:fetch"
import "web:streams"

declare val r: fetch.Response
val b: streams.ReadableStream | null = r.body
`)
	require.Empty(t, errorMessages(errs))

	fileNs := fileScopes[0].Namespace
	for _, pkg := range []string{"fetch", "streams"} {
		_, ok := fileNs.GetNamespace(pkg)
		require.True(t, ok, "expected %s namespace bound at file scope", pkg)
	}
}

// TestStdlibImport_PseudoPackageCycleStdStd pins §4.4's std↔std cycle
// fixture: cycle handling isn't web-only. Two `std:*` packages with a
// mutual import load as a single merged SCC, mirroring the web↔web
// case in TestStdlibImport_PseudoPackageCycle.
func TestStdlibImport_PseudoPackageCycleStdStd(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"std/alpha.esc": `
import "std:beta"

@js("Map")
export declare class Alpha {
    beta: beta.Beta,
}
`,
		"std/beta.esc": `
import "std:alpha"

@js("Set")
export declare class Beta {
    alpha: alpha.Alpha,
}
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	fileScopes, errs := inferStdlibImportSource(t, `
import "std:alpha"
import "std:beta"
`)
	require.Empty(t, errorMessages(errs))

	fileNs := fileScopes[0].Namespace
	// Single-class shortcut fires for both (class Alpha ↔ pkg alpha, Beta ↔ beta).
	for _, name := range []string{"Alpha", "Beta"} {
		_, ok := fileNs.Types[name]
		require.True(t, ok, "expected %s type bound at file scope via single-class shortcut", name)
	}
}
