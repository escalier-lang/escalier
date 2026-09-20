# Implementation plan: Web environments and package organization

Stages are ordered so each one lands on its own and leaves the tree green. A
stage names what it does not do, because several of these problems are tempting
to fix together and doing so makes a diff nobody can review.

`requirements.md` in this folder states the problems, the functional
requirements F1 through F5, and the non-functional requirements N1 through N4
referenced here.

## Stage 0: Settle what a package file name means

**Goal.** Close open question 1 before any file moves, since every later stage
touches these names.

**Work.**

1. Pick one of two readings. Either the suffix states which environments may
   import the package, which is a real fact distinct from its declarations', or
   the suffix goes and importability is derived from the declarations the package
   holds.
2. Write the chosen reading into `docs/03_imports.md`, replacing the current
   text, which says a name "says where it runs" without distinguishing the two.
3. If the suffix survives, add a check that a package's suffix covers the union of
   its declarations' environments, so `dom.window.esc` holding 264
   every-environment declarations is either legal by the stated rule or reported.

**Done when.** `docs/03_imports.md` states the rule, and a converter check
enforces it or the rule explicitly needs none.

**Not this stage.** No file renames and no partition changes.

## Stage 1: Derive exposure from WebIDL

**Goal.** Satisfy N3 for the facts TypeScript's libs cannot carry: which worker
kinds expose an interface, and which interfaces are constructible.

**Work.**

1. Add `@webref/idl` as a devDependency.
2. Write a generator reading each interface's `[Exposed]` extended attribute and
   its `constructor()` operations.
3. Emit a Go table and check it in, so conversion stays hermetic and offline. The
   hand table at `internal/dts_to_esc/env_table.go:91-121` is the shape to follow.
4. Feed the exposure half into `declEnvsFrom` ahead of `DeclEnvsFromLibs`, since
   it is strictly more precise.
5. Retire the hand table entries the generated one now answers, and leave a
   comment on each survivor saying why WebIDL does not cover it.

**Done when.** `AudioData`, `VideoFrame`, `RTCDataChannel` and `MediaSourceHandle`
carry `@env("window", "dedicated_worker")` in the regenerated tree, and
`check_generated_tree` passes.

**Not this stage.** The constructor half is #1645 and can land separately from the
same generated table.

## Stage 2: Repartition so a package is importable as a unit

**Goal.** Satisfy N2 and F4. A worker can name `OffscreenCanvas` and everything
`getContext` returns.

**Work.**

1. Add `web:canvas` holding `OffscreenCanvas`,
   `OffscreenCanvasRenderingContext2D`, `ImageBitmapRenderingContext`,
   `ImageBitmap`, `ImageData`, `Path2D`, `CanvasGradient`, `CanvasPattern`,
   `TextMetrics`, `ImageEncodeOptions` and the 2D-context mixin interfaces.
2. Point `web:dom` at it for `HTMLCanvasElement.transferControlToOffscreen` and
   `getContext`, and reconsider `web:webgl`'s window-only suffix, since
   `lib.webworker.d.ts` declares both WebGL context interfaces.
3. Carry out #1644, moving `MessagePort`, `MessagePortEventMap`,
   `MessageEventTargetEventMap`, `StructuredSerializeOptions` and `Transferable`
   into `web:core`. Its recorded blocker does not hold, as section 7 of
   `requirements.md` explains.
4. Sweep the remaining 264 unannotated declarations in `dom.window.esc` and move
   the ones another package can hold.

**Done when.** A fixture compiles the worker program in section 3.2 of
`requirements.md` without importing `web:dom`, and
`BenchmarkStdlibClosureLoad` shows no warm regression beyond the 18ms the whole
tree already costs.

**Not this stage.** No enforcement. A worker importing `web:dom` still typechecks,
because nothing checks it yet.

## Stage 3: Give a program a target environment

**Goal.** Satisfy F1 and F2, and supply the input Stages 4 and 5 both need.

**Work.**

1. Add a `package.json` field naming the environment, accepting the same
   vocabulary `@env` takes.
2. When the field is absent, infer the environment as the intersection of the
   environments of the packages the program imports.
3. Report an empty intersection with both conflicting imports named. This is what
   makes importing `web:dom` and `web:worker` together an error, and it comes for
   free from inference rather than from a rule.
4. Thread the resolved environment through to the checker, which has no notion of
   one today.

**Done when.** A fixture importing `web:dom` and `web:worker` together fails with
a diagnostic naming both, and one importing only portable packages compiles with
the environment left unresolved.

**Not this stage.** No reference checking. The environment is computed and carried,
nothing consults it yet.

## Stage 4: Enforce environments on user code

**Goal.** Satisfy F3.

**Work.**

1. Lift the reference check out of the converter. `CheckEnvs` and
   `checkModuleEnvs` read `DeclEnvs` and a name index, both of which a compiled
   program also has.
2. Run it against the program's environment from Stage 3, reporting a reference to
   a declaration absent from it.
3. Keep the converter's own call, which validates the generated tree against
   itself and answers a different question.

**Done when.** A fixture referencing a window-only declaration from a worker
program fails with the full diagnostic, and the committed tree still converts with
no violations.

**Not this stage.** Nothing about type positions. A reference is a reference here,
which is stricter than the end state and is the safe direction.

## Stage 5: Represent a divergent declaration

**Goal.** Satisfy F5, so `MessageEvent.source` stops being retyped to `null`.

**Work.**

1. Resolve open question 3, per-environment declarations against per-arm
   annotations. The requirements document argues for the first and records what it
   costs.
2. If per-environment declarations win, decide how several files contribute to one
   package. `stdlibdir.PackageName` already maps `core.window.esc` to `core`, but
   `stdlibdir.PackageFile` currently errors when two suffixed files claim one
   package, so that rule inverts.
3. Replace `EnvIndex`'s intersection over a duplicate name. Intersection is right
   for the interface and `declare var` pair and wrong for disjoint per-environment
   copies, where it yields the empty set.
4. Remove the `source` and `ports` retypes from
   `internal/interop/overlay/web/core.replace.esc` and the comment explaining why
   they exist.

**Done when.** A service worker sees `source` as
`Client | ServiceWorker | MessagePort | null`, a page sees
`WindowProxy | MessagePort | ServiceWorker | null`, and #1613 closes.

## Stage 6: Type-only references, if still needed

**Goal.** Let portable code name a declaration that is genuinely absent from some
environment, so it can be narrowed away.

Reassess before starting. Stages 2 and 5 remove most of the demand, and the
remaining case is narrow: a union arm such as `WindowProxy` that no repartition can
make portable.

**Work.**

1. Split name resolution by position, allowing a type-position reference to a
   declaration outside the program's environment and rejecting a value-position
   one. `TypeRefNames` at `internal/dts_to_esc/references.go:150` does this for
   the generated tree; user code has no equivalent.
2. Decide whether a whole-package `import type` form is wanted as well, or whether
   per-reference position is enough. Per-reference needs no syntax.
3. Leave pattern matching alone. A match on an absent class compiles to a bare
   `instanceof` at `internal/codegen/builder.go:2831` and would throw
   `ReferenceError`. Guarding it with a `typeof` test is a separate change and is
   not required by any requirement here.

**Done when.** A portable declaration can name `WindowProxy` in a union, and
constructing one outside a page is reported.

## Measurement

Re-measure before Stage 2 and after. #1631 recorded 268ms warm and 4.4s cold for
`web:dom` entering `web:fetch`'s closure. Both exceed what loading the entire tree
costs today, 46ms warm and 951ms cold, so the figure is unreliable and it is the
evidence the current partition rests on. Either reproduce it or retire it.

`BenchmarkStdlibClosureLoad` in `internal/solver/stdlib_load_bench_test.go` is the
harness. #1643 will move the cold baseline once it lands, so record which side of
it each measurement was taken on.

## Sequencing

Stage 0 first, because every later stage renames or moves a file. Stage 1 next,
because Stage 2 needs the exposure data to place the `Transferable` arms. Stage 3
before Stage 4 and Stage 5, both of which read the program's environment. Stage 6
last and only if the demand survives.

Stages 1 and 2 are independent of Stages 3 through 5. The first pair corrects the
generated tree; the second gives user programs an environment. Either pair can go
first if one turns out to be blocked.
