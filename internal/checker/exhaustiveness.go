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

	// Step 3: Group non-guarded, non-catch-all branches by which coverage
	// set member they cover. Also detect whether a catch-all is present.
	type memberGroup struct {
		coverages   []CaseCoverage
		caseIndices []int
	}
	groups := make(map[int]*memberGroup)
	hasCatchAll := false

	for i, cov := range coverages {
		if cov.HasGuard {
			continue
		}
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
				groups[idx].caseIndices = append(groups[idx].caseIndices, i)
			}
		}
	}

	// Step 4: Determine coverage for each member, including inner
	// exhaustiveness checking. A member is fully covered if a catch-all
	// is present or its branches' inner patterns collectively exhaust
	// the inner type.
	coveredSet := set.NewSet[int]()
	var partialCoverages []PartialCoverage
	partialMembers := set.NewSet[int]()
	nestedGroupBranches := set.NewSet[int]()

	for idx, member := range coverageSet {
		if hasCatchAll {
			coveredSet.Add(idx)
			continue
		}

		group := groups[idx]
		if group == nil {
			continue // no branches cover this member
		}

		// Check inner exhaustiveness for this member's branches.
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

		// When multiple branches cover the same member and at least one
		// has partial inner patterns, protect all of them from redundancy
		// detection — they contribute different inner values.
		if len(group.coverages) > 1 {
			for _, bc := range group.coverages {
				if branchPartiallyCovers(bc, member) {
					for _, ci := range group.caseIndices {
						nestedGroupBranches.Add(ci)
					}
					break
				}
			}
		}
	}

	// Step 5: Detect redundancy by processing branches in order.
	var redundantCases []RedundantCase
	contributed := set.NewSet[int]() // members contributed by earlier branches
	seenCatchAll := false

	for i, cov := range coverages {
		if cov.HasGuard {
			continue
		}

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

		if len(cov.CoveredTypes) > 0 {
			// Branches in nested groups skip redundancy detection — they
			// may cover different inner values than earlier branches.
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

			for _, covType := range cov.CoveredTypes {
				idx := indexInCoverageSet(covType, coverageSet)
				if idx != -1 {
					contributed.Add(idx)
				}
			}
		}
	}

	// Step 6: Compute uncovered types.
	var uncoveredTypes []type_system.Type
	if isFinite {
		// For finite types, report each uncovered member in declaration order.
		// Exclude partially covered members — they are reported separately.
		for i, member := range coverageSet {
			if !coveredSet.Contains(i) && !partialMembers.Contains(i) {
				uncoveredTypes = append(uncoveredTypes, member)
			}
		}
	} else if !hasCatchAll {
		// Non-finite types require a catch-all. If none was found, report
		// the target type itself as uncovered.
		uncoveredTypes = []type_system.Type{targetType}
	}

	return &ExhaustivenessResult{
		IsExhaustive:     len(uncoveredTypes) == 0 && len(partialCoverages) == 0,
		UncoveredTypes:   uncoveredTypes,
		RedundantCases:   redundantCases,
		IsNonFinite:      !isFinite,
		PartialCoverages: partialCoverages,
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

	// A literal type (e.g., "circle") is finite with exactly one member.
	// This is needed for nested exhaustiveness checking where a property
	// type is a literal (e.g., the discriminant property in a union member).
	if _, ok := targetType.(*type_system.LitType); ok {
		return []type_system.Type{targetType}, true
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
	// Guarded branches cover nothing for exhaustiveness purposes (R6).
	// They are runtime filters and should not be treated as covering any type.
	if matchCase.Guard != nil {
		return CaseCoverage{
			Pattern:  matchCase.Pattern,
			HasGuard: true,
		}
	}

	coverage := c.computePatternCoverage(matchCase.Pattern, targetType)
	return coverage
}

// computePatternCoverage determines which types from the target type a single
// pattern covers. This is the pattern-specific coverage logic shared by both
// top-level match case analysis and inner pattern checking.
func (c *Checker) computePatternCoverage(
	pat ast.Pat,
	targetType type_system.Type,
) CaseCoverage {
	coverage := CaseCoverage{Pattern: pat}

	switch pat := pat.(type) {
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
		// Check if all elements are catch-all patterns (wildcard/ident/rest).
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
			if _, ok := elem.(*ast.RestPat); ok {
				continue
			}
			allCatchAll = false
			break
		}

		if union, ok := targetType.(*type_system.UnionType); ok {
			// Target is a union whose members are already concrete tuples.
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

		tupleTarget, ok := targetType.(*type_system.TupleType)
		if !ok || len(pat.Elems) != len(tupleTarget.Elems) {
			break
		}

		if allCatchAll {
			coverage.IsCatchAll = true
			break
		}

		// Target is a single tuple type. The coverage set is the Cartesian
		// product of each element position's expanded types, so we compute
		// per-position coverage and take the product.
		//
		// Example: target [boolean, boolean]
		//   coverage set = [true,true], [true,false], [false,true], [false,false]
		//   pattern [true, _] → per-position sets: {true} × {true, false}
		//                     → covers [true,true], [true,false]
		perPositionSets := make([][]type_system.Type, len(pat.Elems))
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
				perPositionSets[i] = members
			case *ast.LitPat:
				inferredType := type_system.Prune(ep.InferredType())
				matched := findMatchingMembers(inferredType, elemType)
				if len(matched) == 0 {
					return coverage
				}
				perPositionSets[i] = matched
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
		elementSets[i] = members
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

// branchFullyCoversMember checks if a single branch's inner patterns are all
// catch-alls (wildcard/identifier), meaning it fully covers the union member
// without needing inner exhaustiveness checking.
func branchFullyCoversMember(cov CaseCoverage) bool {
	switch pat := cov.Pattern.(type) {
	case *ast.ExtractorPat:
		if len(pat.Args) == 0 {
			return true
		}
		for _, arg := range pat.Args {
			switch arg.(type) {
			case *ast.WildcardPat, *ast.IdentPat:
				continue
			default:
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
			switch elem.(type) {
			case *ast.WildcardPat, *ast.IdentPat, *ast.RestPat:
				continue
			default:
				return false
			}
		}
		return true
	default:
		return true // other pattern types don't have inner patterns
	}
}

// branchPartiallyCovers checks if a branch has inner patterns that only
// partially cover the given member. This is like !branchFullyCoversMember but
// also considers the member's type: for TuplePat, if every LitPat element
// exactly matches the corresponding LitType element in the member, the branch
// fully covers that specific member (not partial). This distinction prevents
// clearing genuine redundancy warnings for duplicate tuple branches.
func branchPartiallyCovers(cov CaseCoverage, member type_system.Type) bool {
	if branchFullyCoversMember(cov) {
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
				switch elemPat.(type) {
				case *ast.WildcardPat, *ast.IdentPat:
					// catch-all — always matches
				case *ast.LitPat:
					p := elemPat.(*ast.LitPat)
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
	branchCoverages []CaseCoverage,
	member type_system.Type,
) *ExhaustivenessResult {
	if len(branchCoverages) == 0 {
		return nil
	}

	// Quick check: if any branch fully covers the member
	// (all inner patterns are catch-alls), it's exhaustive.
	for _, cov := range branchCoverages {
		if branchFullyCoversMember(cov) {
			return &ExhaustivenessResult{IsExhaustive: true}
		}
	}

	// Dispatch based on pattern type.
	switch branchCoverages[0].Pattern.(type) {
	case *ast.ExtractorPat:
		return c.checkExtractorInnerExhaustiveness(branchCoverages)
	case *ast.ObjectPat:
		return c.checkObjectInnerExhaustiveness(branchCoverages, member)
	case *ast.TuplePat:
		return c.checkTupleInnerExhaustiveness(branchCoverages, member)
	default:
		return nil
	}
}

// checkExtractorInnerExhaustiveness checks whether extractor pattern arguments
// collectively exhaust the extractor's return type.
func (c *Checker) checkExtractorInnerExhaustiveness(
	branchCoverages []CaseCoverage,
) *ExhaustivenessResult {
	firstPat, ok := branchCoverages[0].Pattern.(*ast.ExtractorPat)
	if !ok {
		return nil
	}

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
			extPat, ok := cov.Pattern.(*ast.ExtractorPat)
			if !ok || len(extPat.Args) == 0 {
				continue
			}
			innerPatterns = append(innerPatterns, extPat.Args[0])
		}
		return c.checkInnerPatternsExhaustive(innerPatterns, returnTuple.Elems[0])
	}

	// Multi-argument extractor: use Cartesian product tracking for finite
	// inner types (like tuple exhaustiveness in Phase 5). For non-finite
	// inner types, fall back to per-position checking.
	innerCoverageSet, innerFinite := expandTupleCoverageSet(returnTuple)
	if innerFinite {
		// Finite inner type: track exact combinations.
		innerCoveredSet := set.NewSet[int]()

		for _, cov := range branchCoverages {
			extPat, ok := cov.Pattern.(*ast.ExtractorPat)
			if !ok || len(extPat.Args) != len(returnTuple.Elems) {
				continue
			}

			// Check if all args are catch-alls → covers everything.
			allCatchAll := true
			for _, arg := range extPat.Args {
				switch arg.(type) {
				case *ast.WildcardPat, *ast.IdentPat:
				default:
					allCatchAll = false
				}
				if !allCatchAll {
					break
				}
			}
			if allCatchAll {
				return &ExhaustivenessResult{IsExhaustive: true}
			}

			// Compute per-position coverage sets and take Cartesian product.
			perPositionSets := make([][]type_system.Type, len(extPat.Args))
			valid := true
			for i, argPat := range extPat.Args {
				elemType := type_system.Prune(returnTuple.Elems[i])
				elemType = resolveTypeRef(elemType)
				if expanded, ok := expandBooleanType(elemType); ok {
					elemType = expanded
				}

				switch p := argPat.(type) {
				case *ast.WildcardPat, *ast.IdentPat:
					members, finite := expandCoverageSet(elemType)
					if !finite {
						valid = false
					} else {
						perPositionSets[i] = members
					}
				case *ast.LitPat:
					inferredType := type_system.Prune(p.InferredType())
					matched := findMatchingMembers(inferredType, elemType)
					if len(matched) == 0 {
						valid = false
					} else {
						perPositionSets[i] = matched
					}
				default:
					valid = false
				}
				if !valid {
					break
				}
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

	// Non-finite inner type: check per-position as approximation.
	for i, elemType := range returnTuple.Elems {
		var posPatterns []ast.Pat
		hasCatchAll := false
		for _, cov := range branchCoverages {
			extPat, ok := cov.Pattern.(*ast.ExtractorPat)
			if !ok || i >= len(extPat.Args) {
				continue
			}
			switch extPat.Args[i].(type) {
			case *ast.WildcardPat, *ast.IdentPat:
				hasCatchAll = true
			default:
				posPatterns = append(posPatterns, extPat.Args[i])
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

// checkObjectInnerExhaustiveness checks whether object patterns covering the
// same union member collectively exhaust the member's property types.
// Each property is checked independently.
func (c *Checker) checkObjectInnerExhaustiveness(
	branchCoverages []CaseCoverage,
	member type_system.Type,
) *ExhaustivenessResult {
	memberObj, ok := type_system.Prune(resolveTypeRef(member)).(*type_system.ObjectType)
	if !ok {
		return nil
	}

	for _, elem := range memberObj.Elems {
		prop, ok := elem.(*type_system.PropertyElem)
		if !ok {
			continue
		}
		if prop.Name.Kind != type_system.StrObjTypeKeyKind {
			continue
		}
		propName := prop.Name.Str
		propType := type_system.Prune(prop.Value)

		var propPatterns []ast.Pat
		propFullyCovered := false

		for _, cov := range branchCoverages {
			objPat, ok := cov.Pattern.(*ast.ObjectPat)
			if !ok {
				propFullyCovered = true // non-object pattern → treat as catch-all
				break
			}

			found := false
			for _, objElem := range objPat.Elems {
				switch e := objElem.(type) {
				case *ast.ObjKeyValuePat:
					if e.Key.Name == propName {
						propPatterns = append(propPatterns, e.Value)
						found = true
					}
				case *ast.ObjShorthandPat:
					if e.Key.Name == propName {
						propFullyCovered = true // shorthand = catch-all binding
						found = true
					}
				}
				if found {
					break
				}
			}
			if !found {
				propFullyCovered = true // property omitted → implicit wildcard
			}
			if propFullyCovered {
				break
			}
		}

		if propFullyCovered {
			continue
		}

		result := c.checkInnerPatternsExhaustive(propPatterns, propType)
		if !result.IsExhaustive {
			return result
		}
	}

	return &ExhaustivenessResult{IsExhaustive: true}
}

// checkTupleInnerExhaustiveness checks whether tuple patterns covering the
// same union member collectively exhaust the member's element types. For finite
// element types, it uses Cartesian product tracking; for non-finite elements,
// it falls back to per-position checking.
func (c *Checker) checkTupleInnerExhaustiveness(
	branchCoverages []CaseCoverage,
	member type_system.Type,
) *ExhaustivenessResult {
	memberTuple, ok := type_system.Prune(resolveTypeRef(member)).(*type_system.TupleType)
	if !ok {
		return nil
	}

	// Try Cartesian product approach for finite element types.
	innerCoverageSet, innerFinite := expandTupleCoverageSet(memberTuple)
	if innerFinite {
		innerCoveredSet := set.NewSet[int]()

		for _, cov := range branchCoverages {
			tuplePat, ok := cov.Pattern.(*ast.TuplePat)
			if !ok || len(tuplePat.Elems) != len(memberTuple.Elems) {
				continue
			}

			perPositionSets := make([][]type_system.Type, len(tuplePat.Elems))
			valid := true
			for i, elemPat := range tuplePat.Elems {
				elemType := type_system.Prune(memberTuple.Elems[i])
				elemType = resolveTypeRef(elemType)
				if expanded, ok := expandBooleanType(elemType); ok {
					elemType = expanded
				}

				switch p := elemPat.(type) {
				case *ast.WildcardPat, *ast.IdentPat:
					members, finite := expandCoverageSet(elemType)
					if !finite {
						valid = false
					} else {
						perPositionSets[i] = members
					}
				case *ast.LitPat:
					inferredType := type_system.Prune(p.InferredType())
					matched := findMatchingMembers(inferredType, elemType)
					if len(matched) == 0 {
						valid = false
					} else {
						perPositionSets[i] = matched
					}
				default:
					valid = false
				}
				if !valid {
					break
				}
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
		for i, m := range innerCoverageSet {
			if !innerCoveredSet.Contains(i) {
				uncoveredTypes = append(uncoveredTypes, m)
			}
		}

		return &ExhaustivenessResult{
			IsExhaustive:   len(uncoveredTypes) == 0,
			UncoveredTypes: uncoveredTypes,
		}
	}

	// Non-finite element types: check each position independently.
	for i, elemType := range memberTuple.Elems {
		var posPatterns []ast.Pat
		hasCatchAll := false
		for _, cov := range branchCoverages {
			tuplePat, ok := cov.Pattern.(*ast.TuplePat)
			if !ok || i >= len(tuplePat.Elems) {
				continue
			}
			switch tuplePat.Elems[i].(type) {
			case *ast.WildcardPat, *ast.IdentPat:
				hasCatchAll = true
			default:
				posPatterns = append(posPatterns, tuplePat.Elems[i])
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
// exhaust a target type. This is the core recursive check for nested
// exhaustiveness. It uses computePatternCoverage to handle all pattern types
// (ExtractorPat, ObjectPat, LitPat, etc.) and recursively checks inner
// patterns of covered union members.
func (c *Checker) checkInnerPatternsExhaustive(
	patterns []ast.Pat,
	targetType type_system.Type,
) *ExhaustivenessResult {
	targetType = type_system.Prune(targetType)
	targetType = resolveTypeRef(targetType)

	if expanded, ok := expandBooleanType(targetType); ok {
		targetType = expanded
	}

	coverageSet, isFinite := expandCoverageSet(targetType)
	coveredSet := set.NewSet[int]()
	hasCatchAll := false

	// Compute coverage for each inner pattern.
	coverages := make([]CaseCoverage, len(patterns))
	for i, pat := range patterns {
		coverages[i] = c.computePatternCoverage(pat, targetType)
	}

	for _, cov := range coverages {
		if cov.IsCatchAll {
			hasCatchAll = true
			for j := range coverageSet {
				coveredSet.Add(j)
			}
			continue
		}
		for _, covType := range cov.CoveredTypes {
			if idx := indexInCoverageSet(covType, coverageSet); idx != -1 {
				coveredSet.Add(idx)
			}
		}
	}

	if hasCatchAll {
		return &ExhaustivenessResult{IsExhaustive: true}
	}

	// Recursively check nested exhaustiveness for covered members.
	var partialCoverages []PartialCoverage
	partialMembers := set.NewSet[int]()

	if isFinite {
		for idx, member := range coverageSet {
			if !coveredSet.Contains(idx) {
				continue
			}

			var branchCoverages []CaseCoverage
			for _, cov := range coverages {
				if cov.IsCatchAll {
					continue
				}
				for _, covType := range cov.CoveredTypes {
					if typesMatchForCoverage(covType, member) {
						branchCoverages = append(branchCoverages, cov)
						break
					}
				}
			}

			innerResult := c.checkNestedExhaustiveness(branchCoverages, member)
			if innerResult != nil && !innerResult.IsExhaustive {
				coveredSet.Remove(idx)
				partialMembers.Add(idx)
				partialCoverages = append(partialCoverages, PartialCoverage{
					Member:      member,
					InnerResult: innerResult,
				})
			}
		}
	}

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
