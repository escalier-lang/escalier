package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferBlock types a block's statements in source order and returns the block's
// value: the type of its last statement, or void for an empty block. The block
// runs in the scope it is given — the caller establishes it (inferFunc passes
// the param scope, so body-level val/var redeclarations overwrite alongside the
// params, per §3.2). soltype.Void is the result of a block that ends in a
// declaration or a value-free statement.
func (c *checker) inferBlock(scope *Scope, lvl int, b *ast.Block) soltype.Type {
	var result soltype.Type = &soltype.Void{}
	for _, s := range b.Stmts {
		result = c.inferStmt(scope, lvl, s)
	}
	return result
}

// inferStmt types a single statement and returns the value it contributes to
// the enclosing block (void for declarations and bare returns without an
// operand). Body-level declarations are VarDecl-only: a DeclStmt wrapping any
// other decl kind is a permanent BodyDeclNotAllowedError (§3.2), not the
// temporary subset gate. Each val/var introduces a fresh, independent binding
// and overwrites the name's slot in the current scope, so redeclaration rebinds
// without constraining the old and new types together.
func (c *checker) inferStmt(scope *Scope, lvl int, s ast.Stmt) soltype.Type {
	switch s := s.(type) {
	case *ast.ExprStmt:
		return c.inferExpr(scope, lvl, s.Expr)
	case *ast.ReturnStmt:
		if s.Expr == nil {
			return &soltype.Void{}
		}
		return c.inferExpr(scope, lvl, s.Expr)
	case *ast.DeclStmt:
		vd, ok := s.Decl.(*ast.VarDecl)
		if !ok {
			c.report(&BodyDeclNotAllowedError{
				errSpan: errSpan{span: s.Span()},
				Kind:    declKind(s.Decl),
			})
			return &soltype.Void{}
		}
		name, ok := identPatName(vd.Pattern)
		if !ok {
			c.report(&UnsupportedNodeError{
				errSpan: errSpan{span: vd.Span()},
				Kind:    patKind(vd.Pattern),
			})
			return &soltype.Void{}
		}
		b := c.inferVarDecl(scope, lvl, vd)
		scope.defineValue(name, b) // overwrite ⇒ same-name redeclaration rebinds
		return &soltype.Void{}
	default:
		return c.report(&UnsupportedNodeError{
			errSpan: errSpan{span: s.Span()},
			Kind:    stmtKind(s),
		})
	}
}

// stmtKind/declKind name a statement or declaration node for the subset-guard
// and body-decl error messages.
func stmtKind(s ast.Stmt) string { return astKind(s) }
func declKind(d ast.Decl) string { return astKind(d) }
