package ast

// LifetimeAnnNode is the node type for a lifetime annotation appearing on a
// type, such as the 'a in `mut 'a Point`. LifetimeAnn is its only implementer;
// the interface marks the lifetime-annotation position on a type and exposes the
// node's Span.
type LifetimeAnnNode interface {
	isLifetimeAnnNode()
	Span() Span
}

// LifetimeAnn represents a single lifetime in source code (e.g. 'a). Used
// for both declaration sites (in <'a, T>) and use sites (in mut 'a Point);
// the checker resolves which is which during inference.
type LifetimeAnn struct {
	Name string
	span Span
}

func NewLifetimeAnn(name string, span Span) *LifetimeAnn {
	return &LifetimeAnn{Name: name, span: span}
}
func (l *LifetimeAnn) Span() Span         { return l.span }
func (*LifetimeAnn) isLifetimeAnnNode() {}

// LifetimeParam is a lifetime binder in a <…> quantifier list. Bounds are the
// lifetimes this one must outlive. In <'a, 'b: 'a>, 'b has the bound {'a}, read
// "'b outlives 'a". Several bounds are written with &, the lattice meet, the
// way a type-param bound writes an intersection. In <'a: 'b & 'c>, 'a has the
// bounds {'b, 'c}. A bare <'a> has no bounds. Mirrors TypeParam's Name +
// Constraint shape so one quantifier list carries both sorts.
//
// The ':' here means outliving, not the subtyping ':' of a TypeParam bound. A
// binder is a lifetime or a type, never both, so the two never mix on one
// binder and the checker picks the relation from the binder's sort.
type LifetimeParam struct {
	Name   string
	Bounds []*LifetimeAnn
	span   Span
}

func NewLifetimeParam(name string, bounds []*LifetimeAnn, span Span) *LifetimeParam {
	return &LifetimeParam{Name: name, Bounds: bounds, span: span}
}
func (l *LifetimeParam) Span() Span { return l.span }
