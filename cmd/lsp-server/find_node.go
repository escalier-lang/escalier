package main

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

type Visitor struct {
	Cursor ast.Location
	Node   ast.Node
}

func (v *Visitor) VisitLit(l ast.Lit) bool {
	if l.Span().Contains(v.Cursor) {
		v.Node = l
		return true
	}
	return false
}
func (v *Visitor) VisitPat(p ast.Pat) bool {
	if p.Span().Contains(v.Cursor) {
		v.Node = p
		return true
	}
	return false
}
func (v *Visitor) VisitExpr(e ast.Expr) bool {
	if e.Span().Contains(v.Cursor) {
		v.Node = e
		return true
	}
	return false
}
func (v *Visitor) VisitObjExprElem(e ast.ObjExprElem) bool {
	if e.Span().Contains(v.Cursor) {
		v.Node = e
		return true
	}
	return false
}
func (v *Visitor) VisitStmt(s ast.Stmt) bool {
	if s.Span().Contains(v.Cursor) {
		v.Node = s
		return true
	}
	return false
}
func (v *Visitor) VisitDecl(d ast.Decl) bool {
	if d.Span().Contains(v.Cursor) {
		v.Node = d
		return true
	}
	return false
}
func (v *Visitor) VisitTypeAnn(t ast.TypeAnn) bool {
	if t.Span().Contains(v.Cursor) {
		v.Node = t
		return true
	}
	return false
}

func findNodeInScript(script *ast.Script, loc ast.Location) ast.Node {
	visitor := &Visitor{
		Cursor: loc,
		Node:   nil,
	}
	for _, stmt := range script.Stmts {
		stmt.Accept(visitor)
	}
	return visitor.Node
}
