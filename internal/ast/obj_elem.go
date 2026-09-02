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
	commentSlots
}

func NewComputedKey(expr Expr) *ComputedKey {
	return &ComputedKey{Expr: expr, commentSlots: commentSlots{}}
}
func (c *ComputedKey) Span() Span { return c.Expr.Span() }
func (c *ComputedKey) Accept(v Visitor) {
	c.Expr.Accept(v)
}
