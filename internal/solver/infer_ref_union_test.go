package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- R9: unions/intersections as RefInner ---
//
// The borrow wrapper is outer and shared: `&(A | B)` is one borrow over a union
// pointee, with a single lifetime and mutability for the whole value, never
// `&A | &B` with independent lifetimes. A union or intersection joins RefInner so
// these lower and render.

// `&(A | B)` lowers to one borrow whose inner is a union, and round-trips through
// the printer with the pointee parenthesized.
func TestInferBorrowOverUnion(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: &({a: number} | {b: number})) { return p }`)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a>(p: &'a ({a: number} | {b: number})) -> &'a ({a: number} | {b: number})",
		values["f"])
}

// `&mut (A | B)` is the mutable borrow over a union pointee.
func TestInferMutBorrowOverUnion(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: &mut ({a: number} | {b: number})) { return p }`)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a>(p: &'a mut ({a: number} | {b: number})) -> &'a mut ({a: number} | {b: number})",
		values["f"])
}

// An owned-mutable union `mut (A | B)` is accepted: the union joins RefInner, so the
// `mut` wrapper has a borrowable pointee to wrap rather than reporting an unsupported
// feature.
func TestInferOwnedMutUnionAccepted(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: mut ({a: number} | {b: number})) { return p }`)
	require.Empty(t, errs)
	require.Equal(t,
		"fn (p: mut ({a: number} | {b: number})) -> mut ({a: number} | {b: number})",
		values["f"])
}

// `&(A & B)` borrows an intersection pointee the same way.
func TestInferBorrowOverIntersection(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: &({a: number} & {b: number})) { return p }`)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a>(p: &'a ({a: number} & {b: number})) -> &'a ({a: number} & {b: number})",
		values["f"])
}

// A mutable borrow over a union is invariant in its pointee, so `mut A` does not
// satisfy `mut (A | B)`: writing a `B` through the wider target would corrupt the
// caller's `A`-typed storage. The reverse residual write view the RefType rule adds
// for a mutable target drives the rejection.
func TestConstrainMutUnionPointeeInvariant(t *testing.T) {
	c := &Context{}
	mutA := &soltype.RefType{Mut: true, Inner: exactObj(propElem("x", num()))}
	mutUnion := &soltype.RefType{Mut: true, Inner: &soltype.UnionType{
		Types: []soltype.Type{exactObj(propElem("x", num())), exactObj(propElem("y", str()))},
	}}
	require.NotEmpty(t, c.Constrain(mutA, mutUnion))
}

// An immutable borrow reads only, so `&(A | B)` factors by subtyping: `&A` is usable
// where `&(A | B)` is wanted. No residual write view fires for an immutable target,
// so the covariant inner read `A <: (A | B)` succeeds.
func TestConstrainImmutableBorrowUnionFactors(t *testing.T) {
	c := &Context{}
	immA := &soltype.RefType{Lt: c.freshLifetime(0), Inner: exactObj(propElem("x", num()))}
	immUnion := &soltype.RefType{Lt: c.freshLifetime(0), Inner: &soltype.UnionType{
		Types: []soltype.Type{exactObj(propElem("x", num())), exactObj(propElem("y", str()))},
	}}
	require.Empty(t, c.Constrain(immA, immUnion))
}

// A bare owned union satisfies a borrow destination: the bare<:RefType arm wraps the
// borrowable owned source as an immutable view, so `(A | B)` auto-borrows into
// `&(A | B)`.
func TestConstrainBareUnionIntoBorrow(t *testing.T) {
	c := &Context{}
	bareUnion := &soltype.UnionType{
		Types: []soltype.Type{exactObj(propElem("x", num())), exactObj(propElem("y", str()))},
	}
	borrowUnion := &soltype.RefType{Lt: c.freshLifetime(0), Inner: &soltype.UnionType{
		Types: []soltype.Type{exactObj(propElem("x", num())), exactObj(propElem("y", str()))},
	}}
	require.Empty(t, c.Constrain(bareUnion, borrowUnion))
}

// --- R9: mixed-ownership rejection at join sites ---
//
// A union or intersection must have uniform ownership. Inference joins branches of
// different ownership at an if/else, a match arm set, or several return points. A
// borrowed member beside an owned one there has no single owned-or-borrowed verdict
// and is rejected.

// An if/else whose value is a borrow on one branch and an owned object on the other
// forms a mixed-ownership union and is rejected, blaming the if/else expression.
func TestInferMixedOwnershipIfElse(t *testing.T) {
	src := `fn f(p: &mut {x: number}) {
  val q = if true { p } else { {x: 5} }
  return q
}`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"2:10-2:40: union or intersection mixes owned and borrowed members; make ownership uniform — clone the borrowed member to own it, or borrow the owned member",
	}, messagesWithSpan(errs))
}

// Two return points, one a borrow and one an owned object, join into a mixed union
// and are rejected at the function's return join.
func TestInferMixedOwnershipReturns(t *testing.T) {
	src := `fn f(p: &mut {x: number}) {
  if true { return p } else { return {x: 5} }
}`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"1:1-3:2: union or intersection mixes owned and borrowed members; make ownership uniform — clone the borrowed member to own it, or borrow the owned member",
	}, messagesWithSpan(errs))
}

// A match whose arms disagree on ownership is rejected at the match expression.
func TestInferMixedOwnershipMatch(t *testing.T) {
	src := `fn f(p: &mut {x: number}) {
  val r = match 1 {
    1 => p,
    _ => ({x: 5}),
  }
  return r
}`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"2:11-5:4: union or intersection mixes owned and borrowed members; make ownership uniform — clone the borrowed member to own it, or borrow the owned member",
	}, messagesWithSpan(errs))
}

// A union of two owned objects has uniform ownership and is accepted.
func TestInferUniformOwnedUnion(t *testing.T) {
	values, _, errs := inferSource(t, `fn f() {
  if true { return {x: 5} } else { return {x: 6} }
}`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> {x: 5} | {x: 6}", values["f"])
}

// A union of value types carries no ownership and is accepted.
func TestInferUniformValueUnion(t *testing.T) {
	values, _, errs := inferSource(t, `fn f() {
  if true { return 5 } else { return "x" }
}`)
	require.Empty(t, errs)
	require.Equal(t, `fn () -> 5 | "x"`, values["f"])
}

// Two borrows that differ only in lifetime join into one borrow rather than a mixed
// union, so the uniform-ownership check leaves them alone.
func TestInferUniformBorrowUnion(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: &mut {x: number}, q: &mut {x: number}) {
  if true { return p } else { return q }
}`)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a, 'b>(p: &'a mut {x: number}, q: &'b mut {x: number}) -> &('a | 'b) mut {x: number}",
		values["f"])
}

// --- R9: nested-borrow normalization ---
//
// A borrow whose pointee is itself a borrow collapses to depth one, since the JS
// target compiles every borrow to the same bare object reference.

// An immutable nested borrow `&(&{x})` collapses to a single `&{x}` at the outer
// lifetime.
func TestInferNestedImmutableBorrowCollapses(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: &(&{x: number})) { return p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: &'a {x: number}) -> &'a {x: number}", values["f"])
}

// A mutable nested borrow `&mut (&mut {x})` is uninhabitable: it would have to repoint
// the inner borrow, which needs a storage cell the JS target cannot express.
func TestInferNestedMutableBorrowRejected(t *testing.T) {
	_, _, errs := inferSource(t, `fn f(p: &mut (&mut {x: number})) { return p }`)
	require.Equal(t, []string{
		"1:9-1:31: Unsupported: mutable borrow of a borrow is uninhabitable",
	}, messagesWithSpan(errs))
}
