package ast

type Visitor interface {
	EnterLit(l Lit) bool
	EnterPat(p Pat) bool
	EnterExpr(e Expr) bool
	EnterObjExprElem(e ObjExprElem) bool
	EnterStmt(s Stmt) bool
	EnterDecl(d Decl) bool
	EnterTypeAnn(t TypeAnn) bool

	ExitLit(l Lit)
	ExitPat(p Pat)
	ExitExpr(e Expr)
	ExitObjExprElem(e ObjExprElem)
	ExitStmt(s Stmt)
	ExitDecl(d Decl)
	ExitTypeAnn(t TypeAnn)
}

type DefaulVisitor struct{}

func (v *DefaulVisitor) EnterLit(l Lit) bool {
	return true
}

func (v *DefaulVisitor) EnterPat(p Pat) bool {
	return true
}

func (v *DefaulVisitor) EnterExpr(e Expr) bool {
	return true
}

func (v *DefaulVisitor) EnterObjExprElem(e ObjExprElem) bool {
	return true
}

func (v *DefaulVisitor) EnterStmt(s Stmt) bool {
	return true
}

func (v *DefaulVisitor) EnterDecl(d Decl) bool {
	return true
}

func (v *DefaulVisitor) EnterTypeAnn(t TypeAnn) bool {
	return true
}

func (v *DefaulVisitor) ExitLit(l Lit)                 {}
func (v *DefaulVisitor) ExitPat(p Pat)                 {}
func (v *DefaulVisitor) ExitExpr(e Expr)               {}
func (v *DefaulVisitor) ExitObjExprElem(e ObjExprElem) {}
func (v *DefaulVisitor) ExitStmt(s Stmt)               {}
func (v *DefaulVisitor) ExitDecl(d Decl)               {}
func (v *DefaulVisitor) ExitTypeAnn(t TypeAnn)         {}
