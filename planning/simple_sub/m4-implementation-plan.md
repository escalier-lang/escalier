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
// bound fields inline on discard (probe.go's probeEntry.v is *TypeVarType, not an
// interface). The 02-design-notes "probe-API sketches" proposed a `Bounded`
// interface with UNEXPORTED methods — but probe.go:45-52 records that this was
// tried and REJECTED: Go won't let `solver` attach the unexported boundLengths/
// truncateBounds to a `soltype` type, and an interface in `solver` over unexported
// methods is unsatisfiable by a `soltype` type across the package boundary. M3 had
// one bounded sort, so it kept the concrete entry.
//
// M4 adds a SECOND bounded sort (LifetimeVar), which is the trigger probe.go's
// comment names ("if a second bounded type appears, reintroduce the interface with
// EXPORTED methods on the soltype types"). So the correct M4 move is the EXPORTED
// form:
//
//   // package soltype — exported, so cross-package satisfaction works:
//   func (v *TypeVarType) BoundLengths() (int, int)
//   func (v *TypeVarType) TruncateBounds(lower, upper int)
//   func (v *LifetimeVar) BoundLengths() (int, int)
//   func (v *LifetimeVar) TruncateBounds(lower, upper int)
//
//   // package solver:
//   type Bounded interface {
//       BoundLengths() (lower, upper int)
//       TruncateBounds(lower, upper int)
//   }
//   // probeEntry.v becomes Bounded; *TypeVarType and *LifetimeVar both satisfy it.
//
// COST (eyes-open): exporting TruncateBounds publishes a speculation-only "rewind
// my bounds to a checkpoint" verb on soltype's public surface, callable from any
// importer with no probe checkpoint — a silent-corruption footgun (a stray
// `v.TruncateBounds(0, 0)` drops solved bounds, surfacing later as a wrong
// coalesced type, not a panic). Mitigation: a doc comment on the exported methods
// ("speculation-internal; call only through Probe"). The alternative — duplicating
// the inline truncate per sort, no interface — keeps the surface clean at the cost
// of two near-identical discard paths; pick the interface only once the second
// sort makes the duplication real.
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

17 PRs across 7 phases, each ~120–250 non-test LoC, independently mergeable and
green, each with table-driven tests asserting rendered types and **full** error
messages. The gate (C2) sits 3rd on the critical path.

### Phase A — Records & tuples completion (the M2 leftovers)

- **A1 — `Inexact` flag + record exactness rules + the selection split**
  (~200). Add `Inexact` to `RecordType`; rewrite the record constrain arm per
  the sketch (one-way rule; new `InexactIntoExactError`/`ExtraPropertyError`);
  flip `inferMember`'s requirement to `Inexact: true`; printer `...`. The M2
  arm's "this is NOT the final semantics" comment is retired here.
- **A2 — Tuple inexactness + tuple spread** (~150). `TupleType.Inexact`; the
  `longer <: shorter`-vs-inexact arm (narrowing `TupleLengthMismatchError`'s
  firing conditions per its own M4 note); tuple-typed spread in tuple literals
  (the `infer_obj_test` "Tuple/array spread is M4" stub); the computed/numeric
  object-key story (static-string keys resolve; dynamic keys get a typed
  error, full support rides M9 index types).
- **A3 — Record/tuple type annotations** (~180). Extend `resolveTypeAnn`:
  object/tuple annotations incl. trailing `...`; `mut`/lifetime annotation
  forms parse to `RefType` (consumed from C1 on — until then a `mut` annotation
  reports unsupported, preserving today's behavior). Unblocks every
  annotation-side acceptance test.

### Phase B — Usage-based inference refinement

- **B1 — Negative-position record merging + Policy-A closing** (~180). The
  spike's `mergeObjects` brought to production coalesce: record upper bounds
  in an intersection merge into one record (`{a: β} & {b: γ}` ⇒ `{a: β, b:
  γ}`); the usage-collected shape **closes to exact** at display time (Policy
  A, spec §8.1). Mut-record merging is added by C3.
- **B2 — The `open` parameter marker** (~120). Parser marker + keep-inexact
  flag on the closed shape. Spelling gated; semantics are what M4 proves.
- **B3 — `var` literal widening** (~150). **Authors the `widen` helper**
  (`func widen(t soltype.Type) soltype.Type`, ported from the spike — not yet in
  the production solver; C3's field-write reuses it). The principled usage-based
  form from the milestone: a `var`'s type is informed by all assignment sites
  via `widen` + the same coalescing; `val` stays a literal singleton. Retires
  PR8's documented `var a = 5; a = 6` failure.

### Phase C — Borrows & mutability (**the gate**)

- **C1 — `RefType` plumbing** (~220). The node, `RefInner` (PromiseType/
  FuncType/prims excluded), `borrowableType`, `NewRef`, `unwrapRef`/
  `carrierOf`; visitor `Accept` (read-view polarity, cache-shared);
  `LevelOf`/`equalType`/`Print` arms. **No constrain rule yet** — trivially
  green.
- **C2 — The `RefType` constrain rule** (~180). **← THE GATE.** The single rule
  per the sketch, lifetime steps inert; the two cross-cases (struct-literal
  construction in `bare <: RefType` — *not* `NewRef`). Acceptance: `mut {x,y}
  <: mut {x}` **fails** while `{x,y} <: {x}` width-succeeds (inexact);
  mut-decay allowed, reverse rejected; full messages. **Stop and reassess
  before any later phase if this does not encode cleanly.**
- **C3 — Field-write inference + read-after-write** (~200). Replace
  `inferAssign`'s member-branch stub per the sketch (`Lt: nil`); reuse B3's
  `widen` on write; multi-write merge (mut-record case of B1's merge); the
  `written`
  read-after-write map on the checker. Acceptance: `fn foo(obj) { obj.x = 5;
  obj.y = 10 }` ⇒ `(obj: mut {x: number, y: number}) -> unit`; write-then-read
  ⇒ `number`.

### Phase D — Lifetimes (second sort)

- **D1 — Lifetime sort + probe extension** (~220). `LifetimeVar`/
  `StaticLifetime`; `constrainLt` with journal-gated appends
  (`addLowerLtBound`/`addUpperLtBound`); extend the probe to the second sort by
  reintroducing the `Bounded` interface with **exported** `BoundLengths`/
  `TruncateBounds` on both soltype types (the path `probe.go:45-52` prescribes
  for "a second bounded type" — *not* the rejected unexported form), so both
  sorts roll back through one journal; lifetime printing.
- **D2 — Activate the rule + borrow origination** (~160). Turn on step 3 and
  the escape guards; `attachParamLifetimes`; **flip C3's write-requirement
  lifetime from `nil` to a fresh var** and re-run C3's acceptance (borrowed
  receivers now accepted). Acceptance: `IdentityRefReturn` ⇒ `fn <'a>(p: mut
  'a {x: number}) -> mut 'a {x: number}`; `FreshObjectReturn` lifetime-free.
- **D3 — Joins + escape** (~140). Multi-source returns union lifetimes via a
  fresh join var (riding the M3 return-point join); escape to module/static
  storage ⇒ `constrainLt(lt, 'static)`. Acceptance: `ConditionalUnionReturn` ⇒
  `mut ('a | 'b) {x: number}`; `EscapingRefIntoStatic` ⇒ `mut 'static`.
- **D4 — Display-time coalescing + elision** (~200). `analyzeLts` occurrence
  pass over scheme rendering; param-lifetime naming + `fn <'a>(…)` quantifier;
  elision with the **mut elide-in-place vs immutable drop-the-wrapper**
  branch; `RefType` passes the inner's `Inexact` through untouched (spec
  §7.11). Property-level and tuple-per-slot lifetime acceptance.

### Phase E — Destructuring + `match`

- **E1 — Structural patterns** (~220). `TuplePat`/`RecordPat`/literal-pattern
  `Pat` concretes (soltype's `Pat` reserved exactly this growth); binding
  destructuring in `val` and params, dispatched through the member-lookup
  constraint path (`constrain(scrutinee <: {x: β, …})`), not subtyping —
  missing field / wrong arity reject. Patterns peel `RefType` via `carrierOf`
  (works on owned values before Phase D completes).
- **E2 — The `match` expression** (~220). The expression form over structural
  patterns; arm binding + per-arm typing joined via the M3 return-point-join
  machinery; exhaustiveness from exactness — an exact record/tuple scrutinee
  with a complete pattern set needs no catch-all, an inexact one does.
  Constructor/enum patterns are M5; union scrutinee exhaustiveness is M6.

### Phase F — Namespace member lookup (parallel track)

- **F1 — `resolvePath` + namespace access** (~180). Per the sketch: `Foo.bar`,
  constant-keyed `Foo["x"]`; `NamespaceUsedAsValueError` moves off
  `inferIdent` to the value-position consumer (its existing M4 note);
  `UnknownNamespaceMemberError`, `DynamicNamespaceIndexError`. Depends only on
  M2's `Namespace` — can land any time, parallel to every other phase.

### Phase G — Mutability-transition checking (port)

- **G1 — Port liveness + predicates + wiring** (~220). `internal/liveness/`
  verbatim; `isValueType`/`isMutableType` over `soltype`;
  `checkMutabilityTransition` rule logic unchanged; `Liveness`/`Aliases` on
  the solver context; old transition cases as intended-form tests.
- **G2 — Collapse the static-alias escape hatches** (~120). Drop
  `HasStaticMutAlias`/`HasStaticImmAlias`; query the lifetime sort
  (`lt <: 'static`) directly. Depends on D3 — the one genuinely new logic in
  the port.

### Dependency graph

```
A1 → A2
A1 → A3 ──────────────┐
A1 → B1 → B2          │ (annotation-side acceptance tests)
     B1 → B3          │
A1 → C1 → C2(GATE) → C3 → D1 → D2 → D3 → D4 → G1 → G2
A1 → E1 → E2   (independent of C/D; E1's RefType peel activates once C1 lands)
F1             (independent; any time)
```

Critical path to the gate: **A1 → C1 → C2** — three PRs. B, E, F are parallel
tracks off A1; nothing downstream of the gate starts before C2 clears. Total
≈ 3.0k non-test LoC across 17 PRs, with the single highest-risk change (C2)
isolated to ~180 reviewable lines.

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

## Risks / open questions

- **The gate (C2)** remains the dominant risk, front-loaded with an explicit
  stop-and-reassess. The spike proved the algorithm; the residual risk is the
  production encoding (visitor, journal-gated bounds, blame spans).
- **Probe × lifetimes (D1).** Reintroducing the `Bounded` interface touches M3's
  speculation infrastructure used by overload resolution. Two constraints:
  (a) it must use **exported** methods on the soltype types — the unexported
  form is unsatisfiable across the package boundary, which is why `probe.go`
  shipped the concrete-type journal instead (probe.go:45-52); (b) the "appends
  only through journaling helpers" invariant must hold for the new sort from day
  one, or a discarded overload trial could leak lifetime bounds. The exported
  `TruncateBounds` is a public-surface footgun — document it speculation-internal.
- **Elision branching on `Mut` (D4)**: the immutable-elide case **must** drop
  the wrapper or it reconstructs the forbidden degenerate cell; `NewRef`'s
  invariant (plus a printer assertion) is the backstop.
- **Deferred overload resolution (#723).** M4's record types make the
  documented first-match fallback *observable* on object-typed arguments
  (today it only over-narrows function-typed ones). The real fix is scoped
  M4/M5; if object-arg overload tests start failing on arm choice rather than
  typing, that is #723, not a regression in these PRs.
- **Rest-param element checking** (`FuncParam.Rest`'s "needs Array types and is
  M4" note, #677 §4.2.3) — **needs an owner decision**: Array types are not
  otherwise in M4's milestone scope. Recommendation: keep arity-only in M4 and
  move element checking to wherever Array lands (M7 TypeRef ingestion),
  updating the type.go comment.
- **Function-arm Variation-B gap** (constrain.go's KNOWN GAP): unchecked extra
  positions against an inexact callback need the `_ <: unknown` (⊤) rule slated
  for M6. A3 does **not** add function annotations, so the gap stays
  unreachable through M4 — but if function annotations slip in, the ⊤ rule must
  come with them.
- **`match` over borrowed scrutinees**: E2's structural exhaustiveness is
  defined on the carrier; the `RefType` peel must not consult `Mut`/`Lt` for
  completeness. Cheap to test once D-phase types exist; called out so E2
  doesn't silently bake in owned-only assumptions.
