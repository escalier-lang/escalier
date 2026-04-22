package liveness

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// span returns a zero-value Span for test convenience.
func span() ast.Span {
	return ast.Span{}
}

// Helper to build: val <pat> = <init>
func valDecl(pat ast.Pat, init ast.Expr) ast.Stmt {
	return ast.NewDeclStmt(
		ast.NewVarDecl(ast.ValKind, pat, nil, init, false, false, span()),
		span(),
	)
}

// Helper to build: var <pat> = <init>
func varDecl(pat ast.Pat, init ast.Expr) ast.Stmt {
	return ast.NewDeclStmt(
		ast.NewVarDecl(ast.VarKind, pat, nil, init, false, false, span()),
		span(),
	)
}

// Helper to build an expression statement.
func exprStmt(expr ast.Expr) ast.Stmt {
	return ast.NewExprStmt(expr, span())
}

// Helper to build an identifier expression.
func ident(name string) *ast.IdentExpr {
	return ast.NewIdent(name, span())
}

// Helper to build an identifier pattern.
func identPat(name string) *ast.IdentPat {
	return ast.NewIdentPat(name, nil, nil, span())
}

// Helper to build a number literal.
func numLit(val float64) *ast.LiteralExpr {
	return ast.NewLitExpr(ast.NewNumber(val, span()))
}

// Helper to build a call expression: callee(args...)
func call(callee ast.Expr, args ...ast.Expr) *ast.CallExpr {
	return ast.NewCall(callee, args, false, span())
}

// Helper to build a block from statements.
func block(stmts ...ast.Stmt) ast.Block {
	return ast.Block{Stmts: stmts, Span: span()}
}

func TestSimpleBindingAndUse(t *testing.T) {
	// val x = 1; print(x)
	x := identPat("x")
	xRef := ident("x")
	body := block(
		valDecl(x, numLit(1)),
		exprStmt(call(ident("print"), xRef)),
	)

	result := Rename(nil, body, map[string]VarID{"print": -1})

	require.Empty(t, result.Errors)
	require.Equal(t, 1, result.UniqueVarCount)            // one local variable
	require.Equal(t, x.VarID, xRef.VarID)          // use resolves to binding
	require.NotEqual(t, 0, x.VarID)                 // VarID is set
}

func TestCrossScopeShadowing(t *testing.T) {
	// val x = 1; do { val x = 2; print(x) }; print(x)
	outerX := identPat("x")
	innerX := identPat("x")
	innerRef := ident("x")
	outerRef := ident("x")

	body := block(
		valDecl(outerX, numLit(1)),
		exprStmt(&ast.DoExpr{Body: block(
			valDecl(innerX, numLit(2)),
			exprStmt(call(ident("print"), innerRef)),
		)}),
		exprStmt(call(ident("print"), outerRef)),
	)

	result := Rename(nil, body, map[string]VarID{"print": -1})

	require.Empty(t, result.Errors)
	require.Equal(t, 2, result.UniqueVarCount) // two distinct local variables
	require.NotEqual(t, outerX.VarID, innerX.VarID)
	require.Equal(t, innerX.VarID, innerRef.VarID)  // inner print(x) → inner x
	require.Equal(t, outerX.VarID, outerRef.VarID)  // outer print(x) → outer x
}

func TestSameScopeShadowing(t *testing.T) {
	// val x = 1; val y = x; val x = 2; print(x)
	x1 := identPat("x")
	y := identPat("y")
	xRef1 := ident("x") // reference in val y = x
	x2 := identPat("x")
	xRef2 := ident("x") // reference in print(x)

	body := block(
		valDecl(x1, numLit(1)),
		valDecl(y, xRef1),
		valDecl(x2, numLit(2)),
		exprStmt(call(ident("print"), xRef2)),
	)

	result := Rename(nil, body, map[string]VarID{"print": -1})

	require.Empty(t, result.Errors)
	require.Equal(t, 3, result.UniqueVarCount) // x1, y, x2
	require.NotEqual(t, x1.VarID, x2.VarID)
	require.Equal(t, x1.VarID, xRef1.VarID) // val y = x → first x
	require.Equal(t, x2.VarID, xRef2.VarID) // print(x) → second x
}

func TestFunctionParameters(t *testing.T) {
	// fn f(a, b) { print(a) }
	a := identPat("a")
	b := identPat("b")
	aRef := ident("a")

	params := []*ast.Param{
		{Pattern: a, Optional: false},
		{Pattern: b, Optional: false},
	}
	body := block(
		exprStmt(call(ident("print"), aRef)),
	)

	result := Rename(params, body, map[string]VarID{"print": -1})

	require.Empty(t, result.Errors)
	require.Equal(t, 2, result.UniqueVarCount) // a, b
	require.NotEqual(t, a.VarID, b.VarID)
	require.Equal(t, a.VarID, aRef.VarID) // print(a) → parameter a
}

func TestDestructuring(t *testing.T) {
	// val {a, b} = obj
	a := ast.NewObjShorthandPat(ast.NewIdentifier("a", span()), nil, nil, span())
	b := ast.NewObjShorthandPat(ast.NewIdentifier("b", span()), nil, nil, span())
	objPat := ast.NewObjectPat([]ast.ObjPatElem{a, b}, span())

	body := block(
		valDecl(objPat, ident("obj")),
	)

	result := Rename(nil, body, map[string]VarID{"obj": -1})

	require.Empty(t, result.Errors)
	require.Equal(t, 2, result.UniqueVarCount)
	require.NotEqual(t, a.VarID, b.VarID)
	require.NotEqual(t, 0, a.VarID)
	require.NotEqual(t, 0, b.VarID)
}

func TestUnresolvedLocalName(t *testing.T) {
	// print(unknown)
	body := block(
		exprStmt(call(ident("print"), ident("unknown"))),
	)

	result := Rename(nil, body, map[string]VarID{"print": -1})

	require.Equal(t, []RenameError{
		{Name: "unknown", Span: span()},
	}, result.Errors)
}

func TestModuleLevelName(t *testing.T) {
	// print(globalVar)
	gRef := ident("globalVar")

	body := block(
		exprStmt(call(ident("print"), gRef)),
	)

	result := Rename(nil, body, map[string]VarID{"print": -1, "globalVar": -2})

	require.Empty(t, result.Errors)
	require.Equal(t, -2, gRef.VarID) // resolved to outer binding
}

func TestScopeRestorationAfterBlock(t *testing.T) {
	// val x = 1; do { val x = 2 }; print(x)
	outerX := identPat("x")
	innerX := identPat("x")
	xRef := ident("x")

	body := block(
		valDecl(outerX, numLit(1)),
		exprStmt(&ast.DoExpr{Body: block(
			valDecl(innerX, numLit(2)),
		)}),
		exprStmt(call(ident("print"), xRef)),
	)

	result := Rename(nil, body, map[string]VarID{"print": -1})

	require.Empty(t, result.Errors)
	require.Equal(t, outerX.VarID, xRef.VarID) // print(x) → outer x
	require.NotEqual(t, innerX.VarID, xRef.VarID)
}

func TestForInLoop(t *testing.T) {
	// for item in items { print(item) }
	item := identPat("item")
	itemRef := ident("item")

	body := block(
		ast.NewForInStmt(item, ident("items"), block(
			exprStmt(call(ident("print"), itemRef)),
		), false, span()),
	)

	result := Rename(nil, body, map[string]VarID{"print": -1, "items": -2})

	require.Empty(t, result.Errors)
	require.Equal(t, item.VarID, itemRef.VarID)
	require.Equal(t, 1, result.UniqueVarCount) // only item is a local variable
}

func TestForInLoopScopeIsolation(t *testing.T) {
	// for item in items { print(item) }; print(item)  // second print should fail
	item := identPat("item")
	itemRefInside := ident("item")
	itemRefOutside := ident("item")

	body := block(
		ast.NewForInStmt(item, ident("items"), block(
			exprStmt(call(ident("print"), itemRefInside)),
		), false, span()),
		exprStmt(call(ident("print"), itemRefOutside)),
	)

	result := Rename(nil, body, map[string]VarID{"print": -1, "items": -2})

	require.Equal(t, []RenameError{
		{Name: "item", Span: span()},
	}, result.Errors)
	require.Equal(t, item.VarID, itemRefInside.VarID) // inside resolves fine
}

func TestMatchExpr(t *testing.T) {
	// match x { Some(v) => print(v), None => 0 }
	v := identPat("v")
	vRef := ident("v")

	matchExpr := ast.NewMatch(
		ident("x"),
		[]*ast.MatchCase{
			ast.NewMatchCase(
				ast.NewExtractorPat(ast.NewIdentifier("Some", span()), []ast.Pat{v}, span()),
				nil,
				ast.BlockOrExpr{Expr: call(ident("print"), vRef)},
				span(),
			),
			ast.NewMatchCase(
				identPat("_"),
				nil,
				ast.BlockOrExpr{Expr: numLit(0)},
				span(),
			),
		},
		span(),
	)

	body := block(exprStmt(matchExpr))

	result := Rename(nil, body, map[string]VarID{"x": -1, "print": -2})

	require.Empty(t, result.Errors)
	require.Equal(t, v.VarID, vRef.VarID)
}

func TestTupleDestructuring(t *testing.T) {
	// val [a, b] = pair
	a := identPat("a")
	b := identPat("b")
	tuplePat := ast.NewTuplePat([]ast.Pat{a, b}, span())

	body := block(
		valDecl(tuplePat, ident("pair")),
	)

	result := Rename(nil, body, map[string]VarID{"pair": -1})

	require.Empty(t, result.Errors)
	require.Equal(t, 2, result.UniqueVarCount)
	require.NotEqual(t, a.VarID, b.VarID)
}

func TestFuncDeclBindsName(t *testing.T) {
	// fn add(a, b) { a + b }; add(1, 2)
	addRef := ident("add")

	funcDecl := ast.NewFuncDecl(
		ast.NewIdentifier("add", span()),
		nil, // type params
		[]*ast.Param{
			{Pattern: identPat("a")},
			{Pattern: identPat("b")},
		},
		nil,   // return type
		nil,   // throws type
		&ast.Block{Stmts: nil, Span: span()}, // body (not walked)
		false, // export
		false, // declare
		false, // async
		span(),
	)

	body := block(
		ast.NewDeclStmt(funcDecl, span()),
		exprStmt(call(addRef, numLit(1), numLit(2))),
	)

	result := Rename(nil, body, map[string]VarID{})

	require.Empty(t, result.Errors)
	require.NotEqual(t, 0, addRef.VarID) // add is resolved
}

func TestIfLetPattern(t *testing.T) {
	// if let Some(v) = x { print(v) }
	v := identPat("v")
	vRef := ident("v")

	ifLetExpr := ast.NewIfLet(
		ast.NewExtractorPat(ast.NewIdentifier("Some", span()), []ast.Pat{v}, span()),
		ident("x"),
		block(exprStmt(call(ident("print"), vRef))),
		nil,
		span(),
	)

	body := block(exprStmt(ifLetExpr))

	result := Rename(nil, body, map[string]VarID{"x": -1, "print": -2})

	require.Empty(t, result.Errors)
	require.Equal(t, v.VarID, vRef.VarID)
}

func TestAssignmentResolvesExistingBinding(t *testing.T) {
	// var x = 1; x = 2
	x := identPat("x")
	xAssignTarget := ident("x")

	assignExpr := ast.NewBinary(xAssignTarget, numLit(2), ast.Assign, span())

	body := block(
		varDecl(x, numLit(1)),
		exprStmt(assignExpr),
	)

	result := Rename(nil, body, map[string]VarID{})

	require.Empty(t, result.Errors)
	require.Equal(t, x.VarID, xAssignTarget.VarID)
	require.Equal(t, 1, result.UniqueVarCount) // only one variable
}

func TestObjKeyValueDestructuring(t *testing.T) {
	// val {key: value} = obj
	value := identPat("value")
	objPat := ast.NewObjectPat([]ast.ObjPatElem{
		ast.NewObjKeyValuePat(ast.NewIdentifier("key", span()), value, span()),
	}, span())

	body := block(
		valDecl(objPat, ident("obj")),
	)

	result := Rename(nil, body, map[string]VarID{"obj": -1})

	require.Empty(t, result.Errors)
	require.NotEqual(t, 0, value.VarID)
	require.Equal(t, 1, result.UniqueVarCount)
}

func TestNestedFuncExprNotWalked(t *testing.T) {
	// fn outer() {
	//   val x = 1
	//   val f = fn (y) { x + y }
	//   f(x)
	// }
	x := identPat("x")
	f := identPat("f")
	fRef := ident("f")
	xRef := ident("x") // in f(x)

	// These are inside the nested function body — should NOT be touched.
	innerXRef := ident("x")
	yParam := identPat("y")
	yRef := ident("y")

	innerBody := block(
		exprStmt(ast.NewBinary(innerXRef, yRef, ast.Plus, span())),
	)
	funcExpr := ast.NewFuncExpr(
		nil, // type params
		[]*ast.Param{{Pattern: yParam}},
		nil,   // return
		nil,   // throws
		false, // async
		&innerBody,
		span(),
	)

	body := block(
		valDecl(x, numLit(1)),
		valDecl(f, funcExpr),
		exprStmt(call(fRef, xRef)),
	)

	result := Rename(nil, body, map[string]VarID{})

	require.Empty(t, result.Errors)
	require.Equal(t, 2, result.UniqueVarCount) // x, f (not y)
	require.Equal(t, x.VarID, xRef.VarID)      // f(x) → outer x
	require.NotEqual(t, 0, f.VarID)
	require.Equal(t, f.VarID, fRef.VarID)

	// Inner function nodes should be untouched by the outer rename pass.
	require.Equal(t, 0, yParam.VarID)
	require.Equal(t, 0, yRef.VarID)
	require.Equal(t, 0, innerXRef.VarID)
}

func TestNestedFuncDeclNotWalked(t *testing.T) {
	// fn outer() {
	//   val x = 1
	//   fn inner(y) { x + y }
	//   inner(x)
	// }
	x := identPat("x")
	xRef := ident("x")       // in inner(x)
	innerRef := ident("inner") // in inner(x)

	// Inside the nested function body — should NOT be touched.
	innerXRef := ident("x")
	yParam := identPat("y")
	yRef := ident("y")

	innerBody := ast.Block{
		Stmts: []ast.Stmt{
			exprStmt(ast.NewBinary(innerXRef, yRef, ast.Plus, span())),
		},
		Span: span(),
	}
	innerFuncDecl := ast.NewFuncDecl(
		ast.NewIdentifier("inner", span()),
		nil, // type params
		[]*ast.Param{{Pattern: yParam}},
		nil,   // return
		nil,   // throws
		&innerBody,
		false, // export
		false, // declare
		false, // async
		span(),
	)

	body := block(
		valDecl(x, numLit(1)),
		ast.NewDeclStmt(innerFuncDecl, span()),
		exprStmt(call(innerRef, xRef)),
	)

	result := Rename(nil, body, map[string]VarID{})

	require.Empty(t, result.Errors)
	require.Equal(t, 2, result.UniqueVarCount) // x, inner (not y)
	require.Equal(t, x.VarID, xRef.VarID)       // inner(x) → outer x
	require.NotEqual(t, 0, innerRef.VarID) // inner is resolved

	// Inner function nodes should be untouched by the outer rename pass.
	require.Equal(t, 0, yParam.VarID)
	require.Equal(t, 0, yRef.VarID)
	require.Equal(t, 0, innerXRef.VarID)
}

func TestIfElseShadowing(t *testing.T) {
	// val x = 1
	// val y = if cond {
	//   val x = 2
	//   x
	// } else {
	//   val x = 3
	//   x
	// }
	// print(x)
	outerX := identPat("x")
	consX := identPat("x")
	consXRef := ident("x")
	altX := identPat("x")
	altXRef := ident("x")
	outerXRef := ident("x")

	altBlock := ast.Block{
		Stmts: []ast.Stmt{
			valDecl(altX, numLit(3)),
			exprStmt(altXRef),
		},
		Span: span(),
	}
	ifElseExpr := ast.NewIfElse(
		ident("cond"),
		block(
			valDecl(consX, numLit(2)),
			exprStmt(consXRef),
		),
		&ast.BlockOrExpr{Block: &altBlock},
		span(),
	)

	body := block(
		valDecl(outerX, numLit(1)),
		valDecl(identPat("y"), ifElseExpr),
		exprStmt(call(ident("print"), outerXRef)),
	)

	result := Rename(nil, body, map[string]VarID{"cond": -1, "print": -2})

	require.Empty(t, result.Errors)
	// outerX, consX, altX, y = 4 unique local variables
	require.Equal(t, 4, result.UniqueVarCount)

	// All three x's have distinct VarIDs.
	require.NotEqual(t, outerX.VarID, consX.VarID)
	require.NotEqual(t, outerX.VarID, altX.VarID)
	require.NotEqual(t, consX.VarID, altX.VarID)

	// Each branch's reference resolves to its own shadow.
	require.Equal(t, consX.VarID, consXRef.VarID)
	require.Equal(t, altX.VarID, altXRef.VarID)

	// After the if/else, the outer x is visible again.
	require.Equal(t, outerX.VarID, outerXRef.VarID)
}

func TestJSXExpressions(t *testing.T) {
	// val name = "world"
	// val el = <div class={name}>{name}</div>
	name := identPat("name")
	attrRef := ident("name")
	childRef := ident("name")

	attrValue := ast.JSXAttrValue(ast.NewJSXExprContainer(attrRef, span()))
	jsxExpr := ast.NewJSXElement(
		ast.NewJSXOpening(
			ast.NewIdentifier("div", span()),
			[]ast.JSXAttrElem{
				ast.NewJSXAttr("class", &attrValue, span()),
			},
			false,
			span(),
		),
		ast.NewJSXClosing(ast.NewIdentifier("div", span()), span()),
		[]ast.JSXChild{
			ast.NewJSXExprContainer(childRef, span()),
		},
		span(),
	)

	body := block(
		valDecl(name, ast.NewLitExpr(ast.NewString("world", span()))),
		valDecl(identPat("el"), jsxExpr),
	)

	result := Rename(nil, body, map[string]VarID{})

	require.Empty(t, result.Errors)
	require.Equal(t, name.VarID, attrRef.VarID)  // attribute expression
	require.Equal(t, name.VarID, childRef.VarID)  // child expression
}
