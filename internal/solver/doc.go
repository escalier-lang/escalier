// Package solver is the production algebraic-subtyping engine for Escalier's new
// checker. It implements the core of Lionel Parreaux's "Simple-sub" algorithm
// over the structural-core subset of the type system:
//
//   - fresh inference variables that carry lower/upper *bound lists* plus a level
//     for let-generalization,
//   - a constrain(sub <: super) primitive with a coinductive seen-cache, plus
//     level-aware extrusion, and
//   - polarity-driven coalescing that inlines every variable to its bounds,
//     returning a native soltype.Type.
//
// The type representation, the printer, and the Polarity enum live in the
// sibling internal/soltype package; the engine (constrain/extrude),
// coalescing, and the mutable Context live here. solver imports soltype and
// internal/ast for the Info side table's key type. Neither ast nor type_system
// imports solver, so the package is acyclic and additive, sharing no code with
// type_system.
//
// Let-generalization sits on top of the walk: TypeSchemes (poly.go —
// MonoScheme/PolyScheme + ValueBinding.Schemes), instantiate/freshenAbove for
// per-use fresh variables, generalization at the SCC boundary (module.go) and at
// body-level `val`s (infer_decl.go), the inferIdent instantiation hook, the
// FromInstantiation provenance edge (prov.go), and scheme rendering — occurrence
// analysis + single-polarity elimination retaining genuine type parameters
// (coalesce.go's coalesceScheme) with the printer's <T0, …> quantifier prefix
// (soltype.PrintAsScheme).
//
// Scheme rendering also performs CO-OCCURRENCE merging (simplify.go):
// distinct quantified variables that always appear together are unioned over a
// symmetrized bound graph, so coalesceScheme renders them as one type parameter.
// The parameter in
//
//	val outer = fn (y) {
//		val getY = fn () { return y }
//		return [getY(), getY()]
//	}
//
// reaches both tuple slots through two result variables, so the raw
// `fn <T0, T1>(y: T0 & T1) -> [T0, T1]` becomes `fn <T0>(y: T0) -> [T0, T0]`.
// Simplification runs at display time, leaving the raw scheme body intact for
// instantiation.
//
// The probe (probe.go) is a speculation journal over the engine's
// bound-list mutations — a per-variable length snapshot, truncated back on
// discard — plus side-table (Info/Prov/errs) rollback closures, with push/pop
// nesting that hands a committed child's rollback obligation up to its parent. The active
// probe lives on *Context (next to the bound-mutating constrain/extrude); the
// open/close discipline lives on the checker carrier. Overload resolution is one
// consumer — each candidate is trialled under a probe and the losers rolled back
// — but it is general speculation infrastructure.
//
// Function overloading for free functions (overload.go) binds a name with
// more than one top-level FuncDecl to a multi-scheme ValueBinding
// (b.IsOverloaded()), one scheme per arm. Resolution is a phase DISTINCT from
// constrain — the "callable in several ways" disjunction stays out of the subtype
// lattice — so a direct overloaded call routes through resolveOverload, which trials
// the arms under a probe and commits the winner. Arms are tried
// most-specific-first when no argument is an unconstrained variable, and fall back to
// declaration order otherwise. The one scoped lattice exception is the VALUE-position
// type of an overloaded name — the intersection of its arms — which is collapsed back to a
// single arm only when it meets a concrete call shape.
//
// Some behavior remains unmodeled. Overload resolution when a call argument is
// still an unconstrained variable falls back to first-match, which over-narrows
// the enclosing function. The general union/intersection subtyping rules in
// constrain are not yet wired. UnionType and IntersectionType *nodes* exist for
// coalesced output, but their general lattice rules in constrain are deferred;
// only the function-intersection-sub arm above is implemented.
package solver
