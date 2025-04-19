package ast

import "github.com/escalier-lang/escalier/internal/type_system"

type Node interface {
	Span() Span
}

type Inferrable interface {
	InferredType() type_system.Type
	SetInferredType(type_system.Type)
}

// If `Name` is an empty string it means that the identifier is missing in
// the expression.
type Ident struct {
	Name string
	span Span
}

func NewIdentifier(name string, span Span) *Ident {
	return &Ident{Name: name, span: span}
}

type QualIdent interface{ isQualIdent() }

func (*Ident) isQualIdent()  {}
func (*Member) isQualIdent() {}

type Member struct {
	Left  *QualIdent
	Right *Ident
}

func (i *Ident) Span() Span {
	return i.span
}

// TODO add support for imports and exports
type Module struct {
	Stmts []Stmt
}
