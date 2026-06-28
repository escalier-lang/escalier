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
5. **The `_ <: unknown` (⊤) and `never <: _` (⊥) rules**. These close the
   **function-arm Variation-B gap** that M4 left open and documented as a KNOWN GAP in
   [m4-implementation-plan.md](m4-implementation-plan.md). The ⊤ rule supplies the
   extra-position check M4 deferred, now testable end-to-end via an inexact function
   annotation.
6. **The permissive mut-borrow join**: degrade an incompatible reconcile to a
   read-until-narrowed union instead of an error. Joining `mut {x: number}` with
   `mut {x: string}` now infers `(mut 'a {x: number}) | (mut 'b {x: string})` instead
   of erroring on `number <: string`.
7. **`if-let` / `let-else`**: one-arm refutable-match forms that bind a fresh name at
   the narrowed member type, leaving the scrutinee's union type untouched. `let-else`'s
   `else` must diverge, checked via the `never` (⊥) rule.
8. **Subsumption of inferred types**: a concrete-gated pass at the type-finalization
   boundaries collapses an inferred `1 | number` to `number`, so the rendered type and
   `equalType` agree with the annotated form.

## Scope — deliberately out of M6

- **Exactness *propagation through reduction*** — `keyof` of an exact object, the
  element-union of an exact tuple — is **M9**. M6 lands the flag and the
  match-exhaustiveness payoff, not operator-driven propagation
  ([01-milestones.md](01-milestones.md) M9, "Exactness propagation through
  operators").
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
 │     ├─► PR2.5 (trial-and-commit cleanup: shared helper, specificity order,
 │     │          delete constrainAssign)
 │     ├─► PR2.7 (tighter BorrowEscape promotion: lifetime-blocker check)
 │     └─► PR4 (union exactness flag + match exhaustiveness leg)
 ├─► PR3 (monomorphic function-type annotations)
 │     └─► PR5 (⊤/⊥ rules + Variation-B close — now testable end-to-end)
 ├─► PR6 (permissive mut-borrow join)
 ├─► PR7 (if-let / let-else — needs PR4 + PR5)
 └─► PR8 (subsume inferred types at finalization — needs PR1 + PR2)
```

- **PR1 is first because every other PR mints or compares a union/intersection**,
  and they must all route through one normalizer or the canonical-order and
  identity guarantees leak. It is "born-with-the-type" infra in the same sense
  M2.5's `Prov` discipline was.
- **PR2 before PR4** because the exactness one-way rule is a refinement of the
  base lattice rules PR2 installs.
- **PR2.5 depends only on PR2** and could land before or after PR4. Landing it
  early makes the trial-and-commit machinery uniform across the four sites
  before PR4's exactness one-way rule changes how the union-super exists
  rule renders its diagnostics. Independent of PR3/PR5/PR6/PR7/PR8 — purely a
  cleanup of the sites PR2 has just touched.
- **PR2.7 depends only on PR2** and is independent of every other PR. It is a
  diagnostic-quality fix in the BorrowEscape firing condition that tightens
  both the single-trial RefType arm and the PR2 `commonBorrowEscape`
  union-level promotion. The constraint outcome doesn't change — only which
  error fires when it fails. Could land before or after PR2.5; no ordering
  dependency between the two.
- **PR3 (function annotations) feeds PR5.** The `_ <: unknown` rule itself needs
  only PR1, but its reason for existing — the Variation-B check — is only
  *reachable* once an inexact function annotation can reach `constrain`, which is
  PR3. So PR3 lands first and PR5 carries the end-to-end test.
- **PR3 and PR6 depend only on PR1** (PR6 also reads PR2's union rule for the
  covariant-read story but does not require it to land first).
- **PR7 needs PR4 and PR5.** `if-let`/`let-else` reuse PR4's union member matching to
  bind the narrowed name, and `let-else`'s divergent-`else` check uses PR5's `never`
  (⊥) rule, so it lands after both.
- **PR8 needs only PR1 and PR2** and lands last. It reuses PR1's `newUnion` /
  `newIntersection` and PR2's subtype probe, runs once per finalized type rather than in
  the coalesce loop, and is additive — droppable if M6 ships without it.

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

**The normalization splits into a Context-free core and a Context-gated
subsumption step**, because the mint sites differ in what they have available.
`combine` and `coalesce` are free functions with no `*Context`
([coalesce.go:33](../../internal/solver/coalesce.go),
[coalesce.go:295](../../internal/solver/coalesce.go)); the `coalescer` /
`schemeCoalescer` visitors hold only `seen`. `resolveTypeAnn` and `joinBorrows`
are `*checker` methods and so reach `c.ctx` and `c.probe`. So:

- **`newUnion(parts, inexact)` / `newIntersection(parts)` are Context-free** and do
  the bulk of the work — flatten, lattice identities, `ErrorType` elision,
  structural dedup, canonical order, collapse. Every site calls these.
- **Subsumed-member elimination is a separate, optional pass** that needs a
  `*Context` because it runs `constrain` under a probe (PR1 step 5). The
  constructors take it as an optional dependency — `newUnion` runs subsumption only
  when handed a Context, and skips it otherwise. `combine` passes none (its members
  are already coalesced, so the dedup + identity passes already tighten them);
  `resolveTypeAnn` and `joinBorrows` pass `c.ctx` so an annotation or borrow union
  is fully subsumed. This keeps the constructors usable from the coalescer without
  threading a Context through it.

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
Each runs the normalization the milestone enumerates in one pass — step 5 only
when a Context is supplied:

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
5. **Subsumed-member elimination (Context-gated, optional).** Drop a union member
   that is a subtype of another union member, and an intersection member that is a
   supertype of another. The subtype test reuses `constrain` under a discard-only
   probe (a no-error trial means subsumption holds), so it needs a `*Context` — it
   runs only at the mint sites that have one (`resolveTypeAnn`, `joinBorrows`), and
   `combine` skips it (see "smart constructors" above). Keep it gated to concrete
   members to avoid trialling against inference variables mid-walk. Steps 1–4, 6,
   and 7 are Context-free and run everywhere.
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

Canonical form is the stronger normalization, and its cost lands once per construction
rather than on every `equalType` call, which is the hot path. What it buys beyond
equality:

- **Equality stays cheap and unchanged.** `equalTypeSlice` already walks members
  pairwise, so canonical order makes it correct with no rewrite. A set-based equality
  would turn a hot function into a sort- or O(n²)-per-call comparison and add a new code
  path to get wrong.
- **Rendering is deterministic.** `string | number` and `number | string` print
  identically, which keeps snapshots stable. Set-based equality does nothing for display,
  since the members would still render in construction order.
- **Dedup happens at construction.** The normalizer collapses structurally-equal members
  in the same pass. Equality-as-set leaves the duplicates in the type itself.
- **Structural keys are stable.** A canonical form is usable as a stable key for
  memoization or caching; an unordered set is not.

Set-based equality would fix only equality, at the most expensive place to fix it, and
leave rendering and dedup still needing a canonical order.

**Route `combine` and `mergeObjectGroup` through the constructors.** `combine`
([coalesce.go:302](../../internal/solver/coalesce.go)) currently builds
`&soltype.UnionType{Types: parts}` / `&soltype.IntersectionType{Types: parts}`
raw; `mergeObjectGroup` ([coalesce.go:446](../../internal/solver/coalesce.go))
builds a raw `IntersectionType` for a shared property. Both become `newUnion` /
`newIntersection` calls, passing **no** Context, so they get the Context-free
normalization without subsumption. This is observable: previously-unnormalized
coalesced output now dedups and orders canonically, so some rendered snapshots
tighten — update them with `UPDATE_SNAPS=true`.

**Tests.** Table-driven over `newUnion` / `newIntersection`: flatten, dedup,
`never`/`unknown` drop, `ErrorType` elision (and sole-member retention),
canonical order (`string | number` and `number | string` render identically and
`equalType`-match), single-member and empty collapse. Printer round-trip for an
inexact union. The subsumption cases (`number | 1` ⇒ `number`; `{x} & {x, y}` ⇒
`{x, y}` on the intersection side per the meet) pass a `*Context` so the
Context-gated step runs; a separate case asserts that **without** a Context the
constructor leaves non-subsumed members in place — the `combine` posture.

---

### PR2 — Lattice subtyping rules in `constrain` + annotation input

The heart of M6. Install the directional lattice rules and open the annotation
path. Both produce normalized formers via PR1's constructors.

**The rule ordering, per [01-milestones.md](01-milestones.md) §M6.** A **pre-switch
lattice block** in `constrain` ([constrain.go:126](../../internal/solver/constrain.go)),
inserted between the `ErrorType` short-circuit and the structural `switch`, carries
every rule whose deciding operand is a union/intersection **super**, plus the
union-**sub** for-all rule. It has to precede the switch: several structural arms —
**notably the RefType arm** ([constrain.go:356-361](../../internal/solver/constrain.go))
— return early on a non-variable super, so a super-side union/intersection operand
would be intercepted before its lattice-ness is ever considered (see the RefType
note below). The one rule that stays in the switch is the intersection-**sub**
exists case, where the sub is the lattice node the switch already dispatches on.
The block runs for-all decomposition unconditionally and the exists trial when the
deciding side is concrete, and falls through to the variable arms when a variable
is on the deciding side:

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
  - `A <: (B | C)` ⟹ `A <: B` *or* `A <: C`, fired **in the pre-switch block**
    (so it precedes the RefType arm) and **only when `A` is not a variable**.
    Trial each member under a `newProbe`, commit the first success, discard the
    losers — the same shape as the overload intersection arm
    ([constrain.go:387](../../internal/solver/constrain.go)), with a cloned `seen`
    per arm.
  - `(A & B) <: C` ⟹ `A <: C` *or* `B <: C`. The sub **is** the lattice node here,
    so the structural switch's `IntersectionType` case
    ([constrain.go:366](../../internal/solver/constrain.go)) already matches it
    first — there is no interception risk, so this one case can stay in the switch.
    The existing overload arm **is** this rule; generalize its guard so it serves a
    plain annotation intersection, not only an overload synthesis. Keep the
    specificity ordering for the function-arm case; a non-function intersection
    trials in declaration order.

**RefType interception — why the super-side rules go pre-switch.** The structural
`switch` dispatches on the **sub**, so whichever arm matches the sub decides the
constraint without ever inspecting the super's lattice structure. The RefType arm is the
problem case. When the sub is a borrow and the super is **not** a type variable, that
arm ([constrain.go:356-361](../../internal/solver/constrain.go)) treats the pair as a
borrow against a concrete type, peeling to the inner type or returning a
`BorrowEscapeError` when the sub has a lifetime (`sub.Lt != nil`).

A `UnionType` or `IntersectionType` super is not a variable, so it takes that same path.
Without the pre-switch block, `mut 'a {x} <: (mut 'a {x} | mut 'b {y})` hits the RefType
arm and **escape-errors**, even though the sub matches the first union member exactly.
The arm sees only "super is not a variable." It never sees that the super is a union.
This shape shows up directly in PR6 (unions of borrows) and whenever a borrow flows into
a union annotation.

Running the super-side union/intersection rules **before** the switch fixes it. The
lattice rule matches the member first, before the RefType arm can intercept. A
union/intersection **sub** needs no pre-switch handling, since the switch dispatches on
the sub and a lattice sub is matched by its own case.

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

### PR2.5 — Trial-and-commit cleanup: shared helper, specificity order, delete `constrainAssign`

Cleanup pass over the trial-and-commit sites the union-super exists rule joined
in PR2. With PR2 landed, the solver has four such sites: the original
overload-resolution path in `resolveOverload`
([overload.go:43](../../internal/solver/overload.go)), the intersection-sub
arm in `constrain`, the new union-super exists rule, and the M5-era
`constrainAssign` workaround in
[infer_expr.go:991](../../internal/solver/infer_expr.go). All four share the
same shape — iterate members, probe-trial each, commit on first success,
collect per-trial errors on failure — and the same user-visible failure modes:
over-constraining inner inference variables, order-dependence on canonical
sort, brittleness to union-membership changes, and misleading downstream
errors. PR2.5 lands the three lowest-cost mitigations as one cleanup.

**Delete `constrainAssign`.** Its doc-comment already flags itself as a
pre-M6 workaround for assigning into a union target, with the deliberately
not-fixable shape pointing at "M6's deferred union/intersection rules in
`constrain`" as the proper fix. PR2 lands those rules. Route assignment
through `c.constrain` and delete the function. Verify that the pinned
regression `TestInferAssignUnionTargetVarRHSOverNarrows` still passes
through the new path; if its expected behavior diverges, update the
assertion in place rather than reinstating the workaround. Reduces the
trial-and-commit surface from four sites to three.

**Extract a shared `trialAndCommit` helper.** All three remaining sites have
the same outline: a candidate order, a per-candidate trial body, a
first-success commit, and a per-trial error collector for the failure path.
Pull this into one helper in the probe package (or beside it in
`constrain.go`), parameterised by the trial body callback and the candidate
list. Each call site shrinks to a one-liner over its own candidate order
plus the per-candidate work. Concentrates every later mitigation into one
location and catches silent drift in the probe / cloned-`seen` discipline
between the rules. The intersection-sub arm's specificity-order use stays
the same. The union-super exists rule's free-var-member skip rides on top.

**Specificity-ordered trials, not canonical-ordered.** The
IntersectionType-sub arm already uses `specificityOrder`
([overload.go:143](../../internal/solver/overload.go)) for its FuncType-only
case. Extend the ordering to general types — LitType before PrimType,
narrower object before wider, etc. — and route the union-super exists rule
through it. Most-specific-first means adding a less-specific union member
does not change which branch wins, which addresses the brittleness failure
mode head-on. The order also matches intuition better than canonical sort
on member kind, since "the more specific branch wins" is what most type
systems do. The ordering choice still has to be a total order consistent
with `equalType` for the trial sequence to be reproducible. Use the
existing `compareType` for tie-breaks.

**Out of scope here.** Two further diagnostic-quality mitigations are
deferred to M7: tagging committed bounds with their union-trial origin
(so a downstream error chases the tag), and ambient-time ambiguity
detection (so the over-constraint surfaces at declaration time, not at
downstream use). See [01-milestones.md](01-milestones.md) §M7 for the
enumerated failure modes and the deferred mitigations. The structurally
larger fixes — true backtracking and disjunctive bound representation —
are post-MVP work; see [01-milestones.md](01-milestones.md) "Later
(post-MVP)".

**Tests.** A new table in the constrain-tests file: for each rule (overload,
intersection-sub, union-super exists), adding a less-specific member to the
candidate list does not change which branch commits. A regression that
removing `constrainAssign` keeps every existing assignment test passing.
The free-var-member skip in the union-super rule is unaffected since
specificity ordering still respects the "no free TypeVar members" rule.

**Depends on:** PR2 (the union-super exists rule, the new trial site this
cleanup consolidates).

---

### PR2.7 — Tighter `BorrowEscapeError` promotion: only fire when the lifetime is the genuine blocker

Diagnostic-quality fix for the `BorrowEscapeError` class. Today the rule fires
whenever a borrow with a non-nil lifetime constrains against a non-RefType
non-var super, regardless of whether the inner would have matched if the
borrow weren't there. This is misleading whenever the inner is also a shape
mismatch: the error reads "borrowed value … does not live long enough to
satisfy …", which suggests "extend the lifetime and this would work" when in
fact the inner shape doesn't fit either way. The PR2 union-level promotion
through `commonBorrowEscape` inherits the same problem: every per-trial
BorrowEscape gets promoted, even when no branch's shape would have matched
even with the lifetime stripped.

**The rule.** Emit BorrowEscapeError only when peeling the borrow's inner
would have satisfied the super. Otherwise emit the shape-mismatch error
that peeling would have produced (typically a CannotConstrainError or a
deeper structural-arm error). Concretely:

- **Single-trial RefType arm** ([constrain.go:368-373](../../internal/solver/constrain.go)).
  Before returning `BorrowEscapeError{Sub: sub, Super: super}`, trial
  `sub.Inner <: super` under a discard probe. If it succeeds, the lifetime
  was the genuine blocker; emit BorrowEscape. If it fails, surface the
  inner-trial error instead — that's the actual root cause.
- **Union-level promotion** (`commonBorrowEscape` in
  [constrain.go](../../internal/solver/constrain.go)). Replace the
  "every trial returned BorrowEscape" check with the stronger "peeling
  sub's inner against the union super has at least one branch that would
  have succeeded." The check reuses the existing union-super exists rule
  through `c.constrain(sub.Inner, super, ...)` under a discard probe.

The probe makes the inner re-trial side-effect-free: any bound mutations
the peeling would have caused are rolled back before the rule decides
which error to emit.

**Example pairs.** Each pair shows the misleading message today and the
clearer message after PR2.7:

```
&'a {x: number} <: number
  today:    borrowed value &'a object does not live long enough to satisfy number
  PR2.7:    cannot constrain object <: number
            (peeling: {x: number} <: number fails — shape, not lifetime)

&'a {x: number} <: {x: number}
  today:    borrowed value &'a object does not live long enough to satisfy object
  PR2.7:    borrowed value &'a object does not live long enough to satisfy object
            (peeling: {x: number} <: {x: number} succeeds — lifetime IS the blocker)

&'a {x: number} <: (number | string)
  today:    borrowed value &'a object does not live long enough to satisfy number | string
  PR2.7:    cannot constrain object <: number | string
            (peeling: every branch is a shape mismatch — lifetime is incidental)

&'a {x: number} <: (number | {x: number})
  today:    borrowed value &'a object does not live long enough to satisfy number | {x: number}
  PR2.7:    borrowed value &'a object does not live long enough to satisfy number | {x: number}
            (peeling: branch 2 succeeds — lifetime IS the blocker for the matching branch)
```

**Out of scope.** The error class itself is not renamed. The fix is
about WHEN it fires, not what it says. A separate diagnostic-rewording
pass could revisit the "does not live long enough" phrasing once the
firing condition is provably "lifetime was the genuine blocker."

**Tests.** A new table for each of the four example shapes above. Each
asserts both the error kind and the full message. The
`TestConstrainUnionSuperPreservesBorrowEscape` regression updates to use
the meaningful shape (`&'a {x:number} <: (number | {x:number})`), since
the original test happens to be in the misleading-cause column. Add a
sibling test for the non-union case so the single-trial RefType arm's
new behavior is also pinned.

**Depends on:** PR2 (the union-super exists rule and `commonBorrowEscape`
helper this tightens). Independent of PR2.5, PR3, PR4, PR5, PR6, PR7,
PR8 — purely a diagnostic-quality fix in the constrain code path.

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

### PR4 — Union exactness flag: surface syntax + `match` exhaustiveness (union leg)

Thread the `Inexact` flag from surface syntax to the exhaustiveness payoff. The
representation and the constrain rule already exist, so this PR is the missing
two ends — the parser/AST marker that lets source write an inexact union, and the
`match`-exhaustiveness leg that reads the flag.

**Already landed — out of this PR's scope.** Three pieces the original PR4 listed
shipped earlier:

- `soltype.UnionType.Inexact`, its `Accept` flag-carry, and the printer's
  trailing-`...` rendering landed in **PR1**
  ([soltype/type.go:353](../../internal/soltype/type.go),
  [print.go:413](../../internal/soltype/print.go)).
- The one-way exactness rule in `constrain` landed in **PR2 (#776)**: a
  `UnionType` sub decomposes for-all, and an **inexact** sub against a closed
  super emits `InexactUnionIntoExactError`
  ([constrain.go:159-175](../../internal/solver/constrain.go)). It already reads
  the real flag, so there is no separate "activate against the flag" step.

**AST + parser.** Add `Inexact bool` to `ast.UnionTypeAnn`
([ast/type_ann.go:382](../../internal/ast/type_ann.go)) and teach `typeAnn`
([parser/type_ann.go:42](../../internal/parser/type_ann.go)) to set it on a
trailing `...` in a union, mirroring the object/tuple/function inexact markers
already parsed. The union annotation is built by a shunting-yard operator parser,
not `parseDelimSeqInexact`, so the marker is recognized inline: when the operator
just consumed is a `|` and the next token is `...`, consume it, flag the union
inexact, and stop the operand loop. The flag is set on the popped top-level
`UnionTypeAnn` after the operator stack drains. `IntersectionTypeAnn` gets no
marker.

**Resolve and thread.** `resolveUnionTypeAnn`
([type_ann.go:197](../../internal/solver/type_ann.go)) passes `ta.Inexact` into
`newUnion` instead of the hardcoded `false`. Coalesced **output** unions stay
exact by default — `combine` ([coalesce.go:295](../../internal/solver/coalesce.go))
mints `newUnion(..., false)` — matching exact-by-default for inferred shapes.
M6's reachable inexact source is the annotation; the borrow-join union (PR6) and
`keyof` / tuple-element-union propagation (M9) are separate.

**`match` exhaustiveness — the union leg.** Extend the M4 path
([infer_expr.go:2019](../../internal/solver/infer_expr.go) `checkMatchExhaustive`):

- `structuralInexact` ([infer_expr.go:2037](../../internal/solver/infer_expr.go))
  grows a `UnionType` arm returning its `Inexact` flag, so an **exact** union
  scrutinee with arms covering every member needs no catch-all, while an
  **inexact** union requires one — the third leg after structural (M4) and ahead
  of enum (M5). `checkMatchExhaustive` already peels the scrutinee through
  `soltype.CarrierOf` ([ref.go:28](../../internal/soltype/ref.go)), the affine
  borrow-unwrap added after the original PR4 draft, so a union behind a borrow
  reaches the new arm already unwrapped — no extra handling needed.
- Covering "every member" for a union of literal members extends `armCoversShape`
  ([infer_expr.go:2056](../../internal/solver/infer_expr.go)): an exact union is
  exhaustive when the arm patterns collectively match each member. For an exact
  union of literals this is literal-pattern coverage of the member set; reuse the
  per-member shape test rather than inventing a new coverage engine. The
  `NonExhaustiveMatchError` is unchanged; only its trigger condition widens.

**Tests.** An exact-union `match` covering all members needs no default; an
exact-union `match` missing a member is non-exhaustive; an inexact-union `match`
requires a default; printer round-trip for the inexact marker; the parser sets
`Inexact` on a trailing `...` and leaves a bare `A | B` exact. The exact-union
scrutinee is built by inference, since a literal type annotation does not yet
resolve.

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
read-only at its conflicting fields, and a rejected write never changes the union's
type. To write, narrow to one branch with a PR7 `if let` and write through the fresh
mutable view, as in `if let r2: mut {x: number} = r { r2.x = 5 }`. The un-narrowed
binding stays read-only for its whole scope by design, not as a deferral. Document this
at `joinBorrows`.

**Tests.** Replace `TestInferIncompatibleBorrowJoinErrors`: joining
`mut {x: number}` and `mut {x: string}` now infers
`(mut 'a {x: number}) | (mut 'b {x: string})`; a `.x` read off the result yields
`number | string`; the compatible join still renders the single carrier with a
union lifetime; a write to `.x` on the un-narrowed union is rejected with a clear
message.

### PR7 — `if-let` / `let-else` (one-arm refutable-match narrowing)

Add the two single-arm refutable-binding forms over a union scrutinee. Both desugar to
a one-arm `match`: the pattern introduces fresh bindings at the narrowed member type and
leaves the scrutinee's own union type untouched, which is the binding-based narrowing the
design settles on ([02-design-notes.md](02-design-notes.md) §"Settled decisions"). No
flow-sensitive re-typing is involved.

- **`if-let`.** `IfLetExpr` already exists in the AST and the *legacy* checker infers it
  ([internal/ast/expr.go](../../internal/ast/expr.go),
  [internal/checker/infer_expr.go](../../internal/checker/infer_expr.go)), so this is a
  port of that inference into the solver, not new surface syntax. The pattern matches one
  branch of the scrutinee and binds its names in the consequent at the matched member
  type; the alternate sees the scrutinee unchanged. The pattern may be a type annotation,
  `if let x: number = v`, which is how a union narrows to one member. Reuses M4's
  structural-pattern binding and PR4's union member matching.
- **`let-else`.** New construct, so it needs parser and AST in addition to inference. The
  pattern binds its names for the rest of the enclosing block at the narrowed type, and
  the `else` block must diverge. The divergence check types the `else` block as `never`
  via PR5's `never <: _` (⊥) rule, rejecting an `else` that can fall through.
- **Narrowing stays a new binding.** Neither form re-types the scrutinee. The original
  binding keeps its union type for its whole scope; the narrowed view lives on the
  pattern's fresh names. This keeps both forms inside Escalier's one-type-per-binding
  model.
- **Re-enables write-after-narrow.** A `mut` type pattern binds a fresh mutable view of
  the matched branch, so `if let r2: mut {x: number} = r { r2.x = 5 }` is how a write
  reaches a field that PR6's read-until-narrowed union keeps read-only. The write goes
  through the fresh binding; the original `r` keeps its union type.

**Tests.** An `if let` over `A | B` binds the matched arm's names at `A` in the
consequent and leaves the scrutinee `A | B` in the alternate; a `let-else` with a
diverging `else` binds for the rest of the block; a `let-else` whose `else` can fall
through is rejected; an `if let r2: mut {x: number} = r` over a PR6 read-only union
allows `r2.x = 5` while `r` keeps its union type; the scrutinee's own type is unchanged
after both forms.

**Decision pending: free type-var members in the union-super exists trial.**
PR7's `if-let` pattern reuses the union-super exists rule PR2 shipped, which
today SKIPS direct TypeVar members of the super union to avoid speculative
pinning. The skip is sound but rejects `if let x: T = u` over `u: T | number`
when no concrete branch matches, even when `T := matched-type` would
type-check. Two designs were on the table at PR2 time and the choice was
explicitly deferred to PR7. See [01-milestones.md](01-milestones.md) §M7
"Open design question — free type-var members in a union-super exists trial"
for the full enumeration. Short version:

- **Keep the skip.** The honest mitigation is restructuring code away from
  the generic union (split signatures, discriminating wrapper). The
  reorder-the-union and explicit-type-argument workarounds DON'T work in
  Escalier today.
- **Two-pass exists trial.** Trial concrete members first; if none commit,
  trial var members in a second pass. Roughly one-day implementation: ~20
  lines in `constrain.go`, one existing test rewrite, two new test cases,
  one comment update, and resolve the M7 open question.

The deferred decision is what `if let x: T = u` should do for the generic
case, so PR7 is the natural place to make the call. Land the chosen rule
as part of PR7 (or as a tail commit on PR2 if the implementer decides
before PR7 starts) and update both the M7 open question and the comment
on the union-super exists rule in `constrain.go`.

### PR8 — Subsume inferred types at finalization

Close the subsumption gap for inferred types without threading a Context through the
coalesce inner loop. `combine` stays Context-free; instead, after coalesce produces a
final type, run one subsumption pass over it using the ambient Context.

- **Two finalization boundaries.** Subsume at generalization, where a scheme is sealed,
  and at the point a monomorphic inferred type is finalized onto the node / `Info` table.
  Both already hold a Context. Each runs once per finalized type, so the pass is off the
  `constrain` / `coalesce` inner loop and cannot reenter coalescing.
- **Concrete-gated, same as the mint sites.** Drop a union or intersection member only
  when a concrete sibling subsumes it. A member that still carries a free type variable
  is left untouched, to avoid speculative pinning, so a scheme whose union is not yet
  ground is unchanged.
- **No change to `equalType` or `combine`.** The pass canonicalizes the finalized type by
  extending the construction-time canonical form with the one normalization step that
  needs a Context. `equalType` stays positional and cheap.

This closes both symptoms recorded under the subsumption gap. An inferred `1 | number`
renders `number`, and because the stored type is now canonical, it is `equalType`-equal
to the annotated `number`, so caching and annotation round-trip agree. Neither soundness
nor assignability changes, since the dropped members were mutually subtype with the one
that subsumes them.

**Tests.** An inferred `1 | number` renders `number` and is `equalType`-equal to the
annotation `number`; an inferred intersection `{x} & {x, y}` renders `{x, y}`; a union
that still carries a free var is left unchanged; assignability decisions are identical
before and after the pass.

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
  normalizer. Two kinds of change, of different blast radius:
  - **Dedup + canonical ordering** of `combine` output — purely cosmetic
    reordering and collapse of structural duplicates. This is the common case.
  - **Note:** subsumption does **not** run at the `combine` site (it is
    Context-gated and `combine` passes none), so a coalesced output like
    `1 | number` is **not** collapsed to `number` here — that collapse only happens
    where a Context is available (`resolveTypeAnn`, `joinBorrows`). So inferred-type
    renders change by reorder/dedup only, not by subsumption. The subsumption collapse
    (`1 | number` ⇒ `number`) lands later in PR8, which subsumes the finalized type
    rather than threading a Context into `combine`, and carries its own snapshot churn.
  - **What the gap costs, and what it doesn't.** It is not a precision loss.
    `1 | number` denotes exactly `number`, so the inferred type is equally tight, just
    redundantly shaped. The real costs are representation size and `equalType` identity.
    Subsumable members accumulate, so a value flowing through many literal cases can
    carry `1 | 2 | … | number`, which a member-iterating `constrain` checks more slowly
    and renders larger. Separately, an inferred `1 | number` is not `equalType`-equal to
    an annotated `number`, so the two forms diverge for caching and for annotation
    round-trip. Neither affects soundness, since the members are mutually subtype, and
    M4 literal widening keeps the accumulation small in practice. PR8 closes both
    symptoms for concrete inferred types by subsuming at the finalization boundaries
    rather than in the coalesce inner loop; what remains after it is only intermediate
    types during solving and unions that still carry free vars.
  Re-run with `UPDATE_SNAPS=true` and review each diff as an intended improvement.
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
- **Subsumption cost and reach.** The subtype test runs `constrain` under a probe,
  so it needs a `*Context` and runs only at the Context-bearing mint sites
  (`resolveTypeAnn`, `joinBorrows`), never from `combine`/`coalesce`. Keep it gated
  to concrete members and off the hot path — it runs at annotation/join
  construction, not in the `constrain`/`coalesce` inner loops. Gate: a test that a
  Context-free `newUnion` leaves non-subsumed members in place while the
  Context-bearing path collapses them.
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
- An `if let` / `let-else` over a union binds the matched member type into a fresh
  name; a `let-else` whose `else` does not diverge is rejected. (PR7)
- An inferred `1 | number` renders `number` and is `equalType`-equal to the annotation
  `number`; a union with a free var is left unchanged. (PR8)

## Open questions

1. **Generic function-type annotations.** PR3 covers only the monomorphic case.
   The generic form needs a type-name scope to resolve a written `T`, which is M7
   TypeRef work. Confirm M7 is the home, or pull it forward if M5's generic
   method/variance work demands it sooner.
2. **Intersection beyond lattice identities.** M6 normalizes intersections only
   to the identity level. If a later milestone needs general intersection-of-
   objects distribution, decide whether it extends `newIntersection` or lands as
   a separate reduction (cf. M9's operator reduction).
