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
