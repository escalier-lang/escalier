# Implementation plan: Improving the web API type definitions

`requirements.md` in this folder states the problems and the requirements F1
through F9 and N1 through N5 referenced here.

Stages land on their own and leave the tree green. Each names what it leaves
alone.

`planning/web_environments/` is the other stream, and two stages here are
ordered against it. Stage 1 is the prerequisite for that stream's phase one,
and Stage 4 is what its phase two needs, since a worker cannot import the
`web:dom` the portable globals currently sit in.

## What main holds today

Reviewed against `main` at 4ae93f0b. Two of this stream's requirements have
landed since it was written, and one landed in a shape the plan did not ask for.

**F8 is done.** `internal/solver/ambient.go` binds an ambient scope between the
prelude scope and a module's own, over the packages `ambientPackages` lists, and
that list holds `web:core`, `web:fetch` and 23 `std:*` packages. Its comment
states the no-op F8 asked for: "Importing one of them still works, and binds the
package as a namespace the way any import does." It goes further than F8
proposed by including `web:fetch`, which is consistent with F8's own boundary
rule since `web:fetch` is also small and portable.

**F4 and F5 are substantially done for `Window`, and F6 is honoured.**
`web/dom.esc` holds 294 top-level `fn`, `val` and `var` declarations, among them
`alert`, `setTimeout`, `addEventListener`, `atob`, `structuredClone`,
`queueMicrotask`, `caches`, `indexedDB` and `localStorage`. No global is
declared in two packages, which is F6. `Window` and `WindowOrWorkerGlobalScope`
survive as types, which is N3.

**The placement diverges from `hoisted/`, and it matters.** Only `crypto`,
`performance` and `fetch` went to their family packages; everything else
defaulted to `web:dom`. So `setTimeout`, `atob` and `structuredClone`, which the
analysis routed to `web:core`, sit in `web:dom`. `web:dom` is not ambient, so a
program has to write `import "web:dom"` to set a timer, which is exactly the
ergonomics problem F8 removes for the packages that are ambient. And `web:dom` is
browser-tier, so a worker cannot import it, which makes the other stream's F4
harder rather than easier.

What has not changed: WebIDL is not an input and `@webref/idl` is not a
dependency, so F1, F2, F3 and N1 stand. `ImageBitmap` still carries a
constructor. `Transferable` still has 11 arms. `addEventListener` is in
`web:dom` rather than `web:core`, so F7 stands. `ambientPackages` has no test
pinning its contents, so N4 stands.

#1648 landed, so an interface with several supertypes converts to a class that
`implements` the rest. `constructorSide` has moved to
`internal/dts_to_esc/dts_to_esc.go:470`.

## Stage 1: WebIDL as an input

**Goal.** F1 and N1, and the prerequisite the environment stream waits on.

1. Add `@webref/idl` as a devDependency.
2. Write a generator reading `[Exposed]` per interface, `[Global=...]` per
   global scope, `[Transferable]`, and each interface's `constructor()`
   operations.
3. Emit a Go table and check it in, so conversion stays hermetic and offline.
   `webTiers` in `internal/dts_to_esc/tier.go` is the shape to follow: a map
   literal in its own file with a comment saying what each entry claims.
4. Add a test asserting the table against the pinned `@webref/idl` version.

**Done when** the table is checked in, the test passes, and `go test ./...`
needs no network.

**Not this stage.** Nothing consumes the table yet.

## Stage 2: Stop declaring constructors that throw

**Goal.** F2, and #1645.

1. In `constructorSide` at `internal/dts_to_esc/dts_to_esc.go:470`, drop the
   construct signatures for an interface the table says has no
   `constructor()`.
2. Keep the rest of the trio fusion unchanged.
3. Check the overlay's constructor entries, but do not expect to remove them.
   `file.replace.esc` gives `FormData` a no-argument constructor because the
   browser-typed overload is not expressible in a portable package, which is a
   tier problem and not a `.d.ts` error, so the table does not answer it. #1586
   is where that one goes.

**Done when** `new ImageBitmap()` and `new MessagePort()` are rejected,
`x is ImageBitmap` still compiles and still narrows, and
`check_generated_tree` passes. The regenerated tree will show a large diff.

The narrowing half of that needs care, because #1646 reports that a guard on an
alias to a class matches every value. A guard on `ImageBitmap` names the class
directly and is unaffected, but anything this stage checks through an alias
would pass for the wrong reason.

**Not this stage.** The real parameter lists. WebIDL carries them for the
constructible cases and replacing TypeScript's is separate.

## Stage 3: Derive the sets

**Goal.** F3.

Generate `Transferable` from `[Transferable]`. The tree declares 11 arms, the 10
WebIDL interfaces plus `typed_arrays.ArrayBuffer`, where WebIDL marks 16, so the
union gains six: `MIDIAccess`, `MediaStreamTrack`, `MediaStreamTrackHandle`,
`WebTransportDatagramsWritable`, `WebTransportReceiveStream` and
`WebTransportSendStream`.

`MIDIAccess` and `MediaStreamTrack` are already in `web/dom.esc`. The other
four are not in the tree at all and have to be ingested first.

**Done when** the union is generated and `planning/web_environments/transferable.md`'s
sixteen-row table matches what the tree declares.

**Not this stage.** Splitting `Transferable` by environment, which is the other
stream's.

## Stage 4: Place the hoisted globals

**Goal.** F7, and the part of F4 the landed hoisting left undone.

`Window`'s globals are already hoisted, so what remains is where they sit.
`hoisted/` in this folder has the analysis: which package each global belongs to
and why. It was generated from the closed stack's tree, so re-derive the counts
against `main` first; the scripts are checked in to do that.

1. Move the portable globals out of `web:dom`. `setTimeout`, `clearTimeout`,
   `setInterval`, `clearInterval`, `atob`, `btoa`, `structuredClone`,
   `queueMicrotask`, `reportError`, `origin`, `isSecureContext` and
   `crossOriginIsolated` name no browser type and belong in `web:core`, which is
   ambient, so they stop needing an import at all.
2. Move the family globals to their families: `caches` to `web:cache`,
   `indexedDB` to `web:indexeddb`, `localStorage` and `sessionStorage` to
   `web:storage`. `crypto`, `performance` and `fetch` already went.
3. Put `addEventListener` and `removeEventListener` in `web:core`, and settle
   where the event maps live. F7.
4. Resolve `self` and the `this` of a hoisted listener, both open in
   `requirements.md`.

**Done when** a fixture calls `setTimeout` and `atob` with no import at all, and
`alert` after importing `web:dom` alone.

**Not this stage.** The worker and worklet scope classes, which are not in the
tree. Their globals hoist by the same mechanism once the other stream ingests
them.

## Stage 5: Pin what the ambient scope binds

**Goal.** N4. F8 itself has landed; this is the guard it was paired with.

`ambientPackages` in `internal/solver/ambient.go` now decides what every file
has in scope, and nothing asserts its contents. Adding a package to that list,
or a declaration to one of the packages on it, silently adds a global name
everywhere.

1. Add a test pinning the set of names the ambient scope binds.
2. Record in `docs/03_imports.md` where the shape-loading line now falls, since
   25 packages bind ambiently and the document predates that.
3. Decide whether the two unqualified-binding paths should agree. `coreURI` is
   not redundant: its own comment says it "still takes an import, which is the
   whole difference between it and the prelude", and `bindCoreExports` binds
   core's exports into the file scope, which is a child of the module scope, so
   a module's own declaration wins and a collision is reported. The ambient
   scope binds from a parent layer, where a module declaration shadows
   silently. One package therefore has two bindings with two shadowing rules,
   and whether that is deliberate wants settling before either moves.

**Done when** the name set is pinned and a deliberate addition has to update the
test.

## Stage 6: The rest of the augmentation, if measured

Open question 1 in `requirements.md` lists what else WebIDL carries that the
`.d.ts` flattens: nullability, integer width and range, `sequence<T>` against
array, `USVString` against `string`, `optional` with a default, `[SameObject]`
and `[NewObject]`.

None is measured. Measure one before planning it, the way #1645 and the
`Transferable` set were measured, because the argument for each is how often the
`.d.ts` is wrong rather than how much WebIDL says.

## Measurement

`BenchmarkStdlibClosureLoad` is the harness. Warm cost has collapsed since the
cross-run checker cache landed in `internal/solver/stdlib_run_cache.go`: every
package is 0.67ms warm against 380ms before it, so inference is no longer what
a reorganization has to watch. Cold is the whole story at 4.6s, and it barely
moves with closure size, because the new `NoImports` case costs 954ms against
`OnePackage`'s 980ms.

#1861 is closed as not planned. It measured the warm figure the cache removed,
and its bisect hashes are outside `main`'s rewritten history. #1862 is still
live and unfixed. #1643 moves the cold baseline when it lands, and cold is now
the only baseline that matters.

## Testing

New fixtures rather than migrated ones. The rules land in `internal/solver` and
the suite exercising `internal/checker` is not being backported.
