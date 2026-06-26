# 06 — Open verification items & next steps

Two items must be discharged **before** committing to MLstruct adoption. Both are
flagged inline where they arise ([02-caveats-and-mitigations.md](02-caveats-and-mitigations.md)
§4, [04-type-level-operators.md](04-type-level-operators.md) coupling point 2,
[05-feature-interactions.md](05-feature-interactions.md) §Lifetimes); this doc
collects them with a concrete plan of attack so they aren't lost. The actual
investigation is deferred — this records *how* to action each when the time comes.

Most of each can be done **before any post-M12 code exists**: the output is an
artifact the future implementation PR tests against (a conformance oracle, a stated
invariant), not a code change waiting on the implementation.

---

## Item 1 — Verify arrow-intersection normalization against MLscript

**The question.** Does MLscript represent an intersection of arrows
(`(number => boolean) & (string => null)`) via a single merged `fun` slot
`(A|B) => (C&D)` plus a plain arrow rule (which would be **unsound** — see
[04-type-level-operators.md](04-type-level-operators.md) example B), or via the
un-merged Frisch–Castagna–Benzaken decomposition (sound)? M9 conditional-type
`extends` checks make this divergence user-visible, so it gates trusting M9
semantics.

**Plan of attack**, in order of leverage:

1. **Derive the sound spec first** (no tooling needed). Build an Escalier-owned
   **conformance table** of arrow-intersection subtyping cases — each row is
   `(intersection type, target arrow, sound verdict)` derived by hand from the FCB
   arrow decomposition. Seed it with the corners: same domain / different codomain,
   different domain / same codomain (the diverging example A), different / different
   (the reconverging example B), overlapping domains, nested arrows, and an arm with
   a free type variable. This is the test oracle the normal-forms PR will assert
   against.
2. **Source-read MLscript** (GitHub raw is reachable). Read two things and record
   the answer:
   - `NormalForms.scala` — does `LhsNf` hold one *merged* `fun` or a *set* of
     function atoms, and if merged, the exact `&`-formula.
   - `ConstraintSolver.scala` — the function-vs-function arm in `annoying`'s base
     case: does it re-decompose, or trust a merged single arrow? This alone settles
     the soundness question.
3. **Empirically confirm** (needs a Scala toolchain — local or CI). Translate the
   table's key rows into `.mls` snippets that force each subtyping judgment, run via
   `sbt` or the web REPL, record accept / reject.

**Deliverable.** The conformance table with three columns — sound verdict (step 1),
MLscript's observed verdict (steps 2–3), divergence/decision note. Where MLscript
diverges from sound, **Escalier follows the sound column** and documents the
choice. Lands as a conformance fixture / appendix, feeding the future PR's tests —
the "conformance corpus" pattern the `simple_sub` design notes already use.

---

## Item 2 — Record and enforce the `¬Ref` exclusion invariant

**The reframing.** This is not "get a polarity flip right" — it is "**keep the
borrow wrapper out of the Boolean algebra by construction**." That is principled,
not expedient: the outlives lattice is not Boolean, so a negated borrow wrapper
`¬(mut 'a T)` has no well-defined lifetime. Forbid it rather than handle it.

**Already half-true in the code.** `RefType` is handled in the structural `switch`
(the `rec` layer) of `constrain.go`, **not** in the M6 PR2 pre-switch lattice
block — refs already bypass union/intersection decomposition today. Two cases must
stay distinct:

- `¬(mut 'a T)` — negating the **wrapper** — is the **forbidden** case (no sound
  lifetime).
- `mut 'a ¬T` — a ref whose **inner** is a negation — is **fine**: the inner is
  pure type sort (`RefInner` already admits `UnionType`/`IntersectionType`) and
  normalizes normally while the wrapper stays opaque.

**Plan of attack.**

1. **Decide now: exclude, don't handle.** State in the plan that a `RefType` is a
   `rec`-layer atom that never enters `constrainNF` and is never wrapped by
   `NegationType`; its inner still normalizes as ordinary type-sort structure.
2. **Enforce by construction.** When `normal.go`'s negation builder (`DNF.mk`'s
   `NegType` case / the `NegationType` smart constructor) is handed a `RefType`
   operand, panic/error — fail loud, matching the `AsProperty` / "missed kind fails
   loud" convention. Preserve the existing early-return routing so refs never reach
   the normalization layer.
3. **Specify the invariant + tests now; land enforcement with the implementation.**
   Invariant: *no `NegationType` over a `RefType`; refs bypass `constrainNF`.* Test
   list: every borrow-narrowing path stays in `rec` or errors; `mut 'a ¬T` is
   accepted and normalizes its inner. The panic/test code is the only part that
   waits for the post-M12 normal-forms layer.

**Outcome.** The watch-item stops being a subtle soundness obligation and becomes a
one-line well-formedness invariant enforced at a single construction site — and
binding-based narrowing means nothing legitimate wants `¬Ref` anyway.

---

## Status

| Item | Can do now | Waits for implementation |
|---|---|---|
| 1 — arrow-intersection verification | Sound conformance table (step 1); MLscript source read (step 2) | Empirical `.mls` run (step 3, needs Scala); wiring the table into solver tests |
| 2 — `¬Ref` exclusion invariant | Decide exclude-vs-handle; write the invariant + test list into the plan | Construction-site panic + tests in `normal.go` |

Neither is started — investigation is deferred. This doc is the record so the
next planning session can pick them up.
