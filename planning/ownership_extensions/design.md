# Design: Ownership across `await` and `yield`

## 1. Scope

This document extends the rules in
[planning/ownership_rules/requirements.md](../ownership_rules/requirements.md)
to the places where a value crosses a suspension. A *suspension* is a point
where a function stops running and other code runs before it resumes. Escalier
has three:

- `await` in an `async fn`,
- `yield` in a `gen fn`,
- both in an `async gen fn`, which a `for await` loop consumes.

It also settles what a `for … in` loop binds, since a loop consumes a generator
the same way it consumes an array.

The rules here use the terms, rule names and diagnostics of the ownership rules.
A reference such as F2 or E3 names a rule in that document. Rules this document
adds are numbered A1 to A7 for promises, G1 to G5 for generators, R1 to R3 for
loops, and S1 to S3 for async generators. Section 7 lists what the ownership
rules themselves change.

The Ownership Extensions milestone implements this document. It is the design
for [#2426](https://github.com/escalier-lang/escalier/issues/2426),
[#2427](https://github.com/escalier-lang/escalier/issues/2427) and
[#2428](https://github.com/escalier-lang/escalier/issues/2428).

## 2. Problem

Each suspension hands its consumer a value that something else still holds.

**A settled promise returns the same object every time.** Every `await` of a
promise and every reaction registered with `.then`, `.catch` or `.finally`
receives the one object it settled to. The checker treats each `await p` as an
owned value and leaves `p` usable:

```esc
type Flat = {x: number}
declare fn load() -> Promise<Flat>

fn keep(f: Flat) {
    val mut g = f
    g.x = 2
}

async fn go() -> Promise<number> {
    val p = load()
    keep(await p)
    return (await p).x   // 2, though nothing in go wrote x
}
```

This checks clean today. `SharedAwaitIntoMutError` in
[infer_decl.go](../../internal/solver/infer_decl.go) rejects `val mut a = await
p`, but an owning parameter, an owned field and a `return` are unguarded.

**A generator yields a value and keeps running.** The checker treats `yield`
as neither a move nor a borrow:

```esc
gen fn g() {
    val mut buf: Flat = {x: 0}
    yield buf
    buf.x = 1   // accepted, though the consumer owns buf
}

gen fn h() {
    val mut buf: Flat = {x: 0}
    yield &buf
    buf.x = 1   // accepted while the consumer may still hold the &Flat
}
```

`g` renders as `fn () -> Generator<Flat, undefined, unknown>` and `h` as
`fn () -> Generator<&Flat, undefined, unknown>`.

**A suspended body still holds its parameters' borrows.** An `async fn` body
keeps running after the call returns, and a `gen fn` body does not start until
the first `next`. The checker ends an argument's borrow when the call returns,
so the caller can write a value the body reads later. Both programs below check
clean today:

```esc
declare fn tick() -> Promise<undefined>

async fn show(p: &Flat) -> Promise<number> {
    await tick()
    return p.x
}

async fn go() -> Promise<number> {
    val mut f: Flat = {x: 1}
    val pr = show(&f)
    f.x = 2              // show reads p.x after this write
    return await pr
}

gen fn each(p: &Flat) { yield p.x }

fn run() {
    val mut f: Flat = {x: 1}
    val it = each(&f)
    f.x = 2              // each reads p.x after this write
    for n in it {}
}
```

**A loop binds an element that its source still holds.** With
`arr: Array<Flat>` and `keep(f: Flat)`, `for f in arr { keep(f) }` reports
`use of moved value 'f'` at `keep(f)`. Each iteration binds a new `f`, so the
message is wrong. Worse, the element stays in `arr`, so accepting `keep(f)`
would give the element two owners.

## 3. Rules for promises

A promise is reference-shaped. It moves rather than copies, which the checker
already does, since `Promise` is a class. Section 7 removes it from the
ownership rules' list of value types.

### 3.1 A1. `await` has a move form and two borrow forms

`await e` is a flow site. Its source is `e` and its destination is the
`await` itself, which F2 treats as an owned destination. The result follows
from the form of `e`:

| Form | What happens to `p` | Result type for `p: Promise<T>` |
|---|---|---|
| `await p` | Moved, by F2 | `T`, owned |
| `await &p` | Borrowed immutably for `'a` | `&'a T` |
| `await &mut p` | Borrowed mutably for `'a`, which T7 allows only through `val mut p` | `&'a mut T` |
| `await load()`, any `e` that is not a place | Nothing, since no place holds the promise | `T`, owned |

When `T` is itself a borrow, as in `Promise<&'b mut Flat>`, `await p` yields
that borrow. When `T` is a value type, each form yields a copy of the value, as
F2 states for a `&T` whose `T` is a value type.

The borrow forms follow from A3. `&p` has type `Promise<&'a T>`, and awaiting
that promise is an `await` of a value that is not a place, so it yields
`&'a T`.

```esc
async fn go() -> Promise<number> {
    val p = load()
    keep(await p)
    return (await p).x   // error: use of moved value 'p'
}

async fn twice() -> Promise<number> {
    val p = load()
    val a = await &p
    val b = await &p     // accepted, since both borrows are immutable
    return a.x + b.x
}

async fn owned() {
    val p = load()
    keep(await &p)       // error: cannot use borrowed &Flat as owned object
}

async fn write() -> Promise<number> {
    val mut p = load()
    val m = await &mut p
    m.x = 2              // accepted
    val r = await &p     // error: cannot borrow 'p' as immutable while it is borrowed as mutable
    return m.x + r.x
}

async fn after() -> Promise<number> {
    val mut p = load()
    val m = await &mut p
    m.x = 2
    return (await &p).x  // accepted, since m is dead. The result is 2
}
```

A promise held in module-level state can only be borrowed, by E1. A cached
promise is the common case for `await &p`:

```esc
val config = load()

async fn read() -> Promise<number> {
    val a = (await config).x   // error: cannot move module-level value 'config' inside a function. Borrow it with `&config`
    return (await &config).x   // accepted
}
```

Under A1, `SharedAwaitIntoMutError` has no case to catch. `val mut a = await p`
moves `p`, so `a` is the only owner. `val mut a = await &p` binds a borrow, and
the existing D12 warning covers a `mut` on a borrow. The error and
`rejectSharedAwait` are removed.

### 3.2 A2. A reaction borrows its promise for `'static`

The runtime keeps a reaction until the promise settles, and then hands it the
same object every `await` sees. So each reaction method borrows its receiver
immutably for `'static`, and its callback is a function the callee keeps, as
E4 and D10 describe:

```esc
declare class Promise<T, E = never> {
    then<R, F>(&'static self, onfulfilled: &'static fn (value: &'static T) -> R throws F) -> Promise<R, E | F>,
    catch<R, G>(&'static self, onrejected: &'static fn (reason: &'static E) -> R throws G) -> Promise<&'static T | R, G>,
    finally<F>(&'static self, onfinally: &'static fn () -> unknown throws F) -> Promise<&'static T, E | F>,
}
```

The declaration omits the `PromiseLike` return forms and the two-callback
`then` overload, which follow the same pattern. Three consequences follow from
the ordinary rules:

- After `p.then(cb)`, `p` has a `'static` immutable borrow. `await &p` stays
  legal. `await p` reports `cannot move 'p' while it is borrowed`, by E3.
  `await &mut p` reports
  `cannot borrow 'p' as mutable while it is borrowed as immutable`, by E2.
- The callback receives `&'static T`. Passing the value on as owned reports
  `cannot use borrowed &Flat as owned object`, by T6.
- `catch` and `finally` return a promise that settles to the same object `p`
  settles to, so their results hold `&'static T`. `then` returns whatever its
  callback returns, so its result is owned when the callback returns an owned
  value.

```esc
async fn go() -> Promise<number> {
    val p = load()
    val q = p.then(fn (v) { return v.x })
    val a = await &p                       // accepted
    val b = await p                        // error: cannot move 'p' while it is borrowed
    return a.x + (await q)
}
```

A `.then` on a temporary, as in `load().then(cb)`, borrows a promise that no
place holds, which D15 accepts. Nothing can await that promise again, so the
`'static` borrow costs nothing.

The `catch` result `Promise<&'static T | R, G>` mixes ownership when the
fallback `R` is owned. Section 8.3 settles how that union is checked.

### 3.3 A3. A borrow of a promise is a promise of a borrow

A promise's contents can be reached only through `await` or a reaction. A
borrow of a promise therefore gives exactly the access a promise of a borrow
gives, and the type checker normalizes one to the other:

- `&'a Promise<T, E>` is `Promise<&'a T, &'a E>`.
- `&'a mut Promise<T, E>` is `Promise<&'a mut T, &'a mut E>`.

The borrow distributes over the rejection type `E` too, since a rejected
promise hands every awaiter the same reason object. A value-type `T` or `E`
absorbs the borrow, as F2 states, so `&'a Promise<number>` is
`Promise<number>`. The borrow event that `&p` records still holds `p` for
`'a`. The normalization changes the type, not the borrow check.

The normalized form is the one the printer renders. The rule is the same kind
of rule as [#2424](https://github.com/escalier-lang/escalier/issues/2424), which
distributes a borrow over a union, and the two compose.

The rule lets a caller lend a promise to a function that only awaits it:

```esc
async fn wait(p: Promise<&Flat>) -> Promise<number> {
    return (await p).x
}

async fn go() -> Promise<number> {
    val p = load()
    val n = await wait(&p)       // accepted, since &p is a Promise<&Flat>
    return n + (await &p).x
}
```

A `Promise<&'a mut T>` made from `&mut p` is exclusive in the way any `&mut p`
is. It cannot be duplicated, since a promise moves. While it is live, `p` is
mutably borrowed.

A reaction on a promise of a borrow needs that borrow to be `'static`, by A2.
`(&p).then(cb)` on a local `p` is accepted, since L1 lets the borrow of `p`
reach `'static`, and it means the same as `p.then(cb)`. A reaction on a
parameter whose borrow the signature does not make `'static` is rejected:

```esc
fn watch(p: Promise<&Flat>) {
    p.then(fn (v) { return v.x })   // error: `then` keeps its callback until the promise settles, so it needs a promise of a 'static borrow, and 'p' holds a borrow that ends with the call; write `Promise<&'static Flat>` or `await` it instead
}
```

This is a new diagnostic, `ReactionBorrowLifetimeError`. The method name in it
is `then`, `catch` or `finally`, whichever was called.

### 3.4 A4. An `async fn` call lends its arguments until its promise settles

An `async fn` body runs past its first `await` after the call has returned. The
promise the call returns is the only thing the caller can observe the body
through. So the returned promise holds every borrow the call's arguments carry,
the way E4 says a closure holds its captures. The borrows stay live until the
promise settles.

The checker knows the promise has settled once the caller has awaited it in any
of A1's forms. A promise the caller moves elsewhere, stores, or drops without
awaiting may settle at any later point, so its borrows stay live for `'static`.
JavaScript runs an `async fn` body to completion whether or not anyone awaits
its promise, so dropping the promise does not end the body.

```esc
async fn go() -> Promise<number> {
    val mut f: Flat = {x: 1}
    val pr = show(&f)
    f.x = 2              // error: cannot assign to 'f.x' while it is borrowed as immutable
    return await pr
}

async fn ok() -> Promise<number> {
    val mut f: Flat = {x: 1}
    val n = await show(&f)
    f.x = 2              // accepted, since show's promise has settled
    return n
}

fn forget() {
    val mut f: Flat = {x: 1}
    show(&f)
    f.x = 2              // error: cannot assign to 'f.x' while it is borrowed as immutable
}
```

The rule covers an `async fn` and an async closure. A function that is not
`async` but returns a promise states in its signature what the promise holds,
as in `fn later<'a>(p: &'a Flat) -> Promise<&'a Flat>`. Its body can keep a
borrow past the call only by registering a reaction, which A2 makes `'static`.

A bodyless `declare fn` that returns a promise is checked as though it were an
`async fn`. Its declaration cannot say how long the implementation reads its
arguments, and I3 picks the form that rejects more programs. So
`declare fn save(doc: &Doc) -> Promise<undefined>` holds `doc` until its
promise settles.

### 3.5 A5. A suspension point is a call to code the checker cannot see

While a body is suspended at an `await`, other code runs. That code reaches the
suspended body's values only through paths that escaped the frame, such as a
closure stored with a `'static` lifetime or module-level state. E4 and E2
already treat a `'static` path as live at every later point, so an `await`
needs no exclusivity rule of its own for those paths.

An `await` is still a use of each borrow a stored closure holds, the same as a
call that may run the closure, as E4 states. The checking model records it the
same way. In `stored` from E4, an `await` in place of `readIt(&p)` reports
`this await may run 'sink', which writes 'p' while it is borrowed as immutable`
when an immutable borrow of `p` is live across it.

Module-level state that another function writes during the suspension is not
covered. Section 8.6 discusses it.

### 3.6 A6. The rejection value follows the fulfilment value

A rejected promise hands the same reason to every awaiter and every `.catch`.
An `await` that throws raises a value of the type A1 gives the fulfilment
value, with `E` in place of `T`:

| Form | Raises |
|---|---|
| `await p` | `E`, owned |
| `await &p` | `&'a E` |
| `await &mut p` | `&'a mut E` |
| `await load()` | `E`, owned |

A `catch` arm around `await &p` binds `&'a E`:

```esc
declare class MyError { code: number }
declare fn loadOr() -> Promise<Flat, MyError>
declare fn report(e: MyError) -> undefined

async fn go() {
    val p = loadOr()
    try {
        await &p
    } catch {
        e => report(e),   // error: cannot use borrowed &MyError as owned object
    }
}
```

### 3.7 A7. A promise with nothing to write still warns under D12

D12 warns on a `mut` binding or a `&mut` borrow whose type has no writable
slot. A3 makes `&mut p` grant writes to the promise's contents, so whether a
`Promise` has a writable slot depends on `T`. It is judged like a class whose one
`readonly` field holds `T`. The field cannot be replaced, and the owned layers
of `T` can be written:

```esc
async fn fetchNum() { return 5 }

fn k() {
    val mut q = fetchNum()    // warns, since Promise<number> has no writable slot
    val mut r = load()        // accepted, since &mut r can write the Flat inside
}
```

T9 and D18 already widen the literals of an owned layer of a `mut` binding, so
`val mut r = Promise.resolve({x: 1})` settles to `Promise<{x: number}>`.

## 4. Rules for generators

### 4.1 G1. `yield` has a move form and two borrow forms

`yield e` is a flow site whose destination is the generator's yield type. When
no yield type is written, F3 decides it. The value leaves the frame for the
consumer, as a returned value does, so the destination is owned unless `e` is
written as a borrow:

| Form | What happens to `x` | Yield type |
|---|---|---|
| `yield x` | Moved, by F2 | `T`, owned |
| `yield &x` | Borrowed immutably, by G2 | `&T` |
| `yield &mut x` | Borrowed mutably, by G2 | `&mut T` |
| `yield {x: 0}`, any value no place holds | Nothing | owned |

```esc
gen fn g() {
    val mut buf: Flat = {x: 0}
    yield buf
    buf.x = 1    // error: use of moved value 'buf'
}
```

All yields in one body agree on ownership by T8, since the yield type joins
them the way a conditional's branches join.

### 4.2 G2. A yielded borrow lives as long as the generator

The consumer can keep a yielded borrow for as long as it likes. It can collect
every value into an array with `[...h()]`, which calls `next` again while it
still holds the earlier values. So a borrow the body yields stays live at every
later point of the body. E2 then rejects any later write to an immutably
yielded place, and any later immutable borrow of a mutably yielded one.

```esc
gen fn h() {
    val mut buf: Flat = {x: 0}
    yield &buf
    buf.x = 1    // error: cannot assign to 'buf.x' while it is borrowed as immutable
}

gen fn fresh() {
    for i in [0, 1, 2] {
        yield {x: i}    // accepted, since each value is new
    }
}
```

The consumer's side needs no rule. After `yield &x`, E2 rejects every later
write to `x` in the body, so the consumer's `&Flat` observes no mutation.
After `yield &mut x`, the body may still write `x`, since E2 allows several
mutable paths. The consumer's `&mut` promises nothing about other writers, so
nothing is broken. The yield type renders
with an elided lifetime, as `Generator<&Flat, undefined, unknown>`, unless the
borrow ties to a parameter, which L3 renders:

```esc
gen fn items<'a>(p: &'a {inner: Flat}) { yield &p.inner }
// fn <'a>(p: &'a {inner: Flat}) -> Generator<&'a Flat, undefined, unknown>
```

Section 8.1 compares this rule with a lending generator, which ends the borrow
at the next `next` instead.

### 4.3 G3. `next(v)` passes `v` like an argument

A value sent with `it.next(v)` is an argument whose destination is the
generator's `TNext` type. An owned `TNext` moves `v` into the generator, which
the checker already does. A borrow `TNext` needs a written `&v` or `&mut v`, by
F4. The generator may keep the borrow in a local across later suspensions, so
the borrow lives as long as the generator value, by G4.

```esc
gen fn sum() -> Generator<number, number, Flat> {
    val first = yield 0
    val second = yield first.x
    return first.x + second.x
}

fn go() {
    val mut it = sum()
    val mut a: Flat = {x: 1}
    it.next(a)
    a.x = 2      // error: use of moved value 'a'
}
```

Inside the body, `yield` evaluates to the value `next` sent, at type `TNext`.
`val first = yield 0` binds an owned `Flat`. `it` is a `val mut` binding
because `next` takes `&mut self`, by X3.

### 4.4 G4. A generator holds its arguments' borrows while it is live

A `gen fn` body runs only when the consumer calls `next`. A generator that is
never advanced again never reads its arguments again. So the generator value
holds each borrow its call's arguments carry, and the borrows stay live while
the generator value is live, by L1. This is lazier than A4. An `async fn` body
keeps running after its promise is dropped, and a generator body stops when the
generator is.

```esc
fn run() {
    val mut f: Flat = {x: 1}
    val it = each(&f)
    f.x = 2      // error: cannot assign to 'f.x' while it is borrowed as immutable
    for n in it {}
}

fn ok() {
    val mut f: Flat = {x: 1}
    for n in each(&f) {}
    f.x = 2      // accepted, since the generator is dead
}
```

### 4.5 G5. `yield from g` iterates `g`

`yield from e` consumes `e` the way `for x in e { yield x }` does, so R1
decides what happens to `e`, and G1 applies to each value it forwards.

## 5. Rules for loops

### 5.1 R1. A loop moves its source or borrows it, as written

`for x in e` is a flow site whose source is `e` and whose destination is the
loop, an owned destination. It has the same three forms as `await` and
`yield`:

| Form | What happens to `arr` | `x` for `arr: Array<T>` |
|---|---|---|
| `for x in arr` | Moved, by F2 | `T`, owned |
| `for x in &arr` | Borrowed immutably for the loop | `&T` |
| `for x in &mut arr` | Borrowed mutably for the loop | `&mut T` |
| `for x in r`, with `r: &Array<T>` | `r` is copied, by F2 | `&T` |
| `for x in mk()`, any `e` that is not a place | Nothing | `T`, owned |

A loop over an owned source is the source's last owner. Each element reaches
`x` once and nothing else reaches it afterwards, so `x` owns it. A loop over a
borrow binds borrows with the borrow's mutability, the same way F4 and D14 say
a pattern over a borrow binds them. The loop's pattern is a pattern.

```esc
fn go(arr: Array<Flat>) {
    for f in arr {
        keep(f)              // accepted, since the loop moved arr
    }
}

fn twice(arr: Array<Flat>) -> number {
    for f in &arr {
        keep(f)              // error: cannot use borrowed &Flat as owned object
    }
    return arr.length        // accepted, since the loop only borrowed arr
}

fn after(arr: Array<Flat>) -> number {
    for f in arr {}
    return arr.length        // error: use of moved value 'arr'; the `for` loop moved it, so iterate `&arr` to keep it
}

fn bump(mut arr: Array<Flat>) {
    for f in &mut arr {
        f.x = f.x + 1        // accepted
    }
}
```

Section 8.2 compares this rule with binding every element as a borrow.

### 5.2 R2. Each iteration binds a fresh loop variable

The loop variable is a new binding on every iteration. A move of `x` in one
iteration does not consume the `x` of the next. The control-flow graph in
[cfg.go](../../internal/liveness/cfg.go) gives the loop header a definition of
`x` on every path, including the back edge, so E1 sees each `x` as a new
place. A move of a place declared outside the loop still reports on the next
iteration, as the ownership rules' "move in a loop" test expects.

### 5.3 R3. A loop over an iterator consumes it or advances it through `&mut`

A generator or another iterator returns itself from `[Symbol.iterator]`, so
the loop advances the value it was given. `for x in it` moves an owned `it`.
`for x in it` with `it: &mut Generator<T>` advances the generator through the
borrow and leaves the caller holding it. `x` binds the generator's yield type,
owned or borrowed as G1 made it.

A `for` loop closes a generator when it exits early, and a generator that ran
to completion yields nothing more. So moving an owned generator into the loop
loses nothing the caller could still use. `for x in &it` is rejected, since
advancing a generator writes its state:
``cannot borrow 'it' as immutable to iterate it, since advancing a generator writes it; iterate `it` or `&mut it` ``.
This is a new diagnostic, `ImmutableIteratorLoopError`.

## 6. Rules for async generators

### 6.1 S1. An async generator is a generator whose steps are promises

`async gen fn` combines G1 to G5 with A1 to A6. Its body may `await` and
`yield`, and `for await x in e` consumes one. R1 decides what happens to `e`
and what `x` binds, exactly as for a sync loop. Each `next` returns a fresh
promise that no place holds, so the loop's implicit `await` yields an owned
step, by A1.

The borrow lifetimes combine the two laziness rules. The body runs from a
`next` until its next `yield`, even if the consumer drops the promise `next`
returned. So the async generator holds its arguments' borrows while it is live
and until the promise of its last `next` settles.

A yielded borrow lives as long as the generator, by G2, so an `await` inside
the body cannot let the body write a value the consumer still holds. The
consumer may hold a yielded borrow while the body runs ahead to its next
`yield`, and G2 already rejects any write the body would make.

### 6.2 S2. `yield` in an async generator awaits a promise operand

JavaScript awaits a promise passed to `yield` inside an async generator, so the
consumer receives the settled value. `yield e` there is `yield await e`, and A1
then G1 decide it:

```esc
async gen fn stream() {
    val p = load()
    yield p          // moves p and yields the Flat it settles to
    yield &p         // error: use of moved value 'p'
}

async gen fn cached() {
    val p = load()
    yield &p         // yields &Flat, a borrow of p's contents, by A3
    yield await p    // error: cannot move 'p' while it is borrowed
}
```

In `cached`, G2 keeps the first yielded borrow live for the rest of the body,
so the move in `await p` is rejected by E3.

### 6.3 S3. `yield await p` moves `p` and yields the result

`yield await p` moves `p` by A1 and yields the owned result by G1.
`yield await &p` yields a `&T` that G2 keeps live for the rest of the body.

## 7. Impact on the ownership rules

These changes to [requirements.md](../ownership_rules/requirements.md) follow
from the rules above. They land with the PRs in section 12 that make each one
true, so the rules document keeps describing what the checker enforces.

- **Terms.** A promise leaves the list of value types. The list becomes
  primitives and functions. A promise is reference-shaped, which matches the
  checker today, since `isReferenceShaped` in
  [moves.go](../../internal/solver/moves.go) treats a `Promise` class instance
  as movable. Its doc comment also says a promise is a value type and changes
  with it.
- **F1.** The flow-site table gains three rows. `await e` has an owned
  destination. `yield e` has the generator's yield type as its destination.
  `for x in e` has an owned destination. Each takes a written borrow the way an
  argument does. The `return` row notes that a `return` in an `async fn`
  settles the promise, so its value reaches every awaiter, and A1 governs them.
- **F4.** The list of places that borrow without a written `&` gains a loop
  over a borrow, which binds borrows as a pattern does. A `.then`, `.catch` or
  `.finally` receiver is already covered as a method receiver.
- **E4.** A call to an `async fn` holds its arguments' borrows until its
  promise settles, by A4. A generator holds them while it is live, by G4. An
  `await` counts as a call that may run any stored closure, by A5. These sit
  beside the closure rule, since a suspended body is a closure the runtime
  holds.
- **D12.** The example `val mut p = fetchNum()` keeps its warning, since
  `Promise<number>` still has no writable slot. The sentence "A `Promise<T>` is
  one, since nothing can write the value inside it" changes to say a promise is
  judged like a class with one `readonly` field holding `T`, by A7. The
  `wait(p: &mut Promise<number>)` example keeps its warning for the same
  reason. A `&mut Promise<Flat>` does not warn.
  [#2304](https://github.com/escalier-lang/escalier/issues/2304) implements
  the warning and needs the same refinement.
- **D18.** The bullet that says nothing can write the value inside a promise
  changes the same way. `val mut p = fetchNum()` still keeps `Promise<5>`,
  since a number has no owned layer. A promise of an object widens the object's
  writable fields.
- **I2.** A promise leaves the value-type parameters. The converter writes a
  promise parameter as X1 states.
- **Comparison with Rust.** The table gains rows for A1, A2, A3, A4, G2 and
  R1, from section 9.
- **Conformance tests.** The flow matrix gains `await`, `yield` and the loop as
  flow sites, so each column becomes a test at each. The per-rule list gains
  the examples in sections 3 to 6.
- **Checking model.** `await`, `yield` and the loop header emit the same
  `copy`, `borrow` and `move` events as any other flow site. A4 and G4 add the
  returned promise or generator as a holder of each argument borrow.

## 8. Choices

### 8.1 A borrowed yield lives as long as the generator

The options for `yield &x` were:

- **Reject a borrow of a generator local.** Simple, and it rejects `h` above.
  It also rejects a generator that yields a borrow of a local it never writes
  again, which G2 accepts.
- **Type a lending generator.** A *lending* generator is one whose yielded
  borrow ends at the next `next`. Its type would be `Generator<&'r Flat>`, with
  `'r` tied to each call of `next`. The body could then write `buf` after
  each resumption. The consumer could not collect the values, so
  `[...h()]`, `Array.from(h())` and every library function that takes an
  `Iterable<T>` would reject it. Rust's `Iterator` cannot express this
  either, and the lending-iterator designs there need generic associated
  types.
- **Keep the borrow live for the rest of the body.** This is G2. The consumer
  may keep a yielded borrow for as long as it likes, which every JavaScript
  consumer of an iterable assumes. The body cannot write a place after lending
  it. A body that reuses one buffer yields a new value instead, as `fresh` does.

Recommendation: G2. It interoperates with every consumer of `Iterable<T>`, and
it needs no new kind of lifetime.

### 8.2 A loop moves its source unless the source is written as a borrow

The options for `for x in arr` were:

- **Bind each element as a borrow.** `for f in arr` borrows `arr` through
  `[Symbol.iterator](&self)`, the way F4 lets a method receiver borrow, and
  binds `f: &Flat`. `arr` stays usable. `for f in &mut arr` needs a special
  case to bind `&mut`, since the receiver is `&self`. The loop also gives no
  way to take the elements as owned.
- **Move the source unless a borrow is written.** This is R1, Rust's
  `IntoIterator` model. It gives `for` the same three forms as `await` and
  `yield` and keeps F4's rule that every borrow is written. The cost is that
  `for n in nums` followed by `nums.length` reports `use of moved value
  'nums'`, even for an array of numbers.

Recommendation: R1. One rule then covers all three consumers. To soften the
cost, a use of a source a loop moved reports the loop as a related span, and
the message suggests the borrow:
``use of moved value 'nums'; the `for` loop moved it, so iterate `&nums` to keep it``.
PR 5 adds that form of the message.

### 8.3 What `catch` returns when the fallback is owned

`p.catch(fn (e) { return {x: 0} })` produces a promise that settles either to
`p`'s value, a `&'static Flat`, or to a fresh `Flat`. That union mixes
ownership. The options are:

- **Reject it by T8** and ask the callback to write `&{x: 0}`. L1 lets that
  borrow reach `'static`, since nothing else reaches the fresh value. The
  result is then `Promise<&'static Flat>`.
- **Join the union to a borrow implicitly.** The callback's owned value would
  be treated as a `'static` borrow. This hides a borrow F4 says is written.

Recommendation: reject by T8. The diagnostic is the mixed-ownership one:
`mixed-ownership union: members disagree on ownership; make ownership uniform first`.
A callback that returns a value type, or a promise of one, is not affected.

### 8.4 A dropped `async fn` promise keeps its borrows for `'static`

A4 keeps an unawaited promise's borrows live for `'static`. A fire-and-forget
call such as `void save(&doc)` then freezes `doc` for the rest of the program.
The alternatives are unsound. Nothing ends an `async fn` body once it has
started, so the checker cannot pick a shorter lifetime. A caller that wants
`doc` back writes `await save(&doc)`, or passes an owned copy.

Recommendation: keep A4 as stated.

### 8.5 The reaction callback's parameter is a `'static` borrow

A2 gives `onfulfilled` a `&'static T`. The alternative is an owned `T` for the
first reaction only, which no type can express, since the order of reactions
is a runtime fact. A callback that needs an owned value builds one from the
fields it reads, and a caller that needs the value owned writes `await p`
instead of registering a reaction.

Recommendation: keep A2 as stated.

### 8.6 Module-level state written while a body is suspended

A borrow of a module-level binding held across an `await` can observe a write
another function makes while the body is suspended:

```esc
var cfg = {x: 0}

fn bump() { cfg.x = cfg.x + 1 }

async fn show() -> Promise<number> {
    val r = &cfg
    await tick()         // bump may run here
    return r.x
}
```

The ownership rules have the same gap for a synchronous call to `bump` between
the borrow and the read, so it is not specific to `await`. A5 makes an `await`
a call to unknown code, so whatever rule closes the gap for calls closes it for
suspensions too.

Recommendation: file the gap against the ownership rules and leave A5 as the
link. Section 12 files it.

## 9. Comparison with Rust

| Rule | Rust behaviour | Verdict | Note |
|---|---|---|---|
| A1 three `await` forms | `.await` takes the future by value. A future is polled once and then consumed | Differs | Escalier adds `await &p` and `await &mut p`, since a JavaScript promise can be awaited again and returns the same object. Rust's `Shared` future from `futures` is the closest match. It hands each awaiter a clone, where `await &p` hands out a borrow |
| A2 reactions borrow for `'static` | No reactions. A spawned task requires `'static` captures | Same | `tokio::spawn` asks the same of its future that `then` asks of its callback |
| A3 borrow of a promise | `&Future` is not awaitable. `Shared<F>` yields clones | No equivalent | |
| A4 an `async fn` holds its borrows until settled | An `async fn` returns `impl Future + 'a`, which holds its borrows until it is dropped | Differs | Rust futures are lazy, so dropping one ends the borrows. A JavaScript `async fn` runs eagerly, so only settling ends them |
| G1 three `yield` forms | Coroutines are unstable. `gen` blocks yield owned values | Differs | |
| G2 a yielded borrow lives as long as the generator | `Iterator::Item` cannot borrow the iterator. A lending iterator needs generic associated types | Differs | Escalier ties the borrow to the generator, not to each `next` |
| G4 a generator holds its arguments' borrows while live | An iterator holding `&'a T` is bounded by `'a` | Same | |
| R1 a loop moves or borrows its source | `for x in v` moves `v` through `IntoIterator`. `&v` and `&mut v` borrow | Same | |
| R2 a fresh loop variable each iteration | Each iteration binds a fresh pattern | Same | |

## 10. Interop

### 10.1 X1. Promise parameters and returns

A `Promise<T>` in a `.d.ts` signature is converted like any other
reference-shaped type:

- A parameter `p: Promise<T>` stays `p: Promise<T>`, an owned promise. The
  converter cannot tell whether the callee keeps the promise, so I3 makes the
  parameter owned, and the caller moves its promise in. When `T` is a type
  parameter, a caller can still lend with `&p`, since `T` then binds a
  borrow, as in `Promise<&Flat>`.
- A returned `Promise<T>` is owned. A `.d.ts` cannot say that the callee
  also keeps the promise it returns, as a cached `fetch` does. The converter
  writes an owned return, and a hand-written override in the declaration data
  marks each known exception as `Promise<&'static T>`.
- A function that returns a promise holds its arguments' borrows until the
  promise settles, by A4.
- `Promise.all`, `Promise.race`, `Promise.any` and `Promise.allSettled` take
  an iterable of promises. An owned array of owned promises moves them, and an
  array of `&p` borrows them, so the result holds borrows for the second.

I2 drops the promise case, as section 7 states.

### 10.2 X2. The `Promise` declaration

The reaction methods in
[prelude.esc](../../internal/interop/data/std/prelude.esc) take `&self` and a
plain `fn` callback today. They change to the form A2 gives. The `Promise`
entry in [mutability.go](../../internal/dts_to_esc/mutability.go) keeps them
off `&mut self`, and its comment changes to say they borrow for `'static`.

### 10.3 X3. `Iterable<T>`, `Iterator<T>` and `Generator<…>`

`Iterable<T>` is the type a loop or a library function consumes. Its
`[Symbol.iterator]` takes `self`, so passing a value as an `Iterable<T>`
moves it, as R1 moves the source of a loop. The values that satisfy it are:

- an owned `Array<T>`, `Set<T>` or `Generator<T, …>`, moved in,
- a borrow `&'a Array<T>`, which satisfies `Iterable<&'a T>`, and the same for
  `&'a mut Array<T>` and `Iterable<&'a mut T>`.

The checker decides these by R1, not by reading the container's declared
`[Symbol.iterator]`. That method returns borrows, as the next paragraph
states, because a direct call leaves the container holding its elements. A
moved container is iterated once and then gone, so R1 hands its elements out
owned.

`Array.from(&arr)` then returns `Array<&Flat>`, which I3 already asks of a
shallow copy.

`Generator` and `AsyncGenerator` take `&mut self` on `next`, `return` and
`throw`, since advancing a generator writes it. `[Symbol.iterator]` on a
generator returns itself, so R3 decides the loop.

The element-reading methods of `Array`, `Map` and `Set` hand out elements the
container still holds. `values()`, `entries()`, `keys()` on a `Map` with
object keys, and `[Symbol.iterator]` called directly return iterators of owned
`T` today. They change to take `&'a self` and return borrows, as in
`values<'a>(&'a self) -> ArrayIterator<&'a T>`. A `Map`'s entries become
`[&'a K, &'a V]`, since each entry tuple is fresh.

### 10.4 X4. `AsyncIterable<T>`

`AsyncIterable<T>` follows X3, with `[Symbol.asyncIterator]` in place of
`[Symbol.iterator]`.

## 11. Open questions

Each question lists the options and a recommendation. None blocks the first PR.

1. **Whether `await &p` on a promise of a value type needs the `&`.** With
   `p: Promise<number>`, all three forms of A1 yield a `number`, but `await p`
   still moves `p`. Options: keep the move, or treat `await p` of a
   value-type promise as `await &p`. Recommendation: keep the move. One rule is
   easier to read than one that depends on `T`, and the error message names
   the fix.
2. **Whether a bodyless promise-returning function can opt out of A4.** A4
   makes `declare fn save(doc: &Doc) -> Promise<undefined>` hold `doc` until
   the promise settles, even when the implementation reads `doc` only before
   its first `await`. Options: accept the cost, or add a marker the declaration
   data can set on a function whose implementation is known. Recommendation:
   accept the cost until a library shows up where it matters.
3. **Whether a lending generator is worth adding later.** G2 rejects a body
   that reuses a buffer across yields. Recommendation: wait for a real program
   that needs it.

## 12. Plan

Each PR below implements its rules in the checker, adds tests that assert the
full diagnostic for every rejection in its sections, and updates
`requirements.md` and `docs/09_ownership.md` for the rules it makes true, as
section 7 lists. Each PR runs `UPDATE_SNAPS=true go test ./...` and
`UPDATE_FIXTURES=true go test ./cmd/...` and reviews the fixture deltas.

The PRs:

1. **`await` forms and reactions.** Implements A1, A2 and A6, and removes
   `SharedAwaitIntoMutError`. Changes the reaction methods in `prelude.esc`
   per X2. Removes promise from the value types in `requirements.md`. Closes
   [#2426](https://github.com/escalier-lang/escalier/issues/2426).
2. **A borrow of a promise.** Implements A3 and A7, and the D12 refinement in
   section 7. Adds `ReactionBorrowLifetimeError`. Closes
   [#2427](https://github.com/escalier-lang/escalier/issues/2427).
3. **An `async fn` call holds its arguments' borrows.** Implements A4 and A5.
   Closes a new issue for the `show` hole in section 2.
4. **`yield` forms and generator borrows.** Implements G1 to G5. Closes the
   `yield` part of
   [#2428](https://github.com/escalier-lang/escalier/issues/2428) and a new
   issue for the `each` hole in section 2.
5. **Loops.** Implements R1 to R3, and the R2 fix to the loop header in the
   control-flow graph. Adds `ImmutableIteratorLoopError` and the loop form of
   the moved-value message from section 8.2. Closes the rest of
   [#2428](https://github.com/escalier-lang/escalier/issues/2428).
6. **Iterable declarations.** Implements X3 and X4 in the prelude and the
   converter. Closes a new issue for the element-reading methods of `Array`,
   `Map` and `Set`.
7. **Async generators.** Implements S1 to S3, including the implicit `await`
   of a promise operand to `yield`. Closes a new issue.

PRs 1 and 2 go first and in that order, since A3 builds on A1. PR 3 depends on
1. PR 4 can land in parallel with 1 to 3. PR 5 depends on 4, and PR 6 on 5.
PR 7 depends on 3 and 5.

The doc changes two outcomes the existing issues expect. Their Done-when lists
change to match when their Plans are updated to point here:

- [#2427](https://github.com/escalier-lang/escalier/issues/2427) expects
  `(&p).then(cb)` to be rejected. By A3 it is accepted for a local `p`, and the
  rejected case is a reaction on a parameter whose borrow is not `'static`, as
  `watch` shows.
- [#2428](https://github.com/escalier-lang/escalier/issues/2428) expects
  `for f in arr { keep(f) }` to report an error. By R1 it is accepted, since
  the loop moves `arr`. The rejected case is `for f in &arr { keep(f) }`.

New issues to file in the milestone:

- An `async fn` call ends its arguments' borrows at the call, so a caller can
  write a value the body reads after an `await`. Bug. PR 3.
- A generator call ends its arguments' borrows at the call, so a caller can
  write a value the body reads on a later `next`. Bug. PR 4.
- `Array`, `Map` and `Set` iterator methods hand out owned elements the
  container still holds. Bug. PR 6.
- Async generators: `for await`, the implicit `await` in `yield`, and the
  lifetime of a pending `next`. Feature. PR 7.
- Module-level state written by another function while a borrow of it is live,
  across a call or a suspension. Bug, against the ownership rules. Not in this
  milestone's plan.
