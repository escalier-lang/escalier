# Implementation plan: Which web APIs each environment has

`requirements.md` in this folder states the problems and the requirements F1
through F9 and N1 through N5 referenced here. `vocabulary.md` has the nine
global scopes and what matching them costs. `transferable.md` works one API
family through in full.

The work is organized into the three phases section 5.3 sets out, one
environment family at a time. A stage lands on its own and leaves the tree
green.

## Prerequisite, in the other stream

`planning/web_api_types/` Stage 1 adds `@webref/idl` and emits a checked-in Go
table carrying `[Exposed]` per interface and `[Global=...]` per global scope.

§1.1 cannot start before it. An `@env("window")` is a claim about the other
eight environments, and `[Exposed]` is the only thing that knows whether the
claim is true.

The rest of phase one does not wait. §1.2 reads a `package.json` field, §1.3
checks references against whatever the annotations say, and §1.4 is tooling over
§1.2's answer, so all three can be built against a hand-written annotation set
and switched to the generated one when it lands.

## What main holds today

Reviewed against `main` at 4ae93f0b. Every premise this stream rests on is
unchanged, line numbers included.

`internal/dts_to_esc/tier.go` gives each package one of four ranks ordered by
portability, and `CheckTiers` at `import_header.go:116` reports an import edge
that goes up a rank. It still has one caller, `generate.go:138`. The compiler
pipeline has no notion of a tier, so a program can import `web:dom` and
`web:workers` together and nothing reports it.

There is no `@env`, no environment vocabulary, and no worker surface: none of
the four `*GlobalScope` classes is declared, and `importScripts`,
`WorkerLocation`, `FetchEvent` and `Clients` have zero occurrences under
`internal/interop/data/`.

Two things changed in the other stream that bear on this one.

**An ambient scope landed.** `internal/solver/ambient.go` binds 25 packages
without an import, `web:core` and `web:fetch` among them. That is the other
stream's F8, and it sharpens N4 below: the per-environment prelude this stream
needs is now a per-environment `ambientPackages` plus a cache keyed to match.

**`Window`'s globals were hoisted into `web:dom`.** 294 top-level declarations,
including portable ones such as `setTimeout` and `atob` that belong in
`web:core`. `web:dom` is browser-tier and a worker cannot import it, so §2.2
below has more to move than it did when it was written.

## Phase one: window

### 1.1 The vocabulary and the annotations

Build `@env` with all nine names from F7 and both groups, and annotate the tree
from the generated table. F7, N3, N1.

The vocabulary is complete here even though only `window` is an accepted target.
`worker` covers four environments, not three, since
`RTCIdentityProviderGlobalScope` answers to it.

**Done when** the regenerated tree carries an `@env` derived from `Exposed` on
every declaration that needs one, roughly 290 more than a four-name vocabulary
would produce, and `check_generated_tree` passes.

### 1.2 The target environment

Read each entrypoint's environments from `package.json`, both the `lib/` one and
each `bin/` one, and settle the field's spelling. Thread the result through to
the checker. F1, F9.

An entrypoint may state none, which claims every environment and reaches the
intersection of what they provide. That intersection is empty for a declaration
that differs by environment, so a no-target program cannot use `location` at
all. That is the intended answer.

**Done when** a fixture declares a window entrypoint and the checker can report
what it resolved.

### 1.3 The reference check

Run the check against the program's target, reporting a reference to a
declaration absent from it. An error from this release, not a warning. It covers
a value reference in any position, so matching `x is OffscreenCanvas` against an
environment without it is an error rather than a guard that never fires. F3.

Keep the tier check, which answers a different question until #1614 retires it.

**Done when** a new fixture referencing a worker-only declaration from a window
program fails with the full diagnostic, and the committed tree still converts
clean.

### 1.4 Tooling

The language server resolves the target from the same `package.json` field and
filters completion by it. F9.

**Done when** completion in a window entrypoint offers `document` and completion
in an entrypoint declared for a worker does not.

### Not this phase

No repartitioning, so a declaration may still sit in a package the wrong
environments can import. No per-environment declarations: one environment cannot
contradict itself. No unsatisfiable-import check, since every package a window
program reaches is reachable from a window.

## Phase two: workers

### 2.1 Ingest the worker surface

The four `*GlobalScope` classes, `importScripts`, `WorkerLocation`,
`WorkerNavigator`, the service-worker events and `Clients` are not in the tree.
Partition them into a worker package and regenerate. The name wants deciding:
the closed stack produced `web/worker.worker.esc`, which read badly because of a
file-name suffix that no longer exists.

Their globals then hoist by the other stream's mechanism, carrying the `@env`
F6 derives from the declaring scope.

**Done when** a worker fixture calls `importScripts` and reads `location` as a
`WorkerLocation`.

### 2.2 Repartition so a package is importable as a unit

F4, N2, and the substance of #1644.

1. Add `web:canvas` for the worker-reachable canvas surface:
   `OffscreenCanvas`, `OffscreenCanvasRenderingContext2D`,
   `ImageBitmapRenderingContext`, `ImageBitmap`, `ImageData`, `Path2D`,
   `CanvasGradient`, `CanvasPattern`, `TextMetrics` and the 2D-context mixins.
2. Move `MessagePort`, `StructuredSerializeOptions` and `Transferable` into
   `web:core`.
3. Move the nine transferable arms no worker can import, per
   `transferable.md`. The six that are `Exposed=(DedicatedWorker, Window)` need a
   package importable from a page and a dedicated worker, which no current
   package is.
4. Move the portable globals the other stream's hoisting left in `web:dom`.
   `setTimeout`, `atob`, `structuredClone`, `queueMicrotask` and the rest exist
   in every environment and sit in a browser-tier package, so a worker cannot
   reach them. The other stream's Stage 4 owns the move; this phase is what
   makes it necessary.

**Done when** a fixture compiles the worker program in section 4.2 of
`requirements.md`, receiving an `OffscreenCanvas` over `postMessage` and drawing
on it, without importing `web:dom`.

### 2.3 Per-environment declarations

F5, and the end of #1613.

Open question 1 in `requirements.md` has the three mechanisms and which one
needs new machinery.

Whatever name index §1.1 builds must not intersect over a duplicate name. The
closed stack's `EnvIndex` did, which is right for the interface and `declare
var` pair that share a name and wrong for disjoint per-environment copies, where
it yields the empty set. That index does not exist on `main`, so this is a
constraint on §1.1's design rather than a change to existing code.

Split `Transferable` by environment, sixteen arms for a page or a dedicated
worker and ten for the rest.

**Done when** a service worker sees `source` as
`Client | ServiceWorker | MessagePort | null`, a page sees
`WindowProxy | MessagePort | ServiceWorker | null`, and the retypes leave
`internal/interop/overlay/web/core.replace.esc`.

### 2.4 The rest

Turn on the unsatisfiable-import check, which now has something to catch: a page
importing the worker package, or a dependency whose entrypoint does not cover a
worker. F2.

### 2.5 Retire the tier machinery

N1 and #1614. Once `@env` records where a declaration exists, the tier rank is a
second mechanism answering an overlapping question, and N1 says one fact lives in
one place.

Retiring it is not a rename. A tier answers whether a package exists off the
browser at all, which `@env`'s nine browser scopes do not answer, so the WinterTC
question needs a home before `tier.go` can go. The options are an environment for
each non-browser runtime, which the non-goals currently exclude, or a separate
per-package claim that keeps the portability axis and drops the ordering.

Phase two is where this becomes pressing, because `CheckTiers` and the reference
check would both be live and would disagree: a tier forbids a portable package
naming a browser one, where `@env` would permit it if both declarations exist in
the importer's environments.

**Done when** either `tier.go` is gone and the WinterTC question has a stated
home, or the two mechanisms have a documented division and #1614 says which.

## Phase three: worklets

F8, and F1 through F5 applied unchanged.

1. Accept the four worklet names as targets.
2. Add packages for the worklet surface. The CSS Typed OM is the bulk, 36
   interfaces that `LayoutWorklet` and `PaintWorklet` share with `Window` and
   `Worker`, sitting in `web:dom` today where no worklet can reach them. The
   remainder is each worklet's own machinery: `AudioWorkletProcessor`,
   `PaintRenderingContext2D`, `LayoutFragment` and `IntrinsicSizes`.
3. Ingest and hoist the worklet scope classes.

**Done when** a fixture declares a paint-worklet entrypoint, names
`CSSUnitValue` and `PaintRenderingContext2D`, and is refused `document`.

## Testing

New fixtures throughout, not migrated ones. N5. The rules land in
`internal/solver`, and the suite exercising `internal/checker` is not being
backported.

## Measurement

N4, and the figures have inverted since this was written.

The cross-run checker cache in `internal/solver/stdlib_run_cache.go` keeps one
loaded checker per stdlib tree and forks it per run, so warm cost has collapsed:

| | warm | cold |
| --- | --- | --- |
| no imports | 0.45ms | 954ms |
| `web:fetch` alone | 0.36ms | 980ms |
| every package | 0.67ms | 4.6s |

Warm was the constraint on repartitioning at 380ms for every package. At 0.67ms
it is not a constraint at all, so **closure size no longer costs inference** and
the argument against an edge into `web:dom` has to be made on cold load alone.

Cold barely moves with closure size either: the `NoImports` case costs 954ms
against `OnePackage`'s 980ms, because `buildPackageGraph` parses every file in
the tree to read its import header whether or not the program reaches it. So
cold is a property of the tree's size, not of what a program imports, and #1643
is the lever on it.

Two consequences for this plan. §2.2 no longer waits on #1861, which is closed
as not planned: it measured the warm figure the cache removed, and its bisect
hashes are outside `main`'s rewritten history. And #1862 is still live and
unfixed, so a per-environment file landing in phase two would silently shrink
what the benchmark measures.
