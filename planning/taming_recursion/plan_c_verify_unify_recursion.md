# Plan C: Verify and harden unification recursion (issue #1)

**Prerequisites:** Plans A and B are implemented.

## Goal

Confirm that the three pathological scenarios described in issue #1 are fully
resolved after Plans A and B, address any remaining edge cases where unification
can still recurse excessively, and clean up residual workaround code.

## Background

Issue #1 described unbounded recursion in `unifyPruned`'s retry loop for three
scenarios:

1. **TypeRefType with TypeAlias set** (e.g. `HTMLAttributeAnchorTarget`, `Array<any>`)
2. **TupleType with rest spreads** (e.g. `[...Array<any>, ...Array<any>]`)
3. **Large ObjectType instances** (e.g. React SVG attributes with 200+ properties)

Plan A replaced the retry loop with explicit `TypeRefType` expansion, eliminating
the pointer-inequality-driven retries that caused scenarios 1 and 3. Plan B added
visited-set cycle detection, removing the `maxUnifyDepth` safety net.

However, several areas need verification:

- The `ExpandType` calls that remain in `unifyPruned` (KeyOfType at lines 345-346,
  Union+ObjectType at line 1055) were not part of the retry loop but still call
  `ExpandType` with a fresh context. After Plan B removes `maxUnifyDepth`, these
  calls rely entirely on the visited set in `ExpandType` for termination.
- The Tuple+Array and Array+Tuple cases (lines 376-441) call `c.Unify` recursively
  on rest spread types. If a rest spread contains an `Array<T>` where `T` itself
  references the tuple, this could recurse. After Plan B, the `unifySeen` set
  should catch this — but only if `c.Unify` was correctly changed to
  `c.unifyWithDepth(..., seen)` at those call sites.
- The `c.Unify` calls inside `unifyTuples`, `unifyFuncTypes`, and `bind` must all
  propagate the seen set. A missed call site would create a fresh seen set that
  can't detect cycles.

## Plan

### Step 1: Audit `c.Unify` propagation in Tuple/Array paths

Verify that all `c.Unify` calls in the Tuple+Array interaction paths were updated
by Plan B to propagate the seen set:

- `TupleType, ArrayType` case (lines 376-397): calls `c.Unify` for each tuple
  element and for rest spread types
- `ArrayType, TupleType` case (lines 399-421): mirror of above
- `ArrayType, ArrayType` case (lines 422-435): calls `c.Unify` on element types
- `RestSpreadType, ArrayType` case (lines 436-441): calls `c.Unify` on rest type
- `unifyTuples` helper: calls `c.Unify` on each pair of tuple elements

Each of these should be `c.unifyWithDepth(ctx, ..., depth+1, seen)` after Plan B.
If any were missed, fix them.

### Step 2: Audit `ExpandType` calls that remain in unify.go

After Plan A removed the retry loop, two `ExpandType` call sites remain in
`unifyPruned`:

1. **KeyOfType expansion** (lines 345-346): Expands both `keyof` types to get their
   concrete keys, then unifies the results. This calls
   `c.unifyWithDepth(ctx, expandedKeys1, expandedKeys2, depth+1)`.

   **Risk:** If the expanded keys contain TypeRefTypes, they'll be expanded again on
   re-entry to `unifyPruned` via Plan A's explicit cases. The visited set from
   Plan B handles cycles. **No change needed**, but verify with a test case like:
   ```
   type A = { x: number, y: string }
   type B = { x: number, y: string }
   // keyof A should unify with keyof B
   ```

2. **Union+ObjectType expansion** (line 1055): Expands each union member one level
   to check if it's an `ObjectType`. This is a one-shot expansion (not recursive)
   and doesn't call `unifyWithDepth` directly on the result.

   **Risk:** Low. The expansion is bounded by the number of union members, and each
   member is expanded only once. **No change needed.**

### Step 3: Validate the three original scenarios

Write targeted tests that reproduce the three scenarios from issue #1 and verify
they terminate promptly (not after hitting a depth limit):

#### Scenario 1: TypeRefType with TypeAlias set

```
// HTMLAttributeAnchorTarget-like scenario: type alias used in annotation
type Target = "_self" | "_blank" | "_parent" | "_top"
val t: Target = "_blank"
```

```
// Array<any> scenario: unifying Array types with different references
val a: Array<any> = [1, "hello", true]
val b: Array<any> = a
```

#### Scenario 2: TupleType with rest spreads

```
// Rest spreads that expand to Array interface
val concat = fn <T>(a: Array<T>, b: Array<T>): [...Array<T>, ...Array<T>] => {
    // ...
}
val result: Array<number> = concat([1, 2], [3, 4])
```

```
// Nested rest spreads
val nested: [...Array<number>, ...Array<string>] = [1, 2, "a", "b"]
```

#### Scenario 3: Large ObjectType instances

This requires testing with actual React-scale type definitions. If the project has
React type tests:

- Run them and verify no test hits a depth limit or times out
- If no React tests exist, create a synthetic test with a large object type
  (20+ properties with TypeRefType values) and verify unification terminates

### Step 4: Add regression tests

Create a dedicated test file or test section that exercises unification recursion
edge cases. These serve as regression tests to prevent future changes from
reintroducing unbounded recursion:

```go
func TestUnifyRecursionTerminates(t *testing.T) {
    // Test cases:
    // 1. TypeRefType vs TypeRefType (same alias, no args)
    // 2. TypeRefType vs TypeRefType (same alias, with args)
    // 3. TypeRefType vs TypeRefType (different alias, same structure)
    // 4. TypeRefType vs concrete ObjectType
    // 5. TupleType with rest spread of Array<T>
    // 6. Large ObjectType with nested TypeRefTypes
    // 7. Union of TypeRefTypes vs ObjectType
    // 8. KeyOfType of TypeRefType
}
```

Each test should verify both correctness (unification succeeds or fails as expected)
and termination (no timeout or stack overflow).

### Step 5: Remove residual workaround code and comments

After verifying all tests pass:

1. **Remove the `maxUnifyDepth` constant and depth check** (constant at line 94,
   check at lines 119-121) — Plan B already removes these, but any surrounding
   explanatory comments may linger. Remove them since they describe a problem that
   no longer exists.

2. **Remove the `depth` parameter** from `unifyWithDepth` if Plan B kept it for
   debugging. If it's being kept, rename it to make clear it's diagnostic-only
   (e.g. add a comment: "depth is for debugging/logging only, not a termination
   mechanism").

3. **Update the TODO at expand_type.go:343-345** — After Plan B adds visited-set
   cycle detection to `ExpandType`, the TODO about marking type aliases as recursive
   can be updated to reflect that cycle detection is now handled dynamically. The
   `Recursive` flag on `TypeAlias` is no longer a prerequisite for correctness,
   though it could still be useful as an optimization (skip visited-set tracking
   for non-recursive aliases).

4. **Clean up comments** in `unifyPruned` that reference the old retry loop or
   explain why certain cases fall through to it.

### Step 6: Performance validation

Measure unification performance on large type definitions to ensure the explicit
TypeRefType expansion (Plan A) and visited-set lookups (Plan B) don't introduce
regressions:

1. **Benchmark the test suite** — Compare test suite run time before and after
   Plans A+B+C. The removal of spurious retries should make unification faster for
   complex types, but the visited-set overhead could slow down simple cases.

2. **Profile with large .d.ts files** — If the project loads React or DOM type
   definitions, profile the checker to verify that unification of large object
   types (SVG attributes, HTML element props) completes in reasonable time.

3. **Check visited-set memory** — For deeply nested types, the visited set grows
   with the number of unique pairs encountered. Verify this stays reasonable
   (should be proportional to the number of distinct TypeAlias pointers, not to
   the number of unification calls).

## Testing strategy

All tests from Plans A and B must continue to pass. The new tests from Steps 3-4
above are additive.

## Risks

- **Missed propagation sites**: If Plan B missed a `c.Unify` call in the
  Tuple/Array paths that should propagate `seen`, cycles involving tuples with
  recursive rest spreads could still diverge. Step 1 of this plan specifically
  audits these sites.
- **ExpandType calls without visited set**: The `ExpandType` calls at lines 345
  and 1055 create their own expansion context. If the types being expanded contain
  recursive aliases, the expansion-side visited set (from Plan B) should catch
  them, but this depends on `ExpandType` correctly threading its own seen set
  through recursive calls. Step 2 verifies this.
- **Performance regression**: Unlikely but possible if the visited-set map becomes
  a bottleneck for very hot unification paths. Step 6 checks for this.
- **Thread safety**: The `unifySeen` and `expandSeen` maps are created per top-level
  call and threaded through parameters, so they are not shared across goroutines.
  If the checker ever runs concurrently (e.g. checking multiple files in parallel),
  this design is safe because each `Unify`/`ExpandType` entry point creates its own
  seen set. However, if internal helper functions are ever changed to spawn
  goroutines that share a seen map, the maps would need synchronization.
