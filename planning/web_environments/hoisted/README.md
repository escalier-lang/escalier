# Hoisting the scope-class globals

Planning artifact for requirements F6 through F9 in `../requirements.md`. The
`.esc` files beside this one show what every member of every `*GlobalScope`
class looks like as a top-level declaration, which package it lands in, and what
`@env` it carries. They are illustrative and are not part of the generated tree.

## Method

**Which scopes provide a member** comes from the interface inheritance in
TypeScript's libs. Each scope is walked through its `extends` chain, and a
member is recorded against every scope that reaches it.

| scope | environments it contributes |
| --- | --- |
| `Window` | `window` |
| `GlobalEventHandlers`, `WindowEventHandlers` | `window` |
| `WindowLocalStorage`, `WindowSessionStorage` | `window` |
| `WindowOrWorkerGlobalScope` | every environment |
| `AnimationFrameProvider` | `window`, `dedicated_worker` |
| `FontFaceSource` | every worker, through `WorkerGlobalScope` |
| `MessageEventTarget` | `dedicated_worker` |
| `WorkerGlobalScope` | every worker |
| `DedicatedWorkerGlobalScope` | `dedicated_worker` |
| `SharedWorkerGlobalScope` | `shared_worker` |
| `ServiceWorkerGlobalScope` | `service_worker` |

A member's environments are the union over the scopes that declare it, so
`close` is on `Window`, `DedicatedWorkerGlobalScope` and
`SharedWorkerGlobalScope` and comes out as those three.

**The declaration text** comes from the committed tree rather than from
TypeScript. The converter has already turned each member into Escalier syntax
with the right qualified names, so `importScripts` arrives as
`...urls: mut Array<string | url.URL>` with `web:url` already named. Hoisting
drops the `self` receiver and picks the top-level form: a method becomes `fn`, a
read-only property `val`, and a settable property or accessor pair `var`.

**The package** is the family that owns the member's type. Where the Escalier
signature already carries a qualifier the qualifier decides it, so
`readonly caches: cache.CacheStorage` goes to `web:cache` and
`readonly performance: performance.Performance` to `web:performance`. The rest
are assigned by family in the table inside `emit.mjs`, reproduced in
"Assignments worth arguing about" below.

## Results

240 declarations from 227 distinct globals, written as 246 signature lines
because an overload set stays several lines. The gap between 240 and 227 is the
divergent names, which contribute one declaration per environment.

| package | declarations |
| --- | --- |
| `web:dom` | 176 |
| `web:core` | 40 |
| `web:worker` | 16 |
| `web:storage` | 2 |
| `web:canvas` | 1, two signatures |
| `web:cache`, `web:crypto`, `web:fetch`, `web:indexeddb`, `web:performance` | 1 each |

Environments:

| `@env` | count |
| --- | --- |
| `@env("window")` | 189 |
| none, meaning every environment | 18 |
| `@env("service_worker")` | 13 |
| `@env("worker")` | 11 |
| `@env("dedicated_worker")` | 4 |
| `@env("window", "dedicated_worker")` | 2 |
| `@env("window", "dedicated_worker", "shared_worker")` | 1 |
| `@env("dedicated_worker", "shared_worker")` | 1 |
| `@env("shared_worker")` | 1 |

## Findings

### The worker kinds are derivable here

Section 3.6 of `../requirements.md` records that `lib.webworker.d.ts` collapses
the three worker kinds into one file, so per-worker-kind facts need WebIDL. That
does not apply to globals. The scope classes are separate interfaces, so
`skipWaiting` is visibly service-worker-only and `onconnect` visibly
shared-worker-only, straight from the inheritance. 19 of the 246 declarations
carry a single worker kind, all derived without any external source.

### Thirteen names differ by environment

`location`, `name`, `navigator`, `onerror`, `onlanguagechange`, `onmessage`,
`onmessageerror`, `onoffline`, `ononline`, `onrejectionhandled`,
`onunhandledrejection`, `postMessage` and `self`.

`onmessage` is the sharpest, with three forms:

```
@env("window")
export declare var onmessage: (fn (this: WindowEventHandlers, ev: MessageEvent) -> any) | null

@env("dedicated_worker")
export declare var onmessage: (fn (this: T, ev: MessageEvent) -> any) | null

@env("service_worker")
export declare var onmessage: (fn (this: ServiceWorkerGlobalScope, ev: ExtendableMessageEvent) -> any) | null
```

Every one of these needs F5. Without per-environment declarations the hoist has
to intersect the three, which loses `ExtendableMessageEvent` for the service
worker.

### A global on every environment belongs in a package every environment can import

`location` and `navigator` are both on `Window` and on `WorkerGlobalScope`, so
both are available everywhere. An earlier pass sent them to `web:dom` and
`web:worker` on the strength of their types, which made them the only two of the
29 every-environment globals routed to environment-specific packages. A portable
program would then need a different import per environment to reach one global.

Both are in `web:core` here instead. That is not free, because their types
differ, and moving a type wholesale does not work:

| type | size |
| --- | --- |
| `Location` | 13 members |
| `WorkerLocation` | 10 members, exactly `Location` without `ancestorOrigins`, `assign`, `reload` and `replace` |
| `Navigator` | 11 mixins and 69 own members |
| `WorkerNavigator` | 7 mixins and 3 own members |

`Navigator` reaches most of the browser, so it cannot move to `web:core`.

The platform has already factored out the shared part, which is the way through.
`Navigator` and `WorkerNavigator` share seven mixins exactly, `NavigatorBadge`,
`NavigatorConcurrentHardware`, `NavigatorID`, `NavigatorLanguage`,
`NavigatorLocks`, `NavigatorOnLine` and `NavigatorStorage`. The four the window
adds are `NavigatorAutomationInformation`, `NavigatorContentUtils`,
`NavigatorCookies` and `NavigatorPlugins`. `WorkerLocation` is a strict subset of
`Location` on the same principle.

So the shared surface is identified in the source data already. `web:core` can
hold the shared mixins and a `location` and `navigator` typed against them, while
`web:dom` and `web:worker` keep the environment-specific extensions. The two
declarations in `core.esc` carry a `NEEDS A TYPE SPLIT` comment, since they name
the unsplit types and would not compile as written.

**This is a third option for F5.** Per-environment copies and per-arm
annotations both assume the divergence is irreducible. Where the platform
factored a mixin out, the shared part can be hoisted and only the extension left
behind, which needs neither. It does not cover everything: `ExtendableMessageEvent`
extends `ExtendableEvent`, not `MessageEvent`, so `onmessage` has no shared type
and still needs F5 proper.

### `name` still splits across packages

`name` is a `string` in both a page and a worker, so the types agree, but it is
on `Window`, `DedicatedWorkerGlobalScope` and `SharedWorkerGlobalScope` and not
on `ServiceWorkerGlobalScope`. It stays in `web:dom` and `web:worker` here.
Whether a per-environment pair may straddle two packages is a question the
resolution rule has to answer either way.

### `requestAnimationFrame` fits no existing package

It is `window` plus `dedicated_worker`, and no current package is importable
from exactly those two. `web:dom` is a page, `web:worker` is a worker. Both are
placed in `web:core` here, which is importable from everywhere and therefore
wider than the declaration needs. This is N2 biting: the packages are not carved
along environment lines, so a member spanning an unusual pair has nowhere exact
to go.

### `addEventListener` is not hoisted

`addEventListener` and `removeEventListener` are generic over each scope's own
event map, so hoisting yields one per scope, all colliding. They are also the
only members that are more the scope's machinery than a global a program calls.
Both are left out of the files here and need a decision of their own.

## Assignments worth arguing about

- **`web:core` takes the timers and the encoders.** `setTimeout`,
  `clearTimeout`, `setInterval`, `clearInterval`, `queueMicrotask`,
  `reportError`, `atob`, `btoa` and `structuredClone` name no browser type and
  are in the WinterTC minimum common API, which is what `web:core` already
  collects.
- **`web:core` also takes the shared error handlers.** `onerror`,
  `onrejectionhandled` and `onunhandledrejection` are on both `Window` and
  `WorkerGlobalScope`. Their event types, `ErrorEvent` and
  `PromiseRejectionEvent`, are in `web:dom` today, so this placement assumes
  those move with them.
- **`location` and `navigator` go to `web:core`** on the reasoning above,
  conditional on the type split.
- **`createImageBitmap` goes to `web:canvas`,** the package Stage 2 of the
  implementation plan proposes. It has nowhere else to go that a worker can
  import.
- **`requestIdleCallback` stays in `web:dom`** while `requestAnimationFrame`
  moves to `web:core`, because the idle callbacks are window-only and the
  animation frame is not.
- **The 176 window-only members mostly stay in `web:dom`.** Around 120 are the
  `GlobalEventHandlers` set, `onclick` through `onwheel`, which belong with the
  DOM event model. The remaining 56 are the browsing context, `alert`, `scroll`,
  `innerWidth`, `history`, `frames` and the rest. A `web:window` package for
  those is a real option and is left open.

## Artifacts to fix before any of this is generated

- `onmessage` for a dedicated worker renders as `this: T`, because
  `MessageEventTarget<T>` is generic and the hoist does not substitute the type
  argument. It should read `DedicatedWorkerGlobalScope`.
- Window's `onerror` is typed `OnErrorEventHandler`, a type alias, where the
  worker's is written out. Both are correct; they just do not look alike.
- Every hoisted handler keeps a `this:` annotation naming a scope class. That is
  N6 in practice: the classes cannot be deleted once their members leave.
- `set location(mut self, href: string)` takes a `string` while the getter
  returns a `Location`. The hoist renders one `var location: Location` and drops
  the setter's wider input.

## Reproducing

The three scripts are in `scripts/`. They read TypeScript's libs from
`node_modules` and the converted members from a git ref, so run them from that
directory:

```sh
mkdir -p /tmp/hoist
git show <ref>:internal/interop/data/web/dom.window.esc    > /tmp/hoist/dom.esc
git show <ref>:internal/interop/data/web/worker.worker.esc > /tmp/hoist/worker.esc
WORK=/tmp/hoist node extract.mjs
WORK=/tmp/hoist node merge.mjs
WORK=/tmp/hoist OUT=.. node emit.mjs
```

`extract.mjs` walks the scope interfaces and writes each member's environments.
`merge.mjs` joins those to the converted Escalier text. `emit.mjs` assigns
packages and writes the `.esc` files beside this README. The files here were
produced from `claude/undrop-worker-libs`, which is #1635's branch, since
`web/worker.worker.esc` does not exist on `main` yet.
