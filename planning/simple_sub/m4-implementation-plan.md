# M4 implementation plan — Core value types: records + usage-based inference + `mut` + lifetimes + destructuring/`match`

This is the implementation plan for **M4** as defined in
[01-milestones.md](01-milestones.md) §"M4 — Core value types". M1
(`internal/soltype/` + `internal/solver/`), M2 (parser/resolver bridge), M2.5
(provenance + precise error spans), and M3 (let-polymorphism, function
exactness, probe, overloading, async, `ErrorType` recovery) have **landed on
main**; this plan is written against that code, not against the spike or the
pre-M1 design sketches. Where the two disagree, the shipped code wins and is
cited.

The design source remains [02-design-notes.md](02-design-notes.md) (the
`RefType` wrapper, exactness, the probe) and the lifetime lattice in
[03-references.md](03-references.md). Spike provenance: `internal/simplesub/`
M2 (records), M3 (`mut`), M4 (lifetimes).

## What M1–M3 actually shipped (ground truth this plan builds on)

Facts that changed or sharpened the original plan, from the code on main:

1. **Package layout.** Types live in **`internal/soltype/`** (a top-level
   sibling, not `internal/solver/soltype/`): `type.go`, `print.go`,
   `polarity.go`, `visitor.go`. The engine is `internal/solver/`:
   `constrain.go`, `coalesce.go`, `context.go`, `errors.go`, `infer.go` /
   `infer_expr.go` / `infer_decl.go` / `infer_stmt.go`, `module.go`,
   `overload.go`, `poly.go`, `prelude.go`, `probe.go`, `prov.go`, `scope.go`,
   `simplify.go`, `type_ann.go`.
2. **`RecordType` already exists** (M2): `Fields []*RecordField{Name, Type}` —
   an **ordered slice with last-wins dedup**, not a map; `Field(name)` is the
   canonical lookup. Record/tuple **literal inference already works**
   (`inferObject` / tuple walk), as does **member-read usage inference**:
   `inferMember` lowers `recv.prop` to `constrain(recv, {prop: β})`
   ([infer_expr.go:716](../../internal/solver/infer_expr.go)). M4 does *not*
   introduce these — it refines them.
3. **The exactness flag is spelled `Inexact`**, not `exact` — `FuncType.Inexact`
   (M3 PR4) deliberately makes the **zero value exact**, matching
   exact-by-default, so structural rewriters carry the flag through unchanged.
   `RecordType`/`TupleType` follow the same convention; conveniently, every
   record/tuple M2 already mints becomes exact *by zero value*.
4. **One width-tolerant record arm exists and is explicitly provisional.** The
   `RecordType <: RecordType` case in
   [constrain.go:181](../../internal/solver/constrain.go) serves only member
   access and carries a long comment: M4 must split the **field-selection
   requirement** (width-tolerant) from **concrete record `<:` record**
   (exact-by-default). Likewise the tuple arm is exact-only ("M4 adds the exact
   flag and the inexact arm") and the function arm has a **KNOWN GAP (M4)**
   note for inexact-callback extra positions (Variation B, needs the `⊤` rule —
   see Open Questions).
5. **Errors are typed `SolverError` structs with blame spans** (M2.5), reported
   via `c.report(...)` — not `[]error`/`fmt.Errorf`. Several M4 error paths are
   *already stubbed* in [errors.go](../../internal/solver/errors.go):
   `MissingPropertyError`'s receiver/site blame arms "cover the M4 concrete
   record cases", namespace member access "(`Foo.bar`) is M4",
   member-assignment mutability "is M4".
6. **Bound mutations are journal-gated.** Bound lists may be extended **only**
   through `Context.addLowerBound`/`addUpperBound`, which fuse the probe
   snapshot with the append ([context.go](../../internal/solver/context.go)).
   The probe (M3 PR5) journals `*TypeVarType` plus Info/Prov/errs rollback
   closures. M4's `LifetimeVar` is a second bound-list sort, so the **probe
   must be extended** to journal it under the same discipline.
7. **Structural rewrites ride a polarity-threading visitor.** Every
   `soltype.Type` implements `Accept(v TypeVisitor, pol Polarity) Type`
   (#716/#717); coalesce, extrude, and freshenAbove are built on it. New
   formers (`RefType`) extend the visitor — no hand-rolled recursion. `LevelOf`,
   `equalType`, `Print`/`PrintAsScheme` also need arms per new former.
8. **Coalescing stays in the `soltype` universe.** Production coalesce returns
   a native `soltype.Type` (the spike's `type_system` conversion is gone), and
   **simplification runs at display time** (`coalesceScheme` + co-occurrence
   merging in `simplify.go`), leaving raw scheme bodies intact for
   instantiation. Lifetime naming/elision therefore happens at display time.
9. **Reassignment exists for identifiers** (M3 PR8): `inferAssign` handles
   `a = expr` with `CannotAssignToImmutableError`; the member/index target
   branch currently reports "unsupported — deferred to M4"
   ([infer_expr.go:495](../../internal/solver/infer_expr.go)). M4's field-write
   path **extends that branch**, it does not build a new one. PR8 also left
   `var a = 5; a = 6` failing until M4's `var` literal widening.
10. **`ErrorType` absorbs at the top of `constrain`**, so every new M4 arm gets
    error-recovery behavior for free. **`PromiseType` exists** and must be
    classified by `RefInner`/`borrowableType` (excluded — a promise is shared,
    not borrowed). **`UnionType`/`IntersectionType` are coalesced-output-only**
    nodes; their general constrain rules remain M6.
11. **`Scope`/`Namespace` exist** (`GetValue`/`GetType`/`GetNamespace`); a
    namespace in value position errors in `inferIdent`. M4 moves that error to
    the value-position consumer and adds member *lookup*.
12. **`resolveTypeAnn` covers only primitives + `Promise<T>`**
    ([type_ann.go](../../internal/solver/type_ann.go)). M4's acceptance tests
    need record/tuple annotations (incl. the trailing `...` and `mut`/lifetime
    forms), so the annotation surface must grow accordingly.
13. **Milestone renumbering**: library type resolution is the new M7, the
    fixture harness is **M8**, type-level operators **M9**, codegen **M10**.
    The exact-types spec now records Policy A and the `open` marker (§8.1) —
    the original plan's "spec must be written first" risk is **resolved**.
14. **The milestone itself grew**: M4 now also owns **destructuring patterns +
    the `match` expression form**, **namespace member lookup**, **`var`-binding
    literal widening**, and the selection-vs-concrete record split (all in
    [01-milestones.md](01-milestones.md) §M4).

## Why M4 is "the big one"

Records, `mut`, and lifetimes are inseparable: lifetimes ride on borrows,
records are the first borrowable value, and `mut` borrows are what first
populate a lifetime. The milestone names the `RefType` rule's `mut`-driven inner
invariance as the **HIGHEST-RISK gate**: if it cannot be encoded cleanly against
the real AST, the whole migration is in question. The PR sequence below reaches
the gate after only the minimum prerequisite work, and nothing expensive
(lifetimes, patterns, the transition port) starts before it is cleared.

## Scope

In (per the updated milestone):

1. `Inexact` flag on `RecordType`/`TupleType`; exact/inexact subtyping rules;
   the selection-vs-concrete record split; tuple inexact arm + tuple spread.
2. Record/tuple type annotations (incl. `...`, `mut`, lifetimes) in
   `resolveTypeAnn`.
3. Usage-based inference *refinement*: negative-position record merging at
   coalesce; Policy-A exact closing; the `open` parameter marker.
4. `var`-binding literal widening (usage-based over all assignment sites).
5. The unified `RefType{Mut, Lt, Inner}` wrapper + the single constrain rule
   (**the gate**); field-write inference via `inferAssign`'s member branch;
   read-after-write.
6. Lifetimes as a second sort (`LifetimeVar`, `constrainLt`, probe extension,
   borrow origination, joins, escape, display-time coalescing + elision).
7. Destructuring patterns (`TuplePat`/`RecordPat`/literal patterns) + the
   `match` expression over structural patterns, with exactness-driven
   exhaustiveness.
8. Namespace member lookup (`resolvePath`, `Foo.bar`, `Foo["x"]`, new errors).
9. Mutability-transition checking ported (`internal/liveness/` verbatim + two
   predicates over `soltype`).

Out: classes (M5), union/intersection constrain rules and optional chaining
(M6), TypeRef/alias resolution (M7), fixture harness (M8), type-level operators
(M9), codegen / `exact<T>(v)` (M10).

---

## Key types and function sketches

Sketches use the **shipped conventions**: exported fields, `Inexact` (zero value
= exact), `[]SolverError`, journal-gated bound appends, visitor `Accept`.

### Exactness on the existing carriers (`soltype/type.go`)

```go
// RecordType gains Inexact (M3's FuncType convention: zero value = exact, so
// every record M2 already mints — literals, member requirements before this
// milestone — is exact by default with no construction-site churn).
type RecordType struct {
    Fields  []*RecordField // ordered, name-deduped (last wins); Field(name) lookup
    Inexact bool           // trailing `...` ⇒ true
}

type TupleType struct {
    Elems   []Type
    Inexact bool // `[A, B, ...]` ⇒ true: longer <: shorter becomes legal vs an inexact RHS
}
```

The **selection-vs-concrete split** falls out of the flag: `inferMember`'s
synthesized requirement becomes `{prop: β, Inexact: true}` ("has at least this
field" — width-tolerance *is* inexactness), and the record arm then implements
the one-way rule uniformly instead of being unconditionally width-tolerant:

```go
case *soltype.RecordType:
    if r, ok := rhs.(*soltype.RecordType); ok {
        var errs []SolverError
        for _, rf := range r.Fields {               // depth: fields covariant
            lt, ok := l.Field(rf.Name)
            if !ok {
                errs = append(errs, &MissingPropertyError{LHS: l, RHS: r, Name: rf.Name})
                continue
            }
            errs = append(errs, c.constrain(lt, rf.Type, seen)...)
        }
        // One-way exactness (02-design-notes §"Exactness"):
        //   exact <: inexact   ok (width)     inexact <: inexact   ok (width)
        //   exact <: exact     same field set inexact <: exact     rejected
        if !r.Inexact {
            if l.Inexact {
                errs = append(errs, &InexactIntoExactError{LHS: l, RHS: r})
            }
            for _, lf := range l.Fields {
                if _, ok := r.Field(lf.Name); !ok {
                    errs = append(errs, &ExtraPropertyError{LHS: l, RHS: r, Name: lf.Name})
                }
            }
        }
        return errs
    }
```

### The borrow wrapper (`soltype/type.go`)

```go
// RefType is the single wrapper for borrows and mutability.
//   Mut=false Lt=nil  -> forbidden degenerate cell (NewRef returns bare Inner)
//   Mut=false Lt='a   -> immutable borrow      Mut=true Lt=nil -> owned mutable
//   Mut=true  Lt='a   -> mutable borrow
type RefType struct {
    Mut   bool
    Lt    Lifetime // nilable
    Inner RefInner
}

// RefInner is the sealed set of types that may sit inside a RefType.
// PrimType/LitType/FuncType/PromiseType/nested RefType are deliberately excluded
// (a promise/function reference is shared, not borrowed; mut number is a JS no-op).
type RefInner interface {
    Type
    isRefInner()
}
func (*RecordType) isRefInner()  {}
func (*TupleType) isRefInner()   {}
func (*TypeVarType) isRefInner() {} // mid-inference; checked at constrain time
// + UnionType/IntersectionType (M6 inputs), AliasType (M7), ClassType (M5).

// NewRef collapses the degenerate cell so no *RefType type-switch ever sees a
// borrow-shaped value that isn't a borrow.
func NewRef(mut bool, lt Lifetime, inner RefInner) Type {
    if !mut && lt == nil {
        return inner
    }
    return &RefType{Mut: mut, Lt: lt, Inner: inner}
}

func unwrapRef(t Type) (inner Type, mut bool, lt Lifetime) // peel helpers used by
func carrierOf(t Type) Type                                // member access, patterns, equalType
func borrowableType(t Type) bool                           // content invariant behind TypeVarType

// Accept: RefType joins the rewriting visitor. The inner is reachable in BOTH
// polarities when Mut (read+write views), but the REWRITERS visit it once in the
// current polarity (the read view) — extrude/freshenAbove share fresh vars via
// the cache, exactly as the spike's extrude did for Mut. The lifetime is NOT a
// Type and is carried through unchanged by Accept; only the lifetime-aware
// passes (D4) walk it.
func (t *RefType) Accept(v TypeVisitor, pol Polarity) Type
```

`LevelOf` gains a `case *RefType: return LevelOf(t.Inner)` arm; `equalType` and
`Print` (`mut {x: number}`, `'a {x: number}`, `mut 'a Point`) get matching arms.

### The single `RefType` constrain rule — **THE GATE** (`solver/constrain.go`)

```go
// Generalized from the spike's *Mut case (simplesub/constrain.go:182) to the
// unified wrapper. Lifetime steps are written now, inert while Lt == nil (C2),
// activated in D2.
case *soltype.RefType:
    if r, ok := rhs.(*soltype.RefType); ok {
        // 1. mutability compatibility — can't widen immutable to mutable.
        if !l.Mut && r.Mut {
            return []SolverError{&MutabilityMismatchError{LHS: l, RHS: r}}
        }
        // 2. inner variance — bidirectional iff the TARGET writes (read/write
        //    decomposition = invariance). Mut-decay (l.Mut && !r.Mut) falls
        //    through to the covariant-only read view.
        errs := c.constrain(l.Inner, r.Inner, seen)
        if r.Mut {
            errs = append(errs, c.constrain(r.Inner, l.Inner, seen)...)
        }
        // 3. lifetime — covariant when both present.
        switch {
        case l.Lt != nil && r.Lt != nil:
            c.constrainLt(r.Lt, l.Lt)
        case l.Lt == nil && r.Lt != nil: // owned source satisfies any borrow slot
        case l.Lt != nil && r.Lt == nil: // borrow into owned slot: escape
            errs = append(errs, &BorrowEscapeError{LHS: l, RHS: r})
        }
        return errs
    }
    // RefType <: bare: legal only for an owned value (no lifetime); peel.
    if l.Lt != nil {
        return []SolverError{&BorrowEscapeError{LHS: l, RHS: rhs}}
    }
    return c.constrain(l.Inner, rhs, seen)
```

```go
// bare <: RefType (after the structural switch, before the variable arms):
// wrap the source as an immutable no-lifetime view and re-dispatch into the
// RefType<:RefType branch. Construct the struct literal DIRECTLY — NewRef would
// collapse (false, nil) back to the bare inner and recurse forever.
if r, ok := rhs.(*soltype.RefType); ok {
    if inner, ok := asRefInner(lhs); ok {
        return c.constrain(&soltype.RefType{Mut: false, Lt: nil, Inner: inner}, r, seen)
    }
}
```

### The lifetime sort + probe extension (`soltype/lifetime.go`, `solver/probe.go`)

```go
type Lifetime interface{ isLifetime() }
type LifetimeVar struct {
    ID          int
    LowerBounds []Lifetime
    UpperBounds []Lifetime
}
type StaticLifetime struct{} // 'static, top of the outlives lattice

// Context grows the spike's M4 fields (context.go's own comment reserves them):
type Context struct {
    varCounter      int
    probe           *Probe
    lifetimeCounter int          // M4
    // ... paramLifetimes / written live on the checker carrier (see C3/D2)
}

// constrainLt mirrors constrain over the outlives lattice: var-left gains an
// upper bound, var-right a lower bound, var-to-var records both; 'static is
// top (X <: 'static always holds); (lhs, rhs)-keyed seen-set for cycles.
// Bound appends go ONLY through addLowerLtBound/addUpperLtBound — the journal
// invariant extends to the second sort:
func (c *Context) constrainLt(lhs, rhs Lifetime)
func (c *Context) addLowerLtBound(v *soltype.LifetimeVar, lt soltype.Lifetime)
func (c *Context) addUpperLtBound(v *soltype.LifetimeVar, lt soltype.Lifetime)

// Probe today holds the CONCRETE *soltype.TypeVarType and truncates its exported
// bound fields inline on discard (probe.go's probeEntry.v is *TypeVarType). M3 had
// one bounded sort, so it kept the concrete entry rather than abstracting over
// "things with bound lists".
//
// M4 adds a SECOND bounded sort (LifetimeVar). RESOLVED: the probe stays concrete.
// It gains a parallel journal for LifetimeVar — a second probeEntry kind keyed by
// *soltype.LifetimeVar, with the same length-snapshot + truncate-on-discard
// discipline as the *TypeVarType path. This keeps soltype's public surface clean:
// no speculation-only truncate verb exported on the soltype types, and no
// cross-package abstraction. The cost is two near-identical discard paths, which is
// cheap and contained.
```

### Field write + read-after-write (`solver/infer_expr.go`)

```go
// NEW HELPER (B3 authors it; not yet in the production solver — only the spike
// has it, internal/simplesub/constrain.go). widen generalizes a literal to its
// primitive (5 ⇒ number, "x" ⇒ string) and passes non-literals through:
//   func widen(t soltype.Type) soltype.Type
//
// Extends inferAssign's existing member/index branch (today:
// reportUnsupportedFeature, infer_expr.go:495). C3 ships with Lt: nil — no
// borrows exist yet, every receiver is owned — and D2 flips the requirement's
// lifetime to a fresh var (so borrowed receivers of any lifetime are accepted)
// and re-runs C3's acceptance.
func (c *checker) inferMemberAssign(scope *Scope, lvl int, e *ast.BinaryExpr, m *ast.MemberExpr) soltype.Type {
    recv := c.inferExpr(scope, lvl, m.Object)
    rhs := c.inferExpr(scope, lvl, e.Right)
    w := widen(rhs) // 5 ⇒ number: a later write may store any number
    req := &soltype.RefType{Mut: true, Lt: nil /* D2: c.freshLifetime() */,
        Inner: &soltype.RecordType{
            Fields:  []*soltype.RecordField{{Name: m.Prop.Name, Type: w}},
            Inexact: true, // "must accept a write to this field", not a full shape
        }}
    c.constrain(e, recv, req)
    c.recordWritten(recv, m.Prop.Name, w) // spike's `written` map: read-after-write
    return w                               // assignment evaluates to the stored value
}
```

### Display-time lifetime coalescing + elision (`solver/coalesce.go`, `simplify.go`)

Production simplification runs at display time (`coalesceScheme`), so elision
lives there — raw scheme bodies keep their `RefType`/lifetime structure for
instantiation. Adapted from the spike's `analyzeLts`/`coalesceLifetime`:

```go
// Occurrence analysis (extended over RefType/lifetimes): a param lifetime kept
// iff it occurs in both polarities (connects an input to an output) or is
// forced to 'static. The RefType lifetime is COVARIANT — the mut-driven
// bidirectional inner sweep never touches it (it lives on the wrapper, not in
// the inner), so it cannot be accidentally invariated.
//
// Elision branches on Mut — the subtle case:
//   mut, lifetime elided   ⇒ &RefType{Mut: true, Lt: nil, Inner: inner}  // owned-mutable
//   immut, lifetime elided ⇒ inner                                       // MUST drop the
//        // wrapper: RefType{false, nil, _} is the forbidden degenerate cell
//
// Naming: only param-originated lifetimes are named ('a, 'b, …); a join
// variable renders as the union of the param lifetimes it reaches ('a | 'b);
// 'static absorbs. Rendered via soltype Print/PrintAsScheme (fn <'a>(…)).
```

### Borrow origination (`solver/infer_expr.go`, D2)

```go
// A RefType-typed parameter without a lifetime is a borrow of whatever the
// caller lends: attach a fresh lifetime var and record it as nameable.
func (c *checker) attachParamLifetimes(t soltype.Type) soltype.Type {
    r, ok := t.(*soltype.RefType)
    if !ok || r.Lt != nil {
        return t
    }
    lt := c.freshLifetime()
    c.paramLifetimes.Add(lt.ID)
    return &soltype.RefType{Mut: r.Mut, Lt: lt, Inner: r.Inner}
}
```

### Namespace member lookup (`solver/infer_expr.go`, Phase F)

```go
// resolvePath (NEW) resolves an ident/member/index chain to Value | Namespace (a
// name is never both — scope invariant); pathResult (NEW) is its sum return. The
// object/index position tolerates a namespace; every other value position rejects
// one — so NamespaceUsedAsValueError moves OFF inferIdent to the value-position
// consumer and fires once for both f(Foo) and f(A.B).
//
// Namespace lookup reads the namespace's OWN maps directly — soltype's `Namespace`
// (scope.go:61-66) is a struct of FIELDS (Values / Types / Nested), with NO
// methods today: read `ns.Values[name]` / `ns.Nested[name]` inline, non-lexical
// (no parent walk), unlike Scope.GetValue/GetType/GetNamespace which DO walk
// parents. (01-milestones.md names hypothetical LookupValue/LookupNamespace
// helpers; those don't exist yet — add them as thin wrappers over the fields if
// the inline reads get repetitive, but they are not shipped API.) Index keys must
// be statically constant strings.
// New errors: UnknownNamespaceMemberError, DynamicNamespaceIndexError.
func (c *checker) resolvePath(scope *Scope, e ast.Expr) pathResult
```

### Transition checking (`solver/transitions.go`, Phase G)

```go
// internal/liveness/ ports verbatim; only two predicates are reimplemented:
func isMutableType(t soltype.Type) bool {
    if r, ok := t.(*soltype.RefType); ok {
        return r.Mut
    }
    return false
}
func isValueType(t soltype.Type) bool // from checker/check_transitions.go:189-217
// solver.Context gains Liveness / Aliases, populated by the existing prepass.
// G2: HasStaticMutAlias/HasStaticImmAlias collapse — query `lt <: 'static`
// from the lifetime sort instead.
```

---

## PR breakdown

17 PRs across 7 phases, each independently mergeable and green, each with
table-driven tests asserting rendered types and **full** error messages. Every
PR below names the concrete files touched, the data structures added/modified,
the algorithm changes, and its acceptance set — enough to start implementation
without further planning. The gate (C2) sits 3rd on the critical path.

A standing rule for every PR that adds a `soltype` former or a second sort:
**touch every site that switches over the type set.** The shipped checklist is
`type.go` (the `isType` marker + `LevelOf` arm), `visitor.go` (the `Accept`
method), `print.go` (`printType` + `typePrec` + `freeTypeVars`), and
`solver/coalesce.go`'s `equalType`. Missing one is a latent panic
(`printType`/`equalType` end in `panic("unhandled %T")`) or a silent wrong
result (`LevelOf` defaulting to 0 — see its `IntersectionType` comment for why
that corrupts `freshenAbove`). Each PR's "structures" list calls out which of
these arms it must add.

### Phase A — Records & tuples completion (the M2 leftovers)

- **A1 — `Inexact` flag + record exactness rules + the selection split**
  (~200).
  - **Files:** `soltype/type.go`, `soltype/visitor.go`, `soltype/print.go`,
    `solver/constrain.go`, `solver/infer_expr.go`, `solver/errors.go`,
    `solver/coalesce.go`.
  - **Structures:**
    - add `Inexact bool` to `RecordType` (zero value = exact, the
      `FuncType.Inexact` convention — every record M2 mints stays exact with no
      construction churn).
    - `visitor.go`'s `RecordType.Accept` must carry the flag on rebuild:
      `out = &RecordType{Fields: fields, Inexact: cur.Inexact}` (today line 123
      drops it — a latent bug the new field exposes).
    - `equalType`'s `RecordType` arm gains `a.Inexact != b.Inexact` as a
      discriminator (mirrors the `FuncType` arm's `a.Inexact != b.Inexact` at
      coalesce.go:315).
    - `print.go` appends a trailing `...` entry in the record case when `Inexact`
      (mirroring `printFuncTail`'s `if t.Inexact { ps = append(ps, "...") }`).
  - **Algorithm — the record constrain arm** (constrain.go:181, replacing the
    "this is NOT the final semantics" body):
    - keep the per-field covariant loop (depth subtyping)
    - then add the one-way exactness gate: when `!r.Inexact`, reject `l.Inexact`
      (an `InexactIntoExactError`) and reject any field on `l` absent from `r`
      (`ExtraPropertyError` per extra field)
    - when `r.Inexact`, the existing width-tolerant loop is complete and unchanged
  - **Algorithm — selection vs concrete:** flip `inferMember`'s synthesized
    requirement (infer_expr.go:716) to `Inexact: true` so member access stays
    "has at least this field," now expressed *as inexactness* rather than as an
    unconditionally width-tolerant arm. This is the split the milestone calls
    for: the same `RecordType <: RecordType` rule serves both selection (RHS
    inexact) and concrete subtyping (RHS exact).
  - **Errors:** new, same field/blame shape as `MissingPropertyError`
    (errors.go:97):
    - `InexactIntoExactError{LHS, RHS *soltype.RecordType}`
    - `ExtraPropertyError{LHS, RHS *soltype.RecordType; Name string; prov; site}`
  - **Accept:**
    - exact `{x, y}` `<:` inexact `{x, y, ...}` succeeds
    - inexact `<:` exact and extra-member-on-exact reject (full messages)
    - existing member-access tests stay green (selection is now the inexact path)

- **A2 — Tuple inexactness + tuple spread + computed-key story** (~170).
  - **Files:** `soltype/type.go`, `soltype/visitor.go`, `soltype/print.go`,
    `solver/constrain.go`, `solver/coalesce.go`, `solver/infer_expr.go`.
  - **Structures:** add `Inexact bool` to `TupleType` and thread it through:
    - `TupleType.Accept` (visitor.go:109 — `&TupleType{Elems: elems, Inexact:
      cur.Inexact}`)
    - `equalType`'s tuple arm
    - `printType`'s tuple case (a trailing `, ...`)
  - **Algorithm — tuple constrain arm** (constrain.go:162): replace the strict
    `len(l.Elems) != len(r.Elems)` reject with the exactness-aware rule — when
    `r` is exact, lengths must match; when `r` is inexact, `len(l) >= len(r)`
    and the shared prefix is covariant (the `longer <: shorter` case the arm's
    own M4 comment promises). This *narrows* `TupleLengthMismatchError`'s firing
    conditions (its errors.go:66 note anticipates this).
  - **Algorithm — tuple spread:** in the tuple-literal walk, handle
    `ast.ArraySpreadExpr` (today `reportUnsupported` → `ErrorType` slot, see
    `infer_obj_test.go:31` "Tuple/array spread is M4"):
    - infer the spread operand
    - require it to be a `TupleType`
    - splice its element types into the literal's `Elems`

    A non-tuple spread operand is a typed error.
  - **Algorithm — object computed/numeric keys** (inferObject, infer_expr.go:660
    `objKeyName` fail path): a statically-constant string/numeric key resolves
    to a field name; a genuinely dynamic key (`{[k]: v}`) stays a typed error
    (full index-signature support rides M9 index types). Keep the last-wins
    dedup invariant.
  - **Accept:**
    - `[1, 2]` `<:` `[number, ...]` succeeds, `<:` `[number]` rejects
    - a spread `[...pair, 3]` builds the spliced tuple
    - a constant numeric key resolves, a dynamic key errors

- **A3 — Record/tuple/`mut`/lifetime type annotations** (~180).
  - **Files:** `solver/type_ann.go`, plus tests.
  - **Algorithm — extend `resolveTypeAnn`** (type_ann.go:21, today
    primitives + `Promise<T>` only):
    - add arms for object-type and tuple-type annotations (building
      `RecordType`/`TupleType`, honoring a trailing `...` ⇒ `Inexact: true`)
    - add arms for the `mut`/lifetime annotation forms, which lower to `RefType`
      (`mut T` ⇒ `RefType{Mut: true}`, `'a T` ⇒ `RefType{Mut: false, Lt: …}`)

    **Ordering caveat:** `RefType` does not exist until C1, so A3 lands the
    record/tuple annotation arms now and gates the `mut`/lifetime arms behind C1
    (until then a `mut`/`'a` annotation keeps today's `reportUnsupportedFeature`
    + recovery-var behavior). Track the gated arms as a one-line follow-up in
    C1's checklist.
  - **Why first-ish:** every annotation-side acceptance test in Phase A/B/D
    (`var p: {x, y, ...} = …`, `fn f(p: mut {x: number}) …`) needs this; it is
    a leaf with no dependency on the constraint changes.
  - **Accept:**
    - `val r: {x: number, ...} = {x: 1, y: 2}` checks (inexact target admits the
      extra field)
    - `val r: {x: number} = {x: 1, y: 2}` rejects (exact target)
    - tuple annotations round-trip through the printer

### Phase B — Usage-based inference refinement

- **B1 — Negative-position record merging + Policy-A exact closing** (~190).
  - **Files:** `solver/coalesce.go`, plus tests.
  - **Background:** today `combine` (coalesce.go:254) builds a bare
    `IntersectionType` of a negative variable's upper bounds. When two of those
    bounds are member-access requirements on one receiver (`{a: β}` and
    `{b: γ}`), the rendered type is the non-compact `{a: β} & {b: γ}` instead of
    `{a: β, b: γ}`.
  - **Algorithm — port the spike's `mergeObjects`** into the negative
    (`Negative` polarity) path of `combine`: before wrapping parts in an
    `IntersectionType`, fold all `RecordType` parts into one record (union the
    field sets; a field appearing in several parts becomes the intersection of
    its types). This is the production analogue of `simplesub/coalesce.go`'s
    `mergeObjects`/`mergeObjectGroup`, rewritten over `soltype.RecordType`'s
    slice fields and `Field(name)` lookup, using the existing `combine`/`dedup`
    structure. Mut-record merging (`mut {x} & mut {y}` ⇒ `mut {x, y}`) is
    deferred to C3 once `RefType` exists.
  - **Algorithm — Policy-A close:** the merged usage record closes to **exact**
    (`Inexact: false`) at this display-time fold — the row is sealed once body
    inference completes (spec §8.1). The per-access requirements stay inexact
    (A1); only the *coalesced* result is exact. `open` (B2) is the opt-out.
  - **Accept:** `fn (p) { p.a; p.b }` infers a param rendering `{a: …, b: …}`
    (one exact record), not `{a: …} & {b: …}`.

- **B2 — The `open` parameter marker** (~120).
  - **Files:** parser hook (provisional keyword), `solver/infer_expr.go` (param
    binding), `solver/coalesce.go`.
  - **Structures:** carry an `open` bit from the parsed param to the coalescer —
    cheapest form is a `set.Set[*soltype.TypeVarType]` of open param vars on the
    checker, consulted in B1's close step.
  - **Algorithm:** when a param is marked `open`, B1's Policy-A close leaves its
    usage record `Inexact: true` (row-polymorphic) so callers may pass richer
    records. Everything else is unchanged.
  - **Accept:**
    - `fn dist(open p) { p.x; p.y }` renders an inexact `{x, y, ...}` param
    - an un-`open` peer renders exact `{x, y}`
    - passing `{x, y, z}` to the `open` one checks, to the closed one rejects

- **B3 — `var` literal widening (authors `widen`)** (~150).
  - **Files:** `solver/constrain.go` or a new `solver/widen.go` (the helper),
    `solver/infer_decl.go`, plus tests.
  - **Structures/helper:** author `func widen(t soltype.Type) soltype.Type`
    (ported from `simplesub/constrain.go`): a `LitType` widens to its `PrimType`
    (`5` ⇒ `number`, `"x"` ⇒ `string`, `true` ⇒ `boolean`); every other type
    passes through. C3's field-write reuses it.
  - **Algorithm:** in `inferVarDecl` (infer_decl.go:108), a `var` binding's
    initializer type is widened before generalization, and — the principled
    milestone form — informed by **all** assignment sites: collect the
    initializer plus every later `a = e` RHS and coalesce their widened types
    (the binding's type is the join). A `val` is left un-widened (a fixed
    literal singleton). This retires PR8's documented `var a = 5; a = 6` failure
    (infer_expr.go:532 note). Reassignment's RHS-vs-binding constrain
    (inferAssign, infer_expr.go:540s) now checks against the widened binding
    type.
  - **Accept:**
    - `var a = 5; a = 6` checks (binding is `number`)
    - `val a = 5; a = 6` still rejects (`CannotAssignToImmutableError`)
    - `var a = 5` with no reassignment still renders `number` (default-widen)

### Phase C — Borrows & mutability (**the gate**)

- **C1 — `RefType` plumbing** (~230).
  - **Files:** `soltype/type.go`, `soltype/visitor.go`, `soltype/print.go`,
    `solver/coalesce.go` (`equalType`), `solver/type_ann.go` (un-gate A3's
    `mut`/lifetime arms), plus a new `soltype/ref.go` for the helpers.
  - **Structures:**
    - add `RefType{Mut bool; Lt Lifetime; Inner RefInner}`
    - the sealed `RefInner interface { Type; isRefInner() }`, with `isRefInner`
      on `RecordType`/`TupleType`/`TypeVarType` (and forward-declared for
      `UnionType`/`IntersectionType`/`AliasType`/`ClassType`)
    - `Lifetime` is a placeholder interface here (`type Lifetime interface{
      isLifetime() }` with no concretes yet — D1 adds them); C1 only ever sets
      `Lt: nil`
  - **Structures — full type-set checklist (the standing rule):**
    - `isType()` on `*RefType`
    - `LevelOf` arm (`case *RefType: return LevelOf(t.Inner)` — `Inner` is
      `RefInner` which embeds `Type`, so this typechecks)
    - `RefType.Accept` (see below)
    - `printType` arm (`mut {…}`, `mut Point`; immutable-borrow/lifetime forms
      render once D1 adds lifetime printing — until then `Lt` is always nil)
    - `equalType` arm (`a.Mut == b.Mut && equalType(a.Inner, b.Inner)` — lifetime
      equality joins in D1)
    - `freeTypeVars` descends `Inner`
  - **Algorithm — `RefType.Accept`:** the inner is visited **once in the current
    polarity** (the read view); the `Mut` write view shares fresh vars via the
    transform's own cache (exactly the spike's `extrude` treatment of `Mut` at
    `simplesub/constrain.go:293`). Copy-on-write like the other formers:
    rebuild only if `Inner` changed; carry `Mut`/`Lt` through unchanged. The
    lifetime is **not** a `Type`, so `Accept` never walks it — only the
    lifetime-aware passes (D4) do.
  - **Helpers:**
    - `NewRef(mut, lt, inner) Type` (collapses the degenerate `(false, nil)` cell
      to bare `inner`)
    - `unwrapRef(t) (inner, mut, lt)`
    - `carrierOf(t) Type` (peel any `RefType`)
    - `borrowableType(t) bool` (content invariant behind a `TypeVarType` inner)
  - **Accept:** no constrain rule yet, so trivially green:
    - `NewRef` collapses the degenerate cell
    - the printer renders `mut {x: number}`
    - `RefType` round-trips `Accept`

- **C2 — The `RefType` constrain rule** (~180). **← THE GATE.**
  - **Files:** `solver/constrain.go`, `solver/errors.go`, plus tests.
  - **Algorithm — the single rule** (new `case *soltype.RefType` in the
    structural switch, per the sketch above):
    - (1) mutability compatibility — `!l.Mut && r.Mut` ⇒
      `MutabilityMismatchError`
    - (2) inner variance — covariant read view always, plus a contravariant write
      view **iff `r.Mut`** (the read/write decomposition = invariance)
    - (3) lifetime step written but **inert while `Lt == nil`** (the `switch` over
      `l.Lt`/`r.Lt` is dead code until D2)
    - cross-case `RefType <: bare` — peel `l.Inner`, with an escape-error guard
      for `l.Lt != nil` that can't fire yet
    - cross-case `bare <: RefType` — wrap the source as `&soltype.RefType{Mut:
      false, Lt: nil, Inner: inner}` via a struct literal (**not** `NewRef`, which
      would collapse it and recurse forever) and re-dispatch
  - **Errors:**
    - `MutabilityMismatchError{LHS, RHS *soltype.RefType}`
    - `BorrowEscapeError{LHS, RHS}` (the latter's firing path is inert until D2)
  - **Accept (the gate):**
    - `mut {x, y} <: mut {x}` **fails** (invariance — the write view's
      contravariant `{x} <: {x, y}` is missing a field) while immutable
      `{x, y} <: {x}` width-**succeeds** (inexact RHS)
    - mut-decay `mut {x} <: {x}` allowed, the reverse `{x} <: mut {x}` rejected
    - full messages

    **Stop and reassess before any later phase if this does not encode cleanly
    against the real visitor/journal.**

- **C3 — Field-write inference + read-after-write** (~200).
  - **Files:** `solver/infer_expr.go` (the `inferAssign` member branch),
    `solver/infer.go` (the `written` map on the checker), `solver/coalesce.go`
    (mut-record merge), plus tests.
  - **Structures:** add `written map[fieldKey]soltype.Type` to the checker
    (`fieldKey struct{ recvID int; field string }`, ported from
    `simplesub/constrain.go:14`); records the widened type stored into a
    receiver var's field so a later read returns it.
  - **Algorithm — replace the member-target stub** (infer_expr.go:494-500, today
    `reportUnsupportedFeature("assignment to a member or index")`): implement
    `inferMemberAssign` per the sketch:
    - infer receiver + RHS
    - `widen` the RHS
    - constrain `recv <: RefType{Mut: true, Lt: nil, Inner: {field: widen(rhs),
      Inexact: true}}`
    - record `written[recvID,field]`
    - return the stored value

    Keep the `*ast.IndexExpr` sub-case as `reportUnsupportedFeature` (array
    writes need Array types — note it for M7).
  - **Algorithm — read-after-write:** `inferMember` (infer_expr.go:687) consults
    `written` first: a read of a just-written field returns the recorded
    concrete type instead of a fresh var, so `obj.x = 5; obj.x` is `number`.
  - **Algorithm — mut-record merge:** extend B1's `mergeObjects` fold to the
    `RefType{Mut: true, Inner: RecordType}` case so multiple writes merge into
    one mutable record (`mut {x} & mut {y}` ⇒ `mut {x, y}`) — the spike's
    `mergeObjects` handled `Mut`-wrapped objects the same way.
  - **Accept:**
    - `fn foo(obj) { obj.x = 5; obj.y = 10 }` ⇒ `(obj: mut {x: number, y: number})
      -> unit`
    - `fn foo(obj) { obj.x = 5; return obj.x }` ⇒ `... -> number`

### Phase D — Lifetimes (second sort)

- **D1 — Lifetime sort + probe extension** (~230).
  - **Files:** new `soltype/lifetime.go`, `solver/context.go`,
    `solver/constrain.go`, `solver/probe.go`, `soltype/print.go`.
  - **Structures:**
    - `LifetimeVar{ID int; LowerBounds, UpperBounds []Lifetime}`, implementing
      `isLifetime()`
    - `StaticLifetime{}` (top of the outlives lattice), implementing
      `isLifetime()`
    - `Context` grows `lifetimeCounter int` (its own comment at context.go:5
      names this deferred field) and a `freshLifetime()` minter
  - **Algorithm — `constrainLt`** (ported from `simplesub/lifetime.go:61`):
    mirrors `constrain` over the outlives lattice — a var on the left gains an
    upper bound, on the right a lower bound, var-to-var records both;
    `'static` is top (`X <: 'static` always holds); a `(lhs, rhs)`-keyed
    seen-set terminates cycles. Bound appends go **only** through new
    `addLowerLtBound`/`addUpperLtBound` helpers that journal before appending —
    the same discipline as `addLowerBound`/`addUpperBound` (context.go:36).
  - **Algorithm — probe extension (the careful part):**
    - extend the probe with a parallel concrete journal for
      `*soltype.LifetimeVar` — a second `probeEntry` kind with the same
      length-snapshot + truncate-on-discard discipline as the
      `*soltype.TypeVarType` path; the probe stays concrete, so soltype keeps no
      exported speculation-only truncate verb
    - add the lifetime sort to `Probe.touched`/`record` accordingly
    - verify a discarded overload trial that touched a lifetime var rolls it back
      (probe_test.go pattern)
  - **Structures — printer:** lifetime printing in `print.go`:
    - a named param lifetime renders `'a`
    - `'static` renders `'static`
    - the `RefType` print arm (added in C1 with `Lt` always nil) now renders
      `mut 'a T` / `'a T` when `Lt != nil`
  - **Accept:**
    - `constrainLt` unit tests (outlives, transitivity, cycle termination,
      `'static` absorption)
    - a probe discard truncates an appended lifetime bound

- **D2 — Activate the rule + borrow origination** (~160).
  - **Files:** `solver/constrain.go` (un-inert the C2 lifetime step),
    `solver/infer_expr.go` (`attachParamLifetimes`), `solver/infer_expr.go`
    (`inferMemberAssign` lifetime flip).
  - **Structures:** add `paramLifetimes set.Set[int]` to the checker (the
    nameable-lifetime set, ported from `simplesub`).
  - **Algorithm — activate the `RefType` rule's step 3** (C2's inert `switch`):
    - covariant lifetime when both present (`constrainLt(r.Lt, l.Lt)`)
    - owned source into a borrow slot ok
    - borrow into an owned slot ⇒ `BorrowEscapeError` (now live)

    The `RefType <: bare` escape guard (`l.Lt != nil`) also goes live.
  - **Algorithm — `attachParamLifetimes`** (per the sketch): a `RefType`-typed
    param with `Lt == nil` gets a fresh lifetime var, recorded in
    `paramLifetimes`. Called when binding function params.
  - **Algorithm — flip C3's write requirement:** `inferMemberAssign`'s
    `Lt: nil` becomes `Lt: c.freshLifetime()` so a mut-borrow receiver of any
    lifetime is accepted (the fresh var imposes no lifetime obligation). Re-run
    C3's acceptance — owned receivers still check, borrowed receivers now check.
  - **Accept:**
    - `IdentityRefReturn` (`fn (p: mut {x: number}) { return p }`) ⇒ `fn <'a>(p:
      mut 'a {x: number}) -> mut 'a {x: number}`
    - `FreshObjectReturn` (returning a fresh `mut {x: 1}`) carries no lifetime

- **D3 — Multi-source lifetime joins + escape-to-`'static`** (~150).
  - **Files:** `solver/infer_stmt.go` / wherever the M3 return-point join lives,
    `solver/infer_expr.go` (escape sites).
  - **Algorithm — joins:** when the M3 return-point join (or an `if`/`match`
    branch join) unifies two borrowed records with distinct lifetimes, mint a
    fresh **join** lifetime var and `constrainLt` each source lifetime into it
    (so it coalesces to `'a | 'b`). The join var is *not* a param lifetime —
    only its reachable param-lifetime members are named (the spike's
    `paramLifetimes`-vs-join distinction).
  - **Algorithm — escape:** a value flowing into module/static storage (a
    top-level binding, a global write) constrains its lifetime `<: 'static`
    (`constrainLt(lt, &StaticLifetime{})`), which coalesces to `'static`.
  - **Accept:**
    - `ConditionalUnionReturn` (return one of two mut borrows) ⇒ `mut ('a | 'b)
      {x: number}`
    - `EscapingRefIntoStatic` ⇒ `mut 'static`

- **D4 — Display-time lifetime coalescing + elision** (~200).
  - **Files:** new lifetime-occurrence logic alongside `solver/simplify.go` /
    `solver/coalesce.go`, `soltype/print.go` (the `<'a>` quantifier).
  - **Structures:**
    - an `analyzeLts` occurrence pass (ported from `simplesub/lifetime.go`)
      recording, per lifetime var, the polarities it occurs in — built to mirror
      `simplify.go`'s `symOccVisitor` but over the lifetime sort
    - a `ltKeep`/naming map keyed by lifetime-var ID, threaded into the scheme
      coalescer the way `schemeSimplification` is threaded today (coalesce.go:120)
  - **Algorithm — naming:** only param-originated lifetimes are named (`'a`,
    `'b`, … via a base-26 `alphaName`, per the spike); a join var renders as the
    union of the param lifetimes it reaches; `'static` absorbs. The `<'a>`
    quantifier joins the existing `<T0, …>` prefix in `PrintAsSchemeWith`.
  - **Algorithm — elision (the subtle branch):** a param lifetime occurring in
    only one polarity (and not forced to `'static`) connects nothing and is
    elided — the lifetime-sort analogue of single-polarity elimination. The
    elision branches on `Mut`: a **mutable** borrow with an elided lifetime
    becomes `RefType{Mut: true, Lt: nil}` (owned-mutable, well-formed); an
    **immutable** borrow with an elided lifetime **must drop the `RefType`
    wrapper entirely** (returning bare `Inner`), because `RefType{false, nil}` is
    the forbidden degenerate cell `NewRef` rejects. The coalescer branches on
    `Mut` at the elision site; the `NewRef` invariant (plus a printer assertion)
    is the backstop.
  - **Algorithm — exactness passthrough:** the `RefType` coalescing arm carries
    the inner carrier's `Inexact` flag through untouched (spec §7.11 — `mut`/
    lifetime axes are orthogonal to exactness).
  - **Accept:**
    - property-level lifetimes (`{p: mut 'a {…}}`) and tuple-per-slot lifetimes
      render
    - a connect-nothing param lifetime elides (mut ⇒ owned-mut, immut ⇒ wrapper
      dropped)
    - read-after-write field collapse with a lifetime present

### Phase E — Destructuring + `match`

- **E1 — Structural patterns** (~220).
  - **Files:** `soltype/type.go` (the `Pat` concretes), `soltype/print.go`
    (`paramName`/pattern rendering), `solver/infer_decl.go` (`varName` →
    general pattern binding), `solver/infer_expr.go` (param patterns), plus a
    pattern-typing helper.
  - **Structures:**
    - add `TuplePat{Elems []Pat}`, `RecordPat{Fields []*RecordPatField}`, and
      literal patterns as `Pat` concretes (soltype's `Pat` interface was reserved
      for exactly this — type.go's `Pat` comment)
    - `print.go`'s `paramName` (print.go:278) gains arms for the new pattern
      shapes
    - `varName` (infer_decl.go:131, today IdentPat-only) generalizes to return the
      full set of bound names
  - **Algorithm — pattern typing:** a pattern dispatches through the
    **member-lookup constraint path**, not subtyping: a `RecordPat{x, y}`
    against a scrutinee `s` emits `constrain(s <: {x: βx, y: βy, Inexact:
    true})` (the same inexact requirement `inferMember` uses) and binds `x`/`y`
    to `βx`/`βy`; a `TuplePat[a, b]` emits `constrain(s <: [αa, αb])` (exact
    length for an exact tuple pattern). A missing field / wrong arity surfaces
    the existing `MissingPropertyError`/`TupleLengthMismatchError`. Patterns
    peel any `RefType` via `carrierOf` before matching (works on owned values
    before Phase D; borrowed scrutinees once D lands). Used uniformly by `val`
    destructuring, function-param destructuring, and (E2) `match` arms.
  - **Accept:**
    - `val {x, y} = p` binds `x`/`y` at their field types and rejects a missing
      field
    - `val [a, b] = t` binds per slot and rejects wrong arity
    - a destructured param `fn (({x, y})) { … }` types the same way

- **E2 — The `match` expression** (~220).
  - **Files:** `solver/infer_expr.go` (the `match` walk), reusing the M3
    return-point-join machinery and E1's pattern typing.
  - **Algorithm — arm typing:** infer the scrutinee once; for each arm, type its
    pattern against the scrutinee (E1) in a child scope carrying the arm's
    bindings, then infer the arm body; join the arm body types via the M3
    return-point join (the same join D3 hooks lifetimes into).
  - **Algorithm — exhaustiveness from exactness:** an **exact** record/tuple
    scrutinee whose arms cover its shape needs no catch-all; an **inexact**
    scrutinee requires a catch-all arm (a missing one is a typed error).
    Constructor/enum patterns and enum-exhaustive `match` are M5; union-scrutinee
    exhaustiveness is M6 — E2 lays the form and the structural-pattern path they
    both extend.
  - **Accept:**
    - a `match` over structural patterns binds and type-checks each arm
    - an exact-record scrutinee with a complete pattern set needs no catch-all, an
      inexact one does (full error on the missing catch-all)

### Phase F — Namespace member lookup (parallel track)

- **F1 — `resolvePath` + namespace access** (~180).
  - **Files:** `solver/infer_expr.go` (new `resolvePath` + `pathResult`),
    `solver/scope.go` (optional thin `LookupValue`/`LookupNamespace` wrappers
    over the `Namespace` maps — **new** if added; today `Namespace` is
    fields-only, scope.go:61), `solver/errors.go`.
  - **Structures:**
    - `pathResult` sum (`Value | Namespace`)
    - new error `UnknownNamespaceMemberError`
    - new error `DynamicNamespaceIndexError`
  - **Algorithm — `resolvePath`** (per the sketch): resolve an ident/member/
    index chain to `Value | Namespace`. Namespace lookup is a **direct,
    non-lexical** read of `ns.Values[name]` / `ns.Nested[name]` (no parent walk,
    unlike `Scope.GetValue`/`GetType`/`GetNamespace` which do walk parents). The
    object/index position tolerates a namespace; every other value position
    rejects one — so `NamespaceUsedAsValueError` moves **off** `inferIdent`
    (infer_expr.go:50 note) to the value-position consumer, firing once for both
    `f(Foo)` and partial chains `f(A.B)`. Index keys into a namespace must be
    statically constant strings (`Foo["weird-name"]`); a dynamic `Foo[k]` is a
    `DynamicNamespaceIndexError`.
  - **Dependency:** only M2's `Namespace` structure — lands any time, parallel
    to every other phase.
  - **Accept:**
    - `Foo.bar` resolves to the member's type
    - `Foo["weird-name"]` resolves a constant-keyed member
    - `f(Foo)` and `f(A.B)` reject (`NamespaceUsedAsValueError`)
    - `Foo[k]` rejects (`DynamicNamespaceIndexError`)

### Phase G — Mutability-transition checking (port)

- **G1 — Port liveness + predicates + wiring** (~220).
  - **Files:** new `solver/transitions.go`, `solver/context.go`, reusing
    `internal/liveness/` and `internal/checker/liveness_prepass.go` verbatim.
  - **Structures:** `solver.Context` (or the checker carrier) gains `Liveness
    *liveness.Liveness` and `Aliases *liveness.AliasTracker`, populated by the
    existing prepass (operates on the AST, not the checker's types).
  - **Algorithm — two predicate ports:** reimplement `isValueType(t
    soltype.Type) bool` and `isMutableType(t soltype.Type) bool`
    (`check_transitions.go:189-217`) over `soltype` — `isMutableType` becomes
    `if r, ok := t.(*soltype.RefType); ok { return r.Mut }; return false`.
    `checkMutabilityTransition`'s Rule 1 / Rule 2 / Rule 3 logic is **unchanged**
    (it talks only to `liveness.Liveness`/`liveness.AliasTracker`).
  - **Accept:** the old checker's transition-checking cases reproduced as
    intended-form tests pass through the ported checker.

- **G2 — Collapse the static-alias escape hatches** (~120).
  - **Files:** `solver/transitions.go`.
  - **Algorithm:**
    - drop the `HasStaticMutAlias` / `HasStaticImmAlias` bits
    - where the old code consulted them, query the lifetime sort directly — a
      value whose lifetime is constrained `<: 'static` (D3's escape output) is the
      first-class signal the bits approximated

    This is the one genuinely new logic in the port; it depends on D3.
  - **Accept:** the static-escape transition cases pass via lifetime queries
    rather than the dropped bits.

### Dependency graph

```
A1 → A2
A1 → A3 ───────────────────┐  (A3's mut/lifetime arms un-gated by C1)
A1 → B1 → B2               │  (annotation-side acceptance tests)
     B1 → B3               │
     B1, B3 ───────────┐   │  (C3 reuses B1's mergeObjects fold + B3's widen)
A1 → C1 → C2(GATE) →  C3 → D1 → D2 → D3 → D4 → G1 → G2
A1 → E1 → E2   (independent of C/D; E1's RefType peel via carrierOf needs C1)
F1             (independent; any time — only M2's Namespace)
```

Critical path to the gate: **A1 → C1 → C2** — three PRs. B, E, F are parallel
tracks off A1; nothing downstream of the gate starts before C2 clears. B1's
`mergeObjects` fold is reused by C3 (mut-record merge) and the `widen` helper
authored in B3 is reused by C3, so land B before C3 even though B is otherwise
independent of the gate. Total ≈ 3.0k non-test LoC across 17 PRs, with the
single highest-risk change (C2) isolated to ~180 reviewable lines.

## Testing strategy

Per the shipped test conventions (e.g.
[infer_obj_test.go](../../internal/solver/infer_obj_test.go),
[infer_func_test.go](../../internal/solver/infer_func_test.go)): table-driven
tests keyed by name over real parsed source where the walk supports it,
asserting rendered types (`render`/`PrintAsScheme` output) and **full** error
messages via the typed `SolverError`s — authored against intended semantics,
not old-checker output. Blame-span assertions follow the M2.5 pattern
(`blame_test.go`). Inline snapshots for large trees (nested borrowed records
with per-field lifetimes). Each PR carries the acceptance set named above; the
union is the milestone's M4 acceptance. No fixture harness yet (M8).

## Risks

- **The gate (C2)** remains the dominant risk, front-loaded with an explicit
  stop-and-reassess. The spike proved the algorithm; the residual risk is the
  production encoding (visitor, journal-gated bounds, blame spans).
- **Probe × lifetimes (D1).** Extending the probe to a second bounded sort touches
  M3's speculation infrastructure used by overload resolution. The "appends only
  through journaling helpers" invariant must hold for the new sort from day one, or
  a discarded overload trial could leak lifetime bounds. Keeping the probe concrete
  — a parallel `LifetimeVar` journal rather than an abstraction over both sorts —
  avoids exporting a speculation-only truncate verb on soltype's public surface.
- **Elision branching on `Mut` (D4)**: the immutable-elide case **must** drop
  the wrapper or it reconstructs the forbidden degenerate cell; `NewRef`'s
  invariant (plus a printer assertion) is the backstop.
- **`match` over borrowed scrutinees**: E2's structural exhaustiveness is
  defined on the carrier; the `RefType` peel must not consult `Mut`/`Lt` for
  completeness. Cheap to test once D-phase types exist; called out so E2
  doesn't silently bake in owned-only assumptions.

## Open questions

- **Deferred overload resolution (#723).** M4's record types make the
  documented first-match fallback *observable* on object-typed arguments
  (today it only over-narrows function-typed ones). The real fix is scoped
  M4/M5; if object-arg overload tests start failing on arm choice rather than
  typing, that is #723, not a regression in these PRs.
  - **Decision (deferred to M5).** Keep resolution out of M4's scope. The
    fallback to declaration order fires because `moreSpecific`/`structuralSubtype`
    (overload.go) ranks record-shaped args as a tie. The principled fix is to
    rank record args by field-set subsumption and exactness — a record covering
    more required fields, or the exact one, dominates, the object analogue of the
    existing arity/exactness ranking for functions. Land that in M5, where
    classes first produce multi-arm object overloads. For M4, pin the observable
    cases with a `#723`-tagged test group so any later arm-choice change is
    intentional rather than a silent regression.
- **Rest-param element checking** (`FuncParam.Rest`'s "needs Array types and is
  M4" note, #677 §4.2.3) — Array types are not otherwise in M4's milestone scope.
  - **Decision (deferred to M7).** Keep arity-only in M4 and move element checking
    to wherever Array lands (M7 TypeRef ingestion), since the element type to
    check against only exists once `Array<T>` resolves. Update the `FuncParam.Rest`
    comment in type.go to read "M7," not "M4," so the gap is recorded as deferred
    rather than in-scope-but-skipped. Trailing args stay unchecked until then — a
    bounded, documented hole, acceptable because rest params are rare in M4
    source.
- **Function-arm Variation-B gap** (constrain.go's KNOWN GAP): unchecked extra
  positions against an inexact callback need the `_ <: unknown` (⊤) rule slated
  for M6. A3 does **not** add function annotations, so the gap stays
  unreachable through M4 — but if function annotations slip in, the ⊤ rule must
  come with them.
  - **Decision (deferred to M6).** Leave the ⊤ rule for M6 — it touches every
    constrain arm and isn't worth M4's risk. Make the gap fail loud rather than
    stay merely unreachable: have the inexact-callback extra-position branch emit
    an "unsupported until M6" error (via `reportUnsupportedFeature`) instead of
    silently skipping the check, and add a test that a function annotation
    carrying an inexact callback is rejected. That proves the path stays closed
    through M4; M6 removes the guard when the ⊤ rule lands.
- **`match` over borrowed scrutinees**: E2's structural exhaustiveness is
  defined on the carrier; the `RefType` peel must not consult `Mut`/`Lt` for
  completeness. Cheap to test once D-phase types exist; called out so E2
  doesn't silently bake in owned-only assumptions.
