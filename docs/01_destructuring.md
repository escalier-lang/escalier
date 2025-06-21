# 01 Destructuring

Destructuring can be used to bind parts of an expression to different variables.

```ts
val [a, b, c] = [1, 2, 3] // same as `val a = 1`, `val b = 2`, `val c = 3`
val point = {x: 5, y: 10}
val {x, y} = point // same as `val x = point.x`, `val y = point.y`
```

Destructuring can also used with assignment statements.

```ts
var x = 0
var y = 0
val point = {x: 5, y: 10}
{x, y} = point
```

Destructuring of objects and arrays can include "rest" elements, specified using
ellipsis, to capture the remaining values.

```ts
val [a, ...rest] = [1, 2, 3] // `rest` will have the value `[2, 3]`
val point = {x: 5, y: 10}
val {x, ...rest} = point // `rest` will have the value `{y: 10}`
```

Variables can be renamed when destructuring objects

```ts
val {x: a, y: b} = {x: 5, y: 10} // `a` will be `5` and `b` will be `10`
```

Tuples can be used to swap variables, but only if they are the same type.

```ts
var x: number = 5
var y: number = 10
[y, x] = [x, y]
```
