# Ownership rules

## Status and scope

This document states the rules that decide which programs the
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

Where this document and those disagree, this document wins. The section
[Divergences from the docs](#divergences-from-the-docs) lists the places where
the docs still disagree with the checker.

The section [Decisions](#decisions) records the design questions the rules left
open and how each was decided. All of them are decided, and the checker
implements most of them. Each entry with checker work left says what remains.
D17 also lists the points about function lifetimes that are still open.

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
function type. A function may hold borrows, and its type's lifetime bounds them.
The lifetime is written as a prefix, as in `'a fn () -> undefined`, and E4 and
D17 state how it is checked.

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

The rule applies to every owned destination, whatever its binding's
mutability. That covers a binding, a reassignment, a parameter, and a return
value. A `val` binding or an immutable parameter can move its value on into a
`val mut` binding, which then writes the field. A caller can do the same with a
returned value. A parameter's binding mutability is not part of the signature
either, so a call through a function value could not see it. In the program
below, both calls report
`readonly field 'a' cannot satisfy a writable field requirement`:

```esc
fn sink(mut o: {a: number}) { o.a = 5 }
fn move(o: {a: number}) {
    val mut m = o
    m.a = 5
}
fn f(obj: {readonly a: number}, other: {readonly a: number}) {
    sink(obj)
    move(other)
}
```

A `&` borrow can neither write the field nor move the value, so a `readonly`
field still satisfies a writable field behind one. With `obj` above,
`read(&obj)` is accepted for `fn read(o: &{a: number})`.

**T6. A borrow cannot fill an owned destination.** A value of type `&T` or
`&mut T` does not satisfy an owned `T`, since the destination would
claim ownership of storage owned elsewhere. The same holds for a
reference-shaped field read through a borrow. Reading it does not move it out,
so it cannot fill an owned destination, and F4 requires the borrow of it to be
written as `&r.f` or `&mut r.f`. With `r: &{x: number}`, the binding
`val q: {x: number} = r` reports `cannot use borrowed &object as owned object`,
and `val q: &{x: number} = r` is accepted. A literal element with no expected
type takes its source's type by F1, so `val o = {f: r}` copies `r` into a
borrow-typed field and T6 has no owned destination to reject.

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
| Literal element | `{f: p}`, `[p]` | The matching field or element type of the literal's expected type, as in `val o: {f: &T} = {f: &p}`. When the literal has no expected type, the source's type, so `{f: r}` with `r: &T` holds a borrow and `{f: p}` with an owned `p` is owned |
| Spread | `{...p}` | Owned |
| Destructuring or `match` arm | `val {f} = p` | Per part, as for a binding. A pattern over a borrow binds borrows, by F4 |
| Argument | `f(p)` | The parameter's type. An unannotated parameter is decided by F3 |
| Receiver | `p.m()` | The method's `self` type |
| `return` | `return p` | Owned, unless the return type is a borrow |
| Module-level store | `cache = p` | Owned, with a `'static` region |
| Closure capture | `fn () { return p.x }` | Decided by where the closure flows, by F3 and D17 |

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

A primitive or a promise has no borrow form, so its destination is always
bare. A function has no borrow form either. Its type's lifetime, written as in
`'a fn`, bounds the borrows the function holds, and E4 states how it passes. A
generic `&T` whose `T` is a primitive or a promise
stands for `T`. With `fn f<T>(x: &T)`, the call
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
- A closure that is returned owns the locals of the returning body that it
  captures. A closure returned through a local, such as `return f` after
  `val f = fn () { … }`, takes its captures at the return, so the body can use
  them until then. A parameter, or a borrow of something outside the body, is
  not the body's to give away, so the closure still borrows it. A closure that
  stays in the body or is stored into a caller-owned object borrows what it
  captures.
- A closure stored into module-level state, or passed to any other `'static`
  slot, is kept for the rest of the program. Such a slot holds no borrow of a
  local, so the closure owns the locals of the body it captures, as it does
  when it is returned. A later use of one of those locals is an error. A
  closure that captures a parameter or an outside borrow is rejected at the
  store. D17 decides both cases. A function is a value type, so the store
  copies the closure, and the case above for module-level state does not by
  itself make its captures owned.

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
A borrow of a place that already has a borrow of the same kind outliving the
current frame takes that borrow's lifetime, as D16 decides. For two immutable
borrows, E2 rejects every write to the place while the first borrow is live, so
the second borrow reads a value that does not change for as long as either can
read it. Two mutable borrows may be live together under E2 already.

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

An unannotated function whose returned borrow has a lifetime that ends with the
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
is used as an immutable path. A closure kept in a slot with lifetime `'a` may
run during any later call while `'a` is live, so each such call uses its
borrows. A closure that moves one of its captures moves it at each call, so by
E1 a second call is rejected.

A module-level function captures each module-level binding its body reads or
writes, and each one that a module-level function it calls reaches. A call of it
uses those bindings the way a closure call uses its captures. With
`var mut cfg = {x: 0}` and `fn bump() { cfg.x = cfg.x + 1 }`, the call in
`val r = &cfg; bump(); return r.x` writes `cfg` while `r` is live, so E2 rejects
it. An `await` of the call runs it before the body resumes, so `await bump()`
is rejected the same way.

A function type's lifetime bounds the borrows the function holds, the same way
a `&'a T` borrow's lifetime bounds how long the borrow lives. It is written as a
prefix, as in `'a fn () -> undefined`, and a slot keeps the function only while
`'a` is live. A parameter written `f: fn () -> undefined` has an elided lifetime
that ends with the call, so the callee only calls `f` during the call, and a
closure passed to it holds its borrows for that call alone. A parameter written
`f: 'static fn () -> undefined` keeps the function for the rest of the program,
so it accepts only a function that borrows nothing shorter-lived than the
program. A function is still a value type, so F2 copies it into either kind of
parameter, and the call needs no written `&`. The lifetime does not borrow the
function itself. It bounds the borrows the function holds. D17 decides the rule,
including how an unwritten lifetime is elided in each position.

A closure's lifetime comes from what it captures. A closure that captures
nothing, or only reads value types from `val` bindings, copies what it reads and
borrows nothing, so its lifetime is `'static`. A closure that captures an object
local, or reads or writes a captured `var`, borrows it, as F3 states. It
borrows a `var` it only reads, since it must see the binding's later writes.

In the program below, `keep` may hold on to its callback forever and `run` only
calls its callback during the call. `handlers` is a module-level binding, so its
elided element lifetime is `'static`, and it keeps what it holds as `keep` does.
None of the calls writes a `&`, since a function is a value type.

```esc
declare fn keep(cb: 'static fn () -> undefined) -> undefined
declare fn run(cb: fn () -> undefined) -> undefined
declare fn render(view: &{n: number}) -> undefined

var mut handlers: Array<fn () -> undefined> = []

fn setup() {
    val msg = "hello"
    val greet = fn () { console.log(msg) }
    keep(greet)                 // accepted, since greet copies msg and borrows nothing

    val cfg = {msg: "hello"}
    val show = fn () { console.log(cfg.msg) }
    run(show)                   // accepted, since run only calls show during the call
    keep(show)                  // accepted, since cfg has no later use, so show owns it

    val mut count = {n: 0}
    val inc = fn () { count.n = count.n + 1 }
    run(inc)                    // accepted
    keep(inc)                   // accepted, and keep takes count over
    render(&count)              // error: 'count' is used after the 'cb' that 'keep' retains took it over for 'static

    var n = 0
    handlers.push(fn () { n = n + 1 })    // accepted, since n has no later use
}
```

`keep(show)` is accepted because `show` takes ownership of `cfg`. Every local of
`setup` that `show` borrows is dead after the call, so nothing in `setup` can
observe what the closure does with it. `keep(inc)` takes `count` over the same
way, so the error is at `render(&count)`, which reads a value the kept closure
now owns and may write at any time. D17 states the rule.

To keep a closure that borrows a live local, give the slot a lifetime that the
local outlives. A class with a lifetime parameter does this. A `Button<'a>`
holds its handler for `'a`, which lasts while the binding that holds the
`Button` is live. `page` below uses the `render` declared above:

```esc
class Button<'a> {
    handler: 'a fn () -> undefined,
    click(&self) -> undefined { self.handler() },
}

fn page() {
    val mut count = {n: 0}
    val b = Button(fn () { count.n = count.n + 1 })
    render(&count)    // error: this call may run the 'handler' that 'Button' retains, which writes 'count' while it is borrowed as immutable
    b.click()
}
```

Moving `b.click()` above `render(&count)` makes `page` check, since `b` is dead
at the render.

E4 adds no column to the exclusivity table, because it does not decide whether
an operation on `p` is allowed. It decides when a closure's captures count as
used, and so which row applies. Between calls, the two closure rows apply. They
allow writes and new borrows of `p`, and they reject a move, since the closure
holds its borrows until its last call. During a call, the borrow row for the
capture's kind applies for that statement.

In `local` below, the write to `p` between the two calls is allowed. In
`moved`, `get` still holds its borrow of `p` for its later call, so the move
is rejected. In `page` above, `b` holds the closure for `'a`, so any call while
`b` is live may run it, and `render(&count)` conflicts with the write the
closure may perform.

```esc
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
  takes its source's type, so F2 moves an owned source and copies a borrow. A
  source whose type is a union moves when the union has an owned
  reference-shaped member, as a binding does, and is copied otherwise. A spread
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
| F3 owned when it escapes | Every parameter type is written. A closure captures by reference unless it is marked `move` | Differs | Escalier infers an unannotated parameter's ownership, and a kept closure's moves of locals with no later use. Rust requires both to be written |
| F3 binding always moves | `let q = p` moves `p` whatever `q`'s scope | Same | |
| F4 borrows are written | `&` and `&mut` are written to borrow, except on a method receiver, which auto-borrows. Pattern binding modes bind borrows when a pattern destructures a borrow | Same | |
| L1 lifetime is liveness | A borrow cannot outlive its referent, and storing one in a `static` requires `'static` | Differs | A borrow may outlive its owner, because the garbage collector keeps the value alive. Lifetimes only bound how long a path is live for E2 |
| L2 signature states the body's relations | Lifetimes are inferred only inside a body, never in a signature. A declared bound is an assumption the body may use | Differs | Escalier infers signature lifetimes for an unannotated function and treats a declared bound as an obligation. See D4 |
| L3 render only connecting lifetimes | Elision follows fixed syntactic rules, such as one input lifetime flowing to every output | Differs | Escalier elides by whether a lifetime connects anything, which also covers signatures Rust's elision rules reject |
| E1 no use after move | Flow-sensitive per branch, with partial moves, and reassignment re-initializes | Same | Reassignment does not yet re-initialize in Escalier. See [#1269](https://github.com/escalier-lang/escalier/issues/1269) |
| E2 no immutable and mutable path live together | Aliasing XOR mutability over non-lexical lifetimes: one `&mut`, or any number of `&` | Differs | Escalier allows several live mutable paths at once. The target is single-threaded and garbage collected, so the only hazard left is an immutable path observing a write |
| E3 no move while borrowed | Rejected with E0505 | Same | |
| E4 closure calls use its borrows | A closure holds its captured borrows from creation to its last use | Differs | Escalier counts the borrows only at each call, so a write to a captured value between two calls is accepted. Rust rejects it |
| D1 no move out of module-level state | Moving out of a `static` is rejected with E0507 | Same | |
| D2 a moving closure is called once | `FnOnce` | Same | |
| T8 branches agree on ownership | `if` branches must have one type, so `T` and `&T` do not join | Same | |
| D4 declared bounds are obligations | Declared bounds are assumptions | Differs | Follows from L3 |
| D17 function lifetimes | A closure's lifetime is a bound on its trait object, as in `Box<dyn Fn() + 'a>`, and a boxed `dyn Fn()` defaults to `'static`. A closure kept past its locals' scope must be written `move` | Differs | Escalier writes the lifetime on the `fn` type and elides it by position, as it does for `&`. It moves a dead local into a kept closure without a written `move` |
| D5 `readonly` initialized once | A struct literal initializes every field at once | No equivalent | |
| T10 getter and setter receivers | No getters or setters | No equivalent | |
| Move to immutable and move to mutable | `let settled = v; let mut editable = settled;` moves between bindings of different mutability | Same | `docs/09_ownership.md` says Rust has no equivalent. The moves are the same, but interior mutability keeps Rust's move to immutable from being deep |

The differences come from four sources:

- **The runtime.** Single-threaded JavaScript with a garbage collector removes
  data races and dangling pointers. That permits aliased `&mut` in F2 and E2,
  borrows that outlive their owner in L1, the per-call closure borrows in E4,
  and the inferred move of dead locals into a kept closure in D17.
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

Each entry records a design question the rules left open and how it was
decided. All 18 are decided, and D17 also lists the points about function
lifetimes that are still open. An entry the checker implements states its rule
first, then shows it in an example. An entry the checker does not implement
keeps its problem statement and options. When checker work remains for an
entry, the entry ends with a note on that work and the issue that tracks it.

### D1. Moving out of a module-level binding

A move out of a module-level binding or an import is rejected, as E1 states. A
module-level binding's region is `'static`, so it outlives every call. A move
would consume it for every later caller.

```esc
val cfg = {x: 1}

fn take(p: {x: number}) -> number { return p.x }

fn go() -> number {
    return take(cfg)    // error: cannot move module-level value 'cfg' inside a function. Borrow it with `&cfg`
}
```

`ModuleLevelMoveError` in
[module_level_moves.go](../../internal/solver/module_level_moves.go) reports it.

### D2. Closures that move a capture can be called once

A closure that moves one of its captures moves it at each call, as E4 states.
By E1, a second call would use a moved place, so the first call consumes the
closure and the second is rejected:

```esc
fn f() {
    val p = {x: 1}
    val g = fn () {
        val q = p
        return q.x
    }
    g()
    g()    // error: use of moved value 'g'
}
```

The checker enforces this only inside the body that creates the closure. A
function type cannot say that a function may be called once, so a caller of a
returned closure, or a callee it is passed to, may call it twice.
[#2581](https://github.com/escalier-lang/escalier/issues/2581) tracks the gap.

### D3. Branches that mix owned and borrowed values

The branches of a conditional produce all owned values or all borrows, as T8
states. A branch with an owned value borrows it to match the others. Joining the
branches to a borrow instead would need an owner for the owned branch that
outlives the expression, and a fresh value has none.

```esc
fn pick(cond: boolean, q: &{x: number}) -> number {
    val p = {x: 1}
    val r = if cond { p } else { q }
    return r.x
}
```

The `if` reports
`a union or intersection mixes owned and borrowed members. Make ownership uniform first. Clone the borrowed member to own it, or borrow the owned member.`
Writing the first branch as `&p` is accepted. The check reports
`MixedOwnershipError` from [infer_expr.go](../../internal/solver/infer_expr.go).

### D4. Declared lifetime bounds are obligations

A declared lifetime bound `'a: 'b` is an obligation on the body, as L2 states.
The body must establish each bound it declares. Rust reads a declared bound the
other way, as an assumption the body may rely on and the caller must prove.
Escalier's choice follows from L3. A rendered signature has to match the
inferred one, so a bound the body does not establish would render a signature
that inference does not produce.

```esc
fn f<'a: 'b, 'b>(x: &'a {x: number}, y: &'b {x: number}) -> &'b {x: number} {
    return y
}
```

Nothing in the body makes `'a` outlive `'b`, so the declaration reports
`declared lifetime bound 'a: 'b is not satisfied; the body does not make 'a outlive 'b`.
`checkDeclaredLifetimeBounds` in
[infer_expr.go](../../internal/solver/infer_expr.go) runs the check.

### D5. A `readonly` field is initialized once in a constructor

A `readonly` field is initialized once, by its constructor, as T5 states. A
second write to it inside the constructor is rejected:

```esc
class C {
    readonly a: number,
    constructor(&mut self, a: number) {
        self.a = a
        self.a = a + 1    // error: cannot assign to 'a' because it is a readonly property
    },
}
```

The check lives in
[infer_class_ctor.go](../../internal/solver/infer_class_ctor.go).

### D6. Getter and setter receivers

A getter takes `&self` and a setter takes `&mut self`, as T10 states. A getter
only reads its instance, and a setter writes one field of it. Both are
declaration rules and do not interact with flow.

```esc
class C {
    x: number,
    get y(self) -> number { return self.x },
    set y(&self, v: number) {},
}
```

The getter reports
``Getter 'y' must borrow its receiver with `&self` or `&mut self`; reading through it leaves the instance in place.``
The setter reports
``Setter 'y' must declare a `&mut self` receiver; writing through it mutates the instance.``

The checker accepts a getter that takes `&mut self`. Rejecting it is tracked by
[#2287](https://github.com/escalier-lang/escalier/issues/2287).

### D7. Explicit `&` at a call site

Every borrow is written, at every flow site, as F4 states. An owned value that
fills a borrow destination is written `&p` or `&mut p`, whether the destination
is a parameter, a binding, a field, or a return. Each borrow and its mutability
are then visible where the borrow is taken.

```esc
fn read(p: &{x: number}) -> number { return p.x }

fn go() -> number {
    val p = {x: 1}
    val c: &{x: number} = p    // error: this value is borrowed by its destination, so write the borrow: `&`
    return read(p) + c.x       // error: this argument is borrowed by the callee, so write the borrow: `&`
}
```

`ImplicitBorrowArgError` and `ImplicitBorrowError` in
[borrow_exclusivity.go](../../internal/solver/borrow_exclusivity.go) report the
two errors. The LSP server offers a quick fix that inserts the borrow.

### D8. A global write moves a borrow

A borrow stored into module-level state is copied, as F2 states, so the source
stays usable. The stored borrow gets a `'static` lifetime by L1, and E2 then
keeps any conflicting path from forming for the rest of the program.

```esc
var cache: &{x: number} = &{x: 0}

fn keepIt(b: &{x: number}) -> number {
    cache = b
    return b.x       // accepted, since the store copied b
}

fn go() -> number {
    val mut opts = {x: 3}
    cache = &opts
    opts.x = 4       // error: cannot assign to 'opts.x' while it is borrowed as immutable
    return opts.x
}
```

A store of a `&mut` borrow into a module-level `&mut` binding reports
`cannot constrain immutable object <: mutable object`. The fix is tracked by
[#2249](https://github.com/escalier-lang/escalier/issues/2249).

### D9. Whether an escaping value moves or borrows

An escaping parameter that the body never writes is inferred as a `'static &`
borrow, and one the body writes is owned. The rule applies only where the
destination's type leaves the ownership open, such as a `Map` whose value type
is inferred, or a function the converter declares from JavaScript source. Where
the destination's type is owned, T6 already makes the parameter owned, as for
`store` in F3. A written annotation overrides the inference.

```esc
val mut seen = Map()

fn remember(value) { seen.set("last", value) }

fn go() -> number {
    val opts = {retries: 3}
    remember(&opts)
    return opts.retries    // accepted, since remember only borrows opts
}
```

`seen` settles to `Map<string, &'static {retries: number}>`.

L1 allows either answer for an escaping value. The destination can own it, or
it can be a `'static` borrow that leaves the source usable under E2. A converter
declaring `remember` from JavaScript has three choices:

| Declaration | Caller after `remember(...)` |
|---|---|
| `value: {retries: number}` | `opts` is moved, so any later use is an error |
| `value: &'static {retries: number}` | `opts` can still be read, but never written again |
| `value: &'static mut {retries: number}` | `opts` can still be written, but never borrowed as `&` again |

The `'static &` form fits a caller that keeps reading the value, which is the
common case for a memoization key or a configuration object. A body that writes
the stored value needs one of the other two forms.

`markOpenEscapeParams` in [param_escape.go](../../internal/solver/param_escape.go)
applies the rule. Rendering `remember` with its `'static &` parameter is tracked
by [#2352](https://github.com/escalier-lang/escalier/issues/2352).

### D10. Recording that a callback is not retained

A function type's lifetime records how long the callee may keep the function,
and so how long the closure's captures stay borrowed. E4 states how a call
reads it. A parameter whose `fn` type has no written lifetime is only called
during the call, as D17 decides. A parameter written `'static fn` may be kept
for the rest of the program.

`onClick` below keeps its handler for the rest of the program, so the handler
takes ownership of `count`, which it borrows. `render` reads `count` after the
call, a value the handler now owns and may change on any click, so the read is
rejected:

```esc
declare fn onClick(handler: 'static fn () -> undefined) -> undefined
declare fn render(view: &{n: number}) -> undefined

fn setup() {
    val mut count = {n: 0}
    onClick(fn () { count.n = count.n + 1 })
    render(&count)    // error: 'count' is used after the 'handler' that 'onClick' retains took it over for 'static
}
```

There are two ways to write `setup` so that it checks. The first moves every use
of `count` into the handler. `setup` never uses `count` again, so the handler
takes ownership of it, and the borrow `render` takes inside the closure lasts
only for one call:

```esc
fn setup() {
    val mut count = {n: 0}
    onClick(fn () {
        count.n = count.n + 1
        render(&count)    // accepted
    })
}
```

The second keeps the handler for a shorter lifetime. A callee that keeps its
handler only while some holder is live declares that lifetime, as `Button<'a>`
in E4 does. `setup` may then borrow `count` once the holder is dead.

The alternative was a marker on a parameter saying the callee does not keep it,
with every unmarked function parameter read as retained. The lifetime form
reuses L1 and E4 instead of adding a rule.

### D11. Interior mutability for state no reader can observe

T3 has no escape hatch, so a value in a `val` binding or behind a `&` never
changes. Rust's `Cell` and `RefCell` exist mostly because `&mut` is unique
there, and Escalier does not need them for that. Several mutable paths may be
live at once, the garbage collector keeps shared graphs alive, and a `&mut`
field puts one mutable part inside an immutable value, as T3 allows.

Three cases still push against the rule:

- A cache or a lazy field. A `&self` method that computes an expensive result
  once and keeps it, such as a memoized hash, must be `&mut self`, and that
  requirement spreads to every caller.
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

Without a cell, the cache case looks like this. `wordCount` writes its cache,
so it needs `&mut self`, and a caller holding only `&Document` cannot call it:

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
designed. Counters and the interop methods stay on `&mut self`.

The cell's design is tracked by
[#2275](https://github.com/escalier-lang/escalier/issues/2275), and its
implementation by [#2276](https://github.com/escalier-lang/escalier/issues/2276).

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

- Accept it silently.
- Warn when the binding's type has no writable slot, meaning a value type, a
  `&` borrow, a `&mut` borrow, or an owned value whose layers hold nothing a
  write can replace. Warn the same way on a `&mut` type or a `&mut`
  expression whose referent has no writable slot, and suggest `&`. Each check
  needs only one type, and such a `mut` can never matter.
- Warn when a `mut` binding is never written and never borrowed as `&mut`, as
  Rust's `unused_mut` lint does. This covers the value-type case and also an
  owned binding the body only reads, but it needs the body's write analysis.

Recommendation: warn on a type with no writable slot first and add the
unused-binding warning later. Both are warnings rather than errors, since the
program means the same with or without the `mut`.

Decided: accepted. A `mut` binding whose type has no writable slot reports a
warning, and so does a `&mut` type or expression whose referent has none. A
warning on any `mut` binding the body never writes or borrows as `&mut` is a
later addition.

The checker reports neither warning. The first is tracked by
[#2304](https://github.com/escalier-lang/escalier/issues/2304), and the second
by [#2305](https://github.com/escalier-lang/escalier/issues/2305).

### D13. Binding mutability is written, not inferred

A binding is mutable only when it is written `mut`, parameters included, as T2
states. Inference never makes a binding mutable. A write through an immutable
binding is rejected, and the message names the `mut` to write:

```esc
var sink = {x: 0}

fn f(p) {
    p.x = 1    // error: cannot mutate 'p' because its binding is immutable; declare it as `mut p`
    sink = p
}
```

Written as `fn f(mut p)`, the same function is accepted and infers
`fn (p: {x: number}) -> undefined`.

A binding's mutability is not part of the signature, since `fn f(mut p: T)`
has the same type as `fn f(p: T)`. Inferring it would give the caller nothing,
and it would hide from a reader of the signature whether the body changes its
own value.

A parameter's type is different. Whether an unannotated parameter is owned,
`&T`, or `&mut T` decides what the caller writes, so it stays inferred. F3
makes a parameter that does not escape a borrow. A body that writes through
such a parameter makes it `&mut`, and one that only reads it makes it `&`.
`fn r(p) { return p.x }` infers `fn <T0, 'a>(p: &'a {x: T0}) -> &'a T0`.

The checker infers the `&` case only. A parameter that does not escape and that
the body writes through is inferred as owned, so `fn w(p) { p.x = 1 }` asks for
`mut p`. [#2570](https://github.com/escalier-lang/escalier/issues/2570) tracks the `&mut` case.

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

The checker reports the write as
``cannot mutate 'start' because its binding is immutable; declare it as `mut start` ``.
Writing `line.start.x = 1` is accepted, so the pattern takes away a write the
borrow allows.

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

Decided: accepted. F4 states it.

The checker change is tracked by
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

Decided: accepted. T7 states it.

The checker change is tracked by
[#2324](https://github.com/escalier-lang/escalier/issues/2324).

### D16. A returned borrow of a value nothing else reaches

A returned borrow is owned when nothing outside the call reaches the value, as
L1 and L3 state. The returned borrow's lifetime records whether anything else
reaches the value, given five rules:

1. An immutable borrow of a place that already has an immutable borrow
   outliving the current frame takes that borrow's lifetime.
2. A returned `&` is owned when its lifetime ends with the callee's frame,
   meaning it is not tied to a parameter, a capture, or `'static`.
3. A `&mut` of a place that already has a `&mut` outliving the current frame
   takes that borrow's lifetime.
4. A returned `&mut` is owned when its lifetime ends with the callee's frame, as
   the second rule states for a returned `&`.
5. A borrow nested in a returned value is owned when its lifetime ends with the
   callee's frame and no other borrow in the returned value reaches an
   overlapping place. A borrow reaches the place it borrows and every place
   the borrows in its referent reach.

Without the first rule, `h` below would return an owned value that `cache`
still reads, and `k` could write it:

```esc
var cache: &{x: number} = &{x: 0}

fn h() {
    val b = {x: 1}
    cache = &b
    return &b        // h: fn () -> &'static {x: 1}
}

fn k() {
    val mut r = h()
    r.x = 5          // rejected, since r is a borrow of the value cache reads
}
```

A lifetime says how long one borrow stays usable. It does not say whether
another path reaches the same value, so the two borrows of `b` could carry
different lifetimes. E2 closes that gap. While one immutable borrow of `b` is
live, no write to `b` can follow, so any other immutable borrow of `b` can live
as long as that one does. The first rule gives the returned `&b` the `'static`
lifetime of the borrow in `cache`.

The third rule adds no rejection. While a `&mut` of a place outlives the frame,
E2 already rejects every `&` of the place, and several `&mut` paths may be
live together. An outliving `&` of the place makes the `&mut` itself an E2
error, so no rule ties a borrow to one of the other kind.

Inferring every returned `&` as a borrow would also be sound. But `return &b`
for a local that nothing else reaches would then hand the caller a borrow it
could safely own.

For a returned call result, the answer follows the callee's signature:

- `return lookup(&b)` with `lookup<'a>(p: &'a {x: number}) -> &'a {x: number}`
  stays a borrow. The `'a` result may be a `'static` value rather than `b`, and
  owning it would alias that value.
- `return make(&b)` with `make(p: &{x: number}) -> &{x: number}` is owned. The
  result ties to nothing in the signature, so its lifetime ends with the
  caller's frame. A body that returns `p` under that signature is rejected by
  L2.
- A function that returns a borrow of module-level state renders its result as
  `&'static`, since L3 always renders `'static`.

A result that ties to no parameter comes from somewhere else. Either the
function built it, or the result borrows something that already exists outside
the call. Each comment below gives the rendered signature:

```esc
var cfg = {x: 0}
var cache: &{x: number} = &{x: 0}

fn getCfg(p: &{x: number}) {
    return &cfg
}
// fn (p: &{x: number}) -> &'static {x: number}, since cfg is module-level

fn stash(p: &{x: number}) {
    val q = {x: 1}
    cache = &q
    return &q
}
// fn (p: &{x: number}) -> &'static {x: 1}, since cache also reads q
```

In both functions, something else still reads the returned value after the
call. In `getCfg` it is `cfg` itself, and in `stash` it is `cache`.

The rules read the frame rather than the module, so they hold when the same
functions are closures. Here `cfg` and `cache` are locals of `outer`, and
nothing is `'static`:

```esc
fn outer() {
    val cfg = {x: 0}
    var cache: &{x: number} = &{x: 0}

    val getCfg = fn (p: &{x: number}) {
        return &cfg
    }
    val stash = fn (p: &{x: number}) {
        val q = {x: 1}
        cache = &q
        return &q
    }

    val r = stash(&cfg)
    return r
}
// fn () -> {x: 1}
```

- `getCfg` returns a borrow tied to its capture of `cfg`, so its result is a
  borrow. The lifetime belongs to `outer`'s frame, which outlives the closure's
  call.
- `stash` returns a borrow that takes the lifetime of the borrow in `cache`
  under the first rule. That lifetime belongs to `outer`'s frame, so `r` is a
  borrow inside `outer`, and a write through `r` is rejected.
- `outer` returns `r`, whose lifetime ends with `outer`'s frame. Once `outer`
  returns, `cache` is gone and nothing else reaches `q`, so the second rule
  makes the return owned. If a closure that captures `cache` escaped `outer`,
  for instance by being returned or stored in module-level state, the lifetime
  would reach past `outer` and the return would stay a borrow.

The last three rules cover a returned `&mut` and a borrow nested in a returned
value. Each comment below gives the rendered signature:

```esc
declare fn keep(x: &'static mut {x: number}) -> undefined

fn kept() {
    val mut b = {x: 1}
    keep(&mut b)
    return &mut b
}
// fn () -> &'static mut {x: number}, since keep also writes b

fn keptField() {
    val mut b = {x: 1}
    keep(&mut b)
    return {p: &mut b}
}
// fn () -> {p: &'static mut {x: number}}, since keep also writes b

fn fresh() {
    val mut b = {x: 1}
    return {p: &mut b}
}
// fn () -> {p: {x: number}}, since nothing outside the call reaches b

fn twice() {
    val mut b = {x: 1}
    return {l: &mut b, r: &mut b}
}
// fn () -> {l: &mut {x: number}, r: &mut {x: number}}, since both fields reach b
```

- In `kept`, the third rule gives the returned `&mut b` the `'static` lifetime
  of the borrow `keep` holds. The fourth rule then leaves the return a borrow,
  so a caller cannot own a value `keep` can still write.
- In `keptField`, the borrow sits inside the returned literal. The literal
  itself is owned, and the fifth rule leaves its field a borrow for the same
  reason.
- In `fresh`, the field's lifetime ends with the frame and no other borrow
  reaches `b`, so the caller owns both the literal and the value in `p`.
- In `twice`, both fields reach `b`. An owned value has one owner, so owning
  either field would hide the path through the other. Both stay borrows.

A bodyless signature is taken at its word, so the declaration converter must
render `'static` for any returned borrow it cannot vouch for.

[lifetime_frame.go](../../internal/solver/lifetime_frame.go) applies the rules.

### D17. Function lifetimes

A function type's lifetime bounds the borrows the function holds, the way a
`&'a T` borrow's lifetime bounds how long the borrow lives. Six rules decide
how the lifetime is written, elided, inferred, and checked:

1. Any `fn` type may hold borrows, in every position. Its lifetime is written as
   a prefix, as in `'a fn () -> undefined` or `'static fn () -> undefined`. It
   needs writing only where the elided one is not the one wanted. A function
   type takes no
   `&`, so `&fn () -> undefined` is rejected with
   `Unsupported: borrow of a non-borrowable type`. A function is still a value
   type, so F2 copies it into a slot, and passing one needs no written `&`.
2. A `fn` type written with no lifetime takes one by its position, the way an
   elided `&T` borrow does:
   - In a parameter, the lifetime is fresh and ends with the call, unless the
     signature ties it to the result. Such a parameter is *call-only*: the
     body may call it during the call, but may not store it where it outlives
     the call, pass it to a `'static` slot, or read it in an `async` or
     generator body that runs after the call returns. The one exception is a
     result whose lifetime the signature ties to the parameter's, as the next
     bullet's elision does. The body may return the parameter, or a closure
     that holds it, there.
   - In a return type, the lifetime depends on the signature's input
     lifetimes. An input lifetime is one that the receiver, a parameter, or a
     `<'a>` list introduces. When the signature has one, the return lifetime
     is fresh and tied to the input lifetimes, and the body's returns bound
     it. A signature with no input lifetime has nothing a returned function
     could borrow from, so its elided return lifetime is `'static`.
   - In a module-level binding's annotation, a class field, an interface, or a
     type alias, the lifetime is `'static`. So is a field or an element type
     inside one of those. A script's top level counts as module level.
   - In a binding annotation inside a body, the lifetime is fresh, as an elided
     `&` is there.
3. A `'static fn` slot holds only a function that borrows nothing shorter-lived
   than the program. A written `'static` and one that rule 2 elides mean the
   same. A module-level function fits, and so does a closure that
   captures only module-level bindings. A closure that borrows a local is
   rejected, unless rule 5 applies.
4. A closure's lifetime comes from what it captures. The checker infers each
   captured variable's capture mode from the body:
   - A value type read from a `val` binding or a parameter is copied, and adds
     no borrow.
   - A read of any other capture borrows it immutably. A captured `var` is
     borrowed even when it holds a value type, since the closure must see the
     binding's later writes.
   - A write through a capture, or an assignment to a captured `var`, borrows it
     mutably.
   - A call of a captured function holds that function's borrows.

   A closure that borrows nothing is `'static`. There is no capture syntax
   other than `move fn` in rule 6.
5. A kept closure takes ownership of the body's locals it borrows. This rule
   extends F3's returned closure to every destination that keeps a closure
   past the body. Such a destination is a `return` or a `'static` slot. A `'static`
   slot is a `'static fn` parameter, a module-level binding, a `'static`
   field, or an element of a module-level array. When a closure flows into one
   of them, it owns the `val` and `var` locals of the enclosing body that it
   borrows, instead of borrowing them. The store moves each of those locals
   into the closure:
   - A later read, write, move or borrow of one reports that it is used after
     the slot took it over, at the later use. An example is `'count' is used
     after the 'handler' that 'onClick' retains took it over for 'static`.
   - A borrow of one that is still live at the store reports `cannot move 'p'
     while it is borrowed`.
   - A parameter, or a borrow of something outside the body, is not the
     body's to give away, so the closure keeps borrowing it. At a `return`,
     the borrow ties the result to the parameter's lifetime, as F3 and `wrap`
     below show. A `'static` slot rejects such a closure at the store, with a
     message such as `` this closure borrows `p`, but `keep` keeps it for
     'static; pass a function that borrows nothing ``.
6. `move fn` makes a closure own its captures from where it is written. A
   captured value type is still copied, and a captured borrow stays a borrow.

Rule 5 is where Escalier departs from Rust. Rust frees a local at the end of
its scope, so a closure that outlives the scope must be written `move` to own
what it captures. Escalier compiles to JavaScript, where a
captured local is never freed while a closure can reach it. The borrow rules
here protect against aliasing and against use after a move, not against
deallocation. The checker infers the move that Rust asks the user to write,
and reports any later use of a moved local, so nothing in the body can alias
what the closure owns.

The program below shows rule 2 in three positions. Each comment names the
lifetime of the `fn` type on its line or the line below:

```esc
// 'static, since sink is a module-level binding
var sink: fn () -> undefined = fn () {}

// the elided return lifetime is tied to g's, so the closure may hold g
fn wrap(g: fn () -> undefined) -> fn () -> undefined {
    // fresh, so the local may hold g during the call
    val k: fn () -> undefined = g
    k()
    return fn () { g() }
}
// wrap: fn <'a>(g: 'a fn () -> undefined) -> 'a fn () -> undefined

fn stash(g: fn () -> undefined) {
    sink = g    // error: 'g' may borrow values that live only during the call, but it is stored where it lives for the whole program; write `g: 'static fn () -> undefined` to keep it
}
```

E4's `setup` example shows rules 3 through 5. `keep(show)` and `keep(inc)` are
both accepted by rule 5. The later `render(&count)` is rejected because
`keep` took `count` over.

An unannotated parameter that the body only calls is call-only, so
`fn f(g) { return g() }` infers `fn <T0>(g: fn () -> T0) -> T0`. One the body
stores into a `'static` slot infers a `'static fn` parameter.

Each error follows the shape of Rust's. It names the value, what the value
borrows, what keeps the value and for how long, and the fix. Two examples:

- `` 'g' may borrow values that live only during the call, but it is kept after the call returns; write `g: 'static fn () -> undefined` to keep it ``
- `` `show` borrows `cfg`, but `keep` keeps it for 'static; `` followed by the
  fix.

The wording of each fix may change. The shape stays.

A rendered function type shows its lifetime the way L3 renders a borrow's:

- A function with no lifetime renders as `'static fn` inside a parameter list,
  since a bare `fn` there reads back as call-only. Elsewhere it renders bare.
- A lifetime that connects an input to an output is named, as a `&T` borrow's
  is. `wrap` above renders as
  `fn <'a>(g: 'a fn () -> undefined) -> 'a fn () -> undefined`.
- A lifetime that connects nothing is elided.

Whether to also drop a connected output lifetime that rule 2 would recover is
open. Under rule 2, `fn (g: fn () -> undefined) -> fn () -> undefined` reads
back as the same type as `wrap`'s rendered signature above.

The rules above do not settle three cases. The checker behaves as follows in
each, and each item says whether that behaviour is decided or open:

- A function type in an object type written inline in a parameter is
  call-only, since it takes the parameter's elision. The same field in a named
  interface is `'static`. So `mount(opts: {cb: fn () -> undefined})` accepts a
  closure that borrows a local, and `mount(opts: Opts)` with
  `interface Opts { cb: fn () -> undefined }` rejects it. The difference is
  surprising, and whether to keep it is open.
- An unannotated `var` inside a body that starts with a closure that borrows
  nothing gets a `'static` function type. With `var g = fn () {}` in a body,
  a later `g = fn () { p.x = 2 }` over a local `p` is then rejected with
  `` this closure borrows `p`, but `g` has the type of a closure that borrows nothing; annotate `g` with a function type to let it hold one that borrows ``.
  A written type such as `var g: fn () -> undefined` in the same body is
  fresh by rule 2, so it holds either closure. At module level the written
  type is `'static` by rule 2, so it rejects the borrowing closure too. This is decided. The binding's type comes from its
  initializer, as it does for any binding with no written type.
- A conditional type's `extends` test and its `infer` patterns ignore function
  lifetimes. Whether they should compare them is open.

The checker change is tracked by
[#2342](https://github.com/escalier-lang/escalier/issues/2342),
[#2553](https://github.com/escalier-lang/escalier/issues/2553), and
[#2554](https://github.com/escalier-lang/escalier/issues/2554). Its
implementation is in [#2550](https://github.com/escalier-lang/escalier/pull/2550),
in `func_elision.go`, `closure_captures.go`, and `lifetime_call_only.go` under
`internal/solver`. These parts are not implemented:

- `move fn` from rule 6, tracked by
  [#2585](https://github.com/escalier-lang/escalier/issues/2585).
- Marking the `.d.ts` callbacks a callee keeps as `'static fn`, as I2 states.

These gaps remain:

- A borrow of a local passed directly to a `'static` parameter, as
  `keepRef(&cfg)` with `keepRef(p: &'static {msg: string})`, is accepted.
  [#2584](https://github.com/escalier-lang/escalier/issues/2584) tracks it.
- A function value's lifetime ignores a `&` borrow it holds.
  [#2565](https://github.com/escalier-lang/escalier/issues/2565) tracks the
  cases. A returned closure that captures a `&` parameter is not tied to that
  parameter. A method value read off a borrowed receiver has no lifetime, so
  with `v: &View`, `sink = v.get` is accepted where
  `sink = fn () { return v.get() }` is rejected. Section 4 of the issue covers
  this case.
- A constructor parameter written with no lifetime that the constructor stores
  into an `'a` field gets its own lifetime `'b`, with the bound `'b: 'a`. So
  `Keyed<'a>` with `constructor(&mut self, key: fn () -> undefined)` renders
  as `new <'a, 'b: 'a>(key: 'b fn () -> undefined) -> Keyed<'a>`. A caller's
  check that a call may run the retained function compares lifetimes by
  identity and does not follow `'b: 'a`, so it misses the conflict. Section 2
  of #2565 covers it.
- A closure that moves out a capture can be called twice once it crosses a
  function boundary, as D2 states.
  [#2581](https://github.com/escalier-lang/escalier/issues/2581) tracks it.

### D18. A module-level binding whose type its uses decide

A binding *generalizes* when each use of it gets fresh copies of its type
variables, as a polymorphic function does. A `val` or `var` generalizes only
when it has a type annotation, or when it is an immutable binding whose
initializer is a syntactic value. Any other binding's type variables are
*weak*. A weak variable is shared by every use of its binding, so the uses
decide its type together. OCaml calls this the value restriction, and prints
such a variable as `'_weak1`.

```esc
val mut seen = Map()
fn a() { seen.set("k", 1) }
fn b() { seen.set("k", "s") }
```

`seen`'s initializer is a call, so `a` and `b` write the same key and value
types, and `seen` settles to `Map<string, number | string>`. If `seen`
generalized, `a` and `b` would each write their own copy of its type
variables. Both calls would check, and `seen` would render as
`Map<never, never>`.

These initializers are syntactic values:

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

Requiring an annotation on every module-level binding, as Rust requires one on
every `static`, would also give `seen` one type. It would give up inference for
the common case of a call whose result needs none, such as `val five = id(5)`.
Weak variables keep that case.

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

The widening applies to the type the checker uses, not only to the type it
displays. A value widens as it reaches a writable slot, so every read of the
binding sees the settled type, whichever function writes it and in whatever
order. With `seen` written by `seen.set("k", 1)`, `fn get() { return seen.get("k") }`
infers as `fn () -> number | undefined`, and `val one: 1 | undefined = seen.get("k")`
is rejected. A value keeps its literals everywhere else it flows. A `put(x)`
that stores `[x]` in `seen` and returns `x` returns `1` from `put(1)` and
stores `[number]`.

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
and the settling in [weak.go](../../internal/solver/weak.go). Two follow-ups
are open. Keeping a function monomorphic when its parameter flows into a weak
binding is tracked by
[#2352](https://github.com/escalier-lang/escalier/issues/2352). Widening the
literals in the checked type, and not only in the displayed one, is tracked by
[#2348](https://github.com/escalier-lang/escalier/issues/2348).

## Known bugs

**B9. T5 misses a readonly field some owned destinations hide.**
`reportReadonlyIntoWritable` in
[infer_decl.go](../../internal/solver/infer_decl.go) applies T5 to an owned
destination whose type is an object or tuple. It reports nothing for a
destination typed by a type parameter, a union, or a rest parameter, or for a
readonly field inside a class's type argument. Each call below checks clean,
and each should report
`readonly field 'a' cannot satisfy a writable field requirement`:

```esc
fn bounded<T: {a: number}>(o: T) -> T { return o }
fn nullable(o: {a: number} | null) {}
fn rest(...o: Array<{a: number}>) {}
fn list(o: Array<{a: number}>) {}

fn f(p: {readonly a: number}, q: {readonly a: number},
     r: {readonly a: number}, xs: Array<{readonly a: number}>) {
    bounded(p)
    nullable(q)
    rest(r)
    list(xs)
}
```

The class case holds behind a `&mut` borrow too, so a
`&mut Array<{readonly a: number}>` fills a `&mut Array<{a: number}>`. The class
case is tracked by
[#2552](https://github.com/escalier-lang/escalier/issues/2552).

## Divergences from the docs

[docs/08_mutability.md](../../docs/08_mutability.md) and
[docs/09_ownership.md](../../docs/09_ownership.md) disagree with the checker in
these places:

- Exclusivity between two explicit borrows, such as `&p` and `&mut p`, is
  enforced. Taking `val b = &p` and then `val a = &mut p` while `b` is still
  used reports
  `cannot borrow 'p' as mutable while it is borrowed as immutable`. Both docs
  say it is not yet enforced and cite
  [#794](https://github.com/escalier-lang/escalier/issues/794).
- An element store `t[i] = x` is typed and moves its source.
  `docs/09_ownership.md` says it is rejected as unsupported and cites
  [#1268](https://github.com/escalier-lang/escalier/issues/1268).
- The docs say a field store moves only when the container outlives the source.
  The checker moves on every store into an owned field, and F2 agrees with it.
  The container owns the value afterward, so the source cannot keep it.
- Reassigning a `var` after a move gives it a fresh value, and a later use is
  accepted. `docs/09_ownership.md` says the use is reported as a use after a
  move and cites
  [#1269](https://github.com/escalier-lang/escalier/issues/1269).

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
allows and forbids the same writes.

```ts
declare function reset(x: Readonly<{a: {b: number}}>): void;
```

When every slot in `T`, and in every layer `T` owns, is `readonly` or holds a
value type, a `&mut` grants no writes. The converter then emits `&T` instead.
`Readonly<{a: number}>` qualifies. A recursive `DeepReadonly<T>` qualifies too,
but the converter does not resolve a reference to an alias, so it still emits
`&mut` for one.

`classifyExplicitSignal` in
[mutability.go](../../internal/dts_to_esc/mutability.go) maps `this:
Readonly<T>` to `&self` only when no slot reachable through `T` is writable.

`readonly T[]` needs the same treatment, but Escalier has no array type whose
elements are fixed while their contents stay writable, so that case is still
open.

**I2. A value-type parameter is written as its bare type.** A primitive or
promise parameter is copied, so the converter writes the bare type and no
borrow marker. A callback parameter is written as a bare `fn` type too. Its
lifetime is elided, so by D17 the callee only calls it during the call.
`forEach(callbackfn: (value: T) => void)` converts to
`forEach(callbackfn: fn (value: T) -> unknown)`, and a closure that borrows a
local passes to it.

An API that keeps its callback, such as `addEventListener`, `setTimeout`, or
`Promise.then`, needs `'static fn` on that parameter. Marking those parameters
is not implemented. [#2573](https://github.com/escalier-lang/escalier/issues/2573)
tracks marking the Web APIs, and
[#2574](https://github.com/escalier-lang/escalier/issues/2574) tracks analysing
third-party JavaScript to find the rest. Until a parameter is marked, a closure
that borrows a local passes to it, and the checker misses any conflict with a
later call of that closure.

A callback is the one parameter for which the converter does not follow I3.
The `'static fn` form would reject more programs. It would reject each closure
passed to `forEach` that borrows a local the caller uses afterwards, and a
callback that is only called, as `forEach`'s is, is the common case.

**I3. An uncertain answer takes the form that rejects more programs.** When the
converter or an analysis cannot settle a fact, it picks the form under which the
rules reject every program the true answer would reject:

- `&mut` over `&` for a receiver or parameter that may be written.
- Escaping over not escaping for a parameter that may be stored.
- A borrow of the inputs over an owned value for a return that may share
  structure with an input. A shallow copy such as `{...opts}` shares everything
  below its top level, so it returns a borrow of `opts`.

A callback parameter is the exception, as I2 states.

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
| T5 | A `{readonly a: number}` value into a `{a: number}` slot of a `val` binding | Rejected |
| T5 | A `{readonly a: number}` argument for a `mut o: {a: number}` parameter | Rejected |
| T5 | A `{readonly a: number}` argument for an `o: {a: number}` parameter | Rejected |
| T5 | A `{readonly a: number}` argument through a function value | Rejected |
| T5 | A `{readonly a: number}` call result for a `mut o: {a: number}` parameter | Rejected |
| T5 | Reassign a `var o: {a: number}` binding to a `{readonly a: number}` value | Rejected |
| T5 | Return a `{readonly a: number}` value from a function that returns `{a: number}` | Rejected |
| T5 | A `&{readonly a: number}` argument for an `o: &{a: number}` parameter | Accepted |
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
| E4 | `local` and `moved` from E4 | As marked in E4 |
| E4 | `setup` and `page` from E4 | As marked in E4 |
| E4, D17 | `page` with `b.click()` moved above `render(&count)` | Accepted |
| E4 | A closure that moves a capture, called twice | The second call reports a moved value |
| E4, D10 | The two `setup` programs from D10 | The first rejected, the second accepted |
| D17 | `wrap` and `stash` from D17 | The signature and the error D17 lists |
| D17 | `&fn () -> undefined` in an annotation | `Unsupported: borrow of a non-borrowable type` |
| D17 | A closure over a local passed to a `'static fn` parameter, then the local read, written, moved or borrowed | The later use reports that the slot took the local over |
| D17 | A closure over a local passed to a `'static fn` parameter, with no later use of the local | Accepted |
| D17 | A closure over a parameter passed to a `'static fn` parameter | Rejected at the call |
| D17 | An unannotated `var` started with a closure that borrows nothing, then assigned one that borrows a local | Rejected |
| I1 | Convert `reset(x: Readonly<{a: {b: number}}>)` | `reset(x: &mut Readonly<{a: {b: number}}>)` |
| I1 | Convert a parameter typed `Readonly<{a: number}>` | `&Readonly<{a: number}>` |
| I1 | Convert `this: Readonly<T>` where `T` has a writable nested slot | Not `&self` |
| I2 | Convert a `number` parameter | `number`, with no borrow |
| I2 | Convert `forEach(callbackfn: (value: T) => void)` | `forEach(callbackfn: fn (value: T) -> unknown)` |
| I3 | Convert a method the analysis cannot settle | `&mut self` |
| I3 | Convert a function returning `{...opts}` | A borrow of `opts` |

D11's write-once cell gets its tests once its API is designed.

## Migration

The migration replaced the solver's separate ownership passes with the checking
model above. The events live in
[ownership_events.go](../../internal/solver/ownership_events.go), and the
dataflow pass in
[ownership_dataflow.go](../../internal/solver/ownership_dataflow.go).
[#2277](https://github.com/escalier-lang/escalier/issues/2277) tracks the steps.
Each step was one or more PRs.

Steps 1 through 8 are done:

1. The conformance tests above were written against the solver. A test the
   solver fails is disabled the way `CLAUDE.md` describes, under a
   `DISABLED until` comment that names the issue which enables it.
2. Owned mutability moved from types to bindings, as T1 states. A parameter's
   binding is mutable only when written `mut`, as D13 decides.
3. Every borrow is written, as F4 states, and the LSP server offers a quick fix
   that inserts the missing `&` or `&mut`.
4. Parameter inference follows F3 and D9. An unannotated parameter the body
   only reads is a borrow, and a call to an escaping one moves its argument.
5. Function types take lifetimes, as D10 decides, and E4 holds a passed
   closure's borrows for that lifetime.
6. The declaration converter follows I1 through I3.
7. The event lowering and the single dataflow pass were built, brought to
   parity with the earlier checks, and turned on.
8. The earlier checks the pass replaces were removed.

Step 9 is open. It designs and adds the write-once cell from D11, as
[#2275](https://github.com/escalier-lang/escalier/issues/2275) and
[#2276](https://github.com/escalier-lang/escalier/issues/2276) track. The
decisions above name the other checker work that remains.

These checks stay outside the dataflow pass, since they enforce rules the pass
does not check:

- The per-site checks behind `ModuleLevelMoveError` in
  [module_level_moves.go](../../internal/solver/module_level_moves.go). A move
  out of module-level state is rejected wherever it happens, since the function
  may run again or other code may read the binding later. That is a
  flow-insensitive rule about the binding, so each site checks it rather than
  the pass finding an E1 use-after-move on a path.
- T8's `MixedOwnershipError` in [infer_expr.go](../../internal/solver/infer_expr.go).
- The L2 checks in [lifetime_signature.go](../../internal/solver/lifetime_signature.go),
  [lifetime_rigid.go](../../internal/solver/lifetime_rigid.go), and
  [lifetime_undeclared.go](../../internal/solver/lifetime_undeclared.go), and the L3
  rendering in [lifetime_coalesce.go](../../internal/solver/lifetime_coalesce.go).
- The type rules in [constrain.go](../../internal/solver/constrain.go).

The point of the work was a simpler solver, so #2277 records two measurements
from before step 1 and after step 8:

- The line count of the 26 ownership, lifetime, and mutability files the
  baseline counted went from 12,336 to 6,902. The files the migration added for
  the dataflow pass hold another 8,163 lines.
- The `gocognit` cognitive complexity total for the non-test files of
  `internal/solver` went from 10,802 to 11,880.

Both final numbers include unrelated work that landed between the two
measurements.
