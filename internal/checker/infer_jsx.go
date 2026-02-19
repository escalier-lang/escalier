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

	// 2. Infer and validate JSX attributes against expected props
	attrErrors := c.validateJSXAttributes(ctx, expr.Opening.Attrs, propsType)
	errors = slices.Concat(errors, attrErrors)

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
func isIntrinsicElement(name ast.QualIdent) bool {
	r, _ := utf8.DecodeRuneInString(ast.QualIdentToString(name))
	if r == utf8.RuneError {
		return false
	}
	return unicode.IsLower(r)
}

// validateJSXAttributes validates JSX attributes against expected props type.
// For each provided attribute, it checks that:
// 1. The attribute name exists in the expected props (if propsType is available)
// 2. The attribute value type is compatible with the expected prop type
// This is more lenient than full unification - it doesn't require all expected props to be present.
func (c *Checker) validateJSXAttributes(ctx Context, attrs []ast.JSXAttrElem, propsType type_system.Type) []Error {
	var errors []Error

	// Build a map of expected prop types for quick lookup
	expectedProps := make(map[string]type_system.Type)
	if propsType != nil {
		if objType, ok := type_system.Prune(propsType).(*type_system.ObjectType); ok {
			for _, elem := range objType.Elems {
				if prop, ok := elem.(*type_system.PropertyElem); ok {
					if prop.Name.Kind == type_system.StrObjTypeKeyKind {
						expectedProps[prop.Name.Str] = prop.Value
					}
				}
			}
		}
	}

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

			// If we have expected props, validate this attribute against them
			if propsType != nil {
				if expectedType, ok := expectedProps[attr.Name]; ok {
					// Attribute exists in expected props - check type compatibility
					unifyErrors := c.Unify(ctx, valueType, expectedType)
					errors = slices.Concat(errors, unifyErrors)
				}
				// Note: If the attribute is not in expectedProps, we allow it
				// Unknown attribute errors will be added in Phase 7 (error messages)
			}

		case *ast.JSXSpreadAttr:
			// Spread attribute: {...props}
			// Just infer the type, validation of spread contents is complex
			// and will be handled in Phase 3
			_, spreadErrors := c.inferExpr(ctx, attr.Expr)
			errors = slices.Concat(errors, spreadErrors)
		}
	}

	return errors
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

// getIntrinsicElementProps looks up the props type for an intrinsic HTML element
// from JSX.IntrinsicElements. If the JSX namespace is not available or the element
// is not found, returns nil to allow any props.
func (c *Checker) getIntrinsicElementProps(ctx Context, tagName ast.QualIdent, expr *ast.JSXElementExpr) (type_system.Type, []Error) {
	// Get the tag name as a string (handles both simple idents and member expressions)
	tagNameStr := ast.QualIdentToString(tagName)

	// Look up JSX namespace from scope
	jsxNamespace := ctx.Scope.getNamespace("JSX")
	if jsxNamespace == nil {
		// JSX/React types not loaded - allow any props (permissive fallback)
		return nil, nil
	}

	// Look up IntrinsicElements type alias in JSX namespace
	intrinsicElements, ok := jsxNamespace.Types["IntrinsicElements"]
	if !ok || intrinsicElements == nil {
		// IntrinsicElements not defined - allow any props
		return nil, nil
	}

	// Get the underlying type (resolve type aliases, type variables, etc.)
	intrinsicType := type_system.Prune(intrinsicElements.Type)

	// IntrinsicElements should be an object type mapping tag names to prop types
	switch t := intrinsicType.(type) {
	case *type_system.ObjectType:
		// Look for a property matching the tag name
		for _, elem := range t.Elems {
			if prop, ok := elem.(*type_system.PropertyElem); ok {
				if prop.Name.Kind == type_system.StrObjTypeKeyKind && prop.Name.Str == tagNameStr {
					return prop.Value, nil
				}
			}
		}
		// Tag not found in IntrinsicElements - this is an unknown HTML element
		// For now, allow any props; Phase 7 will add better error messages
		return nil, nil
	default:
		// IntrinsicElements is not an object type - allow any props
		return nil, nil
	}
}

// Phase 1 stub - returns nil to allow any props (replaced in Phase 3)
func (c *Checker) getComponentProps(ctx Context, tagName ast.QualIdent, expr *ast.JSXElementExpr) (type_system.Type, []Error) {
	return nil, nil
}

// Phase 1 stub - returns empty object type (replaced in Phase 4)
func (c *Checker) getJSXElementType(provenance *ast.NodeProvenance) type_system.Type {
	return type_system.NewObjectType(provenance, nil)
}
