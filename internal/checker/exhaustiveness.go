package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

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
