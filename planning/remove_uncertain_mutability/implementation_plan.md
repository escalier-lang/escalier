# Remove `mut?` and Gate `mut self` Access on Immutable Receivers

## Context

Two pieces of the type system are doing more work than they earn:

- **`mut?` (uncertain mutability)** is a deferred-decision wrapper. Open objects, constructor returns, and method-body `self` parameters get tagged `mut?` and a later visitor (`RemoveUncertainMutabilityVisitor`) settles them. With #499 + lifetimes, the answer is always knowable at the construction site — the user either wrote `mut`, wrote a mutable annotation, or didn't (immutable). Deferral buys nothing and adds switches across the checker.
- **`mut self` methods** are still listed on immutable receivers and silently callable. This is the wrong default: such methods should be invisible — same property-not-found path as a typo — both at call time and in LSP completions.

Both are doable independently, but Phase 1 (the gate) is the smaller and reviewable-first piece. Phase 2 (`mut?` removal) builds cleanly on top of it.

## Phase 1 — Gate `mut self` access on receiver mutability ✅ Landed

### Property lookup filter (as built)

A single `memberElemHidden(elem, receiverMut)` helper in [internal/checker/expand_type.go](../../internal/checker/expand_type.go) decides per-element visibility, called from each of the four element-resolution loops (`lazyMemberLookup`, the three branches of `getObjectAccess` for property/string-literal/symbol keys). An `ReceiverIsDefinitelyMutable(t)` helper (exported so the LSP shares it) handles the entry decision.

- `MethodElem` with `MutSelf == true` → skipped when the receiver is not definitely `mut`. ✓
- `GetterElem`, `MethodElem` with `MutSelf` false/nil, plain properties → always visible. ✓
- `SetterElem` → **not hidden**. The plan originally called for hiding setters in `AccessWrite` mode, but doing so produced a 3-error cascade for `immutableObj.setterProp = value`: `Unknown property` + the existing `Cannot mutate immutable type` + a follow-on `cannot be assigned to undefined`. The existing `CannotMutateImmutableError` already enforces write gating with a clearer message, so setter hiding adds nothing. (The LSP `completionsFromObjectType` *does* still hide setters from completion suggestions on immutable receivers — completion shows what the user can successfully do, while the checker keeps the better error message.)

When all elements at a key are filtered out, the lookup falls through to the existing `UnknownPropertyError` / `KeyNotFoundError` path. No new error variant.

To thread the receiver mutability through unwrappings without losing it, `getMemberType` and `completionsFromType` each wrap an `…Impl(…, receiverMut bool)` form that takes the flag as an explicit parameter. The `MutabilityType` switch case ORs `receiverMut || t.Mutability == MutabilityMutable` so a definite `mut` wrapper on an inner layer upgrades an inherited-immutable flag.

The per-member cache key (`memberCacheKey`) was extended with a `receiverMut bool` field so mutable vs immutable lookups don't share a slot. Not-found results aren't cached, which avoids stale-hide pitfalls.

### Edge cases (resolved)

- **`mut?` receiver** — treated as immutable in `ReceiverIsDefinitelyMutable` (only `MutabilityMutable` returns true). The open-object hatch is `receiverMutForElems := receiverMut || objType.Open` in both the checker and LSP paths — open objects under inference only ever hold `PropertyElem`s and `RestSpreadElem`s by construction (per `newOpenObjectWithProperty`), so the filter is a no-op for them. This avoids the ordering hazard the plan flagged.
- **Type-var receivers** — `ReceiverIsDefinitelyMutable` recurses into `tv.Constraint`. Unconstrained type vars return false. Tested.
- **`ArrayConstraint` resolution** — `getArrayConstraintPropertyAccess` calls `getMemberTypeImpl` with `receiverMut=true` so `push`/`unshift`/etc. resolve during constraint-driven inference (the eventual parameter type ends up wrapped in `mut Array<…>` if any mutating method is recorded).
- **`UpdateMethodMutability` in `prelude.go` flipped its default.** The old code defaulted `MutSelf=true` for every method on every `*Constructor` type, then overrode entries from `mutabilityOverrides`. The new code only sets `MutSelf` for methods that appear in the overrides table; everything else is left `nil`. This avoids hiding non-mutating methods on classes like `Function` whose `mutabilityOverrides` entry is empty/missing, but as a side effect, `Date.setHours(...)` and similar mutating methods on classes not in the overrides table are now visible on immutable receivers. Tracked in TODO(#500) at the top of `mutabilityOverrides` — needs entries for `Date`, `Promise`, `Error`, etc.

### LSP completion (as built)

`completionsFromType` mirrors the checker structure — it dispatches to `completionsFromTypeImpl` with the receiver-mutability flag computed by `checker.ReceiverIsDefinitelyMutable`. Mut-self methods and setters are hidden from suggestions on immutable receivers; getters at the same key still surface the property as readable. Tests added in [completion_test.go](../../cmd/lsp-server/completion_test.go): `TestMemberCompletionHidesMutSelfOnImmutableReceiver`, `TestMemberCompletionShowsMutSelfOnMutableReceiver`.

### Tests landed

In [mut_prefix_test.go](../../internal/checker/tests/mut_prefix_test.go):

- `ImmutableInstance_CannotCallMutSelfMethod` — `Counter(0).tick()` → property-not-found.
- `MutInstance_CanCallMutSelfMethod` — `mut Counter(0).tick()` → succeeds.
- `MutInstance_CanBindMutSelfMethod` — `val t = c.tick` on a `mut` instance succeeds (replaces the symmetric coverage lost when the immutable `inc = obj1.increment` line was removed from the `objects_with_self` fixture).
- `ImmutableMap_CannotClear`, `ImmutableSet_CannotAdd` — collection cases.
- `TypeVarReceiver_ImmutableConstraint_CannotCallMutSelfMethod` — `<T: Counter>(t: T) -> t.tick()` errors with `Callee is not callable: undefined`.
- `TypeVarReceiver_MutConstraint_CanCallMutSelfMethod` — `<T: mut Counter>` succeeds.

Fixtures touched:

- `fixtures/objects/error.txt` — back to two errors (the `Cannot mutate` + type-mismatch pair) after dropping setter hiding.
- `fixtures/class_with_fluent_mutating_methods/lib/index.esc` — added `mut` prefix at construction site since the chained methods are mut-self.
- `fixtures/objects_with_self/lib/objects_with_self.esc` — removed `val inc = obj1.increment` (binding a mut-self method on an immutable receiver no longer resolves; covered positively by the new bind test above).

### Follow-ups

- **TODO(#500)** — populate `mutabilityOverrides` for `Date`, `Promise`, `Error`, and other classes whose methods mutate the receiver. Without this, mut-self gating silently misses these classes.
- The `objType.Open` short-circuit assumes open objects only hold `PropertyElem`/`RestSpreadElem`. Currently true; if methods ever get added to open objects, revisit.

## Phase 2 — Remove `mut?`

### Audit every `mut?` creation site

Grep `MutabilityUncertain` and any `&type_system.MutabilityType{...}` literal that doesn't specify `MutabilityMutable`. Expected hits:

- [internal/checker/expand_type.go:1445 `newOpenObjectWithProperty`](../../internal/checker/expand_type.go#L1445) — open object widening (the wrapper is constructed at line 1458 with `MutabilityUncertain`). The load-bearing case.
- [internal/checker/infer_expr.go:315, :391, :547](../../internal/checker/infer_expr.go) — three more `MutabilityUncertain` constructions in expression inference.
- [internal/checker/infer_module.go:578](../../internal/checker/infer_module.go#L578) — class-decl path.
- [internal/checker/expand_type.go:745, :948, :1208, :1613](../../internal/checker/expand_type.go) — sites that *check for* `MutabilityUncertain` (key-type unwrapping during member access). These need their `MutabilityUncertain` branch deleted, but they still need to handle the bare-and-mut cases.

For each site, choose one of:

1. **Drop the wrapper.** Right answer for nearly every constructor return after #499 — the result is immutable unless the caller wrote `mut`.
2. **Replace with definite `mut` (`MutabilityMutable`).** Right answer for `mut self` method bodies — `self` is genuinely mutable inside the body.
3. **Defer to the open-object finalization pass** (below) — only the open-object case needs this.

### Open-object finalization pass

This is the load-bearing piece. Today, an unannotated parameter `p` with body `p.x = 1` gets widened to `mut? { x: ... }`, and `RemoveUncertainMutabilityVisitor` later promotes it to `mut` because some property's `Written` flag is set.

Replacement: after each function body completes inference (in `inferFuncBody` or wherever the body's effects are summarized), walk the parameter types. For each open `ObjectType`:

- If **any** field has `Written == true` → wrap that param's type in a definite `mut` wrapper.
- Else → leave unwrapped (immutable).

This is a single forward pass, runs immediately after body inference, and produces definite types that flow into generalization. The `Written` flag plumbing already exists ([expand_type.go:1486+ `markPropertyWritten`](../../internal/checker/expand_type.go#L1486)) — we just shift the decision earlier.

### Delete dead machinery

- `MutabilityUncertain` constant at [internal/type_system/types.go:2165](../../internal/type_system/types.go#L2165).
- `RemoveUncertainMutabilityVisitor` + `removeUncertainMutability` at [internal/checker/unify.go:2351-2371](../../internal/checker/unify.go#L2351). **Call sites of `removeUncertainMutability` are inside `unify.go` itself** ([lines 2075, 2121](../../internal/checker/unify.go#L2075)), not in `generalize.go` — verify before deleting.
- `unwrapMutability` at [internal/checker/unify.go:2421](../../internal/checker/unify.go#L2421). Only stripped `mut?`. Call sites: [unify.go:236, :243](../../internal/checker/unify.go#L236) and [infer_expr.go:1143](../../internal/checker/infer_expr.go#L1143). Replace each with direct `*MutabilityType` pattern-matching where a strip is still needed.
- The `mutWrapper.Mutability != type_system.MutabilityUncertain` check at [unify.go:2467](../../internal/checker/unify.go#L2467) becomes always-true (i.e. drop the conditional).
- All `mut.Mutability == MutabilityUncertain` branches in switches across `expand_type.go` (lines 745, 948, 1208, 1613), `iterable.go`, `infer_lifetime.go`.
- The `?` print in `printMutabilityType`.

### Generalization interaction

Note: `removeUncertainMutability` is **not currently called from `generalize.go`** — its only call sites are in `unify.go`. Still audit [internal/checker/generalize.go](../../internal/checker/generalize.go) for any direct `MutabilityUncertain` checks or assumptions that a `mut?` wrapper may appear on the input. After Phase 2, generalize sees only definite types.

### Tests and fixtures

- Mass-update fixtures: every `"mut? Foo"` in expected-type strings becomes `"Foo"` (or `"mut Foo"` for the open-object case where the body mutates). Likely 30–50 fixtures across [infer_class_decl_test.go](../../internal/checker/tests/infer_class_decl_test.go), [lifetime_test.go](../../internal/checker/tests/lifetime_test.go), and others.
- New tests for the open-object finalization pass:
  - Param accessed only via reads → no `mut` wrapper added.
  - Param has at least one property write → `mut` wrapper added.
  - Nested case: param's property is itself an object whose inner field is written.

## Phase 3 — Sweep, snapshots, verification

- `UPDATE_SNAPS=true go test ./...` — refresh and review snapshot diffs.
- Re-enable any test cases left disabled during Phase 1 development.
- Final sanity: `grep -r "mut?" internal/` and `grep -r "MutabilityUncertain" internal/` both return zero hits.

## Risks and unknowns

- **Open-object finalization order.** If a function body's open-object decision needs to be visible to a *caller's* unification before the caller is checked, single-pass-after-body finalization is fine. If there's a mutual-recursion case where the caller's checks run first, the order may need shuffling — same machinery the lifetimes pass already uses for lifetime params.
- **Argument unwrapping in `inferCallExpr`.** [infer_expr.go:1143](../../internal/checker/infer_expr.go#L1143) calls `unwrapMutability(at)` on every argument when the callee is a `TypeVarType` — this strips `mut?` from synthetic argument types so the inferred function signature is clean (per the comment at [:1136](../../internal/checker/infer_expr.go#L1136)). After Phase 2 there is no `mut?` to strip; verify the surrounding code still produces clean inferred signatures.
- **`infer_lifetime.go:466` reference.** The doc comment for `stripMutabilityWrapper` cross-references `unwrapMutability`; rewrite or remove the comment when `unwrapMutability` is deleted.
- **Print-format churn.** Every test fixture mentioning `mut?` will need a sweep — mostly mechanical but loud in PR diff.

## Sizing

| Phase                                        | Effort       | Status / Risk                                                  |
| -------------------------------------------- | ------------ | -------------------------------------------------------------- |
| Phase 1 (mut-self gate + LSP)                | ~half-day    | ✅ Landed. Open-object hazard sidestepped via `objType.Open` short-circuit. |
| Phase 2 (`mut?` removal + finalization pass) | 2–4 days     | Open-object finalization is the unknown                        |
| Phase 3 (fixture sweep + new tests)          | ~half-day    | Mechanical                                                     |
| **Total**                                    | **3–5 days** | Phase 1 done; Phase 2 next.                                    |

## Verification

1. `go test ./...` green after each phase. ✓ (Phase 1)
2. `grep -r "mut?" internal/` and `grep -r "MutabilityUncertain" internal/` both return zero hits after Phase 2.
3. LSP completion at `immutablePoint.` does not list any `mut self` methods; at `mutablePoint.` it does. ✓ (Phase 1)
4. The receiver-mutability tests previously commented out in [mut_prefix_test.go](../../internal/checker/tests/mut_prefix_test.go) are re-enabled and passing, plus type-var-receiver coverage. ✓ (Phase 1)
5. Generated JS for existing programs is byte-identical (the change is purely type-level). ✓ (Phase 1; the only fixture JS deltas are removals of `obj1.increment.bind(obj1)` for the binding line that was deleted — no other diffs.)
