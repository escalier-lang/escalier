# Design: Improving the web API type definitions

## 1. Scope

This is one of two streams. It covers what a declaration in
`internal/interop/data/web/` **says**: its members, their signatures, which
package holds it, and whether a global is reachable at all.

`planning/environments/` covers **where** a declaration exists, which
environments provide it and what the compiler does about that. Its environments
are the browser global scopes and, from its phase four, the non-browser
runtimes.

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
the omission. `planning/environments/transferable.md` has the table.

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
packages have the second one. F7 removes both, keeping one layer and deciding
membership by the package's name rather than by a list and a constant. Nothing
asserts what `ambientPackages` binds either, so N4 stands. Section 5.1 does
both, and renames `web:core` to `web:prelude` as part of it.

### 3.4 A third-party `.d.ts` cannot name a DOM type

An npm package's declarations are checked against the ambient scope.
`package_load.go` parents the package's scope at `globalsScope()`, because a
`.d.ts` is written against TypeScript's global library and names `Document` or
`Blob` with no import of its own.

The ambient scope holds 26 packages, of which two are `web:*`: `web:core` and
`web:fetch`. So the global library a `.d.ts` is checked against is missing most
of the platform.

| name | package | resolves in a `.d.ts` |
| --- | --- | --- |
| `Event`, `EventTarget`, `AbortSignal`, `DOMException` | `web:core` | yes |
| `Request`, `Response`, `Headers` | `web:fetch` | yes |
| `Blob`, `File`, `FormData` | `web:file` | no |
| `ReadableStream` | `web:streams` | no |
| `URL` | `web:url` | no |
| `Document`, `HTMLElement`, `Node` | `web:dom` | no |

A package naming one of the unreachable ones fails to load with
`cannot find type` against its own `.d.ts`, so the error lands on a file nobody
can edit. These are not obscure names: a client library takes a `Blob` body, a
React binding names `HTMLElement`, and an upload helper names `FormData`.

### 3.5 What else moved

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

This is the general rule that a pseudo-package exporting a value under its own
name is treated as that value, and not something scope packages need of their
own. `std:console` is
the same shape. It exports a `console` value and a `Console` type, and the
useful spelling is `console.log(...)` rather than `console.console.log(...)`.

The binding occupies both sorts, so it reads as a namespace that happens to
carry values and a type together. `console.Console` resolves as the type, from
the namespace. `console.log(...)` is not a member the namespace holds, so
`AsValue` supplies it off the `Console` instance. The two spellings share a
qualifier and neither needs the package's name repeated.

`web:window` is the same arrangement one scope up: `window.Window` is the type
and `window.alert(...)` is the member. Section 5.5 states the rule precisely and
says why it tests the name rather than counting value exports.

`web:fetch` is a third instance, and the reason F7 costs it nothing. It exports
four values, the classes `Headers`, `Request` and `Response` plus the function
`fetch`, and `fetch` is the one whose name matches the package's binding. So
`import "web:fetch"` gives `fetch(...)` as a call and `fetch.Response` as a
type. That is the shape `assignedSurface` already documents for an npm package
whose `.d.ts` writes a function and a namespace of one name, so the rule covers
a callable binding as well as an object.

None of the three exports exactly one value. A class occupies the value sort
beside the type sort, so `web:window` has `window` and `Window`, and `web:fetch`
has four. Only `std:console` would pass a count, which is why the test is the
name and never the number.

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
`web:prelude` could not. `EventTarget.addEventListener` is in `web:prelude`
already and `Window` restates the four typed overloads, so
`window.addEventListener` and `element.addEventListener` are both correctly
typed today with nothing to move. `hoisted/README.md` records what a hoisted
copy would have cost.

**F6. A member a family package already declares is not hoisted twice.** `fetch`
is a member of `WindowOrWorkerGlobalScope` and a top-level declaration of
`web:fetch`. The hoist drops the member rather than adding a second binding for
one global.

**F7. Only a prelude is ambient in a project's own file.** An `.esc` file reaches
the `*:prelude` packages without an import, and every other package by importing
it, so `std:math` gives `math.PI` and `std:error` gives
`error.Error`. `web:core` is renamed `web:prelude` to say what it is.

The naming convention is the rule. Whether a package is ambient is readable from
its URI, rather than from a list in `internal/solver/ambient.go` plus a
`coreURI` constant in two more packages. Nothing else may join the ambient layer
without being named a prelude, which is a decision a reader can see at the
import site.

`coreURI` and `bindCoreExports` go. They exist to bind one package's exports
unprefixed while still requiring an import, which is the shape a prelude already
has without the import, so the two collapse into one. Section 3.3 has the two
mechanisms they currently are.

*This trades a diagnostic away.* `bindCoreExports` binds into the file scope, a
child of the module scope, so a module declaring `Event` gets a reported
collision. The ambient layer binds from a parent, so the module's declaration
wins silently. That is what `std:prelude` already does, and shadowing a prelude
name is ordinary, so the loss is accepted rather than overlooked.

A converted `.d.ts` keeps the whole ambient globals scope, which is F9. It is
written against TypeScript's global library and names `Console` or `JsonTag` with
no import, and it cannot be rewritten to add one.

*The split already exists as an option.* `Options.NoAmbientNames` empties the
ambient scope for the project's own files while an npm package's scope stays a
child of the globals scope, which `package_load.go` says is deliberate: "that
option governs the names the project's own files reach."
`TestNodeModulesImportReadsGlobalsWithoutAmbientNames` asserts both halves, that
the package resolves `JsonTag` and the entry module does not. What F7 asks is
that this stop being an option, become the behavior, and admit the preludes.

What an `.esc` file gives up is 203 top-level names plus the `Math`, `Intl`,
`JSON`, `Reflect` and `Atomics` namespaces that `bindGlobals` reconstructs from
dotted `@js` paths. That scope is a faithful copy of the JavaScript global
object, which is the right thing for a `.d.ts` to be checked against and the
wrong thing for a language that has imports.

**`web:prelude` is ambient unconditionally here, and probably everywhere.** Its
24 declarations are `Event`, `EventTarget`, `AbortController`, `AbortSignal`,
`DOMException`, `MessageEvent`, `TextEncoder`, `TextDecoder` and their option
bags. Node, Deno and Bun all implement that set, which is what makes the package
`TierCore` rather than browser-tier, so a gate on the target would admit it for
every environment anyway.

The gate is for a prelude whose scheme names one runtime. `node:prelude` holding
`process` and `Buffer` has no business in scope for a browser program, and that
is the case worth refusing. `planning/environments/` owns the rule, its §7.3.2
builds it, and F10's `node:prelude` is the first prelude it excludes from
anything.

Availability inside an ambient package is a separate question that `@env`
answers. `MessageEvent` is `Exposed=(Window, Worker, AudioWorklet)` and the
timers §5.6 moves in are `@env("window", "worker")`, so a paint worklet has
those names in scope and the reference check refuses them. The package being
ambient is about what a file can name, not about what the platform provides.

**F8. A hoisted global goes to the package that owns it, not to `web:dom`.** A
global's package is the family that owns its type, and where the signature names
no browser type the family is `web:prelude`. `setTimeout` and `atob` belong there
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

**F9. A third-party `.d.ts` resolves every web type it names.** A converted npm
package reaches the whole platform, not just the two `web:*` packages
`ambientPackages` happens to hold, which are `web:prelude` and `web:fetch`. A
library that takes a `Blob` or returns an `HTMLElement` loads, and a
`cannot find type` never lands on a `.d.ts` the project cannot edit.

F7 splits the ambient scope so it serves a `.d.ts` and not a project's own
files. That is what makes this affordable: once no `.esc` file reads the ambient
scope, what it holds is invisible to user code and can be as wide as the global
library it stands in for.

Two mechanisms would do it, and §5.1 picks one.

The first adds every `web:*` package to `ambientPackages`, so the scope a
`.d.ts` is checked against is the platform. It is the honest model, since
TypeScript's global library is exactly that, and it costs a load of packages the
program may not use. The measurement in section 6 says that cost is small:
`buildPackageGraph` already parses every file in the tree to read its import
header, so the packages are read whether or not anything binds them.

The second qualifies the references instead. `qualifyCrossPackageRefs` in
`internal/dts_to_esc/import_header.go` already rewrites `Blob` to `file.Blob`
and adds the matching import when generating the tree, and the same pass over a
converted npm package would give it exactly the packages it names. That is
narrower, and it needs the name-to-package map at check time rather than
conversion time, where the solver does not have one today.

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
`BenchmarkStdlibClosureLoad`. Cold is the only figure that constrains this work,
since the cross-run checker cache has taken whole-tree warm inference to 0.67ms.
Cold is between 4.6s and 6.4s for every package against about 1s for none, so it
does scale with closure size, and the every-package figure is still under
re-measurement. §5.8 and §6.2 of `../environments/design.md` carry the runs.
#1862 is open against the benchmark and unfixed. #1861 is closed as not planned, since it measured the warm
cost the cache removed.

**N6. A read is never narrower than an environment provides.** Where environments
disagree about what a member yields, the tree states each environment's form,
which is `planning/environments/`' F5. It does not pick the narrowest and call it
portable.

The rule is about reads, not writes. Narrowing what a program may pass or
construct rejects valid code, which is a cost and not a lie. `FormData`'s
overlay entry keeps only the no-argument constructor, so a browser program
cannot build one from a form element, and #1586 is where that goes. Narrowing
what a program receives tells it something false about a value it already holds,
and no amount of inconvenience elsewhere makes that the cheaper trade.

`web:core`'s `MessageEvent` is the case to undo. The overlay types `readonly
source: null` and `readonly ports: Array<unknown>`, where a browser gives
`source` a `WindowProxy`, a `MessagePort` or a `ServiceWorker`. The doc comment
directly above the field says so and the type contradicts it. A file reaching
`MessageEvent` is told `null` and handed an object, which is the one way a
declaration can be actively wrong rather than merely unhelpful. #1613 tracks it.

Undoing the retype is also what moves `MessageEvent` out of the prelude. Its
real `ports` is `Array<MessagePort>`, so it belongs with the port it carries,
and §7.4.5 of the other stream gives that family a package of its own.

Unnameable beats narrow. A declaration whose type a file cannot spell is sound,
and `Transferable` is the shape that proves it: the union names arms in packages
an importer may not reach, so the type is accurate and the file cannot narrow to
those arms. That costs a cast. A retype costs correctness, and the two are not
alternatives of the same kind.

This bounds what the overlay may do. It keeps a retype that no source answers.
It does not keep one that understates what a real target provides.

## 5. Plan

Stages land on their own and leave the tree green. Each names what it leaves
alone.

Every stage is tracked in one milestone:

| milestone | stages | issues | tracking |
| --- | --- | --- | --- |
| [Web API: Type definitions](https://github.com/escalier-lang/escalier/milestone/11) | 5.1 to 5.7 | #2390, #2388, #1645, #1646, #2389, #2391, #2394, #2395 | #2414 |

#2414 holds the dependency graph as a mermaid diagram and says which orderings
are forced and why. There are two chains in it, independent of each other. §5.2
roots the WebIDL work, and §5.1 roots the binding and placement work that runs
from §5.1 through §5.7.

Section 5.8 has no milestone. It is a standing instruction to measure before
planning, not work to schedule.

§5.2 comes first within the milestone, because its table is what the
environments stream waits on. #2378 is not in a milestone and blocks any fixture
importing `web:dom` or `web:window`, which is §5.7's done-condition.

A stage that regenerates the tree produces a large diff that needs no
line-by-line review. The hand-written part is the generator and the fixture, and
`check_generated_tree` is what validates the rest. §5.2, §5.3 and §5.7 are the
three where the generated half is big enough to say so.

### 5.1 Rename `web:core` to `web:prelude`, and let only a prelude be ambient

**Goal.** F7, F9 and N4, before anything else. Every later stage moves
declarations between packages, and while the globals scope covers a project's
own files, moving one silently changes what every `.esc` file has in scope.

`Options.NoAmbientNames` already does the `.esc` half of F7, and
`TestNodeModulesImportReadsGlobalsWithoutAmbientNames` already asserts it, so
part of the work is removing a choice rather than building a mechanism.

1. Rename `web:core` to `web:prelude`: the generated file, the two import
   headers that name it, its row in `internal/dts_to_esc/tier.go`, and the
   overlay `internal/interop/overlay/web/core.replace.esc`.
2. Make the `NoAmbientNames` behavior unconditional for a project's own files
   and remove the option, so there is one answer rather than two. An `.esc`
   file's ambient layer is then the `*:prelude` packages and nothing else.
3. Delete `coreURI` and `bindCoreExports`, whose nine call sites across
   `internal/solver/imports.go`, `internal/solver/stdlib_group_load.go` and
   `internal/dts_to_esc/import_header.go` all go. A reference to a
   `web:prelude` export needs no qualifier and no import, so
   `qualifyCrossPackageRefs` skips it for the reason it already skips
   `std:prelude`.
4. Leave `package_load.go`'s parenting of an npm package's scope at the globals
   scope alone. That is the half of F7 that stays.
5. Settle F9 and apply it, so a `.d.ts` naming `Blob` or `HTMLElement` loads.
   Widening `ambientPackages` is the smaller change and the one to try first.
6. Add the test N4 asks for, over what an `.esc` file reaches with no import.
   Assert the `.d.ts` side separately, since the two scopes now differ.
7. Record in `docs/03_imports.md` where the shape-loading line falls, which the
   document predates.

**Done when** a fixture naming `PI`, `console` or `Error` with no import fails,
the same fixture compiles with the imports, a fixture reaching `Event` and
`setTimeout` with no import succeeds, an npm package whose `.d.ts` names `Blob`
and `HTMLElement` loads clean, no `coreURI` remains, and both name sets are
pinned.

**Not this stage.** Any change to which package holds a declaration, and gating
`web:prelude` on the target, which F7 leaves to the other stream. This moves
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

Dropping a constructor is a redirection, not a loss, and the thing it redirects
to has to stay reachable from the same environments as the class. A worker gets
an `ImageBitmap` from `createImageBitmap` and a `MessagePort` from
`new MessageChannel()`, both of which are `Exposed=(Window, Worker)` and keep
their own constructor. The class value also stays, since `instanceof` needs it,
so what goes is the construct signature alone.

**Done when** `new ImageBitmap()` and `new MessagePort()` are rejected,
`createImageBitmap` and `new MessageChannel()` still typecheck,
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
`planning/environments/transferable.md` records which four of its sixteen
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

The rule is that a pseudo-package exporting a value under its own name is
treated as that value. `std:console` is the case that already exists. It exports
a `console` value and a `Console` type, and `console.log(...)` resolves today
only because
`std:console` is in `ambientPackages` and the ambient layer binds a package's
exports unprefixed. Section 5.1 takes that layer away from a project's own
files, so by the time this stage runs nothing makes `console.log(...)` resolve
and this rule is what restores it.

Both sorts come back under one qualifier. `console.Console` is the type, read
off the namespace, and `console.log(...)` is a member the namespace does not
hold, so `AsValue` supplies it off the `Console` instance.

The test is the value whose name is the package's own binding, not a count of
value exports. `web:window` exports `window` and also `Window`, since a class
occupies the value sort as well as the type sort, so a count would find two and
the rule would not fire. `console` in `std:console` and `window` in `web:window`
each match their package's derived name, and `Window` does not.

1. In `exportedSurface`, set `AsValue` to the exported value whose name equals
   `ast.DeriveImportName(uri)`, when the package has one.
2. Assert it on `std:console`, which §5.1 has already stopped binding ambiently
   in a project's own files.

**Done when** a fixture importing `std:console` calls `console.log(...)` and
annotates with `console.Console`, and the same fixture without the import fails
on both.

**Not this stage.** Any scope package, and any `export =` in a generated file.
The rule reads the declarations the converter already emits, so the printer
needs no new case.

### 5.6 Place the portable and family globals

**Goal.** F8 for the globals that move rather than un-hoist.

1. Move the portable globals out of `web:dom`. `setTimeout`, `clearTimeout`,
   `setInterval`, `clearInterval`, `atob`, `btoa`, `queueMicrotask`,
   `reportError`, `origin`, `isSecureContext` and
   `crossOriginIsolated` name no browser type and belong in `web:prelude`, which
   is ambient, so reaching them takes no import and no qualifier.

   Every one of them carries `@env("window", "worker")`. They are members of
   `WindowOrWorkerGlobalScope`, which `WorkletGlobalScope` does not include, so
   a worklet has no timers. Portable is not the same claim as universal, and
   leaving these unannotated would say all nine environments have them.

   Two helper types move with them, or the signatures name `web:dom` from
   `web:prelude` and `CheckTiers` rejects the edge. `TimerHandler`, which is
   `string | function.Function`, reaches 29 declarations and all but itself are
   `std:*`. `VoidFunction` reaches nothing but itself.

   `structuredClone` goes somewhere else entirely. Its options bag is
   `StructuredSerializeOptions`, whose `transfer` field is
   `Array<Transferable>`, so it belongs with the messaging family rather than
   with the timers. §7.4.5 of the other stream gives that family the package
   `web:messaging` and takes `structuredClone` with it, which means calling it
   costs an import where `setTimeout` costs none.

2. Move the family globals to their families: `caches` to `web:cache`,
   `indexedDB` to `web:indexeddb`, `localStorage` and `sessionStorage` to
   `web:storage`. `crypto`, `performance` and `fetch` already went.

Around 17 declarations move: the 11 globals above, the two helper types, and the
four family globals below. `hoisted/` has the per-global analysis, generated from
the closed stack's tree, so re-derive the counts against `main` first. The
scripts are checked in to do that, and `hoisted/scripts/closure.mjs` is what
says whether a signature's references can follow it.

**Done when** a fixture reaches `setTimeout` and `atob` with no import at all, a
worklet fixture is refused both once the other stream can express a worklet
target, and `check_generated_tree` passes.

**Not this stage.** The window-only globals, which do not move anywhere, and
`structuredClone`, which waits on the other stream.

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
a reorganization has to watch. Cold is the whole story, and it does scale with
closure size: `NoImports` costs 954ms and `OnePackage` 980ms, where every
package is between 4.6s and 6.4s across runs. The spread is why the
every-package figure is under re-measurement rather than quoted as one number.

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
   which. What stays is the one-off that no source answers, such as `EventTarget`
   dropping the `null` listener the DOM spec allows, where the correction is a
   judgement the `.d.ts` and WebIDL both leave open.

   What does not stay is a retype that narrows a member below what a target
   provides, which N6 now forbids. `MessageEvent.source` to `null` is one, and
   its replacement is a per-environment declaration rather than an overlay
   entry.

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
| #2377 | importing `web:dom` reports 346 errors, so several done-conditions here cannot be met until it is fixed |
| #2378 | the override check rejects a method that indexes its own type parameter, which is 319 of those errors |

