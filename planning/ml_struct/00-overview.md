# 00 — Overview: when and why to adopt MLstruct

## What MLstruct adds over Simple-sub

Simple-sub — the algorithm the `solver/` package implements — restricts where
union and intersection may appear. Type variables carry lower-bound and
upper-bound lists; a variable coalesces to the **join of its lowers** in positive
position (a union) and the **meet of its uppers** in negative position (an
intersection). Unions only ever surface in output position, intersections only in
input position, and there is **no negation**. This polarity discipline is exactly
what makes `constrain` a deterministic structural recursion that records bounds
and never has to guess.

MLstruct lifts the polarity restriction. Types form a **Boolean algebra** with
first-class `∪`, `∩`, and complement `¬` in *any* position, while keeping
Simple-sub-style principal type inference with no backtracking. The mechanism
that replaces the polarity discipline is **normalization to DNF/CNF**: when a
union, intersection, or negation appears at a position the structural recursion
can't decompose directly, the solver normalizes it into a canonical form and
decomposes *that*, instead of trial-and-committing over alternatives. A "healthy
sprinkle of nominality" — class tags carrying their parent set — makes negation
behave well and gives extensible variants without row variables.

Concretely, over the Simple-sub core MLstruct adds:

1. a complement type node (`NegType` in MLscript; `NegationType` here),
2. a normal-forms module (DNF/CNF over conjuncts/disjuncts; ~500 lines in the
   reference implementation),
3. a second constraint-solving layer (`goToWork` / `constrainDNF` / `annoying`)
   that handles the wrong-polarity and negation cases the structural recursion
   can't,
4. nominal class tags with parent-set bookkeeping threaded through both.

The Simple-sub bound/level/extrude machinery is unchanged. See
[03-graft-sketch.md](03-graft-sketch.md) for how each piece lands in `solver/`.

## The decision point

Adoption is **sequenced after M12** (the `simple_sub` cutover that retires the
old checker). It is the principled realization of the post-MVP item already named
in [`../simple_sub/01-milestones.md`](../simple_sub/01-milestones.md) §"Later
(post-MVP) — Backtracking + disjunctive bounds". That item records that four
trial-and-commit sites in `internal/solver` — `resolveOverload`, the
`IntersectionType`-sub arm, the M6 PR2 union-super exists rule, and the
pre-PR2.5 `constrainAssign` — all "pick the first candidate that holds and never
reconsider," and that the remaining failure modes "require a structurally
different solver." It lists two candidate directions: true backtracking, and
disjunctive bound representation.

**MLstruct is the third, and strongest, candidate for that RFC.** Normalization
is neither backtracking nor a disjunctive bound list — it is "decompose
deterministically so no choice is needed." Of the three it is the only one that
preserves Simple-sub's principal-inference guarantee, and it is the same author
lineage as the solver Escalier already runs. The backtracking RFC is where
MLstruct should be evaluated head-to-head, not before.

## Triggers — when to bring it forward

Default is to wait for the post-M12 RFC. Pull it earlier only when one of these
stops being hypothetical:

1. **The "exists"-rule first-success-commits failure modes bite users at
   scale.** The `simple_sub` plan enumerates these (over-constraining inner
   inference variables, order-dependence on canonical sort, no backtracking on a
   downstream contradiction, loss of cross-variable correlation). MLstruct's
   normalization removes the commitment entirely. If user reports show these
   biting, the cost is justified.

2. **A first-class negation requirement lands in scope.** A real `Not<T>` /
   type-subtraction operator that must work on *type variables*, or a decision to
   switch from binding-based to flow-based narrowing. Today neither is on the
   roadmap — see "Why Escalier gains less" below.

3. **Inferred intersection-of-arrows becomes a requirement.** Overloaded
   functions getting inferred overload types *without* the annotation
   restrictions M3 imposes. The `simple_sub` plan keeps overloads out of the
   lattice as side-channel metadata and requires annotations for mutually
   recursive overload sets. MLstruct infers arrow intersections natively.
   **Caveat: this is an inference-and-display win that does not reach codegen.**
   The set-theoretic intersection does not round-trip to a TypeScript overload
   table, and the runtime dispatcher still needs per-arm parameter annotations —
   so *implemented* overloads keep the annotation obligation even after adoption.
   See [05-feature-interactions.md](05-feature-interactions.md) §"Function
   overloading" for the full analysis; it is the one feature MLstruct
   *complicates* rather than upgrades.

Absent one of these, the Simple-sub polarity discipline is simpler, and adopting
MLstruct means paying its costs (see
[02-caveats-and-mitigations.md](02-caveats-and-mitigations.md)) for a capability
nothing consumes.

## Why Escalier gains less from MLstruct than TypeScript would

This is the load-bearing caveat on the whole effort, and it is specific to
Escalier's design.

MLstruct's flagship use of negation is the **exact else-branch of a type guard**.
In a flow-narrowing language, `if (typeof x === "string")` refines `x` to
`string` in the then-branch and to `(string | number) ∩ ¬string` in the
else-branch; the `¬string` is first-class negation that the solver handles via
normalization. This is the single biggest thing MLstruct's Boolean algebra buys a
TypeScript-like language.

Escalier has designed this away. Per
[`../simple_sub/02-design-notes.md`](../simple_sub/02-design-notes.md) §"Settled
decisions" #8 and restated across the milestones, **narrowing introduces a new
binding rather than re-typing an existing variable**. `if let r2: mut {x: number}
= r` binds a fresh view whose narrowed type is *named in the pattern*, so the
checker never computes `T ∩ ¬U` for an else-branch. M6's permissive mut-borrow
join is explicit that "Escalier has no runtime-type flow narrowing."

The consequence: the headline feature MLstruct exists to deliver, Escalier's
narrowing design does not need. What remains as genuine motivation is the
internal solver-quality argument (trigger 1) and the two capability gaps
(triggers 2 and 3) — real, but narrower than the TypeScript case. Weigh the costs
against *those*, not against a flow-narrowing payoff Escalier won't collect.

## Keeping the option cheap (do this during the M-series, not after)

Nothing here requires pre-building MLstruct. It requires not foreclosing it. The
`simple_sub` plan is already well-positioned: `soltype` has `UnionType` /
`IntersectionType` nodes, `constrain` has the directional lattice rules, the
`seen` cache already keys on arbitrary `(sub, super)` pairs (the coinductive
keying MLstruct's normalization needs), and the core is bounds + levels +
extrude, which MLstruct keeps verbatim. Two cheap habits preserve the seam:

- **Centralize the trial/commit sites.** M6 PR2.5's "shared trial helper"
  consolidates the four exists-rule sites into one. That single helper is the one
  place a later change swaps "first-success-commit" for "normalize-and-decompose."
  Lean into that consolidation rather than scattering bespoke probe loops.
- **Keep type-display simplification a separable post-coalesce pass.** The
  simplifier needed for readable inferred types (caveat #2) is the same layer
  MLstruct leans on harder. Building it over coalesced output — as
  `solver/simplify.go` already does for co-occurrence merging — rather than
  entangling it with `constrain` means it survives a solver swap intact.

Net: finish the MVP on Simple-sub as planned. Carry MLstruct as a named candidate
into the post-M12 backtracking RFC. Bring it forward only if a negation or
arrow-intersection requirement jumps into scope.
