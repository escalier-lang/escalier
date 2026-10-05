# Implementation plan: Web environments and package organization

`requirements.md` states the problems, the functional requirements F1 through
F14, and the non-functional requirements N1 through N8 referenced here.

The work is organized into the three phases that document sets out, one
environment family at a time. A stage lands on its own and leaves the tree
green. Each one names what it deliberately leaves alone, because several of
these problems are tempting to fix together and doing so makes a diff nobody
can review.

## What main holds today

The PRs that built the first environment system, #1628 through #1635, were
closed without merging. So this is a greenfield start rather than a refinement,
and two of the plan's earlier assumptions no longer apply.

Gone from `main`: the `@env` decorator and every annotation in the tree, the
`internal/dts_to_esc/env*.go` files, `CheckEnvs`, `DeclEnvs`, the environment
vocabulary, the file-name suffix convention that made `web/dom.window.esc`, and
the `web:worker` package. The `web/` directory holds 24 packages, all named
without a suffix.

Still in place, and load-bearing for what follows:

- `coreURI` in `internal/dts_to_esc/import_header.go:33` and
  `internal/solver/imports.go:94`, which is F14's premise.
- `web:core` at 24 declarations, which is F14's size argument.
- The `*GlobalScope` classes, now in `web/core.esc` and `web/dom.esc`, which no
  program can reach into. That is F6's premise.

Two things improved while the stack sat closed. #1648 landed, so an interface
with several supertypes converts to a class that `implements` the rest, and
`Element` reads `extends Node implements ARIAMixin, Animatable, ChildNode,
NonDocumentTypeChildNode, ParentNode, Slottable`. The phase-one prerequisite is
met. And there is no file-name convention left to unwind, so the question of
whether the suffix survives is moot and N1 is satisfied by the current tree
rather than by a change to it.

## Prerequisites

**Derive exposure from WebIDL.** Every environment claim in every phase comes
from here, so nothing else can start. N3.

1. Add `@webref/idl` as a devDependency. It is reachable from the build
   environment; `planning/web_environments/transferable.md` was produced from it.
2. Write a generator reading `[Exposed]` per interface and `[Global=...]` per
   global scope, and emit a Go table that is checked in so conversion stays
   hermetic and offline.
3. Also emit the `[Transferable]` set and each interface's `constructor()`
   operations. The first feeds F5's `Transferable` split, the second settles
   #1645, and both come free once the generator exists.

**Done when** the table is checked in and a test asserts it against the pinned
`@webref/idl` version.

## Phase one: window

A window program compiles correctly, and the mechanisms the later phases extend
all exist.

### 1.1 The vocabulary and the annotations

Build `@env` from nothing, with all nine names from F10 and both groups, and
annotate the tree from the generated table. F10, N3, N1.

The vocabulary is complete in this stage even though only `window` is an
accepted target, because `@env("window")` is a claim about the other eight and
cannot be written without names for them. `worker` covers four environments, not
three, since `RTCIdentityProviderGlobalScope` answers to it.

**Done when** the regenerated tree carries an `@env` derived from `Exposed` on
every declaration that needs one, roughly 290 more than a four-name vocabulary
would produce, and `check_generated_tree` passes.

### 1.2 The target environment

Read each entrypoint's environments from `package.json`, both the `lib/` one and
each `bin/` one. Settle the field's spelling, which `requirements.md` leaves
open. Thread the result through to the checker, which has no notion of one.
F1, F12.

An entrypoint may state none, which claims every environment and reaches the
intersection of what they provide.

**Done when** a fixture declares a window entrypoint and the checker can report
what it resolved.

### 1.3 The reference check

Run the environment check against a user program's target, reporting a
reference to a declaration absent from it. An error from this release, not a
warning. It covers a value reference in any position, so matching
`x is OffscreenCanvas` against an environment without it is an error rather than
a guard that never fires. F3.

Keep the converter's own call, which validates the tree against itself and
answers a different question.

**Done when** a new fixture referencing a worker-only declaration from a window
program fails with the full diagnostic, and the committed tree still converts
clean.

### 1.4 Hoist `Window`'s globals

Turn each member of `Window` and its mixins into a top-level declaration in the
package owning its family, carrying `@js` with the global's own name. F6, F7,
F8, F9, F13, N5, N6.

`planning/web_environments/hoisted/` has the analysis: which package each global
lands in, what `@env` it carries, and the 176 that are window-only.
`addEventListener` and `removeEventListener` go to `web:core` beside the event
model, which puts the event maps in `web:core`'s reach, so either they move too
or `web:core` imports the packages holding them.

**Done when** a window fixture calls `alert`, `setTimeout` and
`document.querySelector` with no scope object named, and the `*GlobalScope`
classes survive as the types the handler signatures still annotate `this` with.

### 1.5 `web:core` becomes ambient

Inject `web:core` the way `std:prelude` is injected, keeping
`import "web:core"` legal as a redundant no-op. Retire `coreURI`. F14, N8.

Unconditional for now: every environment in the vocabulary is a web platform, so
the condition the requirement describes cannot yet discriminate.

**Done when** a fixture writes `setTimeout(...)` and `Event` with no import, a
test pins the ambient name set, and `docs/03_imports.md` says where the
shape-loading line now falls.

### 1.6 Tooling

The language server resolves the target the same way the compiler does and
filters completion by it. F12.

**Done when** completion in a window entrypoint offers `document` and completion
in an entrypoint declared for a worker does not.

### Not this phase

No repartitioning, so a declaration may still sit in a package the wrong
environments can import. No per-environment declarations: one environment cannot
contradict itself, so F5 has nothing to do. No unsatisfiable-import check, since
every package a window program reaches is reachable from a window.

## Phase two: workers

### 2.1 Repartition so a package is importable as a unit

F4, N2, and the substance of #1644.

1. Add `web:canvas` for the worker-reachable canvas surface:
   `OffscreenCanvas`, `OffscreenCanvasRenderingContext2D`,
   `ImageBitmapRenderingContext`, `ImageBitmap`, `ImageData`, `Path2D`,
   `CanvasGradient`, `CanvasPattern`, `TextMetrics`, `ImageEncodeOptions` and the
   2D-context mixins.
2. Move `MessagePort`, `MessagePortEventMap`, `MessageEventTargetEventMap`,
   `StructuredSerializeOptions` and `Transferable` into `web:core`.
3. Move the nine transferable arms no worker can import, per
   `transferable.md`. The six that are `Exposed=(DedicatedWorker, Window)` need a
   package importable from a page and a dedicated worker, which no current
   package is.
4. Re-add a worker package for the worker-only surface. It is gone from `main`
   with the rest of the stack, and the name wants deciding: the earlier attempt
   produced `web/worker.worker.esc`, which read badly, and the suffix no longer
   exists to make it read that way.

**Done when** a fixture compiles the worker program in §3.2 of
`requirements.md`, receiving an `OffscreenCanvas` over `postMessage` and drawing
on it, without importing `web:dom`.

### 2.2 Per-environment declarations

F5, and the end of #1613.

Thirteen names differ by environment, listed in `hoisted/README.md`. Three
mechanisms cover different cases and all three are needed:

1. **Merge, where one form is a subset.** `WorkerNavigator` is a strict subset of
   `Navigator`, so one declaration carries `@env("window")` on the mixins and
   members only a page has.
2. **Split the accessor, where mutability differs.** `Location.hash` is writable
   in a page and read-only in a worker. A getter with no decorator plus a setter
   carrying `@env("window")` says that with no new mechanism, since an accessor
   pair is already the one legal case of two members under one name.
3. **Duplicate the member or the declaration, where the type differs.**
   `onmessage` is a `MessageEvent` handler in a page and on a dedicated worker
   and an `ExtendableMessageEvent` handler on a service worker. This is the one
   that needs new machinery: the slot key in
   `internal/dts_to_esc/decl_key.go` has to carry environments, overlapping
   environments under one name has to be an error, and a per-environment
   duplicate has to be distinguishable from an overload set.

Replace `EnvIndex`'s intersection over a duplicate name. Intersection is right
for the interface and `declare var` pair and wrong for disjoint per-environment
copies, where it yields the empty set.

Also split `Transferable` by environment and derive it from `[Transferable]`
rather than from TypeScript's union, which names ten of the sixteen.

**Done when** a service worker sees `source` as
`Client | ServiceWorker | MessagePort | null`, a page sees
`WindowProxy | MessagePort | ServiceWorker | null`, and the retypes leave
`internal/interop/overlay/web/core.replace.esc`.

### 2.3 The rest

Hoist the worker scope classes' globals, which §1.4's mechanism already covers.
Turn on the unsatisfiable-import check, which now has something to catch: a page
importing the worker package, or an npm dependency whose entrypoint does not
cover a worker. F2. Extend the prelude to carry `web:core`'s per-environment
content, which is where N4's cache keying lands.

## Phase three: worklets

F11, and F1 through F5 applied unchanged.

1. Accept the four worklet names as targets.
2. Add packages for the worklet surface. The CSS Typed OM is the bulk, 36
   interfaces that `LayoutWorklet` and `PaintWorklet` share with `Window` and
   `Worker`, sitting in `web:dom` today where no worklet can reach them. The
   remainder is each worklet's own machinery: `AudioWorkletProcessor`,
   `PaintRenderingContext2D`, `LayoutFragment`, `IntrinsicSizes`.
3. Hoist the worklet scope classes' globals.

**Done when** a fixture declares a paint-worklet entrypoint, names
`CSSUnitValue` and `PaintRenderingContext2D`, and is refused `document`.

## Testing

New fixtures throughout, not migrated ones. N7. The rules land in
`internal/solver`, and the suite exercising `internal/checker` is not being
backported, so each phase adds fixtures written against the new imports and
leaves the existing ones running unchanged against the old path.

## Measurement

Re-measure before §2.1 and after. N4.

#1631 recorded 268ms warm and 4.4s cold for `web:dom` entering `web:fetch`'s
closure. Both exceed what loading the entire tree cost at the time, 46ms warm
and 951ms cold, so the figure is unreliable and it is the evidence the current
partition rests on. Either reproduce it or retire it.

`BenchmarkStdlibClosureLoad` in `internal/solver/stdlib_load_bench_test.go` is
the harness. #1643 moves the cold baseline once it lands, so record which side
of it each measurement was taken on.
