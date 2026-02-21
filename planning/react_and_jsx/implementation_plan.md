# Implementation Plan: JSX and React Support in Escalier

This document provides a detailed implementation plan for adding JSX and React support to the Escalier compiler. It is based on [requirements.md](requirements.md) and the current codebase structure.

---

## Overview

The JSX parser and AST types are already complete. Implementation requires:

1. **Type Checking** - Add inference logic in `internal/checker/infer_expr.go`
2. **Code Generation** - Add transform logic in `internal/codegen/builder.go`
3. **Type Definitions** - Use `@types/react` for JSX types (loaded automatically, no explicit import required)

### Implementation Progress

| Phase | Status | Description |
|-------|--------|-------------|
| Phase 1 | ✅ Complete | Core infrastructure - basic JSX compiles and type-checks |
| Phase 2 | ✅ Complete | Intrinsic element type validation |
| Phase 3 | ✅ Complete | Component prop type checking |
| Phase 4 | 🔄 In Progress (4.1 ✅) | React type definitions integration |
| Phase 5 | Not started | Code generation enhancements |
| Phase 6 | Not started | Automatic JSX transform |
| Phase 7 | Not started | Error messages and DX |
| Phase 8 | Not started | Final verification |

---

## Phase 1: Minimal Viable JSX (Core Infrastructure) ✅ COMPLETE

**Goal**: Get basic JSX elements compiling and type-checking with permissive stub types. This phase focuses on infrastructure; proper type validation comes in later phases.

**Status**: Completed. All basic JSX elements and fragments compile and type-check correctly.

### 1.1 Type Checker: Basic JSX Element Inference

**File**: `internal/checker/infer_expr.go` (lines 665-668)

Replace the panic with basic inference logic:

```go
case *ast.JSXElementExpr:
    resultType, errors = c.inferJSXElement(ctx, expr)
case *ast.JSXFragmentExpr:
    resultType, errors = c.inferJSXFragment(ctx, expr)
```

**New file**: `internal/checker/infer_jsx.go`

```go
package checker

import (
    "unicode"

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
    if isIntrinsic {
        propsType, errors = c.getIntrinsicElementProps(ctx, tagName, expr)
    } else {
        propsType, errors = c.getComponentProps(ctx, tagName, expr)
    }

    // 2. Build props object type from attributes
    attrType, attrErrors := c.inferJSXAttributes(ctx, expr.Opening.Attrs)
    errors = append(errors, attrErrors...)

    // 3. Unify attribute types with expected props
    if propsType != nil {
        unifyErrors := c.Unify(ctx, attrType, propsType)
        errors = append(errors, unifyErrors...)
    }

    // 4. Type check children
    childErrors := c.inferJSXChildren(ctx, expr.Children)
    errors = append(errors, childErrors...)

    // 5. Return JSX.Element type
    return c.getJSXElementType(provenance), errors
}

// isIntrinsicElement returns true if the tag name represents an HTML element.
// Intrinsic elements start with a lowercase letter.
func isIntrinsicElement(name string) bool {
    if len(name) == 0 {
        return false
    }
    return unicode.IsLower(rune(name[0]))
}
```

**Phase 1 Stub Implementations**:

In Phase 1, helper functions return permissive types to get the infrastructure working:

```go
// Phase 1 stub - returns nil to allow any props (replaced in Phase 2)
func (c *Checker) getIntrinsicElementProps(ctx Context, tagName string, expr ast.Expr) (type_system.Type, []Error) {
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
```

**Tasks**:
- [x] Create `internal/checker/infer_jsx.go`
- [x] Implement `inferJSXElement()` - main entry point
- [x] Implement `inferJSXFragment()` - similar but simpler
- [x] Implement `isIntrinsicElement()` - check lowercase first char
- [x] Implement `inferJSXAttributes()` - build object type from attrs
- [x] Implement `inferJSXChildren()` - type check each child
- [x] Implement stub versions of `getIntrinsicElementProps()`, `getComponentProps()`, `getJSXElementType()`

### 1.2 Code Generator: Basic JSX Transform

**File**: `internal/codegen/builder.go` (lines 1969-1972)

Replace the panic with transform logic:

```go
case *ast.JSXElementExpr:
    return b.buildJSXElement(expr, parent)
case *ast.JSXFragmentExpr:
    return b.buildJSXFragment(expr, parent)
```

**New file**: `internal/codegen/jsx.go`

```go
package codegen

import (
    "unicode"

    "github.com/escalier-lang/escalier/internal/ast"
)

// buildJSXElement transforms a JSX element into React.createElement call.
// <div className="foo">Hello</div>
// becomes:
// React.createElement("div", { className: "foo" }, "Hello")
func (b *Builder) buildJSXElement(expr *ast.JSXElementExpr, parent ast.Expr) (Expr, []Stmt) {
    var stmts []Stmt

    // 1. Build the element type (string for intrinsic, identifier for component)
    tagName := expr.Opening.Name
    var elementType Expr
    if isIntrinsicElement(tagName) {
        elementType = NewLitExpr(NewStrLit(tagName, expr), expr)
    } else {
        elementType = NewIdentExpr(tagName, "", expr)
    }

    // 2. Build props object (or null if no props)
    propsExpr, propsStmts := b.buildJSXProps(expr.Opening.Attrs, expr)
    stmts = append(stmts, propsStmts...)

    // 3. Build children
    childrenExprs, childrenStmts := b.buildJSXChildren(expr.Children)
    stmts = append(stmts, childrenStmts...)

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

func isIntrinsicElement(name string) bool {
    if len(name) == 0 {
        return false
    }
    return unicode.IsLower(rune(name[0]))
}
```

**Tasks**:
- [x] Create `internal/codegen/jsx.go`
- [x] Implement `buildJSXElement()` - main transform
- [x] Implement `buildJSXFragment()` - use React.Fragment
- [x] Implement `buildJSXProps()` - build props object from attrs
- [x] Implement `buildJSXChildren()` - transform children array
- [x] Handle spread attributes (using object spread syntax in generated output)
- [x] Handle boolean shorthand attributes (`<input disabled />` → `{disabled: true}`)

### 1.3 Tests for Phase 1

**Unit Tests**

**File**: `internal/checker/tests/jsx_test.go` ✅ Created

Contains test functions:
- `TestJSXElementBasic` - tests self-closing elements, elements with closing tags, props (string, expression, multiple, boolean shorthand, spread), children (text, expression), and nested elements
- `TestJSXFragmentBasic` - tests empty fragments, fragments with children, nested fragments
- `TestJSXComponent` - tests components without props, with props, and nested components

**File**: `internal/codegen/jsx_test.go` ✅ Created

Contains test functions:
- `TestJSXTransformBasic` - snapshot tests for element transforms (includes boolean shorthand)
- `TestJSXTransformFragment` - snapshot tests for fragment transforms
- `TestJSXTransformComponent` - snapshot tests for component transforms
- `TestJSXTransformSpread` - snapshot tests for spread attribute transforms

**Snapshot file**: `internal/codegen/__snapshots__/jsx_test.snap` ✅ Created

**File**: `internal/parser/jsx_test.go` ✅ Updated

Contains test functions:
- `TestParseJSXNoErrors` - tests for valid JSX parsing (includes spread attributes, boolean shorthand)
- `TestParseJSXErrors` - tests for JSX parse errors

**Snapshot file**: `internal/parser/__snapshots__/jsx_test.snap` ✅ Created

**Tasks**:
- [x] Create `internal/checker/tests/jsx_test.go` with basic inference tests
- [x] Create `internal/codegen/jsx_test.go` with transform verification
- [x] Add snapshot tests for generated JavaScript output
- [ ] Create `testdata/jsx/phase1/` integration test fixtures (deferred - unit tests sufficient for Phase 1)

---

## Phase 2: Intrinsic Element Types

**Goal**: Type-check HTML element attributes correctly.

### 2.1 JSX Namespace Types

Use the TypeScript types from `@types/react` for `JSX.IntrinsicElements`. This provides comprehensive, well-maintained prop types for all HTML elements including detailed event handler types, ARIA attributes, and more.

The `@types/react` package defines `JSX.IntrinsicElements` as an interface mapping tag names to their prop types. The compiler will **automatically load these types when JSX syntax is encountered**, without requiring an explicit `import` statement from the developer. See [Phase 4.1](#41-load-react-type-definitions) for the automatic type loading implementation details.

**File**: `internal/checker/infer_jsx.go` (add to existing file)

```go
func (c *Checker) getIntrinsicElementProps(ctx Context, tagName string, expr ast.Expr) (type_system.Type, []Error) {
    // Look up JSX.IntrinsicElements from loaded React types
    jsxNamespace := ctx.Scope.GetNamespace("JSX")
    if jsxNamespace == nil {
        // React types not loaded - allow any props
        return nil, nil
    }

    intrinsicElements := jsxNamespace.GetType("IntrinsicElements")
    if intrinsicElements == nil {
        return nil, nil
    }

    // Get the prop type for this specific tag from IntrinsicElements
    switch t := type_system.Prune(intrinsicElements).(type) {
    case *type_system.ObjectType:
        for _, elem := range t.Elems {
            if prop, ok := elem.(*type_system.PropertyElem); ok {
                if prop.Key.Name == tagName {
                    return prop.Type, nil
                }
            }
        }
    }

    // Unknown HTML element - could warn or allow
    return nil, nil
}
```

**Tasks**:
- [ ] Implement automatic loading of `@types/react` types when JSX syntax is encountered (no explicit import required) - deferred to Phase 4
- [x] Ensure `JSX` namespace is available in scope for JSX files (tests manually set up JSX namespace; automatic loading in Phase 4)
- [x] Implement `getIntrinsicElementProps()` to look up props from `JSX.IntrinsicElements`
- [x] Handle unknown HTML elements (allow with warning or error) - currently allows any props for unknown elements

### 2.2 Event Handler Types

Event handler types (`MouseEvent`, `KeyboardEvent`, `ChangeEvent`, etc.) are included in `@types/react` as part of the `React` namespace. These types are automatically available when using `JSX.IntrinsicElements` for prop validation.

**Tasks**:
- [x] Verify event handler types from `@types/react` are resolved correctly (tests verify onClick, onChange work with function types)
- [x] Ensure event handler props like `onClick`, `onChange`, `onSubmit` type-check properly

### 2.3 Tests for Phase 2

**Unit Tests** (add to `internal/checker/infer_jsx_test.go`)

```go
func TestIntrinsicElementValidProps(t *testing.T) {
    // Test: <div className="foo" id="bar" />
    // Expected: No errors
}

func TestIntrinsicElementInvalidProp(t *testing.T) {
    // Test: <div unknownProp="value" />
    // Expected: Error - unknown prop on div element
}

func TestIntrinsicElementWrongPropType(t *testing.T) {
    // Test: <div className={123} />
    // Expected: Error - className expects string, got number
}

func TestEventHandlerType(t *testing.T) {
    // Test: <button onClick={fn(e) { ... }} />
    // Expected: e should be typed as MouseEvent
}

func TestInputElementProps(t *testing.T) {
    // Test: <input type="text" value="hello" onChange={...} />
    // Expected: No errors, correct prop types
}
```

**Integration Test Fixtures**

```
testdata/jsx/phase2/
├── valid_intrinsic_props.esc       # <div className="x" id="y" />
├── event_handlers.esc              # <button onClick={...} onMouseOver={...} />
├── input_element.esc               # <input type="text" value={v} onChange={...} />
├── aria_attributes.esc             # <div aria-label="..." role="button" />
└── errors/
    ├── unknown_prop.esc            # Expected error: unknown prop
    └── wrong_prop_type.esc         # Expected error: type mismatch
```

**Tasks**:
- [x] Add intrinsic element prop validation tests (`TestIntrinsicElementValidProps`)
- [x] Add event handler type inference tests (`TestIntrinsicElementEventHandlers`)
- [ ] Create `testdata/jsx/phase2/` integration test fixtures (deferred - unit tests sufficient)
- [x] Add error case tests for invalid props (`TestIntrinsicElementInvalidPropType`)
- [x] Add missing required props tests (`TestIntrinsicElementMissingRequiredProp`, `TestIntrinsicElementWithAllRequiredProps`)

---

## Phase 3: Component Type Checking

**Goal**: Type-check custom component props.

### 3.1 Component Resolution

```go
func (c *Checker) getComponentProps(ctx Context, tagName string, expr *ast.JSXElementExpr) (type_system.Type, []Error) {
    // 1. Look up component in scope
    binding := ctx.Scope.GetValue(tagName)
    if binding == nil {
        return nil, []Error{&UnknownComponentError{Name: tagName, span: expr.Span()}}
    }

    // 2. Get the component's type
    componentType := binding.Type

    // 3. Extract props type from function signature
    switch t := type_system.Prune(componentType).(type) {
    case *type_system.FuncType:
        // Function component: (props: Props) -> JSX.Element
        if len(t.Params) > 0 {
            return t.Params[0].Type, nil
        }
    case *type_system.ObjectType:
        // Class component or object with call signature
        // Look for constructor or call signature
    }

    return nil, nil
}
```

**Tasks**:
- [x] Implement `getComponentProps()` - extract props from component type
- [x] Handle function components: `fn(props: Props) -> JSX.Element`
- [x] Handle member expressions: `<Namespace.Component />`
- [x] Report error for unknown components

### 3.2 Props Validation

```go
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
                    errors = append(errors, exprErrors...)
                }
            }

            key := type_system.NewStrKey(attr.Name)
            elems = append(elems, type_system.NewPropertyElem(key, valueType))

        case *ast.JSXSpreadAttr:
            // Spread attribute: {...props}
            spreadType, spreadErrors := c.inferExpr(ctx, attr.Expr)
            errors = append(errors, spreadErrors...)
            if spreadType != nil {
                elems = append(elems, type_system.NewRestSpreadElem(spreadType))
            }
        }
    }

    return type_system.NewObjectType(nil, elems), errors
}
```

**Tasks**:
- [x] Implement `inferJSXAttributes()` - build props object type
- [x] Handle string attribute values
- [x] Handle expression containers `{...}`
- [x] Handle boolean shorthand (presence = true)
- [x] Handle spread attributes `{...props}`

### 3.3 Children Type Checking

Children need to be type-checked in two ways:
1. Each child expression must be valid
2. The combined children type must match the component's `children` prop type (if specified)

```go
// inferJSXChildren infers types of all children and returns the combined children type.
func (c *Checker) inferJSXChildren(ctx Context, children []ast.JSXChild) (type_system.Type, []Error) {
    var errors []Error
    var childTypes []type_system.Type

    for _, child := range children {
        switch ch := child.(type) {
        case *ast.JSXText:
            // Text is always valid, type is string
            text := normalizeJSXText(ch.Value)
            if text != "" {
                childTypes = append(childTypes, type_system.StringType)
            }
        case *ast.JSXExprContainer:
            childType, exprErrors := c.inferExpr(ctx, ch.Expr)
            errors = append(errors, exprErrors...)
            childTypes = append(childTypes, childType)
        case *ast.JSXElementExpr:
            childType, elemErrors := c.inferJSXElement(ctx, ch)
            errors = append(errors, elemErrors...)
            childTypes = append(childTypes, childType)
        case *ast.JSXFragmentExpr:
            childType, fragErrors := c.inferJSXFragment(ctx, ch)
            errors = append(errors, fragErrors...)
            childTypes = append(childTypes, childType)
        }
    }

    // Compute combined children type
    return c.computeChildrenType(childTypes), errors
}

// computeChildrenType returns the appropriate type for children based on count.
func (c *Checker) computeChildrenType(childTypes []type_system.Type) type_system.Type {
    switch len(childTypes) {
    case 0:
        return nil // No children
    case 1:
        return childTypes[0] // Single child: use its type directly
    default:
        // Multiple children: array of ReactNode or union
        return type_system.NewArrayType(nil, c.getReactNodeType())
    }
}
```

**Validating children against component's `children` prop**:

```go
func (c *Checker) validateChildrenType(
    ctx Context,
    childrenType type_system.Type,
    propsType type_system.Type,
    expr *ast.JSXElementExpr,
) []Error {
    if childrenType == nil || propsType == nil {
        return nil
    }

    // Look for 'children' prop in the expected props type
    expectedChildrenType := c.getChildrenPropType(propsType)
    if expectedChildrenType == nil {
        // Component doesn't specify children type - allow any
        return nil
    }

    // Unify actual children type with expected
    return c.Unify(ctx, childrenType, expectedChildrenType)
}
```

**Tasks**:
- [x] Implement `inferJSXChildren()` - type check all children and return combined type
- [x] Handle text nodes (string type)
- [x] Handle expression containers
- [x] Handle nested JSX elements
- [x] Handle nested fragments
- [x] Implement `computeChildrenType()` - single child vs array
- [x] Validate children type against component's `children` prop type
- [ ] Support `React.ReactNode` as the general children type (deferred to Phase 4)

**Design Note: Children Type Checking**

**Components must declare a `children` prop to accept children:**
- If a custom component does not have a `children` prop, passing children to it is an error
- Intrinsic elements (HTML tags like `<div>`, `<span>`) always allow children
- This ensures explicit contracts for component children

**Required vs optional children:**
- If `children` is declared as required (e.g., `children: string`), children must be provided
- If `children` is declared as optional (e.g., `children?: string`), children are not required
- Missing required children produce a `MissingRequiredPropError`

**Multiple children produce a tuple type:**
When a JSX element has multiple children, `computeChildrenType()` returns a tuple type representing the exact types of each child. For example, `<Container>Hello{" "}World</Container>` produces a tuple type `[string, string, string]`.

This means:
- If `children: string`, only a **single** string child is allowed
- If a component wants to accept **multiple** children of type `T`, it should declare `children: Array<T>`
- The tuple type must be assignable to the declared children prop type

This behavior is intentional and provides precise type checking for children.

### 3.4 Special Props: `key` and `ref`

React treats `key` and `ref` as special props that are not passed to components. They require special handling in type checking.

**`key` prop**:
- Valid on any JSX element (intrinsic or component)
- Type: `string | number | null`
- Not included in the component's props type, so must be allowed separately
- Used by React for reconciliation, not passed to the component

**`ref` prop**:
- Valid on intrinsic elements and components wrapped with `forwardRef`
- Type varies: `RefObject<T>`, `RefCallback<T>`, or `null`
- For intrinsic elements, `T` is the DOM element type (e.g., `HTMLDivElement`)
- For components, only valid if the component uses `forwardRef`

```go
func (c *Checker) inferJSXAttributes(ctx Context, attrs []ast.JSXAttrElem) (type_system.Type, []Error) {
    var errors []Error
    var keyAttr, refAttr *ast.JSXAttr
    regularAttrs := make([]ast.JSXAttrElem, 0, len(attrs))

    // Separate special props from regular props
    for _, attrElem := range attrs {
        switch attr := attrElem.(type) {
        case *ast.JSXAttr:
            switch attr.Name {
            case "key":
                keyAttr = attr
            case "ref":
                refAttr = attr
            default:
                regularAttrs = append(regularAttrs, attr)
            }
        case *ast.JSXSpreadAttr:
            // Spread attrs go to regular attrs
            regularAttrs = append(regularAttrs, attr)
        }
    }

    // Type check key (must be string | number | null)
    if keyAttr != nil {
        keyType, keyErrors := c.inferJSXAttrValue(ctx, keyAttr.Value)
        errors = append(errors, keyErrors...)
        // Validate keyType is assignable to string | number | null
    }

    // Type check ref (complex - depends on element type)
    if refAttr != nil {
        // Handle ref type checking
    }

    // Build props type from regular attributes only
    return c.buildPropsType(ctx, regularAttrs)
}
```

**Tasks**:
- [x] Separate `key` and `ref` from regular props during attribute inference
- [x] Type check `key` prop: must be `string | number | null`
- [x] Type check `ref` prop for intrinsic elements (DOM element ref)
- [x] Handle `ref` prop for `forwardRef` components (basic support - allows refs)
- [x] Ensure `key` and `ref` are not passed through to component props

### 3.5 Default Props and Optional Props

Components may have optional props with default values. The type checker needs to handle these correctly.

**Optional props**:
- Props marked as optional in the component's type (e.g., `title?: string`)
- Do not need to be provided when using the component
- The component receives `undefined` if not provided

**Default props** (less common in modern React):
- Some components define `defaultProps` for fallback values
- TypeScript's `@types/react` handles this via type manipulation

```go
func (c *Checker) validateRequiredProps(
    ctx Context,
    providedProps type_system.Type,
    expectedProps type_system.Type,
    expr *ast.JSXElementExpr,
) []Error {
    var errors []Error

    // For each required prop in expectedProps, check if it's in providedProps
    switch expected := type_system.Prune(expectedProps).(type) {
    case *type_system.ObjectType:
        for _, elem := range expected.Elems {
            if prop, ok := elem.(*type_system.PropertyElem); ok {
                if !prop.Optional && !c.hasProp(providedProps, prop.Key.Name) {
                    errors = append(errors, &MissingPropError{
                        Component: expr.Opening.Name,
                        Prop:      prop.Key.Name,
                        span:      expr.Span(),
                    })
                }
            }
        }
    }

    return errors
}
```

**Tasks**:
- [x] Distinguish required vs optional props in component types
- [x] Only report missing prop errors for required props
- [ ] Handle `defaultProps` if encountered (lower priority, deferred)

### 3.6 Tests for Phase 3

**Unit Tests** (add to `internal/checker/infer_jsx_test.go`)

```go
func TestComponentValidProps(t *testing.T) {
    // Test: <MyComponent title="Hello" count={5} />
    // Expected: No errors when props match component type
}

func TestComponentMissingRequiredProp(t *testing.T) {
    // Test: <MyComponent /> where title is required
    // Expected: Error - missing required prop 'title'
}

func TestComponentOptionalProp(t *testing.T) {
    // Test: <MyComponent title="Hi" /> where count is optional
    // Expected: No errors
}

func TestComponentWrongPropType(t *testing.T) {
    // Test: <MyComponent title={123} /> where title expects string
    // Expected: Error - type mismatch
}

func TestComponentWithChildren(t *testing.T) {
    // Test: <Container><Child /></Container>
    // Expected: Children type matches component's children prop
}

func TestKeyPropAllowed(t *testing.T) {
    // Test: <MyComponent key="unique" title="Hi" />
    // Expected: No errors, key is allowed on any element
}

func TestRefPropOnIntrinsic(t *testing.T) {
    // Test: <div ref={myRef} />
    // Expected: No errors, ref typed as HTMLDivElement
}

func TestMemberExpressionComponent(t *testing.T) {
    // Test: <Icons.Star size={24} />
    // Expected: Props validated against Icons.Star type
}
```

**Integration Test Fixtures**

```
testdata/jsx/phase3/
├── function_component.esc          # fn Button(props: {label: string}) -> ...
├── component_with_children.esc     # Component that accepts children
├── optional_props.esc              # Component with optional props
├── key_and_ref.esc                 # Usage of key and ref props
├── member_expression.esc           # <Namespace.Component />
└── errors/
    ├── missing_required_prop.esc   # Expected error: missing prop
    ├── wrong_prop_type.esc         # Expected error: type mismatch
    ├── unknown_component.esc       # Expected error: component not defined
    └── invalid_children.esc        # Expected error: children type mismatch
```

**Tasks**:
- [x] Add component prop validation tests
- [x] Add tests for key and ref special props
- [x] Add optional vs required prop tests
- [x] Add children type validation tests
- [ ] Create `testdata/jsx/phase3/` integration test fixtures (deferred to future phase)

---

## Phase 4: React Type Definitions Integration

**Goal**: Use real React types from `@types/react`.

### 4.1 Resolve `@types/react` Package Location

Before loading type definitions, the compiler must locate the `@types/react` package on disk. This follows Node.js module resolution conventions.

**Resolution algorithm**:

1. Starting from the source file's directory, look for `node_modules/@types/react`
2. Walk up parent directories, checking each `node_modules/@types/react`
3. Stop when found or when reaching the filesystem root

```go
// ResolveTypesPackage finds the @types package for a given module name.
// Returns the path to the package directory, or empty string if not found.
// This is a standalone function (not a method) for simplicity.
func ResolveTypesPackage(moduleName string, fromDir string) (string, error) {
    typesPackage := "@types/" + moduleName

    dir := fromDir
    for {
        candidate := filepath.Join(dir, "node_modules", typesPackage)
        if info, err := os.Stat(candidate); err == nil && info.IsDir() {
            return candidate, nil
        }

        parent := filepath.Dir(dir)
        if parent == dir {
            // Reached filesystem root
            return "", fmt.Errorf("@types/%s not found", moduleName)
        }
        dir = parent
    }
}
```

**Finding the entry point `.d.ts` file**:

Once the package directory is found, determine which `.d.ts` file to load:

1. Read `package.json` from the package directory
2. Check for `types` or `typings` field → use that file
3. If not present, check for `main` field and replace extension with `.d.ts`
4. Fall back to `index.d.ts`

```go
// GetTypesEntryPoint returns the main .d.ts file for a types package.
// Returns an error if the resolved entry point file does not exist.
// This is a standalone function (not a method) for simplicity.
func GetTypesEntryPoint(packageDir string) (string, error) {
    pkgJsonPath := filepath.Join(packageDir, "package.json")

    data, err := os.ReadFile(pkgJsonPath)
    if err != nil {
        // No package.json, try index.d.ts
        indexPath := filepath.Join(packageDir, "index.d.ts")
        if _, statErr := os.Stat(indexPath); statErr != nil {
            return "", fmt.Errorf("types entry point not found: %s", indexPath)
        }
        return indexPath, nil
    }

    var pkg struct {
        Types    string                 `json:"types"`
        Typings  string                 `json:"typings"`
        Main     string                 `json:"main"`
        Exports  interface{}            `json:"exports"` // Can be string, object, or nested
    }
    if err := json.Unmarshal(data, &pkg); err != nil {
        return "", err
    }

    // Priority: exports["types"] > exports["."]["types"] > types > typings > main > index.d.ts

    // Check exports field for types (handles modern package.json exports)
    if pkg.Exports != nil {
        if typesPath := resolveExportsTypes(pkg.Exports); typesPath != "" {
            fullPath := filepath.Join(packageDir, typesPath)
            if _, statErr := os.Stat(fullPath); statErr != nil {
                return "", fmt.Errorf("types entry point from exports not found: %s", fullPath)
            }
            return fullPath, nil
        }
    }

    if pkg.Types != "" {
        fullPath := filepath.Join(packageDir, pkg.Types)
        if _, statErr := os.Stat(fullPath); statErr != nil {
            return "", fmt.Errorf("types entry point not found: %s (from 'types' field)", fullPath)
        }
        return fullPath, nil
    }
    if pkg.Typings != "" {
        fullPath := filepath.Join(packageDir, pkg.Typings)
        if _, statErr := os.Stat(fullPath); statErr != nil {
            return "", fmt.Errorf("types entry point not found: %s (from 'typings' field)", fullPath)
        }
        return fullPath, nil
    }
    if pkg.Main != "" {
        // Strip any JS-type extension (.js, .cjs, .mjs) before appending .d.ts
        mainWithoutExt := pkg.Main
        for _, ext := range []string{".mjs", ".cjs", ".js"} {
            if strings.HasSuffix(mainWithoutExt, ext) {
                mainWithoutExt = strings.TrimSuffix(mainWithoutExt, ext)
                break
            }
        }
        dtsMain := mainWithoutExt + ".d.ts"
        fullPath := filepath.Join(packageDir, dtsMain)
        if _, statErr := os.Stat(fullPath); statErr != nil {
            return "", fmt.Errorf("types entry point not found: %s (derived from 'main' field)", fullPath)
        }
        return fullPath, nil
    }

    // Fallback to index.d.ts
    indexPath := filepath.Join(packageDir, "index.d.ts")
    if _, statErr := os.Stat(indexPath); statErr != nil {
        return "", fmt.Errorf("types entry point not found: %s (fallback)", indexPath)
    }
    return indexPath, nil
}

// resolveExportsTypes extracts the types path from package.json exports field.
// Handles various shapes:
//   - exports: "./index.d.ts" (string - if ends in .d.ts)
//   - exports: { "types": "./index.d.ts" }
//   - exports: { ".": { "types": "./index.d.ts" } }
//   - exports: { ".": { "import": { "types": "./index.d.ts" }, "require": { "types": "./index.d.ts" } } }
func resolveExportsTypes(exports interface{}) string {
    switch e := exports.(type) {
    case string:
        // Direct string export - only use if it's a .d.ts file
        if strings.HasSuffix(e, ".d.ts") {
            return e
        }
        return ""
    case map[string]interface{}:
        // Check for direct "types" key
        if types, ok := e["types"].(string); ok {
            return types
        }
        // Check for "." entry (main entry point)
        if dot, ok := e["."]; ok {
            return resolveExportsTypes(dot)
        }
        // Check for nested condition maps (import/require/default)
        for _, key := range []string{"import", "require", "default"} {
            if nested, ok := e[key]; ok {
                if result := resolveExportsTypes(nested); result != "" {
                    return result
                }
            }
        }
    }
    return ""
}
```

**Tasks**:
- [x] Implement `ResolveTypesPackage()` to locate `@types/react` directory
- [x] Implement `GetTypesEntryPoint()` to find the main `.d.ts` file
- [x] Handle resolution from different starting directories (source file location)
- [ ] Cache resolved paths to avoid repeated filesystem lookups

**Tests** (file: `internal/resolver/types_resolver_test.go`):

| Test | Description | Status |
|------|-------------|--------|
| `TestResolveTypesPackage` | Find @types/react in node_modules | ✅ |
| `TestResolveTypesPackageWalkUp` | Find @types/react in parent directory's node_modules | ✅ |
| `TestResolveTypesPackageNotFound` | @types/react not installed returns error | ✅ |
| `TestGetTypesEntryPointFromTypes` | package.json has "types" field | ✅ |
| `TestGetTypesEntryPointFromTypings` | package.json has "typings" field (older convention) | ✅ |
| `TestGetTypesEntryPointFallback` | package.json has no types field, fallback to index.d.ts | ✅ |
| `TestGetTypesEntryPointFromExports` | package.json has exports field with types | ✅ |
| `TestGetTypesEntryPointFromExportsNested` | package.json has nested exports with import/require conditions | ✅ |
| `TestGetTypesEntryPointFromMain` | package.json has main field, derive .d.ts path | ✅ |
| `TestGetTypesEntryPointFileNotFound` | package.json points to non-existent file | ✅ |
| `TestGetTypesEntryPointNoPackageJson` | No package.json, fallback to index.d.ts | ✅ |
| `TestGetTypesEntryPointFromExportsDirectString` | exports is direct .d.ts string | ✅ |
| `TestGetTypesEntryPointTypesHasPriorityOverTypings` | types field takes priority over typings | ✅ |
| `TestResolveExportsTypes` | Table-driven tests for resolveExportsTypes helper | ✅ |
| `TestIntegrationWithRealTypesReact` | Integration test with actual @types/react package | ✅ |

### 4.2 Load and Infer TypeScript Definition Files

The existing infrastructure already handles parsing and inferring `.d.ts` files. We can reuse:

- **`loadClassifiedTypeScriptModule()`** in [prelude.go:202-274](internal/checker/prelude.go#L202-L274) - parses `.d.ts` files using `dts_parser`, classifies them into package/global/named modules using `ClassifyDTSFile()`, and converts to AST via `interop.ConvertModule()`
- **`loadPackageForImport()`** in [infer_import.go:108-231](internal/checker/infer_import.go#L108-L231) - infers modules using `c.InferModule()`, handles global augmentations, and registers in `PackageRegistry`

**Using existing infrastructure for `@types/react`**:

```go
// LoadReactTypes loads @types/react and injects types into scope.
// Reuses existing loadClassifiedTypeScriptModule infrastructure.
func (c *Checker) LoadReactTypes(ctx Context, sourceDir string) []Error {
    var errors []Error

    // 1. Resolve @types/react location (new function from 4.1)
    reactTypesDir, err := resolver.ResolveTypesPackage("react", sourceDir)
    if err != nil {
        // Emit warning, not error - fall back to permissive mode
        return []Error{&Warning{message: "Install @types/react for JSX type checking"}}
    }

    // 2. Find entry point (new function from 4.1)
    entryPoint, err := resolver.GetTypesEntryPoint(reactTypesDir)
    if err != nil {
        return []Error{&TypesLoadError{pkg: "@types/react", cause: err}}
    }

    // 3. Check if already loaded (use PackageRegistry for caching)
    if pkgNs, found := c.PackageRegistry.Lookup(entryPoint); found {
        // Already loaded - inject into current scope
        c.injectReactTypes(ctx, pkgNs)
        return nil
    }

    // 4. Load and classify using existing infrastructure
    loadResult, loadErr := loadClassifiedTypeScriptModule(entryPoint)
    if loadErr != nil {
        return []Error{&GenericError{
            message: "Could not load @types/react: " + loadErr.Error(),
        }}
    }

    // 5. Process global augmentations (JSX namespace lives here)
    if loadResult.GlobalModule != nil {
        globalCtx := Context{
            Scope:      c.GlobalScope,
            IsAsync:    false,
            IsPatMatch: false,
        }
        globalErrors := c.InferModule(globalCtx, loadResult.GlobalModule)
        errors = append(errors, globalErrors...)
    }

    // 6. Process package module (React namespace with FC, Component, etc.)
    var pkgNs *type_system.Namespace
    if loadResult.PackageModule != nil {
        pkgNs = type_system.NewNamespace()
        pkgScope := &Scope{
            Parent:    c.GlobalScope,
            Namespace: pkgNs,
        }
        pkgCtx := Context{
            Scope:      pkgScope,
            IsAsync:    false,
            IsPatMatch: false,
        }

        pkgErrors := c.InferModule(pkgCtx, loadResult.PackageModule)
        errors = append(errors, pkgErrors...)
    }

    // 6b. Process named modules (e.g., declare module "react" { ... })
    // Check if there's a "react" named module that should be used as the package namespace
    if reactModule, ok := loadResult.NamedModules["react"]; ok {
        if pkgNs == nil {
            pkgNs = type_system.NewNamespace()
        }
        namedScope := &Scope{
            Parent:    c.GlobalScope,
            Namespace: pkgNs,
        }
        namedCtx := Context{
            Scope:      namedScope,
            IsAsync:    false,
            IsPatMatch: false,
        }
        namedErrors := c.InferModule(namedCtx, reactModule)
        errors = append(errors, namedErrors...)
    }

    // 7. Always register in PackageRegistry for caching (even if partially populated)
    // This prevents re-parsing on subsequent calls
    if pkgNs == nil {
        pkgNs = type_system.NewNamespace() // Empty namespace for caching
    }
    c.PackageRegistry.Register(entryPoint, pkgNs)

    // 8. Inject types into current scope
    c.injectReactTypes(ctx, pkgNs)

    return errors
}

// injectReactTypes adds React types to the current scope.
func (c *Checker) injectReactTypes(ctx Context, pkgNs *type_system.Namespace) {
    // React namespace is available as a value (for React.createElement, etc.)
    if pkgNs != nil {
        ctx.Scope.Namespace.SetNamespace("React", pkgNs)
    }

    // JSX namespace should already be in GlobalScope from global augmentations
    // It's accessible via ctx.Scope since GlobalScope is the parent
}
```

**New error types** (add to `internal/checker/error.go`):

```go
// Warning represents a non-fatal diagnostic that doesn't block compilation.
type Warning struct {
    message string
    span    ast.Span
}

func (w *Warning) Error() string   { return w.message }
func (w *Warning) Message() string { return w.message }
func (w *Warning) Span() ast.Span  { return w.span }
func (w *Warning) IsWarning() bool { return true }

// TypesLoadError indicates a failure to load type definitions.
type TypesLoadError struct {
    pkg   string
    cause error
    span  ast.Span
}

func (e *TypesLoadError) Error() string {
    return fmt.Sprintf("failed to load types for %s: %v", e.pkg, e.cause)
}
func (e *TypesLoadError) Message() string { return e.Error() }
func (e *TypesLoadError) Span() ast.Span  { return e.span }
```

**Add Warnings field to Checker struct** (in `internal/checker/checker.go`):

```go
type Checker struct {
    // ... existing fields ...
    Warnings []*Warning // Accumulated warnings (non-fatal diagnostics)
}
```

**Helper to separate warnings from errors** (add to `internal/checker/checker.go`):

```go
// accumulateWarnings separates warnings from errors.
// Warnings are appended to c.Warnings; non-warning errors are returned.
func (c *Checker) accumulateWarnings(errs []Error) []Error {
    var nonWarnings []Error
    for _, err := range errs {
        if w, ok := err.(*Warning); ok {
            c.Warnings = append(c.Warnings, w)
        } else {
            nonWarnings = append(nonWarnings, err)
        }
    }
    return nonWarnings
}
```

**Key differences from `loadPackageForImport()`**:

1. Uses `ResolveTypesPackage()` instead of `resolveImport()` to find `@types/*` packages
2. Injects types directly into scope without requiring an import statement
3. The JSX namespace comes from `declare global` blocks in `@types/react`

**Handling `/// <reference types="..." />` directives**:

`@types/react` may include reference directives like `/// <reference types="scheduler" />`.
These need to be followed to load dependent type packages.

**Add `ReferenceTypes` field to `LoadedPackageResult`** (in `internal/checker/prelude.go`):

```go
// ReferenceDirective represents a /// <reference types="..." /> directive.
type ReferenceDirective struct {
    Path string // The referenced package name (e.g., "scheduler")
}

type LoadedPackageResult struct {
    // ... existing fields ...

    // ReferenceTypes contains parsed /// <reference types="..." /> directives.
    // These should be followed to load dependent type packages.
    ReferenceTypes []ReferenceDirective
}
```

The `dts_parser` should extract reference directives during parsing and populate
this field. The `loadClassifiedTypeScriptModule` function should pass them through.

**Processing reference directives in LoadReactTypes**:

```go
// In LoadReactTypes, after step 7 (register in PackageRegistry), process references.
// This must happen AFTER registration to prevent infinite loops on circular refs.

// 9. Process reference type directives (e.g., /// <reference types="scheduler" />)
for _, ref := range loadResult.ReferenceTypes {
    // Check if already loaded to prevent circular references
    // Use a composite key: we don't know the exact path yet, so check by package name
    refTypesDir, err := resolver.ResolveTypesPackage(ref.Path, sourceDir)
    if err != nil {
        // Reference not found - emit warning but continue
        c.Warnings = append(c.Warnings, &Warning{
            message: fmt.Sprintf("Referenced types package %q not found", ref.Path),
        })
        continue
    }

    refEntryPoint, err := resolver.GetTypesEntryPoint(refTypesDir)
    if err != nil {
        c.Warnings = append(c.Warnings, &Warning{
            message: fmt.Sprintf("Could not find entry point for %q: %v", ref.Path, err),
        })
        continue
    }

    // Check PackageRegistry to avoid re-loading (and circular refs)
    if _, found := c.PackageRegistry.Lookup(refEntryPoint); found {
        continue // Already loaded
    }

    // Recursively load the referenced types package
    // Note: This is a simplified approach - for full support, factor out
    // the loading logic into a shared helper that both LoadReactTypes and
    // this loop can use.
    refLoadResult, loadErr := loadClassifiedTypeScriptModule(refEntryPoint)
    if loadErr != nil {
        c.Warnings = append(c.Warnings, &Warning{
            message: fmt.Sprintf("Could not load referenced types %q: %v", ref.Path, loadErr),
        })
        continue
    }

    // Process global augmentations from the referenced package
    if refLoadResult.GlobalModule != nil {
        globalCtx := Context{
            Scope:      c.GlobalScope,
            IsAsync:    false,
            IsPatMatch: false,
        }
        globalErrors := c.InferModule(globalCtx, refLoadResult.GlobalModule)
        errors = append(errors, globalErrors...)
    }

    // Register the referenced package to prevent re-loading
    refNs := type_system.NewNamespace()
    if refLoadResult.PackageModule != nil {
        refScope := &Scope{Parent: c.GlobalScope, Namespace: refNs}
        refCtx := Context{Scope: refScope, IsAsync: false, IsPatMatch: false}
        refErrors := c.InferModule(refCtx, refLoadResult.PackageModule)
        errors = append(errors, refErrors...)
    }
    c.PackageRegistry.Register(refEntryPoint, refNs)
}
```

**Note on package location**: `ResolveTypesPackage` and `GetTypesEntryPoint` are
defined as standalone functions in `internal/resolver/types_resolver.go`. All call
sites use the `resolver.` package qualifier for consistency.

**Tasks**:
- [ ] Implement `LoadReactTypes()` using existing `loadClassifiedTypeScriptModule()`
- [ ] Implement `injectReactTypes()` to add React/JSX types to scope
- [ ] Handle `declare global` blocks (already supported by `loadClassifiedTypeScriptModule`)
- [ ] Handle `declare module "react"` named modules in addition to PackageModule
- [ ] Cache loaded modules in `PackageRegistry` to avoid re-parsing
- [ ] Add `ReferenceDirective` type and `ReferenceTypes` field to `LoadedPackageResult`
- [ ] Update `dts_parser` to extract `/// <reference types="..." />` directives
- [ ] Update `loadClassifiedTypeScriptModule()` to populate `ReferenceTypes` field
- [ ] Handle `/// <reference types="..." />` directives to load dependent types
- [ ] Guard against circular references via `PackageRegistry.Lookup()` checks
- [ ] Add `Warnings` field to Checker struct for non-fatal diagnostics

**Tests** (add to `internal/checker/tests/jsx_test.go`):

```go
func TestLoadReactTypesIntegration(t *testing.T) {
    // Test: LoadReactTypes() successfully loads @types/react
    // Expected: JSX namespace and React types available in scope
}

func TestLoadReactTypesWithReferenceDirectives(t *testing.T) {
    // Test: @types/react references other packages (e.g., scheduler)
    // Expected: Referenced packages are also loaded
}

func TestLoadReactTypesCaching(t *testing.T) {
    // Test: Calling LoadReactTypes() twice doesn't re-parse
    // Expected: Second call uses cached namespace from PackageRegistry
}
```

### 4.3 Automatic Loading for JSX Files

The compiler will **automatically load `@types/react` types** when JSX syntax is detected, without requiring an explicit import from the developer.

**Implementation approach**:
- During parsing or early checking, detect if the file contains JSX syntax
- If JSX is present, automatically load `@types/react` types into the module's scope
- The `JSX` namespace and `React` types become implicitly available
- This is similar to TypeScript's behavior where JSX types are resolved based on compiler configuration

**Detection of JSX usage**:

```go
// JSXDetector implements ast.Visitor to detect JSX syntax in AST nodes.
// Embeds DefaultVisitor to inherit no-op implementations of all Enter*/Exit* methods.
// Using the Visitor pattern ensures we catch JSX nested in any expression,
// including ternaries, closures, and method chains.
type JSXDetector struct {
    ast.DefaultVisitor
    Found bool
}

// EnterExpr is called for each expression node during traversal.
// Returns false to stop traversal when JSX is found, true to continue.
func (d *JSXDetector) EnterExpr(e ast.Expr) bool {
    if d.Found {
        return false // Stop traversal once JSX is found
    }
    switch e.(type) {
    case *ast.JSXElementExpr, *ast.JSXFragmentExpr:
        d.Found = true
        return false // No need to traverse children
    }
    return true // Continue traversing children
}

// hasJSXSyntax checks if an AST module contains any JSX expressions.
// Iterates over module.Namespaces and traverses each declaration using Accept.
func hasJSXSyntax(module *ast.Module) bool {
    detector := &JSXDetector{}

    // Iterate over all namespaces in the module
    module.Namespaces.Scan(func(name string, ns *ast.Namespace) bool {
        if detector.Found {
            return false // Stop scanning namespaces
        }
        // Traverse each declaration in the namespace
        for _, decl := range ns.Decls {
            decl.Accept(detector)
            if detector.Found {
                return false // Stop scanning namespaces
            }
        }
        return true // Continue scanning namespaces
    })

    return detector.Found
}
```

**Integration with checker**:

The actual integration point depends on the checker's structure. The checker likely processes
modules via `InferModule`, so JSX detection should happen early in that flow:

```go
// In InferModule or a similar entry point
func (c *Checker) InferModule(ctx Context, module *ast.Module) []Error {
    var errors []Error

    // Auto-load React types if JSX is present (check once per module)
    if hasJSXSyntax(module) {
        // Get source directory from the first file in the module.
        // ast.Module has Files []*File where each File has a Path field.
        var sourceDir string
        if len(module.Files) > 0 {
            sourceDir = filepath.Dir(module.Files[0].Path)
        } else {
            // Fallback: use current working directory if no files (shouldn't happen)
            sourceDir, _ = os.Getwd()
        }
        loadErrors := c.LoadReactTypes(ctx, sourceDir)
        errors = append(errors, c.accumulateWarnings(loadErrors)...)
    }

    // ... continue with normal inference ...
}
```

**Fallback behavior when `@types/react` is unavailable**:
- If `@types/react` is not installed, the compiler should emit a warning (not an error)
- Fall back to permissive typing: allow any props on intrinsic elements
- JSX expressions should still type-check and compile, just without prop validation
- The warning message should suggest: "Install @types/react for JSX type checking"

**Tasks**:
- [ ] Implement `hasJSXSyntax()` to detect JSX in AST
- [ ] Integrate JSX detection into `InferModule()` or appropriate entry point
- [ ] Call `LoadReactTypes()` automatically when JSX is detected
- [ ] Inject `JSX` namespace and `React` types into scope without explicit import
- [ ] Implement graceful fallback when `@types/react` is missing (warn + permissive typing)

**Tests** (add to `internal/checker/tests/jsx_test.go`):

```go
func TestAutoLoadReactTypesForJSX(t *testing.T) {
    // Test: File with JSX automatically loads @types/react
    // Expected: JSX namespace available without explicit import
}

func TestFallbackWithoutReactTypes(t *testing.T) {
    // Test: JSX without @types/react installed
    // Expected: Warning emitted, permissive typing allows compilation
}

func TestHasJSXSyntax(t *testing.T) {
    // Test: hasJSXSyntax() correctly detects JSX in various positions
    // - Top-level JSX element
    // - JSX in function body
    // - JSX in ternary expression
    // - JSX in nested closure
    // - File without JSX returns false
}
```

### 4.4 JSX.Element Type

Once React types are loaded, the checker can resolve proper JSX types.

```go
func (c *Checker) getJSXElementType(ctx Context, provenance *ast.NodeProvenance) type_system.Type {
    // Try to resolve JSX.Element from React types
    // JSX namespace is in GlobalScope (from declare global), so check there first
    // then fall back to checking the current scope chain
    var jsxNamespace *type_system.Namespace
    var found bool

    // Check GlobalScope first (JSX is typically a global namespace)
    if c.GlobalScope != nil && c.GlobalScope.Namespace != nil {
        jsxNamespace, found = c.GlobalScope.Namespace.GetNamespace("JSX")
    }

    // Fall back to current scope chain if not in GlobalScope
    if !found || jsxNamespace == nil {
        jsxNamespace, found = ctx.Scope.Namespace.GetNamespace("JSX")
    }

    if !found || jsxNamespace == nil {
        // Fallback: use a placeholder type
        return type_system.NewObjectType(provenance, nil)
    }

    // Look up Element in the namespace's Types map (which stores *TypeAlias values)
    if elementAlias, ok := jsxNamespace.Types["Element"]; ok {
        // Return the underlying type from the TypeAlias
        return elementAlias.Type
    }

    // Fallback: use a placeholder type
    return type_system.NewObjectType(provenance, nil)
}

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
        reactNamespace, found = ctx.Scope.Namespace.GetNamespace("React")
    }

    if !found || reactNamespace == nil {
        // Fallback: allow any type for children
        return type_system.UnknownType
    }

    // Look up ReactNode in the namespace's Types map (which stores *TypeAlias values)
    if reactNodeAlias, ok := reactNamespace.Types["ReactNode"]; ok {
        // Return the underlying type from the TypeAlias
        return reactNodeAlias.Type
    }

    // Fallback: allow any type for children
    return type_system.UnknownType
}

// computeChildrenType determines the type for JSX children.
// Accepts ctx to resolve React.ReactNode from loaded types.
func (c *Checker) computeChildrenType(ctx Context, childTypes []type_system.Type) type_system.Type {
    if len(childTypes) == 0 {
        return nil // No children
    }
    if len(childTypes) == 1 {
        return childTypes[0]
    }
    // Multiple children: wrap in array or use ReactNode union
    // For now, return a union of all child types
    // A more complete implementation would use React.ReactNode
    expectedChildType := c.getReactNodeType(ctx)
    if expectedChildType != type_system.UnknownType {
        // Validate each child against ReactNode
        // For now, just return the expected type
        return expectedChildType
    }
    // Fallback: return union of child types
    return type_system.NewUnionType(nil, childTypes)
}
```

**Call site updates required**:

Adding `ctx Context` parameters to `getJSXElementType`, `getReactNodeType`, and `computeChildrenType` requires updating their call sites:

1. `inferJSXElement()` already has `ctx` - update call: `c.getJSXElementType(ctx, provenance)`
2. `computeChildrenType(ctx, childTypes)` - now accepts `ctx Context` as first parameter
3. `inferJSXChildren()` needs to pass `ctx` to `computeChildrenType(ctx, childTypes)`
4. Any other callers of these functions need `ctx` threaded through

Example call site in `inferJSXChildren`:
```go
func (c *Checker) inferJSXChildren(ctx Context, children []ast.JSXChild) ([]type_system.Type, []Error) {
    var childTypes []type_system.Type
    var errors []Error
    // ... infer each child ...
    return childTypes, errors
}

// In inferJSXElement:
childTypes, childErrors := c.inferJSXChildren(ctx, element.Children)
errors = append(errors, childErrors...)
childrenType := c.computeChildrenType(ctx, childTypes) // Pass ctx here
```

**Tasks**:
- [ ] Implement `getJSXElementType()` - resolve JSX.Element from loaded types
- [ ] Implement `getReactNodeType()` - resolve React.ReactNode for children
- [ ] Update `computeChildrenType()` signature to accept `ctx Context`
- [ ] Thread `ctx` through `inferJSXChildren()` and `inferJSXElement()` call chains
- [ ] Handle case where React types aren't available (use fallback types)
- [ ] Map `React.FC`, `React.Component` types to Escalier type-system

**Tests** (add to `internal/checker/tests/jsx_test.go`):

```go
func TestJSXElementTypeResolution(t *testing.T) {
    // Test: <div /> returns JSX.Element type
    // Expected: Type is JSX.Element from @types/react
}

func TestReactFCComponent(t *testing.T) {
    // Test: Component typed as React.FC<Props>
    // Expected: Props correctly extracted and validated
}

func TestReactComponentClass(t *testing.T) {
    // Test: Class extending React.Component<Props, State>
    // Expected: Props correctly extracted and validated
}

func TestReactNodeChildren(t *testing.T) {
    // Test: Component with children: React.ReactNode
    // Expected: Accepts strings, numbers, elements, arrays
}
```

**Integration Test Fixtures**:

```
testdata/jsx/phase4/
├── jsx_element_type.esc            # Verify return type is JSX.Element
├── react_fc_component.esc          # React.FC<Props> component
├── react_node_children.esc         # Children typed as ReactNode
├── auto_load_types.esc             # JSX without explicit import (types auto-loaded)
└── no_react_types/
    └── permissive_fallback.esc     # Should compile with warning
```

---

## Phase 5: Code Generation Enhancements

**Goal**: Support all JSX features and automatic transform.

### 5.1 Complete Classic Transform

**Tasks**:
- [ ] Handle all attribute types (string, expression, spread)
- [ ] Handle all child types (text, expression, element, fragment)
- [ ] Handle self-closing vs open/close tags
- [ ] Handle member expression tags: `<Ctx.Provider />`
- [ ] Generate proper `null` for missing props/children

### 5.2 Props Object Generation

```go
func (b *Builder) buildJSXProps(attrs []ast.JSXAttrElem, source *ast.JSXElementExpr) (Expr, []Stmt) {
    if len(attrs) == 0 {
        return NewLitExpr(NewNullLit(source), source), nil
    }

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

    return NewObjectExpr(props, source), stmts
}
```

**Tasks**:
- [ ] Implement `buildJSXProps()` - handle all attribute forms
- [ ] Implement `buildJSXAttrValue()` - handle string and expression values
- [ ] Handle spread attributes with `Object.assign()`
- [ ] Handle boolean shorthand: `disabled` -> `disabled: true`

### 5.3 Children Transformation

```go
func (b *Builder) buildJSXChildren(children []ast.JSXChild) ([]Expr, []Stmt) {
    var exprs []Expr
    var stmts []Stmt

    for _, child := range children {
        switch ch := child.(type) {
        case *ast.JSXText:
            // Normalize whitespace and skip empty text
            text := normalizeJSXText(ch.Value)
            if text != "" {
                exprs = append(exprs, NewLitExpr(NewStrLit(text, ch), ch))
            }
        case *ast.JSXExprContainer:
            expr, exprStmts := b.buildExpr(ch.Expr, ch)
            exprs = append(exprs, expr)
            stmts = append(stmts, exprStmts...)
        case *ast.JSXElementExpr:
            expr, exprStmts := b.buildJSXElement(ch, nil)
            exprs = append(exprs, expr)
            stmts = append(stmts, exprStmts...)
        case *ast.JSXFragmentExpr:
            expr, exprStmts := b.buildJSXFragment(ch, nil)
            exprs = append(exprs, expr)
            stmts = append(stmts, exprStmts...)
        }
    }

    return exprs, stmts
}

func normalizeJSXText(text string) string {
    // Remove leading/trailing whitespace from lines
    // Collapse multiple whitespace into single space
    // This matches React's JSX text handling
}
```

**Tasks**:
- [ ] Implement `buildJSXChildren()` - transform all child types
- [ ] Implement `normalizeJSXText()` - match React's whitespace handling
- [ ] Skip empty/whitespace-only text nodes

### 5.4 Fragment Transformation

```go
func (b *Builder) buildJSXFragment(expr *ast.JSXFragmentExpr, parent ast.Expr) (Expr, []Stmt) {
    var stmts []Stmt

    // Build children
    childrenExprs, childrenStmts := b.buildJSXChildren(expr.Children)
    stmts = append(stmts, childrenStmts...)

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
```

**Tasks**:
- [ ] Implement `buildJSXFragment()` - use React.Fragment
- [ ] Handle empty fragments

### 5.5 Runtime React Import (Classic Transform)

**Important**: The classic transform generates `React.createElement(...)` calls, which requires `React` to be in scope at runtime. Unlike type definitions (which are loaded automatically), the runtime import must be handled explicitly.

**Options**:
1. **Require explicit import** (recommended for classic transform): Users must write `import React from "react"` in their source files. The compiler should emit a helpful error if `React` is not in scope when using JSX with classic transform.

2. **Auto-inject import**: The compiler could automatically add `import React from "react"` to the generated JavaScript output. However, this may surprise users and create issues with module bundlers.

**Recommendation**: For classic transform, require explicit import and provide a clear error message. The automatic transform (Phase 6) handles imports automatically via `react/jsx-runtime`.

**Tasks**:
- [ ] Detect when `React` is not in scope during JSX codegen (classic transform)
- [ ] Emit helpful error: "JSX requires React to be in scope. Add: import React from 'react'"
- [ ] Consider compiler flag to auto-inject React import (opt-in)

### 5.6 Tests for Phase 5

**Unit Tests** (add to `internal/codegen/jsx_test.go`)

```go
func TestJSXTransformSpreadProps(t *testing.T) {
    // Input: <Button {...props} />
    // Output: React.createElement(Button, props) or Object.assign({}, props)
}

func TestJSXTransformMixedSpreadProps(t *testing.T) {
    // Input: <Button {...props} extra="value" />
    // Output: React.createElement(Button, Object.assign({}, props, { extra: "value" }))
}

func TestJSXTransformBooleanShorthand(t *testing.T) {
    // Input: <input disabled />
    // Output: React.createElement("input", { disabled: true })
}

func TestJSXTransformMemberExpression(t *testing.T) {
    // Input: <Context.Provider value={...} />
    // Output: React.createElement(Context.Provider, { value: ... })
}

func TestJSXTransformWhitespace(t *testing.T) {
    // Input: <div>  Hello   World  </div>
    // Output: Properly normalized whitespace
}

func TestJSXTransformMissingReactError(t *testing.T) {
    // Input: JSX without React import (classic transform)
    // Expected: Helpful error message
}
```

**Snapshot Tests** (verify exact output format)

```go
func TestJSXCodegenSnapshots(t *testing.T) {
    // Compare generated JavaScript against expected snapshots
    // Covers all prop types, children variations, fragments
}
```

**Integration Test Fixtures**

```
testdata/jsx/phase5/
├── spread_props.esc                # <Button {...props} />
├── mixed_spread_props.esc          # <Button {...props} extra="x" />
├── boolean_shorthand.esc           # <input disabled />
├── member_expression.esc           # <Ctx.Provider />
├── whitespace_handling.esc         # Various whitespace cases
├── complex_nesting.esc             # Deeply nested JSX
└── errors/
    └── missing_react_import.esc    # Expected error: React not in scope
```

**Tasks**:
- [ ] Add codegen tests for spread props
- [ ] Add codegen tests for boolean shorthand
- [ ] Add whitespace normalization tests
- [ ] Add snapshot tests for generated JavaScript
- [ ] Add test for missing React import error
- [ ] Create `testdata/jsx/phase5/` integration test fixtures

---

## Phase 6: Automatic JSX Transform (React 17+)

**Goal**: Support the new JSX transform that doesn't require React import.

### 6.1 Configuration

Add compiler option for JSX mode:

```go
type JSXMode string

const (
    JSXModeClassic   JSXMode = "react"     // React.createElement
    JSXModeAutomatic JSXMode = "react-jsx" // jsx-runtime
    JSXModePreserve  JSXMode = "preserve"  // No transform
)

type CompilerOptions struct {
    JSX               JSXMode
    JSXFactory        string // default: "React.createElement"
    JSXFragmentFactory string // default: "React.Fragment"
    JSXImportSource   string // default: "react"
}
```

### 6.2 Automatic Transform Generation

```go
func (b *Builder) buildJSXElementAutomatic(expr *ast.JSXElementExpr) (Expr, []Stmt) {
    // Use jsx() or jsxs() from react/jsx-runtime
    // jsxs() is used when there are multiple children

    hasMultipleChildren := len(expr.Children) > 1

    var jsxFunc string
    if hasMultipleChildren {
        jsxFunc = "_jsxs"
    } else {
        jsxFunc = "_jsx"
    }

    // Build props with children included
    propsExpr := b.buildJSXPropsWithChildren(expr)

    return NewCallExpr(
        NewIdentExpr(jsxFunc, "", expr),
        []Expr{elementType, propsExpr},
        false,
        expr,
    ), nil
}
```

**Tasks**:
- [ ] Add JSX mode configuration
- [ ] Implement automatic transform with `jsx()`/`jsxs()`
- [ ] Auto-inject `react/jsx-runtime` import
- [ ] Handle children as props in automatic mode

### 6.3 Tests for Phase 6

**Unit Tests** (add to `internal/codegen/jsx_test.go`)

```go
func TestJSXAutomaticTransformSingleChild(t *testing.T) {
    // Input: <div>Hello</div>
    // Output: _jsx("div", { children: "Hello" })
}

func TestJSXAutomaticTransformMultipleChildren(t *testing.T) {
    // Input: <div><span /><span /></div>
    // Output: _jsxs("div", { children: [_jsx("span", {}), _jsx("span", {})] })
}

func TestJSXAutomaticTransformFragment(t *testing.T) {
    // Input: <><div /><span /></>
    // Output: _jsxs(Fragment, { children: [...] })
}

func TestJSXAutomaticImportInjection(t *testing.T) {
    // Verify import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime"
    // is added to generated output
}

func TestJSXModeConfiguration(t *testing.T) {
    // Test switching between classic and automatic modes
    // Same input should produce different output based on config
}
```

**Integration Test Fixtures**

```
testdata/jsx/phase6/
├── automatic_single_child.esc      # Single child -> jsx()
├── automatic_multiple_children.esc # Multiple children -> jsxs()
├── automatic_fragment.esc          # Fragment with automatic transform
├── automatic_import.esc            # Verify import injection
└── mode_comparison/
    ├── classic_output.esc          # Same JSX, classic transform
    └── automatic_output.esc        # Same JSX, automatic transform
```

**Tasks**:
- [ ] Add tests for `jsx()` single-child transform
- [ ] Add tests for `jsxs()` multiple-children transform
- [ ] Add tests for automatic import injection
- [ ] Add tests for compiler mode configuration
- [ ] Create `testdata/jsx/phase6/` integration test fixtures

---

## Phase 7: Error Messages and Developer Experience

**Goal**: Provide helpful error messages for JSX issues.

### 7.1 Custom JSX Errors

**New file**: `internal/checker/jsx_errors.go`

```go
package checker

type UnknownComponentError struct {
    Name string
    span ast.Span
}

func (e *UnknownComponentError) Error() string {
    return fmt.Sprintf("Component '%s' is not defined", e.Name)
}

type MissingPropError struct {
    Component string
    Prop      string
    span      ast.Span
}

func (e *MissingPropError) Error() string {
    return fmt.Sprintf("Property '%s' is missing in props for component '%s'", e.Prop, e.Component)
}

type InvalidPropTypeError struct {
    Component string
    Prop      string
    Expected  type_system.Type
    Actual    type_system.Type
    span      ast.Span
}

type UnknownPropError struct {
    Element string
    Prop    string
    span    ast.Span
}

type ChildrenNotAllowedError struct {
    Component string
    span      ast.Span
}
```

**Tasks**:
- [ ] Create `internal/checker/jsx_errors.go`
- [ ] Define specific error types for JSX issues
- [ ] Include suggestions in error messages
- [ ] Add "Did you mean...?" for typos

### 7.2 Tests for Phase 7

**Unit Tests** (new file: `internal/checker/jsx_errors_test.go`)

```go
func TestUnknownComponentErrorMessage(t *testing.T) {
    // Test: <UnknownThing />
    // Expected: "Component 'UnknownThing' is not defined"
}

func TestMissingPropErrorMessage(t *testing.T) {
    // Test: <Button /> where 'label' is required
    // Expected: "Property 'label' is missing in props for component 'Button'"
}

func TestInvalidPropTypeErrorMessage(t *testing.T) {
    // Test: <Button label={123} /> where label expects string
    // Expected: Clear message showing expected vs actual type
}

func TestUnknownPropErrorWithSuggestion(t *testing.T) {
    // Test: <div classname="foo" />  (typo: should be className)
    // Expected: "Unknown prop 'classname' on div. Did you mean 'className'?"
}

func TestComponentTypoSuggestion(t *testing.T) {
    // Test: <Buton /> when Button is defined
    // Expected: "Component 'Buton' is not defined. Did you mean 'Button'?"
}
```

**Error Message Snapshot Tests**

```go
func TestJSXErrorMessageSnapshots(t *testing.T) {
    // Verify exact error message format for all error types
    // Ensures consistent, helpful error messages
}
```

**Integration Test Fixtures**

```
testdata/jsx/phase7/errors/
├── unknown_component_suggestion.esc    # Typo with similar component name
├── unknown_prop_suggestion.esc         # Typo with similar prop name
├── missing_required_prop.esc           # Clear error for missing prop
├── type_mismatch_detailed.esc          # Shows expected vs actual types
└── multiple_errors.esc                 # Multiple JSX errors in one file
```

**Tasks**:
- [ ] Add error message unit tests
- [ ] Add "Did you mean?" suggestion tests
- [ ] Add error message snapshot tests
- [ ] Create `testdata/jsx/phase7/` error test fixtures

---

## Phase 8: Final Verification and Real-World Testing

**Goal**: Verify the complete JSX implementation works end-to-end with real React applications.

### 8.1 End-to-End Integration Tests

Test complete workflows from source to compiled output:

```
testdata/jsx/e2e/
├── simple_app/
│   ├── App.esc                 # Main app component
│   ├── components/
│   │   ├── Button.esc          # Reusable button component
│   │   └── Card.esc            # Card component with children
│   └── expected_output/        # Expected compiled JavaScript
├── todo_app/
│   ├── TodoList.esc            # List with map and key props
│   ├── TodoItem.esc            # Individual item component
│   └── expected_output/
└── form_app/
    ├── Form.esc                # Form with controlled inputs
    ├── Input.esc               # Input component with onChange
    └── expected_output/
```

### 8.2 Cross-Phase Regression Tests

Ensure changes in later phases don't break earlier functionality:

```go
func TestAllPhasesRegression(t *testing.T) {
    // Run all phase-specific test fixtures
    // Verify Phase 1 tests still pass after Phase 7 changes
}
```

### 8.3 Performance Tests

```go
func BenchmarkJSXTypeChecking(b *testing.B) {
    // Benchmark type checking performance on large JSX files
}

func BenchmarkJSXCodegen(b *testing.B) {
    // Benchmark code generation performance
}
```

### 8.4 Test Summary

| Test Category | File | Coverage |
|--------------|------|----------|
| JSX Type Inference | `infer_jsx_test.go` | Element, fragment, props, children, components |
| JSX Code Generation | `jsx_test.go` | Classic + automatic transforms, all prop types |
| JSX Errors | `jsx_errors_test.go` | All error types with suggestions |
| Integration | `testdata/jsx/` | Phase-specific + e2e fixtures |

**Tasks**:
- [ ] Create end-to-end test applications
- [ ] Add cross-phase regression test suite
- [ ] Add performance benchmarks
- [ ] Verify all phase-specific tests pass together
- [ ] Test with real React patterns (hooks, context, etc.)

---

## File Changes Summary

### New Files

| File | Purpose | Status |
|------|---------|--------|
| `internal/checker/infer_jsx.go` | JSX type inference logic (including intrinsic element lookup from `@types/react`) | ✅ Created |
| `internal/checker/jsx_errors.go` | JSX-specific error types | Planned (Phase 7) |
| `internal/codegen/jsx.go` | JSX code generation | ✅ Created |
| `internal/checker/tests/jsx_test.go` | Type checker unit tests | ✅ Created |
| `internal/checker/jsx_errors_test.go` | Error message tests | Planned (Phase 7) |
| `internal/codegen/jsx_test.go` | Code generator unit tests | ✅ Created |
| `internal/codegen/__snapshots__/jsx_test.snap` | Codegen snapshot test expectations | ✅ Created |
| `internal/parser/__snapshots__/jsx_test.snap` | Parser snapshot test expectations | ✅ Created |
| `internal/resolver/types_resolver.go` | Resolution of `@types/*` packages from node_modules | ✅ Created |
| `internal/resolver/types_resolver_test.go` | Tests for types package resolution | ✅ Created |
| `internal/checker/react_types.go` | `LoadReactTypes()` and `injectReactTypes()` for auto-loading React types | Planned (Phase 4) |

### New Test Fixtures

```
testdata/jsx/
├── phase1/                     # Basic JSX compilation tests
├── phase2/                     # Intrinsic element prop tests
│   └── errors/
├── phase3/                     # Component prop tests
│   └── errors/
├── phase4/                     # React type integration tests
│   └── no_react_types/
├── phase5/                     # Codegen edge cases
│   └── errors/
├── phase6/                     # Automatic transform tests
│   └── mode_comparison/
├── phase7/                     # Error message tests
│   └── errors/
└── e2e/                        # End-to-end app tests
    ├── simple_app/
    ├── todo_app/
    └── form_app/
```

### Modified Files

| File | Changes | Status |
|------|---------|--------|
| `internal/checker/infer_expr.go` | Add cases for JSXElementExpr, JSXFragmentExpr | ✅ Done |
| `internal/codegen/builder.go` | Add cases for JSXElementExpr, JSXFragmentExpr | ✅ Done |
| `internal/parser/jsx.go` | Updated to parse fragments as `JSXFragmentExpr`, added spread attributes and boolean shorthand parsing | ✅ Done |
| `internal/parser/expr.go` | Call `jsxElementOrFragment()` instead of `jsxElement()` | ✅ Done |
| `internal/ast/jsx.go` | Added `JSXAttrElem` interface, `JSXSpreadAttr` type, changed `JSXOpening.Attrs` to `[]JSXAttrElem` | ✅ Done |

---

## Implementation Order

```
Phase 1 (Foundation)
├── 1.1 Create infer_jsx.go with stub functions
├── 1.2 Create jsx.go with basic transform
├── 1.3 Tests: basic inference + transform tests
│
Phase 4 (React Integration) - implement early, needed by Phase 2
├── 4.1 Auto-load @types/react for JSX files
├── 4.2 Use real JSX.Element type
├── 4.3 Tests: type loading + fallback tests
│
Phase 2 (Intrinsic Elements)
├── 2.1 Look up intrinsic props from @types/react
├── 2.2 Verify event handler types
├── 2.3 Tests: intrinsic prop validation tests
│
Phase 3 (Components)
├── 3.1 Component type resolution
├── 3.2 Props validation
├── 3.3 Children type checking
├── 3.4 Special props (key, ref)
├── 3.5 Default/optional props
├── 3.6 Tests: component prop tests + error cases
│
Phase 5 (Codegen Polish)
├── 5.1 Complete classic transform
├── 5.2 Props object generation
├── 5.3 Children transformation
├── 5.4 Fragment transformation
├── 5.5 Runtime React import handling
├── 5.6 Tests: codegen snapshots + edge cases
│
Phase 6 (Automatic Transform)
├── 6.1 Add configuration
├── 6.2 Implement automatic transform
├── 6.3 Tests: jsx()/jsxs() transform tests
│
Phase 7 (Developer Experience)
├── 7.1 Improved error messages
├── 7.2 Tests: error message snapshots
│
Phase 8 (Final Verification)
├── 8.1 End-to-end integration tests
├── 8.2 Cross-phase regression tests
├── 8.3 Performance benchmarks
```

**Note**: Phase 4 should be implemented before Phase 2, since Phase 2 depends on loading `@types/react` types. Each phase includes its own tests that must pass before moving to the next phase.

---

## Success Criteria

### Phase 1 Complete When: ✅
- [x] `<div />` compiles without panic
- [x] Output is `React.createElement("div", null)`
- [x] Basic tests pass

### Phase 2 Complete When: ✅
- [x] Intrinsic element props are validated against `JSX.IntrinsicElements` (when available in scope)
- [x] Invalid prop types on HTML elements produce errors (e.g., `<div className={123} />`)
- [x] Event handler types are correctly inferred (e.g., `onClick` accepts function type)
- [x] Unknown elements and missing JSX namespace fall back to permissive mode (allow any props)
- [x] Missing required props on intrinsic elements produce errors (e.g., `<img />` missing required `src` and `alt`)

### Phase 3 Complete When:
- [ ] Custom components type-check props
- [ ] Missing required props on custom components produce errors
- [ ] Wrong prop types on custom components produce errors
- [ ] `key` and `ref` props are handled correctly
- [ ] Optional props don't require values

### Phase 4 Complete When:
- [x] `@types/react` package can be resolved from `node_modules/@types/react`
- [x] `.d.ts` entry point file is correctly identified from `package.json`
- [ ] `.d.ts` files load via existing `loadClassifiedTypeScriptModule()` infrastructure
- [ ] `@types/react` loads automatically for JSX files (no explicit import needed)
- [ ] `JSX.Element` type is resolved from React types
- [ ] `React.FC` and `React.Component` types work
- [ ] Graceful fallback when `@types/react` is missing (warning + permissive mode)

### Phase 5 Complete When:
- [ ] All JSX syntax forms compile correctly
- [ ] Output matches React's expected format
- [x] Spread attributes work
- [x] Boolean shorthand attributes work
- [ ] Clear error when `React` not in scope (classic transform)

### Phase 6 Complete When:
- [ ] Automatic transform generates `jsx()`/`jsxs()` calls
- [ ] `react/jsx-runtime` import is auto-injected
- [ ] Children are passed as props (not varargs)
- [ ] Compiler option switches between classic and automatic

### Phase 7 Complete When:
- [ ] Error messages include component/element names
- [ ] "Did you mean?" suggestions for typos
- [ ] Clear distinction between missing vs wrong-type props

### Full Implementation Complete When:
- [ ] All tests in `testdata/jsx/` pass
- [ ] Error messages are clear and helpful
- [ ] Real React applications can be compiled

---

## Dependencies

- Existing JSX parser (`internal/parser/jsx.go`) - **Complete** (updated to properly distinguish elements from fragments)
- Existing JSX AST types (`internal/ast/jsx.go`) - **Complete**
- Package registry for type definitions - **Exists**
- Unification algorithm - **Exists**

## Parser Limitations (to be addressed in future phases)

All planned parser features have been implemented.

**Recently Added Parser Features**:
- ✅ Boolean shorthand attributes (`<input disabled />`) - implemented
- ✅ Spread attributes (`<Button {...props} />`) - implemented
- ✅ Member expression components (`<Icons.Star />`) - implemented

---

## Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| React types are complex | Use `@types/react` directly; leverage existing TypeScript type loading infrastructure |
| Generic components are hard | Defer to Phase 4+, use any for now |
| JSX whitespace handling is tricky | Study React's algorithm, add comprehensive tests |

---

## References

- [TypeScript JSX Checking](https://www.typescriptlang.org/docs/handbook/jsx.html)
- [React JSX Transform](https://reactjs.org/blog/2020/09/22/introducing-the-new-jsx-transform.html)
- [Escalier Requirements](requirements.md)
