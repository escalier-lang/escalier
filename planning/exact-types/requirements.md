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
parameter can still be contravariant, etc.), but the **arity is constrained**:
for objects, tuples, and unions, an exact type's member set must match exactly;
for functions, exactness bounds how many arguments the function tolerates when
invoked, which in turn governs callback compatibility (see §4.2.1 — the
"accept-set" model).

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

- A bare function type `fn(...) -> T` is **exact** — it does not tolerate
  being invoked with more arguments than it declares, in any context.
- A function type whose parameter list ends with a trailing `...` is
  **inexact** — it tolerates being invoked with extra positional
  arguments *when used as a callback* (i.e. it may fill a function-typed
  slot that passes more arguments than it names; see §4.2.1).

At a *direct* call site, both forms reject more arguments than the callee
declares (§4.2.3); the exact/inexact distinction governs **callback
subtyping** (§4.2.1), not direct calls.

This is distinct from an explicit typed rest parameter:

```
fn sum(...nums: Array<number>) -> number { ... }   // typed rest — extras must be number
fn log(msg: string, ...) -> undefined { ... }      // inexact — extras of any type allowed
```

### 4.2. Semantics

In TypeScript, a function type `(x: number) => void` is assignable to a
parameter expecting `(x: number, y: number) => void` because the implementation
may simply ignore extra arguments — so a holder of the two-parameter type can
call the supplied function with a second argument it silently discards. This
*callback bivariance* is a well-known source of bugs.

Escalier splits the concern in two:

- **Direct call sites** reject more arguments than the callee declares,
  regardless of exactness — passing extra arguments to a call you can see
  is treated as a likely mistake (§4.2.3).
- **Callback subtyping** — whether a function may be supplied where a
  function-typed slot is expected — is governed by exactness, via the
  accept-set rule (§4.2.1). An *inexact* function explicitly tolerates
  being invoked with extras, so it may fill a slot that passes more
  arguments than it names; an *exact* function may not.

```
declare val f: ExactCallback     // fn(x: number, y: number) -> number
declare val g: InexactCallback   // fn(x: number, y: number, ...) -> number

f(1, 2)        // okay
f(1, 2, 3)     // Error — more arguments than declared
g(1, 2)        // okay
g(1, 2, 3)     // Error — more arguments than declared (direct calls reject
               //         extras regardless of exactness; the `...` matters
               //         for callback subtyping, not direct calls)
```

#### 4.2.1. Subtyping (Function Compatibility)

Standard function subtyping rules still apply — parameter types are
checked contravariantly and return types covariantly — and the arity
relationship is governed by the **accept-set** model below.

**The accept-set.** A function type's exactness defines the set of
argument counts it tolerates when invoked. Let `required` be the number
of required (non-optional) parameters and `n` the number of declared
parameters:

- exact `fn(p1..pn)` → accepts the closed range `[required, n]`
- inexact `fn(p1..pn, ...)` → accepts the open range `[required, ∞)`

`required` is the lower bound — a function always needs at least its
required arguments. Exactness sets the *upper* bound: `n` for exact,
unbounded for inexact. Optional parameters lower `required` without
changing `n`:

```
fn(a, b)       // required=2, n=2  → exact [2,2]   inexact [2,∞)
fn(a, b?)      // required=1, n=2  → exact [1,2]   inexact [1,∞)
fn()           // required=0, n=0  → exact [0,0]   inexact [0,∞)
```

**Subtyping rule.** Read the supertype `F` as a callback slot: whoever
holds an `F` will invoke whatever is supplied with the argument counts
`F` permits. A supplied function `G` is therefore a subtype of `F` only
if `G` tolerates every argument count `F` may invoke it with (with
parameter types contravariant and the return type covariant):

> **`G <: F` iff `accept(G) ⊇ accept(F)`**, i.e. with
> `accept(F) = [rF, uF]` and `accept(G) = [rG, uG]`:
> - `rG <= rF` — `G` must not *demand* more arguments than `F` might supply, **and**
> - `uG >= uF` — `G` must not *refuse* an argument count `F` might supply.

The upper-bound condition is the part exactness governs; the lower-bound
condition is the `required` part. Consequences:

- **Exact <: Exact:** standard structural rule with the accept-set check.
  `fn(a: A, b: B) -> R <: fn(c: C, d: D) -> S` requires `C <: A`,
  `D <: B`, `R <: S`, and `accept ⊇`. Same-arity exact functions relate
  structurally; an exact function with an optional trailing parameter can
  also fill a *narrower* exact slot (e.g. `fn(a: A, b?: B) -> R`,
  accept `[1, 2]`, fills `fn(a: A) -> R`, accept `[1, 1]`) — the slot
  simply never supplies the optional argument.
- **Inexact <: Inexact:** the classic "fewer params is okay" rule —
  `fn(a: A, ...) -> R <: fn(a: A, b: B, ...) -> R`. Both have upper bound
  `∞`, so `uG >= uF` holds; the supplier just needs to handle at least
  the consumer's required parameters.
- **Inexact <: (narrower or wider) exact:** an inexact function fills an
  exact callback slot whenever its required count fits.
  `fn(a: A, ...) -> R <: fn(a: A, b: B) -> R` — the slot invokes with 2
  args; the inexact supplier's accept-set `[1, ∞)` covers `{2}`. A
  zero-param inexact `fn(...) -> R` fills *any* exact slot whose required
  count it meets. **This is the case exactness exists to permit:** a
  function that has explicitly opted into tolerating extras can stand in
  for a higher-arity callback.
- **Exact </: inexact, and exact </: any higher-arity slot:** an exact
  function's finite upper bound `n` cannot cover a slot whose upper bound
  exceeds `n` (an inexact slot's `∞`, or a wider exact slot's larger
  `n`). It would be invoked with arguments it refuses. See "Why exact
  `</:` inexact for functions" below.

**Optional parameters are part of the declared parameter list.** A
function type `fn(a: A, b?: B) -> R` has declared parameter list
`[a: A, b?: B]` with `required=1`, `n=2` — *not* `[a: A]`. The
optionality marker is structural identity of the function type, not a
deletion the type system silently performs; it lowers the accept-set's
lower bound (the function may be called with one argument) without
discarding the second declared parameter. (See §4.2.1.2 for a worked
example.)

##### 4.2.1.1. Why exact `</:` inexact for functions

The accept-set rule is asymmetric: an *inexact* function can fill a
narrower slot (§4.2.1), but an *exact* function can never fill a wider
one — in particular `exact </: inexact`. This subsection explains why
that asymmetry is the sound one.

Function exactness sits on the *opposite side* of the call contract from
value exactness:

- Object/tuple/union exactness is a property of the **value**: "this value
  has no more than the declared shape." Widening to inexact loses
  information but never grants new permissions.
- Function exactness is a property of how the function may be **invoked**:
  an exact function does not tolerate extra arguments. Treating an exact
  function as inexact would *grant its holders a permission the function
  never agreed to honor* — the right to call it with extras.

Concretely:

```
declare val f: fn(a: A, b: B) -> R              // exact: accept-set [2, 2]
val g: fn(a: A, b: B, ...) -> R = f             // inexact slot: accept-set [2, ∞)
g(1, 2, 3)                                       // slot permits 3 args, but f refuses the 3rd
```

The accept-set check rejects this: `accept(f) = [2, 2]` does not contain
`3`, so `f`'s upper bound `2` fails the `uG >= uF` condition against the
slot's `∞`. The same reasoning blocks an exact function from filling a
*wider exact* slot (`fn(a, b) </: fn(a, b, c)`): the slot would invoke it
with 3 arguments it refuses.

**The soundness insight is preserved.** Exact functions reject extra
arguments *everywhere* — both at direct call sites (§4.2.3) and by being
unable to fill any slot that would pass more arguments than they declare.
This is exactly what closes the `parseInt`-in-`map` class of bug: an
exact function can never silently receive arguments its body does not
expect. What the accept-set model *adds* is the dual direction — an
inexact function filling a narrower slot — and that direction is sound
precisely because the trailing `...` is an explicit declaration that the
function tolerates being called with extras.

When a user genuinely wants an exact function to flow into a slot that
passes extras, the conversion must be made explicit — either with the
`Inexact<T>` utility (§6.2) or, preferably, a one-line wrapper
(`fn(a, b, ...) => f(a, b)`) that makes the arity narrowing visible at
the source. We considered instead inserting an implicit runtime arity
check at such boundaries; that is sound but pays a runtime cost in a
system that otherwise compiles to zero-cost TypeScript, and boundary
wrappers break referential equality, leak into stack traces, and
interfere with `.length` / `.name` / `Function.prototype.bind`. Making
the type system's behavior depend on codegen is a smell, so the explicit
opt-in is preferred.

##### 4.2.1.2. A worked example: chained widenings in TypeScript

The principled argument above is best illustrated by a concrete program
that is sound in Escalier but silently miscompiles in TypeScript. The
following TypeScript code type-checks and runs without errors, yet
exhibits a runtime type confusion:

```ts
function foo(cb: (a: string) => void) {
    bar(cb);
}

function bar(cb: (a: string, b: number) => void) {
    cb("hello", 5);
}

function cb(a: string, b?: boolean) { /* uses b as a boolean */ }

foo(cb);
// At runtime, the original `cb` is invoked with ("hello", 5).
// `b` is bound to the number `5`, but the body trusts `b: boolean`.
```

The unsoundness chain has three steps:

1. `foo(cb)` — TS allows `(a: string, b?: boolean) => void` to flow into
   `(a: string) => void` because TS treats the trailing optional as
   droppable.
2. `bar(cb)` (inside `foo`) — TS allows `(a: string) => void` to flow
   into `(a: string, b: number) => void` via the classic "fewer-args is
   fine" bivariance.
3. `bar` calls `cb("hello", 5)`. The original `cb`'s `b: boolean`
   parameter is bound to the number `5`. No runtime error; downstream
   computations on `b` produce silent garbage.

The literal Escalier translation rejects this at **step 2**:

```
fn foo(cb: fn(a: string) -> undefined) {
    bar(cb)
}
fn bar(cb: fn(a: string, b: number) -> undefined) {
    cb("hello", 5)
}
fn cb(a: string, b?: boolean) -> undefined { ... }

foo(cb)
```

- **Step 1 — `foo(cb)` is accepted, and soundly so.** `cb` is exact with
  `required=1`, `n=2`, so `accept(cb) = [1, 2]`; `foo`'s slot
  `fn(a: string)` has `accept = [1, 1]`. The check `[1, 2] ⊇ [1, 1]`
  holds (`rG=1 <= rF=1`, `uG=2 >= uF=1`), and the only position the unary
  slot ever supplies is `a: string`, which matches. This is correct: `foo`
  will only ever call `cb` with one argument, leaving the optional `b`
  unset — exactly what an optional parameter is for.
- **Step 2 — `bar(cb)` (inside `foo`) is rejected.** Here `cb` has `foo`'s
  *declared* type `fn(a: string)` (exact, `accept = [1, 1]`), not its
  original type. `bar`'s slot is `fn(a: string, b: number)` with
  `accept = [2, 2]`. The check fails: `uG = 1 >= uF = 2` is false — the
  exact unary function would be invoked with a second argument it refuses
  (§4.2.1.1).

The chain breaks because the *advertised* unary type `fn(a: string)`
cannot widen back up to a binary slot. The exactness that `foo`'s
parameter declares — "this is called with exactly one argument" — is the
information that stops the unsound second step, independently of `cb`'s
optional `b`.

###### Variations that look promising but still fail

A reader might try to "rescue" the chain by introducing inexact slots
or explicit `Inexact<T>` widenings. None of them succeed; each falls to
a different rule.

**Variation A — inexact slot at `foo`, exact `cb`:**

```
fn foo(cb: fn(a: string, ...) -> undefined) { bar(cb) }
foo(cb)
// Error: exact </: inexact. accept(cb) = [1, 2] does not cover the
// inexact slot's [1, ∞): the exact cb would be invoked with extras it
// refuses (§4.2.1.1).
```

**Variation B — inexact slot at `foo`, user widens with `Inexact<T>`:**

```
fn foo(cb: fn(a: string, ...) -> undefined) { bar(cb) }
foo(Inexact(cb))
// Inexact(cb) is fn(a: string, b?: boolean, ...). The accept-set check
// now passes ([1, ∞) ⊇ [1, ∞)), but parameter-type contravariance fails
// at position 1: foo's inexact slot may pass an extra of arbitrary type
// there, and that arbitrary type is not <: the supplier's declared
// b?: boolean. Error.
```

This is the load-bearing rejection: if it were accepted, the chain would
complete soundly-looking and miscompile. Inside `foo`, `cb` would have the
slot type `fn(a: string, ...)`, which *does* satisfy `bar`'s exact
`fn(a: string, b: number)` slot (an inexact function filling a narrower
exact slot, §4.2.1), and `bar` would then invoke the original `cb` with
`("hello", 5)`. Contravariance at the `foo` boundary is what stops it.

**Variation C — inexact slot at `foo` with matching arity:**

```
fn foo(cb: fn(a: string, b: number, ...) -> undefined) { bar(cb) }
foo(Inexact(cb))
// Error: parameter-type contravariance fails at position 1.
// Slot demands b: number; supplier offers b?: boolean. number </: boolean.
```

**Variation D — both `foo` and `bar` inexact, all arities aligned:**

```
fn foo(cb: fn(a: string, b: number, ...) -> undefined) { bar(cb) }
fn bar(cb: fn(a: string, b: number, ...) -> undefined) { cb("hello", 5) }
foo(Inexact(cb))
// Error: same contravariance failure at position 1.
```

The parameter-type check at position 1 is the ultimate backstop. The
accept-set rule deliberately *permits* an inexact function to fill a
narrower slot — that is the feature — but it never relaxes the
contravariant check on the declared parameter types. Even when the user
systematically loosens every slot to inexact and explicitly widens with
`Inexact<T>`, that contravariant check (`number`/arbitrary-extra vs
`boolean` at position 1) preserves the soundness invariant.

##### 4.2.1.3. Anti-patterns that undermine the soundness story

The §4.2.1.2 analysis shows that pure Escalier source cannot reproduce
the TypeScript unsoundness. The remaining ways to introduce the
runtime bug all involve the user explicitly opting out of type
safety. These are anti-patterns, and code review should flag them.

**A1. `any` in callback parameter types.**

```
fn cb(a: string, b?: any) -> undefined {
    if b { ... }   // body treats b as a boolean
}
```

Once `b: any` is in the type, contravariance trivially succeeds at every
boundary (everything `<:` `any`), and the chained-widening unsoundness
becomes reachable. The escape is not specific to exact types — `any`
disables every check — but the function-typing rules' soundness
guarantees no longer apply once it is introduced. **Prefer `unknown`
plus an explicit narrowing**, which forces the unsafe step to be
visible at the source.

**A2. Intersection types that promise overloads the implementation
does not honor.**

```
val cb: (fn(a: string) -> undefined) & (fn(a: string, b: number) -> undefined)
    = realImpl                            // realImpl is fn(a, b?: boolean)
```

A function intersection (§7.7) is a *promise* that the value handles
each listed arm correctly. Annotating an implementation with an
intersection it does not actually satisfy is a lie that the type
system cannot independently verify. The runtime confusion that
follows is the user's annotation, not a gap in the exactness rules.
**Construct intersection-typed values only from implementations that
genuinely handle every arm**, or use a single accurate signature.

**A3. Unverified `.d.ts` declarations from third-party packages.**

A TypeScript-authored `.d.ts` whose declared signature does not match
its JavaScript implementation can carry the runtime unsoundness into
Escalier regardless of how carefully the Escalier-side types are
written. The standard exact/inexact import defaults (§8) cannot
detect this. **When importing from plain TypeScript, treat the
declared signature as a claim that requires the same scrutiny as any
other unverified input.** When the implementation is known to differ
from the declaration, narrow the declaration to match the implementation
(via a local override or a wrapper) rather than trusting the published
type.

**A4. `Inexact<T>` as a habit rather than an interop tool.**

`Inexact<T>` (§6.2) exists for the rare cases where a callback genuinely
must be passed to a TypeScript API that types it inexact. Using it
reflexively — for example, to silence a type error without
understanding why the exact type was rejected — re-introduces exactly
the widening the §4.2.1.1 argument exists to prevent (modulo the
parameter-type contravariance check, which remains in force).
**Reach for `Inexact<T>` only when interop forces it, and prefer a
hand-written wrapper** (`fn(a, ...) => cb(a)`) when the lossy step
deserves to be visible at the source. This is the same widening pattern
recommended in §4.2.1.1 (an exact callable exposed through an
inexact-facing slot) — the wrapper makes the arity narrowing explicit
at the source rather than implicit at the type-system level.

**A5. `any`-typed callback parameters in Escalier-authored
`.d.ts`-equivalents.**

When emitting `.d.ts` for downstream Escalier consumers, the
`@escalier-type` JSDoc tag carries the original Escalier type
(§9). Any `any` introduced into a published signature defeats the
soundness story for every consumer, not just the author. Treat
published `any` in function types as a soundness-breaking commitment
that should not be made lightly.

In short: the function-typing rules in §4.2.1 are sound *with respect
to fully-typed Escalier source*. Each anti-pattern above is a way the
user explicitly tells the type system "don't check this," and each
re-opens the door to the bug `parseInt`-in-`map` and the
optional-trailing-param example were designed to close.

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

At a **direct** call site (`f(args)` where `f`'s type is known), the checker
enforces the same bounds regardless of exactness:

- The number of supplied arguments must be `>=` the number of required
  parameters and `<=` the number of declared parameters (counting optionals
  and any typed rest as documented).

Passing more arguments than the callee declares is rejected for both exact
and inexact function types — supplying extra arguments to a call you can see
is treated as a likely mistake:

```
declare val f: fn(x: number, y: number) -> number          // exact
declare val g: fn(x: number, y: number, ...) -> number     // inexact

f(1, 2)        // okay
f(1, 2, 3)     // Error — more arguments than declared
g(1, 2)        // okay
g(1, 2, 3)     // Error — more arguments than declared
```

The exact/inexact distinction does **not** affect direct calls; it governs
**callback subtyping** (§4.2.1) — whether the function may be supplied where a
function-typed slot expects it. An inexact type's tolerance for extras is a
statement about how the function may be *invoked through a slot that holds it*,
not a license to pass extras at a visible call site.

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
  function types.** A single fixed-exactness pattern would only match its
  own exactness and let the other fall to the `else` branch (exact and
  inexact function types are not interchangeable as match targets — their
  accept-sets differ, §4.2.1). The pattern `fn(...args: infer P) -> any`
  and the constraint `fn(...args: any) -> any` are therefore treated as
  exactness-agnostic for matching and constraint-checking purposes — they
  accept either an exact or an inexact function type, and the captured
  `infer P` faithfully reflects which one it was.

Consequently:

```
type F = fn(a: string, b: number) -> boolean         // exact
type G = fn(a: string, b: number, ...) -> boolean    // inexact

type PF = Parameters<F>    // exact [string, number]
type PG = Parameters<G>    // inexact [string, number, ...]
```

Note that `Parameters` is a projection, not a subtyping bridge: even
though the *exact* `F` is not a subtype of the *inexact* `G` as function
types (§4.2.1.1 — `F` cannot fill `G`'s wider slot), their parameter
tuples `PF` and `PG` are related via the usual tuple rule
`[string, number] <: [string, number, ...]`. This is intentional — the
asymmetry on function-type subtyping reflects the call-contract
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
  `fn(a: A, b: B, ...) -> R`, a function that tolerates being invoked with
  extra arguments. Producing the inexact form from an exact one is the one
  direction subtyping *cannot* give you — an exact function is never a
  subtype of its inexact counterpart (§4.2.1.1), since the inexact slot
  would invoke it with extras it refuses. `Inexact<F>` exists precisely to
  express that widening explicitly (with the lossy step visible at the
  source), so that the widened value can then fill inexact slots.
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

### 6.6. Value-Level Conversion: `exact<T>(v)`

The `Exact<T>` / `Inexact<T>` utilities in §6.1 and §6.2 are
*type-level* operators — they change a type without producing any
runtime code. Sometimes a program also needs to convert a *value* from
the inexact form to the exact form (e.g. to call into Escalier code
that requires an exact-typed argument with a TS-imported inexact
value). This subsection specifies a built-in `exact<T>(v)` operator
for that purpose, and explicitly documents the categories where it is
*not* available.

#### 6.6.1. Semantics by category

`exact<T>(v)` is a compile-time-resolved built-in. The checker
inspects the target type `T`, validates that the input type `typeof v`
is the corresponding inexact form (or already exact), and lowers the
call to the appropriate runtime expression. There is no generic
"convert anything" implementation — each category gets its own
lowering, and some categories have no lowering at all.

| Category | Lowering | Cost |
|---|---|---|
| Object | Property pick into a fresh object: `{ k1: v.k1, ..., kn: v.kn }` | One allocation, `O(n)` in declared keys |
| Tuple | `v.slice(0, n)` (where `n` is the declared length) | One allocation, `O(n)` |
| Union (discriminable) | A `match` over the listed members, emitting a `RuntimeError` for unhandled tails | `O(1)` per call (one discrimination check) |
| Function | **No lowering needed** — the usual inexact→exact narrowing is already a subtype relationship (a plain annotation), so `exact<...>` is unnecessary. See §6.6.3. |

##### Runtime type information

`exact<T>(v)` is deliberately designed not to depend on reified
types. The object and tuple lowerings are pure property access and
slicing — the declared keys and length are known to the compiler from
`T`, so the emitted JavaScript only manipulates values it can already
see. No type tags or runtime type descriptors are introduced.

The union lowering does require some runtime cooperation, but only
in the form of mechanisms JavaScript already exposes: value equality
for primitive-literal unions, discriminator-field equality for
tagged unions, and `instanceof` for unions of class instances. The
checker selects the appropriate discrimination strategy from `T`'s
structure and emits the corresponding JS check. Each strategy uses
ordinary JS values (string literals, the class objects themselves)
rather than a separate runtime type system.

| Union shape | Discrimination strategy | Example |
|---|---|---|
| Primitive literals (`"a" \| "b" \| ...`, `1 \| 2 \| ...`, `true \| false \| ...`) | Value equality (`v === "a"`) | exact color, exact bit flag |
| Discriminated objects (`{kind: "ok", ...} \| {kind: "err", ...}`) | Discriminator-field equality (`v.kind === "ok"`) | Result types, tagged AST nodes |
| Class instances (`Circle \| Square`) where each member is a `final` class instance type | `instanceof` against the class object (`v instanceof Circle`) | enum variants (§2.6.1), exception hierarchies |
| Mixed primitive/class (`string \| Date \| RegExp`) | `typeof` for primitives, `instanceof` for the rest | rarely needed, but supported |

Unions whose members can only be told apart by **structural property
shape** — e.g., `{x: number} | {y: string}` with no shared discriminator
— are **rejected at compile time**. JavaScript has no built-in way to
inspect arbitrary structural shapes at runtime, and adding one would
require exactly the reified-type machinery the rest of this design
avoids. Users in this position must either add a discriminator field
to their union members or write a manual converter with explicit
predicates.

Examples:

```
val inexactPoint: {x: number, y: number, ...} = importedFromTS()
val p: {x: number, y: number} = exact(inexactPoint)
// lowers to: val p = { x: inexactPoint.x, y: inexactPoint.y }

val inexactPair: [string, number, ...] = importedFromTS()
val pair: [string, number] = exact(inexactPair)
// lowers to: val pair = inexactPair.slice(0, 2)

val inexactColor: "red" | "green" | "blue" | ... = importedFromTS()
val color: "red" | "green" | "blue" = exact(inexactColor)
// lowers to: val color = match (inexactColor) {
//     "red" => "red", "green" => "green", "blue" => "blue",
//     _ => throw RuntimeError("exact: value outside listed members"),
// }
```

For object and tuple lowerings, extras present on the input are
silently dropped: an inexact object with a `z` property converted to
exact `{x, y}` produces an object without `z`. This matches the §6.1
*type-level* semantics — `Exact<T>` over an inexact type removes the
trailing `...`, not the extra members the runtime value happened to
carry. For union lowerings, the runtime error makes the alternative
explicit: an inexact value that does not match any listed member
throws rather than silently slipping through.

#### 6.6.2. Conditions on `T` and `v`

For `exact<T>(v)` to type-check:

- `T` must be in a category where the operator has a lowering (object,
  tuple, or union — see §6.6.3 for the excluded categories).
- `typeof v` must be assignable to `Inexact<T>`. In particular, `v`
  must have at least the declared members of `T` (with each
  member-type covariantly compatible). For unions, every listed member
  of `T` must be assignable to some listed member of `v`'s type.
- `T` must itself be a concrete enough shape that the lowering has
  something to enumerate. `exact<Inexact>(v)` (with `Inexact` being a
  pure interface or non-`final` class instance type) is rejected —
  there is no closed set of members to pick.
- **For union targets:** every listed member of `T` must be
  *discriminable at runtime* using one of the strategies in §6.6.1
  (value equality for primitive literals, discriminator-field equality
  for tagged objects, or `instanceof` for `final` class instance
  types). Unions whose members can only be told apart by structural
  property shape are rejected — JavaScript has no general way to
  inspect arbitrary structural shapes at runtime, and `exact<T>(v)`
  is designed not to introduce reified-type machinery to fill that
  gap.

If `T` is already exact, `exact<T>(v)` is the identity: the checker
emits `v` unchanged. This is convenient for generic code that should
not have to case-split on whether the input type happens to be exact
or inexact.

#### 6.6.3. Why functions need no `exact<...>` lowering

Function types have no `exact<...>` lowering because, unlike objects,
tuples, and unions, no runtime work is required to tighten one. The
object/tuple/union lowerings exist because tightening drops real runtime
data (extra properties, trailing elements), so a fresh allocation is
needed. Narrowing a function's *type* drops nothing at runtime — the
function value is unchanged.

And the narrowing that interop usually wants is already a subtype
relationship, so a plain annotation does it for free. An inexact function
is a subtype of any compatible exact function whose required count it
meets (§4.2.1):

```
declare val f: fn(a: A, b: B, ...) -> R   // inexact, from TS interop

val g: fn(a: A, b: B) -> R = f            // okay — inexact <: narrower exact
// accept(f) = [2, ∞) ⊇ accept(target) = [2, 2]. g is typed exact; the
// inexact f underneath tolerates the two-argument calls g's type permits.
```

A separate, genuinely lossy operation is narrowing to *fewer* parameters
than the function requires (e.g. forcing `fn(a: A, b: B, ...) -> R` into
`fn(a: A) -> R`, which the function cannot satisfy — it needs `b`). That
is not a type-narrowing at all but a behavioral change, and it should be
written as an explicit wrapper (`fn(a) => f(a, defaultB)`) so the dropped
argument is visible at the source. No built-in performs it silently.

The class instance form is excluded for the reason expressed in
type-level terms (§6.1): a non-`final` class instance type cannot be
tightened because the class's openness is part of its public contract.
Declare the class `final` if you need exact instance types.

#### 6.6.4. TypeScript interop

`exact<T>(v)` has no TypeScript equivalent. When emitting `.d.ts`,
calls to `exact<...>` are erased to their lowered form — TypeScript
consumers see the property-pick / slice / match expression directly,
with the inexact-to-exact step recorded only in the `@escalier-type`
JSDoc payload (§9). Escalier consumers re-importing the declaration
recover the original `exact<T>(v)` form.

A common pattern at the TS-interop boundary: wrap an imported value
once at the boundary and pass the exact form through the rest of the
program.

```
import { getConfig } from "some-ts-lib"
// `getConfig` declared as returning {host: string, port: number}
// (imported as inexact per §8).

fn loadConfig() -> {host: string, port: number} {
    return exact(getConfig())
}
```

The boundary `exact(...)` makes the trust decision explicit at the
source — every downstream consumer of `loadConfig` works with a
genuinely exact value, with no further conversion needed.

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

When `T` is a union, the conditional distributes over its members:
each listed member is checked against `U` individually, and the
per-member results are unioned.

For an **exact** union, every member is enumerated by name, so the
distribution is complete:

```
type Foo<T> = if T : string { "yes" } else { "no" }

type R1 = Foo<"a" | "b">             // exact: "yes" | "yes" = "yes"
```

The exactness of the result follows the standard union-combining
rules (§7.6): if every distributed branch result is exact, the
overall result is exact.

For an **inexact** union `M1 | ... | Mn | ...`, distribution can
enumerate only the listed members `M1..Mn`. The unknown tail is
`unknown`-typed (§5.2), and for any `U` other than `unknown` the
predicate `unknown : U` is **undecidable** — the tail may at runtime
contain values that satisfy `U` (sending them to the `Then` branch)
or values that do not (sending them to the `Else` branch).

The checker handles this conservatively by **widening the tail's
contribution to include both branch results** and **marking the
overall result inexact**. Concretely, the distributed type is:

```
Distributed(M1) | ... | Distributed(Mn) | Then | Else | ...
```

where `Then` and `Else` are the conditional's branch result types
(evaluated at the worst-case input for any `T`-dependent
computation in the branch — typically `unknown` for `infer`-bound
positions). The trailing `...` records that conservative widening
occurred and that downstream consumers cannot rely on the result
being a closed set even when `Then` and `Else` are themselves exact.

```
type R2 = Foo<"a" | "b" | ...>       // inexact: "yes" | "no" | ...
                                     //   "yes" — distributed from "a" and "b"
                                     //   "no"  — Else branch widened for the
                                     //           undecidable unknown tail
                                     //   ...   — marker that widening occurred
```

##### Exactness rule for conditional results

The conditional's result is **exact** iff *both* of the following hold:

1. The predicate `T : U` is decidable for every member of `T`
   (including the unknown tail, if any).
2. Each contributing branch's result type is itself exact.

Otherwise the result is **inexact**. For an exact `T`, condition (1)
is automatic — every member is named and the predicate is decided
per-member. For an inexact `T`, condition (1) holds only when `U` is
`unknown` (so the tail trivially routes to `Then`) or under a
sufficiently strong constraint on `T` at the type-parameter level
(§7.4.4) that prevents inexact arguments from reaching the
conditional in the first place.

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
  `(fn(A) -> R) & (fn(B) -> S)` is callable as either signature. The
  operands' exactnesses do not need to agree; each call site is resolved
  to a matching overload and checked against that overload's accept-set
  (§4.2.1) independently.
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
tuples, and unions. (Function-type constraints instead follow the
accept-set subtyping rule of §4.2.1: a function argument satisfies a
function-type constraint exactly when it is a subtype of the constraint —
so an inexact function satisfies a narrower exact constraint, but an exact
function does not satisfy a wider or inexact one.)

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

### 10.3. Function: A Minimal Callback Slot Prevents Surprise Arguments

A common JavaScript pitfall: `["1", "2", "3"].map(parseInt)` returns
`[1, NaN, NaN]` because `parseInt` accepts a second `radix` argument and
`Array.prototype.map` passes the index, so `parseInt("2", 1)` is evaluated
in base 1. The bug is that `map` passes a *surprise* second argument.

Exact function types let an API author close this off — not by rejecting
`parseInt`, but by declaring a callback slot that promises to invoke its
argument with exactly one parameter:

```
declare fn map<T, U>(arr: Array<T>, f: fn(item: T) -> U) -> Array<U>

map(["1", "2", "3"], parseInt)   // okay — returns [1, 2, 3]
```

The slot type `fn(item: T) -> U` is exact, so `map` is bound by its own
declared signature to call `f` with a single argument; the JS index is
never forwarded. Passing `parseInt` (type `fn(s: string, radix?: number)
-> number`, accept-set `[1, 2]`) is *accepted* — its accept-set covers the
slot's `[1, 1]` (§4.2.1) — and because `map` only ever supplies one
argument, `parseInt` runs as `parseInt("1")`, `parseInt("2")`,
`parseInt("3")`, yielding `[1, 2, 3]`. The exactness of the **slot** is
what prevents the bug, by constraining what the API may pass, rather than
constraining which functions the caller may supply.

A binary callback slot, by contrast, *would* reject a plain unary
function: into `fn(elem: T, index: number) -> U` (accept-set `[2, 2]`) an
exact `fn(elem: T) -> U` (accept-set `[1, 1]`) does not fit, since it
would be invoked with a second argument it refuses. This is exactly why
`std:array` splits `map` (unary) from `mapi` (binary) rather than exposing
one binary slot — see §11.1.

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

## 11. `std:*` and `dom:*` Module Adjustments

Exact function types change what idiomatic JavaScript APIs look like when
wrapped for Escalier. Several built-in JS APIs invoke their callbacks with
"bonus" arguments that are rarely used in practice and that, under
TypeScript's permissive callback compatibility, enable bugs like
`["1","2","3"].map(parseInt)`. With exact callbacks, those bonus arguments
become a usability problem: either every callback must declare every
parameter, or we relax to inexact and lose the safety guarantee.

The resolution is to design the `std:*` (and where relevant, `dom:*`)
wrappers around **exact, minimal callback signatures**, and to expose
secondary methods for the less common cases where the extra arguments are
genuinely wanted. The runtime cost is zero — extras are dropped at the JS
boundary as they already are today — and the type-level cost is one extra
method per affected operation.

### 11.1. `std:array`

The iteration methods on `std:array` follow the split below. Each `map`-
style method takes an **exact unary** callback; the `-i` variant takes an
exact binary callback that also receives the index.

| Method | Callback type | Notes |
|---|---|---|
| `map<T, U>(arr, f)` | `fn(elem: T) -> U` | The default; rejects `parseInt`-style misuse. |
| `mapi<T, U>(arr, f)` | `fn(elem: T, index: number) -> U` | Use when the index is needed. |
| `filter<T>(arr, p)` | `fn(elem: T) -> boolean` | |
| `filteri<T>(arr, p)` | `fn(elem: T, index: number) -> boolean` | |
| `forEach<T>(arr, f)` | `fn(elem: T) -> undefined` | |
| `forEachi<T>(arr, f)` | `fn(elem: T, index: number) -> undefined` | |
| `find<T>(arr, p)` | `fn(elem: T) -> boolean` | |
| `findi<T>(arr, p)` | `fn(elem: T, index: number) -> boolean` | |
| `findIndex<T>(arr, p)` | `fn(elem: T) -> boolean` | Returns `number`. |
| `some<T>(arr, p)` | `fn(elem: T) -> boolean` | |
| `every<T>(arr, p)` | `fn(elem: T) -> boolean` | |
| `reduce<T, A>(arr, f, init)` | `fn(acc: A, elem: T) -> A` | No index variant by default. |
| `reducei<T, A>(arr, f, init)` | `fn(acc: A, elem: T, index: number) -> A` | When the index is needed. |

The third "source array" argument that `Array.prototype.{map,filter,...}`
passes is **not** exposed by any `std:array` method. In the rare case where
a callback genuinely needs the source array, the caller already has it in
scope and can close over it explicitly.

Users who want the index *and* something more (e.g. both index and source)
should fall back to a `for` loop or `arr.entries()` rather than reach for
an ever-wider callback. The split stops at adding at most one context
parameter (the index) beyond the callback's intrinsic shape — so
`map`/`filter`/`forEach`/`find` cap out at two-parameter callbacks
(elem + index), and `reduce` caps out at three (acc + elem + index in
`reducei`).

### 11.2. Other `std:*` callback-taking APIs

A survey of the non-DOM `lib.es*.d.ts` surface area shows that
callback-taking methods fall into three patterns. Each has a fixed
treatment.

**Pattern 1 — Bonus-arg callbacks (the `-i` split).** Apply the §11.1
rule to every method whose JS callback receives a fixed but mostly
unused trailing argument:

- `std:typedArray.{map,filter,forEach,find,findIndex,reduce,sort,...}` —
  same shape as `std:array`, including the `-i` variants.
- `std:iterator.{map,filter,flatMap,reduce,forEach,some,every,find}`
  (the ES2024 Iterator helpers) — exact unary + `-i` binary.
- `std:map.groupBy`, and other ES2024+ `Map`/`Set` callback methods —
  the key (for maps) or value (for sets) replaces "index" in the `-i`
  variant.

**Pattern 2 — Fixed-arity callbacks (no split needed).** Some callbacks
have a known, useful arity with no bonus args; these are exposed as a
single exact signature:

- `std:array.sort`, `std:typedArray.sort` — `fn(a: T, b: T) -> number`.
- `std:json.parse` reviver — `fn(key: string, value: unknown) -> unknown`.
- `std:json.stringify` replacer — `fn(key: string, value: unknown) -> unknown`.
- `Promise` executor — `fn(resolve, reject) -> undefined`.
- `Promise.then/catch/finally` callbacks — exact unary on the
  fulfillment/rejection value, or exact nullary for `finally`.
- All `ProxyHandler` traps — each trap has a fixed, documented arity.

**Pattern 3 — Genuinely variadic callbacks.** Only one method in the
core lib surface qualifies: `String.prototype.replace` with a function
replacement, where the callback is invoked with
`(match, p1, …, pN, offset, fullString)` and `N` depends on the regex.
An inexact callback type is **not** the right tool here. An inexact
*slot* such as `fn(match: string, ...) -> string` has accept-set
`[1, ∞)`, and an ordinary exact function the user naturally writes —
`fn(match: string) -> string`, accept-set `[1, 1]` — does *not* fit it:
its finite upper bound `1` cannot cover the slot's `∞` (§4.2.1). So an
inexact slot would reject exactly the functions users want to pass.
Instead, `std:string` exposes two specialized methods, each with an exact
callback:

```
fn replace(s: string, pattern: string | RegExp, f: fn(match: string) -> string) -> string
fn replaceWithGroups(
    s: string,
    re: RegExp,
    f: fn(match: string, groups: Array<string>, offset: number) -> string,
) -> string
```

The simple `replace` accepts either a string or a RegExp as the search
pattern and exposes only the matched substring to the callback — for
the string-search case there are no capture groups, and for the
RegExp case the groups are discarded. The `replaceWithGroups` form is
RegExp-only (a string search would always yield an empty `groups`
array) and exposes the capture groups and offset.

The `std:string` implementation collects the variadic capture-group
arguments into a single `groups` array before calling the user's
function, so the variadic shape is handled at the boundary, not in the
type system.

### 11.3. Non-callback variadic functions

A separate cluster of built-ins takes a variable number of *values*
(not callbacks). These use **typed rest parameters**, which are exact
(§4.2.2):

- `std:math.max`, `std:math.min` — `fn(...nums: Array<number>) -> number`.
- `std:console.log` and peers — `fn(...args: Array<unknown>) -> undefined`.

The §4.2.2 rule explicitly forbids combining a typed rest parameter
with the inexact `...` sentinel, so there is no ambiguity here: typed
rest is always the correct spelling for "any number of values of a
known element type."

A handful of TS-surface APIs use an *untyped* variadic passthrough
(`setTimeout(fn, ms, ...args)`, `Function.prototype.bind` partial
application). `std:*` does not reproduce these signatures. Instead:

- `std:timer.setTimeout(cb: fn() -> undefined, ms: number) -> TimerHandle` —
  users close over any extra state explicitly.
- No `std:*` equivalent of `Function.prototype.bind`'s partial
  application — arrow functions and explicit closures replace it
  cleanly.

### 11.4. When inexact function types are appropriate

The survey above produces no case where `std:*` should expose an
inexact function type, in either parameter or return position. This is
not a coincidence: the §4.2.1.1 asymmetry means inexact function types
only make sense for values whose *call contract* genuinely permits
extra positional arguments — and Escalier-authored functions, by
default, never have such a contract.

Inexact function types are therefore principally an **interop
construct**:

- TS-imported functions are inexact by default (§8), because we
  cannot trust the TS-side arity contract.
- The `Inexact<T>` utility (§6.2) is the explicit way to widen an
  exact function type when interop genuinely requires it.

Escalier source code — including the entire `std:*` and `dom:*`
surface — should be written with exact function types throughout. If
a future `std:*` or `dom:*` API appears to call for an inexact
function type, that's a strong signal the surface should be
restructured (split into multiple exact methods, switched to a typed
rest parameter, or dropped entirely) rather than reaching for `...`.

### 11.5. `dom:*` modules

A survey of `lib.dom.d.ts`, `lib.dom.iterable.d.ts`, and the worker
variants confirms that the §11.2 patterns cover the DOM surface as
well. The §11.4 conclusion — inexact function types have no place in
Escalier-authored wrappers — applies here without modification. The
specifics below show how each pattern maps to DOM APIs.

#### 11.5.1. Event listeners (Pattern 2 — fixed-arity)

`addEventListener` and the `on*` event-handler properties take exact
unary callbacks. The event argument *is* the callback's purpose, and
there are no JS-level bonus arguments to discard:

```
fn addEventListener<K : keyof HTMLElementEventMap>(
    target: EventTarget,
    type: K,
    handler: fn(event: HTMLElementEventMap[K]) -> undefined,
) -> undefined
```

#### 11.5.2. Observer callbacks (Pattern 1 — drop the bonus arg)

Every modern observer constructor invokes its callback with
`(entries, observer)`, where the `observer` argument is the same value
the user just constructed and has in scope. It's pure bonus; the
`dom:*` wrapper drops it and exposes an exact unary callback. This
applies uniformly to:

- `MutationObserver` — `fn(mutations: Array<MutationRecord>) -> undefined`
- `IntersectionObserver` — `fn(entries: Array<IntersectionObserverEntry>) -> undefined`
- `ResizeObserver` — `fn(entries: Array<ResizeObserverEntry>) -> undefined`
- `PerformanceObserver` — `fn(entries: PerformanceObserverEntryList) -> undefined`
- `ReportingObserver` — `fn(reports: Array<Report>) -> undefined`

Unlike `std:array`'s `-i` split, there is no observer-receiving
variant: the user already has the observer reference in scope, so the
second method would be pure noise.

#### 11.5.3. Array-like `forEach` (Pattern 1 — `-i`/`-k` split)

A large number of DOM collection types expose `Array.forEach`-shaped
callbacks with `(value, key, parent)`. Each gets the `-i`/`-k` split
following §11.1:

- Index-keyed: `NodeList`, `NodeListOf<T>`, `DOMTokenList`,
  `CSSNumericArray`, `CSSTransformValue`, `CSSUnparsedValue`,
  `EventCounts`.
- String-keyed: `FormData`, `Headers`, `URLSearchParams`,
  `HighlightRegistry`, `AudioParamMap`, `CustomStateSet`,
  `RTCStatsReport`.

The "parent collection" third argument is dropped in every case,
matching the §11.1 rule for `std:array`.

#### 11.5.4. Scheduling, filter, and one-shot callbacks (Pattern 2)

These are already fixed-arity with no bonus args, and the `dom:*`
wrapper preserves the natural exact signature unchanged:

- `requestAnimationFrame(cb: fn(time: DOMHighResTimeStamp) -> undefined)`
- `requestIdleCallback(cb: fn(deadline: IdleDeadline) -> undefined, options?)`
- `queueMicrotask(cb: fn() -> undefined)`
- `NodeFilter` (used by `createNodeIterator` / `createTreeWalker`) —
  `fn(node: Node) -> number`
- `Geolocation.{getCurrentPosition, watchPosition}` success/error
  callbacks
- `MediaSession.setActionHandler` — `fn(details: MediaSessionActionDetails) -> undefined`
- `HTMLVideoElement.requestVideoFrameCallback` —
  `fn(now: DOMHighResTimeStamp, metadata: VideoFrameCallbackMetadata) -> undefined`
- WebCodecs output and error callbacks
- `DataTransferItem.getAsString` callback

`setTimeout` and `setInterval` follow §11.3: the `...arguments: any[]`
passthrough is dropped, and the `dom:*` (or `std:timer`) wrapper
exposes `fn(cb: fn() -> undefined, ms: number) -> TimerHandle`. Users
close over any extra state.

#### 11.5.5. Stream controllers are *not* bonus args

`ReadableStream`, `WritableStream`, and `TransformStream` source/sink/
transformer callbacks take a `controller` parameter that superficially
looks like a bonus argument but is essential: the callback's whole job
is to call `controller.enqueue(...)`, `controller.terminate()`, etc.
The `dom:*` wrappers therefore preserve the natural exact signatures
without splitting:

```
fn(chunk: I, controller: TransformStreamDefaultController<O>) -> Promise<undefined> | undefined
fn(controller: ReadableStreamDefaultController<R>) -> Promise<undefined> | undefined
```

This is the one DOM pattern that *looks* like Pattern 1 but isn't — a
useful reminder that "second argument" is not the same as "bonus
argument." The test is whether the argument carries information the
callback can't obtain from its enclosing scope.

#### 11.5.6. Non-callback variadics

DOM has many variadic-value APIs, all of which use typed rest
parameters (§11.3) and pose no challenge:

- `Element.{append, prepend, after, before, replaceWith, replaceChildren}`
- `DOMTokenList.{add, remove}` and `Element.classList` operations
- `Document.{write, writeln}`
- `Console.{log, error, warn, info, debug, trace, group, ...}`
- `CSSNumericValue.{add, sub, mul, div, min, max, equals, toSum}` and
  the `CSSMath*` constructors
- `RTCRtpSender.{addTrack, setStreams}`
- `Highlight` constructor

None require inexact function types; all are well-served by
`fn(...args: Array<T>) -> R` with a known element type.

### 11.6. TypeScript interop

The native callback-taking methods on `Array.prototype` (`map`, `filter`,
`forEach`, `find`, `findIndex`, `some`, `every`, `reduce`, `reduceRight`,
and `flatMap`) are **not exposed** in Escalier. Allowing them via direct
TypeScript import would reintroduce the `parseInt`-style misuse the
`std:array` wrappers exist to prevent, and would give users two
near-identical APIs with different safety properties — exactly the kind
of footgun the exact-types feature is designed to eliminate.

Concretely, the Escalier prelude's `Array<T>` type omits these methods
from the instance interface. Non-callback methods on `Array.prototype`
(`length`, `push`, `pop`, `slice`, `concat`, indexing, `includes`,
`indexOf`, `join`, etc.) remain available unchanged; only the
callback-taking subset is removed in favor of the `std:array`
free-function equivalents.

The same rule applies to any other built-in JS type whose native
callback-taking methods have a corresponding `std:*` wrapper: the
wrapper is the supported surface, and the native method is hidden at
the type level even though it still exists at runtime.

### 11.7. Class finality for `std:*` and `dom:*` wrappers

Per §2.6, class instance types are inexact by default; per §2.6.1,
declaring a class `final` makes its instance type exact and disallows
subclassing. This subsection specifies which built-in classes in the
`std:*` and `dom:*` wrappers are declared `final` and which are not.

The analysis below is grounded in an enumeration of all class-like
declarations in TypeScript's bundled `lib.*.d.ts` files. Under
Escalier's interpretation of `interface Foo` + `var Foo: FooConstructor`
(the es-lib pattern) and `interface Foo` + `var Foo: { prototype: Foo;
new(): Foo; ... }` (the dom-lib pattern), TypeScript's lib declares 692
classes. The full enumeration with parents and source files is saved
as [`builtin-classes.md`](./builtin-classes.md) alongside this document.

#### 11.7.1. The mechanical rule: lib subclasses imply non-final

Any class that has at least one subclass declared in the TypeScript lib
itself is exposed as **non-final** by the corresponding wrapper.
Declaring such a class `final` would forbid lib-declared subclasses,
which is incoherent.

This rule alone forces roughly 110 classes non-final. The major
non-final bases are:

- **`Error`** — 12 direct subclasses (`EvalError`, `RangeError`,
  `ReferenceError`, `SyntaxError`, `TypeError`, `URIError`,
  `AggregateError`, `SuppressedError`, `DOMException`, plus the
  WebAssembly `CompileError` / `LinkError` / `RuntimeError`).
- **`Event` → `UIEvent` → `MouseEvent`** — each non-leaf level in the
  event hierarchy is non-final because of its lib subclasses.
- **`EventTarget`** — ~70 lib subclasses (`Node`, `Window`,
  `AbortSignal`, `XMLHttpRequest`, `WebSocket`, all `AudioNode`s, all
  IDB and Media* types, etc.).
- **`Node` → `Element` → `HTMLElement` / `SVGElement` / `MathMLElement`** —
  the DOM element tree. `HTMLElement` and `SVGElement` are also the
  documented web-platform extension points (§11.7.2).
- **`AudioNode` → `AudioScheduledSourceNode`** — Web Audio.
- **`CSSRule` → `CSSGroupingRule` → `CSSConditionRule`** — CSS OM.
- **`CSSStyleValue` → `CSSNumericValue` → `CSSMathValue`** — CSS Typed OM.
- **The `*ReadOnly` / `*Mutable` pairs**: `DOMMatrixReadOnly`,
  `DOMPointReadOnly`, `DOMRectReadOnly` — each non-final because the
  mutable variant extends it.
- **Other base classes with lib subclasses**: `Blob` (→ `File`),
  `CharacterData`, `Document`, `DocumentFragment`, `Text`, `IDBRequest`,
  `MIDIPort`, `MediaStreamTrack`, `XMLHttpRequestEventTarget`,
  `BaseAudioContext`, `Animation`, `AnimationEffect`,
  `AnimationTimeline`, `AbstractRange`, `Credential`,
  `AuthenticatorResponse`, `PaymentRequestUpdateEvent`,
  `SpeechSynthesisEvent`, `TextTrackCue`.

#### 11.7.2. Documented extension points stay non-final at the leaves

Some lib non-leaves are also documented *extension points*: the web
platform expects user code to subclass them. They are already non-final
by §11.7.1, but the rationale is independent and worth stating
explicitly so the choice survives any future lib reshuffling:

- **`HTMLElement`** is the canonical custom-elements extension point
  (`class MyEl extends HTMLElement { ... }`, registered via
  `customElements.define`).
- **`SVGElement`** and **`MathMLElement`** are the analogous extension
  points for custom SVG and MathML elements.
- **`Error`** is the canonical extension point for user-defined error
  hierarchies (`class NotFoundError extends Error { ... }`).
- **`Event`** is the standard pattern for custom event subclasses, used
  alongside `CustomEvent` for typed-payload events.

No other DOM extension points exist that the §11.7.1 rule does not
already cover.

#### 11.7.3. Library-leaf classes: `final` by default

Every concrete class with no lib subclass and no platform expectation of
subclassing is exposed as `final`. This covers the bulk of the lib —
roughly 580 of the 692 classes. Representative groups:

- **All concrete `HTMLXxxElement` leaves** — `HTMLDivElement`,
  `HTMLAnchorElement`, `HTMLInputElement`, ..., ~85 classes.
- **All concrete `SVGXxxElement` leaves** — `SVGCircleElement`,
  `SVGPathElement`, ..., ~50 classes.
- **All concrete `*Event` leaves** — `KeyboardEvent`, `WheelEvent`,
  `CustomEvent`, `PointerEvent`, `DragEvent`, ..., ~40 classes.
- **All concrete `AudioNode` leaves** — `GainNode`, `OscillatorNode`,
  `AnalyserNode`, etc.
- **All observers** — `MutationObserver`, `IntersectionObserver`,
  `ResizeObserver`, `PerformanceObserver`, `ReportingObserver`.
- **All typed arrays** — `Int8Array` through `Float64Array`,
  `Float16Array`, `BigInt64Array`, `BigUint64Array`,
  `Uint8ClampedArray`.
- **ES wrapper types** — `Date`, `RegExp`, `ArrayBuffer`,
  `SharedArrayBuffer`, `DataView`, `WeakRef`, `FinalizationRegistry`,
  `DisposableStack`, `AsyncDisposableStack`, `Symbol`, `BigInt`,
  `Number`, `String`, `Boolean`.
- **DOM utility classes** — `URL`, `URLSearchParams`, `Headers`,
  `FormData`, `AbortController`, `Request`, `Response`, `FileReader`,
  `Blob` *as constructed* (the `File` subclass exists, so `Blob`
  itself is non-final; see §11.7.1).
- **Stream classes** — `ReadableStream`, `WritableStream`,
  `TransformStream`, and their controllers/readers/writers.
- **Promise** — see §11.7.4.

The payoff: `keyof HTMLDivElement` is the *exact* set of properties
HTML defines for `<div>`; `match` over a known union of `*Event`
subclasses can be exhaustive without a default arm; and reading
`.fooo` on an `HTMLDivElement` is a type error rather than "well, a
subclass might define it."

#### 11.7.4. Leaf exceptions: classes users genuinely subclass

A small number of leaves have a real-world subclassing pattern common
enough that the wrapper should accommodate it. These are exposed as
**non-final** despite having no lib subclasses:

- **`Array`** — subclassed for typed collections, observable arrays,
  custom iteration wrappers. The exact-instance benefit is small
  (most `Array` properties are well-known anyway) and the ergonomic
  cost of `final` is high.
- **`Map`** — subclassed for LRU caches, ordered maps, multi-maps,
  weak-keyed caches.
- **`Set`** — subclassed for ordered sets, sets-with-default, bag-like
  collections.

A second motivation for keeping `Array`, `Map`, and `Set` non-final is
**third-party TypeScript interop**. These three classes are the ones
TS libraries most commonly subclass (e.g. `NonEmptyArray`, `LRUCache
extends Map`, ordered-set wrappers). Marking them `final` would force a
boundary conversion at every interop site that produced or consumed
such a subclass — either an `Array.from(...)`-style copy or a wrapper
module. The downstream gains from exact instance types are not large
enough on these specific classes to justify that tax. For other final
classes the same interop hazard is present in principle but rare in
practice; see §11.7.7.

Other leaves that *appear* to invite subclassing are nonetheless
**final**. These already appear in §11.7.3's list; called out here to
explain why each one stays in the default-final bucket despite a
plausible-sounding case for subclassing:

- **`Promise`** — subclassing is spec-supported via `Symbol.species`
  but rare, bug-prone, and breaks common library patterns.
- **`Function`** — subclassing has no sensible runtime semantics
  beyond what arrow functions and ordinary closures already provide.
- **`WeakMap`, `WeakSet`, `WeakRef`, `FinalizationRegistry`** — same
  reasoning as `Promise`; no observed real-world subclassing demand.

The primitive wrapper types **`Number`**, **`String`**, and
**`Boolean`** are also final (per §11.7.3) and warrant no special
justification — they are essentially never subclassed in practice.

#### 11.7.5. Why default-final, not default-non-final

`final` is the right default for leaves because the benefit accrues to
*every* downstream use site, not just the declaration site:

- **Exact instance types** mean `keyof T` and property access on `T`
  are precise. Downstream code does not have to defensively handle
  "what if a subclass added a property."
- **Exhaustive `match` over class unions** works without a default
  arm — relevant for the DOM event union, the error hierarchy, and
  any user-defined enum-like union of variant classes (§2.6.1).
- **Better error messages**: a typo accessing `.fooo` on an
  `HTMLDivElement` is rejected immediately rather than treated as
  "perhaps a subclass defined `fooo`."

The cost of `final` — forbidding subclassing — is bounded by being
**reversible** in a backward-compatible way. Loosening a class from
`final` to non-`final` is a widening change (exact `<:` inexact for
objects/instance types), so we can start a class `final` and relax it
later if a genuine subclassing need emerges. The reverse direction
(non-`final` → `final`) is breaking. The conservative initial position
is therefore `final` on every leaf, with the §11.7.4 exceptions
explicitly carved out where current real-world usage justifies the
weaker contract.

#### 11.7.6. Round-tripping through `.d.ts`

When emitting `.d.ts` files, `final` is an Escalier-only annotation:
TypeScript has no equivalent. Per §9, the `@escalier-type` JSDoc tag
carries the original Escalier declaration (including the `final`
modifier), so other Escalier consumers recover the exactness of the
instance type. Plain TypeScript consumers see the erased class
declaration and lose nothing — TypeScript already allows subclassing
anything, and subclasses introduced on the TS side cannot leak back
into Escalier as instance values of the `final` type, because the
Escalier-side type only admits direct instances.

#### 11.7.7. Importing third-party subclasses of final classes

A TS library may declare `class Foo extends Bar` where `Bar` is a class
that the `std:*` or `dom:*` wrapper exposes as `final`. The `extends`
clause cannot be honored as a subtype relationship in Escalier without
contradicting the `final` modifier — `final` guarantees that values
typed `Bar` have *exactly* `Bar`'s declared members, and admitting an
`extends Bar` subclass would silently widen every consumer of `Bar`.

The importer therefore **strips the `extends` clause** when the parent
is a final wrapper class. The imported `Foo` is treated as an
independent, inexact class instance type that inherits `Bar`'s methods
into its own interface (flattened from the TS declaration) but is
**not** a subtype of `Bar`. Concretely, after import:

- `Foo`'s own methods and `Bar`'s inherited methods are both callable
  on values of type `Foo`.
- `val x: Bar = someFoo` is rejected — `Foo </: Bar`.
- `val x: Bar = Bar.from(someFoo)` (or whatever the analogous
  conversion is for the specific `Bar`) succeeds, with an explicit,
  runtime-visible boundary conversion.

This contains the impact of the mismatch: the third-party subclass is
usable through its own type, the user pays a conversion only when they
actually need to cross into a `Bar`-typed slot, and Escalier's
`final`-instance guarantees remain intact for all downstream consumers
of `Bar`.

**Residual unsoundness risk.** A `.d.ts` whose function signature
claims to return `Bar` but actually returns a subclass instance at
runtime can still produce a value that violates the `final` contract.
The exact-types system has no way to detect this; it is an instance of
the lying-`.d.ts` anti-pattern (§4.2.1.3 A3), made slightly more
consequential by `final` because consumers now actively rely on the
absence of extra members. The recommended defense is the same as for
any other lying `.d.ts`: wrap the suspect import in a local module
that converts at the boundary, and treat libraries that hide
subclasses behind base-class signatures as needing scrutiny.

This is part of the trade-off justifying the §11.7.4 carve-out for
`Array`, `Map`, and `Set`: those three are the classes where TS-side
subclassing is common enough that the boundary-conversion tax and the
lying-`.d.ts` exposure would be noticeable. For the other ~580 final
leaves, third-party subclassing is rare enough that the residual risk
is acceptable.

## 12. Open Questions

*(None at present — previously open questions about index signatures and
generic constraints are resolved in §2.7.1 and §7.12 respectively.)*
