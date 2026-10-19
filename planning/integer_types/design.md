# Integer and integer range types

## 1. Overview

This document adds two subtypes of `number`.

- `int` is the type of every integral number.
- `n..m` is the type of the integers from `n` up to but not including `m`. So
  `0..3` admits `0`, `1`, and `2`, and does not admit `3`.

The `n..m` syntax works both as a type annotation and as an expression. As a
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
- Section 12 lists milestones and section 13 lists open questions.

## 2. Semantics

### 2.1. `int`

`int` admits exactly the values for which `Number.isInteger(x)` is true. These
are the finite `number` values with no fractional part.

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

### 2.2. `n..m`

`n..m` is the set of integers `k` with `n <= k < m`. The range is half-open. The
lower bound is included and the upper bound is excluded.

Half-open ranges compose without off-by-one adjustments:

- `0..len` covers the valid indices of a collection of length `len`.
- `a..b | b..c` is exactly `a..c`.
- `a..b & b..c` is `never`.
- `0..n` has exactly `n` members when `n >= 0`.

When `n >= m` the set is empty. A range annotation whose bounds are both literal
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

A bound is a type, not a number, so the general rule has to say what a bound
like `int` or `0 | 5` means. The rule is a union over bound values:

> `L..U` is the union of `l..u` over every `l` in `L` and every `u` in `U`.

When `L` and `U` are single literals this is the plain range. Otherwise it
reduces to `min(L)..max(U)`, where `min` and `max` are taken over the integers
each bound admits. A bound that admits arbitrarily large integers reduces to
`Infinity`, and one that admits arbitrarily small integers reduces to
`-Infinity`:

| Annotation | Meaning |
|---|---|
| `(0 \| 5)..10` | `0..10` |
| `0..(5 \| 10)` | `0..10` |
| `0..int` | `0..Infinity` |
| `int..0` | `-Infinity..0` |
| `0..(0..100)` | `0..99` |

The last row comes up with generic bounds in section 9.5. The upper bound ranges
over `0` through `99`, and the largest range it yields is `0..99`. The `0..int`
row is why `0..n` with `n: int` has type `IntRange<0..Infinity>` in section 6.1.

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
- An integral number literal `k` is a subtype of `int`, and of `n..m` when
  `n <= k < m`.
- A non-integral literal such as `1.5` is not a subtype of `int`, and its meet
  with `int` is `never`.
- `a..b <: c..d` when the first range is empty, or when `c <= a` and `b <= d`.
- `n..m <: int` always.
- `int <: n..m` only when the range is `-Infinity..Infinity`.

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
takes an annotation, as in `var i: int = 0`. Section 13 lists widening to `int`
as an open question.

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

`number & ~int` is the set of non-integral numbers plus `NaN` and the two
infinities. No interval names it, so it stays as a residual
`number & ~int` in the normal form. Excluding one regex type from another leaves
a residual of the same kind.

`number & ~(0..10)` mixes both cases. It equals
`(number & ~int) | -Infinity..0 | 10..Infinity`. That form is longer than the input and no
easier to read, so the solver prints `number & ~(0..10)` and keeps the interval
split internal.

### 4.3. Narrowing in `match`

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
guide should point readers at `T & ~U` for numeric exclusion. Section 13 asks
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
| `/` | any | `number` |
| `**` | any | `number` |
| `%` | `int`, `int & ~0` | `int` |
| `%` | `int`, `int` | `number` |
| comparisons | `number`, `number` | `boolean` |

`%` with a zero divisor yields `NaN`, so `int % int` is not closed over `int`.
The result is `int` only when the divisor type excludes `0`. That is a direct use
of negation: `1..Infinity` and `int & ~0` both qualify, since neither contains `0`.
The lexer does not tokenize `%` yet, so this row applies once `%` lands.

The bitwise operators always yield integers in JavaScript. When they are added
they return `int`. `>>>` returns `0..4294967296`.

### 5.2. Overflow

`int + int` and `int * int` overflow to `Infinity` once the magnitude passes
about `1.8e308`. `Infinity` is not an `int`, so typing the result as `int` is
unsound at that boundary. This design accepts that gap. Values of that size are
rare in integer code, and typing every sum as `number` would make `int` useless
for arithmetic. The language guide states the gap.

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

### 5.5. Narrowing on comparisons

`if i < 10` on `i: int` could narrow `i` to `-Infinity..10` in the `then` branch and to
`10..Infinity` in the `else` branch. The narrowing is not needed for the first
milestones. Section 12 schedules it last.

## 6. Range values

### 6.1. What `a..b` evaluates to

As an expression, `a..b` builds a range object. The object is not an `int`. Its
type is `IntRange<A..B>`, where `A` and `B` are the operand types:

```esc
val r = 0..10            // r: IntRange<0..10>
for i in r { ... }       // i: 0..10

fn f(n: int) {
    val s = 0..n         // s: IntRange<0..Infinity>
}
```

The name avoids `Range`, which the DOM library declares as a global interface.

The prelude declares `IntRange` as a class backed by the runtime package
`@escalier/runtime`:

```esc
declare class IntRange<T: int> {
    readonly start: int,
    readonly end: number,
    readonly length: number,
    has(&self, x: number) -> boolean,
    [Symbol.iterator](&self) -> Iterator<T, undefined, undefined>,
}
```

`end` and `length` are `number` because both are `Infinity` for an unbounded
range.

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
subtype of `int` or be `Infinity`. `0..x` with `x: number` reports
`range bound must be an int or Infinity, found number`.

`n..Infinity` iterates without end, so a loop over it needs a `break` or
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

The pattern narrows the scrutinee to the meet of its type with the range, and
section 4.3 gives the coverage rule.

## 7. Stepped ranges

### 7.1. Syntax and meaning

`n..m by s` keeps every `s`-th integer of a range, starting from `n`:

```esc
type Even = 0..10 by 2      // 0, 2, 4, 6, 8

for i in 0..10 by 3 { ... } // i: 0..10 by 3, yields 0, 3, 6, 9
for i in 10..0 by -1 { ... } // yields 10, 9, ..., 1
```

The bounds stay half-open. A positive step yields `n`, `n+s`, `n+2s`, and so on
while the value is below `m`. A negative step yields values while they are above
`m`, so `10..0 by -1` stops before `0`. A step with the wrong sign for its
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

A stepped range is a residue class intersected with an interval. A residue
class is the set of integers that leave the same remainder `r` when divided by
the step `s`. `0..10 by 2` is the residue class `r = 0, s = 2` within `0..10`.
The rules below extend sections 3 and 4.

Subtyping:

- `A <: B` when every member of `A` lies in `B`'s interval and in `B`'s residue
  class.
- When `A` has two or more members, the residue condition holds exactly when
  `B`'s step divides `A`'s step and the two residues agree modulo `B`'s step.
- `0..10 by 4 <: 0..10 by 2` holds. `0..10 by 2 <: 0..10 by 4` does not.
- Every stepped range is a subtype of the unstepped range with the same bounds,
  and so of `int`.

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
  `int & ~(n..m by s)` unreduced instead of expanding it. That is the same kind
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
declare class IntRange<T: int> {
    readonly start: int,
    readonly end: number,
    readonly step: int,
    ...
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
    val r = 0..n by s        // r: IntRange<0..Infinity>
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
`-Infinity` for a range anchored on its upper bound.

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
- a type reference such as `Lo`, `N`, or `int`
- a parenthesized type such as `(0 | 5)` or `(0..100)`

`..` binds tighter than `&` and `|`, so `0..10 | 20..30` is a union of two
ranges. `-5..5` parses as `(-5)..5`.

A value identifier is not a valid bound. `0..MAX` with `val MAX = 10` reports
that `MAX` is a value, and points the user at `0..typeof MAX`.

### 8.4. Printing

The type printer prints `int` and `n..m`. It prints a one-member interval as its
literal, so `5..6` prints as `5`. It wraps a range in parentheses when the range
is a bound of another range, as in `0..(0..100)`. The source printer used by
`printer.Print` gains the same two forms for annotations and the `RangeExpr`
form for expressions.

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

A range with a type-variable bound needs a second form, covered in section 9.5.

### 9.2. Places that change

These sites switch on primitives or literals and each needs a case for
`IntRangeType`:

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
`-Infinity`. `Infinity` and `-Infinity` resolve as bounds without a type
reference, since the prelude gives them no literal type.

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
into an `int` through a check that narrows it:

```esc
if Number.isInteger(n) { ... }      // narrows n to int in the then branch
val k: int = n else { return 0 }    // let-else narrowing
```

### 10.2. Runtime guards

A pattern or narrowing test on the new types emits an inline check.
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
| `IntRange<T>` | `IntRange<T'>`, where `T'` is `T` mapped by this table |

A literal union gives TypeScript callers a real check for small ranges such as a
day of the week or a die face. Above the threshold a union bloats the
declaration and slows TypeScript's checker, so it falls back to `number`.

TypeScript callers can pass any `number` where Escalier expects an `int`, since
nothing checks at the boundary. Exported functions emit no runtime validation
under this design. Section 13 asks whether an opt-in boundary check is worth
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

### 11.3. Stdlib refinements

Some stdlib members always return integers. Typing them as `int` or a range
makes the new types useful without casts. These go through the stdlib override
mechanism rather than edits to the generated `.esc` files:

| Member | Type |
|---|---|
| `Array.prototype.length`, `String.prototype.length` | `0..Infinity` |
| `indexOf`, `lastIndexOf`, `findIndex`, `findLastIndex` | `-1..Infinity` |
| the index argument of `map`, `forEach`, `filter`, and the other array callbacks | `0..Infinity` |
| `Number.isInteger`, `Number.isSafeInteger` | narrow their argument to `int` |
| `Number.MAX_SAFE_INTEGER`, `Number.MIN_SAFE_INTEGER` | their literal values |

Several members look integral and are not, so they stay `number`:

- `Math.floor`, `Math.ceil`, `Math.round`, and `Math.trunc` return `NaN` or an
  infinity for those inputs.
- `charCodeAt` returns `NaN` past the end of the string.
- `parseInt` returns `NaN` on a bad input.
- `Date.prototype.getFullYear` and its siblings return `NaN` on an invalid date.

## 12. Milestones

1. **`int` type.** Covers the lexer-independent parts: `int` as a prelude type
   name, `IntRangeType` with unbounded ends, constrain and lattice rules,
   printing, and `.d.ts` emission as `number`. The `Number.isInteger` narrowing
   lands here too.
2. **Range annotations.** Covers the `DotDot` token, the number-scan fix,
   `RangeTypeAnn` with literal and type-reference bounds, interval meets, joins,
   and the right-hand union normalization from section 3.3. The literal-union
   `.d.ts` emission lands here too.
3. **Negation.** Covers the difference rewrite from section 4.1, residuals that
   leave `int`, and tests over `~int` and `~(n..m)`.
4. **Integer arithmetic.** Covers the result types in section 5.1, computed from
   concrete operand types.
5. **Range values.** Covers `RangeExpr`, the `IntRange` prelude class and its
   runtime implementation, `for ... in` lowering, and range patterns.
6. **Coverage.** Covers interval-based exhaustiveness and narrowing in `match`,
   and tuple indexing by range.
7. **Stepped ranges.** Covers `by` in types, values, and patterns, the
   residue-class lattice rules, and stepped loop lowering from section 7.
8. **Stdlib refinements.** Covers the overrides in section 11.3.
9. **Later work.** Covers interval arithmetic with steps, generic bounds, and
   narrowing on comparisons.

The `float32` printing fix from section 10.4 lands before milestone 5.

## 13. Open questions

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
6. **Inclusive ranges.** `n..=m` would name `n` through `m` inclusive. It is not
   needed for this design and can be added later without conflict.
7. **Boundary checks.** Exported functions could validate `int` and range
   parameters when called from TypeScript, behind a compiler flag.
8. **Literal-union threshold.** Section 11.1 uses 16 members as the cutoff for
   emitting a range as a TypeScript literal union.
9. **`bigint` ranges.** `1n..10n` would need a parallel interval atom in the
   `bigint` family. The lexer does not read `bigint` literals yet.
10. **Template literal types.** `` `${int}` `` could match strings of decimal
   digits with an optional sign. The checker has the regex machinery for it.
