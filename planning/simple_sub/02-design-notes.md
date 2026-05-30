# 02 — Design notes

Concrete shapes for the pieces the milestones reference. These promote the
spike (`internal/simplesub/`) into production form. Names are
provisional.

## Package layout

```text
internal/solver/            (new top-level package, sibling to internal/checker/; leaf name TBD)
  soltype/                  the type representation (its own; NOT type_system)
    type.go                 Type interface; TypeVar, Primitive, Literal,
                            Function, Tuple, Record, Mut, Union, Intersection,
                            Alias, ... each carrying what it needs
    lifetime.go             Lifetime sort: LifetimeVar (bound lists),
                            StaticLifetime, LifetimeUnion
    print.go                Type -> Escalier annotation string
  constrain.go              constrain(lhs <: rhs), extrusion, lattice rules
  scheme.go                 let-polymorphism: schemes, instantiate, freshenAbove
  simplify.go               occurrence analysis, co-occurrence merging
  coalesce.go               SimpleType -> coalesced soltype.Type (polarity)
  infer.go                  the AST-driven inference walk; Info side table
  scope.go                  Scope / Binding / Namespace (own, not type_system)
  errors.go                 type error representation
```

## `soltype` — the type representation

The clean, algorithm-shaped data model that motivates not reusing
`type_system`. The core difference from `type_system.TypeVarType`:

```go
// type_system today: single mutable cell.
type TypeVarType struct { Instance Type; Constraint Type; ... }

// soltype: bound lists (Simple-sub).
type TypeVar struct {
    id          int
    level       int
    lowerBounds []Type
    upperBounds []Type
}
```

Lifetime-carrying types hold the optional lifetime **from the start** (M4
decision — lifetimes ride on values, introduced with the first carrier):

```go
type Record struct {
    fields map[string]Type
    lt     Lifetime        // nil if it borrows nothing
}
type Mut struct{ inner Type }          // invariance via read/write decomposition
type Tuple struct{ elems []Type; lt Lifetime }
```

Lifetimes are a **second sort** solved by the same machinery:

```go
type LifetimeVar struct { id int; lowerBounds, upperBounds []Lifetime }
type StaticLifetime struct{}           // top of the outlives lattice
```

## `Info` side table (the AST decoupling — option (a))

The AST is never modified. Node→type associations live here, à la
`go/types.Info`. Nodes are always pointers, so pointer-keyed maps work.

```go
type Info struct {
    types map[ast.Node]soltype.Type
    // (later) defs/uses, scopes, etc. as needed by LSP
}
func (i *Info) TypeOf(n ast.Node) soltype.Type { return i.types[n] }
func (i *Info) setType(n ast.Node, t soltype.Type) { i.types[n] = t }
```

The new checker **never** calls `ast` node `InferredType()`/`SetInferredType()`.
The embedded `inferredType` field stays for the old checker until M11 cleanup.

## The constraint-generating AST walk (`infer.go`)

Replaces the spike's hand-built IR. Walks real `ast.Expr`/`ast.Stmt`/`ast.Decl`,
generating constraints and recording results in `Info`. Sketch of the
expression cases (mirrors the spike's `typeTerm`, but over `ast`):

- `*ast.IdentExpr` → instantiate the binding's scheme.
- `*ast.FuncExpr` → fresh var per param (or annotated type via `TypeAnn`),
  infer body, build `Function`; `mut` record params get a fresh lifetime
  (`attachParamLifetimes`).
- `*ast.CallExpr` → `constrain(callee <: Function{args, fresh result})`.
- `*ast.MemberExpr` (read) → `constrain(recv <: Record{field: fresh})`
  (usage-based inference).
- assignment to a member (write) → `constrain(recv <: Mut{Record{field:
  widen(v)}})` and record the written field type for read-after-write.
- `*ast.ObjectExpr`/`*ast.TupleExpr` → `Record`/`Tuple` over inferred elems.
- conditionals/branches → join branch types; for borrowed records, union their
  lifetimes via a fresh join lifetime var.
- Every node: `info.setType(node, result)`.

Module-level: drive declaration order via the existing `dep_graph` (SCC order,
matching how the old checker handles mutual recursion), generalizing at
binding boundaries.

## Scope / Binding (own, not `type_system`)

A `Scope` of `Binding`s owned by the new package, structurally similar to the
old checker's but over `soltype`. This is what the compiler reads back. During
the differential phase the compiler holds either the old or the new scope behind
the flag (a small interface or two fields on `CheckOutput`).

## Conformance corpus format (M7)

Checker-agnostic, encoding the language semantics we *want* (improve, don't
match). One suggested layout — a directory of cases, each either an inline table
or a fixture dir:

```text
testdata/conformance/<category>/<name>.txt
---
source:
  val id = fn (x) { return x }
expect:
  id: fn <T0>(x: T0) -> T0
---
```

Or, for error cases:

```text
source:
  val x: number = "hello"
expect-error:
  <full error message asserted, per CLAUDE.md "assert the full error message">
```

Key properties:
- Expected outputs authored to intended semantics, **not** copied from the old
  checker. Where the new checker improves (e.g. `unknown` vs vacuous `<T0>`), the
  corpus asserts the improved form.
- This *is* the "more comprehensive fixtures" upgrade — organized by language
  feature, with both positive (inferred type) and negative (error) cases.

## Differential harness (M7)

A triage tool, not a conformance gate (since we improve, don't match):

```text
for each fixture/source:
    tree := parse(source)                  // parse ONCE
    oldScope := oldChecker.Infer(tree)     // uses ast.inferredType field
    newScope, info := newChecker.Infer(tree) // uses its own Info side table
    diff := compareRendered(oldScope, newScope)
    bucket(diff): match | intended-improvement | bug
```

Because both checkers annotate the **same** parsed tree into **separate** stores
(the old field vs. the new `Info`), no double-parse and no clobbering — which is
exactly why the side-table approach beats AST generics here. Output a triaged
report; fail CI only on `bug`-bucket (untriaged/unintended) divergences.

## Compiler wiring (M7)

The flag lives at the **3** `checker.NewChecker(ctx)` sites in
`internal/compiler/compiler.go` (`CheckLib`, `Compile`, `CompilePackage`).
the MVP selects the new checker for *checking only* — codegen continues from the old
checker's output (codegen deferred). Simplest form: an env var or build tag
read once and branched at those three sites.

## What gets deleted at M11 (cleanup)

- `internal/checker/` (old package) + its ~38k LoC of tests.
- `internal/ast/ast.go`: `type Type = type_system.Type`.
- The generated `inferredType` field + `InferredType()`/`SetInferredType()` and
  their generation in `tools/gen_ast`.
- `decl.go`'s public `InferredType` field.
- Result: the AST imports `type_system` only for `BindingOwner` (or that moves
  too) — effectively type-system-agnostic.

## Open questions (decide as we go)

1. **Package leaf name** — top-level under `internal/`: `solver/` vs `algsub/` vs other.
2. **`BindingOwner`** — reuse `type_system`'s marker interface, or define the
   new checker's own.
3. **Codegen path (M9)** — bridge (`soltype → type_system`) vs. port codegen.
4. **Error representation** — reuse the old checker's `Error`/diagnostic types,
   or a fresh one (the corpus asserts full messages either way).
5. **Scope sharing in `CheckOutput`** — interface vs. parallel fields during the
   differential phase.
