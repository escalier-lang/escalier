# M4 implementation plan — Core value types: records + usage-based inference + `mut` + lifetimes

This is the implementation plan for **M4** as defined in
[01-milestones.md](01-milestones.md) §"M4 — Core value types". It assumes M1
(package skeleton + `soltype`), M2 (parser/resolver bridge), and M3 (functions,
application, let-polymorphism, function exactness) have landed. It is grounded in
the design in [02-design-notes.md](02-design-notes.md) (the `RefType` wrapper, the
exactness flag, the `Probe` API, the provenance/`Info` side tables) and the
lifetime lattice in [03-references.md](03-references.md).

## Why M4 is "the big one"

M4 promotes three spike milestones at once and they are **inseparable**:

- **records/objects** (spike M2) — the first *value* type that can be borrowed;
- **`mut`** (spike M3) — invariant mutable references, encoded by the read/write
  decomposition; the **highest-risk gate** of the whole migration;
- **lifetimes** (spike M4) — a *second sort* solved by the same `constrain`
  machinery, riding on the `RefType` wrapper.

Lifetimes ride on borrows, records are the first thing that can be borrowed, and
`mut` borrows are what first populate a lifetime. Trying to land any one of them
without the others produces a representation you immediately have to rework, so
the milestone is deliberately scoped as a cluster. Exactness (the `exact` flag on
`RecordType`/`TupleType`) also lands here because, per the settled exactness
decision, the flag is introduced *with* each former rather than retrofitted.

The plan below decomposes the cluster into a strictly-ordered PR sequence that
keeps `go test ./...` green at every step and **clears the highest-risk gate as
early as the representation allows**.

## Scope (from the milestone definition)

In:

1. Owned `RecordType` / `TupleType` with the `exact` flag (default exact;
   trailing `...` ⇒ inexact). Object/tuple **literals infer as exact**.
2. The unified `RefType{mut, lt, inner}` borrow wrapper (per
   [02-design-notes.md](02-design-notes.md) §"`soltype`"), with the `RefInner`
   sealed marker and the `borrowableType` content predicate.
3. **Usage-based inference**: member read `obj.bar` ⇒ `constrain(obj <:
   RecordType{bar: β})`; field requirements accumulate as upper bounds and
   coalesce (negative position) into a record/intersection. Replaces
   `Open`/`Widenable`/`ArrayConstraint`. Usage-collected shape coalesces **exact**
   by default (Policy A); the `open` parameter marker opts back into row
   polymorphism.
4. **The single `RefType` constrain rule**: mutability compatibility, `mut`-driven
   inner invariance via read/write decomposition, covariant lifetime, mutability
   decay, plus the two cross-cases (`bare <: RefType`, `RefType <: bare`).
   Field-write inference (`obj.x = v` ⇒ `mut` record with a fresh lifetime) and
   read-after-write field collapse.
5. **Lifetimes as a second sort**: `LifetimeVar` (bound lists over the outlives
   lattice, `'static` = top), `constrainLt`, lifetime coalescing + elision,
   borrow origination at `RefType`-typed parameters, multi-source-return unioning,
   escape ⇒ `<: 'static`.
6. **Mutability-transition checking** ported from the old checker
   (`internal/liveness/` verbatim + the two narrow predicates over `soltype`),
   wired onto `solver.Context`.

Out (deferred): nominal classes (M5), unions/intersections as *input annotations*
(M6 — though M2/M4 already produce them as coalescing *output*), type-level
operators (M8), codegen / value-level `exact<T>(v)` (M9).

## Prerequisites and assumptions

- `soltype` already has `TypeVarType` (bound lists + level), `PrimitiveType`,
  `LiteralType`, `FunctionType`, `TupleType` scaffolding, `constrain`, extrusion,
  let-polymorphism, simplification, and polarity-driven coalescing (M1–M3).
- The `Info` side table and the `Prov` provenance side table exist (M1).
- The `Probe` API (length-snapshot journal, design (A) in
  [02-design-notes.md](02-design-notes.md)) exists; M4 does **not** need
  speculation for its core path (bound-list monotonicity covers failed
  constraints), but the field-write/read-after-write merge and any future
  union-against-variable deferral should be probe-aware where they trial
  constraints.
- The constraint-generating AST walk (`infer.go`) handles `IdentExpr`,
  `FuncExpr`, `CallExpr` (M2/M3). M4 adds `MemberExpr` (read + write),
  `ObjectExpr`, `TupleExpr`, and conditional/branch joining.

## Architecture / where the code lands

Per the package layout in [02-design-notes.md](02-design-notes.md) (names
provisional):

```
internal/solver/
  soltype/
    type.go        RecordType, TupleType (exact flag), RefType, RefInner marker
    lifetime.go    LifetimeVar, StaticLifetime, LifetimeUnion (the second sort)
    print.go       record / tuple / `mut 'a T` / `'a T` / exact `...` rendering
  constrain.go     record & tuple structural+width rules (exact-aware);
                   the single RefType rule; constrainLt; borrowableType
  coalesce.go      negative-position record coalescing; lifetime join/meet;
                   lifetime elision (mut elide-in-place vs immutable drop-wrapper)
  simplify.go      occurrence analysis extended over record fields / Ref inner / lt
  infer.go         MemberExpr read/write, ObjectExpr, TupleExpr, branch joining,
                   attachParamLifetimes; field-write & read-after-write
  transitions.go   ported checkMutabilityTransition over soltype (new in M4)
  context.go       solver.Context gains Liveness / Aliases fields
```

`internal/liveness/` is reused **verbatim** (it operates on the name-resolved AST
and has no `type_system` references — see the milestone note).

## Sequencing rationale

The ordering is driven by two forces that mostly agree:

1. **Dependency order.** A carrier must exist before it can be borrowed; the
   `RefType` wrapper must exist before a lifetime can ride on it; transition
   checking consumes the lifetime sort.
2. **Risk order.** The milestone names the `RefType` rule's `mut`-driven inner
   invariance as the **HIGHEST-RISK gate**: *"if it cannot be encoded cleanly
   against the real AST, the whole migration is in question."* So it must be hit
   as early as the dependency order permits, and nothing expensive (lifetimes,
   transition port) should be built before it is cleared.

These agree: records (carrier) → usage-based reads (needed to produce non-trivial
record shapes worth borrowing) → **`RefType` + `mut` gate** → lifetimes →
transition checking. The gate lands third — right after the minimum needed to
exercise it against the real AST.

De-risking note: the spike already proved the `mut`-invariance encoding in
isolation (`internal/simplesub` M3, `mut {x,y} <: mut {x}` fails while immutable
succeeds). PR 3 is therefore **re-validation against the production AST/`soltype`
representation**, not novel research — but it is still the go/no-go gate, so its
acceptance set is the spike's `mut` cases reproduced end-to-end from real source.

## PR breakdown

Each PR is independently reviewable, lands behind no flag (the new checker is not
yet wired into the compiler — that's M7), keeps `go test ./...` green, and ships
its own table-driven tests asserting **rendered types** and **full error
messages** (per CLAUDE.md). Sizes are rough.

### PR 1 — Owned records & tuples + exactness + literal inference

Representation and structural subtyping for the carriers, no borrows yet.

- `RecordType{fields, exact}` and extend `TupleType` with `exact` (M1 stubbed
  `TupleType`; this completes it). Smart constructors; printer support for
  `{x: T, y: U}`, `[T, U]`, and the trailing `...` for inexact.
- `constrain` structural cases:
  - record `<:` record: per-field covariant; **width subtyping only in the
    inexact `<:` inexact case**; exact `<:` exact requires the *same* field set;
    exact `<:` inexact allowed; inexact `<:` exact rejected (the one-way rule
    from [02-design-notes.md](02-design-notes.md) §"Exactness").
  - tuple `<:` tuple: per-slot covariant; length rules mirror the exactness rule.
- Inference (`infer.go`): `ObjectExpr` ⇒ `RecordType` (exact), `TupleExpr` ⇒
  `TupleType` (exact), over inferred element types.
- Coalescing: positive-position record/tuple recursion (fields are covariant).
- **Tests**: object/tuple literal inference renders exact; exact `{x,y}` assignable
  to inexact `{x,y,...}` but not the reverse; extra member on an exact target
  rejected (full message); width subtyping works inexact-to-inexact.
- **~Size**: medium. No `mut`, no lifetimes, no usage inference — deliberately the
  smallest first slice so the carrier and exactness rule are reviewed alone.

### PR 2 — Usage-based inference (member reads) + the `open` marker

Turn member access into constraints; this is what makes record shapes worth
borrowing in PR 3.

- `infer.go` `MemberExpr` (value-typed receiver, read): `constrain(recv <:
  RecordType{field: β})` with `β` fresh at the current level; record the field
  type for the eventual read-after-write path (stubbed here, completed in PR 3).
- Negative-position coalescing (`coalesce.go`): a variable's upper-bound record
  requirements **merge into one record** (intersection of object bounds → single
  record), coalescing as **exact** by default (Policy A — row closed once body
  inference completes).
- The `open` parameter marker (keyword provisional, e.g. `fn dist(open p) =>`):
  keeps the usage-inferred param **inexact** so callers may pass richer records.
  Lands here because this is the first milestone with record-typed params.
- Namespace-qualified `MemberExpr` (`ns.foo`) routing is M2's concern; confirm the
  value-typed-receiver path is correctly distinguished (see
  [02-design-notes.md](02-design-notes.md) §"the constraint-generating AST walk").
- **Tests**: `fn (p) => p.x + p.y` infers an (exact) `{x, y}`-shaped param; `open`
  variant infers inexact `{x, y, ...}`; accessing a missing field on a known exact
  record is rejected; the replacement of `Open`/`ArrayConstraint` behavior is
  covered by re-expressing representative old-checker cases as intended-form table
  tests.
- **~Size**: medium.

### PR 3 — `RefType` + `mut` (THE GATE)

The single borrow wrapper and the one constrain rule. This clears the
highest-risk gate.

- `soltype`: `RefType{mut, lt, inner}` with `lt` left **nil** throughout this PR
  (lifetimes arrive in PR 4); the `RefInner` sealed marker interface (only
  `RecordType`/`TupleType`/`ClassType`(future)/`AliasType`/`UnionType`/
  `IntersectionType`/`TypeVarType` qualify) and the `borrowableType` content
  predicate. The `NewRef` smart constructor enforcing the degenerate-cell
  invariant (`{false, nil}` ⇒ return bare `inner`). Helpers `unwrapRef` /
  `carrierOf`. Printer: `mut Point`, and bare-inner pass-through for the
  degenerate cell.
- `constrain` — the **one** `RefType` rule (per
  [02-design-notes.md](02-design-notes.md) §"The one `RefType` constrain rule"),
  with lifetime steps written but inert while `lt == nil`:
  1. mutability compatibility (`!l.mut && r.mut` ⇒ error);
  2. inner variance — **bidirectional iff `r.mut`** (read view always +
     contravariant write view when the target writes) = the read/write
     decomposition that encodes invariance; covariant-only otherwise;
  3. lifetime step (no-op here);
  - plus `bare <: RefType` (wrap source as immutable/no-lt and re-dispatch) and
    `RefType <: bare` (peel; escape-error guard becomes live in PR 4).
- Field-write inference (`infer.go`): `obj.x = v` ⇒ `constrain(obj <: RefType{mut:
  true, lt: nil, inner: RecordType{x: widen(v)}})` with literal widening; multiple
  writes merge into one mutable record. Read-after-write: a read of a
  just-written field returns the written type.
- **Acceptance (the gate)**: reproduce the spike's `mut` cases from real source —
  `mut {x,y} <: mut {x}` **fails** while immutable `{x,y} <: {x}` succeeds by
  width subtyping; `fn foo(obj) { obj.x = 5; obj.y = 10 }` infers `(obj: mut {x:
  number, y: number}) -> unit`; `fn foo(obj) { obj.x = 5; return obj.x }` infers
  `(obj: mut {x: number}) -> number`; mut-borrow decay (`mut {x} <: {x}`) allowed,
  the reverse rejected (full messages).
- **Gate decision**: if the `mut`-driven invariance cannot be encoded cleanly here,
  **stop and reassess** before PR 4/5 — that is the milestone's instruction.
- **~Size**: medium-large. This is the conceptual core of M4.

### PR 4 — Lifetimes as a second sort

Populate the `lt` field that PR 3 left nil; activate the lifetime steps of the
`RefType` rule.

- `soltype/lifetime.go`: `LifetimeVar{lowerBounds, upperBounds}`,
  `StaticLifetime` (top), and the single `LifetimeUnion` flow-set surface
  representation (per [03-references.md](03-references.md) — one list, read as
  join in positive position / meet in negative). `constrainLt` mirroring
  `constrain` over the outlives lattice.
- Activate `RefType` rule step 3 (covariant lifetime; the `RefType <: bare`
  escape-error when `l.lt != nil`) and the `bare <: RefType` owned-satisfies-any
  branch.
- Borrow origination: `mut`/immutable `RefType`-typed parameters get a **fresh
  lifetime** (`attachParamLifetimes` in `infer.go`); returning a borrow shares the
  lifetime by value identity; multi-source returns **union** lifetimes via a fresh
  join var; escape to module/static storage ⇒ `constrain(lt <: 'static)`.
- Field-write target lifetime becomes a **fresh variable** (not nil) so the
  receiver may be owned-mutable or a mut-borrow of any lifetime (per
  [02-design-notes.md](02-design-notes.md) §"the constraint-generating AST walk",
  assignment-to-member case).
- Lifetime coalescing + **elision**, branching on `mut` at the elision site:
  mutable borrow with elided lt ⇒ elide-in-place (`RefType{mut:true, lt:nil}`,
  well-formed owned-mutable); **immutable** borrow with elided lt ⇒ **drop the
  wrapper entirely** (else it becomes the forbidden degenerate cell).
- **Acceptance**: the canonical lifetime cases from real source —
  `IdentityRefReturn` ⇒ `fn <'a>(p: mut 'a {x: number}) -> mut 'a {x: number}`;
  `FreshObjectReturn` carries no lifetime; `ConditionalUnionReturn` ⇒ `mut ('a |
  'b) {x: number}`; `EscapingRefIntoStatic` ⇒ `mut 'static`; property-level and
  tuple-per-slot lifetimes; read-after-write field collapse with a lifetime
  present. Plus: `RefType` neither tightens nor loosens the inner's exactness (the
  inner carrier's `exact` flag passes through unchanged).
- **~Size**: large. The second-sort machinery + elision branching is the bulk.

### PR 5 — Mutability-transition checking (port)

The flow-sensitive mutable↔immutable alias-creation check, ported with minimal
adaptation (per the milestone's "reuses existing infrastructure" note).

- Reuse `internal/liveness/` **verbatim** (`VarID`/`CFG`/`AliasTracker`/
  `LivenessInfo`); reuse `liveness_prepass.go`.
- Reimplement the two narrow predicates over `soltype`: `isValueType(t)` and
  `isMutableType(t)` (the latter becomes `if r, ok := t.(*soltype.RefType); ok {
  return r.mut }; return false`).
- `checkMutabilityTransition`'s Rule 1 / Rule 2 / Rule 3 logic is **unchanged** —
  it talks only to `liveness.Liveness`/`liveness.AliasTracker`.
- `solver.Context` gains `Liveness` / `Aliases` fields, populated by the existing
  prepass.
- **Simplification**: collapse the `HasStaticMutAlias` / `HasStaticImmAlias`
  escape-hatch bits — under the new checker the escape is first-class
  (`lt <: 'static` is in the inference output), so the transition checker queries
  the lifetime sort directly. This depends on PR 4, which is why it is last.
- **Acceptance**: port the old checker's transition-checking fixtures/cases as
  intended-form table tests; the static-escape cases pass via lifetime queries
  rather than the dropped bits.
- **~Size**: medium (mostly a port; the simplification is the only genuinely new
  logic).

## Sequencing summary

```
PR1 records+tuples+exactness ─► PR2 usage-based reads ─► PR3 RefType + mut  (GATE)
                                                              │
                                                              ▼
                                              PR4 lifetimes (second sort)
                                                              │
                                                              ▼
                                              PR5 transition checking (port)
```

- PR1 → PR2: usage inference needs the record carrier and its coalescing.
- PR2 → PR3: the `mut` write path and read-after-write build on the member-access
  path; non-trivial inferred record shapes make the gate's tests meaningful.
- **PR3 is the gate** — do not start PR4/PR5 until it is cleared.
- PR3 → PR4: lifetimes ride on the `RefType` wrapper PR3 introduces.
- PR4 → PR5: the transition-check simplification (dropping the static-alias bits)
  consumes the lifetime sort.

PR1 and the *test scaffolding* for PR2 can be drafted in parallel, but the merge
order is strict. PR4 and PR5 cannot be meaningfully parallelized because PR5's
simplification depends on PR4's escape-as-`'static` output.

## Testing strategy

Following [02-design-notes.md](02-design-notes.md) §"Test coverage" and CLAUDE.md:

- **Granular semantics** as table-driven `*_test.go` in the new checker package,
  keyed by test name with `expectedValues` / `expectedTypes` (rendered Escalier
  type-annotation strings) and `expectedError` (**full** message). Authored
  against intended semantics, not copied from the old checker.
- Where a type tree is large (e.g. nested borrowed records with per-field
  lifetimes), render the subtree and use an inline snapshot rather than many
  drill-down assertions.
- Each PR carries the acceptance set named in its section above; the union of
  those sets is the milestone's M4 acceptance criteria from
  [01-milestones.md](01-milestones.md).
- No fixture-harness wiring yet (that is M7); M4 validation is entirely the new
  package's own tests driven from real `.esc` source through the M2 parser bridge.

## Risks and open questions

- **The gate (PR 3)** is the dominant risk; it is front-loaded and has an explicit
  stop-and-reassess decision. The spike de-risked the *algorithm*; the residual
  risk is encoding it cleanly against the production AST and `soltype`.
- **Lifetime elision branching on `mut`** (PR 4) is the subtlest non-gate piece —
  the immutable-borrow-must-drop-the-wrapper rule is easy to get wrong and
  reintroduce the forbidden degenerate cell. The `NewRef` smart-constructor
  assertion is the backstop; the coalescer must branch on `mut` at the elision
  site.
- **`open` keyword** is provisional (PR 2). If naming is unsettled, gate the
  surface syntax behind the parser and keep the semantic flag; the inference
  behavior is what M4 must prove, not the spelling.
- **Probe usage**: M4's core path is monotone and needs no rollback, but the
  field-write merge and any union-against-variable deferral should be written
  probe-aware so M6/M8 speculation composes without rework.
- **`exact`-by-default surface**: per the milestone, the exactness default for
  usage-inferred shapes (Policy A) and the `open` opt-out are **not yet reflected
  in `planning/exact-types/requirements.md`**. That spec section should be written
  before PR 2 lands so the tests assert an agreed default.
