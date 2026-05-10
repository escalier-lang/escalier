package interop

import (
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/set"
)

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
func Classify(ctx ClassifyContext) ClassifyResult {
	// Tier 1: @esctype tag — Phase 6.

	// Tier 2: explicit author signals.
	if result, ok := classifyTier2(ctx); ok {
		return result
	}

	// Tier 3: user override files — Phase 3.

	// Tier 4: shipped overrides (stdlib, FP libraries) — Phase 4.

	// Tier 5: primitive wrapper classes — Phase 5.

	// Tier 6: get* prefix rule — Phase 5.

	// Tier 7: name-based heuristics — Phase 5.

	// Tier 8: default to mutating.
	return ClassifyResult{Mut: true, Source: TierDefault}
}

// classifyTier2 applies explicit author signals:
//   - Getters never mutate the receiver.
//   - Setters always mutate the receiver.
//   - Methods with a `readonly this` parameter are non-mutating.
//   - Methods on Readonly-prefixed collection classes (ReadonlyArray, etc.) are non-mutating.
//   - Well-known symbol methods (toString, toJSON, etc.) are non-mutating.
//   - readonly properties are non-mutating (principle #6).
func classifyTier2(ctx ClassifyContext) (ClassifyResult, bool) {
	nonMut := ClassifyResult{Mut: false, Source: TierExplicitSignal}
	mut := ClassifyResult{Mut: true, Source: TierExplicitSignal}

	switch m := ctx.Member.(type) {
	case *dts_parser.GetterDecl:
		return nonMut, true

	case *dts_parser.SetterDecl:
		return mut, true

	case *dts_parser.PropertyDecl:
		if m.Modifiers.Readonly {
			return nonMut, true
		}

	case *dts_parser.MethodDecl:
		// Well-known symbol methods are non-mutating by convention.
		if isWellKnownMethod(m.Name) {
			return nonMut, true
		}
		// Explicit `readonly this` parameter.
		if hasReadonlyThisParam(m.Params) {
			return nonMut, true
		}
		// Class is a Readonly-prefixed collection variant.
		if isReadonlyCollectionClass(ctx.ClassName) {
			return nonMut, true
		}
	}

	return ClassifyResult{}, false
}

// wellKnownNonMutatingMethods lists method names that are non-mutating by
// convention regardless of the containing type.
var wellKnownNonMutatingMethods = set.FromSlice([]string{
	"toString",
	"toJSON",
	"toLocaleString",
	"valueOf",
})

// wellKnownSymbols lists Symbol.* property names whose methods are
// non-mutating by convention.
var wellKnownSymbols = set.FromSlice([]string{
	"iterator",
	"asyncIterator",
	"toPrimitive",
})

// isWellKnownMethod returns true when the method name is in the
// well-known non-mutating allow-list or is a well-known Symbol method.
func isWellKnownMethod(key dts_parser.PropertyKey) bool {
	switch k := key.(type) {
	case *dts_parser.Ident:
		return wellKnownNonMutatingMethods.Contains(k.Name)
	case *dts_parser.ComputedKey:
		member, ok := k.Expr.(*dts_parser.MemberExpr)
		if !ok {
			return false
		}
		obj, ok := member.Object.(*dts_parser.IdentExpr)
		if !ok || obj.Name != "Symbol" {
			return false
		}
		return wellKnownSymbols.Contains(member.Prop.Name)
	}
	return false
}

// hasReadonlyThisParam returns true when the parameter list starts with a
// `this` parameter whose type is Readonly<T>, ReadonlyArray<T>, etc.
func hasReadonlyThisParam(params []*dts_parser.Param) bool {
	if len(params) == 0 {
		return false
	}
	first := params[0]
	if first.Name == nil || first.Name.Name != "this" {
		return false
	}
	if first.Type == nil {
		return false
	}
	return isReadonlyWrapperType(first.Type)
}

// isReadonlyWrapperType returns true for Readonly<T>, ReadonlyArray<T>,
// ReadonlySet<T>, ReadonlyMap<K, V>, and readonly T[].
func isReadonlyWrapperType(t dts_parser.TypeAnn) bool {
	if arr, ok := t.(*dts_parser.ArrayType); ok {
		return arr.Readonly
	}
	typeRef, ok := t.(*dts_parser.TypeReference)
	if !ok {
		return false
	}
	ident, ok := typeRef.Name.(*dts_parser.Ident)
	if !ok {
		return false
	}
	switch ident.Name {
	case "Readonly", "ReadonlyArray", "ReadonlySet", "ReadonlyMap":
		return true
	}
	return false
}

// isReadonlyCollectionClass returns true when the class name is one of
// TypeScript's Readonly-prefixed collection variants.
func isReadonlyCollectionClass(name string) bool {
	switch name {
	case "ReadonlyArray", "ReadonlySet", "ReadonlyMap":
		return true
	}
	return false
}
