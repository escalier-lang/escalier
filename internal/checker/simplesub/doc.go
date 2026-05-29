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
// Records/usage inference (M2), `mut` invariance (M3), and lifetimes (M4) remain
// out of scope.
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
