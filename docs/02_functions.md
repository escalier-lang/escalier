# 02 Functions

## Type Annotations

```ts
// Function params must have type annotations, but the return type annotation
// is optional since it can always be inferred.
fn add(a: number, b: number) {
    return a + b
}

fn sub(a: number, b: number) -> number {
    return a - b
}

fn sqrt(x: number) -> number throws RangeError {
    if x < 0 {
        throw RangeError("Can't take root of negative numbers")
    }
    return Math.sqrt(x)
}

// When passing a function expression as a callback, we can infer the param
// types from the higher order function's callback param type.  Param types
// can also be ommited when passing a function expression as a prop.  If you
// assign the function to a variable function, the param types must be provided.
const strings = ["1", "2", "3"]
const numbers = strings.map(fn (elem, index) { return parseInt(elem) })
```

The reason we don't allow function param types to be inferred from the usage
of the params within the function is that it's difficult to infer object types
from property access.

## Destructuring Params

TODO

## Function Overloading

TODO

## Async/Await

```
// Inferred as `fn(url: string) -> Promise<string>`
async fn fetchJSON(url: string) {
    val res = await fetch(url)
    return res.json()
}

// Inferred as `fn(url: string) -> Promise<unknown, Error>`
async fn fetchJSON(url: string) {
    val res = await fetch(url)
    if !res.ok {
        throw new Error(`couldn't fetch ${url}`)
    }
    return res.json()
}
```

`Promise<T, E = never>` differs from the `Promise` type provided by TypeScript.
It has a second, optional type param that can be used to describe which errors
an async function can throw.

If an `await` expression is awaiting `Promise<T, E>` where `E` is not `never`,
we will need to set the `ThrowsType` on this expression node.  This is used
when determining what the caller throws.

## Generators (post-MVP)

```
// Inferred as `fn(start: number, stop: number) -> Generator<number, "done", unknown>`
gen fn range(start: number, stop: number) {
    for i = start; i < stop; i++ {
        yield i
    }
    return "done"
}

for i in range(0, 10) {
    console.log(`i = ${i}`)
}

// Inferred as `fn(url: string, intervalMs: number) -> AsyncGenerator<unknown, "done", unknown>
async gen fn pollData(url: string, intervalMs: number) {
    while (true) {
        const response = await fetch(url);
        yield await response.json();
        await new Promise(resolve => setTimeout(resolve, intervalMs));
    }
    return "done"
}
```
