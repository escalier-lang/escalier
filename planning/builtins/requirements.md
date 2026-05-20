# Builtins: Ambient Set and Pseudo-Package Imports

## Provenance

Extracted from
[../interop_mutability/dts_to_esc_proposal.md](../interop_mutability/dts_to_esc_proposal.md).
That proposal bundles two distinct workstreams: (1) building `.esc`
files for the JavaScript and Web platform surface as `std:*` and
`dom:*` pseudo-packages, and (2) lazy on-first-compile conversion
of third-party npm dependencies. This document covers only the
first workstream. The third-party workstream is explicitly out of
scope here and will be tracked separately.

## Goals

- Replace the current model (Escalier consumes TypeScript's
  `lib.es*.d.ts` at startup and patches it via overrides) with one in
  which Escalier *owns* the source of truth for the JavaScript and
  Web platform surface as first-class `.esc` files.
- Eliminate the implicit-globals model entirely. TypeScript exposes
  the whole `lib.dom` surface ambient in every browser program and
  the whole `lib.es*` surface ambient everywhere; Escalier instead
  puts every name behind an explicit `import "std:*"` /
  `import "dom:*"`, while still letting the checker reason about
  primitive method dispatch, iteration, and `await` from
  language-level shape knowledge.
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
- **Named imports** from pseudo-packages
  (`import { x } from "std:..."`). All pseudo-package imports go
  through the namespace forms in FR4. Named imports can be added
  in a follow-up workstream if a concrete need surfaces.
  Qualified-only is the Go convention: reading `math.sin(x)` makes
  the origin of `sin` visible at every call site, which improves
  readability and gives editor tooling an unambiguous target.
  Encountering a named-import clause on a `std:*` or `dom:*` URI
  is a compile error with a diagnostic that points to the
  namespace-import alternatives.

## Scope of this workstream

In scope:

- A pseudo-package model with `std:*` and `dom:*` (and reserved
  `node:*`) URI-scheme imports under `internal/interop/data/std/`
  and `internal/interop/data/dom/`. **No ambient set** —
  everything lives in a package; nothing is unconditionally visible
  by name.
- Two-mode loading per `std:*` package: **shape-loaded** invisibly
  per-file based on language-feature usage (primitive method
  dispatch, `await`, iteration, …), and **named** when the file
  explicitly imports the package.
- Per-import binding-shape flags (`?local`, `?nested`, `?flat`)
  governing how a pseudo-package's namespace lands in the importing
  file.
- The **single-class shortcut** for per-class packages: when the
  package's lowercased name matches a class declared in it, the
  `?local` binding *is* the class for member access, construction,
  and type position.
- Cross-package augmentation via open registry interfaces
  (`HTMLElementTagNameMap`, `HTMLElementEventMap`,
  `SVGElementTagNameMap`, …) and the analogous augmentation of
  `Symbol`'s static side with well-known symbols from sibling
  packages, with activation scoped to the importing file.
- Inter-package imports between pseudo-packages.
- A one-time bootstrap converter `tools/dts_to_esc/` that
  AST-to-AST translates the pinned TypeScript `.d.ts` set into the
  initial `.esc` files. Output is hand-edited and committed; ongoing
  maintenance is by hand-editing source. The converter persists as a
  review tool (`--check`) for TypeScript version bumps, never as a
  build step.
- Prelude changes: stop walking `lib.*.d.ts`; replace with lazy
  per-file shape loading of `std:*` packages.
- **Adaptive diagnostic rendering** — type names in diagnostics
  use the shortest unambiguous form given the bindings in the file
  where the diagnostic originates.
- **Auto-import** as a first-class LSP feature, adding a namespace
  import for the package owning the referenced name.
- Always-current API at type-check time; runtime version
  compatibility is the codegen's job (polyfill insertion at
  lowering).
- Intrinsic types (`Uppercase`, `Lowercase`, `Capitalize`,
  `Uncapitalize`, `NoInfer`) implemented as checker-resident
  handlers, not source-visible in `.esc`. `Awaited<T>` is
  source-expressible in principle (recursive conditional type); we
  attempt the source-level definition first and only fall back to
  an intrinsic if a concrete blocker surfaces.
- A declaration-printer audit (FR14 gate) covering declaration-level
  forms (`declare class`, `declare fn`, type aliases, generic
  constraints, mapped types with `as` rename, conditional types
  with `infer`, ambient module syntax) prior to committing to the
  converter approach.

## Functional requirements

### FR1. No ambient set; shape-loaded vs named bindings

There is **no `builtins.esc` ambient set.** Every declaration lives
in a pseudo-package; nothing is unconditionally visible by name.
The previous "ambient builtins" tier is gone — replaced by a
two-mode loading model for `std:*` packages.

The checker treats each pseudo-package's `.esc` file in two modes:

- **Shape-loaded (invisible at startup, no name bindings).** The
  checker loads the relevant package contents to satisfy *implicit*
  type-resolution needs that arise from language syntax — primitive
  method dispatch on a string literal, the result type of `await`,
  the iterable protocol behind `for x of xs`, the array shape behind
  an array literal, the regex shape behind a regex literal,
  well-known symbols referenced by desugarings. No identifier is
  added to user scope; this is purely the checker knowing what the
  language guarantees about its own values.
- **Named (visible after `import "std:X"`).** Naming a class, type,
  or value (`Array`, `Promise`, `Error`, `parseInt`, `Partial`,
  `Symbol`, …) requires an explicit import. The bindings exposed
  are exactly the package's top-level declarations.

The shapes are loaded lazily and per-file: when checking file F,
the checker inspects which language features F uses and shape-loads
only the packages those features depend on. Shape-loading is
purely additive — multiple files in a compilation share a single
parsed copy of each `std:*` package. The user-visible model is
"the checker just knows" what methods strings/arrays/promises have;
the lazy-load detail is an implementation concern.

**Package partition (full list).** Per-class packages — each
contains exactly one top-level class (and possibly related type
aliases or interfaces), and is eligible for the single-class
shortcut defined in FR5:

- `std:array` — `Array<T>`
- `std:string`, `std:number`, `std:boolean`, `std:bigint` —
  primitive wrapper classes. `parseInt`, `parseFloat`, `isNaN`,
  `isFinite` live in `std:number` since their domain is numeric
  parsing.
- `std:regexp` — `RegExp`
- `std:promise` — `Promise<T>`
- `std:symbol` — `Symbol`. The well-known symbols (`Symbol.iterator`,
  `Symbol.asyncIterator`, `Symbol.toPrimitive`, …) are *not*
  declared here; sibling packages augment `Symbol`'s static side
  with the symbols they own (see FR8).
- `std:object` — `Object`. Also hosts object-shaped utility types:
  `Partial`, `Required`, `Readonly`, `Pick`, `Omit`, `Record`,
  `Exclude`, `Extract`, `NonNullable`. The last three operate on
  union types but `std:object` is the pragmatic catch-all for
  type-manipulation utilities.
- `std:function` — `Function`. Also hosts function-shaped utility
  types: `Parameters`, `ConstructorParameters`, `ReturnType`,
  `InstanceType`, `ThisParameterType`, `OmitThisParameter`,
  `ThisType`.

Bundled packages — multiple types, no single-class shortcut:

- `std:iterator` — `Iterator<T>`, `Iterable<T>`,
  `IterableIterator<T>`, `IteratorResult<T>`,
  `Generator<T, R, N>`; augments `Symbol` with `iterator`.
- `std:async` — `AsyncIterator<T>`, `AsyncIterable<T>`,
  `AsyncGenerator<T, R, N>`, `AggregateError`; augments `Symbol`
  with `asyncIterator`. (`Promise<T>` itself lives in `std:promise`,
  not here.) Depends on `std:iterator` for the iteration-protocol
  base.
- `std:error` — `Error`, `TypeError`, `RangeError`, `SyntaxError`,
  `ReferenceError`. The five ubiquitous ones. Domain-specific
  errors live with their domain: `URIError` in `std:url`,
  `AggregateError` in `std:async`. `EvalError` is dropped.

Other `std:*` packages from the existing layout (`math`, `json`,
`console`, `date`, `map`, `set`, `weak_ref`, `typed_arrays`,
`reflect`, `proxy`, `intl`, `temporal`, `wasm`) are unchanged in
structure; per-class packages (`std:date`, `std:map`, `std:set`,
`std:weak_ref`) participate in the single-class shortcut.

**What `globalThis` and `eval` do.** Both drop entirely. `eval` has
no good use case; `globalThis` was the union of every previously-
ambient name, and with no ambient set there is nothing to take its
union over.

**Intrinsics, unchanged.** `Uppercase`, `Lowercase`, `Capitalize`,
`Uncapitalize`, `NoInfer` remain checker-resident handlers; they
have no source file (see FR13). `Awaited<T>` is source-expressible
and lives in `std:promise` (consumed shape-only by `await`); we
fall back to an intrinsic only if a concrete blocker surfaces.

### FR2. Pseudo-package layout

Every declaration lives in a pseudo-package under
`internal/interop/data/std/` or `internal/interop/data/dom/`. There
is no `builtins.esc` at the data root — the ambient tier is gone
(see FR1). File layout:

```
internal/interop/data/
    std/
        # per-class packages (eligible for single-class shortcut)
        array.esc, string.esc, number.esc, boolean.esc, bigint.esc,
        regexp.esc, promise.esc, symbol.esc, object.esc,
        function.esc,
        date.esc, map.esc, set.esc, weak_ref.esc,

        # bundled packages
        iterator.esc, async.esc, error.esc,

        # other std:* packages
        math.esc, json.esc, console.esc,
        typed_arrays.esc, reflect.esc, proxy.esc,
        intl.esc, temporal.esc, wasm.esc

    dom/
        dom.esc, html.esc, svg.esc, mathml.esc,
        http.esc, canvas.esc, webgl.esc, webrtc.esc,
        storage.esc, workers.esc, media.esc, forms.esc,
        # additional dom:* packages as needed
```

DOM packages split more finely than `std/` because the surface is
larger; a typical browser program touches 2–3 `dom:*` packages.

**File-naming convention.** Multi-word package names use underscores
in both the file name and the user-visible binding so the two
match: `std/weak_ref.esc` binds as `weak_ref`,
`dom/web_rtc.esc` binds as `web_rtc`. Hyphens do not appear in
file names.

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
specifiers to the resolved stdlib data directory (see
"Filesystem-resident stdlib data" in non-functional requirements
for the discovery scheme). Mapping is mechanical:
`std:math` → `<stdlib>/std/math.esc`,
`dom:http` → `<stdlib>/dom/http.esc`. The `?flag`
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
FR4 for the rules that consume them.

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
  - **Exception:** when the package qualifies for the single-class
    shortcut (FR5), the `?local` binding is the class name with its
    original capitalization (e.g. `Array`, `Date`), not the
    lowercase URI segment. The shortcut applies only under `?local`.
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

**Identifier hygiene.** Package names use underscores, matching
the file naming convention (FR2): `import "std:typed_arrays"` binds
as `typed_arrays`, `import "std:typed_arrays?nested"` binds as
`std.typed_arrays`. For `std:*` and `dom:*` pseudo-packages there
is no `-` → `_` substitution because hyphens never appear in their
URIs. (Third-party npm package names *can* contain hyphens and will
need a `-` → `_` substitution when computing the binding name; that
substitution rule is part of the third-party workstream and is out
of scope here.) `?flat` drops the package name from the user-visible
binding, but internal bookkeeping (tracking whether a given file's
imports have already pulled in a package's declarations / registry
augmentations) uses the pseudo-package's full URI as the key —
e.g. `dom:canvas` — regardless of which binding-shape flag the
import carried. This applies uniformly across `?local`, `?nested`,
and `?flat`.

The flag slot is extensible: future flags (e.g. `?type-only`,
`?lazy`) compose with the binding-shape flags subject to their own
per-flag compatibility rules.

### FR5. Single-class shortcut

When a package's lowercased name matches a class declared in that
package, the `?local` binding **is** that class, named with the
class's original capitalization (e.g. `Array`, not `array`) — for
member-access, constructor-call, and type-position purposes. Other
exports of the package are still accessible as namespace members
on the same binding.

```escalier
import "std:array"
import "std:date"

let nums = [1, 2, 3]
Array.isArray(nums)         // class statics
let xs: Array<number> = []  // type position
Array<string>(5)            // construct (no `new` keyword)

let start = Date.now()      // class statics
let d: Date = Date()        // type and constructor
```

**Activation rule.** The shortcut applies iff, after lowercasing
the package's last URI segment, the package declares a top-level
class whose name matches that lowercased segment case-insensitively.
`std:array` declares `Array<T>`; the shortcut applies and the
binding is `Array`. `std:math` declares no `Math` class (it's a
namespace of free functions); no shortcut — the binding stays
lowercase `math` and `math.sin(x)` works as a plain namespace
access. `std:iterator` exports several types and no single
dominant class; no shortcut, binding stays lowercase `iterator`.

**Eligible packages from the FR1 partition.** `std:array` (→
`Array`), `std:string` (→ `String`), `std:number` (→ `Number`),
`std:boolean` (→ `Boolean`), `std:bigint` (→ `BigInt`),
`std:regexp` (→ `RegExp`), `std:promise` (→ `Promise`),
`std:symbol` (→ `Symbol`), `std:object` (→ `Object`),
`std:function` (→ `Function`), `std:date` (→ `Date`), `std:map`
(→ `Map`), `std:set` (→ `Set`), `std:weak_ref` (→ `WeakRef`).
Each per-class package's `?local` binding is its class.

**Disambiguation.** Under `?local` the binding `Array` resolves
both as a namespace member (`Array.someOtherExport`) and as the
class itself (`Array.isArray`, `Array(5)`, `Array<number>`).
Static methods on the class take precedence when names collide
with other package exports — a collision should be rare in
practice given the small surface of per-class packages, but the
rule is "class statics win" so the shortcut behavior remains
predictable.

**Not applicable to `?nested` or `?flat`.** Those binding shapes
do not use the capitalized shortcut form; they follow the
URI-segment-based rules in FR4. `?nested` binds under
`scheme.package`, so writes look like `std.array.Array.isArray(nums)`
— the package and class names are both explicit. `?flat` drops
the package name entirely; the class is accessed as
`std.Array.isArray(nums)`. In both cases the shortcut adds no
value because the class is already directly nameable.

### FR6. Inter-package imports

Pseudo-packages that reference types declared in other
pseudo-packages must `import` them explicitly, exactly like ordinary
user code. Examples:

- `dom/canvas.esc` needs `import "dom:dom"` (in some binding shape)
  to extend `HTMLElement`.
- `std/intl.esc` needs `import "std:date"` to reference `Date`.

There is no implicit "all sibling packages visible" rule, and
there is no ambient tier (see FR1). The checker shape-loads
language-level dependencies (primitive method shapes, iterator
protocol, `Promise` for `await`, etc.) invisibly per-file —
those shapes are not name-bound and do not satisfy explicit
references like `Promise.all(...)` or `Error(...)`, which
still require an explicit import of the owning package.

**Import cycles between pseudo-packages are permitted.** The
ordinary cycle-detection rule for user packages does **not** apply
to imports between `std:*`, `dom:*`, and (reserved) `node:*`
packages. The pseudo-package layer naturally contains cycles:
`std:promise` references `Iterable<T>` from `std:iterator`;
`std:iterator` may reference `Promise<T>` (or types from
`std:async`) for protocol overlap; `Error` subclasses in
`std:error` may reference types declared in `std:array` or
`std:string`; etc. These cycles are purely **type-level** and
**runtime-erased** — the JavaScript runtime exposes all of these
as pre-existing globals (`Promise`, `Symbol`, `Error`, …), so
there is no runtime initialization order to worry about. The
checker accepts cycles within the pseudo-package layer; the
resolver/dep-graph code must special-case the `std:`, `dom:`, and
`node:` schemes to skip cycle reporting for imports whose source
*and* target both live under these schemes.

User code → pseudo-package imports remain acyclic (user code can
depend on pseudo-packages but pseudo-packages do not depend on
user code), and cycles among user packages remain disallowed as
before.

### FR7. Cross-package augmentation via open registries

Splitting the DOM into feature packages requires that `dom:dom`
APIs return or reference types that live in feature packages, e.g.
`document.createElement("canvas")` should return
`HTMLCanvasElement`.

**Primary mechanism: open registry interfaces, augmented per
package.** `dom:dom` declares the registry (`HTMLElementTagNameMap`,
`HTMLElementEventMap`, `SVGElementTagNameMap`,
`MathMLElementTagNameMap`, etc.) with a minimal baseline or empty
body. Core APIs are written once against the registry:

```escalier
fn createElement<K: keyof HTMLElementTagNameMap>(
    self, tag: K
) -> HTMLElementTagNameMap[K]

fn createElementNS<K: keyof SVGElementTagNameMap>(
    self, ns: "http://www.w3.org/2000/svg", qualifiedName: K
) -> SVGElementTagNameMap[K]
```

Each feature package augments only the registry by declaring its
own contribution to the open interface in its `.esc` source, e.g.
`dom:canvas`:

```escalier
declare interface HTMLElementTagNameMap {
    canvas: HTMLCanvasElement,
}
```

Every registry-keyed API (`createElement`, `querySelector`,
`getElementsByTagName`, `createElementNS`, …) picks up the new
entry automatically.

**Registries live in `dom:dom`; the element families they index
live in their feature packages.** `SVGElement` and its subclasses
live in `dom:svg`; `MathMLElement` and its subclasses live in
`dom:mathml`; specialized HTML element classes
(`HTMLCanvasElement`, `HTMLVideoElement`, …) live in their
respective feature packages. `dom:dom` declares each registry
interface with an empty body — no reference to the element classes
themselves — and writes the keyed APIs purely against the registry.
Feature packages augment the registry with their entries (e.g.
`dom:svg` adds `{ circle: SVGCircleElement, path: SVGPathElement,
… }` to `SVGElementTagNameMap`). The registry interface is more a
contract slot than a real type; the value types it indexes need
not be visible to `dom:dom` at all.

A file that imports `dom:svg` gets `createElementNS(svgNS, "circle")
→ SVGCircleElement`; a file without the import gets `never` from
the indexed access, which loudly surfaces the missing import. This
keeps the augmentation pattern uniform across all element families
and avoids needing the per-API augmentation fallback for the SVG
and MathML cases.

**No `override declare`.** Builtins are declared directly in their
own `.esc` files. The `override declare` syntax is **not** used for
pseudo-package augmentation — that syntax is reserved for the
third-party workstream's override mechanism and does not apply to
builtins or pseudo-packages.

**Fallback: per-API augmentation** for the rare cross-package APIs
that do not fit a registry pattern. Two cases:

1. The API takes the type as a *parameter* (not a return type): the
   feature package exports the type; `dom:dom` references it via
   the type's qualified name; no augmentation needed.
2. The API is overload-keyed on a string but lacks a registry: the
   feature package augments the API's overload set directly. Rare;
   treat as one-off when it arises.

**Language requirements this depends on:**

- Open interfaces / cross-package interface merging. The §5
  override-merge code already does interface merging for type
  refinement; whether the same mechanism can be repurposed for
  cross-package augmentation, or whether augmentation needs its own
  loader path, must be confirmed during the resolver-extension step.
- Indexed access over open registries: `HTMLElementTagNameMap[K]`
  where `K extends keyof HTMLElementTagNameMap` must refresh
  correctly when the registry is augmented.

Activation semantics for these augmentations — per-importing-file
scoping, transitive propagation rules, independence from the
binding-shape flag — are specified in FR9.

### FR8. Symbol augmentation and well-known symbol re-exports

The registry mechanism from FR7 applies to `Symbol`'s static side
as well. `std:symbol` declares `Symbol` with no well-known symbols
on its static side. Sibling packages augment `Symbol` with the
symbols they own: `std:iterator` adds `iterator: unique symbol`,
`std:async` adds `asyncIterator: unique symbol`, and so on.

**Shape-loaded language-level use vs. explicit references.**
Language-level use of well-known symbols (the `for-of` desugaring
referencing `Symbol.iterator`, etc.) goes through the checker's
**shape knowledge** and does not require any import. Explicit
references via `Symbol.<name>` require importing both `std:symbol`
(for the `Symbol` name) and the domain package that owns the
augmentation entry.

**Each domain package also re-exports its owned symbol(s) under a
short package-local name**, so a file that needs the well-known
symbol does not have to import `std:symbol` separately. The same
runtime value is exposed twice — once as the namespace member from
the domain package, once as a static on `Symbol` — and a file
picks whichever reads better:

```escalier
import "std:iterator"

class Range {
    [iterator.key]() {
        // ... implement the iterator protocol
    }
}
```

vs. the dual-import form:

```escalier
import "std:symbol"
import "std:iterator"

class Range {
    [Symbol.iterator]() {
        // ...
    }
}
```

**Naming convention.** A domain package owning **one** well-known
symbol exposes it as `<package>.key`: `iterator.key`, `async.key`.
A package owning **multiple** well-known symbols exposes each as
`<package>.<name>Key`, using the symbol's ECMAScript spelling for
`<name>`: for example, if `std:regexp` ends up owning the
regex-related symbols, it exposes them as `regexp.matchKey`,
`regexp.replaceKey`, `regexp.searchKey`, `regexp.splitKey`,
`regexp.matchAllKey`. The `<name>Key` form is consistent for
multi-symbol packages; the bare `key` form avoids stuttering for
singletons.

The name on the `Symbol` static side keeps its standard ECMAScript
spelling (`Symbol.iterator`, `Symbol.match`, …) regardless of how
the domain package re-exports it. Having the same value exported
by more than one pseudo-package is fine — the package-local name
is an alias for the canonical `Symbol`-side name, not a separate
symbol.

Symbol augmentations share the activation semantics specified in
FR9 (per-importing-file scoping, explicit-re-export propagation,
flag independence).

### FR9. Augmentation activation semantics

The scoping rules below apply uniformly to both DOM-style registry
augmentation (FR7) and Symbol augmentation (FR8). Any future
augmentation mechanism added under this workstream inherits the
same rules.

**Per-importing-file scoping.** Augmentation activation is
**scoped to the importing module**, not the whole compilation
unit. A file that imports `dom:canvas` sees the augmented
`HTMLElementTagNameMap` entry for `canvas`; a sibling file in the
same program that does not import `dom:canvas` does not. This
deliberately departs from TypeScript's `declare module` behavior,
where any module-augmenting import becomes global. The type a
file sees is fully determined by that file's own imports.

**Transitive propagation via explicit re-export.** Augmentations
propagate only through **explicit re-exports** of the pseudo-package.
If file A writes `export * from "dom:canvas"` (or an equivalent
named re-export of canvas's bindings), then a file importing A
sees canvas's augmentations as part of importing A. A plain
`import "dom:canvas"` in A that does not re-export anything from
canvas does *not* leak the augmentation to A's importers. Simply
importing a package never magically re-exports it.

**Independent of the binding-shape flag.** Importing `dom:canvas`
under any of `?local`, `?nested`, or `?flat` adds canvas's
contributions to that file's view of `HTMLElementTagNameMap`. The
flag only affects how canvas's *direct exports* land in the
importing scope, not which augmentations are activated.

### FR10. Bootstrap converter (`tools/dts_to_esc/`)

A one-time seeding tool. A Go binary that:

1. Reads the pinned TypeScript lib `.d.ts` set via the existing
   `internal/dts_parser/`.
2. Converts each `.d.ts` declaration **directly into the
   corresponding Escalier declaration AST**, bypassing
   `type_system.Type` and the checker entirely. The TS-side AST node
   for `declare class Foo`, `interface Bar`, `declare function baz`,
   `type alias`, etc. maps mechanically to the matching Escalier
   declaration. `declare namespace` blocks in the input are
   flattened — their inner declarations become top-level
   declarations in the output `.esc` file, since each pseudo-package
   file is itself a single namespace and Escalier does not emit
   nested namespace syntax (see FR14). This is a pure AST-to-AST
   translator — no type resolution involved, so there is no
   prelude-needs-checker-needs-prelude bootstrap cycle.

   The translator recognizes TypeScript's class-via-trio idiom
   (`interface Foo` + `interface FooConstructor` +
   `declare var Foo: FooConstructor`) at the AST level and emits a
   single `declare class Foo` Escalier declaration — instance
   members from the `Foo` interface, static members and constructor
   signature from `FooConstructor`, the `declare var` dropped. This
   is the AST-level analogue of the type-level fusion in
   [internal/interop/class_shapes.go](../../internal/interop/class_shapes.go)
   (`tryFuseTrio`); the recognition rules are identical (constructor
   `new` signature must return the instance type; both sides must be
   object-shaped; etc.), only the substrate differs. The trio's
   sibling Escalier-style shape recognized by `tryFuseEscalierClass`
   is not expected in `lib.*.d.ts` and need not be handled.
3. Runs `interop.Classify` (tiers 3/5/6) at conversion time to seed
   receiver mutability into the emitted AST (`self` vs `mut self` on
   each method).
4. Partitions the resulting declarations into pseudo-package `.esc`
   files under `std/` or `dom/`, per the partition specified in
   FR1 (e.g. `Array<T>` → `std/array.esc`; `Promise<T>` →
   `std/promise.esc`; `Error` family → `std/error.esc`; etc.). The
   partition is driven by a hand-maintained mapping table in the
   converter, not by anything in the `.d.ts`.
5. Detects which TS-lib symbols belong to registries
   (`HTMLElementTagNameMap`, `HTMLElementEventMap`, …) and which
   well-known symbols belong to which `std:*` package (e.g.
   `Symbol.iterator` → `std/iterator.esc` augmentation;
   `Symbol.asyncIterator` → `std/async.esc` augmentation), routing
   their per-feature entries into the appropriate package's
   augmentation block.
6. **Preserves JSDoc.** Leading JSDoc comments attached to TS-side
   declarations carry through to the emitted Escalier declaration
   as doc comments. This is a pass-through, not a transform — most
   of the prose value in `lib.dom.d.ts` is JSDoc, and reusing it
   gives the generated `.esc` files documentation parity with the
   TS source at zero authoring cost. A small set of TS-specific
   tags may need stripping or rewriting (`@override` dropped,
   `@param` syntax touched up where Escalier differs); the rest
   pass through verbatim. `internal/dts_parser/` must attach
   leading JSDoc to declaration AST nodes if it does not already.
7. Renders each output file via the (now-validated) declaration
   printer and `internal/type_system/print_type.go`.
8. Supports a `--check` mode that diffs generated output against
   committed files without overwriting.

**One-time seeding.** After the initial bootstrap commit, the
pseudo-package `.esc` files are first-class Escalier source. Hand-edits add `throws`, lifetimes, and any
structural refinements — written inline like `self` and `mut self`,
no merge layer. JSDoc is *not* in the hand-edit list because it is
carried through automatically by step 6 above; manual editing of
JSDoc is only for places where the upstream comment is wrong,
incomplete, or Escalier-specific behavior diverges. Re-running the
converter is a **review tool** (`--check`), never a build step;
its output is never auto-applied.

**`throws` annotations are hand-curated for now.** Scraping MDN is
tempting but the failure modes (prose-not-data extraction,
brittleness, copyleft licensing of MDN content) outweigh the wins.
The realistic sources of `throws` data are:

- **WebIDL `[Throws]` extended attributes** for DOM APIs (DOM
  specs publish machine-readable IDL; `@webref/idl` ships curated
  extracts). Plausible future automation lever for `dom:*`
  packages; out of scope for the initial bootstrap.
- **ECMAScript spec (ECMARKUP) annotations** for `std:*` builtins.
  No off-the-shelf extractor; a one-shot script could harvest a
  starting set if needed.
- **Hand-curation of the high-value ~50 entries.** `JSON.parse`,
  `decodeURI*`, `BigInt`, `fetch`, `Response.json`, etc. The set
  of JS APIs that throw under normal use is small; hand-editing
  the most common ones is faster than building scraping
  infrastructure. This is the approach for the initial bootstrap.

**TS version bump workflow.** Run
`tools/dts_to_esc/regenerate --check` against the bumped TS. The
output is a diff against the current files showing what TS added /
removed / changed. A contributor decides which changes to port by
hand. An optional CI nudge can annotate a PR with "TS lib changed
since last bump" when the diff exceeds some threshold.

### FR11. Prelude changes; lazy per-file shape loading

`Prelude` in `internal/checker/prelude.go` stops walking
`node_modules/typescript/lib/*.d.ts` for global symbols. **It also
does not pre-load any pseudo-package as an ambient global namespace
— there is no ambient set (FR1).**

Instead, the checker drives two new loading paths:

1. **Lazy shape loading, per file.** Before/while checking file F,
   the checker inspects which language features F uses and parses
   the corresponding `std:*` packages in shape-only mode (no name
   bindings added to scope). The trigger map:

   - String/number/boolean/bigint literals or operator dispatch on
     primitive values → `std:string` / `std:number` / `std:boolean`
     / `std:bigint`.
   - Array literals → `std:array`.
   - Regex literals → `std:regexp`.
   - `async fn` / `await` → `std:promise` (and `std:async` if the
     file uses async iteration).
   - `for x of xs` / generators → `std:iterator`.
   - `for await x of xs` → `std:async`.
   - `try` / `catch` / `throw` / `throws` clauses on functions
     that use error-class names → `std:error` (only when error
     classes are explicitly referenced by name).

   Multiple files in a compilation share one parsed copy of each
   package; shape-loading is idempotent and additive.

2. **Explicit import loading, named.** `import "std:array"` (or
   any binding-shape variant) goes through the standard module
   resolver (FR3) and binds names into F's scope per FR4 / FR5.

The shape-load and named-import paths reuse the same parsed
declarations; they differ only in whether identifiers are added
to user scope. There is no bootstrap cycle because each `std:*`
package contains only declarations (no value-level expressions
needing their own prelude).

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
`internal/checker/prelude.go:804–836`) move into the appropriate
pseudo-package as ordinary source. `Symbol.customMatcher` lives in
`std:symbol` (or wherever pattern matching's runtime support lands)
and is hand-authored — the bootstrap converter will not emit it
because it is not part of any `lib.*.d.ts`.

The **builtin tier** of the override store goes away entirely. There
are **no override `.esc` fragments for builtins** — no
`internal/interop/data/builtins/` overrides subtree, no per-builtin
merge layer. After the one-time bootstrap conversion of
`lib.*.d.ts`, the generated `.esc` files are **manually edited** to
add receiver mutability, lifetimes, thrown-exception annotations,
and any other Escalier-specific refinements, and then **maintained
as source going forward** (re-running the converter is only a
`--check` review tool; its output is never auto-applied). JSDoc is
carried through automatically by the converter (FR10 step 6); it
appears in the hand-edit list only when upstream comments need
correction.

### FR12. Always-current API; polyfills at lowering

The type checker always sees the modern surface. ES2024 methods
like `Promise.withResolvers` are present at type-check time even
when the codegen target is older. Runtime-version compatibility is
the codegen's job: when lowering to an older target, the emitter
inserts a polyfill for any newer feature actually used. ES-version
awareness does not enter the type system.

Polyfill insertion is its own follow-up effort outside this
workstream. This document assumes only that it is tractable.

### FR13. Intrinsic types are checker-resident

`Uppercase`, `Lowercase`, `Capitalize`, `Uncapitalize`, and
`NoInfer` are implemented directly in the Escalier type checker as
named handlers — the four string-case utilities as pure
`Type → Type` resolvers, and `NoInfer` as an inference-machinery
hook (see escalier-lang/escalier#631).

`Awaited<T>` is **not** in this list. Before resorting to an
intrinsic implementation, we verify the source-level definition:
write `Awaited<T>` in `std:promise` as the recursive conditional
type (the same shape as TypeScript's definition, per
escalier-lang/escalier#630), exercise it against a representative
fixture (nested promises, thenables, mixed `T | Promise<T>`,
generic propagation), and only fall back to a checker-resident
intrinsic if a concrete blocker surfaces — e.g. recursive
conditionals don't yet reduce correctly, performance is pathological
under real workloads, or a soundness issue appears. The fallback
must be documented with the specific failure that motivated it.

The `intrinsic` keyword therefore never appears in `.esc` files.
The bootstrap converter strips or substitutes any `intrinsic`-typed
declaration found in source `.d.ts`. The Escalier parser does
*not* need to accept `intrinsic`.

### FR14. Declaration printer audit

The bootstrap converter must emit declaration-level forms (`declare
class`, `declare fn`, `declare type alias`, `declare var`, generic
parameter constraints with `extends`, ambient module syntax,
conditional types referring to `infer T`, mapped types with `as`
rename clauses, …). `declare namespace` is **not** in this list:
each pseudo-package `.esc` file is itself a single namespace (the
package's), so the converter never emits nested namespace
declarations — TS `declare namespace` blocks in the input map to
top-level declarations in the output file. An FR14 audit pass —
parallel in shape to the prior §8 audit of `type_system.Type` — must
confirm these declaration forms round-trip through the parser before
committing to the converter approach. Most of these forms are
believed to be already supported; the audit is up-front
confirmation. This is the **riskiest gate**; do it first — even
though it carries the FR14 label, it is the first concrete step of
the implementation plan.

### FR15. Adaptive diagnostic rendering

Type names in diagnostics are rendered using the shortest
unambiguous form given the bindings in scope at the diagnostic's
source location. The renderer takes the type plus the importing
file's scope and picks among:

1. **Single-class shortcut.** If the file has a `?local` import
   whose package qualifies for the single-class shortcut (FR5),
   render as the capitalized class binding — `Array<number>`,
   `Date.now()` — matching what the user would write.
2. **Namespace member.** `?local` without shortcut → `math.Foo`;
   `?nested` → `std.math.Foo`; `?flat` → `std.Foo`.
3. **Not imported.** Render as the fully-qualified canonical name
   (`std:array.Array`) and pair the diagnostic with a "did you mean
   to `import \"std:array\"`?" hint (see FR16).

(Named imports from pseudo-packages are out of scope per Non-goals,
so the renderer has no "bare name" case to handle.)

When multiple forms are simultaneously in scope, the renderer picks
the shortest; ties break in the order bindings appear above. The
rendering is per-diagnostic, not per-compilation, so a type seen in
two files with different imports is named differently in each.

The implementation surface is a `renderTypeForLocation(t, scope)`
function replacing the global `renderType(t)`, threading the
file-scope through the diagnostic pipeline.

### FR16. Auto-import (LSP first-class)

When a user types a bare reference to a known pseudo-package export
in a file that has not imported the owning package, the LSP offers
a quick-fix that:

1. Adds the appropriate namespace import statement
   (`import "std:promise"`, `import "std:math"`, …).
2. For single-class shortcut packages (FR5): leaves the bare
   reference unchanged, since the import binding *is* the class
   name in its capitalized form. `Promise.all([...])` typed
   without an import → quick-fix adds `import "std:promise"` and
   leaves the reference as-is; same for `Array.isArray`,
   `Date.now`, `Error(...)`, etc.
3. For non-shortcut packages: rewrites the bare reference to
   qualify it through the resulting namespace binding (e.g. a
   bare `sin(x)` triggers `import "std:math"` and rewrites the
   call to `math.sin(x)`).

Named imports from pseudo-packages are out of scope (see
Non-goals), so the quick-fix only adds a namespace import. There
is no named-import variant of the quick-fix in this workstream.

**Auto-import is a hard requirement, not a deferred nice-to-have.**
The migration story (every file that uses `Promise`, `Error`,
`Array.from`, `parseInt`, etc. now needs explicit imports) depends
on auto-import quick-fixes being available out-of-the-box in the
supported editor surfaces. Without it the per-file import overhead
falls on the user and adoption suffers.

The implementation depends on:

- An index of "name → owning pseudo-package" derived from the
  resolved stdlib data directory. Built at LSP startup;
  refreshed via filesystem watch on the data directory, so users
  editing their stdlib copy see the index update without LSP
  restart.
- A binding-shape preference per file (default `?local`; user-
  configurable). The quick-fix uses the file's existing convention
  if any of its imports already pick a flag.

## Non-functional requirements

- **Ergonomics.** Default `?local` binding gives terse access
  (`math.sin(x)`, `console.log(...)`). The binding choice per
  import is the file author's; no global configuration.
- **Soundness of activation.** A file's view of cross-package
  augmentations is fully determined by that file's own imports — no
  spooky action from a sibling module.
- **Zero runtime cost.** Pseudo-package imports erase entirely at
  codegen.
- **Filesystem-resident stdlib data.** `.esc` files under
  `internal/interop/data/` ship alongside the compiler binary and
  are loaded from disk at compile time. They are **not** embedded
  into the binary via `//go:embed`. Editability is the priority:
  compiler users tweak builtin types or add new packages by
  editing files in the install tree (or a copy pointed at by
  `ESCALIER_STDLIB_DIR`), with no recompile of the compiler.
  Discovery order: `ESCALIER_STDLIB_DIR` env var → `--stdlib-dir`
  CLI flag → sibling-to-executable (`<exe-dir>/../share/escalier/data/`)
  → repo-relative when running from a build tree. The toolchain
  has no `node_modules` or network dependency for pseudo-package
  declarations; the stdlib tree is a fixed install artifact.
- **`--explain-type` diagnostic.** When a tag-keyed return is wider
  than expected (e.g. `createElement` returning the union element
  type instead of `HTMLCanvasElement`), the diagnostic suggests
  likely `dom:*` imports to widen the file's view. Complements the
  FR16 auto-import quick-fix for the type-narrowing case.

## Migration phases

The phasing here is the subset of the original proposal's migration
path that pertains to the builtins workstream. Steps 10 (third-party
lazy cache) and 11 (deletion of the runtime interop pipeline) are
omitted; they belong to the third-party workstream.

1. **FR14 Declaration printer audit.** Same shape as the prior §8
   but for declaration-level forms. Establishes that the converter
   can emit `.esc` for the constructs `lib.*.d.ts` actually uses.
   Riskiest gate; do this first.
2. **URI-scheme import support.** Extend the parser and module
   resolver to accept `import "scheme:name"` and route `std:`,
   `dom:` (and reserved `node:`) prefixes to the resolved stdlib
   data directory. Default `?local`
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
   `dom/dom.esc` declares an empty `HTMLElementTagNameMap`;
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
5. **Converter productionization.** Add the package-partition
   table (which TS-lib declaration goes into which `std:*` / `dom:*`
   package per FR1/FR2), full output paths under
   `internal/interop/data/`, `--check` mode, and the full
   `lib.*.d.ts` set as input. The converter must also detect which
   TS-lib symbols belong to registries (and to `Symbol`'s well-known
   symbol set) and route their per-feature entries into the
   appropriate package's augmentation block.
   - **Gate:** every output file parses; running the converter
     twice produces byte-identical output.
6. **Stdlib bootstrap.** Run the converter, review the output,
   hand-edit obvious mis-classifications and high-value `throws`
   annotations across the generated `std/` and `dom/` packages,
   commit.
   - **Gate:** humans review the committed files.
7. **Prelude switchover.** Replace the lib-walking path in
   `internal/checker/prelude.go` with the lazy per-file shape
   loader (FR11). Old path stays behind a build flag for one
   release; run both side-by-side in CI; assert that programs
   type-check equivalently — the previously-ambient surface now
   resolves through shape-loading and/or explicit imports, and
   diagnostics for *unimported* references should appear under the
   new path (with the FR16 quick-fix offering the import).
8. **Existing code migration.** Update Escalier's own fixtures and
   tests that relied on previously-ambient symbols (`Math`, `JSON`,
   `console`, `Promise`, `Error`, `Array.from`, `parseInt`, …) to
   import the corresponding pseudo-package. This is the user-
   visible breaking change. The auto-import quick-fix (FR16) is
   expected to be available before this step lands so internal
   migration exercises the same tooling external users will rely on.
9. **Delete builtin overrides infrastructure.** Any builtin-tier
   override store / `data/builtins/overrides/` subtree contemplated
   by the current §6 plan goes away. `BuildBuiltinStore` returns an
   empty store; eventually the function deletes. Pseudo-package
   refinements live inline in the `.esc` files and are maintained
   as source (no merge layer). **Depends on steps 7 and 8** — the
   prelude must have switched to lazy shape loading (step 7), and
   fixtures relying on previously-ambient symbols must have
   migrated to `import "std:*"` (step 8), otherwise removing the
   override path regresses them.
10. **Flip the default.** Remove the build flag from step 7.

(Steps 10 and 11 from the original proposal's migration path —
third-party lazy cache and deletion of the runtime interop
pipeline — are intentionally omitted; they belong to the
third-party workstream. The steps above have been renumbered
sequentially within this workstream; the original step numbering
no longer applies.)

Steps 6–8 are authoring + migration paced by human review of
generated files. Steps 9 and 10 add a small amount of cleanup
work. This document deliberately avoids time-based effort
estimates; complexity (PR-sized vs needs-splitting) is the only
estimate that matters per step.

## Risks

- **Declaration-form printer fidelity** (see FR14). If
  `lib.es*.d.ts` contains shapes that do not round-trip, the whole
  approach is gated on extending the parser. Hard to estimate
  without doing the audit.
- **Ergonomic cost of imports.** Today every JS name (`console`,
  `Promise`, `Error`, `Array.from`, `parseInt`, …) Just Works
  ambient. Under this model every reference by name requires an
  explicit import. Defensible (Rust, Go, Python all require
  imports) but a meaningful friction increase relative to JS/TS,
  and **broader than the original "non-namespace globals" framing**
  — `Promise`, `Error`, and the utility types are now also affected.
  Mitigations: the FR16 auto-import quick-fix (hard requirement,
  not deferred); descriptive error messages on unbound names ("did
  you mean to `import \"std:async\"`?"); the single-class shortcut
  (FR5) keeping per-class access terse (`Array.isArray(xs)`,
  `Date.now()`).
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
- **Cross-package references between `std:*` files.** `Promise<T>`
  in `std:promise` references `Iterable<T>` from `std:iterator`;
  `Array<T>` in `std:array` references the iteration protocol; etc.
  The existing module
  loader handles this for user code; just need to confirm it works
  for shape-loaded `std:*` packages. Probably not an issue.

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
  near-spelling matches from the resolved stdlib package list.
- **Invalid flag combination.** Two of `?local`, `?nested`,
  `?flat` on the same URI. Message names the specific pair and
  explains they are mutually exclusive binding shapes.
- **Unknown flag.** `?something` that is not a recognized flag.
  Message names the flag and lists the currently-recognized set.
- **Named import from a pseudo-package URI.** `import { sin } from
  "std:math"` (named imports from pseudo-packages are out of scope
  for this
  workstream). Message explains that pseudo-package imports must
  use a namespace form (`import "std:math"` or
  `import "std:math?flat"`) and suggests the rewrite.
- **`?flat` name collision.** Two `?flat` imports under the same
  scheme contributing the same identifier. **Warning, not error**
  (per FR4): message names the colliding identifier, the two
  source packages, and which import won under the
  collision-resolution rule.

Each diagnostic ties back to a span on the offending `import`
statement, ideally pointing at the URI string literal (and within
it, the flag portion when the failure is flag-shaped).

## Source-map and diagnostic provenance for stdlib pseudo-packages

All pseudo-package declarations live in real `.esc` source under
the resolved stdlib data directory (see "Filesystem-resident
stdlib data" in non-functional requirements). Diagnostics that
reference these declarations (e.g. "expected `string`, got
`number`" pointing at a parameter of `String.prototype.charAt`)
need a source location that is meaningful to the user.

Requirements:

- Spans on declarations parsed from stdlib `.esc` files carry the
  **resolved filesystem path** to the file (e.g.
  `/usr/local/share/escalier/data/std/string.esc`). The file
  exists on disk and the user can open it directly. When the
  resolved path lies under a well-known install prefix, the
  diagnostic renderer may abbreviate it as `<stdlib>/std/string.esc`
  for compactness, but the underlying span still carries the real
  path so editor click-through works.
- Line/column information from the parser must be preserved so
  diagnostics point at the right line of the file. This is the
  same `Span` shape used for ordinary source files; no virtual
  source-map machinery is needed.
- When the user clicks through (LSP "go to definition") on a
  pseudo-package symbol, the editor opens the resolved file
  directly. If the install location is read-only (system
  install), the editor opens it in read-only mode; users who
  want to edit point `ESCALIER_STDLIB_DIR` at a writable copy
  per the discovery rules above.

## Testing strategy

- **Parser tests.** Bare-string imports (`import "std:math"`),
  `?flag` and `?flag1&flag2` suffix parsing. Round-trip via the
  printer.
- **Resolver tests.** End-to-end resolution of `std:`, `dom:`,
  `node:` (reserved → clear error) schemes; unknown scheme; known
  scheme + unknown package; `?flag` stripping before path lookup.
- **Binding-shape tests** (per FR4). Fixture per shape (`?local`,
  `?nested`, `?flat`), per single- and multi-package case,
  including `?flat` collision warning.
- **Registry augmentation tests** (per FR7). Two-file fixture
  where one file imports the augmenting package and the other does
  not; assert the augmented-vs-base type at each site (e.g.
  `createElement("canvas")` narrows in the importing file,
  returns the base element type in the sibling).
- **Symbol augmentation tests** (per FR8). Verify that domain
  packages augment `Symbol`'s static side correctly; verify the
  package-local re-export convention (`iterator.key`,
  `regexp.matchKey`, …) aliases to the same runtime value as
  `Symbol.<name>`; verify that a class implementing
  `[iterator.key]()` is recognized as iterable by `for-of`.
- **Augmentation activation tests** (per FR9). Per-file scoping:
  sibling file without the import does not see augmentations.
  Transitive propagation: `export * from "dom:canvas"` propagates
  augmentations; bare `import "dom:canvas"` (no re-export) does
  not. Flag independence: augmentation visibility is identical
  under `?local`, `?nested`, and `?flat`.
- **Prelude switchover parity.** While the old `lib.*.d.ts`-walking
  path remains behind a build flag (migration step 7), CI runs
  both side-by-side on a fixture suite. The parity check asserts
  that programs which *did* type-check under the ambient model
  still type-check under the new model after their previously-
  ambient references are rewritten through `import "std:*"`
  statements (the auto-import quick-fix is exercised as part of
  the parity check). Diagnostic equivalence is *not* expected —
  the new path reports unbound-name errors for unimported
  references, which is the intended user-visible difference.
- **Adaptive diagnostic rendering** (per FR15). Fixture per
  rendering case: `?local` with single-class shortcut → lowercase;
  `?local` without shortcut → dotted; `?nested` →
  scheme.package.name; `?flat` → scheme.name; no import →
  fully-qualified canonical name plus "did you mean to import"
  hint.
- **Auto-import quick-fix** (per FR16). LSP-level integration
  test: unimported reference produces a diagnostic; quick-fix
  applies the namespace import and the resulting source compiles.
- **Named-import rejection** (per Non-goals). Fixture where a file
  writes `import { x } from "std:math"`; assert the diagnostic
  fires with the message from the error-message taxonomy.
- **Snapshot tests via `go-snaps`.** Generated `.esc` output from
  the bootstrap converter has snapshots; `tools/dts_to_esc/
  --check` re-runs the converter and diffs against the committed
  files in CI to catch upstream TS changes.
- **Fixture tests** under `fixtures/` exercise each new failure
  mode listed in the error-message taxonomy with full
  message-text assertions (per CLAUDE.md test conventions).

## Backwards-compatibility and deprecation policy

The switch from "TS `lib.*.d.ts` walked at startup" to "every name
lives in a pseudo-package; nothing is ambient" is a **broad
user-visible breaking change**. Under the previous model only
namespace-shaped globals (`Math`, `JSON`, `console`, `Map`, `Date`)
required attention; under the new model essentially every program
needs imports, because **`Promise`, `Error`, `TypeError`,
`Array.from`, `Object.keys`, `parseInt`, `Partial`, `Pick`,
`Symbol`, etc. all become package-bound names**. Most JS-style
files will need 3–5 new imports.

The migration cost is real but each individual addition is
mechanical. The mitigation strategy leans heavily on tooling:

- **Deprecation flag.** Migration step 7 introduces a build flag
  that keeps the old `lib.*.d.ts`-walking path live alongside the
  new lazy-shape-load path for **one release cycle**. Users opt
  into the new behavior; the next release flips the default
  (migration step 10); the release after that removes the flag.
- **Diagnostic-assisted migration.** When a name that used to be
  ambient is referenced without an import under the new default,
  the unbound-name diagnostic includes a suggestion ("did you mean
  to `import \"std:async\"`?") whenever the unbound name matches a
  known pseudo-package export. The suggestion list is derived
  mechanically from the resolved stdlib data directory.
- **Auto-import quick-fix (FR16).** The LSP turns each unbound-
  name diagnostic into a one-keystroke fix that adds the namespace
  import and rewrites the bare reference to the qualified form.
  This is the primary migration aid for users editing in a
  supported editor; the diagnostic suggestion above is the
  fallback for command-line use.
- **No automatic codemod for user code in this workstream.** A
  separate codemod that rewrites every previously-ambient
  reference is desirable but out-of-scope here. The auto-import
  quick-fix and the diagnostic suggestion are the supported
  migration aids.
- **Internal fixtures and tests** migrate as part of migration
  step 8 in the same release that introduces the new default,
  serving as the canary for the user-facing change.
