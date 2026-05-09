package ast

import "github.com/escalier-lang/escalier/internal/provenance"

type ClassDecl struct {
	Name           *Ident
	LifetimeParams []*LifetimeAnn    // generic lifetime parameters (e.g. <'a>)
	TypeParams     []*TypeParam      // generic type parameters
	Extends        *TypeRefTypeAnn   // optional superclass (can be a simple identifier or a generic type reference)
	Implements     []*TypeRefTypeAnn // interfaces this class implements (may be nil/empty)
	Body           []ClassElem       // fields, methods, etc.
	export         bool
	declare        bool
	span           Span
	provenance     provenance.Provenance
}

type ClassElem interface {
	IsClassElem()
	Accept(v Visitor)
	Span() Span
}

// MethodReceiver describes a `self` receiver on a method, getter, setter, or
// constructor. A nil *MethodReceiver means no receiver was written — this
// covers static members, getters/setters with an empty parameter list, and
// also non-static instance methods that omit `self` (the checker reports
// MissingSelfReceiverError for the latter).
//
//	self           → &MethodReceiver{Mut: false}
//	mut self       → &MethodReceiver{Mut: true}
//	'a self        → &MethodReceiver{Mut: false, Lifetime: 'a}
//	mut 'a self    → &MethodReceiver{Mut: true,  Lifetime: 'a}
type MethodReceiver struct {
	Mut      bool
	Lifetime LifetimeAnnNode // optional
	Span_    Span
}

func (r *MethodReceiver) Span() Span { return r.Span_ }

// Exported constructor for use in parser
func NewClassDecl(name *Ident, lifetimeParams []*LifetimeAnn, typeParams []*TypeParam, extends *TypeRefTypeAnn, implements []*TypeRefTypeAnn, body []ClassElem, export, declare bool, span Span) *ClassDecl {
	return &ClassDecl{
		Name:           name,
		LifetimeParams: lifetimeParams,
		TypeParams:     typeParams,
		Extends:        extends,
		Implements:     implements,
		Body:           body,
		export:         export,
		declare:        declare,
		span:           span,
		provenance:     nil,
	}
}

func (*ClassDecl) isDecl()            {}
func (d *ClassDecl) Export() bool     { return d.export }
func (d *ClassDecl) SetExport(e bool) { d.export = e }
func (d *ClassDecl) Declare() bool    { return d.declare }
func (d *ClassDecl) Span() Span       { return d.span }
func (d *ClassDecl) Accept(v Visitor) {
	if v.EnterDecl(d) {
		if d.Extends != nil {
			d.Extends.Accept(v)
		}
		for _, impl := range d.Implements {
			impl.Accept(v)
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
	Name ObjKey
	Type TypeAnn // required for class fields; optional for object-pattern shorthands
	// Value is the field's initializer expression (`= expr`). Only valid
	// on static fields — instance fields are initialized in the
	// constructor body. The checker rejects `Value != nil` on instance
	// fields.
	Value    Expr
	Static   bool // true if this is a static field
	Private  bool // true if this field is private
	Readonly bool // true if this field is readonly
	Optional bool // true if this field is declared `name?: T`
	Span_    Span
}

func (*FieldElem) IsClassElem() {}
func (f *FieldElem) Accept(v Visitor) {
	if v.EnterClassElem(f) {
		f.Name.Accept(v)
		if f.Type != nil {
			f.Type.Accept(v)
		}
		if f.Value != nil {
			f.Value.Accept(v)
		}
	}
	v.ExitClassElem(f)
}
func (f *FieldElem) Span() Span { return f.Span_ }

type MethodElem struct {
	Name     ObjKey
	Fn       *FuncExpr
	Receiver *MethodReceiver // nil if static / no receiver
	Static   bool            // true if this is a static method
	Private  bool            // true if this is a private method
	Span_    Span
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
	Name     ObjKey
	Fn       *FuncExpr
	Receiver *MethodReceiver // nil if static / no receiver
	Static   bool            // true if this is a static getter
	Private  bool            // true if this is a private getter
	Span_    Span
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

// ConstructorElem represents an explicit `constructor(...) { ... }` block
// inside a class body. The constructor's receiver is represented by
// `Receiver *MethodReceiver` (nil when absent — a non-nil `Lifetime` is
// rejected by validation). The first entry in `Fn.Params` corresponds to
// the user-written receiver when `Receiver` is non-nil; the receiver's
// mutability is recorded on `Receiver`, not on the param. Remaining params
// are the constructor's callable params. The constructor's return type is
// always `Self` and is not part of the AST; `Fn.Return` must remain nil.
// `Fn.Throws` may be non-nil — constructors may declare a `throws` clause.
type ConstructorElem struct {
	Fn       *FuncExpr
	Receiver *MethodReceiver // nil if absent. Carried for diagnostics — a non-nil Lifetime is rejected by validation.
	Private  bool            // reserved for future "Private Constructors" work
	Span_    Span
}

func (*ConstructorElem) IsClassElem() {}
func (c *ConstructorElem) Accept(v Visitor) {
	if v.EnterClassElem(c) {
		if c.Fn != nil {
			c.Fn.Accept(v)
		}
	}
	v.ExitClassElem(c)
}
func (c *ConstructorElem) Span() Span { return c.Span_ }

// SetterElem represents a setter in a class.
type SetterElem struct {
	Name     ObjKey
	Fn       *FuncExpr
	Receiver *MethodReceiver // nil if static / no receiver
	Static   bool            // true if this is a static setter
	Private  bool            // true if this is a private setter
	Span_    Span
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
