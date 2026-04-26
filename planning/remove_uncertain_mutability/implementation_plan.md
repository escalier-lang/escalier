# Remove `mut?` and Gate `mut self` Access on Immutable Receivers

## Context

Two pieces of the type system are doing more work than they earn:

- **`mut?` (uncertain mutability)** is a deferred-decision wrapper. Open objects, constructor returns, and method-body `self` parameters get tagged `mut?` and a later visitor (`RemoveUncertainMutabilityVisitor`) settles them. With #499 + lifetimes, the answer is always knowable at the construction site — the user either wrote `mut`, wrote a mutable annotation, or didn't (immutable). Deferral buys nothing and adds switches across the checker.
- **`mut self` methods** are still listed on immutable receivers and silently callable. This is the wrong default: such methods should be invisible — same property-not-found path as a typo — both at call time and in LSP completions.

Both are doable independently, but Phase 1 (the gate) is the smaller and reviewable-first piece. Phase 2 (`mut?` removal) builds cleanly on top of it.

## Phase 1 — Gate `mut self` / setter access on receiver mutability

### Property lookup filter

The element resolution loop appears in **multiple branches** of [internal/checker/expand_type.go](../../internal/checker/expand_type.go) — at [lines 1058, 1124, 1226, 1360](../../internal/checker/expand_type.go) — depending on whether the receiver is an ObjectType, TypeRefType, IntersectionType, or pattern-key access. The cleanest implementation is a small helper that wraps the per-elem decision (returning the elem if visible, nil if hidden) and is called from each of those four loops, plus a `receiverIsMutable(t type_system.Type) bool` helper. Apply it during element resolution:

- `MethodElem` with `MutSelf == true` → skip when the receiver is not definitely `mut`.
- `SetterElem` → skip when the receiver is not definitely `mut` (but only in `AccessWrite` mode — see edge cases).
- `GetterElem`, `MethodElem` with `MutSelf` false/nil, plain properties → always visible.

When all elements at a key are filtered out, fall through to the existing `UnknownPropertyError` / `KeyNotFoundError` path. **Do not introduce a new error variant** — collapsing the "hidden" and "doesn't exist" paths is the user's stated requirement.

### Edge cases

- **`mut?` receiver** (until Phase 2 lands) — treat as immutable for filtering, consistent with how `RemoveUncertainMutabilityVisitor` settles after #499. After Phase 2 these don't exist. Important: open objects produced by `newOpenObjectWithProperty` are wrapped in `mut?` and still pre-finalization during body inference — Phase 1 must not start filtering on these prematurely (a method invoked on an open object during inference would otherwise vanish before the body completes). Either restrict the gate to non-open object types, or land Phase 2 first to avoid the ordering hazard.
- **Type-var receivers** — when the receiver is a `TypeVarType` with a constraint, look at the constraint's mutability wrapper. If unconstrained, treat as immutable so generic code can't sneak past the gate.
- **Setter access mode** — the existing `AccessMode` parameter on `getMemberType` (`AccessRead` vs `AccessWrite`) already splits the read/write paths. Audit the call sites to confirm: setters only need filtering on `AccessWrite`; methods only on call-style access.
- **Generic methods on parametric classes** — `Self<T>` carries mutability from its instantiation. Verify the propagation already works (it should, since `MutabilityType` wraps the whole `TypeRefType`).

### LSP completion

[cmd/lsp-server/completion.go:479-497](../../cmd/lsp-server/completion.go#L479-L497) iterates `obj.Elems` and emits a completion item per element (the `MethodElem` branch is on line 497). Apply the same `receiverIsMutable` filter here. Existing tests live in [completion_test.go](../../cmd/lsp-server/completion_test.go) — extend with mut-self cases. ~30 minutes.

### Tests

Add to [internal/checker/tests/mut_prefix_test.go](../../internal/checker/tests/mut_prefix_test.go) — re-enable the cases that are currently commented out:

- `Counter(0).tick()` → `UnknownPropertyError("tick")` (not a separate mut error).
- `mut Counter(0).tick()` → succeeds.
- `Map`/`Set`: `m.clear()` and `s.add(1)` on immutable receivers → property-not-found.
- Type-var receiver: a generic function calling `t.mutMethod()` where `T extends SomeMutClass` (no `mut` constraint) → property-not-found.
- LSP completion test (find existing pattern): `obj.` lists `mut self` methods only when `obj` is `mut`.

### Critical files

- [internal/checker/expand_type.go](../../internal/checker/expand_type.go) — filter logic.
- LSP completion path (TBD on first grep).
- [internal/checker/tests/mut_prefix_test.go](../../internal/checker/tests/mut_prefix_test.go) — re-enable hidden-method tests.
- A handful of existing fixtures likely call `mut` methods on what is now an immutable receiver and need a `mut` prefix at the construction site.

### Sizing

Half a day, plus an hour or two for the type-var edge case if it turns out to need real work.

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
- **Generic methods on parametric classes.** A method on `Self<T>` whose mutability is inherited from the instantiation site needs a careful look during Phase 1.
- **Setter access mode.** Setters as "mut self" elements during write access vs. read access. Likely already handled by `AccessMode`, but worth verifying.
- **Print-format churn.** Every test fixture mentioning `mut?` will need a sweep — mostly mechanical but loud in PR diff.

## Sizing

| Phase                                        | Effort       | Risk                                                           |
| -------------------------------------------- | ------------ | -------------------------------------------------------------- |
| Phase 1 (mut-self gate + LSP)                | ~half-day    | Low; type-var edge case may add 1–2 hours; open-object ordering is a risk if Phase 2 doesn't land first |
| Phase 2 (`mut?` removal + finalization pass) | 2–4 days     | Open-object finalization is the unknown                        |
| Phase 3 (fixture sweep + new tests)          | ~half-day    | Mechanical                                                     |
| **Total**                                    | **3–5 days** | Either land Phase 1 first (with a Phase 1.5 step to handle open-object receivers conservatively), or land Phase 2 first and Phase 1 on top |

## Verification

1. `go test ./...` green after each phase.
2. `grep -r "mut?" internal/` and `grep -r "MutabilityUncertain" internal/` both return zero hits after Phase 2.
3. LSP completion at `immutablePoint.` does not list any `mut self` methods; at `mutablePoint.` it does.
4. The receiver-mutability tests currently commented out in [mut_prefix_test.go](../../internal/checker/tests/mut_prefix_test.go) are re-enabled and passing.
5. Generated JS for existing programs is byte-identical (the change is purely type-level).
