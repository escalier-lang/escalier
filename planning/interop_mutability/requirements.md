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
4. **`get`-prefixed methods are non-mutating, with exceptions.** Methods named
   `getX` are assumed non-mutating, except for documented patterns that
   mutate-on-miss: `getOrInsert*`, `getOrUpdate*`, `getOrCreate*`,
   `getOrDefault*` (when defaulting writes back), and similar. (Promoted
   to a core principle rather than a name-based heuristic because the
   prefix is unusually unambiguous in JS/TS APIs and shows up across most
   collection-style libraries.)
5. **Known FP / immutability libraries are fully non-mutating.** For
   well-known functional or immutable-data libraries — Ramda, fp-ts,
   Effect, Immutable.js, lodash/fp, and similar — assume every method is
   non-mutating in both its receiver and its arguments. Ship these as
   library-level overrides alongside the standard-library overrides.
   New libraries can be added by the user via the same override
   mechanism.
6. **TS `readonly` properties are always readonly in Escalier.** A field
   declared `readonly` in a `.d.ts` cannot be assigned to, regardless of
   whether the holding reference is mutable or immutable. This is a
   property-level constraint independent of the receiver's mutability — a
   mutable reference to an object with `readonly foo` still cannot write
   `foo`. (Mutable references can still call mutating methods that modify
   non-`readonly` fields.)

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
- **Consume.** When reading a `.d.ts`, an `@esctype` tag on a symbol is
  the source of truth. The compiler parses the tag value, reconstructs
  the Escalier type, and uses it directly — bypassing every other rule
  in this document for that symbol. This makes Escalier-to-Escalier
  interop lossless across the `.d.ts` boundary.
- **Precedence.** `@esctype` outranks core principles, shipped
  overrides, user overrides, and all heuristics. A symbol classified
  via `@esctype` is treated as fully explicit and never triggers the
  uncertainty warning.
- **TSDoc registration.** `@esctype` is a custom TSDoc tag and must be
  declared so consumer tooling doesn't flag it as unknown. Ship a
  `tsdoc.json` (or equivalent) alongside generated `.d.ts` output, and
  document the tag for users who run TSDoc-aware tooling against it.

## Resolution order

When classifying a symbol's mutability, apply rules in this order and
stop at the first match:

1. **`@esctype` tag** on the symbol (round-trip from Escalier source).
2. **Explicit author signals** — `readonly this`, getters/setters,
   `Readonly<T>` collection variant, `readonly` properties (principle
   #6), well-known symbol methods.
3. **User overrides** for the symbol's module / class.
4. **Shipped overrides** — stdlib (principle covers `Array`/`Map`/`Set`
   readonly variants by shape; explicit override entries cover `Date`,
   `RegExp`, `Promise`, etc.) and known FP libraries (principle #5).
5. **Primitive wrapper classes** (principle #3) — `Number`, `BigInt`,
   `String`, `Boolean` methods are non-mutating.
6. **`get*` rule** (principle #4), with the documented exceptions.
7. **Name-based heuristics** — predicate, conversion, query, copy,
   iteration prefixes (Medium signals); mutating-prefix list reinforces
   the default.
8. **Default to mutating** (principle #1).

Items 2–4 are *explicit signals* for the purposes of the uncertainty
warning; items 5–7 are heuristics, and a non-mutating classification
from any of them counts as uncertain.

## Heuristics

In addition to the core principles above, the compiler applies the
following heuristics, grouped from strongest to weakest signal.

### Strong signals (explicit author intent)

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
- **Standard-library overrides.** For `Date`, `RegExp`, `Promise`,
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

Counter-example (why we still need stdlib overrides): the ES2023 Array
methods `toSorted`, `toReversed`, `toSpliced`, `with` are
**non-mutating** despite containing `sort`/`reverse`/`splice`. The
"prefer mutating" rule would misclassify them; the standard-library
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
(stdlib, well-known FP / immutability libraries per principle #5). The
same machinery serves user-supplied corrections for third-party APIs.

- **Shipped overrides** — bundled with the compiler. Cover standard-
  library classes that don't have a `Readonly*` variant in TS's lib
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

### Override file format

- **Files are `.esc`.** Free syntax highlighting, parser reuse, and
  the override surface looks like the language being overridden.
- **Location & discovery.** Override files live under an `overrides/`
  directory at the root of a package. The compiler walks the following
  locations in order, with later locations winning on conflict:
  1. Shipped overrides bundled with the compiler (resolution-order
     tier 4).
  2. `node_modules/<dep>/overrides/**/*.esc` — overrides shipped by
     a dependency for itself or its own deps. (Allows library
     authors to publish overrides for libraries they wrap.)
  3. The consuming project's own `overrides/**/*.esc` (resolution-
     order tier 3 — user overrides; consuming-project wins).

  Within each location, all `.esc` files under `overrides/` are
  loaded recursively. Subdirectory structure is for the author's
  convenience only; it has no semantic effect.
- **No prescribed file naming or 1:1 mapping to libraries.** A single
  file may contain overrides for multiple libraries; a single
  library's overrides may be split across multiple files. The
  compiler discovers all `.esc` files under `overrides/` recursively.
- **Top-level form.** Override declarations must use the `override`
  keyword at the top level, e.g.:

  ```esc
  override declare module "ramda" { ... }
  override declare module "some-lib" { ... }
  override declare global { class Date { ... } }
  ```

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
- **Partial overrides.** A class or interface body inside an override
  block need only list the members being overridden. Members present
  in the original `.d.ts` but absent from the override remain
  unchanged. The override patches the type rather than replacing it.
- **Overload collapsing.** A `.d.ts` may declare a method with many
  overloads; a single override entry for that method name overrides
  *all* of them. The override becomes the new authoritative signature
  regardless of how many overloads the original had.
- **Override-defined overloads.** If an override file declares the
  same method name multiple times, those declarations form an
  overload set in the resulting type — exactly as if the user had
  written overloads in normal Escalier source.

## Decisions

- **Surfacing ambiguity / strict mode.** Provide an opt-in warning that
  fires whenever a method is called on an immutable reference and the
  classification came from a heuristic (resolution-order steps 5–7)
  rather than an explicit signal (steps 1–4). Off by default; a single
  flag covers both "tell me about uncertainty" and "strict interop."
- **Argument mutation: default to mutating.** Assume any object/array
  passed as a parameter may be mutated by the callee. Later we can refine
  the standard-library override file using MDN as the source of truth
  (e.g. mark `Array.prototype.map`'s callback receiver as non-mutated,
  flag `Object.assign`'s target as mutated). Third-party APIs stay
  conservative until overrides are written.
- **Inheritance is mutability-preserving.** A subclass override is
  assumed to have the same mutability as the base method, regardless of
  which side of the interop boundary base and subclass live on (TS base
  + Escalier subclass, Escalier base + TS subclass, or same-language).
  Don't try to detect divergence; if a library actually diverges, an
  override entry on the specific subclass method can correct it.
