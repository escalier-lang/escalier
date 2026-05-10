package interop

import "github.com/escalier-lang/escalier/internal/dts_parser"

// ResolutionTier identifies which tier in the eight-tier resolution order
// produced a mutability classification for a class member's receiver.
//
// The tiers match the requirements document:
//  1. @esctype tag (round-trip from Escalier source)
//  2. Explicit author signals (readonly this, getters/setters, Readonly<T>, readonly props)
//  3. User override files
//  4. Shipped overrides (stdlib, FP libraries)
//  5. Primitive wrapper classes (Number, BigInt, String, Boolean)
//  6. get* prefix rule (with documented exceptions)
//  7. Name-based heuristics
//  8. Default: mutating
type ResolutionTier int

const (
	TierEsctype          ResolutionTier = 1
	TierExplicitSignal   ResolutionTier = 2
	TierUserOverride     ResolutionTier = 3
	TierShippedOverride  ResolutionTier = 4
	TierPrimitiveWrapper ResolutionTier = 5
	TierGetPrefix        ResolutionTier = 6
	TierNameHeuristic    ResolutionTier = 7
	TierDefault          ResolutionTier = 8
)

// ClassifyResult is the outcome of Classify.
type ClassifyResult struct {
	Mut    bool           // true = receiver is mutating; false = non-mutating
	Source ResolutionTier // which tier produced this classification
}

// ClassifyContext carries the information needed to classify a class member's
// receiver mutability. Fields other than Member are optional and used by
// higher-numbered tiers.
type ClassifyContext struct {
	Member     dts_parser.ClassMember // the declaration being classified
	ClassName  string                 // enclosing class name (empty if none)
	ModulePath string                 // module path (empty if none)
}

// Classify determines the mutability of a class member's receiver using the
// eight-tier resolution order defined in
// planning/interop_mutability/requirements.md.
//
// Phase 1: only tier 8 (default to mutating) is implemented. Tiers 1–7 are
// stubbed in order so each subsequent phase can fill in its slot without
// restructuring this function.
func Classify(ctx ClassifyContext) ClassifyResult {
	// Tier 1: @esctype tag — Phase 6.

	// Tier 2: explicit author signals (readonly this, getters/setters,
	// Readonly<T> collection variant, readonly properties, well-known symbol
	// methods) — Phase 2.

	// Tier 3: user override files — Phase 3.

	// Tier 4: shipped overrides (stdlib, FP libraries) — Phase 4.

	// Tier 5: primitive wrapper classes — Phase 5.

	// Tier 6: get* prefix rule — Phase 5.

	// Tier 7: name-based heuristics — Phase 5.

	// Tier 8: default to mutating.
	return ClassifyResult{Mut: true, Source: TierDefault}
}
