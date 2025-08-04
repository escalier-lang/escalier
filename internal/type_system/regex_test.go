package type_system

import (
	"testing"

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
		{
			name:     "named capture group",
			jsRegex:  "/(?<name>[a-z]+)/",
			expected: "(?P<name>[a-z]+)",
			hasError: false,
		},
		{
			name:     "multiple named capture groups",
			jsRegex:  "/(?<first>[a-z]+)-(?<second>[0-9]+)/",
			expected: "(?P<first>[a-z]+)-(?P<second>[0-9]+)",
			hasError: false,
		},
		{
			name:     "named capture group with flags",
			jsRegex:  "/(?<word>[a-z]+)/i",
			expected: "(?i)(?P<word>[a-z]+)",
			hasError: false,
		},
		{
			name:     "mixed capture groups",
			jsRegex:  "/([a-z]+)-(?<id>[0-9]+)-([a-z]+)/",
			expected: "([a-z]+)-(?P<id>[0-9]+)-([a-z]+)",
			hasError: false,
		},
		{
			name:     "nested named capture groups",
			jsRegex:  "/(?<outer>prefix-(?<inner>[0-9]+)-suffix)/",
			expected: "(?P<outer>prefix-(?P<inner>[0-9]+)-suffix)",
			hasError: false,
		},
		{
			name:     "email pattern with named groups",
			jsRegex:  `/(?<user>[a-zA-Z0-9._%+-]+)@(?<domain>[a-zA-Z0-9.-]+\.[a-zA-Z]{2,})/`,
			expected: `(?P<user>[a-zA-Z0-9._%+-]+)@(?P<domain>[a-zA-Z0-9.-]+\.[a-zA-Z]{2,})`,
			hasError: false,
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
