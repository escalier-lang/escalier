# Exhaustive Pattern Matching — Implementation Plan

## Status

| Phase | Status | Commit |
|---|---|---|
| Phase 3 (Error types) | **Done** | `c008d08` |
| Phase 6 (Boolean expansion) | **Done** | `c008d08` |
| Phase 1 (Coverage extraction) | **Done** | `ba06f78` |
| Phase 2 (Exhaustiveness checking) | **Done** | `262e027` |
| Phase 4 (Integration) | **Done** | `e12b2a9` |
| Phase 5 (Tuple exhaustiveness) | **Done** | — |
| Phase 7 (Nested exhaustiveness) | Not started | — |

## Approach

Rather than implementing a full Maranget-style pattern matrix algorithm (as described at
[compiler.club](https://compiler.club/compiling-pattern-matching/)), we take a
**set-coverage** approach that operates on the already-inferred types from the existing
pattern matching infrastructure, extended with recursive checking for nested patterns.
This works because:

1. Escalier's existing unification (Phases 1–6) already determines which union members each
   pattern matches, via `MatchedUnionMembers` on `ObjectType` and the `customMatcher` param
   type on extractors.
2. The set-coverage approach naturally produces the list of uncovered types needed for
   error messages (R10) and future LSP integration (R11).
3. Nested exhaustiveness (R13) is handled by grouping branches that match the same union
   member and recursively checking their inner patterns, reusing the same coverage logic.

## Data Structures

### `ExhaustivenessResult`

A structured result type returned by the exhaustiveness checker, satisfying R11 (design
must not impede future LSP integration):

```go
// internal/checker/exhaustiveness.go

type ExhaustivenessResult struct {
    IsExhaustive    bool
    UncoveredTypes  []type_system.Type  // union members not covered by any branch
    RedundantCases  []RedundantCase     // branches that can never match
}

type RedundantCase struct {
    CaseIndex int       // index into MatchExpr.Cases
    Span      ast.Span  // span of the redundant branch's pattern
}
```

This gives the LSP (F1) everything it needs to generate missing match arms without
re-implementing the logic.

### `CaseCoverage`

Per-branch intermediate data computed during the analysis:

```go
type CaseCoverage struct {
    Pattern       ast.Pat
    HasGuard      bool
    CoveredTypes  []type_system.Type  // which union members this branch covers
    IsCatchAll    bool                // true for unguarded wildcard/identifier
    InnerPatterns []ast.Pat           // nested patterns (e.g., args of ExtractorPat)
}
```

The `InnerPatterns` field is used by Phase 7 (nested exhaustiveness). For an
`ExtractorPat` like `Result.Ok(0)`, `InnerPatterns` would be `[LitPat(0)]`. For a
top-level `WildcardPat` or `IdentPat`, it is nil.

## Implementation Order

The phases have the following dependency chain and should be implemented in this order:

| Order | Phase | Rationale |
|---|---|---|
| 1st | Phase 3 (Error types) + Phase 6 (Boolean expansion) | Leaf dependencies — no prerequisites, needed by later phases |
| 2nd | Phase 1 (Coverage extraction) | Core building block consumed by Phase 2 |
| 3rd | Phase 2 (Exhaustiveness checking) | Consumes Phase 1 output, uses Phase 6 for boolean targets |
| 4th | Phase 4 (Integration) | Wires Phases 1–3 into the checker — first point where end-to-end tests can run |
| 5th | Phase 5 (Tuple exhaustiveness) | Extends Phase 2 for tuple targets; independent of Phase 7 |
| 6th | Phase 7 (Nested exhaustiveness) | Extends Phase 2 with recursive checking; independent of Phase 5 |

Phases 5 and 7 are independent of each other and can be done in either order. Tests should
be added incrementally: basic exhaustiveness tests (Cases 1–13) after Phase 4, tuple tests
(Cases 14–15) after Phase 5, and nested tests (Cases 16–20) after Phase 7.

## Implementation Phases

### Phase 1: Coverage extraction — `computeCaseCoverage` ✅

**Status:** Implemented in `ba06f78`.

**File:** `internal/checker/exhaustiveness.go`

```go
func (c *Checker) computeCaseCoverage(
    matchCase *ast.MatchCase,
    targetType type_system.Type,
) CaseCoverage
```

**Logic by pattern type:**

| Pattern kind | How to determine covered types |
|---|---|
| `WildcardPat` | If no guard: `IsCatchAll = true`. If guard: covers nothing (R6). |
| `IdentPat` | Same as `WildcardPat` — unguarded = catch-all, guarded = nothing. |
| `ExtractorPat` | The `customMatcher` method's param type identifies the variant. After unification, look up the extractor's resolved type and extract the param type from `[Symbol.customMatcher]`. If no guard: covers that variant. Also populate `InnerPatterns` with the extractor's arg patterns for nested checking (Phase 7). |
| `InstancePat` | The pattern's inferred type has a nominal ID matching a specific union member. Look up which union member shares that ID. If no guard: covers that member. |
| `ObjectPat` | Read `MatchedUnionMembers` from the pattern's inferred `ObjectType` (populated by `unifyPatternWithUnion` in Phase 4 of the existing implementation). If no guard: covers those members. |
| `LitPat` | The pattern's inferred type is a literal type (e.g., `true`, `"foo"`, `42`). If no guard: covers that literal type within the union. |
| `TuplePat` | Handled specially — see Phase 5. |

**Guard handling (R6):** If `matchCase.Guard != nil`, the branch covers nothing regardless
of pattern type. Set `HasGuard = true` and `CoveredTypes = nil`.

**Helper functions added:**

- `getCustomMatcherParamType(ext)` — walks an `ExtractorType`'s object to find
  `[Symbol.customMatcher]` and returns its param type.
- `findMatchingMembers(patternType, targetType)` — finds which union members (or single
  target) a pattern type covers.
- `typesMatchForCoverage(a, b)` — compares two types for coverage purposes: pointer
  identity first, then `TypeRefType` by `TypeAlias` pointer, nominal `ObjectType` by `ID`,
  `LitType` by `Lit.Equal()`, with recursive resolution through `TypeAlias.Type`.

### Phase 2: Exhaustiveness checking — `checkExhaustiveness` ✅

**Status:** Implemented in `262e027`.

**File:** `internal/checker/exhaustiveness.go`

```go
func (c *Checker) checkExhaustiveness(
    expr *ast.MatchExpr,
    targetType type_system.Type,
) *ExhaustivenessResult
```

**Algorithm (as implemented):**

1. **Expand the target type into a coverage set.**
   - Resolve `TypeRefType` to its underlying type via `resolveTypeRef` (added during
     Phase 4 integration — needed because type aliases like `type Color = ...` remain as
     `TypeRefType` in the target type).
   - Expand `boolean` primitive to `{true, false}` via `expandBooleanType` (Phase 6).
   - `UnionType`: each member is a separate item in the coverage set (finite).
   - Non-union types (`number`, `string`, object types): non-finite, require a catch-all.

2. **Compute coverage for each branch** using `computeCaseCoverage`.

3. **Track covered set and detect redundancy (R7)** — initialize an empty covered set,
   then iterate branches in order. For each branch:
   - **Skip guarded branches for redundancy checking.** If `HasGuard` is true, the branch
     covers nothing (`CoveredTypes` is nil) but is not redundant — guards are runtime
     filters, not dead code. Do not run the redundancy predicate on guarded branches.
   - **For unguarded branches**, before adding to the covered set, check if the branch is
     redundant: if `CoveredTypes` is non-empty and every type in it is already in the
     covered set *accumulated so far* (or the branch is a catch-all but all types are
     already covered), record it as a `RedundantCase`. This catches duplicates like two
     `false` branches — the second one is redundant because `false` was added to the
     covered set by the first.
   - **Then** add the branch's `CoveredTypes` to the covered set.
   - If a catch-all is encountered (unguarded wildcard/identifier), mark all remaining
     types as covered.

4. **Compute uncovered types** — for finite types, report each uncovered member in
   declaration order. For non-finite types, report the target type itself if no catch-all
   was found.

**Deferred to Phase 7:** Steps 3–4 from the original plan (grouping branches by union
member and checking nested exhaustiveness) are not yet implemented. The current
implementation treats any branch covering a union member as fully covering it, regardless
of inner patterns.

**Helper functions added:**

- `expandCoverageSet(targetType)` — extracts the finite coverage set from union types;
  returns `(members, true)` for unions and `(nil, false)` for non-finite types.
- `indexInCoverageSet(t, coverageSet)` — finds a type's position in the coverage set
  using `typesMatchForCoverage`.
- `resolveTypeRef(t)` — resolves a `TypeRefType` to its underlying `TypeAlias.Type`.

**Type comparison:** Two types "match" for coverage purposes when:
- They are the same pointer (identity — needed because `MatchedUnionMembers` stores
  direct references to union member objects).
- Both are `TypeRefType` with the same `TypeAlias` pointer (enum variants).
- Both are nominal `ObjectType` with the same `ID` (class instances).
- Both are literal types (`LiteralType`) with equal values.
- One is a `TypeRefType` that resolves to the other (structural type aliases).

### Phase 3: Error reporting ✅

**Status:** Implemented in `c008d08`.

**File:** `internal/checker/error.go`

Added two new error/warning types and the `IsWarning()` method to the `Error` interface.

**Changes made:**

1. Added `IsWarning() bool` to the `Error` interface.
2. Added `IsWarning()` returning `false` on all 35 pre-existing error types.
3. Added `NonExhaustiveMatchError` with two message formats:
   - Finite unions: `"Non-exhaustive match: missing cases for Color.Hex, Color.RGB"`
   - Non-finite types: `"Non-exhaustive match: type 'number' is not fully covered; add a catch-all branch"`
4. Added `RedundantMatchCaseWarning` with `IsWarning()` returning `true`.
5. Added `"strings"` import for `strings.Join` in error formatting.

**Formatting uncovered types (R10, R2):** The `Message()` method on
`NonExhaustiveMatchError` delegates to each type's `String()` method, which already
produces human-readable output:
- `TypeRefType` with a qualified name → `Color.Hex` (via `QualIdentToString`)
- Literal types → `"east"`, `false` (via `LitType.String()`)
- Object types → `{kind: "rect", width: number, height: number}`
- Non-finite base types → detected by checking for `*PrimType` in uncovered types

### Phase 4: Integration into `inferMatchExpr` ✅

**Status:** Implemented in `e12b2a9`.

**File:** `internal/checker/infer_expr.go`

Integrated `checkExhaustiveness` into `inferMatchExpr` with error gating.

**Changes made:**

1. Introduced a `matchErrors` slice that tracks errors from target inference, pattern
   inference, and unification (separate from case body errors).
2. After inferring all cases, `matchErrors` is appended to the final error list.
3. Exhaustiveness checking only runs when `matchErrors` is empty.
4. Reports `NonExhaustiveMatchError` for uncovered types and `RedundantMatchCaseWarning`
   for redundant branches.

**Test updates required during integration:**

- Updated `pattern_match_test.go` — added catch-all `_` branches to 5 tests that match
  non-union/non-finite types (now correctly flagged as non-exhaustive).
- Updated `fixtures/pattern_matching/lib/pattern_matching.esc` — added catch-all branches
  to 5 non-exhaustive matches and regenerated fixture expected outputs.
- Created `exhaustive_match_test.go` with 29 table-driven test cases covering exhaustive
  matches, non-exhaustive errors, redundancy warnings, guard behavior, and error gating.

**Implementation note:** During integration, two issues in Phase 2 were discovered and
fixed:
- `resolveTypeRef` helper added — `TypeRefType` aliases (like `type Color = Color.RGB |
  Color.Hex`) need to be resolved to their underlying union before coverage analysis.
- Pointer identity check added to `typesMatchForCoverage` — `MatchedUnionMembers` stores
  direct references to union member objects, and structural `ObjectType` members are not
  comparable by ID or name.

### Phase 5: Tuple exhaustiveness (R9) ✅

**Status:** Implemented.

Tuple exhaustiveness requires combinatorial reasoning. For the initial implementation,
support only tuples where every element position has a finite type (boolean or literal
union). For tuples containing non-finite types, require a catch-all.

**Algorithm for finite tuples (as implemented):**

1. Expand each element position into its set of possible values (e.g., `boolean` →
   `{true, false}`). This is done by `expandTupleCoverageSet`, which calls
   `expandCoverageSet` recursively on each element after resolving type refs and
   expanding booleans.
2. Compute the Cartesian product of all positions via `cartesianProductTuples` — this
   is the full coverage set. Each entry is a synthetic `TupleType`.
3. For each `TuplePat` branch, determine which combination it covers by examining each
   element pattern:
   - `LitPat` → covers that specific value at that position (via `findMatchingMembers`).
   - `WildcardPat` / `IdentPat` → covers all values at that position.
   - A `TuplePat` where all elements are `WildcardPat`/`IdentPat` is treated as a
     catch-all (`IsCatchAll = true`).
4. A `TuplePat` may cover multiple combinations if some positions are wildcards. The
   covered combinations are the Cartesian product of per-position coverage sets.
5. The standard covered-set tracking and redundancy detection from Phase 2 applies.

**Complexity bound:** The Cartesian product can be large (e.g., a 10-element boolean tuple
has 1024 combinations). A limit of 256 combinations is enforced; if the product exceeds
it, the tuple is treated as non-finite (requiring a catch-all).

**Additional changes:**

- Added `TupleType` element-wise comparison to `typesMatchForCoverage`.
- Added `IsNonFinite` field to `ExhaustivenessResult` and `NonExhaustiveMatchError` so
  that the error message correctly distinguishes between finite types with missing
  members ("missing cases for [true, false]") and non-finite types ("type '[boolean,
  number]' is not fully covered; add a catch-all branch").
- Updated `MatchWithPatternBindings` test (removed redundant `_` catch-all after
  all-ident `TuplePat`, which is now correctly recognized as a catch-all).
- Updated `pattern_matching` fixture (removed two redundant `_` branches after
  all-ident `TuplePat` patterns).

### Phase 6: Boolean expansion (R3) ✅

**Status:** Implemented in `c008d08`.

**File:** `internal/checker/exhaustiveness.go`

Added `expandBooleanType(t)` function that checks if a type is `*PrimType` with
`BoolPrim`, and if so returns a synthetic union via
`NewUnionType(nil, NewBoolLitType(nil, true), NewBoolLitType(nil, false))`.

Called at the top of `checkExhaustiveness` before `expandCoverageSet`, so the standard
union coverage algorithm handles boolean targets transparently.

### Phase 7: Nested pattern exhaustiveness (R13)

When multiple branches match the same union member with different inner patterns, we must
verify that those inner patterns collectively exhaust the member's inner type. This phase
adds recursive exhaustiveness checking for nested patterns.

**Algorithm:**

1. **Group branches by union member.** After Phase 1 computes `CaseCoverage` for each
   branch, group branches that cover the same union member. For example, given:
   ```ts
   Result.Ok(0) => "zero",
   Result.Ok(1) => "one",
   Result.Err(message) => message,
   ```
   The `Result.Ok` group contains two branches with inner patterns `[LitPat(0)]` and
   `[LitPat(1)]`. The `Result.Err` group contains one branch with `[IdentPat(message)]`.

2. **Check each group for inner exhaustiveness.** For each union member's group:
   - If any branch in the group has a catch-all inner pattern (wildcard or identifier for
     all inner positions), the member is fully covered. No further checking needed.
   - Otherwise, determine the inner type from the `customMatcher` return type (for
     extractors) or from the instance type's constructor params (for instance patterns).
   - Recursively call the exhaustiveness checker on the inner patterns against the inner
     type. For single-argument extractors, this is a direct recursive check. For
     multi-argument extractors, treat the inner patterns as a tuple and apply the tuple
     exhaustiveness logic from Phase 5.

3. **Report partial coverage.** If a union member's inner patterns are not exhaustive, the
   member is not fully covered. The error message should indicate both the member and what's
   missing at the inner level:
   - `"Non-exhaustive match: Result.Ok is not fully covered; add a catch-all branch"`
   - For finite inner types: `"Non-exhaustive match: Wrapper.Bool is missing case for false"`

**Structural object patterns and missing properties:**

Structural object patterns may only list a subset of the matched type's properties (per R1
in the pattern matching requirements). For nested exhaustiveness, properties that are
omitted from the pattern should be treated as implicitly matched by a wildcard (`_`). For
example, given:

```ts
type Foo = {kind: "a", x: number, y: boolean}
         | {kind: "b", z: string}

declare val foo: Foo
match foo {
    {kind: "a", x: 0} => ...,   // y is implicitly _
    {kind: "a", x} => ...,      // y is implicitly _, x is a catch-all
    {kind: "b"} => ...,         // z is implicitly _
}
```

When checking inner exhaustiveness for the `{kind: "a"}` group, the inner patterns for
`x` are `[LitPat(0), IdentPat(x)]` and for `y` are `[WildcardPat, WildcardPat]` (since
`y` was omitted from both branches). The `y` position is trivially exhaustive, and `x` is
exhaustive because the second branch has an identifier catch-all.

**Extracting inner types:**

For `ExtractorPat`, the inner type comes from the `customMatcher` method's return type
(a tuple). After unification and type parameter substitution, this tuple contains the
concrete types of the extractor's arguments. For a single-argument extractor like
`Result.Ok(value: number)`, the inner type is `number`. For multi-argument extractors like
`Color.RGB(r, g, b)`, the inner type is `[number, number, number]`.

**Recursive depth:** The recursion naturally terminates because each level examines inner
patterns that are structurally smaller than the outer pattern. In practice, nesting is
rarely deeper than 2–3 levels.

## File Summary

| File | Changes | Status |
|---|---|---|
| `internal/checker/exhaustiveness.go` | **New.** `ExhaustivenessResult`, `CaseCoverage`, `computeCaseCoverage`, `checkExhaustiveness`, `expandBooleanType`, `resolveTypeRef`, `expandCoverageSet`, `expandTupleCoverageSet`, `cartesianProductTuples`, `findMatchingMembers`, `typesMatchForCoverage`, `getCustomMatcherParamType`, `indexInCoverageSet` | Done |
| `internal/checker/error.go` | Add `NonExhaustiveMatchError`, `RedundantMatchCaseWarning`, `IsWarning() bool` to `Error` interface and all existing types | Done |
| `internal/checker/infer_expr.go` | Add `matchErrors` slice and call `checkExhaustiveness` at the end of `inferMatchExpr` | Done |
| `internal/checker/tests/exhaustive_match_test.go` | **New.** 38 table-driven tests covering phases 1–6 | Done |
| `internal/checker/tests/pattern_match_test.go` | Updated 5 tests to add catch-all branches for non-finite types | Done |
| `fixtures/pattern_matching/lib/pattern_matching.esc` | Updated 5 match expressions to add catch-all branches | Done |

## Test Plan

Tests are in `internal/checker/tests/exhaustive_match_test.go` using table-driven format.

**Implemented test cases (41 total):**

| Category | Test cases | Req |
|---|---|---|
| Exhaustive enum matches | `EnumFullyCovered`, `EnumWithCatchAll` | R1, R2 |
| Boolean exhaustiveness | `BooleanBothBranches`, `BooleanWithCatchAll` | R3 |
| Literal union coverage | `LiteralUnionFullyCovered` | R4 |
| Structural union coverage | `StructuralUnionFullyCoveredByObjectPatterns` | R12 |
| Nominal union coverage | `NominalUnionCoveredByInstancePatterns`, `MixedNominalAndStructuralPatterns` | R1 |
| Non-finite catch-all | `NonFiniteTypeCoveredByCatchAll`, `StringTypeCoveredByCatchAll`, `GuardedBranchWithCatchAll` | R5, R8 |
| Missing enum variant | `EnumMissingVariant` | R2, R10 |
| Missing boolean literal | `BooleanMissingFalse`, `BooleanMissingTrue`, `BooleanOnlyGuardedBranches` | R3, R6 |
| Missing literal union members | `LiteralUnionMissingMembers` | R4, R10 |
| Non-finite without catch-all | `NonFiniteTypeNoCatchAll`, `StringTypeNoCatchAll`, `NonFiniteTypeOnlyGuardedBranches` | R6, R8 |
| Structural partial coverage | `StructuralUnionPartialCoverage` | R12 |
| Nominal class no catch-all | `NominalClassNoCatchAll` | R8 |
| Catch-all covers enum | `EmptyMatchOnEnum` | R5 |
| Redundancy warnings | `RedundantDuplicateLiteralBranch`, `RedundantCatchAllAfterFullCoverage`, `RedundantDuplicateEnumVariant`, `RedundantDuplicateStringLiteral` | R7 |
| Guard behavior | `GuardedBranchDoesNotCoverType`, `GuardedBranchNotRedundant` | R6 |
| Error gating | `NoExhaustivenessCheckWhenPatternErrors` | Phase 4 |
| Tuple fully covered | `TupleBoolBoolFullyCovered`, `TupleLiteralUnionFullyCovered` | R9, Case 14 |
| Tuple with wildcards | `TupleBoolBoolWithWildcard`, `TupleCatchAll`, `TupleAllIdentIsCatchAll` | R9 |
| Tuple missing combos | `TupleBoolBoolMissingCombinations` | R9, Case 15 |
| Tuple non-finite | `TupleNonFiniteElementNoCatchAll`, `TupleNonFiniteElementWithCatchAll` | R9 |
| Tuple redundancy | `TupleRedundantBranch` | R7, R9 |
| Tuple empty | `TupleEmptyFullyCovered` | R9 |
| Tuple union of tuples | `TupleUnionOfTuplesFullyCovered` | R9 |

**Remaining test cases (to be added with future phases):**

| Phase | Test Cases from requirements doc |
|---|---|
| Phase 7 (nested) | Cases 16, 17, 18, 19, 20 |

## Risks and Mitigations

**Risk:** `MatchedUnionMembers` may not be populated for all structural pattern scenarios
(e.g., when the target is not a union type).
**Mitigation:** Only rely on `MatchedUnionMembers` when the target is a union type. For
non-union targets, determine coverage based on the target's kind:
- A single nominal class type is fully covered by a pattern that matches it (instance
  pattern with matching ID, or structural pattern whose fields all unify successfully).
- Non-finite types (`number`, `string`, open object types) are not covered by specific
  patterns — they still require an unguarded catch-all per R8.
Do not assume that a non-union target is "trivially covered" by any pattern.
**Outcome:** This risk materialized during Phase 4 integration. `ObjectPat` against
non-union targets falls back to `findMatchingMembers`, which treats non-union, non-finite
targets as requiring a catch-all. Five existing tests needed catch-all branches added.

**Risk:** Extractor patterns don't currently store which union member they matched in an
easily accessible way.
**Mitigation:** After unification, the `customMatcher`'s param type identifies the variant.
Extract this during `computeCaseCoverage` by walking the extractor's resolved type to find
the `[Symbol.customMatcher]` method and reading its param type.
**Outcome:** Implemented as planned via `getCustomMatcherParamType`. Works correctly.

**Risk:** `TypeRefType` comparison may fail if the same type alias is referenced through
different paths.
**Mitigation:** Compare by `TypeAlias` pointer identity (same alias declaration) rather
than by name string.
**Outcome:** Implemented as planned. Additionally, pointer identity comparison was added
as a first check in `typesMatchForCoverage` because `MatchedUnionMembers` stores direct
references to union member objects, and structural `ObjectType` members lack nominal IDs.

**Risk (discovered during implementation):** Target types may be `TypeRefType` aliases
that need resolution before coverage set expansion.
**Mitigation:** Added `resolveTypeRef` helper that resolves `TypeRefType` to its underlying
`TypeAlias.Type`. Called at the top of `checkExhaustiveness` before `expandCoverageSet`.
