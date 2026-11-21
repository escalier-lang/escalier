package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestNonNilExprType covers the check inferExpr runs on the type an expression's arm
// produced. A nil type reports ExprWithoutTypeError at the expression and comes back as the
// ErrorType sentinel, which is also what Info records for the expression. A non-nil type
// passes through with nothing reported.
func TestNonNilExprType(t *testing.T) {
	module := parseModule(t, `val x = f(1)`)
	ns, ok := module.Namespaces.Get("")
	require.True(t, ok)
	decl, ok := ns.Decls[0].(*ast.VarDecl)
	require.True(t, ok)
	call := decl.Init

	t.Run("NilReportsAndRecovers", func(t *testing.T) {
		c := newTestChecker()
		got := c.nonNilExprType(call, nil)
		require.IsType(t, &soltype.ErrorType{}, got)
		require.Same(t, got, c.info.TypeOf(call))
		require.Equal(t, []string{
			"1:9-1:13: internal error: the checker inferred no type for this CallExpr; please report this as a bug",
		}, messagesWithSpan(t, c.errs))
	})

	t.Run("NonNilPassesThrough", func(t *testing.T) {
		c := newTestChecker()
		want := soltype.Type(&soltype.PrimType{Prim: soltype.NumPrim})
		require.Same(t, want, c.nonNilExprType(call, want))
		require.Empty(t, c.errs)
	})
}
