# 08 Mutability

Escalier is immutable by default. This differs from TypeScript and JavaScript,
where every object is mutable and `readonly` is the opt-in.

The guarantee the system provides:

> No immutable reference ever observes a mutation. If a value is reachable
> through an immutable reference that is live at some point, then between the
> creation of that reference and its last use, the value is not mutated through
> any other path.

Everything below follows from holding that invariant. The ownership machinery
that enforces it lives in [Ownership](09_ownership.md); this page covers what
`mut` means and where it applies.

## `mut`

A freshly produced value is owned and immutable. Opt into mutability at the
binding pattern. An owned value's mutability belongs to the binding that owns
it, so the editor shows it on the name, as `mut q: {x: number, y: number}`.

```esc
val p = {x: 0, y: 0}                       // p: {x: 0, y: 0}
// p.x = 1                                 // ERROR: cannot mutate 'p' because its
                                           // binding is immutable

val mut q = {x: 0, y: 0}                   // mut q: {x: number, y: number}
q.x = 1                                    // OK

val mut r: {x: number, y: number} = {x: 0, y: 0}
r.x = 1                                    // OK
```

A parameter takes `mut` the same way. `fn f(mut p: {x: number})` has the same
type as `fn f(p: {x: number})`, since the caller moves its argument into `p`
either way. The `mut` lets the body write the value it now owns.

```esc
fn reset(mut p: {x: number}) -> number {
    p.x = 0
    return p.x
}
```

A binding is mutable only when it is written `mut`. That holds for a parameter
too, whether or not it has a type annotation, so `fn f(p) { p.x = 1 }` is an
error that asks for `mut p`.

`mut` never appears inside an owned type. `val r: mut {x: number}` and
`Array<mut Point>` are errors, and the diagnostic names the two forms to write
instead. Write `mut` on the binding, as in `val mut r: {x: number}`, or write
`&mut T` for a mutable borrow.

This applies uniformly to object literals, tuple and array literals, and class
instances. A class instance is immutable by default whether or not the class
declares `&mut self` methods:

```esc
class Counter {
    count: number,
    constructor(&mut self, count: number) { self.count = count },
    increment(&mut self) -> number { return self.count },
}

val c = Counter(0)        // c: Counter — immutable
val mut d = Counter(0)    // mut d: Counter
```

A method declares its receiver's mutability in its own parameter list. `fn m(&self)`
reads and `fn m(&mut self)` writes.

```esc
class Counter {
    count: number,
    constructor(&mut self, count: number) { self.count = count },
    incr(&mut self) { self.count = self.count + 1 },
    read(&self) -> number { return self.count },
}

fn go() {
    val mut c = Counter(0)
    c.incr()          // &mut self — needs the mutable binding
    return c.read()   // &self — reads through either
}
```

A `&mut self` method requires a mutable binding, so calling `incr` on a plain
`val c = Counter(0)` is an error that asks for `val mut c`.

A receiver written without `&` consumes the instance instead of borrowing it.
Calling a `self` or `mut self` method moves the instance into the call, so the
caller cannot use it afterwards. The caller's binding does not need `mut` for
either one. In a `mut self` method, `mut` makes the method's own `self` binding
mutable, the way it does for any other parameter. A consuming method cannot be
called through a borrow, since a borrow has no instance to give up.

```esc
class Builder {
    parts: Array<string>,
    finish(self) -> string { return self.parts.join("") },
}

fn go() {
    val b = Builder([])
    val s = b.finish()   // moves b
    b.finish()           // error: use of moved value 'b'
}
```

## `mut` is deep and uniform

A `mut` binding's mutability propagates through everything the value owns.

```esc
val mut a: {a: {b: {c: number}}} = {a: {b: {c: 0}}}
a.a.b.c = 1                     // OK — every layer is writable
```

It flows through type arguments as well, so it does not stop at a type parameter.
A `val mut xs: Array<Point>` binding owns a mutable array of mutable points,
where you may push, reorder, and reassign elements *and* mutate a point's fields.
A plain `val xs: Array<Point>` is immutable all the way down.

Depth is therefore all-or-nothing. A binding cannot make a container mutable
and leave its elements immutable.

## Mixing mutable and immutable data

The one boundary that stops the propagation is a **borrow**. A `&` or `&mut`
field is a window into another value and carries its own mutability, not the
mutability of the binding that holds it.

```esc
val mut a: Array<&Point> = []    // mutable array of immutable points
val b: Array<&mut Point> = []    // immutable array of mutable points
```

Through `a` you may push, reorder, and reassign elements, but each element is a
read-only window. Through `b` the array's shape is fixed, yet each element lets
you write through to its point. The trade-off is ownership: the array holds borrows,
so the points live elsewhere and must outlive it.

## `readonly`

`readonly` is a field-level modifier that forbids **reassigning** the field.

```esc
fn f(mut p: {readonly id: string, name: string}) {
    p.name = "x"    // OK
    p.id = "y"      // ERROR: cannot assign to 'id' because it is a readonly
                    // property
}
```

`readonly` governs one slot, and `mut` governs everything the value owns. So the
two answer different questions and compose without interacting. A field is never
annotated `mut` on its own. The binding that owns the value decides its
mutability, and the compiler rejects a `mut` on a field with a diagnostic saying
so.

`readonly` also constrains structural compatibility. A `{readonly a: T}` value
cannot satisfy a **writable** `{a: T}` target, since a holder of that target
could reassign through `a` and break the source's guarantee:

```esc
fn f(mut p: {readonly a: number}) {
    val mut q: {a: number} = p
    // ERROR: readonly field 'a' cannot satisfy a writable field requirement
}
```

An immutable target is fine, because nobody can write through it.

The reverse direction — a writable `{a: T}` value against a `{readonly a: T}`
target — depends on whether the target *owns* the value and on how long the
source stays in use. Moving into the target is fine, since the move consumes the
source and leaves no writable path. Borrowing is fine only while no mutable path
is still live:

```esc
fn go() {
    val mut p = {a: 0}
    val q: &{readonly a: number} = p
    p.a = 1                  // ERROR: cannot assign 'p' to immutable 'q':
    val n = q.a              //        'p' is still used mutably after this point
}

fn ok() {
    val mut p = {a: 0}
    p.a = 1                  // last mutable use of p
    val q: &{readonly a: number} = p
    val n = q.a              // OK
}
```

This is the exclusivity rule below, not a `readonly` rule of its own.

`readonly` survives a freeze and a thaw unchanged, since it is part of the
structural shape rather than the binding's mutability.

## Freezing and thawing

There is no utility type that converts between mutable and immutable forms. The
binding's mutability already governs the whole reachable owned structure, so
moving a value into a differently-mutable binding *is* the conversion. Naming
the converted type is tracked in
[#1266](https://github.com/escalier-lang/escalier/issues/1266).

```esc
val mut g = build()
val frozen = g            // freeze — consumes g
val mut thawed = frozen   // thaw — consumes frozen
```

Both are free at runtime. Soundness comes from the move consuming the source. See
[Ownership](09_ownership.md).

## Exclusivity

Two paths to one value may not disagree about whether it can change. An immutable
borrow and a mutable path to the same value may never both be live. Within one
kind, aliasing is free: several immutable borrows may be live at once, and so may
several mutable ones.

Liveness is what makes this workable. The check runs over the control-flow graph,
so a path that is never used again does not block a later transition — only an
overlap does.

```esc
fn conflicting() {
    val mut p = {x: 0}
    val q: &{x: number} = p   // ERROR: cannot assign 'p' to immutable 'q':
    p.x = 1                   //        'p' is still used mutably after this point
    val n = q.x
}

fn fine() {
    val mut p = {x: 0}
    p.x = 1                   // the mutable path ends here
    val q: &{x: number} = p   // OK — nothing mutable is live past this point
    val n = q.x
}
```

The two functions differ only in statement order. In `conflicting` the write to
`p` comes after `q` is created and `q` is read afterwards, so the two paths
overlap. In `fine` the write is `p`'s last use, so no mutable path survives.

Exclusivity between two `&` borrows of one value is specified but not yet
enforced. Taking `&p` and `&mut p` and using both is accepted today; see
[#794](https://github.com/escalier-lang/escalier/issues/794).

## Interop

TypeScript's standard library ships paired mutable and immutable versions of a
few classes — `Array`/`ReadonlyArray`, `Set`/`ReadonlySet`, `Map`/`ReadonlyMap`.
Escalier uses those pairs to work out which methods on the class mutate their
receiver. Most classes in the TypeScript ecosystem do not make the distinction,
so for those the receiver's mutability has to come from somewhere else.

For the JavaScript standard library, that source is **ECMA-262 itself**. Each
builtin is specified as a numbered algorithm, and an algorithm states directly
whether it performs a mutating abstract operation on a value and whether it
returns an input rather than a freshly allocated object. Those facts are
extracted mechanically and used to annotate the `std:*` packages, giving five
things per method:

1. whether it mutates its receiver, so `&self` or `&mut self`;
2. each other parameter's disposition — read-only borrow, in-place mutable
   borrow, or escape;
3. whether the return value borrows the receiver or a parameter;
4. which exceptions it can throw, as a candidate `throws` clause;
5. which exceptions a promise-returning method rejects with, as a candidate `E`
   in `Promise<T, E>`.

This replaces name-based heuristics, which guess from a method's name and get
cases like `String.prototype.replace` wrong — the name looks mutating, but the
method returns a fresh string.

The `web:*` packages have no equivalent machine-readable source in ECMA-262. The
closest analogue is WebIDL's `[Throws]`, `[NewObject]`, and `[SameObject]`
extended attributes. Node builtins have neither and stay hand-authored.

Mutability, borrows, and lifetimes are all editable inline in the `.esc` source
for each pseudo-package, with no separate override or merge layer.
