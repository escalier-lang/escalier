# Builtins: Implementation Plan

This plan implements the requirements in
[requirements.md](requirements.md). The structure follows the
[Migration phases](requirements.md#migration-phases) of the
requirements, with one phase per `§` here. Within each phase, work
items list the touch points in the existing codebase and the gate
that proves the phase is done.

## Implementation order and status

Status legend: ✅ done, 🚧 partial, ⬜ not started.

| §   | Phase                                                | FRs         | Status | Depends on | Notes                                                                                                                                                                                                                                                |
| --- | ---------------------------------------------------- | ----------- | ------ | ---------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | Declaration-printer audit                            | FR14        | ⬜      | —          | Riskiest gate; if the existing parser can't round-trip some `lib.*.d.ts` declaration form, the converter is blocked on parser work.                                                                                                                  |
| 2   | URI-scheme imports + binding-shape flags             | FR2–FR5     | ⬜      | §1         | Parser change (bare-string imports, `?flag` suffix), resolver routes to a stdlib data directory on disk, scope-insertion applies `?local`/`?nested`/`?flat`. Placeholder `std:math` stub is the gate.                                              |
| 3   | Cross-package augmentation + inter-package imports   | FR6–FR9     | ⬜      | §2         | Confirm §5 interface-merge can be reused for cross-package augmentation with per-importing-file activation. Symbol augmentation rides on the same mechanism. Pseudo-package import cycles are permitted.                                             |
| 4   | Converter MVP (`tools/dts_to_esc/`)                  | FR10        | ⬜      | §1         | Tiny slice (`Boolean` alone, ~10 lines of `.d.ts`). AST-to-AST translation; emit to stdout; no partition logic.                                                                                                                                      |
| 5   | Converter productionization                          | FR10        | ⬜      | §4         | Partition table; full output paths under `internal/interop/data/{std,dom}/`; `--check` mode; full `lib.*.d.ts` input set; registry/well-known-symbol routing.                                                                                        |
| 6   | Stdlib bootstrap (committed `.esc` files)            | FR1–FR2     | ⬜      | §5         | Run the converter once; review; hand-edit high-value `throws`, lifetimes, mutability; commit.                                                                                                                                                        |
| 7   | Prelude switchover + override deletion               | FR11, FR12  | ⬜      | §2, §3, §6 | Replace `lib.*.d.ts` walking in [prelude.go](../../internal/checker/prelude.go) with the per-file shape loader. Delete the legacy `BuildBuiltinStore` / `loadGlobalDefinitions` / `populateSelfParams` / `UpdateMethodMutability` / `mergeReadonlyVariant` / `mutabilityOverrides` paths in the same PR — pre-1.0, no deprecation cycle. |
| 8   | Internal-fixture migration; intrinsics; LSP support  | FR13, FR15, FR16 | ⬜  | §7         | Migrate Escalier's own fixtures to `import "std:*"`. Implement adaptive diagnostic rendering and the auto-import quick-fix; verify `Awaited<T>` source-level fallback. Confirm intrinsic handlers stay checker-resident.                             |

**Step ordering rationale.** §1 is first because a failed audit
forces parser work that gates everything else. §2 and §4 can run
in parallel after §1 (the converter MVP does not need imports to
resolve at runtime — it only needs the printer to emit parseable
declarations). §3 lands after §2 because augmentation tests need
real `import` statements. §6 produces the source-of-truth `.esc`
files; §7 swaps the prelude and deletes the legacy paths in a
single cut (pre-1.0, no deprecation cycle); §8 migrates internal
fixtures and adds the LSP / diagnostic tooling.

**Internal fixture migration order.** §7 deletes the legacy path
in the same change that swaps the prelude, so the existing
fixtures must be migrated to `import "std:*"` **before §7 lands**,
not after. Re-ordering vs. the requirements doc's migration
phasing: do the §8 fixture-migration substep first
(§8.5) — feasible because the new resolver path (§2) and the
augmentation machinery (§3) are already in place; the legacy
prelude can resolve previously-ambient *and* new-style imports
side-by-side during the fixture-rewriting commit. The rest of
§8 (LSP quick-fix, adaptive rendering, intrinsics) can land
either before or after §7.

---

## §1. Declaration-printer audit (FR14)

**Goal.** Establish, before writing the converter, that every
declaration form the converter needs to emit round-trips through
parser + printer.

**Scope.** Mirror the prior `type_system.Type` audit
([print_type_audit_test.go](../../internal/type_system/print_type_audit_test.go))
but for declaration-level forms:

- `declare class` (including generic parameters and `extends`)
- `declare fn` (including generic constraints, overloads, optional
  / rest parameters, `this` parameter)
- `declare type <alias>` (including conditional types referring to
  `infer T`, mapped types with `as` rename clauses, intersections,
  unions, indexed access)
- `declare var` / `declare val`
- Open `interface` declarations and interface merging
- Ambient module syntax (`declare module "..."` is *out* of scope —
  pseudo-packages are files, not nested ambient modules)

**Explicitly not in the audit:** `declare namespace`. Per FR10
step 2, the converter flattens TS `declare namespace` blocks into
top-level declarations in the output `.esc` file. The printer
does not need to emit nested namespace syntax.

**Work items.**

1. Write `TestPrintDeclAudit_RoundTrip` (parallel to
   `TestPrintTypeAudit_RoundTrip`) covering every declaration form
   listed above. Source-input form, print, re-parse, double-print
   idempotency.
2. For each variant that fails to round-trip, file a follow-up to
   extend the parser or printer; gate §4 on those follow-ups
   landing.
3. Document any decisions ("TS form X is mapped to Escalier form
   Y by the converter") in a short section of this file, since
   they constrain the converter implementation.

**Touch points.**

- [internal/parser/decl.go](../../internal/parser/decl.go)
- [internal/printer/](../../internal/printer/)
- [internal/type_system/print_type.go](../../internal/type_system/print_type.go)

**Gate.** All `TestPrintDeclAudit_*` tests pass; the audit
section in this doc lists any unsupported forms with their
follow-up issues.

---

## §2. URI-scheme imports + binding-shape flags (FR2–FR5)

**Goal.** End-to-end resolution and scope-binding for
`import "std:math"`, including the three binding-shape flags and
the single-class shortcut.

### 2.1 Parser change

- Accept **bare-string imports** with no binding clause:
  `import "std:math"`. Currently
  [internal/parser/decl.go](../../internal/parser/decl.go) (look
  for `parseImport` / equivalent) requires either a namespace
  alias or a `{ ... }` clause.
- Accept the `?flag` and `?flag1&flag2` suffix on the
  module-specifier string literal. Preserve the suffix in the
  AST; do not strip it at parse time (the resolver strips it).
- AST: extend `ImportStmt` with a representation that distinguishes
  bare from named/aliased imports, and that carries the parsed
  `?flag` set. Round-trip via the printer.
- **Rejection:** named imports from a scheme-prefixed URI must
  parse (so we can emit a clear semantic error in §2.2) but the
  resolver rejects them. See "Error taxonomy" below.

### 2.2 Resolver change

Touch point:
[internal/checker/infer_import.go](../../internal/checker/infer_import.go)
(`resolveImport`, `resolveExportModulePath`).

- Detect `std:`, `dom:`, `node:` schemes before the
  `node_modules/<pkg>` walk. Route them to the **stdlib data
  directory** on disk (resolution scheme below).
- Mapping: `std:math` → `<stdlib>/std/math.esc`;
  `dom:http` → `<stdlib>/dom/http.esc`. Multi-word packages use
  underscores in both URI and filename
  (`std:typed_arrays` → `std/typed_arrays.esc`,
  `dom:web_rtc` → `dom/web_rtc.esc`). Hyphens never appear in
  pseudo-package URIs or filenames; there is no `-` → `_`
  substitution at this layer (that rule belongs to the
  third-party workstream).
- Strip the `?flag` portion before path lookup; pass the flag set
  to the binding step.
- `node:` resolves but always errors with "node:* is reserved;
  not yet populated" until Node support lands.

### 2.2a Stdlib data directory resolution

The `.esc` files under `internal/interop/data/` are **loaded
from disk at compile time, not embedded into the binary**.
This keeps them editable by compiler users — adding a new
builtin or tweaking a return type does not require rebuilding
the compiler.

Discovery order, first hit wins:

1. **`ESCALIER_STDLIB_DIR` environment variable.** Absolute
   path to a directory containing `std/` and `dom/`
   subdirectories. Overrides everything else; intended for
   contributors testing alternative stdlibs and for tooling
   that ships its own.
2. **`--stdlib-dir <path>` CLI flag** on `escalier check` /
   `escalier build` / `lsp-server`. Same shape; flag wins over
   env var if both are present (standard CLI convention).
3. **Sibling to the executable.** `<exe-dir>/../share/escalier/data/`
   (Unix convention; resolves on the typical
   `bin/`+`share/` install layout). Falls back to
   `<exe-dir>/data/` for single-directory installs.
4. **Repo-relative.** When the binary is run from a build tree
   (detected by walking up for a `go.mod` whose module path
   matches the Escalier module), use `internal/interop/data/`
   relative to the repo root. Makes `go run ./cmd/escalier`
   work without setup.

If none resolve, emit a fatal startup error pointing at the
discovery order and the `ESCALIER_STDLIB_DIR` env var. The
error is **not** a per-file diagnostic — it fires before any
user file is parsed.

**User customization.** Users who want a tweaked stdlib copy
the install's `share/escalier/data/` tree to a writable
location, edit the `.esc` files, and point
`ESCALIER_STDLIB_DIR` at the copy. No recompile of the
compiler. Adding a new builtin is just a new `.esc` file in
the appropriate subdirectory; the resolver picks it up on
next compile.

**`node/` directory.** Created empty in the source tree with a
`README.md` explaining the reserved scheme. No empty-pattern
constraint to satisfy — the loader simply reports
"node:* is reserved" when asked.

Touch point: a new `internal/interop/stdlib_dir.go` (or
similar) hosts the discovery logic; the resolver in
[infer_import.go](../../internal/checker/infer_import.go) calls
it lazily on first scheme-prefixed import.

### 2.2b Codegen runtime-erasure (FR3)

Pseudo-package imports are **type-system-only, runtime-erased**.
The codegen must drop `import` statements whose specifier carries
a `std:`, `dom:`, or `node:` scheme before emitting JS. Zero
runtime artifact.

Touch point:
[internal/codegen/](../../internal/codegen/) — extend the import
lowering step to recognize the scheme prefix and emit nothing for
those imports. Fixture: an Escalier file with
`import "std:math"; math.sin(x)` lowers to JS that references
`Math.sin(x)` directly with no `import` line.

**Gate.** Codegen fixture under [fixtures/](../../fixtures/)
covers a representative program (`std:` and `dom:` both present)
and the generated JS has no scheme-prefixed import statements.

### 2.3 Binding-shape application

- Implement the three flag rules from FR4. Each pseudo-package
  import contributes a binding entry whose shape depends on the
  flag:
  - `?local` (default): binding name = lowercased last URI
    segment (or capitalized class name when the single-class
    shortcut FR5 fires).
  - `?nested`: contributes to a per-scheme namespace
    (`std`, `dom`); the package sits under it as
    `std.<package>`. Multiple `?nested` imports from the same
    scheme merge under disjoint sub-namespaces — no collision
    risk.
  - `?flat`: merges directly into the per-scheme namespace.
    Multiple `?flat` imports from the same scheme merge; package
    names are dropped. Collision risk is real.
- **`?flat` name collision is a warning, not an error.** Per
  FR4: deterministic last-import-wins (or equivalent) binds the
  colliding name; the diagnostic surfaces the conflict so the
  user can resolve it by switching one of the imports off
  `?flat`. See "Error taxonomy" cross-cutting section.
- Internal bookkeeping (whether a file has loaded a package's
  augmentations / declarations) keys on the package's full URI
  (`dom:canvas`), independent of binding-shape flag. This
  applies uniformly across `?local`, `?nested`, and `?flat`.
- **Mutually exclusive:** combining any two of `?local`,
  `?nested`, `?flat` on one URI is a compile error; the
  resolver reports which flag pair is invalid.
- **Extensible flag slot.** The grammar reserves the `?flag` /
  `?flag1&flag2` shape for future flags (`?type-only`, `?lazy`,
  …). Unknown flags currently error per the taxonomy; the
  resolver factors flag recognition into a per-flag table so
  future flags slot in without restructuring.

### 2.4 Single-class shortcut (FR5)

- Detect activation: the package declares a top-level class whose
  name matches the lowercased last URI segment case-insensitively.
- When active **and** the import is `?local`, bind the class name
  with its original capitalization (`Array`, `Date`, `Promise`).
  Other package exports remain accessible as namespace members on
  the same binding.
- `Array.isArray(xs)`, `Array<number>` (type position),
  `Array(5)` (construct, no `new`).
- Static methods on the class take precedence over namespace
  members on name collision.
- Not applicable to `?nested` or `?flat`.

### 2.5 Tests and gates

- **Parser tests.** Bare-string imports with and without `?flag`,
  including `?flag1&flag2`. Round-trip via the printer.
- **Resolver tests.** Each scheme; unknown scheme; known scheme +
  unknown package; `?flag` stripping. Place the
  `internal/interop/data/std/math.esc` stub with
  `let PI: number = 3.14` so end-to-end resolution has something
  to find. Stdlib-discovery tests cover all four discovery
  paths from §2.2a (env var, `--stdlib-dir`, sibling-to-exe,
  repo-relative) plus the "no stdlib found" fatal error.
- **Binding-shape fixtures** under [fixtures/](../../fixtures/):
  one per shape, plus two `?flat` packages merging into a single
  `dom` binding, plus the mutually-exclusive error case.
- **Single-class shortcut fixture:** `std:array` stub declaring
  `Array<T>`; assert `Array.isArray`, `Array<number>` type
  position, and `Array(5)` construct all bind correctly.

**Gate.** Stub `std:math` resolves end-to-end with all three
binding-shape flags; two-package `?flat` fixture merges; flag
collision errors. Single-class shortcut works for the `std:array`
stub.

---

## §3. Cross-package augmentation + inter-package imports (FR6–FR9)

**Goal.** A file that imports `dom:canvas` sees
`createElement("canvas") → HTMLCanvasElement`; a sibling file
without the import does not. Symbol augmentation rides on the
same machinery. Inter-package imports between pseudo-packages
work, including cycles.

### 3.1 Confirm the augmentation mechanism

The §5 (interop_mutability) override-merge code already does
interface merging at a fixed merge point. Cross-package
augmentation needs the same primitive applied **per importing
file**: the same `interface HTMLElementTagNameMap` is merged
with different augmentation sets depending on which packages the
checking file has imported.

Decision required up-front (before writing more code): does the
existing merge code support per-file scoping, or does
augmentation need its own loader path? Output of this
investigation lives in a short note appended to this section.

Touch points:
- [internal/interop/](../../internal/interop/) — existing merge.
- [internal/checker/infer_import.go](../../internal/checker/infer_import.go) —
  per-file scope assembly.

### 3.2 Open-registry augmentation (FR7)

- `dom:dom` declares the registries
  (`HTMLElementTagNameMap`, `HTMLElementEventMap`,
  `SVGElementTagNameMap`, `MathMLElementTagNameMap`, …) with
  empty bodies and writes the registry-keyed APIs
  (`createElement`, `querySelector`, `getElementsByTagName`,
  `createElementNS`) against them. The element families
  themselves (`HTMLCanvasElement`, `SVGCircleElement`,
  `MathMLElement`, …) live in their feature packages — the
  registry interface is a contract slot; `dom:dom` never
  references the element classes directly.
- Feature packages augment **only** the registry, not the API:
  `dom:canvas` adds `{ canvas: HTMLCanvasElement }` to
  `HTMLElementTagNameMap`; `dom:svg` adds
  `{ circle: SVGCircleElement, path: SVGPathElement, … }` to
  `SVGElementTagNameMap`.
- Activation is per-importing-file (FR9): the augmentation is
  visible iff the importing file imports the augmenting package
  (under any binding-shape flag).
- Augmentations propagate via **explicit re-export** only:
  `export * from "dom:canvas"` in file A makes A's importers see
  canvas's augmentations; a bare `import "dom:canvas"` in A does
  not leak.
- Sibling file without the import sees `never` (or the
  pre-augmentation shape) from the indexed access, surfacing the
  missing import.

**Language requirements verified at this phase.** Output of
§3.1's investigation must confirm both:

1. **Open interfaces / cross-package interface merging** — the
   §5 (interop_mutability) merge code can be applied per
   importing file, or a new loader path is built.
2. **Indexed access over open registries** refreshes correctly
   when the registry is augmented:
   `HTMLElementTagNameMap[K]` where
   `K extends keyof HTMLElementTagNameMap` must pick up the
   augmented entries in the importing file's view, not a
   snapshot taken at registry-declaration time.

If either fails, the gap becomes a follow-up issue blocking §3
completion.

### 3.2a No `override declare` for builtins (FR7)

The `override declare` syntax — designed for the third-party
workstream's override mechanism — is **not** used for builtin
augmentation. Pseudo-package augmentation uses plain
`declare interface Foo { … }` blocks added to the augmenting
package's `.esc` file. The converter (§5) must emit
augmentation blocks in plain `declare interface` form, not
`override declare`. Reviewers of §6 verify this on the
committed files.

### 3.2b Per-API augmentation fallback (FR7)

For the rare cross-package API that does not fit a registry
pattern, the requirements specify two fallback patterns. Track
each occurrence in the partition table (§5.1) so it is
deliberately classified, not silently mis-routed:

1. **Parameter-typed APIs** — the feature package exports the
   type; `dom:dom` references it via the type's qualified name;
   no augmentation needed.
2. **String-overload APIs without a registry** — the feature
   package augments the API's overload set directly. Rare;
   treated case-by-case when it arises.

The converter records candidate per-API augmentations in a
log so reviewers can confirm the classification during §6
bootstrap review.

### 3.3 Symbol augmentation (FR8)

- `std:symbol` declares `Symbol` with no well-known symbols on
  its static side.
- Sibling packages augment `Symbol`'s static side: `std:iterator`
  adds `iterator: unique symbol`; `std:async` adds
  `asyncIterator`.
- Each domain package also re-exports its symbol(s) under a
  short package-local name:
  - **Single symbol per package** → `<package>.key`
    (e.g. `iterator.key`, `async.key`).
  - **Multiple symbols per package** → `<package>.<name>Key`
    using the ECMAScript spelling for `<name>`
    (e.g. `regexp.matchKey`, `regexp.replaceKey`).
- The re-exported name and the `Symbol.<name>` form are aliases
  for the same runtime value; either reads as iterable in a
  `for-of`.
- **Shape-loaded language use** of well-known symbols (the
  `for-of` desugaring, etc.) needs no import; explicit
  references via `Symbol.iterator` need both `std:symbol` and
  the owning domain package.

### 3.4 Inter-package imports + cycles (FR6)

- Pseudo-packages `import` other pseudo-packages explicitly
  (e.g. `dom/canvas.esc` does `import "dom:dom"` to extend
  `HTMLElement`).
- **Cycles between pseudo-packages are permitted.** The
  resolver / `internal/dep_graph/` must special-case `std:`,
  `dom:`, `node:` schemes to skip cycle reporting when both
  endpoints of an edge live under these schemes.
- Cycles among user packages, and user-package-to-pseudo-package
  cycles, remain disallowed.

### 3.5 Tests and gate

- Two-file fixture: `dom/dom.esc` declares an empty
  `HTMLElementTagNameMap`; `dom/canvas.esc` augments it with
  `canvas: HTMLCanvasElement`. Importing file gets the narrow
  return; sibling file gets `never` / base shape.
- Run the fixture under each of `?local`, `?nested`, `?flat` —
  binding shape must not affect augmentation visibility.
- Symbol augmentation fixture: a class implementing
  `[iterator.key]()` is iterable; `[Symbol.iterator]()` form
  works iff both `std:symbol` and `std:iterator` are imported.
- Cycle fixture: two `std:*` packages with a mutual import; the
  resolver accepts the cycle; the same shape between two user
  packages errors.

**Gate.** All four fixtures pass; the dependency-investigation
note in §3.1 is committed.

---

## §4. Converter MVP (FR10)

**Goal.** A minimal `tools/dts_to_esc/` Go binary that translates
a tiny TS-lib slice (e.g. the `Boolean` declarations, ~10 lines)
to readable, parseable `.esc`.

**Location.** New directory `tools/dts_to_esc/` alongside
existing `tools/gen_ast/` and `tools/gen_types/`.

### 4.0 Precursor: `dts_parser` JSDoc retention

Verify that [internal/dts_parser/](../../internal/dts_parser/)
attaches leading JSDoc comments to declaration AST nodes. If it
does not, add this as a precursor before the converter can do
the FR10 step 6 pass-through. Touch points:
[internal/dts_parser/comment_test.go](../../internal/dts_parser/comment_test.go)
already exists for comments; check whether leading JSDoc on a
declaration is in the AST shape or needs attaching.

**Work items.**

1. Read a single `.d.ts` file via the existing
   [internal/dts_parser/](../../internal/dts_parser/).
2. AST-to-AST translation: map TS declaration AST nodes to
   Escalier declaration AST nodes directly, bypassing
   `type_system.Type` and the checker. **No type resolution
   involved** — no prelude bootstrap cycle.
3. Recognize the **class-via-trio idiom** at AST level:
   `interface Foo` + `interface FooConstructor` +
   `declare var Foo: FooConstructor` collapses to one
   `declare class Foo` (instance members from `Foo`, statics +
   constructor from `FooConstructor`, `declare var` dropped).
   The recognition rules mirror
   [internal/interop/class_shapes.go](../../internal/interop/class_shapes.go)
   `tryFuseTrio` — same predicates, different substrate:
   - The `FooConstructor.new(...)` signature must return the
     instance type `Foo`.
   - Both `Foo` and `FooConstructor` interface bodies must be
     object-shaped (no other variants).
   - The `declare var Foo: FooConstructor` binding must match
     the `FooConstructor` interface name exactly.
   The Escalier-style sibling shape recognized by
   `tryFuseEscalierClass` is not expected in `lib.*.d.ts` and
   does not need handling. Trios that do not satisfy the
   predicates pass through unchanged.
3a. **Flatten `declare namespace` blocks.** Per FR10 step 2,
    TS `declare namespace Foo { … }` becomes top-level
    declarations in the output file (each pseudo-package file
    is itself a namespace). The converter does not emit nested
    namespace syntax; the FR14 audit (§1) excludes
    `declare namespace` from the supported declaration forms.
4. **Receiver mutability seeding.** Run `interop.Classify`
   (tiers 3/5/6 from the interop_mutability workstream) at
   conversion time to seed `self` vs `mut self` on each method.
5. **JSDoc pass-through.** Leading JSDoc on a TS declaration
   carries through to the emitted Escalier declaration as a
   doc comment. Strip TS-specific tags (`@override` dropped;
   `@param` syntax adjusted where Escalier differs); pass the
   rest verbatim. Precursor: §4.0 above. The JSDoc tag
   stripping/rewriting table is a small in-tree config inside
   `tools/dts_to_esc/`, easy to extend as cases surface.
6. **Intrinsic stripping.** `intrinsic`-typed declarations are
   skipped (FR13). The parser does not learn the `intrinsic`
   keyword.
7. Emit via the (now-audited) declaration printer and
   [internal/type_system/print_type.go](../../internal/type_system/print_type.go).
   Emit to stdout. No file layout, no partition table yet.

**Gate.** Output for the `Boolean` slice:

- Parses through `parser.ParseFile`.
- Reads naturally to a human (snapshot-tested via `go-snaps`).
- Two consecutive conversions produce byte-identical output.

---

## §5. Converter productionization (FR10)

**Goal.** Convert the full pinned `lib.*.d.ts` set into the
committed package partition.

### 5.1 Partition table

A hand-maintained Go map in the converter source: TS-lib
declaration name → target pseudo-package. Drives both file
output and the LSP name-index (§8.3). Driven by the
[FR1 partition list](requirements.md#fr1-no-ambient-set-shape-loaded-vs-named-bindings).

**`std/` (full enumeration).**

| Package           | Type                | Members / notes                                                                                                                  |
| ----------------- | ------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| `std:array`       | per-class           | `Array<T>`                                                                                                                       |
| `std:string`      | per-class           | `String`                                                                                                                         |
| `std:number`      | per-class           | `Number`; also `parseInt`, `parseFloat`, `isNaN`, `isFinite` (numeric-parsing domain)                                            |
| `std:boolean`     | per-class           | `Boolean`                                                                                                                        |
| `std:bigint`      | per-class           | `BigInt`                                                                                                                         |
| `std:regexp`      | per-class           | `RegExp`; owns regex-related well-known symbols                                                                                  |
| `std:promise`     | per-class           | `Promise<T>`; source-level `Awaited<T>`                                                                                          |
| `std:symbol`      | per-class           | `Symbol`; **no** well-known symbols (sibling packages augment)                                                                   |
| `std:object`      | per-class + utility | `Object`; `Partial`, `Required`, `Readonly`, `Pick`, `Omit`, `Record`, `Exclude`, `Extract`, `NonNullable`                       |
| `std:function`    | per-class + utility | `Function`; `Parameters`, `ConstructorParameters`, `ReturnType`, `InstanceType`, `ThisParameterType`, `OmitThisParameter`, `ThisType` |
| `std:date`        | per-class           | `Date`                                                                                                                           |
| `std:map`         | per-class           | `Map`                                                                                                                            |
| `std:set`         | per-class           | `Set`                                                                                                                            |
| `std:weak_ref`    | per-class           | `WeakRef`                                                                                                                        |
| `std:iterator`    | bundled             | `Iterator<T>`, `Iterable<T>`, `IterableIterator<T>`, `IteratorResult<T>`, `Generator<T,R,N>`; augments `Symbol.iterator`         |
| `std:async`       | bundled             | `AsyncIterator<T>`, `AsyncIterable<T>`, `AsyncGenerator<T,R,N>`, `AggregateError`; augments `Symbol.asyncIterator`; depends on `std:iterator` |
| `std:error`       | bundled             | `Error`, `TypeError`, `RangeError`, `SyntaxError`, `ReferenceError`. `URIError` → `std:url`; `AggregateError` → `std:async`. `EvalError` dropped |
| `std:math`        | namespace           | unchanged from existing layout                                                                                                   |
| `std:json`        | namespace           | unchanged                                                                                                                        |
| `std:console`     | namespace           | unchanged                                                                                                                        |
| `std:typed_arrays`| bundled             | unchanged                                                                                                                        |
| `std:reflect`     | namespace           | unchanged                                                                                                                        |
| `std:proxy`       | per-class           | unchanged                                                                                                                        |
| `std:intl`        | bundled             | unchanged; needs `import "std:date"`                                                                                             |
| `std:temporal`    | bundled             | unchanged                                                                                                                        |
| `std:wasm`        | bundled             | unchanged                                                                                                                        |

**`dom/` partition.** DOM splits more finely than `std/` because
the surface is larger; a typical browser program touches 2–3
`dom:*` packages. Initial set:

`dom:dom` (registries + core APIs), `dom:html`, `dom:svg`,
`dom:mathml`, `dom:http`, `dom:canvas`, `dom:webgl`,
`dom:webrtc`, `dom:storage`, `dom:workers`, `dom:media`,
`dom:forms`. Additional `dom:*` packages may be added in §6
review as the partition is exercised against the full lib
input.

**Single-class shortcut eligibility (FR5).** Per FR5, the
single-class shortcut applies to:

`std:array → Array`, `std:string → String`,
`std:number → Number`, `std:boolean → Boolean`,
`std:bigint → BigInt`, `std:regexp → RegExp`,
`std:promise → Promise`, `std:symbol → Symbol`,
`std:object → Object`, `std:function → Function`,
`std:date → Date`, `std:map → Map`, `std:set → Set`,
`std:weak_ref → WeakRef`. The converter does not mark this
explicitly — the shortcut activates structurally when the
package's lowercased URI segment matches a top-level class
name case-insensitively.

**Drops.** `globalThis` and `eval` drop entirely — `eval` has
no good use case; `globalThis` was the union of every
previously-ambient name and has nothing to take its union over
in the new model. The converter recognizes both and skips
emission with a logged note. `intrinsic`-typed declarations
(FR13) skip the same way.

**`node:*`.** Partition deferred until Node support lands. The
`internal/interop/data/node/` directory is created empty.

### 5.2 Registry + well-known-symbol routing

- Detect TS-lib entries on registry interfaces
  (`HTMLElementTagNameMap`, `HTMLElementEventMap`,
  `SVGElementTagNameMap`, `MathMLElementTagNameMap`, …) and
  route each entry into the corresponding feature package's
  augmentation block.
- Detect well-known symbol declarations on
  `SymbolConstructor` and route each into its owning package's
  `Symbol` augmentation block: `Symbol.iterator` →
  `std/iterator.esc`, `Symbol.asyncIterator` → `std/async.esc`,
  regex symbols → `std/regexp.esc`, etc. The routing table is
  another hand-maintained map.

### 5.3 Output layout

Per [FR2](requirements.md#fr2-pseudo-package-layout):

```
internal/interop/data/std/  — std/*.esc
internal/interop/data/dom/  — dom/*.esc
internal/interop/data/node/ — reserved, empty
```

**Distribution.** Files are shipped alongside the compiler
binary (typically under `share/escalier/data/` on Unix-style
installs) and discovered at runtime per §2.2a — **not** embedded
via `//go:embed`. Editable post-install so users can tweak
builtins or add new ones without rebuilding the compiler.
Release packaging (`make`, install scripts, distro packages)
copies the tree alongside the binary; CI verifies the
post-install layout discovers correctly.

### 5.4 `--check` mode

Re-run the converter, diff against the committed files, fail CI
on mismatch. Print a unified diff. This is the TS-version-bump
review tool — **never auto-applied**.

### 5.5 `throws` annotations (bootstrap policy)

Per [FR10](requirements.md#fr10-bootstrap-converter-tools-dts_to_esc):
hand-curate the high-value ~50 entries (`JSON.parse`,
`decodeURI*`, `BigInt`, `fetch`, `Response.json`, etc.). Scraping
MDN is rejected (prose-not-data, brittle, copyleft).
WebIDL `[Throws]` extraction (`@webref/idl`) is a plausible
future automation lever for `dom:*` but is **out of scope for
the bootstrap**. ECMARKUP extraction for `std:*` is similarly
deferred.

### 5.6 TS-version-bump workflow

Document the bump workflow in `tools/dts_to_esc/README.md`:

1. Bump the pinned TS dependency.
2. Run `tools/dts_to_esc/regenerate --check`.
3. The `--check` output is a unified diff against current
   committed files showing TS-side adds / removes / changes.
4. Contributor decides which changes to port by hand and
   commits the result.

**Optional CI nudge.** An action that annotates a PR with "TS
lib changed since last bump" when the diff exceeds some
threshold. Out of scope for the initial workstream, but the
hook point is identified in this doc so it can be added later
without re-architecture.

**Gate.** Every output `.esc` file under
`internal/interop/data/{std,dom}/` parses; the converter is
idempotent (byte-identical on re-run); the partition matches
[FR1](requirements.md#fr1-no-ambient-set-shape-loaded-vs-named-bindings)
member-for-member.

---

## §6. Stdlib bootstrap

**Goal.** Commit the initial generated-then-hand-edited `.esc`
files as the source of truth.

**Work items.**

1. Run the converter (§5) once, producing the full tree under
   `internal/interop/data/{std,dom}/`.
2. Human review of every file. Hand-edit:
   - Obvious mis-classifications.
   - High-value `throws` annotations (the ~50 from §5.5).
   - Lifetimes where applicable (the existing
     [planning/lifetimes/](../lifetimes/) work feeds in here).
   - Mutability refinements not captured by the
     `interop.Classify` seeding.
   - `Symbol.customMatcher` (Escalier-specific, not in
     `lib.*.d.ts`) hand-authored in `std:symbol`.
3. **`Awaited<T>` source-level definition.** Write `Awaited<T>`
   in `std:promise` as the recursive conditional type (the same
   shape as TypeScript's definition). Exercise against a
   representative fixture (nested promises, thenables, mixed
   `T | Promise<T>`, generic propagation). If a concrete
   blocker surfaces, fall back to a checker-resident intrinsic
   and document the specific failure.
4. Commit. After this point, the converter persists only as
   `--check` review tool; ongoing edits are direct to the
   committed `.esc` files.

**Gate.** Humans review the committed files; `go test ./...`
passes (the existing checker still resolves these declarations
through the legacy `lib.*.d.ts`-walking prelude — §7 swaps it
out).

---

## §7. Prelude switchover + override deletion (FR11)

**Goal.** Replace the `lib.*.d.ts`-walking prelude with the lazy
per-file shape loader, and delete the legacy override / builtin
machinery in the same change. Pre-1.0; no deprecation cycle, no
build flag, no parallel paths.

### 7.1 Lazy shape-loader

Touch point:
[internal/checker/prelude.go](../../internal/checker/prelude.go).

For each file F being checked, the checker inspects F's syntax
and shape-loads only the needed `std:*` packages (no name
bindings added to scope). Trigger map per FR11:

| Trigger                                                            | Shape-loaded package(s)              |
| ------------------------------------------------------------------ | ------------------------------------ |
| String/number/boolean/bigint literal or operator on a primitive    | `std:string`/`number`/`boolean`/`bigint` |
| Array literal                                                      | `std:array`                          |
| Regex literal                                                      | `std:regexp`                         |
| `async fn` / `await`                                               | `std:promise` (+ `std:async` if async iteration) |
| `for x of xs` / generator                                          | `std:iterator`                       |
| `for await x of xs`                                                | `std:async`                          |
| `try` / `catch` / `throw` / `throws` clause naming an error class  | `std:error`                          |

Multiple files share one parsed copy of each package.
Shape-loading is idempotent and additive.

### 7.2 Explicit import loading

Reuses §2's resolver path. The shape-load and named-import paths
share the same parsed declarations; they differ only in whether
identifiers land in F's scope. Multiple files in a compilation
share **one parsed copy** of each `std:*` package; the
shape-loader memoizes by package URI. No bootstrap cycle: each
`std:*` package contains only declarations (no value-level
expressions needing their own prelude).

### 7.2a Cross-package shape-load verification

The risk callout from requirements §"Risks" — "cross-package
references between `std:*` files" — needs explicit
verification here. `Promise<T>` in `std:promise` references
`Iterable<T>` from `std:iterator`; `Array<T>` in `std:array`
references the iteration protocol; etc. The existing module
loader handles cross-package references for user code; this
phase confirms it works for shape-loaded `std:*` packages
under the per-file shape-loader.

Test: a fixture that uses only an `async fn` + a `for of`
loop should trigger shape-loading of both `std:promise` and
`std:iterator`, and `Promise<T>` should resolve `Iterable<T>`
references successfully without explicit imports being present
in user code. If this fails, the loader needs adjustment.

### 7.3 Delete the legacy paths (same PR)

Delete in the same change that swaps the prelude — no flag, no
parallel paths, no waiting. The compiler is pre-1.0; the
breaking change lands cleanly. Internal fixtures must already
have been migrated to `import "std:*"` (§8.5) before this PR
opens, otherwise the test suite breaks.

Touch points to delete or empty out:

- `loadGlobalDefinitions` ([prelude.go](../../internal/checker/prelude.go))
- `populateSelfParams`
- `UpdateMethodMutability`
- `mergeReadonlyVariant`
- the `mutabilityOverrides` Go map
- `BuildBuiltinStore` (empty the function body; delete the call
  site; delete the function in a follow-up if no callers
  remain)
- `internal/interop/data/builtins/` (if present) — no override
  fragments for builtins remain
- The Escalier-specific
  `SymbolConstructor.customMatcher` injection at
  [prelude.go:804–836](../../internal/checker/prelude.go#L804-L836)
  (now sourced from `std:symbol` per §6 step 2)

**No `override declare` for builtins.** That syntax stays
reserved for the third-party workstream's override mechanism;
no builtin pseudo-package uses it.

**Gate.** `go test ./...` passes on the new path alone; the
legacy code is gone, not behind a flag.

---

## §8. Internal-fixture migration; intrinsics; LSP support (FR13, FR15, FR16)

**Goal.** Migrate Escalier's own fixtures and tests; ship the
diagnostic + LSP tooling users will need.

### 8.1 Intrinsic types (FR13)

Confirm that `Uppercase`, `Lowercase`, `Capitalize`,
`Uncapitalize`, `NoInfer` remain checker-resident handlers — no
source file under `internal/interop/data/`. The four string-case
utilities are pure `Type → Type` resolvers; `NoInfer` is an
inference-machinery hook. Tracked in escalier-lang/escalier#631.

`Awaited<T>` source-level definition lives in `std:promise` per
§6 step 3 (the recursive conditional type matching TS's
definition; tracked in escalier-lang/escalier#630). Fallback to
a checker-resident intrinsic only on documented blocker —
recursive conditionals don't reduce correctly, pathological
performance, or a soundness issue. **The fallback decision
must be committed with a documented description of the specific
failure that motivated it.** The doc lives alongside the
fallback implementation, not in this plan.

The bootstrap converter strips `intrinsic`-typed declarations
encountered in TS source (FR13). The parser does **not** learn
the `intrinsic` keyword. Verify the parser still rejects
`intrinsic` after this workstream lands (regression guard).

### 8.2 Adaptive diagnostic rendering (FR15)

Replace the global `renderType(t)` with
`renderTypeForLocation(t, scope)`. The renderer picks the
shortest unambiguous form for `t` given the bindings in scope at
the diagnostic's source location:

1. **Single-class shortcut.** If the file has a `?local` import
   whose package qualifies for the single-class shortcut (FR5),
   render as the capitalized class binding (`Array<number>`,
   `Date.now()`) — matching what the user would write.
2. **Namespace member.** `?local` without shortcut → `math.Foo`;
   `?nested` → `std.math.Foo`; `?flat` → `std.Foo`.
3. **Not imported.** Fully-qualified canonical name
   (`std:array.Array`) plus a "did you mean to
   `import "std:array"`?" hint pointing at the FR16 quick-fix.

**Tie-breaking.** When multiple forms are simultaneously in
scope (e.g. the file has both `import "std:array"` and
`import "std:array?nested"`), the renderer picks the shortest;
ties break in the order 1 → 2 → 3 above. The rendering is
per-diagnostic, not per-compilation — same type can render
differently in two files.

(Named imports from pseudo-packages are out of scope per
Non-goals, so the renderer has no "bare name" case to handle.)

Touch points: every diagnostic site that currently calls
`renderType(t)` needs threading of the file scope through the
diagnostic pipeline.

### 8.2a Diagnostic-assisted migration

When a name that used to be ambient is referenced without an
import under the new default, the **unbound-name diagnostic**
includes a suggestion ("did you mean to `import "std:async"`?")
whenever the unbound name matches a known pseudo-package export.
The suggestion list is derived mechanically from the LSP
name-index (§8.3); the diagnostic path reuses the same index.
This is the **fallback for command-line use** — users in a
supported editor get the FR16 quick-fix instead. Suggestion
text routes through the error-message taxonomy entries; spans
point at the bare reference, not the surrounding statement.

### 8.3 Auto-import quick-fix (FR16)

LSP first-class. Quick-fix on an unbound-name diagnostic that:

1. Adds the appropriate namespace import statement
   (`import "std:promise"`, `import "std:math"`, …).
2. **Single-class shortcut packages:** leaves the bare reference
   unchanged (`Promise.all`, `Array.isArray`, `Date.now`,
   `Error(...)` already match the imported binding name).
3. **Other packages:** rewrites the bare reference to qualify
   through the resulting namespace
   (`sin(x)` → `math.sin(x)`).

Named imports are out of scope. Quick-fix only adds namespace
imports.

Implementation:

- **Name → owning pseudo-package index.** Build at LSP startup
  by walking the resolved stdlib data directory (§2.2a) and
  reading top-level declaration names from each `.esc` file.
  Cache; **refresh on file change** via filesystem watch on
  the data directory — users editing their stdlib copy see the
  index update without restarting the LSP. Same index serves
  §8.2a diagnostic suggestions and §8.4 `--explain-type` hints.
- **Per-file binding-shape preference.** Default `?local`;
  user-configurable. The quick-fix follows the file's existing
  convention if any of its imports already pick a flag — e.g.
  if every other import in the file uses `?nested`, the
  quick-fix emits `?nested` too.
- **Name-collision suggestion ordering.** When the same name is
  exported by more than one pseudo-package (rare but possible
  for `Error` subclasses, etc.), the quick-fix offers each
  candidate as a separate fix; ranking by canonical
  alphabetical order, with `std:*` ranked before `dom:*`.

Touch points: [cmd/lsp-server/](../../cmd/lsp-server/),
[internal/lsp/](../../internal/lsp/) (or wherever the LSP code
actually lives).

### 8.4 `--explain-type` diagnostic refinement

When a tag-keyed return is wider than expected
(`createElement` returning the union element type instead of
`HTMLCanvasElement`), the diagnostic suggests likely `dom:*`
imports to widen the file's view. Complements the FR16
quick-fix for the type-narrowing case.

### 8.5 Internal fixture migration

Update every fixture and test under
[fixtures/](../../fixtures/) and
[internal/checker/tests/](../../internal/checker/tests/) that
relied on previously-ambient symbols (`Math`, `JSON`, `console`,
`Promise`, `Error`, `Array.from`, `parseInt`, …) to use
`import "std:*"` statements.

The auto-import quick-fix is the migration aid — run it across
the fixture tree to exercise the same tooling external users
will rely on.

### 8.6 Source-map / diagnostic provenance

Per requirements §"Source-map and diagnostic provenance for
embedded pseudo-packages" (the section name retains the
original "embedded" terminology; the implementation is
filesystem-resident per §2.2a):

- **Real filesystem path.** Spans on declarations parsed from
  stdlib `.esc` files carry the **actual resolved path**
  (e.g. `/usr/local/share/escalier/data/std/string.esc`), since
  the file is on disk and the user can open it directly. No
  virtual URI scheme is needed. When the resolved path lies
  under a well-known install prefix, diagnostics may render it
  as `<stdlib>/std/string.esc` for compactness, but the
  underlying span still carries the real path so editor
  click-through works.
- **Preserved line/column.** Line/column information from the
  parser is preserved as for any other file. The `Span` shape
  already carries this; no change.
- **LSP go-to-definition.** Clickthrough opens the resolved
  file directly — no materialization, no custom URI scheme.
  If the file is read-only (system install) the editor opens
  it in read-only mode; users who want to edit point
  `ESCALIER_STDLIB_DIR` at a writable copy.

Touch points: span construction in
[internal/parser/parser.go](../../internal/parser/parser.go)
already takes a filename; the resolver passes the resolved
path through unchanged.

**Gate.** All fixtures migrated and passing under the new path;
LSP quick-fix integration test green; renderer fixture per
case (`?local` shortcut, `?local` non-shortcut, `?nested`,
`?flat`, no-import) passes.

---

## Cross-cutting

### Error taxonomy (per requirements §"Error-message taxonomy")

Each diagnostic ties back to the offending `import` statement,
ideally to the URI string literal (and within it, to the flag
portion when the failure is flag-shaped):

- **Unknown scheme** — names the scheme and lists the
  recognized set.
- **Unknown package within a known scheme** — names scheme +
  package; suggests near-spelling matches if cheap.
- **Invalid flag combination** — names the specific pair;
  explains mutual exclusion.
- **Unknown flag** — names the flag; lists recognized set.
- **Named import from a pseudo-package URI** — explains
  namespace-only; suggests the rewrite.
- **`?flat` name collision** — **warning, not error.** Names the
  collision; names the two source packages; names which won
  under last-import-wins.

Fixtures under [fixtures/](../../fixtures/) exercise each with
full message-text assertions per CLAUDE.md test conventions.

### Testing strategy summary

Per requirements §"Testing strategy":

- Parser, resolver, binding-shape (§2).
- Registry augmentation, Symbol augmentation, activation
  scoping (§3).
- Prelude switchover (§7) — internal fixtures, migrated to
  `import "std:*"` ahead of the switchover commit (§8.5), keep
  type-checking under the new resolver. No parity check against
  the legacy path: the legacy path is deleted in the same PR
  rather than kept behind a flag (pre-1.0).
- Adaptive diagnostic rendering (§8.2), auto-import quick-fix
  (§8.3), named-import rejection (§8.5).
- Snapshot tests on converter output via `go-snaps`;
  `tools/dts_to_esc/ --check` runs in CI to catch upstream TS
  changes (§5.4).

### Non-functional requirements

- **Filesystem-resident stdlib data.** `.esc` files under
  `internal/interop/data/` ship alongside the compiler binary
  and are loaded from disk at compile time, **not** embedded
  via `//go:embed`. Discovery per §2.2a. This is a deliberate
  divergence from the requirements doc's "embedded data" line:
  user-editability of builtins (tweaking a type, adding a
  package) without recompiling the compiler is a higher
  priority than the single-binary distribution win that
  embedding would provide.
- **Zero runtime cost.** Pseudo-package imports erase at
  codegen.
- **Soundness of activation.** A file's view of cross-package
  augmentations is fully determined by that file's own
  imports.
- **Ergonomics.** `?local` default; single-class shortcut keeps
  per-class access terse.

### Risks (from requirements §"Risks")

Tracked here for visibility; mitigations are baked into the
phasing above:

- **FR14 printer fidelity** — gated by §1; if the audit
  surfaces unsupported forms, parser/printer follow-ups
  precede §4.
- **Ergonomic cost of imports** — mitigated by auto-import
  quick-fix (§8.3, hard requirement), suggestion-bearing
  diagnostics (FR15/§8.2), and the single-class shortcut
  (FR5/§2.4).
- **Initial bootstrap quality** — mitigated by human review
  pass at §6 and by `--check` mode at §5.4.
- **Cross-package augmentation mechanism** — investigation
  output is the first deliverable of §3.1; if reuse fails, the
  loader-path work adds to §3.
- **Polyfill story is separate.** This workstream assumes
  polyfill insertion at lowering is tractable (per FR12). No
  polyfill work happens here.

### Backwards-compatibility

**Not applicable — pre-1.0.** Escalier has no released compiler
yet, so there are no external users to migrate and no
deprecation cycle to manage. §7 swaps the prelude and deletes
the legacy paths in one PR; internal fixtures are migrated
(§8.5) in the commit immediately preceding so the suite stays
green.

Diagnostic-assisted migration (§8.2a) and the FR16 auto-import
quick-fix (§8.3) are still implemented — not for migration, but
because they are first-class ergonomics features under the new
model (FR15 / FR16 are hard requirements). No automatic codemod
for user code is included.

(The requirements doc's "Backwards-compatibility and deprecation
policy" section describes a single deprecation cycle with a
build flag and a one-release parallel-paths window. Pre-1.0 lets
us skip that — when the requirements doc is next revised, the
section can be reframed as the FR15/FR16 ergonomics story rather
than as a migration plan.)

---

## FR coverage matrix

A satisfaction check: every functional requirement maps to one
or more phases above.

| FR    | Topic                                      | Covered in                  |
| ----- | ------------------------------------------ | --------------------------- |
| FR1   | No ambient set; two-mode loading           | §5.1 (partition), §7 (lazy shape-load + legacy-path deletion), Drops subsection in §5.1 (`globalThis`/`eval`/`EvalError`) |
| FR2   | Pseudo-package layout                      | §2.2 (resolver mapping + underscore convention), §2.2a (stdlib data directory discovery), §5.1 (full enumeration), §5.3 (output layout + distribution) |
| FR3   | URI-scheme import grammar                  | §2.1 (parser), §2.2 (resolver), §2.2b (runtime erasure)                                                          |
| FR4   | Binding-shape flags                        | §2.3 (all three shapes, mutual exclusion, extensibility, collision warning, URI-keyed bookkeeping)               |
| FR5   | Single-class shortcut                      | §2.4; eligibility list in §5.1                                                                                   |
| FR6   | Inter-package imports                      | §3.4 (cycles permitted within pseudo-package layer)                                                              |
| FR7   | Cross-package augmentation (registries)    | §3.1 (mechanism investigation), §3.2 (registry pattern), §3.2a (no `override declare`), §3.2b (per-API fallback) |
| FR8   | Symbol augmentation                        | §3.3 (registry mechanism + re-export naming convention)                                                          |
| FR9   | Augmentation activation semantics          | §3.2 (per-file scoping, explicit-re-export propagation, flag independence)                                       |
| FR10  | Bootstrap converter                        | §4 (MVP, trio idiom, namespace flattening), §4.0 (JSDoc precursor), §5.1 (partition), §5.2 (routing), §5.4 (`--check`), §5.5 (`throws`), §5.6 (TS-bump workflow) |
| FR11  | Prelude changes; lazy shape loading        | §7.1 (trigger map), §7.2 (shared parsed copies), §7.2a (cross-package verification), §7.3 (legacy-path deletion in same PR) |
| FR12  | Always-current API; polyfills at lowering  | Acknowledged as out-of-scope dependency in cross-cutting; type checker sees modern surface unconditionally       |
| FR13  | Intrinsic types checker-resident           | §8.1 (handlers stay, `Awaited<T>` source-first with documented-fallback requirement, parser rejects `intrinsic`) |
| FR14  | Declaration printer audit                  | §1 (entire phase)                                                                                                |
| FR15  | Adaptive diagnostic rendering              | §8.2 (renderer + tie-breaking), §8.2a (migration suggestions)                                                    |
| FR16  | Auto-import (LSP first-class)              | §8.3 (quick-fix, name-index, binding-shape preference, name-collision ordering)                                  |

**Non-functional / cross-section coverage:**

- **Ergonomics, soundness of activation, zero runtime cost,
  filesystem-resident stdlib data** — Cross-cutting "Non-functional requirements".
- **`--explain-type` diagnostic** — §8.4.
- **Source-map and diagnostic provenance** — §8.6.
- **Error-message taxonomy** — Cross-cutting "Error taxonomy"
  (six failure modes).
- **Testing strategy** — Cross-cutting "Testing strategy
  summary".
- **Risks** — Cross-cutting "Risks", each tied to a mitigating
  phase.
- **Backwards-compatibility** — Cross-cutting
  "Backwards-compatibility".

**Things the requirements explicitly leave out** (so the
absence is correct, not a gap): lazy `.d.ts` → `.esc`
conversion for third-party npm packages,
`node_modules/.cache/escalier/`, per-dep / user-project
overrides, `escalier cache clean` CLI, original steps 10–11
(third-party lazy cache; deletion of the runtime interop
pipeline), `node:*` content, polyfill insertion at lowering,
and **named imports from pseudo-packages** (rejected with a
helpful diagnostic per the error taxonomy). None of these
appear as work items above.
