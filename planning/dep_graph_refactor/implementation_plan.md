# DepGraph Refactor Implementation Plan

This document outlines the step-by-step implementation plan for refactoring `DepGraph` to use `BindingKey` as the primary identifier instead of `DeclID`.

## Progress Checklist

### Phase 1: Define New Data Structures
- [x] Step 1.1: Add BindingKey type
- [x] Step 1.2: Define new DepGraph structure

### Phase 2: Implement Core DepGraph Functions
- [x] Step 2.1: Implement NewDepGraphV2
- [x] Step 2.2: Implement accessor methods
- [x] Step 2.3: Implement ModuleBindingVisitorV2
- [x] Step 2.4: Implement DependencyVisitorV2
- [x] Step 2.5: Implement FindDeclDependenciesV2
- [x] Step 2.6: Implement BuildDepGraphV2

### Phase 3: Update SCC Algorithm
- [x] Step 3.1: Implement FindStronglyConnectedComponentsV2
- [x] Step 3.2: Create cycles_v2.go with V2 cycle detection functions

### Phase 4: Update Checker (infer_module.go)
- [ ] Step 4.1: Update InferModule
- [ ] Step 4.2: Update InferDepGraph
- [ ] Step 4.3: Update InferComponent
- [ ] Step 4.4: Update GetNamespaceCtx

### Phase 5: Update Codegen (builder.go)
- [ ] Step 5.1: Update Builder struct
- [ ] Step 5.2: Update BuildTopLevelDecls
- [ ] Step 5.3: Update BuildDefinitions

### Phase 6: Update Compiler
- [ ] Step 6.1: Update compiler.go

### Phase 7: Update Tests
- [ ] Step 7.1: Update dep_graph_test.go
- [ ] Step 7.2: Update overload_test.go
- [ ] Step 7.3: Add interface merging tests

### Phase 8: Remove Old Code
- [ ] Step 8.1: Remove deprecated types and functions
- [ ] Step 8.2: Update all imports

---

## Overview

The refactor touches the following packages:
- `internal/dep_graph` - Core changes to data structures and algorithms
- `internal/checker` - Updates to `infer_module.go` 
- `internal/codegen` - Updates to `builder.go` and `dts.go`
- `internal/compiler` - Updates to `compiler.go`

## Phase 1: Define New Data Structures ✅

### Step 1.1: Add BindingKey type ✅

**File:** `internal/dep_graph/dep_graph_v2.go`

**Status:** Implemented (with a simpler string-based approach)

The implementation uses a string type with a "value:" or "type:" prefix instead of a struct. This is simpler and works well with `btree.Map` since strings are naturally comparable.

```go
// BindingKey uniquely identifies a binding in the dependency graph.
// It is a string that combines the dependency kind ("value" or "type") with
// the fully qualified name, separated by a colon.
// Examples: "value:foo.bar", "type:foo.MyType", "value:createUser"
type BindingKey string

// ValueBindingKey creates a BindingKey for a value binding with the given qualified name.
func ValueBindingKey(qualifiedName string) BindingKey {
    return BindingKey("value:" + qualifiedName)
}

// TypeBindingKey creates a BindingKey for a type binding with the given qualified name.
func TypeBindingKey(qualifiedName string) BindingKey {
    return BindingKey("type:" + qualifiedName)
}

// NewBindingKey creates a BindingKey from a qualified name and dependency kind.
func NewBindingKey(qualifiedName string, kind DepKind) BindingKey {
    if kind == DepKindType {
        return TypeBindingKey(qualifiedName)
    }
    return ValueBindingKey(qualifiedName)
}

// Kind returns the dependency kind (DepKindValue or DepKindType) for this binding key.
func (k BindingKey) Kind() DepKind {
    if strings.HasPrefix(string(k), "type:") {
        return DepKindType
    }
    return DepKindValue
}

// Name returns the fully qualified name portion of the binding key.
func (k BindingKey) Name() string {
    if idx := strings.Index(string(k), ":"); idx != -1 {
        return string(k)[idx+1:]
    }
    return string(k)
}

// String returns the string representation of the BindingKey.
func (k BindingKey) String() string {
    return string(k)
}
```

### Step 1.2: Define new DepGraph structure ✅

**File:** `internal/dep_graph/dep_graph_v2.go`

**Status:** Implemented

The `DepGraphV2` struct was added as planned:

```go
// DepGraphV2 is the refactored dependency graph using BindingKey as the primary key.
type DepGraphV2 struct {
    // Map from binding key to all declarations that contribute to that binding.
    // For most bindings this will be a single declaration, but for interfaces
    // (declaration merging) and functions (overloading) there may be multiple.
    Decls btree.Map[BindingKey, []ast.Decl]

    // Dependencies for each binding key.
    // The dependencies are the union of all dependencies from all declarations
    // that contribute to this binding.
    DeclDeps btree.Map[BindingKey, btree.Set[BindingKey]]

    // Namespace for each binding (derived from the qualified name, but stored
    // for convenience).
    DeclNamespace btree.Map[BindingKey, string]

    // Strongly connected components of bindings sorted in topological order.
    // Each component is a slice of BindingKeys that form a cycle.
    Components [][]BindingKey

    // All namespace names in the module, indexed by NamespaceID.
    Namespaces []string
}
```

**Note:** We use `btree.Map` and `btree.Set` for deterministic iteration order, which is critical for reproducible code generation.

**Implementation Note:** The code examples in this plan that show struct literal syntax like `BindingKey{Name: qualName, Kind: DepKindValue}` should be updated to use the helper functions: `ValueBindingKey(qualName)` or `TypeBindingKey(qualName)` or `NewBindingKey(qualName, kind)`.

## Phase 2: Implement Core DepGraph Functions

### Step 2.1: Implement NewDepGraphV2 ✅

**File:** `internal/dep_graph/dep_graph_v2.go`

**Status:** Implemented

```go
func NewDepGraphV2(namespaceMap []string) *DepGraphV2 {
    return &DepGraphV2{
        Decls:         btree.Map[BindingKey, []ast.Decl]{},
        DeclDeps:      btree.Map[BindingKey, btree.Set[BindingKey]]{},
        DeclNamespace: btree.Map[BindingKey, string]{},
        Components:    [][]BindingKey{},
        Namespaces:    namespaceMap,
    }
}
```

### Step 2.2: Implement accessor methods ✅

**File:** `internal/dep_graph/dep_graph_v2.go`

**Status:** Implemented - All accessor methods are complete:

```go
// GetDecls returns all declarations for a binding key
func (g *DepGraphV2) GetDecls(key BindingKey) []ast.Decl {
    decls, _ := g.Decls.Get(key)
    return decls
}

// GetDeps returns the dependencies for a binding key
func (g *DepGraphV2) GetDeps(key BindingKey) btree.Set[BindingKey] {
    deps, _ := g.DeclDeps.Get(key)
    return deps
}

// GetNamespace returns the namespace for a binding key
func (g *DepGraphV2) GetNamespace(key BindingKey) string {
    ns, _ := g.DeclNamespace.Get(key)
    return ns
}

// AllBindings returns all binding keys in the graph in deterministic order
func (g *DepGraphV2) AllBindings() []BindingKey {
    keys := make([]BindingKey, 0, g.Decls.Len())
    iter := g.Decls.Iter()
    for ok := iter.First(); ok; ok = iter.Next() {
        keys = append(keys, iter.Key())
    }
    return keys
}

// HasBinding checks if a binding exists
func (g *DepGraphV2) HasBinding(key BindingKey) bool {
    _, exists := g.Decls.Get(key)
    return exists
}

// AddDecl adds a declaration to the graph under the given binding key.
// If the key already exists, the declaration is appended to the existing slice.
func (g *DepGraphV2) AddDecl(key BindingKey, decl ast.Decl, namespace string) {
    existing, _ := g.Decls.Get(key)
    g.Decls.Set(key, append(existing, decl))
    g.DeclNamespace.Set(key, namespace)
}

// SetDeps sets the dependencies for a binding key
func (g *DepGraphV2) SetDeps(key BindingKey, deps btree.Set[BindingKey]) {
    g.DeclDeps.Set(key, deps)
}
```

### Step 2.3: Implement ModuleBindingVisitorV2 ✅

**File:** `internal/dep_graph/dep_graph_v2.go`

**Status:** Implemented

The visitor populates `DepGraphV2` directly using the helper functions for creating `BindingKey`s. Also added a `PopulateBindings` function as the entry point:

```go
// ModuleBindingVisitorV2 collects all declarations and populates a DepGraphV2.
// Unlike the original ModuleBindingVisitor, this version:
// - Uses BindingKey instead of DeclID
// - Automatically groups multiple declarations under the same key (for overloads and interface merging)
// - Does not require separate ValueBindings/TypeBindings maps
type ModuleBindingVisitorV2 struct {
    ast.DefaultVisitor
    Graph         *DepGraphV2
    currentNSName string
}

func (v *ModuleBindingVisitorV2) qualifyName(name string) string {
    if v.currentNSName != "" {
        return v.currentNSName + "." + name
    }
    return name
}

func (v *ModuleBindingVisitorV2) EnterDecl(decl ast.Decl) bool {
    switch d := decl.(type) {
    case *ast.VarDecl:
        bindingNames := ast.FindBindings(d.Pattern)
        for name := range bindingNames {
            qualName := v.qualifyName(name)
            key := ValueBindingKey(qualName)
            v.Graph.AddDecl(key, decl, v.currentNSName)
        }
    case *ast.FuncDecl:
        if d.Name != nil && d.Name.Name != "" {
            qualName := v.qualifyName(d.Name.Name)
            key := ValueBindingKey(qualName)
            v.Graph.AddDecl(key, decl, v.currentNSName)
        }
    case *ast.TypeDecl:
        if d.Name != nil && d.Name.Name != "" {
            qualName := v.qualifyName(d.Name.Name)
            key := TypeBindingKey(qualName)
            v.Graph.AddDecl(key, decl, v.currentNSName)
        }
    case *ast.ClassDecl:
        if d.Name != nil && d.Name.Name != "" {
            qualName := v.qualifyName(d.Name.Name)
            typeKey := TypeBindingKey(qualName)
            valueKey := ValueBindingKey(qualName)
            v.Graph.AddDecl(typeKey, decl, v.currentNSName)
            v.Graph.AddDecl(valueKey, decl, v.currentNSName)
        }
    case *ast.InterfaceDecl:
        if d.Name != nil && d.Name.Name != "" {
            qualName := v.qualifyName(d.Name.Name)
            key := TypeBindingKey(qualName)
            v.Graph.AddDecl(key, decl, v.currentNSName)
        }
    case *ast.EnumDecl:
        if d.Name != nil && d.Name.Name != "" {
            qualName := v.qualifyName(d.Name.Name)
            typeKey := TypeBindingKey(qualName)
            valueKey := ValueBindingKey(qualName)
            v.Graph.AddDecl(typeKey, decl, v.currentNSName)
            v.Graph.AddDecl(valueKey, decl, v.currentNSName)
        }
    }
    return false
}

// Other visitor methods return false to avoid traversing into nested structures
func (v *ModuleBindingVisitorV2) EnterStmt(stmt ast.Stmt) bool               { return false }
func (v *ModuleBindingVisitorV2) EnterExpr(expr ast.Expr) bool               { return false }
func (v *ModuleBindingVisitorV2) EnterPat(pat ast.Pat) bool                  { return false }
func (v *ModuleBindingVisitorV2) EnterObjExprElem(elem ast.ObjExprElem) bool { return false }
func (v *ModuleBindingVisitorV2) EnterTypeAnn(t ast.TypeAnn) bool            { return false }
func (v *ModuleBindingVisitorV2) EnterLit(lit ast.Lit) bool                  { return false }
func (v *ModuleBindingVisitorV2) EnterBlock(block ast.Block) bool            { return false }

// PopulateBindings visits all declarations in a module and populates the graph.
func PopulateBindings(graph *DepGraphV2, module *ast.Module) {
    visitor := &ModuleBindingVisitorV2{
        DefaultVisitor: ast.DefaultVisitor{},
        Graph:          graph,
        currentNSName:  "",
    }

    iter := module.Namespaces.Iter()
    for ok := iter.First(); ok; ok = iter.Next() {
        nsName := iter.Key()
        ns := iter.Value()
        for _, decl := range ns.Decls {
            visitor.currentNSName = nsName
            decl.Accept(visitor)
        }
    }
}
```

### Step 2.4: Implement DependencyVisitorV2 ✅

**File:** `internal/dep_graph/dep_graph_v2.go`

**Status:** Implemented

The visitor finds dependencies in declarations and returns them as `btree.Set[BindingKey]`. Key features:
- Uses `LocalScopeV2` struct for tracking local bindings
- Helper methods: `addValueDependency()`, `addTypeDependency()` that check `Graph.HasBinding()`
- Handles all expression, statement, type annotation, and block traversals
- Properly tracks scope for functions, blocks, and declarations

```go
// LocalScopeV2 represents a single scope with separate value and type bindings
type LocalScopeV2 struct {
    ValueBindings set.Set[string]
    TypeBindings  set.Set[string]
}

// DependencyVisitorV2 finds dependencies in a declaration and returns them as BindingKeys
type DependencyVisitorV2 struct {
    ast.DefaultVisitor
    Graph            *DepGraphV2
    NamespaceMap     map[string]ast.NamespaceID
    Dependencies     btree.Set[BindingKey]
    LocalScopes      []LocalScopeV2
    CurrentNamespace string
}

// Key methods implemented:
// - pushScope(), popScope() - manage scope stack
// - isLocalValueBinding(), isLocalTypeBinding() - check for shadowing
// - addValueDependency(), addTypeDependency() - add deps with namespace resolution
// - EnterStmt(), EnterExpr(), ExitExpr() - handle expressions and statements
// - EnterTypeAnn() - handle type references and typeof
// - EnterBlock(), ExitBlock() - handle block scopes
// - EnterObjExprElem() - handle property shorthand
// - processTypeParams() - handle generic type parameters
// - buildQualifiedName() - build qualified names from member expressions
```

The key change from the original is that instead of looking up `DeclID` in `ValueBindings`/`TypeBindings`, we construct a `BindingKey` and check if it exists using `Graph.HasBinding()`.

### Step 2.5: Implement FindDeclDependenciesV2 ✅

**File:** `internal/dep_graph/dep_graph_v2.go`

**Status:** Implemented

This function finds all dependencies for declarations under a binding key. For bindings with multiple declarations (overloaded functions, merged interfaces), dependencies are unioned.

Key features:
- Iterates over all declarations for the key
- Creates a fresh `DependencyVisitorV2` for each declaration
- Handles all declaration types: VarDecl, FuncDecl, TypeDecl, InterfaceDecl, EnumDecl, ClassDecl
- Properly processes type parameters, parameters, return types, class bodies, etc.
- Unions dependencies from all declarations

```go
// FindDeclDependenciesV2 finds all dependencies for declarations under a binding key.
// For bindings with multiple declarations (overloaded functions, merged interfaces),
// the dependencies are the union of all declarations' dependencies.
func FindDeclDependenciesV2(key BindingKey, graph *DepGraphV2) btree.Set[BindingKey] {
    decls := graph.GetDecls(key)
    currentNamespace := graph.GetNamespace(key)
    namespaceMap := make(map[string]ast.NamespaceID)
    for i, nsName := range graph.Namespaces {
        namespaceMap[nsName] = ast.NamespaceID(i)
    }

    var allDeps btree.Set[BindingKey]

    for _, decl := range decls {
        visitor := &DependencyVisitorV2{...}
        visitor.pushScope()

        // Handle declaration based on type...

        visitor.popScope()

        // Union the dependencies
        iter := visitor.Dependencies.Iter()
        for ok := iter.First(); ok; ok = iter.Next() {
            allDeps.Insert(iter.Key())
        }
    }

    return allDeps
}
```

### Step 2.6: Implement BuildDepGraphV2 ✅

**File:** `internal/dep_graph/dep_graph_v2.go`

**Status:** Implemented

This is the main entry point for building the V2 dependency graph. It:
1. Collects namespaces from the module
2. Creates a new graph with `NewDepGraphV2`
3. Populates bindings using `PopulateBindings`
4. Finds dependencies for each binding using `FindDeclDependenciesV2`
5. Computes strongly connected components using `FindStronglyConnectedComponentsV2`

```go
func BuildDepGraphV2(module *ast.Module) *DepGraphV2 {
    namespaceMap := collectNamespaces(module)
    graph := NewDepGraphV2(namespaceMap)

    PopulateBindings(graph, module)

    allKeys := graph.AllBindings()
    for _, key := range allKeys {
        deps := FindDeclDependenciesV2(key, graph)
        graph.SetDeps(key, deps)
    }

    graph.Components = graph.FindStronglyConnectedComponentsV2(0)

    return graph
}
```

## Phase 3: Update SCC Algorithm ✅

### Step 3.1: Implement FindStronglyConnectedComponentsV2 ✅

**File:** `internal/dep_graph/dep_graph_v2.go`

**Status:** Implemented

Uses Tarjan's algorithm adapted for `BindingKey` instead of `DeclID`.

```go
// FindStronglyConnectedComponentsV2 uses Tarjan's algorithm with BindingKey
func (g *DepGraphV2) FindStronglyConnectedComponentsV2(threshold int) [][]BindingKey {
    index := 0
    stack := make([]BindingKey, 0)
    indices := make(map[BindingKey]int)
    lowlinks := make(map[BindingKey]int)
    onStack := make(map[BindingKey]bool)
    sccs := make([][]BindingKey, 0)

    var strongConnect func(BindingKey)
    strongConnect = func(v BindingKey) {
        indices[v] = index
        lowlinks[v] = index
        index++
        stack = append(stack, v)
        onStack[v] = true

        deps := g.GetDeps(v)
        iter := deps.Iter()
        for ok := iter.First(); ok; ok = iter.Next() {
            w := iter.Key()
            if _, exists := indices[w]; !exists {
                strongConnect(w)
                if lowlinks[w] < lowlinks[v] {
                    lowlinks[v] = lowlinks[w]
                }
            } else if onStack[w] {
                if indices[w] < lowlinks[v] {
                    lowlinks[v] = indices[w]
                }
            }
        }

        if lowlinks[v] == indices[v] {
            var scc []BindingKey
            for {
                w := stack[len(stack)-1]
                stack = stack[:len(stack)-1]
                onStack[w] = false
                scc = append(scc, w)
                if w == v {
                    break
                }
            }
            deps := g.GetDeps(scc[0])
            if len(scc) > threshold || (len(scc) == threshold && deps.Contains(scc[0])) {
                sccs = append(sccs, scc)
            }
        }
    }

    // Run for all binding keys
    allKeys := g.AllBindings()
    for _, key := range allKeys {
        if _, exists := indices[key]; !exists {
            strongConnect(key)
        }
    }

    return sccs
}
```

### Step 3.2: Create cycles_v2.go with V2 cycle detection functions

**File:** `internal/dep_graph/cycles_v2.go`

**Status:** Not started

Create a new file `cycles_v2.go` with V2 versions of all cycle-related types and functions from `cycles.go`. The V2 versions work with `BindingKey` instead of `DeclID`.

#### CycleInfoV2

The cycle info struct uses `BindingKey` instead of `DeclID`:

```go
// CycleInfoV2 represents information about a problematic cycle using BindingKey
type CycleInfoV2 struct {
    Cycle   []BindingKey // The binding keys involved in the cycle
    Message string       // Description of why this cycle is problematic
}
```

#### Helper methods on BindingKey

Since `BindingKey` already encodes the kind (value/type), many helper functions become trivial:

```go
// IsValueBinding returns true if this is a value binding
func (k BindingKey) IsValueBinding() bool {
    return k.Kind() == DepKindValue
}

// IsTypeBinding returns true if this is a type binding
func (k BindingKey) IsTypeBinding() bool {
    return k.Kind() == DepKindType
}
```

#### FindCyclesV2

The main cycle detection function adapted for `DepGraphV2`:

```go
// FindCyclesV2 detects problematic cycles in the dependency graph.
// It uses Tarjan's algorithm to find strongly connected components, then identifies
// cycles that are problematic according to these rules:
// - Type-only cycles are allowed and ignored
// - Mixed cycles (containing both types and values) are always problematic
// - Value-only cycles are problematic if any binding in the cycle is used outside function bodies
// Returns a slice of CycleInfoV2 containing details about each problematic cycle found.
func (g *DepGraphV2) FindCyclesV2() []CycleInfoV2 {
    var problematicCycles []CycleInfoV2

    // Find all strongly connected components (cycles)
    cycles := g.FindStronglyConnectedComponentsV2(1)

    // Pre-compute bindings used outside function bodies (only once for all cycles)
    var usedOutsideFunctionBodies set.Set[BindingKey]
    var hasComputedUsage bool

    for _, cycle := range cycles {
        // Check if cycle contains any value bindings
        hasValue := false
        for _, key := range cycle {
            if key.IsValueBinding() {
                hasValue = true
                break
            }
        }

        if !hasValue {
            // Type-only cycles are allowed, skip
            continue
        }

        // For cycles involving values, they are problematic in these cases:
        // 1. Mixed cycles (type + value) are always problematic
        // 2. Value-only cycles are problematic if any value is used outside function bodies

        isProblematic := false

        hasType := false
        for _, key := range cycle {
            if key.IsTypeBinding() {
                hasType = true
                break
            }
        }

        if hasType {
            // Mixed cycle: always problematic
            isProblematic = true
        } else {
            // Value-only cycle: check if any value is used outside function bodies
            if !hasComputedUsage {
                usedOutsideFunctionBodies = g.findBindingsUsedOutsideFunctionBodiesV2()
                hasComputedUsage = true
            }

            for _, key := range cycle {
                if key.IsValueBinding() && usedOutsideFunctionBodies.Contains(key) {
                    isProblematic = true
                    break
                }
            }
        }

        if isProblematic {
            problematicCycles = append(problematicCycles, CycleInfoV2{
                Cycle:   cycle,
                Message: "Cycle detected between bindings that are used outside of function bodies",
            })
        }
    }

    return problematicCycles
}
```

#### findBindingsUsedOutsideFunctionBodiesV2

Finds all bindings used outside function bodies, returning a set of `BindingKey`:

```go
// findBindingsUsedOutsideFunctionBodiesV2 finds all bindings that are used outside function bodies
// This function traverses the AST only once and returns a set of all such bindings
func (g *DepGraphV2) findBindingsUsedOutsideFunctionBodiesV2() set.Set[BindingKey] {
    usedOutsideFunctionBodies := set.NewSet[BindingKey]()

    // Iterate over all bindings and their declarations
    iter := g.Decls.Iter()
    for ok := iter.First(); ok; ok = iter.Next() {
        decls := iter.Value()
        for _, decl := range decls {
            visitor := &AllBindingsUsageVisitorV2{
                DefaultVisitor:                  ast.DefaultVisitor{},
                Graph:                           g,
                FunctionDepth:                   0,
                LocalBindings:                   make([]set.Set[string], 0),
                BindingsUsedOutsideFunctionBody: usedOutsideFunctionBodies,
            }
            decl.Accept(visitor)
        }
    }

    return usedOutsideFunctionBodies
}
```

#### AllBindingsUsageVisitorV2

The visitor for tracking binding usage, adapted to work with `DepGraphV2`:

```go
// AllBindingsUsageVisitorV2 checks if any bindings are used outside function bodies
type AllBindingsUsageVisitorV2 struct {
    ast.DefaultVisitor
    Graph                           *DepGraphV2
    FunctionDepth                   int               // Track nesting depth in function bodies
    LocalBindings                   []set.Set[string] // Stack of local scopes
    BindingsUsedOutsideFunctionBody set.Set[BindingKey]
}

// Key methods (similar to original but use BindingKey):
// - EnterExpr: Check identifier usage, construct BindingKey if graph.HasBinding()
// - ExitExpr: Handle function expression exit
// - EnterDecl: Handle function declaration entry
// - ExitDecl: Handle function declaration exit
// - EnterBlock/ExitBlock: Manage scope stack
// - EnterTypeAnn: Handle type references
// - pushScope/popScope: Scope management
// - isLocalBinding: Check for shadowed bindings

func (v *AllBindingsUsageVisitorV2) EnterExpr(expr ast.Expr) bool {
    switch e := expr.(type) {
    case *ast.IdentExpr:
        if !v.isLocalBinding(e.Name) && v.FunctionDepth == 0 {
            // Check if this name exists as a value binding in the graph
            valueKey := ValueBindingKey(e.Name)
            if v.Graph.HasBinding(valueKey) {
                v.BindingsUsedOutsideFunctionBody.Add(valueKey)
            }
        }
        return false
    case *ast.CallExpr:
        // Handle call expressions similarly
        if ident, ok := e.Callee.(*ast.IdentExpr); ok {
            if !v.isLocalBinding(ident.Name) && v.FunctionDepth == 0 {
                valueKey := ValueBindingKey(ident.Name)
                if v.Graph.HasBinding(valueKey) {
                    v.BindingsUsedOutsideFunctionBody.Add(valueKey)
                }
            }
        }
        return true
    case *ast.FuncExpr:
        v.FunctionDepth++
        v.pushScope()
        // Add parameters to scope...
        return true
    default:
        return true
    }
}

func (v *AllBindingsUsageVisitorV2) EnterTypeAnn(typeAnn ast.TypeAnn) bool {
    switch t := typeAnn.(type) {
    case *ast.TypeRefTypeAnn:
        typeName := ast.QualIdentToString(t.Name)
        if !v.isLocalBinding(typeName) && v.FunctionDepth == 0 {
            typeKey := TypeBindingKey(typeName)
            if v.Graph.HasBinding(typeKey) {
                v.BindingsUsedOutsideFunctionBody.Add(typeKey)
            }
        }
        return true
    default:
        return true
    }
}

// ... other methods similar to original but adapted for BindingKey
```

#### GetBindingNames (optional utility)

A utility method to get all binding key names in the graph:

```go
// GetBindingNames returns the names for a slice of binding keys
func GetBindingNames(keys []BindingKey) []string {
    names := make([]string, len(keys))
    for i, key := range keys {
        names[i] = key.Name()
    }
    return names
}
```

**Key differences from cycles.go:**
1. Uses `BindingKey` instead of `DeclID` throughout
2. No need for `getBindingsForDecl` - the key already identifies the binding
3. `hasValueBinding`/`hasTypeBinding` replaced by `key.IsValueBinding()`/`key.IsTypeBinding()`
4. The visitor uses `Graph.HasBinding()` to check if a name is a graph binding
5. Simpler logic since binding keys encode both name and kind

## Phase 4: Update Checker (infer_module.go)

### Step 4.1: Update InferModule

**File:** `internal/checker/infer_module.go`

```go
func (c *Checker) InferModule(ctx Context, m *ast.Module) []Error {
    depGraph := dep_graph.BuildDepGraphV2(m)
    return c.InferDepGraphV2(ctx, depGraph)
}
```

### Step 4.2: Update InferDepGraph

**File:** `internal/checker/infer_module.go`

```go
func (c *Checker) InferDepGraphV2(ctx Context, depGraph *dep_graph.DepGraphV2) []Error {
    var errors []Error
    for _, component := range depGraph.Components {
        declsErrors := c.InferComponentV2(ctx, depGraph, component)
        errors = slices.Concat(errors, declsErrors)
    }
    return errors
}
```

### Step 4.3: Update InferComponent

**File:** `internal/checker/infer_module.go`

The key changes:
1. Iterate over `[]BindingKey` instead of `[]DeclID`
2. Use `depGraph.GetDecls(key)` to get declarations
3. Handle multiple declarations per key (for overloads and interface merging)

```go
func (c *Checker) InferComponentV2(
    ctx Context,
    depGraph *dep_graph.DepGraphV2,
    component []dep_graph.BindingKey,
) []Error {
    errors := []Error{}
    
    // Maps to track state per binding key
    paramBindingsForKey := make(map[dep_graph.BindingKey]map[string]*type_system.Binding)
    declCtxMap := make(map[dep_graph.BindingKey]Context)
    
    // Infer placeholders
    for _, key := range component {
        nsCtx := GetNamespaceCtxV2(ctx, depGraph, key)
        decls := depGraph.GetDecls(key)
        
        // Process all declarations for this key
        for _, decl := range decls {
            switch d := decl.(type) {
            case *ast.FuncDecl:
                // Handle function (potentially overloaded)
                // ... existing logic but accumulate into intersection type
            case *ast.InterfaceDecl:
                // Handle interface (potentially merged)
                // ... existing logic but merge into existing interface
            // ... other cases
            }
        }
    }
    
    // Infer definitions (similar structure)
    // ...
    
    return errors
}
```

### Step 4.4: Update GetNamespaceCtx

**File:** `internal/checker/infer_module.go`

```go
func GetNamespaceCtxV2(
    ctx Context,
    depGraph *dep_graph.DepGraphV2,
    key dep_graph.BindingKey,
) Context {
    nsName := depGraph.GetNamespace(key)
    if nsName == "" {
        return ctx
    }
    // ... rest unchanged
}
```

## Phase 5: Update Codegen (builder.go)

### Step 5.1: Update Builder struct

**File:** `internal/codegen/builder.go`

```go
type Builder struct {
    tempId       int
    depGraph     *dep_graph.DepGraphV2  // Changed type
    hasExtractor bool
    isModule     bool
    inBlockScope bool
    // Remove overloadDecls - no longer needed as overloads are grouped by BindingKey
}
```

### Step 5.2: Update BuildTopLevelDecls

**File:** `internal/codegen/builder.go`

```go
func (b *Builder) BuildTopLevelDecls(depGraph *dep_graph.DepGraphV2) *Module {
    b.depGraph = depGraph
    b.isModule = true

    var stmts []Stmt
    nsStmts := b.buildNamespaceStatements(depGraph)
    stmts = slices.Concat(stmts, nsStmts)

    // Iterate over components (now [][]BindingKey)
    for _, component := range depGraph.Components {
        for _, key := range component {
            decls := depGraph.GetDecls(key)
            nsName := depGraph.GetNamespace(key)
            
            // Skip type-only bindings
            if key.Kind == dep_graph.DepKindType {
                // Check if there's also a value binding with same name
                valueKey := dep_graph.BindingKey{Name: key.Name, Kind: dep_graph.DepKindValue}
                if !depGraph.HasBinding(valueKey) {
                    continue // Type-only, skip codegen
                }
            }
            
            // Handle multiple declarations (overloaded functions)
            if len(decls) > 1 {
                if _, ok := decls[0].(*ast.FuncDecl); ok {
                    // Build overloaded function dispatch
                    funcDecls := make([]*ast.FuncDecl, len(decls))
                    for i, d := range decls {
                        funcDecls[i] = d.(*ast.FuncDecl)
                    }
                    stmts = slices.Concat(stmts, b.buildOverloadedFunc(funcDecls, nsName))
                    continue
                }
            }
            
            // Single declaration
            stmts = slices.Concat(stmts, b.buildDeclWithNamespace(decls[0], nsName))
        }
    }
    
    // ... rest unchanged
}
```

### Step 5.3: Update BuildDefinitions

**File:** `internal/codegen/dts.go`

Similar changes - iterate over `BindingKey` instead of `DeclID`.

## Phase 6: Update Compiler

### Step 6.1: Update compiler.go

**File:** `internal/compiler/compiler.go`

```go
func CompilePackage(sources []*ast.Source) CompilerOutput {
    // ...
    
    if len(libSources) > 0 {
        inMod, parseErrors := parser.ParseLibFiles(ctx, libSources)
        depGraph := dep_graph.BuildDepGraphV2(inMod)

        c := checker.NewChecker()
        inferCtx := checker.Context{
            Scope:      checker.Prelude(c).WithNewScope(),
            IsAsync:    false,
            IsPatMatch: false,
        }
        typeErrors := c.InferDepGraphV2(inferCtx, depGraph)

        // No longer need MergeOverloadedFunctions - overloads are already grouped

        libNS = inferCtx.Scope.Namespace

        builder := &codegen.Builder{}
        jsMod := builder.BuildTopLevelDecls(depGraph)  // No overloadDecls param
        dtsMod := builder.BuildDefinitions(depGraph, libNS)
        
        // ... rest unchanged
    }
    // ...
}
```

## Phase 7: Update Tests

### Step 7.1: Update dep_graph_test.go

Update all tests to use the new API:

```go
func TestBuildDepGraphV2(t *testing.T) {
    // ... setup ...
    
    depGraph := dep_graph.BuildDepGraphV2(module)
    
    // Check bindings
    funcKey := dep_graph.BindingKey{Name: "myFunc", Kind: dep_graph.DepKindValue}
    assert.True(t, depGraph.HasBinding(funcKey))
    
    decls := depGraph.GetDecls(funcKey)
    assert.Len(t, decls, 1)
    
    // Check dependencies
    deps := depGraph.GetDeps(funcKey)
    expectedDep := dep_graph.BindingKey{Name: "MyType", Kind: dep_graph.DepKindType}
    assert.True(t, deps.Contains(expectedDep))
}
```

### Step 7.2: Update overload_test.go

```go
func TestOverloadedFunctions(t *testing.T) {
    // ... setup ...
    
    depGraph := dep_graph.BuildDepGraphV2(module)
    
    // Overloaded functions should be grouped under single BindingKey
    funcKey := dep_graph.BindingKey{Name: "process", Kind: dep_graph.DepKindValue}
    decls := depGraph.GetDecls(funcKey)
    
    assert.Len(t, decls, 2) // Two overloads
    
    // Dependencies should be union of all overloads
    deps := depGraph.GetDeps(funcKey)
    // ... verify both NumberConfig and StringConfig are in deps
}
```

### Step 7.3: Add interface merging tests

```go
func TestInterfaceMerging(t *testing.T) {
    source := `
        interface User {
            name: string
        }
        interface User {
            age: number
        }
    `
    // ... setup ...
    
    depGraph := dep_graph.BuildDepGraphV2(module)
    
    interfaceKey := dep_graph.BindingKey{Name: "User", Kind: dep_graph.DepKindType}
    decls := depGraph.GetDecls(interfaceKey)
    
    assert.Len(t, decls, 2) // Two interface declarations
}
```

## Phase 8: Remove Old Code

### Step 8.1: Remove deprecated types and functions

Once all tests pass with the V2 implementation:

1. Remove `DeclID` type
2. Remove `ModuleBindingVisitor` (old version)
3. Remove `DependencyVisitor` (old version)
4. Remove `FindModuleBindings` function
5. Remove `FindDeclDependencies` function
6. Remove `BuildDepGraph` function (old version)
7. Remove `MergeOverloadedFunctions` function
8. Remove `ValueBindings` and `TypeBindings` from old `DepGraph`
9. Remove `cycles.go` (replaced by `cycles_v2.go`)
10. Rename `DepGraphV2` to `DepGraph`
11. Rename `CycleInfoV2` to `CycleInfo`
12. Rename `cycles_v2.go` to `cycles.go`
13. Rename all V2 functions to remove suffix

### Step 8.2: Update all imports

Search and replace any remaining references to old types/functions.

## Migration Strategy

To minimize risk, implement this refactor incrementally:

1. **Phase 1-3**: Add new types and functions alongside existing ones (V2 suffix)
2. **Phase 4-6**: Create V2 versions of consumer code that use new API
3. **Phase 7**: Add new tests, update existing tests to work with both versions
4. **Run both implementations in parallel**: Temporarily run both old and new code and compare results
5. **Phase 8**: Once validated, remove old code and rename V2 → final names

## Estimated Scope

| Phase | Files Modified | Lines Changed (Est.) |
|-------|---------------|---------------------|
| 1-3   | dep_graph_v2.go (new), cycles_v2.go (new) | +580 |
| 4     | infer_module.go | +150, -100        |
| 5     | builder.go, dts.go | +100, -80      |
| 6     | compiler.go   | +10, -15            |
| 7     | *_test.go     | +200, -150          |
| 8     | dep_graph.go, cycles.go, dep_graph_v2.go, cycles_v2.go | -600, rename |

**Total estimated: ~1040 lines added, ~945 lines removed**

## Open Questions Resolution

Based on the existing codebase:

1. **Qualified names**: Keep current convention - root namespace bindings have no prefix, namespaced bindings use dot notation.

2. **Classes with dual bindings**: Store the same `*ast.ClassDecl` under both type and value keys (Option A from requirements).

3. **Declaration order**: Yes, use slices and append in source order to preserve declaration ordering for overloads and interface merging.
