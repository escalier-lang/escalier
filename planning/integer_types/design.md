# Integer and integer range types

## 1. Overview

This document adds two subtypes of `number`.

- `int` is the type of every integral number.
- `m..n` is the type of the integers from `m` up to but not including `n`. So
  `0..3` admits `0`, `1`, and `2`, and does not admit `3`.

The `m..n` syntax works both as a type annotation and as an expression. As a
type it names a set of integers. As an expression it builds a range value that
can be iterated:

```esc
type Digit = 0..10

fn digitName(d: Digit) -> string { ... }

for i in 0..10 {     // i: 0..10
    print(digitName(i))
}
```

A bound may be an identifier. In a type annotation the identifier must name a
type that is a subtype of `int`. In an expression it must be a value whose type
is a subtype of `int`.

```esc
type Lo = 0
type Hi = 10
type R  = Lo..Hi     // same as 0..10

fn countTo(n: int) {
    for i in 0..n { ... }   // i: 0..Infinity, which is every int >= 0
}
```

The rest of the document covers the following, in order:

- Section 2 gives the meaning of the types.
- Sections 3 and 4 place them in the lattice next to the other `number` types
  and next to negation.
- Section 5 covers operators.
- Section 6 covers range values.
- Section 7 covers stepped ranges.
- Sections 8 to 11 cover parsing, the solver, codegen, and TypeScript interop.
- Section 12 applies the new types to arrays, tuples, and the rest of the
  standard library.
- Section 13 lists milestones and section 14 lists open questions.

## 2. Semantics

### 2.1. `int`

`int` admits exactly the values for which `Number.isInteger(x)` is true. These
are the finite `number` values with no fractional part.

- `1.0` is an `int`. JavaScript has one `number` value for `1` and `1.0`, and
  the lexer keeps only the parsed value, so `1.0` has the literal type `1`. The
  same holds for `1e3`, whose type is `1000`.
- `-0` is an `int`.
- `2 ** 60` is an `int`. Every `number` with magnitude at or above `2 ** 53` is
  integral, so `int` is not limited to the safe-integer range.
- `NaN`, `Infinity`, and `-Infinity` are not `int`s.
- `1.5` is not an `int`.
- A `bigint` is never an `int`. The two live in different value families. See
  section 3.

The alternative is to bound `int` to the safe integers,
`-(2 ** 53 - 1)` through `2 ** 53 - 1`. That makes `int + int` leave `int` on
ordinary inputs, which would push every arithmetic result back to `number`.
`Number.isInteger` semantics keep `+`, `-`, and `*` closed over `int` except at
overflow to `Infinity`. Section 5.2 discusses that case.

### 2.2. `m..n`

`m..n` is the set of integers `k` with `m <= k < n`. The range is half-open. The
lower bound is included and the upper bound is excluded.

Half-open ranges compose without off-by-one adjustments:

- `0..len` covers the valid indices of a collection of length `len`.
- `a..b | b..c` is exactly `a..c`.
- `a..b & b..c` is `never`.
- `0..n` has exactly `n` members when `n >= 0`.

When `m >= n` the set is empty. A range annotation whose bounds are both literal
and empty, such as `5..5` or `5..3`, is an error, since it almost always means a
typo. An empty range that arises through inference, such as from an alias
instantiation, is `never`.

### 2.3. Unbounded ranges

`Infinity` may be the upper bound and `-Infinity` the lower bound. Neither is an
`int`, and neither is ever a member of the range. They mark an end with no
limit:

| Annotation | Meaning |
|---|---|
| `0..Infinity` | every `int >= 0` |
| `-Infinity..0` | every `int < 0` |
| `-Infinity..Infinity` | `int` |

The checker prints every unbounded interval it computes in this form, which
section 4 relies on.

`Infinity` is the only bound that is not an `int`. `0..1.5`, `0..NaN`,
`0..number`, and `0..string` are errors. So are `Infinity..0` and
`0..-Infinity`, which are empty.

### 2.4. Bounds that are not single integers

A bound may name a type that is not a single integer, such as a type parameter
`N: 0..100` or an alias `type Lo = 0 | 5`. The rule for such a bound is a union
over bound values:

> `L..U` is the union of `l..u` over every `l` in `L` and every `u` in `U`.

When `L` and `U` are single literals this is the plain range. Otherwise it
reduces to `min(L)..max(U)`, where `min` and `max` are taken over the integers
each bound admits. A bound that admits arbitrarily large integers reduces to
`Infinity`, and one that admits arbitrarily small integers reduces to
`-Infinity`:

| Bound types | Annotation | Meaning |
|---|---|---|
| `type Lo = 0 \| 5` | `Lo..10` | `0..10` |
| `type Hi = 5 \| 10` | `0..Hi` | `0..10` |
| | `0..int` | `0..Infinity` |
| | `int..0` | `-Infinity..0` |
| `N: 0..100` | `0..N` | `0..99` |

In the last row `N` ranges over `0` through `99`, and the largest range it
yields is `0..99`. Section 9.5 covers that case. The same rule types value
ranges. `0..n` with `n: int` has element type `0..Infinity` in section 6.1,
and `0..n` with `n: 0..100` has element type `0..99`.

The rule always reduces a bound to a single integer or an infinity, so the
checker never prints a range whose bound is another type.

### 2.5. Reading the bounds of a range type

Two prelude intrinsics read the bounds back out of a type `T` that is a subtype
of `int`:

- `Lower<T>` is the lower bound as `m..n` would write it. That is the smallest
  member of `T`, or `-Infinity` when `T` has no smallest member.
- `Upper<T>` is the exclusive upper bound as `m..n` would write it. That is
  one step past the largest member of `T`, or `Infinity` when `T` has no
  largest member.

| `T` | `Lower<T>` | `Upper<T>` |
|---|---|---|
| `0..10` | `0` | `10` |
| `5` | `5` | `6` |
| `0..10 by 3` | `0` | `12` |
| `0..3 \| 5..8` | `0` | `8` |
| `0..Infinity` | `0` | `Infinity` |
| `int` | `-Infinity` | `Infinity` |
| `never` | `never` | `never` |

For a stepped range, `Upper<T>` follows the printed form from section 7.2, so it
is the last member plus the step. For a union of intervals, the two intrinsics
give the bounds of the smallest range that covers the whole union. A type
parameter stays unreduced until it is instantiated, so `Lower<N>` is a valid
type in a generic signature.

`Lower` and `Upper` return bounds, not members. Intrinsics that return the
smallest and largest members, `Min` and `Max`, can come later if a signature
needs them. `Max<0..10>` would be `9` where `Upper<0..10>` is `10`.

## 3. The `number` lattice

The new types sit between `number` and its integral literals.

```
                 number
                /      \
             int        number & ~int
            /    \           |
   -Infinity..0  0..Infinity        1.5, 0.25, ...
          |
        0..10
       /  |   \
      0   1 ... 9
```

The rules below define the order:

- `int <: number`.
- An integral number literal `k` is a subtype of `int`, and of `m..n` when
  `m <= k < n`.
- A non-integral literal such as `1.5` is not a subtype of `int`, and its meet
  with `int` is `never`.
- `a..b <: c..d` when the first range is empty, or when `c <= a` and `b <= d`.
- `m..n <: int` always.
- `int <: m..n` only when the range is `-Infinity..Infinity`.

The solver groups primitive types into value families. A family is a set of
runtime values that no other family shares, such as numbers, strings, or
booleans. Two atoms from different families always meet at `never`. `int` and
every range belong to the number family, next to `number` and the number
literals. `int` therefore stays disjoint from `string`, `boolean`, `bigint`,
`symbol`, `null`, and `undefined` with no new rules.

The existing family rules treat a literal as narrower than its primitive and two
distinct literals as disjoint. The new atoms add a third kind of member: an
interval of integers. An integral literal `k` behaves as the interval `k..k+1`
in every lattice operation. A non-integral literal is disjoint from every
interval. `RegexType` in the string family is the precedent. It is a set of
strings that sits between `string` and the string literals, with containment
rules for pairs of patterns.

### 3.1. Meets

| Meet | Result |
|---|---|
| `int & number` | `int` |
| `0..10 & int` | `0..10` |
| `0..10 & 5..20` | `5..10` |
| `0..10 & 10..20` | `never` |
| `0..10 & 3` | `3` |
| `0..10 & 12` | `never` |
| `int & 1.5` | `never` |
| `int & string` | `never` |

### 3.2. Joins

| Join | Result |
|---|---|
| `int \| number` | `number` |
| `0..10 \| int` | `int` |
| `0..5 \| 5..10` | `0..10` |
| `0..5 \| 3..10` | `0..10` |
| `0..5 \| 6..10` | stays two atoms |
| `0..5 \| 5` | `0..6` |
| `int \| 1.5` | stays two atoms |
| `1 \| 2 \| 3` | stays three literals |

A union made only of literals keeps its literal form. `1 | 2 | 3` stays as
written rather than becoming `1..4`, so existing programs and snapshots print
as they do today. A literal fuses into a range only when it touches a range in
the same union. The solver still compares such a union as an interval set. That
makes `0..3 <: 0 | 1 | 2` hold even though the right side prints as literals.

### 3.3. Unions on the right of `<:`

The existing rule for `S <: A | B` succeeds when `S` is a subtype of one member.
That rule cannot prove `0..10 <: 0..5 | 5..10`, since neither member covers the
whole range. The union must be normalized into its interval set before that rule
runs. Normalizing merges `0..5 | 5..10` into `0..10` and the check succeeds. The
same pass proves `int <: -Infinity..0 | 0..Infinity`.

### 3.4. Literal widening

`val x = 5` keeps the literal type `5`. `var x = 5` widens to `number` today, and
this design keeps that. Widening to `int` would reject the common
`var total = 0` followed by `total += price`. A `var` that should stay integral
takes an annotation, as in `var i: int = 0`. Section 14 lists widening to `int`
as an open question.

### 3.5. `NaN` and the infinities

`NaN`, `Infinity`, and `-Infinity` each get a type whose one value is that
number. The prelude binds the global values `NaN` and `Infinity` at these
types, along with `Number.NaN`, `Number.POSITIVE_INFINITY`, and
`Number.NEGATIVE_INFINITY`. `val x = Infinity` gives `x: Infinity`, and unary
minus folds `-Infinity` to the `-Infinity` type the way it folds `-5` to `-5`.

The three types sit in the number family next to the literals. Each is a
subtype of `number`. Each is disjoint from the other two, from `int`, and from
every literal. With negation they split `number` into disjoint parts, and the
prelude names the part that excludes all three:

```esc
type finite = number & ~NaN & ~Infinity & ~-Infinity
```

| Part of `number` | Examples |
|---|---|
| `int` | `0`, `-7`, `2 ** 60` |
| `finite & ~int` | `1.5`, `0.1` |
| `NaN` | `NaN` |
| `Infinity`, `-Infinity` | `1 / 0`, `-1 / 0` |

`int <: finite` holds, and every range is a subtype of `finite`.

As a type, `NaN` equals itself, even though the value `NaN` is not equal to
itself at runtime. That has two consequences:

- `x === NaN` is always `false` at runtime, so a program tests for `NaN` with
  a pattern, as in `if val n: NaN = x`, or with `Number.isNaN`.
- A `NaN` pattern or type test compiles to `Number.isNaN(x)`. An `Infinity`
  pattern compiles to `x === Infinity`.

In a type annotation, `-Infinity` parses as a single type, the way `-5` does.

## 4. Negation

`~T` is every value `T` rejects, so `~int` admits strings, objects, `1.5`,
`NaN`, and everything else that is not an integer.

### 4.1. Complements inside `int`

The integer intervals are closed under complement relative to `int`. The
complement of a finite set of intervals is another finite set of intervals, and
section 2.3 gives the printed form of the unbounded ends:

| Type | Normal form |
|---|---|
| `int & ~(0..10)` | `-Infinity..0 \| 10..Infinity` |
| `0..10 & ~5` | `0..5 \| 6..10` |
| `0..10 & ~(3..6)` | `0..3 \| 6..10` |
| `int & ~int` | `never` |
| `int & ~(0..Infinity)` | `-Infinity..0` |
| `int & ~number` | `never` |

The solver checks `A <: ~B` by deciding whether `A & B` is `never`. The meet
rules in section 3.1 answer that for every pair of number atoms, so these
checks hold with no new negation rule:

```esc
val a: ~int = 1.5          // ok, 1.5 & int is never
val b: ~(0..10) = 10       // ok, 10 is outside the range
val c: ~(0..10) = 9        // error
val d: ~int = "hi"         // ok, strings are another family
```

The normal form needs one new rewrite. An interval set met with the complement
of an interval set reduces to their difference. The rewrite gives
`0..10 & ~5` the precise form `0..5 | 6..10` instead of an unreduced residual.

### 4.2. Complements that leave `int`

`number & ~int` is `(finite & ~int) | NaN | Infinity | -Infinity`. The checker
prints the shorter `number & ~int`. No interval names the `finite & ~int`
part, so it stays as a residual in the normal form. Excluding one regex type from another leaves
a residual of the same kind.

`number & ~(0..10)` mixes both cases. It equals
`(number & ~int) | -Infinity..0 | 10..Infinity`. That form is longer than the input and no
easier to read, so the solver prints `number & ~(0..10)` and keeps the interval
split internal.

### 4.3. Refinement in `match`

An arm that matches a range removes it from the scrutinee for the arms that
follow:

```esc
fn bucket(n: int) -> string {
    return match n {
        -Infinity..0 => "negative",
        0..10  => "small",
        x      => `large ${x}`,  // x: 10..Infinity
    }
}
```

Coverage over `int` becomes an interval union. A match on an `int` scrutinee is
exhaustive when the union of its unguarded range, literal, and `int` arms
covers `-Infinity..Infinity`. Replacing the last arm with `10..Infinity => "large"` keeps the
match above exhaustive, since `-Infinity..0 | 0..10 | 10..Infinity` merges to `int`. A finite range scrutinee such
as `0..3` is exhaustive when its arms cover `0`, `1`, and `2`.

A `number` scrutinee is exhaustive with arms `x: int` and `x: ~int`, or with an
`int` arm plus a catch-all.

### 4.4. `Exclude` and conditional types

The prelude defines `Exclude<T, U>` as `if T : U { never } else { T }`, which
distributes over the members of a union `T`. A range is one atom rather than a
union of literals, so `Exclude<0..10, 3>` checks `0..10 : 3`, finds it false,
and returns `0..10` unchanged. `0..10 & ~3` gives the precise `0..3 | 4..10`.

Splitting a range into literals for distribution would turn `0..Infinity` into an
infinite union, so this design does not distribute over ranges. The language
guide should point readers at `T & ~U` for numeric exclusion. Section 14 asks
whether `Exclude` should be redefined in terms of negation.

## 5. Operators

### 5.1. Result types

The prelude gives every arithmetic operator the monomorphic signature
`(number, number) -> number`. The new types need the result to depend on the
operand types:

| Operator | Operands | Result |
|---|---|---|
| `+`, `-`, `*` | `int`, `int` | `int` |
| `+`, `-`, `*` | ranges | interval arithmetic, see 5.3 |
| unary `-` | `int` | `int` |
| unary `-` | `a..b` | `(1-b)..(1-a)` |
| `/` | `int`, `int & ~0` | `finite` |
| `/` | any other | `number` |
| `**` | any | `number` |
| `%` | `int`, `int & ~0` | `int` |
| `%` | `int`, `int` | `number` |
| comparisons | `number`, `number` | `boolean` |

`%` with a zero divisor yields `NaN`, so `int % int` is not closed over `int`.
The result is `int` only when the divisor type excludes `0`. That is a direct use
of negation: `1..Infinity` and `int & ~0` both qualify, since neither contains `0`.
The same divisor makes `/` return `finite`, since dividing an integer by a
nonzero integer cannot overflow.
The lexer does not tokenize `%` yet, so this row applies once `%` lands.

The bitwise operators always yield integers in JavaScript. When they are added
they return `int`. `>>>` returns `0..4294967296`.

### 5.2. Overflow

`int + int` and `int * int` overflow to `Infinity` once the magnitude passes
about `1.8e308`. `Infinity` is not an `int`, so typing the result as `int` is
unsound at that boundary. This design accepts that gap. Values of that size are
rare in integer code, and typing every sum as `number` would make `int` useless
for arithmetic. The language guide states the gap.

The types in section 3.5 can spell the sound result, which is
`int | Infinity | -Infinity`. Using it would force a pattern match after every
sum, so `+`, `-`, and `*` keep `int`. The functions in section 12.5 have no
such cost, so they return exact types.

Precision loss above `2 ** 53` does not break soundness. The rounded result is
still an integer, so it is still an `int`.

### 5.3. Interval arithmetic

On ranges with finite bounds the result is computed from the bounds. Each rule
converts to inclusive bounds, applies the operator, and converts back:

- `a..b + c..d` is `(a+c)..(b+d-1)`.
- `a..b - c..d` is `(a-d+1)..(b-c)`.
- `a..b * c..d` is `min(P)..(max(P)+1)`, where `P` holds the four products
  that pair `a` or `b-1` with `c` or `d-1`.

A literal operand is the one-member range `k..k+1`, so `i + 1` with `i: 0..10`
gives `1..11`. An unbounded end stays unbounded. `0..Infinity + 1` gives `1..Infinity`.

Interval arithmetic is a later milestone. Until it lands, any arithmetic on
range operands yields `int`.

### 5.4. Operand inference

`inferBinary` constrains each operand against the operator's parameter types and
returns the fixed return type. Two approaches fit the new rows:

1. Give the operators overloaded signatures, typed as an intersection of
   function types such as `((int, int) -> int) & ((number, number) -> number)`.
2. Compute the result from the operand types when those types are concrete at
   inference time, and fall back to the `number` signature otherwise.

The recommendation is the second. Overloaded prelude operators are an unscheduled
milestone, and an intersection of arrows makes the principal type of an
unannotated `fn (a, b) { a + b }` harder to read. Under the second approach that
function keeps its current type, `fn (a: number, b: number) -> number`. An
annotated `fn (a: int, b: int) { a + b }` returns `int`.

### 5.5. Comparisons do not refine types

Escalier does not re-type a variable based on control flow. A refinement always
binds a new name through `match`, `if val`, or `val … else`. A comparison is a
`boolean` expression, so `if i < 10` leaves `i` at `int` in both branches.

A range pattern does the job a comparison would do in TypeScript:

```esc
fn clamp(i: int) -> 0..10 {
    return match i {
        j: -Infinity..0 => 0,
        j: 0..10        => j,     // j: 0..10
        _               => 9,
    }
}

if val small: -Infinity..10 = i {
    // small: -Infinity..10, and i is still int
}
```

The pattern `j: 0..10` compiles to `i >= 0 && i < 10` on an `int` scrutinee,
the same test the comparison would make. See section 10.2. Unlike a guard such
as `j if j < 10`, a range pattern also counts toward exhaustiveness, since a
guarded arm covers nothing. Section 14 asks whether a guard that compares a
binding with a literal should be read as a range pattern.

## 6. Range values

### 6.1. What `a..b` evaluates to

As an expression, `a..b` builds a range object. The object is not an `int`. Its
type is `IntRange<A..B, A, B>`, where `A` and `B` are the operand types. The
first argument is the element type. The other two are the types of `start` and
`end`:

```esc
val r = 0..10            // r: IntRange<0..10>
for i in r { ... }       // i: 0..10
r.start                  // 0
r.end                    // 10

fn f(n: int) {
    val s = 0..n         // s: IntRange<0..Infinity, 0, int>
}
```

The last two parameters default to `Lower<T>` and `Upper<T>` from section 2.5.
The printer omits trailing arguments that equal their defaults, as section 8.4
describes, so a range with literal bounds prints with one argument. `s` keeps
all three, because its `end` has type `int` while `Upper<0..Infinity>` is
`Infinity`.

The separate parameters matter because the element type is a set and loses
facts about how the range was written:

| Expression | Type |
|---|---|
| `0..10` | `IntRange<0..10>` |
| `0..9 by 2` | `IntRange<0..10 by 2, 0, 9>` |
| `10..0 by -1` | `IntRange<1..11, 10, 0>` |
| `5..5` | `IntRange<never, 5, 5>` |

`0..9 by 2` and `0..10 by 2` have the same members and different `end`
values. `10..0 by -1` has the same members as `1..11` and starts at the other
end. `5..5` has no members and still has a `start` of `5`.

Code that only reads elements names the element type alone. A parameter typed
`IntRange<0..10>` accepts only ranges whose `start` is `0` and whose `end` is
`10`, so a function that only iterates should take `Iterable<0..10>` instead.

The name avoids `Range`, which the DOM library declares as a global interface.

The prelude declares `IntRange` as a class backed by the runtime package
`@escalier/runtime`:

```esc
declare class IntRange<
    T: int,
    S: int = Lower<T>,
    E: int | Infinity | -Infinity = Upper<T>,
> {
    readonly start: S,
    readonly end: E,
    readonly length: number,
    has(&self, x: number) -> boolean,
    [Symbol.iterator](&self) -> Iterator<T, undefined, undefined>,
}
```

`E` admits both infinities because a range with a negative step may end at
`-Infinity`. `length` is `number` because it is `Infinity` for an unbounded
range. Open question 15 covers giving it an exact type such as `Sub<E, S>`.

### 6.2. Iteration

Every range value is iterable. `for ... in` already reads `[Symbol.iterator]`
through the iteration protocol, so it accepts an `IntRange` without a new arm in
`syncElemType`. The same member makes a range work anywhere an `Iterable` is
expected, in Escalier and in TypeScript:

```esc
val xs = [...0..5]                 // xs: Array<0..5>
val ys = Array.from(1..4)          // ys: Array<1..4>
```

The iterator yields `start`, `start + 1`, and so on while the value is below
`end`. A range whose bounds are equal or reversed yields nothing and is not an
error.

### 6.3. Bounds

The lower operand must be a subtype of `int`. The upper operand must be a
subtype of `int | Infinity`, using the `Infinity` type from section 3.5. `0..x` with `x: number` reports
`range bound must be an int or Infinity, found number`.

`m..Infinity` iterates without end, so a loop over it needs a `break` or
`return`. Spreading a range whose type has an `Infinity` upper bound never
finishes, so `[...0..Infinity]` and `Array.from(0..Infinity)` report an error
when the checker sees the unbounded type.

`-Infinity` is valid as a lower bound in types but not in values. An iterator
cannot start from `-Infinity`, since adding `1` to it gives `-Infinity` again.
`-Infinity..0` in expression position is an error.

### 6.4. Precedence

`..` binds looser than every arithmetic operator and tighter than the
comparisons. `0..n+1` parses as `0..(n+1)`. `..` does not associate, so
`a..b..c` is a parse error.

### 6.5. Range patterns

A range in pattern position tests membership:

```esc
match code {
    200..300 => "ok",
    400..500 => "client error",
    _        => "other",
}
```

The pattern binds its name at the meet of the scrutinee's type with the range, and
section 4.3 gives the coverage rule.

## 7. Stepped ranges

### 7.1. Syntax and meaning

`m..n by s` keeps every `s`-th integer of a range, starting from `m`:

```esc
type Even = 0..10 by 2      // 0, 2, 4, 6, 8

for i in 0..10 by 3 { ... } // i: 0..10 by 3, yields 0, 3, 6, 9
for i in 10..0 by -1 { ... } // yields 10, 9, ..., 1
```

The bounds stay half-open. A positive step yields `m`, `m+s`, `m+2s`, and so on
while the value is below `n`. A negative step yields values while they are above
`n`, so `10..0 by -1` stops before `0`. A step with the wrong sign for its
bounds, such as `0..10 by -1`, yields nothing.

The same form works in a type annotation, an expression, and a pattern. Without
`by` the step is `1`, so every rule in sections 2 to 6 is the `by 1` case of the
rules here.

### 7.2. Stepped types are sets

As a type, a stepped range is a set of integers. Order plays no part, so a
negative step names the same set as some positive one. `10..0 by -2` is
`{10, 8, 6, 4, 2}`, which is `2..12 by 2`. The checker stores and prints every
stepped type with a positive step. The negative-step form matters only for the
order a range value yields its members in.

The checker also tightens the upper bound, so equal sets print the same way.
The printed upper bound is the last member plus the step:

| Annotation | Printed form |
|---|---|
| `0..10 by 2` | `0..10 by 2` |
| `0..9 by 2` | `0..10 by 2` |
| `0..10 by 3` | `0..12 by 3` |
| `10..0 by -2` | `2..12 by 2` |
| `0..10 by 1` | `0..10` |
| `0..1 by 5` | `0` |

In a type, the step must be a single nonzero integer literal or an alias for
one. `0..10 by 0`, `0..10 by int`, and `0..10 by (2 | 3)` are errors. The
union-over-bounds rule from section 2.4 still applies to the bounds.

### 7.3. Unbounded stepped ranges

`0..Infinity by 2` is every even integer from `0` up. Its members are anchored
on the lower bound.

When the lower bound is `-Infinity`, the members are anchored on the upper
bound instead. `-Infinity..10 by 2` is `{8, 6, 4, ...}`, the values `10 - 2k`
for every `k >= 1`.

When both bounds are infinite, the members are anchored on `0`.
`-Infinity..Infinity by 2` is every even integer. The odd integers have no
direct spelling. Negation supplies one, as `int & ~(-Infinity..Infinity by 2)`,
and the checker prints them that way.

### 7.4. The lattice

The rules below extend sections 3 and 4 to stepped ranges.

Subtyping:

- `A <: B` when every member of `A` is a member of `B`. Each member of `A` must
  fall within `B`'s bounds and land on one of `B`'s steps.
- When `A` has two or more members, a quick check decides the second part.
  `A`'s step must be a multiple of `B`'s step, and `A`'s first member must
  land on one of `B`'s steps. Then every later member of `A` lands on one too.
- Every stepped range is a subtype of the unstepped range with the same bounds,
  and so of `int`.

| `A` | `B` | `A <: B` | Why |
|---|---|---|---|
| `0..10 by 4` | `0..10 by 2` | yes | `0`, `4`, `8` are all steps of `B` |
| `2..10 by 4` | `0..10 by 2` | yes | `2`, `6` are all steps of `B` |
| `1..10 by 4` | `0..10 by 2` | no | `1` is not a step of `B` |
| `0..10 by 3` | `0..10 by 2` | no | `3` is not a step of `B` |
| `0..10 by 2` | `0..10 by 4` | no | `2` is not a step of `B` |
| `6` | `0..10 by 3` | yes | `6` is a step of `B` |

The meet and negation rules need one more term. A residue class is the set of
integers that leave the same remainder `r` when divided by a step `s`. It is
every integer a range's steps would hit if the range extended forever in both
directions. `0..10 by 2` is the residue class `r = 0, s = 2`, which is the
even integers, cut down to `0..10`.

Meets:

- The intervals intersect as in section 3.1.
- Two residue classes meet in one class whose step is the least common multiple
  of the two steps. If no integer satisfies both remainders, the meet is
  `never`. `0..Infinity by 2 & 0..Infinity by 3` is `0..Infinity by 6`.
  `0..Infinity by 2 & 1..Infinity by 2` is `never`.

Joins:

- Two stepped ranges with the same step and residue merge when their intervals
  touch or overlap, as unstepped ranges do.
- Stepped ranges with the same step merge into an unstepped range when together
  they cover every integer of an interval. `0..10 by 2 | 1..10 by 2` is
  `0..10`, and `0..Infinity by 2 | 1..Infinity by 2` is `0..Infinity`.
- Any other union keeps its atoms.

Negation:

- `int & ~(0..10 by 2)` is
  `-Infinity..0 | 1..11 by 2 | 10..Infinity`. The complement of a stepped range
  inside its own interval is the other `s - 1` residue classes.
- That count grows with the step. Above a step of 16, the checker keeps
  `int & ~(m..n by s)` unreduced instead of expanding it. That is the same kind
  of residual as `number & ~int` in section 4.2.

Coverage in `match` follows from these rules:

```esc
fn parity(n: 0..Infinity) -> string {
    return match n {
        0..Infinity by 2 => "even",
        1..Infinity by 2 => "odd",
    }
}
```

The two arms join into `0..Infinity`, so the match is exhaustive.

### 7.5. Arithmetic

Interval arithmetic in section 5.3 extends to steps:

- Adding a literal shifts the residue and keeps the step. `x + 1` with
  `x: 0..10 by 2` is `1..11 by 2`.
- Multiplying by a literal `k` multiplies the bounds and the step. `x * 3` with
  `x: 0..5` is `0..15 by 3`.
- Adding two stepped ranges gives the greatest common divisor of their steps.

These rules land with the rest of interval arithmetic, in the later-work
milestone.

### 7.6. Values

`a..b by s` builds an `IntRange` with a `step` field:

```esc
declare class IntRange<T: int, S: int = Lower<T>, E: ... = Upper<T>> {
    ...
    readonly step: int,
}
```

The step operand of a value range may be any expression, with two conditions:

- Its type must be a subtype of `int & ~0`. A zero step would never advance,
  so the checker rejects one statically. A parameter used as a step needs a
  type such as `1..Infinity` or `int & ~0`.
- When the step type is a single literal, the element type of the range is the
  stepped type. Otherwise it is the unstepped range over the same bounds, which
  contains every member the range can yield.

```esc
val r = 0..10 by 2           // r: IntRange<0..10 by 2>

fn every(n: int, s: 1..Infinity) {
    val r = 0..n by s        // r: IntRange<0..Infinity, 0, int>
}
```

The rules for unbounded value ranges flip with the sign of the step. With a
positive step, the end may be `Infinity`. With a negative step, the end may be
`-Infinity`. The start must always be a finite `int`.

The `IntRange` constructor throws a `RangeError` on a zero step. Only
TypeScript callers can reach that check, since Escalier code cannot pass one.

### 7.7. Parsing

`by` is a contextual keyword. It is recognized only directly after the upper
bound of a range, so `by` stays usable as an identifier everywhere else. The
step operand parses at the same precedence as a bound, so `0..n by k + 1` is
`0..n by (k + 1)`.

`by` must sit on the same line as the upper bound. The parser ends a binary
expression at a line break outside a multi-line context, and `by` follows that
rule:

```esc
val r = 0..10
by(3)        // a call to a function named `by`, not a step
```

`RangeExpr` and `RangeTypeAnn` each gain an optional `Step` field. `a by b`
without a range is a parse error.

### 7.8. Codegen

A counting loop over a stepped range picks its comparison from the sign of the
step:

```js
// for i in a..b by 2
for (let i = a, $end = b; i < $end; i += 2) { body }

// for i in a..b by -2
for (let i = a, $end = b; i > $end; i -= 2) { body }
```

When the step type contains both signs, as with `int & ~0`, the loop tests the
sign at runtime:

```js
for (let i = a, $end = b, $s = s; $s > 0 ? i < $end : i > $end; i += $s) { body }
```

Runtime guards for stepped types add a remainder check to the guard in
section 10.2:

| Test | Emitted JavaScript |
|---|---|
| `x: 0..10 by 2` | `Number.isInteger(x) && x >= 0 && x < 10 && (x - 0) % 2 === 0` |
| `x: -Infinity..10 by 2` | `Number.isInteger(x) && x < 10 && (10 - x) % 2 === 0` |
| `x: -Infinity..Infinity by 2` | `Number.isInteger(x) && x % 2 === 0` |

A remainder in JavaScript takes the sign of the dividend, so `-4 % 2` is `-0`.
`-0 === 0` holds, so the check is correct for negative values.

### 7.9. TypeScript interop

A stepped range follows the table in section 11.1 by its member count. A finite
range with at most 16 members emits as a literal union, so `0..10 by 2` emits as
`0 | 2 | 4 | 6 | 8`. Larger and unbounded stepped ranges emit as `number`.

### 7.10. Representation

The `IntRangeType` atom from section 9.1 gains a step and a residue:

```go
type IntRangeType struct {
    Lo, Hi float64
    Step   float64 // always positive, 1 when unstepped
    Rem    float64 // in [0, Step), 0 when unstepped
}
```

The residue is stored rather than derived from `Lo`, because `Lo` is
`-Infinity` for a range anchored on its upper bound. Section 9.1 gives the
precision rules for all four fields.

## 8. Parsing

### 8.1. Lexer

The lexer has no `..` token, and it reads `1..2` as the number `1.` followed by
the number `.2`. Two changes fix that:

1. Add a `DotDot` token. On a `.`, emit `DotDotDot` for three dots, `DotDot` for
   two, and `Dot` for one.
2. In the number scan, stop before a `.` that is followed by another `.`. Then
   `1..2` lexes as `1`, `..`, `2`, and `1...` still lexes as `1` and `...`.

JavaScript reads `1..toString()` as a method call on `1.`. Escalier reads it as
the range `1..toString()`, which fails to type-check because a function is not
an `int`. Escalier does not promise JavaScript's lexical grammar, so this is
acceptable.

### 8.2. Expressions

Add a binary operator `..` to `ast.Precedence` between the additive and
comparison levels. The parser builds a new `RangeExpr` node with `Start` and
`End`, not a `BinaryExpr`. A range is not a call to a prelude operator binding,
and its codegen differs, so a distinct node keeps both paths simple. The node
goes through the AST generator in `tools/gen_ast`.

### 8.3. Type annotations

`int` resolves as a type name bound in the prelude scope rather than as a
keyword. That keeps `int` usable as a value identifier, as in `val int = 5`.
The resolver maps it to the new `int` type the way it maps `number`.

A range annotation is `RangeTypeAnn{Start, End}`. Each bound is one of:

- an integer literal, including a negative one such as `-5`
- `Infinity` as an upper bound, or `-Infinity` as a lower bound
- a type reference such as `Lo`, `N`, or `int`

Any other type is not a valid bound, so `0..(0 | 5)`, `0..(0..100)`, and
`0..typeof n` are parse errors. A bound that needs one of these forms names it
with an alias first, as in `type Hi = 0 | 5` followed by `0..Hi`. Keeping bounds
to names and literals keeps every range readable at a glance.

`..` binds tighter than `&` and `|`, so `0..10 | 20..30` is a union of two
ranges. `-5..5` parses as `(-5)..5`.

A value identifier is not a valid bound. `0..MAX` with `val MAX = 10` reports
that `MAX` is a value, and points the user at `type Max = typeof MAX`.

### 8.4. Printing

The type printer prints `int` and `m..n`. It prints a one-member interval as its
literal, so `5..6` prints as `5`. The source printer used by `printer.Print`
gains the same two forms for annotations and the `RangeExpr` form for
expressions.

The type printer also omits trailing type arguments that equal their
parameters' defaults. The rule applies to every generic type, not only to
`IntRange`. It drops arguments from the end of the list and stops at the first
argument that differs from its default, so the remaining arguments keep their
positions. `IntRange<0..10, 0, 10>` prints as `IntRange<0..10>`.
`IntRange<0..Infinity, 0, int>` keeps all three, because its last argument
differs from its default.

## 9. Solver

### 9.1. Representation

Add one atom to `soltype`:

```go
// IntRangeType is the set of integers k with Lo <= k < Hi. Lo may be -Inf and
// Hi may be +Inf, so int is IntRangeType{Lo: -Inf, Hi: +Inf}.
type IntRangeType struct {
    Lo, Hi float64
}
```

`float64` holds every integer a JavaScript `number` can hold, and the two
infinities stand for unbounded ends. `int` is the fully unbounded range, so the
lattice needs no separate `int` atom. `0..Infinity` is `{0, +Inf}`. Integral
literals stay `LitType` and convert to one-member intervals inside the lattice
operations.

#### Precision

Storing a bound as a `float64` is exact. Computing a new bound is not always
exact, because `float64` addition and multiplication round once a result passes
`2 ** 53` in magnitude. The checker uses `float64` throughout and handles that
limit with these rules:

1. A bound written in an annotation is stored as the lexer parsed it and is
   never adjusted. That matches the value the same literal has at runtime.
2. Meets and joins of intervals take the `min` or `max` of existing bounds,
   so they are exact. The residue of a stepped range comes from `math.Mod`,
   which is also exact.
3. A bound computed by arithmetic rounds outward when its magnitude passes
   `2 ** 53`. A lower bound moves one representable value down with
   `math.Nextafter(lo, math.Inf(-1))`, and an upper bound moves one value up.
   One `float64` operation rounds by at most half of that gap, so the widened
   range still contains every true member. This covers the interval arithmetic
   in sections 5.3 and 7.5 and the `last + step` normalization in section 7.2.
4. A step computed by arithmetic is dropped when it passes `2 ** 53`, which
   leaves the unstepped range over the same bounds. Multiplying a stepped range
   by a large literal is the case that reaches this rule. The greatest common
   divisor used when adding two stepped ranges never exceeds its inputs, so it
   stays exact.
5. A meet of two stepped ranges whose least common multiple passes `2 ** 53`
   stays unreduced, as `A & B`. It keeps both atoms instead of rounding, the
   way other residuals in section 4.2 do.

Rules 3 and 4 only widen the type an operation produces. A wider result type is
sound, and it loses precision only for ranges with bounds or steps beyond
`2 ** 53`. Rule 5 is exact. None of the rules ever narrows a type, so the
checker never accepts a value that the exact rules would reject.

A range with a type-variable bound needs a second form, covered in section 9.5.

### 9.2. Places that change

These sites switch on primitives or literals and each needs a case for
`IntRangeType`:

- `soltype/type.go`. `NaN`, `Infinity`, and `-Infinity` can be `NumLit`
  values, since a `float64` holds all three. `LitType.Equal` compares values
  with `==`, which is false for two `NaN`s, so it needs a `math.IsNaN` case.

- `solver/constrain.go`, around `:1335`. Add `IntRangeType <: IntRangeType`,
  `IntRangeType <: number`, and integral `LitType <: IntRangeType`.
- `solver/normal.go`. `valueFamilyOf` returns `numberFamily`. `meetValueAtoms`
  intersects intervals. `joinValueAtoms` merges touching intervals. The
  difference rewrite from section 4.1 goes beside them.
- `solver/lattice.go`. The ordering and equality helpers at `:559`, `:899`, and
  `:992` compare intervals.
- `solver/widen.go`. An `IntRangeType` widens to `number`, matching a literal.
- `solver/infer_expr.go`, `infiniteInhabitants`. An interval with an unbounded
  end is infinite. A finite interval is coverable arm by arm.
- `solver/ucs_coverage.go`. Coverage over the number family becomes an
  interval union. See section 4.3.
- `solver/typeops.go`, `isNumericKey` and `indexTuple`. See section 9.4.
- `soltype/print.go` and `soltype/visitor.go`. Printing, and a leaf `Accept`.
- `solver/type_ann.go`. `resolveTypeAnnType` resolves `RangeTypeAnn` and the
  `int` name. It reports a bound that is not a subtype of `int`, and an empty
  literal range.

### 9.3. Type-reference bounds

A bound written as a type reference resolves through `resolveScopedTypeRef`
like any other reference. After resolution, the bound's type is checked against
`int`, then reduced by the union-over-bounds rule from section 2.4 to the `min`
or `max` integer it admits. `type Lo = 0` gives `0`, and `type Lo = int` gives
`-Infinity`. An `Infinity` or `-Infinity` bound is the type from section 3.5.

### 9.4. Indexing

`isNumericKey` accepts `int` and every range. On a tuple of length `n`:

- A range key inside `0..n` reads the union of the elements it covers. With
  `t: [string, number, boolean]` and `i: 0..2`, `t[i]` is `string | number`.
- A range key that reaches past `n` reports `TupleIndexOutOfRangeError`, as a
  literal key out of range does today.
- An `int` key reads the union of every element, as a `number` key does today.

Array reads already return the element type for any numeric key, so they need
only the `isNumericKey` change.

### 9.5. Generic bounds

A bound may be a type parameter:

```esc
fn clampIndex<N: int>(i: int, len: N) -> 0..N { ... }
```

`0..N` cannot reduce to an interval until `N` is known, so it needs a symbolic
form, `IntRangeType` with `Lo` or `Hi` held as a type. The rules for it:

- `0..N <: int` holds.
- `0..N <: 0..N` holds by identity.
- Against a concrete range, a symbolic bound is replaced by its declared upper
  bound and reduced by the union-over-bounds rule. With `N: 0..100`, `0..N` is
  checked as `0..99`.
- Instantiation substitutes the bound and reduces. `clampIndex(i, 5)` returns
  `0..5`.

Generic bounds are a later milestone. Before they land, a type-parameter bound
reduces through its declared upper bound right away, so `0..N` with `N: int`
becomes `0..Infinity`.

## 10. Codegen

### 10.1. Types are erased

`int` and range annotations emit no runtime code. `val x: 0..10 = f()` is
checked statically and compiles to `const x = f()`. A program turns a `number`
into an `int` through a pattern that binds a new name at the refined type:

```esc
if val k: int = n { ... }           // k: int, and n is still number
val k: int = n else { return 0 }    // k: int for the rest of the block
```

Both compile to a `Number.isInteger(n)` test, as section 10.2 shows.

### 10.2. Runtime guards

A pattern or type test on the new types emits an inline check.
`buildTypeGuard` and `buildSolTypeGuard` in `codegen/builder.go` gain these
forms:

| Test | Emitted JavaScript |
|---|---|
| `x: int` | `Number.isInteger(x)` |
| `x: 0..10` | `Number.isInteger(x) && x >= 0 && x < 10` |
| `x: 0..Infinity` | `Number.isInteger(x) && x >= 0` |
| `0..10` pattern on an `int` scrutinee | `x >= 0 && x < 10` |

The last row drops `Number.isInteger` because the scrutinee's static type
already proves it.

### 10.3. Range expressions

A range is an `IntRange` object by default. Codegen replaces it with a counting
loop when the loop is the only thing that can observe the object. That holds in
two cases:

- the iterated expression of a `for ... in` is a range expression
- the iterated expression is a `val` bound to a range expression, and the loop
  is the binding's only use

The counting loop evaluates each bound once, in source order, and matches what
the iterator would yield:

```js
// for i in a..b { body }
for (let i = a, $end = b; i < $end; i++) { body }

// for i in a..Infinity { body }
for (let i = a; ; i++) { body }
```

`break`, `continue`, and labels work the same in either form, since both are
JavaScript loops.

Every other range expression builds an `IntRange` from `@escalier/runtime`:

```js
// val r = 0..n
import { IntRange } from "@escalier/runtime";
const r = new IntRange(0, n);
```

The counting-loop form matters because ranges will appear mostly in loop heads.
It avoids an allocation and an iterator call per step.

### 10.4. Large integer literals

`codegen/printer.go` prints number literals with
`strconv.FormatFloat(v, 'f', -1, 32)`. The bit size of 32 rounds the value to
`float32` precision, so a large integer such as `9007199254740991` is emitted
with the wrong value. This bug exists today. It matters more once code works
with large `int` bounds, so the fix goes in before the codegen milestone.

## 11. TypeScript interop

### 11.1. Emitting `.d.ts`

TypeScript has no integer type. The emitter maps the new types as follows:

| Escalier | TypeScript |
|---|---|
| `int` | `number` |
| `0..Infinity`, `-Infinity..0`, and other unbounded ranges | `number` |
| a finite range with at most 16 members | union of its literals, such as `0 \| 1 \| 2` |
| a larger finite range | `number` |
| `NaN`, `Infinity`, `-Infinity`, `finite` | `number` |
| `IntRange<T, S, E>` | `IntRange<T', S', E'>`, where each argument is mapped by this table |

A literal union gives TypeScript callers a real check for small ranges such as a
day of the week or a die face. Above the threshold a union bloats the
declaration and slows TypeScript's checker, so it falls back to `number`.

TypeScript callers can pass any `number` where Escalier expects an `int`, since
nothing checks at the boundary. Exported functions emit no runtime validation
under this design. Section 14 asks whether an opt-in boundary check is worth
adding.

The planned `@esctype` tag carries the exact Escalier type next to the
TypeScript one. It is described in
[interop_mutability/requirements.md](../interop_mutability/requirements.md).
An Escalier consumer reading the `.d.ts` recovers `int` and ranges from it. The
`@esctype` path has no implementation yet, so until it lands an Escalier
consumer sees the TypeScript type.

The `IntRange` class ships with a `.d.ts` in `@escalier/runtime`, so emitted
declarations that mention it resolve for TypeScript consumers.

### 11.2. Importing `.d.ts`

TypeScript's `number` stays `number`, and a numeric literal stays a literal. A
TypeScript literal union such as `0 | 1 | 2` stays a union of literals, per
section 3.2. It still compares as the interval `0..3`, so it accepts a value
typed `0..3`.

`int <: number`, so every TypeScript API that takes a `number` accepts an `int`
or a range with no change.

## 12. Arrays, tuples, and the standard library

The stdlib is where most programs first meet `int` and ranges. Tightening its
signatures turns a range into the natural type of every length, index, and
search result. Each change below goes through the stdlib override mechanism,
not through edits to the generated `.esc` files.

### 12.1. Length and index aliases

JavaScript caps an array's length at `2 ** 32 - 1`, so lengths and indices have
exact ranges. The prelude names them:

```esc
type ArrayLength = 0..4294967296    // 0 through 2 ** 32 - 1
type ArrayIndex  = 0..4294967295    // 0 through 2 ** 32 - 2
```

Two intrinsics compute the same facts for a particular array or tuple type:

- `Indices<T>` is the set of valid indices of `T`.
- `At<T, I>` is the type of `T` read at index `I`, as `Array.prototype.at`
  defines it. A negative `I` counts from the end, and an index past either end
  reads `undefined`.

| `T` | `Indices<T>` | `T["length"]` |
|---|---|---|
| `[A, B, C]` | `0..3` | `3` |
| `[A, B?]` | `0..2` | `1..3` |
| `[A, ...Array<B>]` | `ArrayIndex` | `1..4294967296` |
| `[A, B, ...]` | `ArrayIndex` | `2..4294967296` |
| `Array<T>` | `ArrayIndex` | `ArrayLength` |

A stdlib signature refers to the receiver as `Self`, so `Indices<Self>` in an
`Array` method resolves to `0..3` when the receiver is `[A, B, C]`.

### 12.2. Arrays

| Member | Type today | Type with this design |
|---|---|---|
| `length` | `number` | `ArrayLength` |
| `new Array(n)`, `Array(n)` | `n: number` | `n: ArrayLength` |
| `push`, `unshift` | return `number` | return `ArrayLength` |
| `indexOf`, `lastIndexOf`, `findIndex`, `findLastIndex` | return `number` | return `-1 \| ArrayIndex` |
| callback `index` in `map`, `forEach`, `filter`, `reduce`, and the rest | `number` | `Indices<Self>` |
| `at(index)` | `index: number`, returns `T \| undefined` | `index: int`, returns `At<Self, I>` |
| `with(index, value)` | `index: number` | `index: int` |
| `slice`, `fill`, `copyWithin`, `includes`, `indexOf` position arguments | `number` | `int` |
| `splice(start, deleteCount)` | `number`, `number` | `int`, `ArrayLength` |

Two of these catch errors that throw at runtime:

- Assigning `arr.length = 1.5` or calling `new Array(-1)` throws a
  `RangeError`. A write to `length` and the constructor's argument now require
  an `ArrayLength`, so the checker reports both.
- `with` throws a `RangeError` for an index outside the array. On a tuple,
  `with` takes `Indices<Self>`, so the checker catches the out-of-range case
  there too.

The position arguments of `at`, `slice`, and the others are truncated by
JavaScript, so `arr.at(1.5)` reads `arr[1]`. Typing them as `int` turns a
silent truncation into a type error. A program with a `number` in hand binds
it at `int` with `if val k: int = n`, or converts it with `Math.trunc` and
binds the result at `int`, as section 12.5 shows.

Element reads keep their current rule for `int` and `number` keys. They return
the element type, and an index past the end still reads as the element type.
Two key types are always wrong and now report an error:

- A key whose type is disjoint from `int`, such as `arr[1.5]` or a key of type
  `number & ~int`, always reads `undefined`.
- A negative key, such as `arr[-1]`, reads a property named `"-1"`, not the
  last element. The error message points at `arr.at(-1)`.

### 12.3. Tuples

A tuple gets every array change above, with `Self` resolved to the tuple type.
The ranges then carry the tuple's length:

```esc
val t: [string, number, boolean] = ["a", 1, true]

t.length                          // 3
t.map(fn (x, i) => i)             // i: 0..3
t.at(-1)                          // boolean
t.at(i)                           // with i: 0..2, string | number

for i in 0..t.length {
    print(t[i])                   // t[i]: string | number | boolean
}
```

The loop needs no out-of-range check. `t.length` is the literal `3`, so `i` has
type `0..3`, and section 9.4 reads `t[i]` as the union of the elements at
`0`, `1`, and `2`.

`t.with(3, x)` is an error, since `3` is outside `Indices<Self>`. `t.at(5)` is
`undefined`, matching what JavaScript returns.

A tuple with optional or rest elements reads from the table in section 12.1. A
read at an index that may hold an optional element includes `undefined`.

### 12.4. Typed arrays and `DataView`

Every typed array stores integers of a fixed width, so its element type is an
exact range:

| Class | Element type |
|---|---|
| `Int8Array` | `-128..128` |
| `Uint8Array`, `Uint8ClampedArray` | `0..256` |
| `Int16Array` | `-32768..32768` |
| `Uint16Array` | `0..65536` |
| `Int32Array` | `-2147483648..2147483648` |
| `Uint32Array` | `0..4294967296` |

The range is the read type. A write takes any `number`, since JavaScript wraps
or clamps the value into the range rather than throwing. Writing `300` into a
`Uint8Array` stores `44`. The element signature therefore needs separate read
and write types. Section 14 asks how the stdlib should spell that.

`Float32Array`, `Float64Array`, and the `BigInt` arrays keep `number` and
`bigint` elements. The `DataView` getters follow the same table. For example,
`getUint8` returns `0..256` and `getInt16` returns `-32768..32768`.

### 12.5. Strings, `Math`, and `Number`

| Member | Type |
|---|---|
| `String.prototype.length` | `0..9007199254740992` |
| `charAt`, `at`, `slice`, `substring`, `padStart` position and length arguments | `int` |
| `codePointAt` | returns `0..1114112 \| undefined` |
| `charCodeAt` | returns `0..65536 \| NaN` |
| `Math.clz32` | returns `0..33` |
| `Math.sign` | returns `-1 \| 0 \| 1 \| NaN` |
| `parseInt` | returns `int \| NaN \| Infinity \| -Infinity` |
| `Date.prototype.getFullYear` and the other date getters | return `int \| NaN` |
| `Number.MAX_SAFE_INTEGER`, `Number.MIN_SAFE_INTEGER` | their literal values |

The string length bound is `2 ** 53 - 1`, the limit the specification sets.
`parseInt` includes the infinities because a long enough run of digits, such as
400 nines, overflows.

`Math.floor`, `Math.ceil`, `Math.round`, and `Math.trunc` pass `NaN` and the
infinities through unchanged, and return an integer for every other input. Each
takes this overload set, with `floor` shown:

```esc
floor<T: int>(x: T) -> T
floor(x: finite) -> int
floor(x: NaN) -> NaN
floor(x: Infinity) -> Infinity
floor(x: -Infinity) -> -Infinity
floor(x: number) -> int | NaN | Infinity | -Infinity
```

A call takes the first overload whose parameter accepts the argument:

| Argument type | Result |
|---|---|
| `0..10` | `0..10` |
| `finite` | `int` |
| `number` | `int \| NaN \| Infinity \| -Infinity` |

The first overload keeps a range intact. The last one gives a plain `number`
an exact result instead of `number`. A program binds that result at `int`
with a pattern:

```esc
val i = Math.trunc(x)            // i: int | NaN | Infinity | -Infinity
if val k: int = i {
    arr.at(k)                    // k: int
}
```

`Number.isInteger`, `Number.isFinite`, and `Number.isNaN` keep their
`boolean` results and refine nothing. The patterns `k: int`, `k: finite`, and
`k: NaN` do the refining, and they compile to the same calls.

## 13. Milestones

1. **`int` type.** Covers the lexer-independent parts: `int` as a prelude type
   name, `IntRangeType` with unbounded ends, constrain and lattice rules,
   printing, and `.d.ts` emission as `number`. The `NaN`, `Infinity`, and
   `-Infinity` types, the `finite` alias, and the `x: int`, `x: finite`, and
   `x: NaN` type tests land here too.
2. **Range annotations.** Covers the `DotDot` token, the number-scan fix,
   `RangeTypeAnn` with literal and type-reference bounds, interval meets, joins,
   and the right-hand union normalization from section 3.3. The literal-union
   `.d.ts` emission lands here too.
3. **Negation.** Covers the difference rewrite from section 4.1, residuals that
   leave `int`, and tests over `~int` and `~(m..n)`.
4. **Integer arithmetic.** Covers the result types in section 5.1, computed from
   concrete operand types.
5. **Range values.** Covers `RangeExpr`, the `IntRange` prelude class and its
   runtime implementation, the `Lower` and `Upper` intrinsics, the printer rule
   that omits default type arguments, `for ... in` lowering, and range
   patterns.
6. **Coverage.** Covers interval-based exhaustiveness and narrowing in `match`,
   and tuple indexing by range.
7. **Stepped ranges.** Covers `by` in types, values, and patterns, the
   residue-class lattice rules, and stepped loop lowering from section 7.
8. **Standard library.** Covers section 12: the length and index aliases, the
   `Indices` and `At` intrinsics, the array, tuple, typed array, and string
   signatures, and the `Math` rounding overloads.
9. **Later work.** Covers interval arithmetic with steps and generic bounds.

The `float32` printing fix from section 10.4 lands before milestone 5.

## 14. Open questions

1. **Name.** The working name is `int`. `integer` reads closer to
   `Number.isInteger` and avoids suggesting a fixed-width type. `int` is shorter.
2. **Open-ended shorthand.** Rust-style `0..` and `..0` would be shorter than
   `0..Infinity` and `-Infinity..0`. They collide with spread in some positions
   and need their own parse rules.
3. **Widening `var`.** Section 3.4 keeps `var i = 0` as `number`. Widening an
   integral literal to `int` would help counting loops and would reject
   accumulators that later receive fractions.
4. **`Exclude` over ranges.** Section 4.4 keeps the distributive definition,
   which leaves ranges whole. Redefining `Exclude<T, U>` as `T & ~U` would
   change its result for object types too.
5. **Complement cutoff.** Section 7.4 stops expanding the complement of a
   stepped range above a step of 16. The cutoff trades precision for the size
   of the normal form.
6. **Inclusive ranges.** `m..=n` would name `m` through `n` inclusive. It is not
   needed for this design and can be added later without conflict.
7. **Boundary checks.** Exported functions could validate `int` and range
   parameters when called from TypeScript, behind a compiler flag.
8. **Literal-union threshold.** Section 11.1 uses 16 members as the cutoff for
   emitting a range as a TypeScript literal union.
9. **`bigint` ranges.** `1n..10n` would need a parallel interval atom in the
   `bigint` family. The lexer does not read `bigint` literals yet.
10. **Template literal types.** `` `${int}` `` could match strings of decimal
   digits with an optional sign. The checker has the regex machinery for it.
11. **Comparing with `NaN`.** `x == NaN` is always `false`. The checker could
   report it and point at `Number.isNaN`.
12. **Split element types.** Section 12.4 reads typed array elements as a range
   and writes them as `number`. This design does not settle how the stdlib
   spells an index signature whose read and write types differ.
13. **Guards as range patterns.** Section 5.5 refines through range patterns,
   and a guard covers nothing for exhaustiveness. The checker could read a
   guard that compares the arm's own binding with a literal, such as
   `j if j < 10`, as the pattern `j: -Infinity..10`. The refinement would
   still land on the new binding `j`, so it keeps the no-flow-typing rule. The
   cost is a second way to write the same arm.
14. **Flow typing.** Section 5.5 keeps Escalier's rule that a refinement binds
   a new name. Flow typing would instead re-type an existing variable after a
   test. Several `int` and range idioms read more naturally that way:
   - A bounds check before an index. After `if i >= 0 && i < 10`, `i` could
     have type `0..10` in the `then` branch, so `t[i]` on a 10-element tuple
     would check without a second name.
   - An early return. After `if n < 0 { return }`, `n` could have type
     `0..Infinity` for the rest of the function. `val k: 0..Infinity = n else
     { return }` covers this today, at the cost of a second name.
   - A loop counter. In `var i: int = 0` followed by `while i < t.length { t[i];
     i += 1 }` over a 3-element tuple `t`, `i` could have type `0..3` inside the
     body. A `for ... in`
     over a range covers the common case, but not a loop that advances by a
     variable amount.
   - A divisor check. After `if d != 0`, `d` could have type `int & ~0`, so
     `a % d` would return `int` under section 5.1.
   - A `NaN` or infinity check. After `if !Number.isFinite(x) { return }`, `x`
     could have type `finite`, so `Math.floor(x)` would pick the `finite`
     overload and return `int`.

   The costs are the reasons Escalier avoids flow typing today. A binding would
   no longer have one type for its whole scope, which the checker's principal
   types rely on. A `var` that is reassigned after the test, or captured by a
   closure that may reassign it, would lose the refinement. Each comparison
   operator would also need a rule for the range it implies on each branch.
15. **Arithmetic on types.** Section 5.3 computes the result of `+`, `-`, and
   `*` on ranges inside the checker. Exposing the same operations in type
   annotations would let a signature state how its result depends on its
   arguments:
   ```esc
   fn inc<N: int>(n: N) -> Add<N, 1>
   fn concat<A, B>(a: A, b: B) -> ... // length Add<A["length"], B["length"]>
   fn slice<S: int, E: int>(...) -> ... // length Sub<E, S>
   type Grid = 0..Mul<Width, Height>
   ```
   Named intrinsics such as `Add`, `Sub`, and `Mul` need no new syntax. Infix
   operators in types would read better, but `-` already means a negative
   literal and a mapped-type modifier, as in `-readonly`.

   Type-level arithmetic is a step toward dependent typing, where a type names
   a value. `i: Indices<typeof arr>` would tie an index to a particular array.
   `typeof arr` gives `arr`'s declared type, though, not its runtime length. For
   an `Array<T>` that type has the length `ArrayLength`, so the index type is
   just `ArrayIndex`. Tying a type to a value's length needs the checker to
   track that value symbolically. A precise result also needs a solver for
   linear integer constraints, as in proving `i + 1 < arr.length` from
   `i < arr.length - 1`. The intrinsics are cheap and fit the current solver.
   Full dependent typing is a much larger change.
