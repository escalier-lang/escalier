# Extended Standard Library Types - Implementation Plan

## Overview

This plan outlines the implementation steps for extending the Escalier compiler to support TypeScript lib files beyond lib.es5.d.ts and lib.dom.d.ts.

## Current State

### What's Working
- ✅ `lib.es5.d.ts` parsing and type inference
- ✅ `lib.dom.d.ts` parsing and type inference
- ✅ Global scope caching in `Prelude()`
- ✅ dts_parser handles complex TypeScript declaration syntax
- ✅ Declaration merging for interfaces via `MergeInterface()`
- ✅ Package registry for named modules
- ✅ `Promise<T, E>` augmentation for `Promise` and `PromiseLike` interfaces (adds error type parameter)
- ✅ Async/await error type inference from awaited promises

### Key Files
| File | Purpose |
|------|---------|
| `internal/checker/prelude.go` | Entry point for stdlib loading |
| `internal/checker/prelude.go:337-353` | `loadGlobalDefinitions()` - loads lib.es5.d.ts and lib.dom.d.ts |
| `internal/checker/prelude.go:355-429` | `loadGlobalFile()` - loads single lib file |
| `internal/dts_parser/` | Complete .d.ts parsing infrastructure |
| `internal/interop/module.go` | AST conversion from dts_parser to ast |
| `internal/interop/decl.go:263-304` | `Promise<T>` → `Promise<T, E>` augmentation via `PromiseVisitor` |
| `internal/checker/package_registry.go` | Registry for named modules |

### Existing Promise<T, E> Augmentation

Escalier already augments TypeScript's `Promise<T>` to `Promise<T, E>` in the interop layer:

```go
// internal/interop/decl.go:263-304
// When converting Promise/PromiseLike interfaces from .d.ts files
if di.Name.Name == "PromiseLike" || di.Name.Name == "Promise" {
    // Add error type parameter "E" with default type
    // Using a default allows Promise<T> references to work without transformation
    errorTypeParam := ast.NewTypeParam("E", nil, ast.NewNeverTypeAnn(...)) // default = never
    typeParams = append(typeParams, &errorTypeParam)

    // PromiseVisitor handles special cases where error types differ from the default
    visitor := &PromiseVisitor{...}
    objType.Accept(visitor)
}
```

This augmentation happens during AST conversion, before type inference. By using a default type for `E`, existing `Promise<T>` references in TypeScript lib files automatically become `Promise<T, never>` without requiring explicit transformation.

### Current Loading Code

```go
// internal/checker/prelude.go:337-353
func (c *Checker) loadGlobalDefinitions(globalScope *Scope) {
    repoRoot, _ := findRepoRoot()

    // Load lib.es5.d.ts
    libES5Path := filepath.Join(repoRoot, "node_modules", "typescript", "lib", "lib.es5.d.ts")
    c.loadGlobalFile(libES5Path, globalScope)

    // Load lib.dom.d.ts
    libDOMPath := filepath.Join(repoRoot, "node_modules", "typescript", "lib", "lib.dom.d.ts")
    c.loadGlobalFile(libDOMPath, globalScope)
}
```

### Architecture Flow

```
Prelude() [global scope factory]
    ↓
initializeGlobalScope()
    ↓
loadGlobalDefinitions()
    ├── loadGlobalFile("lib.es5.d.ts")
    └── loadGlobalFile("lib.dom.d.ts")
        ↓
    loadClassifiedTypeScriptModule()
        ↓
    DtsParser.ParseModule() → dts_parser.Module
        ↓
    ClassifyDTSFile() → FileClassification
        ├── GlobalDecls
        ├── PackageDecls
        └── NamedModules
            ↓
        interop.ConvertModule() → ast.Module
            ↓
        Checker.InferModule() → type_system.Namespace
            ↓
        Global namespace populated with types
```

## Incremental Adoption Strategy

Rather than implementing support for all ES versions at once, we will adopt an incremental approach, starting with ES2015 and progressively adding later versions. This approach:

- **Reduces risk**: Each increment is smaller and easier to test
- **Delivers value early**: ES2015 types (Map, Set, Promise, Symbol) are the most impactful
- **Identifies parser gaps incrementally**: Later ES versions tend to use more advanced TypeScript syntax
- **Allows course correction**: Lessons learned in earlier increments inform later work

### Increment 1: ES2015 (Highest Priority)

**Target lib files:**
```
lib.es2015.symbol.d.ts
lib.es2015.symbol.wellknown.d.ts
lib.es2015.iterable.d.ts
lib.es2015.generator.d.ts
lib.es2015.core.d.ts
lib.es2015.collection.d.ts
lib.es2015.promise.d.ts
lib.es2015.proxy.d.ts
lib.es2015.reflect.d.ts
```

**Key types unlocked:**
- `Map<K, V>`, `Set<T>`, `WeakMap<K, V>`, `WeakSet<T>`
- `Promise<T>` static methods (`all`, `race`, `resolve`, `reject`)
- `Symbol` and well-known symbols (`Symbol.iterator`, `Symbol.toStringTag`, etc.)
- `Generator<T>`, `Iterable<T>`, `Iterator<T>`, `IterableIterator<T>`
- `Proxy`, `ProxyHandler<T>`, `Reflect`
- Array methods: `find`, `findIndex`, `fill`, `copyWithin`, `entries`, `keys`, `values`
- Object methods: `assign`, `keys`, `values`, `entries`
- String methods: `startsWith`, `endsWith`, `includes`, `repeat`

**Expected parser challenges:**
- `unique symbol` type (lib.es2015.symbol.d.ts)
- Well-known symbol types (`[Symbol.iterator]()` method signatures)
- Complex generic constraints in iterable/iterator interfaces

**Success criteria for Increment 1:**
- [ ] All 9 ES2015 lib files parse without errors
- [ ] `Map`, `Set`, `WeakMap`, `WeakSet` types available and working
- [ ] `Promise.all()`, `Promise.race()` type-check correctly
- [ ] `Symbol` type available, well-known symbols accessible
- [ ] `for...of` iteration types work with `Iterable<T>`
- [ ] Array ES2015 methods (`find`, `findIndex`, etc.) available

### Increment 2: ES2016-ES2017

**Target lib files:**
```
lib.es2016.array.include.d.ts
lib.es2017.object.d.ts
lib.es2017.string.d.ts
lib.es2017.sharedmemory.d.ts
lib.es2017.typedarrays.d.ts
lib.es2017.intl.d.ts
```

**Key types unlocked:**
- `Array.prototype.includes`
- `Object.values`, `Object.entries`, `Object.getOwnPropertyDescriptors`
- `String.prototype.padStart`, `String.prototype.padEnd`
- `SharedArrayBuffer`, `Atomics`
- `Intl.DateTimeFormat` options

**Expected parser challenges:**
- Minimal - these files use straightforward interface extensions

**Success criteria for Increment 2:**
- [ ] All ES2016-ES2017 lib files parse without errors
- [ ] `arr.includes(x)` type-checks correctly
- [ ] `Object.values()`, `Object.entries()` return correct types
- [ ] `"hello".padStart(10)` type-checks correctly

### Increment 3: ES2018-ES2019

**Target lib files:**
```
lib.es2018.asyncgenerator.d.ts
lib.es2018.asynciterable.d.ts
lib.es2018.intl.d.ts
lib.es2018.promise.d.ts
lib.es2018.regexp.d.ts
lib.es2019.array.d.ts
lib.es2019.object.d.ts
lib.es2019.string.d.ts
lib.es2019.symbol.d.ts
lib.es2019.intl.d.ts
```

**Key types unlocked:**
- `AsyncGenerator<T>`, `AsyncIterable<T>`, `AsyncIterator<T>`
- `Promise.prototype.finally()`
- `Array.prototype.flat()`, `Array.prototype.flatMap()`
- `Object.fromEntries()`
- `String.prototype.trimStart()`, `String.prototype.trimEnd()`
- `Symbol.prototype.description`
- RegExp named capture groups

**Expected parser challenges:**
- Async iterator types have complex generic signatures
- `Promise.finally()` needs to preserve error type `E`

**Success criteria for Increment 3:**
- [ ] All ES2018-ES2019 lib files parse without errors
- [ ] `for await...of` iteration types work
- [ ] `Promise.finally()` preserves both `T` and `E` types
- [ ] `arr.flat()`, `arr.flatMap()` type-check correctly
- [ ] `Object.fromEntries()` returns correct types

### Increment 4: ES2020

**Target lib files:**
```
lib.es2020.bigint.d.ts
lib.es2020.promise.d.ts
lib.es2020.string.d.ts
lib.es2020.symbol.wellknown.d.ts
lib.es2020.intl.d.ts
lib.es2020.sharedmemory.d.ts
lib.es2020.date.d.ts
lib.es2020.number.d.ts
```

**Key types unlocked:**
- `BigInt`, `BigInt64Array`, `BigUint64Array`
- `Promise.allSettled()`
- `String.prototype.matchAll()`
- `globalThis`
- `Intl.RelativeTimeFormat`, `Intl.Locale`

**Expected parser challenges:**
- `BigInt` primitive type handling
- `globalThis` type (may need special handling as a global variable)

**Success criteria for Increment 4:**
- [ ] All ES2020 lib files parse without errors
- [ ] `BigInt(123)` type-checks correctly
- [ ] `Promise.allSettled()` preserves tuple structure with error type `never`
- [ ] `globalThis` is accessible

### Increment 5: ES2021

**Target lib files:**
```
lib.es2021.promise.d.ts
lib.es2021.string.d.ts
lib.es2021.weakref.d.ts
lib.es2021.intl.d.ts
```

**Key types unlocked:**
- `Promise.any()` - returns `Promise<T, AggregateError<Errors>>` with tuple-typed errors
- `AggregateError`
- `String.prototype.replaceAll()`
- `WeakRef<T>`, `FinalizationRegistry<T>`
- `Intl.ListFormat`, `Intl.DateTimeFormat` improvements

**Expected parser challenges:**
- `Promise.any()` needs special handling to return `AggregateError<Errors>` (with tuple-typed errors) instead of `never`

**Success criteria for Increment 5:**
- [ ] All ES2021 lib files parse without errors
- [ ] `Promise.any()` returns `Promise<T, AggregateError<Errors>>` with tuple-typed errors
- [ ] `WeakRef<T>` and `FinalizationRegistry<T>` available
- [ ] `"hello".replaceAll("l", "x")` type-checks correctly

### Increment 6: ES2022-ES2023

**Target lib files:**
```
lib.es2022.array.d.ts
lib.es2022.object.d.ts
lib.es2022.string.d.ts
lib.es2022.regexp.d.ts
lib.es2022.error.d.ts
lib.es2022.intl.d.ts
lib.es2022.sharedmemory.d.ts
lib.es2023.array.d.ts
lib.es2023.collection.d.ts
lib.es2023.intl.d.ts
```

**Key types unlocked:**
- `Array.prototype.at()`, `String.prototype.at()`
- `Object.hasOwn()`
- `Error.cause` property
- `Array.prototype.findLast()`, `Array.prototype.findLastIndex()`
- `Array.prototype.toReversed()`, `Array.prototype.toSorted()`, `Array.prototype.toSpliced()`, `Array.prototype.with()`

**Expected parser challenges:**
- These files generally use simpler syntax
- `Error.cause` may involve recursive type definitions

**Success criteria for Increment 6:**
- [ ] All ES2022-ES2023 lib files parse without errors
- [ ] `arr.at(-1)` type-checks correctly
- [ ] `Object.hasOwn(obj, "key")` type-checks correctly
- [ ] `arr.findLast(predicate)` type-checks correctly
- [ ] Immutable array methods (`toReversed`, etc.) available

### Increment Timeline

| Increment | ES Version(s) | Files | Priority | Dependencies |
|-----------|---------------|-------|----------|--------------|
| 1 | ES2015 | 9 | **Critical** | ES5 (done) |
| 2 | ES2016-ES2017 | 6 | High | Increment 1 |
| 3 | ES2018-ES2019 | 10 | High | Increment 2 |
| 4 | ES2020 | 8 | Medium | Increment 3 |
| 5 | ES2021 | 4 | Medium | Increment 4 |
| 6 | ES2022-ES2023 | 10 | Low | Increment 5 |

**Recommendation**: Complete Increment 1 (ES2015) fully before starting Increment 2. ES2015 introduces the foundational types (iterators, generators, symbols) that later versions build upon. Parser gaps discovered in ES2015 will likely need to be fixed before later increments can proceed.

---

## Implementation Tasks

### Phase 1: Add Additional Lib Files (Core)

**Location**: `internal/checker/prelude.go`

**Task 1.1**: Discover and filter lib files dynamically

Rather than hardcoding the list of lib files, discover them from the TypeScript installation. The dependency order is determined by:
1. Loading `lib.es5.d.ts` first (it contains actual type definitions)
2. For each subsequent ES version bundle (lib.es2015.d.ts, lib.es2016.d.ts, ...), parsing `/// <reference lib="..." />` directives to get sub-libraries in the correct order

```go
// referenceDirectivePattern matches /// <reference lib="es2015.core" /> directives
// Compiled once at package level for efficiency.
var referenceDirectivePattern = regexp.MustCompile(`/// <reference lib="([^"]+)" />`)

// bundleFilePattern matches ES2015+ bundle files like lib.es2015.d.ts, lib.es2016.d.ts, etc.
// These bundle files contain only /// <reference> directives pointing to sub-libraries.
// Note: lib.es5.d.ts is NOT a bundle - it contains actual type definitions.
// Compiled once at package level for efficiency.
var bundleFilePattern = regexp.MustCompile(`^lib\.es20\d{2}\.d\.ts$`)

// discoverESLibFiles returns ES lib files from the TypeScript lib directory,
// sorted in dependency order based on reference directives in bundle files.
//
// Load order:
// 1. lib.es5.d.ts (contains actual types, loaded first)
// 2. Sub-libraries referenced by lib.es2015.d.ts (in order)
// 3. Sub-libraries referenced by lib.es2016.d.ts (in order)
// 4. ... and so on for each ES version
func discoverESLibFiles(libDir string) ([]string, error) {
    // Find all ES2015+ bundle files (lib.es2015.d.ts, lib.es2016.d.ts, etc.)
    // These contain /// <reference> directives pointing to sub-libraries.
    entries, err := os.ReadDir(libDir)
    if err != nil {
        return nil, fmt.Errorf("failed to read lib directory: %w", err)
    }

    var bundleFiles []string
    for _, entry := range entries {
        name := entry.Name()
        if isBundleFile(name) && !isESNextFile(name) {
            bundleFiles = append(bundleFiles, name)
        }
    }

    // Sort bundle files by ES version (es2015, es2016, ...)
    sort.Slice(bundleFiles, func(i, j int) bool {
        return compareESVersions(extractESVersion(bundleFiles[i]), extractESVersion(bundleFiles[j]))
    })

    var orderedLibFiles []string
    seen := make(map[string]bool)

    // lib.es5.d.ts contains actual type definitions (not just references).
    // Load it first as the base of all ES types.
    orderedLibFiles = append(orderedLibFiles, "lib.es5.d.ts")
    seen["lib.es5.d.ts"] = true

    // For each ES2015+ bundle, parse its /// <reference> directives
    // to get sub-libraries in the correct order.
    for _, bundleFile := range bundleFiles {
        bundlePath := filepath.Join(libDir, bundleFile)
        refs, err := parseReferenceDirectives(bundlePath)
        if err != nil {
            return nil, fmt.Errorf("failed to parse %s: %w", bundleFile, err)
        }

        for _, ref := range refs {
            // Convert reference name to filename: "es2015.core" -> "lib.es2015.core.d.ts"
            filename := "lib." + ref + ".d.ts"
            if !seen[filename] && !isESNextFile(filename) {
                orderedLibFiles = append(orderedLibFiles, filename)
                seen[filename] = true
            }
        }
    }

    return orderedLibFiles, nil
}

// parseReferenceDirectives extracts lib references from a bundle file.
// Example: /// <reference lib="es2015.core" /> -> "es2015.core"
func parseReferenceDirectives(bundlePath string) ([]string, error) {
    content, err := os.ReadFile(bundlePath)
    if err != nil {
        return nil, err
    }

    matches := referenceDirectivePattern.FindAllStringSubmatch(string(content), -1)
    var refs []string
    for _, match := range matches {
        if len(match) >= 2 {
            refs = append(refs, match[1])
        }
    }
    return refs, nil
}

// isBundleFile returns true for ES2015+ bundle files like lib.es2015.d.ts, lib.es2016.d.ts, etc.
// These bundle files contain only /// <reference> directives to sub-libraries.
// Note: lib.es5.d.ts is NOT a bundle - it contains actual type definitions.
func isBundleFile(name string) bool {
    return bundleFilePattern.MatchString(name)
}

// isESNextFile returns true for lib.esnext.*.d.ts files (unstable features)
func isESNextFile(name string) bool {
    return strings.HasPrefix(name, "lib.esnext")
}

// extractESVersion extracts the ES version from a filename.
// "lib.es2015.core.d.ts" -> "es2015", "lib.es5.d.ts" -> "es5"
func extractESVersion(filename string) string {
    // Remove "lib." prefix and ".d.ts" suffix, then take first segment
    name := strings.TrimPrefix(filename, "lib.")
    name = strings.TrimSuffix(name, ".d.ts")
    parts := strings.Split(name, ".")
    if len(parts) > 0 {
        return parts[0]
    }
    return name
}

// compareESVersions returns true if version a should be loaded before version b.
// Only used for ES2015+ versions: "es2015" < "es2016" < ... < "es2023"
// (lib.es5.d.ts is handled separately, not via this comparison)
func compareESVersions(a, b string) bool {
    // Both are es20XX, compare lexicographically (works because es2015 < es2016 < ...)
    return a < b
}
```

**Why this approach?**

TypeScript's lib files have two distinct patterns:

1. **`lib.es5.d.ts`** - Contains actual type definitions (Array, Object, String, Function, etc.). This is the base of the type hierarchy and must be loaded first.

2. **ES2015+ bundle files** (`lib.es2015.d.ts`, `lib.es2016.d.ts`, etc.) - These are pure reference files that only contain `/// <reference lib="..." />` directives pointing to sub-libraries. For example, `lib.es2015.d.ts` contains:

```typescript
/// <reference lib="es5" />
/// <reference lib="es2015.core" />
/// <reference lib="es2015.collection" />
/// <reference lib="es2015.iterable" />
/// <reference lib="es2015.generator" />
/// <reference lib="es2015.promise" />
/// <reference lib="es2015.proxy" />
/// <reference lib="es2015.reflect" />
/// <reference lib="es2015.symbol" />
/// <reference lib="es2015.symbol.wellknown" />
```

By processing bundle files in version order and extracting their references:
1. **Guarantees correct order**: Dependencies are defined by TypeScript itself
2. **Handles intra-version dependencies**: References are listed in the order TypeScript expects (e.g., `es2015.symbol` before `es2015.iterable` which uses Symbol)
3. **Automatically adapts**: When TypeScript adds new lib files, they'll be discovered via their bundle
4. **Avoids guessing**: No need to infer order from naming conventions
5. **Deduplicates automatically**: The `seen` map ensures each file is loaded only once (e.g., `es5` referenced by `lib.es2015.d.ts` is skipped since we already loaded `lib.es5.d.ts`)

**Task 1.2**: Update `loadGlobalDefinitions()` to use dynamic discovery

```go
func (c *Checker) loadGlobalDefinitions(globalScope *Scope) {
    repoRoot, err := findRepoRoot()
    if err != nil {
        panic(fmt.Sprintf("failed to find repository root: %v", err))
    }

    libDir := filepath.Join(repoRoot, "node_modules", "typescript", "lib")

    // Verify TypeScript is installed
    if _, statErr := os.Stat(libDir); statErr != nil {
        if os.IsNotExist(statErr) {
            panic(fmt.Sprintf(
                "TypeScript lib directory not found at %s. "+
                "Please install TypeScript: npm install typescript",
                libDir,
            ))
        }
        panic(fmt.Sprintf("cannot access TypeScript lib directory %s: %v", libDir, statErr))
    }

    // Discover and load ES lib files
    esLibFiles, err := discoverESLibFiles(libDir)
    if err != nil {
        // Hard error - can't proceed without lib files
        panic(fmt.Sprintf("failed to discover ES lib files: %v", err))
    }

    if len(esLibFiles) == 0 {
        panic(fmt.Sprintf(
            "no ES lib files found in %s. "+
            "TypeScript installation may be corrupted. "+
            "Try: rm -rf node_modules && npm install",
            libDir,
        ))
    }

    for _, filename := range esLibFiles {
        libPath := filepath.Join(libDir, filename)
        c.loadGlobalFile(libPath, globalScope)
    }

    // Load DOM lib file after ES lib files
    // DOM types may reference ES2015+ types (e.g., Promise, Symbol)
    libDOMPath := filepath.Join(libDir, "lib.dom.d.ts")
    c.loadGlobalFile(libDOMPath, globalScope)
}
```

### Phase 2: Verify Declaration Merging

**Location**: `internal/checker/infer_module.go`

**Task 2.1**: Verify `MergeInterface()` handles cross-file merging

The existing `MergeInterface()` function should already handle declaration merging since lib files are inferred sequentially into the same global scope. Verify that:

1. Interfaces defined in lib.es5.d.ts (e.g., `Array<T>`) are correctly extended by lib.es2015.core.d.ts
2. Methods added in later lib files appear on the merged interface

**Task 2.2**: Add test cases for declaration merging

Create tests that verify:
- `Array<T>` has `find()` from ES2015
- `Array<T>` has `includes()` from ES2016
- `Array<T>` has `flat()` from ES2019
- `Array<T>` has `at()` from ES2022

### Phase 2.5: Extend Promise<T, E> Augmentation for ES2015+ Lib Files

**Location**: `internal/interop/decl.go`

The existing `PromiseVisitor` augments `Promise<T>` to `Promise<T, E>` for the base Promise interface. However, ES2015+ lib files introduce additional Promise-related types and methods that need similar treatment.

#### Task 2.5.1: Audit Promise-Related Types in ES2015+ Lib Files

Identify all Promise-related declarations that need augmentation:

| Lib File | Declarations | Required Augmentation |
|----------|--------------|----------------------|
| `lib.es2015.promise.d.ts` | `Promise<T>`, `PromiseLike<T>`, `PromiseConstructorLike` | Already handled ✅ |
| `lib.es2018.promise.d.ts` | `Promise.finally()` method | Return type should preserve error type |
| `lib.es2020.promise.d.ts` | `Promise.allSettled()`, `PromiseSettledResult<T>`, `PromiseRejectedResult` | Augment `PromiseRejectedResult<E>` and `PromiseSettledResult<T, E>` for typed errors |
| `lib.es2021.promise.d.ts` | `Promise.any()`, `AggregateError` | Augment `AggregateError<Errors>` for tuple-typed errors, returns `Promise<T, AggregateError<Errors>>` |

#### Task 2.5.2: Static Methods Must Preserve Error Types

Promise static methods must propagate error types from their input promises. This is essential for Escalier's typed error handling.

**Helper type for extracting error types:**

Escalier needs an `AwaitedError<T>` helper type (analogous to TypeScript's `Awaited<T>` for values):

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
```

**Simple methods:**

1. **Promise.resolve**: Returns `Promise<T, never>`
   - Wrapping a value in a resolved promise never throws
   - Example: `Promise.resolve(42)` → `Promise<number, never>`

2. **Promise.reject**: Returns `Promise<never, E>` where `E` is the rejection type
   - Example: `Promise.reject(new Error("fail"))` → `Promise<never, Error>`
   - Requires capturing the argument type as `E`

**`Promise.all`, `Promise.race`, `Promise.allSettled`, and `Promise.any` with mapped type signatures:**

These methods use TypeScript's mapped types to preserve heterogeneous tuple structure. Escalier extends them to also extract and union error types.

1. **Promise.all**: Uses mapped type to preserve tuple structure for values
   ```typescript
   // TypeScript's official signature (lib.es2015.promise.d.ts):
   // all<T extends readonly unknown[] | []>(values: T): Promise<{ -readonly [P in keyof T]: Awaited<T[P]>; }>;

   // Escalier's augmented signature:
   all<T extends readonly unknown[] | []>(values: T): Promise<
       { -readonly [P in keyof T]: Awaited<T[P]>; },           // Value: tuple of awaited values
       { [P in keyof T]: AwaitedError<T[P]> }[keyof T]         // Error: union of all error types
   >;
   ```
   - If any input promise rejects, the entire operation rejects with that error
   - Example: `Promise.all([fetchUser(1), fetchPost(1)])` → `Promise<[User, Post], UserFetchError | PostFetchError>`

2. **Promise.race**: Uses mapped type to union values, extracts error types
   ```typescript
   // Escalier's augmented signature:
   race<T extends readonly unknown[] | []>(values: T): Promise<
       Awaited<T[number]>,                                      // Value: union of awaited values
       { [P in keyof T]: AwaitedError<T[P]> }[keyof T]         // Error: union of all error types
   >;
   ```
   - The first promise to settle (resolve or reject) determines the result
   - Example: `Promise.race([fetchUser(1), fetchPost(1)])` → `Promise<User | Post, UserFetchError | PostFetchError>`

3. **Promise.allSettled**: Uses mapped type to preserve tuple structure for settled results with typed errors
   ```typescript
   // TypeScript's official signature (lib.es2020.promise.d.ts):
   // allSettled<T extends readonly unknown[] | []>(values: T): Promise<{ -readonly [P in keyof T]: PromiseSettledResult<Awaited<T[P]>>; }>;

   // Escalier's augmented signature:
   allSettled<T extends readonly unknown[] | []>(values: T): Promise<
       { -readonly [P in keyof T]: PromiseSettledResult<Awaited<T[P]>, AwaitedError<T[P]>>; },  // Value: tuple of settled results with typed errors
       never                                                                                     // Error: always succeeds
   >;
   ```
   - Always resolves, never rejects (rejections become `PromiseRejectedResult<E>` objects with typed `reason`)
   - Example: `Promise.allSettled([fetchUser(1), fetchPost(1)])` → `Promise<[PromiseSettledResult<User, UserFetchError>, PromiseSettledResult<Post, PostFetchError>], never>`

4. **Promise.any**: Uses mapped type to produce tuple-typed AggregateError
   ```typescript
   // TypeScript's official signature (lib.es2021.promise.d.ts):
   // any<T>(values: Iterable<T | PromiseLike<T>>): Promise<Awaited<T>>;

   // Escalier's augmented signature:
   any<T extends readonly unknown[] | []>(values: T): Promise<
       Awaited<T[number]>,                                         // Value: union of awaited values (first to resolve)
       AggregateError<{ -readonly [P in keyof T]: AwaitedError<T[P]> }>  // Error: AggregateError with tuple of error types
   >;
   ```
   - Resolves with the first fulfilled promise's value; rejects with `AggregateError<Errors>` only if ALL promises reject
   - Example: `Promise.any([fetchUser(1), fetchPost(1)])` → `Promise<User | Post, AggregateError<[UserFetchError, PostFetchError]>>`

**How the mapped types work:**
- `{ -readonly [P in keyof T]: Awaited<T[P]>; }` - Maps each tuple element to its awaited value type, preserving tuple structure
- `{ [P in keyof T]: AwaitedError<T[P]> }[keyof T]` - Maps each element to its error type, then indexes with `keyof T` to get the union
- `Awaited<T[number]>` - For `Promise.race` and `Promise.any`, gets the union of all awaited value types
- `AggregateError<{ -readonly [P in keyof T]: AwaitedError<T[P]> }>` - For `Promise.any`, wraps the tuple of error types in `AggregateError<Errors>`, preserving the structure so `errors[0]` has the error type of the first promise, `errors[1]` has the error type of the second, etc.
- `AwaitedError<T>` returns `never` for non-promise values, so they don't pollute the error type union

#### Task 2.5.3: Instance Methods Must Preserve Error Types

Promise instance methods must properly track error types through promise chains.

**TODO:** Figure out the correct types for `then`, `catch`, and `finally` to
correctly propagate error types in different situations.

**Method signatures with error type tracking:**

1. **then()**: Returns `Promise<TResult1 | TResult2, E2>` where `E2` depends on handlers
   ```typescript
   then<TResult1, TResult2, E2>(
       onfulfilled?: (value: T) => TResult1 | PromiseLike<TResult1, E2>,
       onrejected?: (reason: E) => TResult2 | PromiseLike<TResult2, E2>
   ): Promise<TResult1 | TResult2, E2>;
   ```
   - If `onrejected` is provided: error is handled, new error type comes from handlers
   - If `onrejected` is NOT provided: original error type `E` propagates
   - Example: `promise.then(x => x + 1)` on `Promise<number, ApiError>` → `Promise<number, ApiError>`
   - Example: `promise.then(x => x, err => fallback)` → `Promise<number, never>` (error handled)

2. **catch()**: Returns `Promise<T | TResult, E2>` where error is handled
   ```typescript
   catch<TResult, E2>(
       onrejected?: (reason: E) => TResult | PromiseLike<TResult, E2>
   ): Promise<T | TResult, E2>;
   ```
   - The original error type `E` is consumed by the handler
   - New error type `E2` comes from the handler's return type (or `never` if it returns a value)
   - Example: `promise.catch(err => defaultValue)` → `Promise<T | DefaultType, never>`
   - Example: `promise.catch(err => { throw new OtherError() })` → `Promise<T, OtherError>`

3. **finally()**: Returns `Promise<T, E>` preserving both types
   ```typescript
   finally(onfinally?: () => void): Promise<T, E>;
   ```
   - `finally` does not transform the value or error, just runs cleanup
   - Both `T` and `E` pass through unchanged
   - Example: `promise.finally(() => cleanup())` preserves original `Promise<T, E>`

**Implementation approach:**

The instance methods require modifying how the Promise interface is augmented:

1. Add `E` type parameter to the interface itself (already done via `PromiseVisitor`)
2. Update method signatures to reference `E` appropriately
3. For `then` with no `onrejected`: propagate `E` to the return type
4. For `then` with `onrejected` or `catch`: compute new error type from handler
5. For `finally`: preserve `E` in return type

This requires the `PromiseVisitor` to transform method signatures, not just add the type parameter.

#### Task 2.5.4: Implementation Strategy

**Full error type propagation approach:**

To properly track error types through Promise operations, the `PromiseVisitor` must transform both interface declarations and method signatures.

1. **Interface augmentation**: Add `E` type parameter with default to `Promise` and `PromiseLike` interfaces
   ```go
   // In interop/decl.go
   errorTypeParam := ast.NewTypeParam("E", nil, ast.NewNeverTypeAnn(...)) // default = never
   typeParams = append(typeParams, &errorTypeParam)
   ```

2. **Transform instance method signatures**: Update `then`, `catch`, and `finally` to use `E`:
   - `then()`: If no `onrejected`, propagate `E`; otherwise compute from handler
   - `catch()`: Original `E` is handled, new error type from handler
   - `finally()`: Preserve `E` in return type

3. **Transform static method signatures**: Update `PromiseConstructor` methods:
   - `Promise.resolve()`: Return `Promise<T, never>`
   - `Promise.reject()`: Return `Promise<never, E>` capturing argument type
   - `Promise.any()`: Return `Promise<T, AggregateError<Errors>>` with tuple of error types
   - `Promise.all()`: Use mapped type signature with `AwaitedError<T>` for error union
   - `Promise.race()`: Use mapped type signature with `AwaitedError<T>` for error union
   - `Promise.allSettled()`: Use mapped type signature with error type `never`

4. **Implement `AwaitedError<T>` helper type**:
   ```typescript
   type AwaitedError<T> =
       T extends Promise<any, infer E> ? E :
       T extends PromiseLike<any, infer E> ? E :
       never;
   ```
   This type extracts the error type from a promise, returning `never` for non-promise values.

5. **PromiseVisitor responsibilities**:
   - Add `E` type parameter to interface declarations
   - Transform method return types to reference `E` appropriately
   - Handle `PromiseConstructor` interface (static methods)
   - Transform mapped type signatures to include error type extraction
   - Ensure `AwaitedError<T>` helper type is available in global scope

6. **Checker support**: May need checker-level logic for:
   - Extracting `E` from `Promise<T, E>` types via `AwaitedError<T>`
   - Evaluating mapped types like `{ [P in keyof T]: AwaitedError<T[P]> }[keyof T]`
   - Computing union of error types from tuple/array of promises
   - Inferring error types from callback return types in `then`/`catch`

#### Task 2.5.5: Verify Existing Behavior is Preserved

Ensure the current Promise<T, E> behavior continues to work:

1. Async functions infer error types from throw statements
2. Await expressions extract error types from promises
3. Error types propagate through async function calls
4. `Promise<T>` (single param) defaults to `Promise<T, never>` or `Promise<T, any>` as appropriate

### Phase 2.7: Handle Constructor/Prototype Cyclic Dependencies (FR4)

**Location**: `internal/dep_graph/dep_graph.go`

TypeScript's standard library uses a pattern where constructor interfaces (e.g., `SymbolConstructor`) reference their instance type (e.g., `Symbol`) via a `prototype` property. This creates cyclic dependencies that must be handled specially. See FR4 in `requirements.md` for detailed background.

**The Problem:**

```typescript
interface SymbolConstructor {
    readonly prototype: Symbol;  // Creates dependency on Symbol type
    readonly toPrimitive: unique symbol;
}

declare var Symbol: SymbolConstructor;  // Depends on SymbolConstructor type

interface Symbol {
    [Symbol.toPrimitive](hint: string): symbol;  // Depends on Symbol VALUE
}
```

This creates a cycle:
1. `SymbolConstructor` (type) → `Symbol` (type) via `prototype: Symbol`
2. `Symbol` (type) → `Symbol` (value) via `[Symbol.toPrimitive]`
3. `Symbol` (value) → `SymbolConstructor` (type) via type annotation

**Originally Chosen Solution: Option C - Special-Case `prototype` by Naming Convention (with cycle detection)**

> **⚠️ IMPLEMENTATION NOTE:** This approach was attempted but **does not work** with the current checker architecture. See "Why Option C Fails" below for details.

**Important:** Not all `Foo`/`FooConstructor` patterns have cycles. The cycle only exists when:
1. `FooConstructor` has `prototype: Foo` (type depends on instance type)
2. `interface Foo` uses computed keys like `[Foo.someProperty]` (type depends on value)
3. `var Foo: FooConstructor` (value depends on constructor type)

Currently, **`Symbol`/`SymbolConstructor` is the primary case** where this cycle occurs. Most other constructor interfaces (e.g., `ArrayConstructor`, `DateConstructor`) don't have this issue because their instance interfaces don't use computed keys.

**Original Approach (Option C):**
1. Build the dependency graph normally (include all dependencies)
2. Compute SCCs to detect cycles
3. Only when a cycle is detected involving `FooConstructor` and `Foo` via `prototype`, remove that dependency
4. Recompute SCCs after removing the edge

---

### Why Option C Fails

The checker's `InferComponent` function uses a **two-phase approach within each SCC**:

1. **Placeholder phase**: Creates fresh type variables for ALL bindings in the component BEFORE any are fully inferred
2. **Definition phase**: Infers the actual types and unifies with placeholders

When cyclic types are in the **same SCC**, the placeholder phase ensures all types have bindings before any are resolved, allowing mutual references to work correctly.

**The Problem with Breaking Cycles:**

Breaking cycles by removing edges **separates types into different SCCs**. For example, after removing the `SymbolConstructor → Symbol` dependency:

- `SymbolConstructor` (type) → own SCC (no dependencies)
- `Symbol` (value) → depends on `SymbolConstructor`
- `Symbol` (type) → depends on `Symbol` (value)

These become **separate SCCs processed sequentially**. When `SymbolConstructor` is processed, `Symbol` type doesn't have a placeholder yet (it's in a later SCC), causing "Unknown type: Symbol" errors.

**Correct Approach: Option D - Ordered Processing Within SCCs**

Instead of breaking cycles, keep all related types in the same SCC but enforce a specific **processing order within the component**:

1. Process all `*Constructor` interfaces first (they define properties like `toPrimitive`)
2. Process all value declarations next (they create value bindings referencing constructors)
3. Process instance type interfaces last (they can now resolve computed keys)

This requires modifying `InferComponent` to sort bindings within an SCC, not the dependency graph.

---

#### Task 2.7.1: Create Binding Key Sorting Function

**Location**: `internal/checker/infer_module.go`

Create a function to sort binding keys within an SCC so that constructor interfaces are processed before their instance types:

```go
// sortComponentBindings sorts binding keys within an SCC to ensure correct processing order:
// 1. *Constructor type bindings (define properties like toPrimitive)
// 2. Value bindings (create value bindings referencing constructors)
// 3. Instance type bindings (can now resolve computed keys like [Symbol.toPrimitive])
func sortComponentBindings(component []dep_graph.BindingKey) []dep_graph.BindingKey {
    sorted := make([]dep_graph.BindingKey, len(component))
    copy(sorted, component)

    slices.SortFunc(sorted, func(a, b dep_graph.BindingKey) int {
        priorityA := getBindingPriority(a)
        priorityB := getBindingPriority(b)
        return priorityA - priorityB
    })

    return sorted
}

// getBindingPriority returns the processing priority for a binding key.
// Lower numbers are processed first.
func getBindingPriority(key dep_graph.BindingKey) int {
    name := key.Name()
    isType := key.IsTypeBinding()

    // Priority 0: *Constructor type bindings (e.g., SymbolConstructor)
    // These define properties like toPrimitive that other types reference
    if isType && strings.HasSuffix(name, "Constructor") {
        return 0
    }

    // Priority 1: Value bindings (e.g., Symbol value)
    // These create value bindings that can be used in computed keys
    if !isType {
        return 1
    }

    // Priority 2: Other type bindings (e.g., Symbol type)
    // These may use computed keys that reference values
    return 2
}
```

#### Task 2.7.2: Integrate Sorting into InferComponent

**Location**: `internal/checker/infer_module.go`

Update `InferComponent` to sort the component before processing:

```go
func (c *Checker) InferComponent(
    ctx Context,
    depGraph *dep_graph.DepGraph,
    component []dep_graph.BindingKey,
) []Error {
    errors := []Error{}

    // Sort the component to ensure correct processing order for cyclic dependencies
    // (e.g., SymbolConstructor must be processed before Symbol type)
    sortedComponent := sortComponentBindings(component)

    // ... rest of the function uses sortedComponent instead of component ...
}
```

The key insight is that within an SCC:
- All bindings get placeholders in the **placeholder phase** before any are resolved
- The **definition phase** resolves types and unifies with placeholders
- By sorting, we ensure `SymbolConstructor` is fully defined (including `toPrimitive`) before `Symbol` type tries to resolve `[Symbol.toPrimitive]`

#### Task 2.7.3: Handle Placeholder Phase Ordering

The placeholder phase already creates type variables for all bindings before resolution. However, we need to ensure that when resolving `SymbolConstructor`:

1. The `Symbol` type has a placeholder (it does, since they're in the same SCC)
2. The `prototype: Symbol` reference can resolve to this placeholder
3. Later, when `Symbol` type is resolved, the placeholder is unified with the actual type

The current checker architecture already supports this via the two-phase approach. The sorting ensures that when `Symbol` type is being resolved in the definition phase, `SymbolConstructor` has already been fully defined (including `toPrimitive` as a unique symbol).

#### Task 2.7.4: Add Tests for SCC Ordering

**Location**: `internal/checker/tests/` (new test file or add to existing)

```go
func TestSymbolConstructorCyclicDependency(t *testing.T) {
    // This test verifies that the Symbol/SymbolConstructor cycle is handled correctly
    // by processing them in the right order within the same SCC
    sources := []*ast.Source{
        {
            ID:   0,
            Path: "test.esc",
            Contents: `
                interface SymbolConstructor {
                    prototype: Symbol,
                    toPrimitive: unique symbol,
                }

                declare var Symbol: SymbolConstructor

                interface Symbol {
                    [Symbol.toPrimitive]: fn (hint: string) -> symbol,
                }

                // Test that we can use Symbol.toPrimitive
                declare var testSym: Symbol
            `,
        },
    }

    checker := NewChecker()
    // ... setup and run inference ...

    // Verify SymbolConstructor.prototype has type Symbol
    symConstructor := checker.GlobalScope.GetTypeAlias("SymbolConstructor")
    require.NotNil(t, symConstructor)

    objType, ok := type_system.Prune(symConstructor.Type).(*type_system.ObjectType)
    require.True(t, ok)

    // Find and verify prototype property
    var prototypeType type_system.Type
    for _, elem := range objType.Elems {
        if prop, ok := elem.(*type_system.PropertyElem); ok {
            if prop.Name.Kind == type_system.StrObjTypeKeyKind && prop.Name.Str == "prototype" {
                prototypeType = prop.Value
                break
            }
        }
    }
    require.NotNil(t, prototypeType, "prototype property should exist")

    // Verify Symbol type has the [Symbol.toPrimitive] property
    symType := checker.GlobalScope.GetTypeAlias("Symbol")
    require.NotNil(t, symType)

    symObjType, ok := type_system.Prune(symType.Type).(*type_system.ObjectType)
    require.True(t, ok)

    // Should have at least one element (the Symbol.toPrimitive method)
    assert.GreaterOrEqual(t, len(symObjType.Elems), 1,
        "Symbol should have at least one element")
}

func TestSortComponentBindings(t *testing.T) {
    // Unit test for the sorting function
    component := []dep_graph.BindingKey{
        dep_graph.TypeBindingKey("Symbol"),           // Instance type - priority 2
        dep_graph.ValueBindingKey("Symbol"),          // Value - priority 1
        dep_graph.TypeBindingKey("SymbolConstructor"), // Constructor - priority 0
    }

    sorted := sortComponentBindings(component)

    assert.Equal(t, dep_graph.TypeBindingKey("SymbolConstructor"), sorted[0],
        "SymbolConstructor should be first")
    assert.Equal(t, dep_graph.ValueBindingKey("Symbol"), sorted[1],
        "Symbol value should be second")
    assert.Equal(t, dep_graph.TypeBindingKey("Symbol"), sorted[2],
        "Symbol type should be last")
}
```

#### Task 2.7.5: Verify with Real TypeScript Lib Files

After implementing the sorting, verify that loading `lib.es2015.symbol.d.ts` and `lib.es2015.symbol.wellknown.d.ts` works without "Unknown type" errors:

```go
func TestLoadES2015SymbolLibFiles(t *testing.T) {
    checker := NewChecker()

    // Load the prelude which includes ES2015 Symbol lib files
    // Prelude returns *Scope, not errors
    scope := Prelude(checker)

    // Collect any errors that were recorded during prelude loading
    errors := checker.Errors
    var symbolErrors []Error
    for _, err := range errors {
        if strings.Contains(err.Message(), "Symbol") {
            symbolErrors = append(symbolErrors, err)
        }
    }

    assert.Empty(t, symbolErrors,
        "Should not have Symbol-related errors after loading lib files")

    // Verify Symbol and SymbolConstructor are properly defined
    symConstructor := scope.GetTypeAlias("SymbolConstructor")
    assert.NotNil(t, symConstructor, "SymbolConstructor should be defined")

    symType := scope.GetTypeAlias("Symbol")
    assert.NotNil(t, symType, "Symbol type should be defined")

    symValue := scope.GetValue("Symbol")
    assert.NotNil(t, symValue, "Symbol value should be defined")
}
```

#### Expected Result

After implementing Option D:

1. **Dependency graph** remains unchanged - all cyclic types stay in the same SCC:
   ```
   SCC: [SymbolConstructor (type), Symbol (value), Symbol (type)]
   ```

2. **Processing order within SCC** is sorted:
   1. `SymbolConstructor` (type) - processed first, defines `toPrimitive` unique symbol
   2. `Symbol` (value) - processed second, creates value binding of type `SymbolConstructor`
   3. `Symbol` (type) - processed last, can now resolve `[Symbol.toPrimitive]`

3. **Type resolution** works because:
   - All three bindings have placeholders before any definition phase runs
   - `SymbolConstructor` is fully defined first, including its `toPrimitive` property
   - When `Symbol` type is processed, `Symbol.toPrimitive` resolves to the unique symbol
   - The `prototype: Symbol` reference in `SymbolConstructor` resolves to `Symbol`'s placeholder, which is later unified with the actual type

### Phase 3: Parser Compatibility

**Location**: `internal/dts_parser/`, `internal/ast/`, `internal/checker/`

Parsing new lib files may reveal TypeScript syntax not yet supported by the dts_parser. This could require changes at multiple levels:

1. **dts_parser** - Parse the new syntax into dts_parser AST nodes
2. **interop** - Convert dts_parser AST to Escalier AST
3. **ast** - Add new AST node types if needed
4. **type_system** - Add new type representations if needed
5. **checker** - Handle new types during type inference and checking

#### Task 3.1: Audit All Lib Files for Parser Compatibility

Run the dts_parser on each lib file and collect parsing errors:

```go
func TestParseAllLibFiles(t *testing.T) {
    libDir := filepath.Join(repoRoot, "node_modules", "typescript", "lib")
    libFiles, err := discoverESLibFiles(libDir)
    require.NoError(t, err, "failed to discover lib files")

    for _, filename := range libFiles {
        t.Run(filename, func(t *testing.T) {
            path := filepath.Join(libDir, filename)
            source, err := os.ReadFile(path)
            if err != nil {
                t.Errorf("failed to read %s: %v", filename, err)
                return
            }

            parser := NewDtsParser(string(source))
            _, parseErrors := parser.ParseModule()

            // Use t.Errorf instead of require.Empty to collect all failures
            // This allows the test to continue and report all files with errors
            for _, parseErr := range parseErrors {
                t.Errorf("parse error: %s", parseErr)
            }
        })
    }
}
```

#### Task 3.2: Identify New Syntax Patterns

Common TypeScript syntax that may appear in ES2015+ lib files:

| Syntax | Example | Lib File | Complexity |
|--------|---------|----------|------------|
| `unique symbol` | `declare const iterator: unique symbol` | lib.es2015.symbol.d.ts | Medium |
| `readonly` arrays | `readonly T[]` | lib.es2019+ | Low |
| Template literal types | `` `${string}px` `` | lib.es2021+ | High |
| `infer` keyword | `T extends Promise<infer U> ? U : T` | lib.es5.d.ts (Awaited) | Already supported ✓ |
| Mapped type modifiers | `+readonly`, `-optional` | Various | Medium |
| `const` type parameters | `<const T>` | lib.es2022+ | Medium |
| `satisfies` keyword | Not in lib files | N/A | N/A |
| Index signature syntax | `[K: string]: V` | Various | Already supported? |
| `asserts` return type | `asserts condition` | lib.es2019+ | Medium |
| `is` type predicate | `x is T` | Various | Medium |

**Note on `infer` keyword support**: The dts_parser already supports the `infer` keyword, as evidenced by:
- Lexer token: `INFER` in `dts_parser/lexer.go`
- AST node: `InferType` in `dts_parser/types.go`
- Parser function: `parseInferType()` in `dts_parser/parser.go`
- Tests: `infer` parsing tests in `dts_parser/parser_test.go`
- Real-world usage: Successfully parses `Awaited<T>` type in `lib.es5.d.ts` which uses `infer` in its conditional type definition

#### Task 3.3: Implement Missing Parser Support

For each unsupported syntax pattern, the implementation may span multiple components:

**Example: Adding `unique symbol` support**

1. **dts_parser/types.go** - Add `UniqueSymbolType` node
   ```go
   type UniqueSymbolType struct {
       // unique symbol has no additional fields
   }
   ```

2. **dts_parser/parser.go** - Parse `unique symbol` in type positions
   ```go
   func (p *Parser) parseType() Type {
       if p.match("unique") && p.peek() == "symbol" {
           p.advance() // consume "symbol"
           return &UniqueSymbolType{}
       }
       // ... existing type parsing
   }
   ```

3. **interop/types.go** - Convert to Escalier AST
   ```go
   case *dts_parser.UniqueSymbolType:
       return ast.NewUniqueSymbolTypeAnn(...)
   ```

4. **ast/types.go** - Add AST node (if not using existing Symbol type)
   ```go
   type UniqueSymbolTypeAnn struct {
       // ...
   }
   ```

5. **checker/infer_type_ann.go** - Infer the type
   ```go
   case *ast.UniqueSymbolTypeAnn:
       return type_system.NewUniqueSymbolType(...)
   ```

6. **type_system/types.go** - Add type representation
   ```go
   type UniqueSymbolType struct {
       // Each unique symbol is nominally distinct
       ID int // Unique identifier
   }
   ```

#### Task 3.4: Prioritize Syntax by Implementation Order

All syntax gaps must be fixed before a lib file can be used (no partial parsing). Prioritize based on:

1. **ES version dependency** - Earlier ES versions must work first (ES2015 before ES2016, etc.)
2. **Syntax frequency** - Fix syntax used in many declarations before rare syntax
3. **Implementation complexity** - Start with simpler syntax to build momentum

**Priority order:**
1. Fix all syntax gaps in ES2015 lib files first (Increment 1)
2. Then fix syntax gaps in ES2016-ES2017 (Increment 2)
3. Continue incrementally through later ES versions

**Note**: Since parsing failures are hard errors, each increment's lib files must parse completely before that increment is considered complete.

#### Task 3.5: Create Tracking Issue for Each Syntax Gap

For each unsupported syntax, document:
- Which lib files use it
- What types/declarations are affected
- Estimated complexity to implement
- Which increment is blocked by this gap

### Phase 4: Handle Reference Directives

**Location**: `internal/dts_parser/` and `internal/checker/prelude.go`

TypeScript lib files use triple-slash reference directives:

```typescript
/// <reference lib="es2015.symbol" />
/// <reference lib="es2015.iterable" />
```

**Task 4.1**: Parse and follow reference directives

The implementation should parse and follow `/// <reference lib="..." />` directives in `.d.ts` files to ensure correct load order. This is already implemented in Phase 1 (Task 1.1) for bundle files.

**How reference following works:**

1. **Bundle files** (`lib.es2015.d.ts`, `lib.es2016.d.ts`, etc.) contain only reference directives pointing to sub-libraries. These are parsed by `parseReferenceDirectives()` to discover sub-libraries in the correct order.

2. **Sub-library files** (`lib.es2015.core.d.ts`, `lib.es2015.collection.d.ts`, etc.) contain actual type definitions. Some sub-libraries may also contain reference directives to other sub-libraries they depend on.

3. **The `seen` map** in `discoverESLibFiles()` tracks which files have been loaded to avoid duplicates (e.g., `es5` referenced by `lib.es2015.d.ts` is skipped since `lib.es5.d.ts` is loaded first).

**Future enhancement**: If sub-library files contain reference directives (e.g., `lib.es2015.iterable.d.ts` referencing `lib.es2015.symbol.d.ts`), these could be followed recursively. Currently, the bundle file's reference order is assumed to handle intra-version dependencies correctly.

### Phase 5: Testing

**Location**: `internal/dts_parser/integration_test.go` and `internal/checker/tests/`

**Task 5.1**: Parser integration tests

Add tests that parse each lib file without errors:

```go
func TestParseES2015LibFiles(t *testing.T) {
    libDir := filepath.Join(repoRoot, "node_modules", "typescript", "lib")
    libFiles := []string{
        "lib.es2015.core.d.ts",
        "lib.es2015.collection.d.ts",
        "lib.es2015.generator.d.ts",
        "lib.es2015.iterable.d.ts",
        "lib.es2015.promise.d.ts",
        "lib.es2015.proxy.d.ts",
        "lib.es2015.reflect.d.ts",
        "lib.es2015.symbol.d.ts",
        "lib.es2015.symbol.wellknown.d.ts",
    }
    for _, filename := range libFiles {
        t.Run(filename, func(t *testing.T) {
            path := filepath.Join(libDir, filename)
            source, err := os.ReadFile(path)
            if err != nil {
                t.Errorf("failed to read %s: %v", filename, err)
                return
            }

            parser := NewDtsParser(string(source))
            _, parseErrors := parser.ParseModule()

            // Use t.Errorf to collect all failures across all files
            for _, parseErr := range parseErrors {
                t.Errorf("parse error: %s", parseErr)
            }
        })
    }
}
```

**Task 5.2**: Type inference integration tests

Create test fixtures that verify modern APIs type-check correctly:

```
// fixtures/stdlib_types/es2015_features.esc
val map = new Map<string, number>()
map.set("key", 42)
val value: number | undefined = map.get("key")

val set = new Set([1, 2, 3])
val hasTwo: boolean = set.has(2)

val arr = [1, 2, 3]
val found: number | undefined = arr.find(fn(x) { x > 2 })
```

```
// fixtures/stdlib_types/es2016_features.esc
val arr = [1, 2, 3]
val hasTwo: boolean = arr.includes(2)
```

```
// fixtures/stdlib_types/es2020_features.esc
val big: bigint = BigInt(9007199254740991)
```

**Task 5.3**: Regression tests

Ensure existing tests continue to pass with additional lib files loaded.

### Phase 6: Documentation

**Task 6.1**: Update README or documentation

Document which TypeScript lib features are supported.

**Task 6.2**: Add comments to prelude.go

Document the lib file loading order and any dependencies.

## Implementation Order

The implementation follows an **increment-per-ES-version** approach, with each increment going through all relevant phases before moving to the next ES version.

### Per-Increment Workflow

For each increment (ES2015, then ES2016-ES2017, etc.):

```
┌─────────────────────────────────────────────────────────────┐
│  Increment N: ES20XX                                        │
├─────────────────────────────────────────────────────────────┤
│  1. Phase 1: Add lib files to loading list                  │
│       ↓                                                     │
│  2. Phase 3: Fix parser gaps for these specific files       │
│       ↓                                                     │
│  3. Phase 2: Verify declaration merging                     │
│       ↓                                                     │
│  4. Phase 2.5: Handle Promise augmentation (if applicable)  │
│       ↓                                                     │
│  5. Phase 5: Add tests for this increment                   │
│       ↓                                                     │
│  6. ✅ Increment complete, move to Increment N+1            │
└─────────────────────────────────────────────────────────────┘
```

### Detailed Implementation Order

#### Increment 1: ES2015 (Start Here)

1. **Phase 1** (Core implementation for ES2015)
   - Implement `discoverESLibFiles()` to dynamically find lib files
   - Update `loadGlobalDefinitions()` to use dynamic discovery
   - Test that ES2015 files are being discovered and loaded

2. **Phase 3** (Parser compatibility for ES2015) ⚠️ **Likely largest effort in this increment**
   - Run parser on all 9 ES2015 lib files
   - Identify and fix syntax gaps:
     - `unique symbol` type
     - Well-known symbol signatures (`[Symbol.iterator]()`)
     - Complex generic constraints in iterables
   - For each gap: update dts_parser → interop → ast → type_system → checker as needed

3. **Phase 2** (Declaration merging for ES2015)
   - Verify `Array<T>` merging with ES2015 methods (`find`, `findIndex`, etc.)
   - Verify `String`, `Object`, `Number` extensions merge correctly

4. **Phase 2.5** (Promise<T, E> for ES2015)
   - Verify `Promise.all()`, `Promise.race()` work with default error type
   - Add tests for ES2015 Promise methods

5. **Phase 5** (Testing for ES2015)
   - Add parser integration tests for all 9 files
   - Add type inference tests for Map, Set, Symbol, Promise, iterables
   - Verify no regressions

6. **Checkpoint**: ES2015 complete ✅

#### Increment 2: ES2016-ES2017

1. **Phase 1**: Add 6 lib files to loading list
2. **Phase 3**: Fix any parser gaps (expected: minimal)
3. **Phase 2**: Verify `Array.includes` merges correctly
4. **Phase 5**: Add tests for `includes`, `Object.values`, `padStart`, etc.
5. **Checkpoint**: ES2016-ES2017 complete ✅

#### Increment 3: ES2018-ES2019

1. **Phase 1**: Add 10 lib files to loading list
2. **Phase 3**: Fix async iterator/generator syntax gaps
3. **Phase 2.5**: Ensure `Promise.finally()` preserves error type `E`
4. **Phase 5**: Add tests for `flat`, `flatMap`, `fromEntries`, async iteration
5. **Checkpoint**: ES2018-ES2019 complete ✅

#### Increment 4: ES2020

1. **Phase 1**: Add 8 lib files to loading list
2. **Phase 3**: Handle `BigInt` primitive type, `globalThis`
3. **Phase 2.5**: Verify `Promise.allSettled()` returns `Promise<..., never>`
4. **Phase 5**: Add tests for BigInt, allSettled, matchAll
5. **Checkpoint**: ES2020 complete ✅

#### Increment 5: ES2021

1. **Phase 1**: Add 4 lib files to loading list
2. **Phase 2.5**: Special handling for `Promise.any()` → `AggregateError<Errors>`
3. **Phase 5**: Add tests for `Promise.any`, `WeakRef`, `replaceAll`
4. **Checkpoint**: ES2021 complete ✅

#### Increment 6: ES2022-ES2023

1. **Phase 1**: Add 10 lib files to loading list
2. **Phase 3**: Handle `Error.cause` if needed
3. **Phase 5**: Add tests for `at()`, `hasOwn`, `findLast`, immutable array methods
4. **Checkpoint**: ES2022-ES2023 complete ✅

### Cross-Cutting Phases

These phases span all increments:

- **Phase 4** (Reference directives)
  - Deferred: Explicit loading is sufficient for now
  - Document as future enhancement

- **Phase 6** (Documentation)
  - Update after each increment with newly supported features
  - Final documentation update after all increments complete

## Potential Issues

### Issue 1: Circular Type References
Some lib files may have circular references. The existing `expandedTypes` tracking in `ExpandType` should handle this, but verify with complex types like `Promise`.

### Issue 2: Large Type Graphs
Loading many lib files increases memory usage and type-checking time. Monitor performance with all libs loaded.

### Issue 3: Parser Gaps and Cascade Effects

The dts_parser may not support all TypeScript declaration syntax used in newer lib files. **This is likely the largest source of implementation work.** Each syntax gap may require changes across multiple components:

**Potential syntax gaps:**
- `unique symbol` type
- `infer` keyword in conditional types
- Template literal types (`` `${string}` ``)
- `const` type parameters (`<const T>`)
- `asserts` return types
- Mapped type modifiers (`+readonly`, `-?`)

**Cascade of changes for each syntax:**
1. `dts_parser/` - Parse the syntax
2. `internal/interop/` - Convert to Escalier AST
3. `internal/ast/` - Add new AST nodes (if needed)
4. `internal/type_system/` - Add new type representations (if needed)
5. `internal/checker/` - Handle during type inference

**Mitigation strategies:**
- Start with lib files that use simpler syntax (lib.es2015.collection.d.ts likely simpler than lib.es2020.d.ts)
- Prioritize syntax used by commonly-needed types (Map, Set, Promise)
- **Hard errors on parse failure**: If a lib file fails to parse, compilation must fail with a clear error message. Partial parsing would lead to confusing behavior where some types are available but others are mysteriously missing.

See Phase 3 for detailed implementation approach.

### Issue 4: Namespace Merging
Some lib files extend global namespaces (e.g., `Intl`). Verify namespace declarations merge correctly.

### Issue 5: Promise<T, E> Augmentation Completeness
The existing `PromiseVisitor` handles the basic `Promise` and `PromiseLike` interfaces. Full error type propagation requires:
- Adding `E` type parameter with default to interface declarations
- Transforming static methods (`all`, `race`, `any`, `reject`) to propagate error types
- Transforming instance methods (`then`, `catch`, `finally`) to track error types through chains
- TypeScript's Promise methods use overloads extensively - each overload must be transformed appropriately

### Issue 6: Error Type Inference for Promise Combinators
Promise static methods like `all`, `race`, `allSettled`, and `any` have complex error type semantics:
- `Promise.all` should union error types from all input promises
- `Promise.race` should union error types from all input promises
- `Promise.allSettled` should have error type `never` (always succeeds)
- `Promise.any` should have error type `AggregateError<Errors>` where `Errors` is a tuple of input error types (throws when all reject)

Correctly inferring these requires understanding the semantics of each method.

## Risks and Mitigation Strategies

### Risk 1: Parser Gaps Block Progress (High Likelihood, High Impact)

**Risk**: The dts_parser may not support TypeScript syntax used in ES2015+ lib files, blocking entire increments until parser updates are complete.

**Mitigation strategies**:
1. **Early discovery**: Run parser on all target lib files at the start of each increment to identify gaps before implementation begins
2. **Prioritize by value**: Fix syntax gaps that unblock high-value types first (e.g., `unique symbol` for Symbol support before obscure mapped type modifiers)
3. **Stub unsupported syntax**: For syntax that only affects rarely-used declarations, consider parsing to a placeholder type with a warning (only as last resort - prefer full support)
4. **Track in issues**: Create a GitHub issue for each syntax gap with clear scope, affected lib files, and increment blocked

**Contingency**: If a critical syntax gap proves too complex, consider:
- Shipping the increment without the affected lib file(s)
- Providing hand-written type stubs for the most important types from that file

### Risk 2: Declaration Merging Edge Cases (Medium Likelihood, Medium Impact)

**Risk**: Complex declaration merging scenarios (generics with different constraints, method overloads across files) may not merge correctly.

**Mitigation strategies**:
1. **Comprehensive test suite**: Create tests for each merging scenario before implementation
2. **Incremental verification**: After adding each lib file, verify affected interfaces still work correctly
3. **Align with TypeScript**: When behavior is unclear, match TypeScript's merging semantics

### Risk 3: Performance Degradation (Medium Likelihood, Medium Impact)

**Risk**: Loading 40+ lib files may significantly slow compilation, especially for small projects.

**Mitigation strategies**:
1. **Measure baseline**: Record prelude loading time before changes
2. **Lazy loading**: Consider loading lib files on-demand based on usage (future optimization)
3. **Caching**: Ensure global scope caching works correctly with all lib files
4. **Acceptable threshold**: Define < 2x increase in prelude time as acceptable

### Risk 4: Promise<T, E> Augmentation Breaks Existing Code (Low Likelihood, High Impact)

**Risk**: Changes to Promise handling may break existing async/await code that relies on current behavior.

**Mitigation strategies**:
1. **Existing test coverage**: Run all `async_test.go` tests after each Promise-related change
2. **Default to current behavior**: Ensure `Promise<T>` (single param) continues to work as `Promise<T, never>`
3. **Incremental rollout**: Add Promise augmentation for one lib file at a time, verifying tests between each

### Risk 5: TypeScript Version Incompatibility (Low Likelihood, Medium Impact)

**Risk**: Different TypeScript versions may have different lib file contents or syntax, causing failures for some users.

**Mitigation strategies**:
1. **Document supported versions**: Specify minimum TypeScript version (e.g., 5.0+)
2. **CI testing**: Test against multiple TypeScript versions in CI
3. **Graceful degradation**: If a newer lib file uses unsupported syntax, skip it with a warning rather than failing entirely (only for optional/newer ES versions)

### Risk 6: Scope Creep (Medium Likelihood, Low Impact)

**Risk**: Temptation to add features beyond the defined scope (e.g., custom lib paths, ESNext support) delays core implementation.

**Mitigation strategies**:
1. **Strict increment boundaries**: Complete each increment fully before considering extensions
2. **Document future work**: Add ideas to "Out of Scope" section rather than implementing immediately
3. **Review checkpoints**: At each increment completion, assess whether to proceed or ship

## Success Criteria

### Overall Criteria

- [ ] **[BLOCKER]** No regressions in existing test suite at any point
- [ ] **[BLOCKER]** Performance impact is acceptable (< 2x increase in prelude time with all libs loaded)
- [ ] **[BLOCKER]** Each increment is fully tested before proceeding to the next

### Per-Increment Success Criteria

#### Increment 1: ES2015 ✓ (Required for subsequent increments)

- [ ] **[BLOCKER]** All 9 ES2015 lib files parse without errors
- [ ] **[BLOCKER]** `Map<K, V>`, `Set<T>`, `WeakMap<K, V>`, `WeakSet<T>` types available and working
- [ ] **[BLOCKER]** `Symbol` type available, including well-known symbols
- [ ] **[BLOCKER]** `Iterable<T>`, `Iterator<T>`, `IterableIterator<T>` available
- [ ] **[NICE-TO-HAVE]** `Generator<T>`, `GeneratorFunction` types working
- [ ] **[NICE-TO-HAVE]** `Proxy`, `ProxyHandler<T>`, `Reflect` available
- [ ] **[BLOCKER]** Array ES2015 methods: `find`, `findIndex`, `fill`, `copyWithin`, `entries`, `keys`, `values`
- [ ] **[BLOCKER]** `Promise.all()`, `Promise.race()` type-check correctly
- [ ] **[BLOCKER]** Declaration merging works for extended interfaces

#### Increment 2: ES2016-ES2017

- [ ] **[BLOCKER]** All 6 ES2016-ES2017 lib files parse without errors
- [ ] **[BLOCKER]** `Array.prototype.includes()` available
- [ ] **[BLOCKER]** `Object.values()`, `Object.entries()`, `Object.getOwnPropertyDescriptors()` available
- [ ] **[BLOCKER]** `String.prototype.padStart()`, `String.prototype.padEnd()` available
- [ ] **[NICE-TO-HAVE]** `SharedArrayBuffer`, `Atomics` types available

#### Increment 3: ES2018-ES2019

- [ ] **[BLOCKER]** All 10 ES2018-ES2019 lib files parse without errors
- [ ] **[BLOCKER]** `AsyncGenerator<T>`, `AsyncIterable<T>`, `AsyncIterator<T>` available
- [ ] **[BLOCKER]** `Promise.prototype.finally()` preserves error type `E`
- [ ] **[BLOCKER]** `Array.prototype.flat()`, `Array.prototype.flatMap()` available
- [ ] **[BLOCKER]** `Object.fromEntries()` available
- [ ] **[NICE-TO-HAVE]** RegExp named capture groups typed correctly

#### Increment 4: ES2020

- [ ] **[BLOCKER]** All 8 ES2020 lib files parse without errors
- [ ] **[BLOCKER]** `BigInt` primitive type working
- [ ] **[NICE-TO-HAVE]** `BigInt64Array`, `BigUint64Array` available
- [ ] **[BLOCKER]** `Promise.allSettled()` preserves tuple structure with error type `never`
- [ ] **[BLOCKER]** `globalThis` accessible
- [ ] **[NICE-TO-HAVE]** `String.prototype.matchAll()` available

#### Increment 5: ES2021

- [ ] **[BLOCKER]** All 4 ES2021 lib files parse without errors
- [ ] **[BLOCKER]** `Promise.any()` returns `Promise<T, AggregateError<Errors>>` with tuple-typed errors
- [ ] **[BLOCKER]** `AggregateError<Errors>` type available
- [ ] **[NICE-TO-HAVE]** `WeakRef<T>`, `FinalizationRegistry<T>` available
- [ ] **[BLOCKER]** `String.prototype.replaceAll()` available

#### Increment 6: ES2022-ES2023

- [ ] **[BLOCKER]** All 10 ES2022-ES2023 lib files parse without errors
- [ ] **[BLOCKER]** `Array.prototype.at()`, `String.prototype.at()` available
- [ ] **[BLOCKER]** `Object.hasOwn()` available
- [ ] **[NICE-TO-HAVE]** `Error.cause` property typed correctly
- [ ] **[BLOCKER]** `Array.prototype.findLast()`, `Array.prototype.findLastIndex()` available
- [ ] **[NICE-TO-HAVE]** Immutable array methods: `toReversed()`, `toSorted()`, `toSpliced()`, `with()`

### Promise<T, E> Specific Criteria (Across All Increments)

- [ ] **[BLOCKER]** `Promise<T, E>` augmentation works for all lib files (not just lib.es5.d.ts)
- [ ] **[BLOCKER]** `Promise.all()` preserves tuple structure: `Promise.all([Promise<A, E1>, Promise<B, E2>])` returns `Promise<[A, B], E1 | E2>`
- [ ] **[BLOCKER]** `Promise.race()` unions value and error types: `Promise.race([Promise<A, E1>, Promise<B, E2>])` returns `Promise<A | B, E1 | E2>`
- [ ] **[BLOCKER]** `Promise.allSettled()` preserves tuple structure with typed errors: `Promise.allSettled([Promise<A, E1>, Promise<B, E2>])` returns `Promise<[PromiseSettledResult<A, E1>, PromiseSettledResult<B, E2>], never>` (ES2020)
- [ ] **[BLOCKER]** `Promise.any()` returns `Promise<T, AggregateError<Errors>>` with tuple-typed errors (ES2021)
- [ ] **[BLOCKER]** `Promise.resolve()` returns `Promise<T, never>`
- [ ] **[BLOCKER]** `Promise.reject()` returns `Promise<never, E>` where E is inferred from argument
- [ ] **[NICE-TO-HAVE]** `promise.then()` correctly transforms both value and error types
- [ ] **[NICE-TO-HAVE]** `promise.catch()` correctly handles error type transformation
- [ ] **[BLOCKER]** `promise.finally()` preserves both value and error types (ES2018)
- [ ] **[BLOCKER]** Async/await error type inference continues to work with new lib files
- [ ] **[BLOCKER]** Existing Promise<T, E> tests continue to pass

## Notes

- **Parser updates are expected**: ES2015+ lib files likely use TypeScript syntax not yet supported by dts_parser. Budget time for parser work, which may cascade into AST and type checker changes.
- **Incremental adoption is critical**: Start with ES2015 (Increment 1) as it introduces foundational types (iterators, generators, symbols) that later versions depend on. Do not proceed to Increment 2 until Increment 1 is fully working.
- The TypeScript lib file organization uses both bundle files (lib.es2015.d.ts) and sub-libraries (lib.es2015.collection.d.ts)
- Loading the bundle files includes reference directives to sub-libraries, but loading sub-libraries directly avoids parsing references
- Consider adding a configuration option in the future to select target ES version (similar to TypeScript's `lib` compiler option)
- **Stopping points**: Each increment is a valid stopping point. If time/resources are limited, completing Increment 1 (ES2015) provides the highest value. Increments 2-6 can be deferred without breaking functionality.

### Promise<T, E> Implementation Notes

- The `PromiseVisitor` in `internal/interop/decl.go` currently handles `Promise` and `PromiseLike` interface augmentation
- **Default type parameter**: `Promise<T, E = never>` uses a default so that `Promise<T>` references resolve to `Promise<T, never>`
- **Full error propagation**: Static and instance methods must be transformed to properly track error types:
  - `Promise.all/race`: Extract and union error types from input promise array
  - `Promise.any`: Returns `Promise<T, AggregateError<Errors>>` where `Errors` is tuple of input error types
  - `Promise.reject`: Captures argument type as error type
  - `then/catch`: Compute new error type based on handler presence and return types
  - `finally`: Preserves original error type
- ES2015+ lib files add methods to `PromiseConstructor` interface (static methods like `all`, `race`, etc.)
- The augmentation must happen during AST conversion (interop layer), before type inference
- TypeScript's Promise methods use overloads extensively - the `PromiseVisitor` must transform each overload appropriately
- The `Awaited<T>` helper type (ES2020+) extracts the resolved type - we need `AwaitedError<T>` or similar for error types
- Test files: `internal/checker/tests/async_test.go` contains existing Promise<T, E> tests
