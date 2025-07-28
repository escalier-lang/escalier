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
