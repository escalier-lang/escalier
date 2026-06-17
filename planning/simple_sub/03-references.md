# 03 — References & background

Background reading and a precise description of the type **lattice** our
extension of Simple-sub is built on. This is the conceptual foundation for the
plan; the spike in [`internal/simplesub/`](../../internal/simplesub/)
is its concrete realization.

## Primary sources

- **Lionel Parreaux — *The Simple Essence of Algebraic Subtyping* (blog
  walkthrough).** The most approachable introduction to the Simple-sub
  algorithm we are adopting; explains type variables with lower/upper bounds,
  `constrain`, and polarity-driven coalescing.
  <https://lptk.github.io/programming/2020/03/26/demystifying-mlsub.html>

- **Lionel Parreaux — *The Simple Essence of Algebraic Subtyping: Principal
  Type Inference with Subtyping Made Easy* (ICFP 2020, functional pearl).** The
  paper. Reference for the algorithm's correctness, the bound representation,
  simplification, and coalescing.
  <https://dl.acm.org/doi/epdf/10.1145/3409006>

### Further background (not required, but relevant)

- Stephen Dolan, *Algebraic Subtyping* (PhD thesis) and Dolan & Mycroft,
  *Polymorphism, Subtyping, and Type Inference in MLsub* (POPL 2017) — the
  original algebraic-subtyping / MLsub work that Simple-sub simplifies.
- MLstruct (Parreaux et al.) — extends the lattice to a **Boolean algebra** with
  **negation types**, the basis for principled narrowing (`x is not null`,
  `builtins.is<T>`). Relevant if we later add negation-based narrowing.
- Amadio & Cardelli, *Subtyping Recursive Types* — the coinductive ("seen-set")
  subtyping used for recursive types (spike `lazy.go`).
- Rémy-style **levels/ranks** for let-generalization (spike `scheme.go`).

> Note on the article that started this investigation: "Tix", a Nix
> type-checker/LSP, is built on Simple-sub + negation types
> (<https://johns.codes/blog/making-a-type-checker-lsp-for-nix>). It is what
> prompted evaluating algebraic subtyping for Escalier.

## The lattice

Algebraic subtyping is organized around a **lattice**: a set with an ordering
`≤` in which any two elements have a unique **join** (least upper bound, `⊔`)
and a unique **meet** (greatest lower bound, `⊓`). A **bounded** lattice also
has a **top** (`⊤`) above everything and a **bottom** (`⊥`) below everything.

Simple-sub's whole computation is: collect ordering constraints as bounds on
variables, then **coalesce** each variable by taking joins/meets in this
lattice. So getting the lattice right *is* getting the type system right.

### Lattice 1 — the subtype lattice (types)

The ordering is the subtype relation: **`A ≤ B` ⟺ `A <: B`** ("A is assignable
to B"; A is more specific, lower in the lattice).

| Lattice notion | Type-system meaning |
|---|---|
| order `≤` | subtyping `<:` |
| join `A ⊔ B` | **union** `A \| B` (least type both are subtypes of) |
| meet `A ⊓ B` | **intersection** `A & B` (greatest type that is a subtype of both) |
| top `⊤` | `unknown` (everything `<: unknown`) |
| bottom `⊥` | `never` (`never <:` everything) |

```text
            unknown            ⊤
          /    |    \
     number  string  {x:number} ...
          \    |    /
            never              ⊥
```

This is why unions/intersections are **not special-cased** in Simple-sub — they
are the join/meet of the lattice, produced by coalescing:

- a variable in **positive** (output/covariant) position coalesces to the
  **join of its lower bounds** → a **union**;
- a variable in **negative** (input/contravariant) position coalesces to the
  **meet of its upper bounds** → an **intersection**.

Corollaries the plan relies on:

- The **empty join is `never`** and the **empty meet is `unknown`**. A
  parameter-only variable (no lower bounds) thus coalesces, in negative position,
  to `unknown` — this is the principled `unknown`-vs-vacuous-`<T0>` improvement.
- **Coalescing is deterministic** because join and meet are unique — the defining
  property of a lattice.

### Lattice 2 — the lifetime ("outlives") lattice — *our extension*

Simple-sub as published is purely structural; it has no notion of lifetimes.
**Our extension treats lifetimes as a second sort with its own lattice**, solved
by the *same* constraint machinery (the spike proved this collapses Escalier's
multi-phase `infer_lifetime.go` into ordinary constraint solving).

The ordering `<:` is **"outlives"**: `'a <: 'b` ⟺ `'a` outlives `'b`
(equivalently, `'a` lives at least as long as `'b`). This is the relation
`constrainLt` asserts. So a **longer-lived lifetime is a lesser element**, and
`'static` — which outlives everything — is the **bottom**: `'static <: X` holds
for every `X`, while `X <: 'static` holds only for `X = 'static` and is therefore
the forcing escape constraint. It is consistent with references being covariant
in their lifetime — a longer-lived value can stand in where a shorter-lived one
is expected, so `'static` is the universal subtype and sits at the bottom.

| Lattice notion | Lifetime meaning |
|---|---|
| order `<:` | "outlives" (`'a <: 'b` ⟺ `'a` outlives `'b`) |
| join `'a ⊔ 'b` | arises in **positive position** from a multi-source borrow (e.g. an `if` returning either `&'a T` or `&'b T`); the result carries one of the source lifetimes and is valid only within the span where both are still alive |
| meet `'a ⊓ 'b` | arises in **negative position** from a borrow with multiple upper bounds (e.g. a borrow that must fit within both context `'a` and context `'b`); the borrow must be valid in every context that uses it |
| bottom `⊥` | `'static` — outlives everything, so `'static <: X` for every lifetime `X` |
| top `⊤` | a maximally-short / fresh lifetime |

The lattice formally has both operations and the spike computes both —
positive-position coalescing joins lower bounds, negative-position coalescing
meets upper bounds (see `LifetimeVar` in
[internal/simplesub/lifetime.go](../../internal/simplesub/lifetime.go)). The
spike's *surface representation*, however, uses a single `LifetimeUnion` for
both: the underlying value is a list of param lifetimes, and the polarity at
which the list was collected determines whether it's read as a join (each
member is a possible carrier) or a meet (each member is a context the borrow
must fit within). A future production representation could split these into
`LifetimeUnion`/`LifetimeIntersection` for clarity, but the spike intentionally
unified them since the list of lifetimes is the same in both directions.

Strictly speaking, the rendered `'a | 'b` is a **flow-set**, not a literal
lattice element — it names the source lifetimes that fed into the variable.
For a positive-position result this is operationally indistinguishable from
the LUB (the caller still must consume the result within the intersection of
the source spans), and for a negative-position constraint it's the bound list
itself. The flow-set framing is what makes a single representation work for
both polarities.

```text
         (fresh/short)         ⊤
          /    |    \
       'a     'b    'c   ...     (concrete borrows)
          \    |    /
            'static            ⊥   (outlives everything)
```

A `LifetimeVar` carries lower/upper bound lists and coalesces by join/meet
**identically** to a type variable — only the lattice differs. This symmetry is
the entire reason "lifetimes as a second sort" works:

- returning a borrowed parameter ⇒ `constrain(lifetime(param) <: lifetime(result))`;
- a multi-source return ⇒ the result lifetime is the **join** of the sources,
  coalescing to `('a | 'b)`;
- a value escaping to module/static storage ⇒ `constrain(lifetime <: 'static)`,
  coalescing to `'static` (the bottom absorbs the meet);
- a parameter-only lifetime that connects nothing is **elided** — the
  lifetime-sort analogue of single-polarity elimination.

### How the two lattices compose

A type and its lifetime are solved in parallel over their respective lattices,
joined at the `Ref` wrapper — a single unified node that carries both the
mutability flag and the (nilable) lifetime around a borrowed value (see
[02-design-notes.md](02-design-notes.md) §"`soltype` — the type representation").
`Ref` is **invariant in its inner type when the wrapper is mutable**, encoded
via the read/write decomposition (covariant read view + contravariant write
view); invariance is not native to a co/contravariant lattice, so it is the
one place we deliberately step outside the clean lattice structure. The write
view is **per named field**: it pins the fields the target names invariantly but
ranges over those fields only, so an **inexact** mutable target stays
width-tolerant (`mut {x, y} <: mut {x, ...}` holds — `x` is invariant, `y` is
hidden and not writable through the target). This is what lets a field write,
which lowers to an inexact `mut {field, ...}` requirement, apply to a
concretely-typed mutable receiver.
**Lifetimes themselves are always covariant**: a `Ref`'s lifetime field is
constrained once in the subtype direction (longer-lived can stand in for
shorter-lived), regardless of whether the wrapper is mutable. Because the
lifetime lives on the wrapper rather than inside the inner type, the
mutability-driven bidirectional sweep over the inner cannot accidentally
double-emit the lifetime constraint into both directions — lifetime
covariance is structural, not a special case.

### Beyond a plain lattice (for context)

- **Boolean algebra (negation):** adding negation types `¬T` (MLstruct) turns the
  subtype lattice into a Boolean algebra (complements + distributivity), enabling
  narrowing. Out of scope for the MVP; noted for the narrowing future.
- **Nominal types** break pure structural ordering; they are layered on as atomic
  lattice elements with an explicit declared-subtype relation feeding `constrain`.
- **Recursive types** are not lattice elements per se; they are handled
  coinductively (Amadio-Cardelli seen-set) with a finite μ-knot representation,
  plus a depth budget for the genuinely non-regular (Turing-complete) residue.

## One-line summary

The lattice is the subtype-ordered space of types (`never` ⊥, `unknown` ⊤, union
= join, intersection = meet); Simple-sub is "collect ordering constraints, then
compute joins and meets in it," and **our extension adds a second lattice over
lifetimes ordered by `<:` = "outlives" so that longer-lived is lesser (`'static`
= ⊥, the universal subtype) — so lifetimes are solved by the very same machinery.**
