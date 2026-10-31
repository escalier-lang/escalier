package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferBlock types a block's statements in source order and returns the block's
// VALUE — the type of its last statement, or `undefined` for an empty block —
// together with whether the block DIVERGES (always transfers control out before
// reaching its tail, so it completes no value). The block runs in the scope it is
// given — the caller establishes it (inferFunc passes the param scope, so
// body-level val/var redeclarations overwrite alongside the params, per §3.2).
// soltype.UndefinedType is the result of a block that ends in a declaration or a
// value-free statement, which is the value such a block evaluates to at runtime.
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
// (mirroring the old checker's inferFuncBody), so `fn f() { 5 }` returns
// `undefined` while `fn f() { return 5 }` returns `5` (the operand, collected
// and joined).
// Value-position consumers still use the tail: `val x = if c { return 5 } else
// { 6 }` sees the cons branch diverge and binds `x : 6`.
func (c *checker) inferBlock(scope *Scope, lvl int, b *ast.Block) (soltype.Type, bool) {
	var result soltype.Type = &soltype.UndefinedType{}
	for _, s := range b.Stmts {
		result = c.inferStmt(scope, lvl, s)
	}
	return result, blockDiverges(b)
}

// inferStmt types a single statement and returns the value it contributes to
// the enclosing block, which is `undefined` for a declaration and for a bare return
// with no operand. Body-level declarations are VarDecl-only: a DeclStmt wrapping any
// other decl kind is a permanent BodyDeclNotAllowedError (§3.2), not the
// temporary subset gate. Each val/var introduces a fresh, independent binding
// and overwrites the name's binding in the current scope, so redeclaration rebinds
// without constraining the old and new types together.
func (c *checker) inferStmt(scope *Scope, lvl int, s ast.Stmt) soltype.Type {
	c.markLifetimes(s)
	// M4 G1: record the enclosing statement so a reassignment in expression position
	// can find its CFG StmtRef for transition checking. A no-op outside a function
	// body (c.fn == nil), where no liveness analysis ran.
	if c.fn != nil {
		c.fn.currentStmt = s
		// A statement inside an expression the CFG builder did not decompose has no CFG point
		// of its own, so the ownership events inside it attach to the enclosing statement's.
		if _, ok := c.fn.stmtToRef[s]; ok {
			fn, saved := c.fn, c.fn.eventStmt
			fn.eventStmt = s
			defer func() { fn.eventStmt = saved }()
		}
	}
	switch s := s.(type) {
	case *ast.ExprStmt:
		return c.inferExpr(scope, lvl, s.Expr)
	case *ast.ForInStmt:
		return c.inferForIn(scope, lvl, s)
	case *ast.ReturnStmt:
		// A return contributes both as the block's tail value (consumed only by
		// value-position blocks; inferFunc discards the tail) AND as one of the
		// enclosing function's return points. Bare `return` contributes `undefined` in
		// both roles.
		var t soltype.Type = &soltype.UndefinedType{}
		if s.Expr != nil {
			t = c.inferExpr(scope, lvl, s.Expr)
		}
		if c.fn != nil {
			c.fn.returns = append(c.fn.returns, t)
			c.fn.returnExprs = append(c.fn.returnExprs, s.Expr)
			// Returning an owned value moves it out of the call frame, so the source
			// binding is consumed and a later use is a use-after-move. A returned borrow
			// flows out at its own lifetime and is not consumed here.
			if s.Expr != nil {
				if ref, ok := c.fn.stmtToRef[s]; ok {
					c.emitReturnFlow(s.Expr, ref)
					// A value that fills a borrow return without a written borrow is reported for
					// that alone, and records no move.
					// With no return annotation the function returns whatever s.Expr produces, so
					// the check runs against a nil destination type. An annotation that does not
					// resolve, or that nests the returned type, names no destination to check.
					checked := c.fn.returnAnn != nil || !c.fn.returnAnnotated
					missing := checked && c.checkWrittenBorrow(s.Expr, c.fn.returnAnn)
					if !missing && !c.rejectModuleLevelMove(scope, s.Expr) {
						c.consumeOwned(s.Expr, t, s.Expr, ref)
					}
					// A returned value must not borrow a function-local, which dies when the
					// frame returns and would leave the borrow dangling, unless it owns a
					// self-contained component the move re-anchors. checkReturnEscape records
					// the return for the post-pass to decide.
					c.checkReturnEscape(s.Expr, ref)
				}
			}
		} else {
			// A `return` reached outside any function body — e.g. inside an `if` that
			// is part of a top-level `val` initializer (`val x = if c { return 1 }
			// else { 2 }`). Reject it by the walk, symmetric to AwaitOutsideAsyncError,
			// rather than silently dropping the return point.
			c.report(&ReturnOutsideFunctionError{Return: s})
		}
		return t
	case *ast.DeclStmt:
		// A script (bin/) walks its top-level statements in source order, so a class, enum,
		// alias or function declaration binds where it appears — used before its declaration
		// it is an unknown identifier, unlike the module dep-graph path's out-of-order
		// resolution. A real function body still rejects each of them, so all four are gated
		// on the script context (a funcCtx with a nil node; see InferScript). An alias and a
		// function declaration are further gated on the script's root scope. A script's top-level
		// declarations live for the whole run, so the class registry's insert/overwrite
		// invariant holds exactly as it does for a module's top-level decls.
		switch decl := s.Decl.(type) {
		case *ast.EnumDecl:
			if c.inScript() {
				// preBindEnum binds the enum's union type first so a self-recursive variant
				// resolves, then inferEnumBody builds the constructors.
				c.inferEnumBody(c.preBindEnum(scope, lvl+1, decl, ""))
				return &soltype.UndefinedType{}
			}
			c.report(&BodyDeclNotAllowedError{Decl: s.Decl})
			return &soltype.UndefinedType{}
		case *ast.ClassDecl:
			if c.inScript() {
				c.bindScriptClass(scope, lvl, decl)
				return &soltype.UndefinedType{}
			}
			c.report(&BodyDeclNotAllowedError{Decl: s.Decl})
			return &soltype.UndefinedType{}
		case *ast.TypeDecl:
			// An alias is registered under its name for the whole run, so one declared in a
			// nested block would outlive the block. Only the top level binds one.
			if c.atScriptRoot(scope) {
				c.bindScriptAlias(scope, lvl, decl)
				return &soltype.UndefinedType{}
			}
			c.report(&BodyDeclNotAllowedError{Decl: s.Decl})
			return &soltype.UndefinedType{}
		case *ast.FuncDecl:
			// A function declaration binds only at a script's top level. A nested one is
			// rejected, in a script's blocks and in every function body.
			if c.atScriptRoot(scope) {
				switch {
				case decl.Declare():
					c.bindScriptFunc(scope, lvl, decl)
					return &soltype.UndefinedType{}
				case decl.Body != nil:
					c.bindScriptFuncWithBody(scope, lvl, s, decl)
					return &soltype.UndefinedType{}
				}
			}
			c.report(&BodyDeclNotAllowedError{Decl: s.Decl})
			return &soltype.UndefinedType{}
		}
		vd, ok := s.Decl.(*ast.VarDecl)
		if !ok {
			c.report(&BodyDeclNotAllowedError{Decl: s.Decl})
			return &soltype.UndefinedType{}
		}
		// A `let`-`else` binding is refutable: its pattern narrows the initializer and
		// its `else` runs on a failed match, either diverging or supplying a fallback.
		// inferValElse binds the pattern's names into this scope for the rest of the
		// block, so it takes over from the ordinary irrefutable `val`/`var` paths below.
		if vd.Else != nil {
			c.inferValElse(scope, lvl, vd)
			return &soltype.UndefinedType{}
		}
		name, named := varName(vd)
		if !named {
			// A nil pattern (hand-built AST; the parser synthesizes a placeholder)
			// blames the decl, mirroring inferFunc — never a nil-node Span() panic.
			if vd.Pattern == nil {
				c.reportUnsupported(vd)
				return &soltype.UndefinedType{}
			}
			// M4 E1: a destructuring `val`/`var` such as `{x, y} = …` or `[a, b] = …`.
			// Type the initializer, then bind the pattern's leaves against it as
			// monomorphic projections.
			c.inferDestructureDecl(scope, lvl, vd)
			if c.fn != nil {
				moduleLevel := c.rejectModuleLevelDestructure(scope, vd.Pattern, vd.Init)
				if ref, ok := c.fn.stmtToRef[s]; ok {
					if !moduleLevel {
						c.consumeDestructureLeaves(scope, vd.Pattern, vd.Init, ref)
					}
					c.emitPatternFlows(vd.Pattern, vd.Init, ref)
					c.flushBorrowDirty(ref)
				}
			}
			return &soltype.UndefinedType{}
		}
		// Unlike the module driver (inferComponent), a body-level redeclaration is
		// allowed and overwrites the name's binding (§3.2). inferVarDecl reports a
		// missing initializer itself and returns ok=false; bind only when it
		// produced a type.
		if b, ok := c.inferVarDecl(scope, lvl, vd, nil); ok {
			c.defineLocal(scope, name, vd, b, s)
		}
		return &soltype.UndefinedType{}
	default:
		return c.reportUnsupported(s)
	}
}

// defineLocal binds name to b in scope for the single-name `val`/`var` vd written at
// statement s, overwriting any earlier binding of name. Inside a body it copies the
// VarID the rename pass gave vd's pattern onto b, then records the aliases, consumes,
// borrow edges, and loans the declaration creates. A later closure capture of the
// binding resolves to its alias set through that VarID.
func (c *checker) defineLocal(scope *Scope, name string, vd *ast.VarDecl, b ValueBinding, s ast.Stmt) {
	if ip, ok := vd.Pattern.(*ast.IdentPat); ok && c.varIDs.Of(ip) > 0 {
		b.VarID = int(c.varIDs.Of(ip))
	}
	scope.defineValue(name, b)
	if c.fn == nil {
		return
	}
	if ref, ok := c.fn.stmtToRef[s]; ok {
		c.emitBindingFlow(vd, liveness.VarID(b.VarID), bindingType(b), ref)
	}
	if c.fn.unwrittenBorrows.Contains(vd.Init) {
		return
	}
	c.trackAliasesForVarDecl(scope, vd, bindingType(b), s)
	c.consumeBindingInit(vd, bindingType(b), s)
	// Record which function-locals this binding's initializer borrows, so a later
	// flow-out of the binding can find a borrow of a local that would escape the frame.
	c.recordBorrowEdges(liveness.VarID(b.VarID), vd.Init)
	if ref, ok := c.fn.stmtToRef[s]; ok {
		c.flushBorrowDirty(ref)
		// An initializer that borrows a place binds a loan of it to this binding, which
		// then has to stay exclusive for as long as the binding is read.
		c.recordBorrowLoan(liveness.VarID(b.VarID), vd.Init, ref)
	}
	// A value that keeps a closure keeps it reachable while this binding is live.
	c.noteRetainedHolder(liveness.VarID(b.VarID), vd.Init)
}
