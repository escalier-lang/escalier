package liveness

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// findBlockContaining returns the block ID of the block whose statements
// contain the given expression (by pointer identity). It checks direct
// ExprStmt expressions, CallExpr arguments, ReturnStmt expressions, and
// ThrowExpr arguments. Returns -1 if not found.
func findBlockContaining(cfg *CFG, expr ast.Expr) int {
	for _, blk := range cfg.Blocks {
		for _, stmt := range blk.Stmts {
			if es, ok := stmt.(*ast.ExprStmt); ok {
				if es.Expr == expr {
					return blk.ID
				}
				if ce, ok := es.Expr.(*ast.CallExpr); ok {
					for _, arg := range ce.Args {
						if arg == expr {
							return blk.ID
						}
					}
				}
				if te, ok := es.Expr.(*ast.ThrowExpr); ok {
					if te.Arg == expr {
						return blk.ID
					}
				}
			}
			if rs, ok := stmt.(*ast.ReturnStmt); ok {
				if rs.Expr == expr {
					return blk.ID
				}
			}
		}
	}
	return -1
}

// --- CFG Construction Tests ---

func TestCFGStraightLine(t *testing.T) {
	// val x = 1; print(x)
	// Should produce: entry block with 2 stmts → exit
	x := identPat("x")
	xRef := ident("x")
	body := block(
		valDecl(x, numLit(1)),
		exprStmt(call(ident("print"), xRef)),
	)
	Rename(nil, body, map[string]VarID{"print": -1})

	cfg := BuildCFG(body)

	// Entry + exit = 2 blocks
	require.Equal(t, 2, len(cfg.Blocks))
	require.Equal(t, 2, len(cfg.Entry.Stmts))
	require.Equal(t, 1, len(cfg.Entry.Successors))
	require.Equal(t, cfg.Exit, cfg.Entry.Successors[0])
}

func TestCFGIfElse(t *testing.T) {
	// if cond { print(a) } else { print(b) }
	// Should produce: entry(cond) → [cons, alt] → join → exit
	aRef := ident("a")
	bRef := ident("b")
	body := block(
		exprStmt(ast.NewIfElse(
			ident("cond"),
			block(exprStmt(call(ident("print"), aRef))),
			&ast.BlockOrExpr{Block: &ast.Block{
				Stmts: []ast.Stmt{exprStmt(call(ident("print"), bRef))},
				Span:  span(),
			}},
			span(),
		)),
	)
	Rename(nil, body, map[string]VarID{"cond": -1, "print": -1, "a": -2, "b": -3})

	cfg := BuildCFG(body)

	// entry, join, cons, alt, exit = 5 blocks
	require.Equal(t, 5, len(cfg.Blocks))
	// Entry has 2 successors (cons and alt)
	require.Equal(t, 2, len(cfg.Entry.Successors))
}

func TestCFGReturn(t *testing.T) {
	// val x = 1; return x; print(x)
	// return terminates the path; print(x) is unreachable
	x := identPat("x")
	xRef1 := ident("x")
	xRef2 := ident("x")
	body := block(
		valDecl(x, numLit(1)),
		ast.NewReturnStmt(xRef1, span()),
		exprStmt(call(ident("print"), xRef2)),
	)
	Rename(nil, body, map[string]VarID{"print": -1})

	cfg := BuildCFG(body)

	// Entry block has 2 stmts (val x, return x). print(x) is unreachable.
	require.Equal(t, 2, len(cfg.Entry.Stmts))
	// Entry edges to exit (via return)
	require.Equal(t, 1, len(cfg.Entry.Successors))
	require.Equal(t, cfg.Exit, cfg.Entry.Successors[0])
}

func TestCFGForIn(t *testing.T) {
	// for i in items { print(i) }
	// Should produce: entry(iterable) → header → [body, post] → exit
	i := identPat("i")
	iRef := ident("i")
	body := block(
		ast.NewForInStmt(
			i, ident("items"),
			block(exprStmt(call(ident("print"), iRef))),
			false, span(),
		),
	)
	Rename(nil, body, map[string]VarID{"items": -1, "print": -2})

	cfg := BuildCFG(body)

	// entry, header, body, post, exit = 5 blocks
	require.Equal(t, 5, len(cfg.Blocks))
	// Entry has iterable eval stmt and edges to header
	require.Equal(t, 1, len(cfg.Entry.Stmts)) // ExprStmt(items)
	require.Equal(t, 1, len(cfg.Entry.Successors))
	header := cfg.Entry.Successors[0]
	// Header has 2 successors: body and post
	require.Equal(t, 2, len(header.Successors))
}

// --- Liveness with Control Flow Tests ---

func TestLivenessIfElseOneBranchUse(t *testing.T) {
	// val x = 1; val y = 2
	// val z = if cond { x } else { y }
	// x is used only in the then branch, y only in the else branch
	x := identPat("x")
	y := identPat("y")
	z := identPat("z")
	xRef := ident("x")
	yRef := ident("y")

	body := block(
		valDecl(x, numLit(1)),
		valDecl(y, numLit(2)),
		valDecl(z, ast.NewIfElse(
			ident("cond"),
			block(exprStmt(xRef)),
			&ast.BlockOrExpr{Block: &ast.Block{
				Stmts: []ast.Stmt{exprStmt(yRef)},
				Span:  span(),
			}},
			span(),
		)),
	)
	Rename(nil, body, map[string]VarID{"cond": -1})

	cfg := BuildCFG(body)
	info := AnalyzeFunction(cfg)

	xID := VarID(x.VarID)
	yID := VarID(y.VarID)

	consBlockID := findBlockContaining(cfg, xRef)
	altBlockID := findBlockContaining(cfg, yRef)

	// In the cons block: x is live (used), y is dead
	require.True(t, info.LiveBefore[consBlockID][0][xID], "x should be live in cons block")
	require.False(t, info.LiveBefore[consBlockID][0][yID], "y should be dead in cons block")

	// In the alt block: y is live (used), x is dead
	require.True(t, info.LiveBefore[altBlockID][0][yID], "y should be live in alt block")
	require.False(t, info.LiveBefore[altBlockID][0][xID], "x should be dead in alt block")
}

func TestLivenessIfWithoutElse(t *testing.T) {
	// val x = 1
	// if cond { print(x) }
	// print(x)
	// x should be live through both paths (cons and fall-through)
	x := identPat("x")
	xRef1 := ident("x") // inside if
	xRef2 := ident("x") // after if

	body := block(
		valDecl(x, numLit(1)),
		exprStmt(ast.NewIfElse(
			ident("cond"),
			block(exprStmt(call(ident("print"), xRef1))),
			nil, // no else
			span(),
		)),
		exprStmt(call(ident("print"), xRef2)),
	)
	Rename(nil, body, map[string]VarID{"cond": -1, "print": -2})

	cfg := BuildCFG(body)
	info := AnalyzeFunction(cfg)

	xID := VarID(x.VarID)

	// x should be live in the entry block after its definition
	require.True(t, info.LiveAfter[cfg.Entry.ID][0][xID],
		"x should be live after its definition")

	// x should be live in the cons block (used in print(x))
	consBlockID := findBlockContaining(cfg, xRef1)
	require.True(t, info.LiveBefore[consBlockID][0][xID],
		"x should be live in the then branch")
}

func TestLivenessForLoop(t *testing.T) {
	// val x = 1
	// for i in items { print(x) }
	// x is used inside the loop body, so it should be live for the
	// entire duration of the loop.
	x := identPat("x")
	i := identPat("i")
	xRef := ident("x")

	body := block(
		valDecl(x, numLit(1)),
		ast.NewForInStmt(
			i, ident("items"),
			block(exprStmt(call(ident("print"), xRef))),
			false, span(),
		),
	)
	Rename(nil, body, map[string]VarID{"items": -1, "print": -2})

	cfg := BuildCFG(body)
	info := AnalyzeFunction(cfg)

	xID := VarID(x.VarID)

	// x is live after its definition (used in loop body)
	require.True(t, info.LiveAfter[cfg.Entry.ID][0][xID],
		"x should be live after definition (used in loop)")

	// Find the loop body block
	bodyBlockID := findBlockContaining(cfg, xRef)
	require.True(t, info.LiveBefore[bodyBlockID][0][xID],
		"x should be live in loop body")
}

func TestLivenessEarlyReturn(t *testing.T) {
	// val x = 1; val y = 2
	// if cond { return x }
	// print(y)
	// On the returning path, y is dead. On the fall-through, y is live.
	x := identPat("x")
	y := identPat("y")
	xRef := ident("x")
	yRef := ident("y")

	body := block(
		valDecl(x, numLit(1)),
		valDecl(y, numLit(2)),
		exprStmt(ast.NewIfElse(
			ident("cond"),
			block(ast.NewReturnStmt(xRef, span())),
			nil,
			span(),
		)),
		exprStmt(call(ident("print"), yRef)),
	)
	Rename(nil, body, map[string]VarID{"cond": -1, "print": -2})

	cfg := BuildCFG(body)
	info := AnalyzeFunction(cfg)

	xID := VarID(x.VarID)
	yID := VarID(y.VarID)

	consBlockID := findBlockContaining(cfg, xRef)
	joinBlockID := findBlockContaining(cfg, yRef)

	// In the returning branch: x is live (returned), y is dead
	require.True(t, info.LiveBefore[consBlockID][0][xID],
		"x should be live in the returning branch")
	require.False(t, info.LiveBefore[consBlockID][0][yID],
		"y should be dead in the returning branch")

	// In the fall-through path: y is live (used in print)
	require.True(t, info.LiveBefore[joinBlockID][0][yID],
		"y should be live in the fall-through path")
}

func TestLivenessThrow(t *testing.T) {
	// val x = 1; val y = 2
	// if cond { throw x }
	// print(y)
	// Same as early return: y is dead on the throwing path.
	x := identPat("x")
	y := identPat("y")
	xRef := ident("x")
	yRef := ident("y")

	body := block(
		valDecl(x, numLit(1)),
		valDecl(y, numLit(2)),
		exprStmt(ast.NewIfElse(
			ident("cond"),
			block(exprStmt(ast.NewThrow(xRef, span()))),
			nil,
			span(),
		)),
		exprStmt(call(ident("print"), yRef)),
	)
	Rename(nil, body, map[string]VarID{"cond": -1, "print": -2})

	cfg := BuildCFG(body)
	info := AnalyzeFunction(cfg)

	yID := VarID(y.VarID)

	consBlockID := findBlockContaining(cfg, xRef)

	// y should be dead on the throwing path
	require.False(t, info.LiveBefore[consBlockID][0][yID],
		"y should be dead on the throwing path")
}

func TestLivenessMatchExpr(t *testing.T) {
	// val x = 1; val y = 2; val z = 3
	// val r = match target {
	//   case _ => x    // arm 0 uses x
	//   case _ => y    // arm 1 uses y
	// }
	// print(r)
	// z is dead in both arms (not used).
	// x is dead in arm 1, y is dead in arm 0.
	x := identPat("x")
	y := identPat("y")
	z := identPat("z")
	r := identPat("r")
	xRef := ident("x")
	yRef := ident("y")
	rRef := ident("r")

	body := block(
		valDecl(x, numLit(1)),
		valDecl(y, numLit(2)),
		valDecl(z, numLit(3)),
		valDecl(r, ast.NewMatch(
			ident("target"),
			[]*ast.MatchCase{
				ast.NewMatchCase(
					ast.NewWildcardPat(span()),
					nil,
					ast.BlockOrExpr{Expr: xRef},
					span(),
				),
				ast.NewMatchCase(
					ast.NewWildcardPat(span()),
					nil,
					ast.BlockOrExpr{Expr: yRef},
					span(),
				),
			},
			span(),
		)),
		exprStmt(call(ident("print"), rRef)),
	)
	Rename(nil, body, map[string]VarID{"target": -1, "print": -2})

	cfg := BuildCFG(body)
	info := AnalyzeFunction(cfg)

	xID := VarID(x.VarID)
	yID := VarID(y.VarID)
	zID := VarID(z.VarID)

	arm0ID := findBlockContaining(cfg, xRef)
	arm1ID := findBlockContaining(cfg, yRef)

	// Arm 0: x is live, y is dead, z is dead
	require.True(t, info.LiveBefore[arm0ID][0][xID], "x should be live in arm 0")
	require.False(t, info.LiveBefore[arm0ID][0][yID], "y should be dead in arm 0")
	require.False(t, info.LiveBefore[arm0ID][0][zID], "z should be dead in arm 0")

	// Arm 1: y is live, x is dead, z is dead
	require.True(t, info.LiveBefore[arm1ID][0][yID], "y should be live in arm 1")
	require.False(t, info.LiveBefore[arm1ID][0][xID], "x should be dead in arm 1")
	require.False(t, info.LiveBefore[arm1ID][0][zID], "z should be dead in arm 1")
}

func TestLivenessNestedControlFlow(t *testing.T) {
	// val x = 1
	// for i in items {
	//   if cond { print(x) }
	// }
	// x is used inside an if inside a for loop, so it should be live
	// through the entire loop.
	x := identPat("x")
	i := identPat("i")
	xRef := ident("x")

	body := block(
		valDecl(x, numLit(1)),
		ast.NewForInStmt(
			i, ident("items"),
			block(
				exprStmt(ast.NewIfElse(
					ident("cond"),
					block(exprStmt(call(ident("print"), xRef))),
					nil,
					span(),
				)),
			),
			false, span(),
		),
	)
	Rename(nil, body, map[string]VarID{"items": -1, "cond": -2, "print": -3})

	cfg := BuildCFG(body)
	info := AnalyzeFunction(cfg)

	xID := VarID(x.VarID)

	// x should be live after its definition
	require.True(t, info.LiveAfter[cfg.Entry.ID][0][xID],
		"x should be live after definition (used in nested if inside loop)")
}

func TestLivenessDoExpr(t *testing.T) {
	// val x = 1
	// val y = do { val a = x + 1; a }
	// print(y)
	// x is used inside the do block, so it should be live before the do.
	x := identPat("x")
	a := identPat("a")
	y := identPat("y")
	xRef := ident("x")
	aRef := ident("a")
	yRef := ident("y")

	body := block(
		valDecl(x, numLit(1)),
		valDecl(y, ast.NewDo(
			block(
				valDecl(a, ast.NewBinary(xRef, numLit(1), ast.Plus, span())),
				exprStmt(aRef),
			),
			span(),
		)),
		exprStmt(call(ident("print"), yRef)),
	)
	Rename(nil, body, map[string]VarID{"print": -1})

	cfg := BuildCFG(body)
	info := AnalyzeFunction(cfg)

	xID := VarID(x.VarID)

	// x should be live after its definition (used in the do block)
	require.True(t, info.LiveAfter[cfg.Entry.ID][0][xID],
		"x should be live after definition (used in do block)")
}

func TestLivenessVarUsedInBranch(t *testing.T) {
	// Phase 3 limitation: variables used only inside branches appeared
	// dead. Phase 4 correctly tracks them.
	// val x = 1; val y = do { x + 1 }
	// x should be live before the do expression.
	x := identPat("x")
	y := identPat("y")
	xRef := ident("x")

	body := block(
		valDecl(x, numLit(1)),
		valDecl(y, ast.NewDo(
			block(exprStmt(ast.NewBinary(xRef, numLit(1), ast.Plus, span()))),
			span(),
		)),
	)
	Rename(nil, body, nil)

	cfg := BuildCFG(body)
	info := AnalyzeFunction(cfg)

	xID := VarID(x.VarID)

	// x should be live after its definition
	require.True(t, info.LiveAfter[cfg.Entry.ID][0][xID],
		"x should be live (Phase 4 correctly tracks uses in nested blocks)")
}

func TestLivenessIfElseVarUsedInBranch(t *testing.T) {
	// Phase 3 limitation: val y = if true { x } else { 0 }
	// x was not detected as used. Phase 4 fixes this.
	x := identPat("x")
	y := identPat("y")
	xRef := ident("x")

	altBlock := ast.Block{
		Stmts: []ast.Stmt{exprStmt(numLit(0))},
		Span:  span(),
	}
	body := block(
		valDecl(x, numLit(1)),
		valDecl(y, ast.NewIfElse(
			ident("cond"),
			block(exprStmt(xRef)),
			&ast.BlockOrExpr{Block: &altBlock},
			span(),
		)),
	)
	Rename(nil, body, map[string]VarID{"cond": -1})

	cfg := BuildCFG(body)
	info := AnalyzeFunction(cfg)

	xID := VarID(x.VarID)

	// x should be live after its definition (used in then branch)
	require.True(t, info.LiveAfter[cfg.Entry.ID][0][xID],
		"x should be live (Phase 4 correctly tracks uses in if-else branches)")
}

func TestLivenessForLoopVarDef(t *testing.T) {
	// for i in items { print(i) }
	// The loop variable i should be defined (ExtraDefs) in the body block.
	i := identPat("i")
	iRef := ident("i")

	body := block(
		ast.NewForInStmt(
			i, ident("items"),
			block(exprStmt(call(ident("print"), iRef))),
			false, span(),
		),
	)
	Rename(nil, body, map[string]VarID{"items": -1, "print": -2})

	cfg := BuildCFG(body)
	info := AnalyzeFunction(cfg)

	iID := VarID(i.VarID)

	// Find the body block (has ExtraDefs containing iID)
	var bodyBlock *BasicBlock
	for _, blk := range cfg.Blocks {
		for _, d := range blk.ExtraDefs {
			if d == iID {
				bodyBlock = blk
			}
		}
	}
	require.NotNil(t, bodyBlock, "should find body block with loop var ExtraDefs")

	// i should be live before the print(i) call in the body block
	require.True(t, info.LiveBefore[bodyBlock.ID][0][iID],
		"loop variable i should be live in the body block")
}

func TestLivenessAllBranchesReturn(t *testing.T) {
	// val x = 1
	// if cond { return x } else { return x }
	// val y = 2  // unreachable
	// y should never be live.
	x := identPat("x")
	y := identPat("y")
	xRef1 := ident("x")
	xRef2 := ident("x")

	body := block(
		valDecl(x, numLit(1)),
		exprStmt(ast.NewIfElse(
			ident("cond"),
			block(ast.NewReturnStmt(xRef1, span())),
			&ast.BlockOrExpr{Block: &ast.Block{
				Stmts: []ast.Stmt{ast.NewReturnStmt(xRef2, span())},
				Span:  span(),
			}},
			span(),
		)),
		valDecl(y, numLit(2)),
	)
	Rename(nil, body, map[string]VarID{"cond": -1})

	cfg := BuildCFG(body)
	info := AnalyzeFunction(cfg)

	yID := VarID(y.VarID)

	// y should never be live anywhere in the CFG
	for _, blk := range cfg.Blocks {
		for i := range blk.Stmts {
			require.False(t, info.LiveBefore[blk.ID][i][yID],
				"y should never be live (unreachable)")
			require.False(t, info.LiveAfter[blk.ID][i][yID],
				"y should never be live (unreachable)")
		}
	}
}

func TestLivenessMatchWithGuard(t *testing.T) {
	// val x = 1; val g = true
	// match target {
	//   case _ if g => x
	//   case _ => 0
	// }
	// Guard expression uses g; arm body uses x.
	x := identPat("x")
	g := identPat("g")
	xRef := ident("x")
	gRef := ident("g")

	body := block(
		valDecl(x, numLit(1)),
		valDecl(g, ident("true")),
		exprStmt(ast.NewMatch(
			ident("target"),
			[]*ast.MatchCase{
				ast.NewMatchCase(
					ast.NewWildcardPat(span()),
					gRef,
					ast.BlockOrExpr{Expr: xRef},
					span(),
				),
				ast.NewMatchCase(
					ast.NewWildcardPat(span()),
					nil,
					ast.BlockOrExpr{Expr: numLit(0)},
					span(),
				),
			},
			span(),
		)),
	)
	Rename(nil, body, map[string]VarID{"target": -1, "true": -2})

	cfg := BuildCFG(body)
	info := AnalyzeFunction(cfg)

	xID := VarID(x.VarID)
	gID := VarID(g.VarID)

	arm0ID := findBlockContaining(cfg, xRef)

	// In arm 0: both g (guard) and x (body) should be live
	require.True(t, info.LiveBefore[arm0ID][0][gID],
		"guard variable g should be live in arm 0")
	require.True(t, info.LiveBefore[arm0ID][1][xID],
		"x should be live before its use in arm 0")
}
