package checker

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/test_util"
	. "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
)

func TestUnifyStrLitWithRegexLit(t *testing.T) {
	checker := &Checker{}
	ctx := Context{}

	t.Run("string matches regex pattern", func(t *testing.T) {
		strType := test_util.ParseTypeAnn(`"hello"`)
		result, _ := NewRegexType("/^hello$/")
		regexType := result.(*RegexType)

		errors := checker.unify(ctx, strType, regexType)
		assert.Empty(t, errors, "Expected no errors when string matches regex pattern")
	})

	t.Run("string does not match regex pattern", func(t *testing.T) {
		strType := test_util.ParseTypeAnn(`"world"`)
		result, _ := NewRegexType("/^hello$/")
		regexType := result.(*RegexType)

		errors := checker.unify(ctx, strType, regexType)
		assert.NotEmpty(t, errors, "Expected error when string does not match regex pattern")
		assert.IsType(t, &CannotUnifyTypesError{}, errors[0])
	})

	t.Run("string matches complex regex pattern", func(t *testing.T) {
		strType := test_util.ParseTypeAnn(`"123-456-7890"`)
		result, _ := NewRegexType(`/^\d{3}-\d{3}-\d{4}$/`)
		regexType := result.(*RegexType)

		errors := checker.unify(ctx, strType, regexType)
		assert.Empty(t, errors, "Expected no errors when string matches phone number pattern")
	})

	t.Run("case insensitive matching", func(t *testing.T) {
		strType := test_util.ParseTypeAnn(`"HELLO"`)
		result, _ := NewRegexType("/^hello$/i")
		regexType := result.(*RegexType)

		errors := checker.unify(ctx, strType, regexType)
		assert.Empty(t, errors, "Expected no errors when string matches regex with case insensitive flag")
	})

	t.Run("invalid regex format", func(t *testing.T) {
		result, err := NewRegexType("/invalid")

		assert.NotNil(t, err, "Expected error when regex format is invalid")
		assert.IsType(t, NewNeverType(), result)
	})

	t.Run("regex with global flag", func(t *testing.T) {
		strType := test_util.ParseTypeAnn(`"hello"`)
		result, _ := NewRegexType("/hello/g")
		regexType := result.(*RegexType)

		errors := checker.unify(ctx, strType, regexType)
		assert.Empty(t, errors, "Expected no errors when string matches regex with global flag")
	})

	t.Run("existing functionality still works - string literals", func(t *testing.T) {
		strType1 := test_util.ParseTypeAnn(`"hello"`)
		strType2 := test_util.ParseTypeAnn(`"hello"`)

		errors := checker.unify(ctx, strType1, strType2)
		assert.Empty(t, errors, "Expected no errors when unifying identical string literals")
	})
}

func TestUnifyWithUnionTypes(t *testing.T) {
	checker := &Checker{}
	ctx := Context{}

	t.Run("literal type unifies with union containing compatible type", func(t *testing.T) {
		numType := test_util.ParseTypeAnn("5")
		unionType := test_util.ParseTypeAnn("string | number")

		errors := checker.unify(ctx, numType, unionType)
		assert.Empty(t, errors, "Expected no errors when literal unifies with union containing compatible type")
	})

	t.Run("literal type fails to unify with union containing no compatible types", func(t *testing.T) {
		boolType := test_util.ParseTypeAnn("true")
		unionType := test_util.ParseTypeAnn("string | number")

		errors := checker.unify(ctx, boolType, unionType)
		assert.NotEmpty(t, errors, "Expected error when literal does not unify with any type in union")
		assert.IsType(t, &CannotUnifyTypesError{}, errors[0])
	})

	t.Run("primitive type unifies with union containing same type", func(t *testing.T) {
		stringType := test_util.ParseTypeAnn("string")
		unionType := test_util.ParseTypeAnn("string | number | boolean")

		errors := checker.unify(ctx, stringType, unionType)
		assert.Empty(t, errors, "Expected no errors when primitive type unifies with union containing same type")
	})

	t.Run("primitive type fails to unify with union not containing that type", func(t *testing.T) {
		// Create a bigint primitive type
		bigintType := &PrimType{Prim: BigIntPrim}

		// Create a union type: string | number | boolean (no bigint)
		unionType := test_util.ParseTypeAnn("string | number | boolean")

		// Test unification - should fail because bigint is not in the union
		errors := checker.unify(ctx, bigintType, unionType)
		assert.NotEmpty(t, errors, "Expected error when primitive type is not in union")
		assert.IsType(t, &CannotUnifyTypesError{}, errors[0])
	})

	t.Run("union type unifies with broader union type", func(t *testing.T) {
		// Create a smaller union type: string | number
		smallUnion := test_util.ParseTypeAnn("string | number")

		// Create a larger union type: string | number | boolean
		largeUnion := test_util.ParseTypeAnn("string | number | boolean")

		// Test unification - should succeed because all types in smallUnion are in largeUnion
		errors := checker.unify(ctx, smallUnion, largeUnion)
		assert.Empty(t, errors, "Expected no errors when smaller union unifies with larger union")
	})

	t.Run("union type fails to unify with incompatible union type", func(t *testing.T) {
		// Create a union type: string | number
		union1 := test_util.ParseTypeAnn("string | number")

		// Create another union type: boolean | bigint
		bigintType := &PrimType{Prim: BigIntPrim}
		booleanType := NewBoolType()
		union2 := NewUnionType(booleanType, bigintType)

		// Test unification - should fail because no types overlap
		errors := checker.unify(ctx, union1, union2)
		assert.NotEmpty(t, errors, "Expected error when union types have no overlapping types")
		assert.IsType(t, &CannotUnifyTypesError{}, errors[0])
	})

	t.Run("string literal unifies with string in union", func(t *testing.T) {
		// Create a string literal type "hello"
		strType := test_util.ParseTypeAnn(`"hello"`)

		// Create a union type: string | number
		unionType := test_util.ParseTypeAnn("string | number")

		// Test unification - should succeed because "hello" is compatible with string
		errors := checker.unify(ctx, strType, unionType)
		assert.Empty(t, errors, "Expected no errors when string literal unifies with union containing string")
	})

	t.Run("multiple literal types in union", func(t *testing.T) {
		// Create specific literal types
		colorUnion := test_util.ParseTypeAnn(`"red" | "green" | "blue"`)

		// Test with matching literal
		testStr := test_util.ParseTypeAnn(`"red"`)
		errors := checker.unify(ctx, testStr, colorUnion)
		assert.Empty(t, errors, "Expected no errors when literal matches one of the union literals")

		// Test with non-matching literal
		wrongStr := test_util.ParseTypeAnn(`"yellow"`)
		errors = checker.unify(ctx, wrongStr, colorUnion)
		assert.NotEmpty(t, errors, "Expected error when literal does not match any union literals")
	})

	t.Run("nested union types", func(t *testing.T) {
		// Create inner union: string | number
		innerUnion := test_util.ParseTypeAnn("string | number")

		// Create outer union that includes the inner union: (string | number) | boolean
		booleanType := NewBoolType()
		outerUnion := NewUnionType(innerUnion, booleanType)

		// Test with number literal - should work with nested union
		numLit := test_util.ParseTypeAnn("42")
		errors := checker.unify(ctx, numLit, outerUnion)
		assert.Empty(t, errors, "Expected no errors when literal unifies with nested union")
	})
}

func TestUnifyFuncTypes(t *testing.T) {
	checker := NewChecker()
	ctx := Context{
		Scope:      Prelude(),
		IsAsync:    false,
		IsPatMatch: false,
	}

	t.Run("identical function types should unify", func(t *testing.T) {
		func1 := test_util.ParseTypeAnn("fn(x: number) -> string")
		func2 := test_util.ParseTypeAnn("fn(x: number) -> string")

		errors := checker.unify(ctx, func1, func2)
		assert.Empty(t, errors, "Expected no errors when unifying identical function types")
	})

	t.Run("contravariant parameter types", func(t *testing.T) {
		// fn (x: number) -> string  vs  fn (x: any) -> string
		// number is subtype of any, so this should work (any -> string accepts number -> string)
		func1 := test_util.ParseTypeAnn("fn(x: number) -> string")
		func2 := test_util.ParseTypeAnn("fn(x: any) -> string")

		errors := checker.unify(ctx, func1, func2)
		assert.Empty(t, errors, "Expected no errors for contravariant parameter types")
	})

	t.Run("covariant return types", func(t *testing.T) {
		// fn (x: number) -> number  vs  fn (x: number) -> any
		// number is subtype of any, so this should work
		func1 := test_util.ParseTypeAnn("fn(x: number) -> number")
		func2 := test_util.ParseTypeAnn("fn(x: number) -> any")

		errors := checker.unify(ctx, func1, func2)
		assert.Empty(t, errors, "Expected no errors for covariant return types")
	})

	t.Run("incompatible parameter types should fail", func(t *testing.T) {
		// string and number are not related, so this should fail
		func1 := test_util.ParseTypeAnn("fn(x: string) -> number")
		func2 := test_util.ParseTypeAnn("fn(x: number) -> number")

		errors := checker.unify(ctx, func1, func2)
		assert.NotEmpty(t, errors, "Expected errors for incompatible parameter types")
	})

	t.Run("fewer parameters in target function", func(t *testing.T) {
		// fn (x: number, y: string) -> boolean  vs  fn (x: number) -> boolean
		// func1 takes more params than func2 expects - this should work
		func1 := test_util.ParseTypeAnn("fn(x: number, y: string) -> boolean")
		func2 := test_util.ParseTypeAnn("fn(x: number) -> boolean")

		errors := checker.unify(ctx, func1, func2)
		assert.Empty(t, errors, "Expected no errors when target function has fewer parameters")
	})

	t.Run("more parameters in target function should fail", func(t *testing.T) {
		// fn (x: number) -> boolean  vs  fn (x: number, y: string) -> boolean
		// func2 expects more params than func1 provides - this should fail
		func1 := test_util.ParseTypeAnn("fn(x: number) -> boolean")
		func2 := test_util.ParseTypeAnn("fn(x: number, y: string) -> boolean")

		errors := checker.unify(ctx, func1, func2)
		assert.NotEmpty(t, errors, "Expected errors when target function has more parameters")
	})

	t.Run("optional parameters", func(t *testing.T) {
		// fn (x: number, y?: string) -> boolean  vs  fn (x: number) -> boolean
		// Optional parameter in func1 should be compatible with no parameter in func2

		func1 := test_util.ParseTypeAnn("fn(x: number, y?: string) -> boolean")
		func2 := test_util.ParseTypeAnn("fn(x: number) -> boolean")

		errors := checker.unify(ctx, func1, func2)
		assert.Empty(t, errors, "Expected no errors with optional parameters")
	})

	t.Run("rest parameters", func(t *testing.T) {
		// fn (x: number, y: string, z: boolean) -> void  vs  fn (x: number, ...rest: Array<string | boolean>) -> void
		// func1 has more fixed params, func2 has rest param that should unify with excess params
		func1 := test_util.ParseTypeAnn("fn(x: number, y: string, z: boolean) -> undefined")
		func2 := test_util.ParseTypeAnn("fn(x: number, ...rest: Array<string | boolean>) -> undefined")

		errors := checker.unify(ctx, func1, func2)
		assert.Empty(t, errors, "Expected no errors when unifying with rest parameter")
	})

	t.Run("rest parameters - incompatible types", func(t *testing.T) {
		// fn (x: number, y: string, z: boolean) -> void  vs  fn (x: number, ...rest: Array<number>) -> void
		// Should fail because excess params [string, boolean] don't match rest type Array<number>
		func1 := test_util.ParseTypeAnn("fn(x: number, y: string, z: boolean) -> undefined")
		func2 := test_util.ParseTypeAnn("fn(x: number, ...rest: Array<number>) -> undefined")

		errors := checker.unify(ctx, func1, func2)
		assert.NotEmpty(t, errors, "Expected errors when rest parameter types don't match")
	})

	t.Run("rest parameters - homogeneous array", func(t *testing.T) {
		// fn (x: number, y: string, z: string) -> void  vs  fn (x: number, ...rest: Array<string>) -> void
		// Should succeed because both excess params are strings
		func1 := test_util.ParseTypeAnn("fn(x: number, y: string, z: string) -> undefined")
		func2 := test_util.ParseTypeAnn("fn(x: number, ...rest: Array<string>) -> undefined")

		errors := checker.unify(ctx, func1, func2)
		assert.Empty(t, errors, "Expected no errors when excess params match rest array element type")
	})

	t.Run("both functions have rest parameters", func(t *testing.T) {
		// fn (x: number, ...rest1: Array<string>) -> void  vs  fn (x: number, ...rest2: Array<string>) -> void
		// Should succeed because both rest parameters have the same type
		func1 := test_util.ParseTypeAnn("fn(x: number, ...rest1: Array<string>) -> undefined")
		func2 := test_util.ParseTypeAnn("fn(x: number, ...rest2: Array<string>) -> undefined")

		errors := checker.unify(ctx, func1, func2)
		assert.Empty(t, errors, "Expected no errors when both functions have compatible rest parameters")
	})
}

func TestUnifyUnknownType(t *testing.T) {
	checker := NewChecker()
	ctx := Context{}

	unknownType := test_util.ParseTypeAnn("unknown")
	anyType := test_util.ParseTypeAnn("any")
	neverType := test_util.ParseTypeAnn("never")
	numberType := test_util.ParseTypeAnn("number")
	stringType := test_util.ParseTypeAnn("string")
	booleanType := test_util.ParseTypeAnn("boolean")
	numLitType := test_util.ParseTypeAnn("42")
	strLitType := test_util.ParseTypeAnn(`"hello"`)
	boolLitType := test_util.ParseTypeAnn("true")

	t.Run("UnknownType can only unify with itself", func(t *testing.T) {
		errors := checker.unify(ctx, unknownType, unknownType)
		assert.Empty(t, errors, "unknown should unify with unknown")
	})

	t.Run("UnknownType cannot be assigned to other types", func(t *testing.T) {
		testCases := []struct {
			name       string
			targetType Type
		}{
			// Note: unknown -> any should succeed because any is the top type
			{"unknown -> never", neverType},
			{"unknown -> number", numberType},
			{"unknown -> string", stringType},
			{"unknown -> boolean", booleanType},
			{"unknown -> number literal", numLitType},
			{"unknown -> string literal", strLitType},
			{"unknown -> boolean literal", boolLitType},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				errors := checker.unify(ctx, unknownType, tc.targetType)
				assert.NotEmpty(t, errors, "UnknownType should not be assignable to %s", tc.name)
			})
		}
	})

	t.Run("UnknownType can be assigned to any (top type)", func(t *testing.T) {
		errors := checker.unify(ctx, unknownType, anyType)
		assert.Empty(t, errors, "UnknownType should be assignable to any (top type)")
	})

	t.Run("All types can be assigned to UnknownType", func(t *testing.T) {
		testCases := []struct {
			name       string
			sourceType Type
		}{
			{"any -> unknown", anyType},
			{"never -> unknown", neverType},
			{"number -> unknown", numberType},
			{"string -> unknown", stringType},
			{"boolean -> unknown", booleanType},
			{"number literal -> unknown", numLitType},
			{"string literal -> unknown", strLitType},
			{"boolean literal -> unknown", boolLitType},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				errors := checker.unify(ctx, tc.sourceType, unknownType)
				assert.Empty(t, errors, "%s should be assignable to UnknownType", tc.name)
			})
		}
	})

	t.Run("Complex types can be assigned to UnknownType", func(t *testing.T) {
		funcType := test_util.ParseTypeAnn("fn(x: number) -> string")
		errors := checker.unify(ctx, funcType, unknownType)
		assert.Empty(t, errors, "function type should be assignable to UnknownType")

		objType := test_util.ParseTypeAnn("{x: number, y: string}")
		errors = checker.unify(ctx, objType, unknownType)
		assert.Empty(t, errors, "object type should be assignable to UnknownType")

		tupleType := test_util.ParseTypeAnn("[number, string]")
		errors = checker.unify(ctx, tupleType, unknownType)
		assert.Empty(t, errors, "tuple type should be assignable to UnknownType")

		unionType := NewUnionType(numberType, stringType)
		errors = checker.unify(ctx, unionType, unknownType)
		assert.Empty(t, errors, "union type should be assignable to UnknownType")
	})
}

func TestUnifyMutableTypes(t *testing.T) {
	checker := NewChecker()
	ctx := Context{}

	t.Run("identical mutable types should unify", func(t *testing.T) {
		mutType1 := test_util.ParseTypeAnn("mut number")
		mutType2 := test_util.ParseTypeAnn("mut number")

		errors := checker.unify(ctx, mutType1, mutType2)
		assert.Empty(t, errors, "identical mutable types should unify")
	})

	t.Run("different mutable types should not unify", func(t *testing.T) {
		// mut number should NOT unify with mut string (invariant)
		mutNumber := test_util.ParseTypeAnn("mut number")
		mutString := test_util.ParseTypeAnn("mut string")

		errors := checker.unify(ctx, mutNumber, mutString)
		assert.NotEmpty(t, errors, "different mutable types should not unify")
		assert.IsType(t, &CannotUnifyTypesError{}, errors[0])
	})

	t.Run("mutable type should not unify with covariant subtype", func(t *testing.T) {
		// mut number should NOT unify with mut any (even though number is subtype of any)
		// This is different from regular unify which would allow this covariant relationship
		mutNumber := test_util.ParseTypeAnn("mut number")
		mutAny := test_util.ParseTypeAnn("mut any")

		errors := checker.unify(ctx, mutNumber, mutAny)
		assert.NotEmpty(t, errors, "mutable types require exact equality, not covariance")
		assert.IsType(t, &CannotUnifyTypesError{}, errors[0])
	})

	t.Run("mutable object types with same structure should unify", func(t *testing.T) {
		mutObj1 := test_util.ParseTypeAnn("mut {x: number, y: string}")
		mutObj2 := test_util.ParseTypeAnn("mut {x: number, y: string}")

		errors := checker.unify(ctx, mutObj1, mutObj2)
		assert.Empty(t, errors, "mutable object types with identical structure should unify")
	})

	t.Run("mutable object types with different property types should not unify", func(t *testing.T) {
		mutObj1 := test_util.ParseTypeAnn("mut {x: number}")
		mutObj2 := test_util.ParseTypeAnn("mut {x: string}")

		errors := checker.unify(ctx, mutObj1, mutObj2)
		assert.NotEmpty(t, errors, "mutable object types with different property types should not unify")
		assert.IsType(t, &CannotUnifyTypesError{}, errors[0])
	})

	t.Run("mutable array types with same element type should unify", func(t *testing.T) {
		// mut Array<number> should unify with mut Array<number>
		mutArray1 := test_util.ParseTypeAnn("mut Array<number>")
		mutArray2 := test_util.ParseTypeAnn("mut Array<number>")

		errors := checker.unify(ctx, mutArray1, mutArray2)
		assert.Empty(t, errors, "mutable array types with same element type should unify")
	})

	t.Run("mutable array types with different element types should not unify", func(t *testing.T) {
		// mut Array<number> should NOT unify with mut Array<string>
		mutArray1 := test_util.ParseTypeAnn("mut Array<number>")
		mutArray2 := test_util.ParseTypeAnn("mut Array<string>")

		errors := checker.unify(ctx, mutArray1, mutArray2)
		assert.NotEmpty(t, errors, "mutable array types with different element types should not unify")
		assert.IsType(t, &CannotUnifyTypesError{}, errors[0])
	})

	t.Run("mutable tuple types with same elements should unify", func(t *testing.T) {
		// mut [number, string] should unify with mut [number, string]
		mutTuple1 := test_util.ParseTypeAnn("mut [number, string]")
		mutTuple2 := test_util.ParseTypeAnn("mut [number, string]")

		errors := checker.unify(ctx, mutTuple1, mutTuple2)
		assert.Empty(t, errors, "mutable tuple types with same elements should unify")
	})

	t.Run("mutable tuple types with different elements should not unify", func(t *testing.T) {
		// mut [number, string] should NOT unify with mut [number, boolean]
		mutTuple1 := test_util.ParseTypeAnn("mut [number, string]")
		mutTuple2 := test_util.ParseTypeAnn("mut [number, boolean]")

		errors := checker.unify(ctx, mutTuple1, mutTuple2)
		assert.NotEmpty(t, errors, "mutable tuple types with different elements should not unify")
		assert.IsType(t, &CannotUnifyTypesError{}, errors[0])
	})

	t.Run("mutable literal types should unify only with exact same literal", func(t *testing.T) {
		mutLit42_1 := test_util.ParseTypeAnn("mut 42")
		mutLit42_2 := test_util.ParseTypeAnn("mut 42")
		mutLit43 := test_util.ParseTypeAnn("mut 43")

		// Same literal values should unify
		errors := checker.unify(ctx, mutLit42_1, mutLit42_2)
		assert.Empty(t, errors, "mutable literal types with same value should unify")

		// Different literal values should not unify
		errors = checker.unify(ctx, mutLit42_1, mutLit43)
		assert.NotEmpty(t, errors, "mutable literal types with different values should not unify")
		assert.IsType(t, &CannotUnifyTypesError{}, errors[0])
	})

	t.Run("mutable function types should unify with exact same signature", func(t *testing.T) {
		mutFunc1 := test_util.ParseTypeAnn("mut fn(x: number) -> string")
		mutFunc2 := test_util.ParseTypeAnn("mut fn(x: number) -> string")

		errors := checker.unify(ctx, mutFunc1, mutFunc2)
		assert.Empty(t, errors, "mutable function types with identical signatures should unify")
	})

	t.Run("nested mutable types should unify with exact same nesting", func(t *testing.T) {
		numberType := NewNumType()
		mutNumber := NewMutableType(numberType)
		mutMutNumber1 := NewMutableType(mutNumber)
		mutMutNumber2 := NewMutableType(NewMutableType(numberType))

		errors := checker.unify(ctx, mutMutNumber1, mutMutNumber2)
		assert.Empty(t, errors, "nested mutable types should unify with exact same nesting")
	})

	t.Run("mutable union types require exact same union members", func(t *testing.T) {
		// mut (number | string) should unify with mut (number | string)
		// but NOT with mut (number | boolean)
		mutUnion1 := test_util.ParseTypeAnn("mut number | string")
		mutUnion2 := test_util.ParseTypeAnn("mut number | string")
		mutUnion3 := test_util.ParseTypeAnn("mut number | boolean")

		// Same union should unify
		errors := checker.unify(ctx, mutUnion1, mutUnion2)
		assert.Empty(t, errors, "mutable union types with same members should unify")

		// Different unions should not unify
		errors = checker.unify(ctx, mutUnion1, mutUnion3)
		assert.NotEmpty(t, errors, "mutable union types with different members should not unify")
		assert.IsType(t, &CannotUnifyTypesError{}, errors[0])
	})
}
