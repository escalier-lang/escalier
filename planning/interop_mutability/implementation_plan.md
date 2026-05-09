# TypeScript Interop Mutability: Implementation Plan

This plan implements the requirements in [requirements.md](requirements.md).
Phases are ordered so each one is independently testable and merges
without requiring the next to be in place. Within a phase, work items
list the touch points in the existing codebase.

## Existing surface area to extend

- [internal/dts_parser/](../../internal/dts_parser/) — parses `.d.ts`
  into a TS-shaped AST. Already handles `readonly` on properties; needs
  to surface comments, `this` parameters, and `Readonly<…>` references
  to the consumer.
- [internal/interop/](../../internal/interop/) — converts the
  `dts_parser` AST into Escalier `type_system` types. This is where
  mutability classification needs to land.
- [internal/codegen/](../../internal/codegen/) — emits `.d.ts` and JS.
  `@esctype` emission lands here.
- [internal/type_system/](../../internal/type_system/) — already models
  mutability on function/method types; classification feeds into
  existing fields, no new ADT additions expected.
- [internal/checker/](../../internal/checker/) — already enforces
  mutability at call sites. The new uncertainty warning lives here.

## Phase 0 — Spec & test scaffolding

Goal: lock the override-file format and the `@esctype` grammar before
writing code that depends on either.

### Done

- **`override` keyword** added to the lexer
  ([token.go](../../internal/parser/token.go),
  [lexer.go](../../internal/parser/lexer.go),
  [expect.go](../../internal/parser/expect.go)). Tests pass; no
  conflicts with existing identifiers.
- **`overrides/` discovery rule** specified in
  [requirements.md](requirements.md) — three-tier walk (shipped →
  dep `node_modules/*/overrides/` → consuming project), recursive
  within each, subdirectory layout has no semantic effect.
- **Merge-semantics doc** —
  [override_merge_semantics.md](override_merge_semantics.md) covers
  member-presence rules, overload collapsing, override-defined
  overloads, static/instance separation, getter/setter independence,
  generics arity matching, module-level blanket pragma
  (`@all_pure`), conflict resolution across files, and order
  independence.
- **`@esctype` grammar.** Form `@esctype {<type>}`, balanced-brace
  scanning respecting string-literal context, multiline allowed,
  object types nest naturally. Only remaining escaping concern is
  `*/` inside a string literal — handle as `*\/` on emit /
  unescape on consume.
- **Resolution-order fixture** —
  [fixtures/interop_mutability/](../../fixtures/interop_mutability/)
  exercises tiers 2–8. Captures current behavior; will flip to
  passing as later phases land.
- **Override-merge fixture placeholder** —
  [fixtures/interop_mutability/overrides/example.esc.future](../../fixtures/interop_mutability/overrides/example.esc.future)
  shows the intended syntax. Renamed from `.esc` so the build
  harness ignores it until the parser sub-task below lands.

### Remaining (Phase 0a — parser sub-task)

The lexer recognizes `override`, but the parser does not yet accept
the `override declare ...` forms because the underlying
`declare module "..."` and `declare global` block forms don't exist
in the grammar. This is its own self-contained task.

- Add `declare module "<name>" { <decl>* }` to the grammar.
- Add `declare global { <decl>* }` to the grammar.
- Accept an optional `override` prefix on top-level `declare module`
  / `declare global` and on the existing top-level declare forms
  (`declare class`, `declare interface`, `declare fn`, `declare
  type`, `declare val`).
- Plumb an `Override bool` flag through the affected AST nodes
  alongside the existing `Declare bool`.
- Once parsing works, rename
  `fixtures/interop_mutability/overrides/example.esc.future` to
  `.esc` and snapshot.

Exit criteria for Phase 0 overall: all of the above. Phase 0a is the
gate before Phase 1 can rely on the resolver finding override
declarations at all.

## Phase 1 — Resolution-order plumbing in `interop`

Goal: introduce the eight-tier resolution order from the requirements
as a single function, wired in but populated only with existing
behavior (so output is unchanged).

- Add `internal/interop/mutability.go` with a `Classify` entry point
  that takes the TS-side declaration plus surrounding context (class
  shape, module path) and returns `(mutability, source)`, where
  `source` is one of the eight resolution tiers. `source` is needed for
  the uncertainty warning.
- Route every place in `interop/decl.go` and `interop/helper.go` that
  currently decides method/parameter mutability through `Classify`.
  At this phase the function only implements tiers 2 (the existing
  `readonly`-property handling) and 8 (default to mutating).
- Snapshot tests in `internal/interop/` verify no behavior change.

Exit criteria: `Classify` is the single decision point; `go test ./...`
green with no snapshot churn.

## Phase 2 — Strong signals (resolution-order tier 2)

Goal: cover all explicit author signals that don't require external
data.

Implement, in `internal/interop/mutability.go`, classification for:

- `Readonly<T>` / `ReadonlyArray<T>` / `ReadonlySet<T>` /
  `ReadonlyMap<T>` — drives tier-2 classification. Requires
  `dts_parser` to expose the unresolved type-reference name; extend if
  not already present.
- `readonly` `this` parameter — requires `dts_parser` to model the
  `this` parameter (it currently may discard it). Add a small AST
  field if needed and thread through.
- Property getters/setters — `get foo()` ⇒ non-mutating receiver,
  `set foo()` ⇒ mutating. Likely a small classifier change in
  `dts_parser/class.go` consumers.
- Well-known symbol methods — small allow-list (`toString`, `toJSON`,
  `toLocaleString`, `valueOf`, `[Symbol.iterator]`,
  `[Symbol.asyncIterator]`, `[Symbol.toPrimitive]`).
- `readonly` properties (principle #6) — already partially handled;
  consolidate so it's not bypassed by anything else.

Tests: extend the Phase 0 fixtures; add table-driven unit tests in
`internal/interop/mutability_test.go`.

Exit criteria: every symbol in the Phase 0 strong-signals fixture
classifies correctly, with `source = strong-signal`.

## Phase 3 — Override file format & loader

Goal: build the loader that Phase 4 (shipped overrides) and the user
override file feature both depend on.

- Implement the format chosen in Phase 0 in a new
  `internal/interop/overrides/` subpackage (or inline if small).
- Resolver: given a fully-qualified TS symbol path
  (`module#Class.prototype.method`), return the override entry or
  nothing.
- Load priority: user overrides > shipped overrides (matches
  resolution-order tiers 3 and 4).
- `Classify` consults the resolver between tiers 2 and 5.

Tests: load a synthetic override file in tests, verify resolver order
and that user overrides win over shipped.

Exit criteria: loader covered by unit tests; `Classify` consults it but
no overrides are shipped yet.

## Phase 4 — Shipped overrides

Goal: author the data tables that the resolver loads at startup.

Two sub-tasks, parallelizable:

- **Standard library** — classes that don't ship a `Readonly*`
  variant in TypeScript's lib files and therefore can't be classified
  by tier 2 alone: `Date`, `RegExp`, `Promise`, `Error`, typed arrays
  (`Int8Array` etc.), `URL`, `URLSearchParams`, `WeakRef`, `WeakMap`,
  `WeakSet`, iterator / generator protocols. Source of truth: MDN.
  Coverage tracked in a checklist in this file as entries are added.

  `Array` / `Map` / `Set` are **not** in this list — TypeScript
  ships `ReadonlyArray` / `ReadonlyMap` / `ReadonlySet` alongside
  the mutable variants, so principle #2 (tier 2) handles them
  directly. The ES2023 `toSorted` / `toReversed` / `toSpliced` /
  `with` methods appear on `ReadonlyArray` in lib.es2023, so they
  classify as non-mutating without an override entry.

  **Layout: group by ECMAScript spec revision**, mirroring
  TypeScript's `lib.es*.d.ts` split. A symbol introduced in a given
  revision lives in that revision's directory:

  ```
  overrides/stdlib/
    es5/
      Date.esc
      RegExp.esc
      Error.esc
    es2015/
      Promise.esc
      WeakMap.esc
      WeakSet.esc
      iterator.esc
    es2017/
      typedarrays.esc
    es2021/
      WeakRef.esc
    es2023/
      // (none today — Array additions are tier 2)
    dom/
      URL.esc
      URLSearchParams.esc
  ```

  When an Escalier project later gains a target-ES-version setting,
  the loader can include only the override files at or below the
  selected revision, matching TS's `lib` semantics. Until then, all
  revisions load. The `dom/` bucket is separate because DOM types
  aren't keyed to ECMAScript revisions; they map to TS's
  `lib.dom.d.ts`.
- **FP / immutability libraries** (principle #5) — Ramda, fp-ts,
  Effect, Immutable.js, lodash/fp. For these, default every method to
  non-mutating in receiver and arguments; one blanket entry per
  module rather than per-method.

Tests: fixture per library that imports a representative subset and
checks the classification.

Exit criteria: stdlib counter-examples (`Date.setHours` mutates,
`toSorted` doesn't, `Object.assign` mutates target) all classify
correctly.

## Phase 5 — Heuristics (tiers 5–7)

Goal: implement the remaining tiers so unknown TS APIs get useful
classifications.

- Primitive wrappers (tier 5) — small allow-list against `Number`,
  `BigInt`, `String`, `Boolean`.
- `get*` rule with exception list (tier 6) — implement against the
  exception patterns in principle #4.
- Name-based heuristics (tier 7) — predicate, conversion, query, copy,
  iteration prefixes for non-mutating; mutating-prefix list. The
  conflict resolution rule ("if both, prefer mutating") is part of
  this.

Tests: each heuristic gets a table-driven test; a mixed fixture
verifies tier ordering.

Exit criteria: every heuristic in the requirements is testable and
covered.

## Phase 6 — `@esctype` round-trip

Goal: round-trip Escalier types through `.d.ts` losslessly.

- **Emit side** in `internal/codegen/` (verify exact location): for
  every exported symbol, append `@esctype <printed-type>` to the
  existing TSDoc comment if present, or emit a fresh comment. Reuse
  the Escalier printer for the value.
- **Consume side** in `internal/interop/mutability.go`: parse
  `@esctype` from the leading TSDoc block, run the value through the
  Escalier type parser, return as the tier-1 classification (and as
  the full Escalier type, bypassing every other inference).
- Ship a `tsdoc.json` template documenting `@esctype` so consumers'
  TSDoc tooling doesn't flag it as unknown.
- Round-trip fixture: `.esc` → `.d.ts` → consumed back as Escalier;
  diff the two type printouts, expect identical.

Exit criteria: round-trip fixture passes; classification tier 1 wired
in.

## Phase 7 — Uncertainty warning

Goal: opt-in warning when an immutable-receiver call relies on a
heuristic.

- Add a CLI flag (suggested `--strict-interop` or
  `--warn-uncertain-mutability`).
- In `internal/checker/` at the call-site mutability check, if the
  callee's mutability classification has `source` ∈ {tier 5, 6, 7} and
  the receiver is immutable, emit a warning diagnostic.
- The warning carries the resolution tier so users can decide whether
  to write an override or accept the call.

Tests: fixture-driven, both with and without the flag.

Exit criteria: warning fires only on heuristic-classified non-mutating
calls; never fires on `@esctype`, strong signals, or overrides.

## Phase 8 — Argument mutation refinement (deferred)

Goal: tighten the "all params default to mutating" decision via
overrides, using MDN as source of truth.

- Extend the override schema with per-parameter mutability entries.
- Backfill the standard-library override file with the documented
  cases (`Array.prototype.map` callback receiver: non-mutating;
  `Object.assign` target: mutating; etc.).

Deferred from the initial milestone; tracked here so the schema in
Phase 0 leaves room for it.

## Cross-cutting concerns

- **Performance.** Override-file load happens once per compilation;
  the lookup is per-symbol and runs during interop conversion.
  Expected to be negligible. Add a benchmark only if it shows up in
  profiles.
- **Diagnostics.** Every classification carries the resolution tier so
  diagnostics can explain *why* a method was treated as (non-)mutating.
- **Documentation.** User-facing docs land alongside Phase 6 (so the
  `.d.ts` interop story is explained at the same time it works).

## Open implementation questions

- Should the override file live in the consuming project's repo, in
  `node_modules/<lib>/`, or both? Probably both, with an explicit
  precedence (consuming-repo wins).
- `*/` inside a string literal in an `@esctype` value would prematurely
  terminate the surrounding `/* */` comment. Settle in Phase 0:
  `*\/` escape on emit, or use line-comment blocks where supported.
- Does the Escalier type printer already produce a parseable
  representation, or do we need a stable serialization format
  separate from the human-readable one? Worth checking before Phase 6.
