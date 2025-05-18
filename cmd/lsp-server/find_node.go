package main

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/moznion/go-optional"
)

type Visitor struct {
	Cursor ast.Location
	Node   optional.Option[ast.Node]
}

func (v *Visitor) VisitLit(l ast.Lit) bool {
	if l.Span().Contains(v.Cursor) {
		var node ast.Node = l
		v.Node = optional.Some(node)
		return true
	}
	return false
}
func (v *Visitor) VisitPat(p ast.Pat) bool {
	if p.Span().Contains(v.Cursor) {
		var node ast.Node = p
		v.Node = optional.Some(node)
		return true
	}
	return false
}
func (v *Visitor) VisitExpr(e ast.Expr) bool {
	if e.Span().Contains(v.Cursor) {
		var node ast.Node = e
		v.Node = optional.Some(node)
		return true
	}
	return false
}
func (v *Visitor) VisitObjExprElem(e ast.ObjExprElem) bool {
	if e.Span().Contains(v.Cursor) {
		var node ast.Node = e
		v.Node = optional.Some(node)
		return true
	}
	return false
}
func (v *Visitor) VisitStmt(s ast.Stmt) bool {
	if s.Span().Contains(v.Cursor) {
		var node ast.Node = s
		v.Node = optional.Some(node)
		return true
	}
	return false
}
func (v *Visitor) VisitDecl(d ast.Decl) bool {
	if d.Span().Contains(v.Cursor) {
		var node ast.Node = d
		v.Node = optional.Some(node)
		return true
	}
	return false
}
func (v *Visitor) VisitTypeAnn(t ast.TypeAnn) bool {
	if t.Span().Contains(v.Cursor) {
		var node ast.Node = t
		v.Node = optional.Some(node)
		return true
	}
	return false
}

func findNodeInScript(script *ast.Script, loc ast.Location) optional.Option[ast.Node] {
	visitor := &Visitor{
		Cursor: loc,
		Node:   nil,
	}
	for _, stmt := range script.Stmts {
		stmt.Accept(visitor)
	}
	return visitor.Node
}
