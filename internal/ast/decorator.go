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
}

func (d *Decorator) Span() Span { return d.Span_ }
