# ECMA-262-Derived Builtin Annotations: Requirements

## Background

The builtins workstream
([../builtins/requirements.md](../builtins/requirements.md)) owns the
JavaScript standard-library surface as first-class `.esc` files. Its
bootstrap converter (FR10) translates the pinned TypeScript `.d.ts` set
into Escalier declarations and seeds receiver mutability by running
`interop.Classify` — the name-based tiers in
[../../internal/interop/mutability.go](../../internal/interop/mutability.go).
Those tiers are heuristics over method *names*: a `get*` prefix is
non-mutating, a `set*`/`push`/`delete` prefix is mutating, and a
hand-maintained exception table in
[../../internal/checker/prelude.go](../../internal/checker/prelude.go)
(`mutabilityOverrides`) patches the cases the names get wrong.

Name heuristics are guesses. They misclassify methods whose names look
mutating but are not — `String.prototype.replace` returns a fresh
string, yet `replace` is a mutating-prefix, so it needs a hand override
at `prelude.go`. They miss methods whose names carry no signal —
`String.prototype.charAt`, `Object.prototype.propertyIsEnumerable`. And
they cannot speak to non-receiver parameter mutability or to aliasing at
all.

ECMA-262 specifies each builtin as a numbered algorithm. Those
algorithms state the ground truth the heuristics approximate: a method
mutates a value exactly when its algorithm performs a mutating abstract
operation on that value, and a method's return aliases an input exactly
when the algorithm returns that input rather than a freshly allocated
object. This workstream extracts those facts mechanically and feeds them
to the converter as a higher-confidence classification source than the
name heuristics.

## Goals

- Derive, per `std:*` builtin method, three facts from the ECMA-262
  algorithm semantics:
  1. whether the method mutates its receiver (`self` vs `mut self`);
  2. whether it mutates any non-receiver parameter;
  3. whether its return value aliases the receiver or a parameter, as a
     seed for lifetime annotation.
- Emit those facts as a committed JSON artifact keyed by canonical spec
  name, consumed by the bootstrap converter as a classification source
  ranked above the name-based tiers.
- Keep all Escalier-specific logic in Go. The only non-Go component is a
  thin serializer that dumps ECMA-262's parsed control-flow graph to
  JSON; it contains no analysis.
- Make the extraction reproducible and version-pinned, re-run on
  ECMA-262 edition bumps, mirroring how FR10 pins the TypeScript `.d.ts`
  set.

## Non-goals

- **Web and Node builtins.** ECMA-262 covers only the JavaScript
  language surface, the `std:*` packages. DOM and Web APIs are specified
  in WebIDL, where `[Throws]`, `[NewObject]`, and `[SameObject]`
  extended attributes are the analogous machine-readable signals; that
  is a separate extractor for `web:*`. Node builtins have neither and
  stay hand-authored.
- **`throws` annotations.** ECMA-262 abrupt completions could in
  principle seed `throws`, but that is tracked separately under the
  builtins workstream's hand-curation plan
  ([../builtins/requirements.md](../builtins/requirements.md), FR10
  "throws annotations are hand-curated for now"). This workstream emits
  only mutability and alias facts.
- **Porting ESMeta.** We do not reimplement ECMA-262's algorithm-step
  grammar. ESMeta already parses the spec into a control-flow graph; we
  consume that graph and decline to recreate it.
- **Runtime coupling.** Nothing here runs at compile time inside the
  Escalier compiler. The extractor is an offline tool; its output is a
  committed data file.
- **Lifetime *generation* as a primary deliverable.** The alias facts
  are a seed for hand-authored lifetime annotations, not a replacement
  for the lifetime inference and elision rules already in the checker
  ([../lifetimes/requirements.md](../lifetimes/requirements.md)). See
  the confidence ranking below.

## The three determinations and their confidence

The three asks do not carry equal value, and the requirements reflect
that ranking.

1. **Receiver mutability — high confidence, high payoff.** The receiver
   is always `this value`, usually bound via `O ← ? ToObject(this
   value)`. Mutation of that value is explicit in the algorithm. This
   determination retires the `mutabilityOverrides` table for `std:*`
   types and corrects the misses and misclassifications the name
   heuristics produce. It is the primary deliverable.

2. **Parameter mutability — medium confidence, low payoff.** The same
   analysis aimed at parameter-origin values. Genuinely
   param-mutating builtins are rare in ECMA-262; most read their
   arguments. The spec mostly confirms the safe default "param not
   mutated," and catches the few exceptions. Worth emitting for
   completeness; it will not move much. Note that `mut` on a parameter
   unifies invariantly in the checker
   ([../../internal/checker/unify_mut.go](../../internal/checker/unify_mut.go)),
   so an over-eager `mut` param classification is more disruptive at
   call sites than an over-eager receiver tag. Bias param-mut toward the
   non-mutating default and require positive evidence.

3. **Return aliasing — low confidence as a generator, useful as a
   seed.** The algorithm reveals whether the return aliases an input:
   `return O` / `return M` aliases the receiver; a return of a
   freshly-allocated value from `ArrayCreate` / `OrdinaryObjectCreate` /
   `ArraySpeciesCreate` does not. This maps onto the signals the
   checker's lifetime inference already uses, and the elision rules
   (lifetimes Phase 11) already cover the common return-self and
   return-fresh cases without per-method data. The alias facts are a
   secondary output of the same analysis, surfaced to a human editing
   lifetime annotations, not an automatic annotator.

## Functional requirements

### FR1. Mutation vocabulary

A value is *mutated* by an algorithm iff the algorithm performs one of a
fixed set of mutating abstract operations on that value, directly or
transitively:

- property writes: `Set`, `CreateDataProperty`, `CreateDataPropertyOrThrow`,
  `CreateMethodProperty`, `DefinePropertyOrThrow`,
  `OrdinaryDefineOwnProperty`, `DeletePropertyOrThrow`;
- integrity changes: `SetIntegrityLevel`;
- internal-slot writes phrased as "Set *value*.[[Slot]] to …",
  "Append … to *value*.[[List]]", or "Remove … from *value*.[[List]]".

This vocabulary is the source of truth for the analysis and must be
maintained against the spec edition the extractor is pinned to. Adding a
new mutating abstract operation to the spec without adding it here
produces a false non-mutating classification, so the vocabulary list is
itself a reviewed artifact.

### FR2. Origin tagging and transitive mutation

The analysis tags each value in an algorithm with its origin:

- `this value`, including `? ToObject(this value)` and other coercions
  of it, is **receiver-origin**;
- a named formal parameter is **parameter-origin**, tracked per
  parameter index;
- a value produced by an allocating abstract operation
  (`ArrayCreate`, `OrdinaryObjectCreate`, `ArraySpeciesCreate`,
  `OrdinaryObjectCreate`, constructor calls, and the like) is **fresh**.

Origins propagate through `Let x be y` bindings. A method mutates an
origin when a mutating operation from FR1 reaches a value of that
origin.

Mutation may be **transitive**: a method calls a helper abstract
operation that itself performs the mutation. The analysis must compute,
for every abstract operation, whether it mutates its k-th argument, as a
fixpoint over the call graph seeded by the direct mutators in FR1. A
per-method classification that ignored transitivity would miss any
method that delegates its writes to a helper.

### FR3. Internal-slot backing stores

Some builtins mutate through internal slots rather than property
operations — `Map.prototype.set` appends to `M.[[MapData]]`,
`TypedArray.prototype.set` writes through `[[ArrayBufferData]]`. The
analysis must recognize a curated list of slots that constitute an
object's mutable backing store, including at least `[[MapData]]`,
`[[SetData]]`, `[[ArrayBufferData]]`, `[[ArrayBufferByteLength]]`,
`[[TypedArrayName]]`, `[[ViewedArrayBuffer]]`, and `[[WeakRefTarget]]`.
A write to such a slot on a receiver- or parameter-origin value is a
mutation of that value. The list is hand-curated and small; new slots
are added as new collection types enter the spec.

### FR4. Return-alias classification

For each method the analysis records what its return statements alias:

- `receiver` when every reachable return yields a receiver-origin value;
- `param:<n>` when a return yields the n-th parameter;
- `fresh` when returns yield only freshly-allocated or primitive values;
- `union` when different reachable returns alias different inputs.

This is the lifetime seed of the third determination. It is recorded but
not automatically converted into a lifetime annotation.

### FR5. Soundness bias

Consistent with the interop mutability core principle
([../interop_mutability/requirements.md](../interop_mutability/requirements.md),
"Default to mutating"), the extractor biases toward soundness in both
directions of uncertainty:

- A method the analysis cannot classify — prose-only algorithm, host-
  defined behavior, an unrecognized mutation phrasing — is emitted as
  **unclassified**, not as a guess. Unclassified methods fall through to
  the existing name heuristics and hand-curation in the converter.
- Receiver mutability defaults to **mutating** when unclassified, so an
  immutable Escalier value can never call a method the analysis failed
  to prove non-mutating.
- Parameter mutability defaults to **non-mutating** when unclassified,
  because the invariant-unification cost of a wrong `mut` param
  outweighs the rare missed mutation, and the receiver default already
  carries the soundness guarantee for the common case.

Every unclassified method is reported by name so the gap is visible and
auditable rather than silent.

### FR6. Output contract

The extractor emits a committed JSON facts file keyed by canonical spec
name. Each entry records the three determinations and the tier-relevant
provenance:

```json
{
  "Array.prototype.push":  { "mutatesReceiver": true,  "mutatesParams": [], "returns": "fresh",    "classified": true },
  "Array.prototype.fill":  { "mutatesReceiver": true,  "mutatesParams": [], "returns": "receiver", "classified": true },
  "Array.prototype.slice": { "mutatesReceiver": false, "mutatesParams": [], "returns": "fresh",    "classified": true },
  "Map.prototype.set":     { "mutatesReceiver": true,  "mutatesParams": [], "returns": "receiver", "classified": true },
  "String.prototype.replace": { "mutatesReceiver": false, "mutatesParams": [], "returns": "fresh", "classified": true }
}
```

The key space covers prototype methods (`X.prototype.method`), static
methods (`X.method`), and symbol-keyed methods, addressed by FR7.
`classified: false` marks the FR5 fall-through entries; they carry no
mutability claim.

### FR7. Keying and join to converter declarations

The facts file is keyed by spec name; the bootstrap converter holds a
class name plus a method name derived from the `.d.ts`. A normalizer
joins the two and must handle:

- symbol-keyed methods — `Symbol.iterator` in the spec maps to the
  `[Symbol.iterator]` member the converter emits;
- accessor properties — spec getters/setters map to the converter's
  `get`/`set` elements, which carry fixed mutability and must not be
  overwritten;
- overload sets — one TypeScript overload set may map to a single spec
  algorithm; the fact applies to all signatures of the merged method
  element.

Names present on one side and absent from the other are reported,
mirroring FR10's unmapped-symbol fail-safe in the builtins converter.
A spec method with no converter declaration and a converter declaration
with no spec fact are both informational, not fatal, because the spec
and the TypeScript lib drift independently.

### FR8. Integration as a classification source

The converter consumes the facts file as a classification source ranked
**above** the name-based tiers of `interop.Classify`. The resolution
order becomes:

1. explicit author signals already in `Classify` (getters/setters,
   `this: Readonly<T>`, well-known symbols) — unchanged;
2. **ECMA-262 facts** — new, this workstream;
3. `get*` prefix rule — unchanged;
4. name-based heuristics — unchanged, now a fall-through for methods the
   facts file does not classify;
5. default to mutating — unchanged.

The facts source slots in at rung 2 so that explicit author intent still
wins, but spec-derived ground truth overrides every name guess. The
`mutabilityOverrides` table in `prelude.go` becomes redundant for every
`std:*` method the facts file classifies; its entries are removed as the
facts coverage is verified against them (FR9).

### FR9. Validation against the current classification

Before the facts source is trusted, the extractor's receiver-mutability
output is diffed against the union of the current `mutabilityOverrides`
table and the name-heuristic output for the same methods. Every
disagreement is reviewed: either the facts source is correct and the
hand override was a workaround now subsumed, or the facts source has a
bug to fix. The diff is the gate that justifies deleting override
entries.

## Non-functional requirements

- **Pinned spec edition.** The extractor pins a specific ECMA-262
  revision via ESMeta's `-extract:target` flag, which accepts a git tag,
  branch, or commit of the `tc39/ecma262` repository. Re-running on a
  revision bump is the maintenance workflow, parallel to FR10's
  TypeScript `.d.ts` pin.
- **Go owns the analysis.** The mutation, transitivity, alias, and join
  logic is Go. The JVM-dependent component is a thin serializer with no
  analysis logic, run only on a spec bump (see implementation plan §3).
- **Normal builds need no JVM.** The serialized control-flow graph is
  committed, so the Go analysis and the converter run without Java or
  sbt installed. Only regenerating the serialized graph on a spec bump
  requires the JVM toolchain, scoped to `tools/spec-extract/` via a
  per-directory `mise.toml`.
- **Auditability.** Every classification carries its provenance, and
  every unclassified method is listed, so a reviewer can see exactly
  which methods the spec proved and which fell through to heuristics.

## Coverage and limitations

- ECMA-262 covers `std:*` only. `web:*` and `node:*` are out of scope.
- A handful of algorithms are prose-only or host-defined and fall
  through to the name heuristic per FR5.
- Generic and array-like receivers operate on `O ← ? ToObject(this
  value)`; the analysis treats `ToObject(this value)` as
  receiver-origin so that a write to `O` is a write to the receiver.
- Strings, numbers, booleans, bigints, and symbols are immutable
  primitives. Every method on their wrapper classes is provably
  non-mutating because the algorithm coerces to the primitive and builds
  a fresh result. This is where the facts source most clearly beats the
  name heuristics, which misclassify `String.prototype.replace` and miss
  `String.prototype.charAt`.
