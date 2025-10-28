package test_util

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
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

func TestConvertPatternToTypePat(t *testing.T) {
	// Helper function to create empty span
	emptySpan := func() ast.Span {
		return ast.Span{}
	}

	t.Run("convert TuplePat", func(t *testing.T) {
		// Create an ast.TuplePat with some elements
		astTuplePat := ast.NewTuplePat([]ast.Pat{
			ast.NewIdentPat("a", nil, nil, emptySpan()),
			ast.NewIdentPat("b", nil, nil, emptySpan()),
			ast.NewWildcardPat(emptySpan()),
		}, emptySpan())

		// Convert to type system pattern
		typePat := convertPatternToTypePat(astTuplePat)

		// Check it's a TuplePat
		tuplePat, ok := typePat.(*TuplePat)
		assert.True(t, ok, "Expected convertPatternToTypePat to return a TuplePat")

		// Check the number of elements
		assert.Equal(t, 3, len(tuplePat.Elems), "Expected 3 elements in tuple pattern")

		// Check first element is an IdentPat with name "a"
		identPat1, ok := tuplePat.Elems[0].(*IdentPat)
		assert.True(t, ok, "Expected first element to be IdentPat")
		assert.Equal(t, "a", identPat1.Name)

		// Check second element is an IdentPat with name "b"
		identPat2, ok := tuplePat.Elems[1].(*IdentPat)
		assert.True(t, ok, "Expected second element to be IdentPat")
		assert.Equal(t, "b", identPat2.Name)

		// Check third element is a WildcardPat
		_, ok = tuplePat.Elems[2].(*WildcardPat)
		assert.True(t, ok, "Expected third element to be WildcardPat")
	})

	t.Run("convert nested TuplePat", func(t *testing.T) {
		// Create a nested tuple pattern: [a, [b, c]]
		nestedTuple := ast.NewTuplePat([]ast.Pat{
			ast.NewIdentPat("b", nil, nil, emptySpan()),
			ast.NewIdentPat("c", nil, nil, emptySpan()),
		}, emptySpan())

		astTuplePat := ast.NewTuplePat([]ast.Pat{
			ast.NewIdentPat("a", nil, nil, emptySpan()),
			nestedTuple,
		}, emptySpan())

		// Convert to type system pattern
		typePat := convertPatternToTypePat(astTuplePat)

		// Check it's a TuplePat
		tuplePat, ok := typePat.(*TuplePat)
		assert.True(t, ok, "Expected convertPatternToTypePat to return a TuplePat")

		// Check the number of elements
		assert.Equal(t, 2, len(tuplePat.Elems), "Expected 2 elements in tuple pattern")

		// Check first element is an IdentPat with name "a"
		identPat, ok := tuplePat.Elems[0].(*IdentPat)
		assert.True(t, ok, "Expected first element to be IdentPat")
		assert.Equal(t, "a", identPat.Name)

		// Check second element is a nested TuplePat
		nestedTuplePat, ok := tuplePat.Elems[1].(*TuplePat)
		assert.True(t, ok, "Expected second element to be TuplePat")
		assert.Equal(t, 2, len(nestedTuplePat.Elems), "Expected 2 elements in nested tuple")

		// Check nested elements
		nestedIdent1, ok := nestedTuplePat.Elems[0].(*IdentPat)
		assert.True(t, ok, "Expected first nested element to be IdentPat")
		assert.Equal(t, "b", nestedIdent1.Name)

		nestedIdent2, ok := nestedTuplePat.Elems[1].(*IdentPat)
		assert.True(t, ok, "Expected second nested element to be IdentPat")
		assert.Equal(t, "c", nestedIdent2.Name)
	})

	t.Run("convert TuplePat with RestPat", func(t *testing.T) {
		// Create a tuple pattern with rest: [a, ...rest]
		astTuplePat := ast.NewTuplePat([]ast.Pat{
			ast.NewIdentPat("a", nil, nil, emptySpan()),
			ast.NewRestPat(
				ast.NewIdentPat("rest", nil, nil, emptySpan()),
				emptySpan(),
			),
		}, emptySpan())

		// Convert to type system pattern
		typePat := convertPatternToTypePat(astTuplePat)

		// Check it's a TuplePat
		tuplePat, ok := typePat.(*TuplePat)
		assert.True(t, ok, "Expected convertPatternToTypePat to return a TuplePat")

		// Check the number of elements
		assert.Equal(t, 2, len(tuplePat.Elems), "Expected 2 elements in tuple pattern")

		// Check first element is an IdentPat with name "a"
		identPat, ok := tuplePat.Elems[0].(*IdentPat)
		assert.True(t, ok, "Expected first element to be IdentPat")
		assert.Equal(t, "a", identPat.Name)

		// Check second element is a RestPat
		restPat, ok := tuplePat.Elems[1].(*RestPat)
		assert.True(t, ok, "Expected second element to be RestPat")

		// Check the inner pattern of the RestPat
		innerIdent, ok := restPat.Pattern.(*IdentPat)
		assert.True(t, ok, "Expected rest pattern inner to be IdentPat")
		assert.Equal(t, "rest", innerIdent.Name)
	})
}

func TestConvertPatternToTypePatObjectPat(t *testing.T) {
	// Helper function to create empty span
	emptySpan := func() ast.Span {
		return ast.Span{}
	}

	t.Run("convert basic ObjectPat with key-value pairs", func(t *testing.T) {
		// Create an object pattern: {a: x, b: y}
		astObjectPat := ast.NewObjectPat([]ast.ObjPatElem{
			ast.NewObjKeyValuePat(
				ast.NewIdentifier("a", emptySpan()),
				ast.NewIdentPat("x", nil, nil, emptySpan()),
				emptySpan(),
			),
			ast.NewObjKeyValuePat(
				ast.NewIdentifier("b", emptySpan()),
				ast.NewIdentPat("y", nil, nil, emptySpan()),
				emptySpan(),
			),
		}, emptySpan())

		// Convert to type system pattern
		typePat := convertPatternToTypePat(astObjectPat)

		// Check it's an ObjectPat
		objectPat, ok := typePat.(*ObjectPat)
		assert.True(t, ok, "Expected convertPatternToTypePat to return an ObjectPat")

		// Check the number of elements
		assert.Equal(t, 2, len(objectPat.Elems), "Expected 2 elements in object pattern")

		// Check first element is a key-value pair
		keyValuePat1, ok := objectPat.Elems[0].(*ObjKeyValuePat)
		assert.True(t, ok, "Expected first element to be ObjKeyValuePat")
		assert.Equal(t, "a", keyValuePat1.Key)
		identPat1, ok := keyValuePat1.Value.(*IdentPat)
		assert.True(t, ok, "Expected value to be IdentPat")
		assert.Equal(t, "x", identPat1.Name)

		// Check second element is a key-value pair
		keyValuePat2, ok := objectPat.Elems[1].(*ObjKeyValuePat)
		assert.True(t, ok, "Expected second element to be ObjKeyValuePat")
		assert.Equal(t, "b", keyValuePat2.Key)
		identPat2, ok := keyValuePat2.Value.(*IdentPat)
		assert.True(t, ok, "Expected value to be IdentPat")
		assert.Equal(t, "y", identPat2.Name)
	})

	t.Run("convert ObjectPat with shorthand properties", func(t *testing.T) {
		// Create an object pattern: {a, b}
		astObjectPat := ast.NewObjectPat([]ast.ObjPatElem{
			ast.NewObjShorthandPat(
				ast.NewIdentifier("a", emptySpan()),
				nil,
				nil,
				emptySpan(),
			),
			ast.NewObjShorthandPat(
				ast.NewIdentifier("b", emptySpan()),
				nil,
				nil,
				emptySpan(),
			),
		}, emptySpan())

		// Convert to type system pattern
		typePat := convertPatternToTypePat(astObjectPat)

		// Check it's an ObjectPat
		objectPat, ok := typePat.(*ObjectPat)
		assert.True(t, ok, "Expected convertPatternToTypePat to return an ObjectPat")

		// Check the number of elements
		assert.Equal(t, 2, len(objectPat.Elems), "Expected 2 elements in object pattern")

		// Check first element is a shorthand
		shorthandPat1, ok := objectPat.Elems[0].(*ObjShorthandPat)
		assert.True(t, ok, "Expected first element to be ObjShorthandPat")
		assert.Equal(t, "a", shorthandPat1.Key)

		// Check second element is a shorthand
		shorthandPat2, ok := objectPat.Elems[1].(*ObjShorthandPat)
		assert.True(t, ok, "Expected second element to be ObjShorthandPat")
		assert.Equal(t, "b", shorthandPat2.Key)
	})

	t.Run("convert ObjectPat with rest pattern", func(t *testing.T) {
		// Create an object pattern: {a, ...rest}
		astObjectPat := ast.NewObjectPat([]ast.ObjPatElem{
			ast.NewObjShorthandPat(
				ast.NewIdentifier("a", emptySpan()),
				nil,
				nil,
				emptySpan(),
			),
			ast.NewObjRestPat(
				ast.NewIdentPat("rest", nil, nil, emptySpan()),
				emptySpan(),
			),
		}, emptySpan())

		// Convert to type system pattern
		typePat := convertPatternToTypePat(astObjectPat)

		// Check it's an ObjectPat
		objectPat, ok := typePat.(*ObjectPat)
		assert.True(t, ok, "Expected convertPatternToTypePat to return an ObjectPat")

		// Check the number of elements
		assert.Equal(t, 2, len(objectPat.Elems), "Expected 2 elements in object pattern")

		// Check first element is a shorthand
		shorthandPat, ok := objectPat.Elems[0].(*ObjShorthandPat)
		assert.True(t, ok, "Expected first element to be ObjShorthandPat")
		assert.Equal(t, "a", shorthandPat.Key)

		// Check second element is a rest pattern
		restPat, ok := objectPat.Elems[1].(*ObjRestPat)
		assert.True(t, ok, "Expected second element to be ObjRestPat")

		// Check the inner pattern of the rest
		innerIdent, ok := restPat.Pattern.(*IdentPat)
		assert.True(t, ok, "Expected rest pattern inner to be IdentPat")
		assert.Equal(t, "rest", innerIdent.Name)
	})

	t.Run("convert nested ObjectPat", func(t *testing.T) {
		// Create a nested object pattern: {a: {b: c}}
		nestedObject := ast.NewObjectPat([]ast.ObjPatElem{
			ast.NewObjKeyValuePat(
				ast.NewIdentifier("b", emptySpan()),
				ast.NewIdentPat("c", nil, nil, emptySpan()),
				emptySpan(),
			),
		}, emptySpan())

		astObjectPat := ast.NewObjectPat([]ast.ObjPatElem{
			ast.NewObjKeyValuePat(
				ast.NewIdentifier("a", emptySpan()),
				nestedObject,
				emptySpan(),
			),
		}, emptySpan())

		// Convert to type system pattern
		typePat := convertPatternToTypePat(astObjectPat)

		// Check it's an ObjectPat
		objectPat, ok := typePat.(*ObjectPat)
		assert.True(t, ok, "Expected convertPatternToTypePat to return an ObjectPat")

		// Check the number of elements
		assert.Equal(t, 1, len(objectPat.Elems), "Expected 1 element in object pattern")

		// Check first element is a key-value pair
		keyValuePat, ok := objectPat.Elems[0].(*ObjKeyValuePat)
		assert.True(t, ok, "Expected first element to be ObjKeyValuePat")
		assert.Equal(t, "a", keyValuePat.Key)

		// Check the nested object
		nestedObjectPat, ok := keyValuePat.Value.(*ObjectPat)
		assert.True(t, ok, "Expected value to be ObjectPat")
		assert.Equal(t, 1, len(nestedObjectPat.Elems), "Expected 1 element in nested object")

		// Check nested key-value pair
		nestedKeyValue, ok := nestedObjectPat.Elems[0].(*ObjKeyValuePat)
		assert.True(t, ok, "Expected nested element to be ObjKeyValuePat")
		assert.Equal(t, "b", nestedKeyValue.Key)

		nestedIdent, ok := nestedKeyValue.Value.(*IdentPat)
		assert.True(t, ok, "Expected nested value to be IdentPat")
		assert.Equal(t, "c", nestedIdent.Name)
	})

	t.Run("convert mixed ObjectPat", func(t *testing.T) {
		// Create a mixed object pattern: {a: x, b, ...rest}
		astObjectPat := ast.NewObjectPat([]ast.ObjPatElem{
			ast.NewObjKeyValuePat(
				ast.NewIdentifier("a", emptySpan()),
				ast.NewIdentPat("x", nil, nil, emptySpan()),
				emptySpan(),
			),
			ast.NewObjShorthandPat(
				ast.NewIdentifier("b", emptySpan()),
				nil,
				nil,
				emptySpan(),
			),
			ast.NewObjRestPat(
				ast.NewIdentPat("rest", nil, nil, emptySpan()),
				emptySpan(),
			),
		}, emptySpan())

		// Convert to type system pattern
		typePat := convertPatternToTypePat(astObjectPat)

		// Check it's an ObjectPat
		objectPat, ok := typePat.(*ObjectPat)
		assert.True(t, ok, "Expected convertPatternToTypePat to return an ObjectPat")

		// Check the number of elements
		assert.Equal(t, 3, len(objectPat.Elems), "Expected 3 elements in object pattern")

		// Check first element (key-value)
		keyValuePat, ok := objectPat.Elems[0].(*ObjKeyValuePat)
		assert.True(t, ok, "Expected first element to be ObjKeyValuePat")
		assert.Equal(t, "a", keyValuePat.Key)

		// Check second element (shorthand)
		shorthandPat, ok := objectPat.Elems[1].(*ObjShorthandPat)
		assert.True(t, ok, "Expected second element to be ObjShorthandPat")
		assert.Equal(t, "b", shorthandPat.Key)

		// Check third element (rest)
		restPat, ok := objectPat.Elems[2].(*ObjRestPat)
		assert.True(t, ok, "Expected third element to be ObjRestPat")

		innerIdent, ok := restPat.Pattern.(*IdentPat)
		assert.True(t, ok, "Expected rest pattern inner to be IdentPat")
		assert.Equal(t, "rest", innerIdent.Name)
	})
}
