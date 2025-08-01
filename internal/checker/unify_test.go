package checker

import (
	"testing"

	. "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
)

func TestConvertJSRegexToGo(t *testing.T) {
	tests := []struct {
		name     string
		jsRegex  string
		expected string
		hasError bool
	}{
		{
			name:     "simple pattern without flags",
			jsRegex:  "/hello/",
			expected: "hello",
			hasError: false,
		},
		{
			name:     "pattern with case insensitive flag",
			jsRegex:  "/hello/i",
			expected: "(?i)hello",
			hasError: false,
		},
		{
			name:     "pattern with multiple flags",
			jsRegex:  "/hello/gim",
			expected: "(?im)hello",
			hasError: false,
		},
		{
			name:     "complex pattern with anchors",
			jsRegex:  "/^hello$/",
			expected: "^hello$",
			hasError: false,
		},
		{
			name:     "pattern with character class and flags",
			jsRegex:  "/[a-z]+/i",
			expected: "(?i)[a-z]+",
			hasError: false,
		},
		{
			name:     "phone number pattern",
			jsRegex:  `/^\d{3}-\d{3}-\d{4}$/`,
			expected: `^\d{3}-\d{3}-\d{4}$`,
			hasError: false,
		},
		{
			name:     "invalid format - no closing slash",
			jsRegex:  "/hello",
			expected: "",
			hasError: true,
		},
		{
			name:     "invalid format - no starting slash",
			jsRegex:  "hello/",
			expected: "",
			hasError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := convertJSRegexToGo(test.jsRegex)
			if test.hasError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, test.expected, result)
			}
		})
	}
}

func TestUnifyStrLitWithRegexLit(t *testing.T) {
	checker := &Checker{}
	ctx := Context{}

	t.Run("string matches regex pattern", func(t *testing.T) {
		// Create a string literal type "hello"
		strLit := &StrLit{Value: "hello"}
		strType := NewLitType(strLit)

		// Create a regex literal type that matches "hello" (JavaScript syntax)
		regexLit := &RegexLit{Value: "/^hello$/"}
		regexType := NewLitType(regexLit)

		// Test unification - should succeed because "hello" matches "^hello$"
		errors := checker.unify(ctx, strType, regexType)
		assert.Empty(t, errors, "Expected no errors when string matches regex pattern")
	})

	t.Run("string does not match regex pattern", func(t *testing.T) {
		// Create a string literal type "world"
		strLit := &StrLit{Value: "world"}
		strType := NewLitType(strLit)

		// Create a regex literal type that matches only "hello" (JavaScript syntax)
		regexLit := &RegexLit{Value: "/^hello$/"}
		regexType := NewLitType(regexLit)

		// Test unification - should fail because "world" does not match "^hello$"
		errors := checker.unify(ctx, strType, regexType)
		assert.NotEmpty(t, errors, "Expected error when string does not match regex pattern")
		assert.IsType(t, &CannotUnifyTypesError{}, errors[0])
	})

	t.Run("string matches complex regex pattern", func(t *testing.T) {
		// Create a string literal type "123-456-7890"
		strLit := &StrLit{Value: "123-456-7890"}
		strType := NewLitType(strLit)

		// Create a regex literal type for phone number pattern (JavaScript syntax)
		regexLit := &RegexLit{Value: `/^\d{3}-\d{3}-\d{4}$/`}
		regexType := NewLitType(regexLit)

		// Test unification - should succeed because the string matches the phone pattern
		errors := checker.unify(ctx, strType, regexType)
		assert.Empty(t, errors, "Expected no errors when string matches phone number pattern")
	})

	t.Run("case insensitive matching", func(t *testing.T) {
		// Create a string literal type "HELLO"
		strLit := &StrLit{Value: "HELLO"}
		strType := NewLitType(strLit)

		// Create a regex literal type with case insensitive flag (JavaScript syntax)
		regexLit := &RegexLit{Value: "/^hello$/i"}
		regexType := NewLitType(regexLit)

		// Test unification - should succeed because of case insensitive flag
		errors := checker.unify(ctx, strType, regexType)
		assert.Empty(t, errors, "Expected no errors when string matches regex with case insensitive flag")
	})

	t.Run("invalid regex format", func(t *testing.T) {
		// Create a string literal type
		strLit := &StrLit{Value: "test"}
		strType := NewLitType(strLit)

		// Create an invalid regex literal type (missing closing slash)
		regexLit := &RegexLit{Value: "/invalid"}
		regexType := NewLitType(regexLit)

		// Test unification - should fail because the regex format is invalid
		errors := checker.unify(ctx, strType, regexType)
		assert.NotEmpty(t, errors, "Expected error when regex format is invalid")
		assert.IsType(t, &CannotUnifyTypesError{}, errors[0])
	})

	t.Run("regex with global flag", func(t *testing.T) {
		// Create a string literal type "hello world hello"
		strLit := &StrLit{Value: "hello"}
		strType := NewLitType(strLit)

		// Create a regex literal type with global flag (JavaScript syntax)
		regexLit := &RegexLit{Value: "/hello/g"}
		regexType := NewLitType(regexLit)

		// Test unification - should succeed (global flag is ignored in MatchString)
		errors := checker.unify(ctx, strType, regexType)
		assert.Empty(t, errors, "Expected no errors when string matches regex with global flag")
	})

	t.Run("existing functionality still works - string literals", func(t *testing.T) {
		// Create two identical string literal types
		strLit1 := &StrLit{Value: "hello"}
		strType1 := NewLitType(strLit1)

		strLit2 := &StrLit{Value: "hello"}
		strType2 := NewLitType(strLit2)

		// Test unification - should succeed because they are identical
		errors := checker.unify(ctx, strType1, strType2)
		assert.Empty(t, errors, "Expected no errors when unifying identical string literals")
	})
}

func TestUnifyWithUnionTypes(t *testing.T) {
	checker := &Checker{}
	ctx := Context{}

	t.Run("literal type unifies with union containing compatible type", func(t *testing.T) {
		// Create a number literal type "5"
		numLit := &NumLit{Value: 5}
		numType := NewLitType(numLit)

		// Create a union type: string | number
		stringType := NewStrType()
		numberType := NewNumType()
		unionType := NewUnionType(stringType, numberType)

		// Test unification - should succeed because 5 is compatible with number
		errors := checker.unify(ctx, numType, unionType)
		assert.Empty(t, errors, "Expected no errors when literal unifies with union containing compatible type")
	})

	t.Run("literal type fails to unify with union containing no compatible types", func(t *testing.T) {
		// Create a boolean literal type "true"
		boolLit := &BoolLit{Value: true}
		boolType := NewLitType(boolLit)

		// Create a union type: string | number (no boolean)
		stringType := NewStrType()
		numberType := NewNumType()
		unionType := NewUnionType(stringType, numberType)

		// Test unification - should fail because boolean is not compatible with string or number
		errors := checker.unify(ctx, boolType, unionType)
		assert.NotEmpty(t, errors, "Expected error when literal does not unify with any type in union")
		assert.IsType(t, &CannotUnifyTypesError{}, errors[0])
	})

	t.Run("primitive type unifies with union containing same type", func(t *testing.T) {
		// Create a string primitive type
		stringType := NewStrType()

		// Create a union type: string | number | boolean
		numberType := NewNumType()
		booleanType := NewBoolType()
		unionType := NewUnionType(stringType, numberType, booleanType)

		// Test unification - should succeed because string is in the union
		errors := checker.unify(ctx, stringType, unionType)
		assert.Empty(t, errors, "Expected no errors when primitive type unifies with union containing same type")
	})

	t.Run("primitive type fails to unify with union not containing that type", func(t *testing.T) {
		// Create a bigint primitive type
		bigintType := &PrimType{Prim: BigIntPrim}

		// Create a union type: string | number | boolean (no bigint)
		stringType := NewStrType()
		numberType := NewNumType()
		booleanType := NewBoolType()
		unionType := NewUnionType(stringType, numberType, booleanType)

		// Test unification - should fail because bigint is not in the union
		errors := checker.unify(ctx, bigintType, unionType)
		assert.NotEmpty(t, errors, "Expected error when primitive type is not in union")
		assert.IsType(t, &CannotUnifyTypesError{}, errors[0])
	})

	t.Run("union type unifies with broader union type", func(t *testing.T) {
		// Create a smaller union type: string | number
		stringType := NewStrType()
		numberType := NewNumType()
		smallUnion := NewUnionType(stringType, numberType)

		// Create a larger union type: string | number | boolean
		booleanType := NewBoolType()
		largeUnion := NewUnionType(stringType, numberType, booleanType)

		// Test unification - should succeed because all types in smallUnion are in largeUnion
		errors := checker.unify(ctx, smallUnion, largeUnion)
		assert.Empty(t, errors, "Expected no errors when smaller union unifies with larger union")
	})

	t.Run("union type fails to unify with incompatible union type", func(t *testing.T) {
		// Create a union type: string | number
		stringType := NewStrType()
		numberType := NewNumType()
		union1 := NewUnionType(stringType, numberType)

		// Create another union type: boolean | bigint
		booleanType := NewBoolType()
		bigintType := &PrimType{Prim: BigIntPrim}
		union2 := NewUnionType(booleanType, bigintType)

		// Test unification - should fail because no types overlap
		errors := checker.unify(ctx, union1, union2)
		assert.NotEmpty(t, errors, "Expected error when union types have no overlapping types")
		assert.IsType(t, &CannotUnifyTypesError{}, errors[0])
	})

	t.Run("string literal unifies with string in union", func(t *testing.T) {
		// Create a string literal type "hello"
		strLit := &StrLit{Value: "hello"}
		strType := NewLitType(strLit)

		// Create a union type: string | number
		stringType := NewStrType()
		numberType := NewNumType()
		unionType := NewUnionType(stringType, numberType)

		// Test unification - should succeed because "hello" is compatible with string
		errors := checker.unify(ctx, strType, unionType)
		assert.Empty(t, errors, "Expected no errors when string literal unifies with union containing string")
	})

	t.Run("multiple literal types in union", func(t *testing.T) {
		// Create specific literal types
		str1 := NewLitType(&StrLit{Value: "red"})
		str2 := NewLitType(&StrLit{Value: "green"})
		str3 := NewLitType(&StrLit{Value: "blue"})
		colorUnion := NewUnionType(str1, str2, str3)

		// Test with matching literal
		testStr := NewLitType(&StrLit{Value: "red"})
		errors := checker.unify(ctx, testStr, colorUnion)
		assert.Empty(t, errors, "Expected no errors when literal matches one of the union literals")

		// Test with non-matching literal
		wrongStr := NewLitType(&StrLit{Value: "yellow"})
		errors = checker.unify(ctx, wrongStr, colorUnion)
		assert.NotEmpty(t, errors, "Expected error when literal does not match any union literals")
	})

	t.Run("nested union types", func(t *testing.T) {
		// Create inner union: string | number
		stringType := NewStrType()
		numberType := NewNumType()
		innerUnion := NewUnionType(stringType, numberType)

		// Create outer union that includes the inner union: (string | number) | boolean
		booleanType := NewBoolType()
		outerUnion := NewUnionType(innerUnion, booleanType)

		// Test with number literal - should work with nested union
		numLit := NewLitType(&NumLit{Value: 42})
		errors := checker.unify(ctx, numLit, outerUnion)
		assert.Empty(t, errors, "Expected no errors when literal unifies with nested union")
	})
}
