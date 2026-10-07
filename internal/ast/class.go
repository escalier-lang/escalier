package ast

type ClassDecl struct {
	declDoc
	Name           *Ident
	LifetimeParams []*LifetimeParam  // generic lifetime parameters (e.g. <'a>)
	TypeParams     []*TypeParam      // generic type parameters
	Extends        *TypeRefTypeAnn   // optional superclass (can be a simple identifier or a generic type reference)
	Implements     []*TypeRefTypeAnn // interfaces this class implements (may be nil/empty)
	Body           []ClassElem       // fields, methods, etc.
	Decorators     []*Decorator
	export         bool
	declare        bool
	override       bool
	final          bool
	span           Span
	declProvenance
	commentSlots
}

type ClassElem interface {
	IsClassElem()
	Accept(v Visitor)
	Span() Span
	Commented
	// Doc returns the leading JSDoc (`/** ... */`) retained on the elem,
	// verbatim with delimiters, or "" if absent. Populated by both the
	// dts_to_esc converter and the regular parser (#663).
	Doc() string
	SetDoc(string)
}

// MethodReceiver describes a `self` receiver on a method, getter, setter, or
// constructor. A nil *MethodReceiver means no receiver was written. That covers
// static members, getters and setters with an empty parameter list, and
// non-static instance methods that omit `self`, which the checker reports as
// MissingSelfReceiverError.
//
// A receiver either borrows the instance for the call or consumes it:
//
//	&self          → &MethodReceiver{Mode: BorrowReceiver}
//	&mut self      → &MethodReceiver{Mode: BorrowReceiver, Mut: true}
//	&'a self       → &MethodReceiver{Mode: BorrowReceiver, Lifetime: 'a}
//	&'a mut self   → &MethodReceiver{Mode: BorrowReceiver, Mut: true, Lifetime: 'a}
//	self           → &MethodReceiver{Mode: ConsumeReceiver}
//	mut self       → &MethodReceiver{Mode: ConsumeReceiver, Mut: true}
//
// Only a borrow carries a lifetime. A consuming receiver moves the instance
// into the call, so there is no loan for a lifetime to bound.
type MethodReceiver struct {
	Mode     ReceiverMode
	Mut      bool
	Lifetime LifetimeAnnNode // optional, and only on a borrow
	Span_    Span
	commentSlots
}

// ReceiverMode says whether a method receiver borrows the instance or consumes
// it. The zero value is a borrow, the form nearly every method takes.
type ReceiverMode int

const (
	// BorrowReceiver is `&self` or `&mut self`. The caller keeps the instance.
	BorrowReceiver ReceiverMode = iota
	// ConsumeReceiver is `self` or `mut self`. The call moves the instance, so
	// the caller cannot use it afterwards.
	ConsumeReceiver
)

// Consumes reports whether the receiver moves the instance into the call.
func (r *MethodReceiver) Consumes() bool { return r.Mode == ConsumeReceiver }

func (r *MethodReceiver) Span() Span { return r.Span_ }

// acceptReceiver visits the lifetime on a `self` receiver, the `'a` in
// `&'a mut self`. A receiver holds no other node, and a nil one means the
// member wrote no receiver at all.
//
// The receiver sits beside the member's function rather than inside it, so the
// walk reaches this lifetime before the `<'a>` list that binds it. #635 would
// put the two in source order.
func acceptReceiver(v Visitor, r *MethodReceiver) {
	if r != nil && r.Lifetime != nil {
		r.Lifetime.Accept(v)
	}
}

// Exported constructor for use in parser
func NewClassDecl(name *Ident, lifetimeParams []*LifetimeParam, typeParams []*TypeParam, extends *TypeRefTypeAnn, implements []*TypeRefTypeAnn, body []ClassElem, export, declare, final bool, span Span) *ClassDecl {
	return &ClassDecl{
		Name:           name,
		LifetimeParams: lifetimeParams,
		TypeParams:     typeParams,
		Extends:        extends,
		Implements:     implements,
		Body:           body,
		export:         export,
		declare:        declare,
		final:          final,
		span:           span,
		declProvenance: declProvenance{},
		commentSlots:   commentSlots{},
	}
}

func (*ClassDecl) isDecl()              {}
func (d *ClassDecl) Export() bool       { return d.export }
func (d *ClassDecl) SetExport(e bool)   { d.export = e }
func (d *ClassDecl) Declare() bool      { return d.declare }
func (d *ClassDecl) Override() bool     { return d.override }
func (d *ClassDecl) SetOverride(o bool) { d.override = o }

// Final reports whether the class was declared `final`. A final class has no
// subclasses, so its instance type is exact (exact-types §2.6): the checker projects
// an exact body for it.
func (d *ClassDecl) Final() bool { return d.final }
func (d *ClassDecl) Span() Span  { return d.span }
func (d *ClassDecl) Accept(v Visitor) {
	// TODO(#634): traverse d.Decorators once Decorator has Accept.
	if v.EnterDecl(d) {
		acceptLifetimeParams(v, d.LifetimeParams)
		acceptTypeParams(v, d.TypeParams)
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

type FieldElem struct {
	declDoc
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
	commentSlots
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
	declDoc
	Name     ObjKey
	Fn       *FuncExpr
	Receiver *MethodReceiver // nil if static / no receiver
	Static   bool            // true if this is a static method
	Private  bool            // true if this is a private method
	Span_    Span
	commentSlots
}

func (*MethodElem) IsClassElem() {}
func (m *MethodElem) Accept(v Visitor) {
	if v.EnterClassElem(m) {
		m.Name.Accept(v)
		acceptReceiver(v, m.Receiver)
		if m.Fn != nil {
			m.Fn.Accept(v)
		}
	}
	v.ExitClassElem(m)
}
func (m *MethodElem) Span() Span { return m.Span_ }

// ImplementationArm returns the implementation of a method overload set, or nil when the set
// has none. A set has an implementation when exactly one arm has a body and every other arm
// is a bodiless signature:
//
//	contains(&self, x: number) -> boolean,
//	contains(&self, x: unknown) -> boolean { return false },
//
// Callers see the bodiless signatures. The implementation is the arm the runtime runs. arms
// holds one overload set, the same-named arms that are all static or all instance members.
func ImplementationArm(arms []*MethodElem) *MethodElem {
	var impl *MethodElem
	for _, arm := range arms {
		if arm.Fn == nil || arm.Fn.Body == nil {
			continue
		}
		if impl != nil {
			return nil
		}
		impl = arm
	}
	if len(arms) < 2 {
		return nil
	}
	return impl
}

// GetterElem represents a getter in a class.
type GetterElem struct {
	declDoc
	Name     ObjKey
	Fn       *FuncExpr
	Receiver *MethodReceiver // nil if static / no receiver
	Static   bool            // true if this is a static getter
	Private  bool            // true if this is a private getter
	Span_    Span
	commentSlots
}

func (*GetterElem) IsClassElem() {}
func (g *GetterElem) Accept(v Visitor) {
	if v.EnterClassElem(g) {
		g.Name.Accept(v)
		acceptReceiver(v, g.Receiver)
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
	declDoc
	Fn       *FuncExpr
	Receiver *MethodReceiver // nil if absent. Carried for diagnostics — a non-nil Lifetime is rejected by validation.
	Private  bool            // reserved for future "Private Constructors" work
	Span_    Span
	commentSlots
}

func (*ConstructorElem) IsClassElem() {}
func (c *ConstructorElem) Accept(v Visitor) {
	if v.EnterClassElem(c) {
		acceptReceiver(v, c.Receiver)
		if c.Fn != nil {
			c.Fn.Accept(v)
		}
	}
	v.ExitClassElem(c)
}
func (c *ConstructorElem) Span() Span { return c.Span_ }

// SetterElem represents a setter in a class.
type SetterElem struct {
	declDoc
	Name     ObjKey
	Fn       *FuncExpr
	Receiver *MethodReceiver // nil if static / no receiver
	Static   bool            // true if this is a static setter
	Private  bool            // true if this is a private setter
	Span_    Span
	commentSlots
}

func (*SetterElem) IsClassElem() {}
func (s *SetterElem) Accept(v Visitor) {
	if v.EnterClassElem(s) {
		s.Name.Accept(v)
		acceptReceiver(v, s.Receiver)
		if s.Fn != nil {
			s.Fn.Accept(v)
		}
	}
	v.ExitClassElem(s)
}
func (s *SetterElem) Span() Span { return s.Span_ }

// CallableElem represents an unnamed `(...) -> T` call signature in a class body. It makes
// the class value callable: `Symbol("desc")` calls it where `Symbol()` alone would construct.
//
// The specification forbids constructing `Symbol` and `BigInt`, so a call signature is the
// only way to make one, and trio fusion has nowhere else to put the `SymbolConstructor` member
// that declares it. `Fn.Body` is nil: a call signature declares a shape rather than an
// implementation, so only a `declare class` may carry one.
type CallableElem struct {
	declDoc
	Fn    *FuncExpr
	Span_ Span
	commentSlots
}

func (*CallableElem) IsClassElem() {}
func (c *CallableElem) Accept(v Visitor) {
	if v.EnterClassElem(c) {
		if c.Fn != nil {
			c.Fn.Accept(v)
		}
	}
	v.ExitClassElem(c)
}
func (c *CallableElem) Span() Span { return c.Span_ }
