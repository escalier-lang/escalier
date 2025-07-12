# Cycles spec

There are some types of cycles in a `DepGraph` that make it impossible to compile
certain Escalier programs.  This document explains these types of cycles as well
as how to detect them.

Cycles containing only type bindings can be ignored.  All of the following examples
are okay and should **not** be reported.

```
// Okay
type Foo = number | { bar: Bar }
type Bar = string | { foo: Foo }

// Okay
type Node = { value: string, children?: Array<Node> }
```

Cycles between values where the bindings are used outside of function bodies
must **always** be reported.

```
val a = b
val b = a

val obj1 = { foo: obj2.bar }
val obj2 = { bar: obj1.foo }

val [p, q] = [x, 5]
val [x, y] = [p, 10]
```

Cycles where a binding is used inside of a function should be reported, but
**only** when the function it's being used in called outside of a function or
method body.

```
// report a cycle because `a()` is called outside of a function
fn a() { return b }
val b = a()

// report a cycle because `c()` is called outside of a function
val c = fn() { return d }
val d = c()

// report a cycle because `obj1.foo()` is called outside of a function body
val obj1 = { foo() { obj2.bar() } }
val obj2 = { bar: obj1.foo() }
```

Allowed cases:
```
// allowed because bindings are being called inside function bodies
fn a() { b() }
fn b() { a() }

// allowed because bindings are being called inside function bodies
val c = fn() { d() }
val d = fn() { c() }

// allowed because bindings are being called inside method bodies
val obj1 = { foo() { obj2.bar() } }
val obj2 = { bar() { obj1.foo() } }
```
