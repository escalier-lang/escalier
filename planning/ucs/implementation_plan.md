# UCS conditional-normalization IR — Implementation Plan

This plan implements Phase 1 of the MLstruct rework-minimization plan,
[issue #882](https://github.com/escalier-lang/escalier/issues/882), scoped to
`internal/solver`. It sequences the work into dependency-ordered pull requests,
each sized to be reviewable on its own. The follow-up is Phase 2,
[issue #883](https://github.com/escalier-lang/escalier/issues/883), the MLstruct
graft that reuses this IR.

## What UCS gives us

The design adopts the desugar-then-normalize pipeline from "The Ultimate
Conditional Syntax" (Cheng & Parreaux, OOPSLA 2024). Three terms recur below:

- **Desugared core.** A small term language the rich conditional surface lowers
  into. It has one `Split` node that tests a scrutinee against a sequence of
  branches, `Let` nodes for intermediate bindings, guard tests, and leaf nodes
  that hold an arm body. The core still carries nested patterns.
- **Normalized form.** A backtracking-free rewrite of the core. Nested patterns
  are flattened into successive scrutinee splits, so each split tests exactly one
  tag-level of one scrutinee and hands its sub-scrutinees to inner splits. The
  checker then reasons about one tag-level at a time instead of walking deep
  nesting.
- **Scrutinee.** The value a split tests. The top-level scrutinee is the match
  target. A nested split's scrutinee is a projection of an outer one, for example
  the field `x` of an object the outer split already matched.

The normalized form is the shared IR for type checking, coverage checking, and
later codegen. Phase 1 builds it and drives type checking and the interim
top-level coverage check off it. Comprehensive nested-match exhaustiveness rides
Phase 2 (#883) via the negation algebra, not this work.

## Scope and constraints

- **Solver only.** All work lands in `internal/solver` and a new pure-IR
  subpackage. Nothing here depends on negation types, DNF/CNF, or the MLstruct
  graft. It is `internal/solver`, not the legacy `internal/checker`.
- **Subsumes four ad-hoc paths.** Today `match`, `if let`, `val … else`, and
  refutable-pattern guard handling are four separate hand-written paths in
  `internal/solver`. This plan unifies them under one desugar → normalize →
  check pipeline. The paths and their current homes:
  - `inferMatch` — [internal/solver/infer_expr.go:2476](../../internal/solver/infer_expr.go)
  - `inferIfLet` — [internal/solver/infer_expr.go:2356](../../internal/solver/infer_expr.go)
  - `inferLetElse` — [internal/solver/infer_expr.go:2423](../../internal/solver/infer_expr.go)
  - `bindRefutable` — [internal/solver/infer_expr.go:2386](../../internal/solver/infer_expr.go)
  - dispatch at [internal/solver/infer.go:423](../../internal/solver/infer.go) and
    [internal/solver/infer_stmt.go:128](../../internal/solver/infer_stmt.go).
- **Interim coverage stays.** The existing top-level exhaustiveness check moves
  onto the normalized form with its semantics unchanged. No new coverage
  algorithm is written here — that is Phase 2's residual-based check. The interim
  helpers are `checkMatchExhaustive`, `unionMatchExhaustive`, `armCoversShape`,
  `structuralInexact`, `narrowMatchArm`, and `isCatchAll`, all in
  [internal/solver/infer_expr.go](../../internal/solver/infer_expr.go).
- **Reuse the shared pattern path.** Leaf binding keeps going through
  `bindPattern` / `bindPatternWith` in
  [internal/solver/pattern.go](../../internal/solver/pattern.go). The IR decides
  *which* scrutinee a leaf binds against; `bindPattern` still does the
  member-lookup constraints and the borrow-mode projection.

### Out of scope

- **Codegen (M10).** The IR package is placed so `internal/codegen` can import it
  later, but no codegen consumer is written here.
- **Pattern alternatives / or-patterns.** The surface form the issue lists as
  "pattern alternatives" has no AST node today —
  [internal/ast/pattern.go](../../internal/ast/pattern.go) has no `OrPat`. The
  desugarer is shaped to accept one branch producing several core branches, but
  wiring real alternatives needs parser and AST work first. Flagged, not built.
- **`try` / `catch` arms.** `TryCatchExpr` carries `[]*MatchCase` for its catch
  clauses but has no solver typing yet. The desugarer is designed so catch arms
  can lower through the same `Split`, but throws-narrowing is a Phase 2 (#883)
  payoff and is not part of this plan.

## Package layout

Put the pure IR in a new subpackage `internal/solver/ucs`:

- `ucs` holds the core and normalized ADTs, the desugarer, the normalizer, and a
  printer. It imports only `internal/ast`. It never imports `internal/solver` or
  `internal/soltype`, so it stays acyclic and additive.
- The typing walk and the coverage check stay in `internal/solver`, which imports
  `ucs`. They need the checker's mutable `Context`, `bindPattern`, and
  `soltype`, none of which the IR should pull in.

This boundary lets `internal/codegen` import `internal/solver/ucs` for M10 without
a dependency on the solver engine, matching the acyclic layering the package doc
in [internal/solver/doc.go](../../internal/solver/doc.go) already relies on.

## Pull requests

The PRs are ordered so each merges without the next. PR1 through PR3 are pure IR
with no behavior change. PR4 flips type checking onto the IR. PR5 moves coverage.
PR6 deletes the superseded code.

### PR1 — Core and normalized IR ADTs plus a printer

Add the `internal/solver/ucs` package with the term types and a printer, wired
into nothing.

- Define the desugared-core ADT: a `Split` over a scrutinee with an ordered list
  of branches, a `Let` node for an intermediate binding, a guard-test node, and a
  leaf node carrying the arm's body expression and its source span.
- Define the normalized-form ADT: a split whose branches each test one tag-level
  and whose sub-scrutinees are projection paths into the matched value, plus a
  default / fallthrough tail.
- Define `Scrutinee` as either the root match target or a projection path
  relative to an enclosing scrutinee, so a nested split names its value without
  re-inferring it.
- Add a `String()` printer over both ADTs so tests can lock IR shape with
  `snaps.MatchInlineSnapshot` per the testing guidance in
  [CLAUDE.md](../../CLAUDE.md), rather than drilling into fields.

**Tests.** Constructor and printer round-trips on hand-built IR values. No
solver behavior changes.

### PR2 — Desugar the surface into the core

Add `desugar` in `internal/solver/ucs`: a pure function from the AST conditional
surface to the desugared core.

- Lower `MatchExpr` arms into a `Split` whose branches carry each arm's pattern,
  optional guard, and body. Guards become guard-test nodes on their branch, not
  inline boolean handling.
- Lower `IfLetExpr` into a `Split` with the pattern branch and the `else`
  fallthrough.
- Lower a `val pat = init else { … }` `VarDecl` into a `Split` with the pattern
  branch and the diverging-or-fallback `else`.
- Represent intermediate bindings introduced by desugaring as `Let` nodes so
  later stages see them uniformly.

**Tests.** Snapshot the core IR for a representative source of each surface form.
No typing yet; the desugarer is not called from `inferMatch` in this PR.

### PR3 — Normalize the core into the backtracking-free form

Add `normalize` in `internal/solver/ucs`: desugared core to normalized form.

- Flatten nested patterns into successive scrutinee splits. An object or tuple
  pattern becomes an outer split on the container tag whose branches split again
  on the projected sub-scrutinees, one tag-level per split.
- Merge branches that test the same scrutinee against different tags into one
  split, so the checker visits each scrutinee once.
- Thread a default / fallthrough tail through every split so the form is
  backtracking-free: a failed test falls to the tail, never re-tries an earlier
  branch.
- Before finalizing the split and tail shape, confirm the details against the
  UCS paper's normalization section and the `hkust-taco/ucs` reference, per the
  issue's fourth task.

**Tests.** Snapshot the normalized IR for nested patterns, overlapping arms, and
guarded arms, asserting the one-tag-level-at-a-time shape.

### PR4 — Type-check the conditional surface off the normalized form

Rewrite the four ad-hoc paths to desugar, normalize, then walk the normalized
form. This is the first behavior-affecting PR.

- Replace the bodies of `inferMatch`, `inferIfLet`, `inferLetElse`, and
  `bindRefutable` with a single walk over the normalized form. Each split projects
  its scrutinee's type, each leaf infers its body, and non-diverging bodies
  constrain into one fresh branch-join var, as the current code already does.
- Bind leaves through the existing `bindPattern` / `bindPatternWith`. The IR
  supplies the projected sub-scrutinee type for each leaf; `bindPattern` keeps
  emitting the member-lookup constraints and the borrow-mode projection it does
  today.
- Type each guard-test node as a boolean over its branch's bindings, matching the
  current inline guard constraint.
- Preserve the provenance edges `MatchBranch`, `IfLetBranch`, and `LetElseBranch`
  from [internal/solver/prov.go](../../internal/solver/prov.go) so branch-join
  vars still render with their source.
- Preserve `checkUniformOwnership`
  ([internal/solver/infer_expr.go:514](../../internal/solver/infer_expr.go)) and
  the divergence-join behavior where an all-diverging match coalesces to `never`.
- Reproduce `narrowMatchArm`'s union narrowing through the split projection: an
  arm that destructures one union variant must still bind against only that
  variant's members, so no regression in variant-narrowing.

**Tests.** The full existing solver pattern and if-let suites must stay green:
`infer_pattern_test.go`, `infer_pattern_nominal_test.go`,
`infer_pattern_mut_test.go`, `infer_if_let_test.go`, and the match cases in
`infer_expr_test.go`. Run `go test ./...`, and `UPDATE_SNAPS=true` only for
intended IR-print snapshots.

### PR5 — Run the interim coverage check off the normalized form

Move the top-level exhaustiveness check onto the IR without changing its verdict
on any current input.

- Reimplement `checkMatchExhaustive` to read the normalized form's top-level
  split and its default tail instead of the `matchShape` scrutinee snapshot taken
  in `inferMatch`.
- Keep the interim semantics identical: an inexact scrutinee needs a catch-all, an
  exact union is covered when every member has an unguarded covering branch, and a
  guarded branch covers nothing. This is the seam Phase 2 (#883) later replaces
  with `residual = scrutinee ∧ ¬covered ; exhaustive iff residual <: ⊥`.

**Tests.** Every current `NonExhaustiveMatchError` case keeps its exact message,
asserted in full per [CLAUDE.md](../../CLAUDE.md). The `matchShape` snapshot logic
in `inferMatch` is removed once coverage reads the IR.

### PR6 — Remove the superseded ad-hoc helpers

Cleanup only, no behavior change.

- Delete the now-dead helpers `unionMatchExhaustive`, `armCoversShape`,
  `structuralInexact`, `narrowMatchArm`, and the pattern-shape branches of
  `isCatchAll` that the IR walk subsumes.
- Fold any still-needed predicate into the `ucs` package if the coverage walk
  reuses it, keeping the solver side free of pattern-shape casing.

**Tests.** `go test ./...` unchanged; this PR removes code with no reachable
callers after PR5.

## Handoff to Phase 2 (#883)

Phase 2 plugs into two seams this plan creates:

- **The normalized form IR** is what `#883`'s residual coverage check consumes.
  `residual = scrutinee ∧ ¬covered` is computed over the same splits, and the
  residual's DNF is the uncovered witness set.
- **`checkMatchExhaustive`** is the function `#883` supersedes. Phase 1 leaves it
  reading off the IR so Phase 2 swaps the body for the algebra without touching
  the surface lowering.

The IR is also the M10 codegen substrate. Keeping it in `internal/solver/ucs`
with an ast-only dependency lets codegen import it later without a solver cycle.

## Risks

- **Projection must match `bindPattern`.** The split's sub-scrutinee type has to
  be the same projection `bindPattern` would compute through `CarrierOf` and the
  member-lookup path, or leaf types drift. Mitigate by having the IR walk call the
  existing projection rather than recomputing it.
- **Union-variant narrowing.** `narrowMatchArm` currently drops the union members
  an arm cannot destructure. The normalized split must reproduce this so a
  one-variant arm does not bind against the whole union. Snapshot the normalized
  IR for a union scrutinee to lock the split boundaries.
- **Guard and binding scope.** A guard sees its arm's bindings; the `else` of an
  `if let` and a `val … else` does not. The IR must keep guard tests inside the
  bound branch and fallthroughs outside it, matching the current child-scope
  discipline.
- **Snapshot churn.** PR4 and PR5 should not move any inferred type or error
  message. Land IR-print snapshots in PR1 through PR3 so PR4's diff is limited to
  the walk, making an accidental behavior change visible in review.
