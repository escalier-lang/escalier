# SimpleSub type checker — implementation plan

This directory holds the plan for replacing Escalier's unification-based type
checker (`internal/checker/`) with one based on **algebraic subtyping**
(Parreaux's *Simple-sub*, extended with lifetimes as a second sort). The
approach was de-risked by a working spike in
[`internal/simplesub/`](../../internal/simplesub/) that proved
out the algorithm against Escalier's actual semantics (function/record/`mut`
inference, lifetimes-as-a-second-sort, type-level operators, recursive types).

## Documents

- **[00-overview.md](00-overview.md)** — context, goals, strategic decisions,
  and the boundary analysis that makes this tractable.
- **[01-milestones.md](01-milestones.md)** — the ordered milestones with
  acceptance criteria and go/no-go gates.
- **[02-design-notes.md](02-design-notes.md)** — concrete shapes: the `soltype`
  package, the `Info` side table, the constraint-generating AST walk, the
  conformance-corpus format, and the differential harness.
- **[03-references.md](03-references.md)** — background reading (Simple-sub /
  algebraic subtyping) and a precise description of the **lattice** our
  extension is built on (the subtype lattice + the lifetime "outlives" lattice).

## TL;DR of the strategy

- **Parallel package**, not an in-place rewrite. The new checker lives beside the
  old one and is selected by a flag at the compiler's three `NewChecker` sites.
- **Its own type representation** (`soltype`, with bound-list type variables);
  it does **not** reuse `internal/type_system/`.
- **The AST is left untouched.** The new checker keeps node→type associations in
  a side table (`Info`, à la `go/types.Info`) and never calls the AST's
  `InferredType()`/`SetInferredType()`. The embedded `inferredType` field stays
  for the old checker and is deleted only at end-of-migration cleanup.
- **Improve, don't match.** The conformance corpus encodes the *language*
  semantics we want; divergences from the old checker that are improvements
  (e.g. `unknown` instead of a vacuous `<T0>`) are blessed, not chased.
- **Lifetimes are baked into the core**, introduced with the first
  lifetime-carrying type (records), not bolted on afterward.
- **Codegen and the LSP are deferred** — the MVP is a pure type-checker (errors +
  inferred types), validated by its own test suite. Codegen/`.d.ts` and the LSP
  keep running on the old checker until the new one is the default.
