# M1 — Implementation plan: package skeleton + `soltype`

Concrete, sequenced plan for landing **M1** of the SimpleSub migration
([01-milestones.md](01-milestones.md#m1--package-skeleton--soltype)). M1 stands
up the new checker's package and its type representation — the structural-core
subset — by **promoting proven spike code** (`internal/simplesub/`,
[#676](https://github.com/escalier-lang/escalier/pull/676)) into a production
package, while cutting the spike's two expedient `type_system` imports: the
spike's `coalesce.go` returns `type_system.Type` (built with constructors like
`type_system.NewFuncType` / `NewUnionType` / `NewTypeRefType`) and renders that
output by calling `type_system.PrintType`. M1 replaces every such constructor
call with a native `soltype` node so coalescing returns `soltype.Type`, and
ships its own `soltype/print.go`. The result is a `soltype`/`solver` package
pair with **zero** `type_system` imports — though several surfaces are
deliberately **inspired by** `type_system` (node names mirror the `…Type`
convention, the printer mirrors `type_system.PrintType`'s surface syntax so M8's
differential harness can string-compare outputs, and M1 error wording matches
the spike's, which echoes the old checker's). None of that is shared code; the
only sharing actually planned (per [02-design-notes.md](02-design-notes.md)
Settled Decision #4) is reusing the old checker's **diagnostic types**, and
even that is deferred past M1 since there are no source spans yet (see §2.4).

This document covers scope, the spike→production deltas, the PR breakdown with
sequencing, per-PR acceptance, and risks. It assumes the context in
[00-overview.md](00-overview.md) and [02-design-notes.md](02-design-notes.md).

---

## 1. Scope

### In scope (M1)

Per the milestone, M1 delivers the **structural core**:

1. **New package** `internal/solver/` (sibling to `internal/checker/`; leaf name
   settled, [02-design-notes.md](02-design-notes.md) §"Settled decisions" #1)
   with a `internal/solver/soltype/` subpackage for the type representation.
2. **`soltype` core types** promoted from the spike's `SimpleType`:
   - `TypeVarType` — bound-list inference variable (`id`, `level`,
     `lowerBounds`, `upperBounds`).
   - `PrimType`, `LitType`, `FuncType` (multi-arg), `TupleType`,
     `Void`, plus the lattice bounds `NeverType` (⊥) and `UnknownType` (⊤) —
     these two are fundamental to the subtyping lattice (they're the coalesced
     output of an empty-bounds single-polarity variable), so they belong in M1
     even though the spike emits them via `type_system`.
   - `UnionType` and `IntersectionType` — needed by Delta #1's `combine` when a
     variable has two or more distinct bounds (positive ⇒ union of lowers,
     negative ⇒ intersection of uppers). M1 ships the nodes and the printer
     cases for them so coalescing can return native `soltype.Type` in every
     case. **Subtyping rules** for union/intersection in `constrain` remain
     deferred to M6 — these nodes only appear in *coalesced output*, not as
     `constrain` inputs.
3. **The constraint engine** — `constrain(lhs <: rhs)` with the coinductive
   `seen`-cache, the structural cases for the M1 type set, the variable cases
   (bound-append + transitive propagation), and **levels + extrusion**.
4. **Bound-inlining coalescing** — `coalesce(t, pol)` walks a bound-carrying
   `soltype.Type` and returns a *coalesced* `soltype.Type` by inlining every
   `TypeVarType` to its bounds (positive ⇒ union of lowers, negative ⇒
   intersection of uppers; empty ⇒ `never`/`unknown`). No bipolar-variable
   retention, no occurrence analysis, no named-type-param refs — all deferred
   to M3 with the rest of the polymorphism-rendering story (§3.3).
5. **A `soltype` printer** — `soltype.Type` → Escalier type-annotation string
   for the M1 type set, **its own**, not `type_system.PrintType`. No
   `<T0, …>` quantifier prefix in M1 (no named refs to collect).
6. **The `Info` side table** — `map[ast.Node]soltype.Type` + `TypeOf`/`setType`.

### Explicitly out of scope (deferred to later milestones)

| Deferred | Milestone | Why not M1 |
|---|---|---|
| AST-driven inference walk (`infer.go`), parser/resolver bridge | M2 | M1 builds terms by hand, exactly as the spike does. |
| `Scope`/`Binding`/`Namespace` | M2 | No name resolution until the bridge exists. |
| Let-generalization machinery (`instantiate`/`freshenAbove`, `Scheme`s) | M3 | No polymorphism in M1; the parser bridge (M2) provides no `let`-bindings that need generalization. |
| **The entire polymorphism-rendering bundle** — bipolar variable retention, occurrence analysis (`analyze`), named-type-param ref node (`TypeRefType`), quantifier-prefix collection in the printer, the identity headline test | M3 | See §3.3 — these all hang off the same design question (what node represents a named ref, alias vs param) and should land together once M3's `Scheme`/alias context exists to inform the choice. M1 ships a coalescer that always inlines vars to bounds. |
| **Co-occurrence variable merging** (`collectCoOcc`/`mergeCoOccurring`/union-find) | M3 | Hangs off occurrence analysis; lands as part of the M3 polymorphism-rendering bundle above. |
| Records, `RefType`/`mut`, **lifetimes** | M4 | First lifetime-carrying types. |
| `exact` flag on formers + the inexact `constrain` arms | M3 (functions), M4 (records, tuples), M6 (unions) | M1 stands up bare `FuncType`/`TupleType` without the flag. Because Escalier is **exact-by-default**, M1 implements each former's *exact* rule uniformly — so each M1 constraint rule is the **exact "one side"** of the eventual exact/inexact split: M1's `FuncType` "same-arity required" is the *exact-function* case (the inexact fewer-params-is-subtype arm lands in M3 with the flag); M1's `TupleType` "same length, element-wise covariant" is the *exact-tuple* case (the inexact arm `longer <: shorter` lands in M4 alongside `RecordType`). Functions and tuples thus follow the **same trajectory** — ship exact now, add the inexact arm with the flag later. See [01-milestones.md](01-milestones.md) §M4 "Exactness flag on records and tuples." |
| Classes, union/intersection **subtyping rules** in `constrain`, type-level operators | M5/M6/M9 | M1 ships `UnionType`/`IntersectionType` *nodes* for coalesced output, but their lattice rules in `constrain` land in M6. |
| `Prov` provenance side table, `Probe` speculation API | M3+/as-needed | M1 has no speculation and no error-message provenance yet. |

### Reversibility

M1 is **purely additive**: it creates new files in a new package and edits
**zero** existing files. The old checker is untouched; there is no flag to flip
yet (that's M8). `go build ./...` and `go test ./...` stay green throughout.

---

## 2. Differences from the spike

The spike (`internal/simplesub/`) already implements every M1 mechanism and is
the reference implementation, so most of M1 is a faithful copy-with-rename. But
the spike was a *throwaway proof-of-concept* (its own `doc.go` says so), and it
took several shortcuts and skipped several project conventions that the
production package must not. This section enumerates **every** intended
difference, grouped as:

- **§2.1** file→file mapping (what moves where),
- **§2.2** the two output deltas (the only genuinely *new* code),
- **§2.3** representation & API differences,
- **§2.4** convention cleanups the spike skipped,
- **§2.5** what is deliberately *not* carried over.

A reviewer comparing an M1 PR against the spike should be able to account for
any diff by one of the entries below; anything else is unintended drift.

### 2.1 File→file mapping

Most of M1 is a faithful copy-with-rename. The files that map directly:

| Spike file | M1 destination | Notes |
|---|---|---|
| `polarity.go` | `soltype/polarity.go` | Copy verbatim. Lives in `soltype` (not `solver`) so `soltype.TypeVarType.BoundsAt` can take a `Polarity` argument without `soltype` importing `solver`. |
| `types.go` (M1 subset) | `soltype/type.go` | Keep `Variable`→`TypeVarType`, `Primitive`→`PrimType`, `Literal`→`LitType`, `Function`→`FuncType`, `Tuple`→`TupleType`, `Void`; add `NeverType`/`UnknownType` (lattice ⊥/⊤, which the spike emits via `type_system`); add `UnionType`/`IntersectionType` (needed by Delta #1's `combine` — the spike emits these via `type_system.NewUnionType`/`NewIntersectionType`). **Reshape `PrimType` and `LitType` to mirror `type_system`** (closed `Prim` enum instead of `Name string`; sealed `Lit` interface with `NumLit`/`StrLit`/`BoolLit` concretes instead of a flat `{Kind, Str, Num, Bool}` struct) — see §2.3 row 3. **Drop** `Record`/`Mut`/`Alias` (M4) and the spike's `TypeRefType` role (M3 — see §3.3). Trim `levelOf`/`containsVariable` to the M1 cases — this also drops their `*ResidualOp` arms (the `ResidualOp` type itself is defined in the spike's `residual.go`, not `types.go`, and is M9). |
| `constrain.go` | `solver/constrain.go` | Keep the prim/literal/function/tuple/variable cases + `extrude`. Drop the union/intersection lattice rules **that fire on `UnionType`/`IntersectionType` inputs to `constrain`** (distributivity laws like `A \| B <: C` ⟹ `A <: C ∧ B <: C`), and drop the record/mut/alias cases (re-added in their milestones). The implicit lattice — bounds on `TypeVarType` (lowers semantically join, uppers meet) — is fully present in M1; only the explicit-node rules go. M1 ships `UnionType`/`IntersectionType` nodes in `soltype` for coalesced output, but no path in M1 feeds them back as `constrain` inputs (no user annotations until M2, scheme bounds carry raw `TypeVarType`s, coalesced output isn't re-constrained). |
| `simplify.go` | — | Not M1. Both `analyze` (occurrence analysis) and co-occurrence merging land in M3 as part of the polymorphism-rendering bundle (§3.3). |
| `coalesce.go` | `solver/coalesce.go` | **Delta #1 below.** Significantly trimmed vs. the spike — no occurrence/`bipolar` branching, no `nameFor`/`inProc`, just inline-to-bounds. |
| (the printer the spike borrowed) | `soltype/print.go` | **Delta #2 below.** |
| `scheme.go`, `infer.go`, `lifetime.go`, `typeops.go`, `residual.go`, `regularity.go`, `lazy.go` | — | Not M1. |

### 2.2 The two output deltas (the only new code)

These two items are the reason M1 is *not* a pure `sed`-rename: the spike leaned
on `type_system` for both its coalescing output and its printing, and M1 must
sever both.

#### Delta #1 — coalescing targets `soltype`, not `type_system`

The spike's `coalesce.go` produces `type_system.Type`
(`type_system.NewUnionType`, `NewFuncType`, …) — an expedient shortcut so spike
output could be string-compared against the old checker's tests. M1 **cuts that
dependency**: coalescing takes a bound-carrying `soltype.Type` and returns a
*coalesced* `soltype.Type` in which every `TypeVarType` is inlined to its
bounds (positive ⇒ union of lowers, negative ⇒ intersection of uppers; empty
positive ⇒ `never` — the identity of `|` and the lattice ⊥; empty negative ⇒
`unknown` — the identity of `&` and the lattice ⊤).

Unlike the spike, M1's coalescer is **uniformly inlining** — there is no
bipolar-variable retention, no occurrence-analysis input, no named-ref output
node. The spike's bipolar branch (and its `type_system.NewTypeRefType(nil,
"T0", nil)` emission) is deferred to M3, where it lands alongside `Scheme`
machinery and alias references so the named-ref node can be designed informed
by both use cases (§3.3, and §1 out-of-scope "polymorphism-rendering bundle").

This delta is small in M1: it's a line-for-line port of the spike's coalesce
arms with each `type_system.New…` call replaced by the corresponding
`soltype` constructor.

#### Delta #2 — a native `soltype` printer

M1 must ship `soltype/print.go` rendering Escalier type-annotation syntax
directly from `soltype.Type` — the spike never had this (it leaned on
`type_system.PrintType`). The printer renders the M1 coalesced type set —
`PrimType`/`LitType`/`FuncType`/`TupleType`/`Void`/`NeverType`/
`UnknownType`/`UnionType`/`IntersectionType` — in Escalier surface syntax
(`number`, `"hello"`, `fn (x: T) -> U`, `[number, string]`, `never`,
`unknown`, `number | string`, `number & string`).

There is **no `<T0, …>` quantifier prefix in M1**: no named-ref node exists
to collect, since coalescing always inlines variables (Delta #1). The
quantifier-prefix machinery lands in M3 with the rest of the
polymorphism-rendering bundle.

Mirror `type_system.PrintType`'s surface syntax so the two checkers' rendered
types are comparable in M8's differential harness, but share **no code** with
it.

> **Two renderers, distinct jobs.** Keep this printer separate from the spike's
> `describe()` (§2.3). `describe` renders a *raw, uncoalesced* `soltype.Type`
> mid-`constrain` for error messages (`t0`, `function`, `number`); `Print`
> renders a *coalesced* type as user-facing Escalier syntax. They look similar
> but operate at different stages and must not be merged in M1.

### 2.3 Representation & API differences

| # | Spike | M1 production | Rationale |
|---|---|---|---|
| 1 | One flat `package simplesub` | Two packages: `solver` (engine) + `soltype` (representation + printer) | Matches [02-design-notes.md](02-design-notes.md) layout; lets the printer live with the types without importing the engine. |
| 2 | Terse names: `Variable`, `Primitive`, `Literal`, `Function`, `Tuple` | `TypeVarType`, `PrimType`, `LitType`, `FuncType`, `TupleType` | The design-notes names; consistent `…Type` suffix. |
| 3 | `Primitive { name string }`; `Literal { kind string; str string; num float64; bool bool }` — flat structs with string discriminators; `Function { params []SimpleType; paramNames []string; ret SimpleType }` — two parallel arrays for params | `PrimType { Prim Prim }` with closed `Prim` enum; `LitType { Lit Lit }` where `Lit` is a sealed interface with `NumLit { Value float64 }` / `StrLit { Value string }` / `BoolLit { Value bool }` concretes; `FuncType { Params []*FuncParam; Ret Type }` where `FuncParam { Pattern Pat; Type Type }` and `Pat` is a sealed interface (`IdentPat { Name string }` in M1; M4 adds destructuring concretes, M2 stays IdentPat-only) | Mirrors `type_system`'s shapes: closed enum catches mismatches at compile time (vs. silent typos like `"numbr"`); per-kind literal structs carry exactly the value field they need (vs. flat struct with two dead value fields per instance); single struct per param (vs. parallel-array bug-trap); `FuncParam.Pattern` is a sealed interface from day one so M4's destructuring lands as new `Pat` concretes without restructuring `FuncParam`. `soltype.Pat` is defined inside `soltype` (not imported from `ast`) to keep `soltype` ast-free. M1 omits `type_system.FuncParam.Optional` — no optional params in M1. |
| 4 | `SimpleType` interface (`isSimpleType()`) | `soltype.Type` interface (`isType()`) | One representation in production — no separate "SimpleType vs output type" split, since coalescing now stays within `soltype` (Delta #1). |
| 5 | `Inferer` struct carrying `varCounter`, `lifetimeCounter`, `paramLifetimes`, `written` | `solver.Context` carrying **only** `varCounter` (+ `freshVar`) | `lifetimeCounter`/`paramLifetimes` are M4 (lifetimes); `written` (field-write tracking) is M4 (records/`mut`). M1's `Context` is correspondingly lean. |
| 6 | Public entry points `Infer(term)` / `Render(...)` over a hand-built `Term` IR | **No public inference entry point in M1.** Tests drive `constrain` / `coalesce` / `Print` directly | The `Term` IR and `typeTerm` walk are the spike's stand-in for the parser bridge — replaced wholesale by the real AST walk in **M2**, not promoted. |

### 2.4 Production conventions

Per [CLAUDE.md](../../CLAUDE.md):

- **Sets.** Use `set.Set[T]` (`set.NewSet`, `set.FromSlice`) for all set-shaped
  state, including the `constrain` seen-cache (`set.Set[constraintKey]`).
- **Errors.** Use a sealed `SolverError` interface with **one concrete struct
  per error kind**, mirroring [internal/checker/error.go](../../internal/checker/error.go)'s
  shape (`CannotUnifyTypesError { T1, T2 type_system.Type }`, etc.). Each
  struct carries typed references to the involved `soltype.Type`s / nodes
  rather than just a rendered message string, so LSP/tooling consumers can
  programmatically inspect what the error refers to (e.g., "click on the
  offending type to navigate to its definition"). Each struct exposes a
  `Message() string` that produces the human-readable text — same wording as
  the spike's `fmt.Errorf` so tests remain stable. Errors are span-free in M1
  (no parser bridge until M2); M2 adds a `Span() ast.Span` method and rebases
  these onto the old checker's diagnostic types per
  [02-design-notes.md](02-design-notes.md) Settled Decision #4 (which may
  collapse the solver-local structs into the checker's existing kinds where
  they overlap).
- **Testing.** `testify/require` for assertions. For rendered-type
  assertions, use `snaps.MatchInlineSnapshot`; reserve `require.Equal` for
  short, stable strings. Assert **full** error messages, never substrings.
- **No shadowing.** Don't shadow Go builtins or imported package/type
  aliases.

### 2.5 What is deliberately *not* carried over

- **The `Term` IR + `typeTerm` walk** (`infer.go`) — the spike's hand-built
  expression IR is its stand-in for the parser; M2 replaces it with a real
  `*ast.Module` walk. M1 builds `soltype` terms directly in tests.
- **Everything past the structural core** — `Record`, `Mut`, `Alias`,
  `ResidualOp`, lifetimes (`lifetime.go`), type operators
  (`typeops.go`), residuals (`residual.go`), regularity (`regularity.go`), and
  the lazy/coinductive variant (`lazy.go`). Each lands in its own milestone with
  its own tests (and, for `mut`, its own gate).
- **The spike's documented `freshenAbove` lifetime-generalization limitation.**
  It's in `scheme.go`, which is M3 (let-polymorphism) work; the fix it calls for
  (lifetime levels) is M4. Neither `scheme.go` nor that limitation enters M1.
- **The `type_system` import.** After M1, the new package must have **zero**
  `type_system` references. (For scale: 4 non-test files in the *whole* spike
  import `type_system` — `coalesce.go`, `infer.go`, `typeops.go`, `residual.go` —
  but of the files M1 actually promotes, only `coalesce.go` uses it, ~64
  references. `infer.go`/`typeops.go`/`residual.go` aren't carried into M1 at
  all, per §2.1.) The greppable success signal for Delta #1:
  `grep -rn "type_system" internal/solver/ | grep -v _test` returns nothing.

  This is about **shared code**, not shared *design*. Several M1 surfaces are
  intentionally modeled on `type_system` and must stay comparable to it:
  `soltype`'s node names follow the `…Type` convention, `print.go` mirrors
  `type_system.PrintType`'s surface forms (so M8 can string-diff the two
  checkers' output), and error wording is preserved verbatim from the spike.
  None of these share an import — they're reimplementations that look alike on
  purpose. The one place real sharing is on the roadmap is **diagnostic types**
  (per [02-design-notes.md](02-design-notes.md) Settled Decision #4: production
  reuses the old checker's diagnostic types where they apply); that wiring is
  deferred until M2 brings source spans, so M1 itself ships span-free value
  errors and imports nothing from `type_system` or the old checker.

---

## 3. Design decisions to settle in M1

### 3.1 Package/file layout

```text
internal/solver/
  soltype/
    type.go        Type iface; TypeVarType, PrimType, LitType,
                   FuncType, TupleType, Void, NeverType, UnknownType,
                   UnionType, IntersectionType; levelOf, boundsAt
    polarity.go    Polarity enum + flip (lives here so BoundsAt can take it
                   without soltype importing solver)
    print.go       Type -> Escalier annotation string (M1 type set)
  context.go       Context: varCounter + freshVar (the engine's mutable state)
  constrain.go     constrain(lhs <: rhs), seen-cache, extrude
  errors.go        SolverError iface + per-kind structs (CannotConstrainError,
                   FuncArityMismatchError, TupleLengthMismatchError, …), describe
  coalesce.go      soltype.Type (with bounds) -> coalesced soltype.Type
                   (uniform inline-to-bounds; no occurrence analysis in M1)
  info.go          Info side table (map[ast.Node]soltype.Type)
  doc.go           package doc
```

The type nodes, the printer, and `Polarity` live in `soltype` (representation
+ a small enum that types want to reference); the engine, coalescing, and
`Context` live in `solver` (algorithm). The printer must **not** import
`solver` (it renders an already-coalesced type), and `soltype` more generally
must not import `solver` — that's why `Polarity` is in `soltype`, so
`TypeVarType.BoundsAt` can take it without inverting the dependency.
`solver` imports `soltype` and `internal/ast` (for `Info`'s key type); neither
`ast` nor `type_system` imports `solver`, so there's no cycle and M1 stays
additive.

### 3.2 Naming

Promote the spike's terse names to the design-notes names: `Variable` →
`TypeVarType`, `Primitive` → `PrimType`, etc.
([02-design-notes.md](02-design-notes.md) §"`soltype`"). Keep the spike's
`Inferer`-style mutable state but name it `Context` (the design notes refer to
`solver.Context`). Avoid shadowing Go builtins per CLAUDE.md.

### 3.3 The M1/M3 boundary: defer the polymorphism-rendering bundle

The spike bundles several closely related pieces into one "simplification +
rendering" story: occurrence analysis (`analyze`), bipolar-variable retention
during coalescing, named-type-param ref node (`type_system.TypeRefType`),
co-occurrence merging (`collectCoOcc`/`mergeCoOccurring`/union-find), variable
naming (`T0`, `T1`, …), and the `<…>` quantifier prefix in the printer. They
share a single design question — *what node represents a named ref in
coalesced output, and how does it relate to type aliases?* — that can't be
answered well until M3's `Scheme` machinery and (M4's) alias references give
it context. M1 ducks the question entirely:

- **M1's coalescer always inlines.** Every `TypeVarType` is replaced by its
  bounds (or `never`/`unknown` for empties). No bipolar retention, no
  occurrence analysis input, no named-ref output. `id(5)`-shaped terms still
  coalesce cleanly (`5` directly, since the result var is positive with lower
  `5`); the identity coalesces to a degenerate `(unknown) → never` — which
  is *not* tested, because identity rendering is deferred to M3.
- **No `simplify.go` in M1.** Both `analyze` and the union-find merging code
  stay in the spike until M3 promotes them together.
- **No `TypeRefType` node in M1.** Avoids committing to a single-purpose node
  that would need to be redesigned (param-ref vs alias-ref) once aliases land.
- **No `<T0, …>` quantifier prefix in the printer.** Nothing to collect.

**M3 then lands the whole bundle in one coherent slice**: `Scheme`s,
`instantiate`/`freshenAbove`, `analyze`, bipolar retention, the named-ref
node(s) (informed by M4's alias-ref needs), co-occurrence merging, variable
naming, and the `<…>` quantifier prefix in the printer. The identity test
(`fn <T0>(x: T0) -> T0`) and the `InnerCapturesOuterParam` test
(`fn <T0>(y: T0) -> [T0, T0]`) are both M3 accept criteria.

The risk this trade pays down: a `TypeRefType` introduced in M1 to serve the
bipolar-survivor role would almost certainly need refactoring in M4 when
alias references arrive, and again in M3 if scheme-quantifier semantics
diverge from "any bipolar survivor." Deferring sidesteps both refactors.

The cost: M1 no longer has the identity rendering as a single headline
end-to-end test. M1's accept becomes a constellation of `constrain` and
coalesce checks (see §6), and the "polymorphism works end-to-end" demo
slides to M3.

#### Forward requirements for the named-ref node design

Three requirements the M3/M4 named-ref design needs to honor so the M1
seen-cache (pointer-identity keying on `constraintKey{lhs, rhs soltype.Type}`
— see §9.3 and the in-code comment there) stays sound once recursive aliases
land:

- **Lazy, memoized unfolding.** The alias-ref variant of the named-ref node
  must carry a lazily-populated `Body Type` field, and `Unfold()` must cache
  the substituted body on first call so every subsequent use returns the same
  pointer. Without this, two unfoldings of the same alias produce
  structurally-equal but pointer-distinct bodies, and the seen-cache stops
  catching cycles that pass through the alias.
- **Knot-tying in `substitute`.** When the alias body contains a self-
  reference (`type T = T | null`), the substitution must reuse the *outer*
  alias-ref pointer at the recursion point rather than allocating a fresh
  ref. This is the standard OCaml-style recursive-type trick: the alias-ref
  node itself is the recursion anchor, and the seen-cache catches the cycle
  via its pointer.
- **Canonicalize alias-ref instances by `(Decl, Args)`.** Every mention of an
  alias anywhere in the program — in user annotations, in other alias
  bodies, in inferred coalesced output — must resolve through a single
  `Context.AliasRef(decl, args)` interner to the same pointer. **This is
  required, not optional, for mutually-recursive types**: in `type A = B |
  null; type B = A | number`, the cycle `constrain(ref_A, X)` → unfold →
  hits `ref_B` → unfold → hits `ref_A_again` only closes pointer-wise if
  `ref_A_again` is the *same* pointer as the original `ref_A`. Knot-tying
  inside a single decl doesn't reach across decls; canonicalization does.
  Generalizes to N-way mutual recursion: every cycle passes through at
  least one canonical alias-ref pointer per decl, and the seen-cache
  catches it on revisit.

With all three in place, the cycle-forming nodes in M4+ are `TypeVarType`
(pointer-stable per M1) and `AliasRefType` (pointer-stable per the above),
and pointer-identity caching in `constraintKey` works unchanged. Without
them, M4 has to either restructure the seen-cache (e.g., switch to a
structural-content key) or chase down subtle non-termination bugs. Cheap to
plant the requirement now; expensive to remember at M4.

> Out of scope for this mechanism: **non-regular recursive aliases** like
> `type T<X> = T<List<X>>`, where each unfold step grows the args. There is
> no honest pointer cycle to detect (the unfolded sequence really is
> infinite), so pointer-identity caching can't help — and neither can
> structural-content caching, for the same reason. M4 should pick one of the
> standard mitigations (reject at definition time, bound the instantiation
> depth, or require a termination-asserting annotation); that choice is
> orthogonal to the seen-cache design.

---

## 4. PR breakdown

Five PRs. PR 1 is the foundation; PRs 2→3→4 are a linear chain (engine →
coalesce → print) because each is tested against the previous; PR 5 (`Info`) is
independent of the engine and can land in parallel any time after PR 1. Each PR
leaves `go build ./...` and `go test ./...` green. **§9 sketches the concrete
types and functions each PR introduces** — the PR descriptions below reference
those sketches by name.

```text
PR1 (skeleton + soltype types)
 ├─► PR2 (constrain + extrude) ─► PR3 (coalesce) ─► PR4 (printer)
 └─► PR5 (Info side table)        [parallel with PR2–PR4]
```

### PR 1 — Package skeleton + `soltype` core types

**Creates:** `soltype/polarity.go`, `soltype/type.go`, `solver/context.go`,
`solver/doc.go`. *(sketches: §9.1, §9.2)*

- `soltype/type.go`: the `Type` interface (`isType()` marker), the M1 type set
  (`TypeVarType`, `PrimType`, `LitType`, `FuncType`, `TupleType`,
  `Void`, `NeverType`, `UnknownType`, `UnionType`, `IntersectionType`),
  `boundsAt`, literal equality, and `levelOf` trimmed to M1 cases.
- `soltype/polarity.go`: copy of the spike's `Polarity` + `Flip` (lives in
  `soltype` so `TypeVarType.BoundsAt` can take a `Polarity` without inverting
  the package boundary).
- `solver/context.go`: `Context` owning `varCounter`; `freshVar(level)`.
- `solver/doc.go`: package doc describing the production package (adapt the
  spike's `doc.go`, scoped to what's actually present).

**Tests:** `levelOf` over nested function/tuple terms; `freshVar` id/level
sequencing; literal `eq`. Table-driven, `require.*`.

**Accept:** package builds; `go vet ./internal/solver/...` clean.

### PR 2 — `constrain` + extrusion (the engine)

**Creates:** `solver/constrain.go`, `solver/errors.go`. *(sketches: §9.3)*

- `constrain(lhs, rhs, seen)` with the coinductive `seen`-cache.
- Structural cases: `PrimType` (name equality), `LitType <:
  LitType` and `LitType <: PrimType` (literal is a subtype of its
  primitive), `FuncType` (**same-arity required** + contravariant
  params / covariant return — implicitly the *exact* function case; the
  inexact fewer-params-is-subtype arm lands in M3 with the exact flag),
  `TupleType` (same length, covariant elements — implicitly the *exact* tuple
  case; the inexact arm lands in M4 with the exact flag),
  `Void`.
- Variable cases: append to `upper`/`lowerBounds` and transitively propagate
  existing bounds; **levels + `extrude`** for cross-level constraints.
- `describe(...)` for error messages.

**Tests** (this is the bulk of the M1 "unit tests for `constrain`" accept
criterion — table-driven, **full** error-message assertions per CLAUDE.md;
where structural inspection adds value, also type-switch on the concrete
`SolverError` to assert the carried `LHS`/`RHS` references):
- prim `<:` prim success and the exact mismatch error (assert message text
  *and* that the failure is a `*CannotConstrainError` with the right
  `*PrimType`s);
- `LitType <: PrimType` (e.g. `5 <: number`), and the mismatch error;
- **function variance** with exact arity both directions (arity
  `1 <: 2` and `2 <: 1` both rejected — assert `*FuncArityMismatchError`
  carrying the offending `*FuncType`s — plus same-arity success);
- tuple same-length covariant; length-mismatch error (`*TupleLengthMismatchError`);
- variable binding + transitive propagation (constrain `α <: number` then `5 <:
  α` propagates `5 <: number`);
- extrusion: a higher-level type constrained against a lower-level variable is
  copied down (assert no higher-level var leaks into the lower var's bounds).

### PR 3 — Bound-inlining coalescing

**Creates:** `solver/coalesce.go`. *(sketch: §9.4)*

- `coalesce(st, pol)` → **`soltype.Type`** (Delta #1): every `TypeVarType` is
  inlined to its bounds — `combine` builds a `soltype.UnionType` in positive
  position from the variable's lowers / a `soltype.IntersectionType` in
  negative position from its uppers, with `combine` returning the sole element
  directly when only one remains; empty bounds collapse to `never` (positive)
  or `unknown` (negative). Structural cases (function/tuple) recurse with the
  appropriate polarity flips. Deduplicate by structural equality.
- No occurrence analysis, no `bipolar` flag, no `nameFor`, no `inProc`
  recursion guard — those land in M3 with the polymorphism-rendering bundle
  (§3.3). No `seen` cache needed in M1 either, since the M1 type set has no
  recursive formers.

**Tests:** single-bound inlining (positive var with lower bound `5` ⇒ `5`;
negative var with upper bound `number` ⇒ `number`); empty-bound collapse
(empty positive ⇒ `never`, empty negative ⇒ `unknown`); multi-bound coalescing
(positive var with two distinct lower bounds ⇒ `soltype.UnionType`; negative
var with two distinct upper bounds ⇒ `soltype.IntersectionType`); structural
recursion (`fn (x) -> x` with the body var positive ⇒ `fn (x: unknown) ->
never` — the degenerate inline result, which is the *expected* M1 output for
the identity; the named-`<T0>` rendering is M3).

### PR 4 — `soltype` printer

**Creates:** `soltype/print.go`. *(sketches: §9.5)*

- `Print(t soltype.Type) string` rendering Escalier annotation syntax for the
  M1 coalesced type set (Delta #2). No `<…>` quantifier prefix in M1.
- Mirror `type_system.PrintType`'s surface forms; share no code.

**Tests** (completes the M1 acceptance):
- round-trips for primitives, literals, tuples (`[number, string]`),
  multi-arg functions, the lattice bounds (`never`, `unknown`), and
  multi-element unions/intersections (`number | string`, `number & string`).
- end-to-end via PR 3: a hand-built var with two distinct lower bounds, fed
  through `coalesce + Print`, renders as the expected `number | string`.

> Inline-snapshot option: per CLAUDE.md, the printed-string assertions are exactly
> the case where `snaps.MatchInlineSnapshot` is appropriate; use it for the
> richer shapes and reserve `require.Equal` for the short, stable round-trips.

### PR 5 — `Info` side table *(parallel; depends only on PR 1)*

**Creates:** `solver/info.go`. *(sketches: §9.6)*

- `Info{ types map[ast.Node]soltype.Type }`, `TypeOf(n) soltype.Type`,
  `setType(n, t)`. No probe/cleanup discipline yet (deferred with `Prov`/`Probe`).

**Tests:** construct a couple of real `ast.Node` pointers, assert `setType`/
`TypeOf` round-trip and that an absent node returns the zero value. Confirms the
`solver`→`ast` import direction is acyclic.

> May be folded into PR 1 if reviewers prefer a single foundational PR — it's
> independent of the engine and trivially small. Kept separate here for focus.

---

## 5. Sequencing summary

1. **PR 1** — skeleton + types. Unblocks everything.
2. **PR 2** — `constrain` + `extrude`. (Bulk of the constrain unit-test accept.)
3. **PR 3** — `coalesce` (targets `soltype` — Delta #1; bound-inlining only).
   (Coalescing accept.)
4. **PR 4** — `soltype` printer (Delta #2). (Printer round-trip accept.)
5. **PR 5** — `Info`. Parallel with 2–4.

Critical path is 1 → 2 → 3 → 4. PR 5 is off the critical path.

---

## 6. Acceptance criteria (milestone → PR mapping)

The milestone doc's accept clause is: *"unit tests for `constrain`
(prim/function variance with exact arity) and coalescing; an identity term
renders `fn <T0>(x: T0) -> T0`."* **This plan diverges** on the identity
clause: the identity rendering is deferred to M3 along with the rest of the
polymorphism-rendering bundle (§3.3). A
[milestones-doc update](01-milestones.md) should restate M1's accept as the
constellation below and move the identity rendering into M3's accept.
(Functions are **exact** in M1 — same-arity required, exact-by-default — so
the inexact fewer-params-is-subtype rule is an M3 accept clause, not M1's.)

| Accept clause | Delivered by |
|---|---|
| `constrain` prim variance | PR 2 |
| `constrain` function variance + exact arity (same-arity required) | PR 2 |
| `constrain` levels + extrusion (no cross-level leakage into bound lists) | PR 2 |
| coalescing unit tests (inline-to-bounds, empty-bound collapse, multi-bound union/intersection) | PR 3 |
| printer round-trips for the M1 coalesced type set (prims, literals, tuples, multi-arg functions, `never`/`unknown`, `\|`/`&`) | PR 4 |
| ~~identity renders `fn <T0>(x: T0) -> T0`~~ — **deferred to M3** | M3 |

**Definition of done for M1:** all five PRs merged; `go test ./internal/solver/...`
green; zero edits to existing packages; the old checker, `go build ./...`, and
the full `go test ./...` suite unaffected. (There are **no fixture tests** in M1
— `cmd/...` is untouched until the M2 parser bridge — so M1 is validated purely
by `internal/solver/...` unit tests.) The greppable signal for Delta #1:
`grep -rn "type_system" internal/solver/ | grep -v _test` returns **nothing** —
the new package has fully severed the spike's `type_system` coupling.

---

## 7. Risks & gates

- **M1 has no go/no-go gate of its own.** The plan's gates are at M2 (the
  parser-bridge boundary check) and M4 (the `mut`-invariance gate). M1 is the
  lowest-risk milestone: it promotes code already proven by the spike's
  differential harness (10 match / 2 benign / 0 regression).
- **Only real risk: the two deltas.** Cutting the `type_system` dependency
  (Delta #1) and writing a native printer (Delta #2) are the sole pieces of
  genuinely new code. Mitigation: keep the coalescer's *logic* a line-for-line
  port of the spike's `coalesce.go`, changing only the constructor calls
  (`type_system.New… → soltype.…`) and moving variable-naming into the
  coalescer/printer; diff against the spike during review to confirm the
  algorithm is unchanged.
- **Scope creep from the spike.** The spike file set is tempting to copy
  wholesale. Resist: M1 is the structural-core subset only. Records, `mut`,
  lifetimes, union/intersection *subtyping rules* in `constrain`, and type
  operators each land in their own milestone with their own tests and (for
  `mut`) their own gate. (The `UnionType`/`IntersectionType` *nodes* themselves
  ship in M1 — see §1 scope — but only as coalesced output.) Copying them early lands
  untested-against-real-source code ahead of the bridge.
- **The M1/M3 boundary** (§3.3) is a judgement call recorded here so a reviewer
  doesn't flag "where's the identity rendering / `<T0>` / occurrence analysis /
  `TypeRefType`?" — the whole polymorphism-rendering bundle is deliberately M3,
  and M1's revised accept is met without any of it.
- **Milestones-doc divergence.** §6 notes that this plan defers the identity-
  rendering clause from the milestone's accept. Land the milestones-doc update
  *before* M1 PRs merge so the milestone description stays in sync.

---

## 8. Conventions

Follow [CLAUDE.md](../../CLAUDE.md): table-driven tests; `require.*` over
`assert.*`; assert **full** error messages, not substrings; use `internal/set`'s
`Set` ADT rather than `map[T]struct{}`; use the inline-snapshot pattern
(`snaps.MatchInlineSnapshot`) for the printer's richer rendered-type assertions.
Don't shadow Go builtins. There are no snapshot/fixture env-var flows in M1
(no `cmd/...` involvement); tests run with plain `go test ./internal/solver/...`.

---

## 9. Type & function sketches

Illustrative shapes for the M1 surface, derived from the spike with the §2
deltas applied. **Names and signatures are provisional** — the point is to pin
down the data model and the boundaries between functions, not to prescribe final
code. Repetitive switch arms are elided with `// …`. Sketches are grouped by the
file they land in (§3.1 layout) and tagged with the PR that introduces them.

### 9.1 `soltype/type.go` — the type representation *(PR 1)*

```go
package soltype

// Type is the sealed interface for all soltype nodes. (Production name for the
// spike's SimpleType; marker renamed isSimpleType -> isType.)
type Type interface{ isType() }

// TypeVarType is an inference variable carrying Simple-sub lower/upper bound
// lists plus the level at which it was created (for let-generalization in M3).
type TypeVarType struct {
	ID          int
	Level       int
	LowerBounds []Type
	UpperBounds []Type
}

// BoundsAt returns the bounds relevant to a polarity: lowers in Positive
// position (the var becomes their union), uppers in Negative (their meet).
func (v *TypeVarType) BoundsAt(pol Polarity) []Type {
	if pol == Positive {
		return v.LowerBounds
	}
	return v.UpperBounds
}

// Prim is the closed set of primitives M1 carries. Mirrors the type_system
// package's Prim enum, but only the three M1's tests exercise; M2+ extends
// Prim (BigIntPrim, SymbolPrim) and Lit (BigIntLit, NullLit, UndefinedLit)
// to the full type_system set as the parser bridge surfaces them. The
// additions are inert from constrain's perspective — same prim/literal arms
// with one more concrete each — so the deferral is purely scope, not design.
type Prim int

const (
	NumPrim Prim = iota
	StrPrim
	BoolPrim
)

type PrimType struct{ Prim Prim }

// Lit is the sealed interface for literal values inside a LitType.
// Mirrors type_system.Lit (with NumLit/StrLit/BoolLit concretes) so each
// literal kind carries exactly the value field it needs — no flat struct
// where two of three value fields are dead per instance.
type Lit interface{ isLit() }

type NumLit struct{ Value float64 }
type StrLit struct{ Value string }
type BoolLit struct{ Value bool }

func (*NumLit) isLit()  {}
func (*StrLit) isLit()  {}
func (*BoolLit) isLit() {}

type LitType struct{ Lit Lit }

// Equal is structural equality on the contained literal.
func (l *LitType) Equal(o *LitType) bool { /* dispatch on Lit concrete, compare Value */ }

// Pat is the sealed interface for parameter patterns. Mirrors the role of
// type_system.Pat (and ast.Pat) but lives in soltype to keep soltype ast-free.
// M1 ships a single concrete (IdentPat); M4 adds destructuring concretes
// (TuplePat, RecordPat, …) once record/tuple types exist — M2 stays
// IdentPat-only. The sealed interface means they land as new Pat concretes
// with no FuncParam restructuring.
type Pat interface{ isPat() }

type IdentPat struct{ Name string }

func (*IdentPat) isPat() {}

// FuncParam mirrors type_system.FuncParam. M1 omits Optional (no optional
// params until M3+); Pattern is reachable only through Pat concretes M1
// defines (IdentPat). Destructured params (TuplePat/RecordPat) arrive in M4.
type FuncParam struct {
	Pattern Pat
	Type    Type
}

type FuncType struct {
	Params []*FuncParam
	Ret    Type
}

type TupleType struct{ Elems []Type }

// Void is the result type of a statement block with no value.
type Void struct{}

// NeverType (⊥) and UnknownType (⊤) are the bottom/top of the subtype lattice —
// the coalesced output of an empty-bounds single-polarity variable (positive ⇒
// never, negative ⇒ unknown). The spike emits these via type_system; M1 carries
// them natively because they're fundamental to the lattice, not optional sugar.
type NeverType struct{}
type UnknownType struct{}

// UnionType / IntersectionType are coalesced-output nodes for multi-bound
// single-polarity variables (positive ⇒ union of lowers, negative ⇒ intersection
// of uppers). The spike emits these via type_system.NewUnionType /
// NewIntersectionType; M1 carries them natively so coalescing returns
// soltype.Type in every case. Their *subtyping rules* in constrain are M6 —
// these nodes appear only as coalesced output in M1, never as constrain inputs.
type UnionType struct{ Types []Type }
type IntersectionType struct{ Types []Type }

func (*TypeVarType) isType()       {}
func (*PrimType) isType()     {}
func (*LitType) isType()       {}
func (*FuncType) isType()      {}
func (*TupleType) isType()         {}
func (*Void) isType()              {}
func (*NeverType) isType()         {}
func (*UnknownType) isType()       {}
func (*UnionType) isType()         {}
func (*IntersectionType) isType()  {}

// LevelOf is the max level of any TypeVarType inside t; concrete leaves are 0.
// Trimmed to the M1 type set (grows back as later milestones add formers).
func LevelOf(t Type) int {
	switch t := t.(type) {
	case *TypeVarType:
		return t.Level
	case *FuncType:
		m := 0
		for _, p := range t.Params {
			m = max(m, LevelOf(p.Type))
		}
		return max(m, LevelOf(t.Ret))
	case *TupleType:
		m := 0
		for _, e := range t.Elems {
			m = max(m, LevelOf(e))
		}
		return m
	default: // PrimType, LitType, Void, NeverType, UnknownType, UnionType, IntersectionType
		// UnionType/IntersectionType only appear in coalesced output, where every
		// TypeVarType has been inlined — so they contain no level-bearing nodes
		// reachable to LevelOf in M1. M6 (when these become constrain inputs via
		// user annotations) adds the recursive arms alongside the distributivity
		// rules in constrain.
		return 0
	}
}
```

### 9.2 `soltype/polarity.go` and `solver/context.go` *(PR 1)*

```go
package soltype

// Polarity lives in soltype so TypeVarType.BoundsAt can take it without
// soltype importing solver (the algorithm package is allowed to depend on
// the representation, not the other way around — §3.1).
type Polarity int

const (
	Positive Polarity = iota
	Negative
)

func (p Polarity) Flip() Polarity { /* Positive<->Negative */ }
```

```go
package solver

// Context owns the engine's mutable counters. M1 carries ONLY varCounter; the
// spike's lifetimeCounter / paramLifetimes / written fields are M4 (§2.3 row 4).
type Context struct {
	varCounter int
}

func (c *Context) freshVar(level int) *soltype.TypeVarType {
	v := &soltype.TypeVarType{ID: c.varCounter, Level: level}
	c.varCounter++
	return v
}
```

### 9.3 `solver/constrain.go` — the engine *(PR 2)*

```go
package solver

// constraintKey keys the coinductive seen-set by pointer identity (Go's
// interface == on pointer-backed soltype concretes). Sufficient for M1:
// cycles in subtype-checking can only form via TypeVarTypes, and TypeVarType
// pointers are stable throughout inference (extrude allocates fresh vars,
// but those are stable thereafter; structural decomposition in constrain
// hands child pointers around without copying). Structurally-equal-but-
// pointer-distinct duplicates produce redundant cache entries, not infinite
// loops. M4's recursive types (aliases, letrec) must preserve this property:
// see §3.3 "Forward requirements for the named-ref node design" — alias-ref
// nodes need lazy-memoized Unfold() and knot-tied substitute() so the only
// cycle-forming pointers stay stable across an inference run.
type constraintKey struct{ lhs, rhs soltype.Type }

// Constrain asserts lhs <: rhs, mutating bound lists. Empty result == success.
func (c *Context) Constrain(lhs, rhs soltype.Type) []SolverError {
	return c.constrain(lhs, rhs, set.NewSet[constraintKey]()) // set.Set, not map (§2.4)
}

func (c *Context) constrain(lhs, rhs soltype.Type, seen set.Set[constraintKey]) []SolverError {
	key := constraintKey{lhs, rhs}
	if seen.Contains(key) {
		return nil
	}
	seen.Add(key)

	switch l := lhs.(type) {
	case *soltype.PrimType:
		if r, ok := rhs.(*soltype.PrimType); ok {
			if r.Prim == l.Prim {
				return nil
			}
			return []SolverError{&CannotConstrainError{LHS: l, RHS: r}}
		}
	case *soltype.LitType:
		// Two arms:
		//   LitType <: LitType   -> nil if l.Equal(r), else CannotConstrainError.
		//   LitType <: PrimType  -> nil if primOf(l.Lit) == r.Prim
		//                           (a literal is a subtype of its primitive),
		//                           else CannotConstrainError.
		// A mismatch returns the error; any other rhs falls through to the var case. // …
		// primOf maps a Lit concrete to its Prim: *NumLit -> NumPrim, etc.
	case *soltype.FuncType:
		if r, ok := rhs.(*soltype.FuncType); ok {
			// Exact arity (exact-by-default): l <: r requires the SAME arity,
			// len(l.Params) == len(r.Params). The inexact fewer-params arm is M3.
			if len(l.Params) != len(r.Params) {
				return []SolverError{&FuncArityMismatchError{LHS: l, RHS: r}}
			}
			var errs []SolverError
			for i := range l.Params {
				errs = append(errs, c.constrain(r.Params[i].Type, l.Params[i].Type, seen)...) // contravariant
			}
			return append(errs, c.constrain(l.Ret, r.Ret, seen)...) // covariant
		}
	case *soltype.TupleType:
		// same length, element-wise covariant. // …
		// On length mismatch: TupleLengthMismatchError{LHS, RHS *soltype.TupleType}.
		// M1's TupleType has no exact flag — same-length is the *exact <: exact*
		// case applied implicitly. M4 introduces the exact flag and adds the
		// inexact <: inexact arm (longer-is-subtype-of-shorter, element-wise
		// covariant on the overlap), plus exact <: inexact; inexact <: exact is
		// rejected. See 01-milestones.md §M4 ("Exactness flag on records and
		// tuples").
	case *soltype.Void:
		if _, ok := rhs.(*soltype.Void); ok {
			return nil
		}
	}

	// lhs is a variable: record rhs as an upper bound, propagate existing lowers.
	if lv, ok := lhs.(*soltype.TypeVarType); ok {
		if soltype.LevelOf(rhs) <= lv.Level {
			lv.UpperBounds = append(lv.UpperBounds, rhs)
			var errs []SolverError
			for _, lb := range lv.LowerBounds {
				errs = append(errs, c.constrain(lb, rhs, seen)...)
			}
			return errs
		}
		return c.constrain(lhs, c.extrude(rhs, soltype.Negative, lv.Level, map[int]*soltype.TypeVarType{}), seen)
	}
	// rhs is a variable: symmetric (record lower bound, propagate uppers). // …

	return []SolverError{&CannotConstrainError{LHS: lhs, RHS: rhs}}
}

// extrude copies t so variables above lvl become fresh vars at lvl, wired to the
// originals through the polarity-appropriate bound. (Same algorithm as the
// spike; cache is keyed by var ID.)
func (c *Context) extrude(t soltype.Type, pol soltype.Polarity, lvl int, cache map[int]*soltype.TypeVarType) soltype.Type {
	// … TypeVarType / FuncType (params flip) / TupleType cases …
}
```

Errors are a sealed `SolverError` interface with one concrete struct per
kind, modeled on [internal/checker/error.go](../../internal/checker/error.go).
Each struct carries typed references to the offending `soltype.Type` values
so LSP/tooling consumers can inspect them (e.g., navigate to a type's
declaration) without reparsing the rendered message. Wording matches the
spike's `fmt.Errorf` strings verbatim so test assertions stay stable.

```go
// errors.go (PR 2). Sealed interface + per-kind concrete structs. M2 adds a
// Span() ast.Span method and likely rebases these onto checker's diagnostic
// types per 02-design-notes.md Settled Decision #4.
type SolverError interface {
	isSolverError()
	Message() string
}

// CannotConstrainError fires when a non-variable LHS/RHS pair fails to
// match (prim/prim mismatch, lit/lit mismatch, lit/prim mismatch, and the
// generic "no rule applies" fall-through at the end of constrain).
type CannotConstrainError struct {
	LHS, RHS soltype.Type
}

// FuncArityMismatchError fires on FuncType <: FuncType when the arities
// differ (exact-function rule; exact-by-default requires same arity). Holds
// the full FuncTypes, not just the arities, so consumers can report
// param/return types too.
type FuncArityMismatchError struct {
	LHS, RHS *soltype.FuncType
}

// TupleLengthMismatchError fires on TupleType <: TupleType with different
// lengths (M1's exact-tuple case; M4 may narrow the firing conditions when
// the inexact flag is added).
type TupleLengthMismatchError struct {
	LHS, RHS *soltype.TupleType
}

func (*CannotConstrainError) isSolverError()       {}
func (*FuncArityMismatchError) isSolverError()     {}
func (*TupleLengthMismatchError) isSolverError()   {}

func (e *CannotConstrainError) Message() string {
	return fmt.Sprintf("cannot constrain %s <: %s", describe(e.LHS), describe(e.RHS))
}
func (e *FuncArityMismatchError) Message() string {
	return fmt.Sprintf("cannot constrain function of arity %d <: function of arity %d",
		len(e.LHS.Params), len(e.RHS.Params))
}
func (e *TupleLengthMismatchError) Message() string {
	return fmt.Sprintf("cannot constrain tuple of length %d <: tuple of length %d",
		len(e.LHS.Elems), len(e.RHS.Elems))
}

// describe renders a RAW, uncoalesced type for in-flight error messages
// (t0, function, number). Distinct from soltype.Print, which renders coalesced
// output (§2.2). Lives in solver because it walks bound-carrying vars.
func describe(t soltype.Type) string { /* … */ }
```

> Test helpers: a `Messages(errs []SolverError) []string` adapter keeps
> table-driven tests reading naturally — `require.Equal(t, []string{"cannot
> constrain number <: string"}, Messages(errs))`. Tests that want to
> inspect the *structure* of an error (e.g., assert the LHS is a particular
> `*PrimType`) do so via a type switch on the concrete error type — exactly
> the affordance that motivates the per-kind structs in the first place.

### 9.4 `solver/coalesce.go` — `soltype.Type` → coalesced `soltype.Type` *(PR 3)*

Delta #1: returns `soltype.Type`, not `type_system.Type`. M1's coalescer is
**uniformly inlining** — no occurrence analysis, no `bipolar` branching, no
`nameFor`/`inProc`. The whole bundle moves to M3 (§3.3).

```go
package solver

// coalesce is a package-private free function in M1 — it needs no Context
// (no shared counters/occurrence state until M3 reintroduces them). Callers
// are all inside package solver (tests, future infer.go), so no exported
// entry point is required yet; M3 can attach it to Context if the
// polymorphism-rendering bundle needs shared state.
func coalesce(t soltype.Type, pol soltype.Polarity) soltype.Type {
	switch t := t.(type) {
	case *soltype.PrimType, *soltype.LitType, *soltype.Void,
		*soltype.NeverType, *soltype.UnknownType:
		return t // atoms pass through
	case *soltype.FuncType:
		params := make([]*soltype.FuncParam, len(t.Params))
		for i, p := range t.Params {
			params[i] = &soltype.FuncParam{Pattern: p.Pattern, Type: coalesce(p.Type, pol.Flip())} // contravariant
		}
		return &soltype.FuncType{Params: params, Ret: coalesce(t.Ret, pol)}
	case *soltype.TupleType:
		// element-wise coalesce in pol // …
	case *soltype.TypeVarType:
		// Uniform inline: drop the variable, keep only its (recursively coalesced)
		// bounds in the current polarity.
		bounds := make([]soltype.Type, 0, len(t.BoundsAt(pol)))
		for _, b := range t.BoundsAt(pol) {
			bounds = append(bounds, coalesce(b, pol))
		}
		if len(bounds) == 0 {
			if pol == soltype.Positive {
				return &soltype.NeverType{} // ⊥ — empty positive (§9.1)
			}
			return &soltype.UnknownType{} // ⊤ — empty negative (§9.1)
		}
		return combine(pol, dedup(bounds))
	}
	panic("coalesce: unhandled type")
}

// combine builds a soltype.UnionType (Positive) or soltype.IntersectionType
// (Negative) of parts, returning the sole element directly when only one
// remains. UnionType/IntersectionType nodes ship in M1 (§9.1) so combine can
// always return a native soltype.Type.
func combine(pol soltype.Polarity, parts []soltype.Type) soltype.Type { /* … */ }
```

> M1 note: `NeverType`/`UnknownType` are the lattice ⊥/⊤ and the reachable
> output of an empty-bounds variable in this uniform-inline coalescer.
> `UnionType`/`IntersectionType` are emitted by `combine` for variables with
> two or more distinct bounds. None of these nodes ever appear as `constrain`
> inputs in M1 — their subtyping rules in `constrain` are M6.
>
> M1 doesn't bother with an `inProc` / recursion guard: the M1 type set has no
> recursive formers (no aliases, no recursive types), so a uniform-inline walk
> terminates on the bound-graph as-is. M3 adds the guard when bipolar
> retention and aliases create the possibility of recursive structures in
> coalesced output.

### 9.5 `soltype/print.go` — the native printer *(PR 4)*

Delta #2. Renders a *coalesced* type. No `<…>` quantifier prefix in M1 (no
named refs to collect — that machinery lands in M3 with the rest of the
polymorphism-rendering bundle, §3.3).

```go
package soltype

// Print renders a coalesced Type as an Escalier type-annotation string.
func Print(t Type) string {
	return printType(t)
}

func printType(t Type) string {
	switch t := t.(type) {
	case *PrimType:
		// Prim is an int enum (§9.1), so map it to its surface name —
		// mirrors type_system/print_type.go's prim switch.
		switch t.Prim {
		case NumPrim:
			return "number"
		case StrPrim:
			return "string"
		case BoolPrim:
			return "boolean"
		}
		panic("printType: unhandled Prim")
	case *LitType:
		// "hello" | 5 | true (Escalier literal syntax) // …
	case *NeverType:
		return "never"
	case *UnknownType:
		return "unknown"
	case *UnionType:
		// comma-join via " | " — printType each member // …
	case *IntersectionType:
		// comma-join via " & " — printType each member // …
	case *FuncType:
		return "fn " + printFuncTail(t)
	case *TupleType:
		// "[" + comma-join(printType(e)) + "]" // …
	case *Void:
		return "void"
	}
	panic("Print: unhandled type")
}

// printFuncTail renders the "(params) -> ret" portion of a function, without
// the "fn" keyword. Kept as a separate helper so M3 can compose it with a
// quantifier prefix without byte-slicing.
func printFuncTail(t *FuncType) string {
	ps := make([]string, len(t.Params))
	for i, p := range t.Params {
		ps[i] = paramName(p, i) + ": " + printType(p.Type)
	}
	return "(" + strings.Join(ps, ", ") + ") -> " + printType(t.Ret)
}

// paramName renders p.Pattern; for M1 the only Pat concrete is IdentPat,
// so this dispatches on that and falls back to "x"+strconv.Itoa(i) for a
// nil/unknown pattern. M4's destructuring Pat concretes add their own arms.
func paramName(p *FuncParam, i int) string {
	switch pat := p.Pattern.(type) {
	case *IdentPat:
		return pat.Name
	default:
		return "x" + strconv.Itoa(i)
	}
}
```

An end-to-end test demonstrates the engine→coalesce→print pipeline using the
multi-bound case (the canonical M1 demo of "the inline coalescer is wired up
right"). **This test lives in `package solver`** (e.g.
`solver/coalesce_test.go`), *not* in `soltype` — it drives the engine's
unexported `Context`/`freshVar` and reaches `soltype.Print` across the
boundary (`soltype` must not import `solver`, per §3.1):

```go
package solver // solver/coalesce_test.go

func TestMultiBoundRenders(t *testing.T) {
	ctx := &Context{}
	a := ctx.freshVar(1)
	a.LowerBounds = []soltype.Type{
		&soltype.PrimType{Prim: soltype.NumPrim},
		&soltype.PrimType{Prim: soltype.StrPrim},
	}
	got := soltype.Print(coalesce(a, soltype.Positive))
	require.Equal(t, "number | string", got)
}
```

### 9.6 `solver/info.go` — the `Info` side table *(PR 5)*

```go
package solver

// Info is the AST->type side table (à la go/types.Info). The new checker never
// touches ast node InferredType()/SetInferredType(). No probe/cleanup discipline
// in M1 (that arrives with Prov/Probe later).
type Info struct {
	types map[ast.Node]soltype.Type
}

func NewInfo() *Info { return &Info{types: map[ast.Node]soltype.Type{}} }

func (i *Info) TypeOf(n ast.Node) soltype.Type { return i.types[n] }
func (i *Info) setType(n ast.Node, t soltype.Type) { i.types[n] = t }
```
