package liveness

import "github.com/escalier-lang/escalier/internal/ast"

// BlockResultExpr returns the result expression of a block, which is the
// expression of its last statement when that statement is an ExprStmt. It
// returns nil if the block is empty or ends with a non-expression statement.
func BlockResultExpr(b ast.Block) ast.Expr {
	if len(b.Stmts) == 0 {
		return nil
	}
	if exprStmt, ok := b.Stmts[len(b.Stmts)-1].(*ast.ExprStmt); ok {
		return exprStmt.Expr
	}
	return nil
}

// BlockOrExprResultExpr returns the result expression from a BlockOrExpr.
func BlockOrExprResultExpr(boe *ast.BlockOrExpr) ast.Expr {
	if boe == nil {
		return nil
	}
	if boe.Expr != nil {
		return boe.Expr
	}
	if boe.Block != nil {
		return BlockResultExpr(*boe.Block)
	}
	return nil
}

// BranchResults returns the result expression of each branch of e, and whether e is an
// expression that branches. Those are a conditional, an `if val`, a `match`, a `do`, and a
// `try`. A `do` has one branch, its body. A `try` has its body and each `catch` case.
//
// An entry is nil for a branch whose block ends in something other than an expression. A
// conditional or `if val` without an `else` yields only its consequent. The branches are not
// descended into, so a branch that itself branches is returned whole.
func BranchResults(e ast.Expr) ([]ast.Expr, bool) {
	switch e := e.(type) {
	case *ast.IfElseExpr:
		return condResults(e.Cons, e.Alt), true
	case *ast.IfValExpr:
		return condResults(e.Cons, e.Alt), true
	case *ast.MatchExpr:
		return caseResults(e.Cases), true
	case *ast.DoExpr:
		return []ast.Expr{BlockResultExpr(e.Body)}, true
	case *ast.TryCatchExpr:
		return append([]ast.Expr{BlockResultExpr(e.Try)}, caseResults(e.Catch)...), true
	}
	return nil, false
}

// condResults returns the result expressions of a conditional's consequent cons and its
// `else` alt, leaving alt out when it is nil.
func condResults(cons ast.Block, alt *ast.BlockOrExpr) []ast.Expr {
	results := []ast.Expr{BlockResultExpr(cons)}
	if alt != nil {
		results = append(results, BlockOrExprResultExpr(alt))
	}
	return results
}

// caseResults returns the result expression of each case body in cases.
func caseResults(cases []*ast.MatchCase) []ast.Expr {
	results := make([]ast.Expr, 0, len(cases))
	for _, mc := range cases {
		results = append(results, BlockOrExprResultExpr(&mc.Body))
	}
	return results
}
