package codegen

import (
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// jsLoweringFor returns the lowered JS expression for an AST identifier
// or member-access node that resolves to a pseudo-package declaration
// carrying an `@js("...")` decorator. Returns (nil, false) when no
// lowering applies — callers fall through to ordinary codegen.
//
// See planning/builtins/implementation_plan.md §3.1-§3.2:
//   - IdentExpr `parseInt` / `Array` whose owning decl has
//     `@js("parseInt")` / `@js("Array")` lowers to the parsed JS
//     expression.
//   - MemberExpr `math.sin` / `iterator.iteratorKey` whose property's
//     binding owns a decl with `@js("Math.sin")` /
//     `@js("Symbol.iterator")` lowers to the parsed JS expression (the
//     receiver vanishes because @js carries the complete JS-runtime
//     expression).
func (b *Builder) jsLoweringFor(expr ast.Expr) (Expr, bool) {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		jsExpr, ok := jsExprFromOwner(e.Owner)
		if !ok {
			return nil, false
		}
		return buildDottedJSExpr(jsExpr, e), true
	case *ast.MemberExpr:
		jsExpr, ok := memberJSExpr(e)
		if !ok {
			return nil, false
		}
		return buildDottedJSExpr(jsExpr, e), true
	}
	return nil, false
}

// memberJSExpr returns the JS lowering for a MemberExpr whose receiver
// resolves to a NamespaceType (the package binding under ?local /
// ?nested / ?flat). The property's Binding in that namespace owns a
// decl whose `@js("...")` decorator carries the lowering.
func memberJSExpr(m *ast.MemberExpr) (string, bool) {
	if m.Prop == nil {
		return "", false
	}
	objType := m.Object.InferredType()
	if objType == nil {
		return "", false
	}
	nsType, ok := type_system.Prune(objType).(*type_system.NamespaceType)
	if !ok || nsType.Namespace == nil {
		return "", false
	}
	binding := nsType.Namespace.Values[m.Prop.Name]
	if binding == nil {
		return "", false
	}
	return jsExprFromOwner(binding.Owner)
}

// jsExprFromOwner reads the `@js("...")` decorator argument off a
// binding's owning declaration. Returns ("", false) when the owner is
// nil or carries no `@js` decorator. The owner is guaranteed (by the
// `BindingOwner` sumtype) to be one of the three decl kinds switched
// on below.
func jsExprFromOwner(owner type_system.BindingOwner) (string, bool) {
	var decorators []*ast.Decorator
	switch d := owner.(type) {
	case *ast.VarDecl:
		decorators = d.Decorators
	case *ast.FuncDecl:
		decorators = d.Decorators
	case *ast.ClassDecl:
		decorators = d.Decorators
	default:
		return "", false
	}
	for _, dec := range decorators {
		if dec.Name == nil || dec.Name.Name != "js" || len(dec.Args) != 1 {
			continue
		}
		lit, ok := dec.Args[0].(*ast.LiteralExpr)
		if !ok {
			continue
		}
		s, ok := lit.Lit.(*ast.StrLit)
		if !ok {
			continue
		}
		return s.Value, true
	}
	return "", false
}

// buildDottedJSExpr parses a dotted JS expression like "Math.sin" or
// "Symbol.iterator" into a codegen IdentExpr + chain of MemberExprs.
// Single identifiers (no dot) become a bare IdentExpr. The whole chain
// shares the same source AST node so source maps still point back at
// the original Escalier expression.
//
// jsArg comes from a string literal in an `@js(...)` decorator authored
// in a stdlib `.esc` file, so empty segments (".foo", "Math..PI") would
// be a stdlib authoring bug — not validated here.
func buildDottedJSExpr(jsArg string, source ast.Node) Expr {
	parts := strings.Split(jsArg, ".")
	var out Expr = NewIdentExpr(parts[0], "", source)
	for _, part := range parts[1:] {
		out = NewMemberExpr(out, NewIdentifier(part, source), false, source)
	}
	return out
}
