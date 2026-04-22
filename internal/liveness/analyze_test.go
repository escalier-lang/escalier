package liveness

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// assignExpr builds: target = value
func assignExpr(target, value ast.Expr) *ast.BinaryExpr {
	return ast.NewBinary(target, value, ast.Assign, span())
}

// --- CollectUses tests ---

func TestCollectUsesSimpleDecl(t *testing.T) {
	// val x = 1
	x := identPat("x")
	stmts := []ast.Stmt{valDecl(x, numLit(1))}
	Rename(nil, block(stmts...), nil)

	uses := CollectUses(stmts)

	require.Len(t, uses, 1)
	require.Empty(t, uses[0].Uses)
	require.Equal(t, []VarID{VarID(x.VarID)}, uses[0].Defs)
}

func TestCollectUsesDeclWithIdentInit(t *testing.T) {
	// val x = 1; val y = x
	x := identPat("x")
	xRef := ident("x")
	y := identPat("y")
	stmts := []ast.Stmt{
		valDecl(x, numLit(1)),
		valDecl(y, xRef),
	}
	Rename(nil, block(stmts...), nil)

	uses := CollectUses(stmts)

	require.Len(t, uses, 2)
	// val x = 1: def x
	require.Empty(t, uses[0].Uses)
	require.Equal(t, []VarID{VarID(x.VarID)}, uses[0].Defs)
	// val y = x: use x, def y
	require.Equal(t, []VarID{VarID(xRef.VarID)}, uses[1].Uses)
	require.Equal(t, []VarID{VarID(y.VarID)}, uses[1].Defs)
}

func TestCollectUsesAssignment(t *testing.T) {
	// var x = 1; x = 2
	x := identPat("x")
	xTarget := ident("x")
	stmts := []ast.Stmt{
		varDecl(x, numLit(1)),
		exprStmt(assignExpr(xTarget, numLit(2))),
	}
	Rename(nil, block(stmts...), nil)

	uses := CollectUses(stmts)

	require.Len(t, uses, 2)
	// x = 2: def x (no use — plain assignment)
	require.Empty(t, uses[1].Uses)
	require.Equal(t, []VarID{VarID(xTarget.VarID)}, uses[1].Defs)
}

func TestCollectUsesExprStmt(t *testing.T) {
	// val x = 1; print(x)
	x := identPat("x")
	xRef := ident("x")
	stmts := []ast.Stmt{
		valDecl(x, numLit(1)),
		exprStmt(call(ident("print"), xRef)),
	}
	Rename(nil, block(stmts...), map[string]VarID{"print": -1})

	uses := CollectUses(stmts)

	require.Len(t, uses, 2)
	// print(x): use x (print has negative VarID, so excluded)
	require.Equal(t, []VarID{VarID(xRef.VarID)}, uses[1].Uses)
	require.Empty(t, uses[1].Defs)
}

func TestCollectUsesMemberExpr(t *testing.T) {
	// val obj = {}; obj.x
	obj := identPat("obj")
	objRef := ident("obj")
	stmts := []ast.Stmt{
		valDecl(obj, &ast.ObjectExpr{Elems: nil}),
		exprStmt(&ast.MemberExpr{Object: objRef, Prop: ast.NewIdentifier("x", span())}),
	}
	Rename(nil, block(stmts...), nil)

	uses := CollectUses(stmts)

	// obj.x: use obj
	require.Equal(t, []VarID{VarID(objRef.VarID)}, uses[1].Uses)
}

func TestCollectUsesIgnoresNonLocal(t *testing.T) {
	// print(globalVar) — both are non-local
	stmts := []ast.Stmt{
		exprStmt(call(ident("print"), ident("globalVar"))),
	}
	Rename(nil, block(stmts...), map[string]VarID{"print": -1, "globalVar": -2})

	uses := CollectUses(stmts)

	require.Len(t, uses, 1)
	require.Empty(t, uses[0].Uses) // non-local VarIDs are filtered out
	require.Empty(t, uses[0].Defs)
}

func TestCollectUsesReturnStmt(t *testing.T) {
	// val x = 1; return x
	x := identPat("x")
	xRef := ident("x")
	stmts := []ast.Stmt{
		valDecl(x, numLit(1)),
		ast.NewReturnStmt(xRef, span()),
	}
	Rename(nil, block(stmts...), nil)

	uses := CollectUses(stmts)

	require.Len(t, uses, 2)
	require.Equal(t, []VarID{VarID(xRef.VarID)}, uses[1].Uses)
	require.Empty(t, uses[1].Defs)
}

func TestCollectUsesFuncDecl(t *testing.T) {
	// fn add() {}; print(add)
	addRef := ident("add")
	funcDecl := ast.NewFuncDecl(
		ast.NewIdentifier("add", span()),
		nil, // type params
		nil, // params
		nil, // return type
		nil, // throws type
		&ast.Block{Stmts: nil, Span: span()},
		false, false, false, span(),
	)
	stmts := []ast.Stmt{
		ast.NewDeclStmt(funcDecl, span()),
		exprStmt(call(addRef)),
	}
	Rename(nil, block(stmts...), nil)

	uses := CollectUses(stmts)

	require.Len(t, uses, 2)
	// fn add: def add
	require.Empty(t, uses[0].Uses)
	require.Equal(t, []VarID{VarID(funcDecl.VarID)}, uses[0].Defs)
	// add(): use add
	require.Equal(t, []VarID{VarID(addRef.VarID)}, uses[1].Uses)
}

func TestCollectUsesDoExprRecursed(t *testing.T) {
	// val x = 1; val y = do { x + 1 }
	// When nested inside another expression (not decomposed by the CFG),
	// CollectUses recurses into do block bodies to capture all uses.
	x := identPat("x")
	xRef := ident("x") // inside do block
	y := identPat("y")
	stmts := []ast.Stmt{
		valDecl(x, numLit(1)),
		valDecl(y, &ast.DoExpr{Body: block(
			exprStmt(ast.NewBinary(xRef, numLit(1), ast.Plus, span())),
		)}),
	}
	Rename(nil, block(stmts...), nil)

	uses := CollectUses(stmts)

	require.Len(t, uses, 2)
	// The use of x inside the do block is captured by CollectUses.
	require.Equal(t, []VarID{VarID(xRef.VarID)}, uses[1].Uses, "do block body should be recursed into by CollectUses")
}

func TestCollectUsesIfElseRecursed(t *testing.T) {
	// val x = 1; val y = if cond { x } else { 0 }
	// When nested inside another expression (not decomposed by the CFG),
	// CollectUses recurses into branch bodies to capture all uses.
	x := identPat("x")
	xRef := ident("x") // inside then branch
	y := identPat("y")
	altBlock := ast.Block{
		Stmts: []ast.Stmt{exprStmt(numLit(0))},
		Span:  span(),
	}
	stmts := []ast.Stmt{
		valDecl(x, numLit(1)),
		valDecl(y, ast.NewIfElse(
			ident("cond"),
			block(exprStmt(xRef)),
			&ast.BlockOrExpr{Block: &altBlock},
			span(),
		)),
	}
	Rename(nil, block(stmts...), map[string]VarID{"cond": -1})

	uses := CollectUses(stmts)

	require.Len(t, uses, 2)
	// The use of x inside the then branch is captured by CollectUses.
	require.Equal(t, []VarID{VarID(xRef.VarID)}, uses[1].Uses, "if/else branch bodies should be recursed into by CollectUses")
}

// --- AnalyzeBlock tests ---

func TestLivenessEmptyBlock(t *testing.T) {
	info := AnalyzeBlock(nil)

	require.NotNil(t, info)
	require.Empty(t, info.LastUse)
}

func TestLivenessSimpleSequential(t *testing.T) {
	// val x = 1; val y = x; print(y)
	x := identPat("x")
	xRef := ident("x")
	y := identPat("y")
	yRef := ident("y")
	stmts := []ast.Stmt{
		valDecl(x, numLit(1)),
		valDecl(y, xRef),
		exprStmt(call(ident("print"), yRef)),
	}
	Rename(nil, block(stmts...), map[string]VarID{"print": -1})

	info := AnalyzeBlock(stmts)

	xID := VarID(x.VarID)
	yID := VarID(y.VarID)

	// Statement 0: val x = 1
	// LiveBefore: {} (x not yet used, will be defined here)
	// LiveAfter: {x} (x is used in statement 1)
	require.False(t, info.LiveBefore[0][0][xID])
	require.True(t, info.LiveAfter[0][0][xID])

	// Statement 1: val y = x
	// LiveBefore: {x} (x is used here)
	// LiveAfter: {y} (y is used in statement 2, x is dead)
	require.True(t, info.LiveBefore[0][1][xID])
	require.False(t, info.LiveAfter[0][1][xID])
	require.True(t, info.LiveAfter[0][1][yID])

	// Statement 2: print(y)
	// LiveBefore: {y} (y is used here)
	// LiveAfter: {} (nothing needed after)
	require.True(t, info.LiveBefore[0][2][yID])
	require.False(t, info.LiveAfter[0][2][yID])
}

func TestLivenessVariableDeadAfterLastUse(t *testing.T) {
	// val x = 1; print(x); val y = 2; print(y)
	x := identPat("x")
	xRef := ident("x")
	y := identPat("y")
	yRef := ident("y")
	stmts := []ast.Stmt{
		valDecl(x, numLit(1)),
		exprStmt(call(ident("print"), xRef)),
		valDecl(y, numLit(2)),
		exprStmt(call(ident("print"), yRef)),
	}
	Rename(nil, block(stmts...), map[string]VarID{"print": -1})

	info := AnalyzeBlock(stmts)

	xID := VarID(x.VarID)

	// x is dead after statement 1 (its last use)
	require.True(t, info.LiveBefore[0][1][xID])
	require.False(t, info.LiveAfter[0][1][xID])
	require.False(t, info.LiveBefore[0][2][xID])
	require.False(t, info.LiveAfter[0][2][xID])
}

func TestLivenessDefinitionKillsVariable(t *testing.T) {
	// var x = 1; print(x); x = 2; print(x)
	x := identPat("x")
	xRef1 := ident("x")
	xTarget := ident("x")
	xRef2 := ident("x")
	stmts := []ast.Stmt{
		varDecl(x, numLit(1)),
		exprStmt(call(ident("print"), xRef1)),
		exprStmt(assignExpr(xTarget, numLit(2))),
		exprStmt(call(ident("print"), xRef2)),
	}
	Rename(nil, block(stmts...), map[string]VarID{"print": -1})

	info := AnalyzeBlock(stmts)

	xID := VarID(x.VarID)

	// After stmt 1 (print(x)): x is dead because stmt 2 redefines it
	// before stmt 3 uses it.
	require.True(t, info.LiveBefore[0][1][xID])
	require.False(t, info.LiveAfter[0][1][xID])

	// Stmt 2 (x = 2): x is defined here, so LiveBefore doesn't include x
	// (the old value is killed). But LiveAfter does (x is used in stmt 3).
	require.False(t, info.LiveBefore[0][2][xID])
	require.True(t, info.LiveAfter[0][2][xID])

	// Stmt 3 (print(x)): x is live before (used here), dead after.
	require.True(t, info.LiveBefore[0][3][xID])
	require.False(t, info.LiveAfter[0][3][xID])
}

func TestLivenessUnusedVariableNeverLive(t *testing.T) {
	// val x = 1; val y = 2; print(y)
	x := identPat("x")
	y := identPat("y")
	yRef := ident("y")
	stmts := []ast.Stmt{
		valDecl(x, numLit(1)),
		valDecl(y, numLit(2)),
		exprStmt(call(ident("print"), yRef)),
	}
	Rename(nil, block(stmts...), map[string]VarID{"print": -1})

	info := AnalyzeBlock(stmts)

	xID := VarID(x.VarID)

	// x is never used, so it should never be live.
	for i := range 3 {
		require.False(t, info.LiveBefore[0][i][xID], "x should not be live before stmt %d", i)
		require.False(t, info.LiveAfter[0][i][xID], "x should not be live after stmt %d", i)
	}
}

func TestLivenessShadowingDistinctVarIDs(t *testing.T) {
	// val x = 1; val y = x; val x = 2; print(x)
	// First x is dead after val y = x, second x is live until print(x).
	x1 := identPat("x")
	xRef1 := ident("x") // in val y = x
	y := identPat("y")
	x2 := identPat("x")
	xRef2 := ident("x") // in print(x)
	stmts := []ast.Stmt{
		valDecl(x1, numLit(1)),
		valDecl(y, xRef1),
		valDecl(x2, numLit(2)),
		exprStmt(call(ident("print"), xRef2)),
	}
	Rename(nil, block(stmts...), map[string]VarID{"print": -1})

	info := AnalyzeBlock(stmts)

	x1ID := VarID(x1.VarID)
	x2ID := VarID(x2.VarID)

	// Distinct VarIDs for the two x's.
	require.NotEqual(t, x1ID, x2ID)

	// First x: live before stmt 1 (used there), dead after.
	require.True(t, info.LiveBefore[0][1][x1ID])
	require.False(t, info.LiveAfter[0][1][x1ID])

	// Second x: live after stmt 2 (defined there), live before stmt 3.
	require.True(t, info.LiveAfter[0][2][x2ID])
	require.True(t, info.LiveBefore[0][3][x2ID])
	require.False(t, info.LiveAfter[0][3][x2ID])
}

func TestLivenessLastUse(t *testing.T) {
	// val x = 1; print(x); print(x)
	x := identPat("x")
	xRef1 := ident("x")
	xRef2 := ident("x")
	stmts := []ast.Stmt{
		valDecl(x, numLit(1)),
		exprStmt(call(ident("print"), xRef1)),
		exprStmt(call(ident("print"), xRef2)),
	}
	Rename(nil, block(stmts...), map[string]VarID{"print": -1})

	info := AnalyzeBlock(stmts)

	xID := VarID(x.VarID)

	// Last use of x is in statement 2 (the second print).
	require.Equal(t, StmtRef{BlockID: 0, StmtIdx: 2}, info.LastUse[xID])
}

func TestLivenessIsLiveAfter(t *testing.T) {
	// val x = 1; print(x)
	x := identPat("x")
	xRef := ident("x")
	stmts := []ast.Stmt{
		valDecl(x, numLit(1)),
		exprStmt(call(ident("print"), xRef)),
	}
	Rename(nil, block(stmts...), map[string]VarID{"print": -1})

	info := AnalyzeBlock(stmts)

	xID := VarID(x.VarID)

	require.True(t, info.IsLiveAfter(StmtRef{BlockID: 0, StmtIdx: 0}, xID))
	require.False(t, info.IsLiveAfter(StmtRef{BlockID: 0, StmtIdx: 1}, xID))
}

func TestLivenessMultipleVariables(t *testing.T) {
	// val a = 1; val b = 2; val c = a + b; print(c)
	a := identPat("a")
	b := identPat("b")
	aRef := ident("a")
	bRef := ident("b")
	c := identPat("c")
	cRef := ident("c")
	stmts := []ast.Stmt{
		valDecl(a, numLit(1)),
		valDecl(b, numLit(2)),
		valDecl(c, ast.NewBinary(aRef, bRef, ast.Plus, span())),
		exprStmt(call(ident("print"), cRef)),
	}
	Rename(nil, block(stmts...), map[string]VarID{"print": -1})

	info := AnalyzeBlock(stmts)

	aID := VarID(a.VarID)
	bID := VarID(b.VarID)
	cID := VarID(c.VarID)

	// After stmt 0 (val a = 1): a is live (used in stmt 2)
	require.True(t, info.LiveAfter[0][0][aID])

	// After stmt 1 (val b = 2): a and b are both live
	require.True(t, info.LiveAfter[0][1][aID])
	require.True(t, info.LiveAfter[0][1][bID])

	// After stmt 2 (val c = a + b): a and b are dead, c is live
	require.False(t, info.LiveAfter[0][2][aID])
	require.False(t, info.LiveAfter[0][2][bID])
	require.True(t, info.LiveAfter[0][2][cID])

	// After stmt 3 (print(c)): c is dead
	require.False(t, info.LiveAfter[0][3][cID])
}

func TestLivenessAssignmentFromVariable(t *testing.T) {
	// val a = 1; var b = 0; b = a; print(b)
	a := identPat("a")
	b := identPat("b")
	bTarget := ident("b")
	aRef := ident("a")
	bRef := ident("b")
	stmts := []ast.Stmt{
		valDecl(a, numLit(1)),
		varDecl(b, numLit(0)),
		exprStmt(assignExpr(bTarget, aRef)),
		exprStmt(call(ident("print"), bRef)),
	}
	Rename(nil, block(stmts...), map[string]VarID{"print": -1})

	info := AnalyzeBlock(stmts)

	aID := VarID(a.VarID)
	bID := VarID(b.VarID)

	// Stmt 2 (b = a): a is used, b is defined.
	// LiveBefore should include a (used here) but not b (killed here).
	require.True(t, info.LiveBefore[0][2][aID])
	require.False(t, info.LiveBefore[0][2][bID])
	// LiveAfter: b is live (used in stmt 3), a is dead.
	require.True(t, info.LiveAfter[0][2][bID])
	require.False(t, info.LiveAfter[0][2][aID])
}
