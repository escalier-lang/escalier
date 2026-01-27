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

`declare module "name"` blocks each define a **separate package**, regardless of other content in the file. Unlike files with top-level exports (which use `package.json` path as identity), named module declarations use the **module name string** as their identity:

```typescript
// multi-module.d.ts
declare module "lodash" {
    // Package identity: "lodash" (the module name string)
    export function map<T, U>(arr: T[], fn: (x: T) => U): U[];
}

declare module "lodash/fp" {
    // Package identity: "lodash/fp" (a separate module name string)
    export function map<T, U>(fn: (x: T) => U): (arr: T[]) => U[];
}
```

Each named module declaration creates its own isolated package namespace, keyed by the module name string. Multiple `declare module "lodash"` blocks across different files will merge into the same "lodash" package namespace.

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
| `declare module "name"` block | Package (named) | The module name string (e.g., `"lodash"`, `"lodash/fp"`) |
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

## .esc File Classification Rules

`.esc` files (Escalier source files) follow simpler classification rules:

### User Code (.esc files in project)

`.esc` files in the user's project are always classified as **local/user code**:
- Declarations are added to the local namespace
- Can shadow both package and global declarations
- Do not require imports to be visible within the same project

### Library Code (.esc files in libs/)

`.esc` files in a `libs/` folder are treated as **packages**:
- The package identity is the path to the `package.json` in the library's root
- Declarations must be exported to be accessible from outside the library
- Follow the same merging rules as `.d.ts` packages

### .esc Classification Summary

| Location | Classification | Package Identity |
|----------|----------------|------------------|
| Project source files | Local/User code | N/A |
| `libs/` folder | Package | Path to library's `package.json` |

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

Package identity depends on how the package is defined:

**1. Files with top-level exports**: Identified by the **path to the `package.json`** file found by traversing up the directory tree. This means:
- Two packages with the same name but different `package.json` paths are **different packages**
- Multiple `.d.ts` files belonging to the same `package.json` are merged into **the same namespace**

**2. Named module declarations (`declare module "name"`)**: Identified by the **module name string**. This means:
- `declare module "lodash"` and `declare module "lodash/fp"` are **different packages** (different strings)
- Multiple `declare module "lodash"` blocks across different files merge into **the same namespace**

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

Each package has its own isolated namespace. When a package is imported, its exported declarations become available for lookup and can shadow globals.

```
Lookup Resolution (priority order):
1. Local scope (user-defined types/values)
2. Imported package namespaces (in import order, shadowing globals)
3. Global namespace (TypeScript builtins)

Scope Structure:
    └── Local Namespace (user code)
        └── Imported Package Namespaces (one per imported package)
            └── Global Namespace (lib.es5.d.ts, lib.dom.d.ts)
```

**Important**: Package declarations only participate in lookup **after being imported**. An unimported package's declarations do not shadow globals.

### Multiple Package Lookup

When multiple imported packages define the same name, the **first import wins** (earlier imports shadow later ones):

```typescript
// user code
import { Array } from "custom-arrays";  // This Array shadows...
import { Array } from "other-arrays";   // ...this Array

let arr: Array<string>;  // Resolves to custom-arrays.Array
```

### Shadowing Example

```typescript
// lib.es5.d.ts (global)
interface Array<T> { length: number; ... }

// some-package/index.d.ts (package)
interface Array<T> { customMethod(): void; ... }

// user code
import { Array } from "some-package";
let arr: Array<string>;  // Resolves to package Array, not global
```

## Technical Requirements

### 1. Namespace Separation

- **R1.1**: Create a distinct global namespace container for TypeScript builtins
- **R1.2**: Create separate namespace containers for each package (keyed by package identity)
- **R1.3**: Both global and package namespaces must be "nameless" (no prefix required for access)
- **R1.4**: Implement lookup chain: Local → Imported Packages (in import order) → Global

### 2. Type Resolution

- **R2.1**: Type lookups must traverse the namespace chain in priority order
- **R2.2**: First match in the chain wins (enables shadowing)
- **R2.3**: Unresolved lookups should continue to parent namespaces
- **R2.4**: Error only when a binding is not found in any namespace
- **R2.5**: Local (user code) types can shadow package types
- **R2.6**: Package types can shadow global types

### 3. Value Resolution

- **R3.1**: Value bindings follow the same resolution rules as types
- **R3.2**: Local (user code) values can shadow package values
- **R3.3**: Package values can shadow global values (e.g., custom `fetch`)

### 4. .d.ts File Classification

- **R4.1**: Files with top-level `export` statements must be classified as packages
- **R4.2**: For files with top-level exports, the package identity is the path to the closest `package.json` found by traversing up the file's directory path
- **R4.3**: `declare module "name"` blocks must use the module name string as the package identity (e.g., `"lodash"`, `"lodash/fp"`)
- **R4.4**: Files without top-level exports must treat non-module declarations as globals
- **R4.5**: A single `.d.ts` file may contain both global declarations and named module packages
- **R4.6**: The parser/loader must detect and categorize declarations based on these rules

### 4b. .esc File Classification

- **R4b.1**: `.esc` files in the user's project must be classified as local/user code
- **R4b.2**: `.esc` files in a `libs/` folder must be classified as packages
- **R4b.3**: Library `.esc` files must use the library's `package.json` path as package identity
- **R4b.4**: Only exported declarations from library `.esc` files are accessible externally

### 4c. Global Augmentation (`declare global`)

- **R4c.1**: `declare global { ... }` blocks must add declarations to the global namespace, not the package namespace
- **R4c.2**: Global augmentations apply regardless of whether the file is classified as a package
- **R4c.3**: Global augmentations merge with existing global declarations (following TypeScript's declaration merging rules)
- **R4c.4**: Global augmentations do not affect shadowing—packages can still shadow augmented globals

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
- **R7.3**: Namespace chain should be: `local → imported packages (in order) → global`
- **R7.4**: Package namespaces must only be included in lookup after the package is imported

### 8. Import Mechanics

- **R8.1**: `import { name } from "pkg"` must add `name` to the local namespace, bound to the package's exported declaration
- **R8.2**: `import * as ns from "pkg"` must add `ns` to the local namespace as a namespace object containing all exports
- **R8.3**: `import pkg from "pkg"` must add `pkg` to the local namespace, bound to the package's default export
- **R8.4**: After any import from a package, that package's namespace must be added to the lookup chain for shadowing
- **R8.5**: When the same package is imported multiple times, it must only appear once in the lookup chain (at its first import position)
- **R8.6**: Import order determines shadowing priority: earlier imports shadow later imports

### 9. Error Handling

- **R9.1**: If no `package.json` is found when traversing up from a `.d.ts` file with top-level exports, emit an error indicating the package cannot be identified
- **R9.2**: If a `package.json` is found but lacks a `name` field, emit a warning (package identity is still the path)
- **R9.3**: If an import cannot be resolved via Node module resolution, emit an error indicating the module was not found
- **R9.4**: If a binding is not found in any namespace (local, packages, or global), emit an "undefined identifier" error
- **R9.5**: Error messages should indicate which namespace was searched and suggest possible corrections (e.g., "Did you mean to import X from package Y?")

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

### Namespace Separation
1. [ ] Global types from `lib.es5.d.ts` and `lib.dom.d.ts` are in a separate namespace from package types
2. [ ] Package declarations can shadow global declarations of the same name
3. [ ] User code (local) declarations can shadow package declarations
4. [ ] User code can access both shadowed and shadowing types (if qualified access is supported)
5. [ ] Existing code that doesn't rely on shadowing continues to work

### .d.ts File Classification
6. [ ] `.d.ts` files with top-level exports are correctly classified as packages
7. [ ] `.d.ts` files without exports correctly treat non-module declarations as globals
8. [ ] Named module declarations (`declare module "name"`) use module name string as identity
9. [ ] Mixed files (globals + named modules) are correctly partitioned

### Cross-File Merging
10. [ ] Global declarations from multiple files merge into a single global namespace
11. [ ] Files resolving to the same `package.json` path merge into a single package namespace
12. [ ] Files resolving to different `package.json` paths have isolated namespaces (even with same package name)
13. [ ] Multiple `declare module "X"` blocks (same name) across files merge into one namespace

### .esc File Handling
14. [ ] `.esc` files in user project are classified as local/user code
15. [ ] `.esc` files in `libs/` folder are classified as packages

### Import Mechanics
16. [ ] Importing a package adds it to the lookup chain for shadowing
17. [ ] Import order determines shadowing priority (earlier imports shadow later)
18. [ ] Same package imported multiple times appears only once in lookup chain

### Global Augmentation
19. [ ] `declare global` blocks add declarations to global namespace (not package)
20. [ ] Global augmentations merge with existing globals

### Error Handling
21. [ ] Error emitted when `package.json` not found for files with top-level exports
22. [ ] Error emitted when imported module cannot be resolved
23. [ ] Error messages indicate which namespaces were searched

### Performance & Quality
24. [ ] Type resolution performance is not significantly degraded
25. [ ] Error messages clearly indicate which namespace a binding came from
26. [ ] Tests cover shadowing scenarios for both types and values

## Open Questions

1. Should there be a way to explicitly access the global namespace when shadowed (e.g., `global::Array`)?
2. How should conflicting declarations within the same namespace be handled (e.g., two files defining the same interface with incompatible signatures)?
3. Should package subpath imports (e.g., `import from "lodash/array"`) resolve to the same package namespace as the root import (`import from "lodash"`)?
4. How should re-exports be handled? If package A re-exports a type from package B, does it shadow globals or expose B's original?

## References

- Current implementation: [infer_module.go](../../internal/checker/infer_module.go)
- Prelude loading: [prelude.go](../../internal/checker/prelude.go)
- Import handling: [infer_import.go](../../internal/checker/infer_import.go)
- Scope structure: [scope.go](../../internal/checker/scope.go)
