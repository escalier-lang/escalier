# 05 Enums

An enum declares one or more variants. Each variant is an identifier followed by
an optional parameter list.

```esc
enum MyOption<T> {
    Some(value: T),
    None,
}

val msg = MyOption.Some("hello")   // msg: MyOption<"hello">

if val MyOption.Some(greeting) = msg {
    // greeting: "hello"
}

match msg {
    MyOption.Some(greeting) => console.log(`${greeting}, world!`),
    MyOption.None => console.log("nothing here"),
}
```

The enum's name binds two things: a **type**, which is a transparent alias for
the union of its variants, and a **namespace** holding one constructor per
variant. `MyOption.Some(5)` calls the constructor and yields `MyOption<5>`.

## Variants are nominal

A variant belongs to its enum, so two enums that share a variant name stay
distinct. A variant renders qualified, as `Color.RGB`, wherever it surfaces —
in a union member, in a diagnostic, or in a narrowed `match` arm.

```esc
enum Color { RGB(r: number, g: number, b: number), Hex(code: string) }
enum Ink   { RGB(r: number, g: number, b: number) }

val c: Color = Color.RGB(255, 0, 0)
// val i: Ink = c   // ERROR: Color and Ink are distinct
```

## Parameter names

Variant parameters may be named or written `_`, in which case the constructor
gets positional names:

```esc
enum E {
    Pair(_: number, _: string),
}
val ctor = E.Pair
// ctor: fn (arg0: number, arg1: string) -> E
```

## Recursion

Enums may be recursive, mutually recursive with each other, and mutually
recursive with classes. Declarations in a module are ordered by dependency graph,
so a variant may reference an enum declared later in the file or in another file
of the same module.

```esc
enum Expr {
    Lit(value: number),
    Add(left: Expr, right: Expr),
}
```

## Exhaustiveness

An enum type is an exact union of its variants, so a `match` over one is checked
for exhaustiveness by variant:

```esc
match c {
    Color.RGB(r, g, b) => r,
}
// ERROR: match is not exhaustive; add a branch for `Color.Hex`
```

See [Pattern Matching](04_pattern_matching.md).

## Placement

Enums are declared at module top level or script top level. A local enum inside a
function body is rejected, the same as a local class.

## TypeScript interop

Escalier enums are variant types with extractors. TypeScript's `enum` is a
numeric or string map, which is a different construct, so importing one models it
as a union of literal types plus a value holding the members:

```ts
// TypeScript
enum MyEnum { Foo, Bar, Baz }
enum StringEnum {
    MouseUp = "mouseup",
    MouseDown = "mousedown",
    MouseClick = "mouseclick",
}
```

```esc
// Escalier representation of the imported declarations
type MyEnum = 0 | 1 | 2
val MyEnum = {Foo: 0, Bar: 1, Baz: 2}

type StringEnum = "mouseup" | "mousedown" | "mouseclick"
val StringEnum = {
    MouseUp: "mouseup",
    MouseDown: "mousedown",
    MouseClick: "mouseclick",
}
```

## Extension with spread (not implemented)

The planned syntax for extending an enum is a spread rather than `extends`:

```esc
enum Color {
    RGB(number, number, number),
    HSL(number, number, number),
}

enum FutureColor {
    ...Color,
    Oklab(number, number, number),
}

val c = Color.RGB(255, 0, 0)
val fc: FutureColor = c
```

Spread is chosen over `extends` because the subtyping relation runs the opposite
way from a class's: a `Color` is a `FutureColor`, where a subclass instance is a
superclass instance. The parser accepts the form; the checker reports it as
unsupported.
