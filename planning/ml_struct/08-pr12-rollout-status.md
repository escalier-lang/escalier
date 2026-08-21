# 08 — PR12 differential and rollout status

PR12 (issue [#1069](https://github.com/escalier-lang/escalier/issues/1069)) was
planned as a flag-gated rollout. Add a solver-mode flag that selects Simple-sub
versus MLstruct normalization in `constrain`, run a differential over the
conformance corpus and the `fixtures/` tree, triage the divergences, then flip
the default to MLstruct.

The implementation reached that end state by a different route, so this document
records where the code actually landed, what stands in for the differential, and
what remains deferred. It supersedes the flag-and-flip mechanics sketched in
[07-implementation-plan.md](07-implementation-plan.md) §PR12.

## The flag premise is already satisfied

There is no solver-mode flag, and there is nothing left to flip inside
`internal/solver`. PR5 ([#1111](https://github.com/escalier-lang/escalier/pull/1111))
rerouted the union-super and intersection-sub exists-rules through `constrainNF`,
the normal-form layer, rather than adding it alongside a retained Simple-sub path.
PR6 through PR11 then built every subsequent feature on that layer. So MLstruct
normalization is the sole path `constrain` takes for a union, an intersection, or
a negation, and it has been the default since PR5 merged.

The one Simple-sub artifact that survives is `trialAndCommit` in
[probe.go](../../internal/solver/probe.go). It is not a selectable mode. It is the
exists-rule helper the structural switch still calls, and caveat #4 keeps it as a
sound escape hatch for a would-widen union-super. A flag toggling two coexisting
normalization strategies would have to reconstruct the pre-PR5 Simple-sub
exists-rule, which no feature needs and which PR6–PR11 would each have to
special-case. The rollout therefore ships the MLstruct path directly.

## What stands in for the differential

The differential's job was to prove the MLstruct path agrees with the Simple-sub
baseline except where a change is a blessed improvement. Two things discharge that
without a runtime toggle.

1. **The conformance oracle.** `nfArrowCorpus` and `nfRecordCorpus` in
   [constrain_nf_test.go](../../internal/solver/constrain_nf_test.go) state, for
   each subtyping case, the verdict that is sound under a types-as-values reading.
   Every verdict is derived by hand from that reading, not read off any
   implementation. The solver is measured against truth rather than against the
   old path, which is a stronger check than a self-differential. These tables are
   green under the MLstruct path.

2. **The new-checker conformance corpus.** The `internal/solver` table tests are
   the corpus PR12 names, and they encode the inferred type or error message each
   case must produce. `go test ./internal/solver/` is green. A row whose expected
   output changed relative to the Simple-sub baseline was updated in the PR that
   introduced the change, so the corpus already carries the blessed improvements
   in its expectations.

The old `internal/checker` was not run as a differential baseline. Its behavior
predates negation, so it is not a meaningful baseline for the negation-based
features, and the issue's own gotcha rules it out. Comparing against it would
report every negation improvement as a divergence to re-triage by hand.

## Blessed intended improvements

Each row is a user-visible behavior change the MLstruct path makes over the
Simple-sub baseline, with the merged PR that introduced it. These are the
"intended-improvement" bucket the differential would have produced.

| PR | Improvement | Merged as |
| --- | --- | --- |
| PR6 | A narrowed type prints without redundant complements. `(string \| number) ∩ ¬string` renders as `number`; a three-guard chain `(string \| number \| boolean) ∩ ¬string ∩ ¬number` renders as `boolean`. Display-only, disjointness-driven. | [#1116](https://github.com/escalier-lang/escalier/pull/1116) |
| PR7 | Object and tuple meets respect exact-by-default. Two exact objects with differing required fields meet to `never` rather than a blind field-union, and `{x, ...} ∩ {y, ...}` fuses to `{x, y, ...}`. The Simple-sub field-union was unsound for exact objects. | [#1117](https://github.com/escalier-lang/escalier/pull/1117) |
| PR8 | A union of refs differing only in lifetime factors to one ref. `(mut 'a T) \| (mut 'b T)` becomes `mut ('a \| 'b) T`. The `¬Ref` polarity obligation is discharged: the flip reaches a borrow's lifetime, as [#1127](https://github.com/escalier-lang/escalier/pull/1127) verifies. | [#1120](https://github.com/escalier-lang/escalier/pull/1120) |
| PR9 | A `catch` of a concrete type subtracts it from the body's inferred throws via native set difference. `try { throwsAorB() } catch (e: A) {}` leaves surrounding throws `B`, where the Simple-sub encoding was a conservative union. The subtraction stays conservative when the caught type is abstract. | [#1118](https://github.com/escalier-lang/escalier/pull/1118) |
| PR10 | `Exclude`, `Omit`, and `NonNullable` become total on a type variable, producing `∩ ¬` forms that reduce once the operand grounds, rather than only reducing over a ground union. A conditional `extends` decides its branch via the new `<:`. | [#1119](https://github.com/escalier-lang/escalier/pull/1119) |
| PR11 | An overloaded `fn` in a mutually recursive group infers without return annotations on any arm, because the arrow-intersection is one fixed-point type the decomposition need not branch on. Parameter annotations stay required, since un-annotated domains leave the arms indistinguishable. | [#1146](https://github.com/escalier-lang/escalier/pull/1146) |

The two examples the issue cites map onto this table. An un-annotated recursive
overload now inferring is PR11. A set-difference now total on a variable is PR9
for the exception sort and PR10 for the utility-type operators.

## Caveat #4 status

The flip was gated on caveat #4's MLscript verification having no open
divergences. It has none. The arrow-intersection and record-union corpus is the
verification, and every divergence between Escalier and MLscript in it is one
where Escalier gives the sound answer and MLscript over-approximates to `unknown`.
Escalier controls its own normalization and does not inherit the over-approximation,
so these are blessed by design rather than open. The corpus pins them with the
sound verdict, and a port that inherited the widening would fail those rows.

## What is deferred

- **The production flip.** Wiring `internal/solver` in as the checker the compiler
  and codegen call is the Simple-sub M12 cutover, not this PR. Nothing outside
  `internal/solver` imports the new checker today, and `internal/checker` stays the
  production path until M12. The MLstruct default described here is a default
  within the new solver, not a switch of the shipping checker.
- **Everything in PR11 that touches codegen.** The overload dispatcher
  reconciliation is gated on M12 for the same reason, and is recorded in
  [#1068](https://github.com/escalier-lang/escalier/issues/1068) under "Deferred:
  everything touching codegen."

## Coverage bounds

Logged per the repo's no-silent-truncation convention.

- The MLscript column of the conformance corpus is partial by design. Tagged-union
  rows and every row carrying a `throws` clause stay `nfUnobserved`, because the
  reference implementation has no `throws` and its tag slot behaves differently
  from its single record slot. The sound column is complete and is what the solver
  is measured against; the MLscript column is evidence for caveat #4, not the gate.
- The `fixtures/` tree is exercised through `internal/checker` by
  `cmd/escalier/fixture_test.go`, not routed through `internal/solver`. Routing it
  through the new solver is part of the M12 cutover, when the solver becomes the
  checker those fixtures compile against. Until then the new-checker corpus is the
  conformance surface, and it is green.
