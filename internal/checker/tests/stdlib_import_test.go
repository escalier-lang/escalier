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
// Used by tests that need synthetic packages (e.g. the ?flat collision
// case) without polluting the committed `internal/interop/data/`
// tree.
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
		`unknown import flag "wat"; recognized flags: flat, local, nested`,
		errs[0].Message(),
	)
}

func TestStdlibImport_MutuallyExclusiveFlags(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import "std:math?local&flat"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		`binding-shape flags "flat" and "local" are mutually exclusive; pick one`,
		errs[0].Message(),
	)
}

func TestStdlibImport_NestedBindsUnderSchemeNamespace(t *testing.T) {
	fileScopes, errs := inferStdlibImportSource(t, `
		import "std:math?nested"
		val x: number = std.math.PI
	`)
	require.Empty(t, errorMessages(errs))

	fileScope, ok := fileScopes[0]
	require.True(t, ok)
	schemeNs, ok := fileScope.Namespace.GetNamespace("std")
	require.True(t, ok, "expected `std` namespace bound in file scope")
	_, ok = schemeNs.GetNamespace("math")
	require.True(t, ok, "expected `std.math` sub-namespace")
}

func TestStdlibImport_MultipleNestedSharesSchemeNamespace(t *testing.T) {
	fileScopes, errs := inferStdlibImportSource(t, `
		import "std:math?nested"
		import "std:array?nested"
		val x: number = std.math.PI
		val isArr: boolean = std.array.Array.isArray(0)
	`)
	require.Empty(t, errorMessages(errs))

	fileScope := fileScopes[0]
	schemeNs, ok := fileScope.Namespace.GetNamespace("std")
	require.True(t, ok)
	_, ok = schemeNs.GetNamespace("math")
	require.True(t, ok)
	_, ok = schemeNs.GetNamespace("array")
	require.True(t, ok)
}

func TestStdlibImport_FlatMergesIntoSchemeNamespace(t *testing.T) {
	fileScopes, errs := inferStdlibImportSource(t, `
		import "std:math?flat"
		val x: number = std.PI
	`)
	require.Empty(t, errorMessages(errs))

	fileScope := fileScopes[0]
	schemeNs, ok := fileScope.Namespace.GetNamespace("std")
	require.True(t, ok)
	_, ok = schemeNs.Values["PI"]
	require.True(t, ok, "expected PI merged directly into `std` namespace")
}

func TestStdlibImport_FlatNameCollision(t *testing.T) {
	// Both packages export the same identifier; the second ?flat
	// import must fail with the taxonomy-aligned collision message.
	dir := makeCustomStdlibDir(t, map[string]string{
		"std/alpha.esc": "@js(\"Math.PI\")\nexport val Common: number = 1",
		"std/beta.esc":  "@js(\"Math.E\")\nexport val Common: number = 2",
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `
		import "std:alpha?flat"
		import "std:beta?flat"
	`)
	require.Len(t, errs, 1)
	require.Equal(t,
		`?flat name collision: "Common" is contributed by both "std:alpha" and "std:beta"; `+
			`rename upstream or drop one import's ?flat flag`,
		errs[0].Message(),
	)
}

func TestStdlibImport_SingleClassShortcut(t *testing.T) {
	// std:array stub exposes `class Array<T>` — FR5 binds the class
	// with its original capitalization (not lowercased "array") when
	// imported as ?local.
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

func TestStdlibImport_SingleClassShortcutDoesNotApplyToNested(t *testing.T) {
	fileScopes, errs := inferStdlibImportSource(t, `import "std:array?nested"`)
	require.Empty(t, errorMessages(errs))

	fileScope := fileScopes[0]
	schemeNs, ok := fileScope.Namespace.GetNamespace("std")
	require.True(t, ok)
	pkgNs, ok := schemeNs.GetNamespace("array")
	require.True(t, ok, "?nested must bind the package as a sub-namespace, not the class")
	_, hasArray := pkgNs.Values["Array"]
	require.True(t, hasArray, "Array class should be reachable via std.array.Array")
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
// (every exported value-level decl has `@js("...")`; type-level decls
// have no decorator).
func TestStdlibImport_LoaderRule_AcceptsValidPackage(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"std/example.esc": "@js(\"parseInt\")\nexport declare fn foo() -> number\n" +
			"declare type Helper = number",
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `import "std:example"`)
	require.Empty(t, errorMessages(errs))
}

// TestStdlibImport_LoaderRule_MalformedJSDecorator pins the loader's
// shape check on `@js(...)`: the argument must be a single string
// literal. Non-string args and zero/multi-arg forms are rejected
// uniformly. The parser accepts any positional expression list to leave
// room for future decorators, so this rule lives in the loader.
func TestStdlibImport_LoaderRule_MalformedJSDecorator(t *testing.T) {
	cases := map[string]string{
		"NonStringArg":  "@js(123)\nexport declare val PI: number",
		"MultipleArgs":  "@js(\"a\", \"b\")\nexport declare val PI: number",
		"NoArgs":        "@js()\nexport declare val PI: number",
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
func TestStdlibImport_PseudoPackageCycle(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"web/dom.esc": `
import "web:webgl?nested"

@js("HTMLCanvasElement")
export declare class HTMLCanvasElement {
    getContext(self, id: "webgl") -> web.webgl.WebGLRenderingContext,
}
`,
		"web/webgl.esc": `
import "web:dom?nested"

@js("WebGLRenderingContext")
export declare class WebGLRenderingContext {
    canvas: web.dom.HTMLCanvasElement,
}
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	fileScopes, errs := inferStdlibImportSource(t, `
import "web:dom?nested"

export declare fn make() -> web.dom.HTMLCanvasElement
`)
	require.Empty(t, errorMessages(errs))

	fileScope := fileScopes[0]
	webNs, ok := fileScope.Namespace.GetNamespace("web")
	require.True(t, ok, "expected web namespace bound in importing file")
	domNs, ok := webNs.GetNamespace("dom")
	require.True(t, ok, "expected web.dom sub-namespace")
	_, hasCanvas := domNs.Types["HTMLCanvasElement"]
	require.True(t, hasCanvas, "HTMLCanvasElement should be exposed via web.dom")
}

// TestStdlibImport_PseudoPackageCycleThreeWay verifies SCC handling
// generalizes beyond pairs: three packages in a 3-cycle still resolve
// each other's qualified references. The `@js("Map"/"Set"/"WeakMap")`
// targets are arbitrary placeholders chosen because they're on the
// known-globals allow-list; they bear no relationship to ClassA/B/C.
func TestStdlibImport_PseudoPackageCycleThreeWay(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"web/a.esc": `
import "web:b?nested"

@js("Map")
export declare class ClassA {
    refB: web.b.ClassB,
}
`,
		"web/b.esc": `
import "web:c?nested"

@js("Set")
export declare class ClassB {
    refC: web.c.ClassC,
}
`,
		"web/c.esc": `
import "web:a?nested"

@js("WeakMap")
export declare class ClassC {
    refA: web.a.ClassA,
}
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `import "web:a?nested"`)
	require.Empty(t, errorMessages(errs))
}

// TestStdlibImport_PseudoPackageCycleMixedSchemes verifies the SCC
// loader handles cross-scheme cycles (a `std:*` package importing a
// `web:*` package and vice versa) as a single merged load, not just
// cycles confined to a single scheme.
func TestStdlibImport_PseudoPackageCycleMixedSchemes(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"std/host.esc": `
import "web:client?nested"

@js("Map")
export declare class Host {
    client: web.client.Client,
}
`,
		"web/client.esc": `
import "std:host?nested"

@js("Set")
export declare class Client {
    host: std.host.Host,
}
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `
import "std:host?nested"
import "web:client?nested"
`)
	require.Empty(t, errorMessages(errs))
}

// TestStdlibImport_PseudoPackageCycle_DecoratorErrorNamesURI pins the
// diagnostic-label fix: when an SCC member fails the §3.4 `@js` rules,
// the error message must identify the offending member by URI
// (e.g. `web:webgl`) rather than by an opaque synthetic SCC label.
func TestStdlibImport_PseudoPackageCycle_DecoratorErrorNamesURI(t *testing.T) {
	dir := makeCustomStdlibDir(t, map[string]string{
		"web/dom.esc": `
import "web:webgl?nested"

@js("HTMLCanvasElement")
export declare class HTMLCanvasElement {
    getContext(self, id: "webgl") -> web.webgl.WebGLRenderingContext,
}
`,
		"web/webgl.esc": `
import "web:dom?nested"

@js("ThisGlobalDoesNotExist")
export declare class WebGLRenderingContext {
    canvas: web.dom.HTMLCanvasElement,
}
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `import "web:dom?nested"`)
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
import "web:webgl?nested"

@js("HTMLCanvasElement")
export declare class HTMLCanvasElement {
    getContext(self, id: "webgl") -> web.webgl.WebGLRenderingContext,
}
`,
		"web/webgl.esc": `
import "web:dom?nested"

@js("ThisGlobalDoesNotExist")
export declare class WebGLRenderingContext {
    canvas: web.dom.HTMLCanvasElement,
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
import "web:dom?nested"
import "web:webgl?nested"
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
import "web:webgl?nested"

@js("HTMLCanvasElement")
export declare class HTMLCanvasElement {
    getContext(self, id: "webgl") -> web.webgl.WebGLRenderingContext,
}
`,
		// webgl.esc parses its import line, then fails on the trailing
		// garbage — that error surfaces during the SCC's real-load parse,
		// not during the graph scan.
		"web/webgl.esc": `
import "web:dom?nested"

@js("WebGLRenderingContext")
export declare class WebGLRenderingContext {
    canvas: web.dom.HTMLCanvasElement,
}

@@@ this is not valid escalier
`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `
import "web:dom?nested"
import "web:webgl?nested"
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
