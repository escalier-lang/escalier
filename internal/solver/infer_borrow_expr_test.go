package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- Annotation-literal ownership, owned/borrow params, auto-borrow ---
//
// Rule 2: a `&` parameter is a borrow and a bare parameter is owned. Rule 3:
// `val q = p` / `val q = &p` and the annotated forms `val q: {x} = p` /
// `val q: &{x} = p` choose between move (owned) and borrow at the binding site.
// `&p` and `&mut p` are the explicit borrow expressions.

// --- Rule 2 (params) -----------------------------------------------------------

// A bare `mut` parameter is OWNED-mutable, not a borrow. Under the earlier
// borrow-by-default convention, a `mut T` param picked up a fresh inferred
// lifetime. Now only an explicit `&` annotation borrows, so the rendered
// signature carries no lifetime quantifier on a bare-mut returning function.
func TestInferBareMutParamIsOwned(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: mut {x: number}) { return p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {x: number}) -> mut {x: number}", values["f"])
}

// A bare immutable parameter is OWNED-immutable. The signature renders the
// unwrapped value type with no borrow lifetime.
func TestInferBareImmutableParamIsOwned(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: {x: number}) { return p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number}) -> {x: number}", values["f"])
}

// --- Rule 3 (binding initializer): inferred bindings, owned operand ------------

// `val q = p` for an owned-immutable p establishes q as owned-immutable. A later
// change will make this consume p. For now it only establishes the owned binding.
func TestInferValOwnedImmFromOwnedImm(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: {x: number}) {
  val q = p
  return q
}`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number}) -> {x: number}", values["f"])
}

// `val q = &p` for an owned-immutable p establishes q as an immutable borrow.
// The borrow lifetime is inferred and carried through to the return.
//
// DISABLED until directional lifetime bounds land.
//
// Returning a borrow of a locally-owned `p` is unsound: `p` is destroyed when
// `f` returns, so `q` dangles. The current solver under-checks this because
// the fresh lifetime on `&p` has no upper bound from `p`, since an owned value
// has no lifetime, and the elision pass drops the unconstrained lifetime to
// render the result as owned. Generalizing escape detection at returns will
// force the lifetime to 'static and change the rendering, but the hard
// rejection needs directional lifetime bounds. Re-enable then and assert the
// borrow-of-local diagnostic instead of the empty error list.
/*
func TestInferValBorrowFromOwnedImm(t *testing.T) {
	_, _, errs := inferSource(t, `fn f(p: {x: number}) {
  val q = &p
  return q
}`)
	require.Equal(t, []string{
		"cannot return borrow of local 'p': 'p' does not live long enough",
	}, Messages(errs))
}
*/

// `val mut q = p` for an owned-mutable p establishes q as owned-mutable.
func TestInferValMutOwnedFromOwnedMut(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: mut {x: number}) {
  val mut q = p
  return q
}`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {x: number}) -> mut {x: number}", values["f"])
}

// `val q = &mut p` for an owned-mutable p establishes q as a mutable borrow.
//
// DISABLED until directional lifetime bounds land. Same under-checking as
// TestInferValBorrowFromOwnedImm: the borrow of a locally-owned `p` escapes
// through the return without a lifetime that can refute it. Re-enable when
// lifetime bounds catch the dangling-borrow case.
/*
func TestInferValMutBorrowFromOwnedMut(t *testing.T) {
	_, _, errs := inferSource(t, `fn f(p: mut {x: number}) {
  val q = &mut p
  return q
}`)
	require.Equal(t, []string{
		"cannot return borrow of local 'p': 'p' does not live long enough",
	}, Messages(errs))
}
*/

// `&mut p` on an owned-IMMUTABLE p is a mutability mismatch. A mutable borrow
// cannot be taken on a value that is not declared mutable.
func TestInferBorrowMutOnImmutableRejected(t *testing.T) {
	_, _, errs := inferSource(t, `fn f(p: {x: number}) {
  val q = &mut p
  return q
}`)
	require.Equal(t, []string{
		"cannot constrain immutable object <: mutable object",
	}, Messages(errs))
}

// --- Rule 3 (binding initializer): annotated bindings --------------------------

// `val q: {x} = p` for an owned-immutable p binds q at the annotated owned type.
func TestInferValAnnotatedOwnedImm(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: {x: number}) {
  val q: {x: number} = p
  return q
}`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number}) -> {x: number}", values["f"])
}

// `val q: &{x} = p` for an owned p auto-borrows p into the annotated borrow slot.
// The constrain rule's bare<:RefType arm wraps p as an immutable view.
//
// DISABLED until directional lifetime bounds land. The annotated borrow form has
// the same dangling-borrow soundness gap as the inferred `val q = &p` case
// in TestInferValBorrowFromOwnedImm.
/*
func TestInferValAnnotatedBorrowImm(t *testing.T) {
	_, _, errs := inferSource(t, `fn f(p: {x: number}) {
  val q: &{x: number} = p
  return q
}`)
	require.Equal(t, []string{
		"cannot return borrow of local 'p': 'p' does not live long enough",
	}, Messages(errs))
}
*/

// `val q: &mut {x} = p` for an owned-mutable p auto-borrows p as a mutable borrow.
//
// DISABLED until directional lifetime bounds land. Mutable analogue of
// TestInferValAnnotatedBorrowImm.
/*
func TestInferValAnnotatedBorrowMut(t *testing.T) {
	_, _, errs := inferSource(t, `fn f(p: mut {x: number}) {
  val q: &mut {x: number} = p
  return q
}`)
	require.Equal(t, []string{
		"cannot return borrow of local 'p': 'p' does not live long enough",
	}, Messages(errs))
}
*/

// --- Auto-borrow at call sites -------------------------------------------------

// An owned-mutable argument auto-borrows into a `&mut` parameter slot. The
// RefType<:RefType rule treats an owned source (Lt nil) as satisfying any borrow
// slot, so the call type-checks without an explicit `&mut`.
func TestInferAutoBorrowOwnedIntoMutParam(t *testing.T) {
	src := `fn use(o: &mut {x: number}) -> number {
  return o.x
}
fn f(p: mut {x: number}) {
  return use(p)
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {x: number}) -> number", values["f"])
}

// An owned-immutable argument auto-borrows into a `&` parameter slot.
func TestInferAutoBorrowOwnedIntoImmParam(t *testing.T) {
	src := `fn use(o: &{x: number}) -> number {
  return o.x
}
fn f(p: {x: number}) {
  return use(p)
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number}) -> number", values["f"])
}

// An owned-immutable argument cannot auto-borrow into a `&mut` parameter: the
// mutability check rejects an immutable source filling a mutable borrow slot.
func TestInferAutoBorrowImmIntoMutParamRejected(t *testing.T) {
	src := `fn use(o: &mut {x: number}) -> number {
  return o.x
}
fn f(p: {x: number}) {
  return use(p)
}`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"cannot constrain immutable object <: mutable object",
	}, Messages(errs))
}

// --- Borrow expressions infer to borrow types ----------------------------------

// A returned `&p` carries the borrow lifetime out. The display name is then
// load-bearing, so the rendered signature quantifies it. The receiver `p` is a
// `&mut` borrow, and its lifetime threads through into the immutable reborrow at
// the return.
func TestInferBorrowExprImmReturnedCarriesLifetime(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: &mut {x: number}) { return &p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: &'a mut {x: number}) -> &'a {x: number}", values["f"])
}

// `&mut p` on an owned-mutable p infers a mutable borrow.
//
// DISABLED until directional lifetime bounds land.
//
// Returning a borrow expression `&mut p` for a locally-owned `p` is unsound:
// `p` is destroyed when the function returns, so the borrow dangles. The
// current solver under-checks this because the fresh lifetime on `&mut p`
// has no anchor and the elision pass drops it to render the result as
// `mut {x: number}`. Forcing the lifetime to 'static at the return is part of
// it, but the hard rejection still needs directional lifetime bounds.
/*
func TestInferBorrowExprMutFromOwnedMut(t *testing.T) {
	_, _, errs := inferSource(t, `fn f(p: mut {x: number}) { return &mut p }`)
	require.Equal(t, []string{
		"cannot return borrow of local 'p': 'p' does not live long enough",
	}, Messages(errs))
}
*/

// A borrow of an undefined identifier must not cascade a second diagnostic.
// inferIdent reports the unknown name and returns the ErrorType sentinel. The
// borrow form absorbs that sentinel without emitting a follow-on
// "borrow of a non-borrowable type" error.
func TestInferBorrowExprAbsorbsUnknownIdentifier(t *testing.T) {
	_, _, errs := inferSource(t, `fn f() { return &q }`)
	require.Equal(t, []string{
		"Unknown identifier: q",
	}, Messages(errs))
}

// A borrow of a primitive value reports the unsupported-borrow diagnostic once.
// The recovery mirrors borrowInner on the annotation path. A fresh inner var
// keeps the wrapper alive so the surrounding expression does not cascade.
func TestInferBorrowExprOfPrimitiveRecovers(t *testing.T) {
	_, _, errs := inferSource(t, `fn f() { return &5 }`)
	require.Equal(t, []string{
		"Unsupported: borrow of a non-borrowable type",
	}, Messages(errs))
}

// Passing an explicit `&p` argument into a `&` parameter type-checks the same as
// auto-borrowing an owned argument. The borrow expression produces a RefType.
// The argument-constraining path then relates that RefType to the parameter
// through the existing RefType<:RefType rule.
func TestInferBorrowExprAsArgumentIntoBorrowParam(t *testing.T) {
	src := `fn use(o: &{x: number}) -> number {
  return o.x
}
fn f(p: {x: number}) {
  return use(&p)
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number}) -> number", values["f"])
}

// `&mut p` as an argument into a `&mut` parameter on an owned-mutable receiver
// type-checks. The same source written `use(p)` also checks via auto-borrow, so
// this pins the explicit-borrow form as an alternative spelling.
func TestInferBorrowMutExprAsArgumentIntoMutBorrowParam(t *testing.T) {
	src := `fn use(o: &mut {x: number}) -> number {
  return o.x
}
fn f(p: mut {x: number}) {
  return use(&mut p)
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {x: number}) -> number", values["f"])
}

// A `&mut` borrow of a `&mut` parameter is a reborrow at the parameter's lifetime.
// The result carries the same lifetime through to the return, which renders as
// the parameter's named lifetime when the borrow reaches the output.
func TestInferBorrowExprMutReborrowOfMutParam(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: &mut {x: number}) { return &mut p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: &'a mut {x: number}) -> &'a mut {x: number}", values["f"])
}
