# Proposal: Small ambient builtins, pseudo-package imports, converter-fed third-party deps

**Status:** Historical draft, **superseded**. The builtins
workstream split out into [planning/builtins/](../builtins/),
and that workstream's [requirements.md](../builtins/requirements.md)
is authoritative for the builtins side. In particular,
**[FR1 (no ambient set)](../builtins/requirements.md#fr1-no-ambient-set-shape-loaded-vs-named-bindings)**
of the builtins requirements rejects the "small ambient builtins"
section of this proposal: there is no ambient tier in the adopted
design — `Symbol`, `RegExp`, `Object`, `Function`, error classes,
etc. all live in `std:*` pseudo-packages and are either
shape-loaded (invisible, language-feature-driven) or named via
explicit `import "std:*"`. The pseudo-package model from this
proposal carried forward; the ambient-set proposal did not. This
file is retained for historical rationale only.

**Companion to:** [implementation_plan.md](implementation_plan.md),
[requirements.md](requirements.md),
[override_merge_semantics.md](override_merge_semantics.md).

## Motivation

The current §6 plan layers Escalier-authored `.esc` *overrides* on top
of TypeScript's `lib.es*.d.ts` and bundled `@types/*` packages. Mutability
(and later: lifetimes, `throws`) live in the override file; the
*structure* of each type comes from the upstream `.d.ts` at startup. To
make this work, the compiler carries:

- A seven-tier classification ladder in
  [internal/interop/mutability.go](../../internal/interop/mutability.go)
  (`Classify`) that decides receiver mutability when no explicit
  signal is present.
- A merge pipeline in [internal/interop/merge.go](../../internal/interop/merge.go)
  that combines original + override into an effective type, with
  per-tier collapse, member-presence rules, and overload semantics
  ([override_merge_semantics.md](override_merge_semantics.md)).
- Trio-fusion logic in
  [internal/interop/class_shapes.go](../../internal/interop/class_shapes.go)
  to recover that `interface Foo` + `interface FooConstructor` +
  `declare var Foo: FooConstructor` is "really" one class.
- A `Readonly*`-variant merger in
  [internal/checker/prelude.go](../../internal/checker/prelude.go)
  (`mergeReadonlyVariant`) that uses `ReadonlyArray`/`ReadonlyMap`/`ReadonlySet`
  as positive non-mut signals for the matching mutable type.
- A §6.D consistency test (planned) that re-parses every override's
  matching `.d.ts` declaration on every CI run to guard against drift.
- A bootstrap cycle for §6.B: the prelude needs lib globals before it
  can type-check builtin overrides, but the overrides are part of
  what the prelude installs. The plan notes the caller "must supply a
  checker," without yet specifying how.

Most of this complexity exists *because* Escalier doesn't own the
builtins' shape — it consumes TypeScript's, then patches it. The
patching also doesn't scale gracefully past mutability: `throws` in
particular has no good heuristic, so the override file would end up
covering almost every method. At that point "baseline + override"
stops being a useful split.

A second motivation, surfaced during proposal review: TypeScript
treats *everything globally available in any environment* as
ambient. A browser-targeted program type-checks the full DOM
surface — thousands of types — even when it never touches a `Canvas`
or a `WebRTC` peer connection. A Node program type-checks DOM types
that don't exist at runtime. Escalier can do better by drawing a
tighter line around what's truly ambient and putting the rest behind
imports.

## Proposal in one sentence

**Builtins:** a small, tight ambient set (primitives, syntax-produced
types like `Array`/`Promise`/`RegExp`, `Error` family, iterators,
utility types) lives in one `.esc` file under `internal/interop/data/`
and is always loaded. **Everything else** — `Math`, `JSON`, `console`,
typed arrays, `Date`, `Map`, `Intl`, DOM APIs, etc. — lives in
**pseudo-packages** under `std/` and `web/` and is brought in with
URI-scheme imports like `import "std:math"`, which by default adds
`math` to the local scope as a namespace binding. Optional `?flag`
modifiers on the URI (e.g. `?nested` to bind under a scheme-named
namespace) tweak the binding shape per import.
**Third-party deps** still go through lazy on-first-compile
`.d.ts` → `.esc` conversion with the §5 baseline + override merge,
cached under `node_modules/.cache/escalier/`.

## Pseudo-package model

### URI-scheme imports

The grammar grows a new module specifier shape:

```escalier
import "std:math"        // binds `math`
import "std:json"        // binds `json`
import "std:console"     // binds `console`
import "std:date"        // binds `date`
import "std:intl"        // binds `intl`
import "web:canvas"      // binds `canvas`
import "web:http"        // binds `http`
```

The default `import "scheme:name"` form is a **local namespace
import**: the package is bound under a local identifier equal to the
last URI segment (lowercase), with all public declarations accessible
as fields — both values and types. Access is `math.sin(x)`,
`canvas.HTMLCanvasElement`, etc.

This default is deliberate. Each import introduces its own local
binding, so two imports from the same scheme (`import "web:canvas"`
and `import "web:webgl"`) don't risk name-collisions in any shared
namespace, and most `std:*` / `node:*` use cases want the terse local
form anyway. Cross-scheme, cross-package naming conflicts are isolated
to the importing module's local scope.

**Per-import flag modifiers.** The URI may carry one or more `?flag`
modifiers, mimicking the query-string convention from Vite/Parcel
build systems (`?raw`, `?url`, `?worker`):

```escalier
import "std:math?nested"           // binds `std.math` (scheme.package qualified)
import "web:canvas?local"          // explicit form of the default (redundant; equivalent
                                   // to no flag)
import "web:canvas?flag1&flag2"    // multiple flags separated by `&`
```

Two flags govern the **binding shape**; they are mutually exclusive
and exactly one is in effect per import (with no flag treated as
`?local`):

- `?local` (default) — bind the package's contents under a local
  identifier equal to the last URI segment. Each import is its own
  binding; no cross-import merging.
  - `import "std:math"` → `math.sin(x)`
  - `import "web:canvas"` → `canvas.HTMLCanvasElement`
- `?nested` — bind under a scheme-named namespace, with the package
  as a sub-namespace. Multiple `?nested` imports from the same scheme
  merge under disjoint sub-namespaces (one per package URI), so
  there's no collision risk.
  - `import "std:math?nested"` → `std.math.sin(x)`
  - `import "std:json?nested"` adds `std.json` alongside
  - Useful when a file imports several packages from one scheme and
    wants origins visible at every call site.

Combining `?local` and `?nested` in one URI is a compile error —
the resolver reports which flag pair is invalid. Future flags
(e.g. `?type-only`, `?lazy`) compose with the binding-shape flags
as appropriate but require their own per-flag compatibility rules.

The flag-syntax slot is intentionally extensible: new variants can be
added in future without grammar changes, only resolver work and new
per-flag compatibility rules. Multiple flags in one URI use `&` as
the separator (URL-query convention).

Recognized schemes:

- `std:` — ECMAScript standard library and proposed-but-shipping APIs
  (Temporal, WebAssembly). Available in every JS runtime.
- `web:` — Web platform APIs. Available only in browser-like
  environments.
- `node:` — Node.js standard library. Already a real Node.js scheme;
  Escalier just adopts it. (Deferred: actual `.esc` content for
  `node:*` packages comes later.)

Mapping is mechanical: `std:math` → `internal/interop/data/std/math.esc`
(content embedded into the compiler binary), `web:http` →
`internal/interop/data/web/http.esc`. The compiler's module
resolver recognizes the scheme prefixes and routes accordingly. The
`?flag` portion of the URI is stripped before path resolution; it
affects only how the loaded module's exports land in the importing
scope.

**Type-system-only, runtime-erased.** At runtime, `Math.sin`,
`console.log`, etc. are globally available in every JS environment.
The `import "std:math"` statement adds *type information* to the
compile-time scope; codegen erases the import line entirely.
Zero runtime cost. The "package" concept is purely a type-checking
grouping mechanism.

### What stays ambient

The "true builtins" set is small. Everything in it satisfies at least
one of:

- The language itself produces values of this type (literal syntax,
  operators, control flow desugaring).
- Primitive method dispatch references it (`"x".charAt(0)` needs the
  string prototype interface).
- Spec-mandated free global function (`parseInt`, etc.).
- Purely type-level utility used pervasively.

Concretely (one file, `internal/interop/data/builtins.esc`):

- **Primitive instance interfaces:** `String`, `Number`, `Boolean`,
  `BigInt` — instance methods only (so `"hello".charAt(0)`
  type-checks). The constructor / static side lives in
  `std:string`, `std:number`, etc.
- **Symbol (full).** Both the instance interface *and* the constructor
  with all well-known symbols (`Symbol.iterator`, `Symbol.asyncIterator`,
  `Symbol.toPrimitive`, …) are ambient. The ambient iterator protocol
  below references `[Symbol.iterator]` / `[Symbol.asyncIterator]` in
  its method signatures, so the well-known symbols can't be deferred
  to a pseudo-package without a bootstrap cycle. There is no
  `std:symbol` package.
- **Syntax-produced classes:** `Array<T>` (array literals),
  `Promise<T>` (async functions), `RegExp` (regex literals). Both
  interface and constructor stay ambient for these — the literal
  desugars to the constructor and the prototype is referenced
  directly. There is no `std:regexp` package; `RegExp` lives entirely
  in `builtins.esc`.
- **Iterator protocol:** `Iterator<T>`, `Iterable<T>`,
  `IteratorResult<T>`, `AsyncIterator<T>`, `AsyncIterable<T>`,
  `Generator<T, TReturn, TNext>`, `AsyncGenerator<T, TReturn, TNext>`
  — referenced by `for…of`, generators, and `await`. (See
  [lib.es2015.iterable.d.ts](../../node_modules/typescript/lib/lib.es2015.iterable.d.ts)
  for the upstream shapes. Note: there is no `IterableIterator` —
  any earlier mention in review notes was incorrect.)
- **Prototype roots (full):** `Object` and `Function`, including
  their static sides (`Object.keys`, `Object.assign`, `Object.entries`,
  `Object.freeze`, `Function.prototype.bind`, etc.). Keeping these
  ambient avoids forcing `import "std:object"` for code as routine as
  `Object.keys(o)`.
- **Error family:** `Error`, `TypeError`, `RangeError`,
  `SyntaxError`, `ReferenceError`, `EvalError`, `URIError`,
  `AggregateError` — referenced by `throw` / `catch` / `throws`.
- **Free globals:** `globalThis`, `parseInt`, `parseFloat`, `isNaN`,
  `isFinite`, `encodeURI`, `decodeURI`, `encodeURIComponent`,
  `decodeURIComponent`, `eval`.
- **Utility types:** `Partial`, `Required`, `Readonly`, `Pick`,
  `Omit`, `Record`, `Exclude`, `Extract`, `NonNullable`,
  `Parameters`, `ConstructorParameters`, `ReturnType`,
  `InstanceType`, `ThisParameterType`, `OmitThisParameter`,
  `ThisType`. (The intrinsic-backed ones — `Uppercase`, `Lowercase`,
  `Capitalize`, `Uncapitalize`, `NoInfer`, `Awaited` — are
  checker-resident, not in any file.)

Roughly 35–40 declarations.

### What moves to pseudo-packages

Everything else. Per-package granularity follows the natural
conceptual unit:

```
internal/interop/data/
    builtins.esc            # the ambient set above

    std/
        math.esc             # Math
        json.esc             # JSON
        console.esc          # console + Console interface
        string.esc           # StringConstructor (fromCharCode, raw, ...)
        number.esc           # NumberConstructor (parseInt, MAX_VALUE, ...)
        boolean.esc          # BooleanConstructor (mostly empty)
        bigint.esc           # BigIntConstructor (asIntN, asUintN)
        date.esc             # Date
        map.esc              # Map, WeakMap
        set.esc              # Set, WeakSet
        weak-ref.esc         # WeakRef, FinalizationRegistry
        typed-arrays.esc     # ArrayBuffer, SharedArrayBuffer, DataView,
                             # Int8Array..Float64Array, BigInt64Array,
                             # BigUint64Array
        reflect.esc          # Reflect
        proxy.esc            # Proxy
        intl.esc             # Intl namespace + all its inner classes
        temporal.esc         # Temporal (stage-3 proposal, shipping)
        wasm.esc             # WebAssembly

    web/
        core.esc             # Document, Element, Node, Window, basic events
        http.esc             # fetch, Request, Response, Headers, URL,
                             # URLSearchParams, Body
        canvas.esc           # CanvasRenderingContext2D, ImageData, Path2D
        webgl.esc
        webrtc.esc
        storage.esc          # localStorage, sessionStorage, IndexedDB
        workers.esc          # Worker, ServiceWorker, BroadcastChannel,
                             # MessageChannel
        media.esc            # HTMLMediaElement, MediaStream, MediaSource
        forms.esc            # HTMLFormElement, FormData
        # ... more web:* packages as needed
```

DOM splits are finer than `std/` because the DOM surface is so
much larger; a typical browser program touches maybe 2–3 `web:*`
packages out of dozens.

**Inter-package references.** Pseudo-packages that reference types
declared in other pseudo-packages must `import` them explicitly,
exactly like ordinary user code. `web/canvas.esc` needs
`import "web:core"` (in some binding shape) to extend `HTMLElement`;
`std/intl.esc` needs `import "std:date"` to reference `Date`. There
is no implicit "all sibling packages visible" rule. Ambient
declarations from `builtins.esc` (iterators, primitives, Symbol,
Promise, Array, the Error family, well-known symbols) are visible
everywhere without import.

`node:*` packages are deferred — when Node support lands, populating
`internal/interop/data/node/` follows the same model.

### Binding conventions

The default binding for `import "scheme:name"` (equivalent to
`?local`) is the lowercased last URI segment, used as a local
namespace identifier:

| Import statement | Default binding | Example access |
|---|---|---|
| `import "std:math"` | `math` | `math.sin(x)`, `math.PI` |
| `import "std:json"` | `json` | `json.parse(s)`, `json.stringify(v)` |
| `import "std:console"` | `console` | `console.log("hi")` |
| `import "std:date"` | `date` | `date.Date.now()`, `new date.Date()` |
| `import "std:intl"` | `intl` | `intl.NumberFormat(...)` |
| `import "web:canvas"` | `canvas` | `canvas.HTMLCanvasElement` |
| `import "web:http"` | `http` | `http.fetch(url)`, `http.Request` |

Package names may contain hyphens but identifiers can't — the
resolver substitutes `-` → `_` when computing the binding name. So
a hypothetical `web:web-rtc` would bind `web_rtc`. Same rule applies
to the per-package slot under `?nested`: `import "web:web-rtc?nested"`
would bind `web.web_rtc`.

The default keeps each import in its own local namespace — no
collisions across imports, terse access, and it's what `std:*` and
`node:*` use cases want almost all the time. Departures from JS
casing (e.g. `math` instead of `Math`) are intentional — Escalier
idiom is lowercase namespace identifiers; primitive type names
(`string`, `number`) and the value-namespace package binding
(`string` from `std:string`) live in separate namespaces, so there's
no conflict.

**No "promoted-class" shortcut.** A package containing a single
dominant class (`std:date` → `Date`, `std:regexp` would have been
`RegExp` had it not been kept ambient, etc.) does *not* re-export
that class's members at the namespace root. `date.now()` is **not**
valid; you write `date.Date.now()` for the static and
`new date.Date()` for construction. This keeps the namespace model
uniform — every imported binding is a plain namespace whose fields
are exactly the package's top-level declarations.

**Opting into a scheme-rooted nested namespace.** The `?nested` flag
binds under a scheme-named namespace with the package as a
sub-namespace. Multiple `?nested` imports from the same scheme merge
under disjoint sub-namespaces, with no collision risk because each
package keeps its own slot:

```escalier
import "std:math?nested"
import "std:json?nested"

// Both available under one `std` binding, origins visible at every call site:
std.math.sin(x)
std.json.parse(s)
```

This is useful when a file pulls in several packages from one scheme
and wants the origin spelled out at every call site, at the cost of
some extra typing.

**Named imports** (`import { sin, PI } from "std:math"`) are supported
as a syntactic convenience — they bind the named symbols directly
into the local scope, no namespace wrapping. Useful for pulling
specific symbols out of a package without bringing in the full
namespace, especially for packages like `web:http` where the natural
access pattern is bare names (`fetch(url)`).

**Named imports may not carry binding-shape flags.** Writing
`import { fetch } from "web:http?nested"` (or `?local`) is a
compile error. The binding-shape flags govern how a package's
*namespace* lands in the importing scope; named imports bypass that
namespace entirely, so the flag has nothing to act on. If a file
needs both named bindings and a namespace contribution, it must
write two separate `import` statements.

### Cross-package DOM type references

Splitting the DOM into feature packages surfaces a real problem:
many `web:core` APIs return or reference types that live in feature
packages. The canonical example is `document.createElement`:

```typescript
document.createElement("canvas")  // should return HTMLCanvasElement
```

`document` lives in `web:core` but `HTMLCanvasElement` lives in
`web:canvas`. If `web:canvas` isn't imported somewhere in the
compilation unit, the return type falls back to a coarser base
(`HTMLElement`); if it is, the return type narrows to
`HTMLCanvasElement`. The same pattern applies to `querySelector`,
`getElementsByTagName`, `addEventListener`'s event-typing,
`createElementNS`, and a handful of other APIs.

**Primary mechanism: open registry interfaces, augmented per package.**
TS lib already encodes the tag-string-to-element-type mapping as a
single open interface, `HTMLElementTagNameMap`. Core APIs are
written *once* against that registry:

```escalier
fn createElement<K: keyof HTMLElementTagNameMap>(
    self, tag: K
) -> HTMLElementTagNameMap[K]
```

`web:core` declares the registry with a minimal baseline (or empty).
Each feature package augments only the registry — `web:canvas` adds
exactly one line:

```escalier
override declare interface HTMLElementTagNameMap {
    canvas: HTMLCanvasElement,
}
```

…and every API that reads through the registry — `createElement`,
`querySelector`, `getElementsByTagName`, `createElementNS` — picks
up the new entry automatically. Same pattern for the parallel event
registries (`HTMLElementEventMap`, `WindowEventMap`, etc.) and the
SVG tag registry (`SVGElementTagNameMap`).

**Fallback: per-API augmentation.** Some cross-package APIs don't
fit a registry pattern — e.g. a `web:core` function that takes an
`HTMLCanvasElement` as a parameter rather than narrowing on a tag
string. Two cases here:

1. The API takes the type as a *parameter* (not a return type): the
   feature package just exports the type, `web:core` references it
   via the type's qualified name, no augmentation needed. This is
   ordinary cross-package type referencing — the same way any module
   uses types declared in another module.
2. The API is overload-keyed on a string but doesn't have a
   registry: the feature package augments the API's overload set
   directly. Rare; treat as a one-off when it comes up.

**Activation semantics.** Augmentation activation is **scoped to the
importing module**, not the whole compilation unit. A file that
imports `web:canvas` sees the augmented `HTMLElementTagNameMap`
entry for `canvas`; a sibling file in the same program that does not
import `web:canvas` does not. This deliberately departs from
TypeScript's `declare module` behavior, where any module-augmentation
import becomes global. The Escalier rule keeps the type a file sees
fully determined by that file's own imports — no spooky action from
a sibling — at the cost that each file using `createElement("canvas")`
must import `web:canvas` itself.

Augmentation activation is independent of the binding-shape flag.
Importing `web:canvas` (whether as the default `?local` or as
`?nested`) adds canvas's contributions to that file's view of
`HTMLElementTagNameMap` so `createElement("canvas")` narrows
correctly. The flag only affects how canvas's *direct exports* land
in the importing module's scope.

**Language requirements.** Two pieces of machinery this approach
depends on:

- *Open interfaces / interface merging across packages.* A feature
  package needs to add members to an interface declared in another
  package. Escalier's §5 override-merge code already does interface
  merging for type refinement; the question is whether the same
  mechanism can be repurposed for cross-package augmentation, or
  whether augmentation needs its own loader path. Worth confirming
  during the migration plan's resolver-extension step.
- *Indexed access over open registries.* `HTMLElementTagNameMap[K]`
  where `K extends keyof HTMLElementTagNameMap` needs to refresh
  when the registry is augmented. Escalier already has indexed
  access types (exercised in §8's audit); just confirming the
  open-keyof case behaves correctly under augmentation.

## Concrete changes

### Builtin bootstrap

Add a `tools/dts_to_esc/` directory with a Go binary that:

1. Reads the pinned TypeScript lib `.d.ts` set via the existing
   [internal/dts_parser/](../../internal/dts_parser/).
2. Converts each `.d.ts` declaration **directly into the
   corresponding Escalier declaration AST**, bypassing
   `type_system.Type` and the checker entirely. The TS-side AST node
   for `declare class Foo`, `interface Bar`, `declare function baz`,
   `type alias`, `declare namespace`, etc. maps mechanically to the
   matching Escalier declaration. This avoids the §6.B-style
   bootstrap cycle (checker needs prelude needs checker) — the
   converter is a pure AST-to-AST translator, no type resolution
   involved. `internal/interop/decl.go` / `extract.go` may be reused
   where they happen to do AST-level work, but anything that builds
   `type_system.Type` is not on this path.
3. Runs `interop.Classify` (tiers 3/5/6) at conversion time to seed
   receiver mutability directly into the emitted AST (e.g. deciding
   `self` vs `mut self` on each method).
4. **Partitions** the resulting declarations into:
   - The ambient set (per the list above) → `builtins.esc`.
   - Each pseudo-package's surface → that package's `.esc` file
     under `std/` or `web/`.
   The partition is driven by a hand-maintained mapping table in the
   converter ("here's where each TS lib symbol lives in our layout"),
   not by anything in the `.d.ts` itself.
5. Renders each output file via the now-validated
   [internal/type_system/print_type.go](../../internal/type_system/print_type.go)
   (per §8) plus a new declaration-level renderer.
6. Supports a `--check` mode that diffs generated output against the
   committed files without overwriting.

After the initial bootstrap commit, **the `.esc` files (ambient
and pseudo-package alike) are first-class Escalier source**. Hand-edits
add `throws`, lifetimes, JSDoc, and any structural refinements — written
inline like `self` and `mut self`, no merge layer involved. Re-running
the converter is a review tool (`--check`), not a build step — its
output is never auto-applied.

**TS version bump workflow.** Run `tools/dts_to_esc/regenerate
--check` against the bumped TS. The output is a diff against the
current files showing what TS added/removed/changed. A contributor
decides which changes to port by hand. An optional CI nudge can
annotate a PR with "TS lib changed since last bump" when the diff
exceeds some threshold.

### Always-current API, polyfills at lowering

The type checker always sees the modern surface — ES2024 methods
like `Promise.withResolvers` are present at type-check time even
when the codegen target is older. Runtime-version compatibility is
the codegen's job: when lowering to ES2015, the emitter inserts a
polyfill for any ES2016+ feature actually used. This matches how
TypeScript+target+Babel/swc work in practice and avoids putting
ES-version awareness into the type system.

Polyfill insertion is its own follow-up effort outside this doc's
scope. The proposal assumes only that it's tractable, which it is —
Babel and swc both demonstrate it.

### Third-party deps: lazy convert + baseline-plus-override

Third-party `.d.ts` (typically `@types/*` packages and packages
shipping their own `.d.ts`) go through a separate path because we
don't own them. When the Escalier compiler first encounters a dep
whose merged-cache entry is missing or stale, it:

1. Reads the dep's `.d.ts` via `internal/dts_parser/`.
2. Converts to a *baseline* AST using the same converter the builtin
   bootstrap uses (incl. `Classify` heuristics).
3. Merges the baseline against any present override fragments from
   `node_modules/<pkg>/overrides/*.esc` (tier 1a) and the user
   project's `overrides/*.esc` (tier 1b) via §5's existing `Merge`.
   Most deps will have *no* overrides; the baseline becomes the
   result directly.
4. Writes the merged `.esc` to
   `node_modules/.cache/escalier/<pkg>@<hash>.esc`.
5. Loads the cached `.esc` into the checker.

Subsequent compiles hit the cache directly. The baseline is never
persisted — only the merged result.

The merge model still applies here (and not for our own builtins)
because we don't own the upstream `.d.ts`: it changes on dep
updates, users want a refinement path that doesn't require forking,
and the baseline carries no `throws`/lifetime annotations (no
heuristic) — *uncertainty is the default* for code we don't own,
with the override mechanism as the precision path.

**Cache invalidation.** The merged `.esc` cache invalidates when:

- The dep's npm package name or installed version changes. The cache
  key is `<pkg-name>@<pkg-version>` from `package.json`, not a content
  hash of the `.d.ts` files. This is cheap to compute, stable across
  machines, and good enough in practice — patch releases that quietly
  re-emit `.d.ts` without bumping a version are rare, and the
  `escalier cache clean` escape hatch covers the case if it happens.
- Any override fragment whose target path names this dep changes
  (covers both `node_modules/<pkg>/overrides/` and the user
  project's `<project>/overrides/`). The §5 loader already parses
  overrides into a target-keyed map, so computing "fragments
  targeting `X`" is mechanical.
- The Escalier compiler version changes (captures converter
  improvements and bug fixes).

Cache lives under `node_modules/.cache/escalier/` so `npm ci` /
clean-install resets it for free.

**CLI surface change.** A new `escalier cache clean` subcommand
provides a manual escape hatch (wipes
`node_modules/.cache/escalier/`).

### Prelude

`Prelude` in [internal/checker/prelude.go](../../internal/checker/prelude.go)
stops walking `node_modules/typescript/lib/*.d.ts` for global symbols.
Instead it parses the embedded `internal/interop/data/builtins.esc`
through `parser.ParseDecls` + the standard checker pipeline. Because
`builtins.esc` contains only declarations (no value-level expressions
that need a prelude themselves), loading it via the checker does
*not* reintroduce the §6.B bootstrap cycle — there's nothing inside
that needs a prior global scope to type-check against.
**The prelude does not pre-load any pseudo-package files** — those
are loaded on demand when a program imports them.
`loadGlobalDefinitions`, `populateSelfParams`,
`UpdateMethodMutability`, `mergeReadonlyVariant`, and the
`mutabilityOverrides` Go map all become dead code.

### Override pipeline scope after the shift

§5's override loader / merge code stays, but its surface area
contracts to *third-party deps only*:

- **Per-dep overrides** (`node_modules/<pkg>/overrides/*.esc`, tier
  1a) — library authors ship Escalier-aware refinements alongside
  their `.d.ts`.
- **User-project overrides** (`<project>/overrides/*.esc`, tier 1b) —
  users refine `@types/*` without forking.

The **builtin tier** of the override store (tier 4 in
[requirements.md](requirements.md)) goes away entirely. There's no
need for it — builtin and pseudo-package refinements live inline in
the `.esc` files, edited like any other source.

> *Tier-numbering caveat.* This proposal refers to "tier 1a / 1b"
> (per-dep / user-project) and "tier 4" (builtin) following the
> `requirements.md` numbering at the time of writing. If the tier
> scheme there changes, update the cross-references here in lockstep
> — adopting this proposal materially reshapes the tier landscape.

What also goes away: `interop.ConvertModule` as a *runtime* call,
the seven-tier `Classify` at runtime (it still runs at conversion
time), the trio-fusion in `class_shapes.go`, and
`mergeReadonlyVariant`. All of that becomes either dead code or
converter-only code. Escalier-specific extras currently injected
via prelude code (e.g. `SymbolConstructor.customMatcher` at
[prelude.go:809–846](../../internal/checker/prelude.go#L809)) move
into `builtins.esc` as ordinary source, since `SymbolConstructor`
itself is now ambient.

## Open questions

### Declaration printer completeness

§8 audited every `type_system.Type` variant with a syntactic form,
plus mapped types after the fix. But the bootstrap converter also
has to emit *declaration-level* forms (`declare class`, `declare fn`,
`declare namespace`, `declare type alias`, `declare var`, generic
parameter constraints with `extends`, etc.). The AST printer in
[internal/printer/printer.go](../../internal/printer/printer.go)
already handles these for `.esc` source — but does it handle
`declare` prefixes, ambient module syntax, and the exotic shapes
that show up in `lib.es*.d.ts` (conditional types referring to
`infer T`, mapped types with `as` rename clauses, etc.)?

A §8.5 audit pass would establish this before committing to the
proposal. Estimated work: similar to §8 — a day to enumerate, plus
fixes. **This is the riskiest gate**; the rest of the proposal is
straightforward implementation work.

### Intrinsic types are checker-resident, not source-visible

A handful of TS lib symbols rely on `intrinsic` (`Uppercase`,
`Lowercase`, `Capitalize`, `Uncapitalize`, `NoInfer`, `Awaited`).
**These are implemented directly in the Escalier type checker** as
named handlers — the four string-case utilities as pure
`Type → Type` resolvers, `NoInfer` as an inference-machinery hook
(see [#631](https://github.com/escalier-lang/escalier/issues/631)),
and `Awaited` as a built-in reduction (potentially expressible as a
recursive conditional type per
[#630](https://github.com/escalier-lang/escalier/issues/630), but
likely retained as an intrinsic for performance regardless).

Consequence: the `intrinsic` keyword never appears in `.esc` files.
The bootstrap converter strips or substitutes any `intrinsic`-typed
declaration in the source `.d.ts` — the checker already knows the
name. The Escalier parser therefore does *not* need to accept
`intrinsic`.

## Migration path

Phased so we can back out if it doesn't work:

1. **§8.5 Declaration printer audit.** Same shape as §8 but for
   declaration-level forms. Establishes that the converter can emit
   `.esc` for the constructs `lib.*.d.ts` actually uses. Riskiest
   gate; do this first.
2. **URI-scheme import support.** Extend the parser and module
   resolver to accept `import "scheme:name"` and route `std:`,
   `web:` (and the deferred `node:`) prefixes to the embedded
   `internal/interop/data/` tree. Default *local* namespace-binding
   semantics per the convention above. Parse the `?flag` /
   `?flag1&flag2` suffix on the URI; strip it before path resolution;
   apply per-flag binding rules at scope-insertion time. Initial flag
   set covers both binding shapes: `?local` (default-equivalent
   no-op) and `?nested` (scheme.package qualified binding).
   Combining the two reports a clear error. Gate: a placeholder
   `std:math` package with a stub `math.PI = 3.14` imports and
   resolves end-to-end; a two-fixture test confirms each flag binds
   correctly (`?local` → `math.PI`, `?nested` → `std.math.PI`);
   mutually-exclusive flag combos error.
3. **Cross-package augmentation support.** Confirm that the existing
   §5 interface-merging mechanism (or a small extension of it) lets
   a package augment an open interface declared in another package,
   with augmentation scoped to files that import the augmenting
   package. Test with a hand-authored two-file pair: `web/core.esc`
   declares an empty `HTMLElementTagNameMap`; `web/canvas.esc`
   augments it with `canvas: HTMLCanvasElement`; importing
   `web:canvas` under either binding-shape flag (`?local` or
   `?nested`) makes `createElement("canvas")` narrow correctly in
   the importing file, and a sibling file *without* the import
   sees only the pre-augmentation shape. Gate: that test passes.
   Indexed-access over the augmented registry resolves to the right
   element type. Registry-augmentation activation is shown to be
   orthogonal to the binding-shape flag.
4. **Converter MVP.** Build `tools/dts_to_esc/` against a tiny
   slice (`Boolean` alone, ~10 lines of `.d.ts`). Emit to stdout.
   No file layout, no partition logic. Gate: output round-trips
   through the parser and reads naturally to a human.
5. **Converter productionization.** Add the ambient-vs-package
   partition table, full output paths under
   `internal/interop/data/`, `--check` mode, and the full
   `lib.*.d.ts` set as input. The converter must also detect which
   TS-lib symbols belong to registries (`HTMLElementTagNameMap`,
   `HTMLElementEventMap`, …) and route their per-feature entries
   into the appropriate `web:*` package's augmentation block. Gate:
   every output file parses; running the converter twice produces
   byte-identical output.
6. **Builtin bootstrap.** Run the converter, review the output,
   hand-edit obvious mis-classifications and high-value `throws`
   annotations across `builtins.esc` and the `std/`/`web/` packages,
   commit. Gate: humans review the committed files.
7. **Prelude switchover.** Replace the lib-walking path in
   [prelude.go](../../internal/checker/prelude.go) with a load of
   `builtins.esc` only. Old path stays behind a build flag for one
   release; run both side-by-side in CI; assert resulting global
   namespace is equivalent for *ambient* surface (pseudo-package
   types are no longer ambient and that's intentional).
8. **Existing code migration.** Update Escalier's own fixtures and
   tests that relied on previously-ambient symbols (`Math`, `JSON`,
   `console`, `Map`, etc.) to import the corresponding pseudo-package.
   This is the user-visible breaking change.
9. **Delete builtin overrides infrastructure.** The
   `data/builtins/overrides/` subdirectory that the current §6 plan
   introduces goes away. `BuildBuiltinStore` returns an empty store;
   eventually the function deletes. **Depends on step 8** — fixtures
   relying on previously-ambient symbols must have been migrated to
   `import "std:*"` first, otherwise removing the override path
   regresses them before step 11 removes the underlying runtime
   interop pipeline.
10. **Third-party lazy cache.** Wire the converter into the compile
    pipeline. Uncached deps trigger baseline conversion → merge
    against §5 tier 1a/1b overrides → write to
    `node_modules/.cache/escalier/`.
11. **Delete the runtime interop pipeline.** Once step 10 is stable,
    `interop.ConvertModule`, the runtime `Classify`, the trio-fusion
    in `class_shapes.go`, and `mergeReadonlyVariant` come out.
12. **Flip the default.** Remove the build flag from step 7.

Estimated total effort: ~1 week for steps 1–5 if no major surprises
in the declaration-printer audit, the resolver extension, or the
augmentation mechanism. Steps 6–8 are authoring + migration work
paced by the human review of generated files (probably another 1–2
weeks). Steps 9–12 add a few more days for cache plumbing and the
deletion pass.

## Risks

- **Declaration-form printer fidelity** (see Open Questions). If
  `lib.es*.d.ts` contains shapes that don't round-trip, the whole
  approach is gated on extending the parser. Hard to estimate
  without doing the audit.
- **Ergonomic cost of imports.** Today `console.log` Just Works
  ambient. Under this model the program needs `import "std:console"`
  first. Defensible — Rust, Go, Python all require imports — but
  it's friction relative to JS/TS. Mitigations: editor auto-import
  suggestions (standard for modern language tooling), descriptive
  error messages on unbound globals ("did you mean to
  `import \"std:console\"`?").
- **Initial bootstrap quality.** The committed files start from
  heuristic output. Bad classifications that slip past review ship
  to users. Mitigation: the files are editable, so corrections are
  PRs, not version bumps.
- **Partition table churn.** "Which symbol lives in which pseudo-
  package" is a maintained mapping. When TS adds a new symbol, the
  partition table needs a new entry — manual decision per addition.
  Probably not large in practice (TS lib additions are rare in
  minor versions), but not zero.
- **Cross-package augmentation mechanism.** The registry pattern
  depends on a package being able to augment an open interface
  declared in another package, with the augmentation visible across
  the compilation unit. Escalier's §5 interface-merge code is
  *probably* reusable, but it was designed for type-refinement at a
  fixed merge point, not for arbitrary cross-package activation. If
  the existing mechanism doesn't fit, this needs its own loader path
  — adds work to step 3 of the migration. Worth confirming early.
- **Non-local reasoning from augmentation activation.** A function
  using `document.createElement("canvas")` gets `HTMLCanvasElement`
  only if some module in the same compilation unit imported
  `web:canvas`. This is type-only (runtime behavior is unchanged) but
  users encountering an unexpectedly-coarse type will need to learn
  to look for missing imports. Mitigation: an `--explain-type`
  diagnostic that, when a tag-keyed return is wider than expected,
  suggests likely `web:*` imports.
- **Hand-edits drift from upstream TS.** If TS adds a method to
  `Array.prototype` in a minor TS version bump, someone has to
  notice (via `--check`) and port it by hand. Mostly a feature —
  gives us intent over upstream churn — but Escalier's view of the
  JS stdlib can lag.
- **Cache correctness for third-party deps.** A stale cache that
  doesn't invalidate when it should will silently use outdated
  types. Mitigated by content-hashing all inputs and the
  `escalier cache clean` escape hatch.
- **First-build latency for third-party deps.** The first compile
  in a fresh checkout converts every dep's `.d.ts`. This is
  amortized — subsequent builds are cache hits — but the cold-start
  cost is real. Should be parallelizable per-package.
- **Polyfill story is a separate effort.** Always-modern builtins
  only work if codegen can lower modern features to older targets.
  Babel and swc demonstrate it's tractable, but it's its own work
  item that this proposal depends on.
- **Cross-file references within `builtins.esc`.** Even one file is
  one module — but `Promise<T>` references `Iterator`, `Array<T>`
  references iteration protocol, etc. The existing module loader
  handles this for user code; just need to confirm it works for the
  ambient set. Probably not an issue.

## Recommendation

Run the §8.5 declaration-printer audit first. If declaration forms
round-trip cleanly, commit to this proposal and pause §6.B authoring
on the override-based plan. If declaration forms reveal major parser
gaps, the audit work isn't wasted — it tightens the printer
regardless, and §9 (`@esctype` round-trip) needs it anyway.

§8 was a worthwhile prerequisite: the mapped-element fix it surfaced
would have silently broken builtin bootstrap regeneration under this
proposal.
