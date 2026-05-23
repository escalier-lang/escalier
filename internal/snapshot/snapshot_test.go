package snapshot

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

func TestString(t *testing.T) {
	span := ast.Span{
		Start: ast.Location{Line: 1, Column: 1},
		End:   ast.Location{Line: 1, Column: 4},
	}
	spanWithSrc := ast.Span{
		Start:    ast.Location{Line: 2, Column: 3},
		End:      ast.Location{Line: 2, Column: 7},
		SourceID: 5,
	}

	tests := []struct {
		name string
		in   any
		want string
	}{
		{
			name: "primitives skip zero",
			in: struct {
				Name      string
				Empty     string
				Count     int
				Zero      int
				Flag      bool
				FlagFalse bool
			}{Name: "a", Count: 3, Flag: true},
			want: "struct { Name string; Empty string; Count int; Zero int; Flag bool; FlagFalse bool }{\n" +
				"    Name: \"a\",\n" +
				"    Count: 3,\n" +
				"    Flag: true,\n" +
				"}",
		},
		{
			name: "span compact",
			in:   span,
			want: "1:1-1:4",
		},
		{
			name: "span with source id",
			in:   spanWithSrc,
			want: "2:3-2:7@5",
		},
		{
			name: "ident expr omits zero fields",
			in: &ast.IdentExpr{
				Name: "x",
			},
			want: "&ast.IdentExpr{\n" +
				"    Name: \"x\",\n" +
				"}",
		},
		{
			name: "nil pointer",
			in:   (*ast.IdentExpr)(nil),
			want: "nil",
		},
		{
			name: "empty slice elided",
			in: struct {
				A []int
				B []int
			}{A: []int{1, 2}},
			want: "struct { A []int; B []int }{\n" +
				"    A: []int{\n" +
				"        1,\n" +
				"        2,\n" +
				"    },\n" +
				"}",
		},
		{
			name: "map sorted by key",
			in:   map[string]int{"b": 2, "a": 1},
			want: "map[string]int{\n" +
				"    \"a\": 1,\n" +
				"    \"b\": 2,\n" +
				"}",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := String(tc.in)
			require.Equal(t, tc.want, got)
		})
	}
}
