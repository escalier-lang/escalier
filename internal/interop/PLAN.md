# Plan: Converting dts_parser.Module to ast.Module

## Overview
This document outlines the plan for implementing a function that converts a `Module` from the `dts_parser` package to a `Module` from the `ast` package. This conversion is essential for integrating TypeScript `.d.ts` declarations into the Escalier compiler's type system.

## Source and Target Structures

### dts_parser.Module
```go
type Module struct {
    Statements []Statement  // Top-level declarations and statements
}
```

### ast.Module
```go
type Module struct {
    Namespaces btree.Map[string, *Namespace]
}

type Namespace struct {
    Decls []Decl
}
```

## Key Differences

1. **Structure**: 
   - `dts_parser.Module` has a flat list of `Statement` items
   - `ast.Module` organizes declarations into named namespaces using a btree map

2. **Statement vs Decl**:
   - `dts_parser` uses `Statement` interface (broader scope including imports/exports)
   - `ast` uses `Decl` interface (pure declarations only)

3. **Namespace Handling**:
   - `dts_parser` has `DeclareNamespace` as a statement type
   - `ast` requires all declarations to be placed in a namespace (default is root/global namespace)

## Conversion Strategy

### Phase 1: Core Type Mapping

Create conversion functions for fundamental types that appear in both systems:

#### 1.1 Type Annotations
- `dts_parser.TypeAnn` → `ast.TypeAnn`
  - PrimitiveType → corresponding ast type representation
  - TypeReference → ast type reference
  - FunctionType → ast function type
  - ArrayType, TupleType, UnionType, IntersectionType, etc.

#### 1.2 Identifiers and References
- `dts_parser.Ident` → `ast.Ident`
- `dts_parser.QualIdent` → `ast.QualIdent`
- `dts_parser.Member` → `ast.Member`

#### 1.3 Type Parameters
- `dts_parser.TypeParam` → `ast.TypeParam`
  - Map Name, Constraint, Default fields
  
#### 1.4 Function Parameters
- `dts_parser.Param` → `ast.Param`
  - Note: dts_parser.Param has `Name *Ident, Type TypeAnn, Optional, Rest bool`
  - ast.Param has `Pattern Pat, Optional bool, TypeAnn TypeAnn`
  - Need to convert `Name` to a pattern (IdentPat)

### Phase 2: Declaration Conversion

Map dts_parser Statement types to ast Decl types:

#### 2.1 Variable Declarations
`DeclareVariable` → `VarDecl`
- Map `Name` to `Pattern` (create IdentPat)
- Map `TypeAnn` to `TypeAnn`
- Map `Readonly` to `Kind` (const vs val)
- Set `Init` to nil (declarations don't have initializers)
- Set `declare` to true

#### 2.2 Function Declarations
`DeclareFunction` → `FuncDecl`
- Map `Name` → `Name`
- Map `TypeParams` → `TypeParams`
- Map `Params` → `Params` (convert each parameter)
- Map `ReturnType` → `Return`
- Set `Body` to nil
- Set `Throws` to nil (unless we parse JSDoc for throws)
- Set `declare` to true

#### 2.3 Type Alias Declarations
`DeclareTypeAlias` → `TypeDecl`
- Map `Name` → `Name`
- Map `TypeParams` → `TypeParams`
- Map `TypeAnn` → `TypeAnn`
- Set `declare` to true

#### 2.4 Enum Declarations
`DeclareEnum` → `EnumDecl`
- Map `Name` → `Name`
- Map `Members` to `Elems` (EnumVariant)
- Handle `Const` modifier (may need special handling)
- Note: TypeScript enums are different from Escalier enums
  - TS enums are value-level constructs with numeric/string values
  - Escalier enums are algebraic data types
  - **Decision needed**: How to represent TS enums in Escalier?
    - Option 1: Convert to type alias with union of literal types
    - Option 2: Create a special EnumDecl variant
    - Option 3: Skip for now and add TODO

#### 2.5 Class Declarations
`DeclareClass` → `ClassDecl`
- Map `Name` → `Name`
- Map `TypeParams` → `TypeParams`
- Map `Members` to `Body` (ClassElem)
  - Convert each member type:
    - `ConstructorDecl` → constructor representation in Escalier
    - `MethodDecl` → `MethodElem`
    - `PropertyDecl` → `FieldElem`
    - `GetterDecl` → `GetterElem`
    - `SetterDecl` → `SetterElem`
- Handle `Extends`, `Implements` (need to determine representation)
- Handle `Abstract` modifier
- Set `declare` to true

#### 2.6 Interface Declarations
`DeclareInterface` → **Challenge**
- Escalier doesn't have first-class interfaces
- **Decision needed**: How to represent interfaces?
  - Option 1: Convert to TypeDecl with ObjectType
  - Option 2: Extend ast to support interface declarations
  - Option 3: Merge with classes (interfaces as abstract classes)
  - **Recommended**: Convert to TypeDecl for now

#### 2.7 Namespace Declarations
`DeclareNamespace` → Populate namespace in `ast.Module`
- Recursively convert nested statements
- Create or add to namespace in the btree map
- Handle nested namespaces (create qualified names like "A.B.C")

#### 2.8 Module Declarations
`DeclareModule` → **Challenge**
- TypeScript module declarations (ambient modules)
- Similar to namespaces but for module augmentation
- **Decision needed**: Treat as namespace or special handling?

### Phase 3: Import/Export Handling

#### 3.1 Import Declarations
`ImportDecl` → **Challenge**
- ast.Module doesn't have explicit import representation
- **Decisions needed**:
  - Option 1: Skip imports (they're mainly for resolution)
  - Option 2: Store in a separate imports field (requires extending ast.Module)
  - Option 3: Store as metadata/comments
  - **Recommended**: Skip for initial implementation, add TODO

#### 3.2 Export Declarations
`ExportDecl` → Set `export` flag on declarations
- For `export default`, need special handling
- For `export { ... }`, map to exported declarations
- For re-exports (`export * from`), need to track module references

### Phase 4: Advanced Type Features

#### 4.1 Class/Interface Members
Convert member signatures:
- `CallSignature` → Function type in object type
- `ConstructSignature` → Constructor type
- `MethodSignature` → Method in object type
- `PropertySignature` → Property in object type
- `IndexSignature` → Index signature in object type
- `GetterSignature`, `SetterSignature` → Getter/setter representations

#### 4.2 Complex Type Annotations
- `ConditionalType` → May not be supported in Escalier
- `MappedType` → May not be supported in Escalier
- `TemplateLiteralType` → May be supported
- `IndexedAccessType` → May be supported
- `KeyOfType`, `TypeOfType` → Check support
- `ImportType` → Special handling for type imports
- `TypePredicate` → Type guard representation
- `InferType` → Part of conditional types

**Decision needed**: Which advanced types to support vs. skip

### Phase 5: Modifiers and Metadata

#### 5.1 Modifiers
Map `dts_parser.Modifiers` to ast equivalents:
- `Public`, `Private`, `Protected` → Private field in ast
- `Static` → Static field in ast
- `Readonly` → Readonly field in ast
- `Abstract` → May need special handling
- `Async` → Async field in FuncSig
- `Declare` → declare flag (always true for .d.ts)

#### 5.2 Optional/Rest
- `Optional` → Optional field
- `Rest` → Convert to rest pattern/param

### Phase 6: Namespace Organization

#### 6.1 Root Namespace
All non-namespaced declarations go into the root/global namespace:
- Use empty string "" or a special constant as the key
- Or use a predefined constant like "global"

#### 6.2 Nested Namespaces
For `DeclareNamespace`:
- Create nested namespace structure
- Use qualified names as keys (e.g., "Foo.Bar.Baz")
- Or create nested Namespace structures (if ast supports it)

#### 6.3 Module Augmentation
TypeScript allows augmenting existing modules:
```typescript
declare module "existing-module" {
    export function newFunction(): void;
}
```
**Decision needed**: How to handle this in ast.Module?

## Implementation Plan

### Step 1: Helper Functions
Create utility functions for common conversions:
```go
// convertIdent converts dts_parser.Ident to ast.Ident
func convertIdent(id *dts_parser.Ident) *ast.Ident

// convertQualIdent converts dts_parser.QualIdent to ast.QualIdent
func convertQualIdent(qi dts_parser.QualIdent) ast.QualIdent

// convertTypeParam converts dts_parser.TypeParam to ast.TypeParam
func convertTypeParam(tp *dts_parser.TypeParam) (*ast.TypeParam, error)

// convertParam converts dts_parser.Param to ast.Param
func convertParam(p *dts_parser.Param) (*ast.Param, error)

// convertTypeAnn converts dts_parser.TypeAnn to ast.TypeAnn
func convertTypeAnn(ta dts_parser.TypeAnn) (ast.TypeAnn, error)

// convertModifiers extracts modifier flags
func convertModifiers(m dts_parser.Modifiers) (static, private, readonly bool)

// convertPropertyKey converts dts_parser.PropertyKey to ast.ObjKey
func convertPropertyKey(pk dts_parser.PropertyKey) (ast.ObjKey, error)

// convertInterfaceMember converts dts_parser.InterfaceMember to ast.ObjTypeAnnElem
func convertInterfaceMember(member dts_parser.InterfaceMember) (ast.ObjTypeAnnElem, error)
```

### Step 2: Statement to Decl Conversion
Create conversion function for each statement type:
```go
// convertStatement attempts to convert a Statement to a Decl
// Returns nil for statements that can't be represented as Decl (like imports)
func convertStatement(stmt dts_parser.Statement) (ast.Decl, error)

// Specific converters
func convertDeclareVariable(dv *dts_parser.DeclareVariable) (*ast.VarDecl, error)
func convertDeclareFunction(df *dts_parser.DeclareFunction) (*ast.FuncDecl, error)
func convertDeclareTypeAlias(dt *dts_parser.DeclareTypeAlias) (*ast.TypeDecl, error)
func convertDeclareEnum(de *dts_parser.DeclareEnum) (ast.Decl, error) // Return type TBD
func convertDeclareClass(dc *dts_parser.DeclareClass) (*ast.ClassDecl, error)
func convertDeclareInterface(di *dts_parser.DeclareInterface) (ast.Decl, error)
```

### Step 3: Class Member Conversion
```go
// convertClassMember converts dts_parser.ClassMember to ast.ClassElem
func convertClassMember(cm dts_parser.ClassMember) (ast.ClassElem, error)

// Specific converters
func convertMethodDecl(md *dts_parser.MethodDecl) (*ast.MethodElem, error)
func convertPropertyDecl(pd *dts_parser.PropertyDecl) (*ast.FieldElem, error)
func convertGetterDecl(gd *dts_parser.GetterDecl) (*ast.GetterElem, error)
func convertSetterDecl(sd *dts_parser.SetterDecl) (*ast.SetterElem, error)
func convertConstructorDecl(cd *dts_parser.ConstructorDecl) (ast.ClassElem, error) // TBD
```

### Step 4: Namespace Processing
```go
// processNamespace recursively processes a namespace and adds declarations
func processNamespace(
    name string,
    stmts []dts_parser.Statement,
    namespaces *btree.Map[string, *ast.Namespace],
) error

// mergeNamespace merges declarations into an existing namespace or creates new
func mergeNamespace(
    name string,
    decls []ast.Decl,
    namespaces *btree.Map[string, *ast.Namespace],
) error
```

### Step 5: Main Conversion Function
```go
// ConvertModule converts dts_parser.Module to ast.Module
func ConvertModule(dtsModule *dts_parser.Module) (*ast.Module, error) {
    var namespaces btree.Map[string, *ast.Namespace]
    
    // Process each statement
    for _, stmt := range dtsModule.Statements {
        switch s := stmt.(type) {
        case *dts_parser.DeclareNamespace:
            // Process namespace recursively
            if err := processNamespace(s.Name.Name, s.Statements, &namespaces); err != nil {
                return nil, fmt.Errorf("processing namespace %s: %w", s.Name.Name, err)
            }
        case *dts_parser.DeclareModule:
            // Handle module declarations
            // TBD: namespace or special handling
        case *dts_parser.ImportDecl, *dts_parser.ExportDecl:
            // Skip or handle separately
        case *dts_parser.AmbientDecl:
            // Unwrap and process the inner declaration
        default:
            // Convert to Decl and add to root namespace
            decl, err := convertStatement(s)
            if err != nil {
                return nil, fmt.Errorf("converting statement: %w", err)
            }
            if decl != nil {
                if err := mergeNamespace("", []ast.Decl{decl}, &namespaces); err != nil {
                    return nil, fmt.Errorf("merging to root namespace: %w", err)
                }
            }
        }
    }
    
    return &ast.Module{Namespaces: namespaces}, nil
}
```

## Open Questions & Decisions Needed

1. **TypeScript Enums**: How to represent in Escalier?
   - Recommend: Convert to union of literal types initially

2. **Interfaces**: How to represent in ast?
   - Recommend: Convert to TypeDecl with object type

3. **Imports**: Store, skip, or metadata?
   - Recommend: Skip initially, add TODO for later

4. **Module Declarations**: Namespace or special handling?
   - Recommend: Treat as namespaces

5. **Advanced Types**: Which to support?
   - Start with basic types, add advanced types incrementally
   - Document unsupported types with clear error messages

6. **Export Default**: How to handle?
   - May need special flag or naming convention

7. **Constructor in Classes**: Representation in Escalier?
   - Check if ClassDecl.Params represents constructor params

8. **Index Signatures**: How to represent in object types?
   - Check if ast object types support index signatures

9. **Root Namespace Name**: Empty string or "global"?
   - Recommend: Empty string "" for consistency

10. **Error Handling**: All conversion functions now return errors
    - Unsupported constructs return descriptive errors using fmt.Errorf
    - Errors are wrapped with context using %w for proper error chains
    - Callers can handle errors appropriately (fail fast, collect, log, etc.)

## Testing Strategy

1. **Unit Tests**: Test each converter function individually
   - Test with simple cases first
   - Add complex cases incrementally

2. **Integration Tests**: Test full module conversion
   - Use real .d.ts files from TypeScript stdlib
   - Start with simple modules, add complex ones

3. **Roundtrip Tests**: Parse .d.ts → convert → codegen → compare
   - May not be exact match but should be equivalent

4. **Fixture Tests**: Add to fixtures/ directory
   - Create test cases for each construct type

## Success Criteria

1. Can convert basic .d.ts declarations (variables, functions, types)
2. Can handle namespaces correctly
3. Can convert class declarations with members
4. Can handle type parameters and constraints
5. Gracefully handles or reports unsupported constructs
6. Comprehensive test coverage
7. Clear documentation of limitations

## Future Enhancements

1. Support for more advanced TypeScript types
2. Better import/export handling
3. Module augmentation support
4. JSDoc comment preservation
5. Source map generation for debugging
6. Incremental conversion for performance
