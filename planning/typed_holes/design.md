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
`total` and doesn't conflict with one. A name must be unique within a module, and
a duplicate is an error that points at both holes. Requiring unique names keeps
the CLI report and the LSP goal list unambiguous. The checker numbers anonymous
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
their types coalesced for display. It omits the prelude and imported namespaces
to keep the list short. The LSP's `scopeView`
([cmd/lsp-server/scope_view.go](../../cmd/lsp-server/scope_view.go)) already
walks a solver scope level by level for completions. The hole report reads the
same scope structure, so the walk lives in the solver and the LSP reuses it.

## Diagnostics

### The diagnostic type

The solver produces one `HoleDiagnostic` per hole:

```go
type HoleDiagnostic struct {
    Hole     *ast.HoleExpr
    Name     string            // the source name, or "_1", "_2", ...
    Expected soltype.Type      // nil when the hole is unconstrained
    Contains soltype.Type      // the inner type of a non-empty hole, or nil
    FitErr   SolverError       // why Contains doesn't fit, or nil
    Bindings []HoleBinding     // innermost scope first
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

Two changes are needed in the build command:

1. The build counts every entry in `TypeErrors` as a failure today
   (cmd/escalier/build.go:241). It should count only entries that aren't warnings.
   `compiler.Diagnostic` needs an `IsWarning()` method, or the compiler needs to
   split warnings into their own list. Either change also fixes the existing
   warnings, which fail the build today.
2. A `--deny-holes` flag turns hole warnings into errors so CI can reject
   unfinished code.

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
- A fixture under `fixtures/typed_holes/` covers the CLI output and the generated
  JavaScript.
- LSP tests cover severity, hover and document symbols.

## Phases

1. Parse `?` and `?name`, infer the expected type, report it through the CLI with
   the bindings in scope, and generate the throw. Fix the CLI's warning count.
2. Fix LSP severity mapping, and add the diagnostic, hover and goal list.
3. Add non-empty holes.
4. Add `--deny-holes`.
5. Add hole fits and code actions: fill with a binding, case-split, and
   introduce a lambda when the expected type is a function.

## Open questions

- Should a hole's runtime error be catchable as an ordinary `Error`, or should it
  use a dedicated class so tests can tell holes apart from other failures?
- Should `--deny-holes` be the default for `escalier build` and opt-out for
  development, the way GHC treats holes as errors by default?
- Should hole names be unique per module or per file? Per module matches how the
  checker scopes declarations. Per file is easier to see while editing.
- Should the bindings list include the prelude when the expected type is
  something only a prelude value provides, such as a function from `Math`?
