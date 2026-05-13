package interop

import (
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/set"
)

// ResolutionTier identifies which tier in the seven-tier resolution order
// produced a mutability classification for a class member's receiver.
//
// The tiers match the requirements document:
//  0. User-authored .esc source (sentinel; not produced by Classify — see §11.2)
//  1. User override files
//  2. @esctype tag (round-trip from Escalier source)
//  3. Explicit author signals (this: Readonly<T>, getters/setters, Readonly<T>, readonly props)
//  4. Shipped overrides (stdlib, FP libraries)
//  5. get* prefix rule (with documented exceptions)
//  6. Name-based heuristics
//  7. Default: mutating
type ResolutionTier int

const (
	TierUserSource      ResolutionTier = iota // 0: user-authored .esc source (sentinel)
	TierUserOverride                          // 1
	TierEsctype                               // 2
	TierExplicitSignal                        // 3
	TierShippedOverride                       // 4
	TierGetPrefix                             // 5
	TierNameHeuristic                         // 6
	TierDefault                               // 7
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
// seven-tier resolution order defined in
// planning/interop_mutability/requirements.md.
func Classify(ctx ClassifyContext) ClassifyResult {
	// Tier 1: user override files — §5.

	// Tier 2: @esctype tag — §9.

	// Tier 3: explicit author signals.
	if result, ok := classifyExplicitSignal(ctx); ok {
		return result
	}

	// Tier 4: shipped overrides (stdlib, FP libraries) — §6.

	// Tier 5: get* prefix rule — §7.1.

	// Tier 6: name-based heuristics — §7.2.

	// Tier 7: default to mutating.
	return ClassifyResult{Mut: true, Source: TierDefault}
}

// classifyExplicitSignal applies explicit author signals (tier 3):
//   - Getters never mutate the receiver.
//   - Setters always mutate the receiver.
//   - Methods with a `this: Readonly<T>` (or `this: readonly T[]`) parameter are non-mutating.
//   - Methods on Readonly-prefixed collection classes (ReadonlyArray, etc.) are non-mutating.
//   - Well-known symbol methods (toString, toJSON, etc.) are non-mutating.
//   - readonly properties are non-mutating (principle #6).
func classifyExplicitSignal(ctx ClassifyContext) (ClassifyResult, bool) {
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
		// Explicit `this: Readonly<T>` (or `this: readonly T[]`) parameter.
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
