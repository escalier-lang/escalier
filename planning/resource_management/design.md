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
The rules this document adds are numbered C1 to C10.

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

Goal 2 is a soundness property and holds on every accepted program. Goal 3 is
checked where the checker can see the resource's type, and section 9 lists where
it cannot.

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

A resource that must outlive the loop goes into a `DisposableStack`, as section
4.8 describes.

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
`async gen fn`, by C10. Swift and Zig impose the same restriction. JavaScript lets
a `finally` block `return`, which discards the exception in flight, and that is
the behaviour C3 rules out.

`defer` is rejected at module top level. A module's top-level block exits when
the module finishes evaluating, and code that runs then would surprise readers
who expect module-level resources to live as long as the program does. A
resource that lives for the whole program is held in a module-level binding and
never disposed, and C6 does not apply to module-level bindings.

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
that weaker sense, and C6 does not apply to it.

A structural type can also be a resource. An object type, tuple type or array
type that owns a value of a resource type is a resource type, through any
number of levels of ownership:

- `{file: File, name: string}`, `[File, File]` and `Array<File>` are resource
  types.
- `{file: &File}`, `fn () -> File` and `Promise<File>` are not. A borrow does
  not own its referent, and functions and promises are value types.

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

### 4.6 Async cleanup and generators

**C10. A deferral may `await` inside an `async fn` or `async gen fn`.** The
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
loop does on `break`. A generator that is never finished and never has
`return()` called on it never runs them. JavaScript's `finally` has the same
limit.

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

### 4.8 `DisposableStack` for a dynamic number of resources

A deferral is attached to a block, so resources that must live until a later
point, and whose number is decided at run time, are collected in a
`DisposableStack`. The interop overlay retypes its members so that the stack
owns what it holds:

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

`try … finally` is removed once `defer` lands. A `do` block with a `defer`
expresses the same thing with the cleanup at the top:

```esc
try { work() } catch { e => recover(e) } finally { cleanup() }

do {
    defer cleanup()
    try { work() } catch { e => recover(e) }
}
```

The parser keeps recognizing `finally` for one release and reports it with a
fix-it that performs this rewrite. A `finally` block that raises or contains a
`return` has no direct translation by C3 and C9, and the fix-it is not offered
for it.

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
program holds would be noise.

## 6. Open questions

- **Generic code.** A type parameter is affine, so a generic body can drop a
  value of its type. `fn ignore<T>(x: T) {}` accepts a `File` and drops it, and
  C6 does not see it. Rust accepts the same hole, since `mem::forget` is safe.
  The leak is not unsound for goal 2, since nothing can use the dropped file.
  Closing the hole needs a bound saying a type parameter may be instantiated
  with a resource, with C6 applied to the body under that bound. The proposal is
  to accept the hole in the first version.
- **Closures that take a resource.** A returned closure moves the owned values it
  captures, but functions are value types and can be copied. A copied closure
  that disposes its capture would dispose it twice. Until `move fn`
  ([#2585](https://github.com/escalier-lang/escalier/issues/2585)) gives such a
  closure a type that cannot be copied, a closure that moves a resource capture
  is rejected.
- **Promises of resources.** `Promise<File>` is a value type, so a promise
  dropped without being awaited leaks the file it settles to. C6 starts to apply
  when the promise is awaited and the result bound. Section 8.4 of the ownership
  extensions already warns about some dropped promises, and that warning could
  be extended to a promise whose fulfilment type is a resource.
- **Sugar for acquire-then-defer.** If `val x = acquire()` followed by
  `defer dispose(x)` proves common enough to want one line, a form such as
  `defer val x = acquire()` could register `dispose(x)` and bind `x`. This waits
  on usage.
- **`errdefer`**, as section 5.3 describes.

## 7. Implementation plan

1. **Syntax.** Add `DeferStmt` to the AST, visitor, parser and printer. Report
   `defer` at module top level, and `return`, `yield`, and a `break` or
   `continue` that leaves the deferral, by C3.
2. **Checking and codegen of deferred code.** Check deferred code against a
   `never` throws sink, by C9, and accept `await` in it only inside an async
   body, by C10. Add `dispose` and `disposeAsync` to the prelude. Lower a block
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
   raise edges included.
5. **`DisposableStack`.** Retype `DisposableStack` and `AsyncDisposableStack` in
   the interop overlay, as section 4.8 describes.
6. **`finally`.** Report `finally` with the fix-it of section 4.9, then remove it
   from the parser a release later. Update
   [06 Error Handling](../../docs/06_error_handling.md) for `defer`, and the
   "Affine, not linear" section of [09 Ownership](../../docs/09_ownership.md)
   for resource types.

Steps 1 and 2 give a working `defer` with no ownership rules beyond today's,
which already rejects a use after `dispose(x)`. Steps 3 and 4 add the rules that
need the control-flow graph, and can land separately.
