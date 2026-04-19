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

## Rules

### Rule 1: Mutable-to-Immutable Transition

A mutable value can be assigned to a variable with an immutable type when **no
live mutable references** to that value exist after the assignment point. This
includes aliases created through other variables, object properties, or function
return values.

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

### Rule 2: Immutable-to-Mutable Transition

An immutable value can be assigned to a variable with a mutable type when **no
live immutable references** to that value exist after the assignment point.

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
print(config.host)  // would see mutated value if mutableConfig.host was changed
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
val q: Point = p             // ERROR: r is a live mutable alias of p
r.x = 5
print(q.x)
```

### Object Properties

When a mutable value is stored into an object property, the containing object
becomes an alias. The value is considered to have a live mutable reference for
as long as the containing object is live.

```esc
val p: mut Point = {x: 0, y: 0}
val obj: mut {point: mut Point} = {point: p}  // obj is an alias of p
val q: Point = p             // ERROR: obj is live and provides mutable access
obj.point.x = 5
print(q.x)
```

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
```

This is the conservative choice — the compiler cannot know which branch was
taken, so it must assume the worst case.

### Method Receivers (`self`)

When a method stores a parameter into `self`, the receiver becomes an alias
of that parameter. This is tracked the same way as object property assignments:

```esc
type Container {
    val item: mut Point

    fn setItem(mut self, p: mut Point) -> void {
        self.item = p  // self is now an alias of p
    }
}

val p: mut Point = {x: 0, y: 0}
val c: mut Container = Container { item: {x: 1, y: 1} }
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

### Function Calls

Aliasing through function calls is tracked via **lifetime annotations** on
function signatures. See the Lifetime Annotations section below.

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
```

### Early Returns

An early `return` terminates the current path, which affects liveness. A variable
that is only used after an early return on one branch may be dead on the branch
that returns:

```esc
val items: mut Array<number> = [1, 2, 3]
if condition {
    return items  // early return — `items` is dead on this path after this point
}
// on this path, `items` is still live
val snapshot: Array<number> = items  // OK: on the non-returning path, no conflict
print(snapshot.length)
```

Liveness is computed per-path. A variable is live at a point if it is used on
**any** path from that point forward (consistent with the Control Flow rule).
Early returns reduce the set of paths on which a variable may be live, which
can enable transitions that would otherwise be rejected.

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

When a type implements an interface, its method signatures must be compatible
with the declared lifetimes. The compiler verifies that the implementation does
not violate the aliasing contract:

```esc
type Mirror: Transform {
    fn apply<'a>(self, p: mut 'a Point) -> mut 'a Point {
        return p  // OK: returns the parameter, consistent with 'a
    }
}

type Cloner: Transform {
    fn apply<'a>(self, p: mut 'a Point) -> mut 'a Point {
        return {x: p.x, y: p.y}  // OK: returns a fresh value — this is more
                                  // conservative than needed, but safe
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

To reduce annotation burden, common patterns do not require explicit lifetimes:

1. **Single reference parameter**: If a function has exactly one reference-typed
   parameter and returns a reference type, the output lifetime is assumed to
   match the input
2. **No reference return**: If a function returns a non-reference type (number,
   string, boolean, void), no lifetimes are needed regardless of parameters
3. **Method receiver**: For methods, if the return type needs a lifetime, it
   defaults to the receiver's lifetime

These rules match the common cases. When a function has multiple reference
parameters and returns a reference, explicit annotation is required to
disambiguate.

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

- **Returning primitives or void**: Functions that return `number`, `string`,
  `boolean`, or `void` never need lifetimes, regardless of their parameters.
- **Single reference parameter**: If a function takes one reference-typed
  parameter and returns a reference, the lifetime is inferred automatically
  (elision rule 1).
- **Methods returning derived values**: A method that returns data derived from
  `self` gets the receiver's lifetime automatically (elision rule 3).
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
| Interface/trait method declarations | Library/API authors | Occasional |
| Functions without available source (external packages without overrides) | Library consumers using FFI | Rare |
| Higher-order functions where aliasing depends on a callback | Library authors writing generic combinators | Rare |
| Mutual recursion where fixed-point inference doesn't converge | Any developer | Very rare |

In all of these cases, the compiler reports an error explaining that it cannot
infer the lifetime relationship and asks for an explicit annotation. The
developer does not need to proactively decide when annotations are needed.

### Comparison With the Current `mut?` System

| Aspect | Current (`mut?`) | Proposed (lifetimes) |
|--------|------------------|----------------------|
| What developers see in types | `mut?` appears in inferred types, hover info, error messages | `mut` or immutable — never `mut?`, lifetimes hidden by default |
| What developers write | Nothing (but must understand `mut?` to read types) | Nothing in most code; rare explicit `'a` annotations for library authors |
| Mental model required | Three-way distinction: `mut`, `mut?`, immutable | Two-way distinction: `mut` or immutable |
| New errors developers may encounter | None | Liveness errors when aliasing conflicts with mutability transitions |
| New syntax developers may write | None | `'a` lifetime parameters (rare, library-author scenarios only) |

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

#### Rule F: Callback parameters → assume immutable access unless the callback type indicates mutation

If a callback parameter receives a value derived from another parameter:

- If the callback's parameter is `readonly` or a primitive → immutable
- Otherwise → assume mutable (conservative)

```typescript
declare function forEach<T>(items: T[], f: (item: T) => void): void;
// → fn forEach<T>(items: Array<T>, f: fn(T) -> void) -> void
//   (T could be a reference type, but callback returns void — no aliasing)

declare function reduce<T, U>(items: T[], f: (acc: U, item: T) => U, init: U): U;
// → fn reduce<'a, T, U>(items: Array<T>, f: fn('a U, T) -> 'a U, init: 'a U) -> 'a U
//   (accumulator flows through and is returned)
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

## What This Replaces

| Current (`mut?`)                        | Proposed (lifetimes)                     |
|-----------------------------------------|------------------------------------------|
| `MutabilityUncertain` type wrapper      | Removed entirely                         |
| `unwrapMutability()` stripping          | Not needed                               |
| `finalizeOpenObject()` mut? resolution  | Replaced by direct write-tracking        |
| Three mutability states in types        | Two states: `mut` or immutable           |
| `mut?` in printed types                 | Never appears                            |
| No cross-function alias tracking        | Lifetime annotations (mostly inferred)   |

## Scope and Limitations

### In Scope

- Intraprocedural liveness analysis for local variables
- Alias tracking through variables, object properties, closures, destructuring,
  method receivers, and function return values
- Conditional aliasing (variables assigned from multiple branches)
- Reassignment semantics (leaving old alias sets)
- Lifetime annotations on function signatures (inferred when possible)
- Lifetimes on generic type parameters (element-level vs container-level)
- Lifetime elision rules for common patterns
- Escaping reference detection (storing parameters into external locations)
- Mutable-to-immutable and immutable-to-mutable transitions
- Integration with existing `mut` type annotations
- Configurable lifetime display (hidden by default, shown in errors)
- Automatic lifetime assignment for imported TypeScript declarations
- Override mechanism for correcting heuristic lifetime assignments
- Clear error messages for lifetime violations

### Out of Scope (Future Work)

- Concurrency / data race prevention (would require Rust-style exclusive
  borrowing)
- Heap escape analysis beyond function return values

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
  including aliases created through property assignments and function calls
- **Mutability map**: For each variable, whether its type is `mut` or immutable
- **Lifetime map**: For each function, the inferred or declared lifetime
  relationships between parameters and return type

### Incremental Approach

1. Implement liveness analysis for straight-line code (no branching)
2. Extend to if/else, match expressions, and early returns
3. Extend to loops
4. Implement alias tracking for local variables and object properties
5. Extend alias tracking for closures, destructuring, and method receivers
6. Implement reassignment (leaving old alias sets) and conditional aliasing
7. Implement lifetime inference for function signatures
8. Add lifetimes on generic type parameters (element-level vs container-level)
9. Add lifetime elision rules
10. Add explicit lifetime annotation syntax for cases where inference fails
11. Implement escaping reference detection
12. Remove `mut?` from the type system
13. Update `PrintType` with `showLifetimes` option
14. Update error messages to show lifetimes when relevant
