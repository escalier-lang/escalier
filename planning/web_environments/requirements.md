# Requirements: Web environments and package organization

## 1. Problem statement

A web declaration exists on some set of runtimes. A page has the DOM, a dedicated
worker has a different surface, and a service worker has a third. The tree states
that fact in several places, and the statements disagree with each other.

The disagreement surfaced while working the `web:worker` stack, PRs #1628 through
#1635. Each individual problem below looked local at the time. Together they say
the model needs restating rather than patching.

## 2. What the tree does today

### 2.1 Three ideas share one vocabulary

The names `window`, `dedicated_worker`, `shared_worker` and `service_worker` are
used for three different questions, and nothing keeps the answers aligned.

1. **Where a declaration exists.** `@env` on a declaration, absent meaning every
   environment.
2. **What a package file's name claims.** `web/dom.window.esc` against
   `web/fetch.esc`.
3. **Which packages a program may import.** Stated in prose in
   `docs/03_imports.md`, for example that a page cannot import `web:worker`.

Only the first is enforced.

### 2.2 How a declaration gets its environments

`declEnvsFrom` in `internal/dts_to_esc/env_table.go` answers in order:

1. `declEnvOverrides`, a hand-written map, currently empty.
2. `DeclEnvsFromLibs`, which reads the set of TypeScript lib files that declared
   the name.
3. `packageFileEnvs`, read from the package file's name.
4. Every environment.

The lib set answers first for anything the libs declared, which is nearly
everything. `AnnotateEnvs` then stamps `@env` on a declaration whose set is
narrower than every environment, and leaves an every-environment declaration
bare.

### 2.3 What the file name actually governs

The file name is a fallback, reached only for a package no lib contributed to. It
never enters the reference check. `CheckEnvs` and `checkModuleEnvs` read
`DeclEnvs`, which consults the declaration's own decorator and nothing else.

### 2.4 Where the check runs

`CheckEnvs` has one caller, `internal/dts_to_esc/generate.go:143`. It validates
the generated tree against itself at conversion time. The compiler pipeline has no
notion of an environment at all, so none of this reaches a user program.

## 3. Observed problems

### 3.1 A file name reads as a claim it does not make

`internal/interop/data/web/dom.window.esc` holds 1195 top-level declarations. 931
carry `@env("window")`. The remaining 264 carry nothing, which means every
environment.

`WindowOrWorkerGlobalScope` is one of them, and it is correct: both
`lib.dom.d.ts` and `lib.webworker.d.ts` declare it, as they do `DOMRectInit`,
`ImageBitmapOptions` and `EventSourceInit`. A reader who takes the `.window`
suffix as a statement about the file's contents is misled about 22% of them.

### 3.2 A worker cannot name the types it receives

A page transfers an `OffscreenCanvas` to a worker:

```
val offscreen = canvas.transferControlToOffscreen()
worker.postMessage({ canvas: offscreen }, [offscreen])
```

The worker receives a real `OffscreenCanvas` and draws on it. To type the handler
it must name `OffscreenCanvas`, which is declared at
`internal/interop/data/web/dom.window.esc:13419`, inside a package a worker is not
meant to import. The same holds for everything `getContext` returns:

```
getContext(self, contextId: "2d", options?: unknown) -> OffscreenCanvasRenderingContext2D | null,
getContext(self, contextId: "bitmaprenderer", options?: unknown) -> ImageBitmapRenderingContext | null,
getContext(self, contextId: "webgl", options?: unknown) -> webgl.WebGLRenderingContext | null,
```

`lib.webworker.d.ts` declares every one of those, along with `Path2D`,
`CanvasGradient`, `CanvasPattern`, `TextMetrics` and `ImageData`. The platform
supports the program. The packaging does not.

### 3.3 Nothing enforces package environments on user code

A worker can `import "web:dom"` today and it typechecks. The rule that forbids it
exists only as prose.

### 3.4 A program has no target environment

Nothing records which runtime a program is compiled for. Any rule that resolves a
name differently per environment, or rejects an import, has nowhere to read the
answer from.

### 3.5 The partition cuts across environments

`webPackages` in `internal/dts_to_esc/partition.go` groups declarations by API
family. Environments cut across those families, so a package is not importable as
a unit. `web:dom` mixes the 931 window-only declarations with the 264 portable
ones, and the canvas surface a worker needs sits among them.

### 3.6 The environment vocabulary is both finer and coarser than the platform

The tree distinguishes four environments where the platform has nine, and
`lib.webworker.d.ts` is a single file covering three of them, so it cannot
express the difference either. Every per-worker-kind fact in the repository comes
from a 24-entry hand-written table at `internal/dts_to_esc/env_table.go:91-121`.

`vocabulary.md` sets out the nine and what matching them costs. WebIDL answers
both halves: `[Exposed]` per interface, and `[Global=...]` for the scopes an
`Exposed` value resolves against.

This matters for `Transferable`, whose arms are not uniformly available. Six of
the sixteen transferable interfaces are `Exposed=(DedicatedWorker, Window)` and
are absent from shared and service workers. The two libs declare `Transferable`
identically, so the converter sees no difference and cannot derive the split.

`transferable.md` in this folder works the case through in full, against
`@webref/idl`.

### 3.7 Per-arm availability has no representation

`MessageEvent.source` is `WindowProxy | MessagePort | ServiceWorker` in a page,
`Client | ServiceWorker | MessagePort` in a service worker, and `null` elsewhere.
`internal/interop/overlay/web/core.replace.esc` retypes it to `null` and retypes
`ports` to `Array<unknown>`, which is the only expressible answer today and costs
every program the real types. #1613 tracks this.

`WindowProxy` is a further wrinkle. `lib.dom.d.ts:29419` declares it as
`type WindowProxy = Window`, a type alias, so no realm has a `WindowProxy`
binding. Narrowing it means testing `Window`, which a worker genuinely lacks.

### 3.8 Load cost is charged per closure but paid per tree

`BuildPackageClosure` puts every reachable package into one group, so any edge
into `web:dom` pulls the whole tree into every program's closure. Measured with
`BenchmarkStdlibClosureLoad` on the committed tree:

| | warm | cold |
| --- | --- | --- |
| `web:fetch` alone | 28ms | 850ms |
| every package | 46ms | 951ms |

Cold barely moves because `buildPackageGraph` parses every file in the tree to
read its import header, reached or not. Closure size costs about 18ms of warm
inference per run, not the seconds earlier estimates suggested.

### 3.9 A global is reachable only through a scope class

Escalier does not give a program the global object. Declarations are organized
into packages and imported under a namespace binding, so a page calls
`fetch(...)` after `import "web:fetch"` rather than reading it off a global.

The `*GlobalScope` classes do not follow that. Each one describes what a runtime
puts in scope, and the tree carries them as classes whose members a program has no
way to reach:

```
export declare class WorkerGlobalScope extends EventTarget {
    readonly location: WorkerLocation,
    readonly navigator: WorkerNavigator,
    onerror: (fn (this: WorkerGlobalScope, ev: dom.ErrorEvent) -> any) | null,
    readonly self: WorkerGlobalScope & typeof globalThis,
    importScripts(mut self, ...urls: mut Array<string | url.URL>) -> unknown,
    ...
}
```

A worker program cannot call `importScripts`, read `location`, or set
`onerror`. The declarations exist and describe the runtime correctly, and nothing
can name them.

Three members of `web:worker` are already hoisted by hand, `importScripts`,
`onrtctransform` and `fonts`, so the shape is established but applied
inconsistently. `web:fetch` shows the target form, a top-level declaration
carrying the global's own name:

```
@js("fetch")
export declare fn fetch(input: RequestInfo | url.URL, init?: RequestInit) -> Promise<Response>
```

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

**F12. Tooling resolves the same target environment the compiler does.** The
language server reads the target rather than assuming a page, so completion
inside a worker does not offer `document`. It resolves the target from the file
name, from the `package.json` of the package the file belongs to, or from
`escalier.toml` at the repository root, in that order of specificity.

**F13. The event-map machinery lives in `web:events`.** `addEventListener` and
`removeEventListener` are generic over each scope's own event map, so they
collide under one name when hoisted and need a package of their own. Whether the
rest of the event model moves there from `web:core` is open.

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
| F12 tooling reads the target | ✅ | extend | extend |
| F13 `web:events` | ✅ | extend | extend |
| N1 one fact, one place | ✅ | — | — |
| N2 importable as a unit | partial | ✅ | ✅ |
| N3 derived claims | ✅ | — | — |
| N4 cold load | ✅ | ✅ | ✅ |
| N5 bare reference | ✅ | extend | extend |
| N6 scope classes as types | ✅ | extend | extend |
| N7 new fixtures | ✅ | extend | extend |

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
2. **How does a program declare its environment?** Answered by F1 and F12. It
   is read from the file name, the package's `package.json`, or `escalier.toml`
   at the repository root, most specific first, and a program may declare none,
   which claims every environment. What remains is the spelling of the
   `package.json` and `escalier.toml` keys.

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
| #1614 | retiring the package-to-tier machinery, done in #1631 |
| #1641, #1642 | per-environment source files, of which the file-name rule is the stdlib half |
| #1643 | comment attachment dominates cold load |
| #1644 | moving `MessagePort` and `Transferable` into `web:core` |
| #1645 | the converted tree declares constructors that throw |
| #1646 | a type guard on an alias to a class matches every value |

#1644 was filed before the mechanism in section 2.3 was understood. Its stated
blocker, that a `Transferable` in `web:core` naming `OffscreenCanvas` would fail
`CheckEnvs`, does not hold, because `OffscreenCanvas` is unannotated and therefore
already on every environment. `worker.worker.esc:74` already names
`dom.Transferable` and the committed tree passes.
