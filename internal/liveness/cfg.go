package liveness

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// BasicBlock represents a maximal sequence of statements with no internal
// branching. Control flow edges connect blocks.
type BasicBlock struct {
	ID           int
	Stmts        []ast.Stmt
	Successors   []*BasicBlock
	Predecessors []*BasicBlock
	// ExtraDefs lists variables defined in this block that aren't captured
	// by Stmts — for example, loop variable bindings from ForInStmt
	// patterns, IfLetExpr pattern bindings, or MatchExpr arm patterns.
	ExtraDefs []VarID
}

// CFG represents the control flow graph for a function body.
type CFG struct {
	Entry  *BasicBlock
	Exit   *BasicBlock
	Blocks []*BasicBlock
}

// BuildCFG constructs a control flow graph from a function body.
// Each basic block contains a sequence of statements with no internal
// branching. Control flow constructs (if/else, match, for-in, etc.) are
// decomposed into separate blocks connected by edges.
func BuildCFG(body ast.Block) *CFG {
	b := &cfgBuilder{}
	entry := b.newBlock()
	exit := b.newBlock()
	b.exit = exit

	final := b.processStmts(body.Stmts, entry)
	if final != nil {
		b.addEdge(final, exit)
	}

	return &CFG{Entry: entry, Exit: exit, Blocks: b.blocks}
}

// cfgBuilder accumulates basic blocks while walking the AST.
type cfgBuilder struct {
	blocks []*BasicBlock
	exit   *BasicBlock
}

func (b *cfgBuilder) newBlock() *BasicBlock {
	bb := &BasicBlock{ID: len(b.blocks)}
	b.blocks = append(b.blocks, bb)
	return bb
}

func (b *cfgBuilder) addEdge(from, to *BasicBlock) {
	from.Successors = append(from.Successors, to)
	to.Predecessors = append(to.Predecessors, from)
}

// processStmts walks a sequence of statements, building basic blocks.
// Returns the current block after processing, or nil if the path terminated
// (e.g., via return or throw).
func (b *cfgBuilder) processStmts(stmts []ast.Stmt, current *BasicBlock) *BasicBlock {
	for _, stmt := range stmts {
		if current == nil {
			break // unreachable code after return/throw — TODO: #486 emit dead code warnings
		}
		current = b.processStmt(stmt, current)
	}
	return current
}

// processStmt processes a single statement and returns the current block
// (which may be a new block if the statement introduced control flow),
// or nil if the path terminated.
func (b *cfgBuilder) processStmt(stmt ast.Stmt, current *BasicBlock) *BasicBlock {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		current.Stmts = append(current.Stmts, stmt)
		b.addEdge(current, b.exit)
		return nil

	case *ast.ForInStmt:
		return b.processForIn(s, current)

	case *ast.ExprStmt:
		// Throw terminates the path.
		if _, ok := s.Expr.(*ast.ThrowExpr); ok {
			current.Stmts = append(current.Stmts, stmt)
			b.addEdge(current, b.exit)
			return nil
		}
		return b.processExprStmt(s, current)

	case *ast.DeclStmt:
		return b.processDeclStmt(s, current)

	default:
		current.Stmts = append(current.Stmts, stmt)
		return current
	}
}

// processExprStmt handles an expression statement, decomposing it if it
// contains a branching expression (if/else, match, do, try/catch).
func (b *cfgBuilder) processExprStmt(s *ast.ExprStmt, current *BasicBlock) *BasicBlock {
	// Direct branching expression.
	if join := b.decomposeBranch(s.Expr, nil, current); join != nil {
		return join
	}

	// Assignment with branching RHS: x = if cond { ... } else { ... }
	if be, ok := s.Expr.(*ast.BinaryExpr); ok && be.Op == ast.Assign {
		if join := b.processAssignBranch(be, current); join != nil {
			return join
		}
	}

	current.Stmts = append(current.Stmts, s)
	return current
}

// processAssignBranch handles assignments where the RHS is a branching
// expression. Returns the join block, or nil if the RHS is not branching.
func (b *cfgBuilder) processAssignBranch(be *ast.BinaryExpr, current *BasicBlock) *BasicBlock {
	// Early return if RHS is not a branching expression, before any side
	// effects on current.Stmts.
	switch be.Right.(type) {
	case *ast.IfElseExpr, *ast.IfLetExpr, *ast.MatchExpr, *ast.DoExpr, *ast.TryCatchExpr:
		// RHS is branching, continue processing below.
	default:
		return nil
	}

	// Determine join defs from the assignment target.
	// Only track local variables (VarID > 0). Non-local idents (VarID < 0)
	// are ignored by liveness analysis, so they don't need joinDefs.
	var joinDefs []VarID
	if ident, ok := be.Left.(*ast.IdentExpr); ok && ident.VarID > 0 {
		joinDefs = []VarID{VarID(ident.VarID)}
	} else {
		// For member/index access, the LHS is evaluated before branching
		// (e.g., obj and idx in obj[idx] = if ...). Add it as a synthetic
		// ExprStmt so CollectUses captures all uses within the LHS.
		switch be.Left.(type) {
		case *ast.MemberExpr, *ast.IndexExpr:
			current.Stmts = append(current.Stmts,
				ast.NewExprStmt(be.Left, be.Left.Span()))
		}
	}

	return b.decomposeBranch(be.Right, joinDefs, current)
}

// processDeclStmt handles a declaration statement, decomposing it if the
// initializer is a branching expression.
func (b *cfgBuilder) processDeclStmt(s *ast.DeclStmt, current *BasicBlock) *BasicBlock {
	vd, ok := s.Decl.(*ast.VarDecl)
	if !ok || vd.Init == nil {
		current.Stmts = append(current.Stmts, s)
		return current
	}

	joinDefs := collectPatVarDefs(vd.Pattern)
	if join := b.decomposeBranch(vd.Init, joinDefs, current); join != nil {
		return join
	}

	current.Stmts = append(current.Stmts, s)
	return current
}

// decomposeBranch decomposes a branching expression into separate basic
// blocks. The sub-expression evaluated before branching (e.g., the condition
// of an if/else or the target of a match) is wrapped in a synthetic ExprStmt
// and added to the current block so that CollectUses can find variable uses
// in it. Returns the join block, or nil if the expression is not branching.
//
// joinDefs lists variables defined at the join point after the branches
// converge. For example, in:
//
//	val x = if cond { a } else { b }
//
// joinDefs would be [x], because x is defined once the if/else completes.
// The join block records these as ExtraDefs so liveness knows x is defined
// there, even though no statement in the join block explicitly assigns it.
func (b *cfgBuilder) decomposeBranch(expr ast.Expr, joinDefs []VarID, current *BasicBlock) *BasicBlock {
	switch e := expr.(type) {
	case *ast.IfElseExpr:
		current.Stmts = append(current.Stmts, ast.NewExprStmt(e.Cond, e.Cond.Span()))
		return b.processIfElse(e, joinDefs, current)
	case *ast.IfLetExpr:
		current.Stmts = append(current.Stmts, ast.NewExprStmt(e.Target, e.Target.Span()))
		return b.processIfLet(e, joinDefs, current)
	case *ast.MatchExpr:
		current.Stmts = append(current.Stmts, ast.NewExprStmt(e.Target, e.Target.Span()))
		return b.processMatch(e, joinDefs, current)
	case *ast.DoExpr:
		return b.processDo(e, joinDefs, current)
	case *ast.TryCatchExpr:
		return b.processTryCatch(e, joinDefs, current)
	default:
		return nil
	}
}

// processIfElse decomposes an if-else expression into branch blocks.
// joinDefs are variable definitions that belong in the join block
// (e.g., from a wrapping VarDecl pattern).
func (b *cfgBuilder) processIfElse(e *ast.IfElseExpr, joinDefs []VarID, current *BasicBlock) *BasicBlock {
	join := b.newBlock()
	join.ExtraDefs = joinDefs

	// Consequent branch.
	consBlock := b.newBlock()
	b.addEdge(current, consBlock)
	consEnd := b.processStmts(e.Cons.Stmts, consBlock)
	if consEnd != nil {
		b.addEdge(consEnd, join)
	}

	// Alternative branch.
	if e.Alt != nil {
		altBlock := b.newBlock()
		b.addEdge(current, altBlock)
		altEnd := b.processStmts(blockOrExprToStmts(e.Alt), altBlock)
		if altEnd != nil {
			b.addEdge(altEnd, join)
		}
	} else {
		// No else: fall through directly to join.
		b.addEdge(current, join)
	}

	return join
}

// processIfLet decomposes an if-let expression. The pattern bindings are
// scoped to the consequent branch (added as ExtraDefs on the cons block).
func (b *cfgBuilder) processIfLet(e *ast.IfLetExpr, joinDefs []VarID, current *BasicBlock) *BasicBlock {
	join := b.newBlock()
	join.ExtraDefs = joinDefs

	// Consequent branch with pattern bindings.
	consBlock := b.newBlock()
	consBlock.ExtraDefs = collectPatVarDefs(e.Pattern)
	b.addEdge(current, consBlock)
	consEnd := b.processStmts(e.Cons.Stmts, consBlock)
	if consEnd != nil {
		b.addEdge(consEnd, join)
	}

	// Alternative branch.
	if e.Alt != nil {
		altBlock := b.newBlock()
		b.addEdge(current, altBlock)
		altEnd := b.processStmts(blockOrExprToStmts(e.Alt), altBlock)
		if altEnd != nil {
			b.addEdge(altEnd, join)
		}
	} else {
		b.addEdge(current, join)
	}

	return join
}

// processMatch decomposes a match expression. Each arm gets its own block
// with pattern bindings as ExtraDefs. Guards are added as synthetic
// ExprStmts before the arm body.
func (b *cfgBuilder) processMatch(e *ast.MatchExpr, joinDefs []VarID, current *BasicBlock) *BasicBlock {
	join := b.newBlock()
	join.ExtraDefs = joinDefs

	for _, mc := range e.Cases {
		armBlock := b.newBlock()
		armBlock.ExtraDefs = collectPatVarDefs(mc.Pattern)
		b.addEdge(current, armBlock)

		armStmts := blockOrExprToStmts(&mc.Body)
		if mc.Guard != nil {
			guardStmt := ast.NewExprStmt(mc.Guard, mc.Guard.Span())
			armStmts = append([]ast.Stmt{guardStmt}, armStmts...)
		}

		armEnd := b.processStmts(armStmts, armBlock)
		if armEnd != nil {
			b.addEdge(armEnd, join)
		}
	}

	return join
}

// processDo decomposes a do expression by processing the body statements
// in a new block.
func (b *cfgBuilder) processDo(e *ast.DoExpr, joinDefs []VarID, current *BasicBlock) *BasicBlock {
	bodyBlock := b.newBlock()
	b.addEdge(current, bodyBlock)
	bodyEnd := b.processStmts(e.Body.Stmts, bodyBlock)

	join := b.newBlock()
	join.ExtraDefs = joinDefs
	if bodyEnd != nil {
		b.addEdge(bodyEnd, join)
	}
	return join
}

// processTryCatch decomposes a try-catch expression. The try block can
// edge to the join (normal completion) or to catch blocks (exception).
func (b *cfgBuilder) processTryCatch(e *ast.TryCatchExpr, joinDefs []VarID, current *BasicBlock) *BasicBlock {
	join := b.newBlock()
	join.ExtraDefs = joinDefs

	tryBlock := b.newBlock()
	b.addEdge(current, tryBlock)
	tryBodyStart := len(b.blocks)
	tryEnd := b.processStmts(e.Try.Stmts, tryBlock)
	if tryEnd != nil {
		b.addEdge(tryEnd, join)
	}
	// Capture all blocks created during try body processing. These blocks
	// (from nested if/else, match, etc.) can also throw, so they need
	// edges to catch handlers.
	tryBodyBlocks := b.blocks[tryBodyStart:]

	// Each catch case is a separate block. The try block and all blocks
	// created during try body processing edge to each catch block (any
	// statement in try can throw). This is a conservative approximation:
	// variables defined partway through the try block will appear live in
	// catch blocks even if the throw occurs before the def. This
	// over-approximation is safe for liveness (keeps variables live longer
	// than necessary rather than dropping them too early).
	for _, mc := range e.Catch {
		catchBlock := b.newBlock()
		catchBlock.ExtraDefs = collectPatVarDefs(mc.Pattern)
		b.addEdge(tryBlock, catchBlock)
		for _, tb := range tryBodyBlocks {
			b.addEdge(tb, catchBlock)
		}

		catchStmts := blockOrExprToStmts(&mc.Body)
		if mc.Guard != nil {
			guardStmt := ast.NewExprStmt(mc.Guard, mc.Guard.Span())
			catchStmts = append([]ast.Stmt{guardStmt}, catchStmts...)
		}

		catchEnd := b.processStmts(catchStmts, catchBlock)
		if catchEnd != nil {
			b.addEdge(catchEnd, join)
		}
	}

	return join
}

// processForIn decomposes a for-in loop. The iterable is evaluated once
// in the current block. The header serves as the loop entry/exit point.
// The body block has the loop variable as ExtraDefs and a back edge to
// the header.
func (b *cfgBuilder) processForIn(s *ast.ForInStmt, current *BasicBlock) *BasicBlock {
	// Evaluate iterable before entering the loop.
	current.Stmts = append(current.Stmts, ast.NewExprStmt(s.Iterable, s.Iterable.Span()))

	// Header block: loop entry point (branch: enter body or exit loop).
	header := b.newBlock()
	b.addEdge(current, header)

	// Body block with loop variable bindings.
	bodyBlock := b.newBlock()
	bodyBlock.ExtraDefs = collectPatVarDefs(s.Pattern)
	b.addEdge(header, bodyBlock)

	bodyEnd := b.processStmts(s.Body.Stmts, bodyBlock)
	if bodyEnd != nil {
		b.addEdge(bodyEnd, header) // back edge
	}

	// Post-loop block.
	post := b.newBlock()
	b.addEdge(header, post)

	return post
}

// blockOrExprToStmts converts a BlockOrExpr to a slice of statements.
// If the BlockOrExpr contains a single expression, it is wrapped in an
// ExprStmt.
func blockOrExprToStmts(boe *ast.BlockOrExpr) []ast.Stmt {
	if boe == nil {
		return nil
	}
	if boe.Block != nil {
		return boe.Block.Stmts
	}
	if boe.Expr != nil {
		return []ast.Stmt{ast.NewExprStmt(boe.Expr, boe.Expr.Span())}
	}
	return nil
}

// collectPatVarDefs returns the VarIDs defined by a pattern.
func collectPatVarDefs(pat ast.Pat) []VarID {
	c := &collector{}
	c.collectPatDefs(pat)
	return c.defs
}
