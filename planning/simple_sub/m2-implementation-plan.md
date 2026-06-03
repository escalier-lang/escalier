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
> when a test source imports a `.d.ts`-typed third-party module (likely beyond the
> M2 `val`/`fn` bar). The plan below is built around `dep_graph`.

Per the milestone, M2 delivers:

1. **Drive from real source.** `parser.Parse*` → `*ast.Module` →
   `dep_graph.BuildDepGraph` → a constraint-generating AST walk that produces
   `soltype` types and populates `Info`.
2. **Own `Scope`/`Binding`/`Namespace`.** Analogues owned by the new package,
   *not* reused from `internal/type_system/`.
3. **A table-driven test harness.** Given `.esc` source (single- and multi-file,
   in-memory), infer and assert the rendered binding types — its own assertions,
   independent of the old checker. No on-disk `fixtures/` directories in M2; the
   real fixture harness is M8's job (see §3.6).

**Exit criteria (from the milestone):**
- Top-level `val`/`fn` declarations from real source infer correct rendered
  types end-to-end.
- A multi-file module resolves via the dep graph.

**Gate (from the milestone):** if driving from the real AST/dep-graph requires
reaching back into the old checker's internals, the parallel-package boundary is
wrong — **stop and reassess**.

### Scope boundary against neighbouring milestones

- **M1 (prerequisite — Package skeleton + `soltype`) has landed** (PRs #686–#693
  on `main`). It created **two** packages: `internal/soltype/` (the type
  representation — its own top-level package, imported as `soltype`) and
  `internal/solver/` (the engine + side tables, sibling to `internal/checker/`).
  M2 code lives in `internal/solver/`. What M1 actually delivered, and what it
  **deferred**, materially shapes M2 — see the "M1 landed: deltas from this
  plan's original assumptions" box in §2.
  - **Available now:** `soltype.Type` and its kinds (`TypeVarType` with
    `ID`/`Level`/`LowerBounds`/`UpperBounds`; `PrimType`/`LitType`/`FuncType`
    (params are `*FuncParam` carrying a sealed `Pat`, M1 = `IdentPat` only)/
    `TupleType`/`Void`/`NeverType`/`UnknownType`/`UnionType`/`IntersectionType`);
    `soltype.Polarity` + `TypeVarType.BoundsAt(pol)`; `solver.Context` with
    `freshVar(level)` and `Constrain(lhs, rhs) []SolverError`; levels/extrusion;
    bound-inlining `coalesce(t, pol)`; the `soltype.Print(t)` printer;
    `solver.Info` (`TypeOf` / unexported `setType`); and a `SolverError`
    interface with concrete span-free error kinds.
  - **Deferred to M3 (NOT available to M2):** type **schemes**
    (`TypeScheme`/`MonoScheme`/`PolyScheme`), `instantiate`/`freshenAbove`,
    occurrence analysis / co-occurrence merging, and the `<T0, …>` quantifier
    prefix in the printer. **Consequence:** M2 inference is **monomorphic** — it
    cannot generalize a top-level binding, so an un-annotated polymorphic
    function (`val id = fn (x) { x }`) cannot render as `fn <T0>(x: T0) -> T0`
    in M2. That rendering, and the `TopLevelLetPolymorphism` /
    `IdentityPolymorphism` / `InnerCapturesOuterParam` acceptance cases, are
    **M3** (the M1 milestone already moves them there). M2's "infer correct
    rendered types end-to-end" bar therefore means the **monomorphic** forms
    (`val x = 5` ⇒ `5`; `val f = fn (x: number) { x }` ⇒ `fn(x: number) -> number`).
  - **Deferred to later (NOT available to M2):** the `Prov` provenance side
    table (M2/later) and error `Span()` (M2 *adds* this — see §3.5).
- **M2 expression coverage is deliberately shallow.** The milestone's bar is
  "top-level `val`/`fn` infer correct rendered types end-to-end" plus multi-file
  resolution. The *deep* function/application/let-polymorphism work — and its
  acceptance cases (`TopLevelLetPolymorphism`, `IdentityPolymorphism`,
  `InnerCapturesOuterParam`) plus the simplification pass and function
  exactness — is **M3**. M2 wires up enough of the walk to satisfy its own
  acceptance (literals, identifiers, simple `val` initializers, `fn` decls with
  bodies the spike already handles), all **monomorphic** (M1 deferred schemes /
  generalization to M3), and leaves richer expression coverage and polish to M3.
- **Stdlib-type placeholder seeding (new in M2's milestone).** The M1-era
  edit to `01-milestones.md` added an M2 responsibility: names that downstream
  type rules reference — `Promise<T>`, `Iterable<T>`/`AsyncIterable<T>`,
  `Generator`/`AsyncGenerator`, `IteratorResult<T>` — must **resolve** to *some*
  `soltype.Type` so a reference doesn't error. M2 does this with **hand-seeded
  placeholder bindings**, not by reading the real stdlib decls: the real
  ingestion is checker/`type_system`-coupled and needs generics M1's `soltype`
  doesn't have yet. Real library type resolution is its own milestone, **M7**;
  M2 only unblocks the names. M2 also does **not** implement the rules that
  *use* them (`await`/`for-in`/`yield` land in later milestones). See §3.8.
- **Records/`mut`/lifetimes (M4), classes (M5), unions (M6), operators (M9)**
  are out of scope. Unsupported expression/decl nodes produce a structured
  "unsupported in M2" error, never a panic.
- **Body-level declarations are `VarDecl`-only — a language rule, not a subset
  gate.** Inside a function/method body the only declaration a statement may
  introduce is a `val`/`var`; any other decl kind (`FuncDecl`, `TypeDecl`,
  `ClassDecl`, …) is a permanent `BodyDeclNotAllowedError`, distinct from the
  temporary "unsupported in M2" gate above. Bodies may also **redeclare** a
  name — a later `val x` rebinds it with a fresh, unrelated type. Both are
  deliberate language simplifications that keep the body walk to a single
  decl shape; see §3.2. (Methods get the same rule when classes land in M5.)

## 2. Current state this builds on

> **M1 landed: deltas from this plan's original assumptions.** This plan was
> drafted before M1 merged. M1 (PRs #686–#693) came in with a different package
> split and a narrower scope than assumed; the sketches in §3.7 were updated to
> match, but read these deltas first:
>
> | Plan originally assumed | M1 actually shipped |
> |---|---|
> | `soltype` is a sub-package `internal/solver/soltype/` | **Two top-level packages**: `internal/soltype/` (types) + `internal/solver/` (engine). M2 code is in `internal/solver/`, importing `soltype`. |
> | An `Inferer` carrier with `c.core.Level()` | `solver.Context{ varCounter }` with `freshVar(level)`; **no ambient level** — level is an explicit argument everywhere. |
> | Schemes + `instantiate`/`freshenAbove` exist (re-homed from spike) | **Deferred to M3.** No `TypeScheme`/`MonoScheme`/`PolyScheme`, no `instantiate`. M2 inference is **monomorphic**. |
> | `Info.setType` callable; a `Prov` table exists | `solver.Info` with **unexported** `setType` (so the walk lives in `package solver`); **no `Prov`** (deferred). |
> | M2 defines the error types | M1 already ships `SolverError` + `CannotConstrainError`/`FuncArityMismatchError`/`TupleLengthMismatchError`, **span-free**; M2's job is to **add `Span()`** and the new error kinds it needs. |
> | `coalesce` does occurrence analysis / quantifier prefix | M1 `coalesce` is **bound-inlining only** (positive ⇒ ∪ lowers, negative ⇒ ∩ uppers; empty ⇒ `never`/`unknown`). Occurrence analysis + `<T0,…>` prefix are M3. |
>
> The single biggest impact: **M2 cannot generalize**, so polymorphic rendering
> (`fn <T0>(x: T0) -> T0`) and the recursive-group `LetRecGroup` lift both move
> their *generalization* aspect to M3. M2's SCC handling (PR-5) still orders and
> infers groups, but binds them monomorphically (see §3.7 note).

- **Spike core** (`internal/simplesub/`, the throwaway PoC): `typeTerm` (the
  recursive switch M2 re-targets), `constrain`, `coalesce`, `simplify`, levels
  via `scheme.go`, `LetRecGroup`. **Note:** the spike is the *reference shape*,
  but M1 re-homed only a subset onto `soltype`/`solver` (see the deltas box);
  the scheme/generalization parts the spike has are M3, not available to M2.
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
  flag is M8.

## 3. Design

### 3.1 Package layout

M2 code goes in `internal/solver/` (the engine package M1 created); the type
representation it consumes lives in the **separate** `internal/soltype/` package
(M1). M2 adds no files to `soltype` — it only imports it.

```
internal/solver/        # M1: context.go, constrain.go, coalesce.go, info.go, errors.go
  infer.go        # M2: constraint-generating walk over *ast.Module (production typeTerm)
  infer_expr.go   # M2: per-expression-kind constraint generation
  infer_decl.go   # M2: VarDecl / FuncDecl → bindings, SCC group inference
  module.go       # M2: InferModule(s): dep_graph SCC ordering + drive the walk
  scope.go        # M2: Scope / Binding / Namespace (own, not type_system)
  prelude.go      # M2: global scope — operator/builtin schemes + placeholder stdlib type bindings (§3.8)
  errors.go       # bridge errors (unbound name, unsupported node) with provenance/spans
  // (soltype core, Info side table, printer: from M1)
  *_test.go       # M2: table-driven tests (single- + multi-file, in-memory); see §3.6
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
| `Block` | type each stmt in source order; result = last expr (or `void`). Body decls are `VarDecl`-only; redeclaration rebinds (below) | yes |

Every node that produces a type records it in M1's `Info` side table via the
(unexported, same-package) `setType(node, t)` — which is why the M2 walk lives
in `package solver`. `Info` is the single source of truth for node→type (the AST
stays untouched — no `InferredType()` writes; that is the AST-decoupling
decision). Nodes outside the M2 subset emit an "unsupported in M2" error.

**Statement-level declarations in bodies — `VarDecl` only.** Inside a
function/method body the only declaration a statement may introduce is a
`VarDecl` (`val`/`var`). A body-level `DeclStmt` wrapping any other decl kind —
`FuncDecl`, `TypeDecl`, `ClassDecl`, `EnumDecl`, `InterfaceDecl`, … — is a
**language-level error** (`BodyDeclNotAllowedError`, §3.7), *not* the
"unsupported in M2" subset gate: it is a deliberate, permanent simplification of
the language, so the body walk only ever folds a `VarDecl` into scope. No
expressiveness is lost — a function value inside a body is written as a `val`
bound to a `FuncExpr` (`val f = fn () { … }`), which is a `VarDecl`. Top-level
decls are unaffected: the module driver (§3.3) still infers
`FuncDecl`/`TypeDecl`/… through the dep graph.

**Redeclaration within a body is allowed.** A body may bind the same name more
than once, each occurrence with its own type:

```
fn foo() {
    val x: string = "hello"   // x : string here
    val x: number = 5         // x : number from here on
}
```

Each `val`/`var` introduces a **fresh, independent** binding: `inferVarDecl`
infers the initializer on its own and the body walk *overwrites* the name's slot
in the current `Scope` (plain map assignment via `defineValue`, §3.4). No
constraint links the old and new bindings — the second `x` is **not** constrained
`<:` the first — so the two types need not be compatible. Statements between the
decls see the earlier type; statements after the second see the later one (the
walk is strictly source-order within a block). This matches what the current
checker already does for `val`/`var` (it merges body bindings with `maps.Copy`,
an overwrite); M2 makes the behavior uniform and intentional rather than an
accident of which insertion path a decl kind happens to take.

```go
// infer_stmt.go — body-level DeclStmt: VarDecl only, redeclaration overwrites.
case *ast.DeclStmt:
    vd, ok := s.Decl.(*ast.VarDecl)
    if !ok {
        c.report(BodyDeclNotAllowedError{Kind: declKind(s.Decl), span: s.Span()})
        break
    }
    b := c.inferVarDecl(scope, lvl, vd)
    scope.defineValue(varName(vd), b) // overwrite ⇒ same-name redeclaration rebinds
```

(`varName(vd)` reads the `IdentPat` name — M2 binds `IdentPat`-only patterns,
mirroring M1's `IdentPat`-only `FuncParam`; destructuring `val`/param patterns
(`TuplePat`/`RecordPat`) arrive in **M4**, once record/tuple types exist.)

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
- **`resolver` is *not* on this path (decided — §7).** `internal/resolver/` only
  locates `@types/*` third-party TypeScript packages; the `val`/`fn` bar has no
  such imports, so **M2 skips `resolver` entirely**. `.d.ts` / stdlib / library
  type ingestion is M7.

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
not-found case is explicit at every call site. All three `Get*` are lexical
lookups: check this scope's own map, then walk the `parent` chain — they differ
only in which of the three sorts (`values`/`types`/`namespaces`) they consult.)

- `ValueBinding` — in M2, a name's **monomorphic** `soltype.Type` plus its
  source provenance (the production analogue of the spike's
  `ctx map[string]TypeScheme`, minus the scheme). M1 has no schemes, so binding
  carries a plain type; M3 swaps the field for a `TypeScheme` when generalization
  lands.
- The **value-position `IdentExpr`** path queries `GetValue`, then `GetNamespace`
  only to raise `NamespaceUsedAsValueError` (namespaces are a separate sort and
  never flow as values — design-notes §"The constraint-generating AST walk"). M2
  has **no** namespace *member* access (`Foo.bar`) — that's M4 (see the
  `inferIdent` note in §3.7). M2's `Namespace` is structure + the free-floating
  error only.
- `Namespace` is keyed by the qualified `BindingKey` namespace `dep_graph`
  records (`GetNamespace(key)`), which is how multi-file resolution lands.
  Cross-file references in M2's acceptance use **root-namespace short names**
  (`foo`, resolved through the module scope), not qualified `Foo.bar` access —
  the latter needs the M4 namespace-member lookup.

**M2 scope:** `values` + `namespaces` are what the `val`/`fn` + multi-file bar
needs. The `types` slot's shape lands now (cheap, and it's load-bearing for the
two-map test harness below), but populating it with real type aliases/classes is
M3+ work. Keep the rest deliberately small; it grows with later milestones.

**`defineValue` overwrites — redeclaration and rec-groups both rely on it.**
`defineValue(name, b)` is a plain insert into the current scope's `values` map:
if `name` is already bound *in this scope* it replaces the binding — it does
**not** panic or error the way the old checker's `setValue` does on a duplicate.
Two M2 paths depend on the overwrite: (a) body-level variable redeclaration
(§3.2), and (b) `inferComponent`, which binds each rec-group name twice — first
to its fresh var, then to its coalesced type (§3.7). The old checker's split
behavior — `val`/`var` overwrite via `maps.Copy` but a duplicate `fn` panics via
`setValue` — is **not** carried into M2: `defineValue` is uniformly overwrite,
and `fn`-as-a-body-statement is disallowed outright (§3.2), so that inconsistency
cannot arise.

### 3.5 Errors & provenance

- Bridge errors (`errors.go`): unbound name, unsupported node — carry source
  spans (`ast.Span`) taken from the offending AST node via `node.Span()`. (Note:
  the `internal/provenance/` package exports only the `Provenance` marker
  interface — `IsProvenance()` — *not* a span type; spans live in `internal/ast`.)
- Inference errors from the core (`constrain` failures) carry the offending
  node's span **directly on the error kind** (the `Span() ast.Span` field added
  to `SolverError`, §3.7) — M2 stamps it at the `constrain` call site from the
  AST node being walked. M2 does **not** look provenance up from a side table:
  the `Prov` provenance table (`Type → Origin`, the inverse of `Info`) is
  **deferred to M3+** ([02-design-notes.md](02-design-notes.md) §"Provenance side
  table"), so the richer "why this type" derivation chains it powers are a later
  milestone. (`ValueBinding.Source` is a separate, per-binding back-pointer to
  the *introducing* AST node — present in M2, unrelated to `Prov`.) Assert
  **full** messages in tests (CLAUDE.md).

### 3.6 Test harness (table-driven)

M2 has **one** test surface — table-driven `*_test.go` in the new package — and
**no** on-disk `fixtures/` directories. This satisfies the milestone's "given
`.esc` source, infer and assert the rendered binding types … its own assertions,
independent of the old checker" directly, and keeps M2 from standing up fixture
infrastructure that M8 owns deliberately.

- **Single-file cases:** `.esc` snippet → expected rendered binding type string
  (using the M1 `soltype` printer). The primary M2 surface — fast, no per-case
  package overhead, mirrors the spike's `simplesub_test.go` pattern and the
  checker-tests pattern in `internal/checker/tests/`.
- **Multi-file / dep-graph cases:** still table-driven, but the case supplies
  **several in-memory sources** instead of one. `ast.Module` already holds
  `Files []*File` (and `Sources`), and `InferModule`/`InferModules` take parsed
  modules — so a case parses each source string, assembles a multi-file `Module`
  (or several modules), drives `BuildDepGraph` across them, and asserts the
  rendered top-level types. This exercises the exact dep-graph-spanning-files
  path on-disk fixtures would, without `package.json` / file-discovery ceremony.

**Why no on-disk fixtures in M2.** Real `fixtures/<name>/lib/index.esc` (+
`package.json`) directories would pull in the `resolver` / file-discovery layer
and a second harness — both of which M2 otherwise defers (M2 doesn't engage
`resolver` unless a `.d.ts` import forces it, and doesn't wire into the compiler
entry points; that's M8). An M2 on-disk fixture wouldn't run the real pipeline
anyway — it'd run M2's own harness — so the directory layout is pure ceremony
here. M8 stands up the real fixture harness (sibling to
`cmd/escalier/fixture_test.go`) with differential triage; M2 leaves it there.

Assert inferred types as Escalier type-annotation strings; use `testify/require`
(CLAUDE.md). For tree-shaped assertions prefer `snaps.MatchInlineSnapshot` over
field-by-field drilling.

### 3.7 Sketched types & function signatures

Concrete shapes to anchor the PRs below. These are *sketches* — names and
fields will shift in review — but they pin down the surface area each PR owns.
All M2 code lives in `internal/solver`. Types prefixed `soltype.` come from the
`internal/soltype/` package (M1); `solver.`-level names without comment are M1's
engine; the rest are introduced by M2 (the PR that owns each is noted).

> **These sketches reflect M1 as shipped** (see §2 deltas box). Specifically:
> the carrier embeds M1's `*solver.Context` (there is no `Inferer`); there are
> **no schemes** (`ValueBinding` holds a plain `soltype.Type`, not a
> `TypeScheme`), **no `Instantiate`**, and **no `Prov`** — all M3-or-later. M2
> is monomorphic.

**The checker carrier (PR-1).** A per-inference-run struct wrapping M1's
`solver.Context` (the fresh-var counter + `Constrain`) and threading the `Info`
side table and accumulated errors. Method receiver for the whole walk.

```go
// infer.go
type checker struct {
    ctx  *Context  // M1 solver.Context: freshVar(level), Constrain(lhs, rhs) []SolverError
    info *Info     // M1 solver.Info: node → soltype.Type side table (unexported setType)
    errs []Error   // accumulated; mirrors the spike's []error threading
}

func newChecker() *checker

// freshAt allocates a fresh inference variable at the given level (delegates to
// ctx.freshVar(level)). No provenance recording in M2 — Prov is deferred.
func (c *checker) freshAt(lvl int) *soltype.TypeVarType

// constrain delegates to ctx.Constrain and appends any SolverErrors (wrapped
// with the offending node's span) to c.errs.
func (c *checker) constrain(n ast.Node, lhs, rhs soltype.Type)

// report appends a structured error; returns a placeholder type (e.g.
// &soltype.NeverType{}) so callers can `return c.report(...)` in value position.
func (c *checker) report(e Error) soltype.Type
```

**Scope / Binding / Namespace (PR-1).** Design-notes §"Scope / Binding" gives
the three-slot shape; the constructors and `define*` mutators are M2's. Because
M1 has **no schemes**, an M2 `ValueBinding` holds a plain monomorphic
`soltype.Type` — the `Scheme` field arrives in M3 with generalization:

```go
// scope.go
type ValueBinding struct {
    Type   soltype.Type          // M2: monomorphic. M3 replaces with a TypeScheme.
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
func (s *Scope) GetValue(name string) (ValueBinding, bool)      // all three walk parents
func (s *Scope) GetType(name string) (TypeBinding, bool)        // (lexical lookup: this scope, then parent chain)
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
`*Var` case crossed with design-notes §"The constraint-generating AST walk". In
M2 (monomorphic, no schemes) it returns the binding's type directly; the M3
note marks where `instantiate` slots in once schemes exist:

```go
func (c *checker) inferIdent(scope *Scope, lvl int, e *ast.IdentExpr) soltype.Type {
    if b, ok := scope.GetValue(e.Name); ok {
        t := b.Type // M2: monomorphic — return as-is.
                    // M3: t = c.instantiate(b.Scheme, lvl) (fresh copy per use).
        c.recordType(e, t) // wraps info.setType (unexported; same package)
        return t
    }
    if _, ok := scope.GetNamespace(e.Name); ok {
        return c.report(NamespaceUsedAsValueError{Name: e.Name, Span: e.Span()})
    }
    return c.report(UnknownIdentifierError{Name: e.Name, Span: e.Span()})
}
```

> **Namespace member access (`Foo.bar`) is M4, not M2.** In M2 the *only* thing a
> namespace ident can do is fail: there is no legal namespace-member position yet
> (M2's `MemberExpr` is value-only — `constrain(recv, Record{…})` — and there is
> no `IndexExpr`). So `inferIdent` raising `NamespaceUsedAsValueError` on *any*
> namespace name is correct for M2. M4 — where member access deepens and
> `IndexExpr` lands — adds qualified namespace access: a `resolvePath` that
> returns `Value | Namespace`, namespace branches in member/index access
> (`LookupValue`/`LookupNamespace` — a **direct**, non-lexical lookup in the
> namespace's own maps, unlike `Scope.Get*`), and at that point the
> `NamespaceUsedAsValueError` moves from `inferIdent` to the value-position
> consumer (so `Foo.bar` is allowed while `f(Foo)` / `f(A.B)` still error). M2
> keeps the namespace **structure** (for qualified `BindingKey` resolution) and
> the free-floating error; the *lookup* is M4.

**Declaration & module driver (PR-2 single-decl; PR-5 SCC).** Mirrors the old
checker's `InferDepGraph`/`InferComponent`, over `soltype`:

```go
// module.go
func InferModule(module *ast.Module) (*Scope, *Info, []Error)   // PR-2 (source-order), PR-5 (SCC)
func InferModules(modules []*ast.Module) (*Scope, *Info, []Error) // PR-6 (multi-file)

func (c *checker) inferDepGraph(scope *Scope, lvl int, g *dep_graph.DepGraph) // PR-5
func (c *checker) inferComponent(scope *Scope, lvl int, g *dep_graph.DepGraph, // PR-5
    component []dep_graph.BindingKey)

// infer_decl.go
func (c *checker) inferVarDecl(scope *Scope, lvl int, d *ast.VarDecl) ValueBinding   // PR-2
func (c *checker) inferFuncDecl(scope *Scope, lvl int, d *ast.FuncDecl) ValueBinding // PR-3
```

`inferComponent` orders an SCC and binds it (PR-5); the singleton non-recursive
case is the same code with a one-element slice. **M2 binds the group
monomorphically** — it still uses the spike's level discipline (fresh var per
binding, infer bodies at `lvl+1`, all group members visible before any body so
recursive references resolve) but **does not generalize**: M1 has no schemes, so
the group's vars stay as their (coalesced) monomorphic types. The
generalization step — wrapping each in a `PolyScheme` at the outer `lvl` so
`freshenAbove` quantifies the `lvl+1` vars — is **M3**, and is what turns these
into reusable polymorphic bindings. M2's value is correct *ordering* and
*recursive resolution*; M3 adds the polymorphism on top.

```go
func (c *checker) inferComponent(scope *Scope, lvl int, g *dep_graph.DepGraph,
    component []dep_graph.BindingKey) {
    inner := lvl + 1
    // 1. fresh var per binding at the inner level, all visible before any body
    //    (so mutually-recursive references resolve through the var).
    vars := map[dep_graph.BindingKey]*soltype.TypeVarType{}
    for _, key := range component {
        v := c.freshAt(inner)
        vars[key] = v
        scope.defineValue(key.Name(), ValueBinding{Type: v})
    }
    // 2. infer each body at the inner level, constrain body <: its var
    for _, key := range component {
        for _, d := range g.GetDecls(key) {
            body := c.inferDeclBody(scope, inner, d)
            c.constrain(d, body, vars[key])
        }
    }
    // 3. M2: rebind each name to its (monomorphic) coalesced type.
    //    M3 replaces this with generalization at the outer level:
    //      scope.defineValue(key.Name(),
    //          ValueBinding{Scheme: &soltype.PolyScheme{Level: lvl, Body: vars[key]}})
    for _, key := range component {
        t := coalesce(vars[key], soltype.Positive)
        scope.defineValue(key.Name(), ValueBinding{Type: t})
    }
}
```

**Errors (PR-1).** M1 already ships a `SolverError` interface
(`isSolverError()` + `Message()`) with span-free concrete kinds
(`CannotConstrainError`, `FuncArityMismatchError`, `TupleLengthMismatchError`).
The M1 milestone notes M2 is where `Span()` arrives. So M2's error work is:
**(a)** add `Span() ast.Span` to the `SolverError` interface and backfill it on
M1's existing kinds (carrying the offending node's span), and **(b)** add the
M2-specific bridge kinds below. Assert full messages in tests.

```go
// errors.go — M2 extends M1's SolverError
type SolverError interface {  // M1 interface, + Span() added in M2
    isSolverError()
    Message() string
    Span() ast.Span           // NEW in M2
}
// new M2 kinds (each implements SolverError):
type UnknownIdentifierError struct{ Name string; span ast.Span }
type NamespaceUsedAsValueError struct{ Name string; span ast.Span }
type UnsupportedNodeError struct{ Kind string; span ast.Span } // M2-subset guard
type BodyDeclNotAllowedError struct{ Kind string; span ast.Span } // body decls are VarDecl-only (§3.2); permanent language rule, not a subset gate
```

(The plan earlier used a separate `Error` interface; M1 having already
established `SolverError` means M2 should extend *that*, not introduce a
parallel hierarchy. The `Error`/`c.report` naming in the carrier sketch reads
against this `SolverError` type.)

**Test harness (PR-2 single-file table; PR-6 multi-file table).**

```go
// infer_test.go — table harness, single-file
func inferSource(t *testing.T, src string) (values, types map[string]string)
// returns name → rendered soltype string, via parser + InferModule + soltype printer

// infer_test.go — table harness, multi-file (in-memory; no on-disk fixtures)
func inferSources(t *testing.T, srcs map[string]string) (values, types map[string]string)
// parses each named source, assembles a multi-file Module / modules, drives
// BuildDepGraph + InferModule(s), returns rendered top-level types
```

### 3.8 Stdlib-type placeholder seeding

The M1-era edit to `01-milestones.md` added a new M2 acceptance clause: names
that *downstream* type rules will reference must already **resolve** to a
`soltype.Type` by the end of M2 — even though the rules that consume them, and
the real type definitions, land later (real ingestion is its own milestone, M7).
The names called out:

| Name | Consumed by (later milestone) |
|------|-------------------------------|
| `Promise<T>` | `await e` |
| `Iterable<T>` / `AsyncIterable<T>` | `for (x in xs)` / `for await (x in xs)` |
| `Generator` / `AsyncGenerator` | `yield e` |
| `IteratorResult<T>` (`{value, done}`) | iteration built-ins |

**M2 seeds placeholders only; real ingestion is M7.** M2 does **not** read the
real stdlib decls and does **not** implement `await`/`for-in`/`yield`. Two hard
reasons it can't: (a) the existing stdlib ingestion lives inside
`internal/checker/` and produces `type_system.Type` — off-limits behind the M2
gate; and (b) these are *generic* type references (`Promise<T>`), and M1's
`soltype` has **no** generic-type-reference / alias node yet (that's M3/M4). So
M2's whole job here is to **hand-seed placeholder `TypeBinding`s** — opaque
stubs (e.g. `soltype.Unknown`) for exactly `Promise`, `Iterable`,
`AsyncIterable`, `Generator`, `AsyncGenerator`, `IteratorResult` — into the
prelude `Scope.types` (§3.4), so a *reference* to the name resolves without an
unbound-name error. Nothing structural; no arity.

The **real** ingestion — retargeting `internal/checker/`'s
`infer_stdlib_import` / `infer_import` + `interop` onto `soltype` — is its own
milestone, **M7 (library type resolution)**, sequenced after M6 once generics
(M3), objects (M4), classes (M5), and unions (M6) exist to represent the lib
types. M7 swaps these placeholders for real structures. (See
[01-milestones.md](01-milestones.md) M7.)

Because it's just a hand-seeded table, this belongs with the **prelude
construction in PR-1** (alongside the operator/builtin bindings the
`BinaryExpr` walk needs), not the multi-file PR — it has no dependency on
dep-graph or multi-file resolution.

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
  test cases will want; isolated so it can slip without blocking the SCC/multi-file
  work.
- The old PR-3 (SCC) becomes **PR-5**, the old PR-4 (multi-file) becomes
  **PR-6** — unchanged in content, renumbered.

### PR dependency graph

```text
        M1 shipped: soltype/ + solver/ (Context, Constrain,
        coalesce, Info, SolverError, Print) — monomorphic; no schemes
                        │
                        ▼
                ┌──────────────────┐
                │ PR-1  carrier +  │   checker{ctx,info,errs}, Scope/Binding/Namespace,
                │ scope + leaves   │   SolverError+Span, inferExpr(lits, idents)
                └───────┬──────────┘
                        │
            ┌───────────┴───────────┐
            ▼                       ▼
   ┌──────────────────┐   ┌───────────────────────┐
   │ PR-2 decl driver │   │ PR-3 fn / call /      │   (PR-2 ∥ PR-3:
   │ + table harness  │   │ block walk            │    independent given PR-1)
   │ (val end-to-end) │   │ (fn end-to-end)       │
   └───────┬──────────┘   └──────────┬────────────┘
           │                         │
           │   ┌─────────────────────┤
           ▼   ▼                     ▼
   ┌──────────────────┐   ┌───────────────────────┐
   │ PR-5 dep_graph   │   │ PR-4 objects /        │   (PR-4 ∥ PR-5:
   │ SCC (monomorphic)│   │ member access         │    both need PR-2+PR-3)
   └───────┬──────────┘   └──────────┬────────────┘
           │                         │
           └───────────┬─────────────┘
                       ▼
           ┌─────────────────────────┐
           │ PR-6 multi-file (x-mod) │   closes M2 exit criteria
           │ resolution + table tests│
           └─────────────────────────┘
```

Edges are hard dependencies (the target imports/uses symbols the source
introduces). The two `∥` pairs (PR-2 ∥ PR-3, and PR-4 ∥ PR-5) have no edge
between them and can be developed/reviewed in parallel. PR-6 is the only PR that
needs *both* upstream branches merged (it asserts end-to-end over in-memory
multi-file sources, which exercises decls, functions, and SCC ordering together).

## 5. PR breakdown

> Each PR lists its **owned files**, the **sketches from §3.7** it implements,
> its **tests**, and an **exit** line. "LoC" estimates are non-test code.

### PR-1 — Checker carrier + Scope + leaf expressions  (~250 LoC)
- `infer.go`: the `checker` struct (wrapping M1's `*Context` + `*Info`),
  `newChecker`, `freshAt`, `constrain`, `report`, `recordType`; `inferExpr`
  dispatch with only the leaf cases wired.
- `scope.go`: `Scope`/`ValueBinding`(monomorphic `Type`)/`TypeBinding`/
  `Namespace` + `NewScope`, `Child`, `defineValue`, `GetValue`/`GetType`/
  `GetNamespace`.
- `errors.go`: add `Span() ast.Span` to M1's `SolverError` and backfill it on
  M1's existing kinds; add `UnknownIdentifierError`, `NamespaceUsedAsValueError`,
  `UnsupportedNodeError`, `BodyDeclNotAllowedError`.
- `infer_expr.go`: `inferLiteral`, `inferIdent`; all other `ast.Expr` kinds fall
  through to `UnsupportedNodeError` (no panic).
- `prelude.go`: build the global/prelude `Scope` — operator/builtin value
  schemes the `BinaryExpr` walk needs, plus the **placeholder stdlib type
  bindings** (§3.8): opaque `soltype` stubs for `Promise`/`Iterable`/
  `AsyncIterable`/`Generator`/`AsyncGenerator`/`IteratorResult` so references
  resolve. Hand-built, no checker/`type_system` import; real ingestion is M7.
  The operator schemes are a near-mechanical port of the old checker's
  `addOperatorBindings` (`internal/checker/prelude.go`) from `type_system`
  constructors to `soltype` ones — **every operator is monomorphic over
  primitives** (`+`/`-`/`*`/`/`: `fn(number, number) -> number`; `<`/`>`/`<=`/
  `>=`: `fn(number, number) -> boolean`; `==`/`!=`: `fn(unknown, unknown) ->
  boolean`; `&&`/`||`: `fn(boolean, boolean) -> boolean`; `!`:
  `fn(boolean) -> boolean`; `++`: `fn(string, string) -> string`), so they need
  no generics/unions/lib types and belong here, not in a later milestone.
  Richer forms (bigint arithmetic, string `<`, generic equality) are refinements
  that land with their enabling milestone (overloads M3, unions M6), not M2.
- Tests: literal → rendered type; identifier resolves to a pre-seeded binding's
  type; a stdlib placeholder name resolves without an unbound-name error;
  unbound identifier and namespace-as-value → full-message errors with spans.
- **Exit:** the walk types the literal/identifier subset against `soltype`/`Info`;
  the prelude resolves operators and the placeholder stdlib names; every other
  node fails cleanly.

### PR-2 — Single-module decl driver + table harness  (~250 LoC)
*(depends on PR-1; parallel with PR-3)*
- `infer_decl.go`: `inferVarDecl` (initializer typed via `inferExpr`, coalesced
  into a **monomorphic** `ValueBinding` — no generalization in M2).
- `module.go`: first `InferModule` for one module, decls in **source order**
  (no SCC yet), seeding the module `Scope` and `Info`.
- `infer_test.go`: the `inferSource` table harness.
- Tests: `val x = 5` ⇒ `5`/`number` (per M1 widening); `val y = x` referencing an
  earlier decl; forward reference → (documented) error until PR-5 adds ordering.
- **Exit:** top-level `val` decls with literal/identifier initializers infer
  end-to-end (monomorphic) and render correctly via the harness.

### PR-3 — Function / application / block walk  (~300 LoC)
*(depends on PR-1; parallel with PR-2)*
- `infer_expr.go`: `inferFuncExpr` (n-ary `Function`, fresh var per param),
  `inferCall` (`constrain(callee <: Function{args, fresh})`), `inferBlock` /
  `inferStmt` (sequence; result = last expr or `void`).
- `infer_decl.go`: `inferFuncDecl` (reuses `inferFuncExpr` on the decl's sig+body).
- Tests: an **annotated** `fn` (e.g. `fn (x: number) { x }` ⇒ `fn(x: number) ->
  number`), application of it, a block with a `return`, arity mismatch on a
  direct call → full-message error. (Un-annotated polymorphic `fn` renders with
  fresh vars, **not** `<T0>` — generalization is M3.)
- **Exit:** `fn` decls and calls infer end-to-end, monomorphically (let-
  polymorphism and `<T0>` rendering are M3, which M1 deferred schemes to).

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
  monomorphic SCC bind sketched in §3.7: fresh var per binding, infer bodies at
  `lvl+1`, group members visible before any body).
- Replace PR-2's source-order loop with SCC ordering. **No placeholder/patching
  phase.** **No generalization** — bindings are bound monomorphically; the
  `PolyScheme` generalization step is M3 (M1 deferred schemes).
- **Verify `coalesce` terminates on recursive groups.** A mutually-recursive SCC
  can build a cyclic var↔var bound graph, and M1's `coalesce` has no `seen`-guard
  (deferred to M3). Confirm guard-free `coalesce` terminates on the recursive
  tests below; if an ungrounded group loops, pull the M3 `seen`-guard forward (§7).
- Tests: self-recursive `fn` (resolves, monomorphic); mutually-recursive `fn`
  pair; a decl that forward-references a later decl now resolves via SCC
  ordering (the PR-2 documented-error case flips to success).
- **Exit:** recursive and out-of-order top-level decls infer correctly in one
  module (monomorphically).

### PR-6 — Multi-file (cross-module) resolution + multi-file table tests  (~250 LoC)
*(depends on PR-4 + PR-5)*
- `module.go`: `InferModules` over multiple parsed modules; build the dep graph
  across them; cross-module references resolve through the qualified-`BindingKey`
  `Namespace` (`dep_graph.GetNamespace`). **M2 does not engage `internal/resolver`**
  (it resolves `@types/*` third-party packages; M2 has no such imports — that's
  M7; see §7).
- `infer_test.go`: the `inferSources` multi-file table helper (§3.7) — in-memory
  sources, **no** on-disk `fixtures/` directories (those are M8; see §3.6).
- Tests: a two-file table case where file B references a `val`/`fn` from file A
  by **root-namespace short name** (not qualified `Foo.bar`) and the inferred
  types render correctly end-to-end. (Qualified namespace-member access is M4;
  stdlib placeholder names are seeded in PR-1, §3.8 — not this PR; real ingestion
  is M7.)
- **Out of scope (→ M4):** qualified namespace-member access (`Foo.bar`,
  `Foo["x"]`) and the `resolvePath` `Value | Namespace` machinery. M2 resolves
  cross-file references through the dep-graph namespace structure under
  short/qualified `BindingKey`s, but the *expression-level* `Foo.bar` walk is M4.
- Update `01-milestones.md` M2 status.
- **Exit (M2 exit criteria):** multi-file module resolves via the dep graph;
  top-level `val`/`fn` infer correct rendered types end-to-end (monomorphic).

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

- **Package name** is settled and shipped: `internal/solver/` (engine, where M2
  code lives) + `internal/soltype/` (types). No open decision here.
- **Monomorphic-`ValueBinding` forward-compat — DECIDED: (a).** M2's
  `ValueBinding` holds a plain `soltype.Type`; M3 swaps it for a `TypeScheme`.
  Ship the plain-`Type` field now and take the M3 churn — it's mechanical, and
  the alternative (a thin accessor M3 re-points) over-abstracts before schemes
  exist.
- **`coalesce` at binding time — DECIDED: keep it (coalesce at SCC completion).**
  The binding-vs-render *timing* is correctness-neutral: a binding is stored at
  **Positive** polarity, `coalesce(var, Positive)` reads only *lower* bounds, and
  every downstream *use* adds *upper* bounds (`f(x)`, `x.foo`, `x+1`, `val y=x`
  all upper-bound `x`), so the Positive view is stable after the defining SCC —
  binding-time and render-time coalesce yield the identical type. Keep
  coalesce-at-binding anyway because it (i) **isolates SCCs** (a later SCC gets a
  frozen, var-free type and can't mutate an earlier binding's vars), (ii) makes
  `ValueBinding.Type` stable/inspectable, and (iii) is the natural monomorphic
  stand-in for M3's `PolyScheme` — both freeze the binding at its definition
  boundary, so the (a) field-swap above is the only M3 change. Binding the raw
  var renders identically but leaves live shared vars in scope (not simpler).
  **Caveat for PR-5:** `coalesce` has **no recursion guard** (M1 deferred the
  `seen`-set to M3), but a mutually-recursive SCC can build a cyclic var↔var
  bound graph (`constrain` appends var-to-var bounds; it terminates on cycles via
  its own coinductive `seen`-set, `coalesce` does not). Well-typed recursion
  usually grounds the return to a concrete type so the cycle collapses, but an
  ungrounded recursive group could loop — PR-5 must verify guard-free `coalesce`
  terminates on its recursive-SCC tests, or pull the M3 `seen`-guard forward.
- **How much namespace resolution to drive in M2 — DECIDED: the minimum.** M2
  populates the `Namespace` slot from `dep_graph` and resolves cross-file
  references by **short name through the scope chain** (the multi-file acceptance:
  file B references a top-level `val`/`fn` in file A by short name). M2 does
  **not** implement `Foo.bar`/`ns.foo` member access, nested-namespace access, or
  qualified cross-namespace resolution — all **M4** (the `resolvePath`
  `Value | Namespace` machinery; see §3.7 and PR-6). M2's namespace surface is
  the slot + qualified `BindingKey` structure + the free-floating-namespace error.
- **Whether M2 needs `internal/resolver` — DECIDED: no, M2 skips it.** `resolver`
  resolves `@types/*` third-party TypeScript packages
  (`ResolveTypesPackage`/`GetTypesEntryPoint`); M2 has no such imports, so it is
  entirely off the M2 path and `dep_graph` alone satisfies the milestone's
  "dep_graph/resolver" phrasing. `.d.ts` / third-party typing lands in M7.

## 8. M2 exit checklist

- [ ] Constraint-generating walk over `*ast.Module` produces `soltype` and
      populates `Info` (no AST `InferredType()` writes).
- [ ] Package-owned `Scope`/`Binding`/`Namespace` in `internal/solver/` (no
      `type_system` reuse).
- [ ] Top-level `val`/`fn` from real source infer correct **monomorphic**
      rendered types end-to-end (table harness). *(Polymorphic `<T0>` rendering
      is M3 — M1 deferred schemes/generalization.)*
- [ ] Multi-file module resolves via the dep graph (in-memory multi-file table
      test; no on-disk fixtures — those are M8).
- [ ] Recursive SCC groups infer (monomorphically) with no placeholder/patching
      phase.
- [ ] Stdlib type names (`Promise`/`Iterable`/`Generator`/`IteratorResult`)
      resolve to **placeholder** `soltype` stubs without unbound-name errors
      (seeded in the prelude; real ingestion is M7).
- [ ] `SolverError` gained `Span()`; M2 error kinds carry spans.
- [ ] No imports of `internal/checker/` or `internal/type_system/` from the new
      package (gate honored).
- [ ] `01-milestones.md` M2 status updated.
