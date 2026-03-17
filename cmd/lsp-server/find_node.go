package main

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

type Visitor struct {
	ast.DefaultVisitor
	Cursor ast.Location
	Node   ast.Node
}

func (v *Visitor) EnterLit(l ast.Lit) bool {
	if l.Span().Contains(v.Cursor) {
		v.Node = l
		return true
	}
	return false
}
func (v *Visitor) EnterPat(p ast.Pat) bool {
	if p.Span().Contains(v.Cursor) {
		v.Node = p
		return true
	}
	return false
}
func (v *Visitor) EnterExpr(e ast.Expr) bool {
	if e.Span().Contains(v.Cursor) {
		v.Node = e
		return true
	}
	return false
}
func (v *Visitor) EnterObjExprElem(e ast.ObjExprElem) bool {
	if e.Span().Contains(v.Cursor) {
		v.Node = e
		return true
	}
	return false
}
func (v *Visitor) EnterStmt(s ast.Stmt) bool {
	if s.Span().Contains(v.Cursor) {
		v.Node = s
		return true
	}
	return false
}
func (v *Visitor) EnterDecl(d ast.Decl) bool {
	if d.Span().Contains(v.Cursor) {
		v.Node = d
		return true
	}
	return false
}
func (v *Visitor) EnterTypeAnn(t ast.TypeAnn) bool {
	if t.Span().Contains(v.Cursor) {
		v.Node = t
		return true
	}
	return false
}

func findNodeInScript(script *ast.Script, loc ast.Location) ast.Node {
	visitor := &Visitor{
		DefaultVisitor: ast.DefaultVisitor{},
		Cursor:         loc,
		Node:           nil,
	}
	for _, stmt := range script.Stmts {
		stmt.Accept(visitor)
	}
	return visitor.Node
}

// ParentVisitor extends the base visitor with parent tracking.
type ParentVisitor struct {
	ast.DefaultVisitor
	Cursor  ast.Location
	Node    ast.Node
	Parent  ast.Node
	parents []ast.Node // stack of ancestors
}

func (v *ParentVisitor) push(n ast.Node) {
	v.parents = append(v.parents, n)
}

func (v *ParentVisitor) pop() {
	if len(v.parents) > 0 {
		v.parents = v.parents[:len(v.parents)-1]
	}
}

func (v *ParentVisitor) currentParent() ast.Node {
	if len(v.parents) == 0 {
		return nil
	}
	return v.parents[len(v.parents)-1]
}

func (v *ParentVisitor) EnterExpr(e ast.Expr) bool {
	if e.Span().Contains(v.Cursor) {
		v.Parent = v.currentParent()
		v.Node = e
		v.push(e)
		return true
	}
	return false
}
func (v *ParentVisitor) ExitExpr(e ast.Expr) {
	v.pop()
}

func (v *ParentVisitor) EnterLit(l ast.Lit) bool {
	if l.Span().Contains(v.Cursor) {
		v.Parent = v.currentParent()
		v.Node = l
		v.push(l)
		return true
	}
	return false
}
func (v *ParentVisitor) ExitLit(l ast.Lit) {
	v.pop()
}

func (v *ParentVisitor) EnterPat(p ast.Pat) bool {
	if p.Span().Contains(v.Cursor) {
		v.Parent = v.currentParent()
		v.Node = p
		v.push(p)
		return true
	}
	return false
}
func (v *ParentVisitor) ExitPat(p ast.Pat) {
	v.pop()
}

func (v *ParentVisitor) EnterObjExprElem(e ast.ObjExprElem) bool {
	if e.Span().Contains(v.Cursor) {
		v.Parent = v.currentParent()
		v.Node = e
		v.push(e)
		return true
	}
	return false
}
func (v *ParentVisitor) ExitObjExprElem(e ast.ObjExprElem) {
	v.pop()
}

func (v *ParentVisitor) EnterStmt(s ast.Stmt) bool {
	if s.Span().Contains(v.Cursor) {
		v.Parent = v.currentParent()
		v.Node = s
		v.push(s)
		return true
	}
	return false
}
func (v *ParentVisitor) ExitStmt(s ast.Stmt) {
	v.pop()
}

func (v *ParentVisitor) EnterDecl(d ast.Decl) bool {
	if d.Span().Contains(v.Cursor) {
		v.Parent = v.currentParent()
		v.Node = d
		v.push(d)
		return true
	}
	return false
}
func (v *ParentVisitor) ExitDecl(d ast.Decl) {
	v.pop()
}

func (v *ParentVisitor) EnterTypeAnn(t ast.TypeAnn) bool {
	if t.Span().Contains(v.Cursor) {
		v.Parent = v.currentParent()
		v.Node = t
		v.push(t)
		return true
	}
	return false
}
func (v *ParentVisitor) ExitTypeAnn(t ast.TypeAnn) {
	v.pop()
}

func findNodeAndParent(script *ast.Script, loc ast.Location) (ast.Node, ast.Node) {
	visitor := &ParentVisitor{
		DefaultVisitor: ast.DefaultVisitor{},
		Cursor:         loc,
	}
	for _, stmt := range script.Stmts {
		stmt.Accept(visitor)
	}
	return visitor.Node, visitor.Parent
}
