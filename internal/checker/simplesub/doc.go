// Package simplesub is a throwaway proof-of-concept — Milestones M0/M1 of the
// algebraic-subtyping de-risking plan — implementing the core of Lionel
// Parreaux's "Simple-sub" algorithm:
//
//   - fresh type variables that carry lower/upper *bound lists* plus a level,
//   - a constrain(lhs <: rhs) primitive with a coinductive seen-cache, plus
//     level-aware extrusion,
//   - level-based let-generalization (instantiate / freshenAbove),
//   - a simplification pass, and
//   - polarity-driven coalescing into a production type_system.Type, rendered
//     with the real printer (type_system.PrintType) so the result can be
//     string-compared against the existing checker test expectations.
//
// Driven by a tiny hand-built expression IR (the parser bridge is a later
// milestone).
//
// M1 simplification: single-polarity elimination (a variable occurring in only
// one polarity is replaced by the union/intersection of its bounds, so e.g.
// id(5) yields `5`, not `T0 | 5`) plus co-occurrence variable merging (variables
// that mutually co-occur in every polarity they appear in are unified, so
// InnerCapturesOuterParam coalesces to `fn <T0>(y: T0) -> [T0, T0]`).
//
// M2 adds records and usage-based inference: member access `obj.bar` constrains
// the receiver to `{bar: <fresh>}`, so a parameter's required shape accumulates
// as upper bounds and coalesces (negative position) to a record — object bounds
// in an intersection are merged into one record.
//
// M3 adds `mut` (invariant mutable references) via the read/write decomposition:
// a Mut's content occurs both covariantly (read) and contravariantly (write), so
// constraining two Muts forces equality in both directions. This is the
// highest-risk gate — invariance is not native to algebraic subtyping — and it
// shows the decomposition encodes it cleanly: e.g. `mut {x,y} <: mut {x}` fails
// even though immutable `{x,y} <: {x}` succeeds by width subtyping.
//
// M3 also infers mutability from usage: a field assignment `obj.x = v`
// constrains the receiver to `mut {x: widen(typeof v)}` (literals widen to their
// primitive on write), and multiple writes merge into one mutable record, so
// `fn foo(obj) { obj.x = 5; obj.y = 10 }` infers
// `fn (obj: mut {x: number, y: number}) -> void`. A write also records the
// field's type per receiver variable, so a later read of the same field returns
// the written type — `fn foo(obj) { obj.x = 5; return obj.x }` infers
// `fn (obj: mut {x: number}) -> number`.
//
// M4 adds lifetimes as a SECOND SORT solved by the same constraint machinery
// (see lifetime.go): a LifetimeVar carries lower/upper bounds over the
// "outlives" lattice ('static = top), and constrainLt mirrors constrain. A `mut`
// record parameter is a borrow, so it gets a fresh lifetime; returning it shares
// that lifetime by value identity (`fn <'a>(p: mut 'a {x}) -> mut 'a {x}`);
// returning one of several borrows unions their lifetimes via a fresh join
// variable (`mut ('a | 'b) {x}`); a borrow that escapes to static storage is
// constrained `<: 'static` and renders `mut 'static {x}`. Lifetime elision drops
// a param lifetime that connects nothing (the lifetime-sort analogue of
// single-polarity elimination). This demonstrates the thesis that the
// production checker's multi-phase infer_lifetime.go collapses into ordinary
// constraint solving over a second sort.
//
// Lifetimes attach to type aliases (Alias) exactly as they do to records: a
// `mut` borrow of an alias-typed value carries a lifetime that renders before
// the alias name (`fn <'a>(p: mut 'a Point) -> mut 'a Point`), while a by-value
// alias parameter borrows nothing and renders bare (`fn (p: Point) -> number`).
// An Alias is structurally its body for subtyping. A by-value parameter never
// carries a lifetime — only `mut` borrows do — matching the production checker,
// where even an unbounded `mut T` is lifetime-free.
//
// M5 adds "Baseline D" type-level operators (see typeops.go), kept separate from
// the value-expression solver: conditional types (`if T : U { X } else { Y }`,
// with `infer` and union distribution), `keyof T`, and indexed access `T[K]`.
// An operator reduces only when its operands are ground (no unresolved type
// parameter) — the common case of a generic alias applied to concrete arguments
// — and otherwise stays symbolic (`keyof Foo`, `Foo["x"]`). This is a TypeEvaluator
// over TyExprs producing concrete type_system.Types directly.
//
// M7 adds Design A — residual type-operators + a post-solve fixpoint (see
// residual.go) — for the case M5 leaves symbolic: an operator whose operand is a
// value whose type is inferred from usage, hence not ground during the value
// solve. A ResidualOp (keyof / indexed access over a value-inference SimpleType)
// is inert during constraint solving — it carries no bounds and constrain never
// touches it, so Design A adds NO new mutable solver state — and reduces at
// coalescing once its operand has a concrete shape, in a fixpoint bounded by
// maxResidualRounds (the termination guard). So `fn f(x) { x.a; x.b; return
// keyof typeof x }` reduces the keyof to `"a" | "b"` post-solve, where M5 would
// have stalled. An operand that never gains object structure leaves the operator
// symbolic (`keyof unknown`) as the fixpoint's terminating result. Designs B/C
// remain out of scope; M7 validates only the recommended Design-A backbone.
//
// Variable bounds live on the spike-local Variable struct, never on
// type_system.TypeVarType — the shared type system stays untouched.
//
// Source layout:
//
//   - polarity.go  — the Polarity enum.
//   - types.go     — the SimpleType representation (Variable, Primitive, ...).
//   - constrain.go — the constrain(lhs <: rhs) primitive and extrusion.
//   - scheme.go    — let-polymorphism: type schemes, instantiate, freshenAbove.
//   - infer.go     — the expression IR, typeTerm, and the public Infer/Render.
//   - simplify.go  — occurrence analysis and co-occurrence variable merging.
//   - coalesce.go  — coalescing a SimpleType into a type_system.Type.
package simplesub
