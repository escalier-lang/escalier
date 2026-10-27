# Ownership rules

## Status and scope

This document is a draft. It states the rules that decide which programs the
ownership, borrowing, lifetime, and mutability checks accept. The goal is a rule
set small enough that every check in the solver is an instance of one of these
rules. A check that maps to no rule is either a bug or a rule this document is
missing, and either way the document or the check changes.

It builds on three earlier documents and does not replace their rationale:

- [docs/08_mutability.md](../../docs/08_mutability.md) and
  [docs/09_ownership.md](../../docs/09_ownership.md) describe the language as a
  user sees it.
- [planning/affine_semantics/requirements.md](../affine_semantics/requirements.md)
  introduces moves and argues that one rule should govern every flow site.
- [planning/lifetimes/requirements.md](../lifetimes/requirements.md) introduces
  liveness-based mutability transitions.

Where this document and those disagree, this document is the proposal. The
section [Divergences from the docs](#divergences-from-the-docs) lists the
disagreements found so far.

The section [Decisions](#decisions) records behaviour the solver has today that
no document stated, and design questions the rules left open. Most are decided,
and the rest need a decision before the rules are final.

## The invariant

> No immutable reference ever observes a mutation. If a value is reachable
> through an immutable reference that is live at some point, then between the
> creation of that reference and its last use, the value is not mutated through
> any other path.

Every accepted program preserves the invariant. Among programs that preserve it,
the checks reject as few as they can. When the two goals conflict, the checks
reject.

The invariant has one exception, from
[D11](#d11-interior-mutability-for-state-no-reader-can-observe). A write-once
cell may be filled the first time it is read. After that its contents never
change.

## Terms

A *value type* is a primitive, a function, or a promise. Value types are copied
freely and none of the rules below apply to them.

A *reference-shaped* value is an object, tuple, array, or class instance. The
rules below govern reference-shaped values only.

A *place* is a binding or a field path rooted at a binding, such as `p`, `p.a`,
or `p.a.b`. Two places *overlap* when one is a prefix of the other. Places such
as `arr[i]` and `arr[j]`, whose disjointness the checker cannot prove, overlap
at the container `arr`.

A *region* is the span of program points during which a binding's storage
exists. A module-level binding's region is `'static`.

A *path* to a value is any place or borrow through which the value can be read
or written. A path is *immutable* when it permits reads only and *mutable* when
it also permits writes.

A path is *live* at a program point when some path through the control-flow
graph from that point reaches a later use of it.

A *flow site* is an operation that passes a value from a source expression to a
destination, such as a binding, an assignment, or a function argument. F1 lists
every kind of flow site.

## Type rules

These rules constrain types alone. They need no flow or liveness information.

**T1. Ownership is part of the type, and an owned value's mutability belongs to
its binding.** A bare type `T` is owned. A type written `&T`, `&mut T`, `&'a T`,
or `&'a mut T` is borrowed. This holds in every annotation position: bindings,
parameters, returns, and fields. An owned value is mutable or immutable
according to the binding that owns it, written `val mut p` or `val p`, and its
type is `T` either way. So `mut` appears in a type only inside a borrow, as
`&mut T`. A parameter declares a mutable binding the same way, as
`fn f(mut p: T)`. That signature has the same type as `fn f(p: T)`, since the
caller moves the argument in both. A rendered binding shows its mutability on
the name, as `mut p: {x: number}`. A `var` binding is reassignable, which is
independent of whether the value it owns is mutable.

**T2. Every binding and every borrow is immutable unless it is written `mut`.**
A value owned by `val p` and a borrow `&T` are both immutable. Only a `mut`
binding, such as `val mut p`, and a `&mut T` borrow permit writes. A fresh
value, such as a literal or a constructor call, takes its mutability from the
binding it lands in.

**T3. Mutability is deep through owned structure and stops at a borrow.** A
binding's mutability applies to every layer the value owns, including type
arguments. Every owned layer of a value in a `val mut` binding is writable, and
every owned layer of a value in a `val` binding is read-only. A `&` or `&mut`
inside the type carries its own mutability either way. With
`val a: Array<&mut Point>`, `a` cannot push but can still write through each
element. With `val mut b: Array<&Point>`, `b` can push but cannot write any
point. Since `mut` is not part of an owned type, `{a: mut {b: number}}` and
`Array<mut Point>` are rejected, and the owner decides each field's mutability.

**T4. A borrow's mutability narrows and never widens.** `&mut T` satisfies `&T`.
`&T` does not satisfy `&mut T`. Every write through a borrow, and every `&mut
self` call through one, requires a `&mut` borrow, and T4 is the rule that
decides each of them. T7 states the matching rule for an owned value. A function
holding `p: &mut {x: number}` can pass `p` to a parameter typed `&{x: number}`.
A function holding `p: &{x: number}` cannot pass it to a parameter typed `&mut
{x: number}`, and the call reports `cannot constrain immutable object <: mutable
object`.

**T5. `readonly` forbids reassigning a slot.** A write to a `readonly` field is
rejected after the field's one initialization. A `readonly` field does not
satisfy a writable field, because a holder of the writable type could reassign
it. A getter with no setter counts as a `readonly` field.

**T6. A borrow cannot fill an owned destination.** A value of type `&T` or
`&mut T` does not satisfy an owned `T`, since the destination would
claim ownership of storage owned elsewhere. The same holds for a
reference-shaped field read through a borrow. Reading it does not move it out,
so it cannot fill an owned destination, and F4 requires the borrow of it to be
written as `&r.f` or `&mut r.f`. With `r: &{x: number}`, the binding
`val q: {x: number} = r` reports `cannot use borrowed &object as owned object`,
and `val q: &{x: number} = r` is accepted.

**T7. An owned value is borrowed mutably only through a `mut` binding.** A
value in a `val mut` binding can be borrowed as `&` or `&mut`. A value in a
`val` binding can be borrowed only as `&`. The same holds for a call to a `&mut self` method, which
borrows its receiver mutably. With `val p = {x: 1}`, `&mut p` reports
`cannot constrain immutable object <: mutable object`. With
`val mut p = {x: 1}`, both `&p` and `&mut p` are accepted.

**T8. The branches of a conditional agree on ownership.** When the branches of
an `if`, a `match`, or another conditional produce the value of one
expression, they all produce owned values or all produce borrows. A branch with
an owned value borrows it to match, as `&p`.

```esc
fn pick(cond: boolean, q: &{x: number}) -> number {
    val p = {x: 1}
    val r = {x: 2}
    val a = if cond { p } else { q }     // error: mixed-ownership union: members disagree on ownership; make ownership uniform first
    val b = if cond { &r } else { q }    // accepted
    return b.x
}
```

**T9. A writable slot widens the literal types in its initializer.** A slot is
writable when it is a `var` binding, any owned layer of a value in a `mut`
binding, or a class field, since any instance of the class may sit in a `mut`
binding. A `readonly` field is never writable. When no type is written, the slot's inferred type replaces each
literal type in its initializer with that literal's primitive, since a later
write can store any value of the primitive. A slot that cannot be written keeps
the literal type, because it can never hold anything else. An annotation fixes
whichever type it names.

- `val n = 5` gives `n: 5`, and `var n = 5` gives `n: number`.
- `val p = {x: 1}` gives `p: {x: 1}`, and `val mut p = {x: 1, y: {z: "a"}}`
  gives `mut p: {x: number, y: {z: string}}`, since T3 makes every owned layer
  writable.
- `var p = {x: 1}` gives `p: {x: number}`. The object is immutable, but the
  binding can be reassigned to any `{x: number}`.
- A `readonly` field keeps its literal type, since T5 forbids writing it. A
  value behind a `&` or `&mut` keeps the type of the value it borrows, since
  the borrow carries its own mutability.

A `readonly` field meets T9 when a class declares a field with an initializer
and no type. In the class below, an instance in a `mut` binding can write
`retries` but
never `method`, so only `retries` widens. The static fields follow the same
rule.

```esc
class Request {
    readonly method = "GET",      // method: "GET"
    retries = 0,                  // retries: number
    readonly static version = 2,  // version: 2
    static count = 0,             // count: number
}
```

**T10. A getter takes `&self`, and a setter takes `&mut self`.** A getter only
reads its instance, and a setter writes one field of it. A getter or a setter
declared with any other receiver is rejected.

## Flow rules

An operation such as a binding, an assignment, or a function call passes a value
from a source to a destination. F1 lists these operations and the destination
type each one has. F2 says what happens to the value, which depends only on the
kind of value and the kind of destination, not on the operation. F3 and F4
settle the two cases the first two leave open: a destination with no written
type, and how a borrow is written.

**F1. Each operation that passes a value gives its destination a type.** The
type is the one written at the destination. When nothing is written, F3 decides
it. These operations are the flow sites:

| Operation | Example | Destination type |
|---|---|---|
| `val` or `var` binding | `val q = p` | The annotation if written. Otherwise owned, or a borrow when the initializer is `&p` or `&mut p` |
| Reassignment | `q = p` | The binding's type |
| Field store | `o.f = p` | The field's declared type |
| Element store | `a[i] = p` | The element type |
| Literal element or spread | `{f: p}`, `[p]`, `{...p}` | Owned |
| Destructuring or `match` arm | `val {f} = p` | Per part, as for a binding. A pattern over a borrow binds borrows, by F4 |
| Argument | `f(p)` | The parameter's type. An unannotated parameter is decided by F3 |
| Receiver | `p.m()` | The method's `self` type |
| `return` | `return p` | Owned, unless the return type is a borrow |
| Module-level store | `cache = p` | Owned, with a `'static` region |
| Closure capture | `fn () { return p.x }` | Decided by where the closure flows, by F3 |

**F2. What happens to a value depends on its kind and the destination's kind.**
There are three outcomes:

- A *copy* gives the destination the same value as the source, and the source
  stays usable. Copying a value type copies the value. Copying a borrow gives
  the destination a second borrow of the same value, and the value itself is
  not copied.
- A *borrow* gives the destination a new borrow of the source's place. The
  source keeps ownership.
- A *move* gives the destination ownership. The source place is consumed, and
  E1 rejects any later use of it. The destination binding decides the value's
  mutability after the move, which is how a move to immutable and a move to
  mutable work.

The table gives the outcome for each kind of source and destination. The
operation that passes the value does not change it.

| Source | Owned destination | `&` destination | `&mut` destination |
|---|---|---|---|
| Value type, such as `number` | Copy | Not applicable | Not applicable |
| Owned, in a `val` binding | Move | Borrow, written `&p` | Rejected by T7 |
| Owned, in a `val mut` binding | Move | Borrow, written `&p` | Borrow, written `&mut p` |
| Immutable borrow, `&T` | Rejected by T6 | Copy | Rejected by T4 |
| Mutable borrow, `&mut T` | Rejected by T6 | Copy, narrowed to `&` by T4 | Copy |

A value type has no borrow form, so its destination is always bare. Each binding
in the function below shows one outcome, and the read of `q.x` shows the error a
use after a move reports.

```esc
fn go(r: &{x: number}) -> number {
    val n = 1
    val a = n                  // copy, since n is a value type
    val b = r                  // copy, since r is a & borrow
    val p = {x: 1}
    val c = &p                 // borrow, so p stays usable
    val q = {x: 2}
    val d = q                  // move, so q is consumed
    val e = q.x                // error: use of moved value 'q'
    return a + b.x + c.x + p.x + d.x
}
```

**F3. A destination with no written type is owned exactly when the value
escapes.** A value escapes when the destination outlives the source's region.
Each case below follows from that:

- A `return` outlives the frame, so it is an owned destination.
- A store into module-level state outlives every local, so it is an owned
  destination. [D9](#d9-whether-an-escaping-value-moves-or-borrows) asks whether
  it should be a `'static` borrow instead.
- An unannotated parameter is owned when the body lets the parameter escape, and
  a borrow otherwise.
- A closure that is returned owns what it captures. A closure that stays in the
  body or is stored into a caller-owned object borrows what it captures.

A `val` or `var` binding of an owned value is always an owned destination, even
when the binding's scope is shorter than the source's. A binding whose
initializer is a `&` or `&mut` expression is a borrow destination.

A return with no written type is owned when it returns a local, and carries the
parameter's borrow when it returns one:

```esc
fn make() {
    val p = {x: 1}
    return p    // moves p out, so make returns an owned value
}

fn same(p: &{x: number}) {
    return p    // same: fn <'a>(p: &'a {x: number}) -> &'a {x: number}
}
```

An unannotated parameter takes its ownership from the body. `store` lets `p`
escape into module-level state, so its parameter is owned and the call moves
`a`. `read` only reads `p`, so its parameter is a borrow and the call borrows
`b`:

```esc
var sink = {x: 0}

fn store(p) { sink = p }     // store: fn (p: {x: number}) -> undefined
fn read(p) { return p.x }    // read: fn (p: &{x: number}) -> number

fn go() -> number {
    val a = {x: 1}
    store(a)                 // moves a
    val b = {x: 2}
    val n = read(&b)         // borrows b, so b stays usable
    return b.x + n
}
```

A closure that stays in the body borrows its captures, and one that is returned
owns them:

```esc
fn local() -> number {
    val p = {x: 1}
    val f = fn () { return p.x }    // borrows p
    val a = f()
    return a + p.x                  // p is still usable
}

fn returned() {
    val p = {x: 1}
    return fn () { return p.x }     // owns p, since it outlives the frame
}
```

A binding moves an owned value even when the binding dies first:

```esc
fn go() -> number {
    val p = {x: 0}
    if true {
        val q = p       // moves p
    }
    return p.x          // error: use of moved value 'p'
}
```

**F4. Every borrow is written.** A borrow is taken only through `&e` or
`&mut e`. Here `e` is an owned place such as `p`, or a reference-shaped field
read through a borrow, such as `line.start` with `line: &Line`. This holds at
every flow site, including bindings, arguments, field and element stores,
returns, and literal elements. A site that borrows without the `&` is an error,
so every borrow is visible where it is taken, along with its mutability.
`val c: &{x: number} = p` is rejected, and `val c: &{x: number} = &p` and
`val c = &p` are accepted.

Two places borrow without a written `&`:

- A method receiver, since the method's own `self` declares the borrow.
  `line.start.norm()` borrows `line.start` for the call.
- A pattern over a borrow. With `line: &Line`, `val {start} = line` binds
  `start: &Point`, since the pattern already says it reads from `line`.

A read that ends at a value type copies and needs no `&`, as in `line.width`
or `line.start.x`.

```esc
class Point { x: number, y: number }
class Line { start: Point, width: number }

fn first(line: &Line) -> &Point {
    val w = line.width      // copies, since number is a value type
    val x = line.start.x    // copies, since the read ends at a value type
    val {start} = line      // start: &Point, since the pattern reads through line
    val s = line.start      // error: line.start is read through a borrow, so write &line.start
    return &line.start
}
```

## Lifetime rules

**L1. A borrow's lifetime is how long its path stays live.** When a borrow of a
place flows into a destination, the borrow's lifetime is extended to cover the
destination. A borrow may outlive the binding that owns its place. The garbage
collector keeps the value alive while any path reaches it, so outliving the owner
is never an error on its own. The lifetime matters only to E2, which reads it to
decide how long the path is live. A borrow that flows into module-level state
gets the lifetime `'static`, so its path stays live for the rest of the program.

In `make` below, the returned borrow outlives `p`, which is accepted. In
`remember`, the borrow stored in `cache` is `'static`, so `cache` can read `p`
at any later point and the write to `p` is rejected by E2.

```esc
fn make() -> &{x: number} {
    val p = {x: 1}
    return &p          // accepted, since the value stays alive for the borrow
}

var cache: &{x: number} = &{x: 0}

fn remember() {
    val mut p = {x: 1}
    cache = &p         // the borrow's lifetime is 'static
    p.x = 2            // rejected by E2, since cache can still read p
}
```

**L2. A signature states exactly the lifetime relations its body needs.** A
body-carrying function must declare every outlives relation between its
parameters and return that its body establishes. Each declared bound `'a: 'b`
must also be established by the body. A bodyless `declare fn` is taken at its
word. See [D4](#d4-declared-lifetime-bounds-are-obligations).

`pick` declares that its result lives as long as `p`, and its body returns `p`.
`wrong` makes the same declaration but returns `q`, so its body needs a relation
the signature does not state. `either` can return `q` as an `'a` result only
because `q` outlives `'a`, so its body establishes `'b: 'a` and the declared
bound is accepted. `unproven` declares the same bound, but its body never
relates `q` to anything, so it does not establish the bound.

```esc
fn pick<'a>(p: &'a {x: number}, q: &{x: number}) -> &'a {x: number} {
    return p    // accepted
}

fn wrong<'a>(p: &'a {x: number}, q: &{x: number}) -> &'a {x: number} {
    return q    // rejected, since the result's tie to q is not declared
}

fn either<'a, 'b: 'a>(p: &'a {x: number}, q: &'b {x: number}, first: boolean) -> &'a {x: number} {
    if first {
        return p
    }
    return q    // accepted, since returning q establishes 'b: 'a
}

fn unproven<'a, 'b: 'a>(p: &'a {x: number}, q: &'b {x: number}) -> &'a {x: number} {
    return p    // rejected, since the body never establishes 'b: 'a
}

declare fn trusted<'a, 'b: 'a>(p: &'a {x: number}, q: &'b {x: number}) -> &'a {x: number}
```

`trusted` has no body, so its bound is taken as written.

**L3. Lifetimes appear in a rendered type only when they connect something.** A
lifetime that relates an input to an output, or that is `'static`, is rendered.
Any other lifetime is elided. The rendered signature is always a valid
annotation that produces the same type when written back.

Each comment below gives the rendered signature:

```esc
var sink = {x: 0}

fn read(p: &{x: number}) -> number { return p.x }
// fn (p: &{x: number}) -> number, since the lifetime connects nothing

fn same(p: &{x: number}) { return p }
// fn <'a>(p: &'a {x: number}) -> &'a {x: number}, since it ties p to the result

fn cache(p: &mut {x: number}) { sink = p }
// fn (p: &'static mut {x: number}) -> undefined, since p escapes to 'static
```

## Exclusivity rules

These rules run over the control-flow graph. They use the copy, borrow, and move
events from F2 and the liveness of each path.

The table below combines E1 through E3, with two rows for closures from E4. Each
row is what is already live on a place `p` that a `val mut` binding owns, and
each column is the next operation on `p`. A borrow is live while it is used
again later. After its last use, the row drops back to "Nothing", which is how
an immutable phase can follow a mutable one. A `'static` borrow follows the row
for its kind, but it is live at every later point, so its row never drops back.
When `p` is a `val` binding instead, T2 and T7 also reject "Write `p`" and
"`&mut p`" in every row.

| Already live on `p` | Read `p` | Write `p` | `&p` | `&mut p` | Move `p` |
|---|---|---|---|---|---|
| Nothing | Allowed | Allowed | Allowed | Allowed | Allowed |
| An immutable borrow, `val a = &p` | Allowed | Rejected by E2 | Allowed | Rejected by E2 | Rejected by E3 |
| A mutable borrow, `val b = &mut p` | Allowed | Allowed | Rejected by E2 | Allowed | Rejected by E3 |
| A closure that reads `p`, between calls | Allowed | Allowed | Allowed | Allowed | Rejected by E3 |
| A closure that writes `p`, between calls | Allowed | Allowed | Allowed | Allowed | Rejected by E3 |
| `p` was moved | Rejected by E1 | Rejected by E1 | Rejected by E1 | Rejected by E1 | Rejected by E1 |

**E1. A moved place is not used until it is reassigned.** After a move of place
`p`, no path through the control-flow graph uses `p` or a place overlapping `p`
until `p` is reassigned. A use is a read, a write, a method call, a borrow, or a
second move. A move on one branch consumes `p` only on the paths through that
branch. A place whose region is `'static`, such as a module-level binding or an
import, is never consumed, so a move out of it is rejected. Every later call
would otherwise find it gone.

**E2. An immutable path and a mutable path to one place are never live
together.** At no program point are an immutable path and a mutable path to
overlapping places both live. The owner's own writes count as a mutable path.
Several immutable paths may be live at once, and so may several mutable paths. A
path with a `'static` lifetime is live at every later point, so a value with a
`'static` immutable path is never written again, and a value with a `'static`
mutable path never gets a new immutable path.

The two functions below differ only in statement order. In the first, `q` is
read after the write to `p`, so the two paths overlap and the write reports
`cannot assign to 'p.x' while it is borrowed as immutable`. In the second, the
write is `p`'s last mutable use, so nothing overlaps.

```esc
fn overlapping() -> number {
    val mut p = {x: 0}
    val q: &{x: number} = &p
    p.x = 1    // error: cannot assign to 'p.x' while it is borrowed as immutable
    return q.x
}

fn sequential() -> number {
    val mut p = {x: 0}
    p.x = 1
    val q: &{x: number} = &p
    return q.x
}
```

**E3. A place with a live borrow cannot be moved.** Moving `p` while a borrow of
a place overlapping `p` is live is rejected. The move makes the destination an
owner, and an owner can always make its value mutable by moving it into a
`val mut` binding. The live borrow would then observe that owner's writes, even
when every path is immutable at the moment of the move. Below, `go` never
writes `p`, but `take` moves its argument to mutable and writes it while `c` is
still live, so the call reports `cannot move 'p' while it is borrowed`.

```esc
fn take(p: {x: number}) -> number {
    val mut q = p
    q.x = 5
    return q.x
}

fn go() -> number {
    val p = {x: 1}
    val c = &p
    val n = take(p)    // error: cannot move 'p' while it is borrowed
    return c.x + n
}
```

**E4. A closure call is a use of each borrow the closure holds.** A call to a
closure, or a call that may run a closure, uses each borrow the closure holds. A
capture the closure writes is used as a mutable path, and a capture it only reads
is used as an immutable path. A closure stored into module-level state may run
during any later call, so every later call uses its borrows. A closure that moves
one of its captures moves it at each call, so by E1 a second call is rejected.

E4 adds no column to the exclusivity table, because it does not decide whether
an operation on `p` is allowed. It decides when a closure's captures count as
used, and so which row applies. Between calls, the two closure rows apply. They
allow writes and new borrows of `p`, and they reject a move, since the closure
holds its borrows until its last call. During a call, the borrow row for the
capture's kind applies for that statement.

In `local` below, the write to `p` between the two calls is allowed. In
`moved`, `get` still holds its borrow of `p` for its later call, so the move
is rejected. In `stored`, the closure in `sink` may run during any later call,
so `readIt(&p)` conflicts with the write it may perform.

```esc
var sink: fn () -> undefined = fn () {}
declare fn readIt(a: &{x: number}) -> undefined

fn local() -> number {
    val mut p = {x: 1}
    val get = fn () { return p.x }
    val a = get()
    p.x = 2       // allowed, since get holds no live borrow between calls
    return a + get()
}

fn moved() -> number {
    val p = {x: 1}
    val get = fn () { return p.x }
    val q = p     // error: cannot move 'p' while it is borrowed
    return get()
}

fn stored() {
    val mut p = {x: 1}
    sink = fn () { p.x = 2 }
    readIt(&p)    // error: this call may run 'sink', which writes 'p' while it is borrowed as immutable
}
```

## Derived behaviour

The rules above produce each of the following. None of them is a separate rule.

- **Move to immutable and move to mutable.** `val settled = g` moves `g` to
  immutable by F2. E3 rejects the move while a mutable path into `g` is live.
  `val mut editable = settled` is a move to mutable, the same move in the other
  direction.
- **Partial moves.** E1 and E3 work on places, so moving `pair.a` leaves
  `pair.b` usable.
- **Moves into literals and spreads.** A literal element fills an owned slot of
  the new value, so F2 moves an owned source. A spread fills one slot for each
  field of its source, so it moves each reference-shaped field and copies each
  value-type field.

  ```esc
  fn go() -> number {
      val p = {x: 1}
      val box = {inner: p}             // moves p into box
      val q = {n: 1, inner: {y: 2}}
      val copy = {...q}                // copies q.n and moves q.inner
      val a = q.n                      // allowed, since q.n was copied
      val b = q.inner.y                // error: use of moved value 'q.inner'
      return p.x                       // error: use of moved value 'p'
  }
  ```
- **Consuming receivers.** A `self` or `mut self` receiver is an owned
  destination, so F2 moves the instance. T6 rejects the call through a borrow.

  ```esc
  class Builder {
      parts: Array<string>,
      finish(self) -> string { return self.parts.join("") },
  }

  fn go(shared: &Builder) -> string {
      val b = Builder([])
      val s = b.finish()     // moves b into the call
      val t = b.finish()     // error: use of moved value 'b'
      return shared.finish() // error: 'finish' takes its receiver by value, so it moves the instance and cannot be reached through a borrow.
  }
  ```
- **Mutability transitions.** `val q = &p` borrows `p` by F2 and F4. A later
  write to `p` while `q` is live is rejected by E2.

## Comparison with Rust

Escalier's model starts from Rust's. The table compares each rule with the Rust
behaviour closest to it. A rule marked "same" accepts and rejects the programs
Rust would, once both are written in the same shape. A rule marked "differs"
changes that set, and the note says in which direction. "No equivalent" means
Rust has no matching feature.

| Rule | Rust behaviour | Verdict | Note |
|---|---|---|---|
| T1 ownership in the type, mutability on the binding | `T` is owned and `&T` or `&mut T` borrows. An owned value's mutability is on the binding, as `let mut x` | Same | |
| T2 immutable by default | Bindings and references are immutable unless written `mut` | Same | |
| T3 deep mutability, stopped by a borrow | Inherited mutability: an owner's mutability reaches every field it owns, and a reference field carries its own mutability | Differs | Same inheritance, but Rust adds interior mutability through `Cell` and `RefCell`. Escalier has no escape hatch, which is what makes a move to immutable deep. See D11 |
| T4 narrow, never widen | `&mut T` coerces to `&T`, never the reverse | Same | |
| T5 `readonly` | No `readonly` fields. Rust restricts writes through field privacy instead | No equivalent | Inherited from TypeScript |
| T6 borrow into owned | `&T` does not satisfy `T`. Moving a field out of a borrow is rejected with E0507 | Same | Both ask for `&r.f`. Rust also copies a field whose type is `Copy`, where Escalier copies only value types |
| T7 mutable borrows need a `mut` binding | `&mut x` requires `let mut x` | Same | |
| T9 writable slots widen literals | No literal types. An integer literal's type is inferred from how it is used | No equivalent | Inherited from TypeScript, which widens a `let` binding's literal type the same way |
| F2 copy, borrow, or move | `Copy` types copy, `&T` copies, and an owned value moves into an owned destination | Differs | A `&mut` source copies, because Escalier allows aliased `&mut` |
| F3 owned when it escapes | Every parameter type is written. A closure captures by reference unless it is marked `move` | Differs | Escalier infers an unannotated parameter's ownership and a returned closure's moves. Rust requires both to be written |
| F3 binding always moves | `let q = p` moves `p` whatever `q`'s scope | Same | |
| F4 borrows are written | `&` and `&mut` are written to borrow, except on a method receiver, which auto-borrows. Pattern binding modes bind borrows when a pattern destructures a borrow | Same | |
| L1 lifetime is liveness | A borrow cannot outlive its referent, and storing one in a `static` requires `'static` | Differs | A borrow may outlive its owner, because the garbage collector keeps the value alive. Lifetimes only bound how long a path is live for E2 |
| L2 signature states the body's relations | Lifetimes are inferred only inside a body, never in a signature. A declared bound is an assumption the body may use | Differs | Escalier infers signature lifetimes for an unannotated function and treats a declared bound as an obligation. See D4 |
| L3 render only connecting lifetimes | Elision follows fixed syntactic rules, such as one input lifetime flowing to every output | Differs | Escalier elides by whether a lifetime connects anything, which also covers signatures Rust's elision rules reject |
| E1 no use after move | Flow-sensitive per branch, with partial moves, and reassignment re-initializes | Same | Reassignment does not yet re-initialize in Escalier. See [#1269](https://github.com/escalier-lang/escalier/issues/1269) |
| E2 no immutable and mutable path live together | Aliasing XOR mutability over non-lexical lifetimes: one `&mut`, or any number of `&` | Differs | Escalier allows several live mutable paths at once. The target is single-threaded and garbage collected, so the only hazard left is an immutable path observing a write |
| E3 no move while borrowed | Rejected with E0505 | Same | |
| E4 closure calls use its borrows | A closure holds its captured borrows from creation to its last use | Differs | Escalier counts the borrows only at each call, so a write to a captured value between two calls is accepted. Rust rejects it. A closure stored into module-level state needs `move` and `'static` captures in Rust |
| D1 no move out of module-level state | Moving out of a `static` is rejected with E0507 | Same | |
| D2 a moving closure is called once | `FnOnce` | Same | |
| T8 branches agree on ownership | `if` branches must have one type, so `T` and `&T` do not join | Same | |
| D4 declared bounds are obligations | Declared bounds are assumptions | Differs | Follows from L3 |
| D5 `readonly` initialized once | A struct literal initializes every field at once | No equivalent | |
| T10 getter and setter receivers | No getters or setters | No equivalent | |
| Move to immutable and move to mutable | `let settled = v; let mut editable = settled;` moves between bindings of different mutability | Same | `docs/09_ownership.md` says Rust has no equivalent. The moves are the same, but interior mutability keeps Rust's move to immutable from being deep |

The differences come from four sources:

- **The runtime.** Single-threaded JavaScript with a garbage collector removes
  data races and dangling pointers. That permits aliased `&mut` in F2 and E2,
  borrows that outlive their owner in L1, and the per-call closure borrows in E4.
- **A deep immutability guarantee.** Escalier's invariant promises that an
  immutable value never changes. Rust's interior mutability would break that
  promise, so T3 has no escape hatch.
- **Inference over annotation.** F3, L2, and L3 infer what Rust requires the
  user to write: parameter ownership, closure moves, and signature lifetimes.
- **TypeScript heritage.** T5, D5, and T10 cover `readonly` fields, getters, and
  setters, and T9 covers literal types. Rust has none of them.

## Checking model

The rules suggest one checker, which is the target of the simplification. It
does not replace the AST or add a second representation of the program. It
keeps the control-flow graph that the [liveness](../../internal/liveness/)
package already builds, and attaches a short list of ownership events to each
node:

| Event | Produced by | Example |
|---|---|---|
| `copy(place, holder)` | A flow site whose outcome is a copy, by F2 | `val b = r` |
| `borrow(kind, place, holder, lifetime)` | A written `&` or `&mut`, a method receiver, or a pattern over a borrow, by F4 | `val c = &p` |
| `move(place, holder)` | A flow site whose outcome is a move, by F2 | `val d = q` |
| `use(place, read or write)` | Each read, write, and call. A call that may run a closure also uses each borrow the closure holds, by E4 | `p.x = 1` |

The `holder` is the binding or place that receives the value. A borrow stays
live while its holder, or any copy of it, is used again later.

The checker runs in three steps:

1. The type checker emits the events while it infers. Only it knows each
   destination's type, and F2 needs that type to choose between copy, borrow,
   and move. Each borrow carries the lifetime L1 gives it.
2. Once inference finishes, the lifetime solver settles F3's escape questions
   from the outlives constraints.
3. One forward dataflow pass over the control-flow graph checks E1, E2, and E3
   against the events and the liveness of each holder.

The type rules stay in the type checker, where they already sit.

The function below shows the events each statement produces, as comments:

```esc
fn go() -> number {
    val mut p = {x: 0}
    val c = &p          // borrow(&, p, c, 'c)
    val d = c           // copy(c, d)
    p.x = 1             // use(p.x, write)
    return d.x          // use(d.x, read)
}
```

The pass reaches `p.x = 1` holding an immutable borrow of `p`. Its first holder,
`c`, is never used again, but the copy made `d` a second holder, and `d` is
read on the next line. So the borrow is live at the write, and E2 reports the
write. Without the copy event, the pass would see the borrow end with `c` and
accept a write that `d` can observe.

The design is close to Polonius, the borrow checker the Rust project is
developing to replace its current one. Polonius reduces Rust's MIR, a fully
lowered form of the program, to flat relations, such as where each loan is
issued, where each variable is used, and where each loan ends. It then solves
over those relations. The events above are the same kind of flat facts per
control-flow point. Escalier produces them directly from inference, with no MIR
in between.

## Decisions

D1 through D8 record behaviour the solver enforces today that no document
stated. D9 through D11 are design questions the rules left open. Each decided
entry says where its outcome now lives. D9 and D10 are still open. The current
behaviour comes from reading the code, and the file references point at where
it lives.

### D1. Moving out of a module-level binding

`ModuleLevelMoveError` rejects moving a module-level binding or an import
([module_level_moves.go](../../internal/solver/module_level_moves.go)). The rule
is sound. A module-level binding outlives every call, so a move would consume it
for every later caller.

Recommendation: keep the behaviour and state it as part of E1. A place whose
region is `'static` is never consumed, so a move out of it is rejected.

Decided: accepted. E1 states it.

### D2. Closures that move a capture can be called once

`consumeOnceCallee` consumes a closure that moves one of its captures when the
closure is called
([module_level_moves.go](../../internal/solver/module_level_moves.go)).

Recommendation: keep it and derive it. The call moves the capture, so by E1 a
second call would use a moved place. No separate rule is needed once E4 treats a
moved capture as a move event.

Decided: accepted. E4 states it.

### D3. Branches that mix owned and borrowed values

`MixedOwnershipError` rejects a conditional whose branches produce an owned value
on one side and a borrow on the other
([infer_expr.go](../../internal/solver/infer_expr.go)).

The options are:

- Keep the error. It is simple and the user can add `&` to the owned branch.
- Join the branches to a borrow. The owned branch would then need an owner that
  outlives the expression, and no such owner exists for a fresh value.

Recommendation: keep the error and add it as a type rule, T8.

Decided: accepted. T8 states it. A branch with an owned value can borrow it to
match the others.

### D4. Declared lifetime bounds are obligations

`checkDeclaredLifetimeBounds` requires the body to establish each declared bound
`'a: 'b`
([infer_expr.go](../../internal/solver/infer_expr.go)). Rust works the other
way. There a declared bound is an assumption the body may rely on and the caller
must prove.

Escalier's choice follows from L3. A rendered signature has to match the
inferred one, so a declared bound the body does not establish would render a
signature the inference does not produce.

Recommendation: keep the behaviour, as L2 states it, and document it in
`docs/09_ownership.md`.

Decided: accepted. L2 states it, and the docs change in the migration.

### D5. A `readonly` field is initialized once in a constructor

A second write to a `readonly` field inside a constructor is rejected, using the
definite-initialization lattice
([infer_class_ctor.go](../../internal/solver/infer_class_ctor.go)).

Recommendation: keep it. T5 already says one initialization, so this needs no
new rule, only the docs.

Decided: accepted. T5 states it.

### D6. Getter and setter receivers

A getter must take `&self` and a setter must take `&mut self`
(`GetterReceiverError` and `SetterReceiverError`).

Recommendation: keep both as declaration well-formedness rules alongside the type
rules. They do not interact with flow.

Decided: accepted. T10 states it.

### D7. Explicit `&` at a call site

`ImplicitBorrowArgError` requires an owned argument passed to a borrow parameter
to be written `&p` or `&mut p`
([borrow_exclusivity.go](../../internal/solver/borrow_exclusivity.go)). The docs
say the compiler still inserts the borrow. Other flow sites still borrow without
the `&`. The compiler accepts `val c: &{x: number} = p`, and a store into a
`&` field without the `&` moves the source, as B1 describes.

Recommendation: adopt F4, which extends the call-site requirement to every flow
site, and update the docs to match.

Decided: accepted. F4 states it.

### D8. A global write moves a borrow

`consumeAtGlobalWrite` records a move for any reference-shaped source, including
a borrow ([moves.go](../../internal/solver/moves.go)). This contradicts F2, under
which a borrow is copied and never consumed.

Recommendation: drop the special case. A borrow stored into module-level state
is copied by F2 and gets a `'static` lifetime by L1. E2 then keeps any
conflicting path from forming for the rest of the program.

Decided: accepted. The migration drops the move that
`consumeAtGlobalWrite` records for a borrow.

### D9. Whether an escaping value moves or borrows

F3 makes a destination with no written type owned when the value escapes, so an
escape always moves the source. L1 allows a second answer. The destination could
be a `'static` borrow instead, and the source would stay usable under E2.

Take a library that memoizes its argument into module-level state:

```js
const seen = new Map();
export function remember(value) { seen.set("last", value); }
```

The converter can declare the parameter three ways:

| Declaration | Caller after `remember(...)` |
|---|---|
| `value: {retries: number}` | `opts` is moved, so any later use is an error |
| `value: &'static {retries: number}` | `opts` can still be read, but never written again |
| `value: &'static mut {retries: number}` | `opts` can still be written, but never borrowed as `&` again |

The owned form works for every caller that does not use the value afterward. The
`'static &` form fits a caller that keeps reading the value, which is the common
case for a memoization key or a configuration object. A function whose body
writes the stored value needs one of the other two forms.

Recommendation: infer a `'static &` borrow for an escaping parameter that the
body never writes, and an owned parameter otherwise. A written annotation
overrides the inference either way.

### D10. Recording that a callback is not retained

A function value is a value type, so it carries no lifetime, and nothing in the
rules distinguishes a callee that only calls a callback from one that stores it.
The source-analysis plan for third-party packages expects to record exactly that
fact. [#1267](https://github.com/escalier-lang/escalier/issues/1267) tracks the
same gap for Escalier's own functions.

The options are:

- Give function types an optional lifetime, as in `&'a fn () -> undefined`.
  The lifetime bounds how long the callee may keep the function, and E4 reads it
  to decide how long the captures stay borrowed.
- Add a marker on the parameter that says the callee does not keep it, and treat
  every unmarked function parameter as retained.

Recommendation: the lifetime form. It reuses L1 and E4 instead of adding a rule.

### D11. Interior mutability for state no reader can observe

T3 has no escape hatch, so a value in a `val` binding or behind a `&` never
changes. Rust's `Cell` and `RefCell` exist mostly because `&mut` is unique
there, and Escalier does not need them for that. Several mutable paths may be
live at once, the garbage collector keeps shared graphs alive, and a `&mut`
field puts one mutable part inside an immutable value, as T3 allows.

Three cases still push against the rule:

- A cache or a lazy field. A `&self` method that computes an expensive result
  once and keeps it, such as a memoized hash, must be `&mut self` today, and
  that requirement spreads to every caller.
- Bookkeeping that is not part of the value, such as a counter, an id
  generator, or debug statistics on an otherwise immutable object.
- JavaScript methods that change internal state while reading. Examples are
  `RegExp.prototype.exec`, which updates `lastIndex`, and an iterator's
  `next`. Each must be declared `&mut self`, which makes code that only reads
  through them noisy.

Every option except the last narrows the invariant to exclude the cell's
contents. From the smallest change to the largest:

- A write-once cell, like Rust's `OnceCell`. A reader can see a value appear
  the first time it is read, but never see it change.
- A `Cell` that holds only value types and returns copies, so no path into its
  contents exists.
- A general `RefCell`, which holds any value and checks its borrows at runtime.
- No cell, which keeps the invariant as written.

The cache case looks like this today. `wordCount` writes its cache, so it needs
`&mut self`, and a caller holding only `&Document` cannot call it:

```esc
class Document {
    text: string,
    count: number | undefined,
    wordCount(&mut self) -> number {
        if val n: number = self.count {
            return n
        }
        val n = self.text.split(" ").length
        self.count = n
        return n
    },
}

fn summary(doc: &Document) -> number {
    return doc.wordCount()    // error: wordCount needs a &mut receiver, by T4
}
```

With a write-once cell, written here as a hypothetical `OnceCell<T>`, the cache
stays inside the cell and `wordCount` only reads:

```esc
class Document {
    text: string,
    count: OnceCell<number>,
    wordCount(&self) -> number {
        return self.count.getOrInit(fn () { return self.text.split(" ").length })
    },
}

fn summary(doc: &Document) -> number {
    return doc.wordCount()    // allowed, since wordCount takes &self
}
```

Recommendation: start with the write-once cell, since it covers caches and lazy
fields with the smallest exception. Keep counters and the interop methods on
`&mut self` until real code shows how often they come up.

Decided: accepted. Escalier adds a write-once cell, and the invariant states
the exception it makes. The cell's name, syntax, and API are still to be
designed. Counters and the interop methods stay on `&mut self` for now.

## Known bugs

**B1. A store into a `&` field without `&` moves the source.** The field and
element store paths in `inferFieldAssign` call `consumeOwned` even when the
destination is declared as a borrow
([infer_expr.go](../../internal/solver/infer_expr.go)). The program below
reports `use of moved value 'p'` on its last line. Under F4 it should instead
reject `h.slot = p` for the missing `&`. Written as `h.slot = &p`, it checks
clean today.

```esc
export fn go() -> number {
    val p = {x: 1}
    val q = {x: 2}
    val mut h: {slot: &{x: number}} = {slot: &q}
    h.slot = p
    return p.x + h.slot.x
}
```

**B2. A `'static` immutable path does not block a later write.** Each program
below checks clean, and each should report an E2 error on its last write or
borrow:

```esc
var cache: &{retries: number} = &{retries: 0}

export fn go() -> number {
    val mut opts = {retries: 3}
    cache = &opts
    opts.retries = 4
    return opts.retries
}
```

```esc
declare fn track(value: &'static mut {retries: number}) -> undefined
declare fn read(value: &{retries: number}) -> number

export fn go() -> number {
    val mut opts = {retries: 3}
    track(&mut opts)
    return read(&opts)
}
```

The store and the call themselves are fine under L1. The bug is that E2 does not
treat the `'static` path as live afterward.

**B3. A borrow taken by an annotation alone escapes E3.** The binding `val c:
&{x: number} = p` borrows `p`, but the move check does not see that borrow. The
program below checks clean, and `take` changes the value `c` reads. Written with
`val c = &p`, the same program reports `cannot move 'p' while it is borrowed`.
F4 rejects the annotation-only form, which closes the hole.

```esc
fn take(p: {x: number}) -> number {
    val mut q = p
    q.x = 5
    return q.x
}

export fn go() -> number {
    val p = {x: 1}
    val c: &{x: number} = p    // no error today. F4 rejects it for the missing &
    val n = take(p)            // no error today, and take changes what c reads
    return c.x + n
}
```

**B4. `mut` does not reach through a mapped type.** The two functions below
declare the same parameter type, once written out and once through
`Readonly<T>`. T3 says `mut` reaches every owned layer, including through type
arguments, so both should check clean. The first does, and the second reports
`cannot constrain immutable object <: mutable object` at the write. An owned
parameter written `mut x: Readonly<{a: {b: number}}>` fails the same way.

```esc
fn resetWritten(x: &mut {readonly a: {b: number}}) {
    x.a.b = 0
}

fn resetMapped(x: &mut Readonly<{a: {b: number}}>) {
    x.a.b = 0
}
```

**B5. A call to an unannotated escaping parameter does not move its argument.**
The compiler infers `store` below as `fn (p: {x: number}) -> undefined`, an
owned parameter, yet `store(a)` records no move and `a.x` checks clean. With
the parameter written as `p: {x: number}`, the same call reports
`use of moved value 'a'`. The unannotated form leaves `a` usable while
`sink` holds the same value, so a later move of `a` to mutable could write what
`sink` reads.

```esc
var sink = {x: 0}

fn store(p) { sink = p }

fn go() -> number {
    val a = {x: 1}
    store(a)
    return a.x
}
```

**B6. An unannotated parameter that does not escape is inferred as owned.** The
compiler infers `fn read(p) { return p.x }` as
`fn <T0>(p: {x: T0}) -> T0`. F3 makes that parameter a borrow, so the
signature should be `fn <T0>(p: &{x: T0}) -> T0`. Today `read(&b)` reports
`cannot use borrowed &object as owned object`, and `read(b)` is accepted
without moving `b`.

## Divergences from the docs

These came from reading the code and need confirming with a fixture each before
the docs change.

- Exclusivity between two explicit borrows, such as `&p` and `&mut p`, is
  enforced by the loan check in
  [borrow_exclusivity.go](../../internal/solver/borrow_exclusivity.go). Both docs
  say it is not yet enforced and cite
  [#794](https://github.com/escalier-lang/escalier/issues/794).
- An owned argument to a borrow parameter needs a written `&`. The docs say the
  borrow is still inserted for the user. See D7.
- Both docs borrow an owned value with an annotation alone, as in
  `val q: &{x: number} = p`. F4 rejects that form, so those examples change to
  `&p`.
- A call to a `&mut self` method on an immutable binding goes through
  `checkReceiverMut` in [classes.go](../../internal/solver/classes.go), which
  appears to reject it. `docs/08_mutability.md` says the call is accepted.
- An element store `t[i] = x` is typed and moves its source. `docs/09_ownership.md`
  says it is rejected as unsupported and cites
  [#1268](https://github.com/escalier-lang/escalier/issues/1268).
- The docs say a field store moves only when the container outlives the source.
  The solver moves on every store into an owned field. F2 agrees with the solver.
  The container owns the value afterward, so the source cannot keep it.
- `docs/09_ownership.md` says a move is read off lifetime constraints. The solver
  decides moves separately at each flow site with its own conditions. The checking
  model above is the proposal to make the docs true.

- Both docs and the solver write an owned value's mutability in its type, as
  `mut T`, and `docs/08_mutability.md` calls `val mut a: {a: {b: {c: number}}}`
  a conflict. T1 moves the mutability to the binding, so that binding is
  ordinary. A `p: mut T` parameter becomes `mut p: T`, a `-> mut T` return
  becomes `-> T`, and the stdlib's `...items: mut Array<T>` rest parameters
  become `...items: Array<T>`.

- A reference-shaped field read through a borrow, such as `line.start` with
  `line: &Line`, produces a borrow today with no `&` written. F4 requires
  `&line.start` there, except on a method receiver and in a pattern.

## Interop

These rules decide how the TypeScript declaration converter and any source
analysis map a TypeScript type onto the rules above.

**I1. `Readonly<T>` keeps its `readonly` slots, and the borrow's mutability is
decided separately.** TypeScript defines `Readonly<T>` as
`{readonly [K in keyof T]: T[K]}`, which adds `readonly` to each top-level slot
and nothing more. That is T5's `readonly`, and Escalier's prelude defines
`Readonly<T>` the same way. Since `readonly` and `mut` are independent, the
converter keeps `Readonly<T>` as written and picks `&` or `&mut` for the
parameter from its other signals, falling back to I3 when they are uncertain.
In the declaration below, `reset` cannot reassign `x.a` but can still run
`x.a.b = 0`. The Escalier signature `reset(x: &mut Readonly<{a: {b: number}}>)`
allows and forbids the same writes, once B4 is fixed.

```ts
declare function reset(x: Readonly<{a: {b: number}}>): void;
```

When every slot in `T`, and in every layer `T` owns, is `readonly` or holds a
value type, a `&mut` grants no writes. The converter then emits `&T` instead.
`Readonly<{a: number}>` and a recursive `DeepReadonly<T>` both qualify.

`classifyExplicitSignal` in
[mutability.go](../../internal/dts_to_esc/mutability.go) maps `this:
Readonly<T>` to `&self` today. That is right only when no slot reachable
through `T` is writable.

`readonly T[]` needs the same treatment, but Escalier has no array type whose
elements are fixed while their contents stay writable, so that case is still
open.

**I2. A value-type parameter is written as its bare type.** A primitive,
function, or promise parameter is copied, so the converter writes the bare type
and no borrow marker.

**I3. An uncertain answer takes the form that rejects more programs.** When the
converter or an analysis cannot settle a fact, it picks the form under which the
rules reject every program the true answer would reject:

- `&mut` over `&` for a receiver or parameter that may be written.
- Escaping over not escaping for a parameter that may be stored.
- A borrow of the inputs over an owned value for a return that may share
  structure with an input. A shallow copy such as `{...opts}` shares everything
  below its top level, so it returns a borrow of `opts`.

## Conformance tests

Each flow site that [F1](#flow-rules) lists gets one test per column below. The
expected result follows from the rules, so reviewing the table of expected
results reviews the rules. The tests are written as Escalier source with the
full expected diagnostic, as `CLAUDE.md` asks.

| Column | Source | Destination | Then | Expected result | Rule |
|---|---|---|---|---|---|
| copy | value type | any | use the source | Accepted | F2 |
| immutable copy | `&T` | `&T` | use the source | Accepted | F2 |
| narrow | `&mut T` | `&T` | use both | Accepted | F2, T4 |
| widen | `&T` | `&mut T` | none | Mutability mismatch | T4 |
| mutable borrow through a `val` binding | owned by `val p`, written `&mut p` | `&mut T` | none | Mutability mismatch | T7 |
| borrow | owned, written `&p` | borrow | use the source | Accepted | F2, F4 |
| unwritten borrow | owned, written `p` | borrow | none | Missing `&` | F4 |
| unwritten field borrow | field read through a borrow, written `r.f` | any | none | Missing `&` | F4, T6 |
| move | owned | owned | use the source | Use of moved value | F2, E1 |
| move on one branch | owned | owned, in one branch | use after the join | Use of moved value | E1 |
| outlive owner | owned | borrow that outlives the source's binding | use the destination | Accepted | L1 |
| write after `'static` immutable borrow | owned, `val mut` | `'static` `&T` | write the source | Exclusivity error | L1, E2 |
| borrow into owned | `&T` | owned | none | Borrow cannot fill owned | T6 |
| write during immutable borrow | owned, `val mut` | `&T` | write the source, then use the destination | Exclusivity error | E2 |
| sequential phases | owned, `val mut` | `&T` | write the source, then create and use the destination | Accepted | E2 |
| move while borrowed | owned | owned | while a borrow of the source is live | Move while borrowed | E3 |
| disjoint field | owned | owned, of `p.a` | use `p.b` | Accepted | E1 |

Some cells do not apply. A literal element has no borrow destination, so its
borrow column is empty, and a value type never meets the move columns. The test
file records such a cell as not applicable instead of leaving it out, so a reader
can see the cell was considered.

## Migration

The checking model replaces several passes the solver runs today. The steps,
each a separate PR:

1. Write the conformance tests above against the current solver. A test the
   current solver fails becomes a known-bug entry, or a reason to change a rule.
2. Settle D9 and D10 and update `docs/08_mutability.md` and
   `docs/09_ownership.md` to match.
3. Move owned mutability from types to bindings, as T1 states. This rewrites the
   `mut T` annotations in tests, fixtures, and the stdlib, and drops the
   owned-mutable `RefType` with no lifetime from
   [soltype](../../internal/soltype/ref.go). The owned-to-`mut` upgrade around
   `isUniquelyOwned` in
   [infer_decl.go](../../internal/solver/infer_decl.go) becomes an ordinary move
   checked by E3.
4. Extend the missing-`&` check from call arguments to every flow site and to
   field reads through a borrow, as F4 states. Each error carries the borrow
   its destination needs, `&` or `&mut`, the way `ImplicitBorrowArgError` in
   [borrow_exclusivity.go](../../internal/solver/borrow_exclusivity.go) does
   today. The same PR adds an LSP quick fix that inserts that borrow:
   - The published diagnostic carries the borrow and the range of the
     expression in its `data` field.
   - `textDocumentCodeAction` in
     [text_document.go](../../cmd/lsp-server/text_document.go) returns a
     `quickfix` action whose edit inserts `&` or `&mut ` before the expression.
     It wraps the expression in parentheses unless the expression is an
     identifier, a field access, an index, or a call.
   - `quickfix` joins the code action kinds the server advertises in
     [main.go](../../cmd/lsp-server/main.go).
   - Tests in `text_document_test.go` cover `&`, `&mut`, and the parenthesized
     case.
5. Build the event lowering and the single control-flow pass behind a flag. Run
   it alongside the current checks and compare diagnostics across every test and
   fixture. Resolve each difference as a bug in the old checks or a change to the
   rules.
6. Remove the old checks one at a time once the new pass matches them. The groups
   that the checking model replaces are:
   - The per-site `consume*` functions in
     [moves.go](../../internal/solver/moves.go) and
     [module_level_moves.go](../../internal/solver/module_level_moves.go),
     replaced by F2 and F3.
   - The alias-set transition check in
     [transitions.go](../../internal/solver/transitions.go) and the loan check
     in [borrow_exclusivity.go](../../internal/solver/borrow_exclusivity.go),
     both replaced by E2 and E3.
   - The frame-exit checks in
     [return_escape.go](../../internal/solver/return_escape.go) and
     [return_shared_paths.go](../../internal/solver/return_shared_paths.go),
     replaced by L1 and E2 at the return.
   - The separate escape logic in
     [return_escape.go](../../internal/solver/return_escape.go) and
     [borrow_flow.go](../../internal/solver/borrow_flow.go), replaced by the
     lifetime solver answering F3.
