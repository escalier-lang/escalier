package tests

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

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
	errs = c.InferModule(inferCtx, module)
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
		`unknown import scheme "foo"; recognized schemes: std, dom, node`,
		errs[0].Message(),
	)
}

func TestStdlibImport_UnknownPackageInKnownScheme(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import "std:nonexistent"`)
	require.Len(t, errs, 1)
	require.Contains(t, errs[0].Message(), `unknown package "nonexistent" in std: scheme`)
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
	require.Contains(t, errs[0].Message(), `named imports from pseudo-package "std:math" are not supported`)
}

func TestStdlibImport_NamespaceImportFromSchemeURIRejected(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import * as M from "std:math"`)
	require.Len(t, errs, 1)
	require.Contains(t, errs[0].Message(), `named imports from pseudo-package "std:math" are not supported`)
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

func TestStdlibImport_NestedFlagNotYetImplemented(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import "std:math?nested"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		`?nested binding-shape is not yet implemented; use ?local`,
		errs[0].Message(),
	)
}

func TestStdlibImport_FlatFlagNotYetImplemented(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import "std:math?flat"`)
	require.Len(t, errs, 1)
	require.Equal(t,
		`?flat binding-shape is not yet implemented; use ?local`,
		errs[0].Message(),
	)
}

func TestStdlibImport_InvalidPackageName(t *testing.T) {
	_, errs := inferStdlibImportSource(t, `import "std:Math"`)
	require.Len(t, errs, 1)
	require.Contains(t, errs[0].Message(), `invalid package name "Math"`)
}
