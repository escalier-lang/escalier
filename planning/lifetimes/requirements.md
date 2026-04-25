# Lifetimes: Replacing `mut?` with Liveness-Based Mutability Transitions

## Motivation

Escalier currently uses `mut?` as a type-level annotation to represent uncertain
mutability during inference. While functional, it adds complexity:

- `mut?` leaks inference internals into type signatures
- The `MutabilityUncertain` variant requires special handling throughout the
  checker (unwrapping, stripping, finalization)
- Users must understand a three-way distinction (`mut`, `mut?`, immutable) when
  reasoning about types

The proposed replacement uses **lifetime/liveness analysis** to determine when
mutability transitions are safe, eliminating `mut?` entirely.

## Core Concept

Instead of annotating types with `mut?`, the compiler tracks which variables
reference a value and whether those references are still **live** (used after a
given point). Mutability transitions are allowed when no conflicting live
references exist.

For cross-function aliasing, **lifetime annotations** track whether a function's
return value may alias one of its parameters. These annotations are inferred from
function bodies when possible and hidden from users by default, only surfacing in
error messages when a lifetime violation occurs.

## Default Mutability

Escalier is biased towards immutability. Values are immutable by default
unless explicitly annotated with `mut` or inferred as mutable from context.
This section describes how default mutability is determined for different
kinds of values.

### Literals

Object literals and tuple literals are inferred as **immutable** by default:

```esc
val point = {x: 0, y: 0}       // type: {x: number, y: number} (immutable)
val pair = (1, "hello")         // type: (number, string) (immutable)
val items = [1, 2, 3]           // type: Array<number> (immutable)
```

To create a mutable value, annotate the binding with `mut`:

```esc
val point: mut {x: number, y: number} = {x: 0, y: 0}
val items: mut Array<number> = [1, 2, 3]
```

**Exception:** Object literals with mutating methods default to mutable,
following the same logic as classes. If an object literal defines a method
that takes `mut self`, the literal is inferred as mutable:

```esc
val counter = {
    count: 0,
    fn increment(mut self) -> void {
        self.count = self.count + 1
    },
}
// type: mut {count: number, fn increment(mut self) -> void} (mutable)
counter.increment()  // OK

val point = {
    x: 0,
    y: 0,
    fn distanceTo(self, other: {x: number, y: number}) -> number {
        Math.sqrt((self.x - other.x) ** 2 + (self.y - other.y) ** 2)
    },
}
// type: {x: number, y: number, ...} (immutable — no mut self methods)
```

### Classes

The default mutability of a class instance depends on whether the class
has methods that mutate `self`. The compiler inspects the class body to
determine this:

**No mutating methods → immutable by default:**

```esc
class Point(x: number, y: number) {
    x,
    y,
    fn distanceTo(self, other: Point) -> number {
        // only reads self and other — no mutation
        Math.sqrt((self.x - other.x) ** 2 + (self.y - other.y) ** 2)
    }
}

val p = Point(1, 2)       // type: Point (immutable)
val q: mut Point = Point(3, 4)  // explicit mut required
```

**Has mutating methods → mutable by default:**

```esc
class Counter(var count: number) {
    count,
    fn increment(mut self) -> void {
        self.count = self.count + 1
    }
}

val c = Counter(0)         // type: mut Counter (mutable by default)
c.increment()              // OK
val frozen: Counter = c    // freeze — c must be dead after this
```

Built-in collection types like `Map`, `Set`, and `Array` follow this rule
naturally — they have mutating methods (`set`, `add`, `push`), so their
constructors return mutable instances by default.

### Overriding the Default with `data` Modifier

Some classes have mutating methods but should still default to immutable.
For example, a data class might have a `withX` method that returns a new
instance rather than mutating in place, plus a rarely-used `setX` method
for performance-critical code. The class author can override the default
with the `data` modifier:

```esc
data class Config(host: string, port: number) {
    host,
    port,
    fn withHost(self, host: string) -> Config {
        Config(host, self.port)  // returns new instance
    }
    fn setHost(mut self, host: string) -> void {
        self.host = host  // mutates in place
    }
}

val c = Config("localhost", 8080)  // type: Config (immutable despite setHost)
// c.setHost("0.0.0.0")           // ERROR: c is immutable
val m: mut Config = Config("localhost", 8080)  // explicit mut required
m.setHost("0.0.0.0")              // OK
```

The `data` modifier on the class declaration means:
- The constructor returns an immutable instance by default
- Callers must explicitly write `mut` to get a mutable binding
- The class may still have `mut self` methods — they just aren't
  accessible through an immutable reference

This is useful for:
- **Data classes** that are primarily immutable but offer mutating methods
  as an optimization escape hatch
- **Builder-like types** that are constructed immutably and only mutated
  in specific contexts
- **Wrapper types** around mutable internals where the wrapper itself
  should be treated as a value

### Summary

| Value Kind | Default Mutability |
|------------|-------------------|
| Object literal (no `mut self` methods) | Immutable |
| Object literal (has `mut self` methods) | Mutable |
| Tuple literal | Immutable |
| Array literal | Immutable |
| Class instance (no `mut self` methods) | Immutable |
| Class instance (has `mut self` methods) | Mutable |
| `data class` instance | Immutable (regardless of methods) |

## Rules

### Rule 1: Mutable-to-Immutable Transition

A mutable value can be assigned to a variable with an immutable type when **no
live mutable references** to that value exist after the assignment point, **or**
when the immutable target itself is dead (never used after the assignment). The
transition is only dangerous when both a mutable alias and the immutable alias
are live simultaneously — only then can a mutation through the mutable alias
violate the immutable alias's stability guarantee.

```esc
val items: mut Array<number> = [1, 2, 3]
items.push(4)
// `items` is not used after this point

val snapshot: Array<number> = items  // OK: no live mutable refs remain
print(snapshot.length)
```

This prevents the scenario where a mutable reference mutates data that an
immutable reference assumes is stable.

**Error case:**

```esc
val items: mut Array<number> = [1, 2, 3]
val snapshot: Array<number> = items  // ERROR: `items` is used mutably below
items.push(4)                        // this mutation would violate `snapshot`'s
                                     // immutability guarantee
print(snapshot.length)
```

**OK — target is dead:**

```esc
val items: mut Array<number> = [1, 2, 3]
val snapshot: Array<number> = items  // OK: `snapshot` is never used
items.push(4)                        // no immutable alias observes this
```

### Rule 2: Immutable-to-Mutable Transition

An immutable value can be assigned to a variable with a mutable type when **no
live immutable references** to that value exist after the assignment point, **or**
when the mutable target itself is dead (never used after the assignment). The
transition is only dangerous when both an immutable alias and the mutable alias
are live simultaneously — only then can a mutation through the mutable alias
violate the immutable alias's stability guarantee.

```esc
val config: {host: string} = {host: "localhost"}
print(config.host)
// `config` is not used after this point

val mutableConfig: mut {host: string} = config  // OK: no live immutable refs
mutableConfig.host = "0.0.0.0"
```

This prevents mutation of data that other variables expect to be immutable.

**Error case:**

```esc
val config: {host: string} = {host: "localhost"}
val mutableConfig: mut {host: string} = config  // ERROR: `config` used below
mutableConfig.host = "example.com"
print(config.host)  // would see mutated value since mutableConfig.host was changed
```

**OK — target is dead:**

```esc
val config: {host: string} = {host: "localhost"}
val mutableConfig: mut {host: string} = config  // OK: `mutableConfig` is never used
print(config.host)                               // no mutable alias exists to cause mutation
```

### Rule 3: Multiple Mutable References to the Same Value Are Allowed

Unlike Rust, multiple variables may reference the same mutable value
simultaneously.

```esc
val a: mut {x: number} = {x: 1}
val b: mut {x: number} = a  // OK: both are mutable
b.x = 2
print(a.x)  // prints 2
```

This is a deliberate simplification. The dangerous case — mutating through one
reference while another expects immutability — is prevented by Rules 1 and 2.
Multiple mutable references are not inherently dangerous in a single-threaded
context.

## Alias Tracking

### Local Variables

When a value is assigned from one variable to another, both variables are added
to the same **alias set**. The liveness rules (Rules 1 and 2) check all members
of an alias set, not just the variable being assigned.

```esc
val p: mut Point = {x: 0, y: 0}
val r: mut Point = p        // r and p are in the same alias set
val q: Point = p             // ERROR: r is a live mutable alias of p,
                             //        and q is live (used below)
r.x = 5
print(q.x)
```

If the immutable alias `q` were never used after the assignment, no error
would occur — both sides must be live simultaneously for the transition to
be dangerous.

### Object Properties

When a value is stored into an object property, the alias sets of the
containing object and the stored value are **merged**. All variables that
alias the container also become aliases of the stored value. The value is
considered to have a live mutable reference for as long as any variable in
the merged alias set is live.

```esc
val p: mut Point = {x: 0, y: 0}
val obj: mut {point: mut Point} = {point: p}  // obj's and p's alias sets merge
val q: Point = p             // ERROR: obj is live and provides mutable access
obj.point.x = 5
print(q.x)
```

Merging (rather than simply adding the container to the value's alias set)
ensures that transitive connections through property chains are preserved
even when intermediate variables are reassigned. See the Recursive and Cyclic
Data Structures section below for details.

### Closures

When a closure captures a variable from the enclosing scope, it creates an
implicit alias. The closure is treated as holding a reference to the captured
variable for as long as the closure itself is live.

```esc
val items: mut Array<number> = [1, 2, 3]
val f = fn() { items.push(4) }  // f captures `items` mutably
val snapshot: Array<number> = items  // ERROR: f is live and holds a mutable
                                     // reference to `items`
f()
```

If the closure only reads the captured variable, it holds a read-only capture:

```esc
val items: mut Array<number> = [1, 2, 3]
val f = fn() { print(items.length) }  // f captures `items` as read-only
items.push(4)                          // OK: f has read-only access, no conflict
f()                                    // f observes the mutated state (length 4)
```

Note: "read-only capture" means the closure cannot mutate the captured variable
— this is the aliasing guarantee enforced by Rule 1 (mutable-to-immutable
transition). It does **not** mean the captured value is frozen: external mutations
through the original mutable reference (like `items.push(4)` above) are still
permitted and will be observed by the closure at runtime. This is safe in the
single-threaded model because no conflicting mutability transition has occurred
— `items` remains `mut` throughout.

A closure that captures a mutable reference and is stored into a variable, passed
to a function, or returned from a function extends the liveness of the captured
reference accordingly. The alias set of the captured variable must include the
closure (and any variable holding the closure).

### Destructuring

When a value is destructured, each extracted binding becomes an alias of the
corresponding part of the original value:

```esc
val obj: mut {point: mut Point} = {point: {x: 0, y: 0}}
val {point} = obj       // `point` aliases `obj.point`
point.x = 5             // mutates through the alias
print(obj.point.x)      // prints 5
```

The destructured binding joins the alias set of the property it was extracted
from. Liveness rules apply to all members of the alias set as usual.

### Reassignment

When a variable is reassigned, it leaves its previous alias set and (if assigned
from another variable) joins the new one:

```esc
val a: mut Point = {x: 0, y: 0}
var b: mut Point = a       // b aliases a
b = {x: 1, y: 1}           // b leaves a's alias set (now points to a fresh value)
val q: Point = a            // OK: b no longer aliases a
```

Note: `var` (not `val`) is required for reassignment. After reassignment, the
old alias relationship is severed and the variable's membership in alias sets is
updated based on its new value.

### Conditional Aliasing

When a variable is assigned from different values depending on control flow,
it is added to the alias sets of **all** possible source values:

```esc
val a: mut Point = {x: 0, y: 0}
val b: mut Point = {x: 1, y: 1}
val c: mut Point = if cond { a } else { b }
// c is in the alias set of both a AND b
val q: Point = a  // ERROR: c is a live mutable alias of a
c.x = 5
c.y = 10
```

This is the conservative choice — the compiler cannot know which branch was
taken, so it must assume the worst case.

### Method Receivers (`self`)

When a method stores a parameter into `self`, the receiver becomes an alias
of that parameter. This is tracked the same way as object property assignments:

```esc
class Container(item: mut Point) {
    item,

    fn setItem(mut self, p: mut Point) -> void {
        self.item = p  // self is now an alias of p
    }
}

val p: mut Point = {x: 0, y: 0}
val c: mut Container = Container({x: 1, y: 1})
c.setItem(p)                 // c now aliases p (through c.item)
val q: Point = p             // ERROR: c is live and provides mutable access to p
c.item.x = 5
print(q.x)
```

The lifetime annotation on the method signature captures this relationship.
If `setItem` stores its parameter into `self`, the inferred signature links
the parameter's lifetime to the receiver, so callers can track the alias:

```esc
fn setItem<'a>(mut 'a self, p: mut 'a Point) -> void
```

### Constructors

In Escalier, constructor parameters are part of the class signature — there is
no separate constructor function. When a constructor parameter is stored as a
field, the constructed object aliases that parameter. This is the constructor
counterpart to the method receiver case above — the difference is that the
object does not exist before the call, so the lifetime appears on the
constructed type rather than on `self`:

```esc
class Container(item: mut Point) {
    item,
}

val p: mut Point = {x: 0, y: 0}
val c = Container(p)     // c.item aliases p
val q: Point = p          // ERROR: c is live and provides mutable access to p
c.item.x = 5
print(q.x)
```

The inferred lifetime for the constructor links the parameter to the
constructed type:

```esc
// Inferred: Container<'a>(item: mut 'a Point)
// Callers see: Container(p) produces a Container<'a>
```

Here `'a` on `Container<'a>` means the constructed value holds a reference
with lifetime `'a` — callers know that `c` aliases `p` and must treat them
as part of the same alias set.

#### Primitive and Fresh-Value Parameters

When a constructor parameter is a primitive or a value type that cannot alias
(e.g. `number`, `string`, `boolean`), no lifetime is needed:

```esc
class Point(x: number, y: number) {
    x,
    y,
}

val p = Point(10, 20)  // no aliasing — x and y are primitives
```

#### Multiple Reference Parameters

When a constructor stores multiple reference-typed parameters, each gets its
own lifetime linking it to the constructed type:

```esc
class Pair(first: mut Point, second: mut Point) {
    first,
    second,
}
// Inferred: Pair<'a, 'b>(first: mut 'a Point, second: mut 'b Point)

val a: mut Point = {x: 0, y: 0}
val b: mut Point = {x: 1, y: 1}
val pair = Pair(a, b)    // pair aliases both a and b
val q: Point = a          // ERROR: pair is a live mutable alias of a
```

Both `'a` and `'b` appear on the constructed type, so callers know the
`Pair` aliases both parameters.

#### Elision

Constructors do not have a receiver (`self`), so elision rule 3 (method
receiver) does not apply. If a constructor has a single reference parameter,
elision rule 1 applies — the constructed type's lifetime is assumed to match
the input. For constructors with multiple reference parameters, explicit
annotation is required to disambiguate (or inference determines the lifetimes
from the class definition).

### Function Calls

Aliasing through function calls is tracked via **lifetime annotations** on
function signatures. See the Lifetime Annotations section below.

### Recursive and Cyclic Data Structures

Recursive data types (linked lists, trees, graphs) interact with alias
tracking through property assignments. When a value is stored into an object
property, the alias sets of the containing object and the stored value are
**merged** — all variables that alias the container also become aliases of
the stored value.

#### Why Merge on Property Assignment

Simple alias tracking (adding the container to the value's alias set) loses
transitive connections when intermediate variables are reassigned during
iterative construction:

```esc
var current: mut Node = Node(1, undefined)
val head: mut Node = current
for val i in [2, 3, 4] {
    val next: mut Node = Node(i, undefined)
    current.next = next     // merge head's alias set with next's
    next.prev = current     // already in same set
    current = next          // current leaves and re-joins same set
}
```

Without merging, when `current` is reassigned each iteration, `head` would
lose its connection to the new nodes. With merging, `current.next = next`
merges the alias sets of `{head, current}` and `{next}` into
`{head, current, next}`. When `current` is reassigned, `head` remains
connected to `next` — the transitive property chain is preserved.

After the loop, all construction variables are in one alias set. This is
the correct behavior: the entire linked list is one interconnected value,
and transitioning any part requires all mutable handles to be dead.

#### Building and Freezing Cyclic Structures

A mutable cyclic structure can be transitioned to immutable by assigning it
to an immutable variable, as long as all mutable aliases are dead (not used
afterward):

```esc
val n1: mut Node = Node(1, undefined)
val n2: mut Node = Node(2, n1)
n1.next = n2                    // creates cycle
val frozen: Node = n1           // OK: n1 and n2 are not used after this point
print(frozen.value)
```

Liveness analysis sees that `n1` and `n2` are both dead after the
`val frozen` assignment — neither is referenced below — so the
mut-to-immutable transition succeeds.

For multiple entry points into the same structure, assign each separately.
All mutable aliases must be dead after the last assignment:

```esc
val n1: mut Node = Node(1, undefined)
val n2: mut Node = Node(2, n1)
n1.next = n2
val first: Node = n1            // OK: n1 and n2 are not used after this
val second: Node = n2           //     (both assignments happen before any use)
print(first.value)
print(second.next.value)
```

**Error case — construction variable used after transition:**

```esc
val n1: mut Node = Node(1, undefined)
val n2: mut Node = Node(2, n1)
n1.next = n2
val frozen: Node = n1
print(n2.value)     // ERROR: n2 is a live mutable alias of n1
```

The fix is to either access through the immutable reference, or assign `n2`
to an immutable variable before using it:

```esc
print(frozen.next.value)  // OK: frozen is immutable
```

A `do` block or function can also be used as an ergonomic convenience to
guarantee that construction variables go out of scope:

```esc
val frozen: Node = do {
    val n1: mut Node = Node(1, undefined)
    val n2: mut Node = Node(2, n1)
    n1.next = n2
    n1              // n1 and n2 go out of scope → dead
}
```

This is equivalent to the flat version but makes it impossible to
accidentally reference the construction variables afterward.

#### Acyclic Recursive Structures

For acyclic recursive structures (trees, singly-linked lists without back
pointers), the same merge rule applies but is less restrictive in practice.
Since there are no cycles, it is possible to freeze subtrees independently
as long as the mutable handles to those subtrees are dead:

```esc
val left: mut Tree = Tree(1, undefined, undefined)
val right: mut Tree = Tree(2, undefined, undefined)
val root: mut Tree = Tree(0, left, right)
// left, right, and root are all in the same alias set (merged via property assignment)

// To freeze, ensure all construction vars are dead:
val frozen: Tree = root         // OK: left, right, root not used after this
print(frozen.left.value)
```

#### Traversal Functions

Functions that traverse recursive structures work naturally with lifetime
inference. The lifetime tracks the aliasing between input and output without
requiring recursive lifetime parameters on the type itself:

```esc
fn last(node: Node) -> Node {
    if node.next == undefined { return node }
    return last(node.next)
}
// Inferred: fn last<'a>(node: 'a Node) -> 'a Node
// Return value aliases the input — no recursive lifetime on Node needed
```

The type `Node` does not need lifetime parameters in its definition. Aliasing
relationships are tracked at the variable level through alias sets, not
encoded into the type structure. The lifetime `'a` on the function signature
tracks the relationship between the function's input and output, which is
sufficient for callers to maintain correct alias sets.

## Liveness Analysis

### Definition

A variable is **live** at a program point if there exists a path from that point
to a use of the variable. A variable is **dead** after its last use.

### Scope

The analysis is **intraprocedural** (within a single function body). Cross-function
aliasing is handled by lifetime annotations on function signatures.

### Control Flow

Liveness must account for branching:

```esc
val items: mut Array<number> = [1, 2, 3]
if condition {
    items.push(4)  // `items` is live here
}
// `items` might still be live depending on control flow
val snapshot: Array<number> = items  // must be safe on ALL paths
```

A variable is considered live at a point if it is live on **any** path from that
point forward.

### Loops

Variables used inside a loop body are live for the entire duration of the loop:

```esc
val items: mut Array<number> = [1, 2, 3]
val snapshot: Array<number> = items  // snapshot aliases items
for val item in snapshot {
    items.push(item)  // ERROR: `items` is a live mutable alias of `snapshot`
}
print(snapshot.length)
```

### Early Returns and Throws

An early `return` or `throw` terminates the current path, which affects liveness.
A variable that is only used after a terminating statement on one branch may be
dead on the branch that returns or throws:

```esc
val items: mut Array<number> = [1, 2, 3]
if condition {
    return items  // early return — `items` is dead on this path after this point
}
// on this path, `items` is still live
val snapshot: Array<number> = items  // OK: on the non-returning path, no conflict
print(snapshot.length)
```

The same applies to `throw` expressions, since they also terminate the current
path:

```esc
val items: mut Array<number> = [1, 2, 3]
if items.length == 0 {
    throw Error("empty")  // `items` is dead on this path
}
val snapshot: Array<number> = items  // OK: only reachable when items is non-empty
print(snapshot.length)
```

Liveness is computed per-path. A variable is live at a point if it is used on
**any** path from that point forward (consistent with the Control Flow rule).
Early returns and throws reduce the set of paths on which a variable may be
live, which can enable transitions that would otherwise be rejected.

## Lifetime Annotations

### Purpose

Lifetime annotations track aliasing relationships across function boundaries.
They answer the question: "does this function's return value alias any of its
parameters?" This information allows callers to correctly maintain alias sets
without needing to inspect the function body.

### Syntax

Lifetime parameters are introduced on functions and applied to reference-typed
parameters and return types:

```esc
fn identity<'a>(p: mut 'a Point) -> mut 'a Point { return p }
fn first<'a, 'b>(a: mut 'a Point, b: mut 'b Point) -> mut 'a Point { return a }
fn sum(items: Array<number>) -> number { ... }  // no lifetimes needed
```

A lifetime `'a` on both a parameter and the return type means the return value
may alias that parameter. If a parameter has no lifetime that appears in the
return type, the function does not let that reference escape via the return value.

#### Multiple Lifetimes on a Return Type

When a function may return one of several parameters depending on control
flow, the return type carries multiple lifetimes using `|` syntax — consistent
with Escalier's union type syntax (`A | B`):

```esc
fn pick<'a, 'b>(a: 'a Point, b: 'b Point, cond: boolean) -> ('a | 'b) Point {
    if cond { a } else { b }
}
```

The `('a | 'b) Point` return type means the return value may alias `a` (lifetime
`'a`) or `b` (lifetime `'b`), but the compiler doesn't know which at compile
time. At the call site, the result is added to the alias sets of **both**
arguments — the cross-function equivalent of conditional aliasing (see
Conditional Aliasing above).

```esc
val x: mut Point = {x: 0, y: 0}
val y: mut Point = {x: 1, y: 1}
val result = pick(x, y, true)
val frozen: Point = x  // ERROR: result is a live mutable alias of x
                        // (result may alias x through lifetime 'a)
```

Parentheses are required to disambiguate: `('a | 'b) Point` vs
`'a | 'b Point` (which would parse as `'a | ('b Point)`).

For functions where the return value aliases only a single parameter, the
single-lifetime form is used as usual: `'a Point`. The `|` form is only
needed when multiple lifetimes apply to the same return type.

This syntax can be inferred from function bodies — the inference (Phase 8.3
in the implementation plan) detects that multiple branches return different
parameters and creates the combined lifetime. Explicit annotation is needed
only for body-less declarations (interface methods, external functions).

### Inference

Lifetime annotations are **inferred from function bodies** whenever possible:

- If the function body returns a parameter (or a property of a parameter), the
  return type is given the same lifetime as that parameter
- If the function body stores a parameter into an external location (e.g. a
  module-level variable), this is treated as an **escaping reference**. The
  parameter is assigned a special `'static` lifetime, meaning the caller must
  treat the value as permanently aliased — no mutability transitions are
  allowed on the value after the call. The compiler reports an error at the
  call site explaining that the function captures the reference, and suggests
  passing a clone if the caller needs to retain mutable access.
- If the function body does not return or store any parameter, no lifetime
  annotation is needed

**Inference limitations** — explicit annotations are required when inference
cannot determine the aliasing relationship:

- Functions whose source is not available (external packages)
- Interface/trait method declarations (the implementation is not known)
- Higher-order functions where aliasing depends on a callback parameter

For higher-order functions, the lifetime annotation goes on the callback's type:

```esc
fn apply<'a>(f: fn(mut 'a Point) -> mut 'a Point, p: mut 'a Point) -> mut 'a Point {
    return f(p)
}
```

Here `'a` threads through the entire chain: `p` flows into `f`, and `f`'s return
value flows out of `apply`. The caller knows the result aliases `p`:

```esc
val p: mut Point = {x: 0, y: 0}
val r: mut Point = apply(identity, p)  // r aliases p (lifetime 'a)
val q: Point = p                       // ERROR: r is a live mutable alias of p
r.x = 5
print(q.x)
```

If the callback does **not** return an alias of its argument, different lifetimes
express that the result is independent:

```esc
fn transform<'a>(f: fn(mut Point) -> mut Point, p: mut 'a Point) -> mut Point {
    return f(p)
}
```

Here `f`'s parameter and return type have no shared lifetime, so the compiler
does not assume the callback's return value aliases its input. The return type
of `transform` also has no lifetime linking it to `p`, so callers know the
result is independent:

```esc
val p: mut Point = {x: 0, y: 0}
val r: mut Point = transform(clone, p)  // r does NOT alias p
val q: Point = p                        // OK: r is independent
r.x = 5
print(q.x)                              // prints 0
```

If the callback should **not mutate** the value passed to it, its parameter is
declared immutable. This lets callers continue using mutable references freely
after the call, since the callback only had read access:

```esc
fn inspect<'a>(f: fn(Point) -> void, p: mut 'a Point) -> void {
    f(p)
}
```

Here `f` takes an immutable `Point`, so even though `p` is mutable, the callback
cannot modify it. Since `f` returns `void` and doesn't alias `p`, the caller
retains full mutable access afterward:

```esc
val p: mut Point = {x: 0, y: 0}
inspect(fn(pt) { print(pt.x) }, p)  // OK: callback only reads p
p.x = 5                              // OK: no aliases created
val q: Point = p                     // OK: p is dead after this
print(q.x)                           // prints 5
```

This is useful for iteration patterns like `forEach` where the callback should
observe but not modify elements:

```esc
fn forEach<T>(items: Array<T>, f: fn(T) -> void) -> void {
    // ...
}

val items: mut Array<number> = [1, 2, 3]
forEach(items, fn(n) { print(n) })  // callback gets immutable access
items.push(4)                        // OK: no aliases, items still mutable
```

### Interface Methods / Dynamic Dispatch

When a method is declared on an interface (or trait), the compiler cannot inspect
an implementation body to infer lifetimes — the implementation is not known at
the declaration site. Lifetime annotations on interface methods must be declared
explicitly.

#### Syntax

Lifetime parameters appear on the method declaration, just like on standalone
functions:

```esc
interface Transform {
    fn apply<'a>(self, p: mut 'a Point) -> mut 'a Point
}
```

#### Implementations Must Match

When a class implements an interface, its method signatures must be compatible
with the declared lifetimes. The compiler verifies that the implementation does
not violate the aliasing contract.

**Note:** Escalier does not currently support an `implements` clause on class
declarations. Support for `implements` will be added in the future. Once
available, the compiler will verify lifetime compatibility between the class's
methods and the interface's declared lifetimes. Until then, lifetime
verification occurs at use sites — when a class instance is passed where an
interface type is expected, the compiler checks that the class's method
signatures are compatible.

```esc
// Future syntax (once `implements` is supported):
class Mirror() implements Transform {
    fn apply<'a>(self, p: mut 'a Point) -> mut 'a Point {
        return p  // OK: returns the parameter, consistent with 'a
    }
}

class Cloner() implements Transform {
    fn apply<'a>(self, p: mut 'a Point) -> mut 'a Point {
        return {x: p.x, y: p.y}  // OK: returns a fresh value — this is more
                                  // conservative than needed, but safe
    }
}

class Translator(dx: number, dy: number) implements Transform {
    dx,
    dy,
    fn apply<'a>(self, p: mut 'a Point) -> mut 'a Point {
        p.x = p.x + self.dx
        p.y = p.y + self.dy
        return p  // OK: returns the parameter, consistent with 'a
    }
}
```

An implementation that is *more conservative* than the interface (e.g. returning
a fresh value when the interface says it *may* alias) is always allowed. The
interface declares the worst case; implementations may be stricter.

#### Calling Through an Interface

When calling a method through an interface type, the caller uses the interface's
declared lifetimes for alias tracking — not any specific implementation's:

```esc
fn process<'a>(t: Transform, p: mut 'a Point) -> mut 'a Point {
    return t.apply(p)  // result aliases p, per Transform's declaration
}

val p: mut Point = {x: 0, y: 0}
val r: mut Point = process(cloner, p)  // r aliases p (from caller's perspective)
val q: Point = p                       // ERROR: r is a live mutable alias of p
```

Even though `Cloner.apply` returns a fresh value, the caller only sees the
`Transform` interface which declares that the return may alias `p`. This is
conservative but sound — the caller cannot know which implementation will run.

#### Default for Unannotated Interface Methods

If an interface method has no explicit lifetime annotations, the elision rules
apply (see below). If elision is ambiguous (e.g. multiple reference parameters
and a reference return type), the compiler requires explicit annotation and
reports an error.

### Recursive and Mutually Recursive Functions

#### Simple Recursion

For recursive functions, the compiler infers lifetimes from the function body
the same way as for non-recursive functions. The key insight is that lifetime
annotations describe the aliasing relationship between parameters and the return
value — this relationship is determined by the function's structure, not by
whether it calls itself.

```esc
fn last<'a, T>(items: 'a Array<T>) -> 'a T | undefined {
    if items.length == 0 {
        return undefined
    } else if items.length == 1 {
        return items[0]        // returns an element — aliases input
    } else {
        return last(items)     // recursive call with same input — same lifetime
    }
}
```

The compiler can infer `'a` here: both the base case (`items[0]`) and the
recursive case (`last(items)`) return values derived from the input parameter.
The recursive call does not introduce a new aliasing relationship — it
propagates the same one.

#### When Recursion Does Not Affect Lifetimes

Many recursive functions return primitives or fresh values, so no lifetime
annotation is needed:

```esc
fn length<T>(items: Array<T>) -> number {
    if items.length == 0 {
        return 0
    } else {
        return 1 + length(items.slice(1))
    }
}
// No lifetimes needed — returns a number
```

#### Mutual Recursion

For mutually recursive functions, the compiler must infer lifetimes for all
functions in the cycle simultaneously using fixed-point computation:

```esc
fn processNode<'a>(node: 'a Node) -> 'a Value {
    if node.isLeaf {
        return node.value          // aliases input
    } else {
        return processChildren(node.children)
    }
}

fn processChildren<'a>(children: 'a Array<Node>) -> 'a Value {
    val first = children[0]
    return processNode(first)      // mutual recursion — same lifetime
}
```

The compiler analyzes the cycle as a unit:
1. Start by assuming no aliasing for all functions in the cycle
2. Analyze each function body, using the current assumptions for calls to other
   functions in the cycle
3. Update the lifetime annotations based on the analysis
4. Repeat until the annotations stabilize (fixed point)

In practice, most mutual recursion follows the same pattern as simple recursion:
the lifetime either propagates through (return value derived from input) or
doesn't (return value is fresh/primitive). The fixed-point computation typically
converges in one or two iterations.

#### When Explicit Annotations Are Needed

If the fixed-point computation does not converge (unlikely in practice) or if
the compiler cannot determine the aliasing relationship from the function bodies
(e.g. due to complex control flow), explicit lifetime annotations are required.
The compiler reports an error pointing to the recursive cycle and asks for
annotations.

### Elision Rules

Elision rules apply only to **body-less declarations** — interface method
signatures, external function declarations, and imported TypeScript types —
where the compiler has no function body to inspect. For functions **with**
bodies, lifetime inference analyzes the body directly to determine which
parameters are aliased by the return value (see the Inference section above).
Body-based inference is strictly more precise than elision because it can
examine the actual data flow rather than relying on heuristic rules.

For body-less declarations without explicit lifetime annotations, the following
rules determine the default lifetimes:

1. **Single reference parameter**: If the declaration has exactly one
   reference-typed parameter and returns a reference type, the output lifetime
   is assumed to match the input
2. **No reference return**: If the declaration returns a non-reference type
   (number, string, boolean, void), no lifetimes are needed regardless of
   parameters
3. **Method receiver**: For methods, if the return type needs a lifetime, it
   defaults to the receiver's lifetime

Note that rule 3 applies to methods (which have a receiver) but not to
constructors. Constructors fall back to rule 1 if they have a single reference
parameter, or require explicit annotation if they have multiple reference
parameters.

These rules match the common cases. When a body-less declaration has multiple
reference parameters and returns a reference, explicit annotation is required
to disambiguate.

### Effect on Callers

When a function with lifetime annotations is called, the caller's alias tracking
is updated based on the signature:

```esc
fn identity<'a>(p: mut 'a Point) -> mut 'a Point { return p }

val p: mut Point = {x: 0, y: 0}
val r: mut Point = identity(p)  // r is in p's alias set (same lifetime 'a)
val q: Point = p                // ERROR: r is a live mutable alias of p
r.x = 5
print(q.x)
```

When a function has no lifetime connecting a parameter to its return:

```esc
fn clone(p: Point) -> mut Point { return {x: p.x, y: p.y} }

val p: mut Point = {x: 0, y: 0}
val r: mut Point = clone(p)  // r is NOT in p's alias set
val q: Point = p             // OK: r is independent of p
r.x = 5
print(q.x)                   // prints 0
```

### Lifetimes on Generic Type Parameters

When a generic container holds references to other values, the lifetime may
appear on the type parameter rather than (or in addition to) the container
itself. This distinguishes "the container aliases the source" from "the
elements within the container alias the source."

```esc
// The returned array is fresh, but its elements alias the input array's elements.
fn filter<'a, T>(self: 'a Array<T>, f: fn(T) -> boolean) -> Array<'a T>
```

Here `'a` on `T` (not on `Array`) means: the array container is independent,
but each element `T` may reference data from the input. This allows the caller
to mutate the returned array (e.g. reorder, push, pop) without conflicting with
the original, while still tracking that the elements themselves are shared.

When `'a` appears on the container itself, the entire container aliases the
source:

```esc
fn identity<'a>(items: mut 'a Array<number>) -> mut 'a Array<number> {
    return items
}
// The returned array IS the input array — full alias.
```

The general rule: a lifetime on a type parameter `'a T` means "the value of
type `T` may reference data with lifetime `'a`." This composes with generic
containers — `Array<'a T>` means the array is fresh but its elements carry
lifetime `'a`, while `'a Array<T>` means the array itself carries lifetime `'a`.

## Developer Impact

Most Escalier code will not require developers to think about lifetimes at all.
The system is designed so that lifetimes are inferred, elided, and hidden in the
common case. This section summarizes what developers will actually experience.

### When Lifetimes Are Invisible

The following patterns — which cover the majority of application code — require
**no lifetime annotations** and produce **no lifetime-related errors**:

- **Any function with a body**: The compiler infers lifetimes directly from the
  function body by analyzing which parameters are returned or stored. This
  covers all functions, methods, and constructors defined in Escalier source
  code — no annotations needed regardless of the number of parameters.
- **Returning primitives or void**: Functions that return `number`, `string`,
  `boolean`, or `void` never need lifetimes, regardless of their parameters.
- **Creating fresh values**: Functions that construct and return new objects or
  arrays have no aliasing relationship — no lifetimes needed.
- **Linear data flow**: Code that creates a value, uses it, and then passes it
  along (without keeping a reference) will never trigger a liveness error.
- **Passing mutable values to read-only parameters**: Calling a function that
  takes an immutable parameter with a mutable value is always safe for the
  duration of the synchronous call.

### When Developers See Lifetime Errors

Developers encounter lifetimes through **error messages**, not annotations. The
compiler reports an error when code attempts a mutability transition while
conflicting references are still live. In these cases, the error message shows
the relevant lifetime information and suggests a fix.

Common situations that trigger lifetime errors:

- **Using a mutable reference after handing it to an immutable binding**:
  ```esc
  val items: mut Array<number> = [1, 2, 3]
  val snapshot: Array<number> = items
  items.push(4)  // ERROR: items is still used after the immutable binding
  ```
  **Fix**: Move the immutable binding after the last mutable use.

- **Aliasing through a function call without realizing it**:
  ```esc
  val p: mut Point = {x: 0, y: 0}
  val r: mut Point = getRef(p)  // r aliases p
  val q: Point = p              // ERROR: r is a live mutable alias
  ```
  **Fix**: Ensure `r` is dead before the immutable binding, or use a function
  that returns a fresh value (like `clone`) instead.

These errors require **restructuring code** (reordering statements, narrowing
variable scope, or cloning), not writing lifetime annotations.

### When Developers Write Lifetime Annotations

Explicit lifetime annotations are needed only in a small set of situations.
Most application developers will rarely or never encounter them — they are
primarily relevant to library authors and developers defining interfaces.

| Situation | Who encounters it | Frequency |
|-----------|-------------------|-----------|
| Interface/trait method declarations (no body to infer from) | Library/API authors | Occasional |
| External function declarations without available source | Library consumers using FFI | Rare |
| Mutual recursion where fixed-point inference doesn't converge | Any developer | Very rare |

Note that higher-order functions, methods, and constructors with bodies do
**not** require explicit annotations — the compiler infers lifetimes from the
body even when multiple reference parameters are involved. Explicit annotations
are only needed for body-less declarations where the compiler has no code to
analyze.

In all of these cases, the compiler reports an error explaining that it cannot
infer the lifetime relationship and asks for an explicit annotation. The
developer does not need to proactively decide when annotations are needed.

### Comparison With the Current `mut?` System

| Aspect | Current (`mut?`) | Proposed (lifetimes) |
|--------|------------------|----------------------|
| What developers see in types | `mut?` appears in inferred types, hover info, error messages | `mut` or immutable — never `mut?`, lifetimes hidden by default |
| What developers write | Nothing (but must understand `mut?` to read types) | Nothing for functions with bodies (always inferred); rare explicit `'a` annotations for body-less declarations (interfaces, external packages) |
| Mental model required | Three-way distinction: `mut`, `mut?`, immutable | Two-way distinction: `mut` or immutable |
| New errors developers may encounter | None | Liveness errors when aliasing conflicts with mutability transitions |
| New syntax developers may write | None | `'a` lifetime parameters (only on body-less declarations like interface methods) |

### Summary

For **application developers**, the primary impact is:
- Types become simpler (no more `mut?`)
- New liveness errors may surface when code creates conflicting mutable/immutable
  aliases — these are real bugs that the current system silently permits
- No lifetime annotations to write; lifetimes only appear in error messages

For **library authors**, the additional impact is:
- Interface method declarations may need explicit lifetime annotations when the
  method returns a reference that could alias a parameter
- Override files may be needed for TypeScript imports where the heuristic rules
  produce incorrect lifetime assignments

## Displaying Lifetimes

### Default: Hidden

In normal type display (hover info, type signatures, documentation), lifetimes
are **hidden** to avoid visual noise. Most developers do not need to think about
lifetimes for everyday code:

```esc
fn identity(p: mut Point) -> mut Point
fn map<T, U>(items: Array<T>, f: fn(T) -> U) -> Array<U>
```

### In Error Messages: Shown When Relevant

When a lifetime violation occurs, the error message shows the relevant lifetime
annotations so the developer can understand why the compiler believes an alias
exists:

```text
error: cannot assign mutable value to immutable variable
  --> src/main.esc:4:20
   |
 1 | fn identity<'a>(p: mut 'a Point) -> mut 'a Point { return p }
   |                         -- return value aliases parameter `p`
 2 | val p: mut Point = {x: 0, y: 0}
   |     - mutable reference created here
 3 | val r: mut Point = identity(p)
   |     - aliases `p` through `identity` (lifetime 'a)
 4 | val q: Point = p
   |              ^^^^^ immutable binding here
 5 | r.x = 5
   |     ^^^ mutable alias `r` is still used here
   |
   = help: ensure all mutable aliases of `p` are dead before the immutable binding
```

### Verbose Mode

`PrintType` supports a `showLifetimes` option for diagnostic/debugging purposes.
When enabled, all inferred lifetimes are printed in type signatures.

## Lifetimes and Unification

Escalier's type checker uses subtyping-based unification: `Unify(t1, t2)`
succeeds when `t1` is a subtype of `t2` (or they are the same type).
Lifetimes introduce a new dimension to this process. This section specifies
how lifetime-annotated types interact with the existing unification rules.

### Lifetime Variables

Lifetime variables (e.g. `'a`, `'b`) behave similarly to type variables during
unification. They are introduced by generic function signatures and resolved
at call sites.

- A **free lifetime variable** can be bound to a concrete lifetime during
  unification, just as a free type variable can be bound to a concrete type.
- Once bound, a lifetime variable must be used consistently — all occurrences
  of `'a` within a single call must resolve to the same lifetime.
- Two distinct lifetime variables (`'a` and `'b`) may independently bind to
  the same or different lifetimes.

```esc
fn identity<'a>(p: mut 'a Point) -> mut 'a Point { return p }

val p: mut Point = {x: 0, y: 0}
val r = identity(p)
// At the call site, 'a is bound to the lifetime of p.
// The return type mut 'a Point unifies with mut Point, linking r to p's alias set.
```

### Unifying Types With and Without Lifetimes

A lifetime annotation on a type (e.g. `'a Point`) represents an aliasing
relationship — it does not change the shape of the type. Unification treats
the lifetime as metadata that is propagated but does not affect structural
compatibility:

- **`'a T` vs `T`**: Unification succeeds. The lifetime is propagated to the
  result so that alias tracking is preserved. This is the common case when a
  lifetime-annotated return value is assigned to a variable with no explicit
  lifetime — the variable inherits the lifetime.
- **`T` vs `'a T`**: Unification succeeds. A type without a lifetime is
  compatible with one that has a lifetime. This occurs when a fresh value is
  passed where a lifetime-annotated type is expected — the fresh value
  trivially satisfies the aliasing constraint.
- **`'a T` vs `'b T`**: Unification succeeds if `'a` and `'b` can be unified
  (see Lifetime Constraint Propagation below). This occurs when two
  lifetime-annotated values flow into the same type variable or are compared
  for compatibility.

### Mutability and Lifetimes

The existing rule that `mut T1` vs `mut T2` requires **invariant** unification
(exact type match) extends to lifetimes:

- **`mut 'a T` vs `mut 'a T`**: Unification succeeds — same mutability, same
  lifetime, same underlying type.
- **`mut 'a T` vs `mut 'b T`**: Unification succeeds only if `'a` and `'b`
  unify. Two mutable references to the same type but with different lifetimes
  can only be unified if their lifetimes are compatible. This ensures that
  alias sets are not incorrectly merged.
- **`mut 'a T` vs `T`** (mutable to immutable): Unification succeeds — this
  is the existing covariant rule for dropping mutability. The lifetime `'a` is
  propagated to the result for alias tracking. However, the **liveness check**
  (not unification) is responsible for verifying that the transition is safe.
- **`T` vs `mut 'a T`** (immutable to mutable): Unification succeeds
  structurally, but the liveness check must verify the transition is safe.

The key principle: **unification determines structural compatibility; liveness
analysis determines whether a mutability transition is safe at a given program
point.** Lifetimes flow through unification so that liveness analysis has the
information it needs.

### Lifetime Constraint Propagation

When two lifetime variables are unified, this creates a **lifetime constraint**
rather than immediately resolving to a single lifetime. Lifetime constraints
track which lifetimes must be related:

- **Binding**: When `'a` is free and unified with a concrete lifetime (e.g.
  the lifetime of a specific variable), `'a` is bound to that lifetime.
- **Equating**: When `'a` is unified with `'b` (both free), they are linked
  so that binding one also binds the other — the same mechanism used for type
  variable unification.
- **Conflict**: If `'a` is already bound to one lifetime and is then unified
  with a different, incompatible lifetime, unification reports an error. This
  catches cases where a function signature claims two parameters share a
  lifetime but the caller passes values with incompatible lifetimes.

```esc
fn swap<'a>(a: mut 'a Point, b: mut 'a Point) -> void { ... }

val p: mut Point = {x: 0, y: 0}
val q: mut Point = {x: 1, y: 1}
swap(p, q)  // ERROR: 'a cannot bind to both p's and q's lifetime
            // p and q are independent values with distinct lifetimes
```

The shared `'a` in `swap`'s signature claims both parameters alias the same
underlying value. Since `p` and `q` are independent, unification detects a
conflict — `'a` is bound to `p`'s lifetime from the first parameter, then
the second parameter tries to bind `'a` to `q`'s (different) lifetime.

A correct `swap` uses separate lifetimes for each parameter:

```esc
fn swap<'a, 'b>(a: mut 'a Point, b: mut 'b Point) -> void { ... }

val p: mut Point = {x: 0, y: 0}
val q: mut Point = {x: 1, y: 1}
swap(p, q)  // OK: 'a binds to p's lifetime, 'b binds to q's lifetime
```

If the caller *does* pass two aliases of the same value, the shared-lifetime
version is valid:

```esc
fn swap<'a>(a: mut 'a Point, b: mut 'a Point) -> void { ... }

val p: mut Point = {x: 0, y: 0}
val r: mut Point = p          // r aliases p — same lifetime
swap(p, r)                     // OK: 'a binds to p's lifetime for both
```

### Function Types

When unifying function types, lifetime parameters follow the same variance
rules as type parameters:

- **Parameter types** are contravariant: if `fn('a T) -> U` is unified with
  `fn('b T) -> U`, the lifetime on the parameter follows contravariant rules.
  In practice, a function that accepts a longer-lived reference can be used
  where a shorter-lived one is expected.
- **Return types** are covariant: if `fn(T) -> 'a U` is unified with
  `fn(T) -> 'b U`, the lifetime on the return type follows covariant rules.
  A function returning a shorter-lived reference is a subtype of one returning
  a longer-lived reference.

When unifying a function type that has lifetime parameters with one that does
not, the lifetimes are inferred at the call site:

```esc
fn apply(f: fn(mut Point) -> mut Point, p: mut Point) -> mut Point {
    return f(p)
}

// Calling with identity, which has lifetime 'a:
// fn identity<'a>(p: mut 'a Point) -> mut 'a Point
val result = apply(identity, myPoint)
// The unifier binds identity's signature to the expected fn type,
// threading 'a through so result is linked to myPoint's alias set.
```

### Constructed Types With Lifetimes

When a class constructor produces a type with lifetime parameters (e.g.
`Container<'a>`), unification handles the lifetime arguments on the
constructed type:

- **`Container<'a>` vs `Container`**: Unification succeeds. The lifetime
  is propagated — the variable receiving the value inherits the alias
  relationship.
- **`Container<'a>` vs `Container<'b>`**: Unification succeeds if `'a` and
  `'b` can be unified. This occurs when two containers holding references
  with different lifetimes are passed where the same type is expected.
- **`mut Container<'a>` vs `mut Container<'b>`**: Invariant in both the
  type and the lifetime — both must unify.

```esc
class Container(item: mut Point) { item, }

val p: mut Point = {x: 0, y: 0}
val c: Container = Container(p)  // c has type Container<'a> where 'a = lifetime of p
// Unification of Container<'a> with Container succeeds; 'a is propagated.
```

### Lifetime-Annotated Type Parameters

When lifetimes appear on type parameters within generic types (e.g.
`Array<'a T>`), unification recurses into the type arguments:

- **`Array<'a T>` vs `Array<T>`**: Unification succeeds. The lifetime on the
  element type is propagated to the result.
- **`Array<'a T>` vs `Array<'b T>`**: Succeeds if `'a` and `'b` unify.
- **`mut Array<'a T>` vs `mut Array<'b T>`**: Since mutable types are
  invariant, the inner types must match exactly — including lifetimes. `'a`
  and `'b` must unify.

This is how element-level aliasing is tracked through generic containers:

```esc
fn filter<'a, T>(self: 'a Array<T>, f: fn(T) -> boolean) -> Array<'a T>

val items: mut Array<mut Point> = [Point(0, 0), Point(1, 1)]
val filtered = filter(items, fn(p) { p.x > 0 })
// filtered has type Array<'a mut Point> where 'a = lifetime of items.
// The elements in filtered alias elements in items.
```

### Interaction With Type Variable Binding

When a type variable is bound during unification and the target type carries
a lifetime, the lifetime is preserved in the binding:

```esc
fn first<'a, T>(items: 'a Array<T>) -> 'a T | undefined { ... }

val items: Array<mut Point> = [Point(0, 0)]
val p = first(items)
// T is bound to mut Point, 'a is bound to the lifetime of items.
// p has type 'a mut Point | undefined — it may alias an element of items.
```

If the type variable is bound from a context without a lifetime (e.g. a fresh
value), no lifetime is attached, and no alias tracking is needed.

### What Unification Does Not Check

Unification is responsible for **structural compatibility** and **lifetime
propagation**. It does **not** check:

- Whether a mutability transition is safe at a given program point (this is
  the liveness analysis pass)
- Whether a variable is dead before an alias is created (this is alias
  tracking)
- Whether an escaping reference makes a value permanently aliased (this is
  escaping reference detection)

These checks run after or alongside unification. Unification's role is to
ensure lifetimes flow correctly through the type system so that these
downstream analyses have accurate information.

## Function Signatures

### Parameter Types

Function parameters declare whether they need mutable or immutable access:

```esc
fn sort<T>(items: mut Array<T>) -> void { ... }
fn sum(items: Array<number>) -> number { ... }
```

### Inference

When a function's parameter type is inferred from usage:

- If the function body writes to a parameter's properties, the parameter is
  inferred as `mut`
- If the function body only reads, the parameter is inferred as immutable
- No `mut?` is needed — the inference is binary

Lifetime annotations on parameters and return types are also inferred from the
function body (see Lifetime Annotations above).

### Calling Convention

When passing a mutable value to a function expecting an immutable parameter,
the immutable parameter type prevents the function from mutating the value.
If the function also does not store the reference (i.e. no escaping reference
is detected), the call is safe for its duration:

```esc
val items: mut Array<number> = [1, 2, 3]
val total = sum(items)  // OK: sum takes immutable ref and doesn't store it
items.push(4)           // OK: sum has returned, no conflicting refs
```

However, an immutable parameter type alone does not prevent the function from
**storing** the reference into an external location. If the function body stores
the parameter (e.g. into a module-level variable), the escaping-reference
detection assigns a `'static` lifetime to that parameter:

```esc
var globalCache: Array<number> = []

fn cacheItems(items: Array<number>) -> number {
    globalCache = items  // stores parameter — escaping reference detected
    return items.length
}
// Inferred signature: fn cacheItems(items: 'static Array<number>) -> number
```

The `'static` lifetime means callers holding a `mut Array` cannot resume
mutable use after the call, because the value is now permanently aliased
through `globalCache`:

```esc
val items: mut Array<number> = [1, 2, 3]
val n = cacheItems(items)  // ERROR: cacheItems captures the reference ('static)
items.push(4)              // would violate globalCache's immutability guarantee
```

When passing a mutable value to a function that returns an aliased reference,
the caller must account for the new alias in its liveness analysis.

## TypeScript Interop: Automatic Lifetime Assignment

Escalier imports TypeScript type declarations from external modules (e.g.
`node_modules/@types/*`). Since TypeScript has no concept of lifetimes or
mutability tracking, the compiler must automatically assign lifetime annotations
to imported function signatures using a set of conservative heuristic rules.

### Guiding Principle

The rules should be **sound by default** — never assume independence when
aliasing is possible — while avoiding being so conservative that common
TypeScript APIs become unusable. When in doubt, assume aliasing.

### Mutability of Parameters

TypeScript's `readonly` modifier provides a signal for mutability:

- `readonly` properties/`ReadonlyArray<T>` → immutable in Escalier
- Regular properties/`Array<T>` → `mut` in Escalier

```typescript
// TypeScript declaration
declare function sort<T>(items: T[]): T[];
declare function sum(items: readonly number[]): number;
```

```esc
// Escalier sees these as:
fn sort<'a, T>(items: mut 'a Array<T>) -> mut 'a Array<T>
fn sum(items: Array<number>) -> number
```

### Rules for Lifetime Assignment

#### Rule A: Primitive/void return → no lifetime

If the return type is a primitive (`number`, `string`, `boolean`, `void`,
`undefined`, `null`), no lifetime is needed. Primitives cannot alias.

```typescript
declare function indexOf<T>(items: T[], value: T): number;
// → fn indexOf<T>(items: Array<T>, value: T) -> number
//   (no lifetimes needed)
```

#### Rule B: Return type matches a parameter type → assume aliasing

If the return type is a reference type (object, array, function) and it matches
or is a subtype of a parameter's type, assume the return value aliases that
parameter. This is the conservative default.

```typescript
declare function first<T>(items: T[]): T | undefined;
// → fn first<'a, T>(items: 'a Array<T>) -> 'a T | undefined
//   (returned element may be a reference into the array)

declare function identity<T>(value: T): T;
// → fn identity<'a, T>(value: 'a T) -> 'a T
```

#### Rule C: Return type differs from all parameters → no lifetime

If the return type is a reference type that does not match any parameter type,
assume no aliasing.

```typescript
declare function keys(obj: object): string[];
// → fn keys(obj: {}) -> mut Array<string>
//   (returns a fresh array, not an alias of obj)
```

#### Rule D: Methods returning `this` → alias with receiver

TypeScript methods that return `this` (for chaining) alias the receiver. Many
array methods follow this pattern:

```typescript
// Array.prototype methods returning this
declare function sort<T>(this: T[]): T[];
declare function reverse<T>(this: T[]): T[];
declare function fill<T>(this: T[], value: T): T[];
```

```esc
// Escalier sees these as methods aliasing the receiver:
fn sort<'self, T>(self: mut 'self Array<T>) -> mut 'self Array<T>
fn reverse<'self, T>(self: mut 'self Array<T>) -> mut 'self Array<T>
```

#### Rule E: Methods returning a new collection → no container-level lifetime on return

Some methods are known to produce fresh containers. When the return type is an
array but the method is a known non-aliasing method (e.g. `map`, `filter`,
`slice`, `concat`, `flat`, `flatMap`), the return **container** gets no lifetime
linking it to the receiver. However, the **elements** within the container may
still alias the original array's elements — in that case, the lifetime appears
on the element type `T`, not on the array itself.

```typescript
declare function map<T, U>(this: T[], f: (item: T) => U): U[];
declare function filter<T>(this: T[], f: (item: T) => boolean): T[];
declare function slice<T>(this: T[], start?: number, end?: number): T[];
```

```esc
// Escalier sees these as returning fresh arrays:
fn map<T, U>(self: Array<T>, f: fn(T) -> U) -> mut Array<U>
fn filter<'a, T>(self: 'a Array<T>, f: fn(T) -> boolean) -> Array<'a T>
fn slice<'a, T>(self: 'a Array<T>, start?: number, end?: number) -> Array<'a T>
```

Note: `filter` and `slice` return new arrays, but the **elements** may still
alias elements in the original array. The lifetime on `T` (not the array)
captures this: the array container is fresh, but the items within it may
reference the original data.

#### Rule F: Callback parameters → determine mutability from the callback's parameter type

If a callback parameter receives a value derived from another parameter, the
mutability of the callback's parameter determines how the value is accessed:

- If the callback's parameter is `readonly` or a primitive → immutable
- Otherwise → assume mutable (conservative)

**Readonly callback parameter → immutable:**

```typescript
declare function forEach<T>(items: readonly T[], f: (item: Readonly<T>) => void): void;
// → fn forEach<T>(items: Array<T>, f: fn(T) -> void) -> void
//   (callback receives readonly T — immutable access to elements)
```

**Primitive callback parameter → immutable:**

```typescript
declare function indexBy(items: string[], f: (item: string) => number): Map<number, string>;
// → fn indexBy(items: Array<string>, f: fn(string) -> number) -> mut Map<number, string>
//   (callback receives string, which is a primitive — immutable access)
```

**Non-readonly, non-primitive callback parameter → assume mutable (conservative):**

```typescript
declare function transform<T>(items: T[], f: (item: T) => T): T[];
// → fn transform<'a, T>(items: 'a Array<T>, f: fn(mut 'a T) -> mut 'a T) -> mut Array<'a T>
//   (callback parameter T is not readonly — assume mutable access;
//    callback may mutate the item and return it, so the return aliases the input)
```

This is conservative: the callback might not actually mutate its argument, but
since the TypeScript declaration does not use `readonly`, Escalier assumes the
worst case. An override can relax this if the callback is known to be read-only.

**Accumulator pattern:**

```typescript
declare function reduce<T, U>(items: T[], f: (acc: U, item: T) => U, init: U): U;
// → fn reduce<'a, T, U>(items: Array<T>, f: fn('a U, T) -> 'a U, init: 'a U) -> 'a U
//   (accumulator flows through and is returned — lifetime tracks that the
//    return value may alias init)
```

### Override Mechanism

Since heuristic rules cannot be correct for every TypeScript API, Escalier
should support manual overrides for imported functions. This could take the
form of a declaration file (e.g. `.esc.d.ts` or a config section) where
developers can provide explicit lifetime annotations for specific imports.

**Buffer.slice** — returns a view into the same memory, not a copy (unlike
`Array.slice`):

```esc
declare fn Buffer.slice<'a>(self: mut 'a Buffer, start?: number, end?: number) -> mut 'a Buffer
```

**Array higher-order methods** — the heuristic rules may not correctly capture
that callbacks receive references to elements within the array. These overrides
make the aliasing relationships explicit:

```esc
// Callback receives immutable references to elements. Returns void, so no
// aliasing through the return value. A mut Array can be passed here safely
// since the parameter is immutable — the array cannot be mutated during
// iteration.
declare fn Array.forEach<'a, T>(self: 'a Array<T>, f: fn('a T) -> void) -> void

// Callback receives immutable references to elements. Returns a fresh array
// with independently-typed elements (no lifetime link to the original).
declare fn Array.map<'a, T, U>(self: 'a Array<T>, f: fn('a T) -> U) -> mut Array<U>

// Callback receives immutable references to elements. Returns a fresh array
// whose elements alias the original array's elements.
declare fn Array.filter<'a, T>(self: 'a Array<T>, f: fn('a T) -> boolean) -> Array<'a T>

// Callback receives immutable references to elements. Returns a boolean,
// so no aliasing through the return value.
declare fn Array.every<'a, T>(self: 'a Array<T>, f: fn('a T) -> boolean) -> boolean
declare fn Array.some<'a, T>(self: 'a Array<T>, f: fn('a T) -> boolean) -> boolean

// Callback receives element and accumulator. The accumulator flows through
// and is returned.
declare fn Array.reduce<'a, 'b, T, U>(self: 'a Array<T>, f: fn('b U, 'a T) -> 'b U, init: 'b U) -> 'b U

// find returns an element from the array, so the result aliases the array's
// elements.
declare fn Array.find<'a, T>(self: 'a Array<T>, f: fn('a T) -> boolean) -> 'a T | undefined

// findIndex returns a primitive, so no aliasing.
declare fn Array.findIndex<'a, T>(self: 'a Array<T>, f: fn('a T) -> boolean) -> number

// flatMap: callback returns arrays that are flattened into a fresh result.
// The elements in the returned arrays are independently typed.
declare fn Array.flatMap<'a, T, U>(self: 'a Array<T>, f: fn('a T) -> Array<U>) -> mut Array<U>

// sort and reverse mutate in place and return `this` (alias of receiver).
declare fn Array.sort<'a, T>(self: mut 'a Array<T>, cmp?: fn(T, T) -> number) -> mut 'a Array<T>
declare fn Array.reverse<'a, T>(self: mut 'a Array<T>) -> mut 'a Array<T>
```

Since all the iteration methods take an immutable `Array<T>`, a `mut Array`
can be passed safely — the mutable-to-immutable transition is valid for the
duration of the synchronous call. The callback only gets immutable access to
elements, preventing mutation during iteration.

These overrides ensure that:
- Callbacks receive immutable element references, preventing mutation during
  iteration
- Methods that return fresh collections (map, filter, flatMap) don't create
  false aliases to the original array
- Methods that return elements (find) or `this` (sort, reverse) correctly
  propagate lifetimes

This allows correcting cases where the heuristic rules are wrong without
modifying the upstream TypeScript declarations.

#### Map, WeakMap, Set, and WeakSet Overrides

```esc
// Map — get returns an element alias, set returns the map (receiver alias)
declare fn Map.get<'a, K, V>(self: 'a Map<K, V>, key: K) -> 'a V | undefined
declare fn Map.set<'self, K, V>(self: mut 'self Map<K, V>, key: K, value: V) -> mut 'self Map<K, V>
declare fn Map.has<K, V>(self: Map<K, V>, key: K) -> boolean
declare fn Map.delete<K, V>(self: mut Map<K, V>, key: K) -> boolean
declare fn Map.forEach<'a, K, V>(self: 'a Map<K, V>, f: fn('a V, K) -> void) -> void

// WeakMap — keys must be objects, get returns an element alias
declare fn WeakMap.get<'a, K, V>(self: 'a WeakMap<K, V>, key: K) -> 'a V | undefined
declare fn WeakMap.set<'self, K, V>(self: mut 'self WeakMap<K, V>, key: K, value: V) -> mut 'self WeakMap<K, V>
declare fn WeakMap.has<K, V>(self: WeakMap<K, V>, key: K) -> boolean
declare fn WeakMap.delete<K, V>(self: mut WeakMap<K, V>, key: K) -> boolean

// Set — add returns the set (receiver alias)
declare fn Set.has<T>(self: Set<T>, value: T) -> boolean
declare fn Set.add<'self, T>(self: mut 'self Set<T>, value: T) -> mut 'self Set<T>
declare fn Set.delete<T>(self: mut Set<T>, value: T) -> boolean
declare fn Set.forEach<'a, T>(self: 'a Set<T>, f: fn('a T) -> void) -> void

// WeakSet — keys must be objects
declare fn WeakSet.has<T>(self: WeakSet<T>, value: T) -> boolean
declare fn WeakSet.add<'self, T>(self: mut 'self WeakSet<T>, value: T) -> mut 'self WeakSet<T>
declare fn WeakSet.delete<T>(self: mut WeakSet<T>, value: T) -> boolean
```

These overrides ensure that:
- `get` on Map/WeakMap returns element aliases so callers track the
  aliasing relationship with the container
- `set`/`add` methods return `this` (receiver alias) for chaining
- `has`, `delete` return primitives — no aliasing
- `forEach` callbacks receive immutable element references

## What This Replaces

| Current (`mut?`)                        | Proposed (lifetimes)                     |
|-----------------------------------------|------------------------------------------|
| `MutabilityUncertain` type wrapper      | Removed entirely                         |
| `unwrapMutability()` stripping          | Not needed                               |
| `finalizeOpenObject()` mut? resolution  | Replaced by direct write-tracking        |
| Three mutability states in types        | Two states: `mut` or immutable           |
| `mut?` in printed types                 | Never appears                            |
| No cross-function alias tracking        | Lifetime annotations (mostly inferred)   |

## Async/Await and Generators

Escalier supports `async` functions, `await` expressions, generator functions
(via `yield`), and `for await...in` loops. These features introduce
**suspension points** — places where a function pauses and resumes later.
This section describes how lifetimes interact with these features.

### Why No Special Rules Are Needed for Local Variables

Suspension points (`await`, `yield`) do not require special lifetime rules
for local variables because:

1. **Local variables are not accessible during suspension.** When an async
   function awaits or a generator yields, the function's local variables
   are captured in the continuation/generator state. No outside code can
   access them — the caller only receives the yielded/resolved value, not
   a handle to the function's locals.

2. **Single-threaded execution model.** Escalier targets JavaScript's
   event loop, where only one task runs at a time. Even if a local mutable
   reference is live across an `await`, no concurrent code can observe or
   mutate it during the suspension.

3. **Intraprocedural analysis is sufficient.** Liveness analysis already
   tracks variables across `await` and `yield` — a variable used after a
   suspension point is live before it. No additional suspension-aware
   analysis is required.

The dangerous case — where a mutable reference **escapes** to code that
runs during suspension — is already handled by the existing lifetime
machinery:

- **Escaping to module-level state:** If a local mutable value is stored
  into a module-level variable, escaping reference detection assigns a
  `'static` lifetime, blocking subsequent mutability transitions.
- **Escaping through a nested function:** If a nested function captures a
  mutable variable and returns it, the return type gets a regular lifetime
  linking it to the captured variable. The enclosing function's alias
  tracker treats the returned value as an alias of the captured variable,
  and standard liveness rules apply. No `'static` is needed — the
  relationship is tracked through the function's lifetime annotation.
- **Closure passed to another function:** If a closure capturing a mutable
  variable is passed to a function that stores it (escaping reference),
  `'static` propagates through the closure capture to the original
  variable. If the function only calls the closure synchronously and does
  not store it, no escape occurs.

### Generators and `yield` as an Alias Source

`yield` expressions are alias sources, analogous to `return` statements.
When a generator yields a value, that value flows to the caller via
`Iterator.next()`. If the yielded expression aliases a parameter, the
lifetime must link that parameter to the generator's yield type `T` in
`Generator<T, TReturn, TNext>`.

```esc
fn items<'a>(arr: 'a Array<number>) -> Generator<'a number, void, never> {
    for val item in arr {
        yield item    // yielded value aliases arr's elements
    }
}
```

The inferred lifetime `'a` links `arr` to `T` (`number` in this case),
telling callers that values produced by the generator may alias the input
array's elements.

For `yield from`, the delegated iterator's element type is propagated:

```esc
fn allItems<'a>(arrs: 'a Array<Array<number>>) -> Generator<'a number, void, never> {
    for val arr in arrs {
        yield from items(arr)  // delegates to items(), propagates lifetime
    }
}
```

### Mutable Values Across Suspension Points

A mutable value that is live across a suspension point follows the same
rules as any other mutable value. No additional restrictions are needed
because local variables are inaccessible during suspension:

```esc
async fn example() {
    val items: mut Array<number> = [1, 2, 3]
    items.push(4)
    await someAsync()    // items is still mut, no one else can touch it
    items.push(5)        // OK: items is local, no aliasing conflict
    val snapshot: Array<number> = items  // OK: items is dead after this
    print(snapshot.length)
}
```

### Yielding Mutable References

When a generator yields a mutable reference, the caller and generator may
both hold mutable references to the same value. This is allowed by Rule 3
(multiple mutable references). However, a mutability transition by either
side requires the other's alias to be dead:

```esc
fn makePairs() -> Generator<mut Point, void, never> {
    val p: mut Point = {x: 0, y: 0}
    yield p          // caller receives a mutable ref
    // p is still live and mutable here — OK (Rule 3)
    p.x = 5          // OK: both sides have mutable access
    val frozen: Point = p  // OK only if the yielded ref is dead
                           // (i.e. the caller has consumed and dropped it)
}
```

In practice, the compiler must conservatively assume that a yielded
reference may be held by the caller until the generator is dropped or
completes. This means a mutability transition on a yielded value is only
safe after the generator's last `yield` of that value and when no other
aliases are live.

### Escaping References Through Module-Level State

The one case where suspension creates a real aliasing hazard is when a
mutable reference is accessible through module-level (or otherwise shared)
state:

```esc
var shared: mut Array<number> = [1, 2, 3]

async fn example() {
    val snapshot: Array<number> = shared  // freeze
    await someAsync()  // other code could mutate `shared` during suspension
    print(snapshot.length)  // could see stale data
}
```

This case is already handled: `shared` is a module-level variable, not a
local. It would be assigned a negative VarID and excluded from
intraprocedural liveness analysis. The assignment `val snapshot = shared`
involves a non-local variable, so the checker treats `shared` as having
an unbounded lifetime — effectively `'static`. The freeze is rejected
because `shared` is permanently live and mutable.

### Summary

| Feature | Lifetime Impact |
|---------|----------------|
| `await` in async functions | None for locals — single-threaded, locals inaccessible during suspension |
| `yield` in generators | Treated as alias source (like `return`) — links parameter lifetimes to generator's `T` type |
| `yield from` | Propagates delegated iterator's element lifetime |
| `for await...in` | No special handling — element binding follows standard alias rules |
| Module-level state across suspension | Already handled by `'static` / non-local exclusion |

## Scope and Limitations

### In Scope

- Intraprocedural liveness analysis for local variables
- Alias tracking through variables, object properties, closures, destructuring,
  method receivers, and function return values
- Alias set merging on property assignment (preserves transitive connections)
- Recursive and cyclic data structure support (build-and-freeze pattern)
- Conditional aliasing (variables assigned from multiple branches)
- Reassignment semantics (leaving old alias sets)
- Lifetime annotations on function signatures (inferred when possible)
- Lifetimes on generic type parameters (element-level vs container-level)
- Lifetime elision rules for common patterns
- Escaping reference detection (storing parameters into external locations)
- Lifetime variable unification and constraint propagation
- Lifetime propagation through type variable binding
- Mutable-to-immutable and immutable-to-mutable transitions
- Integration with existing `mut` type annotations
- Configurable lifetime display (hidden by default, shown in errors)
- Automatic lifetime assignment for imported TypeScript declarations
- Override mechanism for correcting heuristic lifetime assignments
- Clear error messages for lifetime violations
- Generator `yield` treated as alias source for lifetime inference
- Async/await and generators require no special lifetime rules for locals

### Out of Scope (Future Work)

- **`break` and `continue` in loops:** The CFG builder does not currently
  model `break` or `continue` statements. Adding them requires tracking
  the enclosing loop's header and post-loop blocks on the builder so that
  `break` edges to the post-loop block and `continue` edges to the header.
  See #487.
- **Anonymous closure capture tracking:** Capture analysis only runs for
  named closures (`val f = fn() { ... }`) because the closure needs a
  VarID to participate in alias sets. Anonymous closures passed as call
  arguments (e.g. `items.map(fn(x) { captured })`) are not tracked,
  which means mutable captures in anonymous callbacks can go undetected.
  Implementation approach: (1) pre-allocate VarIDs for anonymous
  FuncExpr args during the rename pass, (2) detect FuncExpr arguments
  in `inferCallExpr` and call `trackCapturedAliases` with the synthetic
  VarID, (3) treat anonymous closure captures as live for the remainder
  of the enclosing function (conservative but correct — the callee may
  store the closure). `AnalyzeCaptures` already works on any FuncExpr.
  Estimated scope: ~50-100 lines of production code.
- Concurrency / data race prevention (would require Rust-style exclusive
  borrowing)
- Heap escape analysis beyond function return values
- **Property-level alias sets:** The initial implementation uses
  variable-level alias sets — all destructured bindings from the same
  object share one alias set, which can produce false positives when
  freezing one property while mutating another through a different binding.
  Property-level tracking (keying alias set members by `(VarID, path)`
  instead of just `VarID`) would eliminate these false positives by
  recognizing that disjoint property paths don't conflict. This is
  analogous to Rust's MIR "place" system. The workaround for the
  variable-level model is to extract properties into separate variables
  before freezing, which gives each its own alias set.

## Error Messages

Lifetime violations should produce clear, actionable errors that show lifetime
information only when it helps the developer understand the problem.

**Simple case (no cross-function aliasing):**

```text
error: cannot assign mutable value to immutable variable
  --> src/main.esc:5:20
   |
 3 | val items: mut Array<number> = [1, 2, 3]
   |     ----- mutable reference created here
 4 | val snapshot: Array<number> = items
   |              ^^^^^^^^^^^^^^ immutable binding here
 5 | items.push(4)
   |     ^^^^^^^^^ mutable reference `items` is still used here
   |
   = help: move the immutable binding after the last use of `items`
```

**Cross-function aliasing case:**

```text
error: cannot assign mutable value to immutable variable
  --> src/main.esc:4:16
   |
 2 | val p: mut Point = {x: 0, y: 0}
   |     - mutable reference created here
 3 | val r: mut Point = identity(p)
   |     - aliases `p` (identity returns its argument)
 4 | val q: Point = p
   |              ^^^^^ immutable binding here
 5 | r.x = 5
   |     ^^^ mutable alias `r` is still used here
   |
   = help: ensure all mutable aliases of `p` are dead before the immutable binding
```

## Implementation Considerations

### Phase Ordering

Liveness analysis should run as a pass after type inference but before or during
type checking. The checker already walks the AST — liveness information can be
computed in a preceding pass and consulted during assignment checking.

### Data Structures

- **Liveness sets**: For each program point, track which variables are live
- **Alias sets**: Track which variables reference the same underlying value,
  including aliases created through property assignments and function calls.
  Each alias set tracks mutability per member (`map[VarID]Mutability`), so
  no separate mutability map is needed.
- **Lifetime map**: For each function, the inferred or declared lifetime
  relationships between parameters and return type

### Incremental Approach

1. Define data structures for lifetimes, liveness, and alias tracking
2. Name resolution pre-pass: assign unique VarIDs to all local variable
   bindings and use sites via alpha-conversion (renaming), validating that
   all uses refer to in-scope bindings
3. Liveness analysis for straight-line code (no branching)
4. Extend liveness to control flow (if/else, match, loops, early returns,
   throws)
5. Alias tracking for local variables (direct assignment, reassignment)
6. Mutability transition checking (enforce Rules 1, 2, 3 using liveness
   and alias sets)
7. Extend alias tracking to object properties, closures, destructuring,
   conditional aliasing, and method receivers
8. Lifetime annotations: parser support, inference from function bodies,
   escaping reference detection, constructor lifetimes
9. Lifetime unification (binding, equating, conflict detection, `'static`
   propagation, higher-order function lifetime threading)
10. Lifetime elision rules for body-less declarations
11. TypeScript interop: automatic lifetime assignment for imported
    declarations
12. Error messages for lifetime violations
13. Remove `mut?` from the type system
14. Update `PrintType` with `showLifetimes` option
