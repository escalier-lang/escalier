package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestInferAliasBorrowRoundTripsLifetime borrows an alias-typed value at a declared
// lifetime and returns it: the same lifetime appears on the parameter and the return, so
// the borrow flows out at the lifetime it came in, and the pointee renders under the alias
// name `Point` rather than its expanded body. AliasType is a RefInner, so a `&'a mut Point`
// forms a RefType over the alias through the M4 borrow machinery unchanged.
func TestInferAliasBorrowRoundTripsLifetime(t *testing.T) {
	src := `
		type Point = {x: number}
		fn f<'a>(p: &'a mut Point) -> &'a mut Point { return p }
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: &'a mut Point) -> &'a mut Point", values["f"])
}

// TestInferAliasFreshReturnCarriesNoLifetime returns a freshly-constructed value annotated
// as an alias: the object literal is owned, so the result renders under the alias name with
// no borrow lifetime, matching the record case a fresh object return produces.
func TestInferAliasFreshReturnCarriesNoLifetime(t *testing.T) {
	src := `
		type Point = {x: number}
		fn f() -> Point { return {x: 5} }
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> Point", values["f"])
}

// TestInferAliasInferredBorrowElidesLifetime borrows an alias-typed value at an inferred
// lifetime that reaches nothing in the output: the borrow keeps its `&mut` but its lifetime
// name is elided, exactly as an inferred borrow of a record does.
func TestInferAliasInferredBorrowElidesLifetime(t *testing.T) {
	src := `
		type Point = {x: number}
		fn f(p: &mut Point) -> number { return p.x }
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: &mut Point) -> number", values["f"])
}

// TestExpandAliasSubstitutesLifetimeArg checks that expandAlias substitutes a reference's
// lifetime argument for the alias's lifetime-parameter var, the lifetime twin of the
// type-argument substitution. A body borrowing at the parameter lifetime `'p` expands to a
// borrow at the concrete argument the reference supplies. The parser does not yet bind a
// lifetime parameter on a `type` declaration, so the def is built directly.
func TestExpandAliasSubstitutesLifetimeArg(t *testing.T) {
	c := newChecker()
	param := &soltype.LifetimeVar{ID: 0, Level: 1}
	body := &soltype.RefType{Mut: true, Lt: param, Inner: objT()}
	c.ctx.registerAlias("Borrow", &AliasDef{
		LifetimeParams: []*soltype.LifetimeParam{{Name: "'p", Var: param}},
		Body:           body,
	})

	arg := c.ctx.freshLifetime(1)
	got := c.ctx.expandAlias(&soltype.AliasType{Name: "Borrow", LifetimeArgs: []soltype.Lifetime{arg}})

	ref, ok := got.(*soltype.RefType)
	require.True(t, ok, "the expanded body is a borrow")
	require.Same(t, arg, ref.Lt, "the parameter lifetime is substituted with the reference's argument")
}
