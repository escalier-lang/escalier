# Exhaustive Pattern Matching — Implementation Plan

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

## Implementation Phases

### Phase 1: Coverage extraction — `computeCaseCoverage`

**File:** `internal/checker/exhaustiveness.go` (new file)

Create a function that examines each `MatchCase` and determines which types it covers:

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

### Phase 2: Exhaustiveness checking — `checkExhaustiveness`

**File:** `internal/checker/exhaustiveness.go`

```go
func (c *Checker) checkExhaustiveness(
    expr *ast.MatchExpr,
    targetType type_system.Type,
) *ExhaustivenessResult
```

**Algorithm:**

1. **Expand the target type into a coverage set.** This is the set of types that must be
   covered:
   - `UnionType`: each member is a separate item in the set. Expand `TypeRefType` members
     to resolve their underlying types for comparison, but keep the original `TypeRefType`
     for error messages (R2 — use variant names like `Color.Hex`).
   - `boolean` primitive (R3): expand to `{true, false}` literal types.
   - Non-finite types (`number`, `string`, object types): the coverage set is conceptually
     infinite — can only be covered by a catch-all (R8).

2. **Compute coverage for each branch** using `computeCaseCoverage`.

3. **Group branches by covered union member.** Multiple branches may target the same
   member (e.g., `Result.Ok(0)` and `Result.Ok(1)` both target `Result.Ok`). Group them
   so that nested exhaustiveness can be checked per-member in Phase 7.

4. **Check nested exhaustiveness (R13).** For each union member, collect all branches
   that cover it. If any branch covering that member is a catch-all for the inner value
   (e.g., `Result.Ok(n)` where `n` is an identifier), that member is fully covered.
   Otherwise, recursively check whether the inner patterns collectively cover the member's
   inner type (see Phase 7). A member is only marked as covered if its inner patterns are
   exhaustive.

5. **Track covered set and detect redundancy (R7)** — initialize an empty covered set,
   then iterate branches in order. For each branch:
   - **Before adding to the covered set**, check if the branch is redundant: if every type
     in the branch's `CoveredTypes` is already in the covered set *accumulated so far*
     (or the branch is a catch-all but all types are already covered), record it as a
     `RedundantCase`. This catches duplicates like two `false` branches — the second one
     is redundant because `false` was added to the covered set by the first.
   - **Then** add the branch's `CoveredTypes` to the covered set.
   - If a catch-all is encountered (unguarded wildcard/identifier), mark all remaining
     types as covered.

6. **Compute uncovered types** — subtract the covered set from the coverage set. For
   members that are partially covered at the nested level, include them in the uncovered
   set with a message indicating the inner type needs coverage.

7. **Return `ExhaustivenessResult`** with `UncoveredTypes` and `RedundantCases`.

**Type comparison:** Two types "match" for coverage purposes when:
- Both are `TypeRefType` with the same `TypeAlias` pointer (enum variants).
- Both are nominal `ObjectType` with the same `ID` (class instances).
- Both are literal types (`LiteralType`) with equal values.
- One is a `TypeRefType` that resolves to the other (structural type aliases).

### Phase 3: Error reporting

**File:** `internal/checker/error.go`

Add two new error/warning types:

```go
type NonExhaustiveMatchError struct {
    UncoveredTypes []type_system.Type
    span           ast.Span  // span of the match keyword/expression
}

func (e NonExhaustiveMatchError) Message() string {
    // For finite unions (R10):
    //   "Non-exhaustive match: missing cases for Color.Hex, Color.RGB"
    // For non-finite types (R8):
    //   "Non-exhaustive match: type 'number' is not fully covered; add a catch-all branch"
}

type RedundantMatchCaseWarning struct {
    span ast.Span  // span of the redundant pattern
}

func (e RedundantMatchCaseWarning) Message() string {
    return "Redundant match branch: this case is already covered by earlier branches"
}

func (e RedundantMatchCaseWarning) IsWarning() bool { return true }
```

**Warning vs error distinction (R7):** The existing checker `Error` interface does not
distinguish warnings from errors. Add an `IsWarning() bool` method to `RedundantMatchCaseWarning`
(returning `true`). The `Error` interface can optionally be extended, or callers can use a
type assertion to check. `NonExhaustiveMatchError` does not implement `IsWarning` and
remains a hard error.

**Formatting uncovered types (R10, R2):**
- `TypeRefType` with a qualified name → use the qualified name (e.g., `Color.Hex`)
- Literal types → use the literal representation (e.g., `"east"`, `false`)
- Object types → use the type's string representation
- Non-finite base types → special message: `type 'number' is not fully covered; add a catch-all branch`

### Phase 4: Integration into `inferMatchExpr`

**File:** `internal/checker/infer_expr.go`

After the existing case-by-case type inference loop (after line 1415), add the
exhaustiveness check:

```go
// After inferring all case types...

result := c.checkExhaustiveness(expr, targetType)

if !result.IsExhaustive {
    errors = append(errors, &NonExhaustiveMatchError{
        UncoveredTypes: result.UncoveredTypes,
        span:           expr.Span(),
    })
}

for _, redundant := range result.RedundantCases {
    errors = append(errors, &RedundantMatchCaseWarning{
        span: redundant.Span,
    })
}
```

This placement ensures that:
- All pattern types have been inferred and unified (so `MatchedUnionMembers` is populated).
- All extractor types have been resolved (so `customMatcher` param types are available).
- The exhaustiveness check sees the fully-resolved state of all patterns.

### Phase 5: Tuple exhaustiveness (R9)

Tuple exhaustiveness requires combinatorial reasoning. For the initial implementation,
support only tuples where every element position has a finite type (boolean or literal
union). For tuples containing non-finite types, require a catch-all.

**Algorithm for finite tuples:**

1. Expand each element position into its set of possible values (e.g., `boolean` →
   `{true, false}`).
2. Compute the Cartesian product of all positions — this is the full coverage set.
3. For each `TuplePat` branch, determine which combination it covers by examining each
   element pattern:
   - `LitPat` → covers that specific value at that position.
   - `WildcardPat` / `IdentPat` → covers all values at that position.
4. A `TuplePat` may cover multiple combinations if some positions are wildcards.
5. Apply the same covered-set tracking as Phase 2.

**Complexity bound:** The Cartesian product can be large (e.g., a 10-element boolean tuple
has 1024 combinations). Set a reasonable limit (e.g., 256 combinations) and fall back to
requiring a catch-all if the product exceeds it.

### Phase 6: Boolean expansion (R3)

The `boolean` primitive type needs special handling since it's not represented as a union
internally but is semantically equivalent to `true | false`.

In `checkExhaustiveness`, when the target type is `boolean`:
- Expand it to a synthetic union of `LiteralType(true)` and `LiteralType(false)`.
- Proceed with the standard union coverage algorithm.

This ensures `match b { true => ..., false => ... }` is recognized as exhaustive without
a catch-all.

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

| File | Changes |
|---|---|
| `internal/checker/exhaustiveness.go` | **New.** `ExhaustivenessResult`, `CaseCoverage`, `computeCaseCoverage`, `checkExhaustiveness` |
| `internal/checker/error.go` | Add `NonExhaustiveMatchError`, `RedundantMatchCaseWarning` |
| `internal/checker/infer_expr.go` | Call `checkExhaustiveness` at the end of `inferMatchExpr` |
| `internal/checker/tests/exhaustive_match_test.go` | **New.** Tests from the requirements doc (Cases 1–20) |

## Test Plan

Tests should be added incrementally as each phase is completed. The test cases from the
requirements document map to phases as follows:

| Phase | Test Cases |
|---|---|
| Phase 1–2 (core coverage) | Cases 1, 2, 3, 7, 8, 13 |
| Phase 3 (error messages) | Cases 2, 4, 6, 8, 10, 11 (verify message format) |
| Phase 4 (integration) | All cases — verify errors appear in `inferModuleTypesAndErrors` |
| Phase 5 (tuples) | Cases 14, 15 |
| Phase 6 (booleans) | Cases 4, 5, 12 |
| Phase 7 (nested) | Cases 16, 17, 18, 19, 20 |
| Redundancy (R7) | Cases 9, 12 |
| Guards (R6) | Case 10 |

## Risks and Mitigations

**Risk:** `MatchedUnionMembers` may not be populated for all structural pattern scenarios
(e.g., when the target is not a union type).
**Mitigation:** For non-union structural targets, the pattern trivially covers the single
type. Only rely on `MatchedUnionMembers` when the target is a union.

**Risk:** Extractor patterns don't currently store which union member they matched in an
easily accessible way.
**Mitigation:** After unification, the `customMatcher`'s param type identifies the variant.
Extract this during `computeCaseCoverage` by walking the extractor's resolved type to find
the `[Symbol.customMatcher]` method and reading its param type.

**Risk:** `TypeRefType` comparison may fail if the same type alias is referenced through
different paths.
**Mitigation:** Compare by `TypeAlias` pointer identity (same alias declaration) rather
than by name string.
