# M2 Implementation Plan — AST → Simple-sub lowering

> Companion to [`01-milestones.md`](01-milestones.md) (M2 entry) and
> [`02-design-notes.md`](02-design-notes.md) ("AST lowering" section).
> Status legend: `[ ]` not started · `[~]` in progress · `[x]` complete

## 1. Goal & scope

M2 connects the standalone Simple-sub core to real Escalier source. We lower a
useful subset of the `ast` package into Simple-sub `Term`s, add a driver that
runs `lexer → parser → lower → Infer`, and assert inferred (and simplified)
types over `.esc` snippets.

**In scope (per the milestone):**
- A lowering layer mapping `ast.Expr` / `ast.Decl` → Simple-sub `Term`.
- Coverage for: literals, identifiers, lambdas/functions, application, `let` /
  `letrec` bindings, records, field access, conditionals.
- A driver returning the inferred type for a source string.
- Golden tests over `.esc` snippets, including the Simple-sub paper examples
  translated to Escalier syntax.
- Error reporting (unbound variables, failed constraints) carrying source spans.

**Out of scope (later milestones):**
- Type annotations / ascription (M3).
- Variants and pattern matching beyond plain records (M4).
- Recursive types surfaced as named types, full `let` generalization polish (M5).
- Effects / throws / async (M6).
- Mapping `simplesub.Type` → `type_system.Type`, checker/codegen wiring (M7).
- Diagnostics polish and provenance threading (M8).

The lowering must sit **behind a small interface** so M7 can widen AST coverage
without rewriting the core (design-notes "AST lowering").

## 2. Current state (what M2 builds on)

- **Spike core** lives in `internal/simplesub/` (commit #676): `term.go`,
  `types.go`, `infer.go`, `simplify.go`, `builtins.go`, `errors.go`.
- **Public API today:** `func Infer(term Term) (Type, error)` — fresh context per
  call, returns a coalesced surface `Type`. (`infer.go`)
- **`Term` ADT (curried):** `Lit{Value int}`, `Var{Name}`, `Lam{Name, Body}`,
  `App{Func, Arg}`, `Rcd{Fields}`, `Sel{Term, Name}`,
  `Let{IsRec, Name, Body, Rest}`. (`term.go`)
- **Default environment** (`builtins.go`): `bool`, `+`, `if`, etc.
- **Escalier AST expression nodes** (`internal/ast/expr.go`): `LiteralExpr`,
  `IdentExpr`, `FuncExpr` (`Fn *FuncLit`, with `FuncSig` + body, **n-ary** params),
  `CallExpr` (`Callee`, `Args []Expr`), `ObjectExpr` (`Elems []ObjectExprElem`),
  `MemberExpr`, `IfElseExpr`, `BinaryExpr`, `TupleExpr`, `ArrayExpr`, …
- **Decls:** `VarDecl`, `FuncDecl`, `TypeDecl`, `ClassDecl`.
  **Stmts:** `DeclStmt`, `ExprStmt`, `ReturnStmt`.

### Dependency on M1

M2's deliverables ("a clean API the lowering can depend on") assume M1 has
stabilized the core API. M1 is not yet started and M0 is in progress. Two viable
sequencings:

- **Preferred:** land M1's API freeze first (at minimum the items in §6 PR-0),
  then build M2 against it.
- **Pragmatic (parallel):** start M2 against the current M0 `Infer`/`Term`
  surface, but treat any churn in that surface as M1 work and keep the lowering's
  dependency on the core narrow (only `Term` constructors + `Infer`). This plan
  assumes the pragmatic path with an explicit "API touch-ups" PR (PR-0) that can
  be folded into M1 if M1 lands first.

## 3. Design

### 3.1 Package layout

```
internal/simplesub/
  lower/
    lower.go        # Lowerer + Expr/Decl/Stmt → Term entry points
    lower_expr.go   # per-expression-kind lowering
    lower_decl.go   # VarDecl/FuncDecl → Let/letrec
    builtins.go     # surface-name → builtin Var mapping shared with the env
    errors.go       # LowerError (unsupported node, unbound name) with spans
    driver.go       # InferSource(src) — lexer→parser→lower→Infer
    lower_test.go
    driver_test.go
  testdata/
    *.esc           # paper examples + Escalier-specific snippets
```

Rationale for a sub-package (`internal/simplesub/lower`) rather than living in
`internal/simplesub`: it keeps the core free of any `ast`/`parser` dependency
(the core stays a pure algorithm package), and it gives M7 a clear seam — the
checker integration can provide an alternative lowering target without importing
this package. The "small interface" is the `Lowerer` type below.

### 3.2 The lowering interface

```go
// Lowerer translates a subset of the Escalier AST into Simple-sub Terms.
type Lowerer struct {
    // builtins maps surface operator/keyword names to the Var names bound in
    // simplesub's default environment (e.g. "+" -> "+", "if" -> "if").
    // span side-table: Term has no span field, so we record spans here keyed
    // by Term pointer for error reporting.
    spans map[simplesub.Term]ast.Span
    errs  []LowerError
}

func (l *Lowerer) LowerExpr(e ast.Expr) simplesub.Term
func (l *Lowerer) LowerDecls(decls []ast.Decl, body simplesub.Term) simplesub.Term
func (l *Lowerer) Errors() []LowerError
```

`Term` has no provenance/span field today (deliberately — spans are "added in
integration" per design-notes `errors.go`). M2 keeps `Term` clean and records
spans in a **side table** keyed by `Term` pointer. This is enough to attach
spans to lowering errors and, once the core surfaces constraint failures with a
`Term` reference, to inference errors too.

### 3.3 AST → Term mapping (M2 subset)

Following design-notes "AST lowering":

| Escalier AST | Simple-sub `Term` | Notes |
|--------------|-------------------|-------|
| `LiteralExpr` (number) | `Lit` | see §3.4 — `Lit.Value` is `int` today |
| `LiteralExpr` (string/bool) | `Lit` / builtin `Var` | needs core support (§3.4) |
| `IdentExpr` | `Var` | unbound → `LowerError` with span |
| `FuncExpr` (1 param) | `Lam` | direct |
| `FuncExpr` (n params) | curried `Lam` | `\a b. e` ⇒ `Lam a (Lam b e)` |
| `CallExpr` (1 arg) | `App` | direct |
| `CallExpr` (n args) | curried `App` | `f x y` ⇒ `App (App f x) y` |
| `ObjectExpr` | `Rcd` | shorthand/spread/computed → unsupported error in M2 |
| `MemberExpr` (`.field`) | `Sel` | computed/optional access → unsupported in M2 |
| `BinaryExpr` | `App (App (Var op) l) r` | op resolved via builtins env |
| `IfElseExpr` | `App` of `if` builtin (or dedicated `If`) | see §3.5 |
| `VarDecl` (value) | `Let{IsRec:false}` | §3.6 |
| `FuncDecl` | `Let{IsRec:true}` | recursion allowed |

Block bodies (`FuncLit` body, `IfElseExpr` arms) are sequences of statements
ending in an expression. M2 lowers a block by folding its leading `DeclStmt`s
into nested `Let`s wrapping the final `ExprStmt`/`ReturnStmt` value (§3.6).

Nodes **explicitly unsupported in M2** (emit a clear `LowerError`, not a panic):
`ArrayExpr`/`TupleExpr`, `MatchExpr`, `AwaitExpr`, `AssignExpr`, JSX,
`TemplateLitExpr`, `TypeCastExpr` (M3), `IndexExpr`, `ClassDecl`, `TypeDecl`.

### 3.4 Literals (core touch-up — feeds PR-0 / M1)

`Lit.Value` is `int`. Escalier literals are number/string/bool. Minimum viable
options, in preference order:

1. **Extend the env, not the term:** keep `Lit` for numbers; lower `true`/`false`
   to builtin `Var`s (`true`/`false : bool`) and strings to a `str`-typed
   builtin or a new `Lit` payload. Smallest blast radius if strings are deferred.
2. **Generalize `Lit`:** change `Lit.Value` to a tagged payload
   (`Kind: Int|Str|Bool`) and add `int`/`str`/`bool` primitives to the core.

Recommendation: do (2) as a small, well-scoped change because string/bool
literals appear in nearly every realistic snippet and option (1) leaks encoding
into the lowering. This is a **core change**, so it belongs in PR-0 (or M1) and
is a prerequisite for the records/conditionals PRs.

### 3.5 Conditionals

Two encodings (design-notes leaves this open):
- **Builtin `if`:** `if : bool -> 'a -> 'a -> 'a`, lowered as
  `App(App(App(Var "if", cond), then), else)`. Reuses the existing `builtins.go`
  `if`. Zero core change.
- **Dedicated `If` term:** add `If{Cond, Then, Else}` to the `Term` ADT and type
  it directly in `infer.go`.

Recommendation: **start with the builtin `if`** (zero core change, matches the
reference's approach and the existing `builtins.go`). Revisit a dedicated `If`
only if branch-type precision or error messages demand it.

### 3.6 Decls, blocks, and `letrec`

- A module / block is a list of statements. Lower it right-to-left: the trailing
  expression is the result `Term`; each preceding `DeclStmt` becomes a `Let`
  whose `Rest` is the already-lowered remainder.
- `FuncDecl` ⇒ `Let{IsRec: true}` so self-recursion type-checks.
- `VarDecl` binding a `FuncExpr` ⇒ also `IsRec: true` (matches the milestone's
  "`isrec` for functions"); other `VarDecl`s ⇒ `IsRec: false`.
- Destructuring patterns in `VarDecl` are **out of scope** for M2 (single-name
  bindings only); emit `LowerError` otherwise.

### 3.7 Driver

```go
// InferSource lowers a single Escalier expression/module source and returns the
// inferred, simplified type, or the first lowering/inference error.
func InferSource(src string) (simplesub.Type, error)
```

Pipeline: `lexer_util` → `parser` → collect parse diagnostics (fail fast on
parse errors) → `Lowerer.LowerExpr` / `LowerDecls` → check `Lowerer.Errors()` →
`simplesub.Infer`. Errors are wrapped so the caller sees span + message. Reuse
the existing fixture/parse harness conventions where practical, but the driver
itself is a thin, testable function.

### 3.8 Errors & spans

- `LowerError{Span ast.Span, Msg string}` for unbound names and unsupported
  nodes. Assert the **full** message in tests (CLAUDE.md convention).
- Inference errors from the core (`errors.go`) currently lack spans. M2 attaches
  a span by mapping the failing `Term` back through the side table when the core
  exposes the offending `Term`; if it doesn't yet, that hook is a small core
  addition tracked in PR-0/M1. Until then, inference errors surface message-only
  and span-from-root.

## 4. Testing strategy

- **Lowering unit tests** (`lower_test.go`): parse a snippet, lower it, assert
  the resulting `Term` tree via `snaps.MatchInlineSnapshot` (per CLAUDE.md, snapshot
  tree-shaped output rather than drilling field-by-field).
- **Driver / golden tests** (`driver_test.go`): table-driven, `.esc` snippet →
  expected simplified type string. Include:
  - All Simple-sub paper examples, translated to Escalier syntax (milestone exit
    criterion). Track each against its reference-expected type.
  - Escalier-flavored snippets: records, field access, curried calls,
    `if`/ternary, recursive `FuncDecl`.
  - Error cases: unbound variable, unsupported node, failed constraint — assert
    full message + span.
- Use `testify/require`. Keep snippet inputs as Escalier source and expected
  types as Escalier type-annotation strings (CLAUDE.md).
- Run with `UPDATE_SNAPS=true` on first authoring.

## 5. Sequencing

```
PR-0  core API touch-ups (literals, error/term hook)   ── prereq, foldable into M1
        │
        ▼
PR-1  lowering skeleton + driver (lits, idents, lambda, app)
        │
        ▼
PR-2  records + field access            ┐ (PR-2 and PR-3 are
PR-3  conditionals + binary operators   ┘  independent; either order / parallel)
        │
        ▼
PR-4  decls, blocks, letrec
        │
        ▼
PR-5  paper-example golden suite + error/span polish  ── closes M2 exit criteria
```

PR-1 must land first (it establishes the `Lowerer`, driver, and test harness).
PR-2 and PR-3 are independent and can be developed in parallel once PR-1 is in.
PR-4 depends on the expression coverage from PR-1–PR-3. PR-5 is the
exit-criteria gate.

## 6. PR breakdown

### PR-0 — Core API touch-ups (prerequisite; fold into M1 if M1 lands first)
- Generalize `Lit` to carry `int | string | bool` and add the matching
  primitives + builtins (`true`, `false`, string prim). (§3.4)
- Add a hook so inference errors can reference the offending `Term` (enables
  span attachment in M2). If infeasible cheaply, document the gap and ship
  message-only inference errors.
- Tests: extend the core's existing example tests for the new literal kinds.
- **Exit:** core builds, `Infer` handles bool/string literals, existing tests green.

### PR-1 — Lowering skeleton + driver
- New `internal/simplesub/lower` package: `Lowerer`, span side-table,
  `LowerError`, `LowerExpr` for `LiteralExpr`/`IdentExpr`/`FuncExpr`/`CallExpr`
  (with currying), and `InferSource` driver.
- Unsupported nodes return structured `LowerError` (no panics).
- Tests: lowering snapshots for the four node kinds; driver round-trips
  `(fun x -> x)` / identity, application, and an unbound-variable error.
- **Exit:** `InferSource` infers types for the lambda-calculus subset; errors
  carry spans.

### PR-2 — Records & field access
- Lower `ObjectExpr` → `Rcd`, `MemberExpr` → `Sel`.
- Reject shorthand/spread/computed members with `LowerError`.
- Tests: record literal type, field selection, row-polymorphic function
  (`fun r -> r.x`), missing-field constraint failure.
- **Exit:** record snippets infer expected structural types.

### PR-3 — Conditionals & binary operators
- Lower `IfElseExpr` via the `if` builtin (§3.5); lower `BinaryExpr` via builtin
  operator `Var`s.
- Ensure both `if`/`else` and ternary forms route through the same path.
- Tests: `if`-expression type (join of branches), arithmetic/comparison
  operators, branch-type-mismatch behavior.
- **Exit:** conditional and operator snippets infer expected types.

### PR-4 — Decls, blocks, and `letrec`
- Lower `VarDecl`/`FuncDecl` and statement blocks → nested `Let` (§3.6), with
  `IsRec` for functions.
- Single-name bindings only; destructuring → `LowerError`.
- Tests: `let` polymorphism (`let id = fun x -> x in (id 1, id true)`-style),
  recursive function, sequenced bindings in a block.
- **Exit:** multi-binding modules infer end-to-end; `let` generalization works.

### PR-5 — Paper examples + error/span polish
- Add `testdata/*.esc` for every Simple-sub paper example with its expected
  simplified type; wire into the golden table.
- Tighten error messages and ensure unbound-variable and failed-constraint
  errors report correct spans.
- Update milestone status in `01-milestones.md` (`M2` → `[x]`).
- **Exit (M2 exit criteria):** paper examples infer expected types; errors are
  reported with source spans.

## 7. Risks & mitigations

- **M1 not done:** M2's API dependency may churn. *Mitigation:* keep the
  lowering's dependency on the core to `Term` constructors + `Infer`; isolate any
  core change in PR-0 so it can merge into M1.
- **`Term` has no span:** can't attach spans natively. *Mitigation:* span
  side-table keyed by `Term` pointer; add a core error→Term hook in PR-0.
- **Literal encoding leak:** deferring string/bool support would push encoding
  hacks into the lowering. *Mitigation:* generalize `Lit` up front (PR-0, §3.4).
- **Currying vs n-ary divergence from eventual checker types:** curried lowering
  produces curried function types that won't match Escalier's n-ary `FuncType`.
  *Mitigation:* acceptable for M2 (types asserted in simplesub's own surface);
  flag n-ary `Lam`/`App` as an M7 decision point (design-notes already notes
  this).
- **Scope creep from richer AST nodes:** *Mitigation:* explicit "unsupported in
  M2" list (§3.3) that fails with a clear error rather than partial handling.

## 8. Resolved decisions (milestone "open questions")

- **Reuse `ast` vs. dedicated surface syntax?** → Reuse the existing `ast`
  package; no new surface syntax (design-notes "AST lowering" already commits to
  this).
- **Where does lowering live so it's shareable with checker integration?** →
  `internal/simplesub/lower`, behind the `Lowerer` interface, so the core stays
  AST-free and M7 can supply richer coverage against the same seam.

## 9. M2 exit checklist

- [ ] Lowering covers literals, identifiers, lambdas/functions, application,
      `let`/`letrec`, records, field access, conditionals.
- [ ] `InferSource` driver runs lexer → parser → lower → `Infer`.
- [ ] Golden tests assert inferred + simplified types over `.esc` snippets.
- [ ] Simple-sub paper examples have Escalier equivalents inferring expected types.
- [ ] Unbound variables and failed constraints report errors with source spans.
- [ ] `01-milestones.md` M2 status set to `[x]`.
