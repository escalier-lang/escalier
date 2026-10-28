package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// paramOnlyReadThrough reports whether every use of the parameter named name in body reads a
// member or an element of it, as `p.x` and `p[i]` do. It returns false when the body uses the
// parameter in any other way, and when the body never uses it.
//
// These uses count as other ways:
//   - a bare use, as in `return p`, `sink = p`, `f(p)`, `p + 1`, or a shorthand `{p}`
//   - a write through a member or element chain rooted at the parameter, as in `p.x = 1`
//   - a call through such a chain, as in `p.m()` or `p.f.g()`
//   - a `&mut` borrow of such a chain, as in `&mut p.x`
//   - any use inside a nested function or class, which may capture the parameter
//
// A body that binds the name again anywhere also returns false, since a use may then name
// the inner binding instead.
func paramOnlyReadThrough(name string, body *ast.Block) bool {
	v := &paramReadScanner{name: name, readThrough: set.NewSet[*ast.IdentExpr]()}
	body.Accept(v)
	return !v.other && v.uses > 0
}

// paramReadScanner walks a function body for paramOnlyReadThrough. readThrough holds each
// use of the parameter that is the object of a member or element read, recorded when the
// scanner enters the read, which is before it reaches the use.
type paramReadScanner struct {
	ast.DefaultVisitor
	name        string
	readThrough set.Set[*ast.IdentExpr]
	uses        int
	other       bool
}

func (v *paramReadScanner) EnterExpr(e ast.Expr) bool {
	if v.other {
		return false
	}
	switch e := e.(type) {
	case *ast.IdentExpr:
		if e.Name == v.name {
			v.uses++
			if !v.readThrough.Contains(e) {
				v.other = true
			}
		}
	case *ast.MemberExpr:
		v.noteObject(e.Object)
	case *ast.IndexExpr:
		v.noteObject(e.Object)
	case *ast.BinaryExpr:
		if e.Op == ast.Assign && v.rootedHere(e.Left) {
			v.other = true
		}
	case *ast.CallExpr:
		if v.rootedHere(e.Callee) {
			v.other = true
		}
	case *ast.BorrowExpr:
		if e.Mut && v.rootedHere(e.Arg) {
			v.other = true
		}
	case *ast.FuncExpr:
		v.other = v.other || mentionsName(v.name, e)
		return false
	}
	return !v.other
}

func (v *paramReadScanner) EnterObjExprElem(e ast.ObjExprElem) bool {
	if prop, ok := e.(*ast.PropertyExpr); ok && prop.Value == nil {
		if key, ok := prop.Name.(*ast.IdentExpr); ok && key.Name == v.name {
			v.other = true
		}
	}
	return !v.other
}

func (v *paramReadScanner) EnterPat(p ast.Pat) bool {
	if ip, ok := p.(*ast.IdentPat); ok && ip.Name == v.name {
		v.other = true
	}
	return !v.other
}

func (v *paramReadScanner) EnterDecl(d ast.Decl) bool {
	switch d.(type) {
	case *ast.FuncDecl, *ast.ClassDecl:
		v.other = v.other || mentionsName(v.name, d)
		return false
	}
	return !v.other
}

// noteObject records obj as a read through the parameter when obj names it.
func (v *paramReadScanner) noteObject(obj ast.Expr) {
	if id, ok := obj.(*ast.IdentExpr); ok && id.Name == v.name {
		v.readThrough.Add(id)
	}
}

// rootedHere reports whether e is the parameter, or a member or element chain whose root
// object is the parameter, as `p`, `p.x`, and `p.x[0].y` are.
func (v *paramReadScanner) rootedHere(e ast.Expr) bool {
	for {
		switch n := e.(type) {
		case *ast.MemberExpr:
			e = n.Object
		case *ast.IndexExpr:
			e = n.Object
		case *ast.IdentExpr:
			return n.Name == v.name
		default:
			return false
		}
	}
}

// mentionsName reports whether node reads a variable named name, either as an identifier or
// as a shorthand object property.
func mentionsName(name string, node interface{ Accept(ast.Visitor) }) bool {
	v := &nameMentionScanner{name: name}
	node.Accept(v)
	return v.found
}

type nameMentionScanner struct {
	ast.DefaultVisitor
	name  string
	found bool
}

func (v *nameMentionScanner) EnterExpr(e ast.Expr) bool {
	if id, ok := e.(*ast.IdentExpr); ok && id.Name == v.name {
		v.found = true
	}
	return !v.found
}

func (v *nameMentionScanner) EnterObjExprElem(e ast.ObjExprElem) bool {
	if prop, ok := e.(*ast.PropertyExpr); ok && prop.Value == nil {
		if key, ok := prop.Name.(*ast.IdentExpr); ok && key.Name == v.name {
			v.found = true
		}
	}
	return !v.found
}

// readOnlyBorrowParam reports whether the unannotated parameter p binds a single name that body
// only reads through, as paramOnlyReadThrough decides. A rest, optional, or defaulted parameter,
// and a destructuring one, report false.
func (c *checker) readOnlyBorrowParam(p *ast.Param, pat ast.Pat, rest, optional bool, body *ast.Block) bool {
	ip, ok := pat.(*ast.IdentPat)
	if !ok || p.TypeAnn != nil || ip.Default != nil || rest || optional {
		return false
	}
	return paramOnlyReadThrough(ip.Name, body)
}
