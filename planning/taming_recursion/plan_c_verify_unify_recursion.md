# Plan C: Verify and harden unification recursion

**Prerequisites:** Plans A and B are implemented. Plan A is done (PR #451).
Plan B is done.

**Status: Implemented.**

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
- Tier 1: `canExpandTypeRef` → `ExpandType(t, 1)` → `unifyInner(seen)`
- Tier 2: `ExpandType(t, 0)` + Prune → `continue` for non-TypeRef types
- Tier 3: Last-resort `ExpandType(t, 1)` for nominal/refused TypeRefTypes

The `noMatchError` sentinel ensures expansion is only attempted when no case in
`unifyMatched` handled the types — authoritative errors from `bind`, same-alias
comparison, etc. are returned immediately. `canExpandTypeRef` blocks `IsTypeParam`
aliases, nominal types, and transitive self-referential cycles (A→B→A).

Plan B added `unifySeen` and `expandSeen` visited-set cycle detection, removed
`maxUnifyDepth`, and increased `maxExpansionRetries` from 10 to 100 as a safety
net. All ~70 internal `c.Unify` calls in `unify.go` were replaced with
`c.unifyInner(..., seen)`. All helper functions (`bind`,
`unifyFuncTypes`, `unifyTuples`, `unifyExtractor`, `unifyClosedWithRests`,
`unifyPatternWithUnion`, `handleArrayConstraintBinding`, and all tuple unification
variants) were updated to accept and propagate `seen unifySeen`.

However, several areas need verification:

- The `ExpandType` calls that remain in `unifyMatched` — the KeyOfType expansion
  case and the Union+ObjectType expansion case — were not part of the retry loop
  but still call `ExpandType` via the public API (which creates a fresh
  `expandSeen`). After Plan B removes the hard limits, these calls rely on
  `ExpandType`'s own `expandSeen` visited set for termination within each call,
  but cycles that span across unification and expansion may not be caught.
- Plan B's bulk replacement of `c.Unify` calls was verified by grepping for
  residual `c.Unify` calls in `unify.go` (zero found).
- Plan B deferred Step 6 (evaluate removing ad-hoc counters:
  `expandTypeRefsCount`, `skipTypeRefsCount`, `insideKeyOfTarget`) and the
  evaluation of `canExpandTypeRef`'s transitive cycle detection overlap with
  `unifySeen`/`expandSeen`.

## Plan

### Step 1: Audit `seen` propagation in Tuple/Array paths

**Result: All correct. No fixes needed.**

Audited every `c.unifyInner` call in the Tuple/Array/KeyOf paths. All call sites
correctly propagate the `seen` set. (The `depth` parameter that existed during the
original audit was subsequently removed — see Step 5.1.)

Verified call sites:

- `TupleType, ArrayType` case in `unifyMatched` ✓
- `ArrayType, TupleType` case in `unifyMatched` ✓
- `ArrayType, ArrayType` case in `unifyMatched` ✓
- `RestSpreadType, ArrayType` case in `unifyMatched` ✓
- `unifyTuples` and all variants (`unifyFixedTuples`, `unifyFixedVsVariadic`,
  `unifyVariadicVsFixed`, `unifyVariadicVsVariadic`) ✓
- `KeyOfType, KeyOfType` case in `unifyMatched` ✓
- `unifyExtractor` (helper for custom matcher patterns) ✓
- `unifyClosedWithRests` (helper for rest-spread object unification) ✓
- `unifyPatternWithUnion` (helper for pattern-vs-union matching) ✓

### Step 2: Audit `ExpandType` calls that remain in unify.go

**Result: All safe. No changes needed.**

Three `ExpandType` call sites remain in `unifyMatched`, all using the public API
(which creates a fresh `expandSeen` each time):

1. **KeyOfType, KeyOfType case in `unifyMatched`**: `c.ExpandType(ctx, keyof, 1)`
   on both sides — bounded by the number of properties in the target object.
   Results unified via `unifyInner(seen)`, so `unifySeen` catches cycles on
   the unification side. **Safe.**

2. **ObjectType-vs-UnionType case in `unifyMatched`** (within the destructured-
   object-vs-union handling): one-shot expansion of each union member to check
   if it's an `ObjectType`. Bounded by union member count. No `unifyInner`
   call on the expanded result. **Safe.**

3. **`unifyPatternWithUnion`**: Same one-shot expansion pattern as #2. **Safe.**

### Step 3: Validate the three original scenarios

**Result: All three scenarios pass. Tests added in `unify_recursion_test.go`.**

#### Scenario 1: TypeRefType with TypeAlias set — `TestUnifyRecursion_TypeRefWithAlias`

7 test cases covering: string literal union alias, `Array<number>` assignment,
same alias (no args), same alias (with args), different alias (same structure),
TypeRef vs concrete ObjectType, recursive type alias (`Json`).

#### Scenario 2: TupleType with rest spreads — `TestUnifyRecursion_TupleWithRest`

3 test cases: single rest spread, rest with prefix and suffix, tuple vs Array.

#### Scenario 3: Large ObjectType instances — `TestUnifyRecursion_LargeObjectType`

2 test cases: 25-property object (simulating SVG attributes), 20-property object
with nested TypeRefType values (Color, Size aliases).

### Step 4: Add regression tests

**Result: 6 additional regression tests in `TestUnifyRecursionTerminates`.**

Test file: `internal/checker/tests/unify_recursion_test.go`

All tests use a 2-second timeout to catch infinite recursion. Test cases:

1. Union of TypeRefTypes vs ObjectType
2. keyof TypeRefType
3. keyof of two structurally identical types
4. Self-referential nominal class (complements `TestNominalClassUnificationTerminates`)
5. Recursive tree type (`Tree = {value: number, children: Array<Tree>}`)
6. Generic container with recursive type arg (`Container<T> = {value: T, next: Container<T> | null}`)

### Step 5: Remove residual workaround code and comments

**Result: Cleanup applied; some items deferred.**

1. **`depth` parameter: Removed.** Renamed `unifyWithDepth` to `unifyInner` and
   removed the `depth int` parameter from all internal unification functions
   (`unifyInner`, `unifyPruned`, `unifyMatched`, `bind`, `unifyFuncTypes`,
   `unifyTuples`, `unifyFixedTuples`, `unifyFixedVsVariadic`,
   `unifyVariadicVsFixed`, `unifyVariadicVsVariadic`, `unifyExtractor`,
   `unifyClosedWithRests`, `unifyPatternWithUnion`,
   `unifyNumericWithStringIndexSigs`, `handleArrayConstraintBinding`).
   The `depth` parameter was diagnostic-only and never used for termination —
   the `unifySeen` visited set handles all cycle detection.

2. **`canExpandTypeRef`'s transitive cycle detection: Kept as optimization.**
   Added a comment in `expand_type.go` explaining the overlap with
   `unifySeen`/`expandSeen` and the rationale for keeping it (avoids entering
   `ExpandType` + `unifyInner` for known simple alias cycles).

3. **Ad-hoc counters:**
   - **`expandTypeRefsCount`**: Kept. Still useful for controlling expansion
     eagerness (`ExpandType(ctx, t, 1)` for "expand one level"). Not a safety
     mechanism — it's an optimization hint.
   - **`skipTypeRefsCount`**: Kept. Skips expansion inside structural types to
     avoid unnecessary work. An optimization, not a safety mechanism.
   - **`insideKeyOfTarget`**: Deferred removal (TODO #455). Confirmed overlap
     with `expandSeen` cycle detection, but removal is risky — it affects the
     `expandSeenKey` struct and all `expandTypeWithConfig` call sites. Updated
     TODO comments to note Plan C confirmed the overlap.

4. **TODO about marking type aliases as recursive**: No such TODO exists in the
   current codebase. The `Recursive` flag on `TypeAlias` was never implemented;
   `expandSeen` handles cycle detection dynamically. No action needed.

5. **Stale comments**: No comments referencing the old retry loop remain in
   `unifyPruned` or `unifyMatched`. No cleanup needed.

6. **`maxExpansionRetries`: Reduced from 100 to 10.** The test suite never
   exceeds 2 iterations. The safety net is now tighter while still leaving
   headroom for edge cases.

### Step 6: Performance validation

**Result: No regressions detected.**

1. **Full test suite**: All packages pass (`go test ./...`).
2. **Benchmarks**: All benchmarks in `benchmark_test.go` pass with no regression.
3. **Visited-set memory**: Not explicitly profiled, but the design (keys are
   TypeAlias pointer pairs + typeArgKey strings) ensures memory is proportional
   to the number of distinct type alias instantiations encountered, not the
   number of unification calls.

## Testing strategy

All tests from Plans A and B continue to pass. The 18 new tests in
`internal/checker/tests/unify_recursion_test.go` are additive.

## Risks (post-implementation assessment)

- **Missed propagation sites**: Step 1 audited all call sites. No issues found.
- **ExpandType calls without visited set**: Step 2 confirmed all 3 remaining
  `ExpandType` calls in `unifyMatched` are bounded and safe.
- **Performance regression**: Step 6 confirmed no regression in tests or benchmarks.
- **Thread safety**: Design is safe — `unifySeen` and `expandSeen` are created
  per top-level call and threaded through parameters, not shared across goroutines.

## Remaining work (deferred)

- **TODO #455**: Evaluate removing `insideKeyOfTarget` counter and the
  `insideKeyOf` field from `expandSeenKey`. Plan C confirmed the overlap with
  `expandSeen` but deferred removal due to the number of affected call sites.
- **Issue #5**: `getMemberType` expansion loop still relies on pointer identity
  for termination. Could be hardened with an iteration limit or `expandSeen`.
