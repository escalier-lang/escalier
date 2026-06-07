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
// M3 (PR1) adds let-generalization on top of M2's walk: TypeSchemes (poly.go —
// MonoScheme/PolyScheme + ValueBinding.Schemes), instantiate/freshenAbove for
// per-use fresh variables, generalization at the SCC boundary (module.go) and at
// body-level `val`s (infer_decl.go), the inferIdent instantiation hook, the
// FromInstantiation provenance edge (prov.go), and scheme rendering — occurrence
// analysis + single-polarity elimination retaining genuine type parameters
// (coalesce.go's coalesceScheme) with the printer's <T0, …> quantifier prefix
// (soltype.PrintAsScheme). What remains of the polymorphism-rendering bundle is
// PR2's CO-OCCURRENCE merging (distinct variables that always appear together),
// which makes renders compact where the same variable isn't already shared across
// positions; generalize's simplify hook is a no-op until then.
//
// M3 (PR5) adds the probe (probe.go): a speculation journal over the engine's
// bound-list mutations (a per-variable length snapshot, truncated back on
// discard) plus side-table (Info/Prov) rollback closures, with push/pop nesting
// that hands a committed child's rollback obligation up to its parent. The active
// probe lives on *Context (next to the bound-mutating constrain/extrude); the
// open/close discipline lives on the checker carrier. PR6's overload resolution
// is its first consumer — each candidate is trialled under a probe and the losers
// rolled back — but it is general speculation infrastructure reused beyond M3.
//
// What is still deferred (each lands in a later milestone): records / mut /
// lifetimes (M4), function overloading (M3 PR6), classes and the
// union/intersection *subtyping rules* in constrain (M5/M6), and type-level
// operators (M5/M8). M1 ships UnionType/IntersectionType *nodes* for coalesced
// output, but their lattice rules in constrain remain deferred.
package solver
