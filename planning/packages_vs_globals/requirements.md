# Requirements: Separate Package and Global Namespaces

## 1. Problem Statement

Currently, `InferModule` does not differentiate between:
1. **Global type definitions** - modules converted from `.d.ts` files that ship with TypeScript (e.g., `lib.es5.d.ts`, `lib.dom.d.ts`)
2. **Package type definitions** - modules defined by `.esc` files in a `libs/` folder or converted from `.d.ts` files from npm packages

All type definitions are merged into a single namespace during inference, which prevents package declarations from shadowing global declarations.

## 2. Current Behavior

### 2.1 Type Definition Sources

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

### 2.2 Current Merging Behavior

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

## 3. .d.ts File Classification Rules

A `.d.ts` file's declarations are classified as either **package** or **global** based on the file's structure:

### 3.1 Rule 1: Top-level Exports → Package

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

### 3.2 Rule 2: Named Module Declarations → Separate Packages

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

### 3.3 Rule 3: No Top-level Exports → Globals

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

### 3.4 Rule 4: `export = Namespace` → Expand to Top-Level Exports

When a `.d.ts` file uses the `export = Namespace` pattern, treat it as equivalent to top-level exports by expanding the namespace members:

```typescript
// This pattern:
declare namespace Foo {
    export const bar: number;
    export function baz(): string;
}
export = Foo;

// Is treated as equivalent to:
export const bar: number;
export function baz(): string;
```

This allows CommonJS-style module exports to be handled consistently with ES module exports.

### 3.5 Classification Summary

| File Characteristics | Classification | Package Identity |
|---------------------|----------------|------------------|
| Has top-level `export` | Package | Path to closest `package.json` |
| Has `export = Namespace` | Package (expanded) | Path to closest `package.json` |
| `declare module "name"` block | Package (named) | The module name string (e.g., `"lodash"`, `"lodash/fp"`) |
| No exports, outside named modules | Global | N/A |

### 3.6 Mixed Files

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

## 4. .esc File Classification Rules

`.esc` files (Escalier source files) follow simpler classification rules:

### 4.1 User Code (.esc files in project)

`.esc` files in the user's project are always classified as **local/user code**:
- Declarations are added to the local namespace
- Can shadow both package and global declarations
- Do not require imports to be visible within the same project

### 4.2 Library Code (.esc files in libs/)

`.esc` files in a `libs/` folder are treated as **packages**:
- The package identity is the path to the `package.json` in the library's root
- Declarations must be exported to be accessible from outside the library
- Follow the same merging rules as `.d.ts` packages
- **Cannot** reference symbols from scripts (`bin/` directory)

### 4.3 Scripts (.esc files in bin/)

`.esc` files in a `bin/` folder are treated as **scripts**:
- Scripts are entry points for executable programs
- Can reference any exported symbol from library code (`libs/` directory)
- Can reference globals and imported packages
- **Cannot** be referenced by library code (one-way dependency)
- Each script is independent and does not share local declarations with other scripts

### 4.4 .esc Classification Summary

| Location | Classification | Package Identity | Can Reference |
|----------|----------------|------------------|---------------|
| Project source files | Local/User code | N/A | Libs, globals, packages |
| `libs/` folder | Package | Path to library's `package.json` | Other libs, globals, packages |
| `bin/` folder | Script | N/A | Libs, globals, packages |

## 5. Cross-File Namespace Merging

Declarations from multiple `.d.ts` files are merged into shared namespaces based on their classification:

### 5.1 Global Namespace Merging

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

### 5.2 Package Identity

Package identity depends on how the package is defined:

**1. Files with top-level exports**: Identified by the **path to the `package.json`** file found by traversing up the directory tree.

**Important**: The `name` field inside `package.json` is **irrelevant** for identity—only the file path matters. This means:
- Two packages with the same `name` but different `package.json` paths are **different packages** with **separate namespaces**
- Multiple `.d.ts` files belonging to the same `package.json` path are merged into **the same namespace**

**2. Named module declarations (`declare module "name"`)**: Identified by the **module name string**. This means:
- `declare module "lodash"` and `declare module "lodash/fp"` are **different packages** (different strings)
- Multiple `declare module "lodash"` blocks across different files merge into **the same namespace**

### 5.3 Package Namespace Merging (Same package.json)

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

### 5.4 Package Namespace Isolation (Different package.json)

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

### 5.5 Same Package Name, Different Paths → Different Namespaces

**Critical**: The package `name` field in `package.json` does **not** determine namespace identity—only the **file path** to `package.json` matters. Two packages with identical names but different `package.json` paths are treated as completely separate namespaces.

This commonly occurs with:
- **Nested dependencies**: Different versions of the same package in nested `node_modules`
- **Monorepos**: Multiple packages with similar names in different workspace locations
- **Symlinked packages**: Development packages linked from different directories

```
project/
├── node_modules/
│   └── lodash/                          # v4.17.21
│       ├── package.json                 # { "name": "lodash", "version": "4.17.21" }
│       └── index.d.ts
└── packages/
    └── my-lib/
        └── node_modules/
            └── lodash/                  # v3.10.1 (older version)
                ├── package.json         # { "name": "lodash", "version": "3.10.1" }
                └── index.d.ts
```

Even though both have `"name": "lodash"` in their `package.json`, they result in **two separate namespaces**:

```
Package (project/node_modules/lodash/package.json) Namespace     ← lodash v4.17.21
    └── map, filter, reduce, ...

Package (project/packages/my-lib/node_modules/lodash/package.json) Namespace  ← lodash v3.10.1
    └── map, filter, reduce, ...  (potentially different signatures)
```

Which namespace is used depends on which `package.json` is resolved when processing the import statement.

### 5.6 Merging Rules Summary

| Scenario | Behavior |
|----------|----------|
| Globals from multiple files | Merge into single global namespace |
| Same `package.json` path, multiple files | Merge into single package namespace |
| Different `package.json` paths | Separate isolated namespaces |
| Same declaration name in same namespace | Later declaration augments/overwrites earlier |

## 6. Desired Behavior

Introduce separate namespaces for globals and packages. Packages have their own isolated namespaces, accessed via qualified identifiers. Local declarations can shadow global declarations.

### 6.1 Proposed Namespace Hierarchy

Each package has its own isolated namespace stored in a separate package registry. When a package is imported, its namespace is bound as a **sub-namespace** within the **file's local namespace**, accessible via a **local identifier**. All access to package symbols must be **qualified** using that identifier.

```
Lookup Resolution (priority order):
1. File scope (import bindings as sub-namespaces) - for modules only
2. Module/Script scope (user-defined types/values)
3. Global namespace (TypeScript builtins)

Scope Structure for Modules (three levels):
    globalScope (Namespace: globals like Array, Promise, etc.)
        ↑ Parent
    moduleScope (Namespace: module-level declarations shared across files)
        ↑ Parent
    fileScope (Namespace: file-local import bindings)
        └── Package sub-namespaces (file-scoped, created by import statements):
            ├── "lodash" → points to registry entry
            └── "ramda" → points to registry entry

Scope Structure for Scripts (two levels):
    globalScope (Namespace: globals)
        ↑ Parent
    scriptScope (Namespace: local declarations + import bindings)
```

Package namespaces are **not** part of the unqualified lookup chain. They are accessed via the local identifier bound during import (e.g., `lodash.map` looks up `lodash` in the file scope, then accesses the `map` member of that sub-namespace).

**Import Scoping**: Imports are **file-scoped** (similar to Go). Each `.esc` file must contain its own import statements for the packages it uses. A package namespace bound by an import in one file is **NOT** visible to other files in the same module. If a file attempts to access a package namespace without the corresponding import statement, an error should be reported.

### 6.2 Import and Qualified Access

When a package is imported, the import statement binds the package's namespace as a sub-namespace within the local namespace:

```escalier
import "lodash"

fn main() {
    // All package symbols must be qualified with the package identifier
    // lodash.map: lookup 'lodash' locally (finds sub-namespace), access 'map' member
    let result = lodash.map([1, 2, 3], fn(x) { x * 2 })
    console.log(`result = ${result}`)
}
```

Multiple packages can be imported without conflict, since each is bound as a separate sub-namespace:

```escalier
import "lodash"
import "ramda"

fn main() {
    // No ambiguity - each is a separate sub-namespace
    let a = lodash.map([1, 2, 3], fn(x) { x * 2 })
    let b = ramda.map(fn(x) { x * 2 }, [1, 2, 3])
}
```

### 6.3 Local Shadowing of Globals

Local declarations can shadow global declarations:

```escalier
// lib.es5.d.ts (global)
// interface Array<T> { length: number; ... }

// user code
type Array<T> = { items: T[], customMethod: fn() -> void }

let arr: Array<string>  // Resolves to local Array, not global
let globalArr: globalThis.Array<string>  // Explicitly access the shadowed global
```

**Note**: Packages cannot shadow globals because package access is always qualified. `Array` refers to the global (or local if shadowed), while `some_package.Array` refers to the package's type—they don't conflict. Shadowed globals can always be accessed via `globalThis`.

## 7. Technical Requirements

### 7.1 Namespace Separation

- **R1.1**: Create a distinct global namespace container for TypeScript builtins
- **R1.2**: Create a separate package registry to store package namespaces (keyed by package identity)
- **R1.3**: Package namespaces in the registry are bound as sub-namespaces within the local namespace during import
- **R1.4**: Global namespace must be "nameless" (no prefix required for access)
- **R1.5**: Package namespaces must be accessed via qualified identifiers (e.g., `pkg.Symbol`), using standard namespace member lookup
- **R1.6**: Implement lookup chain: Local → Global (packages are not in the unqualified lookup chain)

### 7.2 Type Resolution

- **R2.1**: Unqualified type lookups traverse Local → Global
- **R2.2**: First match in the chain wins (enables local shadowing of globals)
- **R2.3**: Unresolved lookups should continue to parent namespace (global)
- **R2.4**: Error only when a binding is not found in any namespace
- **R2.5**: Local (user code) types can shadow global types
- **R2.6**: Qualified type lookups (e.g., `pkg.Type`) resolve directly in the package namespace
- **R2.7**: Shadowed globals can be accessed via `globalThis` (e.g., `globalThis.Array`)

### 7.3 Value Resolution

- **R3.1**: Value bindings follow the same resolution rules as types
- **R3.2**: Local (user code) values can shadow global values
- **R3.3**: Qualified value lookups (e.g., `pkg.value`) resolve directly in the package namespace

### 7.4 .d.ts File Classification

- **R4.1**: Files with top-level `export` statements must be classified as packages
- **R4.2**: For files with top-level exports, the package identity is the path to the closest `package.json` found by traversing up the file's directory path
- **R4.3**: `declare module "name"` blocks must use the module name string as the package identity (e.g., `"lodash"`, `"lodash/fp"`)
- **R4.4**: Files without top-level exports must treat non-module declarations as globals
- **R4.5**: A single `.d.ts` file may contain both global declarations and named module packages
- **R4.6**: The parser/loader must detect and categorize declarations based on these rules
- **R4.7**: Files with `export = Namespace` must expand the namespace members to top-level exports (CommonJS-style exports are treated as ES module exports)

### 7.5 .esc File Classification

- **R5.1**: `.esc` files in the user's project must be classified as local/user code
- **R5.2**: `.esc` files in a `libs/` folder must be classified as packages
- **R5.3**: Library `.esc` files must use the library's `package.json` path as package identity
- **R5.4**: Only exported declarations from library `.esc` files are accessible externally
- **R5.5**: `.esc` files in a `bin/` folder must be classified as scripts
- **R5.6**: Scripts can reference exported symbols from library code (`libs/`)
- **R5.7**: Library code must not reference symbols from scripts (`bin/`)—emit an error if attempted

### 7.6 Global Augmentation (`declare global`)

- **R6.1**: `declare global { ... }` blocks must add declarations to the global namespace, not the package namespace
- **R6.2**: Global augmentations apply regardless of whether the file is classified as a package
- **R6.3**: Global augmentations merge with existing global declarations (following TypeScript's declaration merging rules)
- **R6.4**: Global augmentations are accessible via unqualified lookup; package symbols remain qualified

### 7.7 Cross-File Namespace Merging

- **R7.1**: Global declarations from all files must be merged into a single shared global namespace
- **R7.2**: Package declarations that resolve to the same `package.json` path must be merged into the same namespace
- **R7.3**: Packages with different `package.json` paths must have isolated namespaces, **regardless of the `name` field** in their `package.json` files
- **R7.4**: The `name` field in `package.json` must **not** be used for namespace identity—only the file path determines identity
- **R7.5**: Only functions and interfaces can have multiple declarations with the same name in the same namespace (functions for overloading, interfaces for interface merging); other duplicate declarations are errors

### 7.8 Inference Changes

- **R8.1**: `Prelude()` should infer globals into a dedicated global namespace
- **R8.2**: Package declarations should infer into dedicated package namespaces stored in the package registry
- **R8.3**: User code should infer into the local namespace
- **R8.4**: Named module declarations should infer into their respective package namespaces in the registry
- **R8.5**: Import statements bind package namespaces as sub-namespaces in the local namespace
- **R8.6**: Maintain backwards compatibility for existing code

### 7.9 Scope Structure Changes

- **R9.1**: Modify `Scope` structure to support chained namespace lookups (Local → Global)
- **R9.2**: Each scope level should have access to its namespace and parent chain
- **R9.3**: Package namespaces are stored in a separate package registry (e.g., at Checker level)
- **R9.4**: Import statements bind package namespaces as sub-namespaces within the local namespace
- **R9.5**: Qualified access uses standard namespace member lookup (no special package handling in Scope)

### 7.10 Import Mechanics

- **R10.1**: `import "pkg"` must look up the package in the package registry and bind its namespace as a sub-namespace within the **file's** local namespace, using a local identifier derived from the package name (strip scope prefix, replace hyphens with underscores, e.g., `@scope/pkg-name` → `pkg_name`)
- **R10.2**: `import "pkg" as alias` must bind the package namespace as a sub-namespace using the specified alias instead of the derived name
- **R10.3**: All package symbols must be accessed via the qualified identifier (e.g., `pkg.symbol`), which uses standard namespace member lookup
- **R10.4**: The local identifier bound by import can shadow global declarations of the same name
- **R10.5**: Multiple packages can be imported without conflict (each is a separate sub-namespace)
- **R10.6**: If the same package is imported multiple times in the same file, subsequent imports are no-ops (bind to the same sub-namespace)
- **R10.7**: Subpath imports (e.g., `import "lodash/array"`) create separate package namespace entries in the registry from root imports (`import "lodash"`)
- **R10.8**: Package identifiers follow the same scoping rules as other local bindings
- **R10.9**: **File-scoped imports**: Import bindings are file-scoped (like Go). Each `.esc` file must contain its own import statements for the packages it uses
- **R10.10**: A package namespace bound by an import in one file is NOT visible to other files in the same module
- **R10.11**: If a file attempts to access a package namespace without the corresponding import statement in that file, emit an "undefined identifier" error
- **R10.12**: Re-exports of package symbols use alias syntax: `export val map = lodash.map`. This preserves type identity rather than creating copies

### 7.11 Error Handling

- **R11.1**: If no `package.json` is found when traversing up from a `.d.ts` file with top-level exports, emit an error indicating the package cannot be identified
- **R11.2**: If a `package.json` is found but lacks a `name` field, emit a warning (package identity is still the path)
- **R11.3**: If an import cannot be resolved via Node module resolution, emit an error indicating the module was not found
- **R11.4**: If a binding is not found in any namespace (local, packages, or global), emit an "undefined identifier" error
- **R11.5**: Error messages should indicate which namespace was searched and suggest possible corrections (e.g., "Did you mean to import X from package Y?")

## 8. Design Considerations

### 8.1 Option A: Scope Chain with Package Registry

Scope chain for unqualified lookup traverses the parent chain. Packages are stored in a separate registry (at Checker level) and bound as sub-namespaces in the file's local namespace:

```go
// Separate from Scope structure
type PackageRegistry struct {
    packages map[string]*type_system.Namespace  // package identity → namespace
}

type Scope struct {
    Parent    *Scope
    Namespace *type_system.Namespace
}
```

When processing `import "lodash"` in a file:
1. Look up package in registry to get its namespace
2. Bind the identifier `lodash` as a sub-namespace within the **file's** scope namespace
3. Qualified access `lodash.map` works naturally through namespace member lookup

Scope structure for modules (three levels):
```
globalScope (Namespace: globals)
    ↑ Parent
moduleScope (Namespace: module-level declarations shared across files)
    ↑ Parent
fileScope (Namespace: file-local import bindings as sub-namespaces {"lodash": ..., "ramda": ...})
```

Scope structure for scripts (two levels):
```
globalScope (Namespace: globals)
    ↑ Parent
scriptScope (Namespace: local declarations + import bindings)
```

**Note**: Import bindings are file-scoped. If file_a.esc imports "lodash", file_b.esc cannot access "lodash" unless it has its own import statement. Module declarations (types, functions) go to moduleScope and ARE visible across files.

**Pros**: Clean separation between unqualified lookup and qualified package access; file-scoped imports prevent accidental cross-file dependencies
**Cons**: Slightly more complex scope structure for modules

### 8.2 Option B: Package Identifiers as Local Bindings (Merged with Option A)

This option has been merged with Option A. Package identifiers are bound as sub-namespaces in the local namespace:
- The package registry stores complete package namespaces
- Import processing binds the package identifier to a sub-namespace in the local scope
- Member access (`pkg.Symbol`) uses standard namespace member lookup
- Package identifiers can shadow globals (they're just local bindings)

**Note**: This is now the recommended approach (see 8.4)

### 8.3 Option C: Unified Namespace with Provenance

Track binding provenance for diagnostics without affecting lookup:

```go
type Binding struct {
    Type       *Type
    Provenance Provenance  // For error messages and debugging
}
```

**Pros**: Simple lookup; provenance is metadata only
**Cons**: Doesn't help with the core package access design

### 8.4 Recommended Approach

**Option A (with package identifiers as sub-namespaces)** is recommended because:
1. Clean separation between unqualified lookup (Local → Global) and qualified package access
2. Local shadowing of globals is automatic via parent chain
3. Package registry provides single source of truth for package namespaces
4. Qualified access (`pkg.Symbol`) uses standard namespace member lookup - no special handling needed
5. Package identifiers are just local bindings to sub-namespaces, allowing them to shadow globals naturally
6. Matches the desired import semantics (qualified access only)

## 9. Implementation Considerations

### 9.1 Affected Files

| File | Changes Required |
|------|------------------|
| `internal/checker/prelude.go` | Create global scope as base; classify declarations per rules |
| `internal/checker/infer_import.go` | Look up packages in registry; bind package identifier as sub-namespace in local scope |
| `internal/checker/scope.go` | Remove any package-specific logic; scopes now only handle parent chain |
| `internal/checker/checker.go` | Add package registry structure to store all loaded package namespaces |
| `internal/checker/infer_*.go` | Qualified access (e.g., `pkg.Symbol`) uses standard namespace member lookup |
| `internal/dts_parser/*.go` | Detect top-level exports to determine file classification |
| `internal/interop/*.go` | Convert declarations with correct namespace assignment |
| `internal/type_system/types.go` | May need to support sub-namespaces within namespaces |

### 9.2 Migration Path

1. Update `.d.ts` parser to detect top-level exports and track file classification
2. Implement `export = Namespace` expansion to top-level exports
3. Modify `loadTypeScriptModule()` to separate globals from packages based on classification rules
4. Refactor `Prelude()` to return a `globalScope` containing only global declarations
5. Create a separate package registry structure (at Checker level) to store package namespaces
6. Extend namespace support to allow sub-namespaces (for binding package identifiers)
7. Update `InferModule` to use three-level scope chain:
   - globalScope → moduleScope → fileScope (per file)
   - Process imports for each file, binding to that file's scope
   - Build unified DepGraph with file tracking for cross-file cyclic dependencies
8. Update import handling to:
   - Look up package in registry to get its complete namespace
   - Bind package identifier as a sub-namespace within the **file's** scope namespace
9. Qualified member access (`pkg.Symbol`) uses standard namespace member lookup
10. Add tests for:
    - Qualified access and local shadowing of globals
    - File-scoped imports (import in file A not visible in file B)
    - Cross-file cyclic dependencies with file-scoped imports

### 9.3 Edge Cases to Handle

1. **Circular imports between packages**: Each package should resolve against globals, not other packages being loaded
2. **Re-exports**: A package re-exporting from another package should expose the original package's type
3. **Type augmentation**: TypeScript's `declare global` should still augment globals, not create package-level bindings
4. **Qualified access**: Package symbols are accessed via qualified identifiers (e.g., `pkg.Array`), which uses standard namespace member lookup on the bound sub-namespace
5. **Sub-namespace support**: Namespaces must support nested sub-namespaces for binding package identifiers

## 10. Acceptance Criteria

### 10.1 Namespace Separation
1. [ ] Global types from `lib.es5.d.ts` and `lib.dom.d.ts` are in a separate namespace from package types
2. [ ] Package symbols are accessed via qualified identifiers (e.g., `pkg.Symbol`)
3. [ ] User code (local) declarations can shadow global declarations
4. [ ] Shadowed globals can be accessed via `globalThis` (e.g., `globalThis.Array`)
5. [ ] Globals and package symbols don't conflict (different access patterns)
6. [ ] Existing code that doesn't rely on shadowing continues to work

### 10.2 .d.ts File Classification
7. [ ] `.d.ts` files with top-level exports are correctly classified as packages
8. [ ] `.d.ts` files without exports correctly treat non-module declarations as globals
9. [ ] Named module declarations (`declare module "name"`) use module name string as identity
10. [ ] Mixed files (globals + named modules) are correctly partitioned
11. [ ] `export = Namespace` syntax is handled (namespace members expanded to top-level exports)

### 10.3 Cross-File Merging
11. [ ] Global declarations from multiple files merge into a single global namespace
12. [ ] Files resolving to the same `package.json` path merge into a single package namespace
13. [ ] Files resolving to different `package.json` paths have isolated namespaces (even with same `name` field in package.json)
14. [ ] Multiple `declare module "X"` blocks (same name) across files merge into one namespace
15. [ ] Nested `node_modules` with same package name but different paths create separate namespaces

### 10.4 .esc File Handling
16. [ ] `.esc` files in user project are classified as local/user code
17. [ ] `.esc` files in `libs/` folder are classified as packages
18. [ ] `.esc` files in `bin/` folder are classified as scripts
19. [ ] Scripts can reference exported library symbols
20. [ ] Library code referencing script symbols emits an error

### 10.5 Import Mechanics
21. [ ] Importing a package binds its namespace to a local identifier in the file scope
22. [ ] Package symbols are accessible only via qualified access (e.g., `pkg.symbol`)
23. [ ] Same package imported multiple times in the same file results in the same binding
24. [ ] Import bindings are file-scoped (each file must import packages it uses)
25. [ ] Import in file A is NOT visible to file B in the same module
26. [ ] Error emitted when accessing package namespace without import in that file

### 10.6 Global Augmentation
24. [ ] `declare global` blocks add declarations to global namespace (not package)
25. [ ] Global augmentations merge with existing globals

### 10.7 Error Handling
26. [ ] Error emitted when `package.json` not found for files with top-level exports
27. [ ] Error emitted when imported module cannot be resolved
28. [ ] Error messages indicate which namespaces were searched

### 10.8 Performance & Quality
29. [ ] Type resolution performance is not significantly degraded
30. [ ] Error messages clearly indicate which namespace a binding came from
31. [ ] Tests cover local-shadowing-global scenarios and qualified package access for both types and values

## 11. Open Questions

1. ~~Should there be a way to explicitly access the global namespace when shadowed by a local declaration?~~ **Resolved**: Use `globalThis.Array` to access shadowed globals.
2. ~~How should conflicting declarations within the same namespace be handled?~~ **Resolved**: Only functions and interfaces can have multiple declarations with the same name. Functions support overloading; interfaces support interface merging. Both are TypeScript features supported by Escalier for interoperability.
3. ~~Should package subpath imports (e.g., `import "lodash/array"`) create a separate package namespace or share with the root package?~~ **Resolved**: Subpath imports create separate package namespaces (e.g., `import "lodash/array"` is separate from `import "lodash"`).
4. ~~How should the package identifier be derived from the package name?~~ **Resolved**: Strip scope prefix and replace hyphens with underscores (e.g., `@scope/pkg-name` → `pkg_name`).
5. ~~Should there be syntax for aliasing a package identifier?~~ **Resolved**: Yes, use `import "lodash" as _` to alias a package identifier.
6. ~~Should there be a way to bulk-import symbols from a package?~~ **Resolved**: No. All symbols from `import "lodash"` are accessible via the `lodash` namespace (e.g., `lodash.map`, `lodash.filter`) within the file containing the import statement.
7. ~~How should TypeScript's `export =` syntax be handled?~~ **Resolved**: When a .d.ts file uses `export = Namespace`, treat it as equivalent to top-level exports by expanding the namespace members.
8. ~~Should package re-exports create aliases or copies?~~ **Resolved**: Aliases, to preserve type identity. Re-exports use explicit syntax: `export val map = lodash.map`.
9. ~~Should imports be module-scoped or file-scoped?~~ **Resolved**: File-scoped (like Go). Each `.esc` file must contain its own import statements for the packages it uses. A package namespace bound by an import in one file is NOT visible to other files in the same module.
10. How should ambient module declarations in .d.ts files be handled? (e.g., `declare module "*.css" { ... }`) - Need to investigate TypeScript semantics.

## 12. References

- Implementation plan: [implementation_plan.md](implementation_plan.md)
- Current implementation: [infer_module.go](../../internal/checker/infer_module.go)
- Prelude loading: [prelude.go](../../internal/checker/prelude.go)
- Import handling: [infer_import.go](../../internal/checker/infer_import.go)
- Scope structure: [scope.go](../../internal/checker/scope.go)
