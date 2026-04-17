# Taming Recursion: Implementation Plans

Three plans, designed to be implemented in order. See [research.md](research.md)
for the full issue inventory.

## Plan index

- **[Plan A: Expand at the TypeRefType match site](plan_a_typeref_expansion.md)** —
  Replace the catch-all retry loop with an iterative expansion loop in
  `unifyPruned` that calls a new `unifyMatched` function for case-matching and
  `tryExpandTypeRef` for TypeRef expansion. Adds an `IsTypeParam` flag to
  `TypeAlias` to prevent expanding type parameter references. Prerequisite for
  Plan B.

- **[Plan B: Visited-set / seen-pairs memoization](plan_b_visited_set.md)** —
  Add cycle detection via visited sets to both `Unify` and `ExpandType`. Enables
  recursive type aliases and removes both the `maxUnifyDepth` limit and
  `maxExpansionRetries` loop bound.

- **[Plan C: Verify and harden unification recursion](plan_c_verify_unify_recursion.md)** —
  Audit seen-set propagation in Tuple/Array paths, validate the three pathological
  scenarios from issue #1, add regression tests, and clean up residual workaround
  code.

## Issue coverage

Which [research.md](research.md) issues each plan addresses:

| Issue | Description | Plan(s) |
|-------|-------------|---------|
| #1 | Unification retry loop creates unbounded recursion | A (replaces retry loop with iterative expansion), B (removes depth limit and expansion loop bound), C (validates fix, regression tests) |
| #2 | Recursive type aliases cannot be expanded | B (visited set in ExpandType enables cycle detection) |
| #3 | `skipTypeRefsCount` suppresses expansion inside structural types | B (visited set makes this less critical; evaluated for removal in step 6) |
| #4 | `insideKeyOfTarget` prevents nested keyof expansion | B (visited set may subsume this; evaluated for removal in step 6) |
| #5 | `getMemberType` expansion loop relies on pointer identity | — (deferred; low complexity to fix after Plan B provides a visited-set primitive — add an iteration limit or use the `expandSeen` set) |
| #6 | `ExpandType` and `unifyPruned` use different recursion strategies | A (unify uses `tryExpandTypeRef` for TypeRefs and `ExpandType(t, 0)` only for non-TypeRef types), B (shared cycle detection primitive) |
| #7 | No visited-set or seen-pairs mechanism | B (adds visited sets to both Unify and ExpandType) |
| #8 | Package loading cycle prevention | — (working correctly, no changes needed) |
| #9 | Library file discovery visited set | — (working correctly, no changes needed) |
