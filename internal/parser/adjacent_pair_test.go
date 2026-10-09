package parser

import (
	"context"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/stretchr/testify/require"
)

// parseAndPrint parses src as declarations and prints each one back, so a case can assert
// the source round-trips. It returns the printed declarations joined by newlines and the
// parse error messages.
func parseAndPrint(t *testing.T, src string) (string, []string) {
	t.Helper()
	decls, errs := ParseDecls(context.Background(), &ast.Source{ID: 0, Path: "t.esc", Contents: src})
	var msgs []string
	for _, e := range errs {
		msgs = append(msgs, e.Message)
	}
	printed := make([]string, len(decls))
	for i, d := range decls {
		out, err := printer.Print(d, printer.DefaultOptions())
		require.NoError(t, err)
		printed[i] = out
	}
	return strings.Join(printed, "\n"), msgs
}

// TestParseAdjacentGreaterThanPairs covers `>=` read from two adjacent tokens, and `>:`
// recognized the same way so a lower bound written on a binder gets its own error. The
// lexer keeps `>` a token of its own, so a type's closing `>` may be followed by `:` or `=`
// without the pair fusing into one operator.
func TestParseAdjacentGreaterThanPairs(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// want is the printed form, or src itself when empty.
		want string
		errs []string
	}{
		{
			name: "ALowerBoundOnABinder",
			src:  "declare fn f<B >: T>(x: B) -> B",
			want: "declare fn f<B>(x: B) -> B where T: B",
			errs: []string{"lower bounds are written in a where clause, as where T: B"},
		},
		{
			// A spaced pair is not the operator. The `>` closes the list and the `:` after
			// it is unexpected.
			name: "ASpacedLowerBound",
			src:  "type A<B > : T> = B",
			want: "type A<B> = T",
			errs: []string{"Expected = but got :", "Unexpected token", "Unexpected token", "Unexpected token", "Unexpected token"},
		},
		{
			// A conditional type's check type may end in `>` right before its `:`.
			name: "AConditionalTypeCheckingAGenericType",
			src:  "type R<A> = if Foo<A>: Bar { A } else { never }",
			want: "type R<A> = if Foo<A> : Bar { A } else { never }",
		},
		{
			name: "ATypeArgumentListBeforeAnInitializer",
			src:  "val x: Array<number>= [1]",
			want: "val x: Array<number> = [1]",
		},
		{
			name: "ATypeParameterListBeforeAnAliasBody",
			src:  "type A<T>= Array<T>",
			want: "type A<T> = Array<T>",
		},
		{name: "AGreaterThanOrEqualComparison", src: "val c = a >= b"},
		{
			name: "ASpacedGreaterThanOrEqual",
			src:  "val c = a > = b",
			want: "val c = a > b",
			errs: []string{"Unexpected token, '='"},
		},
		{name: "AComparisonWithARegex", src: "val c = x >= /re/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errs := parseAndPrint(t, tt.src)
			require.Equal(t, tt.errs, errs)
			want := tt.want
			if want == "" {
				want = tt.src
			}
			require.Equal(t, want, got)
		})
	}
}

// TestJSXTextStartingWithAColon asserts that JSX text right after a tag's `>` still lexes as
// text when it starts with `:`.
func TestJSXTextStartingWithAColon(t *testing.T) {
	decls, errs := ParseDecls(context.Background(), &ast.Source{ID: 0, Path: "t.esc", Contents: "val e = <b>: hello</b>"})
	require.Empty(t, errs)
	require.Len(t, decls, 1)
	decl, ok := decls[0].(*ast.VarDecl)
	require.True(t, ok)
	elem, ok := decl.Init.(*ast.JSXElementExpr)
	require.True(t, ok)
	require.Len(t, elem.Children, 1)
	text, ok := elem.Children[0].(*ast.JSXText)
	require.True(t, ok)
	require.Equal(t, ": hello", text.Value)
}
