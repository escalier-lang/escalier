# 03 Imports and Package System

This document describes Escalier's import system, package namespaces, and how to work with external TypeScript packages.

## Overview

Escalier uses a namespace-based import system where:

- **Global types** (from TypeScript's lib.es5.d.ts, lib.dom.d.ts) are available without imports
- **Package symbols** require imports and qualified access (e.g., `lodash.map`)
- **Local declarations** can shadow globals
- **Imports are file-scoped** - each file must import the packages it uses

## Import Syntax

### Namespace Imports

Import an entire package as a namespace:

```escalier
import * as lodash from "lodash"
import * as fp from "lodash/fp"

// Use qualified access
val result = lodash.map([1, 2, 3], fn(x) { x * 2 })
val piped = fp.pipe(fn1, fn2, fn3)
```

### Named Imports

Import specific symbols from a package:

```escalier
import { map, filter } from "lodash"
import { useState, useEffect } from "react"

// Use directly (but still file-scoped)
val doubled = map([1, 2, 3], fn(x) { x * 2 })
```

### Named Imports with Aliases

Rename imports to avoid conflicts or for convenience:

```escalier
import { map as lodashMap } from "lodash"
import { map as ramdaMap } from "ramda"

// Both are available with different names
val arr1 = lodashMap([1, 2], fn(x) { x * 2 })
val arr2 = ramdaMap(fn(x) { x + 1 }, [1, 2])
```

## File-Scoped Imports

Imports in Escalier are **file-scoped**, similar to Go. Each file must contain its own import statements for the packages it uses.

```escalier
// lib/utils.esc
import * as lodash from "lodash"

fn helper() -> number {
    lodash.sum([1, 2, 3])  // OK: lodash imported in this file
}
```

```escalier
// lib/main.esc
// No import statement for lodash

fn main() -> number {
    lodash.sum([1, 2, 3])  // ERROR: 'lodash' is not defined
}
```

Even if both files are in the same module, imports do not leak across files. If `main.esc` needs lodash, it must import it explicitly:

```escalier
// lib/main.esc
import * as lodash from "lodash"

fn main() -> number {
    lodash.sum([1, 2, 3])  // OK now
}
```

### Why File-Scoped Imports?

1. **Explicit dependencies**: Each file clearly shows what external packages it depends on
2. **Easier refactoring**: Moving a file to a different location doesn't break imports
3. **Better tooling**: IDEs can easily determine what's available in each file
4. **Familiar pattern**: Developers from Go, Python, and JavaScript will find this natural

## Module-Level Declarations

While imports are file-scoped, **type and function declarations** are shared across files within a module:

```escalier
// lib/types.esc
type UserId = string
type User = { id: UserId, name: string }
```

```escalier
// lib/users.esc
// No need to import User - it's in the same module

fn createUser(id: UserId, name: string) -> User {
    { id, name }
}
```

## Global Types and Shadowing

### Accessing Global Types

Global types from TypeScript's standard library (Array, Promise, Map, Set, etc.) are available without imports:

```escalier
declare val arr: Array<number>
declare val promise: Promise<string>
declare val map: Map<string, number>
```

### Shadowing Globals

Local type declarations can shadow global types:

```escalier
// Define a custom Array type that shadows the global
type Array<T> = {
    items: T,
    length: number,
    isEmpty: boolean,
}

// Uses our custom Array type
declare val myArray: Array<string>
```

### Accessing Shadowed Globals with globalThis

When you shadow a global type, you can still access the original via `globalThis`:

```escalier
// Shadow the global Array
type Array<T> = { items: T, isCustom: boolean }

// Use our custom Array
declare val customArr: Array<number>

// Access the global Array via globalThis
declare val globalArr: globalThis.Array<number>
```

This works for both types and values:

```escalier
// Access global Symbol
val iterator = globalThis.Symbol.iterator

// Access global Array constructor
val arr = globalThis.Array.from([1, 2, 3])
```

## Package Registry

Escalier maintains a package registry that caches loaded packages. When you import a package:

1. The registry checks if the package is already loaded
2. If not, it loads and parses the `.d.ts` file
3. The package namespace is stored in the registry
4. Future imports reuse the cached namespace

This ensures:
- Consistent type identity across files
- Efficient memory usage
- Fast subsequent imports

## Subpath Imports

Packages can have subpath exports that provide different functionality:

```escalier
import * as lodash from "lodash"       // Main package
import * as fp from "lodash/fp"        // Functional programming variant

// These are separate namespaces with different exports
val arr1 = lodash.map([1, 2], fn(x) { x * 2 })  // (array, iteratee)
val arr2 = fp.map(fn(x) { x * 2 })              // curried: (iteratee) -> (array)
```

## Cross-File Cyclic Dependencies

Escalier handles cyclic type dependencies across files correctly:

```escalier
// lib/node.esc
type Node<T> = {
    value: T,
    children: Tree<T>,  // References Tree from tree.esc
}
```

```escalier
// lib/tree.esc
type Tree<T> = {
    root: Node<T>,      // References Node from node.esc
    size: number,
}
```

Both types can reference each other because:
1. All declarations in a module are collected before type checking
2. A unified dependency graph handles cyclic references
3. Placeholder types are created for forward references

Note: Circular **package** dependencies (Package A imports B, which imports A) are not supported.

## Migration Guide

If you're migrating from an older version of Escalier or from TypeScript:

### Breaking Change: Qualified Package Access

**Before** (hypothetical old behavior):
```escalier
import "lodash"
val result = map([1, 2, 3], fn(x) { x * 2 })  // Direct access
```

**After**:
```escalier
import * as lodash from "lodash"
val result = lodash.map([1, 2, 3], fn(x) { x * 2 })  // Qualified access
```

### Re-exporting Package Symbols

If you want shorter access, create module-level re-exports:

```escalier
// lib/utils.esc
import * as lodash from "lodash"

// Re-export for convenience
export val map = lodash.map
export val filter = lodash.filter
```

```escalier
// lib/main.esc
// Now you can use the re-exported symbols
val doubled = map([1, 2, 3], fn(x) { x * 2 })
```

### Handling Shadowing Conflicts

If you have local types that conflict with globals:

```escalier
// Your local Promise type
type Promise<T> = { value: T, status: string }

// Use globalThis for the actual Promise
fn fetchData() -> globalThis.Promise<Data> {
    // ...
}

// Use your local Promise type
fn createPromise<T>(value: T) -> Promise<T> {
    { value, status: "pending" }
}
```

## Best Practices

1. **Use namespace imports** for packages with many exports you'll use:
   ```escalier
   import * as lodash from "lodash"
   ```

2. **Use named imports** for packages where you only need a few symbols:
   ```escalier
   import { useState, useEffect } from "react"
   ```

3. **Import in every file** that needs the package - don't rely on other files' imports

4. **Avoid shadowing globals** unless you have a good reason - it can cause confusion

5. **Use globalThis explicitly** when you need to access a shadowed global

6. **Create re-exports** in a utility file if you want short access to package functions

## Summary

| Feature | Behavior |
|---------|----------|
| Import scope | File-scoped (each file imports what it needs) |
| Declaration scope | Module-scoped (shared across files in same directory) |
| Global types | Available without import (Array, Promise, etc.) |
| Package symbols | Require import + qualified access |
| Shadowing | Local declarations can shadow globals |
| Shadowed global access | Use `globalThis.TypeName` |
| Cyclic types | Supported across files |
| Cyclic packages | Not supported |
