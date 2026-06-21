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
- **Parser carries both grammars.** The parser is shared by both checkers through
  one `ParseLibFiles` entry, and `&` already means intersection in type-annotation
  position. To let the solver read the new `&`-borrow syntax while the old checker
  keeps the existing grammar, the parser gains a syntax-mode flag. The solver's
  parse path requests borrow mode; the old checker keeps the default. This is the
  one cross-cutting change and is described next.

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

## The parser syntax-mode split

A `SyntaxMode` value is added to the `Parser` struct
([internal/parser/parser.go](../../internal/parser/parser.go)) with two values,
`LegacySyntax` (default) and `BorrowSyntax`. It is threaded through `NewParser`,
copied in `saveState`/`restoreState`, and exposed on a solver-facing parse entry.
The fork inside the parser is small and local:

- In `BorrowSyntax`, a prefix `&` in `primaryTypeAnn`
  ([internal/parser/type_ann.go](../../internal/parser/type_ann.go)) parses a
  borrow and produces the new `RefTypeAnn` node. Infix `&` stays intersection, so
  `A & B` is unaffected and `&A` is a borrow; the two are distinguished by
  position.
- In `LegacySyntax`, `&` behaves exactly as today and `RefTypeAnn` is never
  produced, so the old checker never encounters a node its translation switch
  lacks.

Everything else — the `mut` prefix, `'a` prefixes on type refs, lifetime
parameters, and `val mut` patterns — is already shared and stays common.

---

## PR sequence

| PR | Title | Depends on | Rough size |
|----|-------|-----------|-----------|
| 1 | Parser borrow mode, `&` grammar, `RefTypeAnn` node, printer | — | Medium |
| 2 | Solver lowering of `&`, soltype `&` rendering, snapshot migration | 1 | Medium |
| 3 | Annotation-literal ownership, owned/borrow params, auto-borrow | 2 | Medium |
| 4 | Member reads borrow the receiver | 3 | Medium |
| 5 | Move engine core: consume-on-escape and use-after-move | 4 | Large |
| 6 | Move at the remaining flow sites and conditional moves | 5 | Medium |
| 7 | Partial moves and field-level ownership | 6 | Medium |
| 8 | Immutable→mutable thaw move and borrow-phase framing | 5 | Medium |
| 9 | Unions/intersections as `RefInner`, mixed-ownership rejection, nested-borrow normalization | 3 | Medium |
| 10 | Narrowing with pinned discriminant | 8, 9 | Medium |

### PR 1 — Parser borrow mode, `&` grammar, `RefTypeAnn` node, printer

Goal: the parser can read `&{x}`, `&mut {x}`, `&'a {x}`, and `&'a mut {x}` in
borrow mode and round-trips them through the printer, with the old checker
unaffected.

- Add `SyntaxMode` to `Parser`; thread it through `NewParser`,
  `saveState`/`restoreState`, and add a borrow-mode parse entry for the solver.
- Add the `ast.RefTypeAnn{Mut bool, Lifetime LifetimeAnnNode, Inner TypeAnn}`
  variant to the `TypeAnn` sum type
  ([internal/ast/type_ann.go](../../internal/ast/type_ann.go)), add it to
  `isTypeAnn()`, regenerate the AST via `gen_ast.go`, and update
  [internal/ast/visitor.go](../../internal/ast/visitor.go). `MethodReceiver`
  ([internal/ast/class.go](../../internal/ast/class.go)) is the field template:
  it already carries `Mut bool` plus `Lifetime LifetimeAnnNode`.
- Parse prefix `&`/`&mut`/`&'a`/`&'a mut` in `primaryTypeAnn`, gated on
  `BorrowSyntax`. Reuse the existing `LifetimeAnn`/`LifetimeUnionAnn` nodes for
  the lifetime slot.
- Render `RefTypeAnn` in [internal/printer/printer.go](../../internal/printer/printer.go).
- Defensive guard: replace the `panic` default in the old checker's
  `inferTypeAnn` ([internal/checker/infer_type_ann.go](../../internal/checker/infer_type_ann.go))
  with a clean "unsupported annotation" error returning `ErrorType`, so a stray
  new node can never crash the CLI or LSP. The old checker still parses in legacy
  mode and will not see `RefTypeAnn` in normal operation.

Tests: parser tests for each borrow form in
[internal/parser/type_ann_test.go](../../internal/parser/type_ann_test.go);
printer round-trip; a legacy-mode test asserting `A & B` still parses as
intersection and `&A` is not a borrow.

Acceptance: borrow forms parse and print in borrow mode; the full existing parser
and old-checker suites are green with no snapshot changes.

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
- Rule 2: a `&` parameter is a borrow and a bare parameter is owned and consuming.
  Adjust `attachParamLifetimes` ([internal/solver/infer_expr.go](../../internal/solver/infer_expr.go))
  so it mints a fresh lifetime only for `&` parameters, and leaves a bare
  parameter owned. An unannotated parameter stays inferred.
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

### PR 5 — Move engine core: consume-on-escape and use-after-move

Goal: the central affine rule. When an owned value escapes its source binding's
region, ownership moves, the source is consumed, and any later use is a
use-after-move error.

- Add a consumption pass over owned bindings, keyed on the escape verdict already
  computed by `constrainEscape`/`borrowEscapedToStatic`. A value escapes exactly
  when its lifetime is forced to outlive its source region, so move detection is a
  query over existing constraints rather than a new analysis. Build the
  binding-liveness side on the infrastructure in
  [internal/liveness](../../internal/liveness).
- Cover the first flow sites: `val`/`var` binding, `return`, and the store into a
  longer-lived or module-level binding. The global-write site
  ([internal/solver/infer_expr.go](../../internal/solver/infer_expr.go) around the
  `constrainEscape` call) stops dropping mutability and instead consumes the
  source, per "Move subsumes the escape-site logic."
- Add the `UseAfterMoveError`, reported against both the move site and the later
  use.

Tests: the motivating `leak` example now errors at `p.x = 5`; the freeze-by-return
example; a plain `val q = storeGlobally(p); print(p.x)` use-after-move.

Acceptance: escape consumes the source; the use-after-move diagnostic names the
move site and the use site.

### PR 6 — Move at the remaining flow sites and conditional moves

Goal: extend the move rule to every flow site and make it path-sensitive.

- Field and element stores, function arguments to owned/consuming parameters,
  closure captures by escaping closures, and reassignment that drops the previous
  owner.
- Conditional moves tracked per path: a value moved on one branch and untouched on
  another is consumed only on the paths where the move occurs, and a later use is
  an error only if some reaching path moved it.
- This subsumes the "no Copy bound" requirement: reusing an owned type-parameter
  value triggers a second move and a use-after-move error, with no extra
  machinery. Add a test showing `fn dup<T>(x: T) -> [T, T]` fails and the `&T`
  form succeeds.

Tests: a field store that escapes; an owned argument consumed by the callee; a
capture by an escaping closure; an if/else where only one branch moves.

Acceptance: all flow sites in the requirements move or borrow correctly, including
per-branch behaviour.

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
  move variant on the binding site from PR 5.
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
  borrow over a union pointee.
- Reject mixed-ownership unions and intersections: a type whose members disagree
  on ownership, such as `{x} | &{y}`, has no uniform verdict and is an error that
  asks the programmer to make ownership uniform first. No silent downgrade.
- Normalize nested borrows to depth one: collapse `& &` immutable layers to the
  inner borrow at the outer lifetime, and reject the uninhabitable `&mut &mut`
  form. With PR 4's copy-out of `&`-typed field reads, nesting only arises from
  generic substitution, so normalization is a local rule at lowering and
  substitution.

Tests: `&(A | B)` as a single borrow; a mixed-ownership union rejected with the
make-uniform message; `&&Point` normalized to `&Point`.

Acceptance: unions borrow as a unit, mixed ownership is rejected, and nested
borrows never exceed depth one.

### PR 10 — Narrowing with pinned discriminant

Goal: narrowing a union produces a new borrow binding, and a mutable narrowed
binding pins the discriminant.

- Narrowing introduces a fresh borrow of the scrutinee scoped to the narrowed
  region; the original keeps its union type. An immutable narrowed binding sits in
  the immutable phase and is sound with no extra rule.
- A mutable narrowed binding `&mut A` is allowed, and while it is live the
  discriminant the narrowing tested may not be written through any alias, though
  the variant's other fields stay mutable. Implement the tag-pin as a scoped,
  field-level write restriction layered on PR 7's field-level tracking and PR 8's
  phase framing.

Tests: the `match u { a is A => { a.x = 5 } }` example, with `a.x = 5` accepted and
a write to the tag rejected for the arm.

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
