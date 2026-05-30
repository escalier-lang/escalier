# 01 — Milestones

Ordered milestones for the new checker. Each is independently testable and
leaves the old checker fully working. "Structural core first"; lifetimes are
introduced **with the first lifetime-carrying type** (records, M4). The MVP is
M1–M7 (structural core + unions/intersections + conformance corpus + type-level
operators); codegen/LSP and the cutover come after.

Spike provenance is cited where a milestone promotes proven spike work
(`internal/checker/simplesub/`).

---

## M1 — Package skeleton + `soltype`

Stand up the new package and its type representation.

- New package as a **subpackage under `internal/checker/`** (the repo rule keeps
  the checker pipeline under `internal/checker/`; cf. the spike at
  `internal/checker/simplesub/`). Leaf name TBD, e.g. `internal/checker/solver/`.
- `soltype` types promoted from the spike: bound-list `TypeVar`
  (`lowerBounds`/`upperBounds` + `level`), `Primitive`, `Literal`, `Function`,
  `Tuple`, plus the constraint primitive `constrain(lhs <: rhs)` with the
  coinductive seen-cache, levels/extrusion, and polarity-driven coalescing.
- A printer for `soltype.Type` (its own, not `type_system.PrintType`) producing
  Escalier type-annotation syntax for test assertions.
- `Info` side table: `map[ast.Node]soltype.Type` + `TypeOf`/`setType`.

**Accept:** unit tests for `constrain` (prim/function variance incl. Escalier's
fewer-params-is-subtype rule) and coalescing; an identity term renders
`fn <T0>(x: T0) -> T0`. (Spike M0/M1.)

---

## M2 — Parser/resolver bridge

Replace the spike's hand-built IR with a real constraint-generating walk over
`*ast.Module`. This is the deferred spike "parser bridge."

- Drive from real source: `parser.Parse*` → `*ast.Module` → `dep_graph` /
  `resolver` → a constraint-generating AST visitor that produces `soltype` and
  populates `Info`.
- Produce a `Scope`/`Binding`/`Namespace` analogue owned by the new package (its
  own, not `type_system`'s).
- A fixture-style harness: given `.esc` source, infer and assert the rendered
  binding types (its own assertions, independent of the old checker).

**Accept:** top-level `val`/`fn` declarations from real source infer correct
rendered types end-to-end; multi-file module via the dep graph resolves.

**Gate:** if driving from the real AST/dep-graph requires reaching back into the
old checker's internals, the parallel-package boundary is wrong — stop and
reassess.

---

## M3 — Functions, application, let-polymorphism

- Lambda/`fn` decls, application, multi-arg functions.
- Level-based let-generalization (instantiate / freshenAbove).
- The simplification pass: single-polarity elimination + co-occurrence variable
  merging (so generalized signatures render compactly, and parameter-only
  variables coalesce to `unknown` rather than a vacuous `<T0>` — a blessed
  improvement).

**Accept:** the spike's Category-A cases against real source:
`TopLevelLetPolymorphism` ⇒ `fn <T0>(x: T0) -> T0`; `IdentityPolymorphism` ⇒
`fn () -> ["hello", 5]`; `InnerCapturesOuterParam` ⇒ `fn <T0>(y: T0) -> [T0, T0]`.
(Spike M1.)

---

## M4 — Core value types: records + usage-based inference + `mut` + **lifetimes**

The big one. These are inseparable: lifetimes ride on values, records are the
first lifetime-carrying type, and `mut` borrows are what first populate a
lifetime. Land them together.

- **Records/objects** with the optional lifetime field in the representation
  *from the start* (also tuples and type-refs/aliases as lifetime-carriers).
- **Usage-based inference**: member access `obj.bar` ⇒ `constrain(obj <: {bar:
  β})`; field requirements accumulate as upper bounds and coalesce (negative
  position) to a record. This is what replaces `Open`/`Widenable`/
  `ArrayConstraint`. (Spike M2.)
- **`mut` invariance** via the read/write decomposition (covariant read +
  contravariant write). Plus inferring mutability from field writes (`obj.x = v`
  ⇒ `mut {x: widen(v)}`, with literal widening and mut-record merging). (Spike
  M3 + extension.)
- **Lifetimes as a second sort**: `LifetimeVar` with lower/upper bounds over the
  outlives lattice (`'static` = top), `constrainLt`, lifetime coalescing +
  elision (a parameter-only lifetime that connects nothing is dropped). Borrows
  originate at `mut` params; returning shares by value identity; multi-source
  returns union lifetimes; escape constrains `<: 'static`. (Spike M4.)

**Accept:** the canonical lifetime cases against real source — `IdentityRefReturn`
⇒ `fn <'a>(p: mut 'a {x: number}) -> mut 'a {x: number}`; `FreshObjectReturn`
(no lifetime); `ConditionalUnionReturn` ⇒ `mut ('a | 'b) {x: number}`;
`EscapingRefIntoStatic` ⇒ `mut 'static`; property-level and tuple-per-slot
lifetimes; read-after-write field collapse. (Spike M2/M3/M4 + lifetime extensions.)

**Gate (HIGHEST RISK):** `mut` invariance via read/write decomposition is the one
thing that could still surprise at production scale. If it cannot be encoded
cleanly against the real AST, the whole migration is in question — this is the
gate to clear before investing further.

---

## M5 — Unions / intersections

- Union/intersection as both inferred **output** (from bounds, polarity
  coalescing) and written **annotation input**, with the directional lattice
  rules in `constrain` (the "for all" rules before the "exists" rules; the
  "exists" rules defer to the variable case against a variable to avoid
  speculative pinning). (Spike M2 output + M6 annotations.)

**Accept:** `number | string` annotation accepts `number`/rejects `boolean`;
intersection annotation satisfied by a value at both member types; both
round-trip through the printer; inferred unions from multi-branch returns.

---

## M6 — Conformance corpus + differential harness

- A comprehensive, **checker-agnostic** corpus encoding language semantics
  (`(source, expected type | expected error)`), the long-wanted fixtures
  upgrade. Expected outputs are authored to the semantics we *want*, not copied
  from the old checker.
- A **differential harness**: parse once, run both checkers on the same tree
  (into separate side tables), diff, and **triage** each divergence as
  match / intended-improvement / bug. (Automates the spike's M8, but as a triage
  tool, not a conformance gate.)
- Wire the new checker behind a flag at the **3** `compiler.NewChecker` sites.

**Accept:** corpus runs green on the new checker; differential harness runs over
the existing `fixtures/`, with every divergence triaged (no untriaged diffs).

**Gate:** pervasive *unintended* divergence ⇒ the new checker has correctness
gaps; burn down before proceeding.

---

## M7 — Type-level operators

The last MVP milestone. `keyof`, indexed access, conditional types: Baseline-D
(reduce when operands ground) + Design-A residual nodes reduced post-coalescing,
+ recursive-type handling (cycle cache + depth budget, and the level-2
regularity check). (Spike M5/M7/M9 + recursion + CheckRegular.)

**Accept:** the spike's type-operator cases against real source —
`keyof`/indexed access over ground and usage-inferred operands; conditional
types incl. `infer` and distribution; recursive aliases terminate (finite knot
or budget). Errors (e.g. arity, non-regular recursion) assert full messages.

---

## Later (post-MVP)

- **M8 — Codegen.** Either a `soltype → type_system` bridge to reuse codegen
  unchanged, or port codegen (`dts.go` et al., ~4 files / ~30 refs) onto
  `soltype`. Decide when the checker is proven.
- **M9 — LSP.** Switch the LSP to the new checker's `Scope`/`Info`.
- **M10 — Flip & cleanup.** Make the new checker the default; retire the old
  checker + its tests; **delete** the AST `inferredType` field, the
  `type Type = type_system.Type` alias, and `tools/gen_ast`'s generation of the
  field — leaving the AST fully type-system-agnostic.

## Dependency / risk ordering rationale

- M4 is front-loaded as the combined "core value types" milestone because
  records, `mut`, and lifetimes are an inseparable cluster once lifetimes ride on
  values — and it contains the highest-risk gate (`mut` invariance).
- Codegen is deferred to the latest safe point because it is the single largest
  integration cost (its `type_system` dependency) and is not needed to prove the
  checker.
- The corpus (M6) is "improve, don't match," so the differential harness is a
  triage tool — this is what lets intended improvements through instead of
  forcing old-checker parity.
