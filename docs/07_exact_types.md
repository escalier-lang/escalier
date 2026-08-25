# 07 Exact Types

TypeScript uses structural subtyping, so an object conforms to a type as long as
it has all the required properties. Extra properties are fine.

That has a consequence worth naming: given a value of some object type, you can
never be sure it has no extra properties. It is why TypeScript types
`Object.keys` as `string[]`. With exact types the same call is an array of the
declared keys — `Array<"x" | "y">` for a `{x: number, y: string}` — because the
key set is known to be closed.

Escalier does better by distinguishing types known to have no extra members from
types that might, and it makes the former the default.

## Syntax

A **former** is a type constructor that builds a type out of parts — an object,
a tuple, a function. A trailing `...` opts one into inexactness.

```esc
type ExactPoint   = {x: number, y: number}
type InexactPoint = {x: number, y: number, ...}

type ExactTuple   = [string, number]
type InexactTuple = [string, number, ...]
```

Unions and intersections have no exactness of their own. A union is always
closed, and writing a trailing `...` after a `|` is rejected with a diagnostic
pointing at `string`, `number`, or `unknown` as the way to name an open set of
values.

Exactness is about whether the set of inhabitants of a *value shape* is open, and
neither former describes one. An intersection is a constraint combinator, so the
dual reading would mean hidden extra constraints — fewer inhabitants, the
opposite of what inexact means everywhere else. The exactness of an
intersection's *result* is derived from its operands during normalization and
lands on the resulting object or tuple.

## Semantics

The rule is one-way: exact is a subtype of inexact, not the reverse.

```esc
fn f(p: ExactPoint, q: InexactPoint) {
    val a: InexactPoint = p   // OK
    val b: ExactPoint = q     // ERROR: q may have members ExactPoint does not declare
}
```

Exact against exact requires the *same* member set, with no width subtyping.
Inexact against inexact is ordinary structural width subtyping.

Rest patterns and spreads carry exactness through:

```esc
val {x, ...rest} = p    // rest is exact
val {x, ...rest} = q    // rest is inexact

val cp = {color, ...p}  // cp is exact
val cq = {color, ...q}  // cq is inexact
```

## What is exact by default

- Object, tuple, and array **literals** infer as exact.
- A shape **inferred from usage** is exact too. Once body inference finishes, the
  row is closed.
- A **class instance** is exact when the class is `final`. A non-`final` class may
  have subclass instances carrying extra members, so its instance type is inexact.
- A written **function value** is exact, meaning it accepts exactly the arities its
  parameter list declares.
- Everything imported from a TypeScript `.d.ts` file is **inexact**, in every
  category. A `.d.ts` file has no way to say a shape is closed.

## Opting out with `open`

Usage-inferred exactness is right for application code and wrong for a
row-polymorphic helper, which wants to accept anything carrying the fields the
body touches. Mark such a parameter `open`:

```esc
import "std:math"

fn dist(open p) {
    return math.sqrt(p.x * p.x + p.y * p.y)
}
// inferred: fn (p: {x: number, y: number, ...}) -> number
```

Without `open`, the same function infers `{x: number, y: number}` and a caller
passing an object with a third field is rejected.

The default is chosen for the typical audience. Most Escalier code is application
code, where exact-by-default catches extra-field bugs at the call site. Library
authors writing row-polymorphic helpers pay one keyword — and they are the ones
most likely to be writing explicit annotations anyway, where exactness is stated
outright rather than inferred.

## Function exactness

Direct calls reject extra arguments whatever the exactness. Exactness governs
**callback subtyping** instead.

A function type accepts a range of argument counts: `[required, declared]` when
exact, `[required, ∞)` when inexact. `G <: F` when `G` accepts every count `F`'s
holders may invoke with, with parameters contravariant and the return covariant.

So the familiar "a one-parameter callback works where three are supplied" rule
is the *inexact* case, and only that case. A written function value is exact, so
it has to match the slot's arity:

```esc
declare fn hof(cb: fn (a: number, b: number, c: number) -> number) -> number

val r = hof(fn (a) { return a })
// ERROR: cannot constrain function of arity 1 <: function of arity 3

val s = hof(fn (a, ...) { return a })   // OK — [1, ∞) covers 3
```

See [Functions](02_functions.md) for the same rule from the caller's side.

## Exactness and type-level operators

Exactness changes what the operators compute, which is the whole point of
tracking it.

```esc
type ExactTup = [string, number]
keyof ExactTup                   // 0 | 1

type InexactTup = [string, number, ...]
keyof InexactTup                 // number

type ExactObj = {x: number, y: string}
ExactObj[keyof ExactObj]         // number | string

type InexactObj = {x: number, y: string, ...}
InexactObj[keyof InexactObj]     // unknown
```

A mapped type carries its operand's exactness onto its result, so a utility
built from one preserves the distinction rather than flattening it:

```esc
type Partial<T> = {[K]?: T[K] for K in keyof T}

Partial<ExactObj>     // {x?: number, y?: string}
Partial<InexactObj>   // {x?: number, y?: string, ...}
```

Exactness is also what makes `match` exhaustiveness decidable. A union is closed,
so it is covered once every member has a branch. An inexact object or tuple
scrutinee admits values no pattern can name, so it always needs a catch-all.

A rest pattern still *matches* an inexact scrutinee and binds whatever it did not
name, so `{x, ...rest}` reads `x` and collects the undeclared members into
`rest`. What it does not do yet is discharge the catch-all requirement — the
coverage check asks for a catch-all whatever the arms look like. See
[Pattern Matching](04_pattern_matching.md).

## Ownership is orthogonal

A `&` or `mut` wrapper carries its inner type's exactness through unchanged. The
mutability and lifetime axes neither tighten nor loosen exactness, and the two
compose without interacting.

## Interop

TypeScript has no exact types, so everything imported from a package is inexact.

To keep Escalier types across a package boundary, emitted declarations carry an
`@escalier-type` JSDoc tag holding the original annotation in source form. The
emitted TypeScript is a best-effort erasure for plain TypeScript consumers; the
tag is the source of truth for an Escalier consumer re-importing the declaration.

```esc
export declare val p: {x: number, ...}
```

```ts
/** @escalier-type {x: number, ...} */
export declare const p: { x: number };
```

A TypeScript consumer ignores the tag and sees an ordinary structural type. An
Escalier consumer reads it and recovers the inexactness the `.d.ts` form cannot
express. The same mechanism carries tuples, functions, unions, and the
`Exact<T>` / `Inexact<T>` wrappers.

## `Exact<T>` and `Inexact<T>`

The two operators convert between the forms. `Inexact<T>` sets the trailing `...`
marker on its operand and `Exact<T>` clears it.

```esc
type A = Inexact<{x: number}>       // {x: number, ...}
type B = Exact<{x: number, ...}>    // {x: number}
```

An operand carrying no such marker, a primitive or a literal, reduces to itself.
An operand that is still abstract, such as a type parameter, stays symbolic until
it grounds.

The value-level `exact<T>(v)` conversion is a separate thing and is not
implemented. It belongs to codegen, since it lowers to a runtime check rather
than a checker rule.
