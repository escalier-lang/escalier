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
| #379 (fe20b6a) | **Function generalization** — `GeneralizeFuncType` and `collectUnresolvedTypeVars` in `generalize.go`. Promotes unresolved type vars to type params. | Phase 6 (use existing `collectUnresolvedTypeVars`), Phase 7 (row var promotion via `GeneralizeFuncType`) |
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
Mutability is resolved during `GeneralizeFuncType` (Phase 6) based on the
`Written` flag. This avoids the complexity of `MutabilityType` unwrapping during
inference and simplifies the property access path.

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

## Phase 6: Closing After Function Body Inference (partially implemented)

**Requirements covered:** Section 5 (open vs. closed lifecycle), Section 5a
(mutability inference), Section 11d (interaction with closing).

**Goal:** After a function body is fully inferred, close all open object types on
parameters. Remove row variables that don't escape to callers. Resolve
mutability: if any property was written to, the parameter type becomes `mut`;
otherwise the `MutabilityType` wrapper is removed.

> **Partial implementation (2026-04-06):** Mutability resolution is implemented
> in `GeneralizeFuncType` (generalize.go). It runs before type variable
> promotion and checks `PropertyElem.Written` to determine whether the open
> object needs a `mut` wrapper. **Not yet implemented:** closing
> (`Open = false`) and removing `RestSpreadElem`s whose row variables don't
> appear in the return type. Currently, `Open` remains `true` and all
> `RestSpreadElem`s are preserved — this means row variables always become type
> parameters via `GeneralizeFuncType`, which is correct behavior for Phase 7
> (row polymorphism) but produces extra type parameters when the row variable
> isn't needed.

> **Status (2026-04-06):** The generalization work (#379) added
> `GeneralizeFuncType` and `collectUnresolvedTypeVars` in `generalize.go`. The
> The closing pass below uses `collectUnresolvedTypeVars` (from `generalize.go`)
> to determine which row variables escape to the return type. This replaces the
> originally planned `collectTypeVarIDs` helper. The closing pass should
> run **before** `resolveCallSites` and `GeneralizeFuncType`, which are now
> called after `inferFuncBodyWithFuncSigType` in `infer_module.go`.

### Changes

1. **`internal/checker/infer_func.go`** — After `inferFuncBody` returns
   (~line 190 in `inferFuncBodyWithFuncSigType`):
   Add a post-inference pass over `funcSigType.Params`:
   ```go
   for _, param := range funcSigType.Params {
       paramType := Prune(param.Type)

       // Unwrap MutabilityType to get at the ObjectType
       var mutWrapper *MutabilityType
       if mut, ok := paramType.(*MutabilityType); ok {
           mutWrapper = mut
           paramType = Prune(mut.Type)
       }

       if objType, ok := paramType.(*ObjectType); ok && objType.Open {
           objType.Open = false

           // Resolve mutability (Section 5a):
           // Check if any PropertyElem was written to
           hasWrites := false
           for _, elem := range objType.Elems {
               if prop, ok := elem.(*PropertyElem); ok && prop.Written {
                   hasWrites = true
                   break
               }
           }
           if mutWrapper != nil {
               if hasWrites {
                   mutWrapper.Mutability = MutabilityMutable
               } else {
                   // Read-only: remove the MutabilityType wrapper
                   // by pointing the param's type past it
                   param.Type = objType // or update typeVar.Instance
               }
           }

           // Collect unresolved type vars in the return type using the
           // existing helper from generalize.go (added in #379).
           returnVars := map[int]*TypeVarType{}
           returnOrder := &[]int{}
           collectUnresolvedTypeVars(funcSigType.Return, returnVars, returnOrder)
           // Remove RestSpreadElems whose row vars don't appear in return type
           filtered := make([]ObjTypeElem, 0, len(objType.Elems))
           for _, elem := range objType.Elems {
               if rest, ok := elem.(*RestSpreadElem); ok {
                   if tv, ok := Prune(rest.Value).(*TypeVarType); ok {
                       if _, found := returnVars[tv.ID]; !found {
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

2. **No new helper needed.** Use the existing `collectUnresolvedTypeVars`
   from `generalize.go` (added in #379). It recursively walks a type tree,
   follows `Prune()`, and collects all unresolved `TypeVarType` nodes into a
   `map[int]*TypeVarType`. Its signature:
   ```go
   func collectUnresolvedTypeVars(
       t type_system.Type,
       vars map[int]*type_system.TypeVarType,
       order *[]int,
   )
   ```
   This covers all composite types (`ObjectType`, `FuncType`, `UnionType`,
   `IntersectionType`, `TupleType`, `TypeRefType`, etc.) including
   `RestSpreadElem` values — so row variables in the return type are found.

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

- **Closed with mut (writes):**
  `fn foo(obj) { obj.bar = 5 }` — after inference, `obj` type is
  `mut {bar: number}` (closed, mutable, no `RestSpreadElem`).
- **Closed without mut (reads only):**
  `fn foo(obj) { let x = obj.bar }` — after inference, `obj` type is
  `{bar: t1}` (closed, immutable, no `MutabilityType` wrapper).
- **Mixed reads and writes:**
  `fn foo(obj) { let x = obj.bar; obj.baz = 5 }` — `obj` type is
  `mut {bar: t1, baz: number}` (any write makes the whole object `mut`).
- **RestSpreadElem preserved when in return type:**
  `fn foo(obj) { obj.x = 1; return obj }` — `RestSpreadElem` kept
  (tested further in Phase 7).
- **Multiple params, each closed independently:**
  `fn foo(a, b) { a.x = 1; b.y = "hi" }` — both closed, both
  `mut`.
- **Nested function:**
  `fn outer(a) { fn inner(b) { b.x = 1 }; a.y = "hi" }` — each
  closed after its own body, each `mut`.

---

## Phase 7: Row Polymorphism

**Requirements covered:** Section 11 (row polymorphism), Section 8b/8g (return
type propagation).

**Goal:** When an inferred parameter is returned, its row variable becomes a type
parameter on the function, enabling callers to preserve extra properties.

> **Status (2026-04-06, verified):** `GeneralizeFuncType` (#379) handles row
> variables without any special treatment. `collectUnresolvedTypeVars` walks
> into `RestSpreadElem.Value` and collects the inner TypeVar.
> `GeneralizeFuncType` sets `tv.Instance = TypeRefType{...}`, which binds the
> TypeVar *inside* the `RestSpreadElem` — the wrapper is untouched. After
> generalization: `RestSpreadElem{Value: TypeVarType{Instance: TypeRefType}}`.
> At call sites, `instantiateGenericFunc` → `SubstituteTypeParams` →
> `RestSpreadElem.Accept()` substitutes the inner `TypeRefType` with a fresh
> TypeVar while preserving the `RestSpreadElem` wrapper. **No changes to
> `GeneralizeFuncType` or `instantiateGenericFunc` are needed.**

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

3. **Multiple row variables:** When multiple parameters each have row variables
   in the return type (e.g. `fn merge(a, b) { return {...a, ...b} }`), each gets
   its own type parameter. This is naturally handled since each parameter has a
   distinct `RestSpreadElem` with a distinct row variable ID.

### Tests

- **Basic row polymorphism:**
  ```esc
  fn foo(obj) { obj.x = 1; return obj }
  let r = foo({x: 1, y: 2})
  ```
  — `r` type is `{x: number, y: number}`.

- **Multiple extra properties:**
  ```esc
  fn foo(obj) { obj.x = 1; return obj }
  let r = foo({x: 1, y: 2, z: "hi"})
  ```
  — `r` has all three properties.

- **No return — row variable removed:**
  `fn foo(obj) { obj.x = 1 }` — no type parameter, `RestSpreadElem`
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
  `fn foo(obj) { return {...obj, extra: 1} }` — return type has
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
Phase 1: Type System Extensions ✅
└── Phase 2: Property Access ✅
    ├── Phase 3: Unification ✅
    │   ├── Phase 4: Widening ✅
    │   │   └── Phase 5: Method Calls ✅
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

1. ~~**Widening (Phase 4):**~~ Resolved. The `unifyWithDepth` / `unifyPruned`
   split cleanly saves pre-Prune references. Gating via `Widenable` ensures
   normal type inference is unaffected (verified by
   `NormalTypeVarConflictStillErrors` test).

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

| File | Phases | Status |
|------|--------|--------|
| `internal/type_system/types.go` | 1 | ✅ `Open`, `Widenable`, `IsParam`, `Written` fields added; `Accept`/`Copy` updated |
| `internal/checker/expand_type.go` | 2, 9, 10 | ✅ (Phase 2) `TypeVarType` case in `getMemberType`, open-object handling in `getObjectAccess`, helper functions |
| `internal/checker/unify.go` | 3, 4, 10 | ✅ (Phase 3) `openClosedObjectForParam`, open-vs-open/closed paths; (Phase 4) `unifyPruned` refactor, `widenLiteral`, `flatUnion`, `typeContains`, `unwrapMutability` |
| `internal/checker/generalize.go` | 2, 6, 7 | ✅ (Phase 2) Mutability resolution in `GeneralizeFuncType`, `Open` preserved in `deepCloneType` |
| `internal/checker/infer_func.go` | 3, 6, 7, 8 | ✅ (Phase 3) `IsParam: true` for unannotated parameters |
| `internal/checker/infer_expr.go` | 2, 5, 10 | ✅ (Phase 2) `markPropertyWritten` in assignment handler, open-object pointer identity preservation |
| `internal/checker/errors.go` | 11 | Not started |
| `internal/checker/tests/row_types_test.go` | All | ✅ Tests for Phases 1–4 (PropertyAccess, Errors, KeyOf, IntersectionAccess, PassToTypedFunction, WriteAfterPass, StringLiteralIndex, PropertyWidening) |
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
