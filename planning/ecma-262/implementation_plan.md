# ECMA-262-Derived Builtin Annotations: Implementation Plan

This plan implements [requirements.md](requirements.md). Each phase
lists the touch points and the gate that proves it done. The pipeline
has three stages and the phases build them in dependency order:

```
ECMA-262 spec.html
   │  (ESMeta: extract → compile → build-cfg, pinned via -extract:target)
   ▼
control-flow graph  ──(thin Scala serializer)──▶  cfg.json  [committed]
   │
   ▼ (Go: origin tagging + mutation fixpoint + alias detection)
facts.json  [committed]
   │
   ▼ (Go: join + classification source)
bootstrap converter  ──▶  std/*.esc
```

The language boundary is the committed `cfg.json`. Everything left of it
is JVM/Scala and runs only on a spec bump. Everything right of it is Go
and runs in the normal build. The Scala component is a serializer with
no analysis; all Escalier-specific intelligence lives in the Go stages.

## Implementation order and status

Status legend: ✅ done, 🚧 partial, ⬜ not started.

| §   | Phase                                      | FRs        | Status | Depends on | Gate |
| --- | ------------------------------------------ | ---------- | ------ | ---------- | ---- |
| 1   | Feasibility spike                          | FR1–FR4    | ⬜      | —          | ESMeta CFG for ~10 representative methods shows the call nodes, args, slot writes, and returns the analysis needs |
| 2   | Toolchain scoping                          | NFR        | ⬜      | §1         | `tools/spec-extract/mise.toml` builds and runs ESMeta with no JVM in the root environment |
| 3   | Scala CFG→JSON serializer                  | FR6 (cfg)  | ⬜      | §2         | `cfg.json` emitted for the full `std:*` surface, pinned spec, round-trips a schema check |
| 4   | Go analysis: mutation + alias              | FR1–FR5    | ⬜      | §3         | `facts.json` produced from `cfg.json`; spot-checked methods classify correctly |
| 5   | Keying and join                            | FR7        | ⬜      | §4         | Normalizer joins facts to converter declarations; unmatched on both sides reported |
| 6   | Validation diff                            | FR9        | ⬜      | §5         | Receiver facts diffed against `mutabilityOverrides` + heuristics; every disagreement triaged |
| 7   | Integration as classification source       | FR8        | ⬜      | §6         | Converter ranks facts above name tiers; redundant `mutabilityOverrides` entries removed |
| 8   | Param-mut and lifetime-alias outputs       | FR2, FR4   | ⬜      | §7         | Param-mut emitted with non-mutating default; alias facts surfaced to lifetime hand-editing |
| 9   | Throw-set extraction + coercion filter     | FR10, FR11 | ⬜      | §4, §7     | Throw set computed by the §4.1 fixpoint over guard-annotated calls; coercion filter prunes type-guard `TypeError`s; surviving domain throws land in `facts.json` |
| 10  | Maintenance workflow                       | NFR        | ⬜      | §7         | Spec-bump runbook; `--check`-style drift report in CI |

**Dependency graph** (edges are "must land before"):

```
§1 ── §2 ── §3 ── §4 ── §5 ── §6 ── §7 ──┬── §8
                                          ├── §9  (throws)
                                          └── §10 (maintenance)
```

## §1. Feasibility spike

**Goal.** Confirm ESMeta's control-flow graph carries the structure the
analysis needs before committing to the toolchain.

**Work.**

- Build ESMeta from source — clone `es-meta/esmeta`,
  `git submodule update --init`, `sbt assembly` — and run
  `extract → compile → build-cfg` against a pinned ECMA-262 revision.
- Inspect the CFG for a representative set spanning every shape the
  analysis must handle:
  - direct receiver mutation, `Array.prototype.push`;
  - receiver mutation that returns the receiver, `Array.prototype.fill`,
    `Array.prototype.sort`;
  - fresh allocation, no receiver mutation, `Array.prototype.slice`,
    `Array.prototype.map`;
  - internal-slot mutation, `Map.prototype.set`,
    `Set.prototype.add`;
  - transitive mutation through a helper abstract operation;
  - immutable-primitive method, `String.prototype.replace`,
    `String.prototype.charAt`;
  - symbol-keyed method, `Array.prototype[Symbol.iterator]`.

**Gate.** For each method above, confirm the CFG exposes: the abstract-
operation call nodes with their argument variables, the `Let` bindings
and their origins, the internal-slot writes, and the return values.
If any signal is missing from the CFG, record it and fall back to the
pure-Go `spec.html` shallow parser noted in §3 alternatives.

## §2. Toolchain scoping

**Goal.** Make the JVM toolchain a maintainer-only dependency, absent
from the normal Go build and CI.

**Work.**

- Add `tools/spec-extract/mise.toml` with `java = "temurin-21"`
  (satisfies ESMeta's JDK 17+) and `sbt = "1.x"`. Do **not** add these
  to the root `mise.toml`, which every contributor and CI activates.
- Vendor ESMeta as a git submodule under `tools/spec-extract/` so the
  pinned ESMeta revision is reproducible alongside the pinned spec
  revision. ESMeta has no published Maven artifact and no prebuilt JAR,
  so source vendoring is the only stable option.

**Gate.** A maintainer can `cd tools/spec-extract && mise install` and
build ESMeta; a contributor building the compiler from the repo root
never installs Java or sbt.

## §3. Scala CFG→JSON serializer

**Goal.** Give ESMeta's in-memory `esmeta.cfg.CFG` a committed JSON
spelling. This is the only Scala we write, and it contains no analysis.

**Work.**

- Add a small Scala main in `tools/spec-extract/` that depends on the
  vendored ESMeta build, runs `extract → compile → build-cfg` with
  `-extract:target` set to the pinned ECMA-262 revision, and walks each
  `cfg.Func`.
- Lower each ESMeta IR node to the flat, analysis-ready schema in
  [Appendix A](#appendix-a-cfgjson-schema). The serializer's only job is
  this lowering. It pattern-matches ESMeta IR instruction types onto the
  `Node` and `Expr` variants and copies structure; it makes no
  mutability or alias decision. The shape it must surface per function:
  the formal parameters in order, with the receiver as index 0 for
  builtin methods; every `Let` binding's target and source; every
  abstract-operation call with its callee name, argument expressions,
  and completion guard (`?` / `!` / plain, needed for the throw-set
  fixpoint in §9); every internal-slot write with its object expression
  and slot name; every explicit `Throw` step with its exception type;
  every return with its value expression.
- Write the result to `tools/spec-extract/cfg.json` and commit it. The
  file is large; it is an intermediate regenerated only on a spec bump,
  and committing it is what keeps the JVM out of the normal build.

**Schema sketch** (full definitions in
[Appendix A](#appendix-a-cfgjson-schema)):

```go
type CFG struct{ Funcs []Func }

type Func struct {
    Name   string   // "Array.prototype.push", "Array.from", or an AO name "Set"
    Kind   FuncKind // BuiltinMethod | BuiltinStatic | AbstractOp | SyntaxDirected
    Params []string // formals in order; index 0 is the receiver for BuiltinMethod
    Nodes  []Node   // flattened, control-flow-edge order preserved
}

type Node struct {
    Kind   NodeKind // Let | Call | SlotWrite | Return | Branch
    Target string   // Let: bound name        | Call: optional result name
    Source *Expr    // Let: bound expression
    Callee string   // Call: abstract-operation name
    Args   []Expr   // Call: argument expressions
    Object *Expr    // SlotWrite: the object whose slot is written
    Slot   string   // SlotWrite: e.g. "[[MapData]]"
    Value  *Expr    // Return: returned expression
}
```

**Alternative if §1 fails.** If the CFG does not carry the needed
structure, fall back to a pure-Go shallow parser over the pinned
`spec.html` using `golang.org/x/net/html`, exploiting ECMARKUP's
structural markup — `aoid` attributes on `<emu-xref>` call nodes,
`<var>` for variables, literal `.[[Slot]]` text. It emits the same
[Appendix A](#appendix-a-cfgjson-schema) schema, so the Go stage is
unchanged. The shallow parser drops the JVM entirely but must
reconstruct the call graph itself and gives up accuracy on
indirectly-phrased mutations. Keep the §1 CFG dump as a one-time oracle
to validate the shallow parser's output.

**Gate.** `cfg.json` covers the full `std:*` method surface and
round-trips a schema validation. The schema is the contract the Go stage
reads.

## §4. Go analysis: mutation and alias

**Goal.** Produce `facts.json` from `cfg.json` entirely in Go. The
analysis has three passes: an inter-procedural mutation summary
(§4.1), a per-function origin map (§4.2), and a per-method
classification that combines them (§4.3).

The unifying idea is that **the receiver is formal parameter 0**. Every
function — builtin method or abstract operation — is analyzed for which
of its formal positions it mutates. For a builtin method, position 0
mutated means `mut self`, and position `j ≥ 1` mutated means parameter
`j-1` is `mut`.

### §4.1. Mutation summary fixpoint (FR1, FR2, FR3)

Compute `MutArgs(F) ⊆ {0..arity-1}`, the formal positions function `F`
may mutate, directly or transitively. Seed it from the direct mutators
and iterate to a fixpoint over the call graph.

```
MutArgs : map[FuncName] Set[int]
Unattributable : Set[FuncName]   // F mutates something not tied to a formal

// Seed: direct property/integrity mutators mutate their object argument (arg 0).
seed = {
    "Set":0, "CreateDataProperty":0, "CreateDataPropertyOrThrow":0,
    "CreateMethodProperty":0, "DefinePropertyOrThrow":0,
    "OrdinaryDefineOwnProperty":0, "DeletePropertyOrThrow":0,
    "SetIntegrityLevel":0,
}
for ao, k in seed: MutArgs[ao].add(k)

worklist = all funcs
while worklist nonempty:
    F = worklist.pop()
    origin = OriginMap(F)               // §4.2, computed per function
    before = (MutArgs[F].copy(), F in Unattributable)

    for node in F.Nodes:
        switch node.Kind:
        case SlotWrite where node.Slot in BackingStoreSlots:   // FR3
            attribute(F, origin, node.Object)
        case Call:
            for k in MutArgs[node.Callee]:
                attribute(F, origin, node.Args[k])
    // (a fresh-origin mutation is intentionally ignored: mutating a
    //  value F allocated itself is not observable to F's callers.)

    if changed(before, F): worklist.push(callers(F))

// attribute: charge a mutated argument expression to F's formal, if any.
func attribute(F, origin, expr):
    switch originOf(origin, expr):
    case Param(j):   MutArgs[F].add(j)
    case Fresh:      pass                 // not observable
    case Unknown:    Unattributable.add(F)
```

`BackingStoreSlots` is the curated FR3 list: `[[MapData]]`,
`[[SetData]]`, `[[ArrayBufferData]]`, `[[ArrayBufferByteLength]]`,
`[[TypedArrayName]]`, `[[ViewedArrayBuffer]]`, `[[WeakRefTarget]]`, and
others added as collection types enter the spec. Both the seed map and
this list are reviewed Go constants — adding a mutator to the spec
without listing it here produces a false non-mutating result, so they
are deliberately explicit (FR1).

### §4.2. Origin map (FR2, FR4)

For each function, map every value name to its origin by a forward pass.
Origins propagate only through **identity-preserving** operations; a
property or slot *read* breaks the origin chain, because the value read
out of a container is a different object from the container.

```
type Origin struct { Kind OriginKind; Index int }   // Receiver=Param(0)
// OriginKind ∈ { Param, Fresh, Unknown }

func OriginMap(F) map[string]Origin:
    origin = {}
    for i, p in F.Params: origin[p] = Param(i)       // receiver is Param(0)
    for node in F.Nodes:
        if node.Kind == Let:   origin[node.Target] = eval(node.Source)
        if node.Kind == Call && node.Target != "":
                               origin[node.Target] = evalCall(node)
    return origin

func eval(e Expr) Origin:
    switch e.Kind:
    case Var:   return origin[e.Var]
    case This:  return Param(0)
    case Call:  return evalCall(e)
    case Alloc, Lit: return Fresh                     // fresh object / primitive
    case Slot, Prop: return Unknown                   // a READ: origin chain breaks
    default:    return Unknown

func evalCall(c) Origin:
    if c.Callee in Allocators:      return Fresh      // ArrayCreate, ArraySpeciesCreate,
                                                      // OrdinaryObjectCreate, ...
    if c.Callee in IdentityCoercions:                 // ToObject, RequireObjectCoercible
        return eval(c.Args[0])                        // returns the same object identity
    return Unknown                                    // Get, ToString, ToNumber, ... → read/fresh
```

`IdentityCoercions` is the key list for receiver tracking: `ToObject`
and `RequireObjectCoercible` return the same object, so `O ← ?
ToObject(this value)` keeps `O` at `Param(0)`. Coercions that build a
new value — `ToString`, `ToNumber` — are *not* identity-preserving,
which is exactly why every `String.prototype` method comes out
non-mutating: the algorithm coerces `this` to a fresh string primitive
and never writes back to `Param(0)`.

A branch that merges two origins for the same name joins them: equal
origins stay; unequal collapse to `Unknown`. Origins are otherwise
flow-insensitive, so a single forward pass with a join at merge points
suffices; no full dataflow lattice is needed.

### §4.3. Method classification (FR4, FR5)

For each builtin method `M`, combine the summary and the origin map:

```
func classify(M) MethodFact:
    fact.MutatesReceiver = 0 in MutArgs[M]
    fact.MutatesParams   = [ (j-1) for j in MutArgs[M] if j >= 1 ]
    fact.Returns         = returnAlias(M)              // below
    // Soundness bias (FR5):
    fact.Classified = M not in Unattributable
    return fact

func returnAlias(M) AliasKind:
    acc = Bottom
    for node in M.Nodes where node.Kind == Return:
        acc = join(acc, aliasOf(eval(node.Value)))
    return acc
// aliasOf: Param(0)→Receiver; Param(j)→ParamJ; Fresh→FreshReturn; Unknown→UnknownReturn
// join:    equal→same; FreshReturn⊔FreshReturn→FreshReturn;
//          two distinct input origins→Union; anything⊔UnknownReturn→UnknownReturn
```

An `Unattributable` method has a mutation the analysis could not pin to
a formal — a write through an `Unknown`-origin value, including deep
mutation reached through a property read. It is emitted with
`classified: false` and listed, so the converter falls it through to the
name heuristics and the receiver defaults to mutating (FR5). The
return-alias axis tolerates `Unknown` without making the whole method
unclassified, because aliasing is the low-stakes lifetime seed, not a
soundness-bearing claim.

**Gate.** Spot-check the representative methods from §1:
- `Array.prototype.push` — `Set(O,…)` on `Param(0)` ⇒ `mutatesReceiver`,
  returns `len` ⇒ `fresh`.
- `Array.prototype.fill` — `Set(O,…)`, `Return O` ⇒ `mutatesReceiver`,
  `returns: receiver`.
- `Array.prototype.slice` — writes only to an `ArraySpeciesCreate`
  result `A` (Fresh), `Return A` ⇒ not `mutatesReceiver`, `fresh`.
- `Map.prototype.set` — append to `M.[[MapData]]` (backing-store slot on
  `Param(0)`), `Return M` ⇒ `mutatesReceiver`, `returns: receiver`.
- every `String.prototype` method — `this` coerced to a fresh string,
  never written ⇒ all non-mutating.

Unclassified methods are listed.

## §5. Keying and join (FR7)

**Goal.** Join spec-keyed facts to the converter's class+method
declarations.

**Work.**

- Implement the name normalizer that maps a spec key onto the
  `(owner, member, sort)` triple the converter holds. `owner` is a dotted
  path so namespace-nested constructors resolve; `sort` distinguishes an
  instance method, a class static, and a namespace-level function:

```
func normalize(specKey string) (owner []string, member MemberKey, sort MemberSort):
    // MemberSort ∈ { Instance, Static, NamespaceFunc }
    // "Array.prototype.push"                   → (["Array"],     Str("push"),     Instance)
    // "Array.prototype [ @@iterator ]"         → (["Array"],     Sym("iterator"), Instance)
    // "get Map.prototype.size"                 → (["Map"],       Accessor("size"),Instance)  // never overwritten
    // "Array.from"                             → (["Array"],     Str("from"),     Static)
    // "Array [ @@species ]"                    → (["Array"],     Sym("species"),  Static)
    // "Math.max"                               → (["Math"],      Str("max"),      NamespaceFunc)
    // "Intl.getCanonicalLocales"               → (["Intl"],      Str("getCanonicalLocales"), NamespaceFunc)
    // "Intl.DateTimeFormat.prototype.format"   → (["Intl","DateTimeFormat"], Str("format"),  Instance)
    // "Intl.DateTimeFormat.supportedLocalesOf" → (["Intl","DateTimeFormat"], Str("supportedLocalesOf"), Static)
```

  The split rule: strip a trailing `.prototype.<member>` or
  `[ @@symbol ]` to get an instance member; otherwise the last dotted
  segment is the member and the leading segments are the owner. An owner
  whose leading segment is a known namespace (`Intl`, `Math`, `Reflect`,
  `JSON`, `Atomics`, `WebAssembly`) and that has no further constructor
  segment yields `NamespaceFunc`; an owner ending in a constructor name
  yields `Instance`/`Static`. The known-namespace set is a small reviewed
  list, the same one FR7 enumerates.
  `MemberKey` mirrors `type_system.ObjTypeKey` so symbol-keyed members
  join by kind plus payload, matching how
  [../../internal/interop/mutability.go](../../internal/interop/mutability.go)
  already distinguishes string- from symbol-keyed names. A
  `NamespaceFunc` has no receiver, so the join applies only its
  `mutatesParams` and `throws`, never `mutatesReceiver`.
- A spec algorithm maps to a single method element even when the
  TypeScript side is an overload set; the fact applies to **all**
  signatures of the merged `MethodElem`, the same iteration
  `applyMethodMutability` in
  [../../internal/checker/prelude.go](../../internal/checker/prelude.go)
  already does over `me.Signatures`.
- Skip accessor members: spec getters/setters carry fixed mutability set
  by the converter, so the normalizer tags them and the join refuses to
  overwrite them, matching the `GetterElem`/`SetterElem` carve-out in
  `applyMethodMutability`.
- Wire the lookup into the bootstrap converter (`tools/dts_to_esc/`,
  [../../internal/interop/dts_to_esc.go](../../internal/interop/dts_to_esc.go))
  so a converted method element can resolve its fact.
- Report names present on one side only, mirroring the converter's
  unmapped-symbol fail-safe
  ([../../internal/interop/partition.go](../../internal/interop/partition.go)).
  A fact with no declaration and a declaration with no fact are both
  informational, since the spec and the TS lib drift independently.

**Gate.** Every `std:*` method the converter emits either resolves to a
fact or is reported as unmatched; symbol-keyed and accessor members
resolve correctly.

## §6. Validation diff

**Goal.** Prove the facts source before trusting it.

**Work.**

- Diff the receiver-mutability facts against the union of
  `mutabilityOverrides`
  ([../../internal/checker/prelude.go](../../internal/checker/prelude.go))
  and `interop.ClassifyMethodByName`
  ([../../internal/interop/mutability.go](../../internal/interop/mutability.go))
  for the same methods.
- Triage every disagreement: facts correct and override redundant, or
  facts buggy and the §4 analysis fixed.

**Gate.** A reviewed disagreement report with a disposition for each
entry. This is the gate that authorizes removing override entries in §7.

## §7. Integration as classification source

**Goal.** Make the converter rank facts above the name tiers.

**Work.**

- Insert the facts lookup into `interop.Classify` at rung 2 (FR8):
  after explicit author signals, before the `get*` prefix and name
  heuristics.
- Set receiver mutability from a classified fact; leave unclassified
  methods to the existing tiers.
- Remove the `mutabilityOverrides` entries that §6 proved redundant for
  `std:*`. Keep entries the facts source does not cover, such as `web:*`
  classes, untouched.

**Gate.** Converter output for `std:*` matches the facts for every
classified method; the removed override entries cause no regression in
the converter and checker test suites.

## §8. Param-mut and lifetime-alias outputs

**Goal.** Surface the two secondary determinations.

**Work.**

- Emit per-parameter mutability with the non-mutating default and
  invariant-unification caution from the requirements; require positive
  spec evidence to mark a param `mut`.
- Surface the return-alias facts to whoever hand-edits lifetime
  annotations on the committed `.esc` files, as review input rather than
  an automatic annotator. The checker's lifetime inference and elision
  rules ([../lifetimes/requirements.md](../lifetimes/requirements.md))
  remain the mechanism; the facts only inform the hand edits.

**Gate.** Param-mut facts present in `facts.json`; a documented mapping
from `returns` facts to the lifetime annotations a human would write for
the receiver-returning methods (`fill`, `sort`, `reverse`, `Map.set`).

## §9. Throw-set extraction and coercion filter (FR10, FR11)

**Goal.** Produce the `throws` candidate set for each method, reusing the
§4 machinery with a throw transfer function and then pruning the
type-guard noise.

### §9.1. Throw-set fixpoint (FR10)

Compute `Throws(F) ⊆ ErrorType`, the exception types `F` can raise,
directly or transitively. The structure is identical to the §4.1
mutation-summary fixpoint: a worklist over the call graph, a per-call
transfer, re-enqueue callers on change. The transfer differs and depends
on each call's completion guard, which §3 now records on the `Node`.

```
Throws : map[FuncName] Set[ErrorType]   // {TypeError, RangeError, ...}
ThrowSites : map[FuncName] []ThrowSite  // provenance for the §9.2 filter

worklist = all funcs
while worklist nonempty:
    F = worklist.pop()
    before = Throws[F].copy()
    for node in F.Nodes:
        switch node.Kind:
        case Throw:                                 // explicit "Throw a T exception"
            Throws[F].add(node.ErrorType)
            ThrowSites[F].append(ThrowSite{ Type: node.ErrorType, Origin: Direct })
        case Call:
            switch node.Guard:
            case GuardBang:    pass                  // ! asserts no abrupt completion
            case GuardPlain:   pass                  // result not completion-checked
            case GuardQuestion:                      // ? propagates the callee's throws
                for t in Throws[node.Callee]:
                    Throws[F].add(t)
                    ThrowSites[F].append(ThrowSite{ Type: t, Origin: Via(node.Callee) })
    if Throws[F] != before: worklist.push(callers(F))
```

`ThrowSite.Origin` records whether a throw is raised directly in `F` or
propagated from a named callee. The §9.2 filter reads this provenance to
decide whether a throw is a coercion type-guard. There is no seed map as
in §4.1; throws originate only at explicit `Throw` nodes and flow
outward through `?`.

### §9.2. Coercion filter (FR11)

Prune throws whose provenance is a coercion of an already-typed receiver
or parameter, because Escalier's static types make those paths
unreachable.

```
CoercionAOs = { ToObject, RequireObjectCoercible,
                ToString, ToNumber, ToNumeric, ToPrimitive }

func filterThrows(M) []ErrorType:
    kept = {}
    for site in ThrowSites[M]:
        if site.Type == TypeError && viaCoercionOfTypedValue(M, site):
            continue                                  // statically precluded
        kept.add(site.Type)
    return sorted(kept)

// viaCoercionOfTypedValue: the throw propagated (via ?) from a coercion
// AO whose argument origin is the receiver or a parameter. Those
// arguments carry a known Escalier type, so the coercion cannot fail.
func viaCoercionOfTypedValue(M, site) bool:
    if site.Origin is Via(callee) && callee in CoercionAOs:
        arg0 = argOriginAtCallSite(M, callee)         // §4.2 origin of the coerced value
        return arg0 is Param(_)                        // receiver = Param(0) included
    return false
```

A `RangeError`, `SyntaxError`, `URIError`, or a `TypeError` raised by an
explicit domain check rather than a coercion survives. The kept set is
written to `MethodFact.Throws` (Appendix B). Each filter decision is
recorded for review, since FR11 is a heuristic.

**Gate.** Spot-check: `Number.prototype.toFixed` keeps `RangeError`
(out-of-range `fractionDigits`) and drops the receiver-coercion
`TypeError`; `decodeURIComponent` keeps `URIError`; `Array.prototype.push`
keeps nothing. The dropped type-guard throws are listed in the review
report.

## §10. Maintenance workflow

**Goal.** Make spec-edition bumps a repeatable runbook.

**Work.**

- Document the bump: update the pinned `-extract:target`, rebuild
  `cfg.json` under `tools/spec-extract/`, re-run the Go analysis, review
  the `facts.json` diff, re-run the §6 validation.
- Add a CI check that re-runs the Go analysis over the committed
  `cfg.json` and fails if `facts.json` is stale, so the committed facts
  cannot drift from the committed CFG without the JVM.
- Add an informational drift report flagging spec methods that gained or
  lost a mutating operation since the last bump.

**Gate.** A bump runbook exists and a stale-facts CI check is green.

---

## Appendix A. `cfg.json` schema

The serialized control-flow graph. This is the contract between the
Scala serializer (§3) and the Go analysis (§4); it is the only shape
either side agrees on. The schema is provisional pending the §1 spike,
which confirms the ESMeta IR carries each field.

```go
type CFG struct {
    SpecTarget string  `json:"specTarget"` // pinned ecma262 git ref (-extract:target)
    Funcs      []Func  `json:"funcs"`
}

type FuncKind string
const (
    BuiltinMethod  FuncKind = "builtin-method"  // X.prototype.method; receiver is Params[0]
    BuiltinStatic  FuncKind = "builtin-static"  // X.method
    AbstractOp     FuncKind = "abstract-op"     // Set, ToObject, ArrayCreate, ...
    SyntaxDirected FuncKind = "syntax-directed" // evaluation semantics; mostly unused here
)

type Func struct {
    Name   string   `json:"name"`   // canonical spec key (Appendix C) or AO name
    Kind   FuncKind `json:"kind"`
    Params []string `json:"params"` // formal names, in order; index 0 = receiver for methods
    Nodes  []Node   `json:"nodes"`  // CFG nodes in control-flow order, branches flattened
}

type NodeKind string
const (
    NodeLet       NodeKind = "let"       // bind Target = Source
    NodeCall      NodeKind = "call"      // optional Target = Callee(Args...)
    NodeSlotWrite NodeKind = "slotwrite" // write Object.Slot
    NodeThrow     NodeKind = "throw"     // Throw a <ErrorType> exception
    NodeReturn    NodeKind = "return"    // return Value
    NodeBranch    NodeKind = "branch"    // control flow; carries no data we analyze
)

// Guard is the completion-record guard on a call, needed for the §9
// throw-set fixpoint. ? propagates abrupt completions; ! asserts none.
type Guard string
const (
    GuardQuestion Guard = "?"     // Let x be ? Foo(...)
    GuardBang     Guard = "!"     // Let x be ! Foo(...)
    GuardPlain    Guard = "plain" // result not completion-checked
)

type Node struct {
    Kind      NodeKind `json:"kind"`
    Target    string   `json:"target,omitempty"`    // Let target, or Call result binding
    Source    *Expr    `json:"source,omitempty"`    // Let
    Callee    string   `json:"callee,omitempty"`    // Call: abstract-operation name
    Args      []Expr   `json:"args,omitempty"`      // Call
    Guard     Guard    `json:"guard,omitempty"`     // Call: ? / ! / plain
    Object    *Expr    `json:"object,omitempty"`    // SlotWrite
    Slot      string   `json:"slot,omitempty"`      // SlotWrite, e.g. "[[MapData]]"
    ErrorType string   `json:"errorType,omitempty"` // Throw: "TypeError", "RangeError", ...
    Value     *Expr    `json:"value,omitempty"`     // Return
}

type ExprKind string
const (
    ExprVar  ExprKind = "var"  // a named value
    ExprThis ExprKind = "this" // the this value
    ExprLit  ExprKind = "lit"  // literal / primitive
    ExprCall ExprKind = "call" // nested AO call, e.g. ToObject(x)
    ExprSlot ExprKind = "slot" // READ of Object.Slot
    ExprProp ExprKind = "prop" // READ via Get(Object, Key) etc.
)

type Expr struct {
    Kind   ExprKind `json:"kind"`
    Var    string   `json:"var,omitempty"`    // ExprVar
    Callee string   `json:"callee,omitempty"` // ExprCall
    Args   []Expr   `json:"args,omitempty"`   // ExprCall
    Object *Expr    `json:"object,omitempty"` // ExprSlot / ExprProp
    Slot   string   `json:"slot,omitempty"`   // ExprSlot
}
```

The analysis never interprets `NodeBranch`; origins are joined at merge
points (§4.2) without modeling control flow explicitly. `ExprSlot` and
`ExprProp` are *reads* and deliberately resolve to `Unknown` origin, so
the origin chain breaks at a container access — this is what keeps deep
mutation through reads from being mis-attributed to the receiver.

## Appendix B. `facts.json` schema

The committed output of §4, consumed by the converter (§7). Small,
reviewable, keyed by canonical spec name.

```go
type Facts struct {
    SpecTarget string               `json:"specTarget"` // echoes CFG.SpecTarget
    Methods    map[string]MethodFact `json:"methods"`   // key: canonical spec name
}

type AliasKind string
const (
    AliasReceiver AliasKind = "receiver" // every return aliases the receiver
    AliasParam    AliasKind = "param"    // every return aliases ParamIndex
    AliasFresh    AliasKind = "fresh"    // returns only fresh / primitive values
    AliasUnion    AliasKind = "union"    // returns alias differing inputs
    AliasUnknown  AliasKind = "unknown"  // a return origin could not be resolved
)

type MethodFact struct {
    MutatesReceiver bool      `json:"mutatesReceiver"`
    MutatesParams   []int     `json:"mutatesParams"`         // zero-based param indices
    Returns         AliasKind `json:"returns"`
    ParamIndex      int       `json:"paramIndex,omitempty"`  // when Returns == "param"
    Throws          []string  `json:"throws"`                // domain error types post-filter (FR11)
    Classified      bool      `json:"classified"`            // false ⇒ FR5 fall-through
}
```

A `classified: false` entry carries no mutability claim; the converter
ignores its `mutatesReceiver`/`mutatesParams` and falls the method
through to the name heuristics. Such methods are also collected into a
separate `unclassified` report alongside `facts.json` for auditing
(FR5).

## Appendix C. Canonical spec keys

The shared key space between `cfg.json`, `facts.json`, and the §5
normalizer.

The host `X` is a dotted path, so a constructor nested in a namespace
(`Intl.DateTimeFormat`) is just a longer `X`.

| Form                          | Example                                  | Joins to                              |
| ----------------------------- | ---------------------------------------- | ------------------------------------- |
| `X.prototype.method`          | `Array.prototype.push`                   | instance method `push` on `X`         |
| `X.method`                    | `Array.from`                             | static method `from` on `X`           |
| `X.prototype [ @@symbol ]`    | `Array.prototype [ @@iterator ]`         | `[Symbol.iterator]` on `X`            |
| `X [ @@symbol ]`              | `Array [ @@species ]`                    | static `[Symbol.species]` on `X`      |
| `get X.prototype.accessor`    | `get Map.prototype.size`                 | getter `size` (not overwritten)       |
| `set X.prototype.accessor`    | `set …`                                  | setter (not overwritten)              |
| `Namespace.fn`                | `Math.max`, `Intl.getCanonicalLocales`   | namespace-level function (no receiver)|
| `Namespace.Class.prototype.m` | `Intl.DateTimeFormat.prototype.format`   | instance method on a nested ctor      |
| `Namespace.Class.method`      | `Intl.DateTimeFormat.supportedLocalesOf` | static method on a nested ctor        |

Namespace-level functions carry `Kind: builtin-static` in `cfg.json`:
like a class static they have no receiver, so the analysis treats index
0 of their `Params` as the first real argument, not a `this` value. The
§5 normalizer distinguishes a class static from a namespace function by
the known-namespace owner list; the analysis itself does not need the
distinction, because parameter mutation (`Reflect.set` writing its
`target`) flows through the same formal-index machinery either way.

Abstract operations referenced inside algorithms — `Set`, `ToObject`,
`ArrayCreate`, and the like — appear in `cfg.json` as `Func`s with
`Kind: abstract-op` keyed by their plain spec name. They feed the §4.1
fixpoint but never appear in `facts.json`, which holds only builtin
methods.
