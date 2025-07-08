package ast

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
	if v.EnterStmt(s) {
		s.Expr.Accept(v)
	}
	v.ExitStmt(s)
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
	if v.EnterStmt(s) {
		s.Decl.Accept(v)
	}
	v.ExitStmt(s)
}

type ReturnStmt struct {
	Expr Expr // optional
	span Span
}

func NewReturnStmt(expr Expr, span Span) *ReturnStmt {
	return &ReturnStmt{Expr: expr, span: span}
}
func (*ReturnStmt) isStmt()      {}
func (s *ReturnStmt) Span() Span { return s.span }
func (s *ReturnStmt) Accept(v Visitor) {
	if v.EnterStmt(s) {
		if s.Expr != nil {
			s.Expr.Accept(v)
		}
	}
	v.ExitStmt(s)
}
