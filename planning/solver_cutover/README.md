# Solver cutover

A re-sequencing of [planning/simple_sub/](../simple_sub/) aimed at one outcome:
make `internal/solver` the checker the compiler runs, as early as possible.

The existing milestone order puts library ingestion, a fixture harness, codegen,
the LSP, and a diagnostics capstone ahead of the flip. Most of that ordering is
sound. One part of it is not, and it is the part currently absorbing the effort.
The plan treats "every pseudo-package ingests cleanly" as a single prerequisite.
The measurements in [00-current-state.md](00-current-state.md) show the two
halves are nothing alike. `std:*` is nearly there, and `web:*` is a long grind
the compiler's own path never touches.

## The strategy in one paragraph

Split the library-ingestion prerequisite at the `std:` / `web:` line, and split
the M12 flip from the M12 deletion. The `std:*` tree ingests with a handful of
root causes left; the `web:*` tree carries roughly ten times as many diagnostics
and no fixture, no compiler entry point, and no Escalier source file in this
repository imports from it. Finish `std:*`, quarantine `web:*` behind a ledger
test that keeps it from rotting, point the compiler at the solver, and leave
`internal/checker/` in the tree unreferenced until the LSP and the diagnostics
audit are done. The flip becomes cheap because the expensive half of M12 is the
deletion, not the switch.

## Documents

- **[00-current-state.md](00-current-state.md)** — what has landed, what the
  measurements say, and the gaps between the solver's API and the compiler's
  needs. Read this first. The phase order in `01` follows from its numbers.
- **[01-cutover-plan.md](01-cutover-plan.md)** — phases P0 through P7, each with
  a scope, a gate, and an explicit statement of what it does not do.
- **[02-parked-work.md](02-parked-work.md)** — the pseudo-package work this plan
  defers, the files that hold it, and the order to resume it in.

## One decision to confirm before P2

Two shipped features work on the old checker and have no solver implementation:
imports from `node_modules`, and JSX. Neither has fixture coverage, so the
harness in P2 will not flag them, and neither is on the critical path to the
flip. [01-cutover-plan.md](01-cutover-plan.md) parks both, which means P5 lands
a checker that type-checks less than the one it replaces.

That is a real cost, and parking a regression is a heavier call than parking
polish, so it should be a decision rather than a default. The alternative is a
phase between P4 and P5 that ports the resolver chain and then JSX — roughly 870
lines of checker code and 3,100 lines of tests to carry over. Pick one before
P2 starts, because the answer changes what the P2 harness needs to cover.

## The preservation rule

Nothing in the pseudo-package workstream is deleted, disabled, or simplified by
this plan. That includes the partition table, the tiering pass, the reference
rewriter, the generator, the committed `.esc` tree, and the solver's package
loader. [02-parked-work.md](02-parked-work.md) lists every file and states what
protects it. A phase that says "quarantine" means a package stops gating
CI, not that its code or its data leaves the tree.
