# Type Checking Notes

## Recursive Functions

```rs
fn fact(i: number) {
    return if i > 0 {
        i * fact(i - 1)
    } else {
        1
    }
}
```

We can create a placeholder for type for `fact` that's a function as soon as we
run into it.

```rs
let fact = fn(i: number) {
    return if i > 0 {
        i * fact(i - 1)
    } else {
        1
    }
}
```

This is a bit harder because we don't know that `fact` is a function until we
look further ahead.

```rs
let obj = {
    fact: fn(i: number) {
        return if i > 0 {
            i * obj.fact(i - 1)
        } else {
            1
        }
    }
}
```

This is even more difficult because we don't know that `obj` is a object with
a method called `fact` until looking even further ahead.

~~TypeScript gets around these issues by requiring a return type on function that references itself.  We'll do the same.~~

We should be able to create structured placeholders, e.g.

```rs
fact: fn(i: t1) -> t2,
obj: {
    fact: fn(i: t3) -> t4,
}
```

These are so-called structural placeholders.

## Curried Functions

```ts
const foo = (a: number): ((b: number) => number) => (b: number) => a + b;
```

We're repeating the `(b: number)` part twice.  The following also works in TypeScript:

```ts
const foo = (a: number) => (b: number) => a + b;
```

Unfortunately, if `foo` is recursive, it means that we'd have to fully type it
like the first one.

## Captures

In scripts and inside function bodies, newlying defined functions can only 
capture themselves or previously.  This avoids issues where we call a function
that depends on a cpature before the capture has been defined.

In modules, everything is recursive and we don't have to worry about the problem
from the previous paragraph because only type declarations and function declarations
are allowed in modules.

## Action Plan

Get non-recursive functions working first.  Then introduce structural placeholders.
These will require splitting infererence in multiple parts:
- inferring the structural placeholder
- unifying the structural placeholder with the pattern from the variable declaration
  - any fields that isn't a method can be inferred at this point
- adding bindings from the pattern before to the scope used to infer methods before
  inferring them
