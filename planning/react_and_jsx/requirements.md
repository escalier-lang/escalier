# Requirements: JSX and React Support in Escalier

## Overview

This document outlines the requirements for implementing full JSX and React support in Escalier. The goal is to enable developers to write React applications using Escalier's type-safe, immutable-by-default syntax while maintaining seamless interoperability with the React ecosystem.

## Current State

### Already Implemented
- **JSX Parser** (`internal/parser/jsx.go`): Fully functional and tested
- **JSX AST Types** (`internal/ast/jsx.go`, `internal/ast/expr.go`):
  - `JSXElementExpr` - `<Tag>...</Tag>`
  - `JSXFragmentExpr` - `<>...</>`
  - `JSXOpening`, `JSXClosing` - Opening/closing tags
  - `JSXAttr` - Element attributes
  - `JSXChild` - Children (text, expressions, nested elements)
  - `JSXExprContainer` - Expression containers `{...}`

### Not Yet Implemented
- Type checking for JSX expressions (`internal/checker/infer_expr.go:665-668`)
- Code generation for JSX expressions (`internal/codegen/builder.go:1969-1972`)

---

## 1. JSX Syntax Requirements

### 1.1 Element Syntax

Escalier shall support standard JSX element syntax:

```escalier
// Self-closing elements
val icon = <Icon name="star" />

// Elements with children
val container = <div className="wrapper">
    <h1>Hello World</h1>
</div>

// Fragments
val list = <>
    <Item />
    <Item />
</>
```

### 1.2 Attribute Syntax

Support all JSX attribute forms:

```escalier
// String literals
<input type="text" />

// Expression values
<Button onClick={handleClick} disabled={isLoading} />

// Spread attributes
<Component {...props} />

// Boolean shorthand (attribute presence = true)
<input disabled />
```

### 1.3 Children Syntax

Support all child types:

```escalier
// Text children
<p>Hello World</p>

// Expression children
<span>{userName}</span>

// Conditional rendering
<div>{showMessage && <Message />}</div>

// Mapped children
<ul>
    {items.map(fn(item) { <li key={item.id}>{item.name}</li> })}
</ul>

// Mixed children
<div>
    Hello, <strong>{name}</strong>!
</div>
```

---

## 2. Type Checking Requirements

### 2.1 Component Type Resolution

The type checker shall resolve JSX element names to their corresponding types:

| Element Type | Resolution |
|--------------|------------|
| Lowercase (`div`, `span`) | Intrinsic HTML element |
| Uppercase (`Button`, `Card`) | Component function or class |
| Member expression (`Menu.Item`) | Namespaced component |

### 2.2 Props Type Checking

#### 2.2.1 HTML Intrinsic Elements

For intrinsic HTML elements, props shall be type-checked against React's intrinsic element definitions:

```escalier
// Valid - className is a valid div attribute
<div className="container" />

// Error - unknownProp is not a valid div attribute
<div unknownProp="value" />  // Type error
```

#### 2.2.2 Component Props

For function and class components, props shall be type-checked against the component's prop type:

```escalier
type ButtonProps = {
    label: string,
    onClick: fn() -> void,
    disabled?: boolean,
}

fn Button(props: ButtonProps) {
    // ...
}

// Valid
<Button label="Submit" onClick={handleSubmit} />

// Error - missing required prop 'label'
<Button onClick={handleSubmit} />  // Type error

// Error - wrong type for 'disabled'
<Button label="Submit" onClick={handleSubmit} disabled="yes" />  // Type error
```

#### 2.2.3 Generic Components

Support type inference for generic components:

```escalier
type ListProps<T> = {
    items: Array<T>,
    renderItem: fn(item: T) -> JSX.Element,
}

fn List<T>(props: ListProps<T>) {
    // ...
}

// T should be inferred as User
<List items={users} renderItem={fn(user) { <UserCard user={user} /> }} />
```

### 2.3 Children Type Checking

#### 2.3.1 Children Prop

Children shall be typed according to the component's `children` prop type:

```escalier
type CardProps = {
    children: JSX.Element,
}

type ContainerProps = {
    children: Array<JSX.Element>,
}

type TextProps = {
    children: string,
}
```

#### 2.3.2 No Children

Components that don't accept children shall error when children are provided:

```escalier
type IconProps = {
    name: string,
}

fn Icon(props: IconProps) {
    // ...
}

// Error - Icon does not accept children
<Icon name="star">text</Icon>  // Type error
```

### 2.4 Return Type

JSX expressions shall have the type `JSX.Element` (aliased to `React.ReactElement`):

```escalier
fn MyComponent() -> JSX.Element {
    return <div>Hello</div>
}

// Type inference should work
fn MyComponent() {
    return <div>Hello</div>  // Return type inferred as JSX.Element
}
```

### 2.5 Event Handler Types

Event handlers shall be properly typed:

```escalier
// onClick receives a MouseEvent
<button onClick={fn(e) {
    e.preventDefault()  // e is typed as MouseEvent
}} />

// onChange receives appropriate event for element type
<input onChange={fn(e) {
    val value = e.target.value  // e is typed as ChangeEvent<HTMLInputElement>
}} />
```

### 2.6 Ref Types

Support ref typing:

```escalier
val inputRef = useRef<HTMLInputElement>(null)

<input ref={inputRef} />
```

### 2.7 Key Prop

The `key` prop shall be accepted on all elements when rendering lists:

```escalier
{items.map(fn(item) {
    <Item key={item.id} data={item} />
})}
```

---

## 3. Code Generation Requirements

### 3.1 JSX Transform Options

Support both classic and automatic JSX transforms:

#### 3.1.1 Classic Transform (React 16 and earlier)

```javascript
// Input: <div className="foo">Hello</div>
// Output:
React.createElement("div", { className: "foo" }, "Hello")
```

#### 3.1.2 Automatic Transform (React 17+)

```javascript
// Input: <div className="foo">Hello</div>
// Output:
import { jsx as _jsx } from "react/jsx-runtime";
_jsx("div", { className: "foo", children: "Hello" })
```

### 3.2 Element Transformation

| JSX | JavaScript Output (Classic) |
|-----|----------------------------|
| `<div />` | `React.createElement("div", null)` |
| `<Component />` | `React.createElement(Component, null)` |
| `<ns.Component />` | `React.createElement(ns.Component, null)` |

### 3.3 Attribute Transformation

```escalier
// Input
<Button
    label="Submit"
    onClick={handleClick}
    disabled={true}
    {...extraProps}
/>

// Output (Classic)
React.createElement(Button, Object.assign({
    label: "Submit",
    onClick: handleClick,
    disabled: true
}, extraProps))
```

### 3.4 Children Transformation

```escalier
// Input
<div>
    <span>Hello</span>
    {name}
    <span>!</span>
</div>

// Output (Classic)
React.createElement("div", null,
    React.createElement("span", null, "Hello"),
    name,
    React.createElement("span", null, "!")
)
```

### 3.5 Fragment Transformation

```escalier
// Input
<>
    <Item />
    <Item />
</>

// Output (Classic)
React.createElement(React.Fragment, null,
    React.createElement(Item, null),
    React.createElement(Item, null)
)
```

### 3.6 Boolean and Conditional Handling

```escalier
// Boolean shorthand
<input disabled />
// Output: createElement("input", { disabled: true })

// Conditional rendering - preserve JavaScript semantics
{show && <Component />}
// Output: show && createElement(Component, null)
```

---

## 4. React Integration Requirements

### 4.1 React Type Definitions

The compiler shall load and use React type definitions from `@types/react`:

- `React.FC<P>` / `React.FunctionComponent<P>`
- `React.Component<P, S>`
- `React.ReactElement`
- `React.ReactNode`
- `JSX.Element`
- `JSX.IntrinsicElements`

### 4.2 Hooks Support

All React hooks shall work with proper type inference:

```escalier
// useState
val [count, setCount] = useState(0)  // count: number, setCount: fn(number) -> void
val [user, setUser] = useState<User | null>(null)

// useEffect
useEffect(fn() {
    // effect
    return fn() { /* cleanup */ }
}, [dependency])

// useRef
val inputRef = useRef<HTMLInputElement>(null)

// useCallback
val memoizedFn = useCallback(fn(x: number) {
    return x * 2
}, [])

// useMemo
val computed = useMemo(fn() { expensiveComputation() }, [deps])

// useContext
val theme = useContext(ThemeContext)

// useReducer
val [state, dispatch] = useReducer(reducer, initialState)
```

### 4.3 Component Definition Patterns

Support multiple component definition patterns:

```escalier
// Function component (recommended)
fn Greeting(props: {name: string}) {
    return <h1>Hello, {props.name}!</h1>
}

// Function component with destructuring
fn Greeting({name}: {name: string}) {
    return <h1>Hello, {name}!</h1>
}

// Typed function component
val Greeting: React.FC<{name: string}> = fn({name}) {
    <h1>Hello, {name}!</h1>
}

// Class component (if Escalier classes support extends)
class Counter extends React.Component<Props, State> {
    // ...
}
```

### 4.4 Forward Ref

Support `forwardRef` with proper typing:

```escalier
val FancyInput = React.forwardRef<HTMLInputElement, Props>(fn(props, ref) {
    <input ref={ref} {...props} />
})
```

### 4.5 Context

Support context creation and usage:

```escalier
val ThemeContext = React.createContext<Theme>(defaultTheme)

// Provider
<ThemeContext.Provider value={theme}>
    {children}
</ThemeContext.Provider>

// Consumer (hook-based)
val theme = useContext(ThemeContext)
```

---

## 5. Error Handling Requirements

### 5.1 Type Error Messages

Provide clear, actionable error messages:

```
Error: Property 'label' is missing in props for component 'Button'
  at src/App.esc:15:5

  Expected props: { label: string, onClick: fn() -> void, disabled?: boolean }
  Received props: { onClick: fn() -> void }

  Did you mean to add: label="..."
```

### 5.2 Common Error Cases

| Error Case | Message |
|------------|---------|
| Missing required prop | "Property 'X' is missing in props for component 'Y'" |
| Wrong prop type | "Type 'A' is not assignable to prop 'X' of type 'B'" |
| Unknown prop on intrinsic | "Property 'X' does not exist on element 'div'" |
| Children not allowed | "Component 'X' does not accept children" |
| Invalid element | "'X' is not a valid JSX element" |

### 5.3 Suggestions

Where possible, suggest corrections:

- Typos in prop names: "Did you mean 'className' instead of 'classname'?"
- Missing imports: "Component 'Button' is not defined. Did you forget to import it?"

---

## 6. Configuration Requirements

### 6.1 Compiler Options

Add JSX-related compiler options:

```json
{
    "jsx": "react" | "react-jsx" | "preserve",
    "jsxFactory": "React.createElement",
    "jsxFragmentFactory": "React.Fragment",
    "jsxImportSource": "react"
}
```

| Option | Description |
|--------|-------------|
| `jsx: "react"` | Classic transform using `React.createElement` |
| `jsx: "react-jsx"` | Automatic transform using `react/jsx-runtime` |
| `jsx: "preserve"` | Output JSX as-is (for another tool to transform) |

### 6.2 Per-File Pragmas

Support file-level pragma overrides:

```escalier
/** @jsx h */
/** @jsxFrag Fragment */

import { h, Fragment } from "preact"

<div>Hello</div>  // Compiles to: h("div", null, "Hello")
```

---

## 7. Testing Requirements

### 7.1 Type Checker Tests

- Valid JSX elements type check without errors
- Invalid props produce appropriate type errors
- Generic component inference works correctly
- Event handler types are correct
- Children types are validated

### 7.2 Code Generation Tests

- All JSX constructs produce correct JavaScript
- Both classic and automatic transforms work
- Source maps correctly map JSX to generated code
- Output matches React's expected format

### 7.3 Integration Tests

- Full React applications compile and run
- Hot module replacement works
- React DevTools integration works
- Server-side rendering produces correct output

### 7.4 Snapshot Tests

Extend existing snapshot testing for:
- Type checker output for JSX expressions
- Generated JavaScript for various JSX patterns

---

## 8. Future Considerations

### 8.1 React Server Components

Plan for future RSC support:
- `"use client"` and `"use server"` directives
- Async components
- Server/client boundary validation

### 8.2 Other JSX Libraries

While React is the primary target, the design should accommodate:
- Preact
- Solid.js
- Vue JSX
- Custom JSX pragmas

### 8.3 Performance Optimizations

Consider compile-time optimizations:
- Static element hoisting
- Inline constant elements
- Dead code elimination for unused components

---

## 9. Implementation Phases

### Phase 1: Core JSX Type Checking
- Implement `JSXElementExpr` inference in `infer_expr.go`
- Resolve intrinsic elements to HTML attribute types
- Resolve component elements to their prop types
- Basic children type checking

### Phase 2: Code Generation
- Implement JSX code generation in `builder.go`
- Support classic transform initially
- Generate proper props objects
- Handle children arrays

### Phase 3: React Integration
- Full React type definition support
- Hooks type inference
- Event handler typing
- Ref typing

### Phase 4: Advanced Features
- Automatic JSX transform
- Generic component inference
- ForwardRef support
- Context typing

### Phase 5: Developer Experience
- Improved error messages
- IDE integration hints
- Documentation and examples

---

## 10. References

- [React JSX Documentation](https://react.dev/learn/writing-markup-with-jsx)
- [TypeScript JSX](https://www.typescriptlang.org/docs/handbook/jsx.html)
- [React TypeScript Cheatsheet](https://react-typescript-cheatsheet.netlify.app/)
- [JSX Specification](https://facebook.github.io/jsx/)
