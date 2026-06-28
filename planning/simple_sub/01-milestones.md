# 01 — Milestones

Ordered milestones for the new checker. Each is independently testable and
leaves the old checker fully working. "Structural core first"; lifetimes are
introduced **with the first lifetime-carrying type** (records, M4). The MVP is
M1–M9 (structural core + nominal classes + unions/intersections + library type
resolution + fixture differential + type-level operators); codegen/LSP and the
cutover come after.

Spike provenance is cited where a milestone promotes proven spike work
(`internal/simplesub/`).

**Exactness runs through several milestones.** Escalier's structural formers
(objects, tuples, functions, unions) are **exact by default** — closed, no
extra members — with inexactness opted into via a trailing `...`
([exact-types/requirements.md](../exact-types/requirements.md)). Architecturally this is a flag on each
former that flips width subtyping on/off, the same "born-with-the-type" shape as
lifetimes — so the **representation** (an `exact` flag) and the **one-way
`exact <: inexact` subtyping rule** are introduced *with* each former (M3–M6),
not retrofitted. The richer machinery (`Exact<T>`/`Inexact<T>` type operators,
exactness propagation through `keyof`/mapped/conditional types, the value-level
`exact<T>(v)` lowering, and the `std:*`/`dom:*` annotation effort) is deferred to
M9 and later. See [02-design-notes.md](02-design-notes.md) §"Exactness" for the representation and
rules. The **default is settled**: Escalier code is exact-by-default, TypeScript
imports are inexact-by-default, and each former implements its default *as it
lands* (M3 functions, M4 records/tuples, M5 class instances via `final`,
M6 unions) — tests at each milestone assert what the implementation produces.
(The usage-inferred-shape default — that usage-collected shapes coalesce as
**exact** — and the `open` parameter marker that opts back into row polymorphism
are recorded in
[exact-types/requirements.md](../exact-types/requirements.md) §8.1.)

## Contents

- [M1 — Package skeleton + `soltype`](#m1--package-skeleton--soltype)
- [M2 — Parser/resolver bridge](#m2--parserresolver-bridge)
- [M2.5 — Provenance side table + precise error spans](#m25--provenance-side-table--precise-error-spans)
- [M3 — Functions, application, let-polymorphism](#m3--functions-application-let-polymorphism)
- [M4 — Core value types: records + usage-based inference + `mut` + **lifetimes** + destructuring/`match`](#m4--core-value-types-records--usage-based-inference--mut--lifetimes--destructuringmatch)
- [M4.5 — Script inference](#m45--script-inference)
- [M5 — Nominal types (classes)](#m5--nominal-types-classes)
- [M6 — Unions / intersections](#m6--unions--intersections)
- [M6.5 — Lifetime bounds](#m65--lifetime-bounds)
- [M7 — Library type resolution (`std:*` / `web:*` / `node:*`)](#m7--library-type-resolution-std--web--node)
- [M8 — Second fixture harness + differential triage](#m8--second-fixture-harness--differential-triage)
- [M9 — Type-level operators](#m9--type-level-operators)
- [Later (post-MVP)](#later-post-mvp)
- [Dependency / risk ordering rationale](#dependency--risk-ordering-rationale)

---

## M1 — Package skeleton + `soltype`

Stand up the new package and its type representation — the structural-core
subset, no polymorphism rendering yet. See
[m1-implementation-plan.md](m1-implementation-plan.md) for the full PR
breakdown, design rationale, and per-file sketches.

- New package as a **top-level sibling to `internal/checker/`**
  (`internal/solver/`, with `internal/solver/soltype/` for the type
  representation). The spike lives at `internal/simplesub/`, but the
  production package sits beside the old checker so both can be built and
  differential-tested side-by-side, and so the old `internal/checker/` tree
  can be deleted wholesale at cutover.
- **`soltype` types** promoted from the spike, with shapes mirroring
  `type_system` where they're cleaner: `TypeVarType` (bound-list inference
  variable with `ID`/`Level`/`LowerBounds`/`UpperBounds`); `PrimType` with a
  closed `Prim` enum (`NumPrim`/`StrPrim`/`BoolPrim`); `LitType` wrapping a
  sealed `Lit` interface (`NumLit`/`StrLit`/`BoolLit`); `FuncType` whose
  `Params` are `*FuncParam` carrying a sealed `Pat` (M1 ships only
  `IdentPat`); `TupleType`; `Void`; plus the lattice bounds `NeverType` (⊥)
  and `UnknownType` (⊤); plus `UnionType` / `IntersectionType` for
  multi-bound coalesced output. `Polarity` enum lives in `soltype` so
  `TypeVarType.BoundsAt(pol)` can take it without inverting the
  `soltype`/`solver` package boundary.
- **The constraint engine** — `constrain(sub <: super)` with the coinductive
  seen-cache (pointer-identity keying, sufficient for M1's non-recursive
  type set), the structural cases (`PrimType`/`LitType`/`FuncType`/
  `TupleType`/`Void`) for the M1 set, the variable cases (bound-append +
  transitive propagation), and **levels + extrusion**. Note: M1 is
  **uniformly exact** (Escalier is exact-by-default), so both the
  same-length tuple rule *and* the same-arity function rule are the *exact*
  "one side" of the exact/inexact split; the inexact arms
  (fewer-params-is-subtype for functions, `longer <: shorter` for tuples)
  land with the exactness flag in M3 (functions) and M4 (tuples).
- **Bound-inlining coalescing** — `coalesce(t, pol)` walks a bound-carrying
  `soltype.Type` and returns a coalesced `soltype.Type` by inlining every
  `TypeVarType` to its bounds (positive ⇒ union of lowers, negative ⇒
  intersection of uppers; empty positive ⇒ `never`, empty negative ⇒
  `unknown`). No bipolar-variable retention, no occurrence analysis, no
  named-type-param refs — these are deferred to M3 along with the rest of
  the polymorphism-rendering bundle (`Scheme`s,
  `instantiate`/`freshenAbove`, `analyze`, named-ref node informed by M4's
  alias-ref needs, co-occurrence merging, `<T0, …>` quantifier prefix).
- **A printer for `soltype.Type`** (its own, not `type_system.PrintType`)
  rendering the M1 coalesced type set in Escalier type-annotation syntax.
  No `<T0, …>` quantifier prefix in M1 — nothing to collect.
- **Sealed `SolverError` interface with per-kind concrete structs**
  (`CannotConstrainError`, `FuncArityMismatchError`,
  `TupleLengthMismatchError`, …), modeled on
  [internal/checker/error.go](../../internal/checker/error.go). Each struct
  carries typed `soltype.Type` references so LSP/tooling consumers can
  inspect what an error refers to without scraping the rendered message.
  Wording matches the spike's `fmt.Errorf` strings verbatim. Errors are
  span-free in M1; M2 adds `Span()` and may rebase onto the old checker's
  diagnostic types per [02-design-notes.md](02-design-notes.md) Settled
  Decision #4.
- **`Info` side table**: `map[ast.Node]soltype.Type` + `TypeOf`/`setType`.

**Accept:** unit tests for `constrain` (prim/function variance with exact
arity — same-arity required, both fewer and more params rejected, parallel
to the exact-tuple same-length rule; levels + extrusion with no
inner-level var leakage into outer-level bound lists); coalescing
(inline-to-bounds, empty-bound collapse to `never`/`unknown`, multi-bound
union/intersection); printer round-trips for the M1 coalesced type set
(prims, literals, tuples, multi-arg functions, `never`/`unknown`,
`number | string`, `number & string`). The identity rendering
(`fn <T0>(x: T0) -> T0`) is **deferred to M3** along with the rest of the
polymorphism-rendering bundle — see [m1-implementation-plan.md](m1-implementation-plan.md) §3.3.

---

## M2 — Parser/resolver bridge

**Status: landed** (PRs #695–#703 + PR-6 multi-file resolution; see
[m2-implementation-plan.md](m2-implementation-plan.md) §8 exit checklist — all
items complete).

Replace the spike's hand-built IR with a real constraint-generating walk over
`*ast.Module`. This is the deferred spike "parser bridge."

- Drive from real source: `parser.Parse*` → `*ast.Module` → `dep_graph` /
  `resolver` → a constraint-generating AST visitor that produces `soltype` and
  populates `Info`.
- Produce a `Scope`/`Binding`/`Namespace` analogue owned by the new package (its
  own, not `type_system`'s).
- A fixture-style harness: given `.esc` source, infer and assert the rendered
  binding types (its own assertions, independent of the old checker).
- **Stdlib types: prerequisite tracking.** Real source uses constructs whose
  type rules reference standard-library type names — `await e` needs
  `Promise<T>`; `for (x in xs)` and `for await (x in xs)` need an
  `Iterable<T>` / `AsyncIterable<T>` protocol type; `yield e` needs
  `Generator<Y, R, TNext>` / `AsyncGenerator<…>`; iteration-related
  built-ins need a `{value, done}` `IteratorResult<T>`. None of these
  type-checking *rules* land in M2 (they sit in the milestones that own the
  language features below), and the **real** stdlib type definitions are ingested
  in **M7** (library type resolution), once the representational machinery they
  need (generics, objects, classes, unions) exists. M2's narrower job is just to
  **seed placeholder bindings** for these names — hand-built opaque `soltype`
  stubs in the new checker's prelude — so a *reference* resolves without an
  unbound-name error and downstream rules can be authored against a stand-in. M2
  does **not** read the real stdlib decls (that path is old-checker- and
  `type_system`-coupled, and `soltype` has no generic-type node until M3/M4);
  M7 swaps the placeholders for real structures. See the M2 implementation
  plan §3.8.

**Accept:** top-level `val`/`fn` declarations from real source infer correct
rendered types end-to-end; multi-file module via the dep graph resolves;
references to the stdlib type names listed above resolve to placeholder
`soltype.Type` stubs without an unbound-name error (real structures and the
rules that *use* them land in M7 and the feature milestones).

**Gate:** if driving from the real AST/dep-graph requires reaching back into the
old checker's internals, the parallel-package boundary is wrong — stop and
reassess.

---

## M2.5 — Provenance side table + precise error spans

**Status: landed** (see [m2.5-implementation-plan.md](m2.5-implementation-plan.md)
§8 exit checklist — all items complete). The `Prov` side table, the node-derived
`SolverError` reshape (`Span()`/`Related()`, `errSpan`/`setSpan` removed), the
per-operand blame via `Prov`, and the `val`-annotation enabler all live in
[internal/solver/](../../internal/solver/) ([prov.go](../../internal/solver/prov.go),
[errors.go](../../internal/solver/errors.go)), with golden span fixtures in
[blame_test.go](../../internal/solver/blame_test.go) and population tests in
[prov_test.go](../../internal/solver/prov_test.go).

A focused infra milestone, **not** a language feature. See
[m2.5-implementation-plan.md](m2.5-implementation-plan.md) for the full PR
breakdown, the per-operand blame design, and the construction-site population
table. M2 stamps the *umbrella*
node's span on every constraint error — `constrain(n, source, target)` sets `n.Span()`
on each returned error ([internal/solver/infer.go](../../internal/solver/infer.go)
`(*checker).constrain`), so a mismatch deep inside a large declaration blames the
whole declaration. This milestone makes blame point at the **narrowest source of
each type that actually contributed** to the failure. It is "born-with-the-type"
infra (the lifetime/exactness lesson again): threading a provenance entry at each
construction site is cheap done once across M2's ~8 sites, painful to retrofit
once M3–M6 multiply them — so it lands *before* M3.

- **`Prov: Type → Origin` side table.** Promote the deferred design from
  [02-design-notes.md §"Provenance side table"](02-design-notes.md) — a sparse
  map keyed by `soltype.Type` pointer identity, the inverse of `Info`. M2.5 ships
  the **leaf** variant `FromAST{Node, Kind}` only. The interior edge kinds
  (`FromBoundPropagation`, `FromInstantiation`, `FromExtrusion`, `FromCoalesce`)
  are deferred and **ride along** with the M3+ operations that mint them
  (instantiation is M3; bound-propagation/coalesce/extrusion already exist but
  their multi-hop *renderer* lands with the features that make the chains deep).
- **Populate `FromAST` at the construction sites.** The places the M2 walk mints
  a type from a node: literal inference, ident resolution, param binding
  (`inferFunc`), application result var (`inferCall`), tuple elements, object
  field values, member-access result var. A single `prov[t] = FromAST{n, kind}`
  per site, via a node+kind-taking helper (design notes "Population discipline").
- **Per-operand blame at the error path.** Each `SolverError` already carries
  typed `Sub, Super soltype.Type` references
  ([internal/solver/errors.go](../../internal/solver/errors.go)). Replace the
  single umbrella stamp with a lookup: map each failed operand to its narrowest
  `FromAST` span via `Prov` and stamp the **most specific contributing node**,
  falling back to `n` when an operand has no entry (a shared atom or a
  synthesized bound).
- **Multi-span errors.** Extend `errSpan` to carry a primary span plus *related*
  spans, so a mismatch can point at **both** the expected-source and the
  actual-source location (e.g. annotation site *and* offending literal) instead
  of one node that merely dominates both.
- **Honest limitation (drives the leaf/interior split).** Leaf-only blame is
  precise whenever an operand traces directly to a literal/param/field. An
  operand that is itself a *synthesized* type (a coalesced or propagated bound)
  has no single AST node; chasing its blame back needs the interior edges, which
  arrive with M3+. M2.5's fallback-to-`n` keeps those cases no worse than today.
- **Perf invariant.** The hot `constrain`/`coalesce` loops must never consult
  `Prov`; it is read only on error paths and by LSP/diagnostic consumers (design
  notes), so the map-lookup cost stays off the inference critical path.

**Accept:** a golden set of fixtures asserting *exact* error spans —
`val x: number = "hi"` blames the `"hi"` literal, not the whole decl; a record
field-type mismatch blames the offending field value; a tuple-length mismatch
points at the tuple literal; a missing-property read blames the member's prop,
not the receiver. Existing M2 error fixtures (which assert placeholder spans) are
updated to the narrowest real spans.

**Depends on:** M2 (the constraint-generating walk, `Info`, and the bridge error
kinds). Precedes M3 so the table and the `FromAST` discipline exist *before*
scheme instantiation introduces the first interior origin (`FromInstantiation`).

---

## M3 — Functions, application, let-polymorphism

> **Baseline:** M2 already shipped the **monomorphic** function/application/block
> walk (`inferFuncExpr`/`inferFunc`/`inferCall`/`inferBlock`), the dep-graph SCC
> ordering, and recursive-group resolution (freezing each group to a coalesced
> monotype). M3 does **not** rebuild that walk — it layers the **polymorphic**
> machinery (and async, exactness, overloading) on top. See
> [m3-implementation-plan.md](m3-implementation-plan.md) for the PR-by-PR plan.

- Lambda/`fn` decls, application, multi-arg functions — **monomorphic forms
  already done in M2**; M3's contribution here is making them *polymorphic* (the
  generalization + simplification below), not building the walk.
- Level-based let-generalization (instantiate / freshenAbove): replace M2's
  monomorphic SCC freeze (`coalesce` to a monotype) with generalization into a
  `PolyScheme` at the binding boundary, swap `ValueBinding.Type` for a scheme
  (retaining its `Sources`), and add the `<T0, …>` quantifier prefix to the
  printer. (The `coalesce` recursion `seen`-guard already shipped in M2 PR-5;
  what remains for M3 is the precise μ-bound recursive *rendering*.)
- The simplification pass: single-polarity elimination + co-occurrence variable
  merging (so generalized signatures render compactly, and parameter-only
  variables coalesce to `unknown` rather than a vacuous `<T0>` — a blessed
  improvement).
- **`async fn` and `await e`.** An `async fn () -> T` is internally typed
  exactly like a plain function (the body has return type `T`), but its
  *external* type is `fn () -> Promise<T>` — the async modifier wraps the
  return in `Promise<…>`. `await e` requires `e <: Promise<U>` for some `U`
  and produces `U`; the constraint emitted is just that subtype check, with
  `U` minted fresh and inferred from `e`'s bounds like any other inference
  variable. Awaiting outside an `async` function is rejected by the AST
  walk, not by the type rule. Nested `Promise<Promise<T>>` does *not*
  auto-flatten in this milestone — `Awaited<T>` (the recursive-conditional
  flattening) is a type-level operator that lands in M9; user code that
  cares about flattening writes `Awaited<T>` explicitly until then.
  Depends on `Promise<T>` being available from the stdlib (M2 placeholder;
  real resolution lands in M7).
- **Function exactness flag.** `Function` carries an `exact` flag; a bare
  `fn(...)` is exact, `fn(..., ...)` is inexact. **Direct calls reject extra
  args regardless of exactness** (an inexact function ignores them, but passing
  them is almost always a bug — flag it, as TypeScript does). Exactness instead
  governs **callback subtyping**: a function type accepts the set of arg-counts
  it can be invoked with (exact `[required, declared]`, inexact `[required, ∞)`),
  and `G <: F` iff `G` accepts every arg-count `F`'s holders may invoke with
  (params contravariant, return covariant). M1 ships only the *exact* case
  (same-arity required); this milestone adds the *inexact*
  fewer-params-is-subtype case (the spike's uniform rule) once the flag
  exists to opt into it. (This corrects the merged spec's §4.2, which had
  exactness govern call-sites rather than subtyping — see
  escalier-lang/escalier#677.)
- **Block return-point join (carried over from M2).** M2's block walk uses the
  *last statement* as the block's value and only constrains that tail against a
  declared return type; a non-tail `return` (`{ return X; Y }`) is dropped. This
  is harmless at the M2 bar (no `IfElseExpr`, so an early return cannot come from
  a real branch), but once this milestone adds conditionals/early return the walk
  must collect **every** `ReturnStmt` type (valued and bare) and join them with
  the tail expression before constraining against the return annotation. See
  `internal/solver/infer_stmt.go` (`inferBlock` TODO(M3)).

**Accept:** the spike's Category-A cases against real source:
`TopLevelLetPolymorphism` ⇒ `fn <T0>(x: T0) -> T0`; `IdentityPolymorphism` ⇒
`fn () -> ["hello", 5]`; `InnerCapturesOuterParam` ⇒ `fn <T0>(y: T0) -> [T0, T0]`.
(Spike M1.) Plus, on function exactness (per escalier-lang/escalier#677):
**both** exact `fn(x, y)` and inexact `fn(x, y, ...)` reject a 3-argument *direct
call*; and into a `fn(x, y)` callback slot, `fn(x, y)` / `fn(x, ...)` / `fn(...)`
are accepted while exact `fn(x)` and any 3+-param function are rejected. Plus,
on async: `async fn () -> number` renders as `fn () -> Promise<number>`;
`await p` where `p: Promise<string>` yields `string`; `await p` where
`p: Promise<Promise<number>>` yields `Promise<number>` (no auto-flatten —
that's M9's `Awaited<T>`).

**Function overloading.** Escalier supports overloaded `fn` declarations and
this milestone is where they land for free functions. Overloading is a poor fit
for SimpleSub's "one principal type per expression" model — an intersection of
arrow types isn't part of the inferable fragment, and subtyping makes "which
overload applies" genuinely ambiguous. The recommended approach for this
checker:

- **Infer each overload body individually, then merge.** What we must *not* do
  is inject the disjunction into the lattice — there is no SimpleSub type for
  "either this arrow or that arrow." But each overload's body is just a normal
  `fn` with its own principal type, so we can infer them independently and
  bundle the resulting schemes into an overload set as side-channel metadata.
  The overloaded symbol's "type" is then the set of declared/inferred branches,
  not a single SimpleSub type. Full up-front annotation isn't required at the
  top level or inside non-recursive `let`-bindings.
- **Resolve at the call site, as a separate phase from `constrain`.** At each
  call, collect the argument types' bounds, then pick a single overload; emit
  constraints only for the chosen branch. Don't try to encode the disjunction
  as constraints — that's how speculative pinning sneaks in.
- **Require arguments to be "ground enough" before picking.** If an argument is
  still a fully unconstrained variable, either defer the call (preferred — let
  more bounds accumulate) or fall back to declaration-order first-match. Picking
  on a guess and backtracking later is what we're avoiding.
- **Define one specificity ordering and document it.** TypeScript's
  declaration-order + best-match rule is a reasonable starting point; whatever
  we pick has to interact cleanly with subtyping (multiple overloads can be
  applicable to the same call) and with the exact/inexact distinction from this
  milestone — overload selection on object args in M4 will be sensitive to it,
  and we want one rule, not two.
- **Mutual recursion forces annotations.** The spike's `LetRecGroup` pattern
  gives each binding one fresh var, checks bodies against those vars, and
  generalizes. That doesn't work when the binding is an overload set: a call
  site inside the group needs to pick a branch to know what to constrain, but
  the choice depends on the inferred types of the other group members, which
  depend on which branch was picked at *their* call sites into the overloaded
  function. The cycle is real — not just an ordering issue — and fixed-point
  iteration over overload choices isn't guaranteed to converge under subtyping.
  Rule: **if an overloaded function participates in a mutually recursive group,
  its overload signatures must be annotated** (bodies still get checked against
  them like any annotated `fn`; only the set itself has to be ground before the
  group starts). Self-recursion is softer — each body can be inferred with the
  *other* overload signatures visible, since the recursive call has to land in
  one declared branch — but for mutual recursion across multiple overloaded
  participants, require the annotations.

---

## M4 — Core value types: records + usage-based inference + `mut` + **lifetimes** + destructuring/`match`

**Status: landed** (PRs #728–#765; see
[m4-implementation-plan.md](m4-implementation-plan.md) §"Dependency graph" — all
phases A–G complete). The `ObjectType`/`TupleType` exactness flag and subtyping
rules, usage-based inference with the `open` marker, `var` literal widening, the
unified `RefType{mut, lt, inner}` borrow wrapper and its constrain rule,
field-write mutability inference, the lifetime sort with origination/escape/elision,
destructuring patterns and the `match` expression, namespace member lookup, and the
ported mutability-transition checks all shipped.

The big one. These are inseparable: lifetimes ride on borrows, records are the
first value type that can be borrowed, and `mut` borrows (via the `Ref`
wrapper) are what first populate a lifetime. Land them together.

- **Records/objects** with the unified `Ref{mut, lt, inner}` wrapper for
  borrows from the start (per [02-design-notes.md](02-design-notes.md)
  §"`soltype` — the type representation"). Owned `Record`/`Tuple`/`Alias`/
  `Class` have no lifetime field; lifetimes live on `Ref` wrappers around
  the borrowed value. Both mutable and immutable borrows use the same wrapper,
  distinguished by `Ref.mut`; the lifetime is nilable, so the wrapper covers
  owned-mutable values (`mut Point` returned fresh) and borrows (`'a Point`,
  `mut 'a Point`) uniformly.
- **Exactness flag on records and tuples, from the start.** `Record`/`Tuple`
  carry an `exact` flag (default exact; `...` ⇒ inexact). Subtyping honors the
  one-way rule: exact `<:` inexact but not the reverse; exact `<:` exact requires
  the *same* member set (no width subtyping); inexact `<:` inexact is the
  current structural width subtyping. Object/tuple **literals infer as exact**.
  This is the spike's lifetime lesson applied again — a property carried with the
  former is cheap now, painful to retrofit; the spike today is uniformly inexact,
  so this is additive `constrain` cases plus a flag, not a rework.
  - **Differentiate member-access selection from concrete record subtyping.** M2
    routes both through one width-tolerant `RecordType <: RecordType` arm
    ([solver/constrain.go](../../internal/solver/constrain.go) record case),
    because its only consumer is member access — `recv.foo` lowers to
    `constrain(recv, {foo: β})`, a "has-field" *requirement* that is inherently
    width-tolerant (the receiver legitimately carries more fields). M4 must split
    these: the field-selection requirement stays width-tolerant (or becomes its
    own constraint form), while concrete record `<:` record (for record-typed
    params/annotations, which M2 lacks) becomes **exact** — same field set, no
    width — matching the tuple same-length and function same-arity arms. Until
    that split, the M2 record arm's width subtyping is a member-access mechanism,
    not the settled record-subtyping semantics.
- **Usage-based inference**: member access `obj.bar` ⇒ `constrain(obj <: {bar:
  β})`; field requirements accumulate as upper bounds and coalesce (negative
  position) to a record. This is what replaces `Open`/`Widenable`/
  `ArrayConstraint`. (Spike M2.) The usage-collected shape **coalesces as
  exact** by default (the exact-by-default rule — see
  [02-design-notes.md](02-design-notes.md) §"Exactness"): the row is closed once
  body inference completes. Row
  polymorphism is opt-in via an `open` parameter marker (keyword provisional)
  — `fn dist(open p) => ...` keeps `p` inexact so callers can pass records
  richer than the field set the body touches. The `open` marker lands here
  (the first milestone where record-typed params exist).
- **`var`-binding literal widening (reassignment ergonomics).** M3's PR8 ships
  reassignment (`a = expr`) but constrains the source against the binding's type *as
  inferred*, so an un-annotated `var a = 5; a = 6` rejects (`6 <: 5`) until a `var`
  binding **widens its literal initializer to the primitive** (`5` ⇒ `number`,
  `"x"` ⇒ `string`) — the binding-level analog of the `widen(v)` already applied to
  field writes (`obj.x = v`, above). It is *not* PR1 generalization (that quantifies
  free vars and is a no-op on a var-free literal). The principled M4 form is
  usage-based: a `var`'s type is informed by **all** its assignment sites
  (initializer + reassignments) via the same widen + coalescing machinery, with
  `val` left un-widened (a fixed literal singleton). Defaulting to always-widen-`var`
  is the simple rule; the open question this milestone settles is whether a later
  narrowing use should be allowed to reclaim the literal.
- **`Ref` constrain rule** (single rule for the unified borrow wrapper): inner
  variance is bidirectional iff `r.mut` (read/write decomposition: covariant
  read + contravariant write when the target writes), covariant otherwise. The
  write view is **per named field**, ranging over the target's fields only, so an
  inexact mutable target pins its named fields invariantly but stays width-tolerant
  (`mut {x, y} <: mut {x, ...}` holds) — which is what lets a field write's inexact
  `mut {field, ...}` requirement apply to a concretely-typed receiver. Lifetime is
  covariant when both sides have one; mutability decay (`Ref{mut: true} <: Ref{mut:
  false}`) is allowed, the reverse rejects.
- **Mutability inference from field writes** (M4): `obj.x = v` constrains the
  receiver against the inexact requirement `Ref{mut: true, lt: nil, inner:
  Record{x: widen(v)}}`, with literal widening and merging of a receiver's
  selections into one `mut` object. The lifetime is `nil`, so M4 supports only
  owned-mutable receivers. (Spike M3 + extension.) Read-after-write returns the
  stored value, but its cache is keyed on a type-variable receiver, so a concrete
  borrow receiver re-derives from its annotation instead — a divergence observable
  only with M6 unions, tracked in issue #742.
- **Lifetime origination on field writes** (D2, **not** M4): the write
  requirement's lifetime becomes a fresh variable rather than `nil`, so the
  constraint also accepts a mut-borrowed receiver of any lifetime. This depends on
  the lifetime sort and borrow origination, which land in D2 — until then a borrowed
  receiver is not yet supported.
- **Lifetimes as a second sort**: `LifetimeVar` with lower/upper bounds over the
  outlives lattice (`'static` = bottom), `constrainLt`, lifetime coalescing +
  elision (a parameter-only lifetime that connects nothing is dropped). Borrows
  originate at parameters typed as `Ref` (mut or immut); returning shares by
  value identity; multi-source returns union lifetimes; escape constrains `<:
  'static`. (Spike M4.) A borrow flowing into an owned slot is rejected as an
  escape, but a member *read* must look through the borrow first; the read-side
  peel misses a variable bound to a borrow, so such a read can fire a spurious
  escape — tracked in issue #741, not reachable until D3.
- **Destructuring patterns + the `match` expression form.** `IdentPat` (M1, the
  only `Pat` concrete through M2–M3) is joined here by the structural concretes —
  `TuplePat`, `ObjectPat`, and literal patterns — now that tuple/record types
  exist to type them. The *same* `Pat` machinery powers both **binding
  destructuring** (`val {x, y} = p`, `val [a, b] = t`, and the identical forms in
  function params) and **`match` arms**: a pattern dispatches through the usage /
  member-lookup path (`obj.bar` ⇒ `constrain(obj <: {bar: β})`), **not**
  subtyping — the rule M5 restates for class/enum patterns. M4 owns introducing
  the `match` *expression* over these structural patterns; exhaustiveness for a
  closed scrutinee follows the exactness flag (an exact record/tuple pattern set
  is complete; an inexact one requires a catch-all arm). **Constructor/enum
  patterns and enum-exhaustive `match` are M5; union `match` exhaustiveness is
  M6** — M4 lays the form and the structural-pattern machinery they both extend.
- **Namespace member lookup (`Foo.bar`, `Foo["x"]`).** Lands here, co-located
  with object member access, because it's the same operation against a different
  container. M2 introduced the `Namespace` *structure* and made a free-floating
  namespace ident an error; M4 adds the *access*. A single `resolvePath` resolves
  an ident/member/index chain to `Value | Namespace` (a name is never both — a
  scope invariant); the **object/index position tolerates a namespace, every
  other (value) position rejects one** — so the `NamespaceUsedAsValueError` moves
  off `inferIdent` to the value-position consumer and fires once, covering both
  `f(Foo)` and partial chains `f(A.B)`. Namespace lookup is a **direct,
  non-lexical** read of the namespace's own `Values`/`Nested` maps
  (`LookupValue`/`LookupNamespace`) — unlike `Scope.Get*`, no parent walk, just
  like object member access. Index keys into a namespace must be **statically
  constant** (string/symbol — for members whose names aren't valid identifiers),
  since membership is resolved by name, not dynamically. New errors:
  `UnknownNamespaceMemberError`, `DynamicNamespaceIndexError`. (Producing the
  namespace in the first place — cross-package import resolution — is a separate
  concern from this lookup logic.)

**Accept:** the canonical lifetime cases against real source — `IdentityRefReturn`
⇒ `fn <'a>(p: mut 'a {x: number}) -> mut 'a {x: number}`; `FreshObjectReturn`
(no lifetime); `ConditionalUnionReturn` ⇒ `mut ('a | 'b) {x: number}`;
`EscapingRefIntoStatic` ⇒ `mut 'static`; property-level and tuple-per-slot
lifetimes; read-after-write field collapse. (Spike M2/M3/M4 + lifetime extensions.)
Plus exactness: an exact `{x, y}` is assignable to inexact `{x, y, ...}` but not
the reverse; an extra property on an exact target is rejected; `Ref` neither
tightens nor loosens the inner's exactness (the inner carrier's `exact` flag
passes through, per [exact-types/requirements.md](../exact-types/requirements.md)
§7.11 — orthogonal to `Ref`'s mut/lifetime axes).
Plus patterns: `val {x, y} = p` and `val [a, b] = t` bind each name at the
member's type (and reject a missing field / wrong arity); a destructured param
`fn (({x, y})) { … }` types the same way; a `match` over structural patterns
binds and type-checks each arm, and an exact-record/tuple scrutinee with a
complete pattern set needs no catch-all while an inexact one does.
Plus namespaces: `Foo.bar` resolves to the member's type and `Foo["weird-name"]`
to a constant-keyed member, while `f(Foo)` and `f(A.B)` are rejected as
`NamespaceUsedAsValueError` and a dynamic `Foo[k]` as `DynamicNamespaceIndexError`.

**Mutability-transition checking reuses existing infrastructure.** Escalier's
flow-sensitive analysis that permits mutable↔immutable alias creation in
specific situations
([internal/checker/check_transitions.go](../../internal/checker/check_transitions.go),
~689 LoC) and the supporting [internal/liveness/](../../internal/liveness/)
package are structurally orthogonal to type inference and can be reused with
minimal adaptation:

- **`internal/liveness/` ports verbatim.** Its `VarID` / `CFG` /
  `AliasTracker` / `LivenessInfo` types operate on name-resolved AST and have
  no `type_system` references. Drop in unchanged.
- **Two narrow predicate ports.** `isValueType(t)` and `isMutableType(t)` in
  [check_transitions.go:189-217](../../internal/checker/check_transitions.go#L189-L217)
  are reimplemented over `soltype.Type` — a few lines each. `isMutableType`
  becomes `if r, ok := t.(*soltype.Ref); ok { return r.mut }; return false`
  (the unified `Ref` wrapper carries the `mut` flag, per
  [02-design-notes.md](02-design-notes.md) §"`soltype` — the type
  representation").
- **Rule logic is unchanged.** `checkMutabilityTransition` talks only to
  `liveness.Liveness` and `liveness.AliasTracker`; the Rule 1 / Rule 2 / Rule
  3 logic stays as-is.
- **`solver.Context` gains the same `Liveness` / `Aliases` fields**, populated
  by the existing `liveness_prepass.go` (also reusable — operates on the AST,
  not the checker's types).
- **Simplification: the `HasStaticMutAlias` / `HasStaticImmAlias` escape
  hatches collapse.** Those bits exist today to handle "value escapes to a
  callee with a `'static` parameter" because the live-alias check can't see
  the consumer through that boundary. Under the new checker, the escape is
  first-class — the lifetime constraint `'l <: 'static` is part of the
  inference output, so the transition checker queries the lifetime sort
  directly instead of maintaining a parallel "static escape" bit on each
  alias set. This is one place where porting is a simplification, not just a
  translation.

**Gate (HIGHEST RISK):** the `Ref` rule's `mut`-driven inner invariance (via
read/write decomposition) is the one thing that could still surprise at
production scale. If it cannot be encoded cleanly against the real AST, the
whole migration is in question — this is the gate to clear before investing
further.

**Deferred out of M4** (tracked at their target milestones, not lost):

- **Object-argument overload specificity (#723)** — records make the documented
  first-match fallback observable; the principled field-subsumption ranking lands
  in **M5**. M4 only pins the cases with `#723`-tagged tests.
- **Function-arm Variation-B gap** — unchecked extra positions against an inexact
  callback need the `_ <: unknown` (⊤) rule, which lands in **M6**. M4 excludes
  function annotations and fails the branch loud, keeping the path closed.
- **Rest-param element checking (#677)** — `...xs: T[]` element checking needs
  `Array<T>`, so it lands in **M7**. M4 stays arity-only and marks the
  `FuncParam.Rest` note "M7."

**Open design question — per-property lifetimes on alias-typed params (needs type
annotations first).** Per-property and tuple-per-slot lifetimes have a syntactic
home when the parameter's type spells out its structure inline, so each leaf can
name its own lifetime:

```
fn getPoints(line: {p: 'a Point, q: 'b Point}) -> ['a Point, 'b Point] {
  return [line.p, line.q]
}
```

A type alias hides that structure, leaving nowhere to write the per-leaf
lifetimes:

```
type Line = {p: Point, q: Point}
fn getPoints(line: Line) -> [Point, Point] { // no way to name line.p / line.q's lifetimes
  return [line.p, line.q]
}
```

One candidate is a path-based lifetime annotation that names a lifetime by the
access path into the aliased parameter, so the return slots borrow from `line.p`
and `line.q` directly:

```
fn getPoints(line: Line) -> ['line.p Point, 'line.q Point]
```

This is unsettled and needs its own design pass. The surface syntax, how a path
lifetime resolves against the alias body, and how it interacts with elision and
escape are all open. It also depends on type annotations being implemented first,
since there is no annotation surface to attach these to until then. Out of scope
for M4 — capture the design before any milestone commits to it.

---

## M4.5 — Script inference

A **script** is a source file whose top-level statements run in source order with
function-body semantics, the `bin/` counterpart to a library module. A module's
top-level declarations are dependency-ordered and mutually visible; a script's are a
linear body, so liveness, alias tracking, and the `mut` transition rules apply at the
top level exactly as they do inside a function.

The new solver has `InferModule`
([module.go](../../internal/solver/module.go)), which walks declarations in
dependency-graph SCC order, but no `InferScript`. This milestone adds the script entry
point. It is short because M4 already shipped every building block: the liveness
pre-pass (`runLivenessPrePass`), the per-body `funcCtx`, alias tracking, the transition
checker, and lifetime origination/escape. The only new code is the entry point that
runs them over a script body — no new inference.

- **Mirror the old checker's `InferScript`.** `internal/checker/infer_script.go` is the
  template: wrap the script's statements in an `ast.Block`, run `runLivenessPrePass`
  over that block under a fresh `funcCtx`, then walk the statements in source order.
  This is the seam the new solver's `transitions.go` package doc already records — at
  module top level `c.fn` is nil so every transition/liveness entry point is a no-op,
  which is correct for a dependency-ordered module but silently skips a script until it
  is given the same per-body context a function gets.
- **Linear body, not SCC order.** A script is one straight-line body, so there is no
  dependency-graph driver and no generalization at an SCC boundary. Top-level bindings
  behave like locals. A borrow stored into a later top-level binding is a local move; a
  borrow escapes to `'static` only when written into actual module-level or global
  state, the same escape rule D3 applies inside a function.
- **Testable without the fixture harness.** `InferScript` is exercised directly by the
  single-file table harness, the same `inferSource`-style driver M4 already uses, so
  script-level lifetime and transition coverage lands as soon as this entry point
  exists. It does not wait on M8's second fixture harness.

**Why it lands here.** Numbered `.5` to slot between M4 and M5 without renumbering. It
rides immediately after M4 because it consumes M4's lifetime/transition machinery
directly and adds no new inference — it is only the entry point that lets that
machinery run over a script's linear top-level body. It is independent of M5 and later,
which add new type shapes rather than new program forms.

**Accept:** a script of top-level statements, with no enclosing function, infers in
source order; a `mut`→immutable transition and a borrow lifetime at script top level
are checked identically to the same statements wrapped in a function body. For example
a top-level `val items: mut {x: number} = {x: 1}` aliased to an immutable binding and
then used mutably reports the Rule 1 transition error, matching the function-wrapped
form. Parity with the old checker's `InferScript` on the transition and lifetime cases.

---

## M5 — Nominal types (classes)

Escalier's `class` declarations introduce **nominal** types: a value of class
`Point` is not assignable to an **exact** structural `{x: number}`, nor is a
structural object ever assignable to `Point`, even when the fields line up. A
class instance *does* satisfy an **inexact** object target structurally; the
target-dispatched rule below makes the target's exactness the deciding factor.
SimpleSub is fundamentally structural, so nominal
types are layered on as atomic lattice elements with an explicit
**declared-subtype graph** feeding `constrain` — the design sketched in
[`03-references.md`](03-references.md). Lifetimes and `mut` ride on classes
exactly as they do on records (introduced in M4), so this milestone reuses the
M4 substrate without retrofitting.

- A `Class` SimpleType `{name, args, lt, final}` plus the structural member view
  it projects. Against a **class target**, subtyping is nominal and never looks at
  members structurally. Against a **structural object target**, the class projects
  its members and the structural width rule applies — target-dispatched, per the
  M4 plan's design-revision note. Member *lookup* (`p.x`, `p.method()`) resolves
  through the declared body either way, a separate path from subtyping.
- **Class-instance exactness comes from `final`**
  ([exact-types/requirements.md](../exact-types/requirements.md) §2.6). A class instance
  type is **inexact by default** (subclasses may add members, so it behaves like
  an open object); a class declared `final` cannot be subclassed, so its instance
  type — and `keyof` of it — is **exact**. Enum variants are implicitly `final`,
  which is what lets exhaustive `match` over an enum need no default arm. This is
  the nominal-type instance of the same exactness flag: `final` ⇒ exact instance,
  non-`final` ⇒ inexact.
- **Nominal subtyping rule.** `Class<A, args_A> <: Class<B, args_B>` succeeds
  iff (a) `A == B` (per-position check on args, with variance per parameter —
  see below), or (b) `A extends B` (transitively) in the declared-subtype graph
  built from each class's `Extends`/`Implements`. A `Class` against a structural
  object target dispatches on the target's exactness: an **exact** object target
  rejects, since a `Point` is not an exact `{x: number}`, while an **inexact**
  `{x: number, ...}` target **admits** a structurally-conforming class instance
  via the structural width rule. The reverse — a structural object against a
  `Class` target — always rejects: a `{x: number}` is not a `Point`.
- **`match` extends to nominal patterns; destructuring stays separate from
  assignability.** M5 adds **enum/class constructor patterns** to the `match`
  expression introduced in M4 (structural patterns there), plus
  **enum-exhaustive `match`** — enum variants are implicitly `final` (above), so
  a `match` covering every variant needs no default arm. A record pattern like
  `let {x, y} = point` (and the equivalent `match` arm) still succeeds against a
  `Point` because patterns dispatch through member lookup, **not** subtyping —
  the same path that resolves `p.x`. The assignment forms
  `var foo: {x: number, y: number} = Point(5, 10)` (an exact target) and
  `var bar: Point = {x: 5, y: 10}` both remain rejected. The inexact form
  `var foo: {x: number, y: number, ...} = Point(5, 10)` is **accepted** under the
  target-dispatched rule above: an inexact object target admits a
  structurally-conforming class instance.
- **Structural-object union exhaustiveness — the home for the case M6 left
  open.** M6's union `match` leg checks only literal union members. For an exact
  union of structural objects such as `{x: number} | {y: string}` it cannot yet
  decide whether an object pattern covers a member, so it reports each unguarded
  structural arm as an unsupported feature rather than guess. M5 replaces that
  report with real coverage. An object pattern covers a union member when that
  member carries every field the pattern names, so the arms are exhaustive when
  they collectively cover each member. This is the third coverage case after
  literal members in M6 and nominal variants in M5's enum leg. It extends the
  member loop in `unionMatchExhaustive` and removes `reportUnsupportedUnionArms`
  ([internal/solver/infer_expr.go](../../internal/solver/infer_expr.go)).
- **Per-type-parameter variance via polarity (Option 2).** Each class's type
  parameters get their variance inferred from how they appear in the class body,
  exactly as SimpleSub already does for inference variables. A parameter that
  appears only in output positions (field types, method returns) is covariant;
  only in input positions (method parameters, write-only fields), contravariant;
  in both, invariant; in neither, bivariant (phantom). The subtyping rule then
  dispatches per parameter: covariant → `arg <: arg'`, contravariant →
  `arg' <: arg`, invariant → both, bivariant → no constraint emitted (the
  parameter doesn't appear in the body, so its argument can't affect any
  subtyping question). Use-site wildcards are explicitly **not** used. Declaration-site **modifiers `in`/`out`/`in out`** are supported,
  mirroring TypeScript (4.7+): bare `<T>` ⇒ variance inferred; an annotated
  parameter is **checked** against its inferred variance and rejected on
  mismatch. Required for `.d.ts` interop; doubles as load-bearing documentation
  in Escalier sources. Variance is stored on the `Class` decl as a
  `Variance` per parameter (`Covariant | Contravariant | Invariant |
  Bivariant`), frozen at class-decl time.
- **Generic type aliases do *not* carry variance separately.** A non-recursive
  alias like `type Box<T> = {value: T}` is transparent: `Box<A> <: Box<B>`
  reduces to the structural subtyping of its expansion, so variance falls out
  for free and storing it would be redundant. Recursive aliases (handled in M9
  via the cycle cache) are the wrinkle — at the cycle-cache hit point the rule
  must dispatch without expanding, so variance is inferred internally for use
  there, but is never user-annotated. `in`/`out` modifiers are therefore
  allowed only on classes/interfaces, not on `type` declarations (matching TS).
- **Iteration protocol for `for (x in xs)` and `for await (x in xs)`.** Both
  loop forms desugar to a protocol check rather than a structural rule:
  - Sync: `xs <: Iterable<T>` for some `T`; the loop variable's type is `T`.
    `Iterable<T>` is the stdlib type defining `[Symbol.iterator](): Iterator<T>`
    (plus `Iterator<T>` itself with `.next() -> IteratorResult<T>`).
  - Async (`for await`): `xs <: AsyncIterable<T>`, similar shape with
    promise-wrapped results. Only legal inside an `async fn` (the AST walk
    enforces this).
  The constraint is just the standard `xs <: Iterable<T>` subtype check with
  `T` minted fresh — the protocol resolution is one method-dispatch step
  through the M5 nominal machinery (same path as `p.x` on a class instance).
  No new constraint machinery needed; this is purely "wire the loop syntax to
  the existing dispatch path." Depends on `Iterable<T>` / `Iterator<T>` /
  `AsyncIterable<T>` / `IteratorResult<T>` being available from the stdlib
  (M2 placeholder; real resolution lands in M7).
- **`mut` and lifetimes ride on it free.** `Class` is borrowed the same way
  records and tuples are — wrapped in `Ref{mut, lt, inner: Class{...}}`. The
  M4 lifetime machinery applies unchanged (`mut 'a Point` is `Ref{mut: true,
  lt: 'a, Class{Point}}`, structurally identical in shape to `Ref{mut: true,
  lt: 'a, Record{x: number}}`). The `Ref` rule's `mut`-driven inner
  invariance composes with per-parameter variance: when `r.mut` triggers
  the bidirectional inner constraint, both directions fire on the `Class`,
  which cascades to forcing both directions per arg — invariance in `T`
  regardless of `T`'s declared variance.
- **Mutually recursive classes** infer via the same "fresh var per binding +
  constrain + generalize" pattern proven in the spike for recursive functions
  (`LetRec`/`LetRecGroup`) — no placeholder phase or `typeRefsToUpdate` patching.

**Accept:** the four variance lines that pin down Option 2 against `mut`, given

```text
class Box<T> {
  val: T              // T appears only in output position ⇒ covariant
  fn get(self) -> T { self.val }
}

class Consumer<T> {
  fn accept(self, x: T) -> unit { ... }   // T only in input position ⇒ contravariant
}
```

```text
Box<number> <: Box<number | string>                ✓  (T covariant in Box's body)
mut Box<number> <: mut Box<number | string>        ✗  (Mut forces invariance over the top)
Consumer<number> <: Consumer<number | string>      ✗  (T contravariant in Consumer's body)
mut Consumer<number | string> <: mut Consumer<number>  ✗  (Mut over contravariant: still invariant)
```

Plus: a bare `{x: number}` is rejected against `Point` (and vice versa);
`class B extends A` yields `B <: A` via the declared graph and method dispatch
finds A's methods when not overridden; mutually recursive class declarations
infer cleanly. Plus exactness: a `final class Point` instance is exact (rejects
extra members, `keyof` is an exact union); a non-`final` class instance is
inexact. Plus iteration: `for (x in numbers)` where `numbers: Array<number>`
binds `x: number`; `for (x in 5)` is rejected (number doesn't implement
`Iterable`); `for await (x in stream)` outside an `async fn` is rejected by
the AST walk; `for await` over a sync iterable is rejected by the type rule.

**Method overloading.** Methods reuse M3's overload-resolution machinery
(no-inference, separate-phase, ground-enough, single specificity rule) with
two method-specific wrinkles:

- **Receiver-dependent dispatch.** Method overload selection is a function of
  the receiver type as well as the arguments. Under SimpleSub the receiver at
  a call site may be a variable with only lower/upper bounds — overload
  resolution can't peek past those without forcing/widening the receiver,
  which loses precision. Defer resolution until the receiver's bounds are
  collected; if it remains a free variable, fall back to declaration-order on
  the receiver's declared class (which we know nominally).
- **Method lookup already runs through the class body, not `constrain`.**
  Member resolution for `p.method()` is the separate path noted above (the
  same one that resolves `p.x`). Plug overload selection into *that* path —
  it has the declared class in hand and never has to invent an arrow type for
  subtyping to chew on.
- **Object-argument overload specificity (#723; deferred from M4).** M4's record
  types made the documented first-match fallback observable on object-typed
  arguments: `moreSpecific`/`structuralSubtype` (overload.go) ranks record-shaped
  args as a tie, so resolution collapses to declaration order. M5 owns the fix —
  classes are where multi-arm object overloads proliferate — ranking record/class
  args by field-set subsumption and exactness (a superset-of-fields or exact arg
  dominates), the object analogue of M3's arity/exactness ranking. M4 only pins
  the observable cases with `#723`-tagged tests so any later arm-choice change is
  intentional.

See M3 for the full set of recommendations; everything there applies to
methods unchanged.

**Scope note.** The *subtyping rule* is short (a few cases in `constrain` plus
a small declared-subtype graph). The bulk of the class machinery — constructor
handling, static vs. instance partitioning, method overload merging, `Self`
type substitution, the type-vs-value dual binding — is language semantics, not
unification, and is roughly proportional to the surface regardless of the
inference core. That work stays. What SimpleSub does avoid is the placeholder /
`typeRefsToUpdate` patching the production checker needs for cross-class
recursive references (cf. `infer_module.go:431-872` and the discussion in
[02-design-notes.md](02-design-notes.md)).

---

## M6 — Unions / intersections

- Union/intersection as both inferred **output** (from bounds, polarity
  coalescing) and written **annotation input**, with the directional lattice
  rules in `constrain` (the "for all" rules before the "exists" rules; the
  "exists" rules defer to the variable case against a variable to avoid
  speculative pinning). (Spike M2 output + M6 annotations.)
  - **"For all" rules — universal, deterministic, no choice:**
    - `(A | B) <: C`  ⟹  `A <: C` *and* `B <: C` (every member of the union
      must hold).
    - `A <: (B & C)`  ⟹  `A <: B` *and* `A <: C` (every component of the
      intersection must hold).

    Safe to fire eagerly — they just produce more sub-constraints.
  - **"Exists" rules — existential, require a choice:**
    - `A <: (B | C)`  ⟹  `A <: B` *or* `A <: C`.
    - `(A & B) <: C`  ⟹  `A <: C` *or* `B <: C`.

    Committing prematurely over-constrains, so these fire only after the
    "for all" rules have done all the deterministic decomposition they can.
  - **Variable deferral.** When an "exists" rule has a type variable on one
    side, don't pick a branch — record the whole union/intersection in the
    variable's bounds and let coalescing resolve it later. Example:
    `α <: (number | string)` becomes "add `number | string` to α's upper
    bounds," not "guess α := number." This is what "defer to the variable
    case against a variable" means; the alternative is **speculative pinning**
    (locking α to a branch on a guess that may be wrong). The overall shape
    mirrors SAT/SMT unit-propagation-before-branching: do all the forced work
    first, and when forced to branch, keep the decision symbolic in a
    variable's bounds.
- **Union exactness flag — completes `match` exhaustiveness.** A bare `A | B` is
  an **exact** (closed) union — its
  inhabitants are exactly `A ∪ B`; `A | B | ...` is inexact (at least these, with
  an `unknown`-typed tail). Exact `<:` inexact, not the reverse. This is the
  third and last leg of the `match` story (structural M4, enum M5, union here):
  a `match` over an exact union is exhaustive with
  no default arm; over an inexact union a default is required. (The exhaustiveness
  payoff is the main motivation, per
  [exact-types/requirements.md](../exact-types/requirements.md) §5.) `keyof` of an exact object and the
  element-union of an exact tuple are exact unions; their inexact counterparts
  are inexact — so this flag must be threaded by coalescing, not just stored.
- **Former simplification — lattice identities and the `ErrorType` sentinel.** When
  unions/intersections become real formers here, normalize them: drop duplicate and
  subsumed members, and apply the lattice identities — `never` (⊥) drops from a
  union (`A | never ⇒ A`), `unknown` (⊤) drops from an intersection (`A & unknown ⇒
  A`). M3's `ErrorType` recovery sentinel ([m3 PR8](m3-implementation-plan.md)) is
  the special case: it is elided from **both** a union and an intersection unless it
  is the sole member (`A | error ⇒ A`, `A & error ⇒ A`; bare `error` stays) — it is
  the join *and* meet identity, the former-level reflection of its absorbing
  behavior in `constrain`, because it carries no information once the error is
  reported. (In M3 this is an invariant freebie — `ErrorType` is short-circuited out
  of every bound list, so it never reaches a former; M6 must keep it true once
  formers can be built directly.)
- **Canonical union/intersection member order — closes the `equalType` order gap.**
  `equalType` compares `UnionType`/`IntersectionType` members positionally
  (`equalTypeSlice`), so two unions over the same members in a different order
  compare unequal even though both formers are commutative — a latent dedup gap.
  It is masked today only because pre-M6 unions arise from coalescing **output** in
  a deterministic construction order. Once unions are real formers built from
  annotations and constraint inputs, that determinism is gone, so member order must
  be canonicalized at construction. This needs a stable total order over arbitrary
  types — there is no cheap key as there is for the lifetime sort — so fold it into
  the "Former simplification" normalization above, running dedup, lattice
  identities, and canonical ordering as one pass. The lifetime-sort twin —
  `ltEqual` over `LifetimeUnion` — is closed earlier and more cheaply in M4 D4 by
  sorting members on `LifetimeVar.ID` (see m4-implementation-plan.md D4, "canonical
  union member order").
- **The `_ <: unknown` (⊤) subtyping rule — closes M4's Variation-B gap
  (deferred from M4).** With `unknown` a real former here, add the rule that makes
  it the top of the lattice (everything is a subtype of `unknown`). M4 left a
  KNOWN GAP in `constrain`'s function arm: an inexact callback that may supply
  extra arguments needs an `unknown`-against-param check at those positions
  (exact-types §4.2.1.2 "Variation B"), which this rule supplies. M4 keeps the
  path closed — A3 adds no function annotations and the extra-position branch
  fails loud via `reportUnsupportedFeature` — so the gap is unreachable until this
  rule lands and that guard is removed.
- **Permissive mut-borrow joins — degrade an incompatible join to a
  read-until-narrowed union, matching TypeScript.** M4 D3's `joinBorrows` rejects a
  return set of mutable borrows whose shared fields disagree on TYPE. Joining
  `mut {x: number}` and `mut {x: string}` pins `x` invariant in both directions and
  reports `number <: string` / `string <: number`, asserted by
  `TestInferIncompatibleBorrowJoinErrors`. That error is the conservative default M4
  could represent. The intended M6 behavior is more permissive: produce the
  type-level union `(mut 'a {x: number}) | (mut 'b {x: string})` instead. That union
  is readable — `.x` yields `number | string` through the covariant read view — and
  a write to a conflicting field through the un-narrowed binding is rejected and leaves
  its type unchanged. To write, narrow to one branch with an `if let` and write through
  the fresh mutable binding. This is TypeScript's read-until-narrowed rule, adapted to
  Escalier's binding-based narrowing. The un-narrowed union is read-only at its
  conflicting fields, and the write goes through the narrowed binding, never by
  re-typing the original.
  - **Why it lands here, not in M4.** It needs two M6 pieces. First, union types as
    first-class OUTPUTS that can carry `mut` `RefType` members — M4's join only
    builds a single-carrier lifetime-union `mut ('a | 'b) {x}`, never a union of two
    distinct borrows. Second, the `if let` narrowing form that binds a fresh mutable
    view of one branch to write through. Until the first exists the union is
    unrepresentable; until the second, it stays read-only.
  - **The write path is an `if let` binding.** These branches carry no discriminant
    tag, but the narrowed type is named in the pattern, so
    `if let r2: mut {x: number} = r` binds a fresh mutable view and `r2.x = 5` writes
    through it. This is ordinary binding-based narrowing. Escalier has no runtime-type
    flow narrowing, and the un-narrowed `r` keeps its union type.
  - **Scope.** This relaxes D3's all-mut-borrows reconcile-or-error contract to
    reconcile-or-union. The compatible case still joins to one carrier with a union
    lifetime as today; only the incompatible case changes from error to union.
    Revisit the D3 join policy once M6 union outputs and narrowing land.

**Accept:** `number | string` annotation accepts `number`/rejects `boolean`;
intersection annotation satisfied by a value at both member types; both
round-trip through the printer; inferred unions from multi-branch returns. Plus:
an exact union `"a" | "b"` is assignable to inexact `"a" | "b" | ...` but not the
reverse; an exact-union `match` covering all members needs no default, an
inexact-union `match` does.

---

## M6.5 — Lifetime bounds

See [m6.5-implementation-plan.md](m6.5-implementation-plan.md) for the routes
considered, the chosen route, and the sequencing rationale.

A **lifetime bound** is a declared or rendered outlives relation between two named
lifetimes in a signature — Rust's `'a: 'b`, read "`'a` outlives `'b`". Escalier's
lifetimes are unrelated once named today; a bound lets a signature state how two of
them relate. The constraint solver already builds the outlives graph —
`constrainLt` records the edges in `LifetimeVar.LowerBounds`/`UpperBounds`, and M4
D2.5 generalizes them per instantiation — so this milestone is about surfacing,
declaring, and checking relations the solver already computes, not new inference.

- **Bounds live in the lifetime and type-param list, not a `where` clause.** A
  lifetime bound is attached in the `<…>` list as `<'a: 'b>`, mirroring how a
  type-param bound is written `<T: U>`. One quantifier list carries both sorts and
  their bounds, which keeps the surface consistent between type-param and lifetime
  bounds and avoids a second bound-declaration grammar.
- **Render inferred bounds in the param list.** M4 D4 collapses a multi-source join
  to the union `('a | 'b)`, the conservative stand-in for "one of these". The graph
  underneath already carries `'a: 'c, 'b: 'c` for the join `'c`, so the precise form
  `fn <'a: 'c, 'b: 'c, 'c>(…) -> mut 'c {…}` is a display upgrade, not an inference
  change. Naming kept join lifetimes and emitting their `'a: 'b` bounds in the
  quantifier list `PrintAsSchemeWith` already builds are the new rendering pieces.
- **Declare bounds at no-body sites.** A site with no body to infer from must declare
  its bounds — external and library signatures, abstract interface methods, and type
  aliases over borrows. Add in-list bound syntax `<'a, 'b: 'a>` to the AST and parser
  and lower it into `constrainLt` during signature resolution, so a declared bound
  participates in solving exactly like an inferred one.
- **Check inferred against declared.** An annotated function's inferred bound set must
  satisfy its declared one — subsumption over the lifetime sort, built on
  `constrainLt`.
- **Chosen route — a unified bound set.** `coalesceLifetimes` stops expanding and
  eliding and instead computes one canonical, transitively-reduced bound set that
  both the printer and the annotation resolver share. This is also where the
  undirected connected-component grouping in `newLtAnalysis` — sound today only
  because independent param lifetimes never share a bound-graph component, the
  invariant that file documents — is replaced with directional reasoning. The cheaper
  display-only and annotation-only routes are recorded in the plan as the alternatives
  this supersedes.

**Why it lands here.** It sits after M6 because M6 changes the join machinery
directly — the permissive mut-borrow join and the canonical union member order — and
the bound set is built on that settled representation. It sits with or just before M7
because M7 library imports are the first site where declared bounds become
mandatory, which is the first configuration where the unified model beats a
display-only one. Earlier would be premature: nothing in M4 or M5 is blocked, since
the D4 union rendering is sound and only less precise.

**Accept:** a multi-source join renders `fn <'a: 'c, 'b: 'c, 'c>(p: mut 'a {…}, q:
mut 'b {…}) -> mut 'c {…}` instead of the union; a signature with in-list bounds
round-trips through the parser and printer; a body whose inferred bounds violate a
declared bound is rejected with the full outlives message; a redundant declared bound
implied by transitivity is dropped from the rendered set.

---

## M7 — Library type resolution (`std:*` / `web:*` / `node:*`)

Port the standard-library type ingestion onto `soltype`. Today this is a
self-contained subsystem living entirely in `internal/checker/`
(`infer_import.go`, `infer_stdlib_import.go`, `infer_stdlib_scc.go`) plus
`internal/interop/`, and it produces `type_system.Type`. This milestone
retargets it to produce `soltype.Type` in the new checker's `Scope`/`Namespace`,
covering both ingestion channels:

- **Ambient global lib** — `Array`, `Promise`, `Map`, `Set`, `console`, `Math`,
  `JSON`, the iteration protocols, etc., loaded from `lib.*.d.ts` without an
  import (the `globalThis` surface).
- **Scheme imports** — `std:*` / `web:*` / `node:*` (the DOM lives under
  `web:dom`), routed through the `interop` partition table to the stdlib `.esc`
  modules.

This **replaces M2's placeholder "prerequisite tracking"**: the names M2 stubbed
(`Promise`/`Iterable`/`Generator`/`IteratorResult`) and the broader lib surface
now resolve to real `soltype` structures rather than opaque placeholders.

- **Interop reuse, gate intact.** The front half of the existing pipeline
  (`dts_parser` parse → `interop.ConvertModule` → `*ast.Module`) is reusable as
  AST. The back half (the old checker's `InferModule` → `type_system`) is what
  this milestone replaces with the new checker's `soltype` walk over that AST.
  `interop` itself imports `type_system`, so this milestone must consume only its
  AST-producing surface and keep the `soltype` output path free of `type_system`
  — confirm the M2 parallel-package gate still holds.
- **Scope = the operator-free lib subset.** Everything expressible with the
  M3–M6 representational features (generics, object/record types, nominal
  classes + `final`, unions): `Array`, `Promise`, `Map`/`Set`, the
  `Iterable`/`Iterator`/`IteratorResult` protocols, `console`, `Math`, `JSON`,
  the `string`/`number`/`boolean`/`symbol`/`regexp` method surfaces, and the
  common `web:dom` types.
- **Deferred to a phase-2 backfill (after M9).** Lib types whose definitions
  need conditional/mapped/utility **type operators** (`Awaited<T>`, `Partial`,
  `Pick`, `Record`, and the operator-heavy parts of `web:dom`) cannot be
  represented until M9 (type-level operators) lands. Until then they resolve as
  placeholders/inexact stubs — the same posture M2 takes — and are backfilled
  once the operator machinery exists. Record which lib names are stubbed so the
  gap stays visible rather than reading as full coverage.
- **Exactness.** TS-imported lib types are **inexact by default**
  ([exact-types/requirements.md](../exact-types/requirements.md) §8); this
  milestone stamps the inexact flag on ingested lib formers as they land.
- **Not in scope: operators.** The built-in operator schemes (`+`, `==`, `&&`,
  `++`, …) are **not** library types and are **not** owned here. They are
  hand-coded, monomorphic-over-primitive value bindings the expression walk needs
  from day one, so they live in the M2 prelude (a port of the old checker's
  `addOperatorBindings`). M7 ports *type* ingestion (`.d.ts`/interop → `soltype`),
  not the value-level operator env.
- **Rest-param element checking (#677; deferred from M4).** Typed rest params
  `...xs: T[]` check each trailing argument against `T`, which needs `Array<T>` to
  resolve — so the element type only exists once this milestone lands. M4 kept
  rest params arity-only (trailing args unchecked, a documented hole) and marked
  `FuncParam.Rest`'s note "M7." Wire the element check here against the real
  `Array<T>`, dropping the arity-only restriction.
- **Variadic tuple types `[number, ...Array<number>]`.** A tuple with a fixed
  prefix and a *typed*, unbounded, homogeneous tail — distinct from M4's `Inexact`
  tuple flag `[A, B, ...]`, which means "at least these, then *unknown*." The typed
  tail needs `Array<T>`, so it lands here with the rest of library type resolution.
  Add a typed rest/variadic element to `TupleType` — a tail carrying its element
  type, not just a boolean flag — plus the variadic-tuple subtyping rules, e.g.
  `[number, number] <: [number, ...Array<number>]` and
  `[number, ...Array<number>] <: Array<number>`. The `infer`-matching form
  `T extends [any, ...infer R] ? R : never` is M9 (conditional + `infer`), not here.
- **Index-read usage inference + tuple/array closing (the M4 object-close analogue,
  deferred from M4).** M4 shipped `.prop` usage inference plus the close-to-exact
  seal for objects: `inferMember` lowers `recv.prop` to an inexact one-property
  requirement, and `sealUsageObjects` (M4 B2, `solver/poly.go`) folds those
  requirements into one exact object at generalization unless the var escapes to an
  output position or is marked `open`. F1 (#730) extended that to a constant STRING
  index: `recv["bar"]` routes through `valueProp` to the same inexact object
  requirement, so it already gets the object seal. **What index reads still lack is
  the TUPLE path** — `resolveIndexPath` (`solver/infer_expr.go`) sends a constant
  NUMERIC index `recv[0]` and any dynamic key to `reportUnsupported`, because the
  read shape there depends on whether the receiver is a tuple or an `Array`, and
  `Array` only exists at this milestone. Wire it here:
    - **`inferIndex` tuple arm (NEW), constant numeric key.** `recv[0]` lowers to an
      inexact *tuple* requirement "has at least a slot at this index" — the
      positional twin of the `{prop: β, ...}` object requirement `valueProp` already
      builds. So `recv[0]; recv[1]` lands two inexact tuple upper bounds on the
      receiver var, mirroring the object path. The result type is the element var β.
      This is the new-checker port of the reference checker's "single literal index
      infers a tuple" behavior (`checker/tests/row_types_test.go` `NumericIndex` /
      `ArrayElementReadAccess`). It slots into `resolveIndexPath`'s value-index
      branch beside the existing constant-string-key `valueProp` call.
    - **Tuple merge-by-position.** Where the object fold (`mergeObjectGroup`) unions
      properties by NAME, the tuple fold unions slots by INDEX: `recv[0]; recv[2]`
      requires length ≥ 3, with index 1 a hole filled by a fresh var, and a slot
      required at several indices intersects its element types (`recv[0] <: β` and
      `recv[0] <: γ` ⇒ slot 0 is `β & γ`), exactly as the object fold intersects a
      shared property. Add this as a `mergeTupleGroup` beside `mergeObjectGroup`.
    - **Seal arm.** `sealUsageObjects` grows a `TupleType` arm beside its
      `ObjectType` arm, reusing the SAME gating verbatim: a var that is not `open`,
      occurs only negatively, and has no lower bounds gets its inexact tuple upper
      bounds folded into one exact tuple, so `fn f(t) { t[0]; t[1] }` seals to
      `[T0, T1]` and a caller passing a longer tuple is rejected. An escaping
      receiver (`fn f(t) { t[0]; return t }`) is NOT sealed and keeps its open row
      so the returned tuple retains the caller's extra elements, and `open` leaves
      the A2 inexact `[T0, ...]` form — both fall straight out of the existing
      escape/`open` checks, no new gating. The exact rendered form of an escaping
      tuple row follows whatever the object case renders (today `{x} & T0`); the
      tuple row's display is settled when this lands, not specified here.
    - **Dynamic key → `Array`, not tuple.** A non-constant key (`recv[i]`) cannot
      address a positional slot, so it infers an `Array`/index-signature read
      against the now-resolvable `Array<T>` rather than a tuple requirement. **The
      index-signature inference itself is M9** (index types); until then a dynamic
      key against a non-array receiver is a typed error, consistent with M4's
      object computed-key handling and the M9 index-signature deferral. So M7 owns
      constant-index tuple inference and array reads; M9 owns index signatures.
    - **Index WRITES** (`recv[0] = v`) extend C3's `inferMemberAssign` the same way
      — M4 kept the `*ast.IndexExpr` write sub-case as `reportUnsupported` with a
      "needs Array types — M7" note (m4-implementation-plan §C3). The write path
      reuses the tuple/array read classification above plus C3's `mut` receiver
      requirement and `widen`.
- **Field-level move tracking of computed keys (PR 7 follow-up).** The move engine
  tracks moves and uses at field granularity over a `movePlace` — a root binding plus
  a path of `placeSeg` segments (`solver/moves.go`). Today the segment representation is
  already a tagged struct, `placeSeg{kind, name}`, but only the `namedSeg` kind exists,
  built from a static member (`pair.a`) or a *string-literal* index key (`obj["a"]`) via
  `constStringKey`. A computed key — `obj[k]`, `obj[Symbol.iterator]` — currently isn't a
  supported access form, so `exprPlace`/`resolveIndexPath` reach `reportUnsupported` and
  the move engine never sees it. When this milestone makes computed-key and symbol-keyed
  member access infer a type, extend the move engine to derive a segment from the index
  expression's *resolved type*, not its syntax, so a key behind a variable tracks the
  same place as the literal. Concretely:
    - **Generalize `constStringKey` to a type-aware `constKey`** for the move engine,
      keeping the existing syntactic `constStringKey` for the name-resolution call sites
      that need the literal. Priority: an index whose inferred type is a *singleton*
      string or number `LitType` → a `namedSeg` carrying that literal, so `obj[k]` with
      `k: "foo"` keys the same as `obj.foo`; a key whose type is `unique symbol` → a new
      `symbolSeg` kind keyed on the symbol's stable id. A non-singleton or otherwise
      non-constant key falls back to the container place, exactly as a dynamic index does
      now — sound, at worst over-conservative, never a missed use-after-move.
    - **`soltype` prerequisite for the symbol case.** `soltype` has only the `symbol`
      primitive (`SymbolPrim`), no unique-symbol type carrying a stable id; the
      `UniqueSymbolType{Value int}` with that id lives in `internal/type_system`, the old
      checker. So a unique-symbol `soltype` kind must land first, plumbed through the
      visitor/printer/constrain/coalesce, before a `symbolSeg` can key on it. Keying on
      that type id rather than the binding makes `val it = Symbol.iterator` and the
      original `Symbol.iterator` resolve to one place.
    - **Wiring.** Add the `symbolSeg` kind to `placeSeg` and render it as
      `[Symbol.iterator]` in `renderPlace`; thread the index expression's type into
      `exprPlace` (today a free, purely syntactic function), `recordMemberUse`, and
      `consumeOwned`; and give `objKeyName` a `ComputedKey` arm so a symbol-keyed object
      field is tracked. `placeKey` already encodes each segment's kind and length-prefixed
      name, so it admits a new kind without change.

**Open design question — free type-var members in a union-super exists trial.**
M6 PR2's union-super exists rule trials each member of `sub <: (A | B | …)`
under a probe and commits the first success. See
[m6-implementation-plan.md](m6-implementation-plan.md) §PR2 for the rule. The
rule deliberately SKIPS direct `TypeVarType` members in the trial loop.
Canonical sort ranks `TypeVar` before concrete kinds, so without the skip a
`5 <: (α | number)` constraint would trial `α` first, succeed trivially by
recording `5` as `α`'s lower bound, and commit. `α` would then be pinned to
≥5 and the `number` branch would never run. The skip avoids this
speculative pinning.

The skip is sound but incomplete. No source path can reach it today. PR2's
`resolveTypeAnn` arms produce unions whose members are resolved concretes,
and a user-written `T` does not resolve until M7. Once it does, a
generic-union annotation becomes reachable:

```
fn f<T>(x: T | number) { ... }
f("hi")          // currently fails: cannot constrain string <: T | number
                 // even though T := string would type-check
```

The conservative skip rejects this call. A fully complete rule would bind
`T := string` as a last-resort catch-all. The two designs to choose between
when M7 lands generic-union surface:

- **Keep the skip.** Simpler, no speculative pinning. The honest mitigation
  is restructuring the user's code away from the generic union: split into
  separate signatures (`fn fT<T>(x: T)` and `fn fN(x: number)`) or use a
  discriminating wrapper (`type Boxed<T> = {kind: "var", val: T} | {kind:
  "num", val: number}`). Two superficially-attractive workarounds DON'T
  work: reordering the union does nothing because `newUnion` canonicalizes
  member order at construction, and explicit type arguments at the call
  site (`f<string>("hi")`) aren't syntax `CallExpr` supports today. So
  "keep the skip" forces a real structural rewrite at every call site, not
  a one-line tweak.
- **Two-pass exists trial.** Try concrete members first. If none commit,
  try var members in a second pass. Preserves completeness without
  first-pin behavior. PR7's `if-let` / `let-else` narrowing reuses the same
  exists path. See [m6-implementation-plan.md](m6-implementation-plan.md)
  §PR7. So this choice also decides whether `if let x: T = u` over
  `u: T | number` can bind `T` to the matched branch.

The IntersectionType-sub overload arm in `constrain.go` already trials
variable-shaped members through the same probe machinery. That is how
`g = f; g(x)` resolves when `f`'s arms involve type vars. The two arms are
asymmetric today. The resolution chosen here should either align them or
document why the union and intersection cases warrant different treatment.

**Trial-and-commit diagnostic follow-ups (after generic-union surface lands).**
M6 PR2 introduced the union-super exists rule, which trials each member of
`sub <: (A | B | …)` and commits the first success. Three sibling sites in
`internal/solver` use the same pattern: `resolveOverload`, the
IntersectionType-sub arm, and `constrainAssign`. All four share the same
**first-success-commits failure modes**:

1. **Over-constraining inner inference variables.** A trial that succeeds
   by adding bounds to vars nested in sub or super locks those vars to the
   chosen branch. A later use that would have matched a different branch
   is rejected.
2. **Order-dependence on canonical sort, not user intent.** Members are
   trialled in `compareType` order. The user's source order is not what
   decides the winner.
3. **Brittleness to union-membership changes.** Adding a member can shift
   canonical sort and change which branch wins, making a seemingly
   additive change alter downstream behavior.
4. **Misleading downstream error messages.** When the committed bound
   conflicts with a later use, the error blames the later constraint with
   no breadcrumb back to the union trial that forced the commitment.
5. **No backtracking.** Once committed, the system never reconsiders. A
   later contradiction errors instead of unwinding to try the next branch.
6. **Loss of cross-variable correlation.** A trial that touches multiple
   correlated variables locks them together to one branch. A user
   expecting the disjunction to remain open across both vars finds it
   doesn't.

M6 PR2.5 takes the cheapest mitigations: a shared trial helper,
specificity-ordered candidates, and deletion of the M5-era
`constrainAssign` workaround
([m6-implementation-plan.md](m6-implementation-plan.md) §PR2.5). The two
deferred mitigations bite once M7 makes generic-union annotations
reachable from source, since user-written `T | A` is the case where
over-constrained inner variables (failure mode #1) actually surface to the
user. Land them once that happens:

- **Tag committed bounds with their union-trial origin** (addresses
  failure mode #4). When the trial commits, mark the added bounds (or the
  var) with a side-table entry pointing at the union annotation node.
  When a downstream constraint fails on a tagged var, the diagnostic
  engine chases the tag and emits "this var was committed to branch A of
  (A | B) at <span>; later use needs B." Replaces today's flat "string is
  not number" with a breadcrumb back to the union choice that forced the
  conflict. Probe-safe via the existing rollback hook discipline.
- **Ambient-time ambiguity detection** (addresses failure mode #1 at
  declaration time). After the first trial commits, optionally peek at
  later branches under throwaway probes. When another branch would also
  succeed AND would have added different bounds, emit a warning at the
  union annotation site asking the user to disambiguate. Catches
  over-constraint at declaration time rather than at downstream use.
  Roughly doubles the work for ambiguous unions, which matches the cost
  of the original failure mode. Worth landing once user reports of
  confusion start coming in.

**Accept:** real source referencing core lib types (`Array<T>`, `Promise<T>`,
`Map<K, V>`, `Iterable<T>`/`Iterator<T>`/`IteratorResult<T>`, `console`) resolves
to real `soltype` structures and type-checks (not placeholders); `import { … }
from "std:array"` / `"web:dom"` resolves member types; the M3 `await` and M5
`for (x in xs)` rules now exercise against the **real** `Promise`/`Iterable`
(replacing the M2 placeholders); operator-dependent utility types remain stubbed
pending M9, and which names are stubbed is reported, not silently dropped.
`fn f(t) { t[0]; t[1] }` infers and SEALS the param to exact `[T0, T1]`, rejecting a
longer-tuple argument, while `fn f(open t) { … }` stays inexact `[T0, ...]` and
`fn f(t) { t[0]; return t }` is not sealed (the open row is preserved) — the tuple
analogue of M4's object close; a dynamic-key read resolves against the real
`Array<T>`.

**Depends on:** M3 (generics), M4 (objects/records — `sealUsageObjects`, the close
machinery this extends), M5 (classes / methods / `final`), M6 (unions). **Feeds:**
M8 — the real-package differential cannot run
the existing `fixtures/` tree (which uses `console`/`Array`/`Promise`/…) without
real lib types.

---

## M8 — Second fixture harness + differential triage

Two complementary mechanisms, picked to match the granularity of what each one
tests:

- **Granular semantics** lives in table-driven `*_test.go` files in the new
  checker package (the spike's existing pattern). Each entry is
  `(source, expected printed type | expected error message)`. Hundreds of
  entries per language-feature file; zero per-case package overhead. Authored
  against intended semantics, **not** copied from the old checker — where the
  new checker improves (e.g. `unknown` vs. vacuous `<T0>`), the test asserts
  the improved form. This is where the bulk of language-feature coverage
  lives.
- **Real-package regression** runs the new checker over the existing
  `fixtures/` tree via a **second harness** (sibling to
  `cmd/escalier/fixture_test.go`). Phase 1 (this milestone) runs the checker
  only — no codegen; acceptance is "the new checker accepts/rejects every
  fixture the way the old checker does, modulo triaged intended
  improvements." Phase 2 (post-M10) extends to end-to-end compilation and
  `build/` golden diffs once the codegen path is settled. This is the
  regression net that catches "did we break anything real."
- **Differential triage** runs both checkers on the same parsed tree (parse
  once, write to the old `inferredType` field and the new `Info` side table
  separately) and buckets every divergence as match / intended-improvement /
  bug. The bug bucket is the only CI gate. Intended improvements get a short
  note inline so future contributors don't mistake them for regressions.
- Wire the new checker behind a flag at the **3** `compiler.NewChecker` sites.
- **Test assertions encode exact-by-default.** Whichever artifact records the
  intended semantics — table tests, fixture goldens, or both — reflects
  exactness as the implementation produces it: source literals exact,
  TS-imported types inexact
  ([exact-types/requirements.md](../exact-types/requirements.md) §8). Default
  behavior was settled at M3 (functions) and landed with each former through
  M6; no extra coordination needed here.
- **Running exactness-aware fixtures through the exactness-unaware old
  checker.** The old checker knows nothing about exact/inexact, but fixtures
  still need to express the distinction. Strategy (cheapest first):
  - **Parser-level tolerance, semantics no-op in old checker.** Teach the
    shared parser to accept the `...` trailing-marker syntax (and the
    `Exact<T>`/`Inexact<T>` type operators once M9 lands); the old checker
    reads the AST node and ignores the flag, behaving as today. The old
    checker is already an effectively-inexact world, so most fixtures "just
    work" without semantic changes — the cost is one parser change and zero
    old-checker logic changes.
  - **`applicable_to: [new]` skip tag** for fixtures that hinge entirely on
    exact-only behavior (exhaustive `match` with no default arm, rejection of
    an extra member on an exact target, `Exact<T>`/`Inexact<T>` reduction).
    Pick the cheapest location for the tag: a field in `package.json` or a
    magic comment header in `lib/index.esc`. The old-checker harness skips
    tagged fixtures; the new-checker harness runs them.
  - **Per-fixture golden split** (separate `build/` directories per checker)
    as a last resort, for fixtures too central to skip but where the old
    checker's output is meaningfully different (not just absent). Avoid where
    possible — it bifurcates the fixture authoring model.

  Explicitly **not** chosen: preprocessing fixture source to strip `...`
  before feeding the old checker. "Same parse tree, two checkers" is the
  whole premise of the differential harness; a divergent parse pipeline
  muddies it.

**Accept:** new-checker table tests cover the M3–M6 surface with intended-form
assertions; second fixture harness runs the new checker over every fixture in
`fixtures/`, with every old-vs-new divergence triaged (no untriaged diffs);
fixtures tagged `applicable_to: [new]` (exact-only behavior) are skipped on
the old-checker side and contribute no diffs. Exact/inexact-sensitive cases
(literal exactness, TS-import inexactness, an exact-union exhaustive `match`)
are represented either in table tests, as tagged fixtures, or both.

**Gate:** pervasive *unintended* divergence ⇒ the new checker has correctness
gaps; burn down before proceeding.

---

## M9 — Type-level operators

The last MVP milestone. The full type-level operator surface, reduced via
Baseline-D (reduce when operands ground) + Design-A residual nodes reduced
post-coalescing, + recursive-type handling (cycle cache + depth budget, and
the level-2 regularity check). (Spike M5/M7/M9 + recursion + CheckRegular.)

- **`keyof T`** — keys of an object/class type as a union.
- **Indexed access `T[K]`** — including distributive behavior when `K` is a
  union.
- **Conditional types `T extends U ? X : Y`**, including:
  - **`infer T` clauses** in the `extends` operand, binding fresh variables to
    matched positions (function arg/return, tuple element, constructor return,
    promise payload, etc.).
  - **Distribution over naked-type-parameter unions**, matching TS semantics.
- **Mapped types `{[K in Keys]: F<K>}`**, including:
  - Modifier syntax (`readonly`/`?` add/remove, with `+`/`-`).
  - Key remapping via `as` clauses.
  - Combinations with `keyof` / indexed access in the value position (the
    pattern underlying `Pick`, `Omit`, `Partial`, `Required`, `Readonly`).
- **Object spread types `{...A, x: T}`** — first-class object spread types,
  parallel to Escalier's tuple spread types and modeled on Flow; TypeScript has
  no equivalent. A reducible operator: it reduces when the operand grounds, with
  the rightmost field winning on overlap, and stays residual when the operand is
  an abstract type parameter, reduced post-coalescing like the operators above.
  Exactness threads from the operand, so a spread of an inexact object is inexact.
  **Optional-field overlap uses Flow-faithful show-through union:** when a later
  operand's *optional* field overlaps an earlier key, the values union rather than
  override. Required-in-earlier with optional-in-later yields `T | U` **required**;
  optional with optional yields `(T | U)?`. For example, `{...A, ...B}` with
  `A = {k: number}` and `B = {k?: string}` reduces to `k: number | string`,
  required. Object rest/spread in both literals and type annotations lands here,
  not M4.
- **Tuple spread types `[...P, x]`** — the positional analogue of object spread,
  spreading one tuple type into another. Same reducible-operator shape: it splices
  when the operand grounds to a concrete tuple and stays residual when the operand
  is an abstract type parameter, reduced post-coalescing. M4 already handles the
  concrete case for tuple *literals* — `[...pair, 3]` where `pair` is a known
  tuple — but not the abstract-operand type. This is distinct from a typed variadic
  tail like `[number, ...Array<number>]`, which is M7: that needs `Array` and is an
  unbounded homogeneous tail, not a splice.
- **Template literal types** — string-literal types built from interpolated
  type unions (e.g. `` `on${Capitalize<K>}` ``), including the intrinsic
  string-manipulation operators `Uppercase`/`Lowercase`/`Capitalize`/
  `Uncapitalize`.
- **Exactness propagation through operators**
  ([exact-types/requirements.md](../exact-types/requirements.md) §7): `keyof T`
  is exact iff `T`'s key set is exact; `T[K]`, conditional results, mapped
  types, object spread, and template literals derive exactness from their inputs.
  This is the
  first milestone where exactness must *propagate through reduction*, not just
  be checked — it builds on the flag laid down in M3–M6. The
  `Exact<T>`/`Inexact<T>` type-level utilities also land here (they are type
  operators).
- **Generators (`gen fn` / `yield e` / `yield from g`).** Same shape as
  `throws`: `FuncType` gains a `Yields Type` field, covariant in subtyping,
  defaulting to `never`. A `gen fn () -> R` is internally typed with body
  return `R` and a yields-inference variable accumulating each `yield e`'s
  type as a lower; externally the function's type is
  `Generator<Y, R, TNext>` (or `AsyncGenerator<…>` for `async gen fn`) where
  `Y` is the coalesced yields variable. `yield e` requires no special
  constraint beyond `typeof(e) <: yields_var`; the expression itself has
  type `TNext` (the next-value-sent-in type, which lands as a third
  position once anyone uses `generator.next(value)`). `yield from g`
  (a.k.a. `yield*` in JS) requires `g <: Iterable<Y>` and forwards yields.
  The constraint engine extends just like `throws` did: parallel arms in
  `constrain`/`extrude`/`LevelOf`/printer, no new lattice machinery.
  Depends on `Generator<Y, R, TNext>` / `AsyncGenerator<…>` being
  available from the stdlib (M2 placeholder; real resolution in M7 — M9 follows,
  so generators can rely on the real types).
- **`throws T` clause on functions.** `FuncType` gains a `Throws Type` field
  (parallel to `Ret`), covariant in subtyping, defaulting to `never` (⊥) when
  the source has no `throws` clause. The constraint engine extends naturally:
  the function arm in `constrain` recurses `l.Throws <: r.Throws`; `extrude`
  recurses into `Throws` with the same polarity as `Ret`; `LevelOf` takes the
  max of params, ret, and throws; the printer renders `throws T` after the
  return type when `T` isn't `never`. Each function body has a throws
  inference variable that accumulates lowers as `throw e` statements and
  calls to throwing functions emit `constrain(thrown, throws_var)`. Throws
  polymorphism (`<E>(f: () -> T throws E) -> T throws E`) falls out of M3's
  let-generalization without special handling — `E` is just another type
  variable that gets quantified. **Open design question, not settled in
  this plan:** how `try`/`catch` narrows the inferred throws of the body
  (i.e., the "subtract `K` from `body_throws` for everything not in the
  `catch` clause" semantics). A two-variable encoding (`body_throws <:
  surrounding_throws ∪ caught_throws`) works in the existing lattice and is
  the conservative starting point; integration with the existing checker's
  narrowing semantics is the actual question to resolve before
  implementation.

**Accept:** the spike's type-operator cases against real source —
`keyof`/indexed access over ground and usage-inferred operands; conditional
types incl. `infer` and distribution; recursive aliases terminate (finite knot
or budget). Errors (e.g. arity, non-regular recursion) assert full messages.
Plus: `keyof` of an exact object is an exact union and of an inexact object an
inexact union; `Exact<{x, ...}>` ⇒ `{x}` and `Inexact<{x}>` ⇒ `{x, ...}`.
Plus, the TS utility-type suite as end-to-end verification — defining them in
Escalier and asserting their reductions match TS:

- `Pick<T, K>`, `Omit<T, K>` (mapped + indexed access + key filtering via
  conditional `K extends ...`).
- `Partial<T>`, `Required<T>`, `Readonly<T>` (mapped-type modifiers).
- `Exclude<U, V>`, `Extract<U, V>`, `NonNullable<T>` (distributive
  conditional).
- `ReturnType<F>`, `Parameters<F>`, `ConstructorParameters<F>`,
  `InstanceType<C>` (conditional + `infer`).
- `Awaited<T>` (recursive conditional + `infer`).
- `Record<K, V>` (mapped over a key union).
- `Capitalize<S>` / `Uncapitalize<S>` / `Uppercase<S>` / `Lowercase<S>` and a
  small template-literal case (e.g. `EventName<K>` ⇒ `` `on${Capitalize<K>}` ``).

Plus, on `throws`: a no-`throws` body infers `throws never` and prints
without the clause; a body with `throw "boom"` infers `throws "boom"`;
covariant subtyping (`fn () throws "a"` is a subtype of `fn () throws "a"
| "b"` but not the reverse); throws polymorphism (`<E>(f: () -> () throws
E) -> () throws E` round-trips through let-generalization); a `try`/`catch`
test for the body-narrowing rule decided during design.

Plus, on generators: `gen fn () { yield 1; yield "a" }` renders
externally as `Generator<1 | "a", void, unknown>`; `yield from g` where
`g: Iterable<number>` is accepted in a `gen fn` whose yields lower-bound
includes `number`; `gen fn` outside a `gen` context (top-level `yield`)
is rejected by the AST walk; `Awaited<ReturnType<F>>` over an
`async gen fn () -> R` returns `R` once `Awaited<T>` and `ReturnType<F>`
reduce through the M9 operator machinery.

---

## Later (post-MVP)

- **M10 — Codegen.** Either a `soltype → type_system` bridge to reuse codegen
  unchanged, or port codegen (`dts.go` et al., ~4 files / ~30 refs) onto
  `soltype`. Decide when the checker is proven. **The value-level `exact<T>(v)`
  conversion** ([exact-types/requirements.md](../exact-types/requirements.md)
  §6.6) belongs here, not in the checker: it lowers to JS
  (object property-pick, `tuple.slice(0, n)`, a discriminating `match` for
  unions; functions excluded) and needs no reified types. The `@escalier-type`
  JSDoc round-tripping for exactness
  ([exact-types/requirements.md](../exact-types/requirements.md) §9) is also
  codegen work.
- **M11 — LSP.** Switch the LSP to the new checker's `Scope`/`Info`.
- **M11.5 — Diagnostics quality & error-rendering capstone.** A late,
  corpus-driven pass that owns the **error-reporting gestalt** no single feature
  milestone sees, and discharges the diagnostics work deferred through M2.5+. It is
  deliberately *not* a re-litigation of per-feature message wording — that stays
  incremental, asserted as each feature lands (the CLAUDE.md "assert the full error
  message" convention). Its charter is the cross-cutting rendering + audit layer:
  - **The multi-hop provenance renderer.** M2.5 ships only the `FromAST` *leaf* and
    a single-lookup `NodeFor`; the interior `Origin` edges
    (`FromInstantiation`/`FromBoundPropagation`/`FromExtrusion`/`FromCoalesce`) are
    minted by M3+ but nobody *renders* the chains they form. This milestone builds
    the renderer that walks the provenance DAG to its AST leaves — "α is `number`
    because the literal `42` flowed into it at line 17" — the "why this type" story
    [02-design-notes.md](02-design-notes.md) §"Provenance side table" describes and
    M2.5 explicitly defers.
  - **Multi-span / related-span rendering.** M2.5 *records* `Related()` (the
    expected-source alongside the actual-source, the prior declaration alongside the
    duplicate, the "defined here" companion to an ident-use error) but leaves
    presentation to "when the CLI is wired." This milestone renders related spans in
    both the CLI diagnostics and the LSP (hovers / related-information) — the surface
    M11 stands up.
  - **Cascading-error suppression + ordering/dedup.** Suppress derived errors that
    exist only because of an upstream failure, and order/dedup a file's diagnostics
    so one root cause doesn't read as ten.
  - **Corpus-wide audit, differential against the old checker.** Run the audit over
    the M8 fixture corpus with the old checker *still present* (this is why the
    milestone precedes the M12 flip): its diagnostics are the parity baseline M12
    deletes. Acceptance is a checklist applied corpus-wide — every error has a
    precise primary span, sensible related spans, consistent expected/actual
    framing, no duplicate/cascaded noise — with any per-feature wording fix filed in
    place.

  This milestone is also the **named home for the deferred-diagnostics backlog**, so
  those notes get a due date rather than drifting: M2.5's "defined here" related span
  (the `FromInstantiation` companion to an ident-use error), the zero-span fallback
  footguns M2.5 flags for the M4-reachable error kinds, and the multi-hop renderer
  above. Having this backstop is what lets M3–M9 ship *good-enough* errors and defer
  polish here instead of being side-tracked chasing perfect diagnostics mid-feature.

  **Accept:** the audit checklist passes across the M8 corpus (precise primary span,
  coherent related spans, no cascades/dupes), with no diagnostic worse than the old
  checker's; a provenance chain renders its full why-chain to AST leaves; an
  ident-use type error shows both the use (primary) and the definition ("defined
  here"). **Depends on:** M9 (full feature set ⇒ complete provenance DAG), M8
  (fixture corpus + differential harness), M11 (the LSP rendering surface).
  **Precedes:** M12 — the new checker's diagnostics should be at least at parity with
  the old checker *before* the flip deletes the baseline.
- **M12 — Flip & cleanup.** Make the new checker the default; retire the old
  checker + its tests; **delete** the AST `inferredType` field, the
  `type Type = type_system.Type` alias, and `tools/gen_ast`'s generation of the
  field — leaving the AST fully type-system-agnostic.
- **`std:*` / `dom:*` exactness annotation (independent track).** Auditing which
  library callbacks should be inexact and which lib classes are `final`
  ([exact-types/requirements.md](../exact-types/requirements.md) §11,
  [exact-types/builtin-classes.md](../exact-types/builtin-classes.md)) is a
  stdlib-curation
  effort, not a checker change — it consumes the exactness machinery rather than
  implementing it. It can proceed once the flag exists (after M3–M6) and is
  sequenced independently of the cutover.
- **Move/affine semantics (use-after-move) — beyond the M-series.** A future
  effort to treat owned-value stores and moves affinely, so storing or passing an
  owned value or a `mut` borrow consumes the source and any later use through the
  original binding is a compile-time use-after-move error. It is the principled,
  general fix for the aliasing-through-stores gaps the `mut`-transition checker
  only partially covers — for example storing a `mut` borrow into immutable
  module-level state and then mutating it. Tracked at #762, the use-after-move
  item of the broader sound borrow checker #618. It needs its own RFC and is
  layered after the M12 flip, not slotted into the M-series.
- **Backtracking + disjunctive bounds — beyond the M-series.** The structural
  fix for the first-success-commits failure modes in `internal/solver`. Today
  four trial sites (`resolveOverload`, the IntersectionType-sub arm, the M6
  PR2 union-super exists rule, and the pre-PR2.5 `constrainAssign`) all pick
  the first candidate that holds and never reconsider. M6 PR2.5 lands the
  cheap mitigations (shared helper, specificity ordering, fewer sites), and
  M7 adds two diagnostic-quality follow-ups (trial-origin tagging and
  ambient-time ambiguity warnings). The remaining failure modes
  (over-constraining inner vars without warning, no backtracking when a
  downstream constraint contradicts the commit, loss of cross-variable
  correlation across trial branches) require a structurally different
  solver. Two complementary directions:
  - **True backtracking.** A search-based solver that unwinds the committed
    trial when a downstream constraint contradicts it and retries the next
    candidate. Fixes failure modes #1 (over-constraining inner vars), #4
    (misleading errors), and #5 (no backtracking) from the M7 enumeration
    above. Bounded backtracking — only revisit the most recent trial — is
    tractable; full backtracking through propagated bound graphs is an open
    research problem at this language's scale.
  - **Disjunctive bound representation.** Let a var carry "γ ≤ A OR γ ≤ B"
    as a first-class constraint rather than picking one and committing.
    Fixes failure mode #6 (cross-variable correlation) by recording the
    correlation explicitly. Requires row-types or refinement-types machinery
    in the bound graph, which is a fundamentally different solver.
  Both are large undertakings, post-MVP, and warrant their own RFCs. The
  realistic short-term path is to keep accumulating the diagnostic and
  ergonomic mitigations PR2.5 and M7 land, and revisit the structural fix
  if user reports show the failure modes biting at scale.

## Dependency / risk ordering rationale

- M4 is front-loaded as the combined "core value types" milestone because
  records, `mut`, and lifetimes are an inseparable cluster once lifetimes ride on
  values — and it contains the highest-risk gate (`mut` invariance).
- M4.5 (script inference) is numbered `.5` like M2.5 — to slot between M4 and M5
  without renumbering. It rides immediately after M4 because it adds no new inference:
  every building block — the liveness pre-pass, the per-body `funcCtx`, alias tracking,
  the transition checker, and lifetime origination/escape — ships in M4, and M4.5 is
  only the `InferScript` entry point that runs them over a script's linear top-level
  body. It does not wait for M8's fixture harness, since `InferScript` is exercised
  directly by the single-file table harness, so script-level lifetime/transition
  coverage lands as soon as M4's machinery exists.
- M5 (nominal classes) sits right after M4 so it reuses M4's `mut`/lifetimes
  substrate directly. Its subtyping rule is small; its bulk (constructor / body
  inference / overloads) is language-proportional and unrelated to the inference
  core, so it doesn't change M4's risk profile.
- Codegen is deferred to the latest safe point because it is the single largest
  integration cost (its `type_system` dependency) and is not needed to prove the
  checker.
- M8's test posture is "improve, don't match" — table tests assert intended
  semantics (not old-checker output), and the second fixture harness's
  differential is a triage tool, not a parity gate. This is what lets
  intended improvements through instead of forcing old-checker parity.
- M6.5 (lifetime bounds) is numbered `.5` for the same reason M2.5 is — to slot
  between M6 and M7 without renumbering. It rides *after* M6 because M6 settles the
  join machinery its canonical bound set is built on (the permissive mut-borrow join
  and canonical union order), and *before/with* M7 because library imports are the
  first no-body site where declared bounds become mandatory. It is deferred past M4,
  even though M4 D4 already renders the union approximation, because that rendering is
  sound and nothing in M4–M5 needs the precise bounded form.
- M2.5 sits between M2 and M3 (rather than folding into M3 or deferring with the
  rest of `Prov`) because the `FromAST` discipline is "born-with-the-type" infra:
  threading one provenance line per construction site is cheap across M2's ~8
  sites and compounds in cost as M3–M6 multiply them. Numbered `.5` to avoid
  renumbering M3–M9 and their references across the docs and code comments. Its
  scope is the *leaf* layer only; the multi-hop interior edges deliberately ride
  along with the M3+ features that introduce deep provenance chains, so M2.5 stays
  a small, independently-verifiable infra step (golden span fixtures) and does not
  enlarge M3's language-feature acceptance bar.
- M11.5 (diagnostics capstone) is numbered `.5` for the same reason M2.5 is — to
  slot between M11 and M12 without renumbering — and sits *before* the M12 flip on
  purpose: the old checker is the parity baseline for the error-quality differential,
  and M12 deletes it. It rides after M11 because the LSP is the rendering surface for
  the multi-span/chain work, and after M9 because the provenance DAG it renders
  (the M3+ interior edges) isn't complete until the last feature lands. Folding the
  error-rendering gestalt into one late, corpus-driven pass is also what lets M3–M9
  defer diagnostics polish — `Span()` precision and full messages still land with
  each feature, but rendering, chain-following, and cross-feature consistency wait
  for the capstone instead of being gold-plated mid-feature.
