# M6 implementation plan — Unions / intersections

This plan covers **M6 — Unions / intersections** as listed in
[01-milestones.md](01-milestones.md). It is a PR-by-PR breakdown, modeled on the
[M4](m4-implementation-plan.md) and [M3](m3-implementation-plan.md) plans: it
records what prior milestones shipped, the genuine delta M6 adds, the sequencing,
and the per-PR design with file references.

M6 turns `UnionType` / `IntersectionType` from **coalesced-output-only** nodes
into first-class lattice members: legal `constrain` inputs, writable annotations,
carriers of an exactness flag, and the subjects of a normalization pass. It also
discharges two gaps M4 deliberately left open — the `_ <: unknown` (⊤) rule and
the permissive mut-borrow join.

## Ordering note: M6 lands before M5

The earlier milestone discussion settled that **M6 is done before M5**. Nothing
in M6 depends on nominal classes or enums. The one cross-link runs the other way:
M5's per-parameter variance acceptance examples are written with union
annotations such as `Box<number> <: Box<number | string>`, and union *annotation
input* is an M6 deliverable (PR2 below). Landing M6 first means those M5 tests
have real union machinery to lean on rather than a stub.

One consequence for the `match`-exhaustiveness story. The milestone frames
exhaustiveness as three legs — structural (M4), enum (M5), union (M6). With M5
not yet built, the **union leg lands here independently** and the enum leg
arrives later with classes. M6 extends the same `checkMatchExhaustive` path M4
built ([infer_expr.go](../../internal/solver/infer_expr.go) `checkMatchExhaustive`)
and does not touch enum patterns.

## What M1–M4 shipped (ground truth this plan builds on)

The representation and the output path already exist; M6 adds the *input* path
and the *rules*.

- **The nodes exist.** `soltype.UnionType{Types []Type}` and
  `soltype.IntersectionType{Types []Type}`
  ([soltype/type.go:343](../../internal/soltype/type.go)), plus the lattice
  bounds `NeverType` (⊥) and `UnknownType` (⊤)
  ([type.go:334](../../internal/soltype/type.go)) and the absorbing `ErrorType`
  sentinel ([type.go:356](../../internal/soltype/type.go)). **None carries an
  exactness flag** — that is M6's first addition for `UnionType`.
- **Coalescing already produces them.** `combine`
  ([coalesce.go:295](../../internal/solver/coalesce.go)) builds a `UnionType`
  from a positive variable's lower bounds and an `IntersectionType` from a
  negative variable's upper bounds; `emptyOf`
  ([coalesce.go:277](../../internal/solver/coalesce.go)) collapses empty bounds to
  `never` / `unknown`. So a multi-branch `if`/`match` already renders
  `number | string` as output ([infer_expr.go:330](../../internal/solver/infer_expr.go)
  `joinReturnPoints`).
- **The printer renders them, flag-free.** `printType`
  ([print.go:367](../../internal/soltype/print.go)) joins members with ` | ` /
  ` & `; precedence is `precUnion` / `precIntersection`
  ([print.go:29](../../internal/soltype/print.go)). No trailing `...` for an
  inexact union yet.
- **`equalType` compares members positionally.** The `UnionType` /
  `IntersectionType` arms call `equalTypeSlice`
  ([coalesce.go:571](../../internal/solver/coalesce.go),
  [coalesce.go:615](../../internal/solver/coalesce.go)), which is **order-
  sensitive** — the latent dedup gap M6 closes.
- **`constrain` has no union arm and only an overload-shaped intersection arm.**
  The `IntersectionType` *sub* case
  ([constrain.go:366](../../internal/solver/constrain.go)) is the scoped overload
  exception: `(A & B & …) <: C` trials each member under a probe in specificity
  order. There is **no `UnionType` arm at all**, and no general intersection
  rule. The structural `switch` ([constrain.go:150](../../internal/solver/constrain.go))
  falls through to `CannotConstrainError` when a side is a union.
- **`resolveTypeAnn` rejects union/intersection AND function annotations.**
  ([type_ann.go:21](../../internal/solver/type_ann.go)) has no `*ast.UnionTypeAnn`
  / `*ast.IntersectionTypeAnn` / `*ast.FuncTypeAnn` arm; all fall to
  `reportUnsupported`. The AST nodes exist
  ([ast/type_ann.go:381](../../internal/ast/type_ann.go),
  [ast/type_ann.go:438](../../internal/ast/type_ann.go)); `UnionTypeAnn` carries
  **no `Inexact` field**, while `FuncTypeAnn` already has `Params` / `Return` /
  `Inexact`. So M3's accept-set callback subtyping is only unit-tested on
  hand-built `FuncType` values, not in source.
- **The probe journal exists** ([probe.go](../../internal/solver/probe.go)):
  `newProbe` / `Commit` / `Discard`, with bound-append and Info/Prov rollback.
  The overload arm is its first consumer; M6's "exists" rules are the second.
- **The mut-borrow join errors on incompatible fields.** `joinBorrows`
  ([infer_expr.go:367](../../internal/solver/infer_expr.go)) pins shared fields
  invariant across borrows, so joining `mut {x: number}` and `mut {x: string}`
  reports `number <: string` / `string <: number` — asserted today by
  `TestInferIncompatibleBorrowJoinErrors`. M6 relaxes this to a union.
- **The function arm has a documented Variation-B gap.**
  ([constrain.go:195-202](../../internal/solver/constrain.go)): when `super` is an
  inexact function with fewer params than `sub`, soundness needs
  `unknown <: sub.Params[i].Type` at the extra positions, which needs the
  `_ <: unknown` rule M6 adds.

## What M6 adds (the delta)

1. **A normalization pass** with smart constructors `newUnion` / `newIntersection`:
   flatten, dedup, drop lattice identities (`never` from unions, `unknown` from
   intersections), elide `ErrorType` unless sole member, eliminate subsumed
   members, and impose a **canonical member order** so `equalType` stops caring
   about order.
2. **Lattice subtyping rules in `constrain`** — the "for all" rules eagerly, the
   "exists" rules under a probe with **variable deferral** to avoid speculative
   pinning — plus **union/intersection annotation input** in `resolveTypeAnn`.
3. **Monomorphic function-type annotation input** — `resolveTypeAnn` resolves
   `fn(params) -> ret` and the inexact `fn(..., ...) -> ret` into a
   `soltype.FuncType`, so function types are writable as params, returns,
   union/intersection members, and binding annotations. Generic, `throws`, and
   lifetime-param'd function annotations stay deferred.
4. **The `UnionType` exactness flag** end to end: AST/parser for `A | B | ...`,
   inferred-output exact-by-default, the one-way `exact <: inexact` rule, and the
   **union leg of `match` exhaustiveness**.
5. **The `_ <: unknown` (⊤) and `never <: _` (⊥) rules**, and the removal of the
   function-arm Variation-B guard — now testable end-to-end via an inexact
   function annotation.
6. **The permissive mut-borrow join**: degrade an incompatible reconcile to a
   read-until-narrowed union instead of an error.

## Scope — deliberately out of M6

- **Exactness *propagation through reduction*** — `keyof` of an exact object, the
  element-union of an exact tuple — is **M9**. M6 lands the flag and the
  match-exhaustiveness payoff, not operator-driven propagation
  ([01-milestones.md](01-milestones.md) M9, "Exactness propagation through
  operators").
- **Narrowing-gated writes through a union.** PR6 produces the union *output* and
  keeps its conflicting fields **read-only**, which is sound. Re-enabling a write
  after a runtime-type narrow such as `typeof r.x === "number"` needs narrowing
  infrastructure the solver does not have yet, and **no current milestone owns
  narrowing** (see Open questions). Per the settled decision that narrowing
  introduces a new binding ([02-design-notes.md](02-design-notes.md) §"Settled
  decisions"), the write would happen through that *new* narrowed binding, never
  through the original union-typed one — so the read-only-forever behavior of the
  original binding is exactly right, not a stopgap.
- **Generic / `throws` / lifetime-param'd function-type annotations.** M6 resolves
  only the monomorphic function annotation (PR3). A written type parameter `T`
  needs a type-name scope, which is M7 TypeRef work; `throws` is M9; lifetime
  params ride M6.5.
- **Enum/nominal `match` exhaustiveness** — M5.
- **General intersection-of-objects distribution / normalization beyond the
  lattice identities** — not required by any M6 acceptance case; intersections
  arise as annotation input and as the meet of usage requirements, both of which
  the identity-level normalization covers.

## Sequencing rationale

```
PR1 (representation + normalization)
 ├─► PR2 (constrain lattice rules + union/intersection annotation input)
 │     └─► PR4 (union exactness flag + match exhaustiveness leg)
 ├─► PR3 (monomorphic function-type annotations)
 │     └─► PR5 (⊤/⊥ rules + Variation-B close — now testable end-to-end)
 └─► PR6 (permissive mut-borrow join)
```

- **PR1 is first because every other PR mints or compares a union/intersection**,
  and they must all route through one normalizer or the canonical-order and
  identity guarantees leak. It is "born-with-the-type" infra in the same sense
  M2.5's `Prov` discipline was.
- **PR2 before PR4** because the exactness one-way rule is a refinement of the
  base lattice rules PR2 installs.
- **PR3 (function annotations) feeds PR5.** The `_ <: unknown` rule itself needs
  only PR1, but its reason for existing — the Variation-B check — is only
  *reachable* once an inexact function annotation can reach `constrain`, which is
  PR3. So PR3 lands first and PR5 carries the end-to-end test.
- **PR3 and PR6 depend only on PR1** (PR6 also reads PR2's union rule for the
  covariant-read story but does not require it to land first).

## Core types added or changed in M6

### `soltype.UnionType` gains `Inexact` (`soltype/type.go`)

```go
// A bare `A | B` is an exact (closed) union: its inhabitants are exactly A ∪ B.
// `A | B | ...` is inexact — at least these, with an unknown-typed tail. The flag
// is Inexact (not Exact) so the zero value is exact, matching the ObjectType /
// TupleType / FuncType convention (type.go:196, :205, :223).
type UnionType struct {
    Types   []Type
    Inexact bool
}
```

`IntersectionType` gains **no** flag: an intersection has no exact/inexact variant
— exactness is a property of its *result*, not the meet
([02-design-notes.md](02-design-notes.md) §"Exactness", the
"intersection has no exact/inexact variant" note).

**Visitor flag-carry fix (latent bug).** `UnionType.Accept`
([visitor.go:196](../../internal/soltype/visitor.go)) rebuilds
`&UnionType{Types: types}` and would **drop** the new flag on any rewrite — every
coalesce / extrude / widen pass runs through `Accept`. The PR1 change to
`&UnionType{Types: types, Inexact: cur.Inexact}` is mandatory, not cosmetic.

### `newUnion` / `newIntersection` smart constructors (`solver/`)

The single mint path for both nodes. Every current and new construction site
routes through them:

- `combine` ([coalesce.go:295](../../internal/solver/coalesce.go)) — coalesced
  output.
- `mergeObjectGroup` ([coalesce.go:447](../../internal/solver/coalesce.go)) — the
  per-property intersection of usage requirements.
- `resolveTypeAnn` (PR2) — annotation input.
- the permissive `joinBorrows` (PR6) — the borrow union.

### A total order `compareType` (`solver/`)

The canonical-ordering primitive PR1 needs. It must be a deterministic total
order consistent with `equalType`: two `equalType`-equal types compare equal.
There is no cheap key as there is for the lifetime sort (`LifetimeVar.ID`), so it
ranks by a kind tag first, then tie-breaks structurally. See PR1.

---

## PR breakdown

### PR1 — Union/intersection normalization + the `Inexact` representation

The representational foundation. No new subtyping yet; this PR makes the nodes
well-formed, canonical, and flag-carrying so PR2–PR6 build on a settled shape.

**Add the flag and fix the visitor.**

- `UnionType.Inexact` ([type.go:343](../../internal/soltype/type.go)).
- `UnionType.Accept` carries `Inexact` through a rewrite
  ([visitor.go:187](../../internal/soltype/visitor.go)).
- `equalType`'s union arm compares `Inexact` before members
  ([coalesce.go:571](../../internal/solver/coalesce.go)), mirroring the
  Object/Tuple/Func arms' `Inexact` discriminator.
- `printType` renders an inexact union with a trailing `...` entry —
  `A | B | ...` — so the flag round-trips to surface syntax, mirroring the inexact
  function/tuple rendering ([print.go:367](../../internal/soltype/print.go)).

**The smart constructors `newUnion(parts, inexact)` / `newIntersection(parts)`.**
Each runs the normalization the milestone enumerates, in one pass:

1. **Flatten** nested same-kind members: a `UnionType` inside a union is spliced
   in. An inexact member makes the result inexact.
2. **Lattice identities.** Drop `never` (⊥) from a union, `unknown` (⊤) from an
   intersection ([01-milestones.md](01-milestones.md) "lattice identities").
3. **`ErrorType` elision.** Drop `ErrorType` from **both** a union and an
   intersection unless it is the sole member — it is the join *and* meet identity,
   the former-level reflection of its absorbing behavior in `constrain`
   ([constrain.go:141](../../internal/solver/constrain.go)). Today `ErrorType` is
   short-circuited out of every bound list so it never reaches `combine`; once
   PR2 lets an annotation build a former directly, this keeps the invariant true.
4. **Dedup** structurally-equal members via `equalType`
   ([coalesce.go:492](../../internal/solver/coalesce.go)) — the existing `dedup`
   helper ([coalesce.go:470](../../internal/solver/coalesce.go)) generalizes here.
5. **Subsumed-member elimination.** Drop a union member that is a subtype of
   another union member, and an intersection member that is a supertype of
   another. Reuse `constrain` under a discard-only probe for the subtype test, so
   the check mutates no bounds (a no-error trial means subsumption holds). Keep
   this gated to concrete members to avoid trialling against inference variables
   mid-walk.
6. **Canonical order** via `compareType`, so member order is construction-order-
   independent.
7. **Collapse**: an empty union ⇒ `never`, an empty intersection ⇒ `unknown`, a
   single member ⇒ that member directly.

**`compareType`.** A total order: rank by a concrete-kind tag, then tie-break
within a kind — `TypeVarType` by `ID`, `PrimType` by `Prim`, `LitType` by value,
structural nodes by arity then recursively over children. Equal-by-`equalType`
types must compare equal, so the tie-break must bottom out deterministically. A
pragmatic fallback for an otherwise-tied pair is the rendered `Print` string;
note that this couples ordering to the printer, so prefer the structural
comparison and reserve the string key for genuine ties.

**`equalType` stays positional.** With canonical order imposed at construction,
`equalTypeSlice` ([coalesce.go:615](../../internal/solver/coalesce.go)) is already
correct — two unions over the same members now hold them in the same order. This
is the milestone's chosen route: canonicalize at construction rather than make
equality set-based.

**Route `combine` and `mergeObjectGroup` through the constructors.** `combine`
([coalesce.go:302](../../internal/solver/coalesce.go)) currently builds
`&soltype.UnionType{Types: parts}` / `&soltype.IntersectionType{Types: parts}`
raw; `mergeObjectGroup` ([coalesce.go:446](../../internal/solver/coalesce.go))
builds a raw `IntersectionType` for a shared property. Both become `newUnion` /
`newIntersection` calls. This is observable: previously-unnormalized coalesced
output now dedups and orders canonically, so some rendered snapshots tighten —
update them with `UPDATE_SNAPS=true`.

**Tests.** Table-driven over `newUnion` / `newIntersection`: flatten, dedup,
`never`/`unknown` drop, `ErrorType` elision (and sole-member retention),
subsumption (`number | 1` ⇒ `number`; `{x} & {x, y}` ⇒ `{x, y}` on the
intersection side per the meet), canonical order (`string | number` and
`number | string` render identically and `equalType`-match), single-member and
empty collapse. Printer round-trip for an inexact union.

---

### PR2 — Lattice subtyping rules in `constrain` + annotation input

The heart of M6. Install the directional lattice rules and open the annotation
path. Both produce normalized formers via PR1's constructors.

**The rule ordering, per [01-milestones.md](01-milestones.md) §M6.** Insert a
lattice block into `constrain` ([constrain.go:126](../../internal/solver/constrain.go))
between the `ErrorType` short-circuit and the structural `switch`, plus an
"exists" block after the `switch`:

- **"For all" rules — eager, deterministic, fire first.**
  - `(A | B) <: C` ⟹ `A <: C` *and* `B <: C`. A `UnionType` on the **sub** side
    decomposes immediately, before the structural switch and the var arms. Safe
    with a variable super (`(A|B) <: α` ⟹ `A <: α`, `B <: α`).
  - `A <: (B & C)` ⟹ `A <: B` *and* `A <: C`. An `IntersectionType` on the
    **super** side decomposes immediately. Safe with a variable sub.

  These "just produce more sub-constraints", so they fire eagerly regardless of
  what is on the other side.

- **Variable deferral — handled by the existing var arms.** When a variable faces
  a whole union/intersection it does **not** decompose:
  - `α <: (B | C)`: sub is a var, super is a union. The for-all rules do not
    apply. Fall through to the `subVar` arm
    ([constrain.go:427](../../internal/solver/constrain.go)), which records the
    **whole** union as an upper bound — exactly "add `B | C` to α's upper
    bounds", not "guess α := B". This is the speculative-pinning avoidance the
    milestone calls for, and it already works once `UnionType` is a legal type.
  - `(A & B) <: α`: symmetric — the `superVar` arm
    ([constrain.go:441](../../internal/solver/constrain.go)) records the whole
    intersection as a lower bound.

- **"Exists" rules — existential, under a probe, only when the deciding side is
  concrete.**
  - `A <: (B | C)` ⟹ `A <: B` *or* `A <: C`, fired **after** the structural
    switch and **only when `A` is not a variable**. Trial each member under a
    `newProbe`, commit the first success, discard the losers — the same shape as
    the overload intersection arm ([constrain.go:387](../../internal/solver/constrain.go)),
    with a cloned `seen` per arm.
  - `(A & B) <: C` ⟹ `A <: C` *or* `B <: C`. The existing overload arm **is**
    this rule; generalize its guard so it serves a plain annotation intersection,
    not only an overload synthesis. Keep the specificity ordering for the
    function-arm case; a non-function intersection trials in declaration order.

**Union exactness one-way rule (base form; the flag itself lands in PR4).** When
the sub is an **inexact** union and the super is closed — an exact union, or any
non-union concrete that the open tail could violate — reject with an
`InexactIntoExact`-style error before for-all decomposition, mirroring the object
arm ([constrain.go:275](../../internal/solver/constrain.go)). Exact-into-inexact
and exact-into-exact follow from member-wise decomposition. PR4 supplies the flag
this reads; PR2 writes the rule against `Inexact == false` as the only reachable
case until then.

**Annotation input (`resolveTypeAnn`).** Add the two arms
([type_ann.go:21](../../internal/solver/type_ann.go)):

```go
case *ast.UnionTypeAnn:
    // resolve each member; recover an unsupported member to a fresh var, as the
    // object/tuple arms do (type_ann.go:124, :158); build via newUnion.
case *ast.IntersectionTypeAnn:
    // symmetric, via newIntersection.
```

Each records `Prov` against the annotation node and is cascade-safe: an
unsupported member recovers to a fresh var so the former keeps its shape, the
same recovery the `Promise<bad>` and object/tuple arms use.

**Tests.** `number | string` annotation accepts `number`, rejects `boolean`; an
intersection annotation is satisfied by a value at both member types; `α`
constrained against `number | string` records the union whole (assert via the
rendered binding type, not bound internals); `(A & B) <: C` picks a member;
`A <: (B | C)` for concrete `A` picks a branch and a non-member is rejected;
both round-trip through the printer; an inferred union from a multi-branch return
still renders (regression against PR1's normalization).

---

### PR3 — Monomorphic function-type annotations

Resolve `ast.FuncTypeAnn` into `soltype.FuncType` for the monomorphic case, so
function types are writable in source. This is the same "annotation input" work as
PR2 and composes with it — a union or intersection of function types resolves
member-wise. The core target type already exists: `soltype.FuncType` and its
accept-set subtyping rule shipped in M3 ([constrain.go:171](../../internal/solver/constrain.go)),
and `FuncParam` already carries `Optional` / `Rest` and a `Pat`.

**Add the `*ast.FuncTypeAnn` arm to `resolveTypeAnn`**
([type_ann.go:21](../../internal/solver/type_ann.go)):

- Resolve each `Param`'s value annotation via `resolveTypeAnn`; carry its
  `Optional` / `Rest` flags and `Pat` onto the `soltype.FuncParam` (M1 `IdentPat`
  plus M4's structural pats already exist).
- Map `ta.Inexact` ([ast/type_ann.go:444](../../internal/ast/type_ann.go)) to
  `FuncType.Inexact`, resolve `Return`, record `Prov` against the annotation node.
- Recover an unsupported part to a fresh var and keep the function shape —
  cascade-safe, mirroring the `Promise<bad>` and object/tuple arms
  ([type_ann.go:124](../../internal/solver/type_ann.go),
  [type_ann.go:158](../../internal/solver/type_ann.go)).

**Scope boundary — report unsupported (keep the wrapper, recover the inner) for:**

- **Generic** annotations (`TypeParams` non-empty). Resolving a written `T` needs a
  type-name scope, which is M7 TypeRef work — `resolveTypeAnn` already records
  "Full name resolution against the type scope still arrives with M7"
  ([type_ann.go:20](../../internal/solver/type_ann.go)).
- **`throws`** (`Throws` non-nil) — M9.
- **Lifetime params / lifetime-annotated params** (`LifetimeParams` non-empty) —
  the lifetime-annotation surface (M6.5).

These mirror the existing "supported wrapper, unsupported inner" recovery, so a
function annotation with a deferred part still yields a function-shaped type rather
than collapsing the binding.

**Why it lands in M6.** It is annotation input like PR2, it makes PR5's
Variation-B check reachable in real source, and it lets M3's accept-set acceptance
— the `fn(x, y)` callback-slot rule, currently only unit-tested on hand-built
`FuncType` values — be written as source. M5's variance examples are
function/method-typed and will consume it too; landing it here (before M5) removes
that blocker.

**Tests.** `val f: fn(x: number) -> string = ...` checks structurally and rejects a
mismatched body; an inexact `fn(x: number, ...) -> string` annotation resolves and
round-trips; a function annotation as a union member
(`fn() -> number | fn() -> string`) resolves; the M3 accept-set acceptance now
expressible in source (into a `fn(x, y)` callback slot, `fn(x, ...)` / `fn(...)`
are accepted, `fn(x)` and a 3-param function rejected); a generic / `throws` /
lifetime function annotation reports the documented unsupported feature and
recovers function-shaped.

---

### PR4 — Union exactness flag end to end + `match` exhaustiveness (union leg)

Thread the flag from surface syntax to the exhaustiveness payoff.

**AST + parser.** Add `Inexact bool` to `ast.UnionTypeAnn`
([ast/type_ann.go:381](../../internal/ast/type_ann.go)) and teach the parser to
set it on a trailing `...` in a union, mirroring the object/tuple/function
inexact markers already parsed ([ast/type_ann.go:316](../../internal/ast/type_ann.go)
and siblings). `IntersectionTypeAnn` gets no marker.

**Resolve and thread.** `resolveTypeAnn`'s union arm (PR2) passes
`ta.Inexact` into `newUnion`. Coalesced **output** unions are **exact by
default** — `combine` ([coalesce.go:295](../../internal/solver/coalesce.go)) mints
`newUnion(parts, false)` — matching exact-by-default for inferred shapes. The
flag must be **threaded by coalescing, not just stored**: where a union's
exactness derives from a source former's exactness it is carried through. M6's
reachable case is the annotation and the borrow-join union (PR6); `keyof` /
tuple-element-union propagation is M9.

**Constrain.** Activate the full one-way rule from PR2 against the real flag:
exact `<:` inexact ok, inexact `<:` exact rejected, exact `<:` exact requires the
member sets to match after normalization.

**`match` exhaustiveness — the union leg.** Extend the M4 path
([infer_expr.go:1475](../../internal/solver/infer_expr.go) `checkMatchExhaustive`):

- `structuralInexact` ([infer_expr.go:1493](../../internal/solver/infer_expr.go))
  grows a `UnionType` arm returning its `Inexact` flag, so an **exact** union
  scrutinee with arms covering every member needs no catch-all, while an
  **inexact** union requires one — the third leg after structural (M4) and ahead
  of enum (M5).
- Covering "every member" for a union of literal/structural members extends
  `armCoversShape` ([infer_expr.go:1512](../../internal/solver/infer_expr.go)): an
  exact union is exhaustive when the arm patterns collectively match each member.
  For an exact union of literals this is literal-pattern coverage of the member
  set; reuse the per-member shape test rather than inventing a new coverage
  engine. The `NonExhaustiveMatchError`
  ([errors.go:622](../../internal/solver/errors.go)) is unchanged; only its
  trigger condition widens.

**Tests.** An exact union `"a" | "b"` is assignable to inexact `"a" | "b" | ...`
but not the reverse; an exact-union `match` covering all members needs no
default; an inexact-union `match` requires a default; printer round-trip for the
inexact marker.

---

### PR5 — The `_ <: unknown` (⊤) and `never <: _` (⊥) rules + close Variation-B

Small. Depends on PR1 for the rule placement and on PR3 for the end-to-end test.

**The top rule.** Add to `constrain`: any `sub <: UnknownType` succeeds —
everything is a subtype of `unknown` ([01-milestones.md](01-milestones.md), "the
`_ <: unknown` (⊤) subtyping rule"). Place it early, beside the `ErrorType`
short-circuit, so it short-circuits before the structural switch.

**The bottom rule.** Symmetrically, `NeverType <: super` succeeds — `never` is the
bottom. Normalization drops `never` from unions, but a bare coalesced `never` can
still flow as a sub, so the rule keeps it sound. Pair it with the top rule for
lattice completeness even though `_ <: unknown` is the named deliverable.

**Close M4's Variation-B gap.** With ⊤ expressible, implement the function arm's
KNOWN GAP ([constrain.go:195-202](../../internal/solver/constrain.go)): when
`super` is inexact and `sub` declares more params than `super`, constrain
`unknown <: sub.Params[i].Type` at each extra position (exact-types §4.2.1.2
"Variation B"). Remove the comment's "left unchecked for now" and the dependency
note.

**Now reachable in source.** PR3 resolves the inexact function annotation that
drives the extra-position branch, so this PR lands the rule **and** an end-to-end
fixture, not just a unit test on hand-built `FuncType` values. A wider exact
function flowing into an inexact-callback slot exercises the
`unknown <: sub.Params[i].Type` check through `resolveTypeAnn`.

**Tests.** `x <: unknown` holds for every concrete `x` and for a borrow/union;
`never <: x` holds; a unit test on the function arm with a hand-built inexact
`FuncType` super and a wider exact `sub` asserts the extra-position `unknown`
check fires; and the same case in real source via a `fn(x, ...)` annotation.

---

### PR6 — Permissive mut-borrow join (relax D3)

Relax `joinBorrows` ([infer_expr.go:367](../../internal/solver/infer_expr.go))
from D3's reconcile-or-error to **reconcile-or-union**, matching TypeScript's
read-until-narrowed treatment of a union of mutable objects.

**Current behavior.** All inputs must be `mut` borrows of objects with the same
key set; shared fields are pinned invariant by constraining both directions
([infer_expr.go:390-400](../../internal/solver/infer_expr.go)). Differing field
*types* therefore error — `TestInferIncompatibleBorrowJoinErrors`.

**New behavior.**

- **Compatible case unchanged.** When the shared fields reconcile, still join to
  one carrier with a union lifetime — the M4 D3 output `mut ('a | 'b) {x}`.
- **Incompatible case → union.** When a shared field's types do not reconcile,
  produce the type-level union of the distinct borrows,
  `newUnion([mut 'a {x: number}, mut 'b {x: string}], false)`, instead of pinning
  and erroring. This needs PR1's union output carrying `mut` `RefType` members —
  M4's join could only build a single lifetime-union carrier, never a union of
  two distinct borrows.
- **Detect incompatibility without leaving an error.** Trial the invariant pin
  under a discard-only probe ([probe.go](../../internal/solver/probe.go)); if it
  produces errors, discard and emit the union; if it succeeds, commit the
  single-carrier join. This keeps the reconcile attempt side-effect-free on the
  union path.

**Reads work; conflicting writes stay read-only.** A read `.x` through the union
yields `number | string` via PR2's "for all" union rule on member access — the
covariant read view. A **write** to a conflicting field through the un-narrowed
union is **rejected**, which is sound: an un-narrowed union of mutable objects is
read-only at its conflicting fields. Re-enabling the write after a runtime-type
narrow is the **non-discriminated narrowing** case the milestone flags as
narrowing-last; the solver has no narrowing infrastructure yet, so M6 stops at
the sound read-only behavior and does not gate writes on a narrow. Document this
at `joinBorrows` and in the Open questions.

**Tests.** Replace `TestInferIncompatibleBorrowJoinErrors`: joining
`mut {x: number}` and `mut {x: string}` now infers
`(mut 'a {x: number}) | (mut 'b {x: string})`; a `.x` read off the result yields
`number | string`; the compatible join still renders the single carrier with a
union lifetime; a write to `.x` on the un-narrowed union is rejected with a clear
message.

---

## Testing strategy

- **Granular table tests** in the solver package, the established pattern: each
  entry is `(source, expected printed type | expected error message)`, asserting
  the **full** error message per CLAUDE.md. New files / sections for lattice
  subtyping, union/intersection annotations, union exactness + `match`, and the
  permissive join.
- **Normalization unit tests** drive `newUnion` / `newIntersection` directly
  (PR1), since several behaviors — `ErrorType` elision, subsumption, canonical
  order — are awkward to surface through source alone.
- **Snapshot churn is expected** when PR1 routes `combine` through the
  normalizer: previously-unordered or undeduped coalesced output tightens. Re-run
  with `UPDATE_SNAPS=true` and review each diff as an intended improvement.
- **Regression**: the existing union-output renders from M4 multi-branch returns
  must still pass through PR1 unchanged except for canonical ordering.

## Risks & gates

- **`constrain` ordering is the subtle part.** Getting "for all before exists"
  and the variable deferral wrong reintroduces speculative pinning — the exact
  failure mode the design avoids. Gate: a test that `α <: (number | string)`
  records the union as a bound and does **not** pin `α` to `number`, plus a test
  that a concrete `A <: (B | C)` does pick a branch. If the var-deferral path
  ever trials branches, the ordering is wrong.
- **`compareType` totality.** An ordering that is not consistent with `equalType`
  makes canonicalization unstable and dedup unreliable. Gate: a property-style
  test that `newUnion` of a member list and of its shuffle render identically and
  `equalType`-match.
- **Subsumption cost.** The subtype test in normalization runs `constrain` under
  a probe; keep it gated to concrete members and off the hot path. It runs at
  construction, not in the `constrain`/`coalesce` inner loops.
- **Permissive join soundness.** The read-only-until-narrowed contract is only
  sound if conflicting-field writes are actually rejected. Gate: the write-
  rejection test in PR6.

## Acceptance (maps to [01-milestones.md](01-milestones.md) §M6)

- `number | string` annotation accepts `number`, rejects `boolean`. (PR2)
- An intersection annotation is satisfied by a value at both member types. (PR2)
- Both round-trip through the printer; inferred unions from multi-branch returns
  still render. (PR1/PR2)
- A monomorphic `fn(x: A) -> B` annotation resolves and checks structurally; the
  M3 accept-set callback-slot rule is expressible in source; a generic / `throws`
  / lifetime function annotation reports unsupported and recovers function-shaped.
  (PR3)
- An exact union `"a" | "b"` is assignable to inexact `"a" | "b" | ...` but not
  the reverse. (PR4)
- An exact-union `match` covering all members needs no default; an inexact-union
  `match` requires one. (PR4)
- `_ <: unknown` holds for every type; the function-arm Variation-B check is wired
  and exercised end-to-end via an inexact function annotation. (PR5)
- An incompatible mut-borrow join renders
  `(mut 'a {x: number}) | (mut 'b {x: string})` and reads `.x` as
  `number | string`. (PR6)

## Open questions

1. **Narrowing has no milestone.** PR6 leaves conflicting union fields read-only,
   which is sound — but the broader narrowing story (write-after-narrow here,
   match-arm narrowing in `pattern_matching` R9, general union narrowing) is **not
   assigned to any M-series milestone**. `03-references.md` files it under the
   post-MVP "narrowing future," tied to negation types. The settled rule is that
   **narrowing introduces a new binding** ([02-design-notes.md](02-design-notes.md)
   §"Settled decisions"), which removes the flow-sensitive-retyping burden, but a
   milestone still has to own the construct. Decide whether to scope one before or
   after the MVP cutover.
2. **Generic function-type annotations.** PR3 covers only the monomorphic case.
   The generic form needs a type-name scope to resolve a written `T`, which is M7
   TypeRef work. Confirm M7 is the home, or pull it forward if M5's generic
   method/variance work demands it sooner.
3. **Intersection beyond lattice identities.** M6 normalizes intersections only
   to the identity level. If a later milestone needs general intersection-of-
   objects distribution, decide whether it extends `newIntersection` or lands as
   a separate reduction (cf. M9's operator reduction).
