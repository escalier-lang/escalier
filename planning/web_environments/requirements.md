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

### 3.6 The environment vocabulary is finer than the source data

The tree distinguishes four environments. `lib.webworker.d.ts` is a single file
covering all three worker kinds, so it cannot express the difference. Every
per-worker-kind fact in the repository comes from a 24-entry hand-written table at
`internal/dts_to_esc/env_table.go:91-121`.

This matters for `Transferable`, whose arms are not uniformly available:

| arm | exposed in |
| --- | --- |
| `ArrayBuffer`, `MessagePort`, `ReadableStream`, `WritableStream`, `TransformStream`, `ImageBitmap`, `OffscreenCanvas` | window and every worker kind |
| `AudioData`, `VideoFrame`, `RTCDataChannel`, `MediaSourceHandle` | window and dedicated worker only |

The two libs declare `Transferable` identically, so the converter sees no
difference and cannot derive the split.

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

## 4. Requirements

**R1. One fact, one place.** Where a declaration exists is a property of the
declaration. Nothing else may restate it. A package file name, a partition entry
and a directory layout must not carry an environment claim that can drift from the
declaration's own.

**R2. A package is importable as a unit.** Every declaration a package holds is
reachable from every environment that may import the package. Splitting a family
across packages is acceptable; a package no environment can fully use is not.

**R3. A program states its environment.** A program compiled for a runtime
declares which one, or it is inferred from its imports. Both the import rule and
any per-environment name resolution read that one answer.

**R4. Environment rules reach user code.** The check that a reference does not
escape its environments applies to a user program, not only to the generated tree.

**R5. A worker can name what it receives.** Any declaration the platform makes
available in an environment is nameable there, without importing declarations that
environment does not have.

**R6. A divergent declaration is expressible.** Where environments genuinely
disagree, as with `MessageEvent.source`, the tree states each environment's form
rather than narrowing every environment to their intersection.

**R7. Claims are derived, not hand-maintained.** A per-declaration environment
comes from the source data. Hand-written tables are for what no source answers,
and each entry says why it exists.

**R8. Nothing regresses cold load.** Reorganization is measured against
`BenchmarkStdlibClosureLoad`. #1643 covers the separate finding that comment
attachment is 74% of cold load.

## 5. Non-goals

- Feature detection at runtime in user code, such as narrowing on
  `typeof OffscreenCanvas !== "undefined"`. Worth having, not needed for any
  requirement here.
- Node, Deno and Bun. The environment vocabulary is browser-shaped today and
  widening it is separate work.
- Replacing TypeScript's libs as the conversion input. WebIDL is proposed below as
  a supplement for facts the libs do not carry, not as a replacement.

## 6. Open questions

1. **Does the file name survive?** Under R1 it cannot claim anything about the
   declarations inside. Either it is redefined as the package's importability,
   which is a real and distinct fact, or it goes and importability is derived
   from the declarations.
2. **How does a program declare its environment?** A `package.json` field, a
   compiler flag, or inference from the intersection of its imports. Inference
   gives the `web:dom` plus `web:worker` error for free as an empty intersection.
3. **Per-environment declarations or per-arm annotations?** #1613 proposes
   annotating union arms. Emitting one declaration per environment is simpler and
   needs no new annotation granularity, but it makes name resolution
   environment-dependent and `EnvIndex` currently intersects over a duplicate
   name, which is the wrong operation for disjoint copies.
4. **Where does per-worker-kind data come from?** `@webref/idl` publishes the
   curated WebIDL with `[Exposed]` on every interface. Adding it as a build-time
   input would also settle #1645.
5. **Is a type-only reference needed?** For `WindowProxy` in a portable union the
   answer looks like yes, since no split can make it portable. It is not needed
   for `OffscreenCanvas`, which a repartition reaches.

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
