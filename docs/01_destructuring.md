# 01 Destructuring

Destructuring binds parts of a value to separate names.

```esc
val [a, b, c] = [1, 2, 3]     // a = 1, b = 2, c = 3

val point = {x: 5, y: 10}
val {x, y} = point            // x = 5, y = 10
```

Patterns nest:

```esc
val {p: {x}} = {p: {x: 5}}
val [[a], b] = [[1], 2]
```

## Renaming

```esc
val {x: a, y: b} = {x: 5, y: 10}   // a = 5, b = 10
```

## Rest elements

An ellipsis captures whatever the earlier elements did not.

```esc
val [a, ...rest] = [1, 2, 3]       // rest = [2, 3]
val {x, ...rest} = {x: 5, y: 10}   // rest = {y: 10}
```

The rest binding carries the source's exactness: destructuring an exact object
leaves an exact rest, and an inexact one leaves an inexact rest. See
[Exact Types](07_exact_types.md).

## `val`, `var`, and `mut`

Two different kinds of mutability meet in a binding, and they are independent.

- `val` binds a name once; `var` binds a name that can be reassigned. This is the
  difference JavaScript spells `const` and `let`.
- `mut` marks the *value* as writable through the binding.

```esc
val p = {x: 0}         // cannot reassign p; cannot write p.x
val mut q = {x: 0}     // cannot reassign q; can write q.x
var r = {x: 0}         // can reassign r; cannot write r.x
var mut s = {x: 0}     // can reassign s; can write s.x
```

Inside a destructuring pattern, `mut` attaches to each binding leaf rather than
to the surrounding pattern, because mutability belongs to the place:

```esc
val {mut x, y: mut a} = point
```

Writing `mut {x, y}` is rejected, and the diagnostic names the per-leaf form.

## Type annotations

A binding leaf may carry an annotation. In a refutable form it also narrows; see
[Pattern Matching](04_pattern_matching.md).

```esc
val {x, y}: {x: number, y: number} = point
val {a: n}: {a: number} = record
```

## Ownership

Destructuring moves or borrows each extracted part following the ordinary
ownership rules. Extracting a field of an owned object moves that field's slot
and leaves the siblings usable, and annotating a leaf with `&` borrows instead.
See [Ownership](09_ownership.md).

## Assignment targets

Destructuring works in `val` and `var` declarations and in function parameters.
It does not work on the left of an assignment: the assignable targets are a `var`
name and a member expression such as `obj.x`.

```esc
var x = 0
x = 5             // OK

var mut p = {x: 0}
p.x = 5           // OK

// {x, y} = point // not supported
```
