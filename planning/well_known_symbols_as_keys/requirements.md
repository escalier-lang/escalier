# Well-Known Symbols as Keys

## Overview

This document outlines the requirements for supporting JavaScript/TypeScript's well-known symbols as computed property keys in the Escalier compiler. Well-known symbols (e.g., `Symbol.iterator`, `Symbol.toStringTag`, `Symbol.toPrimitive`) are extensively used in TypeScript's `lib.es2015.*.d.ts` files to define standard behaviors for built-in types.

## Motivation

TypeScript's ES2015+ standard library files make heavy use of well-known symbols as computed property keys:

```typescript
// lib.es2015.iterable.d.ts
interface Iterable<T> {
    [Symbol.iterator](): Iterator<T>;
}

interface Array<T> {
    [Symbol.iterator](): ArrayIterator<T>;
}

// lib.es2015.symbol.wellknown.d.ts
interface Symbol {
    [Symbol.toPrimitive](hint: string): symbol;
    readonly [Symbol.toStringTag]: string;
}

interface Date {
    [Symbol.toPrimitive](hint: "default"): string;
    [Symbol.toPrimitive](hint: "string"): string;
    [Symbol.toPrimitive](hint: "number"): number;
}

interface RegExp {
    [Symbol.match](string: string): RegExpMatchArray | null;
    [Symbol.replace](string: string, replaceValue: string): string;
    [Symbol.search](string: string): number;
    [Symbol.split](string: string, limit?: number): string[];
}
```

Without proper support for these computed property keys, the compiler cannot:
1. Parse `lib.es2015.iterable.d.ts` and related files
2. Make `for...of` iteration work with typed iterables
3. Support custom iterators and generators
4. Correctly type string/array methods that use RegExp symbol methods

## Requirements

### Functional Requirements

#### FR1: Support Configurable Target Versions

The compiler must support configurable target versions (es5, es2015, es2016, es2017, etc.) and load the appropriate standard library files.

**Loading process for a target version:**

1. Read the lib file for the target version (e.g., `lib.es2016.d.ts`)
2. Parse the file to collect:
   - All declarations in the file
   - All `/// <reference lib="..." />` directives (file references)
3. Recursively follow each file reference, repeating steps 1-2
4. After all references are resolved, merge declarations from all processed files into a single module

**Example: Target `es2016`**

```
lib.es2016.d.ts
├── /// <reference lib="es2015" />  → lib.es2015.d.ts
│   ├── /// <reference lib="es2015.core" />  → lib.es2015.core.d.ts
│   ├── /// <reference lib="es2015.collection" />  → lib.es2015.collection.d.ts
│   ├── /// <reference lib="es2015.generator" />  → lib.es2015.generator.d.ts
│   ├── /// <reference lib="es2015.iterable" />  → lib.es2015.iterable.d.ts
│   ├── /// <reference lib="es2015.promise" />  → lib.es2015.promise.d.ts
│   ├── /// <reference lib="es2015.proxy" />  → lib.es2015.proxy.d.ts
│   ├── /// <reference lib="es2015.reflect" />  → lib.es2015.reflect.d.ts
│   ├── /// <reference lib="es2015.symbol" />  → lib.es2015.symbol.d.ts
│   └── /// <reference lib="es2015.symbol.wellknown" />  → lib.es2015.symbol.wellknown.d.ts
└── /// <reference lib="es2016.array.include" />  → lib.es2016.array.include.d.ts
```

**Requirements:**
- Each file should only be processed once (track visited files to avoid duplicates)
- File references use the format `/// <reference lib="name" />` which maps to `lib.{name}.d.ts`
- All declarations from all visited files are merged into a single global module

**Key parsing challenges:**
- `[Symbol.iterator]` and other computed property keys using member expressions
- `unique symbol` type declarations
- Complex generic constraints in iterable/iterator interfaces

#### FR2: Merge Declarations from All `lib.es2015.*.d.ts` Files

Multiple lib files declare extensions to the same interfaces. These must be correctly merged into a single unified type:

```typescript
// lib.es5.d.ts
interface Array<T> {
    indexOf(searchElement: T): number;
    map<U>(callbackfn: (value: T) => U): U[];
}

// lib.es2015.core.d.ts
interface Array<T> {
    find(predicate: (value: T) => boolean): T | undefined;
    findIndex(predicate: (value: T) => boolean): number;
}

// lib.es2015.iterable.d.ts
interface Array<T> {
    [Symbol.iterator](): ArrayIterator<T>;
    entries(): ArrayIterator<[number, T]>;
    keys(): ArrayIterator<number>;
    values(): ArrayIterator<T>;
}

// lib.es2015.symbol.wellknown.d.ts
interface Array<T> {
    readonly [Symbol.unscopables]: { [K in keyof any[]]?: boolean };
}

// Result: Array<T> must have ALL methods from all declarations
```

**Merging requirements:**
- All properties and methods from all declarations must be present
- Type parameters must be validated for consistency across declarations
- Method overloads from different files should be collected
- The merged interface must be available before any code that references it

#### FR3: Infer All Interfaces That Should Be Merged Before Inferring Dependents

When multiple interface declarations with the same name exist across different files, they must all be inferred and merged before any dependent declarations are processed.

**Problem scenario:**
```
// lib.es2015.iterable.d.ts
interface SymbolConstructor {
    /**
     * A method that returns the default iterator for an object. Called by the semantics of the
     * for-of statement.
     */
    readonly iterator: unique symbol;
}

interface Iterable<T, TReturn = any, TNext = any> {
    [Symbol.iterator](): Iterator<T, TReturn, TNext>;
}

// lib.es2015.symbol.d.ts
interface SymbolConstructor {
    /* instance methods */
}

declare var Symbol: SymbolConstructor;

// lib.es2015.symbol.wellknown.d.ts
interface SymbolConstructor {
    readonly hasInstance: unique symbol;
    readonly isConcatSpreadable: unique symbol;
    readonly match: unique symbol;
    readonly replace: unique symbol;
    readonly search: unique symbol;
    readonly species: unique symbol;
    readonly split: unique symbol;
    readonly toPrimitive: unique symbol;
    readonly toStringTag: unique symbol;
    readonly unscopables: unique symbol;
}

interface Date {
    [Symbol.toPrimitive](hint: "default"): string;
    [Symbol.toPrimitive](hint: "string"): string;
    [Symbol.toPrimitive](hint: "number"): number;
    [Symbol.toPrimitive](hint: string): string | number;
}
```

**Dependency analysis:** The declarations form a DAG (no cycles):
```
SymbolConstructor (3 interface declarations) ──┐
                                               │
                                               ▼
                                            Symbol (var)
                                               │
                          ┌────────────────────┼────────────────────┐
                          ▼                    ▼                    ▼
                      Iterable               Date              (other uses)
```

**The challenge:** When inferring `Iterable<T>`, we encounter `[Symbol.iterator]` as a computed property key. To resolve this:
1. We need the binding `Symbol` to exist (from `declare var Symbol: SymbolConstructor`)
2. We need `SymbolConstructor` to have the `iterator` property (from one of its interface declarations)
3. But `SymbolConstructor` is declared across multiple files and must be fully merged first

Similarly, `Date[Symbol.toPrimitive]` requires `Symbol.toPrimitive` to be available, which is declared in `lib.es2015.symbol.wellknown.d.ts` - a different file than where `Symbol.iterator` is declared.

**Required behavior:**
1. All interface declarations with the same name must be collected and merged into a single type before any dependent type is inferred
2. Variable declarations (like `declare var Symbol`) must be processed after the interfaces they reference are merged
3. Declarations with computed keys (like `Iterable` with `[Symbol.iterator]`) must be processed after the variables they reference are available
4. Processing order must follow the topological order of the dependency graph

**Implementation approach:**
1. Build a dependency graph that recognizes computed keys as dependencies on variable bindings
2. Identify all interface declarations that share the same name
3. Treat all same-named interface declarations as a single node in the dependency graph (they must be processed together)
4. Process declarations in topological order:
   - First: Merge all `SymbolConstructor` interface declarations
   - Then: Process `var Symbol: SymbolConstructor`
   - Finally: Process `Iterable`, `Date`, etc. (which use `Symbol.iterator`, `Symbol.toPrimitive`)

#### FR4: Convert Computed Property Keys in Interop Layer

The interop layer (`internal/interop/`) must convert computed property keys from the dts_parser AST to Escalier AST.

**Current state:** `convertPropertyKey` in `helper.go` returns an error for `ComputedKey`:
```go
case *dts_parser.ComputedKey:
    // In dts_parser, ComputedKey.Expr is a TypeAnn
    // In ast, ComputedKey.Expr is an Expr
    // TODO: implement conversion for computed keys
    return nil, fmt.Errorf("convertPropertyKey: ComputedKey not yet implemented")
```

**Required conversion:**
```typescript
// Input (dts_parser AST)
[Symbol.iterator](): Iterator<T>
//  ^^^^^^^^^^^^^^^ ComputedKey with TypeAnn = MemberTypeAnn(Symbol, iterator)

// Output (Escalier AST)
[Symbol.iterator](): Iterator<T>
//  ^^^^^^^^^^^^^^^ ComputedKey with Expr = Member(Ident("Symbol"), Ident("iterator"))
```

**Conversion rules:**
1. `ComputedKey.Expr` (a `TypeAnn` in dts_parser) must be converted to an `ast.Expr`
2. For `MemberTypeAnn` (e.g., `Symbol.iterator`), convert to `ast.Member` expression
3. For `IdentTypeAnn` (e.g., `foo`), convert to `ast.Ident` expression
4. For `TypeofTypeAnn` (e.g., `typeof bar`): **Deferred** - not required for ES2015+ lib file support

**Note on TypeofTypeAnn:** The `typeof` pattern in computed keys (e.g., `[typeof someVar]`) is not used in TypeScript's standard library files. Support for this pattern is out-of-scope for the initial implementation. If encountered, the interop layer should return an error with a clear message indicating the pattern is unsupported. This can be added incrementally if needed for user-authored `.d.ts` files.

#### FR5: Support `unique symbol` Type

The `lib.es2015.symbol.d.ts` file declares well-known symbols as `unique symbol`:

```typescript
interface SymbolConstructor {
    readonly iterator: unique symbol;
    readonly hasInstance: unique symbol;
    readonly isConcatSpreadable: unique symbol;
    readonly match: unique symbol;
    readonly replace: unique symbol;
    readonly search: unique symbol;
    readonly species: unique symbol;
    readonly split: unique symbol;
    readonly toPrimitive: unique symbol;
    readonly toStringTag: unique symbol;
    readonly unscopables: unique symbol;
}
```

**Requirements:**
1. The dts_parser must parse `unique symbol` type syntax
2. The type system must represent unique symbols as nominally distinct types
3. Each `unique symbol` declaration creates a distinct type (two `unique symbol` properties are not assignable to each other)
4. `unique symbol` is a subtype of `symbol`

#### FR6: Represent Symbol Keys in the Type System

When an interface has a computed property key that is a well-known symbol, the type system must:

1. **Track the symbol identity:** Know which specific symbol (e.g., `Symbol.iterator`) is used as the key
2. **Support lookup by symbol:** When code accesses `obj[Symbol.iterator]`, the type checker must find the corresponding property
3. **Preserve the key in type printing:** When displaying the type, show `[Symbol.iterator]` not just a generic index signature

**Current support:** `ObjectType` already has a `SymbolKeyMap` field:
```go
// type_system/types.go
type ObjectType struct {
    // ...
    // Maps symbols used as keys to the ast.Expr that was used as the computed key.
    SymbolKeyMap map[int]any
}
```

This needs to be populated during type inference for interfaces with computed symbol keys.

#### FR7: Support Accessing Properties via Symbol Keys

Code that accesses properties using well-known symbols must type-check correctly:

```javascript
// Accessing iterator method
val arr = [1, 2, 3]
val iter = arr[Symbol.iterator]()  // Should be ArrayIterator<number>

// Custom iterable
val obj: Iterable<string> = {
    [Symbol.iterator]() {
        return { next: () => ({ done: true, value: undefined }) }
    }
}
```

### Non-Functional Requirements

#### NFR1: Parser Error Clarity

When the dts_parser encounters a computed key it cannot handle, the error message should:
- Identify the specific syntax that failed
- Indicate the file and line number
- Suggest what type of computed key was expected

#### NFR2: Type Display

When printing types with symbol keys, use readable notation:
```
// Good
{ [Symbol.iterator](): Iterator<T> }

// Not good
{ [<unique symbol #5>](): Iterator<T> }
```

#### NFR3: Incremental Adoption

Support for well-known symbols should be additive:
- Existing code that doesn't use symbols should continue to work
- Partial support (parsing but limited type checking) is acceptable as an intermediate step

## Scope

### In Scope

- Parsing computed property keys in .d.ts files
- Converting computed keys from dts_parser AST to Escalier AST
- `unique symbol` type parsing and representation
- Merging interface declarations with symbol-keyed properties
- Type checking access to symbol-keyed properties
- Well-known symbols defined in `lib.es2015.symbol.d.ts` and `lib.es2015.symbol.wellknown.d.ts`

### Out of Scope (Future Work)

- User-defined symbols as property keys (e.g., `const mySymbol = Symbol(); obj[mySymbol]`)
- `Symbol.for()` / `Symbol.keyFor()` runtime semantics
- Full iterator protocol type checking (e.g., validating that `next()` returns `IteratorResult`)
- Spread/rest with iterables (`[...iterable]`)
- `for...of` loop type inference

## Success Criteria

### Parser Criteria

1. [ ] `lib.es2015.symbol.d.ts` parses without errors
2. [ ] `lib.es2015.symbol.wellknown.d.ts` parses without errors
3. [ ] `lib.es2015.iterable.d.ts` parses without errors
4. [ ] All 9 ES2015 lib files parse without errors

### Interop Criteria

5. [ ] Computed keys with `Symbol.X` expressions convert correctly
6. [ ] `unique symbol` type converts to a distinct type representation
7. [ ] Interface declarations with symbol keys produce correct Escalier AST

### Type System Criteria

8. [ ] `ObjectType.SymbolKeyMap` is populated for interfaces with symbol keys
9. [ ] Multiple interface declarations with the same name merge correctly (including symbol-keyed properties)
10. [ ] `unique symbol` properties on `SymbolConstructor` have distinct types

### Type Checking Criteria

11. [ ] `arr[Symbol.iterator]()` type-checks correctly for arrays
12. [ ] `Iterable<T>` interface is available with `[Symbol.iterator]` method
13. [ ] Declaration merging across lib files produces complete interfaces

## Implementation Notes

### dts_parser Changes

The parser already has `ComputedKey` in the AST (`dts_parser/ast.go:457`):
```go
type ComputedKey struct {
    Expr TypeAnn // in .d.ts, computed keys use type expressions
    span ast.Span
}
```

Ensure the parser correctly handles:
- `[Symbol.iterator]` - member expression (required for ES2015+ lib files)
- `[foo]` - identifier (required for ES2015+ lib files)
- `[typeof bar]` - typeof expression (parsing supported, but interop conversion deferred - see FR4)

### Interop Layer Changes

`internal/interop/helper.go` needs to implement the `ComputedKey` case in `convertPropertyKey`:

```go
case *dts_parser.ComputedKey:
    expr, err := convertTypeAnnToExpr(k.Expr)
    if err != nil {
        return nil, fmt.Errorf("converting computed key: %w", err)
    }
    return ast.NewComputedKey(expr, k.Span()), nil
```

A new helper `convertTypeAnnToExpr` is needed to convert dts_parser type annotations used in computed key contexts to Escalier expressions.

### Type Inference Changes

When inferring an interface with a computed symbol key:
1. Evaluate the key expression to determine which symbol it references
2. Create a `Property` element with the symbol as the key
3. Add an entry to `ObjectType.SymbolKeyMap` mapping the symbol ID to the original expression

### Declaration Merging Changes

The SCC processing in `internal/checker/infer_module.go` needs to be updated to:
1. Collect all interface declarations with the same name in the SCC
2. Process them as a group before other declarations
3. Merge the resulting types

## Expected Usage

After implementation, the following should work:

```javascript
// Iterating over arrays (uses Symbol.iterator)
val arr = [1, 2, 3]
for val x in arr {
    // x: number
}

// Getting an iterator explicitly
val iter = arr[Symbol.iterator]()
val first = iter.next()  // { done: boolean, value: number }

// Custom iterables
val range: Iterable<number> = {
    [Symbol.iterator]() {
        var i = 0
        return {
            next() {
                return if i < 10 {
                    { done: false, value: i++ }
                } else {
                    { done: true, value: undefined }
                }
            }
        }
    }
}

// RegExp with symbol methods
val re = /foo/g
val result = re[Symbol.match]("foobar")  // RegExpMatchArray | null

// Date with toPrimitive
val d = new Date()
val n = d[Symbol.toPrimitive]("number")  // number
val s = d[Symbol.toPrimitive]("string")  // string
```

## Dependencies

This feature depends on:
1. **stdlib_types requirements** - The lib file loading infrastructure being implemented
2. **Declaration merging** - Already implemented, but may need adjustments for symbol keys
3. **Computed property support in Escalier** - Already partially supported (see `[Symbol.customMatcher]` usage in fixtures)

## Risks

### Risk 1: Complex Type Expressions in Computed Keys

Some computed keys may use complex type expressions:
```typescript
[K in keyof T]?: boolean  // Mapped types
```

**Mitigation:** Focus on well-known symbol patterns first (`Symbol.X`). Document limitations for complex expressions.

### Risk 2: Symbol Identity Across Declarations

Well-known symbols must have consistent identity across all declarations that use them (e.g., `Symbol.iterator` in `Iterable` must refer to the same symbol as `Symbol.iterator` in `Array`).

**Mitigation:** All TypeScript stdlib `.d.ts` files are merged into a single module with a unified dependency graph. The dependency graph ensures that `SymbolConstructor` interface declarations are merged and the `Symbol` variable is available before any declarations that reference well-known symbols are processed (see FR3). No special loading order is required beyond what the dependency graph naturally provides.

### Risk 3: Performance Impact of Symbol Tracking

Tracking symbol keys in `SymbolKeyMap` adds overhead.

**Mitigation:** Only populate `SymbolKeyMap` when actual symbol keys are present. Most objects don't have symbol keys.
