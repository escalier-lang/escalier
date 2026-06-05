package solver

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// parseModule parses a single in-memory source and fails the test on parse
// errors, returning the module for inspection (the Info side table, decl nodes).
func parseModule(t *testing.T, src string) *ast.Module {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{
		{ID: 0, Path: "input.esc", Contents: src},
	})
	require.Empty(t, parseErrors, "expected no parse errors")
	return module
}

// A recursive function whose body is a record literal containing the recursive
// call builds a cyclic var graph THROUGH a record field. coalesce's RecordType
// case must thread the path-scoped `seen` set (like the FuncType/TupleType cases)
// or the cycle is never detected and coalescing never terminates. A regression
// here manifests as non-termination (runaway recursion / stack overflow) on this
// otherwise trivial input.
func TestInferModuleRecursiveRecordTerminates(t *testing.T) {
	values, _, _ := inferSource(t, `fn f() { {x: f()} }`)
	require.Equal(t, "fn () -> {x: {x: never}}", values["f"])
}

// A top-level FuncDecl's inferred type must be recorded in the Info side table on
// its name node, the same way a top-level `val` records on its pattern. Without
// this, tooling can query a `val`'s type via Info but not a `fn`'s.
func TestInferModuleFuncDeclRecordsInfoType(t *testing.T) {
	module := parseModule(t, `fn foo(x: number) -> number { x }`)
	_, info, errs := InferModule(module)
	require.Empty(t, errs)

	var foo *ast.FuncDecl
	module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, d := range ns.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Name != nil && fd.Name.Name == "foo" {
				foo = fd
				return false
			}
		}
		return true
	})
	require.NotNil(t, foo, "foo decl not found")

	got := info.TypeOf(foo.Name)
	require.NotNil(t, got, "FuncDecl type not recorded in Info")
	require.Equal(t, "fn (x: number) -> number", soltype.Print(got))
}
