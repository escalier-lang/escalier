# 05 Enums

## Syntax

Enums contain one or more variants.  Each variant consists of an identifier 
followed by an optional param list.

```rs
enum Maybe<T> {
    Some(value: T),
    None,
}

val msg = Maybe.Some("hello") // inferred as Maybe<"hello">
if let Maybe.Some(greeting) = msg {
    // `greeting` has type `"hello"`
}

match msg {
    Maybe.Some(greeting) => console.log(`${greeting}, world!`),
    Maybe.None => console.log("nothing here"),
}
```

Enums can also contain object variants which consist of an identifier followed
by an object type annotation.

```rs
enum Shape {
    Circle {center: Point, radius: number},
    Rectangle {x: number, y: number, width: number, height: number},
    Polygon {points: Array<Point>},
}
```

Enums can be extended using spread notation.

```rs
enum Color {
    RGB(number, number, number),
    HSL(number, number, number),
}

enum FutureColor {
    ...Color
    Oklab(number, number, number),
}

val c = Color.RGB(255, 0, 0)
val fc: FutureColor = c
```

**DESIGN NOTE**
The reason for choosing spread notation for extending enums instead of the
`extends` keyword (e.g. `enum FutureColor extends Color`) is that the subtyping
relation is in the opposite direction to `class`'s use of `extends`.

