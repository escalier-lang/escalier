# 04 Pattern Matching

Escalier has three conditional forms that bind: `match`, `if val`, and
`val … else`. All three lower into one intermediate representation, so a pattern
means the same thing wherever it is written. The lowering follows "The Ultimate
Conditional Syntax" by Cheng and Parreaux and lives in
[internal/solver/ucs/](../internal/solver/ucs/).

## `match`

```esc
val output = match value {
    <pattern> => <expr>,
    <pattern> if <guard> => <expr>,
    _ => {
        // a block is an expression
        "fallback"
    },
}
```

Arms are tried in source order and the first match wins.

## Patterns

```esc
match value {
    // literals
    5 => ...,
    "hello" => ...,

    // tuples and arrays
    [fst, snd] => ...,
    [fst, snd, ...rest] => ...,

    // objects
    {a, b} => ...,
    {a, b, ...rest} => ...,
    {a: x, b: y} => ...,

    // class instances
    Point {x, y} => ...,

    // enum variants
    MyOption.Some(value) => ...,
    MyOption.None => ...,

    // wildcard
    _ => ...,
}
```

A binding leaf may be marked `mut`. Mutability lives on the place, so it attaches
to the identifier rather than to the surrounding pattern:

```esc
val {mut x, y: mut a} = point
```

## Narrowing with `:`

A binding may carry a type annotation. The pattern then matches only the part of
the scrutinee that annotation admits, and the binding has the narrowed type.

```esc
fn f(u: number | string) {
    return match u {
        x: number => x,     // x: number
        y: string => y,     // y: string
    }
}
// inferred: fn (u: number | string) -> number | string
```

This is Escalier's only narrowing mechanism, and it is deliberately different
from TypeScript's control-flow narrowing. **A narrow always introduces a new
binding rather than re-typing an existing one.** A binding has exactly one type
for its whole scope, so a narrow cannot change it; it can only bind a new name at
the narrower type.

That keeps narrowing compatible with the one-principal-type-per-expression model
the checker is built on, and it means there is no flow-sensitive re-typing to
reason about. The narrowed value lives on a different name, visible in the source.

An annotation that no member of the scrutinee fits is rejected, since the arm
could never run.

## Guards

An arm may be followed by a condition with access to the bindings the pattern
introduced.

```esc
match value {
    [fst, snd] if fst == snd => ...,
}
```

A guard can fail, so a guarded arm covers nothing on its own for the purposes of
exhaustiveness.

## Exhaustiveness

A `match` must cover its scrutinee. The compiler reports what is missing and, as
far as it can, what edit would cover it.

```esc
enum Color {
    RGB(r: number, g: number, b: number),
    Hex(code: string),
}

fn f(c: Color) {
    return match c {
        Color.RGB(r, g, b) => r,
    }
}
// ERROR: match is not exhaustive; add a branch for `Color.Hex`
```

The rules follow exactness. An **exact** union is covered once every member has a
covering branch. An **inexact** scrutinee admits values no pattern can name, so it
requires a catch-all:

```esc
// ERROR: match is not exhaustive; `{x: number, ...}` admits values no pattern
// names, so add a catch-all branch
```

Two shapes of near-miss get their own message, since the edit differs:

```
match is not exhaustive; `number` is matched only by a guarded branch, whose
guard can fail, so add an unguarded branch for it

match is not exhaustive; `Color.RGB` is matched only by a branch whose own
pattern can fail, so add a branch that matches it irrefutably
```

## `if val` and `if var`

Every pattern usable in a `match` arm works in an `if val`. The binding is in
scope in the consequent.

```esc
fn f(u: number | string) {
    return if val x: number = u { x } else { 0 }
}
// inferred: fn (u: number | string) -> number
```

A bare identifier pattern carries no annotation, so it binds the whole scrutinee:

```esc
if val x = u { x } else { 0 }
// x: number | string
```

`if var` binds mutably instead.

## `val … else`

The refutable form of a `val` binding. The `else` block runs when the pattern
does not match, and it must not fall through — it returns, throws, or otherwise
diverges, or it supplies a value.

```esc
val x = u else { return }
val x: number = u else { return 0 }
val [a, b] = u else { throw "no" }
val x: number = u else { 0 }
```

This is the early-return shape: the binding is in scope for the rest of the
enclosing block, at the narrowed type, with no nesting.

## Moves and borrows in patterns

Destructuring moves or borrows each extracted part following the ordinary
ownership rules, and `match` arm bindings behave the same way. Binding a part of
an owned scrutinee moves that part, and the sibling parts stay usable. See
[Ownership](09_ownership.md).
