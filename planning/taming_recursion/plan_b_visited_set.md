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

**TypeVar binding stability:** The `typeArgKey` helper (see Step 4) addresses a
subtlety where TypeVar binding during unification could change the key for a type
arg. It uses `TypeVar.ID` for TypeVars (stable regardless of binding state) and
`fmt.Sprint` for concrete types (structural comparison that handles identical types
at different pointers). This ensures seen-set keys are stable throughout a single
unification or expansion pass.

## Plan

### Step 1: Define the seen-pairs type

```go
// unifyPairKey identifies a pair of types being unified.
// For TypeRefType, we use the TypeAlias pointer + typeArgKey(typeArgs)
// to capture meaningful identity across allocations. Without type args,
// List<number> and List<string> would produce the same key (both share
// the same TypeAlias pointer), causing false cycle detection.
type unifyPairKey struct {
    t1     unsafe.Pointer
    t1Args string // typeArgKey(typeArgs) for TypeRefType, empty otherwise
    t2     unsafe.Pointer
    t2Args string // typeArgKey(typeArgs) for TypeRefType, empty otherwise
}

type unifySeen map[unifyPairKey]bool
```

### Step 2: Thread `unifySeen` through unification

**Depth increment rule:** The `depth` parameter should only increment at the
explicit TypeRefType expansion sites added by Plan A (where `expandTypeRef` is
called and the result is re-entered into unification). Forwarding calls that
unify subcomponents (tuple elements, array element types, function parameters,
object properties, rest spread types) are not expansions and should pass `depth`
unchanged. This keeps depth proportional to the alias chain length rather than
the structural size of the types.

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

In the TypeRefType expansion cases added by Plan A. Note: `expandTypeRef`
(defined at `expand_type.go:1617`) resolves a `TypeRefType`'s alias and
substitutes type parameters without recursively expanding nested references.
It does not call `ExpandType`, so the `expandSeen` set from Step 4 is not
consulted here — cycles are caught by the `unifySeen` check on re-entry to
`unifyWithDepth`.

```go
// | TypeRefType, _ -> expand t1
if ref1, ok := t1.(*type_system.TypeRefType); ok {
    key := makeUnifyPairKey(ref1, t2)
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

The `makeUnifyPairKey` helper builds a key that includes both the stable pointer
and type args for `TypeRefType`:

```go
func makeUnifyPairKey(t1, t2 type_system.Type) unifyPairKey {
    key := unifyPairKey{}
    if ref, ok := t1.(*type_system.TypeRefType); ok {
        if ref.TypeAlias != nil {
            key.t1 = unsafe.Pointer(ref.TypeAlias)
        } else {
            key.t1 = pointerOf(ref)
        }
        key.t1Args = typeArgKey(ref.TypeArgs)
    } else {
        key.t1 = interfaceDataPointer(t1)
    }
    if ref, ok := t2.(*type_system.TypeRefType); ok {
        if ref.TypeAlias != nil {
            key.t2 = unsafe.Pointer(ref.TypeAlias)
        } else {
            key.t2 = interfaceDataPointer(ref)
        }
        key.t2Args = typeArgKey(ref.TypeArgs)
    } else {
        key.t2 = interfaceDataPointer(t2)
    }
    return key
}

// interfaceDataPointer extracts the data pointer from a Go interface value.
// In Go, an interface is a (type, data) pair; we want the data pointer so that
// two interface values holding the same concrete pointer produce the same key.
func interfaceDataPointer(t type_system.Type) unsafe.Pointer {
    // (*[2]unsafe.Pointer) reinterprets the interface as its underlying pair.
    return (*[2]unsafe.Pointer)(unsafe.Pointer(&t))[1]
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
// both be expandable. We include type arg identity to distinguish them.
type expandSeenKey struct {
    alias    unsafe.Pointer // TypeAlias pointer
    typeArgs string         // typeArgKey(typeArgs) — see below
}

// expandSeen tracks type alias expansions in progress and caches completed results.
// A nil value means the expansion is in progress (re-encounter = cycle).
// A non-nil value is the cached expansion result (re-encounter = reuse).
type expandSeen map[expandSeenKey]type_system.Type

// typeArgKey produces a stable, deterministic string key for type arguments.
// For TypeVarType, it uses the TypeVar's unique ID rather than its printed
// representation. This ensures the key is stable regardless of whether the
// TypeVar has been bound: TypeVar#42 always produces "$42" whether it's
// unbound or bound to `number`. For all other types, fmt.Sprint provides
// structural comparison, which correctly handles cases like two identical
// ObjectType literals at different pointers (e.g. List<{x: number}>).
//
// KNOWN LIMITATION: This only handles TypeVarType at the top level of a type
// arg. If a type arg is a structural type containing an embedded TypeVar
// (e.g. {x: TypeVar#42, y: string}), the fmt.Sprint fallback will print the
// TypeVar's bound value, which could change mid-pass during unification. This
// would require a type arg like List<{x: T}> where T gets bound between two
// encounters of the same TypeRefType — unusual but theoretically possible.
// During testing, try to trigger this with a test case like:
//
//   type Wrapper<T> = { inner: Wrapper<{x: T}> | null }
//   val w: Wrapper<number> = { inner: { inner: null } }
//
// If the key proves unstable, make typeArgKey recursive: walk the full type
// structure using the visitor pattern, emitting TypeVar.ID for any TypeVarType
// at any depth and structural representation for everything else.
func typeArgKey(args []type_system.Type) string {
    parts := make([]string, len(args))
    for i, arg := range args {
        if tv, ok := arg.(*type_system.TypeVarType); ok {
            parts[i] = fmt.Sprintf("$%d", tv.ID)
        } else {
            parts[i] = fmt.Sprint(arg)
        }
    }
    return strings.Join(parts, ",")
}
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

**Important:** `ExpandType` calls itself recursively in several places
(intersection distribution at line 186, keyof distribution at lines 288-301,
mapped element expansion at line 1484). These recursive calls currently create
a fresh visitor with its own counter values. After this change, the `expandSeen`
map must be passed through to these recursive calls so that cycles spanning
multiple levels of expansion are detected. Concretely, add an `expandSeen`
parameter to `expandTypeWithConfig` and pass `v.seen` from the visitor at each
internal `ExpandType` call site.

In the `TypeRefType` case of `ExitType`:

```go
case *type_system.TypeRefType:
    key := expandSeenKey{
        alias:    unsafe.Pointer(typeAlias),
        typeArgs: typeArgKey(ref.TypeArgs),
    }
    if cached, exists := v.seen[key]; exists {
        if cached == nil {
            // In progress — this is a cycle. Return unexpanded.
            return nil
        }
        // Completed — reuse the cached expansion.
        return cached
    }
    v.seen[key] = nil // mark as in progress
    // ... proceed with expansion ...
    v.seen[key] = expandedType // cache the result
```

The two-phase approach (nil = in progress, non-nil = completed) distinguishes
cycles from reuse. Without caching, a type like `{a: List<number>, b: List<number>}`
would either expand `List<number>` twice (redundant work) or incorrectly treat the
second occurrence as a cycle. With caching, the first occurrence is expanded and
stored, and the second occurrence reuses the cached result.

### Step 5: Remove `maxUnifyDepth` hard limit

Once the visited-set is in place and the test suite passes, remove the hard
depth limit:

- The `maxUnifyDepth` constant (line 94)
- The depth check at lines 119-121

Keep the `depth` parameter in `unifyWithDepth` for now — it is still useful for
debugging (e.g. logging when depth exceeds some threshold) and for Plan C's
verification work. Plan C will decide whether to keep it permanently or remove it
based on whether it provides diagnostic value after all changes are validated.

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
   ```escalier
   type List<T> = { head: T, tail: List<T> | null }
   val list: List<number> = { head: 1, tail: { head: 2, tail: null } }
   ```
   ```escalier
   type Json = string | number | boolean | null | Json[] | { [key: string]: Json }
   val j: Json = { name: "test", values: [1, 2, 3] }
   ```
   ```escalier
   type Tree<T> = { value: T, children: Tree<T>[] }
   val t: Tree<number> = { value: 1, children: [{ value: 2, children: [] }] }
   ```
3. **Mutual recursion test**:
   ```escalier
   type Expr = Lit | Add
   type Lit = { tag: "lit", value: number }
   type Add = { tag: "add", left: Expr, right: Expr }
   ```
4. **Cycle detection test** — Verify that unifying a recursive type with itself
   succeeds quickly (not after 50 depth steps):
   ```escalier
   val a: List<number> = ...
   val b: List<number> = a  // should succeed immediately via same-alias check
   ```
5. **Structural type arg with embedded TypeVar** — Test whether `typeArgKey`
   produces stable keys when a type arg is a structural type containing a TypeVar.
   If this test causes instability (infinite expansion or missed cycle), make
   `typeArgKey` recursive:
   ```escalier
   type Wrapper<T> = { inner: Wrapper<{x: T}> | null }
   val w: Wrapper<number> = { inner: { inner: null } }
   ```
6. **Cross-alias cycle test** — Two different aliases that reference each other.
   Use `declare val` to obtain a value of the recursive type without needing to
   construct a finite literal (no finite object satisfies `A` or `B`):
   ```escalier
   type A = { x: B }
   type B = { y: A }
   declare val a1: A
   val a2: A = a1  // should succeed: same type
   val b: B = a1.x // should succeed: a1.x is B
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
