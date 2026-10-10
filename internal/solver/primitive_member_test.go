package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPrimitiveMemberReadsWrapper covers a member read off a primitive, which resolves
// through the class the standard library declares for that primitive's values.
func TestPrimitiveMemberReadsWrapper(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "NumberLiteral",
			src:  `val n = (1.5).toFixed(2)`,
			want: map[string]string{"n": "string"},
		},
		{
			name: "StringLiteralProperty",
			src:  `val n = "abc".length`,
			want: map[string]string{"n": "number"},
		},
		{
			name: "DeclaredString",
			src: `
				declare val s: string
				val up = s.toUpperCase()
			`,
			want: map[string]string{"up": "string"},
		},
		{
			name: "AnnotatedParameter",
			src:  `fn format(x: number) -> string { return x.toFixed(1) }`,
			want: map[string]string{"format": "fn (x: number) -> string"},
		},
		{
			name: "Boolean",
			src: `
				declare val b: boolean
				val v = b.valueOf()
			`,
			want: map[string]string{"v": "boolean"},
		},
		{
			name: "LiteralUnion",
			src: `
				declare val x: 1 | 2
				val s = x.toFixed(1)
			`,
			want: map[string]string{"s": "string"},
		},
		{
			name: "MethodReadAsValue",
			src: `
				declare val x: number
				val f = x.toFixed
			`,
			want: map[string]string{"f": "fn (fractionDigits?: number) -> string"},
		},
		{
			// The wrapper comes from the package that declares it, so a module's own
			// `Number` does not change what a number's members are.
			name: "ModuleClassDoesNotReplaceWrapper",
			src: `
				class Number { x: number }
				val n = (1.5).toFixed(2)
			`,
			want: map[string]string{"n": "string"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, errs := inferAgainstTree(t, test.src)
			require.Empty(t, errs)
			for name, want := range test.want {
				require.Equal(t, want, values[name], "value binding %q", name)
			}
		})
	}
}

// TestPrimitiveMemberMissing asserts that a member the wrapper does not declare is
// reported as missing.
func TestPrimitiveMemberMissing(t *testing.T) {
	_, errs := inferAgainstTree(t, `val n = (1.5).nope`)
	require.Equal(t, []string{"object is missing property: nope"}, errs)
}

// TestPrimitiveMemberMixedReceiver asserts that a receiver whose values are not all one
// primitive reads no wrapper, so a member only the primitive's wrapper has is reported.
func TestPrimitiveMemberMixedReceiver(t *testing.T) {
	_, errs := inferAgainstTree(t, `
		fn f(c: boolean, x: {a: number}) {
			val y = if c { 1 } else { x }
			return y.toFixed(1)
		}
	`)
	require.Equal(t, []string{
		"cannot constrain 1 <: object",
		"object is missing property: toFixed",
	}, errs)
}
