# Row Types: Requirements for Structural Object Type Inference

## Motivation

Currently, all function parameters in Escalier must have explicit type annotations.
When a parameter lacks an annotation, the checker assigns a fresh type variable but
cannot refine it through property access, method calls, or indexing within the
function body. This means property access on an unannotated parameter produces an
`ExpectedObjectError` because `getMemberType` has no handler for `TypeVarType`.

Row types (also known as row polymorphism) enable the type system to infer
structural object types from usage. When a function accesses `obj.bar` and
`obj.baz`, the system should infer that `obj` has at least those properties —
without requiring the programmer to spell out the type.

```esc
fn foo(obj) {
    obj.bar = "hello":string
    obj.baz = 5:number
}
// inferred: fn foo(obj: {bar: string, baz: number}) -> void
```

## Definitions

- **Row variable**: A type variable that represents the "rest" of an object type's
  properties. Analogous to a regular type variable, but unifies with sets of
  object properties rather than whole types. Row variables are represented as
  `RestSpreadElem`s in `ObjectType.Elems`.
- **Open object type**: An object type with `Open: true` on the `ObjectType`
  struct. Open types can gain new properties during inference — when a property
  is accessed that doesn't yet exist, it is added automatically rather than
  producing an error.
  e.g. during inference of `fn foo(obj) { obj.bar; obj.baz }`, `obj` is bound
  to an open `ObjectType` that accumulates `bar` and `baz` as they are accessed.
- **Closed object type**: An object type with `Open: false`. Its property set is
  fixed and cannot gain new properties. All object types written by programmers
  in type annotations are closed. Inferred types are closed after the enclosing
  function body is fully inferred.
- **Open vs. row variable**: Open/closed (`Open` field) and row variables
  (`RestSpreadElem`) are **orthogonal**. An open type may or may not have a row
  variable, and a closed type may have a row variable (for row polymorphism).
  Open/closed controls whether the property set can grow during inference.
  A `RestSpreadElem` represents a row variable for row polymorphism or a spread
  source. Both are also orthogonal to **exactness** (`Exact` field), which
  controls whether an object type can unify with another that has additional
  properties.
- **Row constraint**: With Option C, row constraints are represented implicitly
  by the `PropertyElem`s and `MethodElem`s within an open `ObjectType`. When a
  property is accessed on a type variable, it is eagerly bound to an open
  `ObjectType` containing that property — the constraint is the structure of
  the `ObjectType` itself, not a separate data structure.

## Requirements

### 1. Property Access Inference

When a property is accessed on a value whose type is a type variable (i.e. an
unannotated parameter), the system must:

1. Bind the type variable to an open `ObjectType` (`Open: true`) containing a
   `PropertyElem` with a fresh type variable for the value and a
   `RestSpreadElem` with a fresh row variable.
2. Return the fresh property type variable as the type of the member expression.
3. If additional properties are accessed on the same object, add new
   `PropertyElem`s to the already-bound open `ObjectType`. Since the type
   variable has been pruned to the `ObjectType`, subsequent accesses go through
   `getObjectAccess`. Currently, `getObjectAccess` returns an
   `UnknownPropertyError` when a property is not found. **It must be modified**
   to detect open `ObjectType`s (those with `Open: true`) and, instead of
   erroring, add a new `PropertyElem` with a fresh type variable and return it.

**Example — read access:**
```esc
fn foo(obj) {
    let x = obj.bar   // obj must have property `bar`
    let y = obj.baz   // obj must also have property `baz`
}
// inferred: fn foo(obj: {bar: t1, baz: t2}) -> void
```

**Example — write access (assignment):**
```esc
fn foo(obj) {
    obj.bar = "hello":string   // obj.bar: string
    obj.baz = 5:number         // obj.baz: number
}
// inferred: fn foo(obj: {bar: string, baz: number}) -> void
```

When a property is written to (assigned), the assigned value's type should unify
with the property's fresh type variable, giving it a concrete type.

**Example — multiple assignments to the same property:**
```esc
fn foo(obj) {
    obj.bar = "hello"
    // do something with obj.bar
    obj.bar = 5
    // do something else with obj.bar
}
// inferred: fn foo(obj: {bar: "hello" | 5}) -> void
```

**Example — same property in different branches:**
```esc
fn foo(obj, cond) {
    if cond {
        obj.bar = "hello"
    } else {
        obj.bar = 5
    }
}
// inferred: fn foo(obj: {bar: "hello" | 5}, cond: boolean) -> void
```

When the same property is assigned different types — whether sequentially in the
same flow or across branches — the inferred property type should be the **union**
of all assigned types. The property type must not get "stuck" as the type of the
first value assigned to it. Each assignment widens the property type to include
the new type.

With the chosen Option C approach (eagerly binding to an open `ObjectType`),
this works as follows:

1. The first assignment (`obj.bar = "hello"`) creates the property with a fresh
   type variable and unifies it with `"hello"`.
2. The second assignment (`obj.bar = 5`) finds the existing property and unifies
   its type variable with `5`.
3. Since the type variable is already bound to `"hello"` and now must also accept
   `5`, the type variable widens to `"hello" | 5`.

This requires the unifier to support **widening** a row-inferred property type
variable when it encounters a new incompatible concrete type: rather than
reporting a conflict, it should produce a union. See Section 6d for details.

### 2. Method Call Inference

When a method is called on a value whose type is a type variable, the system must:

1. Bind the type variable to an open `ObjectType` (if not already bound) and add
   a `MethodElem` with a `FuncType` containing fresh type variables for each
   parameter and the return type.
2. Unify the supplied argument types with the method's fresh parameter types.
3. Return the method's return type variable as the type of the call expression.

**Example:**
```esc
fn foo(obj) {
    let result = obj.process(42:number, "hello":string)
}
// inferred: fn foo(obj: {process: fn(number, string) -> t1}) -> void
```

If the same method is called multiple times with different argument types,
the parameter types widen to a union — the same widening rule as property types
(see Section 6d). For example:

```esc
fn foo(obj) {
    obj.process(42:number)
    obj.process("hello":string)
}
// inferred: fn foo(obj: {process: fn(number | string) -> t1}) -> void
```

Similarly, if the same method's return type is used in contexts expecting
different types, the return type widens to a union. For example:

```esc
fn foo(obj) {
    let x: number = obj.getValue()
    let y: string = obj.getValue()
}
// inferred: fn foo(obj: {getValue: fn() -> number | string}) -> void
```

Here, the first call unifies `getValue`'s return type variable with `number`,
and the second call widens it to `number | string` per Section 6d.

### 3. Array Indexing Inference

When a value whose type is a type variable is indexed with bracket notation,
the system must:

1. If the index is a numeric type: constrain the type variable to be an array-like
   type (i.e. `Array<T>` for a fresh `T`), and return `T`.
2. If the index is a string literal type: treat it as a property access (same as
   dot notation), constraining the type variable to have that property.
3. If the index is a string type (non-literal): constrain the type variable to
   have a string index signature, and return the index signature's value type.

**Example — numeric index:**
```esc
fn foo(obj) {
    let x = obj[0]    // obj is Array<t1> or has numeric index signature
}
```

**Example — string literal index:**
```esc
fn foo(obj) {
    let x = obj["bar"]   // equivalent to obj.bar
}
```

**Interaction with property access:** If the same parameter is used with both
property access (`obj.name`) and numeric indexing (`obj[0]`), this is an error —
an `ObjectType` is not an `Array`. The type variable would be bound to an open
`ObjectType` on the first property access, and the subsequent numeric index
access would fail because `ObjectType` doesn't support numeric indexing (unless
a numeric index signature is added). If only numeric indexing is used, the type
variable should be bound to `Array<T>` rather than an open `ObjectType`.

### 4. Optional Chaining

When optional chaining (`?.`) is used on a value whose type is a type variable,
the system must:

1. Bind the type variable to an open `ObjectType` with the accessed property
   (same as non-optional access via Option C), but also wrap the parameter's
   type as `T | null | undefined` where `T` is the open object type.
2. The inferred property type should be `T`, not `T | undefined` — the
   optionality is a feature of the access, not the property itself.
3. The resulting expression type should be `T | undefined` (as with current
   optional chaining semantics).
4. The inferred object type for the parameter should be
   `T | null | undefined` where `T` is the open object type with the inferred
   properties. If `obj` were just `{bar: t1}`, then `obj?.bar` would always
   succeed and the `?.` would be pointless — so the use of optional chaining is
   evidence that the parameter can be nullish.

**Example:**
```esc
fn foo(obj) {
    let x = obj?.bar
}
// inferred: fn foo(obj: {bar: t1} | null | undefined) -> void, x: t1 | undefined
```

**Chained optional access:** When optional chaining is nested (e.g. `a?.b?.c`),
each `?.` adds nullability to the accessed property's type. In `a?.b?.c`:

- `a` is inferred as `{b: T1} | null | undefined`
- `b`'s type `T1` is `{c: T2} | null | undefined` (because `?.` is used on `b`)
- `c`'s type `T2` is a fresh type variable
- The overall expression type is `T2 | undefined`

```esc
fn foo(a) {
    let x = a?.b?.c
}
// inferred: fn foo(a: {b: {c: t1} | null | undefined} | null | undefined) -> void
// x: t1 | undefined
```

### 5. Open vs. Closed Object Types

The inferred object type for an unannotated parameter should be **open** during
inference of the function body — the property set can grow as new usages are
encountered. Once the function body has been fully inferred, the parameter's
object type should be **closed** — its property set is finalized and no further
widening is allowed.

- Object types from **type annotations** remain **closed** (`Open: false`) by
  default, as they are today. (Exactness is a separate concern — see Definitions.)
- Object types from **inference** are **open** (`Open: true`) while the enclosing
  function body is being inferred. They also have a `RestSpreadElem` with a
  fresh row variable for row polymorphism. Their property set can grow as new
  usages are encountered because `Open` is `true`.
- **After inference completes** for the function body, open object types on the
  function's parameters are closed: `Open` is set to `false`, marking the
  property set as final. Separately, row variables whose `RestSpreadElem`s
  are visible in the function's return type are **not** removed — they are
  promoted to type parameters instead (see Section 11). Only `RestSpreadElem`s
  whose row variables do not appear in the return type are removed.
- Open types are **never** closed prematurely during inference. When an inferred
  open type is unified with a closed type during inference, the closed type's
  properties are added to the open type but `Open` remains `true` (see
  Section 6c). Closing only happens at the end of the function scope.
- When two open types are unified, their known properties are unified pairwise,
  and their row variables are merged.

### 6. Unification Changes

The unifier must be extended to handle the following cases:

#### 6a. Unifying row-inferred TypeVarTypes

An unannotated parameter starts as a bare `TypeVarType`. It may be constrained
by property access (Option C — eagerly binds to an open `ObjectType`), by being
passed to typed functions, or both. This section covers the function-call path.

**Already bound to an open ObjectType** (properties were accessed before the
call): unification proceeds as open-vs-closed (6c) or open-vs-open (6b).

**Not yet bound** (no properties accessed yet): the type variable is unified
with the parameter type via `bind()`. Since the parameter is unannotated, it
must remain open if the target is an `ObjectType`. When `bind()` encounters a
closed `ObjectType`, it should bind the type variable to an open `ObjectType`
(`Open: true`) with the same properties plus a `RestSpreadElem` with a fresh
row variable.

If the target is not an `ObjectType` (e.g. `number`, `string`), `bind()` binds
directly as usual — openness only applies to object types.

**Multiple function calls** compose via intersection. Each call constrains the
type variable further. When the type variable is already bound and a new
unification occurs, the result is the intersection of the existing type and the
new constraint:

```esc
fn foo(obj) {
    bar(obj)       // bar expects {x: number} — obj becomes {x: number, ...R1}
    baz(obj)       // baz expects {y: string} — obj becomes {x: number, ...R1} & {y: string, ...R2}
                   // which simplifies to {x: number, y: string, ...R}
    obj.z = true   // adds z to the open type
}
// inferred: fn foo(obj: {x: number, y: string, z: true}) -> void
```

When two open `ObjectType`s are intersected, their properties are merged and
their row variables unified (see 6b).

#### 6b. Multiple constraints on a type variable

When a type variable is constrained by multiple function calls, the constraints
compose via intersection. For open object types, this means merging properties
and row variables:

1. Unify all shared property names pairwise.
2. Properties present in one but not the other are added to the merged result.
3. The resulting type is still open (row variables merge).

When the intersection involves non-object types, normal intersection rules
apply:

```esc
fn foo(arg) {
    bar(arg)       // bar expects number — arg becomes number
    baz(arg)       // baz expects string — arg becomes number & string = never
    arg.z = true   // error: properties don't exist on never
}
```

#### 6c. Open object type with closed object type

When an open object type is unified with a closed object type during inference
(e.g. passing an inferred param to a typed function), the open type must **not**
be closed prematurely. Properties may still be added to it later in the function
body. Instead:

1. Unify all shared property names pairwise.
2. Properties present in the closed type but not the open type are **added** to
   the open `ObjectType`'s `Elems` list (the closed type's requirements become
   part of the inferred type).
3. Properties present in the open type but not the closed type are allowed
   (structural subtyping — the inferred type may have more properties than the
   closed type requires).
4. The type stays open (`Open` remains `true`) so that subsequent property
   accesses within the function body can still add new properties.

Closing only happens after the function body is fully inferred (see Section 5
and implementation step 5).

**Why this matters:** Without this rule, the order of operations within a
function body would affect inference results. For example:

```esc
fn foo(obj) {
    obj.z = true           // obj becomes {z: boolean, ...R}
    bar(obj)               // bar expects {x: number, y: string}
    obj.w = "hello"        // needs to add w — must still be open
}
// inferred: fn foo(obj: {z: boolean, x: number, y: string, w: string}) -> void
```

If 6c closed the type at step 2 (the `bar(obj)` call), step 3 would fail
because `w` couldn't be added. By keeping the type open, all three operations
contribute to the final inferred type regardless of order.

**Note:** The existing unifier code already handles `RestSpreadElem` by
collecting leftover properties and unifying them with the rest type variable.
The change needed is to check `Open` on the `ObjectType` — when it is `true`,
add the closed type's properties directly to the open `ObjectType`'s `Elems`
instead of binding the row variable.

#### 6d. Property type widening (union accumulation)

When the same element on an inferred open object type is used multiple times with
different types — whether sequentially or across branches — the element's type
must accommodate all observed types. The type variable receives multiple
unification demands and must not get "stuck" on the first one.

The unifier must handle this by **widening** the type to a union:

1. If a type variable has already been bound to a concrete type `A`, and a new
   unification attempts to bind it to an incompatible concrete type `B`, and
   the type variable originated from row-type inference (i.e. it is a type
   variable within an inferred open object), then instead of reporting an error,
   widen the binding to `A | B`.
2. Subsequent unifications with further types `C` widen to `A | B | C`, etc.
3. If the new type is already a member of the existing union, no widening is
   needed (e.g. assigning `"hello"` twice does not produce `"hello" | "hello"`).
4. This widening applies to **all** type variables within inferred open object
   types — including property value types, method parameter types, and method
   return types — but **not** to type variables in general. Conflicting
   unifications on ordinary type variables remain errors.

**Rationale:** An inferred type within an open object represents "what types
does this position need to support?" Every usage is evidence of a type that
position must handle. The result is the union of all such evidence. This is
fundamentally different from ordinary type variable unification, where a
conflict means a genuine type error.

**Implementation note:** One way to implement this is to mark type variables
created during row inference with a flag (e.g. on `TypeVarType`). When `bind()`
encounters a conflict on such a variable, it creates a union instead of
returning an error.

### 7. Constraint Representation

Property constraints on type variables need a representation. Three options were
considered. **Option C was chosen** as the approach we're going with.

**Option A — Constraint field on TypeVarType (not chosen):**
Extend `TypeVarType.Constraint` to support object-like constraints. When a
property is accessed, build up an `ObjectType` constraint on the type variable.
Subsequent property accesses add elements to this constraint object.

- Pro: Leverages existing `Constraint` field; unification already checks constraints.
- Con: The `Constraint` field currently holds a type used for generic bounds
  (`T extends Foo`). Overloading it for row constraints may conflate two
  different mechanisms.

**Option B — Separate row constraint tracking (not chosen):**
Introduce a dedicated data structure (e.g., a map from type variable ID to a list
of property constraints) maintained by the checker. When the type variable is
finally unified with a concrete type, the accumulated constraints are verified.

- Pro: Clean separation of concerns.
- Con: Additional bookkeeping outside the type variable itself.

**Option C — Eagerly build open ObjectType (chosen):**
When a property is accessed on a type variable, immediately bind the type variable
to an open `ObjectType` (`Open: true`) containing a `PropertyElem` with a fresh
type variable for the value and a `RestSpreadElem` with a fresh row variable.
Subsequent accesses on the same variable find the bound `ObjectType` and either
return the existing property's type or add a new `PropertyElem` (gated by `Open`).

When a method is called (property access followed by a call), a `MethodElem` is
added instead, with a `FuncType` containing fresh type variables for each
parameter and the return type. The supplied arguments are then unified with the
fresh parameter types.

- Pro: No new constraint mechanism needed — works within the existing type system.
  `getMemberType` naturally handles `ObjectType`.
- Con: Requires introducing the concept of "open" object types (via `Open` field
  and `RestSpreadElem`). Eagerly binding may complicate some unification scenarios.

**Decision:** Option C is the chosen approach. It is the most incremental path. It reuses the
existing `ObjectType` and `RestSpreadElem` structures, avoids a separate
constraint-tracking system, and makes property access on inferred types go through
the same `getObjectAccess` code path as annotated types. The `Open` field on
`ObjectType` controls whether new properties can be added, while `RestSpreadElem`
serves as the row variable for row polymorphism — the two concerns are orthogonal.

### 8. Integration with Existing Inference

#### 8a. Passing inferred-type params to typed functions

When a value with an inferred (open) object type is passed to a function with a
typed parameter, the open type should unify with the parameter's type:

```esc
fn bar(x: {bar: string}) -> string { return x.bar }

fn foo(obj) {
    let result = bar(obj)   // constrains obj to {bar: string, ...}
}
// inferred: fn foo(obj: {bar: string}) -> void
```

#### 8b. Return type inference

If an inferred-type param is returned from the function, the return type should
reflect the inferred open type. See 8g and Section 11 for details on how the
row variable connects input to output for callers.

#### 8c. Multiple parameters

Each unannotated parameter gets its own independent set of inferred constraints:

```esc
fn foo(a, b) {
    a.x = 1:number
    b.y = "hello":string
}
// inferred: fn foo(a: {x: number}, b: {y: string}) -> void
```

#### 8d. Aliasing and flow

If an unannotated parameter is assigned to a local variable and then properties
are accessed on that variable, the constraints should flow back to the parameter:

```esc
fn foo(obj) {
    let alias = obj
    alias.x = 1:number
}
// inferred: fn foo(obj: {x: number}) -> void
```

This should work naturally through type variable unification — `alias` gets the
same type variable as `obj`.

#### 8e. Nested property access

When a property is accessed on a property of an inferred object, the inner
property's fresh type variable undergoes the same Option C binding recursively:

```esc
fn foo(obj) {
    obj.foo.bar = 5:number
}
// inferred: fn foo(obj: {foo: {bar: number}}) -> void
```

This works because `obj.foo` returns a fresh type variable (the value type of
property `foo`), and then accessing `.bar` on that type variable triggers
Option C again, binding it to a new open `ObjectType` with property `bar`.

#### 8f. Destructuring patterns on params

Destructuring patterns already infer structural object types from the pattern
shape (e.g. `fn foo({bar, baz})` infers `obj: {bar: t1, baz: t2}`). Destructured
parameters without a rest element produce **closed** object types — the pattern
fully specifies the expected properties:

```esc
fn foo({bar, baz}) {
    // bar and baz are available as bindings
}
// inferred: fn foo({bar, baz}: {bar: t1, baz: t2}) -> void
```

When the destructuring pattern includes a **rest element**, the parameter type
should have a `RestSpreadElem` with a row variable — the rest element captures
additional properties beyond those explicitly named. However, the type remains
**closed** (`Open: false`) because the pattern fully specifies the explicit
properties and the parameter itself is not accessible by name (only the bindings
are), so no new explicit properties can be added during inference. This is the
`Open: false` + `RestSpreadElem` case — a natural example of the orthogonality
between openness and row variables.

```esc
fn foo({bar, ...rest}) {
    // bar is a binding, rest captures remaining properties
}
// inferred: fn foo({bar, ...rest}: {bar: t1, ...R}) -> void
// where R is a fresh type variable constrained to be an ObjectType
// rest has type R
// the ObjectType has Open: false (explicit props are fixed) but has a
// RestSpreadElem for R (row polymorphism for extra properties)
```

**Mechanism:** `inferPattern` creates a closed `ObjectType` from the pattern. If
the pattern has a rest element and the parameter lacks a type annotation,
`inferFuncParams` should add a `RestSpreadElem` with a fresh row variable `R`
to the pattern-inferred `ObjectType`, but leave `Open` as `false`. The rest
binding's type is `R`, connecting it to the row variable so that when `R` is
resolved, the rest binding's type reflects the remaining properties. Without a
rest element, the type stays closed with no `RestSpreadElem`.

#### 8g. Return type row variable propagation

When an inferred-type param is returned from the function, the return type
should preserve extra properties that callers pass in. This requires **row
polymorphism** — the function's type must be generic over the row variable so
that callers can instantiate it with their specific extra properties:

```esc
fn foo(obj) {
    obj.x = 1:number
    return obj
}

let result = foo({x: 1, y: 2})
// result type: {x: number, y: number}
```

Without row polymorphism, the function's closed signature would be
`fn foo(obj: {x: number}) -> {x: number}`, and `y` would be lost in the return
type. See Section 11 for how row polymorphism makes this work.

### 9. Error Reporting

When inference fails, error messages should:

1. Identify which parameter's type could not be inferred.
2. Show the relevant usage sites.
3. Suggest adding a type annotation to resolve the ambiguity.

Note: assigning different types to the same property — whether sequentially or
across branches — is not an error. The property type widens to a union (see
Section 6d).

The following are concrete error scenarios with suggested message content.
These are follow-up/implementation details for user-facing diagnostics.

#### 9a. Missing property at call site

When a caller passes an object that is missing a property required by the
inferred type (at the call site, the parameter type is closed per Section 5,
so this is standard closed-vs-closed unification):

```esc
fn foo(obj) {
    obj.bar = "hello"
    obj.baz = 5
}
foo({bar: "hi"})   // error: property `baz` is missing
```

Suggested message:
> Argument of type `{bar: "hi"}` is missing property `baz` required by
> parameter `obj` of `foo`. Property `baz` is required because it is
> assigned at <source location>.

Message elements: (1) identifies parameter `obj`, (2) shows the usage site
where `baz` was assigned, (3) the type mismatch is clear enough that an
annotation suggestion is not needed here.

#### 9b. Numeric indexing vs. property access conflict

When the same parameter is used with both property access and numeric indexing
(Section 3):

```esc
fn foo(obj) {
    let name = obj.name
    let first = obj[0]      // error
}
```

Suggested message:
> Cannot index parameter `obj` with a numeric index because it was already
> constrained to an object type by property access `obj.name` at
> <source location>. Consider adding a type annotation to `obj`.

Message elements: (1) identifies parameter `obj`, (2) shows the conflicting
usage sites (property access and numeric index), (3) suggests adding an
explicit type annotation.

#### 9c. Open-to-closed unification property type mismatch

When an inferred property type is incompatible with the corresponding property
in a closed type during unification (Section 6c):

```esc
fn foo(obj) {
    obj.bar = 5
}
foo({bar: "hi"})   // error: number is not assignable to string
```

Suggested message:
> Type `number` is not assignable to type `string` for property `bar` of
> parameter `obj` in `foo`. Property `bar` was inferred as `number` from
> assignment at <source location>.

Message elements: (1) identifies parameter `obj` and property `bar`,
(2) shows the inference site, (3) the mismatch is self-explanatory — no
annotation suggestion needed.

#### 9d. When to suggest explicit annotations

Error messages should suggest adding a type annotation when the conflict arises
from the inference mechanism itself (not from a straightforward type mismatch).
Specifically:
- **Suggest annotation**: numeric-indexing-vs-property-access conflicts (9b),
  and any case where the inferred type is surprising or non-obvious to the user.
- **Don't suggest annotation**: missing properties (9a) or simple type
  mismatches (9c), where the fix is to change the call site argument rather than
  annotate the parameter.

### 10. Scope and Limitations

The following are explicitly **out of scope** for the initial implementation:

- **Recursive object types**: Inferring that `obj.child` has the same type as
  `obj` (self-referential structures). These require explicit annotations.
- **Conditional property access**: Inferring that a property exists only in
  certain branches (e.g. `if (cond) { obj.x }` should not require `x` if
  `cond` is false). All accessed properties are required unconditionally.

These can be addressed in follow-up work.

### 11. Row Polymorphism

Row polymorphism allows functions with inferred parameter types to preserve
extra properties through their return types. Without it, a function like:

```esc
fn foo(obj) {
    obj.x = 1:number
    return obj
}
```

would have the closed signature `fn foo(obj: {x: number}) -> {x: number}`,
and calling `foo({x: 1, y: 2})` would return `{x: number}` — losing `y`.

#### 11a. Row-polymorphic function types

When a function has an inferred parameter type that is returned (or part of the
return type), the function should be **generic over the row variable**. Instead
of closing the row variable after inference, it is preserved as a type parameter
in the function's type:

```esc
fn foo(obj) {
    obj.x = 1:number
    return obj
}
// type: fn foo<R>(obj: {x: number, ...R}) -> {x: number, ...R}
```

The row variable `R` appears in both the parameter and return type, connecting
them. When the function is called, `R` is instantiated with the caller's extra
properties:

```esc
let result = foo({x: 1, y: 2})
// R = {y: number}, so result: {x: number, y: number}
```

#### 11b. When to preserve row variables

Not all inferred parameters need row polymorphism. A row variable should be
preserved (not closed) only when it appears in a position visible to callers —
specifically:

1. **Return type**: If the parameter (or a property derived from it) is returned,
   the row variable must appear in the return type.
2. **Output parameters**: If the parameter is mutated and the mutations are
   visible to the caller (e.g. the object is passed by reference), the row
   variable should be preserved so callers know their extra properties are
   retained.

If the parameter's row variable does NOT appear in the return type or any other
externally visible position, it can be closed after inference (the caller's
extra properties are accepted but not tracked).

#### 11c. Implementation sketch

1. **After inferring the function body**, set `Open` to `false` on all inferred
   `ObjectType`s — the property set is now final.
2. Identify which row variables (from `RestSpreadElem`s on inferred `ObjectType`s)
   appear in the return type.
3. For those that do: promote the row variable to a **type parameter** on the
   function. The `RestSpreadElem` remains in both the parameter type and the
   return type, referencing the same type variable.
4. For those that don't: remove the `RestSpreadElem` (the row variable is not
   needed since it doesn't escape to callers).
5. **At call sites**, when instantiating a row-polymorphic function:
   - Create a fresh type variable for the row parameter.
   - Unify the argument with the parameter type. The row variable binds to an
     `ObjectType` containing the caller's extra properties.
   - The return type, sharing the same row variable, reflects the extra
     properties.

This extends the existing generic function instantiation mechanism
(`handleFuncCall`) — row type parameters are instantiated the same way as
regular type parameters, with fresh type variables that get solved during
argument unification.

#### 11d. Interaction with Section 5 (closing)

The closing rule from Section 5 is refined:

- All inferred `ObjectType`s are closed (`Open` set to `false`) after inference.
- Row variables that appear in the function's return type are preserved — their
  `RestSpreadElem`s remain, and they become type parameters.
- Row variables that do NOT appear in the return type have their
  `RestSpreadElem`s removed after inference completes.

#### 11e. Limitations

Row polymorphism adds complexity. The following are initially out of scope:

- **Explicit row type parameters**: The syntax `fn foo<R>(obj: {x: number, ...R})`
  already parses since rest spreads are supported in object type annotations.
  However, `R` would need a type constraint indicating it is an object type
  (e.g. `R extends {}` or similar). Explicit row type parameters are not
  required for the initial implementation since row polymorphism is inferred,
  but the syntactic support is already in place for future use.
- **Row variables in generic type arguments**: Passing `{x: number, ...R}` as a
  type argument to a generic type like `Promise<{x: number, ...R}>`. This
  requires row variables to flow through generic instantiation.

Note: **multiple row variables** (functions where multiple parameters each have
their own row variable that appears in the return type) are **in scope**. This
is needed for patterns like `fn merge(a, b) { return {...a, ...b} }` (see
Section 12).

### 12. Object Spread and Multiple RestSpreadElems

Object spread expressions (`{...obj, extra: 1}`) are parsed but not yet handled
by the checker. Additionally, the unifier currently rejects the case where both
sides of a unification have `RestSpreadElem`s. This section addresses both gaps.

#### 12a. Handling ObjSpreadExpr in ObjectExpr inference

The `ObjectExpr` inference code must handle `ObjSpreadExpr` elements. When
inferring `{...a, x: 1, ...b, y: 2}`:

1. Infer the type of each spread source (`a`, `b`).
2. Create the resulting `ObjectType` with explicit `PropertyElem`s for the
   non-spread elements (`x`, `y`) and a `RestSpreadElem` for each spread source.
3. The resulting type is: `{x: number, y: number, ...typeof a, ...typeof b}`.

**Example with inferred types:**
```esc
fn foo(obj) {
    let extended = {...obj, extra: 1}
}
// obj is open: {...R}
// extended type: {extra: number, ...R}
```

The spread of an open type propagates the row variable into the new object's
type, connecting the two.

**Example with multiple spreads:**
```esc
fn merge(a, b) {
    return {...a, ...b}
}
// inferred: fn merge<R1, R2>(a: {...R1}, b: {...R2}) -> {...R1, ...R2}
```

#### 12b. Multiple RestSpreadElems in ObjectType

Currently, `ObjectType.Elems` can contain at most one `RestSpreadElem`, and the
unifier errors on the two-rest-elem case. To support multiple spreads:

1. Allow `ObjectType.Elems` to contain **multiple** `RestSpreadElem`s, each
   representing a distinct spread source.
2. During unification, when an `ObjectType` with multiple rest elements is
   unified with a concrete type, each rest element contributes its properties.
   The known properties are unified first, then the remaining properties must
   be distributed across the rest elements.

**Implementation note:** Multiple `RestSpreadElem`s should remain as separate
entries in `ObjectType.Elems` in the internal representation — they are not
eagerly merged when the `ObjectType` is constructed. Merging (flattening the
`RestSpreadElem`s into explicit properties) is performed only when the
`ObjectType` must be expanded, such as during unification (Section 12c),
compatibility checking, or when producing a concrete/displayable type. At
expansion time, `RestSpreadElem`s are merged left-to-right so that properties
from later rest elements override earlier ones (see Section 12d for override
semantics).

#### 12c. Unification with multiple rest elements

When unifying `{x: number, ...R1, ...R2}` with `{x: number, y: string, z: boolean}`:

1. Unify shared explicit properties pairwise (`x: number`).
2. The remaining properties (`y: string, z: boolean`) must come from `R1`, `R2`,
   or some combination. If one rest element is already bound, subtract its
   properties and assign the remainder to the other. If both are unbound, this
   is ambiguous.

**Ambiguity resolution:** When multiple rest elements are unbound and there are
remaining properties to distribute, this is an error — the system cannot
determine which rest element should receive which properties. In practice, this
case is rare because at least one spread source typically has a known type.

When one rest element is already bound (e.g. `R1 = {y: string}`):
1. Subtract `R1`'s properties from the remaining set.
2. Bind `R2` to the leftover properties (`{z: boolean}`).

When both are already bound:
1. Merge all properties from both rest elements with the explicit properties.
2. Unify the merged result with the target type.

#### 12d. Property override semantics

In JavaScript/TypeScript, later spreads override earlier ones for shared
property names. The same should apply here:

```esc
let result = {...a, ...b}
```

If both `a` and `b` have property `x`, the result's `x` comes from `b`. During
type inference, the rightmost definition of a property wins. When building the
`ObjectType` from the `ObjectExpr`, properties from later elements shadow
properties from earlier elements (including from earlier spreads).

#### 12e. Implementation notes

- **Add `ObjSpreadExpr` case** to `ObjectExpr` inference in `infer_expr.go`:
  infer the spread source type and add a `RestSpreadElem` to the result's
  `Elems`.
- **Update the unifier** to handle the two-rest-elem case instead of returning
  `UnimplementedError`. Implement the distribution logic from 12c.
- **Update `getObjectAccess`** to look through `RestSpreadElem`s when searching
  for a property — if the property isn't found in the explicit elements, check
  each rest element's type. Note: adding new properties via `getObjectAccess`
  is gated by the `Open` field, not by the presence of `RestSpreadElem`s.

## Implementation Approach (Summary)

1. **Extend `getMemberType`** to handle `TypeVarType` with two branches:
   - **Numeric index access** (the key is an `IndexKey` with a numeric type):
     bind the `TypeVarType` to `Array<T>` for a fresh `T`, and return `T`.
     This is the "numeric-first indexing" path (see Section 3, case 1).
   - **Property/method access or string-literal index** (the key is a
     `PropertyKey`, or an `IndexKey` with a string literal type): bind the
     `TypeVarType` to an open `ObjectType` (`Open: true`) containing a
     `PropertyElem` with a fresh type variable for the value and a
     `RestSpreadElem` row variable, then delegate to `getObjectAccess`
     (see Section 3, case 2).
   - **Non-literal string index** (the key is an `IndexKey` with a `string`
     type): bind the `TypeVarType` to an open `ObjectType` (`Open: true`) with
     a string index signature and a `RestSpreadElem` row variable, and return
     the index signature's value type (see Section 3, case 3).

2. **Modify `getObjectAccess`** to handle open `ObjectType`s: when a property is
   not found and the `ObjectType` has `Open: true`, add a new `PropertyElem`
   with a fresh type variable instead of returning `UnknownPropertyError`.

3. **Add `Open` field to `ObjectType`**: Use `Open: true` to indicate the type
   can gain new properties during inference. This is orthogonal to the presence
   of `RestSpreadElem` (which represents row variables for row polymorphism or
   spread sources). Adjust unification to handle open-vs-closed and open-vs-open
   cases based on the `Open` field.

4. **Adjust unification for open objects**: When unifying an open object with a
   closed object during inference, add the closed type's properties to the open
   type's `Elems` without setting `Open` to `false` (see 6c). When unifying two
   open objects, merge properties and row variables.

5. **Adjust `bind()` for row-inferred type variables**: When binding a
   row-inferred `TypeVarType` to a closed `ObjectType`, bind to an open
   `ObjectType` (`Open: true`) with the same properties plus a
   `RestSpreadElem`. When the type variable is already bound, compose via
   intersection — merging properties for open object types, or reducing to
   `never` for incompatible non-object types (see 6a).

6. **Update `inferFuncParams`**: No changes needed — it already creates a fresh
   type variable for unannotated params. The new `getMemberType` behavior will
   naturally constrain these type variables as the function body is inferred.

7. **Close and clean up after function body inference**: After inferring a
   function body: (a) set `Open` to `false` on all inferred `ObjectType`s,
   (b) identify which row variables (from `RestSpreadElem`s) appear in the
   return type and promote those to type parameters on the function (row
   polymorphism — see Section 11), (c) remove `RestSpreadElem`s whose row
   variables do not appear in the return type.

8. **Handle row-polymorphic calls**: Extend `handleFuncCall` to instantiate row
   type parameters with fresh type variables, solved during argument
   unification. Extra properties from the caller flow through to the return type.

9. **Handle object spread expressions**: Add `ObjSpreadExpr` handling to
   `ObjectExpr` inference, support multiple `RestSpreadElem`s in `ObjectType`,
   and update the unifier to distribute properties across multiple rest elements
   (see Section 12).

10. **Add tests**: Cover property access, method calls, array indexing, optional
    chaining, passing to typed functions, widening, closing after inference, row
    polymorphism (return type propagation), object spread, and error cases.
