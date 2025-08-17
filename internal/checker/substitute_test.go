package checker

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/test_util"
	. "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
)

func TestTypeParamSubstitutionVisitor(t *testing.T) {
	t.Run("Direct visitor usage", func(t *testing.T) {
		// Create a function type: (T, U) -> T
		funcType := test_util.ParseTypeAnn("fn (x: T, y: U) -> T")

		// Create substitution map: T -> number, U -> string
		substitutions := map[string]Type{
			"T": NewNumType(),
			"U": NewStrType(),
		}

		visitor := NewTypeParamSubstitutionVisitor(substitutions)
		result := visitor.SubstituteType(funcType)

		assert.Equal(t, "fn (x: T, y: U) -> T", funcType.String())
		assert.Equal(t, "fn (x: number, y: string) -> number", result.String())
	})

	t.Run("Visitor reusability", func(t *testing.T) {
		substitutions := map[string]Type{
			"T": NewStrType(),
		}

		visitor := NewTypeParamSubstitutionVisitor(substitutions)

		// Use the same visitor for multiple types
		type1 := test_util.ParseTypeAnn("T")
		type2 := test_util.ParseTypeAnn("[T, number]")

		result1 := visitor.SubstituteType(type1)
		result2 := visitor.SubstituteType(type2)

		assert.Equal(t, "string", result1.String())
		assert.Equal(t, "[string, number]", result2.String())
	})
}

func TestSubstituteTypeParams(t *testing.T) {
	checker := &Checker{ID: 0}

	t.Run("TypeRefType substitution", func(t *testing.T) {
		t.Run("substitutes type parameter", func(t *testing.T) {
			// Create a type reference to "T"
			typeRef := test_util.ParseTypeAnn("T")

			// Create substitution map: T -> number
			substitutions := map[string]Type{
				"T": NewNumType(),
			}

			result := checker.substituteTypeParams(typeRef, substitutions)

			assert.Equal(t, "T", typeRef.String())
			assert.Equal(t, "number", result.String())
		})

		t.Run("preserves non-parameter type references", func(t *testing.T) {
			// Create a type reference to "Array" with type arg "T"
			typeRef := test_util.ParseTypeAnn("Array<T>")

			// Create substitution map: T -> string
			substitutions := map[string]Type{
				"T": NewStrType(),
			}

			result := checker.substituteTypeParams(typeRef, substitutions)

			assert.Equal(t, "Array<T>", typeRef.String())
			assert.Equal(t, "Array<string>", result.String())
		})

		t.Run("no substitution when type not in map", func(t *testing.T) {
			// Create a type reference to "U"
			typeRef := test_util.ParseTypeAnn("U")

			// Create substitution map: T -> number (doesn't contain U)
			substitutions := map[string]Type{
				"T": NewNumType(),
			}

			result := checker.substituteTypeParams(typeRef, substitutions)

			assert.Equal(t, "U", typeRef.String())
			assert.Equal(t, "U", result.String())
		})
	})

	t.Run("FuncType substitution", func(t *testing.T) {
		t.Run("substitutes parameter types", func(t *testing.T) {
			// Create function type: (T, U) -> T
			funcType := test_util.ParseTypeAnn("fn (x: T, y: U) -> T")

			// Create substitution map: T -> number, U -> string
			substitutions := map[string]Type{
				"T": NewNumType(),
				"U": NewStrType(),
			}

			result := checker.substituteTypeParams(funcType, substitutions)

			assert.Equal(t, "fn (x: T, y: U) -> T", funcType.String())
			assert.Equal(t, "fn (x: number, y: string) -> number", result.String())
		})

		t.Run("substitutes return type", func(t *testing.T) {
			// Create function type: () -> T
			funcType := test_util.ParseTypeAnn("fn () -> T")

			// Create substitution map: T -> boolean
			substitutions := map[string]Type{
				"T": NewBoolType(),
			}

			result := checker.substituteTypeParams(funcType, substitutions)

			assert.Equal(t, "fn () -> T", funcType.String())
			assert.Equal(t, "fn () -> boolean", result.String())
		})

		t.Run("preserves type parameters", func(t *testing.T) {
			// Create function type with type parameters
			funcType := test_util.ParseTypeAnn("fn <T>() -> number")

			substitutions := map[string]Type{
				"U": NewStrType(),
			}

			result := checker.substituteTypeParams(funcType, substitutions)

			assert.Equal(t, "fn <T>() -> number", funcType.String())
			assert.Equal(t, "fn <T>() -> number", result.String())
		})
	})

	t.Run("ObjectType substitution", func(t *testing.T) {
		t.Run("substitutes property types", func(t *testing.T) {
			// Create object type: {x: T, y: number}
			objType := test_util.ParseTypeAnn("{x: T, y: number}")

			// Create substitution map: T -> string
			substitutions := map[string]Type{
				"T": NewStrType(),
			}

			result := checker.substituteTypeParams(objType, substitutions)

			assert.Equal(t, "{x: T, y: number}", objType.String())
			assert.Equal(t, "{x: string, y: number}", result.String())
		})

		t.Run("substitutes method types", func(t *testing.T) {
			// Create object type with method: {foo(x: T): U}
			objType := test_util.ParseTypeAnn("{foo: fn (x: T) -> U}")

			// Create substitution map: T -> number, U -> boolean
			substitutions := map[string]Type{
				"T": NewNumType(),
				"U": NewBoolType(),
			}

			result := checker.substituteTypeParams(objType, substitutions)
			assert.Equal(t, "{foo: fn (x: T) -> U}", objType.String())
			assert.Equal(t, "{foo: fn (x: number) -> boolean}", result.String())
		})
	})

	t.Run("TupleType substitution", func(t *testing.T) {
		t.Run("substitutes element types", func(t *testing.T) {
			// Create tuple type: [T, number, U]
			tupleType := test_util.ParseTypeAnn("[T, number, U]")

			// Create substitution map: T -> string, U -> boolean
			substitutions := map[string]Type{
				"T": NewStrType(),
				"U": NewBoolType(),
			}

			result := checker.substituteTypeParams(tupleType, substitutions)

			assert.Equal(t, "[T, number, U]", tupleType.String())
			assert.Equal(t, "[string, number, boolean]", result.String())
		})
	})

	t.Run("UnionType substitution", func(t *testing.T) {
		t.Run("substitutes all union members", func(t *testing.T) {
			// Create union type: T | number | U
			unionType := test_util.ParseTypeAnn("T | number | U")

			// Create substitution map: T -> string, U -> boolean
			substitutions := map[string]Type{
				"T": NewStrType(),
				"U": NewBoolType(),
			}

			result := checker.substituteTypeParams(unionType, substitutions)

			assert.Equal(t, "T | number | U", unionType.String())
			assert.Equal(t, "string | number | boolean", result.String())
		})
	})

	t.Run("IntersectionType substitution", func(t *testing.T) {
		t.Run("substitutes all intersection members", func(t *testing.T) {
			// Create intersection type: T & {x: number} & U
			intersectionType := test_util.ParseTypeAnn("T & {x: number} & U")

			// Create substitution map: T -> {y: string}, U -> {z: boolean}
			substitutions := map[string]Type{
				"T": test_util.ParseTypeAnn("{y: string}"),
				"U": test_util.ParseTypeAnn("{z: boolean}"),
			}

			result := checker.substituteTypeParams(intersectionType, substitutions)
			assert.Equal(t, "T & {x: number} & U", intersectionType.String())
			assert.Equal(t, "{y: string} & {x: number} & {z: boolean}", result.String())
		})
	})

	t.Run("RestSpreadType substitution", func(t *testing.T) {
		t.Run("substitutes spread type", func(t *testing.T) {
			// Create rest/spread type: ...T
			restType := NewRestSpreadType(NewTypeRefType("T", nil))

			// Create substitution map: T -> number[]
			arrayType := test_util.ParseTypeAnn("Array<number>")
			substitutions := map[string]Type{
				"T": arrayType,
			}

			result := checker.substituteTypeParams(restType, substitutions)

			assert.Equal(t, "...T", restType.String())
			assert.Equal(t, "...Array<number>", result.String())
		})
	})

	t.Run("Primitive types unchanged", func(t *testing.T) {
		t.Run("number type", func(t *testing.T) {
			numType := NewNumType()
			substitutions := map[string]Type{
				"T": NewStrType(),
			}

			result := checker.substituteTypeParams(numType, substitutions)
			assert.Equal(t, "number", numType.String())
			assert.Equal(t, "number", result.String())
		})

		t.Run("string literal type", func(t *testing.T) {
			strLit := test_util.ParseTypeAnn(`"hello"`)
			substitutions := map[string]Type{
				"T": NewNumType(),
			}

			result := checker.substituteTypeParams(strLit, substitutions)
			assert.Equal(t, `"hello"`, strLit.String())
			assert.Equal(t, `"hello"`, result.String())
		})
	})

	t.Run("Complex nested substitution", func(t *testing.T) {
		t.Run("function with generic object parameter", func(t *testing.T) {
			// Create function type: (obj: {data: T, count: number}) -> Array<T>
			funcType := test_util.ParseTypeAnn("fn (obj: {data: T, count: number}) -> Array<T>")

			// Create substitution map: T -> string
			substitutions := map[string]Type{
				"T": NewStrType(),
			}

			result := checker.substituteTypeParams(funcType, substitutions)

			assert.Equal(t, "fn (obj: {data: T, count: number}) -> Array<T>", funcType.String())
			assert.Equal(t, "fn (obj: {data: string, count: number}) -> Array<string>", result.String())
		})
	})

	t.Run("Type parameter shadowing", func(t *testing.T) {
		t.Run("function type parameter shadows outer type parameter", func(t *testing.T) {
			// Create type: (t: T) => <T>(t: T) => T
			// This represents: type Foo1<T> = (t: T) => <T>(t: T) => T;

			// Inner function: <T>(t: T) => T
			innerFunc := test_util.ParseTypeAnn("fn <T>(t: T) -> T")

			// Outer function: (t: T) => <inner function>
			outerFuncTemplate := test_util.ParseTypeAnn("fn (t: T) -> T") // We'll replace the return type
			outerFuncTyped := outerFuncTemplate.(*FuncType)
			outerFunc := &FuncType{
				TypeParams: []*TypeParam{},
				Self:       nil,
				Params:     outerFuncTyped.Params, // Reuse the parsed parameter
				Return:     innerFunc,
				Throws:     NewNeverType(),
			}

			// Substitute T -> number
			substitutions := map[string]Type{
				"T": NewNumType(),
			}

			result := checker.substituteTypeParams(outerFunc, substitutions)

			assert.Equal(t, "fn (t: T) -> fn <T>(t: T) -> T throws never", outerFunc.String())
			assert.Equal(t, "fn (t: number) -> fn <T>(t: T) -> T throws never", result.String())
		})

		t.Run("object method with shadowed type parameter", func(t *testing.T) {
			objType := test_util.ParseTypeAnn("{foo: T, bar: fn <T>(t: T) -> T}")

			// Substitute T -> string
			substitutions := map[string]Type{
				"T": NewStrType(),
			}

			result := checker.substituteTypeParams(objType, substitutions)

			assert.Equal(t, "{foo: T, bar: fn <T>(t: T) -> T}", objType.String())
			assert.Equal(t, "{foo: string, bar: fn <T>(t: T) -> T}", result.String())
		})
	})

	t.Run("Empty substitutions", func(t *testing.T) {
		t.Run("returns original type when no substitutions", func(t *testing.T) {
			typeRef := test_util.ParseTypeAnn("T")
			substitutions := map[string]Type{}

			result := checker.substituteTypeParams(typeRef, substitutions)

			assert.Equal(t, "T", typeRef.String())
			assert.Equal(t, "T", result.String())
		})
	})
}

func TestSubstituteTypeParamsInObjElem(t *testing.T) {
	// Create shared substitutions map
	substitutions := map[string]Type{
		"T": NewNumType(),
		"U": NewStrType(),
		"V": NewBoolType(),
	}

	// Create all object elements
	tType := test_util.ParseTypeAnn("T")
	prop := &PropertyElemType{
		Name:     NewStrKey("test"),
		Optional: true,
		Readonly: true,
		Value:    tType,
	}

	methodFunc := test_util.ParseTypeAnn("fn (x: T) -> U")
	method := &MethodElemType{
		Name: NewStrKey("method"),
		Fn:   methodFunc.(*FuncType),
	}

	getterFunc := test_util.ParseTypeAnn("fn () -> T")
	getter := &GetterElemType{
		Name: NewStrKey("getter"),
		Fn:   getterFunc.(*FuncType),
	}

	setterFunc := test_util.ParseTypeAnn("fn (value: V) -> undefined")
	setter := &SetterElemType{
		Name: NewStrKey("setter"),
		Fn:   setterFunc.(*FuncType),
	}

	callableFunc := test_util.ParseTypeAnn("fn (x: T) -> U")
	callable := &CallableElemType{Fn: callableFunc.(*FuncType)}

	constructorFunc := test_util.ParseTypeAnn("fn (init: V) -> U")
	constructor := &ConstructorElemType{Fn: constructorFunc.(*FuncType)}

	restElem := &RestSpreadElemType{
		Value: tType, // Reuse the T type we parsed earlier
	}

	// Create a single object type containing all elements
	objType := NewObjectType([]ObjTypeElem{
		prop,
		method,
		getter,
		setter,
		callable,
		constructor,
		restElem,
	})

	// Substitute type parameters in the entire object
	checker := &Checker{ID: 0}
	result := checker.substituteTypeParams(objType, substitutions)

	assert.Equal(t, "{test?: T, method: fn (x: T) -> U, get getter: fn () -> T, set setter: fn (value: V) -> undefined, fn (x: T) -> U, new fn (init: V) -> U, ...T}", objType.String())
	assert.Equal(t, "{test?: number, method: fn (x: number) -> string, get getter: fn () -> number, set setter: fn (value: boolean) -> undefined, fn (x: number) -> string, new fn (init: boolean) -> string, ...number}", result.String())
}
