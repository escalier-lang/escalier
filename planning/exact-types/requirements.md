# Exact Types

## 1. Overview

TypeScript uses structural sub-typing, which means a value conforms to a type as
long as it has at least the properties (or elements, or parameters) that the
type requires. Extra properties on objects, extra elements in tuples, and being
called with fewer arguments than declared are all permitted.

Sometimes it is useful to know that a value has *exactly* a certain shape — no
extra properties on an object, no extra elements in a tuple, no extra arguments
passed to a function. **Exact types** are the umbrella term for these. Exact
types still permit ordinary structural variation on the per-property,
per-element, or per-parameter basis (a property can still be a subtype, a
parameter can still be contravariant, etc.), but the **arity must match
exactly**.

This document specifies exact and inexact variants for four categories of
types:

1. Object types
2. Tuple types
3. Function types
4. Union types

By default, all four categories are **exact**. The trailing `...` syntax marks
a type as **inexact**, meaning extra properties / elements / arguments /
members are permitted.

## 2. Object Types

### 2.1. Syntax

Following [`docs/07_exact_types.md`](../../docs/07_exact_types.md):

```
type ExactPoint = {x: number, y: number}
type InexactPoint = {x: number, y: number, ...}
```

- A bare object type literal `{ ... }` (without trailing `...`) is **exact**.
- An object type with a trailing `...` is **inexact**.

### 2.2. Semantics

```
declare val p: ExactPoint
declare val q: InexactPoint

val a: InexactPoint = p // Okay — exact is a subtype of inexact
val b: ExactPoint = q   // Error — q may have extra properties
```

#### 2.2.1. Object Literal Inference

Object literals are inferred as **exact** types:

```
val p = {x: 1, y: 2}                    // inferred as exact {x: number, y: number}
val q: InexactPoint = {x: 1, y: 2}      // okay — exact widens to inexact on assignment
val r: InexactPoint = {x: 1, y: 2, z: 3} // okay — assigning to inexact target
val s: ExactPoint = {x: 1, y: 2, z: 3}   // Error — extra property `z` on exact target
```

#### 2.2.2. Spread and Rest

Spreading and resting interact with exactness as described in the original doc:

```
val {x, ...rest} = p // `rest` will be exact (an exact {y: number})
val {x, ...rest} = q // `rest` will be inexact

val cp = {color, ...p} // `cp` will be exact
val cq = {color, ...q} // `cq` will be inexact
```

In other words, exactness propagates through spread/rest: combining only exact
inputs yields an exact result; combining anything inexact yields an inexact
result.

#### 2.2.3. `Object.keys`, `Object.values`, and `Object.entries`

Because exact object types have a known, complete set of keys, all three of
these built-ins can be given precise types when called on an exact object:

- `Object.keys(o)` for an exact `o` returns an exact tuple of literal-string
  keys (e.g., `["x", "y"]: ["x", "y"]` for `{x: number, y: number}`). Its
  element-type union is `keyof typeof o`, so iterating the tuple yields a
  binding of type `keyof typeof o`.
- `Object.values(o)` for an exact `o` returns an exact tuple of the
  corresponding value types (e.g., `[number, number]` for the same object).
- `Object.entries(o)` for an exact `o` returns an exact tuple of
  `[key, value]` pairs (e.g., `[["x", number], ["y", number]]`).

For an inexact `o`, all three fall back to TypeScript's permissive types:
`Object.keys(o): Array<string>`, `Object.values(o): Array<unknown>`, and
`Object.entries(o): Array<[string, unknown]>` — the unknown extra properties
prevent any tighter typing.

### 2.3. Type-Checking Rules

Let `E = {p1: T1, ..., pn: Tn}` (exact) and `I = {p1: T1, ..., pn: Tn, ...}`
(inexact). For object types `A` and `B`:

- **Exact <: Inexact:** An exact type is a subtype of the corresponding inexact
  type with the same declared properties (assuming each `Ti` is compatible).
- **Inexact </: Exact:** An inexact type is *not* a subtype of an exact type,
  because the inexact value may carry extra properties.
- **Exact <: Exact:** `A <: B` only if both have the *same* set of property
  names (no missing, no extra) and each property of `A` is a subtype of the
  matching property of `B`.
- **Inexact <: Inexact:** Standard structural subtyping — `A` must contain at
  least every property of `B`, with each property of `A` being a subtype of
  the matching property of `B`.

Optional properties (`p?: T`) interact with exactness as expected: an exact type
permits the *absence* of an optional property, but does not permit any
*additional* properties beyond those declared.

### 2.4. Interfaces

Types declared with the `interface` keyword are **always inexact** object
types. This matches TypeScript's behavior and supports the common use cases
that motivated interfaces in the first place:

- **Interface merging.** Multiple `interface` declarations with the same name
  are merged into a single interface containing the union of all declared
  members. Because the set of merged declarations is not closed at any single
  declaration site, an interface cannot meaningfully be exact.
- **Open extension.** Interfaces are designed to be extended by downstream
  code (including across module boundaries and via declaration merging in
  consumer code), so by construction they admit unknown additional
  properties.
- **Class instances.** Class instance types are interface-like and inexact
  by default for the same reason — subclasses may add properties. (See the
  "Class Instances" subsection below for how to opt into exactness via
  `final` classes.)

```
interface Point {
    x: number,
    y: number,
}

interface Point {        // merged with the previous declaration
    z: number,
}

// Point is now (inexact) { x: number, y: number, z: number, ... }
```

Because interfaces are inexact, `keyof SomeInterface` is an inexact union, and
the same exactness propagation rules apply as for any other inexact object
type. To get an exact object type, use a `type` alias with an object type
literal instead of an `interface`.

### 2.5. Namespaces

Namespaces are **always inexact**, for essentially the same reasons as
interfaces:

- **Declaration merging.** Multiple `namespace` declarations with the same
  name merge into a single namespace containing the union of their members.
  As with interfaces, the set of merged declarations is not closed at any
  single declaration site.
- **Open extension across modules.** Namespaces can be augmented across
  module boundaries (especially in TypeScript interop scenarios), so by
  construction they admit unknown additional members.

Consequently, `keyof SomeNamespace` is an inexact union, and the
exactness-propagation rules apply just as they do for interface types. There
is no opt-in to making a namespace type exact; if you need a closed,
exact-keyed grouping of values, use a `type` alias to an exact object type
instead.

### 2.6. Class Instances

Class instance types are **inexact by default**: a parameter typed as
`Animal` should accept any subclass instance (`Dog`, `Cat`, etc.), which is
exactly the open-extension behavior interfaces have. The instance type for
a class `C` therefore behaves as `{ ...declared members of C, ... }` for
subtyping purposes, and `keyof C` is an inexact union.

To tighten this, a class can be declared **`final`**, which forbids subclassing
and therefore makes its instance type exact.

#### 2.6.1. `final` classes

A class declared `final` cannot be extended. Because no subclass can ever
exist, the instance type is closed: it has exactly the declared members and
nothing more. The instance type of a `final` class is therefore **exact**,
and `keyof` of a final class instance is an exact union.

```
final class Point {
    x: number
    y: number
}

// Point instances are exact: { x: number, y: number }
// keyof Point is the exact union "x" | "y"

class BadPoint extends Point { ... }   // Error — Point is final
```

A primary motivating use case: **enum variants**. In Escalier, enum variants
are classes under the hood, and a value of an enum type is an instance of
one of its variant classes. Marking variant classes as `final` (either
implicitly, by virtue of being declared as enum variants, or explicitly via
the keyword) means the union of variant instance types is itself an exact
union — which lets exhaustive `match` over an enum work without a default
arm even when the match discriminates on the variant's runtime class.

```
enum Shape {
    Circle(radius: number),       // implicitly final
    Square(side: number),         // implicitly final
}

// Shape ≈ exact (Circle | Square), where each variant is an exact instance type
```

#### 2.6.2. Per-use exactness for non-final classes

There is **no** per-type-use annotation for narrowing a non-final class
instance type to "direct instances only." If a class author wants to
guarantee instance-type exactness to consumers, they declare the class
`final`. Otherwise, the class's openness is part of its public contract,
and consumers must accept any subclass instance.

This keeps the type system simple — one less type former, one rule fewer in
subtyping — and forces the open-vs-closed decision to be made once, at the
class declaration site, rather than re-litigated at every use site. If we
later discover a concrete need for consumer-side narrowing, it can be added
in a backward-compatible way.

### 2.7. Mapped Elements

An object type may contain a **mapped element** (`MappedElem`) — a member
that iterates an `IndexParam` over a constraint type and produces a property
per value of the parameter:

```
type Pick<T, K : keyof T> = {[P]: T[P] for P in K}
type Record<K : string, V> = {[P]: V for P in K}
```

The exactness of an object type containing a `MappedElem` is determined by
the **constraint on the `IndexParam`** (i.e. the type after `in`):

- If the constraint is an **exact union** (e.g. an exact union of string
  literals like `"x" | "y"`, or `keyof E` where `E` is an exact object
  type), the set of generated properties is fully known. The object type
  containing the mapped element is **exact**, provided nothing else in the
  object type forces inexactness (no trailing `...`, no inexact members
  combined alongside, no spread of an inexact type).
- If the constraint is an **inexact union** (e.g. `string`, `keyof I`
  where `I` is an inexact object type or interface, or any `T | U | ...`),
  the set of generated properties is open-ended. The object type
  containing the mapped element is **inexact**.

```
type Exact   = {x: number, y: number}
type Inexact = {x: number, y: number, ...}

type EM = {[K]: string for K in keyof Exact}    // exact: keyof Exact is exact "x" | "y"
// EM is exact { x: string, y: string }

type IM = {[K]: string for K in keyof Inexact}  // inexact: keyof Inexact is inexact "x" | "y" | ...
// IM is inexact { x: string, y: string, ... }

type Open = {[K]: number for K in string}       // inexact: string is an inexact union of all strings
// Open is inexact, and is exactly the desugaring of the TypeScript-style
// index signature `{ [k: string]: number }` — see the note below.
```

#### 2.7.1. Index signatures as sugar for mapped elements

> **Note.** This subsection resolves what was previously an open
> question about how index signatures interact with exactness.

A TypeScript-style index signature `{[k: K]: V}` is **surface syntax for
the mapped element** `{[P]: V for P in K}` — the index parameter is
renamed and its constraint is lifted into the `for ... in ...` clause.
The two spellings produce the same type:

```
{[k: string]: number}    ≡    {[K]: number for K in string}
{[k: keyof E]: number}   ≡    {[K]: number for K in keyof E}
```

Consequently, the exactness of an object type containing an index
signature is determined by the constraint on its key, by exactly the
rule above:

- `{[k: string]: number}` — constraint `string` is an inexact union, so
  the object type is **inexact**.
- `{[k: keyof E]: number}` for an exact `E` — constraint is an exact
  union, so the object type is **exact** (assuming nothing else in the
  surrounding object forces inexactness).

This rule composes naturally with the other propagation rules: combining a
mapped element whose constraint is exact with a trailing `...` still yields
an inexact object type, and intersecting an exact mapped result with an
inexact object type follows the usual intersection rules above.

## 3. Tuple Types

### 3.1. Syntax

```
type ExactTuple = [string, number]
type InexactTuple = [string, number, ...]
```

- A bare tuple type `[T1, ..., Tn]` is **exact** — it has exactly `n` elements.
- A tuple type ending in `...` is **inexact** — it has *at least* `n` elements,
  with the trailing elements of unknown type.

This is distinct from a tuple with an explicit *typed* rest element, which is
already a TypeScript-style construct:

```
type Variadic     = [string, ...Array<number>]   // exact arity-wise: 1 string then number rest
type InexactTail  = [string, number, ...]        // inexact: extra elements of any type allowed
```

The difference: `[string, ...Array<number>]` is a precise type whose extra
elements are constrained to be `number`. `[string, number, ...]` says "at least
these two elements, and possibly more of any type" — closer in spirit to
`Array<unknown>` for the tail.

### 3.2. Semantics

```
declare val t: ExactTuple    // [string, number]
declare val u: InexactTuple  // [string, number, ...]

val a: InexactTuple = t      // Okay — exact tuple widens to inexact
val b: ExactTuple = u        // Error — `u` may have extra elements
```

#### 3.2.1. Tuple Literal Inference

Tuple literals are inferred as **exact**:

```
val t = ["hello", 42]                // inferred as exact [string, number]
val u: InexactTuple = ["hello", 42, true] // okay — extra element on inexact target
val v: ExactTuple = ["hello", 42, true]   // Error — extra element on exact target
```

#### 3.2.2. Spread and Rest

Tuple spread/rest mirror the object-type rules:

```
val [first, ...rest] = t   // `rest` is exact [number]
val [first, ...rest] = u   // `rest` is inexact [number, ...]

val tt = ["pre", ...t]     // exact [string, string, number]
val uu = ["pre", ...u]     // inexact [string, string, number, ...]
```

#### 3.2.3. `length`

For an exact tuple, `length` is the literal numeric type (`2` for
`[string, number]`). For an inexact tuple, `length` is `number` with a known
lower bound (effectively `number`, with the documented minimum equal to the
number of declared elements).

### 3.3. Type-Checking Rules

For tuple types `A` and `B`:

- **Exact <: Inexact:** `[T1, ..., Tn] <: [T1, ..., Tn, ...]` (when each `Ti`
  is compatible).
- **Inexact </: Exact:** Inexact tuples cannot be assigned to exact tuple types.
- **Exact <: Exact:** `[T1, ..., Tn] <: [U1, ..., Um]` only if `n == m` and
  each `Ti <: Ui`.
- **Inexact <: Inexact:** `[T1, ..., Tn, ...] <: [U1, ..., Um, ...]` only if
  `n >= m` and the first `m` elements are pairwise compatible.

Optional tuple elements (`[T1, T2?]`) interact with exactness analogously to
optional object properties: an exact tuple permits absence of optional trailing
elements but no additional elements beyond the declared ones.

A tuple with a typed rest element and **no trailing `...` sentinel** —
e.g. `[string, ...Array<number>]` — is **exact**: its shape is fully
determined by the declaration, and any extras must satisfy the rest
element type. For subtyping:

- `[string, ...Array<number>] <: [string, ...Array<number>]` (reflexive).
- `[string, number] <: [string, ...Array<number>]` (the rest element
  permits zero `number`s).
- `[string, number, number] <: [string, ...Array<number>]` (any
  non-negative count of trailing `number`s).
- `[string, ...Array<number>] </: [string, number]` (the rest tuple may
  carry more elements than the fixed exact tuple admits).
- `[string, ...Array<number>] <: [string, number, ...]` (the rest tuple
  is at least one element with a string head, satisfying the inexact
  tuple's lower bound).
- `[string, ...Array<number>] <: [string, ...]` (same idea with a
  weaker lower bound: one `string` head plus any number of trailing
  elements, which the unknown-typed inexact tail trivially admits).

## 4. Function Types

### 4.1. Syntax

```
type ExactCallback   = fn(x: number, y: number) -> number
type InexactCallback = fn(x: number, y: number, ...) -> number
```

- A bare function type `fn(...) -> T` is **exact** — callers may not pass extra
  arguments beyond those declared.
- A function type whose parameter list ends with a trailing `...` is
  **inexact** — extra positional arguments are permitted at call sites.

This is distinct from an explicit typed rest parameter:

```
fn sum(...nums: Array<number>) -> number { ... }   // typed rest — extras must be number
fn log(msg: string, ...) -> undefined { ... }      // inexact — extras of any type allowed
```

### 4.2. Semantics

In TypeScript, a function type `(x: number) => void` is assignable to a
parameter expecting `(x: number, y: number) => void` because the implementation
may simply ignore extra arguments. This means *callers* of the latter type can
pass two arguments, and the supplied function will silently discard the second.

In Escalier, with exact function types, **the callee declares how many
arguments it accepts**, and callers may not pass more than that:

```
declare val f: ExactCallback     // fn(x: number, y: number) -> number
declare val g: InexactCallback   // fn(x: number, y: number, ...) -> number

f(1, 2)        // okay
f(1, 2, 3)     // Error — exact function type accepts exactly 2 arguments

g(1, 2)        // okay
g(1, 2, 3)     // okay — inexact function accepts extras
```

#### 4.2.1. Subtyping (Function Compatibility)

Standard function subtyping rules still apply (contravariant parameters,
covariant returns), with an added arity rule:

**Function arities must match exactly when comparing function types,
regardless of exact/inexact.** Neither direction (`Exact <: Inexact` nor
`Inexact <: Exact`) is permitted for function subtyping. Exactness on
function types governs *call-site* checking only — how many arguments a
caller may pass — not subtyping between function types themselves.

- **Exact </: Inexact:** An exact function is *not* a subtype of the
  corresponding inexact function. See "Why exact `</:` inexact for
  functions" below.
- **Inexact </: Exact:** An inexact function is not a subtype of an exact
  function either, because the inexact function "advertises" that callers
  may pass extras, which the exact callsite would forbid.
- **Exact <: Exact:** Standard structural rule with arities matching
  exactly. `fn(a: A, b: B) -> R <: fn(c: C, d: D) -> S` only if both have
  exactly two parameters, `C <: A`, `D <: B`, and `R <: S`.
- **Inexact <: Inexact:** The classic "fewer params is okay" rule applies
  *within* inexact function types: `fn(a: A, ...) -> R <:
  fn(a: A, b: B, ...) -> R`. Both types permit unbounded extra arguments,
  so the supplier just needs to handle at least the parameters the
  consumer's contract requires.

##### 4.2.1.1. Why exact `</:` inexact for functions

Unlike object, tuple, and union types — where exact `<:` inexact is
straightforwardly sound (an exact value just happens to have no extras and
trivially satisfies an inexact contract) — function exactness sits on the
*opposite side* of the call contract from value exactness:

- Object/tuple/union exactness is a property of the **value**: "this value
  has no more than the declared shape." Widening to inexact loses
  information but never grants new permissions.
- Function exactness is a property of what **callers** are allowed to do:
  "callers may not pass extra arguments." Widening an exact function to an
  inexact type *grants callers a permission the underlying function never
  agreed to honor*.

Concretely:

```
declare val f: fn(a: A, b: B) -> R              // exact: rejects extras
val g: fn(a: A, b: B, ...) -> R = f             // would advertise extras are okay
g(1, 2, 3)                                       // type-checks, but f rejects it
```

We considered three resolutions:

1. **Disallow the direction.** Subtyping requires arities to match; users
   who genuinely want "exact function used where extras are silently
   dropped" write a one-line wrapper (`fn(a, b, ...) => f(a, b)`) that
   makes the lossy step explicit at the source.
2. **Allow it, drop extras silently at runtime.** This is the TypeScript
   status quo and is precisely what enables bugs like
   `["1","2","3"].map(parseInt)` returning `[1, NaN, NaN]` — the bug exact
   function types exist to prevent. Allowing this coercion would let any
   exact function silently lose its protection at a widening site,
   defeating the feature's purpose.
3. **Allow it, insert a runtime arity check at the boundary.** Sound, but
   pays a runtime cost in a system that otherwise compiles to zero-cost
   TypeScript. Boundary wrappers also break referential equality, leak
   into stack traces, and interfere with `.length` / `.name` /
   `Function.prototype.bind`. Making the type system's behavior depend on
   codegen is a smell.

**We chose option 1.** The cases that need this coercion are rare and easy
to handle explicitly with a wrapper, and the rule that results — "function
arities must match exactly for subtyping" — is simple and memorable. The
exact/inexact distinction on functions then governs call-site argument
counts only, not function-to-function subtyping, keeping the soundness
story tight.

#### 4.2.2. Optional and Rest Parameters

Optional parameters (`x?: T`) and typed rest parameters (`...xs: Array<T>`)
remain compatible with both exact and inexact function types. An exact function
with optional parameters still has a fixed *maximum* arity; an exact function
with a typed rest parameter accepts any number of trailing arguments, but each
extra must satisfy the rest element type.

A function type with a typed rest parameter — e.g.
`fn(a: A, ...rest: Array<B>) -> R` — consumes *all* trailing arguments,
each of which must be a `B`. There is no JavaScript-level way to have a
typed rest parameter coexist with additional positional parameters after
it, and the same restriction applies in Escalier. Combining a typed rest
parameter with the inexact `...` sentinel
(`fn(a: A, ...rest: Array<B>, ...) -> R`) is therefore **disallowed**:
either the rest type already specifies what the trailing arguments must
be (no need for the sentinel), or the sentinel is used on its own to
admit untyped extras (no typed rest). A function with a typed rest
parameter is always exact in the §4.2.1 sense.

#### 4.2.3. Call-Site Checking

At call sites, the checker enforces:

- For an exact function type, the number of supplied arguments must be `>=` the
  number of required parameters and `<=` the number of declared parameters
  (counting optionals and any typed rest as documented).
- For an inexact function type, the number of supplied arguments must be `>=`
  the number of required parameters; there is no upper bound.

#### 4.2.4. `Parameters<T>` and `infer` on Function Parameter Lists

The standard `Parameters<T>` utility extracts the parameters of a function
type as a tuple:

```
type Parameters<T : fn(...args: any) -> any> =
    if T : fn(...args: infer P) -> any { P } else { never }
```

For this to produce the right tuple exactness, two rules apply:

- **`infer` on a function parameter list captures the function's exactness
  onto the resulting tuple.** When `T` is `fn(a: A, b: B) -> R` (exact),
  `infer P` binds `P` to the exact tuple `[A, B]`. When `T` is
  `fn(a: A, b: B, ...) -> R` (inexact), `infer P` binds `P` to the inexact
  tuple `[A, B, ...]`.
- **The matcher and the constraint must admit both exact and inexact
  function types.** Function-type subtyping requires arities to match in
  both directions (see §4.2.1.1), so a single fixed-exactness pattern
  would cause the other exactness to fall to the `else` branch. The
  pattern `fn(...args: infer P) -> any` and the constraint
  `fn(...args: any) -> any` are therefore treated as exactness-agnostic
  for matching and constraint-checking purposes — they accept either an
  exact or an inexact function type, and the captured `infer P`
  faithfully reflects which one it was.

Consequently:

```
type F = fn(a: string, b: number) -> boolean         // exact
type G = fn(a: string, b: number, ...) -> boolean    // inexact

type PF = Parameters<F>    // exact [string, number]
type PG = Parameters<G>    // inexact [string, number, ...]
```

Note that `Parameters` is a projection, not a subtyping bridge: even
though `F` and `G` are not subtype-related as function types (§4.2.1.1),
their parameter tuples `PF` and `PG` are related via the usual tuple
rule `[string, number] <: [string, number, ...]`. This is intentional —
the asymmetry on function-type subtyping reflects the call-contract
direction, which a tuple of parameter types no longer carries.

#### 4.2.5. `ReturnType<T>` and Other Function Utility Types

The remaining function-shaped utility types are derived analogously, and
each carries the natural exactness:

- **`ReturnType<T>`** is the declared return type of `T`. Its exactness
  is whatever exactness the return type has — `ReturnType<fn(...) -> R>`
  is `R`, with `R`'s exactness preserved.
- **`Awaited<T>`** unwraps `Promise<T>` chains. Exactness of the result
  is the exactness of the resolved type at the bottom of the chain.
- **`ConstructorParameters<T>`** is the `Parameters`-analog for
  constructor signatures and follows the same rules as §4.2.4.
- **`InstanceType<T>`** is the instance type produced by a constructor.
  Its exactness follows §2.6 (exact only if the underlying class is
  `final`).
- **`ThisParameterType<T>`** extracts the type of the explicit `this`
  parameter, with that type's exactness preserved.

The matcher and constraint for each of these utility types are
exactness-agnostic on the function/constructor type they accept, for the
same reason as `Parameters` (§4.2.4).

## 5. Union Types

### 5.1. Motivation

Exactness for unions captures whether the listed members are *all* the
inhabitants of the type, or merely a known subset. This shows up in three
places:

1. **Keys of exact/inexact objects.** For an exact object `{x: number, y:
   number}`, the type of `keyof` is the **exact** union `"x" | "y"`. For an
   inexact object `{x: number, y: number, ...}`, `keyof` is the **inexact**
   union `"x" | "y" | ...` (i.e. at least these, possibly more strings).
2. **Values/element-types of exact/inexact tuples.** For an exact tuple
   `[string, number]`, the union of its element types is the **exact**
   `string | number`. For an inexact tuple `[string, number, ...]`, the union
   of element types is the **inexact** `string | number | ...`.
3. **`throws` clauses on function signatures.** If a function body is fully
   wrapped in `try/catch` with a catch-all that re-throws a known set of
   error types `E1 | E2`, the function's `throws` clause is the **exact**
   union `E1 | E2` — those are *exactly* the errors it can throw. If the
   checker cannot prove the catch is exhaustive (no catch-all, or the body
   contains calls whose throws clauses are themselves inexact), the inferred
   `throws` clause is the **inexact** union `E1 | E2 | ...`.

### 5.2. Syntax

```
type ExactColor   = "red" | "green" | "blue"
type InexactColor = "red" | "green" | "blue" | ...

type ExactError   = IOError | ParseError
type InexactError = IOError | ParseError | ...
```

- A bare union `T1 | T2 | ... | Tn` is **exact**: its inhabitants are exactly
  the values in `T1 ∪ T2 ∪ ... ∪ Tn`, and nothing else.
- A union ending in `| ...` is **inexact**: it contains *at least* the values
  in those members, plus an implicit `unknown`-typed tail standing in for
  zero or more additional union members of unknown type. The tail is always
  `unknown`-typed; it is not constrained by surrounding context (so e.g.
  `"a" | "b" | ...` does *not* satisfy a `: string` constraint, since the
  tail is not proven to be a string — see §7.4.4).

### 5.3. Semantics

```
declare val a: ExactColor
declare val b: InexactColor

val x: InexactColor = a    // okay — exact widens to inexact
val y: ExactColor = b      // Error — `b` may be a value outside the listed members
```

#### 5.3.1. Exhaustiveness Checking

Pattern matching / `switch` on an **exact** union is exhaustive when every
listed member is handled — no default arm is required:

```
fn name(c: ExactColor) -> string {
    match c {
        "red" => "Red",
        "green" => "Green",
        "blue" => "Blue",
    }   // okay — exact union, all members covered
}
```

For an **inexact** union, exhaustiveness requires a default arm because the
runtime value may fall outside the listed members:

```
fn name(c: InexactColor) -> string {
    match c {
        "red" => "Red",
        "green" => "Green",
        "blue" => "Blue",
        _ => "Unknown",      // required — `c` may be something else
    }
}
```

#### 5.3.2. `keyof` and Tuple Element Unions

Exactness flows naturally from objects and tuples to the unions derived from
them:

```
type Exact   = {x: number, y: number}
type Inexact = {x: number, y: number, ...}

type EK = keyof Exact      // exact union "x" | "y"
type IK = keyof Inexact    // inexact union "x" | "y" | ...

type ET = [string, number]
type IT = [string, number, ...]

type EV = ET[number]       // exact union string | number
type IV = IT[number]       // inexact union string | number | ...
```

#### 5.3.3. `throws` Clauses

A function's `throws` clause is a union type whose exactness reflects how
much the checker knows:

- **Exact `throws`:** The body is wholly enclosed in `try/catch` with a
  catch-all clause, every `throw` reachable from the catch-all has a known
  type, and every nested call's `throws` clause is itself exact (or its
  errors are caught locally). The inferred `throws` is the exact union of
  all error types that escape.
- **Inexact `throws`:** Otherwise — there is at least one site that may
  throw an error whose type the checker cannot pin down (e.g., a call to a
  TypeScript-imported function with no `throws` annotation, or an
  uncaught dynamic `throw`). The inferred `throws` is the inexact union of
  all known error types.

```
fn parse(s: string) throws ParseError | IOError { ... }
// Exact: parse only ever throws one of these two error types.

fn loadConfig() throws ParseError | IOError | ... { ... }
// Inexact: loadConfig is known to be able to throw at least these,
// but may also throw something else (e.g., from an inexact callee).
```

A caller can rely on an exact `throws` clause to drive exhaustive `catch`
matching. With an inexact `throws`, a catch-all is required.

### 5.4. Type-Checking Rules

For union types `A` and `B`:

- **Exact <: Inexact:** An exact union `T1 | ... | Tn` is a subtype of any
  inexact union that lists a (subtype-compatible) superset of those members.
- **Inexact </: Exact:** An inexact union is *not* a subtype of an exact
  union, because the inexact value may be a member outside the listed set.
- **Exact <: Exact:** `A <: B` iff every member of `A` is a subtype of some
  member of `B`. (Standard union subtyping. There is no "arity" requirement
  beyond covering every member — the count of listed members on the two
  sides need not match, only the coverage relation.)
- **Inexact <: Inexact:** `A <: B` iff every *listed* member of `A` is a
  subtype of some listed member of `B`, and both `A` and `B` are inexact.
  The `...` tail on each side is implicitly `unknown`-typed, so the tails
  always match — the only check that matters is that the listed members
  on the supplier side are covered by the listed members on the consumer
  side.

### 5.5. Interop

TypeScript has no notion of exact unions. A TypeScript union like
`"red" | "green" | "blue"` is imported as the **inexact** union
`"red" | "green" | "blue" | ...`. The reason: TypeScript allows widening
into and out of unions via casts, index access on inexact objects, and
assertions, so Escalier cannot prove the tighter "closed set" property
holds across the boundary. Treating the imported union as exact would let
downstream Escalier code rely on exhaustive `match` without a default arm,
and that exhaustiveness would be silently wrong whenever the TypeScript
side admits an out-of-set value at runtime.

This applies to discriminated unions as well. For a TypeScript type like
`{kind: "ok", value: number} | {kind: "err", error: string}`, the outer
union is imported as inexact (so `match` on `kind` requires a default arm),
and each member object type is also inexact per the existing
object-import rule.

When a user knows an imported union is actually closed (e.g. a generated
type, a library they trust, or an enum-like definition), they can opt into
exactness at the use site with the `Exact<T>` utility type:

```
import type { Color } from "some-ts-lib"     // inferred inexact
type StrictColor = Exact<Color>              // opt-in to exact
```

Escalier-emitted `.d.ts` files preserve the original Escalier type via
an `@escalier-type` JSDoc tag alongside each declaration (see §9), so
when one Escalier project consumes another Escalier project's emitted
`.d.ts`, exact unions round-trip correctly. Only plain
TypeScript-authored declarations (without an `@escalier-type` tag) fall
back to the inexact default.

##### Provably-closed primitive unions

A small set of TypeScript types are *provably* closed at the language
level and are imported as **exact** unions:

- `boolean` is imported as the exact union `true | false`.
- `null | undefined` (as a TypeScript type) is imported as exact, since
  TypeScript guarantees no other values inhabit it.

These are the only exceptions to the inexact-by-default rule for
TypeScript-imported unions; any other imported union (including the
nominal `Color`-style alias unions, discriminated unions, etc.) follows
the inexact rule above.

### 5.6. Template Literal Types

A template literal type is a derived union over the strings produced by
substituting each hole with a value from its hole type. Its exactness is the
**conjunction** of the exactnesses of its hole types:

- If every hole is an exact union over a finite set of literal types, the
  resulting set of strings is finite and known, so the template literal
  type is **exact**.
- If any hole is `string`, `number`, an inexact union, or any other type
  with an unknown tail, the resulting set is open, so the template literal
  type is **inexact**.

```
type Side = "left" | "right"                       // exact
type Axis = "x" | "y" | "z"                        // exact
type Pad  = `pad-${Side}`                          // exact: "pad-left" | "pad-right"
type Var  = `--${Axis}`                            // exact: "--x" | "--y" | "--z"

type AnySuffix = `pad-${string}`                   // inexact
type Loose     = `--${"x" | "y" | "z" | ...}`      // inexact (hole is inexact)
```

This composes with the union rules above: an exact template literal type
behaves like any other exact union for `match` exhaustiveness, `keyof`
results, and the `Exact`/`Inexact` utility types.

## 6. Utility Types

Escalier provides two built-in utility types for converting between the exact
and inexact forms of any type that has a notion of exactness (objects,
tuples, functions, unions). They are dual: `Exact<T>` tightens, `Inexact<T>`
loosens.

The function-shaped utility types (`Parameters<T>`, `ReturnType<T>`,
`ConstructorParameters<T>`, `InstanceType<T>`, `ThisParameterType<T>`,
`Awaited<T>`) are documented alongside function-type semantics in §4.2.4
and §4.2.5, since their exactness behavior is bound up with how `infer`
captures function parameter lists.

### 6.1. `Exact<T>`

`Exact<T>` produces the exact form of `T`. Its behavior depends on the kind
of type `T`:

- **Object types:** `Exact<{x: number, y: number, ...}>` is
  `{x: number, y: number}`. The set of declared properties is preserved;
  the trailing `...` is removed.
- **Tuple types:** `Exact<[string, number, ...]>` is `[string, number]`.
  The declared elements are kept; the trailing `...` is removed.
- **Function types:** `Exact<fn(a: A, b: B, ...) -> R>` is
  `fn(a: A, b: B) -> R`. Callers of the result may not pass extra
  arguments.
- **Union types:** `Exact<T1 | T2 | ...>` is `T1 | T2`. The trailing `...`
  is removed; the result is a closed union.
- **Already-exact types:** `Exact<T>` is `T` if `T` is already exact.

For types that **cannot be made exact** at the type level, `Exact<T>` is an
error:

- `Exact<I>` where `I` is an interface — interfaces are always inexact and
  cannot be tightened (see the Interfaces subsection).
- `Exact<C>` where `C` is a non-`final` class instance type — the openness
  is part of the class's contract; declare the class `final` to obtain an
  exact instance type instead.

### 6.2. `Inexact<T>`

`Inexact<T>` produces the inexact form of `T`. Its behavior is the dual of
`Exact<T>`:

- **Object types:** `Inexact<{x: number, y: number}>` is
  `{x: number, y: number, ...}`.
- **Tuple types:** `Inexact<[string, number]>` is `[string, number, ...]`.
- **Function types:** `Inexact<fn(a: A, b: B) -> R>` is
  `fn(a: A, b: B, ...) -> R`. Callers of the result may pass extra
  arguments. Note this is the one direction users *cannot* obtain via
  function-type subtyping (§4.2.1.1) — `Inexact<F>` exists precisely to
  express it explicitly.
- **Union types:** `Inexact<T1 | T2>` is `T1 | T2 | ...`.
- **Already-inexact types:** `Inexact<T>` is `T` if `T` is already
  inexact.

`Inexact<T>` is total — it is defined for every kind of type that has an
exactness notion, since loosening is always permitted.

### 6.3. Examples

```
type StrictPoint = {x: number, y: number}
type LoosePoint  = Inexact<StrictPoint>     // {x: number, y: number, ...}

type LooseHeaders = {"content-type": string, ...}
type StrictHeaders = Exact<LooseHeaders>    // {"content-type": string}

type Pair        = [string, number]
type LoosePair   = Inexact<Pair>            // [string, number, ...]

type LooseColor  = "red" | "green" | "blue" | ...
type StrictColor = Exact<LooseColor>        // "red" | "green" | "blue"

// Round-tripping is the identity:
type T1 = Exact<Inexact<StrictPoint>>       // StrictPoint
type T2 = Inexact<Exact<LoosePoint>>        // LoosePoint
```

### 6.4. Interaction with Mapped Types

`Exact` and `Inexact` distribute through mapped types and most utility
types in the natural way:

```
type T = {x: number, y: number, ...}
type P = Partial<T>                  // {x?: number, y?: number, ...}
type EP = Exact<P>                   // {x?: number, y?: number}
// Note: applying `Exact` here drops the `...` tail — values of type EP
// may omit `x` or `y` (they're optional), but they may not carry any
// other property, even though the original `T` admitted arbitrary
// extras.
```

For container generics, `Exact` and `Inexact` apply only to the top-level
type, not recursively into the element type. To tighten or loosen nested
types, compose explicitly:

```
type T = Array<{x: number, ...}>
type T2 = Array<Exact<{x: number, ...}>>   // Array<{x: number}>
```

A deep variant could be added later (e.g. `DeepExact<T>` / `DeepInexact<T>`)
if a real need emerges; we do not provide one initially.

### 6.5. TypeScript Interop

`Exact<T>` and `Inexact<T>` are Escalier-only constructs. When emitting
`.d.ts` files:

- The result type (after applying `Exact`/`Inexact`) is what's emitted as a
  plain TypeScript type — TypeScript itself has no notion of exactness, so
  the `Exact<...>` / `Inexact<...>` wrapper is erased.
- The original Escalier form (including the `Exact<...>` / `Inexact<...>`
  wrapper) is preserved in the `@escalier-type` JSDoc tag alongside the
  declaration so that other Escalier consumers can recover the precise
  exactness. See §9.

## 7. Type Operators and Exactness Propagation

Most type-level operators produce types whose exactness is *derived* from
their inputs rather than declared independently. This section spells out
the propagation rules for each operator so they don't need to be reasoned
out from first principles each time.

### 7.1. `keyof T`

The exactness of `keyof T` matches the exactness of `T`'s key set:
- `keyof` of an exact object type is an **exact** union of literal-string
  keys.
- `keyof` of an inexact object type, an interface, a namespace, or a
  non-`final` class instance type is an **inexact** union (those types
  admit unknown additional keys).
- `keyof` of a tuple yields the union of its index literals only —
  `Array` prototype keys are *not* included (this differs from
  TypeScript). For an exact tuple `[T0, ..., Tn-1]`, `keyof` is the
  exact union `"0" | ... | "n-1"`. For an inexact tuple
  `[T0, ..., Tn-1, ...]`, `keyof` is the inexact union
  `"0" | ... | "n-1" | ...`, since the unknown trailing positions
  contribute unknown additional index literals.

NOTE: The behaviour of `keyof` in Escalier does not include `Array` prototype
keys like it does in TypeScript.

### 7.2. `typeof v`

`typeof v` produces the inferred type of the expression `v`, with whatever
exactness that inference yields. There is no exactness adjustment performed
by the operator itself.

### 7.3. Indexed access `T[K]`

The exactness of `T[K]` follows from `T`:
- `Exact[K]` where `Exact` is an exact tuple yields a value type whose
  surrounding union (when `K` is a union of indices) is exact.
- `Inexact[K]` where `Inexact` is an inexact tuple yields an inexact
  union, because the unknown tail elements contribute unknown member
  types.
- `T[K]` for object types follows the same pattern: exact object →
  result-of-projection is exact in the same sense; inexact object → the
  projected union admits unknown member contributions.

### 7.4. Conditional types `if T : U { X } else { Y }`

The conditional type operator does not introduce or remove exactness on
its own. Exactness affects conditional types in three ways: (1) which
branch is taken, (2) how the chosen branch's exactness flows out, and
(3) how distribution behaves over union `T`.

#### 7.4.1. Which branch is taken

A conditional asks "is `T` a subtype of `U`?" using the same subtyping
rules as everywhere else. Exactness on `T` and `U` therefore changes
branch selection through the asymmetric rules already documented:

- **Exact `<:` inexact** holds for objects, tuples, and unions, so an
  exact `T` will satisfy `T : U` whenever `U` is the inexact form of the
  same shape:
  ```
  type T = {x: number, y: number}            // exact
  type U = {x: number, y: number, ...}       // inexact

  if T : U { X } else { Y }                  // takes the X branch
  ```
- **Inexact `</:` exact** does *not* hold, so an inexact `T` will not
  satisfy `T : U` when `U` is exact, even if their declared shapes match:
  ```
  type T = {x: number, y: number, ...}       // inexact
  type U = {x: number, y: number}            // exact

  if T : U { X } else { Y }                  // takes the Y branch
  ```
- **Function types are a special case.** Because function-type subtyping
  requires matching exactness in *both* directions (see the function
  subtyping section), a conditional comparing two function types of
  mismatched exactness always takes the false branch — even when the
  parameter and return types align.

#### 7.4.2. How the result's exactness is determined

The exactness of the conditional's result comes from the chosen branch
(`X` or `Y`), not from `T`. There is no "result inherits `T`'s exactness"
rule.

```
type Wrap<T> = if T : object { Box<T> } else { never }
// Result exactness comes from Box<T> or never, not from T's exactness.
```

`T`'s exactness only flows into the result when `X` or `Y` references
`T` (or binds it via `infer`):

```
type Id<T> = if T : unknown { T } else { never }
// If T is exact, the result is exact. If T is inexact, the result is inexact —
// because the X branch literally is T.

type Elem<T> = if T : Array<infer E> { E } else { never }
type A = Elem<Array<{x: number}>>          // E is exact {x: number}
type B = Elem<Array<{x: number, ...}>>     // E is inexact {x: number, ...}
```

##### `infer` over object, tuple, and function patterns

The general rule, of which §4.2.4 is the function-pattern instance:

- An `infer` binding inside an object, tuple, or function-parameter
  pattern captures the exactness of the corresponding part of the
  matched type. `if T : {x: infer X, ...} { X }` against an exact
  `{x: P, y: Q}` binds `X` to `P` (with `P`'s exactness preserved); the
  pattern's own `...` says only what the pattern requires of `T`, not
  what is captured.
- The pattern itself is matched against `T` using the standard
  subtyping rules (§2.3, §3.3, §4.2.1) with one exception: patterns
  built solely to extract via `infer` (e.g. `fn(...args: infer P)`,
  `Array<infer E>`) are treated as exactness-agnostic on the matched
  shape, so a single pattern matches both exact and inexact `T`. This
  matches the rule already given for function patterns in §4.2.4 and
  generalizes it to object/tuple patterns used for inference.

#### 7.4.3. Distribution over unions

When `T` is a union, the conditional distributes over its members and
each distributed result has the exactness of its branch:

```
type Foo<T> = if T : string { "yes" } else { "no" }

type R1 = Foo<"a" | "b">             // exact — distributes to "yes" | "yes" = "yes"
type R2 = Foo<"a" | "b" | ...>       // inexact — distributes over known members,
                                     // tail handled conservatively
```

For an inexact `T`, distribution can only enumerate the listed members.
The unknown tail must be handled conservatively — typically by widening
the result to include both branches, or by leaving an inexact result.

#### 7.4.4. Constraints on `T` simplify the picture

Adding a constraint on `T` at the type-parameter level pre-filters what
arguments are admissible and can collapse the conditional entirely:

```
type Foo<T : string> = if T : string { "yes" } else { "no" }
```

With `T : string` enforced at the call site:

- An **exact** union of string literals like `"a" | "b"` satisfies the
  constraint — all members are subtypes of `string`.
- An **inexact** union like `"a" | "b" | ...` does *not* satisfy the
  constraint, because the `...` tail represents members of unknown type
  which are not proven to be subtypes of `string`. Such a call is
  **rejected at the call site**.

Once inside `Foo<T>`, the conditional check `T : string` is redundant —
the constraint guarantees it holds for every admissible `T`. So
`Foo<T>` reduces to `"yes"` for every legal argument:

| `T` | Admissible under `T : string`? | Result |
|---|---|---|
| `"a" \| "b"` (exact) | yes | `"yes"` |
| `"a" \| "b" \| ...` (inexact, tail unconstrained) | **no — call-site error** | n/a |
| `string` | yes | `"yes"` |
| `number` | no — fails constraint | n/a |

This is generally the right way to write conditional generics over
exactness: a sufficiently strong constraint on `T` removes the "unknown
extras" worry from the body of the conditional and pushes it up to the
call site, where it's enforced as a constraint check rather than handled
defensively inside the conditional.

### 7.5. Mapped types `{[K]: T[K] for K in keyof T}`

Already covered in §2.7 **Mapped Elements**: the exactness of the
resulting object type is determined by the exactness of the constraint
on the `IndexParam` (the type after `in`). Exact constraint → exact
result; inexact constraint → inexact result. (As always, the result can
still be forced inexact by other elements in the surrounding object
type.)

### 7.6. Union types `A | B`

A union preserves exactness per branch:

- Combining two exact union types yields an exact union.
- Combining any inexact union with anything yields an inexact union.
- A heterogeneous union like `Exact1 | Exact2` remains a union of two
  exact types — there is no "merging" that loses per-branch exactness.

### 7.7. Intersection types `A & B`

Intersection inherits exactness from its operands per kind:

- **Object types:** The intersection of two exact object types is exact
  and contains exactly the union of their declared property names.
  Intersecting an exact type with an inexact type produces an exact type
  if the inexact type's declared properties are a subset of the exact
  type's declared properties; otherwise it is an error (the extra
  inexact properties cannot be present in an exact value).
- **Tuple types:** Intersection of two tuple types is well-defined only
  when their declared lengths agree (or when one is inexact and the
  other has at least as many declared elements as it does). The result
  is exact iff both operands are exact, and each element type is the
  intersection of the corresponding element types from each side.
- **Function types:** Intersection of function types models overload —
  `(fn(A) -> R) & (fn(B) -> S)` is callable as either signature.
  Because function-type subtyping requires arities (and exactness) to
  match in both directions, the operands' exactnesses do not need to
  agree; each call site is checked against the matching overload's
  exactness.
- **Union types:** Intersection distributes over unions as usual; the
  result's exactness is the conjunction (exact only if both operands are
  exact and the resulting member set is fully determined).
- **Across kinds** (e.g. primitive `&` object): the result inherits
  exactness from the object operand.

### 7.8. Narrowing

Type guards narrow union members the same way regardless of exactness,
but the *residual* union after narrowing preserves the original
exactness. Narrowing an exact union by removing a known member yields an
exact union; narrowing an inexact union still leaves the unknown tail.

### 7.9. Utility types

Built-in utility types preserve exactness in the natural way: the result
of `Partial<T>`, `Readonly<T>`, `Pick<T, K>`, or `Omit<T, K>` over an
exact type is exact; the result over an inexact type is inexact. (See
the **Utility Types** section for `Exact<T>` / `Inexact<T>` themselves,
which deliberately *change* exactness.)

### 7.10. Type aliases and references

`type Foo = T` and references to `Foo` carry whatever exactness `T` has.
Aliasing is purely a naming layer and does not adjust exactness.

### 7.11. `MutType`

Mutability is orthogonal to exactness. `mut T` carries the exactness of
`T`; `mut` neither tightens nor loosens.

### 7.12. Generic constraints

When a type parameter is constrained by an exact type, only exact
arguments may be substituted. An inexact subtype is *not* admissible at
the call site, even if its declared shape would otherwise satisfy the
constraint, because the inexact tail represents members the constraint
cannot prove are present.

```
fn dump<T : {x: number, y: number}>(t: T) -> string { ... }

dump({x: 1, y: 2})                 // okay — exact argument
declare val q: {x: number, y: number, ...}
dump(q)                            // Error — inexact argument fails exact constraint
```

The dual rule already follows from §2.3: an inexact constraint admits
both exact and inexact arguments, since exact `<:` inexact for objects,
tuples, and unions. (Function-type constraints follow the stricter
arities-must-match-in-both-directions rule of §4.2.1.1.)

In short: the exactness of the *constraint* fixes the exactness of the
admissible arguments — exact constraints reject inexact arguments;
inexact constraints accept either.

### 7.13. Special types

- `never`, `unknown`, `any`, and `void` have no notion of arity and
  therefore no exactness distinction. They behave as the absorbing or
  identity elements documented in the intersection/union sections (e.g.
  `never & T = never`, `unknown & T = T`, `any & T = any`).
- `PrimType`, `LitType`, `UniqueSymbolType`, and `RegexType` are
  single-inhabitant or single-kind primitive types with no arity, and
  therefore no exact/inexact variants.

## 8. Defaults and Migration

- **All newly written object, tuple, function, and union types in Escalier
  source are exact by default.** Inexactness must be explicitly opted into
  with `...`.
- **All types imported from TypeScript** (whether via `.d.ts` files or
  declarations without an `@escalier-type` JSDoc tag) are treated as
  **inexact** for all four categories — including unions. TypeScript has no
  notion of an exact/closed union, and admits widening through casts, index
  access, and assertions, so Escalier cannot prove the closed-set property
  holds across the boundary. This default preserves TypeScript's permissive
  behavior across the package boundary and avoids spurious errors when
  calling into TypeScript libraries. Users who know an imported type is
  actually closed can opt into exactness at the use site with `Exact<T>`.
- **Round-tripping:** When Escalier emits `.d.ts` files for consumption by
  other Escalier projects, it preserves the original Escalier type via an
  `@escalier-type` JSDoc tag on each declaration (see §9). TypeScript
  consumers will see the (inexact) erased form; Escalier consumers will
  recover the precise exactness from the JSDoc payload.

## 9. TypeScript Interop

This section consolidates the interop story spread across §5.5 (union
import), §6.5 (`Exact`/`Inexact` erasure), and the per-category emission
rules. The summary table below shows how each kind of Escalier type
round-trips through `.d.ts`:

| Escalier kind | Emitted TS form | JSDoc annotation | Round-trip back to Escalier |
|---|---|---|---|
| Exact object `{x: T, y: U}` | `{ x: T, y: U }` | `@escalier-type {x: T, y: U}` | exact |
| Inexact object `{x: T, ...}` | `{ x: T }` | `@escalier-type {x: T, ...}` | inexact |
| Exact tuple `[T, U]` | `[T, U]` | `@escalier-type [T, U]` | exact |
| Inexact tuple `[T, U, ...]` | `[T, U, ...Array<unknown>]` | `@escalier-type [T, U, ...]` | inexact |
| Exact function `fn(a: A) -> R` | `(a: A) => R` | `@escalier-type fn(a: A) -> R` | exact (arity enforced) |
| Inexact function `fn(a: A, ...) -> R` | `(a: A, ...rest: Array<unknown>) => R` | `@escalier-type fn(a: A, ...) -> R` | inexact |
| Exact union `A \| B` | `A \| B` | `@escalier-type A \| B` | exact |
| Inexact union `A \| B \| ...` | `A \| B \| unknown` (i.e. `unknown`) or just `A \| B` | `@escalier-type A \| B \| ...` | inexact |
| `Exact<T>` / `Inexact<T>` wrappers | erased to the result type | `@escalier-type Exact<T>` / `@escalier-type Inexact<T>` | wrapper recovered |

A single `@escalier-type` JSDoc tag, whose payload is the original
Escalier type annotation in source form, carries everything needed to
round-trip the declaration. The emitted TypeScript form is a best-effort
erasure for plain TS consumers; the JSDoc payload is the source of truth
for Escalier consumers re-importing the declaration.

**Imports from plain TypeScript** (declarations *without* an
`@escalier-type` annotation) default to the inexact form for every
category, with the `boolean` and `null | undefined` exceptions noted in
§5.5. Users who know an imported type is actually closed can opt in
explicitly with `Exact<T>`.

**Round-tripping between Escalier projects** is driven entirely by the
`@escalier-type` annotations above. Plain TypeScript consumers ignore
the JSDoc payload and see only the erased form (with the inexact
default).

## 10. Examples

### 10.1. Object: Exhaustive Property Iteration

```
type Point = {x: number, y: number}     // exact
val p: Point = {x: 1, y: 2}

// Object.keys(p) is the exact tuple ["x", "y"]: ["x", "y"]
for k in Object.keys(p) {
    // k: keyof Point — i.e. the exact union "x" | "y", since Point is exact
    console.log(k, p[k])
}
```

### 10.2. Tuple: Exact-Arity Pair

```
type Pair = [string, number]            // exact

fn first(p: Pair) -> string {
    return p[0]
}

first(["a", 1])           // okay
first(["a", 1, true])     // Error — extra element on exact tuple
```

### 10.3. Function: Forbidding Extra Callback Arguments

A common JavaScript pitfall: `["1", "2", "3"].map(parseInt)` returns
`[1, NaN, NaN]` because `parseInt` accepts a second `radix` argument and
`Array.prototype.map` passes the index. With exact function types, the type
of `parseInt` would be `fn(s: string, radix?: number) -> number` (exact), and
`Array.prototype.map`'s callback parameter could require an exact unary
function:

```
declare fn map<T, U>(arr: Array<T>, f: fn(item: T) -> U) -> Array<U>

map(["1", "2", "3"], parseInt)
// Error — parseInt's exact arity (1 or 2) doesn't match exact unary `fn(item: T) -> U`
```

### 10.4. Mixed: An Inexact Object as a Bag

```
type Headers = {
    "content-type": string,
    "content-length": number,
    ...                            // explicitly inexact: other headers are allowed
}

val h: Headers = {
    "content-type": "text/plain",
    "content-length": 11,
    "x-custom": "ok",              // okay — inexact permits extras
}
```

## 11. Open Questions

*(None at present — previously open questions about index signatures and
generic constraints are resolved in §2.7.1 and §7.12 respectively.)*
