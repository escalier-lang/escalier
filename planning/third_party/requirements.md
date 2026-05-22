# Third-party deps: lazy .d.ts → .esc conversion with overrides

**Status:** Draft requirements for the third-party workstream
extracted from the broader interop proposal.

**Provenance:** Extracted from
[../interop_mutability/dts_to_esc_proposal.md](../interop_mutability/dts_to_esc_proposal.md).
That document bundles this workstream with a separate builtins
effort; this file isolates the third-party half so it can be
planned, scoped, and shipped on its own track.

**Depends on:** [../builtins/requirements.md](../builtins/requirements.md).

This is a **hard gate**: the third-party workstream cannot begin
implementation until the builtins workstream is complete. The
third-party path is built entirely on top of the converter the
builtins workstream produces — there is no parallel track and no
partial start. Specifically, builtins must have delivered:

- `tools/dts_to_esc/` — the Go converter that translates TypeScript
  `.d.ts` declarations directly into Escalier declaration AST,
  refactored to be callable from a non-`main` package (i.e. exposed
  as a library, not just a CLI entry point). The builtins workstream
  builds this as a one-shot used during bootstrap; the third-party
  workstream invokes the same converter at compile time.
- The declaration-level printer audit (builtins FR13, see
  [builtins/requirements.md FR13](../builtins/requirements.md)).
  Until the printer can faithfully emit every declaration form the
  converter produces, neither workstream can land. Owned by the
  builtins workstream.
- A decision on the converter's public API contract (package path,
  exported function signatures, input/output types) — see Open
  Questions.

The third-party workstream shall not begin coding until all of the
above are complete and the converter library is callable from a
non-`main` package with a stable signature.

## Goals

- Make every third-party npm dep with `.d.ts` typings (whether
  bundled or shipped via `@types/*`) usable from Escalier code
  without manual authoring of *full* `.esc` files. Users may still
  write small override fragments to refine the auto-converted
  baseline; the goal is to eliminate the need to fork or hand-write
  an entire replacement module.
- Provide a precision path — Escalier-aware refinements layered on
  top of the auto-converted baseline — for cases where heuristic
  classification or missing annotations (`throws`, lifetimes) are
  inadequate.
- Keep first-compile cost amortized: convert once per
  `<pkg>@<version>` (plus override-fragment content hash), cache the
  result, and reuse it across subsequent compiles on the same
  machine.
- Delete the current runtime interop pipeline (`ConvertModule`,
  the runtime `Classify`, trio-fusion, `mergeReadonlyVariant`)
  once the new cache-backed path is stable.

## Scope

In scope:

- Lazy on-first-compile conversion of third-party `.d.ts` into
  `.esc` baselines.
- A two-tier override merge model that lets package authors and
  end users refine the baseline without forking.
- A compile-time cache under `node_modules/.cache/escalier/` keyed
  on package name + version + override-fragment content hashes +
  compiler version.
- A `escalier cache clean` CLI escape hatch.
- Migration steps for wiring the converter into the compile
  pipeline and deleting the runtime interop code.

Out of scope (owned by the builtins workstream or other efforts):

- The ambient `builtins.esc` file and the `std:*` / `dom:*`
  pseudo-package layout.
- URI-scheme imports (`import "std:math"`) and the `?local` /
  `?nested` / `?flat` flag modifiers.
- Cross-package augmentation via registry interfaces.
- Prelude changes that swap lib-walking for `builtins.esc`.
- Always-current API surface plus codegen polyfill insertion.
- Intrinsic type handling in the checker.
- The declaration-printer audit itself (builtins FR13,
  [builtins/requirements.md](../builtins/requirements.md)) —
  consumed here as a precondition.
- The home of `interop.Classify`. The builtins workstream owns this
  decision; the current expectation is that `Classify` continues to
  live in `internal/interop/` and is called from there by both
  workstreams. See the builtins requirements for the authoritative
  statement.

## Terminology

This section defines terms used throughout this document.

- **Baseline** — the `.esc` declaration AST produced by running the
  `tools/dts_to_esc/` converter over a dep's `.d.ts`, before any
  override merging. The converter is a pure AST-to-AST translator;
  it bypasses `type_system.Type` and the checker entirely. The
  baseline is never persisted on its own.
- **Override fragment** — an `.esc` file containing targeted
  refinements (receiver mutability, `throws`, lifetimes, narrower
  types) that the merge model layers onto a baseline.
- **Merged result** — the output of merging a baseline with any
  applicable override fragments. This is the artifact written to
  the cache and loaded into the checker.
- **Tier 1a / Tier 1b** — the two override tiers defined in FR2.
  The "Tier 1a/1b" terminology is inherited from the parent
  proposal; see Open Questions for the canonical definition.
- **Converter** — `tools/dts_to_esc/`, the AST-to-AST translator
  owned by the builtins workstream and reused here.
- **Builtin registry** — the mapping from TypeScript lib
  identifiers (e.g. `Array`, `ReadonlyArray`, `Promise`,
  `HTMLElement`) to a `(escalier module, escalier type
  expression)` pair, derived by inverting the `@js(...)`
  decorators on `export declare` items in the committed `std:*`
  and `dom:*` `.esc` files. The converter consults this registry
  to generate import headers and rewrite type references in the
  converted baseline. The registry is owned by the builtins
  workstream; this workstream is purely a consumer.
- **Origin annotation** — an AST-level field populated by the
  converter that records the original TypeScript syntax for a
  rewritten type reference (e.g. `ReadonlyArray<T>` for an
  emitted Escalier `Array<T>`). Used only by diagnostics; ignored
  by the checker and by hand-authored `.esc` source.

## Functional requirements

### FR1. Lazy baseline conversion

On first encounter of a third-party dep whose cache entry is
missing or stale, the compiler shall:

1. Read the dep's `.d.ts` via `internal/dts_parser/`.
2. Convert each TS declaration directly into the corresponding
   Escalier declaration AST using `tools/dts_to_esc/` (shared with
   the builtins bootstrap path, but invoked at compile time rather
   than as a one-shot regenerator). The converter is a pure
   AST-to-AST translator that bypasses `type_system.Type` and the
   checker entirely.
3. Run `interop.Classify` (the existing tier-3/5/6 heuristics) at
   conversion time to seed receiver mutability into the emitted
   AST — e.g. deciding `self` vs `mut self` on each method.
4. Merge the resulting baseline against any present override
   fragments (see FR2) via §5's existing `Merge`.
5. Persist the merged result to the cache (see FR3) and load it
   into the checker.

The raw baseline shall not be persisted on its own. Only the
merged result lives in the cache.

### FR2. Baseline-plus-override merge model

The merge model from §5 of the original proposal is retained
specifically for third-party deps. Two override tiers apply:

- **Tier 1a — per-dep overrides** at
  `node_modules/<pkg>/overrides/*.esc`. Library authors ship
  Escalier-aware refinements alongside their published `.d.ts`.
- **Tier 1b — user-project overrides** at
  `<project>/overrides/*.esc`. End users refine `@types/*` (or any
  third-party dep) without forking.

(The "Tier 1a/1b" naming is inherited from the parent proposal's
tier scheme; see Open Questions for where the canonical glossary
should live.)

Most deps will have no overrides; the baseline becomes the merge
result directly.

The merged cache artifact shall be serialized as `.esc` source
(textual), re-parsed on cache hit through the standard parser. A
binary on-disk format is explicitly **not** used in Phase 1; this
keeps the cache human-inspectable and avoids a second serialization
contract to version. Revisiting in a follow-up if parse cost becomes
a measurable bottleneck is acceptable but not in scope here.

Rationale for retaining the merge model here (but discarding it
for builtins): Escalier does not own upstream `.d.ts`. The
upstream changes on dep updates, users need a refinement path
that does not require forking, and the heuristic baseline carries
no `throws` or lifetime annotations. Uncertainty is the default
for code we do not own; the override mechanism is the precision
path.

### FR3. Cache layout and invalidation

The merged `.esc` shall be written to
`node_modules/.cache/escalier/<pkg>@<version>+<escalier-version>.esc`.

The cache key is the tuple:

```
(pkg-name,
 pkg-version-of-the-.d.ts-files-being-converted,
 content-hash of any applicable .esc override fragments,
 escalier-compiler-version)
```

The package name and version come from `package.json`. The override
content hash is computed over the byte contents of every override
fragment whose target path names this dep (covering both
`node_modules/<pkg>/overrides/` and the user project's
`<project>/overrides/`). The §5 loader already parses overrides
into a target-keyed map, so enumerating "fragments targeting `X`"
is mechanical; hashing their bytes is a one-pass extension.

The cache entry shall be invalidated when any of the following
changes:

- The dep's npm package name or installed version (i.e. the
  `<pkg-name>@<pkg-version>` portion of the key changes).
- The content of any override fragment targeting this dep changes
  (i.e. the override-content-hash portion of the key changes). This
  closes the gap where editing an override would otherwise reuse a
  stale merged result.
- The Escalier compiler version (the fourth component of the key
  tuple). Captures converter improvements and bug fixes without
  requiring users to wipe the cache manually.

Patch releases that quietly re-emit `.d.ts` without bumping a
version are rare and remain a known soft spot — `escalier cache
clean` covers the case if it happens. Aligning with the parent
proposal's
[Third-party deps: lazy convert + baseline-plus-override](../interop_mutability/dts_to_esc_proposal.md#third-party-deps-lazy-convert--baseline-plus-override)
and
[Risks](../interop_mutability/dts_to_esc_proposal.md#risks)
sections: the `<hash>` referenced there is this composite key
(version + override-fragment content hashes + compiler version),
not a full `.d.ts` content hash.

Placing the cache under `node_modules/.cache/escalier/` means
`npm ci` and other clean-install workflows reset it for free.

### FR4. Classify heuristics seed receiver mutability

`interop.Classify` (tiers 3/5/6) shall run at conversion time on
every third-party `.d.ts` declaration. Its output seeds the
`self` / `mut self` choice on emitted methods directly in the
baseline AST, so the merged cache file contains explicit
receiver-mutability annotations.

Because the heuristics run at conversion time rather than at
runtime, the seven-tier runtime `Classify` becomes dead code on
the user-facing checker path (it remains live as a converter
helper). The package location of `Classify` itself is owned by the
builtins workstream; the expectation is `internal/interop/`.

The merged cache artifact is serialized `.esc` source containing
the seeded receiver-mutability annotations as ordinary syntax (no
side-channel encoding); on cache hit it is re-parsed by the
standard parser.

### FR5. `escalier cache clean` CLI subcommand

The CLI shall grow a new subcommand, `escalier cache clean`,
which removes Escalier cache directories. This is the manual
escape hatch when cache invalidation fails to fire (e.g. an
unannounced `.d.ts` change without a version bump).

The subcommand's contract:

- **Default target.** With no flags, operates on the current
  working directory: locates `./node_modules/.cache/escalier/` and
  removes it. If no such directory exists, exits 0 with an
  informational message ("no Escalier cache found at <path>"); this
  is not an error.
- **`--recursive` / `-r`.** Recurses from the current working
  directory and removes every `node_modules/.cache/escalier/`
  directory it finds. Intended for monorepos and pnpm workspaces
  where caches live under each workspace package's `node_modules/`.
  Without this flag, nested caches are left untouched.
- **`--dry-run` / `-n`.** Prints the directories that would be
  removed without removing them. Exit 0.
- **`--path <dir>`.** Operate on `<dir>` instead of the current
  working directory. Composes with `--recursive`.
- **Exit codes.**
  - `0` — success (including the "nothing to clean" case).
  - `1` — partial failure (some directories removed, others failed,
    e.g. permission denied on a subset).
  - `2` — total failure (could not remove any matched cache, or
    invalid flags).
- **Workspace interaction.** The subcommand does not parse
  `package.json` / `pnpm-workspace.yaml`; `--recursive` is a pure
  filesystem walk. Interaction with workspace layouts is tracked
  under Open Questions.

### FR6. Converter shared with the builtins bootstrap

The third-party path shall reuse `tools/dts_to_esc/` without
forking it. The only delta from the builtins bootstrap invocation
is the entry point: at compile time the converter runs on a single
dep's `.d.ts`, emits the baseline AST in memory, hands it off to
the merge code, and returns the cached file path. There is no
partition table (that machinery is specific to the builtins
ambient-vs-pseudo-package split) and no on-disk regeneration of
checked-in source.

### FR7. Import-header generation and type-reference rewriting

The baseline `.esc` produced by the converter shall be
self-contained: every type or value identifier it references must
resolve either to a declaration in the same file or to an explicit
`import` from a `std:*` / `dom:*` pseudo-package. The converter
shall not rely on ambient globals.

To achieve this, the converter shall:

1. While translating each declaration, look up every referenced
   TypeScript lib identifier in the **builtin registry** (see
   Terminology). Identifiers that resolve to nothing locally and
   are absent from the registry are a conversion error (see FM6).
2. Replace the TypeScript identifier with the registry's
   Escalier type expression. The expression encodes shape and
   mutability only (e.g. `mut Array<T>`, `Array<T>`) and is
   **module-unqualified** — the import that brings the name into
   scope is emitted separately in step 3. The registry entry
   itself still carries the `(module, name)` pair so step 3 can
   look it up; only the substituted expression is unqualified.
   This is a **semantic** rewrite, not a rename. Examples that
   drive the requirement:
   - TS `Array<T>` (mutable) → Escalier `mut Array<T>`.
   - TS `ReadonlyArray<T>` → Escalier `Array<T>` (immutable by
     default).
   - TS `Map<K,V>` (mutable) → Escalier `mut Map<K, V>`.
   - TS `ReadonlyMap<K,V>` → Escalier `Map<K,V>`.
   - TS `Record<K,V>` → corresponding Escalier shape.

   Aliased imports (e.g. `import { ImmutableArray as
   ReadonlyArray }`) are explicitly rejected as the rewriting
   strategy. Rationale: Escalier users should see Escalier types,
   not TypeScript types lightly disguised. Diagnostics carry the
   original TS spelling via FR8 for users debugging interop
   issues.
3. Emit grouped `import` statements at the top of the file
   covering every distinct `(module, name)` pair referenced by the
   converted declarations. Imports are deduplicated and sorted
   for deterministic output.

The rewriting rule for mutability is load-bearing: TypeScript
treats `Array<T>` as mutable by convention, so every unwrapped
`Array<T>` in a third-party `.d.ts` must convert to Escalier's
mutable form. The Readonly* variants convert to Escalier's
immutable defaults. Getting this rule wrong silently strips
mutability from third-party APIs; correctness here is a soundness
concern, not a stylistic one.

The examples above are all type-position rewrites. Value-position
rewriting — constructors invoked as values (`new Array(0)`),
constant references (`Math.PI`), and any other place where a
registry identifier appears as a value rather than a type — has
the same shape (lookup, replace, emit import) but separate
correctness concerns (e.g. a constructor's value-position
mutability binding may differ from its type-position one). The
registry must carry both sides for identifiers that appear in
both positions; whether that's one entry with two fields or two
parallel entries is tracked in Open Questions ("Type-vs-value
rewriting parity"). Implementers should treat type-position and
value-position rewriting as independent passes that share the
same registry, not as a single fused pass.

### FR8. Preserve original TS source for diagnostics

For every type reference the converter rewrites under FR7, the
emitted AST node shall carry an **origin annotation** recording
the original TypeScript spelling (e.g. `ReadonlyArray<T>`).
Diagnostics that mention such a type shall include the original
spelling alongside the Escalier rendering when the annotation is
present. Concrete rendering (suffix, parenthetical, structured
field) is deferred to the diagnostic-formatting design; the
contract here is that the information is retained on the AST and
survives serialization to the cached `.esc` file.

The annotation field shall be ignored by the parser when the
checker loads hand-authored `.esc` source — it has no effect on
type identity, equivalence, or assignability. The converter is
the only producer.

The annotation shall round-trip through the textual cache format
(see FR2). A natural encoding is a decorator on the converted
declaration (e.g. `@ts("ReadonlyArray<T>")`); whether per-decl,
per-reference, or per-line is the right granularity is deferred
to the implementation plan.

## Failure modes and error reporting

This section enumerates the failure modes the third-party pipeline
shall handle, with the observable behavior for each. Concrete error
message wording is illustrative — the contract is the category and
exit behavior.

### FM1. `.d.ts` parse failure on a dep

The `internal/dts_parser/` step fails on a dep's `.d.ts`.

- **Observable:** the compile fails with a diagnostic naming the
  dep, the file path, and the underlying parser error. Form:
  `cannot use third-party dep "<pkg>@<version>": failed to parse
  <path/to/file.d.ts>: <parser error>`.
- **Cache:** no cache entry is written for the dep. A subsequent
  retry re-runs the parse.
- **Exit:** non-zero from the compiler.

### FM2. Converter emits invalid AST

`tools/dts_to_esc/` produces an AST that fails internal validation
or fails to print/re-parse cleanly.

- **Observable:** internal-error diagnostic naming the dep and the
  declaration that triggered the failure. Form: `internal error:
  converter produced invalid AST for "<pkg>@<version>" at
  <decl-name>: <validation error>`. This is a bug class, not a user
  error; the message should encourage filing an issue.
- **Cache:** no cache entry written.
- **Exit:** non-zero.

### FM3. Cache directory unwritable

`node_modules/.cache/escalier/` cannot be created or written
(read-only `node_modules`, sandboxed CI, permission issues, full
disk).

- **Observable:** warning diagnostic, not a hard error. Form:
  `warning: cannot write Escalier cache at <path>: <io error>;
  proceeding without cache (subsequent builds will repeat
  conversion work)`. The compile proceeds using the in-memory
  merged result.
- **Cache:** no entry written; the cache is effectively disabled
  for this dep this compile.
- **Exit:** unaffected by cache write failure; controlled by the
  rest of the compile.

### FM4. Override fragment fails to parse

An `.esc` override fragment in `node_modules/<pkg>/overrides/` or
`<project>/overrides/` does not parse.

- **Observable:** parse-error diagnostic with the standard
  Escalier parser error format, pointing at the offending fragment
  file and span. Form: `failed to parse override fragment
  <path>: <parser error>`.
- **Cache:** no cache entry written for any dep whose merge would
  have consumed the broken fragment. Other deps are unaffected.
- **Exit:** non-zero.

### FM5. Two override fragments conflict on the same target

Two fragments (e.g. one Tier 1a and one Tier 1b, or two Tier 1b
fragments) both attempt to override the same target declaration in
incompatible ways that §5's `Merge` cannot reconcile.

- **Observable:** error diagnostic naming both fragment paths, the
  target declaration, and the nature of the conflict. Form:
  `conflicting override fragments for <pkg>::<target>: <path-a>
  and <path-b> both override <field>`. No implicit precedence is
  applied; the user must resolve the conflict explicitly (typically
  by removing or merging one of the fragments).
- **Cache:** no cache entry written for the affected dep.
- **Exit:** non-zero.

A future precedence policy (e.g. "Tier 1b wins over Tier 1a") may
be considered, but is out of scope for Phase 1.

### FM6. Unknown TypeScript identifier with no registry entry

A `.d.ts` references a TypeScript lib identifier that is not
declared locally and is absent from the builtin registry (see
FR7). This typically means the stdlib is missing a declaration
that the third-party API depends on.

- **Observable:** error diagnostic naming the dep, the
  identifier, and the file/span of the reference. Form: `cannot
  convert "<pkg>@<version>": unknown TypeScript identifier
  <name> at <path>:<line>:<col> — no entry in std:/dom: registry`.
  The message should list the likely causes so users can
  triage before filing an issue: (a) the stdlib is missing a
  declaration for a standard TypeScript lib type — file an issue
  so it can be added with an `@js(...)` decorator; (b) the
  `.d.ts` was produced by a newer TypeScript version that
  introduced a lib symbol the stdlib does not yet cover; (c) the
  `.d.ts` is malformed or references a symbol that was never part
  of any TS lib; (d) the symbol is intentionally excluded from
  the stdlib. Encourage users to check the source package or its
  maintainer before filing against the stdlib.
- **Cache:** no cache entry written.
- **Exit:** non-zero.

## Non-functional requirements

### NFR1. First-build latency is acceptable and amortized

The first compile in a fresh checkout will convert every dep's
`.d.ts`. This cold-start cost is real but should be amortized to
zero on subsequent compiles via the cache. Whether the Phase 1
architecture commits to per-package parallelization (and what the
contract looks like — independent conversions, no shared mutable
state, etc.) is unresolved; see Open Questions.

### NFR2. Cache correctness over speed

A stale cache that fails to invalidate when it should will
silently use outdated types — a worse failure mode than a slow
build. Invalidation triggers (FR3) shall be conservative.

### NFR3. Classification quality on auto-converted code

Because tier-3/5/6 `Classify` heuristics run unsupervised over
third-party APIs at conversion time, the classification quality
on auto-converted code is a direct user-visible quality metric.
Misclassifications must be correctable through the override path
(FR2) without requiring users to fork the dep or wait for a new
Escalier release.

## Acceptance criteria

Per-FR acceptance criteria are deferred to the design-doc /
implementation-plan stage that follows this requirements document.
Each FR above is expected to grow a concrete acceptance-criteria
subsection in that follow-up doc (e.g. for FR1: "given a fresh
checkout with `@types/node` installed, the first `escalier build`
produces `node_modules/.cache/escalier/@types/node@<version>+<escalier-version>.esc`
and subsequent builds do not re-invoke the converter").

## Telemetry and observability

The compiler shall surface, at a minimum:

- A `--verbose` (or equivalent) log line per cache miss naming the
  dep, version, and reason (missing entry / stale key / override
  change / compiler-version change).
- A `--verbose` log line per cache hit naming the dep and the
  cache file path.
- Timing for each conversion (wall-clock per dep), behind a
  diagnostic flag.

Specific flag names, structured-log formats, and whether this
emits to stderr vs. a separate sink are deferred to the
implementation plan.

## Backwards compatibility (Phase 1 → Phase 2)

Phase 1 wires the new cache-backed path alongside the existing
runtime interop pipeline. During Phase 1:

- Existing fixtures and user projects shall continue to compile
  with no source changes. The two pipelines coexist; the new path
  is authoritative for deps that go through it.

### Tests broken by the builtins → third-party gap

The builtins workstream replaces the prelude lib-walking with
`builtins.esc` plus the `std:*` / `dom:*` pseudo-packages. The
runtime interop pipeline that processes third-party `.d.ts`
today relies on TypeScript lib names (`Array`, `Promise`,
`HTMLElement`, …) being ambient. Once builtins removes lib-walking
those names are no longer ambient, and the runtime pipeline (which
still exists during Phase 1) can no longer resolve them.

The cheapest remediation is a bounded skip. The mechanics
(helper location, audit pass, CI guard, lifecycle) live in the
builtins implementation plan, since that is where they are
introduced: see
[../builtins/implementation_plan.md § 8.1 Third-party `.d.ts` fixture carve-out](../builtins/implementation_plan.md#81-third-party-dts-fixture-carve-out).
Summary of the contract:

- Tests that exercise third-party `.d.ts` content shall be marked
  via a single helper (e.g. `testutil.SkipUntilFR7(t, reason)`)
  when builtins lands. The helper is the only call site, so
  re-enabling is one grep + one deletion.
- FR7's definition-of-done shall include "no remaining
  `SkipUntilFR7` markers in the test tree." Phase 1 cannot be
  declared complete while any survive.
- A CI guard shall fail the build if `SkipUntilFR7` is still
  referenced after FR7's tracking issue is closed. This prevents
  the skip from rotting silently.

Rationale: a compatibility shim that lets the runtime pipeline
keep resolving TS lib names through `builtins.esc` was
considered. It works, but it is throwaway code with a narrow
window of utility (deleted by Phase 2 anyway), and main is not
expected to be an external-facing branch during the gap. Skipping
preserves the obligation to fix without adding throwaway shim
code.
- The cache layout (`node_modules/.cache/escalier/<key>.esc`) is
  considered a public-ish surface for the duration of Phase 1, in
  the sense that `escalier cache clean` must keep working against
  it. The on-disk format may change between Escalier versions
  (since the compiler-version is part of the cache key, format
  changes simply invalidate the cache).

Phase 2 removes the runtime interop pipeline. After Phase 2:

- The `interop.ConvertModule` call site and the runtime
  invocation of `Classify` are gone; any external code (tests,
  tooling) that depended on them must migrate to the converter
  library. Within this repo this is owned by the Phase 2 work.

## Security considerations

The cache stores `.esc` source generated by feeding third-party
`.d.ts` through a converter the Escalier compiler will then load
and check. This places `@types/*` packages and any other
`.d.ts`-shipping dep on the supply-chain attack surface for
Escalier projects, in the same way they already are for TypeScript
projects.

Specific considerations:

- **Malicious `@types/*`.** A compromised `@types/*` package could
  ship `.d.ts` crafted to produce baseline `.esc` that misleads
  the checker (e.g. declaring a function as `throws nothing` when
  the runtime throws). This is no worse than the TypeScript
  baseline but should be acknowledged. Override fragments are the
  remediation path for known-bad upstreams.
- **Cache poisoning.** The cache directory lives under
  `node_modules/.cache/escalier/`. Anything that can write to
  `node_modules/` can poison the cache. We rely on the existing
  trust boundary around `node_modules/` (i.e. if it's compromised,
  the build is already compromised).
- **Override fragment trust.** Tier 1a fragments ship with the dep
  and inherit the dep's trust level. Tier 1b fragments live in the
  user project and are trusted by definition.
- **No code execution at conversion time.** The converter is a
  pure AST-to-AST translator; it does not evaluate `.d.ts` content
  beyond parsing. This bounds the attack surface to parser bugs
  and downstream checker behavior.

A dedicated security review is out of scope for this requirements
doc but should precede Phase 1 cutover.

## Migration phases

### Phase 1 — Third-party lazy cache

Wire the converter into the compile pipeline. Uncached deps
trigger:

1. Baseline conversion via `tools/dts_to_esc/`.
2. Merge against §5 tier 1a/1b overrides.
3. Write to `node_modules/.cache/escalier/`.

Cached deps load directly from the cache.

Land the `escalier cache clean` subcommand in the same phase.

### Phase 2 — Delete the runtime interop pipeline

Once Phase 1 is stable, remove:

- `interop.ConvertModule` (the runtime conversion call).
- The runtime invocation path of the seven-tier `Classify` (the
  conversion-time invocation remains).
- The trio-fusion logic in `internal/interop/class_shapes.go`.
- `mergeReadonlyVariant` in the prelude.

The converter helpers in `internal/interop/` that operate purely
on AST may be reused by `tools/dts_to_esc/`; anything that builds
`type_system.Type` at runtime is removed.

## Risks

### R1. Cache correctness

A stale cache silently presenting outdated types is the worst
failure mode on this path. Mitigations: the invalidation triggers
in FR3 (which now include override-fragment content hashes, so
edits to overrides cannot reuse a stale merged result), and the
`escalier cache clean` escape hatch.

The cache key trades some precision (a patch release re-emitting
`.d.ts` without bumping the version slips through) for
cross-machine stability and cheapness of the check. Override edits
no longer fall through this gap thanks to the content-hash
component of the key.

### R2. First-build latency for third-party deps

The first compile in a fresh checkout converts every dep. Real
cost, fully amortized on subsequent builds. Whether the
architecture commits to per-package parallelization is unresolved
(see NFR1 and Open Questions).

### R3. Classification quality on auto-converted code

The tier-3/5/6 heuristics run unsupervised over arbitrary
third-party API surface, including code styles the heuristics
were never tuned on. Where the heuristics get it wrong, users'
only recourse is the override path. The override surface must
therefore be expressive enough to correct any single
classification decision the converter makes.

This is materially different from the builtins workstream, where
the converter output is hand-reviewed and committed once. Here
the output is regenerated continuously on every fresh checkout
of every project, with no human in the loop except via overrides.

### R4. Semantic-rewriting correctness (mutability)

FR7 rewrites TypeScript type references to their Escalier
equivalents using a registry that encodes semantic mappings, not
just renames. The most load-bearing rule is the mutability split:
TS `Array<T>` (mutable by convention) becomes Escalier mutable
array; TS `ReadonlyArray<T>` becomes Escalier `Array<T>`. The
same pattern applies to `Map`/`ReadonlyMap`, `Set`/`ReadonlySet`,
and similar pairs.

Getting any of these mappings wrong silently strips or invents
mutability on every method of every third-party API that
references the affected type. This is a soundness failure with no
local symptom: the user sees a plausible-looking Escalier
signature, the checker accepts mutation that wasn't permitted
upstream (or rejects it where upstream allowed it), and the bug
surfaces far from the converter.

Mitigations: the registry is small and centrally owned by the
builtins workstream; the converter must reject any unmapped
identifier (FM6) rather than emit a guess; FR8's origin
annotation gives users a way to spot a wrong rewrite when a
diagnostic appears.

## Open questions

- **Converter API contract.** The third-party workstream calls
  into `tools/dts_to_esc/` as a library. The exact contract —
  package import path, exported function signatures, input type
  (file path? byte slice? parsed `dts_parser` tree?), output type
  (declaration AST slice? a wrapper struct with diagnostics?) — is
  owned by the builtins workstream and must be pinned before this
  workstream starts coding. Tracked here so the dependency is
  visible from this side.
- **Tier scheme glossary.** The "Tier 1a / Tier 1b" terminology
  used in FR2 (and the broader tier numbering referenced by
  `Classify` tiers 3/5/6) is not defined in this document. It
  needs either a local glossary entry or a single canonical
  definition in the parent proposal
  ([../interop_mutability/dts_to_esc_proposal.md](../interop_mutability/dts_to_esc_proposal.md))
  that this doc links to. Pick one before Phase 1 design freeze.
- **Parallelization contract.** NFR1 previously hedged with
  "should not be precluded by the architecture," which is
  unmeasurable. Either commit to a concrete contract (e.g.
  "per-package conversions shall be independent: no shared mutable
  converter state, no required ordering, safe to run under
  `errgroup`") and add an acceptance test, or drop parallelization
  from the requirements entirely. Decision needed before Phase 1
  architecture is finalized.
- **Escape hatch when overrides become unmanageable.** For deps
  where the override fragment count grows large enough to be hard
  to maintain (Node.js's `@types/node` is the likely first
  candidate), the fallback is to hand-author a full `.esc` type
  definition for the package — the same approach builtins takes —
  and bypass the converter + override pipeline entirely for that
  dep. No benchmarking is needed up front; the trigger is
  maintainer pain, not a measured threshold. Open question is
  purely mechanical: how does a project opt a specific dep out of
  conversion and point at a hand-authored `.esc` file instead?
- **Origin annotation storage and encoding.** FR8 requires that
  the original TypeScript spelling for each rewritten type
  reference survives on the AST and through the textual cache.
  Open: (a) does the annotation live on the `TypeRef` AST node as
  a dedicated field, or as a sidecar decorator like
  `@ts("ReadonlyArray<T>")`? (b) granularity — per declaration,
  per type reference, or per line? (c) how do diagnostics render
  it (trailing parenthetical vs. structured "from TypeScript:"
  field)? Decision needed before FR8 lands.
- **Builtin registry collision rules.** The `@js`-derived
  registry must resolve ambiguities when the same identifier is
  declared in both `std:*` and `dom:*` (rare) or in both `dom:*`
  and `webworker` equivalents (common: `fetch`, `Request`,
  `Response`, `URL`, `TextEncoder`). Open: does the third-party
  converter pick by file context (e.g. "this `.d.ts` triple-slash
  references `lib="dom"`"), by a per-package hint, or by a fixed
  precedence order? Owned by the builtins workstream but
  consumed here; pin before Phase 1.
- **Type-vs-value rewriting parity.** Some TS identifiers (e.g.
  `Array`, `Promise`, `Map`, `Error`) are both a type and a
  value. The registry must specify both the type-position and
  value-position rewrite, and the converter must apply the right
  one based on syntactic position in the `.d.ts`. Open whether
  the registry stores these as a single entry with both sides or
  as two parallel entries.
- **Per-dep override authoring story.** Tier 1a expects library
  authors to ship `overrides/*.esc` in their npm package. What
  does the publishing / discovery workflow look like in practice,
  and is there tooling guidance for library authors?
- **Cold-start parallelization threshold.** Separate from the
  parallelization-contract question above: even if the
  architecture supports it, when is parallelization worth
  implementing? What's the threshold (number of deps, total
  `.d.ts` line count) that triggers user-visible pain?
- **Monorepo / pnpm cache layout.** How does the
  `node_modules/.cache/escalier/` layout interact with:
  - Workspaces (npm/yarn/pnpm workspaces) where each workspace
    package has its own `node_modules/` — does each get its own
    cache, or is there a shared root cache?
  - pnpm's content-addressed store (`~/.pnpm-store`) and symlinked
    `node_modules/` — does cache invalidation behave correctly
    when the underlying `.d.ts` is a symlink into the store?
  - Hoisting — when `@types/foo` is hoisted to the workspace root
    and consumed by multiple workspace packages, do they share a
    cache entry or duplicate?
  - Version conflicts — when two workspace packages depend on
    different versions of the same dep, the cache key
    (`<pkg>@<version>`) disambiguates, but the layout question
    (where each entry lives) remains.

  `escalier cache clean --recursive` is a partial answer for the
  cleanup direction; the read-side semantics still need a design
  decision.
