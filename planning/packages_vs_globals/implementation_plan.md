# Implementation Plan: Separate Package and Global Namespaces

## 1. Overview

This document outlines the implementation plan for introducing separate package and global namespaces in Escalier, following the **recommended Option A** approach from [requirements.md](requirements.md).

### 1.1 Goals

- Separate global declarations (TypeScript builtins) from package declarations
- Allow local declarations to shadow globals
- Support qualified access to package symbols (e.g., `pkg.Symbol`)
- Maintain backward compatibility where possible
- Support proper `.d.ts` file classification (packages vs globals)

### 1.2 Key Design Decisions

**Scope Chain**: Local → Global (for unqualified lookups)
**Package Access**: Qualified only (via identifiers bound in local scope)
**Package Storage**: Separate registry at Checker level
**Import Mechanism**: Bind package namespace as sub-namespace in local namespace
**Import Scope**: File-scoped (like Go) - each file must import packages it uses

## 2. Architecture Overview

### 2.1 Current State

```
Single Namespace (merged)
├── Array (from lib.es5.d.ts)
├── Promise (from lib.es5.d.ts)
├── Element (from lib.dom.d.ts)
├── fetch (from package import)
└── MyType (user-defined)
```

All bindings exist in a flat namespace with no shadowing or isolation.

### 2.2 Target State

```
Package Registry (shared across all files, keyed by resolved .d.ts file path)
├── "/path/to/node_modules/lodash/index.d.ts" → { map, filter, ... }
├── "/path/to/node_modules/ramda/index.d.ts" → { map, filter, ... }
└── "/path/to/node_modules/@types/node/index.d.ts" → { ... }

Scope Chain (for modules - three levels):
    globalScope (Namespace: globals like Array, Promise, etc.)
        ↑ Parent
    moduleScope (Namespace: module-level declarations shared across files)
        ↑ Parent
    fileScope (Namespace: file-local import bindings)
        └── Package sub-namespaces (file-scoped, created by import statements):
            ├── "lodash" → points to registry entry
            └── "ramda" → points to registry entry

Scope Chain (for scripts - two levels):
    globalScope (Namespace: globals)
        ↑ Parent
    scriptScope (Namespace: local declarations + import bindings)
```

Note: For modules, each `.esc` file has its own file scope for imports, but declarations are added to the shared module scope. For scripts, there's a single scope for both.

**Unqualified lookup** (`Array`): Local → Global
**Qualified lookup** (`lodash.map`): Lookup `lodash` in Local (finds sub-namespace) → Member access on that namespace

### 2.3 Access Patterns

| Code | Lookup Path (for modules) |
|------|---------------------------|
| `Array` | fileScope → moduleScope → globalScope (finds globalScope.Array) |
| `MyType` | fileScope → moduleScope (finds moduleScope.MyType) |
| `lodash.map` | fileScope finds `lodash` sub-namespace → member access finds `map` |
| `globalThis.Array` | Special `globalThis` binding → member access on global namespace |

Note: For scripts, the lookup is simpler: scriptScope → globalScope.

## 3. Data Structure Changes

### 3.1 New Structures

```go
// Package registry - separate from scope chain
// Uses resolved .d.ts file paths as keys (not package names) to support
// monorepos where different projects may have different versions of the same package.
type PackageRegistry struct {
    packages map[string]*type_system.Namespace  // .d.ts file path → namespace
}

func NewPackageRegistry() *PackageRegistry {
    return &PackageRegistry{
        packages: make(map[string]*type_system.Namespace),
    }
}

func (pr *PackageRegistry) Register(dtsFilePath string, ns *type_system.Namespace) error {
    if dtsFilePath == "" {
        return fmt.Errorf("package file path cannot be empty")
    }
    if ns == nil {
        return fmt.Errorf("package namespace cannot be nil")
    }
    if _, exists := pr.packages[dtsFilePath]; exists {
        return fmt.Errorf("package at %q is already registered", dtsFilePath)
    }
    pr.packages[dtsFilePath] = ns
    return nil
}

func (pr *PackageRegistry) Lookup(dtsFilePath string) (*type_system.Namespace, bool) {
    ns, ok := pr.packages[dtsFilePath]
    return ns, ok
}
```

### 3.2 Modified Structures

```go
// Checker - add package registry
type Checker struct {
    // ... existing fields ...
    packageRegistry *PackageRegistry  // new
    globalScope     *Scope            // new: explicit reference to global scope
}
```

### 3.3 Existing Structures (for reference)

```go
// Scope - no changes needed
type Scope struct {
    Parent    *Scope
    Namespace *type_system.Namespace
}

// Namespace - no changes needed, already has Namespaces field
type Namespace struct {
    Values     map[string]*Binding
    Types      map[string]*TypeAlias
    Namespaces map[string]*Namespace  // used for binding package identifiers
}
```

### 3.4 File Classification Metadata

```go
// Track classification during parsing
type FileClassification struct {
    HasTopLevelExports bool
    NamedModules       []NamedModule
    GlobalDecls        []ast.Node
}

type NamedModule struct {
    Name  string      // e.g., "lodash"
    Decls []ast.Node
}
```

## 4. Implementation Overview

### 5.1 Infrastructure Setup
**Goal**: Create package registry and update core data structures

### 5.2 .d.ts Classification
**Goal**: Detect and classify .d.ts files (package vs global)

### 5.3 Global Namespace Separation
**Goal**: Isolate globals in their own namespace

### 5.4 Package Registry and Import Binding
**Goal**: Load packages into registry and bind as sub-namespaces

### 5.5 Local Shadowing and globalThis
**Goal**: Enable local shadowing of globals + `globalThis` access

### 5.6 Final Testing and Documentation
**Goal**: End-to-end integration tests, performance testing, and documentation

## 5. Detailed Implementation

### 5.1 Infrastructure Setup

#### 5.1.1 Create Package Registry
**File**: `internal/checker/package_registry.go` (new file)

```go
package checker

import (
    "fmt"

    "github.com/escalier-lang/escalier/internal/type_system"
)

type PackageRegistry struct {
    packages map[string]*type_system.Namespace
}

func NewPackageRegistry() *PackageRegistry {
    return &PackageRegistry{
        packages: make(map[string]*type_system.Namespace),
    }
}

func (pr *PackageRegistry) Register(dtsFilePath string, ns *type_system.Namespace) error {
    if dtsFilePath == "" {
        return fmt.Errorf("package file path cannot be empty")
    }
    if ns == nil {
        return fmt.Errorf("package namespace cannot be nil")
    }
    if _, exists := pr.packages[dtsFilePath]; exists {
        return fmt.Errorf("package at %q is already registered", dtsFilePath)
    }
    pr.packages[dtsFilePath] = ns
    return nil
}

func (pr *PackageRegistry) Lookup(dtsFilePath string) (*type_system.Namespace, bool) {
    ns, ok := pr.packages[dtsFilePath]
    return ns, ok
}

func (pr *PackageRegistry) MustLookup(dtsFilePath string) *type_system.Namespace {
    ns, ok := pr.packages[dtsFilePath]
    if !ok {
        panic(fmt.Sprintf("package at %q not found in registry", dtsFilePath))
    }
    return ns
}

func (pr *PackageRegistry) Has(dtsFilePath string) bool {
    _, ok := pr.packages[dtsFilePath]
    return ok
}
```

**Tasks**:
- [x] Create `package_registry.go` with basic structure
- [x] Implement `Register()` method with validation
- [x] Implement `Lookup()` and `MustLookup()` methods
- [x] Add unit tests for registry operations

#### 5.1.2 Update Checker Structure
**File**: `internal/checker/checker.go`

```go
type Checker struct {
    TypeVarID       int
    SymbolID        int
    Schema          *gqlast.Schema
    OverloadDecls   map[string][]*ast.FuncDecl // Tracks overloaded function declarations for codegen
    PackageRegistry *PackageRegistry           // Registry for package namespaces (separate from scope chain)
    GlobalScope     *Scope                     // Explicit reference to global scope (contains globals like Array, Promise, etc.)
}

func NewChecker() *Checker {
    return &Checker{
        TypeVarID:       0,
        SymbolID:        0,
        Schema:          nil,
        OverloadDecls:   make(map[string][]*ast.FuncDecl),
        PackageRegistry: NewPackageRegistry(),
        GlobalScope:     nil, // Will be set by initializeGlobalScope() during prelude loading
    }
}
```

**Tasks**:
- [x] Add `PackageRegistry` field to Checker
- [x] Add `GlobalScope` field to Checker
- [x] Update `NewChecker()` to initialize registry
- [x] Update all Checker constructors/factories

#### 5.1.3 Add Sub-Namespace Support
**File**: `internal/type_system/types.go`

```go
type Namespace struct {
    Values     map[string]*Binding
    Types      map[string]*TypeAlias
    Namespaces map[string]*Namespace  // already exists
}

// SetNamespace binds a sub-namespace to the given name in this namespace.
// Returns an error if the name conflicts with an existing type or value.
func (ns *Namespace) SetNamespace(name string, subNs *Namespace) error {
    if ns.Namespaces == nil {
        ns.Namespaces = make(map[string]*Namespace)
    }

    // Check for conflicts with types
    if _, exists := ns.Types[name]; exists {
        return fmt.Errorf("cannot bind sub-namespace %q: conflicts with existing type", name)
    }
    // Check for conflicts with values
    if _, exists := ns.Values[name]; exists {
        return fmt.Errorf("cannot bind sub-namespace %q: conflicts with existing value", name)
    }

    ns.Namespaces[name] = subNs
    return nil
}

// GetNamespace returns the sub-namespace bound to the given name.
// Returns (namespace, true) if found, or (nil, false) if not found.
func (ns *Namespace) GetNamespace(name string) (*Namespace, bool) {
    if ns.Namespaces == nil {
        return nil, false
    }
    subNs, ok := ns.Namespaces[name]
    return subNs, ok
}
```

**Tasks**:
- [x] Implement `SetNamespace()` method
- [x] Implement `GetNamespace()` method
- [x] Add conflict detection (sub-namespace vs type/value names)
- [x] Write unit tests for sub-namespace operations

#### 5.1.4 Update Import Statement AST
**File**: `internal/ast/stmt.go`

Renamed the `ModulePath` field to `PackageName` to better reflect that imports reference named npm packages, not arbitrary module paths.

```go
type ImportStmt struct {
    Specifiers  []*ImportSpecifier
    PackageName string // e.g., "lodash", "@types/node", "lodash/fp"
    span        Span
}

func NewImportStmt(specifiers []*ImportSpecifier, packageName string, span Span) *ImportStmt {
    return &ImportStmt{Specifiers: specifiers, PackageName: packageName, span: span}
}
```

**Tasks**:
- [x] Rename `ImportStmt.ModulePath` to `ImportStmt.PackageName`
- [x] Update all references to `ModulePath` in the codebase
- [x] Update parser to populate `PackageName` field (parser already uses constructor)
- [x] Update any serialization/deserialization code (snapshot tests updated)

#### 5.1.5 Infrastructure Setup Tests

**Unit Tests** (in `internal/checker/tests/package_registry_test.go`):
- [x] Package registry operations (register, lookup, duplicate handling)
- [x] Empty identity and nil namespace validation
- [x] Has() and MustLookup() methods
- [x] Multiple packages registration

**Unit Tests** (in `internal/type_system/namespace_test.go`):
- [x] Sub-namespace binding and lookup
- [x] Conflict detection with types and values
- [x] Nil Namespaces map handling
- [x] Multiple and nested sub-namespaces

---

### 5.2 .d.ts Classification

#### 5.2.1 Add Classification Detection
**File**: `internal/dts_parser/classifier.go` (new file)

```go
type FileClassification struct {
    HasTopLevelExports bool
    NamedModules       []NamedModuleDecl
    GlobalDecls        []ast.Node
    PackageDecls       []ast.Node  // if HasTopLevelExports
}

type NamedModuleDecl struct {
    ModuleName string
    Decls      []ast.Node
}

func ClassifyDTSFile(file *ast.File) *FileClassification {
    classifier := &FileClassification{
        NamedModules: make([]NamedModuleDecl, 0),
        GlobalDecls:  make([]ast.Node, 0),
        PackageDecls: make([]ast.Node, 0),
    }

    // First pass: detect top-level exports
    for _, stmt := range file.Statements {
        if isTopLevelExport(stmt) {
            classifier.HasTopLevelExports = true
            break
        }
    }

    // Second pass: classify declarations
    for _, stmt := range file.Statements {
        if namedModule := extractNamedModule(stmt); namedModule != nil {
            classifier.NamedModules = append(classifier.NamedModules, *namedModule)
        } else if classifier.HasTopLevelExports {
            classifier.PackageDecls = append(classifier.PackageDecls, stmt)
        } else {
            classifier.GlobalDecls = append(classifier.GlobalDecls, stmt)
        }
    }

    return classifier
}

func isTopLevelExport(stmt Statement) bool {
    // Check for export keyword at top level
    // Return true for: export interface, export type, export function, etc.
    // Also return true for: export = Namespace
}

func extractNamedModule(stmt Statement) *NamedModuleDecl {
    // Check if statement is "declare module "name" { ... }"
    // Returns ModuleDecl (for `declare module "name" { ... }`)
}

func extractGlobalAugmentation(stmt Statement) []Statement {
    // Check if statement is `declare global { ... }`
    // Returns GlobalDecl.Statements if found, nil otherwise
    // Note: GlobalDecl is a distinct AST type from NamespaceDecl,
    // so `declare global { ... }` is differentiated from `namespace global { ... }`
    if globalDecl, ok := stmt.(*GlobalDecl); ok {
        return globalDecl.Statements
    }
    return nil
}

func expandExportEquals(stmt Statement, module *Module) []Statement {
    // If statement is "export = Namespace", find the namespace declaration
    // and return its members as top-level exports.
    // Example: "export = Foo" where Foo is a namespace with {bar, baz}
    // becomes equivalent to "export const bar; export const baz"
    //
    // Note: ExportDecl has an ExportAssignment field to distinguish
    // `export = Foo` from `export { Foo }`. The isExportAssignment()
    // helper simply checks this field.
}
```

**Tasks**:
- [x] Create classifier.go with classification types
- [x] Implement `ClassifyDTSFile()` function
- [x] Implement `isTopLevelExport()` helper
- [x] Implement `extractNamedModule()` helper
- [x] Implement `extractGlobalAugmentation()` helper (checks for `GlobalDecl` AST type)
- [x] Implement `expandExportEquals()` helper to handle `export = Namespace` syntax
- [x] Add `ExportAssignment` field to `ExportDecl` to distinguish `export = Foo` from `export { Foo }`
- [x] Simplify `isExportAssignment()` to check `ExportAssignment` field
- [x] Add tests for various .d.ts file patterns:
  - [x] File with top-level exports
  - [x] File with no exports (all globals)
  - [x] File with named modules only
  - [x] Mixed file (globals + named modules)
  - [x] Edge case: file with re-exports
  - [x] Edge case: file with `export = Namespace` (expand to top-level exports)
- [x] Global augmentation (`declare global { ... }`)

#### 5.2.2 Package Identifier Derivation
**File**: `internal/dts_parser/package_identity.go` (new file)

Since the package registry uses resolved .d.ts file paths as keys (for monorepo support), and we always have the package name from the import specifier, we only need a function to derive a valid identifier from the module name.

```go
// DerivePackageIdentifier transforms a module/package name (from an import specifier)
// into a valid identifier that can be used as a binding name in Escalier code.
func DerivePackageIdentifier(moduleName string) string {
    // Strip scope prefix (@scope/pkg → pkg)
    name := moduleName
    if strings.HasPrefix(name, "@") {
        parts := strings.SplitN(name, "/", 2)
        if len(parts) == 2 {
            name = parts[1]
        }
    }

    // Replace forward slashes with underscores (for subpath exports)
    name = strings.ReplaceAll(name, "/", "_")

    // Replace hyphens with underscores
    name = strings.ReplaceAll(name, "-", "_")

    // Replace dots with underscores
    name = strings.ReplaceAll(name, ".", "_")

    return name
}
```

**Note**: `ResolvePackageIdentity()` was removed because:
1. The package registry uses file paths as keys (not package names)
2. We always have the package name from the import specifier (e.g., `import "lodash"`)
3. There's no need to traverse the filesystem to find package.json

**Tasks**:
- [x] Implement `DerivePackageIdentifier()` function
- [x] Handle scoped packages (@scope/pkg → pkg)
- [x] Handle subpath exports (lodash/fp → lodash_fp)
- [x] Handle hyphens and dots
- [x] Write tests for identifier derivation

#### 5.2.3 .d.ts Classification Tests

**File Classification Tests**:
- [x] .d.ts files with top-level exports
- [x] .d.ts files without exports (all globals)
- [x] .d.ts files with named modules only
- [x] Mixed .d.ts files (globals + named modules)
- [x] Edge case: file with re-exports
- [x] Edge case: file with `export = Namespace` (should expand namespace members to top-level exports)
- [x] Global augmentation (`declare global { ... }`)

**Package Identifier Tests**:
- [x] Package identifier derivation (scoped packages, hyphens, dots, subpaths)

#### 5.2.4 Update Module Loader
**File**: `internal/interop/load_typescript_module.go` (modify existing)

```go
func LoadTypeScriptModule(filePath string) (*LoadedModule, error) {
    // Parse .d.ts file
    file, err := parser.Parse(filePath)
    if err != nil {
        return nil, err
    }

    // Classify file
    classification := classifier.ClassifyDTSFile(file)

    result := &LoadedModule{
        GlobalDecls: classification.GlobalDecls,
        Packages:    make(map[string]*PackageDecls),
    }

    // Handle top-level package (if any)
    if classification.HasTopLevelExports {
        identity, err := ResolvePackageIdentity(filePath)
        if err != nil {
            return nil, err
        }
        result.Packages[identity] = &PackageDecls{
            Decls: classification.PackageDecls,
        }
    }

    // Handle named modules
    for _, namedModule := range classification.NamedModules {
        result.Packages[namedModule.ModuleName] = &PackageDecls{
            Decls: namedModule.Decls,
        }
    }

    return result, nil
}

type LoadedModule struct {
    GlobalDecls []ast.Node
    Packages    map[string]*PackageDecls  // identity → declarations
}

type PackageDecls struct {
    Decls []ast.Node
}
```

**Tasks**:
- [ ] Update return type to include classification
- [ ] Integrate classifier into loader
- [ ] Separate globals from package declarations
- [ ] Update all callers of `LoadTypeScriptModule()`
- [ ] Add integration tests

---

### 5.3 Global Namespace Separation

#### 5.3.1 Refactor Prelude Loading
**File**: `internal/checker/prelude.go`

Current approach likely looks like:
```go
func (c *Checker) Prelude() error {
    // Load lib.es5.d.ts
    // Load lib.dom.d.ts
    // All declarations go into c.scope
}
```

New approach:
```go
func (c *Checker) initializeGlobalScope() *Scope {
    globalNs := type_system.NewNamespace()
    globalScope := &Scope{
        Parent:    nil,  // Global scope has no parent
        Namespace: globalNs,
    }

    // Load global type definitions
    if err := c.loadGlobalDefinitions(globalScope); err != nil {
        panic(fmt.Sprintf("Failed to load global definitions: %v", err))
    }

    return globalScope
}

func (c *Checker) loadGlobalDefinitions(globalScope *Scope) error {
    // Load lib.es5.d.ts
    if err := c.loadGlobalFile("lib.es5.d.ts", globalScope); err != nil {
        return err
    }

    // Load lib.dom.d.ts
    if err := c.loadGlobalFile("lib.dom.d.ts", globalScope); err != nil {
        return err
    }

    return nil
}

func (c *Checker) loadGlobalFile(filename string, globalScope *Scope) error {
    filePath := c.resolveTypeScriptLib(filename)

    // Load and classify
    loadedModule, err := LoadTypeScriptModule(filePath)
    if err != nil {
        return err
    }

    // Build ast.Module from global declarations and infer
    if len(loadedModule.GlobalDecls) > 0 {
        globalModule := &ast.Module{Decls: loadedModule.GlobalDecls}
        depGraph := dep_graph.BuildDepGraph(globalModule)
        ctx := Context{Scope: globalScope}
        if errs := c.InferDepGraph(ctx, depGraph); len(errs) > 0 {
            return errs[0]  // or collect all errors
        }
    }

    // Handle packages (named modules) - register in package registry
    for dtsFilePath, pkgDecls := range loadedModule.Packages {
        pkgNs := type_system.NewNamespace()
        pkgScope := &Scope{
            Parent:    globalScope,  // Packages can reference globals
            Namespace: pkgNs,
        }

        pkgModule := &ast.Module{Decls: pkgDecls.Decls}
        depGraph := dep_graph.BuildDepGraph(pkgModule)
        ctx := Context{Scope: pkgScope}
        if errs := c.InferDepGraph(ctx, depGraph); len(errs) > 0 {
            return errs[0]  // or collect all errors
        }

        c.packageRegistry.Register(dtsFilePath, pkgNs)
    }

    return nil
}
```

**Tasks**:
- [ ] Create `initializeGlobalScope()` method
- [ ] Move prelude loading to use global scope
- [ ] Update `loadGlobalDefinitions()` to separate globals from packages
- [ ] Ensure named modules in lib files go to package registry
- [ ] Update Checker initialization to call `initializeGlobalScope()`
- [ ] Verify global scope is available to all user code scopes
- [ ] Test that globals are isolated

#### 5.3.2 Update InferModule and InferScript
**Files**: `internal/checker/infer_module.go`, `internal/checker/infer_script.go`

Escalier distinguishes between **modules** and **scripts**:
- A **module** is comprised of multiple files and directories where each directory corresponds to a namespace and each namespace contains all declarations from the files in that directory
- A **script** is a single `.esc` file

**Import Scoping**: Imports are file-scoped (similar to Go). Each `.esc` file must contain its own import statements for the packages it uses. A package namespace bound by an import in one file is NOT visible to other files in the same module. If a file attempts to access a package namespace without the corresponding import statement, an error should be reported.

Both `InferModule` and `InferScript` already exist. However, `InferModule` needs changes to support file-scoped imports while still sharing module-level declarations across files.

**Current `InferModule`** (NEEDS CHANGES for file-scoped imports):
```go
// CURRENT - processes entire module with single scope (imports leak across files)
func (c *Checker) InferModule(ctx Context, m *ast.Module) []Error {
    depGraph := dep_graph.BuildDepGraph(m)
    return c.InferDepGraph(ctx, depGraph)
}
```

**Problem**: The current approach builds a single DepGraph for all files and processes it with one scope. This means an import in file A would be visible in file B, violating file-scoped import semantics.

**Challenge**: We still need a unified DepGraph across all files to handle cross-file cyclic dependencies:
```escalier
// file_a.esc
type Foo = { bar: Bar }  // References Bar from file_b

// file_b.esc
type Bar = { foo: Foo }  // References Foo from file_a
```

Per-file dep graphs would fail here because when processing `file_a.esc`, `Bar` hasn't been defined yet.

**Solution**: Track file provenance in the DepGraph and use file-specific scopes for imports:

```go
// Hybrid approach: Unified DepGraph + file-scoped imports
func (c *Checker) InferModule(ctx Context, m *ast.Module) []Error {
    errors := []Error{}

    // Shared namespace for module-level declarations (types, functions, values)
    moduleNs := ctx.Scope.Namespace

    // Phase 1: Process imports for each file, creating file-scoped bindings
    fileScopes := make(map[string]*Scope)  // filename → file scope
    for _, file := range m.Files {
        fileNs := type_system.NewNamespace()
        fileScope := &Scope{
            Parent:    ctx.Scope,  // Parent is module scope (global as grandparent)
            Namespace: fileNs,
        }
        fileScopes[file.Path] = fileScope

        // Process import statements for this file
        for _, stmt := range file.Imports {
            fileCtx := ctx.WithScope(fileScope)
            importErrors := c.inferImportStatement(fileCtx, stmt)
            errors = append(errors, importErrors...)
        }
    }

    // Phase 2: Build unified DepGraph for ALL declarations across all files
    // The DepGraph tracks which file each declaration came from
    depGraph := dep_graph.BuildDepGraphWithFileTracking(m)

    // Phase 3: Infer declarations using the unified DepGraph
    // When inferring a declaration, use the file scope for that declaration's file
    // This ensures imports are file-scoped while declarations can reference
    // each other across files (including cycles)
    declErrors := c.InferDepGraphWithFileScopes(ctx, depGraph, moduleNs, fileScopes)
    errors = append(errors, declErrors...)

    return errors
}
```

**Key insight**:
- **Import bindings** → file scope (not visible to other files)
- **Module declarations** (types, functions, values) → shared module namespace (visible across files in same directory)
- **Unified DepGraph** → required for cross-file cyclic dependencies
- **File tracking** → when inferring a declaration, use that declaration's file scope for looking up imports

**BuildDepGraphWithFileTracking Implementation Sketch**:

```go
// DepNode represents a declaration in the dependency graph
type DepNode struct {
    Name     string       // Declaration name (e.g., "Foo", "myFunc")
    Decl     ast.Decl     // The AST node for this declaration
    FilePath string       // Source file path - NEW: tracks which file this came from
    Deps     []string     // Names of declarations this depends on
}

// DepGraph represents the dependency graph with file tracking
type DepGraph struct {
    Nodes    map[string]*DepNode  // name → node
    SCCs     [][]*DepNode         // Strongly connected components (for cycle handling)
}

// BuildDepGraphWithFileTracking builds a unified dep graph across all files
// while tracking which file each declaration originated from
func BuildDepGraphWithFileTracking(m *ast.Module) *DepGraph {
    graph := &DepGraph{
        Nodes: make(map[string]*DepNode),
    }

    // Phase 1: Collect all declarations from all files
    for _, file := range m.Files {
        for _, decl := range file.Decls {
            name := getDeclName(decl)
            node := &DepNode{
                Name:     name,
                Decl:     decl,
                FilePath: file.Path,  // Track source file
                Deps:     []string{},
            }
            graph.Nodes[name] = node
        }
    }

    // Phase 2: Analyze dependencies (references to other declarations)
    for _, node := range graph.Nodes {
        // Find all identifiers referenced in this declaration
        refs := findReferencedIdentifiers(node.Decl)
        for _, ref := range refs {
            // Only add as dependency if it's another declaration in the graph
            // (not an import or builtin)
            if _, exists := graph.Nodes[ref]; exists {
                node.Deps = append(node.Deps, ref)
            }
        }
    }

    // Phase 3: Compute SCCs (strongly connected components) for cycle handling
    // Declarations in the same SCC must be inferred together
    graph.SCCs = computeSCCs(graph.Nodes)

    return graph
}

// InferDepGraphWithFileScopes infers declarations using file-specific scopes
func (c *Checker) InferDepGraphWithFileScopes(
    ctx Context,
    depGraph *DepGraph,
    moduleNs *type_system.Namespace,
    fileScopes map[string]*Scope,
) []Error {
    errors := []Error{}

    // Process SCCs in topological order
    for _, scc := range depGraph.SCCs {
        if len(scc) == 1 {
            // Single declaration (no cycle)
            node := scc[0]
            fileScope := fileScopes[node.FilePath]
            fileCtx := ctx.WithScope(fileScope)

            // Infer declaration - imports resolved via fileScope,
            // but declaration is added to moduleNs
            declErrors := c.inferDeclWithTargetNs(fileCtx, node.Decl, moduleNs)
            errors = append(errors, declErrors...)
        } else {
            // Mutually recursive declarations (cycle)
            // All declarations in the SCC are inferred together
            // Each uses its own file scope for import resolution
            declErrors := c.inferMutuallyRecursiveDecls(ctx, scc, moduleNs, fileScopes)
            errors = append(errors, declErrors...)
        }
    }

    return errors
}

// inferMutuallyRecursiveDecls handles a group of mutually recursive declarations
func (c *Checker) inferMutuallyRecursiveDecls(
    ctx Context,
    scc []*DepNode,
    moduleNs *type_system.Namespace,
    fileScopes map[string]*Scope,
) []Error {
    // Step 1: Create placeholder types for all declarations in the SCC
    for _, node := range scc {
        placeholder := createPlaceholderType(node.Decl)
        moduleNs.Types[node.Name] = placeholder
    }

    // Step 2: Infer each declaration, using its file scope for imports
    errors := []Error{}
    for _, node := range scc {
        fileScope := fileScopes[node.FilePath]
        fileCtx := ctx.WithScope(fileScope)

        declErrors := c.inferDeclWithTargetNs(fileCtx, node.Decl, moduleNs)
        errors = append(errors, declErrors...)
    }

    return errors
}
```

**Current `InferScript`** (no changes needed to the function itself):
```go
func (c *Checker) InferScript(ctx Context, m *ast.Script) (*Scope, []Error) {
    errors := []Error{}
    ctx = ctx.WithNewScope()

    for _, stmt := range m.Stmts {
        stmtErrors := c.inferStmt(ctx, stmt)
        errors = slices.Concat(errors, stmtErrors)
    }

    return ctx.Scope, errors
}
```

**Changes needed at the call site** (e.g., in compiler or main entry point):

The caller must create a context with a scope that has the global scope as its parent:

```go
// Create user scope with global scope as parent
userNs := type_system.NewNamespace()
userScope := &Scope{
    Parent:    c.globalScope,  // Link to global scope
    Namespace: userNs,
}

// Create context with user scope
ctx := Context{Scope: userScope}

// For modules:
errors := c.InferModule(ctx, module)

// For scripts:
scope, errors := c.InferScript(ctx, script)
```

**Tasks**:
- [ ] Update call sites of `InferModule` to pass context with global scope parent
- [ ] Update call sites of `InferScript` to pass context with global scope parent
- [ ] Verify parent chain works correctly (user scope → global scope)
- [ ] Test that user code can access globals via parent chain lookup
- [ ] Test that lookup traverses parent chain
- [ ] Modify `InferModule` to use hybrid approach (unified DepGraph + file-scoped imports)
- [ ] Implement `BuildDepGraphWithFileTracking` to track which file each declaration comes from
- [ ] Implement `InferDepGraphWithFileScopes` to use file-specific scopes when inferring declarations
- [ ] Implement `inferDeclWithTargetNs` to infer a declaration using one scope but writing to another namespace
- [ ] Implement `inferMutuallyRecursiveDecls` for handling SCCs with file-scoped imports
- [ ] Process imports in Phase 1 (before building unified DepGraph)
- [ ] Ensure module-level declarations are visible across files in same directory
- [ ] Ensure import bindings are NOT visible across files
- [ ] Ensure cross-file cyclic dependencies work correctly

#### 5.3.3 Global Namespace Separation Tests

**Unit Tests**:
- [ ] Scope chain traversal (Local → Global)
- [ ] Global namespace isolation

**Integration Tests**:
- [ ] Load lib.es5.d.ts (globals)
- [ ] Load lib.dom.d.ts (globals)
- [ ] User code can access globals via parent chain lookup

**File Scope vs Module Namespace Tests**:
- [ ] Module declarations (types, functions) visible across files in same directory
- [ ] Import bindings NOT visible across files (file-scoped)
- [ ] File A declares type T, file B can use type T (shared module namespace)
- [ ] File A imports "lodash", file B cannot use `lodash.map` (file-scoped imports)
- [ ] Cross-file cyclic dependencies: file A has type Foo referencing Bar, file B has type Bar referencing Foo
- [ ] Cross-file cycles with imports: file A imports "lodash" and declares type using lodash types, file B references that type

---

### 5.4 Package Registry and Import Binding

#### 5.4.1 Load Packages into Registry
**File**: `internal/checker/infer_import.go`

Current approach (likely):
```go
func (c *Checker) inferImportStatement(stmt *ast.ImportStmt) error {
    // Resolve module path
    // Load .d.ts file
    // Merge declarations into current scope
}
```

New approach:
```go
// inferImportStatement takes a context with the file scope
// The import binding is added to ctx.Scope.Namespace (the file's namespace)
func (c *Checker) inferImportStatement(ctx Context, stmt *ast.ImportStmt) error {
    packageName := stmt.PackageName  // e.g., "lodash", "@types/node", "lodash/fp"

    // Derive package identifier for binding (used as the local name in user code)
    packageIdent := stmt.Alias
    if packageIdent == "" {
        packageIdent = DerivePackageIdentifier(packageName)
    }

    // Load package and get its namespace directly
    // The file path is used as the registry key (for monorepo support)
    pkgNs, err := c.loadPackageForImport(packageName)
    if err != nil {
        return err
    }

    // Bind package namespace as sub-namespace in current FILE's scope.
    // This binding is file-scoped (like Go): other files in the same module
    // must have their own import statements to access this package.
    // ctx.Scope is the file scope, so this binding is file-scoped
    fileNs := ctx.Scope.Namespace
    if err := fileNs.SetNamespace(packageIdent, pkgNs); err != nil {
        return err
    }

    return nil
}

// loadPackageForImport loads a package and returns the namespace
// that corresponds to the given package name.
// The registry uses file paths as keys to support monorepos with different
// versions of the same package in different project directories.
func (c *Checker) loadPackageForImport(packageName string) (*type_system.Namespace, error) {
    // Resolve to file path via Node module resolution
    filePath, err := c.resolvePackage(packageName)
    if err != nil {
        return nil, fmt.Errorf("cannot resolve package %q: %w", packageName, err)
    }

    // Check if already registered using file path as key (handles re-imports)
    if pkgNs, exists := c.packageRegistry.Lookup(filePath); exists {
        return pkgNs, nil
    }

    // Load and classify the .d.ts file
    module, err := LoadTypeScriptModule(filePath)
    if err != nil {
        return nil, err
    }

    // Create package namespace from the module's declarations
    pkgNs := type_system.NewNamespace()
    pkgScope := &Scope{
        Parent:    c.globalScope,  // Packages can reference globals
        Namespace: pkgNs,
    }

    // Build dep graph and infer package declarations
    // For files with top-level exports, use PackageDecls
    // For named modules, we'd need to match the packageName to find the right one
    var decls []ast.Node
    if classification := module.Classification; classification.HasTopLevelExports {
        decls = classification.PackageDecls
    } else {
        // For named modules, find the matching module
        for _, nm := range classification.NamedModules {
            if nm.ModuleName == packageName {
                decls = nm.Decls
                break
            }
        }
    }

    if len(decls) > 0 {
        pkgModule := &ast.Module{Decls: decls}
        depGraph := dep_graph.BuildDepGraph(pkgModule)
        ctx := Context{Scope: pkgScope}
        if errs := c.InferDepGraph(ctx, depGraph); len(errs) > 0 {
            return nil, errs[0]
        }
    }

    // Register in package registry using file path as key
    c.packageRegistry.Register(filePath, pkgNs)

    // Handle global augmentations if any
    if len(module.GlobalDecls) > 0 {
        globalModule := &ast.Module{Decls: module.GlobalDecls}
        globalDepGraph := dep_graph.BuildDepGraph(globalModule)
        globalCtx := Context{Scope: c.globalScope}
        if errs := c.InferDepGraph(globalCtx, globalDepGraph); len(errs) > 0 {
            return nil, errs[0]
        }
    }

    return pkgNs, nil
}
```

**Note on multi-file packages**: Some packages like `@types/node` span multiple .d.ts files. The current `loadPackageForImport()` sketch assumes single-file packages. For multi-file packages, we may need to:
- Load all .d.ts files in the package
- Merge their declarations into a single package namespace
- This is a simplification for now; full support for multi-file packages can be added later

**Tasks**:
- [ ] Implement new `inferImportStatement()` logic
- [ ] Implement `loadPackageForImport()` method (uses file path as registry key)
- [x] Implement `DerivePackageIdentifier()` (completed in 5.2)
- [ ] Handle import aliases (`import "pkg" as alias`)
- [ ] Handle re-imports (no-op if already loaded - checked by file path)
- [ ] Add caching to avoid re-loading same package
- [ ] Consider multi-file package support (e.g., @types/node)
- [ ] Test various import scenarios:
  - [ ] Simple import
  - [ ] Import with alias
  - [ ] Multiple imports
  - [ ] Circular imports (edge case)
  - [ ] Monorepo: same package at different paths (should be separate registry entries)

#### 5.4.2 Handle Subpath Imports
**File**: `internal/checker/infer_import.go`

Subpath imports (e.g., `import "lodash/fp"`) are handled naturally by `LoadTypeScriptModule`:
- When loading `lodash/fp`, it resolves to a different `.d.ts` file than `lodash`
- If that file has top-level exports, `ResolvePackageIdentity` returns the package name (e.g., `"lodash"` if both are in the same package)
- If that file uses `declare module "lodash/fp"`, the module name string `"lodash/fp"` becomes the identity
- Each distinct identity gets its own entry in the package registry

**Tasks**:
- [ ] Ensure subpath imports create separate registry entries when they resolve to different packages
- [ ] Test that `import "lodash"` and `import "lodash/fp"` work correctly (may be same or different packages depending on .d.ts structure)
- [ ] Document subpath import behavior

#### 5.4.3 Package Registry and Import Binding Tests

**Unit Tests**:
- [ ] Package namespace isolation
- [ ] Simple imports
- [ ] Aliased imports
- [ ] Re-imports (no-op)
- [ ] Qualified access (`pkg.symbol`)
- [ ] Nested qualified access
- [ ] Error: undefined package member
- [ ] Error: bare package identifier used
- [ ] Subpath imports create separate registry entries

**File-Scoped Import Tests**:
- [ ] Import in file A is not visible in file B (same module)
- [ ] Each file must have its own import statement for packages it uses
- [ ] Error: accessing package namespace without import statement in that file
- [ ] Same package imported in multiple files works correctly (shared registry entry, separate bindings)

**Integration Tests**:
- [ ] Load globals, import package, use both in user code
- [ ] Multiple packages with same symbol name (no conflict)
- [ ] Nested `node_modules` with same package name (separate namespaces)
- [ ] Load @types/node (package with named modules)
- [ ] Load lodash (package with top-level exports)

**Edge Cases**:
- [ ] Circular dependencies (Package A imports B, which imports A)
- [ ] Package re-exports (preserve type identity)
- [ ] Package identity edge cases (nested node_modules, symlinked packages, monorepo packages)

---

### 5.5 Local Shadowing and globalThis

#### 5.5.1 Implement Shadowing via Parent Chain
This should already work if the scope chain is set up correctly (Local → Global). Verify with tests.

**File**: `internal/checker/scope.go`

```go
func (s *Scope) Lookup(name string) (*Type, error) {
    // Check current namespace
    if typ, ok := s.Namespace.Types[name]; ok {
        return typ, nil
    }
    if val, ok := s.Namespace.Values[name]; ok {
        return val, nil
    }

    // Check parent scope
    if s.Parent != nil {
        return s.Parent.Lookup(name)
    }

    return nil, fmt.Errorf("undefined: %s", name)
}
```

**Tasks**:
- [ ] Verify `Lookup()` traverses parent chain correctly
- [ ] Test local shadowing of globals
- [ ] Test that shadowed globals are not accessible via unqualified names

#### 5.5.2 Implement globalThis
**File**: `internal/checker/prelude.go` or `checker.go`

Add a special `globalThis` binding that references the global namespace:

```go
func (c *Checker) initializeGlobalScope() *Scope {
    // ... existing code ...

    // Add globalThis as a special namespace binding
    globalThisType := &Type{
        Kind: NamespaceType,
        Namespace: globalNs,
    }
    globalNs.Values["globalThis"] = globalThisType

    return globalScope
}
```

**File**: `internal/checker/infer_expr.go`

The `ast.MemberExpr` case is handled inside `inferExpr` in a switch-case statement. Update this case to handle `globalThis.member` access:

```go
func (c *Checker) inferExpr(ctx Context, expr ast.Expr) (type_system.Type, []Error) {
    // ... existing code ...

    switch expr := expr.(type) {
    // ... other cases ...

    case *ast.MemberExpr:
        // Special handling for globalThis
        if ident, ok := expr.Object.(*ast.Identifier); ok && ident.Name == "globalThis" {
            // Access member on global namespace
            key := PropertyKey{Name: expr.Prop.Name, OptChain: expr.OptChain, span: expr.Prop.Span()}
            return c.getMemberType(ctx, c.globalScope.Namespace, key)
        }

        // Normal member access
        objType, objErrors := c.inferExpr(ctx, expr.Object)
        key := PropertyKey{Name: expr.Prop.Name, OptChain: expr.OptChain, span: expr.Prop.Span()}
        propType, propErrors := c.getMemberType(ctx, objType, key)
        // ... rest of existing code ...

    // ... other cases ...
    }
}
```

**Tasks**:
- [ ] Add `globalThis` binding to global namespace
- [ ] Create NamespaceType kind (or similar mechanism)
- [ ] Update `ast.MemberExpr` case in `inferExpr` to handle `globalThis.member` access
- [ ] Test `globalThis` access to shadowed globals
- [ ] Document `globalThis` behavior

#### 5.5.3 Local Shadowing and globalThis Tests

**Unit Tests**:
- [ ] Local shadowing of globals
- [ ] Shadowed globals are not accessible via unqualified names
- [ ] `globalThis` access to global namespace

**Integration Tests**:
- [ ] Local type shadows global, access global via `globalThis`
- [ ] Mixed file with `declare global` augmentation (augmentations go to global namespace)

---

### 5.6 Final Testing and Documentation

#### 5.6.1 End-to-End Integration Tests

Tests that exercise the complete system with all features working together:

- [ ] Full workflow: load globals → import multiple packages → define local types that shadow globals → access shadowed globals via `globalThis` → use qualified package access
- [ ] Complex project simulation with multiple .d.ts files (globals, packages, mixed)

#### 5.6.2 Performance Testing

- [ ] Measure type resolution time before/after changes
- [ ] Ensure no significant regression (< 10% slowdown acceptable)
- [ ] Profile hot paths if performance degrades

#### 5.6.3 Documentation

- [ ] Update user-facing docs on import syntax
- [ ] Document `globalThis` usage
- [ ] Add migration guide for breaking changes
- [ ] Update API documentation

---

## 6. Testing Strategy

### 6.1 Test Structure

```
internal/checker/
├── package_registry_test.go
├── namespace_test.go
├── shadowing_test.go
├── import_test.go
└── testdata/
    ├── globals/
    │   ├── lib.es5.d.ts
    │   └── lib.dom.d.ts
    └── packages/
        ├── lodash.d.ts
        ├── ramda.d.ts
        ├── mixed.d.ts
        └── export_equals.d.ts  // Tests export = Namespace pattern
```

### 6.2 Test Scenarios

#### Scenario 1: Basic Shadowing
```escalier
// user.esc
type Array<T> = { items: T[] }

let arr: Array<string>  // Should resolve to local Array
let globalArr: globalThis.Array<string>  // Should resolve to global Array
```

#### Scenario 2: Package Isolation
```escalier
// user.esc
import "lodash"
import "ramda"

let a = lodash.map([1, 2], fn(x) { x * 2 })   // lodash.map
let b = ramda.map(fn(x) { x * 2 }, [1, 2])    // ramda.map (different signature)
```

#### Scenario 3: Global Augmentation
```typescript
// pkg.d.ts
declare module "my-pkg" {
    export function myFunc(): void;
}

declare global {
    interface Window {
        myProperty: string;
    }
}
```

User code should access `Window` as global, and `myFunc` via `my_pkg.myFunc`.

#### Scenario 4: Export Equals Pattern
```typescript
// export_equals.d.ts
declare namespace Foo {
    export const bar: number;
    export function baz(): string;
}
export = Foo;
```

This should be treated as equivalent to:
```typescript
export const bar: number;
export function baz(): string;
```

User code imports this as `import "export_equals"` and accesses via `export_equals.bar` and `export_equals.baz`.

#### Scenario 5: File-Scoped Imports
```escalier
// lib/file_a.esc
import "lodash"

val doubled = lodash.map([1, 2, 3], fn(x) { x * 2 })  // OK: lodash imported in this file
```

```escalier
// lib/file_b.esc
// No import statement for lodash

val result = lodash.map([1, 2], fn(x) { x + 1 })  // ERROR: 'lodash' is not defined
```

Each file must have its own import statements. The import in `file_a.esc` does not make `lodash` available in `file_b.esc`.

#### Scenario 6: Cross-File Cyclic Dependencies
```escalier
// lib/node.esc
import "lodash"

type Node = {
    value: number,
    children: Tree,  // References Tree from tree.esc
}

val createNode = fn(v: number): Node { { value: v, children: lodash.empty() } }
```

```escalier
// lib/tree.esc
type Tree = {
    root: Node,  // References Node from node.esc
    size: number,
}
```

This should work correctly:
- `Node` and `Tree` can reference each other (unified DepGraph handles the cycle)
- `lodash` is only accessible in `node.esc` (file-scoped import)
- `tree.esc` cannot use `lodash` without its own import

---

## 7. Migration and Backward Compatibility

### 7.1 Breaking Changes

1. **Package symbols require qualified access**:
   - Before: `import "lodash"` made `map` available directly
   - After: Must use `lodash.map`
   - Migration: Update user code to use qualified access

2. **Import statement syntax**:
   - Ensure `import "pkg"` binds to an identifier
   - May need to add explicit alias if package name is unusual

### 7.2 Compatibility Mode

Consider adding a compatibility flag for gradual migration:

```go
type CheckerOptions struct {
    LegacyImports bool  // If true, merge package symbols into local scope
}
```

This allows users to opt-in to new behavior gradually.

### 7.3 Migration Guide

Create a guide covering:
- How to update import statements
- How to use qualified access (e.g., `lodash.map` instead of `map`)
- How to re-export package symbols (e.g., `export val map = lodash.map`)
- How to handle shadowing conflicts
- How to access globals via `globalThis`

---

## 8. Success Criteria

### 8.1 Functional Requirements

- [ ] Global namespace is isolated from package namespaces
- [ ] Package symbols are accessed via qualified identifiers
- [ ] Local declarations can shadow globals
- [ ] `globalThis` provides access to shadowed globals
- [ ] .d.ts files are correctly classified
- [ ] `export = Namespace` syntax is handled (expanded to top-level exports)
- [ ] Imports are file-scoped (each file must import packages it uses)
- [ ] Error reported when accessing package namespace without import in that file

### 8.2 Quality Requirements

- [ ] All unit tests pass
- [ ] All integration tests pass
- [ ] No significant performance regression (< 10%)
- [ ] Error messages are clear and helpful
- [ ] Documentation is complete and accurate

### 8.3 Acceptance Criteria

From requirements.md section 10:

1. ✅ Global types from `lib.es5.d.ts` and `lib.dom.d.ts` are in a separate namespace
2. ✅ Package symbols are accessed via qualified identifiers
3. ✅ User code declarations can shadow globals
4. ✅ Shadowed globals accessible via `globalThis`
5. ✅ .d.ts file classification works correctly
6. ✅ Cross-file namespace merging works correctly
7. ✅ Import mechanics work as specified
8. ✅ All tests pass

---

## 9. Risk Assessment

### High Risk Areas

1. **Import Mechanism Changes**: Core feature affecting all imports
   - Mitigation: Comprehensive testing, gradual rollout

2. **Breaking Changes**: Existing code may break
   - Mitigation: Compatibility mode, migration guide

3. **Performance Regression**: Additional lookups may slow down type checking
   - Mitigation: Profile and optimize, caching

### Medium Risk Areas

1. **.d.ts Classification**: Complex parsing logic
   - Mitigation: Extensive test coverage

2. **Circular Dependencies**: Edge case that may cause issues
   - Mitigation: Explicit handling, tests

3. **DepGraph/InferModule Changes**: Modifying how modules are processed to support file-scoped imports while sharing module declarations
   - Mitigation: Careful design of scope hierarchy, thorough testing of file vs module scope boundaries

### Low Risk Areas

1. **Package Registry**: Simple data structure
2. **Scope Chain**: Standard parent-child relationship

---

## 10. Milestones

### Milestone 1: Infrastructure
- [x] Complete 5.1: Package registry and data structures
- [x] Complete 5.2: .d.ts classification

### Milestone 2: Core Features
- [ ] Complete 5.3: Global namespace separation
- [ ] Complete 5.4: Package registry and imports

### Milestone 3: Advanced Features and Testing
- [ ] Complete 5.5: Shadowing and globalThis
- [ ] Complete 5.6: Testing and edge cases
- [ ] Documentation
- [ ] Migration guide
- [ ] Release

---

## 11. Open Questions

1. **Should there be a way to bulk-import symbols from a package?**
   - No. All symbols from `import "lodash"` are accessible via the `lodash` namespace (e.g., `lodash.map`, `lodash.filter`) within the file containing the import statement.

2. **How should TypeScript's `export =` syntax be handled?**
   - When a .d.ts file uses `export = Namespace`, treat it as equivalent to top-level exports:
     ```typescript
     // This:
     namespace Foo {
       export const bar = ...
       export const baz = ...
     }
     export = Foo

     // Is equivalent to:
     export const bar = ...
     export const baz = ...
     ```

3. **Should package re-exports create aliases or copies?**
   - Aliases, to preserve type identity. Re-exports use explicit syntax:
     ```escalier
     import "lodash"

     // re-export of `map` from "lodash"
     export val map = lodash.map
     ```

4. **How should ambient module declarations in .d.ts files be handled?**
   - Example: `declare module "*.css" { ... }`
   - Need to investigate TypeScript semantics

---

## 12. References

- [requirements.md](requirements.md) - Complete requirements specification
- [Option A: Two-Level Scope Chain](requirements.md#81-option-a-two-level-scope-chain-with-package-registry)
- [Recommended Approach](requirements.md#84-recommended-approach)
- TypeScript Module Resolution: https://www.typescriptlang.org/docs/handbook/module-resolution.html
- TypeScript Declaration Files: https://www.typescriptlang.org/docs/handbook/declaration-files/introduction.html

---

## Appendix A: Example Code Structure

### Before (Current)
```escalier
// All in one namespace
import "lodash"

let result = map([1, 2, 3], fn(x) { x * 2 })  // Works, but ambiguous
```

### After (Target)
```escalier
// Qualified access required
import "lodash"

let result = lodash.map([1, 2, 3], fn(x) { x * 2 })  // Clear and explicit

// Re-exporting package symbols
export val map = lodash.map  // Creates an alias

// Local shadowing
type Array<T> = { items: T[] }
let arr: Array<number>  // Local Array
let global: globalThis.Array<number>  // Global Array
```

---

## Appendix B: Key Data Structures Summary

```go
// Package Registry (at Checker level)
// Uses resolved .d.ts file paths as keys (not package names) to support
// monorepos where different projects may have different versions of the same package.
type PackageRegistry struct {
    packages map[string]*type_system.Namespace  // .d.ts file path → namespace
}

// Example registry contents:
// "/monorepo/project-a/node_modules/lodash/index.d.ts" → lodash v4.17.21 namespace
// "/monorepo/project-b/node_modules/lodash/index.d.ts" → lodash v4.17.15 namespace

// Scope (simplified)
type Scope struct {
    Parent    *Scope
    Namespace *type_system.Namespace
}

// Namespace (with sub-namespaces)
type Namespace struct {
    Values     map[string]*Binding
    Types      map[string]*TypeAlias
    Namespaces map[string]*Namespace  // For package identifiers
}

// Scope Chain for Modules (three levels)
globalScope (Parent: nil)
    ↑
moduleScope (Parent: globalScope)  // Shared namespace for module declarations
    ↑
fileScope (Parent: moduleScope)  // Each .esc file has its own fileScope
    └── Namespaces (bound by import statements in this file):
        ├── "lodash" → points to namespace from packageRegistry["/path/to/lodash/index.d.ts"]
        └── "ramda" → points to namespace from packageRegistry["/path/to/ramda/index.d.ts"]

// Scope Chain for Scripts (two levels)
globalScope (Parent: nil)
    ↑
scriptScope (Parent: globalScope)  // Single scope for declarations + imports

// Note: Import bindings are file-scoped. If file_a.esc imports "lodash",
// file_b.esc cannot access "lodash" unless it has its own import statement.
// Module declarations (types, functions) go to moduleScope and ARE visible across files.
// The identifier (e.g., "lodash") is derived from the import specifier using DerivePackageIdentifier().
```

---

**End of Implementation Plan**
