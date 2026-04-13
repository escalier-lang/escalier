package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
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
	coveredSet := make(map[int]bool) // index into coverageSet -> covered
	var redundantCases []RedundantCase
	hasCatchAll := false

	for i, cov := range coverages {
		// Skip guarded branches for redundancy checking — they cover
		// nothing but are not redundant (they're runtime filters).
		if cov.HasGuard {
			continue
		}

		if cov.IsCatchAll {
			// Check redundancy: if all types are already covered, this
			// catch-all is redundant.
			if isFinite && len(coveredSet) == len(coverageSet) {
				redundantCases = append(redundantCases, RedundantCase{
					CaseIndex: i,
					Span:      expr.Cases[i].Pattern.Span(),
				})
			}
			// Mark everything as covered.
			hasCatchAll = true
			for j := range coverageSet {
				coveredSet[j] = true
			}
			continue
		}

		if len(cov.CoveredTypes) > 0 {
			// Check redundancy: if every type this branch covers is
			// already in the covered set, the branch is redundant.
			allAlreadyCovered := true
			for _, covType := range cov.CoveredTypes {
				idx := indexInCoverageSet(covType, coverageSet)
				if idx == -1 || !coveredSet[idx] {
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
					coveredSet[idx] = true
				}
			}
		}
	}

	// Step 6: Compute uncovered types.
	var uncoveredTypes []type_system.Type
	if isFinite {
		// For finite types, report each uncovered member in declaration order.
		for i, member := range coverageSet {
			if !coveredSet[i] {
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
				// type directly. Covers the whole target.
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
		// Handled specially in Phase 5. For now, treat as non-covering
		// unless it would be a catch-all (all elements are wildcards/idents).

	case *ast.RestPat:
		// Rest patterns in match expressions are unusual; treat as non-covering.
	}

	return coverage
}

// getCustomMatcherParamType extracts the param type from the [Symbol.customMatcher]
// method on an extractor's object type. This param type identifies which
// variant the extractor matches.
func (c *Checker) getCustomMatcherParamType(ext *type_system.ExtractorType) type_system.Type {
	extractor := type_system.Prune(ext.Extractor)
	extObj, ok := extractor.(*type_system.ObjectType)
	if !ok {
		return nil
	}
	for _, elem := range extObj.Elems {
		methodElem, ok := elem.(*type_system.MethodElem)
		if !ok {
			continue
		}
		if methodElem.Name.Kind == type_system.SymObjTypeKeyKind && methodElem.Name.Sym == c.CustomMatcherSymbolID {
			if len(methodElem.Fn.Params) == 1 {
				return type_system.Prune(methodElem.Fn.Params[0].Type)
			}
		}
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

	return false
}
