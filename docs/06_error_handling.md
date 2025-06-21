# 04 Error Handling

## `try`-`catch`-`finally`

If an exception is thrown by a function call inside a `try` block program execution
will jump to the associated `catch` block.  The "caught" exception will not be
included in the inferred `throws` clause of the function.

```ts
class FooError extends Error {
    constructor() {
        super("foo")
    }
}

// inferred as `fn() -> undefined throws FooError | ...`
fn foo() {
    throw FooError()
}

fn bar() {
    try {
        foo()
    } catch (e) {
        FooError => console.log(`caught FooError`)
        _ => console.log("some other Error occurred")
    }
}
```

The error `e` that's caught in the example of above will have type `FooError | ...`.
Caught errors will always be an inexact/open union.  This means that even if we
handle `FooError` there could be another error.  In the example above we have a
catch-all at the bottom which handles this.  If there is no catch-all, the 
compiler will automatically re-throw the unhandled error.

In `async`-`await` functions, the type of exceptions that can be throw 

```ts
class FetchError extends Error {
    constructor(url: string) {
        super(`failed to fetch ${url}`)
    }
}

// inferred as `fn(url: string) -> Promise<unknown, Error | SyntaxError | ...>
async fn fetchJSON(url: string) {
    val res = await fetch(url)
    if !res.ok {
        throw new FetchError(url)
    }
    return res.json() // can throw `SyntaxError`
}

// inferred as `fn(url: string) -> Promise<undefined>
fn foo() {
    try {
        val json = fetchJSON("https://foo.com/bar")
        // do stuff with `json`
        return
    } catch (e) {
        SyntaxError{message} => ...
        FetchError{message} => ...
        // Since we don't have a catchall, the compiler automatically rethrows
        // the error if none of the patterns match.
    }
}
```

Just because a function signature doesn't include a `throws` clause, doesn't
mean that it can't throw.  Literally any function has the potential to throw.  The
purpose of checked exceptions is not provide a fool-proof system for handling all
exceptions.  Rather, the goal is to provide a system that allows you to safely
handle exceptions that we know for sure can be thrown by a function.

The return type of `foo()` in the example above is `Promise<undefined>`.  It
doesn't specify an error type in the promise because we've handled all known
errors.  It still could throw an error, but so could any function.

## `Result<T, E>` (post-MVP)

`try`-`catch` blocks can be a bit heavy handed.  `Result<T, E>` provide an 
alternative way of handling errors.

We can use a wrapper function and a decorator to convert functions that throw
to functions that return a `Result<T, E>` value.

In order to make working with `Result<T, E>` easier, we can add a `?` operator
to the language which will behave similarly to Rust's `?` operator.  This can
be implemented, by desugaring `value?` to the following:

```
match value {
    Ok(value) => value,
    Err(_) as e => return e,
}
```

```

val a: number = value
if val a: number = value { ... }

val {a} = value
if val {a is number} = value { ... }

val {a: b} = value
if val {a: b is number} = value { ... }

val p: Point = value
if val p: Point = value { ... }
```
