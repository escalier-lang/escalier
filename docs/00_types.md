# 00 Types

Escalier's type system is built on **algebraic subtyping**. Every expression has
exactly one principal type, inference never needs annotations to succeed, and
subtyping is a lattice rather than a set of unification rules. The checker lives
in [internal/solver/](../internal/solver/).

Two consequences shape everything below.

- **Types form a Boolean algebra.** Union `|`, intersection `&`, and negation
  `~` are all first-class, with `unknown` at the top and `never` at the bottom.
- **Inference is usage-driven.** A type variable carries a list of lower bounds
  and a list of upper bounds, and every constraint the program imposes lands on
  one of the two. Reading `p.x` records an upper bound on `p`'s variable, saying
  the value must have an `x`. Flowing a value into a position records a lower
  bound on that position's variable, saying the position must accept that value.
  The final type is read off the two lists.

## Primitives and literals

`number`, `string`, and `boolean` are the primitive types, and `null` and
`undefined` are two atoms with one inhabitant each. Every literal also names a
type of its own.

```esc
val a = 5           // a: 5
val c = "hi"        // c: "hi"
val b: number = 5   // b: number
var d = 5           // d: number
```

A `val` keeps the literal type, since the binding can never hold anything else. A
`var` widens to the primitive, since it can be reassigned. An annotation fixes
whichever type it names.

## `unknown` and `never`

`unknown` is the top of the lattice: every type is a subtype of it, and nothing
can be read off a value typed `unknown` without narrowing first. `never` is the
bottom: it has no values, and it is what `throw` and a diverging branch
contribute to a union.

## `any`

`any` is not part of the type system. Where TypeScript's `.d.ts` files use `any`
in a position Escalier can express, the importer lowers it to `unknown`; where it
appears in a constraint or a conditional's pattern, the corresponding Escalier
form uses `unknown` or the `_` marker described below. There is no escape hatch
that silently disables checking.

## Objects and tuples

Object and tuple types are structural and **exact by default** — a value may not
carry members the type does not declare. A trailing `...` opts into inexactness.

```esc
type ExactPoint = {x: number, y: number}
type OpenPoint  = {x: number, y: number, ...}

type Pair       = [string, number]
type OpenPair   = [string, number, ...]
```

Members may be optional or read-only:

```esc
type Config = {
    host: string,
    port?: number,
    readonly id: string,
}
```

`?` marks a member that may be absent. `readonly` forbids reassigning the member.
See [Exact Types](07_exact_types.md) for the subtyping rules and
[Mutability](08_mutability.md) for how `readonly` relates to `mut`.

## Unions, intersections, and negation

```esc
type Id     = string | number
type Both   = {x: number} & {y: number}
type NotNum = ~number
```

`~T` is the set-theoretic complement of `T`: every value `T` rejects. The solver
normalizes unions, intersections, and negations to disjunctive and conjunctive
normal forms rather than guessing which alternative to commit to, so the three
compose in any position, including inside an inference variable's bounds.

Unions are always closed. There is no syntax for a union that may hold members
beyond the ones listed, so `string`, `number`, or `unknown` is how you name an
open set of values.

## Classes

Classes are **nominal**. A value of class `Point` is not assignable to an exact
structural `{x: number, y: number}`, and a structural object is never assignable
to `Point`, however well the fields line up. A class instance does satisfy an
**inexact** object target structurally, so the target's exactness is what decides.

Nominality governs assignability, not pattern matching. An object pattern
destructures a class instance by field:

```esc
fn f(p: Point) {
    val {x, y} = p    // x: number, y: number
    return x
}
```

```esc
class Point {
    x: number,
    y: number,
    getX(self) -> number { return self.x },
}

val p = Point(1, 2)   // p: Point — no `new` keyword
val x = p.x           // x: number
```

Nominality follows the compilation target. Escalier compiles to JavaScript,
where `instanceof` distinguishes two classes with identical fields, so the type
system distinguishes them too.

TypeScript answers differently, and conditionally. It compares two classes by
their public members, so classes with matching public shapes are interchangeable.
Private and protected members are the exception: they match only when both sides
inherit them from the same declaration, which makes a class carrying one behave
nominally. Escalier applies the nominal rule to every class instead of leaving it
to whether the class happens to declare a private member.

A class instance is exact when the class is `final`; a non-`final` class may have
subclass instances, so its instance type stays inexact.

### `sealed` classes

Not implemented; see [#842](https://github.com/escalier-lang/escalier/issues/842).

A `sealed` class sits between `final` and the open default. It may be subclassed
inside the module that declares it, and an `extends` from a module that imports
it is an error.

`final` and `sealed` close different things, and the two axes are orthogonal.

- `final` closes the instance **width**. There are no subclasses, so an instance
  has exactly the declared members and the instance type is exact.
- `sealed` closes the **set of alternatives**. A sealed base still has
  subclasses, so a base-typed value may carry members the base does not declare,
  and its instance type stays inexact. What is closed is the set of classes such
  a value can be at runtime.

Closing the alternatives is what makes a `match` over the hierarchy exhaustive.
The checker can enumerate every permitted subclass and discriminate with
`instanceof`, the same strategy it uses for enums. That buys the one thing
neither existing option does: a shared base carrying fields and methods, with a
closed set of leaves.

So reach for an enum when the cases are independent shapes, and for a sealed
class when they share inherited behavior.

Escalier's `sealed` is not C#'s. C# spells `final` as `sealed` and forbids
subclassing outright, which is the opposite of the concept here.

## Enums

Enums are variant types with their own namespace. An enum name binds both a type
and a namespace of variant constructors.

```esc
enum Color {
    RGB(r: number, g: number, b: number),
    Hex(code: string),
}

val red = Color.RGB(255, 0, 0)   // red: Color
```

See [Enums](05_enums.md).

## Ownership and mutability in types

A type annotation also records whether a value is owned or borrowed, and whether
it is mutable.

| | owned | borrowed |
|---|---|---|
| immutable | `{x: number}` | `&{x: number}` |
| mutable | `mut {x: number}` | `&mut {x: number}` |

A borrow may name its lifetime, as in `&'a mut {x: number}`. These two axes are
covered in [Mutability](08_mutability.md) and [Ownership](09_ownership.md).

## Type aliases and generics

```esc
type Pair<T> = [T, T]
type Tree<T> = {value: T, children: Array<Tree<T>>}

fn id<T>(x: T) -> T { return x }
```

Aliases may be recursive and mutually recursive across files. The dependency
graph orders declarations, so an alias may reference one written later in the
source.

Lifetime parameters are written in the same list, `<'a>`, and optionally bounded
with `'a: 'b`. Functions, classes, interfaces, and type aliases may all take
them; enums may not. See [Ownership](09_ownership.md).

```esc
type Ref<'a, T> = &'a T
class Container<'a> { p: &'a {x: number} }
declare fn id<'a>(p: &'a {x: number}) -> &'a {x: number}
```

## Type-level operators

Escalier has TypeScript's type-level operator surface with syntax adapted to the
rest of the language.

**`keyof`** yields the union of a type's keys.

```esc
type Keys = keyof {x: number, y: string}   // "x" | "y"
```

**Indexed access** reads a member type out.

```esc
type X = {x: number, y: string}["x"]   // number
```

**`typeof`** lifts a value's type into type position.

```esc
val origin = {x: 0, y: 0}
type Origin = typeof origin
```

**Conditional types** are written as `if`/`else` rather than with `extends` and
`?`. The pattern may capture with `infer`.

```esc
type Elem<T> = if T : Array<infer E> { E } else { never }
```

`_` in a pattern is an `infer` clause with no name. The match fills it and the
capture is dropped, which is how a pattern names only the position it cares
about.

```esc
type ReturnType<F: fn (...args: Array<_>) -> _> =
    if F : fn (...args: Array<_>) -> infer R { R } else { never }
```

A type parameter's constraint is written with `:`, as `F` does above.

**Mapped types** are written as a comprehension. Two forms exist and mean the
same thing.

```esc
type Copy1<T> = {[K: keyof T]: T[K]}
type Copy2<T> = {[K]: T[K] for K in keyof T}
```

Modifiers go where they would on an ordinary member. `?` adds optionality, `-?`
removes it, and `readonly` adds read-only.

```esc
type Partial<T>  = {[K]?: T[K] for K in keyof T}
type Required<T> = {[K]-?: T[K] for K in keyof T}
type Readonly<T> = {readonly [K]: T[K] for K in keyof T}
```

A trailing `if` clause filters the key set. TypeScript has no filter clause and
expresses the same thing by remapping a key to `never`; Escalier accepts that
bracketed form too.

```esc
type Pick<T, Ks> = {[K]: T[K] for K in keyof T if K : Ks}
type Omit<T, Ks> = {[K]: T[K] for K in keyof T if K : ~Ks}
```

The same filtering can be written as a key remapping to `never`, which is how
TypeScript spells it, and Escalier accepts that form too:

```esc
type Omit<T, Ks> = {[if K : Ks { never } else { K }]: T[K] for K in keyof T}
```

**Template literal types** build string types from parts.

```esc
type Getters<T> = {[`get${Capitalize<K>}`]: T[K] for K in keyof T}
```

The utility types TypeScript ships in its standard library are ordinary Escalier
aliases written with these operators, not compiler magic. The exceptions are
`Uppercase`, `Lowercase`, `Capitalize`, `Uncapitalize`, and `NoInfer`, which are
checker-resident handlers with no source definition. They come from `std:object`
and `std:function`; see [Imports](03_imports.md).

## The `_` marker

`_` in a type annotation is a hole: it asks the checker to infer that position
instead of fixing it.

```esc
async fn f() -> Promise<_, _> { ... }   // infer both the payload and the rejection
fn g() throws _ { ... }                 // infer what the body throws
```

A displayed type never contains `_`. Everything the compiler infers, it can also
print as a type the program could have written.

## TypeScript interop

Escalier reads TypeScript declarations and emits them, so most types have a
direct counterpart. The places they differ:

| Escalier | TypeScript |
|---|---|
| Objects and tuples exact by default | Always inexact |
| Classes nominal | Structural |
| Enums are variant types with extractors | `enum` is a numeric or string map |
| `~T` negation | No equivalent |
| `Promise<T, E>` carries a rejection type | `Promise<T>` only |
| `mut` / `&` / lifetimes | No equivalent |

Types imported from TypeScript are **inexact** in every category, since a `.d.ts`
file cannot state that a shape is closed. Emitted declarations carry the original
Escalier type in a JSDoc field so that an Escalier program importing them
recovers the exact type.
