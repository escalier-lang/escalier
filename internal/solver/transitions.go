package solver

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Mutability-transition checking, ported from internal/checker/check_transitions.go
// and internal/checker/liveness_prepass.go (M4 G1). The liveness/CFG/alias-tracking
// machinery in internal/liveness is reused verbatim; only two pieces are
// reimplemented over soltype rather than type_system:
//
//   - isValueType / isMutableType, the two type predicates the transition checker
//     consults, and
//   - the binding model: a captured variable's identity comes from
//     ValueBinding.VarID and its mutability from the binding's coalesced soltype,
//     not from a type_system.Binding.
//
// checkMutabilityTransition's Rule 1 / Rule 2 / Rule 3 logic is unchanged — it
// talks only to liveness.LivenessInfo and liveness.AliasTracker. The whole pass
// runs inside a function body, keyed off the per-body state on funcCtx (c.fn): at
// module top-level c.fn is nil and every entry point below is a no-op.

// staticConflictName is a sentinel placeholder used in
// MutabilityTransitionError.ConflictingVars to represent a permanent alias from a
// `'static` escape. The escape bits it stands for are populated only at `'static`
// call sites, which M4 G1 does not yet mark, so it stays unset until G2 wires the
// lifetime sort into the escape check. The message renders it specially rather than
// printing the literal sentinel.
const staticConflictName = "<static escape>"

// MutabilityTransitionError is reported when a mutability transition
// (mut→immutable or immutable→mut) is attempted while conflicting live aliases
// exist. It is a liveness-derived diagnostic that self-blames from the
// transition's node — the alias-creating declaration or reassignment — rather than
// resolving an operand through the Prov table.
type MutabilityTransitionError struct {
	// SourceVar is the variable being aliased.
	SourceVar string
	// TargetVar is the variable being created/assigned.
	TargetVar string
	// ConflictingVars lists the names of live aliases that conflict.
	ConflictingVars []string
	// MutToImmutable is true for Rule 1 (mut→immutable), false for Rule 2 (immutable→mut).
	MutToImmutable bool
	// node is the transition site, used for blame (the decl or assignment).
	node ast.Node
}

func (*MutabilityTransitionError) isSolverError()        {}
func (e *MutabilityTransitionError) Span() ast.Span      { return e.node.Span() }
func (e *MutabilityTransitionError) Related() []ast.Span { return nil }
func (e *MutabilityTransitionError) Message() string {
	// Render conflicts. The staticConflictName sentinel is rendered without quotes
	// so the message reads naturally; everything else is a real identifier that gets
	// single-quoted.
	parts := make([]string, len(e.ConflictingVars))
	for i, name := range e.ConflictingVars {
		if name == staticConflictName {
			parts[i] = "a `'static` escape"
		} else {
			parts[i] = "'" + name + "'"
		}
	}
	vars := strings.Join(parts, ", ")
	// When the conflicting variable is the source itself, the message is
	// straightforward. When it's a different variable (an alias), clarify the
	// relationship so the user understands the connection.
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

// isValueType reports whether the type is a primitive or literal type (or a union
// of such types). Value types have copy semantics — assigning them to another
// variable creates an independent copy, so alias tracking is unnecessary. Ported
// from check_transitions.go over soltype: there is no Prune step because the
// coalesced display type the caller passes is already resolved.
func isValueType(t soltype.Type) bool {
	switch p := t.(type) {
	case *soltype.PrimType, *soltype.LitType:
		return true
	case *soltype.UnionType:
		for _, member := range p.Types {
			if !isValueType(member) {
				return false
			}
		}
		return len(p.Types) > 0
	}
	return false
}

// isMutableType reports whether the type is a mutable borrow. In the new type
// system mutability is carried by a RefType wrapper with Mut set, replacing the old
// checker's MutType wrapper.
func isMutableType(t soltype.Type) bool {
	if r, ok := t.(*soltype.RefType); ok {
		return r.Mut
	}
	return false
}

// aliasMutability maps a Go bool to the liveness alias-mutability enum.
func aliasMutability(mut bool) liveness.AliasMutability {
	if mut {
		return liveness.AliasMutable
	}
	return liveness.AliasImmutable
}

// bindingType returns a binding's coalesced display type for the transition
// checker's predicates, or nil for an empty (definition-less) binding.
func bindingType(b ValueBinding) soltype.Type {
	if len(b.Schemes) == 0 {
		return nil
	}
	return schemeType(b.Schemes[0])
}

// checkMutabilityTransition verifies that a mutability transition is safe at the
// given program point, reporting a MutabilityTransitionError when conflicting live
// aliases exist. Ported verbatim from check_transitions.go — the Rule 1 / Rule 2 /
// Rule 3 logic talks only to liveness state on c.fn.
//
// A transition is only dangerous when both sides are live simultaneously after the
// transition point — a mutable alias could mutate the value while an immutable
// alias assumes it is unchanged.
//
//	Rule 1 (mut → immutable): no live mutable aliases may exist after this point,
//	  provided the target (immutable) alias is also live.
//	Rule 2 (immutable → mut): no live immutable aliases may exist after this point,
//	  provided the target (mutable) alias is also live.
//	Rule 3: multiple mutable aliases are always allowed (mut → mut is not a transition).
func (c *checker) checkMutabilityTransition(
	sourceVarID liveness.VarID,
	targetVarID liveness.VarID,
	sourceVarName string,
	targetVarName string,
	sourceMut bool,
	targetMut bool,
	assignRef liveness.StmtRef,
	node ast.Node,
) {
	fn := c.fn
	// Same mutability — no transition (Rule 3 for mut→mut).
	if sourceMut == targetMut {
		return
	}
	if fn == nil || fn.liveness == nil || fn.aliases == nil {
		return
	}
	// If the target alias is dead immediately after the transition, there is no
	// window where both sides are live simultaneously, so it's safe.
	if !fn.liveness.IsLiveAfter(assignRef, targetVarID) {
		return
	}

	// The loop intentionally does NOT skip sourceVarID. The source variable is itself
	// a member of the alias set, and if it is still live after the transition point,
	// it IS a conflicting alias. A variable may belong to multiple alias sets
	// (conditional aliasing), so deduplicate by collecting names into a set.
	conflictingSet := make(map[string]struct{})
	for _, aliasSet := range fn.aliases.GetAliasSets(sourceVarID) {
		// A `'static` escape on the alias set represents a permanent outside
		// reference — no liveness check is meaningful, so it always counts as a live
		// alias of its escaped mutability. These bits are unset until G2 marks
		// `'static` call sites.
		if sourceMut && !targetMut && aliasSet.HasStaticMutAlias {
			conflictingSet[staticConflictName] = struct{}{}
		}
		if !sourceMut && targetMut && aliasSet.HasStaticImmAlias {
			conflictingSet[staticConflictName] = struct{}{}
		}
		for varID, aliasMut := range aliasSet.Members {
			if !fn.liveness.IsLiveAfter(assignRef, varID) {
				continue
			}
			if sourceMut && !targetMut {
				// Rule 1: mut → immutable — error if any live mutable alias exists.
				if aliasMut == liveness.AliasMutable {
					conflictingSet[c.varIDToName(varID)] = struct{}{}
				}
			} else {
				// Rule 2: immutable → mut — error if any live immutable alias exists.
				if aliasMut == liveness.AliasImmutable {
					conflictingSet[c.varIDToName(varID)] = struct{}{}
				}
			}
		}
	}

	if len(conflictingSet) == 0 {
		return
	}

	conflicting := make([]string, 0, len(conflictingSet))
	for name := range conflictingSet {
		conflicting = append(conflicting, name)
	}
	sort.Strings(conflicting)

	c.errs = append(c.errs, &MutabilityTransitionError{
		SourceVar:       sourceVarName,
		TargetVar:       targetVarName,
		ConflictingVars: conflicting,
		MutToImmutable:  sourceMut && !targetMut,
		node:            node,
	})
}

// trackAliasesForVarDecl updates the alias tracker and checks mutability
// transitions for a body-level `val`/`var` declaration with an initializer. M4 G1
// handles the IdentPat case (and closure capture when the initializer is a
// FuncExpr); destructuring patterns are unsupported in the new checker until the
// pattern PRs land, and a non-IdentPat decl produces no binding anyway.
func (c *checker) trackAliasesForVarDecl(scope *Scope, decl *ast.VarDecl, bindingT soltype.Type, enclosingStmt ast.Stmt) {
	if c.fn == nil || c.fn.aliases == nil || decl.Init == nil {
		return
	}
	pat, ok := decl.Pattern.(*ast.IdentPat)
	if !ok {
		return
	}
	c.trackAliasesForIdentPat(pat, bindingT, decl.Init, enclosingStmt, decl)

	// Closure-capture aliasing — when the initializer is a FuncExpr, add the closure
	// variable to each captured variable's alias set. A read-only capture of a
	// mutable variable is a mut→immut transition that must be checked against live
	// mutable aliases.
	if funcExpr, ok := decl.Init.(*ast.FuncExpr); ok && pat.VarID > 0 {
		c.trackCapturedAliases(scope, funcExpr, liveness.VarID(pat.VarID), enclosingStmt, decl)
	}
}

// trackAliasesForIdentPat handles alias tracking for a simple identifier pattern
// binding (`val x = expr`).
func (c *checker) trackAliasesForIdentPat(
	identPat *ast.IdentPat,
	bindingT soltype.Type,
	init ast.Expr,
	enclosingStmt ast.Stmt,
	node ast.Node,
) {
	if identPat.VarID <= 0 {
		return
	}
	targetVarID := liveness.VarID(identPat.VarID)
	targetMut := isMutableType(bindingT)
	aliasMut := aliasMutability(targetMut)

	source := liveness.DetermineAliasSource(init)
	switch source.RootKind() {
	case liveness.AliasSourceVariable:
		sourceVarID := source.UniqueVarIDs()[0]
		c.fn.aliases.AddAlias(targetVarID, sourceVarID, aliasMut)
		if stmtRef, hasRef := c.fn.stmtToRef[enclosingStmt]; hasRef {
			sourceMut := c.isSourceMutable(sourceVarID)
			c.checkMutabilityTransition(
				sourceVarID, targetVarID,
				c.varIDToName(sourceVarID), identPat.Name,
				sourceMut, targetMut, stmtRef, node,
			)
		}
	case liveness.AliasSourceMultiple:
		// Conditional aliasing: the target aliases all possible source variables.
		for _, sourceVarID := range source.UniqueVarIDs() {
			c.fn.aliases.AddAlias(targetVarID, sourceVarID, aliasMut)
		}
		if stmtRef, hasRef := c.fn.stmtToRef[enclosingStmt]; hasRef {
			for _, sourceVarID := range source.UniqueVarIDs() {
				sourceMut := c.isSourceMutable(sourceVarID)
				c.checkMutabilityTransition(
					sourceVarID, targetVarID,
					c.varIDToName(sourceVarID), identPat.Name,
					sourceMut, targetMut, stmtRef, node,
				)
			}
		}
	case liveness.AliasSourceFresh, liveness.AliasSourceUnknown:
		c.fn.aliases.NewValue(targetVarID, aliasMut)
	}
}

// trackCapturedAliases adds the closure variable to the alias sets of each captured
// variable from the enclosing scope, and checks the mutability transition each
// capture induces. The captured variable's identity (its VarID) and mutability come
// from its ValueBinding, the new-checker analogue of type_system.Binding.
func (c *checker) trackCapturedAliases(
	scope *Scope,
	funcExpr *ast.FuncExpr,
	closureVarID liveness.VarID,
	enclosingStmt ast.Stmt,
	node ast.Node,
) {
	if c.fn.aliases == nil {
		return
	}
	captures := liveness.AnalyzeCaptures(funcExpr)
	for _, capture := range captures {
		// Look up the captured variable's binding in the enclosing scope. The scope
		// chain handles shadowing correctly — GetValue returns the innermost binding.
		b, found := scope.GetValue(capture.Name)
		if !found || b.VarID <= 0 {
			continue
		}
		// Primitives and literals have value semantics — reassigning a captured
		// primitive inside a closure can't affect other variables that copied the
		// value, so alias tracking is unnecessary.
		if isValueType(bindingType(b)) {
			continue
		}
		enclosingVarID := liveness.VarID(b.VarID)
		c.fn.aliases.AddAlias(closureVarID, enclosingVarID, aliasMutability(capture.IsMutable))

		if stmtRef, hasRef := c.fn.stmtToRef[enclosingStmt]; hasRef {
			sourceMut := c.isSourceMutable(enclosingVarID)
			c.checkMutabilityTransition(
				enclosingVarID, closureVarID,
				capture.Name, c.varIDToName(closureVarID),
				sourceMut, capture.IsMutable, stmtRef, node,
			)
		}
	}
}

// trackAliasesForAssignment updates the alias tracker and checks mutability
// transitions for a variable reassignment (`b = expr`). Called only after the
// assignment's source/target constraint succeeded, so the types it reads are sound.
func (c *checker) trackAliasesForAssignment(target *ast.IdentExpr, rhs ast.Expr, targetType soltype.Type) {
	if c.fn == nil || c.fn.aliases == nil || target.VarID <= 0 {
		return
	}
	targetVarID := liveness.VarID(target.VarID)
	targetMut := isMutableType(targetType)
	aliasMut := aliasMutability(targetMut)

	source := liveness.DetermineAliasSource(rhs)
	switch source.RootKind() {
	case liveness.AliasSourceVariable:
		sourceVarID := source.UniqueVarIDs()[0]
		if stmtRef, hasRef := c.fn.stmtToRef[c.fn.currentStmt]; hasRef {
			sourceMut := c.isSourceMutable(sourceVarID)
			c.checkMutabilityTransition(
				sourceVarID, targetVarID,
				c.varIDToName(sourceVarID), target.Name,
				sourceMut, targetMut, stmtRef, target,
			)
		}
		c.fn.aliases.Reassign(targetVarID, &sourceVarID, aliasMut)
	case liveness.AliasSourceMultiple:
		// Conditional aliasing: reassign to all sources before checking transitions
		// so alias state stays consistent regardless of whether errors are reported.
		c.fn.aliases.ReassignMulti(targetVarID, source.UniqueVarIDs(), aliasMut)
		if stmtRef, hasRef := c.fn.stmtToRef[c.fn.currentStmt]; hasRef {
			for _, sourceVarID := range source.UniqueVarIDs() {
				sourceMut := c.isSourceMutable(sourceVarID)
				c.checkMutabilityTransition(
					sourceVarID, targetVarID,
					c.varIDToName(sourceVarID), target.Name,
					sourceMut, targetMut, stmtRef, target,
				)
			}
		}
	case liveness.AliasSourceFresh, liveness.AliasSourceUnknown:
		c.fn.aliases.Reassign(targetVarID, nil, aliasMut)
	}
}

// trackAliasesForPropAssignment handles alias tracking for property assignments
// like `obj.prop = value`. When the RHS aliases a variable, the alias sets of the
// object and the RHS source are merged.
func (c *checker) trackAliasesForPropAssignment(lhs ast.Expr, rhs ast.Expr) {
	if c.fn == nil || c.fn.aliases == nil {
		return
	}
	objVarID := rootObjectVarID(lhs)
	if objVarID <= 0 {
		return
	}
	source := liveness.DetermineAliasSource(rhs)
	switch source.RootKind() {
	case liveness.AliasSourceVariable:
		c.fn.aliases.MergeAliasSets(objVarID, source.UniqueVarIDs()[0])
	case liveness.AliasSourceMultiple:
		for _, srcID := range source.UniqueVarIDs() {
			c.fn.aliases.MergeAliasSets(objVarID, srcID)
		}
	case liveness.AliasSourceFresh, liveness.AliasSourceUnknown:
		// No alias relationship to track.
	}
}

// isSourceMutable checks whether a source variable is registered as mutable in its
// alias sets. Returns true if the source holds a mutable reference.
func (c *checker) isSourceMutable(sourceVarID liveness.VarID) bool {
	for _, s := range c.fn.aliases.GetAliasSets(sourceVarID) {
		if m, exists := s.Members[sourceVarID]; exists {
			return m == liveness.AliasMutable
		}
	}
	return false
}

// rootObjectVarID iteratively walks a member/index expression chain (`a.b.c`,
// `a[b][c]`) to find the root object's VarID, returning 0 when the root is not a
// local variable.
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

// varIDToName resolves a VarID back to a variable name for error messages, reading
// the current body's rename-pass mapping.
func (c *checker) varIDToName(id liveness.VarID) string {
	if c.fn != nil && c.fn.varIDNames != nil {
		if name, ok := c.fn.varIDNames[id]; ok {
			return name
		}
	}
	return fmt.Sprintf("var#%d", id)
}

// runLivenessPrePass runs name resolution, CFG construction, and liveness analysis
// on the function body about to be walked, populating the transition-checking state
// on c.fn. Ported from internal/checker/liveness_prepass.go: the rename pass writes
// VarIDs directly onto the body's AST nodes, so DetermineAliasSource and the
// IdentPat/IdentExpr reads downstream see them. paramTypes maps each parameter name
// to its soltype, supplying the mutability the alias seeding records.
//
// Must be called after parameters are bound in scope (so outer-scope names resolve)
// but before the body is walked.
func (c *checker) runLivenessPrePass(scope *Scope, astParams []*ast.Param, paramTypes map[string]soltype.Type, body *ast.Block) {
	// Build outer bindings from the scope chain. Every value binding accessible from
	// the current scope gets a negative VarID so the rename pass can distinguish
	// local from non-local variables.
	outerBindings := collectOuterBindings(scope)

	// Extra param names are bindings present as params but not in astParams (e.g. an
	// implicit `self`). The new checker has no such params yet, so this is normally
	// empty; the logic is kept for parity with the old prepass.
	astParamNames := set.NewSet[string]()
	for _, p := range astParams {
		collectPatternBindingNames(p.Pattern, astParamNames)
	}
	var extraParamNames []string
	for name := range paramTypes {
		if !astParamNames.Contains(name) {
			extraParamNames = append(extraParamNames, name)
		}
	}

	renameResult := liveness.Rename(astParams, *body, outerBindings, extraParamNames...)

	cfg := liveness.BuildCFG(*body)
	livenessInfo := liveness.AnalyzeFunction(cfg)
	stmtToRef := liveness.BuildStmtToRef(cfg)

	// Initialize the alias tracker and seed parameters so aliases from parameters are
	// tracked and mutability transitions involving them are detected.
	aliases := liveness.NewAliasTracker()
	seedParamLeafAliases(astParams, paramTypes, aliases)
	for name, varID := range renameResult.ExtraParamVarIDs {
		mut := liveness.AliasImmutable
		if t, ok := paramTypes[name]; ok && isMutableType(t) {
			mut = liveness.AliasMutable
		}
		aliases.NewValue(varID, mut)
	}

	c.fn.liveness = livenessInfo
	c.fn.aliases = aliases
	c.fn.stmtToRef = stmtToRef
	c.fn.varIDNames = renameResult.VarIDNames
}

// seedParamLeafAliases walks each parameter pattern recursively and seeds the alias
// tracker with one alias set per leaf binding, reading each leaf's mutability from
// paramTypes so transitions involving the leaf are checked correctly.
func seedParamLeafAliases(astParams []*ast.Param, paramTypes map[string]soltype.Type, aliases *liveness.AliasTracker) {
	for _, param := range astParams {
		forEachLeafBinding(param.Pattern, func(name string, varID int) {
			if varID <= 0 {
				return
			}
			mut := liveness.AliasImmutable
			if t, ok := paramTypes[name]; ok && isMutableType(t) {
				mut = liveness.AliasMutable
			}
			aliases.NewValue(liveness.VarID(varID), mut)
		})
	}
}

// collectOuterBindings walks the scope chain and collects all value binding names,
// assigning each a unique negative VarID. Names within each scope are sorted before
// assignment so the resulting IDs are deterministic across runs (Go map iteration
// order is randomized), keeping any VarID-capturing snapshots stable.
func collectOuterBindings(scope *Scope) map[string]liveness.VarID {
	bindings := make(map[string]liveness.VarID)
	nextID := liveness.VarID(-1)

	for s := scope; s != nil; s = s.parent {
		names := make([]string, 0, len(s.values))
		for name := range s.values {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			if _, exists := bindings[name]; !exists {
				bindings[name] = nextID
				nextID--
			}
		}
	}

	return bindings
}

// recordParamVarIDs copies each IdentPat parameter's rename-assigned VarID onto its
// scope binding (M4 G1), so a closure that captures the parameter resolves it to its
// alias set through trackCapturedAliases. Runs after the pre-pass, since the rename
// is what assigns the VarIDs.
func recordParamVarIDs(fnScope *Scope, params []*ast.Param) {
	for _, p := range params {
		ip, ok := p.Pattern.(*ast.IdentPat)
		if !ok || ip.VarID <= 0 {
			continue
		}
		if b, found := fnScope.GetValue(ip.Name); found {
			b.VarID = ip.VarID
			fnScope.defineValue(ip.Name, b)
		}
	}
}

// forEachLeafBinding invokes fn for every identifier name a pattern introduces,
// recursing through destructuring patterns. Ported from internal/checker.
func forEachLeafBinding(pat ast.Pat, fn func(name string, varID int)) {
	if pat == nil {
		return
	}
	switch p := pat.(type) {
	case *ast.IdentPat:
		fn(p.Name, p.VarID)
	case *ast.TuplePat:
		for _, sub := range p.Elems {
			forEachLeafBinding(sub, fn)
		}
	case *ast.ObjectPat:
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjKeyValuePat:
				forEachLeafBinding(e.Value, fn)
			case *ast.ObjShorthandPat:
				if e.Key != nil {
					fn(e.Key.Name, e.VarID)
				}
			case *ast.ObjRestPat:
				forEachLeafBinding(e.Pattern, fn)
			}
		}
	case *ast.RestPat:
		forEachLeafBinding(p.Pattern, fn)
	}
}

// collectPatternBindingNames adds every identifier name introduced by a pattern
// (recursively) to the provided set.
func collectPatternBindingNames(p ast.Pat, into set.Set[string]) {
	forEachLeafBinding(p, func(name string, _ int) {
		into.Add(name)
	})
}
