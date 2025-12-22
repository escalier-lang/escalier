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

type QualIdent interface {
	isQualIdent()
	Span() Span
}

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

func (m *Member) Span() Span {
	return Span{
		Start: m.Left.Span().Start,
		End:   m.Right.Span().End,
	}
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

func NewModule(namespaces btree.Map[string, *Namespace]) *Module {
	return &Module{
		Namespaces: namespaces,
	}
}

func (m *Module) Accept(v Visitor) {
	m.Namespaces.Scan(func(key string, ns *Namespace) bool {
		for _, decl := range ns.Decls {
			decl.Accept(v)
		}
		return true
	})
}

type Script struct {
	Stmts []Stmt
	span  Span
}

func NewScript(stmts []Stmt, span Span) *Script {
	return &Script{
		Stmts: stmts,
		span:  span,
	}
}

func (s *Script) Span() Span {
	return s.span
}

func (s *Script) Accept(v Visitor) {
	for _, stmt := range s.Stmts {
		stmt.Accept(v)
	}
}
