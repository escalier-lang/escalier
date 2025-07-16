package ast

import (
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/tidwall/btree"
)

type Node interface {
	Span() Span
	Accept(v Visitor)
}

type Type type_system.Type

type Inferrable interface {
	InferredType() Type
	SetInferredType(Type)
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
func (i *Ident) Accept(v Visitor) {
	// TODO
}

type QualIdent interface{ isQualIdent() }

func (*Ident) isQualIdent()  {}
func (*Member) isQualIdent() {}

// QualIdentToString converts a QualIdent to its string representation
func QualIdentToString(qi QualIdent) string {
	switch q := qi.(type) {
	case *Ident:
		return q.Name
	case *Member:
		left := QualIdentToString(q.Left)
		return left + "." + q.Right.Name
	default:
		return ""
	}
}

type Member struct {
	Left  QualIdent
	Right *Ident
}

func (i *Ident) Span() Span {
	return i.span
}

// TODO add support for imports and exports
type Namespace struct {
	Decls []Decl
}
type Module struct {
	Namespaces btree.Map[string, *Namespace]
}

type Script struct {
	Stmts []Stmt
}
