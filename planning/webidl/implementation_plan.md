# WebIDL-Sourced Types: Implementation Plan

This plan implements [requirements.md](requirements.md). Phases are ordered
so each is independently testable. The prototype already landed phases 1–4;
the table records that and lists the touch points for the remaining work.

## Status

Status legend: ✅ done, 🚧 partial, ⬜ not started.

| §   | Topic                                   | Status | Depends on | Notes |
| --- | --------------------------------------- | ------ | ---------- | ----- |
| 1   | Feasibility spike                       | ✅      | —          | Confirmed `@webref/idl` installs, parses, and carries `[SameObject]`/`[NewObject]`. Established WebIDL is a lifetime seed, not a mutability oracle. |
| 2   | Node extractor + JSON IR                | ✅      | 1          | [tools/webidl_to_esc/extract.mjs](../../tools/webidl_to_esc/extract.mjs); IR schema in [internal/webidl/ir.go](../../internal/webidl/ir.go). Captures interfaces, mixins, includes, and the four signals. |
| 3   | Go converter (`internal/webidl`)        | ✅      | 2          | [internal/webidl/convert.go](../../internal/webidl/convert.go). Receiver via `ClassifyMethodByName`, `[NewObject]`→`mut`, `[SameObject]`→borrow tag, type mapping, partial/mixin merge. |
| 4   | CLI + samples + tests                   | ✅      | 3          | [tools/webidl_to_esc/main.go](../../tools/webidl_to_esc/main.go), committed `samples/dom.{json,esc}`, `convert_test.go`. |
| 5   | Coverage: dictionaries, enums, typedefs | ⬜      | 3          | Extend IR + converter for the remaining top-level WebIDL forms. |
| 6   | `iterable`/`maplike`/`setlike`          | ⬜      | 5          | Model as `Iterator`/`Map`/`Set`-shaped members instead of the current TODO. |
| 7   | Cross-spec references + routing table   | ⬜      | 3          | Spec→`web:*` package map; resolve mixins and type refs across specs. |
| 8   | `--check` diff mode                     | ⬜      | 4          | Diff regenerated output against committed files; CI guard. |
| 9   | Lifetime syntax emission                | ⬜      | parser     | Blocked on the source-level lifetime grammar. Turns FR6/FR5 comments into real annotations. |
| 10  | Static-side modelling refinement        | ⬜      | 3          | Decide constructor/static-namespace shape to match the builtins layout. |
| 11  | Integration into `web:*` `.esc` tree    | ⬜      | 7, 10      | Wire generated output into the builtins workstream's package layout and prelude. |

## 1. Feasibility spike — done

Installed `@webref/idl` and `webidl2`, parsed `dom.idl`, and confirmed the
AST exposes `readonly`, `[SameObject]`, `[NewObject]`, and `[PutForwards]`.
Confirmed `.d.ts` drops the ownership attributes, which is the justification
for the whole workstream. Outcome recorded in
[requirements.md](requirements.md) §"Background".

## 2. Node extractor + JSON IR — done

`extract.mjs` calls `idl.parseAll()` and walks each spec's nodes:

- `interface` / `interface mixin` → an IR `Interface` with its members.
- `includes` → an IR `Include` recording target and mixin.

`convType` flattens a `webidl2` idlType into the recursive `TypeRef`;
`extInfo` collects extended-attribute names and the `[PutForwards]` target.
Output is one `<spec>.json` per spec. Dictionaries, enums, typedefs, and
callbacks are skipped at this stage and picked up in §5.

The IR is deliberately narrow so the Go side never depends on `webidl2`.
The Go structs in `ir.go` are the schema of record; the extractor must emit
matching field names.

## 3. Go converter — done

`ConvertArtifact` renders each merged interface as a `declare class`:

1. `merged` folds same-name partials together and applies `includes` mixins
   within the artifact.
2. Instance members render first, then statics, so a class reads from "what
   an instance can do" to "what the constructor exposes".
3. Operations get a receiver from `interop.ClassifyMethodByName`; an
   unmatched name falls to the tier-7 default and is flagged uncertain.
4. `[NewObject]` returns are wrapped in `mut`; `[SameObject]` getters are
   tagged as borrowing from `self`; writable and `[PutForwards]` attributes
   get a `mut self` setter.
5. `MapType` maps WebIDL types to Escalier types per FR8.

The converter takes no dependency on `dts_parser`. The only shared logic is
the name-based classifier, which is exactly the alignment FR3 requires.

## 4. CLI, samples, tests — done

`webidl_to_esc` reads JSON artifacts and writes `<spec>.esc` to a directory,
alongside the artifact, or to stdout. `convert_test.go` builds a small
`Artifact` exercising all four signals and asserts the full rendered class;
a table test covers `MapType`. `samples/dom.json` and `samples/dom.esc` are
committed so the Go stage runs without `npm install`.

## 5. Coverage: dictionaries, enums, typedefs

Extend the IR and converter to the remaining top-level forms:

- **dictionary** → an Escalier object type or interface, with `required`
  members non-optional and the rest optional.
- **enum** → a union of string literals.
- **typedef** → a type alias.
- **callback** / **callback interface** → a function type.

Touch points: add IR node kinds in `extract.mjs` and `ir.go`; add render
functions in `convert.go`. Gate: each form round-trips through the parser
once §9's grammar work is far enough along, or renders to a human-readable
`.esc` before then.

## 6. Iterable / maplike / setlike

These declaration shapes currently render as a TODO comment. Model them
against the ambient iterator protocol and `Map`/`Set` shapes from the
builtins workstream: `iterable<T>` contributes `[Symbol.iterator]`,
`maplike<K,V>` contributes the `Map` surface, `setlike<T>` the `Set`
surface. Reuse the well-known-symbol handling already in
`interop`'s classifier so receiver mutability on the generated members stays
consistent.

## 7. Cross-spec references and the routing table

Two coupled problems the per-spec artifact model defers:

1. **Routing.** A hand-maintained spec→`web:*` package table, mirroring
   `internal/interop/partition.go`. A spec with no entry aborts the run, the
   same fail-safe the `.d.ts` partitioner uses.
2. **Cross-spec mixins and type references.** A mixin defined in spec A and
   included by an interface in spec B must resolve. Build the mixin and
   interface maps across the whole artifact set before merging, rather than
   per-spec. Type references across specs resolve through the `web:*`
   package layout and the builtins workstream's open-registry augmentation.

## 8. `--check` diff mode

Add a mode that regenerates output and diffs it against the committed files
without overwriting, for a CI guard against extractor/converter drift and an
`@webref/idl` bump signal. Mirror `tools/dts_to_esc`'s `--check` shape.

## 9. Lifetime syntax emission

The payoff phase, blocked on the source-level lifetime grammar. When the
parser accepts lifetime annotations in declaration position:

- `[SameObject]` getters emit a receiver-bound lifetime instead of the
  current comment, tying the result's lifetime to `self`.
- `[NewObject]` returns emit an owned/fresh lifetime where the type system
  needs one to distinguish caller-owned from borrowed.

This is the transition from "review aid" to "type-checks". Until the grammar
lands, keep emitting comments so the signals are not lost.

## 10. Static-side modelling refinement

The prototype puts constructors and statics inside the same `declare class`.
Reconcile this with the builtins workstream's chosen static/constructor
shape — whether statics live on the class, in a `declare val Name: { … }`
companion, or in a namespace. Align the converter once that layout is fixed
so WebIDL output and `.d.ts` output produce the same class shape.

## 11. Integration into the `web:*` `.esc` tree

Wire the routed output (§7) into the builtins workstream's package layout
and prelude loading. Generated `web:*` `.esc` files become first-class
source: committed, hand-edited for the signals WebIDL cannot supply
(`throws`, argument and receiver mutation the heuristics miss), and
regenerated only as a `--check` review tool. This phase depends on the
builtins workstream's `web:*` loading being in place.

## Estimated effort

Phases 1–4 are done. Phases 5–6 are a few days of IR and converter work each.
Phase 7 is the largest remaining chunk — cross-spec resolution and the
routing table. Phase 9 is small once unblocked but gated on parser work owned
by another workstream. Phases 10–11 are paced by the builtins workstream's
`web:*` layout decisions.
