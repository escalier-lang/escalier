# Taming Recursion: Issue Inventory

This document catalogs the known issues, workarounds, and fragile patterns related
to recursion in `internal/checker/`. The goal is to build a complete picture of the
problems before designing solutions.

---

## 1. Unification retry loop creates unbounded recursion

**Location:** `unify.go` (previously lines 1189-1203, now replaced)

**Status: Addressed by Plan A (PR #451).**

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

**Previous workaround:** A hard depth limit (`maxUnifyDepth = 50`) that returned
`CannotUnifyTypesError` when exceeded. This was a blunt instrument — it could reject
valid programs if they happened to need more than 50 expansion steps.

**Current state:** Plan A replaced the retry loop with a `noMatchError` sentinel in
`unifyMatched` and a three-tier expansion loop in `unifyPruned`. Expansion only
happens when no case matched (not on every failed match), and recursion depth is
proportional to alias chain depth (typically 1-3), not property count.
`maxUnifyDepth` and `maxExpansionRetries` remain as safety nets until Plan B adds
visited-set cycle detection.

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

**Plan B** will add a visited set to `ExpandType` that detects cycles dynamically,
making the `Recursive` flag unnecessary for correctness.

---

## 3. `skipTypeRefsCount` suppresses expansion inside structural types

**Location:** `expand_type.go:48, 110-112, 147-149, 348-351`

When the visitor enters a `FuncType` or `ObjectType`, it increments
`skipTypeRefsCount`; on exit, it decrements it. While the counter is positive, all
`TypeRefType` nodes are skipped.

This is a coarse heuristic: it prevents expanding type references that appear in
function parameter/return positions or inside object type literals. The intent is to
avoid unnecessary (and potentially infinite) expansion of types that will be unified
structurally. However, it also prevents expansion in cases where it would be needed,
and the interaction between `skipTypeRefsCount` and `expandTypeRefsCount` is not
obvious — both counters must be consulted when deciding whether to expand a
`TypeRefType`.

---

## 4. `insideKeyOfTarget` prevents nested `keyof` expansion

**Location:** `expand_type.go:50, 214-216, 222-223, 431-437`

When expanding the target of a `keyof` type, the visitor increments
`insideKeyOfTarget`. While positive, any nested `keyof` short-circuits and returns
unexpanded. This prevents cycles like `keyof keyof T` from recursing.

The guard works for this specific case, but it is another ad-hoc counter that only
addresses one source of recursion. It doesn't compose with the other counters in a
principled way, and it's not clear whether there are other type-level operators
(e.g. indexed access types, conditional types) that could produce similar cycles.

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

**Status: Partially addressed by Plan A (PR #451).**

`ExpandType` uses three independent counters (`expandTypeRefsCount`,
`skipTypeRefsCount`, `insideKeyOfTarget`) while `unifyPruned` uses a depth counter
(`depth`). These mechanisms were designed independently and interact in subtle ways.

**Previous state:** `unifyPruned` called `ExpandType` with a fresh count of `1` on
each retry, so the expansion counters reset every time — the depth limit in `Unify`
was the only thing preventing infinite retries.

**Current state:** After Plan A, `unifyPruned` calls `ExpandType` only when the
`noMatchError` sentinel indicates no case matched, and only for specific type kinds
(TypeRefTypes via `canExpandTypeRef`, non-TypeRef types via count=0, nominal
last-resort via count=1). The depth limit is still the safety net, but expansion is
much more targeted. Plan B will add a shared visited-set primitive.

---

## 7. No visited-set or seen-pairs mechanism for type cycles

Neither `ExpandType` nor `Unify` tracks which `(t1, t2)` pairs or type alias
expansions have already been attempted. This is the standard technique (used in
TypeScript, OCaml, and other production type checkers) for handling recursive types:

- In unification, if a pair `(t1, t2)` is encountered a second time, assume success
  (co-inductive reasoning).
- In expansion, if a type alias is encountered while already being expanded, stop
  and return the reference unexpanded.

Without this, every recursion-prevention mechanism must be an ad-hoc counter or
depth limit, and adding support for recursive type aliases will require a
fundamental change to the expansion strategy.

**Note:** Plan A added a small visited set inside `canExpandTypeRef` for detecting
transitive alias cycles (A→B→A), but this is scoped to the predicate only — it
does not provide general cycle detection for unification or expansion. Plan B
addresses this fully.

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

### By seriousness (impact on users and correctness)

| Rank | Issue | Why |
|------|-------|-----|
| 1 | #2 — Recursive type aliases cannot be expanded | **Blocks entire feature.** Users cannot define linked lists, trees, JSON types, or any recursive data structure. This is a missing capability, not just a bug. |
| 2 | #7 — No visited-set / seen-pairs mechanism | **Root cause of most other issues.** The absence of this primitive forces every recursion site to invent its own ad-hoc guard. It also makes #2 unsolvable without an architectural change. |
| 3 | #1 — ~~Unify retry loop / depth limit~~ | **Addressed by Plan A.** The retry loop has been replaced with targeted expansion. `maxUnifyDepth` remains as a safety net until Plan B. |
| 4 | #6 — ~~Non-composing recursion strategies~~ | **Partially addressed by Plan A.** Expansion is now targeted by type kind. Plan B will add a shared cycle-detection primitive. |
| 5 | #5 — `getMemberType` expansion loop | **Fragile but currently working.** Adding a new type kind without updating the terminal-type checks would silently introduce an infinite loop. The `globalThis` ordering dependency is a maintenance hazard. |
| 6 | #3 — `skipTypeRefsCount` suppression | **Over-suppresses expansion.** Can prevent needed expansion inside structural types, potentially causing unification to fail when it shouldn't. The effect is hard to observe because it manifests as "unification didn't try hard enough" rather than an explicit error. |
| 7 | #4 — `insideKeyOfTarget` guard | **Narrow scope, works for its case.** Only affects `keyof keyof T` patterns. Low impact because these patterns are uncommon and the guard correctly handles the ones that do occur. |

### By complexity to address

| Rank | Issue | Why |
|------|-------|-----|
| 1 | #7 — No visited-set / seen-pairs mechanism | **Architectural change.** Requires threading shared state through both `ExpandType` and `Unify`, which currently have no shared context beyond the `Checker` receiver. Every call site that creates a new visitor or calls `ExpandType` independently would need to participate. Design decisions include: what constitutes identity for a type pair, whether to use co-inductive assumption (assume success on re-encounter) or just stop expansion, and how to handle type arguments in cycle detection. |
| 2 | #2 — Recursive type aliases cannot be expanded | **Depends on #7.** Even after adding a visited set, there are design questions: should recursive aliases be detected statically (mark `TypeAlias.Recursive` during definition) or dynamically (detect during expansion)? Static detection requires analyzing the alias graph, which is its own problem for mutually recursive types. Dynamic detection is simpler but means every expansion pays the cost of cycle tracking. |
| 3 | #6 — Non-composing recursion strategies | **Partially addressed by Plan A.** The remaining work is adding a shared cycle-detection primitive (Plan B), which is effectively the same work as #7. |
| 4 | #1 — Unify retry loop / depth limit | **Addressed by Plan A.** The remaining safety-net removal (dropping `maxUnifyDepth`) depends on Plan B. |
| 5 | #3 — `skipTypeRefsCount` suppression | **Requires understanding when expansion is actually needed.** The counter exists because expanding type refs inside object/function types was causing problems, but the exact failure modes aren't documented. Removing or refining it requires identifying those failure modes and ensuring the replacement handles them. |
| 6 | #5 — `getMemberType` expansion loop | **Incremental fix.** Could be hardened by adding a small iteration limit or a seen-set of types already attempted. The terminal-type list could be made exhaustive by switching to a default-break pattern instead of listing specific types. |
| 7 | #4 — `insideKeyOfTarget` guard | **Already works.** If a visited-set mechanism (#7) is added, this counter becomes redundant and can be removed. Until then, it's fine as-is. |

### Observations

- Issues #7, #2, and #6 form a cluster: the missing visited-set primitive (#7) is
  the root cause, recursive type aliases (#2) are the most visible symptom, and the
  non-composing strategies (#6) are the ongoing maintenance cost. Addressing #7
  would make #2 tractable and #6 largely moot.

- Issue #1 has been addressed by Plan A. The remaining work is removing the safety
  nets (`maxUnifyDepth`, `maxExpansionRetries`) once Plan B provides proper cycle
  detection.

- Issues #3, #4, and #5 are lower priority and could be cleaned up incrementally,
  especially after #7 provides a principled cycle-detection mechanism that subsumes
  the ad-hoc counters.
