# MLstruct adoption — implementation plan

This directory holds the plan for extending Escalier's algebraic-subtyping
checker (`internal/solver/`, built per [`../simple_sub/`](../simple_sub/)) into a
**Boolean algebra of structural types** with first-class **negation**, following
MLstruct (Parreaux & Chau, OOPSLA 2022). MLstruct is the direct descendant of
Simple-sub — same bound-list type variables, levels, and extrusion — so this is
an *extension* of the solver Escalier already ships, not a rewrite.

**This work is sequenced after the entire `simple_sub/` M-series (M1–M12) is
complete.** It is the concrete realization of the post-MVP "Backtracking +
disjunctive bounds" item in
[`../simple_sub/01-milestones.md`](../simple_sub/01-milestones.md) §"Later
(post-MVP)". Nothing here should start before the cutover (M12); these docs exist
so the option stays cheap to exercise when the decision point arrives.

## Documents

- **[00-overview.md](00-overview.md)** — what MLstruct adds over Simple-sub, the
  decision point and triggers for adopting it, why Escalier gains *less* from it
  than a TypeScript-like language would, and the option-preservation strategy.
- **[01-references.md](01-references.md)** — the papers, the reference
  implementation (MLscript), and the semantic-subtyping line researched as the
  alternative. Includes a sourcing caveat.
- **[02-caveats-and-mitigations.md](02-caveats-and-mitigations.md)** — the four
  caveats raised during research, each with a concrete example and a
  mitigation grounded in Escalier's existing machinery.
- **[03-graft-sketch.md](03-graft-sketch.md)** — how the normal-forms layer
  grafts onto the `solver/` package and onto the planned `simple_sub/` changes:
  the new `soltype` node, the new `solver/normal.go`, and the seams in
  `constrain.go` / `coalesce.go` / `lattice.go` / `probe.go`.
- **[04-type-level-operators.md](04-type-level-operators.md)** — how MLstruct
  interacts with the M9 TypeScript-style operators (`keyof`, indexed access,
  conditional types with `infer`, mapped types, template literals): the
  set-difference upgrade, the conditional-`extends` hazard with worked examples,
  and the distribution coupling.
- **[05-feature-interactions.md](05-feature-interactions.md)** — how MLstruct
  interacts with Escalier's own Simple-sub extensions: lifetimes (the second
  sort), exact / inexact types, and `throws` clauses. The set-difference upgrade
  reaches try/catch throws-narrowing; the one soundness watch-item is `¬Ref`
  lifetime polarity.

## TL;DR of the strategy

- **Extend, don't replace.** The Simple-sub core — `TypeVarType` with
  lower/upper bounds, levels, `extrude`, `probe` — carries over verbatim.
  MLstruct adds a normal-forms layer and one new node (`NegationType`).
- **Normalization replaces backtracking.** Simple-sub's "exists" rules
  (union-super, intersection-sub) trial-and-commit under a probe and never
  reconsider. MLstruct decomposes the same constraints deterministically via
  DNF/CNF, which is what preserves principal inference once negation is in the
  algebra.
- **Negation is the headline feature, and Escalier needs it least.** Escalier's
  narrowing is binding-based, not flow-based, so the single biggest use of
  MLstruct negation — the exact else-branch of a type guard — is already
  designed around. Adopt only when a requirement actually needs
  negation-in-the-lattice or inferred intersection-of-arrows.
- **Keep the option cheap.** Centralize the trial/commit sites (the `simple_sub`
  plan's M6 PR2.5 already does this) and keep type-display simplification a
  separable post-coalesce pass. Those two choices make a later swap an extension
  at one seam rather than a rewrite across many.
