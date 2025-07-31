package type_system

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// IdentityVisitor is a visitor that returns nil from ExitType,
// meaning it doesn't transform any types but allows traversal
type IdentityVisitor struct{}

func (v *IdentityVisitor) EnterType(t Type) {
	// No-op for entry
}

func (v *IdentityVisitor) ExitType(t Type) Type {
	return nil // Continue traversal without transformation
}

// TypeReplacementVisitor replaces specific types with other types
type TypeReplacementVisitor struct {
	replacements map[Type]Type
}

func NewTypeReplacementVisitor(replacements map[Type]Type) *TypeReplacementVisitor {
	return &TypeReplacementVisitor{replacements: replacements}
}

func (v *TypeReplacementVisitor) EnterType(t Type) {
	// No-op for entry
}

func (v *TypeReplacementVisitor) ExitType(t Type) Type {
	if replacement, found := v.replacements[t]; found {
		return replacement
	}
	return nil // Continue traversal
}

// TrackingVisitor tracks which types are entered and exited during traversal
type TrackingVisitor struct {
	enteredTypes []Type
	exitedTypes  []Type
}

func NewTrackingVisitor() *TrackingVisitor {
	return &TrackingVisitor{
		enteredTypes: make([]Type, 0),
		exitedTypes:  make([]Type, 0),
	}
}

func (v *TrackingVisitor) EnterType(t Type) {
	v.enteredTypes = append(v.enteredTypes, t)
}

func (v *TrackingVisitor) ExitType(t Type) Type {
	v.exitedTypes = append(v.exitedTypes, t)
	return nil // Continue traversal without transformation
}

func (v *TrackingVisitor) GetEnteredTypes() []Type {
	return v.enteredTypes
}

func (v *TrackingVisitor) GetExitedTypes() []Type {
	return v.exitedTypes
}

func (v *TrackingVisitor) Reset() {
	v.enteredTypes = make([]Type, 0)
	v.exitedTypes = make([]Type, 0)
}

// TestVisitorNoMutation tests that visitors don't mutate the original types
func TestVisitorNoMutation(t *testing.T) {
	t.Run("PrimType immutability", func(t *testing.T) {
		original := NewNumType()
		originalProvenance := original.Provenance()

		visitor := &IdentityVisitor{}
		result := original.Accept(visitor)

		// Result should be the same instance since no changes were made
		assert.Same(t, original, result)
		// Original should be unchanged
		assert.Equal(t, originalProvenance, original.Provenance())
	})

	t.Run("LitType immutability", func(t *testing.T) {
		original := &LitType{
			Lit:        &NumLit{Value: 42},
			provenance: nil,
		}
		originalLit := original.Lit

		visitor := &IdentityVisitor{}
		result := original.Accept(visitor)

		// Result should be the same instance since no changes were made
		assert.Same(t, original, result)
		// Original should be unchanged
		assert.Same(t, originalLit, original.Lit)
	})

	t.Run("FuncType immutability", func(t *testing.T) {
		param1 := NewFuncParam(NewIdentPat("x"), NewNumType())
		param2 := NewFuncParam(NewIdentPat("y"), NewStrType())
		original := &FuncType{
			TypeParams: nil,
			Self:       nil,
			Params:     []*FuncParam{param1, param2},
			Return:     NewBoolType(),
			Throws:     NewNeverType(),
			provenance: nil,
		}
		originalParams := original.Params
		originalReturn := original.Return

		visitor := &IdentityVisitor{}
		result := original.Accept(visitor)

		// Result should be the same instance since no changes were made
		assert.Same(t, original, result)
		// Original should be unchanged
		assert.Equal(t, originalParams, original.Params)
		assert.Same(t, originalReturn, original.Return)
		assert.Same(t, param1, original.Params[0])
		assert.Same(t, param2, original.Params[1])
	})

	t.Run("UnionType immutability", func(t *testing.T) {
		numType := NewNumType()
		strType := NewStrType()
		original := &UnionType{
			Types:      []Type{numType, strType},
			provenance: nil,
		}
		originalTypes := original.Types

		visitor := &IdentityVisitor{}
		result := original.Accept(visitor)

		// Result should be the same instance since no changes were made
		assert.Same(t, original, result)
		// Original should be unchanged
		assert.Equal(t, originalTypes, original.Types)
		assert.Same(t, numType, original.Types[0])
		assert.Same(t, strType, original.Types[1])
	})

	t.Run("ObjectType immutability", func(t *testing.T) {
		prop := NewPropertyElemType(NewStrKey("x"), NewNumType())
		original := &ObjectType{
			Elems:      []ObjTypeElem{prop},
			Exact:      false,
			Immutable:  false,
			Mutable:    false,
			Nominal:    false,
			Interface:  false,
			Extends:    nil,
			Implements: nil,
			provenance: nil,
		}
		originalElems := original.Elems

		visitor := &IdentityVisitor{}
		result := original.Accept(visitor)

		// Result should be the same instance since no changes were made
		assert.Same(t, original, result)
		// Original should be unchanged
		assert.Equal(t, originalElems, original.Elems)
		assert.Same(t, prop, original.Elems[0])
	})

	t.Run("TupleType immutability", func(t *testing.T) {
		numType := NewNumType()
		strType := NewStrType()
		original := NewTupleType(numType, strType)
		originalElems := original.Elems

		visitor := &IdentityVisitor{}
		result := original.Accept(visitor)

		// Result should be the same instance since no changes were made
		assert.Same(t, original, result)
		// Original should be unchanged
		assert.Equal(t, originalElems, original.Elems)
		assert.Same(t, numType, original.Elems[0])
		assert.Same(t, strType, original.Elems[1])
	})

	t.Run("IntersectionType immutability", func(t *testing.T) {
		numType := NewNumType()
		objType := NewObjectType([]ObjTypeElem{
			NewPropertyElemType(NewStrKey("x"), NewStrType()),
		})
		original := NewIntersectionType(numType, objType)
		originalTypes := original.Types

		visitor := &IdentityVisitor{}
		result := original.Accept(visitor)

		// Result should be the same instance since no changes were made
		assert.Same(t, original, result)
		// Original should be unchanged
		assert.Equal(t, originalTypes, original.Types)
		assert.Same(t, numType, original.Types[0])
		assert.Same(t, objType, original.Types[1])
	})
}

// TestVisitorCreatesNewInstances tests that when changes are made, new instances are created
func TestVisitorCreatesNewInstances(t *testing.T) {
	t.Run("FuncType parameter replacement creates new instance", func(t *testing.T) {
		oldParamType := NewNumType()
		newParamType := NewStrType()

		param := NewFuncParam(NewIdentPat("x"), oldParamType)
		original := &FuncType{
			TypeParams: nil,
			Self:       nil,
			Params:     []*FuncParam{param},
			Return:     NewBoolType(),
			Throws:     NewNeverType(),
			provenance: nil,
		}

		visitor := NewTypeReplacementVisitor(map[Type]Type{
			oldParamType: newParamType,
		})
		result := original.Accept(visitor)

		// Result should be a different instance
		assert.NotSame(t, original, result)

		// Original should be unchanged
		assert.Same(t, oldParamType, original.Params[0].Type)

		// Result should have the new type
		resultFunc := result.(*FuncType)
		assert.Same(t, newParamType, resultFunc.Params[0].Type)

		// Other properties should be the same
		assert.Same(t, original.Return, resultFunc.Return)
		assert.Same(t, original.Throws, resultFunc.Throws)
	})

	t.Run("UnionType member replacement creates new instance", func(t *testing.T) {
		oldType := NewNumType()
		newType := NewStrType()
		otherType := NewBoolType()

		original := &UnionType{
			Types:      []Type{oldType, otherType},
			provenance: nil,
		}

		visitor := NewTypeReplacementVisitor(map[Type]Type{
			oldType: newType,
		})
		result := original.Accept(visitor)

		// Result should be a different instance
		assert.NotSame(t, original, result)

		// Original should be unchanged
		assert.Same(t, oldType, original.Types[0])
		assert.Same(t, otherType, original.Types[1])

		// Result should have the new type
		resultUnion := result.(*UnionType)
		assert.Same(t, newType, resultUnion.Types[0])
		assert.Same(t, otherType, resultUnion.Types[1])
	})

	t.Run("TupleType element replacement creates new instance", func(t *testing.T) {
		oldType := NewNumType()
		newType := NewStrType()
		otherType := NewBoolType()

		original := NewTupleType(oldType, otherType)

		visitor := NewTypeReplacementVisitor(map[Type]Type{
			oldType: newType,
		})
		result := original.Accept(visitor)

		// Result should be a different instance
		assert.NotSame(t, original, result)

		// Original should be unchanged
		assert.Same(t, oldType, original.Elems[0])
		assert.Same(t, otherType, original.Elems[1])

		// Result should have the new type
		resultTuple := result.(*TupleType)
		assert.Same(t, newType, resultTuple.Elems[0])
		assert.Same(t, otherType, resultTuple.Elems[1])
	})

	t.Run("ObjectType property replacement creates new instance", func(t *testing.T) {
		oldType := NewNumType()
		newType := NewStrType()

		original := NewObjectType([]ObjTypeElem{
			NewPropertyElemType(NewStrKey("x"), oldType),
		})

		visitor := NewTypeReplacementVisitor(map[Type]Type{
			oldType: newType,
		})
		result := original.Accept(visitor)

		// Result should be a different instance
		assert.NotSame(t, original, result)

		// Original should be unchanged
		originalProp := original.Elems[0].(*PropertyElemType)
		assert.Same(t, oldType, originalProp.Value)

		// Result should have the new type
		resultObj := result.(*ObjectType)
		resultProp := resultObj.Elems[0].(*PropertyElemType)
		assert.Same(t, newType, resultProp.Value)
	})
}

// TestNestedVisitorImmutability tests that deeply nested structures don't mutate originals
func TestNestedVisitorImmutability(t *testing.T) {
	t.Run("Nested function in union doesn't mutate original", func(t *testing.T) {
		innerNumType := NewNumType()
		innerStrType := NewStrType()

		funcType := &FuncType{
			TypeParams: nil,
			Self:       nil,
			Params:     []*FuncParam{NewFuncParam(NewIdentPat("x"), innerNumType)},
			Return:     innerStrType,
			Throws:     NewNeverType(),
			provenance: nil,
		}

		unionType := &UnionType{
			Types:      []Type{funcType, NewBoolType()},
			provenance: nil,
		}

		// Store references to verify immutability
		originalFuncType := unionType.Types[0]
		originalParam := funcType.Params[0]
		originalParamType := funcType.Params[0].Type

		visitor := &IdentityVisitor{}
		result := unionType.Accept(visitor)

		// Result should be the same instance since no changes were made
		assert.Same(t, unionType, result)

		// All nested structures should be unchanged
		assert.Same(t, originalFuncType, unionType.Types[0])
		assert.Same(t, originalParam, funcType.Params[0])
		assert.Same(t, originalParamType, funcType.Params[0].Type)
	})

	t.Run("Deeply nested replacement creates appropriate new instances", func(t *testing.T) {
		oldType := NewNumType()
		newType := NewStrType()

		// Create: (oldType) => boolean | string
		funcType := &FuncType{
			TypeParams: nil,
			Self:       nil,
			Params:     []*FuncParam{NewFuncParam(NewIdentPat("x"), oldType)},
			Return:     NewBoolType(),
			Throws:     NewNeverType(),
			provenance: nil,
		}

		unionType := &UnionType{
			Types:      []Type{funcType, NewStrType()},
			provenance: nil,
		}

		visitor := NewTypeReplacementVisitor(map[Type]Type{
			oldType: newType,
		})
		result := unionType.Accept(visitor)

		// Result should be a different instance because nested content changed
		assert.NotSame(t, unionType, result)

		// Original should be completely unchanged
		originalFunc := unionType.Types[0].(*FuncType)
		assert.Same(t, oldType, originalFunc.Params[0].Type)

		// Result should have new instances with changes propagated
		resultUnion := result.(*UnionType)
		resultFunc := resultUnion.Types[0].(*FuncType)
		assert.NotSame(t, funcType, resultFunc)            // New function instance
		assert.Same(t, newType, resultFunc.Params[0].Type) // Changed parameter type
		assert.Same(t, funcType.Return, resultFunc.Return) // Unchanged return type
	})
}

// TestComplexTypeStructures tests visitor behavior with complex type structures
func TestComplexTypeStructures(t *testing.T) {
	t.Run("Conditional type immutability", func(t *testing.T) {
		checkType := NewNumType()
		extendsType := NewStrType()
		consType := NewBoolType()
		altType := NewUnknownType()

		original := &CondType{
			Check:      checkType,
			Extends:    extendsType,
			Cons:       consType,
			Alt:        altType,
			provenance: nil,
		}

		visitor := &IdentityVisitor{}
		result := original.Accept(visitor)

		// Result should be the same instance since no changes were made
		assert.Same(t, original, result)

		// All fields should be unchanged
		assert.Same(t, checkType, original.Check)
		assert.Same(t, extendsType, original.Extends)
		assert.Same(t, consType, original.Cons)
		assert.Same(t, altType, original.Alt)
	})

	t.Run("Index type immutability", func(t *testing.T) {
		targetType := NewObjectType([]ObjTypeElem{
			NewPropertyElemType(NewStrKey("x"), NewNumType()),
		})
		indexType := &LitType{
			Lit:        &StrLit{Value: "x"},
			provenance: nil,
		}

		original := &IndexType{
			Target:     targetType,
			Index:      indexType,
			provenance: nil,
		}

		visitor := &IdentityVisitor{}
		result := original.Accept(visitor)

		// Result should be the same instance since no changes were made
		assert.Same(t, original, result)

		// All fields should be unchanged
		assert.Same(t, targetType, original.Target)
		assert.Same(t, indexType, original.Index)
	})

	t.Run("KeyOf type immutability", func(t *testing.T) {
		targetType := NewObjectType([]ObjTypeElem{
			NewPropertyElemType(NewStrKey("x"), NewNumType()),
			NewPropertyElemType(NewStrKey("y"), NewStrType()),
		})

		original := &KeyOfType{
			Type:       targetType,
			provenance: nil,
		}

		visitor := &IdentityVisitor{}
		result := original.Accept(visitor)

		// Result should be the same instance since no changes were made
		assert.Same(t, original, result)

		// Target type should be unchanged
		assert.Same(t, targetType, original.Type)
	})
}

// TestTypeVarVisitor tests visitor behavior with type variables
func TestTypeVarVisitor(t *testing.T) {
	t.Run("TypeVar without instance", func(t *testing.T) {
		original := &TypeVarType{
			ID:         1,
			Instance:   nil,
			provenance: nil,
		}

		visitor := &IdentityVisitor{}
		result := original.Accept(visitor)

		// Result should be the same instance since no changes were made
		assert.Same(t, original, result)

		// Fields should be unchanged
		assert.Equal(t, 1, original.ID)
		assert.Nil(t, original.Instance)
	})

	t.Run("TypeVar with instance", func(t *testing.T) {
		instanceType := NewNumType()
		original := &TypeVarType{
			ID:         1,
			Instance:   instanceType,
			provenance: nil,
		}

		visitor := &IdentityVisitor{}
		result := original.Accept(visitor)

		// Since the IdentityVisitor returns nil, Accept should return the original TypeVar
		// (even though it has an instance, the visitor doesn't transform it)
		assert.Same(t, original, result)

		// Original should be unchanged
		assert.Equal(t, 1, original.ID)
		assert.Same(t, instanceType, original.Instance)
	})
}

// TestEnterTypeIsCalled tests that EnterType is called for all types during traversal
func TestEnterTypeIsCalled(t *testing.T) {
	t.Run("Simple type calls EnterType", func(t *testing.T) {
		numType := NewNumType()
		visitor := NewTrackingVisitor()

		numType.Accept(visitor)

		enteredTypes := visitor.GetEnteredTypes()
		assert.Len(t, enteredTypes, 1)
		assert.Same(t, numType, enteredTypes[0])

		exitedTypes := visitor.GetExitedTypes()
		assert.Len(t, exitedTypes, 1)
		assert.Same(t, numType, exitedTypes[0])
	})

	t.Run("Union type calls EnterType for all members", func(t *testing.T) {
		numType := NewNumType()
		strType := NewStrType()
		unionType := NewUnionType(numType, strType).(*UnionType)
		visitor := NewTrackingVisitor()

		unionType.Accept(visitor)

		enteredTypes := visitor.GetEnteredTypes()
		// Should enter: numType, strType, unionType (in that order due to traversal)
		assert.Len(t, enteredTypes, 3)
		assert.Same(t, unionType, enteredTypes[0]) // Union entered first
		assert.Same(t, numType, enteredTypes[1])   // Then first member
		assert.Same(t, strType, enteredTypes[2])   // Then second member

		exitedTypes := visitor.GetExitedTypes()
		assert.Len(t, exitedTypes, 3)
		assert.Same(t, numType, exitedTypes[0])   // First member exited first
		assert.Same(t, strType, exitedTypes[1])   // Then second member
		assert.Same(t, unionType, exitedTypes[2]) // Union exited last
	})

	t.Run("Function type calls EnterType for parameters and return type", func(t *testing.T) {
		paramType := NewNumType()
		returnType := NewStrType()
		param := NewFuncParam(NewIdentPat("x"), paramType)
		funcType := &FuncType{
			TypeParams: nil,
			Self:       nil,
			Params:     []*FuncParam{param},
			Return:     returnType,
			Throws:     nil,
			provenance: nil,
		}
		visitor := NewTrackingVisitor()

		funcType.Accept(visitor)

		enteredTypes := visitor.GetEnteredTypes()
		// Should enter: funcType, paramType, returnType
		assert.Len(t, enteredTypes, 3)
		assert.Same(t, funcType, enteredTypes[0])   // Function entered first
		assert.Same(t, paramType, enteredTypes[1])  // Then parameter type
		assert.Same(t, returnType, enteredTypes[2]) // Then return type

		exitedTypes := visitor.GetExitedTypes()
		assert.Len(t, exitedTypes, 3)
		assert.Same(t, paramType, exitedTypes[0])  // Parameter type exited first
		assert.Same(t, returnType, exitedTypes[1]) // Then return type
		assert.Same(t, funcType, exitedTypes[2])   // Function exited last
	})

	t.Run("Object type calls EnterType for property types", func(t *testing.T) {
		propType := NewNumType()
		prop := NewPropertyElemType(NewStrKey("x"), propType)
		objType := NewObjectType([]ObjTypeElem{prop})
		visitor := NewTrackingVisitor()

		objType.Accept(visitor)

		enteredTypes := visitor.GetEnteredTypes()
		// Should enter: objType, propType
		assert.Len(t, enteredTypes, 2)
		assert.Same(t, objType, enteredTypes[0])  // Object entered first
		assert.Same(t, propType, enteredTypes[1]) // Then property type

		exitedTypes := visitor.GetExitedTypes()
		assert.Len(t, exitedTypes, 2)
		assert.Same(t, propType, exitedTypes[0]) // Property type exited first
		assert.Same(t, objType, exitedTypes[1])  // Object exited last
	})

	t.Run("Tuple type calls EnterType for all elements", func(t *testing.T) {
		numType := NewNumType()
		strType := NewStrType()
		boolType := NewBoolType()
		tupleType := NewTupleType(numType, strType, boolType)
		visitor := NewTrackingVisitor()

		tupleType.Accept(visitor)

		enteredTypes := visitor.GetEnteredTypes()
		// Should enter: tupleType, numType, strType, boolType
		assert.Len(t, enteredTypes, 4)
		assert.Same(t, tupleType, enteredTypes[0]) // Tuple entered first
		assert.Same(t, numType, enteredTypes[1])   // Then first element
		assert.Same(t, strType, enteredTypes[2])   // Then second element
		assert.Same(t, boolType, enteredTypes[3])  // Then third element

		exitedTypes := visitor.GetExitedTypes()
		assert.Len(t, exitedTypes, 4)
		assert.Same(t, numType, exitedTypes[0])   // First element exited first
		assert.Same(t, strType, exitedTypes[1])   // Then second element
		assert.Same(t, boolType, exitedTypes[2])  // Then third element
		assert.Same(t, tupleType, exitedTypes[3]) // Tuple exited last
	})

	t.Run("Nested types call EnterType in correct order", func(t *testing.T) {
		// Create: (number | string) => boolean
		innerNumType := NewNumType()
		innerStrType := NewStrType()
		unionType := NewUnionType(innerNumType, innerStrType).(*UnionType)
		returnType := NewBoolType()
		param := NewFuncParam(NewIdentPat("x"), unionType)
		funcType := &FuncType{
			TypeParams: nil,
			Self:       nil,
			Params:     []*FuncParam{param},
			Return:     returnType,
			Throws:     nil,
			provenance: nil,
		}
		visitor := NewTrackingVisitor()

		funcType.Accept(visitor)

		enteredTypes := visitor.GetEnteredTypes()
		// Should enter: funcType, unionType, innerNumType, innerStrType, returnType
		assert.Len(t, enteredTypes, 5)
		assert.Same(t, funcType, enteredTypes[0])     // Function entered first
		assert.Same(t, unionType, enteredTypes[1])    // Then parameter union type
		assert.Same(t, innerNumType, enteredTypes[2]) // Then first union member
		assert.Same(t, innerStrType, enteredTypes[3]) // Then second union member
		assert.Same(t, returnType, enteredTypes[4])   // Finally return type

		exitedTypes := visitor.GetExitedTypes()
		assert.Len(t, exitedTypes, 5)
		assert.Same(t, innerNumType, exitedTypes[0]) // First union member exited first
		assert.Same(t, innerStrType, exitedTypes[1]) // Then second union member
		assert.Same(t, unionType, exitedTypes[2])    // Then union type
		assert.Same(t, returnType, exitedTypes[3])   // Then return type
		assert.Same(t, funcType, exitedTypes[4])     // Function exited last
	})
}

// TestEnterTypeWithTransformation tests that EnterType is called even when transformations occur
func TestEnterTypeWithTransformation(t *testing.T) {
	t.Run("EnterType called before transformation", func(t *testing.T) {
		oldType := NewNumType()
		newType := NewStrType()

		// Create a visitor that both tracks and transforms
		visitor := &TransformingTrackingVisitor{
			enteredTypes: make([]Type, 0),
			exitedTypes:  make([]Type, 0),
			oldType:      oldType,
			newType:      newType,
		}

		result := oldType.Accept(visitor)

		// Should have been transformed
		assert.Same(t, newType, result)

		// EnterType should still have been called with the original type
		assert.Len(t, visitor.enteredTypes, 1)
		assert.Same(t, oldType, visitor.enteredTypes[0])

		assert.Len(t, visitor.exitedTypes, 1)
		assert.Same(t, oldType, visitor.exitedTypes[0])
	})
}

// TransformingTrackingVisitor tracks entries/exits and performs transformations
type TransformingTrackingVisitor struct {
	enteredTypes []Type
	exitedTypes  []Type
	oldType      Type
	newType      Type
}

func (v *TransformingTrackingVisitor) EnterType(t Type) {
	v.enteredTypes = append(v.enteredTypes, t)
}

func (v *TransformingTrackingVisitor) ExitType(t Type) Type {
	v.exitedTypes = append(v.exitedTypes, t)
	if t == v.oldType {
		return v.newType
	}
	return nil
}

// TestEnterTypeCallOrderMatters tests that EnterType/ExitType maintain proper order
func TestEnterTypeCallOrderMatters(t *testing.T) {
	t.Run("EnterType called before child traversal", func(t *testing.T) {
		// Create a visitor that records the exact order of operations
		operations := make([]string, 0)

		enterFunc := func(t Type) {
			switch typ := t.(type) {
			case *UnionType:
				operations = append(operations, "enter-union")
			case *PrimType:
				if typ.Prim == NumPrim {
					operations = append(operations, "enter-number")
				} else if typ.Prim == StrPrim {
					operations = append(operations, "enter-string")
				}
			default:
				// Handle other types if needed
			}
		}

		exitFunc := func(t Type) Type {
			switch typ := t.(type) {
			case *UnionType:
				operations = append(operations, "exit-union")
			case *PrimType:
				if typ.Prim == NumPrim {
					operations = append(operations, "exit-number")
				} else if typ.Prim == StrPrim {
					operations = append(operations, "exit-string")
				}
			default:
				// Handle other types if needed
			}
			return nil
		}

		// Create a typed visitor
		typedVisitor := &OrderTrackingVisitor{
			enterFunc: enterFunc,
			exitFunc:  exitFunc,
		}

		numType := NewNumType()
		strType := NewStrType()
		unionType := NewUnionType(numType, strType).(*UnionType)

		unionType.Accept(typedVisitor)

		// Should be: enter-union, enter-number, exit-number, enter-string, exit-string, exit-union
		expected := []string{"enter-union", "enter-number", "exit-number", "enter-string", "exit-string", "exit-union"}
		assert.Equal(t, expected, operations)
	})
}

// OrderTrackingVisitor helps track the order of enter/exit calls
type OrderTrackingVisitor struct {
	enterFunc func(Type)
	exitFunc  func(Type) Type
}

func (v *OrderTrackingVisitor) EnterType(t Type) {
	v.enterFunc(t)
}

func (v *OrderTrackingVisitor) ExitType(t Type) Type {
	return v.exitFunc(t)
}

// TestEnterTypeWithComplexStructures tests EnterType with more complex type structures
func TestEnterTypeWithComplexStructures(t *testing.T) {
	t.Run("Conditional type structure", func(t *testing.T) {
		checkType := NewNumType()
		extendsType := NewStrType()
		consType := NewBoolType()
		altType := NewUnknownType()

		condType := &CondType{
			Check:      checkType,
			Extends:    extendsType,
			Cons:       consType,
			Alt:        altType,
			provenance: nil,
		}

		visitor := NewTrackingVisitor()
		condType.Accept(visitor)

		enteredTypes := visitor.GetEnteredTypes()
		// Should enter: condType, checkType, extendsType, consType, altType
		assert.Len(t, enteredTypes, 5)
		assert.Same(t, condType, enteredTypes[0])
		assert.Same(t, checkType, enteredTypes[1])
		assert.Same(t, extendsType, enteredTypes[2])
		assert.Same(t, consType, enteredTypes[3])
		assert.Same(t, altType, enteredTypes[4])
	})

	t.Run("Index type structure", func(t *testing.T) {
		targetType := NewObjectType([]ObjTypeElem{
			NewPropertyElemType(NewStrKey("x"), NewNumType()),
		})
		indexType := &LitType{
			Lit:        &StrLit{Value: "x"},
			provenance: nil,
		}

		idxType := &IndexType{
			Target:     targetType,
			Index:      indexType,
			provenance: nil,
		}

		visitor := NewTrackingVisitor()
		idxType.Accept(visitor)

		enteredTypes := visitor.GetEnteredTypes()
		// Should enter: idxType, targetType, property type (NumType), indexType
		assert.GreaterOrEqual(t, len(enteredTypes), 4)
		assert.Same(t, idxType, enteredTypes[0])
		assert.Same(t, targetType, enteredTypes[1])
		// Note: The exact order may vary based on implementation, but all should be present

		// Verify all expected types are entered
		typeSet := make(map[Type]bool)
		for _, enteredType := range enteredTypes {
			typeSet[enteredType] = true
		}
		assert.True(t, typeSet[idxType])
		assert.True(t, typeSet[targetType])
		assert.True(t, typeSet[indexType])
	})
}

// BenchmarkVisitorTraversal benchmarks visitor performance on complex type structures
func BenchmarkVisitorTraversal(b *testing.B) {
	// Create a complex nested type structure
	complexType := &UnionType{
		Types: []Type{
			&FuncType{
				TypeParams: nil,
				Self:       nil,
				Params: []*FuncParam{
					NewFuncParam(NewIdentPat("x"), NewNumType()),
					NewFuncParam(NewIdentPat("y"), NewStrType()),
				},
				Return: &IntersectionType{
					Types: []Type{
						NewObjectType([]ObjTypeElem{
							NewPropertyElemType(NewStrKey("a"), NewBoolType()),
						}),
						NewObjectType([]ObjTypeElem{
							NewPropertyElemType(NewStrKey("b"), NewNumType()),
						}),
					},
					provenance: nil,
				},
				Throws:     NewNeverType(),
				provenance: nil,
			},
			NewTupleType(NewNumType(), NewStrType(), NewBoolType()),
		},
		provenance: nil,
	}

	visitor := &IdentityVisitor{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		complexType.Accept(visitor)
	}
}
