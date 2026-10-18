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
		// Without `u`, the `i` flag compares characters by their uppercase forms, and a
		// non-ASCII character never folds to an ASCII one. The Kelvin sign, the long s and
		// the Angstrom sign are their own uppercase forms.
		{"TheIFlagMatchesTheOtherCase", `/^k$/i`, "K", true},
		{"TheIFlagSkipsTheKelvinSign", `/^k$/i`, "\u212A", false},
		{"TheIFlagSkipsTheLongSInAClass", `/^[a-z]$/i`, "\u017F", false},
		{"TheIFlagSkipsTheLongS", `/^s$/i`, "\u017F", false},
		{"TheIFlagSkipsTheAngstromSign", `/^å$/i`, "\u212B", false},
		{"TheIFlagMatchesANonASCIICase", `/^å$/i`, "Å", true},
		{"TheIFlagFoldsAnEscape", `/^\x41$/i`, "a", true},
		{"TheIFlagFoldsANegatedClassFirst", `/^[^a]$/i`, "A", false},
		{"TheIFlagKeepsWhatANegatedClassAllows", `/^[^a]$/i`, "b", true},
		// Go parses a class of exactly two cases, such as `[ßẞ]`, as one case-folding literal.
		{"TheIFlagKeepsBothCasesOfATwoCaseClass", `/^[ßẞ]$/i`, "ẞ", true},
		{"TheIFlagKeepsBothCasesOutOfANegatedTwoCaseClass", `/^[^ßẞ]$/i`, "ẞ", false},
		{"TheIFlagKeepsTheCapitalSharpSApart", `/^ß$/i`, "ẞ", false},
		{"TheIFlagUnderUMatchesTheKelvinSign", `/^k$/iu`, "\u212A", true},
		{"TheIFlagUnderUMatchesTheLongSInAClass", `/^[a-z]$/iu`, "\u017F", true},
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
		{"ARequiredGroup", `/^(?<a>a)$/`, []Group{{Name: "a", Indices: []int{1}, Bodies: []string{"a"}}}},
		{"AnUnnamedGroupBeforeANamedOne", `/^(a)(?<b>b)$/`, []Group{{Name: "b", Indices: []int{2}, Bodies: []string{"b"}}}},
		{"AnOptionalGroup", `/^(?<a>a)?$/`, []Group{{Name: "a", Indices: []int{1}, Optional: true, Bodies: []string{"a"}}}},
		{"AStarredGroup", `/^(?<a>a)*$/`, []Group{{Name: "a", Indices: []int{1}, Optional: true, Bodies: []string{"a"}}}},
		{"AGroupInAnAlternation", `/^(?<a>a)|b$/`, []Group{{Name: "a", Indices: []int{1}, Optional: true, Bodies: []string{"a"}}}},
		{"ANameSharedAcrossAlternatives", `/^(?<y>a)$|^(?<y>b)$/`, []Group{{Name: "y", Indices: []int{1, 2}, Optional: true, Bodies: []string{"a", "b"}}}},
		{"AGroupUnderAPlus", `/^(?<a>a)+$/`, []Group{{Name: "a", Indices: []int{1}, Bodies: []string{"a"}}}},
		{"ANestedGroup", `/^(?<a>x(?<b>[)]y)z)$/`, []Group{{Name: "a", Indices: []int{1}, Bodies: []string{"x(?<b>[)]y)z"}}, {Name: "b", Indices: []int{2}, Bodies: []string{"[)]y"}}}},
		{"AnEscapedParenInABody", `/^(?<a>\)+)$/`, []Group{{Name: "a", Indices: []int{1}, Bodies: []string{`\)+`}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, reason := Compile(tt.source)
			require.Empty(t, reason)
			require.Equal(t, tt.want, p.Groups)
		})
	}
}

// TestCaptureSources covers the regex literal a group's captures read as, and the groups whose
// captures no literal of the body alone describes.
func TestCaptureSources(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   []string
	}{
		{"ABody", `/^_*(?<id>[a-z][a-z0-9]*)_*$/`, []string{`/^[a-z][a-z0-9]*$/`}},
		{"AnAlternationIsGrouped", `/^(?<v>a|bc)!$/`, []string{`/^(?:a|bc)$/`}},
		{"ANestedAlternationIsNot", `/^(?<v>(a|b)c)$/`, []string{`/^(a|b)c$/`}},
		{"AnAlternationInAClassIsNot", `/^(?<v>[|]x)$/`, []string{`/^[|]x$/`}},
		{"ANameSharedAcrossAlternatives", `/^(?<y>a)$|^(?<y>b)$/`, []string{`/^a$/`, `/^b$/`}},
		{"TheFlagsAMatchReadsAreKept", `/^(?<a>a.)$/gisuy`, []string{`/^a.$/isu`}},
		{"AnAnchorWithoutTheMFlag", `/^(?<a>^a)$/`, []string{`/^^a$/`}},
		{"TheMFlagWithoutAnAnchor", `/^(?<a>a)$/m`, []string{`/^a$/`}},
		{"ABackspaceInAClass", `/^(?<a>[\b])$/`, []string{`/^[\b]$/`}},
		{"AWordBoundary", `/^a(?<w>\bb)$/`, nil},
		{"ANonWordBoundary", `/^a(?<w>\Bb)$/`, nil},
		{"ALineAnchorUnderTheMFlag", `/^(?<a>a$)/m`, nil},
		// A shadowed alternative never takes part in a match, so the literal leaves it out.
		{"AShadowedAlternativeIsLeftOut", `/^(?<a>a|ab).*$/`, []string{`/^a$/`}},
		{"AShadowedAlternativeInsideTheBody", `/^(?<a>x(a|ab).*)$/`, []string{`/^x(a).*$/`}},
		// A shadowed alternative outside the group leaves the group's literal as written.
		{"AShadowedAlternativeOutsideTheGroup", `/^(?:a|ab)(?<b>c).*$/`, []string{`/^c$/`}},
		// The inner `bc` is shadowed too, and goes with the outer alternative it sits in.
		{"AShadowedAlternativeInsideAShadowedAlternative", `/^(?<g>b|(b|bc)).*$/`, []string{`/^b$/`}},
		// A group inside a shadowed alternative never captures, so it gives no literal.
		{"AGroupInsideAShadowedAlternative", `/^(?:b|(?<a>bc)).*$/`, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, reason := Compile(tt.source)
			require.Empty(t, reason)
			got, ok := p.CaptureSources(p.Groups[0])
			require.Equal(t, tt.want != nil, ok)
			require.Equal(t, tt.want, got)
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
