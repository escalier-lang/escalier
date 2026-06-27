// Package solver is the production algebraic-subtyping engine for Escalier's new
// checker, promoted from the internal/simplesub spike (Milestone M1 of the
// SimpleSub migration). It implements the core of Lionel Parreaux's "Simple-sub"
// algorithm over the structural-core subset of the type system:
//
//   - fresh inference variables that carry lower/upper *bound lists* plus a level
//     (for let-generalization in a later milestone),
//   - a constrain(sub <: super) primitive with a coinductive seen-cache, plus
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
// (soltype.PrintAsScheme).
//
// M3 (PR2) completes scheme rendering with CO-OCCURRENCE merging (simplify.go):
// distinct quantified variables that always appear together are unioned over a
// symmetrized bound graph, so coalesceScheme renders them as one type parameter.
// The parameter in
//
//	val outer = fn (y) {
//		val getY = fn () { return y }
//		return [getY(), getY()]
//	}
//
// reaches both tuple elements through two result variables, so the raw
// `fn <T0, T1>(y: T0 & T1) -> [T0, T1]` becomes `fn <T0>(y: T0) -> [T0, T0]`.
// Simplification runs at display time, leaving the raw scheme body intact for
// instantiation.
//
// M3 (PR5) adds the probe (probe.go): a speculation journal over the engine's
// bound-list mutations (a per-variable length snapshot, truncated back on
// discard) plus side-table (Info/Prov/errs) rollback closures, with push/pop
// nesting that hands a committed child's rollback obligation up to its parent. The active
// probe lives on *Context (next to the bound-mutating constrain/extrude); the
// open/close discipline lives on the checker carrier. PR6's overload resolution
// is its first consumer — each candidate is trialled under a probe and the losers
// rolled back — but it is general speculation infrastructure reused beyond M3.
//
// M3 (PR6) adds function overloading for free functions (overload.go): a name with
// more than one top-level FuncDecl binds to a multi-scheme ValueBinding
// (b.IsOverloaded()), one scheme per arm. Resolution is a phase DISTINCT from
// constrain — the "callable in several ways" disjunction stays out of the subtype
// lattice — so a direct overloaded call routes through resolveOverload, which trials
// the arms (reusing the PR5 probe) and commits the winner. Arms are tried
// most-specific-first when no argument is an unconstrained variable, and fall back to
// declaration order otherwise. The one scoped lattice exception is the VALUE-position
// type of an overloaded name — the intersection of its arms — which is collapsed back to a
// single arm only when it meets a concrete call shape.
//
// What is still deferred (each lands in a later milestone): records / mut /
// lifetimes (M4), classes and the general union/intersection *subtyping rules* in
// constrain (M5/M6), type-level operators (M5/M8), and DEFERRED OVERLOAD RESOLUTION
// when a call argument is still an unconstrained variable — today's first-match
// fallback over-narrows the enclosing function, and the real fix rides with M4/M5
// object-arg and method overloads (#723). M1 ships UnionType/IntersectionType *nodes*
// for coalesced output; their general lattice rules in constrain remain deferred (PR6
// adds only the function-intersection-sub arm above).
package solver
