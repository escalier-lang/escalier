# `webidl_to_esc` — WebIDL → Escalier prototype

A two-stage pipeline that turns the W3C/WHATWG specs' embedded WebIDL into
Escalier `.esc` declarations. It exists to answer one question: can the
machine-readable IDL the specs publish do a better job than TypeScript's
`lib.dom.d.ts` at deciding receiver mutability, parameter mutability, and
lifetimes for DOM builtins?

## Why WebIDL at all

`lib.dom.d.ts` is itself *generated* from WebIDL (TypeScript consumes
`@webref/idl`). So WebIDL gives the same structural surface we already parse
with `internal/dts_parser`, plus a few extended attributes the `.d.ts`
generator throws away. Those discarded attributes are the whole reason to go
upstream:

| Signal | In `.d.ts`? | What it tells us |
| --- | --- | --- |
| `readonly attribute` | yes (`readonly`) | getter-only → non-mutating receiver |
| `[SameObject]` | **no** | the value is owned by the host; a returned reference **borrows from `self`** → a lifetime tie |
| `[NewObject]` | **no** | a freshly allocated, **caller-owned** value → no lifetime tie to the receiver |
| `[PutForwards=x]` | partial | a readonly slot whose assignment forwards to `.x` (a hidden setter) |

What WebIDL still does **not** carry:

- **Method receiver mutation.** There is no purity/side-effect annotation on
  operations. `appendChild` mutating its node is nowhere in the IDL. So the
  receiver decision for methods stays heuristic — we reuse
  `interop.ClassifyMethodByName` (the same name-based tiers the `.d.ts` path
  uses).
- **Argument mutation.** Arguments have no aliasing/mutation annotation
  (`[Clamp]`/`[EnforceRange]` only constrain numeric coercion). No signal.

Net: WebIDL is a **lifetime/ownership seed**, not a mutability oracle. The
two mutability axes remain heuristic; lifetimes get real new signal.

## Pipeline

```
@webref/idl  ──(extract.mjs, Node)──►  <spec>.json (IR)  ──(webidl_to_esc, Go)──►  <spec>.esc
```

### Stage 1 — `extract.mjs` (Node)

Parses every spec in `@webref/idl` with `webidl2` and emits one JSON artifact
per spec. The JSON IR is a narrow, stable contract (see `internal/webidl/ir.go`
for the matching Go structs) capturing only the interfaces, members, types,
and the four signals above. Regenerate it whenever `@webref/idl` is bumped;
the Go side never touches `webidl2`.

```sh
cd tools/webidl_to_esc
npm install
node extract.mjs <out-dir>            # all specs
node extract.mjs <out-dir> dom html   # just these specs
```

### Stage 2 — `webidl_to_esc` (Go)

Reads the JSON artifacts and renders `.esc`. Conversion lives in
`internal/webidl` so it is unit-tested without the CLI. Each interface becomes
a `declare class`; instance members get a `self` / `mut self` receiver from
the shared classifier; `[NewObject]` returns are wrapped in `mut`;
`[SameObject]` getters are tagged as borrowing from `self`.

```sh
go run ./tools/webidl_to_esc -stdout samples/dom.json   # to stdout
go run ./tools/webidl_to_esc -o out samples/dom.json     # writes out/dom.esc
```

## Samples

`samples/dom.json` is the stage-1 IR for the DOM spec; `samples/dom.esc` is
the stage-2 output. They let you read the result, and run stage 2, without an
`npm install`. Representative lines:

```escalier
get signal(self) -> AbortSignal,  // [SameObject] result borrows from self; candidate for a self lifetime
dispatchEvent(mut self, event: Event) -> boolean,
static abort(reason?: unknown) -> mut AbortSignal,  // [NewObject] caller owns a fresh value
throwIfAborted(mut self) -> undefined,  // receiver-mut uncertain (tier-7 default)
```

## Status & scope

This is a feasibility prototype, not production:

- Output is a **review aid** that stamps the hard-to-infer signals for a human
  to confirm — the same model as `tools/dts_to_esc`. It is not guaranteed to
  type-check.
- Borrows from `[SameObject]` are emitted as comments, not real lifetime
  syntax — the source-level lifetime grammar is still nascent in the parser.
- `[SameObject]`/`[NewObject]` are applied inconsistently across specs, so
  treat a hit as high-precision but absence as "unknown", not "not a borrow".
- Dictionaries, enums, typedefs, callbacks, and cross-spec mixin resolution
  are out of scope. `iterable`/`maplike`/`setlike` members render as a TODO.
- `internal/dts_parser` stays the path for third-party deps; this only targets
  builtins that have a governing spec.
