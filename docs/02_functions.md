# 02 Functions

## Declarations and inference

A function declaration names its parameters and, optionally, its return type.

```esc
fn add(a: number, b: number) {
    return a + b
}

fn sub(a: number, b: number) -> number {
    return a - b
}
```

The return type is always inferable from the body, so annotating it is optional.
Parameter types are inferred too, from the body's usage of each parameter:

```esc
import "std:math"

fn dist(p) {
    return math.sqrt(p.x * p.x + p.y * p.y)
}
// inferred: fn (p: {x: number, y: number}) -> number
```

`p` is never annotated. Reading `p.x` says the argument must have an `x`, and
multiplying it says that field must be a `number`, so the two constraints
together give the parameter its type.

The inferred parameter shape is **exact**, so a caller may not pass an object
with extra fields. Mark the parameter `open` to keep it row-polymorphic:

```esc
fn dist(open p) {
    return math.sqrt(p.x * p.x + p.y * p.y)
}
// inferred: fn (p: {x: number, y: number, ...}) -> number
```

A function expression passed as a callback infers its parameter types from the
higher-order function's signature:

```esc
import "std:array"
import "std:number"

val strings: Array<string> = ["1", "2", "3"]
val numbers = strings.map(fn (elem, index) { return Number.parseInt(elem) })
// elem: string, index: number, numbers: Array<number>
```

`Array<T>.map` declares its callback as `fn (elem: T, index: number) -> U`, so
`elem` and `index` are fixed by the slot the function expression lands in and `U`
comes back from its body.

The annotation on `strings` is what makes it an `Array<string>`. Without one,
`val strings = ["1", "2", "3"]` infers the tuple `["1", "2", "3"]`, since a `val`
binding keeps each element's literal type.

## Generics

Type parameters are written in `<>`, may carry a constraint after `:`, and may
have a default after `=`.

```esc
fn id<T>(x: T) -> T { return x }
fn first<T>(x: T, y: T) -> T { return x }
fn call<F: fn (x: number) -> number>(f: F) -> number { return f(1) }
fn parse<T = string>(x: T) -> T { return x }
```

A default fills the parameter in where the reference names no argument for it,
so `type Box<T = number> = {value: T}` makes a bare `Box` mean `Box<number>`.

Each call instantiates the parameters independently, so two calls to `id` keep
their own argument types:

```esc
val a = id(5)      // a: 5
val b = id("hi")   // b: "hi"
```

A function may also take **lifetime parameters**, written `<'a>` and optionally
bounded with `'a: 'b`. They are inferred when possible and appear in the
signature only when they connect an input to an output. See
[Ownership](09_ownership.md).

```esc
declare fn id<'a>(p: &'a {x: number}) -> &'a {x: number}
```

## Parameters, ownership, and mutability

A parameter's annotation says whether the function borrows its argument or takes
ownership of it.

```esc
fn read(p: &{x: number}) -> number { return p.x }    // borrows; caller keeps p
fn bump(p: &mut {x: number}) { p.x = p.x + 1 }       // mutable borrow
fn store(p: {x: number}) { ... }                     // consumes; caller gives p up
```

The borrow is inserted at the call site, so a caller may write `read(p)`; writing
`read(&p)` explicitly is also accepted. An unannotated parameter is inferred, and
the checker picks a borrow when the body never lets the value escape and
ownership when it does. See [Ownership](09_ownership.md) for what counts as
escape.

## `throws`

A function's exceptional exits are part of its type. **Omitting the `throws`
clause declares that the function raises nothing**, and a body that does raise is
rejected at the site that raises.

```esc
fn f() { throw "boom" }
// ERROR: cannot constrain "boom" <: never
```

Write `throws _` to read the clause off the body:

```esc
fn f() throws _ { throw "boom" }
// inferred: fn () -> never throws "boom"

fn g(c: boolean) throws _ { if c { throw "a" } else { throw 5 } }
// inferred: fn (c: boolean) -> never throws 5 | "a"
```

This is the point of the design. Checked exceptions in other languages make you
enumerate what a function raises, which means tracing every call it makes and
redoing that work whenever a callee changes. `throws _` opts a function into
checked exceptions and leaves the enumeration to the compiler, so callers get the
obligation without the author doing the bookkeeping. It is the same deal the
return type already offers.

Write `throws T` when you want to name the clause yourself. The declared type is
what callers see, and each `throw` in the body is checked against it at its own
site:

```esc
fn f() throws string { throw "boom" }    // the literal widens to string
fn g() throws number { throw "boom" }    // ERROR at the throw
```

Naming it is how you widen past what the body happens to raise today, so adding a
second `throw` later does not change the signature every caller was written
against.

A call raises whatever its callee declares, so the callee's `throws` reaches the
caller's clause the way a `throw` in the caller's own body would. A body with no
exceptional exit renders no clause, whether or not one was written. Nested
functions own their own clause, so an inner function's `throws` does not leak
into the enclosing one.

See [Error Handling](06_error_handling.md) for `try`/`catch` and the rest.

## Async and `await`

An `async fn` returns `Promise<T, E>`, where `E` is the rejection type. Unlike a
sync function, an `async fn` cannot raise: what its body throws is absorbed into
the promise's rejection slot, so it never carries a `throws` clause.

```esc
async fn f() { throw "x" }
// inferred: fn () -> Promise<never, "x">

async fn g() { return 5 }
// inferred: fn () -> Promise<5>
```

The rejection type may be written, in which case the body's throws are checked
against it:

```esc
async fn fetchJSON(url: string) -> Promise<unknown, FetchError> {
    val res = await fetch(url)
    if !res.ok {
        throw FetchError(url)
    }
    return res.json()
}
```

Either slot accepts `_` to infer just that slot:

```esc
async fn f() -> Promise<_, _> { throw "x" }   // fn () -> Promise<never, "x">
```

`Promise<T, E = never>` differs from TypeScript's `Promise<T>` by carrying the
rejection type. Awaiting a `Promise<T, E>` where `E` is not `never` contributes
`E` to the awaiting function's own rejection or `throws` clause.

## Overloading

Declaring the same name more than once at the top level makes it an overload
set. A direct call resolves to the arm whose parameters accept the arguments.

```esc
fn f(x: number) -> number { return x }
fn f(x: string) -> string { return x }

val r = f(5)      // r: number
val s = f("hi")   // s: string
```

Arms are tried most-specific-first when every argument has a known shape, and in
declaration order otherwise. Overload resolution runs as a phase separate from
subtyping, so "callable in several ways" never enters the type lattice. Each arm
needs its own parameter annotations, since the runtime dispatcher generated for
the set is built from them.

## Generators

```esc
gen fn f() { yield 1 }
// inferred: fn () -> Generator<1, undefined, unknown>

gen fn g() { yield 1
             return "done" }
// inferred: fn () -> Generator<1, "done", unknown>
```

The three type arguments are the yield type, the return type, and the type sent
back in through `next`. Multiple `yield`s union their types. `async gen fn`
produces an `AsyncGenerator` with the same shape.

A generator's values are consumed with `for`-`in`:

```esc
for i in range(0, 10) {
    console.log(`i = ${i}`)
}
```

## Function subtyping and exactness

A written function value is **exact**: it accepts exactly the arities its
parameter list declares, and a direct call with extra arguments is rejected.

Exactness governs callback subtyping. A function type accepts a range of argument
counts — `[required, declared]` when exact, `[required, ∞)` when inexact — and
`G <: F` when `G` accepts every count `F`'s holders may call with, with
parameters contravariant and the return covariant.

An exact value therefore has to match the slot's arity. Passing a
one-parameter function where three arguments will be supplied is rejected,
because its range is `[1, 1]` and the slot's holders call with 3:

```esc
declare fn hof(cb: fn (a: number, b: number, c: number) -> number) -> number

val r = hof(fn (a) { return a })
// ERROR: cannot constrain function of arity 1 <: function of arity 3
```

A trailing `...` makes the value inexact, opening its range to `[1, ∞)`, which
does cover 3:

```esc
val r = hof(fn (a, ...) { return a })   // OK
```

This is the one place Escalier asks for something JavaScript does not. In
JavaScript a callback may simply ignore trailing arguments; here that has to be
stated, and `...` is how you state it.

## Destructuring parameters

Parameters accept the same patterns as `val` bindings.

```esc
fn dist({x, y}: {x: number, y: number}) -> number {
    return Math.sqrt(x ** 2 + y ** 2)
}
```

See [Destructuring](01_destructuring.md).
