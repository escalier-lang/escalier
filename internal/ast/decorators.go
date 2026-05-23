package ast

// JSDecoratorName is the decorator that carries the JS-runtime expression
// each pseudo-package member lowers to at codegen. See
// planning/builtins/implementation_plan.md §3.
const JSDecoratorName = "js"

// DeclDecorators returns the decorator list attached to decl, or nil if
// decl is a kind that cannot carry decorators (the parser rejects
// decorators on those kinds at parse time; the nil return lets callers
// treat "no decorators" and "cannot carry" uniformly).
func DeclDecorators(decl Decl) []*Decorator {
	switch d := decl.(type) {
	case *VarDecl:
		return d.Decorators
	case *FuncDecl:
		return d.Decorators
	case *ClassDecl:
		return d.Decorators
	default:
		return nil
	}
}

// FindJsDecorator returns the first `@js("...")` decorator on decl and
// its argument string. Returns (nil, "", false) if no `@js` decorator is
// present, and (dec, "", false) if `@js` is present but the argument
// isn't a single string literal (reported as a loader error).
func FindJsDecorator(decl Decl) (*Decorator, string, bool) {
	for _, dec := range DeclDecorators(decl) {
		if dec.Name == nil || dec.Name.Name != JSDecoratorName {
			continue
		}
		if len(dec.Args) != 1 {
			return dec, "", false
		}
		lit, ok := dec.Args[0].(*LiteralExpr)
		if !ok {
			return dec, "", false
		}
		s, ok := lit.Lit.(*StrLit)
		if !ok {
			return dec, "", false
		}
		return dec, s.Value, true
	}
	return nil, "", false
}
