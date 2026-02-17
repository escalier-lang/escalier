package codegen

import (
	"slices"
	"strings"
	"unicode"

	"github.com/escalier-lang/escalier/internal/ast"
)

// buildJSXElement transforms a JSX element into React.createElement call.
// <div className="foo">Hello</div>
// becomes:
// React.createElement("div", { className: "foo" }, "Hello")
func (b *Builder) buildJSXElement(expr *ast.JSXElementExpr, parent ast.Expr) (Expr, []Stmt) {
	var stmts []Stmt
	tagName := expr.Opening.Name

	// 1. Build the element type (string for intrinsic, identifier for component)
	var elementType Expr
	if isIntrinsicElement(tagName) {
		elementType = NewLitExpr(NewStrLit(tagName, expr), expr)
	} else {
		// Handle member expressions like Ctx.Provider
		elementType = buildTagExpression(tagName, expr)
	}

	// 2. Build props object (or null if no props)
	propsExpr, propsStmts := b.buildJSXProps(expr.Opening.Attrs, expr)
	stmts = slices.Concat(stmts, propsStmts)

	// 3. Build children
	childrenExprs, childrenStmts := b.buildJSXChildren(expr.Children, expr)
	stmts = slices.Concat(stmts, childrenStmts)

	// 4. Build React.createElement call
	args := []Expr{elementType, propsExpr}
	args = append(args, childrenExprs...)

	callee := NewMemberExpr(
		NewIdentExpr("React", "", expr),
		NewIdentifier("createElement", expr),
		false,
		expr,
	)

	return NewCallExpr(callee, args, false, expr), stmts
}

// buildJSXFragment transforms a JSX fragment into React.createElement(React.Fragment, null, ...).
// <><div /><span /></>
// becomes:
// React.createElement(React.Fragment, null, React.createElement("div", null), React.createElement("span", null))
func (b *Builder) buildJSXFragment(expr *ast.JSXFragmentExpr, parent ast.Expr) (Expr, []Stmt) {
	var stmts []Stmt

	// Build children
	childrenExprs, childrenStmts := b.buildJSXChildrenForFragment(expr.Children, expr)
	stmts = slices.Concat(stmts, childrenStmts)

	// React.createElement(React.Fragment, null, ...children)
	callee := NewMemberExpr(
		NewIdentExpr("React", "", expr),
		NewIdentifier("createElement", expr),
		false,
		expr,
	)

	fragmentType := NewMemberExpr(
		NewIdentExpr("React", "", expr),
		NewIdentifier("Fragment", expr),
		false,
		expr,
	)

	args := []Expr{fragmentType, NewLitExpr(NewNullLit(expr), expr)}
	args = append(args, childrenExprs...)

	return NewCallExpr(callee, args, false, expr), stmts
}

// buildJSXProps builds a props object from JSX attributes.
// Returns null literal if no attributes, otherwise returns an object expression.
// Note: Spread attributes ({...props}) are not yet supported (planned for Phase 5).
func (b *Builder) buildJSXProps(attrs []*ast.JSXAttr, source *ast.JSXElementExpr) (Expr, []Stmt) {
	if len(attrs) == 0 {
		return NewLitExpr(NewNullLit(source), source), nil
	}

	var stmts []Stmt
	var props []ObjExprElem

	for _, attr := range attrs {
		// Regular prop
		key := NewIdentExpr(attr.Name, "", source)
		value, valueStmts := b.buildJSXAttrValue(attr, source)
		stmts = slices.Concat(stmts, valueStmts)
		props = append(props, NewPropertyExpr(key, value, source))
	}

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
		return b.buildJSXElement(v, nil)
	case *ast.JSXFragmentExpr:
		return b.buildJSXFragment(v, nil)
	default:
		// Fallback: should not happen
		return NewLitExpr(NewNullLit(source), source), nil
	}
}

// buildJSXChildren transforms JSX children into an array of expressions.
func (b *Builder) buildJSXChildren(children []ast.JSXChild, source *ast.JSXElementExpr) ([]Expr, []Stmt) {
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
			expr, exprStmts := b.buildJSXElement(ch, nil)
			exprs = append(exprs, expr)
			stmts = slices.Concat(stmts, exprStmts)
		case *ast.JSXFragmentExpr:
			expr, exprStmts := b.buildJSXFragment(ch, nil)
			exprs = append(exprs, expr)
			stmts = slices.Concat(stmts, exprStmts)
		}
	}

	return exprs, stmts
}

// buildJSXChildrenForFragment transforms JSX children for a fragment expression.
func (b *Builder) buildJSXChildrenForFragment(children []ast.JSXChild, source *ast.JSXFragmentExpr) ([]Expr, []Stmt) {
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
			expr, exprStmts := b.buildJSXElement(ch, nil)
			exprs = append(exprs, expr)
			stmts = slices.Concat(stmts, exprStmts)
		case *ast.JSXFragmentExpr:
			expr, exprStmts := b.buildJSXFragment(ch, nil)
			exprs = append(exprs, expr)
			stmts = slices.Concat(stmts, exprStmts)
		}
	}

	return exprs, stmts
}

// buildTagExpression builds an expression for a JSX tag name.
// Handles simple identifiers and member expressions like "Ctx.Provider".
func buildTagExpression(tagName string, source *ast.JSXElementExpr) Expr {
	parts := strings.Split(tagName, ".")
	if len(parts) == 1 {
		return NewIdentExpr(tagName, "", source)
	}

	// Build member expression chain: Ctx.Provider -> MemberExpr(Ctx, Provider)
	result := Expr(NewIdentExpr(parts[0], "", source))
	for _, part := range parts[1:] {
		result = NewMemberExpr(result, NewIdentifier(part, source), false, source)
	}
	return result
}

// isIntrinsicElement returns true if the tag name represents an HTML element.
// Intrinsic elements start with a lowercase letter.
func isIntrinsicElement(name string) bool {
	if len(name) == 0 {
		return false
	}
	return unicode.IsLower(rune(name[0]))
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
