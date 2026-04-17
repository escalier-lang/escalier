# Taming Recursion: Implementation Plans

Three plans, designed to be implemented in order. See [research.md](research.md)
for the full issue inventory.

## Plan index

- **[Plan A: Expand at the TypeRefType match site](plan_a_typeref_expansion.md)** —
  **Implemented (PR #451).** Replaced the catch-all retry loop with a
  `noMatchError` sentinel in `unifyMatched` and an expansion loop in `unifyPruned`.
  `canExpandTypeRef` (a bool predicate) decides whether TypeRef expansion should be
  attempted; it blocks nominal types, `IsTypeParam` aliases, and transitive
  self-referential cycles (A→B→A). Actual expansion uses `ExpandType(ctx, t, 1)`
  for fresh copies, then delegates to `unifyWithDepth` with `depth+1`. Adds an
  `IsTypeParam` flag to `TypeAlias`, checked by `canExpandTypeRef`.

- **[Plan B: Visited-set / seen-pairs memoization](plan_b_visited_set.md)** —
  **Implemented.** Added `unifySeen` (visited set for unification) and `expandSeen`
  (visited set for type expansion) with co-inductive cycle detection. Threaded seen
  sets through all internal unification and expansion functions. Removed
  `maxUnifyDepth = 50`; increased `maxExpansionRetries` from 10 to 100 as a
  defensive safety net. Recursive type aliases (`List<T>`, `Tree<T>`, `Json`,
  mutually recursive types, cross-alias cycles) now work correctly. Step 6
  (evaluate removing ad-hoc counters) deferred to Plan C.

- **[Plan C: Verify and harden unification recursion](plan_c_verify_unify_recursion.md)** —
  Audit seen-set propagation in Tuple/Array paths, validate the three pathological
  scenarios from issue #1, add regression tests, clean up residual workaround code,
  and evaluate removing the ad-hoc counters deferred from Plan B Step 6.

## Issue coverage

Which [research.md](research.md) issues each plan addresses:

| Issue | Description | Plan(s) |
|-------|-------------|---------|
| #1 | Unification retry loop creates unbounded recursion | **A (done)**: replaces retry loop with iterative expansion + `noMatchError` sentinel; **B (done)**: removes `maxUnifyDepth`, increases `maxExpansionRetries` to 100 as safety net; C (validates fix, regression tests) |
| #2 | Recursive type aliases cannot be expanded | **B (done)**: `expandSeen` visited set in ExpandType enables cycle detection; recursive type aliases now work |
| #3 | `skipTypeRefsCount` suppresses expansion inside structural types | **B (deferred to C)**: visited set makes this less critical; evaluation for removal deferred to Plan C |
| #4 | `insideKeyOfTarget` prevents nested keyof expansion | **B (deferred to C)**: visited set may subsume this; evaluation for removal deferred to Plan C |
| #5 | `getMemberType` expansion loop relies on pointer identity | — (deferred; low complexity to fix after Plan B provides a visited-set primitive — add an iteration limit or use the `expandSeen` set) |
| #6 | `ExpandType` and `unifyPruned` use different recursion strategies | **A (done)**: unify uses `canExpandTypeRef` predicate + `ExpandType(t, 1)` for TypeRefs and `ExpandType(t, 0)` for non-TypeRef types; **B (done)**: shared cycle detection primitives (`unifySeen`, `expandSeen`) |
| #7 | No visited-set or seen-pairs mechanism | **B (done)**: adds `unifySeen` to Unify and `expandSeen` to ExpandType |
| #8 | Package loading cycle prevention | — (working correctly, no changes needed) |
| #9 | Library file discovery visited set | — (working correctly, no changes needed) |
