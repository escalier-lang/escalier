# 09 Ownership

Escalier tracks, for every reference-shaped value, which binding **owns** it and
which bindings **borrow** it. This is what lets the compiler guarantee the
property [Mutability](08_mutability.md) states:

> No immutable reference ever observes a mutation. If a value is reachable
> through an immutable reference that is live at some point, then between the
> creation of that reference and its last use, the value is not mutated through
> any other path.

Correctness comes first: the invariant holds on every accepted program. Precision
is the secondary goal — among the programs that preserve it, reject as few as
possible.

## Owned and borrowed

At each point in the program, an object, tuple, array, or class instance is
either owned by a binding or borrowed from an owner. The distinction is written
in the type.

| | owned | borrowed |
|---|---|---|
| **immutable** | `{x: number}` | `&{x: number}` / `&'a {x: number}` |
| **mutable** | `mut {x: number}` | `&mut {x: number}` / `&'a mut {x: number}` |

- **Owned** — the binding is the value's owner and the value's lifetime is the
  binding's own region.
- **Borrowed** — the value is a reference into storage owned elsewhere, valid
  only for the borrow's lifetime, written with a leading `&`.

Ownership and mutability are orthogonal. Ownership decides who is responsible for
the value and whether a transfer consumes the source. Mutability decides whether
writes are allowed.

Primitives, functions, and promises are **value types**. They are never wrapped
in a borrow, ownership does not apply to them, and they are freely duplicated.

## How ownership is decided

1. **Freshly produced values are owned, and immutable by default.** An object,
   tuple, or array literal and a class constructor call all yield a value the
   binding owns. Opt into mutability at the binding pattern, `val mut p = ...`,
   or with a `mut` annotation.
2. **A parameter is owned or borrowed according to its annotation.** `p: &T` and
   `p: &mut T` borrow, and the caller keeps the value. A bare `p: T` or
   `p: mut T` is consuming: the signature declares an ownership transfer. An
   unannotated parameter is inferred from whether the body lets the value escape.
3. **A bare annotation is owned; a `&` annotation borrows.** This holds in every
   annotation position — a binding, a return type, a field, a parameter. So
   `val q: &{x: number} = p` borrows `p` and leaves it usable, while
   `val q: {x: number} = p` moves `p` into `q` and consumes it.
4. **Member reads borrow the receiver.** `obj.f` yields a borrow of the field for
   a lifetime bounded by the receiver, not a fresh owned value.
5. **Escape forces ownership and a lifetime.** A value that flows into storage
   outliving its current binding has its lifetime constrained to outlive the
   destination.

A binding can also borrow with an explicit `&` on the initializer, which avoids
repeating the pointee's shape:

```esc
val q = &p       // q: &Point — immutable borrow
val r = &mut p   // r: &mut Point — mutable borrow; requires p to be owned-mutable
```

Passing an **owned** value to a borrowing parameter takes the same `&` or `&mut`,
following Rust. The borrow is written at the call rather than inferred from the
signature, so a reader sees at the call site that the callee only borrows:

```esc
fn read(p: &{x: number}) -> number { return p.x }
fn bump(p: &mut {x: number}) { p.x = 1 }

fn go() {
    val mut p = {x: 0}
    bump(&mut p)
    val n = read(&p)
}
```

An argument that is *already* a borrow passes as-is; there is nothing to borrow
again.

**Method receivers are the exception.** Calling a method whose `self` is borrowed
needs no marker — `c.read()`, never `(&c).read()` — because the receiver's
mutability is already written on `self` in the method's own parameter list and
there is only ever one receiver to mark.

The call-site `&` is not required yet. Today the compiler inserts the borrow
itself, so both `read(p)` and `read(&p)` are accepted.

## Copy, borrow, move

At every site where a value flows from a source into a destination, the transfer
has one of three outcomes.

1. **Copy.** The source is a value type, so the transfer never consumes it. An
   immutable borrow is copyable too: copying one yields another immutable borrow
   of the same value.
2. **Borrow.** The destination takes a reference whose lifetime is bounded within
   the source's region. The source keeps ownership and stays usable.
3. **Move.** Ownership transfers out of the source binding, which is **consumed**.
   Any later use of it is a use-after-move error.

Binding a borrow never consumes it. Only owned values move.

### What counts as escape

Escape is the single trigger for a move. An owned value moves when it flows into:

- a longer-lived binding, such as a module-level or outer-scope one;
- a field or element of an object that outlives the source;
- a `return`, since the value outlives the call frame;
- an argument whose parameter the callee lets escape;
- a closure that itself escapes, capturing the value.

The escaping-argument case is the one that depends on the callee's body rather
than on its signature. `store` below writes its parameter into module-level
state, so the value has to outlive the call, and passing to it moves:

```esc
var sink: {x: number} = {x: 0}

fn store(p: {x: number}) { sink = p }

fn go() {
    val p = {x: 1}
    store(p)
    val n = p.x     // ERROR: use of moved value 'p'
}
```

A callee that only reads its parameter lets nothing escape, so the same call
shape borrows and leaves the source usable.

A value does **not** escape when it flows into a strictly shorter-lived
destination: a non-escaping argument or a local reborrow. Those are borrows, and
the source is retained.

Binding is the exception to that shape. A `val` or `var` binding of an owned
value moves it whatever its scope, because the new binding takes ownership rather
than borrowing for a while:

```esc
val p = {x: 0}
if true { val q = p }       // moves p, even though q dies first
p.x                         // ERROR: use of moved value 'p'
```

Annotate the destination `&` to borrow instead.

The escape verdict is read off the lifetime constraints rather than recomputed. A
value escapes exactly when its lifetime is forced to outlive its binding's scope.

### Use-after-move

```esc
val p = {x: 0}              // p owns a fresh value
storeGlobally(p)            // p escapes into global state — moved, p consumed
print(p.x)                  // ERROR: use of moved value 'p'
```

"Use" means reading, mutating through, calling a method on, moving again, or
borrowing. A move also requires the source to have no live borrow at the move
point, since the move would consume storage the borrow still points into.

The check runs as a post-pass over the control-flow graph, so a move on one
branch consumes the binding only on paths where it happens, and a move on a back
edge is still caught at a use above it.

### Affine, not linear

A value may be consumed **at most** once. A binding that is never moved is simply
dropped at the end of its scope. There is no obligation to move or explicitly
destroy anything, and no use-before-anything requirement. JavaScript is garbage
collected, so drops need no generated code.

## Flow sites

| Site | Outcome |
|---|---|
| `val` / `var` binding | Copy a value type, duplicate a borrow, move an owned value |
| Reassignment | Same decision as a binding; the old value is dropped |
| Field or element store | Move when the container outlives the source, borrowed containers included |
| `return` | Move, unless the value is a value type or an already-outliving borrow |
| Function argument | Borrow for a `&` parameter, written `&`/`&mut` at the call; move for a bare owned one |
| Closure capture | Move into an escaping closure, borrow into a local one; see below |
| Destructuring | Per part, following the same rules |
| `match` arm bindings | Per part, consistent with destructuring |

Whether the container is owned or borrowed does not change the field-store row.
What matters is how long the container's storage lives, and a `&mut` container
borrows from something that already outlives the value being stored:

```esc
fn put(c: &mut {slot: {x: number}}, v: {x: number}) { c.slot = v }

fn go() {
    val mut c = {slot: {x: 0}}
    val v = {x: 1}
    put(&mut c, v)
    val n = v.x     // ERROR: use of moved value 'v'
}
```

An owned value's mutability belongs to the binding and defaults to immutable, so
moving into a plain `val` freezes it and `val mut` keeps it mutable. A borrow's
mutability belongs to its `&`/`&mut` type and is inherited on copy. It can be
narrowed by annotation but never widened, since a view cannot grant itself access
the owner withheld:

```esc
val mut p = {x: 0}

val q = &mut p
val r: &{x: number} = q        // OK — narrowing a mutable view to a read-only one

val s = &p
val t: &mut {x: number} = s    // ERROR: cannot constrain immutable object <: mutable object
```

## Partial moves

Moving one field out of an owned object consumes that field's slot, not the whole
object.

```esc
val pair = {a: makeWidget(), b: makeWidget()}
storeGlobally(pair.a)      // moves pair.a out
print(pair.b.id)           // OK: pair.b is untouched
print(pair.a.id)           // ERROR: use of moved value 'pair.a'
```

Moves and borrows are tracked at the same path granularity, so `&obj.f` locks
only that path and a disjoint sibling `obj.g` stays independently usable. A path
the checker cannot prove disjoint, such as `arr[i]` versus `arr[j]`, falls back to
a container-level borrow.

## Borrow phases

A mutable owned value may be borrowed many times. Its borrows fall into two
phases that never overlap:

- multiple immutable borrows may be live at once, **or**
- multiple mutable borrows may be live at once,

but an immutable borrow's lifetime and a mutable borrow's lifetime for the same
value may never overlap. The owner's own write path counts as a mutable path.

Aliasing within one kind is free:

```esc
val mut a = {x: 1}
val b = &mut a       // mutable borrow
val c = &mut a       // a second one — allowed
b.x = 2
c.x = 3
```

Multiple simultaneous mutable borrows are safe because Escalier compiles to
single-threaded JavaScript. There is no data race to exclude, and the invariant
forbids only mixing the two kinds, not aliasing within one kind.

Mixing them is decided by *overlap*, not by which borrows exist. Running the
mutable phase to completion before the immutable one begins is fine:

```esc
fn sequential() {
    val mut p = {x: 0}
    val a = &mut p
    a.x = 1          // the mutable phase ends here
    val b = &p
    val n = b.x      // OK — the immutable phase starts after it
}
```

Interleaving them is not. Here `b` is read after a write through `a`, so an
immutable view observes a mutation:

```esc
fn overlapping() {
    val mut p = {x: 0}
    val b = &p
    val a = &mut p
    a.x = 1
    val n = b.x      // should be rejected — b is live across the write
}
```

Escalier catches that today only when the immutable view is created by
annotation rather than by an explicit `&`:

```esc
val mut p = {x: 0}
val b: &{x: number} = p   // ERROR: cannot assign 'p' to immutable 'b':
p.x = 1                   //        'p' is still used mutably after this point
val n = b.x
```

`overlapping` above is accepted as written; exclusivity between two `&` borrows
of one value is specified but not yet enforced. See
[#794](https://github.com/escalier-lang/escalier/issues/794) and
[Mutability](08_mutability.md).

## Freezing and thawing

Freezing means moving a value into an immutable binding; thawing means moving it
into a `val mut` binding. Because `mut` is deep and uniform, both reach the whole
structure the value owns.

```esc
val mut g = build()
g.peers[0].value = 20     // OK — mutable to the leaves

val frozen = g            // freeze: consumes g, deeply immutable
// frozen.peers[0].value = 5   // ERROR

val mut thawed = frozen   // thaw: consumes frozen, mutable again
thawed.peers[0].value = 7
```

Neither transition costs anything at runtime. The value is unchanged and only the
type is stricter or looser. Soundness comes from the move: the freeze requires
every mutable path to be dead, and the thaw requires no live immutable observer.

The "only binding" requirement is load-bearing, and the checker enforces it by
tracking aliases across the freeze:

```esc
val mut g = build()
val first = g.peers[0]    // first: &mut Node — a live mutable path into the component
val frozen = g            // ERROR: a mutable path into the component is still live
first.value = 5           // the later use that keeps `first` live across the freeze
```

Thawing is not a faithful inverse for a value that mixes mutable and immutable
edges. The freeze makes every internal edge `&` and the thaw makes every internal
edge `&mut`, so an edge originally authored `&` comes back `&mut`. That is sound,
since the consuming move leaves the thawed binding the sole owner, but wider than
the author wrote.

## Lifetimes

A borrow's lifetime is inferred. It appears in a rendered type only when it is
**load-bearing** — when it connects an input to an output, or escapes to
`'static`.

```esc
fn f(p: &mut {x: number}) -> &{x: number} {
    val q: &{x: number} = p
    return q
}
// inferred: fn <'a>(p: &'a mut {x: number}) -> &'a {x: number}
```

A lifetime that connects nothing is dropped, leaving the bare `&{x: number}`. The
`&` marker itself is never hidden, so owned and borrowed are always
distinguishable in a displayed type.

Lifetimes may be named and bounded explicitly, which is what a body-less
declaration needs:

```esc
declare fn id<'a>(p: &'a {x: number}) -> &'a {x: number}
declare fn f<'a, 'b: 'a>(p: &'a {x: number}, q: &'b {x: number}) -> &'a {x: number}
```

`'a: 'b` reads "`'a` outlives `'b`". A declared lifetime that connects nothing is
elided from the rendered signature, and `'a: 'static` renders as `'static`
directly.

`'static` is the lifetime of a value that escapes into permanent storage. A
borrow forced to `'static` may never be the target of a mutability transition,
since an outside reference to it survives indefinitely.

### Displayed types are writable types

The governing rule for how any of this is rendered:

> A type annotation should match the inferred type. The displayed signature is
> always a valid annotation, and writing it back produces the same type.

The compiler fills in a type the program did not write only at an explicit hole,
such as the `_` in `fn () -> _ throws _`.

## How this compares to Rust

Escalier's ownership model is Rust's, with the aliasing rule relaxed and the
memory-safety motivation removed.

**What is the same**

- Values are owned by one binding, and ownership transfers on a move.
- A move consumes the source, and use after move is a compile-time error.
- Borrows are written `&` and `&mut`, with lifetimes inferred and elided where
  they connect nothing.
- Lifetimes are named `'a` and bounded `'a: 'b`, with `'static` for values that
  escape permanently.
- Ownership is affine: a value may be consumed at most once, with no obligation
  to consume it.
- Moves and borrows are tracked per path, so a field can be moved or borrowed
  while a disjoint sibling stays usable.
- An immutable borrow and a mutable one may never be live at the same time.

**What is different**

| | Escalier | Rust |
|---|---|---|
| Motivation | Preserving the immutability guarantee | Memory safety and data-race freedom |
| Multiple `&mut` | Allowed simultaneously | Forbidden; `&mut` is exclusive |
| Drops | None; the runtime is garbage collected | `Drop` runs at scope end |
| `Copy` trait | No such bound; primitives, functions, and promises are value types | `Copy`/`Clone` bounds on type parameters |
| Lifetimes as a kind | Ordinary types, so a type parameter can be instantiated at a borrow | A separate kind that must be threaded explicitly |
| Call-site borrows | `&` / `&mut` on the argument, none on a method receiver | The same |
| Mutability depth | Deep and uniform through owned structure | Per-reference, and interior mutability via `Cell`/`RefCell` |
| Freeze and thaw | A move into a `val` or `val mut` binding | No equivalent |
| Concurrency | Out of scope; the target is single-threaded | `Send`/`Sync` built on exclusive borrowing |

The multiple-`&mut` relaxation is the substantive one. Rust needs `&mut` to be
unique because two mutable references can race and because a live `&` must not
see a write through an aliased `&mut` in a compiled, unmanaged setting. Escalier
targets single-threaded JavaScript with a garbage collector, so neither concern
applies. What remains is the immutability guarantee, and that only requires the
two *kinds* of borrow never to overlap — not that mutable borrows be unique. So
`&mut`-to-`&mut` aliasing is legal, and it never consumes the source.

The lifetime-as-an-ordinary-type choice follows from the same simplification.
`&'a T` is a type like any other, so instantiating `fn id<T>(x: T) -> T` at a
borrow yields `fn (&'a U) -> &'a U` with no separate lifetime parameter to
thread. Explicit lifetime variables are needed only to relate two borrows that
elision cannot disambiguate.

There is deliberately no `Copy` bound. Generic code treats a type parameter as
non-duplicable, the conservative affine assumption, so a body may use a `T` value
at most once. A function that needs to reuse its argument takes it by `&T` and
reads through the borrow:

```esc
fn dup<T>(x: &T) -> [&T, &T] { return [x, x] }
```

Copying a reference is not a move, and the result is two immutable borrows of one
value, which the borrow-phase rule permits. A function that genuinely duplicates
an *owned* generic value is not expressible; it must be written against a
concrete value type or take a borrow.

## What ownership does not cover

Out of scope by design:

- **Concurrency and data races.** Excluding them would require Rust-style
  exclusive borrowing, which Escalier deliberately gives up.

Specified above but not yet enforced:

- **Escaping closure captures.** Capturing a value in a closure that escapes is
  specified as a move, but the checker does not consume the captured binding
  today, so the later use goes unreported.
- **Element stores.** `t[i] = x` is rejected as unsupported rather than treated
  as an escape.
- **Reassignment clearing a move.** Reassigning a `var` after it was moved gives
  it a fresh value, but the analysis still reads the binding as moved, so a use
  after re-initialization is reported spuriously.

The other escape sites — a longer-lived binding, a field store, a `return`, and a
consuming argument — do consume their source.
