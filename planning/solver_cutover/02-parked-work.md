# 02 — Parked work

Everything this plan defers, where its code lives, and the order to resume it
in. Nothing listed here is deleted, disabled, or simplified by
[01-cutover-plan.md](01-cutover-plan.md).

## The preservation inventory

These files are the pseudo-package workstream. They stay in the tree, stay
compiled, and stay covered by their existing tests through every phase,
including P7. A phase that says "quarantine" means a package stops gating CI,
not that anything here leaves.

### Generator and partitioning

`internal/dts_to_esc/` — the whole package. The pieces the pseudo-package model
depends on directly:

| File | What it holds |
| --- | --- |
| `partition.go` | the hand-maintained routing table from a TS lib name to a `std:` / `web:` / `node:` package, plus the DOM residual allowlist and the unmapped-symbol fail-safe |
| `partition_writer.go` | the bucketing, cross-file merge, and tree write |
| `tier.go`, `references.go` | the runtime tiering pass and the reference graph it reads |
| `ref_rewrite.go`, `import_header.go` | cross-package reference qualification and the per-package import header |
| `generate.go`, `overlay*.go`, `facts.go` | the generation model and its three inputs |

`tools/dts_to_esc/` — the `bootstrap`, `generate`, and `check` subcommands, and
`check_generated_tree_test.go`, which proves the committed tree matches a
regeneration.

### Committed pseudo-package data

`internal/interop/data/` — 38,666 lines of generated `.esc` across 48 packages,
1.5 MB under `web/` and 532 KB under `std/`. This is generated output. Fixing a
diagnostic in it means fixing the generator, the overlay, or `internal/ecma262/curated.json`, and
regenerating.

### Solver-side loading

| File | What it holds |
| --- | --- |
| `internal/solver/stdlib_import.go` | scheme-prefixed URI recognition and the `ModuleSource` over a stdlib directory |
| `internal/solver/stdlib_closure.go` | the import closure a module's roots reach, grouped |
| `internal/solver/stdlib_group_load.go` | loading a mutually-importing group as one module under synthetic per-package paths |
| `internal/solver/stdlib_parse_cache.go` | the parse cache a second load reuses |
| `internal/solver/package_load.go`, `package_registry.go` | per-package inference, the published surface, and cycle detection |
| `internal/solver/namespaces.go` | the path-derived namespace a group member's declarations land in |

### Planning documents

`planning/builtins/` and `planning/packages_vs_globals/` stay as written. This
plan re-sequences them; it does not supersede them. Their §-numbered phases are
the resume points below.

## What is parked, and against what

| Parked | Owner | Why it is safe to park |
| --- | --- | --- |
| `web:dom` ingestion and the eight packages behind it, roughly 2,500 diagnostics | builtins §7 | **not safe to park silently** — see below. `web:core` and `web:fetch` are P0's, not parked |
| DOM-touching fixture migration | builtins §8 | `fixtures/async_await` is the only one, and P0 covers it by clearing `web:fetch` |
| Per-file shape loading, the FR11 trigger map | builtins §9 | a file can import the package instead; the cost is verbosity, not capability |
| Intrinsics, adaptive rendering, auto-import quick-fix | builtins §10 | tooling on top of a working checker |
| Third-party `.d.ts` ingestion on the solver | M7.5 tail | **not safe to park silently** — see below |
| JSX inference on the solver | `planning/react_and_jsx/` | **not safe to park silently** — see below |
| Regenerating the committed tree | builtins §6, §7 | P0's confirmed causes are solver gaps and need no regeneration. Its one unresolved cause may turn out to be a generator gap, and a regeneration is in P0's scope if it does |
| M6 PR7, `if`-`val` and `val`-`else` | M6 | P2's harness says whether a fixture needs it |
| M9 PR9f, regular-tree normalization | M9 | same |
| M11.5, the diagnostics capstone | M11.5 | its precondition is the old checker still being in the tree, which P5 preserves and P7 ends |

## The three real regressions at the flip

These are the exceptions in that table. All three are shipped capabilities that
work on the old checker and do not work on the solver. The first two have no
fixture coverage at all, so the P2 harness will not surface them.

### Third-party `.d.ts` ingestion

The old checker resolves an import from `node_modules` through
`internal/resolver`, `dts_parser`, and `dts_to_esc.ConvertModule`. The solver's
`bindImport` sends a non-scheme URI to `loadPackage`, which asks the run's
`ModuleSource`, and no `ModuleSource` in the tree answers for `node_modules`.

The work is bounded: write a `ModuleSource` that routes a non-scheme URI through
the existing resolver chain to an `*ast.Module`. The front half of that chain is
already checker-agnostic, which is what M7.5 means by "interop reuse, gate
intact". It is parked because it is not on the critical path to the flip, not
because it is hard.

### JSX

`internal/solver` contains no reference to `JSX`. The old checker has
`infer_jsx.go` at 677 lines and `react_types.go` at 194, with 3,126 lines of
tests under `internal/checker/tests/jsx_test.go`. `internal/codegen/jsx.go`
emits JSX, so the feature ships today. `react_types.go` finds `@types/react`
through `internal/resolver`, which makes JSX downstream of the previous item:
port ingestion first, then JSX on top of it. `planning/react_and_jsx/` holds the
design.

### The ambient DOM surface

`internal/checker/prelude.go:529` appends `lib.dom.d.ts` to the old checker's
global load, so a program writes `fetch`, `document`, or `Element` with no
import today. The solver has no ambient surface beyond `std:prelude`, so those
names come only from a `web:*` package, and every package behind `web:dom` sits
at 2,707 diagnostics.

This is the one regression with fixture coverage: `fixtures/async_await` calls
`fetch`. P0 clears `web:core` and `web:fetch` so that fixture keeps working,
which leaves the DOM proper as the parked part. A program using `document` or an
element type type-checks before P5 and does not after it.

Unlike the other two, no amount of porting fixes this one quickly. It is the
`web:dom` ingestion grind, and parking it is the whole reason this plan exists.
The P1 ledger is what keeps its size visible.

### What keeps the first two honest

1. **Before P7, port the assertions.** The coverage for both lives in
   `internal/checker/tests/import_load_test.go`,
   `internal/checker/tests/package_registry_test.go`, and
   `internal/checker/tests/jsx_test.go`, all of which P7 deletes. Move enough of
   each into `internal/solver` as failing-and-skipped tests that the gap stays
   asserted rather than disappearing with the old checker's test files.
2. **Say so in the release notes for the flip.** A program importing from
   `node_modules`, and a program using JSX, type-check before P5 and do not
   after it.

## Resume order after the flip

1. **Third-party `.d.ts` ingestion**, then **JSX** on top of it. They are the
   two regressions a port closes, so they go first, in that order.
2. **`web:core` and the ten standalone `web:*` siblings.** They sit at 224 to
   279 diagnostics, and `web:core` is the shared floor under all of them, so
   fixing `web:core` moves every sibling at once.
3. **`web:dom`.** The five names in
   [00-current-state.md](00-current-state.md)§"What the residual diagnostics
   actually are" account for 1,829 of the tree's 2,397 `cannot find type`
   reports. The P1 ledger keeps that ranking current.
4. **Builtins §8 for `web:*`**, once there is a `web:*` fixture worth writing.
5. **Builtins §9**, the per-file shape loader.
6. **Builtins §10**, the tooling layer, which also wants M11 from P6.
