package ast

type BindingSource interface {
	isBindingSource()
	Node
}

func (*Ident) isBindingSource()    {} // Used for FuncDecls and TypeDecls
func (*IdentPat) isBindingSource() {}
