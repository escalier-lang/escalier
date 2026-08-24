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
binding pattern or with a `mut` annotation.

```esc
val p = {x: 0, y: 0}                       // p: {x: number, y: number}
// p.x = 1                                 // ERROR: p is immutable

val mut q = {x: 0, y: 0}                   // q: mut {x: number, y: number}
q.x = 1                                    // OK

val r: mut {x: number, y: number} = {x: 0, y: 0}
```

This applies uniformly to object literals, tuple and array literals, and class
instances. A class instance is immutable by default whether or not the class
declares `mut self` methods:

```esc
class Counter {
    count: number,
    constructor(mut self, count: number) { self.count = count },
    increment(mut self) -> number { return self.count },
}

val c = Counter(0)        // c: Counter — immutable
val mut d = Counter(0)    // d: mut Counter
```

A method declares its receiver's mutability in its own parameter list. `fn m(self)`
reads, and `fn m(mut self)` writes. A `mut self` method may only be called through
a mutable binding.

## `mut` is deep and uniform

A `mut` applies to a type and propagates through everything the value owns.

```esc
val a: mut {a: {b: {c: number}}} = {a: {b: {c: 0}}}
a.a.b.c = 1                     // OK — every layer is writable
```

The `mut` goes on the type, not on the outermost layer of it. Writing
`val mut a: {a: {b: {c: number}}} = ...` is a conflict: the binding pattern asks
for a mutable value and the annotation names an immutable one.

It flows through type arguments as well, so it does not stop at a type parameter.
`mut Foo<Point>` makes both `Foo`'s body and the `Point` it holds writable. The
container case follows: `mut Array<Point>` is a mutable array of mutable points,
where you may push, reorder, and reassign elements *and* mutate a point's fields.
`Array<Point>` is immutable all the way down.

Depth is therefore all-or-nothing. There is no "mutable container, immutable
element" type built out of `mut` alone.

## Mixing mutable and immutable data

The one boundary that stops the propagation is a **borrow**. A `&` or `&mut`
field is a window into another value and carries its own mutability, not the
enclosing modifier's.

```esc
type A = mut Array<&Point>       // mutable array of immutable points
type B = Array<&mut Point>       // immutable array of mutable points
```

In `A` you may push, reorder, and reassign elements, but each element is a
read-only window. In `B` the array's shape is fixed, yet each element lets you
write through to its point. The trade-off is ownership: the array holds borrows,
so the points live elsewhere and must outlive it.

## `readonly`

`readonly` is a field-level modifier that forbids **reassigning** the field. It
says nothing about the depth of the value the field holds.

```esc
type Config = {readonly id: string, name: string}
```

So `readonly` governs the slot and `mut` governs depth. The two compose without
interacting.

`readonly` also constrains structural compatibility. A `{readonly a: T}` value
cannot satisfy a writable `{a: T}` target, because a holder of the target could
reassign through `a` and break the source's guarantee. The reverse is allowed: a
writable `{a: T}` value satisfies a `{readonly a: T}` target, since the target
only ever reads.

`readonly` survives a freeze and a thaw unchanged, since it is part of the
structural shape rather than the `mut` wrapper.

## Freezing and thawing

There is no utility type that converts between mutable and immutable forms. The
binding's mutability already governs the whole reachable owned structure, so
moving a value into a differently-mutable binding *is* the conversion.

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

This is checked with liveness analysis over the control-flow graph, so a borrow
that is never used again does not block a later transition.

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
