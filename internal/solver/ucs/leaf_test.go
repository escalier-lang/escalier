package ucs

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// TestLeavesEndACoreTerm checks that all three leaf types terminate a core branch.
// PR3 adds the normalized form and extends this to cover both.
func TestLeavesEndACoreTerm(t *testing.T) {
	leaves := []any{
		&BodyLeaf{Body: exprBody(num(1))},
		&EscapeLeaf{},
		&FallbackLeaf{Body: exprBody(num(0))},
	}

	for _, leaf := range leaves {
		require.Implements(t, (*Core)(nil), leaf)
	}
}

func TestBodySpan(t *testing.T) {
	exprSpan := span(2, 14, 19)
	blockSpan := span(3, 1, 12)

	found, ok := BodySpan(ast.BlockOrExpr{Expr: ast.NewIdent("x", exprSpan)})
	require.True(t, ok)
	require.Equal(t, exprSpan, found)

	found, ok = BodySpan(ast.BlockOrExpr{Block: &ast.Block{Span: blockSpan}})
	require.True(t, ok)
	require.Equal(t, blockSpan, found)

	_, ok = BodySpan(ast.BlockOrExpr{})
	require.False(t, ok)
}

func TestLeavesCarryProvenance(t *testing.T) {
	origin := At(OriginMatchArm, arm(span(2, 5, 18)))

	terms := map[string]Term{
		"BodyLeaf":     &BodyLeaf{Body: exprBody(num(1)), Origin: origin},
		"EscapeLeaf":   &EscapeLeaf{Origin: origin},
		"FallbackLeaf": &FallbackLeaf{Body: exprBody(num(0)), Origin: origin},
	}

	for name, term := range terms {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, origin, term.Prov())
		})
	}
}
