# Implementation plan: Improving the web API type definitions

`requirements.md` in this folder states the problems and the requirements F1
through F8 and N1 through N5 referenced here.

Stages land on their own and leave the tree green. Each names what it leaves
alone.

`planning/web_environments/` is the other stream. Stage 1 here is its
prerequisite; nothing else in either folder is ordered against the other.

## What main holds today

`internal/dts_to_esc` converts from the pinned `lib.*.d.ts` set, the ECMA-262
derived facts, and the overlay. WebIDL is not an input.

The `web/` directory holds 24 packages. `web:core` is 24 declarations and binds
unprefixed through `coreURI`. `Window` and `WindowOrWorkerGlobalScope` are in
`web/dom.esc` as ordinary declarations whose members nothing can reach. The
worker scope classes are not in the tree.

#1648 landed, so an interface with several supertypes converts to a class that
`implements` the rest.

## Stage 1: WebIDL as an input

**Goal.** F1 and N1, and the prerequisite the environment stream waits on.

1. Add `@webref/idl` as a devDependency.
2. Write a generator reading `[Exposed]` per interface, `[Global=...]` per
   global scope, `[Transferable]`, and each interface's `constructor()`
   operations.
3. Emit a Go table and check it in, so conversion stays hermetic and offline.
   The hand-written tables in `internal/dts_to_esc` are the shape to follow.
4. Add a test asserting the table against the pinned `@webref/idl` version.

**Done when** the table is checked in, the test passes, and `go test ./...`
needs no network.

**Not this stage.** Nothing consumes the table yet.

## Stage 2: Stop declaring constructors that throw

**Goal.** F2, and #1645.

1. In `constructorSide` in `internal/dts_to_esc/dts_to_esc.go`, drop the
   construct signatures for an interface the table says has no
   `constructor()`.
2. Keep the rest of the trio fusion unchanged.
3. Check what the overlay was correcting here and remove the entries the table
   now answers.

**Done when** `new ImageBitmap()` and `new MessagePort()` are rejected,
`x is ImageBitmap` still compiles and still narrows, and
`check_generated_tree` passes. The regenerated tree will show a large diff.

**Not this stage.** The real parameter lists. WebIDL carries them for the
constructible cases and replacing TypeScript's is separate.

## Stage 3: Derive the sets

**Goal.** F3.

Generate `Transferable` from `[Transferable]`. The union gains six members:
`MIDIAccess`, `MediaStreamTrack`, `MediaStreamTrackHandle`,
`WebTransportDatagramsWritable`, `WebTransportReceiveStream` and
`WebTransportSendStream`. Four of those are not in the tree at all and have to
be ingested first.

**Done when** the union is generated and `planning/web_environments/transferable.md`'s
sixteen-row table matches what the tree declares.

**Not this stage.** Splitting `Transferable` by environment, which is the other
stream's.

## Stage 4: Hoist the globals

**Goal.** F4, F5, F6, F7, N2, N3.

`hoisted/` in this folder has the analysis: 239 declarations from 227 globals,
which package each lands in, and the merged `Navigator`. It was generated from
the closed stack's tree, so the counts want re-deriving against `main`, and the
scripts are checked in to do that.

1. Hoist `Window` and its mixins. 176 of the globals are window-only.
2. Put `addEventListener` and `removeEventListener` in `web:core`, and settle
   where the event maps live.
3. Keep the scope classes, which the handler signatures still annotate `this`
   with.
4. Resolve `self` and the `this` of a hoisted listener, both open in
   `requirements.md`.

**Done when** a fixture calls `alert`, `setTimeout` and
`document.querySelector` with no scope object named.

**Not this stage.** The worker and worklet scope classes, which are not in the
tree. Their globals hoist by the same mechanism once the other stream ingests
them.

## Stage 5: `web:core` becomes ambient

**Goal.** F8, N4.

Inject `web:core` the way `std:prelude` is injected, keep `import "web:core"`
legal as a no-op, and retire `coreURI`.

**Done when** a fixture writes `setTimeout(...)` and `Event` with no import, a
test pins the ambient name set, and `docs/03_imports.md` says where the
shape-loading line now falls.

## Stage 6: The rest of the augmentation, if measured

Open question 1 in `requirements.md` lists what else WebIDL carries that the
`.d.ts` flattens: nullability, integer width and range, `sequence<T>` against
array, `USVString` against `string`, `optional` with a default, `[SameObject]`
and `[NewObject]`.

None is measured. Measure one before planning it, the way #1645 and the
`Transferable` set were measured, because the argument for each is how often the
`.d.ts` is wrong rather than how much WebIDL says.

## Measurement

`BenchmarkStdlibClosureLoad` is the harness, and two issues are open against it.
#1862 says it can silently measure a smaller closure than it claims, and #1861
says warm whole-tree inference regressed 1.5x. Resolve both before trusting a
figure from it. #1643 moves the cold baseline when it lands.

## Testing

New fixtures rather than migrated ones. The rules land in `internal/solver` and
the suite exercising `internal/checker` is not being backported.
