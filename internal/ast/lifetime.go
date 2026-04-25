package ast

// LifetimeAnnNode is the interface satisfied by both LifetimeAnn and
// LifetimeUnionAnn — anywhere a lifetime annotation can appear (a single
// lifetime like 'a or a multi-lifetime union like ('a | 'b)) the field
// stores a LifetimeAnnNode so callers can handle either form.
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

// LifetimeUnionAnn represents multiple lifetimes on a single type
// (e.g. ('a | 'b) in `('a | 'b) Point`). Used when a value may carry one
// of several lifetimes — typically the return type of a function whose
// body conditionally returns one of multiple parameters.
type LifetimeUnionAnn struct {
	Lifetimes []*LifetimeAnn
	span      Span
}

func NewLifetimeUnionAnn(lifetimes []*LifetimeAnn, span Span) *LifetimeUnionAnn {
	return &LifetimeUnionAnn{Lifetimes: lifetimes, span: span}
}
func (l *LifetimeUnionAnn) Span() Span         { return l.span }
func (*LifetimeUnionAnn) isLifetimeAnnNode() {}
