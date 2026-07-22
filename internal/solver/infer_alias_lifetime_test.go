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

// TestInferLifetimeGenericAliasRoundTrip declares a lifetime-generic alias whose body is an
// immutable borrow, then round-trips it through a function that supplies the lifetime and
// type arguments. The reference renders under the alias name with its lifetime argument on
// both the parameter and the return, so the borrow flows out at the lifetime it came in.
func TestInferLifetimeGenericAliasRoundTrip(t *testing.T) {
	src := `
		type Ref<'a, T> = &'a T
		fn f<'a>(p: Ref<'a, {x: number}>) -> Ref<'a, {x: number}> { return p }
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: Ref<'a, {x: number}>) -> Ref<'a, {x: number}>", values["f"])
}

// TestInferLifetimeGenericMutAliasRoundTrip is the mutable-borrow twin: a `&'a mut T` alias
// body carries the lifetime and mutability through instantiation, so a `mut` borrow of the
// alias round-trips its lifetime the same way the immutable form does.
func TestInferLifetimeGenericMutAliasRoundTrip(t *testing.T) {
	src := `
		type MutRef<'a, T> = &'a mut T
		fn f<'a>(p: MutRef<'a, {x: number}>) -> MutRef<'a, {x: number}> { return p }
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: MutRef<'a, {x: number}>) -> MutRef<'a, {x: number}>", values["f"])
}

// TestInferLifetimeGenericAliasKeepsLifetimesDistinct references one lifetime-generic alias
// at two different lifetimes: the parameter carries 'a and the second carries 'b, so the two
// instances render under distinct lifetimes rather than collapsing onto one.
func TestInferLifetimeGenericAliasKeepsLifetimesDistinct(t *testing.T) {
	src := `
		type Ref<'a, T> = &'a T
		fn f<'a, 'b>(p: Ref<'a, {x: number}>, q: Ref<'b, {x: number}>) -> Ref<'a, {x: number}> { return p }
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a, 'b>(p: Ref<'a, {x: number}>, q: Ref<'b, {x: number}>) -> Ref<'a, {x: number}>",
		values["f"],
	)
}

// TestInferLifetimeGenericAliasIsTransparent checks that a lifetime-generic alias and its
// expanded borrow are interchangeable under subtyping. A `Ref<'a, {x: number}>` value flows
// into a plain `&'a {x: number}` target, and a plain borrow flows back into the alias, so the
// alias expands to exactly the borrow its body writes.
func TestInferLifetimeGenericAliasIsTransparent(t *testing.T) {
	src := `
		type Ref<'a, T> = &'a T
		fn f<'a>(p: Ref<'a, {x: number}>) -> &'a {x: number} { return p }
		fn g<'a>(p: &'a {x: number}) -> Ref<'a, {x: number}> { return p }
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: Ref<'a, {x: number}>) -> &{x: number}", values["f"])
	require.Equal(t, "fn <'a>(p: &{x: number}) -> Ref<'a, {x: number}>", values["g"])
}

// TestInferLifetimeGenericAliasArityErrors covers the lifetime-argument arity checks. A
// lifetime parameter has no default, so supplying too many or too few lifetime arguments each
// report a single AliasLifetimeArityMismatchError, and supplying a lifetime argument to an
// alias that declares none is rejected the same way.
func TestInferLifetimeGenericAliasArityErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "TooManyLifetimeArgs",
			src: `
				type Ref<'a, T> = &'a T
				fn f<'a, 'b>(p: Ref<'a, 'b, {x: number}>) -> number { return p.x }
			`,
			want: "type alias `Ref` expects 1 lifetime arguments but got 2",
		},
		{
			name: "MissingLifetimeArg",
			src: `
				type Ref<'a, T> = &'a T
				fn f<'a>(p: Ref<{x: number}>) -> number { return p.x }
			`,
			want: "type alias `Ref` expects 1 lifetime arguments but got 0",
		},
		{
			name: "LifetimeArgOnNonGenericAlias",
			src: `
				type Point = {x: number}
				fn f<'a>(p: Point<'a>) -> number { return p.x }
			`,
			want: "type alias `Point` expects 0 lifetime arguments but got 1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Message()
			}
			require.Contains(t, msgs, tt.want)
		})
	}
}
