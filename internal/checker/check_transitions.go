package checker

import (
	"fmt"
	"sort"
	"strings"

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
	vars := "'" + strings.Join(e.ConflictingVars, "', '") + "'"
	// When the conflicting variable is the source itself, the message is
	// straightforward. When it's a different variable (an alias), we
	// clarify the relationship so the user understands the connection.
	sameAsSource := len(e.ConflictingVars) == 1 && e.ConflictingVars[0] == e.SourceVar
	if e.MutToImmutable {
		if sameAsSource {
			return fmt.Sprintf(
				"cannot assign '%s' to immutable '%s': '%s' is still used mutably after this point",
				e.SourceVar, e.TargetVar, e.SourceVar,
			)
		}
		return fmt.Sprintf(
			"cannot assign '%s' to immutable '%s': %s still has mutable access to '%s' after this point",
			e.SourceVar, e.TargetVar, vars, e.SourceVar,
		)
	}
	if sameAsSource {
		return fmt.Sprintf(
			"cannot assign '%s' to mutable '%s': '%s' is still used immutably after this point",
			e.SourceVar, e.TargetVar, e.SourceVar,
		)
	}
	return fmt.Sprintf(
		"cannot assign '%s' to mutable '%s': %s still has immutable access to '%s' after this point",
		e.SourceVar, e.TargetVar, vars, e.SourceVar,
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

	// Note: the loop intentionally does NOT skip sourceVarID. The source
	// variable is itself a member of the alias set, and if it is still live
	// after the transition point, it IS a conflicting alias. For example,
	// when creating an immutable alias `snapshot` from a mutable `items`,
	// `items` being live means mutations can still occur through it — that
	// is the conflict we want to report.
	//
	// A variable may belong to multiple alias sets (conditional aliasing),
	// so we deduplicate by collecting names into a set.
	conflictingSet := make(map[string]struct{})
	aliasSets := ctx.Aliases.GetAliasSets(sourceVarID)
	for _, aliasSet := range aliasSets {
		for varID, aliasMut := range aliasSet.Members {
			if !ctx.Liveness.IsLiveAfter(assignRef, varID) {
				continue
			}
			if sourceMut && !targetMut {
				// Rule 1: mut → immutable — error if any live mutable alias exists
				if aliasMut == liveness.AliasMutable {
					conflictingSet[c.varIDToName(ctx, varID)] = struct{}{}
				}
			} else {
				// Rule 2: immutable → mut — error if any live immutable alias exists
				if aliasMut == liveness.AliasImmutable {
					conflictingSet[c.varIDToName(ctx, varID)] = struct{}{}
				}
			}
		}
	}

	if len(conflictingSet) == 0 {
		return nil
	}

	conflicting := make([]string, 0, len(conflictingSet))
	for name := range conflictingSet {
		conflicting = append(conflicting, name)
	}
	sort.Strings(conflicting)

	return []Error{&MutabilityTransitionError{
		SourceVar:       sourceVarName,
		TargetVar:       targetVarName,
		ConflictingVars: conflicting,
		MutToImmutable:  sourceMut && !targetMut,
		span:            span,
	}}
}

// isValueType returns true if the type is a primitive or literal type
// (or a union of such types). Value types have copy semantics — assigning
// them to another variable creates an independent copy, so alias tracking
// is unnecessary.
func isValueType(t type_system.Type) bool {
	pruned := type_system.Prune(t)
	switch p := pruned.(type) {
	case *type_system.PrimType, *type_system.LitType:
		return true
	case *type_system.UnionType:
		for _, member := range p.Types {
			if !isValueType(member) {
				return false
			}
		}
		return len(p.Types) > 0
	}
	return false
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
func (c *Checker) trackAliasesForVarDecl(
	ctx Context,
	decl *ast.VarDecl,
	bindings map[string]*type_system.Binding,
	enclosingStmt ast.Stmt,
) []Error {
	source := determineCheckerAliasSource(decl.Init)

	var errors []Error
	switch pat := decl.Pattern.(type) {
	case *ast.IdentPat:
		errors = c.trackAliasesForIdentPat(ctx, pat, bindings, source, enclosingStmt, decl.Span())

		// Closure capture aliasing — when the initializer is a FuncExpr, add
		// the closure variable to each captured variable's alias set in the
		// enclosing function.
		// TODO: This only handles named closures (val f = fn() { ... }).
		// Anonymous closures passed as call arguments (e.g. items.map(fn(x) { captured }))
		// are not tracked because they have no VarID to add to alias sets.
		if funcExpr, ok := decl.Init.(*ast.FuncExpr); ok && pat.VarID > 0 {
			closureVarID := liveness.VarID(pat.VarID)
			captureErrors := c.trackCapturedAliases(ctx, funcExpr, closureVarID, enclosingStmt, decl.Span())
			errors = append(errors, captureErrors...)
		}

	case *ast.ObjectPat:
		errors = c.trackAliasesForDestructuringPat(ctx, pat, bindings, source, enclosingStmt, decl.Span())

	case *ast.TuplePat:
		errors = c.trackAliasesForDestructuringPat(ctx, pat, bindings, source, enclosingStmt, decl.Span())

	case *ast.ExtractorPat:
		errors = c.trackAliasesForDestructuringPat(ctx, pat, bindings, source, enclosingStmt, decl.Span())
	}

	return errors
}

// trackAliasesForIdentPat handles alias tracking for a simple identifier
// pattern binding (e.g. `val x = expr`).
func (c *Checker) trackAliasesForIdentPat(
	ctx Context,
	identPat *ast.IdentPat,
	bindings map[string]*type_system.Binding,
	source liveness.AliasSource,
	enclosingStmt ast.Stmt,
	span ast.Span,
) []Error {
	if identPat.VarID <= 0 {
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
				span,
			)
		}
	case liveness.AliasSourceMultiple:
		// Conditional aliasing: the target aliases all possible
		// source variables. Add it to each source's alias sets.
		for _, sourceVarID := range source.VarIDs {
			ctx.Aliases.AddAlias(targetVarID, sourceVarID, aliasMut)
		}

		// Check mutability transition against each source
		var allErrors []Error
		stmtRef, hasRef := ctx.StmtToRef[enclosingStmt]
		if hasRef {
			for _, sourceVarID := range source.VarIDs {
				sourceMut := isSourceMutable(ctx, sourceVarID)
				transErrors := c.checkMutabilityTransition(
					ctx,
					sourceVarID,
					targetVarID,
					c.varIDToName(ctx, sourceVarID),
					identPat.Name,
					sourceMut,
					targetMut,
					stmtRef,
					span,
				)
				allErrors = append(allErrors, transErrors...)
			}
		}
		return allErrors
	case liveness.AliasSourceFresh:
		ctx.Aliases.NewValue(targetVarID, aliasMut)
	case liveness.AliasSourceUnknown:
		// Unknown — create a fresh value conservatively
		ctx.Aliases.NewValue(targetVarID, aliasMut)
	default:
		panic(fmt.Sprintf("trackAliasesForIdentPat: unhandled alias source kind %d", source.Kind))
	}

	return nil
}

// trackCapturedAliases adds the closure variable to the alias sets of each
// captured variable from the enclosing scope. A read-only capture creates an
// immutable alias; a mutable capture creates a mutable alias. It also checks
// mutability transitions for each capture — for example, a read-only capture
// of a mutable variable is a mut→immut transition that must be checked against
// live mutable aliases.
func (c *Checker) trackCapturedAliases(
	ctx Context,
	funcExpr *ast.FuncExpr,
	closureVarID liveness.VarID,
	enclosingStmt ast.Stmt,
	span ast.Span,
) []Error {
	if ctx.Aliases == nil {
		return nil
	}

	captures := liveness.AnalyzeCaptures(funcExpr)
	if len(captures) == 0 {
		return nil
	}

	var allErrors []Error
	for _, capture := range captures {
		// Look up the captured variable's binding in the enclosing scope.
		// The scope chain handles shadowing correctly — GetValue returns
		// the innermost binding, which is the one in scope at this point.
		binding := ctx.Scope.GetValue(capture.Name)
		if binding == nil || binding.VarID <= 0 {
			continue
		}
		// Primitives and literals have value semantics — reassigning a
		// captured primitive inside a closure can't affect other variables
		// that copied the value, so alias tracking is unnecessary.
		if isValueType(binding.Type) {
			continue
		}
		enclosingVarID := liveness.VarID(binding.VarID)
		mut := liveness.AliasImmutable
		if capture.IsMutable {
			mut = liveness.AliasMutable
		}
		ctx.Aliases.AddAlias(closureVarID, enclosingVarID, mut)

		// Check mutability transition for this capture.
		stmtRef, hasRef := ctx.StmtToRef[enclosingStmt]
		if hasRef {
			sourceMut := isSourceMutable(ctx, enclosingVarID)
			targetMut := capture.IsMutable
			transErrors := c.checkMutabilityTransition(
				ctx,
				enclosingVarID,
				closureVarID,
				capture.Name,
				c.varIDToName(ctx, closureVarID),
				sourceMut,
				targetMut,
				stmtRef,
				span,
			)
			allErrors = append(allErrors, transErrors...)
		}
	}
	return allErrors
}

// trackAliasesForDestructuringPat handles alias tracking for destructuring
// patterns (ObjectPat and TuplePat). Each extracted binding is added to the
// source variable's alias set (conservative: object-level, not property-level).
func (c *Checker) trackAliasesForDestructuringPat(
	ctx Context,
	pat ast.Pat,
	bindings map[string]*type_system.Binding,
	source liveness.AliasSource,
	enclosingStmt ast.Stmt,
	span ast.Span,
) []Error {
	// Collect all VarIDs from the destructuring pattern.
	varIDs := collectPatternVarIDs(pat)
	if len(varIDs) == 0 {
		return nil
	}

	switch source.Kind {
	case liveness.AliasSourceVariable, liveness.AliasSourceMultiple:
		// Each destructured binding aliases the source(s).
		var allErrors []Error
		for _, targetVarID := range varIDs {
			targetName := c.varIDToName(ctx, targetVarID)
			binding := bindings[targetName]
			targetMut := binding != nil && isMutableType(binding.Type)
			var aliasMut liveness.AliasMutability
			if targetMut {
				aliasMut = liveness.AliasMutable
			} else {
				aliasMut = liveness.AliasImmutable
			}

			for _, sourceVarID := range source.VarIDs {
				ctx.Aliases.AddAlias(targetVarID, sourceVarID, aliasMut)
			}

			// Check mutability transition
			stmtRef, hasRef := ctx.StmtToRef[enclosingStmt]
			if hasRef {
				for _, sourceVarID := range source.VarIDs {
					sourceMut := isSourceMutable(ctx, sourceVarID)
					transErrors := c.checkMutabilityTransition(
						ctx,
						sourceVarID,
						targetVarID,
						c.varIDToName(ctx, sourceVarID),
						targetName,
						sourceMut,
						targetMut,
						stmtRef,
						span,
					)
					allErrors = append(allErrors, transErrors...)
				}
			}
		}
		return allErrors
	case liveness.AliasSourceFresh, liveness.AliasSourceUnknown:
		// Fresh value (or unknown): each destructured binding gets its own fresh set.
		for _, targetVarID := range varIDs {
			targetName := c.varIDToName(ctx, targetVarID)
			binding := bindings[targetName]
			targetMut := binding != nil && isMutableType(binding.Type)
			var aliasMut liveness.AliasMutability
			if targetMut {
				aliasMut = liveness.AliasMutable
			} else {
				aliasMut = liveness.AliasImmutable
			}
			ctx.Aliases.NewValue(targetVarID, aliasMut)
		}
	default:
		panic(fmt.Sprintf("trackAliasesForDestructuringPat: unhandled alias source kind %d", source.Kind))
	}

	return nil
}

// collectPatternVarIDs collects all positive VarIDs from a pattern, recursively
// handling nested patterns (ObjectPat, TuplePat, ExtractorPat, IdentPat).
func collectPatternVarIDs(pat ast.Pat) []liveness.VarID {
	var varIDs []liveness.VarID
	switch p := pat.(type) {
	case *ast.IdentPat:
		if p.VarID > 0 {
			varIDs = append(varIDs, liveness.VarID(p.VarID))
		}
	case *ast.ObjectPat:
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjKeyValuePat:
				varIDs = append(varIDs, collectPatternVarIDs(e.Value)...)
			case *ast.ObjShorthandPat:
				if e.VarID > 0 {
					varIDs = append(varIDs, liveness.VarID(e.VarID))
				}
			case *ast.ObjRestPat:
				varIDs = append(varIDs, collectPatternVarIDs(e.Pattern)...)
			}
		}
	case *ast.TuplePat:
		for _, elem := range p.Elems {
			varIDs = append(varIDs, collectPatternVarIDs(elem)...)
		}
	case *ast.ExtractorPat:
		for _, arg := range p.Args {
			varIDs = append(varIDs, collectPatternVarIDs(arg)...)
		}
	default:
		panic(fmt.Sprintf("collectPatternVarIDs: unhandled pattern type %T", pat))
	}
	return varIDs
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

	source := determineCheckerAliasSource(rhs)

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
	case liveness.AliasSourceMultiple:
		// Conditional aliasing: reassign to all sources.
		// ReassignMulti is called before checking transitions (unlike the
		// single-source case) because we want alias state to be consistent
		// regardless of whether errors are reported.
		ctx.Aliases.ReassignMulti(targetVarID, source.VarIDs, aliasMut)

		// Check mutability transition against each source
		var allErrors []Error
		stmtRef, hasRef := ctx.StmtToRef[ctx.CurrentStmt]
		if hasRef {
			for _, sourceVarID := range source.VarIDs {
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
				allErrors = append(allErrors, transErrors...)
			}
		}
		if len(allErrors) > 0 {
			return allErrors
		}
	case liveness.AliasSourceFresh:
		ctx.Aliases.Reassign(targetVarID, nil, aliasMut)
	case liveness.AliasSourceUnknown:
		ctx.Aliases.Reassign(targetVarID, nil, aliasMut)
	default:
		panic(fmt.Sprintf("trackAliasesForAssignment: unhandled alias source kind %d", source.Kind))
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

// trackAliasesForPropAssignment handles alias tracking for property
// assignments like `obj.prop = value`. When the RHS aliases a variable, the
// alias sets of the object and the RHS source are merged.
func (c *Checker) trackAliasesForPropAssignment(
	ctx Context,
	lhs ast.Expr,
	rhs ast.Expr,
) {
	// Find the root object variable of the member/index chain.
	objVarID := rootObjectVarID(lhs)
	if objVarID <= 0 {
		return
	}

	source := determineCheckerAliasSource(rhs)
	switch source.Kind {
	case liveness.AliasSourceVariable:
		ctx.Aliases.MergeAliasSets(objVarID, source.VarIDs[0])
	case liveness.AliasSourceMultiple:
		for _, srcID := range source.VarIDs {
			ctx.Aliases.MergeAliasSets(objVarID, srcID)
		}
	case liveness.AliasSourceFresh, liveness.AliasSourceUnknown:
		// No alias relationship to track.
	default:
		panic(fmt.Sprintf("trackAliasesForPropAssignment: unhandled alias source kind %d", source.Kind))
	}
}

// rootObjectVarID iteratively walks a member/index expression chain
// (e.g. a.b.c, a[b][c]) to find the root object's VarID.
// Returns 0 if the root is not a local variable.
func rootObjectVarID(expr ast.Expr) liveness.VarID {
	for {
		switch e := expr.(type) {
		case *ast.MemberExpr:
			expr = e.Object
		case *ast.IndexExpr:
			expr = e.Object
		case *ast.IdentExpr:
			if e.VarID > 0 {
				return liveness.VarID(e.VarID)
			}
			return 0
		default:
			return 0
		}
	}
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
