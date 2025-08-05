package test_util

import (
	"testing"

	. "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
)

func TestConvertTypeAnnToType(t *testing.T) {
	t.Run("convert basic primitive types", func(t *testing.T) {
		// Test number type
		numberType := ParseTypeAnn("number")
		_, ok := numberType.(*PrimType)
		assert.True(t, ok)
		assert.Equal(t, "number", numberType.String())

		// Test string type
		stringType := ParseTypeAnn("string")
		_, ok = stringType.(*PrimType)
		assert.True(t, ok)
		assert.Equal(t, "string", stringType.String())

		// Test boolean type
		boolType := ParseTypeAnn("boolean")
		_, ok = boolType.(*PrimType)
		assert.True(t, ok)
		assert.Equal(t, "boolean", boolType.String())

		// Test any type
		anyType := ParseTypeAnn("any")
		_, ok = anyType.(*AnyType)
		assert.True(t, ok)
		assert.Equal(t, "any", anyType.String())

		// Test unknown type
		unknownType := ParseTypeAnn("unknown")
		_, ok = unknownType.(*UnknownType)
		assert.True(t, ok)
		assert.Equal(t, "unknown", unknownType.String())

		// Test never type
		neverType := ParseTypeAnn("never")
		_, ok = neverType.(*NeverType)
		assert.True(t, ok)
		assert.Equal(t, "never", neverType.String())
	})

	t.Run("convert literal types", func(t *testing.T) {
		// Test string literal
		strLitType := ParseTypeAnn(`"hello"`)
		_, ok := strLitType.(*LitType)
		assert.True(t, ok)
		assert.Equal(t, `"hello"`, strLitType.String())

		// Test number literal
		numLitType := ParseTypeAnn("42")
		_, ok = numLitType.(*LitType)
		assert.True(t, ok)
		assert.Equal(t, "42", numLitType.String())

		// Test boolean literal
		boolLitType := ParseTypeAnn("true")
		_, ok = boolLitType.(*LitType)
		assert.True(t, ok)
		assert.Equal(t, "true", boolLitType.String())
	})

	t.Run("convert tuple type", func(t *testing.T) {
		// Create [string, number]
		tupleType := ParseTypeAnn("[string, number]")
		_, ok := tupleType.(*TupleType)
		assert.True(t, ok)
		assert.Equal(t, "[string, number]", tupleType.String())
	})

	t.Run("convert union type", func(t *testing.T) {
		// Create string | number
		unionType := ParseTypeAnn("string | number")
		_, ok := unionType.(*UnionType)
		assert.True(t, ok)
		assert.Equal(t, "string | number", unionType.String())
	})

	t.Run("convert type reference", func(t *testing.T) {
		// Create Array<string>
		typeRefType := ParseTypeAnn("Array<string>")
		_, ok := typeRefType.(*TypeRefType)
		assert.True(t, ok)
		assert.Equal(t, "Array<string>", typeRefType.String())
	})

	t.Run("convert function types with rest params", func(t *testing.T) {
		// Test function with rest params: fn(...args: Array<string>) -> number
		funcType := ParseTypeAnn("fn(...args: Array<string>) -> number")

		fnType, ok := funcType.(*FuncType)
		assert.True(t, ok)
		assert.Equal(t, 1, len(fnType.Params))

		// Check that the parameter is a rest parameter
		restParam := fnType.Params[0]
		_, isRestPat := restParam.Pattern.(*RestPat)
		assert.True(t, isRestPat, "Expected rest parameter to have RestPat pattern")

		// Check that the parameter type is Array<string>
		arrayType, isTypeRef := restParam.Type.(*TypeRefType)
		assert.True(t, isTypeRef, "Expected rest parameter type to be TypeRefType")
		assert.Equal(t, "Array", arrayType.Name)
		assert.Equal(t, 1, len(arrayType.TypeArgs))

		elementType, isPrimType := arrayType.TypeArgs[0].(*PrimType)
		assert.True(t, isPrimType, "Expected array element type to be PrimType")
		assert.Equal(t, StrPrim, elementType.Prim)

		// Check return type
		returnType, isReturnPrimType := fnType.Return.(*PrimType)
		assert.True(t, isReturnPrimType, "Expected return type to be PrimType")
		assert.Equal(t, NumPrim, returnType.Prim)
	})

	t.Run("convert function types with mixed params and rest", func(t *testing.T) {
		// Test function with regular and rest params: fn(x: number, y: string, ...rest: Array<boolean>) -> void
		funcType := ParseTypeAnn("fn(x: number, y: string, ...rest: Array<boolean>) -> undefined")

		fnType, ok := funcType.(*FuncType)
		assert.True(t, ok)
		assert.Equal(t, 3, len(fnType.Params))

		// Check first parameter (regular param)
		param1 := fnType.Params[0]
		_, isIdentPat := param1.Pattern.(*IdentPat)
		assert.True(t, isIdentPat, "Expected first parameter to have IdentPat pattern")
		assert.False(t, param1.Optional, "Expected first parameter to be required")

		numType, isNumType := param1.Type.(*PrimType)
		assert.True(t, isNumType, "Expected first parameter type to be PrimType")
		assert.Equal(t, NumPrim, numType.Prim)

		// Check second parameter (regular param)
		param2 := fnType.Params[1]
		_, isIdentPat2 := param2.Pattern.(*IdentPat)
		assert.True(t, isIdentPat2, "Expected second parameter to have IdentPat pattern")
		assert.False(t, param2.Optional, "Expected second parameter to be required")

		strType, isStrType := param2.Type.(*PrimType)
		assert.True(t, isStrType, "Expected second parameter type to be PrimType")
		assert.Equal(t, StrPrim, strType.Prim)

		// Check third parameter (rest param)
		restParam := fnType.Params[2]
		_, isRestPat := restParam.Pattern.(*RestPat)
		assert.True(t, isRestPat, "Expected third parameter to have RestPat pattern")

		arrayType, isTypeRef := restParam.Type.(*TypeRefType)
		assert.True(t, isTypeRef, "Expected rest parameter type to be TypeRefType")
		assert.Equal(t, "Array", arrayType.Name)
		assert.Equal(t, 1, len(arrayType.TypeArgs))

		elementType, isBoolType := arrayType.TypeArgs[0].(*PrimType)
		assert.True(t, isBoolType, "Expected array element type to be PrimType")
		assert.Equal(t, BoolPrim, elementType.Prim)

		// Check return type (undefined)
		returnType, isLitType := fnType.Return.(*LitType)
		assert.True(t, isLitType, "Expected return type to be LitType")
		_, isUndefinedLit := returnType.Lit.(*UndefinedLit)
		assert.True(t, isUndefinedLit, "Expected return type to be undefined literal")
	})

	t.Run("convert function types with rest params and type args", func(t *testing.T) {
		// Test function with rest params: fn<T>(...args: Array<T>) -> T
		funcType := ParseTypeAnn("fn<T>(...args: Array<T>) -> T")

		fnType, ok := funcType.(*FuncType)
		assert.True(t, ok)
		assert.Equal(t, 1, len(fnType.Params))
		assert.Equal(t, 1, len(fnType.TypeParams))

		// Check type parameter
		typeParam := fnType.TypeParams[0]
		assert.Equal(t, "T", typeParam.Name)

		// Check that the parameter is a rest parameter
		restParam := fnType.Params[0]
		_, isRestPat := restParam.Pattern.(*RestPat)
		assert.True(t, isRestPat, "Expected rest parameter to have RestPat pattern")

		// Check that the parameter type is Array<T>
		arrayType, isTypeRef := restParam.Type.(*TypeRefType)
		assert.True(t, isTypeRef, "Expected rest parameter type to be TypeRefType")
		assert.Equal(t, "Array", arrayType.Name)
		assert.Equal(t, 1, len(arrayType.TypeArgs))

		// The element type should be a TypeRefType referring to T
		elementType, isElementTypeRef := arrayType.TypeArgs[0].(*TypeRefType)
		assert.True(t, isElementTypeRef, "Expected array element type to be TypeRefType")
		assert.Equal(t, "T", elementType.Name)

		// Check return type (should also be T)
		returnType, isReturnTypeRef := fnType.Return.(*TypeRefType)
		assert.True(t, isReturnTypeRef, "Expected return type to be TypeRefType")
		assert.Equal(t, "T", returnType.Name)
	})

	t.Run("convert function types with optional params", func(t *testing.T) {
		// Test function with optional parameters: fn(x: number, y?: string, z?: boolean) -> void
		funcType := ParseTypeAnn("fn(x: number, y?: string, z?: boolean) -> undefined")

		fnType, ok := funcType.(*FuncType)
		assert.True(t, ok)
		assert.Equal(t, 3, len(fnType.Params))

		// Check first parameter (required)
		param1 := fnType.Params[0]
		_, isIdentPat := param1.Pattern.(*IdentPat)
		assert.True(t, isIdentPat, "Expected first parameter to have IdentPat pattern")
		assert.False(t, param1.Optional, "Expected first parameter to be required")

		numType, isNumType := param1.Type.(*PrimType)
		assert.True(t, isNumType, "Expected first parameter type to be PrimType")
		assert.Equal(t, NumPrim, numType.Prim)

		// Check second parameter (optional)
		param2 := fnType.Params[1]
		_, isIdentPat2 := param2.Pattern.(*IdentPat)
		assert.True(t, isIdentPat2, "Expected second parameter to have IdentPat pattern")
		assert.True(t, param2.Optional, "Expected second parameter to be optional")

		strType, isStrType := param2.Type.(*PrimType)
		assert.True(t, isStrType, "Expected second parameter type to be PrimType")
		assert.Equal(t, StrPrim, strType.Prim)

		// Check third parameter (optional)
		param3 := fnType.Params[2]
		_, isIdentPat3 := param3.Pattern.(*IdentPat)
		assert.True(t, isIdentPat3, "Expected third parameter to have IdentPat pattern")
		assert.True(t, param3.Optional, "Expected third parameter to be optional")

		boolType, isBoolType := param3.Type.(*PrimType)
		assert.True(t, isBoolType, "Expected third parameter type to be PrimType")
		assert.Equal(t, BoolPrim, boolType.Prim)

		// Check return type (undefined)
		returnType, isLitType := fnType.Return.(*LitType)
		assert.True(t, isLitType, "Expected return type to be LitType")
		_, isUndefinedLit := returnType.Lit.(*UndefinedLit)
		assert.True(t, isUndefinedLit, "Expected return type to be undefined literal")
	})

	t.Run("convert function types with mixed required and optional params", func(t *testing.T) {
		// Test function with mixed parameters: fn(x: number, y?: string, z: boolean) -> string
		funcType := ParseTypeAnn("fn(x: number, y?: string, z: boolean) -> string")

		fnType, ok := funcType.(*FuncType)
		assert.True(t, ok)
		assert.Equal(t, 3, len(fnType.Params))

		// Check first parameter (required)
		param1 := fnType.Params[0]
		assert.False(t, param1.Optional, "Expected first parameter to be required")

		// Check second parameter (optional)
		param2 := fnType.Params[1]
		assert.True(t, param2.Optional, "Expected second parameter to be optional")

		// Check third parameter (required)
		param3 := fnType.Params[2]
		assert.False(t, param3.Optional, "Expected third parameter to be required")

		boolType, isBoolType := param3.Type.(*PrimType)
		assert.True(t, isBoolType, "Expected third parameter type to be PrimType")
		assert.Equal(t, BoolPrim, boolType.Prim)

		// Check return type (string)
		returnType, isReturnPrimType := fnType.Return.(*PrimType)
		assert.True(t, isReturnPrimType, "Expected return type to be PrimType")
		assert.Equal(t, StrPrim, returnType.Prim)
	})
}
