# Design: Coeffects

## 1. Scope

This document proposes adding coeffects to Escalier. A *coeffect* is a
requirement a computation places on the context it runs in. An *effect* is the
opposite: something a computation does to the world. Escalier already has one
effect in its types, the `throws` clause, which records the exceptions a function
may raise. This design adds a `needs` clause, which records what a function
requires from its caller's context, and builds both clauses on one set of rules.

It covers three kinds of requirement:

- *Context values*, such as a theme, a logger or the current request, which a
  function reads without taking them as parameters.
- *Environments*, the browser global scopes and non-browser runtimes that
  [planning/environments/design.md](../environments/design.md) defines.
- *Features*, platform APIs that some version of a supported runtime lacks and
  that no polyfill supplies.

The environments design is a coeffect system with one context per entrypoint.
This design moves its requirements onto function types, so they are inferred
per function and flow from callee to caller. Section 4.2 compares the two.

## 2. Background

The idea comes from Petricek, Orchard and Mycroft, "Coeffects: a calculus of
context-dependent computation" (ICFP 2014). An effect system annotates a
function's result. A coeffect system annotates its context. In both, the
annotation of a call joins the annotation of the caller, and some construct in
the program removes it:

| | Effect (`throws`) | Coeffect (`needs`) |
|---|---|---|
| Records | what a function may raise | what a function requires |
| Flows | from callee to caller | from callee to caller |
| Removed by | `catch` | a provider, or the entrypoint's target |
| Union means | may raise either | requires both |

The last row is the one difference in how the two combine, and section 6.3
returns to it.

## 3. Problem statement

JavaScript code depends on its context in ways TypeScript does not see. Each of
the following is a runtime failure today that a `needs` clause would report at
compile time:

- **A React context with no provider.** A component that calls `useContext`
  under no matching provider gets the context's default value, or `undefined`,
  and the mistake shows up as wrong output.
- **Code in the wrong environment.** Server code that reaches `window` or
  `document` through any chain of imports crashes during server-side rendering.
  A browser bundle that reaches `fs` fails to build or fails at load. The
  `"use client"` directive and the `server-only` package check this by hand.
- **Hooks outside render.** React requires hooks to be called during a
  component's render. A lint rule approximates this.
- **Ambient request state.** A request ID or the authenticated user read from
  `AsyncLocalStorage` or a module-level variable is a dependency no signature
  shows.
- **Hidden inputs in tests.** `Date.now`, `Math.random`, configuration and the
  network are read directly, so a test can't replace them without a dependency
  injection framework.
- **Unaudited capabilities.** Nothing in a module's types says whether it can
  reach the network or the filesystem.

## 4. Current state

### 4.1 `throws`

[docs/02_functions.md](../../docs/02_functions.md) has the rules. Omitting the
clause declares that a function raises nothing, `throws _` reads the clause off
the body, and a call contributes its callee's clause to the caller's. W5 in
[planning/explicit_signatures/design.md](../explicit_signatures/design.md) keeps
`throws _` legal on a module-level function, where every other part of the
signature must be written.

A higher-order function passes a callback's exceptions through an ordinary type
parameter that defaults to `never`. `Promise.then` in
[internal/interop/overlay/std/prelude.replace.esc](../../internal/interop/overlay/std/prelude.replace.esc)
is the model:

```esc
then<TResult1 = T, F = never>(&self, onfulfilled: fn (value: T) -> TResult1 | PromiseLike<TResult1, F> throws F) -> Promise<TResult1, E | F>
```

### 4.2 Environments

The environments design attaches `@env` to each declaration and gives each
entrypoint one target environment, stated in `package.json` (F9) or inferred
from its imports (F1). A reference to a declaration the target lacks is an error
(F3). Several of its rules are coeffect rules already:

- Combining references intersects their environment sets. A program with no
  target must run in every environment (F1), and an empty intersection is the
  unsatisfiable-imports error (F2).
- Only a value reference adds a requirement. A type-only reference compiles to
  nothing, so a worker may name `WindowProxy` as a type (F4).
- One name can have a different type in each environment, as `location` does
  (F5).

What it lacks, measured against a coeffect system:

- A function's type does not say which environments it needs, so nothing is
  inferred per function and nothing flows from callee to caller.
- A library function that is portable except for one helper can't say so. Its
  package has to pick a target or split the code across entrypoints.
- Nothing inside a program can provide an environment. Runtime feature
  detection is a non-goal of that design (its §11).

## 5. Requirements

**C1. One mechanism for effects and coeffects.** `throws` and `needs` share one
set of rules for inference, polymorphism, printing and interop defaults. A rule
stated for one holds for the other unless section 6 names an exception.

**C2. A requirement is inferable.** `needs _` reads the clause off the body, as
`throws _` does, and W5 allows it on module-level functions for the same reason
it allows `throws _`. Omitting the clause declares that the function needs
nothing.

**C3. A requirement flows through calls.** A call contributes its callee's
`needs` clause to the caller's.

**C4. A higher-order function passes a callback's extent-bound requirements
through.** A requirement variable is an ordinary type parameter, as an exception
variable is. Capturable requirements are owed where a closure is created and
need no variable.

**C5. A requirement is removed only by something that provides it.** A context
value is removed by a provider. An environment is removed by the entrypoint's
target.

**C6. An entrypoint has no outstanding context requirement.** Whatever reaches
an entrypoint unprovided is an error at the entrypoint, naming the requirement
and the call that introduced it.

**C7. Environments are a requirement kind.** The environments design's
vocabulary, its per-declaration `@env` facts and its intersection rule carry over
unchanged. Its reference check becomes the leaf case of `needs` inference.

**C8. A requirement adds no runtime lookup.** Every requirement is known at
compile time, so reading a context value compiles to a property read.

**C9. Providers are composable.** A single function can provide several
requirements at once, and a caller can combine what several such functions
provide in one place. A test suite, for example, can define its standard set of
context values once and reuse it in every test, adding or overriding individual
values where a test needs to.

**C10. A requirement can be extent-bound.** An *extent-bound* requirement holds
only while the call that established it is running, as `Render` holds only
during a component's render and a transaction holds only until it commits.
Using one after that call returns is an error. This covers a use after an
`await` or `yield` and a use from a closure that escapes. C9 applies to
extent-bound requirements too, so a provider can supply them alongside
capturable ones.

**C11. Raising the targets removes requirements without source edits.** When
every runtime version an entrypoint supports provides an API, no function needs
a requirement for it, and changing the supported versions changes no function's
source.

## 6. Design

All syntax in this section is a proposal.

### 6.1 Declaring a context

A context declaration introduces a name that is both a type, which a `needs`
clause names, and a value, which the body reads:

```esc
context Theme: ThemeValue
context Logger: LoggerValue
```

Reading `Theme` in a body adds `Theme` to the function's requirement:

```esc
fn label(text: string) -> string needs Theme {
    return Theme.prefix ++ text
}
```

### 6.2 Providing a context

`with` provides a value for the block it encloses:

```esc
fn main() -> undefined {
    with Theme = dark {
        console.log(label("hi"))
    }
}
```

`with` removes `Theme` from the requirements of the calls in its block, as
`catch` removes the exceptions its arms match. A `with` whose result is a React
element also removes it from the element's type (section 6.6.3).

### 6.3 Combining requirements

A `needs` clause is a union, and a union means the function requires every
member. For context values that is the plain union: a function that reads
`Theme` and `Logger` has `needs Theme | Logger`.

An environment fact is a set of places a declaration exists, written
`Env<window | dedicated_worker>`. Requiring two such facts means running where
both hold, so the union of two environment requirements normalizes to the
intersection of their sets:

```esc
// Env<window | worker> | Env<window>  normalizes to  Env<window>
```

This is the only place the shared rules need a case for one requirement kind.

### 6.4 Higher-order functions

A closure's capturable requirements are owed where the closure is created
(section 6.5), not where it is called. Environment requirements are owed there
too, since a closure runs in the global scope it was created in. So a
higher-order function such as `map` takes no part in them:

```esc
fn map<T, U, E = never>(arr: [T], f: fn (T) -> U throws E) -> [U] throws E

fn renderItem(item: Item) -> Node needs Theme | Env<window> { ... }

fn renderAll(items: [Item]) -> [Node] needs _ {
    return items.map(fn (item) => renderItem(item))
}
// inferred: fn (items: [Item]) -> [Node] needs Theme | Env<window>
```

`renderAll` owes `Theme` and `Env<window>` because it creates the closure that
calls `renderItem`. A capturable requirement never appears on a function value's
type.

A requirement variable is needed only for extent-bound requirements, which
section 6.8 defines. On a callback's type, `needs R` declares that the callee
calls the callback before it returns, so an extent-bound requirement the caller
holds still holds inside the callback:

```esc
fn map<T, U, E = never, R = never>(arr: [T], f: fn (T) -> U throws E needs R) -> [U] throws E needs R

declare fn setTimeout(callback: fn () -> undefined, ms: number) -> number
```

A closure that needs `Render` may be passed to `map` during a render, and
passing it to `setTimeout` is an error, since `setTimeout`'s callback type has
no `needs` clause. `R` is inferred at each call, as `E` is. Only library
signatures write a requirement variable.

### 6.5 Callbacks that escape

A callback passed to `setTimeout` or `addEventListener` runs after its caller
returns. An exception it raises goes to whoever calls it, but a context value it
reads may have no provider by then. A closure therefore takes its context values
from the providers in scope where it is created, and only a requirement no
enclosing provider covers stays in its type. A callback whose type allows no
requirements, such as an event listener, is then accepted inside a `with` block
that covers what it reads.

Environments are unaffected, because a callback runs in the global scope it was
created in.

### 6.6 React

React calls a child component after its parent returns, and it resolves a
context by the element's position in the tree, not by where the element was
created. So a requirement belongs on the element type, not on the call. This is
the one place a capturable requirement stays on a value's type.

The section uses React as its example, but the design applies to any frontend
framework that resolves a context by position in the component tree. Vue's
`provide` and `inject`, SolidJS's `createContext` and `useContext`, Svelte's
`setContext` and `getContext`, and Preact's context API all work this way. Each
framework needs its own codegen for a context declaration, a read and a
provider, and its own extent-bound requirement in place of `Render`. Vue's
`inject` must be called during `setup`, and Svelte's `getContext` must be
called while the component initializes. The typing rules carry over unchanged:

- requirements travel on the framework's element or node type;
- element-typed props pass requirements through by default;
- a `with` whose result is an element is the provider.

#### 6.6.1 Components

A component reads a context the way any function does, and its requirements are
inferred the same way. A context declared without a default value is a
requirement, and one declared with a default adds none.

```esc
context Theme: ThemeValue                 // React.createContext with no default

fn Button(props: {label: string}) -> Element needs Theme {
    return <button class={buttonClass()}>{props.label}</button>
}

fn buttonClass() -> string needs Theme { return Theme.button }

fn useTheme() -> ThemeValue needs Theme | Render { ... }
```

`<C/>` has type `Element needs N`, where `N` is `C`'s requirements minus
`Render`. React provides `Render` when it renders `C`. A component's
requirements include those of every element it returns, so a component that
renders `<Button/>` needs `Theme` itself unless it provides it.

`Render` is a requirement only a component body has, so calling a hook outside a
component is an error. That replaces the hooks lint rule. `Render` is
extent-bound (section 6.8).

`Root.render` accepts only an element whose requirements are environments:

```esc
fn render(self, el: Element needs Env<window>) -> undefined
```

A context requirement that reaches it is an error naming the component that
needs the context and the chain of elements down to it.

#### 6.6.2 Pass-through

An element-typed parameter or prop with no `needs` clause gets a fresh
requirement variable. A returned `Element` includes the variables of the
function's element-typed parameters and props, joined with any requirement its
clause writes. So a component that takes children passes their requirements
through without writing anything:

```esc
fn Card(props: {children: Element}) -> Element {
    return <div class="card">{props.children}</div>
}
// means: fn <R>(props: {children: Element needs R}) -> Element needs R

fn Panel(props: {title: string, children: Element}) -> Element needs Theme {
    return <section class={Theme.panel}><h2>{props.title}</h2>{props.children}</section>
}
// means: fn <R>(props: {title: string, children: Element needs R}) -> Element needs Theme | R
```

When a parent writes `<Card><Button/></Card>`, the parent creates the
`<Button/>` element, so its `Theme` requirement travels with the element through
`Card`'s variable. `Card` needs nothing of its own.

A component with requirements of its own passes the others through too.
Otherwise `Panel` would accept only children that need `Theme` or nothing, and a
child needing `Locale` could not be placed inside it, even under a root that
provides `Locale`.

The default covers element-typed parameters, props and slots, including render
props such as `render: fn (Item) -> Element`. It does not cover fields or
variables. It differs from `throws`, where a bare `fn () -> T` accepts only a
callback that raises nothing. The difference is deliberate. Children are almost
always passed through, and a missing variable would reject them at every call
site.

`needs never` on an element prop declares that the prop accepts no
requirements. A component writes it when it renders the element somewhere other
than under itself:

- under a separate React root, such as one created with `createRoot` inside an
  iframe, since context does not cross roots;
- through a registry rendered elsewhere, such as a toast manager that stores
  elements and renders them under a host near the app root.

The default is sound without it. Handing `props.children` to a registry whose
parameter is `Element needs never` is an error inside the component's body, at
the hand-off, and writing `needs never` on the prop moves that error to the
callers. React portals keep context, so a portal is not one of these cases.

#### 6.6.3 Providers

A `with` block whose result is an `Element` removes the requirement from the
result's type and compiles to a React provider around it. A provider component
is therefore ordinary code:

```esc
fn ThemeProvider<R>(props: {value: ThemeValue, children: Element needs R}) -> Element needs _ {
    return with Theme = props.value { props.children }
}
// inferred: fn <R>(props: {value: ThemeValue, children: Element needs R}) -> Element needs Exclude<R, Theme>
```

`with` is the only construct that removes a requirement, so nothing has to
declare that `ThemeProvider` provides `Theme`. A component may also write the
`with` directly:

```esc
fn App() -> Element {
    return with Theme = dark { <Layout><Button label="Save" /></Layout> }
}
```

Only the block's result is wrapped in the provider. An element created inside
the block that leaves another way, such as through an outer variable, keeps its
requirement in its type.

`ThemeProvider` removes a requirement from a requirement variable. `Exclude` in
the prelude is a conditional type, and a conditional type on an unsolved
variable does not reduce during inference. Open question 1 lists the two ways
out.

#### 6.6.4 Codegen

A context declaration compiles to `React.createContext(undefined)`. A component
body builds its own `$ctx` from `React.use` for the contexts it reads directly,
so the helpers it calls take `$ctx` as they would anywhere else. A `with` whose
result is an element compiles to the context's provider:

```js
const Theme = React.createContext(undefined);

function Button(props) {
    const $ctx = { Theme: React.use(Theme) };
    return <button className={buttonClass($ctx)}>{props.label}</button>;
}

function ThemeProvider(props) {
    return <Theme value={props.value}>{props.children}</Theme>;
}
```

The React planning in
[planning/react_and_jsx/requirements.md](../react_and_jsx/requirements.md) §4.5
types `createContext` and `useContext` as ordinary functions. This design
replaces that section's typing and keeps its runtime behaviour.

### 6.7 Codegen

A function with a non-empty `needs` clause for context values takes one hidden
first parameter, `$ctx`, a record of the values its caller provides. `with`
builds a new record, spreading the enclosing one when it is nested:

```esc
fn main() -> undefined {
    with Theme = dark {
        console.log(["a", "b"].map(fn (s) => label(s)))
    }
}
```

```js
function label($ctx, text) { return $ctx.Theme.prefix + text; }

function main() {
    {
        const $ctx = { Theme: dark };
        console.log(["a", "b"].map((s) => label($ctx, s)));
    }
}
```

A function value never takes `$ctx`. Three rules keep it that way:

- A direct call to a declared function with a context requirement passes
  `$ctx`, as in `label($ctx, s)`.
- A closure captures `$ctx` where it is created, which is the rule in section
  6.5.
- A declared function used as a value, whether passed as an argument, stored in
  a variable or assigned to a field, is wrapped in a closure that binds the
  current record. `p.then(label)` compiles to
  `p.then((x) => label($ctx, x))`.

Every function value therefore has the plain JavaScript calling convention. A
JavaScript built-in such as `Array.prototype.map` calls an Escalier closure
unchanged and needs no wrapper. Requirement variables and environment
requirements add no parameter. They are checked and erased. The runtime cost is
one closure allocation each time a declared function with a context requirement
is used as a value.

Section 6.6.4 has how React contexts compile.

### 6.8 Promises and generators

A promise or generator separates where its body starts from where it resumes,
as a callback that escapes does. They follow the rule in section 6.5: a
requirement is owed where the function is called, and the body reads the record
captured at that call.

- **Async functions.** `$ctx` is a parameter, so it lives in the async
  function's closure and survives every `await`. The returned promise carries no
  requirement, because the requirement was satisfied before the promise existed.
  An `async fn` absorbs its throws into the promise's rejection type, but its
  `needs` clause stays on the function:

  ```esc
  async fn load(id: string) -> Promise<Data, _> needs Logger { ... }

  fn main() -> undefined {
      with Logger = consoleLogger {
          load("a").then(fn (data) => show(data))   // Logger is captured at the call
      }
  }
  ```

- **Promise callbacks.** A callback's capturable requirements are owed where it
  is created, whether the callee runs it at once, as `map` does, or later, as
  `then` does. Passing a function by name binds the current record (section
  6.7). `then` runs its callback after it returns, so its callback type has no
  `needs` clause, and a callback that needs an extent-bound requirement can't be
  passed to it.

- **Generators.** Calling a generator function creates the iterator and captures
  `$ctx`. The body runs on each `next()` and reads the captured record. The
  requirement belongs to the generator function and not to
  `Generator<T, TReturn, TNext>`, so an iterator can be consumed outside the
  `with` block that created it.

- **Environments.** A continuation resumes in the global scope it started in, so
  environment requirements are unaffected.

Capture covers a requirement whose value is all the body needs. Some
requirements describe a span of time instead. `Render` holds only while React is
running the component's render, and a database transaction holds only until it
commits. A captured record can't put a continuation back inside that span, so
requirement kinds fall into two classes:

- A *capturable* requirement, such as a context value, is captured where the
  closure, async call or generator is created, as above.
- An *extent-bound* requirement, such as `Render` or an open transaction, holds
  only while the call that established it is running. Using one after an
  `await` or `yield` is an error, and so is an escaping closure that needs one.

```esc
async fn Profile(props: {id: string}) -> Promise<Element> needs _ {
    val user = await fetchUser(props.id)
    val theme = useTheme()
    // error: useTheme needs Render, which does not hold after the await above
    return <Card theme={theme} user={user} />
}
```

React's rule that a component can't call hooks after an `await` is this check.

### 6.9 Features and targets

A *feature* is a platform API that some version of a supported runtime lacks,
such as `OffscreenCanvas` before Safari 16.4. A feature requirement is a third
requirement kind. Like an environment requirement, it carries no value and is
erased.

#### 6.9.1 Targets

Each entrypoint states the runtime versions it supports, beside its
environments in `package.json` (environments F9). Node versions come from the
existing `engines` field. Browser versions come from a browserslist query or a
[Baseline](https://web.dev/baseline) year, which names the APIs every major
browser has supported for a stated period. A library states its targets the
same way, and an app's targets must fall within those of every library it uses.

#### 6.9.2 What is present, polyfilled or absent

For each API a build references, the compiler compares its targets against
per-version compatibility data, such as MDN's
[browser-compat-data](https://github.com/mdn/browser-compat-data), which
records when each API shipped in each browser and in Node and Deno. The API then
falls into one of three groups:

1. *Present.* Every target version supports it, so nothing is emitted.
2. *Polyfilled.* Some target lacks it and the standard library ships a polyfill,
   as for `Promise.withResolvers` or the newer `Set` methods. The build
   includes the polyfill.
3. *Possibly absent.* Some target lacks it and no polyfill exists, as for
   WebGPU or `OffscreenCanvas`. A reference adds the API's feature to the
   requirement.

Only the list of polyfills is maintained by hand. Everything else derives from
the targets and the compatibility data, which is the environments design's N3.

The checker knows which declaration each reference resolves to, so a build
includes exactly the polyfills it uses. It can tell `x.at()` on an array from
`x.at()` on a string, which a tool working without types cannot. Polyfills are
injected when the app is built, not inside each library, so two libraries don't
each ship a copy.

An API a target ships with a known defect is marked as a partial implementation
in the compatibility data. It is treated as possibly absent.

#### 6.9.3 Inferring feature requirements

A package's targets provide every feature that all of its target versions
support. Inference subtracts those as it goes, so they never appear in a
function's type:

```esc
fn makeSurface(w: number, h: number) -> OffscreenCanvas needs _ {
    return OffscreenCanvas(w, h)
}
// targets include Safari 16: inferred needs web.OffscreenCanvas
// targets all support it:     inferred needs nothing

fn startRenderer() -> undefined needs _ { ... makeSurface(800, 600) ... }
// needs whatever makeSurface needs
```

Raising the targets so that every version supports `OffscreenCanvas` removes the
requirement from `makeSurface` and from its whole call chain, with no edit to
the source. Lowering them adds it back. A function with no written clause then
reports an error at the reference, and a function with `needs _` picks the
requirement up.

A feature is named after its compatibility-data key, under the scheme of the
package that declares the API, as in `web.OffscreenCanvas`. A member that ships
separately from its type, such as `navigator.gpu`, has a feature of its own.

#### 6.9.4 Providing a feature

`with` provides a feature after checking for it at runtime. A feature carries
no value, so the binding has no `=`, and an `else` block runs when the check
fails:

```esc
fn main() -> undefined {
    with web.OffscreenCanvas {
        startRenderer()
    } else {
        startFallbackRenderer()
    }
}
```

```js
if (typeof OffscreenCanvas !== "undefined") { startRenderer(); } else { startFallbackRenderer(); }
```

A feature `with` may appear anywhere, not only near the entrypoint. When every
target supports the feature, codegen drops the check and keeps the first block,
and a lint can suggest removing the `with`.

#### 6.9.5 Rules

- Inference runs against the targets of the package that contains the code.
  Since an app's targets fall within a library's, a feature the library's
  targets provide is provided for the app as well.
- A module that entrypoints with different targets share is checked against the
  widest of their target sets, so its inferred clause holds for each of them.
- A written clause that names a feature the targets already provide is wider
  than it needs to be, which is allowed, as it is for `throws`. A lint can flag
  it.
- A polyfilled API adds no requirement, since the polyfill provides it.
- Versions stay out of `Env`. An environment requirement names kinds of runtime,
  and a version range applies per entrypoint. A function's type does not change
  when an app raises its minimum browser version, except to lose feature
  requirements.

## 7. Alternatives considered

### 7.1 Ship environments first, then lift them into coeffects

Environments ship as designed, per entrypoint, and context values follow as a
separate feature. This leaves the language with three unrelated ways to state a
dependency: `throws`, `@env` targets and context values. It fails C1.

### 7.2 A `needs` clause with its own machinery

The surface syntax matches section 6, but `needs` is implemented beside `throws`
instead of on shared rules. Details such as requirement variables and interop
defaults can then be decided differently for each. It fails C1 over time, though
not on the first day.

### 7.3 Capabilities as values

`window`, `document` and `fs` become implicit parameters, and feature detection
becomes acquiring one, as in `if let dom = Env.window() { ... }`. This unifies
environments with context values, but in a different idiom from `throws`, and it
conflicts with the ambient globals every `.d.ts` file assumes.

### 7.4 Dynamic scoping with `AsyncContext`

`with` compiles to `Theme.run(dark, () => { ... })` and a read to `Theme.get()`,
using the TC39 `AsyncContext.Variable` proposal. There is no hidden parameter,
so the JavaScript boundary needs nothing, and values carry across `await`. It
resolves a context where a callback runs, not where it was created, which
contradicts section 6.5. It also depends on a proposal that is not yet standard
and adds a lookup to every read, which fails C8.

## 8. Prior art

- **Hack** calls the feature "contexts and capabilities" and grew it from a
  coeffect proposal. A function lists its contexts in brackets. A function with
  no list gets the `defaults` context, which is a broad set of capabilities but
  not every one, so existing code keeps compiling. `[ctx $f]` makes a function
  require whatever its function-typed parameter `$f` requires. See the
  [introduction](https://docs.hhvm.com/hack/contexts-and-capabilities/introduction),
  [available contexts](https://docs.hhvm.com/hack/contexts-and-capabilities/available-contexts-and-capabilities/)
  and [dependent contexts](https://docs.hhvm.com/hack/contexts-and-capabilities/dependent-contexts-continued/).
- **Haskell** infers implicit parameters (`?logger`) and `MonadReader`
  constraints along with types, so requirement polymorphism is ordinary type
  polymorphism.
- **Scala 3** declares context parameters with `using` and bundles them under
  context-function aliases such as `type Logged[T] = Logger ?=> T`. Its
  experimental `saferExceptions` turns checked exceptions into a `CanThrow[E]`
  capability.
- **Effekt** treats effects as capabilities and makes function arguments
  second-class, so a callback can use its caller's capabilities without
  polymorphism annotations.
- **Kotlin** context parameters are declared in the signature and passed
  implicitly where a scope provides them. They are not inferred.
- **Swift** global-actor isolation such as `@MainActor` is an environment
  requirement, inferred from enclosing types and inherited by closures.

Escalier's design takes inference from Haskell, requirement variables for
extent-bound requirements from its own `throws`, and capture at creation from Effekt.

## 9. Open questions

1. **How does a provider remove a requirement from a variable?** `ThemeProvider`
   needs `R` minus `Theme`. Either the provider gets a built-in typing rule, as
   `catch` has, or subtraction becomes an operation of the shared rules that the
   solver understands on unsolved variables.
2. **What does an exported function with a context requirement look like to
   JavaScript?** The hidden `$ctx` parameter is invisible to a JavaScript
   caller. The choices are a generated wrapper that takes the context values as
   named arguments, or a rule that an export may not leave a context requirement
   unprovided.
3. **What does an imported `.d.ts` function need?** A declaration file states no
   requirements. Reading it as needing nothing is unsound for code that reads
   ambient state. Reading it as needing everything makes every import
   unprovidable.
4. **Is feature detection a provider for environments?** *Decided: no.* An
   environment stays per entrypoint, and a build for each target selects the
   declarations whose `@env` covers it. Detection provides only a feature
   requirement (section 6.9.4), which records whether a target version has an
   API, not which kind of runtime the code is in.
5. **Do inferred requirements print?** Hover and diagnostics could show the
   clause always, only when non-empty, or only on request.
6. **How does a requirement kind declare that it is extent-bound?** Section 6.8
   needs `Render` and a transaction to be extent-bound and a context value to be
   capturable. The choice could be a modifier on the `context` declaration, a
   separate declaration form, or a fixed set of built-in extent-bound kinds.
   The answer also decides whether a library can define its own, as a
   database client would for a transaction.
7. **What syntax meets C9?** A `with` can list several bindings, as in
   `with Theme = dark, Logger = consoleLogger { ... }`, with each binding able
   to use the ones before it. Nothing in section 6 lets a function provide a
   reusable set. The candidates:

   - **`given` on a callback type.** The provider runs a callback with the
     values supplied. It is the only candidate that supplies an extent-bound
     requirement without the callback writing anything, and it adds a clause
     and a subtyping rule.

     ```esc
     fn withTestEnv<T>(seed: number, body: fn () -> T given Clock | Random) -> T {
         with Clock = fixedClock(seed), Random = seeded(seed) { return body() }
     }

     withTestEnv(42, fn () => runSimulation())
     ```

   - **First-class provider values.** A `provide` expression builds a value of
     type `Provides<...>`, a plain record at runtime, and `with` accepts it.
     Bundles compose by listing them in one `with` or by spreading one into
     another. An owned bundle can outlive the call that built it, so it holds
     only capturable requirements. A borrowed bundle, `&Provides<...>`, can
     carry extent-bound ones, since the borrow can't outlive its call.

     ```esc
     fn testEnv(seed: number) -> Provides<Clock | Random> {
         return provide Clock = fixedClock(seed), Random = seeded(seed)
     }

     with testEnv(42), Theme = dark { runSimulation() }
     ```

   - **Named provider declarations.** Declaration syntax for the previous
     candidate, close to Scala's `given` instances. It adds a declaration form
     for something a function returning `Provides` already expresses.

     ```esc
     provider TestEnv(seed: number) {
         Clock = fixedClock(seed)
         Random = seeded(seed)
     }

     with TestEnv(42) { runSimulation() }
     ```

   - **Records keyed by context.** `with` accepts an object whose keys must be
     context names, and bundles compose by object spread. It adds no type, but
     an ordinary object type then doubles as a provider, so a mistyped key
     reports that the name is not a context.

     ```esc
     val testEnv = {Clock: fixedClock(0), Random: seeded(0)}
     with {...testEnv, Theme: dark} { runSimulation() }
     ```

## 10. Non-goals

- Grades or usage counts on requirements, as in Granule. Ownership already
  covers how many times a value is used.
- A capability-security model for third-party code. The design makes network
  and filesystem access visible in types, but it does not sandbox them.
- Changing what the environments design says about which declarations exist
  where. This design changes how that fact is checked, not the fact.

## 11. Related work

- [planning/environments/design.md](../environments/design.md), which this design
  generalizes.
- [planning/explicit_signatures/design.md](../explicit_signatures/design.md) W5,
  which C2 extends to `needs _`.
- [planning/react_and_jsx/requirements.md](../react_and_jsx/requirements.md)
  §4.5, whose context typing section 6.6 replaces.
- [escalier-lang/escalier#2396](https://github.com/escalier-lang/escalier/issues/2396),
  the environment reference check, which becomes the leaf case of `needs`
  inference under C7.
