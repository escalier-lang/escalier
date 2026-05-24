# Builtins: Ambient Set and Pseudo-Package Imports

## Provenance

Extracted from
[../interop_mutability/dts_to_esc_proposal.md](../interop_mutability/dts_to_esc_proposal.md).
That proposal bundles two distinct workstreams: (1) building `.esc`
files for the JavaScript and Web platform surface as `std:*` and
`web:*` pseudo-packages, and (2) lazy on-first-compile conversion
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
  `import "web:*"`, while still letting the checker reason about
  primitive method dispatch, iteration, and `await` from
  language-level shape knowledge.
- Provide a uniform import model (`import "std:math"`,
  `import "web:dom"`) for accessing standard-library and Web
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
  Encountering a named-import clause on a `std:*` or `web:*` URI
  is a compile error with a diagnostic that points to the
  namespace-import alternatives.

## Scope of this workstream

In scope:

- A pseudo-package model with `std:*` and `web:*` (and reserved
  `node:*`) URI-scheme imports under `internal/interop/data/std/`
  and `internal/interop/data/web/`. **No ambient set** —
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
- **The entire DOM tree lives in one package, `web:dom`** —
  core DOM, SVG, MathML, CSSOM, XML/XPath/parsing, selection,
  history, input events, observers, animations, custom elements,
  etc. All `lib.dom.d.ts` interfaces that meaningfully depend on
  `Document` / `Element` / `Event` are co-located. String-overload
  APIs (`createElement`, `createElementNS`) and their registries
  (`HTMLElementTagNameMap`, `SVGElementTagNameMap`,
  `MathMLElementTagNameMap`) live entirely inside `web:dom`,
  closed and not augmentable across packages.
- **Standalone web APIs split into sibling `web:*` packages** —
  the families that happen to ship in `lib.dom.d.ts` but have no
  DOM coupling: `web:fetch`, `web:streams`, `web:crypto`,
  `web:workers`, `web:webgl`, `web:web_audio`, `web:web_rtc`,
  `web:web_codecs`, `web:indexeddb`, `web:service_worker`,
  `web:websocket`, `web:storage`, `web:url`, `web:encoding`,
  `web:file`, `web:performance`, `web:webauthn`, `web:payments`.
- (True per-file cross-package augmentation — FR7/FR9 in earlier
  drafts — is **deferred** to a future workstream; the closed
  `web:dom` partition makes it unnecessary for MVP.)
- Inter-package imports between pseudo-packages, with
  cross-package type references via qualified names for APIs
  in sibling `web:*` packages that need to mention `web:dom`
  types (e.g. `web:fetch`'s `Response.body` returning a
  `web.streams.ReadableStream`).
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

**Package partition.** The canonical, full enumeration of
every `std:*` and `web:*` package — names, contents, single-
class shortcut eligibility, drops — lives in the partition
table at [implementation_plan.md §6.1](implementation_plan.md#61-partition-table).
That table is the single source of truth and the input to the
converter's routing logic; this section sketches the *shape* of
the partition (per-class vs bundled, the rationale for a few
non-obvious placements) without re-enumerating every package.

Per-class packages — each contains exactly one top-level class
(and possibly related type aliases or interfaces), and is
eligible for the single-class shortcut defined in FR5:

- `std:array` — `Array<T>`
- `std:string`, `std:number`, `std:boolean`, `std:bigint` —
  primitive wrapper classes. `parseInt`, `parseFloat`, `isNaN`,
  `isFinite` live in `std:number` since their domain is numeric
  parsing.
- `std:regexp` — `RegExp`
- `std:symbol` — `Symbol`, including **all** well-known symbols
  (`Symbol.iterator`, `Symbol.asyncIterator`, `Symbol.toPrimitive`,
  `Symbol.match`, `Symbol.replace`, …) declared directly on
  `Symbol`'s static side. Domain packages re-export the symbols
  they own as package-local aliases for ergonomics (see FR8) but
  the canonical declaration lives here.
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
  `Generator<T, R, N>`; re-exports `Symbol.iterator` as `iteratorKey`
  (FR8).
- `std:async` — `Promise<T>`, `Awaited<T>`, `AsyncIterator<T>`,
  `AsyncIterable<T>`, `AsyncGenerator<T, R, N>`, `AggregateError`;
  re-exports `Symbol.asyncIterator` as `asyncIteratorKey` (FR8). Depends on
  `std:iterator` for the iteration-protocol base. `Promise` lives
  here rather than in a dedicated `std:promise` because the
  surface (Promise + async iteration + async errors) is small
  enough to share one package; users write `async.Promise.all(…)`
  under the default `?local` binding shape.
- `std:error` — `Error`, `TypeError`, `RangeError`, `SyntaxError`,
  `ReferenceError`. The five ubiquitous ones. Domain-specific
  errors live with their domain: `URIError` in `std:url`,
  `AggregateError` in `std:async`. `EvalError` is dropped:
  it is a legacy class tightly coupled to `eval`, `eval` itself
  is dropped from the surface (see "What `globalThis` and `eval`
  do" below), and no modern engine throws `EvalError` from
  language semantics. Consumers that previously named `EvalError`
  (extremely rare) can fall back to `Error` or define a
  user-level error class — no shim is provided.
- `std:url` — `URIError`, plus the global URI-encoding functions
  `encodeURI`, `decodeURI`, `encodeURIComponent`,
  `decodeURIComponent`. Bundled; no single-class shortcut.

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
and lives in `std:async` (consumed shape-only by `await`); we
fall back to an intrinsic only if a concrete blocker surfaces.

### FR2. Pseudo-package layout

Every declaration lives in a pseudo-package under
`internal/interop/data/std/` or `internal/interop/data/web/`. There
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
        iterator.esc, async.esc, error.esc, url.esc,

        # other std:* packages
        math.esc, json.esc, console.esc,
        typed_arrays.esc, reflect.esc, proxy.esc,
        intl.esc, temporal.esc, wasm.esc

    web/
        # the entire DOM tree, including SVG/MathML/CSSOM/XML/
        # selection/history/input events/observers/animations/
        # custom elements/etc. Anything coupled to Document,
        # Element, or Event lives here.
        dom.esc,

        # standalone web APIs (no DOM coupling) that ship in
        # lib.dom.d.ts only by historical accident
        fetch.esc, streams.esc, crypto.esc, workers.esc,
        webgl.esc, web_audio.esc, web_rtc.esc, web_codecs.esc,
        indexeddb.esc, service_worker.esc, websocket.esc,
        storage.esc, url.esc, encoding.esc, file.esc,
        performance.esc, webauthn.esc, payments.esc
        # additional standalone web:* packages as needed
```

DOM-coupled APIs are deliberately bundled into a single
`web:dom` package: splitting along HTML/SVG/MathML lines turned
out to require cross-package augmentation primitives that are
not in scope for MVP (see FR7). A typical browser program imports
`web:dom` plus 1–2 standalone web packages (`web:fetch` etc.).

**File-naming convention.** Multi-word package names use underscores
in both the file name and the user-visible binding so the two
match: `std/weak_ref.esc` binds as `weak_ref`,
`web/web_rtc.esc` binds as `web_rtc`. Hyphens do not appear in
file names.

`node:*` content is reserved and deferred — when Node support lands,
populating `internal/interop/data/node/` follows the same model.

### FR3. URI-scheme import grammar

The module-specifier grammar accepts a new shape:

```escalier
import "std:math"
import "std:json"
import "web:dom"
import "web:fetch"
```

We need to **add** support for resolving modules with the `std:`,
`web:`, and (reserved) `node:` schemes; nothing in the existing
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
`web:fetch` → `<stdlib>/web/fetch.esc`. The `?flag`
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

**Lowering via `@js` decorators.** Pseudo-package binding names
are not generally identical to their JS-runtime names
(`math.sin(x)` lowers to `Math.sin(x)`; `parseInt` from
`std:number` lowers to bare `parseInt(...)`, not
`Number.parseInt(...)`; `iterator.iteratorKey` re-export lowers to
`Symbol.iterator`). Every **exported** top-level declaration in
a pseudo-package `.esc` file therefore carries a per-declaration
`@js("...")` decorator naming the JS expression it lowers to.
The decorator is the single source of truth for codegen; there is
no package-level default. The Escalier parser learns decorator
syntax as part of this workstream (the only decorator defined here
is `@js`, but the grammar leaves room for future ones). Class
construction with `new` is **not** carried by `@js` — the codegen
inserts `new` at the call site based on the callee's type, so
`Date()` in Escalier lowers to `new Date()` even though the
class's decorator is just `@js("Date")`.

**`export` is required for package members.** Pseudo-package
`.esc` files follow the regular Escalier module visibility rule:
only `export`-prefixed declarations are visible outside the file
and participate in the package's namespace. The canonical shape
is `<decorators> export declare <kind> ...` (decorators outermost,
then `export`, then `declare`). Internal helper declarations
(used only inside the file) are not exported and carry no `@js`.
This parallels how user packages work and makes
package-private declarations expressible.

### FR4. Binding-shape flags

The URI may carry one or more `?flag` modifiers separated by `&`
(URL-query convention). Three flags govern the **binding shape**;
they are mutually exclusive and exactly one is in effect per import
(absent flag is treated as `?local`):

- **`?local` (default).** Bind the package's contents under a local
  identifier equal to the lowercased last URI segment. Each import
  is its own binding; no cross-import merging.
  - `import "std:math"` → `math.sin(x)`, `math.PI`
  - `import "web:webgl"` → `webgl.WebGLRenderingContext`
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
  - `import "web:webgl?flat"` + `import "web:fetch?flat"` →
    `web.WebGLRenderingContext`, `web.Response`.
  - Collision risk is real (two packages exporting the same name
    under one scheme conflict). Opt-in for that reason. **When
    `?flat` produces a name collision, the compiler reports a
    hard error at the second `import` statement and aborts
    compilation.** The diagnostic points at the URI literal of
    the second import and names the prior package that
    contributed the colliding identifier. No use-site
    unbound-name diagnostics are emitted for the colliding name;
    compilation does not proceed past the resolver. The user
    resolves it by renaming upstream or switching one of the
    imports off `?flat`.

**Combining any two of `?local`, `?nested`, `?flat` in one URI is a
compile error**; the resolver reports which flag pair is invalid.

**Identifier hygiene.** Package names use underscores, matching
the file naming convention (FR2): `import "std:typed_arrays"` binds
as `typed_arrays`, `import "std:typed_arrays?nested"` binds as
`std.typed_arrays`. For `std:*` and `web:*` pseudo-packages there
is no `-` → `_` substitution because hyphens never appear in their
URIs. (Third-party npm package names *can* contain hyphens and will
need a `-` → `_` substitution when computing the binding name; that
substitution rule is part of the third-party workstream and is out
of scope here.) `?flat` drops the package name from the user-visible
binding, but internal bookkeeping (tracking whether a given file's
imports have already pulled in a package's declarations) uses the
pseudo-package's full URI as the key — e.g. `web:fetch` —
regardless of which binding-shape flag the
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
`std:regexp` (→ `RegExp`), `std:symbol` (→ `Symbol`),
`std:object` (→ `Object`), `std:function` (→ `Function`),
`std:date` (→ `Date`), `std:map` (→ `Map`), `std:set` (→
`Set`), `std:weak_ref` (→ `WeakRef`). Each per-class package's
`?local` binding is its class. `Promise` is **not** in this
list — it lives in `std:async` (bundled, multiple top-level
classes), so under `?local` the access is `async.Promise.all(…)`
rather than bare `Promise.all(…)`.

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

- `web/canvas.esc` needs `import "web:dom"` (in some binding shape)
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
to imports between `std:*`, `web:*`, and (reserved) `node:*`
packages. The pseudo-package layer naturally contains cycles:
`std:async` (which owns `Promise<T>`) references `Iterable<T>`
from `std:iterator`; `std:iterator` may reference `Promise<T>`
from `std:async` for protocol overlap; `Error` subclasses in
`std:error` may reference types declared in `std:array` or
`std:string`; etc. These cycles are purely **type-level** and
**runtime-erased** — the JavaScript runtime exposes all of these
as pre-existing globals (`Promise`, `Symbol`, `Error`, …), so
there is no runtime initialization order to worry about. The
checker accepts cycles within the pseudo-package layer; the
resolver/dep-graph code must special-case the `std:`, `web:`, and
`node:` schemes to skip cycle reporting for imports whose source
*and* target both live under these schemes.

User code → pseudo-package imports remain acyclic (user code can
depend on pseudo-packages but pseudo-packages do not depend on
user code), and cycles among user packages remain disallowed as
before.

### FR7. DOM packaging; cross-package type references (open augmentation deferred)

The pseudo-package partition needs to handle two distinct
shapes of cross-package coupling: **DOM-internal coupling** (the
tight web of `Document`, `Element`, `Event`, HTML/SVG/MathML
element classes, CSSOM, observers, …) and **standalone web APIs
coupled to DOM only at the edges** (`web:fetch` returns a
`Response.body` of type `ReadableStream`; `web:webgl`'s context
is obtained from an `HTMLCanvasElement`; etc.).

**MVP design — single `web:dom` package + standalone web siblings.**

- The **entire DOM tree** — Document, Element, Node, Window,
  Navigator, every HTML element class, SVG, MathML, CSSOM,
  XML/XPath/parsing, selection, range, history, navigation,
  input/pointer/keyboard/touch events, drag-and-drop,
  observers (Intersection/Resize/Mutation), Web Animations,
  custom elements, fullscreen, picture-in-picture, view
  transitions — lives in a **single `web:dom` package**. All
  the registry interfaces (`HTMLElementTagNameMap`,
  `SVGElementTagNameMap`, `MathMLElementTagNameMap`,
  `HTMLElementEventMap`, `SVGElementEventMap`,
  `MathMLElementEventMap`, …) are closed and declared in
  `web:dom` alongside the methods that key on them
  (`createElement`, `createElementNS`, `addEventListener`).
- **Standalone web APIs** — families that ship in
  `lib.dom.d.ts` but have no DOM coupling — split into sibling
  `web:*` packages: `web:fetch`, `web:streams`, `web:crypto`,
  `web:workers`, `web:webgl`, `web:web_audio`, `web:web_rtc`,
  `web:web_codecs`, `web:indexeddb`, `web:service_worker`,
  `web:websocket`, `web:storage`, `web:url`, `web:encoding`,
  `web:file`, `web:performance`, `web:webauthn`,
  `web:payments`. (Final list in [implementation_plan.md §6.1](implementation_plan.md#61-partition-table).)

**Why a single `web:dom` package.** Earlier drafts split DOM
into per-element-family packages (`web:svg`, `web:mathml`,
`web:canvas`, etc.) with cross-package augmentation activated
per-importing-file (the original FR7 + FR9). The
implementation-plan §4.1 spike
([internal/checker/tests/spike_aug_test.go](../../internal/checker/tests/spike_aug_test.go))
showed that achieving per-file activation would require two new
checker subsystems: per-file composition of contributing
interface fragments + call-site re-resolution of `keyof T` /
`T[K]` over the merged view. Neither is a small extension of
existing code.

Collapsing the DOM tree into one package sidesteps the problem
entirely. `createElementNS` stays a single overloaded method on
`Document` with its NS-keyed signatures declared once, exactly
matching WebIDL. `SVGElement` etc. are reachable from
`HTMLElement` via `Element.parentNode` and event-target shared
behavior, so the cohesion is real — the DOM is not a clean
partition along element-family lines. Per-element-class
discoverability is preserved by the FR16 auto-import quick-fix
and adaptive diagnostic rendering (FR15), which surface the
owning package on demand.

```escalier
// In web:dom — registries declared closed, alongside the methods
fn createElement<K: keyof HTMLElementTagNameMap>(
    self, tag: K
) -> HTMLElementTagNameMap[K]

fn createElementNS<K: keyof SVGElementTagNameMap>(
    self, ns: "http://www.w3.org/2000/svg", qualifiedName: K
) -> SVGElementTagNameMap[K]
// ... plus MathML, XHTML, and the generic fallback overload

interface HTMLElementTagNameMap {
    canvas: HTMLCanvasElement,
    div: HTMLDivElement,
    // ... every HTML tag
}

interface SVGElementTagNameMap {
    circle: SVGCircleElement,
    path: SVGPathElement,
    // ... every SVG tag
}
```

**Cross-package type references via qualified names.** Sibling
`web:*` packages that need to mention `web:dom` types do so by
qualified name through their imported namespace. No augmentation
machinery involved:

```escalier
// In web:webgl
import "web:dom"

declare fn getContext(
    canvas: web.dom.HTMLCanvasElement,
    contextId: "webgl"
) -> WebGLRenderingContext
```

The same shape works between any two pseudo-packages: `web:fetch`
references `web.streams.ReadableStream` for `Response.body`;
`web:service_worker` references `web.fetch.Request` /
`web.fetch.Response`. Inter-package import cycles between
pseudo-packages are permitted (FR6) when needed for these
references.

**No `override declare`.** Builtins are declared directly in
their own `.esc` files. The `override declare` syntax is **not**
used for any builtin `.esc` file — that syntax is reserved for
the third-party workstream's override mechanism and does not
apply to builtins or pseudo-packages.

**What's deferred until a follow-up workstream un-punts FR7.**
With the single-`web:dom` partition no augmentation primitive
is needed for MVP. The augmentation work resurfaces only for:

1. **User-side custom-element registration** — TS-style
   `declare module "web:dom" { interface
   HTMLElementTagNameMap { "my-widget": MyWidget } }`. Users
   must type custom-element results manually until this work
   resumes.
2. **Third-party packages extending DOM/event maps.** Out of
   scope for the builtins workstream regardless; would be a
   third-party concern.

When the un-punt happens, the §4.1 spike findings size the
work: per-file composition layer + call-site re-resolution of
`keyof T` / `T[K]` over the merged view. The spike scaffolding
stays committed as a regression harness so future drift
breaks the build.

### FR8. Well-known symbol re-exports

All well-known symbols (`Symbol.iterator`, `Symbol.asyncIterator`,
`Symbol.match`, `Symbol.toPrimitive`, …) are declared directly on
`Symbol`'s static side in `std:symbol`. There is **no Symbol
augmentation mechanism**; domain-package aliases (below) are
plain re-exports, not augmentations.

**Shape-loaded language-level use vs. explicit references.**
Language-level use of well-known symbols (the `for-of` desugaring
referencing `Symbol.iterator`, etc.) goes through the checker's
**shape knowledge** and does not require any import. Explicit
references via `Symbol.<name>` require importing `std:symbol`.

**Domain-package aliases.** Each domain package re-exports the
well-known symbol(s) it cares about under a short package-local
name, so a file that needs the symbol does not have to import
`std:symbol` separately. The re-export is a plain
`export declare val` whose `@js` decorator points at the
canonical `Symbol.<name>` expression — same runtime value, two
spellings; a file picks whichever reads better:

```escalier
import "std:iterator"

class Range {
    [iterator.iteratorKey]() {
        // ... implement the iterator protocol
    }
}
```

vs. the dual-import form:

```escalier
import "std:symbol"

class Range {
    [Symbol.iterator]() {
        // ...
    }
}
```

**Naming convention.** Every domain-package re-export uses the
verbose `<package>.<name>Key` form, where `<name>` is the
symbol's ECMAScript spelling. `iterator.iteratorKey`,
`async.asyncIteratorKey`, `regexp.matchKey`, `regexp.replaceKey`,
`regexp.searchKey`, `regexp.splitKey`, `regexp.matchAllKey`. No
abbreviated `<package>.key` form for singletons: future
spec additions that add a second well-known symbol to a
currently-single-symbol package would otherwise force a rename
of `key` → `<name>Key`, breaking every importer. Verbose
uniformly costs a few characters; the rename cliff is real.

The name on the `Symbol` static side keeps its standard ECMAScript
spelling (`Symbol.iterator`, `Symbol.match`, …); the package-local
alias is a re-export with a different binding name, not a separate
symbol.

The re-export aliases are visible only when the importing file
imports the domain package; this is plain Escalier import scoping.

### FR9. Augmentation activation semantics (deferred with FR7)

**MVP: N/A.** FR9 specified per-importing-file activation rules
for cross-package augmentation. With FR7 deferred (single
`web:dom` package + standalone web siblings adopted instead),
there is no augmentation mechanism to scope — a file sees a name
iff it imported the package that owns it, which is plain
Escalier import scoping covered by FR3 / FR4.

The original FR9 rules — per-importing-file scoping, transitive
propagation via explicit re-export, independence from the
binding-shape flag — are preserved verbatim in the [appendix](#appendix-deferred-fr9-spec)
at the end of this document as the spec for the deferred work.
When/if FR7 is un-punted (custom-element support; third-party
DOM extension), those rules are what the augmentation mechanism
must satisfy.

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
   files under `std/` or `web/`, per the partition specified in
   FR1 (e.g. `Array<T>` → `std/array.esc`; `Promise<T>` →
   `std/async.esc`; `Error` family → `std/error.esc`; etc.). The
   partition is driven by a hand-maintained mapping table in the
   converter, not by anything in the `.d.ts`.

   **Unmapped-symbol fail-safe.** Any top-level TS-lib
   declaration name absent from both the partition table and
   the explicit drop list (`globalThis`, `eval`, `intrinsic`-
   typed declarations) is a **converter error**: the run aborts
   with a diagnostic naming the symbol, its source `.d.ts`
   file, and the partition-table file the contributor must
   edit. No "unmapped" catch-all package and no silent drop —
   contributor intent is required for every TS-lib symbol. The
   error is exercised by the routing code that emits per-package
   `.esc` output: the partition-table lookup is the choke point,
   and a missing entry surfaces there, not somewhere downstream.
5. Routes every DOM-coupled declaration — Document, Element,
   Node, Window, every HTML/SVG/MathML element class, every
   registry interface (`HTMLElementTagNameMap`,
   `SVGElementTagNameMap`, `MathMLElementTagNameMap`,
   `HTMLElementEventMap`, `SVGElementEventMap`,
   `MathMLElementEventMap`, …), CSSOM, observers, animations,
   custom elements, input events, etc. — into the single
   `web:dom` package per FR7. Standalone web APIs that ship in
   `lib.dom.d.ts` but have no DOM coupling (Fetch, Streams,
   Crypto, Workers, WebGL, Web Audio, WebRTC, WebCodecs,
   IndexedDB, Service Workers, WebSocket, Storage, URL,
   Encoding, File, Performance, WebAuthn, Payments, …) route
   to their own sibling `web:*` packages — the partition table
   in [implementation_plan.md §6.1](implementation_plan.md#61-partition-table)
   has the full mapping. No cross-package augmentation block
   is emitted. Well-known symbols
   (`Symbol.iterator`, `Symbol.asyncIterator`, …) are **not**
   routed — they stay on `Symbol`'s static side in
   `std/symbol.esc` (FR8). Domain-package re-export aliases
   (`iterator.iteratorKey`, `async.asyncIteratorKey`, …) are hand-authored at
   bootstrap, not emitted by the converter.
6. **Preserves JSDoc.** Leading JSDoc comments attached to TS-side
   declarations carry through to the emitted Escalier declaration
   as doc comments. This is a pass-through, not a transform — most
   of the prose value in `lib.web.d.ts` is JSDoc, and reusing it
   gives the generated `.esc` files documentation parity with the
   TS source at zero authoring cost. A small set of TS-specific
   tags may need stripping or rewriting (`@override` dropped,
   `@param` syntax touched up where Escalier differs); the rest
   pass through verbatim. `internal/dts_parser/` must attach
   leading JSDoc to declaration AST nodes if it does not already.
7. **Emits `export` and an `@js` decorator on every top-level
   declaration.** Pseudo-package members must be exported (FR3
   `export` rule), and exported declarations carry an `@js`
   decorator whose argument is the JS expression the declaration
   lowers to (see FR3 lowering paragraph): class declarations get
   `@js("<ClassName>")`; free functions inside a `declare namespace`
   block get `@js("<Namespace>.<fn>")` after the namespace
   flattening of step 2; declarations hoisted from the global
   scope into a partition package (e.g. `parseInt` →
   `std:number`) get `@js("<bare name>")`. The canonical emitted
   shape is `@js("...") export declare <kind> ...`.
   Hand-authored declarations not present in any `.d.ts` (Symbol
   re-exports, `Symbol.customMatcher`) are written with explicit
   `export` and `@js` at bootstrap.
8. Renders each output file via the (now-validated) declaration
   printer and `internal/type_system/print_type.go`.
9. Supports two re-run modes against a committed `.esc` tree:
   - **Write mode (additive).** Adds declarations present in the
     `.d.ts` but missing from the committed `.esc` (new TS-lib
     symbols since the last bump), and adds members on existing
     classes / interfaces that the `.d.ts` has but the `.esc` is
     missing. **Never overwrites** an existing declaration's body,
     signature, or hand-added annotations — hand-edits are sticky.
     Reports declarations present in the `.esc` but absent from the
     `.d.ts` (likely TS-side removal) as informational; no automatic
     deletion.
   - **`--check` mode (read-only).** Verifies that the committed
     `.esc` is compatible with the `.d.ts`: fails on missing
     declarations, on function/method signatures whose param or
     return types are not assignable to/from the `.d.ts` original
     (per the checker's assignability rules), and on the same
     incompatibility for properties on classes/interfaces. Adding
     `throws`, lifetimes, mutability, or narrowing a parameter /
     return type within the `.d.ts` shape is **not** incompatible
     and does not trip `--check`; the compatibility check is
     one-directional (Escalier-side may be stricter than TS-side,
     not looser). Intended for CI on TS-version bumps.

**One-time seeding.** After the initial bootstrap commit, the
pseudo-package `.esc` files are first-class Escalier source. Hand-edits add `throws`, lifetimes, and any
structural refinements — written inline like `self` and `mut self`,
no merge layer. JSDoc is *not* in the hand-edit list because it is
carried through automatically by step 6 above; manual editing of
JSDoc is only for places where the upstream comment is wrong,
incomplete, or Escalier-specific behavior diverges. Re-running the
converter is **never a wholesale regeneration**: write mode is
strictly additive (new declarations / new members only) and
`--check` is read-only. Existing declaration bodies, signatures, and
hand-added annotations are never overwritten.

**`throws` annotations are hand-curated for now.** Scraping MDN is
tempting but the failure modes (prose-not-data extraction,
brittleness, copyleft licensing of MDN content) outweigh the wins.
The realistic sources of `throws` data are:

- **WebIDL `[Throws]` extended attributes** for DOM APIs (DOM
  specs publish machine-readable IDL; `@webref/idl` ships curated
  extracts). Plausible future automation lever for `web:*`
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
`tools/dts_to_esc/ --check` against the bumped TS. The
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
   - `async fn` / `await` / `for await x of xs` → `std:async`
     (one package covers Promise, Awaited, and async iteration).
   - `for x of xs` / generators → `std:iterator`.
   - An explicit reference to a `std:error` class name
     (`Error`, `TypeError`, `RangeError`, `SyntaxError`,
     `ReferenceError`) inside a `try` / `catch` / `throw`
     expression or a function's `throws` clause →
     `std:error`. A bare `try` / `catch` / `throw` /
     `throws` with no error-class name does **not** trigger
     shape-loading — `throw "string"` and `throws` clauses
     listing only user-defined errors leave `std:error`
     un-loaded.

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
as source going forward** (re-running the converter is additive
only — write mode adds new declarations / members from upstream TS,
`--check` verifies compatibility, and neither mode overwrites
existing bodies, signatures, or hand-added annotations). JSDoc is
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
write `Awaited<T>` in `std:async` as the recursive conditional
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
   (`import "std:async"`, `import "std:math"`, …).
2. For single-class shortcut packages (FR5): leaves the bare
   reference unchanged, since the import binding *is* the class
   name in its capitalized form. `Array.isArray(...)` typed
   without an import → quick-fix adds `import "std:array"` and
   leaves the reference as-is; same for `Date.now`,
   `Error(...)`, etc.
3. For non-shortcut packages: rewrites the bare reference to
   qualify it through the resulting namespace binding. A bare
   `sin(x)` triggers `import "std:math"` and rewrites the call
   to `math.sin(x)`. A bare `Promise.all([...])` triggers
   `import "std:async"` and rewrites to `async.Promise.all([...])`
   (Promise lives in `std:async` and is not eligible for the
   single-class shortcut — see FR5).

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
- **Soundness of activation.** Under the single-`web:dom`
  partition (FR7), this reduces to "a file sees a name iff it
  imported the package that owns it" — plain Escalier import
  scoping, no spooky action from a sibling module. (If FR7 is
  later un-punted for custom-element support, the per-file
  augmentation rules in the deferred-FR9 appendix govern.)
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
- **`--explain-type` diagnostic.** When a tag-keyed return is
  unexpected (e.g. `createElement("does-not-exist")` rejects
  because the tag isn't a key of `HTMLElementTagNameMap`), the
  diagnostic surfaces the closed registry contents and suggests
  the closest valid tag — and, when an HTML tag is misused with
  `createElement` instead of an SVG-specific `createElementNS`
  call, points at the corresponding `createElementNS(svgNS, …)`
  signature (also in `web:dom`). For names that resolve in a
  sibling `web:*` package the user has not imported, suggests
  the missing `import "web:<pkg>"`. Complements the FR16
  auto-import quick-fix.

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
   `web:` (and reserved `node:`) prefixes to the resolved stdlib
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
     (`web:a?flat` + `web:b?flat`) merges into one `web` binding;
     mutually-exclusive flag combos error.
3. **Single `web:dom` partition + inter-package imports.** Build
   the FR7 MVP: the entire DOM tree lives in `web:dom` with all
   its registries (`HTMLElementTagNameMap`,
   `SVGElementTagNameMap`, …) declared closed inside the same
   package; standalone web APIs occupy sibling `web:*` packages
   that reference `web:dom` types via qualified names. Two
   pre-step deliverables inform this step:
   - The §4.1 augmentation spike (already landed under
     [internal/checker/tests/spike_aug_test.go](../../internal/checker/tests/spike_aug_test.go))
     answered "can existing checker machinery support per-file
     activation?" with **no/snapshot**, which is what motivated
     the single-`web:dom` partition. The scaffolding is kept as
     a regression harness — if a future change quietly enables
     cross-package augmentation, those assertions fail loudly
     and the FR7-defer decision can be revisited.
   - **Gate:** a hand-authored fixture declares
     `HTMLElementTagNameMap` populated with at least two
     entries in `web/dom.esc`; `createElement("canvas")`
     narrows to `HTMLCanvasElement`,
     `createElement("does-not-exist")` errors. A second
     fixture exercises an SVG overload of `createElementNS`
     on the same Document (verifying that NS-keyed overloads
     of one method co-located in `web:dom` resolve correctly).
     A third fixture exercises a cross-package type reference
     (`web:fetch` API parameter typed as
     `web.streams.ReadableStream`). Inter-package import
     cycles between two `std:*` packages are accepted by the
     resolver.
4. **Converter MVP.** Build `tools/dts_to_esc/` against a tiny
   slice (`Boolean` alone, ~10 lines of `.d.ts`). Emit to stdout.
   No file layout, no partition logic.
   - **Gate:** output round-trips through the parser and reads
     naturally to a human.
5. **Converter productionization.** Add the package-partition
   table (which TS-lib declaration goes into which `std:*` / `web:*`
   package per FR1/FR2), full output paths under
   `internal/interop/data/`, `--check` mode, and the full
   `lib.*.d.ts` set as input. The converter must route every
   DOM-coupled declaration (Document/Element/Node and the
   HTML/SVG/MathML/CSSOM/event/observer/animation/custom-element
   surfaces hanging off them) into `web:dom`, and route the
   standalone web families (Fetch, Streams, Crypto, Workers,
   WebGL, Web Audio, WebRTC, WebCodecs, IndexedDB, Service
   Workers, WebSocket, Storage, URL, Encoding, File,
   Performance, WebAuthn, Payments) into their sibling
   `web:*` packages (per FR7 MVP).
   - **Gate:** every output file parses; running the converter
     twice produces byte-identical output.
6. **Stdlib bootstrap.** Run the converter, review the output,
   hand-edit obvious mis-classifications and high-value `throws`
   annotations across the generated `std/` and `web/` packages,
   commit.
   - **Gate:** humans review the committed files.
7. **Internal fixture migration.** Update Escalier's own fixtures
   and tests that relied on previously-ambient symbols (`Math`,
   `JSON`, `console`, `Promise`, `Error`, `Array.from`,
   `parseInt`, …) to import the corresponding pseudo-package. The
   auto-import quick-fix (FR16) is expected to be available before
   this step lands so internal migration exercises the same
   tooling external users will rely on. This step must precede
   step 8: the next step deletes the legacy lib-walking path, and
   un-migrated fixtures would regress.
8. **Prelude switchover + legacy deletion (single PR).** Replace
   the lib-walking path in `internal/checker/prelude.go` with the
   lazy per-file shape loader (FR11), and delete the legacy
   builtin / override machinery in the same change:
   `BuildBuiltinStore`, `loadGlobalDefinitions`,
   `populateSelfParams`, `UpdateMethodMutability`,
   `mergeReadonlyVariant`, the `mutabilityOverrides` map, and any
   `internal/interop/data/builtins/` overrides subtree. No build
   flag, no parallel paths, no deprecation cycle — Escalier is
   pre-1.0.

(Steps 10 and 11 from the original proposal's migration path —
third-party lazy cache and deletion of the runtime interop
pipeline — are intentionally omitted; they belong to the
third-party workstream. The steps above have been renumbered
sequentially within this workstream; the original step numbering
no longer applies.)

Steps 6–7 are authoring + migration paced by human review of
generated files. Step 8 is the cut-over. This document
deliberately avoids time-based effort estimates; complexity
(PR-sized vs needs-splitting) is the only estimate that matters
per step.

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
- **Cross-package augmentation mechanism.** *Resolved by MVP
  punt.* The §4.1 spike confirmed that per-file augmentation
  with FR9 semantics would need two new checker subsystems
  (per-file composition + call-site `keyof T` / `T[K]`
  re-resolution); MVP adopts closed registries (FR7) instead.
  Risk re-emerges only when custom-element support is added.
- **`web:dom` is one large package.** Under the MVP partition,
  every DOM-coupled declaration (Document, every HTML / SVG /
  MathML element class, CSSOM, observers, events, etc.) lives
  in `web:dom`. The committed `.esc` file is large — tens of
  thousands of lines. Mitigations: (a) one `import "web:dom"`
  gets the whole DOM surface, so users do not face per-feature
  import churn; (b) FR15 adaptive diagnostic rendering surfaces
  the owning package name on demand; (c) FR16 auto-import
  quick-fix points at `web:dom` for any unresolved DOM-coupled
  reference. Editor performance against a single large `.esc`
  file is the realistic risk; if it becomes a problem the file
  can be split into multiple `.esc` files under
  `internal/interop/data/web/dom/` that the loader concatenates
  into one package namespace (no language-level change).
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
  in `std:async` references `Iterable<T>` from `std:iterator`;
  `Array<T>` in `std:array` references the iteration protocol; etc.
  The existing module
  loader handles this for user code; just need to confirm it works
  for shape-loaded `std:*` packages. Probably not an issue.

## Error-message taxonomy

This workstream introduces several new failure modes at the
resolver / parser boundary. Each gets a distinct, actionable
diagnostic:

- **Unknown scheme.** `import "foo:bar"` where `foo` is not one
  of the recognized schemes (`std`, `web`, reserved `node`).
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
  scheme contributing the same identifier. **Hard error** at the
  second import (per FR4): message names the colliding identifier
  and the two source packages, points at the URI literal of the
  second import, and instructs the user to rename upstream or
  switch one of the imports off `?flat`.

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
- **Resolver tests.** End-to-end resolution of `std:`, `web:`,
  `node:` (reserved → clear error) schemes; unknown scheme; known
  scheme + unknown package; `?flag` stripping before path lookup.
- **Binding-shape tests** (per FR4). Fixture per shape (`?local`,
  `?nested`, `?flat`), per single- and multi-package case,
  including `?flat` collision error.
- **Closed-registry tests** (per FR7 MVP). Fixture where
  `web/dom.esc` declares `HTMLElementTagNameMap` with at least
  two entries (`canvas: HTMLCanvasElement`,
  `div: HTMLDivElement`) plus `createElement(tag)`.
  `createElement("canvas")` narrows to `HTMLCanvasElement`;
  `createElement("does-not-exist")` errors with a typed-key
  diagnostic.
- **`createElementNS` NS-keyed overload tests** (per FR7 MVP).
  Same `web/dom.esc` fixture also declares
  `SVGElementTagNameMap` with `circle: SVGCircleElement` and
  the `createElementNS<K: keyof SVGElementTagNameMap>(ns:
  "http://www.w3.org/2000/svg", qualifiedName: K)` overload.
  `document.createElementNS(svgNS, "circle")` narrows to
  `SVGCircleElement`; verifies that NS-keyed overloads of a
  single method co-located in `web:dom` resolve correctly
  against the literal NS string.
- **Cross-package type reference tests** (per FR7 MVP). A
  package `web:fetch` declares `Response.body` returning
  `web.streams.ReadableStream` via qualified type reference.
  Importing file uses the returned stream; type-checks.
- **Inter-package cycle tests** (per FR6). Two `std:*` packages
  with a mutual import are accepted by the resolver; the same
  shape between two user packages errors.
- **Spike regression harness** (per FR7's deferred portion).
  [internal/checker/tests/spike_aug_test.go](../../internal/checker/tests/spike_aug_test.go)
  pins the observed current behavior (interfaces in two
  separate packages do not merge; `?flat` collisions error;
  call-site `keyof T` snapshots at declaration time). If a
  future change quietly enables cross-package augmentation,
  these assertions fail loudly so the FR7-defer decision can
  be revisited.
- **Symbol re-export tests** (per FR8). Verify that
  domain-package aliases (`iterator.iteratorKey`, `regexp.matchKey`, …)
  refer to the same runtime value as `Symbol.<name>` via the
  `@js("Symbol.<name>")` decorator; verify that a class
  implementing `[iterator.iteratorKey]()` is recognized as iterable by
  `for-of` (importing `std:iterator` alone, without
  `std:symbol`).
- **Prelude switchover.** Internal fixtures are migrated to
  `import "std:*"` (migration step 7) before the legacy path is
  deleted (step 8); the test suite passes on the new path alone
  after step 8. No parity check against the legacy path: it is
  deleted in the same PR rather than kept behind a flag (pre-1.0).
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

## Backwards-compatibility

**Not applicable — Escalier is pre-1.0.** There are no released
versions and no external users to migrate, so this workstream
does not need a deprecation cycle, a build flag, or a parallel-
paths window. Migration step 8 swaps the prelude and deletes the
legacy builtin / override machinery in one PR; migration step 7
(internal fixture migration) lands immediately before so the
test suite stays green across the cut.

The ergonomics features below are still hard requirements — not
as migration aids, but because the new model genuinely needs
them for day-to-day use:

- **Diagnostic-assisted unbound-name suggestions.** When a name
  matching a known pseudo-package export is referenced without
  an import, the unbound-name diagnostic suggests "did you mean
  to `import \"std:async\"`?". The suggestion list is derived
  mechanically from the resolved stdlib data directory. This is
  the fallback for command-line use; users in a supported editor
  get the FR16 quick-fix instead.
- **Auto-import quick-fix (FR16).** The LSP turns each unbound-
  name diagnostic into a one-keystroke fix that adds the
  namespace import and rewrites the bare reference to the
  qualified form.

No automatic codemod for user code ships with this workstream.

## Appendix: deferred FR9 spec

Preserved verbatim from the pre-MVP-punt version. Not in force
under the current single-`web:dom` partition (no augmentation
mechanism exists to scope). Reactivates if/when FR7 is un-punted
for custom-element support or third-party DOM extension —
whatever augmentation mechanism lands at that point must satisfy
these rules.

The scoping rules below applied to DOM-style registry
augmentation (FR7). Any future augmentation mechanism added under
this workstream inherits the same rules. (FR8's domain-package
re-exports of well-known symbols are **not** augmentations —
they are plain Escalier exports governed by ordinary import
scoping, not by these rules.)

**Per-importing-file scoping.** Augmentation activation is
**scoped to the importing module**, not the whole compilation
unit. A file that imports `web:canvas` sees the augmented
`HTMLElementTagNameMap` entry for `canvas`; a sibling file in the
same program that does not import `web:canvas` does not. This
deliberately departs from TypeScript's `declare module` behavior,
where any module-augmenting import becomes global. The type a
file sees is fully determined by that file's own imports.

**Note for library authors.** Per-importing-file scoping means a
library function that calls `document.createElement("canvas")`
must itself include `import "web:canvas"` — relying on the
application to import the package is not enough. Each file that
*uses* the augmented shape needs the import; importing only in
the application's entry point gives the entry-point file the
narrow type but leaves the library file with the pre-augmentation
shape. The same applies to every other augmentation entry point
(SVG element families, etc.). When in doubt, import the augmenting
package in every file that consumes the augmented type. This is
the inverse of the TypeScript habit of relying on a global
side-effect import.

**Transitive propagation via explicit re-export.** Augmentations
propagate only through **explicit re-exports** of the pseudo-package.
If file A writes `export * from "web:canvas"` (or an equivalent
named re-export of canvas's bindings), then a file importing A
sees canvas's augmentations as part of importing A. A plain
`import "web:canvas"` in A that does not re-export anything from
canvas does *not* leak the augmentation to A's importers. Simply
importing a package never magically re-exports it.

**Independent of the binding-shape flag.** Importing `web:canvas`
under any of `?local`, `?nested`, or `?flat` adds canvas's
contributions to that file's view of `HTMLElementTagNameMap`. The
flag only affects how canvas's *direct exports* land in the
importing scope, not which augmentations are activated.
