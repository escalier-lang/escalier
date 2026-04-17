# Plan C: Verify and harden unification recursion

**Prerequisites:** Plans A and B are implemented. Plan A is done (PR #451).
Plan B is done.

## Goal

Confirm that the three pathological scenarios described in research.md issue #1 are
fully resolved after Plans A and B, address any remaining edge cases where
unification can still recurse excessively, and clean up residual workaround code.

## Background

Research.md issue #1 described unbounded recursion in the old `unifyPruned` retry
loop for three scenarios:

1. **TypeRefType with TypeAlias set** (e.g. `HTMLAttributeAnchorTarget`, `Array<any>`)
2. **TupleType with rest spreads** — the original issue referenced multi-spread
   tuples like `[...Array<any>, ...Array<any>]`, but Escalier (like TypeScript)
   does not allow multiple spreads in a tuple type. Single-spread tuples
   (e.g. `[number, ...Array<number>]`) may still be relevant if they trigger
   excessive recursion during unification with Array types.
3. **Large ObjectType instances** (e.g. React SVG attributes with 200+ properties)

Plan A (PR #451) replaced the retry loop with a `noMatchError` sentinel in
`unifyMatched` and a three-tier expansion loop in `unifyPruned`:
- Tier 1: `canExpandTypeRef` → `ExpandType(t, 1)` → `unifyWithDepth(depth+1)`
- Tier 2: `ExpandType(t, 0)` + Prune → `continue` for non-TypeRef types
- Tier 3: Last-resort `ExpandType(t, 1)` for nominal/refused TypeRefTypes

The `noMatchError` sentinel ensures expansion is only attempted when no case in
`unifyMatched` handled the types — authoritative errors from `bind`, same-alias
comparison, etc. are returned immediately. `canExpandTypeRef` blocks `IsTypeParam`
aliases, nominal types, and transitive self-referential cycles (A→B→A).

Plan B added `unifySeen` and `expandSeen` visited-set cycle detection, removed
`maxUnifyDepth`, and increased `maxExpansionRetries` from 10 to 100 as a safety
net. All ~70 internal `c.Unify` calls in `unify.go` were replaced with
`c.unifyWithDepth(..., depth, seen)`. All helper functions (`bind`,
`unifyFuncTypes`, `unifyTuples`, `unifyExtractor`, `unifyClosedWithRests`,
`unifyPatternWithUnion`, `handleArrayConstraintBinding`, and all tuple unification
variants) were updated to accept and propagate `depth int, seen unifySeen`.

However, several areas need verification:

- The `ExpandType` calls that remain in `unifyMatched` — the KeyOfType expansion
  case and the Union+ObjectType expansion case — were not part of the retry loop
  but still call `ExpandType` via the public API (which creates a fresh
  `expandSeen`). After Plan B removes the hard limits, these calls rely on
  `ExpandType`'s own `expandSeen` visited set for termination within each call,
  but cycles that span across unification and expansion may not be caught.
- Plan B's bulk replacement of `c.Unify` calls was verified by grepping for
  residual `c.Unify` calls in `unify.go` (zero found). However, the correctness
  of `depth` propagation (forwarding `depth` vs `depth+1`) was not individually
  audited for each call site.
- Plan B deferred Step 6 (evaluate removing ad-hoc counters:
  `expandTypeRefsCount`, `skipTypeRefsCount`, `insideKeyOfTarget`) and the
  evaluation of `canExpandTypeRef`'s transitive cycle detection overlap with
  `unifySeen`/`expandSeen`.

## Plan

### Step 1: Audit `c.Unify` propagation in Tuple/Array paths

Plan B replaced all `c.Unify` calls in `unify.go` with `c.unifyWithDepth` calls
that propagate `depth, seen`. A grep confirmed zero residual `c.Unify` calls
remain.

However, the **depth forwarding** at each call site was not individually audited.
The convention is:
- **Structural forwarding** (tuple elements, array elements, object properties,
  function params/return): pass `depth` unchanged
- **Alias expansion** (TypeRefType expansion via ExpandType): pass `depth+1`

Audit each `c.unifyWithDepth` call in the following paths and verify the depth
argument is correct:

- `TupleType, ArrayType` case in `unifyMatched`: each element → `depth` (structural)
- `ArrayType, TupleType` case: mirror of above
- `ArrayType, ArrayType` case: element types → `depth` (structural)
- `RestSpreadType, ArrayType` case: rest type → `depth` (structural)
- `unifyTuples` and its variants: each pair → `depth` (structural)
- `KeyOfType, KeyOfType` case: expanded keys → `depth+1` (expansion occurred)
  and inner types → `depth+1` (expansion occurred)

If any depth arguments are incorrect, fix them.

### Step 2: Audit `ExpandType` calls that remain in unify.go

After Plan B, `ExpandType` calls in `unifyMatched` use the public API (which
creates a fresh `expandSeen` each time). This is correct for one-shot expansion
but means that cycles detected during a *previous* expansion are not remembered.
Verify this is safe for each call site:

1. **KeyOfType expansion**: Expands both `keyof` types to get concrete keys, then
   unifies the results via `c.unifyWithDepth(ctx, ..., depth+1, seen)`. The
   `unifySeen` set is propagated through the unification, so cycles on the
   unification side are handled. The expansion side gets a fresh `expandSeen`,
   which is fine because `keyof` expansion is bounded by the number of properties.

   Verify with a test case like:
   ```escalier
   type A = { x: number, y: string }
   type B = { x: number, y: string }
   // keyof A should unify with keyof B
   ```

2. **Union+ObjectType expansion**: Expands each union member one level to check if
   it's an `ObjectType`. This is one-shot (not recursive) and doesn't call
   `unifyWithDepth` directly on the result.

   **Risk:** Low. Bounded by union member count. **No change needed.**

### Step 3: Validate the three original scenarios

Write targeted tests that reproduce the three scenarios from research.md issue #1
and verify they terminate promptly (not after hitting a depth limit):

#### Scenario 1: TypeRefType with TypeAlias set

```escalier
// HTMLAttributeAnchorTarget-like scenario
type Target = "_self" | "_blank" | "_parent" | "_top"
val t: Target = "_blank"
```

```escalier
// Array<any> scenario
val a: Array<any> = [1, "hello", true]
val b: Array<any> = a
```

#### Scenario 2: TupleType with rest spreads

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

**Note:** Plan A already added `TestNominalClassUnificationTerminates` which tests
self-referential nominal class unification through the last-resort expansion path.

### Step 5: Remove residual workaround code and comments

After verifying all tests pass:

1. **Decide whether to keep the `depth` parameter.** Plan B removed
   `maxUnifyDepth` but kept `depth` threaded through `unifyWithDepth` for
   diagnostic value. If the validation in Steps 1-4 shows that the visited set
   reliably handles all recursion cases, remove the `depth` parameter from
   `unifyWithDepth` entirely to simplify the interface. If there is value in
   keeping it (e.g. for logging or as a last-resort safety net during
   development), add a comment making clear it is diagnostic-only and not a
   termination mechanism.

2. **Evaluate `canExpandTypeRef`'s transitive cycle detection.** Plan B's
   `unifySeen` and `expandSeen` visited sets overlap with the transitive cycle
   check (A→B→A) in `canExpandTypeRef`. Consider keeping it as an optimization
   (avoids entering `ExpandType` + `unifyWithDepth` for known cycles) or removing
   it to simplify `canExpandTypeRef`.

3. **Evaluate removing ad-hoc counters** (deferred from Plan B Step 6):
   - **`expandTypeRefsCount`**: Still useful for controlling expansion eagerness
     (e.g. `ExpandType(ctx, t, 1)` for "expand one level"). No longer needed as
     a safety mechanism. Consider keeping as an optimization hint.
   - **`skipTypeRefsCount`**: Skips expansion inside structural types to avoid
     unnecessary work. Consider keeping as an optimization.
   - **`insideKeyOfTarget`**: Test whether removing this counter causes any test
     failures. If not, remove it.

4. **Update the TODO at expand_type.go** — After Plan B adds visited-set cycle
   detection to `ExpandType`, the TODO about marking type aliases as recursive
   can be updated to reflect that cycle detection is now handled dynamically. The
   `Recursive` flag on `TypeAlias` is no longer a prerequisite for correctness,
   though it could still be useful as an optimization (skip visited-set tracking
   for non-recursive aliases).

5. **Clean up comments** in `unifyPruned` and `unifyMatched` that reference the
   old retry loop or explain why certain cases fall through to it.

6. **Reduce `maxExpansionRetries`** — Plan B increased it from 10 to 100 as a
   defensive safety net. After verifying the visited set handles all cycles, this
   could be reduced or removed entirely.

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
- **ExpandType calls without visited set**: The `ExpandType` calls in `unifyMatched`
  (KeyOfType and Union+ObjectType cases) create their own expansion context. If the
  types being expanded contain recursive aliases, the expansion-side visited set
  (from Plan B) should catch them, but this depends on `ExpandType` correctly
  threading its own seen set through recursive calls. Step 2 verifies this.
- **Performance regression**: Unlikely but possible if the visited-set map becomes
  a bottleneck for very hot unification paths. Step 6 checks for this.
- **Thread safety**: The `unifySeen` and `expandSeen` maps are created per top-level
  call and threaded through parameters, so they are not shared across goroutines.
  If the checker ever runs concurrently (e.g. checking multiple files in parallel),
  this design is safe because each `Unify`/`ExpandType` entry point creates its own
  seen set.
