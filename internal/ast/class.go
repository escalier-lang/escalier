package ast

type ClassDecl struct {
	Name       *Ident
	TypeParams []*TypeParam // generic type parameters
	Params     []*Param     // constructor params
	Body       []ClassElem  // fields, methods, etc.
	export     bool
	declare    bool
	span       Span
}

type ClassElem interface {
	IsClassElem()
	Accept(v Visitor)
	Span() Span
}

// Exported constructor for use in parser
func NewClassDecl(name *Ident, typeParams []*TypeParam, params []*Param, body []ClassElem, export, declare bool, span Span) *ClassDecl {
	return &ClassDecl{
		Name:       name,
		TypeParams: typeParams,
		Params:     params,
		Body:       body,
		export:     export,
		declare:    declare,
		span:       span,
	}
}

func (*ClassDecl) isDecl()         {}
func (d *ClassDecl) Export() bool  { return d.export }
func (d *ClassDecl) Declare() bool { return d.declare }
func (d *ClassDecl) Span() Span    { return d.span }
func (d *ClassDecl) Accept(v Visitor) {
	if v.EnterDecl(d) {
		for _, elem := range d.Body {
			elem.Accept(v)
		}
	}
	v.ExitDecl(d)
}

type FieldElem struct {
	Name    *Ident
	Value   Expr    // optional
	Type    TypeAnn // optional
	Default Expr    // optional
	Private bool    // true if this field is private
	Span_   Span
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
	Name       *Ident
	TypeParams []*TypeParam // generic type parameters for the method
	Params     []*Param
	ReturnType TypeAnn // optional
	Body       *Block  // optional
	Static     bool    // true if this is a static method
	Async      bool    // true if this is an async method
	Private    bool    // true if this is a private method
	Span_      Span
}

func (*MethodElem) IsClassElem() {}
func (m *MethodElem) Accept(v Visitor) {
	if v.EnterClassElem(m) {
		for _, param := range m.Params {
			param.Pattern.Accept(v)
		}
		if m.ReturnType != nil {
			m.ReturnType.Accept(v)
		}
		if m.Body != nil {
			m.Body.Accept(v)
		}
	}
	v.ExitClassElem(m)
}
func (m *MethodElem) Span() Span { return m.Span_ }

// GetterElem represents a getter in a class.
type GetterElem struct {
	Name       *Ident
	TypeParams []*TypeParam // generic type parameters for the getter (rare, but for consistency)
	ReturnType TypeAnn      // optional
	Body       *Block       // optional
	Static     bool         // true if this is a static getter
	Private    bool         // true if this is a private getter
	Span_      Span
}

func (*GetterElem) IsClassElem() {}
func (g *GetterElem) Accept(v Visitor) {
	if v.EnterClassElem(g) {
		g.Name.Accept(v)
		if g.ReturnType != nil {
			g.ReturnType.Accept(v)
		}
		if g.Body != nil {
			g.Body.Accept(v)
		}
	}
	v.ExitClassElem(g)
}
func (g *GetterElem) Span() Span { return g.Span_ }

// SetterElem represents a setter in a class.
type SetterElem struct {
	Name       *Ident
	TypeParams []*TypeParam // generic type parameters for the setter (rare, but for consistency)
	Params     []*Param     // should have exactly one param
	Body       *Block       // optional
	Static     bool         // true if this is a static setter
	Private    bool         // true if this is a private setter
	Span_      Span
}

func (*SetterElem) IsClassElem() {}
func (s *SetterElem) Accept(v Visitor) {
	if v.EnterClassElem(s) {
		s.Name.Accept(v)
		for _, param := range s.Params {
			param.Pattern.Accept(v)
		}
		if s.Body != nil {
			s.Body.Accept(v)
		}
	}
	v.ExitClassElem(s)
}
func (s *SetterElem) Span() Span { return s.Span_ }
