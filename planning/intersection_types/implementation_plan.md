# Intersection Types - Implementation Plan

## Overview

Intersection types are already parsed by the Escalier parser and have a type representation in `internal/type_system/types.go` (`IntersectionType`). However, the type checker doesn't properly handle them in key areas:

1. **`Unify` function** in `internal/checker/unify.go` - Has incomplete/TODO cases for intersection types
2. **`getMemberType` function** in `internal/checker/expand_type.go` - Doesn't handle intersection types at all

This plan outlines the work needed to fully support intersection types with TypeScript-compatible semantics.

## Current State

### What's Working
- ✅ Parser creates `ast.IntersectionTypeAnn` nodes
- ✅ `inferTypeAnn` converts to `type_system.IntersectionType`
- ✅ `IntersectionType` struct exists with `Accept`, `Equals`, `String` methods
- ✅ `NewIntersectionType` constructor with basic normalization (returns single type if len=1, never if len=0)
- ✅ Basic visitor support for traversing intersection types

### What's Broken/Missing
- ❌ `Unify` has a TODO panic for `IntersectionType & ObjectType`
- ❌ `Unify` has partial implementation for `IntersectionType & IntersectionType` but may be incomplete
- ❌ `getMemberType` doesn't handle `IntersectionType` at all
- ❌ No normalization for `never`, `unknown`, `any` in intersections
- ❌ No flattening of nested intersections
- ❌ No merging of object types in intersections
- ❌ No handling of primitive & object intersections (branded types)
- ❌ No handling of function intersections (overloads)

## Implementation Tasks

### Phase 1: Normalization (High Priority)

**Location**: `internal/type_system/types.go` - `NewIntersectionType` function

**Current Code** (lines 1666-1674):
```go
func NewIntersectionType(provenance Provenance, types ...Type) Type {
	if len(types) == 0 {
		return NewNeverType(nil)
	}
	if len(types) == 1 {
		return types[0]
	}
	return &IntersectionType{
		Types:      types,
		provenance: provenance,
	}
}
```

**Tasks**:
1. Flatten nested intersections: `(A & B) & C` → `A & B & C`
2. Remove duplicates: `A & A` → `A`
3. Handle `never`: `A & never` → `never`
4. Handle `unknown`: `A & unknown` → `A`
5. Handle `any`: `A & any` → `any`
6. Detect incompatible primitives: `string & number` → `never`
7. Handle mutable object types: `(mut T) & T` → `T`

**Implementation**:
```go
func NewIntersectionType(provenance Provenance, types ...Type) Type {
	if len(types) == 0 {
		return NewNeverType(nil)
	}
	if len(types) == 1 {
		return types[0]
	}
	
	// Flatten nested intersections
	flattened := []Type{}
	for _, t := range types {
		t = Prune(t)
		if inter, ok := t.(*IntersectionType); ok {
			flattened = append(flattened, inter.Types...)
		} else {
			flattened = append(flattened, t)
		}
	}
	
	// Normalize
	normalized := []Type{}
	seen := make(map[string]bool)
	hasAny := false
	hasNever := false
	primitiveTypes := make(map[Prim]*PrimType)
	
	for _, t := range flattened {
		t = Prune(t)
		
		// Check for any
		if _, ok := t.(*AnyType); ok {
			hasAny = true
			break
		}
		
		// Check for never
		if _, ok := t.(*NeverType); ok {
			hasNever = true
			continue // Don't add never to the list
		}
		
		// Remove unknown
		if _, ok := t.(*UnknownType); ok {
			continue
		}
		
		// Handle MutabilityType
		if mut, ok := t.(*MutabilityType); ok {
			if mut.Mutability == MutabilityMutable {
				// Check if immutable version exists
				innerStr := mut.Type.String()
				if seen[innerStr] {
					// (mut T) & T → T, keep the immutable one
					continue
				}
			}
		}
		
		// Track primitive types to detect conflicts
		if prim, ok := t.(*PrimType); ok {
			if existing, exists := primitiveTypes[prim.Prim]; exists {
				// Same primitive, already added
				continue
			}
			// Different primitive type
			if len(primitiveTypes) > 0 {
				// Conflicting primitives: string & number → never
				return NewNeverType(provenance)
			}
			primitiveTypes[prim.Prim] = prim
		}
		
		// Remove duplicates
		typeStr := t.String()
		if seen[typeStr] {
			continue
		}
		seen[typeStr] = true
		normalized = append(normalized, t)
	}
	
	if hasAny {
		return NewAnyType(provenance)
	}
	
	if hasNever {
		return NewNeverType(provenance)
	}
	
	if len(normalized) == 0 {
		return NewNeverType(provenance)
	}
	
	if len(normalized) == 1 {
		return normalized[0]
	}
	
	return &IntersectionType{
		Types:      normalized,
		provenance: provenance,
	}
}
```

### Phase 2: Unify Support (High Priority)

**Location**: `internal/checker/unify.go`

#### Task 2.1: IntersectionType & Any Type
Add cases before the existing intersection handling (after line 900):

```go
// | IntersectionType, _ -> check if intersection is subtype of t2
if intersection, ok := t1.(*type_system.IntersectionType); ok {
	// For A & B to be a subtype of C, all parts of the intersection
	// must be subtypes of C
	errors := []Error{}
	for _, part := range intersection.Types {
		unifyErrors := c.Unify(ctx, part, t2)
		errors = slices.Concat(errors, unifyErrors)
	}
	return errors
}

// | _, IntersectionType -> check if t1 is subtype of intersection
if intersection, ok := t2.(*type_system.IntersectionType); ok {
	// For A to be a subtype of B & C, A must be a subtype of both B and C
	errors := []Error{}
	for _, part := range intersection.Types {
		unifyErrors := c.Unify(ctx, t1, part)
		errors = slices.Concat(errors, unifyErrors)
	}
	return errors
}
```

#### Task 2.2: Remove TODO panics
Replace the TODO panic at line 903:
```go
// | IntersectionType, ObjectType -> ...
if intersection, ok := t1.(*type_system.IntersectionType); ok {
	if obj, ok := t2.(*type_system.ObjectType); ok {
		// This is now handled by the general IntersectionType case above
		// The intersection must satisfy the object type
		errors := []Error{}
		for _, part := range intersection.Types {
			unifyErrors := c.Unify(ctx, part, obj)
			errors = slices.Concat(errors, unifyErrors)
		}
		return errors
	}
}
```

#### Task 2.3: Review IntersectionType & IntersectionType case
The existing code at line 914 looks reasonable but should be reviewed:
- Verify that the logic correctly implements subtyping rules
- Add comments explaining the algorithm
- Consider whether we need both directions (already handled by general cases above)

### Phase 3: Member Access Support (High Priority)

**Location**: `internal/checker/expand_type.go` - `getMemberType` function

Add a new case in the switch statement (around line 570, after UnionType case):

```go
case *type_system.IntersectionType:
	return c.getIntersectionAccess(ctx, t, key, errors)
```

Then implement the helper function:

```go
// getIntersectionAccess handles property and index access on IntersectionType
func (c *Checker) getIntersectionAccess(ctx Context, intersectionType *type_system.IntersectionType, key MemberAccessKey, errors []Error) (type_system.Type, []Error) {
	// For an intersection A & B, a member access should:
	// 1. Try to get the member from each constituent type
	// 2. Merge the results appropriately
	
	// Separate object types from non-object types
	objectTypes := []*type_system.ObjectType{}
	primitiveWithObjectTypes := []type_system.Type{} // For branded primitives
	
	for _, part := range intersectionType.Types {
		part = type_system.Prune(part)
		if objType, ok := part.(*type_system.ObjectType); ok {
			objectTypes = append(objectTypes, objType)
		} else {
			// Could be a primitive with object properties (branded type)
			primitiveWithObjectTypes = append(primitiveWithObjectTypes, part)
		}
	}
	
	// If all parts are object types, merge their properties
	if len(objectTypes) == len(intersectionType.Types) {
		// Create a merged object type for property access
		// The property must exist in at least one of the object types
		// and the result is the intersection of matching property types
		
		memberTypes := []type_system.Type{}
		foundAny := false
		
		for _, objType := range objectTypes {
			memberType, memberErrors := c.getObjectAccess(objType, key, nil)
			// Only report errors if no object type has this property
			if len(memberErrors) == 0 {
				memberTypes = append(memberTypes, memberType)
				foundAny = true
			}
		}
		
		if !foundAny {
			// Property doesn't exist in any part of the intersection
			if propKey, ok := key.(PropertyKey); ok {
				errors = append(errors, &UnknownPropertyError{
					ObjectType: intersectionType,
					Property:   propKey.Name,
					span:       propKey.Span(),
				})
			} else {
				errors = append(errors, &InvalidObjectKeyError{
					Key:  key.(IndexKey).Type,
					span: key.Span(),
				})
			}
			return type_system.NewNeverType(nil), errors
		}
		
		// The result type is the intersection of all matching property types
		if len(memberTypes) == 1 {
			return memberTypes[0], errors
		}
		return type_system.NewIntersectionType(nil, memberTypes...), errors
	}
	
	// For mixed cases (e.g., branded primitives: string & {__brand: "email"})
	// Try to access the member from each part and return the first successful one
	for _, part := range intersectionType.Types {
		memberType, memberErrors := c.getMemberType(ctx, part, key)
		if len(memberErrors) == 0 {
			return memberType, errors
		}
	}
	
	// If no part has this property, report error
	errors = append(errors, &ExpectedObjectError{Type: intersectionType, span: key.Span()})
	return type_system.NewNeverType(nil), errors
}
```

### Phase 4: Expand Type Support (Medium Priority)

**Location**: `internal/checker/expand_type.go` - `TypeExpansionVisitor`

Check if `ExitType` needs special handling for intersection types. Currently it handles `UnionType` to filter out `never` types. We should add similar logic for intersections:

Add in `ExitType` method (around line 218):

```go
case *type_system.IntersectionType:
	// Check for any/never/unknown normalization
	// This should mostly be handled by NewIntersectionType, 
	// but we may need to re-normalize after type expansion
	return type_system.NewIntersectionType(nil, t.Types...)
```

### Phase 5: Handle Special Cases (Medium Priority)

#### Task 5.1: Function Intersections (Overloads)
When intersecting function types, create an intersection type that can be called with any of the function signatures.

This may require changes to:
- `unifyFuncTypes` in `internal/checker/unify.go`
- Function call inference in `internal/checker/infer_expr.go`

#### Task 5.2: Primitive & Object Intersections (Branded Types)
Already partially handled by normalization, but verify:
- `string & {__brand: "email"}` creates an intersection
- The intersection is a subtype of `string`
- Property access works on the object part
- The intersection cannot be assigned from plain `string`

### Phase 6: Code Generation (Low Priority)

**Location**: `internal/codegen/codegen.go`

Verify that intersection types are properly emitted to TypeScript. The code generator likely already handles this since it has `IntersectionTypeAnn` support, but verify:

1. Intersection types are emitted with `&` operator
2. Branded types emit correctly
3. Comments/metadata are preserved

### Phase 7: Testing (Critical)

**Unit Tests**: Add test cases to `internal/checker/tests/intersection_test.go` as each phase is implemented. This file should contain focused unit tests for specific intersection type behaviors.

**Integration Tests**: Create test fixtures in `fixtures/intersection_types/` for end-to-end testing:

1. **Basic intersections**
   - `type A & B` with object types
   - Multiple intersections `A & B & C`
   - Nested intersections `(A & B) & C`

2. **Normalization**
   - `A & A` → `A`
   - `A & never` → `never`
   - `A & unknown` → `A`
   - `A & any` → `any`
   - `string & number` → `never`
   - `(mut T) & T` → `T`

3. **Member access**
   - Property access on intersection of objects
   - Method calls on intersections
   - Index access on intersections

4. **Branded types**
   - `string & {__brand: "email"}`
   - `number & {__brand: "currency"}`
   - Assignment compatibility

5. **Function intersections**
   - Overloaded functions
   - Calling with different signatures

6. **Subtyping**
   - `A & B <: A` and `A & B <: B`
   - `ABC <: A & B` when ABC has all properties
   - Intersection of intersections

7. **Union distribution**
   - `(A | B) & C` behaves correctly
   - `A & (B | C)` behaves correctly

## Implementation Order

1. **Phase 1** (Normalization) - Foundation for everything else
2. **Phase 7.1-7.2** (Basic tests + normalization tests) - Verify Phase 1
   - Add unit tests to `internal/checker/tests/intersection_test.go` for normalization
   - Add integration test fixtures for basic intersection syntax
3. **Phase 2** (Unify support) - Core type checking
4. **Phase 7.6** (Subtyping tests) - Verify Phase 2
   - Add unit tests to `intersection_test.go` for subtyping rules
   - Add integration test fixtures for subtyping scenarios
5. **Phase 3** (Member access) - Enable practical usage
6. **Phase 7.3** (Member access tests) - Verify Phase 3
   - Add unit tests to `intersection_test.go` for property/method access
   - Add integration test fixtures for member access
7. **Phase 4** (Expand type support) - Handle edge cases
8. **Phase 5** (Special cases) - Branded types and function overloads
9. **Phase 7.4-7.5** (Branded types + function tests) - Verify Phase 5
10. **Phase 7.7** (Union distribution tests) - Final verification
11. **Phase 6** (Code generation) - Ensure output is correct

## Success Criteria

- [ ] All test fixtures pass
- [ ] No TODO panics remain for intersection types
- [ ] Member access works on intersection types
- [ ] Intersection types are properly normalized
- [ ] Subtyping rules are correctly implemented
- [ ] Branded types work as expected
- [ ] Generated TypeScript code is valid
- [ ] No regressions in existing tests

## Notes

- The parser already handles intersection types, so no parser changes needed
- The `IntersectionType` struct already exists and has visitor support
- Focus on type checking logic in `Unify` and `getMemberType`
- TypeScript compatibility is the goal - match TypeScript semantics exactly
- Consider looking at `UnionType` implementation as a reference for similar patterns
- **Testing Strategy**: Write unit tests in `internal/checker/tests/intersection_test.go` alongside each implementation phase. These tests should be fast, focused, and test specific behaviors. Integration tests in `fixtures/` verify end-to-end functionality
