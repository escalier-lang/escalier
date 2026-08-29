# ECMA-262-Derived Builtin Annotations: Requirements

## Background

The builtins workstream
([../builtins/requirements.md](../builtins/requirements.md)) owns the
JavaScript standard-library surface as first-class `.esc` files. Its
bootstrap converter (FR10) translates the pinned TypeScript `.d.ts` set
into Escalier declarations and seeds receiver mutability by running
`dts_to_esc.Classify` — the name-based tiers in
[../../internal/dts_to_esc/mutability.go](../../internal/dts_to_esc/mutability.go).
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

- Derive, per `std:*` builtin method, five facts from the ECMA-262
  algorithm semantics:
  1. whether the method mutates its receiver — the choice between a
     `&self` and a `&mut self` borrow;
  2. each non-receiver parameter's disposition — read-only borrow (`&`),
     in-place mutable borrow (`&mut`), or escape when the parameter is
     stored into the receiver and so must outlive it (spelled a move for
     an owning container or a lifetime-bounded borrow otherwise);
  3. whether its return value borrows the receiver or a parameter, as a
     seed for the return's lifetime and `&` annotation;
  4. which exception types the method can throw synchronously, as a
     candidate set for the `throws` clause
     (`soltype.FuncType.Throws`), pruned of the coercion throws the
     type system already precludes;
  5. which exception types a promise-returning method rejects with, as a
     candidate for the reject type `E` of `Promise<T, E>`
     (`soltype.PromiseType.Err`).
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
- **Throws as a finished, unreviewed annotation.** Emitting the throw
  set is an in-scope deliverable (FR10, FR11), but it ships as a reviewed
  candidate set, not a trusted final annotation. The review need is not
  that the extraction is inaccurate — where an entry carries `throws`,
  FR10's *raw* throw set is a sound, complete enumeration of what
  ECMA-262 models the algorithm as throwing, and could ship unreviewed at
  the cost of noise. Review guards the two things downstream of that,
  both properties of the coercion filter (FR11):
  - **The filter can under-report.** Making the raw set usable means
    *narrowing* it — dropping coercion `TypeError`s — and narrowing is the
    unsound direction: a dropped real throw yields a `throws` clause too
    narrow, so a caller is not forced to handle an exception that can
    occur. This is the opposite of receiver mutability, whose bias
    over-constrains and so auto-applies safely (§FR5); the usable throw
    set under-constrains.
  - **The filter needs type information ECMA-262 does not carry.** Whether
    a parameter coercion can throw depends on the parameter's declared
    type, available only at the FR7 join, plus a judgment about which
    coercions the types truly preclude. Both are what a reviewer supplies,
    so the parameter half of the filter is a curated entry rather than a
    stage of the analysis. Only the receiver half, where the type is always
    statically known, is filtered automatically.

  Host and implementation-defined throws are **out of scope**, not a
  review driver: stack-overflow `RangeError`, out-of-memory, and host
  hooks can occur at essentially any call, so — like an effect system
  declining to track OOM — the model does not enumerate them. This is a
  policy exclusion (see Coverage and limitations), not a gap the spec
  fails to fill, and it applies only to these pervasive host errors; a
  `RangeError` from an explicit spec domain check, such as
  `Number.prototype.toFixed`, is a tracked domain throw.

  The claim is falsifiable and evidence-revisable, and the FR14 validation
  is what would falsify it. This refines the builtins workstream's
  hand-curation plan rather than replacing it
  ([../builtins/requirements.md](../builtins/requirements.md), FR10
  "throws annotations are hand-curated for now"): the spec generates the
  ~50 high-value entries instead of hand-curating them from scratch.
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

## The determinations and their confidence

The asks do not carry equal value, and the requirements reflect that
ranking.

1. **Receiver mutability — high confidence, high payoff.** The receiver
   is always `this value`, usually bound via `O ← ? ToObject(this
   value)`. Mutation of that value is explicit in the algorithm. This
   determination retires the `mutabilityOverrides` table for `std:*`
   types and corrects the misses and misclassifications the name
   heuristics produce. It is the primary deliverable.

2. **Parameter disposition — medium confidence.** The same analysis
   aimed at parameter-origin values, sorting each parameter into borrow,
   mutable borrow, or escape (FR12). Where the analysis positively proves a
   parameter read-only it is `borrow`; where it proves in-place mutation it
   is `&mut`; the escape case — a parameter stored into the receiver, as in
   `Array.prototype.push` and `Map.prototype.set` — is common and cleanly
   detectable from the same store analysis. When the mutation of a
   parameter is *uncertain*, the default is `&mut`, the conservative
   direction (FR5): marking a mutating parameter `&` would let an immutable
   value be mutated at runtime, and the failure of an over-eager `&mut` is
   loud and self-correcting rather than silently unsound. Escape is the one
   axis not defaulted conservatively — it requires positive evidence of a
   store — because assuming an uncertain parameter is consumed is
   disproportionate.

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

4. **Thrown exceptions — medium confidence, curation-grade.** The
   algorithm names the exception types it can raise, directly and
   transitively through `?`-guarded calls. This is mechanically sound
   but over-approximates: most methods coerce the receiver and arguments
   up front, and those coercions can throw `TypeError` on a wrong
   dynamic type that Escalier's static types already rule out. So the
   raw throw set is dominated by type-guard noise. After the coercion
   filter (FR11) the residue is the domain throws worth annotating —
   `RangeError`, `URIError`, `SyntaxError`, and the non-coercion
   `TypeError`s. The output is a curation candidate set, better than
   hand-curating from scratch but not a finished annotation.

## Relationship to Escalier's ownership and effect model

Several Escalier features decide how a value flows through a call — who
owns it, who may write it, how long a returned reference stays valid,
and how a failure propagates. This section states what the facts
determine for each of the features this workstream must respect, and
what they deliberately leave to the typed source.

### Two checkers; the active one is the target

Escalier has two type-checker stacks. The **active** one is the
SimpleSub rewrite in `internal/soltype` + `internal/solver`, extended
with MLstruct — a Boolean algebra of structural types giving first-class
unions, intersections, and negation — where the borrow/move model
(`RefType`), the two-argument `Promise<T, E>`, and exactness live. That
type algebra is directly relevant here: an extracted `throws` or
`rejects` set is a union in it (`TypeError | RangeError`), and the
negation is what expresses the `try`/`catch` residual the effect system
already computes — the uncaught set is the caught types minus the handled
ones (`caught & ~handled`). So the effect facts this workstream produces
land in the MLstruct type lattice, not merely a flat list. The **legacy**
stack is `internal/type_system` + `internal/checker`; it is being
superseded and is not the integration target. Where earlier sections of this document cite legacy files
(`internal/checker/prelude.go`, `internal/checker/unify_mut.go`), read
them as naming the *concept*, not the final home — the facts apply to
whichever checker owns builtin ingestion. Real stdlib ingestion into the
solver is milestone M7.5 (Library type resolution), not yet landed — the
solver prelude still seeds stdlib types as opaque placeholders — so the
concrete point where the facts are consumed tracks wherever the builtins
workstream lands that ingestion. The converter's classifier `dts_to_esc.Classify`
([../../internal/dts_to_esc/mutability.go](../../internal/dts_to_esc/mutability.go))
operates on `dts_parser` declarations and is checker-agnostic, so FR8's
integration there holds across both.

### Immutability by default, and how the facts serve it

Escalier values are immutable by default; `mut` opts in
([../../docs/08_mutability.md](../../docs/08_mutability.md)). But
TypeScript `.d.ts` types default to *mutating* at the import boundary —
`dts_to_esc.Classify`'s tier 7 is "default mutating." The
receiver-mutability facts exist precisely to recover the correct split,
so an immutable Escalier value can call the many builtin methods that do
not mutate. The facts are what make immutability-by-default usable
against a mutable-by-default type source; they do not change the
default, they inform it.

### Receiver and parameters in the borrow/move model

A method receiver is always **borrowed**, never consumed — calling a
method does not take ownership of the object. So the receiver fact maps
onto the borrow model directly: a non-mutating method borrows immutably
(`&self`), a mutating one borrows mutably (`&mut self`). "Receiver
mutability" (FR2) is the choice between those two.

Parameters are richer, because the affine model
([../affine_semantics/requirements.md](../affine_semantics/requirements.md))
distinguishes three dispositions:

- **borrow (`&`)** — the method only reads the parameter. The default.
- **mutable borrow (`&mut`)** — the method writes the parameter object
  in place, but the caller keeps it. `Reflect.set` writes its `target`.
- **escape** — the method stores the parameter into the receiver, so it
  outlives the call. `Array.prototype.push` and `Map.prototype.set` store
  their arguments into the receiver's backing store; the argument escapes
  into the array or map and its lifetime must be at least the receiver's.

The escape is the fact the algorithm gives; how it is *spelled* in the
signature is a separate, typed decision, and this is the important
subtlety. Storing a value into the receiver forces the value to outlive
the receiver, and there are two ways to guarantee that:

- **move (owned parameter)** — when the container owns its elements
  (`Array<T>`, `Map<K, V>`), storing transfers ownership into the
  container, and that transfer *is* how the value's lifetime extends to
  the container's; no lifetime is written and the caller gives the value
  up. This is the default spelling.
- **lifetime-bounded borrow (`&'a T`, no move)** — when the container's
  slot is itself a borrow (`Array<&'a T>`), storing a reference transfers
  no ownership; it only requires `'a` to outlive the container, and the
  caller keeps the value.

A move and a lifetime bound are two encodings of the same constraint
"the value outlives the receiver." Which encoding applies depends on the
container's element ownership — a property of the *typed* signature, not
of the untyped algorithm — so it is the FR7 boundary again (see FR12).
The affine checker today defaults to the owning/move model; borrow-into-
container tracking (`.push` of a borrow) is deferred there, so `move` is
the implemented spelling and the lifetime-borrow alternative is partly
future work.

The algorithm reveals only the escape, and it is the same store-detection
as the mutation analysis (FR1–FR3) read against parameter-origin values:
a parameter written into a receiver- or longer-lived-origin object
escapes; a parameter mutated in place is a mutable borrow; a parameter
only read is a borrow. See FR12.

Value types — `number`, `string`, `boolean`, functions, promises — are
never `RefType` and are copied, never moved. So a stored primitive is a
copy at runtime even where the escape would otherwise be spelled a move;
the checker resolves copy-versus-move-versus-borrow per instantiation.
The fact records the escape; the typed source and the model handle the
spelling.

### Return values as borrows with lifetimes

FR4's return-alias is the lifetime seed. In the borrow model a method
that returns the receiver (`return O`) returns a borrow tied to the
receiver's lifetime; a method returning a freshly-allocated value returns
an owned value; a method returning a parameter returns a borrow threaded
to that parameter's lifetime. The `receiver` / `param` / `fresh` /
`union` alias kinds map onto return-borrows-receiver,
return-borrows-parameter, return-owned-fresh, and a lifetime union.

Three things the plain alias edge does **not** settle, so it seeds
hand-authored `&` and lifetime annotations rather than auto-generating
them:

- **The returned receiver-borrow inherits the receiver's mutability.**
  A method returns the receiver to be chained after mutating it —
  `Array.prototype.fill`, `sort`, `reverse`, `Map.prototype.set` — and
  each already took `&mut self`, so the return is `-> &mut Self`. That is
  what keeps a fluent chain's mutability constant: every link takes
  `&mut self` and hands back `&mut Self`, so `arr.fill(0).reverse()` stays
  `mut` throughout. The rule is that the return-borrow carries the
  receiver's mutability, not a fixed one. The harder case — a
  *non-mutating* method that returns self and should preserve whatever
  mutability the caller had — needs mutability polymorphism (a return
  mutability abstracted over the receiver's), which the affine checker
  does not express today. No ECMA-262 builtin hits it: every builtin that
  returns `this` mutates, so `-> &mut Self` covers them all. The
  polymorphic case is deferred to the affine_semantics workstream, out of
  this workstream's scope.
- **Whether a lifetime applies at all depends on the return type.** A
  value-typed return has no lifetime — `String.prototype.charAt` returns a
  fresh string value, `indexOf` a number — so the alias kind is moot
  there. Only a reference-typed return carries a borrow/lifetime, and
  reference-versus-value is the return **type**, which comes from the
  typed source at the FR7 join, not from the shape-free fact.
- **Elision already covers the common shapes.** Escalier's lifetime
  elision infers the lifetime for return-self (`&self`/`&mut self` in,
  borrow of self out) and needs none for return-fresh (owned). So FR4 adds
  value only where elision gives up — a return that borrows a *non-receiver
  parameter* (`param`) or mixes inputs (`union`) — and those are rare in
  ECMA-262 (`Object.assign` returning its `target`; `Promise.resolve`
  returning its argument or a fresh promise). Even there the concrete
  annotation needs the typed signature (the borrow mutability, the union's
  lifetime variables), so it is review input, not an auto-annotator.

### Synchronous throws versus asynchronous rejections

Escalier splits the exceptional exit into two channels
(`internal/soltype/type.go`): `FuncType.Throws` for a synchronous
`throws T` clause, and `PromiseType.Err` for the reject type `E` of a
two-argument `Promise<T, E>`. An `async fn` has no `throws` clause; its
body's failures populate `E`. The throw-set extraction routes
accordingly: a synchronous `Throw` step becomes a `throws` candidate
(FR10), while a rejection of the returned promise becomes a
`Promise<T, E>` reject-type candidate (FR13). A promise-returning builtin
can carry both — synchronous argument validation in `throws`, a rejection
in `E`.

**Generators and async generators fit the same split, observed one step
later** — at the iterator protocol rather than at the call, because a
generator's body does not run when the generator is created; it runs when
the consumer drives it.

- A **generator** (`Generator<T, R, N>`) surfaces its body's failures
  **synchronously** at `.next()` / `.return()` / `.throw()`, so they land
  in the `throws` clause of those methods — the same synchronous channel
  as any other method.
- An **async generator** (`AsyncGenerator<T, R, N>`) has `.next()` return
  a `Promise<IteratorResult<T, R>, E>`, so its body's failures surface as
  **rejections** of that per-`next` promise — the `E` channel, exactly as
  for an `async fn`.

The extraction needs no special case for either: FR13's routing keys on
*which sink* a raised value reaches — a synchronous abrupt completion or a
promise `[[Reject]]` — and a generator method's algorithm reaches the
first while an async generator method's reaches the second. One practical
limit: the generic `%GeneratorPrototype%.next` re-surfaces whatever the
*generator body* threw, which is a type parameter of the generator
instance, not a concrete set the per-method extractor can name; ECMA-262's
own builtin iterators (`Array.prototype[Symbol.iterator]`,
`Map.prototype.entries`, …) throw little, so this mostly bites user-defined
generators, not the builtin surface.

### Exactness is out of scope for the facts

Object-type exactness — whether a type permits extra properties — is
decided by the shape and its provenance, not by the algorithm. Escalier
source objects are exact by default; types imported from TypeScript are
inexact by default
([../exact-types/requirements.md](../exact-types/requirements.md) §8–9),
overridable per use with `Exact<T>`. Nothing in an ECMA-262 algorithm
determines exactness, and the facts carry none. Exactness belongs to the
typed source and the import boundary — the same FR7 boundary that owns
the generic type shapes.

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

This is the lifetime seed of the third determination. In the borrow
model a `receiver` return is a borrow tied to the receiver's lifetime and
**carrying the receiver's mutability** (`&mut Self` for the mutating
self-returning builtins, which keeps fluent chains `mut`), a `param:<n>`
return is a borrow tied to that parameter's lifetime, `fresh` is an owned
return, and `union` is a lifetime union. It is recorded but not
automatically converted into a `&`/lifetime annotation: the alias edge
under-determines the annotation (return type reference-vs-value from the
FR7 join, the borrow mutability, union lifetime variables), and elision
already covers `receiver` and `fresh`, so FR4 mainly informs the rare
`param`/`union` cases. See the ownership-model section above.

### FR5. Soundness bias

When the analysis has no signal, the default is **conservative — assume
the effect** — so that a wrong default fails *loudly* rather than
*silently*. A wrong conservative default (say, `&mut` on a read-only
parameter) surfaces as call-site friction: a caller that cannot pass an
immutable value asks for the type to be corrected, which drives the
annotation toward accuracy. A wrong permissive default (say, `&` on a
mutating parameter) fails silently as latent unsoundness that surfaces
much later as a bug. Loud beats silent. There are exactly two documented
exceptions, where the conservative direction is impractical rather than
merely stricter.

**Conservative defaults — assume the effect when uncertain:**

- A determination the analysis cannot make — a prose-only algorithm, host-
  defined behavior, an unrecognized mutation phrasing — is emitted as
  **unclassified**, not as a guess. Coverage is per determination, so a
  method withholds only the axes that read the step it could not resolve.
  An axis that reads no step is never withheld: a static and a namespace
  function have `receiver: none` whatever the analysis missed.
- An unclassified determination is answered by **review**, recorded as a
  curated entry keyed by the same canonical spec name and merged over the
  analysis one axis at a time. What neither the analysis nor a curated entry
  answers is not published: generation fails, naming the method and the axis,
  so the gap is closed before anything consumes it. The name heuristics stay
  the fallback for a method no entry addresses at all.
  Curating an answer is the cheaper of the two ways to close a gap, and it
  states a reason the next reader can check, where a new lattice member in
  the analysis states only a result.
- Receiver mutability defaults to **mutating** (`&mut self`) when
  unclassified, consistent with the interop core principle "default to
  mutating"
  ([../interop_mutability/requirements.md](../interop_mutability/requirements.md)).
  An immutable Escalier value can never call a method the analysis failed
  to prove non-mutating.
- Parameter mutability defaults to **mutable borrow** (`&mut`) when
  uncertain — the same conservative direction as the receiver, and for the
  same soundness reason: marking a mutating parameter `&` would let an
  immutable value be passed and mutated at runtime. `&mut` refuses that by
  demanding a mutable argument. The cost is real — most parameters are
  read-only, so this default is wrong more often than right and imposes
  friction across the un-analyzed surface (`web:*`, `node:*`, third-party,
  any `.d.ts`-only method) where it, not a real signal, decides the
  disposition — but the failure is loud and self-correcting, and it keeps
  the policy uniformly conservative, matching the receiver.

**Documented exceptions — where the conservative direction is
impractical:**

- **The `escape` disposition is gated on positive evidence, not
  defaulted.** Full conservatism would assume an uncertain parameter is
  stored into the receiver and mark it `escape` (owned/consuming), but that
  is disproportionate — it makes the caller give the value up entirely — so
  `escape` requires the store analysis to actually find the parameter
  written into a longer-lived place (FR12). This leaves a small residual
  unsoundness on the escape axis for uncertain parameters, accepted
  deliberately; the mutable-borrow default above still protects the more
  common mutation case.
- **The throw and reject sets default toward under-reporting.** The
  conservative direction here would be to *over*-report — but that means
  every `this`-touching method carries `throws TypeError` from its
  receiver coercion, pervasive noise a parameter default does not produce.
  So the throw set (FR10) and reject set (FR13) under-report instead,
  accepting that this is the unsound direction. Both are curation-grade,
  and FR14 measures what reaches the `.esc` against observed behavior.

These defaults are applied by the **converter**, not serialized as facts,
and the converter applies one only where no fact addresses the method at
all. A published fact answers every determination it carries: an axis
neither the analysis nor review settles fails generation, naming the method
and the axis, rather than being written as a hole. So the format cannot
conflate "the analysis proved this method borrows and throws nothing" with
"the analysis could not tell" — the second never reaches the file. A
proven-empty result is an empty effect field, and there is no encoding for
an unanalyzed one. FR6 and Appendix B use this one contract.

Failing generation is the loud direction, which is the same reasoning as
the defaults above. A coverage flag would leave the hole in the data for
every consumer to remember to check, and §7 auto-applies the receiver, so a
forgotten check there becomes a silent `&mut self`.

Splitting coverage this way keeps the conservative bias where it buys
safety and drops it where it does not. A method's receiver mutability is
auto-applied, so a missed mutation must cost that claim. `returns` is
FR4's lifetime seed, recorded for curation rather than applied, so a
method whose only problem is a possible hidden mutation still publishes
it. A static's `receiver: none` reads no step at all, so nothing the
analysis missed can put it in doubt.

Every method whose receiver mutability is unclassified is reported by name
so the gap is visible and auditable rather than silent. The report is
taken over the analysis alone as well as over the merged result. The first
is what the analysis is measured by, and the second is what actually
reaches the heuristics.

### FR6. Output contract

The extractor emits a committed JSON facts file keyed by canonical spec
name. Each entry records the five determinations and the tier-relevant
provenance. `receiver` is `borrow` (`&self`), `mutBorrow` (`&mut self`),
or `none` (a static or namespace function with no receiver). `params`
lists only the parameters the analysis found `mutBorrow` (`&mut`) or
`escape` (stored into the receiver, spelled at curation — see FR12).
A parameter omitted from the entry was proven read-only (`borrow`). That
omission is distinct from the FR5 uncertain default, which is `&mut`: the
omission means "the analysis showed this parameter is only read," whereas
the FR5 default applies only where no entry addresses the method.

**Every entry is total.** A determination neither the analysis nor review
settles is not encoded. Generation fails instead, naming the method and the
axis on stderr, so the file carries no coverage flags and a consumer never
has to tell a hole from a claim.

```json
{
  "Array.prototype.push":  { "receiver": "mutBorrow", "params": [{"index":0,"disposition":"escape"}], "returns": "fresh",    "throws": [], "rejects": [] },
  "Array.prototype.fill":  { "receiver": "mutBorrow", "params": [],                                    "returns": "receiver", "throws": [], "rejects": [] },
  "Array.prototype.slice": { "receiver": "borrow",    "params": [],                                    "returns": "fresh",    "throws": [], "rejects": [] },
  "Map.prototype.set":     { "receiver": "mutBorrow", "params": [{"index":0,"disposition":"escape"},{"index":1,"disposition":"escape"}], "returns": "receiver", "throws": [], "rejects": [] },
  "Number.prototype.toFixed": { "receiver": "borrow", "params": [],                                    "returns": "fresh",    "throws": ["RangeError"], "rejects": [] },

  "Reflect.set":              { "receiver": "none", "params": [{"index":0,"disposition":"mutBorrow"},{"index":2,"disposition":"escape"}], "returns": "fresh", "throws": [], "rejects": [] },
  "Intl.getCanonicalLocales": { "receiver": "none", "params": [],                                    "returns": "fresh",    "throws": ["RangeError"], "rejects": [] },

  "String.prototype.toLowerCase": { "receiver": "borrow", "params": [], "returns": "unknown", "throws": [], "rejects": [] }
}
```

- Every entry carries every determination, so there is no coverage object
  and no absent field to interpret. `String.prototype.toLowerCase` reaches
  the Unicode case-mapping table through a prose step, so the analysis
  cannot say what that step wrote; the `borrow` above comes from review,
  and without it generation would fail rather than publish the method. Its
  `returns: unknown` is a value the analysis did produce, meaning the walk
  tied the return to nothing the caller holds. A static keeps
  `receiver: none` through any warning, since its having no receiver is a
  fact about the declaration rather than about a step.
- `receiver` maps a non-mutating method to `&self` and a mutating one to
  `&mut self` (FR2). A method that stores an argument into the receiver
  marks that parameter `escape`, because the argument outlives the call
  inside the longer-lived receiver: `Array.prototype.push` stores its
  element, `Map.prototype.set` stores both key and value (FR12). Whether
  `escape` is spelled a move or a lifetime-bounded borrow is settled at
  the FR7 join from the container's element type.
- The `throws` array holds the synchronous exceptions that survive the
  coercion filter (FR11). `Number.prototype.toFixed` throws `RangeError`
  when `fractionDigits` is out of the 0–100 range — a domain throw the
  type system cannot preclude — while the `TypeError` from coercing a
  non-number receiver is filtered out. Each entry is a standard error-class
  name, an origin ref (`param:k` / `receiver`), or `"unknown"` (FR13).
- The `rejects` array holds the asynchronous reject types for a
  promise-returning method, the candidate for `Promise<T, E>`'s `E`
  (FR13), in the same entry forms as `throws`. It is empty for a
  non-promise method. For ECMA-262 `std:*` it is usually empty or a
  forwarded element `E` (recorded as an origin), because the core language
  has few builtins with a concrete domain rejection; the channel matters
  more for the future `web:*` extractor, where `fetch` and friends reject
  with concrete types.

The host portion of a key may be a **namespace-qualified dotted path**,
not just a single class name. Three forms arise:

- `Intl.DateTimeFormat.prototype.format` — a prototype method of
  `DateTimeFormat`, a constructor nested in the `Intl` namespace. It has
  a receiver, a `DateTimeFormat` instance, exactly like any other
  prototype method.
- `Intl.getCanonicalLocales`, `Math.max`, `JSON.parse` —
  **namespace-level functions**. They have `receiver: none`, so the
  signal of interest is `params`. `Intl.getCanonicalLocales` still
  carries a domain `throws` (`RangeError` on a malformed language tag).
- `Reflect.set`, `Reflect.defineProperty`, `Reflect.deleteProperty` —
  namespace functions that touch a parameter: `Reflect.set` writes its
  `target` in place (`mutBorrow`, index 0) and stores its `value` into
  that target (`escape`, index 2). The analysis models every function
  uniformly over parameter indices (FR2), so a namespace function with no
  receiver still reports parameter dispositions through the same
  machinery. `Reflect.set`'s optional fourth argument `receiver` (index 3)
  is the `this` a triggered setter runs with; whether that setter writes
  through it is conditional on the target property being an accessor and
  unknowable statically, so index 3 is left **unresolved** — the `borrow`
  default — rather than guessed. The analysis does not express conditional
  dispositions.

The key space therefore covers prototype methods
(`X.prototype.method`), static methods (`X.method`), symbol-keyed
methods, and namespace-qualified forms where `X` is a dotted path naming
a namespace and optionally a nested constructor — all joined by FR7.
Every entry answers every determination, so a fall-through to the FR5
defaults happens only where no entry addresses the method — an unmatched
`std:*` declaration, or the `web:*` and `node:*` surfaces that are out of
scope by construction.

### FR7. Keying and join to typed declarations

**Why a join is needed at all.** ECMA-262 is untyped. It specifies
algorithm semantics — what a method mutates, throws, and returns — but
carries no static type information: no generics (`Array<T>`,
`Map<K, V>`), no parameter or return types, no typed overloads.
`Array.prototype.push` in the spec takes "a List of ECMAScript language
values," not `(...items: T[]): number`. So the spec cannot be the source
of the type signatures; it is the source only of the Escalier-specific
semantics this workstream extracts. The facts file is therefore
deliberately shape-free — keyed by spec name, carrying mutability,
aliasing, and throws but no types — and must be joined to a separate
source that holds the typed declarations. That typed source supplies the
signatures; the facts supply the annotations layered on top.

**The intended direction is to keep `.d.ts` as the type source and
generate `.esc`, not to hand-author `.esc` signatures.** The type shapes
come from the pinned TypeScript `.d.ts` — TypeScript's hand-curated
generic signatures and JSDoc, maintained upstream and regenerated on a TS
bump — and the effect annotations come from this workstream's ECMA-262
facts, regenerated on a spec bump. A generated `.esc` builtin is the join
of those two, plus a small hand-curated override layer for the
curation-grade residue (reviewed throws and lifetimes) that is re-applied
at generation rather than edited into the output.

Curated data enters at two points, and they answer different questions.
The **fact layer** answers a determination the spec extraction cannot
settle, and it is keyed by canonical spec name and merged into the facts
before the join. The **override layer** answers what the facts under-report
by design, the annotations FR5 makes the extractor omit, and it is keyed
by declaration and applied after the join. Both are committed data
re-applied at generation. A determination the facts could carry belongs in
the first, so the second never has to restate a fact. This keeps signatures
out of hand-maintenance entirely, which is the whole point: hand-authored
`.esc` signatures are the path to **avoid**, because they would drift from
the upstream types and multiply the maintenance surface.

Mechanically the ECMA-262 workstream stays agnostic to this decision: the
join is purely name-based, so it targets whatever holds the typed
declarations — the `.d.ts`-derived names at generation, or an `.esc` file
if one existed — unchanged. The doc records the decision; it does not
depend on it.

**Cross-workstream alignment.** This preference diverges from the
builtins workstream as written: its FR10
([../builtins/requirements.md](../builtins/requirements.md)) treats the
generated `.esc` files as hand-edited and "maintained as source going
forward," with converter re-runs made additive-only so they never clobber
those edits. Keeping `.esc` a pure generated artifact — with the human
input confined to a re-applied override layer — drops that
"don't-overwrite-my-edits" constraint and makes regeneration
deterministic, but it is a change to the builtins workstream's
maintenance model, not only to this join. Adopting it means updating
builtins FR10 to match; tracked here as a cross-workstream dependency.

A normalizer maps a spec key onto the typed declaration's owner and
member and must handle:

- symbol-keyed methods — `Symbol.iterator` in the spec maps to the
  `[Symbol.iterator]` member the converter emits;
- accessor properties — spec getters/setters map to the converter's
  `get`/`set` elements, which carry fixed mutability and must not be
  overwritten;
- overload sets — one TypeScript overload set maps to a single spec
  algorithm; the algorithm-level facts apply to every signature, while
  the type-dependent parts resolve per overload (FR15);
- namespace-qualified hosts — the host before `.prototype.` or the bare
  member name may be a dotted path. The normalizer splits it: leading
  segments naming a namespace (`Intl`, `Math`, `Reflect`, `JSON`,
  `Atomics`, `WebAssembly`) route to the owning `std:*` package; a
  trailing constructor segment (`Intl.DateTimeFormat`) routes to a class
  in that package; a member with no constructor segment
  (`Intl.getCanonicalLocales`, `Math.max`) is a namespace-level function
  joined to a free function in the package. A namespace function has
  `receiver: none`, so only its `params`, `throws`, and `rejects` apply.

Names present on one side and absent from the other are reported,
mirroring FR10's unmapped-symbol fail-safe in the builtins converter.
A spec method with no converter declaration and a converter declaration
with no spec fact are both informational, not fatal, because the spec
and the TypeScript lib drift independently.

### FR8. Integration as a classification source

The converter consumes the facts file as a classification source ranked
**above** the name-based tiers of `dts_to_esc.Classify`. The resolution
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

### FR10. Throw-set extraction

For each builtin method the analysis computes the set of exception types
the algorithm can raise, by the same inter-procedural fixpoint as the
mutation summary (FR2) over the same control-flow graph, with a throw
transfer function:

- a `Throw a *T* exception` step contributes the named error class `T`
  (one of `TypeError`, `RangeError`, `SyntaxError`, `ReferenceError`,
  `URIError`, `AggregateError`); a `throw` of a non-constructed value
  (`throw <arg>`) instead contributes that value's **origin** — `param:k`
  or `receiver` — by the FR13 origin rule, resolved to a type at the join;
- a call guarded by `?` contributes the callee's entire throw set,
  because `?` propagates any abrupt completion; when the callee is a
  **function-typed parameter** (a callback, e.g. `? Call(callbackfn, …)`)
  it has no throw set in the CFG, so the call instead contributes
  `throwsOf:param:k` — the callback's throws, resolved at the join to
  throws polymorphism (FR13);
- a call guarded by `!` contributes nothing, because `!` asserts the
  operation never returns an abrupt completion;
- a plain unguarded call whose result is not completion-checked
  contributes nothing to the throw set on that path.

The `?` / `!` / plain distinction is essential and must be carried from
the spec into the control-flow graph. This is why throws extraction
relies on ESMeta's completion-record modeling rather than the shallow
`spec.html` fallback, which would have to recover the guards from markup
itself. A method whose throw paths cannot be resolved is left out of the
throw set and flagged, never guessed. The FR5 bias applied to throws is
to under-report rather than over-report a throw the type system would
force a caller to handle.

### FR11. Coercion filter

The raw throw set from FR10 over-approximates, because nearly every
algorithm begins by coercing its receiver and arguments, and those
coercions throw `TypeError` on a wrong dynamic type. Escalier's static
types already preclude those paths, so the raw set is dominated by
`TypeError`s a well-typed caller can never trigger.

The filter discounts a throw when its origin is a coercion of an
already-typed value:

- `TypeError` raised inside `ToObject(this value)` or
  `RequireObjectCoercible(this value)`. The receiver type is statically
  known, so the null/undefined-receiver path is unreachable.
- `TypeError` raised inside `ToString`, `ToNumber`, `ToNumeric`,
  `ToPrimitive`, or `ToObject` applied to a parameter whose Escalier
  type is already the coerced type.

A throw survives the filter when it originates from an explicit domain
check — an `If <condition>, throw a *RangeError*` over a value range, a
`SyntaxError` from a parse step, a `URIError` from a decode step, or a
`TypeError` not attributable to a receiver or parameter coercion. The
surviving set is what FR6 records in `throws`. The filter is a heuristic
over throw provenance, so its decisions are reported per method for
review, consistent with the curation-grade confidence of this
determination.

The parameter types the filter reads come from the **typed source at the
FR7 join**, not from the spec — the pinned TypeScript `.d.ts` that FR7
keeps as the type source. That is why the parameter branch of the filter
runs after the join — before it, the shape-free facts carry no types to
consult, so the filter can only clear receiver coercions (whose type is
always known) and must keep every parameter coercion. The filter's soundness therefore inherits the typed
source's precision: a loose type (`any`, `unknown`) is safe — the filter
keeps the coercion throw, only noisier — but a type that is wrong-but-
tight could drop a throw that can occur, an unsoundness the FR14
validation is built to catch. For this reason FR14's ground truth is
curated independently of the `.d.ts`, not derived from it.

### FR12. Parameter disposition

For each non-receiver parameter the analysis assigns one of three
dispositions in Escalier's affine model
([../affine_semantics/requirements.md](../affine_semantics/requirements.md)):

- **escape** — a parameter-origin value is stored into a receiver- or
  otherwise longer-lived-origin object, through a property write or a
  backing-store slot write (FR1, FR3). The value escapes into that
  object, so its lifetime must be at least the receiver's.
  `Array.prototype.push` stores its element into the receiver array;
  `Map.prototype.set` stores key and value into `M.[[MapData]]`;
  `Reflect.set` stores `value` into `target`.
- **mutBorrow** (`&mut`) — a parameter-origin object is mutated in place
  by a mutating operation from FR1 but is not stored anywhere, so the
  caller keeps it. `Reflect.set` writes a property on its `target`.
- **borrow** (`&`) — the parameter is only read. The default.

**`escape` is the fact; `move` and a lifetime-bounded borrow are its two
spellings.** The algorithm only tells us the value is stored into the
receiver and therefore must outlive it. Whether the signature spells that
as a `move` (an owned parameter, when the container owns its elements —
`Array<T>`, `Map<K, V>`) or as a lifetime-bounded borrow (`&'a T` with
`'a` at least the receiver's lifetime, when the container's slot is itself
a borrow — `Array<&'a T>`) depends on the container's element ownership.
That is a property of the *typed* signature, not of the untyped
algorithm, so the choice is made at the FR7 join, not by the extractor.
`escape` records the raw constraint; curation picks the spelling. The
default is `move`, and the affine checker currently implements that
spelling; borrow-into-container tracking is deferred there, so the
lifetime-borrow spelling is partly future work — tracked as an external
dependency in the implementation plan's "Discovery phases may grow the
plan" list.

The disposition reuses the FR1–FR3 store analysis: it is the mutation
detector run against parameter-origin values, plus a check of *where* the
stored value came from. A parameter that is both mutated in place and has
another value stored into it stays `mutBorrow` for its own object; the
stored *value* parameter is what carries `escape`. Value-typed arguments
— `number`, `string`, functions — are copied at runtime, never moved,
even where an `escape` would otherwise be spelled a move; the checker
resolves copy-versus-move-versus-borrow per instantiation (see the
ownership-model section above), so the fact records `escape` without
committing to a spelling.

Disposition follows the FR5 defaults for an uncertain parameter: the
mutation axis defaults to `&mut` (the conservative direction — a wrong
`&mut` fails loudly at the call site and is corrected, where a wrong `&`
would be silently unsound), while `escape` is gated on positive evidence
of a store rather than defaulted, since assuming an uncertain parameter is
consumed is disproportionate. Being curation-grade does not make a default
safe; it only means a wrong default is *reviewed* before it ships, so the
default still points the conservative way on the mutation axis.

### FR13. Asynchronous reject channel

A promise-returning builtin fails in two distinguishable ways, and the
analysis routes them to two different Escalier slots
(`internal/soltype/type.go`):

- a **synchronous** `Throw` step, executed before or outside the promise
  machinery, is a `throws`-clause candidate (FR10);
- an **asynchronous rejection** — the algorithm calling the created
  promise capability's `[[Reject]]`, an `IfAbruptRejectPromise` step, or
  returning an already-rejected promise — is a candidate for the reject
  type `E` of `Promise<T, E>` (`soltype.PromiseType.Err`), recorded in
  `rejects`.

The two channels use the same throw-set fixpoint (FR10); they differ
only in the sink a raised value flows to, mirroring how the checker
treats `FuncType.Throws` and `PromiseType.Err` as twins. The same
coercion filter (FR11) applies to the reject channel, since a rejection
that merely propagates a receiver/parameter coercion `TypeError` is
precluded by static typing just as a synchronous one is.

**Thrown and rejected values are recorded by origin, not only by name.**
Both `throws` and `rejects` are Escalier types, and a promise may reject
with any value, not just an `Error` subclass. Every ECMA-262 `Throw a *T*
exception` step names one of the six standard error constructors, so an
extracted value the spec **constructs** is an error-class name and the
string form is faithful. The values the string form cannot name are the
ones ECMA-262 **propagates** rather than constructs — and for those the
fact records the value's **origin**, so the FR7 join substitutes the real
type instead of collapsing to a bare `"unknown"`:

- **Direct propagation of a parameter or the receiver** — `Promise.reject(r)`
  rejecting with its argument, or a synchronous `throw <arg>`. The raised
  value's origin is `Param(k)` or `Receiver` by the same origin tagging
  the mutation and disposition analyses use (FR2), applied to the
  raised-value operand. The fact records `param:k` / `receiver`, and the
  join resolves it to that formal's declared type — `Promise.reject<E>(reason:
  E)` becomes `Promise<never, E>`. It is only as precise as that declared
  type — `.d.ts` types `Promise.reject`'s `reason` as `any`, lowered to
  `unknown` by the builtins converter's `any`-lowering policy
  ([../builtins/requirements.md](../builtins/requirements.md) FR17), so
  the immediate result is still `unknown` there — but
  recording the origin makes the curation upgrade mechanical: generalize
  the parameter to `E` and the reject follows automatically.
- **Promise combinators, hand-modeled** — `Promise.all`, `Promise.race`,
  `Promise.any`, and `Promise.allSettled` forward the reject type of their
  *element* promises, but that value arrives through the promise-resolution
  machinery, not a traceable origin, so origin tagging alone cannot see it.
  Each is a hand-modeled rule (the same class of hand-modeling §9.3 uses
  for `IfAbruptRejectPromise`):
  - `Promise.all` and `Promise.race` reject with the **union of the element
    promises' `E`** — `all<T, E>(Iterable<Promise<T, E>>) -> Promise<T[], E>`;
  - `Promise.any` rejects with an **`AggregateError` aggregating the element
    promises' `E`** — its `errors` list is `E[]`, a parameterized
    `AggregateError<E>` if the surface is generalized;
  - `Promise.allSettled` **never** rejects from element rejections — it
    captures each as `{status, value/reason}` data, so its element channel
    is `never`.
- **Callback effects, parametric** — a method that `?`-calls a
  function-typed parameter propagates *that callback's throws*, not a
  value. `Array.prototype.forEach` / `map` / `filter` / `reduce` /
  `sort` do `? Call(callbackfn, …)`, so the method throws whatever the
  callback throws. The fact records `throwsOf:param:k` — an *effect*
  origin naming the callback parameter, the higher-order analogue of the
  value origins above. At curation this becomes **throws polymorphism**: a
  type parameter `E` on the callback's throws, threaded to the method's own
  throws — `map<U, E>(cb: (…) -> U throws E) -> [U] throws E`. A
  non-throwing callback instantiates `E = never` (the method is
  non-throwing at that call), a throwing one propagates its type — the
  precise "may or may not throw" behavior, without the over-approximation
  of `throws unknown` or the unsoundness of dropping it. `.d.ts` carries no
  throws, so the throws-polymorphic signature is a curation enrichment
  (§11) that the `throwsOf:param:k` fact makes mechanical.
- **Unresolvable origin** — a propagated value the analysis can neither name
  nor trace falls back to the sentinel `"unknown"`, filled from the typed
  signature at curation.

So a value in `throws` or `rejects` is a standard error-class name, an
origin ref (`param:k`, `receiver`, a combinator's element-`E` form, or the
callback-effect `throwsOf:param:k`), or `"unknown"`; FR13 is deliberately
not restricted to error-only facts.

For ECMA-262 `std:*` the reject channel is usually empty or a forwarded
element `E` (now recorded as an origin, above); argument-validation
failures are coercion type-guards the filter drops. Concrete domain
rejections are a `web:*` concern — `fetch` rejecting with a network
`TypeError`, `Response.prototype.json` rejecting with a `SyntaxError` — so
FR13 is specified here for completeness and to fix the
`throws`-versus-`rejects` split, but it delivers most of its value once
the WebIDL extractor lands.

### FR14. Throws validation

Whether a method's published `throws`/`rejects` is right is an empirical
question, settled by measurement rather than asserted. FR14 is the throws
counterpart of FR9's mutability validation: diff the published throw sets
against a ground-truth sample of high-value methods and measure two rates.
The sample must be **independent of the spec extraction**. A corpus read
out of the same algorithm would agree by construction, so it is seeded by
dynamic observation in a real engine, fuzzing each method and recording
what it throws or rejects with, rather than by re-reading the spec. Only
the parametric and combinator entries are hand-authored. The plan's §9.4
gives the mechanics.

The check covers curated entries as much as extracted ones. Dynamic
observation is the one source of evidence about throws that shares neither
the extractor's blind spots nor a reviewer's, so it catches a wrong
curated `throws` the same way it catches an over-prune.

- **False-negative rate — the soundness metric.** Real throws that the
  method can raise but the published set omits. This is the rate that
  matters, since a false negative is an unsound `throws` clause. Measure
  it in two layers so the blame is unambiguous:
  - against the *raw* FR10 set, before the FR11 filter, which should be
    zero when the control-flow graph is faithful. This isolates FR10
    extraction soundness.
  - against the *published* set, curated entries and all. A false negative
    there is a real throw a caller has to handle and nothing declares, and
    it is charged to the filter's over-prune or to the curated entry.
- **False-positive rate — the ergonomics metric.** Phantom throws the
  method cannot actually raise. These only cost callers a redundant
  handler, so they carry less weight than a false negative.

Every published false negative is triaged and fixed, in the filter or in
the curated entry it came from. Host and implementation-defined throws are
excluded from the ground-truth sample by an explicit, documented policy so
they do not register as false negatives against a set that cannot contain
them.

### FR15. Overloaded methods

ECMA-262 specifies **one algorithm per method**; it branches internally
on the runtime type and count of its arguments. TypeScript, and the
generated `.esc`, present that one runtime function as several typed
**overload** signatures. FR7 keys the single spec algorithm to the
method element; this requirement governs how the one algorithm's facts
apply across that element's overloads.

Because the overloads are typed views of a single runtime function, they
**cannot differ in what the function actually does**. So the
algorithm-level facts are shared by every overload and are extracted once:

- receiver mutability (a call mutates the receiver or it does not,
  regardless of which overload's types were used);
- the return-alias kind (FR4);
- the raw throw and reject provenance (FR10, FR13), before the
  type-dependent filter.

The per-parameter facts (FR12 disposition, FR2 mutation) are keyed by
**parameter position**, not by overload, and apply to an overload only
where it has that position. An overload with fewer parameters simply
carries no fact for the missing indices — this is the `Reflect.set`
three- versus four-argument case (the optional `receiver` at index 3
exists only on the longer overload). The join (FR7) aligns the spec
algorithm's parameter positions to each overload's positions and reports
a mismatch it cannot align rather than applying a fact to the wrong slot.

Two things legitimately **differ across overloads of the same method**,
and both are type-dependent rather than algorithm-dependent, so the
converter resolves them per overload at the FR7 join:

- **The filtered `throws` / `rejects` set.** FR11's coercion filter
  consults each overload's declared parameter types, so `ToNumber(p)` may
  be precluded on an overload that types `p` as `number` and kept on one
  that types it `unknown`. The same raw throw set can therefore filter to
  different committed sets on different overloads.
- **The `escape` spelling** (FR12). Whether a stored parameter is spelled
  a move or a lifetime-bounded borrow depends on the container's element
  type, which an overload fixes.

The facts file stays **one entry per spec method** carrying the shared
algorithm-level facts, the position-keyed parameter facts, and the throw
provenance; the converter applies that single entry to each overload
signature, completing the type-dependent parts against that overload's
types and arity.

## Non-functional requirements

- **Pinned spec edition.** The extractor pins a specific ECMA-262
  revision via ESMeta's `-extract:target` flag to an immutable **commit**
  SHA of the `tc39/ecma262` repository. The flag also accepts a branch or
  tag, but only a commit is reproducible, so the pin is a commit. The JVM
  toolchain (`tools/spec-extract/`) is likewise pinned to exact `java`
  and `sbt` versions with a committed `mise.lock`. Re-running on a
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
  every method whose receiver mutability is unclassified is listed, so a
  reviewer can see exactly which methods the spec proved, which review
  answered, and which fell through to heuristics. A curated answer that the
  analysis later reaches on its own is reported so the entry can be
  deleted, which keeps the curated layer from growing into a second source
  of truth.

## Coverage and limitations

- ECMA-262 covers `std:*` only. `web:*` and `node:*` are out of scope.
- **Host and implementation-defined throws are out of scope.** Errors
  that can occur at essentially any call — stack-overflow `RangeError`,
  out-of-memory, host-hook failures — are not enumerated in the spec and
  are not tracked, the same way an effect system does not track OOM.
  This exclusion is only for those pervasive host errors: a `RangeError`
  raised by an explicit spec domain check (`Number.prototype.toFixed`
  out of range, `Array(-1)`) is a tracked domain throw. The exclusion is
  what keeps the throw sets ergonomic and keeps FR14's false-negative rate
  measuring something a reviewer can act on.
- A handful of algorithms are prose-only or host-defined, so the analysis
  withholds the receiver mutability of a method among them. A curated
  entry answers it, and only what neither settles falls through to the
  name heuristic per FR5. The return alias is published regardless, since
  FR4 curates it rather than applying it.
- Generic and array-like receivers operate on `O ← ? ToObject(this
  value)`; the analysis treats `ToObject(this value)` as
  receiver-origin so that a write to `O` is a write to the receiver.
- Strings, numbers, booleans, bigints, and symbols are immutable
  primitives. Every method on their wrapper classes is provably
  non-mutating because the algorithm coerces to the primitive and builds
  a fresh result. This is where the facts source most clearly beats the
  name heuristics, which misclassify `String.prototype.replace` and miss
  `String.prototype.charAt`.
