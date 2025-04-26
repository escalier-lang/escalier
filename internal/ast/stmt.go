package ast

import "github.com/moznion/go-optional"

//sumtype:decl
type Stmt interface {
	isStmt()
	Node
}

type ExprStmt struct {
	Expr Expr
	span Span
}

func NewExprStmt(expr Expr, span Span) *ExprStmt {
	return &ExprStmt{Expr: expr, span: span}
}
func (*ExprStmt) isStmt()      {}
func (s *ExprStmt) Span() Span { return s.span }
func (s *ExprStmt) Accept(v Visitor) {
	if v.VisitStmt(s) {
		s.Expr.Accept(v)
	}
}

type DeclStmt struct {
	Decl Decl
	span Span
}

func NewDeclStmt(decl Decl, span Span) *DeclStmt {
	return &DeclStmt{Decl: decl, span: span}
}
func (*DeclStmt) isStmt()      {}
func (s *DeclStmt) Span() Span { return s.span }
func (s *DeclStmt) Accept(v Visitor) {
	if v.VisitStmt(s) {
		s.Decl.Accept(v)
	}
}

type ReturnStmt struct {
	Expr optional.Option[Expr]
	span Span
}

func NewReturnStmt(expr optional.Option[Expr], span Span) *ReturnStmt {
	return &ReturnStmt{Expr: expr, span: span}
}
func (*ReturnStmt) isStmt()      {}
func (s *ReturnStmt) Span() Span { return s.span }
func (s *ReturnStmt) Accept(v Visitor) {
	if v.VisitStmt(s) {
		s.Expr.IfSome(func(expr Expr) {
			expr.Accept(v)
		})
	}
}
