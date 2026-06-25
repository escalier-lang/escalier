# 05 — Interaction with Escalier's own features

How adopting MLstruct interacts with the features Escalier adds *on top of*
Simple-sub — the ones with no TypeScript or MLstruct analogue: **lifetimes** (the
second sort), **exact / inexact types**, and **`throws` clauses** on functions.
These are the genuinely Escalier-specific interactions; the TypeScript-style
type-level operators are covered separately in
[04-type-level-operators.md](04-type-level-operators.md).

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
- **One subtle soundness watch-item: `¬Ref` and lifetime polarity.** Negation is
  contravariant, so `¬(mut 'b T) <: ¬(mut 'a T)` when `'a` outlives `'b` — the
  lifetime's outlives-direction should flip under negation. But the
  `RefType.Accept` visitor deliberately does *not* walk the lifetime (it is a
  separate sort handled by the lifetime passes), so the polarity-flip from
  `NegationType.Accept` ([03-graft-sketch.md](03-graft-sketch.md) §4) would not
  reach it. If `¬Ref` ever participates in constraint solving, the lifetime needs
  its polarity flipped explicitly. Escalier's binding-based narrowing makes `¬Ref`
  rare in practice — you narrow by rebinding, not by complementing a borrow — so
  this is a "rule it out or handle it specially" item, not a pervasive hazard. Add
  a guard either way.

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

## The cross-cutting theme

Negation upgrades **every set-difference in the language at once**: type-level
`Exclude` / `Omit` ([04-type-level-operators.md](04-type-level-operators.md)), and
now exception narrowing in `try` / `catch`. Wherever Escalier currently has a
conservative "distribute over a ground union" or "two-variable encoding"
workaround, MLstruct replaces it with an exact `& ¬`. Meanwhile the non-Boolean
sort (lifetimes) and the orthogonal former-flags (exactness) thread through
unchanged — with the single `¬Ref`-lifetime-polarity guard as the one soundness
item to verify.

| Feature | Interaction with MLstruct |
|---|---|
| Lifetimes (second sort) | Orthogonal — negation does not extend to the outlives lattice. `Ref`-atom normalization splits inner (type algebra) from lifetime (lifetime meet). Watch-item: `¬Ref` must flip lifetime polarity, which `Accept` does not walk. |
| Exact / inexact | Flag threads through; the merge must stay exactness-aware (exact `{x} & {y}` is `never`, not `{x, y}`) by reusing `newIntersection`. Exact unions + tag negation give `match` exhaustiveness. |
| `throws` | Rides parallel to `Ret` as a covariant field. **Upgrade** — try/catch narrowing becomes native `body_throws & ¬caught`, resolving M9's open question. |
