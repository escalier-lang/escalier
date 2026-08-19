# 06 — Verified findings (pre-adoption source verification)

The pre-adoption verification items from earlier drafts have been **discharged** —
the MLscript source reads and the Escalier codegen read are done. This doc is now
the **record of what was verified**. Each residual *decision* those findings imply
is tracked in the PR issue that owns the work (see the map at the end); this doc no
longer holds open questions.

---

## Finding 1 — Negative-position record-union widens to ⊤ (verified)

A **supertype** union of ≥2 distinct-field records over-approximates to ⊤. In
`NormalForms.scala`, `RhsNf.| (Var, FieldType)` bails to `None` on a second
differently-named field (`case _: RhsField | _: RhsBases => N`), and `None` means
Top (the authors' own comment: "it's the same as Top"). Scope is narrow — negative
position only: positive-position unions are precise multi-member DNFs, and tagged
unions are precise because object tags get a *list* slot (`RhsBases.prims`) while
records get a single one.

**Residual decision:** either give Escalier's `RhsNf` a set-valued record slot
(precise), or route would-widen union-supers through the retained `trialAndCommit`
exists-rule instead of `constrainNF` (sound — trials each member). Regression rows
are in the PR2 corpus (#1059). Owned by **PR3 (#1060)** (the `RhsNf` shape) and
**PR5 (#1062)** (the routing).

## Finding 2 — Arrow intersections are merged naively, not decomposed (verified)

MLscript merges intersected arrows during normalization — `NormalForms.scala:58`
gives `FunctionType(l0|l1, r0&r1)` = `(A|B)→(C&D)` — and applies the **plain arrow
rule** to the merged result (`ConstraintSolver.scala:172` routes to `rec`; `rec` at
`:255` is contra-param/cov-return, no decomposition). So the merge is exact when
codomains agree (worked example A) but **unsound** when they conflict (example B:
`boolean & null = never`): example B *holds* in MLstruct, diverging from both TS and
the set-theoretic reading — see [04-type-level-operators.md](04-type-level-operators.md).

**Residual decision:** to keep conditional-type `extends` set-theoretically sound,
implement the Frisch–Castagna–Benzaken arrow decomposition rather than inheriting
MLstruct's merge — or deliberately accept and document the non-standard result.
Owned by the subtyping core — **PR5 (#1062)** (the arrow-vs-arrow decision) and
**PR3 (#1060)** (whether `LhsNf` merges arrows or keeps a set for decomposition) —
and surfaced to users through conditional types in **PR10 (#1067)**.

## Finding 3 — The overload dispatcher consumes per-arm annotations (verified)

`internal/codegen/builder.go`'s `buildOverloadedFunc` sorts arms by specificity
(param count, then type) and `buildTypeGuard` emits `typeof` / `instanceof` /
`Array.isArray` guards from **each arm's written parameter annotations** — the
artifact trigger 3's inference win removes. So relaxing the overload-annotation rule
must be scoped, and static resolution must pick the same arm the dispatcher routes
to (example A is where the two can disagree).

**Residual decision:** the annotation-obligation scope plus the static-vs-runtime
agreement test. Owned by **PR11 (#1068)**, and half settled there. The obligation
covers *parameter* annotations on arms with a body, the arms `buildOverloadedFunc`
emits a branch for; `checkOverloadDispatch` reports the rest. Declare-only arms are
exempt, and inference carries no obligation at all, because `fuseOverloadArms` hands
the whole set to the lattice as one intersection of arrows.

The agreement half is deferred until the new checker feeds codegen, which is the **M12
flip** in milestone 1 rather than anything MLstruct owns. `internal/codegen` reads the
old `internal/checker`, which resolves overloads by first-match rather than by
specificity, and nothing outside `internal/solver` imports the new checker. An ordering
built to match `specificityOrder` would therefore disagree with the checker actually
feeding codegen. Reconcile the two once the flip makes them one checker, reading
inferred types rather than written annotations (#1152). **PR12 (#1069)** assumes the
new checker is at or near default, so its rollout is the natural place for this to
land.

## Finding 4 — `¬Ref` premises hold; the invariant is a construction-site guard (verified)

`RefType.Accept` does not walk the lifetime, and `RefType` is already handled in the
`rec`-layer structural switch (bypassing the lattice block). So `¬(mut 'a T)` has no
sound lifetime and is forbidden by construction, while `mut 'a ¬T` (negation *inside*
the inner) is fine.

**Residual work:** enforce the panic + tests (`no NegationType over a RefType; refs
bypass constrainNF`). Owned by **PR8 (#1065)**.

---

## Owning-PR map

| Verified finding | Residual decision / work | Owner(s) |
|---|---|---|
| 1 — record-union ⊤-widening | set-valued `RhsNf` vs exists-rule fallback | PR3 #1060 / PR5 #1062 |
| 2 — naive arrow merge | FCB decomposition vs accept non-standard `<:` | PR5 #1062, PR3 #1060, PR10 #1067 |
| 3 — dispatcher needs annotations | annotation-scope + static/runtime agreement | PR11 #1068 |
| 4 — `¬Ref` guard | construction-site panic + tests | PR8 #1065 |
| (all) regression oracle | negative-position record + arrow-intersection rows | PR2 #1059 |

The investigation is complete; the decisions above live in their owning PR issues.
This doc stays as the source-grounded record of what those decisions rest on.
