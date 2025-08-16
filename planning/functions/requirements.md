# Functions

NOTE: All code examples in this .md files are written using Escalier syntax,
except where explicitly noted.

Basic function declarations and expression have the following syntax:

```ts
fn add(a: number, b: number) {
    return a + b
}

val sub = fn(a: number, b: number) {
    return a - b
}
```

Functions can have return types but aren't required since the return type can
be inferred from the function's body.  The return type uses the following syntax:

```ts
fn add(a: number, b: number) -> number {
    return a + b
}

val sub = fn(a: number, b: number) -> number {
    return a - b
}
```

If the return type inferred from the function's body isn't a subtype of the
specified return type, a type error will be reported.

Functions can throw exceptions.  What exceptions a function can throw can be
specified with the `throws <type>` clause after the return type.  If function's 
return type doesn't include a `throws` clause, it will be inferred.  If you want
to specify that a function doesn't throw, you can do so with `throws never`.

```ts
// inferred as `fn(a: number, b: number) -> number throws never`
fn add(a: number, b: number) -> number {
    return a + b
}

// indicate that the function shouldn't throw in the signature
fn mul(a: number, b: number) -> number throws never {
    return a * b
}

// indicate that the function does throw in the signature
fn div(a: number, b: number) -> number throws DivByZeroError {
    val result = if b == 0 {
        throw new DivByZeroError()
    } else {
        a / b
    }
    return result
}
```

See the section on "Exception Handler" for details on how to catch exceptions.

The `declare` keyword can be used to declare functions without a function body.
This assumes that the function exists in the current execution context.  It is
used for interop with JavaScript and TypeScript.  The return type must be specified
when using `declare`.  If no `throws` clause is specifed, we assume the function
doesn't throw exceptions.

```ts
// inferred as `fn(a: number, b: number) -> number throws never`
declare fn add(a: number, b: number) -> number
```

Parameters do not require type annotations if their types can be infer from
surrounding types, e.g.

```ts
val add: fn(a: number, b: number) -> number = fn(a, b) {
    // The types of `a` and `b` are inferred from the type annotation on the
    // `add` variable declaration.
    return a + b
}

declare fn addListener(listener: fn(event: Event) -> undefined)

addListener(fn(event) {
    // `event` is inferred as `Event` based on the type of `addListener`'s
    // `listener` parameter.
})
```

## Optional Parameters

Functions can have optional parameters.

```ts
fn parseInt(input: string, radix?: number) -> number {
    // `radix` has type `number | undefined`
}

parseInt("123") // parse the number with the default radix
parseInt("123", 8) // parse an octal number
```

Optional parameters can also be specfied by providing a default value.  In this
case, if a call doesn't specify the optional param, it will be given the default
value.

```ts
// inferred as `fn(input: string, radix?: number) -> number
fn parseInt(input: string, radix: number = 10) -> number {
    // `radix` has type `number`
}
```

If the default value is a more complex expression like an object literal, it is
re-evaluated each time the function is called.  This avoids shared state between
function calls.

```ts
val cacheValue = fn(
    key: string,
    value: number,
    store: mut Record<string, number> = {},
) -> Record<string, number> => {
   store[key] = value
   return store
}

cacheValue("x", 5) // {x: 5}
cacheValue("y", 10) // {y: 10}

val store: mut Record<string, string> = {}
cacheValue("x", 5, store) // {x: 5}
cacheValue("y", 10, store) // {x: 5, y: 10}
```

Functions can have multiple optional params.  Required params cannot appear
after optional params.

## Closures

Functions can capture variables defined in outer scopes and continue to use them
even after the variable goes out of scope.

```ts
fn outer() {
  var count = 0 // `count` is in the outer function's scope

  fn inner() {
    count = count + 1
    console.log(count)
  }

  return inner
}

const counter = outer() // outer() returns the inner() function

counter() // prints `1`
counter() // prints `2`
counter() // prints `3`
```

## Rest

Functions can contain a rest parameter.  This is how Escalier supports var args.

```ts
fn sum(init: number, ...rest: Array<number>) -> number {
    return rest.reduce(fn(accum, value) {
        return accum + value
    }, init)
}

sum(5)         // `rest` will be `[]` inside the function
sum(5, 10, 15) // `rest` will be `[10, 15]` inside the function
```

## Spread

A similar syntax can be used to pass multiple values from an array or tuple as
arguments to a function.

```ts
declare fn foo(a: number, b: string) -> true

val mixedTuple = [5, "hello"]
foo(...mixedTuple)

declare fn sum(init: number, ...rest: Array<number>) -> number

val numTuple = [5, 10]
sum(...numType)  // `init` will be `5` and `rest` will be `[10]`

declare val numArray: Array<number>
sum(0, ...numArray) // `init` will be `0` and `rest` will be `Array<number>`

sum(...numArray) // ERROR - won't work because `numArray` could be empty
```

## Generics

Escalier uses the following syntax for generic functions:

```ts
fn identity<T>(x: T) -> T {
    return x
}

val fst = fn<A, B>(a: A, b: B) -> A {
    return a
}

fst(true, "hello") // inferred as `true`
```

Generics can be constrained:

```ts
// Can only be passed numbers
val fstNum = fn<A : number, B : number>(a: A, b: B) -> A {
    return a
}

fstNum(5, 10) // inferred as `5`
```

Generics can also have defaults:

```ts
fn sndWithDefault<A = string, B = string>(a: A, b: B) -> B {
    return b
}

// If the type params aren't specified, `sndWithDefault` must be passed strings
sndWithDefault("foo", "bar")

// But we can still pass type parameters with we want to override the defaults
sndWithDefault<number, boolean>(5, true)
```

## Exception Handling

Functions can catch exceptions using `try`-`catch`.  `try`-`catch` is an
expression.  The `catch` allows you to pattern match against errors that are
thrown within the `try` clause.

```ts
declare fn parseJSON(input: string) -> unknown throws SyntaxError | TypeError

// `fn(input: string) -> unknown | "malformed JSON" | "not a string" throws never
fn foo(input: string) {
    val result = try {
        parseJSON(input)
    } catch {
        SyntaxError => "malformed JSON"
        TypeError => "not a string"
    }
}

// `fn(input: string) -> unknown | "malformed JSON" throws TypeError
fn bar(input: string) {
    val result = try {
        parseJSON(input)
    } catch {
        SyntaxError => "malformed JSON"
    }
}
```

Escalier will automatically add code to rethrow any errors that aren't caught
by the cases in the `catch` clause.  It isn't possible to know which exceptions
a function may throw so we need to ensure we don't accidental swallow exceptions
that aren't caught by a `catch` clause.

## Async/Await

The `async` keyword is used to indicate that a function runs asynchronously.
Such functions must always return `Promise<T, E>` where `T` is the type of the
value being returned and `E` is the type of errors that may be thrown. 

```ts
declare async fn fetch(url: string) -> Promise<Response, NetworkError | TypeError>
```

The `await` keyword can be used inside the body of an `async` function to suspend
execution until the promise has resolved or rejected.  If the promise is rejected
it is treated as if the `await` expression threw the error.

```ts
// inferred as `fn() -> Promise<string, never>`
// `never` indicates that all exceptions that we know about were caught
async fn foo() -> Promise<string> {
    val result = try {
        val res = await fetch("https://www.foo.com")
        await res.text()
    } catch {
        NetworkError => "network error"
        TypeError => "invalid URL"
        SyntaxError => "invalid JSON"
    }

    return result
}
```

## Overloading

Overloaded functions are defined by declaring multiple functions with the same
name.  This is only supported in modules.

```ts
fn dup(value: number) {
    return 2 * value
}
fn dup(value: string) {
    return value ++ value
}
```

The inferred type for `dup` is inferred as:

```ts
(fn(value: number) -> number) & (fn(value: string) -> string)
```

**NOTE:** The `&` syntax is for intersection types.

The compiler will codegen a single function that combines both functions into a
single function that looks like this:

**NOTE:** The following example is written in JavaScript.
```js
function dup(value) {
    if (typeof value === "number") {
        return 2 * value;
    } else if (typeof value === "string") {
        return value + value;
    } else {
        throw new TypeError("Parameter should be either a number or string")
    }
}
```

## Misc Notes

- Function declarations aren't hoisted
- There is no arrow function equivalent
- There are no special considerations for recursive functions
- Partial application and currying aren't supported.  If you want a curried
  function you'll need to defined as one
