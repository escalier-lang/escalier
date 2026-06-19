package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferBlock types a block's statements in source order and returns the block's
// VALUE — the type of its last statement, or void for an empty block — together
// with whether the block DIVERGES (always transfers control out before reaching
// its tail, so it completes no value). The block runs in the scope it is given —
// the caller establishes it (inferFunc passes the param scope, so body-level
// val/var redeclarations overwrite alongside the params, per §3.2). soltype.Void
// is the result of a block that ends in a declaration or a value-free statement.
//
// The divergence flag is the single source of truth for "this block produces no
// value": a VALUE-position caller (an if/else branch today; do/match arms when
// they land) drops a diverging block from its branch union — see blockDiverges.
// The flag is computed here, in the one node that knows the block's tail, so
// every present and future block-as-value consumer reads it instead of
// re-deriving divergence syntactically at each call site.
//
// Block-as-value is distinct from FUNCTION-return: a ReturnStmt is one of the
// function's return points whether or not it is the block's last statement —
// inferStmt routes every return into the enclosing funcCtx for inferFunc to
// join into the function's return type. inferFunc IGNORES both the tail and the
// divergence flag: a function body's last expression is NOT an implicit return
// (mirroring the old checker's inferFuncBody), so `fn f() { 5 }` returns void
// while `fn f() { return 5 }` returns `5` (the operand, collected and joined).
// Value-position consumers still use the tail: `val x = if c { return 5 } else
// { 6 }` sees the cons branch diverge and binds `x : 6`.
func (c *checker) inferBlock(scope *Scope, lvl int, b *ast.Block) (soltype.Type, bool) {
	var result soltype.Type = &soltype.Void{}
	for _, s := range b.Stmts {
		result = c.inferStmt(scope, lvl, s)
	}
	return result, blockDiverges(b)
}

// inferStmt types a single statement and returns the value it contributes to
// the enclosing block (void for declarations and bare returns without an
// operand). Body-level declarations are VarDecl-only: a DeclStmt wrapping any
// other decl kind is a permanent BodyDeclNotAllowedError (§3.2), not the
// temporary subset gate. Each val/var introduces a fresh, independent binding
// and overwrites the name's slot in the current scope, so redeclaration rebinds
// without constraining the old and new types together.
func (c *checker) inferStmt(scope *Scope, lvl int, s ast.Stmt) soltype.Type {
	// M4 G1: record the enclosing statement so a reassignment in expression position
	// can find its CFG StmtRef for transition checking. A no-op outside a function
	// body (c.fn == nil), where no liveness analysis ran.
	if c.fn != nil {
		c.fn.currentStmt = s
	}
	switch s := s.(type) {
	case *ast.ExprStmt:
		return c.inferExpr(scope, lvl, s.Expr)
	case *ast.ReturnStmt:
		// A return contributes both as the block's tail value (consumed only by
		// value-position blocks; inferFunc discards the tail) AND as one of the
		// enclosing function's return points. Bare `return` contributes Void in
		// both slots.
		var t soltype.Type = &soltype.Void{}
		if s.Expr != nil {
			t = c.inferExpr(scope, lvl, s.Expr)
		}
		if c.fn != nil {
			c.fn.returns = append(c.fn.returns, t)
		} else {
			// A `return` reached outside any function body — e.g. inside an `if` that
			// is part of a top-level `val` initializer (`val x = if c { return 1 }
			// else { 2 }`). Reject it by the walk, symmetric to AwaitOutsideAsyncError,
			// rather than silently dropping the return point.
			c.report(&ReturnOutsideFunctionError{Return: s})
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
			if vd.Pattern == nil {
				c.reportUnsupported(vd)
				return &soltype.Void{}
			}
			// M4 E1: a destructuring `val`/`var` ({x, y} = …, [a, b] = …). Type the
			// initializer, then bind the pattern's leaves against it as monomorphic
			// projections.
			c.inferDestructureDecl(scope, lvl, vd)
			return &soltype.Void{}
		}
		// Unlike the module driver (inferComponent), a body-level redeclaration is
		// allowed and overwrites the name's slot (§3.2). inferVarDecl reports a
		// missing initializer itself and returns ok=false; bind only when it
		// produced a type.
		if b, ok := c.inferVarDecl(scope, lvl, vd); ok {
			// M4 G1: carry the rename-assigned VarID onto the binding so a later
			// closure capture resolves this body-level binding to its alias set, then
			// track the alias the declaration creates and check its mutability
			// transition. Both are no-ops outside a function body (c.fn == nil).
			if ip, ok := vd.Pattern.(*ast.IdentPat); ok && ip.VarID > 0 {
				b.VarID = ip.VarID
			}
			scope.defineValue(name, b) // overwrite ⇒ same-name redeclaration rebinds
			if c.fn != nil {
				c.trackAliasesForVarDecl(scope, vd, bindingType(b), s)
			}
		}
		return &soltype.Void{}
	default:
		return c.reportUnsupported(s)
	}
}
