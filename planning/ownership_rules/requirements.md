# Ownership rules

## Status and scope

This document is a draft. It states the rules that decide which programs the
ownership, borrowing, lifetime, and mutability checks accept. The goal is a rule
set small enough that every check in the solver is an instance of one of these
rules. A check that maps to no rule is either a bug or a rule this document is
missing, and either way the document or the check changes.

It builds on three earlier documents and does not replace their rationale:

- [docs/08_mutability.md](../../docs/08_mutability.md) and
  [docs/09_ownership.md](../../docs/09_ownership.md) describe the language as a
  user sees it.
- [planning/affine_semantics/requirements.md](../affine_semantics/requirements.md)
  introduces moves and argues that one rule should govern every flow site.
- [planning/lifetimes/requirements.md](../lifetimes/requirements.md) introduces
  liveness-based mutability transitions.

Where this document and those disagree, this document is the proposal. The
section [Divergences from the docs](#divergences-from-the-docs) lists the
disagreements found so far.

The section [Open decisions](#open-decisions) lists behaviour the solver has
today that no document states. Each needs a decision before the rules are final.

## The invariant

> No immutable reference ever observes a mutation. If a value is reachable
> through an immutable reference that is live at some point, then between the
> creation of that reference and its last use, the value is not mutated through
> any other path.

Every accepted program preserves the invariant. Among programs that preserve it,
the checks reject as few as they can. When the two goals conflict, the checks
reject.

## Terms

A *value type* is a primitive, a function, or a promise. Value types are copied
freely and none of the rules below apply to them.

A *reference-shaped* value is an object, tuple, array, or class instance. The
rules below govern reference-shaped values only.

A *place* is a binding or a field path rooted at a binding, such as `p`, `p.a`,
or `p.a.b`. Two places *overlap* when one is a prefix of the other. Places such
as `arr[i]` and `arr[j]`, whose disjointness the checker cannot prove, overlap
at the container `arr`.

A *region* is the span of program points during which a binding's storage
exists. A module-level binding's region is `'static`.

A *path* to a value is any place or borrow through which the value can be read
or written. A path is *shared* when it permits reads only and *mutable* when it
permits writes.

A path is *live* at a program point when some path through the control-flow
graph from that point reaches a later use of it.

A *flow site* is any point where a value moves from a source expression into a
destination. Bindings, reassignments, field and element stores, literal
elements, spreads, destructuring, `match` arm bindings, arguments, receivers,
returns, and closure captures are all flow sites.

## Type rules

These rules constrain types alone. They need no flow or liveness information.

**T1. Ownership is part of the type.** A bare type `T` or `mut T` is owned. A
type written `&T`, `&mut T`, `&'a T`, or `&'a mut T` is borrowed. This holds in
every annotation position: bindings, parameters, returns, and fields.

**T2. `mut` is deep through owned structure and stops at a borrow.** `mut T`
makes every layer of `T` that the value owns writable, including type arguments.
A `&` or `&mut` inside `T` carries its own mutability. A `mut` annotation on a
single field is rejected, since the enclosing value decides the field's
mutability.

**T3. A borrow's mutability narrows and never widens.** `&mut T` satisfies
`&T`. `&T` does not satisfy `&mut T`. Every write, every `&mut` borrow, and every
call to a `&mut self` method requires a mutable path to its target, and T3 is
the rule that decides each of them.

**T4. `readonly` forbids reassigning a slot.** A write to a `readonly` field is
rejected after the field's one initialization. A `readonly` field does not
satisfy a writable field, because a holder of the writable type could reassign
it. A getter with no setter counts as a `readonly` field.

**T5. A borrow cannot fill an owned destination.** A value of type `&T` or
`&mut T` does not satisfy a bare `T` or `mut T`, since the destination would
claim ownership of storage owned elsewhere. Reading a field through a borrow
produces a borrow of that field, not an owned value.

## Flow rules

Every flow site has a source and a destination, and the destination has a type.
The destination type is either written or inferred. These two rules decide what
the site does from those types alone.

**F1. Each flow site is a copy, a borrow, or a move.** The outcome depends only
on the source type and the destination type:

1. A value-type source or a `&` source is copied. Copying a `&` borrow produces
   another `&` borrow of the same value.
2. A `&mut` source flowing into a `&` or `&mut` destination is copied as a
   borrow of the same kind or narrower, as T3 allows.
3. An owned source flowing into a borrow destination is borrowed. The source
   keeps ownership.
4. An owned source flowing into an owned destination is moved. The source place
   is consumed.

**F2. A destination with no written type is owned exactly when the value
escapes.** A value escapes when the destination outlives the source's region.
Each case below follows from that:

- A `return` outlives the frame, so it is an owned destination.
- A store into module-level state outlives every local, so it is an owned
  destination.
- An unannotated parameter is owned when the body lets the parameter escape, and
  a borrow otherwise.
- A closure that is returned owns what it captures. A closure that stays in the
  body or is stored into a caller-owned object borrows what it captures.

A `val` or `var` binding of an owned value is always an owned destination, even
when the binding's scope is shorter than the source's. A binding with a `&`
annotation or a `&` initializer is a borrow destination.

## Lifetime rules

**L1. Every borrow produces an outlives constraint.** When a borrow of a place
flows into a destination, the place's region must outlive the destination. A
borrow whose destination outlives the place's owner is rejected. A borrow that
flows into module-level state forces its lifetime to `'static`.

**L2. A signature states exactly the lifetime relations its body needs.** A
body-carrying function must declare every outlives relation between its
parameters and return that its body establishes. Each declared bound `'a: 'b`
must also be established by the body. A bodyless `declare fn` is taken at its
word. See [D4](#d4-declared-lifetime-bounds-are-obligations).

**L3. Lifetimes appear in a rendered type only when they connect something.** A
lifetime that relates an input to an output, or that is `'static`, is rendered.
Any other lifetime is elided. The rendered signature is always a valid
annotation that produces the same type when written back.

## Exclusivity rules

These rules run over the control-flow graph. They use the copy, borrow, and move
events from F1 and the liveness of each path.

**E1. A moved place is not used until it is reassigned.** After a move of place
`p`, no path through the control-flow graph uses `p` or a place overlapping `p`
until `p` is reassigned. A use is a read, a write, a method call, a borrow, or a
second move. A move on one branch consumes `p` only on the paths through that
branch.

**E2. A shared path and a mutable path to one place are never live together.** At
no program point are a shared path and a mutable path to overlapping places both
live. The owner's own writes count as a mutable path. Several shared paths may be
live at once, and so may several mutable paths.

**E3. A place with a live borrow cannot be moved.** Moving `p` while a borrow of
a place overlapping `p` is live is rejected, because the borrow would point into
storage the move consumed.

**E4. A closure call is a use of each borrow the closure holds.** A call to a
closure, or a call that may run a closure, uses each borrow the closure holds. A
capture the closure writes is used as a mutable path, and a capture it only reads
is used as a shared path. A closure stored into module-level state may run during
any later call, so every later call uses its borrows.

## Derived behaviour

The rules above produce each of the following. None of them is a separate rule.

- **Freezing and thawing.** `val frozen = g` moves `g` by F1. E3 rejects the move
  while a mutable path into `g` is live. The thaw `val mut thawed = frozen` is the
  same move in the other direction.
- **Partial moves.** E1 and E3 work on places, so moving `pair.a` leaves
  `pair.b` usable.
- **Moves into literals and spreads.** A literal element and a spread source fill
  owned slots of the new value, so F1 moves an owned source.
- **Consuming receivers.** A `self` or `mut self` receiver is an owned
  destination, so F1 moves the instance. T5 rejects the call through a borrow.
- **The owned-to-`mut` upgrade.** A uniquely owned source can fill a `mut`
  destination because F1 moves it. E3 confirms no live path remains, so no shared
  path can observe the new mutability.
- **Mutability transitions created by annotation.** `val q: &{x: number} = p`
  borrows `p` by F1. A later write to `p` while `q` is live is rejected by E2.
  Rejecting the same program written with `val q = &p` follows from the same rule.

## Comparison with Rust

Escalier's model starts from Rust's. The table compares each rule with the Rust
behaviour closest to it. A rule marked "same" accepts and rejects the programs
Rust would, once both are written in the same shape. A rule marked "differs"
changes that set, and the note says in which direction. "No equivalent" means
Rust has no matching feature.

| Rule | Rust behaviour | Verdict | Note |
|---|---|---|---|
| T1 ownership in the type | `T` is owned and `&T` or `&mut T` borrows | Differs | Rust puts the mutability of an owned value on the binding, as `let mut x`. Escalier puts it in the type, as `mut T` |
| T2 deep `mut` | Inherited mutability: a mutable owner can write every field it owns, and a reference field carries its own mutability | Differs | Same rule, but Rust adds interior mutability through `Cell` and `RefCell`. Escalier has no escape hatch, which is what makes freezing deep |
| T3 narrow, never widen | `&mut T` coerces to `&T`, never the reverse | Same | |
| T4 `readonly` | No `readonly` fields. Rust restricts writes through field privacy instead | No equivalent | Inherited from TypeScript |
| T5 borrow into owned | `&T` does not satisfy `T`. Moving a field out of a borrow is rejected with E0507 | Differs | Escalier reads `r.f` through a borrow as a borrow of the field. Rust rejects the move and asks for `&r.f`, unless the field's type is `Copy` |
| F1 copy, borrow, or move | `Copy` types copy, `&T` copies, and an owned value moves into an owned destination | Differs | Two changes. A `&mut` source copies, because Escalier allows aliased `&mut`. An owned value fills a `&` annotation by borrowing, where Rust reports a type mismatch |
| F2 owned when it escapes | Every parameter type is written. A closure captures by reference unless it is marked `move` | Differs | Escalier infers an unannotated parameter's ownership and a returned closure's moves. Rust requires both to be written |
| F2 binding always moves | `let q = p` moves `p` whatever `q`'s scope | Same | |
| L1 borrows outlive constraints | A borrow cannot outlive its referent, and storing one in a `static` requires `'static` | Same | |
| L2 signature states the body's relations | Lifetimes are inferred only inside a body, never in a signature. A declared bound is an assumption the body may use | Differs | Escalier infers signature lifetimes for an unannotated function and treats a declared bound as an obligation. See D4 |
| L3 render only connecting lifetimes | Elision follows fixed syntactic rules, such as one input lifetime flowing to every output | Differs | Escalier elides by whether a lifetime connects anything, which also covers signatures Rust's elision rules reject |
| E1 no use after move | Flow-sensitive per branch, with partial moves, and reassignment re-initializes | Same | Reassignment does not yet re-initialize in Escalier. See [#1269](https://github.com/escalier-lang/escalier/issues/1269) |
| E2 no shared and mutable path live together | Aliasing XOR mutability over non-lexical lifetimes: one `&mut`, or any number of `&` | Differs | Escalier allows several live mutable paths at once. The target is single-threaded and garbage collected, so the only hazard left is a shared path observing a write |
| E3 no move while borrowed | Rejected with E0505 | Same | |
| E4 closure calls use its borrows | A closure holds its captured borrows from creation to its last use | Differs | Escalier counts the borrows only at each call, so a write to a captured value between two calls is accepted. Rust rejects it. A closure stored into module-level state needs `move` and `'static` captures in Rust |
| D1 no move out of module-level state | Moving out of a `static` is rejected with E0507 | Same | |
| D2 a moving closure is called once | `FnOnce` | Same | |
| D3 mixed owned and borrowed branches | `if` branches must have one type, so `T` and `&T` do not join | Same | |
| D4 declared bounds are obligations | Declared bounds are assumptions | Differs | Follows from L3 |
| D5 `readonly` initialized once | A struct literal initializes every field at once | No equivalent | |
| D6 getter and setter receivers | No getters or setters | No equivalent | |
| D7 written `&` at a call site | `&` and `&mut` are written at the call, except on a method receiver | Same | |
| Freezing and thawing | `let frozen = v; let mut thawed = frozen;` moves between bindings of different mutability | Same | `docs/09_ownership.md` says Rust has no equivalent. The moves are the same, but interior mutability keeps Rust's freeze from being deep |

The differences come from four sources:

- **The runtime.** Single-threaded JavaScript with a garbage collector removes
  data races and dangling pointers. That permits aliased `&mut` in F1 and E2 and
  the per-call closure borrows in E4.
- **A deep immutability guarantee.** Escalier's invariant promises that an
  immutable value never changes. Rust's interior mutability would break that
  promise, so T2 has no escape hatch.
- **Inference over annotation.** F2, L2, and L3 infer what Rust requires the
  user to write: parameter ownership, closure moves, and signature lifetimes.
- **TypeScript heritage.** T4, D5, and D6 cover `readonly` fields, getters, and
  setters, which Rust does not have.

## Flow-site table

The table lists each flow site's destination. Its outcome then follows from F1,
and its checks follow from L1 and E1 through E4. A conformance test exists for
each row crossed with each column of [Conformance tests](#conformance-tests).

| Flow site | Destination type |
|---|---|
| `val` or `var` binding | The annotation if written. Otherwise owned, or a borrow with a `&` initializer |
| Reassignment | The binding's type |
| Field store | The field's declared type |
| Element store | The element type |
| Literal element or spread | Owned |
| Destructuring or `match` arm | Per part, as for a binding |
| Argument | The parameter's type. An unannotated parameter is decided by F2 |
| Receiver | The method's `self` type |
| `return` | Owned, unless the return type is a borrow |
| Module-level store | Owned, with a `'static` region |
| Closure capture | Decided by where the closure flows, by F2 |

## Checking model

The rules suggest one checker, which is the target of the simplification:

1. Each flow site lowers to one event: copy, borrow of a kind, or move. Each
   borrow also lowers to its L1 outlives constraint.
2. Each read, write, and call lowers to a use event on a place. A call that may
   run a closure also produces the E4 uses.
3. The lifetime solver settles F2's escape questions from the outlives
   constraints.
4. One forward pass over the control-flow graph checks E1, E2, and E3 against the
   events and the liveness of each path.

Type rules T1 through T5 stay in the type checker, where they already sit.

## Open decisions

The solver enforces each item below today, but no document states it. Each needs
a choice before the rules are final. The current behaviour comes from reading the
code, and the file references point at where it lives.

### D1. Moving out of a module-level binding

`ModuleLevelMoveError` rejects moving a module-level binding or an import
([module_level_moves.go](../../internal/solver/module_level_moves.go)). The rule
is sound. A module-level binding outlives every call, so a move would consume it
for every later caller.

Recommendation: keep the behaviour and state it as part of E1. A place whose
region is `'static` is never consumed, so a move out of it is rejected.

### D2. Closures that move a capture can be called once

`consumeOnceCallee` consumes a closure that moves one of its captures when the
closure is called
([module_level_moves.go](../../internal/solver/module_level_moves.go)).

Recommendation: keep it and derive it. The call moves the capture, so by E1 a
second call would use a moved place. No separate rule is needed once E4 treats a
moved capture as a move event.

### D3. Branches that mix owned and borrowed values

`MixedOwnershipError` rejects a conditional whose branches produce an owned value
on one side and a borrow on the other
([infer_expr.go](../../internal/solver/infer_expr.go)).

The options are:

- Keep the error. It is simple and the user can add `&` to the owned branch.
- Join the branches to a borrow. The owned branch would then need an owner that
  outlives the expression, and no such owner exists for a fresh value.

Recommendation: keep the error and add it as a type rule, T6.

### D4. Declared lifetime bounds are obligations

`checkDeclaredLifetimeBounds` requires the body to establish each declared bound
`'a: 'b`
([infer_expr.go](../../internal/solver/infer_expr.go)). Rust works the other
way. There a declared bound is an assumption the body may rely on and the caller
must prove.

Escalier's choice follows from L3. A rendered signature has to match the
inferred one, so a declared bound the body does not establish would render a
signature the inference does not produce.

Recommendation: keep the behaviour, as L2 states it, and document it in
`docs/09_ownership.md`.

### D5. A `readonly` field is initialized once in a constructor

A second write to a `readonly` field inside a constructor is rejected, using the
definite-initialization lattice
([infer_class_ctor.go](../../internal/solver/infer_class_ctor.go)).

Recommendation: keep it. T4 already says one initialization, so this needs no
new rule, only the docs.

### D6. Getter and setter receivers

A getter must take `&self` and a setter must take `&mut self`
(`GetterReceiverError` and `SetterReceiverError`).

Recommendation: keep both as declaration well-formedness rules alongside the type
rules. They do not interact with flow.

### D7. Explicit `&` at a call site

`ImplicitBorrowArgError` requires an owned argument passed to a borrow parameter
to be written `&p` or `&mut p`
([borrow_exclusivity.go](../../internal/solver/borrow_exclusivity.go)). The docs
say the compiler still inserts the borrow.

Recommendation: keep the requirement and update the docs. It is a syntax rule,
not an ownership rule, so it sits outside this document's rule list.

### D8. A global write moves a borrow

`consumeAtGlobalWrite` records a move for any reference-shaped source, including
a borrow ([moves.go](../../internal/solver/moves.go)). This contradicts F1, under
which a borrow is copied and never consumed.

Recommendation: drop the special case. A borrow stored into module-level state
is handled by L1, which forces its lifetime to `'static` and rejects it unless
the borrowed place is itself `'static`.

## Known bugs

**B1. A store into a `&` field moves the source.** The field and element store
paths in `inferFieldAssign` call `consumeOwned` even when the destination is
declared as a borrow
([infer_expr.go](../../internal/solver/infer_expr.go)). F1 says this is a borrow.
The program below reports `use of moved value 'p'` on its last line and should
check clean:

```esc
export fn go() -> number {
    val p = {x: 1}
    val q = {x: 2}
    val h: mut {slot: &{x: number}} = {slot: &q}
    h.slot = p
    return p.x + h.slot.x
}
```

## Divergences from the docs

These came from reading the code and need confirming with a fixture each before
the docs change.

- Exclusivity between two explicit borrows, such as `&p` and `&mut p`, is
  enforced by the loan check in
  [borrow_exclusivity.go](../../internal/solver/borrow_exclusivity.go). Both docs
  say it is not yet enforced and cite
  [#794](https://github.com/escalier-lang/escalier/issues/794).
- An owned argument to a borrow parameter needs a written `&`. The docs say the
  borrow is still inserted for the user. See D7.
- A call to a `&mut self` method on an immutable binding goes through
  `checkReceiverMut` in [classes.go](../../internal/solver/classes.go), which
  appears to reject it. `docs/08_mutability.md` says the call is accepted.
- An element store `t[i] = x` is typed and moves its source. `docs/09_ownership.md`
  says it is rejected as unsupported and cites
  [#1268](https://github.com/escalier-lang/escalier/issues/1268).
- The docs say a field store moves only when the container outlives the source.
  The solver moves on every store into an owned field. F1 agrees with the solver.
  The container owns the value afterward, so the source cannot keep it.
- `docs/09_ownership.md` says a move is read off lifetime constraints. The solver
  decides moves separately at each flow site with its own conditions. The checking
  model above is the proposal to make the docs true.

## Conformance tests

Each flow site in the [flow-site table](#flow-site-table) gets one test per
column below. The expected result follows from the rules, so reviewing the table
of expected results reviews the rules. The tests are written as Escalier source
with the full expected diagnostic, as `CLAUDE.md` asks.

| Column | Source | Destination | Then | Expected result | Rule |
|---|---|---|---|---|---|
| copy | value type | any | use the source | Accepted | F1.1 |
| shared copy | `&T` | `&T` | use the source | Accepted | F1.1 |
| narrow | `&mut T` | `&T` | use both | Accepted | F1.2, T3 |
| widen | `&T` | `&mut T` | none | Mutability mismatch | T3 |
| borrow | owned | borrow | use the source | Accepted | F1.3 |
| move | owned | owned | use the source | Use of moved value | F1.4, E1 |
| move on one branch | owned | owned, in one branch | use after the join | Use of moved value | E1 |
| borrow escape | owned | borrow that outlives it | none | Borrow escapes | L1 |
| borrow into owned | `&T` | owned | none | Borrow cannot fill owned | T5 |
| write while shared | owned `mut` | `&T` | write the source, then use the destination | Exclusivity error | E2 |
| sequential phases | owned `mut` | `&T` | write the source, then create and use the destination | Accepted | E2 |
| move while borrowed | owned | owned | while a borrow of the source is live | Move while borrowed | E3 |
| disjoint field | owned | owned, of `p.a` | use `p.b` | Accepted | E1 |

Some cells do not apply. A literal element has no borrow destination, so its
borrow column is empty, and a value type never meets the move columns. The test
file records such a cell as not applicable instead of leaving it out, so a reader
can see the cell was considered.

## Migration

The checking model replaces several passes the solver runs today. The steps,
each a separate PR:

1. Write the conformance tests above against the current solver. A test the
   current solver fails becomes a known-bug entry, or a reason to change a rule.
2. Settle D1 through D8 and update `docs/08_mutability.md` and
   `docs/09_ownership.md` to match.
3. Build the event lowering and the single control-flow pass behind a flag. Run
   it alongside the current checks and compare diagnostics across every test and
   fixture. Resolve each difference as a bug in the old checks or a change to the
   rules.
4. Remove the old checks one at a time once the new pass matches them. The groups
   that the checking model replaces are:
   - The per-site `consume*` functions in
     [moves.go](../../internal/solver/moves.go) and
     [module_level_moves.go](../../internal/solver/module_level_moves.go),
     replaced by F1 and F2.
   - The alias-set transition check in
     [transitions.go](../../internal/solver/transitions.go) and the loan check
     in [borrow_exclusivity.go](../../internal/solver/borrow_exclusivity.go),
     both replaced by E2 and E3.
   - The frame-exit checks in
     [return_escape.go](../../internal/solver/return_escape.go) and
     [return_shared_paths.go](../../internal/solver/return_shared_paths.go),
     replaced by L1 and E2 at the return.
   - The separate escape logic in
     [return_escape.go](../../internal/solver/return_escape.go) and
     [borrow_flow.go](../../internal/solver/borrow_flow.go), replaced by the
     lifetime solver answering F2.
