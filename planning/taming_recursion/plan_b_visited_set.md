# Plan B: Visited-set / seen-pairs memoization

**Prerequisite:** Plan A (expand at TypeRefType match site) should be implemented
first. The catch-all retry loop would interfere with visited-set tracking because
it creates new type objects on every iteration, defeating pointer-based identity.

## Goal

Add a visited-set mechanism to both `Unify` and `ExpandType` that tracks which
type pairs or type alias expansions have already been attempted. This provides
principled cycle detection that replaces the ad-hoc depth counters and enables
future support for recursive type aliases.

## Background

After Plan A, the depth counter in `unifyWithDepth` only increments for genuine
TypeRefType expansions. But it's still an arbitrary limit — there's no guarantee
that `maxUnifyDepth = 50` is sufficient for all programs, and reducing it risks
rejecting valid programs.

The standard approach in type checkers is co-inductive unification: if you
encounter a pair `(t1, t2)` that you've already started unifying, assume it will
succeed. This is sound for recursive types because the only way to encounter the
same pair twice is through a cycle in the type structure, and the non-cyclic parts
of the type have already been checked.

## Design decisions

### What constitutes identity for a type pair?

For unification, the pair key should be the *identity* of the two types being
unified, not their structure. Two options:

- **Pointer-based identity**: Use `(unsafe.Pointer(t1), unsafe.Pointer(t2))` as
  the map key. This is fast but fragile — if a type is reconstructed (new
  allocation) during expansion, it won't be recognized as the same type.
- **TypeAlias-based identity for TypeRefType**: When unifying two `TypeRefType`s,
  use the `TypeAlias` pointer plus the type arguments as the key. This captures
  the meaningful identity: "we're unifying `List<number>` against `List<string>`"
  rather than "we're unifying this specific pointer against that specific pointer."

**Recommendation**: Use `TypeAlias` pointer identity for `TypeRefType` pairs (since
these are the types that can be recursive), and pointer identity as a fallback for
other types.

### What to do when a cycle is detected?

- **In unification**: Assume success (return `nil` errors). This is the co-inductive
  assumption — if the non-cyclic parts unify, the cyclic parts will too.
- **In expansion**: Return the `TypeRefType` unexpanded. This prevents infinite
  expansion and leaves the type reference in place for the consumer to handle.

### Where to store the visited set?

Two options:

- **Thread through function parameters**: Add a `seen` parameter to `unifyWithDepth`
  and `expandTypeWithConfig`. This is explicit but requires changing many call
  signatures.
- **Store on the Checker**: Add a field like `Checker.unifyingSeen` that is set at
  the start of a top-level `Unify` call and cleared at the end. This avoids
  changing signatures but introduces shared mutable state.

**Recommendation**: Thread through parameters. It's more verbose but makes the
lifetime explicit and avoids bugs from forgetting to clear the state. The parameter
can be hidden from external callers by keeping the public `Unify` and `ExpandType`
signatures unchanged.

### Interaction with generics and HM inference

Co-inductive unification is compatible with Escalier's HM-based inference. The
critical property is **monomorphic recursion during body checking**: when a
function's body is being checked, the function's own type is a fixed monomorphic
type (fresh TypeVars, not yet generalized). Recursive calls within the body use
that same monomorphic type. This means type arguments cannot grow across recursive
calls (no `List<List<List<...>>>` problem), so the seen-set keys are stable within
a single unification context.

This holds regardless of where generalization occurs — top-level or inside blocks.
The sequence is always:

1. Create monomorphic signature (fresh TypeVars for params/return)
2. Check body — all unification happens here with fixed types
3. Generalize (unresolved TypeVars → TypeParams)
4. Each call site: instantiate with fresh TypeVars → fresh `Unify` → fresh seen set

Co-inductive unification lives in step 2. Generalization (step 3) and instantiation
(step 4) don't affect it — by the time a generalized type is used, each use gets
its own instantiation and its own seen set.

**Mutually recursive functions** are also safe. Escalier uses Tarjan's SCC algorithm
to group mutually recursive declarations and processes them together: all signatures
are created as monomorphic types, all bodies are checked, then all are generalized.
Calls between functions in the same SCC use monomorphic types during body checking.

**One subtlety remains:** TypeVar binding during unification can change the output
of `fmt.Sprint(typeArgs)` used in `expandSeenKey`. If TypeVar#42 starts unbound and
gets bound to `number` while unifying one field, then a later encounter with the
same `TypeRefType` would produce a different key. This is not caused by
generalization — it's inherent to how unification mutates TypeVars during a single
pass. In practice this is unlikely to cause problems because:

- The recursive `TypeRefType` in a type alias (e.g. `List<T>`) uses the *same*
  TypeVar pointer as the outer reference, so `fmt.Sprint` produces the same string
  whether or not the TypeVar is bound (both occurrences are the same object).
- The risk would arise only if a TypeRefType's type args contained a *different*
  TypeVar that happened to get bound between encounters — an unusual structural
  pattern. If this proves to be a problem during testing, the key can be changed
  to use TypeVar pointer identity instead of resolved values.

## Plan

### Step 1: Define the seen-pairs type

```go
// unifyPairKey identifies a pair of types being unified.
// For TypeRefType, we use the TypeAlias pointer + stringified type args
// to capture meaningful identity across allocations.
type unifyPairKey struct {
    t1 unsafe.Pointer
    t2 unsafe.Pointer
}

type unifySeen map[unifyPairKey]bool
```

### Step 2: Thread `unifySeen` through unification

Change the internal signatures:

```go
func (c *Checker) Unify(ctx Context, t1, t2 type_system.Type) []Error {
    return c.unifyWithDepth(ctx, t1, t2, 0, make(unifySeen))
}

func (c *Checker) unifyWithDepth(ctx Context, t1, t2 type_system.Type, depth int, seen unifySeen) []Error {
    // ...
}

func (c *Checker) unifyPruned(ctx Context, t1, t2 type_system.Type, depth int, seen unifySeen) []Error {
    // ...
}
```

All internal calls that currently call `c.Unify(ctx, ...)` within `unifyPruned`,
`bind`, `unifyFuncTypes`, `unifyObjectTypes`, etc. should be changed to call
`c.unifyWithDepth(ctx, ..., depth, seen)` to propagate the seen set. Calls to
`c.Unify` from outside the unification subsystem (e.g. from `infer_expr.go` or
`expand_type.go`) continue to use the public `Unify` which creates a fresh seen set.

This is the most labor-intensive step — there are many internal `c.Unify` calls
throughout `unify.go` that need to be updated. A systematic approach:

1. Rename `unifyPruned` to accept `seen` parameter.
2. Find all `c.Unify` calls within `unify.go` and change them to
   `c.unifyWithDepth(..., depth, seen)`.
3. Update helper functions (`bind`, `unifyFuncTypes`, `unifyObjElem`, etc.) to
   accept and propagate `seen`.

### Step 3: Add cycle detection at the TypeRefType expansion site

In the TypeRefType expansion cases added by Plan A:

```go
// | TypeRefType, _ -> expand t1
if ref1, ok := t1.(*type_system.TypeRefType); ok {
    key := unifyPairKey{
        t1: typeIdentityPointer(ref1),
        t2: typeIdentityPointer(t2),
    }
    if seen[key] {
        return nil // co-inductive assumption: assume success
    }
    seen[key] = true

    expandedT1, expandErrors := c.expandTypeRef(ctx, ref1)
    if len(expandErrors) > 0 {
        return expandErrors
    }
    return c.unifyWithDepth(ctx, expandedT1, t2, depth+1, seen)
}
```

The `typeIdentityPointer` helper extracts a stable pointer for any type:

```go
func typeIdentityPointer(t type_system.Type) unsafe.Pointer {
    // For TypeRefType, use the TypeAlias pointer if available
    if ref, ok := t.(*type_system.TypeRefType); ok && ref.TypeAlias != nil {
        return unsafe.Pointer(ref.TypeAlias)
    }
    // For other types, use the type's own pointer
    // This requires that the type was not reconstructed between calls
    return pointerOf(t)
}
```

**Note on Prune interaction:** `Unify` calls `Prune` on types before entering
`unifyPruned`, which follows `TypeVar` bindings and may return a different pointer.
If `Prune` returns a `TypeRefType`, the pointer used here will be the pruned
pointer. This is correct — we want to detect cycles on the *resolved* type identity,
not the original `TypeVar` wrapper. However, if `Prune` ever reconstructs a
`TypeRefType` (new allocation for a structurally identical type), the pointer-based
key would miss the cycle. Currently `Prune` returns existing pointers for
`TypeRefType`, so this is safe, but it's a coupling worth noting.

### Step 4: Add cycle detection to ExpandType

Define an analogous seen set for expansion:

```go
// expandSeenKey identifies a specific instantiation of a type alias.
// Using only the TypeAlias pointer would be wrong: List<number> and List<string>
// share the same TypeAlias, but they are different instantiations that should
// both be expandable. We include the stringified type args to distinguish them.
//
// We use fmt.Sprint for structural comparison of type args. This is necessary
// because type args can be structural types (e.g. ObjectType) where two
// identical {x: number, y: number} literals may have different pointers.
// Pointer-based identity would miss cycles in cases like List<{x: number}>.
//
// The one edge case is TypeVar binding: if a TypeVar in the type args gets
// bound between expansion calls, fmt.Sprint could produce a different string.
// In practice this is unlikely to cause problems because the recursive
// TypeRefType in a type alias (e.g. the `List<T>` in `tail: List<T> | null`)
// uses the same TypeVar pointer as the outer reference, so fmt.Sprint produces
// the same output whether or not the TypeVar is bound.
type expandSeenKey struct {
    alias    unsafe.Pointer // TypeAlias pointer
    typeArgs string         // fmt.Sprint(typeArgs) — structural comparison
}

type expandSeen map[expandSeenKey]bool
```

Thread it through `expandTypeWithConfig` and the `TypeExpansionVisitor`:

```go
func (c *Checker) ExpandType(ctx Context, t type_system.Type, expandTypeRefsCount int) (type_system.Type, []Error) {
    return c.expandTypeWithConfig(ctx, t, expandTypeRefsCount, 0, make(expandSeen))
}

type TypeExpansionVisitor struct {
    // ... existing fields ...
    seen expandSeen
}
```

In the `TypeRefType` case of `ExitType`:

```go
case *type_system.TypeRefType:
    key := expandSeenKey{
        alias:    unsafe.Pointer(typeAlias),
        typeArgs: fmt.Sprint(ref.TypeArgs),
    }
    if v.seen[key] {
        // Cycle detected — return the TypeRefType unexpanded
        return nil
    }
    v.seen[key] = true
    defer delete(v.seen, key) // clean up after leaving this expansion
    // ... proceed with expansion ...
```

Note the `defer delete` — unlike unification, expansion uses a stack-like seen set.
A type alias should only be marked as "in progress" while it's actively being
expanded. Once expansion of that alias is complete, it should be removed so that
the same alias can be expanded again in a different context (e.g. `List<number>`
and `List<string>` both reference `List` but should both be expandable). Including
the type args in the key is critical here: without it, expanding `List<number>`
would mark `List` as seen, and a subsequent encounter with `List<string>` during
the same expansion would incorrectly be treated as a cycle.

### Step 5: Remove `maxUnifyDepth`

Once the visited-set is in place and the test suite passes, remove:

- The `maxUnifyDepth` constant (line 94)
- The depth check at lines 119-121
- The `depth` parameter from `unifyWithDepth` (or keep it for debugging but remove
  the hard limit)

The `depth` parameter might still be useful for debugging (e.g. logging when depth
exceeds some threshold), but it should no longer be a termination mechanism.

### Step 6: Evaluate removing ad-hoc counters

With cycle detection in place, evaluate whether these can be simplified or removed:

- **`expandTypeRefsCount`**: May still be useful for controlling how eagerly types
  are expanded (e.g. `ExpandType(ctx, t, 1)` for "expand one level"). But it's no
  longer needed as a safety mechanism — the seen set handles cycles. Consider
  keeping it as an optimization hint.
- **`skipTypeRefsCount`**: The seen set makes this less critical since expanding
  into a `FuncType` or `ObjectType` and hitting a cycle will now terminate. But
  skipping expansion inside structural types is still a useful optimization to avoid
  unnecessary work. Consider keeping it.
- **`insideKeyOfTarget`**: The seen set in `ExpandType` would catch `keyof` cycles
  if the expansion of `keyof T` triggers re-expansion of the same alias. Test
  whether removing this counter causes any test failures. If not, remove it.

## Testing strategy

1. **All existing tests must pass** — This is a refactor of the termination
   mechanism, not the unification logic.
2. **Recursive type alias tests** — After Plan B, recursive type aliases should no
   longer cause stack overflows. Add tests:
   ```
   type List<T> = { head: T, tail: List<T> | null }
   val list: List<number> = { head: 1, tail: { head: 2, tail: null } }
   ```
   ```
   type Json = string | number | boolean | null | Json[] | { [key: string]: Json }
   val j: Json = { name: "test", values: [1, 2, 3] }
   ```
   ```
   type Tree<T> = { value: T, children: Tree<T>[] }
   ```
3. **Mutual recursion test**:
   ```
   type Expr = Lit | Add
   type Lit = { tag: "lit", value: number }
   type Add = { tag: "add", left: Expr, right: Expr }
   ```
4. **Cycle detection test** — Verify that unifying a recursive type with itself
   succeeds quickly (not after 50 depth steps):
   ```
   val a: List<number> = ...
   val b: List<number> = a  // should succeed immediately via same-alias check
   ```
5. **Cross-alias cycle test** — Two different aliases that reference each other:
   ```
   type A = { x: B }
   type B = { y: A }
   val a: A = { x: { y: { x: { y: ... } } } }
   ```

## Risks

- **Pointer stability**: The seen set uses pointers. If `Prune` or expansion
  replaces a type with a structurally identical but different pointer, the seen set
  won't recognize it. Mitigation: for `TypeRefType`, use the `TypeAlias` pointer
  (which is stable) rather than the `TypeRefType` pointer itself.
- **False positives from co-inductive assumption**: If unification incorrectly
  assumes success for a pair that would actually fail, this could accept invalid
  programs. Mitigation: the co-inductive assumption is only applied when a cycle is
  detected, meaning the types are structurally recursive. For recursive types, the
  assumption is sound — if the non-cyclic parts unify, the whole thing unifies.
- **Performance**: The seen set adds a map lookup on every TypeRefType expansion.
  This should be negligible compared to the cost of the expansion itself. For
  unification, the map is only consulted at the TypeRefType cases, not on every
  `unifyPruned` call.
- **Propagation completeness**: Missing a single internal `c.Unify` call that
  should be `c.unifyWithDepth(..., seen)` would create a new fresh seen set at that
  point, potentially missing a cycle. Mitigation: grep for all `c.Unify` calls in
  `unify.go` after the change and verify each one is intentional.
