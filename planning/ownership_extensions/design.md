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
adds are numbered A1 to A8 for promises, G1 to G5 for generators, R1 to R3 for
loops, and S1 to S3 for async generators. Section 7 lists what the ownership
rules themselves change.

The rules here assume the written-signature rules in
[planning/explicit_signatures/design.md](../explicit_signatures/design.md), from
[#2611](https://github.com/escalier-lang/escalier/issues/2611). By its W1, a
written type is complete, and elision fills in any lifetime it leaves out. By
its W2, a module-level function, a method, and a closure bound to a module-level
variable write their full signature. Inference fills in only a closure's return
type and captures, by its W4. The examples in section 2 show code as the checker
reads it today. The examples in the rule sections write their signatures as W2
asks.

The Ownership Extensions milestone implements this document. It is the design
for [#2426](https://github.com/escalier-lang/escalier/issues/2426),
[#2427](https://github.com/escalier-lang/escalier/issues/2427) and
[#2428](https://github.com/escalier-lang/escalier/issues/2428).

## 2. Problem

Each suspension hands its consumer a value that something else still holds.

A *reaction* is a callback registered on a promise with `.then`, `.catch` or
`.finally`. The ECMAScript specification stores each one in a list on the
pending promise, and when the promise settles it queues a job that calls the
callback with the result.

Every `await` of a promise and every reaction receives the one object the
promise settled to. The checker treats each `await p` as an owned value and
leaves `p` usable:

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

A generator yields a value and keeps running afterward. The checker treats
`yield` as neither a move nor a borrow:

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

An `async fn` body keeps running after the call returns, and a `gen fn` body
does not start until the first `next`. Either way the body still holds its
parameters' borrows after the call. The checker ends an argument's borrow when
the call returns, so the caller can write a value the body reads later. Both
programs below check clean today:

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

With `arr: Array<Flat>` and `keep(f: Flat)`, `for f in arr { keep(f) }` reports
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

A1 states the borrow forms' result types directly, so an `await` of `&p` or
`&mut p` needs nothing else. A3 then generalizes them to a borrow of a promise
used outside an `await`: `&p` has type `Promise<&'a T>`, and awaiting that
promise is an `await` of a value that is not a place, so it yields `&'a T`, as
A1 says.

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

async fn owned() -> Promise<undefined> {
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

### 3.2 A2. A reaction borrows its promise until the reaction's result settles

The runtime keeps a reaction until the promise settles, and then hands it the
same object every `await` sees. Dropping the promise does not cancel the
reaction, since whoever holds its `resolve` function keeps it alive. So the
lifetime of the promise itself does not bound how long the callback runs.

The promise the reaction method returns does bound it. `q = p.then(cb)`
settles only after `cb` has run. When `cb` returns a promise, `q` adopts it,
so `q` also waits for an async callback's body. `catch` and `finally` settle
their result once their callback has run, or once `p` has fulfilled and the
`catch` callback never will. Each reaction method therefore borrows its
receiver, and keeps its callback, for the lifetime `'a` of the promise it
returns. A4 defines that lifetime:

```esc
declare class Promise<T, E = never> {
    then<'a, R, F>(&'a self, onfulfilled: 'a fn (value: &'a T) -> R throws F) -> 'a Promise<R, E | F>,
    catch<'a, R, G>(&'a self, onrejected: 'a fn (reason: &'a E) -> R throws G) -> 'a Promise<&'a T | R, G>,
    finally<'a, F>(&'a self, onfinally: 'a fn () -> unknown throws F) -> 'a Promise<&'a T, E | F>,
}
```

The declaration omits the `PromiseLike` return forms and the two-callback `then`
overload, which follow the same pattern. The returned promise is an
`'a Promise`, so by A4 and A8 `'a` lasts until the caller awaits it. It lasts
for `'static` when the caller drops or stores the promise without awaiting it.
Four consequences follow from the ordinary rules:

- While `q = p.then(cb)` is unawaited, `p` has a live immutable borrow.
  `await &p` stays legal. `await p` reports
  `cannot move 'p' while it is borrowed`, by E3. `await &mut p` reports
  `cannot borrow 'p' as mutable while it is borrowed as immutable`, by E2.
- After `await q`, the borrow has ended, so `await p` and writes to the
  callback's captures are legal again.
- The callback receives `&'a T`. Passing the value on as owned reports
  `cannot use borrowed &Flat as owned object`, by T6. Storing it anywhere that
  outlives `'a` is rejected by L1.
- `catch` and `finally` return a promise that settles to the same object `p`
  settles to, so their results hold `&'a T`. `then` returns whatever its
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

async fn after() -> Promise<number> {
    val p = load()
    val n = await p.then(fn (v) { return v.x })
    val b = await p                        // accepted, since the reaction's result has settled
    return b.x + n
}

fn forget() -> undefined {
    val p = load()
    p.then(fn (v) { return v.x })          // the result is dropped, so p stays borrowed for 'static
}
```

A `.then` on a temporary, as in `load().then(cb)`, borrows a promise that no
place holds, which D15 accepts. Nothing can await that promise again, so the
borrow of the receiver costs nothing however long it lasts.

The `catch` result `Promise<&'a T | R, G>` mixes ownership when the fallback
`R` is owned. Section 8.3 settles how that union is checked.

A reaction whose result is never awaited still freezes `p` and the
callback's captures for the rest of the program. Section 8.7 explains when to
prefer `await` and the warning that steers toward it.

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

A reaction on a promise of a borrow needs that borrow to outlive the
reaction's `'a`, by A2. `(&p).then(cb)` on a local `p` is accepted, since L1
lets the borrow of `p` reach `'a`, and it means the same as `p.then(cb)`. A
reaction on a parameter's borrow is accepted when the function awaits the
reaction's result, since `'a` then ends inside the call. It is rejected when
the result is dropped, since A4 then makes `'a` `'static` and the
signature does not make the parameter's borrow `'static`:

```esc
async fn wait(p: Promise<&Flat>) -> Promise<number> {
    return await p.then(fn (v) { return v.x })   // accepted
}

fn watch(p: Promise<&Flat>) -> undefined {
    p.then(fn (v) { return v.x })   // error: the promise `then` returns is never awaited, so the runtime may keep its callback for 'static, and 'p' holds a borrow that ends with the call; await the promise `then` returns, or write `Promise<&'static Flat>`
}
```

This is a new diagnostic, `ReactionBorrowLifetimeError`. The method name in it
is `then`, `catch` or `finally`, whichever was called.

### 3.4 A4. A promise holds the borrows its pending computation reads

A promise stands for a computation that may still be running, and that
computation can read its caller's data after the call has returned. An
`async fn` body runs past its first `await` after the call returns, and
JavaScript runs it to completion whether or not anyone awaits its promise.

A promise type therefore takes a lifetime prefix, written the way D17 writes a
function type's, as in `'a Promise<T, E>`. The lifetime bounds the borrows the
pending computation holds, so a slot keeps the promise only while `'a` is live.
`'static` means the promise may be kept for the whole program, which is the
case for one that holds nothing. The prefix is a different thing from A3's
`Promise<&'a T>`. `'a Promise<T>` holds borrows while it is pending.
`Promise<&'a T>` settles to a borrow.

An `'a Promise` keeps `'a` live until it settles. A8 states where the
checker can tell that it has, and what happens to a promise it cannot follow.

```esc
async fn show(p: &Flat) -> Promise<number> {
    await tick()
    return p.x
}
// show: fn <'a>(p: &'a Flat) -> 'a Promise<number>

async fn go() -> Promise<number> {
    val mut f: Flat = {x: 1}
    val pr = show(&f)
    f.x = 2              // error: cannot assign to 'f.x' while it is borrowed as immutable by 'pr'
    return await pr
}

async fn ok() -> Promise<number> {
    val mut f: Flat = {x: 1}
    val n = await show(&f)
    f.x = 2              // accepted, since show's promise has settled
    return n
}
```

The lifetime comes from three places:

- **Elision, for every written return type.** By W1, a written type is read on
  its own, with or without a body. An elided prefix on a returned promise is
  tied to every input lifetime, as D17 rule 2 and section 5.4 of the
  written-signatures design state. So `show` above reads as
  `fn <'a>(p: &'a Flat) -> 'a Promise<number>`, and
  `declare fn save(doc: &Doc) -> Promise<undefined>` reads as
  `fn <'a>(doc: &'a Doc) -> 'a Promise<undefined>`. That is the form that
  rejects more programs, as I3 asks. It covers a callback parameter too, so a
  closure passed to a promise-returning function stays borrowed until the
  caller awaits the result.
- **Inference, for an async closure with no written return type.** By W4, a
  closure's return type stays inferred. Its promise holds what the body reads
  after its first suspension, the way D17 rule 4 derives a closure's lifetime
  from its captures. A borrowed parameter or a call-only `fn` parameter read
  after an `await` ties its lifetime to the promise's. A body that reads its
  parameters only before it suspends holds nothing, so its promise is
  `'static`.
- **A written prefix.** A declaration states the other two cases:

| How long the implementation uses an argument | Parameter | Returned promise |
|---|---|---|
| Only before it returns | `cb: fn () -> R` | `'static Promise<T>`, written so elision does not tie it to `cb` |
| Until the promise settles | `cb: 'a fn () -> R` | `'a Promise<T>`, which elision also gives |
| Past the promise | `cb: 'static fn () -> R` | `'static Promise<T>`, which elision also gives |

A call-only `fn` parameter keeps D17 rule 2's meaning when the returned
promise does not hold it, as with a written `'static Promise`. Under elision
the returned promise is tied to every input, the parameter included, so an
async body may read the parameter after an `await`, and a plain function may
capture it into a reaction or an async call it returns. D17 rule 2 already lets
a call-only parameter reach a result whose lifetime the signature ties to it,
so no new exception is needed. The caller's closure then stays borrowed until
it awaits the result:

```esc
async fn later(cb: fn () -> undefined) -> Promise<undefined> {
    await tick()
    cb()
}
// later: fn <'a>(cb: 'a fn () -> undefined) -> 'a Promise<undefined>

async fn useLater() -> Promise<number> {
    val mut count = {n: 0}
    val pr = later(fn () { count.n = count.n + 1 })
    val v = &count       // error: cannot borrow 'count' as immutable while 'count.n' is borrowed as mutable by 'pr'
    await pr
    return count.n
}
```

A written `'static` on a body's promise is checked against what the body
holds:

```esc
async fn keyed(pick: fn () -> string) -> 'static Promise<number> {
    val key = pick()     // accepted, since pick is read before the first await
    await tick()
    return key.length
}

async fn alsoBad(cb: fn () -> undefined) -> 'static Promise<undefined> {
    await tick()
    cb()                 // error: the written 'static promise may be kept for the whole program, but 'cb' is read after the first await; write `-> Promise<undefined>` to let the promise hold it
}
```

A rendered prefix follows L3 and D17's rendering. A lifetime that connects an
input to the result is named. A `'static` returned promise is rendered when the
signature has input lifetimes, since a bare one would read back as tied to them.
[#2601](https://github.com/escalier-lang/escalier/issues/2601) adds the type,
and [#2438](https://github.com/escalier-lang/escalier/issues/2438) adds the
checks.

### 3.5 A5. A suspension point is a call to code the checker cannot see

While a body is suspended at an `await`, other code runs. That code reaches the
suspended body's values only through paths that escaped the frame, such as a
closure stored with a `'static` lifetime or module-level state. E4 and E2
already treat a `'static` path as live at every later point, so an `await`
needs no exclusivity rule of its own for those paths.

An `await` is still a use of each borrow a stored closure holds, the same as a
call that may run the closure, as E4 states. A closure kept in `sink` that
writes `p` makes an `await` with an immutable borrow of `p` live across it
report
`this await may run 'sink', which writes 'p' while it is borrowed as immutable`.

E4 also makes a call to a module-level function a use of the module-level
bindings that function writes, and an `await` of such a call runs it before the
body resumes. While the body is suspended, the event loop may run any other
task, and that task may call any module-level function. So an `await` counts as
a call that may run every module-level function. It uses each module-level
binding some module-level function writes, as a mutable path. Section 8.6 shows
the case, and [#2600](https://github.com/escalier-lang/escalier/issues/2600)
implements it. A `yield` is the same, since the consumer may run any code before
it calls `next` again.

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

async fn go() -> Promise<undefined> {
    val p = loadOr()
    try {
        await &p
    } catch {
        e => report(e),   // error: cannot use borrowed &MyError as owned object
    }
}
```

### 3.7 A7. A promise with nothing to write still warns under D12

D12 warns on a `mut` binding or a `&mut` borrow whose type has no writable slot.
A3 makes `&mut p` grant writes to the promise's contents, so whether a `Promise`
has a writable slot depends on `T`. It is judged like a class whose one
`readonly` field holds `T`. The field cannot be replaced, and the owned layers
of `T` can be written:

```esc
async fn fetchNum() -> Promise<number> { return 5 }

fn k() -> undefined {
    val mut q = fetchNum()    // warns, since Promise<number> has no writable slot
    val mut r = load()        // accepted, since &mut r can write the Flat inside
}
```

T9 and D18 already widen the literals of an owned layer of a `mut` binding, so
`val mut r = Promise.resolve({x: 1})` settles to `Promise<{x: number}>`.

### 3.8 A8. A promise's hold ends when the promise is known to have settled

A4 keeps an `'a Promise`'s `'a` live until the promise settles. The checker
cannot see a promise settle at runtime. It ends the hold where the program
shows the promise has settled, follows the promise where it moves, and keeps
`'a` for `'static` where it cannot follow it.

A promise is *dropped* when its holder's last use is not an `await` of it.
That covers a call whose result is discarded, as in `show(&f)` written as a
statement, a binding that dies unawaited, and a slot overwritten before its
promise is awaited. JavaScript has no drop operation, so dropping is something
the checker reads from the program. The promise's body keeps running.

The rules:

- **An `await` ends the hold.** Awaiting the promise in any of A1's forms
  ends it.
- **A move passes the hold along.** A move or copy of the promise into a holder
  the checking model follows, such as a local, an array element, or a field of
  a local, passes the hold to that holder, as L1 passes a borrow.
- **A combinator that waits for every input ends each input's hold.** An
  `await` of `Promise.allSettled` ends the hold of each promise passed in,
  since its result settles only after every input has. `Promise.all` waits for
  every input only when they all fulfil. It rejects as soon as one input
  rejects, while the other inputs' bodies keep running. So an `await` of
  `Promise.all` ends each input's hold on the path where it fulfils, and on
  the path where it throws each input keeps its hold for `'static`.
  `Promise.race` and `Promise.any` settle after the first input, so they end
  no other input's hold on either path.
- **Anything else keeps the hold for `'static`.** A dropped promise, and one
  stored where the checker cannot follow it, such as module-level state or a
  field of a parameter, keeps `'a` for the rest of the program.

```esc
async fn both() -> Promise<number> {
    val mut a: Flat = {x: 1}
    val mut b: Flat = {x: 2}
    val ps = [show(&a), show(&b)]
    a.x = 3              // error: cannot assign to 'a.x' while it is borrowed as immutable by 'ps'
    val [m, n] = await Promise.all(ps)
    b.x = 4              // accepted, since Promise.all fulfilled, so both promises have settled
    return m + n
}

async fn bothOrZero() -> Promise<number> {
    val mut a: Flat = {x: 1}
    val mut b: Flat = {x: 2}
    val ps = [show(&a), show(&b)]
    val n = try {
        val [m, k] = await Promise.all(ps)
        m + k
    } catch {
        e => 0           // show(&a) may have rejected while show(&b) still runs
    }
    b.x = 4              // error: cannot assign to 'b.x' while it is borrowed as immutable by 'ps'
    return n
}
```

A dropped promise that holds borrows freezes them without anything in the
program saying so. It reports a warning, `DroppedPromiseWarning`. A
`'static` promise holds nothing, so dropping one never warns. `void e` drops
a promise on purpose and silences the warning. The hold still lasts for
`'static`:

```esc
fn forget() -> undefined {
    val mut f: Flat = {x: 1}
    show(&f)             // warns: the promise 'show' returns is never awaited, so 'f' stays borrowed for the rest of the program; await it, or write `void` before the call to drop it on purpose
    f.x = 2              // error: cannot assign to 'f.x' while it is borrowed as immutable by the promise 'show' returns
}

fn prefetch(doc: Doc) -> undefined {
    void save(&doc)      // accepted with no warning, and doc stays borrowed for 'static
}
```

`void` is a type keyword today.
[#2610](https://github.com/escalier-lang/escalier/issues/2610) adds `void e` as
an expression whose value is `undefined`, together with the hold tracking and
the warning. [#2438](https://github.com/escalier-lang/escalier/issues/2438)
keeps every case but an `await` of the promise itself at `'static`, which is
sound but stricter.

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
gen fn g() -> Generator<Flat, undefined, unknown> {
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
gen fn h() -> Generator<&Flat, undefined, unknown> {
    val mut buf: Flat = {x: 0}
    yield &buf
    buf.x = 1    // error: cannot assign to 'buf.x' while it is borrowed as immutable
}

gen fn fresh() -> Generator<{x: number}, undefined, unknown> {
    for i in [0, 1, 2] {
        yield {x: i}    // accepted, since each value is new
    }
}
```

Yielding a borrow of a local is accepted for the reason L1 gives for
returning one: a borrow may outlive the binding that owns its place, so `h`
yielding `&buf` is accepted as L1's `make` returning `&p` is. D16 does not turn
the yielded borrow into an owned value, as it would a returned borrow that
nothing else reaches. The body keeps running after the `yield` and may still
read `buf`, so something else does reach it.

The consumer's side needs no rule. After `yield &x`, E2 rejects every later
write to `x` in the body, so the consumer's `&Flat` observes no mutation.
After `yield &mut x`, the body may still write `x`, since E2 allows several
mutable paths. The consumer's `&mut` promises nothing about other writers, so
nothing is broken. A yielded borrow of a local renders with an elided
lifetime, as `Generator<&Flat, undefined, unknown>`. One that ties to a
parameter writes the lifetime it shares with the parameter:

```esc
gen fn items<'a>(p: &'a {inner: Flat}) -> Generator<&'a Flat, undefined, unknown> {
    yield &p.inner
}
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

fn go() -> undefined {
    val mut it = sum()
    val mut a: Flat = {x: 1}
    it.next(a)
    a.x = 2      // error: use of moved value 'a'
}
```

Inside the body, `yield` evaluates to the value `next` sent, at type `TNext`.
`val first = yield 0` binds an owned `Flat`. `it` is a `val mut` binding
because `next` takes `&mut self`, by X3.

### 4.4 G4. A generator holds the borrows its body reads while it is live

A `gen fn` body runs only when the consumer calls `next`, so every read in
the body happens after the call has returned. A generator type takes a lifetime
prefix the way A4's promise does, as in `'a Generator<Y, R, N, E>`. Its
lifetime bounds the borrows the generator's body reads. A written generator
return type's elided prefix is tied to every input, as a promise's is in A4. A
generator closure with no written return type infers it from each borrowed or
call-only `fn` parameter its body reads.

An `'a Generator` keeps `'a` live while the generator value is live, by L1.
This is lazier than A4. An `async fn` body keeps running after its promise is
dropped, and a generator body stops when the generator does.

```esc
gen fn each(p: &Flat) -> Generator<number, undefined, unknown> { yield p.x }
// each: fn <'a>(p: &'a Flat) -> 'a Generator<number, undefined, unknown>

fn run() -> undefined {
    val mut f: Flat = {x: 1}
    val it = each(&f)
    f.x = 2      // error: cannot assign to 'f.x' while it is borrowed as immutable by 'it'
    for n in it {}
}

fn ok() -> undefined {
    val mut f: Flat = {x: 1}
    for n in each(&f) {}
    f.x = 2      // accepted, since the generator is dead
}
```

A call-only `fn` parameter is tied to the generator the same way, so
`gen fn each2(cb: fn () -> number) -> Generator<number, undefined, unknown> { yield cb() }`
reads as
`fn <'a>(cb: 'a fn () -> number) -> 'a Generator<number, undefined, unknown>`,
and the body may call `cb` on any later `next`.

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
| `for x in r`, with `r: &mut Array<T>` | `r` is copied, by F2, and stays usable after the loop | `&mut T` |
| `for x in mk()`, any `e` that is not a place | Nothing | `T`, owned |

A loop over an owned source is the source's last owner. An owned container holds
each reference-shaped element in exactly one slot, by X3. So each element
reaches `x` once and nothing else reaches it afterward, and `x` owns it. A loop
over a borrow binds borrows with the borrow's mutability, the same way F4 and
D14 say a pattern over a borrow binds them. The loop's pattern is a pattern.

```esc
fn go(arr: Array<Flat>) -> undefined {
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

fn bump(mut arr: Array<Flat>) -> undefined {
    for f in &mut arr {
        f.x = f.x + 1        // accepted
    }
}

fn bumpAll(r: &mut Array<Flat>) -> number {
    for f in r {
        f.x = 1              // accepted, since f is &mut Flat
    }
    return r.length          // accepted, since the loop copied r
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

The borrow lifetimes combine the two laziness rules. An async generator's type
takes a lifetime prefix, as in `'a AsyncGenerator<Y, R, N, E>`. The body runs
from a `next` until its next `yield`, even if the consumer drops the promise
`next` returned. So `'a` stays live while the generator is live and until the
promise of its last `next` settles.

A yielded borrow lives as long as the generator, by G2, so an `await` inside
the body cannot let the body write a value the consumer still holds. The
consumer may hold a yielded borrow while the body runs ahead to its next
`yield`, and G2 already rejects any write the body would make.

### 6.2 S2. `yield` in an async generator awaits a promise operand

JavaScript awaits a promise passed to `yield` inside an async generator, so the
consumer receives the settled value. `yield e` there is `yield await e`, and A1
then G1 decide it:

```esc
async gen fn stream() -> AsyncGenerator<Flat, undefined, unknown> {
    val p = load()
    yield p          // moves p and yields the Flat it settles to
    yield &p         // error: use of moved value 'p'
}

async gen fn cached() -> AsyncGenerator<&Flat, undefined, unknown> {
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
  primitives and functions. F2's sentence that a generic `&T` whose `T` is a
  promise stands for `T` changes too, since A3 distributes the borrow instead. A
  promise is reference-shaped, which matches the checker today, since
  `isReferenceShaped` in [moves.go](../../internal/solver/moves.go) treats a
  `Promise` class instance as movable. Its doc comment also says a promise is a
  value type and changes with it.
- **F1.** The flow-site table gains three rows. `await e` has an owned
  destination. `yield e` has the generator's yield type as its destination.
  `for x in e` has an owned destination. Each takes a written borrow the way an
  argument does. The `return` row notes that a `return` in an `async fn`
  settles the promise, so its value reaches every awaiter, and A1 governs them.
- **F4.** The list of places that borrow without a written `&` gains a loop
  over a borrow, which binds borrows as a pattern does. A `.then`, `.catch` or
  `.finally` receiver is already covered as a method receiver.
- **E4.** A pending promise or generator holds the borrows its lifetime
  prefix names, the way a kept closure holds its captures. An `'a Promise`
  holds them until it settles and an `'a Generator` while it is live, by A2,
  A4 and G4. E4 already makes a call to a module-level function, and an
  `await` of one, use the module-level bindings it reaches. A5 extends that to
  every `await` and `yield`, which may let other code run.
- **D17.** Its lifetime prefix extends from function types to promise and
  generator types, by A4 and G4. Rules 2 and 4 then apply to them as written:
  rule 2 elides a returned promise's lifetime, and rule 4's capture inference
  becomes the inference of what an async or generator closure holds when its
  return type is unwritten. Rule 2's
  clause that a call-only parameter may not be read in an `async` or
  generator body after the call returns is replaced. Such a read ties the
  parameter's lifetime to the returned promise or generator, which the rule's
  existing exception for a result tied to the parameter allows. Its rendering
  bullets gain the case of a `'static` returned promise or generator.
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
  promise parameter as X1 states. A callback parameter of a function that
  returns a promise is not call-only by default, since the elided promise
  lifetime holds it until the promise settles. I2 names `Promise.then` among the
  APIs that need a `'static fn` callback. A2 gives the reaction methods `'a fn`
  callbacks tied to the promise they return instead, so
  [#2573](https://github.com/escalier-lang/escalier/issues/2573) leaves `then`,
  `catch` and `finally` to
  [#2438](https://github.com/escalier-lang/escalier/issues/2438).
- **Comparison with Rust.** The table gains rows for A1, A2, A3, A4, G2 and
  R1, from section 9.
- **Conformance tests.** The flow matrix gains `await`, `yield` and the loop as
  flow sites, so each column becomes a test at each. The per-rule list gains
  the examples in sections 3 to 6.
- **Checking model.** `await`, `yield` and the loop header emit the same
  `copy`, `borrow` and `move` events as any other flow site. A4 and G4 add the
  returned promise or generator as a holder of each argument borrow, and A8
  follows a promise through moves and combinators as it follows a borrow.

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
[#2444](https://github.com/escalier-lang/escalier/issues/2444) adds that form of the message.

### 8.3 What `catch` returns when the fallback is owned

`p.catch(fn (e) { return {x: 0} })` produces a promise that settles either to
`p`'s value, a `&'a Flat`, or to a fresh `Flat`. That union mixes ownership.
The options are:

- **Reject it by T8** and ask the callback to write `&{x: 0}`. L1 lets that
  borrow reach `'a`, since nothing else reaches the fresh value. The result is
  then `Promise<&'a Flat>`.
- **Join the union to a borrow implicitly.** The callback's owned value would
  be treated as a borrow. This hides a borrow F4 says is written.

Recommendation: reject by T8. The diagnostic is the mixed-ownership one:
`mixed-ownership union: members disagree on ownership; make ownership uniform first`.
A callback that returns a value type, or a promise of one, is not affected.

### 8.4 A dropped promise keeps the borrows it holds for `'static`

A8 keeps a dropped `'a Promise`'s `'a` live for `'static`. A
fire-and-forget call such as `void save(&doc)` then freezes `doc` for the
rest of the program, when `save`'s promise holds `doc`. A shorter lifetime
would be unsound, since nothing ends a running async body. A caller that wants
`doc` back writes `await save(&doc)`, or passes an owned copy. A callee whose
promise holds nothing, such as `keyed` in A4, costs a fire-and-forget caller
nothing.

Recommendation: keep A8 as stated.

### 8.5 The reaction callback's parameter is a borrow

A2 gives `onfulfilled` a `&'a T`, where `'a` lasts until the reaction's
result settles. The alternative is an owned `T` for the first reaction only,
which no type can express, since the order of reactions is a runtime fact. A
callback that needs an owned value builds one from the fields it reads, and a
caller that needs the value owned writes `await p` instead of registering a
reaction.

A2 also considered tying `'a` to the lifetime of `p` itself. That is
unsound, since dropping `p` does not cancel its reactions. It also considered
ending `'a` when the caller awaits `p` rather than the reaction's result. The
specification runs earlier reactions on `p` first, but an async callback's
body can still be running after its own promise returns, so only the
reaction's result covers it.

Recommendation: keep A2 as stated.

### 8.6 Module-level state written while a body is suspended

A borrow of a module-level binding held across an `await` can observe a write
that another task makes while the body is suspended:

```esc
var mut cfg = {x: 0}

fn bump() -> undefined { cfg.x = cfg.x + 1 }

async fn show() -> Promise<number> {
    val r = &cfg
    await tick()         // error: this await may run 'bump', which writes 'cfg' while it is borrowed as immutable
    return r.x
}
```

E4 already rejects the same write when the body calls `bump` itself, whether
or not it awaits the call. An `await` of `tick` gives the event loop a chance
to run some other task that calls `bump`, which no call in the body shows.
The options are:

- **Treat an `await` as a call that may run every module-level function.**
  This is A5. It rejects `show`, and it accepts a borrow of a binding no
  module-level function writes.
- **Leave suspensions alone.** This accepts `show` and lets `r` observe the
  write.

Recommendation: A5.
[#2600](https://github.com/escalier-lang/escalier/issues/2600) implements it.

### 8.7 Prefer `await` to a reaction whose result is dropped

A reaction whose result the caller awaits costs about what an `await` of the
same promise costs, since A2 ends its borrows when the result settles. A
reaction whose result is dropped or stored without an `await` holds its
borrows for `'static`. It freezes its receiver and every capture of its
callback for the rest of the program, since E2 rejects every write after a
`'static` immutable path.

How much a dropped reaction costs depends on its receiver:

- A temporary, as in `load().then(cb)`, costs nothing through its receiver,
  since nothing else can await that promise. Its callback's captures cost the
  same as those of any closure the runtime keeps, such as one passed to
  `setTimeout`.
- A promise held in a place, as in `p.then(cb)`, leaves `p` awaitable only as
  `&p`. Its value can never be owned or written afterward.
- Code that cannot `await`, such as a synchronous callback or a constructor,
  and code that must not wait for the promise, needs a reaction whose result
  it drops. A reaction on a temporary fits there.

A8's `DroppedPromiseWarning` reports any dropped reaction result that holds a
borrow. Inside an `async fn` or an async closure, a reaction whose receiver is
a place reports it with wording that points at `await` instead,
`ReactionOnPlaceWarning`:

```esc
async fn go() -> Promise<number> {
    val p = load()
    p.then(fn (v) { return v.x })   // warns: the promise `then` returns is never awaited, so 'p' stays borrowed for 'static and can never be moved or awaited by value again; await that promise, or await 'p' with `await &p` instead
    return (await &p).x
}
```

The warning does not fire on a temporary receiver, on a reaction whose result
the function awaits, or outside an `async fn`, where `await` is not available.
[#2447](https://github.com/escalier-lang/escalier/issues/2447) implements it.

## 9. Comparison with Rust

| Rule | Rust behaviour | Verdict | Note |
|---|---|---|---|
| A1 three `await` forms | `.await` takes the future by value. A future is polled once and then consumed | Differs | Escalier adds `await &p` and `await &mut p`, since a JavaScript promise can be awaited again and returns the same object. Rust's `Shared` future from `futures` is the closest match. It hands each awaiter a clone, where `await &p` hands out a borrow |
| A2 reactions borrow until their result settles | No reactions. A spawned task requires `'static` captures, and a scoped task borrows until its scope joins | Differs | An awaited reaction acts like a scoped task, and a dropped one like `tokio::spawn`, which needs `'static` |
| A3 borrow of a promise | `&Future` is not awaitable. `Shared<F>` yields clones | No equivalent | |
| A4 a promise holds the borrows its pending computation reads | An `async fn` returns `impl Future + 'a`, which holds its borrows until it is dropped | Differs | `'a Promise<T>` is the same shape as `impl Future + 'a`. Rust futures are lazy, so dropping one ends the borrows. A JavaScript `async fn` runs eagerly, so only settling ends them |
| A8 a promise's hold ends when it is known to have settled | Dropping a future cancels it. Futures are `#[must_use]`, and `join_all` waits for every input | Differs | Dropping a promise cancels nothing, so it keeps the hold for `'static`. Escalier warns only when the dropped promise holds borrows, and `void` silences it, as `let _ =` does in Rust |
| G1 three `yield` forms | Coroutines are unstable. `gen` blocks yield owned values | Differs | |
| G2 a yielded borrow lives as long as the generator | `Iterator::Item` cannot borrow the iterator. A lending iterator needs generic associated types | Differs | Escalier ties the borrow to the generator, not to each `next` |
| G4 a generator holds the borrows its body reads while live | An iterator holding `&'a T` is bounded by `'a` | Same | |
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
- A returned promise's lifetime prefix is elided, so by A4 it holds every
  argument until it settles.
  [#2602](https://github.com/escalier-lang/escalier/issues/2602) classifies
  built-in and Web API functions from ECMA-262 and WebIDL, and writes
  `'static Promise<T>` for a function that uses its arguments only before it
  returns, such as the `Promise` constructor, whose executor runs synchronously.
- `Promise.all`, `Promise.race`, `Promise.any` and `Promise.allSettled` take
  an iterable of promises. An owned array of owned promises moves them, and an
  array of `&p` borrows them, so the result holds borrows for the second.

I2 drops the promise case, as section 7 states.

### 10.2 X2. The `Promise` declaration

The reaction methods in
[prelude.esc](../../internal/interop/data/std/prelude.esc) take `&self` and a
plain `fn` callback today. They change to the form A2 gives. The `Promise`
entry in [mutability.go](../../internal/dts_to_esc/mutability.go) keeps them
off `&mut self`, and its comment changes to say they borrow their receiver
until the promise they return settles.

### 10.3 X3. `Iterable<T>`, `Iterator<T>` and `Generator<…>`

Two declarations of `[Symbol.iterator]` are involved, and they differ on
purpose:

- **The `Iterable<T>` interface** declares
  `[Symbol.iterator](self) -> Iterator<T>`. It is the type a loop or a library
  function consumes, so passing a value as an `Iterable<T>` moves it, as R1
  moves the source of a loop.
- **`Array`, `Map` and `Set`** declare their own `[Symbol.iterator]` with
  `&'a self` returning borrows, as the last paragraph of this section states.
  A direct call such as `arr[Symbol.iterator]()` leaves the container holding
  its elements, so the elements it hands out are borrows.

The checker does not compare the two declarations to decide whether a container
satisfies `Iterable<T>`. It applies R1 instead, and R1 gives:

- an owned `Array<T>`, `Set<T>` or `Generator<T, …>` satisfies
  `Iterable<T>` and is moved in, since a moved container is iterated once and
  then gone, so its elements can be handed out owned;
- a borrow `&'a Array<T>` satisfies `Iterable<&'a T>`, and `&'a mut Array<T>`
  satisfies `Iterable<&'a mut T>`.

Section 8.2 rejects the `&self` reading as the rule for a loop. It is still the
right declaration for a direct call on a container that stays live.

`Array.from(&arr)` then returns `Array<&Flat>`, which I3 already asks of a
shallow copy.

R1 and E1 both rely on an owned container holding each reference-shaped
element in exactly one slot. Two `Array` methods break that today.
`fill(&mut self, value: T, …)` writes one owned `value` into every slot it
covers, and `copyWithin` copies slots onto other slots. Each would let the
loop in `go` below hand one object to two iterations:

```esc
fn go(mut arr: Array<Flat>) -> undefined {
    arr.fill({x: 0})
    for f in arr {
        keep(f)    // the first two iterations would receive the same Flat
    }
}
```

So both methods require an element type that F2 copies, meaning a value type
or a `&` borrow. `arr.fill({x: 0})` on an `Array<Flat>` reports
``cannot call 'fill' on an array of owned elements, since it stores one value in several slots; fill an array of a value type or of `&` borrows``.
This is a new diagnostic, `DuplicatingArrayMethodError`, and `copyWithin`
reports it with its own name. A `TypedArray` holds only numbers and keeps both
methods unchanged.

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
2. **Whether a lending generator is worth adding later.** G2 rejects a body
   that reuses a buffer across yields. Recommendation: wait for a real program
   that needs it.

## 12. Plan

Each PR below implements its rules in the checker, adds tests that assert the
full diagnostic for every rejection in its sections, and updates
`requirements.md` and `docs/09_ownership.md` for the rules it makes true, as
section 7 lists. Each PR runs `UPDATE_SNAPS=true go test ./...` and
`UPDATE_FIXTURES=true go test ./cmd/...` and reviews the fixture deltas.

Each issue below is one PR. The table lists them in an order that respects their
dependencies. Two of the dependencies are open issues in the "Simplify
ownership, lifetime, and mutability rules and implementation" milestone.
[#2318](https://github.com/escalier-lang/escalier/issues/2318) and
[#2290](https://github.com/escalier-lang/escalier/issues/2290) bind `&mut`
borrows through a pattern over a `&mut` borrow and through a mutably borrowed
array, which R1 relies on for `for x in &mut arr`.

A4, G4 and S1 read a written return type by W1 of the written-signatures
design, so [#2438](https://github.com/escalier-lang/escalier/issues/2438) and
[#2439](https://github.com/escalier-lang/escalier/issues/2439) land after the
PR that implements W1, step 2 of that design's plan under
[#2611](https://github.com/escalier-lang/escalier/issues/2611).

| Issue | Rules | What the PR does | Depends on |
|---|---|---|---|
| [#2601](https://github.com/escalier-lang/escalier/issues/2601) | A4, G4 | Adds the lifetime prefix on promise and generator types, with its elision and rendering | |
| [#2426](https://github.com/escalier-lang/escalier/issues/2426) | A1, A6 | Gives `await` its three forms, removes `SharedAwaitIntoMutError`, and removes promise from the value types in `requirements.md` |  |
| [#2424](https://github.com/escalier-lang/escalier/issues/2424) |  | Distributes a borrow over a union, the rule A3 builds on |  |
| [#2438](https://github.com/escalier-lang/escalier/issues/2438) | A2, A4, A5 for stored closures | Infers an async body's promise lifetime and keeps an `'a Promise`'s `'a` live until it settles, which closes the `show` hole in section 2. Gives the reaction methods `'a Promise` results in `prelude.esc` per X2 | [#2426](https://github.com/escalier-lang/escalier/issues/2426), [#2601](https://github.com/escalier-lang/escalier/issues/2601) |
| [#2610](https://github.com/escalier-lang/escalier/issues/2610) | A8 | Follows a promise through moves and combinators to end its hold, adds `void e`, and warns on a dropped promise that holds borrows | [#2438](https://github.com/escalier-lang/escalier/issues/2438) |
| [#2600](https://github.com/escalier-lang/escalier/issues/2600) | A5 for module-level state | Makes an `await` or a `yield` use each module-level binding some module-level function writes, as section 8.6 describes |  |
| [#2427](https://github.com/escalier-lang/escalier/issues/2427) | A3, A7 | Normalizes a borrow of a promise to a promise of a borrow, refines the D12 warning per section 7, and adds `ReactionBorrowLifetimeError` | [#2426](https://github.com/escalier-lang/escalier/issues/2426), [#2424](https://github.com/escalier-lang/escalier/issues/2424), [#2438](https://github.com/escalier-lang/escalier/issues/2438) |
| [#2439](https://github.com/escalier-lang/escalier/issues/2439) | G4 | Infers a generator body's lifetime and keeps it live while the generator is, which closes the `each` hole in section 2 | [#2601](https://github.com/escalier-lang/escalier/issues/2601) |
| [#2428](https://github.com/escalier-lang/escalier/issues/2428) | G1 to G3 | Gives `yield` its three forms and keeps a yielded borrow live for the rest of the body |  |
| [#2440](https://github.com/escalier-lang/escalier/issues/2440) | R2 | Binds a fresh loop variable on each iteration in the control-flow graph |  |
| [#2441](https://github.com/escalier-lang/escalier/issues/2441) | X3 | Restricts `fill` and `copyWithin` to element types F2 copies, and adds `DuplicatingArrayMethodError` |  |
| [#2444](https://github.com/escalier-lang/escalier/issues/2444) | R1, R3, G5 | Gives `for` loops their three forms and `yield from` the same, since G5 is decided by R1, adds `ImmutableIteratorLoopError` and the loop form of the moved-value message from section 8.2 | [#2428](https://github.com/escalier-lang/escalier/issues/2428), [#2440](https://github.com/escalier-lang/escalier/issues/2440), [#2441](https://github.com/escalier-lang/escalier/issues/2441), [#2318](https://github.com/escalier-lang/escalier/issues/2318), [#2290](https://github.com/escalier-lang/escalier/issues/2290) |
| [#2442](https://github.com/escalier-lang/escalier/issues/2442) | X3, X4 | Changes the element-reading iterator methods and the `Iterable` and `Generator` declarations | [#2444](https://github.com/escalier-lang/escalier/issues/2444) |
| [#2443](https://github.com/escalier-lang/escalier/issues/2443) | S1 to S3 | Applies the rules to async generators, including the implicit `await` of a promise operand to `yield` | [#2426](https://github.com/escalier-lang/escalier/issues/2426), [#2438](https://github.com/escalier-lang/escalier/issues/2438), [#2444](https://github.com/escalier-lang/escalier/issues/2444) |
| [#2447](https://github.com/escalier-lang/escalier/issues/2447) | A2, A8 | Reports A8's dropped-promise warning for a reaction on a promise held in a place with wording that points at `await`, as section 8.7 describes | [#2438](https://github.com/escalier-lang/escalier/issues/2438), [#2610](https://github.com/escalier-lang/escalier/issues/2610) |
| [#2602](https://github.com/escalier-lang/escalier/issues/2602) | X1 | Classifies how long each promise-returning built-in and Web API function keeps its arguments, and writes the declarations to match | [#2601](https://github.com/escalier-lang/escalier/issues/2601) |

[#2426](https://github.com/escalier-lang/escalier/issues/2426),
[#2424](https://github.com/escalier-lang/escalier/issues/2424),
[#2601](https://github.com/escalier-lang/escalier/issues/2601),
[#2600](https://github.com/escalier-lang/escalier/issues/2600),
[#2428](https://github.com/escalier-lang/escalier/issues/2428),
[#2440](https://github.com/escalier-lang/escalier/issues/2440) and
[#2441](https://github.com/escalier-lang/escalier/issues/2441) need nothing else
and can land first.
[#2426](https://github.com/escalier-lang/escalier/issues/2426) implements A1's
borrow forms for an `await` operand alone, and
[#2427](https://github.com/escalier-lang/escalier/issues/2427) extends them to
any borrow of a promise.

Three related issues sit outside this milestone:

- [#2304](https://github.com/escalier-lang/escalier/issues/2304) implements the
  D12 warning. Whichever of it and
  [#2427](https://github.com/escalier-lang/escalier/issues/2427) lands second
  judges a promise as A7 states.
- [#2573](https://github.com/escalier-lang/escalier/issues/2573) marks the Web
  API callbacks a callee keeps. It leaves `then`, `catch` and `finally` to
  [#2438](https://github.com/escalier-lang/escalier/issues/2438), which gives
  them A2's form.
- [#2308](https://github.com/escalier-lang/escalier/issues/2308) binds
  `val mut r = await p` for a sole local promise. A1 settles the same program,
  so [#2426](https://github.com/escalier-lang/escalier/issues/2426) supersedes
  it.
