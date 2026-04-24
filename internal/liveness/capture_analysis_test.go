package liveness

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseFuncExpr parses a script and extracts the first FuncExpr from the
// first variable declaration's initializer. The function body must go through
// a rename pass for VarIDs to be set, so we run Rename on both the outer
// scope and the inner function.
func parseFuncExpr(t *testing.T, input string) *ast.FuncExpr {
	t.Helper()
	source := &ast.Source{ID: 0, Path: "test.esc", Contents: input}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	script, parseErrors := p.ParseScript()
	require.Empty(t, parseErrors, "Expected no parse errors")

	// Run rename on the outer scope first (to assign VarIDs to outer vars).
	outerBindings := make(map[string]VarID)
	Rename(nil, ast.Block{Stmts: script.Stmts}, outerBindings)

	// Find the FuncExpr.
	for _, stmt := range script.Stmts {
		if declStmt, ok := stmt.(*ast.DeclStmt); ok {
			if varDecl, ok := declStmt.Decl.(*ast.VarDecl); ok {
				if funcExpr, ok := varDecl.Init.(*ast.FuncExpr); ok {
					// Run rename on the inner function body to set VarIDs.
					innerOuter := make(map[string]VarID)
					// Add outer variable names as outer bindings with negative IDs.
					nextID := VarID(-1)
					for _, s := range script.Stmts {
						if ds, ok := s.(*ast.DeclStmt); ok {
							if vd, ok := ds.Decl.(*ast.VarDecl); ok {
								if ip, ok := vd.Pattern.(*ast.IdentPat); ok {
									innerOuter[ip.Name] = nextID
									nextID--
								}
							}
						}
					}
					Rename(funcExpr.FuncSig.Params, *funcExpr.Body, innerOuter)
					return funcExpr
				}
			}
		}
	}
	t.Fatal("No FuncExpr found in input")
	return nil
}

func TestAnalyzeCaptures_ReadOnly(t *testing.T) {
	funcExpr := parseFuncExpr(t, `
		val items = [1, 2, 3]
		val f = fn() { items }
	`)

	captures := AnalyzeCaptures(funcExpr)
	require.Len(t, captures, 1)
	assert.Equal(t, "items", captures[0].Name)
	assert.False(t, captures[0].IsMutable)
}

func TestAnalyzeCaptures_MutableDirectAssign(t *testing.T) {
	funcExpr := parseFuncExpr(t, `
		var count = 0
		val f = fn() { count = count + 1 }
	`)

	captures := AnalyzeCaptures(funcExpr)
	require.Len(t, captures, 1)
	assert.Equal(t, "count", captures[0].Name)
	assert.True(t, captures[0].IsMutable)
}

func TestAnalyzeCaptures_MutablePropertyAssign(t *testing.T) {
	funcExpr := parseFuncExpr(t, `
		val obj = {x: 0}
		val f = fn() { obj.x = 1 }
	`)

	captures := AnalyzeCaptures(funcExpr)
	require.Len(t, captures, 1)
	assert.Equal(t, "obj", captures[0].Name)
	assert.True(t, captures[0].IsMutable)
}

func TestAnalyzeCaptures_NoCaptures(t *testing.T) {
	funcExpr := parseFuncExpr(t, `
		val f = fn(x: number) { x + 1 }
	`)

	captures := AnalyzeCaptures(funcExpr)
	assert.Empty(t, captures)
}

func TestAnalyzeCaptures_EmptyBody(t *testing.T) {
	funcExpr := parseFuncExpr(t, `
		val f = fn() {}
	`)

	captures := AnalyzeCaptures(funcExpr)
	assert.Empty(t, captures)
}

func TestAnalyzeCaptures_ReadInAssignmentLHS(t *testing.T) {
	// When a captured variable is read on the LHS of an assignment (e.g. as
	// a callee: `getObj().field = value`), it should be detected as a capture.
	// We construct the AST manually because parseFuncExpr picks the first
	// FuncExpr, which would be getObj's function, not f's.

	// Build: fn() { getObj().field = 1 }
	// where getObj is an outer reference (VarID = -1)
	getObjIdent := ast.NewIdent("getObj", ast.Span{})
	getObjIdent.VarID = -1

	callExpr := ast.NewCall(getObjIdent, nil, false, ast.Span{})
	memberExpr := ast.NewMember(callExpr, ast.NewIdentifier("field", ast.Span{}), false, ast.Span{})
	assignExpr := ast.NewBinary(memberExpr, ast.NewLitExpr(ast.NewNumber(1, ast.Span{})), ast.Assign, ast.Span{})

	body := &ast.Block{Stmts: []ast.Stmt{ast.NewExprStmt(assignExpr, ast.Span{})}}
	funcExpr := ast.NewFuncExpr(nil, nil, nil, nil, false, body, ast.Span{})

	captures := AnalyzeCaptures(funcExpr)
	require.Len(t, captures, 1)
	assert.Equal(t, "getObj", captures[0].Name)
	assert.False(t, captures[0].IsMutable) // read as callee, not mutated
}

func TestAnalyzeCaptures_JSXElement_ExprInChild(t *testing.T) {
	// Build: fn() { <div>{captured}</div> }
	// where captured is an outer reference (VarID = -1)
	capturedIdent := ast.NewIdent("captured", ast.Span{})
	capturedIdent.VarID = -1

	exprContainer := ast.NewJSXExprContainer(capturedIdent, ast.Span{})
	opening := ast.NewJSXOpening(nil, nil, false, ast.Span{})
	closing := ast.NewJSXClosing(nil, ast.Span{})
	jsxElem := ast.NewJSXElement(opening, closing, []ast.JSXChild{exprContainer}, ast.Span{})

	body := &ast.Block{Stmts: []ast.Stmt{ast.NewExprStmt(jsxElem, ast.Span{})}}
	funcExpr := ast.NewFuncExpr(nil, nil, nil, nil, false, body, ast.Span{})

	captures := AnalyzeCaptures(funcExpr)
	require.Len(t, captures, 1)
	assert.Equal(t, "captured", captures[0].Name)
	assert.False(t, captures[0].IsMutable)
}

func TestAnalyzeCaptures_JSXElement_ExprInAttr(t *testing.T) {
	// Build: fn() { <div prop={captured} /> }
	capturedIdent := ast.NewIdent("captured", ast.Span{})
	capturedIdent.VarID = -1

	exprContainer := ast.NewJSXExprContainer(capturedIdent, ast.Span{})
	var attrValue ast.JSXAttrValue = exprContainer
	attr := ast.NewJSXAttr("prop", &attrValue, ast.Span{})
	opening := ast.NewJSXOpening(nil, []ast.JSXAttrElem{attr}, true, ast.Span{})
	jsxElem := ast.NewJSXElement(opening, nil, nil, ast.Span{})

	body := &ast.Block{Stmts: []ast.Stmt{ast.NewExprStmt(jsxElem, ast.Span{})}}
	funcExpr := ast.NewFuncExpr(nil, nil, nil, nil, false, body, ast.Span{})

	captures := AnalyzeCaptures(funcExpr)
	require.Len(t, captures, 1)
	assert.Equal(t, "captured", captures[0].Name)
	assert.False(t, captures[0].IsMutable)
}

func TestAnalyzeCaptures_JSXElement_SpreadAttr(t *testing.T) {
	// Build: fn() { <div {...captured} /> }
	capturedIdent := ast.NewIdent("captured", ast.Span{})
	capturedIdent.VarID = -1

	spreadAttr := ast.NewJSXSpreadAttr(capturedIdent, ast.Span{})
	opening := ast.NewJSXOpening(nil, []ast.JSXAttrElem{spreadAttr}, true, ast.Span{})
	jsxElem := ast.NewJSXElement(opening, nil, nil, ast.Span{})

	body := &ast.Block{Stmts: []ast.Stmt{ast.NewExprStmt(jsxElem, ast.Span{})}}
	funcExpr := ast.NewFuncExpr(nil, nil, nil, nil, false, body, ast.Span{})

	captures := AnalyzeCaptures(funcExpr)
	require.Len(t, captures, 1)
	assert.Equal(t, "captured", captures[0].Name)
	assert.False(t, captures[0].IsMutable)
}

func TestAnalyzeCaptures_JSXFragment_ExprInChild(t *testing.T) {
	// Build: fn() { <>{captured}</> }
	capturedIdent := ast.NewIdent("captured", ast.Span{})
	capturedIdent.VarID = -1

	exprContainer := ast.NewJSXExprContainer(capturedIdent, ast.Span{})
	opening := ast.NewJSXOpening(nil, nil, false, ast.Span{})
	closing := ast.NewJSXClosing(nil, ast.Span{})
	jsxFrag := ast.NewJSXFragment(opening, closing, []ast.JSXChild{exprContainer}, ast.Span{})

	body := &ast.Block{Stmts: []ast.Stmt{ast.NewExprStmt(jsxFrag, ast.Span{})}}
	funcExpr := ast.NewFuncExpr(nil, nil, nil, nil, false, body, ast.Span{})

	captures := AnalyzeCaptures(funcExpr)
	require.Len(t, captures, 1)
	assert.Equal(t, "captured", captures[0].Name)
	assert.False(t, captures[0].IsMutable)
}

func TestAnalyzeCaptures_ObjectWithMethod_NoCaptureInBody(t *testing.T) {
	// Build: fn() { {method() { innerVar }} }
	// where innerVar is local to the method, so no captures from the outer fn.
	// The method body should NOT be recursed into.
	innerIdent := ast.NewIdent("innerVar", ast.Span{})
	innerIdent.VarID = -1 // would be captured if we recurse

	methodBody := &ast.Block{Stmts: []ast.Stmt{ast.NewExprStmt(innerIdent, ast.Span{})}}
	methodFn := ast.NewFuncExpr(nil, nil, nil, nil, false, methodBody, ast.Span{})
	method := ast.NewMethod(ast.NewIdent("method", ast.Span{}), methodFn, nil, ast.Span{})

	objExpr := ast.NewObject([]ast.ObjExprElem{method}, ast.Span{})
	body := &ast.Block{Stmts: []ast.Stmt{ast.NewExprStmt(objExpr, ast.Span{})}}
	funcExpr := ast.NewFuncExpr(nil, nil, nil, nil, false, body, ast.Span{})

	captures := AnalyzeCaptures(funcExpr)
	assert.Empty(t, captures, "Method bodies should not be recursed into")
}

func TestAnalyzeCaptures_MultipleCaptures_SortedByName(t *testing.T) {
	funcExpr := parseFuncExpr(t, `
		val zebra = 1
		val alpha = 2
		val middle = 3
		val f = fn() { zebra + alpha + middle }
	`)

	captures := AnalyzeCaptures(funcExpr)
	require.Len(t, captures, 3)
	// Output should be sorted by name for deterministic results.
	assert.Equal(t, "alpha", captures[0].Name)
	assert.Equal(t, "middle", captures[1].Name)
	assert.Equal(t, "zebra", captures[2].Name)
}
