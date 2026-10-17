package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
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
		declare val either: /^(?<Y>a)$|^(?<Y>b)$/
		type Word<T> = if T : /^(?<W>\b\w+)$/ { W } else { never }
		declare val word: /^(?<W>\b\w+)$/
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
		// Then captures the strings the group's body matches, and Else keeps the strings the whole
		// pattern rejects.
		{"AStringCheckJoinsBothBranches", `StripUnderscores<string>`, `/^[a-zA-Z][a-zA-Z0-9]*$/ | string & ~/^_*(?<Ident>[a-zA-Z][a-zA-Z0-9]*)_*$/`},
		{"AnOptionalGroupThatMatched", `Optional<"ab">`, `"b"`},
		{"AnOptionalGroupThatDidNotMatch", `Optional<"a">`, `undefined`},
		{"TwoGroups", `Split<"left-right">`, `["left", "right"]`},
		// One name on groups in two alternatives captures from whichever took part.
		{"ANameSharedAcrossAlternatives", `Either<"b">`, `"b"`},
		// A value of the same regex type captures some string the checker cannot name. The string
		// is one the group's body matches.
		{"TheSameRegexTypeCapturesWhatTheBodyMatches", `Digits<typeof digits>`, `/^\d+$/`},
		{"TheSameRegexTypeWithAnOptionalGroup", `MaybeB<typeof maybeB>`, `/^b$/ | undefined`},
		// Each group that uses the name contributes its own body. jsregex counts a name shared
		// across alternatives as optional, so `undefined` joins them.
		{"TheSameRegexTypeWithANameSharedAcrossAlternatives", `Either<typeof either>`, `/^a$/ | /^b$/ | undefined`},
		// `\b` tests the character before the group, which a regex type of the body alone cannot
		// read, so the group captures `string`.
		{"ABodyReadingItsSurroundingsCapturesAString", `Word<typeof word>`, `string`},
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

// TestInferRegexLiteral covers a regex literal, which types as an instance of the `RegExp`
// class std:regexp declares, so its members read through that class.
func TestInferRegexLiteral(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "a literal is a RegExp",
			src:  `val re = /ab+c/`,
			want: map[string]string{"re": "RegExp"},
		},
		{
			name: "a literal with flags is a RegExp",
			src:  `val re = /^(?<year>\d{4})$/gi`,
			want: map[string]string{"re": "RegExp"},
		},
		{
			name: "a method reads through RegExp",
			src: `
				val re: mut RegExp = /ab+c/
				val ok = re.test("abbc")
			`,
			want: map[string]string{"ok": "boolean"},
		},
		{
			name: "a property reads through RegExp",
			src:  `val src = /ab+c/.source`,
			want: map[string]string{"src": "string"},
		},
		{
			name: "a literal passes where a RegExp is expected",
			src:  `val parts = "a,b".split(/,/)`,
			want: map[string]string{"parts": "Array<string>"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, errs := inferAgainstTree(t, test.src)
			require.Empty(t, errs)
			for name, want := range test.want {
				require.Equal(t, want, values[name], name)
			}
		})
	}
}

// TestRegexLiteralWithoutRegExp asserts that a run with no std:regexp reports the missing
// class once, however many regex literals it reads, and records each literal's type.
func TestRegexLiteralWithoutRegExp(t *testing.T) {
	c := newTestChecker()
	first := ast.NewLitExpr(ast.NewRegex("/a/", testSpan()))
	second := ast.NewLitExpr(ast.NewRegex("/b/", testSpan()))
	require.IsType(t, &soltype.ErrorType{}, c.inferExpr(NewScope(), 0, first))
	require.IsType(t, &soltype.ErrorType{}, c.inferExpr(NewScope(), 0, second))
	require.Equal(t, []string{
		"the standard library declares no class `RegExp`, which the checker needs",
	}, errorMessagesOf(c.errs))
	require.IsType(t, &soltype.ErrorType{}, c.info.TypeOf(first))
	require.IsType(t, &soltype.ErrorType{}, c.info.TypeOf(second))
}

// TestInferRegexTypeComplement covers a `string` narrowed to the strings a regex type rejects.
// A conditional over a regex type takes both branches for `string`, reading the check as the
// matching strings in Then and as `string & ~/re/` in Else.
func TestInferRegexTypeComplement(t *testing.T) {
	decls := `
		type StripUnderscores<T> = if T : /^_*(?<Ident>[a-zA-Z][a-zA-Z0-9]*)_*$/ { Ident } else { T }
		type NotDigits<T> = if T : /^\d+$/ { never } else { T }
		type OnlyDigits<T> = if T : /^\d+$/ { T } else { never }
		type D = /^\d+$/
		type NotD<T> = if T : D { never } else { T }
	`
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"ARegexAliasSplitsAsAnInlineRegex", `NotD<string>`, `string & ~/^\d+$/`},
		// A check that is not a naked type parameter does not distribute, so it decides one
		// branch.
		{"ANonDistributiveConditionalDecidesOneBranch", `if string : /^\d+$/ { "a" } else { "b" }`, `"b"`},
		{"TheElseBranchReadsTheComplement", `NotDigits<string>`, `string & ~/^\d+$/`},
		{"TheThenBranchReadsTheRegexType", `OnlyDigits<string>`, `/^\d+$/`},
		// Ident captures the strings its group's body matches in Then, and Else yields the
		// complement.
		{"BothBranchesJoin", `StripUnderscores<string>`, `/^[a-zA-Z][a-zA-Z0-9]*$/ | string & ~/^_*(?<Ident>[a-zA-Z][a-zA-Z0-9]*)_*$/`},
		{"AStringAlreadyExcludingTheRegexTakesElse", `NotDigits<NotDigits<string>>`, `string & ~/^\d+$/`},
		{"AMatchingLiteralTakesThen", `NotDigits<"12">`, `never`},
		{"ARejectedLiteralTakesElse", `NotDigits<"ab">`, `"ab"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, decls+"type Result = "+tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// TestInferRegexTypeComplementInAMatch covers the complement a `match` arm after a regex
// annotation test reads, and a literal checked against a complement.
func TestInferRegexTypeComplementInAMatch(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		want     string
		wantErrs []string
	}{
		{
			name: "TheFallThroughArmBindsTheComplement",
			src:  `fn f(s: string) { return match s { d: /^\d+$/ => "", other => other } }`,
			want: `fn (s: string) -> string & ~/^\d+$/`,
		},
		{
			name: "ARegexAliasAnnotation",
			src: `type D = /^\d+$/
				fn f(s: string) { return match s { d: D => "", other => other } }`,
			want: `fn (s: string) -> string & ~/^\d+$/`,
		},
		{
			name: "AMatchingLiteralIsRejectedAgainstTheComplement",
			src: `type NotDigits<T> = if T : /^\d+$/ { never } else { T }
				fn f() -> NotDigits<string> { return "12" }`,
			wantErrs: []string{`2:42-2:46: cannot constrain "12" <: ~/^\d+$/`},
		},
		{
			name: "ARejectedLiteralFitsTheComplement",
			src: `type NotDigits<T> = if T : /^\d+$/ { never } else { T }
				fn f() -> NotDigits<string> { return "ab" }`,
			want: "fn () -> NotDigits<string>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			if tt.wantErrs != nil {
				require.Equal(t, tt.wantErrs, messagesWithSpan(t, errs))
				return
			}
			require.Empty(t, messagesWithSpan(t, errs))
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// TestInferRegexShadowedAlternative covers the warning on a regex alternative that can never
// take part in a match, and the capture type that leaves such an alternative out.
func TestInferRegexShadowedAlternative(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		want     string
		wantErrs []string
	}{
		{
			// `.*` matches whatever follows `a`, so `A` only ever captures what `a` matches.
			name: "AShadowedAlternativeLeavesTheCaptureType",
			src: `type First<T> = if T : /^(?<A>a|ab).*$/ { A } else { never }
declare val s: /^(?<A>a|ab).*$/
type Result = First<typeof s>`,
			want: `/^a$/`,
			wantErrs: []string{
				"1:33-1:35: `ab` can never match, because `a` before it always matches first",
				"2:25-2:27: `ab` can never match, because `a` before it always matches first",
			},
		},
		{
			name: "EveryAlternativeIsReachable",
			src: `type First<T> = if T : /^(?<A>a|ab)c$/ { A } else { never }
declare val s: /^(?<A>a|ab)c$/
type Result = First<typeof s>`,
			want: `/^(?:a|ab)$/`,
		},
		{
			name:     "TheEarlierAlternativesTogether",
			src:      `type Result = /^(?:a.*|b.*|[ab]x)$/`,
			want:     `/^(?:a.*|b.*|[ab]x)$/`,
			wantErrs: []string{"1:28-1:33: `[ab]x` can never match, because the alternatives before it always match first"},
		},
		{
			// `B` sits inside the shadowed alternative, so it never captures.
			name: "AGroupInsideAShadowedAlternative",
			src: `type Inner<T> = if T : /^(?:b|(?<B>bc)).*$/ { B } else { never }
declare val s: /^(?:b|(?<B>bc)).*$/
type Result = Inner<typeof s>`,
			want: `undefined`,
			wantErrs: []string{
				"1:31-1:39: `(?<B>bc)` can never match, because `b` before it always matches first",
				"2:23-2:31: `(?<B>bc)` can never match, because `b` before it always matches first",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Equal(t, tt.wantErrs, messagesWithSpan(t, errs))
			for _, e := range errs {
				require.True(t, isWarning(e), "a shadowed alternative is a warning")
			}
			require.Equal(t, tt.want, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}
