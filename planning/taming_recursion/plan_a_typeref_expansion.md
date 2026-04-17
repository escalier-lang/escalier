# Plan A: Expand at the TypeRefType match site, not in a catch-all retry

## Goal

Replace the catch-all retry loop at the bottom of `unifyPruned` (lines 1189-1203)
with explicit `TypeRefType` handling. This makes expansion predictable: it only
happens for the type kinds that need it, retrying as needed within a bounded
loop to peel through alias chains.

## Background

Currently, when `unifyPruned` fails to match any explicit case, it falls through to
a retry loop that calls `ExpandType(ctx, t, 1)` on both sides. If either side
expanded (detected by pointer inequality), it recurses into `unifyWithDepth` with
`depth + 1`. This has two problems:

1. `ExpandType` always allocates new objects, so `expandedT1 != t1` is always true
   for expandable types, even when the result is structurally identical.
2. Non-`TypeRefType` types can also trigger expansion (e.g. `CondType`, `KeyOfType`,
   `IndexType`), but these are already handled in their own cases within
   `ExpandType`'s visitor. The retry loop re-expands types that don't need it.

## Analysis of what reaches the retry loop

The retry loop is reached when neither `t1` nor `t2` matches any of the explicit
cases in `unifyPruned`. After reviewing all cases, the scenarios that fall through
to the retry are:

1. **TypeRefType + TypeRefType (different alias)** — Lines 505-511 have a TODO and
   do nothing, so these fall through.
2. **TypeRefType + any non-TypeRef concrete type** — e.g. `TypeRefType` vs
   `ObjectType`, `PrimType`, `LitType`, `UnionType`, etc. There is no case that
   handles "one side is a TypeRefType, the other is something else."
3. **TypeOfType + any type** — `TypeOfType` (e.g. `typeof obj`) has no explicit case
   in `unifyPruned` and relies on the retry loop's `ExpandType` to resolve it.
4. **Unrecognized type combinations** — Any pair of types that doesn't match the
   listed cases (e.g. `NullType` + `ObjectType`). These should be genuine
   unification errors.

In cases 1-3, the retry loop expands the types and retries. In case 4, expansion
doesn't change either type, so the retry doesn't fire and the function falls through
to the final `CannotUnifyTypesError`.

## Discovered issues from prototyping

### Issue 1: `expandTypeRef` fails on non-alias TypeRefTypes

`expandTypeRef` (expand_type.go:1617) returns an `UnknownTypeError` when the
TypeRefType refers to something that isn't a type alias (e.g. enum variant `RGB`).
The old `ExpandType` also errored in this case but returned `NeverType`, which
unifies with everything. A new `canExpandTypeRef` helper is needed that returns
`bool` — returning `false` when resolution fails, so the caller can fall through
gracefully.

### Issue 2: Stack overflow from recursive `unifyWithDepth` calls

Calling `unifyWithDepth(ctx, expandedT1, t2, depth+1)` for each TypeRefType
expansion adds a full `unifyWithDepth` → `unifyPruned` frame to the call stack.
For types with many properties (React SVG elements: 200+ properties), each property
may be a TypeRefType that triggers another expansion round. Since property-by-property
unification uses `c.Unify()` (which resets depth to 0), the depth counter cannot
prevent stack overflow — the stack overflows before `maxUnifyDepth` is reached.

**Fix**: Use an in-place expansion loop instead of recursive calls. See the plan
below for the structure that avoids adding indentation to the existing case-matching
code.

### Issue 3: TypeOfType also relies on the retry loop

`TypeOfType` (e.g. `typeof obj`) has no explicit case in `unifyPruned`. The retry
loop expands it via `ExpandType`, which resolves the `typeof` expression to the
value's type. Without the retry loop, `ObjectType` vs `TypeOfType` (found in the
getter/setter tests) fails with `CannotUnifyTypesError`.

**Fix**: Add a targeted expansion for non-TypeRefType expandable types using
`ExpandType(ctx, t, 0)` (count=0 skips TypeRef expansion, since those are already
handled). Only retry when the type actually changed (to avoid the pointer-inequality
trap with ObjectTypes).

Note: `ExpandType` with count=0 does NOT expand TypeRefTypes (since
`expandTypeRefsCount == 0` returns nil in the visitor). It DOES expand TypeOfType,
CondType, and other non-TypeRef expandable types. For plain ObjectTypes with only
TypeRefType properties, `ExpandType(obj, 0)` returns the same pointer since nothing
inside changes — so the pointer-inequality check is reliable here.

**Why pointer identity is preserved**: The visitor's `Accept` implementations track
whether any child changed. When the visitor returns nil for a child (as it does for
TypeRefType when count=0), the Accept method keeps the original pointer. If no
children changed, the parent returns its own original pointer. This propagates up
the tree, so `ExpandType(ctx, t, 0) == t` (pointer equality) when `t` contains
nothing expandable at count=0.

### Issue 4: Type parameter references expanded to their constraints

**This is the most fundamental issue.** `TypeRefType` is used to represent both
type alias references (`type Foo = ...`) and type parameter references (`<T>`).
For type parameters, the `TypeAlias` field on the `TypeRefType` is set to a
`TypeAlias` whose `Type` is the parameter's constraint (e.g. `unknown` for
unbounded `<T>`).

When TypeRefType expansion runs BEFORE the case-matching logic, expanding
`TypeRefType(T)` resolves the TypeAlias and returns the constraint `unknown`. This
destroys the type parameter's identity, breaking union member matching.

**Example failure** (`ClassWithGenericMethod` test):

```escalier
class Box(value: number) {
    value,
    getValue<T>(self, default: T) -> number | T {
        if self.value != 0 { return self.value }
        else { return default }
    }
}
```

When checking the return type, the checker unifies the inferred return type
`number | T` with the declared return type `number | T`. This enters the
`UnionType, _` case which iterates over the left union's members:

- `number` vs `number | T` → matches `number` member → OK
- `TypeRefType(T)` vs `number | T` → enters `_, UnionType` case, which probes:
  - `TypeRefType(T)` vs `number` → fails
  - `TypeRefType(T)` vs `TypeRefType(T)` → same-alias case → succeeds!

**Old code (working):** The retry loop runs AFTER the case-matching logic. By the
time `TypeRefType(T)` reaches the retry loop, the union case has already matched
it via same-alias TypeRefType comparison. The retry loop's expansion of `T` to
`unknown` is never reached for this case.

**New code (broken when expansion is before cases):** The in-place expansion loop
runs BEFORE the union cases. `TypeRefType(T)` is expanded to `unknown` before the
`_, UnionType` case gets a chance to match it. Then `unknown` (top type) can't be
assigned to any member of `number | T`, since `UnknownType` on the left side
errors unless the right side is also `UnknownType` (line 286-294).

**Root cause:** The `TypeAlias` struct has no way to distinguish a regular type
alias from a type parameter constraint. Both look identical:

```go
// Regular type alias: type Foo = unknown
TypeAlias{Type: unknown, TypeParams: [], Exported: false}

// Type parameter constraint: <T> (implicit unknown constraint)
TypeAlias{Type: unknown, TypeParams: [], Exported: false}
```

**Fix (two parts):**

1. **Add `IsTypeParam` flag to `TypeAlias`:** Add an `IsTypeParam bool` field to the
   `TypeAlias` struct. Set it to `true` at all type parameter creation sites. The
   `canExpandTypeRef` helper checks this flag and returns `false` for type
   parameter aliases, preventing expansion from destroying the parameter's identity.

   ```go
   type TypeAlias struct {
       Type        Type
       TypeParams  []*TypeParam
       Exported    bool
       IsTypeParam bool // true for type parameter scope entries, not real aliases
   }
   ```

   Creation sites that need `IsTypeParam: true`:
   - `infer_func.go` — function type params (`inferFuncTypeParams`)
   - `infer_stmt.go` — type params in `buildTypeParams`
   - `infer_module.go` — class type params and enum type params
   - `generalize.go` — generalized type vars bound to TypeRefType
   - `infer_type_ann.go` — `infer` types in conditional types and mapped type params

   Creation sites that stay `IsTypeParam: false` (default):
   - `infer_stmt.go` — `Self` alias for interfaces
   - `infer_expr.go` — `Self` alias for object expressions
   - `infer_import.go` — re-exported aliases (copies from existing aliases)
   - All regular type/class/enum/interface declarations in `infer_module.go`

2. **Position expansion AFTER case-matching:** The case-matching code gets a chance to
   handle TypeRefTypes via same-alias comparison and union member matching first. Only
   when no case matches do we expand and retry. This provides defense in depth — the
   ordering prevents the issue in practice, and the flag prevents it explicitly.

Both parts are used in the plan below.

**Future direction:** If `TypeAlias` serving double duty for both real aliases and type
parameters continues to cause issues, a cleaner separation would be to stop storing
type parameters as `TypeAlias` entries entirely. Instead, store `*TypeParam` directly
in a separate `TypeParams` map on `Namespace`/`Scope`, with dedicated
`GetTypeParam`/`SetTypeParam` methods. This would make the two concepts explicitly
different in the type system, at the cost of a larger refactor (scope API changes, and
every type name resolution path would need to check both maps). The `IsTypeParam` flag
is the pragmatic first step; the full separation can be done later if warranted.

## Discovered issues from implementation attempt

The following issues were discovered during an actual implementation attempt
(beyond the earlier prototyping phase).

### Issue 5: `bind()` side effects leak through the expansion loop

**Problem:** When `unifyMatched` calls `bind(t1, t2)` for a TypeVarType, `bind`
both **returns errors** (from constraint checking) and **sets `Instance`** as a
side effect. In the old code, errors from `unifyPruned` were returned directly.
In the new code, the expansion loop sees the errors and tries to "fix" them by
expanding TypeRefTypes — but the binding already happened, so the retry can
produce incorrect results.

**Example:** `fn bar(items: Array<string>)` / `fn foo(items) { items[0] = 42;
bar(items) }` — the constraint check `number vs string` correctly fails inside
`bind`, but the expansion loop then expands `Array<string>` structurally and
retries, accidentally succeeding.

**Fix:** After `unifyMatched` returns errors, check if either `t1` or `t2` is a
TypeVarType. If so, the error came from `bind` and is authoritative — return
immediately without attempting expansion. This is correct because `bind` handles
all TypeVarType cases exhaustively; expansion cannot help.

### Issue 6: Same-alias TypeRefTypes expanded after type-arg mismatch

**Problem:** When `Array<number>` vs `Array<string>` fails in the same-alias case
(type args don't match), the expansion loop expands both to their structural
ObjectTypes and retries. The structural comparison may succeed incorrectly or
produce confusing errors, when the same-alias type-arg error was the correct answer.

**Fix:** Before attempting expansion, check if both sides are same-alias
TypeRefTypes via `sameTypeRef()`. If so, return the error immediately — the
same-alias case in `unifyMatched` already compared their type args definitively.

### Issue 7: Self-referential type parameter aliases cause infinite expansion loop

**Problem:** `infer_stmt.go:buildTypeParams` creates self-referential aliases for
type parameters: type param `A` gets `TypeAlias{Type: TypeRefType(A)}`. Each call
to `canExpandTypeRef`/expansion "expands" `A` into a new copy of `TypeRefType(A)`, causing
an infinite loop bounded only by `maxExpansionRetries`. Three creation sites produce
self-referential aliases:
- `infer_stmt.go:buildTypeParams` — type declaration type params
- `infer_type_ann.go` — conditional type `infer` params
- `infer_type_ann.go` — mapped type params

The old code avoided this because `ExpandType(t, 1)` decrements an internal counter
and stops after one level, then the retry called `unifyWithDepth` with `depth+1`,
eventually hitting `maxUnifyDepth`.

**Fix:** In `canExpandTypeRef`, after resolving the alias, check if the expanded
type is a TypeRefType with the same name as the input. If so, return `(nil, false)`
to prevent the self-referential loop.

```go
if ref, ok := expandedType.(*type_system.TypeRefType); ok {
    if type_system.QualIdentToString(ref.Name) == type_system.QualIdentToString(t.Name) {
        return nil, false
    }
}
```

This is more targeted than `IsTypeParam` (which blocks ALL type param expansion).
The self-referentiality check blocks only the cycle (`A→A`) while allowing
constraint chains (`C→B` where `C: B`) to expand normally.

Note: This does NOT catch mutual recursion (`A→B→A`). Mutual recursion would still
loop until `maxExpansionRetries`. This is acceptable for now — Plan B's visited set
will handle mutual recursion properly.

### Issue 8: Type param constraint chains blocked by `IsTypeParam`

**Problem:** The `IsTypeParam` flag on `canExpandTypeRef` prevents ALL type
parameter expansion, including constraint chains like `C: B, B: A, A: string`.
When `C` vs `A` fails in `unifyMatched` (different names), the expansion loop
needs to expand `C → B's constraint` and `A → string` to eventually unify. But
`IsTypeParam` blocks this.

**Example:** `fn convert<C: B, B: A, A: string>(x: C) -> A { return x }` — the
return type check needs `C` expanded through the chain to verify it's assignable
to `A`.

**Fix:** Remove the `IsTypeParam` check from `canExpandTypeRef`. The Issue 4 fix
(expansion AFTER case-matching) already provides the necessary defense — the
same-alias case in `unifyMatched` handles `T vs T` before expansion runs, so type
parameter identity is preserved where it matters. The self-referentiality check
from Issue 7 prevents the infinite loop that `IsTypeParam` was also guarding
against.

The `IsTypeParam` field should still be added to `TypeAlias` (it's useful
documentation and may be needed for future use), but `canExpandTypeRef` should NOT
check it.

### Issue 9: Expanded TypeAlias types share mutable pointers

**Problem:** Directly using `typeAlias.Type` from a resolved alias (as an earlier
prototype did) passes a shared pointer into the `TypeAlias` struct. When the
expansion loop passes this to `unifyMatched`, property-by-property unification can
**mutate** the shared type via TypeVarType binding, corrupting the type alias for
all future uses.

The `TypeAlias.Type` is often a `TypeVarType` (not the concrete type). For
example, `type Point = {x: number, y: number}` creates a TypeAlias whose Type is
a TypeVarType bound to the ObjectType `{x: number, y: number}`. If this
TypeVarType were passed directly to `unifyMatched`, the bind logic could rebind it
to the literal type from the other side of the unification (e.g., `{x: 1, y: 2}`),
permanently corrupting Point's alias.

**Confirmed via debugging:** For `val p: Point = {x: 1, y: 2}`, Point's
`TypeAlias.Type` changes from `TypeVarType({x: number, y: number})` to
`TypeVarType({x: mut? 1, y: mut? 2})` after `Unify(initType, taType)`.

The old code avoided this because `ExpandType(ctx, t, 1)` creates **fresh copies**
via the visitor's Accept mechanism. The copies are structurally identical but
pointer-distinct, so unification side effects don't propagate back to the alias.

**Fix:** Use `canExpandTypeRef` as a predicate only (it returns `bool`, not the
expanded type). When it returns `true`, delegate to `ExpandType(ctx, t, 1)` (which
creates fresh copies via the visitor) followed by `unifyWithDepth(ctx, expanded1,
expanded2, depth+1)`. The `unifyWithDepth` call:
1. Prunes both types (resolving TypeVarTypes to concrete types)
2. Goes through the full unification pipeline (including widening)
3. Works on fresh copies, not shared TypeAlias pointers

This means `canExpandTypeRef` handles the "should we expand?" decision, but the
actual expansion + retry goes through `ExpandType` + `unifyWithDepth`. The
`depth+1` parameter bounds the recursion (same as the old code).

**Why this doesn't reintroduce Issue 2 (stack overflow):** In the old code, EVERY
failed case triggered `unifyWithDepth` recursion (via the catch-all retry loop). In
the new code, `unifyWithDepth` is only called when a TypeRefType was actually
expanded — not for every failed match. The recursion depth is proportional to the
alias chain depth (typically 1-3 levels), not to the number of type properties.
For React SVG elements with 200+ properties, each property's unification calls
`c.Unify()` (which starts its own `unifyWithDepth` at depth 0), but the expansion
recursion adds at most one extra frame per alias level — not per property.

**Impact on Plans B and C:** This approach means `depth+1` recursion is still used
for TypeRefType expansion, so `maxUnifyDepth` remains necessary as a safety net.
Plan B's visited set will need to be threaded through `unifyWithDepth` calls (which
was already the plan). Plan C's goal of removing `maxUnifyDepth` may need to keep
it as a fallback even after the visited set is in place, unless the visited set
provably prevents all unbounded recursion through this path.

### Issue 10: Nominal types need ExpandType for pattern matching

**Problem:** `canExpandTypeRef` blocks expansion of nominal ObjectTypes (classes)
to prevent bypassing nominal identity checks. But pattern matching against nominal
types (`match p { {foo} => foo }`) requires expanding the TypeRefType to access
the ObjectType's properties and report `PropertyNotFoundError`.

In the old code, `ExpandType(t, 1)` always expanded TypeRefTypes regardless of
nominality. The nominal semantics were enforced in the ObjectType vs ObjectType
case in `unifyMatched`, which allows structural comparison in pattern-matching
mode (`ctx.IsPatMatch`).

**Fix:** Keep the nominal check in `canExpandTypeRef` (it prevents infinite loops
from self-referential class types). For TypeRefTypes where `canExpandTypeRef`
returns false (nominal, unresolved, etc.), fall through to `ExpandType(ctx, t, 1)`
as a last resort. `ExpandType` always expands TypeRefTypes via the visitor
regardless of nominality — the visitor simply resolves the alias without checking
nominal semantics. This matches the old code's behavior where ExpandType always
expanded, and the nominal semantics were handled downstream in the ObjectType vs
ObjectType case in `unifyMatched`.

In the `unifyPruned` loop, after `canExpandTypeRef` returns false and
`ExpandType(ctx, t, 0)` also fails, add a final fallback:

```go
// Last resort: ExpandType with count=1 for TypeRefTypes that
// canExpandTypeRef refused (e.g. nominal types).
if isRef1 || isRef2 {
    lastResortT1, _ := c.ExpandType(ctx, t1, 1)
    lastResortT2, _ := c.ExpandType(ctx, t2, 1)
    if lastResortT1 != t1 || lastResortT2 != t2 {
        return c.unifyWithDepth(ctx, lastResortT1, lastResortT2, depth+1)
    }
}
```

This uses `unifyWithDepth` (not `continue`) for the same shared-pointer safety
reasons as Issue 9.

## Plan (revised after implementation attempt)

### Step 1: Add `IsTypeParam` flag to `TypeAlias` and update creation sites

Add `IsTypeParam bool` to the `TypeAlias` struct in `type_system/types.go`. Then set
`IsTypeParam: true` at all type parameter creation sites listed in the Issue 4 fix
above. The default `false` is correct for all existing regular aliases, so no other
sites need changes.

Note: Per Issue 8, `canExpandTypeRef` does NOT check this flag — the
self-referentiality check (Issue 7) and expansion-after-matching ordering (Issue 4)
provide sufficient protection. The flag is still valuable as documentation and for
potential future use.

### Step 2: Add `canExpandTypeRef` helper

Add a new method to `Checker` (in expand_type.go) that attempts to expand a
TypeRefType without erroring. This function is used only as a "should we expand?"
predicate — it returns `bool`, not the expanded type. The actual expansion is done
by `ExpandType(ctx, t, 1)` which creates fresh copies (see Issue 9).

```go
func (c *Checker) canExpandTypeRef(ctx Context, t *type_system.TypeRefType) bool {
    typeAlias := t.TypeAlias
    if typeAlias == nil {
        typeAlias = resolveQualifiedTypeAlias(ctx, t.Name)
    }
    if typeAlias == nil {
        return false
    }

    expandedType := type_system.Prune(typeAlias.Type)

    // Don't expand nominal object types — nominal semantics are enforced
    // in the ObjectType vs ObjectType case in unifyMatched. Expanding here
    // would bypass nominal identity checks and can cause infinite loops
    // for self-referential class types.
    if obj, ok := expandedType.(*type_system.ObjectType); ok && obj.Nominal {
        return false
    }

    // Don't expand self-referential aliases (e.g., type param A whose alias
    // Type is TypeRefType(A)). These occur at type parameter creation sites
    // where the alias is a forward-reference placeholder. Expanding would
    // produce a new copy of the same TypeRefType, causing an infinite loop.
    if ref, ok := expandedType.(*type_system.TypeRefType); ok {
        if type_system.QualIdentToString(ref.Name) == type_system.QualIdentToString(t.Name) {
            return false
        }
    }

    return true
}
```

Note: The self-referentiality check compares `QualIdentToString` names. This is
sufficient because `QualIdent` includes namespace qualification, so same-named type
parameters in different scopes will have distinct qualified names. However, mutual
recursion (`A→B→A`) is NOT caught — see Issue 7 notes on this limitation.

Key differences from the original plan:
- **Returns `bool` only** — the expanded type is not needed since the actual
  expansion is delegated to `ExpandType(ctx, t, 1)` (Issue 9). This avoids
  computing substitutions for generic aliases only to discard the result.
- **Prunes `typeAlias.Type`** before checking — the alias Type is often a
  TypeVarType, not a concrete type. Without pruning, the nominal and
  self-referentiality checks would see a TypeVarType instead of the underlying type.
- **No `IsTypeParam` check** — removed per Issue 8. The self-referentiality check
  handles the infinite loop case, and constraint chains need expansion to work.
- **Self-referentiality check added** — per Issue 7. Detects `A→A` cycles.

### Step 3: Extract case-matching into `unifyMatched`, add expansion logic in `unifyPruned`

Rename the current `unifyPruned` to `unifyMatched` (the function that contains all
the explicit type-matching cases). Then rewrite `unifyPruned` as a loop that:
1. Calls `unifyMatched` with the current types
2. If matching succeeds, returns immediately
3. If matching fails, applies guards (Issue 5, 6) and tries expansion
4. If a TypeRefType expanded, delegates to `unifyWithDepth` (Issue 9)
5. If non-TypeRef types expanded (TypeOfType etc.), retries via the loop
6. If nothing expanded, returns the original error

```go
const maxExpansionRetries = 10

func (c *Checker) unifyPruned(ctx Context, t1, t2 type_system.Type, depth int) []Error {
    for attempt := 0; attempt < maxExpansionRetries; attempt++ {
        errors := c.unifyMatched(ctx, t1, t2, depth)
        if len(errors) == 0 {
            return nil
        }

        // Issue 5: If either type is a TypeVarType, the error came from bind
        // (constraint checking). Expansion cannot help — return immediately.
        if _, ok := t1.(*type_system.TypeVarType); ok {
            return errors
        }
        if _, ok := t2.(*type_system.TypeVarType); ok {
            return errors
        }

        // Issue 6: Don't expand same-alias TypeRefTypes — the same-alias case
        // in unifyMatched already compared their type args definitively.
        ref1, isRef1 := t1.(*type_system.TypeRefType)
        ref2, isRef2 := t2.(*type_system.TypeRefType)
        if isRef1 && isRef2 && c.sameTypeRef(ref1, ref2) {
            return errors
        }

        // Try expanding TypeRefTypes. Use canExpandTypeRef as a "should we
        // expand?" predicate, then delegate to ExpandType + unifyWithDepth
        // for the actual retry. ExpandType creates fresh copies via the
        // visitor (preventing mutation of shared TypeAlias types — Issue 9),
        // and unifyWithDepth provides Prune + widening on the expanded result.
        refCanExpand := false
        if isRef1 && c.canExpandTypeRef(ctx, ref1) {
            refCanExpand = true
        }
        if isRef2 && c.canExpandTypeRef(ctx, ref2) {
            refCanExpand = true
        }
        if refCanExpand {
            refExpT1, _ := c.ExpandType(ctx, t1, 1)
            refExpT2, _ := c.ExpandType(ctx, t2, 1)
            return c.unifyWithDepth(ctx, refExpT1, refExpT2, depth+1)
        }

        // Try expanding TypeOfType and other non-TypeRef expandable types.
        // ExpandType with count=0 skips TypeRef expansion (already handled).
        // Pointer-equality check is reliable here (see Issue 3 notes).
        nonRefExpT1, _ := c.ExpandType(ctx, t1, 0)
        nonRefExpT2, _ := c.ExpandType(ctx, t2, 0)
        if nonRefExpT1 != t1 || nonRefExpT2 != t2 {
            t1 = nonRefExpT1
            t2 = nonRefExpT2
            continue
        }

        // Issue 10: Last resort for TypeRefTypes that canExpandTypeRef
        // refused (e.g. nominal types). ExpandType(t, 1) always expands
        // TypeRefTypes via the visitor regardless of nominality — the visitor
        // simply resolves the alias without checking nominal semantics.
        // Nominal semantics are enforced downstream in the ObjectType vs
        // ObjectType case in unifyMatched (which allows structural comparison
        // in pattern-matching mode via ctx.IsPatMatch).
        if isRef1 || isRef2 {
            lastResortT1, _ := c.ExpandType(ctx, t1, 1)
            lastResortT2, _ := c.ExpandType(ctx, t2, 1)
            if lastResortT1 != t1 || lastResortT2 != t2 {
                return c.unifyWithDepth(ctx, lastResortT1, lastResortT2, depth+1)
            }
        }

        // Nothing could be expanded, return the original error
        return errors
    }
    return []Error{&CannotUnifyTypesError{T1: t1, T2: t2}}
}
```

Note: `ExpandType` returns `(Type, []Error)`. The errors are ignored (via `_`)
throughout this function. This matches the existing retry loop behavior (lines
1190-1196). It is safe because: (a) `ExpandType` errors come from `expandTypeRef`
returning `UnknownTypeError` for unresolvable TypeRefTypes (Issue 1), and
`canExpandTypeRef` already screens these out; (b) for the non-TypeRef path
(`count=0`), TypeRefTypes are skipped entirely so `expandTypeRef` is never called;
(c) for the Issue 10 last-resort path, expansion failure is detected by pointer
equality (`lastResortT1 != t1`) and the function falls through to return the
original `unifyMatched` errors.

The existing case-matching code moves to `unifyMatched` with no changes to its
body or indentation. The do-nothing different-alias case (lines 505-511) and the
old retry loop (lines 1189-1203) are both deleted. The final `CannotUnifyTypesError`
at the end of `unifyMatched` remains — it signals to `unifyPruned` that no case
matched, triggering the expansion logic.

Note: The **same-alias** TypeRefType case (lines 448-504, `c.sameTypeRef(ref1, ref2)`)
is kept in `unifyMatched`. When two TypeRefTypes refer to the same alias, they match
directly via type argument unification without needing expansion. Only the
**different-alias** case is removed, since `unifyPruned`'s expansion logic now handles
it by expanding both sides and retrying.

**Key design decision — `unifyWithDepth` vs `continue`:** TypeRefType expansion
delegates to `unifyWithDepth` (a recursive call) rather than looping via `continue`.
This is necessary because:
- `unifyWithDepth` prunes types, resolving shared TypeVarType pointers to concrete
  types before they enter `unifyMatched` (prevents Issue 9 corruption)
- `unifyWithDepth` provides the widening fallback for Widenable TypeVarTypes
- `ExpandType(ctx, t, 1)` creates fresh copies, preventing mutation of shared state
- The recursion depth is bounded by `depth+1` and proportional to alias chain depth
  (typically 1-3), not to property count (which caused Issue 2)

Non-TypeRef expansion (TypeOfType etc.) uses `continue` because `ExpandType(ctx, t, 0)`
already creates copies and the pointer-equality check prevents spurious retries.

### Step 4: Audit other ExpandType calls in unify.go

There are two other `ExpandType` calls in the case-matching code that are unrelated
to the retry loop and should be preserved:

- **Line 345-346**: `KeyOfType` expansion — both sides are `KeyOfType`, expand their
  inner types. Keep as-is.
- **Line 1055**: Union + ObjectType — expands each union member to check if it's an
  `ObjectType`. Keep as-is.

### Step 5: Set an appropriate loop bound

The loop in `unifyPruned` now primarily handles non-TypeRef expansion (TypeOfType
etc.) via the `continue` path. TypeRefType expansion exits via `unifyWithDepth`
(which has its own `maxUnifyDepth` bound). The `maxExpansionRetries` constant
guards against unexpected loops in the non-TypeRef path.

Use a dedicated constant (e.g. `maxExpansionRetries = 10`). After Plan B
(visited-set) is implemented, both `maxExpansionRetries` and `maxUnifyDepth`
become safety nets rather than primary cycle-prevention mechanisms.

## Testing strategy

1. **Run the full test suite** — This is a behavioral refactor; all existing tests
   should pass without changes.
2. **Verify depth behavior** — Add debug logging (temporary) to count the maximum
   loop iterations reached during the test suite. After this change, the max should
   correspond to the actual nesting depth of type alias chains.
3. **Test TypeRefType + TypeRefType (different alias)** — Write a test with two
   different type aliases that resolve to the same structural type and verify they
   unify:
   ```escalier
   type Foo = { x: number }
   type Bar = { x: number }
   val a: Foo = ...
   val b: Bar = a  // should succeed
   ```
4. **Test TypeRefType + concrete type** — Write a test where one side is a type
   alias and the other is an inline type:
   ```escalier
   type Foo = { x: number }
   val a: Foo = { x: 5 }  // TypeRefType vs ObjectType
   ```
5. **Test generic methods** — Verify `ClassWithGenericMethod` and
   `ObjectWithGenericMethods` pass, confirming type parameter identity is preserved
   through union matching.
6. **Test React types** — Verify `TestImportInferenceScript/NamespaceImportReact`
   passes without stack overflow, confirming the loop-based approach handles large
   types.
7. **Test bind error propagation (Issue 5)** — The existing test for
   `fn bar(items: Array<string>)` / `fn foo(items) { items[0] = 42; bar(items) }`
   should continue to report a `number vs string` constraint error. Verify
   expansion does not mask it.
8. **Test same-alias type-arg mismatch (Issue 6)** — Verify that
   `Array<number> vs Array<string>` produces a type-arg error, not a structural
   comparison error from expanding both aliases.
9. **Test self-referential type params (Issue 7)** — Existing tests with generic
   type declarations (e.g. `type Pair<A, B> = ...`) exercise the self-referential
   alias path. Verify no infinite loops or hangs.
10. **Test alias corruption (Issue 9)** — Verify that after
    `val p: Point = {x: 1, y: 2}`, using `Point` again in a subsequent declaration
    still refers to `{x: number, y: number}`, not the literal types.
11. **Test pattern matching on nominal types (Issue 10)** — Existing getter/setter
    and pattern-matching tests exercise this path. Verify `PropertyNotFoundError`
    is reported correctly when destructuring a nominal type.

## Risks

- **Missed cases**: If there are non-`TypeRefType` types that relied on the retry
  loop for expansion (e.g. a `CondType` or `IndexType` that somehow reaches
  `unifyPruned` unexpanded), those would now fail with `CannotUnifyTypesError`
  instead of being retried. Mitigation: run the full test suite and React type
  tests to catch these. The `ExpandType(ctx, t, 0)` fallback handles most of these,
  and the `ExpandType(ctx, t, 1)` last resort (Issue 10) covers the rest.
- **Shared mutable state**: TypeAlias types are shared across all references to the
  alias. Any code path that passes a TypeAlias.Type pointer directly to unification
  risks corruption via TypeVarType rebinding. The plan mitigates this by: (a) using
  `ExpandType` to create fresh copies before `unifyWithDepth`, and (b) pruning in
  `canExpandTypeRef` to resolve TypeVarTypes before nominal/self-referential checks.
  New code touching TypeAlias.Type should be audited for this pattern.
- **Wasted case matching**: `TypeRefType + ObjectType` falls through all cases in
  `unifyMatched` before being expanded in the `unifyPruned` loop. This is
  functionally correct but slightly less efficient than early expansion. The cost
  is negligible since case matching is cheap compared to type expansion.
- **`unifyWithDepth` recursion**: TypeRefType expansion now uses `unifyWithDepth`
  recursion (with `depth+1`) rather than a purely iterative loop. This is bounded
  by alias chain depth (typically 1-3) and `maxUnifyDepth`, not by property count.
  Plan B's visited set will provide additional protection against cycles.
- **Mutual recursion not handled**: The self-referentiality check in
  `canExpandTypeRef` only catches direct cycles (`A→A`), not mutual recursion
  (`A→B→A`). These rely on `maxExpansionRetries`/`maxUnifyDepth` until Plan B adds
  proper cycle detection.
