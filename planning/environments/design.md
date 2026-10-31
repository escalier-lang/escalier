# Design: Which APIs each environment has

## 1. Scope

This is one of two streams. It covers **where** a declaration exists: which
environments provide it, how a program says which it targets, and what the
compiler does when a reference escapes it.

An environment is anywhere Escalier code runs. Two kinds exist. A browser global
scope, of which the platform declares nine, is one. A non-browser runtime, such
as Node, Deno or Bun, is the other. They differ in where the facts come from and
in nothing else: both answer whether a declaration is reachable, both are a
compilation target, and one `@env` vocabulary covers them.

The browser global scopes come first because the `web:*` tree is what exists to
annotate. The runtimes are phase four and the design holds for them unchanged.

`planning/web_api_types/` covers **what** a declaration says, and owns the
WebIDL generator this stream reads. Section 7.1 records what waits on it and
what does not.

Hoisting the globals out of the `*GlobalScope` classes belongs to that stream;
only the question of which environments a hoisted member reaches is here.

`vocabulary.md` beside this file has the nine global scopes and what matching
them costs. `transferable.md` works one API family through in full.

## 2. History

An earlier environment system, the stack of PRs #1628 through #1635, was closed
without merging. `main` has none of it: no `@env` decorator and no annotations,
no `internal/dts_to_esc/env*.go`, no `CheckEnvs` or `DeclEnvs`, no environment
vocabulary, no file-name suffix convention, and no `web:worker` package. What it
has instead is the package-level tier rank that stack was replacing, described in
section 4.

So this is a greenfield design rather than a revision.

## 3. Problem statement

A web declaration exists on some set of global scopes. A page has the DOM, a
dedicated worker has a different surface, a shared worker a third, a service
worker a fourth, and the four worklets more. Nothing in the tree records which,
and the one mechanism that comes close measures a different thing.

The tier rank in section 4 answers how many runtimes ship a package, which is the
question of whether code runs off the browser at all. Every global scope inside a
browser is one tier, so the rank cannot distinguish them, and portability is a
total order where global scopes are not. Section 5 is what that costs.

## 4. Current state

Reviewed against `main` at 4ae93f0b. Every premise this stream rests on is
unchanged, line numbers included.

### 4.1 One rank per package, ordered by portability

`internal/dts_to_esc/tier.go` gives each pseudo-package a `Tier`, and the four
values form a total order:

| tier | value | what it promises |
| --- | --- | --- |
| `TierLanguage` | -1 | wherever ECMAScript is. Every `std:*` package. |
| `TierCore` | 0 | every runtime implementing the WinterTC minimum common API. |
| `TierPortable` | 1 | every non-browser WinterTC runtime, and the browser. |
| `TierBrowser` | 2 | a browser alone. |

`webTiers` assigns one to each of the 24 `web:*` packages. The nine portable
ones are `web:fetch`, `web:url`, `web:streams`, `web:file`, `web:crypto`,
`web:performance`, `web:websocket`, `web:compression` and `web:wasm`. Ten are
browser, including `web:dom`, `web:workers`, `web:service_worker`,
`web:indexeddb` and `web:webgl`. `web:core` is the only core package.

Core and portable both promise availability off the browser, so the line between
them is not about where code runs. It is the WinterTC minimum common API, which
a runtime must implement to claim conformance, against the wider set every such
runtime implements in practice. What the ordering buys is one rule: `CheckTiers`
refuses an edge going up, so `web:core` may not name `web:fetch` while
`web:fetch` may name `web:core`. The base cannot come to depend on the layer
above it.

The portable promise is per package and already leaks. `tier.go`'s own comment
says so and gives the case: `web:performance` is portable and contains
`PerformanceTiming`, which Node 22 does not define. #1586 tracks tiering a
declaration rather than a package, which is what `@env` does.

The membership does not follow the boundary either. ECMA-429, the Minimum common
web API, was published in December 2025 by Ecma TC55, and WebAssembly is in it.
`web:wasm` is `TierPortable`, so a package inside the minimum set sits above the
rank named after it. Several of the other eight look the same way. That is one
more reason the axis is the wrong instrument rather than a mislabelled one, and
§7.3.3 retires it.

### 4.2 The check is on import edges, and runs in the converter

`CheckTiers` at `internal/dts_to_esc/import_header.go:116` reports every import
edge whose target sits above its source. A portable package may name a core one
and not a browser one.

It has one caller, `internal/dts_to_esc/generate.go:138`. Like the system before
it, it validates the generated tree against itself at conversion time. The
compiler pipeline has no notion of a tier, so none of it reaches a user program.

### 4.3 What the axis measures

A tier answers "how many runtimes ship this", and the values order because that
is a nesting: language ⊃ core ⊃ portable ⊃ browser. The axis is the WinterTC
question, whether a package exists off the browser at all.

It says nothing about which global scope inside a browser has a declaration. A
page, a dedicated worker and a service worker are all "the browser", so all three
collapse into `TierBrowser` together.

### 4.4 What the other stream changed

**An ambient scope landed, and a project's own files are losing it.**
`internal/solver/ambient.go` reproduces the JavaScript global object over 26
packages, 203 top-level names plus the namespaces it rebuilds from dotted `@js`
paths. The other stream's F7 keeps that for a converted `.d.ts` and reduces it
for a project's own `.esc` files to the `*:prelude` packages, renaming
`web:core` to `web:prelude` as part of it. Its §5.1 does that much.

What it leaves here is the gate. F7 makes `web:prelude` ambient for every
program, because deciding otherwise needs a target, so §7.3.2 is where a
prelude becomes ambient only for the environments its scheme covers. N4 carries
what the ambient half costs.

**`Window`'s globals were hoisted into `web:dom`.** 294 top-level
declarations, including portable ones such as `setTimeout` and `atob` that
belong in `web:core`. `web:dom` is browser-tier and a worker cannot import it,
so a worker cannot set a timer until §5.6 of the other stream moves them.

## 5. Observed problems

### 5.1 The tier axis cannot express a global scope

Portability is a total order and global scopes are not. A page has `document`, a
dedicated worker has `importScripts`, a service worker has `clients`, and no two
of those three contain each other. There is no rank that puts them in sequence,
so the mechanism that works for Node-versus-browser cannot be extended to
window-versus-worker by adding values to it.

`web:workers` and `web:service_worker` are both `TierBrowser`, which is correct
about the browser and silent about everything this document is for.

### 5.2 A worker has no surface at all

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

### 5.3 Nothing enforces a tier on user code

`CheckTiers` has one caller and it is the converter. A program can import
`web:dom` and `web:workers` together, or import `web:dom` from code meant for a
worker, and nothing reports it.

### 5.4 A program has no target environment

Nothing records which runtime a program is compiled for. Any rule that resolves a
name differently per environment, or rejects an import, has nowhere to read the
answer from. `escalier.toml` holds a project name and `package.json` holds
`main` and `bin`; neither says where the code runs.

### 5.5 The partition cuts across environments

`webPackages` in `internal/dts_to_esc/partition.go` groups declarations by API
family, and environments cut across those families. `OffscreenCanvas`,
`ImageBitmap`, `MessagePort` and `Path2D` are in `web:dom` with `Document` and
`Element`, though the first four exist in every worker and the last two in none.
A package is therefore not usable as a unit by any environment but a page.

### 5.6 Availability is per package, where the facts are per declaration

`TierPortable`'s own doc comment says the promise is per package and not per
name, and #1586 measured the gap against Node 22: `web:file` is portable but
`FileList` and `FileReader` are not, and `web:performance` is portable but five
of its timing interfaces are not.

The same coarseness will bite harder on the environment axis, because the source
data is coarse too. `lib.webworker.d.ts` is one file covering dedicated, shared
and service workers, so it cannot distinguish them. WebIDL can, through
`[Exposed]` and `[Global=...]`, and `vocabulary.md` and `transferable.md` are
built from it.

### 5.7 Per-arm availability has no representation

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

### 5.8 Load cost is charged per closure but paid per tree

`BuildPackageClosure` puts every reachable package into one group, so any edge
into `web:dom` pulls the whole tree into every program's closure. Measured on
`main` at 4ae93f0b with `BenchmarkStdlibClosureLoad`:

| | warm | cold, recorded | cold, re-measured |
| --- | --- | --- | --- |
| no imports | 0.45ms | 954ms | 1015ms |
| `web:fetch` alone | 0.36ms | 980ms | 987ms |
| every package | 0.67ms | 4.6s | 5.3s and 6.4s |

**The every-package figure is under re-measurement and the recorded one is too
low.** Two runs on one container, at `-benchtime 3x` and `6x`, gave 6.4s and
5.3s against the 4.6s recorded, with warm at 1.12ms and 0.71ms against 0.67ms.
No commit has touched `internal/interop/data/` since 4ae93f0b, so the tree is
byte-identical and this is not growth, and the two smaller cases match within
noise, so it is not the machine. Cold varies by about a fifth between runs,
which a figure quoted to two significant digits hides.

Warm is not the constraint. The cross-run checker cache in
`internal/solver/stdlib_run_cache.go` keeps one loaded checker per stdlib tree
and forks it per run, so the whole tree costs 0.67ms of inference against 380ms
before that landed. Closure size no longer costs inference, and the argument
against an edge into `web:dom` has to be made on cold load alone.

**Whether cold moves with closure size is open, and this section answered it
wrongly.** It concluded that cold is a property of the tree's size rather than
of what a program imports, drawing that from `NoImports` at 954ms against
`OnePackage`'s 980ms. Those two agree because `buildPackageGraph` parses every
file's import header either way and both closures are small. Generalising from
them was wrong: every package costs five to six times no imports, which a
tree-size-bound cost could not do. Cold looks instead like a per-tree parse
floor of about a second plus the inference of whatever closure is loaded.

The measurement that would settle it is `web:dom` alone, whose closure is 1193
declarations, against `web:streams` alone, whose closure is 47. If cold tracks
the closure, the first costs most of the every-package figure and the second
costs near the floor. That is not run here.

It matters beyond this section. An edge from an ambient package into the
platform would make every program's closure the whole tree, so every program
would pay the every-package figure rather than the no-imports one. §7.4.5 keeps
the messaging family out of `web:prelude` on cohesion grounds, and this would be
a second reason.

#1643 records that comment attachment is 74% of cold load, all of it in one
linear scan in `ast.(*nodeIndex).enclosing`, and it has not landed.

Two earlier readings of this section were wrong and are worth keeping as a
caution about the harness. A 46ms warm baseline came from a benchmark that
measured less than it claimed, because `committedPackageURIs` derives a root URI
by trimming `.esc` from the file name. `dom.window.esc` yielded
`web:dom.window`, which matched no partition entry, and `reachable` skips a root
it has no edges for. That is #1862, and its symptom is moot on `main`, where no
committed file name carries a suffix any more and the benchmark does name all 48
packages. `TestTheClosureIgnoresAnUnknownRoot` shows the skipping is deliberate
in the loader, so the guard the benchmark lacks is still worth having.

A later reading then called the warm cost a 1.5x regression and bisected it,
which #1861 records. The cache has since removed the figure that regressed and
the bisect's commits are outside `main`'s rewritten history, so that issue is
closed as not planned.

## 6. Requirements

### 6.1 Functional

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

**F4. A declaration is nameable in the sorts its environment provides.** A worker
can name an `OffscreenCanvas` it receives, and
everything reachable from it, without importing declarations that environment
lacks.

A class is two bindings, a type and a value, and an environment can provide one
without the other. A worker holding a `MessageEvent` needs `WindowProxy` as a
type to say what `source` is, and must not get it as a value, since
`Exposed=Window` means no such global is there to name. Open question 3 has what
that costs.

**F5. A declaration that differs by environment is expressible per environment.**
Where environments genuinely disagree, as with `MessageEvent.source`, the tree
states each environment's form rather than narrowing every environment to their
intersection.

`addEventListener` is not one of these cases, though it looks like one. It is
generic over its scope's own event map, so the nine scopes disagree about its
type, but it stays a method on `EventTarget` rather than hoisting to a global,
so each scope class carries its own overloads and no two declarations contend
for one name. The other stream's F4 and F5 have the reasoning.

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

**F7. The vocabulary covers every browser global scope.** `@env` names the nine
global scopes WebIDL declares with `[Global=...]`, and the two group names those
scopes answer to. An unannotated declaration means all nine, which
is `Exposed=*`. `vocabulary.md` has the names and what the change
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

**F10. A non-browser runtime is an environment.** `node`, `deno` and `bun` join
the vocabulary and are compilation targets, and F1 through F6 and F9 apply to
them as written. An environment is where code runs, and a runtime is as much
that as a global scope is.

This is what lets the tier axis retire rather than be renamed. A tier answers
whether a package exists off the browser at all, which no browser global scope
can say. §7.3.3 removes `tier.go` without waiting for an answer, because the
tier check was a converter guard rail whose only reader was itself, and F10 is
where the claim comes back on the right unit.
A runtime environment is the home: a declaration available in Node carries
`@env("node")`, and the portability a tier approximated per package becomes a
per-declaration fact. N1 is then satisfied by removal.

The `node:` scheme becomes live with it. `closureSchemes` in
`internal/solver/stdlib_closure.go` reserves it and populates it with nothing,
which is the right shape for a package set a runtime environment makes
reachable.

What does not carry over is the source. `[Exposed]` is a WebIDL attribute and
says nothing about Node, so N3's requirement that a claim be derived needs a
different answer per runtime. Open question 6 is where that sits.

### 6.2 Non-functional

**N1. One fact, one place.** Where a declaration exists is a property of the
declaration. Nothing else may restate it. A package file name, a partition entry
and a directory layout must not carry an environment claim that can drift from the
declaration's own.

The tier rank is the live case. It answers a different question, how many
runtimes ship a package, but once `@env` lands the two overlap enough to
disagree, so one of them has to go. #1614 and §7.3.3 carry it.

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

The other stream's F7 makes `web:prelude` ambient, and #1567 caches one prelude
across runs, so the question is whether the cache needs a per-target key. It
does not, and §7.4.5 is why.

Nothing left in `web:prelude` differs by environment. Its 22 declarations are
the event model, the abort pair, `DOMException` and the text codecs, every one
of them `Exposed=*`, so one cached prelude serves every target. The declarations
that do differ, `location` and `onmessage` among them, are scope members under
F4 and live in `web:window` and `web:worker`. `MessageEvent` was the one
exception, and §7.4.5 moves it to `web:messaging`.

What is left to measure is the per-environment content itself. A tree carrying
nine forms of a declaration is a larger tree, and section 8 says cold load tracks
tree size rather than closure size.

**N5. New fixtures cover the new import style rather than migrated ones.** The
environment rules land in `internal/solver`, and the fixture suite that exercises
`internal/checker` is not being backported. So the fixtures for this work are
copies written against the new imports, and the existing ones keep running
unchanged against the old path.

## 7. Plan

A stage lands on its own and leaves the tree green.

The stages are tracked in five milestones, which do not line up with the phases
in two places:

| milestone | stages | issues | tracking |
| --- | --- | --- | --- |
| [Environments - A: Environment vocabulary and targets](https://github.com/escalier-lang/escalier/milestone/14) | 7.3.1, 7.3.2, 7.3.4, 7.3.5, 7.4.2 | #1613, #1642, #1641, #2396, #2397, #2398 | #2417 |
| [Environments - B: Retire the package tier axis](https://github.com/escalier-lang/escalier/milestone/15) | 7.3.3 | #1614, #1586 | #2418 |
| [Environments - C: Worker environments](https://github.com/escalier-lang/escalier/milestone/16) | 7.4.1, 7.4.3 to 7.4.8 | #2399, #2400, #2401, #1644, #2402, #2403, #2404, #2405 | #2419 |
| [Environments - D: Worklet environments](https://github.com/escalier-lang/escalier/milestone/17) | 7.5.1 to 7.5.4 | #2406, #2407, #2408, #2409 | #2420 |
| [Environments - E: Non-browser runtimes](https://github.com/escalier-lang/escalier/milestone/18) | 7.6.1 to 7.6.4 | #2410, #2411, #2412, #2413 | #2421 |

Each tracking issue holds the dependency graph for its milestone as a mermaid
diagram, and says which orderings are forced and why.

§7.4.2's slot key sits in milestone A rather than with the workers, because the
worklet and runtime milestones need it too and nothing in phase two is its only
caller. §7.3.3 is a milestone of its own because it is a `Task`-type cleanup
with its own issue, and isolating it means reverting it does not drag the
vocabulary back.

Milestone A waits on §5.2 of the other stream. After it, C, D and E are parallel:
the worklets and the runtimes need the vocabulary and the slot key, not the
worker repartition. Within C the order is forced, since §7.4.3 precedes §7.4.4,
which precedes §7.4.5, which precedes §7.4.7. #2378 is in no milestone and
blocks any fixture importing `web:dom` or `web:window`.

A stage that regenerates the tree produces a large diff that needs no
line-by-line review. The hand-written part is the generator and the fixture, and
`check_generated_tree` is what validates the rest. §7.3.1 annotates roughly 290
declarations this way, and §7.4.1 adds a whole package.

### 7.1 Prerequisite, in the other stream

Section 5.2 of `../web_api_types/design.md` adds `@webref/idl` and emits a
checked-in Go table carrying `[Exposed]` per interface and `[Global=...]` per
global scope.

§7.3.1 cannot start before it. An `@env("window")` is a claim about the other
eight environments, and `[Exposed]` is the only thing that knows whether the
claim is true.

The rest of phase one does not wait. §7.3.2 reads a `package.json` field, §7.3.3
checks references against whatever the annotations say, and §7.3.4 is tooling
over
§7.3.2's answer, so all three can be built against a hand-written annotation set
and switched to the generated one when it lands.

### 7.2 What each phase carries

The work lands in four phases, one environment family at a time: `window`, then
the workers, then the worklets, then the non-browser runtimes. A phase is done
when programs targeting that family compile correctly and the requirements
marked for it hold.

| requirement | window | workers | worklets | runtimes |
| --- | --- | --- | --- | --- |
| F1 target environment | ✅ | extend | extend | extend |
| F2 unsatisfiable imports | — | ✅ | extend | extend |
| F3 reference check | ✅ | extend | extend | extend |
| F4 nameable where provided | partial | ✅ | ✅ | ✅ |
| F5 per-environment declarations | — | ✅ | extend | extend |
| F6 hoisted member's environments | ✅ | extend | extend | extend |
| F7 vocabulary | ✅ | — | — | — |
| F8 worklets as targets | — | — | ✅ | — |
| F9 entrypoint environments | ✅ | extend | extend | extend |
| F10 runtimes as environments | — | — | — | ✅ |
| N1 one fact, one place | ✅ | — | — | extend |
| N2 importable as a unit | partial | ✅ | ✅ | ✅ |
| N3 derived claims | ✅ | — | — | extend |
| N4 cold load | ✅ | ✅ | ✅ | ✅ |
| N5 new fixtures | ✅ | extend | extend | extend |

✅ lands in that phase. "extend" means the phase applies an existing mechanism to
more environments without changing it. "partial" means the phase does as much as
one family allows.

Three are phase-one work for a reason the table does not give.

**F7 lands whole in phase one, before any worklet is a target.** A declaration
that says `@env("window")` is making a claim about the other eight environments.
Without all nine names the claim cannot be written down, so the vocabulary has to
be complete even while only `window` is an accepted target.

**N3 lands with it,** which means §5.2 of the other stream lands first.
`@env("window")` is only correct if something knows the declaration is absent
everywhere else, and `[Exposed]` is what knows.

**F1 lands in phase one even though there is one target.** The target has to be
recorded and threaded through before F3 has anything to check against. What
phases is the set of accepted values, not the mechanism.

F2 and F5 have nothing to do in phase one. A single environment cannot
contradict itself, and no set of imports is unsatisfiable when every package a
window program can reach is a window package.

### 7.3 Phase one: window

#### 7.3.1 The vocabulary and the annotations

Build `@env` with all nine names from F7 and both groups, and annotate the tree
from the generated table. F7, N3, N1.

The vocabulary is complete here even though only `window` is an accepted target.
`worker` covers four environments, not three, since
`RTCIdentityProviderGlobalScope` answers to it.

**Done when** the regenerated tree carries an `@env` derived from `Exposed` on
every declaration that needs one, roughly 290 more than a four-name vocabulary
would produce, and `check_generated_tree` passes.

#### 7.3.2 The target environment

Read each entrypoint's environments from `package.json`, both the `lib/` one and
each `bin/` one, and settle the field's spelling. Thread the result through to
the checker. F1, F9.

An entrypoint may state none, which claims every environment and reaches the
intersection of what they provide. That intersection is empty for a declaration
that differs by environment, so a no-target program cannot use `location` at
all. That is the intended answer.

The target also decides which prelude is ambient, so the rule lands here: a
`*:prelude` package is ambient when the program's environments are ones its
scheme can run on. `web:prelude` passes that for every environment, since its
24 declarations are the portable core Node, Deno and Bun implement as well as
the browser, so this stage changes nothing about it in practice. The rule is
built now because §7.6 is where it first refuses something, when `node:prelude`
must not reach a browser program.

What a prelude holds is still gated per declaration by `@env` and F3. An
ambient package decides what a file can name; the reference check decides what
the target provides.

**Done when** a fixture declares a window entrypoint, the checker reports what
it resolved, and `web:prelude` is ambient for it through the scheme rule rather
than by being hard-coded.

#### 7.3.3 Retire the tier machinery

N1 and #1614, as soon as §7.3.1 makes it possible.

The tier axis is a converter guard rail, not a user-facing check. `CheckTiers`
has one caller, `generate.go:138`, which fails the conversion when an import
edge goes up a rank. Nothing in `internal/solver` or the compiler pipeline
consults a tier, so no program is ever checked against one, and `TierOf` is used
only by `CheckTiers` itself.

So replacing it does not wait on a target or on F10. Once every declaration
carries `@env`, the converter can check each reference instead of each package
edge: a declaration may name another only where the second is available in every
environment the first claims. That is the same guard on a better unit, and it
catches what the per-package promise cannot. `web:performance` is portable and
holds `PerformanceTiming`, which Node 22 does not define, and the tier check
passes it.

What this gives up until §7.6.4 is the off-browser claim. An
`@env("window", "worker")` declaration may or may not exist in Node and nothing
records which. Nothing consumes that answer today either, since `CheckTiers` was
its only reader, so removing the question and its reader together loses nothing
a program can observe.

1. Add the per-reference check over `@env`, reporting the same shape
   `TierViolation` does.
2. Delete `tier.go`, `CheckTiers`, `TierOf` and `webTiers`, and the
   `generate.go` call.
3. Close #1614.

**Done when** a converted tree with a reference outside the referrer's
environments fails the conversion, `tier.go` is gone, and
`check_generated_tree` passes.

**Not this stage.** Off-browser availability, which has no vocabulary until F10.

#### 7.3.4 The reference check

Run the check against the program's target, reporting a reference to a
declaration absent from it. An error from this release, not a warning. It covers
a value reference in any position, so matching `x is OffscreenCanvas` against an
environment without it is an error rather than a guard that never fires. F3.

Keep the tier check, which answers a different question until #1614 retires it.

**Done when** a new fixture referencing a worker-only declaration from a window
program fails with the full diagnostic, and the committed tree still converts
clean.

#### 7.3.5 Tooling

The language server resolves the target from the same `package.json` field and
filters completion by it. F9.

**Done when** completion in a window entrypoint offers `document` and completion
in an entrypoint declared for a worker does not.

#### Not this phase

No repartitioning, so a declaration may still sit in a package the wrong
environments can import. No per-environment declarations: one environment cannot
contradict itself. No unsatisfiable-import check, since every package a window
program reaches is reachable from a window.

### 7.4 Phase two: workers

**The phase is done when** the worker program in section 5.2 compiles: it
receives an `OffscreenCanvas` over `postMessage` and draws on it without
importing `web:dom`. Each stage below lands on its own and is checked on its
own; this is what they add up to.

#### 7.4.1 Ingest the worker surface

The four `*GlobalScope` classes, `importScripts`, `WorkerLocation`,
`WorkerNavigator`, the service-worker events and `Clients` are not in the tree.
Partition them into `web:worker` and regenerate.

The name follows from the binding, since the other stream's F4 makes a scope
package's binding the scope object and the binding is `worker`. `web:workers`
keeps its name and its contents, `Worker`, `SharedWorker`, `AbstractWorker` and
`WorkerOptions`. The two are a letter apart and hold different kinds of thing,
one the worker's view of itself and the other a page's API for starting one.
That is accepted for now, and open question 5 says what a later rename costs.

The receiverless globals then hoist by the other stream's mechanism, carrying
the `@env` F6 derives from the declaring scope. `postMessage`, `onmessage` and
`close` are operations on the scope, so F4 keeps them members and they are
reached through the binding.

**Done when** a worker fixture calls `importScripts` and reads `location` as a
`WorkerLocation`.

#### 7.4.2 Make the slot key carry environments

F5's machinery, landed before anything is split by environment.

The slot key in `internal/dts_to_esc/decl_key.go` has to distinguish a
per-environment duplicate from an overload set. Whatever name index §7.3.1
builds must not intersect over a duplicate name: the closed stack's `EnvIndex`
did, which is right for the interface and `declare var` pair that share a name
and wrong for disjoint per-environment copies, where it yields the empty set.
That index does not exist on `main`, so this is a constraint on §7.3.1's design
rather than a change to existing code.

Open question 1 in section 10 has the three mechanisms and which one needs this.

**Done when** two declarations of one name with disjoint `@env` convert, resolve
per environment, and are not read as an overload set.

#### 7.4.3 Split the canvas back-references

F5 applied to the two declarations that keep `web:canvas` out of a worker's
reach. `hoisted/scripts/closure.mjs` says the proposed package closes over 1193
declarations, the whole platform, and both paths are back-references to
window-only element types:

```
ImageBitmapRenderingContext -> HTMLCanvasElement -> HTMLElement
  -> Element -> Document
```

`ImageBitmapRenderingContext.canvas` splits in two. A worker's context is only
ever bound to an `OffscreenCanvas`:

```
@env("window")  readonly canvas: HTMLCanvasElement | OffscreenCanvas
@env("worker")  readonly canvas: OffscreenCanvas
```

`CanvasImageSource`, which the 2D-context mixins take for `drawImage`, splits in
three, because its arms do not share one exposure. `HTMLImageElement`,
`SVGImageElement`, `HTMLVideoElement` and `HTMLCanvasElement` are
`Exposed=Window`, `ImageBitmap` and `OffscreenCanvas` are `Window, Worker`, and
`VideoFrame` is `Window, DedicatedWorker`:

```
@env("window")
  = HTMLOrSVGImageElement | HTMLVideoElement | HTMLCanvasElement
  | ImageBitmap | OffscreenCanvas | web_codecs.VideoFrame
@env("dedicated_worker")
  = ImageBitmap | OffscreenCanvas | web_codecs.VideoFrame
@env("shared_worker", "service_worker")
  = ImageBitmap | OffscreenCanvas
```

Three forms of one name is the shape F5 already cites for `onmessage`, so this
needs no mechanism §7.4.2 does not provide. N6 of the other stream forbids
narrowing either declaration for every environment instead, since a page really
can draw an `HTMLVideoElement`.

**Done when** a dedicated-worker fixture draws a `VideoFrame` and is refused an
`HTMLImageElement`, a service-worker fixture is refused both, a window program
still draws an `HTMLImageElement`, and the canvas surface closes over no
window-only declaration for a worker target.

#### 7.4.4 Add `web:canvas`

F4 and N2 for the canvas surface, which is the half of #1644 a new package
answers.

`OffscreenCanvas`, `OffscreenCanvasRenderingContext2D`,
`ImageBitmapRenderingContext`, `ImageBitmap`, `ImageData`, `Path2D`,
`CanvasGradient`, `CanvasPattern`, `TextMetrics` and the 2D-context mixins are
worker-reachable and sit in `web:dom`, which a worker cannot import.

§7.4.3 is what makes the package importable as a unit. Without those two splits
the surface closes over the whole platform, so this stage follows it rather than
opening phase two.

Three edges remain and all are worker-reachable, so N2 holds: `web:webgl` for
the contexts `OffscreenCanvas.getContext` returns, `web:web_codecs` for the
`VideoFrame` arm of `CanvasImageSource`, and `web:file` for the `Blob` that
`convertToBlob` yields.

`createImageBitmap` comes too. `ImageBitmap` has no constructor, so that
function is the only way to make one, and leaving it in `web:dom` would give a
worker a type it cannot instantiate. `hoisted/canvas.esc` in the other stream
already routes it here.

**Done when** a worker fixture names `OffscreenCanvas` and draws on it without
importing `web:dom`.

#### 7.4.5 Add `web:messaging`

The other half of #1644. `MessageEvent`, `MessageEventInit`, `MessagePort`,
`MessageChannel`, `StructuredSerializeOptions` and `Transferable` go to one
package of their own, which is the structured-clone and channel-messaging
family.

The reason is cohesion, not cost. These six are one API: a port pair carries
structured clones, `Transferable` says what may be transferred with them, and
`MessageEvent` is what arrives. `web:prelude` is the event model, the abort
pair, `DOMException` and the text codecs, and messaging is a family beside
those rather than part of them. A package naming other packages is ordinary, and
a cycle among pseudo-packages is allowed, so neither is an argument here.

`MessageEvent` moves with them for the same reason, and that is a change from
leaving it in the prelude. Its `ports` is `Array<MessagePort>` once N6 of the
other stream removes the overlay retype, so it belongs with the port it carries.

`MessageChannel` comes for the reason `createImageBitmap` goes with
`ImageBitmap`: `MessagePort` has no constructor, and `new MessageChannel()` is
how a worker gets a pair of them.

`structuredClone` lands here too. Its options bag is
`StructuredSerializeOptions`, so §5.6 of the other stream defers it to this
stage rather than sending it to the prelude with the other portable globals.

One ordering constraint, and it is the tier check rather than the closure.
One ordering constraint, and the reason is F4 rather than the tier check.
`Transferable` names the canvas types, so §7.4.4 lands first or a worker can
name an arm it cannot import.

**Done when** a worker fixture receives a `MessagePort` and reads a
`MessageEvent` after `import "web:messaging"` and without importing `web:dom`,
and the generated tree converts clean.

#### 7.4.6 Move the transferable arms no worker can import

The nine arms `transferable.md` lists. Six are
`Exposed=(DedicatedWorker, Window)` and need a package importable from a page
and a dedicated worker, which no current package is, so this stage decides that
package as well as doing the move.

**Done when** `transferable.md`'s table has no row a worker can name but not
import.

#### 7.4.7 Split the declarations that differ by environment

F5 applied, and the end of #1613. `Transferable` splits into sixteen arms for a
page or a dedicated worker and ten for the rest.

This is also what satisfies the other stream's N6, which forbids typing a read
narrower than some environment provides. `MessageEvent.source` is `null` in
`web:core` today, where a browser yields a `WindowProxy`, a `MessagePort` or a
`ServiceWorker`, so the retype goes and the per-environment forms replace it in
whichever package §7.4.5 left `MessageEvent` in.

**Done when** a service worker sees `source` as
`Client | ServiceWorker | MessagePort | null`, a page sees
`WindowProxy | MessagePort | ServiceWorker | null`, and the retypes leave
`internal/interop/overlay/web/core.replace.esc`.

#### 7.4.8 Turn on the unsatisfiable-import check

F2. The check now has something to catch: a page importing `web:worker`, or a
dependency whose entrypoint does not cover a worker.

**Done when** a fixture importing two packages no single environment provides
fails with the full diagnostic.

### 7.5 Phase three: worklets

F8, and F1 through F5 applied unchanged. Phases one and two built every
mechanism this phase needs, so the work is vocabulary and partitioning.

#### 7.5.1 Accept the worklet targets

Add `audio_worklet`, `paint_worklet`, `layout_worklet` and `animation_worklet`
to the set of accepted targets, which §7.3.1 already put in the vocabulary.

**Done when** a fixture declares a paint-worklet entrypoint and the checker
reports the target it resolved.

#### 7.5.2 Partition the CSS Typed OM

The bulk of the phase. 36 interfaces that `LayoutWorklet` and `PaintWorklet`
share with `Window` and `Worker` sit in `web:dom` today, where no worklet can
reach them.

**Done when** a worklet fixture names `CSSUnitValue` without importing
`web:dom`.

#### 7.5.3 Add each worklet's own machinery

`AudioWorkletProcessor`, `PaintRenderingContext2D`, `LayoutFragment` and
`IntrinsicSizes`, none of which is in the tree.

**Done when** a paint-worklet fixture names `PaintRenderingContext2D`.

#### 7.5.4 Ingest the worklet scope classes

The four `*WorkletGlobalScope` classes. Their receiverless globals hoist by the
other stream's mechanism, and each worklet package's binding points at its scope
object, so the rest is reached as `worklet.<member>`.

**Done when** a paint-worklet fixture reads a member off `worklet` and is
refused `document`.

### 7.6 Phase four: runtimes

F10, and F1 through F6 and F9 applied unchanged. This is where N1 is satisfied
and the tier axis goes.

#### 7.6.1 Settle where a runtime's facts come from

Open question 6. `[Exposed]` says nothing about Node, so each runtime needs a
source N3 accepts, and the answer shapes everything after it.

**Done when** the source is named, pinned, and generates a table the way §7.3.1's
does.

#### 7.6.2 Accept the runtime targets

Add `node`, `deno` and `bun` to the vocabulary and to the accepted targets.

**Done when** a fixture declares a Node entrypoint and the checker reports the
target it resolved.

#### 7.6.3 Populate the `node:` scheme

`closureSchemes` in `internal/solver/stdlib_closure.go` reserves `node:` and
populates it with nothing. The packages a Node program reaches go there, and the
reference check refuses them to a window target.

**Done when** a Node fixture imports a `node:` package and a window fixture
naming the same declaration fails.

#### 7.6.4 State off-browser availability per declaration

What §7.3.2 deferred. `@env`'s browser scopes cannot say whether a declaration
exists in Node, and the tier rank that approximated it per package is gone by
then, so this is where the claim returns on the right unit.

**Done when** a declaration available in Node carries `@env("node")`, the
reference check refuses one that does not to a Node target, and
`check_generated_tree` passes.

## 8. Measurement

N4, and the figures have inverted since this was written.

The cross-run checker cache in `internal/solver/stdlib_run_cache.go` keeps one
loaded checker per stdlib tree and forks it per run, so warm cost has collapsed:

| | warm | cold, recorded | cold, re-measured |
| --- | --- | --- | --- |
| no imports | 0.45ms | 954ms | 1015ms |
| `web:fetch` alone | 0.36ms | 980ms | 987ms |
| every package | 0.67ms | 4.6s | 5.3s and 6.4s |

Warm was the constraint on repartitioning at 380ms for every package. At 0.67ms
it is not a constraint at all, so **closure size no longer costs inference** and
the argument against an edge into `web:dom` has to be made on cold load alone.

Cold barely moves with closure size either: the `NoImports` case costs 954ms
against `OnePackage`'s 980ms, because `buildPackageGraph` parses every file in
the tree to read its import header whether or not the program reaches it. So
cold is a property of the tree's size, not of what a program imports, and #1643
is the lever on it.

Two consequences for this plan. The repartition stages, §7.4.4 through §7.4.6,
no longer wait on #1861, which is closed
as not planned: it measured the warm figure the cache removed, and its bisect
hashes are outside `main`'s rewritten history. And #1862 is still live and
unfixed, so a per-environment file landing in phase two would silently shrink
what the benchmark measures.

## 9. Testing

New fixtures throughout, not migrated ones. N5. The rules land in
`internal/solver`, and the suite exercising `internal/checker` is not being
backported.

## 10. Open questions

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
3. **Is a type-only reference needed?** *Decided: yes, and `WindowProxy` is the
   case that needs it.* A declaration's type and value components are separate
   bindings, and an environment can provide one without the other. Where it
   provides only the type, that is what the tree gives it.

   `WindowProxy` is `Exposed=Window`. A worker receives a `MessageEvent` whose
   `source` may be one, so it needs the type to annotate what it holds, and the
   global does not exist there, so a value binding would name something absent.
   No repartition fixes that, since the problem is the platform's and not the
   package layout's.

   The cost is that such a type cannot be narrowed. `x is C` for a class
   compiles to `x instanceof C` at `internal/codegen/builder.go:1378`, which
   needs the constructor at runtime, so a type-only binding has to reject the
   guard rather than emit one. A worker holding a `MessageEvent` can say its
   `source` is a `WindowProxy` and cannot test for it, which is what the
   platform allows and no more.

   The transferables are not this case, though they look like it. Every one of
   the sixteen in `transferable.md` is exposed in at least one worker kind, so
   wherever a worker can hold one it also has the global and `instanceof`
   works. What several of them lack is a constructor, which is the other
   stream's F2 and leaves the class in place for narrowing.
4. **How is enforcement rolled out?** F3 says an error from the first release.
   The fixtures that exercise the old path are not being migrated, so the
   question is what happens to user code rather than to the suite.

5. **What is the worker scope package called?** *Decided: `web:worker`, with
   `web:workers` unchanged.* F4 makes a scope package's binding the scope
   object, and the binding is `worker`, so the name follows. `web:workers` holds
   `Worker`, `SharedWorker`, `AbstractWorker` and `WorkerOptions`, the API for
   starting a worker from outside one. The two names are a letter apart, which
   is accepted rather than unnoticed. Renaming either later means a generated
   file name, the two import headers that name `web:workers`, and its row in
   `internal/dts_to_esc/tier.go`. The worklets have no such clash.

6. **Where do a runtime's availability facts come from?** `[Exposed]` is a
   WebIDL attribute, so it answers the nine browser global scopes and nothing
   else. N3 says a claim is derived rather than hand-maintained, and for `node`,
   `deno` and `bun` the candidate sources are each runtime's own type
   definitions, the WinterTC minimum common API for what they share, or a
   hand-written table with a stated reason per entry. None is surveyed. §7.6.1
   is the stage, and it comes first in phase four because the answer shapes the
   rest.

## 11. Non-goals

- Improving what a declaration says. The other stream owns the conversion
  input, the constructors, the derived sets and the hoisting.
- Feature detection at runtime in user code, such as narrowing on
  `typeof OffscreenCanvas !== "undefined"`.
- A runtime's own API surface. F10 makes `node` an environment and phase four
  makes it a target, but ingesting `@types/node` and the rest is the other
  stream's kind of work, not this one's.
- Picking a winner among the runtimes. The vocabulary names what a program can
  target; it does not say which a project should use.

## 12. Related issues

| issue | relation |
| --- | --- |
| #1613 | per-arm availability for `MessageEvent.source`, which F5 would close |
| #1614 | retiring the package-to-tier machinery, attempted in #1631 and still open |
| #1586 | the tier is per package where availability is per declaration |
| #1641, #1642 | per-environment source files |
| #1644 | moving `MessagePort` and `Transferable` out of `web:dom`, which is F4 in one family |
| #1861 | warm whole-tree inference regressed 1.5x. Closed, overtaken by the cross-run cache |
| #1862 | the closure benchmark silently measures fewer packages than it claims |
| #2377 | importing `web:dom` reports 346 errors, which blocks every stage that moves a declaration out of it |
| #2378 | the override check rejects a method that indexes its own type parameter, which is most of #2377 |

#1644's blocker section was wrong when filed and has been corrected. It claimed
that a `Transferable` in `web:core` naming `OffscreenCanvas` would fail the
environment check. The check it named no longer exists on `main`, and even in the
tree it was written against the claim did not hold. The blocker is reachability: a
worker cannot import the packages the arms live in.

