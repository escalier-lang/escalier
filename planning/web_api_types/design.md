# Design: Improving the web API type definitions

## 1. Scope

This is one of two streams. It covers what a declaration in
`internal/interop/data/web/` **says**: its members, their signatures, which
package holds it, and whether a global is reachable at all.

`planning/web_environments/` covers **where** a declaration exists, which
global scopes have it and what the compiler does about that.

Two parts of this stream are ordered against that one. Section 5.1 emits the
WebIDL table its phase one waits on, and section 5.4 moves the portable globals
out of `web:dom`, which its phase two needs because a worker cannot import
`web:dom`. Nothing else in either stream is ordered against the other.

Anything here about environments is a dependency note, not a requirement; the
other folder owns it.

## 2. Problem statement

The tree is converted from TypeScript's `lib.dom.d.ts` and
`lib.webworker.d.ts`, which are themselves generated from WebIDL by
TSJS-lib-generator. So the tree is a derivative of a derivative, and the
intermediate step loses information and invents some.

Two cases found so far, both measured:

**It invents constructors.** 628 of the 635 `declare var` blocks in
`lib.dom.d.ts` carry a construct signature, 452 of them a bare `new()`, on
interfaces the platform does not let you construct. `new ImageBitmap()`,
`new MessagePort()`, `new Navigator()` and `new Element()` all typecheck against
the tree and throw `TypeError: Illegal constructor` at runtime. #1645.

**It loses a set.** There is no `Transferable` union in the platform. WebIDL
marks `[Transferable]` on each interface and sixteen carry it; TypeScript's
convenience union names ten, omitting `MIDIAccess`, `MediaStreamTrack`,
`MediaStreamTrackHandle` and the three WebTransport streams. The tree inherits
the omission. `planning/web_environments/transferable.md` has the table.

A third is fixed and worth recording as the pattern: an interface with several
supertypes used to convert to a class with one, so `Element` kept `Node` and
lost `querySelector`. #1648 fixed it with `implements`.

Separately, and not a WebIDL question: the `*GlobalScope` classes describe what
each runtime puts in scope, and a program cannot reach into them, so a page
cannot call `alert` or set `onclick`.

## 3. Current state

Reviewed against `main` at 4ae93f0b. Three of this stream's requirements have
landed since the first draft, and one landed in a shape the plan did not ask
for.

### 3.1 One conversion input

`internal/dts_to_esc` reads the pinned `lib.*.d.ts` set, the ECMA-262 derived
facts, and the overlay under `internal/interop/overlay/`. WebIDL is not an
input, and `@webref/idl` is not a dependency. The overlay is where a wrong or
unrepresentable declaration gets corrected by hand, which makes it the current
answer to everything in section 2.

So F1, F2, F3 and N1 stand. `ImageBitmap` still carries a constructor and
`Transferable` still has 11 arms.

### 3.2 The globals are hoisted, and placed wrongly

`web/dom.esc` holds 294 top-level `fn`, `val` and `var` declarations, among
them `alert`, `setTimeout`, `addEventListener`, `atob`, `structuredClone`,
`queueMicrotask`, `caches`, `indexedDB` and `localStorage`. No global is
declared in two packages. `Window` and `WindowOrWorkerGlobalScope` survive as
types.

So F4 and F5 are substantially met for `Window`, F6 is honoured and N3 holds.

The placement is the problem. Only `crypto`, `performance` and `fetch` went to
their family packages; everything else defaulted to `web:dom`. So `setTimeout`,
`atob` and `structuredClone`, which `hoisted/` routed to `web:core`, sit in
`web:dom`. That package is not ambient, so a program writes
`import "web:dom"` to set a timer, and it is browser-tier, so a worker cannot
import it at all. F8 exists because of this.

### 3.3 `web:core` binds unprefixed twice over

Two mechanisms now bind `web:core`'s exports without a qualifier, and they
disagree about shadowing.

`coreURI` at `internal/dts_to_esc/import_header.go:33` and
`internal/solver/imports.go:164` is the older one. Its comment says it "still
takes an import, which is the whole difference between it and the prelude", and
`bindCoreExports` binds core's exports into the file scope. That scope is a
child of the module scope, so a module's own declaration wins and a collision is
reported.

`internal/solver/ambient.go` is the newer one. It binds an ambient scope between
the prelude scope and a module's own, over the 25 packages `ambientPackages`
lists, and `web:core` and `web:fetch` are on it. Importing one still binds it as
a namespace. It binds from a parent layer, so a module declaration shadows
silently.

So F7 is met, and by a mechanism that goes further than F7 proposed by including
`web:fetch`, which its own boundary rule allows since `web:fetch` is also small
and portable. Nothing asserts what `ambientPackages` binds, so N4 stands, and
section 5.5 is where the two shadowing rules get settled.

### 3.4 What else moved

#1648 landed, so an interface with several supertypes converts to a class that
`implements` the rest. `constructorSide` has moved to
`internal/dts_to_esc/dts_to_esc.go:470`.

## 4. Requirements

### 4.1 Functional

**F1. WebIDL is an input to conversion.** A generated, checked-in table carries
the facts `[Exposed]`, `[Global=...]`, `[Transferable]` and each interface's
`constructor()` operations, and the converter reads it. Where WebIDL and the
`.d.ts` disagree on one of those, WebIDL wins.

**F2. A declaration carries a constructor only where the platform has one.** An
interface with no `constructor()` operation in WebIDL emits no constructor,
however the `.d.ts` declares it. #1645.

Removing the constructor must leave the class usable as the right operand of
`instanceof` and as a pattern-match discriminator. `x is ImageBitmap` stays
valid and correct, because `instanceof` reads the constructor's `prototype` and
never invokes `[[Construct]]`.

**F3. A derived set is derived.** `Transferable` is generated from
`[Transferable]` rather than copied from TypeScript's union. The same holds for
any other set WebIDL states and the `.d.ts` restates.

**F4. A global is reached through a package binding.** Every member of a
`*GlobalScope` class is a top-level declaration in the package owning its
family, and no program is handed a scope object to read members off.

**F5. Each member kind hoists to its matching top-level form.** A method becomes
a `declare fn`, a read-only property a `declare val`, a settable event-handler
property a `declare var`, and an overload set one `declare fn` per signature.
The hoisted declaration carries `@js` with the global's own name.

`addEventListener` and `removeEventListener` are the case this cannot settle on
its own. Both are generic over their scope's own event map, so every scope
hoists a different declaration under one name. The window copy is in `web:dom`
today and the other eight would collide with it, so expressing them needs
per-environment declarations, which is `web_environments`' F5. Where they sit
then follows F8 rather than a rule of their own.

A portable copy is not the way out. The window overload names `WindowEventMap`
and `this: Window`, and each of those reaches 1193 of the tree's 2132
declarations, every `web:` package included, because `UIEvent.view` is a
`Window`. Moving the untyped overload alone costs nothing and buys nothing.
`EventTarget.addEventListener` already carries that exact signature in
`web:core`. A second copy would make `addEventListener("click", ...)` resolve to
the untyped arm and yield an `Event` where the typed arm yields a `MouseEvent`.

**F6. A member a family package already declares is not hoisted twice.** `fetch`
is a member of `WindowOrWorkerGlobalScope` and a top-level declaration of
`web:fetch`. The hoist drops the member rather than adding a second binding for
one global.

**F7. `web:core` is ambient rather than imported.** Its declarations are injected
into scope the way `std:prelude`'s are, so a program writes `setTimeout(...)` and
`Event` without naming a package. `import "web:core"` stays legal as a redundant
no-op, which is how the generated tree and existing code migrate without a flag
day.

*Met.* `internal/solver/ambient.go` binds an ambient scope between the prelude
scope and a module's own, over the 25 packages `ambientPackages` lists, and
`web:core` and `web:fetch` are on it. Importing one still binds it as a
namespace. N4 is the part still outstanding.

This removes a special case rather than adding one. `coreURI` exists in two
places to make `web:core` bind unprefixed, and the comment beside it already
calls `web:core` and `std:prelude` the two packages that do. The exception goes.

It also decides F4's ergonomics. F4 moves a global out of a scope class and into
a package, which on its own means a file that sets a timer has to import
`web:core` first, worse than the global it replaces.

**The boundary needs stating, because the obvious justification proves too
much.** `Document` and `Element` are as ambient at runtime as `Event` is, so
"ambient on the platform" would pull `web:dom` in too. What earns `web:core` the
prelude is that it is small and portable: 24 declarations against
`std:prelude`'s 44. A package that is large stays an import however ambient the
runtime makes it.

**F8. A hoisted global goes to the package that owns it, not to `web:dom`.** A
global's package is the family that owns its type, and where the signature names
no browser type the family is `web:core`. `setTimeout` and `atob` belong there
and not in a browser-tier package a worker cannot import.

This is stated because the hoisting landed without it. 294 globals went to
`web:dom`, including the portable ones, so a program imports the whole DOM to
set a timer and a worker cannot set one at all. `hoisted/` has the per-global
assignment; this requirement is what makes it binding.

### 4.2 Non-functional

**N1. The generated table is pinned and hermetic.** `@webref/idl` is a
devDependency, the table it produces is checked in, and a test asserts the table
against the pinned version. Conversion stays offline.

**N2. A hoisted global compiles to a bare reference.** Codegen emits
`importScripts(...)`, never a member access on a scope object, since no such
object is in scope.

**N3. The scope classes survive as types.** Event-handler signatures annotate
`this` with the scope class and event maps are keyed by it, so hoisting the
members does not make the classes removable.

**N4. The ambient name set is pinned.** F7 makes `web:core`'s membership decide
what every file has in scope, where today it decides which package holds a
declaration. A test asserts the set of ambient names, so adding one is a
deliberate edit rather than a side effect of moving a declaration.

**N5. Nothing regresses cold load.** Measured against
`BenchmarkStdlibClosureLoad`. Cold is the only figure that constrains this work:
the cross-run checker cache has taken whole-tree warm inference to 0.67ms, while
cold is 4.6s and barely moves with closure size. #1862 is open against the
benchmark and unfixed. #1861 is closed as not planned, since it measured the warm
cost the cache removed.

## 5. Plan

Stages land on their own and leave the tree green. Each names what it leaves
alone.

### 5.1 WebIDL as an input

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

### 5.2 Stop declaring constructors that throw

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

### 5.3 Derive the sets

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

### 5.4 Place the hoisted globals

**Goal.** F8, and the part of F4 the landed hoisting left undone.

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
3. Resolve `self` and the `this` of a hoisted listener, both open in
   section 8.

**Done when** a fixture calls `setTimeout` and `atob` with no import at all, and
`alert` after importing `web:dom` alone.

**Not this stage.** The worker and worklet scope classes, which are not in the
tree. Their globals hoist by the same mechanism once the other stream ingests
them.

### 5.5 Pin what the ambient scope binds

**Goal.** N4. F7 itself has landed; this is the guard it was paired with.

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

### 5.6 The rest of the augmentation, if measured

open question 1 in section 8 lists what else WebIDL carries that the
`.d.ts` flattens: nullability, integer width and range, `sequence<T>` against
array, `USVString` against `string`, `optional` with a default, `[SameObject]`
and `[NewObject]`.

None is measured. Measure one before planning it, the way #1645 and the
`Transferable` set were measured, because the argument for each is how often the
`.d.ts` is wrong rather than how much WebIDL says.

## 6. Measurement

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

## 7. Testing

New fixtures rather than migrated ones. The rules land in `internal/solver` and
the suite exercising `internal/checker` is not being backported.

## 8. Open questions

1. **How far does the augmentation go?** F1 names four facts because four have
   been measured. WebIDL carries more that the `.d.ts` flattens: nullability
   markers, integer width and range through `unsigned long`, `[Clamp]` and
   `[EnforceRange]`, `sequence<T>` against array, `USVString` and `ByteString`
   against `string`, `optional` with a default, and `[SameObject]` and
   `[NewObject]`. Each is a separate piece of work with its own payoff, and none
   is measured yet.
2. **Does the overlay shrink?** The overlay exists partly to correct what the
   `.d.ts` gets wrong. Every fact moved to WebIDL is a reason for an overlay
   entry to go, and nobody has checked which.
3. **Does `Window` get hoisted whole?** F4 says every member, which for `Window`
   and its mixins is around 176 declarations. `hoisted/`
   has the analysis. Whether they all belong in `web:dom` or the browsing-context
   API earns a `web:window` is open.
4. **Does `self` survive?** `WorkerGlobalScope.self` is the global object, and
   exposing it returns the program the handle F4 withholds.
5. **What is the `this` of a hoisted listener?** `addEventListener` annotates its
   callback with `this: DedicatedWorkerGlobalScope`, which stays true after the
   hoist even though the receiver is no longer nameable.

## 9. Non-goals

- Replacing the `.d.ts` set as the conversion input. WebIDL supplements it with
  facts it does not carry or gets wrong. Generating the tree from WebIDL alone
  is a larger question and this does not answer it.
- Runtime feature detection in user code, such as narrowing on
  `typeof OffscreenCanvas !== "undefined"`.
- `typeof globalThis`. F4 withholds the global object, so the intersections
  naming it lose their second half and the annotation goes. Nothing replaces it.

## 10. Related issues

| issue | relation |
| --- | --- |
| #1645 | the tree declares constructors that throw, which is F2 |
| #1648 | an interface's supertypes beyond the first were dropped. Fixed with `implements`, and the pattern this stream generalizes |
| #1646 | a type guard on an alias to a class matches every value |
| #1643 | comment attachment dominates cold load |
| #1861 | warm whole-tree inference regressed 1.5x. Closed, overtaken by the cross-run cache |
| #1862 | the closure benchmark silently measures fewer packages than it claims |
| #1586 | the tier is per package where availability is per declaration |

