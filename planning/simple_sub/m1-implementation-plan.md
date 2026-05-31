# M1 — Implementation plan: package skeleton + `soltype`

Concrete, sequenced plan for landing **M1** of the SimpleSub migration
([01-milestones.md](01-milestones.md#m1--package-skeleton--soltype)). M1 stands
up the new checker's package and its type representation — the structural-core
subset — by **promoting proven spike code** (`internal/simplesub/`,
[#676](https://github.com/escalier-lang/escalier/pull/676)) into a production
package, while cutting the spike's two expedient shortcuts (it borrows
`type_system` for coalescing output and printing).

This document covers scope, the spike→production deltas, the PR breakdown with
sequencing, per-PR acceptance, and risks. It assumes the context in
[00-overview.md](00-overview.md) and [02-design-notes.md](02-design-notes.md).

---

## 1. Scope

### In scope (M1)

Per the milestone, M1 delivers the **structural core**:

1. **New package** `internal/solver/` (sibling to `internal/checker/`; leaf name
   settled, [02-design-notes.md](02-design-notes.md) §"Settled decisions" #1)
   with a `internal/solver/soltype/` subpackage for the type representation.
2. **`soltype` core types** promoted from the spike's `SimpleType`:
   - `TypeVarType` — bound-list inference variable (`id`, `level`,
     `lowerBounds`, `upperBounds`).
   - `PrimitiveType`, `LiteralType`, `FunctionType` (multi-arg), `TupleType`,
     plus `Void`.
3. **The constraint engine** — `constrain(lhs <: rhs)` with the coinductive
   `seen`-cache, the structural cases for the M1 type set, the variable cases
   (bound-append + transitive propagation), and **levels + extrusion**.
4. **Polarity-driven coalescing** — occurrence analysis (`analyze`) feeding
   single-polarity elimination, then coalescing a bound-carrying
   `soltype.Type` into a *coalesced* `soltype.Type`.
5. **A `soltype` printer** — `soltype.Type` → Escalier type-annotation string,
   **its own**, not `type_system.PrintType`.
6. **The `Info` side table** — `map[ast.Node]soltype.Type` + `TypeOf`/`setType`.

### Explicitly out of scope (deferred to later milestones)

| Deferred | Milestone | Why not M1 |
|---|---|---|
| AST-driven inference walk (`infer.go`), parser/resolver bridge | M2 | M1 builds terms by hand, exactly as the spike does. |
| `Scope`/`Binding`/`Namespace` | M2 | No name resolution until the bridge exists. |
| Let-generalization machinery (`instantiate`/`freshenAbove`, `Scheme`s) | M3 | M1's `<T0>` quantifier comes from the *printer* naming surviving bipolar vars, not from generalization. |
| **Co-occurrence variable merging** (`collectCoOcc`/`mergeCoOccurring`/union-find) | M3 | See §3.3 — M1 needs only single-polarity elimination; merging is an M3 simplification. |
| Records, `RefType`/`mut`, **lifetimes** | M4 | First lifetime-carrying types. |
| `exact` flag on formers | M3 (functions) → M6 | Functions get it in M3; M1 stands up the bare `FunctionType`. |
| Classes, unions/intersections, type-level operators | M5/M6/M8 | — |
| `Prov` provenance side table, `Probe` speculation API | M3+/as-needed | M1 has no speculation and no error-message provenance yet. |

### Reversibility

M1 is **purely additive**: it creates new files in a new package and edits
**zero** existing files. The old checker is untouched; there is no flag to flip
yet (that's M7). `go build ./...` and `go test ./...` stay green throughout.

---

## 2. What we promote from the spike (and the two deltas)

The spike already implements every M1 mechanism and is the reference
implementation. Most of M1 is a faithful copy-with-rename. The files that map
directly:

| Spike file | M1 destination | Notes |
|---|---|---|
| `polarity.go` | `solver/polarity.go` | Copy verbatim. |
| `types.go` (M1 subset) | `soltype/type.go` | Keep `Variable`→`TypeVarType`, `Primitive`→`PrimitiveType`, `Literal`→`LiteralType`, `Function`→`FunctionType`, `Tuple`→`TupleType`, `Void`. **Drop** `Record`/`Mut`/`Alias`/`Union`/`Intersection`/`ResidualOp` (M4/M6/M8). Trim `levelOf`/`containsVariable` to the M1 cases. |
| `constrain.go` | `solver/constrain.go` | Keep the prim/literal/function/tuple/variable cases + `extrude`. Drop the union/intersection lattice rules and the record/mut/alias cases (re-added in their milestones). |
| `simplify.go` (`analyze` only) | `solver/simplify.go` | Promote **occurrence analysis only**. Leave co-occurrence merging for M3. |
| `coalesce.go` | `solver/coalesce.go` | **Delta #1 below.** |
| (the printer the spike borrowed) | `soltype/print.go` | **Delta #2 below.** |
| `scheme.go`, `infer.go`, `lifetime.go`, `typeops.go`, `residual.go`, `regularity.go`, `lazy.go` | — | Not M1. |

### Delta #1 — coalescing targets `soltype`, not `type_system`

The spike's `coalesce.go` produces `type_system.Type`
(`type_system.NewUnionType`, `NewFuncType`, …) — an expedient shortcut so spike
output could be string-compared against the old checker's tests. M1 **cuts that
dependency**: coalescing takes a bound-carrying `soltype.Type` and returns a
*coalesced* `soltype.Type` in which

- single-polarity variables are replaced by their bounds (positive ⇒ union of
  lowers, negative ⇒ intersection of uppers; empty ⇒ `never`/`unknown`), and
- bipolar variables survive as a **named type-parameter reference** node.

This requires a small new node in `soltype` to represent a named type-param
reference in coalesced output (the role the spike fills with
`type_system.NewTypeRefType(nil, "T0", nil)`). Proposal: a `soltype.TypeRefType{
Name string }` (also the future home for alias references in M4+). Variable
naming (`T0`, `T1`, …) and quantifier collection move out of `type_system` and
into the coalescer/printer (see Delta #2).

This is the single largest piece of *new* (non-copy) work in M1, and the reason
M1 is not a pure `sed`-rename of the spike.

### Delta #2 — a native `soltype` printer

M1 must ship `soltype/print.go` rendering Escalier type-annotation syntax
directly from `soltype.Type` — the spike never had this (it leaned on
`type_system.PrintType`). The printer:

- renders `PrimitiveType`/`LiteralType`/`FunctionType`/`TupleType`/`TypeRefType`
  in Escalier surface syntax (`number`, `"hello"`, `fn (x: T) -> U`,
  `[number, string]`),
- collects the named type-parameter refs reachable from the top-level type into
  a `<T0, T1, …>` quantifier prefix (the source of the identity's `<T0>`), in
  deterministic order.

Mirror `type_system.PrintType`'s surface syntax so the two checkers' rendered
types are comparable in M7's differential harness, but share **no code** with
it.

---

## 3. Design decisions to settle in M1

### 3.1 Package/file layout

```text
internal/solver/
  soltype/
    type.go        Type iface; TypeVarType, PrimitiveType, LiteralType,
                   FunctionType, TupleType, Void, TypeRefType; levelOf, boundsAt
    print.go       Type -> Escalier annotation string (+ quantifier collection)
  polarity.go      Polarity enum + flip
  context.go       Context: varCounter + freshVar (the engine's mutable state)
  constrain.go     constrain(lhs <: rhs), seen-cache, extrude
  simplify.go      analyze (occurrence analysis; single-polarity only)
  coalesce.go      soltype.Type (with bounds) -> coalesced soltype.Type
  info.go          Info side table (map[ast.Node]soltype.Type)
  doc.go           package doc
```

`Polarity`, the engine, and coalescing live in `solver` (they're algorithm, not
representation); the type nodes and printer live in `soltype`. The printer must
**not** import `solver` (it renders an already-coalesced type) so there's no
import cycle. `solver` imports `soltype` and `internal/ast` (for `Info`'s key
type); neither `ast` nor `type_system` imports `solver`, so there's no cycle and
M1 stays additive.

### 3.2 Naming

Promote the spike's terse names to the design-notes names: `Variable` →
`TypeVarType`, `Primitive` → `PrimitiveType`, etc.
([02-design-notes.md](02-design-notes.md) §"`soltype`"). Keep the spike's
`Inferer`-style mutable state but name it `Context` (the design notes refer to
`solver.Context`). Avoid shadowing Go builtins per CLAUDE.md.

### 3.3 The M1/M3 simplification boundary (decision)

The spike bundles single-polarity elimination **and** co-occurrence variable
merging into one "simplification pass," and the milestones doc nominally lists
both under M3. But coalescing **cannot render at all** without occurrence/
polarity data (it needs to know whether a variable is bipolar to decide keep-vs-
inline). So:

- **M1 promotes `analyze`** (occurrence analysis) — single-polarity elimination
  falls out of it, and it's what makes `id(5)`-shaped terms coalesce to `5`
  rather than `T0 | 5`, and the identity's parameter-only variables coalesce to
  `unknown` rather than a vacuous `<T0>`.
- **M1 uses a degenerate (identity) union-find** — no merging.
- **M3 adds co-occurrence merging** (`collectCoOcc`, `mergeCoOccurring`, the real
  union-find), which is what collapses `InnerCapturesOuterParam` to
  `fn <T0>(y: T0) -> [T0, T0]`. That case is an **M3** accept criterion, not M1.

This keeps M1's accept criterion — the identity rendering `fn <T0>(x: T0) -> T0`
— satisfiable with occurrence analysis alone (the parameter variable is bipolar:
negative in the param, positive in the return, so it survives and is named).

---

## 4. PR breakdown

Five PRs. PR 1 is the foundation; PRs 2→3→4 are a linear chain (engine →
coalesce → print) because each is tested against the previous; PR 5 (`Info`) is
independent of the engine and can land in parallel any time after PR 1. Each PR
leaves `go build ./...` and `go test ./...` green.

```text
PR1 (skeleton + soltype types)
 ├─► PR2 (constrain + extrude) ─► PR3 (analyze + coalesce) ─► PR4 (printer + identity)
 └─► PR5 (Info side table)        [parallel with PR2–PR4]
```

### PR 1 — Package skeleton + `soltype` core types

**Creates:** `solver/polarity.go`, `solver/context.go`, `solver/doc.go`,
`soltype/type.go`.

- `soltype/type.go`: the `Type` interface (`isType()` marker), the M1 type set
  (`TypeVarType`, `PrimitiveType`, `LiteralType`, `FunctionType`, `TupleType`,
  `Void`, and the `TypeRefType` coalesced-output node), `boundsAt`, literal
  equality, and `levelOf` trimmed to M1 cases.
- `solver/polarity.go`: copy of the spike's `Polarity` + `flip`.
- `solver/context.go`: `Context` owning `varCounter`; `freshVar(level)`.
- `solver/doc.go`: package doc describing the production package (adapt the
  spike's `doc.go`, scoped to what's actually present).

**Tests:** `levelOf` over nested function/tuple terms; `freshVar` id/level
sequencing; literal `eq`. Table-driven, `require.*`.

**Accept:** package builds; `go vet ./internal/solver/...` clean.

### PR 2 — `constrain` + extrusion (the engine)

**Creates:** `solver/constrain.go`.

- `constrain(lhs, rhs, seen)` with the coinductive `seen`-cache.
- Structural cases: `PrimitiveType` (name equality), `LiteralType <:
  LiteralType` and `LiteralType <: PrimitiveType` (literal is a subtype of its
  primitive), `FunctionType` (**fewer-params-is-subtype** rule + contravariant
  params / covariant return), `TupleType` (same length, covariant elements),
  `Void`.
- Variable cases: append to `upper`/`lowerBounds` and transitively propagate
  existing bounds; **levels + `extrude`** for cross-level constraints.
- `describe(...)` for error messages.

**Tests** (this is the bulk of the M1 "unit tests for `constrain`" accept
criterion — table-driven, **full** error-message assertions per CLAUDE.md):
- prim `<:` prim success and the exact mismatch error;
- `LiteralType <: PrimitiveType` (e.g. `5 <: number`), and the mismatch error;
- **function variance** incl. fewer-params-is-subtype both directions (arity
  `1 <: 2` ok, `2 <: 1` rejected with the exact message), contravariant param /
  covariant return;
- tuple same-length covariant; length-mismatch error;
- variable binding + transitive propagation (constrain `α <: number` then `5 <:
  α` propagates `5 <: number`);
- extrusion: a higher-level type constrained against a lower-level variable is
  copied down (assert no higher-level var leaks into the lower var's bounds).

### PR 3 — Occurrence analysis + polarity-driven coalescing

**Creates:** `solver/simplify.go` (`analyze` only), `solver/coalesce.go`.

- `analyze(st, pol, occurrences, seen)` — occurrence/polarity analysis over the
  M1 type set (function params flip polarity; return covariant; tuple covariant).
- `coalesce(st, pol)` → **`soltype.Type`** (Delta #1): single-polarity vars
  inlined to their bounds (`combine` building unions/intersections, `never`/
  `unknown` for empties); bipolar vars → `soltype.TypeRefType` named by
  representative id via a degenerate union-find (§3.3). Deduplicate by rendered
  string using PR 4's printer once it lands, or by structural equality in the
  interim.

**Tests:** single-polarity elimination (positive var with lower bound `5` ⇒
`5`; parameter-only negative var ⇒ `unknown`); bipolar var survives as a named
ref; `combine` union/intersection shaping.

### PR 4 — `soltype` printer + the identity end-to-end test

**Creates:** `soltype/print.go`.

- `Print(t soltype.Type) string` rendering Escalier annotation syntax for the M1
  type set; collects named `TypeRefType`s into a `<T0, …>` quantifier prefix in
  deterministic order (Delta #2).
- Mirror `type_system.PrintType`'s surface forms; share no code.

**Tests** (completes the M1 accept criterion):
- **the headline case** — build the identity `FunctionType{[α], α}` by hand,
  coalesce + print ⇒ `fn <T0>(x: T0) -> T0`;
- round-trips for primitives, literals, tuples (`[number, string]`),
  multi-arg functions, and a coalesced union/intersection from PR 3.

> Inline-snapshot option: per CLAUDE.md, the printed-string assertions are exactly
> the case where `snaps.MatchInlineSnapshot` is appropriate; use it for the
> richer shapes and reserve `require.Equal` for the headline identity string.

### PR 5 — `Info` side table *(parallel; depends only on PR 1)*

**Creates:** `solver/info.go`.

- `Info{ types map[ast.Node]soltype.Type }`, `TypeOf(n) soltype.Type`,
  `setType(n, t)`. No probe/cleanup discipline yet (deferred with `Prov`/`Probe`).

**Tests:** construct a couple of real `ast.Node` pointers, assert `setType`/
`TypeOf` round-trip and that an absent node returns the zero value. Confirms the
`solver`→`ast` import direction is acyclic.

> May be folded into PR 1 if reviewers prefer a single foundational PR — it's
> independent of the engine and trivially small. Kept separate here for focus.

---

## 5. Sequencing summary

1. **PR 1** — skeleton + types. Unblocks everything.
2. **PR 2** — `constrain` + `extrude`. (Bulk of the constrain unit-test accept.)
3. **PR 3** — `analyze` + `coalesce` (targets `soltype` — Delta #1). (Coalescing
   accept.)
4. **PR 4** — `soltype` printer (Delta #2) + identity end-to-end. (Identity-
   renders accept.)
5. **PR 5** — `Info`. Parallel with 2–4.

Critical path is 1 → 2 → 3 → 4. PR 5 is off the critical path.

---

## 6. Acceptance criteria (milestone → PR mapping)

The milestone's accept clause is: *"unit tests for `constrain` (prim/function
variance incl. Escalier's fewer-params-is-subtype rule) and coalescing; an
identity term renders `fn <T0>(x: T0) -> T0`."*

| Accept clause | Delivered by |
|---|---|
| `constrain` prim variance | PR 2 |
| `constrain` function variance + fewer-params-is-subtype | PR 2 |
| coalescing unit tests | PR 3 |
| identity renders `fn <T0>(x: T0) -> T0` | PR 4 (coalesce from PR 3 + printer) |

**Definition of done for M1:** all five PRs merged; `go test ./internal/solver/...`
green; zero edits to existing packages; the old checker, `go build ./...`, and
the full `go test ./...` suite unaffected. (There are **no fixture tests** in M1
— `cmd/...` is untouched until the M2 parser bridge — so M1 is validated purely
by `internal/solver/...` unit tests.)

---

## 7. Risks & gates

- **M1 has no go/no-go gate of its own.** The plan's gates are at M2 (the
  parser-bridge boundary check) and M4 (the `mut`-invariance gate). M1 is the
  lowest-risk milestone: it promotes code already proven by the spike's
  differential harness (10 match / 2 benign / 0 regression).
- **Only real risk: the two deltas.** Cutting the `type_system` dependency
  (Delta #1) and writing a native printer (Delta #2) are the sole pieces of
  genuinely new code. Mitigation: keep the coalescer's *logic* a line-for-line
  port of the spike's `coalesce.go`, changing only the constructor calls
  (`type_system.New… → soltype.…`) and moving variable-naming into the
  coalescer/printer; diff against the spike during review to confirm the
  algorithm is unchanged.
- **Scope creep from the spike.** The spike file set is tempting to copy whol
 wholesale. Resist: M1 is the structural-core subset only. Records, `mut`,
  lifetimes, unions, and type operators each land in their own milestone with
  their own tests and (for `mut`) their own gate. Copying them early lands
  untested-against-real-source code ahead of the bridge.
- **The M1/M3 simplification line** (§3.3) is a judgement call recorded here so a
  reviewer doesn't flag "where's co-occurrence merging?" — it's deliberately
  M3, and M1's accept is met without it.

---

## 8. Conventions

Follow [CLAUDE.md](../../CLAUDE.md): table-driven tests; `require.*` over
`assert.*`; assert **full** error messages, not substrings; use `internal/set`'s
`Set` ADT rather than `map[T]struct{}`; use the inline-snapshot pattern
(`snaps.MatchInlineSnapshot`) for the printer's richer rendered-type assertions.
Don't shadow Go builtins. There are no snapshot/fixture env-var flows in M1
(no `cmd/...` involvement); tests run with plain `go test ./internal/solver/...`.
