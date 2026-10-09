package parser

import (
	"context"
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
	out := ""
	for i, d := range decls {
		printed, err := printer.Print(d, printer.DefaultOptions())
		require.NoError(t, err)
		if i > 0 {
			out += "\n"
		}
		out += printed
	}
	return out, msgs
}

// TestParseAdjacentGreaterThanPairs covers `>:` and `>=` read from two adjacent tokens. The
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
		{name: "ALowerBoundOnAFunction", src: "declare fn f<B >: T>(x: B) -> B"},
		{
			name: "ALowerBoundOnAMethod",
			src: `class Bag<T> {
    contains<B >: T>(&self, x: B) -> boolean {
        return false
    }
}`,
		},
		// A binder writes its lower bound, then its upper bound, then its default.
		{name: "ALowerBoundAnUpperBoundAndADefault", src: "type A<B >: T: Base = D> = B"},
		{name: "ALowerBoundNamingALaterSibling", src: "type A<B >: T, T> = B"},
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

// TestALowerBoundIsCarriedOnTheTypeParam asserts that `>:` fills TypeParam.LowerBound and
// leaves Constraint to the `:` after it.
func TestALowerBoundIsCarriedOnTheTypeParam(t *testing.T) {
	decls, errs := ParseDecls(context.Background(), &ast.Source{ID: 0, Path: "t.esc", Contents: "type A<B >: T: Base> = B"})
	require.Empty(t, errs)
	decl, ok := decls[0].(*ast.TypeDecl)
	require.True(t, ok)
	require.Len(t, decl.TypeParams, 1)
	tp := decl.TypeParams[0]
	lower, err := printer.Print(tp.LowerBound, printer.DefaultOptions())
	require.NoError(t, err)
	require.Equal(t, "T", lower)
	upper, err := printer.Print(tp.Constraint, printer.DefaultOptions())
	require.NoError(t, err)
	require.Equal(t, "Base", upper)
}

// TestJSXTextStartingWithAColon asserts that JSX text right after a tag's `>` still lexes as
// text when it starts with `:`.
func TestJSXTextStartingWithAColon(t *testing.T) {
	decls, errs := ParseDecls(context.Background(), &ast.Source{ID: 0, Path: "t.esc", Contents: "val e = <b>: hello</b>"})
	require.Empty(t, errs)
	decl, ok := decls[0].(*ast.VarDecl)
	require.True(t, ok)
	elem, ok := decl.Init.(*ast.JSXElementExpr)
	require.True(t, ok)
	require.Len(t, elem.Children, 1)
	text, ok := elem.Children[0].(*ast.JSXText)
	require.True(t, ok)
	require.Equal(t, ": hello", text.Value)
}

// TestALowerBoundNamingALaterSiblingSortsAfterIt asserts that SortTypeParamsTopologically
// reads a lower bound, so `B` in `<B >: T, T>` is ordered after the `T` it names.
func TestALowerBoundNamingALaterSiblingSortsAfterIt(t *testing.T) {
	decls, errs := ParseDecls(context.Background(), &ast.Source{ID: 0, Path: "t.esc", Contents: "type A<B >: T, T> = B"})
	require.Empty(t, errs)
	decl, ok := decls[0].(*ast.TypeDecl)
	require.True(t, ok)
	var names []string
	for _, tp := range ast.SortTypeParamsTopologically(decl.TypeParams) {
		names = append(names, tp.Name)
	}
	require.Equal(t, []string{"T", "B"}, names)
}
