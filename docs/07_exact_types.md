# 07 Exact Types

TypeScript uses structural subtyping, so an object conforms to a type as long as
it has all the required properties. Extra properties are fine.

That has a consequence worth naming: given a value of some object type, you can
never be sure it has no extra properties. It is why TypeScript types
`Object.keys` as `string[]` rather than as the union of the declared keys.

Escalier does better by distinguishing types known to have no extra members from
types that might, and it makes the former the default.

## Syntax

A trailing `...` opts a former into inexactness.

```esc
type ExactPoint = {x: number, y: number}
type OpenPoint  = {x: number, y: number, ...}

type ExactTuple = [string, number]
type OpenTuple  = [string, number, ...]
```

Unions carry the same flag. `A | B` is closed and `A | B | ...` may hold members
beyond the ones written. A union may not have a trailing `...` written directly
after a `|`; the compiler suggests `string`, `number`, or `unknown` for an open
set of members.

Intersections have no exactness of their own. Exactness is about whether the set
of inhabitants is *open*, and an intersection is a constraint combinator rather
than a value shape — the dual reading would mean hidden extra constraints, which
is the opposite of what inexact means everywhere else. The exactness of an
intersection's *result* is derived from its operands during normalization and
lands on the resulting object or tuple.

## Semantics

The rule is one-way: exact is a subtype of inexact, not the reverse.

```esc
fn f(p: ExactPoint, q: OpenPoint) {
    val a: OpenPoint = p    // OK
    val b: ExactPoint = q   // ERROR: q may have members ExactPoint does not declare
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
- **Unions** are exact.
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
fn dist(open p) { p.x
                  p.y }
// inferred: fn (p: {x: unknown, y: unknown, ...}) -> undefined
```

Without `open`, the same function infers `{x: unknown, y: unknown}` and a caller
passing an object with a third field is rejected.

The default is chosen for the typical audience. Most Escalier code is application
code, where exact-by-default catches extra-field bugs at the call site. Library
authors writing row-polymorphic helpers pay one keyword.

## Function exactness

Direct calls reject extra arguments whatever the exactness. Exactness governs
**callback subtyping** instead.

A function type accepts a range of argument counts: `[required, declared]` when
exact, `[required, ∞)` when inexact. `G <: F` when `G` accepts every count `F`'s
holders may invoke with, with parameters contravariant and the return covariant.
The familiar "a one-parameter callback works where three are supplied" rule is
the inexact case.

## Exactness and type-level operators

Exactness changes what the operators compute, which is the whole point of
tracking it.

```esc
type Tup = [string, number]
keyof Tup                    // 0 | 1

type OpenTup = [string, number, ...]
keyof OpenTup                // number

type Obj = {x: number, y: string}
Obj[keyof Obj]               // number | string

type OpenObj = {x: number, y: string, ...}
OpenObj[keyof OpenObj]       // unknown
```

An exact union distributes through a conditional over exactly its written
members. An inexact one cannot, since a member it does not name might behave
differently.

Exactness is also what makes `match` exhaustiveness decidable. An exact union is
covered once every member has a branch; an inexact scrutinee always needs a
catch-all. See [Pattern Matching](04_pattern_matching.md).

## Ownership is orthogonal

A `&` or `mut` wrapper carries its inner type's exactness through unchanged. The
mutability and lifetime axes neither tighten nor loosen exactness, and the two
compose without interacting.

## Interop

TypeScript has no exact types, so everything imported from a package is inexact.

To keep Escalier types across a package boundary, emitted declarations carry a
JSDoc field holding the original Escalier type annotation. An Escalier program
importing those declarations reads the annotation and recovers the exact type;
a TypeScript consumer ignores it and sees ordinary structural types.

## Not yet implemented

The `Exact<T>` and `Inexact<T>` type operators are not implemented. Neither is
the value-level `exact<T>(v)` conversion, which belongs to codegen: it lowers to
a runtime check rather than a checker rule.
