// Package solver is the production algebraic-subtyping engine for Escalier's new
// checker, promoted from the internal/simplesub spike (Milestone M1 of the
// SimpleSub migration). It implements the core of Lionel Parreaux's "Simple-sub"
// algorithm over the structural-core subset of the type system:
//
//   - fresh inference variables that carry lower/upper *bound lists* plus a level
//     (for let-generalization in a later milestone),
//   - a constrain(lhs <: rhs) primitive with a coinductive seen-cache, plus
//     level-aware extrusion, and
//   - polarity-driven coalescing that inlines every variable to its bounds,
//     returning a native soltype.Type.
//
// The type representation, the printer, and the Polarity enum live in the
// sibling internal/soltype package; the engine (constrain/extrude),
// coalescing, and the mutable Context live here. solver imports soltype and
// internal/ast (for the
// Info side table's key type); neither ast nor type_system imports solver, so
// the package is acyclic and additive — it shares no code with type_system.
//
// What M1 deliberately omits (each lands in a later milestone): the AST-driven
// inference walk and parser/resolver bridge (M2), let-generalization and type
// schemes (M3), the polymorphism-rendering bundle — occurrence analysis,
// bipolar-variable retention, named-type-param refs, co-occurrence merging, and
// the quantifier prefix in the printer (M3), records / mut / lifetimes (M4),
// classes and the union/intersection *subtyping rules* in constrain (M5/M6), and
// type-level operators (M5/M8). M1 ships UnionType/IntersectionType *nodes* for
// coalesced output, but their lattice rules in constrain remain deferred.
package solver
