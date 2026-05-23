package tests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "js"), 0o755))
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
		import "js:math"
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
		import "js:math?local"
		val x: number = math.PI
	`)
	require.Empty(t, errorMessages(errs))
}

func TestStdlibImport_UnknownScheme(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import "foo:bar"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		`unknown import scheme "foo"; recognized schemes: js, web, node`,
		errs[0].Message(),
	)
}

func TestStdlibImport_UnknownPackageInKnownScheme(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import "js:nonexistent"`)
	require.Len(t, errs, 1)
	// Stdlib dir is set by TestMain via SetStdlibDirForTest; interpolate
	// it so the full message matches across machines.
	stdlibDir := os.Getenv("ESCALIER_STDLIB_DIR")
	require.Equal(t,
		fmt.Sprintf(`unknown package "nonexistent" in js: scheme (no js/nonexistent.esc under %s)`, stdlibDir),
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
	_, errs := inferStdlibImportSource(t, `import { PI } from "js:math"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		"named imports from pseudo-package \"js:math\" are not supported; "+
			"use a bare-string import (`import \"js:math\"`) and access members through the namespace",
		errs[0].Message(),
	)
}

func TestStdlibImport_NamespaceImportFromSchemeURIRejected(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import * as M from "js:math"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		"named imports from pseudo-package \"js:math\" are not supported; "+
			"use a bare-string import (`import \"js:math\"`) and access members through the namespace",
		errs[0].Message(),
	)
}

func TestStdlibImport_UnknownFlag(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import "js:math?wat"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		`unknown import flag "wat"; recognized flags: flat, local, nested`,
		errs[0].Message(),
	)
}

func TestStdlibImport_MutuallyExclusiveFlags(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import "js:math?local&flat"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		`binding-shape flags "flat" and "local" are mutually exclusive; pick one`,
		errs[0].Message(),
	)
}

func TestStdlibImport_NestedBindsUnderSchemeNamespace(t *testing.T) {
	fileScopes, errs := inferStdlibImportSource(t, `
		import "js:math?nested"
		val x: number = js.math.PI
	`)
	require.Empty(t, errorMessages(errs))

	fileScope, ok := fileScopes[0]
	require.True(t, ok)
	schemeNs, ok := fileScope.Namespace.GetNamespace("js")
	require.True(t, ok, "expected `js` namespace bound in file scope")
	_, ok = schemeNs.GetNamespace("math")
	require.True(t, ok, "expected `js.math` sub-namespace")
}

func TestStdlibImport_MultipleNestedSharesSchemeNamespace(t *testing.T) {
	fileScopes, errs := inferStdlibImportSource(t, `
		import "js:math?nested"
		import "js:array?nested"
		val x: number = js.math.PI
		val isArr: boolean = js.array.Array.isArray(0)
	`)
	require.Empty(t, errorMessages(errs))

	fileScope := fileScopes[0]
	schemeNs, ok := fileScope.Namespace.GetNamespace("js")
	require.True(t, ok)
	_, ok = schemeNs.GetNamespace("math")
	require.True(t, ok)
	_, ok = schemeNs.GetNamespace("array")
	require.True(t, ok)
}

func TestStdlibImport_FlatMergesIntoSchemeNamespace(t *testing.T) {
	fileScopes, errs := inferStdlibImportSource(t, `
		import "js:math?flat"
		val x: number = js.PI
	`)
	require.Empty(t, errorMessages(errs))

	fileScope := fileScopes[0]
	schemeNs, ok := fileScope.Namespace.GetNamespace("js")
	require.True(t, ok)
	_, ok = schemeNs.Values["PI"]
	require.True(t, ok, "expected PI merged directly into `js` namespace")
}

func TestStdlibImport_FlatNameCollision(t *testing.T) {
	// Both packages export the same identifier; the second ?flat
	// import must fail with the taxonomy-aligned collision message.
	dir := makeCustomStdlibDir(t, map[string]string{
		"js/alpha.esc": `export val Common: number = 1`,
		"js/beta.esc":  `export val Common: number = 2`,
	})
	t.Setenv("ESCALIER_STDLIB_DIR", dir)

	_, errs := inferStdlibImportSource(t, `
		import "js:alpha?flat"
		import "js:beta?flat"
	`)
	require.Len(t, errs, 1)
	require.Equal(t,
		`?flat name collision: "Common" is contributed by both "js:alpha" and "js:beta"; `+
			`rename upstream or drop one import's ?flat flag`,
		errs[0].Message(),
	)
}

func TestStdlibImport_SingleClassShortcut(t *testing.T) {
	// js:array stub exposes `class Array<T>` — FR5 binds the class
	// with its original capitalization (not lowercased "array") when
	// imported as ?local.
	fileScopes, errs := inferStdlibImportSource(t, `
		import "js:array"
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
	fileScopes, errs := inferStdlibImportSource(t, `import "js:array?nested"`)
	require.Empty(t, errorMessages(errs))

	fileScope := fileScopes[0]
	schemeNs, ok := fileScope.Namespace.GetNamespace("js")
	require.True(t, ok)
	pkgNs, ok := schemeNs.GetNamespace("array")
	require.True(t, ok, "?nested must bind the package as a sub-namespace, not the class")
	_, hasArray := pkgNs.Values["Array"]
	require.True(t, hasArray, "Array class should be reachable via js.array.Array")
}

func TestStdlibImport_InvalidPackageName(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import "js:Math"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		`invalid package name "Math" in js:Math; expected lowercase letters, digits, and underscores`,
		errs[0].Message(),
	)
}
