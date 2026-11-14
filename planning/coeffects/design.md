# Design: Coeffects

## 1. Scope

This document proposes adding coeffects to Escalier. A *coeffect* is a
requirement a computation places on the context it runs in. An *effect* is the
opposite: something a computation does to the world. Escalier already has one
effect in its types, the `throws` clause, which records the exceptions a function
may raise. This design adds a `needs` clause, which records what a function
requires from its caller's context, and builds both clauses on one set of rules.

It covers two kinds of requirement:

- *Context values*, such as a theme, a logger or the current request, which a
  function reads without taking them as parameters.
- *Environments*, the browser global scopes and non-browser runtimes that
  [planning/environments/design.md](../environments/design.md) defines.

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
`catch` removes the exceptions its arms match.

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

React calls a child component after its parent returns, so a requirement belongs
on the element type, not on the call. `<Button/>` has type `Element needs R`,
where `R` is `Button`'s requirement. A context created without a default value
is a requirement, and one created with a default adds none.

```esc
context Theme: ThemeValue                 // React.createContext with no default

fn useTheme() -> ThemeValue needs Theme | Render { ... }

fn Button(props: {label: string}) -> Element needs _ {
    val theme = useTheme()
    return <button class={theme.button}>{props.label}</button>
}
// inferred: fn (props: {label: string}) -> Element needs Theme | Render

fn ThemeProvider<R>(props: {value: ThemeValue, children: Element needs R}) -> Element needs Exclude<R, Theme>

// Root.render accepts only an element whose requirements are environments
fn render(self, el: Element needs Env<window>) -> undefined
```

`Render` is a requirement only a component body has, so calling `useTheme`
outside a component is an error. That replaces the hooks lint rule.

`ThemeProvider` removes a requirement from a requirement variable. `Exclude` in
the prelude is a conditional type, and a conditional type on an unsolved
variable does not reduce during inference. Section 9 lists the two ways out.

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

React contexts compile to React's own API: `context` to `createContext`, reads
to `useContext`, and the provider to `Context.Provider`. They do not use `$ctx`.

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
4. **Is feature detection a provider for environments?** Narrowing on
   `typeof OffscreenCanvas !== "undefined"` could remove the matching
   environment requirement in its branch. The environments design lists this as
   a non-goal, and adopting it reopens that decision.
5. **Do inferred requirements print?** Hover and diagnostics could show the
   clause always, only when non-empty, or only on request.
6. **How does a requirement kind declare that it is extent-bound?** Section 6.8
   needs `Render` and a transaction to be extent-bound and a context value to be
   capturable. The choice could be a modifier on the `context` declaration, a
   separate declaration form, or a fixed set of built-in extent-bound kinds.
   The answer also decides whether a library can define its own, as a
   database client would for a transaction.

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
