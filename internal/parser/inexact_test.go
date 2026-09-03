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
}

// A comment written on either side of the inexact marker still leaves the marker
// where parseFuncParams looks for it. Both reads of the token — the one that finds
// the `...` and the one that checks for the closing paren behind it — skip
// comments, so the list stays inexact and the `...` does not fall through to the
// parameter parser as a malformed rest parameter.
func TestParseInexactMarkerWithComments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		src        string
		wantParams int
	}{
		{
			name:       "before the marker",
			src:        "fn(x: number, /* open */ ...) -> number",
			wantParams: 1,
		},
		{
			name:       "after the marker",
			src:        "fn(x: number, ... /* open */) -> number",
			wantParams: 1,
		},
		{
			name:       "before a nullary marker",
			src:        "fn(/* open */ ...) -> number",
			wantParams: 0,
		},
		{
			name:       "after a nullary marker",
			src:        "fn(... /* open */) -> number",
			wantParams: 0,
		},
		{
			name:       "on both sides of the marker",
			src:        "fn(x: number, /* a */ ... /* b */) -> number",
			wantParams: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ta, errs := ParseTypeAnn(context.Background(), tc.src)
			require.Empty(t, errs)
			fn, ok := ta.(*ast.FuncTypeAnn)
			require.True(t, ok)
			require.True(t, fn.Inexact)
			require.Len(t, fn.Params, tc.wantParams)
		})
	}
}

// Skipping comments while looking for the marker must not swallow a rest
// parameter. The lookahead restores the lexer to the `...` when what follows is
// not the closing paren, so `...rest` still parses as a parameter and the list
// stays exact.
func TestParseRestParamIsNotTheInexactMarker(t *testing.T) {
	t.Parallel()
	ta, errs := ParseTypeAnn(context.Background(), "fn(x: number, ...rest: Array<number>) -> number")
	require.Empty(t, errs)
	fn, ok := ta.(*ast.FuncTypeAnn)
	require.True(t, ok)
	require.False(t, fn.Inexact)
	require.Len(t, fn.Params, 2)
}

// A union is always closed, so a trailing `...` after a `|` is rejected. Openness is written as
// `string`, `number`, or `unknown` in a member instead.
func TestParseUnionTrailingDotsRejected(t *testing.T) {
	ctx := context.Background()
	for _, src := range []string{"number | ...", "number | string | ...", "number & string | ..."} {
		_, errs := ParseTypeAnn(ctx, src)
		require.Len(t, errs, 1, "input %q", src)
		require.Equal(t, "a union cannot have a trailing `...`; write `string`, `number`, or `unknown` for an open set of members", errs[0].Message)
	}
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

	t.Run("bare union parses", func(t *testing.T) {
		ta, errs := ParseTypeAnn(ctx, "number | string")
		require.Empty(t, errs)
		_, ok := ta.(*ast.UnionTypeAnn)
		require.True(t, ok)
	})
}
