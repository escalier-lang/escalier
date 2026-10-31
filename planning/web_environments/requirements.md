# Requirements: Which web APIs each environment has

## 0. Scope

This is one of two streams. It covers **where** a declaration exists: which
global scopes the platform gives it, how a program says which scope it targets,
and what the compiler does when a reference escapes it.

`planning/web_api_types/` covers **what** a declaration says, and owns the
WebIDL generator both streams read. Stage 1 there is this stream's prerequisite,
because an `@env("window")` is a claim about the other eight environments and
only `[Exposed]` knows it.

Both streams were one document until this split. Hoisting the globals out of the
`*GlobalScope` classes moved to the other folder; only the question of which
environments a hoisted member reaches stayed here.

## 1. History

An earlier environment system, the stack of PRs #1628 through #1635, was closed
without merging. `main` has none of it: no `@env` decorator and no annotations,
no `internal/dts_to_esc/env*.go`, no `CheckEnvs` or `DeclEnvs`, no environment
vocabulary, no file-name suffix convention, and no `web:worker` package. What it
has instead is the package-level tier rank that stack was replacing, described in
section 3.

So this is a greenfield design rather than a revision.

## 2. Problem statement

A web declaration exists on some set of global scopes. A page has the DOM, a
dedicated worker has a different surface, a shared worker a third, a service
worker a fourth, and the four worklets more. Nothing in the tree records which,
and the one mechanism that comes close measures a different thing.

The tier rank in section 3 answers how many runtimes ship a package, which is the
question of whether code runs off the browser at all. Every global scope inside a
browser is one tier, so the rank cannot distinguish them, and portability is a
total order where global scopes are not. Section 4 is what that costs.

## 3. What the tree does today

### 3.1 One rank per package, ordered by portability

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

### 3.2 The check is on import edges, and runs in the converter

`CheckTiers` at `internal/dts_to_esc/import_header.go:116` reports every import
edge whose target sits above its source. A portable package may name a core one
and not a browser one.

It has one caller, `internal/dts_to_esc/generate.go:138`. Like the system before
it, it validates the generated tree against itself at conversion time. The
compiler pipeline has no notion of a tier, so none of it reaches a user program.

### 3.3 What the axis measures

A tier answers "how many runtimes ship this", and the values order because that
is a nesting: language ⊃ core ⊃ portable ⊃ browser. The axis is the WinterTC
question, whether a package exists off the browser at all.

It says nothing about which global scope inside a browser has a declaration. A
page, a dedicated worker and a service worker are all "the browser", so all three
collapse into `TierBrowser` together.

## 4. Observed problems

### 4.1 The tier axis cannot express a global scope

Portability is a total order and global scopes are not. A page has `document`, a
dedicated worker has `importScripts`, a service worker has `clients`, and no two
of those three contain each other. There is no rank that puts them in sequence,
so the mechanism that works for Node-versus-browser cannot be extended to
window-versus-worker by adding values to it.

`web:workers` and `web:service_worker` are both `TierBrowser`, which is correct
about the browser and silent about everything this document is for.

### 4.2 A worker has no surface at all

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

### 4.3 Nothing enforces a tier on user code

`CheckTiers` has one caller and it is the converter. A program can import
`web:dom` and `web:workers` together, or import `web:dom` from code meant for a
worker, and nothing reports it.

### 4.4 A program has no target environment

Nothing records which runtime a program is compiled for. Any rule that resolves a
name differently per environment, or rejects an import, has nowhere to read the
answer from. `escalier.toml` holds a project name and `package.json` holds
`main` and `bin`; neither says where the code runs.

### 4.5 The partition cuts across environments

`webPackages` in `internal/dts_to_esc/partition.go` groups declarations by API
family, and environments cut across those families. `OffscreenCanvas`,
`ImageBitmap`, `MessagePort` and `Path2D` are in `web:dom` with `Document` and
`Element`, though the first four exist in every worker and the last two in none.
A package is therefore not usable as a unit by any environment but a page.

### 4.6 Availability is per package, where the facts are per declaration

`TierPortable`'s own doc comment says the promise is per package and not per
name, and #1586 measured the gap against Node 22: `web:file` is portable but
`FileList` and `FileReader` are not, and `web:performance` is portable but five
of its timing interfaces are not.

The same coarseness will bite harder on the environment axis, because the source
data is coarse too. `lib.webworker.d.ts` is one file covering dedicated, shared
and service workers, so it cannot distinguish them. WebIDL can, through
`[Exposed]` and `[Global=...]`, and `vocabulary.md` and `transferable.md` are
built from it.

### 4.7 Per-arm availability has no representation

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

### 4.8 Load cost is charged per closure but paid per tree

`BuildPackageClosure` puts every reachable package into one group, so any edge
into `web:dom` pulls the whole tree into every program's closure. Measured on
`main` at 4ae93f0b with `BenchmarkStdlibClosureLoad`:

| | warm | cold |
| --- | --- | --- |
| no imports | 0.45ms | 954ms |
| `web:fetch` alone | 0.36ms | 980ms |
| every package | 0.67ms | 4.6s |

Warm is not the constraint. The cross-run checker cache in
`internal/solver/stdlib_run_cache.go` keeps one loaded checker per stdlib tree
and forks it per run, so the whole tree costs 0.67ms of inference against 380ms
before that landed. Closure size no longer costs inference, and the argument
against an edge into `web:dom` has to be made on cold load alone.

Cold barely moves with closure size either. The `NoImports` case costs 954ms
against `OnePackage`'s 980ms, because `buildPackageGraph` parses every file in
the tree to read its import header whether or not the program reaches it. So
cold is a property of the tree's size rather than of what a program imports.
#1643 records that comment attachment is 74% of it, all of it in one linear scan
in `ast.(*nodeIndex).enclosing`, and it has not landed.

Two earlier readings of this section were wrong and are worth keeping as a
caution about the harness. A 46ms warm baseline came from a benchmark that
measured less than it claimed: `committedPackageURIs` derives a root URI by
trimming `.esc` from the file name, so `dom.window.esc` yields `web:dom.window`,
which matches no partition entry, and `reachable` skips a root it has no edges
for. That is #1862, still unfixed, and
`TestTheClosureIgnoresAnUnknownRoot` shows the skipping is deliberate in the
loader, so the fix belongs in the benchmark. A later reading then called the warm
cost a 1.5x regression and bisected it, which #1861 records. The cache has since
removed the figure that regressed and the bisect's commits are outside `main`'s
rewritten history, so that issue is closed as not planned.

## 5. Requirements

### 5.1 Functional

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

**F6. A hoisted member's environments come from the scope that declares it.**
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

**F7. The environment vocabulary matches the platform's globals.** `@env` names
the nine global scopes WebIDL declares with `[Global=...]`, and the two group
names those scopes answer to. An unannotated declaration means all nine, which
is `Exposed=*`. `vocabulary.md` in this folder has the names and what the change
costs.

Two consequences, both breaking. `worker` covers four environments rather than
three, because `RTCIdentityProviderGlobalScope` is
`[Global=(Worker, RTCIdentityProvider)]`. And an unannotated declaration claims
nine environments rather than four, so around 290 declarations that carry no
decorator today need one.

**F8. A worklet is a compilation target with packages of its own.** A paint
worklet is a module the browser loads, so it is a target in the same sense a
worker is, and F1 through F5 apply to it unchanged. The `web:*` packages cover
the worklet surface, which they do not today: the CSS Typed OM sits in
window-only `web:dom`, so a paint worklet cannot name `CSSUnitValue`.

**F9. A package states each entrypoint's environments in `package.json`.** The
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

F6 is what the other stream's hoisting reads. Several scope members share a name
across scopes and differ in type, so hoisting them yields several top-level
declarations of one name with disjoint environments, which is F5. `onmessage` is
a `MessageEvent` handler on `DedicatedWorkerGlobalScope` and an
`ExtendableMessageEvent` handler on `ServiceWorkerGlobalScope`. `location` is a
`WorkerLocation` in a worker and a `Location` in a page.

### 5.2 Non-functional

**N1. One fact, one place.** Where a declaration exists is a property of the
declaration. Nothing else may restate it. A package file name, a partition entry
and a directory layout must not carry an environment claim that can drift from the
declaration's own.

The tier rank is the live case. It answers a different question, how many
runtimes ship a package, but once `@env` lands the two overlap enough to
disagree, so one of them has to go or the division has to be stated. #1614 and
§2.5 of the plan carry it.

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

The other stream's ambient `web:core` complicates this. #1567 caches one prelude
across runs, and F5 makes `web:core`'s content differ by environment, since
`location` is a `Location` or a `WorkerLocation` and `onmessage` has three forms.
So the prelude becomes nine things keyed by environment, and the cache has to be
keyed the same way. That is work on top of #1567 and it lowers the hit rate a
single shared prelude gets. It is the one place the two streams' designs
interact beyond the shared generator.

**N5. New fixtures cover the new import style rather than migrated ones.** The
environment rules land in `internal/solver`, and the fixture suite that exercises
`internal/checker` is not being backported. So the fixtures for this work are
copies written against the new imports, and the existing ones keep running
unchanged against the old path.

### 5.3 Phases

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
| F6 hoisted member's environments | ✅ | extend | extend |
| F7 vocabulary | ✅ | — | — |
| F8 worklets as targets | — | — | ✅ |
| F9 entrypoint environments | ✅ | extend | extend |
| N1 one fact, one place | ✅ | — | — |
| N2 importable as a unit | partial | ✅ | ✅ |
| N3 derived claims | ✅ | — | — |
| N4 cold load | ✅ | ✅ | ✅ |
| N5 new fixtures | ✅ | extend | extend |

✅ lands in that phase. "extend" means the phase applies an existing mechanism to
more environments without changing it. "partial" means the phase does as much as
one family allows.

Three are phase-one work for a reason the table does not give.

**F7 lands whole in phase one, before any worklet is a target.** A declaration
that says `@env("window")` is making a claim about the other eight environments.
Without all nine names the claim cannot be written down, so the vocabulary has to
be complete even while only `window` is an accepted target.

**N3 lands with it,** which means the other stream's Stage 1 lands first.
`@env("window")` is only correct if something knows the declaration is absent
everywhere else, and `[Exposed]` is what knows.

**F1 lands in phase one even though there is one target.** The target has to be
recorded and threaded through before F3 has anything to check against. What
phases is the set of accepted values, not the mechanism.

F2 and F5 have nothing to do in phase one. A single environment cannot
contradict itself, and no set of imports is unsatisfiable when every package a
window program can reach is a window package.

## 6. Non-goals

- Improving what a declaration says. The other stream owns the conversion
  input, the constructors, the derived sets and the hoisting.
- Feature detection at runtime in user code, such as narrowing on
  `typeof OffscreenCanvas !== "undefined"`.
- Node, Deno and Bun. Widening the vocabulary past the browser is separate work.
  The worklets are not in this exclusion: they are browser global scopes and F7
  brings them in.

## 7. Open questions

1. **Per-environment declarations, per-member duplicates, or a merge?** Three
   mechanisms cover different cases and `../web_api_types/hoisted/README.md`
   works them through. A merge handles a strict subset, as `WorkerNavigator` is
   of `Navigator`. An accessor split handles a mutability difference, as
   `Location.hash` has. Only a type difference, as `onmessage` has, needs new
   machinery: the slot key in `internal/dts_to_esc/decl_key.go` has to carry
   environments, and a per-environment duplicate has to be distinguishable from
   an overload set.
2. **What is the `package.json` field called?** F9 sketches a shape and leaves
   the spelling open.
3. **Is a type-only reference needed?** For `WindowProxy` in a portable union the
   answer looks like yes, since no repartition can make it portable. It is not
   needed for `OffscreenCanvas`, which a repartition reaches.
4. **How is enforcement rolled out?** F3 says an error from the first release.
   The fixtures that exercise the old path are not being migrated, so the
   question is what happens to user code rather than to the suite.

## 8. Related issues

| issue | relation |
| --- | --- |
| #1613 | per-arm availability for `MessageEvent.source`, which F5 would close |
| #1614 | retiring the package-to-tier machinery, attempted in #1631 and still open |
| #1586 | the tier is per package where availability is per declaration |
| #1641, #1642 | per-environment source files |
| #1644 | moving `MessagePort` and `Transferable` into `web:core`, which is F4 in one family |
| #1861 | warm whole-tree inference regressed 1.5x. Closed, overtaken by the cross-run cache |
| #1862 | the closure benchmark silently measures fewer packages than it claims |

#1644's blocker section was wrong when filed and has been corrected. It claimed
that a `Transferable` in `web:core` naming `OffscreenCanvas` would fail the
environment check. The check it named no longer exists on `main`, and even in the
tree it was written against the claim did not hold. The blocker is reachability: a
worker cannot import the packages the arms live in.
