# Extended Standard Library Type Support

## Overview

The Escalier compiler currently infers types from TypeScript's `lib.es5.d.ts` and `lib.dom.d.ts`. This document outlines the requirements for extending support to include additional TypeScript standard library files such as `lib.es2015.d.ts`, `lib.es2016.d.ts`, and later ES versions.

## Motivation

TypeScript's standard library is organized into modular files representing different ECMAScript versions and runtime environments. Supporting only ES5 limits the type information available for modern JavaScript features:

- **ES2015 (ES6)**: Promises, Map, Set, Symbol, generators, iterators, Proxy, Reflect
- **ES2016**: `Array.prototype.includes`, exponentiation operator
- **ES2017**: `Object.values/entries`, `String.prototype.padStart/padEnd`, async/await
- **ES2018**: Async iterators, `Promise.finally`, rest/spread properties
- **ES2019**: `Array.prototype.flat/flatMap`, `Object.fromEntries`, optional catch binding
- **ES2020**: `BigInt`, `Promise.allSettled`, `globalThis`, nullish coalescing
- **ES2021**: `String.prototype.replaceAll`, `Promise.any`, `WeakRef`
- **ES2022**: Top-level await, `Array.prototype.at`, `Object.hasOwn`
- **ES2023**: Array `findLast/findLastIndex`, hashbang grammar

## Requirements

### Functional Requirements

#### FR1: Dynamic Lib File Discovery
The compiler must dynamically discover and load ES lib files from the TypeScript installation (`node_modules/typescript/lib/`), rather than hardcoding a list. This ensures:
- Automatic support for new ES versions when TypeScript is updated
- No manual maintenance of lib file lists
- Consistent behavior across different TypeScript versions

**Discovery rules:**
- Load `lib.es5.d.ts` first (contains actual type definitions, not references)
- Parse bundle files (`lib.es2015.d.ts`, `lib.es2020.d.ts`, etc.) for `/// <reference lib="..." />` directives
- Load sub-library files in the order specified by reference directives in each bundle file
- Exclude `lib.esnext.*.d.ts` files (unstable/experimental features)

**Load order determination:**
Bundle files (e.g., `lib.es2015.d.ts`) contain only `/// <reference lib="..." />` directives that specify sub-libraries. We parse these directives to determine:
1. Which sub-library files to load for each ES version
2. The correct order to load them (respecting dependencies between sub-libraries)

#### FR2: Correct Load Order
Lib files must be loaded in correct dependency order. Each ES version's types must be fully loaded before proceeding to the next version, as later versions depend on types defined in earlier versions.

**Load order algorithm:**
1. Load `lib.es5.d.ts` first (contains actual type definitions, not just references)
2. For each subsequent ES version (ES2015, ES2016, ..., ES2023) in order:
   - Parse the bundle file (e.g., `lib.es2015.d.ts`) to extract `/// <reference lib="..." />` directives
   - Load each referenced sub-library in the order they appear (e.g., `lib.es2015.core.d.ts`, `lib.es2015.collection.d.ts`, etc.)
3. Load `lib.dom.d.ts` last (DOM types may reference ES2015+ types like Promise, Symbol)

**Example load sequence:**
```
1. lib.es5.d.ts                    ← Base types (Array, Object, String, etc.)
2. lib.es2015.d.ts references:
   ├── lib.es2015.core.d.ts        ← Array.find, Object.assign, etc.
   ├── lib.es2015.collection.d.ts  ← Map, Set, WeakMap, WeakSet
   ├── lib.es2015.iterable.d.ts    ← Iterable, Iterator
   ├── lib.es2015.generator.d.ts   ← Generator
   ├── lib.es2015.promise.d.ts     ← Promise static methods
   ├── lib.es2015.proxy.d.ts       ← Proxy, Reflect
   ├── lib.es2015.symbol.d.ts      ← Symbol
   └── lib.es2015.symbol.wellknown.d.ts
3. lib.es2016.d.ts references:
   └── lib.es2016.array.include.d.ts ← Array.includes
4. lib.es2017.d.ts references:
   ├── lib.es2017.object.d.ts      ← Object.values, Object.entries
   ├── lib.es2017.string.d.ts      ← padStart, padEnd
   └── ...
5. ... (ES2018 through ES2023)
6. lib.dom.d.ts                    ← DOM types (loaded last)
```

**Why this order matters:**
- `lib.es2015.iterable.d.ts` depends on `Symbol` from `lib.es2015.symbol.d.ts`
- `lib.es2015.promise.d.ts` depends on `PromiseLike` from `lib.es5.d.ts`
- `lib.dom.d.ts` uses `Promise`, `Symbol`, and other ES2015+ types

#### FR3: Declaration Merging
Multiple lib files may extend the same interfaces (e.g., `Array`, `String`, `Object`). The compiler must correctly merge these declarations:

```typescript
// lib.es5.d.ts
interface Array<T> {
    indexOf(searchElement: T): number;
}

// lib.es2015.core.d.ts
interface Array<T> {
    find(predicate: (value: T, index: number, obj: T[]) => unknown): T | undefined;
}

// lib.es2016.array.include.d.ts
interface Array<T> {
    includes(searchElement: T, fromIndex?: number): boolean;
}

// Result: Array<T> has indexOf, find, and includes
```

**Handling conflicting declarations:**

When multiple lib files declare the same method with different signatures, the compiler must merge them as overloads. TypeScript lib files are designed to avoid true conflicts, but the merging behavior should be:

1. **Same method, compatible signatures**: Merge as overloads (all signatures available)
2. **Same property, same type**: Keep single declaration (idempotent)
3. **Same property, different types**: This indicates a bug in lib files or loading order; the compiler should use the later declaration but log a warning

In practice, TypeScript's lib files are carefully designed to avoid conflicts. Later ES versions typically add new methods rather than modifying existing ones.

#### FR4: Global Type Augmentation
Types defined in lib files must be available in the global scope:

```
// Escalier code
val map = new Map<string, number>()  // Map from lib.es2015.collection.d.ts
val promise = Promise.resolve(42)    // Promise from lib.es2015.promise.d.ts
val sym = Symbol("key")              // Symbol from lib.es2015.symbol.d.ts
```

#### FR5: DOM Lib File Load Order
The `lib.dom.d.ts` file must be loaded **after** all ES lib files. DOM types may reference ES2015+ types (e.g., `Promise` in fetch API, `Symbol` in iterables). Loading DOM before ES2015+ would result in unresolved type references.

#### FR6: Preserve Escalier's Two-Parameter Promise Type

Escalier extends TypeScript's `Promise<T>` with a two-parameter variant `Promise<T, E>` where:
- `T` is the resolved value type
- `E` is the error/rejection type (defaults to `never` for non-throwing promises)

This is a core Escalier feature that enables typed error handling in async code:

```
// Promise that always resolves (never throws)
async fn fetchData(url: string) -> Promise<string, never> {
    return "data"
}

// Promise that may reject with a specific error type
async fn fetchWithError(url: string) -> Promise<Response, NetworkError> {
    if !isValidUrl(url) {
        throw NetworkError("Invalid URL")
    }
    return await fetch(url)
}

// Error types are inferred from throw statements and awaited promises
async fn processData(url: string) {
    val data = await fetchWithError(url)  // Propagates NetworkError
    return parse(data)
}
// Inferred: fn (url: string) -> Promise<ParsedData, NetworkError>
```

**Requirements:**

**FR6.1: Augmentation of TypeScript Promise**

When loading `Promise` and `PromiseLike` interfaces from TypeScript lib files, the compiler must add the second type parameter `E` with a default value (e.g., `E = never` or `E = any`). This allows existing `Promise<T>` references to work without modification.

**FR6.2: Consistent augmentation across all lib files**

Promise-related types in ES2015+ lib files must also be augmented:
   - `lib.es2015.promise.d.ts`: `Promise<T>`, `PromiseLike<T>`, `PromiseConstructorLike`
   - `lib.es2018.promise.d.ts`: `Promise.finally()`
   - `lib.es2020.promise.d.ts`: `Promise.allSettled()`
   - `lib.es2021.promise.d.ts`: `Promise.any()`

**FR6.3: Static method return types**

Static methods must properly propagate error types.

   **Simple methods:**
   - `Promise.resolve<T>(value: T): Promise<T, never>` - always succeeds
   - `Promise.reject<E>(reason: E): Promise<never, E>` - captures rejection type as E *(Note: TypeScript uses `any` for the reason parameter; Escalier intentionally captures the actual type for better error tracking)*

   **`Promise.all`, `Promise.race`, `Promise.allSettled`, and `Promise.any` with mapped type signatures:**

   TypeScript's official signature for `Promise.all` uses mapped types to preserve heterogeneous tuple structure:

   ```typescript
   // TypeScript's official signature (lib.es2015.promise.d.ts)
   all<T extends readonly unknown[] | []>(values: T): Promise<{ -readonly [P in keyof T]: Awaited<T[P]>; }>;
   ```

   Escalier must extend this to also extract and union error types. This requires a companion helper type `AwaitedError<T>`:

   ```typescript
   // Escalier helper type for extracting error types from promises
   type AwaitedError<T> =
       T extends Promise<any, infer E> ? E :
       T extends PromiseLike<any, infer E> ? E :
       never;  // Non-promise values contribute no error type

   // Escalier's augmented PromiseSettledResult types (TypeScript's PromiseRejectedResult has untyped `reason: any`)
   interface PromiseRejectedResult<E> {
       status: "rejected";
       reason: E;
   }

   type PromiseSettledResult<T, E> = PromiseFulfilledResult<T> | PromiseRejectedResult<E>;

   // Escalier's augmented AggregateError (TypeScript's AggregateError has untyped `errors: any[]`)
   // Uses a tuple type parameter to preserve the structure of error types from input promises
   interface AggregateError<Errors extends any[]> extends Error {
       errors: Errors;
   }

   // Escalier's augmented Promise.all signature
   all<T extends readonly unknown[] | []>(values: T): Promise<
       { -readonly [P in keyof T]: Awaited<T[P]>; },           // Value: tuple of awaited values
       { [P in keyof T]: AwaitedError<T[P]> }[keyof T]         // Error: union of all error types
   >;

   // Escalier's augmented Promise.race signature
   race<T extends readonly unknown[] | []>(values: T): Promise<
       Awaited<T[number]>,                                      // Value: union of awaited values
       { [P in keyof T]: AwaitedError<T[P]> }[keyof T]         // Error: union of all error types
   >;

   // Escalier's augmented Promise.allSettled signature
   // TypeScript's official signature (lib.es2020.promise.d.ts):
   // allSettled<T extends readonly unknown[] | []>(values: T): Promise<{ -readonly [P in keyof T]: PromiseSettledResult<Awaited<T[P]>>; }>;
   allSettled<T extends readonly unknown[] | []>(values: T): Promise<
       { -readonly [P in keyof T]: PromiseSettledResult<Awaited<T[P]>, AwaitedError<T[P]>>; },  // Value: tuple of settled results with typed errors
       never                                                                                     // Error: always succeeds
   >;

   // Escalier's augmented Promise.any signature
   // TypeScript's official signature (lib.es2021.promise.d.ts):
   // any<T>(values: Iterable<T | PromiseLike<T>>): Promise<Awaited<T>>;
   any<T extends readonly unknown[] | []>(values: T): Promise<
       Awaited<T[number]>,                                         // Value: union of awaited values (first to resolve)
       AggregateError<{ -readonly [P in keyof T]: AwaitedError<T[P]> }>  // Error: AggregateError with tuple of error types
   >;
   ```

   **How the mapped types work:**
   - `{ -readonly [P in keyof T]: Awaited<T[P]>; }` - Maps each tuple element to its awaited value type, preserving tuple structure and removing `readonly`
   - `{ [P in keyof T]: AwaitedError<T[P]> }[keyof T]` - Maps each element to its error type, then indexes with `keyof T` to get the union of all error types
   - `Awaited<T[number]>` - For `Promise.race` and `Promise.any`, gets the union of all awaited value types
   - `{ -readonly [P in keyof T]: PromiseSettledResult<Awaited<T[P]>, AwaitedError<T[P]>>; }` - For `Promise.allSettled`, maps each element to its settled result type (either `PromiseFulfilledResult<T>` or `PromiseRejectedResult<E>`), preserving both value and error types in the tuple structure
   - `AggregateError<{ -readonly [P in keyof T]: AwaitedError<T[P]> }>` - For `Promise.any`, wraps the tuple of error types in `AggregateError<Errors>`, preserving the structure so `errors[0]` has the error type of the first promise, `errors[1]` has the error type of the second, etc.

   **Concrete examples:**

   ```
   // Given these function signatures:
   fn fetchUser(id: number) -> Promise<User, UserFetchError>
   fn fetchPost(id: number) -> Promise<Post, PostFetchError>

   // Promise.all preserves tuple structure for values, unions errors
   val [user, post] = await Promise.all([fetchUser(1), fetchPost(1)])
   // user: User, post: Post (tuple destructuring works!)
   // Error type: UserFetchError | PostFetchError

   // Promise.race unions both values and errors
   val first = await Promise.race([fetchUser(1), fetchPost(1)])
   // first: User | Post
   // Error type: UserFetchError | PostFetchError

   // Works with mixed promise and non-promise values
   val [user, name, count] = await Promise.all([fetchUser(1), "literal", 42])
   // user: User, name: string, count: number
   // Error type: UserFetchError (only the promise contributes an error type)

   // Promise.allSettled preserves tuple structure for settled results with typed errors
   val [userResult, postResult] = await Promise.allSettled([fetchUser(1), fetchPost(1)])
   // userResult: PromiseSettledResult<User, UserFetchError>
   // postResult: PromiseSettledResult<Post, PostFetchError>
   // Error type: never (allSettled always succeeds - rejections become PromiseRejectedResult<E> objects)

   // Promise.any resolves with first fulfilled value, rejects with AggregateError if all reject
   val first = await Promise.any([fetchUser(1), fetchPost(1)])
   // first: User | Post (first promise to fulfill)
   // Error type: AggregateError<[UserFetchError, PostFetchError]>
   // The tuple-typed AggregateError preserves error structure, allowing typed access:
   // catch (e: AggregateError<[UserFetchError, PostFetchError]>) {
   //     e.errors[0]  // UserFetchError
   //     e.errors[1]  // PostFetchError
   // }
   ```

   **Implementation note:** The `AwaitedError<T>` helper must return `never` for non-promise values, so they don't pollute the error type union. Since `never | T = T`, non-promise values are effectively ignored in the error union.

**FR6.4: Backward compatibility**

By using a default type parameter (`E = never`), code using `Promise<T>` (single parameter) automatically works as `Promise<T, never>`. No transformation of existing `Promise<T>` references is needed.

### Non-Functional Requirements

#### NFR1: Performance
- Lib file parsing and type inference should be cached to avoid repeated work
- The existing global scope caching mechanism should be extended for additional libs
- Loading additional libs should not significantly impact compilation time for simple programs

#### NFR2: Maintainability
- The solution should be extensible to support future ES versions
- New lib files should be easy to add without major code changes

#### NFR3: Compatibility
- Type inference should produce results consistent with TypeScript's behavior (except for Escalier-specific extensions like `Promise<T, E>`)
- Generated `.d.ts` files should be valid TypeScript
- When emitting `.d.ts` files, `Promise<T, E>` should be emitted as `Promise<T>` for TypeScript compatibility, with the error type available via JSDoc

#### NFR4: Error Handling
- If a lib file fails to parse, compilation must fail with a clear error message
- Partial parsing (skipping unparseable declarations) is not acceptable as it leads to confusing behavior where some types are mysteriously missing
- Error messages should indicate which lib file failed and ideally what syntax was not supported

## Scope

### In Scope
- Loading and parsing ES2015 through ES2023 lib files
- Declaration merging for extended interfaces
- Global type availability
- Correct dependency ordering

### Out of Scope (Future Work)
- Configuration-based lib selection (similar to TypeScript's `lib` compiler option)
- Custom lib file paths
- Loading lib files from different TypeScript versions
- Web API variants (webworker.d.ts, serviceworker.d.ts)

## Expected Usage

After implementation, the following Escalier code should type-check correctly:

```
// ES2015 features
val map = new Map<string, number>()
map.set("key", 42)
val value = map.get("key")

val set = new Set([1, 2, 3])
val hasTwo = set.has(2)

val promise = Promise.resolve(42)
val result = await promise

val gen = fn*() {
    yield 1
    yield 2
}

// ES2016 features
val arr = [1, 2, 3]
val hasTwo = arr.includes(2)

// ES2017 features
val obj = { a: 1, b: 2 }
val keys = Object.values(obj)
val padded = "hello".padStart(10, " ")

// ES2019 features
val nested = [[1], [2, 3]]
val flat = nested.flat()

// ES2020 features
val big = BigInt(9007199254740991)
val settled = await Promise.allSettled([promise1, promise2])

// ES2021 features
val first = await Promise.any([promise1, promise2, promise3])

// ES2022 features
val lastElement = arr.at(-1)
```

### Promise<T, E> Usage

Escalier's two-parameter Promise should work seamlessly with ES2015+ Promise APIs:

```
// Basic Promise with error type
async fn fetchUser(id: number) -> Promise<User, ApiError> {
    val response = await fetch(`/api/users/${id}`)
    if !response.ok {
        throw ApiError(response.status, response.statusText)
    }
    return response.json() as User
}

// Promise.all preserves error types
async fn fetchUsers(ids: number[]) -> Promise<User[], ApiError> {
    val promises = ids.map(fn(id) { fetchUser(id) })
    return await Promise.all(promises)
}

// Promise.allSettled always succeeds (error type is never)
async fn fetchUsersSettled(ids: number[]) {
    val promises = ids.map(fn(id) { fetchUser(id) })
    val results = await Promise.allSettled(promises)
    // results: PromiseSettledResult<User>[]
    // This await never throws, so function error type is never
}

// Promise.any may throw AggregateError<Errors> containing all error types as a tuple
async fn fetchFirstUser(ids: number[]) {
    val promises = ids.map(fn(id) { fetchUser(id) })
    val first = await Promise.any(promises)
    // If all promises reject, throws AggregateError<FetchError[]>
}
// Inferred: fn (ids: number[]) -> Promise<User, AggregateError<FetchError[]>>

// Chaining with .then() and .catch()
val handled = fetchUser(1)
    .then(fn(user) { user.name })
    .catch(fn(err) { "Unknown" })
// handled: Promise<string, never>

// Promise.finally() (ES2018)
val withCleanup = fetchUser(1)
    .finally(fn() { cleanup() })
// withCleanup: Promise<User, ApiError>
```

## Success Criteria

1. All ES2015-ES2023 lib files parse without errors
2. Declaration merging correctly combines interface definitions
3. Types from all loaded lib files are available in the global scope
4. Existing tests continue to pass (no regressions)
5. Integration tests verify modern JavaScript APIs type-check correctly
6. `Promise<T, E>` augmentation works for all Promise-related types across lib files
7. Promise static methods (`Promise.all`, `Promise.race`, `Promise.allSettled`, `Promise.any`) correctly propagate error types
8. Async/await continues to infer error types from awaited promises
