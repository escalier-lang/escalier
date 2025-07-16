# Implementation Spec: Namespaces Feature

## Overview

The namespaces feature will extend Escalier's module system to support hierarchical organization of declarations based on directory structure. This will enable better code organization within packages and provide TypeScript-compatible namespace semantics.

## Core Requirements

### 1. Directory-Based Namespace Mapping
- **Requirement**: Map directory structure to nested namespaces
- **Behavior**: Each subdirectory within a package's `lib/` creates a namespace
- **Example**: `lib/Foo/Bar/` creates namespace `Foo.Bar`

### 2. Symbol Resolution Rules
- **Parent Access**: Child namespaces can access parent namespace symbols without qualification
- **Child Access**: Parent namespaces must qualify child namespace symbols
- **Root Access**: Special `$Root` identifier for accessing shadowed root symbols

### 3. Strongly Connected Components
- **Requirement**: Maintain existing dependency analysis across namespaces
- **Behavior**: SCCs can span multiple namespaces within the same module

## Key Design Decisions

### Namespace-Qualified Binding Keys

The existing `DepGraph` uses string keys in `ValueBindings` and `TypeBindings` maps. To support namespaces, these keys will be modified to include namespace qualification:

**Examples of namespace-qualified keys:**
- Variable `x` in `lib/Foo/Bar/a.esc` → key: `"Foo.Bar.x"`
- Variable `x` in `lib/f.esc` (root) → key: `"x"`
- Type `Point` in `lib/Baz/types.esc` → key: `"Baz.Point"`
- Type `Config` in `lib/config.esc` (root) → key: `"Config"`

**Key generation algorithm:**
```go
func createQualifiedName(namespace []string, name string) string {
    if len(namespace) == 0 {
        return name  // Root namespace, no qualification needed
    }
    return strings.Join(namespace, ".") + "." + name
}
```

**Symbol resolution with qualified keys:**
When resolving an unqualified identifier `x` from namespace `["Foo", "Bar"]`, the resolver will:
1. Try `"Foo.Bar.x"` (current namespace)
2. Try `"Foo.x"` (parent namespace)  
3. Try `"x"` (root namespace)
4. If prefixed with `$Root.`, try `"x"` directly (skip namespace hierarchy)

This approach maintains compatibility with the existing `DepGraph` structure while adding namespace support through key naming conventions.

## Data Structure Changes

### 1. Namespace-Aware AST Extensions

```go
// internal/ast/namespace.go - New file
package ast

type NamespaceInfo struct {
    Path      []string  // e.g., ["Foo", "Bar"]
    SourceDir string    // Relative path from lib/
}

// Extend existing Module struct
type Module struct {
    Decls      []Decl
    Namespaces map[string]*NamespaceInfo  // Maps file paths to namespace info
}

// New namespace-qualified identifier
type NamespacedIdent struct {
    Namespace []string  // e.g., ["Foo", "Bar"] 
    Name      string    // e.g., "myFunction"
    IsRoot    bool      // true if prefixed with $Root
    span      Span
}

func (*NamespacedIdent) isQualIdent() {}
```

### 2. Enhanced Dependency Graph

```go
// internal/dep_graph/namespace.go - New file
package dep_graph

type NamespacedDeclID struct {
    DeclID    DeclID
    Namespace []string
}

type NamespaceDepGraph struct {
    *DepGraph
    NamespaceMap map[DeclID][]string  // Maps declarations to their namespaces
}

// Enhanced dependency graph building to use namespace-qualified keys
func BuildNamespaceDepGraph(module *ast.Module) *NamespaceDepGraph {
    // Build value and type bindings with namespace-qualified keys
    valueBindings := btree.Map[string, DeclID]{}
    typeBindings := btree.Map[string, DeclID]{}
    
    // For each declaration, create namespace-qualified binding keys
    // e.g., "Foo.Bar.x" for variable x in lib/Foo/Bar/file.esc
    // e.g., "x" for variable x in lib/file.esc (root namespace)
    
    return &NamespaceDepGraph{
        DepGraph: &DepGraph{
            Decls:         /* ... */,
            ValueBindings: valueBindings,  // Keys: "Foo.Bar.x", "Baz.y", "z" (root)
            TypeBindings:  typeBindings,   // Keys: "Foo.Bar.MyType", "GlobalType" (root)
            Dependencies:  /* ... */,
        },
        NamespaceMap: /* ... */,
    }
}
```

### 3. Checker Namespace Extensions

```go
// internal/checker/namespace.go - New file
package checker

type QualifiedIdent string

// Enhanced to support namespaces
func NewQualifiedIdent(namespace []string, name string) QualifiedIdent {
    if len(namespace) == 0 {
        return QualifiedIdent(name)
    }
    return QualifiedIdent(strings.Join(namespace, ".") + "." + name)
}

type NamespaceScope struct {
    *Scope
    CurrentNamespace []string
    ParentNamespaces map[string][]string  // Maps partial namespace paths to full paths
}
```

## Implementation Phases

### Phase 1: Parser Extensions

**File**: `internal/parser/namespace_parser.go` (new)

```go
package parser

func ParseNamespaceFiles(ctx context.Context, sources []*ast.Source) (*ast.Module, []*Error) {
    allDecls := []ast.Decl{}
    allErrors := []*Error{}
    namespaceMap := make(map[string]*ast.NamespaceInfo)
    
    for _, source := range sources {
        if source == nil {
            continue
        }
        
        // Extract namespace from file path
        namespaceInfo := extractNamespaceFromPath(source.Path)
        namespaceMap[source.Path] = namespaceInfo
        
        parser := NewParser(ctx, source)
        parser.currentNamespace = namespaceInfo.Path
        
        decls := parser.decls()
        allDecls = append(allDecls, decls...)
        allErrors = append(allErrors, parser.errors...)
    }
    
    return &ast.Module{
        Decls: allDecls,
        Namespaces: namespaceMap,
    }, allErrors
}

func extractNamespaceFromPath(filePath string) *ast.NamespaceInfo {
    // Extract namespace from file path relative to lib/
    // e.g., "lib/Foo/Bar/file.esc" -> ["Foo", "Bar"]
}
```

**Changes to existing parser**:
- Add namespace context to parser state
- Extend identifier parsing to handle `$Root` prefix
- Add parsing for namespace-qualified identifiers

### Phase 2: Enhanced Symbol Resolution

**File**: `internal/checker/namespace_resolver.go` (new)

```go
package checker

type NamespaceResolver struct {
    currentNamespace []string
    namespaceBindings map[string]map[string]*Binding  // namespace -> name -> binding
}

func (nr *NamespaceResolver) ResolveSymbol(name string, namespace []string) (*Binding, []string, error) {
    // 1. Check current namespace
    // 2. Check parent namespaces (bottom-up)
    // 3. Check qualified child namespaces
    // 4. Handle $Root special case
}

func (nr *NamespaceResolver) BuildNamespaceTree(module *ast.Module) {
    // Build hierarchical namespace structure from declarations
}
```

### Phase 3: Dependency Graph Extensions

**File**: `internal/dep_graph/namespace_deps.go` (new)

```go
package dep_graph

func BuildNamespaceDepGraph(module *ast.Module) *NamespaceDepGraph {
    valueBindings := btree.Map[string, DeclID]{}
    typeBindings := btree.Map[string, DeclID]{}
    namespaceMap := make(map[DeclID][]string)
    
    // Process each declaration and create namespace-qualified binding keys
    visitor := &ModuleBindingVisitor{
        DefaulVisitor: ast.DefaulVisitor{},
        ValueBindings: valueBindings,
        TypeBindings:  typeBindings,
        Decls:        btree.Map[DeclID, ast.Decl]{},
        nextDeclID:   0,
    }
    
    for filePath, namespaceInfo := range module.Namespaces {
        // Get declarations from this file and assign them to the namespace
        for _, decl := range getDeclarationsFromFile(module, filePath) {
            declID := visitor.generateDeclID()
            visitor.Decls.Set(declID, decl)
            namespaceMap[declID] = namespaceInfo.Path
            
            // Create namespace-qualified binding keys
            bindings := extractBindingsFromDecl(decl)
            for _, binding := range bindings {
                qualifiedName := createQualifiedName(namespaceInfo.Path, binding.Name)
                if binding.Kind == DepKindValue {
                    valueBindings.Set(qualifiedName, declID)
                } else if binding.Kind == DepKindType {
                    typeBindings.Set(qualifiedName, declID)
                }
            }
        }
    }
    
    // Build dependencies using the namespace-qualified keys
    dependencies := btree.Map[DeclID, btree.Set[DeclID]]{}
    for _, decl := range visitor.Decls.Values() {
        declID := getDeclID(decl, visitor.Decls)
        deps := FindDeclDependencies(decl, valueBindings, typeBindings)
        dependencies.Set(declID, deps)
    }
    
    return &NamespaceDepGraph{
        DepGraph: &DepGraph{
            Decls:         visitor.Decls,
            ValueBindings: valueBindings,
            TypeBindings:  typeBindings,
            Dependencies:  dependencies,
        },
        NamespaceMap: namespaceMap,
    }
}

// Helper function to create namespace-qualified names
func createQualifiedName(namespace []string, name string) string {
    if len(namespace) == 0 {
        return name  // Root namespace, no qualification needed
    }
    return strings.Join(namespace, ".") + "." + name
}

func (ndg *NamespaceDepGraph) FindNamespaceDependencies(declID DeclID) []NamespacedDeclID {
    // Find dependencies considering namespace resolution rules
    // This needs to handle the namespace-qualified keys in ValueBindings/TypeBindings
}
```

### Phase 4: Type Checker Integration

**Changes to** `internal/checker/infer.go`:

```go
func (c *Checker) InferNamespaceModule(ctx Context, m *ast.Module) (Namespace, []Error) {
    // Build namespace-aware dependency graph
    namespaceDepGraph := dep_graph.BuildNamespaceDepGraph(m)
    
    // Create namespace-aware context
    nsCtx := ctx.WithNamespaceScope(m.Namespaces)
    
    return c.InferNamespaceDepGraph(nsCtx, namespaceDepGraph)
}

func (c *Checker) InferNamespaceDepGraph(ctx Context, depGraph *dep_graph.NamespaceDepGraph) (Namespace, []Error) {
    // Process strongly connected components with namespace awareness
}
```

### Phase 5: Code Generation Updates

**Changes to** `internal/codegen/namespace_codegen.go` (new):

```go
package codegen

func (b *Builder) BuildNamespaceModule(mod *ast.Module) *Module {
    // Group declarations by namespace
    namespaceGroups := b.groupByNamespace(mod)
    
    // Generate namespace objects/modules
    stmts := []Stmt{}
    for namespace, decls := range namespaceGroups {
        namespaceStmts := b.buildNamespaceBlock(namespace, decls)
        stmts = append(stmts, namespaceStmts...)
    }
    
    return &Module{Stmts: stmts}
}

func (b *Builder) buildNamespaceBlock(namespace []string, decls []ast.Decl) []Stmt {
    // Generate nested namespace structure
    // Create appropriate export patterns
}
```

## File Structure Changes

### New Files to Create:

1. `internal/ast/namespace.go` - Namespace AST extensions
2. `internal/parser/namespace_parser.go` - Namespace-aware parsing
3. `internal/checker/namespace_resolver.go` - Symbol resolution logic  
4. `internal/checker/namespace_scope.go` - Namespace scope management
5. `internal/dep_graph/namespace_deps.go` - Namespace dependency analysis
6. `internal/codegen/namespace_codegen.go` - Namespace code generation

### Modified Files:

1. `internal/ast/ast.go` - Add NamespacedIdent to QualIdent interface
2. `internal/parser/parser.go` - Add namespace context to parser
3. `internal/parser/expr.go` - Handle namespaced identifiers
4. `internal/checker/infer.go` - Integrate namespace-aware inference
5. `internal/checker/scope.go` - Extend scope with namespace support
6. `internal/compiler/compiler.go` - Use namespace-aware compilation
7. `cmd/escalier/build.go` - Process directory structure for namespaces

## Testing Strategy

### Unit Tests:

1. **Namespace Extraction**: Test `extractNamespaceFromPath` function
2. **Symbol Resolution**: Test parent/child namespace access rules
3. **Dependency Analysis**: Test cross-namespace dependencies
4. **Code Generation**: Test namespace structure output

### Integration Tests:

Create test fixtures following the example structure:
```
fixtures/namespaces/
  basic/
    lib/
      Foo/
        Bar/
          a.esc
          b.esc
        c.esc
      Baz/
        d.esc
      e.esc
      f.esc
    expected_output.js
    expected_output.d.ts
```

## Migration Strategy

### Backward Compatibility:
- Existing single-file modules continue to work unchanged
- Flat directory structures (no subdirectories) work as before
- New namespace features are opt-in based on directory structure

### Rollout Plan:
1. **Phase 1**: Parser and AST extensions (no behavior change)
2. **Phase 2**: Symbol resolution with namespace support
3. **Phase 3**: Dependency graph with namespace awareness  
4. **Phase 4**: Type checker integration
5. **Phase 5**: Code generation for namespace structures

## Edge Cases and Error Handling

### Error Conditions:
1. **Conflicting Names**: Same symbol in parent and child namespace
2. **Circular Namespace References**: Namespace A references B, B references A
3. **Invalid $Root Usage**: $Root used outside of namespace context
4. **Missing Namespace**: Reference to non-existent namespace

### Error Messages:
```go
// internal/checker/namespace_errors.go (new)
package checker

type NamespaceError struct {
    Kind    NamespaceErrorKind
    Message string
    Span    ast.Span
}

type NamespaceErrorKind int

const (
    NamespaceNotFound NamespaceErrorKind = iota
    AmbiguousReference
    InvalidRootReference
    CircularNamespaceReference
)
```

## Performance Considerations

### Optimizations:
1. **Lazy Namespace Resolution**: Build namespace tree only when needed
2. **Cached Symbol Lookups**: Cache resolved symbols to avoid repeated traversal
3. **Incremental Compilation**: Only reprocess changed namespaces

### Memory Usage:
- Namespace information stored once per module
- Symbol resolution cache bounded by number of unique symbols
- Dependency graph size scales linearly with declarations

## Example Implementation Details

### Namespace Path Extraction

```go
func extractNamespaceFromPath(filePath string) *ast.NamespaceInfo {
    // Normalize path separators
    normalizedPath := filepath.ToSlash(filePath)
    
    // Find lib/ directory
    libIndex := strings.Index(normalizedPath, "/lib/")
    if libIndex == -1 {
        return &ast.NamespaceInfo{Path: []string{}, SourceDir: ""}
    }
    
    // Get relative path from lib/
    relativePath := normalizedPath[libIndex+5:] // +5 for "/lib/"
    
    // Remove filename to get directory path
    dirPath := filepath.Dir(relativePath)
    if dirPath == "." {
        return &ast.NamespaceInfo{Path: []string{}, SourceDir: ""}
    }
    
    // Split directory path into namespace components
    namespaceParts := strings.Split(dirPath, "/")
    
    return &ast.NamespaceInfo{
        Path:      namespaceParts,
        SourceDir: dirPath,
    }
}
```

### Symbol Resolution Algorithm

```go
func (nr *NamespaceResolver) ResolveSymbol(name string, currentNamespace []string) (*Binding, []string, error) {
    // Handle $Root prefix
    if strings.HasPrefix(name, "$Root.") {
        rootName := strings.TrimPrefix(name, "$Root.")
        if binding, exists := nr.namespaceBindings[""][rootName]; exists {
            return binding, []string{}, nil
        }
        return nil, nil, fmt.Errorf("symbol %s not found in root namespace", rootName)
    }
    
    // Check if name is already qualified (contains dots)
    if strings.Contains(name, ".") {
        return nr.resolveQualifiedSymbol(name)
    }
    
    // Search current namespace and parents (bottom-up)
    // Try namespace-qualified keys in dependency graph bindings
    for i := len(currentNamespace); i >= 0; i-- {
        searchNamespace := currentNamespace[:i]
        qualifiedName := createQualifiedName(searchNamespace, name)
        
        // Check in value bindings (using namespace-qualified keys)
        if declID, exists := nr.valueBindings.Get(qualifiedName); exists {
            binding := nr.getBindingFromDeclID(declID)
            return binding, searchNamespace, nil
        }
        
        // Check in type bindings (using namespace-qualified keys)  
        if declID, exists := nr.typeBindings.Get(qualifiedName); exists {
            binding := nr.getBindingFromDeclID(declID)
            return binding, searchNamespace, nil
        }
    }
    
    return nil, nil, fmt.Errorf("symbol %s not found", name)
}

// Enhanced dependency visitor to handle namespace-qualified symbol resolution
type NamespaceDependencyVisitor struct {
    ast.DefaulVisitor
    ValueBindings    btree.Map[string, DeclID] // Keys: "Foo.Bar.x", "y" (root)
    TypeBindings     btree.Map[string, DeclID] // Keys: "Foo.Bar.MyType", "GlobalType" (root)  
    Dependencies     btree.Set[DeclID]
    LocalBindings    []set.Set[string]
    CurrentNamespace []string // Current namespace context
}

// When visiting IdentExpr, resolve using namespace-qualified keys
func (v *NamespaceDependencyVisitor) VisitIdentExpr(expr *ast.IdentExpr) {
    name := expr.Name.Name
    
    // Skip if it's a local binding
    if v.isLocalBinding(name) {
        return
    }
    
    // Try to resolve symbol using namespace resolution rules
    for i := len(v.CurrentNamespace); i >= 0; i-- {
        searchNamespace := v.CurrentNamespace[:i]
        qualifiedName := createQualifiedName(searchNamespace, name)
        
        if declID, exists := v.ValueBindings.Get(qualifiedName); exists {
            v.Dependencies.Set(declID, true)
            return
        }
    }
    
    // Handle qualified names (e.g., "Foo.Bar.symbol")
    if strings.Contains(name, ".") {
        if declID, exists := v.ValueBindings.Get(name); exists {
            v.Dependencies.Set(declID, true)
        }
    }
}
```

This implementation spec provides a comprehensive roadmap for implementing the namespaces feature while maintaining compatibility with existing code and following Escalier's current architectural patterns.
