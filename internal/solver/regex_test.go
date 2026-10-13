package solver

import (
	"testing"

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
