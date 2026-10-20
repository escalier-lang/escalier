# Typed holes

## Summary

A typed hole is a placeholder for an expression the author hasn't written yet.
The checker infers the type the surrounding code expects at that spot and
reports it, along with the bindings in scope. The program still compiles, and
the generated JavaScript throws if execution reaches the hole.

```esc
enum Shape {
    Circle(r: number),
    Rect(w: number, h: number),
}

fn area(s: Shape) -> number {
    return match s {
        Shape.Circle(r) => Math.PI * r * r,
        Shape.Rect(w, h) => ?rect,
    }
}
```

```
lib/index.esc:9:29: hole ?rect : number
  in scope:
    w: number
    h: number
    s: Shape
```

Holes let an author write a program top-down. They sketch the structure,
leave holes where details go, and fill each hole using the type the checker
reports for it.

## Prior art

| Language | Syntax | Compiles? | What it reports |
|---|---|---|---|
| Haskell (GHC) | `_`, `_name` | Error by default. `-fdefer-typed-holes` makes it a warning plus a runtime error. | Expected type, relevant bindings, valid hole fits |
| Agda | `?`, `{! expr !}` | Yes, as interactive goals | Goal type and context. The editor can refine, case-split and auto-fill. |
| Idris 2 | `?name` | Yes | Type and context, queried by name. The editor can case-split and search. |
| PureScript | `?name` | No | Expected type and bindings in scope |
| Lean 4 | `_`, `?_`, `sorry` | `sorry` compiles with a warning | Goal state |
| Hazel | Empty and non-empty holes | Yes, and evaluation continues around holes | Expected type. A non-empty hole wraps an ill-typed expression. |
| Gleam | `todo`, `todo as "msg"` | Yes, with a warning | Expected type, in the warning |
| Rust, Scala, Elm | `todo!()`, `???`, `Debug.todo` | Yes | Nothing. Each is a bottom-typed expression. |

Two ideas from this list shape the design:

- A named hole, as in Idris and PureScript, gives the editor and the CLI a stable
  handle to report and jump to.
- A hole that compiles with a warning, as in Gleam and Idris, lets the author run
  the parts of the program that are finished.

## Goals

1. Report the expected type of every hole and the value bindings in scope at it.
2. Show the same report from the CLI and from the LSP.
3. Compile a program that contains holes, and make the output throw at a hole
   that is reached.
4. Leave inference of the surrounding code unaffected, so a hole doesn't change the
   inferred type of anything else in a misleading way.

## Non-goals

- Interactive refinement such as Agda's give, refine and case-split. Code actions
  can add these later. See [phases](#phases).
- Holes in type position. `_` already fills this role as the `WildcardTypeAnn`
  ([internal/solver/type_ann.go](../../internal/solver/type_ann.go)), which asks
  the checker to infer a type without reporting it.
- Proof search or automatic program synthesis.

## Syntax

### Anonymous and named holes

```esc
val x: number = ?          // anonymous hole
val y: number = ?total     // named hole
```

A hole is a primary expression, so it can appear anywhere an expression can and
it binds tighter than any operator. In `?cfg.port` the hole is `?cfg`, and the
member read constrains it to an object type with a `port` key.

A hole name follows the identifier rules. The name must follow `?` with no
whitespace between them. `? total` is an anonymous hole followed by the
identifier `total`, which is a parse error.

Hole names live in their own namespace. `?total` doesn't refer to a binding named
`total` and doesn't conflict with one. A name is a label and doesn't have to be
unique. Two holes named `?todo` are separate holes, and each gets its own report.
Every report includes the hole's location, so a repeated name is never
ambiguous. GHC treats `_name` holes the same way. The checker numbers anonymous
holes by source position, as `?_1`, `?_2`, and so on, for reporting only.

### Non-empty holes

```esc
val total: number = ?(items.length + tax)
val total: number = ?sum(items.length + tax)
```

A non-empty hole wraps a draft expression. The checker infers the inner
expression's type and reports it beside the expected type, and it reports whether
the inner type fits. A mismatch inside the hole is part of the report and not an
error, so the author can try candidate expressions without breaking the build.
This is Agda's `{! expr !}` and Hazel's non-empty hole.

### Why `?`

- `?` never starts an expression today. It appears only after an object key or a
  parameter as the optional marker, and inside the `?.`, `?(` and `?[` tokens,
  which all follow an operand. So `?` at the start of an expression is free.
- `_` is already the wildcard pattern, the wildcard type annotation and a valid
  object key. Using it for expression holes, as Haskell does, would collide with
  patterns in destructuring positions.
- `...` would read naturally to TypeScript users, but it collides with spread and
  rest.
- `todo` reads naturally too, but it would claim a keyword or a prelude name, and
  it has no short form for a named hole.

### Parsing

The lexer already produces `Question`, `QuestionOpenParen` and
`QuestionOpenBracket` ([internal/parser/lexer.go](../../internal/parser/lexer.go)).
The expression parser adds one primary-expression case:

1. `Question` followed immediately by an `Identifier` is a named hole. If an
   `OpenParen` follows the name immediately, it's a named non-empty hole.
2. `Question` otherwise is an anonymous hole.
3. `QuestionOpenParen` at the start of an expression is an anonymous non-empty
   hole.

The parser checks adjacency by comparing spans. `QuestionOpenBracket` at the
start of an expression stays a parse error.

The AST gets one node:

```go
type HoleExpr struct {
    Name  string // "" for an anonymous hole
    Inner Expr   // nil for an empty hole
    span  Span
}
```

The AST visitor in [internal/ast/visitor.go](../../internal/ast/visitor.go) visits
`Inner` when present. The printer prints the node back in its source form.

## Typing

### The expected type

The checker gives each hole a fresh type variable α and records the hole in
`Info`, together with the scope it sits in. The surrounding code then constrains α
as it would any other expression. Each use of the hole's value adds an upper bound
to α. For example, `val y: number = ?total` adds `number`, and `?cfg.port` adds
`{port: β}`.

The expected type is α coalesced in negative position. `coalesce` already defines
this as the intersection of α's upper bounds, which is `unknown` when α has none
([internal/solver/coalesce.go](../../internal/solver/coalesce.go)). An `unknown`
expected type means nothing constrains the hole, and the report says so in
words:

```
lib/index.esc:3:12: hole ?x is unconstrained
```

### Effect on the surrounding inference

α has no lower bounds, so it coalesces to `never` in positive position. This
gives the right outward type without special handling:

```esc
fn parse(s: string) {
    return ?parse
}
// inferred: fn (s: string) -> never
```

A function whose only return is a hole does return `never`, because the hole
throws. A hole in one arm of a `match` contributes nothing to the union of arm
types, so `match` keeps the type of its finished arms.

### When the report is computed

The checker computes each hole's report after the enclosing top-level
declaration finishes inference and generalization. Upper bounds can still arrive
up to that point, for example from a call later in the same function body. A
hole inside a generic function reports in terms of that function's type
parameters.

Speculative trials need care. An overload trial or a union-commit trial can
infer the same hole more than once. `hasHardError` ignores warnings when it
decides whether a trial succeeded
([internal/solver/errors.go](../../internal/solver/errors.go)), so a hole never
causes a trial to fail. The checker records holes per AST node and reports only
the inference on the branch it commits to, so each hole produces exactly one
report.

### Non-empty holes

For `?name(e)` the checker infers `e : τ` in the hole's scope. It doesn't add
`τ <: α`, so a draft that doesn't fit can't produce a type error or narrow the
surrounding inference. After coalescing, it runs `τ <: expected` as a trial
constraint and reports the outcome:

```
lib/index.esc:5:25: hole ?sum : number
  contains: string
  string is not assignable to number
  in scope:
    items: Array<Item>
    tax: string
```

Errors inside `e` itself, such as an unbound name, are ordinary errors. A hole
suspends the fit check, not the rest of the checker.

### Bindings in scope

The report lists the value bindings visible at the hole, innermost first, with
their types coalesced for display. This list shows the hole's context, so it
holds only local bindings. The prelude and imported namespaces would bury that
context under hundreds of names. Prelude and imported values still appear as
[hole fits](#hole-fits) when their types fit. The LSP's `scopeView`
([cmd/lsp-server/scope_view.go](../../cmd/lsp-server/scope_view.go)) already
walks a solver scope level by level for completions. The hole report reads the
same scope structure, so the walk lives in the solver and the LSP reuses it.

### Hole fits

A hole fit is a value whose type fits the hole's expected type. The checker
tests every value visible at the hole:

- local bindings,
- the module's declarations,
- imports,
- the prelude, including namespace members such as `Math.sqrt`.

A generic value fits when some instantiation of it fits. `identity` fits
`fn (x: number) -> number`, for example. The report lists fits in the order
above, so the values nearest the hole come first.

```esc
fn hypot(a: number, b: number) -> number {
    return ?root(a * a + b * b)
}
```

```
hole ?root : fn (arg0: number) -> number
  in scope:
    a: number
    b: number
  fits:
    Math.sqrt
    Math.abs
    Math.cbrt
    ... and 21 more
```

The CLI shows the first ten fits and a count of the rest. The LSP returns all of
them, since an editor menu scrolls. An unconstrained hole has no fits list,
because every value fits it.

## Holes in larger programs

The examples below show what a hole reports when inference has to work harder.
Most expected types were checked against the current solver. In each check an
unannotated parameter stood in for the hole, because the checker infers a
parameter's type from its uses the same way it would infer a hole's. The
generic-function, `compose` and let-polymorphism examples weren't checked this
way. Their reports follow from how annotations and generalization work. The reports
are rendered in the format this doc proposes.

Some examples also turn up a requirement the basic design misses. Each of those
ends with a paragraph that states the requirement.

### The type comes from later uses

A hole bound to a name learns from every later use of that name.

```esc
declare fn listen(port: number) -> boolean
declare fn log(msg: string) -> boolean

fn start() {
    val cfg = ?cfg
    listen(cfg.port)
    log(cfg.host)
}
```

```
hole ?cfg : {host: string, port: number}
```

Neither use mentions the whole object type. Each member read adds one upper
bound, and the intersection of the two is the record the author has to build.

This works only if `val cfg` doesn't generalize the hole's type. A generalized
binding gives each use a fresh copy of the type, so no use would reach the hole.
A hole isn't a syntactic value, so it must fall under the value restriction the
same way a call does.

### Callbacks

A hole in a callback's body learns from the call that receives the callback, and
the callback's parameters get their types from the same call.

```esc
declare fn map<T, U>(xs: Array<T>, f: fn (x: T) -> U) -> Array<U>

val prices: Array<number> = [3, 4]
val labels: Array<string> = map(prices, fn (p) { return ?label })
```

```
hole ?label : string
  in scope:
    p: number
    prices: Array<number>
```

The constraint travels three steps. `labels` fixes `U` to `string`, `U` is the
callback's return type, and the hole is the callback's return value. `p` has no
annotation, but `map` gives it the type `number`.

A hole can also stand for the whole callback. The report is then the full
signature the call wants:

```esc
val labels: Array<string> = map(prices, ?toLabel)
```

```
hole ?toLabel : fn (x: number) -> string
```

The same holds for a callback whose type parameter appears in more than one
place:

```esc
declare fn fold<T, A>(xs: Array<T>, init: A, f: fn (acc: A, x: T) -> A) -> A

val names: Array<string> = ["ada", "grace"]
val total: number = fold(names, 0, ?step)
```

```
hole ?step : fn (acc: number, x: string) -> number
```

The solver's raw answer is `fn (acc: A | 0, x: string) -> A & number`. That type
is correct, but it reads poorly. The report has to replace each type variable
that the instantiation solved with its solution, so `A` becomes `number`.

### Generic functions

A hole passed to a generic function gets its type from what the call's result
must be:

```esc
fn identity<T>(x: T) -> T { return x }

val n: number = identity(?x)
```

```
hole ?x : number
```

A hole inside a generic function reports in terms of that function's type
parameters:

```esc
fn first<T>(xs: Array<T>) -> T {
    return ?first
}
```

```
hole ?first : T
  in scope:
    xs: Array<T>
```

### Curried functions and function position

When a curried function's type is annotated, a hole at any level gets the rest
of the signature:

```esc
val add: fn (a: number) -> fn (b: number) -> number = fn (a) {
    return ?addA
}
```

```
hole ?addA : fn (b: number) -> number
  in scope:
    a: number
```

A hole in function position gets a signature built from its call. The author
writes the call first and asks what function would fit it:

```esc
val parse = fn (a: number) { return a }
val show = fn (b: string) { return b }
val ok: boolean = ?combine(parse)(show)
```

```
hole ?combine : fn (arg0: fn (a: number) -> number) -> fn (arg0: fn (b: string) -> string) -> boolean
```

The two calls make the hole a curried function. Each call contributes one
parameter, and the annotation on `ok` fixes the final result.

A hole also helps write a combinator. The expected type and the bindings in
scope narrow the body down to one sensible choice:

```esc
fn compose(
    f: fn (b: string) -> boolean,
    g: fn (a: number) -> string,
) -> fn (n: number) -> boolean {
    return ?composed
}
```

```
hole ?composed : fn (n: number) -> boolean
  in scope:
    f: fn (b: string) -> boolean
    g: fn (a: number) -> string
```

The only way to get from `number` to `boolean` with `f` and `g` is
`fn (n) { return f(g(n)) }`.

The return annotation on `compose` is what gives the hole its type. Without it,
the checker generalizes `compose` before any call site is checked, and a call
site can't reach the hole. [Let-polymorphism](#let-polymorphism) explains why.

### Mutual recursion

Mutually recursive functions are inferred together and generalized together. A
hole in one function can therefore learn from a sibling function's annotation:

```esc
fn isEven(n: number) -> boolean {
    return if n == 0 { true } else { isOdd(n - 1) }
}

fn isOdd(n) {
    return if n == 0 { ?base } else { isEven(n - 1) }
}
```

```
hole ?base : boolean
  in scope:
    n: number
```

`isOdd` has no annotation, but its result flows into the return of `isEven`,
which does have one.

Without the annotation on `isEven`, nothing constrains the hole. The hole's
value flows into the return of `isOdd`, then into the return of `isEven`, and
neither return is used anywhere with a type. Any value fits, so `unconstrained`
is the correct answer. It isn't a helpful one, though, because the other branch
already shows that the author wants a `boolean`.

The report should add the other values that flow into the same place. The
checker collects them by walking from the hole's type variable to each variable
it flows into and reading those variables' lower bounds:

```
hole ?base is unconstrained
  flows into: the return value of isOdd
  alongside: true
```

The diagnostic gains an `Alongside` field for this.

### Narrowing around a hole

A hole's outward type is `never`, so it adds nothing to the types around it.
Those types are therefore narrower than they will be once the hole is filled.
With the unannotated version of the example above, the checker infers:

```esc
// inferred: fn (n: number) -> true
fn isOdd(n) {
    return if n == 0 { ?base } else { isEven(n - 1) }
}
```

Code that calls `isOdd` sees `true` and can type-check against it. That code can
then fail once the hole is filled with `false`. This is the cost of keeping a
hole out of inference, and it's the same cost `throw` has today. The goal list
lets the author see which signatures still depend on a hole. The LSP's hover on
`isOdd` should also say that its type is provisional.

### Let-polymorphism

A generic function is generalized once, and each call gets a fresh copy of its
type. Constraints from a call site therefore never reach a hole inside the
function:

```esc
fn wrap(x) {
    return {value: x, extra: ?extra}
}

val w: {value: number, extra: string} = wrap(1)
```

```
hole ?extra is unconstrained
```

The call site's `string` constrains a copy of the hole's type, not the hole.
That's correct, because `wrap` has to work for every caller. A future report
could still list the types call sites expect as a hint, with the uses labeled
as call sites.

### Overloads

A hole has no lower bounds, so it fits every overload's parameter. The checker
commits to the first overload that accepts the arguments, which is always the
first one:

```esc
declare fn pick(x: number) -> string
declare fn pick(x: string) -> number

val r: number = pick(?x)
```

The checker reports `cannot constrain string <: number` today. It picks the
first overload, and that overload's `string` result doesn't match `r`. The second
overload would have worked.

When an argument is a hole, overload resolution should leave that argument out
of the decision. It should pick by the other arguments and the expected result,
and report the remaining candidates when more than one is left:

```
hole ?x : string
```

```
hole ?x : number | string
  candidates:
    pick(x: number) -> string
    pick(x: string) -> number
```

The first report is for `val r: number = pick(?x)`. The second is for a bare
`pick(?x)`, where nothing narrows the choice. The diagnostic gains a
`Candidates` field for this.

### Throws

A hole lowers to a `throw`, but it must not add to the function's thrown type.
A function that throws a value it doesn't declare gets an error today:

```
`isOdd` can throw `"hole"`, but it doesn't declare it; add `throws string` to `isOdd`'s signature
```

If a hole added to the thrown type, every function containing one would get
this error, and the error would hide the hole's own report. The checker treats
the hole's runtime error as a panic outside the `throws` system. The type system
never sees the error a hole throws, so a hole can't cause a type error anywhere
else in the program.

## Diagnostics

### The diagnostic type

The solver produces one `HoleDiagnostic` per hole:

```go
type HoleDiagnostic struct {
    Hole       *ast.HoleExpr
    Name       string              // the source name, or "_1", "_2", ...
    Expected   soltype.Type        // nil when the hole is unconstrained
    Contains   soltype.Type        // the inner type of a non-empty hole, or nil
    FitErr     SolverError         // why Contains doesn't fit, or nil
    Bindings   []HoleBinding       // innermost scope first
    Alongside  []soltype.Type      // other values that flow where the hole's value flows
    Candidates []*soltype.FuncType // overloads still open when the hole is an argument
    Fits       []HoleFit           // values whose type fits, nearest scope first
}

func (e *HoleDiagnostic) IsWarning() bool { return true }
```

`Message()` returns the first line, such as `hole ?rect : number`. The CLI and the
LSP format the rest from the structured fields. `Info` also exposes the holes it
recorded so the LSP can answer hover and goal-list requests without scanning
diagnostics.

### CLI

`printErrors` and `formatTypeError` in
[cmd/escalier/build.go](../../cmd/escalier/build.go) print a hole the way they
print any type diagnostic, with the source excerpt and the location highlighted.
The `in scope:` and `contains:` lines follow the excerpt.

The build counts every entry in `TypeErrors` as a failure
(cmd/escalier/build.go:241), so a hole fails the build. The build still writes
the generated JavaScript first, so a program with holes can run up to its first
hole. Making it configurable whether warnings fail the build is
[deferred](#deferred).

`compiler.Diagnostic` gets an `IsWarning()` method, or the compiler splits
warnings into their own list. The CLI labels warnings as warnings, and the LSP
maps them to a lower severity.

### LSP

The LSP reports holes in four ways:

- **Diagnostic.** Each hole gets a diagnostic whose message is the first line.
  The bindings and the fit result go in `relatedInformation`. The LSP hard-codes
  `DiagnosticSeverityError` today (cmd/lsp-server/text_document.go:590 and :670),
  so it needs to map `IsWarning()` to a lower severity. Holes use `Information`
  so they don't look like mistakes.
- **Hover.** `textDocumentHover` shows the full report when the cursor is on a
  hole.
- **Goal list.** Document symbols list each hole by name with its expected type,
  so the editor's outline doubles as a to-do list.
- **Code actions, later.** `textDocumentCodeAction` is a stub today. It can offer
  "fill with `w`" for each binding whose type fits, and "case-split" when the
  expected type is a union or an enum.

## Code generation

A hole lowers through the same path as `ThrowExpr`
([internal/codegen/builder.go](../../internal/codegen/builder.go)):

```js
throw new Error("Escalier: reached hole ?rect at lib/index.esc:9:29");
```

A non-empty hole doesn't evaluate its inner expression. The inner expression is
a draft that hasn't passed its fit check, and running it could cause side effects
the author didn't intend.

## Testing

- Parser tests cover each syntax form, the whitespace rule, `?` as the optional
  marker next to a hole such as `{a?: ?x}`, and `?cfg.port` precedence. They
  assert the printed AST with inline snapshots.
- Solver tests are table-driven. Each case is Escalier source with the expected
  `HoleDiagnostic` rendered as a string, including unconstrained holes, holes in
  generic functions, holes inside overload calls, and non-empty holes that fit and
  don't fit.
- Solver tests also cover each example in [holes in larger
  programs](#holes-in-larger-programs), asserting the report shown there.
- A fixture under `fixtures/typed_holes/` covers the CLI output and the generated
  JavaScript.
- LSP tests cover severity, hover and document symbols.

## Phases

1. Parse `?` and `?name`, infer the expected type, report it through the CLI with
   the bindings in scope, and generate the throw. Keep holes out of
   generalization and out of the thrown type. Replace solved type variables in
   the report.
2. Fix LSP severity mapping, and add the diagnostic, hover and goal list.
3. Add non-empty holes.
4. Add the `alongside` report, and leave hole arguments out of overload
   resolution with a `candidates` report.
5. Add hole fits and code actions: fill with a binding, case-split, and
   introduce a lambda when the expected type is a function.

## Deferred

- A `--warnings-as-errors` flag and `PackageConfig` field that decide whether
  warnings fail the build. It would default to on, which matches the build
  today. Turning it off would let a program with holes build cleanly.
- A hole-specific setting that fails the build on holes while other warnings
  still pass.
- Holes that share a name and a type, as in Lean 4, where a repeated `?x` is one
  hole and every use constrains it.
