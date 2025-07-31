package checker

import (
	"testing"

	. "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
)

func TestTypeParamSubstitutionVisitor(t *testing.T) {
	t.Run("Direct visitor usage", func(t *testing.T) {
		// Create a function type: (T, U) -> T
		param1 := NewFuncParam(&IdentPat{Name: "x"}, NewTypeRefType("T", nil))
		param2 := NewFuncParam(&IdentPat{Name: "y"}, NewTypeRefType("U", nil))
		funcType := &FuncType{
			Params: []*FuncParam{param1, param2},
			Return: NewTypeRefType("T", nil),
			Throws: NewNeverType(),
		}

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
		type1 := NewTypeRefType("T", nil)
		type2 := NewTupleType(NewTypeRefType("T", nil), NewNumType())

		result1 := visitor.SubstituteType(type1)
		result2 := visitor.SubstituteType(type2)

		assert.Equal(t, "string", result1.String())

		resultTuple := result2.(*TupleType)
		assert.Equal(t, "string", resultTuple.Elems[0].String())
		assert.Equal(t, "number", resultTuple.Elems[1].String())
	})
}

func TestSubstituteTypeParams(t *testing.T) {
	checker := &Checker{}

	t.Run("TypeRefType substitution", func(t *testing.T) {
		t.Run("substitutes type parameter", func(t *testing.T) {
			// Create a type reference to "T"
			typeRef := NewTypeRefType("T", nil)

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
			typeRef := NewTypeRefType("Array", nil, NewTypeRefType("T", nil))

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
			typeRef := NewTypeRefType("U", nil)

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
			param1 := NewFuncParam(&IdentPat{Name: "x"}, NewTypeRefType("T", nil))
			param2 := NewFuncParam(&IdentPat{Name: "y"}, NewTypeRefType("U", nil))
			funcType := &FuncType{
				Params: []*FuncParam{param1, param2},
				Return: NewTypeRefType("T", nil),
				Throws: NewNeverType(),
			}

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
			funcType := &FuncType{
				Params: []*FuncParam{},
				Return: NewTypeRefType("T", nil),
				Throws: NewNeverType(),
			}

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
			typeParam := NewTypeParam("T")
			funcType := &FuncType{
				TypeParams: []*TypeParam{typeParam},
				Params:     []*FuncParam{},
				Return:     NewNumType(),
				Throws:     NewNeverType(),
			}

			substitutions := map[string]Type{
				"U": NewStrType(),
			}

			result := checker.substituteTypeParams(funcType, substitutions)

			resultFunc := result.(*FuncType)
			assert.Equal(t, []*TypeParam{typeParam}, resultFunc.TypeParams)
		})
	})

	t.Run("ObjectType substitution", func(t *testing.T) {
		t.Run("substitutes property types", func(t *testing.T) {
			// Create object type: {x: T, y: number}
			prop1 := NewPropertyElemType(NewStrKey("x"), NewTypeRefType("T", nil))
			prop2 := NewPropertyElemType(NewStrKey("y"), NewNumType())
			objType := NewObjectType([]ObjTypeElem{prop1, prop2})

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
			methodFunc := &FuncType{
				Params: []*FuncParam{NewFuncParam(&IdentPat{Name: "x"}, NewTypeRefType("T", nil))},
				Return: NewTypeRefType("U", nil),
				Throws: NewNeverType(),
			}
			method := &MethodElemType{Name: NewStrKey("foo"), Fn: methodFunc}
			objType := NewObjectType([]ObjTypeElem{method})

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
			tupleType := NewTupleType(
				NewTypeRefType("T", nil),
				NewNumType(),
				NewTypeRefType("U", nil),
			)

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
			unionType := NewUnionType(
				NewTypeRefType("T", nil),
				NewNumType(),
				NewTypeRefType("U", nil),
			)

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
			objType := NewObjectType([]ObjTypeElem{
				NewPropertyElemType(NewStrKey("x"), NewNumType()),
			})
			intersectionType := NewIntersectionType(
				NewTypeRefType("T", nil),
				objType,
				NewTypeRefType("U", nil),
			)

			// Create substitution map: T -> {y: string}, U -> {z: boolean}
			substitutions := map[string]Type{
				"T": NewObjectType([]ObjTypeElem{
					NewPropertyElemType(NewStrKey("y"), NewStrType()),
				}),
				"U": NewObjectType([]ObjTypeElem{
					NewPropertyElemType(NewStrKey("z"), NewBoolType()),
				}),
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
			substitutions := map[string]Type{
				"T": NewTypeRefType("Array", nil, NewNumType()),
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
			strLit := NewLitType(&StrLit{Value: "hello"})
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
			objParam := NewObjectType([]ObjTypeElem{
				NewPropertyElemType(NewStrKey("data"), NewTypeRefType("T", nil)),
				NewPropertyElemType(NewStrKey("count"), NewNumType()),
			})
			funcType := &FuncType{
				TypeParams: []*TypeParam{},
				Params: []*FuncParam{
					NewFuncParam(&IdentPat{Name: "obj"}, objParam),
				},
				Return: NewTypeRefType("Array", nil, NewTypeRefType("T", nil)),
				Throws: NewNeverType(),
			}

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
			innerParam := NewFuncParam(&IdentPat{Name: "t"}, NewTypeRefType("T", nil))
			innerFunc := &FuncType{
				TypeParams: []*TypeParam{NewTypeParam("T")}, // This T should shadow outer T
				Self:       nil,
				Params:     []*FuncParam{innerParam},
				Return:     NewTypeRefType("T", nil), // This T refers to inner T
				Throws:     NewNeverType(),
			}

			// Outer function: (t: T) => <inner function>
			outerParam := NewFuncParam(&IdentPat{Name: "t"}, NewTypeRefType("T", nil))
			outerFunc := &FuncType{
				TypeParams: []*TypeParam{},
				Self:       nil,
				Params:     []*FuncParam{outerParam},
				Return:     innerFunc,
				Throws:     NewNeverType(),
			}

			// Substitute T -> number
			substitutions := map[string]Type{
				"T": NewNumType(),
			}

			result := checker.substituteTypeParams(outerFunc, substitutions)

			assert.Equal(t, "fn (t: T) -> fn <T>(t: T) -> T", outerFunc.String())
			assert.Equal(t, "fn (t: number) -> fn <T>(t: T) -> T", result.String())
		})

		t.Run("object method with shadowed type parameter", func(t *testing.T) {
			// Create type: {foo: T; bar: <T>(t: T) => T}
			// This represents: type Foo2<T> = {foo: T; bar: <T>(t: T) => T}

			// Method: <T>(t: T) => T
			methodParam := NewFuncParam(&IdentPat{Name: "t"}, NewTypeRefType("T", nil))
			methodFunc := &FuncType{
				TypeParams: []*TypeParam{NewTypeParam("T")}, // This T should shadow outer T
				Self:       nil,
				Params:     []*FuncParam{methodParam},
				Return:     NewTypeRefType("T", nil), // This T refers to method's T
				Throws:     NewNeverType(),
			}

			// Object type: {foo: T; bar: method}
			objType := NewObjectType([]ObjTypeElem{
				NewPropertyElemType(NewStrKey("foo"), NewTypeRefType("T", nil)), // Outer T
				&MethodElemType{Name: NewStrKey("bar"), Fn: methodFunc},
			})

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
			typeRef := NewTypeRefType("T", nil)
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
	prop := &PropertyElemType{
		Name:     NewStrKey("test"),
		Optional: true,
		Readonly: true,
		Value:    NewTypeRefType("T", nil),
	}

	methodFunc := &FuncType{
		Params: []*FuncParam{NewFuncParam(&IdentPat{Name: "x"}, NewTypeRefType("T", nil))},
		Return: NewTypeRefType("U", nil),
		Throws: NewNeverType(),
	}
	method := &MethodElemType{
		Name: NewStrKey("method"),
		Fn:   methodFunc,
	}

	getterFunc := &FuncType{
		Params: []*FuncParam{},
		Return: NewTypeRefType("T", nil),
		Throws: NewNeverType(),
	}
	getter := &GetterElemType{
		Name: NewStrKey("getter"),
		Fn:   getterFunc,
	}

	setterFunc := &FuncType{
		Params: []*FuncParam{NewFuncParam(&IdentPat{Name: "value"}, NewTypeRefType("V", nil))},
		Return: NewLitType(&UndefinedLit{}),
		Throws: NewNeverType(),
	}
	setter := &SetterElemType{
		Name: NewStrKey("setter"),
		Fn:   setterFunc,
	}

	callableFunc := &FuncType{
		Params: []*FuncParam{NewFuncParam(&IdentPat{Name: "x"}, NewTypeRefType("T", nil))},
		Return: NewTypeRefType("U", nil),
		Throws: NewNeverType(),
	}
	callable := &CallableElemType{Fn: callableFunc}

	constructorFunc := &FuncType{
		Params: []*FuncParam{NewFuncParam(&IdentPat{Name: "init"}, NewTypeRefType("V", nil))},
		Return: NewTypeRefType("U", nil),
		Throws: NewNeverType(),
	}
	constructor := &ConstructorElemType{Fn: constructorFunc}

	restElem := &RestSpreadElemType{
		Value: NewTypeRefType("T", nil),
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
	checker := &Checker{}
	result := checker.substituteTypeParams(objType, substitutions)

	assert.Equal(t, "{test?: T, method: fn (x: T) -> U, get getter: fn () -> T, set setter: fn (value: V) -> undefined, fn (x: T) -> U, new fn (init: V) -> U, ...T}", objType.String())
	assert.Equal(t, "{test?: number, method: fn (x: number) -> string, get getter: fn () -> number, set setter: fn (value: boolean) -> undefined, fn (x: number) -> string, new fn (init: boolean) -> string, ...number}", result.String())
}
