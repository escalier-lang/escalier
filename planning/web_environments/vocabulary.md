# The environment vocabulary

`@env` names four environments. The platform has nine. This is what the
vocabulary becomes when it matches, and what the change costs.

Derived from `@webref/idl` 3.84.0. A WebIDL interface declares a global scope
with `[Global=(names)]`, and those names are what an `Exposed` value resolves
against, so the list below is the platform's own and not a judgement call.

## The nine global scopes

| `@env` name | WebIDL global scope |
| --- | --- |
| `window` | `Window` |
| `dedicated_worker` | `DedicatedWorkerGlobalScope` |
| `shared_worker` | `SharedWorkerGlobalScope` |
| `service_worker` | `ServiceWorkerGlobalScope` |
| `rtc_identity_provider` | `RTCIdentityProviderGlobalScope` |
| `audio_worklet` | `AudioWorkletGlobalScope` |
| `paint_worklet` | `PaintWorkletGlobalScope` |
| `layout_worklet` | `LayoutWorkletGlobalScope` |
| `animation_worklet` | `AnimationWorkletGlobalScope` |

## The two groups

| `@env` name | covers |
| --- | --- |
| `worker` | `dedicated_worker`, `shared_worker`, `service_worker`, `rtc_identity_provider` |
| `worklet` | `audio_worklet`, `paint_worklet`, `layout_worklet`, `animation_worklet` |

A declaration with no decorator exists on all nine, which is WebIDL's
`Exposed=*`.

## Two changes to what the current names mean

**`worker` gains a fourth member.** It covers three environments today and four
in WebIDL, because `RTCIdentityProviderGlobalScope` is declared
`[Global=(Worker, RTCIdentityProvider)]`. Every existing `@env("worker")` is
under-claiming by one environment.

**An unannotated declaration claims more.** It means four environments today and
nine after. Only the 38 interfaces WebIDL marks `Exposed=*` belong there, so
most of what carries no decorator now needs one.

## What it costs

1139 interfaces in the curated IDL carry `Exposed`. By signature:

| `Exposed` | interfaces | `@env` after |
| --- | --- | --- |
| `Window` | 714 | `@env("window")`, unchanged |
| `Window, Worker` | 246 | `@env("window", "worker")`, new |
| `*` | 38 | none, unchanged |
| `LayoutWorklet, PaintWorklet, Window, Worker` | 36 | `@env("window", "worker", "layout_worklet", "paint_worklet")`, new |
| `DedicatedWorker, Window` | 35 | `@env("window", "dedicated_worker")`, new |
| `ServiceWorker` | 20 | `@env("service_worker")`, unchanged |
| `LayoutWorklet` | 9 | `@env("layout_worklet")`, new |
| `DedicatedWorker` | 7 | `@env("dedicated_worker")`, unchanged |
| `DedicatedWorker, SharedWorker, Window` | 6 | new |
| `DedicatedWorker, ServiceWorker, Window` | 4 | new |

Around 290 declarations gain a decorator they do not carry today. Every one is
derived from `Exposed`, so the cost is in the size of the diff rather than in
the judgement.

## What the worklets hold

58 interfaces are exposed in a worklet. The bulk is CSS Typed OM, which
`LayoutWorklet` and `PaintWorklet` share with `Window` and `Worker`:
`CSSStyleValue`, `CSSUnitValue`, `CSSMathSum`, `CSSTransformValue` and the rest
of that family. The remainder is each worklet's own scope and machinery, such as
`AudioWorkletProcessor`, `PaintRenderingContext2D`, `LayoutFragment` and
`IntrinsicSizes`.

`MessagePort` and `MessageEvent` are exposed in `AudioWorklet`, which is the
case the four-name vocabulary cannot state at all.

So the worklets are not a corner to postpone. `LayoutWorklet` and `PaintWorklet`
each appear on more interfaces than `SharedWorker` does.

## Settled

**A worklet is a compilation target.** A paint worklet is a module the browser
loads, so the target environment from F1 accepts these names and every rule that
reads it applies to a worklet unchanged.

**`web:*` gains worklet packages.** The CSS Typed OM is in window-only `web:dom`
today, so a paint worklet cannot name `CSSUnitValue`. That is the same
reachability failure `transferable.md` records, in a family nothing has looked at
yet, and F11 covers it.

Both land in phase three. The vocabulary itself lands in phase one, because a
declaration that says `@env("window")` is making a claim about the other eight
environments and cannot be written without the names for them.
