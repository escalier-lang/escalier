package tests

import (
	"testing"

	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
)

// TestNormalizeIntersectionType tests the post-inference normalization logic
func TestNormalizeIntersectionType(t *testing.T) {
	t.Run("normalizes after type variable resolution", func(t *testing.T) {
		c := NewChecker()
		ctx := Context{Scope: Prelude(c), IsAsync: false, IsPatMatch: false}

		// Create a type variable
		typeVar := c.FreshVar(nil)
		strType := type_system.NewStrPrimType(nil)

		// Create an intersection with type variable
		intersection := type_system.NewIntersectionType(nil, typeVar, strType).(*type_system.IntersectionType)

		// Bind the type variable to string
		typeVar.Instance = strType

		// Normalize should detect that both are now string
		result := c.NormalizeIntersectionType(ctx, intersection)
		assert.Equal(t, strType, result, "Expected normalization to reduce string & string to string")
	})

	t.Run("normalizes to never when type variable resolves to conflicting primitive", func(t *testing.T) {
		c := NewChecker()
		ctx := Context{Scope: Prelude(c), IsAsync: false, IsPatMatch: false}

		// Create a type variable
		typeVar := c.FreshVar(nil)
		strType := type_system.NewStrPrimType(nil)

		// Create an intersection with string & T
		intersection := type_system.NewIntersectionType(nil, strType, typeVar).(*type_system.IntersectionType)

		// Bind the type variable to number (conflicts with string)
		numType := type_system.NewNumPrimType(nil)
		typeVar.Instance = numType

		// Normalize should detect string & number → never
		result := c.NormalizeIntersectionType(ctx, intersection)
		assert.IsType(t, &type_system.NeverType{}, result, "Expected string & number to normalize to never")
	})

	t.Run("normalizes multiple type variables to same type", func(t *testing.T) {
		c := NewChecker()
		ctx := Context{Scope: Prelude(c), IsAsync: false, IsPatMatch: false}

		// Create two type variables
		typeVar1 := c.FreshVar(nil)
		typeVar2 := c.FreshVar(nil)
		strType := type_system.NewStrPrimType(nil)

		// Create an intersection with T & U
		intersection := type_system.NewIntersectionType(nil, typeVar1, typeVar2).(*type_system.IntersectionType)

		// Bind both to string
		typeVar1.Instance = strType
		typeVar2.Instance = strType

		// Normalize should reduce to string
		result := c.NormalizeIntersectionType(ctx, intersection)
		assert.Equal(t, strType, result, "Expected T & U to normalize to string when both resolve to string")
	})

	t.Run("handles nested intersections with type variables", func(t *testing.T) {
		c := NewChecker()

		typeVar := c.FreshVar(nil)
		strType := type_system.NewStrPrimType(nil)
		numType := type_system.NewNumPrimType(nil)

		// Create (string & T) & number - will be flattened to string & T & number
		inner := type_system.NewIntersectionType(nil, strType, typeVar)
		outer := type_system.NewIntersectionType(nil, inner, numType)

		// At construction, outer is already string & number & T
		// which is normalized to never because string & number is incompatible
		// This is the correct behavior - NewIntersectionType normalizes eagerly
		assert.IsType(t, &type_system.NeverType{}, outer, "Expected string & T & number to normalize to never at construction")

		// Binding T after construction doesn't change the result
		typeVar.Instance = strType
	})

	t.Run("preserves object types after type variable resolution", func(t *testing.T) {
		c := NewChecker()
		ctx := Context{Scope: Prelude(c), IsAsync: false, IsPatMatch: false}

		typeVar := c.FreshVar(nil)
		obj := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			type_system.NewPropertyElem(type_system.NewStrKey("a"), type_system.NewStrPrimType(nil)),
		})

		// Create T & {a: string}
		intersection := type_system.NewIntersectionType(nil, typeVar, obj).(*type_system.IntersectionType)

		// Bind T to a different object type
		obj2 := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			type_system.NewPropertyElem(type_system.NewStrKey("b"), type_system.NewNumPrimType(nil)),
		})
		typeVar.Instance = obj2

		// Normalize should keep both object types
		result := c.NormalizeIntersectionType(ctx, intersection)
		resultIntersection, ok := result.(*type_system.IntersectionType)
		assert.True(t, ok, "Expected IntersectionType")
		assert.Equal(t, 2, len(resultIntersection.Types), "Expected two object types in intersection")
	})

	t.Run("handles any after type variable resolution", func(t *testing.T) {
		c := NewChecker()
		ctx := Context{Scope: Prelude(c), IsAsync: false, IsPatMatch: false}

		typeVar := c.FreshVar(nil)
		strType := type_system.NewStrPrimType(nil)

		// Create T & string
		intersection := type_system.NewIntersectionType(nil, typeVar, strType).(*type_system.IntersectionType)

		// Bind T to any
		anyType := type_system.NewAnyType(nil)
		typeVar.Instance = anyType

		// Normalize should return any
		result := c.NormalizeIntersectionType(ctx, intersection)
		assert.IsType(t, &type_system.AnyType{}, result, "Expected any & string to normalize to any")
	})

	t.Run("handles never after type variable resolution", func(t *testing.T) {
		c := NewChecker()
		ctx := Context{Scope: Prelude(c), IsAsync: false, IsPatMatch: false}

		typeVar := c.FreshVar(nil)
		strType := type_system.NewStrPrimType(nil)

		// Create T & string
		intersection := type_system.NewIntersectionType(nil, typeVar, strType).(*type_system.IntersectionType)

		// Bind T to never
		neverType := type_system.NewNeverType(nil)
		typeVar.Instance = neverType

		// Normalize should return never
		result := c.NormalizeIntersectionType(ctx, intersection)
		assert.IsType(t, &type_system.NeverType{}, result, "Expected never & string to normalize to never")
	})

	t.Run("handles mutability after type variable resolution", func(t *testing.T) {
		c := NewChecker()
		ctx := Context{Scope: Prelude(c), IsAsync: false, IsPatMatch: false}

		typeVar := c.FreshVar(nil)
		obj := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			type_system.NewPropertyElem(type_system.NewStrKey("a"), type_system.NewStrPrimType(nil)),
		})

		// Create T & {a: string}
		intersection := type_system.NewIntersectionType(nil, typeVar, obj).(*type_system.IntersectionType)

		// Bind T to (mut {a: string})
		mutObj := type_system.NewMutableType(nil, obj)
		typeVar.Instance = mutObj

		// Normalize should return the immutable version
		result := c.NormalizeIntersectionType(ctx, intersection)
		assert.Equal(t, obj, result, "Expected (mut T) & T to normalize to T")
	})

	t.Run("expands type aliases within intersection", func(t *testing.T) {
		c := NewChecker()
		ctx := Context{Scope: Prelude(c), IsAsync: false, IsPatMatch: false}

		// Create a type alias
		strType := type_system.NewStrPrimType(nil)
		typeAlias := &type_system.TypeAlias{
			Type: strType,
		}

		// Add the type alias to the scope
		ctx.Scope.SetTypeAlias("MyString", typeAlias)

		// Create a type reference to the alias
		typeRef := type_system.NewTypeRefType(nil, "MyString", typeAlias)

		// Create an intersection with the type alias and string
		intersection := type_system.NewIntersectionType(nil, typeRef, strType).(*type_system.IntersectionType)

		// Normalize should expand the alias and reduce to string
		result := c.NormalizeIntersectionType(ctx, intersection)
		assert.Equal(t, strType, result, "Expected MyString & string to normalize to string after expanding alias")
	})

	t.Run("expands multiple type aliases to same underlying type", func(t *testing.T) {
		c := NewChecker()
		ctx := Context{Scope: Prelude(c), IsAsync: false, IsPatMatch: false}

		// Create the underlying type
		strType := type_system.NewStrPrimType(nil)

		// Create two type aliases that both resolve to string
		typeAlias1 := &type_system.TypeAlias{
			Type: strType,
		}
		typeAlias2 := &type_system.TypeAlias{
			Type: strType,
		}

		// Add both type aliases to the scope
		ctx.Scope.SetTypeAlias("MyString", typeAlias1)
		ctx.Scope.SetTypeAlias("YourString", typeAlias2)

		// Create type references to the aliases
		typeRef1 := type_system.NewTypeRefType(nil, "MyString", typeAlias1)
		typeRef2 := type_system.NewTypeRefType(nil, "YourString", typeAlias2)

		// Create an intersection with both type aliases
		intersection := type_system.NewIntersectionType(nil, typeRef1, typeRef2).(*type_system.IntersectionType)

		// Normalize should expand both aliases and reduce to string
		result := c.NormalizeIntersectionType(ctx, intersection)
		assert.Equal(t, strType, result, "Expected MyString & YourString to normalize to string after expanding aliases")
	})
}
