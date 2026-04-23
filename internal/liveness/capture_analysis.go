package liveness

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
)

// CaptureInfo describes how a closure captures a variable from the
// enclosing scope.
type CaptureInfo struct {
	Name      string // variable name as it appears in the source
	IsMutable bool   // true if the closure writes to the captured variable
}

// AnalyzeCaptures walks a function expression's body and determines which
// variables are captured from the enclosing scope and whether each capture
// is mutable (the closure writes to the captured variable) or read-only.
//
// A variable is considered captured if its IdentExpr has a negative VarID
// (assigned by the rename pass to indicate outer/non-local bindings).
//
// A capture is mutable if the captured variable appears on the left side
// of an assignment or as the root of a member/index expression that is
// assigned to (e.g. `captured.prop = value`).
func AnalyzeCaptures(funcExpr *ast.FuncExpr) []CaptureInfo {
	if funcExpr.Body == nil || len(funcExpr.Body.Stmts) == 0 {
		return nil
	}

	// Track which outer names are referenced and whether they're mutated.
	captures := make(map[string]bool) // name → isMutable

	walkBody(funcExpr.Body.Stmts, captures)

	// Convert map to sorted slice for deterministic output.
	result := make([]CaptureInfo, 0, len(captures))
	for name, isMutable := range captures {
		result = append(result, CaptureInfo{Name: name, IsMutable: isMutable})
	}
	slices.SortFunc(result, func(a, b CaptureInfo) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return result
}

// walkBody walks a list of statements looking for captured variable uses.
func walkBody(stmts []ast.Stmt, captures map[string]bool) {
	for _, stmt := range stmts {
		walkStmt(stmt, captures)
	}
}

func walkStmt(stmt ast.Stmt, captures map[string]bool) {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		walkExpr(s.Expr, captures)
	case *ast.DeclStmt:
		walkDecl(s.Decl, captures)
	case *ast.ReturnStmt:
		if s.Expr != nil {
			walkExpr(s.Expr, captures)
		}
	case *ast.ForInStmt:
		walkExpr(s.Iterable, captures)
		walkBody(s.Body.Stmts, captures)
	}
}

func walkDecl(decl ast.Decl, captures map[string]bool) {
	switch d := decl.(type) {
	case *ast.VarDecl:
		if d.Init != nil {
			walkExpr(d.Init, captures)
		}
	// FuncDecl: don't recurse into nested function bodies — they get
	// their own capture analysis.
	}
}

func walkExpr(expr ast.Expr, captures map[string]bool) {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		if e.VarID < 0 {
			// Outer reference — captured variable (read).
			if _, exists := captures[e.Name]; !exists {
				captures[e.Name] = false
			}
		}
	case *ast.BinaryExpr:
		if e.Op == ast.Assign {
			// Check if LHS is a captured variable (mutable capture).
			markMutableCapture(e.Left, captures)
			walkExpr(e.Right, captures)
		} else {
			walkExpr(e.Left, captures)
			walkExpr(e.Right, captures)
		}
	case *ast.UnaryExpr:
		walkExpr(e.Arg, captures)
	case *ast.CallExpr:
		walkExpr(e.Callee, captures)
		for _, arg := range e.Args {
			walkExpr(arg, captures)
		}
	case *ast.MemberExpr:
		walkExpr(e.Object, captures)
	case *ast.IndexExpr:
		walkExpr(e.Object, captures)
		walkExpr(e.Index, captures)
	case *ast.TupleExpr:
		for _, elem := range e.Elems {
			walkExpr(elem, captures)
		}
	case *ast.ObjectExpr:
		for _, elem := range e.Elems {
			walkObjExprElem(elem, captures)
		}
	case *ast.IfElseExpr:
		walkExpr(e.Cond, captures)
		walkBody(e.Cons.Stmts, captures)
		if e.Alt != nil {
			walkBlockOrExpr(e.Alt, captures)
		}
	case *ast.IfLetExpr:
		walkExpr(e.Target, captures)
		walkBody(e.Cons.Stmts, captures)
		if e.Alt != nil {
			walkBlockOrExpr(e.Alt, captures)
		}
	case *ast.MatchExpr:
		walkExpr(e.Target, captures)
		for _, mc := range e.Cases {
			if mc.Guard != nil {
				walkExpr(mc.Guard, captures)
			}
			walkBlockOrExpr(&mc.Body, captures)
		}
	case *ast.TryCatchExpr:
		walkBody(e.Try.Stmts, captures)
		for _, mc := range e.Catch {
			if mc.Guard != nil {
				walkExpr(mc.Guard, captures)
			}
			walkBlockOrExpr(&mc.Body, captures)
		}
	case *ast.DoExpr:
		walkBody(e.Body.Stmts, captures)
	case *ast.ThrowExpr:
		walkExpr(e.Arg, captures)
	case *ast.AwaitExpr:
		walkExpr(e.Arg, captures)
	case *ast.YieldExpr:
		if e.Value != nil {
			walkExpr(e.Value, captures)
		}
	case *ast.TemplateLitExpr:
		for _, expr := range e.Exprs {
			walkExpr(expr, captures)
		}
	case *ast.TaggedTemplateLitExpr:
		walkExpr(e.Tag, captures)
		for _, expr := range e.Exprs {
			walkExpr(expr, captures)
		}
	case *ast.TypeCastExpr:
		walkExpr(e.Expr, captures)
	case *ast.ArraySpreadExpr:
		walkExpr(e.Value, captures)
	case *ast.FuncExpr:
		// Don't recurse into nested function bodies — they get their
		// own capture analysis when inferred.
	case *ast.LiteralExpr:
		// No variables.
	}
}

func walkBlockOrExpr(boe *ast.BlockOrExpr, captures map[string]bool) {
	if boe.Block != nil {
		walkBody(boe.Block.Stmts, captures)
	}
	if boe.Expr != nil {
		walkExpr(boe.Expr, captures)
	}
}

func walkObjExprElem(elem ast.ObjExprElem, captures map[string]bool) {
	switch e := elem.(type) {
	case *ast.PropertyExpr:
		if e.Value != nil {
			walkExpr(e.Value, captures)
		} else {
			// Shorthand property {x} — the name is also a variable reference.
			if ident, ok := e.Name.(*ast.IdentExpr); ok && ident.VarID < 0 {
				if _, exists := captures[ident.Name]; !exists {
					captures[ident.Name] = false
				}
			}
		}
	case *ast.ObjSpreadExpr:
		walkExpr(e.Value, captures)
	}
}

// markMutableCapture marks a captured variable as mutably captured when it
// appears on the left side of an assignment.
func markMutableCapture(lhs ast.Expr, captures map[string]bool) {
	switch e := lhs.(type) {
	case *ast.IdentExpr:
		if e.VarID < 0 {
			captures[e.Name] = true
		}
	case *ast.MemberExpr:
		// obj.prop = value — obj is mutably captured
		markMutableCapture(e.Object, captures)
	case *ast.IndexExpr:
		// obj[idx] = value — obj is mutably captured
		markMutableCapture(e.Object, captures)
		walkExpr(e.Index, captures)
	}
}
