package parser

import (
	"context"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// The trailing `...` inexact marker (#677 §4.1) parses on all three function forms
// — declaration, expression, and type annotation — setting Inexact on the node and
// leaving the named params intact. A bare `fn(...)` (no params) is also inexact.
func TestParseInexactMarker(t *testing.T) {
	ctx := context.Background()

	t.Run("func decl", func(t *testing.T) {
		decls, errs := ParseDecls(ctx, &ast.Source{ID: 0, Path: "t.esc", Contents: "fn f(x: number, ...) { x }"})
		require.Empty(t, errs)
		fd, ok := decls[0].(*ast.FuncDecl)
		require.True(t, ok)
		require.True(t, fd.Inexact)
		require.Len(t, fd.Params, 1) // the `...` does not become a param
	})

	t.Run("func expr", func(t *testing.T) {
		decls, errs := ParseDecls(ctx, &ast.Source{ID: 0, Path: "t.esc", Contents: "val g = fn (x: number, ...) { x }"})
		require.Empty(t, errs)
		vd, ok := decls[0].(*ast.VarDecl)
		require.True(t, ok)
		fe, ok := vd.Init.(*ast.FuncExpr)
		require.True(t, ok)
		require.True(t, fe.Inexact)
		require.Len(t, fe.Params, 1)
	})

	t.Run("func type annotation", func(t *testing.T) {
		ta, errs := ParseTypeAnn(ctx, "fn(x: number, ...) -> number")
		require.Empty(t, errs)
		fn, ok := ta.(*ast.FuncTypeAnn)
		require.True(t, ok)
		require.True(t, fn.Inexact)
		require.Len(t, fn.Params, 1)
	})

	t.Run("bare nullary inexact", func(t *testing.T) {
		ta, errs := ParseTypeAnn(ctx, "fn(...) -> number")
		require.Empty(t, errs)
		fn, ok := ta.(*ast.FuncTypeAnn)
		require.True(t, ok)
		require.True(t, fn.Inexact)
		require.Empty(t, fn.Params)
	})

	t.Run("union type annotation", func(t *testing.T) {
		ta, errs := ParseTypeAnn(ctx, "number | string | ...")
		require.Empty(t, errs)
		u, ok := ta.(*ast.UnionTypeAnn)
		require.True(t, ok)
		require.True(t, u.Inexact)
		require.Len(t, u.Types, 2) // the `...` does not become a member
	})
}

// A bare function (no trailing `...`) is exact, and a `...rest` is an ordinary rest
// param — NOT the inexact marker. The lookahead in parseFuncParams must keep these
// distinct.
func TestParseExactAndRestAreNotInexact(t *testing.T) {
	ctx := context.Background()

	t.Run("bare function is exact", func(t *testing.T) {
		decls, errs := ParseDecls(ctx, &ast.Source{ID: 0, Path: "t.esc", Contents: "fn f(x: number, y: number) { x }"})
		require.Empty(t, errs)
		require.False(t, decls[0].(*ast.FuncDecl).Inexact)
	})

	t.Run("rest param is not the inexact marker", func(t *testing.T) {
		decls, errs := ParseDecls(ctx, &ast.Source{ID: 0, Path: "t.esc", Contents: "fn f(x: number, ...rest) { x }"})
		require.Empty(t, errs)
		fd := decls[0].(*ast.FuncDecl)
		require.False(t, fd.Inexact)
		require.Len(t, fd.Params, 2) // x and the rest param
		_, isRest := fd.Params[1].Pattern.(*ast.RestPat)
		require.True(t, isRest)
	})

	t.Run("bare union is exact", func(t *testing.T) {
		ta, errs := ParseTypeAnn(ctx, "number | string")
		require.Empty(t, errs)
		u, ok := ta.(*ast.UnionTypeAnn)
		require.True(t, ok)
		require.False(t, u.Inexact)
	})
}
