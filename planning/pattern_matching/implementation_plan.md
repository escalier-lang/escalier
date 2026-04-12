# Pattern Matching Implementation Plan

This plan implements the requirements from [requirements.md](requirements.md) to fix
pattern matching unification for structural patterns against nominal types and unions.

## Phase 1: Activate the `IsPatMatch` flag

**Requirements:** R6

**What:** The `Context` struct already has an `IsPatMatch` field
([checker.go:45](../../internal/checker/checker.go#L45)) but it is never set to `true`.
Set it to `true` when unifying patterns in `inferMatchExpr`.

**Files to change:**
- [infer_expr.go](../../internal/checker/infer_expr.go) — In `inferMatchExpr` (line 1349),
  create a pattern-matching context before the `Unify` call:

```go
// Before:
unifyErrors := c.Unify(caseCtx, patternType, targetType)

// After:
patMatchCtx := caseCtx
patMatchCtx.IsPatMatch = true
unifyErrors := c.Unify(patMatchCtx, patternType, targetType)
```

**Testing:** Existing tests should continue to pass since `IsPatMatch` is not yet read
anywhere.

## Phase 2: Relax the nominal check in pattern-matching mode

**Requirements:** R1, R2, R6

**What:** In `Unify`'s object-vs-object branch, when `IsPatMatch` is true and the pattern
(t1) is structural while the target (t2) is nominal, skip the nominal ID check and fall
through to structural property matching.

**Files to change:**
- [unify.go](../../internal/checker/unify.go) — Modify the nominal check at lines 863-873:

```go
// Before:
if obj2.Nominal {
    if obj1.ID != obj2.ID {
        return []Error{&CannotUnifyTypesError{T1: obj1, T2: obj2}}
    }
}

// After:
if obj2.Nominal {
    if ctx.IsPatMatch && !obj1.Nominal {
        // In pattern-matching mode, allow structural patterns to match
        // against nominal types by falling through to property comparison.
    } else if obj1.ID != obj2.ID {
        return []Error{&CannotUnifyTypesError{T1: obj1, T2: obj2}}
    }
}
```

**What this enables:** A structural pattern like `{x, y}` can now unify against a nominal
type like `Point` by comparing properties structurally. The existing open-vs-closed
unification logic handles the property matching — the pattern's object type is open (from
`inferPattern`), and the nominal type is closed, so the `open-vs-closed` branch
(lines 994-1005) applies: shared properties are unified, and closed-only properties
(those in the nominal type but not the pattern) are copied to the open type. Pattern-only
properties that don't exist on the nominal type won't produce an error in this branch,
which needs to be addressed (see Phase 3).

**Testing:** Write tests for Case 1 (structural destructuring of a nominal type) and
Case 6 (partial match on a subset of fields).

## Phase 3: Validate pattern fields exist on the target type

**Requirements:** R1

**What:** After the open-vs-closed unification in pattern-matching mode, ensure that
every field in the pattern actually exists on the target type. The current open-vs-closed
logic (lines 994-1005) only iterates the closed type's keys, so pattern fields that don't
exist on the nominal type are silently ignored.

**Approach:** When `IsPatMatch` is true and the pattern (t1) is open while the target (t2)
is closed, after the standard open-vs-closed unification, check that every key in the
pattern exists in the target. If a pattern field is not found, report an error.

**Files to change:**
- [unify.go](../../internal/checker/unify.go) — Add a check after the open-vs-closed
  block (around line 1005):

```go
if obj1.Open && !obj2.Open {
    // ... existing unification logic ...

    // In pattern-matching mode, verify all pattern fields exist on the target
    if ctx.IsPatMatch {
        for _, key := range keys1 {
            if _, ok := namedElems2[key]; !ok {
                errors = append(errors, &PropertyNotFoundError{
                    Property: key,
                    Object:   obj2,
                })
            }
        }
    }
    return errors
}
```

**Testing:** Write test for Case 4 (pattern field `{foo}` not present in any union member).

## Phase 4: Handle structural patterns against union types

**Requirements:** R3, R4, R8

**What:** The existing `_, UnionType` branch in `Unify` (lines 1377-1395) uses a
probe-then-commit strategy that tries each union member and commits to the first match.
For pattern matching, we need different behavior: try the pattern against *every* union
member, collect all matches, and produce union bindings from the matching members.

The existing `UnionType, ObjectType` branch (lines 1197-1359) already does something
similar for destructuring — it collects field types across all union members and creates
union types. However, it currently requires the destructured fields to come from the
*union side* (t1), not the *pattern side* (t2). For pattern matching, the pattern is t1
(structural object) and the target is t2 (union).

**Approach:** Add a new branch in `Unify` for the `ObjectType, UnionType` case when
`IsPatMatch` is true. This branch should:

1. For each union member, check if the member has all fields from the pattern (structural
   compatibility check).
2. Collect the types of each pattern field across all matching members.
3. Record which union members were matched (for future exhaustiveness checking).
4. For each pattern field, unify the pattern's type variable with the union of that
   field's types across matching members.
5. If no members match, report an error.

**Files to change:**
- [unify.go](../../internal/checker/unify.go) — Add a new case before the generic
  `_, UnionType` handler (around line 1377). This should be gated on `ctx.IsPatMatch`
  and `t1` being an `ObjectType` while `t2` is a `UnionType`:

```go
// | ObjectType, UnionType (pattern matching) -> ...
if ctx.IsPatMatch {
    if patObj, ok := t1.(*type_system.ObjectType); ok {
        if union, ok := t2.(*type_system.UnionType); ok {
            return c.unifyPatternWithUnion(ctx, patObj, union)
        }
    }
}
```

The `unifyPatternWithUnion` helper should:

```go
func (c *Checker) unifyPatternWithUnion(
    ctx Context,
    pat *type_system.ObjectType,
    union *type_system.UnionType,
) []Error {
    // 1. Collect pattern field names and their type variables
    patFields := collectNamedElems(pat)

    // 2. For each union member, check if it has ALL pattern fields.
    //    Union members may be nominal — extract their properties structurally.
    matchingFieldTypes := map[ObjTypeKey][]Type{}
    matchedMembers := []type_system.Type{}
    for _, member := range union.Types {
        memberFields := collectNamedElems(member)

        allMatch := true
        for key := range patFields {
            if _, ok := memberFields[key]; !ok {
                allMatch = false
                break
            }
        }
        if allMatch {
            matchedMembers = append(matchedMembers, member)
            for key := range patFields {
                matchingFieldTypes[key] = append(
                    matchingFieldTypes[key], memberFields[key],
                )
            }
        }
    }

    // 3. If no members matched, error
    if len(matchedMembers) == 0 {
        return []Error{&CannotUnifyTypesError{T1: pat, T2: union}}
    }

    // 4. Store matched members on the pattern's ObjectType for future
    //    exhaustiveness checking. See "Future: Exhaustiveness Checking" below.
    pat.MatchedUnionMembers = matchedMembers

    // 5. Unify each pattern field's type variable with the union of matched types
    errors := []Error{}
    for key, patType := range patFields {
        fieldUnion := type_system.NewUnionType(nil, matchingFieldTypes[key]...)
        unifyErrors := c.Unify(ctx, patType, fieldUnion)
        errors = append(errors, unifyErrors...)
    }
    return errors
}
```

**Note:** When collecting named elements from union members, the members may be nominal
object types (class instances). Since we're in pattern-matching mode, we read their
properties structurally regardless of nominality.

**Additional change required:**
- [types.go](../../internal/type_system/types.go) — Add a `MatchedUnionMembers []Type`
  field to `ObjectType`. This field is nil outside of pattern matching and is populated
  by `unifyPatternWithUnion` to record which union members this pattern covers. This
  field is not used for type equality or unification — it is metadata for downstream
  passes (e.g. exhaustiveness checking).

**Testing:** Write tests for Cases 1, 7, 8, and 9.

## Phase 5: Validate match targets for extractor patterns

**Requirements:** R5

**What:** When all patterns in a match expression are extractor patterns (e.g.
`Color.RGB(...)`, `Color.Hex(...)`), the target expression's type should be validated
as a valid instance type. If the target is a constructor/static object (has callable or
newable signatures), it should be rejected.

**Approach:** In `inferMatchExpr`, after inferring the target type, check whether the
patterns are extractor patterns and whether the target type is a constructor rather than
an instance.

**Files to change:**
- [infer_expr.go](../../internal/checker/infer_expr.go) — Add validation in
  `inferMatchExpr` after inferring the target type (after line 1328):

```go
// Check if target type is a constructor when patterns expect instances
targetObjType, isObj := type_system.Prune(targetType).(*type_system.ObjectType)
if isObj {
    hasCallableOrNewable := false
    for _, elem := range targetObjType.Elems {
        switch elem.(type) {
        case *type_system.CallableElem, *type_system.NewableElem:
            hasCallableOrNewable = true
        }
    }
    if hasCallableOrNewable {
        for _, matchCase := range expr.Cases {
            if _, ok := matchCase.Pattern.(*ast.ExtractorPat); ok {
                errors = append(errors, &ConstructorUsedAsMatchTargetError{
                    TargetType: targetType,
                })
                break
            }
        }
    }
}
```

- [errors.go](../../internal/checker/errors.go) (or equivalent) — Add a new error type
  `ConstructorUsedAsMatchTargetError`.

**Testing:** Write tests for Cases 2 and 3.

## Phase 6: Comprehensive testing

**Requirements:** R7

**What:** Run the full test suite and write tests for all 9 test cases from the
requirements doc. Verify no regressions in existing pattern matching behavior.

**Files to change:**
- [infer_test.go](../../internal/checker/tests/infer_test.go) — Add new test cases to
  `TestMatchExprInference` covering:
  - Case 1: Structural destructuring of a nominal union
  - Case 2: Enum constructor as match target (should error)
  - Case 3: Correct enum instance matching (should succeed)
  - Case 4: Structural pattern matching no union member (should error)
  - Case 5: Mixed nominal and structural patterns
  - Case 6: Partial match on subset of fields
  - Case 7: Shared fields across union members produce union bindings
  - Case 8: Shared field with same type across all union members
  - Case 9: Pattern field not present in any union member (should error)

**Verification:** Run `go test ./...` and confirm all tests pass including existing
pattern matching tests.

## Summary of changes by file

| File | Changes |
|------|---------|
| `internal/checker/infer_expr.go` | Set `IsPatMatch = true` for pattern unification; add constructor target validation |
| `internal/checker/unify.go` | Relax nominal check when `IsPatMatch`; add pattern field existence check; add `unifyPatternWithUnion` for `ObjectType` vs `UnionType` in pattern mode |
| `internal/checker/errors.go` | Add `ConstructorUsedAsMatchTargetError` (and `PropertyNotFoundError` if not already present) |
| `internal/type_system/types.go` | Add `MatchedUnionMembers []Type` field to `ObjectType` |
| `internal/checker/tests/infer_test.go` | Add test cases 1-9 |

## Dependencies between phases

```
Phase 1 (activate flag)
  |
  +---> Phase 2 (relax nominal check)
  |       |
  |       +---> Phase 3 (validate pattern fields)
  |
  +---> Phase 4 (pattern vs union)
  |
  +---> Phase 5 (constructor validation)
  |
  +---> Phase 6 (comprehensive testing) — after all above
```

Phases 2/3, 4, and 5 are independent of each other and can be worked on in parallel
after Phase 1 is complete.

## Future: Exhaustiveness Checking

This plan is designed to not block future exhaustiveness checking. The key design decision
is in Phase 4: `unifyPatternWithUnion` records which union members each structural pattern
matched via `ObjectType.MatchedUnionMembers`. This enables a future exhaustiveness checker
to work as follows:

1. After all match arms have been type-checked, collect the set of matched union members
   from each arm:
   - **Structural object patterns**: read `MatchedUnionMembers` from the pattern's
     `ObjectType` (populated by Phase 4).
   - **Instance patterns** (`Point {x, y}`): the matched member is the nominal type
     resolved from the class name — already available via the pattern's inferred type.
   - **Extractor patterns** (`Color.RGB(r, g, b)`): the matched member is determined by
     the extractor's `Symbol.customMatcher` — the class type is already resolved during
     `inferPattern`.
   - **Wildcard/identifier patterns**: match all remaining members.

2. Compute the union of all matched member sets across all arms.

3. Compare against the full set of union members in the target type. If any members are
   uncovered, report a non-exhaustive match warning/error.

For non-union targets (e.g. matching a single nominal type), exhaustiveness reduces to
checking that at least one arm is irrefutable (wildcard, identifier, or a structural
pattern that matches all fields).

No additional fields or structural changes should be needed beyond what this plan
introduces — `MatchedUnionMembers` is the bridge between unification and exhaustiveness.
