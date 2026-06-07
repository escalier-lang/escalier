package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferBlock types a block's statements in source order and returns the block's
// VALUE — the type of its last statement, or void for an empty block. The block
// runs in the scope it is given — the caller establishes it (inferFunc passes
// the param scope, so body-level val/var redeclarations overwrite alongside the
// params, per §3.2). soltype.Void is the result of a block that ends in a
// declaration or a value-free statement.
//
// Block-as-value is distinct from FUNCTION-return: a non-tail ReturnStmt is not
// part of the block's tail value, but it IS one of the function's return points
// — inferStmt routes those into the enclosing funcCtx for inferFunc to join with
// the tail (PR3, replacing M2's TODO that dropped non-tail returns).
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
		// PR3: a return contributes both as a candidate block-tail value (kept for
		// continuity with M2's `{ return 5 }` block-as-expression test) AND as one
		// of the enclosing function's return points. Bare `return` contributes Void
		// in both slots. Outside any function (a hand-built / malformed AST — the
		// real walk never enters a return at top-level) the funcCtx is nil and we
		// silently skip collection rather than panicking.
		var t soltype.Type = &soltype.Void{}
		if s.Expr != nil {
			t = c.inferExpr(scope, lvl, s.Expr)
		}
		if c.fn != nil {
			c.fn.returns = append(c.fn.returns, t)
		}
		return t
	case *ast.DeclStmt:
		vd, ok := s.Decl.(*ast.VarDecl)
		if !ok {
			c.report(&BodyDeclNotAllowedError{Decl: s.Decl})
			return &soltype.Void{}
		}
		name, named := varName(vd)
		if !named {
			// A nil pattern (hand-built AST; the parser synthesizes a placeholder)
			// blames the decl, mirroring inferFunc — never a nil-node Span() panic.
			if vd.Pattern != nil {
				c.reportUnsupported(vd.Pattern)
			} else {
				c.reportUnsupported(vd)
			}
			return &soltype.Void{}
		}
		// Unlike the module driver (inferComponent), a body-level redeclaration is
		// allowed and overwrites the name's slot (§3.2). inferVarDecl reports a
		// missing initializer itself and returns ok=false; bind only when it
		// produced a type.
		if b, ok := c.inferVarDecl(scope, lvl, vd); ok {
			scope.defineValue(name, b) // overwrite ⇒ same-name redeclaration rebinds
		}
		return &soltype.Void{}
	default:
		return c.reportUnsupported(s)
	}
}
