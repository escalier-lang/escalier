# 01 — Cutover plan

Eight phases. P0 through P5 reach the flip, and P6 and P7 finish the migration.
Each phase states what it does not do. The plan is short because it keeps
refusing work the flip does not need.

## The two splits this plan makes

**Split the library prerequisite near the `std:` / `web:` line.** M7.5 and
builtins §7 treat clean ingestion of the pseudo-package tree as one gate. The
measurements in [00-current-state.md](00-current-state.md) say the two halves
are nothing alike: `std:*` is four root causes from clean, and `web:*` carries
roughly 2,500 diagnostics concentrated in DOM types.

The split is near the line rather than on it. `web:core` is the shared floor
under every `web:*` sibling at 224 diagnostics, and `fixtures/async_await` needs
`web:fetch`, which reaches it. So the prerequisite is `std:*` plus `web:core`
plus `web:fetch`, and everything behind `web:dom` is not.

**Split the M12 flip from the M12 deletion.** M12 reads as one step: default the
compiler to the solver, retire `internal/checker`, delete the AST's
`inferredType` field. Those have different prerequisites. Defaulting needs
codegen to work. Deleting needs the LSP ported and the diagnostics audit done,
because `cmd/lsp-server` imports `checker` and because M11.5 wants the old
checker's diagnostics as its parity baseline.

Keeping `internal/checker/` in the tree satisfies both and costs nothing but
build time. After P5 it is no longer the compiler's default, and it stays
reachable three ways: through P2's environment variable, through
`cmd/lsp-server`, which imports it until P6, and through its own test suite,
which is M11.5's parity baseline. P7 is what removes it.

## Phase order

```
P0 (std:* ingestion) ──┐
                       ├──► P2 (flag + check-only harness) ──► P3 (JS codegen) ──► P4 (.d.ts codegen) ──► P5 (flip + fixture imports)
P1 (web:* quarantine) ─┘                                                                                      │
                                                                                                              ├──► P6 (LSP)
                                                                                                              └──► P7 (deletion) ── needs P6 + M11.5
```

P0 and P1 are independent of each other and can land in either order, except
that P0's `web:core` and `web:fetch` rows leave the P1 ledger when P0 clears
them. P6 and P7 are the only phases after the flip.

---

## P0 — Clear the `std:*` ingestion tail, plus `web:core` and `web:fetch`

**Goal.** Every `std:*` package loads with zero diagnostics, and so do
`web:core` and `web:fetch`.

**Work.** The four root causes from
[00-current-state.md](00-current-state.md)§"What the residual diagnostics
actually are":

1. Add the `*ast.BigintTypeAnn` arm to `internal/solver/type_ann.go`. Port the
   shape from `internal/checker/infer_type_ann.go:99`. This clears 137
   diagnostics. Three solver tests assert the current `Unsupported:
   BigintTypeAnn` message and need updating: `infer_async_test.go:419` and `:434`,
   `infer_throws_test.go:394`.
2. Find why a bare sibling name fails to resolve inside `std:typed_arrays` while
   the qualified spelling works. The fix is either in the generator's reference
   rewriter, `internal/dts_to_esc/ref_rewrite.go`, or in how a group member's own
   namespace is searched during a load, `internal/solver/stdlib_group_load.go`.
   Decide which from a minimal reproduction before touching either. This clears
   82 diagnostics.
3. Defer a residual conditional rather than constraining it against a type
   parameter's bound, so `Omit<T, K> = Pick<T, Exclude<keyof T, K>>` checks. This
   is a bound-check ordering question in the type-operator evaluator, not new
   operator surface.
4. Make a numeric indexed access against a type parameter bounded by
   `Array<unknown> | []` yield the element type instead of constraining the
   tuple against `number`.

Then the tail under 70: the `owned-mutable field annotation` reports, the
`typeof of a name that is not a readable value` reports, the two overload
reports, and the inherited-member redeclarations. Triage each as a solver gap or
a generator gap and file the generator ones against builtins §6 rather than
hand-editing the committed tree, which is generated output.

Then `web:core` and `web:fetch`. They are here rather than in P1 because
`fixtures/async_await` calls `fetch`, which the old checker supplies ambiently
from `lib.dom.d.ts` and the solver supplies only from `web:fetch`. `web:core` is
the shared floor under every `web:*` sibling, so clearing it moves all ten at
once and shrinks what P1 has to quarantine. Triage its 224 diagnostics before
committing to this scope; if they turn out to be the DOM-name mass rather than a
few root causes, the cheaper answer is to rewrite `fixtures/async_await` to
declare its own `fetch` and move both packages to P1.

**Gate.** A test that loads every `std:*` package, plus `web:core` and
`web:fetch`, and asserts an empty diagnostic list. This is the strict version of
the P1 ledger and replaces those rows in it.

**Not in scope.** Any `web:*` package behind `web:dom`. Regenerating the tree.
Hand-edits to generated `.esc` files.

---

## P1 — Quarantine `web:*` behind a ledger

**Goal.** Everything behind `web:dom` stops gating anything, without rotting
while parked. `web:core` and `web:fetch` belong to P0, not here.

**Work.** Commit the per-package survey from
[00-current-state.md](00-current-state.md)§"How these numbers were taken" as a
real test in `internal/solver`. It loads each package's closure, counts
diagnostics, and compares against a checked-in baseline table. A count that
grows fails. A count that shrinks fails too, with a message saying to lower the
baseline, so improvements land in the table instead of silently widening the
allowance.

Record the top unresolved names alongside the counts. The five DOM names in
[00-current-state.md](00-current-state.md) account for 1,829 of the 2,397
`cannot find type` reports, so a future `web:dom` session starts from a ranked
list rather than a wall.

**Gate.** The ledger test passes at the recorded baseline, and CI runs it.

**Not in scope.** Fixing any `web:*` diagnostic. Deleting or moving any `web:*`
data file, the partition table, the tiering pass, or the loader. See
[02-parked-work.md](02-parked-work.md).

---

## P2 — Solver behind a flag, with a check-only fixture harness

**Goal.** Find out what the solver actually rejects in real Escalier code,
before any codegen work is spent.

**Work.**

1. Close the two API gaps the compiler path needs, from
   [00-current-state.md](00-current-state.md)§"Gaps between the solver's API and
   the compiler's needs": a lib-scope argument on `InferScript`, and the dep
   graph on `ModuleResult`. Third-party `.d.ts` ingestion and JSX inference are
   the other two gaps and are **not** needed here; both are parked in
   [02-parked-work.md](02-parked-work.md).
2. Add a solver path at the five `checker.NewChecker` sites in
   `internal/compiler/compiler.go`, selected by one environment variable. The
   `type_system.Namespace` in the public signatures of `CheckBinScript`,
   `CompileScript`, and `collectUsedLibSymbols` is the seam that resists this;
   the cheapest shape is a small interface or a per-checker pair of entry points
   rather than a `soltype` to `type_system` bridge, which this plan never builds.
3. Add the second fixture harness, M8 phase 1: a sibling to
   `cmd/escalier/fixture_test.go` that runs the solver over every fixture and
   asserts accept or reject, with no codegen and no `build/` comparison. Give it
   a per-fixture skip list, seeded with everything that fails on day one.
4. Burn the skip list down. This is the phase where the solver's remaining
   language-surface gaps surface. Two are known from the milestone plans and may
   or may not bite: M6 PR7, which is `if`-`val` and `val`-`else`, and M9 PR9f,
   which is regular-tree normalization.

**Gate.** The skip list is empty, or every remaining entry is a triaged intended
improvement with a note naming why the divergence is right.

**Not in scope.** Codegen. `build/` goldens. The LSP. Changing the default
checker. Adding imports to fixtures, which is P5.

---

## P3 — JS emission on the solver

**Goal.** `BuildTopLevelDecls` produces identical JS from solver results.

**Work.** Replace the five `InferredType()` read sites in
`internal/codegen/builder.go` and `js_lowering.go` with `Info` lookups. They ask
five questions, all expressible over `soltype`: is the callee a nominal object,
does it carry a constructor element, is this expression a function, does an
optional-chaining target's union include `null` or `undefined`, and is a member
expression's object a namespace. `js_lowering.go` also reads the AST's
`BindingOwner` field, which P7 re-homes.

The emitter is otherwise driven by the AST and the dep graph, which are
checker-agnostic, so this is a narrow change rather than a port.

**Gate.** Every fixture's `build/lib/index.js` and `index.js.map` is
byte-identical under both checkers. Extend the P2 harness to compare them.

**Not in scope.** `.d.ts`. `self_type_utils.go`, which serves `.d.ts` alone.

---

## P4 — `.d.ts` emission on the solver

**Goal.** `BuildDefinitions` produces `.d.ts` from solver results.

**Work.** This is the largest single piece of the cutover. `BuildDefinitions`
takes a `type_system.Namespace` and renders through
`buildTypeAnn(type_sys.Type)`, about half of `dts.go`'s 1,350 lines, plus all of
`self_type_utils.go`.

Write a `soltype` twin of `buildTypeAnn` rather than a `soltype` to
`type_system` bridge. A bridge would have to reconstruct a representation the
whole migration exists to retire, and it would keep `type_system` alive past P7.

`internal/soltype/print.go` already renders every `soltype` former for
diagnostics. It emits Escalier syntax, not TypeScript, so it is a reference for
the case analysis rather than something to reuse directly.

**Gate.** Every fixture's `build/lib/index.d.ts` is byte-identical, or its diff
is triaged and recorded.

**Not in scope.** The `@escalier-type` JSDoc round-tripping for exactness and
the value-level `exact<T>(v)` lowering, both of which M10 owns and neither of
which the current fixtures exercise.

---

## P5 — Flip the default

**Goal.** `internal/solver` is the checker the compiler runs.

**Work.** One pull request doing three things together:

1. Default the environment variable from P2 to the solver, keeping the old
   checker reachable by setting it the other way.
2. Add `import "std:…"` to the roughly 20 fixtures that name `console`,
   `String`, `Date`, `Number`, or `Object`. These must ride in the same commit
   as the default change, because the old checker cannot resolve those imports
   against the committed tree. That is why both existing stdlib fixtures are
   marked `DISABLED`, and it is the one place this plan accepts a moment where
   the two harnesses cannot both be green on the same tree.
3. Re-enable `fixtures/stdlib_import_local` and
   `fixtures/stdlib_import_class_via_namespace` by removing their `DISABLED`
   markers. Builtins §7 also records four disabled `TestStdlibImport_*` tests;
   those now run against a `t.TempDir` tree rather than the committed one, so
   confirm which of §7's disablements are still live before acting on that note.

**Gate.** `go test ./...` passes with the solver as the default. The old-checker
harness is retired in the same commit, not kept limping.

**Not in scope.** Deleting anything. `internal/checker`, `internal/type_system`,
the AST's `inferredType` field, and `internal/checker/tests` all stay, compiled
and tested, as the parity baseline M11.5 needs.

---

## P6 — LSP on the solver

**Goal.** `cmd/lsp-server` stops importing `internal/checker`.

**Work.** This is M11 unchanged. 86 non-test references, 76 of them in
`completion.go`, plus 51 in `completion_test.go`. The LSP runs on the old checker
from P5 until this lands, which is safe because both packages are in the tree.

**Gate.** No `checker` or `type_system` import under `cmd/lsp-server`, and the
existing LSP tests pass.

---

## P7 — Deletion

**Goal.** Finish M12.

**Work.** Delete `internal/checker`, `internal/checker/tests`,
`internal/type_system`, the AST's `inferredType` field and its
`InferredType`/`SetInferredType` accessors, the `type Type = type_system.Type`
alias, and `tools/gen_ast`'s generation of the field. Re-home the two
`type_system` names `internal/ast` still carries, `type_system.Type` and
`type_system.BindingOwner`. Retire `internal/simplesub`'s differential harness,
which imports the old checker.

**Depends on** P6, so nothing still imports the old checker, and on M11.5, whose
audit wants the old checker's diagnostics as the parity baseline.

**Not in scope.** Anything in [02-parked-work.md](02-parked-work.md).

---

## What this plan deliberately leaves undone at the flip

Stated plainly so nobody reads P5 as "the migration is finished":

- Everything behind `web:dom` ingests with roughly 2,500 diagnostics, so a
  program importing one of those packages gets a wall of noise. Worse, the DOM
  is ambient on the old checker, which loads `lib.dom.d.ts` into the global
  scope, so a program writing `document` or `Element` today has to import a
  package that does not work. The ledger from P1 is the honest record.
- Third-party `.d.ts` ingestion does not work on the solver. A program importing
  from `node_modules` does not type-check.
- JSX does not type-check on the solver. `internal/solver` has no JSX handling
  at all, and JSX reaches `@types/react` through the same resolver chain, so it
  is downstream of the previous item.

  These two are the genuine capability losses at the flip, and neither has
  fixture coverage, so P2's harness will not flag them. They are parked rather
  than dropped; [02-parked-work.md](02-parked-work.md) says how they are kept
  asserted.
- The per-file shape loader, builtins §9, does not exist, so a file must import
  a package to reach a literal's method surface.
- Diagnostics quality has had no cross-cutting pass. M11.5 still owes that.
