# Requirements: Web environments and package organization

## 0. History

An earlier environment system, the stack of PRs #1628 through #1635, was closed
without merging. `main` has none of it: no `@env` decorator and no annotations,
no `internal/dts_to_esc/env*.go`, no `CheckEnvs` or `DeclEnvs`, no environment
vocabulary, no file-name suffix convention, and no `web:worker` package. What it
has instead is the package-level tier rank that stack was replacing, described in
section 2.

So this is a greenfield design rather than a revision, and two problems the
earlier work hit no longer apply. #1648 landed, so an interface with several
supertypes converts to a class that `implements` the rest, which the merged
`Navigator` in `hoisted/README.md` depends on. And no file name claims anything
about its contents any more, so N1 holds on the current tree and open question 1
is moot.

Sections 2 and 3 describe `main` at 06b28c81. `implementation_plan.md` opens
with the same inventory from the implementer's side.

## 1. Problem statement

A web declaration exists on some set of global scopes. A page has the DOM, a
dedicated worker has a different surface, a shared worker a third, a service
worker a fourth, and the four worklets more. Nothing in the tree records which,
and the one mechanism that comes close measures a different thing.

The tier rank in section 2 answers how many runtimes ship a package, which is the
question of whether code runs off the browser at all. Every global scope inside a
browser is one tier, so the rank cannot distinguish them, and portability is a
total order where global scopes are not. Section 3 is what that costs.

## 2. What the tree does today

### 2.1 One rank per package, ordered by portability

`internal/dts_to_esc/tier.go` gives each pseudo-package a `Tier`, and the four
values form a total order:

| tier | value | what it promises |
| --- | --- | --- |
| `TierLanguage` | -1 | wherever ECMAScript is. Every `std:*` package. |
| `TierCore` | 0 | every runtime implementing the WinterTC minimum common API. |
| `TierPortable` | 1 | every non-browser WinterTC runtime, and the browser. |
| `TierBrowser` | 2 | a browser alone. |

`webTiers` assigns one to each of the 24 `web:*` packages. Nine are portable,
including `web:fetch`, `web:url`, `web:streams` and `web:websocket`. Ten are
browser, including `web:dom`, `web:workers`, `web:service_worker`,
`web:indexeddb` and `web:webgl`. `web:core` is the only core package.

### 2.2 The check is on import edges, and runs in the converter

`CheckTiers` at `internal/dts_to_esc/import_header.go:116` reports every import
edge whose target sits above its source. A portable package may name a core one
and not a browser one.

It has one caller, `internal/dts_to_esc/generate.go:138`. Like the system before
it, it validates the generated tree against itself at conversion time. The
compiler pipeline has no notion of a tier, so none of it reaches a user program.

### 2.3 `web:core` binds unprefixed

`coreURI` at `internal/dts_to_esc/import_header.go:33` and
`internal/solver/imports.go:94` make `web:core` the one package whose exports an
importer binds without a qualifier, which is why `web:dom` writes `EventTarget`
rather than `core.EventTarget`. The comment at `import_header.go:222` groups it
with `std:prelude` as the two packages that do this.

### 2.4 What the axis measures

A tier answers "how many runtimes ship this", and the values order because that
is a nesting: language ⊃ core ⊃ portable ⊃ browser. The axis is the WinterTC
question, whether a package exists off the browser at all.

It says nothing about which global scope inside a browser has a declaration. A
page, a dedicated worker and a service worker are all "the browser", so all three
collapse into `TierBrowser` together.

## 3. Observed problems

### 3.1 The tier axis cannot express a global scope

Portability is a total order and global scopes are not. A page has `document`, a
dedicated worker has `importScripts`, a service worker has `clients`, and no two
of those three contain each other. There is no rank that puts them in sequence,
so the mechanism that works for Node-versus-browser cannot be extended to
window-versus-worker by adding values to it.

`web:workers` and `web:service_worker` are both `TierBrowser`, which is correct
about the browser and silent about everything this document is for.

### 3.2 A worker has no surface at all

A page transfers an `OffscreenCanvas` to a worker:

```
val offscreen = canvas.transferControlToOffscreen()
worker.postMessage({ canvas: offscreen }, [offscreen])
```

The worker receives a real `OffscreenCanvas` and draws on it. To type the handler
it has to name `OffscreenCanvas`, which is in `web:dom`, a browser-tier package
describing a page.

That is the smaller half. The worker-only surface is not in the tree at all.
`importScripts`, `WorkerLocation`, `FetchEvent`, `ExtendableMessageEvent` and
`Clients` have zero occurrences under `internal/interop/data/`, and
`WorkerGlobalScope`, `DedicatedWorkerGlobalScope`, `SharedWorkerGlobalScope` and
`ServiceWorkerGlobalScope` are not declared anywhere. `web:workers` holds
`Worker` and `SharedWorker`, the handles a page constructs, and nothing a worker
runs inside.

So a worker program cannot name what it receives, and cannot name its own
globals either.

### 3.3 Nothing enforces a tier on user code

`CheckTiers` has one caller and it is the converter. A program can import
`web:dom` and `web:workers` together, or import `web:dom` from code meant for a
worker, and nothing reports it.

### 3.4 A program has no target environment

Nothing records which runtime a program is compiled for. Any rule that resolves a
name differently per environment, or rejects an import, has nowhere to read the
answer from. `escalier.toml` holds a project name and `package.json` holds
`main` and `bin`; neither says where the code runs.

### 3.5 The partition cuts across environments

`webPackages` in `internal/dts_to_esc/partition.go` groups declarations by API
family, and environments cut across those families. `OffscreenCanvas`,
`ImageBitmap`, `MessagePort` and `Path2D` are in `web:dom` with `Document` and
`Element`, though the first four exist in every worker and the last two in none.
A package is therefore not usable as a unit by any environment but a page.

### 3.6 Availability is per package, where the facts are per declaration

`TierPortable`'s own doc comment says the promise is per package and not per
name, and #1586 measured the gap against Node 22: `web:file` is portable but
`FileList` and `FileReader` are not, and `web:performance` is portable but five
of its timing interfaces are not.

The same coarseness will bite harder on the environment axis, because the source
data is coarse too. `lib.webworker.d.ts` is one file covering dedicated, shared
and service workers, so it cannot distinguish them. WebIDL can, through
`[Exposed]` and `[Global=...]`, and `vocabulary.md` and `transferable.md` are
built from it.

### 3.7 Per-arm availability has no representation

`internal/interop/overlay/web/core.replace.esc` still retypes
`MessageEvent.source` to `null` and `ports` to `Array<unknown>`, and its comment
still reasons in tiers:

> `source` is a `WindowProxy`, a `MessagePort` or a `ServiceWorker` in the
> browser and always null off it, so `null` is what every portable runtime
> reports.

Every program pays the narrowed types so that one declaration can serve every
tier. `url.replace.esc` suppresses the `MediaSource` overload of
`URL.createObjectURL` for the same reason. #1613 tracks this.

`WindowProxy` is a further wrinkle. `lib.dom.d.ts` declares it as
`type WindowProxy = Window`, a type alias, so no realm has a `WindowProxy`
binding. Narrowing it means testing `Window`, which a worker genuinely lacks.

### 3.8 Load cost is charged per closure but paid per tree

`BuildPackageClosure` puts every reachable package into one group, so any edge
into `web:dom` pulls the whole tree into every program's closure. Measured on
`main` at 06b28c81 with `BenchmarkStdlibClosureLoad`:

| | warm | cold |
| --- | --- | --- |
| `web:fetch` alone | 43ms | 810ms |
| every package | 380ms | 4.45s |

Cold is dominated by something other than closure size: `buildPackageGraph`
parses every file in the tree to read its import header, reached or not. #1643
records that comment attachment is 74% of that, all of it in one linear scan in
`ast.(*nodeIndex).enclosing`, and it has not landed.

The warm figure is what closure size costs per inference run, and at 380ms
against 43ms it is the constraint on any repartitioning.

An earlier draft of this section compared those numbers against 46ms warm and
951ms cold and concluded that whole-tree load was cheap. Both halves of that were
wrong.

The 46ms came from a benchmark that measured less than it claimed.
`committedPackageURIs` derives a root URI by trimming `.esc` from the file name,
so `dom.window.esc` yields `web:dom.window`, which matches no partition entry,
and `reachable` skips a root it has no edges for. On the branch those figures
came from, 15 of 25 `web/` files carried such a suffix, so "every package"
resolved 10 of them and reported a number anyway. #1862 covers it.

And warm has genuinely regressed, by 1.5x rather than the 8x the bad baseline
implied. Probing the 65 commits between 6c620c22 and 06b28c81 puts almost all of
it in one step, 282ms to 413ms at 6c447c63, which added 177 lines to
`internal/solver/infer_class.go`. #1861 covers it, with the profile: 21% in
`inferClassDecl`, 21% in `constrain`, 21% in `trialUnderProbe` and 29% in garbage
collection.

So #1631's 4.4s was right. It is what the whole tree costs, and the figure this
document called unreliable was the only correct measurement of the three.

### 3.9 A global is reachable only through a scope class

Escalier does not give a program the global object. Declarations are organized
into packages and imported under a namespace binding, so a page calls
`fetch(...)` after `import "web:fetch"` rather than reading it off a global.

`Window` and `WindowOrWorkerGlobalScope` do not follow that. Both are in
`web/dom.esc` as ordinary declarations whose members a program has no way to
reach, so a page cannot call `alert`, read `innerWidth` or set `onclick`. The
declarations exist and describe the runtime correctly, and nothing can name them.

`web:fetch` shows the target form, a top-level declaration carrying the global's
own name:

```
@js("fetch")
export declare fn fetch(input: RequestInfo | url.URL, init?: RequestInit) -> Promise<Response>
```

`std:console` does the same with `@js("console")` on a `declare var`. So the
shape is established in two packages and applied to none of the scope classes.

## 4. Requirements

Functional requirements say what the compiler does that it does not do today.
Non-functional requirements constrain how the tree and the conversion are built,
and hold whether or not any functional requirement changes.

### 4.1 Functional

**F1. A program has a target environment, or none.** A program compiled for a
runtime declares which one, or it is inferred from the packages it imports.
Every rule below reads that one answer.

A program may also have no target, which is not an absence but a claim: the code
runs in every environment. Its references have to resolve in all nine, so it
reaches the intersection of what they provide. This is what a portable library
is, and it is the reason a target is optional rather than required.

The intersection of a set of per-environment declarations is empty, and that is
the intended answer rather than a gap to patch. A program with no target cannot
use `location` at all, because `Location` in a page and `WorkerLocation` in a
worker have nothing in common that the program could rely on. It has to name a
target to get one.

**F2. An unsatisfiable set of imports is reported.** Importing two packages no
single environment provides, such as `web:dom` and `web:worker`, is an error
naming both. Under inference this is the empty intersection of F1.

**F3. A reference outside the program's environment is reported.** The check that
a reference does not escape its environments applies to a user program, not only
to the generated tree. It is an error from the first release rather than a
warning that hardens later.

It covers a value reference in any position, including a pattern. Matching
`x is OffscreenCanvas` while targeting an environment without `OffscreenCanvas`
is a compile-time error, not a guard that never fires. Nothing is emitted that
could throw `ReferenceError` at a narrowing site.

**F4. Every declaration an environment provides is nameable in it.** A worker can
name an `OffscreenCanvas` it receives, and everything reachable from it, without
importing declarations that environment lacks.

**F5. A declaration that differs by environment is expressible per environment.**
Where environments genuinely disagree, as with `MessageEvent.source`, the tree
states each environment's form rather than narrowing every environment to their
intersection.

**F6. A global is reached through a package binding.** Every member of a
`*GlobalScope` class is a top-level declaration in the package owning its family.
F14 is what keeps this readable for the globals that land in `web:core`.
A worker program calls `worker.importScripts(...)` after importing
`web:worker`, and no program is handed a scope object to read members off.

**F7. Each member kind hoists to its matching top-level form.** A method becomes a
`declare fn`, a read-only property a `declare val`, a settable event-handler
property a `declare var`, and an overload set one `declare fn` per signature.
The hoisted declaration carries `@js` with the global's own name, as
`web:fetch`'s `fetch` already does.

**F8. A hoisted member's environments come from the scope that declares it.**
Resolution follows the inheritance chain, so a member reaches every environment
whose scope class inherits it:

| declaring scope | `@env` on the hoisted declaration |
| --- | --- |
| `WindowOrWorkerGlobalScope` | none, meaning every environment |
| `WorkerGlobalScope` | `worker` |
| `DedicatedWorkerGlobalScope` | `dedicated_worker` |
| `SharedWorkerGlobalScope` | `shared_worker` |
| `ServiceWorkerGlobalScope` | `service_worker` |
| `Window` | `window` |

**F9. A member a family package already declares is not hoisted twice.** `fetch`
is a member of `WindowOrWorkerGlobalScope` and a top-level declaration of
`web:fetch`. The hoist drops the member rather than adding a second binding for
one global.

F6 depends on F5. Several scope members share a name across scopes and differ in
type, so hoisting them produces several top-level declarations of one name with
disjoint environments. `onmessage` is a `MessageEvent` handler on
`DedicatedWorkerGlobalScope` and an `ExtendableMessageEvent` handler on
`ServiceWorkerGlobalScope`. `addEventListener` is generic over each scope's own
event map, so every scope contributes a different one. `location` is a
`WorkerLocation` in a worker and a `Location` in a page. Without per-environment
declarations the hoist has to fall back on the intersection of these, which is the
same loss `MessageEvent.source` already takes.

**F10. The environment vocabulary matches the platform's globals.** `@env` names
the nine global scopes WebIDL declares with `[Global=...]`, and the two group
names those scopes answer to. An unannotated declaration means all nine, which
is `Exposed=*`. `vocabulary.md` in this folder has the names and what the change
costs.

Two consequences, both breaking. `worker` covers four environments rather than
three, because `RTCIdentityProviderGlobalScope` is
`[Global=(Worker, RTCIdentityProvider)]`. And an unannotated declaration claims
nine environments rather than four, so around 290 declarations that carry no
decorator today need one.

**F11. A worklet is a compilation target with packages of its own.** A paint
worklet is a module the browser loads, so it is a target in the same sense a
worker is, and F1 through F5 apply to it unchanged. The `web:*` packages cover
the worklet surface, which they do not today: the CSS Typed OM sits in
window-only `web:dom`, so a paint worklet cannot name `CSSUnitValue`.

**F12. A package states each entrypoint's environments in `package.json`.** The
environments an entrypoint supports travel with the published package, so they
live in `package.json` and nowhere else. `escalier.toml` is not consulted; it
does not ship.

Every entrypoint is covered, both the `lib/` one named by `main` and each `bin/`
one named under `bin`. They are stated separately, because a package routinely
ships a page module and a worker module together, and the two do not run in the
same place. An entrypoint listing every environment, or listing none, is the
no-target case from F1.

The spelling is open. Something along these lines, keyed by the source
entrypoint rather than the build output:

```json
{
  "main": "build/lib/index.js",
  "bin": { "render": "build/bin/render.js" },
  "escalier": {
    "environments": {
      "lib/index.esc": ["window"],
      "bin/render.esc": ["dedicated_worker"]
    }
  }
}
```

Tooling reads the same field the compiler does, so completion inside a worker
entrypoint does not offer `document`. An import whose own entrypoint does not
cover the importer's environments is an F2 error, which is how a page-only
dependency is caught in a worker.


**F13. `addEventListener` and `removeEventListener` live in `web:core`.** They
are among the most used globals on the platform, and `web:core` already holds the
event model they belong to: `Event`, `EventTarget`, `EventListener`,
`EventListenerOptions` and `AddEventListenerOptions`. A separate package holding
two functions that name all of those across a boundary buys nothing.

Both are generic over each scope's own event map, so they arrive as
per-environment declarations under F5. That puts the event maps themselves in
`web:core`'s reach, and they are in `web:dom` and `web:worker` today, so the maps
move with the functions or `web:core` imports the packages holding them.

**F14. `web:core` is ambient rather than imported.** Its declarations are
injected into scope the way `std:prelude`'s are, so a program writes
`setTimeout(...)` and `Event` without naming a package. `import "web:core"` stays
legal as a redundant no-op, which is how the generated tree and existing code
migrate without a flag day.

This removes a special case rather than adding one. `web:core` already binds its
exports unprefixed, which `coreURI` exists to arrange in three places:
`internal/dts_to_esc/import_header.go:33`, the same file at line 222 where the
comment reads "The prelude and `web:core` are skipped, since both bind their
exports unprefixed", and `internal/solver/imports.go:105`. The code already
groups the two; this finishes it and the exception goes.

It also decides F6's ergonomics. F6 moves a global out of a scope class and into
a package, which on its own means a file that sets a timer has to import
`web:core` first — worse than the global it replaces. Ambient makes the hoisted
globals read like globals again.

**The boundary needs stating, because the obvious justification proves too
much.** `Document` and `Element` are as ambient at runtime as `Event` is, so
"ambient on the platform" would pull `web:dom` in too. What earns `web:core` the
prelude is that it is small and portable: 24 declarations, all on every
environment, against `std:prelude`'s existing 44. A package that is large, or
that only some environments have, stays an import however ambient the runtime
makes it.

**The condition is vacuous today and is written down anyway.** The intent is that
`web:core` is ambient when the target is a web platform, which every environment
in F10's vocabulary is. There is no non-web platform to contrast with until the
non-goals change, so the injection is unconditional for now. The platform axis
above the environment is what the condition would need, and it does not exist.

Two consequences to carry. `Event`, `DOMException` and `MessageEvent` are
plausible names in user code, and shadowing 24 more ambient names makes a
diagnostic such as "Event is not assignable to Event" reachable, so the message
has to distinguish the two. And `docs/03_imports.md` draws a line between
shape-loading, which is additive and never satisfies a named reference, and a
named reference, which requires the import; 24 declarations cross that line and
the text has to say so.

### 4.2 Non-functional

**N1. One fact, one place.** Where a declaration exists is a property of the
declaration. Nothing else may restate it. A package file name, a partition entry
and a directory layout must not carry an environment claim that can drift from the
declaration's own.

**N2. A package is importable as a unit.** Every declaration a package holds is
reachable from every environment that may import the package. Splitting a family
across packages is acceptable; a package no environment can fully use is not.
This is the structural invariant F4 rests on.

**N3. Claims are derived, not hand-maintained.** A per-declaration environment
comes from the source data. Hand-written tables are for what no source answers,
and each entry says why it exists.

**N4. Nothing regresses cold load.** Reorganization is measured against
`BenchmarkStdlibClosureLoad`. #1643 covers the separate finding that comment
attachment is 74% of cold load.

F14 complicates this. #1567 caches one prelude across runs, and F5 makes
`web:core`'s content differ by environment, since `location` is a `Location` or a
`WorkerLocation` and `onmessage` has three forms. So the prelude becomes nine
things keyed by environment, and the cache has to be keyed the same way. That is
work on top of #1567 and it lowers the hit rate a single shared prelude gets.

**N5. A hoisted global compiles to a bare reference.** Codegen emits
`importScripts(...)`, never a member access on a scope object, since no such
object is in scope.

**N6. The scope classes survive as types.** Event-handler signatures annotate
`this` with the scope class and event maps are keyed by it, so hoisting the
members does not make the classes removable.

**N7. New fixtures cover the new import style rather than migrated ones.** The
environment rules land in `internal/solver`, and the fixture suite that exercises
`internal/checker` is not being backported. So the fixtures for this work are
copies written against the new imports, and the existing ones keep running
unchanged against the old path.

**N8. The ambient name set is pinned.** F14 makes `web:core`'s membership decide
what every file has in scope, where today it decides which package holds a
declaration. A test asserts the set of ambient names, so adding one is a
deliberate edit rather than a side effect of moving a declaration.

### 4.3 Phases

The work lands in three phases, one environment family at a time: `window`, then
the workers, then the worklets. A phase is done when programs targeting that
family compile correctly and the requirements marked for it hold.

| requirement | window | workers | worklets |
| --- | --- | --- | --- |
| F1 target environment | ✅ | extend | extend |
| F2 unsatisfiable imports | — | ✅ | extend |
| F3 reference check | ✅ | extend | extend |
| F4 nameable where provided | partial | ✅ | ✅ |
| F5 per-environment declarations | — | ✅ | extend |
| F6 globals through a binding | ✅ | extend | extend |
| F7 hoisted forms | ✅ | extend | extend |
| F8 hoisted environments | ✅ | extend | extend |
| F9 no double hoist | ✅ | extend | extend |
| F10 vocabulary | ✅ | — | — |
| F11 worklets as targets | — | — | ✅ |
| F12 entrypoint environments in `package.json` | ✅ | extend | extend |
| F13 event listeners in `web:core` | ✅ | extend | extend |
| F14 `web:core` ambient | ✅ | extend | extend |
| N1 one fact, one place | ✅ | — | — |
| N2 importable as a unit | partial | ✅ | ✅ |
| N3 derived claims | ✅ | — | — |
| N4 cold load | ✅ | ✅ | ✅ |
| N5 bare reference | ✅ | extend | extend |
| N6 scope classes as types | ✅ | extend | extend |
| N7 new fixtures | ✅ | extend | extend |
| N8 ambient name set pinned | ✅ | — | — |

✅ lands in that phase. "extend" means the phase applies an existing mechanism to
more environments without changing it. "partial" means the phase does as much as
one family allows.

**#1648 is a phase-one prerequisite rather than a neighbour.** An interface with
several supertypes converts to a class with one, so `Element` keeps `Node` and
loses `querySelector`. The merged types this plan rests on cannot be written
until `implements` contributes members to a `declare` class.

Three of these are phase-one work for a reason worth stating.

**F10 lands whole in phase one, before any worklet is a target.** A declaration
that says `@env("window")` is making a claim about the other eight environments.
Without all nine names the claim cannot be written down, so the vocabulary has to
be complete even while only `window` is an accepted target.

**N3 lands with it.** `@env("window")` is only correct if something knows the
declaration is absent everywhere else, and `Exposed` is what knows. Ingesting
WebIDL is not a later refinement; phase one's annotations are wrong without it.

**F1 lands in phase one even though there is one target.** The target has to be
recorded and threaded through before F3 has anything to check against. What
phases is the set of accepted values, not the mechanism.

F2 and F5 have nothing to do in phase one. A single environment cannot
contradict itself, and no set of imports is unsatisfiable when every package a
window program can reach is a window package.

## 5. Non-goals

- Feature detection at runtime in user code, such as narrowing on
  `typeof OffscreenCanvas !== "undefined"`. Worth having, not needed for any
  requirement here.
- `typeof globalThis`. It appears seven times in the tree, five of them in
  `dom.window.esc`. F6 withholds the global object, so the intersections naming
  it lose their second half and the annotation goes. Nothing replaces it.
- Node, Deno and Bun. Widening the vocabulary past the browser is separate work.
  The worklets are not in this exclusion: they are browser global scopes and F10
  brings them in.
- Replacing TypeScript's libs as the conversion input. WebIDL is proposed below as
  a supplement for facts the libs do not carry, not as a replacement.

## 6. Open questions

1. **Does the file name survive?** Under N1 it cannot claim anything about the
   declarations inside. Either it is redefined as the package's importability,
   which is a real and distinct fact, or it goes and importability is derived
   from the declarations.
2. **How does a program declare its environment?** Answered by F1 and F12. Each
   entrypoint states its environments in `package.json`, `escalier.toml` is not
   consulted, and an entrypoint may state none, which claims every environment.
   Only the spelling of the `package.json` field is left.

3. **Per-environment declarations or per-arm annotations?** #1613 proposes
   annotating union arms. Emitting one declaration per environment is simpler and
   needs no new annotation granularity, but it makes name resolution
   environment-dependent and `EnvIndex` currently intersects over a duplicate
   name, which is the wrong operation for disjoint copies.
4. **Where does per-worker-kind data come from?** Answered. `@webref/idl`
   publishes the curated WebIDL with `[Exposed]` on every interface, and it is
   reachable from the build environment. `transferable.md` uses it. Adding it as
   a build-time input would also settle #1645. What remains is whether the
   `@env` vocabulary grows to match the twelve globals the IDL names, or the
   non-goals record the worklets as out of scope and an unmappable `Exposed`
   value is rejected rather than dropped.
5. **Is a type-only reference needed?** For `WindowProxy` in a portable union the
   answer looks like yes, since no split can make it portable. It is not needed
   for `OffscreenCanvas`, which a repartition reaches.

6. **Does `Window` get the same treatment?** Symmetry says yes, and it is a far
   larger change. `Window` and its mixins would add several hundred top-level
   declarations to whichever packages own them.
7. **Does `self` survive the hoist?** `WorkerGlobalScope.self` is the global
   object, and exposing it returns the program the handle F6 withholds. Dropping it
   removes an escape hatch that portable code sometimes wants.
8. **What is the `this` of a hoisted listener?** `addEventListener` annotates
   its callback with `this: DedicatedWorkerGlobalScope`, which stays true after
   the hoist even though the receiver is no longer nameable.

## 7. Related issues

| issue | relation |
| --- | --- |
| #1613 | per-arm availability for `MessageEvent.source` |
| #1614 | retiring the package-to-tier machinery, attempted in #1631 and still open |
| #1641, #1642 | per-environment source files |
| #1586 | the tier is per package where availability is per declaration |
| #1643 | comment attachment dominates cold load |
| #1644 | moving `MessagePort` and `Transferable` into `web:core` |
| #1645 | the converted tree declares constructors that throw |
| #1646 | a type guard on an alias to a class matches every value |
| #1861 | warm whole-tree inference regressed 1.5x, bisected to #1841 |
| #1862 | the closure benchmark silently measures fewer packages than it claims |

#1644's blocker section was wrong when filed and has been corrected. It claimed
that a `Transferable` in `web:core` naming `OffscreenCanvas` would fail the
environment check. The check it named no longer exists on `main`, and even in the
tree it was written against the claim did not hold, because an unannotated
declaration was available in every environment. The blocker is reachability: a
worker cannot import the packages the arms live in.
