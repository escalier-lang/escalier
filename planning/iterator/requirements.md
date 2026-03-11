# Iterator Protocol Support

This document describes the requirements for supporting the JavaScript iterator protocol in Escalier, enabling iteration over collections using `for...in` loops and spread syntax.

## 1. Overview

Escalier should support the standard JavaScript iterator protocol as defined in ES2015+. This includes:
- The `Iterable<T>` interface for objects that can be iterated
- The `Iterator<T>` interface for stateful iteration
- The `for...in` loop syntax
- Spread syntax in arrays and function arguments
- Generator functions

## 2. Type Definitions

### 2.1. Iterator Types

Escalier should recognize and use the TypeScript standard library type definitions from `lib.es2015.iterable.d.ts`:

```typescript
interface IteratorYieldResult<TYield> {
    done?: false;
    value: TYield;
}

interface IteratorReturnResult<TReturn> {
    done: true;
    value: TReturn;
}

type IteratorResult<T, TReturn = any> =
    IteratorYieldResult<T> | IteratorReturnResult<TReturn>;

interface Iterator<T, TReturn = any, TNext = any> {
    next(...[value]: [] | [TNext]): IteratorResult<T, TReturn>;
    return?(value?: TReturn): IteratorResult<T, TReturn>;
    throw?(e?: any): IteratorResult<T, TReturn>;
}

interface Iterable<T, TReturn = any, TNext = any> {
    [Symbol.iterator](): Iterator<T, TReturn, TNext>;
}

interface IterableIterator<T, TReturn = any, TNext = any>
    extends Iterator<T, TReturn, TNext> {
    [Symbol.iterator](): IterableIterator<T, TReturn, TNext>;
}
```

### 2.2. Generator Types

From `lib.es2015.generator.d.ts`:

```typescript
interface Generator<T = unknown, TReturn = any, TNext = any>
    extends IteratorObject<T, TReturn, TNext> {
    next(...[value]: [] | [TNext]): IteratorResult<T, TReturn>;
    return(value: TReturn): IteratorResult<T, TReturn>;
    throw(e: any): IteratorResult<T, TReturn>;
    [Symbol.iterator](): Generator<T, TReturn, TNext>;
}
```

### 2.3. Async Iterator Types

From `lib.es2018.asynciterable.d.ts` and `lib.es2018.asyncgenerator.d.ts`:

```typescript
interface AsyncIterator<T, TReturn = any, TNext = any> {
    next(...[value]: [] | [TNext]): Promise<IteratorResult<T, TReturn>>;
    return?(value?: TReturn | PromiseLike<TReturn>): Promise<IteratorResult<T, TReturn>>;
    throw?(e?: any): Promise<IteratorResult<T, TReturn>>;
}

interface AsyncIterable<T, TReturn = any, TNext = any> {
    [Symbol.asyncIterator](): AsyncIterator<T, TReturn, TNext>;
}

interface AsyncIterableIterator<T, TReturn = any, TNext = any>
    extends AsyncIterator<T, TReturn, TNext> {
    [Symbol.asyncIterator](): AsyncIterableIterator<T, TReturn, TNext>;
}

interface AsyncGenerator<T = unknown, TReturn = any, TNext = any>
    extends AsyncIteratorObject<T, TReturn, TNext> {
    next(...[value]: [] | [TNext]): Promise<IteratorResult<T, TReturn>>;
    return(value: TReturn | PromiseLike<TReturn>): Promise<IteratorResult<T, TReturn>>;
    throw(e: any): Promise<IteratorResult<T, TReturn>>;
    [Symbol.asyncIterator](): AsyncGenerator<T, TReturn, TNext>;
}
```

## 3. Syntax

### 3.1. For-In Loops

Iterate over any `Iterable<T>`:

```escalier
val items = [1, 2, 3]

for item in items {
    console.log(item)
}
```

#### 3.1.1. Destructuring in For-In

Support destructuring patterns in the loop variable:

```escalier
val entries = [["a", 1], ["b", 2]]

for [key, value] in entries {
    console.log(key, value)
}

val map = new Map([["x", 10], ["y", 20]])

for [key, value] in map {
    console.log(key, value)
}
```

#### 3.1.2. Mutating Items During Iteration

The loop variable binding is immutable (cannot be reassigned), but if the iterated items are mutable objects, their properties can be mutated:

```escalier
val points: Array<mut {x: number, y: number}> = [
    {x: 1, y: 2},
    {x: 3, y: 4}
]

for point in points {
    // point cannot be reassigned, but its properties can be mutated
    point.x = point.x * 2
    point.y = point.y * 2
}
// points is now [{x: 2, y: 4}, {x: 6, y: 8}]
```

This follows Escalier's mutability model: the binding `point` is immutable (like `val`), but the object it references may be mutable if typed as `mut`.

#### 3.1.3. Loop Control

The `break` and `continue` statements work within for-in loops:

```escalier
for item in items {
    if item < 0 {
        continue  // Skip negative items
    }
    if item > 100 {
        break  // Stop iteration entirely
    }
    console.log(item)
}
```

### 3.2. Spread Syntax

#### 3.2.1. Array Spread (Iterable)

Spread an iterable into an array literal:

```escalier
val set = new Set([1, 2, 3])
val array = [...set]  // [1, 2, 3]

val combined = [...array1, ...array2]
```

This requires the spread operand to implement `Iterable<T>`.

#### 3.2.2. Function Argument Spread

Spread an iterable as function arguments:

```escalier
val args = [1, 2, 3]
val sum = add(...args)
```

#### 3.2.3. Object Spread (Non-Iterable)

Object spread is distinct from iterable spread. It copies enumerable own properties and does not require `Iterable`:

```escalier
val point = {x: 1, y: 2}
val point3d = {...point, z: 3}  // {x: 1, y: 2, z: 3}

val merged = {...defaults, ...overrides}
```

Object spread compiles directly to JavaScript object spread:

```javascript
const point3d = {...point, z: 3};
```

### 3.3. Generator Functions

Any function that uses the `yield` keyword is automatically a generator function:

```escalier
fn range(start: number, end: number) {
    var i = start
    while i < end {
        yield i
        i = i + 1
    }
}

for n in range(0, 5) {
    console.log(n)  // 0, 1, 2, 3, 4
}
```

The return type of a generator function is `Generator<T, TReturn, TNext>`.

#### 3.3.1. Yield Expressions

The `yield` keyword produces values:

```escalier
fn fibonacci() {
    var [a, b] = [0, 1]
    while true {
        yield a
        [a, b] = [b, a + b]
    }
}
```

#### 3.3.2. Yield From (Delegation)

Delegate to another iterable using `yield from`:

```escalier
fn concat<T>(a: Iterable<T>, b: Iterable<T>) {
    yield from a
    yield from b
}
```

This compiles to JavaScript's `yield*`:

```javascript
function* concat(a, b) {
    yield* a;
    yield* b;
}
```

### 3.4. Async Generators

Async generator functions are async functions that use `yield`. They produce async iterables:

```escalier
async fn fetchPages(urls: Array<string>) {
    for url in urls {
        val response = await fetch(url)
        yield await response.text()
    }
}

// Consumed with for-await-in
async fn processPages() {
    for await page in fetchPages(urls) {
        console.log(page)
    }
}
```

The return type of an async generator function is `AsyncGenerator<T, TReturn, TNext>`.

## 4. Built-in Iterables

The following types should implement `Iterable`:

| Type | Yields | Notes |
|------|--------|-------|
| `Array<T>` | `T` | Array elements |
| `Set<T>` | `T` | Set values |
| `Map<K, V>` | `[K, V]` | Key-value tuples |
| `String` | `string` | Unicode code points |
| `TypedArray` | `number` | Array elements |
| `NodeList` | `Node` | DOM nodes |
| `arguments` | `any` | Function arguments |

### 4.1. Iterator Methods on Collections

Arrays, Maps, and Sets provide iterator-returning methods:

```escalier
val arr = [1, 2, 3]

for [index, value] in arr.entries() {
    console.log(index, value)
}

for key in arr.keys() {
    console.log(key)
}

for value in arr.values() {
    console.log(value)
}
```

## 5. Implementing Iterable Types

### 5.1. Using Symbol.iterator

Classes can implement `Iterable` by defining `[Symbol.iterator]`:

```escalier
class Range(start: number, end: number) {
    start,
    end,

    [Symbol.iterator](self) -> Iterator<number> {
        var current = self.start
        val end = self.end
        return {
            next: fn() {
                if current < end {
                    val value = current
                    current = current + 1
                    return {done: false, value}
                } else {
                    return {done: true, value: undefined}
                }
            }
        }
    }
}

for n in Range(0, 5) {
    console.log(n)
}
```

### 5.2. Using Generator Methods

A simpler approach using methods that contain `yield`:

```escalier
class Range(start: number, end: number) {
    start,
    end,

    [Symbol.iterator](self) {
        var i = self.start
        while i < self.end {
            yield i
            i = i + 1
        }
    }
}
```

## 6. Type Inference

### 6.1. For-In Loop Variable

The type of the loop variable is inferred from the iterable:

```escalier
val numbers: Array<number> = [1, 2, 3]
for n in numbers {
    // n: number
}

val map: Map<string, number> = new Map()
for [k, v] in map {
    // k: string, v: number
}
```

### 6.2. Generator Return Types

Generator function return types are inferred from `yield` expressions:

```escalier
fn example() {
    yield 1
    yield 2
    return "done"
}
// Inferred: Generator<number, string, never>
```

### 6.3. Async Generator Return Types

Async generator function return types are inferred similarly:

```escalier
async fn fetchItems() {
    yield await fetchItem(1)
    yield await fetchItem(2)
    return "complete"
}
// Inferred: AsyncGenerator<Item, string, never>
```

### 6.4. Spread Type Inference

When spreading into an array, the element type is the union of all spread iterables:

```escalier
val nums: Array<number> = [1, 2]
val strs: Array<string> = ["a", "b"]
val mixed = [...nums, ...strs]
// mixed: Array<number | string>
```

## 7. Error Handling

### 7.1. Non-Iterable Error

Attempting to iterate over a non-iterable should produce a type error:

```escalier
val obj = {a: 1, b: 2}
for item in obj {  // Error: Type '{a: number, b: number}' is not iterable
    console.log(item)
}
```

### 7.2. Yield in Nested Functions

The `yield` and `yield from` keywords only apply to the immediately enclosing function.

```escalier
fn outer() {
    yield 1  // OK - makes outer() a generator

    val callback = fn() {
        yield 2  // OK - makes callback a separate generator
    }

    items.forEach(fn(item) {
        yield item  // Error: forEach doesn't expect a generator as a callback
    })
}
```

To yield values from within a loop, use `for...in` instead of `.forEach()`:

```escalier
fn outer() {
    for item in items {
        yield item  // OK - yields from outer()
    }
}
```

### 7.3. Async Context Errors

Using `await` is only allowed in `async fn`:

```escalier
fn notAsync() {
    yield await fetch(url)  // Error: 'await' expression is only allowed in async functions
}
```

Using `for await` is only allowed in async contexts:

```escalier
fn notAsync() {
    for await item in asyncIterable {  // Error: 'for await' is only allowed in async functions
        console.log(item)
    }
}
```

## 8. Code Generation

### 8.1. For-In Loops

Escalier `for...in` compiles to JavaScript `for...of`:

```escalier
for item in items {
    console.log(item)
}
```

Compiles to:

```javascript
for (const item of items) {
    console.log(item);
}
```

### 8.2. For-Await Loops

Escalier `for await` compiles to JavaScript `for await...of`:

```escalier
for await item in asyncItems {
    console.log(item)
}
```

Compiles to:

```javascript
for await (const item of asyncItems) {
    console.log(item);
}
```

### 8.3. Spread Syntax

Spread compiles directly:

```escalier
val arr = [...set]
```

Compiles to:

```javascript
const arr = [...set];
```

### 8.4. Generators

Functions containing `yield` compile to JavaScript generator functions:

```escalier
fn count(n: number) {
    var i = 0
    while i < n {
        yield i
        i = i + 1
    }
}
```

Compiles to:

```javascript
function* count(n) {
    let i = 0;
    while (i < n) {
        yield i;
        i = i + 1;
    }
}
```

### 8.5. Yield From (Delegation)

The `yield from` expression compiles to JavaScript's `yield*`:

```escalier
fn flatten<T>(iterables: Array<Iterable<T>>) {
    for iterable in iterables {
        yield from iterable
    }
}
```

Compiles to:

```javascript
function* flatten(iterables) {
    for (const iterable of iterables) {
        yield* iterable;
    }
}
```

### 8.6. Async Generators

Async functions containing `yield` compile to JavaScript async generator functions:

```escalier
async fn fetchItems(ids: Array<number>) {
    for id in ids {
        yield await fetchItem(id)
    }
}
```

Compiles to:

```javascript
async function* fetchItems(ids) {
    for (const id of ids) {
        yield await fetchItem(id);
    }
}
```

## 9. Edge Cases

### 9.1. Empty Iterables

Empty iterables should work correctly:

```escalier
val empty: Array<number> = []
for item in empty {
    // Never executes
}

val spread = [...empty]  // []
```

### 9.2. Infinite Iterators

Infinite iterators are valid; consumers must `break`:

```escalier
fn naturals() {
    var n = 0
    while true {
        yield n
        n = n + 1
    }
}

for n in naturals() {
    if n >= 10 {
        break
    }
    console.log(n)
}
```

### 9.3. Iterator Protocol Methods

Iterators may optionally implement `return()` and `throw()`:

- `return()` is called when iteration is terminated early (via `break`, `return`, or exception)
- `throw()` is called to inject an error into the iterator

```escalier
fn withCleanup() {
    try {
        yield 1
        yield 2
    } finally {
        console.log("cleanup")
    }
}

for n in withCleanup() {
    if n == 1 {
        break  // Triggers finally block
    }
}
```

## 10. Future Considerations

### 10.1. Iterator Helpers

ES2025 introduces iterator helper methods. These should be supported when targeting ES2025+:

```escalier
val doubled = [1, 2, 3].values().map(fn(x) { x * 2 }).toArray()
```

### 10.2. Object Iteration Syntax

Consider a dedicated syntax for iterating over object entries using `for...of`:

```escalier
val obj = {a: 1, b: 2, c: 3}

for key, value of obj {
    console.log(key, value)
}
```

This would compile to:

```javascript
for (const [key, value] of Object.entries(obj)) {
    console.log(key, value);
}
```

This provides a cleaner syntax than `for [key, value] in Object.entries(obj)` and clearly distinguishes object iteration (`of`) from iterable iteration (`in`).
