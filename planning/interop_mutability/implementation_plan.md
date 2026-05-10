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
  [requirements.md](requirements.md) — three-location walk (shipped
  tier 4 → dep `node_modules/*/overrides/` tier 1a → consuming
  project tier 1b), recursive within each, subdirectory layout has
  no semantic effect. "Root of a package" means the directory
  containing `package.json`.
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
  exercises tiers 2–8 (everything below user overrides). Captures
  current behavior; will flip to passing as later phases land.
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
- Add `declare namespace <ident> { <decl>* }` to the grammar
  (supports nesting per the requirements).
- Accept an optional `override` prefix on top-level `declare module`
  / `declare global` / `declare namespace` and on the existing
  top-level declare forms (`declare class`, `declare interface`,
  `declare fn`, `declare type`, `declare val`).
- Inside an `override declare ...` block, treat `override` and
  `declare` as implied on contained declarations and reject them as
  parse errors if repeated (matches TS's `declare module` behavior).
- Accept computed member names in override class/interface/namespace
  bodies in two shapes:
  - Qualified identifier path (e.g. `[Symbol.iterator]`,
    `[MyLib.tag]`, `[a.b.c.key]`).
  - String-literal key (e.g. `["foo bar"]`).
  Other expression shapes are rejected at parse time.
- Plumb an `Override bool` flag through the affected AST nodes
  alongside the existing `Declare bool`.
- **Future-extensibility note.** The function-signature grammar used
  inside override blocks must be the full Escalier signature
  grammar, not a stripped-down receiver-only subset — so lifetime
  parameters and `throws` clauses can be added in later phases
  without a syntax break (per requirements §"Scope and future
  extensibility"). Concretely: the parser must already accept these
  forms, even though Phase 1 only consumes the receiver mode.
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
  At this phase the function only implements tier 3 (the existing
  `readonly`-property handling, which is part of "Explicit author
  signals" in the renumbered ladder) and tier 8 (default to
  mutating).
- Snapshot tests in `internal/interop/` verify no behavior change.

Exit criteria: `Classify` is the single decision point; `go test ./...`
green with no snapshot churn.

## Phase 2 — Strong signals (resolution-order tier 3)

Goal: cover all explicit author signals that don't require external
data.

Implement, in `internal/interop/mutability.go`, classification for:

- `Readonly<T>` / `ReadonlyArray<T>` / `ReadonlySet<T>` /
  `ReadonlyMap<T>` — drives tier-3 classification. Requires
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

## Phase 3 — Override file format, loader & merge

Goal: build the eager-merge pipeline (Approach A, per requirements
§"Implementation decision") that Phase 4 (shipped overrides) and the
user override file feature both depend on.

- Implement the format chosen in Phase 0 in a new
  `internal/interop/overrides/` subpackage (or inline if small).
- **Discovery & loading.** Walk the three locations in the
  precedence order from requirements:
  - Tier 4: shipped overrides bundled with the compiler.
  - Tier 1a: `node_modules/<dep>/overrides/**/*.esc`.
  - Tier 1b: consuming project's own `overrides/**/*.esc`.
  Within tier 1, 1b wins over 1a on conflict; both win over
  tier 4. Within a tier, duplicate-member declarations across files
  are a hard error (per `override_merge_semantics.md`).
- **Eager merge pass.** After both `.d.ts` and override `.esc` are
  parsed, run a merge keyed on `(module/namespace, qualified-name,
  kind)` that produces a fresh effective type. Originals are not
  mutated. Apply per-member rules from
  `override_merge_semantics.md`: member-presence (override on a
  nonexistent member is an error), overload collapsing,
  override-defined overloads, static/instance separation,
  getter/setter independence, generics arity matching.
- **Signature-shape consistency check.** During merge, verify that
  every override signature matches the upstream `.d.ts`'s arity,
  non-receiver parameter types, and return type (per requirements
  §"Consistency with upstream `.d.ts`"). Mismatch is a hard merge
  error reporting the specific member and which field disagrees.
  This runs from Phase 1 onward even though Phase 1 only consumes
  the receiver mode.
- **Provenance.** Every effective type records its provenance —
  origin `.d.ts` location plus the specific override entries that
  contributed — for use in diagnostics and the uncertainty warning.
- **Resolver.** Given a fully-qualified TS symbol path
  (`module#Class.prototype.method`), return the merged effective
  type plus the source tier of the mutability classification.
- `Classify` consults user overrides first (tier 1) and shipped
  overrides at tier 4; the existing tier-3 strong signals fit
  between them.

Tests:
- Load a synthetic override file; verify merge result matches a
  hand-written expected effective type.
- Verify tier 1b wins over tier 1a, and both win over tier 4.
- Verify hard errors on: nonexistent-member override; arity
  mismatch; param-type mismatch; return-type mismatch; duplicate
  member declarations across files in the same tier.
- Verify overload collapsing and override-defined-overload semantics.

Exit criteria: loader + merge covered by unit tests; `Classify`
consults the merged store but no overrides are shipped yet.

## Phase 4 — Shipped overrides

Goal: author the data tables that the resolver loads at startup.

Three sub-tasks, the first two parallelizable:

- **Built-ins** — classes that don't ship a `Readonly*` variant in
  TypeScript's lib files and therefore can't be classified by tier
  3 alone: `Date`, `RegExp`, `Promise`, `Error`, typed arrays
  (`Int8Array` etc.), `URL`, `URLSearchParams`, `WeakRef`, `WeakMap`,
  `WeakSet`, iterator / generator protocols. Source of truth: MDN.
  Coverage tracked in a checklist in this file as entries are added.

  `Array` / `Map` / `Set` are **not** in this list — TypeScript
  ships `ReadonlyArray` / `ReadonlyMap` / `ReadonlySet` alongside
  the mutable variants, so principle #2 (tier 3) handles them
  directly. The ES2023 `toSorted` / `toReversed` / `toSpliced` /
  `with` methods appear on `ReadonlyArray` in lib.es2023, so they
  classify as non-mutating without an override entry.

  **Layout: group by ECMAScript spec revision**, mirroring
  TypeScript's `lib.es*.d.ts` split. A symbol introduced in a given
  revision lives in that revision's directory:

  ```
  overrides/builtins/
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
      // (none today — Array additions are tier 3)
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
  non-mutating in receiver and arguments; one blanket entry
  (`@all_pure` per `override_merge_semantics.md`) per module rather
  than per-method.

- **Consistency test against upstream `.d.ts`.** Test in the
  compiler suite that runs over every shipped override entry where
  a corresponding upstream `.d.ts` exists:
  - Built-in symbols → bundled TS lib `.d.ts` set, version pinned
    to the compiler's TS lib version.
  - FP / immutability libraries → corresponding `@types/*` package
    (or the library's own bundled types), pinned alongside the
    shipped override.
  For each entry, look up the upstream declaration, compare
  non-receiver arity / parameter types / return type under the
  same mapping the merge uses, and fail the build on divergence.
  Libraries that ship no upstream types are exempt by definition.
  Bumping any pinned version surfaces drift as a deliberate
  fix-up step.

Tests: fixture per library that imports a representative subset and
checks the classification; the consistency test itself runs as part
of `go test ./...`.

Exit criteria: built-in counter-examples (`Date.setHours` mutates,
`toSorted` doesn't, `Object.assign` mutates target) all classify
correctly; consistency test green against pinned upstream versions.

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
  Escalier type parser, return as the tier-2 classification (and as
  the full Escalier type, bypassing tiers 3–8). User overrides
  (tier 1) still outrank `@esctype` — the override merge runs
  first, and `@esctype` is consulted only if no user override
  matched. Hand-authored `@esctype` on a TS-author-written `.d.ts`
  is consumed identically to round-tripped tags.
- Ship a `tsdoc.json` template documenting `@esctype` so consumers'
  TSDoc tooling doesn't flag it as unknown.
- Round-trip fixture: `.esc` → `.d.ts` → consumed back as Escalier;
  diff the two type printouts, expect identical.
- User-override-beats-`@esctype` fixture: a vendored `.d.ts` with
  an `@esctype` tag plus a user override for the same symbol;
  verify the user override wins and the symbol is classified as
  explicit (not uncertain).

Exit criteria: round-trip fixture passes; user-override-precedence
fixture passes; classification tier 2 wired in.

## Phase 7 — `implements` mutability conformance

Goal: enforce that a class implementing an interface matches the
interface's mutability annotations on each implemented method (per
requirements §"Policy decisions" — `implements` requires mutability
conformance).

- In `internal/checker/`, at the point where `implements` clauses
  are resolved, walk each interface member and find the
  corresponding class member.
- Compare receiver mutability (`self` vs `mut self`) post-merge —
  i.e. after override resolution and `@esctype` consumption have
  produced the effective type for both class and interface.
- On mismatch, emit a hard conformance error reporting which member
  diverges and which side has which annotation. Same error class as
  return-type / arity mismatch.
- Member resolution itself is unchanged: `getObjectAccess` still
  doesn't walk `Implements`. This phase only adds a check, not new
  resolution behavior.
- Where the class or interface side gets its mutability from a
  heuristic (tiers 5–7), the check still runs against the resolved
  classification. This is the one place a heuristic can produce a
  hard error rather than the uncertainty warning; the diagnostic
  should suggest adding an explicit signal (override entry,
  `@esctype`, or `readonly this`) on the side that's wrong.

Tests: fixtures covering (a) class & interface agree, (b) class
mutates but interface declares `self` — error, (c) reverse, (d)
heuristic-classified mismatch — error with suggested fix in
diagnostic.

Exit criteria: conformance check is on by default (not gated by the
uncertainty flag); fixtures pass.

## Phase 8 — Uncertainty warning

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

## Phase 9 — Argument mutation refinement (deferred)

Goal: tighten the "all params default to mutating" decision via
overrides, using MDN as source of truth.

- Extend the override schema with per-parameter mutability entries.
- Backfill the built-in override file with the documented cases
  (`Array.prototype.map` callback receiver: non-mutating;
  `Object.assign` target: mutating; etc.).

Deferred from the initial milestone; tracked here so the schema in
Phase 0 leaves room for it. Lifetime annotations and `throws`
clauses (per requirements §"Scope and future extensibility") are
also deferred follow-ons that reuse the merge machinery; they slot
in here as additional override-payload fields rather than new
top-level forms.

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

- `*/` inside a string literal in an `@esctype` value would prematurely
  terminate the surrounding `/* */` comment. Settle in Phase 0:
  `*\/` escape on emit, or use line-comment blocks where supported.
- Does the Escalier type printer already produce a parseable
  representation, or do we need a stable serialization format
  separate from the human-readable one? Worth checking before Phase 6.
- How should the consistency test (Phase 4) source the pinned
  `@types/*` tarballs at test time — vendored under `testdata/`,
  fetched once and cached, or shelled out to `npm pack`? Pick during
  Phase 4; vendoring is the simplest default but inflates repo size.

(Resolved: override-file location precedence — see requirements
§"Override file format". Tier 1b consuming-project wins over
tier 1a `node_modules/<dep>/overrides/`; both win over tier 4
shipped.)
