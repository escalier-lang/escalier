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
			// `bc` is shadowed by the `b` before it, but it sits inside `(b|bc)`, which is
			// shadowed by the outer `b`. Only the outer alternative is reported.
			name:   "AShadowedAlternativeInsideAShadowedAlternative",
			source: `/^(?<g>b|(b|bc)).*$/`,
			want:   []Shadowed{{Start: 9, End: 15, Text: "(b|bc)", By: "b"}},
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
			want:   []Shadowed{{Start: 5, End: 7, Text: "ab", By: "a"}},
		},
		{
			// Under `i`, `a` matches `A` too, so it covers `A`.
			name:   "AnotherCaseUnderTheIFlag",
			source: `/^(?:a|A)$/i`,
			want:   []Shadowed{{Start: 7, End: 8, Text: "A", By: "a"}},
		},
		{
			// Without `u`, `k` does not match the Kelvin sign, so both alternatives can match.
			name:   "TheKelvinSignWithoutTheUFlag",
			source: `/^(?:k|\u212A)$/i`,
			want:   nil,
		},
		{
			name:   "TheKelvinSignWithTheUFlag",
			source: `/^(?:k|\u212A)$/iu`,
			want:   []Shadowed{{Start: 7, End: 13, Text: `\u212A`, By: "k"}},
		},
		{
			name:   "AnotherCaseWithoutTheIFlag",
			source: `/^(?:a|A)$/`,
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

// TestContains covers when one pattern matches every string another does. A pattern matches a
// string when it matches anywhere in it, and a pair the check cannot decide is not contained.
func TestContains(t *testing.T) {
	tests := []struct {
		name     string
		sup, sub string
		want     bool
	}{
		{"AFixedCountInARun", `/^\d+$/`, `/^\d{3}$/`, true},
		{"ARunInAFixedCount", `/^\d{3}$/`, `/^\d+$/`, false},
		{"AlternativesInAClass", `/^[a-z]+$/`, `/^(?:cat|dog)$/`, true},
		{"TheSameLanguageOneWay", `/^[0-9]+$/`, `/^\d+$/`, true},
		{"TheSameLanguageTheOtherWay", `/^\d+$/`, `/^[0-9]+$/`, true},
		// `/\d/` matches any string holding a digit.
		{"AnAnchoredPatternInAnUnanchoredOne", `/\d/`, `/^\d+$/`, true},
		{"AnUnanchoredPatternInAnAnchoredOne", `/^\d+$/`, `/\d/`, false},
		{"AnUnanchoredPatternInAWiderOne", `/[a-z]/`, `/b/`, true},
		{"AStartTestInAnAlternation", `/^[ab]/`, `/^a|^b/`, true},
		{"AStartTestInOneAlternative", `/^a/`, `/^a|b/`, false},
		{"AStickyPatternReadsAsAnchored", `/a/y`, `/^a/`, true},
		{"TheIFlagOnTheSupertype", `/^a$/i`, `/^a$/`, true},
		{"TheIFlagOnTheSubtype", `/^a$/`, `/^a$/i`, false},
		{"TheUFlagOnOneSide", `/^a$/u`, `/^a$/`, false},
		// Without `u`, JavaScript folds case by uppercase forms, and the Kelvin sign is its own.
		{"TheKelvinSignWithoutTheUFlag", `/^k$/i`, `/^\u212A$/`, false},
		{"TheKelvinSignInAClassWithoutTheUFlag", `/^[a-z]$/i`, `/^\u212A$/`, false},
		{"TheKelvinSignWithTheUFlag", `/^k$/iu`, `/^\u212A$/u`, true},
		// Without `u`, a character whose full uppercase form is two characters compares as itself.
		// `ᾀ` and `ᾈ` both uppercase to `ἈΙ`.
		{"AnIotaSubscriptWithoutTheUFlag", `/^\u1F80$/i`, `/^\u1F88$/`, false},
		{"AnIotaSubscriptWithTheUFlag", `/^\u1F80$/iu`, `/^\u1F88$/u`, true},
		{"AnUppercaseLetterWithoutTheUFlag", `/^[a-z]$/i`, `/^K$/`, true},
		// The check models neither a word boundary nor `$` under the `m` flag.
		{"AWordBoundary", `/\bcat/`, `/^cat$/`, false},
		{"TheMFlag", `/^a$/m`, `/^a$/`, false},
		// The subtype's DFA has more states than the walk explores.
		{"APairPastTheCap", `/^(?:a|b)*a(?:a|b){12}$|^c$/`, `/^(?:a|b)*a(?:a|b){12}$/`, false},
		{"TheSamePatternPastTheCap", `/^(?:a|b)*a(?:a|b){12}$/`, `/^(?:a|b)*a(?:a|b){12}$/`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sup, reason := Compile(tt.sup)
			require.Empty(t, reason)
			sub, reason := Compile(tt.sub)
			require.Empty(t, reason)
			require.Equal(t, tt.want, sup.Contains(sub))
		})
	}
}
