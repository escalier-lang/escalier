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

## Phase 2 — Remove `mut?` ✅ Landed

### Audit every `mut?` creation site

> **Note:** the line numbers in the audit below are a pre-implementation snapshot — they were captured when this plan was written and have since drifted. Symbol names (e.g. `newOpenObjectWithProperty`, `markPropertyWritten`) are the durable references; locate the code by symbol, not line.

Grep `MutabilityUncertain` and any `&type_system.MutabilityType{...}` literal that doesn't specify `MutabilityMutable`. Expected hits:

- [internal/checker/expand_type.go `newOpenObjectWithProperty`](../../internal/checker/expand_type.go) — open object widening (the wrapper is constructed inside this function with `MutabilityUncertain`). The load-bearing case.
- [internal/checker/infer_expr.go](../../internal/checker/infer_expr.go) — three more `MutabilityUncertain` constructions in expression inference.
- [internal/checker/infer_module.go](../../internal/checker/infer_module.go) — class-decl path.
- [internal/checker/expand_type.go](../../internal/checker/expand_type.go) — sites that *check for* `MutabilityUncertain` (key-type unwrapping during member access). These need their `MutabilityUncertain` branch deleted, but they still need to handle the bare-and-mut cases.

For each site, choose one of:

1. **Drop the wrapper.** Right answer for nearly every constructor return after #499 — the result is immutable unless the caller wrote `mut`.
2. **Replace with definite `mut` (`MutabilityMutable`).** Right answer for `mut self` method bodies — `self` is genuinely mutable inside the body.
3. **Defer to the open-object finalization pass** (below) — only the open-object case needs this.

### Open-object finalization pass

This is the load-bearing piece. Today, an unannotated parameter `p` with body `p.x = 1` gets widened to `mut? { x: ... }`, and `RemoveUncertainMutabilityVisitor` later promotes it to `mut` because some property's `Written` flag is set.

Replacement: after each function body completes inference (in `inferFuncBody` or wherever the body's effects are summarized), walk the parameter types. For each open `ObjectType`:

- If **any** field has `Written == true` → wrap that param's type in a definite `mut` wrapper.
- Else → leave unwrapped (immutable).

This is a single forward pass, runs immediately after body inference, and produces definite types that flow into generalization. The `Written` flag plumbing already exists in `markPropertyWritten` ([expand_type.go](../../internal/checker/expand_type.go)) — we just shift the decision earlier.

### Delete dead machinery

(Symbols below; the pre-implementation snapshot's line numbers have been omitted since they no longer match.)

- `MutabilityUncertain` constant in [internal/type_system/types.go](../../internal/type_system/types.go).
- `RemoveUncertainMutabilityVisitor` + `removeUncertainMutability` in [internal/checker/unify.go](../../internal/checker/unify.go). **Call sites of `removeUncertainMutability` are inside `unify.go` itself**, not in `generalize.go` — verify before deleting.
- `unwrapMutability` in [internal/checker/unify.go](../../internal/checker/unify.go). Only stripped `mut?`. Call sites: in `unify.go` and [infer_expr.go `inferCallExpr`](../../internal/checker/infer_expr.go). Replace each with direct `*MutabilityType` pattern-matching where a strip is still needed.
- The `mutWrapper.Mutability != type_system.MutabilityUncertain` check in `unify.go` becomes always-true (i.e. drop the conditional).
- All `mut.Mutability == MutabilityUncertain` branches in switches across `expand_type.go`, `iterable.go`, `infer_lifetime.go`.
- The `?` print in `printMutabilityType`.

### Generalization interaction

Note: `removeUncertainMutability` is **not currently called from `generalize.go`** — its only call sites are in `unify.go`. Still audit [internal/checker/generalize.go](../../internal/checker/generalize.go) for any direct `MutabilityUncertain` checks or assumptions that a `mut?` wrapper may appear on the input. After Phase 2, generalize sees only definite types.

### What actually landed (post-implementation notes)

- **`removeUncertainMutability` was not pure deletion — it was renamed to `rebuildContainers` and kept.** The old visitor had two effects: stripping `mut?` (its declared purpose) AND rebuilding containers as it walked via `Accept` (an incidental side effect). The container-rebuild turned out to be load-bearing — three generic-method tests (`ClassWithGenericMethod`, `ObjectWithGenericMethods`, `GenericClassWithGenericMethods`) fail without it. Empirically verified: deleting `rebuildContainers` and re-running tests reproduces the failures. The new function preserves only the rebuild behavior, called at the same `FromBinding` sites in `bind()`. See the comment at [internal/checker/unify.go:2069](../../internal/checker/unify.go#L2069).
- **`finalizeOpenObject` (in `generalize.go`) replaces `RemoveUncertainMutabilityVisitor` for open-object resolution.** Walks param open-object trees post-body-inference; if any property has `Written == true` (or recurses into a nested open object that does), wraps the param in `mut`. Invariant documented in the function's docstring: open-object property values are always TypeVars, never pre-wrapped in `MutabilityType`.
- **`MutabilityUncertain` constant fully removed.** `Mutability` is now effectively a single-value enum (`MutabilityMutable = "!"`). Kept as a typed const in case more variants are added later.
- **`printMutabilityType` lost the `mut?` case** and now panics on unknown mutability values via `default`.
- **Phase 2 follow-ups not done in this pass**: legacy code-modernization lints (rangeint, minmax, QF1003) flagged in `unify.go` and `generalize.go` are pre-existing and out of scope.

### Tests and fixtures

- Mass-update fixtures: every `"mut? Foo"` in expected-type strings becomes `"Foo"` (or `"mut Foo"` for the open-object case where the body mutates). Likely 30–50 fixtures across [infer_class_decl_test.go](../../internal/checker/tests/infer_class_decl_test.go), [lifetime_test.go](../../internal/checker/tests/lifetime_test.go), and others.
- New tests for the open-object finalization pass:
  - Param accessed only via reads → no `mut` wrapper added.
  - Param has at least one property write → `mut` wrapper added.
  - Nested case: param's property is itself an object whose inner field is written.

## Phase 3 — Sweep, snapshots, verification ✅ Landed

- `UPDATE_SNAPS=true go test ./...` — clean across all packages, no working-tree drift. Phase 2 already updated every fixture in the same commit it removed `MutabilityUncertain`. ✓
- Re-enable any test cases left disabled during Phase 1 development — none remained. The two NOTE-block placeholders in `mut_prefix_test.go` were already replaced with active tests during Phase 1 (`ImmutableInstance_CannotCallMutSelfMethod`, `ImmutableMap_CannotClear`, `ImmutableSet_CannotAdd`, plus the typevar-receiver pair). ✓
- Final sanity: `grep -rn "mut?" internal/` and `grep -rn "MutabilityUncertain" internal/` both return zero hits. ✓ (Only matches anywhere in the repo are a regex false positive on `colorGamut?` in `playground/public/types/lib.dom.d.ts` and intentional historical references in `planning/*.md`.)

## Phase 4 — Pattern-level `mut` on bindings

### Motivation

After Phase 1, calling a `mut self` method on an immutable receiver is property-not-found. That's the right default, but it leaves a use-site gap. The fixture [fixtures/objects_with_self/lib/objects_with_self.esc](../../fixtures/objects_with_self/lib/objects_with_self.esc) lost its `val inc = obj1.increment` line in Phase 1 because `obj1`'s binding has no way to say "I'm mutable" — today the parser only accepts `mut` directly preceding a `CallExpr`, which doesn't help when the RHS is a literal or another expression.

A second motivation: sometimes we want a mutable reference to an existing variable without knowing (or wanting to spell out) its type. The closest thing today is `val foo: mut typeof bar = bar`, which forces a `typeof` round-trip and a full annotation just to express "same value, mutable view". That's awkward enough to be effectively unusable in practice.

Rather than introduce an expression-level `MutExpr` (an earlier design considered and rejected — see "Alternative considered" below), this phase puts `mut` on the **binding pattern**, mirroring Rust's `let mut x = …`. Mutability is a property of the *place*, not of the value-producing expression:

```
val mut obj1 = { value: 0, increment(mut self) { ... } }   // binding-side mut
val mut p    = Point(0, 0)                                 // replaces  val p = mut Point(0, 0)
val mut inc  = obj1.increment                              // motivating fixture line
fn move_pt(mut p: Point) { p.x += 1 }                      // function parameters are patterns
val { x: mut a, y: b } = pt                                // per-leaf control inside destructuring
```

Two design wins fall out:

1. **No new soundness hole.** The pattern leaf is a fresh place; declaring it `mut` is a statement about that place's permissions. It does not retype an existing value, so the `mut <ident>` aliasing concern from the earlier `MutExpr` design simply doesn't arise. (Whether the underlying object is *actually* mutable through that place is the existing alias/lifetime/transition machinery's job, and is unchanged.)
2. **Reuses an existing pattern surface.** `Binding.Mutable` already exists in [internal/type_system/types.go](../../internal/type_system/types.go) — `inferPattern` currently sets it to `false // TODO` at every leaf ([internal/checker/infer_pat.go:44, :93](../../internal/checker/infer_pat.go#L44)). The pattern flag is what populates this field. The Phase 1 receiver-mut filter then picks up the binding's mut-ness via the existing path — no new infrastructure in the checker.

`mut <CallExpr>` on the value side stays exactly as it is today (the `Mutable` flag on `*ast.CallExpr`). It's still useful for inline construction-site mut where there's no binding (`someFn(mut Counter())`, `return mut Counter()`, `[mut Counter(), mut Counter()]`). The two forms coexist and fill different niches: pattern `mut` for the binding/place, expression `mut` for the inline allocation.

### AST + parser

- **New flag** on `IdentPat` and `ObjShorthandPat` in [internal/ast/pattern.go](../../internal/ast/pattern.go): `Mutable bool`. These are the only two pattern leaves that introduce a binding name (others either nest patterns or don't bind). Update `NewIdentPat`/`NewObjShorthandPat` constructors; the `//go:generate` directive at the top of `pattern.go` will refresh `pattern_gen.go` for the boilerplate. **Run `go generate ./internal/ast/...`** after the struct edit and commit the diff.
- **Parser** in [internal/parser/pattern.go](../../internal/parser/pattern.go): in the `pattern()` entry point ([line 10](../../internal/parser/pattern.go#L10)), accept an optional leading `Mut` token. When present, consume it and set `Mutable = true` on the resulting `IdentPat`. Reject (with a clear error) if the token is followed by anything other than an identifier — `mut [a, b]`, `mut { x }`, `mut _`, `mut "literal"`, `mut RestPat` are all meaningless at the pattern level (mutability is per-leaf, expressed inside the destructuring). The `mut` keyword does not need new lexer support — it's already a token (`Mut` at [internal/parser/token.go:65](../../internal/parser/token.go#L65)).

  **Object-pattern per-leaf forms — both shorthand and key-value must be supported:**
  - **Shorthand**, e.g. `val { mut x, y } = pt`: `mut` precedes the identifier inside an object pattern's `objPatElem` shorthand branch. This sets `Mutable = true` on the `ObjShorthandPat` leaf. Add `Mut` consumption to [internal/parser/pattern.go](../../internal/parser/pattern.go)'s `objPatElem` shorthand branch (around `objPatElem` near [pattern.go:206](../../internal/parser/pattern.go#L206) where shorthand is recognized).
  - **Key-value**, e.g. `val { x: mut a, y: b } = pt`: the right-hand side of `key: <pat>` is just a nested pattern, so it falls through `pattern()` recursively. Once `pattern()` itself handles `mut <ident>`, this form works **for free** — no extra parser change. The mutability ends up on the inner `IdentPat`'s `Mutable` flag (where `a` is bound), not on the `ObjKeyValuePat` wrapper. Add an explicit parser test to lock this in, since it exercises the recursive path.

  These two forms compose: `val { mut x, y: mut a, z } = pt` mixes shorthand-mut, keyvalue-mut, and immutable shorthand all at once; each leaf carries its own `Mutable` flag independently.
- **Function parameters are patterns** ([internal/parser/decl.go:481](../../internal/parser/decl.go#L481), `p.pattern(false, false)`), so `fn f(mut p: Point)` works automatically once the pattern parser handles `mut`. The existing `mut self` special-case stays as-is — it lives outside `pattern()` and is parsed separately ([decl.go:341 area](../../internal/parser/decl.go#L341)).
- **Parser ambiguity check:** `mut self` already has dedicated handling in `decl.go`'s param-list parser; `mut <ident>` in pattern position needs to *not* claim `self` (which is a keyword and would never reach `pattern()` anyway, but worth a parser test).

### Type inference

#### Split `Binding.Mutable` into two fields

The existing `Binding` struct in [internal/type_system/types.go:2395](../../internal/type_system/types.go#L2395) has a single `Mutable bool` field whose intended meaning is ambiguous — today it's effectively unused (defaulted `false` everywhere) but it was originally drafted to mean "rebindable" (var vs val). Pattern `mut` introduces a second, distinct concept: value-level mutability. Conflating the two would make this phase a footgun for future rebind-tracking. **Split the field as part of this phase:**

```go
type Binding struct {
    Source      provenance.Provenance
    Type        Type
    Assignable  bool  // can the binding name be rebound? (var = true, val = false)
    Mutable     bool  // can the underlying value be mutated through this name? (mut = true)
    Exported    bool
    VarID       int
}
```

- **Naming:** `Assignable` for rebind, `Mutable` for value-mutation. `Mutable` keeps its name (it's the more frequently useful field) but the meaning shifts from "TODO" to "value-mutability per the pattern's `mut` flag". `Assignable` is new.
- **Migration:** every existing `Binding{Mutable: false}` literal becomes `Binding{Assignable: false, Mutable: false}`. A scripted sweep handles the bulk of it; manual review where the intent matters:
  - `infer_pat.go:41-46`, `:90-95` — `IdentPat`/`ObjShorthandPat` branches. Set `Assignable` from the enclosing decl kind (`var` vs `val`, threaded through `inferPattern` as a parameter), and `Mutable` from the pattern flag.
  - `infer_stmt.go:442` — `for-in` currently force-clears the field. After the split: keep `Assignable = false` (loop variables can't be rebound to a different iteration value), but leave `Mutable` derived from the loop pattern (`for mut x in xs` keeps value-mutability).
  - `infer_module.go:1091, 1102, 1150, 1162` (and the analogous `infer_expr.go:493, 507, 521`) — function-parameter `Binding{}` literals. `Assignable: false` (parameters aren't `var`-rebindable today; revisit if/when that changes), `Mutable: <pattern flag>`.
  - `infer_import.go:497, 607` — re-exporting an imported binding propagates both fields verbatim.
  - `prelude.go:570-712` — built-in operator and `globalThis` bindings are immutable both ways: `Assignable: false, Mutable: false`.
  - `type_system/types.go:2486-2489` — namespace cloning needs both fields copied.
  - `type_system/types.go:2651` — namespace structural equality compares `Mutable`. After the split, compare both `Mutable` and `Assignable` (the comment at [:2645](../../internal/type_system/types.go#L2645) explains why `Mutable` matters for structural identity; the same logic applies to `Assignable`).

The split is a **mechanical rename + new field**, not a behavioral change for the existing field. Phase 4 is the right place to do it because (a) this phase is the first to actually populate the field with non-default data, and (b) it forces every binding-construction site to be touched anyway. Doing it here avoids a follow-up "split the overload" PR.

#### Pattern → binding wiring

- **`inferPattern`** ([internal/checker/infer_pat.go](../../internal/checker/infer_pat.go)): when constructing the `Binding` for an `IdentPat` ([line 41-46](../../internal/checker/infer_pat.go#L41-L46)) or `ObjShorthandPat` ([line 90-95](../../internal/checker/infer_pat.go#L90-L95)), set `Mutable: p.Mutable` (replacing the current `Mutable: false, // TODO`) and `Assignable: <derived from var/val context>`. The `var`/`val` distinction lives on the enclosing `VarDecl.Kind` (or equivalent) — thread it into `inferPattern` as a parameter so leaves can read it. For non-VarDecl pattern contexts (function parameters, for-in loops, match arms), pass the appropriate default (`false` for parameters and loop vars; match-arm bindings follow whatever the surrounding statement context dictates).
- **Wrap the binding's *type* in `MutType` when the pattern is mut.** This is what makes `val mut p = Point()` produce a `p` whose type passes the Phase 1 `ReceiverIsDefinitelyMutable` filter. Concretely: in the `IdentPat` branch, after computing `t`, if `p.Mutable` is true and `t` is not already wrapped in `MutType`, wrap it: `t = type_system.NewMutType(provenance, t)`. (If a user writes `val mut p: mut Point = Point()` the type annotation already provides the wrapper — preserve idempotence by only wrapping if not already wrapped.) Same logic in the `ObjShorthandPat` branch.
- **Function parameters:** `inferFuncSig` (and the parameter-binding paths in [infer_module.go](../../internal/checker/infer_module.go) at lines 1091, 1102, 1150, 1162) need to honor `IdentPat.Mutable` on parameter patterns and wrap the parameter type in `MutType` accordingly. Audit those four explicit `Binding{}` literals plus any others surfaced by `grep -n "type_system.Binding{" internal/checker/`.

### Lifetime / alias / transition tracking

The existing machinery already keys off binding identity, so pattern `mut` flows in naturally:

- `check_transitions.go`'s `CannotMutateImmutableError` keys off the binding's type — once the binding type carries `MutType`, mutations through `mut`-bound names are accepted; mutations through plain (non-mut) names continue to error.
- `infer_lifetime.go`'s alias propagation operates on `VarID` and binding types — no change needed.
- `liveness/capture_analysis.go`'s `markMutableLHS` walks LHS chains; it doesn't need to know about pattern mutability per se (it works at use sites).

**No new passthrough sites are required across liveness/lifetime/codegen/printer.** The Phase 4 design's ~10-row passthrough table doesn't apply here because there's no new expression node.

### Printer / codegen

- **Printer** ([internal/printer/printer.go](../../internal/printer/printer.go), pattern-printing path): when `IdentPat.Mutable` is true, emit `mut ` before the name. Same for `ObjShorthandPat`. Existing fixtures should round-trip; new fixtures using `val mut x = …` add new snapshots.
- **Codegen**: pattern `mut` has no runtime representation (same as today's `mut <CallExpr>`). The pattern lowering should ignore the flag — `val mut x = e` codegens identically to `val x = e`. Audit [internal/codegen/builder.go](../../internal/codegen/builder.go)'s pattern-handling to confirm no path conditionally emits anything based on a `Mutable` field that doesn't exist there yet.

### Tests and fixtures

- **Delete** [`TestMutPrefixOnNonCallRejected`](../../internal/checker/tests/mut_prefix_test.go)'s sub-tests for `OnIdent` and `OnArrayLit` — they'll need to remain as *negative* tests for `mut <expr>` since expression-level `mut` on non-call exprs is *still* rejected (we didn't change that). The `OnLiteral` case stays negative too. Update the test description to reflect that the rule lives at the expression level, while pattern-level `mut` is the new sanctioned form.
- **Add positive parser tests** for pattern `mut`:
  - `val mut x = 1` — simple mut binding
  - `var mut x = expr` — mut on a `var`
  - `val mut p: Point = Point(0, 0)` — with type annotation (verify wrapping is idempotent)
  - `val { mut x, y } = pt` — shorthand-form per-leaf mut inside object destructuring (sets `ObjShorthandPat.Mutable` on `x`, leaves `y` immutable)
  - `val { x: mut a, y: b } = pt` — key-value-form per-leaf mut (sets `IdentPat.Mutable` on the nested `a`, leaves `b` immutable); confirms recursive `pattern()` handles `mut`
  - `val { mut x, y: mut a, z } = pt` — mixed shorthand-mut + keyvalue-mut + plain shorthand in one pattern, to lock in independence of per-leaf flags
  - `fn f(mut p: Point) { ... }` — function parameter
  - `for mut x in iterable { ... }` — verify interaction with `for-in` (the loop currently force-overrides `binding.Mutable = false` at [infer_stmt.go:442](../../internal/checker/infer_stmt.go#L442); decide whether `mut` in the loop pattern overrides this or is rejected)
- **Add negative parser tests:**
  - `val mut [a, b] = arr` — `mut` on a tuple pattern → error ("`mut` applies to bindings, not destructuring patterns; write `[mut a, mut b]` instead")
  - `val mut { a, b } = obj` — same error for object pattern
  - `val mut _ = expr` — `mut _` is meaningless
- **Add an end-to-end checker test** covering the motivating fixture's shape:
  ```
  val mut obj1 = { value: 0, increment(mut self) -> Self { self.value = self.value + 1; return self } }
  val inc = obj1.increment    // resolves because obj1 is now mut
  obj1.increment()             // succeeds
  ```
  Assert: no errors; `obj1`'s binding type is `mut { ... }`; `inc`'s type is the (mut-self) method type; the call succeeds.
- **Add a `var mut`-versus-`val mut` test** that documents the (val) vs (var) vs (val mut) vs (var mut) matrix and exercises both `Binding` fields after the split:

  | Form        | `Assignable` | `Mutable` | Rebindable? | Underlying object mutable? |
  |-------------|--------------|-----------|-------------|----------------------------|
  | `val x`     | false        | false     | no          | no                         |
  | `var x`     | true         | false     | yes         | no                         |
  | `val mut x` | false        | true      | no          | yes                        |
  | `var mut x` | true         | true      | yes         | yes                        |

  At least one positive and one negative case per row, covering both the rebind axis (`x = newValue`) and the value-mutation axis (`x.field = newValue`). The rebind axis isn't enforced yet (read-side checks for `Assignable` are out of scope for this phase), so its tests assert binding shape rather than enforcement; the value-mutation axis is fully exercised through `check_transitions.go`.
- **Restore the deleted line** in [fixtures/objects_with_self/lib/objects_with_self.esc](../../fixtures/objects_with_self/lib/objects_with_self.esc): change `export val obj1 = { ... }` to `export val mut obj1 = { ... }` and add back `val inc = obj1.increment`. **Note:** this changes the public type of the `obj1` export to `mut { ... }`, observable to importers — confirm this is the intended public shape (it matches what the pre-Phase-1 `mut?` resolution produced once the body's writes were observed; now it's stated explicitly at the binding site).

### Edge cases

- **`for mut x in iter`:** the for-in loop currently force-sets `binding.Mutable = false` ([infer_stmt.go:442](../../internal/checker/infer_stmt.go#L442)) to enforce that loop variables aren't reassignable. Pattern-level `mut` is about value-mutability, not rebinding, so the override should be removed *or* refined: if the pattern has `Mutable: true`, leave the binding's type's `MutType` wrapper in place; only force `Binding.Mutable = false` (the rebind flag) regardless. Pick whichever is consistent with the rebind-vs-value-mut split documented above.
- **`mut self` parameters:** the existing dedicated parsing for `mut self` ([decl.go around line 341](../../internal/parser/decl.go#L341)) is unaffected — it's a pre-pattern special case. The pattern parser never sees `self`. Add a parser test that confirms `fn f(mut p, mut self)` and similar still parse correctly (or error in the expected way).
- **Type-annotation interaction:** `val mut p: Point = …` doesn't need any reconciliation logic — the annotation and the pattern's `mut` operate at different layers and compose cleanly:
  - The annotation provides the **value type** that the initializer is unified against. `inferTypeAnn` returns `Point`; `Unify(taType, patType)` runs as it does today and reconciles the pattern's fresh TypeVar with `Point`.
  - The pattern's `mut` is then applied **after** unification as an idempotent wrap on the binding's stored type: if the unified type isn't already a `*MutType`, wrap it; otherwise leave it. Pseudocode for the `IdentPat` branch in `inferPattern`:
    ```
    if p.Mutable {
        if _, ok := t.(*type_system.MutType); !ok {
            t = type_system.NewMutType(provenance, t)
        }
    }
    ```
  - Result: `val mut p: Point = …` stores `mut Point` in the binding; `val mut p: mut Point = …` stores `mut Point` (no double-wrap); `val p: mut Point = …` stores `mut Point` via the annotation alone.
  - **No new transition-checker or lifetime-checker hook.** Mutating writes through `p` go through `check_transitions.go`, which already keys off the binding's stored type — once it's `mut Point`, the `CannotMutateImmutableError` path is bypassed naturally. Lifetime params attach to leaf types and propagate via the existing alias machinery; the `mut` wrapper doesn't alter the lifetime graph.
- **`val mut p = mut Counter()`** (both sides claim mut) — the same idempotent guard above keeps the binding type as `mut Counter`, not `mut (mut Counter)`.
- **`val mut foo = bar`** where `bar: Point` (immutable) — this is the "second motivation" use case. The pattern wraps `foo`'s type as `mut Point`, but `foo` aliases `bar`. The existing `trackAliasesForVarDecl` ([infer_stmt.go:123](../../internal/checker/infer_stmt.go#L123)) records the alias edge; subsequent writes through `foo` go through `check_transitions.go`, which already handles mut-typed bindings aliasing immutable places. The pattern flag declares the binding-side view; the existing machinery decides whether that view is sound for the aliased value. **Net:** no new soundness story to invent — sound when the alias/transition machinery accepts it, errors when it doesn't.

### Risks

- **`Binding{}` literal sweep.** Splitting `Mutable` into `Assignable` + `Mutable` touches every `Binding{}` construction site. There are ~15 in `internal/checker/` (see "Split `Binding.Mutable` into two fields" above for the inventory) plus two in `internal/type_system/`. The compiler catches missed sites if the field is renamed (i.e. don't keep `Mutable` as a backwards-compat alias during the migration — let `go build` fail loudly). `grep -n "type_system.Binding{" internal/` is the sweep; `go test ./...` plus the function-parameter and val/var × mut/non-mut matrix tests are the regression check.
- **For-in loop interaction.** The current force-`Mutable=false` at [infer_stmt.go:442](../../internal/checker/infer_stmt.go#L442) becomes force-`Assignable=false` after the split (loop vars aren't rebindable to a different iteration value), and `Mutable` is derived from the loop pattern (`for mut x in xs` keeps value-mutability). A test locks in this behavior.
- **Namespace structural-equality drift.** [types.go:2651](../../internal/type_system/types.go#L2651) compares `Mutable` as part of namespace identity. After the split, both fields participate in equality. If only `Mutable` is updated and `Assignable` is missed, two namespaces that differ only in val/var would compare equal — likely benign today but a latent footgun. Update the comparison loop and the comment at [:2645](../../internal/type_system/types.go#L2645) in the same change.

### Alternative considered: expression-level `MutExpr`

An earlier design replaced `CallExpr.Mutable` with an `*ast.MutExpr` wrapper that could prefix any expression. It was rejected for three reasons:

1. **Soundness gap.** `mut <ident>` retypes a value the user doesn't own — `(mut x).tickInPlace()` could mutate an object the type system elsewhere considers immutable. Mitigations required restricting the form to construction sites, which then made the wrapper redundant with the existing `CallExpr.Mutable` flag.
2. **Passthrough surface.** ~10 expression-walking switches across liveness, lifetimes, alias analysis, codegen, and printer would need a new case, with silent-degradation failure modes when any was missed. Pattern-level `mut` adds zero new expression nodes and reuses existing pattern-walking code.
3. **No destructuring story.** `mut { a, b } = obj` (with `a` mut and `b` not) has no clean expression-level form. Pattern leaves give per-leaf control naturally.

The earlier design's notes — including the passthrough table, the `MutExpr.Accept` recursion requirement, and the `processAssignBranch` early-return hazard — are preserved in this plan's git history if expression-level `mut` is ever revisited.

## Phase 5 — Sweep, snapshots, verification (Phase 4 follow-up)

- `UPDATE_SNAPS=true go test ./...` — review printer/codegen snapshot diffs. Most existing fixtures should be untouched (the only printer-side change is the optional `mut ` prefix on `IdentPat`/`ObjShorthandPat`); the restored `objects_with_self` fixture and any new pattern-mut tests add new snapshots.
- Confirm the restored `val inc = obj1.increment` line in [fixtures/objects_with_self/lib/objects_with_self.esc](../../fixtures/objects_with_self/lib/objects_with_self.esc) round-trips through codegen and matches the pre-Phase-1 generated JS. The export is now `val mut obj1`, but the runtime emission should be identical — pattern `mut` is type-only.
- `go generate ./internal/ast/...` produces no diff against the committed `pattern_gen.go` (i.e. the regeneration was committed alongside the struct edit).
- Lifetimes test suite (`go test ./internal/checker/tests/lifetime_test.go`) green — pattern `mut` should not regress lifetime/alias propagation since binding identity is unchanged. Worth running as a backstop.
- Mutation-transition tests green (`go test ./internal/checker/tests/check_transitions_test.go` and similar) — mut bindings now succeed at field assignment where plain bindings error. The matrix table from "Tests and fixtures" exercises this directly.
- `grep -n "Mutable: false, // TODO" internal/checker/` returns zero hits — both `IdentPat` and `ObjShorthandPat` paths in `infer_pat.go` now derive `Mutable` from the pattern flag.
- `grep -n "type_system.Binding{" internal/` review — every literal sets both `Assignable` and `Mutable` explicitly (or relies on the zero-value default `false`/`false` knowingly). `go build ./...` catches any field-name typos from the split-and-rename.
- Namespace structural-equality test (anything in `internal/type_system/` that exercises `equals` over `Namespace`) covers two bindings that differ only in `Assignable` and confirms they compare unequal.

## Risks and unknowns

- **Open-object finalization order.** If a function body's open-object decision needs to be visible to a *caller's* unification before the caller is checked, single-pass-after-body finalization is fine. If there's a mutual-recursion case where the caller's checks run first, the order may need shuffling — same machinery the lifetimes pass already uses for lifetime params.
- **Argument unwrapping in `inferCallExpr`.** [infer_expr.go:1143](../../internal/checker/infer_expr.go#L1143) calls `unwrapMutability(at)` on every argument when the callee is a `TypeVarType` — this strips `mut?` from synthetic argument types so the inferred function signature is clean (per the comment at [:1136](../../internal/checker/infer_expr.go#L1136)). After Phase 2 there is no `mut?` to strip; verify the surrounding code still produces clean inferred signatures.
- **`infer_lifetime.go:466` reference.** The doc comment for `stripMutabilityWrapper` cross-references `unwrapMutability`; rewrite or remove the comment when `unwrapMutability` is deleted.
- **Print-format churn.** Every test fixture mentioning `mut?` will need a sweep — mostly mechanical but loud in PR diff.

## Sizing

| Phase                                        | Effort       | Status / Risk                                                  |
| -------------------------------------------- | ------------ | -------------------------------------------------------------- |
| Phase 1 (mut-self gate + LSP)                | ~half-day    | ✅ Landed. Open-object hazard sidestepped via `objType.Open` short-circuit. |
| Phase 2 (`mut?` removal + finalization pass) | 2–4 days     | ✅ Landed. `removeUncertainMutability` retained as `rebuildContainers` (load-bearing for FromBinding TypeVar normalization). |
| Phase 3 (fixture sweep + new tests)          | ~half-day    | ✅ Landed. No fixture sweep needed (Phase 2 covered it); no disabled tests remained. |
| Phase 4 (pattern-level `mut` + `Binding` field split) | 1–1.5 days | Pending. AST flag + parser + pattern-inference plumbing + function-param sweep + `Binding.Mutable` → `{Assignable, Mutable}` split touching ~17 construction sites. No new expression nodes; no liveness/lifetime/codegen passthrough sites. Risk: missing one of the `Binding{}` construction sites; namespace-equality drift if the comparison loop isn't updated alongside the struct. |
| Phase 5 (Phase 4 sweep + verification)       | ~half-day    | Pending. Mechanical fixture/snapshot review + the val/var × mut/non-mut matrix tests + namespace-equality regression test. |
| **Total**                                    | **4–6 days** | Phases 1–3 done; Phases 4–5 pending. |

## Verification

1. `go test ./...` green after each phase. ✓ (Phases 1–3)
2. `grep -r "mut?" internal/` and `grep -r "MutabilityUncertain" internal/` both return zero hits after Phase 2. ✓
3. LSP completion at `immutablePoint.` does not list any `mut self` methods; at `mutablePoint.` it does. ✓ (Phase 1)
4. The receiver-mutability tests previously commented out in [mut_prefix_test.go](../../internal/checker/tests/mut_prefix_test.go) are re-enabled and passing, plus type-var-receiver coverage. ✓ (Phase 1)
5. Generated JS for existing programs is byte-identical (the change is purely type-level). ✓ (Phase 1; the only fixture JS deltas are removals of `obj1.increment.bind(obj1)` for the binding line that was deleted — no other diffs.)
6. After Phase 4: `val mut p = Counter(); p.tick()` succeeds; `val p = Counter(); p.tick()` errors with property-not-found (Phase 1 filter); the val/var × mut/non-mut matrix test passes.
7. After Phase 4: the restored `val inc = obj1.increment` line in [fixtures/objects_with_self/lib/objects_with_self.esc](../../fixtures/objects_with_self/lib/objects_with_self.esc) (now under `export val mut obj1 = { ... }`) round-trips through codegen unchanged from the pre-Phase-1 baseline.
8. After Phase 4: `fn f(mut p: Point) { p.x = 1 }` type-checks; `fn f(p: Point) { p.x = 1 }` errors with the existing `CannotMutateImmutableError`. Function parameters honor pattern `mut`.
9. After Phase 4: `grep -n "Mutable: false, // TODO" internal/checker/` returns zero hits — both `IdentPat` and `ObjShorthandPat` branches in `infer_pat.go` now derive `Binding.Mutable` from the pattern flag.
