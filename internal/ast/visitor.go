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
	EnterObjTypeAnnElem(e ObjTypeAnnElem) bool
	EnterLifetimeAnn(l LifetimeAnnNode) bool

	ExitLit(l Lit)
	ExitPat(p Pat)
	ExitExpr(e Expr)
	ExitObjExprElem(e ObjExprElem)
	ExitStmt(s Stmt)
	ExitDecl(d Decl)
	ExitTypeAnn(t TypeAnn)
	ExitBlock(b Block)
	ExitClassElem(e ClassElem)
	ExitObjTypeAnnElem(e ObjTypeAnnElem)
	ExitLifetimeAnn(l LifetimeAnnNode)
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

// EnterObjTypeAnnElem is offered a member of an object type annotation. A
// RestSpreadTypeAnn member arrives through EnterTypeAnn instead, since it is a
// TypeAnn as well as an ObjTypeAnnElem.
func (v *DefaultVisitor) EnterObjTypeAnnElem(e ObjTypeAnnElem) bool { return true }

func (v *DefaultVisitor) ExitLit(l Lit)                       {}
func (v *DefaultVisitor) ExitPat(p Pat)                       {}
func (v *DefaultVisitor) ExitExpr(e Expr)                     {}
func (v *DefaultVisitor) ExitObjExprElem(e ObjExprElem)       {}
func (v *DefaultVisitor) ExitStmt(s Stmt)                     {}
func (v *DefaultVisitor) ExitDecl(d Decl)                     {}
func (v *DefaultVisitor) ExitTypeAnn(t TypeAnn)               {}
func (v *DefaultVisitor) ExitBlock(b Block)                   {}
func (v *DefaultVisitor) ExitClassElem(e ClassElem)           {}
func (v *DefaultVisitor) ExitObjTypeAnnElem(e ObjTypeAnnElem) {}

// EnterLifetimeAnn is offered a lifetime the source writes as a node: the `'a`
// bound in `<'b: 'a>`, the `'a` in a borrow such as `mut 'a Point`, a lifetime
// argument such as the `'a` in `Ref<'a, T>`, and a receiver's, as in
// `mut 'a self`. A binder's own name is a string on the parameter rather than a
// node, so `<'a>` alone reaches nothing.
func (v *DefaultVisitor) EnterLifetimeAnn(l LifetimeAnnNode) bool { return true }
func (v *DefaultVisitor) ExitLifetimeAnn(l LifetimeAnnNode)       {}

// acceptTypeParams visits the constraint and default of each binder in a `<…>`
// quantifier list. A binder's own name is a string rather than a node, so those
// two type annotations are the whole of what a type parameter contributes.
//
// Every node holding a type-parameter list calls this, so the list is walked
// one way rather than seven.
func acceptTypeParams(v Visitor, params []*TypeParam) {
	for _, tp := range params {
		if tp.Constraint != nil {
			tp.Constraint.Accept(v)
		}
		if tp.Default != nil {
			tp.Default.Accept(v)
		}
	}
}

// acceptLifetimeParams visits the bounds of each lifetime binder in a `<…>`
// quantifier list. In `<'a, 'b: 'a>` the walk reaches the `'a` written as 'b's
// bound. A binder's own name is a string, so the bounds are the whole of what a
// lifetime parameter contributes.
func acceptLifetimeParams(v Visitor, params []*LifetimeParam) {
	for _, lp := range params {
		for _, bound := range lp.Bounds {
			bound.Accept(v)
		}
	}
}
