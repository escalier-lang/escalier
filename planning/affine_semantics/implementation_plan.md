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
site** before any consume logic is written, and identifies consumption sites
explicitly at returns, field and element stores, owned-parameter arguments, and
escaping closure captures. The nested and type-variable escape gap is closed as
part of this work, since a move that misses a nested escape is unsound. This is why
the move engine is split across PR 5 (escape generalization and the consumed
lattice) and PR 6 (the consume and use-after-move behaviour).

### Narrowing does not exist yet in the solver

The discriminant pin assumed narrowing infrastructure to build on; there is none.
Neither `inferIfElse` nor `inferMatch`
([internal/solver/infer_expr.go](../../internal/solver/infer_expr.go)) refines the
scrutinee inside a branch or arm. Match binds only the pattern's leaf projections
through the member-lookup constraint path
([internal/solver/pattern.go](../../internal/solver/pattern.go)), never a narrowed
scrutinee. Decision: PR 10 splits. **PR 10a adds union narrowing** to the solver —
discriminant and shape guards in conditionals and match arms that refine the
scrutinee to a new per-arm binding, general type-system work that is independently
useful. **PR 10b adds the affine layer** — a mutable narrowed binding `&mut A` with
the discriminant pinned for the arm. The insertion points are `inferMatch` where
the arm scope is built and the `LitPat` discriminant arm in `bindPatternWith`.

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

| PR | Title | Depends on | Rough size |
|----|-------|-----------|-----------|
| 1 | `&` grammar, `RefTypeAnn` node, printer | — | Medium |
| 2 | Solver lowering of `&`, soltype `&` rendering, snapshot migration | 1 | Medium |
| 3 | Annotation-literal ownership, owned/borrow params, auto-borrow | 2 | Medium |
| 4 | Member reads borrow the receiver | 3 | Medium |
| 5 | Move engine substrate: generalize escape detection, build the consumed lattice | 4 | Large |
| 6 | Consume and use-after-move at every flow site, conditional moves | 5 | Large |
| 7 | Partial moves and field-level ownership | 6 | Medium |
| 8 | Immutable→mutable thaw move and borrow-phase framing | 6 | Medium |
| 9 | Unions/intersections as `RefInner`, mixed-ownership rejection, nested-borrow normalization | 3 | Medium |
| 10a | Union narrowing in the solver (general type-system work) | 9 | Large |
| 10b | Mutable narrowed binding with pinned discriminant | 8, 10a | Medium |

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
  a borrow in every position. Add tests pinning `val q: &{x} = p` as a borrow and
  `val q: {x} = p` as a move into an owned binding.
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
than `foo(&p)`; the `&mut`-requires-mutable rejection.

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

Tests: local member read with no displayed lifetime; an escaping member read that
carries the receiver lifetime; reading a `&`-typed field yields a flat borrow.

Acceptance: member reads display and constrain as receiver-bounded borrows.

### PR 5 — Move engine substrate: generalize escape detection, build the consumed lattice

Goal: stand up the two pieces the move rule needs, with no use-after-move errors
yet. See "Where the move analysis lives" and "Escape detection is generalized, not
queried" above.

- Generalize escape detection beyond the single module-level write site. Run
  `constrainEscape` at returns, field and element stores, owned-parameter
  arguments, and escaping closure captures, and close the top-level-only gap in
  `borrowEscapedToStatic` so a borrow nested in a field or reached through a
  usage-inferred type variable is seen. Files:
  [internal/solver/infer_expr.go](../../internal/solver/infer_expr.go),
  [internal/solver/infer_stmt.go](../../internal/solver/infer_stmt.go),
  [internal/solver/transitions.go](../../internal/solver/transitions.go).
- Build the branch-merged per-binding consumed lattice over the existing CFG. Add
  "moved on some path" and "moved on all paths" state that joins at the CFG merge
  blocks from [internal/liveness](../../internal/liveness), keyed by `VarID` and
  queried at `StmtRef` granularity, alongside the existing liveness and alias state
  on `funcCtx`. This is the genuinely new analysis.

Tests: unit tests that the consumed lattice merges correctly across if/else, match,
and loops; tests that escape now fires at returns, stores, arguments, and captures.

Acceptance: escape is detected at every value-flow-out site, and the consumed
lattice reports correct per-path state. No user-facing errors yet.

### PR 6 — Consume and use-after-move at every flow site, conditional moves

Goal: turn the PR 5 substrate into the affine rule. When an owned value escapes its
source region, ownership moves, the source is consumed, and a later use is an error.

- Consume the source at each flow site: `val`/`var` binding, reassignment that drops
  the previous owner, `return`, field and element stores, owned-parameter arguments,
  and escaping closure captures. The global-write site stops dropping mutability and
  instead consumes, per "Move subsumes the escape-site logic." This is also where a
  bare owned parameter finally consumes its argument, the behaviour PR 3 deferred.
- Conditional moves are path-sensitive through the consumed lattice: a value moved
  on one branch and untouched on another is consumed only on the moving paths, and a
  later use is an error only if some reaching path moved it.
- Add `UseAfterMoveError`, reported against both the move site and the use site.
- This subsumes the "no Copy bound" requirement: reusing an owned type-parameter
  value triggers a second move and a use-after-move error, with no extra machinery.
  Add a test showing `fn dup<T>(x: T) -> [T, T]` fails and the `&T` form succeeds.

Tests: the motivating `leak` example errors at `p.x = 5`; `val q = storeGlobally(p);
print(p.x)`; a field store that escapes; an owned argument consumed by the callee; a
capture by an escaping closure; an if/else where only one branch moves.

Acceptance: every flow site moves or borrows correctly, including per-branch
behaviour, and use-after-move names both sites.

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
- Reframe the residual exclusivity from Rules 1/2/3
  ([internal/solver/transitions.go](../../internal/solver/transitions.go)) as the
  borrow-phase rule: a mutable owned value is in either an immutable phase with any
  number of `&` borrows or a mutable phase with any number of `&mut` borrows, and
  the two never overlap. The existing liveness-driven mut/immut conflict check is
  the mechanism; this PR aligns it to phases over lifetimes and keeps the freeze
  (mut→immut) case it already handles.

Tests: the thaw example with a use-after-move on the immutable source; the
multiple-`&mut` example staying legal; an immutable borrow overlapping a `&mut`
being rejected.

Acceptance: both transitions are moves, and exclusivity reads as the phase rule.

### PR 9 — Unions/intersections as `RefInner`, mixed-ownership rejection, nested-borrow normalization

Goal: the type-former interactions that do not involve narrowing.

- Make `UnionType` and `IntersectionType` participate as `RefInner`
  ([internal/soltype/type.go](../../internal/soltype/type.go)) so `&(A | B)` is one
  borrow over a union pointee. This is two marker methods plus a behavioural review
  of the `.(RefInner)` assertion sites listed under "Unions as RefInner is a
  behavioural change, not a fan-out" above, each with a test.
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

### PR 10a — Union narrowing in the solver

Goal: add the narrowing infrastructure the affine pin depends on, since the solver
has none today. See "Narrowing does not exist yet in the solver" above. This is
general type-system work, useful independently of affine semantics.

- In `inferIfElse` and `inferMatch`
  ([internal/solver/infer_expr.go](../../internal/solver/infer_expr.go)), refine the
  scrutinee inside a branch or arm based on a discriminant or shape guard, binding
  the scrutinee to a narrowed type in the branch or arm scope rather than leaving it
  at the un-narrowed union. The `match` arm scope is built where `bindPattern` is
  called; the discriminant comparison is the `LitPat` arm in `bindPatternWith`
  ([internal/solver/pattern.go](../../internal/solver/pattern.go)).
- Narrowing introduces a fresh binding for the narrowed view; the original keeps its
  union type.

Tests: a discriminated union narrowed in a match arm and in an if guard; the
narrowed binding has the variant type, the original keeps the union.

Acceptance: conditionals and match arms narrow a union scrutinee.

### PR 10b — Mutable narrowed binding with pinned discriminant

Goal: the affine layer on PR 10a's narrowing.

- The narrowed binding is a fresh borrow of the scrutinee scoped to the narrowed
  region. An immutable narrowed binding sits in the immutable phase and is sound
  with no extra rule.
- A mutable narrowed binding `&mut A` is allowed, and while it is live the
  discriminant the narrowing tested may not be written through any alias, though the
  variant's other fields stay mutable. Implement the tag-pin as a scoped,
  field-level write restriction layered on PR 7's field-level tracking and PR 8's
  phase framing.

Tests: the `match u { a is A => { a.x = 5 } }` example, with `a.x = 5` accepted and a
write to the tag rejected for the arm.

Acceptance: mutable narrowing works with the discriminant pinned for the arm.

---

## Deferred and out of scope

- **Old checker affine semantics.** The HM checker keeps current behaviour; only
  the non-panicking guard in PR 1 touches it.
- **Cross-package moves.** Move behaviour at the boundary of imported, body-less
  declarations depends on declared lifetimes and is left to the library-import
  work, consistent with the requirements.
- **Interior-mutability escape hatches.** A `Cell`-like wrapper for cyclic mutable
  data is tracked separately under #618.
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
