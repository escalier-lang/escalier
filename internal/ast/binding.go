package ast

type BindingSource interface {
	isBindingSource()
}

func (*Ident) isBindingSource()    {}
func (*IdentPat) isBindingSource() {}
