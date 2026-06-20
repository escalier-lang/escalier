# Move / affine semantics for owned values

## Status and scope

This document specifies the behaviour we want from move/affine semantics in
Escalier. It is the requirements portion of the RFC tracked at
[#762](https://github.com/escalier-lang/escalier/issues/762), the use-after-move
item of the broader sound borrow checker effort
[#618](https://github.com/escalier-lang/escalier/issues/618).

It assumes the M4 borrow and lifetime machinery has landed: the
`RefType{Mut, Lt, Inner}` wrapper, the lifetime sort (`LifetimeVar`,
`StaticLifetime`, `constrainLt`), borrow origination and escape-to-`'static`,
and the liveness-based mutability-transition checker in
[internal/solver/transitions.go](../../internal/solver/transitions.go). It also
assumes **G3** is in place: a bare object or tuple annotation in reference
position reborrows its initializer as a local immutable view rather than an owned
slot. G3 is in review as [#764](https://github.com/escalier-lang/escalier/pull/764).

This document describes target behaviour, not an implementation plan. The
implementation plan is a separate doc.

## Motivation

Escalier is biased toward immutability and guarantees that an immutable reference
never observes a mutation. Today that guarantee is enforced site by site. The
mutability-transition checker has a Rule 1 for mutable-to-immutable bindings, a
Rule 2 for immutable-to-mutable bindings, and ad-hoc mutability-dropping logic at
global writes. Each new place a value can flow needs its own rule, and some flow
paths slip through. The motivating gap from #762:

```esc
// module scope
var sink: {x: number} = {x: 0}   // immutable global

fn leak(p: mut {x: number}) -> void {
    sink = p        // a mut borrow is stored into an immutable global
    p.x = 5         // mutating through p now changes what `sink` reads
}
```

Storing a mutable borrow into longer-lived immutable state, then mutating through
the borrow, breaks the immutability guarantee. Patching this one site is not
enough, because the same shape recurs at returns, field stores, and escaping
function arguments.

Move/affine semantics is the unifying fix. Instead of a separate rule per flow
site, a single rule governs every site: when a value leaves the region its
current binding owns, ownership **moves** out of that binding, the binding is
**consumed**, and any later use of it is a compile-time use-after-move error.
Because the move consumes the source, no conflicting path back to the value
survives, and the per-site mutability-dropping logic is no longer needed.

## The soundness invariant

The one condition we must always preserve:

> No immutable reference ever observes a mutation. If a value is reachable
> through an immutable reference that is live at some point, then between the
> creation of that reference and its last use, the value is not mutated through
> any other path.

Correctness comes first: this invariant must hold on every accepted program.
Precision is the secondary goal: among the programs that preserve the invariant,
we want to reject as few as possible. The two goals trade off only when we are
unsure whether a program is safe; there we reject, and the precision sections
below describe where we work to avoid being unsure.

## Owned versus borrowed values

Every object, tuple, and other reference-shaped value is, at each point in the
program, either **owned** by a binding or **borrowed** from an owner. The
distinction is carried in the type by the
[`RefType`](../../internal/soltype/type.go) wrapper, whose lifetime field `Lt`
decides ownership.

- **Owned** — `Lt` is nil. The binding is the value's owner; the value's
  lifetime is the binding's own region. Two flavours:
  - **owned-immutable** — `RefType{Mut: false, Lt: nil}`, which `NewRef`
    collapses to the bare inner type (`{x: number}`). The owner may read but not
    mutate.
  - **owned-mutable** — `RefType{Mut: true, Lt: nil}`, printed `mut {x: number}`.
    The owner may read and mutate.
- **Borrowed** — `Lt` is a lifetime. The value is a reference into storage owned
  elsewhere, valid only for the lifetime `Lt`. Two flavours:
  - **immutable borrow** — `RefType{Mut: false, Lt: 'a}`, printed `'a {x: number}`.
    A read-only view for the duration of `'a`.
  - **mutable borrow** — `RefType{Mut: true, Lt: 'a}`, printed `mut 'a {x: number}`.
    A read-write view for the duration of `'a`.

Primitives (`number`, `string`, `boolean`), functions, and promises are **value
types**: they are never wrapped in a `RefType` and are excluded from `RefInner`.
They carry no interior mutability worth tracking, so ownership and moves do not
apply to them. They are freely duplicated.

The four quadrants:

| | owned (`Lt` nil) | borrowed (`Lt` = `'a`) |
|---|---|---|
| **immutable** (`Mut` false) | `{x: number}` | `'a {x: number}` |
| **mutable** (`Mut` true) | `mut {x: number}` | `mut 'a {x: number}` |

Ownership and mutability are orthogonal axes. Ownership decides who is
responsible for the value and whether a transfer consumes the source. Mutability
decides whether writes are allowed. Move/affine semantics governs the ownership
axis; the mutability axis is governed by the existing exclusivity rule.

## When owned versus borrowed is inferred

The default is owned for freshly produced values and borrowed for values reached
through a parameter or a member read. The full set of rules:

1. **Freshly produced values are owned, and immutable by default.** An object,
   tuple, or array literal, a class constructor call, and any other expression
   that builds a new value all yield a value the binding owns. Escalier defaults
   to immutability, so a fresh value is owned-immutable unless the program opts
   into mutability. A class instance is immutable regardless of whether the class
   has `mut self` methods — a bare `val p = Point(5, 10)` is `Point`, never
   `mut Point`, per [#499](https://github.com/escalier-lang/escalier/issues/499)
   and [TestDefaultMutabilityFromClass](../../internal/checker/tests/class_test.go).
   The opt-in is at the binding pattern, `val mut p = Point(5, 10)`, or via a
   `mut` annotation, `val obj: mut {x: number} = {x: 0}`. The same applies to
   object and tuple literals: `{x: 0}` is owned-immutable until a `mut` binding or
   annotation makes it owned-mutable.

2. **Function parameters are borrowed by default.** A reference-typed parameter
   without an explicit lifetime is a borrow of whatever the caller lends. The
   checker attaches a fresh lifetime variable to it, so inside the body the
   parameter is `'a {…}` or `mut 'a {…}`. The caller retains ownership; the callee
   only borrows for the call. A parameter is never owned in the `Lt`-nil sense a
   local binding is — it is always reached across the call boundary, so it always
   carries a lifetime. A parameter becomes *consuming* only when its lifetime is
   `'static`, which the body forces by letting the parameter escape, or which a
   signature states explicitly as `p: mut 'static {…}`. A consuming parameter
   requires the caller to give up ownership; the lifetimes design phrases this to
   the caller as "pass a clone if you need to keep access." There is no separate
   ownership annotation.

3. **Bare annotations in reference position reborrow (G3).** Binding a reference
   into a bare object or tuple annotation lowers the annotation to an immutable
   borrow with a fresh lifetime, not an owned slot. `val q: {x: number} = p` for
   `p: mut {x: number}` makes `q` a local immutable view that borrows `p`; `p`
   keeps ownership. G3 ships the reborrow only for bare, immutable annotations: a
   `mut {x: number}` annotation lowers to an owned-mutable type and a
   lifetime-qualified annotation names the borrow explicitly, neither rewritten by
   G3. Under move/affine semantics the move-versus-borrow choice is escape-driven
   and applies to every form, so a local `mut` binding of a reference is a mutable
   reborrow that retains the source — the multiple-mutable-aliases case — and only
   a binding that escapes moves. The bare/`mut` split decides the *mutability* of
   the resulting view, not whether the source is moved.

4. **Member reads borrow the receiver.** Reading `obj.f` yields a borrow of the
   field for a lifetime bounded by the receiver, not a fresh owned value. A read
   that is immediately consumed locally needs no lifetime in the rendered type;
   one that escapes carries the receiver's lifetime.

5. **Escape forces ownership and lifetime.** A value that flows into storage
   outliving its current binding — a module-level or other longer-lived binding,
   a field of a longer-lived object, a return value, or an escaping closure
   capture — has its lifetime constrained to outlive the destination. For a
   value owned by a local binding, this is the trigger for a move, described
   next. For a borrow, escape that the borrow's lifetime cannot satisfy is a
   borrow-escape error.

The principle behind these rules: **owned** is inferred when a binding is the sole
producer or holder of a value, and **borrowed** is inferred when a binding reaches
a value that something else owns. The lifetime machinery already in place
computes which case applies; move semantics reads its verdict rather than running
a separate analysis.

## Move / affine semantics

At every site where a value flows from a source expression into a destination,
the transfer has one of three outcomes.

1. **Copy.** The source is a value type. It is duplicated; the source binding
   stays fully usable. Primitives, functions, and promises always copy.

2. **Borrow.** The destination takes a reference whose lifetime is bounded within
   the source binding's region. The source keeps ownership and stays usable. The
   borrow is governed by the mutability-exclusivity rule for its lifetime. This
   is the outcome for a read-only argument to a non-escaping parameter, a
   `mut`-to-`mut` local reborrow, sharing an immutable value, and a G3 reborrow
   that stays local.

3. **Move.** Ownership transfers out of the source binding. The source binding is
   **consumed**: any later use of it is a use-after-move error. This is the
   outcome whenever the value **escapes** the source binding's region.

### What counts as escape

Escape is the single trigger for a move. A value escapes its current owner's
region when it must outlive that region. Concretely, an owned value moves when it
flows into:

- a longer-lived binding — a module-level or other outer-scope binding;
- a field or element of an object that outlives the source;
- a `return` (the value outlives the call frame);
- a function argument whose parameter the callee lets escape, which the callee's
  inferred lifetime reports as `'static` or as a lifetime outliving the call;
- a closure that itself escapes, capturing the value.

A value does **not** escape — so the transfer is a borrow, and the source is
retained — when it flows into a strictly shorter-lived destination: a read-only
or non-escaping function argument, an inner-scope binding that dies first, or a
local reborrow.

The escape verdict is derived from the lifetime sort, not recomputed. A value
escapes exactly when its lifetime is forced to outlive its source binding's
scope, which in the constraint graph means the lifetime is pushed up to
`'static` or to a lifetime that strictly outlives the local region. This is the
same `constrainLt(... <: 'static)` machinery the escape rule already emits, so
move detection is a query over existing constraints.

### Use-after-move

Once a binding is consumed by a move, using it again is an error. "Use" means
reading it, mutating through it, calling a method on it, moving it again, or
borrowing it. Each later use is reported against the move site and the use site.

```esc
val p = {x: 0}              // p owns a fresh value
val q = storeGlobally(p)    // p escapes into global state — moved, p consumed
print(p.x)                  // ERROR: use of `p` after it was moved into global state
```

### Affine, not linear

Ownership is **affine**: a value may be consumed at most once, and a binding that
is never moved is simply dropped at the end of its scope. There is no obligation
to move or explicitly destroy a value, and no use-before-anything requirement.
The JavaScript runtime is garbage-collected, so drops need no code. Linearity —
requiring every value to be consumed exactly once — is not a goal.

## Flow sites

Move/affine semantics applies the same copy/borrow/move decision uniformly. This
section lists the sites and the outcome at each.

- **`val` / `var` binding.** `val y = x` copies if `x` is a value type, borrows
  if `y` is a bounded local view of `x` (a G3 reborrow, or a `mut`-to-`mut`
  reborrow), and moves if `y` outlives `x` or escapes.
- **Reassignment.** `y = x` follows the same decision as a binding. A reassigned
  `var` that previously owned a value drops the old value and takes the new one.
- **Field / element store.** `obj.f = x` and `t[i] = x` move `x` when `obj` or
  `t` outlives `x`. Storing into a longer-lived object is the canonical escape.
- **`return`.** `return x` moves `x` out of the frame unless `x` is a value type
  or a borrow whose lifetime already outlives the frame.
- **Function argument.** Passing `x` to a parameter moves `x` when the callee's
  signature lets that parameter escape, and borrows otherwise. A read-only or
  non-escaping parameter borrows; the caller keeps `x`.
- **Closure capture.** Capturing `x` in a closure that escapes moves `x` into the
  closure. Capturing in a closure that stays local borrows.
- **Destructuring.** Destructuring moves or borrows each extracted part following
  the same rules; see partial moves below.
- **`match` arms.** A pattern that binds a part of the scrutinee moves or borrows
  that part per the arm's use, consistent with destructuring.

## Partial moves and field-level ownership

Moving one field of an owned object out does not consume the whole object; it
consumes that field's slot. After a partial move, the moved field may not be
read, but the other fields remain usable.

```esc
val pair = {a: makeWidget(), b: makeWidget()}
storeGlobally(pair.a)      // moves pair.a out — pair.a consumed
print(pair.b.id)           // OK: pair.b is untouched
print(pair.a.id)           // ERROR: use of `pair.a` after it was moved
```

A read of the whole object after a partial move is an error if it would expose a
moved field, and allowed if it only reaches live fields. The precision target is
field-granular tracking; the conservative fallback, acceptable if field-granular
tracking proves costly, is to consume the whole object on any field move and
record that as a precision limitation.

## Why the invariant holds

Move/affine semantics preserves the soundness invariant by eliminating, at the
moment of transfer, every path that could later mutate a value an immutable
reference depends on.

**Escaping into immutable state.** Revisiting the motivating gap: storing a value
into the immutable global moves it. The source binding is consumed, so the
mutation that followed is now a use-after-move error.

```esc
var sink: {x: number} = {x: 0}

fn leak(p: mut {x: number}) -> void {
    sink = p        // p escapes into 'static state — moved, p consumed
    p.x = 5         // ERROR: use of `p` after it was moved into `sink`
}
```

**Freezing a mutable value.** Building a value mutably and then exposing it
immutably moves the mutable owner into the immutable binding when the immutable
binding outlives the construction, leaving no mutable path.

```esc
fn build() -> {x: number} {
    val tmp: mut {x: number} = {x: 0}
    tmp.x = 42
    return tmp       // tmp escapes the frame — moved into the return; no mutable alias survives
}
```

**Local immutable view.** A G3 reborrow that stays local does not move; it
borrows. The invariant is preserved instead by mutability exclusivity: while the
immutable view is live, the mutable owner may not mutate.

```esc
val p: mut {x: number} = {x: 0}
val q: {x: number} = p     // reborrow — q is a local immutable view of p; p retained
print(q.x)
p.x = 5                    // ERROR: p mutated while immutable view q is live
```

The two mechanisms cover the two ways the invariant can be threatened. A value
that **escapes** is handled by a move that consumes the source. A value that
stays **local** but is viewed both mutably and immutably is handled by exclusivity
over the borrow's lifetime.

## Precision

Correctness requires rejecting unsafe programs; precision requires accepting safe
ones. The behaviours below keep precision high.

- **Value types never move.** Primitives, functions, and promises copy, so code
  that passes numbers and strings around is never affected.
- **Reads and non-escaping borrows do not consume.** Passing a value to a
  function that only reads it, or binding a bounded local view, leaves the source
  usable. Most application code is linear data flow and triggers no move at all.
- **Multiple mutable borrows are allowed.** Escalier is not Rust. Several mutable
  borrows of one value may be live at once. Move/affine semantics applies to
  ownership transfer, not to mutable borrowing, so `mut`-to-`mut` aliasing stays
  legal and does not consume the source.

  ```esc
  val a: mut {x: number} = {x: 1}
  val b: mut {x: number} = a    // reborrow, not a move — a retained
  b.x = 2
  print(a.x)                    // OK — prints 2
  ```
- **Conditional moves track per-path.** A value moved on one branch and untouched
  on another is consumed only on paths where the move occurs. A use after a
  conditional move is an error only if some path that reaches it moved the value.
- **Reborrows of mutable references at call boundaries.** Passing a mutable borrow
  to a function that borrows it back for the call duration reborrows rather than
  moves, so the caller regains full access after the call returns.

## Relationship to the mutability-transition checker

Move/affine semantics does not replace the whole transition checker; it takes over
the escape dimension and lets the rest shrink.

- **Move subsumes the escape-site logic.** Every place the current checker drops
  mutability or special-cases a store into longer-lived state — global writes,
  returns, escaping field stores, escaping arguments — is now a move. The ad-hoc
  per-site mutability-dropping logic is removed in favour of the single move rule.
- **Exclusivity is retained for local aliasing.** The case Escalier deliberately
  permits — a value viewed locally through both mutable and immutable references
  whose lifetimes overlap — is not an escape and is not a move. It stays governed
  by a mutability-exclusivity rule: an immutable borrow and a mutable path to the
  same value may not both be live and conflicting. This is the residual of Rules
  1 and 2, now scoped to local, non-escaping aliasing, with the escape cases
  handed to move semantics.
- **Rule 3 is unchanged.** Multiple simultaneous mutable borrows remain allowed,
  because mutable-to-mutable aliasing is a borrow, never a move.

A consequence for alias tracking: a move consumes one binding, but if other live
mutable aliases of the same value exist, consuming one binding alone does not kill
them. The exclusivity rule, which reasons over the alias set rather than a single
binding, covers that residual. Move semantics and alias-set exclusivity compose;
neither alone is complete.

## Interaction with the JavaScript runtime

Escalier compiles to garbage-collected, single-threaded JavaScript. Move/affine
semantics is a compile-time discipline only; it emits no runtime drops, refcounts,
or copies. A move is the absence of generated code plus a compile-time mark that
the source binding is dead.

Closure capture is the one place the runtime model needs care. A captured variable
is an implicit alias. A closure that escapes — stored, returned, or passed to a
function that retains it — extends the captured value's region, so a capture by an
escaping closure is a move of the captured value into the closure. A closure that
stays local borrows its captures. Repeated `await` of one promise returns the same
reference rather than a copy, so a borrowed payload awaited twice is ordinary
aliasing, governed by exclusivity, not a second move.

## Ownership visibility and ergonomics

Ownership is inferred, and the lifetime that distinguishes an owned value from a
borrow is elided from the canonical type. So the same printed type can be owned in
one place and borrowed in another: a binding of type `{x: number}` is owned when
it holds a fresh value and is a borrow when it reborrows another binding (G3).
Nothing in the printed annotation says which. This document keeps that inference,
because reborrowing is strictly more permissive than always moving and is what
lets a program take a cheap immutable view of a value without giving up the
mutable original.

The risk is that a developer reads `{x: number}` as an owned object and is
surprised when it behaves like a borrow. Two facts bound that risk:

- The owned/borrow distinction is **observable only at a small, fixed set of
  points**: the escape sites where a value must outlive its source — returns,
  stores into longer-lived state, fields of an outliving object, escaping closure
  captures — and the one local case where the source is mutated again after a
  local immutable view of it has died. Everywhere else an owned value and a borrow
  of the same type behave identically.
- At every one of those points the divergence surfaces as a **compile-time
  error**, never as silently different runtime behaviour. The failure mode is "the
  compiler won't let me return this," answered by a diagnostic, not a latent bug.

Because the distinction is invisible in the type but visible in behaviour, the
ergonomic burden falls on diagnostics and on-demand display. The requirements:

1. **Errors carry the ownership story.** When a borrow cannot escape, the message
   names the borrow's source and explains the ownership relationship — for
   example, "`q` borrows `p` and cannot outlive it; `p` is a local value" — and
   suggests a concrete fix, such as returning the source directly or cloning. This
   mirrors how the lifetime errors already explain an aliasing relationship that
   is otherwise hidden.
2. **Borrow-ness is discoverable on demand.** The canonical type display stays
   elided and clean, rendering `{x: number}` for both owned and borrowed values.
   LSP hover and a verbose `showLifetimes` mode reveal the inferred lifetime and
   the borrow source, so a developer who wants to know whether a binding is owned
   or borrowed can find out without every signature carrying lifetime noise.

The aim is that a developer never has to reason about ownership to write ordinary
local code, encounters the distinction only through an actionable error or a
deliberate hover, and is never silently surprised by it at runtime.

## Non-goals and deferred questions

- **Linearity / mandatory consumption.** Values need not be consumed; unused
  owned values are dropped. Out of scope.
- **Interior-mutability escape hatches.** Patterns for cyclic mutable data that
  outlive a single owner — a `Cell`-like wrapper — are noted in #618 as a separate
  concern and are not specified here.
- **Field-granular tracking depth.** Partial moves target field granularity. How
  deep the tracking goes through nested objects and through dynamic index
  expressions is an implementation-precision question for the plan, with the
  whole-object-consume fallback as the conservative floor.
- **Cross-package moves.** Move behaviour at the boundary of imported,
  body-less declarations depends on declared lifetimes and is deferred to the
  library-import work, consistent with the elision rules for lifetimes.
- **Surface syntax for a consuming parameter.** A parameter is consuming when its
  lifetime is `'static`. Today that is written by spelling the lifetime out,
  `p: mut 'static {…}`, which is accurate but exposes a lifetime most code never
  names. Whether to keep `'static` as the user-facing way to demand ownership, or
  to add a dedicated keyword that reads as "consume this argument," is an unmade
  design decision. It does not change behaviour — only how a caller-visible
  ownership transfer is spelled.
- **Diagnostics.** Exact wording and blame spans for use-after-move and
  move-on-escape errors are left to the implementation plan; they should name the
  move site, the later use, and why the transfer was a move.
