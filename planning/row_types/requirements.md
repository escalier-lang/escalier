# Row Types: Requirements for Structural Object Type Inference

## Motivation

Currently, all function parameters in Escalier must have explicit type annotations.
When a parameter lacks an annotation, the checker assigns a fresh type variable but
cannot refine it through property access, method calls, or indexing within the
function body. This means property access on an unannotated parameter produces an
`ExpectedObjectError` because `getMemberType` has no handler for `TypeVarType`.

> **Note (updated 2026-04-06):** Several foundational features have been merged
> from `main` that overlap with or support the row types work:
>
> - **Function generalization** (#379): `GeneralizeFuncType` in `generalize.go`
>   collects unresolved type vars in a function's signature and promotes them to
>   type parameters. This provides the infrastructure needed for Section 11 (row
>   polymorphism) — row variables that escape to the return type can be promoted
>   using this same mechanism.
> - **Callback inference** (#380): When a `TypeVarType` is called as a function,
>   the checker now creates a synthetic `FuncType` and defers resolution via
>   `resolveCallSites`. This partially addresses Section 2 (method call inference)
>   for the case where callback parameters are called directly. However, Section 2
>   specifically covers *method* calls on inferred objects (`obj.process(42)`),
>   which requires the property access path (Phase 2) to be in place first.
> - **`deepCloneType`** (#382): A probe-then-commit pattern for union/intersection
>   unification now avoids partial TypeVar mutation on failure. This infrastructure
>   is useful for the widening logic in Section 6d.
> - **`throws never` semantics** (#384): Missing `throws` clause now means
>   `throws never` instead of a fresh TypeVar. This simplifies FuncType creation
>   throughout the row types implementation (synthetic FuncTypes should use
>   `NewNeverType(nil)` for throws).

Row types (also known as row polymorphism) enable the type system to infer
structural object types from usage. When a function accesses `obj.bar` and
`obj.baz`, the system should infer that `obj` has at least those properties —
without requiring the programmer to spell out the type.

```esc
fn foo(obj) {
    obj.bar = "hello"
    obj.baz = 5
}
// inferred: fn foo(obj: mut {bar: string, baz: number}) -> void
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

### 1. Property Access Inference ✅

> **Status (2026-04-06):** Implemented. See Phase 2 in
> [implementation_plan.md](implementation_plan.md) for details.

When a property is accessed on a value whose type is a type variable (i.e. an
unannotated parameter), the system must:

1. Bind the type variable to an open `ObjectType` (`Open: true`) containing a
   `PropertyElem` with a fresh widenable type variable for the value and a
   `RestSpreadElem` with a fresh row variable.
   ~~The `ObjectType` is wrapped in `MutabilityType{Uncertain}`.~~
   **Implemented approach:** The open `ObjectType` is bound directly to the
   type variable (no `MutabilityType` wrapper). The assignment handler bypasses
   the immutability check for open objects and marks the `PropertyElem.Written`
   flag instead. Mutability is resolved during closing (Phase 6).
2. Return the fresh property type variable as the type of the member expression.
3. If additional properties are accessed on the same object, add new
   `PropertyElem`s to the already-bound open `ObjectType`. Since the type
   variable has been pruned to the `ObjectType`, subsequent accesses go through
   `getObjectAccess`, which detects open `ObjectType`s (via `Open: true`) and
   adds a new `PropertyElem` with a fresh type variable instead of erroring.

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
    obj.bar = "hello"   // obj.bar: string  (literal widened per 6e)
    obj.baz = 5         // obj.baz: number  (literal widened per 6e)
}
// inferred: fn foo(obj: mut {bar: string, baz: number}) -> void
```

When a property is written to (assigned), the assigned value's type is widened
from a literal to its primitive type (see Section 6e) and unified with the
property's fresh type variable, giving it a concrete type.

**Example — multiple assignments to the same property:**
```esc
fn foo(obj) {
    obj.bar = "hello"
    // do something with obj.bar
    obj.bar = 5
    // do something else with obj.bar
}
// inferred: fn foo(obj: mut {bar: string | number}) -> void
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
// inferred: fn foo(obj: mut {bar: string | number}, cond: boolean) -> void
```

When the same property is assigned different types — whether sequentially in the
same flow or across branches — the inferred property type should be the **union**
of all assigned types. The property type must not get "stuck" as the type of the
first value assigned to it. Each assignment widens the property type to include
the new type.

With the chosen Option C approach (eagerly binding to an open `ObjectType`),
this works as follows:

1. The first assignment (`obj.bar = "hello"`) creates the property with a fresh
   widenable type variable and unifies it with `"hello"`. The literal is widened
   to `string` per Section 6e, so the type variable is bound to `string`.
2. The second assignment (`obj.bar = 5`) finds the existing property and unifies
   its type variable with `5`. The literal `5` is widened to `number`.
3. Since the type variable is already bound to `string` and now must also accept
   `number`, the type variable widens to `string | number` per Section 6d.

This requires the unifier to support **widening** a widenable property type
variable when it encounters a new incompatible concrete type: rather than
reporting a conflict, it should produce a union. See Section 6d for details.

### 2. Method Call Inference ✅

> **Status (2026-04-08):** Implemented in Phase 5. See
> [implementation_plan.md](implementation_plan.md) for details.

When a method is called on a value whose type is a type variable, the system must:

1. Bind the type variable to an open `ObjectType` (if not already bound) and add
   a `PropertyElem` whose value is a fresh type variable. When the property is
   called, bind the type variable to a `FuncType` containing fresh type
   variables for each parameter and the return type. (Using `PropertyElem`
   with a `FuncType` value rather than `MethodElem` is simpler — the property's
   type variable naturally gets bound to the `FuncType` via the call.)
2. Strip uncertain mutability (`mut?`) from argument types and unify them with
   the method's fresh parameter types. Literal types are preserved (not widened
   to primitives), matching callback inference behavior.
3. Return the method's return type variable as the type of the call expression.

> **Implementation notes:** Method call inference uses the same `TypeVarType`
> case in `inferCallExpr` as callback inference (#380). The only change from
> the original callback inference code is that uncertain mutability (`mut?`) is
> stripped from argument types via `unwrapMutability` before unification, so
> inferred param types are clean (e.g. `fn(42)` not `fn(mut? 42)`). This
> applies uniformly to both method calls and callback calls.
>
> A synthetic `FuncType` is created and appended to `CallSites` for deferred
> resolution. This allows multiple calls with different arg types to accumulate
> (e.g. `obj.process(42); obj.process("hello")`); `resolveCallSites` later
> merges or intersects them and binds the TypeVar.
>
> Multiple calls with different arg types produce an **intersection**
> (overloaded signature) rather than widening to a union. This preserves
> per-call-site return type precision — each overload can have its own return
> type. Method params are contravariant, so the intersection form
> `fn(42) -> T1 & fn("hello") -> T2` correctly models "must handle being
> called with both types" while allowing different return types per call site.

**Example:**
```esc
fn foo(obj) {
    let result = obj.process(42, "hello")
}
// inferred: fn foo(obj: {process: fn(number, string) -> t1}) -> void
```

If the same method is called multiple times with different argument types,
the method's type becomes an **intersection** (overloaded signature) rather
than widening to a union. This preserves per-call-site return type precision:

```esc
fn foo(obj) {
    obj.process(42)
    obj.process("hello")
}
// inferred: fn foo(obj: {process: fn(number) -> t1 & fn(string) -> t2}) -> void
```

Similarly, if the same method's return type is used in contexts expecting
different types, the return type is captured per-overload:

```esc
fn foo(obj) {
    let x: number = obj.getValue()
    let y: string = obj.getValue()
}
// inferred: fn foo(obj: {getValue: fn() -> number & fn() -> string}) -> void
```

Here, each call creates a separate synthetic `FuncType` with its own return
type. `resolveCallSites` produces an intersection, preserving the distinct
return types `number` and `string`.

### 3. Array Indexing Inference ✅

> **Status (2026-04-06):** Implemented as part of Phase 2.

When a value whose type is a type variable is indexed with bracket notation,
the system must:

1. If the index is a numeric type: constrain the type variable to be an array-like
   type (i.e. `Array<T>` for a fresh `T`), and return `T`.
2. If the index is a string literal type: treat it as a property access (same as
   dot notation), constraining the type variable to have that property.
3. If the index is a string type (non-literal): constrain the type variable to
   have a string index signature, and return the index signature's value type.
   *(Deferred from initial implementation — see Section 10.)*

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

### 5. Open vs. Closed Object Types ✅

> **Status (2026-04-08):** Fully implemented. `closeOpenParams` in
> `infer_func.go` closes all open object types on parameters after
> `inferFuncBodyWithFuncSigType` completes. `closeObjectType` recursively
> closes nested open objects. `RestSpreadElem`s whose row variables don't
> appear in the return type are removed. Row variables that escape to the
> return type are preserved for Phase 7 (row polymorphism).

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

### 5a. Mutability Inference ✅

> **Status (2026-04-08):** Fully implemented. Mutability resolution runs during
> `closeOpenParams` in `infer_func.go` via the `finalizeOpenObject` helper in
> `generalize.go`. If any `PropertyElem.Written` is `true`, the param is wrapped
> in `MutabilityType{Mutable}` (`mut`); otherwise the wrapper is removed.
> Nested objects are handled recursively. The `MutabilityType{Uncertain}`
> wrapping approach described below was **not** used — instead, the assignment
> handler directly bypasses the immutability check for open objects. See Phase 2
> notes in the implementation plan.

When an unannotated parameter's type is inferred from usage, the system must
also infer whether the parameter needs to be mutable. Currently, property
assignment on an object checks that the object's type is wrapped in a
`MutabilityType` — if not, a `CannotMutateImmutableError` is reported.

**Rule:** If any property on an inferred open object type is **written to**
(assigned), the parameter's type should be wrapped in
`MutabilityType{MutabilityMutable}` (i.e. `mut`). If the parameter is only
**read from**, no `MutabilityType` wrapper is needed.

**Example — write access requires `mut`:**
```esc
fn foo(obj) {
    obj.bar = "hello"
    obj.baz = 5
}
// inferred: fn foo(obj: mut {bar: string, baz: number}) -> void
```

**Example — read access does not require `mut`:**
```esc
fn foo(obj) {
    let x = obj.bar
    let y = obj.baz
}
// inferred: fn foo(obj: {bar: t1, baz: t2}) -> void
```

**Example — mixed access requires `mut`:**
```esc
fn foo(obj) {
    let x = obj.bar
    obj.baz = 5
}
// inferred: fn foo(obj: mut {bar: t1, baz: number}) -> void
```

#### Implementation approach

When `getMemberType` creates the open `ObjectType` for a `TypeVarType` (Phase 2),
wrap it in `MutabilityType{MutabilityUncertain}`:
```
typeVar.Instance = MutabilityType{Uncertain, openObj}
```

This allows both reads and writes during inference — the assignment code's
mutability check passes because the type is a `MutabilityType` (any kind).
`getMemberType` already handles `MutabilityType` by unwrapping and recursing
(expand_type.go ~line 501), so property lookups work through the wrapper.

After inference completes (Phase 6, closing), resolve the mutability:
- If any property was **written to**, change `MutabilityUncertain` to
  `MutabilityMutable` (the parameter must be `mut`).
- If **no** properties were written to (read-only access), remove the
  `MutabilityType` wrapper entirely.

To track whether writes occurred, add a `Written bool` field to `PropertyElem`.
Set it to `true` in the assignment handler (infer_expr.go ~line 34, the
`MemberExpr` branch of the `Assign` case) when the LHS property is found on an
open `ObjectType`. At closing time, check if any `PropertyElem` has
`Written: true`.

#### Interaction with passing to typed functions

When an open-typed parameter is passed to a function expecting `mut {x: number}`,
the `MutabilityType{Mutable}` on the expected type unifies with the
`MutabilityType{Uncertain}` on the inferred type. This should resolve the
uncertain mutability to mutable — the parameter must be `mut` because the
called function requires it.

When passed to a function expecting `{x: number}` (immutable), the uncertain
mutability is compatible. Whether it resolves to mutable depends on other usages
within the function body.

### 6. Unification Changes

The unifier must be extended to handle the following cases:

#### 6a. Unifying unannotated-parameter TypeVarTypes ✅

> **Status (2026-04-06):** Implemented in Phase 3. The `openClosedObjectForParam`
> helper in `bind()` handles the conversion from closed to open. See Phase 3 in
> [implementation_plan.md](implementation_plan.md) for details.

An unannotated parameter starts as a bare `TypeVarType`. It may be constrained
by property access (Option C — eagerly binds to an open `ObjectType`), by being
passed to typed functions, or both. This section covers the function-call path.

**Already bound to an open ObjectType** (properties were accessed before the
call): unification proceeds as open-vs-closed (6c) or open-vs-open (6b).

**Not yet bound** (no properties accessed yet): the type variable is unified
with the parameter type via `bind()`. Since the parameter is unannotated, it
must remain open if the target is an `ObjectType`. When `bind()` encounters a
closed `ObjectType`, it binds the type variable to an open `ObjectType`
(`Open: true`) with the same properties plus a `RestSpreadElem` with a fresh
row variable. No `MutabilityType` wrapper is added — `GeneralizeFuncType` finds
open objects by checking `ObjectType.Open` directly.

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
// inferred: fn foo(obj: mut {x: number, y: string, z: boolean}) -> void
```

When two open `ObjectType`s are intersected, their properties are merged and
their row variables unified (see 6b).

#### 6b. Multiple constraints on a type variable ✅

> **Status (2026-04-06):** Implemented in Phase 3. Multiple calls compose
> automatically via the open-vs-closed unification path.

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

#### 6c. Open object type with closed object type ✅

> **Status (2026-04-06):** Implemented in Phase 3. The open-vs-closed and
> open-vs-open paths are handled in the ObjectType-vs-ObjectType branch of
> `unifyWithDepth`. Open-vs-closed is split into two separate cases
> (open(t1)-vs-closed(t2) and closed(t1)-vs-open(t2)) to preserve the
> directionality of `Unify`, which is not symmetric.

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
// inferred: fn foo(obj: mut {z: boolean, x: number, y: string, w: string}) -> void
```

If 6c closed the type at step 2 (the `bar(obj)` call), step 3 would fail
because `w` couldn't be added. By keeping the type open, all three operations
contribute to the final inferred type regardless of order.

**Note:** The existing unifier code already handles `RestSpreadElem` by
collecting leftover properties and unifying them with the rest type variable.
The change needed is to check `Open` on the `ObjectType` — when it is `true`,
add the closed type's properties directly to the open `ObjectType`'s `Elems`
instead of binding the row variable.

#### 6d. Property type widening (union accumulation) ✅

> **Status (2026-04-07):** Implemented in Phase 4. The `unifyWithDepth` function
> type-asserts `tv2` before Prune, and when concrete-vs-concrete unification
> fails on a `Widenable` TypeVarType, reads `tv2.InstanceChain` (populated by
> `Prune` via `recordInstanceChain`) to find all aliased TypeVars, then widens
> all of their Instances to a deduplicated union via `flatUnion`. Write-site
> gating ensures only the right-hand side (property write target) is widened;
> read sites report type errors. Uncertain `MutabilityType` wrappers are
> stripped. See Phase 4 in
> [implementation_plan.md](implementation_plan.md) for details.

When the same element on an inferred open object type is used multiple times with
different types — whether sequentially or across branches — the element's type
must accommodate all observed types. The type variable receives multiple
unification demands and must not get "stuck" on the first one.

The unifier must handle this by **widening** the type to a union:

1. If a type variable has already been bound to a concrete type `A`, and a new
   unification attempts to bind it to an incompatible concrete type `B`, and
   the type variable is widenable (i.e. it is a type variable whose type was
   inferred from usage within an inferred open object), then instead of
   reporting an error, widen the binding to `A | B`.
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
inferred from usage with a `Widenable` flag on `TypeVarType`. When `bind()`
encounters a conflict on such a variable, it creates a union instead of
returning an error. The name `Widenable` is preferred over `RowInferred` because
the flag applies to both property values on objects and parameter/return types
on inferred method signatures — the common behavior is widening, not
specifically "row" inference.

#### 6e. Literal type widening to primitives ✅

> **Status (2026-04-07):** Implemented in Phase 4. The `widenLiteral` function
> in `bind()` converts `LitType` to `PrimType` when binding to a `Widenable`
> TypeVarType. Recursively widens literals inside `ObjectType` and `TupleType`
> values (deep widening), including method return types, getter return types,
> and setter param types via `widenFuncType`. Strips uncertain `mut?` wrappers
> from widened results. See Phase 4 in
> [implementation_plan.md](implementation_plan.md) for details.

When a literal type (`"hello"`, `5`, `true`, etc.) is unified with a widenable
type variable, the literal should be **widened to its corresponding primitive
type** (`string`, `number`, `boolean`) before binding. This applies to the
initial binding as well as to subsequent unifications (Section 6d).

**Rationale:** An inferred parameter type represents "what types does this
position need to support." When a function sets `obj.bar = "hello"`, the intent
is almost never to require callers to pass exactly `"hello"` — it is to accept
any `string`. Literal types are useful for local variable inference (`let x =
"hello"` infers `"hello"`), but for inferred parameter constraints, the
primitive type is the right level of precision.

This removes the need for explicit type annotations on literals in most cases:

```esc
fn foo(obj) {
    obj.bar = "hello"     // obj.bar: string  (not "hello")
    obj.baz = 5           // obj.baz: number  (not 5)
    obj.flag = true       // obj.flag: boolean (not true)
}
// inferred: fn foo(obj: mut {bar: string, baz: number, flag: boolean}) -> void
```

Without this widening, the user would need to write `"hello":string`,
`5:number`, `true:boolean` to get the primitive types, which is unnecessarily
verbose for the common case. Anyone who wants a literal type in an inferred
parameter can add an explicit type annotation.

**Scope:** This widening applies only to widenable type variables (those with
`Widenable: true`). It does **not** affect:
- Local variable inference: `let x = "hello"` still infers `"hello"`.
- Explicit type annotations: `let x: string = "hello"` works as before.
- Non-widenable type variable unification: no change to existing behavior.

**Interaction with union accumulation (6d):** With literal widening, multiple
assignments to the same property with different literals of the same kind
produce the primitive type rather than a union of literals:

```esc
fn foo(obj) {
    obj.bar = "hello"
    obj.bar = "world"
}
// inferred: fn foo(obj: mut {bar: string}) -> void
// (not {bar: "hello" | "world"})
```

Assignments with literals of **different** kinds still produce a union of
primitives:

```esc
fn foo(obj) {
    obj.bar = "hello"
    obj.bar = 5
}
// inferred: fn foo(obj: mut {bar: string | number}) -> void
```

**Deep widening of object and tuple literals:** When an object or tuple literal
is assigned to a widenable property, literal values within the nested structure
are recursively widened to their primitive types. Uncertain mutability (`mut?`)
wrappers are stripped from leaf (primitive) values but preserved on nested
structured types so that generalization can later resolve mutability:

```esc
fn foo(obj) {
    obj.loc = {x: 0, y: 0}
    obj.col = "red"
}
// inferred: fn foo(obj: mut {loc: {x: number, y: number}, col: string}) -> void
```

This also applies to tuples and deeply nested structures:

```esc
fn foo(obj) {
    obj.data = {coords: [1, 2], label: "hi"}
    obj.prop = {a: {b: {c: "hello", d: 5}}}
}
// inferred: fn foo(obj: mut {
//   data: {coords: [number, number], label: string},
//   prop: {a: {b: {c: string, d: number}}}
// }) -> void
```

**Method, getter, and setter widening:** Object literals can contain methods,
getters, and setters. Their parameter types and return types are also
recursively widened:

```esc
fn foo(obj) {
    obj.config = {
        _x: 0,
        getValue(self) { return self._x },
        get x(self) { return self._x },
        set x(mut self, v) { self._x = v },
    }
}
// inferred: fn foo(obj: mut {config: {
//   _x: number,
//   getValue(self) -> number,
//   get x(self) -> number,
//   set x(mut self, v: number) -> undefined
// }}) -> void
```

**Note on bigint:** The `widenLiteral` function also handles `BigIntLit` →
`bigint` widening, but this is not yet testable because the parser does not
support bigint literal syntax (`1n`) in all expression positions. See issue #228.

### 7. Constraint Representation ✅

> **Status (2026-04-06):** Option C implemented in Phase 1 (type system
> extensions) and Phase 2 (property access inference).

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

When a method is called (property access followed by a call), the property's
fresh type variable is bound to a `FuncType` containing fresh widenable type
variables for each parameter and the return type. The supplied arguments are
then unified with the fresh parameter types.

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

#### 8a. Passing inferred-type params to typed functions ✅

> **Status (2026-04-06):** Implemented in Phase 3.

When a value with an inferred (open) object type is passed to a function with a
typed parameter, the open type should unify with the parameter's type:

```esc
fn bar(x: {bar: string}) -> string { return x.bar }

fn foo(obj) {
    let result = bar(obj)   // constrains obj to {bar: string, ...}
}
// inferred: fn foo(obj: {bar: string}) -> void
```

#### 8b. Return type inference ✅

> **Status (2026-04-08):** Implemented in Phases 6 and 7. Phase 6 closes
> inferred object types and preserves `RestSpreadElem`s that appear in the
> return type. Phase 7 confirms that `GeneralizeFuncType` promotes these to
> type parameters and that `instantiateGenericFunc` correctly instantiates them
> at call sites.

If an inferred-type param is returned from the function, the return type should
reflect the inferred open type. See 8g and Section 11 for details on how the
row variable connects input to output for callers.

#### 8c. Multiple parameters ✅

> **Status (2026-04-06):** Implemented in Phase 3.

Each unannotated parameter gets its own independent set of inferred constraints:

```esc
fn foo(a, b) {
    a.x = 1
    b.y = "hello"
}
// inferred: fn foo(a: mut {x: number}, b: mut {y: string}) -> void
```

#### 8d. Aliasing and flow ✅

> **Status (2026-04-06):** Works automatically via type variable unification.
> Tested implicitly through Phase 3.

If an unannotated parameter is assigned to a local variable and then properties
are accessed on that variable, the constraints should flow back to the parameter:

```esc
fn foo(obj) {
    let alias = obj
    alias.x = 1
}
// inferred: fn foo(obj: mut {x: number}) -> void
```

This should work naturally through type variable unification — `alias` gets the
same type variable as `obj`.

#### 8e. Nested property access ✅

> **Status (2026-04-06):** Implemented as part of Phase 2. Tested with
> `NestedAccess` and `DeeplyNested` test cases.

When a property is accessed on a property of an inferred object, the inner
property's fresh type variable undergoes the same Option C binding recursively:

```esc
fn foo(obj) {
    obj.foo.bar = 5
}
// inferred: fn foo(obj: mut {foo: mut {bar: number}}) -> void
```

This works because `obj.foo` returns a fresh type variable (the value type of
property `foo`), and then accessing `.bar` on that type variable triggers
Option C again, binding it to a new open `ObjectType` with property `bar`.

#### 8f. Destructuring patterns on params

##### Object destructuring

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

##### Tuple/array destructuring

Tuple destructuring patterns (`[a, b]`) infer a `TupleType` from the pattern
shape, with one element type per position. `inferPattern` already handles
`TuplePat` by creating a `TupleType` with a fresh type variable for each
element.

**Without rest element:** The pattern fully determines the tuple length. Each
position gets an independent type variable:

```esc
fn foo([a, b]) {
    // a and b are available as bindings
}
// inferred: fn foo([a, b]: [t1, t2]) -> void
```

The parameter type is a fixed-length tuple. Callers must pass a tuple (or array)
with at least that many elements.

**With rest element:** When a tuple destructuring pattern includes a rest element
(`[a, b, ...rest]`), the leading positions get fixed types and the rest binding
captures the remaining elements. The parameter should be inferred as a variadic
tuple `[t1, t2, ...R]` where each positional element gets an independent type
variable and `R` is a rest type variable representing the remaining elements:

```esc
fn foo([first, ...rest]) {
    // first is the first element, rest is the remaining elements
}
// inferred: fn foo([first, ...rest]: [t1, ...R]) -> void
// where t1 is the first element's type, R captures remaining elements
// first: t1, rest: R
```

This requires variadic tuple type support (Section 14). The rest variable `R`
enables row polymorphism for tuples — if the parameter is returned, callers'
extra elements are preserved (Section 15).

**Interaction with Section 13 (tuple/array inference):** If the function body
also indexes the parameter by name (which isn't possible for tuple-destructured
params since the parameter isn't bound to a name), this doesn't apply. However,
if a tuple-destructured param's bindings are used in ways that constrain their
types (e.g. `first + 1`), those constraints flow through the type variables
normally.

#### 8g. Return type row variable propagation ✅

> **Status (2026-04-08):** Implemented in Phase 7. Extra properties from
> callers are preserved through row variables. Literal types are preserved (not
> widened) — e.g. `foo({x: 1, y: 2})` produces `{x: number, y: 2}` where `x`
> is widened from the function's write but `y` retains its literal type.

When an inferred-type param is returned from the function, the return type
should preserve extra properties that callers pass in. This requires **row
polymorphism** — the function's type must be generic over the row variable so
that callers can instantiate it with their specific extra properties:

```esc
fn foo(obj) {
    obj.x = 1
    return obj
}

val result = foo({x: 1, y: 2})
// result type: {x: number, y: 2}
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
- **Non-literal string index signatures**: When `obj[strVar]` is used where
  `strVar` is type `string` (not a literal), Section 3 specifies that the type
  variable should be constrained to have a string index signature. This is
  deferred from the initial implementation. For now, this case produces an
  error. Numeric indexing and string-literal indexing are in scope.

These can be addressed in follow-up work.

### 11. Row Polymorphism ✅

> **Status (2026-04-08):** Implemented in Phase 7. See subsection status notes
> below.

Row polymorphism allows functions with inferred parameter types to preserve
extra properties through their return types. Without it, a function like:

```esc
fn foo(obj) {
    obj.x = 1
    return obj
}
```

would have the closed signature `fn foo(obj: mut {x: number}) -> mut {x: number}`,
and calling `foo({x: 1, y: 2})` would return `mut {x: number}` — losing `y`.

#### 11a. Row-polymorphic function types ✅

> **Status (2026-04-08):** Implemented in Phase 7. The function type uses
> `...T0` syntax (shared naming with regular type params). Literal types from
> callers are preserved through row variables — they are not widened.

When a function has an inferred parameter type that is returned (or part of the
return type), the function should be **generic over the row variable**. Instead
of closing the row variable after inference, it is preserved as a type parameter
in the function's type:

```esc
fn foo(obj) {
    obj.x = 1
    return obj
}
// type: fn foo<T0>(obj: mut {x: number, ...T0}) -> {x: number, ...T0}
```

The row variable `T0` appears in both the parameter and return type, connecting
them. When the function is called, `T0` is instantiated with the caller's extra
properties:

```esc
val result = foo({x: 1, y: 2})
// T0 = {y: 2}, so result: {x: number, y: 2}
```

#### 11b. When to preserve row variables ✅

> **Status (2026-04-08):** Implemented in Phase 6 (`closeObjectType` removes
> `RestSpreadElem`s not in return type) and Phase 7 (remaining row vars are
> promoted to type params by `GeneralizeFuncType`). Tested in
> `TestRowTypesRowPolymorphism` — `NoReturn_RowVarRemoved`,
> `DerivedReturn_RowVarDoesNotEscape`, `ReturnInStructure_RowVarDoesNotEscape`.

Not all inferred parameters need row polymorphism. A row variable is preserved
(not closed) **only when it appears in the function's return type**:

1. **Return type**: If the parameter (or the object containing it) is returned,
   the row variable must appear in the return type.
2. **Mutation alone is not sufficient**: If the parameter is mutated but not
   returned, the row variable is removed. The caller's extra properties are
   accepted but not tracked in the function's type. For example,
   `fn foo(obj) { obj.x = 1 }` has type `fn (obj: mut {x: number}) -> void`
   — no row variable, even though `obj` is mutated.

If the parameter's row variable does NOT appear in the return type, it is
removed after inference (the caller's extra properties are accepted but not
tracked).

#### 11c. Implementation sketch ✅

> **Status (2026-04-08):** Fully implemented across Phases 6 and 7. All five
> steps are handled by existing infrastructure — no new code was needed for
> steps 1–5. The only code change was adding `collectFlatElems` to
> `ObjectType.String()` for display flattening of resolved `RestSpreadElem`s.

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

> **Implementation details (2026-04-08):**
>
> Steps 1–4 are handled by Phase 6 (`closeOpenParams`/`closeObjectType`) and
> `GeneralizeFuncType` (#379):
> - `collectUnresolvedTypeVars` walks into `RestSpreadElem.Value` and collects
>   the inner unresolved `TypeVarType` like any other.
> - `GeneralizeFuncType` promotes that TypeVar by setting
>   `tv.Instance = TypeRefType{Name: "T0", ...}`. This binds the TypeVar
>   inside the `RestSpreadElem` — it does **not** replace the
>   `RestSpreadElem` itself. After generalization, the structure is:
>   `RestSpreadElem{Value: TypeVarType{Instance: TypeRefType{Name: "T0"}}}`.
>   `Prune()` resolves the inner value to the `TypeRefType`, preserving the
>   `RestSpreadElem` wrapper.
>
> Step 5 is handled by `instantiateGenericFunc`/`SubstituteTypeParams`:
> - `RestSpreadElem.Accept()` calls `r.Value.Accept(v)`, substituting the
>   `TypeRefType` with a fresh TypeVar while returning a new
>   `RestSpreadElem{Value: freshTypeVar}`. The wrapper is preserved.
> - **No changes to `GeneralizeFuncType` or `instantiateGenericFunc` were
>   needed.**
>
> **Display flattening:** `ObjectType.String()` uses `collectFlatElems` to
> inline properties from resolved `RestSpreadElem`s. For example, `{x: number,
> ...{y: 2}}` displays as `{x: number, y: 2}`. Empty rest objects are dropped.

#### 11d. Interaction with Section 5 (closing) ✅

> **Status (2026-04-08):** Implemented in Phase 6. `closeOpenParams` /
> `closeObjectType` in `infer_func.go` implement all three rules below.

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
their own row variable that appears in the return type) are **in scope** and
**implemented**. Tested in `TestRowTypesRowPolymorphism/MultipleParamsRowPolymorphism`.
The spread-based pattern `fn merge(a, b) { return {...a, ...b} }` requires
Phase 10 (Object Spread).

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

### 13. Tuple and Array Inference from Indexing Patterns

When a function parameter is used with numeric indexing and/or method calls, the
system should infer whether the parameter is a **tuple** or a **`mut Array`**
based on the usage patterns:

#### 13a. Tuple inference (non-negative integer literal indexes only)

If all index accesses on the parameter have a **non-negative integer literal
type** (i.e. the index expression's type is `0`, `1`, `2`, ...) and all property
accesses and method calls are available on the immutable `Array` type (e.g.
`.length`, `.map()`, `.filter()`, `.slice()`), then the parameter should be
inferred as a **tuple type** with one element per distinct index accessed.

The classification is based on the **type** of the index expression, not its
syntactic form. An expression like `items[i]` where `i` has literal type `5`
(e.g. `val i = 5`) is treated identically to `items[5]` — both qualify as
non-negative integer literal indexes. Conversely, if `i` has type `number`
(e.g. from a function parameter `i: number`), it is a non-literal index
regardless of its runtime value. Any other numeric literal types — negative
integers, non-integers like `1.5`, or extremely large values — are treated as
non-literal numeric access and follow the array inference path (Section 13c):

```esc
fn foo(items) {
    let a = items[0]
    let b = items[1]
    let c = items.length
}
// inferred: fn foo(items: [t0, t1]) -> void
// where t0 and t1 are fresh type variables for each indexed position
```

```esc
fn bar(items) {
    val i = 5
    items[i] = "hello"   // i has literal type 5, so this is items[5]
}
// HasIndexAssignment is true → inferred as mut [T0, T1, T2, T3, T4, string]
// (mutable tuple — the index is a literal, so tuple shape is preserved)
```

The tuple length is determined by the highest index accessed + 1. If `items[0]`
and `items[2]` are accessed but not `items[1]`, the tuple has 3 elements:
`[t0, t1, t2]` where `t1` remains an unresolved type variable.

**Why tuple, not Array:** When only literal indexes are used, the programmer is
treating the parameter as a fixed-length, positionally-typed sequence. A tuple
captures this intent precisely — each position can have a different type, and the
length is known. An `Array<T>` would lose positional type information by unifying
all element types into a single `T`.

**Interaction with Array methods:** Read-only `Array` methods like `.map()`,
`.filter()`, `.length`, etc. are available on tuples (tuples delegate method
access to `Array<union of element types>`). So accessing
`.length` or calling `.map()` does not prevent tuple inference.

#### 13b. Array inference (mutating methods)

If the parameter has property accesses or method calls that are only available on
`mut Array` and not on immutable `Array` — specifically mutating methods like
`.push()`, `.pop()`, `.shift()`, `.unshift()`, `.splice()`, `.reverse()`,
`.sort()`, `.fill()`, `.copyWithin()` — then the parameter should be inferred
as `mut Array<T>`:

```esc
fn foo(items) {
    items.push(42)
    let a = items[0]
}
// inferred: fn foo(items: mut Array<number>) -> void
```

When mutating methods are called, all indexed element types and method argument
types contribute to the single element type `T` via union accumulation (same
widening behavior as Section 6d):

```esc
fn foo(items) {
    items.push(42)
    items.push("hello")
}
// inferred: fn foo(items: mut Array<string | number>) -> void
```

**Why `mut Array`, not tuple:** Mutating methods like `.push()` change the
array's length at runtime. A tuple has a fixed length, so mutations that add or
remove elements are incompatible with tuple semantics. The parameter must be an
array to support these operations.

#### 13c. Non-literal numeric index type implies Array

If the parameter is indexed with an expression whose **type** is `number` (not a
literal type like `5`), the parameter should be inferred as `Array<T>` (or
`mut Array<T>` if mutating methods are also used), not as a tuple:

```esc
fn foo(items, i: number) {
    let x = items[i]       // i has type number → non-literal
}
// inferred: fn foo(items: Array<t0>, i: number) -> void
```

Note that `val i = 5` gives `i` the literal type `5`, so `items[i]` would still
qualify as a literal index (see Section 13a). Only when the index expression's
type is the general `number` type (or another non-literal numeric type) does this
rule apply.

**Why:** A non-literal index type means the position being accessed is unknown at
type-check time, so a tuple would not be meaningful.

#### 13d. Index assignment implies mutability

If an element is assigned via index notation (`items[i] = value`), the parameter
must be mutable. When only literal indexes are used (no mutating methods or
non-literal indexes), the parameter is inferred as a **mutable tuple** rather
than `mut Array<T>`:

```esc
fn foo(items) {
    items[0] = 42
}
// inferred: fn foo(items: mut [number]) -> void
```

If a non-literal index is used for assignment, the parameter is inferred as
`mut Array<T>` (per Section 13c). If mutating methods are also used, `mut Array<T>`
takes precedence (per Section 13b):

```esc
fn foo(items) {
    items[0] = 42
    items.push(99)
}
// inferred: fn foo(items: mut Array<number>) -> void
```

**Why:** Index assignment with a literal index mutates a specific known position.
A mutable tuple (`mut [T]`) captures this precisely — the length is still fixed
and each position retains its own type. `mut Array<T>` would lose positional type
information. Note that mutating methods like `.push()` change the length, which
is incompatible with tuple semantics, so they still force `mut Array<T>`.

#### 13e. Summary of inference rules

| Usage pattern | Inferred type |
|---------------|---------------|
| Only non-negative integer literal indexes + read-only Array methods | Tuple `[t0, t1, ...]` |
| Literal index assignment only (no mutating methods) | `mut [t0, t1, ...]` |
| Any non-literal numeric index (read-only) | `Array<T>` |
| Any mutating method (`.push()`, `.pop()`, etc.) | `mut Array<T>` |
| Mix of literal indexes + mutating methods | `mut Array<T>` |
| Index assignment + mutating methods | `mut Array<T>` |

#### 13f. Interaction with existing Section 3

Section 3 previously bound a type variable to `Array<T>` whenever a numeric index
was used. This section refines that behavior:

- **Non-negative integer literal index** (e.g. `obj[0]`): defer commitment —
  the type variable is not immediately bound to `Array<T>`. Instead, an
  `ArrayConstraint` is created on the `TypeVarType` to record the index as a
  tuple constraint. Final resolution to tuple vs. array happens during closing
  (Section 5), based on whether any array-only patterns were observed. Other
  numeric literals (negative, fractional) are treated as non-literal access.
- **Non-literal numeric index** (e.g. `obj[i]`): records `HasNonLiteralIndex`
  on the `ArrayConstraint`, which forces resolution to `Array<T>` at closing
  time.
- **Mutating method call**: records `HasMutatingMethod` on the `ArrayConstraint`,
  which forces resolution to `mut Array<T>` at closing time.
- **Array method on fresh TypeVar**: When a property like `.push()` or `.length`
  is accessed on a TypeVarType that doesn't yet have an `ArrayConstraint`, the
  system checks whether the property exists on the Array type definition. If so,
  an `ArrayConstraint` is created instead of an open object.

This deferred approach avoids premature commitment to `Array<T>` when the usage
pattern actually indicates a tuple.

### 14. Variadic Tuple Types ✅

> **Status (2026-04-08):** Implemented in Phase 13. Tuple-vs-tuple unification
> with `RestSpreadType` at any position is complete (`splitTupleAtRest` +
> specialized helpers in `unify.go`). `TupleType.String()` flattens resolved
> rest types via `collectFlatTupleElems`. `getMemberType` handles
> `RestSpreadType` in both numeric index and method access via `tupleElemUnion`.
> The parser now supports `...T` in tuple type annotations (`primaryTypeAnn` in
> `type_ann.go`). Generalization and instantiation work automatically via the
> visitor pattern. Tests: `TestVariadicTupleTypes`, `TestVariadicTupleSubtyping`.

Variadic tuple types extend tuple types with a rest element that captures a
variable number of trailing elements. This is the tuple analogue of row variables
(`RestSpreadElem`) in object types — it enables functions to be generic over the
"tail" of a tuple.

#### 14a. Syntax and representation ✅

A variadic tuple type is written as `[A, B, ...T]` where `A` and `B` are fixed
positional element types and `T` is a type variable (or concrete type)
representing the remaining elements. `T` can be:

- A type variable: `[number, ...T]` — generic over the trailing elements.
- An array type: `[number, ...Array<string>]` — fixed first element, then any number
  of strings.
- A tuple type: `[number, ...[string, boolean]]` — equivalent to
  `[number, string, boolean]` (flattened in display).

**Rest spread position:** A `RestSpreadType` is not limited to the trailing
position. It can appear at the start, middle, or end of a tuple, but a tuple
type may contain **at most one** `RestSpreadType` with an unbounded type (e.g.
`Array<T>`). Multiple bounded rest spreads (where the inner type is a tuple or
type variable) are allowed. This matches TypeScript's constraint and keeps
length inference decidable.

```esc
// Leading rest:
type LastIsString = [...Array<number>, string]
val a: LastIsString = ["hello"]         // ok — zero numbers, then string
val b: LastIsString = [1, 2, "hello"]   // ok — two numbers, then string

// Trailing rest:
type Prefixed = [string, ...Array<number>]
val c: Prefixed = ["label"]             // ok — string, zero numbers
val d: Prefixed = ["label", 1, 2, 3]   // ok — string, three numbers
```

Variadic tuple types can be used in type aliases to express useful constraints:

```esc
type OneOrMore<T> = [T, ...Array<T>]
```

This ensures at least one element is present. A function accepting `OneOrMore<T>`
is guaranteed a non-empty collection at the type level:

```esc
fn first<T>(items: OneOrMore<T>) -> T { return items[0] }
first([1, 2, 3])    // ok — T = number
first([])           // error — [] is not assignable to [number, ...Array<number>]
```

In the type system, this is represented as a `TupleType` with `RestSpreadType`
elements at any position in `Elems`:
```go
// [number, ...T]
TupleType{Elems: [number, RestSpreadType{Type: T}]}

// [...Array<number>, string]
TupleType{Elems: [RestSpreadType{Type: Array<number>}, string]}
```

The `RestSpreadType` type already exists and can appear in `TupleType.Elems`.
The parser handles `...T` in tuple type annotations (support was added to
`primaryTypeAnn` in Phase 13 — previously `...` was only handled in object type
annotation elements).

#### 14b. Unification rules ✅

When unifying tuple types that contain `RestSpreadType` elements:

**Fixed-vs-variadic** (`[A, B]` vs `[C, ...R]`):
1. Unify positional elements pairwise: `Unify(A, C)`.
2. Bind `R` to a tuple of the remaining elements: `R = [B]`.

**Variadic-vs-fixed** (`[A, ...R]` vs `[B, C]`):
1. Unify positional elements pairwise: `Unify(A, B)`.
2. Bind `R` to the remaining elements: `R = [C]`.

**Variadic-vs-variadic** (`[A, ...R1]` vs `[B, ...R2]`):
1. Unify positional elements pairwise: `Unify(A, B)`.
2. If both have the same number of positional elements: `Unify(R1, R2)`.
3. If one has more positional elements, collect the extras and unify the shorter
   side's rest with a variadic tuple of the extras plus the longer side's rest.

**Variadic-vs-Array** (`[A, ...R]` vs `Array<T>`):
1. Unify `A` with `T`.
2. Unify `R` with `Array<T>`.

**Examples:**
```esc
// Fixed-vs-variadic
val x: [number, ...Array<string>] = [1, "a", "b"]
// 1 unifies with number, ["a", "b"] unifies with ...Array<string>

// Variadic generic
fn foo<T>(items: [number, ...T]) -> T { ... }
val r = foo([1, "hello", true])
// T = ["hello", true], r: ["hello", true]
```

#### 14c. Interaction with Array methods ✅

A variadic tuple like `[number, string, ...T]` supports the same Array methods
as fixed tuples. The element type union for method resolution includes all
fixed elements plus the rest type's element type:

- `[number, string, ...Array<boolean>]` → methods resolve against
  `Array<number | string | boolean>`.
- `[number, ...T]` where `T` is unresolved → methods resolve against
  `Array<number | T>`.

#### 14d. Display ✅

Variadic tuple types display with `...` before the rest type:
- `[number, string, ...T]` — unresolved rest.
- `[number, string, ...Array<boolean>]` — rest is an array.

When the rest type resolves to a concrete tuple, the elements are **flattened**
into the parent (same display behavior as `ObjectType.String()` for resolved
`RestSpreadElem`s):
- `[number, ...[string, boolean]]` displays as `[number, string, boolean]`.
- `[number, ...[]]` displays as `[number]` (empty rest dropped).

### 15. Tuple Row Polymorphism (Variadic Inference) ✅

> **Status (2026-04-09):** Implemented in Phase 14. `resolveArrayConstraint`
> appends a `RestSpreadType` with a fresh type variable to inferred tuples.
> `closeTupleType` removes rest variables that don't appear in the return type.
> `closeOpenParams` was reordered to resolve ArrayConstraints before collecting
> `returnVars` so that freshly-created rest type variables are visible.
> Generalization and call-site instantiation required no changes — the existing
> visitor pattern in `GeneralizeFuncType` and `SubstituteTypeParams` already
> walks `RestSpreadType` inside `TupleType.Elems`. Tests in
> `TestTupleRowPolymorphism`.

Tuple row polymorphism enables functions with inferred tuple parameters to
preserve extra elements through their return types. This is the tuple analogue
of row polymorphism for objects (Section 11).

#### 15a. Motivation

Without tuple row polymorphism, a function like:

```esc
fn first(items) {
    return items[0]
}
```

has the closed signature `fn first(items: [t0]) -> t0` after Phase 12 infers a
tuple. Calling `first([1, "hello", true])` works (the extra elements are
accepted), but a function that returns the whole parameter:

```esc
fn identity(items) {
    let x = items[0]
    return items
}
```

would lose extra elements: the signature `fn (items: [t0]) -> [t0]` means
`identity([1, "hello", true])` returns `[1]`, discarding `"hello"` and `true`.

#### 15b. Row-polymorphic tuple types

When a function parameter is inferred as a tuple (Phase 12) and the parameter
is returned (or part of the return type), the inferred tuple should include a
rest type variable to capture extra elements:

```esc
fn identity(items) {
    let x = items[0]
    return items
}
// type: fn <T0, R>(items: [T0, ...R]) -> [T0, ...R]
```

The rest variable `R` appears in both the parameter and return type, connecting
them. When the function is called, `R` is instantiated with the caller's extra
elements:

```esc
val result = identity([1, "hello", true])
// T0 = 1, R = ["hello", true]
// result: [1, "hello", true]
```

#### 15c. When to preserve rest type variables

Same rules as object row polymorphism (Section 11b):

- **Appears in return type:** Preserve the rest variable — it becomes a type
  parameter.
- **Does not appear in return type:** Remove the `RestSpreadType` — the tuple
  becomes fixed-length. Extra elements are accepted but not tracked.

```esc
fn first(items) {
    return items[0]
}
// Return type is T0, not the tuple → rest removed
// type: fn <T0>(items: [T0]) -> T0
```

```esc
fn identity(items) {
    let x = items[0]
    return items
}
// Return type includes the tuple → rest preserved
// type: fn <T0, R>(items: [T0, ...R]) -> [T0, ...R]
```

#### 15d. Literal preservation

Literal types from callers are preserved through tuple rest variables (not
widened), matching the behavior of object row polymorphism (Section 11a):

```esc
fn identity(items) {
    let x = items[0]
    return items
}
val r = identity([1, "hello"])
// r: [1, "hello"] — literal types preserved
```

#### 15e. Interaction with Phase 12 and Phase 8

- **Phase 12 (tuple inference):** When resolving an `ArrayConstraint` to a
  tuple, append a `RestSpreadType` with a fresh type variable. The rest variable
  is resolved at closing time (same as row variables on open objects).
- **Phase 8 (destructuring):** Tuple destructuring without a rest pattern
  produces a fixed-length tuple (no rest variable). Tuple destructuring with a
  rest pattern produces a variadic tuple `[t0, t1, ...R]` where `R` is the
  rest binding's type — this is now possible thanks to variadic tuple support
  (Phase 13), rather than collapsing to `Array<T>`.

### 16. Array/Tuple Spread

Array/tuple spread expressions (`[...arr, extra]`) allow spreading an iterable
into a tuple literal. The checker already handles `ArraySpreadExpr` in
`TupleExpr` inference (wrapping the spread operand in a `RestSpreadType` with
`Array<elementType>`), but this section covers the full semantics including
interaction with inferred types and variadic tuples.

#### 16a. Basic array/tuple spread

Spreading an array or tuple into a tuple literal creates a `RestSpreadType`
element in the resulting `TupleType`:

```esc
val arr: Array<number> = [1, 2, 3]
val result = [0, ...arr, 4]
// result: [number, ...Array<number>, number]
```

```esc
val tup: [string, boolean] = ["hello", true]
val result = [0, ...tup, 4]
// result: [number, string, boolean, number]
// (spread of a concrete tuple is flattened)
```

When the spread source is a concrete `TupleType`, the elements should be
inlined into the parent tuple rather than kept as a `RestSpreadType` (same
flattening behavior as `TupleType.String()` for resolved rest types).

#### 16b. Spread of inferred types (TypeVarType)

When the spread source is an unannotated parameter (TypeVarType), the
`RestSpreadType` wraps the type variable directly. This enables the spread
to participate in variadic inference:

```esc
fn prepend(value, items) {
    return [value, ...items]
}
// inferred: fn <T0, T1>(value: T0, items: T1) -> [T0, ...T1]
// where T1 is constrained to be iterable
```

```esc
fn concat(a, b) {
    return [...a, ...b]
}
// inferred: fn <T0, T1>(a: T0, b: T1) -> [...T0, ...T1]
// where T0 and T1 are constrained to be iterable
```

#### 16c. Iterable constraint

The spread operand must be iterable (i.e., it must satisfy the `Iterable`
interface). This is already checked via `GetIterableElementType` in the
existing `ArraySpreadExpr` handler. Non-iterable types produce an error:

```esc
val x = [...42]  // error: Type 'number' is not iterable
```

#### 16d. Interaction with variadic tuple types (Section 14)

When a spread source has a variadic tuple type, the `RestSpreadType` is
preserved in the result:

```esc
fn foo<T>(items: [number, ...T]) -> [string, number, ...T] {
    return ["prefix", ...items]
}
```

When the spread source is an `Array<T>`, the result contains
`...Array<T>` at that position, which unifies with variadic tuple patterns.

#### 16e. Multiple spreads

A tuple type can contain at most one `RestSpreadType` with an unbounded type
(e.g. `Array<T>`). When multiple arrays are spread into a single tuple literal,
the result type collapses to `Array<T1 | T2 | ...>` because the boundary
between the two variable-length sequences is unknowable at the type level:

```esc
fn merge(a: Array<number>, b: Array<string>) {
    return [...a, ...b]
}
// return type: Array<number | string>
```

If one spread source is a fixed-length tuple and the other is an array, the
tuple elements can remain fixed while the array becomes the single rest:

```esc
fn prepend(tup: [number, string], arr: Array<boolean>) {
    return [...tup, ...arr]
}
// return type: [number, string, ...Array<boolean>]
```

Multiple tuple spreads (no arrays) produce a single flattened tuple:

```esc
fn concat(a: [number, string], b: [boolean]) {
    return [...a, ...b]
}
// return type: [number, string, boolean]
```

#### 16f. Spread with literal elements

Literal elements adjacent to spreads retain their literal types:

```esc
val arr: Array<number> = [1, 2, 3]
val result = [0, ...arr, "end"]
// result: [0, ...Array<number>, "end"]
```

#### 16g. Mutability

Spreading does **not** preserve mutability. The result of a spread expression is
always a fresh, immutable tuple (wrapped in `MutabilityUncertain` like all
inferred tuple literals). Spreading a `mut Array<T>` or `mut [...]` produces
the same result type as spreading the immutable version:

```esc
val arr: mut Array<number> = [1, 2, 3]
val result = [0, ...arr]
// result: [number, ...Array<number>] — not mut
```

#### 16h. Implementation notes

- **Existing handler:** `infer_expr.go` already handles `ArraySpreadExpr` in
  `TupleExpr` inference by calling `GetIterableElementType` and wrapping the
  result in `RestSpreadType{Type: Array<elementType>}`. This needs to be
  refined:
  - If the spread source is an `ArrayType` or `TypeRefType` referencing `Array`,
    use `RestSpreadType{Type: sourceType}` directly (preserving the array type).
  - If the spread source is a `TupleType`, inline the elements directly into
    the parent tuple.
  - If the spread source is a `TypeVarType`, use
    `RestSpreadType{Type: typeVar}` and verify the iterable constraint is
    deferred appropriately.
- **Unification:** Tuple-vs-tuple unification with `RestSpreadType` (Phase 13)
  already handles the mechanics. No additional unification changes needed.
- **Display:** `TupleType.String()` already handles `RestSpreadType` elements
  (Phase 13). No display changes needed.

## Implementation Approach (Summary)

1. **Extend `getMemberType`** to handle `TypeVarType` with two branches:
   - **Numeric index access** (the key is an `IndexKey` with a numeric type):
     for non-literal indexes, bind the `TypeVarType` to `Array<T>` for a fresh
     `T`, and return `T`. For non-negative integer literal indexes, defer
     commitment by recording the index on an `ArrayConstraint` (see
     Section 13).
   - **Property/method access or string-literal index** (the key is a
     `PropertyKey`, or an `IndexKey` with a string literal type): bind the
     `TypeVarType` to a `MutabilityType{Uncertain}` wrapping an open
     `ObjectType` (`Open: true`) containing a `PropertyElem` with a fresh
     widenable type variable for the value and a `RestSpreadElem` row variable,
     then delegate to `getObjectAccess`
     (see Section 3, case 2).
   - **Non-literal string index** (the key is an `IndexKey` with a `string`
     type): deferred from initial implementation (see Section 10). Eventually
     this should bind the `TypeVarType` to an open `ObjectType` with a string
     index signature, but for now it produces an error.

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

5. **Adjust `bind()` for unannotated-parameter type variables**: When binding an
   unannotated-parameter `TypeVarType` to a closed `ObjectType`, bind to an open
   `ObjectType` (`Open: true`) with the same properties plus a
   `RestSpreadElem`. When the type variable is already bound, compose via
   intersection — merging properties for open object types, or reducing to
   `never` for incompatible non-object types (see 6a).

6. **Update `inferFuncParams`**: Set `IsParam: true` on the fresh type variable
   created for unannotated params (see Section 6a). This flag is checked in
   `bind()` to keep the type open when binding to a closed `ObjectType`.

7. **Close and clean up after function body inference** ✅: After inferring a
   function body: (a) set `Open` to `false` on all inferred `ObjectType`s,
   (b) identify which row variables (from `RestSpreadElem`s) appear in the
   return type and promote those to type parameters on the function (row
   polymorphism — see Section 11), (c) remove `RestSpreadElem`s whose row
   variables do not appear in the return type, (d) resolve mutability — if any
   `PropertyElem` was written to, change `MutabilityUncertain` to
   `MutabilityMutable`; otherwise remove the `MutabilityType` wrapper
   (see Section 5a). Implemented by `closeOpenParams` / `closeObjectType` in
   `infer_func.go`.

8. **Handle row-polymorphic calls**: Extend `handleFuncCall` to instantiate row
   type parameters with fresh type variables, solved during argument
   unification. Extra properties from the caller flow through to the return type.

9. **Handle object spread expressions**: Add `ObjSpreadExpr` handling to
   `ObjectExpr` inference, support multiple `RestSpreadElem`s in `ObjectType`,
   and update the unifier to distribute properties across multiple rest elements
   (see Section 12).

10. **Refine numeric indexing to support tuple inference**: Instead of immediately
    binding to `Array<T>` on numeric index access, defer the decision. Track
    whether indexes are non-negative integer literals, whether mutating methods are
    called, and whether index assignments occur. At closing time, resolve to a
    tuple type (if only literal indexes + read-only Array methods) or
    `Array<T>` / `mut Array<T>` (if mutating methods, non-literal indexes, or
    index assignments are present). See Section 13.

11. **Complete variadic tuple type support** ✅: Finish `RestSpreadType` unification
    in tuple-vs-tuple, tuple-vs-array, and variadic-vs-variadic paths. Update
    `TupleType.String()` to flatten resolved rest types. Support variadic tuples
    in type annotations, generalization, and instantiation (see Section 14).
    Implemented in Phase 13: `splitTupleAtRest` + specialized unification helpers
    in `unify.go`; `collectFlatTupleElems` in `types.go`; `tupleElemUnion` in
    `expand_type.go`; `DotDotDot` case in `type_ann.go`.

12. **Add tuple row polymorphism** ✅: When an inferred tuple parameter is returned,
    append a `RestSpreadType` with a fresh type variable to capture extra
    elements. At closing time, preserve rest variables that appear in the return
    type (promoting to type parameters) and remove those that don't. This is
    the tuple analogue of object row polymorphism (see Section 15).
    Implemented in Phase 14: `resolveArrayConstraint` appends rest TV;
    `closeTupleType` filters by `returnVars`; `closeOpenParams` reordered to
    resolve constraints before collecting return vars.

13. **Add tests**: Cover property access, method calls, array indexing, optional
    chaining, passing to typed functions, widening, closing after inference, row
    polymorphism (return type propagation), object spread, tuple/array inference,
    variadic tuples, tuple row polymorphism ✅, and error cases.
