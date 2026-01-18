package type_system

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIntersectionNormalization tests the normalization logic in NewIntersectionType
func TestIntersectionNormalization(t *testing.T) {
	t.Run("empty intersection returns never", func(t *testing.T) {
		result := NewIntersectionType(nil)
		assert.Equal(t, "never", result.String())
	})

	t.Run("single type intersection returns the type", func(t *testing.T) {
		strType := NewStrPrimType(nil)
		result := NewIntersectionType(nil, strType)
		assert.Equal(t, "string", result.String())
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

		inner := NewIntersectionType(nil, obj1, obj2)
		result := NewIntersectionType(nil, inner, obj3)

		// Should flatten to single intersection with 3 types
		assert.Equal(t, "{a: string} & {b: number} & {c: boolean}", result.String())
	})

	t.Run("removes duplicates", func(t *testing.T) {
		strType := NewStrPrimType(nil)
		result := NewIntersectionType(nil, strType, strType, strType)
		assert.Equal(t, "string", result.String())
	})

	t.Run("A & never returns never", func(t *testing.T) {
		strType := NewStrPrimType(nil)
		neverType := NewNeverType(nil)
		result := NewIntersectionType(nil, strType, neverType)
		assert.Equal(t, "never", result.String())
	})

	t.Run("A & unknown returns A", func(t *testing.T) {
		strType := NewStrPrimType(nil)
		unknownType := NewUnknownType(nil)
		result := NewIntersectionType(nil, strType, unknownType)
		assert.Equal(t, "string", result.String())
	})

	t.Run("A & any returns any", func(t *testing.T) {
		strType := NewStrPrimType(nil)
		anyType := NewAnyType(nil)
		result := NewIntersectionType(nil, strType, anyType)
		assert.Equal(t, "any", result.String())
	})

	t.Run("conflicting primitives return never", func(t *testing.T) {
		strType := NewStrPrimType(nil)
		numType := NewNumPrimType(nil)
		result := NewIntersectionType(nil, strType, numType)
		assert.Equal(t, "never", result.String())
	})

	t.Run("same primitive types are deduplicated", func(t *testing.T) {
		strType1 := NewStrPrimType(nil)
		strType2 := NewStrPrimType(nil)
		result := NewIntersectionType(nil, strType1, strType2)
		assert.Equal(t, "string", result.String())
	})

	t.Run("(mut T) & T returns T", func(t *testing.T) {
		obj := NewObjectType(nil, []ObjTypeElem{
			NewPropertyElem(NewStrKey("a"), NewStrPrimType(nil)),
		})
		mutObj := NewMutableType(nil, obj)
		result := NewIntersectionType(nil, mutObj, obj)
		// Should return the immutable version
		assert.Equal(t, "{a: string}", result.String())
	})

	t.Run("T & (mut T) returns T", func(t *testing.T) {
		obj := NewObjectType(nil, []ObjTypeElem{
			NewPropertyElem(NewStrKey("a"), NewStrPrimType(nil)),
		})
		mutObj := NewMutableType(nil, obj)
		result := NewIntersectionType(nil, obj, mutObj)
		// Should return the immutable version
		assert.Equal(t, "{a: string}", result.String())
	})

	t.Run("multiple unknown types are removed", func(t *testing.T) {
		strType := NewStrPrimType(nil)
		unknownType1 := NewUnknownType(nil)
		unknownType2 := NewUnknownType(nil)
		result := NewIntersectionType(nil, strType, unknownType1, unknownType2)
		assert.Equal(t, "string", result.String())
	})

	t.Run("only unknown types return never", func(t *testing.T) {
		unknownType1 := NewUnknownType(nil)
		unknownType2 := NewUnknownType(nil)
		result := NewIntersectionType(nil, unknownType1, unknownType2)
		assert.Equal(t, "never", result.String())
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
		assert.Equal(t, "{a: string} & {b: string}", result.String())
	})
}
