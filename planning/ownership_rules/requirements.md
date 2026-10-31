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
freely, and the rules below do not apply to them. The one exception is a
function parameter, which may carry a lifetime saying how long the callee keeps
the function, as E4 states.

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
binding it lands in. Inference never makes a binding mutable. A parameter is
mutable only when it is written `mut p`, whether or not it has a type
annotation, so `fn f(p) { p.x = 1 }` reports that `p`'s binding is immutable.
F3 still infers an unannotated parameter's type.

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
``cannot mutate 'p' because its binding is immutable; declare it as `val mut p` ``.
With `val mut p = {x: 1}`, both `&p` and `&mut p` are accepted.
A value that no binding holds, such as a literal or a call's result, is borrowed
as though a fresh `val mut` held it, as D15 decides. `&mut e` checks the same
way as `val mut t = e` followed by `&mut t`.

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
| Literal element | `{f: p}`, `[p]` | The matching field or element type of the literal's expected type, as in `val o: {f: &T} = {f: &p}`. Owned when the literal has no expected type |
| Spread | `{...p}` | Owned |
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

A value type has no borrow form, so its destination is always bare. A generic
`&T` whose `T` is a value type stands for `T`. With `fn f<T>(x: &T)`, the call
`f(5)` copies `5` into `x`, and a read of a value-type field through a `&` copies
the field out. Each binding
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
  destination unless the state's type is a borrow.
- An unannotated parameter is owned when the body lets the parameter escape, and
  a borrow otherwise. When the destination's type leaves the ownership open,
  [D9](#d9-whether-an-escaping-value-moves-or-borrows) decides.
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
`b`. A field read through that borrow keeps it, as a read through a written
`&` parameter does, so `read` returns `&'a T0`. A use that needs a value type,
such as the `n` in `b.x + n`, copies the field out of the borrow:

```esc
var sink = {x: 0}

fn store(p) { sink = p }     // store: fn (p: {x: number}) -> undefined
fn read(p) { return p.x }    // read: fn <T0, 'a>(p: &'a {x: T0}) -> &'a T0

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

Three places borrow without a written `&`:

- A method receiver, since the method's own `self` declares the borrow.
  `line.start.norm()` borrows `line.start` for the call.
- A pattern over a borrow. With `line: &Line`, `val {start} = line` binds
  `start: &Point`, since the pattern already says it reads from `line`. The
  pattern binds with the borrow's mutability, so with `line: &mut Line` it
  binds `start: &mut Point`, as D14 decides.
- A closure capture, since F3 decides whether the closure borrows or owns what
  it captures, and the body decides the borrow's mutability.
  `fn () { return p.x }` borrows `p` immutably while it stays in the body.

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
An immutable borrow of a place that already has an immutable borrow outliving
the current frame takes that borrow's lifetime, as D16 decides. E2 rejects
every write to the place while the first borrow is live, so the second borrow
reads a value that does not change for as long as either can read it.

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

An unannotated function whose returned `&` has a lifetime that ends with the
function's frame gets an owned return, as D16 decides. Such a lifetime is not
tied to a parameter, a capture, or `'static`. Only a value that nothing outside
the call can reach fits that description, so the caller may own it.

Each comment below gives the rendered signature:

```esc
var sink: &mut {x: number} = &mut {x: 0}
var shared: &{x: number} = &{x: 0}

fn fresh() {
    val q = {x: 1}
    return &q
}
// fn () -> {x: number}, since nothing outside the call reaches q

fn kept() {
    val q = {x: 1}
    shared = &q
    return &q
}
// fn () -> &'static {x: number}, since shared also reads q

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

A function parameter says how long the callee keeps the function it receives.
A parameter written `f: fn () -> undefined` is only called during the call, so a
closure passed to it holds its borrows for that call alone. A parameter written
`f: &'a fn () -> undefined` may keep the function for `'a`, so a closure passed
to it holds its borrows for `'a`, and `&'static` keeps them for the rest of the
program. A function is still a value type, so F2 copies it into either kind of
parameter, and the call needs no written `&`. The `&'a` on a function
parameter does not borrow the function. It only says how long the callee keeps
its copy. [D10](#d10-recording-that-a-callback-is-not-retained) shows an
example.

A `fn` type outside a parameter, such as in a binding, a field, an array element,
or a return type, keeps the function for a lifetime inferred from where the
value flows, the same way an elided `&` lifetime is inferred, as D17 decides. A
module-level slot keeps it for `'static`, so storing a plain `fn` parameter
there is rejected.

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
- **Moves into literals and spreads.** A literal element with no expected type
  fills an owned slot of the new value, so F2 moves an owned source. A spread
  fills one slot for each field of its source, so it moves each
  reference-shaped field and copies each value-type field.

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
   and move. Each borrow carries the lifetime L1 gives it. A flow into a
   destination whose ownership F3 has not settled yet, such as an unannotated
   parameter, is recorded as a provisional flow from its source to its
   destination.
2. Once inference finishes, the lifetime solver settles F3's escape questions
   from the outlives constraints. Each provisional flow then becomes the copy,
   borrow, or move event that F2 gives for the settled destination type.
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
stated. D9 through D18 are design questions the rules left open. Each decided
entry says where its outcome now lives. All of them are now decided. The current
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

Decided: accepted. Step 3 of the migration drops the move that
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

Decided: accepted. An escaping parameter that the body never writes is inferred
as a `'static &` borrow, and one the body writes is owned. The rule applies only
where the destination's type leaves the ownership open, such as a `Map` whose
value type is inferred, or a function the converter declares from JavaScript
source. Where the destination's type is owned, T6 already makes the parameter
owned, as for `store` in F3. A written annotation overrides the inference.

### D10. Recording that a callback is not retained

A function value is a value type, so it carries no lifetime, and nothing in the
rules distinguishes a callee that only calls a callback from one that stores it.
The source-analysis plan for third-party packages expects to record exactly that
fact. [#1267](https://github.com/escalier-lang/escalier/issues/1267) tracks the
same gap for Escalier's own functions.

The program below shows the gap. It writes the parameters in the lifetime form
recommended below. `onClick` keeps its handler for the rest of the program, so
the handler's mutable borrow of `count` stays live after the call, as for a
closure stored into module-level state under E4. `render` then takes an
immutable view of `count` that a click could change. Today the two parameter
types are the same `fn () -> undefined`, so the checker treats `onClick` like a
callee that only calls its argument and accepts the program.

```esc
declare fn onClick(handler: &'static fn () -> undefined) -> undefined
declare fn forEachItem(f: fn () -> undefined) -> undefined
declare fn render(view: &{n: number}) -> undefined

fn setup() {
    val mut count = {n: 0}
    onClick(fn () { count.n = count.n + 1 })
    render(&count)    // error: a click may run the handler, which writes 'count' while it is borrowed as immutable
}
```

There are two ways to write `setup` so that it checks. The first lets the
handler own its state. `onClick` keeps the closure past the frame, so by F3 the
closure owns `count`, and the borrow inside it lasts only for one call:

```esc
fn setup() {
    val mut count = {n: 0}
    onClick(fn () {
        count.n = count.n + 1
        render(&count)    // accepted
    })
}
```

The second gives `render` a mutable view. An immutable view promises that the
value does not change while it is held, which a stored handler makes
impossible. A mutable view makes no such promise, and E2 allows several mutable
paths at once:

```esc
declare fn render(view: &mut {n: number}) -> undefined

fn setup() {
    val mut count = {n: 0}
    onClick(fn () { count.n = count.n + 1 })
    render(&mut count)    // accepted
}
```

The options are:

- Give function types an optional lifetime, as in `&'a fn () -> undefined`.
  The lifetime bounds how long the callee may keep the function, and E4 reads it
  to decide how long the captures stay borrowed.
- Add a marker on the parameter that says the callee does not keep it, and treat
  every unmarked function parameter as retained.

Recommendation: the lifetime form. It reuses L1 and E4 instead of adding a rule.

Decided: accepted. A function type takes an optional lifetime, as in
`&'a fn () -> undefined`, and E4 states how a call reads it. A function
parameter with no lifetime is only called during the call.

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

### D12. A `mut` that grants nothing

T1 lets any binding or parameter be written `mut`, and nothing rejects one whose
`mut` cannot matter. A value type has no owned layers, so T2 and T3 give the
`mut` nothing to make writable. A borrow has none either. A `&` borrow allows
no writes, and a `&mut` borrow gets its writes from the borrow, not from the
binding. An owned value can also have nothing to write. A `Promise<T>` is one,
since nothing can write the value inside it. An instance of a class whose fields
are all `readonly` is another. T1 keeps reassignment on `var`, so the `mut`
does not make the binding reassignable either:

```esc
fn read<'a>(p: &'a {x: {y: number}}) -> &'a {y: number} { return &p.x }

fn f(mut p?: number) -> number { return 1 }

fn g() {
    val b = {x: {y: 1}}
    var mut r = read(&b)    // r holds a & borrow, so the mut grants nothing
    r.y = 5                 // rejected, since r is a & borrow
}

async fn fetchNum() { return 5 }

fn h() {
    val mut p = fetchNum()  // nothing inside a promise can be written
}
```

In `g`, a warning on the `mut` points at the binding behind the error on
`r.y = 5`.

A slot is writable through a `mut` binding when it is a field that is not
`readonly`, a tuple or array element, or a class argument the class writes. A
class writes an argument when the argument is invariant in the class's
`MutVariance`, as it is when a field that is not `readonly` holds it.
`Map<K, V>` and a class with a writable `value: T` have writable slots.
`Promise<T>` does not.

The same holds for the `mut` of a `&mut` borrow. A `&mut` borrow of a value
with no writable slot allows exactly the reads a `&` borrow allows, yet it
reads as a promise that the callee may change the value:

```esc
fn wait(p: &mut Promise<number>) -> undefined { … }   // nothing in p can be written

fn k() {
    val mut q = fetchNum()  // warns, since q has no writable slot
    wait(&mut q)            // warns, since &mut q allows no write
}
```

The options are:

- Accept it silently, as the checker does today.
- Warn when the binding's type has no writable slot, meaning a value type, a
  `&` borrow, a `&mut` borrow, or an owned value whose layers hold nothing a
  write can replace. Warn the same way on a `&mut` type or a `&mut`
  expression whose referent has no writable slot, and suggest `&`. Each check
  needs only one type, and such a `mut` can never matter.
- Warn when a `mut` binding is never written and never borrowed as `&mut`, as
  Rust's `unused_mut` lint does. This covers the value-type case and also an
  owned binding the body only reads, but it needs the body's write analysis.

Recommendation: warn on a type with no writable slot now and add the
unused-binding warning later. Both are warnings rather than errors, since the
program means the same with or without the `mut`.

Decided: accepted. A `mut` binding whose type has no writable slot reports a
warning, and so does a `&mut` type or expression whose referent has none. Both
are tracked by
[#2304](https://github.com/escalier-lang/escalier/issues/2304).
A warning on any `mut` binding the body never writes or borrows as `&mut` is a
later addition, tracked by
[#2305](https://github.com/escalier-lang/escalier/issues/2305).

### D13. Binding mutability is written, not inferred

T2 makes every binding immutable unless it is written `mut`, and T1 lets a
parameter declare a mutable binding as `fn f(mut p: T)`. F3 infers an
unannotated parameter's ownership from the body, but says nothing about its
binding's mutability. The checker infers that too. A write in the body makes an
unannotated parameter's binding mutable:

```esc
var sink = {x: 0}

fn f(p) {
    p.x = 1
    sink = p
}
```

The options are:

- Infer the binding's mutability from writes in the body, as the checker does
  today.
- Require `mut` to be written, as T2 states, and report a write through an
  immutable binding with a message that says to write `mut p`.

A binding's mutability is not part of the signature, since `fn f(mut p: T)`
has the same type as `fn f(p: T)`. Inferring it gives the caller nothing, and
it hides from a reader of the signature whether the body changes its own value.

The parameter's type is different. Whether an unannotated parameter is owned,
`&T`, or `&mut T` decides what the caller writes, so it stays inferred. F3
says a parameter that does not escape is a borrow. A body that writes through
such a parameter makes it `&mut`, and one that only reads it makes it `&`.

Recommendation: require `mut` on every mutable binding, parameters included,
and keep inferring an unannotated parameter's type.

Decided: accepted. `fn f(p)` above reports the write to `p.x` and asks for
`mut p`. Step 2 of the migration removes the inference of binding mutability.
Step 4 infers `&` or `&mut` for an unannotated parameter that does not
escape, from whether the body writes through it.

### D14. The mutability of a borrow a pattern binds

F4 lets a pattern over a borrow bind borrows without a written `&`. With
`line: &Line`, `val {start} = line` binds `start: &Point`. F4 does not say
what the pattern binds when the scrutinee is a `&mut` borrow:

```esc
class Point { x: number, y: number }
class Line { start: Point, width: number }

fn f(line: &mut Line) {
    val {start} = line
    start.x = 1
}
```

The checker binds `start` as an immutable `Point`, so the write reports
`cannot constrain immutable Point <: mutable object`. Writing `line.start.x = 1`
is accepted, so the pattern takes away a write the borrow allows.

The options are:

- Bind borrows with the scrutinee's mutability. A pattern over `&mut Line` binds
  `start: &mut Point`, and a pattern over `&Line` binds `start: &Point`. This
  is Rust's default binding mode.
- Always bind `&`. A pattern over `&mut Line` binds `start: &Point`, and a
  caller that needs to write uses `&mut line.start`. The mutability of every
  `&mut` stays written, but a pattern over a `&mut` gives up its writes.

The binding's own `mut` plays no part in either option. T3 applies a
binding's `mut` to the layers its value owns, and `start` owns none.

Recommendation: bind borrows with the scrutinee's mutability. The scrutinee's
type already shows the `&mut`, and T4 lets a caller narrow `start` to `&Point`
wherever it needs only reads.

Decided: accepted. F4 states it. The checker change is tracked by
[#2318](https://github.com/escalier-lang/escalier/issues/2318).

### D15. A mutable borrow of a value no binding holds

T7 grants `&mut` through a binding's `mut`. A literal or a call's result has no
binding, so T7 gives no answer for it:

```esc
fn mk() -> {x: number} { return {x: 1} }
fn bump(p: &mut {x: number}) { p.x = 2 }

fn g() {
    val s: &mut {x: number} = &mut mk()
    bump(&mut {x: 1})
}
```

The checker rejects both borrows with
`cannot constrain immutable object <: mutable object`. The literal also reports
`cannot constrain number <: 1`, since its field keeps the literal type.

The same programs check when the value goes through a binding first, as in
`val mut t = mk()` followed by `bump(&mut t)`. Nothing else holds the value
the borrow reaches, so the borrow aliases nothing.

The options are:

- Reject `&mut` of a value no binding holds, and require the binding.
- Check `&mut e` as `val mut t = e` followed by `&mut t`, for an `e` that is
  not a place. Each check the binding runs then decides the borrow.

Under the second option the answer for a call follows its return type. An owned
return is accepted, as `val mut t = mk()` is. A `&mut T` return is a mutable
reborrow. A `&T` return is rejected, as is a readonly collection that a
`.d.ts` return type hands out as a `'static` borrow, since neither fills an
owned `mut` binding. A literal widens its fields, as `val mut q = {x: 1}` does,
so `&mut {x: 1}` borrows a `{x: number}`. A literal that holds a place, such as
`&mut {p: cfg}` with a module-level `cfg`, reports the module-level move that
`val mut t = {p: cfg}` reports. The runtime is garbage collected, so the
borrowed value lives as long as the borrow does.

Recommendation: check `&mut e` as the binding it stands for.

Decided: accepted. T7 states it. The checker change is tracked by
[#2324](https://github.com/escalier-lang/escalier/issues/2324).

### D16. A returned borrow of a value nothing else reaches

L1 lets a borrow outlive the binding that owns its place, so a function may
return `&b` for a local `b`. The rules do not say what type the caller receives.
The checker infers an owned return, which is unsound when module-level state
still reads `b`:

```esc
var cache: &{x: number} = &{x: 0}

fn h() {
    val b = {x: 1}
    cache = &b
    return &b        // h infers fn () -> {x: 1}
}

fn k() {
    val mut r = h()
    r.x = 5          // writes the value cache still reads
}
```

A lifetime says how long one borrow stays usable. It does not say whether
another path reaches the same value, so the two borrows of `b` above can carry
different lifetimes. E2 closes that gap. While one immutable borrow of `b` is
live, no write to `b` can follow, so any other immutable borrow of `b` can
live as long as that one does.

The options are:

- Infer every returned `&` as a borrow. This is always sound, but `return &b`
  for a local that nothing else reaches hands the caller a borrow it could
  safely own.
- Infer an owned return only when nothing outside the call reaches the value.
  The returned borrow's lifetime records that, given two rules:
  1. An immutable borrow of a place that already has an immutable borrow
     outliving the current frame takes that borrow's lifetime.
  2. A returned `&` is owned when its lifetime ends with the callee's frame,
     meaning it is not tied to a parameter, a capture, or `'static`.

Under the second option, the answer for a call follows the callee's signature:

- `return same(&b)` with `same<'a>(p: &'a {x: number}) -> &'a {x: number}`
  stays a borrow. The `'a` result may be a `'static` value rather than `b`, and
  owning it would alias that value.
- `return same(&b)` with `same(p: &{x: number}) -> &{x: number}` is owned. The
  result ties to nothing in the signature, so its lifetime ends with the
  caller's frame. A body that returns `p` under that signature is rejected by
  L2.
- A function that returns a borrow of module-level state renders its result as
  `&'static`, since L3 always renders `'static`.

The two rules work together. A result that ties to no parameter comes from
somewhere else. Either the function built it, or the result borrows something
that already exists outside the call. Each comment below gives the rendered
signature:

```esc
var cfg = {x: 0}
var cache: &{x: number} = &{x: 0}

fn same1(p: &{x: number}) {
    return &cfg
}
// fn (p: &{x: number}) -> &'static {x: number}, since cfg is module-level

fn same2(p: &{x: number}) {
    val q = {x: 1}
    cache = &q
    return &q
}
// fn (p: &{x: number}) -> &'static {x: number}, since cache also reads q
```

In both functions, something else still reads the returned value after the
call. In `same1` it is `cfg` itself, and in `same2` it is `cache`. Without the
first rule, the `&q` that `same2` returns would carry its own lifetime, because
only the `&q` stored in `cache` flows into module-level state. The second rule
would then make the result owned, and a caller could write the value `cache`
reads. The first rule gives the returned `&q` the lifetime of the borrow in
`cache`, so the result stays a borrow.

The rules read the frame rather than the module, so they hold when the same
functions are closures. Here `cfg` and `cache` are locals of `outer`, and
nothing is `'static`:

```esc
fn outer() {
    val cfg = {x: 0}
    var cache: &{x: number} = &{x: 0}

    val same1 = fn (p: &{x: number}) {
        return &cfg
    }
    val same2 = fn (p: &{x: number}) {
        val q = {x: 1}
        cache = &q
        return &q
    }

    val r = same2(&cfg)
    return r
}
// fn () -> {x: number}
```

- `same1` returns a borrow tied to its capture of `cfg`, so its result is a
  borrow. The lifetime belongs to `outer`'s frame, which outlives the closure's
  call.
- `same2` returns a borrow that takes the lifetime of the borrow in `cache`
  under the first rule. That lifetime belongs to `outer`'s frame, so `r` is a
  borrow inside `outer`, and a write through `r` is rejected.
- `outer` returns `r`, whose lifetime ends with `outer`'s frame. Once `outer`
  returns, `cache` is gone and nothing else reaches `q`, so the second rule
  makes the return owned. If a closure that captures `cache` escaped `outer`,
  for instance by being returned or stored in module-level state, the lifetime
  would reach past `outer` and the return would stay a borrow.

A bodyless signature is taken at its word, so the declaration converter must
render `'static` for any returned borrow it cannot vouch for. This entry covers
`&` returns only. A `&mut` return keeps its current behaviour.

Recommendation: infer an owned return only when nothing outside the call reaches
the value, using the two rules above.

Decided: accepted. L1 and L3 state it. The checker change is tracked by
[#2330](https://github.com/escalier-lang/escalier/issues/2330).

### D17. The retention lifetime of a `fn` type outside a parameter

E4 and D10 give a plain `fn` parameter a meaning: the callee only calls the
function during the call. A `fn` type also appears in a binding, a field, an
array element, and a return type, and the rules do not say how long those keep
the function. The checker accepts any plain `fn` there, which lets a body keep
a call-only parameter past the call:

```esc
var sink: fn () -> undefined = fn () {}

fn keep(g: fn () -> undefined) {
    val k: fn () -> undefined = g
    k()            // harmless, since k lives only in the body
    sink = g       // keeps g for the rest of the program
}
```

The options are:

- Read a plain `fn` outside a parameter as `&'static fn`. This is always sound,
  but it rejects `val k: fn () -> undefined = g`, which keeps `g` no longer
  than the call does.
- Infer the retention lifetime from where the value flows, the same way an
  elided `&` lifetime is inferred. A module-level slot such as `sink` lives for
  the rest of the program, so its lifetime is `'static`. A local such as `k`
  keeps the function only while `k` is live. A return type or a class field
  takes the lifetime of the caller's result or the instance, which the
  signature or the class names.

Under the second option, `sink = g` is rejected, since `g` is kept only for the
call and `sink` needs `'static`. `val k = g; k()` is accepted. L3 renders the
inferred lifetime the way it renders a borrow's, so `sink` shows as
`&'static fn () -> undefined` and an unconnected local lifetime is elided.

Recommendation: infer the retention lifetime of a `fn` type outside a parameter
the way an elided `&` lifetime is inferred.

Decided: accepted. E4 states it. The checker change is tracked by
[#2342](https://github.com/escalier-lang/escalier/issues/2342).

### D18. A module-level binding whose type its uses decide

The checker generalizes every binding. Each use of a generalized binding gets
fresh copies of its type variables, which is right for a function and wrong for
a container a call creates:

```esc
val mut seen = Map()
fn a() { seen.set("k", 1) }
fn b() { seen.set("k", "s") }
```

`a` and `b` each write into their own copy of `seen`'s key and value types, so
both calls check and `seen` renders as `Map<never, never>`. A function that
stores a `'static` borrow and another that stores an owned value also both
check, which D9 relies on not happening.

The options are:

- Generalize every binding, as the checker does today.
- Generalize only a binding whose initializer is a syntactic value, and leave
  the variables of any other binding weak. A weak variable is shared by every
  use of its binding, so the uses decide its type together. OCaml calls this
  the value restriction, and prints such a variable as `'_weak1`.
- Require an annotation on every module-level binding, as Rust requires one on
  every `static`.

An annotation on every binding gives up inference for the common case of a
call whose result needs none, such as `val five = id(5)`. Weak variables keep
that case and still give `seen` one type.

Recommendation: adopt weak variables, as OCaml does, and require no
annotation.

Decided: accepted. A `val` or `var` generalizes when it has a type annotation,
or when it is an immutable binding whose initializer is a syntactic value. These
initializers are syntactic values:

- a function expression,
- a literal, and a template literal whose interpolations are syntactic values,
- an identifier,
- a member or index read whose object and index are syntactic values,
- an object or tuple literal whose members and spreads are syntactic values,
  where a method is a property holding a function expression, and
- a unary, binary, borrow, or type-cast expression whose operands are syntactic
  values.

A call, which also constructs a class instance, is not one. Neither is a tagged
template, `await`, `yield`, `throw`, or an `if`, `match`, `do`, or `try`
expression. A `var` and a binding written `mut` never generalize, whatever
their initializer, since a write through one use would not reach another. So
`val f = fn (x) { return x }` stays `fn <T0>(x: T0) -> T0`.

A weak binding's type settles once everything that can write it is inferred.
For a module-level binding that is the end of the module, and for a binding in
a function or script body it is the end of that body. The checker then records
the settled type for display, so hover and the rendered bindings show what the
uses decided rather than what the initializer alone gave.

A weak variable is one type, so the settled type shows it the same way in every
position. That type is the union of the values that flowed into it, or the
types its uses require when no value did, or `never` when nothing reached it.
With `fn makeId() { return fn (x) { return x } }`, `val g = makeId()` called
as `g(5)` and `g("a")` settles to `fn (x: 5 | "a") -> 5 | "a"`. A `g` that
nothing calls settles to `fn (x: never) -> never`, so a module that imports it
cannot call it with a value the exporting module never settled it on.

The settled type widens the literals in each slot the binding can write, the
same slots T9 widens in a literal initializer:

- A `val` binding writes nothing, so it keeps every literal.
  `val c = id([5, 10])` settles to `[5, 10]` as `val a = [5, 10]` does, and
  `val b = Box(5)` settles to `Box<5>`.
- A `var` binding can be rebound, so its whole value is a writable slot.
  `var q = fetchNum()` settles to `Promise<number>`.
- A `mut` binding can write its owned layers. A field that is not `readonly`, a
  tuple element, and a class argument the class's `MutVariance` makes invariant
  are writable slots. In the example, `seen` settles to
  `Map<string, number | string>`, and `val mut b = Box(5)` settles to
  `Box<number>`. A class argument that is only read, such as the `T` of
  `Promise<T>` or of a class whose field holding `T` is `readonly`, is not a
  writable slot. `val mut p = fetchNum()` keeps `Promise<5>`, since nothing can
  write the value inside a promise.
- Every position inside a writable slot is writable too, since a write replaces
  the slot's whole value. `val mut a = [fetchNum()]` settles to
  `[Promise<number>]`.

A class argument the class makes contravariant keeps its literal in every case,
since widening it would change what the value accepts.

A borrow's literals belong to the place it points to, so a borrow that is not
in a writable slot keeps them, whether it is `&` or `&mut`. `val r = borrow(&p)`
with `val p = {x: 1}` settles to `&{x: 1}`. A `&` borrow in a writable slot
widens what it points to, since a write can replace it with a borrow of any
value of the widened type, and `&T` accepts a borrow of any subtype of `T`.
With `val mut seen = Map()` written with `seen.set("k", &p)`, `seen` settles to
`Map<string, &{x: number}>`, and `var r = borrow(&p)` settles to
`&{x: number}`. A `&mut` borrow keeps its literals even in a writable slot,
since `&mut T` accepts only a borrow of exactly `T`. With
`val mut p: {x: 1} = {x: 1}`, widening `val r = borrowMut(&mut p)` to
`&mut {x: number}` would claim that `r.x = 2` is allowed, which the type of `p`
rejects.

The checker requires no annotation. A weak variable no use decides settles
silently. A container that is never written settles at `never`, so
`val mut seen = Map()` with no `seen.set` reads as `Map<never, never>`. A
function that stores its parameter in the map and has no caller leaves the
parameter `unknown`, as `value` is in
`fn remember(value) { seen.set("last", value) }`.
An exported binding needs no annotation either, so `export val mut b = Box(5)`
settles to `Box<number>`. Requiring one for an exported or undecided weak
variable is a possible later tightening.

A weak variable that received an owned value from one use and a borrow from
another reports the error T8 gives a join of such branches, against the
module-level binding.

The rule lives in `generalizes` in [poly.go](../../internal/solver/poly.go),
and the settling in [weak.go](../../internal/solver/weak.go).

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
signature should be `fn <T0, 'a>(p: &'a {x: T0}) -> &'a T0`. Today `read(&b)` reports
`cannot use borrowed &object as owned object`, and `read(b)` is accepted
without moving `b`.

**B7. A retained callback's captures are borrowed only for the call.** The
checker treats every function parameter as one the callee only calls, so a
closure passed to a callee that keeps it holds its borrows for the call alone.
The first program in [D10](#d10-recording-that-a-callback-is-not-retained)
checks clean today and should report the error marked there.
[#1267](https://github.com/escalier-lang/escalier/issues/1267) tracks the gap.

**B8. A borrow copied into a second binding is not tracked.** A loan records
only the binding that a written `&` or `&mut` fills. `recordBorrowLoan` in
[borrow_exclusivity.go](../../internal/solver/borrow_exclusivity.go) ignores an
initializer that names an existing borrow, so the copy holds no loan. A
conflict through the copy is not reported. The loan also ends when the
original holder is last used, even while the copy is still read. The program
below checks clean today and should report the shared and mutable pair at the
call:

```esc
declare fn readWrite(a: &{v: number}, b: &mut {v: number}) -> undefined

fn g() {
    val mut x = {v: 1}
    val a = &x
    val c = a
    val b = &mut x
    readWrite(c, b)
}
```

The checking model closes it by recording the copy as a `copy(place, holder)`
event, which makes `c` a second holder of the borrow.
[#1527](https://github.com/escalier-lang/escalier/issues/1527) tracks the gap.

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
- An element store `t[i] = x` is typed and moves its source. `docs/09_ownership.md`
  says it is rejected as unsupported and cites
  [#1268](https://github.com/escalier-lang/escalier/issues/1268).
- The docs say a field store moves only when the container outlives the source.
  The solver moves on every store into an owned field. F2 agrees with the solver.
  The container owns the value afterward, so the source cannot keep it.
- `docs/09_ownership.md` says a move is read off lifetime constraints. The solver
  decides moves separately at each flow site with its own conditions. The checking
  model above is the proposal to make the docs true.

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
and no borrow marker. A function parameter the callee keeps is the one
exception, written `&'a fn …` as E4 states.

**I3. An uncertain answer takes the form that rejects more programs.** When the
converter or an analysis cannot settle a fact, it picks the form under which the
rules reject every program the true answer would reject:

- `&mut` over `&` for a receiver or parameter that may be written.
- Escaping over not escaping for a parameter that may be stored.
- A borrow of the inputs over an owned value for a return that may share
  structure with an input. A shallow copy such as `{...opts}` shares everything
  below its top level, so it returns a borrow of `opts`.

## Conformance tests

The tests come in two sets. The flow matrix crosses every flow site with the
cases the flow and exclusivity rules decide. The per-rule list covers the rules a
flow site cannot express. In both, the expected result follows from the rules,
so reviewing the expected results reviews the rules. The tests are written as
Escalier source with the full expected diagnostic, as `CLAUDE.md` asks.

### Flow matrix

Each flow site that [F1](#flow-rules) lists gets one test per column below.

| Column | Source | Destination | Then | Expected result | Rule |
|---|---|---|---|---|---|
| copy | value type | owned | use the source | Accepted | F2 |
| immutable copy | `&T` | `&T` | use the source | Accepted | F2 |
| mutable copy | `&mut T` | `&mut T` | write through both | Accepted | F2, E2 |
| narrow | `&mut T` | `&T` | use both | Accepted | F2, T4 |
| widen | `&T` | `&mut T` | none | Mutability mismatch | T4 |
| borrow | owned, written `&p` | `&T` | use the source | Accepted | F2, F4 |
| mutable borrow | owned by `val mut p`, written `&mut p` | `&mut T` | write through the destination | Accepted | F2, T7 |
| mutable borrow through a `val` binding | owned by `val p`, written `&mut p` | `&mut T` | none | Mutability mismatch | T7 |
| unwritten borrow | owned, written `p` | borrow | none | Missing `&` | F4 |
| unwritten field borrow | field read through a borrow, written `r.f` | any | none | Missing `&` | F4, T6 |
| immutable borrow into owned | `&T` | owned | none | Borrow cannot fill owned | T6 |
| mutable borrow into owned | `&mut T` | owned | none | Borrow cannot fill owned | T6 |
| move | owned | owned | use the source | Use of moved value | F2, E1 |
| move on one branch | owned | owned, in one branch | use after the join | Use of moved value | E1 |
| move in a loop | owned | owned, inside a loop body | the next iteration | Use of moved value | E1 |
| reassign after a move | owned | owned | reassign the source, then use it | Accepted | E1 |
| partial move | owned field `p.a` | owned | use `p` | Use of moved value | E1 |
| disjoint field | owned field `p.a` | owned | use `p.b` | Accepted | E1 |
| move out of module-level state | module-level binding | owned | none | Move out of module-level binding | E1 |
| outlive owner | owned, written `&p` | borrow that outlives the source's binding | use the destination | Accepted | L1 |
| write during immutable borrow | owned by `val mut p`, written `&p` | `&T` | write the source, then use the destination | Exclusivity error | E2 |
| sequential phases | owned by `val mut p`, written `&p` | `&T` | write the source, then create and use the destination | Accepted | E2 |
| write during mutable borrow | owned by `val mut p`, written `&mut p` | `&mut T` | write the source, then use the destination | Accepted | E2 |
| immutable borrow during mutable borrow | owned by `val mut p`, written `&mut p` | `&mut T` | take `&p`, then use both | Exclusivity error | E2 |
| write after `'static` immutable borrow | owned by `val mut p`, written `&p` | `'static` `&T` | write the source | Exclusivity error | L1, E2 |
| immutable borrow after `'static` mutable borrow | owned by `val mut p`, written `&mut p` | `'static` `&mut T` | take `&p` | Exclusivity error | L1, E2 |
| disjoint borrows | owned by `val mut p`, written `&p.a` | `&T` | write `p.b`, then use the destination | Accepted | E2 |
| element borrows | owned by `val mut a`, written `&a[i]` | `&T` | write `a[j]`, then use the destination | Exclusivity error | E2 |
| move while immutably borrowed | owned, written `&p` | owned, in a `val` binding | move the source while the borrow is live | Move while borrowed | E3 |
| move to mutable while immutably borrowed | owned, written `&p` | owned, in a `val mut` binding | move the source while the borrow is live | Move while borrowed | E3 |
| move while mutably borrowed | owned by `val mut p`, written `&mut p` | owned | move the source, then use the borrow | Move while borrowed | E3 |

Every cell that a program can express is a test, and its expected result is
the one the rules give at that site. At a few sites that result differs from
the column's, and the test says why:

- A method receiver, a pattern over a borrow, and a closure capture borrow
  without a written `&`, by F4. At those sites the "unwritten borrow" column
  expects Accepted, not a missing `&`. A receiver and a pattern do the same
  for the "unwritten field borrow" column.
- A closure capture takes the borrow its body needs, so the written-borrow
  columns test the capture the closure makes.
- A capture of a borrow copies it, so a returned closure that captures a borrow
  gives T6 no owned destination to reject.

A cell no program can express is recorded as not applicable instead of left
out, so a reader can see it was considered. The only such cells are the move
columns that use the source after the flow, at a `return`, since nothing after
the `return` can use the source.

### Per-rule tests

Each case below is one test. Most of them are the examples already in the rule,
so the test and the rule state the same program. The interop cases are tests of
the declaration converter in `internal/dts_to_esc`, and the rest are tests of
the solver.

| Rule | Case | Expected result |
|---|---|---|
| T2 | Write `p.x = 1` with `val p` | Rejected |
| T3 | Write `p.a.b = 1` with `val mut p` | Accepted |
| T3 | Push onto `val a: Array<&mut Point>`, then write `a[0].x` | Push rejected, write accepted |
| T3 | Push onto `val mut b: Array<&Point>`, then write `b[0].x` | Push accepted, write rejected |
| T3 | Annotate `{a: mut {b: number}}` or `Array<mut Point>` | Rejected |
| T5 | Write a `readonly` field after its initialization | Rejected |
| T5 | A `{readonly a: number}` value into a `{a: number}` slot of a `val mut` binding | Rejected |
| T5 | Assign to a getter with no setter | Rejected |
| T5 | Write a `readonly` field twice in a constructor | Rejected |
| T8 | `if c { p } else { q }` with `p` owned and `q` a borrow | Rejected |
| T8 | `if c { &r } else { q }` | Accepted |
| T9 | `val n = 5`, `var n = 5`, `val p = {x: 1}`, `val mut p = {x: 1, y: {z: "a"}}`, and `var p = {x: 1}` | The inferred types T9 lists |
| T9 | The `Request` class fields | The inferred types T9 lists |
| T10 | A getter with `&mut self`, and a setter with `&self` | Both rejected |
| F3 | `make` and `same` from F3 | The signatures F3 lists |
| F3 | `store` and `read` from F3 | `store(a)` moves `a`, and `read(&b)` leaves `b` usable |
| F3 | `local` and `returned` from F3 | `local` accepted, and `returned` owns `p` |
| F3 | A binding inside an `if` | The later use reports a moved value |
| F3, D9 | An escaping parameter the body only reads, stored where the type leaves ownership open | Inferred as `&'static` |
| L1 | `make` and `remember` from L1 | `make` accepted, and the write in `remember` rejected |
| L2 | `pick`, `wrong`, `either`, `unproven`, and `trusted` | As marked in L2 |
| L3 | `read`, `same`, and `cache` | The rendered signatures L3 lists |
| E4 | `local`, `moved`, and `stored` from E4 | As marked in E4 |
| E4 | A closure that moves a capture, called twice | The second call reports a moved value |
| E4, D10 | The three `setup` programs from D10 | The first rejected, the other two accepted |
| I1 | Convert `reset(x: Readonly<{a: {b: number}}>)` | `reset(x: &mut Readonly<{a: {b: number}}>)` |
| I1 | Convert a parameter typed `Readonly<{a: number}>` | `&Readonly<{a: number}>` |
| I1 | Convert `this: Readonly<T>` where `T` has a writable nested slot | Not `&self` |
| I2 | Convert a `number` parameter | `number`, with no borrow |
| I3 | Convert a method the analysis cannot settle | `&mut self` |
| I3 | Convert a function returning `{...opts}` | A borrow of `opts` |

D11's write-once cell gets its tests once its API is designed.

## Migration

The checking model replaces several passes the solver runs today. Each step
below is a separate PR. Every step also does two things for its own change:

- It updates the parts of `docs/08_mutability.md` and `docs/09_ownership.md`
  that its change makes true. `docs/README.md` says the pages describe the
  checker as it is, so the docs change with the code rather than ahead of it.
- It rewrites the tests, fixtures, and stdlib declarations its change breaks.
  The fixture-delta classifier in CI rates the resulting fixture changes for
  review.

The steps:

1. Write the conformance tests above against the current solver. A test the
   current solver fails is still written with its expected result, then
   disabled the way `CLAUDE.md` describes. A Go test body is wrapped in
   `/* */` under a `DISABLED until <step>` comment that names the step which
   enables it, and a fixture gets a `DISABLED` marker file. A failing test that
   no step below fixes becomes a known-bug entry, or a reason to change a rule.
2. Move owned mutability from types to bindings, as T1 states:
   - The parser accepts `mut` on a parameter binding, as in `fn f(mut p: T)`,
     and rejects `mut` in an owned type annotation, as T3 states.
   - The `mut T` annotations in tests, fixtures, and the stdlib become binding
     annotations. There are about 600 in the solver tests and 174 in the stdlib,
     most of them `...items: mut Array<T>` rest parameters.
   - The owned-mutable `RefType` with no lifetime leaves
     [soltype](../../internal/soltype/ref.go).
   - The owned-to-`mut` upgrade around `isUniquelyOwned` in
     [infer_decl.go](../../internal/solver/infer_decl.go) becomes an ordinary move checked by E3.
   - The printer and the LSP hover show a binding's mutability on its name, as
     `mut p: {x: number}`.
   - T9's widening reads the binding's mutability instead of the type's.
   - A parameter's binding is mutable only when written `mut`, as D13 decides.
     The checker stops inferring it from writes in the body.
3. Require every borrow to be written, as F4 states. The missing-`&` check
   extends from call arguments to every flow site and to field reads through a
   borrow. Each error carries the borrow its destination needs, `&` or
   `&mut`, the way `ImplicitBorrowArgError` in
   [borrow_exclusivity.go](../../internal/solver/borrow_exclusivity.go) does today. The same PR:
   - Adds an LSP quick fix that inserts that borrow. The published diagnostic
     carries the borrow and the range of the expression in its `data` field.
     `textDocumentCodeAction` in
     [text_document.go](../../cmd/lsp-server/text_document.go) returns a
     `quickfix` action whose edit inserts `&`, or `&mut` and a space, before the
     expression. It wraps the expression in parentheses unless the expression
     is an identifier, a field access, an index, or a call. `quickfix` joins
     the code action kinds the server advertises in
     [main.go](../../cmd/lsp-server/main.go), and tests in
     `text_document_test.go` cover `&`, `&mut`, and the parenthesized case.
   - Drops the move that `consumeAtGlobalWrite` in [moves.go](../../internal/solver/moves.go)
     records for a borrow, as D8 decides.
4. Change parameter inference to follow F3 and D9. An unannotated parameter
   that does not escape becomes a borrow. A call to an escaping one moves its
   argument. An escaping parameter that the body only reads becomes a
   `'static &` borrow where the destination's type leaves the ownership open.
   A parameter that does not escape becomes `&mut` when the body writes through
   it and `&` otherwise, as D13 decides.
5. Add function-type lifetimes, as D10 decides. The parser and the type system
   accept `&'a fn …` in a parameter, and E4 holds a passed closure's borrows
   for `'a`. A function parameter without a lifetime keeps its current
   meaning, so existing code keeps checking.
6. Update the declaration converter to follow I1 through I3. It keeps
   `Readonly<T>` as written, emits `&T` only when no slot reachable through
   `T` is writable, and fixes `classifyExplicitSignal` in
   [mutability.go](../../internal/dts_to_esc/mutability.go) to match. B4 has to
   be fixed first, since I1's `&mut Readonly<T>` depends on it.
7. Build the event lowering and the single control-flow pass behind a flag. Run
   it alongside the current checks and compare diagnostics across every test and
   fixture. Resolve each difference as a bug in the old checks or a change to the
   rules.
8. Remove the old checks one at a time once the new pass matches them. The
   checking model replaces these groups:
   - The per-site `consume*` functions in [moves.go](../../internal/solver/moves.go) and
     [module_level_moves.go](../../internal/solver/module_level_moves.go), replaced by F2 and
     F3. This includes `consumeOnceCallee`, replaced by E4, and the separate
     pass behind `ModuleLevelMoveError`, replaced by E1.
   - The alias-set transition check in [transitions.go](../../internal/solver/transitions.go),
     the alias tracker in [alias.go](../../internal/liveness/alias.go) and
     [alias_analysis.go](../../internal/liveness/alias_analysis.go), and the
     loan check in [borrow_exclusivity.go](../../internal/solver/borrow_exclusivity.go), all
     replaced by E2 and E3.
   - The frame-exit checks in [return_escape.go](../../internal/solver/return_escape.go) and
     [return_shared_paths.go](../../internal/solver/return_shared_paths.go), replaced by L1 and
     E2 at the return.
   - The separate escape logic in [return_escape.go](../../internal/solver/return_escape.go) and
     [borrow_flow.go](../../internal/solver/borrow_flow.go), and the owned-return rewriting in
     [owned_return.go](../../internal/solver/owned_return.go), replaced by the lifetime solver
     answering F3.
   - The plumbing that keeps the engines from reporting one error twice, such
     as `resolvePhaseTransitions` and `reportedMixedOwnership`.

   These stay, since they enforce rules the checking model does not cover:
   - T8's `MixedOwnershipError` in [infer_expr.go](../../internal/solver/infer_expr.go).
   - The L2 checks in [lifetime_signature.go](../../internal/solver/lifetime_signature.go),
     [lifetime_rigid.go](../../internal/solver/lifetime_rigid.go), and
     [lifetime_undeclared.go](../../internal/solver/lifetime_undeclared.go), and the L3
     rendering in [lifetime_coalesce.go](../../internal/solver/lifetime_coalesce.go).
   - The type rules in [constrain.go](../../internal/solver/constrain.go).
9. Design and add the write-once cell from D11. It does not depend on the steps
   above and can land any time after step 2.

Each known bug closes at a step:

| Bug | Closed by | Soundness hole |
|---|---|---|
| B1 | Step 3, since F4 rejects the unwritten borrow | No |
| B2 | Step 7, or sooner by its own fix to the current checker | Yes |
| B3 | Step 3, since F4 rejects the annotation-only borrow | Yes |
| B4 | Its own fix, which step 6 needs first | No |
| B5 | Step 4 | Yes |
| B6 | Step 4 | No |
| B7 | Step 5 | Yes |
| B8 | Step 7, since the dataflow pass records a copied borrow as a holder | Yes |

Steps 3 through 5 close B3, B5, and B7 in the current checker, so those holes
close before the rewrite in step 7. B2 already has its own fix queued, so it
does not need to wait for step 7 either.

The migration is finished when every conformance test is enabled and passing,
and the groups step 8 lists are gone. The point of the work is a simpler
solver, so record two measurements before step 1 and after step 8:

- The line count of the ownership, lifetime, and mutability files. It is about
  12,000 lines across about 25 files today.
- The `gocognit` cognitive complexity total for `internal/solver`. It is
  about 10,800 today.
