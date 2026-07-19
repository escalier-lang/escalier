# Generic-function generalization — implementation plan

This plan covers **the generic-function work** that [M5](m5-implementation-plan.md)
and [M7](m7-implementation-plan.md) both name and defer: inferring and generalizing
a standalone generic function `fn f<T>(…)` and a generic function-type annotation
`fn <T>(…) -> …`. It is not a numbered milestone. It is a cross-cutting
prerequisite whose *representation* M5 already shipped and whose remaining
consumers span several milestones, so it is written as its own plan rather than
folded into any one of them.

M5's own note draws the line this plan starts from
([m5-implementation-plan.md](m5-implementation-plan.md) §"Scope expansion this
implies"):

> `FuncType.TypeParams` is not class-specific … M5 populates and consumes it for
> methods and leaves general `fn f<U>` *inference* to the generic-function work;
> the *representation* is shared.

## What M5 shipped (ground truth this plan builds on)

The representation and the *class/method* consumer are done. Only the
value-binding path and the standalone-function surfaces are missing.

- **`FuncType.TypeParams` is the shared representation.** Every function type has a
  home for its own generics. `TypeParam.Var` is an ordinary bounded inference var
  freshened by the existing level-based `instantiate`/`freshenAbove`, not a named
  parameter resolved by substitution, so no second polymorphism mechanism runs
  ([soltype/type.go:206-229](../../internal/soltype/type.go),
  [m5-implementation-plan.md](m5-implementation-plan.md) §"Why `TypeParam.Var` is a
  bounded inference var").
- **`resolveTypeParams` resolves a `<…>` list.** The two-pass resolver mints one
  var per parameter, declares it in a child scope, then resolves bounds and
  defaults, so a forward, mutual, or F-bound reference between siblings resolves
  ([type_params.go](../../internal/solver/type_params.go)). The class, enum, and
  alias paths already route through it; this plan routes the function and
  function-type-annotation paths through it too, the "once generic-function
  inference lands" case its own doc comment anticipates.
- **The keep-set retention exists — but only inside the class-body freeze.**
  `coalesceKeeping` holds a set of vars symbolic rather than inlining them to their
  bounds, and `freezeClassBody` passes a generic class's own type-param vars *and
  each method's own `FuncType.TypeParams` vars* as `keep`, so a member typed
  through `T` stores as `T` rather than collapsing to `never`
  ([coalesce.go:39-52](../../internal/solver/coalesce.go),
  [infer_class.go:99-115](../../internal/solver/infer_class.go),
  `classKeepVars`/`keptFlowMap`). The **value-binding** generalization path has no
  equivalent — see the gap below.
- **The rank-1 boundary is fixed.** A parameter whose own type is polymorphic (a
  higher-rank callback `<V>(V) -> V`) is rejected rather than approximated, matching
  SimpleSub's and MLstruct's rank-1 boundary
  ([m5-implementation-plan.md](m5-implementation-plan.md) §"Known limitation"). This
  plan stays rank-1; relaxing it is out of scope.

## The gap (the delta this plan adds)

Three seams, one shared core.

1. **The value-binding generalization path drops `FuncType.TypeParams`.** `generalize`
   quantifies every var with `Level > lvl` into a `PolyScheme` and coalesces the rest
   ([poly.go:451-472](../../internal/solver/poly.go)). A function's own type-param
   vars are neither in a `keep` set nor excluded from that quantification, so a
   binding whose type is a generic `FuncType` breaks two ways:
   - inlined to `never` and a panic in `acceptTypeParams`, which requires a bound
     parameter to stay a variable
     ([soltype/visitor.go:338-349](../../internal/soltype/visitor.go)); or
   - double-quantified — the same var appears once as a retained scheme variable and
     once in `FuncType.TypeParams`, rendering `fn <T0, T: T0>(x: T) -> T`.

   The fix is the freezeClassBody analogue for the value path: hold a function
   type's own `TypeParams` vars symbolic through coalescing, and exclude them from
   the outer scheme's free-var quantification, so the function's declared quantifier
   is its *only* quantifier.
2. **`fn f<T>(…)` decls are gated unsupported.** `inferFunc` reports
   `reportUnsupportedFeature(node, "TypeParam")` and infers monomorphically, leaving
   `T` unbound ([infer_expr.go:201-208](../../internal/solver/infer_expr.go)). Once
   the core lands, route `sig.TypeParams` through `resolveTypeParams` into the
   function's child scope, generalize the body into `FuncType.TypeParams`, and
   instantiate per call.
3. **`fn <T>(…) -> …` annotations are gated unsupported.** `resolveFuncTypeAnn`
   reports `"generic function type annotation"` rather than resolving the `<…>` list
   ([type_ann.go](../../internal/solver/type_ann.go),
   `resolveFuncTypeAnn`). This is the **M7 PR6 deferred half**: routing the list
   through `resolveTypeParams` resolves the parameters, but the resulting vars hit
   the value-path gap above. It also unblocks a union member that is a bare type
   parameter, the source path that makes M7's already-landed union-super two-pass
   exists trial reachable.

## PR-by-PR breakdown

Three PRs. PR1 is the shared core; PR2 and PR3 are the two surfaces and are
mutually independent once PR1 lands.

### PR1 — Retain `FuncType.TypeParams` through value-binding generalization

The core. Lift the `keep`/`flow` retention `freezeClassBody` gives a class body
into the value-binding generalization path, so a function type's own type-param
vars survive coalescing as its quantifier instead of inlining to `never`.

- Collect a binding's function-type `TypeParams` vars the way `classKeepVars`
  collects a class's, and thread them as `keep` into the coalescing that
  `generalize` / `coalesceScheme` run
  ([poly.go:451-472](../../internal/solver/poly.go),
  [coalesce.go:252-291](../../internal/solver/coalesce.go)).
- Exclude those vars from the outer scheme's free-var quantification so the same var
  is not both a retained scheme variable and a `FuncType.TypeParams` entry — the
  `fn <T0, T: T0>` double-render.

**Accept.** A `FuncType` carrying `TypeParams` round-trips through a value binding
without panicking and renders `fn <T>(x: T) -> T`, not `fn <T0, T: T0>(x: T) -> T`.

### PR2 — Generic function declarations `fn f<T>(…)`

- Lift the `reportUnsupportedFeature(node, "TypeParam")` gate in `inferFunc`
  ([infer_expr.go:201-208](../../internal/solver/infer_expr.go)); route
  `sig.TypeParams` through `resolveTypeParams` into the function's child scope so a
  parameter or return annotation reads each `T` as one shared var.
- Generalize the inferred body into `FuncType.TypeParams` (PR1's retention), and
  instantiate the scheme per call so `f(5)` and `f("hi")` bind `T` independently.

**Accept.** `fn id<T>(x: T) -> T` type-checks; `id(5)` yields `number` and
`id("hi")` yields `string` from independent instantiations; a higher-rank parameter
is still rejected at the rank-1 boundary.

**Depends on** PR1.

### PR3 — Generic function-type annotations (M7 PR6 deferred half)

- Route `ta.TypeParams` through `resolveTypeParams` in `resolveFuncTypeAnn` into a
  child scope, and resolve the parameters, return, and any union member that is a
  bare type parameter against it
  ([type_ann.go](../../internal/solver/type_ann.go)). Delete the
  `"generic function type annotation"` unsupported report.
- Re-enable `TestInferGenericFuncAnnotationReportsUnsupported`
  ([infer_func_ann_test.go](../../internal/solver/infer_func_ann_test.go)) as a
  resolves-and-renders test, and add the `fn <T>(x: T | number) -> …` case that
  exercises M7's union-super two-pass exists trial from source.

**Accept.** `val f: fn<T>(x: T) -> T = fn (x) { return x }` resolves and renders
`fn <T>(x: T) -> T`; `fn f<T>(x: T | number)` resolves and its union member `T`
binds per the union-super two-pass rule already in `constrain`.

**Depends on** PR1. Independent of PR2.

## Who this unblocks

- **M7 PR6** — the generic-union annotation surface. Its union-super two-pass exists
  trial already landed in `constrain`
  ([constrain.go](../../internal/solver/constrain.go)); PR3 here makes the surface
  that reaches it with a real type-var union member from source.
- **Standalone `fn f<T>(…)` decls** — the general generic-function inference M5 and
  M3 both point at.

## Not blocked on MLstruct

Generalization over bounded variables already works in Simple-sub; it is M3
let-polymorphism. MLstruct is a *possible future* extension for negation types and
narrowing, "relevant if we later add negation-based narrowing"
([03-references.md:27-29](03-references.md)). The `TypeParam.Var`-as-bounded-var
design is forward-compatible with it
([m5-implementation-plan.md](m5-implementation-plan.md) §"Why `TypeParam.Var` is a
bounded inference var"), but MLstruct neither delivers nor blocks this work.

## Dependency graph

```
PR1 (retain FuncType.TypeParams through value-binding generalization)
 ├─► PR2 (generic function declarations fn f<T>)
 └─► PR3 (generic function-type annotations)  ── unblocks M7 PR6 annotation surface

feeds ─► M7 PR6 (generic-union annotation surface + union-super trial, already landed in constrain)
```
