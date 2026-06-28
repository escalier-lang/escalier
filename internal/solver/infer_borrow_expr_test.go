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

// A bare `mut` parameter is OWNED-mutable, not a borrow. Under the pre-PR 3
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

// `val q = p` for an owned-immutable p establishes q as owned-immutable. PR 6
// will make this consume p. For now it only establishes the owned binding.
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
// DISABLED until lifetime-bounds (M6.5) lands.
//
// Returning a borrow of a locally-owned `p` is unsound: `p` is destroyed when
// `f` returns, so `q` dangles. The current solver under-checks this because
// the fresh lifetime on `&p` has no upper bound from `p` (an owned value has
// no lifetime), and D4 elides the unconstrained lifetime to render the result
// as owned. PR 5 of the affine plan generalizes escape detection at returns,
// which will force the lifetime to 'static and change the rendering, but the
// hard rejection needs M6.5's directional lifetime bounds. Re-enable then and
// assert the borrow-of-local diagnostic instead of the empty error list.
/*
func TestInferValBorrowFromOwnedImm(t *testing.T) {
	_, _, errs := inferSource(t, `fn f(p: {x: number}) {
  val q = &p
  return q
}`)
	require.Equal(t, []string{
		"cannot return borrow of local 'p': 'p' does not live long enough",
	}, messagesWithSpan(errs))
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
// DISABLED until lifetime-bounds (M6.5) lands. Same under-checking as
// TestInferValBorrowFromOwnedImm: the borrow of a locally-owned `p` escapes
// through the return without a lifetime that can refute it. Re-enable when
// M6.5 lifetime bounds catch the dangling-borrow case.
/*
func TestInferValMutBorrowFromOwnedMut(t *testing.T) {
	_, _, errs := inferSource(t, `fn f(p: mut {x: number}) {
  val q = &mut p
  return q
}`)
	require.Equal(t, []string{
		"cannot return borrow of local 'p': 'p' does not live long enough",
	}, messagesWithSpan(errs))
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
		"2:11-2:17: cannot constrain immutable object <: mutable object",
	}, messagesWithSpan(errs))
}

// --- Rule 3 (binding initializer): owned-mutable construction ------------------

// `val mut q = {…}` from a freshly constructed literal builds an owned-mutable
// value, the unannotated mirror of `val q: mut {x} = {x: 1}`. A fresh literal is
// uniquely owned, so granting it the mutable type aliases nothing. The literal's
// field widens, since the mutable cell admits any `number`, so the result is
// `mut {x: number}` rather than `mut {x: 0}`.
func TestInferValMutConstructsOwnedMut(t *testing.T) {
	values, _, errs := inferSource(t, `fn f() {
  val mut q = {x: 0}
  return q
}`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> mut {x: number}", values["f"])
}

// `var mut q = {…}` constructs owned-mutable too. The reassignable binding and the
// mutable value are orthogonal: both widen the literal, so the cell renders the
// same `mut {x: number}` as the `val mut` form.
func TestInferVarMutConstructsOwnedMut(t *testing.T) {
	values, _, errs := inferSource(t, `fn f() {
  var mut q = {x: 0}
  return q
}`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> mut {x: number}", values["f"])
}

// The owned-mutable construction is deep and uniform, reaching nested objects and
// tuple elements, so `val mut q = {a: {b: 1}}` is mutable to its leaves.
func TestInferValMutConstructsDeepOwnedMut(t *testing.T) {
	values, _, errs := inferSource(t, `fn f() {
  val mut q = {a: {b: 1}}
  return q
}`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> mut {a: {b: number}}", values["f"])
}

// A constructed owned-mutable value admits an ordinary field write. The widened
// field is `number`, so `q.x = 5` checks where a non-widened `mut {x: 0}` would
// reject `5 <: 0` under the mutable field's invariance.
func TestInferValMutConstructedAllowsFieldWrite(t *testing.T) {
	values, _, errs := inferSource(t, `fn f() {
  val mut q = {x: 0}
  q.x = 5
  return q
}`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> mut {x: number}", values["f"])
}

// A constructed owned-mutable value can be borrowed `&mut`. The mutable cell fills
// the mutable borrow destination, so `&mut q` checks where an owned-immutable q would fail
// the mutability gate. This is the construction-side companion to
// TestInferValMutBorrowFromOwnedMut, which sources owned-mutable from a parameter. The
// borrow checks, but returning it escapes the local q, which dies at the frame end, so
// PR 15's return-escape rule rejects the return while leaving the borrow itself legal.
func TestInferValMutConstructedBorrowsMut(t *testing.T) {
	_, _, errs := inferSource(t, `fn f() {
  val mut q = {x: 0}
  val r = &mut q
  return r
}`)
	require.Equal(t, []string{
		"4:10-4:11: borrowed value 'q' does not live long enough to escape the function",
	}, messagesWithSpan(errs))
}

// A `mut` binding of a primitive is unchanged: a primitive is a value type with no
// interior mutability, so it is not wrapped in an owned-mutable borrow. A `val mut`
// keeps the literal singleton, exactly as a plain `val` would.
func TestInferValMutPrimitiveUnchanged(t *testing.T) {
	values, _, errs := inferSource(t, `fn f() {
  val mut n = 5
  return n
}`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> 5", values["f"])
}

// `val mut p = src` thaws an owned-immutable source into an owned-mutable binding.
// The move consumes `src` and leaves `p` the sole owner. No reference to the value
// survives to observe `p`'s later mutations, so it is sound for `p` to be mutable. The
// binding's mutability comes from the `mut` pattern, not from the source. So `p` is
// `mut {x: number}` and the function returns it at that type.
func TestInferValMutThawFromVariable(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(src: {x: number}) {
  val mut p = src
  return p
}`)
	require.Empty(t, errs)
	require.Equal(t, "fn (src: {x: number}) -> mut {x: number}", values["f"])
}

// Thawing widens the source's literal fields to their primitive type. `p` holds the
// singleton `{x: 0}`, and thawing it into `val mut q` yields `mut {x: number}` rather
// than `mut {x: 0}`. A mutable cell admits any value of the field's primitive type, so
// a write like `q.x = 5` would otherwise be rejected against a `0` singleton. This is
// the same widening the fresh-literal `val mut q = {x: 0}` upgrade applies.
func TestInferValMutThawWidensLiteral(t *testing.T) {
	values, _, errs := inferSource(t, `fn f() {
  val p = {x: 0}
  val mut q = p
  return q
}`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> mut {x: number}", values["f"])
}

// Freezing into a `var` binding widens the source's literal fields, since a `var` binding
// must admit any later reassignment of the field's primitive type. Freezing the
// singleton `{x: 0}` into `var q` yields `{x: number}`. A plain `val q` keeps the
// singleton, because an immutable, non-reassignable binding has no widening reason.
// Both freeze the value immutably; only the `var` binding widens.
func TestFreezeVarWidensLiteral(t *testing.T) {
	t.Run("var_widens", func(t *testing.T) {
		values, _, errs := inferSource(t, `fn f() {
  val p = {x: 0}
  var q = p
  return q
}`)
		require.Empty(t, errs)
		require.Equal(t, "fn () -> {x: number}", values["f"])
	})
	t.Run("val_keeps_singleton", func(t *testing.T) {
		values, _, errs := inferSource(t, `fn f() {
  val p = {x: 0}
  val q = p
  return q
}`)
		require.Empty(t, errs)
		require.Equal(t, "fn () -> {x: 0}", values["f"])
	})
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

// `val q: &{x} = p` for an owned p auto-borrows p into the annotated borrow destination.
// The constrain rule's bare<:RefType arm wraps p as an immutable view.
//
// DISABLED until lifetime-bounds (M6.5) lands. The annotated borrow form has
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
	}, messagesWithSpan(errs))
}
*/

// `val q: &mut {x} = p` for an owned-mutable p auto-borrows p as a mutable borrow.
//
// DISABLED until lifetime-bounds (M6.5) lands. Mutable analogue of
// TestInferValAnnotatedBorrowImm.
/*
func TestInferValAnnotatedBorrowMut(t *testing.T) {
	_, _, errs := inferSource(t, `fn f(p: mut {x: number}) {
  val q: &mut {x: number} = p
  return q
}`)
	require.Equal(t, []string{
		"cannot return borrow of local 'p': 'p' does not live long enough",
	}, messagesWithSpan(errs))
}
*/

// --- Auto-borrow at call sites -------------------------------------------------

// An owned-mutable argument auto-borrows into a `&mut` parameter. The
// RefType<:RefType rule treats an owned source (Lt nil) as satisfying any borrow
// destination, so the call type-checks without an explicit `&mut`.
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

// An owned-immutable argument auto-borrows into a `&` parameter.
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
// mutability check rejects an immutable source filling a mutable borrow destination.
func TestInferAutoBorrowImmIntoMutParamRejected(t *testing.T) {
	src := `fn use(o: &mut {x: number}) -> number {
  return o.x
}
fn f(p: {x: number}) {
  return use(p)
}`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"5:10-5:16: cannot constrain immutable object <: mutable object",
	}, messagesWithSpan(errs))
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
// DISABLED until lifetime-bounds (M6.5) lands.
//
// Returning a borrow expression `&mut p` for a locally-owned `p` is unsound:
// `p` is destroyed when the function returns, so the borrow dangles. The
// current solver under-checks this because the fresh lifetime on `&mut p`
// has no anchor and D4 elides it to render the result as `mut {x: number}`.
// PR 5 will force the lifetime to 'static at the return, but the hard
// rejection still needs M6.5's directional lifetime bounds.
/*
func TestInferBorrowExprMutFromOwnedMut(t *testing.T) {
	_, _, errs := inferSource(t, `fn f(p: mut {x: number}) { return &mut p }`)
	require.Equal(t, []string{
		"cannot return borrow of local 'p': 'p' does not live long enough",
	}, messagesWithSpan(errs))
}
*/

// A borrow of an undefined identifier must not cascade a second diagnostic.
// inferIdent reports the unknown name and returns the ErrorType sentinel. The
// borrow form absorbs that sentinel without emitting a follow-on
// "borrow of a non-borrowable type" error.
func TestInferBorrowExprAbsorbsUnknownIdentifier(t *testing.T) {
	_, _, errs := inferSource(t, `fn f() { return &q }`)
	require.Equal(t, []string{
		"1:18-1:19: Unknown identifier: q",
	}, messagesWithSpan(errs))
}

// A borrow of a primitive value reports the unsupported-borrow diagnostic once.
// The recovery mirrors borrowInner on the annotation path. A fresh inner var
// keeps the wrapper alive so the surrounding expression does not cascade.
func TestInferBorrowExprOfPrimitiveRecovers(t *testing.T) {
	_, _, errs := inferSource(t, `fn f() { return &5 }`)
	require.Equal(t, []string{
		"1:17-1:19: Unsupported: borrow of a non-borrowable type",
	}, messagesWithSpan(errs))
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
