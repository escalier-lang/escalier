# 06 Error Handling

A function's exceptional exits are part of its type. Omitting the `throws` clause
declares that the function raises nothing.

```esc
fn f() { throw "boom" }
// ERROR: cannot constrain "boom" <: never
```

Writing `throws never` is not how a non-throwing function is spelled. Omitting
the clause is. See [Functions](02_functions.md) for `throws _`, declared clauses,
and how a callee's throws reaches its caller.

Any value may be thrown, not only an `Error` subclass, since that is what
JavaScript permits.

This is planned to change. Throwing will be restricted to `Error` and its
subclasses so that every exception carries a stack trace. A thrown string or
object literal has none, which is what makes a failure in production hard to
trace back to where it came from.

## `try` / `catch` / `finally`

Catch arms are patterns, the same ones `match` uses.

```esc
try { riskyOperation() } catch { error => console.log(error) }

try { operation() } catch {
    NetworkError{msg} => `Network: ${msg}`,
    TimeoutError => "Timeout",
    _ => "Unknown error",
}

try { getValue() } catch {
    error if error.code == 404 => "Not found",
    error => error.message,
}
```

A `try` block collects what it raises into a sink of its own, so an exception the
arms cover never reaches the enclosing function's clause. `try` is an expression,
so it produces a value, and a `finally` block may follow the arms.

A `try` with no catch arms is rejected, whether or not its block can raise:

```esc
fn f() { try { 42 } }
// ERROR: a `try` with no catch arms catches nothing; drop the `try` and keep
// its block, or add at least one catch arm
```

### The caught value is `unknown`

Whatever the block's signatures named, the runtime can throw anything, so the
caught binding has type `unknown`. An arm narrows it by naming a type or a shape:

```esc
fn f() { try { throw "boom" } catch { e => { return e } } }
// inferred: fn () -> unknown

fn g() {
    try { a() } catch {
        FooError{msg} => { return msg },   // msg: string — narrowed to FooError
        e => { return 0 },
    }
}
// inferred: fn () -> string | 0
```

When throwing is restricted to `Error` and its subclasses, the caught binding
becomes `Error` instead, and an arm narrows from there. Calls into third-party
npm packages keep the weaker guarantee, since nothing constrains what a
JavaScript library throws, so which of the two applies will be a setting rather
than a fixed rule.

### Uncovered exceptions are rethrown

Without a catch-all, the arms leave part of the block's known throws unhandled.
Those are rethrown rather than reported as a non-exhaustive `match`, and they
reach the enclosing function's clause.

```esc
fn f(c: boolean) throws _ {
    try {
        if c { throw "boom" } else { throw 5 }
    } catch {
        5 => 0
    }
}
// inferred: fn (c: boolean) -> undefined throws "boom"
```

Covering every known member removes the obligation entirely, so a caller needs no
clause of its own:

```esc
fn f() { try { throw "boom" } catch { "boom" => 0 } }
// inferred: fn () -> undefined
```

A guarded arm can always fail its guard, so it covers nothing and its member is
still rethrown.

Only the **known** throws are weighed. A value outside them can still be thrown
and rethrown at runtime, but no signature named its type, and recording it would
widen every clause to `unknown`. This is the deliberate limit of the system:
checked exceptions are not a proof that nothing else can happen. They let you
handle, exhaustively, what a function is known to raise.

### What escapes is a set difference

The type that escapes a `try` is `caught & ~handled`, where `handled` is what the
arms catch. Taking a difference rather than matching member names is what lets an
arm subtract more than the one type it spells: an arm naming a base class catches
every value of a subclass, so the subclass member is subtracted too.

```esc
class AppError { code: number, constructor(mut self) { self.code = 0 } }
class ParseError extends AppError { constructor(mut self) { super() } }
```

An arm naming `AppError` also handles a raised `ParseError`. See
[Types](00_types.md) for negation.

## Async errors

An `async fn` cannot raise. What its body throws is absorbed into the rejection
slot of the promise it returns, so its type is always `Promise<T, E>` and never
carries a `throws` clause.

```esc
class FetchError { url: string, constructor(mut self, url: string) { self.url = url } }

async fn fetchJSON(url: string) -> Promise<unknown, FetchError | SyntaxError> {
    val res = await fetch(url)
    if !res.ok {
        throw FetchError(url)
    }
    return res.json()
}
```

Awaiting a promise whose `E` is not `never` contributes `E` to the awaiting
function's own rejection or `throws` clause, so the obligation propagates the way
a synchronous one does. Handle it with the same `try`/`catch`:

```esc
async fn main() {
    try {
        val json = await fetchJSON("https://example.com/data")
        use(json)
    } catch {
        SyntaxError{message} => report(message),
        FetchError{url} => report(url),
    }
}
```

Since both known members are covered, `main`'s promise has no rejection type.

## `Result<T, E>` (post-MVP)

`try`/`catch` is heavy for a value-level failure. A `Result<T, E>` enum offers an
alternative, together with a `?` operator that desugars to an early return:

```esc
val value = fallible()?
// desugars to
val value = match fallible() {
    Result.Ok(v) => v,
    Result.Err(_) as e => return e,
}
```

This is not implemented.
