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

## 2. Differences from the spike

The spike (`internal/simplesub/`) already implements every M1 mechanism and is
the reference implementation, so most of M1 is a faithful copy-with-rename. But
the spike was a *throwaway proof-of-concept* (its own `doc.go` says so), and it
took several shortcuts and skipped several project conventions that the
production package must not. This section enumerates **every** intended
difference, grouped as:

- **§2.1** file→file mapping (what moves where),
- **§2.2** the two output deltas (the only genuinely *new* code),
- **§2.3** representation & API differences,
- **§2.4** convention cleanups the spike skipped,
- **§2.5** what is deliberately *not* carried over.

A reviewer comparing an M1 PR against the spike should be able to account for
any diff by one of the entries below; anything else is unintended drift.

### 2.1 File→file mapping

Most of M1 is a faithful copy-with-rename. The files that map directly:

| Spike file | M1 destination | Notes |
|---|---|---|
| `polarity.go` | `solver/polarity.go` | Copy verbatim. |
| `types.go` (M1 subset) | `soltype/type.go` | Keep `Variable`→`TypeVarType`, `Primitive`→`PrimitiveType`, `Literal`→`LiteralType`, `Function`→`FunctionType`, `Tuple`→`TupleType`, `Void`. **Drop** `Record`/`Mut`/`Alias`/`Union`/`Intersection`/`ResidualOp` (M4/M6/M8). Trim `levelOf`/`containsVariable` to the M1 cases. |
| `constrain.go` | `solver/constrain.go` | Keep the prim/literal/function/tuple/variable cases + `extrude`. Drop the union/intersection lattice rules and the record/mut/alias cases (re-added in their milestones). |
| `simplify.go` (`analyze` only) | `solver/simplify.go` | Promote **occurrence analysis only**. Leave co-occurrence merging for M3. |
| `coalesce.go` | `solver/coalesce.go` | **Delta #1 below.** |
| (the printer the spike borrowed) | `soltype/print.go` | **Delta #2 below.** |
| `scheme.go`, `infer.go`, `lifetime.go`, `typeops.go`, `residual.go`, `regularity.go`, `lazy.go` | — | Not M1. |

### 2.2 The two output deltas (the only new code)

These two items are the reason M1 is *not* a pure `sed`-rename: the spike leaned
on `type_system` for both its coalescing output and its printing, and M1 must
sever both.

#### Delta #1 — coalescing targets `soltype`, not `type_system`

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

#### Delta #2 — a native `soltype` printer

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

> **Two renderers, distinct jobs.** Keep this printer separate from the spike's
> `describe()` (§2.3). `describe` renders a *raw, uncoalesced* `soltype.Type`
> mid-`constrain` for error messages (`t0`, `function`, `number`); `Print`
> renders a *coalesced* type as user-facing Escalier syntax. They look similar
> but operate at different stages and must not be merged in M1.

### 2.3 Representation & API differences

| # | Spike | M1 production | Rationale |
|---|---|---|---|
| 1 | One flat `package simplesub` | Two packages: `solver` (engine) + `soltype` (representation + printer) | Matches [02-design-notes.md](02-design-notes.md) layout; lets the printer live with the types without importing the engine. |
| 2 | Terse names: `Variable`, `Primitive`, `Literal`, `Function`, `Tuple` | `TypeVarType`, `PrimitiveType`, `LiteralType`, `FunctionType`, `TupleType` | The design-notes names; consistent `…Type` suffix. |
| 3 | `SimpleType` interface (`isSimpleType()`) | `soltype.Type` interface (`isType()`) | One representation in production — no separate "SimpleType vs output type" split, since coalescing now stays within `soltype` (Delta #1). |
| 4 | `Inferer` struct carrying `varCounter`, `lifetimeCounter`, `paramLifetimes`, `written` | `solver.Context` carrying **only** `varCounter` (+ `freshVar`) | `lifetimeCounter`/`paramLifetimes` are M4 (lifetimes); `written` (field-write tracking) is M4 (records/`mut`). M1's `Context` is correspondingly lean. |
| 5 | Named-type-param refs in output are `type_system.NewTypeRefType(nil, "T0", nil)` | A native `soltype.TypeRefType{Name string}` node | Delta #1 needs an in-`soltype` node for coalesced bipolar vars; also the future home for alias refs (M4+). |
| 6 | Variable naming (`T0…`) + `<…>` quantifier collection done inside the coalescer (`nameForRep`, `order`) | Naming assigned during coalesce; **quantifier collection done in the printer** | Keeps `Print` self-contained (it walks the coalesced tree and gathers the refs it sees) and the coalescer free of presentation concerns. |
| 7 | Public entry points `Infer(term)` / `Render(...)` over a hand-built `Term` IR | **No public inference entry point in M1.** Tests drive `constrain` / `coalesce` / `Print` directly | The `Term` IR and `typeTerm` walk are the spike's stand-in for the parser bridge — replaced wholesale by the real AST walk in **M2**, not promoted. |

### 2.4 Convention cleanups the spike skipped

The spike is a throwaway, so it freely uses raw maps and `fmt.Errorf`. Per
[CLAUDE.md](../../CLAUDE.md), the production package must not:

- **`set.Set` instead of raw `map[T]bool`.** The spike threads the `constrain`
  seen-cache as `map[constraintKey]bool` and uses `map[int]bool` / `map[polKey]bool`
  throughout `analyze`/coalescing. M1 uses `set.Set[T]`
  (`set.NewSet`, `set.FromSlice`) per the repo convention. (The spike is already
  inconsistent here — it uses `set.Set[int]` for `paramLifetimes` but raw maps
  for the seen-cache; M1 makes it uniform.) The `seen` cache becomes
  `set.Set[constraintKey]`.
- **Error representation.** The spike returns `[]error` built from bare
  `fmt.Errorf("cannot constrain %s <: %s", …)`. M1 keeps the **same full message
  strings** (so the unit-test assertions read naturally and match the spike's
  proven wording) but routes them through the package's error type rather than
  ad-hoc `fmt.Errorf`. Per [02-design-notes.md](02-design-notes.md) Settled
  Decision #4, production reuses the old checker's diagnostic types where they
  apply and adds new kinds as needed — but **M1 has no source spans yet** (no
  parser bridge until M2), so M1's errors stay span-free value types; wiring
  provenance/locations is deferred. The discipline to settle in M1 is just "one
  error constructor, not scattered `fmt.Errorf`," so M2 has a single place to add
  spans.
- **Testing conventions.** The spike already uses `testify/require` (good) and
  small constructor helpers (`num()`, `str()`, `fn1`, `fn2`) — carry those over.
  But for the richer rendered-type assertions, adopt `snaps.MatchInlineSnapshot`
  per CLAUDE.md (reserve `require.Equal` for the single headline identity
  string). Assert **full** error messages, never substrings.

### 2.5 What is deliberately *not* carried over

- **The `Term` IR + `typeTerm` walk** (`infer.go`) — the spike's hand-built
  expression IR is its stand-in for the parser; M2 replaces it with a real
  `*ast.Module` walk. M1 builds `soltype` terms directly in tests.
- **Everything past the structural core** — `Record`, `Mut`, `Alias`, `Union`,
  `Intersection`, `ResidualOp`, lifetimes (`lifetime.go`), type operators
  (`typeops.go`), residuals (`residual.go`), regularity (`regularity.go`), and
  the lazy/coinductive variant (`lazy.go`). Each lands in its own milestone with
  its own tests (and, for `mut`, its own gate).
- **The spike's documented `freshenAbove` lifetime-generalization limitation.**
  It's in `scheme.go`, which is M3 (let-polymorphism) work; the fix it calls for
  (lifetime levels) is M4. Neither `scheme.go` nor that limitation enters M1.
- **The `type_system` import.** After M1, the new package must have **zero**
  `type_system` references (the spike has 4 in non-test files, all in
  `coalesce.go`). This is the concrete, greppable success signal for Delta #1.

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
  errors.go        typeError value type + cannotConstrain* constructors, describe
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
leaves `go build ./...` and `go test ./...` green. **§9 sketches the concrete
types and functions each PR introduces** — the PR descriptions below reference
those sketches by name.

```text
PR1 (skeleton + soltype types)
 ├─► PR2 (constrain + extrude) ─► PR3 (analyze + coalesce) ─► PR4 (printer + identity)
 └─► PR5 (Info side table)        [parallel with PR2–PR4]
```

### PR 1 — Package skeleton + `soltype` core types

**Creates:** `solver/polarity.go`, `solver/context.go`, `solver/doc.go`,
`soltype/type.go`. *(sketches: §9.1, §9.2)*

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

**Creates:** `solver/constrain.go`, `solver/errors.go`. *(sketches: §9.3)*

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
*(sketches: §9.4, §9.5)*

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

**Creates:** `soltype/print.go`. *(sketches: §9.6)*

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

**Creates:** `solver/info.go`. *(sketches: §9.7)*

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
by `internal/solver/...` unit tests.) The greppable signal for Delta #1:
`grep -rn "type_system" internal/solver/ | grep -v _test` returns **nothing** —
the new package has fully severed the spike's `type_system` coupling.

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

---

## 9. Type & function sketches

Illustrative shapes for the M1 surface, derived from the spike with the §2
deltas applied. **Names and signatures are provisional** — the point is to pin
down the data model and the boundaries between functions, not to prescribe final
code. Repetitive switch arms are elided with `// …`. Sketches are grouped by the
file they land in (§3.1 layout) and tagged with the PR that introduces them.

### 9.1 `soltype/type.go` — the type representation *(PR 1)*

```go
package soltype

// Type is the sealed interface for all soltype nodes. (Production name for the
// spike's SimpleType; marker renamed isSimpleType -> isType.)
type Type interface{ isType() }

// TypeVarType is an inference variable carrying Simple-sub lower/upper bound
// lists plus the level at which it was created (for let-generalization in M3).
type TypeVarType struct {
	ID          int
	Level       int
	LowerBounds []Type
	UpperBounds []Type
}

// BoundsAt returns the bounds relevant to a polarity: lowers in Positive
// position (the var becomes their union), uppers in Negative (their meet).
// Polarity lives in package solver, so this takes the raw direction as a bool
// to avoid a soltype->solver import. (Alternatively, move Polarity into soltype;
// decide in PR 1.)
func (v *TypeVarType) BoundsAt(positive bool) []Type {
	if positive {
		return v.LowerBounds
	}
	return v.UpperBounds
}

type PrimitiveType struct{ Name string } // "number" | "string" | "boolean"

type LiteralType struct {
	Kind string // "str" | "num" | "bool"
	Str  string
	Num  float64
	Bool bool
}

func (l *LiteralType) Eq(o *LiteralType) bool { /* same as spike Literal.eq */ }

type FunctionType struct {
	Params     []Type
	ParamNames []string // for rendering param names; "" => synthesized x0, x1, …
	Ret        Type
}

type TupleType struct{ Elems []Type }

// Void is the result type of a statement block with no value.
type Void struct{}

// TypeRefType is the coalesced-output node for a named type parameter (the role
// the spike fills with type_system.NewTypeRefType). Also the future home for
// alias references (M4+). NEW in M1 — see §2.2 Delta #1.
type TypeRefType struct{ Name string }

func (*TypeVarType) isType()   {}
func (*PrimitiveType) isType() {}
func (*LiteralType) isType()   {}
func (*FunctionType) isType()  {}
func (*TupleType) isType()     {}
func (*Void) isType()          {}
func (*TypeRefType) isType()   {}

// LevelOf is the max level of any TypeVarType inside t; concrete leaves are 0.
// Trimmed to the M1 type set (grows back as later milestones add formers).
func LevelOf(t Type) int {
	switch t := t.(type) {
	case *TypeVarType:
		return t.Level
	case *FunctionType:
		m := 0
		for _, p := range t.Params {
			m = max(m, LevelOf(p))
		}
		return max(m, LevelOf(t.Ret))
	case *TupleType:
		m := 0
		for _, e := range t.Elems {
			m = max(m, LevelOf(e))
		}
		return m
	default: // PrimitiveType, LiteralType, Void, TypeRefType
		return 0
	}
}
```

### 9.2 `solver/polarity.go` and `solver/context.go` *(PR 1)*

```go
package solver

type Polarity int

const (
	Positive Polarity = iota
	Negative
)

func (p Polarity) Flip() Polarity { /* Positive<->Negative */ }

// Context owns the engine's mutable counters. M1 carries ONLY varCounter; the
// spike's lifetimeCounter / paramLifetimes / written fields are M4 (§2.3 row 4).
type Context struct {
	varCounter int
}

func (c *Context) freshVar(level int) *soltype.TypeVarType {
	v := &soltype.TypeVarType{ID: c.varCounter, Level: level}
	c.varCounter++
	return v
}
```

### 9.3 `solver/constrain.go` — the engine *(PR 2)*

```go
package solver

// constraintKey keys the coinductive seen-set.
type constraintKey struct{ lhs, rhs soltype.Type }

// Constrain asserts lhs <: rhs, mutating bound lists. Empty result == success.
func (c *Context) Constrain(lhs, rhs soltype.Type) []error {
	return c.constrain(lhs, rhs, set.NewSet[constraintKey]()) // set.Set, not map (§2.4)
}

func (c *Context) constrain(lhs, rhs soltype.Type, seen set.Set[constraintKey]) []error {
	key := constraintKey{lhs, rhs}
	if seen.Contains(key) {
		return nil
	}
	seen.Add(key)

	switch l := lhs.(type) {
	case *soltype.PrimitiveType:
		if r, ok := rhs.(*soltype.PrimitiveType); ok {
			if r.Name == l.Name {
				return nil
			}
			return []error{cannotConstrain(l, r)}
		}
	case *soltype.LiteralType:
		// LiteralType <: LiteralType (Eq) and LiteralType <: PrimitiveType
		// (a literal is a subtype of its primitive). // …
	case *soltype.FunctionType:
		if r, ok := rhs.(*soltype.FunctionType); ok {
			// Fewer-params-is-subtype: l <: r requires len(l.Params) <= len(r.Params).
			if len(l.Params) > len(r.Params) {
				return []error{cannotConstrainArity(len(l.Params), len(r.Params))}
			}
			var errs []error
			for i := range l.Params {
				errs = append(errs, c.constrain(r.Params[i], l.Params[i], seen)...) // contravariant
			}
			return append(errs, c.constrain(l.Ret, r.Ret, seen)...) // covariant
		}
	case *soltype.TupleType:
		// same length, element-wise covariant // …
	case *soltype.Void:
		if _, ok := rhs.(*soltype.Void); ok {
			return nil
		}
	}

	// lhs is a variable: record rhs as an upper bound, propagate existing lowers.
	if lv, ok := lhs.(*soltype.TypeVarType); ok {
		if soltype.LevelOf(rhs) <= lv.Level {
			lv.UpperBounds = append(lv.UpperBounds, rhs)
			var errs []error
			for _, lb := range lv.LowerBounds {
				errs = append(errs, c.constrain(lb, rhs, seen)...)
			}
			return errs
		}
		return c.constrain(lhs, c.extrude(rhs, Negative, lv.Level, map[int]*soltype.TypeVarType{}), seen)
	}
	// rhs is a variable: symmetric (record lower bound, propagate uppers). // …

	return []error{cannotConstrain(lhs, rhs)}
}

// extrude copies t so variables above lvl become fresh vars at lvl, wired to the
// originals through the polarity-appropriate bound. (Same algorithm as the
// spike; cache is keyed by var ID.)
func (c *Context) extrude(t soltype.Type, pol Polarity, lvl int, cache map[int]*soltype.TypeVarType) soltype.Type {
	// … TypeVarType / FunctionType (params flip) / TupleType cases …
}
```

The error helpers are the §2.4 "one constructor, not scattered `fmt.Errorf`"
discipline — same message strings as the spike, span-free until M2:

```go
// errors.go (PR 2). A lightweight value type now; gains spans/diagnostic-kind in M2+.
type typeError struct{ msg string }

func (e *typeError) Error() string { return e.msg }

func cannotConstrain(lhs, rhs soltype.Type) error {
	return &typeError{fmt.Sprintf("cannot constrain %s <: %s", describe(lhs), describe(rhs))}
}
func cannotConstrainArity(a, b int) error { /* "cannot constrain function of arity %d <: …" */ }

// describe renders a RAW, uncoalesced type for in-flight error messages
// (t0, function, number). Distinct from soltype.Print, which renders coalesced
// output (§2.2). Lives in solver because it walks bound-carrying vars.
func describe(t soltype.Type) string { /* … */ }
```

### 9.4 `solver/simplify.go` — occurrence analysis *(PR 3)*

Single-polarity elimination only; co-occurrence merging is M3 (§3.3).

```go
package solver

type polKey struct {
	id  int
	pol Polarity
}

// analyze records, per variable, the polarities it occurs in (following bounds
// in the relevant direction). Drives single-polarity elimination in coalescing.
func analyze(t soltype.Type, pol Polarity, occ map[int]set.Set[Polarity], seen set.Set[polKey]) {
	switch t := t.(type) {
	case *soltype.TypeVarType:
		if occ[t.ID] == nil {
			occ[t.ID] = set.NewSet[Polarity]()
		}
		occ[t.ID].Add(pol)
		k := polKey{t.ID, pol}
		if seen.Contains(k) {
			return
		}
		seen.Add(k)
		for _, b := range t.BoundsAt(pol == Positive) {
			analyze(b, pol, occ, seen)
		}
	case *soltype.FunctionType:
		for _, p := range t.Params {
			analyze(p, pol.Flip(), occ, seen) // contravariant
		}
		analyze(t.Ret, pol, occ, seen)
	case *soltype.TupleType:
		for _, e := range t.Elems {
			analyze(e, pol, occ, seen)
		}
	}
}
```

### 9.5 `solver/coalesce.go` — `soltype.Type` → coalesced `soltype.Type` *(PR 3)*

Delta #1: returns `soltype.Type`, not `type_system.Type`. Lean coalescer — no
union-find (degenerate/identity in M1), no lifetime fields (M4).

```go
package solver

type coalescer struct {
	occ     map[int]set.Set[Polarity] // from analyze
	names   map[int]string            // var ID -> "T0" (assigned on first sight)
	counter int
	inProc  set.Set[polKey] // recursion guard
}

func (co *coalescer) nameFor(id int) string {
	if n, ok := co.names[id]; ok {
		return n
	}
	n := "T" + strconv.Itoa(co.counter)
	co.counter++
	co.names[id] = n
	return n
}

func (co *coalescer) bipolar(id int) bool {
	return co.occ[id].Contains(Positive) && co.occ[id].Contains(Negative)
}

func (co *coalescer) coalesce(t soltype.Type, pol Polarity) soltype.Type {
	switch t := t.(type) {
	case *soltype.PrimitiveType, *soltype.LiteralType, *soltype.Void:
		return t // atoms pass through
	case *soltype.FunctionType:
		params := make([]soltype.Type, len(t.Params))
		for i, p := range t.Params {
			params[i] = co.coalesce(p, pol.Flip()) // contravariant
		}
		return &soltype.FunctionType{Params: params, ParamNames: t.ParamNames, Ret: co.coalesce(t.Ret, pol)}
	case *soltype.TupleType:
		// element-wise coalesce in pol // …
	case *soltype.TypeVarType:
		pk := polKey{t.ID, pol}
		if co.inProc.Contains(pk) {
			return &soltype.TypeRefType{Name: co.nameFor(t.ID)}
		}
		co.inProc.Add(pk)
		defer co.inProc.Remove(pk)

		bounds := make([]soltype.Type, 0, len(t.BoundsAt(pol == Positive)))
		for _, b := range t.BoundsAt(pol == Positive) {
			bounds = append(bounds, co.coalesce(b, pol))
		}
		if !co.bipolar(t.ID) {
			// Single-polarity: drop the var, keep only its bounds.
			if len(bounds) == 0 {
				if pol == Positive {
					return &soltype.NeverType{} // or the chosen bottom repr
				}
				return &soltype.UnknownType{} // top
			}
			return combine(pol, dedup(bounds))
		}
		self := &soltype.TypeRefType{Name: co.nameFor(t.ID)}
		return combine(pol, dedup(append([]soltype.Type{self}, bounds...)))
	}
	panic("coalesce: unhandled type")
}

// combine builds a union (Positive) or intersection (Negative) of parts,
// returning the sole element directly when only one remains. Union/Intersection
// nodes themselves arrive in M6 — in M1, len(parts) is always 1, so combine just
// returns parts[0]. (Stub the multi-part branch with a TODO(M6).)
func combine(pol Polarity, parts []soltype.Type) soltype.Type { /* … */ }
```

> M1 note: `NeverType`/`UnknownType`/`UnionType`/`IntersectionType` are not in
> the M1 type set (§1). For M1, the only reachable coalesce outputs are atoms,
> functions, tuples, and `TypeRefType`; the empty-bounds and multi-part branches
> are written against placeholders and exercised for real in M6. Decide in PR 3
> whether to introduce bare `NeverType`/`UnknownType` now (cheap) or stub them.

### 9.6 `soltype/print.go` — the native printer *(PR 4)*

Delta #2. Renders a *coalesced* type; collects the `TypeRefType` names it sees
into the `<…>` quantifier prefix (quantifier collection moved out of the
coalescer, §2.3 row 6).

```go
package soltype

// Print renders a coalesced Type as an Escalier type-annotation string,
// prefixing a function's type-parameter quantifier when it has free TypeRefs.
func Print(t Type) string {
	params := collectTypeParams(t) // distinct TypeRefType names, first-seen order
	body := printType(t)
	if fn, ok := t.(*FunctionType); ok && len(params) > 0 {
		_ = fn
		return "fn <" + strings.Join(params, ", ") + ">" + body[len("fn "):]
	}
	return body
}

func printType(t Type) string {
	switch t := t.(type) {
	case *PrimitiveType:
		return t.Name
	case *LiteralType:
		// "hello" | 5 | true (Escalier literal syntax) // …
	case *TypeRefType:
		return t.Name
	case *FunctionType:
		ps := make([]string, len(t.Params))
		for i, p := range t.Params {
			ps[i] = paramName(t.ParamNames, i) + ": " + printType(p)
		}
		return "fn (" + strings.Join(ps, ", ") + ") -> " + printType(t.Ret)
	case *TupleType:
		// "[" + comma-join(printType(e)) + "]" // …
	case *Void:
		return "void"
	}
	panic("Print: unhandled type")
}

// collectTypeParams walks t and returns distinct TypeRefType names in first-seen
// order — the order the <…> quantifier is emitted in. Deterministic because the
// walk is a fixed left-to-right traversal.
func collectTypeParams(t Type) []string { /* … */ }

func paramName(names []string, i int) string { /* names[i] or "x"+i, per spike */ }
```

For the identity term, a test builds the type by hand and asserts the string:

```go
func TestIdentityRenders(t *testing.T) {
	ctx := &Context{}
	a := ctx.freshVar(1)
	fn := &soltype.FunctionType{Params: []soltype.Type{a}, ParamNames: []string{"x"}, Ret: a}

	occ := map[int]set.Set[Polarity]{}
	analyze(fn, Positive, occ, set.NewSet[polKey]())
	co := &coalescer{occ: occ, names: map[int]string{}, inProc: set.NewSet[polKey]()}

	got := soltype.Print(co.coalesce(fn, Positive))
	require.Equal(t, "fn <T0>(x: T0) -> T0", got)
}
```

### 9.7 `solver/info.go` — the `Info` side table *(PR 5)*

```go
package solver

// Info is the AST->type side table (à la go/types.Info). The new checker never
// touches ast node InferredType()/SetInferredType(). No probe/cleanup discipline
// in M1 (that arrives with Prov/Probe later).
type Info struct {
	types map[ast.Node]soltype.Type
}

func NewInfo() *Info { return &Info{types: map[ast.Node]soltype.Type{}} }

func (i *Info) TypeOf(n ast.Node) soltype.Type { return i.types[n] }
func (i *Info) setType(n ast.Node, t soltype.Type) { i.types[n] = t }
```
