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
today that no document states, and two design questions the rules leave open.
Each needs a decision before the rules are final.

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
or written. A path is *immutable* when it permits reads only and *mutable* when
it also permits writes.

A path is *live* at a program point when some path through the control-flow
graph from that point reaches a later use of it.

A *flow site* is an operation that passes a value from a source expression to a
destination, such as a binding, an assignment, or a function argument. F1 lists
every kind of flow site.

## Type rules

These rules constrain types alone. They need no flow or liveness information.

**T1. Ownership is part of the type.** A bare type `T` or `mut T` is owned. A
type written `&T`, `&mut T`, `&'a T`, or `&'a mut T` is borrowed. This holds in
every annotation position: bindings, parameters, returns, and fields.

**T2. Every type is immutable unless it is written `mut`.** A bare `T` and a
borrow `&T` are both immutable. Only `mut T` and `&mut T` permit writes. A
fresh value, such as a literal or a constructor call, is immutable until a `mut`
annotation or a `val mut` binding makes it mutable.

**T3. Mutability is deep through owned structure and stops at a borrow.** A
type's mutability applies to every layer the value owns, including type
arguments. Every owned layer of `mut T` is writable, and every owned layer of
`T` is read-only. A `&` or `&mut` inside the type carries its own mutability
either way. An immutable `Array<&mut Point>` can still write through each
element, and a `mut Array<&Point>` cannot write any point. A `mut` annotation
on a single field is rejected, since the enclosing value decides the field's
mutability.

**T4. A borrow's mutability narrows and never widens.** `&mut T` satisfies `&T`.
`&T` does not satisfy `&mut T`. Every write through a borrow, and every `&mut
self` call through one, requires a `&mut` borrow, and T4 is the rule that
decides each of them. T7 states the matching rule for an owned value. A function
holding `p: &mut {x: number}` can pass `p` to a parameter typed `&{x: number}`.
A function holding `p: &{x: number}` cannot pass it to a parameter typed `&mut
{x: number}`, and the call reports `cannot constrain immutable object <: mutable
object`.

**T5. `readonly` forbids reassigning a slot.** A write to a `readonly` field is
rejected after the field's one initialization. A `readonly` field does not
satisfy a writable field, because a holder of the writable type could reassign
it. A getter with no setter counts as a `readonly` field.

**T6. A borrow cannot fill an owned destination.** A value of type `&T` or
`&mut T` does not satisfy a bare `T` or `mut T`, since the destination would
claim ownership of storage owned elsewhere. Reading a field through a borrow
produces a borrow of that field, not an owned value. With `r: &{x: number}`, the
binding `val q: {x: number} = r` reports `cannot use borrowed &object as owned
object`, and `val q: &{x: number} = r` is accepted.

**T7. An owned value is borrowed mutably only when it is mutable.** An owned
mutable value can be borrowed as `&` or `&mut`. An owned immutable value can be
borrowed only as `&`. The same holds for a call to a `&mut self` method, which
borrows its receiver mutably. With `val p = {x: 1}`, `&mut p` reports
`cannot constrain immutable object <: mutable object`. With
`val mut p = {x: 1}`, both `&p` and `&mut p` are accepted.

## Flow rules

An operation such as a binding, an assignment, or a function call passes a value
from a source to a destination. F1 lists these operations and the destination
type each one has. F2 says what happens to the value, which depends only on the
kind of value and the kind of destination, not on the operation. F3 and F4
settle the two cases the first two leave open: a destination with no written
type, and how a borrow is written.

**F1. Each operation that passes a value gives its destination a type.** The
type is the one written at the destination. When nothing is written, F3 decides
it. These operations are the flow sites:

| Operation | Example | Destination type |
|---|---|---|
| `val` or `var` binding | `val q = p` | The annotation if written. Otherwise owned, or a borrow when the initializer is `&p` or `&mut p` |
| Reassignment | `q = p` | The binding's type |
| Field store | `o.f = p` | The field's declared type |
| Element store | `a[i] = p` | The element type |
| Literal element or spread | `{f: p}`, `[p]`, `{...p}` | Owned |
| Destructuring or `match` arm | `val {f} = p` | Per part, as for a binding |
| Argument | `f(p)` | The parameter's type. An unannotated parameter is decided by F3 |
| Receiver | `p.m()` | The method's `self` type |
| `return` | `return p` | Owned, unless the return type is a borrow |
| Module-level store | `cache = p` | Owned, with a `'static` region |
| Closure capture | `fn () { return p.x }` | Decided by where the closure flows, by F3 |

**F2. What happens to a value depends on its kind and the destination's kind.**
There are three outcomes:

- A *copy* gives the destination the same value as the source, and the source
  stays usable. Copying a value type copies the value. Copying a borrow gives
  the destination a second borrow of the same value, and the value itself is
  not copied.
- A *borrow* gives the destination a new borrow of the source's place. The
  source keeps ownership.
- A *move* gives the destination ownership. The source place is consumed, and
  E1 rejects any later use of it. A move may change the value's mutability,
  which is how freezing and thawing work.

The table gives the outcome for each kind of source and destination. The
operation that passes the value does not change it.

| Source | Owned destination | `&` destination | `&mut` destination |
|---|---|---|---|
| Value type, such as `number` | Copy | Not applicable | Not applicable |
| Owned immutable, `T` | Move | Borrow, written `&p` | Rejected by T7 |
| Owned mutable, `mut T` | Move | Borrow, written `&p` | Borrow, written `&mut p` |
| Immutable borrow, `&T` | Rejected by T6 | Copy | Rejected by T4 |
| Mutable borrow, `&mut T` | Rejected by T6 | Copy, narrowed to `&` by T4 | Copy |

A value type has no borrow form, so its destination is always bare. Each binding
in the function below shows one outcome. Reading `q.x` after `val d = q` would
report `use of moved value 'q'`.

```esc
fn go(r: &{x: number}) -> number {
    val n = 1
    val a = n                  // copy, since n is a value type
    val b = r                  // copy, since r is a & borrow
    val p = {x: 1}
    val c = &p                 // borrow, so p stays usable
    val q = {x: 2}
    val d = q                  // move, so q is consumed
    return a + b.x + c.x + p.x + d.x
}
```

**F3. A destination with no written type is owned exactly when the value
escapes.** A value escapes when the destination outlives the source's region.
Each case below follows from that:

- A `return` outlives the frame, so it is an owned destination.
- A store into module-level state outlives every local, so it is an owned
  destination. [D9](#d9-whether-an-escaping-value-moves-or-borrows) asks whether
  it should be a `'static` borrow instead.
- An unannotated parameter is owned when the body lets the parameter escape, and
  a borrow otherwise.
- A closure that is returned owns what it captures. A closure that stays in the
  body or is stored into a caller-owned object borrows what it captures.

A `val` or `var` binding of an owned value is always an owned destination, even
when the binding's scope is shorter than the source's. A binding whose
initializer is a `&` or `&mut` expression is a borrow destination.

A return with no written type is owned when it returns a local, and carries the
parameter's borrow when it returns one:

```esc
fn make() {
    val p = {x: 1}
    return p    // moves p out, so make returns an owned value
}

fn same(p: &{x: number}) {
    return p    // same: fn <'a>(p: &'a {x: number}) -> &'a {x: number}
}
```

An unannotated parameter takes its ownership from the body. `store` lets `p`
escape into module-level state, so its parameter is owned and the call moves
`a`. `read` only reads `p`, so its parameter is a borrow and the call borrows
`b`:

```esc
var sink = {x: 0}

fn store(p) { sink = p }     // store: fn (p: {x: number}) -> undefined
fn read(p) { return p.x }    // read: fn (p: &{x: number}) -> number

fn go() -> number {
    val a = {x: 1}
    store(a)                 // moves a
    val b = {x: 2}
    val n = read(&b)         // borrows b, so b stays usable
    return b.x + n
}
```

A closure that stays in the body borrows its captures, and one that is returned
owns them:

```esc
fn local() -> number {
    val p = {x: 1}
    val f = fn () { return p.x }    // borrows p
    val a = f()
    return a + p.x                  // p is still usable
}

fn returned() {
    val p = {x: 1}
    return fn () { return p.x }     // owns p, since it outlives the frame
}
```

A binding moves an owned value even when the binding dies first:

```esc
fn go() -> number {
    val p = {x: 0}
    if true {
        val q = p       // moves p
    }
    return p.x          // use of moved value 'p'
}
```

**F4. Borrowing an owned value is always written.** An owned source reaches a
borrow destination only through `&p` or `&mut p`. This holds at every flow
site, including bindings, arguments, field and element stores, returns, and
literal elements. A method receiver is the one exception, since the method's own
`self` declares the borrow. A site that borrows without the `&` is an error,
so every borrow is visible where it is taken. `val c: &{x: number} = p` is
rejected, and `val c: &{x: number} = &p` and `val c = &p` are accepted.

## Lifetime rules

**L1. A borrow's lifetime is how long its path stays live.** When a borrow of a
place flows into a destination, the borrow's lifetime is extended to cover the
destination. A borrow may outlive the binding that owns its place. The garbage
collector keeps the value alive while any path reaches it, so outliving the owner
is never an error on its own. The lifetime matters only to E2, which reads it to
decide how long the path is live. A borrow that flows into module-level state
gets the lifetime `'static`, so its path stays live for the rest of the program.

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
events from F2 and the liveness of each path.

**E1. A moved place is not used until it is reassigned.** After a move of place
`p`, no path through the control-flow graph uses `p` or a place overlapping `p`
until `p` is reassigned. A use is a read, a write, a method call, a borrow, or a
second move. A move on one branch consumes `p` only on the paths through that
branch.

**E2. An immutable path and a mutable path to one place are never live
together.** At no program point are an immutable path and a mutable path to
overlapping places both live. The owner's own writes count as a mutable path.
Several immutable paths may be live at once, and so may several mutable paths. A
path with a `'static` lifetime is live at every later point, so a value with a
`'static` immutable path is never written again, and a value with a `'static`
mutable path never gets a new immutable path.

The two functions below differ only in statement order. In the first, `q` is
read after the write to `p`, so the two paths overlap and the write reports
`cannot assign to 'p.x' while it is borrowed as immutable`. In the second, the
write is `p`'s last mutable use, so nothing overlaps.

```esc
fn overlapping() -> number {
    val mut p = {x: 0}
    val q: &{x: number} = &p
    p.x = 1
    return q.x
}

fn sequential() -> number {
    val mut p = {x: 0}
    p.x = 1
    val q: &{x: number} = &p
    return q.x
}
```

**E3. A place with a live borrow cannot be moved.** Moving `p` while a borrow of
a place overlapping `p` is live is rejected. The move makes the destination an
owner, and an owner can always make its value mutable by moving it into a
`val mut` binding. The live borrow would then observe that owner's writes, even
when every path is immutable at the moment of the move. Below, `go` never
writes `p`, but `take` thaws its argument and writes it while `c` is still
live, so the call reports `cannot move 'p' while it is borrowed`.

```esc
fn take(p: {x: number}) -> number {
    val mut q = p
    q.x = 5
    return q.x
}

fn go() -> number {
    val p = {x: 1}
    val c = &p
    val n = take(p)
    return c.x + n
}
```

**E4. A closure call is a use of each borrow the closure holds.** A call to a
closure, or a call that may run a closure, uses each borrow the closure holds. A
capture the closure writes is used as a mutable path, and a capture it only reads
is used as an immutable path. A closure stored into module-level state may run during
any later call, so every later call uses its borrows.

## Derived behaviour

The rules above produce each of the following. None of them is a separate rule.

- **Freezing and thawing.** `val frozen = g` moves `g` by F2. E3 rejects the move
  while a mutable path into `g` is live. The thaw `val mut thawed = frozen` is the
  same move in the other direction.
- **Partial moves.** E1 and E3 work on places, so moving `pair.a` leaves
  `pair.b` usable.
- **Moves into literals and spreads.** A literal element and a spread source fill
  owned slots of the new value, so F2 moves an owned source.
- **Consuming receivers.** A `self` or `mut self` receiver is an owned
  destination, so F2 moves the instance. T6 rejects the call through a borrow.
- **The owned-to-`mut` upgrade.** A uniquely owned source can fill a `mut`
  destination because F2 moves it. E3 confirms no live path remains, so no immutable
  path can observe the new mutability.
- **Mutability transitions.** `val q = &p` borrows `p` by F2 and F4. A later
  write to `p` while `q` is live is rejected by E2.

## Comparison with Rust

Escalier's model starts from Rust's. The table compares each rule with the Rust
behaviour closest to it. A rule marked "same" accepts and rejects the programs
Rust would, once both are written in the same shape. A rule marked "differs"
changes that set, and the note says in which direction. "No equivalent" means
Rust has no matching feature.

| Rule | Rust behaviour | Verdict | Note |
|---|---|---|---|
| T1 ownership in the type | `T` is owned and `&T` or `&mut T` borrows | Differs | Rust puts the mutability of an owned value on the binding, as `let mut x`. Escalier puts it in the type, as `mut T` |
| T2 immutable by default | Bindings and references are immutable unless written `mut` | Same | |
| T3 deep mutability, stopped by a borrow | Inherited mutability: an owner's mutability reaches every field it owns, and a reference field carries its own mutability | Differs | Same inheritance, but Rust adds interior mutability through `Cell` and `RefCell`. Escalier has no escape hatch, which is what makes freezing deep |
| T4 narrow, never widen | `&mut T` coerces to `&T`, never the reverse | Same | |
| T5 `readonly` | No `readonly` fields. Rust restricts writes through field privacy instead | No equivalent | Inherited from TypeScript |
| T6 borrow into owned | `&T` does not satisfy `T`. Moving a field out of a borrow is rejected with E0507 | Differs | Escalier reads `r.f` through a borrow as a borrow of the field. Rust rejects the move and asks for `&r.f`, unless the field's type is `Copy` |
| T7 mutable borrows need a mutable owner | `&mut x` requires `let mut x` | Same | |
| F2 copy, borrow, or move | `Copy` types copy, `&T` copies, and an owned value moves into an owned destination | Differs | A `&mut` source copies, because Escalier allows aliased `&mut` |
| F3 owned when it escapes | Every parameter type is written. A closure captures by reference unless it is marked `move` | Differs | Escalier infers an unannotated parameter's ownership and a returned closure's moves. Rust requires both to be written |
| F3 binding always moves | `let q = p` moves `p` whatever `q`'s scope | Same | |
| F4 borrows are written | `&` and `&mut` are written to borrow an owned value, except on a method receiver, which auto-borrows | Same | |
| L1 lifetime is liveness | A borrow cannot outlive its referent, and storing one in a `static` requires `'static` | Differs | A borrow may outlive its owner, because the garbage collector keeps the value alive. Lifetimes only bound how long a path is live for E2 |
| L2 signature states the body's relations | Lifetimes are inferred only inside a body, never in a signature. A declared bound is an assumption the body may use | Differs | Escalier infers signature lifetimes for an unannotated function and treats a declared bound as an obligation. See D4 |
| L3 render only connecting lifetimes | Elision follows fixed syntactic rules, such as one input lifetime flowing to every output | Differs | Escalier elides by whether a lifetime connects anything, which also covers signatures Rust's elision rules reject |
| E1 no use after move | Flow-sensitive per branch, with partial moves, and reassignment re-initializes | Same | Reassignment does not yet re-initialize in Escalier. See [#1269](https://github.com/escalier-lang/escalier/issues/1269) |
| E2 no immutable and mutable path live together | Aliasing XOR mutability over non-lexical lifetimes: one `&mut`, or any number of `&` | Differs | Escalier allows several live mutable paths at once. The target is single-threaded and garbage collected, so the only hazard left is an immutable path observing a write |
| E3 no move while borrowed | Rejected with E0505 | Same | |
| E4 closure calls use its borrows | A closure holds its captured borrows from creation to its last use | Differs | Escalier counts the borrows only at each call, so a write to a captured value between two calls is accepted. Rust rejects it. A closure stored into module-level state needs `move` and `'static` captures in Rust |
| D1 no move out of module-level state | Moving out of a `static` is rejected with E0507 | Same | |
| D2 a moving closure is called once | `FnOnce` | Same | |
| D3 mixed owned and borrowed branches | `if` branches must have one type, so `T` and `&T` do not join | Same | |
| D4 declared bounds are obligations | Declared bounds are assumptions | Differs | Follows from L3 |
| D5 `readonly` initialized once | A struct literal initializes every field at once | No equivalent | |
| D6 getter and setter receivers | No getters or setters | No equivalent | |
| Freezing and thawing | `let frozen = v; let mut thawed = frozen;` moves between bindings of different mutability | Same | `docs/09_ownership.md` says Rust has no equivalent. The moves are the same, but interior mutability keeps Rust's freeze from being deep |

The differences come from four sources:

- **The runtime.** Single-threaded JavaScript with a garbage collector removes
  data races and dangling pointers. That permits aliased `&mut` in F2 and E2,
  borrows that outlive their owner in L1, and the per-call closure borrows in E4.
- **A deep immutability guarantee.** Escalier's invariant promises that an
  immutable value never changes. Rust's interior mutability would break that
  promise, so T3 has no escape hatch.
- **Inference over annotation.** F3, L2, and L3 infer what Rust requires the
  user to write: parameter ownership, closure moves, and signature lifetimes.
- **TypeScript heritage.** T5, D5, and D6 cover `readonly` fields, getters, and
  setters, which Rust does not have.

## Checking model

The rules suggest one checker, which is the target of the simplification:

1. Each flow site lowers to one event: copy, borrow of a kind, or move. Each
   borrow also gets the lifetime L1 gives it.
2. Each read, write, and call lowers to a use event on a place. A call that may
   run a closure also produces the E4 uses.
3. The lifetime solver settles F3's escape questions from the outlives
   constraints.
4. One forward pass over the control-flow graph checks E1, E2, and E3 against the
   events and the liveness of each path.

Type rules T1 through T7 stay in the type checker, where they already sit.

## Open decisions

The solver enforces D1 through D8 today, but no document states them. D9 and D10
are design questions the rules above leave open. Each needs a choice before the
rules are final. The current behaviour comes from reading the code, and the file
references point at where it lives.

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

Recommendation: keep the error and add it as a type rule, T8.

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

Recommendation: keep it. T5 already says one initialization, so this needs no
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
say the compiler still inserts the borrow. Other flow sites still borrow without
the `&`. The compiler accepts `val c: &{x: number} = p`, and a store into a
`&` field without the `&` moves the source, as B1 describes.

Recommendation: adopt F4, which extends the call-site requirement to every flow
site, and update the docs to match.

### D8. A global write moves a borrow

`consumeAtGlobalWrite` records a move for any reference-shaped source, including
a borrow ([moves.go](../../internal/solver/moves.go)). This contradicts F2, under
which a borrow is copied and never consumed.

Recommendation: drop the special case. A borrow stored into module-level state
is copied by F2 and gets a `'static` lifetime by L1. E2 then keeps any
conflicting path from forming for the rest of the program.

### D9. Whether an escaping value moves or borrows

F3 makes a destination with no written type owned when the value escapes, so an
escape always moves the source. L1 allows a second answer. The destination could
be a `'static` borrow instead, and the source would stay usable under E2.

Take a library that memoizes its argument into module-level state:

```js
const seen = new Map();
export function remember(value) { seen.set("last", value); }
```

The converter can declare the parameter three ways:

| Declaration | Caller after `remember(...)` |
|---|---|
| `value: {retries: number}` | `opts` is moved, so any later use is an error |
| `value: &'static {retries: number}` | `opts` can still be read, but never written again |
| `value: &'static mut {retries: number}` | `opts` can still be written, but never borrowed as `&` again |

The owned form works for every caller that does not use the value afterward. The
`'static &` form fits a caller that keeps reading the value, which is the common
case for a memoization key or a configuration object. A function whose body
writes the stored value needs one of the other two forms.

Recommendation: infer a `'static &` borrow for an escaping parameter that the
body never writes, and an owned parameter otherwise. A written annotation
overrides the inference either way.

### D10. Recording that a callback is not retained

A function value is a value type, so it carries no lifetime, and nothing in the
rules distinguishes a callee that only calls a callback from one that stores it.
The source-analysis plan for third-party packages expects to record exactly that
fact. [#1267](https://github.com/escalier-lang/escalier/issues/1267) tracks the
same gap for Escalier's own functions.

The options are:

- Give function types an optional lifetime, as in `&'a fn () -> undefined`.
  The lifetime bounds how long the callee may keep the function, and E4 reads it
  to decide how long the captures stay borrowed.
- Add a marker on the parameter that says the callee does not keep it, and treat
  every unmarked function parameter as retained.

Recommendation: the lifetime form. It reuses L1 and E4 instead of adding a rule.

## Known bugs

**B1. A store into a `&` field without `&` moves the source.** The field and
element store paths in `inferFieldAssign` call `consumeOwned` even when the
destination is declared as a borrow
([infer_expr.go](../../internal/solver/infer_expr.go)). The program below
reports `use of moved value 'p'` on its last line. Under F4 it should instead
reject `h.slot = p` for the missing `&`. Written as `h.slot = &p`, it checks
clean today.

```esc
export fn go() -> number {
    val p = {x: 1}
    val q = {x: 2}
    val h: mut {slot: &{x: number}} = {slot: &q}
    h.slot = p
    return p.x + h.slot.x
}
```

**B2. A `'static` immutable path does not block a later write.** Each program
below checks clean, and each should report an E2 error on its last write or
borrow:

```esc
var cache: &{retries: number} = &{retries: 0}

export fn go() -> number {
    val mut opts = {retries: 3}
    cache = &opts
    opts.retries = 4
    return opts.retries
}
```

```esc
declare fn track(value: &'static mut {retries: number}) -> undefined
declare fn read(value: &{retries: number}) -> number

export fn go() -> number {
    val mut opts = {retries: 3}
    track(&mut opts)
    return read(&opts)
}
```

The store and the call themselves are fine under L1. The bug is that E2 does not
treat the `'static` path as live afterward.

**B3. A borrow taken by an annotation alone escapes E3.** The binding `val c:
&{x: number} = p` borrows `p`, but the move check does not see that borrow. The
program below checks clean, and `take` changes the value `c` reads. Written with
`val c = &p`, the same program reports `cannot move 'p' while it is borrowed`.
F4 rejects the annotation-only form, which closes the hole.

```esc
fn take(p: {x: number}) -> number {
    val mut q = p
    q.x = 5
    return q.x
}

export fn go() -> number {
    val p = {x: 1}
    val c: &{x: number} = p
    val n = take(p)
    return c.x + n
}
```

**B4. `mut` does not reach through a mapped type.** The two functions below
declare the same parameter type, once written out and once through
`Readonly<T>`. T3 says `mut` reaches every owned layer, including through type
arguments, so both should check clean. The first does, and the second reports
`cannot constrain immutable object <: mutable object` at the write. An owned
`mut Readonly<{a: {b: number}}>` parameter fails the same way.

```esc
fn resetWritten(x: &mut {readonly a: {b: number}}) {
    x.a.b = 0
}

fn resetMapped(x: &mut Readonly<{a: {b: number}}>) {
    x.a.b = 0
}
```

**B5. A call to an unannotated escaping parameter does not move its argument.**
The compiler infers `store` below as `fn (p: {x: number}) -> undefined`, an
owned parameter, yet `store(a)` records no move and `a.x` checks clean. With
the parameter written as `p: {x: number}`, the same call reports
`use of moved value 'a'`. The unannotated form leaves `a` usable while
`sink` holds the same value, so a later thaw of `a` could write what `sink`
reads.

```esc
var sink = {x: 0}

fn store(p) { sink = p }

fn go() -> number {
    val a = {x: 1}
    store(a)
    return a.x
}
```

**B6. An unannotated parameter that does not escape is inferred as owned.** The
compiler infers `fn read(p) { return p.x }` as
`fn <T0>(p: {x: T0}) -> T0`. F3 makes that parameter a borrow, so the
signature should be `fn <T0>(p: &{x: T0}) -> T0`. Today `read(&b)` reports
`cannot use borrowed &object as owned object`, and `read(b)` is accepted
without moving `b`.

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
- Both docs borrow an owned value with an annotation alone, as in
  `val q: &{x: number} = p`. F4 rejects that form, so those examples change to
  `&p`.
- A call to a `&mut self` method on an immutable binding goes through
  `checkReceiverMut` in [classes.go](../../internal/solver/classes.go), which
  appears to reject it. `docs/08_mutability.md` says the call is accepted.
- An element store `t[i] = x` is typed and moves its source. `docs/09_ownership.md`
  says it is rejected as unsupported and cites
  [#1268](https://github.com/escalier-lang/escalier/issues/1268).
- The docs say a field store moves only when the container outlives the source.
  The solver moves on every store into an owned field. F2 agrees with the solver.
  The container owns the value afterward, so the source cannot keep it.
- `docs/09_ownership.md` says a move is read off lifetime constraints. The solver
  decides moves separately at each flow site with its own conditions. The checking
  model above is the proposal to make the docs true.

## Interop

These rules decide how the TypeScript declaration converter and any source
analysis map a TypeScript type onto the rules above.

**I1. `Readonly<T>` keeps its `readonly` slots, and the borrow's mutability is
decided separately.** TypeScript defines `Readonly<T>` as
`{readonly [K in keyof T]: T[K]}`, which adds `readonly` to each top-level slot
and nothing more. That is T5's `readonly`, and Escalier's prelude defines
`Readonly<T>` the same way. Since `readonly` and `mut` are independent, the
converter keeps `Readonly<T>` as written and picks `&` or `&mut` for the
parameter from its other signals, falling back to I3 when they are uncertain.
In the declaration below, `reset` cannot reassign `x.a` but can still run
`x.a.b = 0`. The Escalier signature `reset(x: &mut Readonly<{a: {b: number}}>)`
allows and forbids the same writes, once B4 is fixed.

```ts
declare function reset(x: Readonly<{a: {b: number}}>): void;
```

When every slot in `T`, and in every layer `T` owns, is `readonly` or holds a
value type, a `&mut` grants no writes. The converter then emits `&T` instead.
`Readonly<{a: number}>` and a recursive `DeepReadonly<T>` both qualify.

`classifyExplicitSignal` in
[mutability.go](../../internal/dts_to_esc/mutability.go) maps `this:
Readonly<T>` to `&self` today. That is right only when no slot reachable
through `T` is writable.

`readonly T[]` needs the same treatment, but Escalier has no array type whose
elements are fixed while their contents stay writable, so that case is still
open.

**I2. A value-type parameter is written as its bare type.** A primitive,
function, or promise parameter is copied, so the converter writes the bare type
and no borrow marker.

**I3. An uncertain answer takes the form that rejects more programs.** When the
converter or an analysis cannot settle a fact, it picks the form under which the
rules reject every program the true answer would reject:

- `&mut` over `&` for a receiver or parameter that may be written.
- Escaping over not escaping for a parameter that may be stored.
- A borrow of the inputs over an owned value for a return that may share
  structure with an input. A shallow copy such as `{...opts}` shares everything
  below its top level, so it returns a borrow of `opts`.

## Conformance tests

Each flow site that [F1](#flow-rules) lists gets one test per column below. The
expected result follows from the rules, so reviewing the table of expected
results reviews the rules. The tests are written as Escalier source with the
full expected diagnostic, as `CLAUDE.md` asks.

| Column | Source | Destination | Then | Expected result | Rule |
|---|---|---|---|---|---|
| copy | value type | any | use the source | Accepted | F2 |
| immutable copy | `&T` | `&T` | use the source | Accepted | F2 |
| narrow | `&mut T` | `&T` | use both | Accepted | F2, T4 |
| widen | `&T` | `&mut T` | none | Mutability mismatch | T4 |
| mutable borrow of immutable owner | owned, written `&mut p` | `&mut T` | none | Mutability mismatch | T7 |
| borrow | owned, written `&p` | borrow | use the source | Accepted | F2, F4 |
| unwritten borrow | owned, written `p` | borrow | none | Missing `&` | F4 |
| move | owned | owned | use the source | Use of moved value | F2, E1 |
| move on one branch | owned | owned, in one branch | use after the join | Use of moved value | E1 |
| outlive owner | owned | borrow that outlives the source's binding | use the destination | Accepted | L1 |
| write after `'static` immutable borrow | owned `mut` | `'static` `&T` | write the source | Exclusivity error | L1, E2 |
| borrow into owned | `&T` | owned | none | Borrow cannot fill owned | T6 |
| write during immutable borrow | owned `mut` | `&T` | write the source, then use the destination | Exclusivity error | E2 |
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
2. Settle D1 through D10 and update `docs/08_mutability.md` and
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
     replaced by F2 and F3.
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
     lifetime solver answering F3.
