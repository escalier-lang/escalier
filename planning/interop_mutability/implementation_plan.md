# TypeScript Interop Mutability: Implementation Plan

This plan implements the requirements in [requirements.md](requirements.md).
Phases are ordered so each one is independently testable and merges
without requiring the next to be in place. Within a phase, work items
list the touch points in the existing codebase.

## Implementation order and status

Section numbering loosely follows the order of work, but a few
sections are gated on others and a few have already started. The
table below makes both explicit. Status legend: ✅ done,
🚧 partial, ⬜ not started.

| §   | Topic                                  | Status | Depends on  | Notes                                                                                                                                                                                                                                                                                          |
| --- | -------------------------------------- | ------ | ----------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | Existing surface area                  | ✅      | —           | Descriptive only.                                                                                                                                                                                                                                                                              |
| 2.1 | Spec scaffolding                       | ✅      | —           | `override` keyword, fixtures, merge-semantics doc, `@esctype` grammar.                                                                                                                                                                                                                         |
| 2.2 | Parser sub-task                        | ✅      | 2.1         | `declare module/global/namespace` and `override` prefix accepted by [internal/parser/decl.go](../../internal/parser/decl.go); `fixtures/interop_mutability/overrides/example.esc` is no longer `.future`.                                                                                       |
| 3   | Resolution-order plumbing              | ✅     | 2.2         | `ResolutionTier` enum and `Classify` entry point in [internal/interop/mutability.go](../../internal/interop/mutability.go) use the 7-tier ladder (`TierUserSource=0` sentinel, `TierUserOverride=1`, `TierEsctype=2`, `TierExplicitSignal=3`, `TierBuiltinOverride=4`, `TierGetPrefix=5`, `TierNameHeuristic=6`, `TierDefault=7`). All decision sites in `decl.go`/`helper.go` route through `Classify`. |
| 4   | Strong signals (tier 3)                | ✅     | 3           | `classifyExplicitSignal` in [internal/interop/mutability.go](../../internal/interop/mutability.go) handles getters/setters, `readonly` props, `Readonly<T>`/`ReadonlyArray<T>`/`ReadonlySet<T>`/`ReadonlyMap<T>` wrappers, `this: Readonly<T>` (incl. `readonly T[]`), Readonly-prefixed collection classes, and the well-known-symbol allow-list. End-to-end coverage in `TestClassifyTier3_EndToEnd`. Known parser gap (separate from §4): `dts_parser` does not yet parse `[Symbol.iterator]()` as a computed method name — it treats `[` at member position as an index signature. The classifier already handles `ComputedKey` correctly. |
| 5   | Override file format, loader, merge    | 🚧     | 2.2, 3      | Core pipeline landed across PRs #606–#609 plus the 5.A "section 6 blockers" commit: data types, extract, merge & loader, checker wiring, and the trio-fusion / static-side lookup prerequisites for §6 are all in. Remaining: §5.13 Group B (property-type consistency on non-function leaves) and Group C (lifetime-erased equivalence, type/value namespace split in `Container.Free`). |
| 6   | Built-in overrides                      | 🚧     | 5 (Group B optional) | Per-class authoring of stdlib + FP-library overrides. Includes the always-immutable built-ins (`Number`/`BigInt`/`String`/`Boolean`/`Symbol`/`Promise`). §5.13 Group A prerequisites are done; Group B (property-type consistency) is recommended alongside but not strictly blocking. **Stop-gap landed with #612**: `prelude.go`'s legacy `mutabilityOverrides` Go map has been extended with bootstrap entries for `Date`, `Function`, `Console`, `Body`, `Response`, `Request` (in addition to the pre-existing `String`/`Number`/`Boolean`/`RegExp`) so that the polarity flip didn't render their methods invisible. These are bootstrap stop-gaps that the §6 `.esc` override pipeline should formally supersede. |
| 7   | Heuristics (tiers 5–6)                 | 🚧     | 3           | `classifyGetPrefix` (tier 5) and `classifyNameHeuristic` (tier 6) in [internal/interop/mutability.go](../../internal/interop/mutability.go) implement the full requirements prefix/exact-match lists with mutating-wins-on-conflict and `getOr{Insert,Update,Create}` fall-through. Inheritance fallthrough wired via optional `ClassifyContext.Base`: when all per-class tiers miss, `Classify` recurses on the base context; tests cover explicit-on-base, heuristic-on-base, default fall-through, and subclass-wins. Pending: plumb `Base` from `decl.go`/`helper.go` call sites (needs class-name → declaration lookup, blocked on §5 override-store path conventions). |
| 8   | Type-printer round-trip audit          | ⬜      | —           | Independent; prerequisite for §9 emit. Can be done at any time.                                                                                                                                                                                                                                |
| 9   | `@esctype` round-trip                  | ⬜      | 3, 5, 8     | Emit side needs §8; consume side needs parser TSDoc retention (§9.2); integration needs §5.                                                                                                                                                                                                    |
| 10  | `implements` mutability conformance    | 🚧     | —           | Lean check **already done**: `selfReceiverCompatible` in [internal/checker/check_implements.go:247](../../internal/checker/check_implements.go#L247) implements bidirectional `ReceiverIsMut` equality and is wired in at all three method-comparison sites. Currently emits the generic `mismatchedMember` error. |
| —   | §10 diagnostic richness                | ⬜      | 3, 5, 7, 11.2 | Replace generic error with `ImplementsMutabilityMismatchError` carrying tier sources + provenance; add the "add an explicit signal" suggestion clause. Purely additive on top of the lean check.                                                                                            |
| 11.2 | `SelfParam.Source` plumbing           | ⬜      | 3           | Small data-layout change: add `Source ResolutionTier` to `FuncParam`; set from `decl.go` when building types from `ClassifyResult`.                                                                                                                                                            |
| 11  | Uncertainty warning                    | ⬜      | 3, 5, 7, 11.2 | The `--warn-uncertain-mutability` flag, `UncertainMutabilityWarning` error variant, and the call-site predicate.                                                                                                                                                                            |
| 12  | Argument-mutation refinement           | ⬜      | 5           | Deferred from the initial milestone. Reuses §5 merge machinery.                                                                                                                                                                                                                                |
| 13  | Cross-cutting concerns                 | —      | —           | Performance, diagnostics, docs. Tracked alongside the relevant section as it lands.                                                                                                                                                                                                            |
| 14  | Open implementation questions          | ✅      | —           | None outstanding.                                                                                                                                                                                                                                                                              |

**Recommended order of attack** (assuming the partial work above
is finished first):

1. **§3 renumbering.** Finalise the 7-tier ladder in
   `mutability.go` so downstream code targets the right names.
   Drop `TierPrimitiveWrapper`. Small, fast.
2. **§4 cleanup.** Rename `classifyTier2` → `classifyTier3`,
   finish remaining tier-3 cases.
3. **§5 (override system).** Largest chunk; unblocks §6, §9, §11. Core pipeline + §6 prerequisites are landed (PRs #606–#609 and the 5.A blockers commit); remaining work is §5.13 Group B/C follow-ups.
4. **§6 (built-in overrides).** Author the stdlib + FP data,
   including the always-immutable built-ins.
5. **§7 (heuristics).** Add `classifyTier5` (get-prefix) and
   `classifyTier6` (name-based) plus inheritance fallthrough.
6. **§11.2 (`SelfParam.Source`).** Small data-layout change.
7. **§11 (uncertainty warning).** Needs §3, §5, §7, §11.2.
8. **§10 diagnostic richness.** Replace the generic
   `mismatchedMember` call with the dedicated error type.
9. **§8 (printer audit).** Independent; can land any time, but
   needed before §9 emit.
10. **§9 (`@esctype` round-trip).** Last; needs §3, §5, §8.

§8 and the existing lean §10 are merge-safe today, so other
phases don't need to wait on either.

## 1. Existing surface area to extend

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

## 2. Spec & test scaffolding

Goal: lock the override-file format and the `@esctype` grammar before
writing code that depends on either.

### 2.1 Done

- **`override` keyword** added to the lexer
  ([token.go](../../internal/parser/token.go),
  [lexer.go](../../internal/parser/lexer.go),
  [expect.go](../../internal/parser/expect.go)). Tests pass; no
  conflicts with existing identifiers.
- **`overrides/` discovery rule** specified in
  [requirements.md](requirements.md) — three-location walk (built-in
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
  object types nest naturally. `*/` appearing inside a string-literal
  or template-literal type would prematurely terminate the
  surrounding `/* */` comment at the JS lexer level (no JSDoc/TSDoc
  tool can recover what the lexer has already truncated). Emit as
  `*\/` — the backslash breaks the `*/` byte match without changing
  the logical string content — and undo on consume. Verify against
  `tsc`'s `.d.ts` emit during §9 to confirm the escape
  round-trips through downstream tooling unchanged.
- **Resolution-order fixture** —
  [fixtures/interop_mutability/](../../fixtures/interop_mutability/)
  exercises tiers 2–8 (everything below user overrides). Captures
  current behavior; will flip to passing as later phases land.
- **Override-merge fixture placeholder** —
  [fixtures/interop_mutability/overrides/example.esc.future](../../fixtures/interop_mutability/overrides/example.esc.future)
  shows the intended syntax. Renamed from `.esc` so the build
  harness ignores it until the parser sub-task below lands.

### 2.2 Remaining — parser sub-task

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
- **Sugar desugaring.** Per
  [override_merge_semantics.md](override_merge_semantics.md)
  §"Top-level matching", `override declare class Foo { ... }` and
  `override declare interface Foo { ... }` at file root are sugar
  for `override declare global { class Foo { ... } }` (and the
  interface equivalent). The same applies to top-level
  `override declare fn` / `override declare type` / `override
  declare val` — they target the global scope. Desugaring happens
  in the parser: the resulting AST has a single canonical shape
  (an `override declare global { ... }` block whose body contains
  the sugared decl), so downstream consumers don't need to know
  the sugar existed. This keeps §5's loader / merge logic from
  branching on top-level form.
- **Future-extensibility note.** The function-signature grammar used
  inside override blocks must be the full Escalier signature
  grammar, not a stripped-down receiver-only subset — so lifetime
  parameters and `throws` clauses can be added in later phases
  without a syntax break (per requirements §"Scope and future
  extensibility"). Concretely: the parser must already accept these
  forms, even though §3 only consumes the receiver mode.
- Once parsing works, rename
  `fixtures/interop_mutability/overrides/example.esc.future` to
  `.esc` and snapshot.

Exit criteria for §2 overall: all of the above. §2.2 is the
gate before §3 can rely on the resolver finding override
declarations at all.

## 3. Resolution-order plumbing in `interop`

Goal: introduce the seven-tier resolution order from the requirements
as a single function, wired in but populated only with existing
behavior (so output is unchanged).

- Add `internal/interop/mutability.go` defining the `ResolutionTier`
  enum (the canonical 7-tier ladder, plus a zero-valued
  "user-authored source" sentinel — see §11.2):

  ```go
  type ResolutionTier int

  const (
      TierUserSource        ResolutionTier = iota // 0: user-authored .esc source
      TierUserOverride                            // 1: requirements tier 1
      TierEsctype                                 // 2
      TierExplicitSignal                          // 3
      TierBuiltinOverride                         // 4
      TierGetPrefix                               // 5
      TierNameHeuristic                           // 6
      TierDefault                                 // 7
  )
  ```

  Plus a `Classify` entry point that takes the TS-side declaration
  plus surrounding context (class shape, module path) and returns
  a `ClassifyResult` (see §9.3). The `Source` field of the result
  is one of the constants above and is needed by §10's conformance
  check and §11's uncertainty warning.
- Route every place in `interop/decl.go` and `interop/helper.go` that
  currently decides method/parameter mutability through `Classify`.
  At this phase `Classify` implements only tier 7 (default to
  mutating) — strong signals land in §4, the override store in §5,
  `@esctype` in §9, and the heuristics in §7. The existing
  `readonly`-property handling is a *property-write* check, separate
  from receiver-mutability classification; it stays where it is and
  §4 will consolidate it.
- Snapshot tests in `internal/interop/` verify no behavior change.

Exit criteria: `Classify` is the single decision point; `go test ./...`
green with no snapshot churn.

## 4. Strong signals (resolution-order tier 3)

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

Tests: extend the §2 resolution-order fixture with strong-signal
cases; add table-driven unit tests in
`internal/interop/mutability_test.go`.

Exit criteria: every strong-signal case in the §2 resolution-order
fixture classifies correctly, with `Source = TierExplicitSignal`.

### 4.1 Iterator-protocol workaround (post-#612 stop-gap)

The polarity flip in #612 defaults every `.d.ts`-loaded method's
receiver to `mut self`. `classifyExplicitSignal` (tier 3) is the
right home for "iterator-protocol methods are non-mutating on the
source" classification — both the well-known-symbol methods
(`[Symbol.iterator]`, `[Symbol.asyncIterator]`) and the protocol's
own `.next` / `.return` / `.throw` calls — but the checker's
iterator entry points (`getMemberType` lookups in
`GetIterableElementType`, `GetIteratorReturnType`,
`GetAsyncIterableElementType`, and `unifyIteratorNextReturn`) run
before tier-3 classification is wired into the prelude. Without a
workaround the polarity flip would break the iterator protocol for
every non-mut iterable (Generator, String, user types without a
`Readonly*` pair, etc.).

Current stop-gap (`internal/checker/iterable.go`): an
`asMutReceiver` helper wraps the source type in `MutType` before
each iterator-protocol member lookup. This bypasses
`isMemberVisible`'s mutability gate for the protocol calls only,
preserving the rest of the polarity flip's loud-failure behaviour.

The proper fix lives in this section: once `classifyExplicitSignal`
results flow into the prelude's `SelfParam` assignment, the wrap can
be removed and `iterable.go` can do the lookups with their natural
receiver mutability.

## 5. Override file format, loader & merge

Goal: build the eager-merge pipeline (Approach A, per requirements
§"Implementation decision") that §6 (built-in overrides) and the
user override file feature both depend on.

### 5.1 Merge model

The override system applies **shadowing, not blending**. At any
given slot (one kind/name combination on one owner), at most one
override entry contributes to the final type — competing entries
from other tiers are dropped, not merged with it.

A **slot is the full overload set**, not an individual signature.
For a method or top-level function with N original overloads,
the slot holds all N together (carried as a single
`Effective.Type` whose underlying type packs every signature); an
override entry for that name declares the entire replacement set
(M overloads, where M may differ from N), and §5.6 substitutes
the override's set for the original's entirely. Free functions live in a `Container`
(§5.3) — at module top level or inside a nested namespace — and
the slot rule is identical: one `*Effective` per name, holding
the full overload set.

A consequence: **partial override of an overload set is not
supported.** If a user wants to adjust mutability on one of three
original overloads, they must redeclare all three in their
override file. §5.7's `consistency.CheckSet` then verifies that
overload counts match and every paired signature has the same
non-mutability shape (arity, parameter types, return type) as
the original — any mismatch is a hard error, not a silent merge.

There are two layers of competition:

1. **Within the override system** — three internal tiers
   (`OverrideTierUserProject` > `OverrideTierUserDep` >
   `OverrideTierBuiltin`). The loader builds one module map per
   tier (a `map[string]*ModuleScope`).
   The merge collapses them into a single override scope by walking
   slot-by-slot and keeping the highest-precedence non-empty entry.
   See §5.5 step 6 for the explicit collapse step.
2. **Override system vs. the original `.d.ts`** — the collapsed
   override scope is then merged with the original scope,
   slot-by-slot. A slot present on the override side replaces
   the original's type for that slot entirely; a slot only on the
   original side passes through with no `Source` stamped (leaving
   classification to `Classify`'s later tiers).

Naming convention: `OverrideTier` is the *internal* three-tier enum
used only inside `internal/interop`. `ResolutionTier`
(defined in `internal/interop/mutability.go`) is the broader 7-tier
ladder. The two never appear in the same field or function
parameter; the merge translates between them when it builds each
leaf.

After merge, `Effective.Source` records which tier of the broader
7-tier resolution ladder produced the value: `TierUserOverride`
(ladder tier 1) when a user-project or user-dep entry won, or
`TierBuiltinOverride` (ladder tier 4) when only a built-in entry was
present. `Classify` reads this value to decide where in the ladder
the override fits, and §11's warning predicate reads it to
suppress warnings on override-backed classifications.

**Duplicate-detection rules:**

- *Within an `OverrideTier`:* two entries occupying the same slot
  is a hard error (`ErrDuplicateMember`, carrying both `Origin`s).
  This is what enforces "one source of truth per tier" — you can't
  have two `overrides/*.esc` files in the same project both
  claiming `Array.prototype.map`. A class and a namespace declared
  under the same name in the same module also collides on the
  same `Container.Children` slot; namespace+class declaration
  merging (legacy TS) is not supported.
- *Across `OverrideTier`s:* the lower-precedence entry is silently
  dropped. This is the whole point of the tier system — user
  overrides shadow built-in ones without complaint.

The same shadowing applies to `@all_pure`: a module pragma at a
higher tier wins over the same pragma at a lower tier, but does
not stack with per-member overrides — explicit per-member entries
at any tier always beat the synthesised "non-mutating by default"
leaves from a lower-tier `@all_pure`.

### 5.2 Package layout

Override `.esc` files are ordinary Escalier source, so the
ambient declarations they contain are converted to
`type_system.Type` values by **reusing the existing checker
pipeline** (the same pipeline that already handles `.d.ts`
interop via `interop.ConvertModule` → `Checker.InferModule`).
No new `ast.Decl → type_system.Type` converter is written: every
existing inference helper (`inferTypeAnn`, `inferFuncSig`,
`inferTypeDecl`, `inferInterface`, etc.) applies directly.

The whole override system lives in `internal/interop/`. Note
that this is possible because `Build` does *not* import
`checker` directly — it takes a `TypeChecker` function-type
callback that the consumer (in `internal/checker/`) plugs in.
The callback seam sidesteps the cycle that would otherwise
arise (`checker` already imports `interop` for
`interop.ConvertModule`), so no subpackage is needed.

Files added to `internal/interop/`:

- `store.go` — `OverrideStore`, `ModuleScope`, `ChildScope`,
  `Container`, `MemberSet`, `Effective`, `Origin`, `Path`,
  `OverrideTier`, `MemberKind` plus `Resolve` (§5.10) and
  canonical-name helpers (§5.4).
- `errors.go` — typed merge errors (§5.8).
- `consistency.go` — signature-shape consistency check
  (§5.7).
- `extract.go` — "shape extractor" that walks the parsed
  override AST alongside the checker-produced
  `*type_system.Namespace` to build the `ModuleScope` /
  `ChildScope` / `MemberSet` tree (the namespace alone does
  not preserve method/getter/setter slot independence — the
  AST does).
- `merge.go` — `Collapse` (three-tier shadowing) and `Merge`
  (eager merge of override scopes onto the original `.d.ts`
  scope) — §5.6.
- `loader.go` — `Discover` (filesystem walk + parse) and
  `Build` (full pipeline: discover → typecheck via injected
  `TypeChecker` → extract → collapse → merge).
- `store_test.go`, `consistency_test.go`, `merge_test.go`,
  `loader_test.go`, `extract_test.go` — unit coverage.

External callers reference `interop.OverrideStore`,
`interop.Build`, `interop.Origin`, and so on. `Classify` reads
`*OverrideStore` through `ClassifyContext.Store`; the store
itself is built by `interop.Build` invoked from
`internal/checker/`.

**Checker hook.** The checker currently no-ops on
`*ast.DeclareModuleDecl`, `*ast.DeclareGlobalDecl`, and
`*ast.NamespaceDecl` (see
[infer_stmt.go](../../internal/checker/infer_stmt.go) — the
`return []Error{}` branch). When `Override()` is true on one
of these, the checker must instead descend into the block
and infer the contained declarations into the surrounding
namespace, exactly as it does for top-level declarations. The
hook is small (one switch arm wired to the existing per-decl
helpers) and is the only checker change §5 requires.

**Sequencing.** Override decls reference types declared in
the original `.d.ts` (e.g. an override of `Array.prototype.map`
mentions `Array<T>`). The loader must therefore run *after*
the relevant `.d.ts` has been inferred, so the global /
package scope is populated. Concretely: `interop.Build` is
invoked from the same call site that wires `.d.ts` inference
into the checker (today
[infer_import.go](../../internal/checker/infer_import.go)),
after the package and global namespaces are in place but
before user-source inference starts.

### 5.3 Core data types

The shape is a **scope tree** mirroring the original `.d.ts` nesting
(module → namespace/class → instance/static → kind → name), not a
flat map. Every map key is a plain string, so the `QualIdent`
map-key problem doesn't arise; lookups walk the tree.

```go
// OverrideStore holds the post-merge module map. Per-tier
// pre-merge module maps exist only inside Build (used to run
// within-tier duplicate detection and the slot-by-slot
// collapse of §5.5) and are not retained on the store —
// every diagnostic that needs provenance reads
// Effective.Provenance, which already carries the
// contributing Origins.
type OverrideStore struct {
    // Merged across all OverrideTiers + the original. Key is the
    // module specifier ("" = global; "lodash", "fs", etc.).
    Modules map[string]*ModuleScope
}

// Resolve walks Modules by Path. Returns nil if the path has no
// override (the caller falls through to lower tiers in Classify).
func (s *OverrideStore) Resolve(p Path) *Effective

// Container holds the slots populated by modules and namespaces:
// free-function-style entries plus a map of nested children. Both
// ModuleScope and ChildScope embed it so namespace-nested
// functions land in the same slot as module-top-level functions.
// On a class/interface ChildScope, both fields stay empty (those
// shapes use Instance/Static instead).
type Container struct {
    Free   map[string]*Effective  // free functions, vals, type aliases
    Children map[string]*ChildScope // nested namespaces / classes / interfaces
    Origin Origin                 // declaring file for diagnostics
}

type ModuleScope struct {
    Container
    AllPure bool // module-level @all_pure pragma
}

// ChildScope is a namespace, class, or interface. Namespaces use
// only Container.Free and Container.Children; classes/interfaces
// use Instance and Static. The two shapes are mutually exclusive
// — namespace+class declaration merging (a legacy TS feature) is
// not supported; a class and a namespace declared under the same
// name in the same module is an `ErrDuplicateMember`.
type ChildScope struct {
    Container
    Instance *MemberSet // nil for namespaces
    Static   *MemberSet // nil for namespaces
}

// MemberSet groups the four independent slots that share a name
// space within a class/interface (per override_merge_semantics.md:
// getter/setter independence, instance/static separation).
type MemberSet struct {
    Methods    map[string]*Effective // names canonicalised: "foo",
                                     // "[Symbol.iterator]", "[\"foo bar\"]"
    Getters    map[string]*Effective
    Setters    map[string]*Effective
    Properties map[string]*Effective
    Ctor       *Effective            // single slot per class
}

// OverrideTier identifies where an override came from. Distinct
// from interop.ResolutionTier (the 7-tier classification ladder)
// — OverrideTier is only used inside this package to drive the
// internal three-tier collapse (§5.5). The merge translates the
// winning OverrideTier into a ResolutionTier on the resulting
// Effective.Source.
//
// Lower integer = higher precedence (UserProject beats UserDep
// beats Built-in). This matches the convention used by
// ResolutionTier where TierUserOverride = 1 outranks
// TierBuiltinOverride = 4.
type OverrideTier int
const (
    OverrideTierUserProject OverrideTier = iota // requirements tier 1b
    OverrideTierUserDep                         // requirements tier 1a
    OverrideTierBuiltin                         // requirements tier 4
)

// Effective is the merged result for a single member. It carries
// no key — its location in the tree is the key. Receiver shape
// (no receiver / self / mut self) is encoded structurally on
// Type's *FuncType.SelfParam (nil = no receiver; non-nil with
// SelfParam.Type wrapped in *MutType = mut self; non-nil
// otherwise = self). Callers use type_system.ReceiverIsMut(fn)
// rather than a separate flag.
type Effective struct {
    Type       type_system.Type       // post-merge type
    Source     interop.ResolutionTier
    Provenance []Origin               // contributing files
}

type Origin struct {
    Kind     OriginKind // OriginalDTS | OverrideFile
    FilePath string
    Span     ast.Span
}

// Path is the structured address of a member, used for resolver
// queries and diagnostics. Mirrors the tree walk.
type Path struct {
    Module string                 // "" for global
    Owner  dts_parser.QualIdent   // nil for module-free decls; dotted
                                  // QualIdent for nested namespaces/classes
    Name   dts_parser.PropertyKey // ident or computed key
    Kind   MemberKind             // Method | Getter | Setter | Property | Ctor | Free
    Static bool
}
```

### 5.4 Canonicalising member names

Computed keys (§2.2 accepts `[Symbol.iterator]` and `["foo bar"]`)
are stored in `MemberSet.Methods` (etc.) under a canonical string:

- Plain identifier `foo` → `"foo"`.
- Qualified path `Symbol.iterator` → `"[Symbol.iterator]"`.
- String literal `"foo bar"` → `"[\"foo bar\"]"`.

A `canonicalName(PropertyKey) string` helper lives next to the
override-tree types and is the single source of truth for this
mapping —
the override-parser, the original-AST consumer, and `Path → walk`
all call it.

### 5.5 Discovery, loading, and three-tier collapse

Entry point (in package `interop`):

```go
// TypeChecker is the dependency-injection seam that lets the
// loader invoke the checker without importing it (and thereby
// avoid the cycle that would arise — checker already imports
// interop). The consumer in internal/checker/ wires up a
// TypeChecker that runs InferModule against scopes populated
// with the original .d.ts symbols (§5.2 "Sequencing").
type TypeChecker func(p *ParsedOverride) (
    globalNs *type_system.Namespace,
    namedNs map[string]*type_system.Namespace,
    errs []error,
)

func Build(
    ctx context.Context,
    tc TypeChecker,
    root string,
    deps []DepInfo,
    builtin fs.FS,
    originals map[string]*ModuleScope,
) (*OverrideStore, []error)
```

1. Walk `builtin` (embedded `fs.FS` populated in §6) — emit
   `Entry`s with `OverrideTier = OverrideTierBuiltin`.
2. For each dep in `deps`, walk `<dep.Dir>/overrides/**/*.esc` — emit
   with `OverrideTier = OverrideTierUserDep`. `dep.Dir` is the
   directory containing that dep's `package.json`.
3. Walk `<root>/overrides/**/*.esc` — emit with
   `OverrideTier = OverrideTierUserProject`.
4. Parse each file via the existing `internal/parser` entry point
   (`parser.ParseLibFiles`); reject files with parse errors as
   hard errors.
5. **Type-check.** Drive `Checker.InferModule` (or a
   per-decl variant) over each parsed override file against
   the same package/global scopes the `.d.ts` populated in
   step 0 (the implicit prerequisite per §5.2 "Sequencing").
   The checker's `Override()`-aware branch (§5.2 "Checker
   hook") descends into `override declare module "..."`,
   `override declare global`, and `override declare
   namespace` blocks and infers the contained decls into a
   fresh `*type_system.Namespace`. Type-check errors are
   surfaced as hard errors — overrides whose types don't
   resolve are not silently dropped.
6. **Shape-extract.** Walk each override file's parsed AST in
   lockstep with the checker-produced `*type_system.Namespace`
   and build one `*ModuleScope` per `OverrideTier`. The AST
   walk decides which slot a declaration lands in (method vs.
   getter vs. setter vs. property, static vs. instance, free
   function vs. nested namespace member); the namespace
   supplies the typed `type_system.Type` for each slot. Within
   an `OverrideTier`, inserting into an already-occupied slot
   is `ErrDuplicateMember` (carries both files' `Origin`s).
   Cross-tier shadowing is handled by the collapse step below.
7. Collapse the three per-tier scopes into a single override
   scope by walking all three trees together, slot-by-slot:

   ```text
   collapsed[slot] := first non-nil of (
       userProject[slot],
       userDep[slot],
       builtin[slot])
   ```

   Concretely, the walk descends `Modules` → `Container` (`Free`
   and recursively `Children`) → (`Instance` | `Static`) →
   (`Methods` | `Getters` | `Setters` | `Properties` | `Ctor`)
   and takes the highest-precedence non-empty leaf at every key.
   `Container` lives on both `ModuleScope` and `ChildScope`
   (§5.3), so the same recursion handles namespace-nested free
   functions and module top-level free functions uniformly.
   `ModuleScope.AllPure` collapses the same way: a higher-tier
   `@all_pure` shadows a lower-tier one entirely (no stacking).

   The collapsed scope records, for each surviving leaf, the
   `OverrideTier` it came from on a transient field that §5.6
   reads when stamping `Effective.Source`. The per-tier scopes
   are dropped after collapse — they have no consumer past this
   point.

### 5.6 Eager merge pass

Lives in `internal/interop/merge.go`:

`Merge(original, override map[string]*ModuleScope) (*OverrideStore, []error)`:

The merge is a recursive tree walk: visit the original scope and
the collapsed override scope together, slot-by-slot (override
wins when present, original otherwise), and produce a fresh
`Effective` for every leaf. Per node:

- **Module level.** Walk the original `Modules`. For each module,
  recurse into its `Container` (see below). If the override side
  has `AllPure = true`, that's recorded on the resulting
  `ModuleScope` and consulted when each leaf is built (also below).
- **Container level.** Recurse into `Free` (entry-by-entry) and
  `Children`. The same recursion runs at module top level and
  inside every nested child, so namespace-nested free functions
  and module top-level free functions follow the same merge code
  path.
- **Child level.** After descending through `Container.Children`
  to reach a `ChildScope`, the embedded `Container` is already
  handled by the Container-level recursion above. The remaining
  work depends on shape: a namespace child has nil `Instance` and
  `Static`, so there's nothing further to do at the child level;
  a class/interface child has both `MemberSet`s populated, and
  recursion descends into each. Static/instance never merge into
  each other (they're separate fields). A `ChildScope` with both
  shapes populated indicates an `ErrDuplicateMember` collision
  upstream of merge — namespace+class declaration merging is not
  supported (§5.3).
- **MemberSet level.** Each kind (Methods/Getters/Setters/Properties)
  has its own slot — getter/setter independence falls out of the
  shape.
- **Leaf level.** Construct `Effective`:
  - If only the original side has it: leave `Source` unstamped
    (zero value), so `Classify`'s later tiers determine the final
    classification.
  - If only override and member-presence requires the original
    (`@all_pure` is the one case that doesn't): emit
    `ErrUnknownMember` with the available-name list pulled from
    the sibling slots in the original's MemberSet.
  - If both: apply overload collapsing (override's overload set
    replaces the original's entirely), run `consistency.CheckSet`
    over the paired overload arrays (§5.7), and emit the merged
    `Effective`.
  - If `ModuleScope.AllPure` is true and the slot is a method
    without an explicit override: synthesise an `Effective` whose
    `Type` is the original's `*FuncType` with the `*MutType`
    wrapper stripped from `SelfParam.Type` (so `ReceiverIsMut`
    reports false), and `Source` set per the contributing
    `OverrideTier` — `TierUserOverride` for UserProject/UserDep,
    `TierBuiltinOverride` for Built-in. Free functions and static
    methods (`SelfParam == nil`) are unaffected by `@all_pure`.

Generics arity is checked at two places:

- **Child-level generics** (class/interface type parameters) are
  checked when entering a `ChildScope`: if the override's child
  declares a different arity than the original, emit
  `ErrGenericArityMismatch{Path: Path{Owner: ...}, ...}` and skip
  merging that child's body.
- **Method-level generics** (per-signature type parameters on a
  method or free function) are checked inside
  `consistency.Check` (§5.7), since they're part of the
  per-signature equivalence contract.

Originals are not mutated; merge builds a fresh `OverrideStore`.

### 5.7 Signature-shape consistency check

Per §5.1, a slot holds the full overload set, and an override
replaces the original's set entirely. §5.7 enforces that the
replacement preserves the non-mutability shape of every signature.

Two entry points:

```go
// CheckSet verifies overload-set shape: counts match and each
// override signature is equivalent to the original's at the same
// index. Called from merge.Apply once per method/function slot.
func CheckSet(override, original []*type_system.FuncType, path Path) error

// Check is the per-signature helper used by CheckSet (and exposed
// directly for the §10 implements check, which compares single
// signatures).
func Check(override, original *type_system.FuncType, path Path) error
```

`CheckSet` rules:

- **Overload count.** `len(override) != len(original)` returns
  `ErrSignatureMismatch{Path, Field: "overload count", Override,
  Original}` with `Override`/`Original` carrying the two counts.
  No per-signature checks run when counts differ — there's no
  defensible pairing once arity disagrees, and the user must
  redeclare the full set anyway.
- **Pairing.** When counts match, signatures are paired
  **positionally** in source order (override's *i*-th declaration
  against the original's *i*-th). Position is the same key the
  `type_system.Type` representation uses to hold overloads, so no
  re-sorting is needed. Override authors are expected to mirror
  the original's declaration order; the §5.12 tests include a
  fixture exercising this.
- **Per signature.** `Check` runs for each pair and reports the
  first mismatch with `Field` extended to `"overload[i]/arity" |
  "overload[i]/param[j]" | "overload[i]/return"` so diagnostics
  point at the specific signature. For single-signature slots
  (no overloads) the bracketed prefix is omitted.

`Check` rules (per signature):

- Arity (excluding `this` receiver) must match.
- Each non-receiver parameter type must be equivalent.
- Return type must be equivalent.
- On mismatch return `ErrSignatureMismatch{Path, Field, Override,
  Original}` where `Field` is `"arity" | "param[i]" | "return"`.

The equivalence helper is a small wrapper rather than a direct call
to `FuncType.Equals`:

```go
// funcSignatureEquivalent compares two FuncTypes for the consistency
// contract: arity, per-position non-receiver param types, and return
// type. Parameter names are ignored, the `this` receiver (if any) is
// stripped before comparison, and SelfParam mode is intentionally
// excluded — that's the field the override is allowed to change.
func funcSignatureEquivalent(a, b *type_system.FuncType) (field string, ok bool)
```

Why a wrapper rather than `FuncType.Equals` directly: the consistency
contract is specific (names ignored, receiver excluded, SelfParam
allowed to differ), whereas `Equals` is a general type-system
predicate that might evolve to include fields we explicitly want to
ignore here.

This check runs unconditionally from §3 onward — earlier phases
just don't *consume* the non-receiver fields.

### 5.8 Error categories (in `errors.go`)

```go
type ErrDuplicateMember struct { Path Path; First, Second Origin }
type ErrUnknownMember   struct { Path Path; Override Origin
                                 Available []string } // siblings for "did you mean"
type ErrSignatureMismatch struct {
    Path     Path
    Field    string // "arity" | "param[0]" | ... | "return"
    Override string // pretty-printed override side
    Original string // pretty-printed original side
    OverrideOrigin Origin
}
type ErrGenericArityMismatch struct { Path Path; Override, Original int }
```

All implement `error` with messages that name the file and member.
Surfaced through the existing `internal/checker/error.go` error
channel (or `interop`'s, whichever owns interop-time diagnostics).

### 5.9 Diagnostic format

Every diagnostic that names a classified member appends its
provenance chain — the original `.d.ts` location plus each
override file that contributed. The chain is `Effective.Provenance`
rendered as `<file>:<line>` lines under the main message:

```text
warning: call to `foo.bar()` treats receiver as non-mutating ...
  at lib.es5.d.ts:1247
  overridden by overrides/builtins/es5.esc:42
```

Both the §10 and §11 diagnostics carry this chain via a
`Provenance []Origin` field copied from the `Effective` they were
derived from. Errors raised by the merge itself
(`ErrSignatureMismatch`, `ErrDuplicateMember`, etc.) already carry
explicit `Origin`s on their structs; the renderer formats them the
same way.

### 5.10 Resolver

`OverrideStore.Resolve(p Path) *Effective` — the single entry point
consulted by `Classify`. Implementation is a tree walk:

```go
func (s *OverrideStore) Resolve(p Path) *Effective {
    mod := s.Modules[p.Module]
    if mod == nil { return nil }

    // Locate the Container that holds this member. Free entries
    // can live either at module top level (p.Owner == nil) or
    // inside a namespace (p.Owner != nil), so resolve the
    // container first.
    container := &mod.Container
    var child *ChildScope
    if p.Owner != nil {
        child = walkChild(mod.Children, p.Owner)
        if child == nil { return nil }
        container = &child.Container
    }

    name := canonicalName(p.Name)
    if p.Kind == KindFree {
        return container.Free[name]
    }

    // Remaining kinds are class/interface members; require Owner.
    if child == nil { return nil }
    set := child.Instance; if p.Static { set = child.Static }
    if set == nil { return nil }
    switch p.Kind {
    case KindMethod:   return set.Methods[name]
    case KindGetter:   return set.Getters[name]
    case KindSetter:   return set.Setters[name]
    case KindProperty: return set.Properties[name]
    case KindCtor:     return set.Ctor
    }
    return nil
}
```

`walkChild` follows a `*Member` `QualIdent` left-to-right,
descending one nested child per segment via `Container.Children`
at each level. A nil `Path.Owner` means "module top level" — used
both for module-free decls (`KindFree`) and as the starting point
of any `QualIdent` walk.

### 5.11 `Classify` integration

`Classify` lives in `internal/interop/mutability.go`; the
store it consults is built upstream by `interop.Build`
(§5.2) and handed in via `ClassifyContext`.

Extend `ClassifyContext` with `Store *OverrideStore` (nil
is allowed and means "no overrides registered"). `Classify` calls
`Store.Resolve(path)` exactly once at the very top of the cascade.
The merge has already decided whether a user-project, user-dep, or
built-in entry won, and stamped that decision on `Effective.Source`
as either `TierUserOverride` or `TierBuiltinOverride`. So:

- If `Resolve` returns nil: fall through to tier 2 (`@esctype`).
- If `Resolve` returns an `Effective` with `Source =
  TierUserOverride`: the result lands at ladder tier 1.
- If `Resolve` returns an `Effective` with `Source =
  TierBuiltinOverride`: the result lands at ladder tier 4 — but
  *only* if no earlier tier (2 or 3) supersedes it. Concretely:
  `Classify` evaluates tier 1 (the `TierUserOverride` case), then
  tier 2 (`@esctype`), then tier 3 (strong signals), and only
  then accepts a `TierBuiltinOverride` hit. The override store
  thus contributes to two non-adjacent ladder rungs; in code,
  consult the store once at the top, save the result, and apply
  it at the right tier.

In all cases the hit returns `ClassifyResult{Source: eff.Source,
Replacement: eff.Type}` (Replacement defined in §9.3). Receiver
mutability is read off the replacement type via
`type_system.ReceiverIsMut(eff.Type.(*FuncType))` at the call site
that needs it.
When `decl.go` builds the class's effective member map, it also
substitutes `Effective.Type` for the original type — that wiring
lives in `decl.go`, not `Classify`.

### 5.12 Tests

All tests live in `internal/interop/`:

- `store_test.go`: `OverrideStore.Resolve` walks against
  hand-built scope trees; verify free-function vs. nested
  member resolution and the kind/static dispatch.
- `consistency_test.go`: each of arity, param-type,
  return-type, and generics-arity mismatches produces the
  right error. Inputs are hand-built `*type_system.FuncType`
  pairs — no parser/checker dependency.
- `extract_test.go`: small Escalier-source snippets parsed
  and checker-inferred (the real pipeline, driven through
  the `TypeChecker` callback); assert the resulting
  `*ModuleScope` shape — method vs. getter vs. setter slot,
  static/instance separation, namespace nesting, `@all_pure`
  propagation.
- `loader_test.go`: synthetic fs with files at all three
  tiers; assert grouping, precedence,
  duplicate-within-tier error.
- `merge_test.go`: hand-rolled original + override pairs;
  assert resulting `Effective`. Cover overload collapsing,
  override-defined overloads, getter/setter independence,
  static/instance separation, `@all_pure`.
- Integration: a fixture under
  `fixtures/interop_mutability/overrides_loaded/` with a real
  `package.json` + `overrides/foo.esc` + a `.d.ts` it
  references — exercises the full
  parse → check → extract → merge → resolve pipeline.

Exit criteria: loader + merge covered by unit tests; `Classify`
consults the merged store but no built-in overrides exist yet
(§6 ships them).

### 5.13 Deferred to a follow-up PR

The initial landing of §5 (PR #603) plumbs the
discover → check → extract → merge → resolve pipeline end to
end and wires `Classify` into the merged store, but several
threads were left for a follow-up to keep that PR
reviewable. The remaining items are grouped below by when
they need to land relative to §6.

**Already resolved during 5.5–5.9 (struck from this list):**

- *Namespace-vs-class shape conflict.* `mergeChild` now
  routes through `sameChildKind` and emits
  `ErrShapeConflict` when the two sides disagree
  ([merge.go:327-334](../../internal/interop/merge.go#L327-L334)).
- *Destructuring `VarDecl` patterns in override files.*
  `extractIntoContainer` walks patterns via
  `ast.FindBindings` ([extract.go:110](../../internal/interop/extract.go#L110)),
  which traverses every binding node, so destructured
  override bindings already produce scope entries.

#### Group A — Blocks §6 (land first, in a single PR)

These two items are tightly coupled: trio fusion builds the
static-side `ObjectType` that the static-lookup change reads
from. Without both, §6 (built-in overrides) cannot be
authored — every TS stdlib class is a trio and most carry
statics.

- **Original-side `ModuleScope` must fuse the TS
  class-via-trio pattern.** TypeScript's lib files declare
  what is conceptually a class as a trio:
  `interface Foo { … }`,
  `interface FooConstructor { new (…); /* statics */ }`,
  `declare var Foo: FooConstructor`. An override author
  writing `override declare class Foo { … }` expresses one
  unit of intent — overriding "the Foo class." For that
  override to land on all three original-side slots, the
  `.d.ts` → `ModuleScope` builder must recognise the trio
  pattern and emit a single class-shaped
  `Children["Foo"]` populated from `Types["Foo"]`
  (instance), `Types["FooConstructor"]` (statics +
  constructor), and `Values["Foo"]` (the value binding).
  Without trio-fusion the user would need three
  separate override declarations to cover one
  conceptual class, which defeats the ergonomic goal.

  Recommended approach: a name-based heuristic. A
  `Types["X"]` with a sibling `Types["XConstructor"]` and
  a `Values["X"]: TypeRef("XConstructor")` collapses to
  one class-shaped Child; otherwise fall back to literal
  mapping. Mirror the `ArrayConstructor` exception from
  prelude.go. Note that the override side already
  produces this shape uniformly (Escalier's own
  `class Foo { … }` does the same), so once the original
  side fuses, the merge is symmetric.

  Ordering constraint: fusion must run on the
  post-inference `Namespace`, *after* TypeScript-style
  interface declaration merging has folded every
  `interface Foo { … }` (and every
  `interface FooConstructor { … }`) sibling into a single
  `Types["Foo"]` / `Types["FooConstructor"]`. Fusion then
  snapshots the merged shapes, so same-module interface
  augmentations (the common `.d.ts` augmentation pattern,
  including `class Foo` augmented by a later
  `interface Foo`) are carried through automatically. If
  fusion ran incrementally per file, later augmentations
  would be lost — don't do that.

- **Static-side method/property types.** `extract.go`
  `lookupObjElemType` short-circuits to `nil` when
  `static == true`, and `buildClassChild` currently drops
  static-side override entries entirely to avoid corrupting
  the merge store with nil-typed slots. The user-visible
  effect is that static overrides are silently no-ops.

  The static-side ObjectType is *already in the namespace*
  — no checker change is needed. Both Escalier's own
  `class Foo { … static bar() }` and the TS trio
  (`interface Foo + interface FooConstructor +
  declare var Foo: FooConstructor`) park the static
  ObjectType under `ns.Values["Foo"].Type`:
  - Escalier class: `Values["Foo"].Type` is the static
    `ObjectType` directly (containing `ConstructorElem`
    + statics).
  - TS trio: `Values["Foo"].Type` is
    `TypeRef("FooConstructor")` whose alias resolves to
    the same shape.

  Fix: teach `lookupObjElemType` (when `static == true`)
  to look up `ns.Values[name]`, peel any `TypeRefType`
  layer via `unwrapToObject`, and search the resulting
  ObjectType's non-`ConstructorElem` members. Then lift
  the `buildClassChild` skip so static overrides flow
  through the pipeline like instance ones. The
  `ArrayConstructor` exception encoded in
  `UpdateMethodMutability` (prelude.go) should be carried
  over.

#### Group B — Correctness multiplier for §6 (land alongside A)

Independent of Group A and can run in parallel. Land before
§6 authoring picks up steam, otherwise mistakes in property
overrides go silent and only surface at use sites.

- **Property-type consistency.** `mergeLeaf` only runs the
  consistency check when both `orig` and `over` carry
  `*FuncType` ([merge.go:574-581](../../internal/interop/merge.go#L574-L581)).
  Property slots (and any other non-function leaf) can
  silently diverge in type between override and original.
  Add a structural-equivalence check on non-function leaves.

  Note: property type overrides are a first-class use case,
  not a forgotten corner of the merge. Per requirements
  principle #7, a property has three independent axes — slot
  reassignability (`readonly`), referent mutability (the type's
  `Mut[…]` wrapping), and borrow scope (property-level
  lifetimes) — and overrides legitimately need to change any
  combination of them. The most common drivers are:
  *Mut wrapping* (recording that `Container<T>.items: T[]`
  is actually `Mut[Array[T]]`); *precision tightening*
  (refining a TS-side `any`/`object`/sloppy union to the
  runtime shape); and *brand narrowing* (`id: string` →
  `id: UserId`). The structural-equivalence check should
  permit these directions of refinement while still catching
  outright shape mismatches; the exact rule (full equality
  vs. subtype-on-`Mut`-axis vs. opt-in via a `@checked` tag)
  is open and worth resolving as part of this item.

#### Group C — Defer until a concrete case appears

Both gaps surface only in narrow scenarios and don't block
§6, §9, or §11. Track them here; land when an override
actually trips them.

- **Lifetime-erased signature equivalence.** §5.7's
  `funcSignatureEquivalent` uses the strict
  `Type.Equals`, which is sensitive to `LifetimeParams`
  arity on nested `FuncType`-valued parameters. TS-derived
  originals never carry lifetimes, so overrides that add
  lifetimes to a nested function-type param will trip the
  consistency check spuriously. Introduce a
  lifetime-erased equivalence routine (or a flag on
  `Equals`) and use it only on the consistency-check path.

- **Type and value namespace collision in `Container.Free`.**
  `extract.go` routes type aliases and values (var/func/class)
  into the same `Container.Free` map keyed by string. Escalier
  keeps types and values in separate namespaces, so a module
  may legitimately declare `type Foo = …` alongside
  `val Foo = …` or `class Foo { … }`. Today the second-seen
  entry silently overwrites the first. Split `Container.Free`
  into `FreeValues` and `FreeTypes` (or wrap each `Effective`
  with a namespace tag) and update the resolver/merge to
  route lookups accordingly.

## 6. Built-in overrides

Goal: author the data tables that the resolver loads at startup.

### 6.0 Prerequisite: §5.13 Group A

§6 authoring depends on the two §5.13 Group A items being in place:
trio fusion on the original-side `ModuleScope` builder, and
static-side member lookup in `extract.go`. Both landed in
[internal/interop/class_shapes.go](../../internal/interop/class_shapes.go)
(`RecoverClassShapes`) and the refactored
[internal/interop/extract.go](../../internal/interop/extract.go)
(`buildClassChild` reads the instance side via `lookupInstanceObject`
and the static side via `lookupStaticObject`).

Concretely, this means a built-in override file can write:

```escalier
override declare class Promise<T> {
  then<U>(self, onFulfilled: (T) => U): Promise<U>
  static all<T>(promises: Promise<T>[]): Promise<T[]>
}
```

…and it merges correctly against the TS-side trio
(`interface Promise` + `interface PromiseConstructor` +
`declare var Promise: PromiseConstructor`) without the author
having to write three matching `override` declarations.

**Deviation from the plan as originally written.** The plan text
recommended "mirror the `ArrayConstructor` exception from
[prelude.go](../../internal/checker/prelude.go)" — i.e., skip trio
fusion for `Array`. That exception was **not** implemented. The
prelude exception is about *receiver-mutability flipping*
(Array's mutability is handled by `UpdateCollectionMutability`,
not by the generic `*Constructor` walk); the trio-fusion question
is purely structural — `interface Array` + `interface ArrayConstructor` +
`declare var Array: ArrayConstructor` is conceptually one class,
and §6 (or a user override) must be able to write
`override declare class Array { … }` against it. Skipping fusion
for `Array` would defeat that.

Compatible with the rest of §6 anyway: `Array` is **not** in the
built-in override list below (tier 3 / `ReadonlyArray` covers it).
The fusion change just keeps the door open for user overrides on
`Array` without forcing a special case.

### 6.1 Bundling mechanism

Built-in override `.esc` files live under
`internal/interop/data/` and are embedded into the binary with
`//go:embed data/builtins/* data/libs/*` declared in
`interop/data.go`:

```go
//go:embed data
var BuiltinFS embed.FS
```

`loader.Load` accepts `BuiltinFS` as its `builtin` argument so tests
can substitute a synthetic FS without touching disk.

### 6.2 Module-name mapping

A built-in override file declares which TS module(s) it applies to
with a header pragma (defined in §2's grammar):

```escalier
// overrides/data/libs/lodash.esc
override declare module "lodash" {
  @all_pure
  ...
}
```

Built-in files use `declare global { ... }` (or
`declare namespace`) for symbols on the global object. The loader
keys entries by the module string for `import`ed modules, and by an
empty module string + the global owner for `declare global`.

Three sub-tasks, the first two parallelizable:

- **Built-ins** — classes that don't ship a `Readonly*` variant in
  TypeScript's lib files and therefore can't be classified by tier
  3 alone. Two flavours:
  - *Always-immutable instances:* the primitive wrappers
    (`Number`, `BigInt`, `String`, `Boolean`), plus `Symbol` and
    `Promise`. Every method declares `self` (non-mutating
    receiver). These are written out explicitly rather than
    special-cased in `Classify` — over time we want built-in
    overrides to be the primary source of mutability / lifetime /
    throws information, independent of any one version of
    TypeScript's lib declarations.
  - *Mixed-mutability instances:* `Date`, `RegExp`, `Error`,
    `Function`, `Console`, typed arrays (`Int8Array` etc.), `URL`,
    `URLSearchParams`, `WeakRef`, `WeakMap`, `WeakSet`, iterator /
    generator protocols, DOM body-bearing types (`Body`, `Request`,
    `Response`). Each method annotated individually per primary
    sources (ECMAScript spec, MDN). Coverage tracked in a checklist
    in this file as entries are added.

  **Bootstrap state (post-#612).** A subset of these — `Date`,
  `Function`, `Console`, `Body`, `Request`, `Response` (and the
  pre-existing `String`/`Number`/`Boolean`/`RegExp`) — are
  currently shipped as hardcoded entries in `prelude.go`'s legacy
  `mutabilityOverrides` Go map (see `UpdateMethodMutability`'s
  second pass for non-`*Constructor`-shaped classes). They were
  added inline as stop-gaps to keep the test suite green after the
  #612 polarity flip exposed under-classification. The §6 `.esc`
  override pipeline, once authored, should supersede those
  hardcoded entries.

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
    es5.esc        // Date, RegExp, Error
    es2015.esc     // Promise, WeakMap, WeakSet, iterator
    es2017.esc     // typed arrays
    es2021.esc     // WeakRef
    es2023.esc     // (none today — Array additions are tier 3)
    dom.esc        // URL, URLSearchParams
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

- **Consistency test against original `.d.ts`.** Test in the
  compiler suite that runs over every built-in override entry. Every
  override has a corresponding original `.d.ts` — Escalier-authored
  libraries also ship `.d.ts` for TypeScript back-compat, so there
  is no "no original types" case:
  - Built-in symbols → TS lib `.d.ts` set bundled with the
    `typescript` version pinned in the repo's root
    [package.json](../../package.json) (currently `^5.7.2`).
    Bumping `typescript` in `package.json` is what bumps the
    consistency baseline.
  - FP / immutability libraries → corresponding `@types/*` package
    (or the library's own bundled types), also pinned via
    `package.json` alongside the built-in override.
  For each entry, look up the original declaration, compare
  non-receiver arity / parameter types / return type under the
  same mapping the merge uses, and fail the build on divergence.
  Bumping any pinned version surfaces drift as a deliberate
  fix-up step.

  **Sourcing.** Read the bundled `.d.ts` directly from the
  installed `node_modules/typescript/lib/lib.*.d.ts` and
  `node_modules/@types/<lib>/...` at test time — no separate
  vendoring. The repo's `package.json` is the single pin; CI's
  `npm install` step produces the inputs. Bumping is a one-step:
  change the version in `package.json`, run the consistency test,
  resolve any reported drift.

  The consistency test (`builtin_consistency_test.go`) iterates
  every entry in `BuiltinFS`, parses the corresponding original
  declaration from the installed `node_modules/typescript/lib/`
  or `node_modules/@types/<lib>/` path (per the **Sourcing**
  paragraph above), and calls `consistency.CheckSet` — the same
  code path used by user-override merging in §5.

Tests:

- Fixture per library under
  `fixtures/interop_mutability/builtin_<lib>/` that imports a
  representative subset and asserts the receiver mutability via
  call-site type errors (mutate through immutable binding fails).
- `builtin_consistency_test.go` as described above.
- A regression test asserting that the embedded FS is non-empty
  and every embedded file parses.

Exit criteria: built-in counter-examples (`Date.setHours` mutates,
`toSorted` doesn't, `Object.assign` mutates target) all classify
correctly; consistency test green against pinned original versions.

## 7. Heuristics (tiers 5–6)

Goal: implement the remaining tiers so unknown TS APIs get useful
classifications.

Both tiers live in `internal/interop/mutability.go` as new
`classifyTier5` / `classifyTier6` functions called from `Classify`
in tier order. Each returns `(ClassifyResult, bool)` like the
existing `classifyTier2`.

Note: built-in classes whose instances have no mutable surface
(the primitive wrappers `Number`/`BigInt`/`String`/`Boolean`,
plus `Symbol` and `Promise`) are **not** modelled here. They are
authored as explicit per-method built-in overrides in §6 alongside
the rest of the stdlib. The rationale is the longer-term goal of
decoupling Escalier from any one version of TypeScript's lib
declarations: mutability, lifetimes, and throws information
should ultimately come from primary sources (ECMAScript spec,
MDN, library docs), with TypeScript's `.d.ts` being just one
input. Special-casing these classes inside `Classify` would
short-circuit that pipeline; routing them through built-in
overrides keeps the same authoritative path the rest of the
stdlib uses.

### 7.1 Tier 5: `get*` prefix rule

Requirements §"Core principles" #4 names three mutate-on-miss
shapes that must not be caught by the `get*` rule: `getOrInsert*`,
`getOrUpdate*`, `getOrCreate*` (note: `getOrDefault*` is *not* an
exception — it returns a default without writing). Tier 5 skips
these names so they fall through to tier 6, which classifies them
as mutating via its prefix rules.

```go
// getOrMutatingPrefixes are name shapes where the leading "get"
// is followed by a mutating action. Tier 5 must not classify
// these as non-mutating; tier 6 picks them up via its mutating-
// prefix list.
var getOrMutatingPrefixes = []string{
    "getOrInsert", "getOrUpdate", "getOrCreate",
}

func classifyTier5(ctx ClassifyContext) (ClassifyResult, bool) {
    m, ok := ctx.Member.(*dts_parser.MethodDecl)
    if !ok { return ClassifyResult{}, false }
    name := identName(m.Name); if name == "" { return ClassifyResult{}, false }
    if !strings.HasPrefix(name, "get") || len(name) < 4 ||
        !unicode.IsUpper(rune(name[3])) {
        return ClassifyResult{}, false
    }
    for _, p := range getOrMutatingPrefixes {
        // The outer `get*` guard above already requires len(name) > len("get")
        // and an uppercase letter at index 3, so every `p` (which begins with
        // "get" + uppercase) is strictly shorter than `name` when matched —
        // no len(name) == len(p) arm needed.
        if strings.HasPrefix(name, p) && unicode.IsUpper(rune(name[len(p)])) {
            return ClassifyResult{}, false // fall through to tier 6
        }
    }
    return ClassifyResult{SelfMut: false, Source: TierGetPrefix}, true
}
```

`identName` extracts the name string from a `PropertyKey` (Ident or
string-literal computed key); returns `""` for symbol-keyed members.

The mutating-prefix list in §7.2 must include `getOrInsert*`,
`getOrUpdate*`, `getOrCreate*` so that the fall-through actually
lands on a mutating classification. Requirements §"Heuristics" →
Mutating-name signals lists `getOrInsert` / `getOrCreate` /
`getOrUpdate` as examples of "both prefixes → prefer mutating," so
this matches the spec.

### 7.2 Tier 6: name-based heuristics

Requirements §"Heuristics" → Medium signals mixes two shapes:
*prefixes* (e.g. `is*`, `to*`, `find*`) and *exact-match keywords*
that are themselves whole method names (e.g. `contains`, `equals`,
`indexOf`, `forEach`, `keys`, `values`, `entries`, `at`, `every`,
`some`). Tier 6 needs both: the prefix matcher fires when the name
starts with the prefix and is followed by end-of-string or an
uppercase letter, while the exact-match list compares the name in
full.

All four slices below are the source of truth synced from
requirements.md; updating one means updating the other.

```go
// Source of truth: requirements.md §"Heuristics".
var nonMutatingPrefixes = []string{ /* is, has, can, should, will,
    was, did, to, as, with, find, filter, map, reduce, clone, copy,
    count, ... */ }
var nonMutatingExact    = []string{ "contains", "includes",
    "equals", "matches", "slice", "concat", "at", "every", "some",
    "indexOf", "lastIndexOf", "forEach", "keys", "values",
    "entries" }
var mutatingPrefixes    = []string{ /* set, add, remove, delete,
    clear, reset, init, push, pop, shift, unshift, insert, replace,
    update, register, unregister, dispatch, emit, write, flush,
    getOrInsert, getOrUpdate, getOrCreate, ... */ }
var mutatingExact       = []string{ "sort", "reverse" }
```

Lookup logic:

```go
func classifyTier6(ctx ClassifyContext) (ClassifyResult, bool) {
    name := identName(memberName(ctx.Member))
    if name == "" { return ClassifyResult{}, false }
    isNonMut := matchesAnyPrefix(name, nonMutatingPrefixes) ||
                stringInSlice(name, nonMutatingExact)
    isMut    := matchesAnyPrefix(name, mutatingPrefixes) ||
                stringInSlice(name, mutatingExact)
    switch {
    case isMut: // requirements: if both, prefer mutating
        return ClassifyResult{SelfMut: true,  Source: TierNameHeuristic}, true
    case isNonMut:
        return ClassifyResult{SelfMut: false, Source: TierNameHeuristic}, true
    }
    return ClassifyResult{}, false
}
```

The four slices are package-level `var` so they can be re-synced
from requirements without changing the matching function.

### 7.3 Inheritance fallthrough

Requirements §"Resolution order" → Inherited classifications: when
a subclass method has no direct match at any tier, the
classification is inherited from the nearest base-class method, and
the inherited result carries the *base method's* tier — so an
inherited classification from a base whose tier was an explicit
signal stays explicit, and one inherited from a heuristic stays
uncertain. Inheritance never upgrades certainty.

This is implemented as a fallthrough wrapper around the per-tier
cascade, not as a new tier number. After tiers 1–6 all miss on the
subclass's member, `Classify` re-runs itself against the same
member name on the nearest base class. The recursion terminates at
the root of the inheritance chain; if no base method exists,
`Classify` falls through to tier 7 (default mutating).

```go
func Classify(ctx ClassifyContext) ClassifyResult {
    if r, ok := classifySelf(ctx); ok { return r } // tiers 1..6
    if base, ok := nearestBaseMember(ctx); ok {
        return Classify(base) // inherited tier carries through
    }
    return ClassifyResult{SelfMut: true, Source: TierDefault}
}
```

`nearestBaseMember` walks the `Extends` chain on `ctx.Owner`,
looking up the same member name (canonicalised per §5.4) at each
level. Static and instance scopes are walked independently. The
context passed to the recursive call has `Owner` and `Member`
swapped to the base; the rest (module path, store, etc.) carries
through unchanged so the override store still gets consulted
against the base member's qualified path.

The §11 uncertainty warning predicate (`isHeuristicTier`) reads the
final `Source` and so naturally fires on inherited heuristic
classifications without special-casing.

### 7.4 Tests

`mutability_test.go` gets one table-driven test per tier:

- `TestClassifyTier5_GetPrefix` — `getFoo` → non-mutating;
  `get` (bare), `getter`, `gets`, `setFoo` → fall through.
- `TestClassifyTier6_NamePrefixes` — every prefix in both lists; a
  name matching both (e.g. `setToString`) lands mutating; a name
  matching neither falls through to tier 7.
- `TestClassifyTierOrdering` — a method that would match multiple
  tiers resolves at the earliest matching tier. Verifies wiring,
  not heuristic content.
- `TestClassifyInheritance` — covers §7.3: subclass method with
  no direct match inherits the base method's classification *and*
  tier (explicit-on-base stays explicit, heuristic-on-base stays
  heuristic); no-base-method falls through to `TierDefault`;
  override-store hit on the subclass path takes precedence over
  inheriting from the base.

Exit criteria: every heuristic in the requirements is testable and
covered; tier-ordering test passes; inheritance preserves the base
tier as required.

## 8. Type printer round-trip audit

Prerequisite for §9. Can run in parallel with §5–§7.

Run every `type_system.Type` shape through `internal/printer`'s
type-printing entry point and feed the output back through
`internal/parser`'s type-annotation parser + the existing type-ann →
`type_system.Type` checker pipeline. Diff input and output.

- For shapes that round-trip cleanly: nothing to do.
- For shapes that don't: fix the printer in place. Divergence from
  the human-readable form is a smell — `@esctype` consumers (humans
  reading the generated `.d.ts`) and the parser should see the same
  text.
- If a specific shape genuinely needs a different serialised form
  (escaping rules, e.g.) prefer extending the printer with a
  serializer-mode flag rather than maintaining two type printers.

Audit input: a fixture file enumerating one instance of every
concrete `Type` variant in `type_system/types.go`, including the
hairy ones (intersection / union / conditional / mapped / template
literal / generic instantiation / regex / unique symbol / class
self-type).

Exit criteria: every type variant prints and parses to a structurally
equal type. The §9 round-trip fixture builds on this.

## 9. `@esctype` round-trip

Goal: round-trip Escalier types through `.d.ts` losslessly.

### 9.1 Emit side

Lives in [internal/codegen/dts.go](../../internal/codegen/dts.go).
For every exported decl that gets a `.d.ts` form (functions,
classes, methods, properties, type aliases):

- Print the Escalier type using the existing printer
  (`internal/printer`). §8 establishes that the printer's
  output round-trips through the parser; §9 relies on that
  guarantee.
- Attach the tag as `@esctype {<printed-type>}` using the
  `@esctype` grammar fixed in §2 (balanced braces, `*\/`
  escape for `*/` inside string literals).
- Merge with any existing leading comment: if the decl already has
  a `/** ... */` block, append the tag inside it; otherwise emit a
  fresh `/** @esctype {...} */` block immediately above the decl.

New helper `func renderEsctypeTag(t *type_system.Type) string` in
`dts.go`, plus a `attachLeadingComment(decl, line string)` helper
that handles the merge-or-create logic. Both should be exercised
by `dts_test.go` snapshot tests with and without a pre-existing
TSDoc block on the decl.

### 9.2 Consume side

**Parser change: retain leading TSDoc.** Today the `dts_parser`
lexes `LineComment` / `BlockComment` tokens
([token.go:17](../../internal/dts_parser/token.go#L17)) but
unconditionally discards them at decl boundaries via
`p.skipComments()` and ad-hoc `for p.peek().Type == LineComment ||
... { p.consume() }` loops
([object.go:25](../../internal/dts_parser/object.go#L25)). No AST
node currently carries comment content — the existing
`comment_test.go` snapshots only assert parse-success, not
preservation. Concrete work needed:

1. Replace the skip-and-discard pattern with a *pending leading
   doc* buffer on the parser: when a `BlockComment` whose token
   text starts with `/**` is encountered at a decl boundary,
   strip the `/**` / `*/` markers, normalise per-line `*` prefixes,
   and store the contents on the buffer. A non-doc comment
   (`/*` without the second `*`, or a `//` line comment) clears
   the buffer; so does any non-whitespace token that isn't either
   the start of a decl or a leading modifier keyword that prefixes
   one. The decl-modifier whitelist — tokens that the buffer
   survives — is `export`, `default`, `declare`, `async`, `public`,
   `private`, `protected`, `static`, `readonly`, `abstract`,
   `override`, plus any modifier the parser later grows. Anything
   else resets the buffer so intervening "noise" doesn't leak doc
   from a prior decl. Multiple adjacent `/**` blocks: keep only
   the most recent (matches TS's own TSDoc resolution).
2. When the decl-parsing function constructs its AST node, attach
   the pending buffer to a new `LeadingDoc string` field on that
   node and clear the buffer.
3. Add `LeadingDoc string` to every decl-bearing AST node where
   `@esctype` is permitted: `MethodSignature`, `MethodDecl`,
   `PropertyDecl`, `GetterDecl`, `SetterDecl`, `ConstructorDecl`,
   `FunctionDecl`, `VariableDecl`, `TypeAliasDecl`, `ClassDecl`,
   `InterfaceDecl`, `NamespaceDecl`, and top-level
   var/let/const/function/type/interface/class/namespace decls.
   Empty string when no leading TSDoc precedes the decl.
4. Extend `comment_test.go` to assert `LeadingDoc` contents
   across: bare TSDoc, multi-line TSDoc with `*`-prefixed lines,
   trailing whitespace between TSDoc and decl (still picks it
   up), multiple TSDoc blocks separated by whitespace (only the
   last wins), TSDoc followed by a `//` line comment then the
   decl (buffer reset — the TSDoc is no longer immediately above),
   TSDoc followed by an unrelated decl (buffer cleared, no leak
   to the next decl), TSDoc followed by one or more decl modifiers
   (`export`, `declare`, `export default`, `export declare`,
   `readonly`, etc.) then the decl (buffer survives and attaches
   to the decl). Snapshots in `__snapshots__/` regenerate to
   include the new field on every decl node.

New file `internal/interop/esctype.go`:

```go
// ParseEsctype scans a TSDoc block for the first `@esctype {...}`
// tag. Returns nil, nil if no tag is present. Returns a non-nil
// error only when a tag is present but malformed.
func ParseEsctype(doc string) (*type_system.Type, error)
```

Implementation:

1. Scan `doc` for the literal `@esctype` followed by optional
   whitespace and `{`.
2. Read until the matching `}`, tracking string-literal context to
   ignore braces inside strings, and undoing the `*\/` escape.
3. Feed the inner text to a new entry point on the Escalier parser
   (`parser.ParseTypeAnn(src string) (ast.TypeAnn, []error)`),
   then through the existing `checker` type-ann → `type_system.Type`
   pipeline. Malformed type → return error with span.

### 9.3 `Classify` integration

Add `classifyTier2(ctx)` consulting the parsed `@esctype` tag on
`ctx.Member.LeadingDoc`. The function returns the full effective
type, so the caller in `decl.go` substitutes the entire type when
tier 2 hits. Extend `ClassifyResult`:

```go
type ClassifyResult struct {
    // SelfMut is the mutability decision for the `self` receiver,
    // produced by tiers that classify methods (3/5/6/7). It is
    // only read by decl.go on code paths that build a SelfParam
    // — instance methods, getters, setters, constructors. For
    // static methods and bare functions, decl.go does not consult
    // SelfMut; the no-receiver case is encoded structurally as
    // SelfParam == nil on the resulting *FuncType. The name
    // makes the scope explicit: this field is about the
    // receiver's mutability, not the member's value-side
    // mutability.
    SelfMut     bool
    Source      ResolutionTier
    // Replacement is non-nil for tiers that supply a full type
    // (tier 1 / TierUserOverride, tier 2 / TierEsctype, tier 4 /
    // TierBuiltinOverride). When set, decl.go uses it wholesale
    // and ignores SelfMut — the receiver shape lives on
    // Replacement.(*FuncType).SelfParam.
    Replacement *type_system.Type
}
```

The three receiver states (no receiver / `self` / `mut self`) are
always preserved on the final `*FuncType` that lands in
`Binding`: no-receiver is structural (decl.go doesn't build a
`SelfParam` at all on those paths); `self` vs `mut self` is set
either from `SelfMut` (heuristic tiers) or taken wholesale from
`Replacement.(*FuncType).SelfParam` (explicit tiers).

Tiers other than user overrides and `@esctype` leave `Replacement`
nil; the override store (§5) and the `@esctype` tag (this section)
both set it, and `decl.go` uses it to replace the original type
entirely. User overrides still outrank `@esctype` per requirements
§"Round-tripping" → Precedence: `Classify` evaluates the override
store's `TierUserOverride` path *before* `classifyTier2`. If both
apply, the store wins and the symbol's `Source` is
`TierUserOverride` — never `TierEsctype` — which is what the "user
override beats `@esctype`" fixture verifies. The store's
`TierBuiltinOverride` path is evaluated *after* `classifyTier2`
(see §5.11), so `@esctype` outranks built-in overrides.

### 9.4 TSDoc tooling

`cmd/escalier`'s build step writes outputs under `build/`
([cmd/escalier/build.go:117](../../cmd/escalier/build.go#L117)).
Emit `tsdoc.json` once at `build/tsdoc.json` declaring the
`@esctype` tag:

```json
{
  "tagDefinitions": [
    { "tagName": "@esctype", "syntaxKind": "block", "allowMultiple": false }
  ]
}
```

The file is overwritten on every build; it has no per-module
content. When a richer package-output story lands (emitting a real
`package.json`, etc.), revisit placement to match wherever the
generated package's TSDoc root ends up.

**Scope of this file — what it does and doesn't do.** TSDoc
configuration does not propagate automatically from a dependency
into a consuming project; tools like `eslint-plugin-tsdoc` and
`api-extractor` discover `tsdoc.json` by walking up from the
source file being analyzed, not by reading it out of
`node_modules`. The emitted file is therefore useful in two
narrow ways:

1. *Within the Escalier package itself.* Any TSDoc tooling run
   over the generated `build/` tree (during package authoring,
   CI, or local validation) picks up the tag definition and
   doesn't flag `@esctype` as unknown.
2. *As an `extends` target for opt-in consumers.* A TypeScript
   consumer that runs strict TSDoc tooling and wants to silence
   unknown-tag complaints on imported Escalier types can extend
   the shipped file in their own `tsdoc.json`:

   ```json
   { "extends": ["./node_modules/<escalier-pkg>/tsdoc.json"] }
   ```

**What consumers do not need to do.** The TypeScript compiler
itself (`tsc`) parses JSDoc/TSDoc tags loosely and ignores tag
schemas, so a plain `tsc` consumer can import an Escalier-built
`.d.ts` containing `@esctype` blocks with no setup whatsoever —
the interop path works regardless of whether the consumer has a
`tsdoc.json`. The emitted file matters only for opt-in TSDoc
tooling like ESLint plugins or doc-extraction pipelines, and even
there only when the consumer wires up the `extends` reference.

### 9.5 Tests

- `internal/codegen/dts_test.go` snapshots covering: fresh comment,
  merge into existing TSDoc, string-literal containing `*/`,
  nested object type in the tag, multi-line tag.
- `internal/interop/esctype_test.go` — table-driven, mirroring the
  emit tests on the parse side; plus malformed-tag errors.
- Round-trip fixture
  `fixtures/interop_mutability/esctype_roundtrip/`: an Escalier
  module is compiled to `.d.ts`, re-consumed as an external
  package by a second Escalier module, and the printed type of
  each re-imported symbol is asserted equal to the source.
- Precedence fixture
  `fixtures/interop_mutability/esctype_vs_override/`: a vendored
  `.d.ts` with an `@esctype` tag plus a user `overrides/*.esc`
  entry for the same symbol; the override wins, source tier is
  `TierUserOverride`, no uncertainty warning fires (§11
  cross-check).

Exit criteria: round-trip fixture passes; precedence fixture
passes; classification tier 2 wired in.

## 10. `implements` mutability conformance

Goal: enforce that a class implementing an interface matches the
interface's mutability annotations on each implemented method (per
requirements §"Policy decisions" — `implements` requires mutability
conformance).

### 10.0 Phasing — independent of other sections

§10's core check only needs primitives that already exist:
`type_system.ReceiverIsMut`
([internal/type_system/types.go:1071](../../internal/type_system/types.go#L1071)),
the `FuncType.SelfParam` encoding, and the existing
member-comparison walk in
[internal/checker/check_implements.go](../../internal/checker/check_implements.go)
(#561). The safety guarantee — catching `class { mut self foo() }`
against `interface { self foo() }` mismatches in either direction
— can therefore ship before §3, §5, §7, §9, or §11 land.

Suggested two-pass landing:

1. **Lean pass (no other sections required).** Implement the
   bidirectional `ReceiverIsMut` equality check (§10.2) and emit a
   minimal `ImplementsMutabilityMismatchError` carrying just
   `Class`, `Interface`, `Member`, `ClassSide`, `InterfaceSide`,
   and `Span`. Skip `ClassSource` / `InterfaceSource` /
   `ClassProvenance` / `InterfaceProvenance` and the tier-aware
   "add an explicit signal" suggestion. All §10.5 test fixtures
   except `err_heuristic_source/` pass at this point.
2. **Diagnostic-richness pass (additive, once dependencies land).**
   After §3 finalises `ResolutionTier`, §11.2 adds `Source` to
   `FuncParam`, §5 introduces `interop.Origin`, and §7 populates
   tier sources on heuristically-classified members, extend the
   error struct with the four omitted fields and the message
   renderer with the suggestion clause. `err_heuristic_source/`
   becomes testable here.

The error struct grows purely additively across these two passes,
so the lean version's public surface stays compatible.

### 10.1 Touch point

[internal/checker/check_implements.go](../../internal/checker/check_implements.go)
already verifies structural conformance (added in #561). Extend the
existing per-member walk there — *don't* introduce a second pass.

Conceptually the change is: where the existing code unifies the
interface method's type against the class method's type, also
compare `self` vs `mut self` and report mismatches as a separate,
more specific error than a generic unification failure.

### 10.2 Comparison rule

Per requirements §"Policy decisions" → `implements`, the check is
**strict bidirectional equality on the receiver mode**, not
subtyping. Either direction of mismatch is a hard error: a class
method declared `mut self` cannot satisfy an interface method
declared `self`, *and* a class method declared `self` cannot
satisfy an interface method declared `mut self`. The fix is to
align the annotation (or add an explicit signal) on whichever side
is wrong.

For each interface member matched against a class member:

- If both have a `SelfParam`: `ReceiverIsMut` must return the same
  value on both sides.
- If only one side has a `SelfParam`: treat the absent side as
  `mut self` (the type-system default for an unannotated receiver)
  before comparing.

Both sides are read from the **post-merge** effective type — i.e.
after `OverrideStore.Resolve` and `@esctype` consumption have run
during interop conversion. By the time `check_implements` runs the
types are already effective. A `SelfParam.Source` of
`TierUserSource` (the class or interface was authored in `.esc`)
counts as authoritative — the conformance check treats it like any
other explicit tier when deciding whether to emit the "add an
explicit signal" suggestion.

### 10.3 Diagnostic

New error variant in
[internal/checker/error.go](../../internal/checker/error.go):

```go
type ImplementsMutabilityMismatchError struct {
    Class, Interface     string
    Member               string
    ClassSide            string // "self" | "mut self"
    InterfaceSide        string
    ClassSource          interop.ResolutionTier
    InterfaceSource      interop.ResolutionTier
    ClassProvenance      []interop.Origin // chain from class-side Effective
    InterfaceProvenance  []interop.Origin // chain from interface-side Effective
    Span                 ast.Span
}
```

Message format:

```text
class `Foo` does not conform to `Bar`: method `baz` declares `mut self`
but interface declares `self`
  class side resolved via tier 6 (name heuristic) — add an explicit
  `self` or `mut self` annotation, an override entry, or an
  `@esctype` tag to make this deterministic
```

When either source is in `{TierGetPrefix, TierNameHeuristic}`,
the diagnostic includes the "add an explicit signal" suggestion —
this is the one place a heuristic produces a hard error rather
than the §11 warning. `TierUserSource` and the
explicit tiers (`TierUserOverride`, `TierEsctype`,
`TierExplicitSignal`, `TierBuiltinOverride`) do not trigger the
suggestion.

**Import direction.** `checker` already depends on `interop` for
type construction; the new error type extends that direction with
`interop.ResolutionTier` and `interop.Origin`. `interop` must not
import `checker` (override errors live in `internal/interop/
errors.go` per §5.8 and are surfaced through the checker's
existing error channel by value, not by interface reference back
into checker). Confirm at implementation time that no checker →
interop → checker cycle exists.

### 10.4 Scope (unchanged)

- `getObjectAccess` still doesn't walk `Implements`. No resolution
  changes.
- Other conformance checks (arity, return type, param types)
  stay as they are; this only adds the receiver-mode check.

### 10.5 Tests

`fixtures/interop_mutability/implements_mutability/` with sub-cases:

- `ok_both_mut/` — class & interface both `mut self`, passes.
- `ok_both_self/` — both `self`, passes.
- `err_class_mut_iface_self/` — class mutates, interface declares
  `self`; expects `ImplementsMutabilityMismatchError`.
- `err_class_self_iface_mut/` — reverse, expects error.
- `err_heuristic_source/` — class member name matches a tier-6
  mutating prefix while interface declares `self`; expects error
  with the "add explicit signal" suggestion text.

Also a table-driven test in
`internal/checker/check_implements_test.go` covering the comparison
logic directly.

Exit criteria: conformance check is on by default (not gated by the
uncertainty flag); fixtures and table-driven tests pass.

## 11. Uncertainty warning

Goal: opt-in warning when an immutable-receiver call relies on a
heuristic.

### 11.1 CLI flag

Add `--warn-uncertain-mutability` to
[cmd/escalier/](../../cmd/escalier/) (and the equivalent
config-file key under `compilerOptions` once that exists). Default
off. Threaded through to the `checker` via its existing options
struct.

### 11.2 Carrying the tier through to call sites

The receiver mode currently lives structurally on
`FuncType.SelfParam` (nil for no receiver; `SelfParam.Type`
wrapped in `*MutType` for `mut self`; bare otherwise for `self`)
— `type_system.ReceiverIsMut` is the canonical accessor. The
classification tier produced in §5 / §7 needs to survive interop
conversion long enough to reach the checker.

Add a non-serialised field `Source ResolutionTier` on `FuncParam`,
set by `interop/decl.go` on the `SelfParam` when it constructs the
type from `ClassifyResult`. The field is metadata, not part of
unification.

**Default tier for user-authored Escalier source.** Ladder tiers
1–7 cover the interop classification path. A `SelfParam`
constructed by the checker from `.esc` source (a user-written
`mut self` or `self` annotation) is not produced by `Classify` at
all, so its `Source` must sit outside the ladder. This is
`TierUserSource`, defined in §3 as the zero value of
`ResolutionTier`. `Classify` never returns it; checker-constructed
`SelfParam`s carry it implicitly; predicates that branch on tier
(e.g. the §11 uncertainty warning, §10's heuristic-suggestion
clause) must explicitly handle it as "authoritative, never
uncertain."

(A side table from `*FuncType` to tier requires more wiring and
is preferred only if `SelfParam` turns out to be shared across
multiple call sites that need different sources — keep this option
in reserve.)

### 11.3 Call-site check

The receiver-mutability gate already lives in
[internal/checker/expand_type.go](../../internal/checker/expand_type.go) —
member-lookup filters out `mut self` methods and setters when the
receiver is not definitely mutable (the `ReceiverIsMut` calls at
[expand_type.go:1056](../../internal/checker/expand_type.go#L1056) and
[expand_type.go:1756](../../internal/checker/expand_type.go#L1756) are
the canonical sites). The §11 warning attaches at the inverse case:
when a non-mutating call resolves successfully but the callee's
non-mutating classification came from a heuristic. Add the warning
emission immediately after the existing gate, around the same
comparison:

```go
if !methodNeedsMut && receiverIsImmut {
    if opts.WarnUncertainMutability &&
        isHeuristicTier(method.SelfParam.Source) {
        emitWarning(UncertainMutabilityWarning{...})
    }
}

func isHeuristicTier(t interop.ResolutionTier) bool {
    return t == interop.TierGetPrefix       ||
           t == interop.TierNameHeuristic
}
```

`UncertainMutabilityWarning` implements the existing
`IsWarning() bool` method on the checker error interface
([error.go:22](../../internal/checker/error.go#L22)) returning
`true`. The CLI already differentiates: warnings print but don't
set a non-zero exit code.

### 11.4 Diagnostic shape

```go
type UncertainMutabilityWarning struct {
    Callee     string
    Tier       interop.ResolutionTier
    Provenance []interop.Origin // chain from the callee's Effective
    Span       ast.Span
}
```

Message:

```text
warning: call to `foo.bar()` treats receiver as non-mutating based
on a name heuristic (tier 6); add an override entry, `@esctype`
tag, or explicit `readonly this` to make this guarantee explicit
```

The message names the tier ("name heuristic", "get prefix")
rather than the bare number.

### 11.5 Negative cases

The warning must **not** fire when:

- `Source ∈ {TierUserSource, TierUserOverride, TierEsctype,
  TierExplicitSignal, TierBuiltinOverride, TierDefault}`. (The
  `TierDefault` case is "assume mutating"; non-mutating calls
  can't reach that tier, so it can't trigger the warning in
  practice — listed for completeness.)
- The receiver is mutable (no immutable-call concern).
- The method is mutating (no contract risk for the immutable caller —
  this would be a hard error, not a warning).

### 11.6 Tests

`fixtures/interop_mutability/uncertain_warning/`:

- `heuristic_warns/` — non-mutating call resolved by tier 6;
  flag on → warning, flag off → silent. Compare diagnostic
  snapshots for both runs.
- `override_silent/` — same call but a built-in override pins the
  classification; flag on → silent.
- `esctype_silent/` — `@esctype` provides the type; flag on → silent.
- `strong_signal_silent/` — `readonly this` on the method; flag on
  → silent.

Plus a checker unit test asserting `isHeuristicTier` returns true
for exactly tiers 5 and 6 and false otherwise.

Exit criteria: warning fires only on heuristic-classified
non-mutating calls; never fires on `@esctype`, strong signals, or
overrides; behaviour gated by the flag.

## 12. Argument mutation refinement (deferred)

Goal: tighten the "all params default to mutating" decision via
overrides, using MDN as source of truth.

- Extend the override schema with per-parameter mutability entries.
- Backfill the built-in override file with the documented cases
  (`Array.prototype.map` callback receiver: non-mutating;
  `Object.assign` target: mutating; etc.).

Deferred from the initial milestone; tracked here so the schema in
§2 leaves room for it. Lifetime annotations and `throws`
clauses (per requirements §"Scope and future extensibility") are
also deferred follow-ons that reuse the merge machinery; they slot
in here as additional override-payload fields rather than new
top-level forms.

## 13. Cross-cutting concerns

- **Performance.** Override-file load happens once per compilation;
  the lookup is per-symbol and runs during interop conversion.
  Expected to be negligible. Add a benchmark only if it shows up in
  profiles.
- **Diagnostics.** Every classification carries the resolution tier so
  diagnostics can explain *why* a method was treated as (non-)mutating.
- **Documentation.** User-facing docs land alongside §9 (so the
  `.d.ts` interop story is explained at the same time it works).

## 14. Open implementation questions

(None outstanding.)

Resolved:

- Override-file location precedence — see requirements
  §"Override file format". Tier 1b consuming-project wins over
  tier 1a `node_modules/<dep>/overrides/`; both win over tier 4
  built-in.
- `@esctype` `*/`-in-strings escape — `*\/` on emit, unescape on
  consume (see §2).
- Type printer reparseability — covered by §8.
- Consistency-test upstream sourcing — read directly from
  `node_modules/typescript` and `node_modules/@types/*`, pinned via
  the repo's root `package.json`.
