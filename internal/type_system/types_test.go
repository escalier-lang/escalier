package type_system

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewRegexType(t *testing.T) {
	tests := []struct {
		name            string
		pattern         string
		expectPanic     bool
		expectedGroups  []string
		shouldHaveRegex bool
	}{
		{
			name:            "simple pattern without capture groups",
			pattern:         "/hello/",
			expectPanic:     false,
			expectedGroups:  []string{},
			shouldHaveRegex: true,
		},
		{
			name:            "pattern with anchors",
			pattern:         "/^hello$/",
			expectPanic:     false,
			expectedGroups:  []string{},
			shouldHaveRegex: true,
		},
		{
			name:            "pattern with character class",
			pattern:         "/[a-z]+/",
			expectPanic:     false,
			expectedGroups:  []string{},
			shouldHaveRegex: true,
		},
		{
			name:            "pattern with flags",
			pattern:         "/hello/i",
			expectPanic:     false,
			expectedGroups:  []string{},
			shouldHaveRegex: true,
		},
		{
			name:            "pattern with multiple flags",
			pattern:         "/hello/gim",
			expectPanic:     false,
			expectedGroups:  []string{},
			shouldHaveRegex: true,
		},
		{
			name:            "pattern with unnamed capture group",
			pattern:         "/(hello)/",
			expectPanic:     false,
			expectedGroups:  []string{},
			shouldHaveRegex: true,
		},
		{
			name:            "pattern with named capture group",
			pattern:         "/(?<word>hello)/",
			expectPanic:     false,
			expectedGroups:  []string{"word"},
			shouldHaveRegex: true,
		},
		{
			name:            "pattern with multiple named capture groups",
			pattern:         "/(?<first>[a-z]+)-(?<second>[0-9]+)/",
			expectPanic:     false,
			expectedGroups:  []string{"first", "second"},
			shouldHaveRegex: true,
		},
		{
			name:            "pattern with mixed capture groups",
			pattern:         "/([a-z]+)-(?<id>[0-9]+)-([a-z]+)/",
			expectPanic:     false,
			expectedGroups:  []string{"id"},
			shouldHaveRegex: true,
		},
		{
			name:            "complex email pattern with named groups",
			pattern:         `/(?<user>[a-zA-Z0-9._%+-]+)@(?<domain>[a-zA-Z0-9.-]+\.[a-zA-Z]{2,})/`,
			expectPanic:     false,
			expectedGroups:  []string{"user", "domain"},
			shouldHaveRegex: true,
		},
		{
			name:            "nested named capture groups",
			pattern:         "/(?<outer>prefix-(?<inner>[0-9]+)-suffix)/",
			expectPanic:     false,
			expectedGroups:  []string{"outer", "inner"},
			shouldHaveRegex: true,
		},
		{
			name:            "phone number pattern",
			pattern:         `/^\d{3}-\d{3}-\d{4}$/`,
			expectPanic:     false,
			expectedGroups:  []string{},
			shouldHaveRegex: true,
		},
		{
			name:            "invalid pattern - no closing slash",
			pattern:         "/hello",
			expectPanic:     false,
			expectedGroups:  []string{},
			shouldHaveRegex: false,
		},
		{
			name:            "invalid pattern - no starting slash",
			pattern:         "hello/",
			expectPanic:     false,
			expectedGroups:  []string{},
			shouldHaveRegex: false,
		},
		{
			name:            "invalid pattern - empty",
			pattern:         "",
			expectPanic:     false,
			expectedGroups:  []string{},
			shouldHaveRegex: false,
		},
		{
			name:            "invalid pattern - single slash",
			pattern:         "/",
			expectPanic:     false,
			expectedGroups:  []string{},
			shouldHaveRegex: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.expectPanic {
				assert.Panics(t, func() {
					_, _ = NewRegexTypeWithPatternString(nil, test.pattern)
				})
				return
			}

			// Test creation
			result, err := NewRegexTypeWithPatternString(nil, test.pattern)

			if !test.shouldHaveRegex {
				// For invalid patterns, expect an error and NeverType
				assert.NotNil(t, err, "Expected error for invalid pattern")
				assert.IsType(t, NewNeverType(nil), result)
				return
			}

			// For valid patterns, expect no error
			assert.Nil(t, err, "Expected no error for valid pattern")
			regexType := result.(*RegexType)

			// Verify basic properties
			assert.NotNil(t, result)
			assert.Nil(t, result.Provenance()) // should be nil by default

			if test.shouldHaveRegex {
				assert.NotNil(t, regexType.Regex)
			}

			// Verify named capture groups
			assert.NotNil(t, regexType.Groups)

			// Check that all expected groups are present
			for _, expectedGroup := range test.expectedGroups {
				groupType, exists := regexType.Groups[expectedGroup]
				assert.True(t, exists, "Expected group %s not found", expectedGroup)
				assert.NotNil(t, groupType)
				// Groups should be initialized with UnknownType
				assert.IsType(t, NewStrPrimType(nil), groupType)
			}

			// Check that no unexpected groups are present
			assert.Equal(t, len(test.expectedGroups), len(regexType.Groups),
				"Number of groups doesn't match expected")

			// Verify that the regex compiles correctly by testing String() method
			if test.shouldHaveRegex {
				assert.NotEmpty(t, result.String())
			}
		})
	}
}

func TestRegexType_Methods(t *testing.T) {
	t.Run("Equal method", func(t *testing.T) {
		regex1, _ := NewRegexTypeWithPatternString(nil, "/hello/")
		regex2, _ := NewRegexTypeWithPatternString(nil, "/hello/")
		regex3, _ := NewRegexTypeWithPatternString(nil, "/world/")

		// Same pattern should be equal
		assert.True(t, Equals(regex1, regex2))

		// Different patterns should not be equal
		assert.False(t, Equals(regex1, regex3))

		// Different types should not be equal
		assert.False(t, Equals(regex1, NewStrPrimType(nil)))
	})

	t.Run("String method", func(t *testing.T) {
		regex, _ := NewRegexTypeWithPatternString(nil, "/hello/")
		str := regex.String()
		assert.NotEmpty(t, str)
		assert.Contains(t, str, "hello")
	})

	t.Run("Provenance methods", func(t *testing.T) {
		regex, _ := NewRegexTypeWithPatternString(nil, "/hello/")

		// Initial provenance should be nil
		assert.Nil(t, regex.Provenance())

		// TODO: Test SetProvenance and WithProvenance when we have a concrete Provenance implementation
	})
}

func TestRegexType_CaptureGroups(t *testing.T) {
	t.Run("no capture groups", func(t *testing.T) {
		result, _ := NewRegexTypeWithPatternString(nil, "/hello/")
		regexType := result.(*RegexType)
		assert.Empty(t, regexType.Groups)
	})

	t.Run("single named capture group", func(t *testing.T) {
		result, _ := NewRegexTypeWithPatternString(nil, "/(?<word>hello)/")
		regexType := result.(*RegexType)
		assert.Len(t, regexType.Groups, 1)
		assert.Contains(t, regexType.Groups, "word")
		assert.IsType(t, NewStrPrimType(nil), regexType.Groups["word"])
	})

	t.Run("multiple named capture groups", func(t *testing.T) {
		result, _ := NewRegexTypeWithPatternString(nil, "/(?<first>\\w+)-(?<second>\\d+)/")
		regexType := result.(*RegexType)
		assert.Len(t, regexType.Groups, 2)
		assert.Contains(t, regexType.Groups, "first")
		assert.Contains(t, regexType.Groups, "second")
		assert.IsType(t, NewStrPrimType(nil), regexType.Groups["first"])
		assert.IsType(t, NewStrPrimType(nil), regexType.Groups["second"])
	})

	t.Run("mixed named and unnamed groups", func(t *testing.T) {
		result, _ := NewRegexTypeWithPatternString(nil, "/(\\w+)-(?<id>\\d+)-(\\w+)/")
		regexType := result.(*RegexType)
		// Only named groups should be in the Groups map
		assert.Len(t, regexType.Groups, 1)
		assert.Contains(t, regexType.Groups, "id")
		assert.IsType(t, NewStrPrimType(nil), regexType.Groups["id"])
	})
}

func TestRegexType_JavaScriptToGoConversion(t *testing.T) {
	t.Run("flags conversion", func(t *testing.T) {
		tests := []struct {
			name    string
			pattern string
		}{
			{"case insensitive", "/hello/i"},
			{"multiline", "/hello/m"},
			{"dot all", "/hello/s"},
			{"global (ignored)", "/hello/g"},
			{"unicode (ignored)", "/hello/u"},
			{"sticky (ignored)", "/hello/y"},
			{"multiple flags", "/hello/gims"},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				result, _ := NewRegexTypeWithPatternString(nil, test.pattern)
				regexType := result.(*RegexType)
				assert.NotNil(t, regexType.Regex)
			})
		}
	})

	t.Run("named capture group conversion", func(t *testing.T) {
		result, _ := NewRegexTypeWithPatternString(nil, "/(?<name>\\w+)/")
		regexType := result.(*RegexType)

		// Verify the regex was created successfully
		assert.NotNil(t, regexType.Regex)

		// Verify the named group was captured
		assert.Len(t, regexType.Groups, 1)
		assert.Contains(t, regexType.Groups, "name")

		// The underlying Go regex should use (?P<name>...) syntax
		// We can verify this by checking the SubexpNames
		names := regexType.Regex.SubexpNames()
		assert.Contains(t, names, "name")
	})
}

func TestObjectType_Equal(t *testing.T) {
	t.Run("empty object types should be equal", func(t *testing.T) {
		objType1 := NewObjectType(nil, []ObjTypeElem{})
		objType2 := NewObjectType(nil, []ObjTypeElem{})

		assert.True(t, Equals(objType1, objType2), "Empty object types should be equal")
	})

	t.Run("object type should not be equal to non-object type", func(t *testing.T) {
		numberType := NewNumPrimType(nil)
		objType := NewObjectType(nil, []ObjTypeElem{})

		assert.False(t, Equals(objType, numberType), "Object type should not be equal to primitive type")
		assert.False(t, Equals(objType, NewStrPrimType(nil)), "Object type should not be equal to different type")
	})

	t.Run("object types with single identical property should be equal", func(t *testing.T) {
		// Create two object types with the same field: {x: number}
		numberType := NewNumPrimType(nil)

		elems1 := []ObjTypeElem{
			NewPropertyElem(NewStrKey("x"), numberType),
		}
		elems2 := []ObjTypeElem{
			NewPropertyElem(NewStrKey("x"), numberType),
		}

		objType1 := NewObjectType(nil, elems1)
		objType2 := NewObjectType(nil, elems2)

		assert.True(t, Equals(objType1, objType2), "Object types with identical single property should be equal")
		assert.True(t, Equals(objType2, objType1), "Equality should be symmetric")
	})

	t.Run("object types with different single property should not be equal", func(t *testing.T) {
		// Create two object types: {x: number} vs {x: string}
		numberType := NewNumPrimType(nil)
		stringType := NewStrPrimType(nil)

		elems1 := []ObjTypeElem{
			NewPropertyElem(NewStrKey("x"), numberType),
		}
		elems2 := []ObjTypeElem{
			NewPropertyElem(NewStrKey("x"), stringType),
		}

		objType1 := NewObjectType(nil, elems1)
		objType2 := NewObjectType(nil, elems2)

		assert.False(t, Equals(objType1, objType2), "Object types with different property types should not be equal")
	})
}
