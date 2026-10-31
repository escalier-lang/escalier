# Design: Improving the web API type definitions

## 1. Scope

This is one of two streams. It covers what a declaration in
`internal/interop/data/web/` **says**: its members, their signatures, which
package holds it, and whether a global is reachable at all.

`planning/web_environments/` covers **where** a declaration exists, which
global scopes have it and what the compiler does about that.

Two parts of this stream are ordered against that one. Section 5.2 emits the
WebIDL table its phase one waits on, and section 5.6 moves the portable globals
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
`web:dom`. A program therefore writes `import "web:dom"` to set a timer and
reaches the name as `dom.setTimeout`, and `web:dom` is browser-tier, so a worker
cannot import it at all. F8 exists because of this.

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
the prelude scope and a module's own, over the 26 packages `ambientPackages`
lists, `web:core` and `web:fetch` among them. It reproduces the JavaScript
global object rather than binding exports flat: a dotted `@js` path rebuilds its
namespace, so `@js("Math.clz32")` binds `clz32` into a `Math`. Importing a
package on the list still binds it as a namespace. The layer is a parent of the
module scope, so a module declaration shadows an ambient name silently.

So one package has two unqualified bindings with two shadowing rules, and 26
packages have the second one. F7 keeps `bindCoreExports` and takes the globals
scope away from a project's own files, because reported beats silent and 203
top-level names in scope without an import is the problem F4 is about. Nothing
asserts what `ambientPackages` binds either, so N4 stands. Section 5.1 does
both.

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

A generated set holds the arms the tree declares. WebIDL marks six transferable
interfaces the tree lacks, and four of those are outside `lib.dom.d.ts`
altogether, so the set gains two arms now and the rest when the conversion input
widens.

**F4. Only a receiverless member becomes a global.** A scope member that reads
as a function of no particular object, `setTimeout` and `atob` among them,
hoists to a top-level declaration in the package owning its family, reached
through that package's binding. A member that reads as an operation on the scope
keeps its receiver, and the scope object is what its own package's binding
stands for, so a program writes `window.alert(...)` after importing
`web:window`.

The binding and the object are one name rather than two. `import "web:window"`
binds `window`, and a separate value of that name inside the package would read
as `window.window`.

This is the general rule that a pseudo-package exporting one value is treated as
that value, and not something scope packages need of their own. `std:console` is
the same shape: it exports `Console` and a `console` value, and the useful
spelling is `console.log(...)` rather than `console.console.log(...)`. A
namespace's `AsValue` carries it, since a member the namespace does not hold is
read off that value, so `window.alert(...)` resolves through the `Window`
instance while `window.Window` resolves as the type. Section 5.5 states the rule
precisely and says why it tests the name rather than counting value exports.

Each scope package names its binding after its own scope, so the three are
`window`, `worker` and `worklet`.

The number of names a program gets without asking is the thing being held down.
`hoisted/` routes 239 declarations and counts 176 of them window-only. Hoisting
those would put the browsing-context API in scope unqualified for every file
that imports `web:dom`, where keeping them as `Window` members costs one import
and one receiver.

**F5. Each member kind hoists to its matching top-level form.** A method becomes
a `declare fn`, a read-only property a `declare val`, a settable event-handler
property a `declare var`, and an overload set one `declare fn` per signature.
The hoisted declaration carries `@js` with the global's own name.

`addEventListener` and `removeEventListener` do not hoist at all. They are
methods on every `EventTarget`, which is to say on `window`, on `document` and
on every element, and a program calls them on the thing it is listening to. F4
keeps a member that has a receiver, and this is the clearest case of one.

That also settles where they live, which the earlier plan to put them in
`web:core` could not. `EventTarget.addEventListener` is in `web:core` already
and `Window` restates the four typed overloads, so `window.addEventListener`
and `element.addEventListener` are both correctly typed today with nothing to
move. `hoisted/README.md` records what a hoisted copy would have cost.

**F6. A member a family package already declares is not hoisted twice.** `fetch`
is a member of `WindowOrWorkerGlobalScope` and a top-level declaration of
`web:fetch`. The hoist drops the member rather than adding a second binding for
one global.

**F7. A project's own file imports what it uses. A `.d.ts` does not have to.**
An `.esc` file reaches `std:prelude` without an import and nothing else, so
`std:math` gives `math.PI` and `std:error` gives `error.Error`. `web:core` keeps
the unprefixed binding `coreURI` and `bindCoreExports` give it, so
`import "web:core"` puts `setTimeout` and `Event` in scope under their own
names.

A converted `.d.ts` keeps the ambient globals. It is written against
TypeScript's global library and names `Console` or `JsonTag` with no import, and
it cannot be rewritten to add one, so the package it becomes is inferred with
those names in scope.

*The split already exists as an option.* `Options.NoAmbientNames` empties the
ambient scope for the project's own files while an npm package's scope stays a
child of the globals scope, which `package_load.go` says is deliberate: "that
option governs the names the project's own files reach."
`TestNodeModulesImportReadsGlobalsWithoutAmbientNames` asserts both halves, that
the package resolves `JsonTag` and the entry module does not. What F7 asks is
that this stop being an option and become the behavior.

What an `.esc` file gives up is 203 top-level names plus the `Math`, `Intl`,
`JSON`, `Reflect` and `Atomics` namespaces that `bindGlobals` reconstructs from
dotted `@js` paths. That scope is a faithful copy of the JavaScript global
object, which is the right thing for a `.d.ts` to be checked against and the
wrong thing for a language that has imports.

Two mechanisms bind a package unprefixed, and this keeps the older one.
`bindCoreExports` binds into the file scope, a child of the module scope, so a
module's own declaration wins and the collision is reported. The globals scope
binds from a parent layer, where a module declaration shadows silently.
Reported beats silent, and `web:core` is the one package small and portable
enough to earn it: 24 declarations, every one of which a portable program uses.

This costs F4 the ergonomics the ambient scope was giving it. A file that sets a
timer writes `import "web:core"` first. That is one line for the unprefixed
names of the one package that has them.

**F8. A hoisted global goes to the package that owns it, not to `web:dom`.** A
global's package is the family that owns its type, and where the signature names
no browser type the family is `web:core`. `setTimeout` and `atob` belong there
and not in a browser-tier package a worker cannot import.

The browsing-context API earns `web:window`. `Window` itself, the `window`
value, and the members that stay members under F4 go there.

The reason is the qualifier, not partitioning. `web:dom` and `web:window` cannot
be separated: `Window` names 29 other `web:dom` declarations and 139 `web:dom`
declarations name `Window` or `WindowProxy` back, `Document.defaultView` and
`UIEvent.view` among them. The cycle that creates is routine, since `web:dom`
already cycles with six packages, and `PackageGroups` maps every URI in a
closure to the same group, so the two load together whichever one a program
imports. What the split buys is that the browsing context is reached as
`window` rather than through `dom`.

This is stated because the hoisting landed without it. 294 globals went to
`web:dom`, including the portable ones, so a program imports the whole DOM to
set a timer and a worker cannot set one at all. `hoisted/` has the per-global
assignment; this requirement is what makes it binding, and its 176 window-only
routings are superseded by F4 rather than by a different package.

### 4.2 Non-functional

**N1. The generated table is pinned and hermetic.** `@webref/idl` is a
devDependency, the table it produces is checked in, and a test asserts the table
against the pinned version. Conversion stays offline.

**N2. A hoisted global compiles to a bare reference.** Codegen emits
`importScripts(...)` for a hoisted declaration, never a synthesized member
access. A member that kept its receiver compiles to the access the source
wrote.

**N3. The scope classes survive as types.** Event-handler signatures annotate
`this` with the scope class and event maps are keyed by it, so hoisting the
members does not make the classes removable.

**N4. What a program gets without an import is pinned.** A test asserts the set
of names in scope with no import, so adding one is a deliberate edit rather than
a side effect of moving a declaration. Under F7 that set is `std:prelude`'s
exports, and the test is what keeps a package from quietly rejoining it.

**N5. Nothing regresses cold load.** Measured against
`BenchmarkStdlibClosureLoad`. Cold is the only figure that constrains this work:
the cross-run checker cache has taken whole-tree warm inference to 0.67ms, while
cold is 4.6s and barely moves with closure size. #1862 is open against the
benchmark and unfixed. #1861 is closed as not planned, since it measured the warm
cost the cache removed.

## 5. Plan

Stages land on their own and leave the tree green. Each names what it leaves
alone.

A stage that regenerates the tree produces a large diff that needs no
line-by-line review. The hand-written part is the generator and the fixture, and
`check_generated_tree` is what validates the rest. §5.2, §5.3 and §5.7 are the
three where the generated half is big enough to say so.

### 5.1 Make the ambient split the behavior rather than an option

**Goal.** F7 and N4, before anything else. Every later stage moves declarations
between packages, and while the globals scope covers a project's own files,
moving one silently changes what every `.esc` file has in scope.

`Options.NoAmbientNames` already does what F7 wants, and
`TestNodeModulesImportReadsGlobalsWithoutAmbientNames` already asserts it. The
work is removing the choice.

1. Make the `NoAmbientNames` behavior unconditional for a project's own files
   and remove the option, so there is one answer rather than two.
2. Leave `package_load.go`'s parenting of an npm package's scope at the globals
   scope alone. That is the half of F7 that stays.
3. Keep `coreURI` and `bindCoreExports`, which bind `web:core`'s exports
   unprefixed on import and report a collision with a module's own declaration.
4. Add the test N4 asks for, over what an `.esc` file reaches with no import.
5. Record in `docs/03_imports.md` where the shape-loading line falls, which the
   document predates.

**Done when** a fixture naming `PI`, `console` or `Error` with no import fails,
the same fixture compiles with the imports, an npm package referencing `Console`
still resolves, `setTimeout` still resolves after `import "web:core"` alone, and
the no-import name set is pinned.

**Not this stage.** Any change to which package holds a declaration. This moves
nothing; it changes what a file reaches without asking.

### 5.2 WebIDL as an input

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

### 5.3 Stop declaring constructors that throw

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

### 5.4 Derive the sets

**Goal.** F3.

Generate `Transferable` from `[Transferable]`. The tree declares 11 arms, the 10
WebIDL interfaces plus `typed_arrays.ArrayBuffer`, where WebIDL marks 16.

Two of the six it lacks are declared already and join the union here:
`MIDIAccess` and `MediaStreamTrack`, both in `web/dom.esc`.
`MediaStreamTrackHandle`, `WebTransportDatagramsWritable`,
`WebTransportReceiveStream` and `WebTransportSendStream` are in no pinned lib,
so they wait on the wider conversion input rather than being ingested here.

**Done when** the union carries 13 arms, the 12 WebIDL interfaces the pinned
libs declare plus `typed_arrays.ArrayBuffer`, and
`planning/web_environments/transferable.md` records which four of its sixteen
rows are still unreachable and why.

**Not this stage.** Splitting `Transferable` by environment, which is the other
stream's.

### 5.5 A pseudo-package that is one object binds as that object

**Goal.** The mechanism F4 needs, landed before anything depends on it.

A namespace's `AsValue` already does the work: `bindNamespace` binds the name in
both sorts, and a member read looks in the namespace first and falls through to
that value. What is missing is a pseudo-package that sets it. `exportedSurface`
in `internal/solver/package_load.go` leaves `AsValue` nil, and only
`assignedSurface` beside it sets one, reached solely from an npm `.d.ts` writing
`export = f`.

The rule is that a pseudo-package exporting one value is treated as that value.
`std:console` is the case that already exists: it exports `Console` and a
`console` value, and `console.log(...)` works today only because `std:console`
is in `ambientPackages` and the ambient layer binds a package's exports
unprefixed. Under this rule the explicit import works the same way, and the
ambient layer stops being what makes the common spelling resolve.

The test is the value whose name is the package's own binding, not a count of
value exports. `web:window` exports `window` and also `Window`, since a class
occupies the value sort as well as the type sort, so a count would find two and
the rule would not fire. `console` in `std:console` and `window` in `web:window`
each match their package's derived name, and `Window` does not.

1. In `exportedSurface`, set `AsValue` to the exported value whose name equals
   `ast.DeriveImportName(uri)`, when the package has one.
2. Assert it on `std:console`, where `console.log(...)` must resolve through an
   explicit `import "std:console"` rather than through the ambient layer.

**Done when** a fixture importing `std:console` calls `console.log` and reads
`console.Console` as a type, with `std:console` removed from `ambientPackages`
for the duration of the test.

**Not this stage.** Any scope package, and any `export =` in a generated file.
The rule reads the declarations the converter already emits, so the printer
needs no new case.

### 5.6 Place the portable and family globals

**Goal.** F8 for the globals that move rather than un-hoist.

1. Move the portable globals out of `web:dom`. `setTimeout`, `clearTimeout`,
   `setInterval`, `clearInterval`, `atob`, `btoa`, `structuredClone`,
   `queueMicrotask`, `reportError`, `origin`, `isSecureContext` and
   `crossOriginIsolated` name no browser type and belong in `web:core`, whose
   exports an importer binds unprefixed, so reaching them is one import and no
   qualifier.
2. Move the family globals to their families: `caches` to `web:cache`,
   `indexedDB` to `web:indexeddb`, `localStorage` and `sessionStorage` to
   `web:storage`. `crypto`, `performance` and `fetch` already went.

Around 16 declarations move. `hoisted/` has the per-global analysis, generated
from the closed stack's tree, so re-derive the counts against `main` first; the
scripts are checked in to do that.

**Done when** a fixture calls `setTimeout` and `atob` with no import at all, and
`check_generated_tree` passes.

**Not this stage.** The window-only globals, which do not move anywhere.

### 5.7 Add `web:window` and un-hoist the window-only globals

**Goal.** F4 and the rest of F8, and the part of the landed hoisting that has to
be reversed rather than relocated.

1. Add `web:window` holding `Window`, its mixins and `WindowEventMap`, which is
   around 1038 lines across 14 declarations in `web/dom.esc` today.
2. Point its binding at the scope object, using §5.5.
3. Delete the top-level copies of the window-only globals, which `hoisted/`
   counts at 176. The members they were copied from are already on `Window`, so
   nothing is declared to replace them.

**Done when** a fixture reaches `alert` only as `window.alert` after importing
`web:window`, the converter rejects a bare `alert`, and
`check_generated_tree` passes.

**Not this stage.** The worker and worklet scope classes, which are not in the
tree. They get the same treatment once the other stream ingests them.

### 5.8 The rest of the augmentation, if measured

Open question 1 lists what else WebIDL carries that the `.d.ts` flattens:
nullability, integer width and range, `sequence<T>` against array, `USVString`
against `string`, `optional` with a default, `[SameObject]` and `[NewObject]`.

None is measured. Measure one before planning it, the way #1645 and the
`Transferable` set were measured, because the argument for each is how often the
`.d.ts` is wrong rather than how much WebIDL says.

Widening the conversion input past `lib.dom.d.ts` belongs to the same later
phase. It is what unblocks the four transferable interfaces §5.4 leaves out.

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

1. **Which of the remaining WebIDL facts are worth adopting?** F1 names four
   because four have been measured. WebIDL carries more that the `.d.ts`
   flattens: nullability markers, integer width and range through
   `unsigned long`, `[Clamp]` and `[EnforceRange]`, `sequence<T>` against array,
   `USVString` and `ByteString` against `string`, `optional` with a default, and
   `[SameObject]` and `[NewObject]`. Each is separate work with its own payoff
   and none is measured, so the argument for each is how often the `.d.ts` is
   wrong rather than how much WebIDL says.

   *Decided, for the surface rather than the facts.* The augmentation covers
   what `lib.dom.d.ts` declares. An interface WebIDL marks and no pinned lib
   carries waits for a later phase that widens the conversion input, which is
   why F3's set gains two arms and not six. Which facts that later phase adds is
   what stays open here.

2. **What is left of the overlay?** *Decided: it shrinks but stays.* Every fact
   moved to WebIDL is a reason for an overlay entry to go, and nobody has checked
   which. What does not go is the one-off retype that no source answers, as with
   `MessageEvent.source` to `null` and `ports` to `Array<unknown>`, where the
   platform's own type names a class the portable tier cannot reach. Those are a
   tier decision rather than a `.d.ts` error, so WebIDL has nothing to say about
   them.

3. **What is the new name for `self`?** *Decided: `worker` and `worklet`,
   mirroring `window`.* The scope object is exposed so a worker can reach
   itself, and `self` would read as the receiver `self` of a method body. Each
   family names its handle after itself, and the declaration carries
   `@js("self")` the way `window` carries `@js("window")`.

   `self` is not reserved. The parser treats it specially only in a receiver
   position, so the rename serves the reader rather than the grammar.

   What this needs is a generated package that sets `AsValue`, which F4
   describes and section 5.5 builds.

## 9. Non-goals

- Replacing the `.d.ts` set as the conversion input. WebIDL supplements it with
  facts it does not carry or gets wrong. Generating the tree from WebIDL alone
  is a larger question and this does not answer it.
- Runtime feature detection in user code, such as narrowing on
  `typeof OffscreenCanvas !== "undefined"`.
- `typeof globalThis`. A program names the scope object through its package, as
  `window`, `worker` or `worklet`, so the intersections naming `globalThis` lose
  their second half and the annotation goes. Nothing replaces it.

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

