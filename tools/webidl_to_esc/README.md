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
- **Exceptions.** WebIDL has no `[Throws]` in the standard. Exceptions live in
  the spec's prose *algorithms*, which webref also publishes as structured
  JSON — see "Stage 1b" below.

Net: WebIDL is a **lifetime/ownership seed**, not a mutability oracle. The
two mutability axes remain heuristic; lifetimes get real new signal; throws
come from the algorithm corpus, not the IDL.

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
go run ./tools/webidl_to_esc -stdout out/dom.json   # to stdout
go run ./tools/webidl_to_esc -o out out/dom.json     # writes out/dom.esc
```

### Stage 1b — `extract_throws.mjs` (Node)

WebIDL carries no exception info, but webref publishes the specs' algorithms
as structured JSON at `ed/algorithms/<spec>.json` in the `w3c/webref` repo
(not on npm — pull from `raw.githubusercontent.com`). Exceptions there are
typed links (`data-link-type="exception"`), and each algorithm is named
`Interface/method(args)`, so they map straight back to operations.

`extract_throws.mjs` loads a directory of those algorithm files, builds the
call graph (steps link to the helper algorithms they invoke), computes the
**transitive throw closure** per operation, and emits a map keyed
`Interface.method`. A method's throw usually lives in a helper it calls
(`append` → `pre-insert` → `ensure pre-insertion validity`), so the closure
is essential.

```sh
# fetch the DOM dependency set first (dom + its cross-spec helpers), plus dfns
for s in dom infra webidl url html; do
  curl -sL "https://raw.githubusercontent.com/w3c/webref/main/ed/algorithms/$s.json" -o algos/$s.json
done
curl -sL "https://raw.githubusercontent.com/w3c/webref/main/ed/dfns/dom.json" -o dfns/dom.json
node extract_throws.mjs algos out/dom.throws.json dfns   # map + coverage on stderr
```

Then feed the map to stage 2:

```sh
go run ./tools/webidl_to_esc -throws out/dom.throws.json -o out out/dom.json
```

Three subtleties the prototype had to handle:

- **Inconsistent exception markup.** webref tags a thrown exception's name with
  `data-link-type="exception"` on some specs and `data-link-type="idl"` on
  others — even within one algorithm (`NamespaceError` vs
  `InvalidCharacterError` in validate-and-extract). The extractor matches the
  link *text* by shape (any `*Error` name) rather than the link type.
- **Terse delegating methods (the bridge).** webref captures algorithms
  written as explicit step lists. A one-line delegating method ("the
  `removeChild(child)` method steps are to return the result of pre-removing
  child") is not emitted as an operation node, and webref carries no
  machine-readable method→concept link — the method dfn's only outgoing link is
  a dev example. So `extract_throws.mjs` keeps a small **curated `BRIDGE`
  table** mapping such methods to their concept algorithm's href, and unions
  that concept's closed throw set into the method. The optional `dfns` argument
  validates every bridge key names a real method, catching typos and spec
  drift.
- **Mixin origin.** A bridged or algorithm-derived throw is keyed by the
  *declaring* interface, which may be a mixin (`ParentNode.querySelector`). The
  Go converter folds mixins into concrete interfaces, so it records each
  member's origin and resolves the throws map against both the concrete
  interface and the origin.

Remaining gap: **cross-spec helpers.** A throw reachable only through an
algorithm in a spec that was not loaded is still missed; load the full
`ed/algorithms/*` set to close it. The extractor reports the unresolved
external-edge count so the gap is visible.

## Example output

The generated `.esc` types are checked in separately, not in this tool's
directory — run the stages above to produce them. Representative output lines:

```escalier
get signal(self) -> AbortSignal,  // [SameObject] result borrows from self; candidate for a self lifetime
dispatchEvent(mut self, event: Event) -> boolean throws InvalidStateError,
createElement(mut self, localName: string, options?: string | ElementCreationOptions) -> mut Element throws InvalidCharacterError | NotSupportedError,
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
