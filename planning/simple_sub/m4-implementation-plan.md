# M4 implementation plan — Core value types: records + usage-based inference + `mut` + lifetimes

This is the implementation plan for **M4** as defined in
[01-milestones.md](01-milestones.md) §"M4 — Core value types". It assumes M1
(package skeleton + `soltype`), M2 (parser/resolver bridge), and M3 (functions,
application, let-polymorphism, function exactness) have landed. It is grounded in
[02-design-notes.md](02-design-notes.md) (the `RefType` wrapper, the exactness
flag, the `Probe` API, the `Info`/`Prov` side tables) and the lifetime lattice in
[03-references.md](03-references.md).

The type/function sketches below are **adapted from the proven spike**
(`internal/simplesub/` — `types.go`, `constrain.go`, `lifetime.go`,
`coalesce.go`) into production `soltype` shapes. The spike's separate `Mut`
wrapper + `lt`-on-`Record` is **replaced** by the unified `RefType{mut, lt,
inner}` per the settled design; names are provisional.

## Why M4 is "the big one"

M4 promotes three spike milestones at once and they are **inseparable**:
records/objects (spike M2 — the first borrowable value), `mut` (spike M3 — the
highest-risk gate), and lifetimes (spike M4 — a second sort solved by the same
`constrain`). Lifetimes ride on borrows, records are the first thing that can be
borrowed, `mut` borrows are what first populate a lifetime. Exactness (the
`exact` flag on `RecordType`/`TupleType`) also lands here, because the settled
decision is to introduce the flag *with* each former, not retrofit it.

## Scope

In: owned `RecordType`/`TupleType` with the `exact` flag; the unified `RefType`
borrow wrapper; usage-based inference (member reads → constraints); the single
`RefType` constrain rule (mut invariance via read/write decomposition); field-write
inference + read-after-write; lifetimes as a second sort; ported
mutability-transition checking.

Out (later milestones): nominal classes (M5); union/intersection *annotations*
(M6 — M2/M4 already produce them as coalescing *output*); type-level operators
(M8); codegen and value-level `exact<T>(v)` (M9).

---

## Key types and function sketches

### Owned carriers + exactness (`soltype/type.go`)

```go
// RecordType is an owned structural object type. exact closes the type (no extra
// members, no width subtyping); inexact (written `{... ...}`) permits width
// subtyping. Owned — carries NO lifetime; a lifetime is a property of a borrow
// and lives on the RefType wrapper.
type RecordType struct {
    fields map[string]Type
    exact  bool
}

// TupleType gains the same flag (M1 left it as a stub). exact ⇒ fixed length;
// inexact ⇒ length-flexible tail.
type TupleType struct {
    elems []Type
    exact bool
}
```

### The borrow wrapper (`soltype/type.go`)

```go
// RefType is the single wrapper for borrows and mutability (replaces the spike's
// Mut + lt-on-carrier). See 02-design-notes.md §"soltype".
//
//   mut=false lt=nil  -> forbidden degenerate cell (smart ctor returns bare inner)
//   mut=false lt='a   -> immutable borrow with lifetime 'a
//   mut=true  lt=nil  -> owned mutable value
//   mut=true  lt='a   -> mutable borrow with lifetime 'a
type RefType struct {
    mut   bool
    lt    Lifetime  // nilable; see table above
    inner RefInner  // narrower than Type — see marker below
}

// RefInner is the sealed set of types that may sit inside a RefType. Primitives,
// literals, functions, and nested RefTypes are deliberately excluded.
type RefInner interface {
    Type
    isRefInner()
}

func (*RecordType)  isRefInner() {}
func (*TupleType)   isRefInner() {}
func (*AliasType)   isRefInner() {}
func (*TypeVarType) isRefInner() {} // mid-inference; checked at constrain time
// + UnionType / IntersectionType once M6 lands; ClassType in M5.

// NewRef enforces the wrapper invariant: the degenerate (immutable, no-lifetime)
// cell collapses to the bare inner so no downstream *RefType type-switch ever
// sees a borrow-shaped value that isn't a borrow.
func NewRef(mut bool, lt Lifetime, inner RefInner) Type {
    if !mut && lt == nil {
        return inner
    }
    return &RefType{mut: mut, lt: lt, inner: inner}
}

// Peel helpers used everywhere a value is destructured (field access, etc.).
func unwrapRef(t Type) (inner Type, mut bool, lt Lifetime)
func carrierOf(t Type) Type // peel any RefType, return the inner carrier

// borrowableType is the content invariant the RefInner marker can't express
// (e.g. it rejects Union{RecordType, PrimitiveType}); descends collections.
func borrowableType(t Type) bool
```

### The lifetime sort (`soltype/lifetime.go`, faithful to spike)

```go
type Lifetime interface{ isLifetime() }

type LifetimeVar struct {
    id          int
    lowerBounds []Lifetime
    upperBounds []Lifetime
}
type StaticLifetime struct{} // top of the outlives lattice ('static)

func (c *checker) freshLifetime() *LifetimeVar

// constrainLt mirrors constrain over the outlives lattice: a var on the left
// gains an upper bound, on the right a lower bound; var-to-var records both
// directions. 'static is top, so X <: 'static always holds. Uses a seen-set
// keyed on (lhs, rhs) for cycle termination.
func (c *checker) constrainLt(lhs, rhs Lifetime)
```

### `constrain`: record + exactness (`constrain.go`)

```go
func (c *checker) constrainRecords(l, r *RecordType, seen seenSet) []error {
    var errs []error
    // every field r requires must exist in l, covariantly (depth subtyping)
    for name, rt := range r.fields {
        lt, ok := l.fields[name]
        if !ok {
            errs = append(errs, missingFieldError(name)) // full message asserted in tests
            continue
        }
        errs = append(errs, c.constrain(lt, rt, seen)...)
    }
    // one-way exactness rule (02-design-notes §"Exactness"):
    //   exact <: inexact            ok
    //   exact <: exact              same member set (no extra in l)
    //   inexact <: exact            rejected
    //   inexact <: inexact          width subtyping (the loop above already allows extra)
    if r.exact {
        if !l.exact {
            errs = append(errs, inexactIntoExactError(l, r))
        }
        for name := range l.fields {
            if _, ok := r.fields[name]; !ok {
                errs = append(errs, extraMemberError(name, r))
            }
        }
    }
    return errs
}
```

### `constrain`: the single `RefType` rule — **THE GATE** (`constrain.go`)

```go
// Adapted from the spike's *Mut case (constrain.go:182-198), generalized to the
// unified wrapper. The mut-driven bidirectional inner sweep is the read/write
// decomposition that encodes invariance — the highest-risk gate.
case *RefType: // l := lhs.(*RefType)
    if r, ok := rhs.(*RefType); ok {
        // 1. mutability compatibility — can't widen immutable to mutable.
        if !l.mut && r.mut {
            return []error{mutabilityError(l, r)}
        }
        // 2. inner variance — bidirectional iff the TARGET is mutable.
        errs := c.constrain(l.inner, r.inner, seen) // read view (covariant)
        if r.mut {
            errs = append(errs, c.constrain(r.inner, l.inner, seen)...) // write view (contra)
        }
        // 3. lifetime — covariant when both present (inert until Phase D).
        switch {
        case l.lt != nil && r.lt != nil:
            c.constrainLt(r.lt, l.lt)
        case l.lt == nil && r.lt != nil: // owned source into borrow slot: ok
        case l.lt != nil && r.lt == nil: // borrow into owned slot: escape
            errs = append(errs, escapeError(l, r))
        }
        return errs
    }
    // RefType <: bare: only valid when l is an owned value (no lifetime).
    if l.lt != nil {
        return []error{escapeError(l, rhs)}
    }
    return c.constrain(l.inner, rhs, seen)
// (bare <: RefType handled symmetrically: wrap source as NewRef(false,nil,_)
//  and re-dispatch into the RefType<:RefType branch.)
```

### Usage-based inference + field write (`infer.go`)

```go
// MemberExpr read on a value-typed receiver: obj.field
// The synthesized requirement is INEXACT ({field: β, ...}) — "recv must have AT
// LEAST this field"; Policy-A coalescing closes the param to exact afterward.
func (c *checker) inferMemberRead(recv Type, field string, n ast.Node) Type {
    beta := c.freshVarAt(n, FieldAccess) // also writes Prov
    req := &RecordType{fields: map[string]Type{field: beta}, exact: false}
    c.constrain(recv, req, c.newSeen())
    return beta
}

// Member write: obj.field = v  (constrain.go widen() reused)
func (c *checker) inferFieldWrite(recv Type, field string, v Type, n ast.Node) {
    lt := c.freshLifetime() // fresh: recv may be owned-mutable OR a mut-borrow of any lifetime
    req := NewRef(true, lt, &RecordType{
        fields: map[string]Type{field: widen(v)}, exact: false,
    })
    c.constrain(recv, req, c.newSeen())
    c.recordWritten(recv, field, widen(v)) // read-after-write (spike's `written` map)
}
```

### Borrow origination + lifetime coalescing/elision (`infer.go` / `coalesce.go`)

```go
// A RefType-typed parameter is a borrow without a lifetime yet: give it a fresh
// one and record it as a "param lifetime" (only these are named in output).
// Adapted from spike attachParamLifetimes (lifetime.go:260).
func (c *checker) attachParamLifetimes(t Type) Type {
    r, ok := t.(*RefType)
    if !ok || r.lt != nil {
        return t
    }
    lt := c.freshLifetime()
    c.paramLifetimes.Add(lt.id)
    return &RefType{mut: r.mut, lt: lt, inner: r.inner}
}

// Coalescing a RefType, with the mut-vs-immutable elision branch (the subtle one).
func (c *checker) coalesceRef(t *RefType, pol Polarity, st *coalesceState) Type {
    inner := c.coalesceInner(t.inner, pol, st) // inner invariance handled by constrain, not here
    name := c.lifetimeName(t.lt, pol)          // "" when the lifetime is elidable
    if name == "" {
        if t.mut {
            return NewRef(true, nil, inner.(RefInner)) // owned-mutable, well-formed
        }
        return inner // immutable + elided ⇒ MUST drop the wrapper (else degenerate cell)
    }
    return type_system.NewBorrowType(nil, inner, name, t.mut) // `mut 'a T` / `'a T`
}
```

### Transition checking (`transitions.go` / `context.go`)

```go
// Two narrow predicates reimplemented over soltype (the rest of
// internal/liveness ports verbatim).
func isMutableType(t Type) bool {
    if r, ok := t.(*RefType); ok {
        return r.mut
    }
    return false
}
func isValueType(t Type) bool // ported from check_transitions.go:189-217

// solver.Context gains the liveness fields the ported checker reads.
type Context struct {
    // ... existing M1–M3 fields (level, Probe, etc.)
    Liveness *liveness.Liveness
    Aliases  *liveness.AliasTracker
}
```

---

## Revised PR breakdown (finer than the first cut)

The first draft of this plan used five PRs, two of them "large" (the `mut` gate
and lifetimes lumped whole). For "the big one" that is too coarse to review
well — the gate in particular deserves a PR where it is the *only* thing under
review. The breakdown below is **13 PRs across 5 phases**, each independently
mergeable, each keeping `go test ./...` green, each shipping its own
table-driven tests (rendered types + **full** error messages, per CLAUDE.md).
LoC figures are non-test estimates.

### Phase A — Records & tuples (carrier + exactness)

- **A1 — Representation + literal inference + coalescing + printer** (~250).
  `RecordType`/`TupleType` with the `exact` flag; smart constructors; `ObjectExpr`
  ⇒ exact record, `TupleExpr` ⇒ exact tuple; positive-position coalescing
  (covariant fields/elems); printer for `{x: T}`, `[T, U]`, trailing `...`.
  *Mergeable alone:* infers and renders literals; no subtyping needed to test.
- **A2 — Structural + exactness subtyping** (~200). `constrainRecords` /
  `constrainTuples` with the one-way exact/inexact rule (width subtyping only
  inexact↔inexact; exact↔exact same-set; extra-member rejection). *Builds on A1;
  first PR that accepts/rejects against annotations.*

### Phase B — Usage-based inference

- **B1 — Member-read usage inference + negative-position record coalescing**
  (~250). `MemberExpr` read ⇒ `constrain(recv <: {field: β, ...})`; merge object
  upper-bounds into one record at coalescing; close to **exact** (Policy A).
  Replaces `Open`/`Widenable`/`ArrayConstraint` (re-express representative
  old-checker cases as intended-form tests).
- **B2 — The `open` parameter marker** (~120). Parser keyword (provisional) +
  semantic flag that keeps a usage-inferred param **inexact**. *Small, isolated;
  the surface syntax can be gated if the spelling is unsettled.*

### Phase C — Borrows & mutability (**the gate**)

- **C1 — `RefType` plumbing** (~200). The wrapper type, `RefInner` sealed marker,
  `borrowableType`, `NewRef` smart constructor (degenerate-cell invariant),
  `unwrapRef`/`carrierOf`, printer. **No constrain rule yet.** *Pure plumbing +
  unit tests on the constructor/marker/printer; trivially green.*
- **C2 — The `RefType` constrain rule** (~180). **← THE GATE.** The single rule:
  mutability compatibility, mut-driven bidirectional inner sweep (read/write
  decomposition), lifetime steps written but inert (`lt == nil`), and the two
  cross-cases. *Scoped so the gate is the only thing under review.* Acceptance:
  `mut {x,y} <: mut {x}` **fails** while immutable `{x,y} <: {x}` succeeds;
  mut-borrow decay (`mut {x} <: {x}`) allowed, reverse rejected.
  **If this can't be encoded cleanly against the real AST, stop and reassess
  before Phase D/E** (the milestone's instruction).
- **C3 — Field-write inference + read-after-write** (~180). `obj.x = v` ⇒ a `mut`
  record requirement (lifetime nil for now); literal widening; multi-write merge;
  read-after-write field collapse. Acceptance: `fn foo(obj){obj.x=5; obj.y=10}`
  ⇒ `(obj: mut {x: number, y: number}) -> unit`; `{obj.x=5; return obj.x}` ⇒
  `... -> number`.

### Phase D — Lifetimes (second sort)

- **D1 — Lifetime sort plumbing** (~220). `LifetimeVar`/`StaticLifetime`/
  `LifetimeUnion`, `constrainLt`, printer. *Standalone second-sort machinery +
  unit tests on `constrainLt` (outlives, transitivity, cycles, `'static` top).*
- **D2 — Activate lifetimes in the `RefType` rule + borrow origination** (~150).
  Turn on step 3 of the gate rule and the escape guards; `attachParamLifetimes`.
  Acceptance: `IdentityRefReturn` ⇒ `fn <'a>(p: mut 'a {x: number}) -> mut 'a {x:
  number}`; `FreshObjectReturn` carries no lifetime.
- **D3 — Multi-source unioning + escape-to-`'static`** (~140). Join branch
  lifetimes via a fresh join var; escape ⇒ `constrain(lt <: 'static)`. Acceptance:
  `ConditionalUnionReturn` ⇒ `mut ('a | 'b) {x}`; `EscapingRefIntoStatic` ⇒
  `mut 'static`.
- **D4 — Lifetime coalescing + elision** (~200). `analyzeLts` occurrence pass;
  drop a param-only lifetime that connects nothing; the **mut elide-in-place vs
  immutable drop-the-wrapper** branch (the subtle one — isolated on purpose).
  Acceptance: property-level / tuple-per-slot lifetimes; `RefType` passes the
  inner carrier's `exact` flag through unchanged.

### Phase E — Mutability-transition checking (port)

- **E1 — Port liveness + predicates + wiring** (~220). Reuse `internal/liveness/`
  verbatim and `liveness_prepass.go`; reimplement `isValueType`/`isMutableType`
  over `soltype`; `checkMutabilityTransition` logic unchanged; add `Liveness`/
  `Aliases` to `Context`. Port the old transition fixtures as intended-form tests.
- **E2 — Collapse the static-alias escape hatches** (~120). Drop
  `HasStaticMutAlias`/`HasStaticImmAlias`; the transition checker queries the
  lifetime sort (`lt <: 'static`) directly. *Depends on Phase D, hence last; the
  only genuinely new logic in the port.*

### Dependency graph

```
A1 → A2 → B1 → B2
            └─→ C1 → C2(GATE) → C3 → D1 → D2 → D3 → D4 → E1 → E2
```

A1→A2 (subtyping needs the carrier); B1 needs A's coalescing; C2 needs C1's
wrapper; C3/Phase D ride on the gate; E2 needs Phase D's escape-as-`'static`.
A and the test scaffolding for B can be drafted in parallel, but merge order is
strict. The gate (C2) is reached after only the minimum needed to exercise it
against the real AST, and nothing expensive (lifetimes, the port) is built before
it is cleared.

Total ≈ 2.4k LoC non-test across 13 PRs — a realistic shape for "the big one,"
with the single highest-risk change (C2) isolated to ~180 reviewable lines.

---

## Testing strategy

Per [02-design-notes.md](02-design-notes.md) §"Test coverage" and CLAUDE.md:
table-driven `*_test.go` keyed by name, with `expectedValues`/`expectedTypes`
(rendered Escalier annotations) and `expectedError` (**full** message), authored
against intended semantics — not copied from the old checker. Use inline snapshots
for large type trees (nested borrowed records with per-field lifetimes). Each PR
carries the acceptance set named in its section; the union is the milestone's M4
criteria. No fixture-harness wiring yet (that's M7); M4 is validated entirely by
the new package's own tests driven from real `.esc` source through the M2 bridge.

## Risks / open questions

- **The gate (C2)** is the dominant risk and is front-loaded with an explicit
  stop-and-reassess. The spike de-risked the *algorithm*; residual risk is
  encoding it cleanly against the production AST/`soltype`.
- **Elision branching on `mut` (D4)** is the subtlest non-gate piece — the
  immutable-borrow-must-drop-the-wrapper rule, if missed, reintroduces the
  forbidden degenerate cell. The `NewRef` assertion is the backstop.
- **`open` keyword (B2)** is provisional; gate the spelling, keep the semantic
  flag — the inference behavior is what M4 must prove.
- **Probe usage**: M4's core path is monotone (no rollback needed for failed
  constraints), but the field-write merge and any future union-against-variable
  deferral should be written probe-aware so M6/M8 speculation composes.
- **Exact-by-default spec**: the Policy-A default and the `open` opt-out are **not
  yet in `planning/exact-types/requirements.md`** — that section should be written
  before B1 lands so the tests assert an agreed default.
