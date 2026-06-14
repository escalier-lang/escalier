# WebIDL-Sourced Types for Web Platform Builtins

## Provenance

This workstream grew out of a feasibility question: can the W3C/WHATWG
specs' embedded WebIDL generate better Escalier types for DOM builtins
than TypeScript's `lib.dom.d.ts`? A working prototype answered "yes, for
lifetimes; no, for mutability" and lives at:

- [tools/webidl_to_esc/extract.mjs](../../tools/webidl_to_esc/extract.mjs) —
  Node stage that parses `@webref/idl` into a JSON IR.
- [internal/webidl/](../../internal/webidl/) — Go stage that renders the IR
  to `.esc`.
- [tools/webidl_to_esc/samples/](../../tools/webidl_to_esc/samples/) —
  committed `dom.json` IR and `dom.esc` output.

It is a companion to two existing workstreams:

- [../builtins/requirements.md](../builtins/requirements.md) — the
  authoritative design for `web:*` pseudo-packages. WebIDL is a *source*
  for the `web:*` `.esc` files that workstream defines; it does not change
  the packaging, import, or prelude model.
- [../interop_mutability/](../interop_mutability/) — the `Classify`
  resolution ladder and the `tools/dts_to_esc/` converter. The WebIDL path
  reuses `Classify` for receiver mutability and mirrors the
  "stamp-then-hand-edit" model of the `.d.ts` converter.

## Background: why go upstream of `.d.ts`

`lib.dom.d.ts` is itself generated from WebIDL — TypeScript's own generator
consumes `@webref/idl`. Parsing the IDL directly therefore yields the same
structural surface Escalier already parses with `internal/dts_parser`, plus
a handful of extended attributes that the `.d.ts` generator discards. Those
discarded attributes are the entire reason to add a second source.

The three questions this workstream set out to answer, and what WebIDL
actually carries:

1. **When should a method receiver be `mut`?** WebIDL has no purity or
   side-effect annotation on operations. `appendChild` mutating its node is
   nowhere in the IDL. The only receiver signal WebIDL carries is
   `readonly attribute`, which the `.d.ts` already preserves as `readonly`.
   So WebIDL adds nothing here; the method-receiver decision stays
   heuristic.
2. **When should a non-receiver parameter be `mut`?** WebIDL arguments carry
   no aliasing or mutation annotation. `[Clamp]` and `[EnforceRange]` only
   constrain numeric coercion. No signal.
3. **Where should lifetimes go?** This is the real win. Two extended
   attributes encode ownership and identity, and both are dropped by the
   `.d.ts` generator:
   - `[NewObject]` — the operation returns a freshly allocated value. It is
     caller-owned, with no lifetime tie to the receiver.
   - `[SameObject]` — the attribute returns the identical object every call.
     It is stored on the host, so a returned reference borrows from `self` —
     a lifetime tie between receiver and result.

The conclusion that shapes every requirement below: **WebIDL is a
lifetime/ownership seed, not a mutability oracle.** The two mutability axes
remain heuristic; lifetimes gain real signal.

### A fourth question: which exceptions does an operation throw?

WebIDL has no `[Throws]` in the standard grammar — a survey of all 325 spec
files in `@webref/idl` found zero. Exceptions live in the specs' prose
*algorithms*, not the IDL. But webref publishes those algorithms as
structured JSON at `ed/algorithms/<spec>.json` in the `w3c/webref` repo, and
the data is well-shaped for extraction:

- A throwing step links the exception with `data-link-type="exception"`, so
  `(SyntaxError, …)` extracts deterministically rather than by prose
  matching. `TypeError`/`RangeError` are referenced by name.
- Each algorithm is named `Interface/method(args)`, mapping it straight back
  to the IDL operation.
- A method's throw usually lives in a helper algorithm it calls. Steps link
  to those helpers by href, so the **transitive throw closure** over the
  algorithm call graph yields each operation's full throw set.

A prototype confirmed this end to end: extracting from the DOM dependency
specs produced correct `throws` clauses such as
`createElement(...) throws InvalidCharacterError | NotSupportedError` and
`dispatchEvent(...) throws InvalidStateError`. So a fourth signal —
exceptions — is recoverable, from the algorithm corpus rather than the IDL.
Two coverage gaps are documented under FR12.

## Goals

- Use WebIDL as the authoritative source for the ownership and identity
  signals (`[NewObject]`, `[SameObject]`, `readonly attribute`,
  `[PutForwards]`) that drive lifetime and getter classification on `web:*`
  builtins.
- Reuse `interop.ClassifyMethodByName` for receiver mutability so the WebIDL
  path and the `.d.ts` path make identical name-based decisions, and a fix
  to one fixes both.
- Auto-seed `throws` clauses from webref's structured spec algorithms, turning
  exception annotations from a pure hand-edit into a reviewable generated
  default.
- Produce `.esc` output that is a *review aid* — it stamps the
  hard-to-infer signals for a human to confirm, exactly as
  `tools/dts_to_esc` does. Generated files are committed, then hand-edited;
  regeneration is a review tool, never an automatic build step.
- Keep the IDL parser (`webidl2`) confined to the Node stage. The Go stage
  consumes only the narrow JSON IR, so an `@webref/idl` bump never ripples
  into Go.

## Non-goals

- **Replacing `internal/dts_parser`.** It remains the path for third-party
  npm dependencies and for any JavaScript builtin that has no governing
  spec. WebIDL targets only Web platform builtins.
- **A mutability oracle.** WebIDL does not annotate method-receiver or
  argument mutation; this workstream does not pretend otherwise. The
  generated receiver mode is heuristic and flagged when uncertain.
- **Defining the source-level lifetime grammar.** Borrows from
  `[SameObject]` are emitted as review comments until the parser accepts
  lifetime annotations in declaration position. Wiring real syntax is
  gated on that grammar landing and tracked as a follow-up.
- **The `web:*` packaging, import, and prelude model.** Those are owned by
  [../builtins/requirements.md](../builtins/requirements.md). This
  workstream feeds `.esc` content into that layout; it does not change it.
- **Full WebIDL coverage on day one.** Dictionaries, enums, typedefs,
  callbacks, namespaces, and cross-spec mixin resolution are phased in. The
  `iterable`/`maplike`/`setlike` declarations render as a TODO until
  modelled.

## Functional requirements

### FR1. Two-stage pipeline with a JSON IR boundary

The pipeline is `@webref/idl` → Node extractor → per-spec JSON IR → Go
converter → `.esc`. The JSON IR is the only contract between stages. The Go
side never imports or mirrors the `webidl2` AST. The IR schema is fixed by
the Go structs in [internal/webidl/ir.go](../../internal/webidl/ir.go); any
field the extractor emits must have a matching struct field.

### FR2. IR captures the four signals plus structure

Per spec, the IR records each `interface` and `interface mixin`, its
inheritance, partial flag, and its members. Each member carries exactly the
fields the converter needs:

- **Attributes:** name, type, `readonly`, `static`, `[SameObject]`,
  `[NewObject]`, `[PutForwards]` target.
- **Operations:** name, `static`, `special`, `[NewObject]`, return type,
  arguments with optional/variadic flags.
- **Constructors and constants:** arguments, or name/type/value.

Types are structured, not stringified: unions and generic arguments live in
a recursive `TypeRef` so the Go side maps them without re-parsing.

### FR3. Receiver mutability via the shared classifier

For each operation, the converter calls
`interop.ClassifyMethodByName(name)`. When a name tier matches, the receiver
is `self` or `mut self` per the result. When no tier matches, the receiver
falls to the tier-7 default (`mut self`) and the line is annotated as
uncertain so a reviewer confirms it. WebIDL carries no method-level mutation
signal, so this default is the honest one.

### FR4. `readonly attribute` → getter; writable → getter + setter

A `readonly attribute` renders as a getter with a non-mutating `self`
receiver. A writable attribute renders as a getter plus a setter whose
receiver is `mut self`. A `readonly` attribute carrying `[PutForwards=x]`
also gets a setter, annotated with the forwarding target, because
assignment to it is observable.

### FR5. `[NewObject]` → owned `mut` return

An operation or attribute marked `[NewObject]` has its return or value type
wrapped in `mut`, expressing that the caller owns a fresh value with no
lifetime tie to the receiver. The line is annotated to record the source
signal.

### FR6. `[SameObject]` → borrow-from-`self` tag

An attribute marked `[SameObject]` keeps its non-mutating `self` getter and
is tagged as borrowing from `self` — a candidate for a receiver-bound
lifetime. Until the lifetime grammar lands (FR9), this tag is a comment.

### FR7. Partial-interface and mixin merging within a spec

Partial interfaces of the same name merge into their base. An
`X includes M;` statement folds mixin `M`'s members into interface `X` when
both are in the same artifact. Cross-spec mixin resolution is deferred; an
unresolved mixin leaves the base unchanged and is noted.

### FR8. WebIDL → Escalier type mapping

The converter maps WebIDL types to Escalier types: the string family to
`string`, the numeric family to `number`, `boolean` to `boolean`, `bigint`
to `bigint`, `any`/`object` to `unknown`, `undefined`/`void` to `undefined`,
`sequence`/`FrozenArray`/`ObservableArray` to `Array<…>`, `record` to
`Record<…>`, `Promise` to `Promise<…>`, unions to `A | B`, and a nullable
type to `… | null`. An unrecognised name passes through as an interface,
dictionary, or enum reference.

### FR9. Lifetime syntax emission (gated)

Once the parser accepts lifetime annotations in declaration position,
`[SameObject]` getters emit a real receiver-bound lifetime instead of a
comment, and `[NewObject]` returns emit an owned/fresh lifetime where the
type system needs one. This requirement is blocked on the lifetime grammar
and is the bridge from "review aid" to "type-checks".

### FR10. CLI surface

`webidl_to_esc` reads one or more JSON artifacts and writes `<spec>.esc`,
either to a chosen output directory, alongside each artifact, or to stdout.
Conversion logic lives in `internal/webidl` so it is unit-testable without
the CLI. The Node extractor takes an output directory and an optional list
of spec names for slicing.

### FR11. Routing into the `web:*` layout

Generated `<spec>.esc` files are routed into the `web:*` pseudo-package tree
defined by the builtins workstream. The spec-to-package mapping is a
hand-maintained table, the same model as
`internal/interop/partition.go`'s `.d.ts` routing. A spec with no mapping
entry aborts the run rather than guessing a package.

### FR12. `throws` extraction from spec algorithms

A second extractor consumes `ed/algorithms/<spec>.json` from `w3c/webref` and
emits a map keyed `Interface.method` to a sorted exception list. The
algorithm:

1. Load the algorithm files for the target spec and its cross-spec helpers.
   Index every algorithm by `href`.
2. Per algorithm, extract directly-thrown exceptions from throwing steps and
   the call edges to other algorithms it invokes.
3. Compute the transitive throw closure over the call graph to a fixpoint.
4. For each operation-named algorithm, emit its closed throw set.

The Go converter accepts this map and renders a `throws E1 | E2` clause on the
matching operation. WebIDL supplies no exception data, so this map is the only
source. Two coverage gaps are accepted for the first cut and tracked for
follow-up:

- **Cross-spec helpers.** A throw reachable only through an algorithm in a spec
  that was not loaded is missed. Loading the full `ed/algorithms/*` set closes
  it. The extractor reports the count of unresolved external edges so the gap
  is visible.
- **Terse method definitions.** webref captures algorithms written as explicit
  step lists. A one-line delegating method — "the `removeChild(child)` method
  steps are to return the result of pre-removing child" — is not captured as
  an operation-named node, so its throws are not attributed. Bridging
  method → concept needs the `ed/dfns/<spec>.json` extract, deferred to the
  follow-up. The proven closure machinery is unaffected; only the
  method-to-algorithm association is incomplete.

The full throw set for an operation is the union of these algorithm-derived
exceptions and the binding-layer `TypeError` that `[EnforceRange]` and
argument coercion imply (FR2's IDL signal).

## Non-functional requirements

- **Determinism.** Running either stage twice on the same inputs produces
  byte-identical output. Member order follows IDL source order so diffs are
  reviewable.
- **Reproducibility without npm.** A committed sample IR and `.esc` let a
  reviewer run the Go stage and read the result without `npm install`.
- **Isolation.** The Node tool has its own `package.json` and is outside the
  pnpm workspace, so its `npm`-managed deps do not perturb the workspace.
- **Regeneration cost.** An `@webref/idl` bump re-runs the Node stage only;
  the Go stage is unaffected unless the IR schema changes.

## Risks

- **Inconsistent extended-attribute coverage.** `[SameObject]` and
  `[NewObject]` are applied unevenly across specs. A hit is high-precision,
  but absence means "unknown", not "not a borrow". Generated lifetimes are
  therefore a floor, not a complete picture, and need human review.
- **Lifetime grammar dependency.** FR9 is the payoff and is blocked on
  parser work owned elsewhere. Until it lands, the lifetime output is
  advisory comments.
- **Two sources for one surface.** `web:*` builtins now have a WebIDL source
  while `std:*` and third-party types come from `.d.ts`. The two paths must
  agree on receiver mutability; FR3's shared classifier is what keeps them
  aligned, and any divergence is a bug in that shared code.
- **Spec churn.** `@webref/idl` tracks living specs and changes often. The
  hand-edit model gives intent over upstream churn but means Escalier's view
  can lag; a `--check` diff mode mitigates this.
- **Cross-spec references.** DOM types are split across many specs, and one
  spec's interface references types defined in another. The per-spec
  artifact model defers this; the routing table (FR11) and the builtins
  workstream's open-registry augmentation are where it is resolved.
- **Incomplete `throws` coverage.** The algorithm extractor misses throws in
  unloaded specs and throws on terse delegating methods (FR12). A generated
  `throws` clause is therefore a floor, not a guarantee — under-reporting is
  possible until both gaps close. This argues for hand-review of the
  generated clauses and for never treating an absent clause as "cannot
  throw".

## Testing strategy

- **Converter unit tests** in `internal/webidl` build small in-memory
  `Artifact`s and assert the full rendered class, covering each of the four
  signals, receiver classification, and the type mapping. See
  [internal/webidl/convert_test.go](../../internal/webidl/convert_test.go).
- **Type-mapping table tests** cover scalars, generics, unions, and
  nullability.
- **Golden samples.** The committed `samples/dom.esc` is regenerated and
  diffed in CI so extractor or converter drift is caught.
- **Round-trip parse** (follow-up). Once the lifetime grammar lands,
  generated `.esc` is fed through `parser.ParseLibFiles` to assert it parses.
