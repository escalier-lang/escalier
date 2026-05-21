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
| 3   | Codegen lowering and `@js` decorators                | FR3         | ⬜      | §1         | Decorator parser support (new grammar); per-declaration `@js("...")` carries the JS-runtime expression each pseudo-package member lowers to; codegen drops scheme-prefixed `import` lines and emits the decorator's argument at each reference. |
| 4   | Cross-package augmentation + inter-package imports   | FR6–FR9     | ⬜      | §2         | Confirm §6 interface-merge can be reused for cross-package augmentation (registry interfaces only) with per-importing-file activation. Pseudo-package import cycles are permitted. Well-known symbols stay on `Symbol`; domain packages re-export aliases (FR8). |
| 5   | Converter MVP (`tools/dts_to_esc/`)                  | FR10        | ⬜      | §1, §3     | Two tiny slices: a trio-idiom class (`Boolean`) and a small `declare namespace` block (e.g. `JSON`). AST-to-AST translation; emit to stdout; no partition logic. Exercises trio recognition + namespace flattening; emits `@js` decorators per §3. |
| 6   | Converter productionization                          | FR10        | ⬜      | §5         | Partition table; full output paths under `internal/interop/data/{std,dom}/`; `--check` mode; full `lib.*.d.ts` input set; registry/well-known-symbol routing.                                                                                        |
| 7   | Stdlib bootstrap (committed `.esc` files)            | FR1–FR2     | ⬜      | §6         | Run the converter once; review; hand-edit high-value `throws`, lifetimes, mutability; commit.                                                                                                                                                        |
| 8   | Internal fixture migration                           | (precedes §9) | ⬜ | §4, §7    | Migrate Escalier's own fixtures to `import "std:*"`. Must land **before §9** so the prelude switchover doesn't break the test suite. Requires §7 because the imports resolve against the committed `.esc` files; requires §4 for any fixture that touches DOM-style augmentation. The legacy prelude still resolves previously-ambient names side-by-side until §9 deletes it. |
| 9   | Prelude switchover + override deletion               | FR11, FR12  | ⬜      | §2, §4, §7, §8 | Replace `lib.*.d.ts` walking in [prelude.go](../../internal/checker/prelude.go) with the per-file shape loader. Delete the legacy `BuildBuiltinStore` / `loadGlobalDefinitions` / `populateSelfParams` / `UpdateMethodMutability` / `mergeReadonlyVariant` / `mutabilityOverrides` paths in the same PR — pre-1.0, no deprecation cycle. |
| 10  | Intrinsics, adaptive rendering, LSP support          | FR13, FR15, FR16 | ⬜ | §9      | Implement adaptive diagnostic rendering (FR15) and the auto-import quick-fix (FR16); verify `Awaited<T>` source-level definition with documented-fallback policy; confirm intrinsic handlers stay checker-resident (FR13).                                                                                                          |

**Dependency graph** (edges are "must land before"; only direct
edges shown — transitive deps omitted for clarity):

```
                  ┌─ §2 ── §4 ──────────────┐
§1 (audit) ───────┤                         ├── §8 ── §9 ── §10
                  └─ §3 ── §5 ── §6 ── §7 ──┘
```

Two lanes diverge from §1 and reconverge at §8: the upper lane
(§2 → §4) builds the resolver and augmentation machinery; the
lower lane (§3 → §5 → §6 → §7) builds the decorator parser,
the converter, and the committed `.esc` files. §8 needs both
lanes — fixtures import the `.esc` files via the resolver, and
any DOM-touching fixture relies on augmentation. §9 cuts over
the prelude; §10 adds LSP/diagnostic tooling on top.

**Step ordering rationale.** §1 is first because a failed audit
forces parser work that gates everything else. §2 (resolver),
§3 (decorator parser + codegen lowering), and §5 (converter MVP)
can run in parallel after §1 — they share no internal dependency
beyond the audit. §3 must land before §5 lands its decorator
emission step; §3 must also land before any fixture exercises
codegen end-to-end. §4 lands after §2 because augmentation tests
need real `import` statements. §7 produces the source-of-truth
`.esc` files. §8 migrates internal fixtures to `import "std:*"`
while the legacy prelude is still live; §9 then swaps the prelude
and deletes the legacy paths in a single cut (pre-1.0, no
deprecation cycle); §10 adds the LSP / diagnostic tooling on top.

**Why §8 precedes §9.** §9 deletes the legacy path in the same
change that swaps the prelude, so existing fixtures must already
be migrated to `import "std:*"`. §8 is feasible once §7 has
committed the `.esc` files (so imports actually resolve) and §4
has landed (so any DOM-touching fixture can use augmentation);
the resolver from §2 is in place transitively via §7. The legacy
prelude still resolves previously-ambient names side-by-side
during the fixture-rewriting commit, then §9 removes it. §10
(LSP, adaptive rendering, intrinsic verification) has no
ordering constraint relative to §9 other than building on the
post-switchover code.

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
- **Decorator syntax** (`@js("...")`) on every
  decorator-eligible declaration form, in combination with the
  `export` modifier — see §3.3 for the grammar. Decorators are
  new to the parser; the audit confirms the lexer, parser, AST,
  and printer round-trip `<decorators> export declare <kind>`
  (the canonical pseudo-package shape) and reject decorators
  placed between `export` and `declare`.
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
   extend the parser or printer; gate §5 on those follow-ups
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

1. **`--stdlib-dir <path>` CLI flag** on `escalier check` /
   `escalier build` / `lsp-server`. Highest precedence
   (standard CLI convention: explicit flags beat ambient
   configuration).
2. **`ESCALIER_STDLIB_DIR` environment variable.** Absolute
   path to a directory containing `std/` and `dom/`
   subdirectories. Used only when `--stdlib-dir` is not
   supplied. Intended for contributors testing alternative
   stdlibs and for tooling that ships its own.
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
- **`?flat` name collision is a hard error at the second
  `import` statement and aborts compilation**, per FR4. The
  diagnostic points at the URI literal of the second import
  and names the prior package that contributed the colliding
  identifier. No use-site unbound-name diagnostics are emitted
  for the colliding name; compilation does not proceed past
  the resolver. See "Error taxonomy" cross-cutting section.
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

## §3. Codegen lowering and `@js` decorators (FR3)

**Goal.** Lower references to pseudo-package members to the
correct JS runtime expression, and erase pseudo-package `import`
statements at codegen. The lowering mapping is carried by
per-declaration `@js` decorators inline in the pseudo-package
`.esc` source.

Pseudo-package imports are **type-system-only, runtime-erased**.
The codegen drops `import` statements whose specifier carries a
`std:`, `dom:`, or `node:` scheme before emitting JS. Zero
import-line artifact.

References to pseudo-package members must still lower to the
correct JS runtime expression, and the Escalier-side binding name
is not generally the JS-side name (`math.sin(x)` → `Math.sin(x)`;
`parseInt(s)` from `std:number` → bare `parseInt(s)`;
`iterator.key` re-export → `Symbol.iterator`; etc.). The mapping
is carried by **per-declaration `@js` decorators** authored
inline in the pseudo-package `.esc` source.

### 3.1 `@js` decorator semantics

Every **exported** top-level declaration in a pseudo-package
`.esc` file carries an `@js` decorator whose argument is the JS
expression that the declaration lowers to. Pseudo-package files
follow the regular Escalier module rule: visibility outside the
file requires explicit `export`, and only exported declarations
participate in the package's namespace. Internal helper
declarations (used only inside the file) are not exported and
carry no `@js`. Examples:

```escalier
// std/math.esc
@js("Math.sin")
export declare fn sin(x: number) -> number

@js("Math.PI")
export declare val PI: number

// std/number.esc — hoisted globals share a package with Number
@js("parseInt")
export declare fn parseInt(s: string, radix?: number) -> number

@js("Number")
export declare class Number { … }

// std/iterator.esc — Symbol re-export
@js("Symbol.iterator")
export declare val key: unique symbol

// std/array.esc — single-class shortcut package
@js("Array")
export declare class Array<T> { … }

// std/async.esc — package-private helper, no export, no @js
declare type Thenable<T> = { then(onfulfilled: (v: T) => void): void }
```

There is no package-level default. Every exported declaration is
annotated explicitly. The converter (§5–§6) emits `export` and
`@js` on every declaration it produces; hand-authored
declarations at §7 (`Symbol.customMatcher`, Symbol re-exports,
etc.) write both keywords explicitly. The loader rejects an
exported declaration missing `@js` (§3.4); an unexported
declaration with `@js` is also rejected as nonsensical
(the decorator only matters at codegen sites, which only see
exported names).

### 3.2 Lowering rules

- **Member access through a package binding** (`math.sin(x)`,
  `std.math.sin(x)` under `?nested`, `std.sin(x)` under `?flat`)
  collapses to the underlying declaration's `(package, name)`
  identity and lowers to that declaration's `@js` expression
  applied to the call's arguments. Binding shape is purely an
  Escalier-side concern; codegen never sees it.
- **Single-class shortcut bindings** (`Array`, `Date`, …) resolve
  to the class declaration and lower via its `@js` decorator.
  `Array.isArray(xs)` lowers to `Array.isArray(xs)` via the class
  declaration's `@js("Array")` decorator plus the static member.
- **`@js` arguments are JS expressions, not just identifiers.**
  Dotted forms like `"Math.sin"`, `"Symbol.iterator"` are valid;
  the codegen pastes them in textually. This keeps the
  representation tiny — no parsed JS-side AST needed for the 99%
  case.
- **Class construction with `new`** is **not** carried by `@js`.
  The checker knows whether a callable is a class; the codegen
  inserts `new` at the construction site based on the callee's
  type, regardless of how the class declaration's `@js` is
  spelled. So `Date()` in Escalier lowers to `new Date()` even
  though the decorator just says `@js("Date")`.

### 3.3 Parser dependency

The Escalier parser does **not** currently support decorator
syntax. This phase adds it:

- Lex `@<ident>` as a new decorator-introducer token.
- Parse a decorator as `@ident(<arg>)` where `<arg>` is, for
  this workstream, a single string literal. The grammar leaves
  room for richer decorator arguments in the future (named args,
  identifier args) without committing to them now.
- **Placement.** Decorators sit **above** any modifier keywords
  on the declaration they target. The canonical ordering is
  `<decorators> export declare <kind> ...`:
  ```escalier
  @js("Math.sin")
  export declare fn sin(x: number) -> number
  ```
  Decorators between `export` and `declare` are a parse error.
  Multiple decorators on one declaration stack top-to-bottom;
  ordering preserved for printer round-trip.
- Decorators are allowed on `declare fn`, `declare class`,
  `declare val`/`declare var`, `declare type`, and interface
  declarations — in both exported and unexported positions, but
  see the loader rule in §3.4: unexported declarations carrying
  `@js` are rejected for pseudo-package files. Decorators are
  disallowed on inner declarations (members, parameters) — out
  of scope for this workstream; revisit if a concrete need
  surfaces.
- Printer round-trips decorators (FR14 audit must cover them,
  in combination with the `export` modifier).

Touch points:
[internal/lexer_util/](../../internal/lexer_util/),
[internal/parser/decl.go](../../internal/parser/decl.go),
[internal/ast/](../../internal/ast/) (new `Decorator` AST node
and field on declarations),
[internal/printer/](../../internal/printer/).

The decorator grammar must land in the FR14 audit scope (§1) so
the converter (§5) can rely on round-trip behavior.

### 3.4 Codegen

Touch point:
[internal/codegen/](../../internal/codegen/) — at every
pseudo-package symbol reference, resolve the binding to the
underlying declaration, read its `@js` decorator, and emit the
decorator's argument as the JS expression. Import statements
carrying a `std:`/`dom:`/`node:` scheme are dropped (no JS
output).

**Loader rules** (enforced after `.esc` parse, before
type-check):

1. Every **exported** top-level declaration in a pseudo-package
   file must carry an `@js` decorator. Missing `@js` is an
   internal-compiler-error naming the file and declaration.
2. An **unexported** top-level declaration in a pseudo-package
   file must **not** carry an `@js` decorator. The decorator
   only matters at codegen sites, which reference exported
   names; an unexported `@js` declaration is dead and almost
   certainly a typo (someone forgot `export`). Error message
   tells the user to add `export` or drop `@js`.

Both rules apply only to files under the resolved stdlib data
directory (§2.2a). User code is free of these constraints.

### 3.5 Gates

- Parser round-trips `@js("...")` decorators above `export
  declare` on every decorator-eligible declaration form (folded
  into the FR14 audit, §1). Decorator between `export` and
  `declare` is a parse error.
- Codegen fixture under [fixtures/](../../fixtures/) covers:
  - Namespace member: `math.sin(x)` → `Math.sin(x)`.
  - Hoisted global: `parseInt(s)` → `parseInt(s)`.
  - Symbol re-export: `iterator.key` → `Symbol.iterator`.
  - Single-class shortcut: `Array.isArray(xs)` →
    `Array.isArray(xs)`; `Date()` (construct) → `new Date()`.
  - Binding-shape independence: the same call lowers identically
    under `?local`, `?nested`, and `?flat`.
  - Package-private declaration (unexported, no `@js`) is
    invisible to importers — referencing it from outside the
    pseudo-package errors as unbound.
- Loader checks fire on (a) an exported pseudo-package
  declaration missing `@js`, and (b) an unexported pseudo-package
  declaration carrying `@js`. Both are negative tests.
- Generated JS contains no scheme-prefixed `import` lines.

---

## §4. Cross-package augmentation + inter-package imports (FR6–FR9)

**Goal.** A file that imports `dom:canvas` sees
`createElement("canvas") → HTMLCanvasElement`; a sibling file
without the import does not. Inter-package imports between
pseudo-packages work, including cycles. (Well-known symbols are
**not** augmented — all of them live on `Symbol`'s static side
in `std:symbol`; domain packages re-export them as plain
aliases per FR8, no augmentation machinery involved.)

### 4.1 Confirm the augmentation mechanism

The [§5 (interop_mutability)](../interop_mutability/implementation_plan.md#5-override-file-format-loader--merge)
override-merge code already does interface merging at a fixed
merge point. Cross-package
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

### 4.2 Open-registry augmentation (FR7)

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
§4.1's investigation must confirm both:

1. **Open interfaces / cross-package interface merging** — the
   [§5 (interop_mutability)](../interop_mutability/implementation_plan.md#5-override-file-format-loader--merge)
   merge code can be applied per importing file, or a new
   loader path is built.
2. **Indexed access over open registries** refreshes correctly
   when the registry is augmented:
   `HTMLElementTagNameMap[K]` where
   `K extends keyof HTMLElementTagNameMap` must pick up the
   augmented entries in the importing file's view, not a
   snapshot taken at registry-declaration time.

If either fails, the gap becomes a follow-up issue blocking §4
completion.

### 4.2a No `override declare` for builtins (FR7)

The `override declare` syntax — designed for the third-party
workstream's override mechanism — is **not** used for builtin
augmentation. Pseudo-package augmentation uses plain
`declare interface Foo { … }` blocks added to the augmenting
package's `.esc` file. The converter (§6) must emit
augmentation blocks in plain `declare interface` form, not
`override declare`. Reviewers of §7 verify this on the
committed files.

### 4.2b Per-API augmentation fallback (FR7)

For the rare cross-package API that does not fit a registry
pattern, the requirements specify two fallback patterns. Track
each occurrence in the partition table (§6.1) so it is
deliberately classified, not silently mis-routed:

1. **Parameter-typed APIs** — the feature package exports the
   type; `dom:dom` references it via the type's qualified name;
   no augmentation needed.
2. **String-overload APIs without a registry** — the feature
   package augments the API's overload set directly. Rare;
   treated case-by-case when it arises.

The converter records candidate per-API augmentations in a
log so reviewers can confirm the classification during §7
bootstrap review.

### 4.3 Inter-package imports + cycles (FR6)

- Pseudo-packages `import` other pseudo-packages explicitly
  (e.g. `dom/canvas.esc` does `import "dom:dom"` to extend
  `HTMLElement`).
- **Cycles between pseudo-packages are permitted.** The
  resolver / `internal/dep_graph/` must special-case `std:`,
  `dom:`, `node:` schemes to skip cycle reporting when both
  endpoints of an edge live under these schemes.
- Cycles among user packages, and user-package-to-pseudo-package
  cycles, remain disallowed.

### 4.4 Tests and gate

- Two-file fixture: `dom/dom.esc` declares an empty
  `HTMLElementTagNameMap`; `dom/canvas.esc` augments it with
  `canvas: HTMLCanvasElement`. Importing file gets the narrow
  return; sibling file gets `never` / base shape.
- Run the fixture under each of `?local`, `?nested`, `?flat` —
  binding shape must not affect augmentation visibility.
- Cycle fixture: two `std:*` packages with a mutual import; the
  resolver accepts the cycle; the same shape between two user
  packages errors.

(Symbol re-export aliasing — `iterator.key` as an alias of
`Symbol.iterator` via the `@js` decorator — is covered by §3.5's
codegen fixtures and the §7 bootstrap review; no separate
augmentation test is needed because Symbol no longer uses the
augmentation mechanism, see FR8.)

**Gate.** All three fixtures pass; the dependency-investigation
note in §4.1 is committed.

---

## §5. Converter MVP (FR10)

**Goal.** A minimal `tools/dts_to_esc/` Go binary that translates
two tiny TS-lib slices to readable, parseable `.esc`:

1. **A trio-idiom class.** `Boolean` from `lib.es5.d.ts` (~10
   lines: `interface Boolean { … }` + `interface BooleanConstructor
   { … }` + `declare var Boolean: BooleanConstructor`). Exercises
   work item 3 (class-via-trio recognition) and confirms the
   emitted form is `@js("Boolean") export declare class Boolean
   { … }`.
2. **A `declare namespace` block.** A small namespace from
   `lib.es5.d.ts` (e.g. `JSON` declared as
   `declare namespace JSON { fn parse(...); fn stringify(...); }`,
   or `Reflect` — pick whichever is smaller in the pinned TS
   version). Exercises work item 4 (namespace flattening). Each
   member becomes a top-level `export declare fn` in the output
   file, carrying `@js("<Namespace>.<fn>")` per work item 8 —
   e.g. `@js("JSON.parse") export declare fn parse(…) -> …`.

Covering both shapes in the MVP means the two highest-risk
translations (trio recognition and namespace flattening) each
have a concrete fixture by the time §6 productionizes the
converter against the full lib set.

**Location.** New directory `tools/dts_to_esc/` alongside
existing `tools/gen_ast/` and `tools/gen_types/`.

### 5.0 Precursor: `dts_parser` JSDoc retention

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
4. **Flatten `declare namespace` blocks.** Per FR10 step 2,
   TS `declare namespace Foo { … }` becomes top-level
   declarations in the output file (each pseudo-package file
   is itself a namespace). The converter does not emit nested
   namespace syntax; the FR14 audit (§1) excludes
   `declare namespace` from the supported declaration forms.
5. **Receiver mutability seeding.** Run `interop.Classify`
   (tiers 3/5/6 from the interop_mutability workstream) at
   conversion time to seed `self` vs `mut self` on each method.
6. **JSDoc pass-through.** Leading JSDoc on a TS declaration
   carries through to the emitted Escalier declaration as a
   doc comment. Strip TS-specific tags (`@override` dropped;
   `@param` syntax adjusted where Escalier differs); pass the
   rest verbatim. Precursor: §5.0 above. The JSDoc tag
   stripping/rewriting table is a small in-tree config inside
   `tools/dts_to_esc/`, easy to extend as cases surface.
7. **Intrinsic stripping.** `intrinsic`-typed declarations are
   skipped (FR13). The parser does not learn the `intrinsic`
   keyword.
8. **`export` and `@js` decorator emission** (§3). Every emitted
   top-level declaration is `export`-prefixed (pseudo-package
   files follow the regular Escalier module visibility rule) and
   carries an `@js(...)` decorator naming the JS expression it
   lowers to. The canonical shape is
   `<decorators> export declare <kind> ...`. The MVP slices
   exercise both rule branches:
   - Trio-idiom class → `@js("<ClassName>")` (`Boolean` →
     `@js("Boolean") export declare class Boolean { … }`).
   - Namespace member → `@js("<Namespace>.<fn>")` after the
     namespace flattening of step 4 (`JSON.parse` →
     `@js("JSON.parse") export declare fn parse(…) -> …`).
   The general `@js` rule also covers declarations hoisted from
   the global scope into a partition package (e.g. `parseInt` →
   `std:number`), which get `@js("<bare name>")` — exercised in
   §6 against the full lib set, not in the MVP. The converter
   does not emit unexported declarations — every TS-side
   top-level declaration that the partition table maps to a
   package is exposed. Symbol re-exports and other hand-authored
   declarations are §7 territory.
9. Emit via the (now-audited) declaration printer and
   [internal/type_system/print_type.go](../../internal/type_system/print_type.go).
   Emit to stdout. No file layout, no partition table yet.

**Gate.** Output for both MVP slices (the trio-idiom class and
the small namespace):

- Parses through `parser.ParseFile`.
- Reads naturally to a human (snapshot-tested via `go-snaps`).
- Two consecutive conversions produce byte-identical output.
- The namespace slice emits zero `declare namespace` blocks in
  the output — every former-namespace member is a top-level
  declaration with `@js("<Namespace>.<fn>")`.
- The trio slice emits exactly one `declare class` and zero
  `declare var` (the constructor's `declare var` is consumed by
  the trio recognizer).

---

## §6. Converter productionization (FR10)

**Goal.** Convert the full pinned `lib.*.d.ts` set into the
committed package partition.

### 6.1 Partition table

A hand-maintained Go map in the converter source: TS-lib
declaration name → target pseudo-package. Drives both file
output and the LSP name-index (§10.3). Driven by the
[FR1 partition list](requirements.md#fr1-no-ambient-set-shape-loaded-vs-named-bindings).

**`std/` (full enumeration).**

| Package           | Type                | Members / notes                                                                                                                  |
| ----------------- | ------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| `std:array`       | per-class           | `Array<T>`                                                                                                                       |
| `std:string`      | per-class           | `String`                                                                                                                         |
| `std:number`      | per-class           | `Number`; also `parseInt`, `parseFloat`, `isNaN`, `isFinite` (numeric-parsing domain)                                            |
| `std:boolean`     | per-class           | `Boolean`                                                                                                                        |
| `std:bigint`      | per-class           | `BigInt`                                                                                                                         |
| `std:regexp`      | per-class           | `RegExp`; re-exports the regex-related well-known symbols (`Symbol.match`, `replace`, `search`, `split`, `matchAll`) as `regexp.matchKey`, `regexp.replaceKey`, etc.        |
| `std:symbol`      | per-class           | `Symbol`, including **all** well-known symbols (`Symbol.iterator`, `Symbol.asyncIterator`, `Symbol.match`, `Symbol.toPrimitive`, …) declared directly on `Symbol`'s static side |
| `std:object`      | per-class + utility | `Object`; `Partial`, `Required`, `Readonly`, `Pick`, `Omit`, `Record`, `Exclude`, `Extract`, `NonNullable`                       |
| `std:function`    | per-class + utility | `Function`; `Parameters`, `ConstructorParameters`, `ReturnType`, `InstanceType`, `ThisParameterType`, `OmitThisParameter`, `ThisType` |
| `std:date`        | per-class           | `Date`                                                                                                                           |
| `std:map`         | per-class           | `Map`                                                                                                                            |
| `std:set`         | per-class           | `Set`                                                                                                                            |
| `std:weak_ref`    | per-class           | `WeakRef`                                                                                                                        |
| `std:iterator`    | bundled             | `Iterator<T>`, `Iterable<T>`, `IterableIterator<T>`, `IteratorResult<T>`, `Generator<T,R,N>`; re-exports `Symbol.iterator` as `key`         |
| `std:async`       | bundled             | `Promise<T>`, source-level `Awaited<T>`, `AsyncIterator<T>`, `AsyncIterable<T>`, `AsyncGenerator<T,R,N>`, `AggregateError`; re-exports `Symbol.asyncIterator` as `key`; depends on `std:iterator`. `Promise` lives here (not in a dedicated `std:promise`); under `?local` access is `async.Promise.all(…)`. |
| `std:error`       | bundled             | `Error`, `TypeError`, `RangeError`, `SyntaxError`, `ReferenceError`. `URIError` → `std:url`; `AggregateError` → `std:async`. `EvalError` dropped |
| `std:url`         | bundled             | `URIError`, `encodeURI`, `decodeURI`, `encodeURIComponent`, `decodeURIComponent`                                                 |
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
`dom:forms`. Additional `dom:*` packages may be added in §7
review as the partition is exercised against the full lib
input.

**Single-class shortcut eligibility (FR5).** Per FR5, the
single-class shortcut applies to:

`std:array → Array`, `std:string → String`,
`std:number → Number`, `std:boolean → Boolean`,
`std:bigint → BigInt`, `std:regexp → RegExp`,
`std:symbol → Symbol`, `std:object → Object`,
`std:function → Function`, `std:date → Date`, `std:map → Map`,
`std:set → Set`, `std:weak_ref → WeakRef`. The converter does
not mark this explicitly — the shortcut activates structurally
when the package's lowercased URI segment matches a top-level
class name case-insensitively. `std:async` does **not**
qualify (multiple top-level classes including `Promise`,
`AsyncIterator`, …; no class named `Async`), so `Promise.all`
is accessed as `async.Promise.all` under `?local`.

**Drops.** `globalThis` and `eval` drop entirely — `eval` has
no good use case; `globalThis` was the union of every
previously-ambient name and has nothing to take its union over
in the new model. The converter recognizes both and skips
emission with a logged note. `intrinsic`-typed declarations
(FR13) skip the same way.

**Unmapped-symbol fail-safe.** Per FR10 step 4: any top-level
TS-lib declaration name absent from both this partition table
and the explicit drop list above causes the converter to abort
with a diagnostic naming the symbol, its source `.d.ts` file,
and this partition-table file. No catch-all "unmapped" package
and no silent drop. The check lives at the partition-table
lookup site so misses surface where the routing decision is
actually made.

**`node:*`.** Partition deferred until Node support lands. The
`internal/interop/data/node/` directory is created empty.

### 6.2 Registry routing

- Detect TS-lib entries on registry interfaces
  (`HTMLElementTagNameMap`, `HTMLElementEventMap`,
  `SVGElementTagNameMap`, `MathMLElementTagNameMap`, …) and
  route each entry into the corresponding feature package's
  augmentation block.
- Well-known symbol declarations on `SymbolConstructor` stay
  in `std/symbol.esc` — they are **not** rerouted (FR8). The
  domain-package re-export aliases (`iterator.key`, `async.key`,
  `regexp.matchKey`, …) are hand-authored at §7 bootstrap, not
  emitted by the converter.

### 6.3 Output layout

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

### 6.4 Re-run semantics and `--check` mode

Re-running the converter against committed files is **additive
and signature-checked**, not a wholesale overwrite. The hand-edits
under [§7](#7-stdlib-bootstrap) (`throws`, lifetimes, mutability
refinements) must survive a re-run.

**Default re-run (write mode):**

- **Add** declarations present in the `.d.ts` but missing from the
  committed `.esc` (new TS-lib symbols since last bump).
- **Add** members on existing classes / interfaces that the `.d.ts`
  has but the `.esc` is missing.
- **Never overwrite** an existing declaration's body, signature, or
  hand-added annotations. Hand-edits are sticky.
- **Report** declarations present in the `.esc` but absent from the
  `.d.ts` (likely TS-side removal) — informational, no automatic
  deletion.

**`--check` mode (CI):** read-only verification that fails CI on
any of:

1. **Missing declarations.** A `.d.ts` declaration with no
   corresponding `.esc` declaration in the partition's target file.
2. **Incompatible signature drift.** An `.esc` function / method
   signature whose param or return types are not assignable to /
   from the `.d.ts` original (per the checker's assignability
   rules, applied to the converted-from-TS form). Catches
   accidental hand-edits that change the meaning of a signature
   rather than refining it.
3. **Incompatible property-type drift.** Same check for properties
   on classes / interfaces.

Adding `throws`, lifetimes, mutability, or narrowing a parameter /
return type within the `.d.ts` shape is **not** incompatible and
does not trip `--check`. The compatibility check is one-directional
in the obvious sense (Escalier-side may be stricter than TS-side,
not looser).

This is the TS-version-bump review tool — **never auto-applies**
deletions or signature changes.

### 6.5 `throws` annotations (bootstrap policy)

Per [FR10](requirements.md#fr10-bootstrap-converter-tools-dts_to_esc):
hand-curate the high-value ~50 entries (`JSON.parse`,
`decodeURI*`, `BigInt`, `fetch`, `Response.json`, etc.). Scraping
MDN is rejected (prose-not-data, brittle, copyleft).
WebIDL `[Throws]` extraction (`@webref/idl`) is a plausible
future automation lever for `dom:*` but is **out of scope for
the bootstrap**. ECMARKUP extraction for `std:*` is similarly
deferred.

### 6.6 TS-version-bump workflow

**CLI shape.** `tools/dts_to_esc/` is a single Go binary with
subcommands:

- `dts_to_esc check` — read-only verification (§6.4); CI uses this.
- `dts_to_esc regenerate` — additive write mode (§6.4); adds new
  declarations / members from upstream TS without overwriting
  existing bodies, signatures, or hand-edits.
- `dts_to_esc bootstrap` — one-time initial seeding from a fresh
  TS-lib input set (no committed `.esc` tree assumed); used by §7.

Document the bump workflow in `tools/dts_to_esc/README.md`:

1. Bump the pinned TS dependency.
2. Run `dts_to_esc check`. Output is a unified diff against
   current committed files showing TS-side adds / removes /
   changes plus any compatibility errors.
3. Optionally run `dts_to_esc regenerate` to apply additive
   changes; review the diff and commit.
4. Contributor ports any signature / removal changes by hand and
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

## §7. Stdlib bootstrap

**Goal.** Commit the initial generated-then-hand-edited `.esc`
files as the source of truth.

**Work items.**

1. Run the converter (§6) once, producing the full tree under
   `internal/interop/data/{std,dom}/`.
2. Human review of every file. Hand-edit:
   - Obvious mis-classifications.
   - High-value `throws` annotations (the ~50 from §6.5).
   - Lifetimes where applicable (the existing
     [planning/lifetimes/](../lifetimes/) work feeds in here).
   - Mutability refinements not captured by the
     `interop.Classify` seeding.
   - `Symbol.customMatcher` (Escalier-specific, not in
     `lib.*.d.ts`) hand-authored in `std:symbol`, written as
     `@js("Symbol.customMatcher") export declare …` per §3.
   - **Symbol re-exports** per FR8 (`iterator.key`, `async.key`,
     `regexp.matchKey`, …) hand-authored in their owning
     packages, written as
     `@js("Symbol.<name>") export declare val <name>: unique symbol`.
     The converter does not emit these because they are not part
     of any `lib.*.d.ts`.
   - **`export` + `@js` decorator review.** Verify every
     exported top-level declaration has an `@js` decorator with
     a real JS-runtime expression as argument. Spot-check that
     declarations meant to be package-private (helper types,
     internal aliases) are correctly unexported and carry no
     `@js`. Missing-decorator, missing-`export`, or
     typo'd-target bugs ship to users otherwise; the §3 loader
     check catches them at compile time but humans should catch
     obvious cases here.
3. **`Awaited<T>` source-level definition.** Write `Awaited<T>`
   in `std:async` as the recursive conditional type (the same
   shape as TypeScript's definition). Exercise against a
   representative fixture (nested promises, thenables, mixed
   `T | Promise<T>`, generic propagation). If a concrete
   blocker surfaces, fall back to a checker-resident intrinsic
   and document the specific failure.
4. Commit. After this point, the converter persists only as
   `--check` review tool; ongoing edits are direct to the
   committed `.esc` files.

**Gate.** Humans review the committed files; `go test ./...`
passes — the existing checker still resolves these declarations
through the legacy `lib.*.d.ts`-walking prelude. §8 then
migrates fixtures and tests to `import "std:*"` while both
resolution paths are live, and §9 deletes the legacy path in a
single PR. The §7 commit on its own changes no checker
behavior; it just lands the source-of-truth `.esc` files.

---

## §8. Internal fixture migration

**Goal.** Migrate Escalier's own fixtures and tests to
`import "std:*"` so that §9 can delete the legacy lib-walking
prelude without breaking the suite.

**Prerequisites.** §7 (the `.esc` files must be committed for
imports to resolve) and §4 (cross-package augmentation must be
in place if any fixture touches DOM). §2's resolver is in place
transitively via §7.

Update every fixture and test under
[fixtures/](../../fixtures/) and
[internal/checker/tests/](../../internal/checker/tests/) that
relied on previously-ambient symbols (`Math`, `JSON`, `console`,
`Promise`, `Error`, `Array.from`, `parseInt`, …) to use
`import "std:*"` statements. The legacy prelude is still live
during this phase, so the migrated and not-yet-migrated files
type-check side-by-side until §9 removes the legacy path.

The auto-import quick-fix from §10.3 is the migration aid when
it is available; otherwise migration is by hand. Ordering between
this phase and §10.3 is not strict — fixture migration can proceed
without the quick-fix, but having the quick-fix first lets the
fixture rewrite exercise the same tooling external users will
rely on.

**Gate.** `go test ./...` passes with every fixture using
explicit `import "std:*"` statements; no fixture relies on
ambient resolution.

---

## §9. Prelude switchover + override deletion (FR11)

**Goal.** Replace the `lib.*.d.ts`-walking prelude with the lazy
per-file shape loader, and delete the legacy override / builtin
machinery in the same change. Pre-1.0; no deprecation cycle, no
build flag, no parallel paths.

**Hard prerequisite: §8 lands first.** Deleting the legacy
prelude removes the resolution path that previously made `Math`,
`JSON`, `console`, `Promise`, `Error`, `Array.from`, `parseInt`,
… visible without imports. Every fixture and test that names
those symbols must already be rewritten to use
`import "std:*"` — that is §8's job. §8 is feasible after §3 and
§4 land (resolver + augmentation in place) and runs while the
legacy prelude is still live, so migrated and not-yet-migrated
files type-check side-by-side throughout the §8 work. §9 is the
cut-over: the PR that opens §9 must have a green test suite on
`HEAD` *with* the legacy prelude still active, and must keep it
green *without* it once the legacy paths come out. If a fixture
slipped past §8, the §9 PR will fail CI on the unbound-name
diagnostics for the previously-ambient symbols.

### 9.1 Lazy shape-loader

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
| `async fn` / `await` / `for await x of xs`                         | `std:async` (covers Promise, Awaited, and async iteration) |
| `for x of xs` / generator                                          | `std:iterator`                       |
| `try` / `catch` / `throw` / `throws` clause **naming** a `std:error` class (`Error`, `TypeError`, …) | `std:error` — bare `throw "x"` or a `throws` listing only user-defined errors does not trigger |

Multiple files share one parsed copy of each package.
Shape-loading is idempotent and additive.

### 9.2 Explicit import loading

Reuses §2's resolver path. The shape-load and named-import paths
share the same parsed declarations; they differ only in whether
identifiers land in F's scope. Multiple files in a compilation
share **one parsed copy** of each `std:*` package; the
shape-loader memoizes by package URI. No bootstrap cycle: each
`std:*` package contains only declarations (no value-level
expressions needing their own prelude).

### 9.2a Cross-package shape-load verification

The risk callout from requirements §"Risks" — "cross-package
references between `std:*` files" — needs explicit
verification here. `Promise<T>` in `std:async` references
`Iterable<T>` from `std:iterator`; `Array<T>` in `std:array`
references the iteration protocol; etc. The existing module
loader handles cross-package references for user code; this
phase confirms it works for shape-loaded `std:*` packages
under the per-file shape-loader.

Test: a fixture that uses only an `async fn` + a `for of`
loop should trigger shape-loading of both `std:async` and
`std:iterator`, and `Promise<T>` should resolve `Iterable<T>`
references successfully without explicit imports being present
in user code. If this fails, the loader needs adjustment.

### 9.3 Delete the legacy paths (same PR)

Delete in the same change that swaps the prelude — no flag, no
parallel paths, no waiting. The compiler is pre-1.0; the
breaking change lands cleanly. Internal fixtures must already
have been migrated to `import "std:*"` (§8) before this PR
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
  (now sourced from `std:symbol` per §7 step 2)

**No `override declare` for builtins.** That syntax stays
reserved for the third-party workstream's override mechanism;
no builtin pseudo-package uses it.

**Gate.** `go test ./...` passes on the new path alone; the
legacy code is gone, not behind a flag.

---

## §10. Intrinsics; adaptive rendering; LSP support (FR13, FR15, FR16)

**Goal.** Ship the diagnostic + LSP tooling users need under the
new model, and confirm intrinsic handlers stay checker-resident.

### 10.1 Intrinsic types (FR13)

Confirm that `Uppercase`, `Lowercase`, `Capitalize`,
`Uncapitalize`, `NoInfer` remain checker-resident handlers — no
source file under `internal/interop/data/`. The four string-case
utilities are pure `Type → Type` resolvers; `NoInfer` is an
inference-machinery hook. Tracked in escalier-lang/escalier#631.

`Awaited<T>` source-level definition lives in `std:async` per
§7 step 3 (the recursive conditional type matching TS's
definition; tracked in escalier-lang/escalier#630). Fallback to
a checker-resident intrinsic only on documented blocker —
recursive conditionals don't reduce correctly, pathological
performance, or a soundness issue. **The fallback decision
must be committed with a documented description of the specific
failure that motivated it.** Concretely: a Go doc comment on the
checker-resident `Awaited` handler citing the failing fixture
under `internal/checker/tests/` (or `fixtures/`) that motivated
the fallback. Not duplicated in this plan.

The bootstrap converter strips `intrinsic`-typed declarations
encountered in TS source (FR13) — no `.esc` output is produced
for them, which means no `export` and no `@js` either. The
parser does **not** learn the `intrinsic` keyword. Verify the
parser still rejects `intrinsic` after this workstream lands
(regression guard).

### 10.2 Adaptive diagnostic rendering (FR15)

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

### 10.2a Diagnostic-assisted migration

When a name that used to be ambient is referenced without an
import under the new default, the **unbound-name diagnostic**
includes a suggestion ("did you mean to `import "std:async"`?")
whenever the unbound name matches a known pseudo-package export.
The suggestion list is derived mechanically from the LSP
name-index (§10.3); the diagnostic path reuses the same index.
This is the **fallback for command-line use** — users in a
supported editor get the FR16 quick-fix instead. Suggestion
text routes through the error-message taxonomy entries; spans
point at the bare reference, not the surrounding statement.

### 10.3 Auto-import quick-fix (FR16)

LSP first-class. Quick-fix on an unbound-name diagnostic that:

1. Adds the appropriate namespace import statement
   (`import "std:async"`, `import "std:math"`, …).
2. **Single-class shortcut packages:** leaves the bare reference
   unchanged (`Array.isArray`, `Date.now`, `Error(...)` already
   match the imported binding name).
3. **Other packages:** rewrites the bare reference to qualify
   through the resulting namespace (`sin(x)` → `math.sin(x)`;
   `Promise.all([...])` → `async.Promise.all([...])` since
   `Promise` lives in `std:async`, which is not a single-class
   shortcut package).

Named imports are out of scope. Quick-fix only adds namespace
imports.

Implementation:

- **Name → owning pseudo-package index.** Build at LSP startup
  by walking the resolved stdlib data directory (§2.2a) and
  reading top-level declaration names from each `.esc` file.
  Cache; **refresh on file change** via filesystem watch on
  the data directory — users editing their stdlib copy see the
  index update without restarting the LSP. Same index serves
  §10.2a diagnostic suggestions and §10.4 `--explain-type` hints.
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

### 10.4 `--explain-type` diagnostic refinement

When a tag-keyed return is wider than expected
(`createElement` returning the union element type instead of
`HTMLCanvasElement`), the diagnostic suggests likely `dom:*`
imports to widen the file's view. Complements the FR16
quick-fix for the type-narrowing case.

### 10.5 Source-map / diagnostic provenance

Per requirements §"Source-map and diagnostic provenance for
stdlib pseudo-packages":

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

**Gate.** LSP quick-fix integration test green; renderer fixture
per case (`?local` shortcut, `?local` non-shortcut, `?nested`,
`?flat`, no-import) passes; parser still rejects `intrinsic`.

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
- **`?flat` name collision** — **hard error** at the second
  import. Names the colliding identifier and the two source
  packages; points at the URI literal of the second import;
  instructs the user to rename upstream or drop one import's
  `?flat` flag.

Fixtures under [fixtures/](../../fixtures/) exercise each with
full message-text assertions per CLAUDE.md test conventions.

### Testing strategy summary

Per requirements §"Testing strategy":

- Parser, resolver, binding-shape (§2).
- Registry augmentation, activation scoping (§4); Symbol
  re-export aliases via `@js` (§3.5 codegen fixtures + §7
  bootstrap review).
- Prelude switchover (§9) — internal fixtures, migrated to
  `import "std:*"` ahead of the switchover commit (§8), keep
  type-checking under the new resolver. No parity check against
  the legacy path: the legacy path is deleted in the same PR
  rather than kept behind a flag (pre-1.0).
- Adaptive diagnostic rendering (§10.2), auto-import quick-fix
  (§10.3), named-import rejection (§2 parser/resolver).
- Snapshot tests on converter output via `go-snaps`;
  `tools/dts_to_esc/ --check` runs in CI to catch upstream TS
  changes (§6.4).

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
  precede §5.
- **Ergonomic cost of imports** — mitigated by auto-import
  quick-fix (§10.3, hard requirement), suggestion-bearing
  diagnostics (FR15/§10.2), and the single-class shortcut
  (FR5/§2.4).
- **Initial bootstrap quality** — mitigated by human review
  pass at §7 and by `--check` mode at §6.4.
- **Cross-package augmentation mechanism** — investigation
  output is the first deliverable of §4.1; if reuse fails, the
  loader-path work adds to §4.
- **Polyfill story is separate.** This workstream assumes
  polyfill insertion at lowering is tractable (per FR12). No
  polyfill work happens here.

### Backwards-compatibility

**Not applicable — pre-1.0.** Escalier has no released compiler
yet, so there are no external users to migrate and no
deprecation cycle to manage. §9 swaps the prelude and deletes
the legacy paths in one PR; internal fixtures are migrated
(§8) in the commit immediately preceding so the suite stays
green.

Diagnostic-assisted migration (§10.2a) and the FR16 auto-import
quick-fix (§10.3) are still implemented — not for migration, but
because they are first-class ergonomics features under the new
model (FR15 / FR16 are hard requirements). No automatic codemod
for user code is included.

(The requirements doc's "Backwards-compatibility" section has
been updated in step with this plan: no deprecation cycle, no
build flag. FR15/FR16 ergonomics are framed as first-class
features, not as migration aids.)

---

## FR coverage matrix

A satisfaction check: every functional requirement maps to one
or more phases above.

| FR    | Topic                                      | Covered in                  |
| ----- | ------------------------------------------ | --------------------------- |
| FR1   | No ambient set; two-mode loading           | §6.1 (partition), §9 (lazy shape-load + legacy-path deletion), Drops subsection in §6.1 (`globalThis`/`eval`/`EvalError`) |
| FR2   | Pseudo-package layout                      | §2.2 (resolver mapping + underscore convention), §2.2a (stdlib data directory discovery), §6.1 (full enumeration), §6.3 (output layout + distribution) |
| FR3   | URI-scheme import grammar; runtime erasure | §2.1 (parser), §2.2 (resolver), §3 (decorator-based lowering + import erasure)                                |
| FR4   | Binding-shape flags                        | §2.3 (all three shapes, mutual exclusion, extensibility, hard-error on `?flat` collision, URI-keyed bookkeeping) |
| FR5   | Single-class shortcut                      | §2.4; eligibility list in §6.1                                                                                   |
| FR6   | Inter-package imports                      | §4.3 (cycles permitted within pseudo-package layer)                                                              |
| FR7   | Cross-package augmentation (registries)    | §4.1 (mechanism investigation), §4.2 (registry pattern), §4.2a (no `override declare`), §4.2b (per-API fallback) |
| FR8   | Well-known symbol re-exports               | §7 step 2 (hand-authored re-export aliases with `@js("Symbol.<name>")`), §3 (decorator semantics carry the alias) |
| FR9   | Augmentation activation semantics          | §4.2 (per-file scoping, explicit-re-export propagation, flag independence — registries only)                     |
| FR10  | Bootstrap converter                        | §5 (MVP, trio idiom, namespace flattening), §5.0 (JSDoc precursor), §6.1 (partition), §6.2 (routing), §6.4 (`--check`), §6.5 (`throws`), §6.6 (TS-bump workflow) |
| FR11  | Prelude changes; lazy shape loading        | §9.1 (trigger map), §9.2 (shared parsed copies), §9.2a (cross-package verification), §9.3 (legacy-path deletion in same PR) |
| FR12  | Always-current API; polyfills at lowering  | Acknowledged as out-of-scope dependency in cross-cutting; type checker sees modern surface unconditionally       |
| FR13  | Intrinsic types checker-resident           | §10.1 (handlers stay, `Awaited<T>` source-first with documented-fallback requirement, parser rejects `intrinsic`) |
| FR14  | Declaration printer audit                  | §1 (entire phase)                                                                                                |
| FR15  | Adaptive diagnostic rendering              | §10.2 (renderer + tie-breaking), §10.2a (migration suggestions)                                                    |
| FR16  | Auto-import (LSP first-class)              | §10.3 (quick-fix, name-index, binding-shape preference, name-collision ordering)                                  |

**Non-functional / cross-section coverage:**

- **Ergonomics, soundness of activation, zero runtime cost,
  filesystem-resident stdlib data** — Cross-cutting "Non-functional requirements".
- **`--explain-type` diagnostic** — §10.4.
- **Source-map and diagnostic provenance** — §10.5.
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
