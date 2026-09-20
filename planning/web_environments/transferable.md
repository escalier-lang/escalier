# Validating the plan against `Transferable`

Checks whether the requirements in `requirements.md` let a program name every
transferable type in every environment the platform exposes it in. They do not
yet, and the gaps are listed below.

The data here comes from `@webref/idl` 3.84.0, the curated WebIDL for the whole
platform. It is reachable from the build environment, which settles open
question 4 in `requirements.md`: the source for per-worker-kind exposure exists
and can be fetched.

## The set is larger than the tree knows

WebIDL marks transferability with a `[Transferable]` extended attribute on the
interface. There is no `Transferable` union in the platform. The union in
`lib.dom.d.ts` is TypeScript's own convenience type, and it is incomplete.

Sixteen interfaces carry `[Transferable]`:

| interface | `Exposed` | in the tree |
| --- | --- | --- |
| `ReadableStream` | `*` | `web/streams.esc` |
| `WritableStream` | `*` | `web/streams.esc` |
| `TransformStream` | `*` | `web/streams.esc` |
| `ImageBitmap` | Window, Worker | `web/dom.window.esc` |
| `OffscreenCanvas` | Window, Worker | `web/dom.window.esc` |
| `MessagePort` | Window, Worker, AudioWorklet | `web/dom.window.esc` |
| `MIDIAccess` | Window, Worker | `web/dom.window.esc` |
| `WebTransportDatagramsWritable` | Window, Worker | absent |
| `WebTransportReceiveStream` | Window, Worker | absent |
| `WebTransportSendStream` | Window, Worker | absent |
| `AudioData` | Window, DedicatedWorker | `web/web_codecs.window.esc` |
| `VideoFrame` | Window, DedicatedWorker | `web/web_codecs.window.esc` |
| `RTCDataChannel` | Window, DedicatedWorker | `web/web_rtc.window.esc` |
| `MediaSourceHandle` | Window, DedicatedWorker | `web/dom.window.esc` |
| `MediaStreamTrack` | Window, DedicatedWorker | `web/dom.window.esc` |
| `MediaStreamTrackHandle` | Window, DedicatedWorker | absent |

TypeScript's union lists eleven, of which `ArrayBuffer` is ECMAScript rather
than WebIDL. So it names ten of the sixteen and omits `MIDIAccess`,
`MediaStreamTrack`, `MediaStreamTrackHandle` and the three WebTransport
streams. The tree inherits the omission.

## Not every transferable type reaches every worker

Six of the sixteen are `Exposed=(DedicatedWorker, Window)`. A shared worker and
a service worker do not have them:

`AudioData`, `VideoFrame`, `RTCDataChannel`, `MediaSourceHandle`,
`MediaStreamTrack`, `MediaStreamTrackHandle`.

So a single `Transferable` covering every environment cannot be right. A page or
a dedicated worker can receive sixteen kinds of object, and a shared or service
worker ten. The type has to say which, through F5 or through per-arm
availability, and a merged form claiming all sixteen everywhere would tell a
service worker it might receive a `VideoFrame`.

## Nine of the twelve present are unreachable from a worker

| package | file | transferable types it holds | reachable from a worker |
| --- | --- | --- | --- |
| `web:streams` | `streams.esc` | `ReadableStream`, `WritableStream`, `TransformStream` | yes |
| `web:dom` | `dom.window.esc` | `ImageBitmap`, `OffscreenCanvas`, `MessagePort`, `MIDIAccess`, `MediaSourceHandle`, `MediaStreamTrack` | no |
| `web:web_codecs` | `web_codecs.window.esc` | `AudioData`, `VideoFrame` | no |
| `web:web_rtc` | `web_rtc.window.esc` | `RTCDataChannel` | no |

A worker that receives any of the nine cannot name its type. That is F4 failing,
and N2 is why: the packages are carved by API family, and every one of these
families has a worker-visible part sitting in a window-only package.

## What the plan has to do

1. **Move the nine.** Each needs a package importable from the environments it
   is exposed in. `MessagePort`, `ImageBitmap` and `OffscreenCanvas` are
   Window plus every worker, so `web:core` or the proposed `web:canvas` fits.
   The six dedicated-worker-only ones need a package importable from a page and
   a dedicated worker, which no current package is. This is the same shape as
   `requestAnimationFrame` in `hoisted/README.md`.
2. **Split `Transferable` by environment.** Sixteen arms for a page and a
   dedicated worker, ten for a shared or service worker.
3. **Add the four missing types**, or record why the tree tracks TypeScript's
   union rather than the `[Transferable]` attribute.
4. **Derive the attribute rather than hand-listing it.** `[Transferable]` is
   machine-readable, so the union should be generated. That is N3.

## The environment vocabulary is short

`@env` names four environments. The curated IDL names twelve globals, by count
of the interfaces exposed in each:

| global | interfaces |
| --- | --- |
| `Window` | 1092 |
| `Worker` | 297 |
| `DedicatedWorker` | 56 |
| `LayoutWorklet` | 45 |
| `*` | 39 |
| `PaintWorklet` | 39 |
| `ServiceWorker` | 29 |
| `SharedWorker` | 9 |
| `AudioWorklet` | 4 |
| `AnimationWorklet` | 3 |
| `Worklet` | 3 |
| `RTCIdentityProvider` | 2 |

`MessagePort` is exposed in `AudioWorklet`, which `@env` cannot say. The
worklets are not a corner: `LayoutWorklet` and `PaintWorklet` together carry
more interfaces than `ServiceWorker`.

This does not have to be solved here. It has to be decided: either the
vocabulary grows to match the IDL, or the non-goals record that worklets are out
of scope and something rejects an `Exposed` value the vocabulary cannot map.
Silently dropping a global the IDL names would put the tree back where the file
name left it, claiming more than it knows.
