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
			name:        "invalid pattern - no closing slash",
			pattern:     "/hello",
			expectPanic: true,
		},
		{
			name:        "invalid pattern - no starting slash",
			pattern:     "hello/",
			expectPanic: true,
		},
		{
			name:        "invalid pattern - empty",
			pattern:     "",
			expectPanic: true,
		},
		{
			name:        "invalid pattern - single slash",
			pattern:     "/",
			expectPanic: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.expectPanic {
				assert.Panics(t, func() {
					NewRegexType(test.pattern)
				})
				return
			}

			// Test successful creation
			result := NewRegexType(test.pattern)

			// Verify basic properties
			assert.NotNil(t, result)
			assert.Nil(t, result.Provenance()) // should be nil by default

			if test.shouldHaveRegex {
				assert.NotNil(t, result.Regex)
			}

			// Verify named capture groups
			assert.NotNil(t, result.Groups)

			// Check that all expected groups are present
			for _, expectedGroup := range test.expectedGroups {
				groupType, exists := result.Groups[expectedGroup]
				assert.True(t, exists, "Expected group %s not found", expectedGroup)
				assert.NotNil(t, groupType)
				// Groups should be initialized with UnknownType
				assert.IsType(t, NewStrType(), groupType)
			}

			// Check that no unexpected groups are present
			assert.Equal(t, len(test.expectedGroups), len(result.Groups),
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
		regex1 := NewRegexType("/hello/")
		regex2 := NewRegexType("/hello/")
		regex3 := NewRegexType("/world/")

		// Same pattern should be equal
		assert.True(t, regex1.Equal(regex2))

		// Different patterns should not be equal
		assert.False(t, regex1.Equal(regex3))

		// Different types should not be equal
		assert.False(t, regex1.Equal(NewStrType()))
	})

	t.Run("String method", func(t *testing.T) {
		regex := NewRegexType("/hello/")
		str := regex.String()
		assert.NotEmpty(t, str)
		assert.Contains(t, str, "hello")
	})

	t.Run("Provenance methods", func(t *testing.T) {
		regex := NewRegexType("/hello/")

		// Initial provenance should be nil
		assert.Nil(t, regex.Provenance())

		// TODO: Test SetProvenance and WithProvenance when we have a concrete Provenance implementation
	})
}

func TestRegexType_CaptureGroups(t *testing.T) {
	t.Run("no capture groups", func(t *testing.T) {
		regex := NewRegexType("/hello/")
		assert.Empty(t, regex.Groups)
	})

	t.Run("single named capture group", func(t *testing.T) {
		regex := NewRegexType("/(?<word>hello)/")
		assert.Len(t, regex.Groups, 1)
		assert.Contains(t, regex.Groups, "word")
		assert.IsType(t, NewStrType(), regex.Groups["word"])
	})

	t.Run("multiple named capture groups", func(t *testing.T) {
		regex := NewRegexType("/(?<first>\\w+)-(?<second>\\d+)/")
		assert.Len(t, regex.Groups, 2)
		assert.Contains(t, regex.Groups, "first")
		assert.Contains(t, regex.Groups, "second")
		assert.IsType(t, NewStrType(), regex.Groups["first"])
		assert.IsType(t, NewStrType(), regex.Groups["second"])
	})

	t.Run("mixed named and unnamed groups", func(t *testing.T) {
		regex := NewRegexType("/(\\w+)-(?<id>\\d+)-(\\w+)/")
		// Only named groups should be in the Groups map
		assert.Len(t, regex.Groups, 1)
		assert.Contains(t, regex.Groups, "id")
		assert.IsType(t, NewStrType(), regex.Groups["id"])
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
				regex := NewRegexType(test.pattern)
				assert.NotNil(t, regex.Regex)
			})
		}
	})

	t.Run("named capture group conversion", func(t *testing.T) {
		regex := NewRegexType("/(?<name>\\w+)/")

		// Verify the regex was created successfully
		assert.NotNil(t, regex.Regex)

		// Verify the named group was captured
		assert.Len(t, regex.Groups, 1)
		assert.Contains(t, regex.Groups, "name")

		// The underlying Go regex should use (?P<name>...) syntax
		// We can verify this by checking the SubexpNames
		names := regex.Regex.SubexpNames()
		assert.Contains(t, names, "name")
	})
}
