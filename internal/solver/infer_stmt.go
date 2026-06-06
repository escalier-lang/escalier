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
//
// TODO(M3): a non-tail ReturnStmt only contributes the last statement's type to
// the block value, so an early return (`{ return X; Y }`) is dropped and never
// checked against the declared return type. Harmless at the M2 bar — there is no
// IfElseExpr (control flow), so an early return cannot arise from a real branch —
// but once M3 adds conditionals the walk must collect every return-point type and
// join it with the tail before constraining against the annotation. Tracked in
// planning/simple_sub/01-milestones.md (M3).
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
