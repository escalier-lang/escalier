package liveness

import (
	"cmp"
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

	v := &captureVisitor{
		captures: make(map[VarID]CaptureInfo),
	}
	funcExpr.Body.Accept(v)

	// Convert map to sorted slice for deterministic output.
	result := make([]CaptureInfo, 0, len(v.captures))
	for _, info := range v.captures {
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

// captureVisitor implements ast.Visitor to walk a function body and collect
// captured variables. It uses the AST visitor's built-in traversal for most
// node types, only overriding behavior where capture analysis needs special
// handling.
type captureVisitor struct {
	ast.DefaultVisitor
	captures map[VarID]CaptureInfo
}

// recordCapture records a read-only capture for an outer variable reference.
// If the variable is already recorded (possibly as mutable), this is a no-op.
func (v *captureVisitor) recordCapture(varID int, name string) {
	id := VarID(varID)
	if _, exists := v.captures[id]; !exists {
		v.captures[id] = CaptureInfo{ID: id, Name: name, IsMutable: false}
	}
}

// markMutable marks a captured variable as mutably captured. This is called
// when the variable (or a property/index path rooted at it) appears on the
// left side of an assignment.
func (v *captureVisitor) markMutable(varID int, name string) {
	id := VarID(varID)
	info := v.captures[id]
	info.ID = id
	info.Name = name
	info.IsMutable = true
	v.captures[id] = info
}

// markMutableLHS walks the left-hand side of an assignment to find the root
// variable being mutated. For direct assignment (`x = ...`) the root is x.
// For property/index chains (`x.a.b = ...`, `x[i] = ...`) the root is x.
// Index sub-expressions on the path are walked normally to detect reads
// (e.g. `obj[captured] = ...` reads captured).
func (v *captureVisitor) markMutableLHS(lhs ast.Expr) {
	switch e := lhs.(type) {
	case *ast.IdentExpr:
		if e.VarID < 0 {
			v.markMutable(e.VarID, e.Name)
		}
	case *ast.MemberExpr:
		v.markMutableLHS(e.Object)
	case *ast.IndexExpr:
		v.markMutableLHS(e.Object)
		e.Index.Accept(v) // walk index for reads
	}
}

// EnterExpr handles three special cases and delegates the rest to the default
// visitor traversal:
//
//  1. IdentExpr with negative VarID — records a read capture.
//  2. BinaryExpr with Assign op — requires custom traversal because both
//     sides need normal walking (for read captures), but the assignment root
//     on the LHS must also be marked mutable. The default visitor would only
//     walk left and right without the mutable-marking step. We return false
//     to suppress the default traversal and do it ourselves.
//  3. FuncExpr — returns false to skip nested function bodies, which get
//     their own capture analysis.
func (v *captureVisitor) EnterExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		if e.VarID < 0 {
			v.recordCapture(e.VarID, e.Name)
		}
		return true
	case *ast.BinaryExpr:
		if e.Op == ast.Assign {
			// Assignment requires a two-pass treatment of the LHS:
			//
			// Pass 1 (read detection): Walk the full LHS tree normally so
			// that any captured variables used in sub-expressions are
			// recorded as read captures. For example, in
			//   getObj(captured).field = value
			// `captured` is read (not mutated).
			//
			// Pass 2 (mutable marking): Walk the LHS again via
			// markMutableLHS to find the assignment root and mark it as a
			// mutable capture. For example, in
			//   captured.field = value
			// `captured` is the root and is marked mutable.
			e.Left.Accept(v)
			v.markMutableLHS(e.Left)
			e.Right.Accept(v)
			return false // we already traversed the children
		}
		return true
	case *ast.FuncExpr:
		// Don't recurse into nested function bodies — they get their own
		// capture analysis when inferred.
		return false
	case *ast.JSXElementExpr:
		// JSXElementExpr.Accept does not yet recurse into children/attrs
		// (#490), so we walk them manually here.
		v.walkJSXAttrs(e.Opening.Attrs)
		v.walkJSXChildren(e.Children)
		return false
	case *ast.JSXFragmentExpr:
		// Same as JSXElementExpr — Accept does not recurse (#490).
		v.walkJSXChildren(e.Children)
		return false
	default:
		return true
	}
}

// EnterDecl returns false for FuncDecl to skip nested function bodies.
func (v *captureVisitor) EnterDecl(decl ast.Decl) bool {
	switch decl.(type) {
	case *ast.FuncDecl:
		return false
	default:
		return true
	}
}

// walkJSXAttrs walks JSX attributes for captured variable references.
// Remove once #490 is resolved and JSXElementExpr.Accept handles this.
func (v *captureVisitor) walkJSXAttrs(attrs []ast.JSXAttrElem) {
	for _, attr := range attrs {
		switch a := attr.(type) {
		case *ast.JSXAttr:
			if a.Value != nil {
				switch av := (*a.Value).(type) {
				case *ast.JSXExprContainer:
					av.Expr.Accept(v)
				case *ast.JSXElementExpr:
					av.Accept(v)
				case *ast.JSXFragmentExpr:
					av.Accept(v)
				}
			}
		case *ast.JSXSpreadAttr:
			a.Expr.Accept(v)
		}
	}
}

// walkJSXChildren walks JSX children for captured variable references.
// Remove once #490 is resolved and JSX Accept methods handle this.
func (v *captureVisitor) walkJSXChildren(children []ast.JSXChild) {
	for _, child := range children {
		switch ch := child.(type) {
		case *ast.JSXExprContainer:
			ch.Expr.Accept(v)
		case *ast.JSXElementExpr:
			ch.Accept(v)
		case *ast.JSXFragmentExpr:
			ch.Accept(v)
		}
	}
}

// EnterObjExprElem handles shorthand properties like {x}. The visitor
// normally skips visiting the IdentExpr key of a PropertyExpr (it's a
// label, not a reference). But for shorthand properties (Value == nil)
// the name IS a variable reference, so we must check it here.
func (v *captureVisitor) EnterObjExprElem(elem ast.ObjExprElem) bool {
	if prop, ok := elem.(*ast.PropertyExpr); ok && prop.Value == nil {
		if ident, ok := prop.Name.(*ast.IdentExpr); ok && ident.VarID < 0 {
			v.recordCapture(ident.VarID, ident.Name)
		}
	}
	return true
}
