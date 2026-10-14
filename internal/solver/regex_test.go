package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestInferRegexType covers a regex annotation: which string literals it admits, how it
// relates to `string` and to another regex type, how its flags and named groups translate to
// RE2, and the diagnostic for each construct RE2 does not support.
func TestInferRegexType(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// want is the printed type of `v`, checked when wantErrs is nil.
		want     string
		wantErrs []string
	}{
		{
			name: "a matching literal is admitted",
			src:  `val v: /^\d{3}-\d{4}$/ = "555-1234"`,
			want: `/^\d{3}-\d{4}$/`,
		},
		{
			name:     "a literal the pattern rejects is not admitted",
			src:      `val v: /^\d{3}-\d{4}$/ = "5551234"`,
			wantErrs: []string{`1:26-1:35: cannot constrain "5551234" <: /^\d{3}-\d{4}$/`},
		},
		{
			name:     "a string is not admitted",
			src:      `declare val s: string` + "\n" + `val v: /^a/ = s`,
			wantErrs: []string{`2:15-2:16: cannot constrain string <: /^a/`},
		},
		{
			name: "a regex type is a string",
			src:  `declare val r: /^a/` + "\n" + `val v: string = r`,
			want: `string`,
		},
		{
			name: "a regex type is the same regex type",
			src:  `declare val r: /^a/` + "\n" + `val v: /^a/ = r`,
			want: `/^a/`,
		},
		{
			name:     "a regex type is not a different regex type",
			src:      `declare val r: /^a/` + "\n" + `val v: /^a+/ = r`,
			wantErrs: []string{`2:16-2:17: cannot constrain /^a/ <: /^a+/`},
		},
		{
			name: "the i flag matches without case",
			src:  `val v: /^abc$/i = "ABC"`,
			want: `/^abc$/i`,
		},
		{
			name:     "the y flag anchors the match at the start",
			src:      `val v: /b/y = "ab"`,
			wantErrs: []string{`1:15-1:19: cannot constrain "ab" <: /b/y`},
		},
		{
			name: "a named group translates to RE2",
			src:  `val v: /^(?<area>\d{3})-\d{4}$/ = "555-1234"`,
			want: `/^(?<area>\d{3})-\d{4}$/`,
		},
		{
			name: "an escaped paren and a class are not read as groups",
			src:  `val v: /^\(\?=[(?=]$/ = "(?=="`,
			want: `/^\(\?=[(?=]$/`,
		},
		{
			// JavaScript's `\s` holds the Unicode spaces, where Go's holds only ASCII ones.
			name: "a space class matches a no-break space",
			src:  "val v: /^\\s$/ = \"\u00a0\"",
			want: `/^\s$/`,
		},
		{
			// JavaScript's `.` excludes every line terminator, where Go's excludes only `\n`.
			// The literal holds a raw U+2028 line separator.
			name:     "a dot does not match a line separator",
			src:      "val v: /^.$/ = \"\u2028\"",
			wantErrs: []string{`1:16-1:19: cannot constrain "\u2028" <: /^.$/`},
		},
		{
			name: "an empty negated class matches any character",
			src:  `val v: /^[^]$/ = "a"`,
			want: `/^[^]$/`,
		},
		{
			name:     "an empty class matches nothing",
			src:      `val v: /^[]$/ = "a"`,
			wantErrs: []string{`1:17-1:20: cannot constrain "a" <: /^[]$/`},
		},
		{
			name: "a unicode escape translates to RE2",
			src:  `val v: /^\u0041\u{42}$/u = "AB"`,
			want: `/^\u0041\u{42}$/u`,
		},
		{
			// Without the `u` flag, `\u{3}` is a literal `u` repeated three times.
			name: "a braced unicode escape without the u flag repeats a u",
			src:  `val v: /^\u{3}$/ = "uuu"`,
			want: `/^\u{3}$/`,
		},
		{
			name:     "a braced unicode escape without the u flag is no code point",
			src:      `val v: /^\u{42}$/ = "B"`,
			wantErrs: []string{`1:21-1:24: cannot constrain "B" <: /^\u{42}$/`},
		},
		{
			name:     "a repeated flag is rejected",
			src:      `val v: /a/ii = "a"`,
			wantErrs: []string{"1:8-1:13: regex type /a/ii repeats the flag `i`"},
		},
		{
			name:     "the v flag is rejected",
			src:      `val v: /a/v = "a"`,
			wantErrs: []string{"1:8-1:12: regex type /a/v uses the `v` flag, whose set notation RE2 does not support"},
		},
		{
			name:     "a negated space class inside a class is rejected",
			src:      `val v: /[\Sa]/ = "a"`,
			wantErrs: []string{"1:8-1:15: regex type /[\\Sa]/ uses `\\S` inside a character class, which has no RE2 form"},
		},
		{
			// A regex type meets a literal it admits at the literal.
			name: "an intersection with a matching literal is the literal",
			src:  `declare val r: /^a+$/ & "aaa"` + "\n" + `val v = r`,
			want: `"aaa"`,
		},
		{
			name: "an intersection with string is the regex type",
			src:  `declare val r: /^a+$/ & string` + "\n" + `val v = r`,
			want: `/^a+$/`,
		},
		{
			name:     "lookahead is rejected",
			src:      `val v: /(?=foo)foo/ = "foo"`,
			wantErrs: []string{`1:8-1:20: regex type /(?=foo)foo/ uses lookahead, which RE2 does not support`},
		},
		{
			name:     "negative lookahead is rejected",
			src:      `val v: /a(?!b)/ = "a"`,
			wantErrs: []string{`1:8-1:16: regex type /a(?!b)/ uses lookahead, which RE2 does not support`},
		},
		{
			name:     "lookbehind is rejected",
			src:      `val v: /(?<=a)b/ = "b"`,
			wantErrs: []string{`1:8-1:17: regex type /(?<=a)b/ uses lookbehind, which RE2 does not support`},
		},
		{
			name:     "negative lookbehind is rejected",
			src:      `val v: /(?<!a)b/ = "b"`,
			wantErrs: []string{`1:8-1:17: regex type /(?<!a)b/ uses lookbehind, which RE2 does not support`},
		},
		{
			name:     "a backreference is rejected",
			src:      `val v: /(a)\1/ = "aa"`,
			wantErrs: []string{`1:8-1:15: regex type /(a)\1/ uses a backreference, which RE2 does not support`},
		},
		{
			name:     "a named backreference is rejected",
			src:      `val v: /(?<x>a)\k<x>/ = "aa"`,
			wantErrs: []string{`1:8-1:22: regex type /(?<x>a)\k<x>/ uses a named backreference, which RE2 does not support`},
		},
		{
			name:     "a pattern RE2 cannot parse is rejected",
			src:      `val v: /a{2,1}/ = "aa"`,
			wantErrs: []string{"1:8-1:16: regex type /a{2,1}/ does not compile: error parsing regexp: invalid repeat count: `{2,1}`"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			if tt.wantErrs != nil {
				require.Equal(t, tt.wantErrs, messagesWithSpan(t, errs))
				return
			}
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["v"])
		})
	}
}

// A regex type reads the members of the `String` wrapper and keys a property, as `string`
// does. Both need the stdlib tree, which declares `String`.
func TestInferRegexTypeAsString(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a member read goes through the String wrapper",
			src:  `declare val r: /^a+$/` + "\n" + `val v = r.length`,
			want: "number",
		},
		{
			name: "a regex type keys a property as a string",
			src:  `declare val r: /^a+$/` + "\n" + `declare val o: {[key: string]?: number}` + "\n" + `val v = o[r]`,
			want: "number | undefined",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, errs := inferAgainstTree(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["v"])
		})
	}
}

// TestInferRegexTypeCaptures covers a regex type written as a conditional's Extends operand.
// Each named group captures the text it matched, which the Then branch reads under the group's
// name.
func TestInferRegexTypeCaptures(t *testing.T) {
	decls := `
		type StripUnderscores<T> = if T : /^_*(?<Ident>[a-zA-Z][a-zA-Z0-9]*)_*$/ { Ident } else { T }
		type Optional<T> = if T : /^a(?<B>b)?$/ { B } else { never }
		type Split<T> = if T : /^(?<Head>\w+)-(?<Tail>\w+)$/ { [Head, Tail] } else { never }
		type Either<T> = if T : /^(?<Y>a)$|^(?<Y>b)$/ { Y } else { never }
		type Digits<T> = if T : /^(?<D>\d+)$/ { D } else { never }
		type MaybeB<T> = if T : /^a(?<B>b)?$/ { B } else { never }
		declare val digits: /^(?<D>\d+)$/
		declare val maybeB: /^a(?<B>b)?$/
	`
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"AGroupCapturesItsText", `StripUnderscores<"__baz__">`, `"baz"`},
		{"ALiteralWithoutTheAffixes", `StripUnderscores<"foo">`, `"foo"`},
		{"ANonMatchTakesTheElseBranch", `StripUnderscores<"_123_">`, `"_123_"`},
		{"AUnionCheckDistributes", `StripUnderscores<"_a_" | "b">`, `"a" | "b"`},
		{"AStringCheckTakesTheElseBranch", `StripUnderscores<string>`, `string`},
		{"AnOptionalGroupThatMatched", `Optional<"ab">`, `"b"`},
		{"AnOptionalGroupThatDidNotMatch", `Optional<"a">`, `undefined`},
		{"TwoGroups", `Split<"left-right">`, `["left", "right"]`},
		// One name on groups in two alternatives captures from whichever took part.
		{"ANameSharedAcrossAlternatives", `Either<"b">`, `"b"`},
		// A value of the same regex type captures some string the checker cannot name.
		{"TheSameRegexTypeCapturesAString", `Digits<typeof digits>`, `string`},
		{"TheSameRegexTypeWithAnOptionalGroup", `MaybeB<typeof maybeB>`, `string | undefined`},
		// A regex type carries no members, as a string literal carries none.
		{"KeyofARegexType", `keyof /^a/`, `never`},
		{"ExactOverARegexType", `Exact< /^a/ >`, `/^a/`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, decls+"type Result = "+tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}
