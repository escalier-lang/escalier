package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferDo covers `do { … }`, which is the value its block completes with.
func TestInferDo(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "Literal", src: `val x = do { 5 }`, want: "5"},
		{
			name: "LastExpression",
			src: `val x = do {
				val a = 5
				val b = 10
				a + b
			}`,
			want: "number",
		},
		{name: "Nested", src: `val x = do { do { 10 } }`, want: "10"},
		{
			// A block ending in a declaration completes with no value.
			name: "EndsInDeclaration",
			src:  `val x = do { val a = 1 }`,
			want: "undefined",
		},
		{name: "Empty", src: `val x = do {}`, want: "undefined"},
		{
			name: "Conditional",
			src:  `val x = do { if true { "large" } else { "small" } }`,
			want: `"large" | "small"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, _, errs := inferSource(t, test.src)
			require.Empty(t, errs)
			require.Equal(t, test.want, values["x"])
		})
	}
}

// TestInferDoScope asserts that a name a `do` block declares stays inside it.
func TestInferDoScope(t *testing.T) {
	_, _, errs := inferSource(t, `
		val x = do { val inner = 1
			inner }
		val y = inner
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unknown identifier: inner", errs[0].Message())
}

// TestInferDoReturn asserts that a `return` inside a `do` block returns from the
// enclosing function, so the block diverges and contributes nothing to `x`.
func TestInferDoReturn(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(c: boolean) {
			val x = if c { do { return "early" } } else { 1 }
			return x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (c: boolean) -> 1 | "early"`, values["f"])
}
