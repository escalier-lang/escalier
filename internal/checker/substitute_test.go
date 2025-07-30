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

		resultFunc := result.(*FuncType)
		assert.Equal(t, NewNumType(), resultFunc.Params[0].Type)
		assert.Equal(t, NewStrType(), resultFunc.Params[1].Type)
		assert.Equal(t, NewNumType(), resultFunc.Return)
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

		assert.Equal(t, NewStrType(), result1)

		resultTuple := result2.(*TupleType)
		assert.Equal(t, NewStrType(), resultTuple.Elems[0])
		assert.Equal(t, NewNumType(), resultTuple.Elems[1])
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

			assert.Equal(t, NewNumType(), result)
		})

		t.Run("preserves non-parameter type references", func(t *testing.T) {
			// Create a type reference to "Array" with type arg "T"
			typeRef := NewTypeRefType("Array", nil, NewTypeRefType("T", nil))

			// Create substitution map: T -> string
			substitutions := map[string]Type{
				"T": NewStrType(),
			}

			result := checker.substituteTypeParams(typeRef, substitutions)

			expected := NewTypeRefType("Array", nil, NewStrType())
			assert.Equal(t, expected, result)
		})

		t.Run("no substitution when type not in map", func(t *testing.T) {
			// Create a type reference to "U"
			typeRef := NewTypeRefType("U", nil)

			// Create substitution map: T -> number (doesn't contain U)
			substitutions := map[string]Type{
				"T": NewNumType(),
			}

			result := checker.substituteTypeParams(typeRef, substitutions)

			assert.Equal(t, typeRef, result)
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

			resultFunc := result.(*FuncType)
			assert.Equal(t, NewNumType(), resultFunc.Params[0].Type)
			assert.Equal(t, NewStrType(), resultFunc.Params[1].Type)
			assert.Equal(t, NewNumType(), resultFunc.Return)
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

			resultFunc := result.(*FuncType)
			assert.Equal(t, NewBoolType(), resultFunc.Return)
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

			resultObj := result.(*ObjectType)
			assert.Len(t, resultObj.Elems, 2)

			prop1Result := resultObj.Elems[0].(*PropertyElemType)
			assert.Equal(t, NewStrType(), prop1Result.Value)

			prop2Result := resultObj.Elems[1].(*PropertyElemType)
			assert.Equal(t, NewNumType(), prop2Result.Value)
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

			resultObj := result.(*ObjectType)
			methodResult := resultObj.Elems[0].(*MethodElemType)
			assert.Equal(t, NewNumType(), methodResult.Fn.Params[0].Type)
			assert.Equal(t, NewBoolType(), methodResult.Fn.Return)
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

			resultTuple := result.(*TupleType)
			assert.Len(t, resultTuple.Elems, 3)
			assert.Equal(t, NewStrType(), resultTuple.Elems[0])
			assert.Equal(t, NewNumType(), resultTuple.Elems[1])
			assert.Equal(t, NewBoolType(), resultTuple.Elems[2])
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

			// NewUnionType returns Type, so we need to handle that
			if unionResult, ok := result.(*UnionType); ok {
				assert.Len(t, unionResult.Types, 3)
				assert.Equal(t, NewStrType(), unionResult.Types[0])
				assert.Equal(t, NewNumType(), unionResult.Types[1])
				assert.Equal(t, NewBoolType(), unionResult.Types[2])
			} else {
				t.Fatalf("Expected UnionType, got %T", result)
			}
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

			intersectionResult := result.(*IntersectionType)
			assert.Len(t, intersectionResult.Types, 3)

			// Check first type (substituted T)
			firstObj := intersectionResult.Types[0].(*ObjectType)
			firstProp := firstObj.Elems[0].(*PropertyElemType)
			assert.Equal(t, NewStrKey("y"), firstProp.Name)
			assert.Equal(t, NewStrType(), firstProp.Value)

			// Check second type (unchanged object)
			secondObj := intersectionResult.Types[1].(*ObjectType)
			secondProp := secondObj.Elems[0].(*PropertyElemType)
			assert.Equal(t, NewStrKey("x"), secondProp.Name)
			assert.Equal(t, NewNumType(), secondProp.Value)

			// Check third type (substituted U)
			thirdObj := intersectionResult.Types[2].(*ObjectType)
			thirdProp := thirdObj.Elems[0].(*PropertyElemType)
			assert.Equal(t, NewStrKey("z"), thirdProp.Name)
			assert.Equal(t, NewBoolType(), thirdProp.Value)
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

			restResult := result.(*RestSpreadType)
			arrayType := restResult.Type.(*TypeRefType)
			assert.Equal(t, "Array", arrayType.Name)
			assert.Equal(t, NewNumType(), arrayType.TypeArgs[0])
		})
	})

	t.Run("Primitive types unchanged", func(t *testing.T) {
		t.Run("number type", func(t *testing.T) {
			numType := NewNumType()
			substitutions := map[string]Type{
				"T": NewStrType(),
			}

			result := checker.substituteTypeParams(numType, substitutions)
			assert.Equal(t, numType, result)
		})

		t.Run("string literal type", func(t *testing.T) {
			strLit := NewLitType(&StrLit{Value: "hello"})
			substitutions := map[string]Type{
				"T": NewNumType(),
			}

			result := checker.substituteTypeParams(strLit, substitutions)
			assert.Equal(t, strLit, result)
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

			resultFunc := result.(*FuncType)

			// Check parameter object type
			paramObj := resultFunc.Params[0].Type.(*ObjectType)
			dataProp := paramObj.Elems[0].(*PropertyElemType)
			assert.Equal(t, NewStrType(), dataProp.Value)

			countProp := paramObj.Elems[1].(*PropertyElemType)
			assert.Equal(t, NewNumType(), countProp.Value)

			// Check return type
			returnType := resultFunc.Return.(*TypeRefType)
			assert.Equal(t, "Array", returnType.Name)
			assert.Equal(t, NewStrType(), returnType.TypeArgs[0])
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
			resultFunc := result.(*FuncType)

			// Outer parameter should be substituted: (t: number)
			assert.Equal(t, NewNumType(), resultFunc.Params[0].Type)

			// Inner function should preserve its own T
			innerResult := resultFunc.Return.(*FuncType)
			assert.Equal(t, []*TypeParam{NewTypeParam("T")}, innerResult.TypeParams)

			// Inner parameter and return should still be T (not substituted)
			assert.Equal(t, NewTypeRefType("T", nil), innerResult.Params[0].Type)
			assert.Equal(t, NewTypeRefType("T", nil), innerResult.Return)
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
			resultObj := result.(*ObjectType)

			// Property should be substituted: foo: string
			fooProp := resultObj.Elems[0].(*PropertyElemType)
			assert.Equal(t, NewStrType(), fooProp.Value)

			// Method should preserve its own T
			barMethod := resultObj.Elems[1].(*MethodElemType)
			assert.Equal(t, []*TypeParam{NewTypeParam("T")}, barMethod.Fn.TypeParams)

			// Method parameter and return should still be T (not substituted)
			assert.Equal(t, NewTypeRefType("T", nil), barMethod.Fn.Params[0].Type)
			assert.Equal(t, NewTypeRefType("T", nil), barMethod.Fn.Return)
		})
	})

	t.Run("Empty substitutions", func(t *testing.T) {
		t.Run("returns original type when no substitutions", func(t *testing.T) {
			typeRef := NewTypeRefType("T", nil)
			substitutions := map[string]Type{}

			result := checker.substituteTypeParams(typeRef, substitutions)
			assert.Equal(t, typeRef, result)
		})
	})
}

func TestSubstituteTypeParamsInObjElem(t *testing.T) {
	t.Run("PropertyElemType", func(t *testing.T) {
		prop := &PropertyElemType{
			Name:     NewStrKey("test"),
			Optional: true,
			Readonly: true,
			Value:    NewTypeRefType("T", nil),
		}

		substitutions := map[string]Type{
			"T": NewNumType(),
		}

		visitor := NewTypeParamSubstitutionVisitor(substitutions)
		result := visitor.substituteTypeParamsInObjElem(prop)

		resultProp := result.(*PropertyElemType)
		assert.Equal(t, NewStrKey("test"), resultProp.Name)
		assert.True(t, resultProp.Optional)
		assert.True(t, resultProp.Readonly)
		assert.Equal(t, NewNumType(), resultProp.Value)
	})

	t.Run("MethodElemType", func(t *testing.T) {
		methodFunc := &FuncType{
			Params: []*FuncParam{NewFuncParam(&IdentPat{Name: "x"}, NewTypeRefType("T", nil))},
			Return: NewTypeRefType("U", nil),
			Throws: NewNeverType(),
		}
		method := &MethodElemType{
			Name: NewStrKey("method"),
			Fn:   methodFunc,
		}

		substitutions := map[string]Type{
			"T": NewStrType(),
			"U": NewBoolType(),
		}

		visitor := NewTypeParamSubstitutionVisitor(substitutions)
		result := visitor.substituteTypeParamsInObjElem(method)

		resultMethod := result.(*MethodElemType)
		assert.Equal(t, NewStrKey("method"), resultMethod.Name)
		assert.Equal(t, NewStrType(), resultMethod.Fn.Params[0].Type)
		assert.Equal(t, NewBoolType(), resultMethod.Fn.Return)
	})

	t.Run("GetterElemType", func(t *testing.T) {
		getterFunc := &FuncType{
			Params: []*FuncParam{},
			Return: NewTypeRefType("T", nil),
			Throws: NewNeverType(),
		}
		getter := &GetterElemType{
			Name: NewStrKey("getter"),
			Fn:   getterFunc,
		}

		substitutions := map[string]Type{
			"T": NewNumType(),
		}

		visitor := NewTypeParamSubstitutionVisitor(substitutions)
		result := visitor.substituteTypeParamsInObjElem(getter)

		resultGetter := result.(*GetterElemType)
		assert.Equal(t, NewStrKey("getter"), resultGetter.Name)
		assert.Equal(t, NewNumType(), resultGetter.Fn.Return)
	})

	t.Run("SetterElemType", func(t *testing.T) {
		setterFunc := &FuncType{
			Params: []*FuncParam{NewFuncParam(&IdentPat{Name: "value"}, NewTypeRefType("T", nil))},
			Return: NewLitType(&UndefinedLit{}),
			Throws: NewNeverType(),
		}
		setter := &SetterElemType{
			Name: NewStrKey("setter"),
			Fn:   setterFunc,
		}

		substitutions := map[string]Type{
			"T": NewBoolType(),
		}

		visitor := NewTypeParamSubstitutionVisitor(substitutions)
		result := visitor.substituteTypeParamsInObjElem(setter)

		resultSetter := result.(*SetterElemType)
		assert.Equal(t, NewStrKey("setter"), resultSetter.Name)
		assert.Equal(t, NewBoolType(), resultSetter.Fn.Params[0].Type)
	})

	t.Run("CallableElemType", func(t *testing.T) {
		callableFunc := &FuncType{
			Params: []*FuncParam{NewFuncParam(&IdentPat{Name: "x"}, NewTypeRefType("T", nil))},
			Return: NewTypeRefType("U", nil),
			Throws: NewNeverType(),
		}
		callable := &CallableElemType{Fn: callableFunc}

		substitutions := map[string]Type{
			"T": NewStrType(),
			"U": NewNumType(),
		}

		visitor := NewTypeParamSubstitutionVisitor(substitutions)
		result := visitor.substituteTypeParamsInObjElem(callable)

		resultCallable := result.(*CallableElemType)
		assert.Equal(t, NewStrType(), resultCallable.Fn.Params[0].Type)
		assert.Equal(t, NewNumType(), resultCallable.Fn.Return)
	})

	t.Run("ConstructorElemType", func(t *testing.T) {
		constructorFunc := &FuncType{
			Params: []*FuncParam{NewFuncParam(&IdentPat{Name: "init"}, NewTypeRefType("T", nil))},
			Return: NewTypeRefType("U", nil),
			Throws: NewNeverType(),
		}
		constructor := &ConstructorElemType{Fn: constructorFunc}

		substitutions := map[string]Type{
			"T": NewBoolType(),
			"U": NewStrType(),
		}

		visitor := NewTypeParamSubstitutionVisitor(substitutions)
		result := visitor.substituteTypeParamsInObjElem(constructor)

		resultConstructor := result.(*ConstructorElemType)
		assert.Equal(t, NewBoolType(), resultConstructor.Fn.Params[0].Type)
		assert.Equal(t, NewStrType(), resultConstructor.Fn.Return)
	})

	t.Run("RestSpreadElemType", func(t *testing.T) {
		restElem := &RestSpreadElemType{
			Value: NewTypeRefType("T", nil),
		}

		substitutions := map[string]Type{
			"T": NewObjectType([]ObjTypeElem{
				NewPropertyElemType(NewStrKey("x"), NewNumType()),
			}),
		}

		visitor := NewTypeParamSubstitutionVisitor(substitutions)
		result := visitor.substituteTypeParamsInObjElem(restElem)

		resultRest := result.(*RestSpreadElemType)
		objType := resultRest.Value.(*ObjectType)
		prop := objType.Elems[0].(*PropertyElemType)
		assert.Equal(t, NewStrKey("x"), prop.Name)
		assert.Equal(t, NewNumType(), prop.Value)
	})
}
