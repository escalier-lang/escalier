# 02 — Caveats and mitigations

The four caveats raised while evaluating MLstruct, each with a concrete example
and a mitigation grounded in machinery the `solver/` package already has or the
`simple_sub/` plan already schedules. Caveats are ordered by how much they should
weigh on the adoption decision: #1 and #2 are the real costs; #4's non-standard
subtyping semantics are real, but its original "lossy unions" framing was
overstated and is corrected below; #3 is downgraded to a minor task.

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

## Caveat 4 — Non-standard subtyping semantics (the "lossy unions" framing was overstated)

**The real part.** MLstruct proves its *own* subtyping algebra — deliberately
**not** the naive "types as sets of values" model — and the POPL 2026 follow-up
notes it validates some identities a set-theoretic reading would not. This matters
wherever users **observe `<:` reflectively**, above all in M9 conditional-type
`extends` checks: see [04-type-level-operators.md](04-type-level-operators.md)
coupling point 2 and worked examples A/B, where a conditional's branch can flip
relative to a TS-faithful `<:`. That divergence is genuine and is the part of this
caveat worth carrying.

**What was overstated: unions are NOT lossy.** An earlier draft claimed a union of
structurally-disjoint members — `{x: int} | {y: int}`, `(A -> B) | {x: C}` —
"over-approximates toward ⊤." That is wrong for the normal representation. In
positive position such a union is a **precise multi-member DNF**
(`[Conjunct({x:int}), Conjunct({y:int})]`); in negative position it is a single
**disjunct** (a union of atoms). `tryMergeUnion` is an *optimization* that fuses
conjuncts when it can and **keeps them separate otherwise** — and "kept separate"
is exactly precise. A merge that *bails* does not widen anything; the "→ ⊤"
reading conflated an optimization's no-op with a soundness loss. All that is true
is that these members can't be **fused into one conjunct** (a conjunct is a meet)
— but they don't need to be; the multi-member form is exact.

**The two residual concerns that are real** (and far narrower than "lossy unions"):

1. **A representation limit — CONFIRMED, negative position only.** A disjunct's
   `RhsNf` holds at most one *structural record* atom, so a **supertype** `sub <:
   ({x: int} | {y: int})` of two distinct single-field records over-approximates to
   ⊤. Verified in MLscript's `NormalForms.scala`: the `RhsNf.| (Var, FieldType)`
   operation bails to `None` on a second differently-named field
   (`case _: RhsField | _: RhsBases => N`), and `None` means Top by the authors'
   own comment ("can't merge a record and a function or a tuple -> it's the same as
   Top"). Positive position is unaffected — a *subtype* union stays a precise
   multi-member DNF (`Conjunct.tryMergeUnion` keeps incompatible conjuncts
   separate) — and **tagged unions are unaffected**, since object tags get a *list*
   slot (`RhsBases.prims`) while records get a single one. **Mitigation for the
   port** (Escalier controls normalization, so it need not inherit this): either
   give Escalier's `RhsNf` a set-valued record slot (fully precise), or route a
   would-widen union-super through the retained `trialAndCommit` exists-rule
   instead of `constrainNF` (sound — it trials each member and rejects `number <:
   ({x:int}|{y:int})` correctly — and free since PR5 keeps the helper). Pinned by
   the negative-position rows in the PR2 corpus (#1059).
2. **Inexact unions need the exactness flag threaded — real work.** Escalier's
   `UnionType.Inexact` (`A | B | ...`, "at least these, with an open unknown
   tail") is **not native** to MLstruct's Boolean algebra, where `unknown` is just
   ⊤. DNF/CNF can carry it, but only by threading the `Inexact` flag through
   normalization, where it is a property of the *whole* union node and must survive
   member merge / dedup / reorder / distribution. This is PR7's remit (#1064) and
   belongs with the exactness thread, not with a "lossy union" worry.

**Mitigations.**

- **Keep un-mergeable members separate — this is precision-preserving, not a
  compromise.** `solver/lattice.go`'s `newUnion` already flattens, prunes, and
  `subsumeMembers` but keeps distinct members distinct. Carry that discipline into
  the normal-forms layer; a positive union that can't fuse simply stays
  multi-member, which is exact.
- **Tags keep the DNF *small*, not *precise*.** The idiomatic tagged form
  (`{ tag: "ok", .. } | { tag: "err", .. }`) does not buy precision — a
  multi-member DNF already has it — it keeps the conjunct count down, which is a
  **caveat #1** (blowup) concern. An untagged `{ ok: T } | { err: E }` stays a
  precise two-member DNF too, so `"ok" in r`-style branch recovery works on it
  without a discriminant. Prefer tags for blowup control, not for correctness.
- **The `RhsNf` capacity is verified (residual #1, done).** The source read of
  `NormalForms.scala` confirmed the single-record-slot → ⊤ widening, so the fix is
  now a port decision, not an investigation: give Escalier's `RhsNf` a set-valued
  record slot, or fall back to the `trialAndCommit` exists-rule for would-widen
  union-supers. What remains open under `06-open-items.md` Item 1 is the *separate*
  arrow-intersection half of that read; implement from the POPL 2026 semantics for
  *which* identities hold there. The exactness-flag threading (residual #2) is
  PR7's work.
