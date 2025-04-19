package ast

type ObjKey interface {
	isObjKey()
	Node
}

func (*IdentExpr) isObjKey()   {}
func (*StrLit) isObjKey()      {}
func (*NumLit) isObjKey()      {}
func (*ComputedKey) isObjKey() {}

type ComputedKey struct {
	Expr Expr
}

func NewComputedKey(expr Expr) *ComputedKey {
	return &ComputedKey{Expr: expr}
}
func (c *ComputedKey) Span() Span { return c.Expr.Span() }
