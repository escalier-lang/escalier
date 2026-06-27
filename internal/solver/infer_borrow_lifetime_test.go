package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- M4 D2/D4: borrow origination + display-time lifetime naming and elision ---
//
// These assert the D4 display form. A param lifetime that reaches the output is
// named 'a, 'b, … and quantified in a leading <…> prefix. A param lifetime that
// connects nothing, never reaching an output, has its NAME elided but its `&`
// kept, so the displayed borrow stays distinguishable from an owned value.

// A `&mut` param returned unchanged carries its lifetime to the result: the same
// lifetime appears on both the parameter and the return type, so the borrow flows
// out at the lifetime it came in. Occurring in both positions, it is named `'a`
// and quantified. This is the IdentityRefReturn acceptance (M4 D4).
func TestInferIdentityRefReturn(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: &mut {x: number}) { return p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: &'a mut {x: number}) -> &'a mut {x: number}", values["f"])
}

// Returning a freshly-constructed owned object carries no borrow lifetime: the
// object literal is owned (Lt nil) and not mutable, so the result renders as a
// bare object `{…}` with no `mut` prefix and no `'l` lifetime annotation.
func TestInferFreshObjectReturn(t *testing.T) {
	values, _, errs := inferSource(t, `fn f() { return {x: 5} }`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> {x: 5}", values["f"])
}

// Two `&mut` params carry independent lifetimes; only the returned one reaches
// the output. `p` is returned, so its lifetime is named `'a`. `q` connects
// nothing, so its lifetime name is elided but the `&mut` is kept.
func TestInferDistinctParamLifetimes(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: &mut {x: number}, q: &mut {y: number}) { return p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: &'a mut {x: number}, q: &mut {y: number}) -> &'a mut {x: number}", values["f"])
}

// Writing a field through an owned-mutable param checks. The write requirement's
// fresh lifetime imposes no obligation, so an owned receiver satisfies it.
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
fn f(p: &mut {x: number}) {
  return use(p)
}`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"4:9-4:25: borrowed value mut object does not live long enough to satisfy object",
	}, messagesWithSpan(errs))
}

// The companion to the escape case: passing the same borrow into a function
// whose parameter is itself a `&mut` borrow checks. The RefType<:RefType arm
// relates the two lifetimes via constrainLt instead of rejecting, so the borrow
// slot admits the borrow.
func TestInferBorrowIntoBorrowArg(t *testing.T) {
	src := `fn use(o: &mut {x: number}) -> number {
  return o.x
}
fn f(p: &mut {x: number}) {
  return use(p)
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: &mut {x: number}) -> number", values["f"])
}

// Reading a field after writing it through an annotated `&mut` borrow returns
// the written field's type. The receiver is a concrete mutable borrow, so
// valueProp peels it via CarrierOf before emitting the read requirement.
// Without the peel this would trip the escape guard on the bare read
// requirement. Unlike the usage-inferred read-after-write tests, which key off
// the `written` map on a receiver var, this exercises the peel-and-constrain
// path on a concrete borrow.
func TestInferReadAfterWriteThroughBorrowParam(t *testing.T) {
	src := `fn f(p: &mut {x: number}) {
  p.x = 5
  return p.x
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: &mut {x: number}) -> number", values["f"])
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
		"2:3-2:10: cannot constrain immutable object <: mutable object",
	}, messagesWithSpan(errs))
}

// Returning one of two borrows with DISTINCT lifetimes joins them into a single
// borrow whose lifetime is the union of theirs. This is the ConditionalUnionReturn
// acceptance (M4 D3). The return-point join mints a fresh join lifetime bounded
// below by 'l0 and 'l1, which coalesces to `('l0 | 'l1)` in the positive return
// position. The param lifetimes 'l0/'l1 stay named on the borrows they originate.
// D4 renders them `'a`/`'b` and the union `('a | 'b)`.
func TestInferConditionalUnionReturn(t *testing.T) {
	src := `fn f(p: &mut {x: number}, q: &mut {x: number}) {
  if true {
    return p
  } else {
    return q
  }
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a, 'b>(p: &'a mut {x: number}, q: &'b mut {x: number}) -> &('a | 'b) mut {x: number}",
		values["f"])
}

// Returning borrows whose objects have DIFFERENT field sets does NOT join. A mut
// object's field set is invariant, so uniting `mut {x}` and `mut {y}` would invent a
// writable field absent from one branch. joinBorrows rejects the mismatch and the
// return falls back to the generic union, preserving both borrows with their own
// lifetimes (M4 D3).
func TestInferMismatchedBorrowsFallBackToUnion(t *testing.T) {
	src := `fn f(p: &mut {x: number}, q: &mut {y: number}) {
  if true {
    return p
  } else {
    return q
  }
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a, 'b>(p: &'a mut {x: number}, q: &'b mut {y: number}) -> &'a mut {x: number} | &'b mut {y: number}",
		values["f"])
}

// A join over THREE borrows unites all their lifetimes. The fresh join lifetime is
// bounded below by each, coalescing to `('l0 | 'l1 | 'l2)` in the return position.
// This confirms the join generalizes past the two-branch case to an n-ary return set.
func TestInferThreeWayBorrowJoin(t *testing.T) {
	src := `fn f(p: &mut {x: number}, q: &mut {x: number}, r: &mut {x: number}) {
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
		"fn <'a, 'b, 'c>(p: &'a mut {x: number}, q: &'b mut {x: number}, r: &'c mut {x: number}) -> &('a | 'b | 'c) mut {x: number}",
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
	src := `fn pick(p: &mut {x: number}, q: &mut {x: number}) {
  if true { return p } else { return q }
}
fn use(a: &mut {x: number}, b: &mut {x: number}) {
  return pick(a, b)
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a, 'b>(p: &'a mut {x: number}, q: &'b mut {x: number}) -> &('a | 'b) mut {x: number}",
		values["pick"])
	require.Equal(t,
		"fn <'a, 'b>(a: &'a mut {x: number}, b: &'b mut {x: number}) -> &('a | 'b) mut {x: number}",
		values["use"])
}

// Joining borrows that share a field NAME but disagree on its TYPE is rejected. A mut
// object's fields are observable in both directions, so the join pins each shared
// field invariant, and `number` vs `string` for `x` fails that pin in both
// directions. This locks in that the soundness constraint actually fires rather than
// silently unifying incompatible borrows (M4 D3).
//
// FUTURE (M6): this error is the conservative M4 default. M6 may relax it to a
// read-until-narrowed union — `(&'a mut {x: number}) | (&'b mut {x: string})`, readable
// always and writable only after narrowing — to match TypeScript. See 01-milestones.md
// M6, "Permissive mut-borrow joins". When that lands, this test changes from asserting
// an error to asserting the union.
func TestInferIncompatibleBorrowJoinErrors(t *testing.T) {
	src := `fn f(p: &mut {x: number}, q: &mut {x: string}) {
  if true {
    return p
  } else {
    return q
  }
}`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"1:18-1:24: cannot constrain number <: string",
		"1:39-1:45: cannot constrain string <: number",
	}, messagesWithSpan(errs))
}

// A return set mixing a borrow with an OWNED value does not join. joinBorrows
// requires every input to be a mutable borrow carrying a lifetime, and an object
// literal is owned rather than a RefType. The all-borrows gate falls back to the
// generic union, so the result keeps the borrow's lifetime alongside the owned
// literal (M4 D3).
//
// DISABLED until PR 9 lands.
//
// PR 9 of the affine semantics plan ("Reject mixed-ownership unions and
// intersections") makes a type whose members disagree on ownership an error
// rather than a silent union. Once it lands, this source should fail at the
// if-else join with a "mixed-ownership union" diagnostic against the union
// site instead of producing the union shown below. Re-enable then and flip
// the assertion from a successful render to an error list.
/*
func TestInferMixedBorrowAndOwnedReturnFallsBackToUnion(t *testing.T) {
	src := `fn f(p: &mut {x: number}) {
  if true {
    return p
  } else {
    return {x: 5}
  }
}`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"mixed-ownership union: members disagree on ownership; make ownership uniform first",
	}, messagesWithSpan(errs))
}
*/

// A borrowed parameter written into module-level storage escapes to 'static. This is
// the EscapingRefIntoStatic acceptance (M4 D3), now reachable from real source.
// `cache` stores its borrow `p` into the module-level `var sink`, a global write. The
// stored value outlives every borrow region, so p's lifetime is forced `<: 'static`
// and the parameter renders `&'static mut {x: number}` rather than under a borrow
// lifetime `'l{id}`. The store itself checks. A 'static borrow is owned-forever, so
// it fills the owned slot instead of tripping BorrowEscapeError.
func TestInferGlobalWriteEscapesBorrowToStatic(t *testing.T) {
	src := `var sink = {x: 0}
fn cache(p: &mut {x: number}) {
  sink = p
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "{x: number}", values["sink"])
	require.Equal(t, "fn (p: &'static mut {x: number}) -> void", values["cache"])
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

// --- Borrow-alias bindings ---

// Binding a `&mut` borrow into an explicit `&` annotation establishes q as an
// immutable view of p. q dies within p's region and never escapes, so the
// lifetime sort accepts it with no error.
func TestInferImmutableBorrowAliasLocally(t *testing.T) {
	src := `fn f(p: &mut {x: number}) {
  val q: &{x: number} = p
  return q.x
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: &mut {x: number}) -> number", values["f"])
}

// An immutable borrow alias that is RETURNED carries its lifetime to the output.
// The borrow flows out at p's lifetime, so the return renders an immutable borrow
// over the same named lifetime as the parameter. This is the D4 display path for
// a borrow alias that reaches an output.
func TestInferImmutableBorrowAliasReturnedCarriesLifetime(t *testing.T) {
	src := `fn f(p: &mut {x: number}) {
  val q: &{x: number} = p
  return q
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: &'a mut {x: number}) -> &'a {x: number}", values["f"])
}

// Returning a local borrow into an OWNED return slot errors. The return
// annotation is owned, so the borrow flowing into it trips the escape guard. The
// alias only relaxes the binding itself, not the function boundary.
func TestInferBorrowAliasEscapingOwnedReturnRejected(t *testing.T) {
	src := `fn f(p: &mut {x: number}) -> {x: number} {
  val q: &{x: number} = p
  return q
}`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"1:30-1:41: borrowed value object does not live long enough to satisfy object",
	}, messagesWithSpan(errs))
}

// An owned-immutable source binds through an owned annotation as an owned
// binding, with no borrow lifetime introduced. q then flows into an
// owned-immutable parameter without an escape error.
func TestInferOwnedToOwnedImmutableBinding(t *testing.T) {
	src := `fn acceptObj(o: {x: number}) -> number { return o.x }
fn f(r: {x: number}) {
  val q: {x: number} = r
  return acceptObj(q)
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (r: {x: number}) -> number", values["f"])
}

// An owned-mutable value binds through an owned-immutable annotation as a
// mut-decayed owned binding, with no borrow lifetime introduced. q then flows
// into an owned-immutable parameter without an escape error.
func TestInferOwnedMutDecayToOwnedImmutable(t *testing.T) {
	src := `fn acceptObj(o: {x: number}) -> number { return o.x }
fn f() {
  val items: mut {x: number} = {x: 1}
  val q: {x: number} = items
  return acceptObj(q)
}`
	_, _, errs := inferSource(t, src)
	require.Empty(t, errs)
}

// A tuple borrow alias is the tuple twin of the object case. A `&mut` tuple
// borrow aliased through an explicit `&` tuple annotation and returned carries
// the source's lifetime to the output.
func TestInferTupleBorrowAliasReturnsLifetime(t *testing.T) {
	src := `fn f(p: &mut [number, number]) {
  val q: &[number, number] = p
  return q
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: &'a mut [number, number]) -> &'a [number, number]", values["f"])
}

// The tuple twin of TestInferOwnedToOwnedImmutableBinding. An owned tuple source
// binds through an owned tuple annotation as an owned binding, with no borrow
// lifetime introduced. q then flows into an owned tuple parameter without an
// escape error.
func TestInferOwnedToOwnedTupleBinding(t *testing.T) {
	src := `fn acceptTup(o: [number, number]) -> number { return 0 }
fn f(r: [number, number]) {
  val q: [number, number] = r
  return acceptTup(q)
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (r: [number, number]) -> number", values["f"])
}

// A borrow alias composes with an inexact annotation. Aliasing `&mut {x, y}`
// through `&{x, ...}` produces an immutable view that names only `x`, with the
// source's extra `y` tolerated by width subtyping. Returned, the view carries
// the source's lifetime and renders its inexact tail.
func TestInferInexactBorrowAlias(t *testing.T) {
	src := `fn f(p: &mut {x: number, y: number}) {
  val q: &{x: number, ...} = p
  return q
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: &'a mut {x: number, y: number}) -> &'a {x: number, ...}", values["f"])
}

// --- PR2: lowering `&` borrow annotations to RefType ---

// A bare `&` parameter lowers to an immutable borrow with a fresh inferred lifetime.
// Returned, the borrow carries that lifetime to the output, which is then load-bearing
// and named `'a`, so the signature renders the borrow in `&'a` notation rather than the
// old `'a {x}` form.
func TestInferBorrowAnnImmutableParam(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: &{x: number}) { return p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: &'a {x: number}) -> &'a {x: number}", values["f"])
}

// A `&mut` parameter lowers to a mutable borrow with a fresh inferred lifetime. This is
// the same RefType the borrow-by-default `mut {x}` param produced, now written
// explicitly, so it renders identically as `&'a mut {x}`.
func TestInferBorrowAnnMutableParam(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: &mut {x: number}) { return p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: &'a mut {x: number}) -> &'a mut {x: number}", values["f"])
}

// A named `&'a` lifetime resolves to one lifetime variable shared by every occurrence in
// the function, so two parameters annotated `&'a` borrow at the same lifetime. Returning
// one makes that lifetime load-bearing, and the display names it `'a` on both params.
func TestInferNamedBorrowLifetimeShared(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: &'a {x: number}, q: &'a {x: number}) { return p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: &'a {x: number}, q: &'a {x: number}) -> &'a {x: number}", values["f"])
}

// A borrow of a value type has nothing to point at. `&number` wraps a primitive, which
// is excluded from RefInner, so lowering reports it as an unsupported feature rather than
// fabricating a borrow over a non-borrowable type.
func TestInferBorrowOfNonBorrowableRejected(t *testing.T) {
	_, _, errs := inferSource(t, `fn f(p: &number) -> number { return 0 }`)
	require.Equal(t, []string{"1:9-1:16: Unsupported: borrow of a non-borrowable type"}, messagesWithSpan(errs))
}

// --- PR 4: member reads borrow the receiver ---

// A member read off an OWNED receiver yields the field's owned value, not a
// receiver-bounded borrow, so the field can be moved out of the object (PR 7).
// Here `pair.a` returns as the owned `{id: number}` and moves out of the frame,
// where a borrow of a frame-local would be rejected as not living long enough. A
// borrowed receiver still yields a borrow; the tests below pin that path.
func TestInferOwnedReceiverFieldReadIsOwned(t *testing.T) {
	src := `fn f() -> {id: number} {
  val pair = {a: {id: 1}, b: {id: 2}}
  return pair.a
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> {id: number}", values["f"])
}

// A member read that escapes carries the receiver's borrow lifetime through.
// Deep `mut` makes the field owned-mutable, so the read yields a mutable borrow.
func TestInferMemberReadEscapingBorrowsReceiver(t *testing.T) {
	src := `fn f(p: &mut {a: {x: number}}) {
  return p.a
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a>(p: &'a mut {a: {x: number}}) -> &'a mut {x: number}",
		values["f"])
}

// A member read consumed inside the body leaves the receiver lifetime free,
// so D4's display-time elision drops it from the rendered signature. `obj.a`
// reads as a borrow inside the body, but the binding `q` never escapes, so
// the param's borrow lifetime does not need to be named.
func TestInferMemberReadLocalElidesLifetime(t *testing.T) {
	src := `fn f(p: &mut {a: {x: number}}) {
  val q = p.a
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: &mut {a: {x: number}}) -> void", values["f"])
}

// A field whose static type is itself an immutable borrow reads through as
// the same borrow rather than nesting under the receiver's lifetime. The
// flat copy-out keeps reads of `&T` fields from producing `& &T` types,
// setting up PR 9's nested-borrow normalization. Here `obj.a: &{x: number}`
// reads back as a `&'a {x: number}` at the field's own annotation-minted
// lifetime, not a depth-two borrow over the receiver.
func TestInferMemberReadFlatBorrowOfRefField(t *testing.T) {
	src := `fn f(obj: {a: &{x: number}}) {
  return obj.a
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a>(obj: {a: &'a {x: number}}) -> &'a {x: number}",
		values["f"])
}

// A `&mut` field falls through fieldReadBorrow's no-wrap branch, so the read
// returns the field's mutable borrow at the field's own lifetime rather than
// nesting under the receiver's. The TypeVar result var coalesces to the
// concrete field type, and the function returns that `&mut` borrow at the
// annotation-minted lifetime. Aliasing exclusivity on the surviving mut
// borrow needs the move-engine work in PR 6, and PR 9 normalizes the
// otherwise uninhabitable `&mut &mut` shape.
func TestInferMemberReadOfMutBorrowField(t *testing.T) {
	src := `fn f(p: {a: &mut {x: number}}) {
  return p.a
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a>(p: {a: &'a mut {x: number}}) -> &'a mut {x: number}",
		values["f"])
}

// A primitive field stays a value, since `PrimType` is not a `RefInner`. The
// PR 4 wrap is skipped and the read returns the primitive directly. This is
// the same shape pre-PR-4 returned. Pinning it here guards against the wrap
// firing on non-borrowable fields and tripping the bare<:RefType escape
// guard when the field flows into a primitive sink.
func TestInferMemberReadPrimitiveStaysValue(t *testing.T) {
	src := `fn f(p: &mut {x: number}) {
  return p.x
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: &mut {x: number}) -> number", values["f"])
}

// An explicit `&obj.f` shares the receiver-bounded lifetime of the implicit
// read. PR 4 routes a `&`-of-MemberExpr through inferBorrowOfMember, which
// produces a `&` borrow at the receiver lifetime. This matches the shape the
// implicit read produces, with no extra wrapping. Both the param and the
// return render at the receiver's named lifetime.
func TestInferExplicitBorrowOfMemberSharesLifetime(t *testing.T) {
	src := `fn f(obj: &mut {a: {x: number}, b: {y: number}}) {
  return &obj.b
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a>(obj: &'a mut {a: {x: number}, b: {y: number}}) -> &'a {y: number}",
		values["f"])
}

// An explicit `&mut obj.g` mutably borrows the field at the receiver's
// lifetime when the receiver supports a mutable view. The receiver is an
// owned-mut object, so the mut requirement lowers via the RefType <: RefType
// rule. The partial-moves work in PR 7 adds path-granular tracking that
// leaves a disjoint sibling such as `obj.a` independently usable. This test
// pins the typing rule only.
func TestInferExplicitMutBorrowOfMemberAcceptsMutReceiver(t *testing.T) {
	src := `fn f(obj: &mut {a: {x: number}, b: {y: number}}) {
  return &mut obj.b
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a>(obj: &'a mut {a: {x: number}, b: {y: number}}) -> &'a mut {y: number}",
		values["f"])
}

// An explicit `&mut obj.f` on an immutable receiver is rejected: the mut
// requirement lowers to `mut {f: T, ...}`, and an immutable receiver cannot
// fill the mutable slot. This is the same mutability gate inferMemberAssign
// imposes for a field write `obj.f = v`, surfacing here at the explicit mut
// borrow.
func TestInferExplicitMutBorrowOfMemberOnImmutableRejected(t *testing.T) {
	src := `fn f(obj: &{a: {x: number}}) {
  return &mut obj.a
}`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"1:11-1:28: cannot constrain immutable object <: mutable object",
	}, messagesWithSpan(errs))
}

// A usage-inferred receiver keeps its pre-PR-4 read behaviour. A
// usage-inferred receiver is an un-annotated param whose shape the body's
// uses determine. The wrap fires only off a concrete ObjectType carrier, so
// a TypeVar carrier returns the field's result var directly. The param
// closes to its inferred object shape, and the function returns the field's
// primitive type rather than a borrow.
func TestInferMemberReadInferredReceiverUnchanged(t *testing.T) {
	src := `fn f(p) { return p.x }
val r = f({x: 5})`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "5", values["r"])
}

// `&mut obj["foo"]` for a reference-shaped field matches the dot form. The
// constant-string index access is the bracket form of dot access, so the
// dispatch in inferBorrow routes it through inferBorrowOfMember and lifts
// the receiver's borrow lifetime onto the mutable result. Without the
// IndexExpr branch the operand types as an immutable wrap that the outer
// `&mut` cannot upgrade.
func TestInferExplicitMutBorrowOfConstIndexAcceptsMutReceiver(t *testing.T) {
	src := `fn f(obj: &mut {a: {x: number}, b: {y: number}}) {
  return &mut obj["b"]
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a>(obj: &'a mut {a: {x: number}, b: {y: number}}) -> &'a mut {y: number}",
		values["f"])
}
