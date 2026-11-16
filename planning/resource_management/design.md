# Design: Scope-based resource cleanup

## 1. Scope

This document is the design for
[escalier-lang/escalier#2787](https://github.com/escalier-lang/escalier/issues/2787).
It replaces JavaScript's `finally` with a `defer` statement that is written next
to the code that acquires a resource and runs when the enclosing block exits by
any path. It also settles how the checker uses ownership to reject a use of a
resource after it has been disposed, and to report a resource that is never
disposed.

Terms and rule names such as F2 come from
[planning/ownership_rules/requirements.md](../ownership_rules/requirements.md).
Rules A1 to A8 and G1 to G5 come from
[planning/ownership_extensions/design.md](../ownership_extensions/design.md).
The rules this document adds are numbered C1 to C12.

## 2. Problem

A JavaScript program releases a resource in a `finally` block:

```ts
const conn = await db.connect(url)
try {
    const rows = await conn.query(sql)
    // forty more lines
} finally {
    await conn.close()
}
```

The release sits at the end of the `try` block, far from the `connect` call
that made it necessary. A reader checking that every resource is released has
to match each acquisition to a `finally` by hand. Each further resource adds a
level of nesting, and a resource acquired conditionally needs a flag that the
`finally` block tests.

Escalier inherits the same problem. The checker already knows where each owned
value's scope ends and which values have been moved, but nothing ties cleanup to
that knowledge. The [fallbacks design](https://github.com/escalier-lang/escalier/blob/claude/intelligent-lovelace-9u921m/planning/fallbacks/design.md)
leaves `finally` out of the expression form of `catch` for this reason.

Three goals follow:

1. Cleanup is written next to the acquisition.
2. A resource cannot be used after it has been disposed. This is a compile-time
   error, not a runtime check.
3. A resource that is never disposed is reported, so forgetting the cleanup is
   caught.

Goal 2 is a soundness property. It holds on every accepted program apart from
two gaps: a direct call to a dispose method that borrows its receiver, which
section 4.3 covers with a lint, and the standard-library methods that hand out
an element a collection still holds, which section 4.8 describes. Goal 3 is checked where the checker can see the
resource's type, and section 6 lists where it cannot.

## 3. Background

### 3.1 The TC39 proposal

[Explicit Resource Management](https://github.com/tc39/proposal-explicit-resource-management)
reached Stage 4 at the May 2026 plenary. Chrome 134, Firefox 141 and Node 24
ship it. Safari ships it only in Technology Preview, so it is not yet Baseline.
TypeScript has accepted the syntax since 5.2 and lowers it for older targets.

The proposal adds a declaration that registers a value for disposal at the end
of the enclosing block:

```ts
{
    using file = openSync(path)
    await using conn = await db.connect(url)
    // …
}   // conn[Symbol.asyncDispose]() is awaited, then file[Symbol.dispose]() runs
```

Its semantics, from the spec text:

- The dispose method is looked up once, when the declaration runs. A value with
  no callable `[Symbol.dispose]` throws a `TypeError` there, not at exit.
- `null` and `undefined` are accepted and skipped. Under `await using`, a skipped
  value still forces one `await` at exit, so leaving the block always yields to
  the event loop.
- Resources are disposed in reverse order of registration.
- A disposer that throws while an exception is already propagating produces a
  `SuppressedError`. Its `error` is the disposer's exception and its
  `suppressed` is the one that was propagating. Several failures nest, so the
  body's exception ends up at the bottom of the `suppressed` chain.
- Bindings are identifiers. Destructuring is not allowed.
- `using` is rejected at script top level and allowed at module top level.
- `for (using x of xs)` disposes each element at the end of its iteration.

`DisposableStack` is the same machinery as an object. `use(v)` registers `v` and
returns it, `adopt(v, f)` registers `f(v)`, `defer(f)` registers `f()`,
`move()` transfers everything to a new stack and marks the old one disposed, and
`dispose()` runs everything in reverse. `AsyncDisposableStack` is the awaited
variant. The design draws on Python's `contextlib.ExitStack`, which has the same
five operations.

The proposal ties disposal to a protocol on the value. It has no way to say that
cleanup runs only on failure, and `move()` stands in for that. It does nothing to
stop a program from using a value after disposing it.

### 3.2 Other languages

**Arbitrary code at scope exit.**

- *Go* `defer` is attached to the function, not the block. A `defer` in a loop
  holds every resource until the function returns. Arguments are evaluated at
  the `defer` and the call runs at return. `defer f.Close()` discards the error
  `Close` returns, which is a known source of lost writes.
- *Zig* `defer` is attached to the block. `errdefer` runs only when the block
  exits by returning an error, and `errdefer |err|` names that error. Deferred
  code cannot `return` or `try`, so cleanup cannot fail.
- *Swift* `defer` is attached to the block and cannot `return`, `break` or
  `throw` out.
- *D* has `scope(exit)`, `scope(success)` and `scope(failure)`, attached to the
  block. None of them can throw an exception out or `return`.
- *Odin* and *Jai* have block-attached `defer` with no error variant.

**A protocol on the resource.**

- *C#* `using` declarations dispose an `IDisposable` at the end of the block, and
  `await using` handles `IAsyncDisposable`. An exception from `Dispose` replaces
  the body's.
- *Java* try-with-resources closes each `AutoCloseable` in reverse order. When
  both the body and `close()` throw, the body's exception propagates and the
  other is attached with `addSuppressed`. That is the reverse of JavaScript,
  where the newest exception is outermost. Kotlin's `use {}` follows Java.
- *Python* `with` calls `__exit__`, which can suppress the exception by
  returning true. An exception raised in `__exit__` replaces the original.

**Destruction tied to ownership.**

- *Rust* runs `Drop` when an owned value's scope ends. `mem::drop(x)` moves `x`,
  so the borrow checker rejects a later use. `Drop` cannot return an error, so a
  type that can fail on close offers an explicit method beside it. A panic in a
  destructor during another panic aborts. `mem::forget` is safe, so a destructor
  is not guaranteed to run. Async drop is unsolved, since `drop` is synchronous
  and a future can be dropped at any `await`. The `scopeguard` crate adds
  `defer!` and a guard object that `into_inner` defuses.
- *C++* runs destructors in reverse construction order. They are `noexcept`, and
  a throw out of one terminates the program. Nothing stops a use after a move.
- *Swift* noncopyable types (`~Copyable`) have a `deinit`. A `consuming` method
  ends the value's lifetime, and the compiler rejects a later use. `discard self`
  inside such a method skips `deinit`.
- *Hylo* consumes a value through a `sink` parameter and rejects a later use.
- [Congard, Munch-Maccagnoni and Douence](https://arxiv.org/abs/2510.23517)
  (ESOP 2026) give a formal model of C++ and Rust destructors with exceptions.
  When every resource type has a destructor, a raise frees what is in scope in
  reverse order and every resource is freed exactly once. The model needs
  destructors that cannot raise and exceptions that hold no resources. A `move`
  changes the order destructors run in, which a program needs to free
  resources in anything other than reverse order. A closure that captures a
  resource is itself a resource, of a different type from a closure that can be
  copied.

**Linear types.** A *linear* value must be consumed exactly once, which rules
out forgetting it as well as using it twice.

- *Austral* checks that every linear value is consumed exactly once on every
  path. `closeFile(file)` takes the handle by value. Austral has no exceptions,
  which sidesteps the question of what runs when a block raises.
- *Vale* calls its version "higher RAII". A type with no implicit destructor must
  be passed to one of its consuming functions, which can take arguments and
  return values, and the compiler reports an owned value that leaves scope
  unconsumed.
- *Linear Haskell* puts linearity on the function arrow, as in `Handle %1 -> r`.
  *ATS* tracks ownership proofs that freeing consumes.
- Rust proposals for linear types (`!Leak`, `?Drop`) have stalled on panics,
  `mem::forget` and generic code.

*Effect-TS* registers a release action in an ambient `Scope` with
`acquireRelease`. The release runs on success, failure or interruption and is
told which.

### 3.3 The axes they differ on

| Axis | Choices |
|---|---|
| What runs | A protocol method on the value (JS, C#, Java, Python, Rust) or arbitrary code (Go, Zig, Swift, D) |
| Where it attaches | The block (most), the function (Go), or an explicit construct (Java, Python, Kotlin) |
| Failure-only cleanup | `errdefer`, `scope(failure)`, or a transfer of ownership that defuses the cleanup (`move()`, `pop_all()`, `into_inner`) |
| Cleanup that raises | Forbidden (Zig, Swift, D, C++), replaces the original (C#, Python, Go), or aggregated (Java, JS) |
| Use after dispose | Unchecked (JS, Go, Zig, C#, Java, Python, C++), rejected by move checking (Rust, Swift, Hylo), or rejected along with leaks by linear checking (Austral, Vale) |
| Async cleanup | Built in (JS, C#, Python, Effect) or absent (Rust, Go, C++) |

Escalier already has the move checking that Rust and Swift use to reject a use
after dispose. Its checked exceptions push toward Zig's answer for failing
cleanup, as section 4.5 argues. The remaining choices are arbitrary code over a
protocol, and whether to add Austral's leak check for the types that ask for it.
This design takes arbitrary code, and takes the leak check.

## 4. Proposal

### 4.1 Summary

```esc
fn copy(src: string, dst: string) throws IOError {
    val input = File.open(src)
    defer dispose(input)
    val mut output = File.create(dst)
    defer dispose(output)

    for chunk in input.chunks() {
        output.write(chunk)
    }
}
```

- `defer stmt` and `defer { … }` run their code when the enclosing block exits.
  Deferred code runs in the reverse order it was written, so `output` is
  disposed before `input`.
- `dispose(x)` and `disposeAsync(x)` are prelude functions that consume `x` and
  call its `[Symbol.dispose]` or `[Symbol.asyncDispose]` method. Because they
  consume their argument, the ordinary use-after-move check rejects a use of
  `x` after it is disposed.
- A *resource type* is a class whose dispose method consumes its receiver, as
  in `[Symbol.dispose](self)`. An owned value of a resource type must be
  consumed on every path out of its scope. Writing `defer dispose(x)` counts,
  and so does moving it out. Forgetting to do either is a compile error.
- Moving a scheduled resource out of its scope, such as returning it, cancels
  the deferral that would have disposed it. Ownership of the resource and the
  duty to dispose it move together.
- Deferred code raises nothing the checker knows of. A cleanup that can fail is
  written with a `catch` fallback.
- No `using` keyword. Acquiring a resource is an ordinary `val` binding.

### 4.2 The `defer` statement

```
deferStmt = 'defer' (block | expr)
```

`defer` takes an expression or a block. The expression form covers the common
case, a single call. The block form runs several statements as one deferral:

```esc
defer dispose(conn)

defer {
    metrics.record("closed", conn.id)
    dispose(conn)
}
```

**C1. A deferral runs when its block exits, by any path.** A *deferral* is the
code a `defer` statement schedules. It is attached to the innermost enclosing
block, not to the function as Go's `defer` is. The exits of a block are:

- falling off its end,
- a `return`,
- a `break` or `continue` that leaves it,
- a raise that leaves it, whether from a `throw`, a call, or an `await`.

A block's deferrals run in reverse order of the `defer` statements that
registered them. A `defer` statement that control never reaches registers
nothing, so a deferral after an early `return` does not run:

```esc
fn f(path: string) {
    val file = File.open(path)
    defer dispose(file)
    if file.isEmpty() {
        return         // disposes `file`
    }
    val lock = Lock.acquire(path)
    defer dispose(lock)
    process(&file, &lock)
}                      // disposes `lock`, then `file`
```

Block scoping is what makes a deferral in a loop body run once per iteration,
which is the case Go's function-scoped `defer` gets wrong:

```esc
for path in paths {
    val file = File.open(path)
    defer dispose(file)
    process(&file)
}                      // each `file` is disposed before the next opens
```

A resource that must outlive the loop goes into an array or a
`DisposableStack`, as section 4.8 describes.

**C2. Deferred code runs at exit and sees the bindings as they are then.** The
whole deferral is evaluated when the block exits, as in Zig and Swift. Go
evaluates a deferred call's arguments at the `defer`, which differs only when a
`var` is reassigned in between:

```esc
var label = "first"
defer log(label)
label = "second"
// logs "second"
```

**C3. Deferred code cannot leave the deferral.** `return`, and a `break` or
`continue` that targets a loop outside the deferral, are rejected inside
deferred code. So is `yield`. `await` is accepted only inside an `async fn` or
`async gen fn`, by C11. Swift and Zig impose the same restriction. JavaScript lets
a `finally` block `return`, which discards the exception in flight, and that is
the behaviour C3 rules out.

`defer` is rejected at module top level, and so is a module-level binding of a
resource type. A module's top-level code runs when the module is first
imported, so a resource acquired there is opened as a side effect of the
import. Nothing would dispose it either. A module's top-level block finishes
before the program does its work, and JavaScript has no reliable hook that runs
at program exit. Node's `process.on("exit")` runs only synchronous code,
browsers do not guarantee `pagehide`, and a `FinalizationRegistry` callback may
never run. The operating system reclaims file descriptors and sockets when the
process ends, but whatever the dispose method does, such as flushing a buffer,
is lost.

```esc
val log = File.create("app.log")
// ERROR: a module-level binding cannot hold the resource 'log'; acquire it in the
// function that uses it, or in the program's entry function, and pass it down
```

The rule covers `var` bindings too, so a lazily filled singleton such as
`var pool: Pool | undefined` is rejected. A resource that lives as long as the
program is acquired in the entry function under a `defer` and passed to the
code that needs it. It is disposed when the entry function's block exits, which
is before the program ends only if the entry function waits for the program's
work. An entry function that starts a server and returns hands control to the
event loop, and its deferral closes the server at once. It has to be `async`
and wait until the work is done:

```esc
async fn main() {
    val server = await Server.listen(8080)
    defer await disposeAsync(server)
    await server.closed()          // settles when the server shuts down
}
```

`defer` is a statement, so it can appear in any block, including the block of
an `if`, `match` arm, or `do` expression. A deferral in a block that produces a
value runs after the value is computed. The value cannot borrow from what the
deferral consumes, by C5 and the lifetime rules.

### 4.3 Disposing a resource

The prelude gains two functions:

```esc
export declare fn dispose<T: Disposable>(resource: T) -> undefined
export declare async fn disposeAsync<T: AsyncDisposable | Disposable>(
    resource: T,
) -> Promise<undefined>
```

`dispose(x)` calls `x[Symbol.dispose]()`. `disposeAsync(x)` calls and awaits
`x[Symbol.asyncDispose]()`, falling back to `x[Symbol.dispose]()` when the
first is absent, the same lookup `await using` performs. Both take their
argument by value, so by F2 the call moves `x` and any later use of it is a
use-after-move:

```esc
val file = File.open(path)
dispose(file)
file.read()
// ERROR: use of moved value 'file'
```

This holds for every disposable, including one typed by a `.d.ts` file. It does
not depend on how the dispose method's receiver is written, since the move
happens at the `dispose` call.

TC39 leaves a disposed value reachable, and expects each resource to reject
calls made after it is disposed. Its reasons, given in
[tc39/proposal-explicit-resource-management#161](https://github.com/tc39/proposal-explicit-resource-management/issues/161),
are that making the binding unreachable would put it back into its temporal
dead zone, which implementers objected to, and that a resource created without
`using` must guard itself anyway. Neither constraint applies to a compile-time
check. The thread also gives using a resource as a `Map` key as a reason, since
a program may want to remove the key after disposing the resource. A key needs
only the value's identity, and the Escalier program removes the key first. A
`Map<&File, V>` that holds a borrow of `file` as a key keeps `dispose(file)`
from checking while the borrow is live, so the checker requires
`cache.delete(&file)` before the dispose. A map that owns its resources, such as
a `Map<string, File>`, hands the owned value back on removal, and the program
disposes that.

They belong in the prelude because the language's own rules name
them. C6 below reports a resource that is never disposed, and its diagnostic
suggests `defer dispose(x)`. A program should not need an import to follow the
suggestion. `Disposable` and `AsyncDisposable` are prelude types already.

A direct call `x[Symbol.dispose]()` moves `x` only when the method's receiver
is `self`. Calling the method directly on a type whose receiver is `&self` or
`&mut self` leaves `x` usable, which is the one way to reach a disposed value
in an accepted program. A lint suggests `dispose(x)` at such a call.

A resource held in a field or an array slot cannot be moved out through a
borrow, so a `&mut self` method has no way to dispose the connection in
`self.conn` and store a new one. The prelude adds `replace` for this, as Rust
has `mem::replace`:

```esc
export declare fn replace<T>(place: &mut T, value: T) -> T
```

`replace(&mut p, v)` moves `v` into the place `p` and returns the value `p` held,
owned:

```esc
class Client {
    conn: Conn,
    [Symbol.dispose](self) -> undefined { dispose(self.conn) },
    reconnect(&mut self, url: string) -> undefined throws NetError {
        val old = replace(&mut self.conn, Conn.open(url))
        dispose(old)
    },
}
```

JavaScript has no reference to a place, so `replace` is an intrinsic. Codegen
evaluates `value` first, then reads the place into a temporary and assigns the
place, as in `const $v = Conn.open(url); const old = this.conn; this.conn = $v;`.
If `Conn.open` raises, the place still holds the old connection.

### 4.4 Resource types

**C4. A class whose dispose method consumes its receiver is a resource type.**
A class declares itself a resource by writing its `[Symbol.dispose]` or
`[Symbol.asyncDispose]` method with a `self` or `mut self` receiver:

```esc
class File {
    fd: number,
    read(&self) -> string { … },
    write(&mut self, s: string) -> undefined { … },
    [Symbol.dispose](self) -> undefined { fs.closeSync(self.fd) },
}
```

The receiver decides it because it already says what disposing means for
ownership. A consuming receiver says the value cannot be used afterwards, which
is the property a resource needs. A class whose dispose method borrows its
receiver, such as an iterator whose `[Symbol.dispose]` calls `return()`, is
disposable but not a resource. Every iterator in the prelude is disposable in
that weaker sense, and C6 does not apply to it. A generator whose body holds
cleanup at a `yield` is a resource by C12.

A structural type can also be a resource. An object type, tuple type or array
type that owns a value of a resource type is a resource type, through any
number of levels of ownership:

- `{file: File, name: string}`, `[File, File]` and `Array<File>` are resource
  types.
- `{file: &File}`, `fn () -> File` and `Promise<File>` are not. A borrow does
  not own its referent, and functions and promises are value types.

A closure that moves a resource it captures owns that resource, so it is a
resource too. By D2 it can be called once, and calling it consumes it, so C6
requires it to be called or moved on every path. Its type is a `move fn` type
from [#2585](https://github.com/escalier-lang/escalier/issues/2585), which F2
never copies:

```esc
fn closer(file: File) -> move fn () -> undefined {
    return move fn () { dispose(file) }
}

fn f(path: string) {
    val close = closer(File.open(path))
    defer close()
}
```

Until #2585 lands, a closure that moves a resource capture is rejected, since an
ordinary `fn` value can be copied and a copy that disposes its capture would
dispose it twice.

A class that owns a resource in a field must itself be a resource type, since
nothing else can dispose the field once an instance is dropped:

```esc
class Conn {
    socket: Socket,
    send(&mut self, bytes: Uint8Array) -> undefined { … },
}
// ERROR: class 'Conn' owns the resource 'socket' but its [Symbol.dispose] does
// not consume 'self'; declare `[Symbol.dispose](self)` and dispose 'socket' in it
```

A class that declares the method with a consuming receiver disposes its fields
in the body like any other owned value:

```esc
class Conn {
    socket: Socket,
    log: File,
    [Symbol.dispose](self) -> undefined {
        defer dispose(self.log)
        dispose(self.socket)
    },
}
```

**C5. A deferral that consumes a binding takes over the binding's disposal.**
A `defer` whose code moves an owned binding, such as `defer dispose(file)`,
*schedules* that binding. The binding stays usable as a place until the block
exits. It can be read, borrowed, mutated through, and passed to borrowing
parameters, and none of those affect the deferral, since the deferral's move
happens after every use in the block.

**C6. An owned resource must be consumed on every path out of its scope.** This
makes resource types linear, where every other owned value is affine. An
*affine* value may be consumed at most once and is otherwise dropped silently.
A *linear* value must be consumed exactly once. Each owned binding of a
resource type is in one of three states at each point of the control-flow
graph:

| State | Reached by | At scope exit |
|---|---|---|
| unscheduled | the binding is initialized | error |
| scheduled | a `defer` that consumes it, by C5 | its deferral disposes it |
| moved | a move by F2, including a call to `dispose` | nothing |

Any move consumes a resource, not only `dispose`. A resource type can offer
several consuming methods, such as a transaction's `commit(self)` and
`rollback(self)`, and calling either one discharges C6. Vale calls this "higher
RAII". Section 5.3 shows how it combines with C7.

The checker reports a binding that is unscheduled on some path to an exit:

```esc
fn f(path: string) {
    val file = File.open(path)
    process(&file)
}
// ERROR: the resource 'file' is never disposed; write `defer dispose(file)`
// after it is acquired, or move it
```

The check covers raise edges. A binding that is still unscheduled when a call
with known throws runs leaks on the path where that call raises:

```esc
fn f(a: string, b: string) throws IOError {
    val first = File.open(a)
    val second = File.open(b)   // may raise IOError
    defer dispose(first)
    defer dispose(second)
}
// ERROR: the resource 'first' is not disposed if 'File.open(b)' raises; move
// `defer dispose(first)` above it
```

Only known raise sites are weighed, the same limit [06 Error
Handling](../../docs/06_error_handling.md#uncovered-exceptions-are-rethrown)
places on `throws`. Writing `defer dispose(x)` on the line after the acquisition
is the habit C6 rewards, and it is never reported.

**C7. Moving a scheduled binding out cancels its deferral.** A scheduled
binding can still be moved, by `return`, into a field, or to a consuming
parameter. The move hands the resource to a new owner, and the deferral that
would have disposed it does not run on that path:

```esc
fn openPair(a: string, b: string) -> Pair throws IOError {
    val first = File.open(a)
    defer dispose(first)
    val second = File.open(b)   // if this raises, `first` is disposed
    defer dispose(second)
    return Pair(first, second)  // both move into the Pair; neither is disposed
}
```

This is how Rust's `Drop` behaves: a value is dropped by whoever owns it when
its scope ends. It covers what Zig needs `errdefer` for and what TC39's
`DisposableStack` needs `move()` for, which is acquiring several resources where
a failure part way must release the earlier ones and success must hand all of
them on.

Cancellation applies to a whole deferral. A block deferral that consumes two
bindings is cancelled when either moves, so the other becomes unscheduled again
and C6 reports it if nothing else consumes it. A deferral that consumes one
binding is the form to prefer.

**C8. A deferral that reads a binding keeps it from moving.** A deferral that
reads or borrows a binding without consuming it needs the binding at exit. A
move of that binding after the `defer` is rejected:

```esc
val mut stats = Stats()
defer report(&stats)
archive(stats)
// ERROR: cannot move 'stats' because the deferral at line 2 reads it when the
// block exits
```

The borrow rules apply to the deferral at each exit. A deferral that borrows
`stats` mutably conflicts with an immutable borrow of `stats` that is live at
the exit, such as one held by a closure stored in a `'static` slot.

### 4.5 Errors raised by cleanup

**C9. Deferred code raises nothing the checker knows of.** The throws sink for
deferred code is `never`, so a deferral that calls a function with known throws
is rejected unless it handles them:

```esc
declare fn flush(f: &mut File) -> undefined throws IOError

defer flush(&mut output)
// ERROR: cannot constrain IOError <: never; deferred code raises nothing, so
// handle the error inside it, for example with `catch`
```

The [fallbacks design](https://github.com/escalier-lang/escalier/blob/claude/intelligent-lovelace-9u921m/planning/fallbacks/design.md)
gives the short form:

```esc
defer flush(&mut output) catch e => log.warn("flush failed", e)
```

TC39 takes the other path. When the body raises and a disposer also raises, the
runtime throws a `SuppressedError` whose `error` is the disposer's exception and
whose `suppressed` is the body's. That fits poorly with checked exceptions:

- The body's `throws IOError` would reach the caller as
  `IOError | SuppressedError<…, IOError>`, and a catch arm naming `IOError` no
  longer matches the failure it was written for.
- The type escaping a `try` is `caught & ~handled`. A wrapper that nests one
  error inside another has no member that set difference can subtract.

C9 keeps the caller's `throws` clause what the body raises. A cleanup that
genuinely needs to report failure, such as committing a transaction, belongs in
the body as an ordinary call, not in a deferral. Zig makes the same choice,
since a `defer` there cannot use `try`.

A disposer can still raise a value no signature named, since any JavaScript can
throw. Section 4.7 lowers deferrals onto `DisposableStack`, so in that case the
runtime produces TC39's `SuppressedError` and the original exception is kept in
its `suppressed` field rather than lost, as a raising `finally` would lose it.

A resource type's dispose method declared with a `throws` clause is reported at
the declaration, with a note that `dispose` has nowhere to send it.

**C10. A raised value holds no resource.** A `throw` whose operand has a
resource type is rejected, and so is a promise type whose rejection type is a
resource type. A catch arm binds what it catches as `unknown`, so C6 could never
require the arm to dispose it, and the raise frees what is in scope while the
thrown value is still on its way out:

```esc
throw ConnectError(conn)
// ERROR: a thrown value cannot hold the resource 'conn'; dispose it and throw
// what describes the failure, such as `ConnectError(conn.url)`
```

### 4.6 Async cleanup and generators

**C11. A deferral may `await` inside an `async fn` or `async gen fn`.** The
`await` is written in the deferral, so every suspension point in the function
is still visible in its source:

```esc
async fn handle(req: Request) -> Promise<Response, DBError> {
    val conn = await DBConnection.create(req.dbUrl)
    defer await disposeAsync(conn)
    return await conn.query(req.sql)
}
```

The ownership rules treat the `await` as a suspension point at the block's
exit, by A5. Exiting the block runs the deferral and waits for it before the
function continues, so the function's promise settles only after `conn` is
disposed.

A rejection from the awaited cleanup follows C9. `disposeAsync` declares no
rejection type, and a deferral awaiting a promise whose rejection type is not
`never` handles it with `catch`.

In a `gen fn` or `async gen fn`, a block exits when the generator's body leaves
it, as for any function. A generator that is abandoned without being finished
runs its deferrals when `return()` is called on it, which is what a `for … in`
loop does on `break`. `return()` resumes the body at the paused `yield` as if a
`return` statement ran there. An iterator's `[Symbol.dispose]` calls
`return()`, and calling `return()` does not call `[Symbol.dispose]`. A
generator that is never finished and never has `return()` called on it never
runs its deferrals, which C12 makes the checker report.

**C12. A generator whose body holds cleanup at a `yield` is a resource.** Its
return type is a `ResourceGenerator`, a prelude type whose `return` and
`[Symbol.dispose]` consume the generator, so C4 makes it a resource type:

```esc
export declare interface ResourceGenerator<T, TReturn, TNext> {
    next(&mut self, value: TNext) -> IteratorResult<T, TReturn>,
    return(self, value?: TReturn) -> IteratorResult<T, TReturn>,
    [Symbol.dispose](self) -> undefined,
}
```

A body *holds cleanup* at a `yield` when a deferral is registered at that point,
or when it owns a value of a resource type there. The signature writes the
type, and the checker verifies it against the body, since a written type means
exactly what it says
([planning/explicit_signatures/design.md](../explicit_signatures/design.md)).
`throws` clauses are left out of the examples below to keep them short.

```esc
gen fn lines(path: string) -> ResourceGenerator<string, undefined, undefined> {
    val file = File.open(path)
    defer dispose(file)
    for line in file.lines() {
        yield line
    }
}

gen fn badLines(path: string) -> Generator<string, undefined, undefined> {
    val file = File.open(path)
    defer dispose(file)
    for line in file.lines() {
        yield line
        // ERROR: 'file' is still to be disposed at this yield, so abandoning the
        // generator would leak it; declare the return type ResourceGenerator<…>
    }
}
```

A `for` loop consumes the generator by R1, and `break` or `return` inside the
loop calls `return()`, so a loop needs nothing written:

```esc
fn firstError(path: string) -> string | undefined {
    for line in lines(path) {
        if line.startsWith("ERROR") {
            return line            // the loop calls return(), which closes the file
        }
    }
    return undefined
}
```

A generator driven by hand is consumed by `dispose(it)` or `it.return()`, and C6
reports one that is neither:

```esc
fn header(path: string) -> string | undefined {
    val mut it = lines(path)
    defer dispose(it)
    return it.next(undefined).value
}

fn headerLeaks(path: string) -> string | undefined {
    val mut it = lines(path)
    return it.next(undefined).value
}
// ERROR: the resource 'it' is never disposed; write `defer dispose(it)` after it
// is acquired, or move it
```

Returning the generator moves it, which hands the duty to dispose it to the
caller, as for any resource:

```esc
fn errors(path: string) -> ResourceGenerator<string, undefined, undefined> {
    return lines(path)
}
```

`return` can consume the generator because C3 rejects `yield` inside deferred
code. In JavaScript a `finally` block can `yield`, which leaves a generator
suspended after `return()`. In Escalier `return()` always runs the generator to
completion, so the generator can do nothing afterwards.

Other iterators are not resources. Under
[#2442](https://github.com/escalier-lang/escalier/issues/2442), `arr.values()`
is an `ArrayIterator<&'a T>`, which has no `return()` and releases nothing when
it is dropped. A generator whose body holds no cleanup at any `yield` keeps the
plain `Generator` type.

An `async gen fn` follows the same rule with a `ResourceAsyncGenerator`, whose
`return` and `[Symbol.asyncDispose]` consume the generator. A generator driven by
hand is disposed with `defer await disposeAsync(it)`.

Iterator helpers take their source by value, by X5, so `lines(path).filter(f)`
owns a resource. C4's containment rule covers object, tuple and array types, but
a helper returns a declared `IteratorObject`, whose dispose method borrows its
receiver. Section 6 leaves open how the helpers return a resource form when
their source is a `ResourceGenerator`.

### 4.7 Code generation

A block with at least one deferral lowers to a `DisposableStack` held in a
`using` declaration. Each `defer` becomes a call to the stack's `defer` method:

```esc
fn f(path: string) {
    val file = File.open(path)
    defer dispose(file)
    process(&file)
}
```

```js
function f(path) {
    using $stack = new DisposableStack();
    const file = File.open(path);
    $stack.defer(() => dispose(file));
    process(file);
}
```

`DisposableStack.defer` runs its callbacks in reverse order and combines errors
with `SuppressedError`, which gives C1 and the runtime half of C9 with no code
of Escalier's own. A block whose deferrals contain `await` uses an
`AsyncDisposableStack` held in `await using`, and each deferral becomes an
`async` arrow.

A deferral that C7 can cancel gets a flag, cleared at each move that cancels
it. A flag is only emitted where some path moves the binding after the
`defer`:

```js
function openPair(a, b) {
    using $stack = new DisposableStack();
    const first = File.open(a);
    let $first = true;
    $stack.defer(() => { if ($first) dispose(first); });
    const second = File.open(b);
    let $second = true;
    $stack.defer(() => { if ($second) dispose(second); });
    $first = false;
    $second = false;
    return new Pair(first, second);
}
```

The flags are cleared before the call that receives the moved values, so the
callee owns them if it raises.

Native `using` declarations need a runtime that ships the proposal, and Safari
does not yet. Until the target setting says otherwise, codegen emits the
`try`/`catch`/`finally` shape TypeScript emits, with a helper equivalent to
TypeScript's `__disposeResources`. The helper reproduces the `SuppressedError`
chaining, and a polyfill supplies `DisposableStack` and `SuppressedError` where
they are missing. A target that ships the proposal gets the native form above.

`DisposableStack` looks up nothing at registration, since `defer` registers a
closure. That differs from `using`, which looks up the dispose method when the
declaration runs. A value whose `[Symbol.dispose]` is missing fails at the call
to `dispose` at exit. The checker has already required `T: Disposable` there,
so only a value whose type lies about it reaches that failure.

### 4.8 Collections of resources

A deferral is attached to a block, so resources that must live until a later
point, and whose number is decided at run time, are collected in an array or a
`DisposableStack`.

An array of resources is a resource type by C4, so C6 requires it to be
consumed. `push` takes its items by value and moves a resource in, and `pop`
hands one back owned. The prelude adds `disposeAll`, which disposes every
element:

```esc
export declare fn disposeAll<T: Disposable>(resources: Array<T>) -> undefined
```

```esc
fn mergeAll(paths: Array<string>) -> undefined throws IOError {
    val mut files: Array<File> = []
    defer disposeAll(files)
    for path in paths {
        files.push(File.open(path))
    }
    merge(&files)
}
```

By C5, `files` stays usable after the `defer`, so the loop can push into it.
`disposeAll` disposes the newest element first, as a block's deferrals run, and
keeps going when one raises a value no signature named. It lowers to a
`DisposableStack` that takes every element and is then disposed, so those
errors combine into a `SuppressedError`. A loop of `dispose` calls would stop at
the first raise and leave the remaining elements open.

A collection owns each element in exactly one slot only if no method hands out
a second owner. Several standard-library methods do, by typing an element the
collection keeps as owned:

- `Array` methods that copy elements, such as `slice` and `filter`, return an
  element, such as `at` and `find`, or pass one to a callback, such as `map`
  and `forEach`, in [#2788](https://github.com/escalier-lang/escalier/issues/2788).
- `Map.get`, `WeakMap.get`, the `forEach` callbacks of `Map` and `Set`, and
  `Map.groupBy` and `Object.groupBy`, in [#2789](https://github.com/escalier-lang/escalier/issues/2789).
- The `Set` methods `union`, `intersection`, `difference` and
  `symmetricDifference`, in [#2790](https://github.com/escalier-lang/escalier/issues/2790).
- `WeakRef.deref`, in [#2792](https://github.com/escalier-lang/escalier/issues/2792).

The iterator methods of all three collections are covered by [#2442](https://github.com/escalier-lang/escalier/issues/2442). Goal 2
needs each of these methods to return borrows. Until they do, a program can
read a resource through a second owner after `disposeAll` has consumed the
first.

Lookups have the opposite problem. `has`, `get`, `delete`, `includes` and
`indexOf` take the value to search for by value, so looking up a resource moves
it, and `cache.delete(&file)` in section 4.3 is rejected today.
[#2791](https://github.com/escalier-lang/escalier/issues/2791) makes those parameters borrows.

A `DisposableStack` holds resources of different types, and values paired with
a disposer of the program's choosing through `adopt`. The interop overlay
retypes its members so that the stack owns what it holds:

```esc
declare class DisposableStack {
    use<'a, T: Disposable>(&'a mut self, value: T) -> &'a mut T,
    adopt<'a, T>(&'a mut self, value: T, onDispose: 'static fn (value: T) -> unknown) -> &'a mut T,
    defer<'a>(&mut self, onDispose: 'a fn () -> unknown) -> undefined,
    move(self) -> DisposableStack,
    [Symbol.dispose](self) -> undefined,
}
```

`use` takes ownership of the value and hands back a borrow that lasts as long as
the stack is borrowed. TypeScript's `use<T>(value: T) -> T` returns the same
object it stored, which would leave two owners. Since `[Symbol.dispose]`
consumes the stack, `DisposableStack` is a resource type, and disposing it
requires every borrow from `use` to be dead:

```esc
fn openAll(paths: Array<string>) -> undefined throws IOError {
    val mut stack = DisposableStack()
    defer dispose(stack)
    val mut files: Array<&mut File> = []
    for path in paths {
        files.push(stack.use(File.open(path)))
    }
    merge(&mut files)
}
```

`move` consumes the stack and returns a new one holding everything, which is
TC39's pattern for handing a set of resources on after acquiring them all.

### 4.9 Replacing `finally`

`try … finally` is removed once `defer` lands. Cleanup that a `finally` block
did is written as a `defer` next to the acquisition, in the block that uses the
resource:

```esc
fn load(path: string) -> Config {
    return try {
        val file = File.open(path)
        defer dispose(file)
        parseConfig(file.read())
    } catch {
        e => defaults(e)
    }
}
```

The deferral belongs to the `try` block, so on a raise it runs before the catch
arm. The file is closed by the time `defaults(e)` runs, and the arm cannot reach
it, since `file` is not in scope there. A `finally` block runs after the arms,
and the design does not reproduce that order. A cleanup that has to run after the arms
goes in a `defer` in the enclosing block, placed after the code that acquires
what it releases.

No fix-it rewrites a `finally` block. Where its `defer` belongs depends on
where the resource it releases was acquired, which is often outside the `try`.
For one release the parser keeps recognizing `finally` and reports it with a
diagnostic that suggests a `defer` next to the acquisition. The release after
that removes it.

### 4.10 Interop

- A disposable that a `.d.ts` file types is not a resource type, since the
  conversion writes no consuming receivers. `dispose(x)` still moves it, so a
  use after `dispose` is rejected. C6 does not report forgetting it. The interop
  overlay can mark a type a resource by retyping its dispose method with a
  `self` receiver, as section 4.8 does for `DisposableStack`.
- Passing an owned resource to a `.d.ts` function with a bare parameter moves
  it, by F2. The callee is trusted to dispose it.
- An Escalier resource type compiles to a class with an ordinary
  `[Symbol.dispose]` method, so TypeScript code can hold it in a `using`
  declaration.
- [#1475](https://github.com/escalier-lang/escalier/issues/1475) proposes
  `using` for bringing a namespace's members into scope. This design takes no
  keyword from JavaScript's disposal syntax, so `using` stays free. A reader
  coming from JavaScript or C# will read `using` as disposal, which counts
  against that spelling for #1475, but the decision belongs to that issue.

## 5. Alternatives

### 5.1 TC39 `using` declarations

Escalier could adopt `using x = acquire()` as JavaScript spells it, or under
another keyword such as the `use` in the `DBConnection` example of
[planning/objects_and_classes/requirements.md](../objects_and_classes/requirements.md).
It would compile to itself and is one line shorter than a `val` followed by
`defer dispose(x)`.

It is not taken because `defer` covers more with one construct. Cleanup that is
not a dispose method, such as restoring a setting or logging, needs `defer`
anyway, and `using` would make two ways to write the common case. A `using`
binding also pairs acquisition with the protocol, so a value with two ways to
end, such as a transaction that commits or rolls back, has to pick one as its
dispose method. The cost `using` saves, forgetting the second line, is what C6
reports.

### 5.2 Implicit disposal when an owned value's scope ends

The checker knows where each owned value's scope ends, so it could insert the
`dispose` call itself, as Rust inserts `drop`. Then acquiring a resource would
need nothing written at all.

It is not taken for three reasons:

- Cleanup would be invisible in the source. A reader could not see which calls
  run at a block's end without knowing which types are resources.
- An async dispose method would add an `await` at every block exit that drops
  one. Section A5 of the ownership extensions makes each suspension point
  visible for the borrow rules, and a hidden one undoes that. This is the
  problem Rust has not solved for async drop.
- Many JavaScript values are disposable without being resources. Every
  iterator's `[Symbol.dispose]` calls `return()`, and disposing every owned
  iterator at scope end would change what programs do.

C6 and C7 keep most of what implicit disposal offers. The checker still decides
where a resource must be disposed and reports where it is not, and the program
writes the call where the reader can see it.

### 5.3 `errdefer`

Zig's `errdefer` runs only when the block raises. C7 covers its main use, since
a resource that is handed on is a resource that was moved, and the move cancels
its deferral. A transaction shows it, with `commit` and `rollback` both written
with `self` receivers:

```esc
fn transfer(db: &mut DB, from: string, to: string, amount: number) throws DBError {
    val tx = db.begin()
    defer tx.rollback()
    tx.debit(from, amount)
    tx.credit(to, amount)
    tx.commit()            // consumes `tx`, which cancels the rollback
}
```

A cleanup that should run only on failure but consumes nothing has no move to
cancel it, and `errdefer` would be the tool. Section 6 leaves it open until such
a case turns up.

### 5.4 A `defer` that makes the binding a borrow

A simpler rule than C7 makes a scheduled binding read as a borrow until the
block exits, so it can never be moved out. The checker needs no cancellation and
codegen needs no flags. It is not taken because it rules out the case section
5.3 shows, and the case in C7's `openPair`. Both would need an explicit
`DisposableStack` and its `move()`.

### 5.5 A leak check for every disposable

C6 could apply to every type with a dispose method rather than to classes whose
dispose method consumes its receiver. It is not taken because the prelude's
iterators are disposable, and requiring `defer dispose(it)` for every iterator a
program holds would be noise. Disposing most of them does nothing. C12 picks out
the generators whose `return()` runs cleanup, which are the iterators that need
disposing.

## 6. Open questions

- **Generic code.** A type parameter is affine, so a generic body can drop a
  value of its type. `fn ignore<T>(x: T) {}` accepts a `File` and drops it, and
  C6 does not see it. Rust accepts the same hole, since `mem::forget` is safe.
  The leak is not unsound for goal 2, since nothing can use the dropped file.
  Closing the hole needs a bound saying a type parameter may be instantiated
  with a resource, with C6 applied to the body under that bound. The proposal is
  to accept the hole in the first version.
- **Promises of resources.** `Promise<File>` is a value type, so a promise
  dropped without being awaited leaks the file it settles to. C6 starts to apply
  when the promise is awaited and the result bound. Section 8.4 of the ownership
  extensions already warns about some dropped promises, and that warning could
  be extended to a promise whose fulfilment type is a resource.
- **Sugar for acquire-then-defer.** If `val x = acquire()` followed by
  `defer dispose(x)` proves common enough to want one line, a form such as
  `defer val x = acquire()` could register `dispose(x)` and bind `x`. A variant
  that names its disposer, such as `defer(close) val x = acquire()`, would
  register `close(x)` and give any value a disposer at the binding. Both are
  sugar for a binding followed by a `defer`, so C5 and C7 apply unchanged. This
  waits on usage.
- **Iterator helpers over a `ResourceGenerator`.** A helper such as `filter`
  takes its source by value, so it owns the generator, but it returns an
  `IteratorObject` that is not a resource type. Each helper could gain an
  overload that returns a `ResourceIterator<T>` when its source is a
  `ResourceGenerator`, or `IteratorObject` could take a parameter saying whether
  it owns a resource.
- **`errdefer`**, as section 5.3 describes.

## 7. Implementation plan

1. **Syntax.** Add `DeferStmt` to the AST, visitor, parser and printer. Report
   `defer` at module top level, and `return`, `yield`, and a `break` or
   `continue` that leaves the deferral, by C3.
2. **Checking and codegen of deferred code.** Check deferred code against a
   `never` throws sink, by C9, and accept `await` in it only inside an async
   body, by C11. Add `dispose`, `disposeAsync`, `disposeAll` and the `replace`
   intrinsic to the prelude. Lower a block
   with deferrals to a `DisposableStack` or `AsyncDisposableStack` with the
   downlevel helper, as section 4.7 describes. Fixture tests cover the order of
   deferrals, each kind of exit, and an unknown throw from a disposer producing
   a `SuppressedError` that keeps the body's exception.
3. **Ownership.** Route every exit of a block through its deferrals in the
   control-flow graph the ownership post-pass walks, including the raise edge
   from each known raise site. Apply C5, C7 and C8 there, and emit the
   cancellation flags codegen needs.
4. **Resource types.** Mark a class a resource type by C4, extend that to object,
   tuple and array types that own one, and report a class that owns a resource
   but does not consume itself on disposal. Add the C6 check on every exit edge,
   raise edges included. Reject a module-level binding of a resource type, as
   section 4.2 describes, and a raised value that holds a resource, by C10. Reject a closure that moves a resource capture until `move fn`
   ([#2585](https://github.com/escalier-lang/escalier/issues/2585)) lands, then
   type it as a resource. Add `ResourceGenerator` and `ResourceAsyncGenerator`
   to the prelude, and check each generator body against its return type by
   C12.
5. **Collections.** Retype `DisposableStack` and `AsyncDisposableStack` in the
   interop overlay, as section 4.8 describes. Goal 2 for collections of resources
   depends on [#2788](https://github.com/escalier-lang/escalier/issues/2788), [#2789](https://github.com/escalier-lang/escalier/issues/2789),
   [#2790](https://github.com/escalier-lang/escalier/issues/2790) and [#2792](https://github.com/escalier-lang/escalier/issues/2792). Section
   4.3's `cache.delete(&file)` depends on [#2791](https://github.com/escalier-lang/escalier/issues/2791).
6. **`finally`.** Report `finally` with the diagnostic of section 4.9, then remove it
   from the parser a release later. Update
   [06 Error Handling](../../docs/06_error_handling.md) for `defer`, and the
   "Affine, not linear" section of [09 Ownership](../../docs/09_ownership.md)
   for resource types.

Steps 1 and 2 give a working `defer` with no ownership rules beyond today's,
which already rejects a use after `dispose(x)`. Steps 3 and 4 add the rules that
need the control-flow graph, and can land separately.
