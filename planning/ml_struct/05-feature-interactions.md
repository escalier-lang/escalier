# 05 — Interaction with Escalier's own features

How adopting MLstruct interacts with the features Escalier adds *on top of*
Simple-sub. Three have no TypeScript or MLstruct analogue — **lifetimes**, the
second sort; **exact / inexact types**; and **`throws` clauses** on functions. A
fourth, **function overloading**, has a TS analogue in name only: Escalier gives
each arm its own body and synthesizes the runtime dispatch, where TS overloads are
type-only signatures over one hand-written body. These are the genuinely
Escalier-specific interactions; the TypeScript-style type-level operators are
covered separately in [04-type-level-operators.md](04-type-level-operators.md).

## The unifying frame

Escalier extends Simple-sub two ways: **extra sorts** solved by the same bound
machinery (lifetimes), and **extra structure carried on formers** (the exactness
flag, the `throws` field). MLstruct only Boolean-izes the **type** lattice — it
adds negation and full union / intersection / normalization *there*. Three
consequences fall out:

1. Extra *sorts* stay non-Boolean. Negation does not extend to them, and shouldn't.
2. Extra *fields on formers* thread through normalization the way the formers do,
   reusing the existing combine logic.
3. Negation upgrades the **set-difference** operation in whichever domain has one —
   and two of these features have one.
4. Function overloading sits outside this lattice frame. It interacts through the
   inference→codegen pipeline rather than the type algebra, and is the one feature
   where MLstruct *complicates* rather than upgrades or threads through unchanged.

---

## Lifetimes

Lifetimes are a second sort with their own "outlives" lattice (`'static` = ⊥),
solved by `constrainLt` over `LifetimeVar` bounds, and carried on the
`RefType{Mut, Lt, Inner}` wrapper rather than inside the inner type
([`../simple_sub/03-references.md`](../simple_sub/03-references.md) §"Lattice 2").

- **Negation does not extend to the lifetime lattice, and that is correct.** There
  is no meaningful `¬'a` — the outlives lattice is not a Boolean algebra.
  MLstruct's complement stays in the type sort. The two sorts already have
  *different* lattices, so Boolean-izing one and leaving the other a plain
  join / meet lattice is clean, not a special case.
- **Normal-form merging of `Ref` atoms must delegate the lifetime to the lifetime
  sort.** When two refs land in one conjunct — `(mut 'a T) & (mut 'b U)` — the
  *inner* types combine in the (now Boolean) type algebra while the *lifetimes*
  combine via the lifetime meet (`constrainLt`). The `LhsNf` ref-atom merge must
  split the work by sort, exactly as the rest of the solver already splits
  inner-vs-lifetime. A union of refs differing only in lifetime
  (`(mut 'a T) | (mut 'b T)`) is the M6 permissive-borrow-join case; it factors to
  `mut ('a | 'b) T` via the existing M4 D4 single-carrier logic, and normalization
  must reuse that rather than treating the two refs as un-mergeable.
- **The lifetime polarity-flip under `¬Ref` already works.** Negation is
  contravariant, so `¬(mut 'b T) <: ¬(mut 'a T)` when `'a` outlives `'b`, and the
  lifetime's outlives-direction must flip under negation. It does.
  `NegationType.Accept` ([03-graft-sketch.md](03-graft-sketch.md) §4) flips the
  polarity *before* descending, and every pass that reads a lifetime reads it off
  the `RefType` node in its own `EnterType`, where the flip has already applied.
  `e.c.extrudeLt(r.Lt, pol, …)` in `internal/solver/constrain.go` is the clearest
  case. `extrudeLt` wires the origin lifetime to its fresh proxy through the bound
  direction the polarity picks — an upper bound at `Positive`, a lower bound at
  `Negative`. Extruding `&'a T` from level 5 to level 0 at root `Positive` leaves
  `'a` with `lower=0 upper=1`; extruding `¬(&'a T)` at the same root polarity leaves
  it with `lower=1 upper=0`. That is the outlives direction flipping.
  `RefType.Accept` not walking the lifetime turns out not to matter, because no pass
  relies on `Accept` to reach it.
  `TestComplementFlipsExtrudedLifetimeDirection` pins both rows.
- **`¬Ref` is excluded for two other reasons.** `soltype.AssertNegatable` panics on
  a borrow operand, and the guard stays for now.
  1. **The outlives lattice is not a Boolean algebra**, so `¬'a` names nothing. The
     decision procedure never asks for one: in `constrainImplied` a negated atom
     always crosses the `<:` and lands as a positive atom on the other side, where
     it is met or joined, and `meetRefs` / `joinRefs` already provide both.
  2. **A residual `¬Ref` would not reduce.** The solver knows disjointness only
     inside the value families, which cover primitives, literals, `null` and
     `undefined`. `normal.go`'s `valueFamily` comment records that objects, tuples,
     functions and class tags were left out. A borrow is in no family, so `Exclude`
     over one yields back the same `T & ¬(&'a U)`. Lifting the exclusion stops the
     panic without making the result useful. Widening `valueFamilyOf` to cover
     borrows is the other half of the work.

  Display-time lifetime classification was a third blocker. It no longer is.
  `coalesceLifetimes` reads a borrow's position as dataflow rather than as variance.
  Negative means the borrow originates at a parameter, so its lifetime is nameable.
  Positive means the borrow reaches an output, so its lifetime is not elided. A
  complement does not move a borrow between a parameter and an output, but it does flip
  the polarity its operand is visited at, so reading position straight off the polarity
  strips the name from every complemented borrow. That changes the type rather than
  merely dropping a name, since `¬(&'a T)` rendered as `¬(&T)` is the complement of
  *any* borrow of `T`.

  `ltOccVisitor` therefore produces two facts rather than one:

  1. **Position**, recovered by undoing one polarity flip per enclosing complement.
     Only the parity matters, since two complements cancel. Position decides which
     connected component counts as output-reaching, so a mis-read position keeps
     unrelated lifetimes named and invents outlives bounds.
  2. **Complement-enclosed**, a veto that forbids eliding a lifetime whatever its
     position. Position alone is not enough, because a complemented borrow reaching no
     output is genuinely connect-nothing and D4 elides those. The veto is what puts the
     name on a complemented borrow.

  The two are kept separate so that correcting one cannot silently re-break the other.
  They are also pinned by different tests: the veto by
  `TestComplementedBorrowKeepsLifetimeName`, the position correction by
  `TestComplementedBorrowAssertsNoOutlivesRelation` and
  `TestComplementedBorrowGroupsLikeAnOrdinaryParam`.

  The same occurrence map feeds `checkDeclaredLifetimeBounds` through
  `ltOutlivesRelation`, so the mis-reading reached the declared-bound check and not only
  the printer.

---

## Exact / inexact types

Exactness is a flag on each former (`ObjectType.Inexact`, `TupleType.Inexact`,
`FuncType.Inexact`, `UnionType.Inexact`) that toggles width subtyping, with the
one-way `exact <: inexact` rule
([`../simple_sub/01-milestones.md`](../simple_sub/01-milestones.md) M3–M6 exactness
thread).

- **The flag threads through normalization, but the merge must stay
  exactness-aware.** The graft already requires `tryMerge` to carry `Inexact`
  through unchanged ([03-graft-sketch.md](03-graft-sketch.md) §7). The substantive
  point is *what the merge computes*. Intersecting two **inexact** objects unions
  their fields (`{x, ...} & {y, ...} = {x, y, ...}`). But two **exact** objects
  with differing required fields have no common inhabitant — exact `{x}` is closed,
  so nothing is simultaneously exactly-`{x}` and exactly-`{y}`, and the meet is
  `never`. A TypeScript-style blind field-union would be **unsound** for exact
  objects. So `normal.go`'s object-meet must delegate to the existing
  exactness-aware `newIntersection` (lattice.go), not reimplement field-union. This
  is an existing Escalier semantic that normalization must preserve, not something
  MLstruct introduces.
- **Negation must track exactness on negated atoms.** `¬{x}` (exact) and
  `¬{x, ...}` (inexact) are different complements, so the `RhsNf` structural atoms
  carry the flag like the positive ones. Mechanical, but it has to be threaded.
- **Positive interaction: exact unions + tag negation give exhaustiveness for
  free.** An exact union `A | B` is closed (M6), and MLstruct's class-tag partition
  (`C` vs `¬C`) lets a `match` subtract matched cases. Composing them, an
  exhaustive `match` over an exact tagged union needs no default arm, because the
  complement of the covered cases within a closed union is empty. This is
  MLstruct's extensible-variants story meeting Escalier's exactness payoff — they
  reinforce each other rather than collide.

---

## `throws` clauses

`throws T` is a covariant field on `FuncType`, defaulting to `never`, with a
per-body throws inference variable accumulating thrown types as lower bounds (M9).

- **It rides parallel to the return type through normalization.** When arrows merge
  or decompose in the normal form, `Throws` combines exactly as `Ret` does — it is
  another covariant output position. The arrow-intersection merge intersects throws
  like it intersects codomains, and the Lemma-6.8-style decomposition
  ([04-type-level-operators.md](04-type-level-operators.md) coupling point 2)
  checks throws per overload like the codomain. The M9 plan already says throws
  needs "no new lattice machinery"; MLstruct does not change that — it just carries
  one more covariant field through the same merge.
- **A throws type is a coalesced union, which the lattice already handles.**
  `throws "a" | "b"` is a positive-position join of lower bounds, exactly like any
  inferred union. No special handling.
- **The real payoff: try/catch narrowing becomes a native set difference.** M9
  flags as an *open question* how `try` / `catch` narrows the body's throws —
  "subtract the caught types from `body_throws`" — and offers a conservative
  two-variable encoding `body_throws <: surrounding_throws ∪ caught_throws` because
  Simple-sub cannot express subtraction. MLstruct's negation expresses it exactly:

  ```
  surrounding_throws = body_throws & ¬caught
  ```

  This is the *same* set-difference upgrade as `Exclude` / `Omit`
  ([04-type-level-operators.md](04-type-level-operators.md) coupling point 1),
  applied to the exception sort. So adopting MLstruct directly resolves M9's open
  throws-narrowing question — with the same fidelity caveat that the subtraction is
  exact only when the caught types are concrete enough, and the same design choice
  of native `& ¬` versus a distributive encoding.

---

## Function overloading

Escalier supports overloaded `fn` declarations, and its form has no TypeScript
analogue: each arm is a full `FuncDecl` with **its own body**, and the compiler
synthesizes the runtime dispatcher rather than relying on a hand-written one. The
overload *type* is the MLstruct interaction — adoption trigger 3
([00-overview.md](00-overview.md)) makes inferred intersection-of-arrows
first-class — but it runs through the inference→codegen pipeline, not the lattice.
The through-line: **the trigger-3 win is an inference-and-display win, and it does
not reach codegen.**

- **What trigger 3 buys.** Under Simple-sub, overloads are side-channel metadata
  outside the lattice, so an overloaded call must *pick a single arm* to know what
  to constrain ([`../simple_sub/01-milestones.md`](../simple_sub/01-milestones.md)
  §"Function overloading"). In a mutually recursive group that branch choice depends
  on the inferred types of the other members, which depend on the branches picked at
  *their* recursive calls — a cycle that need not converge under subtyping, which
  Simple-sub breaks by **requiring annotations**. MLstruct infers arrow
  intersections natively, so there is no branch to pick: the whole intersection is
  one lattice type in the fixed point, and the recursive-group cycle dissolves. An
  un-annotated overloaded function in a recursive group becomes inferable.
- **The inferred type does not round-trip to a TS overload table.** "Infers the
  overload type" means the **set-theoretic** intersection, not a dispatch table. The
  arms render as `(A → B) & (C → D)` — shared syntax — but the projection is lossy:
  faithful only on the sublattice that factors as `⋂ᵢ (Aᵢ → Bᵢ)`; the table reading
  is the *weaker* one, dropping the union-domain callability MLstruct grants (worked
  example A in [04-type-level-operators.md](04-type-level-operators.md) coupling
  point 2); and if it feeds `.d.ts` emit, `extends` / `Parameters` / `infer` over
  the emitted type diverge from the inferred type. The display simplifier (caveat
  #2) is equivalence-preserving, so the loss is in surface-expressibility, not
  simplification — a residual stuck variable, a residual `¬` with no `Not<T>` surface
  syntax, or an interpretation-divergent arrow-intersection each breaks the
  round-trip.
- **Codegen needs the commitment inference discarded.** `buildOverloadedFunc`
  (`internal/codegen/builder.go`) sorts arms by specificity — parameter count, then
  required-property count — and emits an if-else chain whose per-arm guards come from
  `buildTypeGuard` over **each arm's written parameter annotations**: `typeof` for
  primitives, `===` for literals, `"k" in o` plus recursive checks for object shapes,
  `Array.isArray`, `instanceof` for nominal classes. Undiscriminable types fall
  through to `true`; a no-match throws `TypeError`. Deterministic dispatch is a
  *commit-to-one-runtime-discriminable-arm-in-fixed-order* constraint — the opposite
  of MLstruct's normalize-and-decompose, which **removes** the per-call arm
  commitment (trigger 1). Four concrete tensions:
  1. The dispatcher consumes per-arm annotations, not the inferred type — and
     trigger 3's win is inferring the type *without* them, removing the artifact
     codegen dispatches on.
  2. Normalization fuses arms, so the lattice type is body-agnostic; the syntactic
     arm→body map must be kept as side metadata regardless.
  3. Static resolution must select the same arm the dispatcher routes to. MLstruct
     resolves via the non-standard Boolean-algebra `<:` (caveat #4) while the dispatcher
     runs a concrete `typeof` test; worked example A is where they disagree.
  4. Negated / union arm domains are silently un-dispatchable — `buildTypeGuard`
     emits `true`, so the first arm in sort order swallows the call rather than the
     checker rejecting it.
- **The carve-out and the consequence.** This bites only *implemented* overloads —
  `buildOverloadedFunc` emits no dispatcher for declare-only arms, so external /
  `.d.ts`-shaped overloads take the inference freedom harmlessly. The design
  consequence: any overloaded `fn` with bodies still needs per-arm parameter
  annotations, or inferred arm domains restricted to a mutually-distinguishable,
  runtime-checkable sublanguage, to codegen deterministic dispatch. Scope the
  trigger-3 relaxation to inference and display; keep the annotation obligation
  wherever a dispatcher is generated.

---

## Diagnostics and blame

Normalization mints types no AST node minted. A structural merge fuses two atoms
into one — two records field by field, two arrows into one, two tags of one class —
and the fused node is a pointer nobody wrote. `Prov`, the side table that maps a
type back to its origin, is keyed by pointer identity, so a fused node resolves to
nothing and a diagnostic naming it falls back to the constraint site's span rather
than the narrower node it would otherwise blame.

The rule for the minting side is settled here so a later PR in the epic follows one
convention. Every fusion records a `FromNormalization` interior edge naming the two
atoms it merged. `Origin` is an interface, so the edge is an addition rather than a
change to the table's value type, alongside the `FromAST` leaf and the
`FromInstantiation` edge.

The two atom-merge dispatchers `meetAtoms` and `joinAtoms` in
`internal/solver/normal.go` mint the edge for every fusion they perform — a
structural merge of two records, tuples, arrows, borrows, or class tags; a
value-atom absorption; or a `never` from two disjoint atoms — naming the two atoms
combined. The field-level `meetTypes` and `joinTypes` each record the member node
they build as well, since a rebuilt arrow domain such as `number | string` or a
field that collapses to `never` does not itself pass back through a dispatcher. An
identity or absorption merge returns a source unchanged, `A & A` is `A` and
`5 & number` is `5`, and keeps that source's own leaf origin rather than gaining a
self-referential edge. `meetClassArgs` also runs from `instanceBelow`, which fuses
two tags only to test a subtype relation and discards the result; that path bypasses
the dispatchers, so a throwaway meet does not mint a container edge.

The rendering side is deferred to M11.5, the multi-hop provenance renderer in
`planning/simple_sub/01-milestones.md`. `NodeFor` resolves only the `FromAST` leaf
today, so a `FromNormalization` edge is minted-but-unread and no diagnostic moves
until that renderer chases the edge to its AST leaves. A fused atom has more than
one source, which the single-source `FromAST` and `FromInstantiation` edges never
do, so the renderer needs a rule the others do not. Two candidates: blame the
nearest common ancestor of the leaves, or attach every leaf as a related span. The
choice belongs with the renderer and is recorded here when it is made.

---

## The cross-cutting theme

Negation upgrades **every set-difference in the language at once**: type-level
`Exclude` / `Omit` ([04-type-level-operators.md](04-type-level-operators.md)), and
now exception narrowing in `try` / `catch`. Wherever Escalier currently has a
conservative "distribute over a ground union" or "two-variable encoding"
workaround, MLstruct replaces it with an exact `& ¬`. Meanwhile the non-Boolean
sort (lifetimes) and the orthogonal former-flags (exactness) thread through
unchanged. `¬Ref` stays excluded, because the outlives lattice has no complement
and a residual `¬Ref` would not reduce, not because the polarity flip fails to
reach the lifetime. Function overloading is the lone counter-current: there the
inference win does not reach codegen, so MLstruct complicates rather than upgrades.

| Feature | Interaction with MLstruct |
|---|---|
| Lifetimes (second sort) | Orthogonal — negation does not extend to the outlives lattice. `Ref`-atom normalization splits inner (type algebra) from lifetime (lifetime meet). `¬Ref` stays excluded: `¬'a` names nothing and a residual `¬Ref` would not reduce until `valueFamilyOf` widens. The polarity flip does reach the lifetime. |
| Exact / inexact | Flag threads through; the merge must stay exactness-aware (exact `{x} & {y}` is `never`, not `{x, y}`) by reusing `newIntersection`. Exact unions + tag negation give `match` exhaustiveness. |
| `throws` | Rides parallel to `Ret` as a covariant field. **Upgrade** — try/catch narrowing becomes native `body_throws & ¬caught`, resolving M9's open question. |
| Function overloading | **Complication** — trigger 3 infers recursive-group overloads without annotations, but the set-theoretic type does not round-trip to a TS overload table and the inference win does not reach codegen. Implemented overloads still need per-arm annotations for deterministic dispatch. |
| Diagnostics / blame | **Precision cost** — a structural merge mints a fused node no AST node wrote, so `Prov` resolves it to nothing and blame widens to the constraint site. Each merge records a `FromNormalization` edge naming the atoms it fused; the renderer that chases it to AST leaves is deferred to M11.5. |
