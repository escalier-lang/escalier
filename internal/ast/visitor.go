package ast

type Visitor interface {
	VisitLit(l Lit) bool
	VisitPat(p Pat) bool
	VisitExpr(e Expr) bool
	VisitObjExprElem(e ObjExprElem) bool
	VisitStmt(s Stmt) bool
	VisitDecl(d Decl) bool
	VisitTypeAnn(t TypeAnn) bool
}

type DefaulVisitor struct{}

func (v *DefaulVisitor) VisitLit(l Lit) bool {
	return true
}

func (v *DefaulVisitor) VisitPat(p Pat) bool {
	return true
}

func (v *DefaulVisitor) VisitExpr(e Expr) bool {
	return true
}

func (v *DefaulVisitor) VisitObjExprElem(e ObjExprElem) bool {
	return true
}

func (v *DefaulVisitor) VisitStmt(s Stmt) bool {
	return true
}

func (v *DefaulVisitor) VisitDecl(d Decl) bool {
	return true
}

func (v *DefaulVisitor) VisitTypeAnn(t TypeAnn) bool {
	return true
}
