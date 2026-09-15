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

// elemDecorators carries the decorator list on a class member. Every ClassElem
// embeds it, so no member kind repeats the field.
type elemDecorators struct {
	Decorators []*Decorator
}

func (e *elemDecorators) decoratorList() []*Decorator { return e.Decorators }

// decorated is what ClassElemDecorators matches a member against. The field is
// exported and reached directly on a concrete member, so the accessor exists
// only to let the helper below read it through the ClassElem interface.
type decorated interface {
	decoratorList() []*Decorator
}
