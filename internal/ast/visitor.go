package ast

type Visitor interface {
	EnterLit(l Lit) bool
	EnterPat(p Pat) bool
	EnterExpr(e Expr) bool
	EnterObjExprElem(e ObjExprElem) bool
	EnterStmt(s Stmt) bool
	EnterDecl(d Decl) bool
	EnterTypeAnn(t TypeAnn) bool
	EnterBlock(b Block) bool
	EnterClassElem(e ClassElem) bool

	ExitLit(l Lit)
	ExitPat(p Pat)
	ExitExpr(e Expr)
	ExitObjExprElem(e ObjExprElem)
	ExitStmt(s Stmt)
	ExitDecl(d Decl)
	ExitTypeAnn(t TypeAnn)
	ExitBlock(b Block)
	ExitClassElem(e ClassElem)
}

type DefaultVisitor struct{}

func (v *DefaultVisitor) EnterLit(l Lit) bool                 { return true }
func (v *DefaultVisitor) EnterPat(p Pat) bool                 { return true }
func (v *DefaultVisitor) EnterExpr(e Expr) bool               { return true }
func (v *DefaultVisitor) EnterObjExprElem(e ObjExprElem) bool { return true }
func (v *DefaultVisitor) EnterStmt(s Stmt) bool               { return true }
func (v *DefaultVisitor) EnterDecl(d Decl) bool               { return true }
func (v *DefaultVisitor) EnterTypeAnn(t TypeAnn) bool         { return true }
func (v *DefaultVisitor) EnterBlock(b Block) bool             { return true }
func (v *DefaultVisitor) EnterClassElem(e ClassElem) bool     { return true }

func (v *DefaultVisitor) ExitLit(l Lit)                 {}
func (v *DefaultVisitor) ExitPat(p Pat)                 {}
func (v *DefaultVisitor) ExitExpr(e Expr)               {}
func (v *DefaultVisitor) ExitObjExprElem(e ObjExprElem) {}
func (v *DefaultVisitor) ExitStmt(s Stmt)               {}
func (v *DefaultVisitor) ExitDecl(d Decl)               {}
func (v *DefaultVisitor) ExitTypeAnn(t TypeAnn)         {}
func (v *DefaultVisitor) ExitBlock(b Block)             {}
func (v *DefaultVisitor) ExitClassElem(e ClassElem)     {}
