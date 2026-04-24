package liveness

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
)

// CaptureInfo describes how a closure captures a variable from the
// enclosing scope.
type CaptureInfo struct {
	ID        VarID  // unique binding identity (negative for outer references)
	Name      string // variable name as it appears in the source (diagnostics only)
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

	// Track which outer bindings are referenced and whether they're mutated.
	// Keyed by VarID so shadowed bindings remain distinct.
	captures := make(map[VarID]CaptureInfo)

	walkBody(funcExpr.Body.Stmts, captures)

	// Convert map to sorted slice for deterministic output.
	result := make([]CaptureInfo, 0, len(captures))
	for _, info := range captures {
		result = append(result, info)
	}
	slices.SortFunc(result, func(a, b CaptureInfo) int {
		if a.ID != b.ID {
			return cmp.Compare(a.ID, b.ID)
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return result
}

// walkBody walks a list of statements looking for captured variable uses.
func walkBody(stmts []ast.Stmt, captures map[VarID]CaptureInfo) {
	for _, stmt := range stmts {
		walkStmt(stmt, captures)
	}
}

func walkStmt(stmt ast.Stmt, captures map[VarID]CaptureInfo) {
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
	default:
		panic(fmt.Sprintf("walkStmt: unhandled statement type %T", stmt))
	}
}

func walkDecl(decl ast.Decl, captures map[VarID]CaptureInfo) {
	switch d := decl.(type) {
	case *ast.VarDecl:
		if d.Init != nil {
			walkExpr(d.Init, captures)
		}
	case *ast.FuncDecl:
		// Don't recurse into nested function bodies — they get
		// their own capture analysis.
	default:
		panic(fmt.Sprintf("walkDecl: unhandled declaration type %T", decl))
	}
}

func walkExpr(expr ast.Expr, captures map[VarID]CaptureInfo) {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		if e.VarID < 0 {
			// Outer reference — captured variable (read).
			id := VarID(e.VarID)
			if _, exists := captures[id]; !exists {
				captures[id] = CaptureInfo{ID: id, Name: e.Name, IsMutable: false}
			}
		}
	case *ast.BinaryExpr:
		if e.Op == ast.Assign {
			// Walk the LHS to detect reads (e.g. captured variable used as
			// a callee or index: getObj(captured).field = value).
			walkExpr(e.Left, captures)
			// Then mark the assignment root as a mutable capture.
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
	case *ast.JSXElementExpr:
		for _, attr := range e.Opening.Attrs {
			switch a := attr.(type) {
			case *ast.JSXAttr:
				if a.Value != nil {
					switch av := (*a.Value).(type) {
					case *ast.JSXExprContainer:
						walkExpr(av.Expr, captures)
					case *ast.JSXElementExpr:
						walkExpr(av, captures)
					case *ast.JSXFragmentExpr:
						walkExpr(av, captures)
					}
				}
			case *ast.JSXSpreadAttr:
				walkExpr(a.Expr, captures)
			}
		}
		for _, child := range e.Children {
			switch ch := child.(type) {
			case *ast.JSXExprContainer:
				walkExpr(ch.Expr, captures)
			case *ast.JSXElementExpr:
				walkExpr(ch, captures)
			case *ast.JSXFragmentExpr:
				walkExpr(ch, captures)
			}
		}
	case *ast.JSXFragmentExpr:
		for _, child := range e.Children {
			switch ch := child.(type) {
			case *ast.JSXExprContainer:
				walkExpr(ch.Expr, captures)
			case *ast.JSXElementExpr:
				walkExpr(ch, captures)
			case *ast.JSXFragmentExpr:
				walkExpr(ch, captures)
			}
		}
	case *ast.FuncExpr:
		// Don't recurse into nested function bodies — they get their
		// own capture analysis when inferred.
	case *ast.LiteralExpr:
		// No variables.
	case *ast.ErrorExpr:
		// No variables.
	default:
		panic(fmt.Sprintf("walkExpr: unhandled expression type %T", expr))
	}
}

func walkBlockOrExpr(boe *ast.BlockOrExpr, captures map[VarID]CaptureInfo) {
	if boe.Block != nil {
		walkBody(boe.Block.Stmts, captures)
	}
	if boe.Expr != nil {
		walkExpr(boe.Expr, captures)
	}
}

func walkObjExprElem(elem ast.ObjExprElem, captures map[VarID]CaptureInfo) {
	switch e := elem.(type) {
	case *ast.PropertyExpr:
		if e.Value != nil {
			walkExpr(e.Value, captures)
		} else {
			// Shorthand property {x} — the name is also a variable reference.
			if ident, ok := e.Name.(*ast.IdentExpr); ok && ident.VarID < 0 {
				id := VarID(ident.VarID)
				if _, exists := captures[id]; !exists {
					captures[id] = CaptureInfo{ID: id, Name: ident.Name, IsMutable: false}
				}
			}
		}
	case *ast.ObjSpreadExpr:
		walkExpr(e.Value, captures)
	case *ast.CallableExpr:
		// Don't recurse into nested function bodies.
	case *ast.ConstructorExpr:
		// Don't recurse into nested function bodies.
	case *ast.MethodExpr:
		// Don't recurse into nested function bodies.
	case *ast.GetterExpr:
		// Don't recurse into nested function bodies.
	case *ast.SetterExpr:
		// Don't recurse into nested function bodies.
	default:
		panic(fmt.Sprintf("walkObjExprElem: unhandled element type %T", elem))
	}
}

// markMutableCapture marks a captured variable as mutably captured when it
// appears on the left side of an assignment.
func markMutableCapture(lhs ast.Expr, captures map[VarID]CaptureInfo) {
	switch e := lhs.(type) {
	case *ast.IdentExpr:
		if e.VarID < 0 {
			id := VarID(e.VarID)
			info := captures[id]
			info.ID = id
			info.Name = e.Name
			info.IsMutable = true
			captures[id] = info
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
