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
	ids := NewVarIDs()
	// val x = 1
	x := identPat("x")
	stmts := []ast.Stmt{valDecl(x, numLit(1))}
	RenameFrom(ids, nil, block(stmts...), nil, 1)

	uses := CollectUses(ids, stmts)

	require.Len(t, uses, 1)
	require.Empty(t, uses[0].Uses)
	require.Equal(t, []VarID{ids.Of(x)}, uses[0].Defs)
}

func TestCollectUsesDeclWithIdentInit(t *testing.T) {
	ids := NewVarIDs()
	// val x = 1; val y = x
	x := identPat("x")
	xRef := ident("x")
	y := identPat("y")
	stmts := []ast.Stmt{
		valDecl(x, numLit(1)),
		valDecl(y, xRef),
	}
	RenameFrom(ids, nil, block(stmts...), nil, 1)

	uses := CollectUses(ids, stmts)

	require.Len(t, uses, 2)
	// val x = 1: def x
	require.Empty(t, uses[0].Uses)
	require.Equal(t, []VarID{ids.Of(x)}, uses[0].Defs)
	// val y = x: use x, def y
	require.Equal(t, []VarID{ids.Of(xRef)}, uses[1].Uses)
	require.Equal(t, []VarID{ids.Of(y)}, uses[1].Defs)
}

func TestCollectUsesAssignment(t *testing.T) {
	ids := NewVarIDs()
	// var x = 1; x = 2
	x := identPat("x")
	xTarget := ident("x")
	stmts := []ast.Stmt{
		varDecl(x, numLit(1)),
		exprStmt(assignExpr(xTarget, numLit(2))),
	}
	RenameFrom(ids, nil, block(stmts...), nil, 1)

	uses := CollectUses(ids, stmts)

	require.Len(t, uses, 2)
	// x = 2: def x (no use — plain assignment)
	require.Empty(t, uses[1].Uses)
	require.Equal(t, []VarID{ids.Of(xTarget)}, uses[1].Defs)
}

func TestCollectUsesExprStmt(t *testing.T) {
	ids := NewVarIDs()
	// val x = 1; print(x)
	x := identPat("x")
	xRef := ident("x")
	stmts := []ast.Stmt{
		valDecl(x, numLit(1)),
		exprStmt(call(ident("print"), xRef)),
	}
	RenameFrom(ids, nil, block(stmts...), map[string]VarID{"print": -1}, 1)

	uses := CollectUses(ids, stmts)

	require.Len(t, uses, 2)
	// print(x): use x (print has negative VarID, so excluded)
	require.Equal(t, []VarID{ids.Of(xRef)}, uses[1].Uses)
	require.Empty(t, uses[1].Defs)
}

func TestCollectUsesMemberExpr(t *testing.T) {
	ids := NewVarIDs()
	// val obj = {}; obj.x
	obj := identPat("obj")
	objRef := ident("obj")
	stmts := []ast.Stmt{
		valDecl(obj, &ast.ObjectExpr{Elems: nil}),
		exprStmt(&ast.MemberExpr{Object: objRef, Prop: ast.NewIdentifier("x", span())}),
	}
	RenameFrom(ids, nil, block(stmts...), nil, 1)

	uses := CollectUses(ids, stmts)

	// obj.x: use obj
	require.Equal(t, []VarID{ids.Of(objRef)}, uses[1].Uses)
}

func TestCollectUsesIgnoresNonLocal(t *testing.T) {
	ids := NewVarIDs()
	// print(globalVar) — both are non-local
	stmts := []ast.Stmt{
		exprStmt(call(ident("print"), ident("globalVar"))),
	}
	RenameFrom(ids, nil, block(stmts...), map[string]VarID{"print": -1, "globalVar": -2}, 1)

	uses := CollectUses(ids, stmts)

	require.Len(t, uses, 1)
	require.Empty(t, uses[0].Uses) // non-local VarIDs are filtered out
	require.Empty(t, uses[0].Defs)
}

func TestCollectUsesReturnStmt(t *testing.T) {
	ids := NewVarIDs()
	// val x = 1; return x
	x := identPat("x")
	xRef := ident("x")
	stmts := []ast.Stmt{
		valDecl(x, numLit(1)),
		ast.NewReturnStmt(xRef, span()),
	}
	RenameFrom(ids, nil, block(stmts...), nil, 1)

	uses := CollectUses(ids, stmts)

	require.Len(t, uses, 2)
	require.Equal(t, []VarID{ids.Of(xRef)}, uses[1].Uses)
	require.Empty(t, uses[1].Defs)
}

func TestCollectUsesFuncDecl(t *testing.T) {
	ids := NewVarIDs()
	// fn add() {}; print(add)
	addRef := ident("add")
	funcDecl := ast.NewFuncDecl(
		ast.NewIdentifier("add", span()),
		nil, // lifetime params
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
	RenameFrom(ids, nil, block(stmts...), nil, 1)

	uses := CollectUses(ids, stmts)

	require.Len(t, uses, 2)
	// fn add: def add
	require.Empty(t, uses[0].Uses)
	require.Equal(t, []VarID{ids.Of(funcDecl)}, uses[0].Defs)
	// add(): use add
	require.Equal(t, []VarID{ids.Of(addRef)}, uses[1].Uses)
}

func TestCollectUsesDoExprRecursed(t *testing.T) {
	ids := NewVarIDs()
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
	RenameFrom(ids, nil, block(stmts...), nil, 1)

	uses := CollectUses(ids, stmts)

	require.Len(t, uses, 2)
	// The use of x inside the do block is captured by CollectUses.
	require.Equal(t, []VarID{ids.Of(xRef)}, uses[1].Uses, "do block body should be recursed into by CollectUses")
}

func TestCollectUsesIfElseRecursed(t *testing.T) {
	ids := NewVarIDs()
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
	RenameFrom(ids, nil, block(stmts...), map[string]VarID{"cond": -1}, 1)

	uses := CollectUses(ids, stmts)

	require.Len(t, uses, 2)
	// The use of x inside the then branch is captured by CollectUses.
	require.Equal(t, []VarID{ids.Of(xRef)}, uses[1].Uses, "if/else branch bodies should be recursed into by CollectUses")
}

func TestCollectUsesMatchRecursed(t *testing.T) {
	ids := NewVarIDs()
	// val x = 1; val y = 2
	// val r = match target {
	//   case _ => x
	//   case _ => y
	// }
	// When nested inside another expression (not decomposed by the CFG),
	// CollectUses recurses into match arms to capture all uses.
	x := identPat("x")
	y := identPat("y")
	r := identPat("r")
	xRef := ident("x")
	yRef := ident("y")
	stmts := []ast.Stmt{
		valDecl(x, numLit(1)),
		valDecl(y, numLit(2)),
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
	}
	RenameFrom(ids, nil, block(stmts...), map[string]VarID{"target": -1}, 1)

	uses := CollectUses(ids, stmts)

	require.Len(t, uses, 3)
	// Both x and y inside the match arms are captured by CollectUses.
	require.Equal(t, []VarID{ids.Of(xRef), ids.Of(yRef)}, uses[2].Uses,
		"match arm bodies should be recursed into by CollectUses")
}

func TestCollectUsesMatchWithGuardRecursed(t *testing.T) {
	ids := NewVarIDs()
	// val x = 1; val g = true
	// val r = match target {
	//   case _ if g => x
	//   case _ => 0
	// }
	// Guard expression uses should also be captured.
	x := identPat("x")
	g := identPat("g")
	r := identPat("r")
	xRef := ident("x")
	gRef := ident("g")
	stmts := []ast.Stmt{
		valDecl(x, numLit(1)),
		valDecl(g, ident("true")),
		valDecl(r, ast.NewMatch(
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
	}
	RenameFrom(ids, nil, block(stmts...), map[string]VarID{"target": -1, "true": -2}, 1)

	uses := CollectUses(ids, stmts)

	require.Len(t, uses, 3)
	// Both the guard (g) and arm body (x) are captured.
	require.Equal(t, []VarID{ids.Of(gRef), ids.Of(xRef)}, uses[2].Uses,
		"match guard and arm body should be recursed into by CollectUses")
}

func TestCollectUsesTryCatchRecursed(t *testing.T) {
	ids := NewVarIDs()
	// val x = 1; val y = 2
	// val r = try { x } catch { case e => y }
	// When nested inside another expression (not decomposed by the CFG),
	// CollectUses recurses into try and catch bodies to capture all uses.
	x := identPat("x")
	y := identPat("y")
	r := identPat("r")
	xRef := ident("x")
	yRef := ident("y")
	e := identPat("e")
	stmts := []ast.Stmt{
		valDecl(x, numLit(1)),
		valDecl(y, numLit(2)),
		valDecl(r, ast.NewTryCatch(
			block(exprStmt(xRef)),
			[]*ast.MatchCase{
				ast.NewMatchCase(
					e,
					nil,
					ast.BlockOrExpr{Expr: yRef},
					span(),
				),
			},
			span(),
		)),
	}
	RenameFrom(ids, nil, block(stmts...), nil, 1)

	uses := CollectUses(ids, stmts)

	require.Len(t, uses, 3)
	// Both x (try body) and y (catch body) are captured by CollectUses.
	require.Equal(t, []VarID{ids.Of(xRef), ids.Of(yRef)}, uses[2].Uses,
		"try and catch bodies should be recursed into by CollectUses")
}
