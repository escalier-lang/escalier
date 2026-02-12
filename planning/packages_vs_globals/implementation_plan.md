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
Package Registry
├── "lodash" namespace → { map, filter, ... }
├── "ramda" namespace → { map, filter, ... }
└── "@types/node" namespace → { ... }

Scope Chain (for unqualified lookup):
    globalScope (Namespace: globals)
        ↑ Parent
    userScope (Namespace: local + sub-namespaces)
        ├── Local bindings (MyType, myFunc, ...)
        └── Package sub-namespaces:
            ├── "lodash" → points to registry entry
            └── "ramda" → points to registry entry
```

**Unqualified lookup** (`Array`): Local → Global
**Qualified lookup** (`lodash.map`): Lookup `lodash` in Local (finds sub-namespace) → Member access on that namespace

### 2.3 Access Patterns

| Code | Lookup Path |
|------|-------------|
| `Array` | Local → Global (finds Global.Array) |
| `MyType` | Local (finds Local.MyType) |
| `lodash.map` | Local finds `lodash` sub-namespace → member access finds `map` |
| `globalThis.Array` | Special `globalThis` binding → member access on global namespace |

## 3. Data Structure Changes

### 3.1 New Structures

```go
// Package registry - separate from scope chain
type PackageRegistry struct {
    packages map[string]*type_system.Namespace  // package identity → namespace
}

func NewPackageRegistry() *PackageRegistry {
    return &PackageRegistry{
        packages: make(map[string]*type_system.Namespace),
    }
}

func (pr *PackageRegistry) Register(identity string, ns *type_system.Namespace) {
    pr.packages[identity] = ns
}

func (pr *PackageRegistry) Lookup(identity string) (*type_system.Namespace, bool) {
    ns, ok := pr.packages[identity]
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

## 4. Implementation Phases

### Phase 1: Infrastructure Setup
**Goal**: Create package registry and update core data structures
**Duration**: ~2-3 days
**Risk**: Low

### Phase 2: .d.ts Classification
**Goal**: Detect and classify .d.ts files (package vs global)
**Duration**: ~3-4 days
**Risk**: Medium (complex parsing logic)

### Phase 3: Global Namespace Separation
**Goal**: Isolate globals in their own namespace
**Duration**: ~2-3 days
**Risk**: Medium (may break existing code)

### Phase 4: Package Registry and Import Binding
**Goal**: Load packages into registry and bind as sub-namespaces
**Duration**: ~4-5 days
**Risk**: High (core feature implementation)

### Phase 5: Local Shadowing and globalThis
**Goal**: Enable local shadowing of globals + `globalThis` access
**Duration**: ~2-3 days
**Risk**: Medium

### Phase 6: Testing and Edge Cases
**Goal**: Comprehensive testing and bug fixes
**Duration**: ~3-5 days
**Risk**: Medium

**Total Estimated Duration**: 16-25 days

## 5. Detailed Phase Implementation

### Phase 1: Infrastructure Setup

#### 1.1 Create Package Registry
**File**: `internal/checker/package_registry.go` (new file)

```go
package checker

import (
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

func (pr *PackageRegistry) Register(identity string, ns *type_system.Namespace) error {
    // Validate identity
    // Register namespace
    // Handle duplicate registrations (merge or error)
}

func (pr *PackageRegistry) Lookup(identity string) (*type_system.Namespace, bool) {
    // Simple map lookup
}

func (pr *PackageRegistry) MustLookup(identity string) *type_system.Namespace {
    // Panic if not found (for internal use)
}
```

**Tasks**:
- [ ] Create `package_registry.go` with basic structure
- [ ] Implement `Register()` method with validation
- [ ] Implement `Lookup()` and `MustLookup()` methods
- [ ] Add unit tests for registry operations

#### 1.2 Update Checker Structure
**File**: `internal/checker/checker.go`

```go
type Checker struct {
    // ... existing fields ...
    packageRegistry *PackageRegistry
    globalScope     *Scope
}

func NewChecker(...) *Checker {
    c := &Checker{
        packageRegistry: NewPackageRegistry(),
        // ... other initialization ...
    }

    // Initialize global scope
    c.globalScope = c.initializeGlobalScope()

    return c
}
```

**Tasks**:
- [ ] Add `packageRegistry` field to Checker
- [ ] Add `globalScope` field to Checker
- [ ] Update `NewChecker()` to initialize registry
- [ ] Update all Checker constructors/factories

#### 1.3 Add Sub-Namespace Support
**File**: `internal/type_system/namespace.go` (or relevant file)

```go
type Namespace struct {
    Values     map[string]*Binding
    Types      map[string]*TypeAlias
    Namespaces map[string]*Namespace  // already exists
}

func (ns *Namespace) setNamespace(name string, subNs *Namespace) error {
    if ns.Namespaces == nil {
        ns.Namespaces = make(map[string]*Namespace)
    }

    // Check for conflicts with types/values
    if _, exists := ns.Types[name]; exists {
        return fmt.Errorf("cannot bind sub-namespace %q: conflicts with existing type", name)
    }
    if _, exists := ns.Values[name]; exists {
        return fmt.Errorf("cannot bind sub-namespace %q: conflicts with existing value", name)
    }

    ns.Namespaces[name] = subNs
    return nil
}

func (ns *Namespace) getNamespace(name string) (*Namespace, bool) {
    if ns.Namespaces == nil {
        return nil, false
    }
    subNs, ok := ns.Namespaces[name]
    return subNs, ok
}
```

**Tasks**:
- [ ] Implement `setNamespace()` method
- [ ] Implement `getNamespace()` method
- [ ] Add conflict detection (sub-namespace vs type/value names)
- [ ] Write unit tests for sub-namespace operations

---

### Phase 2: .d.ts Classification

#### 2.1 Add Classification Detection
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

func isTopLevelExport(stmt ast.Node) bool {
    // Check for export keyword at top level
    // Return true for: export interface, export type, export function, etc.
}

func extractNamedModule(stmt ast.Node) *NamedModuleDecl {
    // Check if statement is "declare module "name" { ... }"
    // Return module name and declarations if found
}
```

**Tasks**:
- [ ] Create classifier.go with classification types
- [ ] Implement `ClassifyDTSFile()` function
- [ ] Implement `isTopLevelExport()` helper
- [ ] Implement `extractNamedModule()` helper
- [ ] Add tests for various .d.ts file patterns:
  - [ ] File with top-level exports
  - [ ] File with no exports (all globals)
  - [ ] File with named modules only
  - [ ] Mixed file (globals + named modules)
  - [ ] Edge case: file with re-exports

#### 2.2 Resolve Package Identity
**File**: `internal/dts_parser/package_identity.go` (new file)

```go
func ResolvePackageIdentity(dtsFilePath string) (string, error) {
    // Traverse up directory tree looking for package.json
    dir := filepath.Dir(dtsFilePath)
    for {
        pkgJsonPath := filepath.Join(dir, "package.json")
        if fileExists(pkgJsonPath) {
            return pkgJsonPath, nil
        }

        parent := filepath.Dir(dir)
        if parent == dir {
            // Reached filesystem root
            return "", fmt.Errorf("no package.json found for %s", dtsFilePath)
        }
        dir = parent
    }
}

func DerivePackageIdentifier(moduleName string) string {
    // Strip scope prefix (@scope/pkg → pkg)
    name := moduleName
    if strings.HasPrefix(name, "@") {
        parts := strings.SplitN(name, "/", 2)
        if len(parts) == 2 {
            name = parts[1]
        }
    }

    // Replace hyphens with underscores
    name = strings.ReplaceAll(name, "-", "_")

    return name
}
```

**Tasks**:
- [ ] Implement `ResolvePackageIdentity()` function
- [ ] Implement `DerivePackageIdentifier()` function
- [ ] Add caching for package.json lookups
- [ ] Handle edge cases:
  - [ ] No package.json found
  - [ ] package.json without "name" field
  - [ ] Symlinked directories
- [ ] Write tests for identity resolution

#### 2.3 Update Module Loader
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

### Phase 3: Global Namespace Separation

#### 3.1 Refactor Prelude Loading
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
    for identity, pkgDecls := range loadedModule.Packages {
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

        c.packageRegistry.Register(identity, pkgNs)
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

#### 3.2 Update InferModule and InferScript
**Files**: `internal/checker/infer_module.go`, `internal/checker/infer_script.go`

Escalier distinguishes between **modules** and **scripts**:
- A **module** is comprised of multiple files and directories where each directory corresponds to a namespace and each namespace contains all declarations from the files in that directory
- A **script** is a single `.esc` file

Both `InferModule` and `InferScript` already exist. The key change is ensuring the context passed to these functions has a scope whose parent chain includes the global scope.

**Current `InferModule`** (no changes needed to the function itself):
```go
func (c *Checker) InferModule(ctx Context, m *ast.Module) []Error {
    depGraph := dep_graph.BuildDepGraph(m)
    return c.InferDepGraph(ctx, depGraph)
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

---

### Phase 4: Package Registry and Import Binding

#### 4.1 Load Packages into Registry
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
func (c *Checker) inferImportStatement(stmt *ast.ImportStmt) error {
    modulePath := stmt.ModulePath

    // Derive package identifier for binding
    packageIdent := stmt.Alias
    if packageIdent == "" {
        packageIdent = DerivePackageIdentifier(modulePath)
    }

    // Check if package already loaded
    if _, exists := c.packageRegistry.Lookup(modulePath); !exists {
        // Load package for the first time
        if err := c.loadPackage(modulePath); err != nil {
            return err
        }
    }

    // Bind package namespace as sub-namespace in current scope
    pkgNs, ok := c.packageRegistry.Lookup(modulePath)
    if !ok {
        return fmt.Errorf("package %q not found in registry", modulePath)
    }

    currentNs := c.currentScope.Namespace
    if err := currentNs.setNamespace(packageIdent, pkgNs); err != nil {
        return err
    }

    return nil
}

func (c *Checker) loadPackage(modulePath string) error {
    // Resolve to file path via Node module resolution
    filePath, err := c.resolveModule(modulePath)
    if err != nil {
        return fmt.Errorf("cannot resolve module %q: %w", modulePath, err)
    }

    // Load and classify
    module, err := LoadTypeScriptModule(filePath)
    if err != nil {
        return err
    }

    // Determine package identity
    var packageIdentity string
    if len(module.Packages) == 1 && module.GlobalDecls == nil {
        // Single package file - use resolved identity
        packageIdentity, err = ResolvePackageIdentity(filePath)
        if err != nil {
            return err
        }
    } else {
        // Named module - use module name as identity
        packageIdentity = modulePath
    }

    // Create package namespace
    pkgNs := type_system.NewNamespace()
    pkgScope := &Scope{
        Parent:    c.globalScope,  // Packages can reference globals
        Namespace: pkgNs,
    }

    // Infer package declarations
    pkgDecls := module.Packages[packageIdentity]
    if pkgDecls == nil && len(module.Packages) > 0 {
        // Take the first (and likely only) package
        for _, decls := range module.Packages {
            pkgDecls = decls
            break
        }
    }

    // Build dep graph and infer package declarations
    pkgModule := &ast.Module{Decls: pkgDecls.Decls}
    depGraph := dep_graph.BuildDepGraph(pkgModule)
    ctx := Context{Scope: pkgScope}
    if errs := c.InferDepGraph(ctx, depGraph); len(errs) > 0 {
        return errs[0]  // or collect all errors
    }

    // Register in package registry
    c.packageRegistry.Register(modulePath, pkgNs)

    // Handle global augmentations if any
    if len(module.GlobalDecls) > 0 {
        globalModule := &ast.Module{Decls: module.GlobalDecls}
        globalDepGraph := dep_graph.BuildDepGraph(globalModule)
        globalCtx := Context{Scope: c.globalScope}
        if errs := c.InferDepGraph(globalCtx, globalDepGraph); len(errs) > 0 {
            return errs[0]  // or collect all errors
        }
    }

    return nil
}
```

**Tasks**:
- [ ] Implement new `inferImportStatement()` logic
- [ ] Implement `loadPackage()` method
- [ ] Implement `DerivePackageIdentifier()` (from Phase 2)
- [ ] Handle import aliases (`import "pkg" as alias`)
- [ ] Handle re-imports (no-op if already loaded)
- [ ] Add caching to avoid re-loading same package
- [ ] Test various import scenarios:
  - [ ] Simple import
  - [ ] Import with alias
  - [ ] Multiple imports
  - [ ] Circular imports (edge case)

#### 4.2 Handle Subpath Imports
**File**: `internal/checker/infer_import.go`

```go
func (c *Checker) loadPackage(modulePath string) error {
    // ... existing code ...

    // For subpath imports, use full path as identity
    // e.g., "lodash/array" is separate from "lodash"
    packageIdentity := modulePath

    // ... rest of loading logic ...
}
```

**Tasks**:
- [ ] Ensure subpath imports create separate registry entries
- [ ] Test that `import "lodash"` and `import "lodash/fp"` are separate
- [ ] Document subpath import behavior

---

### Phase 5: Local Shadowing and globalThis

#### 5.1 Implement Shadowing via Parent Chain
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

#### 5.2 Implement globalThis
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

---

### Phase 6: Testing and Edge Cases

#### 6.1 Unit Tests

**Core Infrastructure**:
- [ ] Package registry operations (register, lookup, duplicate handling)
- [ ] Sub-namespace binding and lookup
- [ ] Scope chain traversal (Local → Global)

**File Classification**:
- [ ] .d.ts files with top-level exports
- [ ] .d.ts files without exports
- [ ] .d.ts files with named modules
- [ ] Mixed .d.ts files

**Namespace Operations**:
- [ ] Global namespace isolation
- [ ] Package namespace isolation
- [ ] Local shadowing of globals
- [ ] `globalThis` access

**Import and Access**:
- [ ] Simple imports
- [ ] Aliased imports
- [ ] Re-imports (no-op)
- [ ] Qualified access (`pkg.symbol`)
- [ ] Nested qualified access
- [ ] Error: undefined package member
- [ ] Error: bare package identifier used

#### 6.2 Integration Tests

**End-to-End Scenarios**:
- [ ] Load globals, import package, use both in user code
- [ ] Local type shadows global, access global via `globalThis`
- [ ] Multiple packages with same symbol name (no conflict)
- [ ] Nested `node_modules` with same package name (separate namespaces)

**TypeScript Interop**:
- [ ] Load lib.es5.d.ts (globals)
- [ ] Load lib.dom.d.ts (globals)
- [ ] Load @types/node (package with named modules)
- [ ] Load lodash (package with top-level exports)
- [ ] Mixed file with `declare global` augmentation

#### 6.3 Edge Cases

**Circular Dependencies**:
- [ ] Package A imports package B, which imports A
- [ ] Solution: Load packages against global scope only, not each other

**Re-exports**:
- [ ] Package A re-exports types from package B
- [ ] Ensure original package types are preserved

**Global Augmentation**:
- [ ] Package file with `declare global { ... }`
- [ ] Ensure augmentations go to global namespace

**Subpath Imports**:
- [ ] `import "lodash"` vs `import "lodash/fp"`
- [ ] Ensure separate namespaces

**Package Identity Edge Cases**:
- [ ] Nested `node_modules` with same package name
- [ ] Symlinked packages
- [ ] Monorepo packages

#### 6.4 Performance Testing

- [ ] Measure type resolution time before/after changes
- [ ] Ensure no significant regression (< 10% slowdown acceptable)
- [ ] Profile hot paths if performance degrades

#### 6.5 Documentation

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
        └── mixed.d.ts
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
- How to use qualified access
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

### Low Risk Areas

1. **Package Registry**: Simple data structure
2. **Scope Chain**: Standard parent-child relationship

---

## 10. Timeline and Milestones

### Milestone 1: Infrastructure (Week 1)
- [ ] Complete Phase 1: Package registry and data structures
- [ ] Complete Phase 2: .d.ts classification

### Milestone 2: Core Features (Week 2)
- [ ] Complete Phase 3: Global namespace separation
- [ ] Complete Phase 4: Package registry and imports

### Milestone 3: Advanced Features and Testing (Week 3)
- [ ] Complete Phase 5: Shadowing and globalThis
- [ ] Complete Phase 6: Testing and edge cases
- [ ] Documentation
- [ ] Migration guide
- [ ] Release

---

## 11. Open Questions

1. **Should there be a way to bulk-import symbols from a package?**
   - Example: `import "lodash" exposing (map, filter, reduce)`
   - Decision: Not in initial implementation, can be added later

2. **How should TypeScript's `export = ` syntax be handled?**
   - Need to investigate and document

3. **Should package re-exports create aliases or copies?**
   - Recommendation: Aliases to preserve type identity

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

// Local shadowing
type Array<T> = { items: T[] }
let arr: Array<number>  // Local Array
let global: globalThis.Array<number>  // Global Array
```

---

## Appendix B: Key Data Structures Summary

```go
// Package Registry (at Checker level)
type PackageRegistry struct {
    packages map[string]*type_system.Namespace
}

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

// Scope Chain
globalScope (Parent: nil)
    ↑
userScope (Parent: globalScope)
    └── Namespaces:
        ├── "lodash" → points to packageRegistry["lodash"]
        └── "ramda" → points to packageRegistry["ramda"]
```

---

**End of Implementation Plan**
