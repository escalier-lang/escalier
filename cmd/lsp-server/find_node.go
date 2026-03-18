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
	Cursor    ast.Location
	Node      ast.Node
	Parent    ast.Node
	Ancestors []ast.Node // snapshot of the ancestor chain for the deepest node found
	parents   []ast.Node // stack of ancestors (working state)
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

func (v *ParentVisitor) enter(n ast.Node) bool {
	if n.Span().Contains(v.Cursor) {
		v.Parent = v.currentParent()
		v.Node = n
		// Snapshot the current ancestor chain (excluding n itself)
		v.Ancestors = make([]ast.Node, len(v.parents))
		copy(v.Ancestors, v.parents)
		v.push(n)
		return true
	}
	return false
}

// exit only pops if n was actually pushed (i.e. Enter returned true).
// Accept methods call Exit unconditionally, so we must guard against
// popping nodes that were never pushed.
func (v *ParentVisitor) exit(n ast.Node) {
	if len(v.parents) > 0 && v.parents[len(v.parents)-1] == n {
		v.pop()
	}
}

func (v *ParentVisitor) EnterExpr(e ast.Expr) bool              { return v.enter(e) }
func (v *ParentVisitor) ExitExpr(e ast.Expr)                     { v.exit(e) }
func (v *ParentVisitor) EnterLit(l ast.Lit) bool                 { return v.enter(l) }
func (v *ParentVisitor) ExitLit(l ast.Lit)                       { v.exit(l) }
func (v *ParentVisitor) EnterPat(p ast.Pat) bool                 { return v.enter(p) }
func (v *ParentVisitor) ExitPat(p ast.Pat)                       { v.exit(p) }
func (v *ParentVisitor) EnterObjExprElem(e ast.ObjExprElem) bool { return v.enter(e) }
func (v *ParentVisitor) ExitObjExprElem(e ast.ObjExprElem)       { v.exit(e) }
func (v *ParentVisitor) EnterStmt(s ast.Stmt) bool               { return v.enter(s) }
func (v *ParentVisitor) ExitStmt(s ast.Stmt)                     { v.exit(s) }
func (v *ParentVisitor) EnterDecl(d ast.Decl) bool               { return v.enter(d) }
func (v *ParentVisitor) ExitDecl(d ast.Decl)                     { v.exit(d) }
func (v *ParentVisitor) EnterTypeAnn(t ast.TypeAnn) bool         { return v.enter(t) }
func (v *ParentVisitor) ExitTypeAnn(t ast.TypeAnn)               { v.exit(t) }

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

// findNodeWithAncestors returns the deepest node at loc and its full ancestor chain
// (outermost first, excluding the node itself).
func findNodeWithAncestors(script *ast.Script, loc ast.Location) (ast.Node, []ast.Node) {
	visitor := &ParentVisitor{
		DefaultVisitor: ast.DefaultVisitor{},
		Cursor:         loc,
	}
	for _, stmt := range script.Stmts {
		stmt.Accept(visitor)
	}
	return visitor.Node, visitor.Ancestors
}

// findNodeAndParentInFile finds the deepest node at loc within a specific file
// of a module. It walks only declarations belonging to the given sourceID,
// plus that file's import statements.
func findNodeAndParentInFile(module *ast.Module, sourceID int, loc ast.Location) (ast.Node, ast.Node) {
	visitor := &ParentVisitor{
		DefaultVisitor: ast.DefaultVisitor{},
		Cursor:         loc,
	}
	// Walk file-scoped imports
	for _, file := range module.Files {
		if file.SourceID == sourceID {
			for _, imp := range file.Imports {
				imp.Accept(visitor)
			}
			break
		}
	}
	// Walk declarations from this file across all namespaces
	module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			if decl.Span().SourceID == sourceID {
				decl.Accept(visitor)
			}
		}
		return true
	})
	return visitor.Node, visitor.Parent
}

// findNodeWithAncestorsInFile returns the deepest node at loc and its full
// ancestor chain for a specific file within a module.
func findNodeWithAncestorsInFile(module *ast.Module, sourceID int, loc ast.Location) (ast.Node, []ast.Node) {
	visitor := &ParentVisitor{
		DefaultVisitor: ast.DefaultVisitor{},
		Cursor:         loc,
	}
	// Walk file-scoped imports
	for _, file := range module.Files {
		if file.SourceID == sourceID {
			for _, imp := range file.Imports {
				imp.Accept(visitor)
			}
			break
		}
	}
	// Walk declarations from this file across all namespaces
	module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			if decl.Span().SourceID == sourceID {
				decl.Accept(visitor)
			}
		}
		return true
	})
	return visitor.Node, visitor.Ancestors
}
