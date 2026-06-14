# WebIDL-Sourced Types: Implementation Plan

This plan implements [requirements.md](requirements.md). Phases are ordered
so each is independently testable. The prototype already landed phases 1–4;
each section below gives the data structures and algorithms it needs,
referring to the prototype code where it exists and sketching new types where
it does not.

## Status

Status legend: ✅ done, 🚧 partial, ⬜ not started.

| §   | Topic                                   | Status | Depends on | Notes |
| --- | --------------------------------------- | ------ | ---------- | ----- |
| 1   | Feasibility spike                       | ✅      | —          | Confirmed `@webref/idl` carries `[SameObject]`/`[NewObject]`; WebIDL is a lifetime seed, not a mutability oracle. |
| 2   | Node extractor + JSON IR                | ✅      | 1          | [tools/webidl_to_esc/extract.mjs](../../tools/webidl_to_esc/extract.mjs); IR schema in [internal/webidl/ir.go](../../internal/webidl/ir.go). |
| 3   | Go converter (`internal/webidl`)        | ✅      | 2          | [internal/webidl/convert.go](../../internal/webidl/convert.go). |
| 4   | CLI + samples + tests                   | ✅      | 3          | [tools/webidl_to_esc/main.go](../../tools/webidl_to_esc/main.go), `samples/dom.{json,esc}`, `convert_test.go`. |
| 5   | Coverage: dictionaries, enums, typedefs | ⬜      | 3          | New IR node kinds + render functions. |
| 6   | `iterable`/`maplike`/`setlike`          | ⬜      | 5          | Expand to protocol members. |
| 7   | Cross-spec references + routing table   | ⬜      | 3          | `Universe` merge + spec→package routing. |
| 8   | `--check` diff mode                     | ⬜      | 4          | Regenerate-and-diff CI guard. |
| 9   | Lifetime syntax emission                | ⬜      | parser     | Turn FR5/FR6 comments into annotations. |
| 10  | Static-side modelling refinement        | ⬜      | 3          | Match the builtins static/constructor shape. |
| 11  | Integration into `web:*` `.esc` tree    | ⬜      | 7, 10      | Group-by-package, write, scaffold. |

## 1. Feasibility spike — done

No data structures. The spike installed `@webref/idl` + `webidl2`, dumped the
parsed AST for `Element`, and confirmed `readonly`, `[SameObject]`,
`[NewObject]`, and `[PutForwards]` are present and that `.d.ts` drops the
ownership attributes. Outcome recorded in
[requirements.md](requirements.md) §"Background".

## 2. Node extractor + JSON IR — done

### Data structures

The IR is defined twice, in lockstep: as the emitted JSON shape in
`extract.mjs` and as the Go structs that consume it in
[internal/webidl/ir.go](../../internal/webidl/ir.go). The Go structs are the
schema of record — `Artifact`, `Interface`, `Include`, `Member`, `Arg`, and
the recursive `TypeRef`. The one non-obvious choice is `TypeRef`:

```go
type TypeRef struct {
	Union    bool      // when true, Args are the union members
	Name     string    // base or generic name; "" when Union
	Args     []TypeRef // generic args or union members
	Nullable bool
}
```

Keeping types structured rather than stringified is what lets the Go side map
`sequence<DOMString>` or `(Event or undefined)` without re-parsing.

### Algorithm

`main()` in `extract.mjs`:

1. `const parsed = await idl.parseAll()` — a map of spec name to `webidl2`
   AST array.
2. For each spec, walk top-level nodes. `interface` and `interface mixin`
   become an `Interface` via `convInterface`; `includes` becomes an
   `Include`. Other node types are skipped here and added in §5.
3. `convMember` switches on `m.type` and projects each member to the IR,
   reading extended attributes through `extInfo`, which returns the set of
   ext-attr names plus the `[PutForwards]` target.
4. `convType` recurses: a union projects `idlType[]` to `Args`; a generic
   projects its inner `idlType` to `Args` under the generic `Name`; a base
   type stores its string `Name`. `Nullable` is read at each level.
5. Write `<spec>.json` per spec that has at least one interface.

## 3. Go converter — done

### Data structures

The converter is stateless over a `strings.Builder`. Its one local data
structure is the merge index built by `merged`:

```go
bases  := map[string]*Interface{} // name -> accumulating base interface
mixins := map[string]Interface{}  // name -> interface mixin
order  := []string{}              // base names in first-seen order, for stable output
```

### Algorithm

`ConvertArtifact` →

1. `merged(a)`: classify each `Interface` as a base or a mixin. Same-name
   bases accumulate their `Members`. Then for each `Include`, append the
   mixin's members to the target base. Return bases in `order`.
2. For each merged interface, `writeClass` emits `declare class Name [extends
   Inheritance] { … }`, instance members before statics.
3. `writeOperation` chooses the receiver:

   ```go
   mut, ok := interop.ClassifyMethodByName(m.Name)
   if !ok { mut = true }            // tier-7 default
   recv := "self"; if mut { recv = "mut self" }
   ```

   `[NewObject]` wraps the return in `mut`; an unmatched name or a
   `[NewObject]` hit adds a trailing review note.
4. `writeAttribute` emits a `self` getter, tags `[SameObject]` as a borrow,
   and adds a `mut self` setter for writable or `[PutForwards]` attributes.
5. `MapType` recurses over `TypeRef`: unions join with `|`; the generic names
   in `mapNamed` fold to `Array`/`Promise`/`Record`; `scalarMap` covers the
   primitive families; an unknown name passes through; `Nullable` appends
   `| null`.

## 4. CLI, samples, tests — done

### Data structures

`run` in `main.go` holds only flag state: `outDir string`, `toStdout bool`,
and the positional artifact paths. Each file is unmarshalled into a
`webidl.Artifact`.

### Algorithm

For each path: read, `json.Unmarshal` into `Artifact`, `ConvertArtifact`, then
write to stdout, alongside the artifact, or under `-o`. Tests build in-memory
`Artifact`s and assert the full rendered class.

## 5. Coverage: dictionaries, enums, typedefs

### Data structures

Add four IR node kinds and widen `Artifact`:

```go
type Artifact struct {
	Spec         string
	Interfaces   []Interface
	Includes     []Include
	Dictionaries []Dictionary // new
	Enums        []Enum       // new
	Typedefs     []Typedef    // new
	Callbacks    []Callback   // new
}

type Dictionary struct {
	Name        string
	Inheritance *string
	Partial     bool
	Members     []DictMember
}
type DictMember struct {
	Name     string
	Type     *TypeRef
	Required bool
	Default  *string
}
type Enum struct {
	Name   string
	Values []string
}
type Typedef struct {
	Name string
	Type *TypeRef
}
type Callback struct {
	Name   string
	Return *TypeRef
	Args   []Arg
}
```

### Algorithm

- **Extractor:** add `dictionary`, `enum`, `typedef`, `callback`, and
  `callback interface` cases to the top-level walk in `main()`. Dictionary
  members carry `required` and `default` straight from the `webidl2` member.
- **Converter render functions:**
  - dictionary → `interface Name [extends Inheritance] { name: T, opt?: T }`;
    `Required` members are non-optional, the rest get `?`. Same-name partials
    merge through the existing `merged`-style fold, generalised to a
    `mergeDicts` helper.
  - enum → `type Name = "a" | "b" | …` built from `Values`.
  - typedef → `type Name = MapType(Type)`.
  - callback → `type Name = fn(MapType(args)…) -> MapType(Return)`.

Gate: each form renders to readable `.esc`; once §9's grammar work allows,
feed the output through `parser.ParseLibFiles` to assert it parses.

## 6. Iterable / maplike / setlike

### Data structures

Extend `Member` with the declaration shape rather than adding a new kind, so
they flow through the same instance-member loop:

```go
// Member, additional fields:
Declaration  string   // "iterable" | "maplike" | "setlike" | ""
KeyType      *TypeRef // pair-iterable / maplike key; nil otherwise
ValueType    *TypeRef // element / value type
ReadonlyDecl bool     // readonly maplike / setlike
```

### Algorithm

A new `expandDeclaration(m Member) []Member` desugars each declaration into
ordinary attributes and operations before rendering, so receiver mutability
flows through `ClassifyMethodByName` unchanged:

- `iterable<V>` → `[Symbol.iterator]`, `values`, `keys`, `entries`,
  `forEach`. `[Symbol.iterator]` is already on the well-known non-mutating
  allow-list in `interop`, so it classifies as `self`.
- `iterable<K, V>` (pair iterable) → the same set typed over `K`/`V`.
- `maplike<K, V>` → `get`, `has`, `size`, plus `set`, `delete`, `clear`
  unless `ReadonlyDecl`. `get`/`has` classify `self`; `set`/`delete`/`clear`
  classify `mut self` — both already correct under the name heuristics.
- `setlike<V>` → `has`, `size`, plus `add`, `delete`, `clear` unless
  `ReadonlyDecl`.

The desugaring runs in `writeClass` before the instance/static split. The
TODO line the prototype currently emits for these is removed once
`expandDeclaration` covers them.

## 7. Cross-spec references and the routing table

### Data structures

Cross-spec resolution needs a whole-universe index, not per-spec maps:

```go
type Universe struct {
	Interfaces   map[string]*Interface  // merged across all specs, by name
	Mixins       map[string]Interface   // by name
	Dictionaries map[string]*Dictionary // by name
	SpecOf       map[string]string      // type name -> declaring spec
}
```

Routing mirrors `internal/interop/partition.go`. A hand-maintained table maps
a spec to its `web:*` package, with per-type overrides for the cases where
one spec spans several packages:

```go
var SpecToPackage = map[string]string{
	"dom":    "web:dom",
	"html":   "web:html",
	"cssom":  "web:cssom",
	// …
}
// Per-type overrides win over the spec default.
var TypeToPackage = map[string]string{ /* exceptions */ }

type RouteResult struct {
	Package  string
	Unmapped bool // fail-safe: abort the run, like interop.Route
}
```

### Algorithm

1. **Load.** Read every artifact and merge into one `Universe`. Folding
   partials and applying `includes` happens globally here, replacing the
   per-artifact `merged`: a mixin declared in spec A and included by an
   interface in spec B now resolves because both live in `Universe`.
2. **Route.** For each interface, consult `TypeToPackage` then
   `SpecToPackage`. An unrouted type sets `Unmapped` and aborts the run, the
   same fail-safe `interop.Route` uses for unmapped `.d.ts` symbols.
3. **Group.** Bucket interfaces by package into `map[string][]Interface`.
4. **Emit.** Render each bucket to its package's `.esc`. A type reference
   that lands in another package needs no special handling at this layer —
   the builtins workstream's `web:*` import and open-registry augmentation
   resolve it; the converter only needs to know which package a name belongs
   to, which `Universe.SpecOf` plus the routing table provide.

## 8. `--check` diff mode

### Data structures

None beyond the existing per-spec output. The check compares two byte
slices: freshly rendered output and the committed file.

### Algorithm

Mirror `tools/dts_to_esc`'s check shape:

1. Render every routed package to an in-memory `map[string][]byte`.
2. For each, read the committed `.esc` and compare bytes.
3. On any difference, print a unified diff and exit non-zero. Use this in CI
   to catch extractor/converter drift and to flag an `@webref/idl` bump.

## 9. Lifetime syntax emission

### Data structures

A render flag plus a small annotation carrier, so the comment path and the
real-syntax path share one code path:

```go
type RenderOptions struct {
	EmitLifetimes bool // false today: emit comments; true once the grammar lands
}
```

The borrow relationship is already implicit in the IR: `SameObject` on an
attribute means "result borrows from `self`", and `NewObject` means "result
is freshly owned". No new IR field is needed — only a different rendering of
the same flags.

### Algorithm

When `EmitLifetimes` is true:

- `[SameObject]` getter: emit a receiver-bound lifetime instead of the
  comment — a lifetime parameter on the getter, bound to `self`, and applied
  to the return type's `TypeRefType.LifetimeArgs`. The exact surface syntax
  follows whatever the parser accepts in declaration position; the unit tests
  in [internal/checker/tests/unify_lifetimes_test.go](../../internal/checker/tests/unify_lifetimes_test.go)
  show the `<'a>` shape the type system already models.
- `[NewObject]` return: keep the `mut` wrap and, where the type system needs
  to distinguish caller-owned from borrowed, emit an owned/fresh lifetime.

Until the grammar lands, `EmitLifetimes` stays false and the comments persist
so the signals are not lost. This phase is the transition from review aid to
type-checking output.

## 10. Static-side modelling refinement

### Data structures

A render strategy enum, chosen to match whatever the builtins workstream
fixes for the static/constructor shape:

```go
type StaticShape int
const (
	StaticOnClass     StaticShape = iota // statics + constructor inside `declare class`
	StaticCompanion                      // `declare val Name: { … }` companion
)
```

The prototype hard-codes `StaticOnClass`. The TS-trio model the builtins
workstream documents pushes statics into a `declare val Name: { … }`
companion (see the `dom.esc` fixture's `declare val FileReader`).

### Algorithm

Split `writeClass` into instance and static emitters keyed on `StaticShape`:

- `StaticOnClass`: today's behaviour — `static` members and `constructor`
  inside the class body.
- `StaticCompanion`: collect static members and constructors into a separate
  `declare val Name: { prototype: Name, new(args) -> Name, … }`, leaving the
  class body instance-only.

Pick the shape once the builtins layout is final so WebIDL and `.d.ts`
converters produce the same class shape and merge cleanly.

## 11. Integration into the `web:*` `.esc` tree

### Data structures

Reuse the partition writer's grouping shape so WebIDL output and `.d.ts`
output land in the same tree:

```go
// Parallel to interop.PartitionResult: package name -> rendered source.
type WebIDLResult struct {
	Packages map[string][]byte // "web:dom" -> file bytes
}
```

### Algorithm

1. Run §7's load → route → group to get `map[string][]Interface` per package.
2. Render each package, including §5/§6 forms.
3. Write under the builtins workstream's `internal/interop/data/web/` layout,
   mirroring `interop.WritePartitionedTree`, and scaffold any package README.
4. The generated `web:*` files become first-class source: committed,
   hand-edited for what WebIDL cannot supply — `throws`, and the receiver or
   argument mutation the heuristics miss — and regenerated only via §8's
   `--check`. This phase depends on the builtins workstream's `web:*` loading
   being in place.

## Estimated effort

Phases 1–4 are done. Phases 5–6 are a few days each of IR and converter work.
Phase 7 is the largest remaining chunk — the `Universe` merge and the routing
table. Phase 8 is small. Phase 9 is small once unblocked but gated on parser
work owned by another workstream. Phases 10–11 are paced by the builtins
workstream's `web:*` layout decisions.
