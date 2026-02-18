package codegen

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/escalier-lang/escalier/internal/ast"
)

// buildJSXElement transforms a JSX element into a _jsx or _jsxs call.
// <div className="foo">Hello</div>
// becomes:
// _jsx("div", { className: "foo", children: "Hello" })
//
// <div><span>One</span><span>Two</span></div>
// becomes:
// _jsxs("div", { children: [_jsx("span", { children: "One" }), _jsx("span", { children: "Two" })] })
func (b *Builder) buildJSXElement(expr *ast.JSXElementExpr) (Expr, []Stmt) {
	var stmts []Stmt
	tagName := expr.Opening.Name

	// 1. Build the element type (string for intrinsic, identifier for component)
	var elementType Expr
	if isIntrinsicElement(tagName) {
		elementType = NewLitExpr(NewStrLit(ast.QualIdentToString(tagName), expr), expr)
	} else {
		// Handle member expressions like Ctx.Provider
		elementType = buildTagExpression(tagName, expr)
	}

	// 2. Build children
	childrenExprs, childrenStmts := b.buildJSXChildren(expr.Children, expr)
	stmts = slices.Concat(stmts, childrenStmts)

	// 3. Build props object with children included
	propsExpr, propsStmts := b.buildJSXPropsWithChildren(expr.Opening.Attrs, childrenExprs, expr)
	stmts = slices.Concat(stmts, propsStmts)

	// 4. Build _jsx or _jsxs call based on number of children
	var callee Expr
	if len(childrenExprs) > 1 {
		b.hasJsxs = true
		callee = NewIdentExpr("_jsxs", "", expr)
	} else {
		b.hasJsx = true
		callee = NewIdentExpr("_jsx", "", expr)
	}

	args := []Expr{elementType, propsExpr}

	return NewCallExpr(callee, args, false, expr), stmts
}

// buildJSXFragment transforms a JSX fragment into a _jsx or _jsxs call with _Fragment.
// <><div /><span /></>
// becomes:
// _jsxs(_Fragment, { children: [_jsx("div", {}), _jsx("span", {})] })
func (b *Builder) buildJSXFragment(expr *ast.JSXFragmentExpr) (Expr, []Stmt) {
	var stmts []Stmt

	// Build children
	childrenExprs, childrenStmts := b.buildJSXChildren(expr.Children, expr)
	stmts = slices.Concat(stmts, childrenStmts)

	// Build props object with children
	var props []ObjExprElem
	if len(childrenExprs) == 1 {
		key := NewIdentExpr("children", "", expr)
		props = append(props, NewPropertyExpr(key, childrenExprs[0], expr))
	} else if len(childrenExprs) > 1 {
		key := NewIdentExpr("children", "", expr)
		childrenArray := NewArrayExpr(childrenExprs, expr)
		props = append(props, NewPropertyExpr(key, childrenArray, expr))
	}
	propsExpr := NewObjectExpr(props, expr)

	// Track Fragment usage
	b.hasFragment = true

	// Use _jsx for 0 or 1 child, _jsxs for multiple children
	var callee Expr
	if len(childrenExprs) > 1 {
		b.hasJsxs = true
		callee = NewIdentExpr("_jsxs", "", expr)
	} else {
		b.hasJsx = true
		callee = NewIdentExpr("_jsx", "", expr)
	}

	fragmentType := NewIdentExpr("_Fragment", "", expr)
	args := []Expr{fragmentType, propsExpr}

	return NewCallExpr(callee, args, false, expr), stmts
}

// buildJSXPropsWithChildren builds a props object from JSX attributes and children.
// Children are included as a "children" property in the props object.
// For multiple children, they are wrapped in an array.
func (b *Builder) buildJSXPropsWithChildren(attrs []ast.JSXAttrElem, children []Expr, source *ast.JSXElementExpr) (Expr, []Stmt) {
	var stmts []Stmt
	var props []ObjExprElem

	for _, attrElem := range attrs {
		switch attr := attrElem.(type) {
		case *ast.JSXAttr:
			// Regular prop
			key := NewIdentExpr(attr.Name, "", source)
			value, valueStmts := b.buildJSXAttrValue(attr, source)
			stmts = slices.Concat(stmts, valueStmts)
			props = append(props, NewPropertyExpr(key, value, source))
		case *ast.JSXSpreadAttr:
			// Spread prop: {...props}
			spreadExpr, spreadStmts := b.buildExpr(attr.Expr, source)
			stmts = slices.Concat(stmts, spreadStmts)
			props = append(props, NewRestSpreadExpr(spreadExpr, source))
		}
	}

	// Add children property if there are children
	if len(children) == 1 {
		// Single child: children: <child>
		key := NewIdentExpr("children", "", source)
		props = append(props, NewPropertyExpr(key, children[0], source))
	} else if len(children) > 1 {
		// Multiple children: children: [<child1>, <child2>, ...]
		key := NewIdentExpr("children", "", source)
		childrenArray := NewArrayExpr(children, source)
		props = append(props, NewPropertyExpr(key, childrenArray, source))
	}

	// Always return an object, even if empty (jsx runtime expects an object, not null)
	return NewObjectExpr(props, source), stmts
}

// buildJSXAttrValue builds the value expression for a JSX attribute.
func (b *Builder) buildJSXAttrValue(attr *ast.JSXAttr, source *ast.JSXElementExpr) (Expr, []Stmt) {
	if attr.Value == nil {
		// Boolean shorthand: <input disabled /> -> { disabled: true }
		return NewLitExpr(NewBoolLit(true, source), source), nil
	}

	switch v := (*attr.Value).(type) {
	case *ast.JSXString:
		return NewLitExpr(NewStrLit(v.Value, source), source), nil
	case *ast.JSXExprContainer:
		return b.buildExpr(v.Expr, source)
	case *ast.JSXElementExpr:
		return b.buildJSXElement(v)
	case *ast.JSXFragmentExpr:
		return b.buildJSXFragment(v)
	default:
		// Fallback: should not happen
		return NewLitExpr(NewNullLit(source), source), nil
	}
}

// buildJSXChildren transforms JSX children into an array of expressions.
func (b *Builder) buildJSXChildren(children []ast.JSXChild, source ast.Expr) ([]Expr, []Stmt) {
	var exprs []Expr
	var stmts []Stmt

	for _, child := range children {
		switch ch := child.(type) {
		case *ast.JSXText:
			// Normalize whitespace and skip empty text
			text := normalizeJSXText(ch.Value)
			if text != "" {
				exprs = append(exprs, NewLitExpr(NewStrLit(text, source), source))
			}
		case *ast.JSXExprContainer:
			expr, exprStmts := b.buildExpr(ch.Expr, source)
			exprs = append(exprs, expr)
			stmts = slices.Concat(stmts, exprStmts)
		case *ast.JSXElementExpr:
			expr, exprStmts := b.buildJSXElement(ch)
			exprs = append(exprs, expr)
			stmts = slices.Concat(stmts, exprStmts)
		case *ast.JSXFragmentExpr:
			expr, exprStmts := b.buildJSXFragment(ch)
			exprs = append(exprs, expr)
			stmts = slices.Concat(stmts, exprStmts)
		}
	}

	return exprs, stmts
}

// buildTagExpression builds an expression for a JSX tag name.
// Handles simple identifiers and member expressions like "Ctx.Provider".
func buildTagExpression(tagName ast.QualIdent, source *ast.JSXElementExpr) Expr {
	switch t := tagName.(type) {
	case *ast.Ident:
		return NewIdentExpr(t.Name, "", source)
	case *ast.Member:
		left := buildTagExpression(t.Left, source)
		return NewMemberExpr(left, NewIdentifier(t.Right.Name, source), false, source)
	default:
		// Should not happen
		return NewIdentExpr("", "", source)
	}
}

// isIntrinsicElement returns true if the tag name represents an HTML element.
// Intrinsic elements start with a lowercase letter.
// Only simple identifiers can be intrinsic (member expressions like Foo.Bar are always components).
func isIntrinsicElement(name ast.QualIdent) bool {
	ident, ok := name.(*ast.Ident)
	if !ok {
		// Member expressions are never intrinsic elements
		return false
	}
	r, _ := utf8.DecodeRuneInString(ident.Name)
	if r == utf8.RuneError {
		return false
	}
	return unicode.IsLower(r)
}

// normalizeJSXText normalizes whitespace in JSX text content.
// This matches React's behavior of collapsing whitespace.
func normalizeJSXText(text string) string {
	// Trim leading and trailing whitespace
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	// Collapse multiple whitespace characters into a single space
	var result strings.Builder
	prevWasSpace := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			if !prevWasSpace {
				result.WriteRune(' ')
				prevWasSpace = true
			}
		} else {
			result.WriteRune(r)
			prevWasSpace = false
		}
	}

	return result.String()
}
