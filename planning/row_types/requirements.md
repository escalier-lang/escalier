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

```
fn foo(obj) {
    obj.bar = "hello":string
    obj.baz = 5:number
}
// inferred: fn foo(obj: {bar: string, baz: number}) -> void
```

## Definitions

- **Row variable**: A type variable that represents the "rest" of an object type's
  properties. Analogous to a regular type variable, but unifies with sets of
  object properties rather than whole types.
- **Open object type**: An object type that contains a row variable, meaning it may
  have additional properties beyond those explicitly listed.
  e.g. `{bar: string, baz: number, ...R}` where `R` is a row variable.
- **Closed object type**: An object type with no row variable — its set of
  properties is fully known. All object types written by programmers in type
  annotations are closed. Note: open/closed is orthogonal to **exactness**
  (`Exact` field on `ObjectType`). Exactness controls whether an object type
  can unify with another that has additional properties. Open/closed controls
  whether an object type's property set can be **widened during inference**
  within a function body.
- **Row constraint**: A constraint recorded against a type variable (or row variable)
  that requires it to have certain properties. Accumulated during inference and
  resolved during unification.

## Requirements

### 1. Property Access Inference

When a property is accessed on a value whose type is a type variable (i.e. an
unannotated parameter), the system must:

1. Bind the type variable to an open `ObjectType` containing a `PropertyElem`
   with a fresh type variable for the value and a `RestSpreadElem` with a fresh
   row variable.
2. Return the fresh property type variable as the type of the member expression.
3. If additional properties are accessed on the same object, add new
   `PropertyElem`s to the already-bound open `ObjectType` (since the type
   variable has been pruned to the `ObjectType`, subsequent accesses go through
   `getObjectAccess` which finds existing properties or triggers further
   additions).

**Example — read access:**
```
fn foo(obj) {
    let x = obj.bar   // obj must have property `bar`
    let y = obj.baz   // obj must also have property `baz`
}
// inferred: fn foo(obj: {bar: t1, baz: t2}) -> void
```

**Example — write access (assignment):**
```
fn foo(obj) {
    obj.bar = "hello":string   // obj.bar: string
    obj.baz = 5:number         // obj.baz: number
}
// inferred: fn foo(obj: {bar: string, baz: number}) -> void
```

When a property is written to (assigned), the assigned value's type should unify
with the property's fresh type variable, giving it a concrete type.

**Example — multiple assignments to the same property:**
```
fn foo(obj) {
    obj.bar = "hello"
    // do something with obj.bar
    obj.bar = 5
    // do something else with obj.bar
}
// inferred: fn foo(obj: {bar: "hello" | 5}) -> void
```

**Example — same property in different branches:**
```
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
```
fn foo(obj) {
    let result = obj.process(42:number, "hello":string)
}
// inferred: fn foo(obj: {process: fn(number, string) -> t1}) -> void
```

If the same method is called multiple times with different argument types,
the parameter types widen to a union — the same widening rule as property types
(see Section 6d). For example:

```
fn foo(obj) {
    obj.process(42:number)
    obj.process("hello":string)
}
// inferred: fn foo(obj: {process: fn(number | string) -> t1}) -> void
```

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
```
fn foo(obj) {
    let x = obj[0]    // obj is Array<t1> or has numeric index signature
}
```

**Example — string literal index:**
```
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

1. Record the same property/method constraint as non-optional access.
2. The inferred property type in the constraint should be `T`, not `T | undefined`
   — the optionality is a feature of the access, not the property itself.
3. The resulting expression type should be `T | undefined` (as with current
   optional chaining semantics).
4. The inferred object type for the parameter should be
   `T | null | undefined` where `T` is the open object type with the inferred
   properties. If `obj` were just `{bar: t1}`, then `obj?.bar` would always
   succeed and the `?.` would be pointless — so the use of optional chaining is
   evidence that the parameter can be nullish.

**Example:**
```
fn foo(obj) {
    let x = obj?.bar
}
// inferred: fn foo(obj: {bar: t1} | null | undefined) -> void, x: t1 | undefined
```

### 5. Open vs. Closed Object Types

The inferred object type for an unannotated parameter should be **open** during
inference of the function body — the property set can grow as new usages are
encountered. Once the function body has been fully inferred, the parameter's
object type should be **closed** — its property set is finalized and no further
widening is allowed.

- Object types from **type annotations** remain **closed** by default, as they
  are today. (Exactness is a separate concern — see Definitions.)
- Object types from **inference** are **open** while the enclosing function body
  is being inferred. They have an implicit row variable (`RestSpreadElem`)
  representing additional unknown properties, and their property set can grow
  as new usages are encountered.
- **After inference completes** for the function body, all open object types on
  the function's parameters are closed: the `RestSpreadElem` is removed, marking
  the property set as final. From the perspective of callers, the function's
  parameter types are fully known closed types.
- When an inferred open type is unified with a closed type during inference, the
  open type's row variable is resolved to "empty" (no additional properties),
  effectively closing it early.
- When two open types are unified, their known properties are unified pairwise,
  and their row variables are merged.

### 6. Unification Changes

The unifier must be extended to handle the following cases:

#### 6a. TypeVarType bound to open ObjectType

With Option C, accessing a property on a `TypeVarType` eagerly binds it to an
open `ObjectType`. From that point on, the type variable prunes to the
`ObjectType`, so subsequent unification is between `ObjectType`s — covered by
6b and 6c below.

The key implication: when an unannotated parameter is passed to a function with a
typed parameter (e.g. `bar(obj)` where `bar` expects `{bar: string}`), the type
variable may or may not already be bound to an open `ObjectType`:

- **Already bound** (properties were accessed before the call): unification
  proceeds as open-vs-closed (6c) or open-vs-open (6b).
- **Not yet bound** (no properties accessed yet): the type variable unifies
  directly with the parameter type, binding it to that type. If properties are
  accessed later, they must be compatible with the now-bound type.

#### 6b. Open object type with open object type

When unifying two open object types:

1. Unify all shared property names pairwise.
2. Properties present in one but not the other are added to the merged result.
3. The resulting type is still open (row variables merge).

#### 6c. Open object type with closed object type

1. All properties required by the open type must exist in the closed type.
2. The closed type may have additional properties.
3. Unify matching property types.
4. The row variable in the open type is resolved (bound to empty).

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
to an open `ObjectType` containing a `PropertyElem` with a fresh type variable for
the value and a `RestSpreadElem` with a fresh row variable. Subsequent accesses on
the same variable find the bound `ObjectType` and either return the existing
property's type or add a new `PropertyElem`.

When a method is called (property access followed by a call), a `MethodElem` is
added instead, with a `FuncType` containing fresh type variables for each
parameter and the return type. The supplied arguments are then unified with the
fresh parameter types.

- Pro: No new constraint mechanism needed — works within the existing type system.
  `getMemberType` naturally handles `ObjectType`.
- Con: Requires introducing the concept of "open" object types via `RestSpreadElem`
  (or similar). Eagerly binding may complicate some unification scenarios.

**Decision:** Option C is the chosen approach. It is the most incremental path. It reuses the
existing `ObjectType` and `RestSpreadElem` structures, avoids a separate
constraint-tracking system, and makes property access on inferred types go through
the same `getObjectAccess` code path as annotated types. The `RestSpreadElem`
already exists in the type system and can serve as the row variable.

### 8. Integration with Existing Inference

#### 8a. Passing inferred-type params to typed functions

When a value with an inferred (open) object type is passed to a function with a
typed parameter, the open type should unify with the parameter's type:

```
fn bar(x: {bar: string}) -> string { return x.bar }

fn foo(obj) {
    let result = bar(obj)   // constrains obj to {bar: string, ...}
}
// inferred: fn foo(obj: {bar: string}) -> void
```

#### 8b. Return type inference

If an inferred-type param is returned from the function, the return type should
reflect the inferred open type. See 8g for details on how the row variable
connects input to output for callers.

#### 8c. Multiple parameters

Each unannotated parameter gets its own independent set of inferred constraints:

```
fn foo(a, b) {
    a.x = 1:number
    b.y = "hello":string
}
// inferred: fn foo(a: {x: number}, b: {y: string}) -> void
```

#### 8d. Aliasing and flow

If an unannotated parameter is assigned to a local variable and then properties
are accessed on that variable, the constraints should flow back to the parameter:

```
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

```
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
shape (e.g. `fn foo({bar, baz})` infers `obj: {bar: t1, baz: t2}`). With row
types, destructured parameters should produce **open** object types, just like
non-destructured parameters that have properties accessed on them. This allows
callers to pass objects with additional properties beyond those destructured.

```
fn foo({bar, baz}) {
    // bar and baz are available as bindings
}
// inferred: fn foo({bar, baz}: {bar: t1, baz: t2, ...}) -> void
```

#### 8g. Return type row variable propagation

When an inferred-type param is returned from the function, the return type
preserves the open object type including its row variable. This means the row
variable connects the input to the output — callers see that extra properties
are preserved through the function:

```
fn foo(obj) {
    obj.x = 1:number
    return obj
}

let result = foo({x: 1, y: 2})
// result type: {x: number, y: number}
```

The row variable in `obj`'s type is unified with the extra properties from the
call site argument, and that same row variable appears in the return type,
so `result` retains the `y` property.

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
inferred type (Section 6c — open unified with closed):

```
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

```
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

```
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
- **Higher-order row polymorphism**: Passing open object types through generic
  type parameters while preserving the row variable.
- **Conditional property access**: Inferring that a property exists only in
  certain branches (e.g. `if (cond) { obj.x }` should not require `x` if
  `cond` is false). All accessed properties are required unconditionally.
- **Spread into inferred types**: Using `{...obj, extra: 1}` to extend an
  inferred type.

These can be addressed in follow-up work.

## Implementation Approach (Summary)

1. **Extend `getMemberType`** to handle `TypeVarType`: when a property/method/index
   is accessed on a type variable, bind it to an open `ObjectType` with the
   accessed property and a `RestSpreadElem` row variable, then delegate to
   `getObjectAccess`.

2. **Introduce open object type semantics**: Use the presence of a `RestSpreadElem`
   in an `ObjectType.Elems` to indicate the type is open. Adjust unification to
   handle open-vs-closed and open-vs-open cases.

3. **Adjust unification for open objects**: When unifying an open object with a
   closed object, verify all required properties exist and bind the row variable.
   When unifying two open objects, merge properties and row variables.

4. **Update `inferFuncParams`**: No changes needed — it already creates a fresh
   type variable for unannotated params. The new `getMemberType` behavior will
   naturally constrain these type variables as the function body is inferred.

5. **Close open types after function body inference**: After inferring a function
   body, walk the function's parameter types and remove any remaining
   `RestSpreadElem` entries from inferred open `ObjectType`s. This finalizes the
   parameter types so callers see closed types with a fixed set of properties.

6. **Add tests**: Cover property access, method calls, array indexing, optional
   chaining, passing to typed functions, widening, closing after inference, and
   error cases.
