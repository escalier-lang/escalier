# 07 Exact Types

TypeScript uses structural sub-typing which means that an object conforms to a
particular type as long as it has all of the same properties.  It's fine if the
object has extra properties.

This leads to some interesting consequences.  In particular it means that you
can't be sure it doesn't have extra properties.  As a result, TypeScript defines
the type of `Object.keys` to be `string[]`.

We can do better than this, but it requires differentiating types which we know
don't have extra properties (exact types) from those that might (inexact types).

## Syntax

```
type ExactPoint = {x: number, y: number}
type InexactPoint = {x: number, y: number, ...}
```

## Semantics

```
declare var p: ExactPoint
declare var q: InexactPoint

val a: InexactPoint = p // Okay
val b: ExactPoint = q // Error! Exact types can't have properties

val {x, ...rest} = p // `rest` will be exact
val {x, ...rest} = q // `rest` will be inexact

val cp = {color, ...p} // `cp` will be exact
val cq = {color, ...q} // `cq` will be inexact
```

## Interop

As mentioned at the start of this page, TypeScript doesn't support exact types.
This means that all types imported from TypeScript packages will be inexact.

In order to preserve Escalier types across package boundaries, all declarations
will include JSDoc comments that include a field containing type annotations of
the original Escalier types.

## Tuples

The same idea cna be extended to tuples, with the following syntax.

```
type ExactTuple = [string, number]
type InexactTuple = [string, number, ...]
```

The semantics and TypeScript interop would be similar as well.
