package liveness

import (
	"fmt"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// findFuncExpr parses a script, finds the first FuncExpr in a variable
// declaration, and runs the rename pass on both the outer scope and the
// inner function body so that VarIDs are set correctly.
func findFuncExpr(t *testing.T, input string) *ast.FuncExpr {
	t.Helper()
	script := parseScript(t, input)

	// Run rename on the outer scope to assign VarIDs to outer vars.
	outerBindings := make(map[string]VarID)
	Rename(nil, ast.Block{Stmts: script.Stmts}, outerBindings)

	// Find the first FuncExpr in a variable declaration.
	for _, stmt := range script.Stmts {
		if declStmt, ok := stmt.(*ast.DeclStmt); ok {
			if varDecl, ok := declStmt.Decl.(*ast.VarDecl); ok {
				if funcExpr, ok := varDecl.Init.(*ast.FuncExpr); ok {
					// Collect outer variable names as negative-ID bindings
					// so the inner rename pass marks them as captures.
					innerOuter := make(map[string]VarID)
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
	t.Fatal("No FuncExpr found in script")
	return nil
}

// formatCaptures formats a []CaptureInfo as a readable string for test assertions.
// Each capture is shown as "name(mut)" or "name(immut)", sorted by VarID.
// Example: "items(immut), count(mut)"
func formatCaptures(captures []CaptureInfo) string {
	if len(captures) == 0 {
		return "[]"
	}
	parts := make([]string, len(captures))
	for i, c := range captures {
		mut := "immut"
		if c.IsMutable {
			mut = "mut"
		}
		parts[i] = fmt.Sprintf("%s(%s)", c.Name, mut)
	}
	return strings.Join(parts, ", ")
}

func TestAnalyzeCaptures_ReadOnly(t *testing.T) {
	funcExpr := findFuncExpr(t, `
		val items = [1, 2, 3]
		val f = fn() { items }
	`)

	// items is read but not written — immutable capture
	require.Equal(t, "items(immut)", formatCaptures(AnalyzeCaptures(funcExpr)))
}

func TestAnalyzeCaptures_MutableDirectAssign(t *testing.T) {
	funcExpr := findFuncExpr(t, `
		var count = 0
		val f = fn() { count = count + 1 }
	`)

	// count is assigned to inside the closure — mutable capture
	require.Equal(t, "count(mut)", formatCaptures(AnalyzeCaptures(funcExpr)))
}

func TestAnalyzeCaptures_MutablePropertyAssign(t *testing.T) {
	funcExpr := findFuncExpr(t, `
		val obj = {x: 0}
		val f = fn() { obj.x = 1 }
	`)

	// obj.x is assigned — obj is mutably captured
	require.Equal(t, "obj(mut)", formatCaptures(AnalyzeCaptures(funcExpr)))
}

func TestAnalyzeCaptures_NoCaptures(t *testing.T) {
	funcExpr := findFuncExpr(t, `
		val f = fn(x: number) { x + 1 }
	`)

	// No outer variables referenced — no captures
	require.Equal(t, "[]", formatCaptures(AnalyzeCaptures(funcExpr)))
}

func TestAnalyzeCaptures_ReadInAssignmentLHS(t *testing.T) {
	// Build manually because parseFuncExpr picks the first FuncExpr,
	// which would be getObj's function rather than f's.
	// Build: fn() { getObj().field = 1 }
	// where getObj is an outer reference (VarID = -1)
	getObjIdent := ast.NewIdent("getObj", ast.Span{})
	getObjIdent.VarID = -1

	callExpr := ast.NewCall(getObjIdent, nil, false, ast.Span{})
	memberExpr := ast.NewMember(callExpr, ast.NewIdentifier("field", ast.Span{}), false, ast.Span{})
	assignExpr := ast.NewBinary(memberExpr, ast.NewLitExpr(ast.NewNumber(1, ast.Span{})), ast.Assign, ast.Span{})

	body := &ast.Block{Stmts: []ast.Stmt{ast.NewExprStmt(assignExpr, ast.Span{})}}
	funcExpr := ast.NewFuncExpr(nil, nil, nil, nil, nil, false, body, ast.Span{})

	// getObj is called (read) on the LHS of an assignment, not mutated itself
	require.Equal(t, "getObj(immut)", formatCaptures(AnalyzeCaptures(funcExpr)))
}

func TestAnalyzeCaptures_JSXElement_ExprInChild(t *testing.T) {
	funcExpr := findFuncExpr(t, `
		val captured = 1
		val f = fn() { <div>{captured}</div> }
	`)

	// captured is used inside a JSX expression container in a child position
	require.Equal(t, "captured(immut)", formatCaptures(AnalyzeCaptures(funcExpr)))
}

func TestAnalyzeCaptures_JSXElement_ExprInAttr(t *testing.T) {
	funcExpr := findFuncExpr(t, `
		val captured = 1
		val f = fn() { <div prop={captured} /> }
	`)

	// captured is used inside a JSX attribute value
	require.Equal(t, "captured(immut)", formatCaptures(AnalyzeCaptures(funcExpr)))
}

func TestAnalyzeCaptures_JSXElement_SpreadAttr(t *testing.T) {
	funcExpr := findFuncExpr(t, `
		val captured = {a: 1}
		val f = fn() { <div {...captured} /> }
	`)

	// captured is used in a JSX spread attribute
	require.Equal(t, "captured(immut)", formatCaptures(AnalyzeCaptures(funcExpr)))
}

func TestAnalyzeCaptures_NestedFuncExpr_NotRecursedInto(t *testing.T) {
	// Build manually: fn() { {method() { outerRef }} }
	// The method body contains outerRef but should NOT be recursed into.
	outerRef := ast.NewIdent("outerRef", ast.Span{})
	outerRef.VarID = -1

	methodBody := &ast.Block{Stmts: []ast.Stmt{ast.NewExprStmt(outerRef, ast.Span{})}}
	methodFn := ast.NewFuncExpr(nil, nil, nil, nil, nil, false, methodBody, ast.Span{})
	method := ast.NewMethod(ast.NewIdent("method", ast.Span{}), methodFn, nil, ast.Span{})

	objExpr := ast.NewObject([]ast.ObjExprElem{method}, ast.Span{})
	body := &ast.Block{Stmts: []ast.Stmt{ast.NewExprStmt(objExpr, ast.Span{})}}
	funcExpr := ast.NewFuncExpr(nil, nil, nil, nil, nil, false, body, ast.Span{})

	// Nested function bodies are not walked — each gets its own capture analysis
	require.Equal(t, "[]", formatCaptures(AnalyzeCaptures(funcExpr)))
}

func TestAnalyzeCaptures_MultipleCaptures_SortedByVarID(t *testing.T) {
	funcExpr := findFuncExpr(t, `
		val zebra = 1
		val alpha = 2
		val middle = 3
		val f = fn() { zebra + alpha + middle }
	`)

	// Output is sorted by VarID (declaration order: zebra=-1, alpha=-2, middle=-3)
	// which means reverse alphabetical in this case
	require.Equal(t, "middle(immut), alpha(immut), zebra(immut)", formatCaptures(AnalyzeCaptures(funcExpr)))
}
