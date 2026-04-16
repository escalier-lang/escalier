# Plan A: Expand at the TypeRefType match site, not in a catch-all retry

## Goal

Replace the catch-all retry loop at the bottom of `unifyPruned` (lines 1189-1203)
with explicit `TypeRefType` handling earlier in the function. This makes expansion
predictable: it only happens for the type kind that needs it, and only once per
unification call.

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

1. **TypeRefType + TypeRefType (different alias)** — Lines 506-510 have a TODO and
   do nothing, so these fall through.
2. **TypeRefType + any non-TypeRef concrete type** — e.g. `TypeRefType` vs
   `ObjectType`, `PrimType`, `LitType`, `UnionType`, etc. There is no case that
   handles "one side is a TypeRefType, the other is something else."
3. **Unrecognized type combinations** — Any pair of types that doesn't match the
   listed cases (e.g. `NullType` + `ObjectType`). These should be genuine
   unification errors.

In cases 1 and 2, the retry loop expands the TypeRefType and retries. In case 3,
expansion doesn't change either type, so the retry doesn't fire and the function
falls through to the final `CannotUnifyTypesError`.

## Plan

### Step 1: Add explicit TypeRefType expansion cases

Add two new cases in `unifyPruned`, placed after the existing TypeRefType + TypeRefType
(same alias) case at line 503 and replacing the do-nothing different-alias case at
lines 505-511:

```go
// | TypeRefType, _ -> expand t1 (covers both TypeRefType+TypeRefType
//   with different aliases and TypeRefType+any other type)
if ref1, ok := t1.(*type_system.TypeRefType); ok {
    expandedT1, expandErrors := c.expandTypeRef(ctx, ref1)
    if len(expandErrors) > 0 {
        return expandErrors
    }
    return c.unifyWithDepth(ctx, expandedT1, t2, depth+1)
}
// | _, TypeRefType -> expand t2
if ref2, ok := t2.(*type_system.TypeRefType); ok {
    expandedT2, expandErrors := c.expandTypeRef(ctx, ref2)
    if len(expandErrors) > 0 {
        return expandErrors
    }
    return c.unifyWithDepth(ctx, t1, expandedT2, depth+1)
}
```

Note: `expandTypeRef` is used instead of `ExpandType` because we only want to
resolve the type alias and substitute type parameters — not recursively expand the
entire result. The recursive expansion happens naturally through re-entering
`unifyWithDepth`. This also means `expandTypeRef` bypasses the
`expandTypeRefsCount`, `skipTypeRefsCount`, and `insideKeyOfTarget` counters in
`ExpandType`'s visitor — this is intentional, since the expansion depth is
controlled by re-entering `unifyWithDepth` rather than by the visitor's counters.

### Step 2: Handle nominal TypeRefTypes

`ExpandType` currently returns `nil` for nominal `ObjectType`s (expand_type.go:375-379),
meaning nominal types are not expanded. The explicit cases above use `expandTypeRef`
which does not check for nominality. We need to ensure nominal types are handled:

- When both sides are `TypeRefType` with the same alias (already handled at
  lines 451-503) — this is fine, nominality is irrelevant.
- When one side is a `TypeRefType` pointing to a nominal type — `expandTypeRef` will
  return the nominal `ObjectType`, and `unifyPruned` will enter the `ObjectType`
  case which already checks `obj.Nominal`. No change needed.

### Step 3: Remove the retry loop

Delete the retry loop at lines 1189-1203:

```go
// DELETE:
retry := false
expandedT1, _ := c.ExpandType(ctx, t1, 1)
if expandedT1 != t1 {
    t1 = expandedT1
    retry = true
}
expandedT2, _ := c.ExpandType(ctx, t2, 1)
if expandedT2 != t2 {
    t2 = expandedT2
    retry = true
}

if retry {
    return c.unifyWithDepth(ctx, t1, t2, depth+1)
}
```

The final `CannotUnifyTypesError` at lines 1205-1208 remains — it is still the
correct fallback for types that genuinely cannot be unified.

### Step 4: Audit other ExpandType calls in unify.go

There are two other `ExpandType` calls in `unifyPruned` that are unrelated to the
retry loop and should be preserved:

- **Line 345-346**: `KeyOfType` expansion — both sides are `KeyOfType`, expand their
  inner types. Keep as-is.
- **Line 1055**: Union + ObjectType — expands each union member to check if it's an
  `ObjectType`. Keep as-is.

### Step 5: Evaluate whether `maxUnifyDepth` can be reduced or removed

After this change, the depth counter in `unifyWithDepth` still increments on each
TypeRefType expansion. But now it only increments for genuine expansions (not
spurious pointer-inequality retries). This means:

- The depth should stay bounded by the actual nesting depth of type aliases, not by
  how many times `ExpandType` happens to allocate new objects.
- For non-recursive types, the depth should correspond to the length of the longest
  type alias chain.
- `maxUnifyDepth = 50` is likely still more than enough but should be kept as a
  safety net until Plan B (visited-set) is implemented.

After Plan B, `maxUnifyDepth` can be removed entirely since the visited-set will
handle cycle detection.

## Testing strategy

1. **Run the full test suite** — This is a behavioral refactor; all existing tests
   should pass without changes.
2. **Verify depth behavior** — Add debug logging (temporary) to count the maximum
   depth reached during the test suite. Before this change, some tests likely hit
   depth 20-50. After this change, the max depth should drop significantly since
   spurious retries are eliminated.
3. **Test TypeRefType + TypeRefType (different alias)** — Write a test with two
   different type aliases that resolve to the same structural type and verify they
   unify:
   ```
   type Foo = { x: number }
   type Bar = { x: number }
   val a: Foo = ...
   val b: Bar = a  // should succeed
   ```
4. **Test TypeRefType + concrete type** — Write a test where one side is a type
   alias and the other is an inline type:
   ```
   type Foo = { x: number }
   val a: Foo = { x: 5 }  // TypeRefType vs ObjectType
   ```

## Risks

- **Missed cases**: If there are non-`TypeRefType` types that relied on the retry
  loop for expansion (e.g. a `CondType` or `IndexType` that somehow reaches
  `unifyPruned` unexpanded), those would now fail with `CannotUnifyTypesError`
  instead of being retried. Mitigation: run the full test suite and React type
  tests to catch these.
- **Double expansion**: The TypeRefType + TypeRefType (different alias) case
  expands only `t1`. If the expanded form of `t1` is itself a `TypeRefType`, it
  will be expanded again on re-entry. This is correct behavior but relies on the
  depth counter to prevent cycles with recursive aliases (which will be properly
  handled by Plan B).
