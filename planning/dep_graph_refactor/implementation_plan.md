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
- [x] Step 4.1: Update InferModule
- [x] Step 4.2: Update InferDepGraph
- [x] Step 4.3: Update InferComponent
- [x] Step 4.4: Update GetNamespaceCtx

### Phase 5: Update Codegen (builder.go)
- [x] Step 5.1: Update Builder struct
- [x] Step 5.2: Update BuildTopLevelDecls
- [x] Step 5.3: Update BuildDefinitions

### Phase 6: Update Compiler
- [x] Step 6.1: Update compiler.go

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

### Step 3.2: Create cycles_v2.go with V2 cycle detection functions ✅

**File:** `internal/dep_graph/cycles_v2.go`

**Status:** Implemented

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

## Phase 4: Update Checker (infer_module.go) ✅

### Step 4.1: Update InferModule ✅

**File:** `internal/checker/infer_module_v2.go`

**Status:** Not created as a separate function

The V2 functions are in a separate file (`infer_module_v2.go`) that will be integrated into the main flow via the compiler. There is no separate `InferModuleV2` function; instead, `InferDepGraphV2` serves as the entry point.

### Step 4.2: Update InferDepGraph ✅

**File:** `internal/checker/infer_module_v2.go`

**Status:** Implemented (lines 17-25)

Simple implementation that iterates over components and calls `InferComponentV2`:

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

### Step 4.3: Update InferComponent ✅

**File:** `internal/checker/infer_module_v2.go`

**Status:** Fully implemented (lines 50-1094)

This is a comprehensive implementation that handles all declaration types with the new `BindingKey`-based structure. The implementation uses a multi-phase approach with careful handling of declaration processing order.

#### Key Implementation Details:

**Tracking Maps:**
- Uses `ast.Decl` as keys (not `BindingKey`) to maintain separate state for each declaration
- This is critical for overloaded functions and interface merging where multiple declarations share the same binding key
- Maps include: `paramBindingsForDecl`, `declCtxMap`, `typeRefsToUpdate`, `funcTypeForDecl`, `methodCtxForElem`

**Phase 1: Placeholder Creation** (lines 84-524):
- Iterates over binding keys and processes all declarations for each key
- Uses `processedPlaceholders` map to avoid processing classes/enums twice (they have both type and value keys)

Declaration-specific handling:
- **FuncDecl** (lines 97-139):
  - Creates individual `FuncType` for each declaration and stores in `funcTypeForDecl` map
  - Merges multiple declarations into `IntersectionType` in the binding for overloads
  - Stores overload declarations in `c.OverloadDecls` for codegen
  - Handles both first declaration and subsequent overloads

- **VarDecl** (lines 140-190):
  - Infers pattern type to create bindings
  - Handles type annotations with `AllowUndefinedTypeRefs` for recursive definitions
  - Tracks deferred type refs in `typeRefsToUpdate` for later resolution
  - Stores inferred type in `decl.InferredType` for use in definition phase

- **TypeDecl** (lines 191-203):
  - Creates placeholder type alias with fresh variable
  - Actual type inference happens in definition phase

- **ClassDecl** (lines 204-456):
  - Creates instance type and class object type
  - Processes all body elements (fields, methods, getters, setters)
  - Separates static vs instance elements
  - Handles extends clause
  - Stores method contexts in `methodCtxForElem` map for later body inference
  - Creates constructor with proper signature

- **EnumDecl** (lines 457-500):
  - Creates enum namespace
  - Creates placeholder type alias with fresh variable
  - Sets up context for inferring variants in definition phase

- **InterfaceDecl** (lines 501-522):
  - Creates placeholder type with fresh variable
  - Directly sets in namespace (not using SetTypeAlias) to allow interface merging
  - Merging happens in definition phase

**Phase 2: Definition Inference - Pass 1** (lines 531-1030):
Processes FuncDecl, TypeDecl, InterfaceDecl, EnumDecl, ClassDecl (skips VarDecl):

- Uses `processedDefinitionsPass1` map to avoid re-processing
- Skips declarations with `declare` keyword (except types)

- **FuncDecl** (lines 566-579):
  - Reuses individual `FuncType` from `funcTypeForDecl` map
  - Infers function body with proper parameter bindings
  - Critical for overloads: each overload has its own FuncType

- **TypeDecl** (lines 582-593):
  - Infers actual type from type annotation
  - Unifies with placeholder created in phase 1
  - Validates type parameters

- **InterfaceDecl** (lines 594-622):
  - Infers interface type
  - If interface already exists (from previous declaration), merges elements
  - Otherwise unifies with placeholder
  - Handles interface merging for declaration merging

- **EnumDecl** (lines 623-763):
  - Creates variant types with constructors
  - Generates Symbol.customMatcher methods for pattern matching
  - Builds union type from all variants
  - Unifies with placeholder

- **ClassDecl** (lines 764-1028):
  - Infers field initializers and method bodies
  - Handles static vs instance elements separately
  - Processes fields, methods, getters, setters
  - Properly sets up `self` parameter for instance members

**Phase 3: Definition Inference - Pass 2** (lines 1032-1083):
Processes VarDecl initializers:

- Uses `processedDefinitionsPass2` map
- Allows VarDecl to reference types/functions defined in pass 1
- Handles destructuring patterns that create multiple bindings
- Unifies initializer type with pattern type or type annotation

**Phase 4: Type Reference Resolution** (lines 1085-1091):
- Resolves deferred type references that were marked in phase 1
- Enables recursive definitions between type and variable declarations

### Step 4.4: Update GetNamespaceCtx ✅

**File:** `internal/checker/infer_module_v2.go`

**Status:** Implemented (lines 27-48)

Creates a context with the appropriate namespace for a binding key. Handles nested namespaces by splitting on "." and creating/navigating the namespace hierarchy:

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
    ns := ctx.Scope.Namespace
    nsCtx := ctx
    for part := range strings.SplitSeq(nsName, ".") {
        if _, ok := ns.Namespaces[part]; !ok {
            ns.Namespaces[part] = type_system.NewNamespace()
        }
        ns = ns.Namespaces[part]
        nsCtx = nsCtx.WithNewScopeAndNamespace(ns)
    }
    return nsCtx
}
```

## Phase 5: Update Codegen (builder.go) ✅

**Status:** V2 implementations completed in `internal/codegen/builder_v2.go`

The V2 versions of the codegen functions have been implemented in a separate file `builder_v2.go`:
- `BuildTopLevelDeclsV2(depGraph *dep_graph.DepGraphV2) *Module` - Generates JavaScript code
- `BuildDefinitionsV2(depGraph *dep_graph.DepGraphV2, moduleNS *type_sys.Namespace) *Module` - Generates .d.ts definitions
- `buildNamespaceStatementsV2(depGraph *dep_graph.DepGraphV2) []Stmt` - Helper for namespace creation

Key differences from the original:
- Uses `BindingKey` instead of `DeclID` throughout
- Automatically handles overloaded functions (grouped by binding key)
- No longer requires `overloadDecls` parameter
- Handles interface merging (multiple declarations per binding)
- Skips type-only bindings that don't have corresponding value bindings
- Tracks processed declarations to avoid duplicating code for destructuring patterns

### Step 5.1: Update Builder struct ✅

**File:** `internal/codegen/builder.go`

**Status:** Builder struct remains unchanged

The existing Builder struct was not modified. Instead, the V2 functions work with the existing struct and manage the dep graph fields appropriately:
- Sets `b.depGraph = nil` (old version not used)
- Sets `b.depGraphV2 = depGraph` (stores V2 graph for namespace lookups)

The Builder struct already had a `depGraphV2` field added in previous work.

### Step 5.2: Update BuildTopLevelDecls ✅

**File:** `internal/codegen/builder_v2.go`

**Status:** Fully implemented (lines 16-144)

Implemented as `BuildTopLevelDeclsV2`. Generates JavaScript code from the dependency graph.

#### Key Implementation Details:

**Initialization** (lines 17-26):
```go
b.depGraph = nil       // We're using V2, so set old depGraph to nil
b.depGraphV2 = depGraph // Store V2 dep graph for namespace lookups
b.isModule = true
var stmts []Stmt
nsStmts := b.buildNamespaceStatementsV2(depGraph)
stmts = slices.Concat(stmts, nsStmts)
```

**Duplicate Declaration Tracking** (lines 28-40):
- Uses `processedDecls` map to track which `ast.Decl` nodes have been processed
- Critical for VarDecl with pattern destructuring that creates multiple binding keys
- Example: `val C(D(msg), E(x, y)) = subject` creates three binding keys ("value:msg", "value:x", "value:y") all pointing to the same VarDecl
- Ensures code is only emitted once for such declarations

**Component Iteration** (lines 42-121):
Iterates over components in topological order, processing each binding key:

1. **Type-only binding skip** (lines 52-61):
   - Skips bindings with `DepKindType` that don't have a corresponding value binding
   - Type-only declarations (like `type Foo = ...` or standalone interfaces) don't generate JS code
   - Classes and enums have both type and value bindings, so they're handled via the value binding

2. **Overloaded function handling** (lines 63-85):
   - Checks if binding has multiple declarations that are all FuncDecls
   - Generates overloaded function dispatch using `buildOverloadedFunc`
   - For interface merging (multiple interface declarations), only first is used (interfaces don't generate runtime code)

3. **Single declaration processing** (lines 87-98):
   - Processes first declaration if not already processed
   - Uses existing `buildDeclWithNamespace` helper

4. **Namespace assignment** (lines 99-119):
   - For namespaced bindings (name contains "."), generates assignment to namespace
   - Example: `Foo.bar` gets assigned as `Foo.bar = Foo__bar`

**Extractor Import** (lines 123-139):
- Adds import statement for `InvokeCustomMatcherOrThrow` if extractors were used
- Prepends import to start of statements
- Resets `hasExtractor` flag

**Helper: buildNamespaceStatementsV2** (lines 148-162):
- Generates variable declarations for namespace objects
- Tracks defined namespaces to avoid redefinition
- Builds full hierarchy for nested namespaces

### Step 5.3: Update BuildDefinitions ✅

**File:** `internal/codegen/builder_v2.go`

**Status:** Fully implemented (lines 166-267)

Implemented as `BuildDefinitionsV2`. Generates TypeScript `.d.ts` definition files from the dependency graph.

#### Key Implementation Details:

**Namespace Grouping** (lines 171-183):
- Groups binding keys by their namespace
- Collects all binding keys in topological order from components
- Creates a map from namespace name to list of binding keys

**Namespace Processing** (lines 186-264):
Processes namespaces in sorted order for consistent output:

1. **Root namespace** (lines 202-223):
   - Declarations without a namespace go directly to module level
   - Processes all declarations for each binding key (to handle overloads and interface merging)
   - Uses `processedDecls` map to avoid duplicates (classes/enums have both type and value bindings)

2. **Named namespaces** (lines 224-263):
   - Finds the nested namespace in the type system using `findNamespace`
   - Processes all declarations for each binding key
   - Wraps statements in namespace declaration using `buildNamespaceDecl`
   - Only emits namespace if it has statements

**Declaration Processing:**
- Calls `buildDeclStmt` for each declaration to generate TypeScript definition
- Passes the appropriate namespace from the type system for type lookups
- Handles export flag based on whether it's root or nested namespace

## Phase 6: Update Compiler ✅

**Status:** Completed - updated to use V2 functions

### Step 6.1: Update compiler.go ✅

**File:** `internal/compiler/compiler.go`

**Status:** Fully implemented (lines 110, 119, 126-127)

Updated the `CompilePackage` function to use V2 functions:

**Changes made:**
1. **Line 110**: Changed `BuildDepGraph` → `BuildDepGraphV2`
   ```go
   depGraph := dep_graph.BuildDepGraphV2(inMod)
   ```

2. **Line 119**: Changed `InferDepGraph` → `InferDepGraphV2`
   ```go
   typeErrors := c.InferDepGraphV2(inferCtx, depGraph)
   ```

3. **Line 121**: Removed `MergeOverloadedFunctions` call
   - Added comment explaining it's no longer needed
   - Overloads are automatically grouped by `BindingKey` in the V2 implementation

4. **Line 126**: Changed `BuildTopLevelDecls` → `BuildTopLevelDeclsV2`
   ```go
   jsMod := builder.BuildTopLevelDeclsV2(depGraph)
   ```
   - No longer requires `overloadDecls` parameter

5. **Line 127**: Changed `BuildDefinitions` → `BuildDefinitionsV2`
   ```go
   dtsMod := builder.BuildDefinitionsV2(depGraph, libNS)
   ```

**Impact:**
- The compiler now uses the BindingKey-based dependency graph throughout
- Overloaded functions are handled seamlessly without manual merging
- Interface merging works automatically
- Code generation properly handles destructuring patterns and other edge cases

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
