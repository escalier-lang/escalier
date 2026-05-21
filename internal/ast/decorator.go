package ast

// Decorator is a `@name(args...)` annotation attached to a declaration.
//
// Today only `@js("string-literal")` is exercised — see
// planning/builtins/implementation_plan.md §3.3 — but the AST shape
// allows multiple positional arguments so richer decorator forms can
// be added later without a node change.
//
// TODO(#634): give Decorator an Accept method and wire it into the
// decoratable decls' Accept methods so visitors see decorator args.
type Decorator struct {
	Name  *Ident
	Args  []Expr
	Span_ Span
}

func (d *Decorator) Span() Span { return d.Span_ }
