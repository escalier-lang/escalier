# Taming Recursion: Issue Inventory

This document catalogs the known issues, workarounds, and fragile patterns related
to recursion in `internal/checker/`. The goal is to build a complete picture of the
problems before designing solutions.

---

## 1. Unification retry loop creates unbounded recursion

**Location:** `unify.go` (previously lines 1189-1203, now replaced)

**Status: Resolved by Plans A, B, and C.**

When `unifyPruned` failed to unify two types, it called `ExpandType(ctx, t, 1)` on
both sides and retried if either expansion produced a different object. The problem
was that `ExpandType` allocates new type objects on every call, so the pointer
comparison `expandedT1 != t1` was always true for any expandable type — even when
the expansion was structurally identical.

This caused unbounded recursion for:
- `TypeRefType` with `TypeAlias` set (e.g. `HTMLAttributeAnchorTarget`, `Array<any>`)
- `TupleType` with rest spreads that expand to include the full `Array` interface
- Large `ObjectType` instances (e.g. React SVG attributes with 200+ properties)
  where nested type references keep producing new objects

**Resolution:** Plan A replaced the retry loop with a `noMatchError` sentinel in
`unifyMatched` and a three-tier expansion loop in `unifyPruned`. Plan B added
`unifySeen`/`expandSeen` visited-set cycle detection and removed `maxUnifyDepth`.
Plan C validated all three scenarios with regression tests (18 tests in
`unify_recursion_test.go`), audited depth propagation at all call sites, and
reduced `maxExpansionRetries` from 100 to 10.

---

## 2. Recursive type aliases cannot be expanded

**Location:** `expand_type.go:343-345`

`ExpandType` is eager: when it encounters a `TypeRefType`, it resolves the alias and
recursively expands the result. This means a recursive type alias like
`type List<T> = { head: T, tail: List<T> | null }` would expand forever.

The TODO acknowledges this: _"implement once TypeAliases have been marked as
recursive."_ Until type aliases carry a `Recursive` flag (or equivalent), there is
no way to detect the cycle and stop.

This blocks support for linked lists, trees, JSON values, and other recursive data
structures that are common in real programs.

**Resolved by Plan B:** `expandSeen` visited set in `ExpandType` detects cycles
dynamically, making a static `Recursive` flag unnecessary for correctness.

---

## 3. `skipTypeRefsCount` suppresses expansion inside structural types

**Location:** `expand_type.go:48, 110-112, 147-149, 348-351`

**Status: Evaluated in Plan C, kept as optimization.**

When the visitor enters a `FuncType` or `ObjectType`, it increments
`skipTypeRefsCount`; on exit, it decrements it. While the counter is positive, all
`TypeRefType` nodes are skipped.

This is a coarse heuristic: it prevents expanding type references that appear in
function parameter/return positions or inside object type literals. The intent is to
avoid unnecessary (and potentially infinite) expansion of types that will be unified
structurally. Plan C evaluated this counter and concluded it is an optimization hint
(avoids unnecessary work), not a safety mechanism. The `expandSeen` visited set
handles cycle detection regardless. Kept as-is.

---

## 4. `insideKeyOfTarget` prevents nested `keyof` expansion

**Location:** `expand_type.go:50, 214-216, 222-223, 431-437`

**Status: Evaluated in Plan C, deferred removal (TODO #455).**

When expanding the target of a `keyof` type, the visitor increments
`insideKeyOfTarget`. While positive, any nested `keyof` short-circuits and returns
unexpanded. This prevents cycles like `keyof keyof T` from recursing.

Plan C confirmed this guard overlaps with `expandSeen` cycle detection but deferred
removal due to risk — it affects `expandSeenKey`, `expandTypeWithConfig`, and
multiple call sites. The TODO(#455) comments were updated to reflect this finding.

---

## 5. `getMemberType` expansion loop relies on pointer identity for termination

**Location:** `expand_type.go:481-517`

`getMemberType` contains a `for` loop that repeatedly calls `ExpandType(ctx, objType, 1)`
until it reaches a terminal type (`ObjectType`, `NamespaceType`, `IntersectionType`,
`TypeVarType`, `MutabilityType`) or expansion produces no change (`expandedType == objType`).

Termination depends on:
- The set of terminal types being complete (any new type kind would need to be added)
- `ExpandType` eventually returning the same pointer when no further expansion is
  possible

The comment at lines 485-486 notes a specific risk: `globalThis` points back to the
global namespace, so `NamespaceType` must be checked *before* expansion to avoid an
infinite loop. This is a fragile ordering dependency.

---

## 6. `ExpandType` and `unifyPruned` use different recursion strategies that don't compose

**Status: Resolved by Plans A and B.**

`ExpandType` uses three independent counters (`expandTypeRefsCount`,
`skipTypeRefsCount`, `insideKeyOfTarget`) while `unifyPruned` uses a depth counter
(`depth`). These mechanisms were designed independently and interact in subtle ways.

**Resolution:** Plan A made expansion targeted by type kind. Plan B added shared
cycle-detection primitives (`unifySeen` for unification, `expandSeen` for
expansion). The `depth` parameter is now diagnostic-only (not a termination
mechanism). The ad-hoc counters remain as optimization hints but are no longer
the safety mechanisms they once were.

---

## 7. No visited-set or seen-pairs mechanism for type cycles

**Status: Resolved by Plan B.**

Previously, neither `ExpandType` nor `Unify` tracked which `(t1, t2)` pairs or
type alias expansions had already been attempted.

**Resolution:** Plan B added `unifySeen` (co-inductive visited set for unification)
and `expandSeen` (visited set with caching for expansion). These are the standard
technique used in TypeScript, OCaml, and other production type checkers. Plan C
verified the seen sets are correctly propagated through all call sites.

---

## 8. Package loading has its own cycle prevention (separate from type recursion)

**Location:** `package_registry.go:9-98`, `infer_import.go:338-341, 803-816`

Package loading uses a sentinel namespace to mark packages as "in-progress" and
detect `A -> B -> A` import cycles. This is a well-contained solution that works
independently of the type-level recursion issues.

Noted here for completeness — it is not a problem, but it is an example of a
visited-set pattern that could inform solutions for the type-level issues.

---

## 9. Library file discovery uses a visited set (working correctly)

**Location:** `prelude.go:60-66, 112-150`

`discoverESLibFiles` / `loadLibFilesRecursive` passes a `visited` map through
recursive calls to prevent reprocessing the same `.d.ts` files. This works
correctly and is another example of the visited-set pattern.

---

## Rankings

Issues 8 and 9 are working correctly and are excluded from these rankings.

### Current status summary (post Plans A, B, C)

| Issue | Status |
|-------|--------|
| #1 — Unify retry loop / depth limit | **Resolved.** Plans A + B + C. |
| #2 — Recursive type aliases cannot be expanded | **Resolved.** Plan B. |
| #3 — `skipTypeRefsCount` suppression | **Evaluated, kept.** Plan C — optimization hint. |
| #4 — `insideKeyOfTarget` guard | **Evaluated, deferred.** Plan C — TODO #455. |
| #5 — `getMemberType` expansion loop | **Open.** Low priority, could use `expandSeen`. |
| #6 — Non-composing recursion strategies | **Resolved.** Plans A + B. |
| #7 — No visited-set / seen-pairs mechanism | **Resolved.** Plan B. |

### Remaining work

- **#4 (`insideKeyOfTarget`)**: Remove if `expandSeen` alone handles nested keyof
  recursion. Requires careful testing of keyof expansion paths (TODO #455).
- **#5 (`getMemberType` expansion loop)**: Harden with an iteration limit or
  `expandSeen`. Low priority — fragile but currently working.
