# Implementation plan: framework-agnostic JSX in `internal/solver`

## Goal

`internal/solver` has no JSX inference today. Every `JSXElementExpr` and
`JSXFragmentExpr` falls through to `reportUnsupported` in `inferExpr`
([#2100](https://github.com/escalier-lang/escalier/issues/2100)).

This plan adds JSX type checking without tying the solver to any one framework.
The solver reads a small set of types from a *JSX profile*. A profile is a
package that answers four questions: what a JSX expression evaluates to, which
values may appear as children, which lowercase tags exist and what props each
takes, and which props every element accepts. React, Preact, Solid and Vue each
publish those answers as a `JSX` namespace in their `jsx-runtime` module, so a
framework becomes one more profile rather than a special case in the solver.

The first profile ships with the compiler and needs no third-party package. It
types components the way React types function components: a component is a
function that takes one props object and returns something renderable. This
gets JSX checking in place while third-party `.d.ts` loading
([#2099](https://github.com/escalier-lang/escalier/issues/2099)) continues in
parallel. The React profile lands after #2099 and reuses everything built here.

## Non-goals for the framework-less milestone

- A runtime. Codegen keeps emitting `react/jsx-runtime` imports until the import
  source setting described under PR 7 lands.
- `ref`, class components, `LibraryManagedAttributes`, and default props. These
  are React concepts and arrive with the React profile.
- Per-tag attributes and DOM event handler types such as
  `onclick: fn(event: MouseEvent) -> void`. Every tag takes the same global
  attributes at first. Both can later be derived from `web:dom`, which
  `web:jsx` already imports for its tag list.

## The JSX profile

The solver holds the resolved profile in one Go struct, built once per run on
the first JSX expression it infers.

```go
// jsxProfile holds the types JSX inference reads. Each field is nil when the
// profile package does not declare that type.
type jsxProfile struct {
	Element             soltype.Type // the type of every JSX expression
	Node                soltype.Type // what a child or a component's result may be
	IntrinsicElements   soltype.Type // object type from tag name to props
	IntrinsicAttributes soltype.Type // props every element accepts, such as `key`
	ChildrenProp        string       // the prop that receives children, "children" by default
}
```

Where the types come from depends on the profile source:

- The built-in profile reads them from the top-level type exports of a
  compiler-shipped package, `web:jsx`.
- A third-party profile reads them from the `JSX` namespace that
  `<source>/jsx-runtime` exports, using TypeScript's names
  (`JSX.Element`, `JSX.IntrinsicElements`, `JSX.IntrinsicAttributes`,
  `JSX.ElementChildrenAttribute`). `Node` maps to the profile's child type,
  `React.ReactNode` for React.

A new `Options.JSXImportSource` field selects the source. The compiler and
the LSP server fill it from the `escalier.jsxImportSource` field of the
package's `package.json`, read by `ReadPackageConfig` alongside the
dependencies it already reads. Each package in a monorepo can then pick its own
framework.

```json
{
  "escalier": {
    "jsxImportSource": "react"
  }
}
```

A missing field, or an empty string, selects `web:jsx`. Any other value names an npm package whose
`<value>/jsx-runtime` the solver loads through the third-party chain. Inference
code reads only `jsxProfile`, so nothing past the loader knows which framework
is in use.

When the profile cannot be loaded, the first JSX expression reports one
diagnostic naming the source, and every JSX expression in the run infers to
`ErrorType` so no cascade follows.

## The `web:jsx` package

`web:jsx` is a hand-written Escalier file at
`internal/interop/data/web/jsx.esc`. It lives under `web/` because JSX
renders to DOM elements, and it imports `web:dom` for the tag list. That puts
it in the browser tier, next to `web:dom`. A first draft:

```esc
import "web:dom" { HTMLElementTagNameMap }

// Opaque so that programs do not depend on the shape of an element, which
// differs between frameworks.
export declare class Element {}

export type Node = Element | string | number | boolean | null | undefined | Array<Node>

export type IntrinsicAttributes = {key?: string | number}

type GlobalAttributes = {
	id?: string,
	class?: string,
	style?: string,
	title?: string,
	hidden?: boolean,
	tabindex?: number,
	children?: Node,
}

export type IntrinsicElements = {[K]: GlobalAttributes for K in keyof HTMLElementTagNameMap}
```

The mapped type gives every tag `HTMLElementTagNameMap` declares, about 110 of
them, and no others. `<div>` and `<button>` are valid and `<dvi>` is an
unknown tag. SVG and MathML tags are left out until a later PR adds
`SVGElementTagNameMap` and `MathMLElementTagNameMap` with their own attribute
types.

Attribute names follow HTML (`class`, `tabindex`), not React (`className`,
`tabIndex`), because no framework sits in between to rename them. Each props
object is exact, so a misspelled attribute is an `ExtraPropertyError`.

## Inference rules

A new file, `internal/solver/infer_jsx.go`, adds `inferJSXElement` and
`inferJSXFragment` arms to `inferExpr`.

### Tag resolution

The tag rule matches codegen's `isIntrinsicElement`. A tag that is a single
identifier starting with a lowercase letter is intrinsic. Any other tag,
including every member tag such as `<Icons.Star>`, names a value. PR 1 moves
that predicate into `internal/ast` so the solver and codegen share it.

### The props object

The solver builds one object type from the attributes and children:

- `name="text"` gives `name` the string literal type `"text"`.
- `name={expr}` gives `name` the type of `expr`.
- A bare `name` gives `name` the type `true`.
- `{...expr}` spreads the type of `expr`, with the same rules as an object
  literal spread.
- Children set the `ChildrenProp` key. With no children the key is absent. One
  child gives its own type. Several children give a tuple of their types, which
  is a subtype of `Array<Node>`.

Child types follow codegen's JSX whitespace rules, which PR 1 also moves into
`internal/ast`. Text becomes a string literal after normalization,
whitespace-only text and empty `{}` containers are dropped, `{expr}` is the type
of `expr`, and a nested element or fragment is `Element`.

### Intrinsic elements

For `<div ...>` the solver constrains the props object against
`IntrinsicElements["div"]` intersected with `IntrinsicAttributes`. An unknown
tag reports a diagnostic naming the tag. The element's type is `Element`.

### Components

For `<Button label="x">hi</Button>` the solver splits the props object. Keys of
`IntrinsicAttributes` such as `key` are checked against it and removed. The rest
is passed to the tag value as a single call argument.

The call goes through `inferCallWithArgTypes`, the same path
`inferTaggedTemplateLit` takes. The solver builds a synthetic `CallExpr` whose
callee is the tag (an `IdentExpr` or `MemberExpr`) and whose argument is a
synthetic `ObjectExpr` holding the original attribute and child expressions.
Arity, overloads, generic components, unknown identifiers and argument blame all
come from that path.

```esc
fn Button(props: {label: string}) -> Element { ... }

<Button label="Click me" />          // ok
<Button />                           // missing `label`
<Button label="x" size={2} />        // `size` is extra, since props is exact
<Button label="x">child</Button>     // `children` is extra
```

The result of the call is constrained to `Node`, which rejects a component that
returns, say, an object. The element's own type is `Element` whatever the
component returns.

The synthetic nodes are built once per JSX expression and cached on the checker.
Later passes that need to see the call, such as ownership events and closure
captures, read the cached nodes instead of building their own.

### Fragments

`<>...</>` checks each child against `Node` and has type `Element`.

## Pull requests

1. **Walk JSX in the AST visitor and share JSX helpers.** Implement `Accept` on
   `JSXElementExpr` and `JSXFragmentExpr` to visit attributes, spreads and
   children ([#490](https://github.com/escalier-lang/escalier/issues/490)).
   Remove the manual walks in `internal/liveness/capture_analysis.go`. Move
   `isIntrinsicElement` and the child whitespace normalization from
   `internal/codegen/jsx.go` to `internal/ast`. Done when codegen and liveness
   tests pass unchanged.
2. **Load the JSX profile.** Add `web:jsx` and its entry in the
   `dts_to_esc` partition and tier tables. Add `escalier.jsxImportSource` to
   `ReadPackageConfig`, `Options.JSXImportSource`, `jsxProfile`, and the
   loader for the built-in source. Report a missing
   profile once per run. Done when a solver test loads the profile and asserts
   each field's rendered type.
3. **Infer intrinsic elements and fragments.** Add `infer_jsx.go` with the props
   object, intrinsic lookup, `key` handling and fragments. Add
   `TestInferJSXFramework`, a table that runs against `web:jsx`, and a fixture
   under `fixtures/jsx_framework_less/`.
4. **Infer components.** Add the synthetic call, the `Node` check on the result,
   and member tags. Extend `TestInferJSXFramework` with component rows. Test
   components written with explicit signatures or as `declare fn`, and test the
   inferred signature of an unannotated component in its own row.
5. **Ownership and borrow checking through JSX.** Make
   `ownership_events_emit.go` and `closure_captures.go` read the cached
   synthetic call, and drop JSX from the opaque case at
   `ownership_events_emit.go:733`. Done when a move of an owned value into a
   prop and a later use of that value reports the same error as the equivalent
   direct call.
6. **JSX wording for diagnostics.** Reword errors whose blame site is a JSX
   attribute or child, such as "Missing required prop 'title'" and "Component
   'Container' does not accept children". The constrain errors stay the same and
   only the message changes.
7. **Codegen import source.** Read `JSXImportSource` in codegen so the emitted
   import names `<source>/jsx-runtime`. The built-in source keeps
   `react/jsx-runtime` until Escalier ships a runtime of its own.
8. **React profile (blocked on #2099).** Load the `JSX` namespace from
   `react/jsx-runtime`, add `ref` and `LibraryManagedAttributes`, and remove the
   `t.Skip` from `TestInferJSX`. That closes #2100.

PRs 1 and 2 can land in either order. PRs 3 to 7 depend on 2. Only PR 8 waits on
#2099.

## Testing

- `TestInferJSXFramework` in `internal/solver/jsx_test.go` covers the built-in
  profile. It sits beside the existing `TestInferJSX`, which stays skipped and
  keeps its React expectations (`className`, `onClick`) for PR 8.
- Rows assert full error messages and use Escalier type syntax for inferred
  types, following `infer_class_test.go`.
- Each component row uses a component with an explicit signature, so a failure
  points at the JSX rules rather than at function inference.

## Open questions

- `internal/interop/data/web/` is generated by `dts_to_esc` from `lib.dom.d.ts`.
  `web:jsx` has no `.d.ts` origin, so the partition table needs a way to
  list a hand-written file, or the generator needs to leave `jsx.esc` in place.
- Should an intrinsic element type as its DOM element, so `<div />` is
  `HTMLDivElement` through `HTMLElementTagNameMap`? That fits a DOM-only
  runtime, but React, Preact and Solid each return a different element type, so
  code written against it would not carry over to a framework profile.
