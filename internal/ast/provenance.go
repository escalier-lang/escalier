package ast

func (*ExprProvenance) IsProvenance()    {}
func (*PatProvenance) IsProvenance()     {}
func (*TypeAnnProvenance) IsProvenance() {}

type ExprProvenance struct {
	Expr Expr
}
type PatProvenance struct {
	Pat Pat
}
type TypeAnnProvenance struct {
	TypeAnn TypeAnn
}
