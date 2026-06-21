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
[internal/solver/transitions.go](../../internal/solver/transitions.go).

Borrows are written with an explicit `&`, following Rust: `&{x: number}` and
`&mut {x: number}` are immutable and mutable references whose lifetime is
inferred, and `&'a {x: number}` and `&'a mut {x: number}` name the lifetime. A
bare annotation is therefore always *owned*: `{x: number}` is owned-immutable and
`mut {x: number}` is owned-mutable. This supersedes the earlier **G3** proposal
([#764](https://github.com/escalier-lang/escalier/pull/764)), where a bare
annotation in reference position implicitly reborrowed its initializer. With `&`
the reborrow is spelled out, so a bare annotation no longer needs to mean anything
other than ownership.

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
    sink = p        // an owned mutable value is stored into an immutable global
    p.x = 5         // mutating it now changes what `sink` reads
}
```

Storing a mutable value into longer-lived immutable state, then mutating it,
breaks the immutability guarantee. Patching this one site is not
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
  elsewhere, valid only for the lifetime `Lt`, and is written with a leading `&`.
  Two flavours, each with an inferred or an explicitly named lifetime:
  - **immutable borrow** — `RefType{Mut: false, Lt: 'a}`, printed `&{x: number}`
    when the lifetime is inferred and `&'a {x: number}` when it is named. A
    read-only view for the duration of `'a`.
  - **mutable borrow** — `RefType{Mut: true, Lt: 'a}`, printed `&mut {x: number}`
    when the lifetime is inferred and `&'a mut {x: number}` when it is named. A
    read-write view for the duration of `'a`.

Primitives (`number`, `string`, `boolean`), functions, and promises are **value
types**: they are never wrapped in a `RefType` and are excluded from `RefInner`.
They carry no interior mutability worth tracking, so ownership and moves do not
apply to them. They are freely duplicated.

The four quadrants:

| | owned (`Lt` nil) | borrowed (`Lt` = `'a`) |
|---|---|---|
| **immutable** (`Mut` false) | `{x: number}` | `&{x: number}` / `&'a {x: number}` |
| **mutable** (`Mut` true) | `mut {x: number}` | `&mut {x: number}` / `&'a mut {x: number}` |

Ownership and mutability are orthogonal axes. Ownership decides who is
responsible for the value and whether a transfer consumes the source. Mutability
decides whether writes are allowed. Move/affine semantics governs the ownership
axis; the mutability axis is governed by the existing exclusivity rule.

### Borrows of a mutable owned value

A mutable owned value may be borrowed many times over. Its borrows fall into two
phases that never overlap:

- multiple immutable borrows (`&{x: number}`) may be live at once, **or**
- multiple mutable borrows (`&mut {x: number}`) may be live at once,

but the lifetime of an immutable borrow and the lifetime of a mutable borrow of
the same value may never overlap. While any mutable borrow is live, no immutable
borrow of that value may be live, and vice versa. That is exactly what preserves
the soundness invariant: an immutable borrow never coexists with a mutable path,
so it never observes a mutation. Single-threaded execution is what makes the
multiple-simultaneous-mutable-borrows case safe — there is no data race to
exclude, and the invariant forbids only mixing the two kinds, not aliasing within
one kind.

## When owned versus borrowed is inferred

Freshly produced values are owned. A value is borrowed when an annotation marks it
with `&`, when a member read reaches into a receiver, or when inference on an
unannotated binding finds the value reaches storage something else owns. The full
set of rules:

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

   A `val mut` binding may also take ownership of an existing owned-immutable
   value by moving it. For an owned-immutable `p`, `val mut q = p` moves `p` into
   the mutable binding `q` and consumes `p`. Because the move leaves `q` the sole
   owner, it is sound for `q` to be mutable — no immutable reference to the value
   survives the move. This is the mirror image of freezing: freezing moves an
   owned-mutable value into an immutable binding, and this moves an owned-immutable
   value into a mutable one.

   ```esc
   val p = Point(5, 10)       // owned-immutable
   val mut q = Point(0, 0)    // owned-mutable, independent value
   ```

   ```esc
   val p = Point(5, 10)       // owned-immutable
   val mut q = p              // move: p into mutable q; p consumed
   p.x                        // ERROR: use of `p` after it was moved into `q`
   ```

2. **A parameter is owned or borrowed according to its annotation.** A parameter
   written with `&` is a borrow — `p: &{x: number}` or `p: &mut {x: number}` — and
   the caller retains ownership, lending the value for the call. The lifetime is
   inferred unless named with `&'a`. This is the common case: a function that only
   reads or transiently uses its argument takes it by `&`, and the borrow keeps the
   caller's value alive and usable after the call. A parameter written *without*
   `&` is owned — `p: {x: number}` or `p: mut {x: number}` — and is *consuming*:
   the signature declares an ownership transfer, so the caller gives the value up
   at the call site. An unannotated parameter is inferred: the checker reads whether
   the body lets the value escape and picks owned or borrowed accordingly. A
   borrowing parameter no longer relies on an implicit lifetime — the `&` states the
   borrow outright, and the lifetime appears only when it is load-bearing.

3. **A bare annotation is owned; a `&` annotation borrows.** An annotation is read
   literally. A bare object or tuple annotation fixes shape and mutability and
   denotes an *owned* value — `{x: number}` owned-immutable, `mut {x: number}`
   owned-mutable. To annotate a borrow, write `&`: `&{x: number}` and
   `&mut {x: number}` leave the lifetime inferred, while `&'a {x: number}` and
   `&'a mut {x: number}` name it. This holds in every annotation position — a
   `val`/`var` binding, a return type, a field, a parameter.

   So `val q: &{x: number} = p` makes `q` an immutable borrow of `p` with an
   inferred lifetime, leaving `p` its owner; writing `val q: {x: number} = p`
   instead *moves* `p` into the owned binding `q` and consumes `p`. Whether a value
   is owned or borrowed is now visible in the annotation rather than inferred from
   it. The same distinction works in return position, where a `&` return borrows
   from a `&` parameter:

   ```esc
   fn f(p: &mut {x: number}) -> &{x: number} {
       val q: &{x: number} = p    // downgrade the mutable borrow to an immutable one
       return q
   }
   // inferred: fn <'a>(p: &'a mut {x: number}) -> &'a {x: number}
   ```

   The lifetime `'a` threads the input borrow to the output borrow, so it is
   load-bearing and shown. This replaces the implicit bare-annotation reborrow of
   G3 ([#764](https://github.com/escalier-lang/escalier/pull/764)): the reborrow is
   now spelled with `&`, and a bare annotation means ownership in every position,
   so the inferred type and a written annotation agree without a special reborrow
   rule. A `mut` modifier still fixes mutable shape, and a lifetime-qualified `&'a`
   names the lifetime instead of inferring it.

   A binding may also borrow with an explicit `&` on the initializer instead of an
   annotation. `val q = &p` borrows `p` immutably and infers `q: &{x: number}`, and
   `val q = &mut p` takes a mutable borrow, which requires `p` to be owned-mutable. This
   is equivalent to the annotated form, but it does not repeat the pointee shape, so it
   is the ergonomic way to force a borrow where inference would otherwise move. The
   explicit `&` expression is a binding-site convenience. Call arguments still borrow
   implicitly, `foo(p)` rather than `foo(&p)`, per the "Function argument" rule, because
   the parameter's own `&` already states the borrow. A binding has no parameter
   annotation to lean on, so the `&` on the initializer states the borrow there instead.

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

The principle behind these rules: when an annotation is present, its `&` or bare
form decides owned versus borrowed directly. Otherwise **owned** is inferred when a
binding is the sole producer or holder of a value, and **borrowed** is inferred when
a binding reaches a value that something else owns. For the inferred case the
lifetime machinery already in place computes which one applies, and move semantics
reads its verdict rather than running a separate analysis.

## Move / affine semantics

At every site where a value flows from a source expression into a destination,
the transfer has one of three outcomes.

1. **Copy.** The source is a value type, so the transfer never consumes it and
   the source binding stays fully usable. What happens at runtime depends on the
   kind of value. Primitives — `number`, `string`, and `boolean` — are
   duplicated by value, so each binding holds an independent copy. Functions and
   promises are reference objects that JavaScript cannot copy, so the transfer
   shares the same object. They count as value types anyway, because the system
   tracks no interior mutability for them. A function and a promise are immutable
   to their holders, and freely aliasing an immutable reference can never let
   another reference observe a mutation. "Copy" names the compile-time category,
   meaning never tracked and never moved. It does not assert that a runtime
   duplicate is made. A promise's resolved payload is governed separately.
   Awaiting the promise borrows that payload under the ordinary borrow rules.

   Immutable borrows are Copy as well. Copying one yields another immutable borrow of
   the same value, and the source stays live, which is why a `&T` can be freely
   duplicated. A mutable borrow is also non-consuming when bound, though as a
   `&mut`-to-`&mut` reborrow under outcome 2 rather than a copy. The common rule is that
   binding a borrow never consumes it. Only an owned value moves.

2. **Borrow.** The destination takes a reference whose lifetime is bounded within
   the source binding's region. The source keeps ownership and stays usable. The
   borrow is governed by the mutability-exclusivity rule for its lifetime. This
   is the outcome for a `&` parameter the callee does not let escape, a
   `&mut`-to-`&mut` local reborrow, sharing an immutable value, and any explicit
   `&` borrow that stays local.

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

- **`val` / `var` binding.** The outcome depends on `x`. A value type is copied. A
  borrow, `&T` or `&mut T`, is duplicated non-consumingly so the source stays live. An
  immutable borrow is copied and a mutable borrow reborrowed, and the multiple-`&mut`
  rule makes the mutable case sound. An owned value is moved into `y` and consumed,
  unless `y` is annotated `&` or `&mut`, which borrows `x` and keeps it usable. So
  binding a borrow never consumes it, and only owned values move.

  ```esc
  val p = Point(5, 10)
  val q = &p             // q: &Point — immutable borrow of p
  val r = q              // r: &Point — copies the borrow; q stays live
  ```

  ```esc
  val mut p = Point(0, 0)
  val q = &mut p         // q: &mut Point — mutable borrow of p
  val r = q              // r: &mut Point — reborrows; q stays live (multiple &mut allowed)
  ```

  Mutability lives in a different place for the two, which is why `val r = q` preserves
  a borrow's mutability while a plain `val` move of an owned value drops it. An owned
  value's mutability belongs to the binding and defaults to immutable, so moving into a
  plain `val` freezes it, and `val mut` keeps it mutable. A borrow's mutability belongs
  to its `&`/`&mut` type and is inherited on copy. It can be narrowed by annotation, as
  in `val r: &Point = q` downgrading a `&mut`, but never widened, since a view cannot
  grant itself access the owner withheld.
- **Reassignment.** `y = x` follows the same decision as a binding. A reassigned
  `var` that previously owned a value drops the old value and takes the new one.
- **Field / element store.** `obj.f = x` and `t[i] = x` move `x` when `obj` or
  `t` outlives `x`. Storing into a longer-lived object is the canonical escape.
- **`return`.** `return x` moves `x` out of the frame unless `x` is a value type
  or a borrow whose lifetime already outlives the frame.
- **Function argument.** Passing `x` to a `&` parameter borrows; the caller keeps
  `x`. Passing `x` to a bare owned parameter moves `x`; the caller gives it up. For
  an unannotated parameter the outcome is inferred — a move when the callee lets the
  value escape, a borrow otherwise. Both kinds of borrow are inserted implicitly at
  the call: an owned argument passed to a `&` or `&mut` parameter is borrowed
  without writing `&` or `&mut` — `foo(p)`, not `foo(&mut p)` — since owning a value
  subsumes lending it. A `&mut` parameter requires the argument to be owned-mutable.
  The compiler still inserts the borrow and checks it against the phase and
  exclusivity rules, so the elision is purely syntactic and never changes which
  calls are legal.
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

Place borrows share this path-granular tracking. `&obj.f` borrows the field `obj.f`
rather than the whole receiver, so it locks only that path and leaves a disjoint sibling
such as `obj.g` independently usable, including through `&mut obj.g`. Moves and borrows
read the same per-path ownership state, so they compose, and a field may be borrowed
while a disjoint field is moved. The same conservative fallback applies. A path the
checker cannot prove disjoint, such as a dynamic `arr[i]` versus `arr[j]`, falls back to
a container-level borrow, the way the whole-object-consume fallback covers field moves it
cannot separate.

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

**Thawing an immutable value.** The reverse transition is also a move. Moving an
owned-immutable value into a `val mut` binding consumes the source, so the new
binding is the sole owner and may safely mutate — no immutable reference to the
value survives.

```esc
val p = {x: 0}             // owned-immutable
val mut q = p              // move: p into mutable q; p consumed
q.x = 5                    // OK — q is the sole owner
print(p.x)                 // ERROR: use of `p` after it was moved into `q`
```

**Local immutable view.** A `&` borrow that stays local does not move; it borrows.
The invariant is preserved instead by mutability exclusivity: while the immutable
borrow is live, the mutable owner may not mutate.

```esc
val p: mut {x: number} = {x: 0}
val q: &{x: number} = p    // immutable borrow — q is a local view of p; p retained
p.x = 5                    // ERROR: p mutated while immutable borrow q is live
print(q.x)                 // q read here, so the borrow spans the mutation above
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
- **Multiple borrows of one kind are allowed.** Escalier is not Rust. Several
  mutable borrows of one value may be live at once, as may several immutable
  borrows — but never a mutable and an immutable borrow at the same time, per
  "Borrows of a mutable owned value." Move/affine semantics applies to ownership
  transfer, not to borrowing, so `&mut`-to-`&mut` aliasing stays legal and does not
  consume the source.

  ```esc
  val a: mut {x: number} = {x: 1}    // owned-mutable
  val b: &mut {x: number} = a         // mutable borrow of a — a retained
  b.x = 2
  print(a.x)                          // OK — prints 2
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

## Type display and annotations

Ownership is read from the annotation: a `&` marks a borrow and a bare type is
owned, and inference fills in the verdict only for an unannotated value. The `&`
marker is the part of the displayed type that records owned versus borrowed, and a
lifetime name records the borrow's extent when it is load-bearing. The governing
decision for how lifetimes appear:

> A type annotation should match the inferred type. The displayed signature is
> always a valid annotation, and writing it back produces the same type. The
> compiler fills in a type the program did not write only at an explicit hole,
> such as the `_` in `fn () -> _ throws _`.

Two consequences follow.

- **Load-bearing lifetime names are shown; connect-nothing names are elided, but
  the `&` is never hidden.** A borrow always prints its `&`, so owned (`{x: number}`)
  and borrowed (`&{x: number}`) are always distinguishable — the marker that earlier
  drafts could only infer is now in the surface type. What the display elides is the
  lifetime *name*. A lifetime that connects an input to an output or escapes to
  `'static` is rendered, so `fn <'a>(p: &'a mut {x: number}) -> &'a {x: number}`
  shows its `'a`; a lifetime that connects nothing is dropped, leaving the bare
  `&{x: number}`. Names appear exactly when they carry meaning, while the borrow
  marker itself is always present, so the display is never misleading.
- **Annotations are read literally, and a written type round-trips.** A bare
  annotation denotes an owned value and a `&` annotation a borrow (inference rule
  3), so the displayed type is itself a valid annotation: writing it back produces
  the same type, and no position silently reinterprets a bare annotation as a
  borrow. This removes the
  [#764](https://github.com/escalier-lang/escalier/pull/764) inconsistency, where a
  bare return annotation forced an owned slot and errored while the same code with
  the return type omitted inferred a lifetime. Now the omitted case infers a `&`
  borrow and the explicit borrow is written `&`, so the two agree.

This stance is a deliberate reversal of an earlier draft that elided lifetimes from
the canonical display and surfaced them only on hover. Matching annotations to
inferred types matters more now that much code is written or transformed by LLMs: a
tool can read the inferred signature and reproduce it verbatim as an annotation,
and an annotated program type-checks to the same result as the inferred one. The
remaining requirement on diagnostics:

- **Errors carry the ownership story.** When a borrow cannot escape, the message
  names the borrow's source and explains the ownership relationship — for example,
  "`q` borrows `p` and cannot outlive it; `p` is a local value" — and suggests a
  concrete fix, such as returning the source directly or cloning. A `showLifetimes`
  verbose mode may additionally render every lifetime, including elided ones, for
  diagnostic use.

The aim is that the inferred type a developer sees is exactly the type they could
write, ordinary local code needs no lifetime *names* because the elided lifetimes
connect nothing, and the cases that do require a named lifetime show it in the
signature rather than hiding it behind a later error. Borrows are always marked
with `&`, but their lifetimes stay inferred until they become load-bearing.

## Type-former interactions

Ownership and lifetime live in the `RefType{Mut, Lt, Inner}` wrapper, which sits
outside the shape. Type variables, aliases, unions, intersections, and tuples all
describe the `Inner` shape, and the wrapper is a mostly-orthogonal layer applied on
top. Borrowing therefore composes with the type formers: a borrow wraps whatever
shape a former produces. The places where the composition is not fully orthogonal
are described below.

### Type variables

A borrow applies outside a type parameter, never inside it. `&T`, `&mut T`, and
`&'a T` wrap a `T`, and the lifetime is solved by the existing `constrainLt`
machinery rather than baked into `T`. Because `&'a U` is itself an ordinary type, a
type parameter can be instantiated with a borrow, and the lifetime rides along for
free. Instantiating `fn id<T>(x: T) -> T` at a borrow yields `fn(&'a U) -> &'a U`
with no separate lifetime parameter. This is a deliberate divergence from Rust,
where lifetimes are a distinct kind that must be threaded explicitly.

Explicit lifetime variables, a separate sort from type variables, are needed only to
relate two borrows whose lifetimes elision cannot disambiguate — the
multiple-input-borrows case. Within a single solved instantiation a variable has one
ownership. Owned and borrowed are different types, so an inference variable used as
owned in one place and borrowed in another is a conflict, not a value that is both.

There is no `Copy` bound. Generic code treats a type parameter as non-duplicable —
the conservative affine assumption — because it cannot tell whether the argument is a
value type. A body may therefore use a `T` value at most once, and a function that
needs to reuse its argument takes it by `&T` and reads through the borrow. A function
that genuinely duplicates an owned value, such as `fn dup<T>(x: T) -> [T, T]`, is not
expressible generically; it must be written against a concrete value type or take a
borrow. The bound is omitted deliberately: duplicating a type-parameter value is rare
enough not to justify the constraint machinery, and borrowing covers the common case.

Writing the parameter as `&T` makes the function borrow-only, and that is what lets it
duplicate the argument. In `fn dup<T>(x: &T) -> [&T, &T]` the parameter is a borrow, so
`x` is a reference rather than an owned value. Copying a reference is not a move, so the
body `return [x, x]` is allowed. It yields two immutable borrows of one value, both
sharing the lent lifetime, which is sound because multiple immutable borrows may be live
at once. Because `&T` is part of the signature, the function never takes ownership. It
borrows for any `T`, whether the caller's value is owned or itself borrowed. This is the
difference from the bare-`T` form above: the borrow is stated in the type, so the body's
duplication is checked against a known-copyable reference rather than an unconstrained
`T`. At the call the borrow is inserted implicitly. With an owned `x` you write `dup(x)`,
not `dup(&x)`, since the parameter is declared `&` and owning a value subsumes lending
it.

### Type aliases

A shape alias is transparent. `type Point = {x: number, y: number}` names an owned
shape, and `&Point` expands to `&{x: number, y: number}`. An alias that itself names
a reference type is implicitly generic over the lifetime: each use of
`type PointRef = &Point` gets a fresh inferred lifetime, the way lifetime elision
works inside a Rust type alias. Write `&'a Point` directly to name the lifetime
instead.

Utility types extend this transparency to aliases that *compute* a shape rather than
name a fixed one. TypeScript's `Partial<T>`, `Pick<T, K>`, `Record<K, V>`, and
`ReturnType<T>` are generic aliases built on three foundations: conditional types,
mapped types, and template literal types. The rule for `&` follows the wrapper's outer
position and matches a plain alias. Strip the wrapper, evaluate the operator over the
pointee shape, then re-wrap the result. `&Partial<T>` borrows the object that
`Partial<T>` computes; it is not `Partial<&T>` with the borrow pushed inside the
operands. The lifetime is solved over the whole computed shape and is fresh per use,
exactly as a `&` alias is implicitly generic over its lifetime.

The three foundations differ in whether `&` applies at all. Template literal types
compute string-kind results, and strings are value types, so `&` never wraps them and
the borrow question does not arise. Conditional and mapped types can compute an object
or tuple shape, so the wrapper does apply. A conditional type wraps whichever branch
shape it selects, and a mapped type wraps the object it builds. The per-property `readonly`
modifier that a mapped type may toggle is a separate axis from the whole-value wrapper,
and the two compose without conflict. A `readonly` field stays read-only however the
object is held, even through an owned-mutable value or a `&mut` borrow. The whole-value
mutability decides whether the value may be written or reassigned at all, while
`readonly` independently forbids writes to that one field. A mapped type such as
`Readonly<T>` therefore works inside the shape, marking fields read-only, and leaves the
borrow wrapper untouched. No special reconciliation is needed.

### Unions and intersections

The wrapper is outer and shared. `&(A | B)` is one borrow over a union pointee, with
a single lifetime and mutability for the whole value, not `&A | &B` with independent
lifetimes. A union is owned-or-borrowed and mutable-or-not as a unit; there is no
per-member ownership. Move and borrow treat the union as a whole, so moving consumes
it regardless of which variant it currently holds. Intersections compose the same
way, with the wrapper outside `A & B`.

Narrowing introduces a new binding, so it falls under the ordinary borrow and phase
rules rather than a special narrowing rule. The narrowed binding is a fresh borrow of
the scrutinee, scoped to the narrowed region, and the original keeps its `A | B`
type. An immutable narrowed binding puts the value into the immutable phase for its
scope, and the phase rule already forbids any mutable borrow from changing the
variant while it is live. A concurrent mutation can therefore never silently re-type
a narrowed view.

Narrowing a mutable value may yield a mutable narrowed binding, `&mut A`, so the
variant's fields can be written. To keep that sound while several mutable borrows are
live at once, the discriminant is **pinned** for the binding's scope: while a mutable
narrowed `&mut A` is live, the tag the narrowing tested may not be written through any
alias, though the variant's other fields stay mutable. Pinning matches the mental
model — the narrowing already depends on the tag, so the tag is frozen for the arm —
and it removes the only way a concurrent write could invalidate the narrowed type.

```esc
match u {              // u: mut (A | B)
    a is A => {
        a.x = 5        // OK — a is &mut A, and x is an ordinary field of the A variant
        // a.tag = "b" is rejected — the discriminant is pinned for this arm
    }
}
```

`mut` and `&` sit identically on the wrapper but behave differently over a union,
because mutation is invariant where an immutable borrow is covariant. An immutable
borrow reads only, so `&(A | B)` factors by subtyping: `&A` is usable where
`&(A | B)` is wanted, and `&A | &B <: &(A | B)`. A mutable wrapper can also write, so
it is invariant: `mut A` and `mut (A | B)` are incomparable, and `mut (A | B)` does
not factor. The corruption is observable only through a borrow, where the callee
still aliases the caller's value: passing a `&mut A` where `&mut (A | B)` is expected
would let the callee write a `B` into the caller's `A`-typed storage:

```esc
type A = {tag: "a", x: number}
type B = {tag: "b", y: number}

fn f(p: &mut (A | B)) -> void {
    p = {tag: "b", y: 0}   // writes a value of type A | B; would corrupt storage typed mut A
}
```

A union or intersection must have uniform ownership. Because ownership is the outer
wrapper, a type whose members disagree — an owned member beside a borrowed one, such
as `{x: number} | &{y: number}` — has no single owned-or-borrowed verdict and is
rejected. The programmer makes ownership uniform first: clone the borrowed member to
own it, or borrow the owned member, so both sides agree before the union is formed.
Auto-factoring the owned member down to a borrow is deliberately not done: when that
member is a fresh temporary the resulting borrow lifetime is too short to be useful,
and the silent downgrade would hide the problem rather than surface it.

### Nested borrows

A nested borrow such as `&&Point` arises only from generic substitution — `&T` at
`T = &Point` — and every nesting normalizes to depth at most one, so Escalier never
represents a borrow of a borrow. The JS compile target forces this rather than a
policy choosing it.

- **Immutable layers collapse.** Immutable borrows are Copy. Here Copy duplicates the reference and never the
  referenced data, so a duplicate is just another immutable alias to the same value.
  Both layers compile to the same bare object reference, so `&'a &'b Point` reduces to `&'a Point` — the
  outer, shorter lifetime, with the standing constraint that `'b` outlives `'a`.
  Immutable `&` is idempotent.
- **Mutable nesting is uninhabitable.** `&mut &mut Point` would have to mean "repoint
  the inner borrow," which needs a storage cell holding the inner reference. A borrow
  compiles to the bare object reference with no cell of its own, and JS offers no
  lvalue-reference to a binding, so no such operation is expressible. Repointing a
  borrow stored in a field is written by mutably borrowing the *container* — an
  ordinary `&mut Holder` — never by forming `&mut &mut Point`. The only way to get a
  real repointable cell is to box it by hand as a `{ value: ... }` object, which is a
  normal owned value, not a borrow. So the mutable nested type has no inhabitant you
  can produce, and rejecting it costs nothing.

### Other formers

- **Tuples and arrays** are `RefInner`. `&[A, B]` borrows the whole tuple, and
  element access borrows a piece for a lifetime bounded by the container. Partial
  moves apply at element granularity, as in the partial-moves section.
- **Functions** are value types. They copy and are never wrapped in `RefType`. The
  lifetimes inside a function signature elide or show by the display rules.
- **Generic containers** such as `Array<T>` own their elements, so `arr[i]` yields a
  `&T` bounded by the array's lifetime, and `&mut Array<T>` is a mutable borrow of
  the container.
- **Conditional and mapped types** compute a shape without reference to ownership.
  The wrapper is applied to the computed result, so a conditional type that selects
  between branches yields a pointee shape that a borrow then wraps.

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
- **Diagnostics.** Exact wording and blame spans for use-after-move and
  move-on-escape errors are left to the implementation plan; they should name the
  move site, the later use, and why the transfer was a move.
