# Conditional Types Implementation Specification

## Overview

This document describes the implementation changes required to fully support conditional types in Escalier as specified in the requirements document. The implementation will follow TypeScript's conditional type semantics, including distributive conditional types and inference within conditional types.

## Current Status

Based on code analysis, conditional types are partially implemented:

✅ **Implemented:**
- AST node definition (`CondTypeAnn` in `internal/ast/type_ann.go`)  
- Parser support for `if ... : ... { ... } else { ... }` syntax
- Type system definition (`CondType` in `internal/type_system/types.go`)
- Code generation support for `.d.ts` files

❌ **Missing:**
- Type inference for conditional types in the checker
- Support for `else if` chaining in parser
- Distributive conditional types evaluation
- Inference variable (`infer`) support in conditional types
- Type evaluation and reduction logic

## Implementation Plan

### 1. Parser Enhancements

#### 1.1 Add `else if` Support
**File:** `internal/parser/type_ann.go`

**Changes:**
- Modify the conditional type parsing logic in `primaryTypeAnn()` function
- Add support for chaining multiple conditions with `else if`
- Update AST structure to support multiple branches

**Current implementation:**
```go
// In primaryTypeAnn() around line 243
case If: // conditional type
    // ... existing if/else parsing ...
    typeAnn = ast.NewCondTypeAnn(checkType, extendsType, thenType, elseType, span)
```

**New implementation needed:**
```go
case If: // conditional type
    branches := []ConditionalBranch{}
    
    // Parse first condition
    checkType := p.typeAnn()
    p.expect(Colon, AlwaysConsume)
    extendsType := p.typeAnn()
    p.expect(OpenBrace, AlwaysConsume)
    thenType := p.typeAnn()
    p.expect(CloseBrace, AlwaysConsume)
    
    branches = append(branches, ConditionalBranch{
        Check: checkType,
        Extends: extendsType,
        Then: thenType,
    })
    
    // Parse else if branches
    for p.lexer.peek().Type == Else {
        p.lexer.consume() // consume 'else'
        if p.lexer.peek().Type == If {
            p.lexer.consume() // consume 'if'
            // Parse else if branch...
        } else {
            // Parse final else branch
            break
        }
    }
    
    // Convert to nested CondTypeAnn structure
    typeAnn = buildNestedConditional(branches, elseType)
```

#### 1.2 Add Infer Type Annotation Support
**File:** `internal/parser/type_ann.go`

**Changes:**
- Add parsing for `infer` keyword in conditional type extends clauses
- Create `InferTypeAnn` nodes during parsing

**New token needed:**
```go
// In internal/parser/token.go
Infer // "infer"
```

**Parser logic:**
```go
// In primaryTypeAnn()
case Infer:
    p.lexer.consume() // consume 'infer'
    token := p.lexer.peek()
    if token.Type != Identifier {
        p.reportError(token.Span, "expected identifier after 'infer'")
        return nil
    }
    p.lexer.consume() // consume identifier
    typeAnn = ast.NewInferTypeAnn(token.Value, token.Span)
```

### 2. AST Enhancements

#### 2.1 Update Existing AST Nodes
**File:** `internal/ast/type_ann.go`

**Current `InferTypeAnn`:**
```go
type InferTypeAnn struct {
    Name         string
    span         Span
    inferredType Type
}
```

This appears to already exist and be correct.

#### 2.2 Support for Complex Conditional Chains
Consider adding a helper to represent conditional chains more efficiently, though the current nested approach may be sufficient.

### 3. Type System Enhancements

#### 3.1 Add Constructor Functions
**File:** `internal/type_system/types.go`

**Add missing constructor:**
```go
func NewCondType(check, extends, cons, alt Type) *CondType {
    return &CondType{
        Check:   check,
        Extends: extends,
        Cons:    cons,
        Alt:     alt,
    }
}

func NewInferType(name string) *InferType {
    return &InferType{
        Name: name,
    }
}
```

#### 3.2 Add Type System Utilities
**File:** `internal/type_system/utils.go` (new file)

```go
package type_system

// ProcessInferTypes replaces InferType nodes with fresh type variables
// and returns the modified extends type and a mapping from infer names to type variables
func ProcessInferTypes(extendsType Type) (Type, map[string]*TypeVar) {
    visitor := &InferTypeProcessor{
        inferVars: make(map[string]*TypeVar),
    }
    processedType := extendsType.Accept(visitor).(Type)
    return processedType, visitor.inferVars
}

// InferTypeProcessor implements TypeVisitor to replace InferType nodes with fresh TypeVar instances
type InferTypeProcessor struct {
    inferVars map[string]*TypeVar
}

func (v *InferTypeProcessor) EnterType(t Type) {
    // No-op - just for traversal
}

func (v *InferTypeProcessor) ExitType(t Type) Type {
    t = Prune(t)
    
    if inferType, ok := t.(*InferType); ok {
        if existingVar, exists := v.inferVars[inferType.Name]; exists {
            // Reuse existing type variable for same infer name
            return existingVar
        }
        // Create fresh type variable
        freshVar := NewTypeVar()
        v.inferVars[inferType.Name] = freshVar
        return freshVar
    }
    
    // For all other types, return as-is (children have already been processed)
    return t
}

// SubstituteInferVars replaces TypeRefType nodes that correspond to infer variables
// with the actual inferred types from unification
func SubstituteInferVars(t Type, inferMapping map[string]*TypeVar) Type {
    visitor := &InferVarSubstitutor{
        inferMapping: inferMapping,
    }
    return t.Accept(visitor).(Type)
}

// InferVarSubstitutor implements TypeVisitor to substitute infer variable references
type InferVarSubstitutor struct {
    inferMapping map[string]*TypeVar
}

func (v *InferVarSubstitutor) EnterType(t Type) {
    // No-op - just for traversal
}

func (v *InferVarSubstitutor) ExitType(t Type) Type {
    t = Prune(t)
    
    if typeRef, ok := t.(*TypeRefType); ok {
        // Check if this type reference corresponds to an infer variable
        for inferName, typeVar := range v.inferMapping {
            if typeRef.Name == inferName {
                // Return the inferred type (what the type variable was unified with)
                return Prune(typeVar)
            }
        }
    }
    
    // For all other types, return as-is (children have already been processed)
    return t
}
```

### 4. Type Checker Implementation

**Important:** Distribution logic should only be invoked during generic type alias instantiation. The `evaluateConditionalTypeWithDistribution` function should be called from the generic instantiation code path, not from normal conditional type evaluation.

#### 4.1 Add Conditional Type Inference
**File:** `internal/checker/infer.go`

**Add case to `inferTypeAnn` function around line 1405:**

```go
func (c *Checker) inferTypeAnn(ctx Context, typeAnn ast.TypeAnn) (Type, []Error) {
    // ... existing code ...
    
    case *ast.CondTypeAnn:
        checkType, checkErrors := c.inferTypeAnn(ctx, typeAnn.Check)
        errors = slices.Concat(errors, checkErrors)
        
        extendsType, extendsErrors := c.inferTypeAnn(ctx, typeAnn.Extends)
        errors = slices.Concat(errors, extendsErrors)
        
        consType, consErrors := c.inferTypeAnn(ctx, typeAnn.Cons)
        errors = slices.Concat(errors, consErrors)
        
        altType, altErrors := c.inferTypeAnn(ctx, typeAnn.Alt)
        errors = slices.Concat(errors, altErrors)
        
        t = c.evaluateConditionalType(ctx, checkType, extendsType, consType, altType)
        
    case *ast.InferTypeAnn:
        // Infer types should be replaced during conditional type evaluation
        // If we reach here, it's likely an error or unsupported usage
        t = NewInferType(typeAnn.Name)
        
    // ... rest of existing cases ...
}
```

#### 4.2 Add Conditional Type Evaluation Logic
**File:** `internal/checker/conditional.go` (new file)

```go
package checker

import (
    "github.com/escalier-lang/escalier/internal/type_system"
    . "github.com/escalier-lang/escalier/internal/type_system"
)

// Conditional Type Distribution Rules:
//
// Distribution only occurs during generic type alias instantiation when:
// 1. We have a generic type alias with conditional types in its definition
// 2. We're instantiating that alias with union type arguments  
// 3. The conditional's check type is a "naked" type parameter (not wrapped)
// 4. That type parameter gets substituted with a union type
//
// The key insight is that distribution is a property of how generics are instantiated,
// not of conditional types themselves. During normal type checking, conditional types
// evaluate non-distributively. Distribution only happens when the type checker is
// performing generic type substitution and encounters a conditional where the check
// type was originally a naked type parameter that's being replaced with a union.
//
// Implementation approach:
// 1. During generic instantiation, track which type parameters are being substituted with unions
// 2. When evaluating conditionals during substitution, check if the check type is a union
//    that resulted from substituting a naked type parameter
// 3. If so, distribute; otherwise evaluate normally
//
// Examples:
//   type ToArray<T> = if T : any { T[] } else { never }
//   
//   When evaluating ToArray<string | number>:
//   1. T is a naked type parameter in check position 
//   2. T gets substituted with (string | number)
//   3. Distribution occurs: evaluate separately for string and number
//   4. Result: string[] | number[]
//
//   type Wrapped<T> = if T[] : any[] { true } else { false }
//   
//   When evaluating Wrapped<string | number>:
//   1. T[] is NOT a naked type parameter (T is wrapped in array)
//   2. T gets substituted with (string | number), giving (string | number)[]
//   3. No distribution occurs: evaluate normally
//   4. Result: true (since (string | number)[] extends any[])

// Conditional Type Evaluation Strategy:
//
// The approach for handling infer types in conditional types:
// 1. Process the extends clause to replace InferType nodes with fresh TypeVar instances
// 2. Unify the check type against the processed extends type
// 3. If unification succeeds, the TypeVar instances will be bound to the corresponding
//    parts of the check type through the unification process
// 4. Substitute any TypeRefType nodes in the consequent type that reference the same
//    names as the infer variables with the bound TypeVar instances
//
// Example:
//   if T : fn(...args: infer P) -> infer R { [P, R] } else { never }
//   
//   Processing extends clause:
//   - Replace "infer P" with fresh TypeVar $1
//   - Replace "infer R" with fresh TypeVar $2  
//   - Result: fn(...args: $1) -> $2
//   
//   Unifying T = fn(string, number) -> boolean against fn(...args: $1) -> $2:
//   - $1 becomes bound to [string, number] (rest parameters type)
//   - $2 becomes bound to boolean
//   
//   Substituting in consequent [P, R]:
//   - P (TypeRefType) -> $1 -> [string, number]
//   - R (TypeRefType) -> $2 -> boolean
//   - Result: [[string, number], boolean]

// Note: Distribution in conditional types ONLY occurs when:
// 1. The conditional type is part of a generic type alias definition
// 2. A union type is passed as a type argument when instantiating that generic type alias
// 3. The union type parameter appears in the "check" position of the conditional
// 4. The type parameter in the check position is a "naked" type parameter (not wrapped in another type)
//
// Distribution does NOT occur for:
// - Inline conditional types (not part of a generic type alias)
// - Conditional types where the check type is not a type parameter
// - Type parameters wrapped in other types (e.g., T[] where T is a union)
//
// Examples of distribution (only when expanding generic type aliases):
//   type ToArray<T> = if T : any { T[] } else { never }
//   type Result = ToArray<string | number>  // Distributes: string[] | number[]
//
//   type Complex<T, U> = if T : string { U[] } else { never }
//   type Result = Complex<string | number, boolean | symbol>
//   // Only T distributes (naked T in check position): boolean[] | symbol[] | never
//
//   type DoubleCheck<T, U> = if T : any { if U : any { [T, U] } else { never } } else { never }
//   type Result = DoubleCheck<string | number, boolean | symbol>
//   // Both T and U distribute (both naked in their respective check positions)
//   // Result: [string, boolean] | [string, symbol] | [number, boolean] | [number, symbol]
//
// Examples of NO distribution:
//   type Test = if (string | number) : any { true } else { false }  // true (inline conditional)
//   
//   type Wrapped<T> = if T[] : any[] { true } else { false }
//   type Result = Wrapped<string | number>  // true (T is wrapped in array, not naked)
//   
//   type Identity<T> = T
//   type Test2 = if Identity<string | number> : any { true } else { false }  // true (not naked type param)

// evaluateConditionalType evaluates a conditional type (non-distributive by default)
func (c *Checker) evaluateConditionalType(ctx Context, check, extends, cons, alt Type) Type {
    return c.evaluateNonDistributiveConditional(ctx, check, extends, cons, alt)
}

// evaluateConditionalTypeWithDistribution evaluates a conditional type with potential distribution
// This should ONLY be called during generic type alias instantiation when:
// 1. The conditional type is part of a generic type alias definition
// 2. Union type arguments are being substituted for type parameters
// 3. The check type is a naked type parameter that was substituted with a union
func (c *Checker) evaluateConditionalTypeWithDistribution(ctx Context, check, extends, cons, alt Type, distributiveTypeParams map[string]bool) Type {
    check = Prune(check)
    extends = Prune(extends)
    
    // Only distribute if this is a naked type parameter that became a union during substitution
    if union, ok := check.(*UnionType); ok {
        if c.isNakedDistributiveTypeParam(check, distributiveTypeParams) {
            return c.evaluateDistributiveConditional(ctx, union, extends, cons, alt, distributiveTypeParams)
        }
    }
    
    // Non-distributive evaluation, but still need to handle nested conditionals
    return c.evaluateNonDistributiveConditionalWithParams(ctx, check, extends, cons, alt, distributiveTypeParams)
}

// isNakedDistributiveTypeParam checks if the check type is a naked type parameter that should distribute
// A naked type parameter is one that appears directly in the check position without being wrapped
func (c *Checker) isNakedDistributiveTypeParam(checkType Type, distributiveTypeParams map[string]bool) bool {
    // The check type should be exactly a union that was substituted for a distributive type parameter
    // This means the original check type was a naked type parameter (e.g., T in "if T : ...") 
    // and during generic instantiation, T was replaced with a union type
    
    // For now, we can use a simple heuristic: if distributiveTypeParams is provided and non-empty,
    // it means we're in a generic instantiation context where distribution should occur
    return distributiveTypeParams != nil && len(distributiveTypeParams) > 0
}

// evaluateDistributiveConditional handles union types in check position
func (c *Checker) evaluateDistributiveConditional(ctx Context, union *UnionType, extends, cons, alt Type, distributiveTypeParams map[string]bool) Type {
    results := make([]Type, len(union.Types))
    
    for i, elem := range union.Types {
        results[i] = c.evaluateNonDistributiveConditionalWithParams(ctx, elem, extends, cons, alt, distributiveTypeParams)
    }
    
    return NewUnionType(results...)
}

// evaluateNonDistributiveConditional evaluates the conditional without distribution (overload for simple cases)
func (c *Checker) evaluateNonDistributiveConditional(ctx Context, check, extends, cons, alt Type) Type {
    return c.evaluateNonDistributiveConditionalWithParams(ctx, check, extends, cons, alt, nil)
}

// evaluateNonDistributiveConditionalWithParams is the main implementation
func (c *Checker) evaluateNonDistributiveConditionalWithParams(ctx Context, check, extends, cons, alt Type, distributiveTypeParams map[string]bool) Type {
    // Process infer types in the extends clause, replacing them with fresh type variables
    processedExtends, inferVars := ProcessInferTypes(extends)
    
    // Unify the check type with the processed extends type
    unifyErrors := c.unify(ctx, check, processedExtends)
    if len(unifyErrors) == 0 {
        // Types are compatible - substitute infer variables in consequent
        substitutedCons := SubstituteInferVars(cons, inferVars)
        
        // Handle nested conditionals in the consequent that may need distribution
        if distributiveTypeParams != nil {
            return c.handleNestedConditionals(ctx, substitutedCons, distributiveTypeParams)
        }
        return substitutedCons
    } else {
        // Handle nested conditionals in the alternative that may need distribution
        if distributiveTypeParams != nil {
            return c.handleNestedConditionals(ctx, alt, distributiveTypeParams)
        }
        return alt
    }
}

// handleNestedConditionals processes any nested conditional types that may need distribution
func (c *Checker) handleNestedConditionals(ctx Context, t Type, distributiveTypeParams map[string]bool) Type {
    visitor := &NestedConditionalHandler{
        checker:                c,
        ctx:                   ctx,
        distributiveTypeParams: distributiveTypeParams,
    }
    return t.Accept(visitor).(Type)
}

// NestedConditionalHandler implements TypeVisitor to handle nested conditional types during distribution
type NestedConditionalHandler struct {
    checker                *Checker
    ctx                   Context
    distributiveTypeParams map[string]bool
}

func (v *NestedConditionalHandler) EnterType(t Type) {
    // No-op - just for traversal
}

func (v *NestedConditionalHandler) ExitType(t Type) Type {
    t = Prune(t)
    
    if condType, ok := t.(*CondType); ok {
        // Recursively evaluate nested conditional with distribution context
        return v.checker.evaluateConditionalTypeWithDistribution(
            v.ctx, 
            condType.Check, 
            condType.Extends, 
            condType.Cons, 
            condType.Alt, 
            v.distributiveTypeParams,
        )
    }
    
    // For all other types, return as-is (children have already been processed by the visitor)
    return t
}
```

### 5. Testing

#### 5.1 Parser Tests
**File:** `internal/parser/type_ann_test.go`

**Add test cases:**
```go
"ConditionalTypeElseIf": {
    input: "if A : B { C } else if D : E { F } else { G }",
},
"ConditionalTypeWithInfer": {
    input: "if T : fn(...args: infer P) -> any { P } else { never }",
},
"NestedConditionalTypes": {
    input: "if T : U { if V : W { X } else { Y } } else { Z }",
},
```

#### 5.2 Type Checker Tests
**File:** `internal/checker/conditional_test.go` (new file)

```go
package checker

import (
    "testing"
    "github.com/stretchr/testify/assert"
)

func TestConditionalTypes(t *testing.T) {
    tests := map[string]struct {
        input    string
        expected string
    }{
        "BasicConditional": {
            input: `type Test<T> = if T : string { true } else { false }
                   type Result1 = Test<string>  // should be true
                   type Result2 = Test<number>  // should be false`,
            expected: "true, false",
        },
        "DistributiveConditional": {
            input: `type ToArray<T> = if T : any { T[] } else { never }
                   type Result = ToArray<string | number>  // should be string[] | number[]`,
            expected: "string[] | number[]",
        },
        "NonDistributiveInlineConditional": {
            input: `type Test = if (string | number) : any { true } else { false }
                   type Result = Test  // should be true (no distribution - not a generic type alias)`,
            expected: "true",
        },
        "NonDistributiveWrappedTypeParam": {
            input: `type Wrapped<T> = if T[] : any[] { true } else { false }
                   type Result = Wrapped<string | number>  // should be true (no distribution - T is wrapped)`,
            expected: "true",
        },
        "MultipleUnionDistribution": {
            input: `type Complex<T, U> = if T : string { U[] } else { never }
                   type Result = Complex<string | number, boolean | symbol>  // should be boolean[] | symbol[] | never`,
            expected: "boolean[] | symbol[] | never",
        },
        "NestedDistributiveConditional": {
            input: `type DoubleCheck<T, U> = if T : any { if U : any { [T, U] } else { never } } else { never }
                   type Result = DoubleCheck<string | number, boolean | symbol>  // should distribute both T and U`,
            expected: "[string, boolean] | [string, symbol] | [number, boolean] | [number, symbol]",
        },
        "NonDistributiveInlineConditional": {
            input: `type Test = if (string | number) : any { true } else { false }
                   type Result = Test  // should be true (no distribution - inline conditional)`,
            expected: "true",
        },
        "ConditionalWithInfer": {
            input: `type GetReturnType<T> = if T : fn(...args: never[]) -> infer R { R } else { never }
                   type Result = GetReturnType<fn() -> string>  // should be string`,
            expected: "string",
        },
        "ConditionalElseIf": {
            input: `type TypeName<T> = if T : string { "string" } 
                                  else if T : number { "number" } 
                                  else if T : boolean { "boolean" } 
                                  else { "other" }
                   type Result = TypeName<number>  // should be "number"`,
            expected: "\"number\"",
        },
    }
    
    for name, test := range tests {
        t.Run(name, func(t *testing.T) {
            // Test implementation
        })
    }
}
```

#### 5.3 Integration Tests
**File:** `fixtures/conditional_types/` (new directory)

Create fixture files to test real-world scenarios:
- `basic_conditional.esc`
- `distributive_conditional.esc`
- `infer_conditional.esc`
- `else_if_conditional.esc`

### 6. Documentation Updates

#### 6.1 Language Documentation
**File:** `docs/11_conditional_types.md` (new file)

Document the conditional types feature with examples and edge cases.

#### 6.2 Parser Grammar Updates
Update any formal grammar documentation to include conditional type syntax.

### 7. Additional Considerations

#### 7.1 Error Handling
- Add specific error types for conditional type errors
- Improve error messages for malformed conditional types
- Handle circular conditional types

#### 7.2 Performance Considerations
- Implement memoization for conditional type evaluation
- Detect and prevent infinite recursion in complex conditional types
- Optimize common conditional type patterns

#### 7.3 Compatibility
- Ensure conditional types work with existing type system features
- Test interaction with generics, unions, intersections
- Verify code generation to TypeScript preserves semantics

## Implementation Order

1. **Phase 1**: Parser enhancements (else if, infer parsing)
2. **Phase 2**: Type system utilities and constructor functions  
3. **Phase 3**: Basic conditional type inference in checker
4. **Phase 4**: Distributive conditional types
5. **Phase 5**: Infer variable support
6. **Phase 6**: Testing and edge case handling
7. **Phase 7**: Documentation and examples

## Files to Modify

### Core Implementation
- `internal/parser/type_ann.go` - Add else if and infer parsing
- `internal/parser/token.go` - Add infer token
- `internal/parser/lexer.go` - Add infer keyword recognition
- `internal/checker/infer.go` - Add conditional type inference
- `internal/type_system/types.go` - Add constructor functions
- `internal/type_system/utils.go` - Add utility functions (new file)
- `internal/checker/conditional.go` - Conditional type evaluation (new file)

### Testing
- `internal/parser/type_ann_test.go` - Parser tests
- `internal/checker/conditional_test.go` - Type checker tests (new file)
- `fixtures/conditional_types/` - Integration tests (new directory)

### Documentation  
- `docs/11_conditional_types.md` - Feature documentation (new file)
- `planning/conditional_types/implementation_spec.md` - This file

This implementation will provide full conditional type support matching TypeScript's behavior while integrating cleanly with Escalier's existing type system architecture.
