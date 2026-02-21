package checker

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/tidwall/btree"
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

	// 2. Infer JSX attributes (separates key/ref from regular props)
	attrResult, attrErrors := c.inferJSXAttributes(ctx, expr.Opening.Attrs)
	errors = slices.Concat(errors, attrErrors)

	// 3. Validate special props (key and ref)
	if attrResult.KeyType != nil {
		keyErrors := c.validateKeyProp(ctx, attrResult.KeyType, *attrResult.KeySpan)
		errors = slices.Concat(errors, keyErrors)
	}
	if attrResult.RefType != nil {
		refErrors := c.validateRefProp(ctx, attrResult.RefType, *attrResult.RefSpan, isIntrinsic)
		errors = slices.Concat(errors, refErrors)
	}

	// 4. Unify each provided attribute with the corresponding expected prop type
	if propsType != nil {
		unifyErrors := c.unifyJSXPropsWithAttrs(ctx, propsType, attrResult.PropsType)
		errors = slices.Concat(errors, unifyErrors)
	}

	// 5. Type check children and get the combined children type
	childrenType, childErrors := c.inferJSXChildren(ctx, expr.Children)
	errors = slices.Concat(errors, childErrors)

	// 6. Validate children type against the component's children prop type
	childValidationErrors := c.validateChildrenType(ctx, childrenType, propsType, isIntrinsic, expr)
	errors = slices.Concat(errors, childValidationErrors)

	// 7. Return JSX.Element type
	return c.getJSXElementType(ctx, provenance), errors
}

// inferJSXFragment infers the type of a JSX fragment expression.
// Returns JSX.Element type and any type errors.
func (c *Checker) inferJSXFragment(ctx Context, expr *ast.JSXFragmentExpr) (type_system.Type, []Error) {
	var errors []Error
	provenance := &ast.NodeProvenance{Node: expr}

	// Fragments only have children, no props to validate
	// We still type-check the children but don't validate against any expected type
	_, childErrors := c.inferJSXChildren(ctx, expr.Children)
	errors = slices.Concat(errors, childErrors)

	// Return JSX.Element type
	return c.getJSXElementType(ctx, provenance), errors
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

// unifyJSXPropsWithAttrs unifies expected props with provided attributes.
// For each provided attribute, it finds the corresponding expected prop type and
// calls Unify to check type compatibility. This uses full unification per-property.
// It also checks that all required (non-optional) props are provided.
func (c *Checker) unifyJSXPropsWithAttrs(ctx Context, propsType type_system.Type, attrType type_system.Type) []Error {
	var errors []Error

	// Build maps of expected prop types and track which are required
	// Using btree for deterministic iteration order
	var expectedProps btree.Map[string, type_system.Type]
	var requiredProps btree.Set[string]
	propsObjType, ok := type_system.Prune(propsType).(*type_system.ObjectType)
	if ok {
		for _, elem := range propsObjType.Elems {
			if prop, ok := elem.(*type_system.PropertyElem); ok {
				if prop.Name.Kind == type_system.StrObjTypeKeyKind {
					expectedProps.Set(prop.Name.Str, prop.Value)
					if !prop.Optional {
						requiredProps.Insert(prop.Name.Str)
					}
				}
			}
		}
	}

	// Get the provided attributes as an object type
	attrObj, ok := type_system.Prune(attrType).(*type_system.ObjectType)
	if !ok {
		return errors
	}

	// Track which props were provided
	var providedProps btree.Set[string]

	// For each provided attribute, unify with the expected prop type
	for _, elem := range attrObj.Elems {
		if prop, ok := elem.(*type_system.PropertyElem); ok {
			if prop.Name.Kind == type_system.StrObjTypeKeyKind {
				attrName := prop.Name.Str
				attrValue := prop.Value
				providedProps.Insert(attrName)

				if expectedType, ok := expectedProps.Get(attrName); ok {
					// Attribute exists in expected props - use full unification
					unifyErrors := c.Unify(ctx, attrValue, expectedType)
					errors = slices.Concat(errors, unifyErrors)
				}
				// Note: If the attribute is not in expectedProps, we allow it for now
				// Unknown attribute errors will be added in Phase 7 (error messages)
			}
		}
	}

	// Check for missing required props (iterates in sorted order)
	// Note: 'children' is handled separately via JSX children, not attributes
	requiredProps.Scan(func(propName string) bool {
		if propName == "children" {
			// Skip 'children' - it's validated separately via validateChildrenType
			// which checks for missing required children
			return true
		}
		if !providedProps.Contains(propName) {
			errors = append(errors, &MissingRequiredPropError{
				PropName:   propName,
				ObjectType: propsObjType,
				span:       getSpanFromType(attrType),
			})
		}
		return true
	})

	return errors
}

// JSXAttributeResult holds the result of inferring JSX attributes.
// It separates special props (key, ref) from regular props.
type JSXAttributeResult struct {
	// Regular props (excludes key and ref)
	PropsType type_system.Type
	// Type of the key attribute, if provided
	KeyType type_system.Type
	// Type of the ref attribute, if provided
	RefType type_system.Type
	// Span of the key attribute for error reporting
	KeySpan *ast.Span
	// Span of the ref attribute for error reporting
	RefSpan *ast.Span
}

// inferJSXAttributes infers the types of JSX attributes and returns an object type
// representing all the provided attributes. This object type can then be unified
// with the expected props type for full type checking.
// Special props (key, ref) are separated and returned in JSXAttributeResult.
func (c *Checker) inferJSXAttributes(ctx Context, attrs []ast.JSXAttrElem) (JSXAttributeResult, []Error) {
	var errors []Error
	var elems []type_system.ObjTypeElem
	var result JSXAttributeResult

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

			// Handle special props: key and ref
			switch attr.Name {
			case "key":
				result.KeyType = valueType
				span := attr.Span()
				result.KeySpan = &span
			case "ref":
				result.RefType = valueType
				span := attr.Span()
				result.RefSpan = &span
			default:
				// Regular attribute - add as a property element
				prop := type_system.NewPropertyElem(type_system.NewStrKey(attr.Name), valueType)
				elems = append(elems, prop)
			}

		case *ast.JSXSpreadAttr:
			// Spread attribute: {...props}
			spreadType, spreadErrors := c.inferExpr(ctx, attr.Expr)
			errors = slices.Concat(errors, spreadErrors)

			// If the spread type is an object, merge its properties
			// Note: key and ref in spread objects are passed through to regular props
			// (this matches React's behavior - only explicit key/ref are special)
			if spreadType != nil {
				if objType, ok := type_system.Prune(spreadType).(*type_system.ObjectType); ok {
					elems = append(elems, objType.Elems...)
				}
				// For non-object spread types, we could add an error, but for now
				// we just ignore them (Phase 3 will handle this more thoroughly)
			}
		}
	}

	// Return an object type representing all the provided attributes (excluding key/ref)
	result.PropsType = type_system.NewObjectType(nil, elems)
	return result, errors
}

// inferJSXChildren type-checks all children of a JSX element and returns the combined children type.
func (c *Checker) inferJSXChildren(ctx Context, children []ast.JSXChild) (type_system.Type, []Error) {
	var errors []Error
	var childTypes []type_system.Type

	for _, child := range children {
		switch ch := child.(type) {
		case *ast.JSXText:
			// Text nodes are always valid and have string type
			text := normalizeJSXText(ch.Value)
			if text != "" {
				childTypes = append(childTypes, type_system.NewStrPrimType(nil))
			}
		case *ast.JSXExprContainer:
			childType, exprErrors := c.inferExpr(ctx, ch.Expr)
			errors = slices.Concat(errors, exprErrors)
			if childType != nil {
				childTypes = append(childTypes, childType)
			}
		case *ast.JSXElementExpr:
			childType, elemErrors := c.inferJSXElement(ctx, ch)
			errors = slices.Concat(errors, elemErrors)
			if childType != nil {
				childTypes = append(childTypes, childType)
			}
		case *ast.JSXFragmentExpr:
			childType, fragErrors := c.inferJSXFragment(ctx, ch)
			errors = slices.Concat(errors, fragErrors)
			if childType != nil {
				childTypes = append(childTypes, childType)
			}
		}
	}

	// Use computeChildrenType to determine the combined children type
	return c.computeChildrenType(ctx, childTypes), errors
}

// validateChildrenType validates the actual children type against the expected children prop type.
// For custom components, it reports an error if children are provided but no `children` prop exists.
// Intrinsic elements (HTML tags) always allow children.
func (c *Checker) validateChildrenType(
	ctx Context,
	childrenType type_system.Type,
	propsType type_system.Type,
	isIntrinsic bool,
	expr *ast.JSXElementExpr,
) []Error {
	if propsType == nil {
		// No props type - allow any children
		return nil
	}

	// Find the 'children' prop in the props type
	var childrenPropType type_system.Type
	var childrenPropOptional bool
	var childrenPropExists bool

	if objType, ok := type_system.Prune(propsType).(*type_system.ObjectType); ok {
		for _, elem := range objType.Elems {
			if prop, ok := elem.(*type_system.PropertyElem); ok {
				if prop.Name.Kind == type_system.StrObjTypeKeyKind && prop.Name.Str == "children" {
					childrenPropType = prop.Value
					childrenPropOptional = prop.Optional
					childrenPropExists = true
					break
				}
			}
		}
	}

	if childrenType == nil {
		// No children provided - check if children is required
		if childrenPropExists && !childrenPropOptional {
			// Children prop is required but not provided
			return []Error{
				&MissingRequiredPropError{
					PropName:   "children",
					ObjectType: propsType,
					span:       expr.Span(),
				},
			}
		}
		return nil
	}

	if !childrenPropExists {
		// Component doesn't specify children type
		if isIntrinsic {
			// Intrinsic elements (HTML tags) always allow children
			return nil
		}
		// Custom component doesn't have a children prop - report error
		return []Error{
			&UnexpectedChildrenError{
				ComponentName: ast.QualIdentToString(expr.Opening.Name),
				span:          expr.Span(),
			},
		}
	}

	// Unify actual children type with expected
	return c.Unify(ctx, childrenType, childrenPropType)
}

// validateKeyProp validates that the key prop has an acceptable type.
// Valid types: string, number, null (and their literal types)
func (c *Checker) validateKeyProp(ctx Context, keyType type_system.Type, span ast.Span) []Error {
	// Create the expected type: string | number | null
	expectedKeyType := type_system.NewUnionType(
		nil,
		type_system.NewStrPrimType(nil),
		type_system.NewNumPrimType(nil),
		type_system.NewNullType(nil),
	)

	// Unify the provided key type with the expected type
	unifyErrors := c.Unify(ctx, keyType, expectedKeyType)
	if len(unifyErrors) > 0 {
		// Replace with a more specific error message
		return []Error{&InvalidKeyPropError{
			ActualType: keyType,
			span:       span,
		}}
	}

	return nil
}

// validateRefProp validates the ref prop for a JSX element.
// For intrinsic elements, ref is allowed and typically refers to the DOM element.
// For components, ref is only valid if the component uses forwardRef (not fully implemented).
func (c *Checker) validateRefProp(ctx Context, refType type_system.Type, span ast.Span, isIntrinsic bool) []Error {
	// For now, we allow ref on intrinsic elements without strict type checking
	// Full ref type checking would require:
	// 1. For intrinsic elements: validate against RefObject<HTMLElement> | RefCallback<HTMLElement> | null
	// 2. For components: check if component uses forwardRef and validate accordingly
	//
	// This is a basic implementation that accepts refs without strict validation
	if isIntrinsic {
		// Allow refs on intrinsic elements - React handles these
		return nil
	}

	// For components, we currently allow refs but don't validate forwardRef usage
	// A more complete implementation would check if the component type includes forwardRef
	// For now, we just allow it (permissive behavior)
	return nil
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

// getComponentProps resolves a JSX component and extracts its props type.
// It handles:
// - Simple identifiers: <Button /> -> looks up "Button" in scope
// - Member expressions: <Icons.Star /> -> looks up "Icons" then gets "Star" property
// For function components, it extracts the first parameter's type as the props type.
func (c *Checker) getComponentProps(ctx Context, tagName ast.QualIdent, expr *ast.JSXElementExpr) (type_system.Type, []Error) {
	// Resolve the component type based on the tag name structure
	componentType, errors := c.resolveJSXComponentType(ctx, tagName)
	if componentType == nil {
		// Unknown component - return nil to allow any props (permissive fallback)
		// The error has already been added by resolveJSXComponentType
		return nil, errors
	}

	// Extract props type from the component type
	propsType := c.extractPropsFromComponentType(componentType)
	return propsType, errors
}

// resolveJSXComponentType resolves the type of a JSX component based on its tag name.
// For simple identifiers, it looks up the binding in scope.
// For member expressions, it recursively resolves the member chain.
func (c *Checker) resolveJSXComponentType(ctx Context, tagName ast.QualIdent) (type_system.Type, []Error) {
	switch name := tagName.(type) {
	case *ast.Ident:
		// Simple identifier: <Button />
		binding := ctx.Scope.GetValue(name.Name)
		if binding == nil {
			// Check if it's a namespace (less common but possible)
			if namespace := ctx.Scope.getNamespace(name.Name); namespace != nil {
				return type_system.NewNamespaceType(nil, namespace), nil
			}
			// Unknown component
			return nil, []Error{&UnknownComponentError{
				Name: name.Name,
				span: name.Span(),
			}}
		}
		return binding.Type, nil

	case *ast.Member:
		// Member expression: <Icons.Star /> or <Namespace.Sub.Component />
		// Resolve the left side first
		leftType, errors := c.resolveJSXComponentType(ctx, name.Left)
		if leftType == nil {
			return nil, errors
		}

		// Get the property from the resolved type
		key := PropertyKey{Name: name.Right.Name, OptChain: false, span: name.Right.Span()}
		propType, propErrors := c.getMemberType(ctx, leftType, key)
		errors = append(errors, propErrors...)

		if propType == nil {
			return nil, errors
		}
		return propType, errors

	default:
		panic("unexpected tag name type in resolveJSXComponentType")
	}
}

// extractPropsFromComponentType extracts the props type from a component type.
// For function components (fn(props: Props) -> JSX.Element), it returns the first parameter's type.
// For other component patterns (class components, etc.), it returns nil to allow any props.
func (c *Checker) extractPropsFromComponentType(componentType type_system.Type) type_system.Type {
	// Prune to get the underlying type (resolve type variables, etc.)
	prunedType := type_system.Prune(componentType)

	switch t := prunedType.(type) {
	case *type_system.FuncType:
		// Function component: fn(props: Props) -> JSX.Element
		if len(t.Params) > 0 {
			// The first parameter is the props
			return t.Params[0].Type
		}
		// No parameters - component doesn't accept props
		return type_system.NewObjectType(nil, nil)

	case *type_system.ObjectType:
		// Could be a class component or an object with a call signature
		// For now, allow any props - class component support can be added later
		return nil

	case *type_system.IntersectionType:
		// Could be an overloaded function component
		// TODO: The current behavior is a temporary shortcut that returns props from the
		// first inner type that yields a FuncType. This can miss other overloads.
		// Overloaded function components should be resolved by checking whether the given
		// JSX props match any overload, similar to the NoMatchingOverloadError logic for
		// call expressions in inferCallExpr. Future work should replace this simple
		// first-match return with full overload resolution that tries each overload and
		// reports NoMatchingOverloadError if none match.
		// NOTE: Overloaded functional components usually use a union of prop types
		// instead of an intersection of function types.
		for _, innerType := range t.Types {
			if propsType := c.extractPropsFromComponentType(innerType); propsType != nil {
				return propsType
			}
		}
		return nil

	default:
		// Unknown component type - allow any props
		return nil
	}
}

// getJSXElementType resolves the JSX.Element type from loaded React types.
// If React types are not available, returns a fallback empty object type.
func (c *Checker) getJSXElementType(ctx Context, provenance *ast.NodeProvenance) type_system.Type {
	// Try to resolve JSX.Element from React types
	// JSX namespace is in GlobalScope (from declare global), so check there first
	var jsxNamespace *type_system.Namespace
	var found bool

	// Check GlobalScope first (JSX is typically a global namespace)
	if c.GlobalScope != nil && c.GlobalScope.Namespace != nil {
		jsxNamespace, found = c.GlobalScope.Namespace.GetNamespace("JSX")
	}

	// Fall back to current scope chain if not in GlobalScope
	if !found || jsxNamespace == nil {
		jsxNamespace = ctx.Scope.getNamespace("JSX")
	}

	if jsxNamespace == nil {
		// Fallback: use a placeholder type when JSX types are not available
		return type_system.NewObjectType(provenance, nil)
	}

	// Look up Element in the namespace's Types map (which stores *TypeAlias values)
	if elementAlias, ok := jsxNamespace.Types["Element"]; ok {
		// Return the underlying type from the TypeAlias
		return elementAlias.Type
	}

	// Fallback: use a placeholder type if JSX.Element is not defined
	return type_system.NewObjectType(provenance, nil)
}

// getReactNodeType resolves the React.ReactNode type for children types.
// If React types are not available, returns nil to allow any type for children.
func (c *Checker) getReactNodeType(ctx Context) type_system.Type {
	// Try to resolve React.ReactNode for children types
	// React namespace may be in GlobalScope (if injected globally) or in current scope
	var reactNamespace *type_system.Namespace
	var found bool

	// Check GlobalScope first (React may be injected globally by LoadReactTypes)
	if c.GlobalScope != nil && c.GlobalScope.Namespace != nil {
		reactNamespace, found = c.GlobalScope.Namespace.GetNamespace("React")
	}

	// Fall back to current scope chain if not in GlobalScope
	if !found || reactNamespace == nil {
		reactNamespace = ctx.Scope.getNamespace("React")
	}

	if reactNamespace == nil {
		// Fallback: allow any type for children
		return nil
	}

	// Look up ReactNode in the namespace's Types map (which stores *TypeAlias values)
	if reactNodeAlias, ok := reactNamespace.Types["ReactNode"]; ok {
		// Return the underlying type from the TypeAlias
		return reactNodeAlias.Type
	}

	// Fallback: allow any type for children
	return nil
}

// computeChildrenType determines the type for JSX children.
// Accepts ctx to resolve React.ReactNode from loaded types.
func (c *Checker) computeChildrenType(ctx Context, childTypes []type_system.Type) type_system.Type {
	switch len(childTypes) {
	case 0:
		return nil // No children
	case 1:
		return childTypes[0] // Single child: use its type directly
	default:
		// Multiple children: create a tuple type containing all child types
		// This allows for more precise type checking than a generic array
		// In the future, we may want to use React.ReactNode for validation
		return type_system.NewTupleType(nil, childTypes...)
	}
}
