package checker

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
)

func (c *Checker) InferScript(ctx Context, m *ast.Script) (*Scope, []Error) {
	errors := []Error{}
	ctx = ctx.WithNewScope()

	for _, stmt := range m.Stmts {
		switch stmt := stmt.(type) {
		case *ast.DeclStmt:
			declErrors := c.inferDecl(ctx, stmt.Decl)
			errors = slices.Concat(errors, declErrors)
		case *ast.ExprStmt:
			_, exprErrors := c.inferExpr(ctx, stmt.Expr)
			errors = slices.Concat(errors, exprErrors)
		case *ast.ReturnStmt:
			panic("TODO: infer return statement")
		}
	}

	return ctx.Scope, errors
}
