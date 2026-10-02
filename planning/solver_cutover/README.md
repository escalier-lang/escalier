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

Split the library-ingestion prerequisite near the `std:` / `web:` line, and
split the M12 flip from the M12 deletion. The `std:*` tree is one root cause
from clean. The packages behind `web:dom` carry roughly thirty times as
many diagnostics, and one fixture depends on that half, through `web:fetch`. So
the prerequisite is `std:*` plus `web:core` plus `web:fetch`.
Finish those, quarantine `web:dom` behind a ledger test that keeps it from
rotting, rebuild an ambient scope over what is left so `Math.PI` and
`console.log` still resolve without an import, and point the compiler at the
solver. `internal/checker/` stays in the tree afterwards, no longer the default
but still reachable behind a flag and still imported by the LSP, until P6 ports
the LSP and the diagnostics audit finishes. The flip becomes cheap because the
expensive half of M12 is the deletion, not the switch.

## Documents

- **[00-current-state.md](00-current-state.md)** — what has landed, what the
  measurements say, and the gaps between the solver's API and the compiler's
  needs. Read this first. The phase order in `01` follows from its numbers.
- **[01-cutover-plan.md](01-cutover-plan.md)** — phases P0 through P7 broken into
  about thirty-five pull requests, each with a scope, a gate, and an explicit
  statement of what it does not do. Its §"Phase order" has the dependency graph
  and says what runs in parallel.
- **[02-parked-work.md](02-parked-work.md)** — the pseudo-package work this plan
  defers, the files that hold it, and the order to resume it in.

## One decision to confirm before P2

P5 lands a checker that type-checks less than the one it replaces, in three
ways. Two are shipped features with no solver implementation and no fixture
coverage, so the P2 harness will not flag either: imports from `node_modules`,
and JSX. The third is the DOM. The old checker loads `lib.dom.d.ts` into the
global scope, so a program writes `document` or `Element` with no import, and
after the flip those names come only from `web:dom`, which does not ingest.

The rest of the ambient surface is not a loss, because P1.5 rebuilds it. That
phase was added after this section first asked the question, and it is why
`Math.PI` and `console.log` are no longer on this list.

[01-cutover-plan.md](01-cutover-plan.md) parks all three, and
[02-parked-work.md](02-parked-work.md) says how each stays visible. Parking a
regression is a heavier call than parking polish, so it should be a decision
rather than a default:

- **`node_modules` and JSX** can be bought back with a phase between P4 and P5
  that ports the resolver chain and then JSX, roughly 870 lines of checker code
  and 3,100 lines of tests.
- **The DOM** cannot be bought back cheaply. It is the `web:dom` ingestion
  grind, which is the thing this plan exists to defer.

Pick before P2 starts, because the answer changes what the P2 harness covers.

## Where this stands

Seven of the plan's pull requests have landed, and the solver now checks **43 of
the 72 runnable fixtures** with 31 skipped and none failing. #1712 replaced this
plan's predictions about what the solver cannot do with a measured skip list in
`cmd/escalier/solver_fixture_test.go`, which is the artifact to read before
picking up work. Eight causes it found were not in this plan, filed as #1714
through #1721.

`std:*` is one root cause from clean, #1659. The standalone `web:*` siblings
halved when #1650 landed. `web:dom` did not move.

## The shortest path to a measurable solver

Two phases were added after the first draft, both found by asking what the old
checker does that the solver does not. P1.7 fills six expression forms the
solver rejected, binary operators among them, of which five remain. P1.5 rebuilds the ambient builtin
surface. Until both land, running the solver over `fixtures/` reports nearly the
whole tree as failing for reasons that say nothing about the migration, so no
later phase can be measured.

That advice named #1652 first, and it landed as #1686. What has no gate on it now
is [#1673](https://github.com/escalier-lang/escalier/issues/1673) and
[#1676](https://github.com/escalier-lang/escalier/issues/1676), the two codegen
pull requests #1671 and #1675 unblocked. Everything else on the way to the flip
runs through the skip list, where the largest single cause is still
[#1666](https://github.com/escalier-lang/escalier/issues/1666) at six
fixtures.

## The preservation rule

Nothing in the pseudo-package workstream is deleted, disabled, or simplified by
this plan. That includes the partition table, the tiering pass, the reference
rewriter, the generator, the committed `.esc` tree, and the solver's package
loader. [02-parked-work.md](02-parked-work.md) lists every file and states what
protects it. A phase that says "quarantine" means a package stops gating
CI, not that its code or its data leaves the tree.
