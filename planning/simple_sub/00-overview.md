# 00 — Overview

## Context

Escalier's type checker (`internal/checker/`, ~32k LoC of non-test code) is
built on Hindley-Milner-style **unification**: a type variable is a single
mutable cell (`type_system.TypeVarType` with one `Instance` + one `Constraint`),
and inference threads through `Unify`/`unifyInner`. Over time this core has
accreted a lot of hand-rolled machinery to approximate subtyping — `Widenable`
type-var widening, `Open` objects that grow during unification, the
`ArrayConstraint` apparatus, intersection-over-union distribution, and a
separate multi-phase lifetime analysis (`infer_lifetime.go`, ~2k LoC).

A spike in [`internal/simplesub/`](../../internal/simplesub/)
validated that **algebraic subtyping** (Simple-sub) reproduces Escalier-shaped
inferences while dissolving most of that machinery into one mechanism:
type variables carry lower/upper **bound lists**, the primitive is
`constrain(lhs <: rhs)`, and **polarity-driven coalescing** turns bounds into
unions/intersections. The spike covered functions + let-polymorphism, records +
usage-based inference, `mut` invariance (via the read/write decomposition),
lifetimes as a **second sort** over an outlives lattice, Baseline-D + residual
(Design A) type-level operators, recursive types (cycle cache + budget, plus a
level-2 regularity check), and a lazy/coinductive subtyping variant. Its
differential harness landed at **10 match / 2 benign / 0 regression** against
the production checker, where the two benign cases are an *improvement*
(principled `unknown` vs. a vacuous `<T0>`).

This plan turns that spike into a production checker.

## Goals

1. Replace the unification core with algebraic subtyping, with a **clean** type
   representation (bound-list type variables) rather than retrofitting
   `type_system.TypeVarType`.
2. **Improve** type-checking behaviour where we can; do not chase bug-for-bug
   compatibility with the old checker.
3. Keep **lifetimes** first-class, integrated into the core types rather than a
   separate phase.
4. Build a **comprehensive, checker-agnostic conformance corpus** (a long-wanted
   improvement to the fixtures) that encodes the language semantics we want.
5. Migrate **reversibly**: at every step before the final flip, the old checker
   still works and the new one is opt-in.

## Non-goals (for the MVP)

- Codegen / `.d.ts` emission from the new checker — **deferred entirely**. The
  MVP is a pure type-checker. `.d.ts`/JS keep running on the old checker.
- LSP on the new checker — **deferred**. It may lag the CLI and switch once the
  new checker is the default.
- Bug-for-bug parity with the old checker.

(Type-level operators — `keyof`, conditional, indexed access — **are** in the
MVP, as the final milestone M8; the spike already proved them.)

## Strategic decisions (settled)

| Decision | Choice | Rationale |
|---|---|---|
| Build location | New top-level package **sibling to `internal/checker/`** (e.g. `internal/solver/` — leaf name TBD) | Reversible; differential-testable; delete the old `internal/checker/` package at the end. |
| Type representation | Own `soltype` package, bound-list type vars | Clean algorithm-shaped data model; the whole reason not to reuse `type_system`. |
| AST coupling | **Untouched** — side table (`Info`), option (a) | No AST generics; old checker undisturbed; AST becomes type-system-agnostic at cleanup. |
| Compatibility | Improve, don't match | Corpus encodes language semantics; improvements are blessed. |
| Lifetimes | In the core from the start | Introduced with the first lifetime-carrying type (records); lifetimes ride on values. |
| Codegen / LSP | Deferred | The MVP is pure checking; biggest integration cost (codegen's `type_system` use) is paid later. |

## Boundary analysis (why this is tractable)

The integration surface is small and the reuse boundary is clean:

- **Compiler entry points: 3.** `internal/compiler/compiler.go` reaches the
  checker through exactly three `checker.NewChecker(ctx)` sites (`CheckLib`,
  `Compile`, `CompilePackage`). A flag selects old vs. new there.
- **AST coupling: ~2 lines.** `internal/ast/ast.go` has
  `type Type = type_system.Type` plus a generated `inferredType` field +
  `InferredType()`/`SetInferredType()` on each node (from `tools/gen_ast`), and
  one `BindingOwner` field (a reverse dependency). The new checker uses **none**
  of these — it keeps types in its own `Info` side table.
- **Consumer surface of `InferredType()`:** ~7 writes (all in the old checker),
  ~62 reads (old checker + ~6 in the LSP). The new checker bypasses all of them.
- **Reusable as-is:** `parser`, `ast`, `resolver`, `dep_graph`, `set`,
  `provenance`, `liveness`, `interop`. These are upstream of or orthogonal to
  inference.
- **Deferred boundary (the real cost of not reusing `type_system`):** codegen
  consumes `type_system` in ~4 files / ~30 refs, concentrated in `dts.go`
  (`.d.ts` emission). Because codegen is deferred, this cost is not paid in the
  MVP; a `soltype → type_system` bridge (or a port of codegen) is a later
  decision.

## Migration shape (reversible until the flip)

```text
parser ──► *ast.Module ──┬──► old checker ─► type_system.Scope ──► codegen / LSP  (unchanged)
 (parse once)            │
                         └──► new checker ─► soltype.Scope + Info  ─► new test suite + differential harness
```

1. Build the new checker in its own package; **no AST, codegen, or LSP changes.**
2. Drive it from real `*ast.Module` via the existing `dep_graph`/`resolver`.
3. Validate with a parallel test suite + a checker-agnostic conformance corpus.
4. Differential harness: parse once, run both checkers, triage divergences.
5. Flip the default at the 3 compiler sites → retire the old checker → delete the
   AST `inferredType` field/alias → (later) port codegen off `type_system`.
