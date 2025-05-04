package ast

func (*LitProvenance) IsProvenance()     {}
func (*ExprProvenance) IsProvenance()    {}
func (*PatProvenance) IsProvenance()     {}
func (*TypeAnnProvenance) IsProvenance() {}

type LitProvenance struct {
	Lit Lit
}
type ExprProvenance struct {
	Expr Expr
}
type PatProvenance struct {
	Pat Pat
}
type TypeAnnProvenance struct {
	TypeAnn TypeAnn
}

func NewTypeAnnProvenance(typeAnn TypeAnn) *TypeAnnProvenance {
	return &TypeAnnProvenance{
		TypeAnn: typeAnn,
	}
}
