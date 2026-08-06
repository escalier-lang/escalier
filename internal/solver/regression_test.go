package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// A recursive function whose body is an object literal containing the recursive
// call builds a cyclic var graph THROUGH an object property. coalesce's ObjectType
// case must thread the path-scoped `seen` set (like the FuncType/TupleType cases)
// or the cycle is never detected and coalescing never terminates.
//
// The cycle closes as a μ-knot, so the recursive position names the shape it stands for. The one
// unrolled level in front of it is a monomorphic-recursion artifact: each call site instantiates
// its own return variable, so the outer object comes from the call and the knot from the variable
// the body's recursive call flows through.
//
// The recursion is unguarded, so `f` cannot return and checkCanReturn rejects it. What this test
// pins is that coalescing TERMINATES on the cyclic graph, which it has to do before any diagnostic
// can be reported at all.
//
// NOTE: a regression that bypasses the `seen` guard stack-overflows here, which
// is a fatal (uncatchable) crash that takes down the whole package test binary
// rather than failing this test in isolation. Tracked in
// https://github.com/escalier-lang/escalier/issues/702 (add a recursion-depth
// ceiling to coalesce so a guard bypass fails cleanly instead of crashing).
func TestInferModuleRecursiveRecordTerminates(t *testing.T) {
	values, _, errs := inferSource(t, `fn f() { return {x: f()} }`)
	require.Equal(t, []string{nonReturningMsg("1:4-1:5", "f", "{x: μX0.{x: X0}}")}, messagesWithSpan(errs))
	require.Equal(t, "fn () -> {x: μX0.{x: X0}}", values["f"])
}

// A top-level FuncDecl's inferred type must be recorded in the Info side table on
// its name node, the same way a top-level `val` records on its pattern. Without
// this, tooling can query a `val`'s type via Info but not a `fn`'s.
func TestInferModuleFuncDeclRecordsInfoType(t *testing.T) {
	module := parseModule(t, `fn foo(x: number) -> number { return x }`)
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

// A POLYMORPHIC binding's Info entry retains its quantified type-parameter vars, so
// it must be rendered with soltype.PrintAsScheme (the var-aware renderer); plain
// soltype.Print shows the raw t{ID} debug form. (PR1: the recorded display type is
// NOT var-free for generalized bindings — the inverse of
// TestInferModuleFuncDeclRecordsInfoType, whose fixture is monomorphic.)
func TestInferModulePolymorphicFuncDeclInfoNeedsPrintScheme(t *testing.T) {
	module := parseModule(t, `fn id(x) { return x }`)
	_, info, errs := InferModule(module)
	require.Empty(t, errs)

	var id *ast.FuncDecl
	module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, d := range ns.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Name != nil && fd.Name.Name == "id" {
				id = fd
				return false
			}
		}
		return true
	})
	require.NotNil(t, id, "id decl not found")

	got := info.TypeOf(id.Name)
	require.NotNil(t, got, "FuncDecl type not recorded in Info")
	require.Equal(t, "fn <T0>(x: T0) -> T0", soltype.PrintAsScheme(got))
	require.NotEqual(t, soltype.PrintAsScheme(got), soltype.Print(got),
		"plain Print leaks raw var IDs for a generalized binding; consumers must use PrintAsScheme")
}
