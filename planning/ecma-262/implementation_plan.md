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
| 9   | Maintenance workflow                       | NFR        | ⬜      | §7         | Spec-bump runbook; `--check`-style drift report in CI |

**Dependency graph** (edges are "must land before"):

```
§1 ── §2 ── §3 ── §4 ── §5 ── §6 ── §7 ──┬── §8
                                          └── §9
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
- For each function emit a structural record: its name, its parameters,
  its nodes in order, each call node's callee abstract-operation name and
  argument variables, each `Let` binding's target and source expression,
  each internal-slot write's target and slot, and each return's value
  expression. The serializer copies structure; it makes no mutability or
  alias decision.
- Write the result to `tools/spec-extract/cfg.json` and commit it. The
  file is large; it is an intermediate regenerated only on a spec bump,
  and committing it is what keeps the JVM out of the normal build.

**Alternative if §1 fails.** If the CFG does not carry the needed
structure, fall back to a pure-Go shallow parser over the pinned
`spec.html` using `golang.org/x/net/html`, exploiting ECMARKUP's
structural markup — `aoid` attributes on `<emu-xref>` call nodes,
`<var>` for variables, literal `.[[Slot]]` text. The shallow parser
drops the JVM entirely but must reconstruct the call graph itself and
gives up accuracy on indirectly-phrased mutations. Keep the §1 CFG dump
as a one-time oracle to validate the shallow parser's output.

**Gate.** `cfg.json` covers the full `std:*` method surface and
round-trips a schema validation. The schema is the contract the Go stage
reads.

## §4. Go analysis: mutation and alias

**Goal.** Produce `facts.json` from `cfg.json` entirely in Go.

**Work.**

- Define the mutation vocabulary (FR1) and the internal-slot backing-
  store list (FR3) as reviewed Go constants.
- Build the call graph from `cfg.json` and compute, as a worklist
  fixpoint, whether each abstract operation mutates its k-th argument,
  seeded by the direct mutators (FR2 transitivity).
- For each builtin method: tag value origins (`this value`/`ToObject(this
  value)` → receiver, formals → params, allocators → fresh), propagate
  through `Let` bindings, and flag the origins reached by a mutating
  operation. Classify receiver mutability and per-parameter mutability.
- Classify return aliasing (FR4) from the return nodes' value origins.
- Apply the soundness bias (FR5): emit `classified: false` for any method
  the analysis cannot resolve, and list every such method.
- Write `facts.json` (FR6).

**Gate.** Spot-check the representative methods from §1: `push`/`fill`
mutate the receiver, `slice`/`map` do not, `Map.set` mutates via
`[[MapData]]` and returns the receiver, every `String.prototype` method
is non-mutating. Unclassified methods are listed.

## §5. Keying and join

**Goal.** Join spec-keyed facts to the converter's class+method
declarations.

**Work.**

- Implement the name normalizer (FR7) handling symbol-keyed methods,
  accessors, and overload sets.
- Wire it into the bootstrap converter (`tools/dts_to_esc/`,
  [../../internal/interop/dts_to_esc.go](../../internal/interop/dts_to_esc.go))
  so a converted method element can look up its fact.
- Report names present on one side only, mirroring the converter's
  unmapped-symbol fail-safe
  ([../../internal/interop/partition.go](../../internal/interop/partition.go)).

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

## §9. Maintenance workflow

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
