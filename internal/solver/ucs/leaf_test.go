package ucs

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// TestLeavesBelongToBothForms checks that the three leaf types terminate a core term
// and a normalized term alike, which is why there is one set of leaves rather than a
// parallel pair.
func TestLeavesBelongToBothForms(t *testing.T) {
	leaves := []any{
		&BodyLeaf{Body: exprBody(num(1))},
		&EscapeLeaf{},
		&FallbackLeaf{Body: exprBody(num(0))},
	}

	for _, leaf := range leaves {
		require.Implements(t, (*Core)(nil), leaf)
		require.Implements(t, (*Norm)(nil), leaf)
	}
}

func TestBodySpan(t *testing.T) {
	exprSpan := span(54, 59)
	blockSpan := span(81, 92)

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
	origin := At(OriginMatchArm, arm(span(45, 58)))

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
