package ast

// Decorator is a `@name(args...)` annotation attached to a declaration.
//
// Today only `@js("string-literal")` is exercised — see
// planning/builtins/implementation_plan.md §3.3 — but the AST shape
// allows multiple positional arguments so richer decorator forms can
// be added later without a node change.
type Decorator struct {
	Name  *Ident
	Args  []Expr
	Span_ Span
	commentSlots
}

func (d *Decorator) Span() Span { return d.Span_ }

// Accept visits the decorator's arguments, which is the whole of what a
// decorator contributes to a walk. The name is a label the loader matches
// against rather than an expression to resolve, so it reaches no visitor hook,
// and Decorator itself is offered through none: a caller after the decorators
// on a node reads them off that node, through DeclDecorators or
// ClassElemDecorators.
func (d *Decorator) Accept(v Visitor) {
	for _, arg := range d.Args {
		if arg != nil {
			arg.Accept(v)
		}
	}
}

// acceptDecorators visits each decorator in a list, in source order.
func acceptDecorators(v Visitor, decorators []*Decorator) {
	for _, d := range decorators {
		if d != nil {
			d.Accept(v)
		}
	}
}

// nodeDecorators carries the decorator list on a node that takes decorators.
// Every ClassElem embeds it, as does every Decl but a namespace, so no node
// kind repeats the field. A Decl and a ClassElem never sit in the same struct,
// so one embed serves both.
type nodeDecorators struct {
	Decorators []*Decorator
}

func (n *nodeDecorators) decoratorList() []*Decorator { return n.Decorators }

// SetDecorators replaces a node's decorator list. It is promoted from the
// embed, so one definition serves every Decl and every ClassElem.
func (n *nodeDecorators) SetDecorators(decorators []*Decorator) { n.Decorators = decorators }

// DecoratorSetter is a node whose decorator list can be replaced. Every Decl
// but a namespace satisfies it, as does every ClassElem, in both cases through
// the promoted SetDecorators on the embed. A caller holding the Decl or
// ClassElem interface asserts against this rather than naming the kinds.
type DecoratorSetter interface {
	SetDecorators([]*Decorator)
}

// decorated is what DeclDecorators and ClassElemDecorators match against. The
// field is exported and reached directly on a concrete node, so the accessor
// exists only to let the helpers read it through the Decl and ClassElem
// interfaces.
type decorated interface {
	decoratorList() []*Decorator
}
