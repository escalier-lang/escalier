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

Nothing here can start before it. An `@env("window")` is a claim about the
other eight environments, and `[Exposed]` is the only thing that knows whether
the claim is true.

## What main holds today

`internal/dts_to_esc/tier.go` gives each package one of four ranks ordered by
portability, and `CheckTiers` reports an import edge that goes up a rank. It has
one caller, the converter. The compiler pipeline has no notion of a tier, so a
program can import `web:dom` and `web:workers` together and nothing reports it.

There is no `@env`, no environment vocabulary, and no worker surface: none of
the four `*GlobalScope` classes is declared, and `importScripts`,
`WorkerLocation`, `FetchEvent` and `Clients` have zero occurrences under
`internal/interop/data/`.

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

**Done when** a fixture compiles the worker program in section 4.2 of
`requirements.md`, receiving an `OffscreenCanvas` over `postMessage` and drawing
on it, without importing `web:dom`.

### 2.3 Per-environment declarations

F5, and the end of #1613.

Open question 1 in `requirements.md` has the three mechanisms and which one
needs new machinery. Replace `EnvIndex`'s intersection over a duplicate name,
which is right for the interface and `declare var` pair and wrong for disjoint
per-environment copies.

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

N4. Two issues are open against the harness and both want resolving before any
figure from it is trusted: #1862, which says it can silently measure a smaller
closure than it claims, and #1861, which says warm whole-tree inference
regressed 1.5x and bisects it to one commit. Resolve #1861 before §2.2, because a
repartition measured against a regressed baseline looks cheaper than it is.

`main` costs 43ms warm and 810ms cold for `web:fetch` alone, and 380ms warm and
4.45s cold for every package. #1643 moves the cold baseline when it lands.
