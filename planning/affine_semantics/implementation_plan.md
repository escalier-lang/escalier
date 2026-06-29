# Implementation plan: move / affine semantics

This is the implementation plan for the requirements in
[requirements.md](./requirements.md). It sequences the work into
dependency-ordered pull requests, each sized to be reviewable on its own.

## Scope and constraints

- **New checker only.** All semantic work lands in the SimpleSub-based checker,
  `internal/solver` and `internal/soltype`. The old HM-based checker in
  `internal/checker` keeps its current behaviour and is not given affine
  semantics. The new solver is invoked only from its own tests today via
  `solver.InferModule` ([internal/solver/module.go](../../internal/solver/module.go)),
  so the work rides in solver tests and never touches the fixture corpus, which
  belongs to the old checker.
- **Parser parses borrows unconditionally.** The parser is shared by both checkers
  through one `ParseLibFiles` entry, and `&` already means intersection in
  type-annotation position. Prefix `&` is position-disambiguated from infix `&`, so
  the parser can read a prefix `&` as a borrow without a grammar mode: infix `&`
  stays intersection and is unaffected. The old checker keeps working through a
  graceful fallback rather than a parser flag, as described next.

## What the M4 substrate already provides

These exist, are tested, and are built on rather than rebuilt:

- `RefType{Mut, Lt, Inner}` and the four owned/borrowed quadrants —
  [internal/soltype/type.go](../../internal/soltype/type.go), `NewRef`/`UnwrapRef`
  in [internal/soltype/ref.go](../../internal/soltype/ref.go).
- The lifetime sort `LifetimeVar`/`StaticLifetime`/`LifetimeUnion` —
  [internal/soltype/lifetime.go](../../internal/soltype/lifetime.go).
- `constrainLt` and the outlives lattice with level extrusion, plus the
  `RefType <: RefType` rule where owned-into-borrow is free and borrow-into-owned
  is a `BorrowEscapeError` — [internal/solver/constrain.go](../../internal/solver/constrain.go).
- The mutability-transition checker, Rules 1/2/3, and the `'static`-escape query
  `borrowEscapedToStatic` — [internal/solver/transitions.go](../../internal/solver/transitions.go).
- Param borrow origination `attachParamLifetimes`, escape forcing via
  `constrainEscape`/`escapeVisitor`, and join lifetimes `joinBorrows` —
  [internal/solver/infer_expr.go](../../internal/solver/infer_expr.go).
- `val mut p` binding patterns already parse, via `IdentPat.Mutable` —
  [internal/ast/pattern.go](../../internal/ast/pattern.go).

What is missing: the `&` notation end to end, annotation-literal ownership, member
reads that borrow the receiver, and the whole move / use-after-move analysis. The
PRs below add these.

The move-engine work, PRs 1 through 8, builds only on this M4 substrate. The
type-former PRs, 9 and 10, additionally depend on **M6** of
[planning/simple_sub/01-milestones.md](../simple_sub/01-milestones.md), which lands
union and intersection formers together with union-scrutinee narrowing. PR 11, the
connected-component moves, is a further move-engine extension and stays on M4. PR 12 is
retired: because mutability is uniformly deep, the freeze/thaw binding moves (PR 8) and
the connected-component move (PR 11) already convert a whole graph between mutable and
immutable, so no `Freeze`/`Thaw` mapped type and no M9 dependency remain. PR 13, deep
uniform `mut` with `readonly`, is foundational mut-semantics work that stays on M4 and
is best sequenced early, since the affine model's mutability builds on it. PR 14
reshapes the PR 13 representation from an eager lowering to a lazy one that stores the
surface annotation, also on M4. So PRs 1 through 8, 11, 13, and 14 can proceed once M4
is in place, and 9 and 10 wait on M6.

## Parsing borrows without a syntax mode

There is no parser grammar mode. A prefix `&` is parsed as a borrow unconditionally,
and the old checker is kept working by a graceful fallback at its translation layer.
Three facts make this safe:

- **Infix `&` is untouched.** The only `&` form the existing corpus uses is infix
  intersection — `A & B`, `keyof T & U`, `typeof x & U` — and a prefix `&` is
  distinguished from it by position the way any Pratt parser separates prefix from
  infix. A `&` where an atom is expected is a borrow; a `&` between two parsed types
  is intersection.
- **The only changed behaviour is unused.** The single thing a prefix-borrow
  reinterpretation alters is the incidental leading-`&` skip at
  [internal/parser/type_ann.go](../../internal/parser/type_ann.go) lines 51-52,
  which mirrors the leading-`|` union sugar. No `.esc` fixture and no test string
  starts a type annotation with `&`, so reinterpreting a leading `&` as a borrow
  changes nothing in the corpus.
- **The old checker degrades cleanly.** It only meets a `RefTypeAnn` if borrow
  syntax appears in a file it parses, which does not happen for existing files. For
  any stray case, the non-panicking fallback in PR 1 turns it into a clean
  "borrows unsupported in the legacy checker" error rather than a crash, which is
  better than a silent misparse.

Everything else — the `mut` prefix, `'a` prefixes on type refs, lifetime
parameters, and `val mut` patterns — is already shared and stays common.

## Architecture decisions for the high-risk areas

These resolve the cross-cutting unknowns that several PRs depend on. They are the
result of a spike into the flow-analysis, narrowing, type-representation, and
grammar code, and should be read before starting PR 1.

### Where the move analysis lives

Move, use-after-move, conditional-move, and narrowing-scope checks are
flow-sensitive, but solver inference is a single ordered pass over statements in
source order, and a real CFG-based liveness analysis already exists. The decision
is to **interleave the move checks into the existing ordered walk**, hook them at
the same program-point granularity the mutability-transition checks already use,
reuse the CFG and liveness, and build one new piece: a branch-merged per-binding
*consumed* lattice.

Reused as-is:

- [internal/liveness](../../internal/liveness) builds a real CFG (`BuildCFG`) with
  blocks and edges for if/else, match arms, loops, and try/catch, and runs
  branch-merged backward liveness to a fixed point (`AnalyzeFunction`).
  `BuildStmtToRef` plus `LivenessInfo.IsLiveAfter(StmtRef, VarID)` answer "is this
  binding still live after point P," join-correct across branches. This is already
  wired onto the solver's per-function `funcCtx`
  ([internal/solver/infer.go](../../internal/solver/infer.go)) and consumed by the
  transition checker.
- The ordered walk exposes a program point via `c.fn.currentStmt` mapped through
  `stmtToRef` to a `StmtRef`. The transition checks already interleave here, so the
  move checks attach at the same points.

Built new:

- A per-binding consumed/moved lattice with joins at CFG merge blocks. Liveness is
  branch-merged, but the existing `AliasTracker`
  ([internal/liveness/alias.go](../../internal/liveness/alias.go)) is straight-line
  accumulate with a conservative multi-set over-approximation and has no
  per-program-point merge. Use-after-move needs "moved on some path" and "moved on
  all paths" state that joins at if/else, match, and loop merge points. This is the
  genuinely new analysis and the main cost of the move engine.

Rejected: a standalone CFG post-pass after inference. It would duplicate the
rename/VarID/StmtRef plumbing and re-derive types the ordered walk already holds on
`funcCtx`, for no benefit, since the walk already provides program-point ordering.

### Escape detection is generalized, not queried

An earlier framing said move detection is a query over existing escape constraints.
That is not true today and is corrected here. `constrainEscape`/`escapeVisitor`
([internal/solver/infer_expr.go](../../internal/solver/infer_expr.go)) force
reachable borrow lifetimes to `'static`, but they run at exactly one site, the
module-level global write. Returns, field and element stores, function arguments,
and closure captures do not run escape. The query `borrowEscapedToStatic`
([internal/solver/transitions.go](../../internal/solver/transitions.go)) also
inspects only the top-level `RefType`, so a borrow nested in a field or reached
through a usage-inferred type variable is missed, a known gap.

Decision: the move engine **generalizes escape detection to every value-flow-out
site**, and identifies consumption sites explicitly at returns, field and element
stores, owned-parameter arguments, and escaping closure captures. The nested and
type-variable escape gap is closed as part of this work, since a move that misses a
nested escape is unsound. This is why the move engine is split across PR 5 and PR 6.
PR 5 landed the consumed lattice and the `borrowEscapedToStatic` detection-gap
closure (#786, #787). PR 6 landed the consume at every flow site. The `constrainEscape`
forcing at the non-global flow sites was deferred past both, to PR 15, since the consume
and the forcing target different destination regions and were cleaner as separate
changes; see PR 15. The consume and use-after-move behaviour is PR 6's own scope.

### Narrowing comes from M6, not this plan

The discriminant pin needs union-scrutinee narrowing to build on, and the current
solver has none — neither `inferIfElse` nor `inferMatch`
([internal/solver/infer_expr.go](../../internal/solver/infer_expr.go)) refines the
scrutinee inside a branch or arm, and match binds only the pattern's leaf
projections through the member-lookup constraint path
([internal/solver/pattern.go](../../internal/solver/pattern.go)). But narrowing is a
planned deliverable of **M6** in
[planning/simple_sub/01-milestones.md](../simple_sub/01-milestones.md), which adds
union and intersection formers together with union-scrutinee narrowing to gate the
read-until-narrowed-union write site, including the trickier non-discriminated form
that narrows on a field's runtime type. Decision: the affine plan does not add
narrowing. PR 10 adds only the affine layer — a mutable narrowed binding `&mut A`
with the discriminant pinned for the arm — on top of M6's narrowing. The insertion
points are `inferMatch` where the arm scope is built and the `LitPat` discriminant
arm in `bindPatternWith`.

### Unions as RefInner is a behavioural change, not a fan-out

`RefType` is correctly not a `RefInner`
([internal/soltype/type.go](../../internal/soltype/type.go)), so nested-borrow
normalization is sound at the type level. Making `UnionType` and `IntersectionType`
into `RefInner` is two new marker methods, and there is no name-exhaustive switch
that forces a mechanical fan-out. The risk is the inverse: roughly seven
`.(RefInner)` assertions silently start accepting unions and intersections, so each
needs a behavioural review and a test, not a compile fix. Sites to review:

- the `RefType <: RefType` rule and the `bare <: RefType` arm in
  [internal/solver/constrain.go](../../internal/solver/constrain.go) — confirm
  invariance handling for a `mut (A | B)` inner, and that a bare union satisfies
  borrow-into.
- [internal/solver/widen.go](../../internal/solver/widen.go) and
  [internal/soltype/visitor.go](../../internal/soltype/visitor.go) — confirm a
  widened or rewritten union inner stays a `RefInner`.
- `resolveMutableTypeAnn` in
  [internal/solver/type_ann.go](../../internal/solver/type_ann.go) — `mut (A | B)`
  stops being rejected, which is the intended new surface.
- the `RefType` print arm in
  [internal/soltype/print.go](../../internal/soltype/print.go) — it already
  parenthesizes a looser inner, so `&(A | B)` renders correctly; add a snapshot.

### Prefix `&` binds tight, like `mut`

A leading `&` borrows the immediately following atom, not the whole annotation, so
`&A | B` is `(&A) | B` and a borrow of the union is written with explicit
parentheses, `&(A | B)`. This keeps `&` uniform with the prefixes that already
exist: `mut` and the lifetime prefix are parsed inside `primaryTypeAnn` and bind
tighter than intersection (precedence 4) and union (precedence 3), so `mut A | B` is
`(mut A) | B`. Decision: prefix `&` is parsed in `primaryTypeAnn` alongside `mut`,
consuming an optional lifetime and an optional `mut` and then the atom, so `&`,
`&mut`, `&'a`, and `&'a mut` are tight prefixes on one atom. To borrow a union,
intersection, or other compound, parenthesize it. In the printer the borrow-`&`
prefix prints at `precPrefix`, the same level as `mut`/lifetime, so `&(A | B)`
renders with the inner union parenthesized.

Disambiguation is by position, the standard prefix-versus-infix split: a `&` where
an atom is expected is a borrow of that atom, and a `&` between two parsed types is
intersection. Each string has one parse. A borrow as an intersection member needs no
parentheses; only borrowing a compound does.

### Param-default flip is sequenced with the move engine

PR 3 changes the parameter default from borrow-by-default — `attachParamLifetimes`
mints a lifetime for every reference parameter today — to bare-is-owned. Two
consequences. It churns existing solver snapshots that assert
`fn <'a>(p: mut 'a {x}) -> ...`, which assume params borrow. And a bare owned
parameter is consuming, but consuming has no enforcement until the move engine
lands. Decision: land the `&`-parameter and auto-borrow behaviour in PR 3 and update
the affected snapshots there, but defer making bare owned parameters actually
consume to PR 6, where the consuming-argument check and its tests live. Between PR 3
and PR 6 a bare owned parameter is owned in the type but not yet enforced as
consumed.

### Member-read borrows are scoped to rvalue reads of reference types

Rule 4 wraps a member read in a receiver-bounded borrow. To avoid destabilising
every member access, three limits apply. Only rvalue reads originate a borrow; an
assignment target `obj.f = x` is not a borrow-producing read. A read whose result is
a value type stays a value type, since primitives are excluded from `RefInner`, so
the wrap is conditional on the field being reference-shaped. The receiver-bounded
lifetime is elided in display when the read is consumed locally, so ordinary
`obj.f` code shows no lifetime, and only an escaping member read shows the receiver
lifetime. Chained access `a.b.c` composes the rule at each link.

---

## PR sequence

Status legend: ✅ done · 🚧 in progress · ⬜ not started.

| PR | Title | Depends on | Rough size | Status |
|----|-------|-----------|-----------|--------|
| 1 | `&` grammar, `RefTypeAnn` node, printer | — | Medium | ✅ done (#769) |
| 2 | Solver lowering of `&`, soltype `&` rendering, snapshot migration | 1 | Medium | ✅ done (#770) |
| 3 | Annotation-literal ownership, owned/borrow params, auto-borrow | 2 | Medium | ✅ done (#771) |
| 4 | Member reads borrow the receiver | 3 | Medium | ✅ done (#773) |
| 5 | Move engine substrate: generalize escape detection, build the consumed lattice | 4 | Large | ✅ done (#786, #787); constrainEscape generalization deferred to PR 15 |
| 6 | Consume and use-after-move at every flow site, conditional moves | 5 | Large | ✅ done; escaping-closure-capture moves deferred; return/argument `constrainEscape` forcing moved to PR 15 |
| 7 | Partial moves and field-level ownership | 6 | Medium | ✅ done (#791) |
| 8 | Immutable→mutable thaw move and borrow-phase framing | 6 | Medium | ✅ done |
| 9 | Unions/intersections as `RefInner`, mixed-ownership rejection, nested-borrow normalization | 3, M6 | Medium | ✅ done (#811) |
| 10 | Mutable narrowed binding with pinned discriminant | 8, 9, M6 | Medium | ⬜ not started |
| 11 | Connected-component moves for graphs | 6, 7, 15 | Large | ⬜ not started |
| 12 | ~~`Freeze`/`Thaw` utility types~~ — retired; subsumed by uniform deep `mut` + the freeze/thaw moves (PR 8, 11) | — | — | ❌ retired |
| 13 | Deep, uniform `mut` and `readonly` | 1, 2 | Medium | ✅ done (#777, #781) |
| 14 | Lazy deep `mut`: store the surface form, push the rule to access and constrain | 13 | Large | ✅ done (#780) |
| 15 | Escape forcing at returns, stores, and consuming arguments (deferred from PR 6) | 6 | Large | ✅ done (#814); element stores deferred to M7, field-granular escape to PR 11 |

### PR 1 — `&` grammar, `RefTypeAnn` node, printer

Goal: the parser can read `&{x}`, `&mut {x}`, `&'a {x}`, and `&'a mut {x}` and
round-trips them through the printer, with the old checker unaffected.

- Add the `ast.RefTypeAnn{Mut bool, Lifetime LifetimeAnnNode, Inner TypeAnn}`
  variant to the `TypeAnn` sum type
  ([internal/ast/type_ann.go](../../internal/ast/type_ann.go)), add it to
  `isTypeAnn()`, regenerate the AST via `gen_ast.go`, and update
  [internal/ast/visitor.go](../../internal/ast/visitor.go). `MethodReceiver`
  ([internal/ast/class.go](../../internal/ast/class.go)) is the field template:
  it already carries `Mut bool` plus `Lifetime LifetimeAnnNode`.
- Parse prefix `&`/`&mut`/`&'a`/`&'a mut` in `primaryTypeAnn` alongside the existing
  `mut` and lifetime prefixes, so `&` binds tight to one atom and `&A | B` is
  `(&A) | B`. A borrow of a union or intersection is written `&(A | B)`. This
  replaces the incidental leading-`&` skip. See "Prefix `&` binds tight, like `mut`"
  above. Reuse the existing `LifetimeAnn`/`LifetimeUnionAnn` nodes for the lifetime
  slot.
- Render `RefTypeAnn` in [internal/printer/printer.go](../../internal/printer/printer.go).
- Graceful fallback, which is the isolation mechanism in place of a parser mode:
  replace the `panic` default in the old checker's `inferTypeAnn`
  ([internal/checker/infer_type_ann.go](../../internal/checker/infer_type_ann.go))
  with a clean "borrows unsupported in the legacy checker" error returning
  `ErrorType`, so a `RefTypeAnn` can never crash the CLI or LSP. The old checker
  does not see `RefTypeAnn` in normal operation, since existing files contain no
  borrow syntax.

Tests: parser tests for each borrow form in
[internal/parser/type_ann_test.go](../../internal/parser/type_ann_test.go);
printer round-trip; a test asserting infix `A & B` still parses as intersection and
`&A` parses as a borrow distinguished by position; `&(A | B)` borrows the union
while `&A | B` is `(&A) | B`.

Acceptance: borrow forms parse and print; the full existing parser and old-checker
suites are green with no snapshot changes.

### PR 2 — Solver lowering of `&`, soltype `&` rendering, snapshot migration

Goal: the solver lowers `&`-annotations to `RefType` and renders every borrow in
`&` notation, replacing the old `mut 'a {x}` display.

- Add a `case *ast.RefTypeAnn` arm to `resolveTypeAnn`
  ([internal/solver/type_ann.go](../../internal/solver/type_ann.go)) producing
  `soltype.RefType{Mut, Lt, Inner}`. A `&` with no named lifetime mints a fresh
  inferred `LifetimeVar`; `&'a` resolves the named lifetime, which closes the
  currently-deferred named-lifetime path. A bare annotation stays owned:
  `{x}` lowers to owned-immutable and `mut {x}` to owned-mutable, which the
  existing `resolveMutableTypeAnn` already does.
- Change the `RefType` arm of [internal/soltype/print.go](../../internal/soltype/print.go)
  to render `&`, `&mut`, `&'a`, `&'a mut`, leaving owned as bare `{x}` / `mut {x}`.
  Show the lifetime name only when it is load-bearing, per the display rules; the
  `&` is always shown.
- Migrate solver snapshots from `mut 'a {x}` to `&'a mut {x}` with `UPDATE_SNAPS`.
  Watch [internal/solver/infer_borrow_lifetime_test.go](../../internal/solver/infer_borrow_lifetime_test.go)
  and [internal/soltype/print_test.go](../../internal/soltype/print_test.go).

Tests: lowering tests for each `&` form; print tests for owned vs borrowed and for
elided vs named lifetimes.

Acceptance: `&` round-trips annotation → soltype → display; no remaining
`mut 'a {x}`-style borrow output.

### PR 3 — Annotation-literal ownership, owned/borrow params, auto-borrow

Goal: implement inference rules 2 and 3 — the annotation is read literally, and
call sites auto-borrow.

- Rule 3: with PR 2's lowering, a bare annotation is owned and a `&` annotation is
  a borrow in every position. The binding initializer makes the move-or-borrow and
  mutability choice when `p` is an owned value:

  | | move (owned `q`) | borrow |
  |---|---|---|
  | **immutable `q`** | `val q = p` | `val q = &p` |
  | **mutable `q`** | `val mut q = p` | `val q = &mut p` |

  A `&mut` borrow requires an owned-mutable `p`, and the `&` forms infer `q`'s borrow
  type without repeating the pointee shape. The move forms establish owned bindings
  here, with the consuming enforcement and use-after-move landing in PR 6, as with the
  bare owned parameter.

  Each quadrant also has an annotated spelling that restates the pointee shape instead
  of inferring it:

  | | move (owned `q`) | borrow |
  |---|---|---|
  | **immutable `q`** | `val q: {x} = p` | `val q: &{x} = p` |
  | **mutable `q`** | `val q: mut {x} = p` | `val q: &mut {x} = p` |

  Add tests pinning each form in both tables.
- Borrow-expression syntax: `&p` and `&mut p` are the first `&` in expression position,
  so they need new surface syntax, unlike the type-position `&` of PR 1. Add an
  `ast.BorrowExpr{Mut bool, Arg Expr}` variant to the `Expr` sum type
  ([internal/ast/expr.go](../../internal/ast/expr.go)), add it to `isExpr()`, regenerate
  via `gen_ast.go`, and update [internal/ast/visitor.go](../../internal/ast/visitor.go).
  The existing `UnaryExpr` is the precedent for a prefix operator. Parse prefix `&`/`&mut`
  in the unary position of the expression parser and render it in
  [internal/printer/printer.go](../../internal/printer/printer.go). In the solver, infer
  `&p` to an immutable `RefType` borrow and `&mut p` to a mutable one, minting a fresh
  inferred lifetime bounded by `p`'s region, the same `RefType` the annotated
  `val q: &{x} = p` produces. Require `p` to be owned-mutable for `&mut p`. Prefix `&`
  binds looser than the postfix `.` and `[]`, so `&obj.f` parses as `&(obj.f)`, a borrow
  of the whole place path, not `(&obj).f`. Its field-granular semantics are in PR 4.
- Rule 2: a `&` parameter is a borrow and a bare parameter is owned. Adjust
  `attachParamLifetimes` ([internal/solver/infer_expr.go](../../internal/solver/infer_expr.go))
  so it mints a fresh lifetime only for `&` parameters, and leaves a bare parameter
  owned. An unannotated parameter stays inferred. The bare parameter is owned in the
  type here, but is not yet enforced as *consuming* its argument; that enforcement
  lands with the move engine in PR 6. See "Param-default flip is sequenced with the
  move engine" above. This PR also migrates the existing param snapshots from the
  borrow-by-default `mut 'a {x}` form to the new defaults.
- Auto-borrow at call sites: passing an owned argument to a `&` or `&mut`
  parameter inserts the borrow implicitly. The `RefType <: RefType` rule in
  [internal/solver/constrain.go](../../internal/solver/constrain.go) already makes
  owned-into-borrow free, so most of this falls out of argument constraining; add
  the check that a `&mut` parameter requires an owned-mutable argument.

Tests: owned vs borrow parameters; the consuming-parameter case; `foo(p)` rather
than `foo(&p)`; the `&mut`-requires-mutable rejection; `&p` and `&mut p` borrow
expressions parse, print round-trip, and infer borrows.

Acceptance: the rule-2 and rule-3 examples in the requirements check as written.

### PR 4 — Member reads borrow the receiver

Goal: implement inference rule 4.

- Change `inferMember`/`resolveMemberPath`
  ([internal/solver/infer_expr.go](../../internal/solver/infer_expr.go)) so reading
  `obj.f` yields a borrow of the field bounded by the receiver, rather than
  returning the property type directly. An owned receiver mints a fresh lifetime
  bounded by its scope; a borrowed receiver passes its lifetime through. A read
  consumed locally needs no displayed lifetime; one that escapes carries the
  receiver's lifetime.
- A field whose static type is itself a `&` borrow copies the borrow out rather
  than nesting, since immutable borrows are freely duplicable. This keeps reads
  from producing `&&` types and sets up PR 9's normalization.
- An explicit `&obj.f` borrows the path `obj.f` at field granularity, with the same
  receiver-bounded lifetime as the implicit member read, and locks only that field. A
  disjoint sibling such as `obj.g` stays independently usable, including `&mut obj.g`.
  This shares the path-granular tracking the partial-moves work introduces. A path the
  checker cannot prove disjoint, such as `arr[i]` versus `arr[j]`, falls back to a
  container-level borrow.

Tests: local member read with no displayed lifetime; an escaping member read that
carries the receiver lifetime; reading a `&`-typed field yields a flat borrow; an
explicit `&obj.f` borrow locks only the field and leaves `&mut obj.g` legal.

Acceptance: member reads display and constrain as receiver-bounded borrows.

### PR 5 — Move engine substrate: generalize escape detection, build the consumed lattice

Goal: stand up the two pieces the move rule needs, with no use-after-move errors
yet. See "Where the move analysis lives" and "Escape detection is generalized, not
queried" above.

Status: done. PR 5's scope is the consumed lattice and the `borrowEscapedToStatic`
detection-gap closure, both of which landed. The `constrainEscape` generalization to
the non-global flow sites, the forcing half of the first bullet below, was deferred past
PR 6 to PR 15; see PR 15.

- Generalize escape detection beyond the single module-level write site. Two halves:
  - Force a borrow flowing out at returns, field and element stores, owned-parameter
    arguments, and escaping closure captures to outlive its destination. **Deferred to
    PR 15** — today only the module-level global write forces escape
    ([internal/solver/infer_expr.go](../../internal/solver/infer_expr.go) at the
    `b.ModuleLevel` store), and that site forces `<: 'static`, while the non-global
    sites force a borrow to outlive the caller, receiver, or callee region instead.
  - Close the top-level-only gap in `borrowEscapedToStatic` so a borrow nested in a
    field, or reached through a usage-inferred type variable, is seen. **Done** — the
    nested field/element case landed in #786, and the usage-inferred `TypeVarType`
    case landed for #787 by descending into a type variable's bounds in
    `escapeDetectVisitor` ([internal/solver/transitions.go](../../internal/solver/transitions.go)).
- Build the branch-merged per-binding consumed lattice over the existing CFG. **Done**
  in #786. Add
  "moved on some path" and "moved on all paths" state that joins at the CFG merge
  blocks from [internal/liveness](../../internal/liveness), keyed by `VarID` and
  queried at `StmtRef` granularity, alongside the existing liveness and alias state
  on `funcCtx`. This is the genuinely new analysis.

The lattice the second bullet builds has three per-binding states, joined at every CFG
merge:

```text
            MaybeMoved
           /          \
     NotMoved          Moved
```

- **NotMoved** — no reaching path has moved the binding, so a use is allowed.
- **Moved** — every reaching path has moved it, so a use is an unconditional
  use-after-move.
- **MaybeMoved** — some but not all reaching paths moved it, so a use is a conditional
  use-after-move.

`NotMoved` and `Moved` are the agreeing states below the top. Joining two edges that
disagree raises the result to `MaybeMoved`, which then absorbs everything:

| ⊔ | NotMoved | Moved | MaybeMoved |
|---|---|---|---|
| **NotMoved** | NotMoved | MaybeMoved | MaybeMoved |
| **Moved** | MaybeMoved | Moved | MaybeMoved |
| **MaybeMoved** | MaybeMoved | MaybeMoved | MaybeMoved |

The entry state for every binding is `NotMoved`, and a move site sets it to `Moved`.
PR 6 reads the state at each use, passing `NotMoved` and rejecting `Moved` and
`MaybeMoved`.

Tests: unit tests that the consumed lattice merges correctly across if/else, match,
and loops; unit tests that `borrowEscapedToStatic` sees a borrow nested in a field or
tuple element and one reachable only through a usage-inferred type variable. The tests
that escape fires at returns, stores, and arguments move to PR 15, which forces escape
over the move engine's borrow tracking at those sites. Escaping-closure-capture escape
stays deferred.

Acceptance: the consumed lattice reports correct per-path state, and the escape query
sees every borrow in a recorded type, including nested and type-variable positions. The
"escape forced at the value-flow-out sites" criterion moves to PR 15, which covers
returns, parameter-field stores, and consuming arguments. No user-facing errors yet.

### PR 6 — Consume and use-after-move at every flow site, conditional moves

Goal: turn the PR 5 substrate into the affine rule. When an owned value escapes its
source region, ownership moves, the source is consumed, and a later use is an error.

Deferred from PR 5: the `constrainEscape` generalization to the non-global flow sites
was slated to land here. PR 6 landed the consume at each flow site, but the escape
forcing was deferred again, to PR 15, so PR 6 ships the consume and PR 15 ships the
forcing over the same sites. PR 5 was scoped to the consumed lattice and the
`borrowEscapedToStatic` detection-gap closure, both of which landed (#786 escape
nesting, #787 the usage-inferred `TypeVarType` case). The forcing half of PR 5's first
bullet is that today only the module-level global write runs `constrainEscape`
([internal/solver/infer_expr.go](../../internal/solver/infer_expr.go) at the
`b.ModuleLevel` store), so a borrow that flows out through a return, a field or element
store, an owned-parameter argument, or an escaping closure capture is not yet forced to
outlive its destination. PR 15 closes that, since the consume and the forcing turned out
to need different region targets and were cleaner as separate changes than as one wiring.

- Consume the source at each flow site: `val`/`var` binding, reassignment that drops
  the previous owner, `return`, field and element stores, owned-parameter arguments,
  and escaping closure captures. The global-write site stops dropping mutability and
  instead consumes, per "Move subsumes the escape-site logic." This is also where a
  bare owned parameter finally consumes its argument, the behaviour PR 3 deferred. The
  matching `constrainEscape` forcing at these same sites lands in PR 15.
- Conditional moves are path-sensitive through the consumed lattice: a value moved
  on one branch and untouched on another is consumed only on the moving paths, and a
  later use is an error only if some reaching path moved it.
- Add `UseAfterMoveError`, reported against both the move site and the use site.
- Reconcile the residual exclusivity check against the lattice. The mut/immut
  transition check runs inline during the walk, so when a global write consumes a
  source it would also raise the M4 G2 `borrowEscapedToStatic` self-conflict at a
  later alias of that source, double-reporting alongside the use-after-move.
  `reconcileMovedTransitions`
  ([internal/solver/moves.go](../../internal/solver/moves.go)) drops a
  `MutabilityTransitionError` whose source the lattice finds moved at the
  transition's program point, since the move engine reports the use-after-move
  there. The lattice query keeps this path-sensitive: a source moved on one branch
  is `NotMoved` on a sibling, so a real exclusivity conflict there survives. PR 8
  finishes this by driving the phase decision from the lattice directly rather than
  inline-plus-reconcile; see PR 8.
- This subsumes the "no Copy bound" requirement: reusing an owned type-parameter
  value triggers a second move and a use-after-move error, with no extra machinery.
  Add a test showing `fn dup<T>(x: T) -> [T, T]` fails and the `&T` form succeeds.
- Retire the M4 G3 implicit-reborrow path in `reborrowAnnotation`
  ([internal/solver/infer_decl.go](../../internal/solver/infer_decl.go)). G3 silently
  rewrites `val q: {x} = p` for a borrowed `p` into an immutable reborrow rather than
  an owned binding, which was sound in M4 but contradicts the affine rule landing in
  this PR. Once a `val` binding is a flow site that consumes an owned source, a
  borrowed source flowing into a bare owned annotation is no longer "silently
  reborrow"; it is a use-after-move on the borrow's referent or, equivalently, a
  borrow-into-owned escape. Delete the call to `reborrowAnnotation` in
  `constrainInitAgainstAnnotation` so the constraint falls through to the ordinary
  RefType<:bare arm, which already trips `BorrowEscapeError`. Users who want the
  reborrow keep it explicit with `val q: &{x} = p`. Update the few solver tests that
  were renamed in PR 3 from "Reborrow" to "BorrowAlias" — they already use the
  explicit `&` form and continue to test what they claim.

Tests: the motivating `leak` example errors at `p.x = 5`; `val q = storeGlobally(p);
print(p.x)`; a field store that escapes; an owned argument consumed by the callee; a
capture by an escaping closure; an if/else where only one branch moves; a
`val q: {x} = p_borrow` that was silently accepted under G3 is now rejected.

Acceptance: every flow site moves or borrows correctly, including per-branch
behaviour, and use-after-move names both sites. Forcing a borrow flowing out to outlive
its destination is PR 15, not this PR. A borrowed source flowing into a bare owned
annotation is rejected, with the explicit `&` form preserved as the opt-in for an alias.

### PR 7 — Partial moves and field-level ownership

Goal: moving one field out consumes that field's slot, not the whole object.

- Track consumption at field granularity. After a partial move the moved field may
  not be read; sibling fields stay usable. A whole-object read after a partial
  move errors only if it would expose a moved field.
- The conservative floor, if field-granular tracking proves costly, is to consume
  the whole object on any field move and record it as a precision limitation.

Tests: the `pair.a` / `pair.b` partial-move example from the requirements.

Acceptance: partial moves keep siblings live and reject reads of moved fields.

### PR 8 — Immutable→mutable thaw move and borrow-phase framing

Goal: the two ownership transitions, expressed as moves, plus the phase view of
exclusivity.

- Thaw: `val mut q = p` for an owned-immutable `p` moves `p` into a mutable
  binding and consumes it, so `q` is the sole owner and may be mutable. This is a
  move variant on the binding site from PR 6.
- Freeze: the mirror transition, `val q = p` for an owned-mutable `p`, moves `p` into
  an immutable binding and consumes it, so no mutable alias survives. This already
  falls out of the PR 6 move engine, since moving an owned value into an immutable
  binding is an ordinary escape-move; PR 8 only confirms the phase reframing leaves it
  intact.

  ```esc
  val mut p = {x: 0}    // owned-mutable
  p.x = 42
  val q = p             // freeze: move into an immutable binding; p consumed
  q.x = 5               // ERROR: q is immutable
  print(p.x)            // ERROR: use of `p` after it was moved into `q`
  ```
- Reframe the residual exclusivity from Rules 1/2/3
  ([internal/solver/transitions.go](../../internal/solver/transitions.go)) as the
  borrow-phase rule: a mutable owned value is in either an immutable phase with any
  number of `&` borrows or a mutable phase with any number of `&mut` borrows, and
  the two never overlap. The existing liveness-driven mut/immut conflict check is
  the mechanism; this PR aligns it to phases over lifetimes.
- Move the exclusivity check fully onto the consumed lattice. PR 6 runs the
  mut/immut transition check inline during the walk and then reconciles it against
  the lattice in a post-pass: `reconcileMovedTransitions`
  ([internal/solver/moves.go](../../internal/solver/moves.go)) drops a
  `MutabilityTransitionError` whose source the lattice finds moved at the transition
  point, since the move engine already reports a use-after-move there. That
  reconciliation is the seed of the phase reframing, not the end state. The inline
  check still mutates the alias tracker as it walks and cannot consult the
  post-walk lattice while running, so a transition that depends on per-path move
  state is decided in two steps rather than one. PR 8 should finish the move:
  drive the phase/exclusivity decision from the lattice and the alias set directly
  in the post-pass, so the inline check and the separate reconciliation collapse
  into one lattice-driven pass. With that done, the
  [internal/solver/transition_test.go](../../internal/solver/transition_test.go)
  static-escape unit tests, which pin the M4 G2 `borrowEscapedToStatic` self-conflict
  the move now subsumes, can be retired or rephrased as phase tests.

Tests: the thaw example with a use-after-move on the immutable source; the
multiple-`&mut` example staying legal; an immutable borrow overlapping a `&mut`
being rejected; a borrow-alias exclusivity conflict on a branch where the value is
NOT moved still reported, while the same conflict on a branch where it IS moved
reads as a single use-after-move.

Acceptance: both transitions are moves, exclusivity reads as the phase rule, and
the phase decision is taken in one lattice-driven post-pass rather than an inline
check plus a reconciliation.

### PR 9 — Unions/intersections as `RefInner`, mixed-ownership rejection, nested-borrow normalization

Goal: the type-former interactions that do not involve narrowing, built on M6's
union and intersection formers.

- Make M6's `UnionType` and `IntersectionType` participate as `RefInner`
  ([internal/soltype/type.go](../../internal/soltype/type.go)) so `&(A | B)` is one
  borrow over a union pointee. M6 already introduces the formers and can carry
  `mut` borrow members in a union, but does not make a union a valid borrow inner.
  The change itself is small, just two marker methods, but it quietly reaches further
  than that. The roughly seven `.(RefInner)` type assertions in the codebase will now
  also accept unions and intersections, with no compile error to point them out. Review
  each of those sites to confirm its logic still behaves correctly for a union or
  intersection inner, and add a test for each. The sites are listed earlier under
  "Unions as `RefInner`".
- Reject mixed-ownership unions and intersections: a type whose members disagree
  on ownership, such as `{x} | &{y}`, has no uniform verdict and is an error that
  asks the programmer to make ownership uniform first. No silent downgrade.
  Detection sits at the join sites where a mixed union forms during inference, such
  as if-branches or merges with different ownership, not at annotations, since the
  wrapper is outer and an annotation cannot spell a mixed union.
- Normalize nested borrows to depth one: collapse `& &` immutable layers to the
  inner borrow at the outer lifetime, and reject the uninhabitable `&mut &mut`
  form. With PR 4's copy-out of `&`-typed field reads, nesting only arises from
  generic substitution, so normalization is a local rule at lowering and
  substitution.

Tests: `&(A | B)` as a single borrow; a mixed-ownership union rejected with the
make-uniform message; `&&Point` normalized to `&Point`.

Acceptance: unions borrow as a unit, mixed ownership is rejected, and nested
borrows never exceed depth one.

### PR 10 — Mutable narrowed binding with pinned discriminant

Goal: the affine layer on M6's union-scrutinee narrowing. See "Narrowing comes from
M6, not this plan" above. This PR adds no narrowing of its own; it depends on M6
having landed.

- The narrowed binding M6 produces is treated as a fresh borrow of the scrutinee
  scoped to the narrowed region. An immutable narrowed binding sits in the immutable
  phase and is sound with no extra rule.
- A mutable narrowed binding `&mut A` is allowed. Through the binding itself the tag is
  already unwritable, because M6 narrows it to a single literal: `a.tag = "b"` is a type
  error since `"b"` is not assignable to `"a"`, with no extra rule, while the variant's
  other fields stay mutable. The work this PR adds is the cross-alias case. While the
  narrowed `&mut A` is live, the tag may not be written through any other alias of the
  scrutinee, even one typed as the full union, since such a write would flip the variant
  and invalidate the narrowed binding.

  ```esc
  val mut u: mut (A | B) = ...
  val b = &mut u            // b: &mut (A | B) — not narrowed
  match u {
      a is A => {
          a.x = 5           // OK — x is an ordinary field of the A variant
          // a.tag = "b" is already a type error: "b" is not assignable to "a"
          b.tag = "b"       // ERROR: the tag is pinned while a: &mut A is live
      }
  }
  ```

  Implement the pin as a scoped, field-level write restriction over the alias set,
  layered on PR 7's field-level tracking and PR 8's phase framing, hooked at the
  `inferMatch` arm scope and the `LitPat` discriminant arm in `bindPatternWith`.

Tests: the `match u { a is A => { a.x = 5 } }` example, with `a.x = 5` accepted; a tag
write through the narrowed binding rejected as a type error; a tag write through an
un-narrowed alias rejected by the pin.

Acceptance: mutable narrowing works with the discriminant pinned for the arm.

### PR 15 — Escape forcing at returns, stores, and consuming arguments

Goal: force a borrow flowing out of the function frame to outlive its destination
region, at the value-flow-out sites PR 6 left unforced. PR 6 consumes the source at
these sites but never pins the borrows the flowed-out value carries, so a returned or
stored value that borrows a function-local binding is silently accepted today. This PR
makes that case the escape error it should be, which is the baseline PR 11 then relaxes
for a self-contained component.

Status: done (#814). The return, parameter-field-store, and consuming-argument sites landed in
[internal/solver/return_escape.go](../../internal/solver/return_escape.go), built over a
per-binding borrow-edge graph on `funcCtx` rather than the lifetime sort, so the check
does not wait on M6.5. Deferred: element stores `xs[i] = …` need index-assignment support
from M7; extending the borrow graph through a field store into a *local* receiver is left
to PR 11, since storing a borrow into a borrow-typed field is itself rejected by the type
system today; field-granular escape such as `return a.peer` waits on PR 11's per-field
tracking; escaping-closure-capture escape stays deferred with PR 6's closure-capture work.

This is the forcing half of PR 5's first bullet, deferred through PR 6 to here. PR 5
landed the consumed lattice and the `borrowEscapedToStatic` detection-gap closure; PR 6
landed the consume at every flow site. Only the `constrainEscape` forcing at the
non-global sites remained, since today only the module-level global write runs it
([internal/solver/infer_expr.go](../../internal/solver/infer_expr.go) at the
`b.ModuleLevel` store).

- Force escape at the non-global value-flow-out sites: a `return`, a field store into a
  parameter, and a consuming argument. A flowed-out value travels at the call's own
  region, not `'static`, so the forcing is **not** the `'static` pin
  `consumeAtGlobalWrite` applies. Each borrow the value carries must outlive its
  destination: the caller's region for a return, the receiver's region for a field
  store, the callee's region for a consuming argument.
- Distinguish a function-local borrow from a parameter borrow. A borrow of a parameter
  carries a caller-supplied lifetime that already outlives the call, so flowing it out is
  sound — `fn (p: &'a {x}) -> &'a {x}` returns `p` at `'a` and keeps checking. A borrow
  of a function-local cannot outlive the frame, so flowing it out is an
  `EscapingBorrowError`. `collectParamVarIDs` records the parameter leaf VarIDs on
  `funcCtx`, and `isLocalReferent` exempts a referent in that set.
- Detect the carried borrows over the move engine's borrow tracking rather than the
  lifetime sort, so the check does not wait on M6.5. A per-binding borrow-edge graph on
  `funcCtx` records which function-locals each binding borrows, populated by
  `recordBorrowEdges` at `val`/`var` bindings. At each flow-out site, `escapingLocalsOf`
  scans the outgoing expression for direct `&`/`&mut` borrows with `borrowCollector` and
  follows the edges of a whole-binding place with `collectBorrowedLocals`.
- Files: the return site in
  [internal/solver/infer_stmt.go](../../internal/solver/infer_stmt.go); the field-store
  and consuming-argument sites in
  [internal/solver/infer_expr.go](../../internal/solver/infer_expr.go); the borrow-edge
  graph, the detection, and `EscapingBorrowError` in
  [internal/solver/return_escape.go](../../internal/solver/return_escape.go).

Tests: returning a borrow of a function-local is an `EscapingBorrowError`; returning a
parameter borrow `fn (p: &'a {x}) -> &'a {x}` still checks; storing a local borrow into a
parameter's field escapes; passing a local borrow as a consuming argument escapes; the
existing global-write escape tests are unchanged.

Acceptance: a borrow flowing out through a return, a store, or a consuming argument is
forced to outlive its destination and errors when it borrows a function-local, while a
parameter borrow flowing out at its own lifetime still checks. This is the
"escape forced at every value-flow-out site" criterion PR 5 and PR 6 deferred.

### PR 11 — Connected-component moves for graphs

Goal: move a self-contained cyclic or acyclic graph out of a function — returned or
stored — when no node is referenced from outside the graph. See "Moving a graph" in
the requirements. This logically extends the move engine (PRs 5–7) and the escape
forcing (PR 15); it's numbered later only because its dependencies are 6, 7, and 15.

PR 15 makes a returned or stored borrow of a function-local an escape error. PR 11
relaxes exactly that error for a self-contained component: when the flowed-out value's
borrow edges reach only local bindings that nothing outside the component references,
the escape is re-anchored to the destination region instead of rejected, and every
binding in the component is consumed.

- Compute the connected component reachable from an escaping value through its
  borrow edges, over the move/borrow state the engine already tracks on `funcCtx`
  (PRs 5–7).
- Establish the precondition that no node in the component is reachable from any
  binding or store outside it, reusing the alias and liveness state and the
  per-path tracking from PR 7.
- When it holds, treat the escape as a component move: re-anchor the internal borrow
  lifetimes to the destination region — unify the mutual lifetimes into the
  destination rather than failing the borrow-escape check — and consume every local
  binding in the component, so any later use of any of them is a use-after-move.
- When it fails — some node is externally aliased — fall back to the ordinary
  borrow-escape error or phase conflict.
- Soundness rests on the GC keeping co-moved nodes alive and on there being no
  external observer, so the phase rules are unchanged; this is the requirements'
  "Moving a graph" argument.
- Extend the borrow-edge graph beyond what PR 15 records. PR 15 records an edge only at a
  binding's initializer and keys it on the root binding, so three cases under-check: a
  borrow introduced by reassigning a `var` such as `a = &mut b`, a borrow projected into a
  destructuring leaf such as `val {peer} = {peer: &mut b}`, and a borrow held in one field
  returned on its own such as `return a.peer`. Closing them needs flow-sensitive,
  field-granular edges — set-and-clear per assignment joined at CFG merges, keyed by field
  place rather than root binding — which the component analysis here builds on anyway. The
  reassignment and destructuring cases are pinned by the disabled
  `TestVarReassignBorrowEscapes` and `TestDestructuredBorrowLeafEscapes`; the field-return
  miss is noted in [internal/solver/return_escape.go](../../internal/solver/return_escape.go).

Tests: the cyclic `build()` returns `a` with both `a` and `b` consumed; an acyclic
shared graph returned the same way; a graph where a node is also held by a retained
binding is rejected.

Acceptance: a self-contained graph, cyclic or acyclic, can be returned or stored;
externally-aliased nodes still error.

### PR 12 — retired (`Freeze`/`Thaw` utility types)

This PR is removed. It existed to convert a structure between mutable and immutable
*through type-parameter boundaries*, the one place an earlier design let deep `mut`
stop. With mutability uniformly deep (PR 13), `mut` already flows through type
arguments into a container's element types, so there is no boundary left for a mapped
type to cross. Freezing and thawing are then just the binding moves: moving a value
into an immutable binding freezes it, into a `val mut` binding thaws it, each reaching
the whole owned structure. A whole graph converts in one move via the connected-
component move (PR 11) — consuming the one owning binding kills every mutable path at
once, which is what licenses reading the component's `&mut` edges as `&`. The M9
mapped/conditional-type machinery is no longer a dependency of the affine model. See
"Freezing and thawing" in the requirements.

### PR 13 — Deep, uniform `mut` and `readonly`

Goal: make `mut` deep and uniform — flowing through a type's concrete structure *and*
through its type arguments — and add the `readonly` field modifier. This is
foundational mut-semantics work that the affine model builds on; it depends only on the
parser (PR 1) and the `&`/`mut` lowering (PR 2) and can land early, independent of the
move engine. See "Mutability depth" in the requirements.

- Lower a `mut`/`&mut` annotation by setting `RefType.Mut` deeply through the type's
  whole reachable structure, not just the outermost `RefType`. The modifier flows
  through type arguments as well, so `mut Foo<Point>` makes both `Foo`'s body and the
  substituted `Point` writable; the same distribution covers the `&` of `&mut`, so
  borrow-ness and mutability reach equally far. A `&`/`&mut` field is the one boundary:
  the enclosing modifier does not flip an explicitly-written borrow field's mutability.
- Treat a bare `mut T` as the mutable view at every instantiation, so `mut T` at
  `T = Point` lowers to `mut Point`, and `mut Array<T>` yields `mut T` elements. Real
  type-argument resolution for stdlib containers lands with TypeRef support (M7); until
  then the uniform-deep rule is exercised on the structural object/tuple forms, and the
  container element case follows the same `recvMut` propagation once `Array<T>`
  resolves.
- Parser: add the `readonly` field modifier to object type annotations
  ([internal/parser/type_ann.go](../../internal/parser/type_ann.go)), and lower it to
  a per-field no-reassignment flag on the object type. `readonly` governs only whether
  `obj.f = …` is allowed; it is orthogonal to deep mutability.
- This changes the current shallow-`mut` lowering, so migrate the affected solver
  snapshots.

Tests: `mut Array<Point>` is a fully mutable array of mutable points; `mut {a: {b}}`
and `&mut {a: {x}}` are deep; `mut Foo<Point>` over `type Foo<T> = {a: T}` makes both
the `a` layer and the `Point` writable; `readonly` rejects field reassignment but still
permits mutating the field's value when that value is mutable.

Acceptance: `mut`/`&mut` are deep and uniform, flowing through type arguments, and
`readonly` forbids reassignment only.

### PR 14 — Lazy deep `mut`: store the surface form, push the rule to access and constrain

Goal: change deep `mut` from an eager lowering that pre-bakes `mut` onto every nested
object and tuple cell to a lazy representation that stores the type as the user wrote
it and applies the deep-mut rule at access and constrain time. The two representations
encode the same semantic. The lazy form stores `mut {a: {x}}` verbatim instead of
`mut {a: mut {x}}`, so the stored type matches the surface annotation, error messages
and hover need no special elision, and the fresh-literal upgrade has nothing to strip.
The cost is threading a "mut context" flag through the constrain pipeline so the
object subtyping arm knows when it is inside a mutable wrapper and treats fields as
invariant, the work PR 13's eager representation does by baking `mut` into the field
types themselves.

Sequence this after PR 13 so the affine model and PR 12 can settle on deep-mut
behaviour first. The migration touches the same call sites PR 13 grew, so it is best
done as one focused change rather than dripped into the move-engine PRs.

- Remove `applyDeepMut` and `deepMutComponent` from
  [internal/solver/type_ann.go](../../internal/solver/type_ann.go).
  `resolveMutableTypeAnn` and `resolveRefTypeAnn` go back to wrapping the resolved
  inner in one `RefType` without touching its children. `inheritProv` goes with them,
  since the lazy form does not rebuild the inner.
- Remove `stripOwnedMut` and the deep-mut comment block from
  [internal/solver/infer_decl.go](../../internal/solver/infer_decl.go).
  `constrainInitAgainstAnnotation` constrains the fresh literal against `ref.Inner`
  directly, the way it did before PR 13. The literal is owned-immutable, the inner is
  the bare shape, and the C2 gate accepts it covariantly without a strip walk.
- Drop the owned-mutable-cell branch in `fieldReadBorrow`
  ([internal/solver/infer_expr.go](../../internal/solver/infer_expr.go)). With the
  lazy form, a field's value is the bare shape the user wrote, never a `mut` cell, so
  the only `RefType` branch left is the explicit borrow field. The bare
  `ObjectType`/`TupleType` arm now handles the deep-mut field too, with the receiver's
  mutability propagated through `recvMut` to decide whether the produced borrow is
  `&mut` or `&`.
- Likewise simplify `borrowInnerOf`. Its owned-mut peel exists only to undo the eager
  lowering; with the lazy form there is no owned-mut cell to peel and the helper
  collapses to the ordinary `RefInner` cast.
- Thread the mut context through the constrain pipeline. Add a flag, threaded
  alongside `seen`, that the RefType arm sets to true when entering a mutable
  wrapper. The `ObjectType <: ObjectType` arm consults it to decide per-field
  variance: covariant outside a mutable wrapper, invariant inside one. The same flag
  drives the recursive walk into nested object and tuple fields. Reset the flag at
  function and promise boundaries, since each carries its own annotation context.
  This subsumes `constrainWriteBack`: the per-field invariance the write view added
  becomes a natural consequence of the flag, and the function shrinks to the readonly
  guard plus a passthrough to constrain.
- Update the field-write requirement in `inferMemberAssign`. Today the requirement
  `mut {f: w, ...}` relies on the RefType arm's writeBack to pin `f` invariant; under
  the lazy form the same flag mediates the pin, so the requirement stays a one-line
  `mut {f: w, ...}` and the readonly upfront check is unchanged.
- Remove the `underMut` plumbing from
  [internal/soltype/print.go](../../internal/soltype/print.go). The stored type already
  matches the surface annotation, so the printer's RefType arm drops the elision
  branch and `printTypeMinPrec` drops its UM variant. The print tests that pinned the
  elision retire with it.
- Migrate the solver snapshots back to the surface form. Tests that asserted
  `mut {a: mut {x: number}}` flip to `mut {a: {x: number}}`. The PR 13 tests for
  deep-mut behaviour stay; only their rendered strings update.

Tests: the PR 13 acceptance tests still pass with the rendered strings switched to
the surface form. Two new tests pin the constrain-side rule: `mut {a: {x: number}}`
and `mut {a: {y: number}}` are incomparable under the lazy form when the outer is
mut, matching the eager form's invariance, while the same shapes are subtypes under
an immutable wrapper. A diagnostic that previously rendered through the elision pass
now renders the same string with no elision step.

Acceptance: deep-mut semantics is preserved end to end with no representational
deepening. The stored type matches the surface annotation, the printer is unchanged,
and the constrain pipeline carries the deep-mut rule through a context flag rather
than through the field types.

---

## Deferred and out of scope

- **Old checker affine semantics.** The HM checker keeps current behaviour; only
  the non-panicking guard in PR 1 touches it.
- **Cross-package moves.** Move behaviour at the boundary of imported, body-less
  declarations depends on declared lifetimes and is left to the library-import
  work, consistent with the requirements.
- **Interior-mutability escape hatches.** Moving a self-contained graph (PR 11) plus
  the freeze/thaw binding moves (PR 8) cover the cyclic and acyclic cases where the
  graph owns its own nodes. What stays deferred is the case they do not: a mutable
  cyclic structure whose nodes are referenced from outside the graph, with no single
  owner — a `Cell`-like wrapper, tracked separately under #618.
- **Diagnostic wording.** Each PR ships a working diagnostic; final wording and
  blame spans for use-after-move and move-on-escape are tuned as the engine
  settles.

## Testing approach

Solver work is tested with inline Escalier source asserted against rendered
`soltype` strings, the pattern in
[internal/solver/infer_borrow_lifetime_test.go](../../internal/solver/infer_borrow_lifetime_test.go),
with `snaps.MatchInlineSnapshot` for larger inferred types. Parser work is tested
in [internal/parser](../../internal/parser). No fixture changes are expected, since
fixtures exercise the old checker. After any change to checker or printer output,
re-run with `UPDATE_SNAPS=true` so snapshots stay in sync.
