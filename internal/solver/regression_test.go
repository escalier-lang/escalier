package solver

import (
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// A recursive function whose body is a record literal containing the recursive
// call builds a cyclic var graph THROUGH a record field. coalesce's RecordType
// case must thread the path-scoped `seen` set (like the FuncType/TupleType cases)
// or the cycle is never detected and coalescing never terminates.
//
// The assertion is intentionally loose: the test's real contract is that
// inference TERMINATES (reaching the assertions at all proves that) and produces
// a sane shape — a function returning a record bottoming out in `never`. The
// exact unrolling depth (`{x: {x: never}}`) is a monomorphic-recursion artifact
// that later coalesce/printer changes may legitimately alter; pinning it would
// conflate "terminates" with "renders this exact shape".
//
// NOTE: a regression that bypasses the `seen` guard stack-overflows here, which
// is a fatal (uncatchable) crash that takes down the whole package test binary
// rather than failing this test in isolation. Tracked in
// https://github.com/escalier-lang/escalier/issues/702 (add a recursion-depth
// ceiling to coalesce so a guard bypass fails cleanly instead of crashing).
func TestInferModuleRecursiveRecordTerminates(t *testing.T) {
	values, _, _ := inferSource(t, `fn f() { {x: f()} }`)
	got := values["f"]
	require.True(t, strings.HasPrefix(got, "fn () -> {x:"),
		"want a function returning a record with field x, got %q", got)
	require.Contains(t, got, "never",
		"ungrounded recursion should bottom out in never, got %q", got)
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
