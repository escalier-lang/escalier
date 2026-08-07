package ucs

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

func TestExactnessString(t *testing.T) {
	require.Equal(t, "exact", Exact.String())
	require.Equal(t, "inexact prefix", InexactPrefix.String())
	require.Equal(t, "unknown exactness", Exactness(99).String())
}

func TestTagTests(t *testing.T) {
	tests := []struct {
		name string
		in   Test
		want string
	}{
		{"empty object", &ObjectTest{}, "{}"},
		{"object shape", &ObjectTest{Keys: keys("x", "y")}, "{x, y}"},
		{
			// A destructuring default makes the field optional, so `{x = 0}` matches a
			// value with no `x` at all and must not test for one.
			"optional key from a default",
			&ObjectTest{Keys: []ObjectKey{{Name: "x", Optional: true}, {Name: "y"}}},
			"{x?, y}",
		},
		{
			// `{x, ...rest}` tests an object with at least an `x` field.
			"inexact object shape",
			&ObjectTest{Keys: keys("x"), Exactness: InexactPrefix},
			"{x, ...}",
		},
		{"empty tuple", &TupleTest{}, "[]"},
		{"tuple shape", &TupleTest{Len: 2}, "[_, _]"},
		{
			// `[first, ...rest]` tests a tuple at least one element long.
			"inexact tuple shape",
			&TupleTest{Len: 1, Exactness: InexactPrefix},
			"[_, ...]",
		},
		{"number literal", &LitTest{Lit: ast.NewNumber(1, ast.Span{})}, "1"},
		{"string literal", &LitTest{Lit: ast.NewString("one", ast.Span{})}, `"one"`},
		{"class tag", &ClassTest{Name: ast.NewIdentifier("Point", ast.Span{})}, "Point"},
		{
			"extractor tag",
			&ExtractorTest{Name: ast.NewIdentifier("Ok", ast.Span{}), Arity: 1},
			"Ok(_)",
		},
		{
			"nullary extractor tag",
			&ExtractorTest{Name: ast.NewIdentifier("None", ast.Span{})},
			"None()",
		},
		{"nil test", nil, "<nil>"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, testString(test.in))
		})
	}
}
