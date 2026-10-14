package jsregex

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMatchString covers how a compiled pattern decides a string, including where JavaScript
// and Go read the same syntax differently.
func TestMatchString(t *testing.T) {
	tests := []struct {
		name   string
		source string
		input  string
		want   bool
	}{
		{"AnUnknownEscapeIsItsCharacter", `/^\a$/`, "a", true},
		{"AnUnknownEscapeIsNotGosBell", `/^\a$/`, "\a", false},
		{"APropertyEscapeWithoutUIsLiteral", `/^\p{L}$/`, "p{L}", true},
		{"APropertyEscapeWithoutUMatchesNoLetter", `/^\p{L}+$/`, "abc", false},
		{"APropertyEscapeUnderU", `/^\p{L}+$/u`, "abc", true},
		{"AScriptPropertyUnderU", `/^\p{Script=Greek}+$/u`, "αβγ", true},
		{"ABackspaceInAClass", `/^[\b]$/`, "\b", true},
		{"AHexEscape", `/^\x41$/`, "A", true},
		{"ASurrogatePairWithoutU", `/^😀$/`, "😀", true},
		{"ASurrogatePairUnderU", `/^😀$/u`, "😀", true},
		{"ADotWithoutUMatchesOneCodeUnit", `/^.$/`, "😀", false},
		{"TwoDotsWithoutUMatchAnAstralCharacter", `/^..$/`, "😀", true},
		{"ADotUnderUMatchesOneCodePoint", `/^.$/u`, "😀", true},
		{"AnAstralLiteralWithoutU", `/^😀$/`, "😀", true},
		{"ANegatedClassWithoutUMatchesOneUnit", `/^[^a]$/`, "😀", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, reason := Compile(tt.source)
			require.Empty(t, reason)
			require.Equal(t, tt.want, p.MatchString(tt.input))
		})
	}
}

// TestCompileRejects covers the reason Compile gives for a literal it cannot compile.
func TestCompileRejects(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"AnUnknownEscapeUnderU", `/\a/u`, "uses the escape `\\a`, which the `u` flag does not allow"},
		{"AnUnsupportedProperty", `/\p{Script_Extensions=Greek}/u`, "uses the property `Script_Extensions`, which RE2 does not support"},
		{"AMalformedBracedEscape", `/\u{zz}/u`, "uses a malformed `\\u{` escape"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, reason := Compile(tt.source)
			require.Nil(t, p)
			require.Equal(t, tt.want, reason)
		})
	}
}

// TestCaptures covers the text each named group captures. A group that takes no part in the
// match has no entry.
func TestCaptures(t *testing.T) {
	tests := []struct {
		name   string
		source string
		input  string
		want   map[string]string
	}{
		{"TwoGroups", `/^(?<a>\w+)-(?<b>\w+)$/`, "x-y", map[string]string{"a": "x", "b": "y"}},
		{"AnOptionalGroupThatMatched", `/^a(?<b>b)?$/`, "ab", map[string]string{"b": "b"}},
		{"AnOptionalGroupLeftOut", `/^a(?<b>b)?$/`, "a", map[string]string{}},
		// One name on groups in two alternatives captures from whichever took part.
		{"ANameSharedAcrossAlternatives", `/^(?<y>a)$|^(?<y>b)$/`, "b", map[string]string{"y": "b"}},
		{"AnAstralCaptureWithoutU", `/^(?<c>..)$/`, "😀", map[string]string{"c": "😀"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, reason := Compile(tt.source)
			require.Empty(t, reason)
			got, ok := p.Captures(tt.input)
			require.True(t, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestGroups covers the names a pattern's groups use and whether a match can leave each out.
func TestGroups(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   []Group
	}{
		{"NoGroups", `/^a+$/`, nil},
		{"ARequiredGroup", `/^(?<a>a)$/`, []Group{{Name: "a", Indices: []int{1}}}},
		{"AnUnnamedGroupBeforeANamedOne", `/^(a)(?<b>b)$/`, []Group{{Name: "b", Indices: []int{2}}}},
		{"AnOptionalGroup", `/^(?<a>a)?$/`, []Group{{Name: "a", Indices: []int{1}, Optional: true}}},
		{"AStarredGroup", `/^(?<a>a)*$/`, []Group{{Name: "a", Indices: []int{1}, Optional: true}}},
		{"AGroupInAnAlternation", `/^(?<a>a)|b$/`, []Group{{Name: "a", Indices: []int{1}, Optional: true}}},
		{"ANameSharedAcrossAlternatives", `/^(?<y>a)$|^(?<y>b)$/`, []Group{{Name: "y", Indices: []int{1, 2}, Optional: true}}},
		{"AGroupUnderAPlus", `/^(?<a>a)+$/`, []Group{{Name: "a", Indices: []int{1}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, reason := Compile(tt.source)
			require.Empty(t, reason)
			require.Equal(t, tt.want, p.Groups)
		})
	}
}

// TestGroupNames covers which groups of a literal count as named capture groups.
func TestGroupNames(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   []string
	}{
		{"NamedGroups", `/^(?<area>\d{3})-(?<line>\d{4})$/`, []string{"area", "line"}},
		{"EscapedParen", `/\(?<a>b/`, []string{}},
		{"InsideAClass", `/[(?<a>]/`, []string{}},
		{"ALiteralThatDoesNotCompile", `/(?=a)(?<b>b)/`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, GroupNames(tt.source))
		})
	}
}
