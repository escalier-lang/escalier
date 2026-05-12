# TypeScript Interop Mutability: Implementation Plan

This plan implements the requirements in [requirements.md](requirements.md).
Phases are ordered so each one is independently testable and merges
without requiring the next to be in place. Within a phase, work items
list the touch points in the existing codebase.

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

Goal: introduce the eight-tier resolution order from the requirements
as a single function, wired in but populated only with existing
behavior (so output is unchanged).

- Add `internal/interop/mutability.go` defining the `ResolutionTier`
  enum (the canonical 8-tier ladder, plus a zero-valued
  "user-authored source" sentinel — see §11.2):

  ```go
  type ResolutionTier int

  const (
      TierUserSource        ResolutionTier = iota // 0: user-authored .esc source
      TierUserOverride                            // 1: requirements tier 1
      TierEsctype                                 // 2
      TierExplicitSignal                          // 3
      TierShippedOverride                         // 4
      TierPrimitiveWrapper                        // 5
      TierGetPrefix                               // 6
      TierNameHeuristic                           // 7
      TierDefault                                 // 8
  )
  ```

  Plus a `Classify` entry point that takes the TS-side declaration
  plus surrounding context (class shape, module path) and returns
  a `ClassifyResult` (see §9.3). The `Source` field of the result
  is one of the constants above and is needed by §10's conformance
  check and §11's uncertainty warning.
- Route every place in `interop/decl.go` and `interop/helper.go` that
  currently decides method/parameter mutability through `Classify`.
  At this phase `Classify` implements only tier 8 (default to
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

## 5. Override file format, loader & merge

Goal: build the eager-merge pipeline (Approach A, per requirements
§"Implementation decision") that §6 (shipped overrides) and the
user override file feature both depend on.

### 5.1 Merge model

The override system applies **shadowing, not blending**. At any
given slot (one kind/name combination on one owner), at most one
override entry contributes to the final type — competing entries
from other tiers are dropped, not merged with it.

There are two layers of competition:

1. **Within the override system** — three internal tiers
   (`OverrideTierUserProject` > `OverrideTierUserDep` >
   `OverrideTierShipped`). The loader builds one `Scope` per tier.
   The merge collapses them into a single override scope by walking
   slot-by-slot and keeping the highest-precedence non-empty entry.
   See §5.5a for the explicit collapse step.
2. **Override system vs. upstream `.d.ts`** — the collapsed
   override scope is then zipped against the upstream scope. A slot
   present on the override side wholesale displaces upstream's
   type for that slot; a slot only on upstream passes through with
   no `Source` stamped (leaving classification to `Classify`'s
   later tiers).

Naming convention: `OverrideTier` is the *internal* three-tier enum
used only inside `internal/interop/overrides/`. `ResolutionTier`
(defined in `internal/interop/mutability.go`) is the broader 8-tier
ladder. The two never appear in the same field or function
parameter; the merge translates between them at leaf construction.

After merge, `Effective.Source` records which tier of the broader
8-tier resolution ladder produced the value: `TierUserOverride`
(ladder tier 1) when a user-project or user-dep entry won, or
`TierShippedOverride` (ladder tier 4) when only a shipped entry was
present. `Classify` reads this value to decide where in the ladder
the override fits, and §11's warning predicate reads it to
suppress warnings on override-backed classifications.

**Duplicate-detection rules:**

- *Within an `OverrideTier`:* two entries occupying the same slot
  is a hard error (`ErrDuplicateMember`, carrying both `Origin`s).
  This is what enforces "one source of truth per tier" — you can't
  have two `overrides/*.esc` files in the same project both
  claiming `Array.prototype.map`.
- *Across `OverrideTier`s:* the lower-precedence entry is silently
  dropped. This is the whole point of the tier system — user
  overrides shadow shipped ones without complaint.

The same shadowing applies to `@all_pure`: a module pragma at a
higher tier wins over the same pragma at a lower tier, but does
not stack with per-member overrides — explicit per-member entries
at any tier always beat the synthesised "non-mutating by default"
leaves from a lower-tier `@all_pure`.

### 5.2 Package layout

New subpackage `internal/interop/overrides/` with the following
files:

- `store.go` — `OverrideStore` type and public lookup API.
- `loader.go` — discovery walk + filesystem reading.
- `merge.go` — eager merge of override decls onto upstream `.d.ts`.
- `consistency.go` — signature-shape consistency check (arity /
  non-receiver param types / return type).
- `errors.go` — typed merge errors (see "Error categories" below).
- `store_test.go`, `loader_test.go`, `merge_test.go`,
  `consistency_test.go` — unit coverage.

### 5.3 Core data types

The shape is a **scope tree** mirroring the upstream `.d.ts` nesting
(module → namespace/class → instance/static → kind → name), not a
flat map. Every map key is a plain string, so the `QualIdent`
map-key problem doesn't arise; lookups walk the tree.

```go
// Scope is the top of the resolved override tree.
type Scope struct {
    Modules map[string]*ModuleScope // "" = global; "lodash", "fs", etc.
}

type ModuleScope struct {
    Free    map[string]*Effective // free functions, vals, type aliases
    Owners  map[string]*OwnerScope // class / interface / namespace name
    AllPure bool                  // module-level @all_pure pragma
    Origin  Origin                // declaring file for diagnostics
}

// OwnerScope is a class, interface, or namespace. Namespaces can
// nest; classes/interfaces hold members.
type OwnerScope struct {
    Nested   map[string]*OwnerScope // namespace.Class, Class.InnerClass, ...
    Instance *MemberSet
    Static   *MemberSet
    Origin   Origin
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
// from interop.ResolutionTier (the 8-tier classification ladder)
// — OverrideTier is only used inside this package to drive the
// internal three-tier collapse (§5.5a). The merge translates the
// winning OverrideTier into a ResolutionTier on the resulting
// Effective.Source.
//
// Lower integer = higher precedence (UserProject beats UserDep
// beats Shipped). This matches the convention used by
// ResolutionTier where TierUserOverride = 1 outranks
// TierShippedOverride = 4.
type OverrideTier int
const (
    OverrideTierUserProject OverrideTier = iota // requirements tier 1b
    OverrideTierUserDep                         // requirements tier 1a
    OverrideTierShipped                         // requirements tier 4
)

// Effective is the merged result for a single member. It carries
// no key — its location in the tree is the key.
type Effective struct {
    Type       type_system.Type       // post-merge type
    Mut        bool                   // receiver mutability (for methods)
    Source     interop.ResolutionTier
    Provenance []Origin               // contributing files
}

type Origin struct {
    Kind     OriginKind // UpstreamDTS | OverrideFile
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

// OverrideStore wraps the post-merge effective scope. Per-tier
// pre-merge scopes exist only inside loader.Load (used to run
// within-tier duplicate detection and the slot-by-slot collapse
// of §5.5a) and are not retained on the store — every diagnostic
// that needs provenance reads Effective.Provenance, which already
// carries the contributing Origins.
type OverrideStore struct {
    Effective *Scope // merged across all OverrideTiers + upstream
}

// Resolve walks Effective by Path. Returns nil if the path has no
// override (the caller falls through to lower tiers in Classify).
func (s *OverrideStore) Resolve(p Path) *Effective
```

### 5.4 Canonicalising member names

Computed keys (§2.2 accepts `[Symbol.iterator]` and `["foo bar"]`)
are stored in `MemberSet.Methods` (etc.) under a canonical string:

- Plain identifier `foo` → `"foo"`.
- Qualified path `Symbol.iterator` → `"[Symbol.iterator]"`.
- String literal `"foo bar"` → `"[\"foo bar\"]"`.

A `canonicalName(PropertyKey) string` helper lives next to the
`Scope` types and is the single source of truth for this mapping —
the override-parser, the upstream-AST consumer, and `Path → walk`
all call it.

### 5.5 Discovery & loading

`loader.Load(root string, deps []DepInfo, shipped fs.FS) (*OverrideStore, []error)`:

1. Walk `shipped` (embedded `fs.FS` populated in §6) — emit
   `Entry`s with `OverrideTier = OverrideTierShipped`.
2. For each dep in `deps`, walk `<dep.Dir>/overrides/**/*.esc` — emit
   with `OverrideTier = OverrideTierUserDep`. `dep.Dir` is the
   directory containing that dep's `package.json`.
3. Walk `<root>/overrides/**/*.esc` — emit with
   `OverrideTier = OverrideTierUserProject`.
4. Parse each file via the existing `internal/parser` entry point;
   reject files with parse errors as hard errors.
5. Build one `Scope` per `OverrideTier` by walking parsed decls and
   inserting each into the appropriate `MemberSet` slot. Within an
   `OverrideTier`, inserting into an already-occupied slot is
   `ErrDuplicateMember` (carries both files' `Origin`s). Cross-tier
   shadowing is handled by §5.5a, not here.

### 5.5a Internal three-tier collapse

Before the merge against upstream (§5.6) runs, the three per-tier
scopes from §5.5 are collapsed into a single override scope. The
collapse walks the trees in parallel, slot-by-slot:

```
collapsed[slot] := first non-nil of (
    userProject[slot],
    userDep[slot],
    shipped[slot])
```

Concretely, the walk descends `Modules` → `Owners` → (`Instance` |
`Static`) → (`Methods` | `Getters` | `Setters` | `Properties` |
`Ctor`) and takes the highest-precedence non-empty leaf at every
key. `ModuleScope.AllPure` collapses the same way: a higher-tier
`@all_pure` shadows a lower-tier one entirely (no stacking).

The collapsed scope records, for each surviving leaf, the
`OverrideTier` it came from on a transient field that §5.6 reads
when stamping `Effective.Source`. The per-tier scopes are dropped
after collapse — they have no consumer past this point.

### 5.6 Eager merge pass

`merge.Apply(upstream *Scope, store *OverrideStore) (*Scope, []error)`:

The merge is a recursive tree walk: zip the upstream scope with each
tier's override scope (highest precedence wins per slot) and produce
a fresh `Effective` for every leaf. Per node:

- **Module level.** Walk upstream `Modules`. For each module, recurse
  into `Free` (entry-by-entry) and `Owners`. If the override side has
  `AllPure = true`, that's recorded on the resulting `ModuleScope`
  and consulted at leaf construction (see below).
- **Owner level.** Recurse into `Nested`, `Instance`, `Static`.
  Static/instance never merge into each other (they're separate
  fields). Nested namespaces match by name.
- **MemberSet level.** Each kind (Methods/Getters/Setters/Properties)
  has its own slot — getter/setter independence falls out of the
  shape.

Generics arity is checked at two places:

- **Owner-level generics** (class/interface type parameters) are
  checked when entering an `OwnerScope`: if the override's owner
  declares a different arity than upstream, emit
  `ErrGenericArityMismatch{Path: Path{Owner: ...}, ...}` and skip
  merging this owner's body.
- **Method-level generics** (per-signature type parameters on a
  method or free function) are checked inside
  `consistency.Check` (§5.7), since they're part of the
  per-signature equivalence contract.
- **Leaf.** Construct `Effective`:
  - If only upstream: leave `Source` unstamped (zero value), so
    `Classify`'s later tiers determine the final classification.
  - If only override and member-presence requires upstream
    (`@all_pure` is the one case that doesn't): emit
    `ErrUnknownMember` with the available-name list pulled from the
    sibling slots in upstream's MemberSet.
  - If both: apply overload collapsing (override's overload set
    replaces upstream's wholesale), run `consistency.Check`, and
    emit the merged `Effective`.
  - If `ModuleScope.AllPure` is true and the slot is a method
    without an explicit override: synthesise an `Effective` with
    `Mut = false` and `Source` set per the contributing
    `OverrideTier` — `TierUserOverride` for UserProject/UserDep,
    `TierShippedOverride` for Shipped — preserving upstream's full
    type.

Originals are not mutated; merge builds a fresh `Scope`.

### 5.7 Signature-shape consistency check

`consistency.Check(override, upstream *type_system.FuncType, path Path) error`
(called from `merge.Apply`):

- Arity (excluding `this` receiver) must match.
- Each non-receiver parameter type must be equivalent.
- Return type must be equivalent.
- On mismatch return `ErrSignatureMismatch{Path, Field, Override,
  Upstream}` where `Field` is `"arity" | "param[i]" | "return"`.

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
    Upstream string // pretty-printed upstream side
    OverrideOrigin Origin
}
type ErrGenericArityMismatch struct { Path Path; Override, Upstream int }
```

All implement `error` with messages that name the file and member.
Surfaced through the existing `internal/checker/error.go` error
channel (or `interop`'s, whichever owns interop-time diagnostics).

### 5.9 Diagnostic format

Every diagnostic that names a classified member appends its
provenance chain — the upstream `.d.ts` location plus each
override file that contributed. The chain is `Effective.Provenance`
rendered as `<file>:<line>` lines under the main message:

```
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
    mod := s.Effective.Modules[p.Module]
    if mod == nil { return nil }
    if p.Kind == KindFree {
        return mod.Free[canonicalName(p.Name)]
    }
    owner := walkOwner(mod.Owners, p.Owner) // descends QualIdent path
    if owner == nil { return nil }
    set := owner.Instance; if p.Static { set = owner.Static }
    if set == nil { return nil }
    name := canonicalName(p.Name)
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

`walkOwner` recursively descends a `*Member` `QualIdent` from
left-most identifier down. A nil `Owner` is reserved for `KindFree`.

### 5.11 `Classify` integration

Extend `ClassifyContext` with `Store *overrides.OverrideStore` (nil
is allowed and means "no overrides registered"). `Classify` calls
`Store.Resolve(path)` exactly once at the very top of the cascade.
The merge has already decided whether a user-project, user-dep, or
shipped entry won, and stamped that decision on `Effective.Source`
as either `TierUserOverride` or `TierShippedOverride`. So:

- If `Resolve` returns nil: fall through to tier 2 (`@esctype`).
- If `Resolve` returns an `Effective` with `Source =
  TierUserOverride`: the result lands at ladder tier 1.
- If `Resolve` returns an `Effective` with `Source =
  TierShippedOverride`: the result lands at ladder tier 4 — but
  *only* if no earlier tier (2 or 3) supersedes it. Concretely:
  `Classify` evaluates tier 1 (the `TierUserOverride` case), then
  tier 2 (`@esctype`), then tier 3 (strong signals), and only
  then accepts a `TierShippedOverride` hit. The override store
  thus contributes to two non-adjacent ladder rungs; the easiest
  shape in code is to consult the store once at the top, hold the
  result, and use it at the appropriate point.

In all cases the hit returns `ClassifyResult{Mut: eff.Mut, Source:
eff.Source, Replacement: eff.Type}` (Replacement defined in §9.3).
The `Effective.Type` also displaces the upstream type at the call
site that constructs the class's effective member map — that wiring
lives in `decl.go`, not `Classify`.

### 5.12 Tests

- `loader_test.go`: synthetic fs with files at all three tiers;
  assert grouping, precedence, duplicate-within-tier error.
- `merge_test.go`: hand-rolled upstream + override pairs; assert
  resulting `Effective`. Cover overload collapsing,
  override-defined overloads, getter/setter independence,
  static/instance separation, `@all_pure`.
- `consistency_test.go`: each of arity, param-type, return-type,
  generics-arity mismatches produces the right error.
- Integration: a fixture under
  `fixtures/interop_mutability/overrides_loaded/` with a real
  `package.json` + `overrides/foo.esc` + a `.d.ts` it references.

Exit criteria: loader + merge covered by unit tests; `Classify`
consults the merged store but no overrides are shipped yet
(§6 ships them).

## 6. Shipped overrides

Goal: author the data tables that the resolver loads at startup.

### 6.1 Bundling mechanism

Shipped override `.esc` files live under
`internal/interop/overrides/data/` and are embedded into the binary
with `//go:embed data/builtins/* data/libs/*` declared in
`overrides/data.go`:

```go
//go:embed data
var ShippedFS embed.FS
```

`loader.Load` accepts `ShippedFS` as its `shipped` argument so tests
can substitute a synthetic FS without touching disk.

### 6.2 Module-name mapping

A shipped override file declares which TS module(s) it applies to
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

- **Consistency test against upstream `.d.ts`.** Test in the
  compiler suite that runs over every shipped override entry where
  a corresponding upstream `.d.ts` exists:
  - Built-in symbols → TS lib `.d.ts` set bundled with the
    `typescript` version pinned in the repo's root
    [package.json](../../package.json) (currently `^5.7.2`).
    Bumping `typescript` in `package.json` is what bumps the
    consistency baseline.
  - FP / immutability libraries → corresponding `@types/*` package
    (or the library's own bundled types), also pinned via
    `package.json` alongside the shipped override.
  For each entry, look up the upstream declaration, compare
  non-receiver arity / parameter types / return type under the
  same mapping the merge uses, and fail the build on divergence.
  Libraries that ship no upstream types are exempt by definition.
  Bumping any pinned version surfaces drift as a deliberate
  fix-up step.

  **Sourcing.** Read the bundled `.d.ts` directly from the
  installed `node_modules/typescript/lib/lib.*.d.ts` and
  `node_modules/@types/<lib>/...` at test time — no separate
  vendoring. The repo's `package.json` is the single pin; CI's
  `npm install` step produces the inputs. Bumping is a one-step:
  change the version in `package.json`, run the consistency test,
  resolve any reported drift.

  The consistency test (`shipped_consistency_test.go`) iterates
  every entry in `ShippedFS`, parses the corresponding upstream
  declaration from `testdata/upstream/`, and calls
  `consistency.Check` — the same code path used by user-override
  merging in §5.

Tests:

- Fixture per library under
  `fixtures/interop_mutability/shipped_<lib>/` that imports a
  representative subset and asserts the receiver mutability via
  call-site type errors (mutate through immutable binding fails).
- `shipped_consistency_test.go` as described above.
- A regression test asserting that the embedded FS is non-empty
  and every embedded file parses.

Exit criteria: built-in counter-examples (`Date.setHours` mutates,
`toSorted` doesn't, `Object.assign` mutates target) all classify
correctly; consistency test green against pinned upstream versions.

## 7. Heuristics (tiers 5–7)

Goal: implement the remaining tiers so unknown TS APIs get useful
classifications.

All three tiers live in `internal/interop/mutability.go` as new
`classifyTier5` / `classifyTier6` / `classifyTier7` functions called
from `Classify` in tier order. Each returns
`(ClassifyResult, bool)` like the existing `classifyTier2`.

### 7.1 Tier 5: primitive wrappers

The classification only applies to the four built-in global
constructors, not to any user-defined class that happens to share a
name. `ClassifyContext` carries an `OwnerScope` flag distinguishing
"this class came from the ambient/global scope of a TS lib `.d.ts`"
from "this class came from a user module."

```go
var primitiveWrapperClasses = set.FromSlice([]string{
    "Number", "BigInt", "String", "Boolean",
})

func classifyTier5(ctx ClassifyContext) (ClassifyResult, bool) {
    if !ctx.OwnerIsGlobalBuiltin {
        return ClassifyResult{}, false
    }
    if primitiveWrapperClasses.Contains(ctx.ClassName) {
        return ClassifyResult{Mut: false, Source: TierPrimitiveWrapper}, true
    }
    return ClassifyResult{}, false
}
```

`OwnerIsGlobalBuiltin` is true iff the declaring `.d.ts` is part of
the TS standard-library set bundled at the path used by the §6
consistency test (`node_modules/typescript/lib/lib.*.d.ts`). A
user-defined class named `String` in the consuming project's
`.d.ts` therefore does not match.

(`Symbol` is intentionally excluded — it has no mutable surface in
practice but the requirements don't list it; revisit if needed.)

### 7.2 Tier 6: `get*` prefix rule

Requirements §"Core principles" #4 names three mutate-on-miss
shapes that must not be caught by the `get*` rule: `getOrInsert*`,
`getOrUpdate*`, `getOrCreate*` (note: `getOrDefault*` is *not* an
exception — it returns a default without writing). These are
short-circuited *before* the tier-6 match so tier 7's mutating-
prefix logic can claim them.

```go
// getOrMutatingPrefixes are name shapes where the leading "get"
// is followed by a mutating action. Tier 6 must not classify
// these as non-mutating; tier 7 picks them up via its mutating-
// prefix list.
var getOrMutatingPrefixes = []string{
    "getOrInsert", "getOrUpdate", "getOrCreate",
}

func classifyTier6(ctx ClassifyContext) (ClassifyResult, bool) {
    m, ok := ctx.Member.(*dts_parser.MethodDecl)
    if !ok { return ClassifyResult{}, false }
    name := identName(m.Name); if name == "" { return ClassifyResult{}, false }
    if !strings.HasPrefix(name, "get") || len(name) < 4 ||
        !unicode.IsUpper(rune(name[3])) {
        return ClassifyResult{}, false
    }
    for _, p := range getOrMutatingPrefixes {
        if strings.HasPrefix(name, p) &&
            (len(name) == len(p) || unicode.IsUpper(rune(name[len(p)]))) {
            return ClassifyResult{}, false // fall through to tier 7
        }
    }
    return ClassifyResult{Mut: false, Source: TierGetPrefix}, true
}
```

`identName` extracts the name string from a `PropertyKey` (Ident or
string-literal computed key); returns `""` for symbol-keyed members.

The mutating-prefix list in §7.3 must include `getOrInsert*`,
`getOrUpdate*`, `getOrCreate*` so that the fall-through actually
lands on a mutating classification. Requirements §"Heuristics" →
Mutating-name signals lists `getOrInsert` / `getOrCreate` /
`getOrUpdate` as examples of "both prefixes → prefer mutating," so
this matches the spec.

### 7.3 Tier 7: name-based heuristics

Requirements §"Heuristics" → Medium signals mixes two shapes:
*prefixes* (e.g. `is*`, `to*`, `find*`) and *exact-match keywords*
that are themselves whole method names (e.g. `contains`, `equals`,
`indexOf`, `forEach`, `keys`, `values`, `entries`, `at`, `every`,
`some`). Tier 7 needs both: the prefix matcher fires when the name
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
func classifyTier7(ctx ClassifyContext) (ClassifyResult, bool) {
    name := identName(memberName(ctx.Member))
    if name == "" { return ClassifyResult{}, false }
    isNonMut := matchesAnyPrefix(name, nonMutatingPrefixes) ||
                stringInSlice(name, nonMutatingExact)
    isMut    := matchesAnyPrefix(name, mutatingPrefixes) ||
                stringInSlice(name, mutatingExact)
    switch {
    case isMut: // requirements: if both, prefer mutating
        return ClassifyResult{Mut: true,  Source: TierNameHeuristic}, true
    case isNonMut:
        return ClassifyResult{Mut: false, Source: TierNameHeuristic}, true
    }
    return ClassifyResult{}, false
}
```

The four slices are package-level `var` so they can be re-synced
from requirements without changing the matching function.

### 7.3a Inheritance fallthrough

Requirements §"Resolution order" → Inherited classifications: when
a subclass method has no direct match at any tier, the
classification is inherited from the nearest base-class method, and
the inherited result carries the *base method's* tier — so an
inherited classification from a base whose tier was an explicit
signal stays explicit, and one inherited from a heuristic stays
uncertain. Inheritance never upgrades certainty.

This is implemented as a fallthrough wrapper around the per-tier
cascade, not as a new tier number. After tiers 1–7 all miss on the
subclass's member, `Classify` re-runs itself against the same
member name on the nearest base class. The recursion terminates at
the root of the inheritance chain; if no base method exists,
`Classify` falls through to tier 8 (default mutating).

```go
func Classify(ctx ClassifyContext) ClassifyResult {
    if r, ok := classifySelf(ctx); ok { return r } // tiers 1..7
    if base, ok := nearestBaseMember(ctx); ok {
        return Classify(base) // inherited tier carries through
    }
    return ClassifyResult{Mut: true, Source: TierDefault}
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

- `TestClassifyTier5_PrimitiveWrappers` — `Number`, `BigInt`,
  `String`, `Boolean` methods classify non-mutating; arbitrary class
  doesn't.
- `TestClassifyTier6_GetPrefix` — `getFoo` → non-mutating;
  `get` (bare), `getter`, `gets`, `setFoo` → fall through.
- `TestClassifyTier7_NamePrefixes` — every prefix in both lists; a
  name matching both (e.g. `setToString`) lands mutating; a name
  matching neither falls through to tier 8.
- `TestClassifyTierOrdering` — a method that would match multiple
  tiers (e.g. a `Number` method named `setFoo`) resolves at the
  earliest matching tier. Verifies wiring, not heuristic content.
- `TestClassifyInheritance` — covers §7.3a: subclass method with
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

The `dts_parser` needs to surface leading TSDoc comments on each
decl — verify what it already retains and extend if necessary.
Add a field like `LeadingDoc string` (raw block contents minus
`/**` `*/` markers) to the relevant decl nodes, populated by the
parser.

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

Add `classifyTier1(ctx)` consulting the parsed `@esctype` tag on
`ctx.Member.LeadingDoc`. The function returns the full effective
type as well as the mutability bit, so the caller in `decl.go`
substitutes the entire type — not just the receiver flag — when
tier 1 hits. Extend `ClassifyResult`:

```go
type ClassifyResult struct {
    Mut         bool
    Source      ResolutionTier
    Replacement *type_system.Type // non-nil for tier 1, optional otherwise
}
```

Tiers other than user overrides and `@esctype` leave `Replacement`
nil; the override store (§5) and the `@esctype` tag (this section)
both set it, and `decl.go` uses it to replace the upstream type
wholesale. User overrides still outrank `@esctype` per requirements
§"Round-tripping" → Precedence: `Classify` evaluates the override
store's `TierUserOverride` path *before* `classifyTier1`. If both
apply, the store wins and the symbol's `Source` is
`TierUserOverride` — never `TierEsctype` — which is what the "user
override beats `@esctype`" fixture verifies. The store's
`TierShippedOverride` path is evaluated *after* `classifyTier1`
(see §5.11), so `@esctype` outranks shipped overrides.

### 9.4 TSDoc tooling

`cmd/escalier`'s build step writes outputs under `build/`
([cmd/escalier/build.go:117](../../cmd/escalier/build.go#L117)).
Emit `tsdoc.json` once at `build/tsdoc.json` so consumers'
TSDoc tooling recognises `@esctype`:

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
passes; classification tier 1 wired in.

## 10. `implements` mutability conformance

Goal: enforce that a class implementing an interface matches the
interface's mutability annotations on each implemented method (per
requirements §"Policy decisions" — `implements` requires mutability
conformance).

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

- If both have a `SelfParam`: their `Mut` fields must be equal.
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
    ClassProvenance      []overrides.Origin // chain from class-side Effective
    InterfaceProvenance  []overrides.Origin // chain from interface-side Effective
    Span                 ast.Span
}
```

Message format:

```
class `Foo` does not conform to `Bar`: method `baz` declares `mut self`
but interface declares `self`
  class side resolved via tier 7 (name heuristic) — add an explicit
  `self` or `mut self` annotation, an override entry, or an
  `@esctype` tag to make this deterministic
```

When either source is in `{TierPrimitiveWrapper, TierGetPrefix,
TierNameHeuristic}`, the diagnostic includes the "add an explicit
signal" suggestion — this is the one place a heuristic produces a
hard error rather than the §11 warning. `TierUserSource` and the
explicit tiers (`TierUserOverride`, `TierEsctype`,
`TierExplicitSignal`, `TierShippedOverride`) do not trigger the
suggestion.

**Import direction.** `checker` already depends on `interop` for
type construction; the new error type extends that direction with
`interop.ResolutionTier` and the merge package's `overrides.Origin`.
`interop` and `overrides` must not import `checker` (overrides
errors live in `internal/interop/overrides/errors.go` per §5.8 and
are surfaced through the checker's existing error channel by value,
not by interface reference back into checker). Confirm at
implementation time that no checker → interop → checker cycle
exists.

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
- `err_heuristic_source/` — class member name matches a tier-7
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

### 11.2 Persisting the tier through to call sites

The receiver mode currently lives on `FuncType.SelfParam.Mut`. The
classification tier produced in §5 / §7 needs to survive interop
conversion long enough to reach the checker.

Add a non-serialised field `Source ResolutionTier` on `SelfParam`,
set by `interop/decl.go` when it constructs the type from
`ClassifyResult`. The field is metadata, not part of unification.

**Default tier for user-authored Escalier source.** Ladder tiers
1–8 cover the interop classification path. A `SelfParam`
constructed by the checker from `.esc` source (a user-written
`mut self` or `self` annotation) is not produced by `Classify` at
all, so its `Source` must sit outside the ladder. This is
`TierUserSource`, defined in §3 as the zero value of
`ResolutionTier`. `Classify` never returns it; checker-constructed
`SelfParam`s carry it implicitly; predicates that branch on tier
(e.g. the §11 uncertainty warning, §10's heuristic-suggestion
clause) must explicitly handle it as "authoritative, never
uncertain."

(A side table from `*FuncType` to tier is heavier plumbing and
preferred only if `SelfParam` turns out to be shared across
multiple call sites that need different sources — keep this option
in reserve.)

### 11.3 Call-site check

In [internal/checker/check_implements.go](../../internal/checker/check_implements.go)'s
neighbour file that handles method invocation (likely
`infer_expr.go` — verify; this is where mutability conflicts on
receivers are currently surfaced), at the point that compares the
call's receiver mutability to the method's `SelfParam.Mut`:

```go
if !methodNeedsMut && receiverIsImmut {
    if opts.WarnUncertainMutability &&
        isHeuristicTier(method.SelfParam.Source) {
        emitWarning(UncertainMutabilityWarning{...})
    }
}

func isHeuristicTier(t interop.ResolutionTier) bool {
    return t == interop.TierPrimitiveWrapper ||
           t == interop.TierGetPrefix       ||
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
    Provenance []overrides.Origin // chain from the callee's Effective
    Span       ast.Span
}
```

Message:

```
warning: call to `foo.bar()` treats receiver as non-mutating based
on a name heuristic (tier 7); add an override entry, `@esctype`
tag, or explicit `readonly this` to make this guarantee explicit
```

The message names the tier ("name heuristic", "get prefix",
"primitive wrapper") rather than the bare number.

### 11.5 Negative cases

The warning must **not** fire when:

- `Source ∈ {TierUserSource, TierUserOverride, TierEsctype,
  TierExplicitSignal, TierShippedOverride, TierDefault}`. (The
  `TierDefault` case is "assume mutating"; non-mutating calls
  can't reach that tier, so it can't trigger the warning in
  practice — listed for completeness.)
- The receiver is mutable (no immutable-call concern).
- The method is mutating (no contract risk for the immutable caller —
  this would be a hard error, not a warning).

### 11.6 Tests

`fixtures/interop_mutability/uncertain_warning/`:

- `heuristic_warns/` — non-mutating call resolved by tier 7;
  flag on → warning, flag off → silent. Compare diagnostic
  snapshots for both runs.
- `override_silent/` — same call but a shipped override pins the
  classification; flag on → silent.
- `esctype_silent/` — `@esctype` provides the type; flag on → silent.
- `strong_signal_silent/` — `readonly this` on the method; flag on
  → silent.

Plus a checker unit test asserting `isHeuristicTier` returns true
for exactly tiers 5/6/7 and false otherwise.

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
  shipped.
- `@esctype` `*/`-in-strings escape — `*\/` on emit, unescape on
  consume (see §2).
- Type printer reparseability — covered by §8.
- Consistency-test upstream sourcing — read directly from
  `node_modules/typescript` and `node_modules/@types/*`, pinned via
  the repo's root `package.json`.
