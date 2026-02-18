package checker

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// inferJSXElement infers the type of a JSX element expression.
// Returns JSX.Element type and any type errors.
func (c *Checker) inferJSXElement(ctx Context, expr *ast.JSXElementExpr) (type_system.Type, []Error) {
	var errors []Error
	provenance := &ast.NodeProvenance{Node: expr}

	tagName := expr.Opening.Name
	isIntrinsic := isIntrinsicElement(tagName)

	// 1. Resolve the element type (component or intrinsic)
	var propsType type_system.Type
	var propsErrors []Error
	if isIntrinsic {
		propsType, propsErrors = c.getIntrinsicElementProps(ctx, tagName, expr)
	} else {
		propsType, propsErrors = c.getComponentProps(ctx, tagName, expr)
	}
	errors = slices.Concat(errors, propsErrors)

	// 2. Build props object type from attributes
	attrType, attrErrors := c.inferJSXAttributes(ctx, expr.Opening.Attrs)
	errors = slices.Concat(errors, attrErrors)

	// 3. Unify attribute types with expected props (if we have expected props)
	if propsType != nil && attrType != nil {
		unifyErrors := c.Unify(ctx, attrType, propsType)
		errors = slices.Concat(errors, unifyErrors)
	}

	// 4. Type check children
	childErrors := c.inferJSXChildren(ctx, expr.Children)
	errors = slices.Concat(errors, childErrors)

	// 5. Return JSX.Element type
	return c.getJSXElementType(provenance), errors
}

// inferJSXFragment infers the type of a JSX fragment expression.
// Returns JSX.Element type and any type errors.
func (c *Checker) inferJSXFragment(ctx Context, expr *ast.JSXFragmentExpr) (type_system.Type, []Error) {
	var errors []Error
	provenance := &ast.NodeProvenance{Node: expr}

	// Fragments only have children, no props to validate
	childErrors := c.inferJSXChildren(ctx, expr.Children)
	errors = slices.Concat(errors, childErrors)

	// Return JSX.Element type
	return c.getJSXElementType(provenance), errors
}

// isIntrinsicElement returns true if the tag name represents an HTML element.
// Intrinsic elements start with a lowercase letter.
func isIntrinsicElement(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	if r == utf8.RuneError {
		return false
	}
	return unicode.IsLower(r)
}

// inferJSXAttributes builds an object type from JSX attributes.
func (c *Checker) inferJSXAttributes(ctx Context, attrs []ast.JSXAttrElem) (type_system.Type, []Error) {
	var errors []Error
	elems := make([]type_system.ObjTypeElem, 0, len(attrs))

	for _, attrElem := range attrs {
		switch attr := attrElem.(type) {
		case *ast.JSXAttr:
			var valueType type_system.Type

			if attr.Value == nil {
				// Boolean shorthand: <input disabled />
				valueType = type_system.NewBoolLitType(nil, true)
			} else {
				switch v := (*attr.Value).(type) {
				case *ast.JSXString:
					valueType = type_system.NewStrLitType(nil, v.Value)
				case *ast.JSXExprContainer:
					var exprErrors []Error
					valueType, exprErrors = c.inferExpr(ctx, v.Expr)
					errors = slices.Concat(errors, exprErrors)
				case *ast.JSXElementExpr:
					// JSX element as attribute value (rare but possible)
					var elemErrors []Error
					valueType, elemErrors = c.inferJSXElement(ctx, v)
					errors = slices.Concat(errors, elemErrors)
				case *ast.JSXFragmentExpr:
					// JSX fragment as attribute value
					var fragErrors []Error
					valueType, fragErrors = c.inferJSXFragment(ctx, v)
					errors = slices.Concat(errors, fragErrors)
				}
			}

			if valueType == nil {
				valueType = type_system.NewUnknownType(nil)
			}
			key := type_system.NewStrKey(attr.Name)
			elems = append(elems, type_system.NewPropertyElem(key, valueType))

		case *ast.JSXSpreadAttr:
			// Spread attribute: {...props}
			var spreadType type_system.Type
			var spreadErrors []Error
			spreadType, spreadErrors = c.inferExpr(ctx, attr.Expr)
			errors = slices.Concat(errors, spreadErrors)

			if spreadType == nil {
				spreadType = type_system.NewUnknownType(nil)
			}
			elems = append(elems, type_system.NewRestSpreadElem(spreadType))
		}
	}

	return type_system.NewObjectType(nil, elems), errors
}

// inferJSXChildren type-checks all children of a JSX element.
func (c *Checker) inferJSXChildren(ctx Context, children []ast.JSXChild) []Error {
	var errors []Error

	for _, child := range children {
		switch ch := child.(type) {
		case *ast.JSXText:
			// Text is always valid - nothing to type check
			// In a more complete implementation, we might normalize whitespace
			_ = normalizeJSXText(ch.Value)
		case *ast.JSXExprContainer:
			_, exprErrors := c.inferExpr(ctx, ch.Expr)
			errors = slices.Concat(errors, exprErrors)
		case *ast.JSXElementExpr:
			_, elemErrors := c.inferJSXElement(ctx, ch)
			errors = slices.Concat(errors, elemErrors)
		case *ast.JSXFragmentExpr:
			_, fragErrors := c.inferJSXFragment(ctx, ch)
			errors = slices.Concat(errors, fragErrors)
		}
	}

	return errors
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

// Phase 1 stub - returns nil to allow any props (replaced in Phase 2)
func (c *Checker) getIntrinsicElementProps(ctx Context, tagName string, expr *ast.JSXElementExpr) (type_system.Type, []Error) {
	return nil, nil
}

// Phase 1 stub - returns nil to allow any props (replaced in Phase 3)
func (c *Checker) getComponentProps(ctx Context, tagName string, expr *ast.JSXElementExpr) (type_system.Type, []Error) {
	return nil, nil
}

// Phase 1 stub - returns empty object type (replaced in Phase 4)
func (c *Checker) getJSXElementType(provenance *ast.NodeProvenance) type_system.Type {
	return type_system.NewObjectType(provenance, nil)
}
