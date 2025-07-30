package type_system

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// IdentityVisitor is a visitor that returns nil from VisitType,
// meaning it doesn't transform any types but allows traversal
type IdentityVisitor struct{}

func (v *IdentityVisitor) VisitType(t Type) Type {
	return nil // Continue traversal without transformation
}

// TypeReplacementVisitor replaces specific types with other types
type TypeReplacementVisitor struct {
	replacements map[Type]Type
}

func NewTypeReplacementVisitor(replacements map[Type]Type) *TypeReplacementVisitor {
	return &TypeReplacementVisitor{replacements: replacements}
}

func (v *TypeReplacementVisitor) VisitType(t Type) Type {
	if replacement, found := v.replacements[t]; found {
		return replacement
	}
	return nil // Continue traversal
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
			Nomimal:    false,
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
