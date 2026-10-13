package ast

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRegexGroupNames covers which groups of a JavaScript regex literal count as named
// capture groups.
func TestRegexGroupNames(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   []string
	}{
		{"NoGroups", `/^a+$/`, nil},
		{"NamedGroups", `/^(?<area>\d{3})-(?<line>\d{4})$/`, []string{"area", "line"}},
		{"UnnamedGroup", `/^(a)(?<b>b)$/`, []string{"b"}},
		{"Lookbehind", `/(?<=a)(?<!b)(?<c>c)/`, []string{"c"}},
		{"EscapedParen", `/\(?<a>b/`, nil},
		{"InsideAClass", `/[(?<a>]/`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, RegexGroupNames(tt.source))
		})
	}
}
