# 02 — Caveats and mitigations

The four caveats raised while evaluating MLstruct, each with a concrete example
and a mitigation grounded in machinery the `solver/` package already has or the
`simple_sub/` plan already schedules. Caveats are ordered by how much they should
weigh on the adoption decision: #1 and #2 are the real costs, #4 is the one to
*verify* before committing, and #3 is downgraded to a minor task.

---

## Caveat 1 — NP-hard subtyping / normalization blowup

**The problem.** Normalizing to DNF distributes intersection over union, and the
conjunct count grows multiplicatively. The constraint

```
(A1 | B1) & (A2 | B2) & ... & (An | Bn)
```

normalizes to **2ⁿ conjuncts**. Worse, subtyping itself encodes SAT: deciding
`s <: t` reduces to emptiness of `s ∧ ¬t`, and a CNF formula maps directly onto an
intersection-of-unions whose emptiness is co-SAT. The POPL 2026 follow-up states
subtyping is NP-hard even without recursive types. A narrowing-heavy or
overload-heavy function can, worst case, make the checker solve SAT.

**Concrete example.** A function that combines several independently-guarded
values, each contributing a small union, produces an intersection of those unions
whose DNF is exponential in the number of guards. Or, adversarially, a type
annotation shaped like a CNF SAT instance forces an emptiness check that is co-SAT.

**Mitigations.**

- **The constraint cache already keys on arbitrary `(sub, super)` pairs.**
  `solver/constrain.go`'s `seen set.Set[constraintKey]` keys `constraintKey{sub,
  super}` for *any* pair, not just variables. MLscript's `ConstraintSolver`
  comments that, unlike Simple-sub, it must cache subtyping tests between
  non-variable types because normalization routes cycles through unions /
  intersections — Escalier's cache is already shaped that way. The same
  memoization that makes recursion terminate caps repeated emptiness sub-checks.
- **Lazy / on-demand DNF, not eager materialization.** MLscript does not hold
  types as fully-expanded DNF; it normalizes incrementally and merges losslessly
  where it can (`tryMergeUnion` / `tryMergeInter`). Port that discipline rather
  than materializing 2ⁿ conjuncts up front.
- **The nominal fast path collapses unrelated classes immediately.** MLstruct's
  `glb` over class tags returns "no greatest lower bound" for unrelated nominal
  classes, which makes a conjunct mixing them `never` and drops it before any
  structural work. Escalier's M5 declared-subtype graph (`simple_sub` M5) is
  exactly the `glb` oracle, so `C & D` for unrelated classes never enters the
  combinatorial path.
- **Reuse the M9 recursion budget as a normalization guard.** `simple_sub` M9
  already ships a cycle cache + depth budget + the level-2 regularity check
  (`CheckRegular`) for type-level operators and recursive types. The same
  budget bounds runaway normalization, degrading to a typed error rather than
  hanging — and any silent cap must be logged, per the project's "no silent
  truncation" convention.
- **Exact-by-default shrinks the input.** Escalier's structural formers are exact
  by default (`simple_sub` exactness thread), so inferred unions are narrower and
  closed more often than TypeScript's, giving the DNF fewer wide members to
  distribute over.

---

## Caveat 2 — Type display / readability of inferred types

**The problem.** Raw normal forms carry redundant variables and negations a user
should never see. Narrowing `x: string | number` against "not string" yields, in
normal form, `(string | number) ∩ ¬string` — semantically `number`, but the
literal form keeps `number ∩ ¬string` until a simplifier collapses it using the
fact that `string` and `number` are disjoint. Nest several guards and the
displayed type is a pile of negated members. MLscript needs a whole
`TypeSimplifier.scala` for exactly this, and it is the part most likely to leave
types ugly or be buggy.

**Concrete example.** Three chained guards over `string | number | boolean`
produce, unsimplified, `(number ∩ ¬string ∩ ¬boolean) | (boolean ∩ ¬string ∩
¬number)` where the user means `number | boolean`.

**Mitigations.**

- **Build on the simplification passes that already exist.**
  `solver/simplify.go` already does **co-occurrence merging** (collapsing
  quantified variables that always appear together), and `solver/lattice.go`
  already has `subsumeMembers` / `unionDrops` / `intersectionDrops` (dropping
  subsumed union/intersection members under a probe) plus `sortTypes` /
  `compareType` for canonical ordering. The new requirement is a
  **disjointness-aware negation simplifier**: `T ∩ ¬U → T` when `T` and `U` are
  provably disjoint, using the same disjointness facts M5 (class tags) and M6
  (literal/primitive disjointness) already compute. It slots beside the existing
  passes, not on top of `constrain`.
- **Binding-based narrowing confines and de-accumulates the clutter.** Escalier
  introduces a fresh binding on refinement
  ([`../simple_sub/02-design-notes.md`](../simple_sub/02-design-notes.md)
  §"Settled decisions" #8). This is a genuine structural advantage over
  flow-narrowing: each refinement is computed once at a definite site, simplified,
  and **frozen** as the new binding's type, so nested guards do not compound
  `∩ ¬A ∩ ¬B ∩ ...` on one long-lived variable. `x1 = x & ¬string` simplifies to
  `number | boolean`, and the next guard starts from that clean base rather than
  from an accumulating intersection. The clutter also stays scoped to the
  refinement binding — the original variable keeps its declared type everywhere
  else. The fresh binding does *not* remove the need for the disjointness
  simplifier; it makes the simplifier's input small and local, which is most of
  the battle.
- **Keep simplification a separable post-coalesce pass.** It already is —
  `coalesce` / `coalesceScheme` produce the type, and `simplify.go` runs over the
  result. Keeping the negation simplifier in that layer (never inside
  `constrain`) means it is a display concern that can be tuned independently and
  survives a solver swap.
- **Render through `TypeRefType` aliases where possible.** When a coalesced type
  matches a declared alias, print the alias name instead of the expanded normal
  form — the same name-recovery that helps caveat #3.

---

## Caveat 3 — Equi-recursive vs iso-recursive / named-alias (downgraded)

**The problem, and why it mostly dissolves for Escalier.** In an equi-recursive
system a type equals its unfolding, and inferred recursion is an anonymous μ-knot.
Two worries — and both are weak for Escalier specifically:

- *"Distinct named structural types collapse."* `type List = {head: number, tail:
  List | null}` and an identically-shaped `Stream` become the same type. But
  Escalier is **structurally typed**, like TypeScript — those aliases *are* the
  same type and *should* be interchangeable. This is desired behavior, not an
  artifact. Where nominal distinctness is wanted — classes — MLstruct keeps it via
  class tags (M5), so equi-recursion collapses exactly what should collapse and
  preserves exactly what should not.
- *"Inferred recursive types print as anonymous μ-knots."* A real but minor
  printer concern: coalescing a recursive value yields `μX. {head: number, tail: X
  | null}` rather than the alias name.

**Mitigations.**

- **Name-recovery in the printer.** `soltype` already carries alias references
  (`TypeRefType`, ingested in M7). When a coalesced μ-knot matches a declared
  alias, emit the `TypeRefType` instead of the raw recursion. This is the same
  pass that helps caveat #2, and it is a printer task, not a solver change.
- **The coinductive cache is already in place.** `solver/constrain.go`'s `seen`
  cache (keyed on arbitrary pairs) is the Amadio-Cardelli coinductive treatment
  equi-recursion needs; closing a recursive cycle is already how `constrain`
  terminates. Equi-recursion is the *natural* fit here, not an imposition.
- **The one genuinely new obligation is small: contractivity + regularity.**
  Reject ill-formed recursive type expressions such as `type T = T | T` or `type T
  = ¬T`. `simple_sub` M9 already ships a cycle cache, a depth budget, and
  `CheckRegular`; the contractivity check (every infinite branch must pass a
  product/object/arrow constructor) is a small extension of that existing
  well-formedness machinery, now also covering the new `NegationType`.

Net: downgrade from "caveat" to "a printer name-recovery task plus one
well-formedness check."

---

## Caveat 4 — Non-standard semantics / lossy unions (verify before committing)

**The problem.** MLstruct proves its own subtyping algebra, which is *not* the
naive "types as sets of values" model, and its normal-form representation
over-approximates some unions of structurally-disjoint shapes. Reading MLscript's
`tryMergeUnion`, a union like `{x: int} | {y: int}` or `(A -> B) | {x: C}` cannot
be packed losslessly into one normal-form conjunct, so it is kept as separate
members or over-approximated toward the top of that part of the lattice rather
than as a precise two-member union.

**Concrete example, and why it matters for Escalier.** TypeScript-style
discriminated unions of *untagged* records —

```
type Result = { ok: T } | { err: E }
```

— depend on the two members staying distinct so a check like `"ok" in r` can
recover the branch. If the union is over-approximated, that precision is lost.

**Mitigations.**

- **Verify the exact behavior against MLscript before committing.** The
  "over-approximates to ⊤" reading comes from the reference implementation's
  `tryMergeUnion`, *not* from the paper's formal rules. This is the single fact to
  confirm against MLscript (and the POPL 2026 follow-up's cleaner semantics)
  during the backtracking RFC, because it directly bounds how well TypeScript-style
  discriminated unions translate. Treat it as an open verification item, not a
  settled limitation.
- **Tagged unions survive, and Escalier already leans on tags.** A discriminant —
  a literal field or a class tag — keeps members provably disjoint, which is
  exactly the nominal/literal disjointness M5 and M6 provide. Escalier's
  exhaustive `match` story is already built on the exactness flag and on
  class/enum tags (`simple_sub` M4–M6), so the idiomatic tagged form
  (`{ tag: "ok", .. } | { tag: "err", .. }`) is unaffected.
- **Keep un-mergeable union members separate rather than collapsing.**
  `solver/lattice.go`'s `newUnion` already flattens, prunes, and `subsumeMembers`
  but otherwise **keeps distinct members as distinct** — it does not force a merge
  to a single node. Preserving that "don't merge what can't merge losslessly"
  discipline in the normal-forms layer is what keeps untagged record unions as
  precise as the representation allows.
- **Implement from the POPL 2026 semantics.** The follow-up's semantic soundness
  proof and cleaner decision procedure are the reference for *which* identities
  hold, so the non-standard corners are documented and intentional rather than
  surprises inherited from the implementation.
