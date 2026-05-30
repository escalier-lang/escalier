# 03 — References & background

Background reading and a precise description of the type **lattice** our
extension of Simple-sub is built on. This is the conceptual foundation for the
plan; the spike in [`internal/checker/simplesub/`](../../internal/checker/simplesub/)
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

```
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

The ordering is **"outlives"**: `'a ≤ 'b` ⟺ `'a` outlives `'b` (a longer-lived
value is usable wherever a shorter-lived one is expected — references are
covariant in their lifetime).

| Lattice notion | Lifetime meaning |
|---|---|
| order `≤` | "outlives" |
| join `'a ⊔ 'b` | `LifetimeUnion` `('a \| 'b)` — value may carry either lifetime |
| top `⊤` | `'static` — outlives everything |
| bottom `⊥` | a maximally-short / fresh lifetime |

A `LifetimeVar` carries lower/upper bound lists and coalesces by join/meet
**identically** to a type variable — only the lattice differs. This symmetry is
the entire reason "lifetimes as a second sort" works:

- returning a borrowed parameter ⇒ `constrain(lifetime(param) <: lifetime(result))`;
- a multi-source return ⇒ the result lifetime is the **join** of the sources,
  coalescing to `('a | 'b)`;
- a value escaping to module/static storage ⇒ `constrain(lifetime <: 'static)`,
  coalescing to `'static` (top absorbs);
- a parameter-only lifetime that connects nothing is **elided** — the
  lifetime-sort analogue of single-polarity elimination.

### How the two lattices compose

A type and its lifetime are solved in parallel over their respective lattices,
joined at the types that *carry* a lifetime (records, tuples, type-refs/aliases,
through `mut`). `mut` is **invariant** in both its contents and its lifetime,
encoded via the read/write decomposition (covariant read view + contravariant
write view) — invariance is not native to a co/contravariant lattice, so it is
the one place we deliberately step outside the clean lattice structure.

### Beyond a plain lattice (for context)

- **Boolean algebra (negation):** adding negation types `¬T` (MLstruct) turns the
  subtype lattice into a Boolean algebra (complements + distributivity), enabling
  narrowing. Out of scope for v1; noted for the type-operator/narrowing future.
- **Nominal types** break pure structural ordering; they are layered on as atomic
  lattice elements with an explicit declared-subtype relation feeding `constrain`.
- **Recursive types** are not lattice elements per se; they are handled
  coinductively (Amadio-Cardelli seen-set) with a finite μ-knot representation,
  plus a depth budget for the genuinely non-regular (Turing-complete) residue.

## One-line summary

The lattice is the subtype-ordered space of types (`never` ⊥, `unknown` ⊤, union
= join, intersection = meet); Simple-sub is "collect ordering constraints, then
compute joins and meets in it," and **our extension adds a second lattice ordered
by 'outlives' (`'static` = ⊤) so lifetimes are solved by the very same
machinery.**
