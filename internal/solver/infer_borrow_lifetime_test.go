package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- M4 D2/D4: borrow origination + display-time lifetime naming and elision ---
//
// These assert the D4 display form. A param lifetime that reaches the output is
// named 'a, 'b, … and quantified in a leading <…> prefix. A param lifetime that
// connects nothing, never reaching an output, is elided: a mut borrow becomes
// owned-mutable, and an immutable borrow drops the wrapper.

// A `mut` param returned unchanged carries its originated lifetime to the result:
// the same lifetime appears on both the parameter and the return type, so the
// borrow flows out at the lifetime it came in. Occurring in both positions, it is
// named `'a` and quantified. This is the IdentityRefReturn acceptance (M4 D4).
func TestInferIdentityRefReturn(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: mut {x: number}) { return p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: mut 'a {x: number}) -> mut 'a {x: number}", values["f"])
}

// Returning a freshly-constructed owned object carries no borrow lifetime: the
// object literal is owned (Lt nil) and not mutable, so the result renders as a
// bare object `{…}` with no `mut` prefix and no `'l` lifetime annotation.
func TestInferFreshObjectReturn(t *testing.T) {
	values, _, errs := inferSource(t, `fn f() { return {x: 5} }`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> {x: 5}", values["f"])
}

// Two `mut` params originate independent lifetimes, but only the returned one
// reaches the output. `p` is returned, so its lifetime is named `'a`. `q` connects
// nothing, so its borrow lifetime is elided to owned-mutable.
func TestInferDistinctParamLifetimes(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: mut {x: number}, q: mut {y: number}) { return p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: mut 'a {x: number}, q: mut {y: number}) -> mut 'a {x: number}", values["f"])
}

// Writing a field through an annotated `mut` borrow checks: the receiver carries a
// borrow lifetime, and the write requirement's fresh lifetime imposes no obligation,
// so a borrowed receiver of any lifetime satisfies it. The borrow never reaches the
// output, since the body returns void, so D4 elides its lifetime to owned-mutable.
func TestInferFieldWriteThroughBorrowParam(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: mut {x: number}) { p.x = 10 }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {x: number}) -> void", values["f"])
}

// Passing a borrow into a function whose parameter is an OWNED (bare) object is the
// borrow-into-owned-slot escape: the RefType<:bare arm rejects because the source
// carries a lifetime and the target owns its value. This is the only path that
// exercises the escape guard D2 activated — before D2 every Lt was nil, so it never
// fired.
func TestInferBorrowEscapesIntoOwnedArg(t *testing.T) {
	src := `fn use(o: {x: number}) -> number {
  return o.x
}
fn f(p: mut {x: number}) {
  return use(p)
}`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"borrowed value mut object does not live long enough to satisfy object",
	}, Messages(errs))
}

// The companion to the escape case: passing the same borrow into a function whose
// parameter is itself a `mut` borrow checks. The RefType<:RefType arm relates the
// two lifetimes via constrainLt (the now-active step 3) instead of rejecting, so the
// borrow slot — unlike the owned slot above — admits the borrow.
func TestInferBorrowIntoBorrowArg(t *testing.T) {
	src := `fn use(o: mut {x: number}) -> number {
  return o.x
}
fn f(p: mut {x: number}) {
  return use(p)
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {x: number}) -> number", values["f"])
}

// Reading a field after writing it through an annotated `mut` borrow returns the
// written field's type. The receiver is the concrete borrow `mut 'l0 {x: number}`,
// so valueProp peels it via CarrierOf before emitting the read requirement — without
// the peel this would trip the escape guard on the bare read requirement. Unlike the
// usage-inferred read-after-write tests (which key off the `written` map on a
// receiver VAR), this exercises the peel-and-constrain path on a concrete borrow.
func TestInferReadAfterWriteThroughBorrowParam(t *testing.T) {
	src := `fn f(p: mut {x: number}) {
  p.x = 5
  return p.x
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {x: number}) -> number", values["f"])
}

// Writing a field of a NON-`mut` owned object is rejected: the write lowers to the
// mutable requirement `mut {x, ...}`, and an immutable owned object cannot fill a
// mutable slot. This confirms the field-write requirement's fresh lifetime (D2) did
// not loosen the mutability gate — an owned-but-immutable receiver still fails.
func TestInferFieldWriteToImmutableObjectRejected(t *testing.T) {
	src := `fn g(o: {x: number}) {
  o.x = 5
}`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"cannot constrain immutable object <: mutable object",
	}, Messages(errs))
}

// Returning one of two borrows with DISTINCT lifetimes joins them into a single
// borrow whose lifetime is the union of theirs. This is the ConditionalUnionReturn
// acceptance (M4 D3). The return-point join mints a fresh join lifetime bounded
// below by 'l0 and 'l1, which coalesces to `('l0 | 'l1)` in the positive return
// position. The param lifetimes 'l0/'l1 stay named on the borrows they originate.
// D4 renders them `'a`/`'b` and the union `('a | 'b)`.
func TestInferConditionalUnionReturn(t *testing.T) {
	src := `fn f(p: mut {x: number}, q: mut {x: number}) {
  if true {
    return p
  } else {
    return q
  }
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a, 'b>(p: mut 'a {x: number}, q: mut 'b {x: number}) -> mut ('a | 'b) {x: number}",
		values["f"])
}

// Returning borrows whose objects have DIFFERENT field sets does NOT join. A mut
// object's field set is invariant, so uniting `mut {x}` and `mut {y}` would invent a
// writable field absent from one branch. joinBorrows rejects the mismatch and the
// return falls back to the generic union, preserving both borrows with their own
// lifetimes (M4 D3).
func TestInferMismatchedBorrowsFallBackToUnion(t *testing.T) {
	src := `fn f(p: mut {x: number}, q: mut {y: number}) {
  if true {
    return p
  } else {
    return q
  }
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a, 'b>(p: mut 'a {x: number}, q: mut 'b {y: number}) -> mut 'a {x: number} | mut 'b {y: number}",
		values["f"])
}

// A join over THREE borrows unites all their lifetimes. The fresh join lifetime is
// bounded below by each, coalescing to `('l0 | 'l1 | 'l2)` in the return position.
// This confirms the join generalizes past the two-branch case to an n-ary return set.
func TestInferThreeWayBorrowJoin(t *testing.T) {
	src := `fn f(p: mut {x: number}, q: mut {x: number}, r: mut {x: number}) {
  if true {
    return p
  } else {
    if true {
      return q
    } else {
      return r
    }
  }
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a, 'b, 'c>(p: mut 'a {x: number}, q: mut 'b {x: number}, r: mut 'c {x: number}) -> mut ('a | 'b | 'c) {x: number}",
		values["f"])
}

// A GENERALIZED joined-borrow function keeps its lifetime union after instantiation.
// A caller that passes two of its own borrows still sees a two-lifetime union, not a
// single name. This is the only end-to-end exercise of the Join flag riding through
// freshenAbove/extrude (D2.5). If the flag were dropped on the freshened join
// lifetime, the instantiated return would coalesce to one lifetime instead of a
// union. `pick` renders `mut ('a | 'b) {…}` over its two param lifetimes.
// Instantiating it inside `use` freshens the join and its two members to use-level
// lifetimes. D4's component-based expansion resolves those intermediaries back to
// `use`'s own param lifetimes, so `use` renders the same clean `mut ('a | 'b) {…}`
// over `a` and `b` rather than the raw freshened ids.
func TestInferInstantiatedJoinReturnsUnion(t *testing.T) {
	src := `fn pick(p: mut {x: number}, q: mut {x: number}) {
  if true { return p } else { return q }
}
fn use(a: mut {x: number}, b: mut {x: number}) {
  return pick(a, b)
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a, 'b>(p: mut 'a {x: number}, q: mut 'b {x: number}) -> mut ('a | 'b) {x: number}",
		values["pick"])
	require.Equal(t,
		"fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number}) -> mut ('a | 'b) {x: number}",
		values["use"])
}

// Joining borrows that share a field NAME but disagree on its TYPE is rejected. A mut
// object's fields are observable in both directions, so the join pins each shared
// field invariant, and `number` vs `string` for `x` fails that pin in both
// directions. This locks in that the soundness constraint actually fires rather than
// silently unifying incompatible borrows (M4 D3).
//
// FUTURE (M6): this error is the conservative M4 default. M6 may relax it to a
// read-until-narrowed union — `(mut 'a {x: number}) | (mut 'b {x: string})`, readable
// always and writable only after narrowing — to match TypeScript. See 01-milestones.md
// M6, "Permissive mut-borrow joins". When that lands, this test changes from asserting
// an error to asserting the union.
func TestInferIncompatibleBorrowJoinErrors(t *testing.T) {
	src := `fn f(p: mut {x: number}, q: mut {x: string}) {
  if true {
    return p
  } else {
    return q
  }
}`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"cannot constrain number <: string",
		"cannot constrain string <: number",
	}, Messages(errs))
}

// A return set mixing a borrow with an OWNED value does not join. joinBorrows
// requires every input to be a mutable borrow carrying a lifetime, and an object
// literal is owned rather than a RefType. The all-borrows gate falls back to the
// generic union, so the result keeps the borrow's lifetime alongside the owned
// literal (M4 D3).
func TestInferMixedBorrowAndOwnedReturnFallsBackToUnion(t *testing.T) {
	src := `fn f(p: mut {x: number}) {
  if true {
    return p
  } else {
    return {x: 5}
  }
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a>(p: mut 'a {x: number}) -> mut 'a {x: number} | {x: 5}",
		values["f"])
}

// A borrowed parameter written into module-level storage escapes to 'static. This is
// the EscapingRefIntoStatic acceptance (M4 D3), now reachable from real source.
// `cache` stores its borrow `p` into the module-level `var sink`, a global write. The
// stored value outlives every borrow region, so p's lifetime is forced `<: 'static`
// and the parameter renders `mut 'static {x: number}` rather than under a borrow
// lifetime `'l{id}`. The store itself checks. A 'static borrow is owned-forever, so
// it fills the owned slot instead of tripping BorrowEscapeError.
func TestInferGlobalWriteEscapesBorrowToStatic(t *testing.T) {
	src := `var sink = {x: 0}
fn cache(p: mut {x: number}) {
  sink = p
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "{x: number}", values["sink"])
	require.Equal(t, "fn (p: mut 'static {x: number}) -> void", values["cache"])
}

// An ordinary global write of a NON-borrow value is unaffected by the escape rule.
// constrainEscape is a no-op on a non-borrow source and CarrierOf is the identity, so
// `bump` reassigning the module-level `var n` checks exactly as before, with no
// lifetime machinery engaged.
func TestInferGlobalWriteNonBorrowUnaffected(t *testing.T) {
	src := `var n = 0
fn bump() {
  n = 5
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "number", values["n"])
	require.Equal(t, "fn () -> void", values["bump"])
}
