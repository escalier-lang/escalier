# 03 — Grafting the normal-forms layer onto `solver/`

How MLstruct's machinery lands on the existing `internal/solver/` package and on
the planned `simple_sub/` changes. This is a **sketch of seams**, not a PR plan —
shapes and call sites, so a future planning session can break it into PRs. Code
fragments are illustrative Go, named to match what is in the tree today.

The guiding principle: the Simple-sub core is untouched. `TypeVarType` with
`LowerBounds` / `UpperBounds`, `Level`, `extrude`, and `probe` all carry over. We
add one `soltype` node, one new file (`solver/normal.go`), and a second layer
inside `constrain` that the existing structural recursion falls into for the
wrong-polarity and negation cases.

The mapping to MLscript:

| MLscript | Escalier `solver/` |
|---|---|
| `TypeVariable` (lower/upper bounds, level) | `soltype.TypeVarType` — **unchanged** |
| `ComposedType(pol, l, r)` | `soltype.UnionType` / `soltype.IntersectionType` — **exist** |
| `NegType(t)` | `soltype.NegationType` — **new** (§1) |
| `ClassTag` + parent set | `soltype` class node + M5 declared-subtype graph |
| `rec` / `recImpl` (structural recursion) | the existing `constrain` switch (§3) |
| `goToWork` / `constrainDNF` / `annoying` | new `constrainNF` / `annoying` (§3) |
| `NormalForms.scala` (DNF/CNF/Conjunct/Disjunct/LhsNf/RhsNf) | new `solver/normal.go` (§2) |
| `TypeSimplifier.scala` | `solver/simplify.go`, extended (§5) |
| extrusion, levels | `extrude` / `extruder` — **unchanged but for one visitor arm** (§4) |

---

## 1. The new `soltype` node: `NegationType`

Add one sealed node to [`internal/soltype/type.go`](../../internal/soltype/type.go),
beside `UnionType` / `IntersectionType`:

```go
// NegationType is the set-theoretic complement ¬Inner — the MLstruct addition
// that turns the subtype lattice into a Boolean algebra. Like UnionType /
// IntersectionType it is a legal constrain input and a coalesced output; unlike
// them it flips polarity on its child (§4). It is never produced from M1–M12
// source — only from the normalization layer and, once negation-based narrowing
// lands, from a `Not<T>` annotation or a guard's complement branch.
type NegationType struct{ Inner Type }

func (*NegationType) isType() {}
```

Touch points that must grow a `NegationType` arm, all already centralized:

- `LevelOf` ([type.go](../../internal/soltype/type.go)) — `return LevelOf(t.Inner)`.
- The rewriting visitor ([`internal/soltype/visitor.go`](../../internal/soltype/visitor.go))
  — the **polarity-flipping** arm (§4). This is the one place negation differs
  structurally from union/intersection.
- The printer ([`internal/soltype/print.go`](../../internal/soltype/print.go)) —
  render `¬T` / `Not<T>` (surface syntax TBD with narrowing).
- `equalType` / `compareType` ([coalesce.go](../../internal/solver/coalesce.go),
  [lattice.go](../../internal/solver/lattice.go)) — structural equality and a
  `typeKindOrder` slot so conjuncts/disjuncts have a canonical order for dedup.

Everything else about the Simple-sub representation is reused as-is.

---

## 2. The normal-forms module: `solver/normal.go`

New file. The DNF/CNF ADTs and their Boolean algebra, ported from
`NormalForms.scala`. These types are **solver-internal** — they never escape into
`soltype` or the `Info` side table; the surface representation stays
`soltype.Type`, and normal forms exist only for the duration of a `constrain`
call.

```go
// A DNF is a union of conjuncts; a CNF is an intersection of disjuncts.
type DNF struct{ Conjuncts []Conjunct }
type CNF struct{ Disjuncts []Disjunct }

// A Conjunct reads as  Lnf ∩ (⋂ Vars) ∩ ¬Rnf ∩ (⋂ ¬NVars):
// a positive structural part, positive type variables, a negated structural
// part, and negated variables. Disjunct is the exact dual, and Conjunct.neg
// swaps the four fields (De Morgan as a field permutation).
type Conjunct struct {
    Lnf   LhsNf                  // intersection of positive structural atoms
    Vars  set.Set[*soltype.TypeVarType]
    Rnf   RhsNf                  // union of negated structural atoms
    NVars set.Set[*soltype.TypeVarType]
}

// LhsNf is a normalized intersection: at most one of each structural kind plus
// the nominal base. Intersecting two of a kind MERGES structurally — two
// FuncTypes meet to fn(l0|l1) -> (r0&r1); two ObjectTypes merge field-wise; two
// class tags go through the M5 glb, and unrelated classes yield "no glb" which
// makes the whole conjunct ⊥ and drops it.
type LhsNf struct {
    base   *ClassNode            // M5 nominal tag (nil = ⊤ base)
    fn     *soltype.FuncType
    arr    *soltype.TupleType
    obj    *soltype.ObjectType   // single merged object
    // prims/lits/refs as needed
}
// RhsNf is the dual: a union of negated structural atoms.
```

Operations to port, all of which reuse existing `solver` helpers rather than
reimplementing lattice algebra:

- `DNF.mk(t, pol)` / `mkDeep` — push a `soltype.Type` into DNF. The union /
  intersection / negation cases recurse; `NegationType(t)` becomes
  `DNF(CNF.mk(t, !pol).disjuncts.map(neg))`.
- `Conjunct.and` / `Conjunct.or` — intersect / union conjuncts. **Reuse
  [`lattice.go`](../../internal/solver/lattice.go)**: `newIntersection` /
  `newUnion` for the structural merges, `subsumeMembers` to drop subsumed parts,
  `compareType` / `sortTypes` for canonical member order. The "merge two
  FuncTypes / ObjectTypes" cases are the meet/join the existing smart
  constructors already compute.
- `tryMergeUnion` / `tryMergeInter` — lossless merge or keep separate. This is the
  caveat #4 seam: **keep un-mergeable members separate, never collapse to ⊤**
  (see [02-caveats-and-mitigations.md](02-caveats-and-mitigations.md) §4).
- `glb(c, d)` over class nodes — delegate to the **M5 declared-subtype graph**.
  This is the nominal oracle that makes `C & D` for unrelated classes `never`.

---

## 3. The `constrain` graft: structural recursion + a normalization layer

[`constrain.go`](../../internal/solver/constrain.go) today has three relevant
pieces, and the graft slots cleanly into the boundaries between them.

**(a) What stays as the structural recursion (MLstruct's `rec`).** The big
`switch sub := sub.(type)` (constrain.go:270 onward) — prim/lit/func/tuple/object/
ref cases plus the variable arms (bound-append + transitive propagation) — *is*
MLstruct's `rec`/`recImpl`. It handles all the "easy" cases and is unchanged. The
variable arms still record bounds; extrusion and levels still fire here.

**(b) What the existing M6 PR2 pre-switch block becomes.** Today
(constrain.go:184–266) the pre-switch block implements the lattice rules:

- the **"for all" rules** — `(A | B) <: super ⟹ A <: super AND B <: super`
  (union-sub) and `sub <: (A & B) ⟹ sub <: A AND sub <: B` (intersection-super) —
  decompose deterministically and **stay exactly as they are**;
- the **"exists" rules** — `sub <: (A | B)` (union-super) trials each member under
  a `probe` and commits the first success (constrain.go:234–266), and the dual
  `(A & B) <: super` lives in the `IntersectionType`-sub overload arm.

The exists rules are the trial-and-commit the post-MVP "Backtracking + disjunctive
bounds" item flags. **The graft replaces the probe-trial with normalization.**
When the deciding side carries a union/intersection at the non-decomposable
polarity — or a `NegationType` appears anywhere — route to `constrainNF` instead
of trialling:

```go
// Replaces the probe-trial exists rules. When the constraint can't be settled by
// the deterministic "for all" rules and the structural recursion, normalize both
// sides and decompose. No probe, no first-success-commit, no backtracking — the
// DNF/CNF decomposition is deterministic, which is what preserves principal
// inference once negation is in the algebra.
func (c *Context) constrainNF(sub, super soltype.Type, seen set.Set[constraintKey]) []SolverError {
    return c.annoying(DNFmkDeep(sub, Positive), CNFmkDeep(super, Negative), seen)
}
```

**(c) The `annoying` worklist (MLstruct's `annoying`/`annoyingImpl`).** Solves
`DNF <: CNF` by generating every implied `conjunct <: disjunct` and threading two
accumulators (`done_ls LhsNf`, `done_rs RhsNf`). Its moves:

- a **leading positive variable** in a conjunct: don't decompose — record the rest
  of the conjunct as a *negated* bound on the variable (`rec(v, mkRhs(rest), ...)`).
  This is how negation enters a variable's bound list **without** guessing, and it
  reuses the existing variable arm in (a).
- **union on the left / intersection on the right**: flatten into the accumulators.
- **`NegationType`**: move the term across the `<:` to the other side (negate and
  swap), the dual-field move from `Conjunct.neg`.
- **`⊥` on the left or `⊤` on the right**: discharged true.
- the **base case** (`done_ls <: done_rs` with everything decomposed): the actual
  structural decision — class-tag-vs-parent through the M5 graph, func-vs-func and
  field-vs-field recursing back into `constrain` (a), else a
  `CannotConstrainError`.

**Where the seam is, exactly.** In `constrain`, the union-super and
intersection-sub exists arms call `constrainNF` instead of opening a `probe`. The
"for all" arms, the structural switch, and the variable arms are untouched. The
`seen` cache needs no change — it already keys arbitrary `(sub, super)` pairs,
which is precisely the coinductive keying `annoying` needs because normalization
routes cycles through composed types.

**Tie-in to `simple_sub`'s centralization.** M6 PR2.5 consolidates the four
trial-and-commit sites (`resolveOverload`, the `IntersectionType`-sub arm, the
union-super exists rule, `constrainAssign`) behind one shared helper. That helper
is the single call site that flips from "probe-trial" to "`constrainNF`." Doing
the consolidation during the M-series (as planned) is what makes this graft a
one-seam change rather than four scattered rewrites.

---

## 4. Extrusion, levels, and the polarity-flipping visitor

`extrude` / `extruder` ([constrain.go:695](../../internal/solver/constrain.go))
and the level machinery are **unchanged in logic**. They are implemented on top of
the per-node `Accept` methods in
[`visitor.go`](../../internal/soltype/visitor.go), where each node visits its
children at the right polarity — `FuncType.Accept` already visits its params
contravariantly (`acceptParams(cur.Params, v, pol)`, "params contravariant") and
its return covariantly. The only addition is a `NegationType.Accept` that visits
its child at **flipped polarity**, mirroring that pattern:

```go
// ¬T is contravariant in T, so visit Inner at the opposite polarity — the same
// EnterType / descend / ExitType shape every node follows, with the one twist
// that the child is visited at pol.Flip(). UnionType/IntersectionType visit their
// members covariantly; NegationType is the one node that inverts.
func (t *NegationType) Accept(v TypeVisitor, pol Polarity) Type {
    e := v.EnterType(t, pol)
    if e.SkipChildren {
        return v.ExitType(skipReplace(t, e), pol)
    }
    cur := descendReplacement(t, e)
    inner := cur.Inner.Accept(v, pol.Flip()) // ¬ flips polarity
    out := cur
    if inner != cur.Inner {
        out = &NegationType{Inner: inner}
    }
    return v.ExitType(out, pol)
}
```

Because `coalesce`, `extrude`, and `freshenAbove` all ride on `Accept`, this one
method threads negation through all three. No bespoke recursion.

---

## 5. Coalescing and the readability pass

[`coalesce.go`](../../internal/solver/coalesce.go) inlines each variable's bounds
to a union (positive) or intersection (negative). Two additions:

- **A `NegationType` arm in the coalescer** so a negated bound renders as `¬T`.
  Mechanically it is the visitor arm in §4; `coalesceScheme`'s occurrence /
  single-polarity logic is unchanged.
- **The disjointness-aware negation simplifier** — the caveat #2 mitigation. It
  runs in [`simplify.go`](../../internal/solver/simplify.go), *after* coalescing,
  beside the existing co-occurrence merging. It applies `T ∩ ¬U → T` when `T` and
  `U` are provably disjoint, using the M5 class-tag and M6 literal/primitive
  disjointness facts, and reuses `lattice.go`'s `subsumeMembers` / `compareType`.
  Keeping it in the post-coalesce display layer (never in `constrain`) is what
  makes readability tunable and solver-swap-safe.

The binding-based-narrowing advantage (fresh binding per refinement, simplified
and frozen — [02-caveats-and-mitigations.md](02-caveats-and-mitigations.md) §2)
means this simplifier's input is small and local, which is most of what keeps
displayed types readable.

---

## 6. Nominal class tags (depends on `simple_sub` M5)

MLstruct's nominal layer is `ClassTag` carrying a parent set, with `glb` returning
"no greatest lower bound" for unrelated classes. Escalier's M5 already builds the
**declared-subtype graph** (`simple_sub` M5) over `class` declarations and their
`Extends` / `Implements`, plus per-type-parameter variance inferred from polarity.
The graft reuses that graph as the `glb` / parent oracle in `solver/normal.go`'s
`LhsNf` base slot:

- `C & D` for unrelated classes ⟹ no glb ⟹ conjunct is `never`, dropped before
  structural work (the caveat #1 fast path).
- `C <: P` ⟹ a parent-set / graph reachability check, the same one M5's nominal
  `constrain` arm already performs.
- exhaustive `match` over a tag and its negation falls out of the algebra — the
  extensible-variant story MLstruct advertises — composing with M5's `final`-driven
  instance exactness and M6's union exactness for the existing exhaustiveness
  payoff.

No new nominal machinery; the normal-forms layer consumes the M5 graph.

---

## 7. What this graft deliberately does NOT change

- The `soltype` surface representation and the `Info` side table. Normal forms are
  solver-internal and transient.
- The variable / bound / level / extrude core. MLstruct keeps it verbatim; so do
  we.
- Binding-based narrowing. Negation here serves the *internal* solver and any
  future `Not<T>` annotation — not a switch to flow narrowing
  ([00-overview.md](00-overview.md)).
- The exactness model. Exact-by-default formers and the one-way `exact <: inexact`
  rule (`simple_sub` M3–M6) are orthogonal to the Boolean algebra and stay as-is;
  the normal-forms `tryMerge` discipline must carry the `Inexact` flag through
  unchanged, exactly as `coalesce` / `extrude` already do.
