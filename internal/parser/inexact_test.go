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

// A trailing `...` is only meaningful on a union of two or more members. After a
// single member, or after a higher-precedence operator that leaves a non-union at
// the top, no UnionTypeAnn carries the flag, so the parser reports an error rather
// than silently dropping the marker and reducing the annotation to the bare member.
func TestParseInexactUnionMarkerRequiresUnion(t *testing.T) {
	ctx := context.Background()
	for _, src := range []string{"number | ...", "number & string | ..."} {
		_, errs := ParseTypeAnn(ctx, src)
		require.Len(t, errs, 1, "input %q", src)
		require.Equal(t, "a trailing `...` must follow two or more union members, as in `A | B | ...`", errs[0].Message)
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

	t.Run("bare union is exact", func(t *testing.T) {
		ta, errs := ParseTypeAnn(ctx, "number | string")
		require.Empty(t, errs)
		u, ok := ta.(*ast.UnionTypeAnn)
		require.True(t, ok)
		require.False(t, u.Inexact)
	})
}

// A `...R` tail bounds the open tail: `A | ...string` draws its unknown members from
// string. The bound is parsed as a primary type and stored on TailBound, and it makes a
// tail meaningful even after a single member, which the bare `...` marker rejects.
func TestParseBoundedUnionTail(t *testing.T) {
	ctx := context.Background()

	t.Run("bound after several members", func(t *testing.T) {
		ta, errs := ParseTypeAnn(ctx, `"a" | "b" | ...string`)
		require.Empty(t, errs)
		u, ok := ta.(*ast.UnionTypeAnn)
		require.True(t, ok)
		require.True(t, u.Inexact)
		require.Len(t, u.Types, 2)
		_, ok = u.TailBound.(*ast.StringTypeAnn)
		require.True(t, ok)
	})

	t.Run("bound after a single member wraps in a one-member union", func(t *testing.T) {
		ta, errs := ParseTypeAnn(ctx, `"a" | ...string`)
		require.Empty(t, errs)
		u, ok := ta.(*ast.UnionTypeAnn)
		require.True(t, ok)
		require.True(t, u.Inexact)
		require.Len(t, u.Types, 1)
		require.NotNil(t, u.TailBound)
	})

	t.Run("a parenthesized bound is a full type", func(t *testing.T) {
		ta, errs := ParseTypeAnn(ctx, `"a" | ...(string | number)`)
		require.Empty(t, errs)
		u, ok := ta.(*ast.UnionTypeAnn)
		require.True(t, ok)
		require.True(t, u.Inexact)
		_, ok = u.TailBound.(*ast.UnionTypeAnn)
		require.True(t, ok)
	})

	t.Run("an unbounded tail leaves TailBound nil", func(t *testing.T) {
		ta, errs := ParseTypeAnn(ctx, `"a" | "b" | ...`)
		require.Empty(t, errs)
		u, ok := ta.(*ast.UnionTypeAnn)
		require.True(t, ok)
		require.True(t, u.Inexact)
		require.Nil(t, u.TailBound)
	})

	t.Run("a single member with no bound is still an error", func(t *testing.T) {
		_, errs := ParseTypeAnn(ctx, `number | ...`)
		require.Len(t, errs, 1)
		require.Equal(t, "a trailing `...` must follow two or more union members, as in `A | B | ...`", errs[0].Message)
	})
}
