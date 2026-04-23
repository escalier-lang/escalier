package checker

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// MutabilityTransitionError is reported when a mutability transition
// (mut→immutable or immutable→mut) is attempted while conflicting live
// aliases exist.
type MutabilityTransitionError struct {
	// SourceVar is the variable being aliased.
	SourceVar string
	// TargetVar is the variable being created/assigned.
	TargetVar string
	// ConflictingVars lists the names of live aliases that conflict.
	ConflictingVars []string
	// MutToImmutable is true for Rule 1 (mut→immutable), false for Rule 2 (immutable→mut).
	MutToImmutable bool
	span           ast.Span
}

func (e MutabilityTransitionError) isError() {}
func (e MutabilityTransitionError) Span() ast.Span {
	return e.span
}
func (e MutabilityTransitionError) IsWarning() bool {
	return false
}
func (e MutabilityTransitionError) Message() string {
	if e.MutToImmutable {
		return fmt.Sprintf(
			"cannot transition '%s' from mutable to immutable: conflicting live mutable alias(es): %v",
			e.SourceVar, e.ConflictingVars,
		)
	}
	return fmt.Sprintf(
		"cannot transition '%s' from immutable to mutable: conflicting live immutable alias(es): %v",
		e.SourceVar, e.ConflictingVars,
	)
}

// checkMutabilityTransition verifies that a mutability transition is safe
// at the given program point. Returns an error if conflicting live aliases
// exist.
//
// A transition is only dangerous when both sides are live simultaneously
// after the transition point — i.e. a mutable alias could mutate the value
// while an immutable alias assumes it is unchanged.
//
// Rule 1 (mut → immutable): No live mutable aliases may exist after this point,
// provided the target (immutable) alias is also live.
//
// Rule 2 (immutable → mut): No live immutable aliases may exist after this point,
// provided the target (mutable) alias is also live.
//
// Rule 3: Multiple mutable aliases are always allowed (mut → mut is not a transition).
func (c *Checker) checkMutabilityTransition(
	ctx Context,
	sourceVarID liveness.VarID,
	targetVarID liveness.VarID,
	sourceVarName string,
	targetVarName string,
	sourceMut bool,
	targetMut bool,
	assignRef liveness.StmtRef,
	span ast.Span,
) []Error {
	// Same mutability — no transition (Rule 3 for mut→mut)
	if sourceMut == targetMut {
		return nil
	}

	if ctx.Liveness == nil || ctx.Aliases == nil {
		return nil
	}

	// If the target alias is dead immediately after the transition, there is
	// no window where both sides are live simultaneously, so it's safe.
	if !ctx.Liveness.IsLiveAfter(assignRef, targetVarID) {
		return nil
	}

	var conflicting []string

	// Note: the loop intentionally does NOT skip sourceVarID. The source
	// variable is itself a member of the alias set, and if it is still live
	// after the transition point, it IS a conflicting alias. For example,
	// when creating an immutable alias `snapshot` from a mutable `items`,
	// `items` being live means mutations can still occur through it — that
	// is the conflict we want to report.
	aliasSets := ctx.Aliases.GetAliasSets(sourceVarID)
	for _, aliasSet := range aliasSets {
		for varID, aliasMut := range aliasSet.Members {
			if !ctx.Liveness.IsLiveAfter(assignRef, varID) {
				continue
			}
			if sourceMut && !targetMut {
				// Rule 1: mut → immutable — error if any live mutable alias exists
				if aliasMut == liveness.AliasMutable {
					conflicting = append(conflicting, c.varIDToName(ctx, varID))
				}
			} else {
				// Rule 2: immutable → mut — error if any live immutable alias exists
				if aliasMut == liveness.AliasImmutable {
					conflicting = append(conflicting, c.varIDToName(ctx, varID))
				}
			}
		}
	}

	if len(conflicting) == 0 {
		return nil
	}

	return []Error{&MutabilityTransitionError{
		SourceVar:       sourceVarName,
		TargetVar:       targetVarName,
		ConflictingVars: conflicting,
		MutToImmutable:  sourceMut && !targetMut,
		span:            span,
	}}
}

// isMutableType checks whether a type has a mutable wrapper (MutabilityType
// with MutabilityMutable). This determines how a variable accesses the
// shared value for alias tracking purposes.
func isMutableType(t type_system.Type) bool {
	pruned := type_system.Prune(t)
	if mut, ok := pruned.(*type_system.MutabilityType); ok {
		return mut.Mutability == type_system.MutabilityMutable
	}
	return false
}

// trackAliasesForVarDecl updates the alias tracker and checks mutability
// transitions for a variable declaration with an initializer.
// Only handles simple IdentPat bindings for now (Phase 5-6).
func (c *Checker) trackAliasesForVarDecl(
	ctx Context,
	decl *ast.VarDecl,
	bindings map[string]*type_system.Binding,
	enclosingStmt ast.Stmt,
) []Error {
	// Only handle simple identifier patterns for now.
	// VarID 0 means unset (rename pass didn't run), and negative VarIDs are
	// outer/non-local bindings — only positive VarIDs are local variables
	// with liveness info.
	identPat, ok := decl.Pattern.(*ast.IdentPat)
	if !ok || identPat.VarID <= 0 {
		return nil
	}

	targetVarID := liveness.VarID(identPat.VarID)
	binding := bindings[identPat.Name]
	if binding == nil {
		return nil
	}

	targetMut := isMutableType(binding.Type)
	var aliasMut liveness.AliasMutability
	if targetMut {
		aliasMut = liveness.AliasMutable
	} else {
		aliasMut = liveness.AliasImmutable
	}

	source := liveness.DetermineAliasSource(decl.Init)

	switch source.Kind {
	case liveness.AliasSourceVariable:
		sourceVarID := source.VarIDs[0]
		ctx.Aliases.AddAlias(targetVarID, sourceVarID, aliasMut)

		// Check mutability transition
		stmtRef, hasRef := ctx.StmtToRef[enclosingStmt]
		if hasRef {
			sourceMut := isSourceMutable(ctx, sourceVarID)

			return c.checkMutabilityTransition(
				ctx,
				sourceVarID,
				targetVarID,
				c.varIDToName(ctx, sourceVarID),
				identPat.Name,
				sourceMut,
				targetMut,
				stmtRef,
				decl.Span(),
			)
		}
	case liveness.AliasSourceFresh:
		ctx.Aliases.NewValue(targetVarID, aliasMut)
	default:
		// Unknown or multiple — create a fresh value conservatively
		ctx.Aliases.NewValue(targetVarID, aliasMut)
	}

	return nil
}

// trackAliasesForAssignment updates the alias tracker and checks mutability
// transitions for a variable reassignment (e.g. `b = expr`).
func (c *Checker) trackAliasesForAssignment(
	ctx Context,
	target *ast.IdentExpr,
	rhs ast.Expr,
	targetType type_system.Type,
) []Error {
	if target.VarID <= 0 {
		return nil
	}
	targetVarID := liveness.VarID(target.VarID)
	targetMut := isMutableType(targetType)
	var aliasMut liveness.AliasMutability
	if targetMut {
		aliasMut = liveness.AliasMutable
	} else {
		aliasMut = liveness.AliasImmutable
	}

	source := liveness.DetermineAliasSource(rhs)

	switch source.Kind {
	case liveness.AliasSourceVariable:
		sourceVarID := source.VarIDs[0]

		// Check mutability transition before reassigning
		stmtRef, hasRef := ctx.StmtToRef[ctx.CurrentStmt]
		if hasRef {
			sourceMut := isSourceMutable(ctx, sourceVarID)

			transErrors := c.checkMutabilityTransition(
				ctx,
				sourceVarID,
				targetVarID,
				c.varIDToName(ctx, sourceVarID),
				target.Name,
				sourceMut,
				targetMut,
				stmtRef,
				target.Span(),
			)
			if len(transErrors) > 0 {
				// Still perform reassignment so alias state stays consistent
				ctx.Aliases.Reassign(targetVarID, &sourceVarID, aliasMut)
				return transErrors
			}
		}

		ctx.Aliases.Reassign(targetVarID, &sourceVarID, aliasMut)
	case liveness.AliasSourceFresh:
		ctx.Aliases.Reassign(targetVarID, nil, aliasMut)
	default:
		ctx.Aliases.Reassign(targetVarID, nil, aliasMut)
	}

	return nil
}

// isSourceMutable checks whether a source variable is registered as mutable
// in its alias sets. Returns true if the source holds a mutable reference.
func isSourceMutable(ctx Context, sourceVarID liveness.VarID) bool {
	for _, set := range ctx.Aliases.GetAliasSets(sourceVarID) {
		if m, exists := set.Members[sourceVarID]; exists {
			return m == liveness.AliasMutable
		}
	}
	return false
}

// varIDToName resolves a VarID back to a variable name for error messages.
// It searches the VarID-to-name mapping built during the rename pass.
func (c *Checker) varIDToName(ctx Context, id liveness.VarID) string {
	if ctx.VarIDNames != nil {
		if name, ok := ctx.VarIDNames[id]; ok {
			return name
		}
	}
	return fmt.Sprintf("var#%d", id)
}
