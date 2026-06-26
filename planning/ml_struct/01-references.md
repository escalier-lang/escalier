# 01 — References & background

Background reading for the MLstruct adoption, plus the semantic-subtyping line
that was researched as the alternative and rejected for Escalier (see
[00-overview.md](00-overview.md) and the conversation that produced these docs).

> **Sourcing caveat.** The research behind these docs was done in an environment
> whose egress policy blocked direct PDF fetches from academic hosts
> (`arxiv.org`, `dl.acm.org`, `irif.fr`, `hkust-taco.github.io`, and others).
> Bibliographic details, abstracts, and the MLscript *source* (reachable on
> GitHub raw) were verified; the exact theorem numbers and formal rule figures in
> the papers were summarized from abstracts, talks, the reference implementation,
> and the POPL 2026 follow-up rather than quoted from the papers' formal sections.
> Pull the PDFs from an unblocked network before relying on any page-exact claim.

## MLstruct — the approach being adopted

- **Lionel Parreaux & Chun Yin Chau — *MLstruct: Principal Type Inference in a
  Boolean Algebra of Structural Types* (OOPSLA 2022).** The paper. Lifts
  Simple-sub's polarity restriction to full union / intersection / negation in any
  position while keeping principal inference, via DNF/CNF normalization plus
  nominal class tags.
  - Project page (PDF, video, DOI): <https://cse.hkust.edu.hk/~parreaux/publication/oopsla22a/>
  - Conference listing: <https://2022.splashcon.org/details/splash-2022-oopsla/41/MLstruct-Principal-Type-Inference-in-a-Boolean-Algebra-of-Structural-Types>
  - DOI: <https://dl.acm.org/doi/10.1145/3563304>

- **Chun Yin Chau & Lionel Parreaux — *The Simple Essence of Boolean-Algebraic
  Subtyping* (POPL 2026).** The follow-up. Re-proves soundness **semantically**
  (the original OOPSLA proof is acknowledged as very hard to follow), gives a
  cleaner subtyping decision procedure, and states the complexity result
  (subtyping is NP-hard even without recursive types). **Implement from this, not
  the 2022 paper** — it is the better foundation.
  - Project page: <https://cse.hkust.edu.hk/~parreaux/publication/popl26/>
  - DOI: <https://doi.org/10.1145/3776689>

- **MLscript — reference implementation (Scala).** The Boolean-algebraic core of
  MLscript, matching the paper. The grafting sketch in
  [03-graft-sketch.md](03-graft-sketch.md) is grounded in these files.
  - Repo: <https://github.com/hkust-taco/mlstruct> (the `mlstruct` branch)
  - Web demo: <https://hkust-taco.github.io/mlstruct>
  - Key source read:
    `shared/src/main/scala/mlscript/NormalForms.scala` (the DNF/CNF layer),
    `ConstraintSolver.scala` (`rec` / `goToWork` / `constrainDNF` / `annoying`),
    `TyperDatatypes.scala` (`ComposedType`, `NegType`, `ClassTag`),
    `Typer.scala` (how `CaseOf` produces negation), and `TypeSimplifier.scala`
    (the readability pass).

## Lineage — Simple-sub and MLsub

Already cited in [`../simple_sub/03-references.md`](../simple_sub/03-references.md);
repeated here so this directory is self-contained.

- **Lionel Parreaux — *The Simple Essence of Algebraic Subtyping* (ICFP 2020,
  functional pearl).** The algorithm `solver/` implements. MLstruct is its direct
  extension. <https://dl.acm.org/doi/10.1145/3409006> — blog walkthrough:
  <https://lptk.github.io/programming/2020/03/26/demystifying-mlsub.html>
- **Dolan & Mycroft — *Polymorphism, Subtyping, and Type Inference in MLsub*
  (POPL 2017)**, and Stephen Dolan, *Algebraic Subtyping* (PhD thesis). The
  original algebraic-subtyping work Simple-sub simplifies.
  <https://dl.acm.org/doi/10.1145/3009837.3009882>
- **Tix — a Simple-sub + negation-types checker/LSP for Nix.** A worked example of
  algebraic subtyping with negation in a real tool.
  <https://johns.codes/blog/making-a-type-checker-lsp-for-nix>

## The alternative: semantic subtyping (Castagna / CDuce line)

Researched as the competing approach and **not** chosen — it gives a cleaner
set-theoretic model but is a subtyping *checker*, not a principal-inference engine,
and adopting it for inference means taking on tallying and giving up either
principality, polymorphism, or arrow-intersection inference. Recorded here so the
comparison is preserved.

- **Frisch, Castagna & Benzaken — *Semantic Subtyping: Dealing Set-Theoretically
  with Function, Union, Intersection, and Negation Types* (JACM 2008).** The
  foundational paper. Types as sets of values; subtyping `s <: t` decided as
  emptiness of `s ∧ ¬t` via DNF decomposition per type constructor.
  <https://dl.acm.org/doi/10.1145/1391289.1391293> —
  author PDF: <https://www.irif.fr/~gc/papers/semantic_subtyping.pdf>
- **Castagna & Frisch — *A Gentle Introduction to Semantic Subtyping* (ICALP/PPDP
  2005).** The most approachable exposition.
  <https://www.irif.fr/~gc/papers/icalp-ppdp05.pdf>
- **Castagna, Nguyễn, Xu & Abate — *Polymorphic Functions with Set-Theoretic
  Types, Part 2: Local Type Inference and Type Reconstruction* (POPL 2015).**
  Introduces **tallying** — the subtyping-constrained analogue of unification.
  Tallying yields a *principal finite set* of substitutions, not a single
  principal type. <https://www.irif.fr/~gc/papers/polydeuces-part2.pdf>
- **Castagna — *Programming with Union, Intersection, and Negation Types*
  (2022/2024 survey).** States the trade-off triangle: implemented set-theoretic
  systems each give up one of {type reconstruction, arrow-intersection inference,
  parametric polymorphism}. <https://www.irif.fr/~gc/papers/set-theoretic-types-2022.pdf>
  / <https://arxiv.org/abs/2111.03354>
- **Castagna, Laurent & Nguyễn — *Polymorphic Type Inference for Dynamic
  Languages* (POPL 2024).** The state of the art for set-theoretic-type inference:
  Hindley-Milner + intersection introduction + union elimination, with a
  reconstruction algorithm proved sound and terminating (not complete). Uses
  tallying inside an Algorithm-W loop. <https://www.irif.fr/~gc/papers/dynlang.pdf>
  / <https://arxiv.org/abs/2311.10426>
- **Gesbert, Genevès & Layaïda — *A Logical Approach to Deciding Semantic
  Subtyping* (TOPLAS 2015).** Establishes the EXPTIME bound (and
  EXPTIME-completeness of the underlying tree-automata inclusion) for polymorphic
  semantic subtyping. <https://tyrex.inria.fr/publications/toplas15.pdf>
- **Castagna, Duboc & Valim — *The Design Principles of the Elixir Type System*
  (Programming Journal 2024).** A real-world deployment of set-theoretic types
  with gradual typing, strong arrows, and guard analysis — the closest production
  comparator. <https://arxiv.org/abs/2306.06391>

### Why semantic subtyping was not chosen (one paragraph)

It decides subtyping beautifully — `s <: t` iff `s ∧ ¬t` is empty — but
classically gives **checking, not principal inference**. Inference needs tallying,
which returns a principal finite *set* of substitutions rather than a single
principal type, and full reconstruction with intersections *and* polymorphism is
the open/restricted frontier (the trade-off triangle). Subtyping is
EXPTIME-complete and production implementations (CDuce, Elixir) represent
person-years. Escalier is already a Simple-sub compiler that values
annotation-free principal inference; MLstruct continues that exact architecture,
whereas semantic subtyping would discard it to chase an inference story that is
not solved for the full feature set. See [00-overview.md](00-overview.md).
