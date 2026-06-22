package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- PR 3: annotation-literal ownership, owned/borrow params, auto-borrow ---
//
// Rule 2: a `&` parameter is a borrow and a bare parameter is owned. Rule 3:
// `val q = p` / `val q = &p` and the annotated forms `val q: {x} = p` /
// `val q: &{x} = p` choose between move (owned) and borrow at the binding site.
// `&p` and `&mut p` are the explicit borrow expressions.

// --- Rule 2 (params) -----------------------------------------------------------

// A bare `mut` parameter is OWNED-mutable, not a borrow. Before PR 3, a `mut T`
// param picked up a fresh inferred lifetime (borrow-by-default); now only an
// explicit `&` annotation borrows, so the rendered signature carries no lifetime
// quantifier on a bare-mut returning function.
func TestInferBareMutParamIsOwned(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: mut {x: number}) { return p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {x: number}) -> mut {x: number}", values["f"])
}

// A bare immutable parameter is OWNED-immutable: an unwrapped value type, no
// borrow lifetime in the signature.
func TestInferBareImmutableParamIsOwned(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: {x: number}) { return p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number}) -> {x: number}", values["f"])
}

// --- Rule 3 (binding initializer): inferred bindings, owned operand ------------

// `val q = p` for an owned-immutable p establishes q as owned-immutable. After
// PR 6 this consumes p; for now it only establishes the owned binding.
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
func TestInferValBorrowFromOwnedImm(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: {x: number}) {
  val q = &p
  return q
}`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number}) -> {x: number}", values["f"])
}

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
func TestInferValMutBorrowFromOwnedMut(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: mut {x: number}) {
  val q = &mut p
  return q
}`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {x: number}) -> mut {x: number}", values["f"])
}

// `&mut p` on an owned-IMMUTABLE p is a mutability mismatch: a mutable borrow
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
func TestInferValAnnotatedBorrowImm(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: {x: number}) {
  val q: &{x: number} = p
  return q
}`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number}) -> {x: number}", values["f"])
}

// `val q: &mut {x} = p` for an owned-mutable p auto-borrows p as a mutable borrow.
func TestInferValAnnotatedBorrowMut(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: mut {x: number}) {
  val q: &mut {x: number} = p
  return q
}`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {x: number}) -> mut {x: number}", values["f"])
}

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

// `&p` on an inferred-owned local infers an immutable borrow. The borrow's
// inferred lifetime is local — it does not reach an output here — so it elides
// at display time, but the borrow is real, as the next test confirms.
func TestInferBorrowExprImmReturnedCarriesLifetime(t *testing.T) {
	// A returned `&p` carries the borrow lifetime out, where it is named at
	// display time. The receiver `p` is `&mut` so its own lifetime threads
	// through into the immutable reborrow at the return.
	values, _, errs := inferSource(t, `fn f(p: &mut {x: number}) { return &p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: &'a mut {x: number}) -> &'a {x: number}", values["f"])
}

// `&mut p` on an owned-mutable p infers a mutable borrow.
func TestInferBorrowExprMutFromOwnedMut(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: mut {x: number}) { return &mut p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {x: number}) -> mut {x: number}", values["f"])
}
