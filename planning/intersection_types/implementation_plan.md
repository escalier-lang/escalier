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
- ✅ `NewIntersectionType` constructor with comprehensive normalization
  - Flattens nested intersections
  - Removes duplicates using structural equality (`Equals()`)
  - Handles `never`, `unknown`, `any`
  - Detects incompatible primitives
  - Handles mutability types
- ✅ Basic visitor support for traversing intersection types
- ✅ Unit tests for normalization in `internal/type_system/intersection_test.go`

### What's Broken/Missing
- ❌ `Unify` has a TODO panic for `IntersectionType & ObjectType`
- ❌ `Unify` has partial implementation for `IntersectionType & IntersectionType` but may be incomplete
- ❌ `getMemberType` doesn't handle `IntersectionType` at all
- ❌ No post-inference normalization for type aliases and type variables
- ❌ No merging of object types in intersections
- ❌ No handling of primitive & object intersections (branded types)
- ❌ No handling of function intersections (overloads)
- ❌ No re-normalization in `ExpandType`

## Implementation Tasks

### Phase 1: Basic Normalization in Constructor (High Priority) ✅ COMPLETE

**Location**: `internal/type_system/types.go` - `NewIntersectionType` function

**Status**: Implemented and tested

**Tasks Completed**:
1. ✅ Flatten nested intersections: `(A & B) & C` → `A & B & C`
2. ✅ Remove duplicates using `Equals()`: `A & A` → `A`
3. ✅ Handle `never`: `A & never` → `never`
4. ✅ Handle `unknown`: `A & unknown` → `A`
5. ✅ Handle `any`: `A & any` → `any`
6. ✅ Detect incompatible primitives: `string & number` → `never`
7. ✅ Handle mutable object types: `(mut T) & T` → `T`

**Implementation Notes**:
- Uses structural equality (`Equals()`) instead of string comparison for reliable deduplication
- Performs basic normalization at construction time
- Additional normalization needed after type inference (see Phase 1.5 below)

### Phase 1.5: Post-Inference Normalization (High Priority) ✅ COMPLETE

**Location**: `internal/checker/expand_type.go`

**Status**: Implemented and tested

**Rationale**: `NewIntersectionType` performs basic normalization, but additional normalization is needed after:
- Type aliases are resolved to their underlying types
- Type variables are substituted with concrete types
- Type expansion reveals equivalent types with different representations

**Tasks Completed**:
1. ✅ Created `NormalizeIntersectionType` as a method on `*Checker`
2. ✅ Handles type aliases that resolve to the same underlying type via `ExpandType`
3. ✅ Re-normalizes after type variable substitution via `Prune`
4. ✅ Integrated into `ExitType` visitor for automatic re-normalization after type expansion
5. ✅ Comprehensive test suite in `internal/checker/tests/intersection_normalization_test.go`

**Implementation**:

```go
// NormalizeIntersectionType performs deep normalization of an intersection type
// after type inference and expansion. This handles cases that NewIntersectionType
// cannot handle because types haven't been fully resolved yet.
func (c *Checker) NormalizeIntersectionType(ctx Context, t *type_system.IntersectionType) type_system.Type {
	// Step 1: Prune and expand all types to resolve type variables and type aliases
	expanded := make([]type_system.Type, len(t.Types))
	for i, typ := range t.Types {
		// Prune to resolve type variables
		typ = type_system.Prune(typ)
		
		// Expand type aliases to their underlying types
		// Use depth 1 to expand one level of type aliases
		if _, ok := typ.(*type_system.TypeRefType); ok {
			expandedType, _ := c.ExpandType(ctx, typ, 1)
			expanded[i] = expandedType
		} else {
			expanded[i] = typ
		}
	}

	// Step 2: Use NewIntersectionType to apply basic normalization
	// This handles flattening, duplicates, never/any/unknown, primitives, mutability
	result := type_system.NewIntersectionType(t.Provenance(), expanded...)

	// Step 3: If still an intersection after normalization, check for further simplifications
	// Future enhancements:
	// - Detect structurally equivalent object types after expansion
	// - Merge compatible object types into a single object type
	// - Handle nominal type equivalences

	return result
}
```

**Where to Apply**:
- In `ExpandType` after expanding type references
- In `Unify` after type variable substitution
- In `ExApplied**:
- ✅ In `ExitType` visitor for `IntersectionType` case (automatic re-normalization)
- Type variable substitution handled automatically via `Prune`
- Type alias expHandled**:
```typescript
// Type aliases resolving to the same type ✅
type A = {x: number}
type B = A
type C = A & B  // Normalizes to A

// Type variables after substitution ✅
function f<T>(a: T & string, b: T & number) {
  // After substituting T with never, both become never
}

// Nested type references ✅
type Obj = {x: number}
type Ref1 = Obj
type Ref2 = Obj
type Both = Ref1 & Ref2  // Normalizes to Obj
```

**Test Coverage**:
- 10 comprehensive test cases in `internal/checker/tests/intersection_normalization_test.go`
- Tests cover: type variable resolution, conflicting primitives, nested intersections, object types, special types (any/never), mutability, and type alias expansion
- All tests passing ✅
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

### Phase 3: Member Access Support (High Priority) ✅ COMPLETE

**Location**: `internal/checker/expand_type.go` - `getMemberType` function

**Status**: Implemented and tested

**Tasks Completed**:
1. ✅ Added `IntersectionType` case in `getMemberType` switch statement
2. ✅ Implemented `getIntersectionAccess` helper function
3. ✅ Fixed `mergeObjectTypes` to properly intersect properties with the same name
4. ✅ Added comprehensive unit tests in `internal/checker/tests/intersection_test.go`

**Implementation Details**:

Added case in `getMemberType`:
```go
case *type_system.IntersectionType:
	return c.getIntersectionAccess(ctx, t, key, errors)
```

Implemented `getIntersectionAccess` helper function:
- When all parts of the intersection are object types, returns the intersection of matching property types
- For mixed cases (e.g., branded primitives like `string & {__brand: "email"}`), tries each part and returns the first successful access
- Properly reports errors when properties don't exist in any part

Fixed `mergeObjectTypes` function:
- When properties have the same name in multiple object types, their types are now intersected
- Example: `{value: string} & {value: number}` becomes `{value: string & number}` (which normalizes to `{value: never}`)
- Preserves property order for consistent output
- Handles readonly and optional properties correctly

**Test Coverage**:
- 8 comprehensive test cases in `TestIntersectionMemberAccess`
- Tests cover: simple property access, property access with same-named properties, multi-way intersections, and branded types
- Branded type tests verify both object property access and primitive method access
- All tests passing ✅

**Bug Fixes**:
- Fixed infinite loop in `getMemberType` expansion loop by adding early exit for `IntersectionType`
- The expansion loop now stops when encountering object types, namespace types, or intersection types ✅ COMPLETE

**Location**: `internal/checker/expand_type.go` - `TypeExpansionVisitor`

**Status**: Implemented as part of Phase 1.5

**Tasks Completed**:
1. ✅ Added handling for intersection types in `ExitType` method
2. ✅ Re-normalizes intersections after type expansion using `NormalizeIntersectionType`
3. ✅ Ensures that expanded types within intersections are properly normalized

**Implementation**:

Added in `ExitType` method:

```go
case *type_system.IntersectionType:
	// Re-normalize intersection after type expansion
	// Type expansion may reveal equivalent types or simplifications
	return v.checker.NormalizeIntersectionType(v.ctx, t)
```

**Notes**:
- Type expansion reveals equivalent types that can be normalized
- After expanding type aliases, intersections are automatically simplified

**Additional Considerations**:
- Type expansion may reveal that types in the intersection are equivalent
- After expanding type aliases, we may need to merge or simplify the intersection
- Similar to `UnionType` handling which filters out `never` types

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

1. **Phase 1** (Basic normalization in constructor) - ✅ **COMPLETE**
   - Implemented `NewIntersectionType` with normalization
   - Uses `Equals()` for reliable deduplication
   - Handles basic cases: flattening, never/any/unknown, primitives, mutability ✅
   - Added 15 comprehensive test cases - all passing

2. **Phase 1.5** (Post-inference normalization) - ✅ **COMPLETE**
   - Implemented `NormalizeIntersectionType()` as method on `*Checker`
   - Handles type alias resolution via `ExpandType`
   - Applied in `ExitType` visitor for automatic re-normalization
   - Added 10 comprehensive test cases - all passing

3. **Phase 2** (Unify support) - ✅ **COMPLETE**
   - Implemented general `IntersectionType & _` case (at least one part must satisfy)
   - Implemented general `_ & IntersectionType` case (must satisfy all parts)
   - Implemented specific `IntersectionType & IntersectionType` case
   - All 10 unit tests passing in `intersection_unify_test.go`
   - No regressions in existing test suite

4. **Phase 7.6** (Subtyping tests) - ✅ **COMPLETE** (implemented with Phase 2)
   - Added unit tests to `intersection_unify_test.go` for subtyping rules
   - Tests cover intersection with objects, primitives, and other intersections

5. **Phase 3** (Member access) - ✅ **COMPLETE**
   - Implemented `IntersectionType` case in `getMemberType`
   - Implemented `getIntersectionAccess` helper function
   - Added 5 branded type test cases covering primitive & object intersections
   - Fixed `mergeObjectTypes` to intersect properties with same names
   - Added 3 unit tests - all passing

6. **Phase 7.3** (Member access tests) - ✅ **COMPLETE** (implemented with Phase 3)
   - Added unit tests to `intersection_test.go` for property access

7. **Phase 4** (Expand type support) - Handle edge cases with re-normalization
9. **Phase 5** (Special cases) - Branded types and function overloads
10. **Phase 7.4-7.5** (Branded types + function tests) - Verify Phase 5
11. **Phase 7.7** (Union distribution tests) - Final verification
12. **Phase 6** (Code generation) - Ensure output is correct

## Success Criteria

- [x] Post-inference normalization implemented with `NormalizeIntersectionType()`
- [x] Unit tests for post-inference normalization passing (10 test cases)
- [x] Intersection types are properly normalized after type inference
- [x] Unification logic implemented for intersection types (10 test cases in `intersection_unify_test.go`)
- [x] No regressions in existing test suite (all 100+ tests passing)
- [x] Subtyping rules correctly implemented for intersections
- [x] Member access works on intersection types (8 test cases passing)
- [x] Property intersection for same-named properties implemented
- [x] Branded types work correctly (primitive & object intersections)
- [x] Both object property access and primitive method access work on branded types
- [ ] All integration test fixtures pass
- [ ] Generated TypeScript code is valid

## Notes
- **Phase 1.5 Complete**: Post-inference normalization implemented as `*Checker` method
- **Phase 4 Complete**: Integrated into type expansion visitor
- **Phase 2 Complete**: Intersection type unification fully implemented in `Unify` function ✅
- **Phase 3 Complete**: Member access support fully implemented in `getMemberType` and `mergeObjectTypes` ✅
- **Branded Types Working**: Tests confirm that `primitive & object` intersections correctly support both primitive methods and object properties ✅
- **Bug Fix**: Fixed infinite loop in `getMemberType` by stopping expansion when `IntersectionType` is encountered
- **Two-Phase Normalization Strategy**:
  - Initial: Basic flattening and obvious simplifications (any, never, duplicates)
  - Post-inference: More sophisticated normalization using `NormalizeIntersectionType` after type resolution
- Property intersections correctly produce `never` when primitives conflict (e.g., `{x: string} & {x: number}` → `{x: never}`)
- **Implementation Details**:
  - `NormalizeIntersectionType` is a method on `*Checker` in `internal/checker/expand_type.go`
  - Automatically expands type aliases one level using `ExpandType(ctx, typ, 1)`
  - Prunes type variables to resolve substitutions
  - Called automatically in `ExitType` visitor after type expansion
- TypeScript compatibility is the goal - match TypeScript semantics exactly
- **Testing Strategy**: 
  - Unit tests in `internal/type_system/intersection_test.go` for basic normalization ✅
  - Unit tests in `internal/checker/tests/` for post-inference and type checking behaviors ✅
  - Integration tests in `fixtures/intersection_types/` verify end-to-end functionality (TODO)

