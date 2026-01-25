package ast

import "github.com/escalier-lang/escalier/internal/provenance"

type ClassDecl struct {
	Name       *Ident
	TypeParams []*TypeParam    // generic type parameters
	Extends    *TypeRefTypeAnn // optional superclass (can be a simple identifier or a generic type reference)
	Params     []*Param        // constructor params
	Body       []ClassElem     // fields, methods, etc.
	export     bool
	declare    bool
	span       Span
	provenance provenance.Provenance
}

type ClassElem interface {
	IsClassElem()
	Accept(v Visitor)
	Span() Span
}

// Exported constructor for use in parser
func NewClassDecl(name *Ident, typeParams []*TypeParam, extends *TypeRefTypeAnn, params []*Param, body []ClassElem, export, declare bool, span Span) *ClassDecl {
	return &ClassDecl{
		Name:       name,
		TypeParams: typeParams,
		Extends:    extends,
		Params:     params,
		Body:       body,
		export:     export,
		declare:    declare,
		span:       span,
		provenance: nil,
	}
}

func (*ClassDecl) isDecl()         {}
func (d *ClassDecl) Export() bool  { return d.export }
func (d *ClassDecl) Declare() bool { return d.declare }
func (d *ClassDecl) Span() Span    { return d.span }
func (d *ClassDecl) Accept(v Visitor) {
	if v.EnterDecl(d) {
		if d.Extends != nil {
			d.Extends.Accept(v)
		}
		for _, elem := range d.Body {
			elem.Accept(v)
		}
	}
	v.ExitDecl(d)
}
func (d *ClassDecl) Provenance() provenance.Provenance {
	return d.provenance
}
func (d *ClassDecl) SetProvenance(p provenance.Provenance) {
	d.provenance = p
}

type FieldElem struct {
	Name     ObjKey
	Value    Expr    // optional
	Type     TypeAnn // optional
	Default  Expr    // optional
	Static   bool    // true if this is a static field
	Private  bool    // true if this field is private
	Readonly bool    // true if this field is readonly
	Span_    Span
}

func (*FieldElem) IsClassElem() {}
func (f *FieldElem) Accept(v Visitor) {
	if v.EnterClassElem(f) {
		f.Name.Accept(v)
		if f.Type != nil {
			f.Type.Accept(v)
		}
		if f.Default != nil {
			f.Default.Accept(v)
		}
		// FieldElem has no children to visit
	}
	v.ExitClassElem(f)
}
func (f *FieldElem) Span() Span { return f.Span_ }

type MethodElem struct {
	Name    ObjKey
	Fn      *FuncExpr
	MutSelf *bool // true if 'self' is mutable
	Static  bool  // true if this is a static method
	Private bool  // true if this is a private method
	Span_   Span
}

func (*MethodElem) IsClassElem() {}
func (m *MethodElem) Accept(v Visitor) {
	if v.EnterClassElem(m) {
		m.Name.Accept(v)
		if m.Fn != nil {
			m.Fn.Accept(v)
		}
	}
	v.ExitClassElem(m)
}
func (m *MethodElem) Span() Span { return m.Span_ }

// GetterElem represents a getter in a class.
type GetterElem struct {
	Name    ObjKey
	Fn      *FuncExpr
	Static  bool // true if this is a static getter
	Private bool // true if this is a private getter
	Span_   Span
}

func (*GetterElem) IsClassElem() {}
func (g *GetterElem) Accept(v Visitor) {
	if v.EnterClassElem(g) {
		g.Name.Accept(v)
		if g.Fn != nil {
			g.Fn.Accept(v)
		}
	}
	v.ExitClassElem(g)
}
func (g *GetterElem) Span() Span { return g.Span_ }

// SetterElem represents a setter in a class.
type SetterElem struct {
	Name    ObjKey
	Fn      *FuncExpr
	Static  bool // true if this is a static setter
	Private bool // true if this is a private setter
	Span_   Span
}

func (*SetterElem) IsClassElem() {}
func (s *SetterElem) Accept(v Visitor) {
	if v.EnterClassElem(s) {
		s.Name.Accept(v)
		if s.Fn != nil {
			s.Fn.Accept(v)
		}
	}
	v.ExitClassElem(s)
}
func (s *SetterElem) Span() Span { return s.Span_ }
