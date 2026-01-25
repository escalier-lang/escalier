# DepGraph Refactor Requirements

## Problem Statement

The current `DepGraph` implementation uses `DeclID` (an integer index into a slice) as the primary key for tracking declarations and their dependencies. This approach has several limitations:

1. **Multiple declarations with the same name**: In Escalier, you can have multiple interface declarations or function declarations with the same name in the same module (declaration merging for interfaces, overloading for functions). The current design assigns each declaration a unique `DeclID`, but the binding maps (`ValueBindings`, `TypeBindings`) only store a single `DeclID` per name, causing later declarations to overwrite earlier ones.

2. **Complex overload merging**: The `MergeOverloadedFunctions` method is a post-processing step that attempts to merge overloaded function nodes by rebuilding the graph. This is error-prone and requires maintaining a separate `oldToNew` ID mapping.

3. **Index-based lookups are fragile**: Using slice indices as IDs means that any modification to the slice (insertion, deletion) invalidates existing IDs and requires rebuilding the entire graph.

## Proposed Solution

Replace `DeclID` with a composite key that combines:
1. **Qualified identifier**: The fully qualified name (e.g., `"foo.bar.MyType"` or `"createUser"`)
2. **Binding kind**: Whether the identifier refers to a value or type (`DepKindValue` or `DepKindType`)

This composite key uniquely identifies a logical binding, and the `DepGraph` will store **slices of declarations** for each key, allowing multiple declarations to be associated with the same binding.

## Updated Data Structures

### BindingKey (New)

```go
// BindingKey uniquely identifies a binding in the dependency graph
type BindingKey struct {
    Name string   // Fully qualified name (e.g., "foo.bar.MyType")
    Kind DepKind  // DepKindValue or DepKindType
}
```

### DepGraph (Updated)

```go
type DepGraph struct {
    // Map from binding key to all declarations that contribute to that binding.
    // For most bindings this will be a single declaration, but for interfaces
    // (declaration merging) and functions (overloading) there may be multiple.
    Decls map[BindingKey][]ast.Decl

    // Dependencies for each binding key.
    // The dependencies are the union of all dependencies from all declarations
    // that contribute to this binding.
    DeclDeps map[BindingKey]set.Set[BindingKey]

    // Namespace for each binding (derived from the qualified name, but stored
    // for convenience).
    DeclNamespace map[BindingKey]string

    // Strongly connected components of bindings (for topological ordering).
    // Each component is a slice of BindingKeys that form a cycle.
    Components [][]BindingKey

    // All namespace names in the module, indexed by NamespaceID.
    Namespaces []string
}
```

### Key Changes

1. **Remove `ValueBindings` and `TypeBindings`**: These are no longer needed because the `BindingKey` already encodes whether something is a value or type binding.

2. **Use maps instead of slices**: All the parallel slices (`Decls`, `DeclDeps`, `DeclNamespace`) become maps keyed by `BindingKey`.

3. **Decls stores slices**: Each `BindingKey` maps to a `[]ast.Decl` to support multiple declarations per binding.

4. **Dependencies are sets of BindingKeys**: Instead of `btree.Set[DeclID]`, dependencies are `set.Set[BindingKey]`.

## Functional Requirements

### FR-1: Building the Dependency Graph

1. When processing declarations, extract the qualified name and determine the binding kind(s).
2. Append declarations to the existing slice for that `BindingKey` (rather than overwriting).
3. For declarations that introduce both value and type bindings (e.g., classes, enums), create entries for both `BindingKey`s pointing to the same declaration.

### FR-2: Finding Dependencies

1. When finding dependencies for a binding, iterate through all declarations for that binding.
2. Collect dependencies from each declaration and store the union.
3. Dependencies should be expressed as `BindingKey`s, not `DeclID`s.

### FR-3: Strongly Connected Components

1. The SCC algorithm should operate on `BindingKey`s instead of `DeclID`s.
2. The resulting components should be `[][]BindingKey`.

### FR-4: Remove MergeOverloadedFunctions

1. The `MergeOverloadedFunctions` post-processing step should no longer be necessary.
2. Overloaded functions will naturally be grouped under the same `BindingKey`.
3. Their dependencies will be automatically unioned during dependency collection.

### FR-5: Accessor Methods

Update or add the following methods:

```go
// GetDecls returns all declarations for a binding key
func (g *DepGraph) GetDecls(key BindingKey) []ast.Decl

// GetDeps returns the dependencies for a binding key
func (g *DepGraph) GetDeps(key BindingKey) set.Set[BindingKey]

// GetNamespace returns the namespace for a binding key
func (g *DepGraph) GetNamespace(key BindingKey) string

// AllBindings returns all binding keys in the graph
func (g *DepGraph) AllBindings() []BindingKey

// HasBinding checks if a binding exists
func (g *DepGraph) HasBinding(key BindingKey) bool
```

## Non-Functional Requirements

### NFR-1: Deterministic Iteration

Maps in Go have non-deterministic iteration order. To ensure deterministic code generation:
- Use `btree.Map` instead of native Go maps, OR
- Maintain a separate sorted slice of `BindingKey`s for iteration, OR
- Sort keys before iteration in places where order matters

### NFR-2: Efficient Lookups

Lookups by `BindingKey` should be O(1) or O(log n). Consider using:
- `btree.Map[BindingKey, ...]` with a custom comparator
- Native Go maps with `BindingKey` as key (requires `BindingKey` to be comparable)

### NFR-3: Memory Efficiency

For the common case of single declarations per binding, avoid unnecessary allocations:
- Consider using a union type or pointer to distinguish single vs. multiple declarations
- Or accept the small overhead of single-element slices

## Migration Considerations

### Callers of DepGraph

Code that currently uses `DeclID` to access declarations will need to be updated:
- `checker/infer_module.go`
- `codegen/builder.go`
- Any code that iterates over `Components`

### Test Updates

All tests in `dep_graph_test.go` and `overload_test.go` will need to be updated to use the new API.

## Example Usage

```go
// Building the graph
depGraph := BuildDepGraph(module)

// Looking up declarations for a type
typeKey := BindingKey{Name: "MyInterface", Kind: DepKindType}
decls := depGraph.GetDecls(typeKey)
// decls may contain multiple InterfaceDecl if there's declaration merging

// Looking up declarations for an overloaded function
funcKey := BindingKey{Name: "process", Kind: DepKindValue}
decls := depGraph.GetDecls(funcKey)
// decls may contain multiple FuncDecl if there's overloading

// Getting dependencies
deps := depGraph.GetDeps(funcKey)
for dep := range deps {
    fmt.Printf("Depends on: %s (%v)\n", dep.Name, dep.Kind)
}

// Iterating in topological order
for _, component := range depGraph.Components {
    for _, key := range component {
        decls := depGraph.GetDecls(key)
        // Process declarations...
    }
}
```

## Open Questions

1. **Should `BindingKey.Name` always be fully qualified?**
   - Currently, root namespace bindings have no prefix (e.g., `"foo"` not `".foo"`)
   - Namespaced bindings have the namespace prefix (e.g., `"math.add"`)
   - This is consistent with current behavior but worth confirming

2. **How should we handle classes that introduce both type and value bindings?**
   - Option A: Store the same `*ast.ClassDecl` under both keys
   - Option B: Store under type key only, and have value lookups redirect
   - Recommendation: Option A for simplicity

3. **Should we preserve declaration order within a binding?**
   - For interfaces: order may matter for codegen (properties should maintain source order)
   - For overloaded functions: order matters for overload resolution
   - Recommendation: Yes, use slices and append in source order
