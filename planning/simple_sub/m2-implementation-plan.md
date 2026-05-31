# M2 Implementation Plan — Parser/resolver bridge

> Companion to [`01-milestones.md`](01-milestones.md) (the **M2 — Parser/resolver
> bridge** entry) and [`02-design-notes.md`](02-design-notes.md)
> (§"The constraint-generating AST walk", §"Scope / Binding", §"`Info` side
> table"). Status legend: `[ ]` not started · `[~]` in progress · `[x]` complete.

## 1. What M2 is (and is not)

**M2 replaces the spike's hand-built expression IR with a real
constraint-generating walk over `*ast.Module`.** The spike
(`internal/simplesub/`) proved the algorithm against a toy `Term` ADT
(`Lit`/`Var`/`Lam`/`App`/`Let`/…) driven by `typeTerm`. M2 throws that IR away
and drives the *same algorithm* — now living in the M1 package — directly from
real parsed source, resolved through the existing `dep_graph`/`resolver`, with
results recorded in the `Info` side table.

Per the milestone, M2 delivers:

1. **Drive from real source.** `parser.Parse*` → `*ast.Module` →
   `dep_graph`/`resolver` → a constraint-generating AST visitor that produces
   `soltype` types and populates `Info`.
2. **Own `Scope`/`Binding`/`Namespace`.** Analogues owned by the new package,
   *not* reused from `internal/type_system/`.
3. **A fixture-style harness.** Given `.esc` source, infer and assert the
   rendered binding types — its own assertions, independent of the old checker.

**Exit criteria (from the milestone):**
- Top-level `val`/`fn` declarations from real source infer correct rendered
  types end-to-end.
- A multi-file module resolves via the dep graph.

**Gate (from the milestone):** if driving from the real AST/dep-graph requires
reaching back into the old checker's internals, the parallel-package boundary is
wrong — **stop and reassess**.

### Scope boundary against neighbouring milestones

- **M1 (prerequisite — Package skeleton + `soltype`)** must land first. It
  creates the new package (sibling to `internal/checker/`, leaf name TBD —
  `internal/solver/` is the working name), the `soltype` representation
  (bound-list `TypeVar` with `lowerBounds`/`upperBounds` + `level`, `Primitive`,
  `Literal`, `Function`, `Tuple`), `constrain`, levels/extrusion, polarity-driven
  coalescing, the **own** `soltype` printer, and the `Info` side table
  (`map[ast.Node]soltype.Type` + `TypeOf`/`setType`). M2 consumes all of these;
  it does not modify the core algorithm.
- **M2 expression coverage is deliberately shallow.** The milestone's bar is
  "top-level `val`/`fn` infer correct rendered types end-to-end" plus multi-file
  resolution. The *deep* function/application/let-polymorphism work — and its
  acceptance cases (`TopLevelLetPolymorphism`, `IdentityPolymorphism`,
  `InnerCapturesOuterParam`) plus the simplification pass and function
  exactness — is **M3**. M2 wires up enough of the walk to satisfy its own
  acceptance (literals, identifiers, simple `val` initializers, `fn` decls with
  bodies the spike already handles) and leaves richer expression coverage and
  polish to M3.
- **Records/`mut`/lifetimes (M4), classes (M5), unions (M6), operators (M8)**
  are out of scope. Unsupported expression/decl nodes produce a structured
  "unsupported in M2" error, never a panic.

## 2. Current state this builds on

- **Spike core** (`internal/simplesub/`): `typeTerm` (the recursive
  switch we are re-targeting), `constrain`, `coalesce`, `simplify`, levels via
  `scheme.go` (`MonoScheme`/`PolyScheme`, `instantiate`/`freshenAbove`),
  `LetRecGroup` (fresh var per binding + `constrain` + generalize — the
  recursion story that avoids placeholder patching). The spike's `Infer` returns
  `(type_system.Type, []error)` and renders via `type_system.PrintType`; M1
  re-homes this onto `soltype` with its own printer.
- **AST** (`internal/ast/`): expression nodes the walk must handle for the M2
  bar — `LiteralExpr`, `IdentExpr`, `FuncExpr` (`FuncSig` at `expr.go:326`,
  `FuncExpr` at `expr.go:335`), `CallExpr`, `ObjectExpr`, `MemberExpr`,
  `TupleExpr`, `IfElseExpr`, `BinaryExpr`, plus `Block` (`expr.go:905`). Decls:
  `VarDecl` (`decl.go:40`), `FuncDecl`, with `Param` at `decl.go:101`. Stmts:
  `DeclStmt`, `ExprStmt`, `ReturnStmt`.
- **Reusable as-is** (overview boundary analysis): `parser`, `ast`, `resolver`,
  `dep_graph`, `set`, `provenance`, `liveness`, `interop`. M2 must consume these
  **without** touching `internal/checker/` or `internal/type_system/` (that is
  the gate).
- **Compiler entry points: 3** (`CheckLib`, `Compile`, `CompilePackage` in
  `internal/compiler/compiler.go`). M2 does **not** wire into these — the new
  checker is exercised only through M2's own harness. Compiler wiring behind a
  flag is M7.

## 3. Design

### 3.1 Package layout

Inside the M1 package (working name `internal/solver/`):

```
internal/solver/
  infer.go        # constraint-generating walk over *ast.Module (production typeTerm)
  infer_expr.go   # per-expression-kind constraint generation
  infer_decl.go   # VarDecl / FuncDecl → bindings, SCC group inference
  module.go       # InferModule: dep_graph SCC ordering + resolver + drive the walk
  scope.go        # Scope / Binding / Namespace (own, not type_system)
  errors.go       # bridge errors (unbound name, unsupported node) with provenance/spans
  // (soltype core, Info side table, printer: from M1)
  testdata/ or fixtures wiring   # see §3.6
```

### 3.2 The constraint-generating walk (`infer.go`)

The production analogue of the spike's `typeTerm`. Two realistic shapes:

- **Direct recursive switch** over `ast.Expr`/`ast.Stmt`/`ast.Decl`, mirroring
  the spike's `typeTerm` (returns `(soltype.Type, []error)` and threads a
  `*Scope` + `level`). This is the natural fit: constraint generation is
  bottom-up and value-producing, which the AST's enter/exit `Visitor`
  (`internal/ast/visitor.go`, designed for transformation) does not model
  cleanly.
- **AST `Visitor`** — rejected for the expression walk: CLAUDE.md says prefer
  the existing visitor for traversals, but that visitor returns no synthesized
  value per node and is shaped for rewriting, not type synthesis. A direct
  switch matching the spike (and the old checker's `inferExpr`) is the right
  call; note this deviation explicitly in the PR.

Each node maps to the constraint the spike already established, now over real
AST instead of `Term`:

| AST node | Constraint (per spike `typeTerm`) | Records into `Info`? |
|----------|-----------------------------------|----------------------|
| `LiteralExpr` | `Literal` soltype | yes |
| `IdentExpr` | resolve via `Scope` → `instantiate` scheme | yes |
| `FuncExpr` | `Function{params, ret}`; params get fresh vars | yes |
| `CallExpr` | fresh `res`; `constrain(fn, Function{args, res})` | yes |
| `BinaryExpr` | operator scheme from builtins env | yes |
| `TupleExpr` | `Tuple{elems}` | yes |
| `ObjectExpr` | `Record{fields}` (basic; usage-inference is M4) | yes |
| `MemberExpr` | `constrain(recv, Record{name: fresh})` (basic; M4 deepens) | yes |
| `IfElseExpr` | join branches; `constrain(cond, boolean)` | yes |
| `Block` | type each stmt; result = last expr (or `void`) | yes |

Every node that produces a type calls the M1 `Info.setType(node, t)` so the side
table is the single source of truth for node→type (the AST stays untouched — no
`InferredType()` writes; that is the AST-decoupling decision). Nodes outside the
M2 subset emit an "unsupported in M2" error.

### 3.3 Module driver (`module.go`) — dep_graph + resolver

The milestone's spine: `parser.Parse*` → `*ast.Module` → `dep_graph`/`resolver`
→ walk.

- **Name resolution** runs through the existing `resolver` so identifiers bind
  to their declarations/namespaces before inference. M2 *consumes* resolver
  output; it does not reimplement resolution.
- **Declaration ordering** comes from `dep_graph`: top-level declarations are
  grouped into strongly-connected components (SCCs) and processed in dependency
  order. This is exactly how the old `infer_module.go` consumes the dep graph —
  M2 reuses the same `dep_graph` package, but feeds its SCCs into the new walk.
- **Recursive groups need no placeholder phase.** Where the old checker uses a
  placeholder/`typeRefsToUpdate` patching pass for cross-declaration recursion
  (`infer_module.go`), the simple-sub approach handles an SCC the way the spike's
  `LetRecGroup` does: give each binding in the SCC a fresh var at `level+1`, make
  all of them visible in every body, `constrain` each body `<:` its var, then
  generalize the whole group at the shared level. M2 lifts this pattern from the
  spike to operate over a `dep_graph` SCC of `VarDecl`/`FuncDecl`. This is the
  single biggest simplification the bridge buys and should be called out.
- **Multi-file** falls out of the dep graph spanning modules: the driver builds
  the graph across the parsed modules and resolves cross-module references
  through the resolver + the new `Namespace` (below).

Entry point (working signature):

```go
// InferModule resolves and infers every top-level declaration in the parsed
// module(s), populating Info and returning the module Scope plus errors.
func InferModule(modules []*ast.Module) (*Scope, *Info, []error)
```

### 3.4 Scope / Binding / Namespace (own, not `type_system`)

A minimal, package-owned analogue (the milestone forbids reusing
`type_system`'s):

- `Binding` — a name's `soltype` scheme (`MonoScheme`/`PolyScheme`) plus its
  source provenance.
- `Scope` — parent-linked name→`Binding` lookup, the production analogue of the
  spike's `ctx map[string]TypeScheme`.
- `Namespace` — the module/namespace grouping that cross-module resolution
  resolves through.

Keep this deliberately small in M2 — only what top-level `val`/`fn` +
multi-file resolution require. It grows with later milestones (types,
lifetimes, classes).

### 3.5 Errors & provenance

- Bridge errors (`errors.go`): unbound name, unsupported node — carry
  `provenance`/source spans from the AST node (reuse `internal/provenance/`).
- Inference errors from the core (`constrain` failures) attach the offending
  node's provenance via the `Info`/provenance side table (M1 provides the
  mechanism; M2 supplies the AST node). Assert **full** messages in tests
  (CLAUDE.md).

### 3.6 Fixture-style harness

Two complementary test surfaces, matching the milestone's "fixture-style harness
… its own assertions, independent of the old checker":

- **Table-driven `*_test.go`** in the new package: `.esc` snippet → expected
  rendered binding type string (using the M1 `soltype` printer). This is the
  primary M2 surface — fast, no per-case package overhead, mirrors the spike's
  existing `simplesub_test.go` pattern and the checker-tests pattern in
  `internal/checker/tests/`.
- **A real `fixtures/`-style harness** (sibling to
  `cmd/escalier/fixture_test.go`) for the multi-file/dep-graph acceptance:
  `fixtures/<name>/lib/index.esc` (+ `package.json`) → resolve via dep graph →
  assert rendered top-level binding types. This proves the dep-graph/multi-file
  path end-to-end, which a single-snippet table test can't.

Assert inferred types as Escalier type-annotation strings; use `testify/require`
(CLAUDE.md). For tree-shaped assertions prefer `snaps.MatchInlineSnapshot` over
field-by-field drilling.

## 4. Sequencing

```
M1 (package skeleton + soltype + Info + printer)   ── prerequisite
        │
        ▼
PR-1  Scope/Binding/Namespace + expression walk skeleton (lits, idents)
        │
        ▼
PR-2  single-decl driver: VarDecl/FuncDecl, table harness, val/fn end-to-end
        │
        ▼
PR-3  dep_graph SCC ordering + recursive-group (LetRecGroup) inference
        │
        ▼
PR-4  resolver wiring + multi-file fixtures harness  ── closes M2 exit criteria
```

PR-1 establishes the package-owned `Scope` and the value-returning walk against
M1's `soltype`/`Info`. PR-2 makes a *single* module's top-level `val`/`fn`
infer end-to-end with the table harness — the first half of the exit bar. PR-3
brings in dep-graph SCC ordering and recursive groups (still single-file). PR-4
adds resolver-driven cross-module resolution and the `fixtures/` harness,
closing the multi-file half of the exit bar. PRs are mostly linear because each
depends on the prior layer's plumbing; PR-2's table harness and PR-1's walk
skeleton are the only pieces that could overlap.

## 5. PR breakdown

### PR-1 — Scope + expression-walk skeleton
- `scope.go`: `Scope`/`Binding`/`Namespace` (minimal).
- `infer.go`/`infer_expr.go`: value-returning recursive walk over `ast.Expr`
  for `LiteralExpr`, `IdentExpr` (via `Scope`), writing into `Info`.
- Unsupported nodes return a structured error (no panic).
- Tests: literal type; identifier resolves to its binding's scheme; unbound
  identifier → full-message error with span.
- **Exit:** the walk types the trivial expression subset against `soltype`/`Info`.

### PR-2 — Single-module decl driver + table harness
- `infer_decl.go`: `VarDecl` → `Binding`; `FuncDecl` → `Function` binding,
  walking the body with the spike's function/let machinery (as re-homed in M1).
- `module.go`: a first `InferModule` that handles one module, declarations in
  source order (no SCC yet), populating `Scope` + `Info`.
- Table-driven harness: `.esc` snippet → rendered top-level binding type.
- Tests: `val x = 5` ⇒ `5`/`number` per M1 widening; a simple `fn` infers its
  rendered type; an expression-stmt module.
- **Exit:** top-level `val`/`fn` in a *single* module infer correct rendered
  types end-to-end (first half of the milestone bar).

### PR-3 — dep_graph SCC ordering + recursive groups
- `module.go`: build the dependency graph from the module, process top-level
  decls in SCC order.
- Lift the spike's `LetRecGroup` pattern to a `dep_graph` SCC: fresh var per
  binding at `level+1`, all visible in every body, `constrain` body `<:` var,
  generalize the group at the shared level. **No placeholder/patching phase.**
- Tests: self-recursive `fn`; mutually-recursive `fn` pair; a decl that
  forward-references a later decl resolves via SCC ordering.
- **Exit:** recursive and out-of-order top-level decls infer correctly in one
  module.

### PR-4 — resolver wiring + multi-file fixtures harness
- Wire the existing `resolver` so identifiers (incl. cross-module) bind before
  inference; resolve through the new `Namespace`.
- `module.go`: accept multiple parsed modules; build the dep graph across them.
- Add a `fixtures/`-style harness (sibling to `cmd/escalier/fixture_test.go`)
  asserting rendered top-level binding types for a multi-file fixture.
- Tests: a two-file fixture where file B imports a `val`/`fn` from file A and the
  inferred types render correctly end-to-end.
- Update `01-milestones.md` M2 status.
- **Exit (M2 exit criteria):** multi-file module resolves via the dep graph;
  top-level `val`/`fn` infer correct rendered types end-to-end.

## 6. Risks & mitigations

- **Gate — reaching into old checker internals.** Driving from
  AST/dep-graph/resolver must not pull in `internal/checker/` or
  `internal/type_system/`. *Mitigation:* a package-boundary test/lint that the
  new package imports neither; if dep_graph/resolver turn out to expose
  checker-coupled types, that is the milestone's stop-and-reassess signal — raise
  it rather than working around it.
- **dep_graph/resolver coupling to `type_system`.** If those packages return
  `type_system`-flavoured data, the bridge leaks. *Mitigation:* consume only
  their structural outputs (names, SCCs, resolution edges); keep all *type* data
  in `soltype`/`Info`.
- **Walk vs. AST `Visitor` deviation.** Using a direct switch instead of the
  shared visitor is a deliberate departure from the CLAUDE.md convention.
  *Mitigation:* document the rationale (value-synthesis vs. transformation) in
  the PR; it matches both the spike and the old checker's `inferExpr`.
- **Scope creep into M3/M4.** Records/usage-inference, full let-polymorphism
  polish, simplification, exactness are *not* M2. *Mitigation:* the explicit
  "unsupported in M2" error path and the shallow-coverage table in §3.2.
- **Curried-Term assumptions in the spike.** The spike curries `Lam`/`App`;
  Escalier `FuncExpr`/`CallExpr` are n-ary. *Mitigation:* the production walk
  builds n-ary `Function` constraints directly (the spike's `typeTerm` `*Lam`
  case already supports multi-param), so no currying shim is needed.

## 7. Open questions to resolve during M2

- **Final package name.** M1 picks the leaf (working name `internal/solver/`);
  M2 inherits it. No new decision unless M1 defers it.
- **How much resolver to drive in M2 vs. defer.** If full namespace resolution
  is heavier than the val/fn bar needs, PR-4 can scope to the minimum
  cross-module resolution that satisfies the multi-file acceptance and leave
  richer namespace semantics to later milestones — decide when PR-4 starts,
  based on what `resolver` already provides.

## 8. M2 exit checklist

- [ ] Constraint-generating walk over `*ast.Module` produces `soltype` and
      populates `Info` (no AST `InferredType()` writes).
- [ ] Package-owned `Scope`/`Binding`/`Namespace` (no `type_system` reuse).
- [ ] Top-level `val`/`fn` from real source infer correct rendered types
      end-to-end (table harness).
- [ ] Multi-file module resolves via the dep graph (fixtures harness).
- [ ] Recursive SCC groups infer with no placeholder/patching phase.
- [ ] No imports of `internal/checker/` or `internal/type_system/` from the new
      package (gate honored).
- [ ] `01-milestones.md` M2 status updated.
