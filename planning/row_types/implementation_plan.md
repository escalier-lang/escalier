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

### Recent Merges from `main` (2026-04-06)

The following PRs have been merged and provide infrastructure that the row types
work builds upon. Affected phases are annotated below.

| PR | Summary | Relevant phases |
|----|---------|-----------------|
| #379 (fe20b6a) | **Function generalization** — `GeneralizeFuncType` and `collectUnresolvedTypeVars` in `generalize.go`. Promotes unresolved type vars to type params. | Phase 6 ✅ (used existing `collectUnresolvedTypeVars`), Phase 7 (row var promotion via `GeneralizeFuncType`) |
| #380 (84c1ec5) | **Callback inference** — `TypeVarType` case in `inferCallExpr` creates synthetic `FuncType`s; `resolveCallSites` defers binding; `deepCloneType` added; `InFuncBody`/`CallSites`/`CallSiteTypeVars` on Context. | Phase 5 ✅ (method calls on inferred objects work naturally via existing TypeVarType case; `mut?` stripping added for clean param types) |
| #382 (7013ec2) | **Probe-then-commit unification** — `deepCloneType` used in union/intersection unification to avoid partial TypeVar mutation. Constraint propagation in `bind()`. | Phase 3, Phase 4 (unification robustness) |
| #384 (d7072ec) | **`throws never`** — missing `throws` clause = `NeverType`. `IsNeverType` helper. `FuncParam.String()` extracted. | Phase 1 (types.go changes), Phase 5 ✅ (synthetic FuncTypes use `NewNeverType(nil)` for throws) |

---

## Phase 1: Core Type System Extensions ✅

**Requirements covered:** Definitions, Section 7 (Option C), prerequisites for
Sections 1–6.

**Goal:** Add the `Open` field to `ObjectType`, `Widenable` and `IsParam` flags
to `TypeVarType`, and `Written` flag to `PropertyElem` so later phases can build
on them. Confirm that `MutabilityUncertain` already exists.

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

3. **`internal/type_system/types.go`** — `PropertyElem` struct (~line 1057):
   - Add `Written bool` field. Set to `true` when a property is assigned to
     during inference. Used at closing time (Phase 6) to determine whether the
     inferred object type needs `mut`. Go zero-value is `false`, so existing
     code is safe.

4. **`internal/type_system/types.go`** — `TypeVarType` struct (~line 106):
   - Add `IsParam bool` field. Set to `true` for type variables created by
     `inferFuncParams` for unannotated parameters. Used in `bind()` (Phase 3)
     to decide whether to keep the type open when binding to a closed
     `ObjectType`. Go zero-value is `false`, so existing code is safe.

5. **`internal/type_system/types.go`** — `ObjectType` printing (`String()` or
   equivalent):
   - `ObjectType.Open` is an internal-only flag that controls whether the
     property set can grow during inference. It does not need to appear in the
     printed representation. `RestSpreadElem` models row variables and spread
     sources — it does **not** indicate openness (the two are orthogonal per
     the Definitions section in the requirements).
   - Audit all call sites that construct `ObjectType` to ensure they default to
     `Open: false` (Go zero-value is `false`, so existing code is safe).

6. **No changes needed for `MutabilityUncertain`:** The `MutabilityUncertain`
   constant already exists in `types.go` (alongside `MutabilityMutable`). It is
   used in Phases 2 and 3 to wrap inferred `ObjectType`s so that both reads and
   writes are permitted during inference, with mutability resolved at closing
   time (Phase 6).

### Notes on merged changes

- **#384** modified `types.go` extensively: `FuncType.String()` now hides
  `throws never` (uses `IsNeverType`), `FuncParam.String()` was extracted, and
  `ObjectType.String()` was updated for setter/getter printing. These changes
  don't conflict with the new fields above but affect line numbers — verify
  exact insertion points before implementing.
- **#380** added synthetic `FuncParam` creation with `NewIdentPat` pattern names
  (e.g. `arg0`, `arg1`). The `FuncParam` struct gained no new fields, but the
  new `String()` method should be tested with the `Written` field if it affects
  display.

### Tests

- Compile and run `go test ./internal/type_system/...` to verify the new fields
  don't break existing tests.
- No behavioral changes yet.

---

## Phase 2: Property Access Inference on TypeVarType ✅

**Requirements covered:** Section 1 (property access inference), Section 3
(array indexing), Section 8e (nested property access).

**Goal:** Accessing `obj.bar` on an unannotated parameter binds the type
variable to an open `ObjectType` instead of producing `ExpectedObjectError`.

> **Note (2026-04-06):** The `InFuncBody` flag on `Context` (added in #380)
> can be used to determine whether we're inside a function body, which is useful
> for gating row-type inference behavior (e.g. only creating open ObjectTypes
> for property access during function body inference). Also, `inferExpr` for
> `Ident` now preserves TypeVarType pointer identity (doesn't call `.Copy()`)
> which is essential for row types — property accesses on a parameter variable
> must flow constraints back to the original TypeVar in the function signature.

### Implementation (completed 2026-04-06)

**Deviation from plan:** The implementation does **not** wrap open `ObjectType`s
in `MutabilityType{Uncertain}`. Instead, the assignment handler in
`infer_expr.go` directly checks for open `ObjectType`s (via the
`markPropertyWritten` helper) and bypasses the immutability error for them.
Mutability is resolved during `closeOpenParams` (Phase 6) via the
`finalizeOpenObject` helper based on the `Written` flag. This avoids the
complexity of `MutabilityType` unwrapping during inference and simplifies the
property access path.

**Changes made:**

1. **`internal/checker/expand_type.go`** — `getMemberType`:
   - Added `*type_system.TypeVarType` as a break condition in the expansion loop
     (~line 490) so we don't needlessly call `ExpandType`.
   - Added a `*type_system.TypeVarType` case in the switch with three sub-cases:

   **a. PropertyKey or string-literal IndexKey:**
   Uses the `newOpenObjectWithProperty(name)` helper which creates an open
   `ObjectType` with one `PropertyElem` (widenable fresh TypeVar) and one
   `RestSpreadElem` (fresh row variable). Sets `typeVar.Instance = openObj`
   directly (no `MutabilityType` wrapper).

   **b. Numeric IndexKey (key type is `number` or a numeric literal):**
   Binds the type variable to `Array<T>` for a fresh `T`.

   **c. Non-literal string IndexKey:**
   Returns `ExpectedObjectError` (deferred).

2. **`internal/checker/expand_type.go`** — `getObjectAccess`:
   - **PropertyKey branch:** Before `UnknownPropertyError`, checks `objType.Open`.
     If `true`, uses the `addPropertyToOpenObject(objType, name)` helper to
     append a new widenable property and return its type.
   - **IndexKey branch (string literal):** Same logic for string-literal keys
     not found on the object. Also added handling for `GetterElem`, `SetterElem`,
     `ConstructorElem`, `CallableElem`, and `RestSpreadElem` in the element loop.
   - Added `RestSpreadElem` as a `continue` case in the PropertyKey element loop.

3. **`internal/checker/infer_expr.go`** — Assignment handler:
   - **MemberExpr branch:** Uses `markPropertyWritten(pruned, propName)` to set
     `Written: true` on the matching `PropertyElem` of an open `ObjectType`.
     If not an open object, falls through to the existing immutability check.
   - **IndexExpr branch:** Same logic for string-literal index assignments.
   - **Ident case (~line 278):** Preserves pointer identity for open `ObjectType`s
     (avoids `.Copy()`) so that property additions during inference flow back to
     the original type.

4. **Helper functions** (in `expand_type.go`):
   - `newOpenObjectWithProperty(name)` — creates open ObjectType with one property
     and a rest-spread row variable.
   - `addPropertyToOpenObject(objType, name)` — appends a widenable property to
     an existing open ObjectType.
   - `markPropertyWritten(prunedType, propName)` — finds a property by name on
     an open ObjectType (handles both bare and MutabilityType-wrapped) and sets
     `Written = true`.

5. **`internal/checker/generalize.go`** — `GeneralizeFuncType`:
   Added a pre-generalization pass that resolves mutability on open objects:
   - If any `PropertyElem` has `Written: true`, wraps the open object in
     `MutabilityType{Mutable}` and strips `mut?` from written property values.
   - If no properties were written, the object remains unwrapped (immutable).

6. **`internal/checker/generalize.go`** — `deepCloneType`:
   Preserves `Open` field when cloning `ObjectType`.
   This tracks which properties were assigned to, used during generalization
   to determine whether the inferred type needs `mut`.

### How subsequent accesses work

After the first access (`obj.bar`), `typeVar.Instance` points to the open
`ObjectType`. The next access (`obj.baz`) calls `getMemberType`, which calls
`Prune(typeVar)` → returns the `ObjectType`. The `ObjectType` case delegates to
`getObjectAccess`, which doesn't find `baz`, checks `Open`, and calls
`addPropertyToOpenObject` to append a new `PropertyElem`.

Idempotent access (`obj.bar` accessed twice) works correctly: the second access
finds the existing `PropertyElem` in `getObjectAccess` and returns the same
type variable. Mixed access (`obj.bar` then `obj["bar"]`) also works — both
resolve to the same `PropertyElem` by name.

Nested access (`obj.foo.bar`) works recursively: `obj.foo` returns a fresh
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

### Tests (implemented)

Test file: `internal/checker/tests/row_types_test.go`

**`TestRowTypesPropertyAccess`** — table-driven tests verifying inferred types:
- **ReadAccess:** `fn foo(obj) { return obj.bar }` →
  `fn <T0, T1>(obj: {bar: T0, ...T1}) -> T0`
- **MultipleReads:** `fn foo(obj) { return [obj.bar, obj.baz] }` →
  `fn <T0, T1, T2>(obj: {bar: T0, ...T1, baz: T2}) -> [T0, T2]`
- **WriteAccess:** `fn foo(obj) { obj.bar = "hello" }` →
  `fn <T0>(obj: mut {bar: string, ...T0}) -> void`
- **ReadAndWrite:** `fn foo(obj) { val x = obj.bar; obj.baz = 5 }` →
  `fn <T0, T1>(obj: mut {bar: T0, ...T1, baz: number}) -> void`
- **NestedAccess:** `fn foo(obj) { return obj.foo.bar }` →
  `fn <T0, T1, T2>(obj: {foo: {bar: T0, ...T1}, ...T2}) -> T0`
- **MultipleParams:** `fn foo(a, b) { return [a.x, b.y] }` →
  `fn <T0, T1, T2, T3>(a: {x: T0, ...T1}, b: {y: T2, ...T3}) -> [T0, T2]`
- **DeeplyNested:** `fn foo(obj) { return obj.a.b.c }` →
  `fn <T0, T1, T2, T3>(obj: {a: {b: {c: T0, ...T1}, ...T2}, ...T3}) -> T0`
- **NumericIndex:** `fn foo(obj) { return obj[0] }` →
  `fn <T0>(obj: Array<T0>) -> T0`
- **StringLiteralIndex:** `fn foo(obj) { return obj["bar"] }` →
  `fn <T0, T1>(obj: {bar: T0, ...T1}) -> T0`
- **MultipleStringLiteralIndexes:** both `obj["bar"]` and `obj["baz"]` inferred.
- **StringLiteralIndexWrite:** `obj["bar"] = "hello"` → `mut` object.
- **StringLiteralIndexReadAndWrite:** mixed bracket read/write.
- **MixedDotAndBracketAccess:** `obj.bar` and `obj["baz"]` on same object.
- **MixedDotReadBracketWrite:** dot read + bracket write → `mut`.
- **MultipleNumericIndexes:** `obj[0]` and `obj[1]` → same `Array<T0>`.
- **IdempotentPropertyAccess:** `obj.bar` accessed twice → same type variable.
- **IdempotentMixedAccess:** `obj.bar` and `obj["bar"]` → same type variable.

**`TestRowTypesErrors`** — tests verifying error cases:
- **MutateAnnotatedImmutableParam:** `fn foo(obj: {bar: number}) { obj.bar = 5 }`
  → `CannotMutateImmutableError`.
- **MutateAnnotatedImmutableParamIndex:** same via bracket notation.

**Note on inferred types:** Property values on written-to objects are widened
to their primitive types (e.g. `bar: string` not `bar: "hello"`) as of
Phase 4. The `Widenable` flag on type variables triggers `widenLiteral` in
`bind()` and union accumulation in `unifyWithDepth`.

---

## Phase 3: Unification for Open Object Types ✅

**Requirements covered:** Sections 6a, 6b, 6c, 8a (passing to typed
functions), 8c (multiple parameters), 8d (aliasing).

> **Note (2026-04-06):** The `bind()` function in `unify.go` was updated in
> #382 to propagate constraints when binding two TypeVarTypes where one has a
> constraint and the other doesn't. This means when a row-typed parameter's
> TypeVar is unified with another TypeVar, constraints are properly preserved.
> Also, the probe-then-commit pattern (#382) in union/intersection unification
> prevents partial TypeVar mutation on failure, which is important for the
> open-vs-closed unification path described below.

**Goal:** Passing an open-typed function parameter to a typed function unifies
correctly without closing the type prematurely.

### Implementation (completed 2026-04-06)

**Deviation from plan:** The `bind()` helper does **not** wrap the opened object
in `MutabilityType{Uncertain}`. Instead, the open `ObjectType` is bound directly
to the type variable. `GeneralizeFuncType` finds open objects by checking
`ObjectType.Open` (not by looking for a `MutabilityType` wrapper), so no wrapper
is needed. Mutability is resolved during generalization based on the `Written`
flag, consistent with Phase 2.

**Changes made:**

1. **`internal/checker/infer_func.go`** — `inferFuncParams` (~line 26):
   - When creating a `FreshVar()` for an unannotated parameter, sets
     `IsParam: true` on the resulting `TypeVarType`. Only simple identifier
     patterns (not destructuring) are marked, since destructuring patterns with
     inline type annotations already determine their type fully.

2. **`internal/checker/unify.go`** — `openClosedObjectForParam` helper:
   Extracted a shared helper used by both `typeVar1` and `typeVar2` branches
   in `bind()`. When a param type variable (`IsParam: true`) is bound to a
   closed `ObjectType`, the helper:
   - Creates an open copy with `Open: true` and a fresh `RestSpreadElem` (row
     variable for row polymorphism). Elements are deep-copied via
     `copyObjTypeElems` so that mutations (e.g. `Written` flag) on the open
     copy do not leak back to the closed source type.
   - Binds the type variable's `Instance` directly to the open copy (no
     `MutabilityType` wrapper).
   - Sets provenance to the original closed type (for error messages/diagnostics).
   - Returns `true` to short-circuit the normal bind path.
   - If the target is not a closed `ObjectType`, returns `false` and the normal
     bind path proceeds (e.g. `number`, `string` bind directly).
   - Note: `IsParam` and `Constraint` are not both set today, but
     `openClosedObjectForParam` checks `Instance != nil` to guard against
     double-binding if that invariant changes.

3. **`internal/checker/unify.go`** — `copyObjTypeElem` / `copyObjTypeElems`:
   Helpers that shallow-copy all named elem types (`PropertyElem`, `MethodElem`,
   `GetterElem`, `SetterElem`) so that future mutable fields on any elem type are
   automatically isolated. `PropertyElem.Written` is reset to `false` on the copy.
   These helpers should only be used when copying from a closed type annotation
   into an open inferred type — not when copying between two open types in the
   same function body, as that would incorrectly discard `Written` information.

4. **`internal/checker/unify.go`** — ObjectType-vs-ObjectType branch
   (~line 957):
   Added checks for `Open` after the existing nominal check. Four paths:

   **a. Open-vs-open (both `Open: true`):**
   1. Unify shared properties pairwise.
   2. Properties in one but not the other are added to both (merge). These are
      appended directly (not via `copyObjTypeElem`) to preserve `Written` state.
   3. If both have `RestSpreadElem`s, unify their row variables.
   4. Both remain open. Returns early.

   **b. Open(t1)-vs-closed(t2):**
   1. Iterate closed-side keys, unify shared properties as `Unify(t1val, t2val)`
      to preserve directionality (`Unify` is not symmetric).
   2. Closed-only properties are copied to the open type via `copyObjTypeElem`.
   3. Open-only properties are allowed (structural subtyping). Returns early.

   **c. Closed(t1)-vs-open(t2):**
   1. Iterate closed-side keys, unify shared properties as `Unify(t1val, t2val)`
      to preserve directionality.
   2. Closed-only properties are copied to the open type via `copyObjTypeElem`.
   3. Open-only properties are allowed (structural subtyping). Returns early.

   Cases (b) and (c) are handled separately rather than merged with a
   swap-and-normalize pattern because `Unify` is asymmetric — the t1/t2
   argument order must be preserved for correct subtyping.

   **d. Closed-vs-closed (existing path):**
   Existing code handles this. Falls through when neither type is open.

4. **Multiple constraints via intersection (Section 6b):**
   Works automatically. After the first call binds the type variable to an open
   `ObjectType`, the second call unifies that open object with the second
   function's parameter type via the open-vs-closed path (3b above).

5. **Aliasing (Section 8d):**
   Works automatically. `let alias = obj` unifies the type variables, so
   subsequent accesses on `alias` constrain `obj`.

### Tests (implemented)

Test file: `internal/checker/tests/row_types_test.go`

**`TestRowTypesPassToTypedFunction`** — table-driven tests:
- **PassToTypedFunction:** `fn bar(x: {bar: string}) ...; fn foo(obj) { bar(obj) }`
  → `foo: fn <T0>(obj: {bar: string, ...T0}) -> void`
- **PropertiesSurviveFunctionCall:** property assignments before and after a
  typed call are preserved → `fn <T0>(obj: mut {z: boolean, ...T0, bar: string, w: string}) -> void`
- **MultipleCallsMerge:** two typed calls merge constraints →
  `fn <T0>(obj: {x: number, ...T0, y: string}) -> void`
- **NonObjectBinding:** passing to `fn takes_num(x: number)` binds directly →
  `fn (obj: number) -> void`
- **MultipleParameters:** independent constraints per parameter →
  `fn <T0, T1>(a: {a: number, ...T0}, b: {b: string, ...T1}) -> void`
- **OpenVsClosedSharedProperty:** shared property unified pairwise; literal
  widened to primitive →
  `fn <T0>(obj: mut {name: string, ...T0}) -> void`
- **OpenVsClosedExtraPropertiesInOpen:** extra properties in open type allowed →
  `fn <T0>(obj: mut {a: number, ...T0, b: string}) -> void`

---

## Phase 4: Property Widening (Union Accumulation) ✅

**Requirements covered:** Section 6d (property type widening), Section 6e
(literal type widening to primitives).

**Goal:** When a literal is unified with a widenable type variable, widen it to
its primitive type. When the same property is assigned different types, the
property type widens to a union instead of producing an error.

### Implementation (completed 2026-04-07)

**Deviation from plan:** The original plan suggested using the probe-then-commit
pattern (`deepCloneType`) for the widening check. The implementation instead
uses a simpler approach: refactor `unifyWithDepth` into an outer function that
saves pre-Prune types and an inner `unifyPruned` function. When `unifyPruned`
returns errors and the original type was a `Widenable` TypeVarType, the outer
function handles widening directly. This avoids the overhead of cloning and is
correct because widening is a one-way operation (union accumulation).

Additionally, the plan did not account for `MutabilityType` wrappers on
property values. The implementation strips `MutabilityType` wrappers before
building unions to prevent `mut?` from leaking into union members.

**Changes made:**

1. **`internal/checker/unify.go`** — Refactored `unifyWithDepth`:
   - Type-asserts `tv2` from `t2` before `Prune` reassigns `t2` to the pruned
     concrete type. `Prune` records the alias chain in `tv2.InstanceChain`,
     which the widening fallback reads to update all aliased TypeVars.
   - Delegates to `unifyPruned(ctx, t1, t2, depth)` for the core unification.
   - If `unifyPruned` returns errors, reads `tv2.InstanceChain` via
     `widenableInstanceChain(tv2)` to find all `Widenable` TypeVars in the
     alias chain. Guards against live TypeVars in `oldType` (from occurs check
     failures). Strips uncertain `MutabilityType` wrappers via
     `unwrapMutability`, widens the new type via `widenLiteral`, deduplicates
     via `typeContains`, and builds a union via `flatUnion`.
   - Updates ALL TypeVars in the chain (not just the starting one) so reads
     through any alias observe the widened type.
   - Non-widenable type variables still return the original errors.

2. **Write-site gating:** Only the `tv2` (right-hand side) is checked for
   widening. Property writes call `Unify(valueType, propertyTV)`, placing the
   Widenable TypeVar on the right. Read sites (e.g. `val s: string = obj.bar`)
   place the TypeVar on the left — those must NOT widen but must report type
   errors.

3. **`internal/checker/unify.go`** — Literal widening in `bind()`:
   Both `typeVar1` and `typeVar2` branches now call `widenLiteral(targetType)`
   before setting `Instance` when the TypeVar has `Widenable: true`.

4. **Deep widening of object, tuple, and function literals:** `widenLiteral`
   uses a top-level switch to dispatch between type kinds:
   - `LitType` → `widenLitToPrim(lit)` converts to corresponding `PrimType`.
   - `ObjectType` → `widenObjectLiterals(obj)` prunes property values through
     TypeVars, then recursively calls `widenLiteral` on each property value.
     Also handles `MethodElem`, `GetterElem`, and `SetterElem` via
     `widenFuncType`.
   - `TupleType` → `widenTupleLiterals(tuple)` widens each element type.
   - `MutabilityType` wrappers: uncertain `mut?` is stripped from all widened
     results (primitives are scalar values that don't need mutability tracking;
     structured types would leak `mut?` into inferred signatures). Explicit
     `mut` is always preserved.
   - This handles `obj.loc = {x: 0, y: 0}` → `loc: {x: number, y: number}`.

5. **Alias chain tracking via `InstanceChain`:** Added `InstanceChain` field
   to `TypeVarType` (in `type_system/types.go`). `Prune` calls
   `recordInstanceChain(tv)` for Widenable TypeVars before path compression,
   recording all TypeVars in the alias chain. Non-Widenable TypeVars skip
   chain building since they never enter the widening path. Features:
   - Subslice assignment: each node gets a suffix view of the same backing
     array, avoiding redundant walks (O(n) total instead of O(n²)).
   - Stale chain detection: when a tail node is re-aliased to another TypeVar,
     `recordInstanceChain` detects it and rebuilds instead of trusting the
     cached chain.
   - Longer-suffix preservation: only overwrites a node's chain if the new
     suffix is longer, preventing truncation when a middle node was pruned
     first.

6. **Helper functions** (in `unify.go`):
   - `unwrapMutability(t)` — strips only uncertain `mut?` wrappers, preserving
     explicit `mut`.
   - `widenLiteral(t)` — top-level switch dispatching to `widenLitToPrim`,
     `widenObjectLiterals`, or `widenTupleLiterals`. Prunes through TypeVars,
     strips uncertain `mut?` from results, preserves explicit `mut`.
   - `widenLitToPrim(lit)` — converts `LitType` to `PrimType`. Returns nil for
     unrecognized literal kinds. Includes `BigIntLit` (TODO #228: untestable
     until parser supports bigint literals in all expression positions).
   - `widenObjectLiterals(obj)` — deep-widens property values, method
     params/returns, getter returns, and setter params.
   - `widenFuncType(fn)` — widens param types and return type.
   - `widenTupleLiterals(tuple)` — deep-widens tuple element values.
   - `flatUnion(oldType, newType)` — builds a flattened, deduplicated union
     from both operands using `collectUnionMembers` and `type_system.Equals`.
   - `typeContains(haystack, needle)` — recursively checks if all leaf types
     in `needle` are present in `haystack`.
   - `collectUnionMembers(t)` — flattens nested unions into leaf types.
   - `widenableInstanceChain(tv)` — reads the `InstanceChain` from a
     `*TypeVarType` and filters for `Widenable` members.

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
   `t1` goes through `bind()`, which widens the literal to `string` (Section 6e)
   and sets `t1.Instance = string`.
2. `obj.bar = 5` — `inferExpr` on `obj.bar` calls `getMemberType` →
   `getObjectAccess` finds the existing `bar` property, returns `t1`.
   Unification calls `unifyWithDepth(5, t1)`. Before `Prune`, `tv2` is
   type-asserted from `t1`. `Prune(t1)` returns `string` and records the alias
   chain in `tv2.InstanceChain`. Unifying `5` with `string` fails. The
   widening fallback reads `tv2.InstanceChain` via `widenableInstanceChain`,
   finds `t1` is `Widenable`, widens `5` to `number`, and replaces
   `t1.Instance` with `string | number` (via `flatUnion` which deduplicates).
3. `obj.bar = true` — same flow, widens to `string | number | boolean`.
4. `obj.loc = {x: 0, y: 0}` — `widenLiteral` recursively enters the
   `ObjectType` case, pruning each property value through TypeVars, stripping
   uncertain `mut?` wrappers, and widening literals to primitives. The result
   is `{x: number, y: number}`.
5. `obj.pair = [1, "hello"]` — `widenLiteral` enters the `TupleType` case,
   widening each element: `[number, string]`.
6. `obj.config = {getValue(self) { return self._x }}` — `widenObjectLiterals`
   handles `MethodElem` via `widenFuncType`, widening the return type.

### Tests (implemented)

Test file: `internal/checker/tests/row_types_test.go`

**`TestRowTypesPropertyWidening`** — table-driven tests:
- **LiteralWideningString:** `obj.bar = "hello"` → `bar: string`
- **LiteralWideningNumber:** `obj.bar = 42` → `bar: number`
- **LiteralWideningBoolean:** `obj.bar = true` → `bar: boolean`
- **SameKindLiteralsCollapse:** `obj.bar = "hello"; obj.bar = "world"` →
  `bar: string` (both widen to `string`, deduplication gives just `string`)
- **DifferentKindLiteralsProduceUnion:** `obj.bar = "hello"; obj.bar = 5` →
  `bar: string | number`
- **ThreeWayWidening:** `obj.bar = "a"; obj.bar = 1; obj.bar = true` →
  `bar: string | number | boolean`
- **BranchWidening:** `if cond { obj.bar = "hello" } else { obj.bar = 5 }` →
  `bar: string | number`
- **NonLiteralTypesNotWidened:** `obj.bar = s` (where `s: string`) →
  `bar: string` (already a primitive, no widening needed)
- **NormalTypeVarConflictStillErrors:** `val x: number = "hello"` → error
  containing `"hello" cannot be assigned to number`
- **DeepWidenObjectLiteral:** `obj.loc = {x: 0, y: 0}; obj.col = "red"` →
  `loc: {x: number, y: number}, col: string`
- **DeepWidenNestedLiterals:** `obj.prop = {a: {b: {c: "hello", d: 5}}}` →
  `prop: {a: {b: {c: string, d: number}}}`
- **DeepWidenTupleLiterals:** `obj.pair = [1, "hello"]` →
  `pair: [number, string]`
- **DeepWidenNestedTupleInObject:** `obj.data = {coords: [1, 2], label: "hi"}`
  → `data: {coords: [number, number], label: string}`
- **ReadWidenedPropertyIntoNarrowType:** reading `string | number` into
  `string` variable → error containing `cannot be assigned to string`
- **ReadWidenedPropertyIntoDifferentType:** reading `string` into `boolean`
  variable → error containing `cannot be assigned to boolean`

- **DeepWidenMethodGetterSetter:** object literal with method, getter, and
  setter — return types and param types are widened via `widenFuncType`

**Updated existing tests:** All tests in `TestRowTypesPropertyAccess`,
`TestRowTypesPassToTypedFunction`, and `TestRowTypesWriteAfterPass` that
previously expected literal types for property writes now expect widened
primitive types (e.g. `"hello"` → `string`, `5` → `number`, `true` →
`boolean`).

**`internal/checker/widening_test.go`** — Unit tests for helper functions:
- `TestFlatUnionFlattensNestedUnions` — 3-step incremental widening.
- `TestFlatUnionFlattensNewTypeUnion` — newType is a union.
- `TestFlatUnionDeduplicatesSharedMembers` — overlapping union members are
  deduplicated.
- `TestTypeContainsFindsNestedMembers` — recursive member lookup.
- `TestTypeContainsUnionNeedle` — union needle matching.
- `TestUnwrapMutabilityOnlyStripsUncertain` — mut vs mut? handling.
- `TestWideningWithAliasedTypeVars` — alias chain consistency after widening.

**`internal/type_system/prune_test.go`** — Unit tests for `Prune`:
- Basic: concrete type, unbound TypeVar, single/multi-node chains.
- `InstanceChain`: two-node, three-node capture with subslice assignment.
- Non-Widenable TypeVars: no `InstanceChain` is built.
- Direct concrete Instance: no `InstanceChain`.
- Not overwritten on second call.
- Unbound terminal: no self-loop, three-node chain.
- Middle-then-head: preserves full chain from prior prune.
- Re-alias: detects stale chain and extends it.

---

## Phase 5: Method Call Inference ✅

**Requirements covered:** Section 2 (method call inference).

**Goal:** Calling a method on an inferred object creates a `FuncType` binding
with appropriate parameters and return type.

> **Status (2026-04-08):** Implemented. Method call inference on inferred
> objects uses the same `TypeVarType` case in `inferCallExpr` as callback
> inference (from #380) — no special-casing for method calls. Multiple calls
> with different arg types produce an intersection (overloaded signature) via
> the existing `resolveCallSites` fallback. Literal argument types are
> preserved (not widened to primitives), matching callback behavior.

### Changes (implemented)

1. **`internal/checker/infer_expr.go`** — `inferCallExpr`, `TypeVarType` case:
   Argument types are stripped of uncertain mutability (`mut?`) via
   `unwrapMutability` before being passed to `handleFuncCall`. This ensures
   inferred param types are clean (e.g. `fn(42)` not `fn(mut? 42)`). The
   `mut?` wrapper tracks whether a value will be mutated and is resolved during
   generalization — it shouldn't leak into inferred function signatures. This
   applies to both method calls and callback calls uniformly.

2. **Interaction with Phase 2 (deferred call-site resolution):**
   When `obj.process(42)` is inferred:
   - `inferExpr` on `obj.process` calls `getMemberType` → TypeVarType case →
     creates `PropertyElem{process: t_process}` on the open ObjectType, returns
     `t_process` (an unbound TypeVarType).
   - `inferCallExpr` receives `t_process` as the callee type, hits the
     `TypeVarType` case. Strips `mut?` from the argument `42`.
   - A synthetic `FuncType` is created and appended to
     `(*ctx.CallSites)[t_process.ID]`. `t_process.Instance` is **NOT set**.
   - `handleFuncCall` unifies the unwrapped argument (`42`) with the fresh
     param type, binding it.
   - A subsequent call (`obj.process("hello")`) creates a second synthetic
     `FuncType` with its own param bound to `"hello"`.
   - After the function body is inferred, `resolveCallSites` runs. The probe
     unification fails (concrete `42` vs `"hello"`), and the fallback
     creates an `IntersectionType`:
     `fn(42) -> T0 & fn("hello") -> T1`.

3. **Why intersection, not union:** Method params are contravariant — the
   requirement is "must handle being called with both types." An intersection
   correctly models overloaded dispatch and preserves per-call-site return type
   precision. A union would lose the ability to have different return types per
   call site.

4. **MethodElem vs PropertyElem with FuncType:** Methods are stored as
   `PropertyElem` with a `FuncType` value, not as `MethodElem`. This is simpler
   and works because `resolveCallSites` binds the property's type variable to
   the resolved `FuncType` or `IntersectionType`.

5. **Multiple calls with different arities:** `resolveCallSites` tries
   `tryMergeCallSitesWithOptionalParams` (prefix-compatible arities), then
   falls back to `IntersectionType` (overloaded signature).

### Tests (implemented)

- **Basic method call:**
  `fn foo(obj) { val r = obj.process(42, "hello") }` — `process`
  is `fn(arg0: 42, arg1: "hello") -> T0`.
- **Method parameter intersection:**
  `fn foo(obj) { obj.process(42); obj.process("hello") }` —
  `process` is `fn(arg0: 42) -> T0 & fn(arg0: "hello") -> T1`.
- **Method return type intersection:**
  `fn foo(obj) { val x: number = obj.getValue(); val y: string = obj.getValue() }`
  — `getValue` is `fn() -> number & fn() -> string`.
- **Method + property on same object:**
  `fn foo(obj) { obj.x = 1; val r = obj.process(obj.x) }` — both
  property and method inferred (`process` param is `number` because `obj.x`
  is already resolved via property widening).
- **Zero-arg method:**
  `fn foo(obj) { val r = obj.getData() }` — `getData` is `fn() -> T0`.

---

## Phase 6: Closing After Function Body Inference ✅

**Requirements covered:** Section 5 (open vs. closed lifecycle), Section 5a
(mutability inference), Section 11d (interaction with closing).

**Goal:** After a function body is fully inferred, close all open object types on
parameters. Remove row variables that don't escape to callers. Resolve
mutability: if any property was written to, the parameter type becomes `mut`;
otherwise the `MutabilityType` wrapper is removed.

### Implementation (completed 2026-04-08)

**Approach:** Two new functions in `infer_func.go`:

- **`closeOpenParams(funcSigType)`** — iterates over `funcSigType.Params`,
  finds open `ObjectType`s (unwrapping `TypeVarType` and `MutabilityType`),
  resolves mutability via the existing `finalizeOpenObject` helper in
  `generalize.go`, and delegates to `closeObjectType` for closing and
  `RestSpreadElem` filtering.
- **`closeObjectType(objType, returnVars)`** — recursively closes an
  `ObjectType` (sets `Open = false`) and its nested open objects, then removes
  `RestSpreadElem`s whose row variables don't appear in `returnVars`.

`closeOpenParams` is called at the end of `inferFuncBodyWithFuncSigType`,
before the function returns. This ensures it runs **before** `resolveCallSites`
and `GeneralizeFuncType`, which are called after `inferFuncBodyWithFuncSigType`
in the call sites in `infer_module.go`, `infer_expr.go`, and `infer_stmt.go`.

**Interaction with `GeneralizeFuncType`:** The mutability resolution code in
`GeneralizeFuncType` (`finalizeOpenObject` loop) becomes a no-op after Phase 6
because all open objects are already closed (`Open = false`) when
`GeneralizeFuncType` runs. The code is left in place as a safety net.

**Recursive nested closing:** `closeObjectType` recurses into nested open
`ObjectType`s found within `PropertyElem` values, so deeply nested inferred
objects (e.g. `obj.a.b.c`) are also closed and have their unused
`RestSpreadElem`s removed.

**Deviation from plan:** The originally proposed code inlined mutability
resolution directly. The actual implementation reuses the existing
`finalizeOpenObject` helper from `generalize.go`, which handles nested objects,
`mut?` stripping on written properties, and upward write propagation. This
avoids duplicating the mutability logic.

**Return type mutability:** When a parameter is returned (e.g.
`fn foo(obj) { obj.x = 1; return obj }`), the parameter type shows `mut` but
the return type does not (e.g. `fn <T0>(obj: mut {x: number, ...T0}) -> {x: number, ...T0}`).
This is because `closeOpenParams` sets `tv.Instance` on the param's TypeVar to
a new `MutabilityType{Mutable}`, but the return type resolves through a
different TypeVar chain that was established during unification. The return type
correctly reflects the object's structure without asserting caller-side
mutability.

### Changes made

1. **`internal/checker/infer_func.go`**:
   - Added `closeOpenParams` — collects return-type vars via
     `collectUnresolvedTypeVars`, iterates params, calls `finalizeOpenObject`
     for mutability, then `closeObjectType` for closing + filtering.
   - Added `closeObjectType` — sets `Open = false`, recurses into nested open
     objects in property values, filters `RestSpreadElem`s not in return vars.
   - Added `closeOpenParams(funcSigType)` call before each return in
     `inferFuncBodyWithFuncSigType` (both the generator early-return path and
     the normal return path).

2. **No changes to `generalize.go`** — uses existing `collectUnresolvedTypeVars`
   and `finalizeOpenObject`. The mutability loop in `GeneralizeFuncType` is now
   effectively dead code for inferred params (they're already closed) but is
   retained as a safety net.

### Tests (`TestRowTypesClosing` in `row_types_test.go`)

- **ClosedWithMut:** `fn foo(obj) { obj.bar = 5 }` →
  `fn (obj: mut {bar: number}) -> void`
- **ClosedWithoutMut:** `fn foo(obj) { val x = obj.bar }` →
  `fn <T0>(obj: {bar: T0}) -> void`
- **MixedReadsAndWrites:** `fn foo(obj) { val x = obj.bar; obj.baz = 5 }` →
  `fn <T0>(obj: mut {bar: T0, baz: number}) -> void`
- **RestSpreadPreservedWhenInReturnType:** `fn foo(obj) { obj.x = 1; return obj }` →
  `fn <T0>(obj: mut {x: number, ...T0}) -> {x: number, ...T0}`
- **MultipleParamsClosedIndependently:** `fn foo(a, b) { a.x = 1; b.y = "hi" }` →
  `fn (a: mut {x: number}, b: mut {y: string}) -> void`
- **NestedFunctionClosedIndependently:** inner and outer functions close after
  their own bodies.

All existing row types tests updated — `RestSpreadElem`s (`...Tn`) removed from
types where the row variable doesn't appear in the return type, and type
parameter lists trimmed accordingly.

---

## Phase 7: Row Polymorphism ✅

**Requirements covered:** Section 11 (row polymorphism), Section 8b/8g (return
type propagation).

**Goal:** When an inferred parameter is returned, its row variable becomes a type
parameter on the function, enabling callers to preserve extra properties.

> **Status (2026-04-08, implemented):** No changes were needed to
> `GeneralizeFuncType`, `instantiateGenericFunc`, or `SubstituteTypeParams` —
> the existing infrastructure handles row polymorphism correctly.
> The only code change was adding `collectFlatElems` to `ObjectType.String()`
> in `types.go`, which flattens `RestSpreadElem`s whose values resolve (via
> `Prune`) to `ObjectType`s by inlining their properties into the parent
> display. Empty resolved `ObjectType`s are dropped entirely.
>
> **Literal preservation:** Literal types from arguments are preserved through
> row variables (not widened). For example, `foo({x: 1, y: 2})` where `foo`
> returns its argument produces `{x: number, y: 2}` — `x` is `number` because
> the function wrote to it, while `y` retains the literal type `2` from the
> caller. This is consistent with Escalier's treatment of generic type
> parameters, which also preserve literal types.

### Changes

1. **Row variable promotion via `GeneralizeFuncType`** — No manual TypeParam
   creation is needed. After Phase 6 closes inferred ObjectTypes and removes
   `RestSpreadElem`s that don't appear in the return type, the remaining row
   variables are still unresolved `TypeVarType`s inside `RestSpreadElem`s.
   `GeneralizeFuncType` (which runs after closing — see Phase 6 status note)
   handles them automatically:
   - `collectUnresolvedTypeVars` walks into `RestSpreadElem.Value` and finds
     unresolved row variables.
   - Promotes them to `TypeParam`s with generated names (e.g. `T0`, `T1`).
   - Sets `tv.Instance = TypeRefType{Name: "T0", ...}` on each TypeVar.

   The `RestSpreadElem` wrapper is naturally preserved: the TypeVar *inside*
   it is bound via `Instance`, but the `RestSpreadElem` struct itself is
   untouched. After generalization, the structure is:
   `RestSpreadElem{Value: TypeVarType{Instance: TypeRefType{Name: "T0"}}}`.
   `Prune()` resolves the inner value to the `TypeRefType`.

   **Post-generalization shape example:**
   ```
   // Before generalization (Phase 6 has closed the ObjectType):
   //   param type: {x: number, ...TypeVarType{ID:5}}
   //   return type: MutabilityType{...{x: number, ...TypeVarType{ID:5}}}
   //
   // After GeneralizeFuncType:
   //   TypeParams: [TypeParam{Name: "T0"}]
   //   param type: {x: number, ...TypeVarType{ID:5, Instance: TypeRefType{Name: "T0"}}}
   //   return type: MutabilityType{...{x: number, ...TypeVarType{ID:5, Instance: TypeRefType{Name: "T0"}}}}
   ```

   **Row variable naming:** `GeneralizeFuncType` generates names like `T0`,
   `T1`. For readability, row type parameters could use `R0`, `R1` instead.
   This is a cosmetic enhancement that can be added by checking whether the
   TypeVar came from a `RestSpreadElem` during collection.

2. **Call-site instantiation** — `internal/checker/infer_expr.go`
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

3. **`ObjectType.String()` flattening** — `internal/type_system/types.go`:
   Added `collectFlatElems` helper used by `ObjectType.String()`. When a
   `RestSpreadElem`'s value resolves (via `Prune`) to an `ObjectType`, its
   elements are inlined into the parent rather than printing `...{y: 2}`.
   Empty resolved `ObjectType`s (no extra properties) are dropped entirely.
   Unresolved TypeVars/TypeRefTypes continue to print as `...T0`.

   This is purely a display change — the underlying type structure still uses
   `RestSpreadElem` wrappers. The flattening is recursive, handling nested
   RestSpreadElems within resolved rest types.

4. **Multiple row variables:** When multiple parameters each have row variables
   in the return type (e.g. `fn merge(a, b) { return {...a, ...b} }`), each gets
   its own type parameter. This is naturally handled since each parameter has a
   distinct `RestSpreadElem` with a distinct row variable ID.

### Tests

All tests are in `TestRowTypesRowPolymorphism` in `row_types_test.go`.

- **Basic row polymorphism:**
  ```esc
  fn foo(obj) { obj.x = 1; return obj }
  val r = foo({x: 1, y: 2})
  ```
  — `foo: fn <T0>(obj: mut {x: number, ...T0}) -> {x: number, ...T0}`,
  `r: {x: number, y: 2}`.

- **Multiple extra properties:**
  ```esc
  fn foo(obj) { obj.x = 1; return obj }
  val r = foo({x: 1, y: 2, z: "hi"})
  ```
  — `r: {x: number, y: 2, z: "hi"}`.

- **No return — row variable removed:**
  `fn foo(obj) { obj.x = 1 }` — no type parameter, `RestSpreadElem`
  removed. `r: void`.

- **Derived return (property of param):**
  `fn foo(obj) { return obj.x }` — return type is `T0` (the type of `x`), not
  the full object. Row variable does not appear in return type, so it's removed.
  Literal type preserved: `r: 5`.

- **Return in a structure:**
  `fn foo(obj) { return {y: obj.x} }` — row variable doesn't escape.
  Literal type preserved: `r: {y: 5}`.

- **Read-only row polymorphism:**
  `fn foo(obj) { val x = obj.x; return obj }` — read-only access with return
  preserves extra properties. `r: {x: 1, y: "hello"}`.

- **Multiple row-polymorphic parameters:**
  ```esc
  fn foo(a, b) { a.x = 1; b.y = "hi"; return [a, b] }
  val r = foo({x: 0, extra1: true}, {y: "", extra2: 42})
  ```
  — `r: [{x: number, extra1: true}, {y: string, extra2: 42}]`.

- **No extra properties:**
  Calling with exact properties — row variable resolves to empty `ObjectType`,
  which is dropped from display: `r: {x: number}`.

**Multiple row-polymorphic parameters via spread (Phase 10):**

- ```esc
  fn merge(a, b) { return {...a, ...b} }
  val r = merge({x: 1}, {y: "hello"})
  ```
  — `r: {x: 1, y: "hello"}`. Tested in `TestObjectSpread/MultipleSpreads`.

---

## Phase 8: Destructuring Patterns on Parameters ✅

**Requirements covered:** Section 8f (destructuring patterns — object and
tuple/array).

**Goal:** Destructured parameters with rest elements get appropriate polymorphic
types: `RestSpreadElem` for object patterns (row polymorphism), `RestSpreadType`
for tuple patterns (variadic tuples of the form `[t0, ...R]`).

### What was implemented

Most of the infrastructure was already in place from earlier phases.
`inferPattern` in `infer_pat.go` already creates `RestSpreadElem` for
`ObjRestPat` and `RestSpreadType` for `RestPat` inside `TuplePat`, with fresh
type variables that are shared between the rest spread and the binding. No
changes to `inferFuncParams` were needed.

#### Object destructuring

`inferPattern` handles the `ObjRestPat` case by creating a `RestSpreadElem`
whose value is the same fresh type variable used for the rest binding. The
resulting `ObjectType` has `Open: false` — the explicit properties are fixed by
the pattern — but includes a `RestSpreadElem` for row polymorphism.
`closeOpenObjectsInType` already preserves `RestSpreadElem`s on non-open
objects, so no changes were needed there.

#### Tuple destructuring

`inferPattern` handles `RestPat` inside `TuplePat` by creating a
`RestSpreadType` element in the `TupleType`. The key change was in
`closeTupleType`: it previously removed trailing `RestSpreadType` elements
whose type variable didn't appear in the return type. Pattern-originated rest
variables (which have `FromBinding=true` on the inner `TypeVarType`) must be
preserved even when not in the return type, since the user explicitly wrote
`...rest` to accept variadic arguments.

#### Type display

`patternStringWithInlineTypesContext` in `types.go` was updated to:
- **`ObjRestPat` case:** Look up the `RestSpreadElem` in the `ObjectType` and
  display the rest binding with its type (e.g. `...rest: T1`).
- **`RestPat` case (new):** Extract the inner type from `RestSpreadType` and
  display the rest binding with its type (e.g. `...rest: T1`).

### Changes

1. **`internal/checker/infer_func.go`** — `closeTupleType`:
   Added a check for `tv.FromBinding` to skip removal of rest spread type
   variables that originated from explicit destructuring patterns.

2. **`internal/type_system/types.go`** — `patternStringWithInlineTypesContext`:
   - `ObjRestPat` case: looks up `RestSpreadElem` in the `ObjectType` and
     recursively prints the inner pattern with the rest type.
   - New `RestPat` case: extracts the inner type from `RestSpreadType` and
     recursively prints the inner pattern.

### Tests (in `row_types_test.go`)

**Object destructuring (`TestDestructuringObjectPatterns`):**
- `ObjectDestructuringWithoutRest` — `fn ({bar, baz})` infers
  `fn <T0, T1>({bar: T0, baz: T1}) -> [T0, T1]`.
- `ObjectDestructuringWithRest` — `fn ({bar, ...rest})` infers
  `fn <T0, T1>({bar: T0, ...rest: T1}) -> T1`.
- `ObjectDestructuringWithRestUnused` — rest var preserved even when not in
  return type: `fn <T0, T1>({bar: T0, ...rest: T1}) -> T0`.
- `ObjectDestructuringWithoutRestBodyConstraint` — body usage constrains
  element types: `fn ({x: number, y: number}) -> number`.
- `ObjectDestructuringWithTypeAnnotation` — type annotation takes precedence.

**Tuple destructuring (`TestDestructuringTuplePatterns`):**
- `TupleDestructuringWithoutRest` — `fn ([a, b])` infers
  `fn <T0, T1>([a: T0, b: T1]) -> [T0, T1]`.
- `TupleDestructuringWithBodyConstraint` — body usage constrains element
  types: `fn ([a: number, b: number]) -> number`.
- `TupleDestructuringWithRest` — `fn ([first, ...rest])` infers
  `fn <T0, T1>([first: T0, ...rest: T1]) -> T1`.
- `TupleDestructuringWithRestUnused` — rest var preserved even when not in
  return type: `fn <T0, T1>([first: T0, ...rest: T1]) -> T0`.
- `TupleDestructuringCalledWithRest` — calling `fn ([first, ...rest]) { return rest }`
  with `[1, 2, 3]` yields `r: [2, 3]`.
- `TupleDestructuringCalledWithoutRest` — calling `fn ([a, b]) { return a + b }`
  with `[1, 2]` yields `r: number`.

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
   // Wrap the open object in MutabilityUncertain (same as Phase 2's
   // non-optional path) so writes through optional chaining work.
   mutObj := NewMutabilityType(nil, openObj, MutabilityUncertain)
   // Wrap in union with null and undefined
   typeVar.Instance = NewUnionType(nil, mutObj, NewNullType(nil), NewUndefinedType(nil))
   // Return propTV | undefined (the ?. expression itself may produce undefined)
   return NewUnionType(nil, propTV, NewUndefinedType(nil))
   ```

   The null/undefined type constructors are `NewNullType(nil)` and
   `NewUndefinedType(nil)` (confirmed in `types.go` — both return `*LitType`
   wrapping `NullLit{}` and `UndefinedLit{}` respectively).

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

## Phase 10: Object & Array/Tuple Spread ✅

**Requirements covered:** Section 12 (object spread, multiple RestSpreadElems),
Section 16 (array/tuple spread).

**Goal:** Handle `ObjSpreadExpr` in object literals and support multiple
`RestSpreadElem`s in unification. Refine `ArraySpreadExpr` handling in tuple
literals to preserve source types and support inferred (TypeVarType) spread
sources.

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
   Search all elements (explicit properties **and** `RestSpreadElem`s) in a
   single **reverse-order** pass. The first match from the end wins, which
   correctly handles JavaScript override semantics: in `{a: 1, ...{a: 2}}` the
   spread's `a` wins, and in `{...{a: 1}, a: 2}` the explicit `a` wins,
   because in both cases the later element is found first during the reverse
   scan. All three branches (`PropertyKey`, `IndexKey` string-literal, and
   `IndexKey` unique-symbol) use this approach.

   For `RestSpreadElem` lookups, `getSpreadPropertyType` applies JavaScript
   spread semantics (methods → fn type, getters → return type, setter-only →
   skip). For symbol keys, `resolveToObjectType` resolves through
   `MutabilityType` wrappers and `TypeRefType` aliases (e.g. `Array<T>`) to
   reach the underlying `ObjectType`.

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

- **Basic spread (`TestObjectSpread/BasicSpread`):**
  `val r = {...{x: 1}, y: 2}` — `r: {x: 1, y: 2}`.
- **Spread with inferred type (`TestObjectSpread/SpreadWithInferredType`):**
  `fn foo(obj) { return {...obj, extra: 1} }` — return type `{...T0, extra: 1}`.
- **Multiple spreads (`TestObjectSpread/MultipleSpreads`):**
  `fn merge(a, b) { return {...a, ...b} }` — two row variables, two type params.
  Calling `merge({x: 1}, {y: "hello"})` yields `{x: 1, y: "hello"}`.
- **Override semantics (`TestObjectSpread/SpreadOverrideSemantics`):**
  `val bar = {a: 1, b: 2, ...{b: 5, c: 10}, c: 3}` — `bar.a` is `1`,
  `bar.b` is `5` (spread overrides earlier explicit), `bar.c` is `3`
  (later explicit overrides spread).
- **Property access through RestSpreadElem (`TestObjectSpread/PropertyAccessThroughSpread`):**
  ```esc
  val base = {x: 1, y: 2}
  val extended = {...base, z: 3}
  val v = extended.x  // found via RestSpreadElem
  ```
- **Spread called with concrete args (`TestObjectSpread/SpreadCalledWithConcreteArgs`):**
  `fn extend(obj) { return {...obj, extra: 1} }` called with
  `{x: "hello", y: true}` yields `{x: "hello", y: true, extra: 1}`.
- **Override ordering (`TestObjectSpread/SpreadBeforeExplicitOverride`,
  `SpreadAfterExplicitOverride`):** `{...obj, x: 1}` vs `{x: 1, ...obj}` —
  later element wins for shared keys.
- **Spread of method/getter/setter (`TestObjectSpread/SpreadOfObjectWithMethod`,
  `SpreadOfObjectWithGetter`, `SpreadOfObjectWithSetterOnly`):** Methods become
  function-valued properties, getters yield return types, setter-only skipped.
- **Symbol keys through spread (`TestObjectSpread/SpreadPreservesSymbolKeys`):**
  `{...arr, extra: 1}` where `arr: Array<number>` — `obj[Symbol.iterator]`
  found via spread.
- **Chained destructuring with rest
  (`TestDestructuringObjectPatterns/ChainedDestructuringWithRest`):**
  Two functions that each destructure with rest, passing rest from outer to
  inner. Nested `RestSpreadElem`s in bound rests are collected as unbound rests.

### Array/Tuple Spread Changes

6. **`internal/checker/infer_expr.go`** — `TupleExpr` inference (~line 272):
   Refine the existing `ArraySpreadExpr` handler. Currently it always wraps the
   result in `RestSpreadType{Type: Array<elementType>}`. Update to preserve the
   source type:

   **a. Spread of `TupleType`:** If the spread source's type (after pruning) is
   a `TupleType`, inline its elements directly into the parent tuple rather
   than creating a `RestSpreadType`:
   ```go
   case *ast.ArraySpreadExpr:
       spreadType, spreadErrors := c.inferExpr(ctx, spread.Value)
       errors = slices.Concat(errors, spreadErrors)
       prunedType := Prune(spreadType)
       // Unwrap MutabilityType if present
       if mut, ok := prunedType.(*MutabilityType); ok {
           prunedType = Prune(mut.Type)
       }
       switch st := prunedType.(type) {
       case *TupleType:
           // Inline tuple elements directly
           elemTypes = append(elemTypes, st.Elems...)
       case *TypeRefType:
           // Array<T> — preserve as RestSpreadType
           elemTypes = append(elemTypes, NewRestSpreadType(nil, st))
       default:
           // Other iterables — check and wrap
           elementType := c.GetIterableElementType(ctx, spreadType)
           if elementType == nil {
               // ... error handling (existing) ...
           }
           elemTypes = append(elemTypes, NewRestSpreadType(nil, &TypeRefType{
               Name: NewIdent("Array"), TypeArgs: []Type{elementType},
           }))
       }
   ```

   **b. Spread of `TypeVarType`:** If the spread source is a `TypeVarType`
   (unannotated parameter), use `RestSpreadType{Type: typeVar}` directly
   without calling `GetIterableElementType`. The iterable constraint is
   enforced structurally at call sites: when the caller passes a concrete
   argument, unification will resolve the type variable and the
   `RestSpreadType` will unify against the argument's elements. If the
   argument is not iterable, unification will fail at that point. This
   mirrors how `ArrayConstraint` defers the tuple-vs-array decision —
   no upfront constraint is needed on the type variable itself.

   **c. Spread of `ArrayType`:** If the spread source resolves to an
   `ArrayType` directly (rare — usually it's `TypeRefType` with name `Array`),
   wrap in `RestSpreadType{Type: sourceType}`.

7. **No unification changes needed:** Tuple-vs-tuple unification with
   `RestSpreadType` at any position is already complete (Phase 13). The
   `RestSpreadType` elements produced by array/tuple spread will unify using
   the existing variadic tuple unification logic.

8. **No display changes needed:** `TupleType.String()` already handles
   `RestSpreadType` elements, including flattening resolved rest types
   (Phase 13).

### Array/Tuple Spread Tests

- **Spread of array into tuple:**
  ```esc
  val arr: Array<number> = [1, 2, 3]
  val result = [0, ...arr, 4]
  ```
  — `result: [0, ...Array<number>, 4]`.

- **Spread of tuple into tuple (flattened):**
  ```esc
  val tup: [string, boolean] = ["hello", true]
  val result = [0, ...tup, 4]
  ```
  — `result: [number, string, boolean, number]` (tuple spread inlined).

- **Spread of inferred type:**
  ```esc
  fn prepend(value, items: Array<number>) {
      return [value, ...items]
  }
  ```
  — return type: `[T0, ...Array<number>]`.

- **Multiple array spreads (collapses to Array):**
  ```esc
  fn merge(a: Array<number>, b: Array<string>) {
      return [...a, ...b]
  }
  ```
  — return type: `Array<number | string>`.

- **Tuple + array spread:**
  ```esc
  fn prepend(tup: [number, string], arr: Array<boolean>) {
      return [...tup, ...arr]
  }
  ```
  — return type: `[number, string, ...Array<boolean>]`.

- **Multiple tuple spreads (flattened):**
  ```esc
  fn concat(a: [number, string], b: [boolean]) {
      return [...a, ...b]
  }
  ```
  — return type: `[number, string, boolean]`.

- **Spread of non-iterable (error):**
  ```esc
  val x = [...42]
  ```
  — error: `Type 'number' is not iterable`.

- **Spread with literal elements:**
  ```esc
  val arr: Array<number> = [1, 2, 3]
  val result = ["start", ...arr, "end"]
  ```
  — `result: ["start", ...Array<number>, "end"]`.

- **Single array spread collapses (`TestTupleSpreadRefined/SpreadOfSet`,
  `SpreadOfMap`):** `[...Set<number>]` → `Array<number>`,
  `[...Map<string, number>]` → `Array<[string, number]>`. A tuple with only a
  single `...Array<T>` rest is collapsed to `Array<T>` via
  `collapseArrayRestSpreads`.

### Known limitations

- **Both-sides RestSpreadElems (#410):** Closed-vs-closed ObjectType
  unification where **both** sides contain `RestSpreadElem`s returns
  `UnimplementedError`. `unifyClosedWithRests` handles the common case
  (one side has rests, the other is concrete) but the both-sides case was
  intentionally deferred. In practice this doesn't arise because spread
  expressions unify against concrete types or type variables (bound by
  call-site arguments), not against other spread expressions.

- **Non-iterable RestSpreadType in tuples (#411):** Spreading an object rest
  element into a tuple (e.g. `[x, ...rest]` where `rest` is an ObjectType)
  produces a `RestSpreadType` wrapping a non-iterable type. The type checker
  does not currently validate that `RestSpreadType` inner types are iterable.

- **Custom symbols as computed keys (#413):** `Symbol()` returns the `symbol`
  primitive type, not a `UniqueSymbolType`, so custom symbols cannot be used
  as computed keys in object literals. Only well-known symbols (e.g.
  `Symbol.iterator`) work as computed keys. Additionally, invalid computed
  keys cause a nil-entry panic in the ObjectType construction.

---

## Phase 12: Tuple and Array Inference from Indexing Patterns ✅

**Requirements covered:** Section 13 (tuple and array inference).

> **Status (2026-04-09):** Implemented. `ArrayConstraint` struct on `TypeVarType`
> defers tuple-vs-array commitment until closing time. `getMemberType` creates/updates
> constraints on numeric index access and delegates Array method lookups via
> `isArrayMethod`/`isArrayMutatingMethod`. `closeOpenParams` (now a `Checker` method)
> resolves constraints: mutating methods or non-literal indexes → `Array<T>` /
> `mut Array<T>`; literal-only indexes → tuple; index assignment forces `mut` on
> tuples (not array — diverges from original plan Section 13d, see note below).
> `deepCloneType` and `collectUnresolvedTypeVars` handle `ArrayConstraint`.
> `handleArrayConstraintBinding` in `unify.go` updates constraints when bound to
> `Array<T>` or tuple types. Tests: `TestTupleArrayInference`,
> `TestTupleArrayInferenceEdgeCases`.

**Goal:** When a function parameter is indexed numerically, infer whether it
should be a tuple or an array based on usage patterns. Non-negative integer
literal indexes with read-only Array methods → tuple. Mutating methods or non-literal
indexes → `mut Array<T>` or `Array<T>`. Index assignment with literal indexes →
`mut` tuple (not `mut Array`).

### Background

Phase 2 previously bound a TypeVarType to `Array<T>` immediately when a numeric
index was used. This phase refines that behavior to support tuple inference by
deferring the commitment until closing time.

### Changes

1. **New tracking structure — `ArrayConstraint` (in `internal/type_system/types.go`):**

   ```go
   type ArrayConstraint struct {
       LiteralIndexes     map[int]Type  // index → element type variable
       HasNonLiteralIndex bool          // true if items[i] used with non-literal
       HasMutatingMethod  bool          // true if .push(), .pop(), etc. called
       HasIndexAssignment bool          // true if items[i] = value used
       ElemTypeVar        Type          // fresh T for Array<T> (union accumulator)
   }
   ```

   Stored on `TypeVarType` as `ArrayConstraint *ArrayConstraint` (nil when no
   numeric indexing observed). All code that reads or writes the constraint —
   `getMemberType`, the assignment handler, `closeOpenParams`, and
   `deepCloneType` — uses `typeVar.ArrayConstraint` as the single source of
   truth. Both `ElemTypeVar` and literal index TypeVars are created with
   `Widenable = true` so that literal values (e.g. `42`) are widened to their
   primitive types (e.g. `number`) on binding.

2. **`internal/checker/expand_type.go`** — `getMemberType`, TypeVarType case:

   The IndexKey branch creates/updates an `ArrayConstraint` instead of
   immediately binding to `Array<T>`. Literal integer indexes record per-position
   type variables; non-literal numeric indexes set `HasNonLiteralIndex`.

   The PropertyKey branch first checks `isArrayMethod(propName)` — if the
   property exists on the Array type definition, an `ArrayConstraint` is created
   (or the existing one is used) and the access is delegated to
   `getArrayConstraintPropertyAccess`. This handles the case where `.push()` is
   called before any numeric index access. If the property is not an Array
   method, the existing open-object path runs.

   When a TypeVarType already has an `ArrayConstraint`, all subsequent property
   and index accesses are routed through `getArrayConstraintPropertyAccess` /
   `getArrayConstraintIndexAccess`.

   `getArrayConstraintPropertyAccess` creates a **fresh element type variable**
   for each method call and appends it to `constraint.MethodElemVars`. This
   allows multiple calls with different argument types (e.g. `push(5)` then
   `push("hello")`) to each bind their own fresh var independently, deferring
   union accumulation to `resolveArrayConstraint`. Without this, the second
   call would fail trying to unify against the already-bound `ElemTypeVar`.

   Method classification uses runtime lookup on the Array type definition via
   `isArrayMutatingMethod` (checks `MutSelf` on `MethodElem`), keeping the
   classification in sync with the type system's actual definitions.

   Helper functions added: `isNumericType`, `asNonNegativeIntLiteral`,
   `getOrCreateArrayConstraint`, `getArrayConstraintPropertyAccess`,
   `getArrayConstraintIndexAccess`, `arrayConstraintElemType`,
   `isArrayMethod`, `isArrayMutatingMethod`.

3. **`internal/checker/infer_expr.go`** — Assignment handler:

   When `items[i] = value` is detected and the LHS base (after Prune) is a
   TypeVarType with an `ArrayConstraint`, sets `constraint.HasIndexAssignment = true`.
   The value type is unified with the element type via the normal
   `Unify(rightType, leftType)` call (where `leftType` is the TypeVar from
   `LiteralIndexes`).

4. **`internal/checker/infer_func.go`** — `closeOpenParams` + resolution:

   `closeOpenParams` is now a `Checker` method (was standalone function) to
   support `Unify` calls during resolution. Before closing open objects, it
   calls `resolveArrayConstraintsInType` on each parameter.

   `resolveArrayConstraint` resolution rules:
   - First, all `MethodElemVars` (fresh type vars from per-call-site method
     resolution) are unified with `ElemTypeVar`, accumulating a union of all
     argument types across method calls.
   - `HasMutatingMethod || HasNonLiteralIndex` → `Array<T>` (or `mut Array<T>`
     if `HasMutatingMethod`). All literal index TypeVars unified with `ElemTypeVar`.
   - Otherwise → tuple. `HasIndexAssignment` adds `mut` wrapper to the tuple
     (diverges from original plan — see note in Section 13d below).
   - Gaps in literal indexes (e.g. only index 0 and 3 accessed) produce fresh
     TypeVars for missing positions.

5. **`internal/checker/unify.go`** — `handleArrayConstraintBinding`:

   When a TypeVarType with an `ArrayConstraint` is bound to a concrete type:
   - Bound to `Array<T>` → forces `HasNonLiteralIndex`, unifies element types.
   - Bound to `mut Array<T>` → additionally forces `HasMutatingMethod`.
   - Bound to a tuple type → unifies element types pairwise.
   Returns `true` if handled (skips normal bind path), `false` otherwise.

6. **`internal/checker/generalize.go`**:

   `deepCloneType` clones `ArrayConstraint` (literal index map, flags, and
   `ElemTypeVar`) when present on a TypeVarType.

   `collectUnresolvedTypeVars` collects type vars from `ArrayConstraint`'s
   `LiteralIndexes` and `ElemTypeVar`.

### Tests

All tests are in `TestTupleArrayInference` and `TestTupleArrayInferenceEdgeCases`
in `internal/checker/tests/row_types_test.go`.

**Core tests (`TestTupleArrayInference`):**

- **Tuple inference from literal indexes:**
  ```esc
  fn foo(items) { return [items[0], items[1]] }
  ```
  → `fn <T0, T1>(items: [T0, T1]) -> [T0, T1]`

- **Tuple with .length:**
  ```esc
  fn foo(items) { val a = items[0]; val l = items.length; return [a, l] }
  ```
  → `fn <T0>(items: [T0]) -> [T0, number]`

- **Array from .push():**
  ```esc
  fn foo(items) { items.push(42) }
  ```
  → `fn (items: mut Array<number>) -> void`

- **Array from non-literal index:**
  ```esc
  fn foo(items, i: number) { return items[i] }
  ```
  → `fn <T0>(items: Array<T0>, i: number) -> T0`

- **Mut tuple from index assignment:**
  ```esc
  fn foo(items) { items[0] = 42 }
  ```
  → `fn (items: mut [number]) -> void`

- **Array from mix of literal index + push:**
  ```esc
  fn foo(items) { val a = items[0]; items.push("hello") }
  ```
  → `fn (items: mut Array<string>) -> void`
  (literal index element type unified with push argument type)

**Edge case tests (`TestTupleArrayInferenceEdgeCases`):**

- **Gap index** — only `items[1]` → `fn <T0>(items: [T0, number]) -> void`
  (2-tuple with unresolved T0 at position 0)
- **Object literal widening** — `items[0] = {x: 5, y: 10}` →
  `fn (items: mut [{x: number, y: number}]) -> void`
- **Read/write different indexes** — read `[0]`, write `[1]` →
  `fn <T0>(items: mut [T0, string]) -> T0`
- **Multiple writes same index** — `items[0] = 42; items[0] = 99` →
  `fn (items: mut [number]) -> void`
- **Sparse indexes** — read `[0]` and `[3]` →
  `fn <T0, T1, T2, T3>(items: [T0, T1, T2, T3]) -> [T0, T3]`
- **Index assignment + push** — `items[0] = 42; items.push(99)` →
  `fn (items: mut Array<number>) -> void` (push forces array)
- **Single index read-only** — `items[0]` → `fn <T0>(items: [T0]) -> T0`
- **Return tuple element** — read `[0]` and `[1]`, return `[0]` →
  `fn <T0, T1>(items: [T0, T1]) -> T0`
- **Write-only index 0** — `items[0] = "hello"` →
  `fn (items: mut [string]) -> void`

- **Multiple push with different types** — `push(5)` then `push("hello")` →
  `fn (items: mut Array<number | string>) -> void`
  (each method call gets a fresh elem var; unified into union during resolution)
- **Multiple push with same type** — `push(5)` then `push(10)` →
  `fn (items: mut Array<number>) -> void`
- **Push + unshift with different types** — `push(5)` then `unshift("hello")` →
  `fn (items: mut Array<number | string>) -> void`
- **Multiple push + literal index** — read `[0]`, `push(5)`, `push("hello")` →
  `fn (items: mut Array<number | string>) -> void`
- **Index assignment with different types** — `items[0] = 5; items[1] = "hello"` →
  `fn (items: mut [number, string]) -> void`
  (literal indexes produce a tuple, not an array)

**Not yet tested:**
- Conflict: object property + numeric index (e.g. `obj.name` + `obj[0]`)
- Read-only method (`.map()`) on inferred tuple (currently produces disconnected
  type variables — may need deferred call-site resolution)

---

## Phase 13: Variadic Tuple Types ✅

**Requirements covered:** Section 14 (variadic tuple types).

> **Status (2026-04-08):** Implemented. Tuple-vs-tuple unification with
> `RestSpreadType` at any position (leading, middle, trailing) is complete.
> `TupleType.String()` flattens resolved rest types. Tuple-vs-array and
> array-vs-tuple unification handle `RestSpreadType` elements. `getMemberType`
> includes rest type element types in the union for method resolution. The
> parser now supports `...T` in tuple type annotations (previously only in
> object type annotations). Generalization and instantiation work automatically
> via the existing visitor pattern. Tests cover fixed-vs-variadic,
> variadic-vs-variadic, generalization, and array-absorbed rest types.

**Goal:** Support `RestSpreadType` elements inside `TupleType` as variadic
type parameters, enabling types like `[number, string, ...T]` where `T`
represents the remaining elements. This is the tuple analogue of
`RestSpreadElem` in `ObjectType` for row polymorphism.

### Background

`TupleType.Elems` is `[]Type`, and `RestSpreadType` already exists as a type
that can appear in that slice (the parser and `inferPattern` already create
`RestSpreadType` elements for rest patterns). The unifier has partial support
for `RestSpreadType` in tuple-vs-tuple unification (with a `TODO: handle spread`
comment). This phase completes that support and makes variadic tuples work
end-to-end: in type annotations, unification, generalization, instantiation,
and display.

### Changes

1. **`internal/checker/unify.go`** — Complete tuple-vs-tuple unification with
   `RestSpreadType`:

   The existing code already handles the case where one side has a
   `RestSpreadType` at the end. Complete the implementation to support
   `RestSpreadType` at any position (leading, middle, or trailing). A tuple
   type may contain at most one `RestSpreadType` with an unbounded type
   (e.g. `Array<T>`), matching TypeScript's constraint.

   **General approach:** Fixed elements at the start and end of the tuple
   anchor the unification — they must match pairwise. The rest spread
   absorbs the variable-length gap.

   **a. Trailing rest — fixed-vs-variadic** (`[A, B]` vs `[C, ...R]`):
   1. Unify positional elements pairwise up to the variadic boundary.
   2. Collect remaining elements from the fixed side.
   3. Unify the `RestSpreadType`'s inner type with a `TupleType` of the
      remaining elements: `Unify(R, [remaining...])`.

   ```go
   // [number, string, boolean] vs [number, ...R]
   // → Unify(number, number), then Unify(R, [string, boolean])
   ```

   **b. Leading rest** (`[...R, string]` vs `[number, number, string]`):
   1. Unify fixed elements from the end: `Unify(string, string)`.
   2. Collect remaining elements from the fixed side: `[number, number]`.
   3. Unify: `Unify(R, [number, number])`.

   **c. Variadic-vs-variadic** (`[A, ...R1]` vs `[B, ...R2]`):
   1. Unify positional elements pairwise up to the shorter prefix.
   2. If both have the same number of positional elements, unify `R1` with `R2`.
   3. If one has more positional elements, collect the extras and unify the
      shorter side's rest with `[extras..., ...longerRest]`.

   **d. Variadic-vs-Array** (`[A, ...R]` vs `Array<T>`):
   1. Unify `A` with `T`.
   2. Unify `R` with `Array<T>` (the rest elements must also be arrays of `T`).

   **e. Array-vs-variadic** (`Array<T>` vs `[A, ...R]`):
   Mirror of (d).

2. **`internal/type_system/types.go`** — `TupleType.String()`:

   Update to handle `RestSpreadType` elements at any position. When an element
   is a `RestSpreadType`, print it with `...` prefix. If the rest type resolves
   (via `Prune`) to a `TupleType`, inline its elements (similar to
   `ObjectType.String()` flattening resolved `RestSpreadElem`s):

   ```esc
   [number, string, ...T]           // unresolved rest type variable
   [number, string, boolean, true]  // rest resolved to [boolean, true], flattened
   [number, string, ...Array<any>]  // rest resolved to Array<any>
   ```

3. **`internal/type_system/types.go`** — `TupleType.Equals()`:

   Already compares elements pairwise including `RestSpreadType` (via
   `equals()`), so no changes needed. Verify with a test.

4. **`internal/type_system/types.go`** — `TupleType.Accept()`:

   Already visits all elements including `RestSpreadType` (the visitor
   recursively calls `elem.Accept(v)` for each element). No changes needed
   for `SubstituteTypeParams` — it walks via `Accept()`, so `...T` inside a
   tuple will be substituted when `T` is a type parameter. Verify with a test.

5. **`internal/checker/generalize.go`** — `collectUnresolvedTypeVars`:

   Already walks into `TupleType.Elems` via the visitor pattern. A
   `RestSpreadType{Type: TypeVarType{...}}` will have its inner TypeVar
   collected automatically. Verify that `GeneralizeFuncType` promotes it to a
   type parameter just like row variables in `RestSpreadElem`. The resulting
   function type should look like:
   ```
   fn <T0>(items: [number, ...T0]) -> [number, ...T0]
   ```

6. **`internal/checker/expand_type.go`** — `getMemberType` for tuples:

   Currently, tuple method/property access computes the union of all element
   types and delegates to `Array<union>`. With variadic tuples, the union must
   include the rest type's element type:
   - `[number, string, ...T]` → methods resolve against
     `Array<number | string | T>`.
   - If `T` resolves to `Array<boolean>`, the union becomes
     `number | string | boolean`.
   - If `T` resolves to `[boolean, bigint]`, the union becomes
     `number | string | boolean | bigint`.

   Numeric indexing on variadic tuples:
   - Literal index within the fixed prefix → returns that element's type.
   - Literal index beyond the prefix → requires resolving the rest type;
     if unresolved, return a type variable.
   - Non-literal index → return the union of all element types (same as
     arrays).

7. **Type annotation parsing:**

   **Note:** The original plan assumed the parser already handled `...T` in
   tuple type annotations, but `DotDotDot` was only handled in
   `objTypeAnnElem` (for object types). A `DotDotDot` case was added to
   `primaryTypeAnn` in `type_ann.go` to support `[number, ...T]` syntax.

### Tests

- **Variadic tuple type annotation:**
  `fn foo(items: [number, ...Array<string>]) { ... }` — parses and type-checks.

- **Type alias with variadic tuple:**
  ```esc
  type OneOrMore<T> = [T, ...Array<T>]
  fn first<T>(items: OneOrMore<T>) -> T { return items[0] }
  val r = first([1, 2, 3])
  ```
  — `T = number`, `r: number`. Calling `first([])` should produce an error
  because `[]` is not assignable to `[number, ...Array<number>]`.

- **Fixed-vs-variadic unification:**
  ```esc
  val x: [number, ...Array<string>] = [1, "a", "b"]
  ```
  — `1` unifies with `number`, `["a", "b"]` unifies with `...Array<string>`.

- **Variadic-vs-fixed unification:**
  ```esc
  fn foo(items: [number, ...T]) { ... }
  foo([1, "a", true])
  ```
  — `T` binds to `[string, boolean]`.

- **Variadic-vs-variadic unification:**
  ```esc
  fn foo(a: [number, ...R1], b: [string, ...R2]) { ... }
  ```
  — independent rest type variables.

- **Variadic-vs-Array unification:**
  ```esc
  fn foo(items: [number, ...T]) { ... }
  val arr: Array<number> = [1, 2, 3]
  foo(arr)
  ```
  — `number` unified with `number`, `T` binds to `Array<number>`.

- **Generalization with variadic rest:**
  ```esc
  fn foo(items: [number, ...T]) { return items }
  ```
  — type: `fn <T>(items: [number, ...T]) -> [number, ...T]`.

- **Display flattening:**
  Resolved `...T` where `T = [string, boolean]` displays as
  `[number, string, boolean]`, not `[number, ...[string, boolean]]`.

- **Method access on variadic tuple:**
  `[number, string, ...T].length` resolves via `Array<number | string | T>`.

- **Leading rest:**
  ```esc
  val x: [...Array<number>, string] = [1, 2, "hello"]
  ```
  — `[1, 2]` absorbed by `...Array<number>`, `"hello"` unified with `string`.

- **Type alias with variadic tuple:**
  ```esc
  type OneOrMore<T> = [T, ...Array<T>]
  fn first<T>(items: OneOrMore<T>) -> T { return items[0] }
  val r = first([1, 2, 3])
  ```
  — `T = number`, `r: number`. Calling `first([])` should produce an error
  because `[]` is not assignable to `[number, ...Array<number>]`.

---

## Phase 14: Tuple Row Polymorphism (Variadic Inference) ✅

**Requirements covered:** Section 15 (tuple row polymorphism).

**Goal:** When a function parameter is inferred as a tuple (Phase 12) and the
parameter is returned, preserve extra elements via a variadic rest type variable
— the tuple analogue of row polymorphism for objects.

### Background

Phase 12 infers tuple types from literal index access. Phase 13 adds variadic
tuple support (`[A, B, ...T]`). This phase combines them: when an inferred tuple
parameter is returned from a function, a rest type variable is added to capture
caller-supplied extra elements, just as `RestSpreadElem` captures extra object
properties in Phase 7.

Without this phase, `fn foo(items) { return items[0] }` works fine (returns the
first element), but `fn foo(items) { items[0] = 1; return items }` would lose
extra elements in the return type — the function would have type
`fn (items: [number]) -> [number]` and `foo([1, "a", true])` would return
`[number]`, losing `"a"` and `true`.

### Changes

1. **Phase 12 modification — add rest type variable to inferred tuples:**

   When Phase 12 resolves an `ArrayConstraint` to a tuple (all literal indexes,
   no mutating methods), append a `RestSpreadType` with a fresh type variable
   to capture extra elements:

   ```go
   // In resolveArrayConstraint, tuple path:
   restTV := c.FreshVar(nil)
   elems = append(elems, type_system.NewRestSpreadType(nil, restTV))
   return NewTupleType(nil, elems...)
   // Result: [t0, t1, ...R] instead of [t0, t1]
   ```

   This is analogous to Phase 2 adding a `RestSpreadElem` with a row variable
   to open `ObjectType`s.

2. **`internal/checker/infer_func.go`** — `closeOpenParams` / new helper:

   At closing time, check whether the rest type variable in an inferred
   variadic tuple appears in the function's return type:

   - **Appears in return type:** Preserve the `RestSpreadType` — the rest
     variable becomes a type parameter via `GeneralizeFuncType` (same mechanism
     as Phase 7 for row variables). The function becomes generic over the
     trailing elements.
   - **Does not appear in return type:** Remove the `RestSpreadType` from
     `TupleType.Elems` — the extra elements are accepted but not tracked.
     The tuple becomes fixed-length.

   This mirrors `closeObjectType`'s logic for removing `RestSpreadElem`s
   whose row variables don't escape to the return type.

   ```go
   func closeTupleType(tupleType *TupleType, returnVars map[int]bool) {
       // Check if the last element is a RestSpreadType with an unresolved TV
       if len(tupleType.Elems) > 0 {
           if rest, ok := last(tupleType.Elems).(*RestSpreadType); ok {
               if tv, ok := Prune(rest.Type).(*TypeVarType); ok {
                   if !returnVars[tv.ID] {
                       // Rest var not in return type — remove it
                       tupleType.Elems = tupleType.Elems[:len(tupleType.Elems)-1]
                   }
                   // else: keep it — GeneralizeFuncType will promote it
               }
           }
       }
   }
   ```

3. **Generalization — no changes needed:**

   `GeneralizeFuncType` already collects unresolved type variables via the
   visitor pattern. The TypeVar inside `RestSpreadType` inside `TupleType` will
   be collected and promoted to a type parameter automatically, just like row
   variables inside `RestSpreadElem` inside `ObjectType` (Phase 7).

4. **Call-site instantiation — no changes needed:**

   `SubstituteTypeParams` already walks into `TupleType.Elems` and
   `RestSpreadType.Type` via `Accept()`. A fresh type variable is created for
   the rest parameter and substituted in. During argument unification, the
   caller's extra elements flow into the rest variable via the variadic
   unification from Phase 13.

5. **Display flattening:**

   `TupleType.String()` (updated in Phase 13) already handles resolved
   `RestSpreadType` by inlining elements. When a caller passes
   `[1, "a", true]` to `fn <R>(items: [number, ...R]) -> [number, ...R]`,
   `R` binds to `["a", true]`, and the return type displays as
   `[number, "a", true]`.

### How it works end-to-end

```esc
fn foo(items) {
    items[0] = 1
    return items
}
// Phase 12 infers: items has ArrayConstraint with LiteralIndexes {0: t0}
//   and HasIndexAssignment = true
// HasIndexAssignment + literal-only indexes → resolves to mut [number].
```

Better example (read-only with return):

```esc
fn first(items) {
    let x = items[0]
    return items
}
// Phase 12: only literal index 0, no mutations → tuple [t0, ...R]
// Phase 14: R appears in return type → preserved
// After generalization: fn <T0, R>(items: [T0, ...R]) -> [T0, ...R]

val result = first([1, "hello", true])
// T0 = 1, R = ["hello", true]
// result: [1, "hello", true]
```

```esc
fn swap(items) {
    let a = items[0]
    let b = items[1]
    return [b, a]
}
// Phase 12: literal indexes 0 and 1 → tuple [t0, t1, ...R]
// Return type is [t1, t0] — a new tuple, not the original param
// R does NOT appear in return type → removed
// After generalization: fn <T0, T1>(items: [T0, T1]) -> [T1, T0]

val result = swap([1, "hello"])
// T0 = 1, T1 = "hello"
// result: ["hello", 1]
```

```esc
fn withDefault(items) {
    let first = items[0]
    let second = items[1]
    return [first, second, ...items]
}
// This requires tuple spread in expressions (out of scope for this phase)
// but illustrates the concept
```

### Tests

- **Tuple with return — rest preserved:**
  ```esc
  fn foo(items) { let x = items[0]; return items }
  val r = foo([1, "hello", true])
  ```
  — `foo: fn <T0, R>(items: [T0, ...R]) -> [T0, ...R]`,
  `r: [1, "hello", true]`.

- **Tuple without return — rest removed:**
  ```esc
  fn foo(items) { let x = items[0] }
  ```
  — `foo: fn <T0>(items: [T0]) -> void` (no rest variable).

- **Derived return (element of tuple) — rest does not escape:**
  ```esc
  fn foo(items) { return items[0] }
  val r = foo([42])
  ```
  — `foo: fn <T0>(items: [T0]) -> T0`, `r: 42`.

- **Multiple tuple params with return:**
  ```esc
  fn foo(a, b) { let x = a[0]; let y = b[0]; return [a, b] }
  val r = foo([1, 2], ["a", "b"])
  ```
  — both rest variables appear in return type, both preserved.

- **No extra elements — rest resolves to empty tuple:**
  ```esc
  fn foo(items) { let x = items[0]; return items }
  val r = foo([42])
  ```
  — `R = []` (empty tuple), display flattened: `r: [42]`.

- **Literal types preserved through rest:**
  ```esc
  fn foo(items) { let x = items[0]; return items }
  val r = foo([1, "hello"])
  ```
  — `r: [1, "hello"]` (literal types preserved, not widened — same as
  row polymorphism for objects in Phase 7).

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

### Core inference pipeline (sequential)

```
Phase 1: Type System Extensions ✅
  → Phase 2: Property Access ✅
    → Phase 3: Unification ✅
      → Phase 4: Widening ✅
        → Phase 5: Method Calls ✅
          → Phase 6: Closing ✅
```

### Object row polymorphism (after Phase 6)

```
Phase 6 → Phase 7: Row Polymorphism ✅
```

### Tuple/array inference (after Phase 6, parallel with Phase 7)

```
Phase 6 → Phase 12: Tuple/Array Inference ✅
```

### Variadic tuples (after Phase 3, independent)

```
Phase 3 → Phase 13: Variadic Tuple Types ✅
            → Phase 14: Tuple Row Polymorphism ✅ (also requires Phase 12)
```

### Destructuring (requires both object and tuple row polymorphism)

```
Phase 7, Phase 14 → Phase 8: Destructuring ✅
```

### Independent branches (can start early)

```
Phase 2 → Phase 9: Optional Chaining
Phase 3 → Phase 10: Object & Array/Tuple Spread ✅ (also requires Phase 13 for tuple spread)
Phase 3 → Phase 13: Variadic Tuple Types ✅
```

### Error reporting (after all other phases)

```
All phases → Phase 11: Error Reporting
```

### Summary

| Phase                             | Depends on | Can parallelize with |
|-----------------------------------|------------|----------------------|
| 1: Type System Extensions ✅      | —          | —                    |
| 2: Property Access ✅             | 1          | —                    |
| 3: Unification ✅                 | 2          | —                    |
| 4: Widening ✅                    | 3          | —                    |
| 5: Method Calls ✅                | 4          | —                    |
| 6: Closing ✅                     | 5          | —                    |
| 7: Row Polymorphism ✅            | 6          | 12                   |
| 8: Destructuring ✅               | 7, 14      | —                    |
| 9: Optional Chaining              | 2          | 3–14                 |
| 10: Object & Array/Tuple Spread ✅| 3, 13      | 4–14                 |
| 11: Error Reporting               | all        | —                    |
| 12: Tuple/Array Inference ✅      | 6          | 7, 13                |
| 13: Variadic Tuple Types ✅       | 3          | 4–12                 |
| 14: Tuple Row Polymorphism ✅     | 12, 13     | 7                    |

---

## Key Risks

1. ~~**Widening (Phase 4):**~~ Resolved. The `unifyWithDepth` / `unifyPruned`
   split cleanly saves pre-Prune references. Gating via `Widenable` ensures
   normal type inference is unaffected (verified by
   `NormalTypeVarConflictStillErrors` test).

2. **Optional chaining (Phase 9):** Wrapping the type variable in a union
   (`T | null | undefined`) means subsequent accesses go through
   `getUnionAccess`, which must correctly find and extend the open `ObjectType`
   inside the union. May be complex — consider deferring if it destabilizes
   earlier phases.

3. ~~**Multiple RestSpreadElems (Phase 10):**~~ Resolved. Property distribution
   across unbound rest elements is inherently ambiguous — the error case for
   ambiguous distributions is kept as designed in `unifyClosedWithRests`.
   Note: closed-vs-closed unification where **both** sides contain
   `RestSpreadElem`s still returns `UnimplementedError` — this case does not
   arise in normal usage (spread expressions unify against concrete types or
   type variables, not against other spread expressions).

4. **Snapshot tests:** Adding `Open` to `ObjectType` shouldn't change printed
   types (it's an internal flag), but changes to `RestSpreadElem` handling in
   unification may affect how types are resolved and printed. Run
   `UPDATE_SNAPS=true go test ./...` after each phase.

5. **Backwards compatibility:** The new `Open`, `Widenable`, and `IsParam`
   fields default to `false` (Go zero values), so existing code paths are
   unaffected. Still, audit all code that constructs or pattern-matches on
   `ObjectType` and `TypeVarType` to ensure the new fields don't cause
   unexpected behavior.

6. **Tuple vs. Array deferred resolution (Phase 12):** Deferring the
   commitment from `Array<T>` to tuple requires that numeric index access
   returns a type variable that works regardless of the final resolution. If
   the parameter is ultimately resolved to `Array<T>`, all per-index type
   variables must be unified with `T`. If resolved to a tuple, each position
   keeps its own type. The deferred approach adds complexity to the closing
   logic and to method call resolution (e.g. `.map()` needs the element type
   before closing). Consider resolving eagerly to `Array<T>` when any
   non-literal usage is observed, and only deferring for the pure literal-index
   case.

7. ~~**Variadic tuple unification (Phase 13):**~~ Resolved. The tuple-vs-tuple
   unification now uses `splitTupleAtRest` to partition elements into
   prefix/rest/suffix, then delegates to specialized helpers for each case
   (fixed-vs-variadic, variadic-vs-fixed, variadic-vs-variadic). The
   variadic-vs-variadic case handles different prefix/suffix lengths by
   wrapping extras into a tuple with the shorter side's rest.

8. ~~**Tuple row polymorphism interaction with Phase 8 (Phase 14):**~~ Resolved.
   Phase 14 is complete. Inferred tuples now include a `RestSpreadType` with a
   fresh type variable, and `closeTupleType` removes it when it doesn't escape
   to the return type. Phase 8 can now use `[t0, ...R]` for tuple destructuring
   with rest patterns. Key implementation detail: `closeOpenParams` was
   reordered to resolve ArrayConstraints before collecting `returnVars`, so that
   rest type variables created during resolution are visible in the return type
   analysis.

---

## Critical Files

| File | Phases | Status |
|------|--------|--------|
| `internal/type_system/types.go` | 1, 7, 12, 13 | ✅ (Phase 1) `Open`, `Widenable`, `IsParam`, `Written` fields added; `Accept`/`Copy` updated; (Phase 7) `collectFlatElems` and `ObjectType.String()` flattening of resolved `RestSpreadElem`s; ✅ (Phase 12) `ArrayConstraint` struct with `MethodElemVars` field for per-call-site deferred resolution, `ArrayConstraint` field on `TypeVarType`; ✅ (Phase 13) `collectFlatTupleElems` and `TupleType.String()` flattening of resolved `RestSpreadType` |
| `internal/checker/expand_type.go` | 2, 9, 10, 12, 13 | ✅ (Phase 2) `TypeVarType` case in `getMemberType`, open-object handling in `getObjectAccess`, helper functions; ✅ (Phase 12) deferred tuple/array commitment via `ArrayConstraint`; `isArrayMethod`/`isArrayMutatingMethod` for runtime method classification; `getArrayConstraintPropertyAccess` uses fresh elem var per method call for deferred union resolution; `getArrayConstraintIndexAccess` for subsequent accesses; ✅ (Phase 13) `tupleElemUnion` helper; `getMemberType` TupleType case handles `RestSpreadType` in both numeric index and method access |
| `internal/checker/unify.go` | 3, 4, 10, 12, 13 | ✅ (Phase 3) `openClosedObjectForParam`, open-vs-open/closed paths; (Phase 4) `unifyPruned` refactor, `widenLiteral`, `flatUnion`, `typeContains`, `unwrapMutability`; ✅ (Phase 12) `handleArrayConstraintBinding` — updates constraint when TypeVar with `ArrayConstraint` is bound to Array or tuple type; ✅ (Phase 13) `splitTupleAtRest`, `unifyTuples`, `unifyFixedTuples`, `unifyFixedVsVariadic`, `unifyVariadicVsFixed`, `unifyVariadicVsVariadic`; tuple-vs-array and array-vs-tuple handle `RestSpreadType` |
| `internal/parser/type_ann.go` | 13 | ✅ (Phase 13) `DotDotDot` case in `primaryTypeAnn` — enables `...T` in tuple type annotations (e.g. `[number, ...T]`) |
| `internal/checker/generalize.go` | 2, 6, 7, 12 | ✅ (Phase 2) Mutability resolution in `GeneralizeFuncType`, `Open` preserved in `deepCloneType`; (Phase 7) No changes needed — handles row variables automatically; ✅ (Phase 12) `deepCloneType` clones `ArrayConstraint` including `MethodElemVars`; `collectUnresolvedTypeVars` collects from `ArrayConstraint` including `MethodElemVars`; ✅ (Phase 13/14) No changes needed — visitor pattern handles `RestSpreadType` in tuples; `collectUnresolvedTypeVars` already walks `TupleType.Elems` and `RestSpreadType.Type` |
| `internal/checker/infer_func.go` | 3, 6, 7, 8, 12, 14 | ✅ (Phase 3) `IsParam: true` for unannotated parameters; (Phase 6) `closeOpenParams`, `closeObjectType`; (Phase 7) No changes needed; ✅ (Phase 12) `closeOpenParams` now a `Checker` method; `resolveArrayConstraintsInType` and `resolveArrayConstraint` resolve constraints during closing; `resolveArrayConstraint` unifies `MethodElemVars` with `ElemTypeVar` before resolution to accumulate union types from multiple method calls; ✅ (Phase 14) `closeTupleType` for rest variable filtering; `resolveArrayConstraint` appends `RestSpreadType` with fresh TV to inferred tuples; `resolveArrayConstraintsInType` checks ArrayConstraint before pruning; `closeOpenParams` resolves ArrayConstraints before collecting returnVars |
| `internal/checker/infer_expr.go` | 2, 5, 7, 10, 12 | ✅ (Phase 2) `markPropertyWritten` in assignment handler; (Phase 7) No changes needed; ✅ (Phase 12) detect index assignment on `ArrayConstraint`, set `HasIndexAssignment` |
| `internal/checker/errors.go` | 11 | Not started |
| `internal/checker/tests/row_types_test.go` | All | ✅ Tests for Phases 1–7 (PropertyAccess, Errors, KeyOf, IntersectionAccess, PassToTypedFunction, WriteAfterPass, StringLiteralIndex, MethodCallInference, PropertyWidening, Closing, RowPolymorphism); ✅ Phase 12 (TupleArrayInference, TupleArrayInferenceEdgeCases); ✅ Phase 13 (VariadicTupleTypes, VariadicTupleSubtyping); ✅ Phase 14 (TupleRowPolymorphism) |
| `internal/checker/widening_test.go` | 4 | ✅ Unit tests for `flatUnion` and `typeContains` helpers |

---

## Verification

After each phase, run:
```bash
go test ./internal/checker/... -run TestRowTypes    # new tests
go test ./internal/type_system/...                  # type system tests
UPDATE_SNAPS=true go test ./internal/checker/...    # if snapshots change
go test ./...                                       # full suite for regressions
```
