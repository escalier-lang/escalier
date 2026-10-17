package jsregex

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestShadowed covers which alternatives can never take part in a match, and the patterns the
// check declines to read.
func TestShadowed(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   []Shadowed
	}{
		{
			// `.*` matches whatever follows `a`, so the match never backtracks into `ab`.
			name:   "AnEarlierAlternativeAlwaysFinishes",
			source: `/^(?<A>a|ab).*$/`,
			want:   []Shadowed{{Start: 9, End: 11, Text: "ab", By: "a"}},
		},
		{
			// "abc" fails through `a`, since `c` cannot match the `b` after it, so `ab` is tried.
			name:   "EveryAlternativeIsReachable",
			source: `/^(?<A>a|ab)c$/`,
			want:   nil,
		},
		{
			// A test needs only a prefix to match, so `cat` finishes every match `cats` would.
			name:   "AnUnanchoredTopLevelAlternation",
			source: `/cat|cats/`,
			want:   []Shadowed{{Start: 5, End: 9, Text: "cats", By: "cat"}},
		},
		{
			name:   "AnAnchoredTopLevelAlternation",
			source: `/^(cat|cats)$/`,
			want:   nil,
		},
		{
			name:   "ADigitRunBeforeADigitRunWithAUnit",
			source: `/^(?<A>\d+|\d+px)/`,
			want:   []Shadowed{{Start: 11, End: 16, Text: `\d+px`, By: `\d+`}},
		},
		{
			// Neither `a.*` nor `b.*` covers `[ab]x` alone, but the two together do.
			name:   "TheEarlierAlternativesTogether",
			source: `/^(?:a.*|b.*|[ab]x)$/`,
			want:   []Shadowed{{Start: 13, End: 18, Text: "[ab]x"}},
		},
		{
			// `c` is covered neither by an earlier alternative alone nor by the two together.
			name:   "EveryAlternativeOfThreeIsReachable",
			source: `/^(?:a|b|c)$/`,
			want:   nil,
		},
		{
			// The `s` and `u` flags carry into the check, and `g` changes nothing it reads.
			name:   "TheSUAndGFlags",
			source: `/^(a|ab).*$/gsu`,
			want:   []Shadowed{{Start: 5, End: 7, Text: "ab", By: "a"}},
		},
		{
			// The continuation repeats, but the alternation does not, so the check reads it.
			// "ab" fails through `a`, since `(?:c|d)*$` cannot match the `b`.
			name:   "ARepeatedContinuation",
			source: `/^(a|ab)(?:c|d)*$/`,
			want:   nil,
		},
		{
			name:   "AnAlternationNestedInANamedGroup",
			source: `/^(?<A>x(a|ab).*)$/`,
			want:   []Shadowed{{Start: 11, End: 13, Text: "ab", By: "a"}},
		},
		{
			name:   "AnAlternativeHoldingANamedGroup",
			source: `/^(?:a|(?<B>ab)).*$/`,
			want:   []Shadowed{{Start: 7, End: 15, Text: "(?<B>ab)", By: "a"}},
		},
		{
			// A quantifier repeats the alternation, so the source after it is no continuation.
			name:   "ARepeatedAlternation",
			source: `/^(a|ab)*$/`,
			want:   nil,
		},
		{
			name:   "TheIFlag",
			source: `/^(a|ab).*$/i`,
			want:   nil,
		},
		{
			// `\b` tests the character before it, which the check does not read.
			name:   "AWordBoundary",
			source: `/^(a|ab)\b/`,
			want:   nil,
		},
		{
			// The second alternative is shadowed, but proving it explores more DFA states than
			// the cap allows. The same pattern with `{2}` in place of `{12}` is proved.
			name:   "APatternOverTheStateCap",
			source: `/^(?:[ab]*a[ab]{12}|[ab]*a[ab]{12}c)/`,
			want:   nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, reason := Compile(tt.source)
			require.Empty(t, reason)
			require.Equal(t, tt.want, p.Shadowed())
		})
	}
}
