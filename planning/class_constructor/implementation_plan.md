# Class Constructor Implementation Plan

This plan describes how to replace the primary-constructor syntax
(`class Foo(p1, p2) { ... }`) with explicit `constructor` blocks inside the
class body, as specified in [requirements.md](./requirements.md).

The work is organized into phases that build incrementally, each producing a
testable milestone. Phases 1–3 are additive (the old syntax still parses);
Phase 4 is a single combined cut-over that lands the new codegen for
single-constructor classes, migrates all fixtures, and removes the old
primary-constructor syntax and `data` modifier together — the codegen
change and fixture migration cannot land separately without breaking the
in-tree fixtures. Multi-constructor support (Phase 5) lands after the
cut-over, on top of the new single-constructor codepath.

## Phase Overview

| Phase | Description                                                                                | Depends On | Status |
|------:|--------------------------------------------------------------------------------------------|------------|--------|
|     1 | AST + parser: introduce `ConstructorElem` (additive, alongside primary ctor)               | —          | Done   |
|     2 | Type checker: derive class constructor type from `ConstructorElem` (single)                | 1          |        |
|     3 | Definite-assignment analysis on constructor bodies (incl. `super(...)`)                    | 2          |        |
|     4 | Combined cut-over: single-ctor codegen, fixture migration, removal of primary ctor + `data` | 3          |        |
|     5 | Multiple constructors: overload resolution, runtime discriminator analysis, merged codegen | 4          |        |
|     6 | Polish: error messages, diagnostics, documentation                                         | 5          |        |

The "Future Work" sections of the requirements doc (private constructors,
`Self(...)` delegation) are explicitly **out of scope** for this plan.

---

## Phase 1: AST + Parser — `ConstructorElem`

**Goal:** Add the new `constructor` element to the AST and parser without
changing any existing behavior. The old primary-constructor syntax continues
to work.

### 1.1 New AST Node

**File:** `internal/ast/class.go`

Add a new `ConstructorElem` that implements `ClassElem`:

```go
type ConstructorElem struct {
    Fn      *FuncExpr  // type params, params, body. No return type.
    Private bool       // reserved for future "Private Constructors" work
    Span_   Span
}

func (*ConstructorElem) IsClassElem() {}
func (c *ConstructorElem) Accept(v Visitor) {
    if v.EnterClassElem(c) {
        if c.Fn != nil {
            c.Fn.Accept(v)
        }
    }
    v.ExitClassElem(c)
}
func (c *ConstructorElem) Span() Span { return c.Span_ }
```

Notes:
- `Fn.ReturnType` should remain `nil` for constructors (the return type is
  always `Self`); the parser rejects an explicit return type.
- `Fn.ThrowsType` may be non-nil — constructors may declare a `throws`
  clause (or have one inferred from the body) to express factory-only
  patterns. See requirements §"Throwing Constructors".
- `Fn.Params[0]` is the user-written `mut self` parameter (see
  requirements §"Core Concept"). Like methods, constructors require a
  `self` parameter to be visible in the source — the parser enforces
  that the first param is `mut self` (no type annotation, `mut`
  modifier present) and reports a parse error otherwise. The remaining
  `Fn.Params[1:]` are the constructor's callable parameters.
- Reuse `FuncExpr` rather than inventing a new struct so visitors and
  rename/liveness passes pick up the body for free.

### 1.2 Lexer / Keywords

`constructor` becomes a soft keyword. Treat it as an identifier everywhere
except at the start of a class element. Mirror what `data` does today
(`internal/parser/decl.go:106`): only special-case when the parser is sitting
at a class-element boundary.

No changes to `internal/lexer_util` — handle this contextually in the parser
rather than reserving `constructor` globally. Reserving it would break
anyone using `constructor` as a property name.

### 1.3 Parser Changes

**File:** `internal/parser/decl.go`

In `parseClassElem` (currently around line 217), add a branch that recognizes
`constructor` after the modifier-parsing loop:

```go
if token.Type == Identifier && token.Value == "constructor" {
    p.lexer.consume()
    typeParams := p.maybeTypeParams()
    if next := p.lexer.peek(); next.Type != OpenParen {
        p.reportError(next.Span, "Expected '(' after 'constructor'")
    }
    p.lexer.consume()
    params := parseDelimSeq(p, CloseParen, Comma, p.param)
    p.expect(CloseParen, AlwaysConsume)

    // Enforce that the first parameter is `mut self` (no type annotation,
    // `mut` modifier present). Methods already require an explicit `self`
    // first parameter; constructors mirror that, but with `mut` since the
    // body must be allowed to assign fields. Errors:
    //   - missing `self` first parameter → "constructors must declare `mut self` as their first parameter"
    //   - `self` without `mut` → "the `self` parameter of a constructor must be declared `mut self`"
    //   - `mut self` with a type annotation → "the `mut self` parameter cannot have a type annotation"
    //   - `self` appearing in a non-leading position → existing rule
    // The `mut self` parameter is preserved in `Fn.Params[0]`; it is the
    // single source of truth for the body checker (Phase 2.3).

    // Reject explicit return type on constructor (the return type is
    // always `Self`). A `throws` clause IS allowed — constructors may
    // throw to express factory-only patterns.
    if p.lexer.peek().Type == Arrow {
        p.reportError(/* span */, "constructors cannot declare a return type")
    }
    throwsType := p.maybeThrowsClause()

    block := p.block()
    span := /* from start to currentLocation */
    return &ast.ConstructorElem{
        Fn:      ast.NewFuncExpr(nil, typeParams, params, nil, nil, false, &block, span),
        Private: isPrivate, // reject in checker for now; reserved
        Span_:   span,
    }
}
```

Modifier handling:
- `static`, `async`, `get`, `set`, `readonly` on a constructor are parse
  errors. Report them inline as the modifiers are consumed.
- `private` parses but the checker rejects it for now (see Future Work
  in requirements.md and §"Out of Scope" below).

### 1.4 Visitor Updates

Sweep all visitors that switch over `ast.ClassElem` and add a
`ConstructorElem` case:

- `internal/checker/infer_module.go` — the two giant switches at
  `decl.Body` iteration (around lines 398 and 1007). Phase 1 ignores the
  new case with a `// TODO(class-ctor Phase 2): wire up` comment so we
  don't lose track. Phase 2 wires it in.
- `internal/printer` — pretty-printing for source dumps.
- `internal/codegen/builder.go` — `buildClassElems`. Phase 1 ignores
  `ConstructorElem` (existing primary-ctor codegen is still active) with
  a `// TODO(class-ctor Phase 4): emit single-ctor body` comment.
- `internal/dts_parser` — declaration-file emission, if it traverses class
  bodies.
- Any rename/liveness traversal in `internal/checker/infer_lifetime.go`.

Use the explicit `// TODO(class-ctor Phase N): …` form rather than panics
in Phase 1 so the additive milestone can land without breaking any
existing primary-constructor behavior.

### 1.5 Tests

**File:** `internal/parser/tests/class_test.go` (or equivalent — match
existing structure)

Add parser tests covering:
- A class body with one `constructor(mut self)` block, no extra params.
- A class body with one `constructor(mut self, x: number)` block.
- A class body with two `constructor` blocks (each with `mut self`).
- A `constructor<U>(mut self, value: U)` with type params.
- Rejection of `static constructor`, `async constructor`, `get constructor`,
  `constructor(mut self) -> T`. (A `throws` clause is allowed — see
  §"Throwing Constructors" in requirements.md.)
- Rejection of `constructor()` (missing `mut self`),
  `constructor(self, x: number)` (must be `mut`),
  `constructor(mut self: Self, x: number)` (no type annotation allowed
  on `self`), and `constructor(x: number, mut self)` (`self` must be
  first).
- A class with both primary-constructor params and an in-body
  `constructor` block — parses cleanly here; Phase 2 rejects it in the
  checker.

---

## Phase 2: Type Checker — Single Explicit Constructor

**Goal:** When a class body contains exactly one `ConstructorElem`, derive
the class's constructor signature from it instead of from `ClassDecl.Params`.
Mixing both forms in the same class is an error.

This phase intentionally restricts to **one** in-body constructor; multiple
constructors come in Phase 5.

### 2.1 Where the Current Logic Lives

The current placeholder phase in `internal/checker/infer_module.go`
(around lines 575–614) does:

```go
params, paramBindings, paramErrors := c.inferFuncParams(declCtx, decl.Params)
// …
funcType := type_system.NewFuncType(provenance, typeParams, params, retType, never)
constructorElem := &type_system.ConstructorElem{Fn: funcType}
```

The `ConstructorElem` already exists at the type level
(`internal/type_system/types.go:1010`); only one is allowed per class today.

### 2.2 New Source-of-Signature

Replace the call to `inferFuncParams(declCtx, decl.Params)` with logic that:

1. Counts the `ConstructorElem`s in `decl.Body`.
2. If `decl.Params` is non-empty AND any in-body constructor exists →
   `MixedConstructorFormsError` on the class span.
3. If `decl.Params` is non-empty (legacy path) → unchanged behavior.
4. If exactly one in-body `ConstructorElem` → infer its params via
   `inferFuncParams`, plus any constructor-local type params, and build the
   `FuncType` from them.
5. If zero in-body constructors AND `decl.Params` is empty → synthesize a
   constructor from the instance fields (see 2.7 below).

The placeholder phase should iterate `decl.Body` once for fields, methods,
**and** constructors, but the constructor branch only records the
signature; the body is checked in the definition phase below.

### 2.3 Body Checking

In the definition phase (around `infer_module.go:1007`), add a
`*ast.ConstructorElem` branch to the body-element switch:

```go
case *ast.ConstructorElem:
    // Build a function context for the constructor. The user-written
    // `mut self` parameter (Fn.Params[0]) is the source of truth for
    // self's binding — its `mut` flag and type (`Self`, supplied by the
    // checker since the source carries no annotation) drive the scope
    // setup below. Constructor params follow at Fn.Params[1:]. Return
    // type is `Self` (implicit). The throws type comes from the optional
    // `throws` clause; constructors may throw.
    ctorCtx := bodyCtx.WithNewScope()
    ctorCtx.Scope.setValue("self", &type_system.Binding{
        Source:     &ast.NodeProvenance{Node: bodyElem},
        Type:       type_system.NewMutType(nil,
                       type_system.NewTypeRefType(nil, decl.Name.Name, typeAlias)),
        Assignable: false,
        Mutable:    true,
    })
    // … add constructor params as bindings, just like methods do …
    bodyErrors := c.inferFuncBodyWithFuncSigType(
        ctorCtx, /* ctorFuncType */, paramBindings,
        bodyElem.Fn.Params, bodyElem.Fn.Body, false /* not async */,
    )
    errors = slices.Concat(errors, bodyErrors)
```

This reuses the method body-checking machinery, but with `self` as `mut`
during construction regardless of class-level default mutability (per
requirements §"Default Mutability").

`inferFuncBodyWithFuncSigType` already infers a `throws` type from the
body when the signature does not declare one, so constructors get
throws inference for free — no separate codepath. When the
`ConstructorElem` does carry an explicit `throws` clause, the body's
inferred throws set must be assignable to the declared one, same as
any other function.

**Modeling `mut self`.** The user writes `mut self` as the literal first
parameter of the constructor (see requirements §"Core Concept"). That
single source flows through both abstractions:

1. *AST level*: `ConstructorElem.Fn.Params[0]` is the user-written
   `mut self` `FuncParam`. Methods already follow the same convention.
2. *Type-system level*: the constructor's `FuncType` carries
   `mut self: Self` as its first formal parameter (lifted from
   `Fn.Params[0]`, with the type filled in by the checker as `Self`
   since the source intentionally has no annotation there). It is
   **not** part of the constructor's callable arity — overload
   resolution and codegen-emitted dispatch ignore it.
3. *Body-checker level*: the constructor's scope has a `self` binding of
   type `mut Self` injected before the body is checked (the
   `ctorCtx.Scope.setValue("self", …)` call in the sketch above). This
   binding is built from `Fn.Params[0]` / the leading `FuncType`
   parameter, not synthesized independently, so the three views cannot
   drift.

### 2.4 Field Initialization Semantics

Field declarations are restricted to the two forms permitted by the
requirements:

- `x: T` — required field.
- `x?: T` — optional field, implicitly `undefined` until assigned.

The legacy form `x: 0` / `x = 0` (a field-level default) is **not**
supported under the new model — defaults belong on constructor parameters
(`constructor(x: T = 0)`) or as explicit assignments inside the constructor
body. Phase 2 reports `FieldDefaultNotAllowedError` on a `FieldElem` whose
`Value` is non-nil **only when the enclosing class has at least one
in-body `ConstructorElem`** (i.e. has opted into the new model). Classes
that still use primary-constructor syntax retain the legacy behavior
until Phase 5 removes them.

For each remaining FieldElem the type comes straight from the annotation
and the obligation to assign it shifts to the constructor (Phase 3
enforces it). Drop the same-named-parameter fallback at
`infer_module.go:1044` when no primary constructor is present.

### 2.5 New Errors

Add to `internal/checker/error.go`:

- `MixedConstructorFormsError` — class has both primary-ctor params and
  an in-body `constructor`.
- `MissingMutSelfParameterError` — constructor's first parameter is not
  `mut self` (also covers `self`-without-`mut` and `self`-with-type-annotation).
  Reported by the parser, mirrored here for safety.
- `MultipleConstructorsNotYetSupportedError` — temporary, removed in
  Phase 5.
- `ConstructorWithReturnTypeError` — caught in the parser, but mirrored
  here for safety.
- `PrivateConstructorNotYetSupportedError` — for `private constructor`
  (lifted in Future Work).
- `FieldDefaultNotAllowedError` — a field declaration carries a default
  value (`x = 0` / `x: 0`); defaults belong on constructor parameters
  instead.
- `ComputedKeyFieldRequiresConstructorError` — class with a non-optional,
  computed-key field cannot have a constructor synthesized.

### 2.6 Tests

**File:** `internal/checker/tests/class_test.go`

- A class with one `constructor`, no fields → instances construct cleanly.
- A class with one `constructor` and matching field declarations → field
  types resolve from annotations, not from constructor params.
- A class with both `class Foo(x: number) { ... }` and
  `constructor(...)` in the body → `MixedConstructorFormsError`.
- A class with two in-body `constructor`s →
  `MultipleConstructorsNotYetSupportedError` (placeholder for Phase 5).

### 2.7 Synthesized Constructor

When a class has no in-body `constructor` and no primary-ctor params, the
checker synthesizes one from the instance fields, per requirements
§"Synthesized Constructor".

Implementation:

1. Walk `decl.Body` in source order.
2. Synthesize `Fn.Params[0]` as a `mut self` parameter (same shape as
   user-written constructors) so downstream passes see a uniform AST.
3. For each `*ast.FieldElem` that is non-static and not optional: append a
   synthetic param to the constructor signature with the same name and
   type as the field.
4. Skip static fields and optional fields.
5. If any qualifying field has a computed key (`*ast.ComputedKey`), report
   `ComputedKeyFieldRequiresConstructorError` and bail — the user must
   write a constructor explicitly.

The synthetic constructor is materialized as a real `*ast.ConstructorElem`
inserted into `decl.Body` before the rest of the placeholder phase runs.
Materializing it in the AST means Phase 3's definite-assignment pass,
Phase 4's codegen, and `.d.ts` emission consume the same shape as
user-written constructors, with no special-casing. Synthesized
constructors **do** appear in `.d.ts` output — the goal is that a
consumer importing the module sees the same callable surface whether
the constructor was synthesized or user-written.

The synthesized body is mechanical:

```go
// Pseudocode for the synthesized FuncExpr body
for _, f := range syntheticFields {
    body.append(`self.{f.Name} = {f.Name}`)
}
```

A class with only optional fields (or no fields at all) synthesizes a
zero-arg constructor, preserving today's behavior for `class Foo {}`.

The synthetic node's `Span` points at the class name (the same span the
class identifier carries), so any diagnostic produced against the
synthesized constructor — e.g. a Phase 3 definite-assignment error on
a synthesized subclass forwarder, or an LSP "go to definition" — lands
on the class header rather than at offset zero. Each synthesized
field-write statement carries the originating field's span.

#### Synthesized Subclass Constructors

When the class `extends` a base, synthesis follows requirements
§"Inheritance and `super`":

1. Base has exactly one constructor, subclass has no non-optional
   fields → synthesize a constructor whose parameters mirror the base
   constructor's, body is a single `super(...)` forwarding all
   parameters.
2. Base has exactly one constructor, subclass has non-optional fields
   → synthesize a constructor whose parameters are
   `(baseParams..., subclassFields...)` (in source order), body is
   `super(baseParams...)` followed by `self.fi = fi` for each subclass
   field.
3. Base has multiple constructors → no synthesis;
   `SubclassNeedsExplicitConstructorError`.
4. Base has only `private` constructors (Future Work; today this case
   cannot arise externally) → reject `extends` with
   `PrivateBaseConstructorError`.

The synthesized constructor is materialized as a real
`*ast.ConstructorElem` exactly like the non-`extends` case, so
Phase 3's super-related checks see the same shape as user-written
constructors.

Tests:
- `class Point { x: number, y: number }` → callable as `Point(1, 2)` with
  the parameters in declaration order.
- `class P { y: number, x: number }` (reversed declaration) → constructor
  takes `(y, x)`.
- `class Mixed { x: number, y?: number }` → synthesized ctor takes only
  `x`.
- A class with a computed-key required field → error.
- A class with only optional fields → callable as `Foo()`.
- A class with a `FieldElem` carrying a default value (`x: 0` /
  `x = 0`) → `FieldDefaultNotAllowedError`.
- `class Dog extends Animal {}` where `Animal` has one ctor → `Dog`
  forwards.
- `class Dog extends Animal { breed: string }` where `Animal` has one
  ctor → `Dog(name, breed)` synthesized.
- `class Dog extends Vec3 {}` where `Vec3` has three ctors →
  `SubclassNeedsExplicitConstructorError`.

---

## Phase 3: Definite-Assignment Analysis

**Goal:** Enforce the requirements §"Definite-Assignment Rule":
- All non-optional fields must be assigned on every reachable exit path
  before the constructor returns.
- Reads of `self.foo` are gated on `foo` being initialized.
- Method calls on `self`, passing `self`, returning `self`, and any aliasing
  of `self` are all gated on **all** non-optional fields being initialized.

### 3.1 Pass Structure

A new pass (called from the constructor body checker added in 2.3) walks
the constructor body's statements, maintaining a `set[FieldName]` of
definitely-assigned fields.

**File:** `internal/checker/init_check.go` (new)

```go
type initCheckState struct {
    initialized map[string]bool   // field name → definitely assigned
    requireAll  map[string]bool   // non-optional fields
    selfAliased bool              // true once self has been aliased
}

func (c *Checker) checkConstructorInit(
    ctx Context,
    classDecl *ast.ClassDecl,
    ctor *ast.ConstructorElem,
) []Error
```

Use the existing `internal/liveness` CFG infrastructure as the basis — it
already does flow-sensitive bit-vector analysis for liveness/aliasing
(see [planning/lifetimes/implementation_plan.md](../lifetimes/implementation_plan.md)
Phases 3–4). Definite-assignment is the dual of liveness: a *forward*
data-flow pass with set-intersection at join points.

### 3.2 What `requireAll` Contains

A field name `f` is in `requireAll` iff:
- It is declared as an instance field (`!Static`), AND
- It is not optional (the field's type annotation is not `T?` / does not
  resolve to `T | undefined`).

Optional fields can be reassigned by a constructor, but their absence at
the exit point is fine. (Field-level defaults are no longer permitted —
see Phase 2.4.)

### 3.3 Per-Statement Effects

Walk the constructor body's `Block` statements. For each expression and
statement:

- `self.foo = expr` → after evaluating `expr`, add `foo` to `initialized`.
  Crucially, evaluating `expr` is gated by 3.4's read rules.
- `self.foo` (read) → error if `foo` not in `initialized`.
- `self.method(...)` or any expression that reads `self` other than as the
  LHS target of a property-write → error if `requireAll` is not a subset of
  `initialized`.
- `val r = self`, passing `self` to a function, returning `self`, capturing
  `self` in a closure → error unless `requireAll ⊆ initialized`. Set
  `selfAliased = true` afterward; from that point on, treat any subsequent
  field write as still permitted but no longer "definitely first" (matters
  if delegation is added later).
- Any call to a free function or property method on a non-`self` value is
  unaffected.
- `throw expr` → terminates the path; subsequent statements are unreachable
  on that path. The exit-point check skips paths that always throw.

### 3.4 Control Flow

Reuse the CFG built by `internal/liveness`. At each merge point, the set
of initialized fields is the **intersection** of incoming sets.

**Loops are disallowed inside constructor bodies for now.** Encountering a
`for`, `while`, or `loop` statement inside a constructor reports
`LoopInConstructorNotSupportedError`. This sidesteps the
"is the loop body provably entered?" question for the initial
implementation; we can relax this later (e.g. allow loops that provably
execute at least once, or require all required fields to already be
initialized before the loop). `if`/`else` and `match` branches are
supported as in the requirements doc's `Range` example.

**`try`/`catch`/`finally` is also disallowed inside constructor bodies**
for the same reason — definite-assignment across exception edges (a
write in `try` may not be visible in `catch`) is non-trivial.
Encountering `try` inside a constructor reports
`TryInConstructorNotSupportedError`. Free-function `try`/`catch` around
a constructor call is unaffected. A bare `throw` is fine; only
catch-handlers are forbidden.

The exit check: at every reachable exit (fall-through of the block, every
`return` statement), `requireAll ⊆ initialized` must hold.

### 3.5 Type-Level Read Gating

The requirement that reads of uninitialized fields are errors needs the
checker to know **which** field is being read. Reads through a fully-
elaborated `self.foo` member expression are easy. Reads and writes
through computed keys (`self[key]`) are gated as follows:

- Allowed when `key`'s type is a **literal string type** (or union of
  literal string types) and every literal in the union names a declared
  field of the class. The access is treated exactly as if the source
  had written `self.<literal>` — for a write, the named field is added
  to `initialized`; for a read, the field must already be in
  `initialized`. For a union of literal string types, the access is
  conservative: a read requires every literal's field to be
  initialized; a write only adds a field to `initialized` if the union
  is a singleton.
- Otherwise, indexed access on `self` is rejected with
  `ComputedSelfAccessBeforeInitError` until all required fields are
  initialized. This covers the cases where `key` is a non-literal
  string, a computed expression, or names something that is not a
  declared field.

This is not a major loss — there is no realistic constructor pattern that
needs general computed access of `self` before initialization is complete.

### 3.6 New Errors

- `FieldNotInitializedError{FieldName}` — at exit point.
- `ReadBeforeInitError{FieldName}` — at the read site.
- `MethodCallBeforeInitError{MissingFields}` — at the call site.
- `SelfAliasBeforeInitError{MissingFields}` — at the alias site.
- `ComputedSelfAccessBeforeInitError` — for `self[…]` before all required
  fields are set.
- `LoopInConstructorNotSupportedError` — `for`/`while`/`loop` inside a
  constructor body. Temporary restriction; relax once we have a story
  for loop-body definite-assignment.
- `TryInConstructorNotSupportedError` — `try`/`catch`/`finally` inside a
  constructor body. Same temporary status as the loop restriction.

### 3.7 Tests

**File:** `internal/checker/tests/init_check_test.go` (new)

Cover the requirements doc's examples verbatim:
- `Point` with two ordered assignments — OK.
- `User` with two-arg ctor leaving `age` unassigned — error.
- `User` with raw-object ctor reading `self.name` before assignment — error.
- `Email` with pre-assignment validation using `val parts = …` — OK.
- `Range` with both branches assigning both fields — OK.
- `Range` with conditional assignment in only one branch — error.
- `val r = self` inside ctor before init complete — error.
- `self.method()` call before init complete — error.
- `for x in xs { … }` inside a ctor → `LoopInConstructorNotSupportedError`.
- `try { … } catch e { … }` inside a ctor → `TryInConstructorNotSupportedError`.

Also include happy-path: passing through a single all-defaults class still
infers correctly with no constructor required.

### 3.8 `super(...)` in Subclass Constructors

For classes that `extends` a base class, the definite-assignment pass
treats `super(...)` as a special statement:

- `super(...)` performs overload resolution against the base class's
  constructors and is permitted at most once per reachable path.
- Before the `super(...)` call: writes to subclass-declared fields are
  permitted; reads of `self`, calls through `self`, and aliasing `self`
  are forbidden (`SelfBeforeSuperError`).
- After `super(...)` returns: every base-class field is added to
  `initialized`. The remaining definite-assignment rules for subclass
  fields are unchanged.
- A subclass constructor whose reachable exit paths do not all call
  `super(...)` exactly once reports `MissingSuperCallError` /
  `MultipleSuperCallsError` as appropriate.

Tests:
- `class Dog extends Animal { … constructor(name, breed) { super(name); self.breed = breed } }` → OK.
- Subclass constructor that reads `self.x` before `super(...)` → error.
- Subclass constructor that omits `super(...)` on one branch → error.
- Subclass constructor that calls `super(...)` twice → error.

---

## Phase 4: Combined Cut-Over — Single-Ctor Codegen, Fixture Migration, Removal

**Goal:** Cut the codebase over to the new model in a single landing,
while still restricted to one in-body constructor per class. This phase
replaces the existing single-constructor codegen path
(`internal/codegen/builder.go:799`) with the new single-constructor
codepath, **and at the same time** migrates every in-tree `.esc`
fixture to the new syntax and removes the primary-ctor syntax + `data`
modifier.

These three pieces are bundled because they cannot land separately
without breaking the in-tree fixtures: the codegen change drops the
old field-init logic that primary-ctor classes depended on, the fixture
migration removes those classes, and the parser/AST/checker cleanup
removes the dead syntax that the migrated fixtures no longer use.
Multi-constructor support is deferred to Phase 5; until then,
`MultipleConstructorsNotYetSupportedError` from Phase 2 still rejects
classes with more than one in-body `constructor`.

### 4.1 Inputs

- `*ast.ClassDecl`
- The single `*ast.ConstructorElem` in the class body (user-written or
  synthesized per Phase 2.7)

### 4.2 Emission Strategy

For Phase 4, every class has exactly one in-body constructor. Emit it
as a plain JS `constructor(x, y, …)` body — no rest-parameter wrapper,
no dispatch logic. **Skip `Fn.Params[0]` (the `mut self` parameter)**
when emitting the JS parameter list: it is implicit at the call site
and bound as `this` inside the JS body. Body statements translate
directly, with `self.x` rewriting to `this.x` (mirrors method-body
codegen).

If a parameter has a default value (`constructor(mut self, x: T = expr)`),
emit the standard JS default-parameter form: `constructor(x = expr) { … }`.
(Field-level defaults no longer exist — see requirements
§"Field Declarations".)

**`super(...)` in subclass constructors.** Escalier `super(...)`
translates directly to a JS `super(...)` call. Argument forwarding is
whatever the constructor body wrote — no rewriting needed.

### 4.3 Fixture Migration

Hand-edit every `.esc` file that uses primary-constructor syntax. The
codebase is small enough that this is tractable manually; no codemod is
needed.

Find them with `grep -rn 'class \w\+(' --include='*.esc'`. Known sites
include `fixtures/class_with_fluent_mutating_methods/`,
`fixtures/class_with_static_members/`, `fixtures/class_with_computed_members/`,
`fixtures/class_with_generic_methods/`, `fixtures/class_with_getter_setter/`,
`fixtures/readonly_properties/`, `fixtures/pattern_matching/`, every
`fixtures/extractor_*/`, plus any `.esc` files under
`internal/checker/tests/` and `internal/parser/tests/`. The grep
command above is the source of truth — sweep what it returns.

Mechanical rewrite:

```
class Foo(p1: T1, p2: T2) {
    p1,
    p2,
    other: "foo",
    method(self) { … },
}
```
becomes
```
class Foo {
    p1: T1,
    p2: T2,
    other: string,

    constructor(mut self, p1: T1, p2: T2) {
        self.p1 = p1
        self.p2 = p2
        self.other = "foo"
    },

    method(self) { … },
}
```

In the common case where the primary constructor's params match the
instance-field declaration order one-for-one (which most existing classes
do) and the class has no field-level defaults, drop the explicit
`constructor` block — the synthesizer (Phase 2.7) produces the
equivalent:

```
class Foo {
    p1: T1,
    p2: T2,

    method(self) { … },
}
```

The simplification rule: skip the explicit `constructor` whenever the
constructor would be exactly the synthesizer's output — parameter list
equals the in-order list of non-optional fields, and the body is a
sequence of `self.fi = fi` assignments in the same order.

Classes that relied on **closure-captured constructor params** inside
methods (`infer_module.go:1102` calls these out) need a manual touch:
either declare a corresponding (eventually-private) field, or pass the
value explicitly.

`data class Config(...) { ... }` fixtures: drop `data` and either let
the inferred default apply or annotate use sites explicitly.

Update snapshot fixtures: `UPDATE_SNAPS=true go test ./...`.

### 4.4 Parser Removal

**File:** `internal/parser/decl.go`

Delete the `if token.Type == OpenParen { … params = parseDelimSeq(…) }`
block (currently around line 156). Encountering `(` after the class name
(and optional type params) now reports `"primary constructors are no
longer supported; declare an explicit `constructor` block in the class
body"`.

Also drop the `data` contextual-keyword logic:
- `internal/parser/stmt.go:124` and `internal/parser/decl.go:106` —
  remove the promotion of `data` from identifier to keyword when
  followed by `class`.
- `internal/parser/decl.go:136` — remove the `data bool` parameter on
  `classDecl` and its call sites.

### 4.5 AST Cleanup

**File:** `internal/ast/class.go`

Remove `ClassDecl.Params` and `ClassDecl.Data`. Update `NewClassDecl`
and every call site (notably `internal/interop/decl.go:218`). Sweep the
codebase with `grep -rn 'decl.Params\|\.Data' internal/checker
internal/codegen` for class-related usages and delete the dead
branches.

### 4.6 Type Checker Cleanup

**File:** `internal/checker/infer_module.go`

- Delete the `inferFuncParams(declCtx, decl.Params)` call and the
  surrounding branch in the placeholder phase.
- Delete the `paramBindingsForDecl[decl]` plumbing that copies primary
  ctor params into method bodies (the comment at lines 1091–1101
  explicitly describes the soon-to-be-removed behavior).
- Delete the FieldElem fallback that resolves a no-value, no-annotation
  field from a same-named primary-ctor param (`infer_module.go:1044`
  area — the `if bodyElem.Value != nil { … } else { … }` branch on
  the `else` side).

**File:** `internal/checker/infer_lifetime.go`

`InferConstructorLifetimes` (called at `infer_module.go:614`) currently
operates on `decl.Params`. Refactor to operate on the (single)
`ConstructorElem` in `decl.Body`, per requirements §"Lifetimes": the
constructor allocates fresh `LifetimeVar`s for its reference-typed
parameters that are stored as fields, and stamps the result onto its
own `FuncType`. Phase 5 extends this to multiple constructors.

Also remove any branch that consults `decl.Data` to override default
mutability. Per requirements §"Default Mutability", constructor calls
always return immutable; the `data` modifier is gone, and the class
default-mutability bit now serves only to record "does the class have
`mut self` methods?" (used by inference at use sites — *not* at
construction).

### 4.7 Codegen Cleanup

**File:** `internal/codegen/builder.go`

Replace the field-init logic at `builder.go:834–903` with the new
single-constructor codegen described in 4.2. Also:

- Delete `b.classParamNames` field on `Builder` and all the surrounding
  lifecycle plumbing (`builder.go:802–818`, `:825`, `:966`).
- Delete the `fieldNames` / param-fallback block (`:923–951`).
- Delete `b.buildParams(d.Params)` for class decls.

After this phase, `internal/codegen/builder.go` has zero references to
class-header parameters. The new model: fields are only ever assigned
by `self.foo = …` statements inside a constructor body (or via
`super(...)` for inherited fields), and those translate directly. There
is no implicit "match field to same-named param" anymore.

### 4.8 Tests

**Files:** `internal/codegen/builder_test.go` (existing) and updated
parser/checker tests.

Codegen:
- Single-arity dispatch: `class Point { constructor(mut self, x, y) { … } }` →
  `constructor(x, y) { … }` (note `mut self` is dropped from the JS
  parameter list; only the callable params remain).
- Class with no constructor and required fields → emitted JS reflects
  the synthesized constructor (params in field declaration order,
  sequence of `this.fi = fi` writes).
- Subclass constructor with `super(...)` → emitted JS uses
  `super(...)` directly.

Parser/checker:
- Confirm `class Foo(...)` reports the expected "no longer supported"
  error.
- Confirm `data class Foo { ... }` is now a parse error.

Confirm the entire test suite passes — this phase is the integration
milestone for single-constructor classes.

---

## Phase 5: Multiple Constructors

**Goal:** Allow multiple `ConstructorElem`s per class. At the type level,
the class's constructor becomes an overload set. Same-arity overloads are
permitted only if they are runtime-distinguishable. Codegen merges all
declared constructors into a single JS `constructor` body that dispatches
at runtime. This phase removes
`MultipleConstructorsNotYetSupportedError` introduced in Phase 2.

### 5.1 Type Representation

`type_system.ConstructorElem` (`internal/type_system/types.go:1010`)
currently wraps a single `*FuncType`. Two options:

**Option A — multi-element:** The class's static `ObjectType` carries
multiple `ConstructorElem`s, one per declared constructor. Overload
resolution code paths consume the full set.

**Option B — single overloaded `FuncType`:** Extend `FuncType` to optionally
hold an overload list (`Overloads []*FuncType`). One `ConstructorElem`
points at the overloaded `FuncType`.

Recommendation: **Option A.** It surfaces the multiplicity directly, makes
diagnostics easier ("two constructors with identical arity and indistinguishable
types"), and avoids leaking constructor-specific machinery into the general
function type. Overload resolution sees an `[]ConstructorElem` and picks
one.

### 5.2 Definition-Phase Wiring

Update Phase 2's branch (`infer_module.go`) to:

1. Collect every `*ast.ConstructorElem` in the body.
2. For each, infer its `FuncType` (params + type params + return = `Self`).
3. Build one `type_system.ConstructorElem` per inferred signature.
4. Append all of them to `classObjTypeElems` before the static methods.

Each constructor body is checked independently in 2.3's branch — no shared
state across constructors. Remove
`MultipleConstructorsNotYetSupportedError` from the checker; the bound
in Phase 2 only existed because dispatch wasn't ready.

### 5.3 Lifetime Refactor

`InferConstructorLifetimes` was refactored in Phase 4.6 to operate on
the single `ConstructorElem` in `decl.Body`. Extend it now to handle
the (possibly multiple) `ConstructorElem`s, per requirements
§"Lifetimes": each constructor independently allocates fresh
`LifetimeVar`s for its reference-typed parameters that are stored as
fields, and stamps the result onto its own `FuncType`. Two constructors
that store references into the same field do not share lifetime
variables.

### 5.4 Overload Resolution at Call Sites

Constructor overload resolution reuses the function-overload resolver.
The call-site checker for `ClassName(args...)`:

1. Collects every constructor whose parameter list the argument list is
   assignable to (per-arg `Unify`, with optional/rest-parameter handling).
2. If exactly one matches → selected.
3. If zero match → `NoMatchingConstructorError`.
4. If two or more match → `AmbiguousConstructorError`. Resolution is
   **not** declaration-order-sensitive (modules have no well-defined
   order across files, so privileging "first" would be unsound).

### 5.4a Overload-Set Validity at Class Definition

In addition to the call-site rule, the class itself is rejected at
definition time if any pair of its constructors are mutually assignable
(i.e. there exists a parameter list both could legally match). This
mirrors the function-overload-set check and removes the possibility of
ever reaching `AmbiguousConstructorError` for argument types that are
fully concrete.

- `AmbiguousConstructorOverloadsError{CtorA, CtorB}` is reported on the
  class span, naming the two offending constructors.
- The check is the same routine used for top-level function overloads;
  factor it out if not already shared.

### 5.5 Runtime-Distinguishability Analysis

For codegen (5.7) to emit dispatch logic, same-arity constructors need
a discriminator. Compute this once per class during the placeholder
or definition phase:

```go
// internal/checker/ctor_dispatch.go (new)

type CtorDispatch struct {
    Plan []CtorDispatchCase  // ordered: first match wins at runtime
}

type CtorDispatchCase struct {
    Ctor      *type_system.ConstructorElem
    Arity     int
    HasRest   bool          // true if last param is `...rest`
    Guards    []ParamGuard  // empty when arity uniquely identifies the ctor
}

type ParamGuard struct {
    ParamIndex int
    Kind       GuardKind  // typeofNumber, typeofString, typeofBoolean,
                          // typeofObject, arrayIsArray, instanceofClass,
                          // hasProperty
    Detail     string     // class name for instanceof, property name for hasProperty
}
```

Algorithm, per arity bucket of size > 1 (parameter indexing here refers
to the **callable** params, i.e. `Fn.Params[1:]` — the leading `mut self`
is excluded throughout dispatch analysis):

1. For each pair of constructors `(A, B)` in the bucket, find the
   smallest parameter index `i` where `A.Params[i].Type` and
   `B.Params[i].Type` admit a non-empty disjoint runtime witness — i.e.
   one of:
   - `typeof === "number" | "string" | "boolean"` (primitive vs. anything
     non-primitive on that side)
   - `typeof === "object" && x !== null` (any non-null object vs. any
     primitive on the other side)
   - `Array.isArray` (array vs. non-array object)
   - presence of a discriminating property (object types whose required
     property sets differ; pick a property only on one side)
   - `instanceof Foo` (nominal class vs. anything-not-Foo)
2. If no pair has a discriminator → `ConstructorsNotRuntimeDistinguishableError`,
   listing the two offending constructors.
3. Otherwise, build the `Guards` list for each ctor in the bucket — do
   what we do for normal function overload dispatch.

When all ctors have distinct arities, the bucket is size 1 and no guards
are needed; codegen dispatches purely on `arguments.length`.

**Rest constructors.** A constructor whose last formal is `...rest: U[]`
has minimum arity `N - 1` and matches any `args.length >= N - 1`. Place
each rest constructor in its own bucket keyed by minimum arity, tried
**after** the fixed-arity bucket of the same arity (per requirements
§"Rest Parameters"). For each pair `(rest, fixed)` at the same minimum
arity, run the same discriminator search as above on a non-rest
parameter index `i < N - 1`; if none exists, report
`ConstructorsNotRuntimeDistinguishableError`. Two rest constructors
with overlapping argument counts (i.e. with min-arities `m1 <= m2`
where any count `>= m2` is matched by both) likewise need a non-rest
discriminator on a leading parameter, otherwise
`AmbiguousConstructorOverloadsError` at definition time.

### 5.6 New Errors

- `ConstructorsNotRuntimeDistinguishableError{CtorA, CtorB}` — same arity,
  no discriminator found.
- `NoMatchingConstructorError` (extends existing call-site error).
- `AmbiguousConstructorError` — reachable only with type-erased arguments
  (e.g. `unknown`); fully-concrete argument lists never hit this since
  `AmbiguousConstructorOverloadsError` rejects ambiguous overload sets at
  class-definition time.
- `AmbiguousConstructorOverloadsError{CtorA, CtorB}` — see 5.4a.

Also: remove `MultipleConstructorsNotYetSupportedError` (introduced in
Phase 2.5), since multi-constructor classes are now supported.

### 5.7 Codegen — Merged Dispatch

Replace the single-constructor codegen from Phase 4.7 with a merged
constructor that takes a JS rest parameter (`...args`) and dispatches
in declaration order. Every arm carries a **positive** guard — the codegen
does not rely on fall-through ordering to disambiguate (this matches the
example in requirements §"Codegen — Constructor Merging"):

```js
constructor(...args) {
    if (/* positive guard for ctor #0 */) {
        // body of ctor #0, with parameter destructuring from args
        return;
    }
    if (/* positive guard for ctor #1 */) {
        // body of ctor #1
        return;
    }
    // …
    throw new TypeError("no matching ClassName constructor");
}
```

Per-ctor guard:
- Arity-only: `args.length === N`
- Rest-arity-only: `args.length >= N - 1` (rest as last formal,
  declared parameter count `N`)
- Arity + ParamGuard: `args.length === N && /* per-param guards joined with && */`
  (replace `===` with `>=` for the rest case)
- For `typeofNumber` on param i: `typeof args[i] === "number"`
- For `typeofObject` on param i: `typeof args[i] === "object" && args[i] !== null`
- For `arrayIsArray` on param i: `Array.isArray(args[i])`
- For `instanceofClass` on param i: `args[i] instanceof Foo`
- For `hasProperty` on param i: `args[i] != null && "k" in args[i]`

Per-ctor body:
- Bind each formal parameter to its index: `const x = args[0]; const y = args[1];`
- If a parameter has a default value (`constructor(mut self, x: T = expr)`),
  emit the standard JS default-parameter form when binding: `const x = args[0]
  !== undefined ? args[0] : expr;`. The `mut self` parameter is skipped
  (it is `this` in the emitted body). (Field-level defaults no longer
  exist — see requirements §"Field Declarations".)
- Inline the constructor's body statements, with `self.x` rewriting to
  `this.x` (mirrors method-body codegen).
- Emit `return;` at the end.

When the class still has exactly one constructor and its arity is known
(no rest params), keep the Phase 4 emission — drop the `if`/rest wrapper
and emit a plain `constructor(x, y, …)` body. This preserves clean
output for the common case.

The dispatch plan from 5.5 is attached to the class's
`type_system.TypeAlias` (or whatever side-channel function overloads
use), so codegen reads it from the type, not by re-walking the AST.
Mirror, don't invent.

**`super(...)` in subclass constructors** translates directly to a JS
`super(...)` call. Argument forwarding is whatever the constructor body
wrote — no rewriting needed. The merged-dispatch wrapper of a subclass
calls the per-ctor body, which contains the `super(...)` in source
order; nothing additional is required at the dispatch level.

### 5.8 Tests

**Files:** `internal/checker/tests/multi_constructor_test.go` (new) and
`internal/codegen/multi_ctor_test.go` (new).

Checker:
- The `Vec3` example from the requirements doc — three constructors of
  arities 3, 1 (number), 1 (object) → no error, dispatch plan computed.
- Two same-arity constructors with the same parameter types → error.
- Two constructors of arities 1 and 2 → no guards needed.
- A constructor pair distinguished by `instanceof` on a class type.
- A constructor pair distinguished by a discriminating property.
- A call site `Foo(x)` where `x: unknown` and `Foo` has two
  type-disjoint same-arity constructors (e.g. `(number)` and `(string)`)
  → `AmbiguousConstructorError` (exercises the "type-erased argument"
  path; without this test the error is unreachable).
- A constructor with a rest param interacting with same-arity overloads.
  Per requirements §"Rest Parameters": rest constructors live in their
  own dispatch bucket and are tried after fixed-arity constructors at
  the same minimum arity; same-min-arity rest + fixed pairs require a
  runtime discriminator on a non-rest parameter, otherwise
  `ConstructorsNotRuntimeDistinguishableError`.

Codegen:
- Multi-arity dispatch by `arguments.length`.
- Same-arity dispatch with `typeof` discriminator.
- Same-arity dispatch with `typeof "object"` discriminator (the Vec3
  case from requirements.md).
- Same-arity dispatch with `instanceof` discriminator.
- Single-constructor classes (post-Phase 4) still emit a plain
  `constructor(x, y, …)` body — the merged-dispatch wrapper only
  appears when there are 2+ constructors.

---

## Phase 6: Polish

**Goal:** Take care of the loose ends.

### 6.1 Error Messages

Every error introduced in Phases 2–5 deserves a high-quality message:
- `FieldNotInitializedError` should list every missing field at the exit
  point and point at the constructor's open brace.
- `ReadBeforeInitError` should highlight the read and add a "field is
  initialized at" note pointing at the eventual write (if there is one
  later in the body).
- `ConstructorsNotRuntimeDistinguishableError` should highlight both
  conflicting constructors with a help message describing what makes
  signatures distinguishable.

### 6.2 Documentation

- Update [docs/](../../docs/) — the language reference must describe
  explicit constructors and the definite-assignment rule.
- Add a migration note under `planning/class_constructor/` describing
  the breaking change and the manual-touch cases.
- Update any slides / examples in `slides/` that show the primary-ctor
  syntax.

### 6.3 LSP / Editor Affordances

- Constructor body completion: inside a constructor, the autocomplete
  should suggest `self.<uninitialized-field> = ` first.
- Hover on the class name at a call site should list overload signatures.
- Diagnostic for "this constructor is reachable but missing" (if a class
  has no constructor and at least one field requires initialization,
  guide the user to add one).

These are nice-to-haves; ship the breaking change first.

---

## Out of Scope (Future Work)

- **Private constructors** (requirements §"Private Constructors"): the
  parser already accepts `private constructor` (Phase 1), but the checker
  rejects it. Lifting that and removing `class Foo private(...)` syntax
  is a follow-up.
- **`Self(...)` delegation between constructors** (requirements
  §"Constructor Delegation"): defer until the base proposal lands and
  ergonomic gaps are clear.
- **Same-arity dispatch error UX and explicit discriminator annotations**
  (the open question in requirements §"Open Questions"): leave as the
  rough error from Phase 5 for now.
