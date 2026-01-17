package type_system

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIntersectionNormalization tests the normalization logic in NewIntersectionType
func TestIntersectionNormalization(t *testing.T) {
	t.Run("empty intersection returns never", func(t *testing.T) {
		result := NewIntersectionType(nil)
		assert.IsType(t, &NeverType{}, result)
	})

	t.Run("single type intersection returns the type", func(t *testing.T) {
		strType := NewStrPrimType(nil)
		result := NewIntersectionType(nil, strType)
		assert.Equal(t, strType, result)
	})

	t.Run("flattens nested intersections", func(t *testing.T) {
		strType := NewStrPrimType(nil)
		numType := NewNumPrimType(nil)
		boolType := NewBoolPrimType(nil)

		// Create (string & number) & boolean
		// This should return never due to conflicting primitives
		// But let's test with objects instead
		obj1 := NewObjectType(nil, []ObjTypeElem{
			NewPropertyElem(NewStrKey("a"), strType),
		})
		obj2 := NewObjectType(nil, []ObjTypeElem{
			NewPropertyElem(NewStrKey("b"), numType),
		})
		obj3 := NewObjectType(nil, []ObjTypeElem{
			NewPropertyElem(NewStrKey("c"), boolType),
		})

		inner2 := NewIntersectionType(nil, obj1, obj2)
		result := NewIntersectionType(nil, inner2, obj3)

		// Should flatten to single intersection with 3 types
		intersection, ok := result.(*IntersectionType)
		assert.True(t, ok, "Expected IntersectionType")
		assert.Equal(t, 3, len(intersection.Types))
	})

	t.Run("removes duplicates", func(t *testing.T) {
		strType := NewStrPrimType(nil)
		result := NewIntersectionType(nil, strType, strType, strType)
		assert.Equal(t, strType, result)
	})

	t.Run("A & never returns never", func(t *testing.T) {
		strType := NewStrPrimType(nil)
		neverType := NewNeverType(nil)
		result := NewIntersectionType(nil, strType, neverType)
		assert.IsType(t, &NeverType{}, result)
	})

	t.Run("A & unknown returns A", func(t *testing.T) {
		strType := NewStrPrimType(nil)
		unknownType := NewUnknownType(nil)
		result := NewIntersectionType(nil, strType, unknownType)
		assert.Equal(t, strType, result)
	})

	t.Run("A & any returns any", func(t *testing.T) {
		strType := NewStrPrimType(nil)
		anyType := NewAnyType(nil)
		result := NewIntersectionType(nil, strType, anyType)
		assert.IsType(t, &AnyType{}, result)
	})

	t.Run("conflicting primitives return never", func(t *testing.T) {
		strType := NewStrPrimType(nil)
		numType := NewNumPrimType(nil)
		result := NewIntersectionType(nil, strType, numType)
		assert.IsType(t, &NeverType{}, result)
	})

	t.Run("same primitive types are deduplicated", func(t *testing.T) {
		strType1 := NewStrPrimType(nil)
		strType2 := NewStrPrimType(nil)
		result := NewIntersectionType(nil, strType1, strType2)
		assert.Equal(t, strType1, result)
	})

	t.Run("(mut T) & T returns T", func(t *testing.T) {
		obj := NewObjectType(nil, []ObjTypeElem{
			NewPropertyElem(NewStrKey("a"), NewStrPrimType(nil)),
		})
		mutObj := NewMutableType(nil, obj)
		result := NewIntersectionType(nil, mutObj, obj)
		// Should return the immutable version
		assert.Equal(t, obj, result)
	})

	t.Run("T & (mut T) returns T", func(t *testing.T) {
		obj := NewObjectType(nil, []ObjTypeElem{
			NewPropertyElem(NewStrKey("a"), NewStrPrimType(nil)),
		})
		mutObj := NewMutableType(nil, obj)
		result := NewIntersectionType(nil, obj, mutObj)
		// Should return the immutable version
		assert.Equal(t, obj, result)
	})

	t.Run("multiple unknown types are removed", func(t *testing.T) {
		strType := NewStrPrimType(nil)
		unknownType1 := NewUnknownType(nil)
		unknownType2 := NewUnknownType(nil)
		result := NewIntersectionType(nil, strType, unknownType1, unknownType2)
		assert.Equal(t, strType, result)
	})

	t.Run("only unknown types return never", func(t *testing.T) {
		unknownType1 := NewUnknownType(nil)
		unknownType2 := NewUnknownType(nil)
		result := NewIntersectionType(nil, unknownType1, unknownType2)
		assert.IsType(t, &NeverType{}, result)
	})

	t.Run("complex normalization - mixed types", func(t *testing.T) {
		strType := NewStrPrimType(nil)
		unknownType := NewUnknownType(nil)
		obj1 := NewObjectType(nil, []ObjTypeElem{
			NewPropertyElem(NewStrKey("a"), strType),
		})
		obj2 := NewObjectType(nil, []ObjTypeElem{
			NewPropertyElem(NewStrKey("b"), strType),
		})

		result := NewIntersectionType(nil, obj1, unknownType, obj2, obj1)
		// Should remove unknown and deduplicate obj1
		intersection, ok := result.(*IntersectionType)
		assert.True(t, ok, "Expected IntersectionType")
		assert.Equal(t, 2, len(intersection.Types))
	})
}
