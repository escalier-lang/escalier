# Row Types Implementation Plan

## Context

All function parameters in Escalier currently require explicit type annotations.
When a parameter lacks one, the checker assigns a fresh type variable but cannot
refine it from property access, method calls, or indexing — producing
`ExpectedObjectError`. Row types enable inferring structural object types from
usage (Option C: eagerly binding to open `ObjectType`s). See
[requirements.md](requirements.md) for the full spec.

### Approach (Option C — Eager Open ObjectType)

When a property is accessed on a type variable, immediately bind it to an open
`ObjectType` (`Open: true`) containing that property. Subsequent accesses find
the bound `ObjectType` and either return existing property types or add new
`PropertyElem`s (gated by `Open`). No separate constraint data structure is
needed.

---

## Phase 1: Core Type System Extensions

**Requirements covered:** Definitions, Section 7 (Option C), prerequisites for
Sections 1–6.

**Goal:** Add the `Open` field to `ObjectType` and `Widenable` flag to
`TypeVarType` so later phases can build on them.

### Changes

1. **`internal/type_system/types.go`** — `ObjectType` struct (~line 1237):
   - Add `Open bool` field. When `true`, the property set can grow during
     inference. All programmer-written types have `Open: false`. Inferred types
     start with `Open: true` and are closed after the enclosing function body is
     fully inferred.
   - `Open` is orthogonal to `Exact` (controls unification with extra
     properties) and to `RestSpreadElem` (represents row variables / spreads).

2. **`internal/type_system/types.go`** — `TypeVarType` struct (~line 106):
   - Add `Widenable bool` field. Set to `true` for type variables whose type is
     inferred from usage — property values on open objects and method
     parameter/return types. When a conflicting unification occurs on a
     `Widenable` type variable, the binding widens to a union instead of
     producing an error (Phase 4).

3. **`internal/type_system/types.go`** — `ObjectType` printing (`String()` or
   equivalent):
   - When `Open` is `true`, no visual change is needed in the printed
     representation — the `RestSpreadElem` already conveys openness. If desired,
     add a comment in the code noting that `Open` is an internal-only flag not
     shown to users.
   - Audit all call sites that construct `ObjectType` to ensure they default to
     `Open: false` (Go zero-value is `false`, so existing code is safe).

### Tests

- Compile and run `go test ./internal/type_system/...` to verify the new fields
  don't break existing tests.
- No behavioral changes yet.

---

## Phase 2: Property Access Inference on TypeVarType

**Requirements covered:** Section 1 (property access inference), Section 3
(array indexing), Section 8e (nested property access).

**Goal:** Accessing `obj.bar` on an unannotated parameter binds the type
variable to an open `ObjectType` instead of producing `ExpectedObjectError`.

### Changes

1. **`internal/checker/expand_type.go`** — `getMemberType` (~line 500 switch):
   Add a `*type_system.TypeVarType` case **before** the `default` case. Also add
   `*type_system.TypeVarType` as a break condition in the expansion loop
   (~line 475–498) so we don't needlessly call `ExpandType`.

   The new case handles three sub-cases based on the key type:

   **a. PropertyKey or string-literal IndexKey:**
   ```go
   propTV := c.FreshVar(nil)
   propTV.Widenable = true
   rowTV := c.FreshVar(nil)
   openObj := NewObjectType(nil, []ObjTypeElem{
       NewPropertyElem(NewStrKey(name), propTV),
       NewRestSpreadElem(rowTV),
   })
   openObj.Open = true
   typeVar.Instance = openObj
   return propTV
   ```
   This eagerly binds the type variable to an open `ObjectType` containing one
   `PropertyElem` and one `RestSpreadElem` (the row variable). For string-literal
   `IndexKey`, extract the string value and treat identically to a `PropertyKey`.

   **b. Numeric IndexKey (key type is `number` or a numeric literal):**
   ```go
   elemTV := c.FreshVar(nil)
   arrayType := &TypeRefType{
       Name:     NewIdent("Array"),
       TypeArgs: []Type{elemTV},
   }
   typeVar.Instance = arrayType
   return elemTV
   ```
   Binds the type variable to `Array<T>` for a fresh `T`.

   **c. Non-literal string IndexKey:**
   Defer to a later phase. For now, return `ExpectedObjectError`. This covers
   `obj[strVar]` where `strVar` is type `string` (not a literal). Requirement 3
   says this should create a string index signature, but that can be added
   incrementally.

2. **`internal/checker/expand_type.go`** — `getObjectAccess` (~line 792,
   `PropertyKey` branch):
   Before the `UnknownPropertyError`, check `objType.Open`. If `true`:
   ```go
   propTV := c.FreshVar(nil)
   propTV.Widenable = true
   objType.Elems = append(objType.Elems, NewPropertyElem(NewStrKey(k.Name), propTV))
   return propTV
   ```
   This mutates the open `ObjectType` in place, adding the new property. The
   `append` adds to the end of `Elems` — ordering doesn't matter for property
   lookup since it's a linear scan by name.

   Apply the same logic in the `IndexKey` branch for string-literal keys that
   aren't found on the object.

### How subsequent accesses work

After the first access (`obj.bar`), `typeVar.Instance` points to the open
`ObjectType`. The next access (`obj.baz`) calls `getMemberType`, which calls
`Prune(typeVar)` → returns the `ObjectType`. The `ObjectType` case delegates to
`getObjectAccess`, which doesn't find `baz`, checks `Open`, and appends a new
`PropertyElem`.

Nested access (`obj.foo.bar = 5`) works recursively: `obj.foo` returns a fresh
`TypeVarType` (the value of property `foo`). Accessing `.bar` on that triggers
the `TypeVarType` case again, binding it to another open `ObjectType`. There is
no depth limit needed — each level creates a distinct type variable.

### Conflict detection: property access vs. numeric indexing

If the same parameter is used with both `obj.name` and `obj[0]`, the first
access binds the TypeVarType to an open `ObjectType`, and the second access
finds the `ObjectType` (via Prune) and tries numeric indexing on it. Since
`ObjectType` doesn't support numeric indexing, this produces an error naturally
(the `IndexKey` branch in `getObjectAccess` won't find a numeric match). The
error message should be improved in Phase 11 (Error Reporting) to mention the
conflict explicitly.

### Tests

New test file `internal/checker/tests/row_types_test.go`:

- **Read access:** `fn foo(obj) { let x = obj.bar }` — no errors, `obj` type
  includes `bar`.
- **Multiple reads:** `fn foo(obj) { let x = obj.bar; let y = obj.baz }` — two
  properties inferred.
- **Write access:** `fn foo(obj) { obj.bar = "hello":string }` — `bar` type
  unifies with `string`.
- **Read + write:** `fn foo(obj) { let x = obj.bar; obj.baz = 5:number }` —
  mixed access.
- **Nested access:** `fn foo(obj) { obj.foo.bar = 5:number }` — nested open
  objects.
- **Deeply nested:** `fn foo(obj) { obj.a.b.c = true }` — three levels.
- **Numeric index:** `fn foo(obj) { let x = obj[0] }` — binds to `Array<t1>`.
- **String literal index:** `fn foo(obj) { let x = obj["bar"] }` — equivalent
  to `obj.bar`.
- **Conflict:** `fn foo(obj) { let x = obj.name; let y = obj[0] }` — error
  (numeric index on object type).
- **Multiple parameters:** `fn foo(a, b) { a.x = 1:number; b.y = "hi":string }`
  — each parameter gets independent constraints.

---

## Phase 3: Unification for Open Object Types

**Requirements covered:** Sections 6a, 6b, 6c, 8a (passing to typed
functions), 8c (multiple parameters), 8d (aliasing).

**Goal:** Passing an open-typed function parameter to a typed function unifies
correctly without closing the type prematurely.

### Changes

1. **`internal/type_system/types.go`** — `TypeVarType` struct:
   - Add `IsParam bool` field. Set to `true` for type variables created by
     `inferFuncParams` for unannotated parameters. Used in `bind()` to decide
     whether to keep the type open when binding to a closed `ObjectType`.

2. **`internal/checker/infer_func.go`** — `inferFuncParams` (~line 26):
   - When creating a `FreshVar()` for an unannotated parameter, set
     `IsParam: true` on the resulting `TypeVarType`.

3. **`internal/checker/unify.go`** — `bind()` (~line 1609):
   The existing `bind()` already has a pattern for special handling before
   setting `Instance`: it checks `typeVar.FromBinding` (~lines 1661, 1694) and
   calls `removeUncertainMutability` when true. The `IsParam` check follows the
   same pattern, placed alongside `FromBinding`:
   ```go
   // At ~line 1661 (typeVar1 branch) and ~line 1694 (typeVar2 branch):
   if typeVar.IsParam {
       if closedObj, ok := targetType.(*ObjectType); ok && !closedObj.Open {
           // Open: true allows new properties to be added during inference.
           // The RestSpreadElem is the row variable for row polymorphism
           // (Phase 7) — without it, callers' extra properties would be
           // lost when the parameter is returned. For example:
           //   fn foo(obj) { bar(obj); return obj }
           //   foo({x: 1, y: 2})  // y preserved via RestSpreadElem
           // Open and RestSpreadElem serve different purposes and are both
           // needed here.
           openCopy := &ObjectType{
               Elems: append(slices.Clone(closedObj.Elems),
                   NewRestSpreadElem(c.FreshVar(nil))),
               Open: true,
           }
           typeVar.Instance = openCopy
           return nil
       }
   }
   // existing FromBinding check follows...
   ```
   - If the target is **not** an `ObjectType` (e.g. `number`, `string`), bind
     directly as usual — openness only applies to object types.
   - If the TypeVarType is already bound (via `Prune`), this path is not
     reached — `bind()` is only called for unbound variables.

4. **`internal/checker/unify.go`** — ObjectType-vs-ObjectType branch
   (~line 874):
   Add checks for `Open` at the top of this branch, after the existing nominal
   check (~line 877–888). Three paths:

   **a. Open-vs-closed (open type on either side):**
   1. Unify all shared property names pairwise (by iterating explicit
      `PropertyElem`s, `MethodElem`s, etc. from both types and matching by
      `Name`).
   2. Properties present in the closed type but **not** in the open type: append
      them as new `PropertyElem`s to the open type's `Elems`. These are
      requirements from the closed type that become part of the inferred type.
   3. Properties present in the open type but **not** in the closed type: allowed
      (structural subtyping — the inferred type has more properties than the
      closed type requires). No error.
   4. If the open type has a `RestSpreadElem` and the closed type has unmatched
      properties, those properties are added to the open type's `Elems` (step 2
      handles this). The `RestSpreadElem` remains — it represents possible
      additional properties from future accesses or callers.
   5. Keep `Open: true` on the open type. Closing only happens after the function
      body is fully inferred (Phase 6).

   **b. Open-vs-open (both types have `Open: true`):**
   1. Unify shared properties pairwise.
   2. Properties in one but not the other are added to both (merge).
   3. If both have `RestSpreadElem`s, unify their row variables by setting one's
      `Instance` to the other (so they share the same binding going forward).
   4. Both remain open.

   **c. Closed-vs-closed (existing path):**
   Existing code handles this. No changes needed. Ensure the existing logic
   remains the fallthrough path.

   **Note on the existing `RestSpreadElem` handling:** The current unifier
   (~line 927–1061) already collects leftover properties and unifies them with
   the rest type variable. The key change is: when `Open` is `true` on one side,
   add the other side's properties to the open type's `Elems` directly instead of
   binding the row variable to them.

5. **Section 6b — Multiple constraints via intersection:**
   When a type variable is constrained by multiple function calls
   (`bar(obj); baz(obj)`), the constraints compose via intersection. After the
   first call, the type variable is bound to an open `ObjectType`. The second
   call unifies that open `ObjectType` with the second function's parameter type.
   This goes through the open-vs-closed path (4a above), which merges properties.
   No additional intersection logic is needed.

   For non-object constraints (`takes_num(arg); takes_str(arg)`), the existing
   unifier handles `number` vs `string` normally (produces an error or `never`).

6. **Aliasing (Section 8d):**
   When `let alias = obj` is inferred, the assignment unifies `alias`'s type
   with `obj`'s type. Since both start as type variables, one's `Instance` is
   set to the other. Subsequent accesses on `alias` therefore constrain `obj`.
   This works automatically — no additional changes needed.

### Tests

- **Pass to typed function:**
  `fn bar(x: {bar: string}) -> string { return x.bar }; fn foo(obj) { bar(obj) }`
  — `obj` gets `{bar: string, ...R}` (open during inference).
- **Properties survive function call:**
  `fn foo(obj) { obj.z = true; bar(obj); obj.w = "hello" }` — all three
  properties inferred.
- **Multiple calls merge:**
  `fn foo(obj) { bar(obj); baz(obj) }` where `bar` expects `{x: number}` and
  `baz` expects `{y: string}` — `obj` gets `{x: number, y: string, ...R}`.
- **Non-object binding:**
  `fn takes_num(x: number) {...}; fn foo(obj) { takes_num(obj) }` — `obj` binds
  to `number` directly, not an open object.
- **Conflicting non-object constraints:**
  `fn foo(arg) { takes_num(arg); takes_str(arg) }` — `arg` becomes `never`
  (number & string = never).
- **Aliasing:**
  `fn foo(obj) { let alias = obj; alias.x = 1:number }` — `obj` inferred as
  `{x: number}`.
- **Multiple parameters:**
  `fn foo(a, b) { bar(a); baz(b) }` — each parameter independently constrained.
- **Open-vs-closed shared property unification:**
  `fn bar(x: {name: string}) {...}; fn foo(obj) { obj.name = "hi":string; bar(obj) }`
  — shared property `name` is unified pairwise; open type keeps extra properties
  if any.
- **Open-vs-closed extra properties in open type:**
  `fn bar(x: {a: number}) {...}; fn foo(obj) { obj.a = 1:number; obj.b = "hi":string; bar(obj) }`
  — `obj` has both `a` and `b`; `bar(obj)` succeeds (structural subtyping).

---

## Phase 4: Property Widening (Union Accumulation)

**Requirements covered:** Section 6d (property type widening).

**Goal:** When the same property on an inferred open object is assigned different
types, the property type widens to a union instead of producing an error.

### Changes

1. **`internal/checker/unify.go`** — `unifyWithDepth` function:
   Save the original (pre-Prune) types at the top of the function:
   ```go
   origT1, origT2 := t1, t2
   t1 = Prune(t1)
   t2 = Prune(t2)
   ```

   When a concrete-vs-concrete unification **fails** (returns errors), before
   returning those errors, check whether either `origT1` or `origT2` was a
   `TypeVarType` with `Widenable: true`. If so:
   ```go
   // origT1 is the Widenable TypeVarType, t1 is its pruned Instance (old type)
   // t2 is the new type that conflicts
   if !typeContains(t1, t2) {  // deduplication check
       origT1.(*TypeVarType).Instance = NewUnionType(nil, t1, t2)
   }
   return nil  // no error — widening succeeded
   ```

   **Deduplication:** Before widening, check whether the new type is already a
   member of the existing type (or union). If `t1` is already `"hello" | 5` and
   `t2` is `"hello"`, no widening needed. Use a helper `typeContains(haystack,
   needle)` that checks union members.

   **Gating:** Only widen if the TypeVarType has `Widenable: true`. For
   ordinary type variables (`Widenable: false`), conflicting bindings remain
   errors. This ensures the widening behavior is scoped to widenable
   types and doesn't change semantics for normal type inference.

   **Scope of Widenable:** Per Section 6d, widening applies to **all** type
   variables within inferred open objects: property value types, method parameter
   types, and method return types. All of these are created with
   `Widenable: true` in Phases 2 and 5.

2. **Helper function** — `typeContains(haystack Type, needle Type) bool`:
   - If `haystack` is a `UnionType`, check if any member equals `needle`.
   - Otherwise, check if `haystack` equals `needle`.
   - Place in `internal/checker/unify.go` or a utility file.

### How it works end-to-end

Assignment (`obj.bar = "hello"`) is handled in `inferExpr` for `BinaryExpr`
with `Op == ast.Assign` (infer_expr.go ~line 23–100). For a `MemberExpr` on
the left side, `inferExpr` calls `getMemberType` to get the property's type,
then unifies the right-hand side type with the left-hand side type via
`c.Unify(ctx, rightType, leftType)` (~line 97).

1. `obj.bar = "hello"` — `inferExpr` on the LHS `obj.bar` calls
   `getMemberType`, which (via the TypeVarType case or `getObjectAccess` on the
   open ObjectType) creates `PropertyElem{bar: t1}` where `t1` is a fresh
   `Widenable` TypeVarType, and returns `t1`. Unification of `"hello"` with
   `t1` sets `t1.Instance = "hello"`.
2. `obj.bar = 5` — `inferExpr` on `obj.bar` calls `getMemberType` →
   `getObjectAccess` finds the existing `bar` property, returns `t1`.
   Unification calls `unifyWithDepth(t1, number(5))`. `Prune(t1)` returns
   `"hello"`. Unifying `"hello"` with `5` fails. The check finds `origT1 = t1`
   is `Widenable`, so it replaces `t1.Instance` with `"hello" | 5`.
3. `obj.bar = true` — same flow, widens to `"hello" | 5 | true`.

### Tests

- **Sequential widening:**
  `fn foo(obj) { obj.bar = "hello"; obj.bar = 5 }` — `bar: "hello" | 5`.
- **Branch widening:**
  `fn foo(obj, cond) { if cond { obj.bar = "hello" } else { obj.bar = 5 } }` —
  same result.
- **Widening with different literals:**
  `fn foo(obj) { obj.bar = "hello"; obj.bar = "world" }` — `bar: "hello" | "world"`.
- **Deduplication:**
  `fn foo(obj) { obj.bar = "hello"; obj.bar = "hello" }` — `bar: "hello"` (not
  `"hello" | "hello"`).
- **Three-way widening:**
  `fn foo(obj) { obj.bar = "a"; obj.bar = 1; obj.bar = true }` —
  `bar: "a" | 1 | true`.
- **Normal type variable conflict still errors:**
  `let x: number = "hello"` — error, not widened (no `Widenable` flag).
- **Widening does not apply to non-widenable variables:**
  A regular TypeVarType that gets conflicting bindings should still error.

---

## Phase 5: Method Call Inference

**Requirements covered:** Section 2 (method call inference).

**Goal:** Calling a method on an inferred object creates a `FuncType` binding
with appropriate parameters and return type.

### Changes

1. **`internal/checker/infer_expr.go`** — `inferCallExpr` (~line 865 switch):
   Add a `*type_system.TypeVarType` case. The callee type comes from
   `c.inferExpr(ctx, expr.Callee)` which may return an unpruned TypeVarType
   (e.g. the fresh type variable for `obj.process`). The existing switch at
   line 865 (`switch t := calleeType.(type)`) does not prune first, so an
   unbound TypeVarType will reach this case directly. A bound TypeVarType
   (from a second call to the same method) would need a `Prune` call to reach
   the `FuncType` case — add `calleeType = type_system.Prune(calleeType)`
   before the switch, or handle both bound and unbound TypeVarTypes in the new
   case.

   The new case goes after the `IntersectionType` case (~line 923) and before
   the default case (~line 951):

   ```go
   case *type_system.TypeVarType:
       // Row inference: callee is a fresh type variable (e.g. from obj.process)
       // Create a fresh FuncType matching the call's argument count
       params := make([]*FuncParam, len(argTypes))
       for i := range argTypes {
           paramTV := c.FreshVar(nil)
           paramTV.Widenable = true
           params[i] = &FuncParam{Type: paramTV}
       }
       retTV := c.FreshVar(nil)
       retTV.Widenable = true
       freshFn := &FuncType{
           Params: params,
           Return: retTV,
       }
       t.Instance = freshFn
       return c.handleFuncCall(ctx, freshFn, expr, argTypes, provenance, errors)
   ```

   The fresh parameter types are unified with the supplied argument types inside
   `handleFuncCall`. The return type variable is returned as the call's result
   type.

   **Note:** All fresh type variables (params and return) have
   `Widenable: true`, enabling widening if the same method is called multiple
   times with different argument types (Phase 4 handles the widening).

2. **Interaction with Phase 2:** When `obj.process(42)` is inferred:
   - `inferExpr` on `obj.process` calls `getMemberType` → TypeVarType case →
     creates `PropertyElem{process: t_process}` on the open ObjectType, returns
     `t_process` (an unbound TypeVarType).
   - `inferCallExpr` receives `t_process` as the callee type, hits the new
     `TypeVarType` case.
   - `t_process.Instance` is set to the fresh `FuncType`.
   - Subsequent calls (`obj.process("hello")`) find `t_process` already bound
     to a `FuncType` via `Prune`. If `calleeType` is pruned before the switch
     (recommended), the `FuncType` case handles it normally, unifying parameter
     types — which widens via Phase 4 since the param TypeVarTypes are
     `Widenable`.

3. **MethodElem vs PropertyElem with FuncType:** The current approach stores
   methods as `PropertyElem` with a `FuncType` value, not as `MethodElem`. This
   is simpler and works because the property's type variable gets bound to the
   `FuncType`. We can refine this later if `MethodElem` semantics are needed.

4. **Multiple calls with different arities:** If the same method is called with
   different numbers of arguments (e.g. `obj.process(1)` then
   `obj.process(1, 2)`), the second call finds the existing `FuncType` and tries
   to unify. The arity mismatch would be handled by `handleFuncCall`'s existing
   parameter count checking. This would produce an error, which is correct — a
   method can't have two different arities.

### Tests

- **Basic method call:**
  `fn foo(obj) { let r = obj.process(42:number, "hello":string) }` — `process`
  is `fn(number, string) -> t1`.
- **Method parameter widening:**
  `fn foo(obj) { obj.process(42:number); obj.process("hello":string) }` —
  `process` is `fn(number | string) -> t1`.
- **Method return type widening:**
  `fn foo(obj) { let x: number = obj.getValue(); let y: string = obj.getValue() }`
  — `getValue` is `fn() -> number | string`.
- **Method + property on same object:**
  `fn foo(obj) { obj.x = 1:number; let r = obj.process(obj.x) }` — both
  property and method inferred.
- **Zero-arg method:**
  `fn foo(obj) { let r = obj.getData() }` — `getData` is `fn() -> t1`.

---

## Phase 6: Closing After Function Body Inference

**Requirements covered:** Section 5 (open vs. closed lifecycle), Section 11d
(interaction with closing).

**Goal:** After a function body is fully inferred, close all open object types on
parameters. Remove row variables that don't escape to callers.

### Changes

1. **`internal/checker/infer_func.go`** — After `inferFuncBody` returns
   (~line 190 in `inferFuncBodyWithFuncSigType`):
   Add a post-inference pass over `funcSigType.Params`:
   ```go
   for _, param := range funcSigType.Params {
       paramType := Prune(param.Type)
       if objType, ok := paramType.(*ObjectType); ok && objType.Open {
           objType.Open = false
           // Collect type var IDs in the return type
           returnVarIDs := collectTypeVarIDs(funcSigType.Return)
           // Remove RestSpreadElems whose row vars don't appear in return type
           filtered := make([]ObjTypeElem, 0, len(objType.Elems))
           for _, elem := range objType.Elems {
               if rest, ok := elem.(*RestSpreadElem); ok {
                   if tv, ok := Prune(rest.Value).(*TypeVarType); ok {
                       if !returnVarIDs[tv.ID] {
                           continue // remove this RestSpreadElem
                       }
                   }
               }
               filtered = append(filtered, elem)
           }
           objType.Elems = filtered
       }
   }
   ```

2. **Helper function** — `collectTypeVarIDs(t Type) map[int]bool`:
   Recursively walk a type tree and collect all `TypeVarType` IDs encountered.
   Handles `UnionType`, `ObjectType` (including nested `PropertyElem`,
   `MethodElem`, `RestSpreadElem`), `FuncType` (params + return), `TypeRefType`
   (type args), `TupleType` (elements), `IntersectionType`, etc.

   Place in `internal/type_system/types.go` or a new utility file like
   `internal/checker/type_utils.go`:

   ```go
   func collectTypeVarIDs(t Type) map[int]bool {
       ids := map[int]bool{}
       var walk func(Type)
       walk = func(t Type) {
           t = Prune(t)
           switch t := t.(type) {
           case *TypeVarType:
               ids[t.ID] = true
           case *ObjectType:
               for _, elem := range t.Elems {
                   switch e := elem.(type) {
                   case *PropertyElem:
                       walk(e.Value)
                   case *MethodElem:
                       walk(e.Fn)
                   case *RestSpreadElem:
                       walk(e.Value)
                   // ... other elem types
                   }
               }
           case *UnionType:
               for _, m := range t.Types {
                   walk(m)
               }
           case *FuncType:
               for _, p := range t.Params {
                   walk(p.Type)
               }
               walk(t.Return)
           case *TypeRefType:
               for _, arg := range t.TypeArgs {
                   walk(arg)
               }
           case *TupleType:
               for _, e := range t.Elems {
                   walk(e)
               }
           case *IntersectionType:
               for _, m := range t.Types {
                   walk(m)
               }
           }
       }
       walk(t)
       return ids
   }
   ```

3. **Scope:** Closing happens per-function. Each function's parameter types are
   closed after **that** function's body is inferred. Nested functions close
   independently — an inner function's parameters are closed after the inner
   body is inferred, even though the outer function is still being processed.
   This works because `inferFuncBodyWithFuncSigType` is called recursively for
   nested function expressions.

4. **Unbound RestSpreadElems:** When a `RestSpreadElem`'s row variable is never
   bound (no callers have instantiated it yet) and it doesn't appear in the
   return type, it is simply removed. The row variable is abandoned — it was
   never needed.

### Tests

- **Closed after inference:**
  `fn foo(obj) { obj.bar = 5:number }` — after inference, `obj` type is
  `{bar: number}` (closed, no `RestSpreadElem`).
- **RestSpreadElem preserved when in return type:**
  `fn foo(obj) { obj.x = 1:number; return obj }` — `RestSpreadElem` kept
  (tested further in Phase 7).
- **Multiple params, each closed independently:**
  `fn foo(a, b) { a.x = 1:number; b.y = "hi":string }` — both closed.
- **Nested function:**
  `fn outer(a) { fn inner(b) { b.x = 1:number }; a.y = "hi":string }` — each
  closed after its own body.

---

## Phase 7: Row Polymorphism

**Requirements covered:** Section 11 (row polymorphism), Section 8b/8g (return
type propagation).

**Goal:** When an inferred parameter is returned, its row variable becomes a type
parameter on the function, enabling callers to preserve extra properties.

### Changes

1. **`internal/checker/infer_func.go`** — Extend the Phase 6 closing logic:
   Instead of just removing `RestSpreadElem`s in the return type, **promote**
   their row variables to type parameters:
   ```go
   returnVarIDs := collectTypeVarIDs(funcSigType.Return)
   for _, elem := range objType.Elems {
       if rest, ok := elem.(*RestSpreadElem); ok {
           if tv, ok := Prune(rest.Value).(*TypeVarType); ok {
               if returnVarIDs[tv.ID] {
                   // Promote to type parameter
                   funcSigType.TypeParams = append(funcSigType.TypeParams, &TypeParam{
                       Name:       fmt.Sprintf("R%d", tv.ID),
                       Constraint: nil,
                   })
                   // RestSpreadElem remains in both param and return types,
                   // referencing the same type variable.
               }
           }
       }
   }
   ```

2. **`TypeParam` naming:** Row type parameters get generated names like `R1`,
   `R2` based on their type variable IDs. These names appear in printed function
   signatures but are internal — users don't write them.

3. **Call-site instantiation** — `internal/checker/infer_expr.go`
   `handleFuncCall` (~line 957):
   The existing generic function instantiation mechanism creates fresh type
   variables for each `TypeParam` and substitutes them throughout the function
   type via `SubstituteTypeParams`. This works for row type parameters too:
   - A fresh type variable is created for the row parameter `R`.
   - During argument unification, the caller's extra properties flow into `R`.
   - The return type shares the same `R`, so extra properties appear in the
     result.

   **Confirmed:** `SubstituteTypeParams` (substitute.go ~line 98) uses the
   `Accept()` visitor pattern. `RestSpreadElem.Accept()` (types.go ~line 1229)
   calls `r.Value.Accept(v)`, so substitution correctly walks into
   `RestSpreadElem` values. No changes needed.

4. **Multiple row variables:** When multiple parameters each have row variables
   in the return type (e.g. `fn merge(a, b) { return {...a, ...b} }`), each gets
   its own type parameter. This is naturally handled since each parameter has a
   distinct `RestSpreadElem` with a distinct row variable ID.

### Tests

- **Basic row polymorphism:**
  ```esc
  fn foo(obj) { obj.x = 1:number; return obj }
  let r = foo({x: 1, y: 2})
  ```
  — `r` type is `{x: number, y: number}`.

- **Multiple extra properties:**
  ```esc
  fn foo(obj) { obj.x = 1:number; return obj }
  let r = foo({x: 1, y: 2, z: "hi"})
  ```
  — `r` has all three properties.

- **No return — row variable removed:**
  `fn foo(obj) { obj.x = 1:number }` — no type parameter, `RestSpreadElem`
  removed.

- **Derived return (property of param):**
  `fn foo(obj) { return obj.x }` — return type is `t1` (the type of `x`), not
  the full object. Row variable does not appear in return type, so it's removed.

- **Return in a structure:**
  `fn foo(obj) { return {y: obj.x} }` — similar; row variable doesn't escape.

- **Multiple row-polymorphic parameters (requires Phase 10 for spread):**
  ```esc
  fn merge(a, b) { return {...a, ...b} }
  let r = merge({x: 1}, {y: "hi"})
  ```
  — `r: {x: number, y: string}`.

---

## Phase 8: Destructuring Patterns on Parameters

**Requirements covered:** Section 8f (destructuring patterns).

**Goal:** Destructured parameters with rest elements get a `RestSpreadElem` for
row polymorphism.

### Changes

1. **`internal/checker/infer_func.go`** — `inferFuncParams`:
   After `inferPattern` creates a closed `ObjectType` from a destructuring
   pattern, check if the pattern has a rest element and the parameter lacks a
   type annotation. If so:
   ```go
   if hasRestElement && param.TypeAnn == nil {
       rowTV := c.FreshVar(nil)
       objType.Elems = append(objType.Elems, NewRestSpreadElem(rowTV))
       // objType.Open remains false — the explicit properties are fixed
       // by the pattern, and the parameter itself isn't accessible by name.
       // The rest binding's type is rowTV.
   }
   ```

2. **Rest binding type:** The rest element's binding should have type `rowTV`
   (the same row variable). When checking the pattern, the rest binding in the
   `bindings` map should have its type set to `rowTV`. This connects the rest
   binding to the row variable so that when `R` is resolved (at a call site),
   the rest binding's type reflects the remaining properties.

3. **Without rest element:** The type stays closed with no `RestSpreadElem`.
   The pattern fully specifies the expected properties.

4. **Open vs. closed:** Destructured parameters are always **closed**
   (`Open: false`) regardless of rest elements. The pattern fully specifies
   explicit properties, and the parameter itself isn't accessible by name (only
   the bindings are), so no new explicit properties can be added during
   inference. The `RestSpreadElem` provides row polymorphism for extra properties
   passed by callers, while `Open: false` prevents the checker from adding
   properties during inference.

### Tests

- **Destructuring without rest:**
  `fn foo({bar, baz}) { ... }` — `{bar: t1, baz: t2}` (closed, no rest).
- **Destructuring with rest:**
  `fn foo({bar, ...rest}) { ... }` — `{bar: t1, ...R}` (closed, with rest).
  `rest` has type `R`.
- **Calling with extra properties:**
  ```esc
  fn foo({bar, ...rest}) { return rest }
  let r = foo({bar: 1, extra: "hi"})
  ```
  — `r` includes `{extra: "hi"}`.
- **Rest without type annotation only:**
  `fn foo({bar, ...rest}: {bar: number, ...}) { ... }` — if a type annotation
  is present, use it as-is. Only add a `RestSpreadElem` when the parameter is
  unannotated.

---

## Phase 9: Optional Chaining

**Requirements covered:** Section 4 (optional chaining).

**Goal:** `obj?.bar` on a TypeVarType infers `obj: {bar: t1} | null | undefined`.

### Changes

1. **`internal/checker/expand_type.go`** — `getMemberType`, TypeVarType case:
   When the key is a `PropertyKey` with `OptChain: true`:
   ```go
   propTV := c.FreshVar(nil)
   propTV.Widenable = true
   rowTV := c.FreshVar(nil)
   openObj := NewObjectType(nil, []ObjTypeElem{
       NewPropertyElem(NewStrKey(k.Name), propTV),
       NewRestSpreadElem(rowTV),
   })
   openObj.Open = true
   // Wrap in union with null and undefined
   typeVar.Instance = NewUnionType(nil, openObj, NewNullType(nil), NewUndefinedType(nil))
   // Return propTV | undefined (the ?. expression itself may produce undefined)
   return NewUnionType(nil, propTV, NewUndefinedType(nil))
   ```

   The null/undefined type constructors are `NewNullType(nil)` and
   `NewUndefinedType(nil)` (or equivalent — check `types.go` for exact names).

2. **Subsequent accesses on the union:** After `obj?.bar`, `typeVar.Instance` is
   `{bar: t1, ...R} | null | undefined`. If the user subsequently accesses
   `obj.baz` (non-optional), `Prune(typeVar)` returns the union, and
   `getMemberType` hits the `UnionType` case → `getUnionAccess`.

   **How `getUnionAccess` handles this:** `getUnionAccess` (expand_type.go
   ~line 827) already handles unions containing null/undefined:
   - It calls `c.getDefinedElems(unionType)` to filter out null/undefined
     members.
   - If null/undefined are present **without** optional chaining (`?.`), it
     reports an error — which is correct, since `obj.baz` on a nullable type
     should be flagged.
   - If null/undefined are present **with** optional chaining, it accesses
     properties on the defined (non-null/undefined) members only, and appends
     `undefined` to the result type.

   This means:
   - `obj?.baz` on `{bar: t1} | null | undefined` works: accesses the open
     ObjectType, adds `baz`, returns `t2 | undefined`.
   - `obj.baz` (non-optional) on the same union correctly errors — the user
     should use `?.` since the type is nullable. No special handling needed.

3. **Nested optional chaining (`a?.b?.c`):**
   - `a?.b` binds `a` to `{b: t_b, ...R} | null | undefined`, returns
     `t_b | undefined`.
   - `(t_b | undefined)?.c`: the `?.` operator strips `undefined` from the
     receiver. The remaining type is `t_b`, which is a `TypeVarType`. This
     triggers the TypeVarType case again, binding `t_b` to
     `{c: t_c, ...R2} | null | undefined`.
   - Result: `a: {b: {c: t_c, ...R2} | null | undefined, ...R} | null | undefined`,
     expression type `t_c | undefined`.

4. **Return type of optional chaining expression:** The expression `obj?.bar`
   has type `propTV | undefined`. The inferred property type itself is just
   `propTV` (not wrapped with undefined) — the `| undefined` is a property of
   the access expression, not the property. This matches existing optional
   chaining semantics.

### Tests

- **Basic optional chaining:**
  `fn foo(obj) { let x = obj?.bar }` — `obj: {bar: t1} | null | undefined`,
  `x: t1 | undefined`.
- **Nested optional:**
  `fn foo(a) { let x = a?.b?.c }` — nested nullability as described above.
- **Mix of optional and non-optional:**
  `fn foo(obj) { let x = obj?.bar; let y = obj.baz }` — error on `obj.baz`
  because `obj` is `{bar: t1} | null | undefined` and non-optional `.` on a
  nullable type is an error (`getUnionAccess` reports it).
- **All optional:**
  `fn foo(obj) { let x = obj?.bar; let y = obj?.baz }` — `obj` is
  `{bar: t1, baz: t2} | null | undefined` (second `?.` adds `baz` to the open
  ObjectType inside the union).

---

## Phase 10: Object Spread & Multiple RestSpreadElems

**Requirements covered:** Section 12 (object spread, multiple RestSpreadElems).

**Goal:** Handle `ObjSpreadExpr` in object literals and support multiple
`RestSpreadElem`s in unification.

### Changes

1. **`internal/checker/infer_expr.go`** — ObjectExpr inference (~line 283):
   Add an `*ast.ObjSpreadExpr` case in the element loop:
   ```go
   case *ast.ObjSpreadExpr:
       sourceType, exprErrors := c.inferExpr(ctx, elem.Arg)
       errors = append(errors, exprErrors...)
       typeElems = append(typeElems, NewRestSpreadElem(sourceType))
   ```
   Multiple spreads create multiple `RestSpreadElem`s in the resulting
   `ObjectType`.

   **Property override semantics (Section 12d):** In JavaScript, later spreads
   override earlier ones for shared property names. When building the
   `ObjectType` from `ObjectExpr`:
   - Track property names seen so far.
   - If a later `PropertyExpr` has the same name as an earlier one (or one from
     an earlier spread), the later one shadows the earlier one.
   - For the type representation, `RestSpreadElem`s are kept in source order.
     During expansion/unification, they are merged left-to-right with later
     elements overriding earlier ones.

   **Spread of TypeVarType:** If the spread source is a TypeVarType (e.g.
   `{...obj}` where `obj` is unannotated), the `RestSpreadElem` value is the
   TypeVarType itself. When it's later resolved to an open ObjectType, the
   RestSpreadElem's properties become visible.

2. **`internal/checker/unify.go`** — Replace the two-rest-elem error
   (~line 957):
   Implement distribution logic per Section 12c:

   **When one rest is bound, the other unbound:**
   ```go
   // R1 is bound to {y: string}, R2 is unbound
   // Target has remaining properties: {y: string, z: boolean}
   // Subtract R1's properties: {z: boolean}
   // Bind R2 to {z: boolean}
   remaining := subtractProperties(r1Props, targetRemaining)
   r2TypeVar.Instance = NewObjectType(nil, remaining)
   ```

   **When both are bound:**
   Merge all properties from both rest elements with the explicit properties.
   Unify the merged result with the target type.

   **When both are unbound:**
   If there are remaining properties to distribute, report an error — the system
   cannot determine which rest element should receive which properties. If there
   are no remaining properties, both stay unbound.

3. **Subtraction helper:**
   ```go
   func subtractProperties(known, target []ObjTypeElem) []ObjTypeElem {
       knownNames := map[ObjTypeKey]bool{}
       for _, elem := range known {
           if prop, ok := elem.(*PropertyElem); ok {
               knownNames[prop.Name] = true
           }
       }
       result := []ObjTypeElem{}
       for _, elem := range target {
           if prop, ok := elem.(*PropertyElem); ok {
               if !knownNames[prop.Name] {
                   result = append(result, elem)
               }
           }
       }
       return result
   }
   ```

4. **`internal/checker/expand_type.go`** — `getObjectAccess`:
   When a property is not found in explicit elements (and the object is **not**
   open), check each `RestSpreadElem`'s resolved type (via Prune). If it
   resolves to an `ObjectType`, search its elements recursively. Search in
   **reverse** order (rightmost rest element first) to respect override
   semantics. Return the first match found.

   Note: property addition on open objects (Phase 2) is gated by `Open`, not by
   the presence of `RestSpreadElem`s. These are orthogonal.

5. **Audit existing code for multi-rest assumptions:**
   - **Type printing/`String()`**: print `{x: t1, ...R1, ...R2}` — each
     `RestSpreadElem` shown separately.
   - **`SubstituteTypeParams`**: should already walk all `Elems` including
     multiple `RestSpreadElem`s. Verify.
   - **Unifier ObjectType branch**: the existing code collects "the"
     `RestSpreadElem` — update to collect all of them.

### Tests

- **Basic spread:**
  `let r = {...{x: 1}, y: 2}` — `r: {x: number, y: number}`.
- **Spread with inferred type:**
  `fn foo(obj) { return {...obj, extra: 1:number} }` — return type has
  `RestSpreadElem` from `obj`.
- **Multiple spreads:**
  `fn merge(a, b) { return {...a, ...b} }` — two row variables, two type params.
- **Override semantics:**
  `let r = {...{x: 1}, ...{x: "hi"}}` — `r.x` is `string` (rightmost wins).
- **Explicit property overrides spread:**
  `let r = {...{x: 1}, x: "hi"}` — `r.x` is `string`.
- **Spread of TypeVarType:**
  `fn foo(obj) { let r = {...obj} }` — `RestSpreadElem{Value: typeVar(obj)}`.
- **Property access through RestSpreadElem:**
  ```esc
  let base = {x: 1, y: 2}
  let extended = {...base, z: 3}
  let v = extended.x  // found via RestSpreadElem
  ```

---

## Phase 11: Error Reporting

**Requirements covered:** Section 9 (error reporting).

**Goal:** Produce clear, contextual error messages for row-type inference
failures.

### Changes

1. **Provenance on inferred PropertyElems:**
   When creating a `PropertyElem` during row inference (Phase 2), attach the
   span of the property access that caused it. This enables error messages to
   reference the source location where a property was inferred.

2. **Section 9a — Missing property at call site:**
   After closing (Phase 6), the parameter type is closed. If a caller passes an
   object missing a required property, the existing `UnknownPropertyError` or
   unification error fires. Enhance the error message to mention that the
   property was inferred:
   > Argument of type `{bar: "hi"}` is missing property `baz` required by
   > parameter `obj` of `foo`. Property `baz` is required because it is assigned
   > at <source location>.

3. **Section 9b — Numeric indexing vs. property access conflict:**
   Add a new error type `IndexingConflictError` in
   `internal/checker/errors.go`:
   ```go
   type IndexingConflictError struct {
       Param        string
       PropertySpan ast.Span  // location of property access
       IndexSpan    ast.Span  // location of numeric index
   }
   ```
   Message:
   > Cannot index parameter `obj` with a numeric index because it was already
   > constrained to an object type by property access `obj.name` at <location>.
   > Consider adding a type annotation to `obj`.

   Detect this in `getMemberType` when a numeric `IndexKey` is used on an
   `ObjectType` that was created by row inference (check if the ObjectType has
   `Open: true` or was recently closed from an open state — may need a flag).

4. **Section 9c — Property type mismatch:**
   When an inferred property type conflicts with a closed type's property during
   unification:
   > Type `number` is not assignable to type `string` for property `bar` of
   > parameter `obj`. Property `bar` was inferred as `number` from assignment
   > at <location>.

5. **Section 9d — When to suggest annotations:**
   - **Suggest annotation**: indexing conflicts (9b), surprising inferred types.
   - **Don't suggest**: missing properties (9a), simple type mismatches (9c) —
     the fix is to change the call site, not annotate the parameter.

### Tests

- Each error scenario (9a, 9b, 9c) should have a test verifying:
  - The correct error type is returned.
  - The error message references the right source locations.
  - Annotation suggestions are present/absent as appropriate.

---

## Scope and Limitations

**Requirements covered:** Section 10.

The following are explicitly **out of scope** for this implementation:

- **Recursive object types:** Inferring that `obj.child` has the same type as
  `obj`. These require explicit annotations.
- **Conditional property access:** Inferring that a property exists only in
  certain branches. All accessed properties are required unconditionally.
- **Non-literal string index signatures:** `obj[strVar]` where `strVar: string`.
  Deferred from Phase 2.
- **Explicit row type parameters:** `fn foo<R>(obj: {x: number, ...R})`. The
  syntax already parses, but `R` would need a type constraint. Deferred.
- **Row variables in generic type arguments:** `Promise<{x: number, ...R}>`.
  Deferred.

These can be addressed in follow-up work.

---

## Phase Dependencies

```
Phase 1: Type System Extensions
└── Phase 2: Property Access
    ├── Phase 3: Unification
    │   ├── Phase 4: Widening
    │   │   └── Phase 5: Method Calls
    │   │       └── Phase 6: Closing
    │   │           └── Phase 7: Row Polymorphism
    │   │               └── Phase 8: Destructuring
    │   └── Phase 10: Object Spread (can start here or after Phase 8)
    └── Phase 9: Optional Chaining (can start here or after Phase 8)

All phases above feed into:
└── Phase 11: Error Reporting
```

**Notes on parallelism:**
- The main trunk is Phases 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8.
- Phases 9 (Optional Chaining) and 10 (Object Spread) can each begin as soon as
  their minimum dependency is met: Phase 9 requires Phase 2, Phase 10 requires
  Phase 3. Alternatively, they can be deferred until after Phase 8.
- Phase 8 (Destructuring) depends on Phases 6/7 for row variable promotion.
- Phase 11 (Error Reporting) depends on all previous phases.

---

## Key Risks

1. **Widening (Phase 4):** Requires saving pre-Prune references in
   `unifyWithDepth` to detect `Widenable` variables after pruning. Must be
   carefully gated so it only fires for widenable variables, not normal type
   inference.

2. **Optional chaining (Phase 9):** Wrapping the type variable in a union
   (`T | null | undefined`) means subsequent accesses go through
   `getUnionAccess`, which must correctly find and extend the open `ObjectType`
   inside the union. May be complex — consider deferring if it destabilizes
   earlier phases.

3. **Multiple RestSpreadElems (Phase 10):** Property distribution across
   unbound rest elements is inherently ambiguous. Keep the error case for
   ambiguous distributions.

4. **Snapshot tests:** Adding `Open` to `ObjectType` shouldn't change printed
   types (it's an internal flag), but changes to `RestSpreadElem` handling in
   unification may affect how types are resolved and printed. Run
   `UPDATE_SNAPS=true go test ./...` after each phase.

5. **Backwards compatibility:** The new `Open`, `Widenable`, and `IsParam`
   fields default to `false` (Go zero values), so existing code paths are
   unaffected. Still, audit all code that constructs or pattern-matches on
   `ObjectType` and `TypeVarType` to ensure the new fields don't cause
   unexpected behavior.

---

## Critical Files

| File | Phases |
|------|--------|
| `internal/type_system/types.go` | 1, 6 (`collectTypeVarIDs` helper) |
| `internal/checker/expand_type.go` | 2, 9, 10 |
| `internal/checker/unify.go` | 3, 4, 10 |
| `internal/checker/infer_func.go` | 3, 6, 7, 8 |
| `internal/checker/infer_expr.go` | 5, 10 |
| `internal/checker/errors.go` | 11 |
| `internal/checker/tests/row_types_test.go` | All (new file) |

---

## Verification

After each phase, run:
```bash
go test ./internal/checker/... -run TestRowTypes    # new tests
go test ./internal/type_system/...                  # type system tests
UPDATE_SNAPS=true go test ./internal/checker/...    # if snapshots change
go test ./...                                       # full suite for regressions
```
