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
	targetType = type_system.Prune(targetType)

	// Step 1: Expand the target type into a coverage set.
	// Resolve TypeRefType to its underlying type so that type aliases
	// for unions (e.g., `type Color = Color.RGB | Color.Hex`) are handled.
	targetType = resolveTypeRef(targetType)

	// Boolean primitives are expanded to {true, false}.
	if expanded, ok := expandBooleanType(targetType); ok {
		targetType = expanded
	}

	// Determine whether the target type is finite (can be fully enumerated)
	// or non-finite (requires a catch-all).
	coverageSet, isFinite := expandCoverageSet(targetType)

	// Step 2: Compute coverage for each branch.
	coverages := make([]CaseCoverage, len(expr.Cases))
	for i, matchCase := range expr.Cases {
		coverages[i] = c.computeCaseCoverage(matchCase, targetType)
	}

	// Step 5: Track covered set and detect redundancy.
	coveredSet := set.NewSet[int]() // indices into coverageSet that are covered
	var redundantCases []RedundantCase
	hasCatchAll := false

	for i, cov := range coverages {
		// Skip guarded branches for redundancy checking — they cover
		// nothing but are not redundant (they're runtime filters).
		if cov.HasGuard {
			continue
		}

		if cov.IsCatchAll {
			// Check redundancy: a catch-all is redundant if all types are
			// already covered (finite case) or a previous catch-all was
			// already seen (non-finite case).
			if hasCatchAll || (isFinite && coveredSet.Len() == len(coverageSet)) {
				redundantCases = append(redundantCases, RedundantCase{
					CaseIndex: i,
					Span:      expr.Cases[i].Pattern.Span(),
				})
			}
			// Mark everything as covered.
			hasCatchAll = true
			for j := range coverageSet {
				coveredSet.Add(j)
			}
			continue
		}

		if len(cov.CoveredTypes) > 0 {
			// Check redundancy: if every type this branch covers is
			// already in the covered set, the branch is redundant.
			allAlreadyCovered := true
			for _, covType := range cov.CoveredTypes {
				idx := indexInCoverageSet(covType, coverageSet)
				if idx == -1 || !coveredSet.Contains(idx) {
					allAlreadyCovered = false
					break
				}
			}
			if allAlreadyCovered {
				redundantCases = append(redundantCases, RedundantCase{
					CaseIndex: i,
					Span:      expr.Cases[i].Pattern.Span(),
				})
			}

			// Add this branch's covered types to the covered set.
			for _, covType := range cov.CoveredTypes {
				idx := indexInCoverageSet(covType, coverageSet)
				if idx != -1 {
					coveredSet.Add(idx)
				}
			}
		}
	}

	// Step 6: Compute uncovered types.
	var uncoveredTypes []type_system.Type
	if isFinite {
		// For finite types, report each uncovered member in declaration order.
		for i, member := range coverageSet {
			if !coveredSet.Contains(i) {
				uncoveredTypes = append(uncoveredTypes, member)
			}
		}
	} else if !hasCatchAll {
		// Non-finite types require a catch-all. If none was found, report
		// the target type itself as uncovered.
		uncoveredTypes = []type_system.Type{targetType}
	}

	return &ExhaustivenessResult{
		IsExhaustive:   len(uncoveredTypes) == 0,
		UncoveredTypes: uncoveredTypes,
		RedundantCases: redundantCases,
		IsNonFinite:    !isFinite,
	}
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

	// Non-finite types (number, string, object types, etc.) cannot be
	// fully enumerated — they require a catch-all.
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
	IsExhaustive   bool
	UncoveredTypes []type_system.Type // union members not covered by any branch
	RedundantCases []RedundantCase    // branches that can never match
	IsNonFinite    bool               // true when the target type is non-finite
}

// RedundantCase identifies a match branch that is unreachable because all
// types it covers are already handled by earlier branches.
type RedundantCase struct {
	CaseIndex int      // index into MatchExpr.Cases
	Span      ast.Span // span of the redundant branch's pattern
}

// CaseCoverage holds per-branch intermediate data computed during the
// exhaustiveness analysis.
type CaseCoverage struct {
	Pattern       ast.Pat
	HasGuard      bool
	CoveredTypes  []type_system.Type // which union members this branch covers
	IsCatchAll    bool               // true for unguarded wildcard/identifier
	InnerPatterns []ast.Pat          // nested patterns (e.g., args of ExtractorPat)
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

// computeCaseCoverage examines a single MatchCase and determines which types
// from the target type it covers. The targetType should already be expanded
// (e.g., boolean -> true | false) before calling this function.
func (c *Checker) computeCaseCoverage(
	matchCase *ast.MatchCase,
	targetType type_system.Type,
) CaseCoverage {
	coverage := CaseCoverage{
		Pattern:  matchCase.Pattern,
		HasGuard: matchCase.Guard != nil,
	}

	// Guarded branches cover nothing for exhaustiveness purposes (R6).
	// They are runtime filters and should not be treated as covering any type.
	if coverage.HasGuard {
		return coverage
	}

	switch pat := matchCase.Pattern.(type) {
	case *ast.WildcardPat:
		coverage.IsCatchAll = true

	case *ast.IdentPat:
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
			// Populate InnerPatterns for nested exhaustiveness (Phase 7).
			if len(pat.Args) > 0 {
				coverage.InnerPatterns = pat.Args
			}
		}

	case *ast.InstancePat:
		// The pattern's inferred type is a nominal ObjectType with an ID
		// matching a specific union member. Find which member shares that ID.
		inferredType := type_system.Prune(pat.InferredType())
		coverage.CoveredTypes = findMatchingMembers(inferredType, targetType)

	case *ast.TuplePat:
		// Check if all elements are catch-all patterns (wildcard/ident).
		// This applies regardless of whether the target is a single tuple
		// or a union of tuples.
		allCatchAll := true
		for _, elem := range pat.Elems {
			if _, ok := elem.(*ast.WildcardPat); ok {
				continue
			}
			if _, ok := elem.(*ast.IdentPat); ok {
				continue
			}
			allCatchAll = false
			break
		}

		if union, ok := targetType.(*type_system.UnionType); ok {
			// Target is a union of types (possibly tuple types among them).
			// Check which union members this pattern covers by matching
			// element-wise against each tuple member. We must NOT set
			// IsCatchAll here because the union may contain non-tuple
			// members (e.g., number) that a TuplePat cannot match.
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
							matches = false
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

		tupleTarget, ok := targetType.(*type_system.TupleType)
		if !ok || len(pat.Elems) != len(tupleTarget.Elems) {
			break
		}

		if allCatchAll {
			coverage.IsCatchAll = true
			break
		}

		// Compute per-position coverage for each element pattern.
		var perPositionSets [][]type_system.Type
		for i, elemPat := range pat.Elems {
			elemType := type_system.Prune(tupleTarget.Elems[i])
			elemType = resolveTypeRef(elemType)
			if expanded, ok := expandBooleanType(elemType); ok {
				elemType = expanded
			}

			switch ep := elemPat.(type) {
			case *ast.WildcardPat, *ast.IdentPat:
				// Covers all values at this position. If the element type
				// is non-finite, we return empty coverage — this is correct
				// because the tuple's target type is also non-finite (via
				// expandTupleCoverageSet), so the exhaustiveness checker
				// already knows a catch-all is required.
				members, finite := expandCoverageSet(elemType)
				if !finite {
					return coverage
				}
				perPositionSets = append(perPositionSets, members)
			case *ast.LitPat:
				inferredType := type_system.Prune(ep.InferredType())
				matched := findMatchingMembers(inferredType, elemType)
				if len(matched) == 0 {
					return coverage
				}
				perPositionSets = append(perPositionSets, matched)
			default:
				// Other pattern types at element positions are not yet
				// supported for tuple exhaustiveness.
				return coverage
			}
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

	// LiteralType: compare by value equality.
	if patLit, ok := patternType.(*type_system.LitType); ok {
		if memLit, ok := memberType.(*type_system.LitType); ok {
			return patLit.Lit.Equal(memLit.Lit)
		}
	}

	// TupleType: compare element-wise.
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
	var elementSets [][]type_system.Type
	totalSize := 1
	for _, elem := range tuple.Elems {
		elem = type_system.Prune(elem)
		elem = resolveTypeRef(elem)
		if expanded, ok := expandBooleanType(elem); ok {
			elem = expanded
		}
		members, finite := expandCoverageSet(elem)
		if !finite {
			return nil, false
		}
		totalSize *= len(members)
		if totalSize > 256 {
			return nil, false
		}
		elementSets = append(elementSets, members)
	}
	return cartesianProductTuples(elementSets), true
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
