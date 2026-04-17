# Plan A: Expand at the TypeRefType match site, not in a catch-all retry

**Status: Implemented (PR #451)**

## Goal

Replace the catch-all retry loop at the bottom of `unifyPruned` with explicit
`TypeRefType` handling. This makes expansion predictable: it only happens for the
type kinds that need it, retrying as needed within a bounded loop to peel through
alias chains.

## Background

Previously, when `unifyPruned` failed to match any explicit case, it fell through to
a retry loop that called `ExpandType(ctx, t, 1)` on both sides. If either side
expanded (detected by pointer inequality), it recursed into `unifyWithDepth` with
`depth + 1`. This had two problems:

1. `ExpandType` always allocates new objects, so `expandedT1 != t1` is always true
   for expandable types, even when the result is structurally identical.
2. Non-`TypeRefType` types can also trigger expansion (e.g. `CondType`, `KeyOfType`,
   `IndexType`), but these are already handled in their own cases within
   `ExpandType`'s visitor. The retry loop re-expands types that don't need it.

## Analysis of what reached the retry loop

The retry loop was reached when neither `t1` nor `t2` matched any of the explicit
cases in `unifyPruned`. After reviewing all cases, the scenarios that fell through
to the retry were:

1. **TypeRefType + TypeRefType (different alias)** — The old code had a TODO and
   did nothing, so these fell through.
2. **TypeRefType + any non-TypeRef concrete type** — e.g. `TypeRefType` vs
   `ObjectType`, `PrimType`, `LitType`, `UnionType`, etc. There was no case that
   handled "one side is a TypeRefType, the other is something else."
3. **TypeOfType + any type** — `TypeOfType` (e.g. `typeof obj`) had no explicit case
   in `unifyPruned` and relied on the retry loop's `ExpandType` to resolve it.
4. **Unrecognized type combinations** — Any pair of types that didn't match the
   listed cases (e.g. `NullType` + `ObjectType`). These should be genuine
   unification errors.

In cases 1-3, the retry loop expanded the types and retried. In case 4, expansion
didn't change either type, so the retry didn't fire and the function fell through
to the final `CannotUnifyTypesError`.

## Issues discovered and resolved

### Issue 1: `expandTypeRef` fails on non-alias TypeRefTypes

`expandTypeRef` returned an `UnknownTypeError` when the TypeRefType referred to
something that isn't a type alias (e.g. enum variant `RGB`). The `canExpandTypeRef`
helper was added, returning `false` when resolution fails so the caller falls
through gracefully.

### Issue 2: Stack overflow from recursive `unifyWithDepth` calls

Calling `unifyWithDepth(ctx, expandedT1, t2, depth+1)` for each TypeRefType
expansion added a full `unifyWithDepth` → `unifyPruned` frame to the call stack.
For types with many properties (React SVG elements: 200+ properties), each property
may be a TypeRefType that triggers another expansion round.

**Resolution:** The `noMatchError` sentinel approach means expansion only happens
when no case matched — not on every failed match. The recursion depth is
proportional to the alias chain depth (typically 1-3 levels), not to the number of
type properties.

### Issue 3: TypeOfType also relies on the retry loop

`TypeOfType` (e.g. `typeof obj`) had no explicit case in `unifyPruned`. The retry
loop expanded it via `ExpandType`, which resolves the `typeof` expression to the
value's type.

**Resolution:** Non-TypeRef expansion uses `ExpandType(ctx, t, 0)` (count=0 skips
TypeRef expansion). Only retries when the type actually changed (pointer-equality
check). `ExpandType(t, 0)` returns the same pointer when `t` contains nothing
expandable at count=0 (e.g. an ObjectType with only TypeRefType properties).

### Issue 4: Type parameter references expanded to their constraints

`TypeRefType` is used to represent both type alias references (`type Foo = ...`)
and type parameter references (`<T>`). Expanding `TypeRefType(T)` resolves the
TypeAlias and returns the constraint `unknown`, destroying the type parameter's
identity.

**Example failure** (`ClassWithGenericMethod` test):

```escalier
class Box(value: number) {
    value,
    getValue<T>(self, default: T) -> number | T {
        if self.value != 0 { return self.value }
        else { return default }
    }
}
```

**Resolution (two parts):**

1. Added `IsTypeParam bool` to `TypeAlias` struct. Set to `true` at all type
   parameter creation sites. `canExpandTypeRef` checks this flag and returns
   `false` for type parameter aliases.

2. Expansion runs AFTER case-matching (via `noMatchError` sentinel). The union
   member matching and same-alias comparison handle `T vs T` before expansion
   has a chance to destroy the parameter's identity.

### Issue 5: `bind()` side effects leak through the expansion loop

When `unifyMatched` calls `bind(t1, t2)` for a TypeVarType, `bind` both returns
errors and sets `Instance` as a side effect.

**Resolution:** The `noMatchError` sentinel approach handles this naturally.
`bind` errors are authoritative (not `noMatchError`), so `unifyPruned` returns
them immediately without attempting expansion.

### Issue 6: Same-alias TypeRefTypes expanded after type-arg mismatch

When `Array<number>` vs `Array<string>` failed in the same-alias case, the
expansion loop could expand both to structural ObjectTypes and retry incorrectly.

**Resolution:** Same as Issue 5 — the same-alias case returns authoritative
errors (not `noMatchError`), so expansion is never attempted.

### Issue 7: Self-referential type parameter aliases cause infinite expansion loop

`infer_stmt.go:buildTypeParams` creates self-referential aliases for type
parameters: type param `A` gets `TypeAlias{Type: TypeRefType(A)}`. Each call to
`canExpandTypeRef`/expansion "expands" `A` into a new copy of `TypeRefType(A)`.

**Resolution:** `canExpandTypeRef` walks the alias chain with a visited set to
detect both direct cycles (`A→A`) and transitive cycles (`A→B→A`).

### Issue 8: Type param constraint chains blocked by `IsTypeParam`

The original plan called for removing the `IsTypeParam` check from
`canExpandTypeRef`, relying only on the self-referentiality check (Issue 7) and
expansion-after-matching ordering (Issue 4).

**Resolution:** The `IsTypeParam` check was kept in `canExpandTypeRef`. It
provides an additional safety guard. If constraint chain expansion (`C: B, B: A,
A: string`) is needed in the future, this can be revisited.

### Issue 9: Expanded TypeAlias types share mutable pointers

Directly using `typeAlias.Type` from a resolved alias passes a shared pointer
into the `TypeAlias` struct. Unification can mutate the shared type via
TypeVarType binding, corrupting the type alias for all future uses.

**Resolution:** `canExpandTypeRef` is a predicate only (returns `bool`). When it
returns `true`, expansion is done by `ExpandType(ctx, t, 1)` which creates fresh
copies via the visitor, then delegates to `unifyWithDepth` with `depth+1`.

### Issue 10: Nominal types need ExpandType for pattern matching

`canExpandTypeRef` blocks expansion of nominal ObjectTypes (classes) to prevent
bypassing nominal identity checks. But pattern matching against nominal types
(`match p { {foo} => foo }`) requires expanding the TypeRefType to access the
ObjectType's properties.

**Resolution:** Added a last-resort expansion path: after `canExpandTypeRef`
returns false and `ExpandType(ctx, t, 0)` also fails, fall through to
`ExpandType(ctx, t, 1)` for any remaining TypeRefTypes. Nominal semantics are
enforced downstream in the ObjectType vs ObjectType case in `unifyMatched`.

## What was implemented

### Key architectural changes

1. **`noMatchError` sentinel**: `unifyMatched` (the extracted case-matching
   function) returns `[]Error{&noMatchError{}}` when no case handles the type
   combination. `unifyPruned` uses `isNoMatch(errors)` to distinguish "no case
   matched" (safe to try expansion) from "a case matched but failed"
   (authoritative error). This subsumes the originally planned Issue 5
   (TypeVarType guard) and Issue 6 (same-alias guard) — both produce
   authoritative errors, not `noMatchError`.

2. **`canExpandTypeRef` predicate** (in `expand_type.go`): Returns `bool` only.
   Blocks expansion for:
   - `nil` TypeAlias (unresolvable)
   - `IsTypeParam` aliases (type parameter placeholders)
   - Nominal ObjectTypes (classes)
   - Self-referential aliases via transitive cycle detection (A→B→A) using a
     visited set

3. **`IsTypeParam` flag on `TypeAlias`**: Set at all type parameter creation
   sites (7 locations: `infer_func.go`, `infer_stmt.go`, `infer_module.go` x2,
   `generalize.go`, `infer_type_ann.go` x2). Checked by `canExpandTypeRef`.

4. **Three-tier expansion in `unifyPruned`**:
   - Tier 1: `canExpandTypeRef` → `ExpandType(t, 1)` → `unifyWithDepth(depth+1)`
     for expandable TypeRefTypes
   - Tier 2: `ExpandType(t, 0)` + pointer-equality check + Prune → `continue`
     for non-TypeRef expandable types (TypeOfType, etc.)
   - Tier 3: `ExpandType(t, 1)` + pointer-equality check →
     `unifyWithDepth(depth+1)` as last resort for nominal/refused TypeRefTypes

### Files changed

- `internal/checker/unify.go` — Extracted `unifyMatched` from `unifyPruned`,
  added `noMatchError` sentinel, added three-tier expansion loop, removed the
  old catch-all retry loop and do-nothing different-alias TypeRefType case
- `internal/checker/expand_type.go` — Added `canExpandTypeRef` predicate with
  transitive cycle detection
- `internal/type_system/types.go` — Added `IsTypeParam bool` to `TypeAlias`
  struct and `NamespaceType.Accept` copy
- `internal/checker/generalize.go` — Set `IsTypeParam: true`
- `internal/checker/infer_func.go` — Set `IsTypeParam: true`
- `internal/checker/infer_module.go` — Set `IsTypeParam: true` (2 sites)
- `internal/checker/infer_stmt.go` — Set `IsTypeParam: true`
- `internal/checker/infer_type_ann.go` — Set `IsTypeParam: true` (2 sites)
- `internal/checker/tests/infer_class_decl_test.go` — Added
  `TestNominalClassUnificationTerminates` regression test

### Differences from the original plan

| Aspect | Original plan | Actual implementation |
|--------|---------------|----------------------|
| Guard for Issue 5 (TypeVarType) | Explicit `TypeVarType` check before expansion | Subsumed by `noMatchError` sentinel — `bind` returns authoritative errors |
| Guard for Issue 6 (same-alias) | Explicit `sameTypeRef` check before expansion | Subsumed by `noMatchError` sentinel — same-alias case returns authoritative errors |
| `IsTypeParam` in `canExpandTypeRef` | Removed per Issue 8 (self-ref check sufficient) | Kept — provides additional safety |
| Self-referentiality check | Direct cycle only (`A→A`) | Transitive cycle detection (`A→B→A`) via visited set |
| Non-TypeRef expansion | Used `continue` without Prune | Added Prune after expansion to resolve TypeVarTypes before re-entering `unifyMatched` |

## Remaining work (Plans B and C)

- `maxUnifyDepth` and `maxExpansionRetries` remain as safety nets. Plan B's
  visited set will make them unnecessary.
- `canExpandTypeRef`'s transitive cycle detection handles alias chains but not
  arbitrary mutual recursion through structural types. Plan B's visited set
  handles this properly.
- The `depth+1` recursion for TypeRefType expansion means `maxUnifyDepth` is
  still load-bearing for deep alias chains. Plan B removes this dependency.
