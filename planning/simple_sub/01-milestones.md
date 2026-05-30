# 01 — Milestones

Ordered milestones for the new checker. Each is independently testable and
leaves the old checker fully working. "Structural core first"; lifetimes are
introduced **with the first lifetime-carrying type** (records, M4). The MVP is
M1–M8 (structural core + nominal classes + unions/intersections + conformance
corpus + type-level operators); codegen/LSP and the cutover come after.

Spike provenance is cited where a milestone promotes proven spike work
(`internal/simplesub/`).

**Exactness runs through several milestones.** Escalier's structural formers
(objects, tuples, functions, unions) are **exact by default** — closed, no
extra members — with inexactness opted into via a trailing `...`
(`planning/exact-types/requirements.md`). Architecturally this is a flag on each
former that flips width subtyping on/off, the same "born-with-the-type" shape as
lifetimes — so the **representation** (an `exact` flag) and the **one-way
`exact <: inexact` subtyping rule** are introduced *with* each former (M3–M6),
not retrofitted. The richer machinery (`Exact<T>`/`Inexact<T>` type operators,
exactness propagation through `keyof`/mapped/conditional types, the value-level
`exact<T>(v)` lowering, and the `std:*`/`dom:*` annotation effort) is deferred to
M8 and later. See `02-design-notes.md` §"Exactness" for the representation and
rules; the **default itself** must be settled before M7's conformance corpus
bakes it in.

---

## M1 — Package skeleton + `soltype`

Stand up the new package and its type representation.

- New package as a **top-level sibling to `internal/checker/`** (e.g.
  `internal/solver/`; leaf name TBD). The spike lives at
  `internal/simplesub/`, but the production package sits beside the old
  checker so both can be built and differential-tested side-by-side, and so the
  old `internal/checker/` tree can be deleted wholesale at cutover.
- `soltype` types promoted from the spike: bound-list `TypeVar`
  (`lowerBounds`/`upperBounds` + `level`), `Primitive`, `Literal`, `Function`,
  `Tuple`, plus the constraint primitive `constrain(lhs <: rhs)` with the
  coinductive seen-cache, levels/extrusion, and polarity-driven coalescing.
- A printer for `soltype.Type` (its own, not `type_system.PrintType`) producing
  Escalier type-annotation syntax for test assertions.
- `Info` side table: `map[ast.Node]soltype.Type` + `TypeOf`/`setType`.

**Accept:** unit tests for `constrain` (prim/function variance incl. Escalier's
fewer-params-is-subtype rule) and coalescing; an identity term renders
`fn <T0>(x: T0) -> T0`. (Spike M0/M1.)

---

## M2 — Parser/resolver bridge

Replace the spike's hand-built IR with a real constraint-generating walk over
`*ast.Module`. This is the deferred spike "parser bridge."

- Drive from real source: `parser.Parse*` → `*ast.Module` → `dep_graph` /
  `resolver` → a constraint-generating AST visitor that produces `soltype` and
  populates `Info`.
- Produce a `Scope`/`Binding`/`Namespace` analogue owned by the new package (its
  own, not `type_system`'s).
- A fixture-style harness: given `.esc` source, infer and assert the rendered
  binding types (its own assertions, independent of the old checker).

**Accept:** top-level `val`/`fn` declarations from real source infer correct
rendered types end-to-end; multi-file module via the dep graph resolves.

**Gate:** if driving from the real AST/dep-graph requires reaching back into the
old checker's internals, the parallel-package boundary is wrong — stop and
reassess.

---

## M3 — Functions, application, let-polymorphism

- Lambda/`fn` decls, application, multi-arg functions.
- Level-based let-generalization (instantiate / freshenAbove).
- The simplification pass: single-polarity elimination + co-occurrence variable
  merging (so generalized signatures render compactly, and parameter-only
  variables coalesce to `unknown` rather than a vacuous `<T0>` — a blessed
  improvement).
- **Function exactness flag.** `Function` carries an `exact` flag; a bare
  `fn(...)` is exact, `fn(..., ...)` is inexact. Per the spec (§4.2.1), this
  governs **call-site argument counts only** — function-to-function subtyping
  requires arities to match in *both* directions regardless of exactness (so
  exact `</:` inexact *and* inexact `</:` exact). The spike's current
  fewer-params-is-subtype rule is the *inexact <: inexact* case; the exact
  arity-match case is added here.

**Accept:** the spike's Category-A cases against real source:
`TopLevelLetPolymorphism` ⇒ `fn <T0>(x: T0) -> T0`; `IdentityPolymorphism` ⇒
`fn () -> ["hello", 5]`; `InnerCapturesOuterParam` ⇒ `fn <T0>(y: T0) -> [T0, T0]`.
(Spike M1.) Plus: an exact `fn(x, y)` rejects a 3-argument call; an inexact
`fn(x, y, ...)` accepts it; exact and inexact function types are not
subtype-related in either direction.

---

## M4 — Core value types: records + usage-based inference + `mut` + **lifetimes**

The big one. These are inseparable: lifetimes ride on values, records are the
first lifetime-carrying type, and `mut` borrows are what first populate a
lifetime. Land them together.

- **Records/objects** with the optional lifetime field in the representation
  *from the start* (also tuples and type-refs/aliases as lifetime-carriers).
- **Exactness flag on records and tuples, from the start.** `Record`/`Tuple`
  carry an `exact` flag (default exact; `...` ⇒ inexact). Subtyping honors the
  one-way rule: exact `<:` inexact but not the reverse; exact `<:` exact requires
  the *same* member set (no width subtyping); inexact `<:` inexact is the
  current structural width subtyping. Object/tuple **literals infer as exact**.
  This is the spike's lifetime lesson applied again — a property carried with the
  former is cheap now, painful to retrofit; the spike today is uniformly inexact,
  so this is additive `constrain` cases plus a flag, not a rework.
- **Usage-based inference**: member access `obj.bar` ⇒ `constrain(obj <: {bar:
  β})`; field requirements accumulate as upper bounds and coalesce (negative
  position) to a record. This is what replaces `Open`/`Widenable`/
  `ArrayConstraint`. (Spike M2.) The *inferred* usage shape is inexact (a lower
  bound on what fields must exist), distinct from an exact literal.
- **`mut` invariance** via the read/write decomposition (covariant read +
  contravariant write). Plus inferring mutability from field writes (`obj.x = v`
  ⇒ `mut {x: widen(v)}`, with literal widening and mut-record merging). (Spike
  M3 + extension.)
- **Lifetimes as a second sort**: `LifetimeVar` with lower/upper bounds over the
  outlives lattice (`'static` = top), `constrainLt`, lifetime coalescing +
  elision (a parameter-only lifetime that connects nothing is dropped). Borrows
  originate at `mut` params; returning shares by value identity; multi-source
  returns union lifetimes; escape constrains `<: 'static`. (Spike M4.)

**Accept:** the canonical lifetime cases against real source — `IdentityRefReturn`
⇒ `fn <'a>(p: mut 'a {x: number}) -> mut 'a {x: number}`; `FreshObjectReturn`
(no lifetime); `ConditionalUnionReturn` ⇒ `mut ('a | 'b) {x: number}`;
`EscapingRefIntoStatic` ⇒ `mut 'static`; property-level and tuple-per-slot
lifetimes; read-after-write field collapse. (Spike M2/M3/M4 + lifetime extensions.)
Plus exactness: an exact `{x, y}` is assignable to inexact `{x, y, ...}` but not
the reverse; an extra property on an exact target is rejected; `mut` neither
tightens nor loosens exactness (it carries the inner type's flag through, per
spec §7.11 — orthogonal to the `mut` invariance encoding).

**Gate (HIGHEST RISK):** `mut` invariance via read/write decomposition is the one
thing that could still surprise at production scale. If it cannot be encoded
cleanly against the real AST, the whole migration is in question — this is the
gate to clear before investing further.

---

## M5 — Nominal types (classes)

Escalier's `class` declarations introduce **nominal** types: a value of class
`Point` is not assignable to a bare structural `{x: number}` (and vice versa)
even when the fields line up. SimpleSub is fundamentally structural, so nominal
types are layered on as atomic lattice elements with an explicit
**declared-subtype graph** feeding `constrain` — the design sketched in
[`03-references.md`](03-references.md). Lifetimes and `mut` ride on classes
exactly as they do on records (introduced in M4), so this milestone reuses the
M4 substrate without retrofitting.

- A `Class` SimpleType `{name, args, lt, final}` that is **atomic from
  `constrain`'s perspective**: subtyping never looks at its members structurally.
  Member *lookup* (`p.x`, `p.method()`) resolves through the declared body —
  that's a separate path from subtyping.
- **Class-instance exactness comes from `final`** (spec §2.6). A class instance
  type is **inexact by default** (subclasses may add members, so it behaves like
  an open object); a class declared `final` cannot be subclassed, so its instance
  type — and `keyof` of it — is **exact**. Enum variants are implicitly `final`,
  which is what lets exhaustive `match` over an enum need no default arm. This is
  the nominal-type instance of the same exactness flag: `final` ⇒ exact instance,
  non-`final` ⇒ inexact.
- **Nominal subtyping rule.** `Class<A, args_A> <: Class<B, args_B>` succeeds
  iff (a) `A == B` (per-position check on args, with variance per parameter —
  see below), or (b) `A extends B` (transitively) in the declared-subtype graph
  built from each class's `Extends`/`Implements`. Mixed
  `Class <: structural record` (and the reverse) rejects: a `Point` is not a
  `{x: number}`.
- **Per-type-parameter variance via polarity (Option 2).** Each class's type
  parameters get their variance inferred from how they appear in the class body,
  exactly as SimpleSub already does for inference variables. A parameter that
  appears only in output positions (field types, method returns) is covariant;
  only in input positions (method parameters, write-only fields), contravariant;
  in both, invariant. The subtyping rule then dispatches per parameter:
  covariant → `arg <: arg'`, contravariant → `arg' <: arg`, invariant → both.
  Declaration-site markers and use-site wildcards are explicitly **not** used.
- **`mut` and lifetimes ride on it free.** `Class` carries an `lt` field, so the
  M4 lifetime machinery applies unchanged (`mut 'a Point` works the same as
  `mut 'a {x: number}`). The `mut` invariance encoding (read/write
  decomposition) composes with per-parameter variance: a `mut` wrapping forces
  both directions on the whole `Class`, which cascades to forcing both
  directions per arg — invariance in `T` regardless of `T`'s declared variance.
- **Mutually recursive classes** infer via the same "fresh var per binding +
  constrain + generalize" pattern proven in the spike for recursive functions
  (`LetRec`/`LetRecGroup`) — no placeholder phase or `typeRefsToUpdate` patching.

**Accept:** the four variance lines that pin down Option 2 against `mut`:

```text
Box<number> <: Box<number | string>                ✓  (T covariant in Box's body)
mut Box<number> <: mut Box<number | string>        ✗  (Mut forces invariance over the top)
Consumer<number> <: Consumer<number | string>      ✗  (T contravariant in Consumer's body)
mut Consumer<number | string> <: mut Consumer<number>  ✗  (Mut over contravariant: still invariant)
```

Plus: a bare `{x: number}` is rejected against `Point` (and vice versa);
`class B extends A` yields `B <: A` via the declared graph and method dispatch
finds A's methods when not overridden; mutually recursive class declarations
infer cleanly. Plus exactness: a `final class Point` instance is exact (rejects
extra members, `keyof` is an exact union); a non-`final` class instance is
inexact.

**Scope note.** The *subtyping rule* is short (a few cases in `constrain` plus
a small declared-subtype graph). The bulk of the class machinery — constructor
handling, static vs. instance partitioning, method overload merging, `Self`
type substitution, the type-vs-value dual binding — is language semantics, not
unification, and is roughly proportional to the surface regardless of the
inference core. That work stays. What SimpleSub does avoid is the placeholder /
`typeRefsToUpdate` patching the production checker needs for cross-class
recursive references (cf. `infer_module.go:431-872` and the discussion in
`02-design-notes.md`).

---

## M6 — Unions / intersections

- Union/intersection as both inferred **output** (from bounds, polarity
  coalescing) and written **annotation input**, with the directional lattice
  rules in `constrain` (the "for all" rules before the "exists" rules; the
  "exists" rules defer to the variable case against a variable to avoid
  speculative pinning). (Spike M2 output + M6 annotations.)
- **Union exactness flag.** A bare `A | B` is an **exact** (closed) union — its
  inhabitants are exactly `A ∪ B`; `A | B | ...` is inexact (at least these, with
  an `unknown`-typed tail). Exact `<:` inexact, not the reverse. Exactness drives
  the closed-set consequences: a `match` over an exact union is exhaustive with
  no default arm; over an inexact union a default is required. (The exhaustiveness
  payoff is the main motivation, per spec §5.) `keyof` of an exact object and the
  element-union of an exact tuple are exact unions; their inexact counterparts
  are inexact — so this flag must be threaded by coalescing, not just stored.

**Accept:** `number | string` annotation accepts `number`/rejects `boolean`;
intersection annotation satisfied by a value at both member types; both
round-trip through the printer; inferred unions from multi-branch returns. Plus:
an exact union `"a" | "b"` is assignable to inexact `"a" | "b" | ...` but not the
reverse; an exact-union `match` covering all members needs no default, an
inexact-union `match` does.

---

## M7 — Conformance corpus + differential harness

- A comprehensive, **checker-agnostic** corpus encoding language semantics
  (`(source, expected type | expected error)`), the long-wanted fixtures
  upgrade. Expected outputs are authored to the semantics we *want*, not copied
  from the old checker.
- A **differential harness**: parse once, run both checkers on the same tree
  (into separate side tables), diff, and **triage** each divergence as
  match / intended-improvement / bug. (Automates the spike's M8, but as a triage
  tool, not a conformance gate.)
- Wire the new checker behind a flag at the **3** `compiler.NewChecker` sites.
- **Corpus encodes exact-by-default.** Because the corpus is the record of "the
  semantics we want," its expected types must reflect exactness: source literals
  exact, TS-imported types inexact (spec §8). This is the deadline for the
  default decision — once the corpus asserts it, flipping later means rewriting
  the corpus and any code written against the MVP.

**Accept:** corpus runs green on the new checker; differential harness runs over
the existing `fixtures/`, with every divergence triaged (no untriaged diffs).
Exact/inexact-sensitive cases (literal exactness, TS-import inexactness, an
exact-union exhaustive `match`) are represented.

**Gate:** pervasive *unintended* divergence ⇒ the new checker has correctness
gaps; burn down before proceeding.

---

## M8 — Type-level operators

The last MVP milestone. `keyof`, indexed access, conditional types: Baseline-D
(reduce when operands ground) + Design-A residual nodes reduced post-coalescing,
+ recursive-type handling (cycle cache + depth budget, and the level-2
regularity check). (Spike M5/M7/M9 + recursion + CheckRegular.)
- **Exactness propagation through operators** (spec §7): `keyof T` is exact iff
  `T`'s key set is exact; `T[K]`, conditional results, and mapped types derive
  exactness from their inputs. This is the first milestone where exactness must
  *propagate through reduction*, not just be checked — it builds on the flag laid
  down in M3–M6. The `Exact<T>`/`Inexact<T>` type-level utilities also land here
  (they are type operators).

**Accept:** the spike's type-operator cases against real source —
`keyof`/indexed access over ground and usage-inferred operands; conditional
types incl. `infer` and distribution; recursive aliases terminate (finite knot
or budget). Errors (e.g. arity, non-regular recursion) assert full messages.
Plus: `keyof` of an exact object is an exact union and of an inexact object an
inexact union; `Exact<{x, ...}>` ⇒ `{x}` and `Inexact<{x}>` ⇒ `{x, ...}`.

---

## Later (post-MVP)

- **M9 — Codegen.** Either a `soltype → type_system` bridge to reuse codegen
  unchanged, or port codegen (`dts.go` et al., ~4 files / ~30 refs) onto
  `soltype`. Decide when the checker is proven. **The value-level `exact<T>(v)`
  conversion** (spec §6.6) belongs here, not in the checker: it lowers to JS
  (object property-pick, `tuple.slice(0, n)`, a discriminating `match` for
  unions; functions excluded) and needs no reified types. The `@escalier-type`
  JSDoc round-tripping for exactness (spec §9) is also codegen work.
- **M10 — LSP.** Switch the LSP to the new checker's `Scope`/`Info`.
- **M11 — Flip & cleanup.** Make the new checker the default; retire the old
  checker + its tests; **delete** the AST `inferredType` field, the
  `type Type = type_system.Type` alias, and `tools/gen_ast`'s generation of the
  field — leaving the AST fully type-system-agnostic.
- **`std:*` / `dom:*` exactness annotation (independent track).** Auditing which
  library callbacks should be inexact and which lib classes are `final`
  (spec §11, `planning/exact-types/builtin-classes.md`) is a stdlib-curation
  effort, not a checker change — it consumes the exactness machinery rather than
  implementing it. It can proceed once the flag exists (after M3–M6) and is
  sequenced independently of the cutover.

## Dependency / risk ordering rationale

- M4 is front-loaded as the combined "core value types" milestone because
  records, `mut`, and lifetimes are an inseparable cluster once lifetimes ride on
  values — and it contains the highest-risk gate (`mut` invariance).
- M5 (nominal classes) sits right after M4 so it reuses M4's `mut`/lifetimes
  substrate directly. Its subtyping rule is small; its bulk (constructor / body
  inference / overloads) is language-proportional and unrelated to the inference
  core, so it doesn't change M4's risk profile.
- Codegen is deferred to the latest safe point because it is the single largest
  integration cost (its `type_system` dependency) and is not needed to prove the
  checker.
- The corpus (M7) is "improve, don't match," so the differential harness is a
  triage tool — this is what lets intended improvements through instead of
  forcing old-checker parity.
