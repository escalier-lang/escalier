package parser

import (
	"context"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// The provisional `open` parameter marker (M4 B2) parses as a per-param flag when
// it precedes a parameter pattern, and is treated as an ordinary identifier
// otherwise (a param literally named `open`, an annotated `open: T`, or a default
// `open = e`). It never becomes a separate param.
func TestParseOpenParamMarker(t *testing.T) {
	ctx := context.Background()

	paramsOf := func(t *testing.T, src string) []*ast.Param {
		decls, errs := ParseDecls(ctx, &ast.Source{ID: 0, Path: "t.esc", Contents: src})
		require.Empty(t, errs)
		fd, ok := decls[0].(*ast.FuncDecl)
		require.True(t, ok)
		return fd.Params
	}

	identName := func(t *testing.T, p *ast.Param) string {
		ip, ok := p.Pattern.(*ast.IdentPat)
		require.True(t, ok)
		return ip.Name
	}

	t.Run("open before a pattern marks the param", func(t *testing.T) {
		params := paramsOf(t, "fn dist(open p) { p }")
		require.Len(t, params, 1)
		require.True(t, params[0].Open)
		require.Equal(t, "p", identName(t, params[0]))
	})

	t.Run("open before mut pattern marks the param", func(t *testing.T) {
		params := paramsOf(t, "fn f(open mut p) { p }")
		require.Len(t, params, 1)
		require.True(t, params[0].Open)
		ip, ok := params[0].Pattern.(*ast.IdentPat)
		require.True(t, ok)
		require.True(t, ip.Mutable)
		require.Equal(t, "p", ip.Name)
	})

	t.Run("open alone is an ordinary param name", func(t *testing.T) {
		params := paramsOf(t, "fn f(open) { open }")
		require.Len(t, params, 1)
		require.False(t, params[0].Open)
		require.Equal(t, "open", identName(t, params[0]))
	})

	t.Run("annotated open is an ordinary param name", func(t *testing.T) {
		params := paramsOf(t, "fn f(open: number) { open }")
		require.Len(t, params, 1)
		require.False(t, params[0].Open)
		require.Equal(t, "open", identName(t, params[0]))
	})

	t.Run("open with a peer param", func(t *testing.T) {
		params := paramsOf(t, "fn f(open p, q) { p }")
		require.Len(t, params, 2)
		require.True(t, params[0].Open)
		require.Equal(t, "p", identName(t, params[0]))
		require.False(t, params[1].Open)
		require.Equal(t, "q", identName(t, params[1]))
	})
}
