// Exhaustiveness checking for match expressions.
//
// This file determines whether a match expression's branches collectively
// cover every possible value of the target type, and flags branches that are
// unreachable (redundant). The algorithm uses a "coverage set" approach:
//
//  1. Normalize the target type — resolve type aliases, expand boolean into
//     the union {true, false}, and prune type variables.
//
//  2. Expand the coverage set — enumerate the distinct values (or value
//     classes) that must be covered. For unions this is the list of members;
//     for tuples it is the Cartesian product of each element position's
//     expanded types; for non-finite types (number, string, etc.) the set
//     is empty and a catch-all wildcard is required instead.
//
//  3. Compute per-pattern coverage — for each branch's pattern, determine
//     which members of the coverage set it matches. Literal patterns match
//     their literal type, extractor patterns match the variant identified by
//     their [Symbol.customMatcher] parameter type, object patterns use the
//     MatchedUnionMembers populated during type checking, and so on.
//
//  4. Track which members are covered — group branches by member, then for
//     each member check whether its branches' inner patterns (extractor
//     arguments, object property patterns, tuple element patterns)
//     collectively exhaust the member's inner type. This inner check
//     recurses through the same algorithm (steps 1-4). A member is fully
//     covered if at least one branch has all-wildcard inner patterns, or if
//     the recursive check reports exhaustiveness.
//
//  5. Detect redundancy (top-level only) — process branches in declaration
//     order, tracking a "contributed" set of members covered so far. A
//     branch is redundant if every member it covers was already contributed
//     by an earlier branch. Branches that participate in a multi-branch
//     partial coverage group (where different branches cover different inner
//     values of the same member) are exempt from this check.
//
// Key entry points:
//
//   - checkExhaustiveness      — top-level, called from the checker on each
//     match expression. Orchestrates coverage analysis and redundancy detection.
//   - analyzeCoverageExhaustiveness — shared core for steps 2-4, used by both
//     the top-level check and recursive inner checks.
//   - checkPositionalExhaustiveness — shared Cartesian-product tracking for
//     extractor arguments and tuple elements.
//   - detectRedundancy         — step 5, separated from coverage analysis.
//
// This is a direct coverage-set enumeration approach rather than the
// matrix-decomposition algorithm described in Maranget 2007 ("Warnings for
// pattern matching", Journal of Functional Programming 17(3), pp. 387-421,
// https://doi.org/10.1017/S0956796807006223). Coverage-set enumeration is a
// good fit for Escalier because:
//
//   - Escalier's pattern matching is primarily over finite discriminated
//     unions, booleans, and tuples of finite types. The type system already
//     tells us the exact set of members to check, so coverage-set is just
//     set arithmetic: enumerate members, check them off, report what's left.
//   - Object patterns with varying/optional properties map naturally to
//     per-member grouping and per-property recursive checking, whereas
//     Maranget's algorithm assumes fixed-arity positional constructors.
//   - Uncovered members are already typed values (e.g., Color.Blue), which
//     translate directly into actionable error messages and IDE quick-fixes.
//
// The main limitation is that coverage-set enumeration does not generalize
// well to recursive/algebraic data types with constructor patterns of
// arbitrary depth — it would need to enumerate all shapes up to some depth
// bound. Maranget's matrix decomposition avoids enumeration entirely by
// decomposing one constructor layer at a time. This is not a concern for
// Escalier's current type system.
//
// For a similar coverage-set strategy, see the Rust compiler's exhaustiveness
// checker (rustc_pattern_analysis) which also expands types into a finite
// set of "constructors" and tracks coverage per constructor.
package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// checkExhaustiveness checks whether a match expression's cases collectively
// cover all possible values of the target type. It also detects redundant
// branches that can never match.
func (c *Checker) checkExhaustiveness(
	expr *ast.MatchExpr,
	targetType type_system.Type,
) *ExhaustivenessResult {
	targetType = normalizeTargetType(targetType)

	// Compute coverage for each branch, inlining guard handling.
	coverages := make([]CaseCoverage[ast.Pat], len(expr.Cases))
	var nonGuardedCoverages []CaseCoverage[ast.Pat]
	for i, matchCase := range expr.Cases {
		if matchCase.Guard != nil {
			coverages[i] = CaseCoverage[ast.Pat]{Pattern: matchCase.Pattern, HasGuard: true}
		} else {
			cov := c.computePatternCoverage(matchCase.Pattern, targetType)
			coverages[i] = cov
			nonGuardedCoverages = append(nonGuardedCoverages, cov)
		}
	}

	// Core coverage analysis (shared with inner pattern checking).
	result := c.analyzeCoverageExhaustiveness(nonGuardedCoverages, targetType)

	// Add redundancy detection (top-level only).
	result.RedundantCases = c.detectRedundancy(coverages, expr, targetType)

	return result
}

// expandCoverageSet expands a target type into the list of types that must be
// covered for exhaustiveness. Returns the coverage set and whether the type is
// finite (i.e., can be fully enumerated without a catch-all).
func expandCoverageSet(targetType type_system.Type) ([]type_system.Type, bool) {
	targetType = type_system.Prune(targetType)

	if union, ok := targetType.(*type_system.UnionType); ok {
		// Each union member is a separate item in the coverage set.
		// Keep original types (including TypeRefTypes) for error messages.
		return union.Types, true
	}

	if tuple, ok := targetType.(*type_system.TupleType); ok {
		return expandTupleCoverageSet(tuple)
	}

	// A literal type (e.g., "circle") is finite with exactly one member.
	// This is needed for nested exhaustiveness checking where a property
	// type is a literal (e.g., the discriminant property in a union member).
	if _, ok := targetType.(*type_system.LitType); ok {
		return []type_system.Type{targetType}, true
	}

	// TODO(#436): Object types with all-finite properties (e.g.,
	// {kind: "flag", value: boolean}) are finite and could be expanded
	// into their Cartesian product, similar to expandTupleCoverageSet.

	// Non-finite types (number, string, etc.) cannot be fully
	// enumerated — they require a catch-all.
	return nil, false
}

// indexInCoverageSet finds the index of a type in the coverage set using
// the same matching logic as typesMatchForCoverage.
func indexInCoverageSet(t type_system.Type, coverageSet []type_system.Type) int {
	for i, member := range coverageSet {
		if typesMatchForCoverage(t, member) {
			return i
		}
	}
	return -1
}

// ExhaustivenessResult is the structured result returned by the exhaustiveness
// checker. It provides enough information for error reporting and future LSP
// integration (e.g., generating missing match arms).
type ExhaustivenessResult struct {
	IsExhaustive     bool
	UncoveredTypes   []type_system.Type // union members not covered by any branch
	RedundantCases   []RedundantCase    // branches that can never match
	IsNonFinite      bool               // true when the target type is non-finite
	PartialCoverages []PartialCoverage  // members partially covered (inner patterns not exhaustive)
}

// PartialCoverage records a union member that is covered by branches but whose
// inner patterns do not collectively exhaust the member's inner type.
type PartialCoverage struct {
	Member      type_system.Type
	InnerResult *ExhaustivenessResult
}

// RedundantCase identifies a match branch that is unreachable because all
// types it covers are already handled by earlier branches.
type RedundantCase struct {
	CaseIndex int      // index into MatchExpr.Cases
	Span      ast.Span // span of the redundant branch's pattern
}

// CaseCoverage holds per-branch intermediate data computed during the
// exhaustiveness analysis. The type parameter P communicates what kind of
// pattern the coverage carries — general code uses CaseCoverage[ast.Pat],
// while inner-exhaustiveness helpers use narrower types like
// CaseCoverage[*ast.ExtractorPat].
type CaseCoverage[P ast.Pat] struct {
	Pattern      P
	HasGuard     bool
	CoveredTypes []type_system.Type // which union members this branch covers
	IsCatchAll   bool               // true for unguarded wildcard/identifier
}

// narrowCoverages converts a slice of general CaseCoverage[ast.Pat] into a
// slice of CaseCoverage[P] by type-asserting each pattern. Entries whose
// pattern is not of type P are silently dropped.
func narrowCoverages[P ast.Pat](coverages []CaseCoverage[ast.Pat]) []CaseCoverage[P] {
	result := make([]CaseCoverage[P], 0, len(coverages))
	for _, cov := range coverages {
		if pat, ok := any(cov.Pattern).(P); ok {
			result = append(result, CaseCoverage[P]{
				Pattern:      pat,
				HasGuard:     cov.HasGuard,
				CoveredTypes: cov.CoveredTypes,
				IsCatchAll:   cov.IsCatchAll,
			})
		}
	}
	return result
}

// isCatchAllPat returns true if the pattern matches any value at its position
// (wildcard, identifier binding, or rest element).
func isCatchAllPat(pat ast.Pat) bool {
	switch pat.(type) {
	case *ast.WildcardPat, *ast.IdentPat, *ast.RestPat:
		return true
	default:
		return false
	}
}

// normalizeTargetType applies the standard target type normalization used by
// both top-level and inner exhaustiveness checking: Prune, resolve type aliases,
// and expand booleans into {true, false}.
func normalizeTargetType(t type_system.Type) type_system.Type {
	t = type_system.Prune(t)
	t = resolveTypeRef(t)
	if expanded, ok := expandBooleanType(t); ok {
		t = expanded
	}
	return t
}

// expandBooleanType expands a boolean primitive type into a synthetic union of
// LiteralType(true) and LiteralType(false). This allows the standard union
// coverage algorithm to handle boolean exhaustiveness (e.g., matching both
// true and false covers the boolean type).
//
// If the given type is not a boolean primitive, it is returned unchanged along
// with false to indicate no expansion occurred.
func expandBooleanType(t type_system.Type) (type_system.Type, bool) {
	prim, ok := t.(*type_system.PrimType)
	if !ok || prim.Prim != type_system.BoolPrim {
		return t, false
	}
	expanded := type_system.NewUnionType(
		nil,
		type_system.NewBoolLitType(nil, true),
		type_system.NewBoolLitType(nil, false),
	)
	return expanded, true
}

// computePatternCoverage determines which types from the target type a single
// pattern covers. This is the pattern-specific coverage logic shared by both
// top-level match case analysis and inner pattern checking.
func (c *Checker) computePatternCoverage(
	pat ast.Pat,
	targetType type_system.Type,
) CaseCoverage[ast.Pat] {
	coverage := CaseCoverage[ast.Pat]{Pattern: pat}

	switch pat := pat.(type) {
	case *ast.WildcardPat, *ast.IdentPat:
		coverage.IsCatchAll = true

	case *ast.LitPat:
		// The pattern's inferred type is a literal type (e.g., true, "foo", 42).
		// It covers that specific literal type within the target union.
		inferredType := type_system.Prune(pat.InferredType())
		coverage.CoveredTypes = findMatchingMembers(inferredType, targetType)

	case *ast.ObjectPat:
		// Read MatchedUnionMembers from the pattern's inferred ObjectType,
		// which was populated by unifyPatternWithUnion during type checking.
		inferredType := type_system.Prune(pat.InferredType())
		if objType, ok := inferredType.(*type_system.ObjectType); ok {
			if len(objType.MatchedUnionMembers) > 0 {
				coverage.CoveredTypes = objType.MatchedUnionMembers
			} else {
				// Non-union structural match: the pattern matched the target
				// type directly, e.g. matching `{x} => ...` against a
				// `Point` class where the target is a single nominal type,
				// not a union. In this case findMatchingMembers checks if
				// the pattern's inferred type matches the target by ID.
				coverage.CoveredTypes = findMatchingMembers(inferredType, targetType)
			}
		}

	case *ast.ExtractorPat:
		// The customMatcher method's param type identifies which variant
		// this extractor matches. Walk the extractor's resolved type to
		// find [Symbol.customMatcher] and read its param type.
		inferredType := type_system.Prune(pat.InferredType())
		if ext, ok := inferredType.(*type_system.ExtractorType); ok {
			paramType := c.getCustomMatcherParamType(ext)
			if paramType != nil {
				coverage.CoveredTypes = findMatchingMembers(paramType, targetType)
			}
		}

	case *ast.InstancePat:
		// The pattern's inferred type is a nominal ObjectType with an ID
		// matching a specific union member. Find which member shares that ID.
		inferredType := type_system.Prune(pat.InferredType())
		coverage.CoveredTypes = findMatchingMembers(inferredType, targetType)

	case *ast.TuplePat:
		// TuplePat coverage has three paths depending on the target type:
		//   (a) Union target — check which union members the pattern matches
		//   (b) Single tuple, all-wildcard — mark as catch-all
		//   (c) Single tuple, mixed — Cartesian product of per-position coverage
		// The union path must come first because it uses different coverage
		// semantics (IsCatchAll must NOT be set for unions, since non-tuple
		// members may exist). The all-wildcard check is deferred until we've
		// validated the target is a single tuple of matching arity.

		// (shared) Pre-compute whether all elements are catch-all patterns.
		allCatchAll := true
		for _, elem := range pat.Elems {
			if !isCatchAllPat(elem) {
				allCatchAll = false
				break
			}
		}

		// (a) Union target: check each member individually.
		if union, ok := targetType.(*type_system.UnionType); ok {
			// The coverage set is the union members themselves, so we check
			// which members the pattern matches element-wise.
			//
			// Example: type T = ["a", "a"] | ["b", "b"]
			//   pattern ["a", _] → covers ["a", "a"] but not ["b", "b"]
			//
			// We must NOT set IsCatchAll here because the union may contain
			// non-tuple members (e.g., number) that a TuplePat cannot match.
			for _, member := range union.Types {
				memberTuple, ok := type_system.Prune(member).(*type_system.TupleType)
				if !ok || len(memberTuple.Elems) != len(pat.Elems) {
					continue
				}
				matches := true
				for i, elemPat := range pat.Elems {
					elemType := type_system.Prune(memberTuple.Elems[i])
					switch ep := elemPat.(type) {
					case *ast.WildcardPat, *ast.IdentPat:
						// Matches anything at this position.
					case *ast.LitPat:
						inferredType := type_system.Prune(ep.InferredType())
						if !typesMatchForCoverage(inferredType, elemType) {
							// Check if the literal is a valid value of the
							// element type (e.g., true is a value of boolean).
							if !literalBelongsToType(inferredType, elemType) {
								matches = false
							}
						}
					default:
						matches = false
					}
					if !matches {
						break
					}
				}
				if matches {
					coverage.CoveredTypes = append(coverage.CoveredTypes, member)
				}
			}
			break
		}

		// Validate target is a single tuple with matching arity.
		tupleTarget, ok := targetType.(*type_system.TupleType)
		if !ok || len(pat.Elems) != len(tupleTarget.Elems) {
			break
		}

		// (b) Single tuple, all-wildcard: catch-all.
		if allCatchAll {
			coverage.IsCatchAll = true
			break
		}

		// (c) Single tuple, mixed: Cartesian product of per-position coverage.
		//
		// Example: target [boolean, boolean]
		//   coverage set = [true,true], [true,false], [false,true], [false,false]
		//   pattern [true, _] → per-position sets: {true} × {true, false}
		//                     → covers [true,true], [true,false]
		perPositionSets := make([][]type_system.Type, len(pat.Elems))
		for i, elemPat := range pat.Elems {
			matched, ok := c.computePositionCoverage(elemPat, tupleTarget.Elems[i])
			if !ok {
				return coverage
			}
			perPositionSets[i] = matched
		}

		coverage.CoveredTypes = cartesianProductTuples(perPositionSets)

	case *ast.RestPat:
		// Rest patterns in match expressions are unusual; treat as non-covering.
	}

	return coverage
}

// getCustomMatcherParamType extracts the param type from the [Symbol.customMatcher]
// method on an extractor's object type. This param type identifies which
// variant the extractor matches.
func (c *Checker) getCustomMatcherParamType(ext *type_system.ExtractorType) type_system.Type {
	methodElem, _ := c.findCustomMatcherMethod(ext)
	if methodElem != nil && len(methodElem.Fn.Params) == 1 {
		return type_system.Prune(methodElem.Fn.Params[0].Type)
	}
	return nil
}

// getCustomMatcherReturnType extracts the return type from the
// [Symbol.customMatcher] method on an extractor's object type. The return type
// is a tuple containing the types of the extractor's arguments, used for nested
// exhaustiveness checking (Phase 7).
func (c *Checker) getCustomMatcherReturnType(ext *type_system.ExtractorType) type_system.Type {
	methodElem, _ := c.findCustomMatcherMethod(ext)
	if methodElem != nil {
		return type_system.Prune(methodElem.Fn.Return)
	}
	return nil
}

// findMatchingMembers finds which members of the target type are matched by
// the given pattern type. If the target is a union, it checks each member.
// Otherwise, it checks if the pattern type matches the target directly.
func findMatchingMembers(patternType type_system.Type, targetType type_system.Type) []type_system.Type {
	targetType = type_system.Prune(targetType)

	if union, ok := targetType.(*type_system.UnionType); ok {
		var matched []type_system.Type
		for _, member := range union.Types {
			if typesMatchForCoverage(patternType, member) {
				matched = append(matched, member)
			}
		}
		return matched
	}

	// Non-union target: check if the pattern matches it directly.
	if typesMatchForCoverage(patternType, targetType) {
		return []type_system.Type{targetType}
	}
	return nil
}

// resolveTypeRef resolves a TypeRefType to its underlying type. If the type
// is not a TypeRefType or has no TypeAlias, it is returned unchanged. This
// is used so that type aliases for unions (e.g., `type Color = Color.RGB | Color.Hex`)
// are expanded before coverage set computation.
func resolveTypeRef(t type_system.Type) type_system.Type {
	if ref, ok := t.(*type_system.TypeRefType); ok && ref.TypeAlias != nil {
		return type_system.Prune(ref.TypeAlias.Type)
	}
	return t
}

// typesMatchForCoverage determines whether two types "match" for coverage
// purposes. This is used to determine which union members a pattern covers.
//
// Structural ObjectType comparison is handled via pointer identity (the first
// check below) rather than field-by-field comparison. This works because
// ObjectPat coverage comes from MatchedUnionMembers, which stores direct
// references to the original union member objects populated during
// unifyPatternWithUnion. Those pointers are the same objects as the ones in
// the coverage set (from UnionType.Types), so identity comparison suffices.
func typesMatchForCoverage(patternType, memberType type_system.Type) bool {
	patternType = type_system.Prune(patternType)
	memberType = type_system.Prune(memberType)

	// Pointer identity: if both types are the exact same object (e.g.,
	// MatchedUnionMembers stores direct references to union members),
	// they match.
	if patternType == memberType {
		return true
	}

	// TypeRefType: compare by TypeAlias pointer identity (same enum variant).
	if patRef, ok := patternType.(*type_system.TypeRefType); ok {
		if memRef, ok := memberType.(*type_system.TypeRefType); ok {
			if patRef.TypeAlias != nil && memRef.TypeAlias != nil {
				return patRef.TypeAlias == memRef.TypeAlias
			}
		}
		// A TypeRefType pattern may also match a resolved member; expand
		// the pattern's TypeAlias and compare structurally.
		if patRef.TypeAlias != nil {
			return typesMatchForCoverage(patRef.TypeAlias.Type, memberType)
		}
	}
	// Also check the reverse: member is a TypeRefType that resolves to
	// something matching the pattern.
	if memRef, ok := memberType.(*type_system.TypeRefType); ok {
		if memRef.TypeAlias != nil {
			return typesMatchForCoverage(patternType, memRef.TypeAlias.Type)
		}
	}

	// Nominal ObjectType: compare by ID.
	if patObj, ok := patternType.(*type_system.ObjectType); ok {
		if memObj, ok := memberType.(*type_system.ObjectType); ok {
			if patObj.Nominal && memObj.Nominal && patObj.ID != 0 && memObj.ID != 0 {
				return patObj.ID == memObj.ID
			}
		}
	}

	// LiteralType: compare by value equality. Pointer identity doesn't
	// work here because the pattern's LitType (from inferLit) and the
	// coverage set's LitType (from expandBooleanType) are separate
	// allocations.
	if patLit, ok := patternType.(*type_system.LitType); ok {
		if memLit, ok := memberType.(*type_system.LitType); ok {
			return patLit.Lit.Equal(memLit.Lit)
		}
	}

	// TupleType: compare element-wise. Pointer identity doesn't work
	// here because both sides are independently constructed by
	// cartesianProductTuples — the coverage set side via
	// expandTupleCoverageSet and the pattern side via computeCaseCoverage.
	if patTuple, ok := patternType.(*type_system.TupleType); ok {
		if memTuple, ok := memberType.(*type_system.TupleType); ok {
			if len(patTuple.Elems) != len(memTuple.Elems) {
				return false
			}
			for i := range patTuple.Elems {
				if !typesMatchForCoverage(patTuple.Elems[i], memTuple.Elems[i]) {
					return false
				}
			}
			return true
		}
	}

	return false
}

// expandTupleCoverageSet expands a TupleType into the Cartesian product of
// its element types' coverage sets. Each element position is independently
// expanded (booleans → {true, false}, unions → members). If any element is
// non-finite, the tuple is treated as non-finite. A complexity limit of 256
// combinations is enforced.
func expandTupleCoverageSet(tuple *type_system.TupleType) ([]type_system.Type, bool) {
	elementSets := make([][]type_system.Type, len(tuple.Elems))
	totalSize := 1
	for i, elem := range tuple.Elems {
		elem = normalizeTargetType(elem)
		members, finite := expandCoverageSet(elem)
		if !finite {
			return nil, false
		}
		totalSize *= len(members)
		if totalSize > 256 {
			return nil, false
		}
		elementSets[i] = members
	}
	return cartesianProductTuples(elementSets), true
}

// computePositionCoverage determines which types a single pattern covers at a
// given element position. It normalizes the element type (Prune, resolveTypeRef,
// expandBooleanType) and then dispatches on the pattern kind. Returns the
// matched types and true on success, or nil and false if the pattern cannot be
// analyzed (non-finite wildcard or unsupported pattern type).
func (c *Checker) computePositionCoverage(elemPat ast.Pat, elemType type_system.Type) ([]type_system.Type, bool) {
	elemType = normalizeTargetType(elemType)

	switch p := elemPat.(type) {
	case *ast.WildcardPat, *ast.IdentPat:
		members, finite := expandCoverageSet(elemType)
		if !finite {
			return nil, false
		}
		return members, true
	case *ast.LitPat:
		inferredType := type_system.Prune(p.InferredType())
		matched := findMatchingMembers(inferredType, elemType)
		if len(matched) == 0 {
			return nil, false
		}
		return matched, true
	case *ast.ExtractorPat:
		inferredType := type_system.Prune(p.InferredType())
		ext, ok := inferredType.(*type_system.ExtractorType)
		if !ok {
			return nil, false
		}
		paramType := c.getCustomMatcherParamType(ext)
		if paramType == nil {
			return nil, false
		}
		matched := findMatchingMembers(paramType, elemType)
		if len(matched) == 0 {
			return nil, false
		}
		return matched, true
	case *ast.InstancePat:
		inferredType := type_system.Prune(p.InferredType())
		matched := findMatchingMembers(inferredType, elemType)
		if len(matched) == 0 {
			return nil, false
		}
		return matched, true
	case *ast.ObjectPat:
		inferredType := type_system.Prune(p.InferredType())
		matched := findMatchingMembers(inferredType, elemType)
		if len(matched) == 0 {
			return nil, false
		}
		return matched, true
	default:
		return nil, false
	}
}

// cartesianProductTuples computes the Cartesian product of per-position type
// sets and returns each combination as a TupleType.
func cartesianProductTuples(elementSets [][]type_system.Type) []type_system.Type {
	if len(elementSets) == 0 {
		// The Cartesian product of zero sets is {()} — a single empty tuple.
		// This ensures zero-element tuple types are treated as inhabited.
		return []type_system.Type{type_system.NewTupleType(nil)}
	}

	// Start with partial combinations containing just the first position.
	type combo = []type_system.Type
	current := make([]combo, len(elementSets[0]))
	for i, t := range elementSets[0] {
		current[i] = combo{t}
	}

	// Extend each combination with subsequent positions.
	for _, set := range elementSets[1:] {
		var next []combo
		for _, existing := range current {
			for _, t := range set {
				extended := make(combo, len(existing)+1)
				copy(extended, existing)
				extended[len(existing)] = t
				next = append(next, extended)
			}
		}
		current = next
	}

	// Wrap each combination in a TupleType.
	result := make([]type_system.Type, len(current))
	for i, c := range current {
		result[i] = type_system.NewTupleType(nil, c...)
	}
	return result
}

// literalBelongsToType checks if a literal type is a valid value of the given
// target type. For example, LitType(true) belongs to PrimType(boolean), and
// LitType("a") belongs to UnionType(["a", "b"]). This is used for
// tuple-against-union matching where a LitPat element should match a member
// element of a broader type.
func literalBelongsToType(litType type_system.Type, targetType type_system.Type) bool {
	lit, ok := litType.(*type_system.LitType)
	if !ok {
		return false
	}

	targetType = type_system.Prune(targetType)
	targetType = resolveTypeRef(targetType)

	// Boolean expansion: true/false belong to boolean.
	if expanded, ok := expandBooleanType(targetType); ok {
		return len(findMatchingMembers(litType, expanded)) > 0
	}

	// Literal kind matches primitive kind.
	if prim, ok := targetType.(*type_system.PrimType); ok {
		switch lit.Lit.(type) {
		case *type_system.NumLit:
			return prim.Prim == type_system.NumPrim
		case *type_system.StrLit:
			return prim.Prim == type_system.StrPrim
		}
	}

	// Literal matches a member of a union.
	if union, ok := targetType.(*type_system.UnionType); ok {
		for _, member := range union.Types {
			if typesMatchForCoverage(litType, member) || literalBelongsToType(litType, member) {
				return true
			}
		}
	}

	return false
}

// innerPatternAreAllCatchAlls checks if a single branch's inner patterns are all
// catch-alls (wildcard/identifier), meaning it fully covers the union member
// without needing inner exhaustiveness checking.
func innerPatternAreAllCatchAlls(cov CaseCoverage[ast.Pat]) bool {
	switch pat := cov.Pattern.(type) {
	case *ast.ExtractorPat:
		for _, arg := range pat.Args {
			if !isCatchAllPat(arg) {
				return false
			}
		}
		return true
	case *ast.ObjectPat:
		for _, elem := range pat.Elems {
			switch e := elem.(type) {
			case *ast.ObjKeyValuePat:
				switch e.Value.(type) {
				case *ast.WildcardPat, *ast.IdentPat:
					continue
				default:
					return false
				}
			case *ast.ObjShorthandPat:
				continue // shorthand is a catch-all binding
			case *ast.ObjRestPat:
				continue
			}
		}
		return true
	case *ast.TuplePat:
		for _, elem := range pat.Elems {
			if !isCatchAllPat(elem) {
				return false
			}
		}
		return true
	case *ast.InstancePat:
		// Delegate to the inner ObjectPat.
		return innerPatternAreAllCatchAlls(CaseCoverage[ast.Pat]{Pattern: pat.Object})
	default:
		return true // other pattern types don't have inner patterns
	}
}

// patternsEqual returns true if two patterns are structurally identical
// (ignoring spans and inferred types). Used to detect duplicate branches
// within a nestedGroupBranches group.
func patternsEqual(a, b ast.Pat) bool {
	switch a := a.(type) {
	case *ast.LitPat:
		b, ok := b.(*ast.LitPat)
		return ok && a.Lit.Equal(b.Lit)
	case *ast.WildcardPat:
		_, ok := b.(*ast.WildcardPat)
		return ok
	case *ast.IdentPat:
		// All ident patterns are catch-alls; treat them as equal.
		_, ok := b.(*ast.IdentPat)
		return ok
	case *ast.ExtractorPat:
		b, ok := b.(*ast.ExtractorPat)
		if !ok || ast.QualIdentToString(a.Name) != ast.QualIdentToString(b.Name) || len(a.Args) != len(b.Args) {
			return false
		}
		for i := range a.Args {
			if !patternsEqual(a.Args[i], b.Args[i]) {
				return false
			}
		}
		return true
	case *ast.TuplePat:
		b, ok := b.(*ast.TuplePat)
		if !ok || len(a.Elems) != len(b.Elems) {
			return false
		}
		for i := range a.Elems {
			if !patternsEqual(a.Elems[i], b.Elems[i]) {
				return false
			}
		}
		return true
	case *ast.ObjectPat:
		b, ok := b.(*ast.ObjectPat)
		return ok && objPatternsEqual(a, b)
	case *ast.InstancePat:
		b, ok := b.(*ast.InstancePat)
		if !ok || ast.QualIdentToString(a.ClassName) != ast.QualIdentToString(b.ClassName) {
			return false
		}
		return objPatternsEqual(a.Object, b.Object)
	case *ast.RestPat:
		b, ok := b.(*ast.RestPat)
		return ok && patternsEqual(a.Pattern, b.Pattern)
	default:
		return false
	}
}

// objPatternsEqual returns true if two ObjectPat values have the same
// structure: same number of elements, same keys, and equal value patterns.
func objPatternsEqual(a, b *ast.ObjectPat) bool {
	if len(a.Elems) != len(b.Elems) {
		return false
	}
	for i, elemA := range a.Elems {
		elemB := b.Elems[i]
		switch ea := elemA.(type) {
		case *ast.ObjKeyValuePat:
			eb, ok := elemB.(*ast.ObjKeyValuePat)
			if !ok || ea.Key.Name != eb.Key.Name || !patternsEqual(ea.Value, eb.Value) {
				return false
			}
		case *ast.ObjShorthandPat:
			eb, ok := elemB.(*ast.ObjShorthandPat)
			if !ok || ea.Key.Name != eb.Key.Name {
				return false
			}
		case *ast.ObjRestPat:
			eb, ok := elemB.(*ast.ObjRestPat)
			if !ok || !patternsEqual(ea.Pattern, eb.Pattern) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// branchPartiallyCovers checks if a branch has inner patterns that only
// partially cover the given member. This is like !branchFullyCoversMember but
// also considers the member's type: for TuplePat, if every LitPat element
// exactly matches the corresponding LitType element in the member, the branch
// fully covers that specific member (not partial). This distinction prevents
// clearing genuine redundancy warnings for duplicate tuple branches.
func branchPartiallyCovers(cov CaseCoverage[ast.Pat], member type_system.Type) bool {
	if innerPatternAreAllCatchAlls(cov) {
		return false // all catch-alls → not partial
	}

	// For TuplePat matching a literal-element tuple member: check if every
	// LitPat element exactly matches the member's corresponding LitType.
	if tuplePat, ok := cov.Pattern.(*ast.TuplePat); ok {
		memberTuple, ok := type_system.Prune(resolveTypeRef(member)).(*type_system.TupleType)
		if ok && len(tuplePat.Elems) == len(memberTuple.Elems) {
			allExactMatch := true
			for i, elemPat := range tuplePat.Elems {
				elemType := type_system.Prune(memberTuple.Elems[i])
				switch p := elemPat.(type) {
				case *ast.WildcardPat, *ast.IdentPat:
					// catch-all — always matches
				case *ast.LitPat:
					inferredType := type_system.Prune(p.InferredType())
					if !typesMatchForCoverage(inferredType, elemType) {
						allExactMatch = false
					}
				default:
					allExactMatch = false
				}
				if !allExactMatch {
					break
				}
			}
			if allExactMatch {
				return false // LitPats exactly match member's literal types
			}
		}
	}

	return true // has patterns that only partially cover the member
}

// checkNestedExhaustiveness checks whether the inner patterns of branches
// covering the same union member collectively exhaust the member's inner type.
// Returns nil if no inner checking is applicable.
func (c *Checker) checkNestedExhaustiveness(
	branchCoverages []CaseCoverage[ast.Pat],
	member type_system.Type,
) *ExhaustivenessResult {
	if len(branchCoverages) == 0 {
		return nil
	}

	// Quick check: if any branch fully covers the member
	// (all inner patterns are catch-alls), it's exhaustive.
	for _, cov := range branchCoverages {
		if innerPatternAreAllCatchAlls(cov) {
			return &ExhaustivenessResult{IsExhaustive: true}
		}
	}

	// Dispatch based on pattern type, narrowing to the concrete pattern type.
	switch branchCoverages[0].Pattern.(type) {
	case *ast.ExtractorPat:
		return c.checkExtractorInnerExhaustiveness(narrowCoverages[*ast.ExtractorPat](branchCoverages))
	case *ast.ObjectPat:
		return c.checkObjectInnerExhaustiveness(narrowCoverages[*ast.ObjectPat](branchCoverages), member)
	case *ast.TuplePat:
		return c.checkTupleInnerExhaustiveness(narrowCoverages[*ast.TuplePat](branchCoverages), member)
	case *ast.InstancePat:
		return c.checkInstanceInnerExhaustiveness(narrowCoverages[*ast.InstancePat](branchCoverages), member)
	default:
		return nil
	}
}

// checkExtractorInnerExhaustiveness checks whether extractor pattern arguments
// collectively exhaust the extractor's return type.
func (c *Checker) checkExtractorInnerExhaustiveness(
	branchCoverages []CaseCoverage[*ast.ExtractorPat],
) *ExhaustivenessResult {
	firstPat := branchCoverages[0].Pattern

	inferredType := type_system.Prune(firstPat.InferredType())
	ext, ok := inferredType.(*type_system.ExtractorType)
	if !ok {
		return nil
	}

	returnType := c.getCustomMatcherReturnType(ext)
	if returnType == nil {
		return nil
	}

	returnTuple, ok := type_system.Prune(returnType).(*type_system.TupleType)
	if !ok {
		return nil
	}

	if len(returnTuple.Elems) == 1 {
		// Single-argument extractor: collect the first arg from each branch.
		var innerPatterns []ast.Pat
		for _, cov := range branchCoverages {
			if len(cov.Pattern.Args) == 0 {
				continue
			}
			innerPatterns = append(innerPatterns, cov.Pattern.Args[0])
		}
		return c.checkInnerPatternsExhaustive(innerPatterns, returnTuple.Elems[0])
	}

	// Multi-argument extractor: extract per-branch element patterns and
	// delegate to the shared positional exhaustiveness checker.
	branchElemPats := make([][]ast.Pat, 0, len(branchCoverages))
	for _, cov := range branchCoverages {
		if len(cov.Pattern.Args) != len(returnTuple.Elems) {
			continue
		}
		branchElemPats = append(branchElemPats, cov.Pattern.Args)
	}

	return c.checkPositionalExhaustiveness(branchElemPats, returnTuple.Elems)
}

// checkObjectInnerExhaustiveness checks whether object patterns covering the
// same union member collectively exhaust the member's property types.
//
// It extracts the string-keyed properties from the member's object type and
// builds a per-branch tuple of property patterns (using a synthetic wildcard
// when a property is omitted or bound via shorthand). It then delegates to
// checkPositionalExhaustiveness which handles both the finite (Cartesian
// product) and non-finite (per-position) cases — so correlated patterns
// across properties are checked jointly when the types are finite.
func (c *Checker) checkObjectInnerExhaustiveness(
	branchCoverages []CaseCoverage[*ast.ObjectPat],
	member type_system.Type,
) *ExhaustivenessResult {
	memberObj, ok := type_system.Prune(resolveTypeRef(member)).(*type_system.ObjectType)
	if !ok {
		return nil
	}

	// Collect the string-keyed properties and their types.
	type propInfo struct {
		name     string
		propType type_system.Type
	}
	var props []propInfo
	for _, elem := range memberObj.Elems {
		prop, ok := elem.(*type_system.PropertyElem)
		if !ok {
			continue
		}
		if prop.Name.Kind != type_system.StrObjTypeKeyKind {
			continue
		}
		props = append(props, propInfo{
			name:     prop.Name.Str,
			propType: type_system.Prune(prop.Value),
		})
	}

	if len(props) == 0 {
		return &ExhaustivenessResult{IsExhaustive: true}
	}

	// Build a per-branch tuple of property patterns.
	wildcard := ast.NewWildcardPat(ast.Span{})
	branchElemPats := make([][]ast.Pat, 0, len(branchCoverages))
	for _, cov := range branchCoverages {
		row := make([]ast.Pat, len(props))
		for i, p := range props {
			if found := c.findPropertyPattern(cov.Pattern, p.name); found != nil {
				row[i] = found
			} else {
				row[i] = wildcard // property omitted → implicit wildcard
			}
		}
		branchElemPats = append(branchElemPats, row)
	}

	// Build the element types slice and delegate to positional checking.
	elemTypes := make([]type_system.Type, len(props))
	for i, p := range props {
		elemTypes[i] = p.propType
	}

	result := c.checkPositionalExhaustiveness(branchElemPats, elemTypes)

	// Simplify uncovered tuple types for clearer error messages:
	// strip positions where the property has only one possible value
	// (e.g., the discriminant "todo"), then unwrap single-element tuples.
	var singularPositions []bool
	for _, et := range elemTypes {
		members, finite := expandCoverageSet(normalizeTargetType(et))
		singularPositions = append(singularPositions, finite && len(members) == 1)
	}
	for i, t := range result.UncoveredTypes {
		tuple, ok := t.(*type_system.TupleType)
		if !ok {
			continue
		}
		var filtered []type_system.Type
		for j, elem := range tuple.Elems {
			if j < len(singularPositions) && singularPositions[j] {
				continue
			}
			filtered = append(filtered, elem)
		}
		if len(filtered) == 1 {
			result.UncoveredTypes[i] = filtered[0]
		} else if len(filtered) < len(tuple.Elems) {
			result.UncoveredTypes[i] = type_system.NewTupleType(nil, filtered...)
		}
	}

	return result
}

// findPropertyPattern extracts the pattern for a named property from an
// ObjectPat. Returns the value pattern for key-value bindings, a synthetic
// wildcard for shorthand bindings (which are catch-alls), or nil if the
// property is not mentioned.
func (c *Checker) findPropertyPattern(objPat *ast.ObjectPat, propName string) ast.Pat {
	for _, objElem := range objPat.Elems {
		switch e := objElem.(type) {
		case *ast.ObjKeyValuePat:
			if e.Key.Name == propName {
				return e.Value
			}
		case *ast.ObjShorthandPat:
			if e.Key.Name == propName {
				return ast.NewWildcardPat(ast.Span{}) // shorthand = catch-all
			}
		}
	}
	return nil
}

// checkInstanceInnerExhaustiveness checks whether instance patterns covering
// the same union member collectively exhaust the member's property types.
// It unwraps each InstancePat's inner ObjectPat and delegates to
// checkObjectInnerExhaustiveness.
func (c *Checker) checkInstanceInnerExhaustiveness(
	branchCoverages []CaseCoverage[*ast.InstancePat],
	member type_system.Type,
) *ExhaustivenessResult {
	objCoverages := make([]CaseCoverage[*ast.ObjectPat], 0, len(branchCoverages))
	for _, cov := range branchCoverages {
		if cov.Pattern.Object == nil {
			continue
		}
		objCoverages = append(objCoverages, CaseCoverage[*ast.ObjectPat]{
			Pattern:    cov.Pattern.Object,
			IsCatchAll: cov.IsCatchAll,
		})
	}
	if len(objCoverages) == 0 {
		return nil
	}
	return c.checkObjectInnerExhaustiveness(objCoverages, member)
}

// checkTupleInnerExhaustiveness checks whether tuple patterns covering the
// same union member collectively exhaust the member's element types. For finite
// element types, it uses Cartesian product tracking; for non-finite elements,
// it falls back to per-position checking.
func (c *Checker) checkTupleInnerExhaustiveness(
	branchCoverages []CaseCoverage[*ast.TuplePat],
	member type_system.Type,
) *ExhaustivenessResult {
	memberTuple, ok := type_system.Prune(resolveTypeRef(member)).(*type_system.TupleType)
	if !ok {
		return nil
	}

	// Extract element patterns from each branch.
	branchElemPats := make([][]ast.Pat, 0, len(branchCoverages))
	for _, cov := range branchCoverages {
		if len(cov.Pattern.Elems) != len(memberTuple.Elems) {
			continue
		}
		branchElemPats = append(branchElemPats, cov.Pattern.Elems)
	}

	return c.checkPositionalExhaustiveness(branchElemPats, memberTuple.Elems)
}

// checkPositionalExhaustiveness checks whether a set of per-branch element
// patterns collectively exhaust the given element types. For finite element
// types, it uses Cartesian product tracking; for non-finite elements, it falls
// back to per-position checking. This is the shared core of both extractor and
// tuple inner exhaustiveness.
func (c *Checker) checkPositionalExhaustiveness(
	branchElemPats [][]ast.Pat,
	elemTypes []type_system.Type,
) *ExhaustivenessResult {
	tuple := type_system.NewTupleType(nil, elemTypes...)
	innerCoverageSet, innerFinite := expandTupleCoverageSet(tuple)
	if innerFinite {
		innerCoveredSet := set.NewSet[int]()

		for _, elemPats := range branchElemPats {
			// Check if all elements are catch-alls → covers everything.
			allCatchAll := true
			for _, ep := range elemPats {
				if !isCatchAllPat(ep) {
					allCatchAll = false
					break
				}
			}
			if allCatchAll {
				return &ExhaustivenessResult{IsExhaustive: true}
			}

			perPositionSets := make([][]type_system.Type, len(elemPats))
			valid := true
			for i, elemPat := range elemPats {
				matched, ok := c.computePositionCoverage(elemPat, elemTypes[i])
				if !ok {
					valid = false
					break
				}
				perPositionSets[i] = matched
			}
			if !valid {
				continue
			}

			coveredCombos := cartesianProductTuples(perPositionSets)
			for _, combo := range coveredCombos {
				if idx := indexInCoverageSet(combo, innerCoverageSet); idx != -1 {
					innerCoveredSet.Add(idx)
				}
			}
		}

		var uncoveredTypes []type_system.Type
		for i, member := range innerCoverageSet {
			if !innerCoveredSet.Contains(i) {
				uncoveredTypes = append(uncoveredTypes, member)
			}
		}

		return &ExhaustivenessResult{
			IsExhaustive:   len(uncoveredTypes) == 0,
			UncoveredTypes: uncoveredTypes,
		}
	}

	// Non-finite element types: check each position independently.
	for i, elemType := range elemTypes {
		var posPatterns []ast.Pat
		hasCatchAll := false
		for _, elemPats := range branchElemPats {
			if i >= len(elemPats) {
				continue
			}
			if isCatchAllPat(elemPats[i]) {
				hasCatchAll = true
			} else {
				posPatterns = append(posPatterns, elemPats[i])
			}
		}
		if hasCatchAll {
			continue
		}
		result := c.checkInnerPatternsExhaustive(posPatterns, elemType)
		if !result.IsExhaustive {
			return result
		}
	}

	return &ExhaustivenessResult{IsExhaustive: true}
}

// checkInnerPatternsExhaustive checks if a list of inner patterns collectively
// exhaust a target type. It normalizes the target type, computes per-pattern
// coverage, and delegates to analyzeCoverageExhaustiveness.
func (c *Checker) checkInnerPatternsExhaustive(
	patterns []ast.Pat,
	targetType type_system.Type,
) *ExhaustivenessResult {
	targetType = normalizeTargetType(targetType)
	coverages := make([]CaseCoverage[ast.Pat], len(patterns))
	for i, pat := range patterns {
		coverages[i] = c.computePatternCoverage(pat, targetType)
	}
	return c.analyzeCoverageExhaustiveness(coverages, targetType)
}

// analyzeCoverageExhaustiveness is the shared core of exhaustiveness checking.
// Given pre-computed coverages and an already-normalized target type, it
// determines which members of the coverage set are covered (including nested
// exhaustiveness) and returns the result. This is used by both the top-level
// checkExhaustiveness (which adds redundancy detection) and the recursive
// checkInnerPatternsExhaustive.
func (c *Checker) analyzeCoverageExhaustiveness(
	coverages []CaseCoverage[ast.Pat],
	targetType type_system.Type,
) *ExhaustivenessResult {
	coverageSet, isFinite := expandCoverageSet(targetType)

	// Group non-catch-all coverages by which coverage set member they cover.
	type memberGroup struct {
		coverages []CaseCoverage[ast.Pat]
	}
	groups := make(map[int]*memberGroup)
	hasCatchAll := false

	for _, cov := range coverages {
		if cov.IsCatchAll {
			hasCatchAll = true
			continue
		}
		for _, covType := range cov.CoveredTypes {
			idx := indexInCoverageSet(covType, coverageSet)
			if idx != -1 {
				if groups[idx] == nil {
					groups[idx] = &memberGroup{}
				}
				groups[idx].coverages = append(groups[idx].coverages, cov)
			}
		}
	}

	if hasCatchAll {
		return &ExhaustivenessResult{IsExhaustive: true}
	}

	// Determine coverage for each member, including inner exhaustiveness.
	coveredSet := set.NewSet[int]()
	var partialCoverages []PartialCoverage
	partialMembers := set.NewSet[int]()

	for idx, member := range coverageSet {
		group := groups[idx]
		if group == nil {
			continue
		}

		innerResult := c.checkNestedExhaustiveness(group.coverages, member)
		if innerResult == nil || innerResult.IsExhaustive {
			coveredSet.Add(idx)
		} else {
			partialMembers.Add(idx)
			partialCoverages = append(partialCoverages, PartialCoverage{
				Member:      member,
				InnerResult: innerResult,
			})
		}
	}

	// Compute uncovered types. Partially covered members are excluded here
	// because they are reported separately via PartialCoverages with a more
	// detailed InnerResult (e.g., "Some is missing inner cases for false"
	// rather than just "missing cases for Some").
	var uncoveredTypes []type_system.Type
	if isFinite {
		for i, member := range coverageSet {
			if !coveredSet.Contains(i) && !partialMembers.Contains(i) {
				uncoveredTypes = append(uncoveredTypes, member)
			}
		}
	} else {
		uncoveredTypes = []type_system.Type{targetType}
	}

	return &ExhaustivenessResult{
		IsExhaustive:     len(uncoveredTypes) == 0 && len(partialCoverages) == 0,
		UncoveredTypes:   uncoveredTypes,
		IsNonFinite:      !isFinite,
		PartialCoverages: partialCoverages,
	}
}

// detectRedundancy identifies match branches that can never match because all
// types they cover are already handled by earlier branches. This is only used
// at the top level because redundancy is a property of user-written branches —
// inner/nested exhaustiveness checks patterns across different branches to
// determine collective coverage, where ordering and duplication don't apply.
func (c *Checker) detectRedundancy(
	coverages []CaseCoverage[ast.Pat],
	expr *ast.MatchExpr,
	targetType type_system.Type,
) []RedundantCase {
	// The algorithm walks branches in declaration order, maintaining a
	// contributed set that tracks which coverage set members have already been
	// "claimed" by earlier branches. A branch is redundant if it can't
	// contribute anything new.
	//
	// --- State initialization ---
	// - contributed — set of coverage member indices already covered by earlier
	//   branches
	// - seenCatchAll — whether a previous catch-all was encountered
	//
	// --- Skip guarded branches ---
	// Guarded branches have runtime conditions, so they might not match even if
	// their pattern matches. They're ignored for redundancy purposes — they
	// neither contribute coverage nor can be flagged as redundant.
	//
	// --- Catch-all branches ---
	// A catch-all (_ or bare identifier) is redundant if:
	// - seenCatchAll — a previous catch-all already exists, or
	// - isFinite && contributed.Len() == len(coverageSet) — every member of a
	//   finite type was already covered by earlier specific branches.
	// Either way, after processing a catch-all, it marks all members as
	// contributed and sets seenCatchAll = true.
	//
	// --- Specific branches---
	// For branches that cover specific types (e.g., Color.RGB, true):
	// 1. Check for redundancy: If the branch is NOT in nestedGroupBranches
	//    (explained below), check whether every member it covers is already in
	//    contributed. If so, the branch adds nothing new → redundant.
	// 2. Record contribution: Regardless of whether the branch is redundant,
	//    add its covered members to contributed.
	//
	// **The nestedGroupBranches exemption** When multiple branches cover the
	// same union member with different inner patterns (e.g., Some(true) and
	// Some(false)), the first unique occurrence of each pattern is added to
	// nestedGroupBranches. Without this exemption, the second branch would
	// appear redundant because Some was already contributed by the first. But
	// they're actually complementary — each covers different inner values.
	// Genuine duplicates (same member, same inner pattern) are NOT protected,
	// so e.g. a second Some(true) after an earlier Some(true) is correctly
	// flagged as redundant.

	coverageSet, isFinite := expandCoverageSet(targetType)

	// Group non-guarded, non-catch-all branches by which coverage set member
	// they cover. Track case indices for nestedGroupBranches.
	type memberGroup struct {
		coverages   []CaseCoverage[ast.Pat]
		caseIndices []int
	}
	groups := make(map[int]*memberGroup)
	for i, cov := range coverages {
		if cov.HasGuard || cov.IsCatchAll {
			continue
		}
		for _, covType := range cov.CoveredTypes {
			idx := indexInCoverageSet(covType, coverageSet)
			if idx != -1 {
				if groups[idx] == nil {
					groups[idx] = &memberGroup{}
				}
				groups[idx].coverages = append(groups[idx].coverages, cov)
				groups[idx].caseIndices = append(groups[idx].caseIndices, i)
			}
		}
	}

	// When multiple branches cover the same member and at least one has
	// partial inner patterns, protect them from redundancy detection —
	// but only if they have distinct patterns. Genuine duplicates (same
	// member, same inner pattern) are NOT protected.
	nestedGroupBranches := set.NewSet[int]()
	for idx, member := range coverageSet {
		group := groups[idx]
		if group == nil || len(group.coverages) <= 1 {
			continue
		}
		hasPartial := false
		for _, bc := range group.coverages {
			if branchPartiallyCovers(bc, member) {
				hasPartial = true
				break
			}
		}
		if !hasPartial {
			continue
		}
		// Protect only the first occurrence of each unique pattern.
		for i, ci := range group.caseIndices {
			isDuplicate := false
			for j := range i {
				if patternsEqual(group.coverages[i].Pattern, group.coverages[j].Pattern) {
					isDuplicate = true
					break
				}
			}
			if !isDuplicate {
				nestedGroupBranches.Add(ci)
			}
		}
	}

	// --- State initialization ---
	var redundantCases []RedundantCase
	contributed := set.NewSet[int]()
	seenCatchAll := false

	for i, cov := range coverages {
		// --- Skip guarded branches ---
		if cov.HasGuard {
			continue
		}

		// --- Catch-all branches ---
		if cov.IsCatchAll {
			isRedundant := seenCatchAll || (isFinite && contributed.Len() == len(coverageSet))
			if isRedundant {
				redundantCases = append(redundantCases, RedundantCase{
					CaseIndex: i,
					Span:      expr.Cases[i].Pattern.Span(),
				})
			}
			seenCatchAll = true
			for j := range coverageSet {
				contributed.Add(j)
			}
			continue
		}

		// --- Specific branches ---
		if len(cov.CoveredTypes) > 0 {
			// 1. Check for redundancy (the nestedGroupBranches exemption)
			if !nestedGroupBranches.Contains(i) {
				allContributed := true
				for _, covType := range cov.CoveredTypes {
					idx := indexInCoverageSet(covType, coverageSet)
					if idx == -1 || !contributed.Contains(idx) {
						allContributed = false
						break
					}
				}
				if allContributed {
					redundantCases = append(redundantCases, RedundantCase{
						CaseIndex: i,
						Span:      expr.Cases[i].Pattern.Span(),
					})
				}
			}

			// 2. Record contribution
			for _, covType := range cov.CoveredTypes {
				idx := indexInCoverageSet(covType, coverageSet)
				if idx != -1 {
					contributed.Add(idx)
				}
			}
		}
	}

	return redundantCases
}
