# 04 Pattern Matching

## `match`

Basic syntax

```rs
val output = match <expr> {
    <pattern_1> => "foo",
    <pattern_2> => "bar",
    ...
    <pattern_n> => {
        // block expression
        "baz"
    }
}
```

```rs
val bar = match foo {
    // primitives and literals
    5 => ...,
    a is 5 => ...,
    number => ...,
    a is number => ...,

    [fst, snd] => ...,
    [fst, snd, ...rest] => ...,

    // Objects
    {a, b} => ...,
    {a, b, ...rest} => ...,
    {a is number, b is string} => ...,
    {a: x, b: y} => ...,
    {a: x is number, b: y is string} => ...,
    
    // Class instances
    Point {x, y} => ...,

    // Enums
    Maybe.Some(value) => ...,
    Maybe.None => ...,

    // defined and non-null
    a! => ...,
    {a!} => ...,
    {a: x!} => ...,
}
```

Pattern can also be followed with a conditional that has access to any bindings
introduced by the pattern, e.g.

```
val bar = match foo {
    [fst, snd] if fst == snd => ...,
}
```

## `if`-`val` and `if`-`var`

All of the patterns that can be used with pattern matching, can also be used in
`if`-`val` and `if`-`var` expressions.

```
if val <pattern> = expr { ... }
if var <pattern> = expr { ... }
```

## `val`-`else` and `var`-`else`

TODO
