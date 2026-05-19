# Builtins: Ambient Set and Pseudo-Package Imports

## Provenance

Extracted from
[../interop_mutability/dts_to_esc_proposal.md](../interop_mutability/dts_to_esc_proposal.md).
That proposal bundles two distinct workstreams: (1) building `.esc`
files for builtins (the ambient set plus `std:*` / `dom:*`
pseudo-packages) and (2) lazy on-first-compile conversion of
third-party npm dependencies. This document covers only the first
workstream. The third-party workstream is explicitly out of scope here
and will be tracked separately.

## Goals

- Replace the current model (Escalier consumes TypeScript's
  `lib.es*.d.ts` at startup and patches it via overrides) with one in
  which Escalier *owns* the source of truth for the JavaScript and
  Web platform surface as first-class `.esc` files.
- Draw a tighter line around what is truly ambient than TypeScript
  does. Stop type-checking the full DOM surface in every browser
  program and the full DOM surface in every Node program; put
  non-ambient APIs behind explicit imports.
- Provide a uniform import model (`import "std:math"`,
  `import "dom:canvas"`) for accessing standard-library and Web
  platform APIs, with per-import flags for choosing the binding
  shape.
- Make `throws`, lifetimes, mutability, and any other Escalier-specific
  annotations editable inline in the source `.esc` files, with no
  override / merge layer.

## Non-goals (out of scope for this workstream)

- Lazy `.d.ts` → `.esc` conversion for third-party npm packages at
  compile time.
- `node_modules/.cache/escalier/` cache and its invalidation rules.
- Per-dep overrides (tier 1a) and user-project overrides (tier 1b).
- `escalier cache clean` CLI surface.
- Steps 10 and 11 of the original migration path (third-party lazy
  cache; deletion of the runtime interop pipeline). These are
  deferred to the third-party workstream.
- Populating `node:*` packages. The scheme is reserved; content is
  deferred until Node support lands.
- Polyfill insertion at lowering. The always-current API requirement
  assumes polyfilling is tractable (see §"Always-current API"); the
  implementation is a separate effort.

## Scope of this workstream

In scope:

- A small ambient set in `internal/interop/data/builtins.esc`.
- A pseudo-package model with `std:*` and `dom:*` (and reserved
  `node:*`) URI-scheme imports under `internal/interop/data/std/`
  and `internal/interop/data/dom/`.
- Per-import binding-shape flags (`?local`, `?nested`, `?flat`) and
  the rules governing their combination with named imports.
- The "no promoted classes" rule for namespace bindings.
- Cross-package augmentation via open registry interfaces
  (`HTMLElementTagNameMap`, `HTMLElementEventMap`,
  `SVGElementTagNameMap`, etc.), with activation scoped to the
  importing file.
- Inter-package imports between pseudo-packages.
- A one-time bootstrap converter `tools/dts_to_esc/` that
  AST-to-AST translates the pinned TypeScript `.d.ts` set into the
  initial `.esc` files. Output is hand-edited and committed; ongoing
  maintenance is by hand-editing source. The converter persists as a
  review tool (`--check`) for TypeScript version bumps, never as a
  build step.
- Prelude changes: stop walking `lib.*.d.ts`; load the embedded
  `builtins.esc`.
- Always-current API at type-check time; runtime version
  compatibility is the codegen's job (polyfill insertion at
  lowering).
- Intrinsic types (`Awaited`, `Uppercase`, `Lowercase`, `Capitalize`,
  `Uncapitalize`, `NoInfer`) implemented as checker-resident
  handlers, not source-visible in `.esc`.
- A declaration-printer audit (FR13 gate) covering declaration-level
  forms (`declare class`, `declare fn`, `declare namespace`, type
  aliases, generic constraints, mapped types with `as` rename,
  conditional types with `infer`, ambient module syntax) prior to
  committing to the converter approach.

## Functional requirements

### FR1. Ambient builtins set

The ambient set lives in one file, `internal/interop/data/builtins.esc`,
embedded into the compiler binary. Approximately 35–40 declarations.
Membership criteria — a declaration is ambient if it satisfies at
least one of:

- The language itself produces values of this type (literal syntax,
  operators, control-flow desugaring).
- Primitive method dispatch references it (`"x".charAt(0)` needs
  `String`'s instance interface).
- It is a spec-mandated free global function (`parseInt`, etc.).
- It is a purely type-level utility used pervasively.

Concrete contents:

- **Primitive instance interfaces.** `String`, `Number`, `Boolean`,
  `BigInt` — instance methods only. The constructor / static side of
  each lives in the corresponding pseudo-package (`std:string`,
  `std:number`, …).
- **Symbol (full).** Both the instance interface and the constructor
  with all well-known symbols (`Symbol.iterator`,
  `Symbol.asyncIterator`, `Symbol.toPrimitive`, …) are ambient. The
  ambient iterator protocol references these by symbol, so they
  cannot be deferred to a pseudo-package without a bootstrap cycle.
  There is no `std:symbol` package.
- **Syntax-produced classes.** `Array<T>` (array literals),
  `Promise<T>` (async functions), `RegExp` (regex literals). Both
  interface and constructor stay ambient. There is no `std:regexp`
  package; `RegExp` lives entirely in `builtins.esc`.
- **Iterator protocol.** `Iterator<T>`, `Iterable<T>`,
  `IterableIterator<T>` (defined by
  `node_modules/typescript/lib/lib.es2015.iterable.d.ts` and
  carried over here), `IteratorResult<T>`, `AsyncIterator<T>`,
  `AsyncIterable<T>`, `Generator<T, TReturn, TNext>`,
  `AsyncGenerator<T, TReturn, TNext>`.
- **Prototype roots (full).** `Object` and `Function`, including
  their static sides (`Object.keys`, `Object.assign`,
  `Object.entries`, `Object.freeze`, `Function.prototype.bind`, …).
  Keeping these ambient avoids forcing `import "std:object"` for
  routine code.
- **Error family.** `Error`, `TypeError`, `RangeError`,
  `SyntaxError`, `ReferenceError`, `EvalError`, `URIError`,
  `AggregateError` — referenced by `throw` / `catch` / `throws`.
- **Free globals.** `globalThis`, `parseInt`, `parseFloat`, `isNaN`,
  `isFinite`, `encodeURI`, `decodeURI`, `encodeURIComponent`,
  `decodeURIComponent`, `eval`.
- **Utility types.** `Partial`, `Required`, `Readonly`, `Pick`,
  `Omit`, `Record`, `Exclude`, `Extract`, `NonNullable`,
  `Parameters`, `ConstructorParameters`, `ReturnType`,
  `InstanceType`, `ThisParameterType`, `OmitThisParameter`,
  `ThisType`.

Note that the utility-types list above does **not** include
`Awaited`; see the "Intrinsic-backed utilities" paragraph below.

Intrinsic-backed utilities (`Uppercase`, `Lowercase`, `Capitalize`,
`Uncapitalize`, `NoInfer`, `Awaited`) are checker-resident and do
not appear in `builtins.esc` or any other source file.

### FR2. Pseudo-package layout

Everything not in the ambient set lives in a pseudo-package under
`internal/interop/data/std/` or `internal/interop/data/dom/`. The
file layout is:

```
internal/interop/data/
    builtins.esc

    std/
        math.esc, json.esc, console.esc,
        string.esc, number.esc, boolean.esc, bigint.esc,
        date.esc, map.esc, set.esc, weak-ref.esc,
        typed-arrays.esc, reflect.esc, proxy.esc,
        intl.esc, temporal.esc, wasm.esc

    dom/
        core.esc, http.esc, canvas.esc, webgl.esc, webrtc.esc,
        storage.esc, workers.esc, media.esc, forms.esc,
        # additional dom:* packages as needed
```

DOM packages split more finely than `std/` because the surface is
larger; a typical browser program touches 2–3 `dom:*` packages.

`node:*` content is reserved and deferred — when Node support lands,
populating `internal/interop/data/node/` follows the same model.

### FR3. URI-scheme import grammar

The module-specifier grammar accepts a new shape:

```escalier
import "std:math"
import "std:json"
import "dom:canvas"
import "dom:http"
```

We need to **add** support for resolving modules with the `std:`,
`dom:`, and (reserved) `node:` schemes; nothing in the existing
resolver recognizes them today. The current module-resolution code
path is `resolveImport` / `resolveExportModulePath` in
`internal/checker/infer_import.go` (which walks
`node_modules/<pkg>` and `node_modules/@types/<pkg>` for `.d.ts`
discovery). The scheme-resolution logic must be added there (or
factored out of there as appropriate) and route scheme-prefixed
specifiers to the embedded `internal/interop/data/` tree. Mapping
is mechanical: `std:math` → `internal/interop/data/std/math.esc`,
`dom:http` → `internal/interop/data/dom/http.esc`. The `?flag`
portion of the URI (see FR4) is stripped before path resolution.

**Grammar updates.** The parser must be extended to accept two
forms it does not currently support:

1. **Bare-string imports** with no binding clause:
   `import "std:math"`. Today the parser requires a named-bindings
   clause (`import { x } from "..."`) or a namespace alias. The new
   form binds the package per the binding-shape flag in effect (see
   FR4); no `from` keyword is involved.
2. **`?flag` and `?flag1&flag2` suffixes** on the module-specifier
   string literal must be parsed (and preserved through to the
   resolver, which strips them before path resolution and applies
   them per FR4).

Both forms are part of this workstream's parser change; see also
FR4 and FR5 for the rules that consume them.

Imports are **type-system-only, runtime-erased.** At runtime
`Math.sin`, `console.log`, etc. are globally available in every JS
environment; the import statement adds type information to the
compile-time scope and codegen erases the import line. Zero runtime
cost. The "package" is a type-checking grouping mechanism only.

### FR4. Binding-shape flags

The URI may carry one or more `?flag` modifiers separated by `&`
(URL-query convention). Three flags govern the **binding shape**;
they are mutually exclusive and exactly one is in effect per import
(absent flag is treated as `?local`):

- **`?local` (default).** Bind the package's contents under a local
  identifier equal to the lowercased last URI segment. Each import
  is its own binding; no cross-import merging.
  - `import "std:math"` → `math.sin(x)`, `math.PI`
  - `import "dom:canvas"` → `canvas.HTMLCanvasElement`
- **`?nested`.** Bind under a scheme-named namespace with the
  package as a sub-namespace. Multiple `?nested` imports from the
  same scheme merge under disjoint sub-namespaces (one per package
  URI). No collision risk.
  - `import "std:math?nested"` → `std.math.sin(x)`
  - `import "std:json?nested"` adds `std.json` alongside.
- **`?flat`.** Merge the package's contents directly into a shared
  scheme-named namespace. Multiple `?flat` imports from the same
  scheme merge; package names are dropped from the access path.
  - `import "dom:canvas?flat"` + `import "dom:webgl?flat"` →
    `dom.HTMLCanvasElement`, `dom.WebGLRenderingContext`.
  - Collision risk is real (two packages exporting the same name
    under one scheme conflict). Opt-in for that reason. **When
    `?flat` produces a name collision, the compiler emits a
    warning** (not a hard error): the colliding name still binds
    under deterministic last-import-wins (or equivalent) rules, but
    the diagnostic surfaces the conflict so the user can resolve it
    by switching one of the imports off `?flat`.

**Combining any two of `?local`, `?nested`, `?flat` in one URI is a
compile error**; the resolver reports which flag pair is invalid.

**Identifier hygiene.** Package names may contain hyphens; the
resolver substitutes `-` → `_` when computing the binding name
(`dom:web-rtc` → `web_rtc`, `dom:web-rtc?nested` → `dom.web_rtc`).
`?flat` drops the package name, so this does not appear at the
user-visible binding level — but `?flat` still requires an internal
per-package bookkeeping handle to deduplicate registry augmentations
across multiple `?flat` imports of the same package.

The flag slot is extensible: future flags (e.g. `?type-only`,
`?lazy`) compose with the binding-shape flags subject to their own
per-flag compatibility rules.

### FR5. Named imports

Named imports are supported as a syntactic convenience and bind
named symbols directly into the local scope, bypassing namespace
wrapping:

```escalier
import { sin, PI } from "std:math"
import { fetch } from "dom:http"
```

**Named imports may not carry binding-shape flags.** Writing
`import { fetch } from "dom:http?flat"` (or `?nested`, or `?local`)
is a compile error: binding-shape flags govern how a package's
*namespace* lands in the importing scope, and named imports bypass
the namespace entirely. A file that needs both named bindings and a
shared-namespace contribution must use two separate `import`
statements.

### FR6. No promoted-class shortcut

A package containing a single dominant class does not re-export that
class's members at the namespace root. Examples:

- `import "std:date"` binds `date`. `date.now()` is **not** valid;
  the static is `date.Date.now()` and construction is
  `new date.Date()`.

Every imported namespace binding is a plain namespace whose fields
are exactly the package's top-level declarations. This keeps the
namespace model uniform.

### FR7. Inter-package imports

Pseudo-packages that reference types declared in other
pseudo-packages must `import` them explicitly, exactly like ordinary
user code. Examples:

- `dom/canvas.esc` needs `import "dom:core"` (in some binding shape)
  to extend `HTMLElement`.
- `std/intl.esc` needs `import "std:date"` to reference `Date`.

There is no implicit "all sibling packages visible" rule. Ambient
declarations from `builtins.esc` (iterators, primitives, Symbol,
Promise, Array, the Error family, well-known symbols) are visible
everywhere without import.

### FR8. Cross-package augmentation

Splitting the DOM into feature packages requires that `dom:core`
APIs return or reference types that live in feature packages, e.g.
`document.createElement("canvas")` should return
`HTMLCanvasElement`.

**Primary mechanism: open registry interfaces, augmented per
package.** `dom:core` declares the registry (`HTMLElementTagNameMap`,
`HTMLElementEventMap`, `SVGElementTagNameMap`, etc.) with a minimal
baseline or empty body. Core APIs are written once against the
registry:

```escalier
fn createElement<K: keyof HTMLElementTagNameMap>(
    self, tag: K
) -> HTMLElementTagNameMap[K]
```

Each feature package augments only the registry by declaring its
own contribution to the open interface in its `.esc` source, e.g.
`dom:canvas`:

```escalier
declare interface HTMLElementTagNameMap {
    canvas: HTMLCanvasElement,
}
```

Builtins are declared directly in their own `.esc` files; the
`override declare` syntax is **not** used here — that syntax is
reserved for the third-party workstream's override mechanism and
does not apply to builtins or pseudo-packages.

Every registry-keyed API (`createElement`, `querySelector`,
`getElementsByTagName`, `createElementNS`, …) picks up the new entry
automatically.

**Fallback: per-API augmentation** for the rare cross-package APIs
that do not fit a registry pattern. Two cases:

1. The API takes the type as a *parameter* (not a return type): the
   feature package exports the type; `dom:core` references it via
   the type's qualified name; no augmentation needed.
2. The API is overload-keyed on a string but lacks a registry: the
   feature package augments the API's overload set directly. Rare;
   treat as one-off when it arises.

**Activation semantics.** Augmentation activation is **scoped to the
importing module**, not the whole compilation unit. A file that
imports `dom:canvas` sees the augmented `HTMLElementTagNameMap`
entry for `canvas`; a sibling file in the same program that does not
import `dom:canvas` does not. This deliberately departs from
TypeScript's `declare module` behavior, where any module-augmenting
import becomes global. The type a file sees is fully determined by
that file's own imports.

Augmentation activation is **independent of the binding-shape flag**.
Importing `dom:canvas` under any of `?local`, `?nested`, or `?flat`
adds canvas's contributions to that file's view of
`HTMLElementTagNameMap`. The flag only affects how canvas's *direct
exports* land in the importing scope.

**Language requirements this depends on:**

- Open interfaces / cross-package interface merging. The §5
  override-merge code already does interface merging for type
  refinement; whether the same mechanism can be repurposed for
  cross-package augmentation, or whether augmentation needs its own
  loader path, must be confirmed during the resolver-extension step.
- Indexed access over open registries: `HTMLElementTagNameMap[K]`
  where `K extends keyof HTMLElementTagNameMap` must refresh
  correctly when the registry is augmented.

### FR9. Bootstrap converter (`tools/dts_to_esc/`)

A one-time seeding tool. A Go binary that:

1. Reads the pinned TypeScript lib `.d.ts` set via the existing
   `internal/dts_parser/`.
2. Converts each `.d.ts` declaration **directly into the
   corresponding Escalier declaration AST**, bypassing
   `type_system.Type` and the checker entirely. The TS-side AST node
   for `declare class Foo`, `interface Bar`, `declare function baz`,
   `type alias`, `declare namespace`, etc. maps mechanically to the
   matching Escalier declaration. This is a pure AST-to-AST
   translator — no type resolution involved, so there is no
   prelude-needs-checker-needs-prelude bootstrap cycle.
3. Runs `interop.Classify` (tiers 3/5/6) at conversion time to seed
   receiver mutability into the emitted AST (`self` vs `mut self` on
   each method).
4. Partitions the resulting declarations into:
   - the ambient set (per FR1) → `builtins.esc`;
   - each pseudo-package's surface → that package's `.esc` file
     under `std/` or `dom/`.
   The partition is driven by a hand-maintained mapping table in
   the converter, not by anything in the `.d.ts`.
5. Detects which TS-lib symbols belong to registries
   (`HTMLElementTagNameMap`, `HTMLElementEventMap`, …) and routes
   their per-feature entries into the appropriate `dom:*` package's
   augmentation block.
6. Renders each output file via the (now-validated) declaration
   printer and `internal/type_system/print_type.go`.
7. Supports a `--check` mode that diffs generated output against
   committed files without overwriting.

**One-time seeding.** After the initial bootstrap commit, the
`.esc` files (ambient and pseudo-package alike) are first-class
Escalier source. Hand-edits add `throws`, lifetimes, JSDoc, and any
structural refinements — written inline like `self` and `mut self`,
no merge layer. Re-running the converter is a **review tool**
(`--check`), never a build step; its output is never auto-applied.

**TS version bump workflow.** Run
`tools/dts_to_esc/regenerate --check` against the bumped TS. The
output is a diff against the current files showing what TS added /
removed / changed. A contributor decides which changes to port by
hand. An optional CI nudge can annotate a PR with "TS lib changed
since last bump" when the diff exceeds some threshold.

### FR10. Prelude changes

`Prelude` in `internal/checker/prelude.go` stops walking
`node_modules/typescript/lib/*.d.ts` for global symbols. Instead it
parses the embedded `internal/interop/data/builtins.esc` through
`parser.ParseDecls` + the standard checker pipeline. Because
`builtins.esc` contains only declarations (no value-level
expressions needing their own prelude), loading it via the checker
does not reintroduce a bootstrap cycle.

**The prelude does not pre-load any pseudo-package files** — those
are loaded on demand when a program imports them.

The following become dead code (for the builtins workstream;
removal of the runtime interop pipeline as a whole is the
third-party workstream's concern):

- `loadGlobalDefinitions`
- `populateSelfParams`
- `UpdateMethodMutability`
- `mergeReadonlyVariant`
- the `mutabilityOverrides` Go map

Escalier-specific extras currently injected via prelude code (e.g.
`SymbolConstructor.customMatcher` at
`internal/checker/prelude.go:804–836`) move into `builtins.esc` as
ordinary source, since `SymbolConstructor` itself is now ambient.
`Symbol.customMatcher` is a TC39 proposal that has not been
accepted into the spec, so it does not appear in any
`lib.*.d.ts`; the bootstrap converter will therefore not emit it,
and we will **hand-author** the declaration directly in
`builtins.esc`.

The **builtin tier** of the override store (tier 4 in
`requirements.md`) goes away entirely. There are **no override
`.esc` fragments for builtins** — no `internal/interop/data/builtins/`
overrides subtree, no per-builtin merge layer. After the one-time
bootstrap conversion of `lib.*.d.ts`, the generated `.esc` files
are **manually edited** to add receiver mutability, lifetimes,
thrown-exception annotations, JSDoc, and any other Escalier-specific
refinements, and then **maintained as source going forward**
(re-running the converter is only a `--check` review tool; its
output is never auto-applied).

### FR11. Always-current API; polyfills at lowering

The type checker always sees the modern surface. ES2024 methods
like `Promise.withResolvers` are present at type-check time even
when the codegen target is older. Runtime-version compatibility is
the codegen's job: when lowering to an older target, the emitter
inserts a polyfill for any newer feature actually used. ES-version
awareness does not enter the type system.

Polyfill insertion is its own follow-up effort outside this
workstream. This document assumes only that it is tractable.

### FR12. Intrinsic types are checker-resident

`Uppercase`, `Lowercase`, `Capitalize`, `Uncapitalize`, `NoInfer`,
`Awaited` are implemented directly in the Escalier type checker as
named handlers — the four string-case utilities as pure
`Type → Type` resolvers, `NoInfer` as an inference-machinery hook
(see escalier-lang/escalier#631), and `Awaited` as a built-in
reduction (potentially expressible as a recursive conditional type
per escalier-lang/escalier#630, but likely retained as an intrinsic
for performance).

The `intrinsic` keyword therefore never appears in `.esc` files.
The bootstrap converter strips or substitutes any `intrinsic`-typed
declaration found in source `.d.ts`. The Escalier parser does
*not* need to accept `intrinsic`.

### FR13. Declaration printer audit

The bootstrap converter must emit declaration-level forms (`declare
class`, `declare fn`, `declare namespace`, `declare type alias`,
`declare var`, generic parameter constraints with `extends`,
ambient module syntax, conditional types referring to `infer T`,
mapped types with `as` rename clauses, …). An FR13 audit pass —
parallel in shape to the prior §8 audit of `type_system.Type` — must
confirm these declaration forms round-trip through the parser before
committing to the converter approach. This is the **riskiest gate**;
do it first.

## Non-functional requirements

- **Ergonomics.** Default `?local` binding gives terse access
  (`math.sin(x)`, `console.log(...)`). The binding choice per
  import is the file author's; no global configuration.
- **Soundness of activation.** A file's view of cross-package
  augmentations is fully determined by that file's own imports — no
  spooky action from a sibling module.
- **Zero runtime cost.** Pseudo-package imports erase entirely at
  codegen.
- **Embedded data.** All `.esc` files under `internal/interop/data/`
  are embedded into the compiler binary so the toolchain has no
  external file dependencies for ambient or pseudo-package
  declarations.
- **Editor support (deferred).** Auto-import suggestions and an
  `--explain-type` diagnostic that, when a tag-keyed return is
  wider than expected, suggests likely `dom:*` imports.

## Migration phases

The phasing here is the subset of the original proposal's migration
path that pertains to the builtins workstream. Steps 10 (third-party
lazy cache) and 11 (deletion of the runtime interop pipeline) are
omitted; they belong to the third-party workstream.

1. **FR13 Declaration printer audit.** Same shape as the prior §8
   but for declaration-level forms. Establishes that the converter
   can emit `.esc` for the constructs `lib.*.d.ts` actually uses.
   Riskiest gate; do this first.
2. **URI-scheme import support.** Extend the parser and module
   resolver to accept `import "scheme:name"` and route `std:`,
   `dom:` (and reserved `node:`) prefixes to the embedded
   `internal/interop/data/` tree. Default `?local`
   namespace-binding semantics. Parse the `?flag` /
   `?flag1&flag2` suffix on the URI; strip it before path
   resolution; apply per-flag binding rules at scope-insertion
   time. Initial flag set covers all three binding shapes: `?local`
   (no-op default), `?nested`, `?flat`. Combining any two of these
   three reports a clear error.
   - **Gate:** a placeholder `std:math` package with a stub
     `math.PI = 3.14` imports and resolves end-to-end; a
     three-fixture test confirms each flag binds correctly
     (`?local` → `math.PI`, `?nested` → `std.math.PI`, `?flat` →
     `std.PI`); a two-package fixture
     (`dom:a?flat` + `dom:b?flat`) merges into one `dom` binding;
     mutually-exclusive flag combos error.
3. **Cross-package augmentation support.** Confirm that the
   existing §5 interface-merging mechanism (or a small extension of
   it) lets a package augment an open interface declared in another
   package, with augmentation scoped to files that import the
   augmenting package. Test with a hand-authored two-file pair:
   `dom/core.esc` declares an empty `HTMLElementTagNameMap`;
   `dom/canvas.esc` augments it with `canvas: HTMLCanvasElement`;
   importing `dom:canvas` under any binding-shape flag (`?local`,
   `?nested`, `?flat`) makes `createElement("canvas")` narrow
   correctly in the importing file, and a sibling file *without*
   the import sees only the pre-augmentation shape.
   - **Gate:** that test passes; indexed-access over the augmented
     registry resolves to the right element type;
     registry-augmentation activation is shown to be orthogonal to
     the binding-shape flag.
4. **Converter MVP.** Build `tools/dts_to_esc/` against a tiny
   slice (`Boolean` alone, ~10 lines of `.d.ts`). Emit to stdout.
   No file layout, no partition logic.
   - **Gate:** output round-trips through the parser and reads
     naturally to a human.
5. **Converter productionization.** Add the ambient-vs-package
   partition table, full output paths under
   `internal/interop/data/`, `--check` mode, and the full
   `lib.*.d.ts` set as input. The converter must also detect which
   TS-lib symbols belong to registries and route their per-feature
   entries into the appropriate `dom:*` package's augmentation
   block.
   - **Gate:** every output file parses; running the converter
     twice produces byte-identical output.
6. **Builtin bootstrap.** Run the converter, review the output,
   hand-edit obvious mis-classifications and high-value `throws`
   annotations across `builtins.esc` and the `std/` / `dom/`
   packages, commit.
   - **Gate:** humans review the committed files.
7. **Prelude switchover.** Replace the lib-walking path in
   `internal/checker/prelude.go` with a load of `builtins.esc`
   only. Old path stays behind a build flag for one release; run
   both side-by-side in CI; assert the resulting global namespace
   is equivalent for *ambient* surface (pseudo-package types are
   no longer ambient and that is intentional).
8. **Existing code migration.** Update Escalier's own fixtures and
   tests that relied on previously-ambient symbols (`Math`, `JSON`,
   `console`, `Map`, …) to import the corresponding pseudo-package.
   This is the user-visible breaking change.
9. **Delete builtin overrides infrastructure.** Any builtin-tier
   override store / `data/builtins/overrides/` subtree contemplated
   by the current §6 plan goes away. `BuildBuiltinStore` returns an
   empty store; eventually the function deletes. Builtin and
   pseudo-package refinements live inline in the `.esc` files and
   are maintained as source (no merge layer). **Depends on steps 7
   and 8** — the prelude must have switched to `builtins.esc`
   (step 7), and fixtures relying on previously-ambient symbols
   must have migrated to `import "std:*"` (step 8), otherwise
   removing the override path regresses them.
10. **Flip the default.** Remove the build flag from step 7.

(Steps 10 and 11 from the original proposal's migration path —
third-party lazy cache and deletion of the runtime interop
pipeline — are intentionally omitted; they belong to the
third-party workstream. The steps above have been renumbered
sequentially within this workstream; the original step numbering
no longer applies.)

The original "~1 week for steps 1–5" estimate has been removed as
implausible; see Open Questions for a note on re-baselining it.
Steps 6–8 are authoring + migration paced by human review of
generated files. Steps 9 and 10 add a small amount of cleanup
work.

## Risks

- **Declaration-form printer fidelity** (see FR13). If
  `lib.es*.d.ts` contains shapes that do not round-trip, the whole
  approach is gated on extending the parser. Hard to estimate
  without doing the audit.
- **Ergonomic cost of imports.** Today `console.log` Just Works
  ambient. Under this model the program needs `import "std:console"`
  first. Defensible (Rust, Go, Python all require imports) but is
  friction relative to JS/TS. Mitigations: editor auto-import
  suggestions; descriptive error messages on unbound globals ("did
  you mean to `import \"std:console\"`?").
- **Initial bootstrap quality.** The committed files start from
  heuristic output. Bad classifications that slip past review ship
  to users. Mitigation: the files are editable, so corrections are
  PRs, not version bumps.
- **Partition table churn.** "Which symbol lives in which
  pseudo-package" is a maintained mapping. When TS adds a symbol,
  the partition table needs an entry — manual decision per
  addition. Not large in practice (TS lib additions are rare in
  minor versions) but not zero.
- **Cross-package augmentation mechanism.** The registry pattern
  depends on a package being able to augment an open interface
  declared in another package. Escalier's §5 interface-merge code
  is *probably* reusable, but it was designed for type-refinement at
  a fixed merge point, not for arbitrary cross-package activation.
  If the existing mechanism does not fit, augmentation needs its own
  loader path — adds work to migration step 3.
- **Non-local reasoning from augmentation activation.** A function
  using `document.createElement("canvas")` gets `HTMLCanvasElement`
  only if that file imported `dom:canvas`. Type-only (runtime
  behavior unchanged) but users encountering an unexpectedly-coarse
  type will need to learn to look for missing imports. Mitigation:
  an `--explain-type` diagnostic that suggests likely `dom:*`
  imports.
- **Hand-edits drift from upstream TS.** If TS adds a method to
  `Array.prototype` in a minor version bump, someone has to notice
  (via `--check`) and port it by hand. Mostly a feature — gives us
  intent over upstream churn — but Escalier's view of the JS stdlib
  can lag.
- **Polyfill story is a separate effort.** Always-modern builtins
  only work if codegen can lower modern features to older targets.
  Babel and swc demonstrate it is tractable, but it is its own work
  item that this workstream depends on.
- **Cross-file references within `builtins.esc`.** Even one file is
  one module — but `Promise<T>` references `Iterator`, `Array<T>`
  references the iteration protocol, etc. The existing module
  loader handles this for user code; just need to confirm it works
  for the ambient set. Probably not an issue.

## Open questions

- **Declaration printer completeness (FR13).** The bootstrap
  converter has to emit declaration-level forms (`declare class`,
  `declare fn`, `declare namespace`, `declare type alias`,
  `declare var`, generic parameter constraints with `extends`,
  ambient module syntax, conditional types referring to `infer T`,
  mapped types with `as` rename clauses). The AST printer in
  `internal/printer/printer.go` already handles these for `.esc`
  source — but does it handle `declare` prefixes, ambient module
  syntax, and the exotic shapes that show up in `lib.es*.d.ts`? A
  FR13 audit pass must establish this before committing to the
  approach. Estimated work: similar to §8 — a day to enumerate,
  plus fixes. This is the riskiest gate; the rest is straightforward
  implementation work.
- **Augmentation loader path.** Whether the §5 override-merge
  interface-merging code can be reused for cross-package
  augmentation, or whether augmentation needs its own loader path,
  is to be confirmed in migration step 3.
- **`?flat` per-package bookkeeping handle.** `?flat` drops the
  package name from the user-visible binding but still needs some
  internal per-package handle to deduplicate registry augmentations
  across multiple `?flat` imports of the same package. The form of
  that handle will become clearer once augmentation activation is
  implemented in migration step 3.
- **Tier-numbering caveat.** The references to "tier 4 (builtin)"
  of the override store follow the
  `../interop_mutability/requirements.md` numbering at the time of
  writing. If that tier scheme changes, update the cross-references
  here in lockstep.
- **Augmentation scope across transitive imports.** Activation is
  scoped to the importing module (FR8). Open: do augmentations
  propagate transitively through imports? I.e. if file A imports
  `dom:canvas` and re-exports something, and file B imports A, does
  B see the augmented `HTMLElementTagNameMap`? The current FR8 text
  reads as "no" (purely the importing file's own imports count),
  but the consequences for library authors who wrap `dom:*` need
  explicit confirmation, and the rule for re-exports specifically
  needs to be pinned down.
- **`std:weak-ref.esc` naming.** Package names map hyphens to
  underscores at the binding level (FR4: `dom:web-rtc` → `web_rtc`).
  Open: do we keep the hyphenated *file name* (`std/weak-ref.esc`)
  and let the resolver translate to the underscore binding, or do
  we rename the file (`std/weak_ref.esc`) so file name and binding
  name match? Cosmetic but worth picking a convention before
  populating `std/`.
- **`globalThis` is not source-declared.** Its type is computed
  over the entire global scope; it cannot simply be a line in
  `builtins.esc` of the form `declare var globalThis: ...`. How
  the checker computes / exposes its type, and how (or whether) any
  stub lands in `builtins.esc` to let the parser accept references
  to it, is an open question.
- **Re-baseline the effort estimate.** The previous "~1 week for
  steps 1–5" estimate has been removed as implausible (it
  underweighted the resolver extension and the FR13 audit). A new
  estimate has not yet been produced; it should be reset once the
  FR13 audit has been scoped.

## Error-message taxonomy

This workstream introduces several new failure modes at the
resolver / parser boundary. Each gets a distinct, actionable
diagnostic:

- **Unknown scheme.** `import "foo:bar"` where `foo` is not one
  of the recognized schemes (`std`, `dom`, reserved `node`).
  Message names the scheme and lists the recognized set.
- **Unknown package within a known scheme.** `import "std:nope"`
  where the scheme is valid but no package file exists. Message
  names the scheme, the requested package, and (if cheap) suggests
  near-spelling matches from the embedded package list.
- **Invalid flag combination.** Two of `?local`, `?nested`,
  `?flat` on the same URI. Message names the specific pair and
  explains they are mutually exclusive binding shapes.
- **Unknown flag.** `?something` that is not a recognized flag.
  Message names the flag and lists the currently-recognized set.
- **Named import carrying a binding-shape flag.** `import { x }
  from "std:math?nested"` (FR5). Message explains that
  binding-shape flags govern namespace placement and named imports
  bypass namespaces, so the flag has no meaning here.
- **`?flat` name collision.** Two `?flat` imports under the same
  scheme contributing the same identifier. **Warning, not error**
  (per FR4): message names the colliding identifier, the two
  source packages, and which import won under the
  collision-resolution rule.

Each diagnostic ties back to a span on the offending `import`
statement, ideally pointing at the URI string literal (and within
it, the flag portion when the failure is flag-shaped).

## Source-map and diagnostic provenance for ambient declarations

Ambient declarations now live in real `.esc` source under
`internal/interop/data/`. Diagnostics that reference these
declarations (e.g. "expected `string`, got `number`" pointing at a
parameter of `String.prototype.charAt`) need a source location
that is meaningful to the user.

Requirements:

- Spans on declarations parsed from embedded `.esc` files must
  resolve to a stable, user-visible path. The path should make it
  obvious the declaration came from the embedded ambient set
  (e.g. `<builtins>/std/string.esc` or
  `escalier://internal/interop/data/std/string.esc`), not a
  filesystem path inside the user's machine.
- Line/column information from the parser must be preserved so
  diagnostics point at the right line of the embedded file.
- When the user clicks through (LSP "go to definition") on an
  ambient symbol, the editor must be able to display the embedded
  file's content. This implies either materializing embedded files
  on demand at a virtual path or having the LSP serve them
  directly.

## Testing strategy

- **Parser tests.** Bare-string imports (`import "std:math"`),
  `?flag` and `?flag1&flag2` suffix parsing, named imports with
  flags (rejected at parse or check, but parsed). Round-trip via
  the printer.
- **Resolver tests.** End-to-end resolution of `std:`, `dom:`,
  `node:` (reserved → clear error) schemes; unknown scheme; known
  scheme + unknown package; `?flag` stripping before path lookup.
- **Binding-shape tests** (per FR4). Fixture per shape (`?local`,
  `?nested`, `?flat`), per single- and multi-package case,
  including `?flat` collision warning.
- **Augmentation tests** (per FR8). Two-file fixture where one
  file imports the augmenting package and the other does not;
  assert the augmented-vs-base type at each site. Test under each
  of `?local`, `?nested`, `?flat`.
- **Prelude switchover parity.** While the old `lib.*.d.ts`-walking
  path remains behind a build flag (migration step 7), CI runs
  both side-by-side and asserts the resulting *ambient* global
  namespace is equivalent (pseudo-package surface is intentionally
  divergent and is excluded from the parity check).
- **Snapshot tests via `go-snaps`.** Generated `.esc` output from
  the bootstrap converter has snapshots; `tools/dts_to_esc/
  --check` re-runs the converter and diffs against the committed
  files in CI to catch upstream TS changes.
- **Fixture tests** under `fixtures/` exercise each new failure
  mode listed in the error-message taxonomy with full
  message-text assertions (per CLAUDE.md test conventions).

## Backwards-compatibility and deprecation policy

The switch from "TS `lib.*.d.ts` walked at startup" to
"`builtins.esc` plus explicit `import "std:*"`" is a **user-visible
breaking change** for any Escalier program that touched a
previously-ambient symbol (`Math`, `JSON`, `console`, `Map`,
`Date`, etc.). Migration policy:

- **Deprecation flag.** Migration step 7 introduces a build flag
  that keeps the old `lib.*.d.ts`-walking path live alongside the
  new `builtins.esc` path for **one release cycle**. Users opt
  into the new behavior; the next release flips the default
  (migration step 10); the release after that removes the flag.
- **Diagnostic-assisted migration.** When a symbol that used to
  be ambient is referenced without an import under the new
  default, the unbound-name diagnostic includes a
  suggestion ("did you mean to `import \"std:console\"`?") whenever
  the unbound name matches a known pseudo-package export. The
  suggestion list is derived from the embedded
  `internal/interop/data/` tree, so it stays in sync mechanically.
- **No automatic codemod for user code in this workstream.** A
  separate codemod that rewrites previously-ambient references to
  add the corresponding `import` statement is desirable but
  out-of-scope here; the diagnostic above is the supported
  migration aid.
- **Internal fixtures and tests** migrate as part of migration
  step 8 in the same release that introduces the new default,
  serving as the canary for the user-facing change.
