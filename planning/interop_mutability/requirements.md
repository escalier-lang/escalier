# TypeScript Interop: Mutability Requirements

## Background

Escalier tracks mutability in its type system; TypeScript does not (beyond
`readonly` properties and `Readonly<T>` wrappers). When Escalier consumes
TypeScript declarations (`.d.ts`), we have to decide for each method whether it
mutates its receiver. Getting this wrong in either direction has costs:

- **Too permissive (assume non-mutating when it mutates):** unsoundness — an
  Escalier value declared immutable could be mutated through a TS method.
- **Too strict (assume mutating when it doesn't):** ergonomic friction —
  immutable values can't call methods that are actually safe.

We bias toward soundness: when in doubt, assume mutating.

## Core principles

1. **Default to mutating.** If no signal applies, methods are assumed to
   mutate the receiver. This keeps Escalier sound in the face of unknown TS
   APIs.
2. **`Readonly`-prefixed standard collections drive shape.** For built-ins
   that ship both mutable and readonly variants — `Array`/`ReadonlyArray`,
   `Set`/`ReadonlySet`, `Map`/`ReadonlyMap` — methods present on the readonly
   variant are non-mutating; methods only on the mutable variant are mutating.
3. **Primitive wrapper classes are fully non-mutating.** All methods on
   `Number`, `BigInt`, `String`, and `Boolean` are treated as non-mutating
   (the underlying values are immutable).
4. **`get`-prefixed methods are non-mutating, with exceptions.**
   `get`-prefixed methods are assumed non-mutating, except for
   documented mutate-on-miss patterns: `getOrInsert*`,
   `getOrUpdate*`, `getOrCreate*`, and similar. (`getOrDefault*` is
   *not* in this list — by convention it returns a default without
   writing.) Promoted to a core principle rather than a name-based
   heuristic because the prefix is unusually unambiguous in JS/TS
   APIs and shows up across most collection-style libraries.
5. **Known FP / immutability libraries are fully non-mutating.** For
   well-known functional or immutable-data libraries — Ramda, fp-ts,
   Effect, Immutable.js, lodash/fp, and similar — assume every method is
   non-mutating in both its receiver and its arguments. Ship these as
   library-level overrides alongside the built-in overrides.
   New libraries can be added by the user via the same override
   mechanism.
6. **TS `readonly` properties are always readonly in Escalier.** A field
   declared `readonly` in a `.d.ts` cannot be assigned to, regardless of
   whether the holding reference is mutable or immutable. This is a
   property-level constraint independent of the receiver's mutability — a
   mutable reference to an object with `readonly foo` still cannot write
   `foo`. (Mutable references can still call mutating methods that modify
   non-`readonly` fields.)

7. **Property type, slot, and lifetime are independent axes.** A class
   or interface property has three orthogonal aspects, each addressed
   by a different mechanism:

   - **Slot reassignability** — whether `obj.foo = …` is legal. Carried
     by the `readonly` modifier on the field (principle #6).
   - **Referent mutability** — whether mutation *through* `obj.foo` is
     legal (e.g. `obj.foo.push(x)` on an array-valued field). Carried
     by the property's type itself: `Mut[Array[T]]` permits in-place
     mutation; `Array[T]` does not. TypeScript has no direct equivalent;
     the type `T[]` in `.d.ts` is silent on this question and the
     override mechanism is the canonical way to record it.
   - **Borrow scope** — how long the referent is valid relative to the
     host. Carried by property-level lifetime annotations
     (`foo: &'self T`, etc.) for properties that expose internal
     references rather than independently-owned values.

   These three axes do not subsume each other. `readonly` does not
   imply non-`Mut` of the referent; `Mut` wrapping does not imply
   anything about lifetimes; and a lifetime annotation does not decide
   mutability. An override may need to change one, two, or all three on
   a single property.

   Property type overrides are therefore a first-class use case for the
   override mechanism — not only for toggling `Mut` wrapping but also
   for:
   - **Precision tightening** — narrowing a TS-side `any`, `object`,
     `unknown`, or sloppy union to the actual runtime shape.
   - **Generic substitution** — recording that a class's own methods
     mutate its `T[]` field by exposing it as `Mut[Array[T]]`.
   - **Brand narrowing** — refining `id: string` in `.d.ts` to
     `id: UserId` where `UserId` is a branded type declared in
     user code.

## Round-tripping via `@esctype`

When Escalier emits `.d.ts` for `.esc` source, the compiler embeds the
original Escalier type as a TSDoc tag so that downstream Escalier
consumers recover the exact mutability information that TypeScript
itself cannot represent.

- **Emit.** For every exported symbol (function, method, getter/setter,
  property, class, type alias) the generated `.d.ts` includes a TSDoc
  comment containing `@esctype {<type>}`, where `<type>` is the string
  representation of the Escalier type for that symbol. The braces are
  required delimiters; the type may span multiple lines. Object-type
  literals nest naturally — e.g.
  `@esctype {{x: number, y: number}}` — so the parser scans for a
  balanced `{ ... }`, respecting string-literal context. If the `.esc`
  source already has a docstring on the symbol, the `@esctype` tag is
  appended to that existing comment rather than emitted as a separate
  block.
- **Consume.** When reading a `.d.ts`, an `@esctype` tag on a symbol
  is consumed identically whether it was emitted by Escalier or
  hand-authored by a TS library author. The compiler parses the tag
  value, reconstructs the Escalier type, and uses it for that symbol.
  Hand-authoring is supported as the inline mechanism for TS authors
  who want to express Escalier-side mutability/lifetime/`throws`
  information directly in their `.d.ts`. (Round-trip emission from
  `.esc` is the same path.) This makes Escalier-to-Escalier interop
  lossless across the `.d.ts` boundary.
- **Precedence.** `@esctype` outranks core principles, shipped
  overrides, and all heuristics, but **user overrides win over
  `@esctype`**. This gives the consuming project a way to correct a
  vendored `.d.ts` whose `@esctype` tag is wrong, stale, or
  intentionally being narrowed locally — without forcing the user to
  edit the `.d.ts` itself. A symbol classified via `@esctype` (and
  not further overridden) is treated as fully explicit and never
  triggers the uncertainty warning; a symbol classified via a user
  override likewise counts as explicit.
- **TSDoc registration.** `@esctype` is a custom TSDoc tag and must be
  declared so consumer tooling doesn't flag it as unknown. Ship a
  `tsdoc.json` (or equivalent) alongside generated `.d.ts` output, and
  document the tag for users who run TSDoc-aware tooling against it.

## Resolution order

When classifying a symbol's mutability, apply rules in this order and
stop at the first match:

1. **User overrides** for the symbol's module / class. Within this
   tier, consuming-project overrides (1b) win over dep-vendored
   overrides (1a).
2. **`@esctype` tag** on the symbol (round-trip from Escalier source,
   or hand-authored on a TS `.d.ts`).
3. **Explicit author signals** — `readonly this`, getters/setters,
   `Readonly<T>` collection variant, `readonly` properties (principle
   #6), well-known symbol methods.
4. **Shipped overrides** — built-ins (principle covers `Array`/`Map`/`Set`
   readonly variants by shape; explicit override entries cover `Date`,
   `RegExp`, `Promise`, etc.) and known FP libraries (principle #5).
5. **Primitive wrapper classes** (principle #3) — `Number`, `BigInt`,
   `String`, `Boolean` methods are non-mutating.
6. **`get*` rule** (principle #4), with the documented exceptions.
7. **Name-based heuristics** — predicate, conversion, query, copy,
   iteration prefixes (Medium signals); mutating-prefix list reinforces
   the default.
8. **Default to mutating** (principle #1).

Items 1–4 are *explicit signals* for the purposes of the uncertainty
warning; items 5–7 are heuristics, and a non-mutating classification
from any of them counts as uncertain.

**Inherited classifications.** When a method on a subclass has no
direct match at any tier, the classification is inherited from the
nearest base-class method per "Inheritance is mutability-preserving"
(see Policy decisions). The inherited classification carries the *tier of
the base method* — so an inherited classification from a base method
whose tier was an explicit signal stays explicit, and one inherited
from a heuristic stays uncertain. This means inheritance never
upgrades certainty.

## Heuristics

In addition to the core principles above, the compiler applies the
following heuristics, grouped from strongest to weakest signal.

### Strong signals (explicit author intent or structural)

- **`Readonly<T>` collection variant.** See core principle #2 — methods
  on `ReadonlyArray`/`ReadonlySet`/`ReadonlyMap` are non-mutating;
  methods only on the mutable variant are mutating. Listed here for
  precedence-resolution purposes.
- **`readonly` `this` parameter.** A method declared with
  `(this: Readonly<T>, ...)` (or on a `readonly` interface) is
  non-mutating. This is the most direct signal a TS author can give.
- **TS property getters/setters.** `get foo()` accessors are non-mutating;
  `set foo(v)` accessors are mutating. (Distinct from name-prefix heuristic
  on regular methods.)
- **Well-known symbol methods.** `toString`, `toJSON`, `toLocaleString`,
  `valueOf`, `[Symbol.iterator]`, `[Symbol.asyncIterator]`,
  `[Symbol.toPrimitive]` are non-mutating by convention. (Iterator state is
  advanced via `next`/`return`/`throw`, not via the symbol method — see
  Special cases below.)
- **Built-in overrides.** For `Date`, `RegExp`, `Promise`,
  `Error`, typed arrays, `URL`, `URLSearchParams`, `WeakRef`, etc.,
  ship overrides that explicitly mark each method as mutating or
  non-mutating. Heuristics are unreliable on these (`Date.setHours`
  mutates; `RegExp.exec` mutates `lastIndex`).

### Medium signals (name-based)

- **Predicate prefixes.** `is*`, `has*`, `can*`, `should*`, `will*`, `was*`,
  `did*`, `contains`, `includes`, `equals`, `matches` — assume non-mutating.
- **Conversion / projection prefixes.** `to*` (`toUpperCase`, `toFixed`,
  `toArray`), `as*` (`asReadonly`), `with*` (`withDefault`) — assume
  non-mutating; these conventionally return a new value.
- **Query / search prefixes.** `find*`, `filter*`, `map*`, `reduce*`,
  `every`, `some`, `indexOf`, `lastIndexOf`, `at`, `count*` — assume
  non-mutating.
- **Copy / clone prefixes.** `clone*`, `copy*`, `slice`, `concat` — assume
  non-mutating.
- **Iteration accessors.** `keys`, `values`, `entries`, `forEach` on
  collection types — non-mutating on the collection (the returned iterator is
  a separate object whose own `next()` is stateful).

### Mutating-name signals (reinforce the default, useful for asymmetric APIs)

These are mostly redundant with "default to mutating," but help when combined
with another signal that would otherwise mark something non-mutating:

- `set*`, `add*`, `remove*`, `delete*`, `clear*`, `reset*`, `init*`,
  `push*`, `pop*`, `shift*`, `unshift*`, `insert*`, `replace*`, `update*`,
  `register*`, `unregister*`, `dispatch*`, `emit*`, `write*`, `flush*`,
  `sort` and `reverse` (in-place on Array).

If a name has both a mutating and non-mutating prefix, prefer mutating.
This shows up often enough in collection APIs to matter; common examples:

- `getOrInsert` / `getOrCreate` / `getOrUpdate` — `get` + write-on-miss.
- `findAndRemove` / `findAndDelete` — search then mutate.
- `getAndSet` / `getAndIncrement` / `getAndUpdate` — atomic-style ops that
  return the old value but mutate.
- `popIfPresent`, `shiftOrDefault` — leading mutating prefix wins; the
  qualifier doesn't neutralize it.

Counter-example (why we still need built-in overrides): the ES2023 Array
methods `toSorted`, `toReversed`, `toSpliced`, `with` are
**non-mutating** despite containing `sort`/`reverse`/`splice`. The
"prefer mutating" rule would misclassify them; the built-in
overrides correct the heuristic.

### Rejected structural cues

Return type equal to receiver type and `void` return are too noisy to
use — `void` methods often only do I/O (`console.log`), and same-type
returns appear in both builder-mutating and copy-on-write APIs. Ignore
them.

### Special cases

- **Iterators.** Methods named `next`, `return`, `throw` on objects matching
  the iterator protocol are mutating (state advance). Generators likewise.
- **Promise methods.** `then`, `catch`, `finally` are non-mutating on the
  promise (return new promises).
- **Builder patterns returning `this`.** Often mutating in JS/TS (jQuery,
  knex, etc.). Lean on the default — don't carve out a non-mutating rule
  here.

## Overrides

Overrides are not just an escape hatch — they're the primary
classifier for any library where the compiler ships explicit knowledge
(built-ins, well-known FP / immutability libraries per principle #5).
The same machinery serves user-supplied corrections for third-party APIs.

- **Shipped overrides** — bundled with the compiler. Cover built-in
  classes that don't have a `Readonly*` variant in TS's lib
  files (`Date`, `RegExp`, `Promise`, `Error`, typed arrays, `URL`,
  `URLSearchParams`, `WeakRef`/`WeakMap`/`WeakSet`, etc.) plus
  well-known FP / immutability libraries (Ramda, fp-ts, Effect,
  Immutable.js, lodash/fp). `Array` / `Map` / `Set` need no entry —
  their mutability is already encoded in the
  `Readonly*` / mutable interface split that TypeScript ships.
- **User override files** — checked into the consuming project,
  declaring per-module corrections for third-party APIs. Loaded
  through the same machinery as the shipped overrides.

Inline overrides on a TS symbol are expressed via `@esctype` (see
Round-tripping section) — that mechanism subsumes any narrower
"`@escalier-pure`"-style pragma, since `@esctype` can encode full
mutability along with the rest of the type.

### Scope and future extensibility

The initial implementation only uses overrides to refine method
receivers — i.e. whether a method takes `self` or `mut self`.
Function/method bodies don't appear in overrides, only signatures,
and within those signatures only the receiver mutability marker is
load-bearing for Phase 1. Argument-mutation refinement, return-type
adjustments, and the rest of the surface specified above are still
*parsed* (so the file format is stable) but only the receiver mode
flows through to the checker initially.

The override format and merge machinery must, however, be designed
so that the following extensions can be layered on without a syntax
or schema break:

- **Lifetime annotations.** Overrides should be able to attach
  Escalier lifetime parameters and constraints to functions and
  methods (e.g. `fn foo<'a>(self: &'a T, x: &'a U) -> &'a V`). The
  signature grammar used inside override blocks must therefore be
  the full Escalier function-signature grammar, not a stripped-down
  receiver-only subset.
- **`throws` clauses.** Overrides should be able to declare the
  set of exceptions a function/method can throw, e.g.
  `fn parse(s: string) -> T throws ParseError | RangeError`. The
  effective type carries the throws set; the checker may ignore it
  in the initial implementation but the merge logic must preserve
  it round-trip.

Both extensions reuse the same member-matching, overload-collapsing,
and conflict-resolution rules already specified — they add fields to
the per-signature payload rather than new top-level forms.

### Consistency with upstream `.d.ts`

Even though Phase 1 only acts on the receiver mode, every override
signature is required to match the corresponding upstream `.d.ts`
signature in:

- **Arity** of non-receiver parameters.
- **Types** of non-receiver parameters (after mapping TS types into
  Escalier's type system; receiver-mutability markers and lifetime
  annotations are excluded from the comparison).
- **Return type** (same mapping).

A mismatch is a hard error from the override merge, not a silent
divergence. This keeps overrides honest: an override can refine
*mutability* (and, later, lifetimes / throws) but cannot quietly
fork the shape of a built-in API.

To keep shipped overrides in sync as upstream `.d.ts` files evolve,
the compiler test suite includes a **consistency test** that runs
over every shipped override entry:

- For TS built-in symbols, the upstream is the bundled TS lib
  `.d.ts` set pinned to the compiler's TS lib version.
- For shipped third-party overrides (Ramda, fp-ts, Effect,
  Immutable.js, lodash/fp, etc.), the upstream is the corresponding
  `@types/*` package (or the library's own bundled types) at a
  pinned version recorded alongside the shipped override.

Every overridden symbol has an upstream `.d.ts` counterpart: even
Escalier-authored libraries ship `.d.ts` for TypeScript
back-compat, so "no upstream types" is not a case that arises.

For each entry, the test:

1. Looks up the corresponding declaration in the pinned upstream
   `.d.ts`.
2. Compares non-receiver arity, parameter types, and return type
   under the same mapping the merge uses.
3. Fails the build on any divergence, reporting the specific member
   and which field disagrees.

Bumping any pinned upstream version is expected to surface override
drift as a deliberate fix-up step rather than letting it accumulate.

### Override file format

- **Files are `.esc`.** Free syntax highlighting, parser reuse, and
  the override surface looks like the language being overridden.
- **Location & discovery.** Override files live under an `overrides/`
  directory at the root of a package — i.e. the directory containing
  the package's `package.json`. The compiler walks the following
  locations in order, with later locations winning on conflict:
  1. Shipped overrides bundled with the compiler (resolution-order
     tier 4).
  2. `node_modules/<dep>/overrides/**/*.esc` — overrides shipped by
     a dependency for itself or its own deps (resolution-order
     tier 1a). Allows library authors to publish overrides for
     libraries they wrap.
  3. The consuming project's own `overrides/**/*.esc` (resolution-
     order tier 1b — consuming-project user overrides).

  Tier 1 is split into 1a (dep-vendored) and 1b (consuming-project),
  with 1b winning over 1a on conflict; both win over tier 4 (and
  over `@esctype` at tier 2 — see Round-tripping section). The
  uncertainty-warning rule treats 1a and 1b uniformly as user
  overrides (explicit signals).

  Within each location, all `.esc` files under `overrides/` are
  loaded recursively; subdirectory structure has no semantic effect.
- **No prescribed file naming or 1:1 mapping to libraries.** A single
  file may contain overrides for multiple libraries; a single
  library's overrides may be split across multiple files.
- **Top-level form.** Override declarations must use the `override`
  keyword at the top level, e.g.:

  ```esc
  override declare module "ramda" { ... }
  override declare module "some-lib" { ... }
  override declare global { class Date { ... } }
  override declare class Date { ... }      // sugar for the above
  override declare interface Foo { ... }   // sugar — globals namespace
  override declare namespace NodeJS { ... }
  override declare fn parseInt(s: string, radix?: number) -> number
  ```

  `override declare class C { ... }` and `override declare interface I { ... }`
  at file root are sugar for an `override declare global` block containing the
  same body. See [override_merge_semantics.md](./override_merge_semantics.md) for
  the full top-level-form table.

  Without `override`, a `declare module` / `declare global` is a
  normal ambient declaration, not an override. Inside the body of an
  `override declare module "..."` or `override declare global { ... }`
  block, `override` and `declare` are implied on each contained
  declaration and must not be repeated — mirroring TypeScript's
  behavior inside `declare module "x" { ... }`. `export` is still
  required on declarations that are part of the module's exported
  surface (same rule as TS modules); inside `override declare global`,
  declarations are ambient by definition and `export` is neither
  required nor allowed.
- **Bare functions.** Top-level functions are overridden the same way
  as methods, using `override declare fn` (at the file root, targeting
  a global) or `export fn` inside an `override declare module "..."`
  block (targeting a module export). The overload-collapsing and
  override-defined-overload rules below apply to bare functions
  identically to methods — a single override entry replaces every
  original overload of the function; multiple entries with the same
  name in the same scope form a new overload set.
- **Namespaces.** TypeScript namespaces are patched with
  `override declare namespace Foo { ... }`. The body follows the same
  member-presence rules as a class/interface body (override existing
  members; declaring a nonexistent member is an error). Namespaces
  may nest (`namespace A { namespace B { ... } }`) and the override
  form mirrors the nesting. Inside a namespace override block,
  `override` and `declare` are implied on contained declarations.
- **Computed keys.** Two key shapes are supported as override
  targets:
  1. **Qualified identifiers** — any dotted identifier path, not
     just `Symbol.foo`. So `[Symbol.iterator]`,
     `[Symbol.asyncIterator]`, and also `[MyLib.tag]` or
     `[a.b.c.key]` are valid when the original `.d.ts` declares a
     member with that computed key. The path is matched
     structurally against the original's computed-key expression
     and must resolve to the same symbol/value as the original.
  2. **String-literal keys** — `["foo bar"]`, `["123"]`, etc.,
     matched by literal string equality against the original's
     key.

  Members keyed by arbitrary expressions (anything other than the
  two shapes above) cannot be overridden.
- **Partial overrides.** A class or interface body inside an override
  block need only list the members being overridden. Members present
  in the original `.d.ts` but absent from the override remain
  unchanged. The override patches the type rather than replacing it.
- **Overload collapsing.** A `.d.ts` may declare a function or method
  with many overloads; a single override entry for that name overrides
  *all* of them. The override becomes the new authoritative signature
  regardless of how many overloads the original had. This applies
  uniformly to bare functions, methods, static members, and
  namespace-scoped functions.
- **Override-defined overloads.** If an override file declares the
  same name multiple times in the same scope (module, class,
  namespace, or global), those declarations form an overload set in
  the resulting type — exactly as if the user had written overloads
  in normal Escalier source. Holds for bare functions and methods
  alike.

## Policy decisions

- **Surfacing ambiguity / strict mode.** Provide an opt-in warning that
  fires whenever a method is called on an immutable reference and the
  classification came from a heuristic (resolution-order steps 5–7)
  rather than an explicit signal (steps 1–4). Off by default; a single
  flag covers both "tell me about uncertainty" and "strict interop."
- **Argument mutation: default to mutating.** Assume any object/array
  passed as a parameter may be mutated by the callee. Later we can refine
  the built-in override file using MDN as the source of truth
  (e.g. mark `Array.prototype.map`'s callback receiver as non-mutated,
  flag `Object.assign`'s target as mutated). Third-party APIs stay
  conservative until overrides are written.
- **Inheritance is mutability-preserving.** A subclass override is
  assumed to have the same mutability as the base method, regardless of
  which side of the interop boundary base and subclass live on (TS base
  + Escalier subclass, Escalier base + TS subclass, or same-language).
  Don't try to detect divergence; if a library actually diverges, an
  override entry on the specific subclass method can correct it.
- **`implements` requires mutability conformance.** Member
  resolution on a class instance always uses the class's own
  declarations, not those of any implemented interface — this is the
  existing strict-`implements` policy (`getObjectAccess` does not
  walk `Implements`). Conformance checking is separate: when a class
  declares `implements I`, every method `C` provides for an
  interface member of `I` must match the interface's mutability
  annotation (post-merge, after both class and interface have been
  through override resolution). A class method declared `mut self`
  cannot satisfy an interface method declared `self`, and vice
  versa. Mismatch is a hard conformance error, the same kind as a
  return-type or arity mismatch — not silently resolved in favor of
  one side.

  Where the class or interface gets its mutability annotation from a
  heuristic rather than an explicit signal, the conformance check
  still runs against the resolved classification. This is the only
  place a heuristic can produce a hard error rather than a warning;
  the correction is to add an explicit signal (override entry,
  `@esctype`, or `readonly this`) on whichever side is wrong.

## Implementation approaches considered

Four architectures were sketched for how override `.esc` files
combine with the upstream `.d.ts` types. Trade-offs in summary:

### A. Parse → eager merge → single effective type

Parse `.d.ts` and `override declare ...` into the same type-system
representation. A dedicated merge pass keyed on
`(module/namespace, qualified-name)` produces a fresh effective
type before the checker runs. The checker has no awareness of
overrides.

- **+** Clean layering: merge is a pure function on types;
  consistency test trivially walks the merged set; diagnostics can
  quote both sources.
- **+** Phase 1 → future-phase migration is just "add fields to the
  per-signature payload"; the merge driver doesn't change.
- **−** Need a parser mode that accepts partial bodies (member
  subsets, no fn bodies, `override`/`declare` implicit inside
  blocks). Have to design the override AST node set up front.

### B. Overlay store + lazy materialization at resolve time

Parse overrides into a flat overlay store indexed by
`(module, qualified-name, kind)`. The resolver, when asked for a
TS symbol's type, checks the overlay; on hit, materializes the
merged type and caches it. Originals untouched.

- **+** Pay merge cost only for symbols actually used.
- **+** Easy to make the consistency test eager by iterating the
  overlay store regardless of usage.
- **−** Order of resolver/checker interactions gets subtler;
  caching across imports has to be airtight or merged types
  diverge. Diagnostics need extra plumbing to know which overlay
  file won.

### C. Metadata sidecar (Phase 1 only)

Skip full parsing of override `.esc` for Phase 1. Extract a
structured per-symbol attribute table (`receiver`, eventually
`pure`) and stamp it onto the original type.

- **+** Minimum code to reach a working Phase 1.
- **−** Conflicts with the requirement that the format be designed
  to layer in lifetimes/throws without a schema break — those need
  type grammar in the override, not just attribute bits. Adopting
  this would mean rewriting the data flow at Phase 2.

### D. Special declaration-merge mode

Reuse the existing TS declaration-merging path and add a mode bit:
`override` means "replace this member" rather than "union with it."

- **+** Reuses the most code; matches author intuition that
  overrides "look like" augmentations.
- **−** Replace-vs-union semantics fork enough from real TS
  merging that the shared path will accumulate `if override` branches
  around overloads, computed keys, and namespace nesting — bug risk
  when a project mixes regular `declare module` augmentation with
  `override declare module` for the same library.

## Implementation decision: approach A

We are going with **A (parse → eager merge → single effective
type)**. Reasoning:

- **Overrides are the source of truth, not a patch layer.** Long
  term we expect overrides to *supersede* the upstream `.d.ts` for
  built-ins and a handful of very popular third-party
  libraries — not just nudge a few methods. An eager merge that
  produces one effective type per symbol matches that mental model;
  a lazy/overlay design (B) or a sidecar (C) frames overrides as
  secondary annotations, which is the wrong default once the
  override is the canonical specification.
- **The future-extensibility requirement (lifetimes, `throws`)
  needs full signature grammar inside override blocks.** A is the
  only approach that lands that grammar in a single representation
  end-to-end; C is explicitly incompatible, and B/D push grammar
  decisions into the resolver/merge-mode boundary where they're
  harder to evolve.
- **The consistency test against bundled TS lib `.d.ts` falls out
  for free.** With one merged type representation, the test is a
  walk over the merged symbol set comparing against the original;
  no extra pathway needed.
- **Diagnostics are simpler.** Every effective type has a
  deterministic provenance — origin `.d.ts` plus the specific
  override entries that contributed — recorded at merge time, so
  errors and "why is this method non-mutating?" queries have a
  single place to look.

The cost — designing the override AST and a parser mode for partial
declarations up front — is real but bounded, and is work we'd have
to do for B or D eventually anyway once lifetimes and `throws`
land.
