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
2. **TupleType with rest spreads** — the original issue referenced multi-spread
   tuples like `[...Array<any>, ...Array<any>]`, but Escalier (like TypeScript)
   does not allow multiple spreads in a tuple type. Single-spread tuples
   (e.g. `[number, ...Array<number>]`) may still be relevant if they trigger
   excessive recursion during unification with Array types.
3. **Large ObjectType instances** (e.g. React SVG attributes with 200+ properties)

Plan A replaced the retry loop with an iterative expansion loop in `unifyPruned`
that calls `unifyMatched` for case-matching and `tryExpandTypeRef` for TypeRef
expansion, eliminating the pointer-inequality-driven retries that caused
scenarios 1 and 3. Plan B added visited-set cycle detection, removing the
`maxUnifyDepth` safety net and the `maxExpansionRetries` loop bound.

However, several areas need verification:

- The `ExpandType` calls that remain in `unifyMatched` — the KeyOfType expansion
  case (which expands both `keyof` types to get their concrete keys) and the
  Union+ObjectType expansion case (which expands each union member to check if
  it's an `ObjectType`) — were not part of the retry loop but still call
  `ExpandType` with a fresh context. After Plan B removes the hard limits, these
  calls rely entirely on the `expandSeen` visited set in `ExpandType` for
  termination.
- The Tuple+Array and Array+Tuple interaction paths in `unifyMatched` (including
  the `TupleType, ArrayType` case, the `ArrayType, TupleType` case, the
  `ArrayType, ArrayType` case, and the `RestSpreadType, ArrayType` case) all
  call `c.Unify` recursively — notably on rest spread types (e.g.
  `c.Unify(ctx, rest.Type, array2)`). If a rest spread contains an `Array<T>`
  where `T` itself references the tuple, this could recurse. After Plan B
  removes the hard limits, the `unifySeen` set should catch this — but only if
  each of these `c.Unify` calls was correctly changed to
  `c.unifyWithDepth(..., seen)` so that `unifySeen` is propagated. A missed
  call site would create a fresh `unifySeen` set via the public `c.Unify`
  entry point, which cannot detect cycles that started in the parent call.
- The `c.Unify` calls inside `unifyTuples`, `unifyFuncTypes`, and `bind` must
  similarly propagate `unifySeen` via `c.unifyWithDepth(..., seen)`. A missed
  call site has the same risk: a fresh seen set that can't detect cycles.

## Plan

### Step 1: Audit `c.Unify` propagation in Tuple/Array paths

Verify that all `c.Unify` calls in the Tuple+Array interaction paths were updated
by Plan B to propagate the seen set. After Plan A, these cases live in
`unifyMatched` (the case-matching function called by `unifyPruned`'s loop):

- `TupleType, ArrayType` case (lines 376-397): calls `c.Unify` for each tuple
  element and for rest spread types
- `ArrayType, TupleType` case (lines 399-421): mirror of above
- `ArrayType, ArrayType` case (lines 422-435): calls `c.Unify` on element types
- `RestSpreadType, ArrayType` case (lines 436-441): calls `c.Unify` on rest type
- `unifyTuples` helper: calls `c.Unify` on each pair of tuple elements

Each of these should be `c.unifyWithDepth(ctx, ..., depth, seen)` after Plan B —
propagating the seen set but keeping the same depth. After Plan A, TypeRefType
expansion is handled iteratively in `unifyPruned`'s loop and does not increment
`depth`. The `depth` parameter only reflects structural recursion depth from
subcomponent unification (these forwarding calls), which should pass `depth`
unchanged. If any `c.Unify` calls were missed during Plan B, fix them.

### Step 2: Audit `ExpandType` calls that remain in unify.go

After Plan A, two `ExpandType` call sites remain in `unifyMatched` (the
case-matching function extracted from the original `unifyPruned`):

1. **KeyOfType expansion** (lines 345-346): Expands both `keyof` types to get their
   concrete keys, then unifies the results. Per Step 1's principle, this is not a
   TypeRef expansion, so it should forward the same depth:
   `c.unifyWithDepth(ctx, expandedKeys1, expandedKeys2, depth, seen)`.

   **Risk:** If the expanded keys contain TypeRefTypes, they'll be expanded again on
   re-entry to `unifyPruned` via Plan A's explicit cases (which do increment depth).
   The visited set from Plan B handles cycles. Verify with a test case like:
   ```escalier
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

```escalier
// HTMLAttributeAnchorTarget-like scenario: type alias used in annotation
type Target = "_self" | "_blank" | "_parent" | "_top"
val t: Target = "_blank"
```

```escalier
// Array<any> scenario: unifying Array types with different references
val a: Array<any> = [1, "hello", true]
val b: Array<any> = a
```

#### Scenario 2: TupleType with rest spreads

> **Note:** Escalier (like TypeScript) does not allow tuple types to contain
> multiple rest/spread elements. The examples below from the original issue
> inventory are invalid syntax. This scenario may not be reproducible as
> described. If single-spread tuples can still trigger excessive recursion
> (e.g. `[number, ...Array<number>]` vs `Array<number>`), add a test for that
> instead.

```escalier
// Single rest spread — valid
val items: [number, ...Array<number>] = [1, 2, 3]
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

1. **Decide whether to keep the `depth` parameter and `maxExpansionRetries`.**
   Plan B removed the `maxUnifyDepth` hard limit and made `maxExpansionRetries`
   (from Plan A) redundant. If the validation in Steps 1-4 shows that the visited
   set reliably handles all recursion cases:
   - Remove `maxExpansionRetries` from `unifyPruned`'s loop (or replace with a
     generous safety limit like 100) since the visited set prevents cycles.
   - Remove the `depth` parameter from `unifyWithDepth` entirely to simplify the
     interface, or keep it as diagnostic-only with a comment making clear it is
     not a termination mechanism. If depth is removed, update the guidance in
     Step 1 accordingly (the "should pass `depth` unchanged" notes become moot).

2. **Update the TODO at expand_type.go:343-345** — After Plan B adds visited-set
   cycle detection to `ExpandType`, the TODO about marking type aliases as recursive
   can be updated to reflect that cycle detection is now handled dynamically. The
   `Recursive` flag on `TypeAlias` is no longer a prerequisite for correctness,
   though it could still be useful as an optimization (skip visited-set tracking
   for non-recursive aliases).

3. **Clean up comments** in `unifyPruned` and `unifyMatched` that reference the
   old retry loop or explain why certain cases fall through to it.

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
