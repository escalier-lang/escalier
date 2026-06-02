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
real parsed source, ordered by the existing `dep_graph`, with results recorded
in the `Info` side table.

> **Terminology note (corrected after surveying the packages).** In Escalier,
> top-level *name/dependency resolution* is done by **`internal/dep_graph/`**
> (`BuildDepGraph(*ast.Module)` → `DepGraph.Components`, the SCCs the checker
> infers in order). `internal/resolver/` is a **narrow** helper that only
> resolves TypeScript `@types` packages (`ResolveTypesPackage`,
> `GetTypesEntryPoint`) — it is *not* the general name resolver. The milestone's
> phrase "dep_graph/resolver" therefore means: drive declaration order and
> cross-declaration references through `dep_graph`; `resolver` is relevant only
> when a fixture imports a `.d.ts`-typed third-party module (likely beyond the
> M2 `val`/`fn` bar). The plan below is built around `dep_graph`.

Per the milestone, M2 delivers:

1. **Drive from real source.** `parser.Parse*` → `*ast.Module` →
   `dep_graph.BuildDepGraph` → a constraint-generating AST walk that produces
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
  creates the new package — `internal/solver/` (settled decision #1 in
  design-notes; sibling to `internal/checker/`) — the `soltype` representation
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

Inside the M1 package (`internal/solver/`):

```
internal/solver/
  infer.go        # constraint-generating walk over *ast.Module (production typeTerm)
  infer_expr.go   # per-expression-kind constraint generation
  infer_decl.go   # VarDecl / FuncDecl → bindings, SCC group inference
  module.go       # InferModule(s): dep_graph SCC ordering + drive the walk
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

### 3.3 Module driver (`module.go`) — dep_graph-ordered inference

The milestone's spine: `parser.Parse*` → `*ast.Module` →
`dep_graph.BuildDepGraph` → SCC-ordered walk. The new driver mirrors the old
checker's `InferDepGraph` / `InferComponent` shape exactly, swapping
`type_system` for `soltype`:

```go
// Old checker (internal/checker/infer_module.go), for reference:
func (c *Checker) InferDepGraph(ctx Context, depGraph *dep_graph.DepGraph) (errors []Error)
func (c *Checker) InferComponent(ctx Context, depGraph *dep_graph.DepGraph,
    component []dep_graph.BindingKey) []Error
```

- **Declaration ordering comes from `dep_graph`.** `BuildDepGraph(module)`
  returns a `*dep_graph.DepGraph` whose `Components` field is the list of SCCs
  (`[][]dep_graph.BindingKey`) in **topological order** (if A depends on B, B's
  component precedes A's). A `BindingKey` is `"value:"` / `"type:"` + the
  qualified name; `GetDecls(key)` returns the `[]ast.Decl` for that binding
  (a slice because overloads / interface-merging contribute several). M2's
  driver iterates `Components` and infers each, exactly as the old
  `InferDepGraph` loops over `depGraph.Components` calling `InferComponent`.
- **Recursive groups need no placeholder phase.** The old `InferComponent`
  runs a two-phase placeholder/definition pass (`sortKeysForPlaceholders` +
  signature-then-body) to break cross-declaration recursion. The simple-sub
  approach replaces that with the spike's `LetRecGroup` pattern: for an SCC,
  give each binding a fresh var at `level+1`, make all of them visible in every
  body, `constrain` each body `<:` its var, then generalize the whole group at
  the shared level. **No placeholder phase, no `typeRefsToUpdate` patching** —
  this is the single biggest simplification the bridge buys and should be
  called out in the PR. (A singleton non-recursive SCC is just the degenerate
  case — one binding, generalize after its body.)
- **Multi-file** falls out of the dep graph spanning modules and the new
  `Namespace` (below): cross-module references resolve through the qualified
  `BindingKey` namespace recorded on each binding (`DeclNamespace` /
  `GetNamespace(key)`).
- **`resolver` is *not* on this path.** `internal/resolver/` only locates
  `@types` `.d.ts` packages; it is engaged only if an M2 fixture imports a
  TypeScript-typed third-party module, which the `val`/`fn` bar does not
  require. M2 leaves `.d.ts` import typing to later milestones unless a fixture
  forces it.

Entry point (working signature, paralleling the old driver):

```go
// InferModule builds the dep graph for the parsed module, infers every
// top-level declaration in SCC order, populates Info, and returns the module
// Scope plus errors. The package-level entry constructs a fresh checker
// internally; multi-file callers use InferModules (§3.7). Full signatures in
// §3.7.
func InferModule(module *ast.Module) (*Scope, *soltype.Info, []Error)
```

### 3.4 Scope / Binding / Namespace (own, not `type_system`)

A package-owned, **multi-sorted** analogue (the milestone forbids reusing
`type_system`'s). Design-notes §"Scope / Binding" already specifies the shape —
three slots, one per binding sort:

```go
type Scope struct {
    values     map[string]ValueBinding   // soltype schemes (Mono | Poly)
    types      map[string]TypeBinding    // type aliases, class types
    namespaces map[string]*Namespace     // a separate sort — NOT a soltype.Type
    parent     *Scope
}
func (s *Scope) GetValue(name string) (ValueBinding, bool)
func (s *Scope) GetType(name string) (TypeBinding, bool)
func (s *Scope) GetNamespace(name string) (*Namespace, bool)
```

(Comma-ok return shape, matching the `inferIdent` sketch in §3.7. Design-notes
sketches these as pointer returns; the comma-ok form is the M2 refinement so the
not-found case is explicit at every call site.)

- `ValueBinding` — a name's `soltype` scheme (`MonoScheme`/`PolyScheme`, from the
  spike's `scheme.go`) plus its source provenance. The production analogue of the
  spike's `ctx map[string]TypeScheme`.
- The **value-position `IdentExpr`** path queries `GetValue`, then `GetNamespace`
  only to raise `NamespaceUsedAsValueError` (namespaces are a separate sort and
  never flow as values — design-notes §"The constraint-generating AST walk").
- `Namespace` is keyed by the qualified `BindingKey` namespace `dep_graph`
  records (`GetNamespace(key)`), which is how multi-file resolution lands.

**M2 scope:** `values` + `namespaces` are what the `val`/`fn` + multi-file bar
needs. The `types` slot's shape lands now (cheap, and it's load-bearing for the
two-map test harness below), but populating it with real type aliases/classes is
M3+ work. Keep the rest deliberately small; it grows with later milestones.

### 3.5 Errors & provenance

- Bridge errors (`errors.go`): unbound name, unsupported node — carry source
  spans (`ast.Span`) taken from the offending AST node via `node.Span()`. (Note:
  the `internal/provenance/` package exports only the `Provenance` marker
  interface — `IsProvenance()` — *not* a span type; spans live in `internal/ast`.)
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

### 3.7 Sketched types & function signatures

Concrete shapes to anchor the PRs below. These are *sketches* — names and
fields will shift in review — but they pin down the surface area each PR owns.
All live in `internal/solver`. Types prefixed `soltype.` come from M1; the rest
are introduced by M2 (the PR that owns each is noted).

**The checker carrier (PR-1).** A per-inference-run struct threading the M1
core's mutable state (the fresh-var counter, the `Info`/provenance tables) — the
production analogue of the spike's `Inferer`. Method receiver for the whole walk.

```go
// infer.go
type checker struct {
    core *soltype.Inferer  // M1: freshVar, constrain, instantiate, freshenAbove, level
    info *soltype.Info     // M1: node → type side table
    prov soltype.Prov      // M1: type → origin side table
    errs []Error           // accumulated; mirrors the spike's []error threading
}

func newChecker() *checker

// freshAt allocates a fresh inference variable at the given level (wrapping the
// spike's Inferer.freshVar(level)) and records its AST origin in prov (the
// freshVarAt helper from design-notes §Provenance).
func (c *checker) freshAt(lvl int, n ast.Node, kind soltype.ASTOriginKind) *soltype.TypeVarType

// report appends a structured error; returns soltype's error type so callers
// can `return c.report(...)` in a value position (yielding an error placeholder).
func (c *checker) report(e Error) soltype.Type
```

**Scope / Binding / Namespace (PR-1).** Design-notes §"Scope / Binding" gives
the three-slot shape; the constructors and `define*` mutators are M2's:

```go
// scope.go
type ValueBinding struct {
    Scheme soltype.TypeScheme    // MonoScheme | PolyScheme (spike scheme.go)
    Source provenance.Provenance // the introducing VarDecl/FuncDecl/param
}
type TypeBinding struct {        // shape only in M2; populated M3+
    Type   soltype.Type
    Source provenance.Provenance
}
type Namespace struct {
    Name   string                // qualified, from dep_graph.GetNamespace
    Values map[string]ValueBinding
    Types  map[string]TypeBinding
    Nested map[string]*Namespace
}

type Scope struct {
    values     map[string]ValueBinding
    types      map[string]TypeBinding
    namespaces map[string]*Namespace
    parent     *Scope
}

func NewScope() *Scope
func (s *Scope) Child() *Scope
func (s *Scope) defineValue(name string, b ValueBinding)
func (s *Scope) GetValue(name string) (ValueBinding, bool)      // walks parents
func (s *Scope) GetType(name string) (TypeBinding, bool)
func (s *Scope) GetNamespace(name string) (*Namespace, bool)
```

**The expression walk (PR-1 lits/idents; PR-3 fn/call/block; PR-4 objects).**
The production `typeTerm`, split by node category. Each returns a `soltype.Type`
and threads `*Scope` + `level`:

```go
// infer.go / infer_expr.go
func (c *checker) inferExpr(scope *Scope, lvl int, e ast.Expr) soltype.Type
func (c *checker) inferStmt(scope *Scope, lvl int, s ast.Stmt) soltype.Type
func (c *checker) inferBlock(scope *Scope, lvl int, b *ast.Block) soltype.Type

// inferExpr dispatches; the per-kind helpers mirror the spike's typeTerm cases:
func (c *checker) inferLiteral(e *ast.LiteralExpr) soltype.Type           // PR-1
func (c *checker) inferIdent(scope *Scope, lvl int, e *ast.IdentExpr) soltype.Type   // PR-1
func (c *checker) inferFuncExpr(scope *Scope, lvl int, e *ast.FuncExpr) soltype.Type // PR-3
func (c *checker) inferCall(scope *Scope, lvl int, e *ast.CallExpr) soltype.Type     // PR-3
func (c *checker) inferObject(scope *Scope, lvl int, e *ast.ObjectExpr) soltype.Type // PR-4
```

`inferIdent` is the load-bearing one — it's the production form of the spike's
`*Var` case crossed with design-notes §"The constraint-generating AST walk":

```go
func (c *checker) inferIdent(scope *Scope, lvl int, e *ast.IdentExpr) soltype.Type {
    if b, ok := scope.GetValue(e.Name); ok {
        t := c.core.Instantiate(b.Scheme, lvl) // MonoScheme: as-is; PolyScheme: freshenAbove
        c.info.SetType(e, t)
        return t
    }
    if _, ok := scope.GetNamespace(e.Name); ok {
        return c.report(NamespaceUsedAsValueError{Name: e.Name, Span: e.Span()})
    }
    return c.report(UnknownIdentifierError{Name: e.Name, Span: e.Span()})
}
```

**Declaration & module driver (PR-2 single-decl; PR-5 SCC).** Mirrors the old
checker's `InferDepGraph`/`InferComponent`, over `soltype`:

```go
// module.go
func InferModule(module *ast.Module) (*Scope, *soltype.Info, []Error)   // PR-2 (source-order), PR-5 (SCC)
func InferModules(modules []*ast.Module) (*Scope, *soltype.Info, []Error) // PR-6 (multi-file)

func (c *checker) inferDepGraph(scope *Scope, lvl int, g *dep_graph.DepGraph) // PR-5
func (c *checker) inferComponent(scope *Scope, lvl int, g *dep_graph.DepGraph, // PR-5
    component []dep_graph.BindingKey)

// infer_decl.go
func (c *checker) inferVarDecl(scope *Scope, lvl int, d *ast.VarDecl) ValueBinding   // PR-2
func (c *checker) inferFuncDecl(scope *Scope, lvl int, d *ast.FuncDecl) ValueBinding // PR-3
```

`inferComponent` is the LetRecGroup lift (PR-5); the singleton non-recursive
case is the same code with a one-element slice. The level discipline mirrors the
spike's `LetRecGroup` exactly: bind the group's vars and infer its bodies at
`lvl+1`, then generalize at the outer `lvl` — `freshenAbove` only quantifies
vars whose level is `> lim`, so the `+1`/generalize-at-`lvl` split is what makes
the recursive bindings polymorphic. (Generalizing at `lvl+1` instead would leave
them monomorphic — the bug to avoid.)

```go
func (c *checker) inferComponent(scope *Scope, lvl int, g *dep_graph.DepGraph,
    component []dep_graph.BindingKey) {
    inner := lvl + 1
    // 1. fresh var per binding at the inner level, all visible before any body
    vars := map[dep_graph.BindingKey]*soltype.TypeVarType{}
    for _, key := range component {
        v := c.freshAt(inner, /* decl node */ nil, soltype.OriginRecBinding)
        vars[key] = v
        scope.defineValue(key.Name(), ValueBinding{Scheme: &soltype.MonoScheme{Ty: v}})
    }
    // 2. infer each body at the inner level, constrain body <: its var
    for _, key := range component {
        for _, d := range g.GetDecls(key) {
            body := c.inferDeclBody(scope, inner, d)
            c.core.Constrain(body, vars[key])
        }
    }
    // 3. generalize the whole group at the OUTER level lvl (not inner): vars
    //    created at lvl+1 are > lvl, so freshenAbove quantifies them.
    for _, key := range component {
        scope.defineValue(key.Name(), ValueBinding{
            Scheme: &soltype.PolyScheme{Level: lvl, Body: vars[key]},
        })
    }
}
```

**Errors (PR-1).** A small set of structured errors with spans; assert full
messages in tests:

```go
// errors.go
type Error interface{ error; Span() ast.Span }
type UnknownIdentifierError struct{ Name string; Span ast.Span }
type NamespaceUsedAsValueError struct{ Name string; Span ast.Span }
type UnsupportedNodeError struct{ Kind string; Span ast.Span } // M2-subset guard
```

**Test harness (PR-2 table; PR-6 fixtures).**

```go
// infer_test.go — table harness
func inferSource(t *testing.T, src string) (values, types map[string]string)
// returns name → rendered soltype string, via parser + InferModule + soltype printer

// fixture_test.go — multi-file harness (sibling to cmd/escalier/fixture_test.go)
func checkSolverFixture(t *testing.T, fixtureDir string) // asserts rendered top-level types
```

## 4. Sequencing

### Sizing rationale (why six PRs, not four)

The earlier four-PR cut bundled too much into "PR-2" (decl driver + the
fn/call/block walk + the table harness all at once) and left "PR-1" thin. The
revised split keeps each PR to **one reviewable concern** — roughly
150–400 LoC of non-test code plus its tests — and front-loads the
infrastructure (`checker` carrier, `Scope`, harness) so later PRs are pure
feature additions. Concretely:

- **PR-1** is foundation only (carrier + scope + errors + the two leaf
  expression cases). Small, no driver.
- The old PR-2 split into **PR-2 (decl driver, source-order, the table
  harness — `val` end-to-end)** and **PR-3 (the function/application/block
  walk — `fn` end-to-end)**. These are independent given PR-1: the decl driver
  can land typing only `val x = <literal/ident>` initializers, and the
  fn/call/block walk is a self-contained set of `inferExpr` cases. Either can
  merge first; the second rebases trivially.
- **PR-4** (objects/members) is a small, optional-for-the-bar add that several
  fixtures will want; isolated so it can slip without blocking the SCC/multi-file
  work.
- The old PR-3 (SCC) becomes **PR-5**, the old PR-4 (multi-file) becomes
  **PR-6** — unchanged in content, renumbered.

### PR dependency graph

```text
                M1 (soltype + Info + printer)
                        │
                        ▼
                ┌─────────────────┐
                │ PR-1  carrier +  │   checker{}, Scope/Binding/Namespace,
                │ scope + leaves   │   errors, inferExpr(lits, idents)
                └───────┬─────────┘
                        │
            ┌───────────┴───────────┐
            ▼                       ▼
   ┌──────────────────┐   ┌──────────────────────┐
   │ PR-2 decl driver │   │ PR-3 fn / call /      │   (PR-2 ∥ PR-3:
   │ + table harness  │   │ block walk            │    independent given PR-1)
   │ (val end-to-end) │   │ (fn end-to-end)       │
   └───────┬──────────┘   └──────────┬───────────┘
           │                         │
           │   ┌─────────────────────┤
           ▼   ▼                     ▼
   ┌──────────────────┐   ┌──────────────────────┐
   │ PR-5 dep_graph   │   │ PR-4 objects /        │   (PR-4 ∥ PR-5:
   │ SCC + LetRecGroup│   │ member access         │    both need PR-2+PR-3)
   └───────┬──────────┘   └──────────┬───────────┘
           │                         │
           └───────────┬─────────────┘
                       ▼
           ┌────────────────────────┐
           │ PR-6 multi-file (x-mod) │   closes M2 exit criteria
           │ resolution + fixtures   │
           └────────────────────────┘
```

Edges are hard dependencies (the target imports/uses symbols the source
introduces). The two `∥` pairs (PR-2 ∥ PR-3, and PR-4 ∥ PR-5) have no edge
between them and can be developed/reviewed in parallel. PR-6 is the only PR that
needs *both* upstream branches merged (it asserts end-to-end over real fixtures,
which exercises decls, functions, and SCC ordering together).

## 5. PR breakdown

> Each PR lists its **owned files**, the **sketches from §3.7** it implements,
> its **tests**, and an **exit** line. "LoC" estimates are non-test code.

### PR-1 — Checker carrier + Scope + leaf expressions  (~250 LoC)
- `infer.go`: the `checker` struct, `newChecker`, `fresh`, `report`;
  `inferExpr` dispatch with only the leaf cases wired.
- `scope.go`: `Scope`/`ValueBinding`/`TypeBinding`/`Namespace` + `NewScope`,
  `Child`, `defineValue`, `GetValue`/`GetType`/`GetNamespace`.
- `errors.go`: `Error` interface, `UnknownIdentifierError`,
  `NamespaceUsedAsValueError`, `UnsupportedNodeError`.
- `infer_expr.go`: `inferLiteral`, `inferIdent`; all other `ast.Expr` kinds fall
  through to `UnsupportedNodeError` (no panic).
- Tests: literal → rendered type; identifier resolves to a pre-seeded binding's
  scheme; unbound identifier and namespace-as-value → full-message errors with
  spans.
- **Exit:** the walk types the literal/identifier subset against `soltype`/`Info`;
  every other node fails cleanly.

### PR-2 — Single-module decl driver + table harness  (~250 LoC)
*(depends on PR-1; parallel with PR-3)*
- `infer_decl.go`: `inferVarDecl` (initializer typed via `inferExpr`, generalized
  into a `ValueBinding`).
- `module.go`: first `InferModule` for one module, decls in **source order**
  (no SCC yet), seeding the module `Scope` and `Info`.
- `infer_test.go`: the `inferSource` table harness.
- Tests: `val x = 5` ⇒ `5`/`number` (per M1 widening); `val y = x` referencing an
  earlier decl; forward reference → (documented) error until PR-5 adds ordering.
- **Exit:** top-level `val` decls with literal/identifier initializers infer
  end-to-end and render correctly via the harness.

### PR-3 — Function / application / block walk  (~300 LoC)
*(depends on PR-1; parallel with PR-2)*
- `infer_expr.go`: `inferFuncExpr` (n-ary `Function`, fresh var per param),
  `inferCall` (`constrain(callee <: Function{args, fresh})`), `inferBlock` /
  `inferStmt` (sequence; result = last expr or `void`).
- `infer_decl.go`: `inferFuncDecl` (reuses `inferFuncExpr` on the decl's sig+body).
- Tests: identity `fn`, application of it, a block with a `return`, arity
  mismatch on a direct call → full-message error.
- **Exit:** `fn` decls and calls infer end-to-end (the second half of the
  per-expression bar; deep let-polymorphism polish is still M3).

### PR-4 — Objects & member access  (~200 LoC)
*(depends on PR-2 + PR-3; parallel with PR-5)*
- `infer_expr.go`: `inferObject` (`Record{fields}`), `inferMember` (basic
  `constrain(recv <: Record{name: fresh})`), `inferTuple`.
- Reject shorthand/spread/computed members with `UnsupportedNodeError`.
- Tests: record literal type; field read; field-on-missing → constraint failure;
  tuple literal.
- **Exit:** record/tuple literals and simple field reads infer; usage-inference
  depth is explicitly deferred to M4.

### PR-5 — dep_graph SCC ordering + recursive groups  (~250 LoC)
*(depends on PR-2 + PR-3)*
- `module.go`: `inferDepGraph` (iterate `g.Components`), `inferComponent` (the
  LetRecGroup lift sketched in §3.7).
- Replace PR-2's source-order loop with SCC ordering. **No placeholder/patching
  phase.**
- Tests: self-recursive `fn`; mutually-recursive `fn` pair; a decl that
  forward-references a later decl now resolves via SCC ordering (the PR-2
  documented-error case flips to success).
- **Exit:** recursive and out-of-order top-level decls infer correctly in one
  module.

### PR-6 — Multi-file (cross-module) resolution + fixtures harness  (~250 LoC)
*(depends on PR-4 + PR-5)*
- `module.go`: `InferModules` over multiple parsed modules; build the dep graph
  across them; cross-module references resolve through the qualified-`BindingKey`
  `Namespace` (`dep_graph.GetNamespace`). Engage `internal/resolver` only if a
  fixture imports a `.d.ts`-typed third-party module.
- `fixture_test.go`: the `checkSolverFixture` harness (sibling to
  `cmd/escalier/fixture_test.go`).
- Tests: a two-file fixture where file B imports a `val`/`fn` from file A and the
  inferred types render correctly end-to-end.
- Update `01-milestones.md` M2 status.
- **Exit (M2 exit criteria):** multi-file module resolves via the dep graph;
  top-level `val`/`fn` infer correct rendered types end-to-end.

## 6. Risks & mitigations

- **Gate — reaching into old checker internals.** Driving from AST/dep-graph
  must not pull in `internal/checker/` or `internal/type_system/`. *Mitigation:*
  a package-boundary test/lint that the new package imports neither; if
  `dep_graph` turns out to expose checker-coupled types, that is the milestone's
  stop-and-reassess signal — raise it rather than working around it.
- **dep_graph coupling to `type_system`.** `dep_graph` operates on `*ast.Module`
  and `ast.Decl` (its `Decls`/`Components`/`BindingKey` API is type-system-free),
  so this risk is low — but confirm it in PR-5. *Mitigation:* consume only its
  structural outputs (`BindingKey`s, `Components`, `GetDecls`/`GetNamespace`);
  keep all *type* data in `soltype`/`Info`.
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

- **Package name** is settled (`internal/solver/`, design-notes decision #1);
  M2 inherits it from M1. No open decision here.
- **How much namespace resolution to drive in M2 vs. defer.** If full
  cross-module/namespace resolution is heavier than the val/fn bar needs, PR-6
  can scope to the minimum that satisfies the multi-file acceptance (a binding
  in file B referencing a top-level `val`/`fn` in file A via its qualified
  `BindingKey`) and leave richer namespace semantics (`ns.foo` member access,
  nested namespaces) to later milestones — decide when PR-6 starts, based on what
  `dep_graph` already records in `DeclNamespace`.
- **Whether M2 needs `internal/resolver` at all.** It's only for `@types`
  `.d.ts` resolution; if no M2 fixture imports a TS-typed module, M2 can skip it
  entirely and the milestone's "dep_graph/resolver" phrasing is satisfied by
  `dep_graph` alone.

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
