# Well-Known Symbols as Keys - Implementation Plan

## Overview

This plan outlines the implementation steps for supporting well-known symbols as computed property keys in the Escalier compiler, enabling parsing and type-checking of TypeScript's ES2015+ standard library files.

## Current State

### What's Working
- `lib.es5.d.ts` parsing and type inference
- `lib.dom.d.ts` parsing and type inference
- Declaration merging for interfaces via `MergeInterface()`
- Computed property support in Escalier source code (e.g., `[Symbol.customMatcher]`)
- `ObjectType.SymbolKeyMap` field exists but is not populated

### What's Missing
- Parsing `unique symbol` type in dts_parser
- Converting `ComputedKey` in interop layer (returns error)
- Recursive lib file loading with reference following
- Dependency graph awareness of computed keys
- Type inference for symbol-keyed properties

### Key Files

| File | Purpose |
|------|---------|
| `internal/dts_parser/ast.go` | Contains `ComputedKey` struct |
| `internal/dts_parser/object.go` | Parses object type members |
| `internal/interop/helper.go` | `convertPropertyKey` - needs ComputedKey support |
| `internal/checker/prelude.go` | Lib file loading |
| `internal/checker/infer_module.go` | Declaration processing and merging |
| `internal/type_system/types.go` | `ObjectType` with `SymbolKeyMap` |

---

## Phase 1: Recursive Lib File Loading (FR1)

**Goal:** Load lib files by following `/// <reference lib="..." />` directives recursively.

### Task 1.1: Parse Reference Directives

**Location:** `internal/dts_parser/`

Add support for extracting `/// <reference lib="..." />` directives from parsed files.

```go
// ReferenceDirective represents a /// <reference lib="..." /> directive
type ReferenceDirective struct {
    Lib  string   // e.g., "es2015.core"
    Span ast.Span
}

// Module should include reference directives
type Module struct {
    Statements []Statement
    References []ReferenceDirective  // NEW
}
```

**Implementation:**
1. Update the lexer to recognize `/// <reference` at the start of lines
2. Parse the `lib="..."` attribute
3. Collect directives in the Module

### Task 1.2: Implement Recursive Loading

**Location:** `internal/checker/prelude.go`

Create a function to load a target lib file and recursively follow all references.

```go
// loadLibFileRecursive loads a lib file and all its references.
// Returns all declarations from all visited files.
func (c *Checker) loadLibFileRecursive(
    libDir string,
    libName string,
    visited map[string]bool,
) ([]dts_parser.Statement, error) {
    filename := "lib." + libName + ".d.ts"
    if visited[filename] {
        return nil, nil // Already processed
    }
    visited[filename] = true

    path := filepath.Join(libDir, filename)
    module, err := c.parseLibFile(path)
    if err != nil {
        return nil, err
    }

    var allDecls []dts_parser.Statement

    // First, recursively load all references
    for _, ref := range module.References {
        refDecls, err := c.loadLibFileRecursive(libDir, ref.Lib, visited)
        if err != nil {
            return nil, err
        }
        allDecls = append(allDecls, refDecls...)
    }

    // Then add this file's declarations
    allDecls = append(allDecls, module.Statements...)

    return allDecls, nil
}
```

### Task 1.3: Update Prelude Loading

**Location:** `internal/checker/prelude.go`

Update `loadGlobalDefinitions` to use the new recursive loading:

```go
func (c *Checker) loadGlobalDefinitions(globalScope *Scope, targetVersion string) {
    libDir := filepath.Join(repoRoot, "node_modules", "typescript", "lib")
    visited := make(map[string]bool)

    // Load target version and all its references
    allDecls, err := c.loadLibFileRecursive(libDir, targetVersion, visited)
    if err != nil {
        panic(fmt.Sprintf("failed to load lib files: %v", err))
    }

    // Convert and merge all declarations into a single module
    // ...
}
```

### Task 1.4: Add Target Version Configuration

**Location:** `internal/checker/checker.go` or configuration

Add a way to specify the target ES version (default to `es2015` or a sensible default).

---

## Phase 2: Parse `unique symbol` Type (FR5)

**Goal:** Parse and represent `unique symbol` as a distinct type.

### Task 2.1: Add Lexer Support

**Location:** `internal/dts_parser/lexer.go`

Add `UNIQUE` as a keyword token.

### Task 2.2: Add AST Node

**Location:** `internal/dts_parser/ast.go`

```go
// UniqueSymbolType represents the `unique symbol` type
type UniqueSymbolType struct {
    span ast.Span
}

func (u *UniqueSymbolType) Span() ast.Span { return u.span }
func (*UniqueSymbolType) isTypeAnn()       {}
```

### Task 2.3: Parse `unique symbol`

**Location:** `internal/dts_parser/base.go` or appropriate parser file

```go
func (p *Parser) parseType() TypeAnn {
    if p.match(UNIQUE) {
        if !p.match(SYMBOL) {
            p.error("expected 'symbol' after 'unique'")
        }
        return &UniqueSymbolType{span: ...}
    }
    // ... existing type parsing
}
```

### Task 2.4: Add Type System Representation

**Location:** `internal/type_system/types.go`

```go
// UniqueSymbolType represents a unique symbol type.
// Each unique symbol declaration creates a nominally distinct type.
type UniqueSymbolType struct {
    ID         int    // Unique identifier for this symbol
    Name       string // Optional: the property name (e.g., "iterator")
    provenance Provenance
}

var uniqueSymbolCounter int = 0

func NewUniqueSymbolType(name string, prov Provenance) *UniqueSymbolType {
    uniqueSymbolCounter++
    return &UniqueSymbolType{
        ID:         uniqueSymbolCounter,
        Name:       name,
        provenance: prov,
    }
}
```

### Task 2.5: Add Interop Conversion

**Location:** `internal/interop/helper.go`

```go
case *dts_parser.UniqueSymbolType:
    return ast.NewUniqueSymbolTypeAnn(convertSpan(t.Span())), nil
```

### Task 2.6: Add AST Type Annotation

**Location:** `internal/ast/type_ann.go`

```go
type UniqueSymbolTypeAnn struct {
    span Span
}

func NewUniqueSymbolTypeAnn(span Span) *UniqueSymbolTypeAnn {
    return &UniqueSymbolTypeAnn{span: span}
}
```

### Task 2.7: Infer Unique Symbol Types

**Location:** `internal/checker/infer_type_ann.go`

```go
case *ast.UniqueSymbolTypeAnn:
    // Each unique symbol creates a fresh, nominally distinct type
    return type_system.NewUniqueSymbolType("", ctx.Provenance), nil
```

---

## Phase 3: Convert Computed Property Keys (FR4)

**Goal:** Convert `ComputedKey` from dts_parser AST to Escalier AST.

### Task 3.1: Add `convertTypeAnnToExpr` Helper

**Location:** `internal/interop/helper.go`

This helper converts type annotations used in computed key contexts to expressions:

```go
// convertTypeAnnToExpr converts a type annotation (used in computed key context)
// to an expression. Only supports patterns valid in computed keys.
func convertTypeAnnToExpr(typeAnn dts_parser.TypeAnn) (ast.Expr, error) {
    switch t := typeAnn.(type) {
    case *dts_parser.IdentTypeAnn:
        // [foo] -> Ident("foo")
        return ast.NewIdent(t.Name.Name, convertSpan(t.Span())), nil

    case *dts_parser.MemberTypeAnn:
        // [Symbol.iterator] -> Member(Ident("Symbol"), Ident("iterator"))
        left, err := convertTypeAnnToExpr(t.Left)
        if err != nil {
            return nil, err
        }
        right := ast.NewIdent(t.Right.Name, convertSpan(t.Right.Span()))
        return ast.NewMember(left, right, convertSpan(t.Span())), nil

    case *dts_parser.TypeofTypeAnn:
        // [typeof foo] -> needs special handling
        return nil, fmt.Errorf("typeof in computed key not yet supported")

    default:
        return nil, fmt.Errorf("unsupported type annotation in computed key: %T", typeAnn)
    }
}
```

### Task 3.2: Implement ComputedKey Conversion

**Location:** `internal/interop/helper.go`

Update `convertPropertyKey`:

```go
case *dts_parser.ComputedKey:
    expr, err := convertTypeAnnToExpr(k.Expr)
    if err != nil {
        return nil, fmt.Errorf("converting computed key: %w", err)
    }
    return ast.NewComputedKey(expr, convertSpan(k.Span())), nil
```

### Task 3.3: Verify AST ComputedKey Exists

**Location:** `internal/ast/`

Ensure `ast.ComputedKey` exists and can be used as an `ObjKey`:

```go
type ComputedKey struct {
    Expr Expr
    span Span
}

func NewComputedKey(expr Expr, span Span) *ComputedKey {
    return &ComputedKey{Expr: expr, span: span}
}

func (c *ComputedKey) Span() Span { return c.span }
func (*ComputedKey) isObjKey()    {}
```

---

## Phase 4: Dependency Graph for Computed Keys (FR3)

**Goal:** Ensure declarations are processed in correct order when computed keys create dependencies.

### Task 4.1: Detect Computed Key Dependencies

**Location:** `internal/dep_graph/`

When building the dependency graph, recognize that a computed key like `[Symbol.iterator]` creates a dependency on the variable `Symbol`.

```go
// When processing an interface declaration with computed keys:
func (g *DepGraph) addComputedKeyDependencies(decl *ast.InterfaceDecl) {
    for _, elem := range decl.Body.Elems {
        switch e := elem.(type) {
        case *ast.PropertyTypeAnn:
            if computedKey, ok := e.Key.(*ast.ComputedKey); ok {
                // Extract the variable reference from the computed key
                // e.g., [Symbol.iterator] depends on "Symbol"
                varName := extractRootIdent(computedKey.Expr)
                if varName != "" {
                    g.addDependency(decl, varName, DependencyKindValue)
                }
            }
        case *ast.MethodTypeAnn:
            // Similar handling for method computed keys
        }
    }
}

func extractRootIdent(expr ast.Expr) string {
    switch e := expr.(type) {
    case *ast.Ident:
        return e.Name
    case *ast.Member:
        return extractRootIdent(e.Left)
    default:
        return ""
    }
}
```

### Task 4.2: Group Same-Named Interface Declarations

**Location:** `internal/checker/infer_module.go`

Before processing declarations, group all interface declarations with the same name:

```go
// Group interface declarations by name
func groupInterfaceDecls(decls []ast.Decl) map[string][]*ast.InterfaceDecl {
    groups := make(map[string][]*ast.InterfaceDecl)
    for _, decl := range decls {
        if iface, ok := decl.(*ast.InterfaceDecl); ok {
            groups[iface.Name.Name] = append(groups[iface.Name.Name], iface)
        }
    }
    return groups
}
```

### Task 4.3: Process Interface Groups Together

**Location:** `internal/checker/infer_module.go`

When processing the dependency graph, treat all same-named interfaces as a single unit:

```go
// In the dependency graph, all interfaces with the same name should be
// represented as a single node. When that node is processed, all the
// interface declarations are inferred and merged together.
func (c *Checker) processInterfaceGroup(ctx Context, decls []*ast.InterfaceDecl) (*type_system.TypeAlias, []Error) {
    var mergedElems []type_system.ObjTypeElem
    var errors []Error

    for _, decl := range decls {
        typeAlias, declErrors := c.inferInterface(ctx, decl)
        errors = append(errors, declErrors...)

        if objType, ok := typeAlias.Type.(*type_system.ObjectType); ok {
            mergedElems = append(mergedElems, objType.Elems...)
        }
    }

    // Create merged type with all elements
    mergedType := &type_system.ObjectType{
        Elems:     mergedElems,
        Interface: true,
        // ... other fields
    }

    return &type_system.TypeAlias{
        Name:       decls[0].Name.Name,
        Type:       mergedType,
        TypeParams: // from first declaration
    }, errors
}
```

---

## Phase 5: Infer Symbol-Keyed Properties (FR6)

**Goal:** Properly infer and track symbol-keyed properties in the type system.

### Task 5.1: Evaluate Computed Key Expressions

**Location:** `internal/checker/infer_stmt.go` or new file

When inferring an interface with a computed key, evaluate the key expression:

```go
func (c *Checker) evaluateComputedKey(ctx Context, key *ast.ComputedKey) (type_system.Type, error) {
    // Infer the type of the key expression
    keyType, err := c.inferExpr(ctx, key.Expr)
    if err != nil {
        return nil, err
    }

    // The key type should be a unique symbol
    if uniqueSym, ok := keyType.(*type_system.UniqueSymbolType); ok {
        return uniqueSym, nil
    }

    return nil, fmt.Errorf("computed key must be a unique symbol, got %T", keyType)
}
```

### Task 5.2: Populate SymbolKeyMap

**Location:** `internal/checker/infer_stmt.go`

When creating an ObjectType with symbol keys, populate the SymbolKeyMap:

```go
func (c *Checker) inferInterfaceWithSymbolKeys(ctx Context, decl *ast.InterfaceDecl) (*type_system.ObjectType, []Error) {
    objType := &type_system.ObjectType{
        Interface:    true,
        SymbolKeyMap: make(map[int]any),
    }

    for _, elem := range decl.Body.Elems {
        switch e := elem.(type) {
        case *ast.PropertyTypeAnn:
            if computedKey, ok := e.Key.(*ast.ComputedKey); ok {
                symbolType, err := c.evaluateComputedKey(ctx, computedKey)
                if err != nil {
                    // handle error
                    continue
                }
                if uniqueSym, ok := symbolType.(*type_system.UniqueSymbolType); ok {
                    // Store the mapping from symbol ID to the original expression
                    objType.SymbolKeyMap[uniqueSym.ID] = computedKey.Expr

                    // Create the property with the symbol as key
                    propType, _ := c.inferTypeAnn(ctx, e.TypeAnn)
                    objType.Elems = append(objType.Elems, &type_system.Property{
                        Key:      uniqueSym, // Use the symbol type as key
                        Value:    propType,
                        Optional: e.Optional,
                        Readonly: e.Readonly,
                    })
                }
            }
        // ... handle other element types
        }
    }

    return objType, nil
}
```

### Task 5.3: Update Property Type to Support Symbol Keys

**Location:** `internal/type_system/types.go`

Ensure `Property` can have a symbol as its key:

```go
type Property struct {
    Key      interface{} // string for regular keys, *UniqueSymbolType for symbol keys
    Value    Type
    Optional bool
    Readonly bool
}
```

---

## Phase 6: Symbol Key Property Access (FR7)

**Goal:** Support accessing properties via symbol keys (e.g., `arr[Symbol.iterator]`).

### Task 6.1: Handle Symbol Index Access

**Location:** `internal/checker/infer_expr.go`

When type-checking index access with a symbol:

```go
func (c *Checker) inferIndexExpr(ctx Context, expr *ast.IndexExpr) (type_system.Type, []Error) {
    objType, errors := c.inferExpr(ctx, expr.Object)
    indexType, indexErrors := c.inferExpr(ctx, expr.Index)
    errors = append(errors, indexErrors...)

    // Check if the index is a unique symbol
    if uniqueSym, ok := indexType.(*type_system.UniqueSymbolType); ok {
        // Look up the property by symbol ID
        if obj, ok := objType.(*type_system.ObjectType); ok {
            for _, elem := range obj.Elems {
                if prop, ok := elem.(*type_system.Property); ok {
                    if propSym, ok := prop.Key.(*type_system.UniqueSymbolType); ok {
                        if propSym.ID == uniqueSym.ID {
                            return prop.Value, errors
                        }
                    }
                }
            }
        }
        // Symbol key not found
        return type_system.NewUnknownType(), append(errors,
            c.newError("property with symbol key not found"))
    }

    // ... existing index access handling
}
```

---

## Phase 7: Testing

### Task 7.1: Parser Tests

**Location:** `internal/dts_parser/parser_test.go`

```go
func TestParseUniqueSymbol(t *testing.T) {
    source := `interface SymbolConstructor {
        readonly iterator: unique symbol;
    }`
    // Test parsing succeeds and produces correct AST
}

func TestParseComputedKey(t *testing.T) {
    source := `interface Iterable<T> {
        [Symbol.iterator](): Iterator<T>;
    }`
    // Test parsing succeeds and produces ComputedKey with MemberTypeAnn
}
```

### Task 7.2: Lib File Parsing Tests

**Location:** `internal/dts_parser/integration_test.go`

```go
func TestParseES2015LibFiles(t *testing.T) {
    libFiles := []string{
        "lib.es2015.symbol.d.ts",
        "lib.es2015.symbol.wellknown.d.ts",
        "lib.es2015.iterable.d.ts",
        // ... all ES2015 files
    }
    for _, filename := range libFiles {
        t.Run(filename, func(t *testing.T) {
            // Parse and verify no errors
        })
    }
}
```

### Task 7.3: Interop Tests

**Location:** `internal/interop/interop_test.go`

```go
func TestConvertComputedKey(t *testing.T) {
    // Test that ComputedKey with Symbol.iterator converts correctly
}

func TestConvertUniqueSymbol(t *testing.T) {
    // Test that unique symbol type annotation converts correctly
}
```

### Task 7.4: Type Inference Tests

**Location:** `internal/checker/tests/`

```go
func TestSymbolKeyedProperty(t *testing.T) {
    source := `
        val arr = [1, 2, 3]
        val iter = arr[Symbol.iterator]()
    `
    // Verify iter has type ArrayIterator<number>
}

func TestIterableInterface(t *testing.T) {
    source := `
        val obj: Iterable<string> = {
            [Symbol.iterator]() {
                return { next: () => ({ done: true, value: undefined }) }
            }
        }
    `
    // Verify type checking succeeds
}
```

### Task 7.5: Declaration Merging Tests

**Location:** `internal/checker/tests/`

```go
func TestSymbolConstructorMerging(t *testing.T) {
    // Verify that SymbolConstructor from multiple lib files merges correctly
    // and Symbol.iterator, Symbol.toPrimitive, etc. are all available
}
```

---

## Implementation Order

### Recommended Sequence

```
Phase 2: unique symbol (foundation)
    ↓
Phase 3: ComputedKey conversion (enables parsing)
    ↓
Phase 1: Recursive lib loading (enables loading ES2015)
    ↓
Phase 4: Dependency graph (correct processing order)
    ↓
Phase 5: Symbol key inference (type system support)
    ↓
Phase 6: Property access (complete feature)
    ↓
Phase 7: Testing (throughout)
```

### Rationale

1. **Phase 2 first**: `unique symbol` is a simple, isolated change that unblocks parsing
2. **Phase 3 second**: ComputedKey conversion is needed before lib files can be loaded
3. **Phase 1 third**: Once parsing works, enable recursive loading
4. **Phase 4 fourth**: Ensure declarations are processed in correct order
5. **Phases 5-6**: Build on the foundation to complete type system support

---

## Checkpoints

### Checkpoint 1: Parsing Works
- [ ] `unique symbol` parses correctly
- [ ] `[Symbol.iterator]` parses as ComputedKey
- [ ] All ES2015 lib files parse without errors

### Checkpoint 2: Interop Works
- [ ] ComputedKey converts to Escalier AST
- [ ] UniqueSymbolType converts correctly
- [ ] No errors when converting ES2015 lib files

### Checkpoint 3: Loading Works
- [ ] Reference directives are parsed
- [ ] Recursive loading follows all references
- [ ] All declarations from all files are collected

### Checkpoint 4: Type Inference Works
- [ ] SymbolConstructor interface has all well-known symbols
- [ ] Symbol variable has type SymbolConstructor
- [ ] Iterable<T> has [Symbol.iterator] method
- [ ] Array<T> has [Symbol.iterator] from merged declarations

### Checkpoint 5: Property Access Works
- [ ] `arr[Symbol.iterator]()` type-checks correctly
- [ ] Symbol-keyed properties can be accessed
- [ ] Type display shows `[Symbol.iterator]` not internal IDs

---

## Risks and Mitigations

### Risk: Parser Changes Break Existing Code

**Mitigation:** Run full test suite after each parser change. The `unique` keyword should only be recognized in type positions.

### Risk: Dependency Graph Changes Affect Performance

**Mitigation:** Benchmark before and after. The additional dependency tracking should be O(n) in the number of declarations.

### Risk: Symbol Identity Issues

**Mitigation:** Use a single counter for unique symbol IDs. Ensure the counter is only incremented when inferring `unique symbol` type annotations, not when referencing existing symbols.

---

## Success Criteria

All items from requirements.md Success Criteria section:

### Parser Criteria
1. [ ] `lib.es2015.symbol.d.ts` parses without errors
2. [ ] `lib.es2015.symbol.wellknown.d.ts` parses without errors
3. [ ] `lib.es2015.iterable.d.ts` parses without errors
4. [ ] All 9 ES2015 lib files parse without errors

### Interop Criteria
5. [ ] Computed keys with `Symbol.X` expressions convert correctly
6. [ ] `unique symbol` type converts to a distinct type representation
7. [ ] Interface declarations with symbol keys produce correct Escalier AST

### Type System Criteria
8. [ ] `ObjectType.SymbolKeyMap` is populated for interfaces with symbol keys
9. [ ] Multiple interface declarations with the same name merge correctly
10. [ ] `unique symbol` properties on `SymbolConstructor` have distinct types

### Type Checking Criteria
11. [ ] `arr[Symbol.iterator]()` type-checks correctly for arrays
12. [ ] `Iterable<T>` interface is available with `[Symbol.iterator]` method
13. [ ] Declaration merging across lib files produces complete interfaces
