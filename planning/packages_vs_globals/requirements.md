# Requirements: Separate Package and Global Namespaces

## Problem Statement

Currently, `InferModule` does not differentiate between:
1. **Global type definitions** - modules converted from `.d.ts` files that ship with TypeScript (e.g., `lib.es5.d.ts`, `lib.dom.d.ts`)
2. **Package type definitions** - modules defined by `.esc` files in a `libs/` folder or converted from `.d.ts` files from npm packages

All type definitions are merged into a single namespace during inference, which prevents package declarations from shadowing global declarations.

## Current Behavior

### Type Definition Sources

1. **Prelude/Globals** (`internal/checker/prelude.go`):
   - Loads `lib.es5.d.ts` and `lib.dom.d.ts` from TypeScript
   - All declarations are inferred into a shared scope
   - No distinction made between ES5 builtins and DOM types

2. **Package Imports** (`internal/checker/infer_import.go`):
   - Resolves package path via `package.json` "types" field
   - Loads `.d.ts` type definitions
   - Infers into a new scope, then merges into current namespace

3. **User Modules**:
   - Parsed from `.esc` files
   - Inferred into the same scope as globals and packages

### Current Merging Behavior

```
User Code Scope
    └── Namespace
        ├── Array (from lib.es5.d.ts)
        ├── Promise (from lib.es5.d.ts)
        ├── Element (from lib.dom.d.ts)
        ├── fetch (from package import)
        └── MyType (user-defined)
```

All bindings coexist in a flat namespace with no shadowing capability.

## .d.ts File Classification Rules

A `.d.ts` file's declarations are classified as either **package** or **global** based on the file's structure:

### Rule 1: Top-level Exports → Package

If a `.d.ts` file contains any top-level `export` statements, the entire file defines a **package**. The package is identified by the **path** to the closest `package.json` file found by traversing up the file's directory path (not by the package name, since names are not unique).

```typescript
// node_modules/my-client/dist/index.d.ts
// Resolves to: node_modules/my-client/package.json
// Package identity: "node_modules/my-client/package.json"

export interface Config { ... }
export function initialize(): void;
export default class Client { ... }
```

All exported declarations belong to the package namespace identified by the `package.json` path.

### Rule 2: Named Module Declarations → Separate Packages

`declare module "name"` blocks each define a **separate package**, regardless of other content in the file:

```typescript
// multi-module.d.ts
declare module "lodash" {
    // This is package "lodash"
    export function map<T, U>(arr: T[], fn: (x: T) => U): U[];
}

declare module "lodash/fp" {
    // This is a separate package "lodash/fp"
    export function map<T, U>(fn: (x: T) => U): (arr: T[]) => U[];
}
```

Each named module declaration creates its own isolated package namespace.

### Rule 3: No Top-level Exports → Globals

If a `.d.ts` file has **no top-level exports**, declarations outside of named module blocks are **global**:

```typescript
// lib.es5.d.ts - No exports, so these are GLOBAL
interface Array<T> {
    length: number;
    push(...items: T[]): number;
}

interface Promise<T> {
    then<U>(onFulfilled: (value: T) => U): Promise<U>;
}

declare function setTimeout(callback: () => void, ms: number): number;

// Named modules in the same file are still packages
declare module "timers" {
    // This is package "timers", not global
    export function setTimeout(callback: () => void, ms: number): Timeout;
}
```

### Classification Summary

| File Characteristics | Classification | Package Identity |
|---------------------|----------------|------------------|
| Has top-level `export` | Package | Path to closest `package.json` |
| `declare module "name"` block | Package (named) | Path to `package.json` resolved via Node module resolution for the module name |
| No exports, outside named modules | Global | N/A |

### Mixed Files

A single `.d.ts` file can contain both globals and packages:

```typescript
// mixed.d.ts

// These are GLOBAL (no top-level exports in file, outside named modules)
interface GlobalConfig { ... }
declare var __VERSION__: string;

// This is PACKAGE "my-lib"
declare module "my-lib" {
    export interface LibConfig { ... }
    export function init(config: LibConfig): void;
}
```

Note: If the file had any top-level `export`, then `GlobalConfig` and `__VERSION__` would be part of an anonymous package, not globals. The presence of `export` changes the entire file's default classification.

## Cross-File Namespace Merging

Declarations from multiple `.d.ts` files are merged into shared namespaces based on their classification:

### Global Namespace Merging

All global declarations across all files are added to **the same global namespace**:

```typescript
// lib.es5.d.ts (globals)
interface Array<T> { ... }
interface Object { ... }

// lib.dom.d.ts (globals)
interface Document { ... }
interface Window { ... }

// lib.es2015.promise.d.ts (globals)
interface Promise<T> { ... }
```

Result:
```
Global Namespace
    ├── Array      (from lib.es5.d.ts)
    ├── Object     (from lib.es5.d.ts)
    ├── Document   (from lib.dom.d.ts)
    ├── Window     (from lib.dom.d.ts)
    └── Promise    (from lib.es2015.promise.d.ts)
```

### Package Identity

Packages are **not** identified by their name (names are not unique). Instead, packages are identified by the **path to the `package.json`** file resolved using the standard Node module resolution algorithm.

This means:
- Two packages with the same name but different `package.json` paths are **different packages**
- Files belonging to the same `package.json` are merged into **the same namespace**

### Package Namespace Merging (Same package.json)

Declarations from multiple `.d.ts` files that resolve to the **same `package.json`** are merged into **the same namespace**:

```typescript
// node_modules/lodash/array.d.ts
// Resolves to: node_modules/lodash/package.json
declare module "lodash" {
    export function chunk<T>(array: T[], size: number): T[][];
    export function compact<T>(array: T[]): T[];
}

// node_modules/lodash/collection.d.ts
// Resolves to: node_modules/lodash/package.json (same package.json)
declare module "lodash" {
    export function each<T>(collection: T[], iteratee: (value: T) => void): void;
    export function map<T, U>(collection: T[], iteratee: (value: T) => U): U[];
}
```

Result:
```
Package (node_modules/lodash/package.json) Namespace
    ├── chunk    (from array.d.ts)
    ├── compact  (from array.d.ts)
    ├── each     (from collection.d.ts)
    └── map      (from collection.d.ts)
```

### Package Namespace Isolation (Different package.json)

Packages with **different `package.json` paths** have their own **isolated namespaces**, even if they have the same name:

```typescript
// node_modules/lodash/index.d.ts
// Resolves to: node_modules/lodash/package.json
export function map<T, U>(arr: T[], fn: (x: T) => U): U[];

// node_modules/underscore/index.d.ts
// Resolves to: node_modules/underscore/package.json
export function map<T, U>(list: T[], iteratee: (value: T) => U): U[];

// node_modules/ramda/index.d.ts
// Resolves to: node_modules/ramda/package.json
export function map<A, B>(fn: (a: A) => B): (list: A[]) => B[];
```

Result:
```
Package (node_modules/lodash/package.json) Namespace
    └── map (lodash signature)

Package (node_modules/underscore/package.json) Namespace
    └── map (underscore signature)

Package (node_modules/ramda/package.json) Namespace
    └── map (ramda signature)
```

Each package's `map` function is distinct and does not conflict with others because they resolve to different `package.json` files.

**Note**: Even two packages with the same `name` field in their `package.json` would be separate if they have different `package.json` paths (e.g., different versions installed in nested `node_modules`).

### Merging Rules Summary

| Scenario | Behavior |
|----------|----------|
| Globals from multiple files | Merge into single global namespace |
| Same `package.json` path, multiple files | Merge into single package namespace |
| Different `package.json` paths | Separate isolated namespaces |
| Same declaration name in same namespace | Later declaration augments/overwrites earlier |

## Desired Behavior

Introduce separate namespaces for globals and packages, where package declarations can shadow global declarations. Both namespaces should be "nameless" (not requiring a prefix to access).

### Proposed Namespace Hierarchy

```
Lookup Resolution (priority order):
1. Local scope (user-defined types/values)
2. Package namespace (shadowing globals)
3. Global namespace (TypeScript builtins)

Scope Structure:
    └── Local Namespace (user code)
        └── Package Namespace (imported packages)
            └── Global Namespace (lib.es5.d.ts, lib.dom.d.ts)
```

### Shadowing Example

```typescript
// lib.es5.d.ts (global)
interface Array<T> { length: number; ... }

// some-package/index.d.ts (package)
interface Array<T> { customMethod(): void; ... }

// user code
let arr: Array<string>;  // Resolves to package Array, not global
```

## Technical Requirements

### 1. Namespace Separation

- **R1.1**: Create distinct namespace containers for globals and packages
- **R1.2**: Both namespaces must be "nameless" (no prefix required for access)
- **R1.3**: Implement lookup chain: Local → Package → Global

### 2. Type Resolution

- **R2.1**: Type lookups must traverse the namespace chain in priority order
- **R2.2**: First match in the chain wins (enables shadowing)
- **R2.3**: Unresolved lookups should continue to parent namespaces
- **R2.4**: Error only when a binding is not found in any namespace

### 3. Value Resolution

- **R3.1**: Value bindings follow the same resolution rules as types
- **R3.2**: Package values can shadow global values (e.g., custom `fetch`)

### 4. .d.ts File Classification

- **R4.1**: Files with top-level `export` statements must be classified as packages
- **R4.2**: For files with top-level exports, the package identity is the path to the closest `package.json` found by traversing up the file's directory path
- **R4.3**: `declare module "name"` blocks must resolve to a package identified by the `package.json` path found via Node module resolution for the module name
- **R4.4**: Files without top-level exports must treat non-module declarations as globals
- **R4.5**: A single `.d.ts` file may contain both global declarations and named module packages
- **R4.6**: The parser/loader must detect and categorize declarations based on these rules

### 5. Cross-File Namespace Merging

- **R5.1**: Global declarations from all files must be merged into a single shared global namespace
- **R5.2**: Package declarations that resolve to the same `package.json` path must be merged into the same namespace
- **R5.3**: Packages with different `package.json` paths must have isolated namespaces (even if they have the same name)
- **R5.4**: When the same declaration name appears multiple times in the same namespace, later declarations augment or overwrite earlier ones (following TypeScript's declaration merging rules)

### 6. Inference Changes

- **R6.1**: `Prelude()` should infer globals into a dedicated global namespace
- **R6.2**: Package imports should infer into a dedicated package namespace
- **R6.3**: User code should infer into the local namespace
- **R6.4**: Named module declarations should infer into their respective package namespaces
- **R6.5**: Maintain backwards compatibility for existing code

### 7. Scope Structure Changes

- **R7.1**: Modify `Scope` structure to support chained namespace lookups
- **R7.2**: Each scope level should have access to its namespace and parent chain
- **R7.3**: Namespace chain should be: `local.Parent → package.Parent → global`

## Design Considerations

### Option A: Scope Chain with Multiple Namespaces

Modify `Scope` to hold a chain of namespaces:

```go
type Scope struct {
    Parent    *Scope
    Namespace *type_system.Namespace
    // Namespace chain is implicit via Parent
}
```

Prelude creates:
```
globalScope (Namespace: globals)
    ↑
packageScope (Namespace: packages, Parent: globalScope)
    ↑
userScope (Namespace: local, Parent: packageScope)
```

**Pros**: Minimal structural changes, uses existing parent chain
**Cons**: Package scope needs explicit insertion point

### Option B: Explicit Namespace Layers

Add explicit layer references:

```go
type Scope struct {
    Parent           *Scope
    Namespace        *type_system.Namespace
    GlobalNamespace  *type_system.Namespace  // Always points to globals
    PackageNamespace *type_system.Namespace  // Package-level bindings
}
```

**Pros**: Clear separation, explicit access to each layer
**Cons**: More complex structure, potential duplication

### Option C: Namespace with Source Tracking

Extend `Binding` to track source and filter during lookup:

```go
type Binding struct {
    Type       *Type
    Source     Provenance
    SourceKind SourceKind  // Global, Package, Local
}
```

**Pros**: Single namespace, rich metadata
**Cons**: Every lookup must filter, complexity in shadowing logic

### Recommended Approach

**Option A** is recommended because:
1. Leverages existing parent chain mechanism
2. Shadowing is automatic (local found before package before global)
3. Minimal changes to existing lookup code
4. Clear conceptual model matching TypeScript behavior

## Implementation Considerations

### Affected Files

| File | Changes Required |
|------|------------------|
| `internal/checker/prelude.go` | Create global scope as base; classify declarations per rules |
| `internal/checker/infer_import.go` | Infer packages into package scope; handle named modules |
| `internal/checker/scope.go` | Potentially add helper methods for layer access |
| `internal/checker/infer_*.go` | Ensure lookups traverse full chain |
| `internal/dts_parser/*.go` | Detect top-level exports to determine file classification |
| `internal/interop/*.go` | Convert declarations with correct namespace assignment |
| `internal/type_system/types.go` | No changes expected |

### Migration Path

1. Update `.d.ts` parser to detect top-level exports and track file classification
2. Modify `loadTypeScriptModule()` to separate globals from packages based on classification rules
3. Refactor `Prelude()` to return a `globalScope` containing only global declarations
4. Create `packageScope` with `Parent: globalScope`
5. Update import handling to infer named modules into separate package namespaces
6. User code scope has `Parent: packageScope`
7. Verify all lookups correctly traverse the chain
8. Add tests for shadowing scenarios and mixed global/package files

### Edge Cases to Handle

1. **Circular imports between packages**: Each package should resolve against globals, not other packages being loaded
2. **Re-exports**: A package re-exporting a global type should expose the global, not shadow it
3. **Type augmentation**: TypeScript's `declare global` should still augment globals, not create package-level bindings
4. **Qualified access**: If explicit namespace access is supported (e.g., `globalThis.Array`), it should bypass shadowing

## Acceptance Criteria

1. [ ] Global types from `lib.es5.d.ts` and `lib.dom.d.ts` are in a separate namespace from package types
2. [ ] Package declarations can shadow global declarations of the same name
3. [ ] User code can access both shadowed and shadowing types (if qualified access is supported)
4. [ ] Existing code that doesn't rely on shadowing continues to work
5. [ ] Type resolution performance is not significantly degraded
6. [ ] Error messages clearly indicate which namespace a binding came from
7. [ ] Tests cover shadowing scenarios for both types and values
8. [ ] `.d.ts` files with top-level exports are correctly classified as packages
9. [ ] `.d.ts` files without exports correctly treat non-module declarations as globals
10. [ ] Named module declarations (`declare module "name"`) create separate package namespaces
11. [ ] Mixed files (globals + named modules) are correctly partitioned
12. [ ] Global declarations from multiple files merge into a single global namespace
13. [ ] Files resolving to the same `package.json` path merge into a single package namespace
14. [ ] Files resolving to different `package.json` paths have isolated namespaces (even with same package name)

## Open Questions

1. Should there be a way to explicitly access the global namespace when shadowed (e.g., `global::Array`)?
2. How should conflicting declarations within the same namespace be handled (e.g., two files defining the same interface with incompatible signatures)?

## References

- Current implementation: [infer_module.go](../../internal/checker/infer_module.go)
- Prelude loading: [prelude.go](../../internal/checker/prelude.go)
- Import handling: [infer_import.go](../../internal/checker/infer_import.go)
- Scope structure: [scope.go](../../internal/checker/scope.go)
