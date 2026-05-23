package codegen

import (
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// This file implements the `@js("...")` codegen lowering described in
// planning/builtins/implementation_plan.md §3.1-§3.2:
//
//   - IdentExpr `parseInt` / `Array` whose owning decl has
//     `@js("parseInt")` / `@js("Array")` lowers to the parsed JS
//     expression. The caller in builder.go reads `expr.Owner` directly.
//   - MemberExpr `math.sin` / `iterator.iteratorKey` whose property's
//     binding owns a decl with `@js("Math.sin")` /
//     `@js("Symbol.iterator")` lowers to the parsed JS expression (the
//     receiver vanishes because @js carries the complete JS-runtime
//     expression).

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
// nil, isn't a decorator-carrying decl, or carries no well-formed `@js`
// decorator. Loader rules §3.4 already reject malformed `@js` on
// pseudo-package decls, so reaching this from production code means
// well-formed or absent — both handled by ast.FindJsDecorator.
func jsExprFromOwner(owner type_system.BindingOwner) (string, bool) {
	decl, ok := owner.(ast.Decl)
	if !ok {
		return "", false
	}
	_, arg, ok := ast.FindJsDecorator(decl)
	if !ok {
		return "", false
	}
	return arg, true
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
