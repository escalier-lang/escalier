package parser

import (
	"context"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// TestParseTypeCtorParamArity covers the `F<_>` form of a type parameter, whose arity is the
// number of `_` slots it writes.
func TestParseTypeCtorParamArity(t *testing.T) {
	tests := map[string]struct {
		input string
		want  []int
	}{
		"Unary":  {input: `fn f<F<_>>() {}`, want: []int{1}},
		"Binary": {input: `fn f<F<_, _>, T>() {}`, want: []int{2, 0}},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}
			script, errors := NewParser(context.Background(), source).ParseScript()
			require.Empty(t, errors)
			decl := script.Stmts[0].(*ast.DeclStmt).Decl.(*ast.FuncDecl)
			got := make([]int, len(decl.TypeParams))
			for i, tp := range decl.TypeParams {
				got[i] = tp.Arity()
			}
			require.Equal(t, test.want, got)
		})
	}
}

// TestParseTypeCtorParamErrors asserts that each malformed `F<…>` parameter reports one error.
func TestParseTypeCtorParamErrors(t *testing.T) {
	tests := map[string]struct {
		input   string
		message string
	}{
		"NamedSlot": {
			input:   `fn f<F<T>>() {}`,
			message: "expected `_` in a type constructor parameter",
		},
		"MissingClose": {
			input:   `fn f<F<_ T>>() {}`,
			message: "expected `>` after a type constructor parameter",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}
			_, errors := NewParser(context.Background(), source).ParseScript()
			require.Len(t, errors, 1, "expected exactly one parse error: %#v", errors)
			require.Equal(t, test.message, errors[0].Message)
		})
	}
}

// TestParseTypeCtorSlotVariance covers the variance modifier each `_` slot may carry.
func TestParseTypeCtorSlotVariance(t *testing.T) {
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: `fn f<F<_, out _, in _, in out _>>() {}`}
	script, errors := NewParser(context.Background(), source).ParseScript()
	require.Empty(t, errors)
	decl := script.Stmts[0].(*ast.DeclStmt).Decl.(*ast.FuncDecl)
	require.Equal(t, []ast.VarianceModifier{
		ast.VarianceNone, ast.VarianceOut, ast.VarianceIn, ast.VarianceInOut,
	}, decl.TypeParams[0].CtorSlots)
}
