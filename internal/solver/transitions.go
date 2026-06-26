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
// machinery in internal/liveness is reused verbatim. Only two pieces are reimplemented
// over soltype rather than type_system:
//
//   - isValueType / isMutableType, the two type predicates the transition checker
//     consults, and
//   - the binding model: a captured variable's identity comes from
//     ValueBinding.VarID and its mutability from the binding's coalesced soltype,
//     not from a type_system.Binding.
//
// checkMutabilityTransition's Rule 1 / Rule 2 / Rule 3 logic is unchanged. It talks
// only to liveness.LivenessInfo and liveness.AliasTracker. The whole pass runs inside a
// function body, keyed off the per-body state on funcCtx. At module top-level c.fn is
// nil, so every entry point below is a no-op. That is correct for a module, whose
// top-level declarations are dependency-ordered rather than a linear body. A script is
// different: its top-level statements run in source order with function-body semantics.
// So when script inference lands it must give the script body the same per-body liveness
// context a function gets, by running runLivenessPrePass over the script statements
// under a funcCtx, or these checks stay silently skipped there. The old checker's
// InferScript ran the pre-pass over the whole script body for this reason.

// staticConflictName is a sentinel placeholder used in
// MutabilityTransitionError.ConflictingVars to represent a permanent alias from a
// `'static` escape. M4 G2 populates it by querying the lifetime sort. A member of the
// source's alias set whose borrow lifetime D3 forced `<: 'static` is the permanent
// outside reference this sentinel stands for. The message renders it specially rather
// than printing the literal sentinel.
const staticConflictName = "<static escape>"

// MutabilityTransitionError is reported when a mutability transition is attempted
// while conflicting live aliases exist. The transition is either mut→immutable or
// immutable→mut. It is a liveness-derived diagnostic. It self-blames from the
// transition's node, the alias-creating declaration or reassignment, rather than
// resolving an operand through the Prov table.
type MutabilityTransitionError struct {
	// SourceVar is the variable being aliased.
	SourceVar string
	// TargetVar is the variable being created/assigned.
	TargetVar string
	// ConflictingVars lists the names of live aliases that conflict.
	ConflictingVars []string
	// MutToImmutable records the transition direction: true for Rule 1, the
	// mut→immutable case, and false for Rule 2, the immutable→mut case.
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

// isValueType reports whether the type is a primitive or literal type, or a union of
// such types. Value types have copy semantics. Assigning one to another variable
// creates an independent copy, so alias tracking is unnecessary.
//
// Ported from check_transitions.go, which first calls type_system.Prune. There is no
// equivalent here. The only caller, trackCapturedAliases, passes a binding's coalesced
// display type, which carries no inference-variable indirection to prune. If such a
// type ever surfaced as a bare *soltype.TypeVarType, it falls through to false and is
// classified as a reference, so it is conservatively alias-tracked. That is sound.
// Mis-seeing a value type as a reference can only add a spurious alias edge, never
// drop a real transition, so it over-reports rather than under-reports.
func isValueType(t soltype.Type) bool {
	switch p := t.(type) {
	case *soltype.PrimType, *soltype.LitType:
		return true
	case *soltype.UnionType:
		if p.Inexact {
			// An inexact union has an open tail of unknown type, so the
			// classification depends on members the union does not name. The
			// conservative answer is "not a value type". Over-reporting an
			// alias edge is sound; under-reporting one is not.
			return false
		}
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

// borrowEscapedToStatic reports, for the variable's recorded type, whether it
// carries a borrow forced to 'static at each mutability — escapedMut for a mutable
// escaped borrow, escapedImm for an immutable one. A borrow escapes when it is stored
// where it outlives the function. D3's constrainEscape then pins its lifetime
// `<: 'static`, so the value has a permanent outside alias of that borrow's mutability.
//
// The whole recorded type is walked, not just its top-level RefType (PR 5). A borrow
// nested in a field or tuple element — for example the `&'static mut {…}` field of an
// object stored into a global — is seen, closing the nested-escape gap that made a
// move past such a borrow unsound. A borrow reachable only through a usage-inferred
// TypeVarType is also seen (#787): the walk descends into a type variable's bounds,
// so an escape hidden behind a branch-join variable such as `sink = if c { p } else
// { … }`, whose source is the join variable rather than a bare RefType, is no longer
// missed. Both mutabilities are reported because a value can carry both a mutable and
// an immutable escaped borrow in different positions, and Rule 1 and Rule 2 each
// conflict with one of them. An owned value, a borrow with an unforced lifetime, or an
// unrecorded variable returns both false.
func (c *checker) borrowEscapedToStatic(varID liveness.VarID) (escapedMut, escapedImm bool) {
	if c.fn == nil || c.fn.varIDTypes == nil {
		return false, false
	}
	t, ok := c.fn.varIDTypes[varID]
	if !ok || t == nil {
		return false, false
	}
	v := &escapeDetectVisitor{}
	t.Accept(v, soltype.Positive)
	return v.foundMut, v.foundImm
}

// escapeDetectVisitor records whether the walked type carries any borrow forced to
// 'static, split by the borrow's mutability. It rewrites nothing; EnterType inspects
// each RefType and the shared visitor carries the descent through inner carriers,
// object properties, and tuple elements.
//
// The shared visitor treats a TypeVarType as a leaf and does not descend into its
// bounds (its bounds are a side graph, not tree children). So EnterType walks a type
// variable's bounds itself, resolving a borrow reachable only through a usage-inferred
// variable (#787). The bounds relevant to the current polarity are followed —
// LowerBounds in Positive position, where the variable stands for what flowed into it
// — so an escaped borrow joined into a branch variable is seen. seen guards the
// pointer-keyed cycles a bounds graph can hold.
type escapeDetectVisitor struct {
	foundMut bool
	foundImm bool
	seen     map[*soltype.TypeVarType]bool
}

func (v *escapeDetectVisitor) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.RefType:
		if lifetimeEscapedToStatic(t.Lt) {
			if t.Mut {
				v.foundMut = true
			} else {
				v.foundImm = true
			}
		}
	case *soltype.TypeVarType:
		if v.seen == nil {
			v.seen = map[*soltype.TypeVarType]bool{}
		}
		if v.seen[t] {
			return soltype.EnterResult{}
		}
		v.seen[t] = true
		for _, bound := range t.BoundsAt(pol) {
			bound.Accept(v, pol)
		}
	}
	return soltype.EnterResult{}
}

func (*escapeDetectVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// lifetimeEscapedToStatic reports whether a borrow's lifetime has been forced to
// outlive 'static — the escape constraint `v <: 'static`, which adds 'static as an
// UPPER bound. A literal *StaticLifetime escapes outright. A lower-bound 'static does
// not: it can arise from a join member and does not mean the borrow escaped, so the
// display-time forcedToStatic, which also matches a lower bound, would over-report.
func lifetimeEscapedToStatic(lt soltype.Lifetime) bool {
	switch lt := lt.(type) {
	case *soltype.StaticLifetime:
		return true
	case *soltype.LifetimeVar:
		return soltype.ContainsLifetime(lt.UpperBounds, soltype.Static)
	}
	return false
}

// recordVarIDType records a tracked variable's soltype into the G2 escape bridge,
// guarding the nil map at module top-level where no pre-pass ran.
func (c *checker) recordVarIDType(varID liveness.VarID, t soltype.Type) {
	if c.fn == nil || c.fn.varIDTypes == nil || varID <= 0 {
		return
	}
	c.fn.varIDTypes[varID] = t
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
// aliases exist. Ported verbatim from check_transitions.go. The Rule 1 / Rule 2 /
// Rule 3 logic talks only to liveness state on c.fn.
//
// A transition is only dangerous when both sides are live simultaneously after the
// transition point. A mutable alias could then mutate the value while an immutable
// alias assumes it is unchanged.
//
//	Rule 1 (mut → immutable): no live mutable aliases may exist after this point,
//	  provided the target (immutable) alias is also live.
//	Rule 2 (immutable → mut): no live immutable aliases may exist after this point,
//	  provided the target (mutable) alias is also live.
//	Rule 3: multiple mutable aliases are always allowed (mut → mut is not a transition).
//
// targetAlwaysLive marks a target that outlives the function, the permanent
// module-level slot checkGlobalWriteTransition passes. Such a target has no liveness
// window, so the dead-target early return below is skipped for it.
func (c *checker) checkMutabilityTransition(
	sourceVarID liveness.VarID,
	targetVarID liveness.VarID,
	sourceVarName string,
	targetVarName string,
	sourceMut bool,
	targetMut bool,
	targetAlwaysLive bool,
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
	// window where both sides are live simultaneously, so it's safe. A permanent
	// target has no window to check, so this is skipped for it.
	if !targetAlwaysLive && !fn.liveness.IsLiveAfter(assignRef, targetVarID) {
		return
	}

	// The loop intentionally does NOT skip sourceVarID. The source variable is itself
	// a member of the alias set, and if it is still live after the transition point,
	// it IS a conflicting alias. A variable may belong to multiple alias sets
	// (conditional aliasing), so deduplicate by collecting names into a set.
	conflictingSet := set.NewSet[string]()
	for _, aliasSet := range fn.aliases.GetAliasSets(sourceVarID) {
		for varID, aliasMut := range aliasSet.Members {
			// A borrow on this member forced to 'static is a permanent outside
			// reference. It outlives the function, so no liveness check is meaningful
			// and it always counts as a live alias of its escaped mutability.
			//
			// The conflict test is escMut == sourceMut, compared against the SOURCE, not
			// the target. Past the early return sourceMut != targetMut holds, so
			// escMut == sourceMut means the escaped alias carries the source's old
			// mutability, which is the opposite of the target view being created. That is
			// exactly the alias that conflicts with the new view. Under Rule 1, mut to
			// immutable, the new view is immutable and a permanent MUTABLE escape can
			// change it under us. Under Rule 2, immutable to mut, the new view is mutable
			// and can mutate what a permanent IMMUTABLE escape assumes is frozen.
			//
			// This mirrors the live-alias loop below, which looks for aliasMut == Mutable
			// under Rule 1 and aliasMut == Immutable under Rule 2, i.e. aliasMut ==
			// sourceMut. The escape check runs that same predicate for a permanent alias
			// rather than a live local one. Checked before the liveness skip so a member
			// that is locally dead but has escaped still counts. A value can carry an
			// escaped borrow of each mutability, so select the one matching sourceMut:
			// Rule 1's source is mutable and conflicts with a permanent MUTABLE escape,
			// Rule 2's immutable source with a permanent IMMUTABLE escape.
			escMut, escImm := c.borrowEscapedToStatic(varID)
			if (sourceMut && escMut) || (!sourceMut && escImm) {
				conflictingSet.Add(staticConflictName)
			}
			if !fn.liveness.IsLiveAfter(assignRef, varID) {
				continue
			}
			if sourceMut && !targetMut {
				// Rule 1: mut → immutable — error if any live mutable alias exists.
				if aliasMut == liveness.AliasMutable {
					conflictingSet.Add(c.varIDToName(varID))
				}
			} else {
				// Rule 2: immutable → mut — error if any live immutable alias exists.
				if aliasMut == liveness.AliasImmutable {
					conflictingSet.Add(c.varIDToName(varID))
				}
			}
		}
	}

	if conflictingSet.Len() == 0 {
		return
	}

	conflicting := conflictingSet.ToSlice()
	sort.Strings(conflicting)

	c.errs = append(c.errs, &MutabilityTransitionError{
		SourceVar:       sourceVarName,
		TargetVar:       targetVarName,
		ConflictingVars: conflicting,
		MutToImmutable:  sourceMut && !targetMut,
		node:            node,
	})
}

// trackAliasesForVarDecl records the alias a body-level `val`/`var` with an
// initializer creates, then checks its mutability transition. It covers an IdentPat
// binding. It also covers closure capture when the initializer is a FuncExpr.
// Destructuring patterns are unsupported in the new checker until the pattern PRs
// land, and a non-IdentPat decl produces no binding anyway.
func (c *checker) trackAliasesForVarDecl(scope *Scope, decl *ast.VarDecl, bindingT soltype.Type, enclosingStmt ast.Stmt) {
	if c.fn == nil || c.fn.aliases == nil || decl.Init == nil {
		return
	}
	pat, ok := decl.Pattern.(*ast.IdentPat)
	if !ok {
		return
	}
	c.trackAliasesForIdentPat(pat, bindingT, decl.Init, enclosingStmt, decl)

	// Closure-capture aliasing. When the initializer is a FuncExpr, add the closure
	// variable to each captured variable's alias set. A read-only capture of a
	// mutable variable is a mut→immut transition that must be checked against live
	// mutable aliases.
	if funcExpr, ok := decl.Init.(*ast.FuncExpr); ok && pat.VarID > 0 {
		c.trackCapturedAliases(scope, funcExpr, liveness.VarID(pat.VarID), enclosingStmt, decl)
	}
}

// checkTransitionsAgainst checks the mutability transition that aliasing each source
// in sourceIDs to (targetVarID, targetName, targetMut) induces, at enclosingStmt's CFG
// point. It is the shared core of the decl and reassignment paths. The single-source
// case is just the one-element instance of this loop, so neither path open-codes its
// own copy. A no-op when the statement has no StmtRef.
func (c *checker) checkTransitionsAgainst(
	sourceIDs []liveness.VarID,
	targetVarID liveness.VarID,
	targetName string,
	targetMut bool,
	enclosingStmt ast.Stmt,
	node ast.Node,
) {
	stmtRef, hasRef := c.fn.stmtToRef[enclosingStmt]
	if !hasRef {
		return
	}
	for _, sourceVarID := range sourceIDs {
		c.checkMutabilityTransition(
			sourceVarID, targetVarID,
			c.varIDToName(sourceVarID), targetName,
			c.isSourceMutable(sourceVarID), targetMut, false, stmtRef, node,
		)
	}
}

// trackAliasesForIdentPat records the alias a simple identifier binding `val x = expr`
// creates and checks its mutability transition.
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
	c.recordVarIDType(targetVarID, bindingT)

	source := liveness.DetermineAliasSource(init)
	switch source.RootKind() {
	case liveness.AliasSourceVariable, liveness.AliasSourceMultiple:
		// The target aliases every source, whether one variable or several under
		// conditional aliasing. Add each alias edge, then check the transition against
		// each source.
		sourceIDs := source.UniqueVarIDs()
		for _, sourceVarID := range sourceIDs {
			c.fn.aliases.AddAlias(targetVarID, sourceVarID, aliasMut)
		}
		c.checkTransitionsAgainst(sourceIDs, targetVarID, identPat.Name, targetMut, enclosingStmt, node)
	case liveness.AliasSourceFresh, liveness.AliasSourceUnknown:
		c.fn.aliases.NewValue(targetVarID, aliasMut)
	}
}

// trackCapturedAliases adds the closure variable to the alias sets of each captured
// variable from the enclosing scope, and checks the mutability transition each
// capture induces. The captured variable's VarID and mutability both come from its
// ValueBinding, the new-checker analogue of type_system.Binding.
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
		// Look up the captured variable's binding in the enclosing scope. GetValue
		// walks the scope chain and returns the innermost binding, so shadowing
		// resolves to the name in scope at the capture site.
		b, found := scope.GetValue(capture.Name)
		if !found || b.VarID <= 0 {
			continue
		}
		// Cross-frame guard (M4 G1). A binding stores the VarID of the body that
		// declared it. Module-wide numbering keeps those ids distinct across bodies, so
		// they no longer collide, but a captured variable from an outer frame still does
		// not appear in THIS frame's varIDNames or liveness tables. Track a capture only
		// when its binding originated in this frame. That holds exactly when the current
		// body's rename assigned b.VarID to this name. A capture from an outer frame
		// reaches past its immediate enclosing function, so its liveness lives in another
		// body's tables and cannot be queried here. Skipping is sound. It misses that
		// transition rather than inventing one. The real cross-frame fix rides G2's
		// lifetime-escape bridge; see the G2 note in m4-implementation-plan. This is
		// unreachable today because a captured mutable is a borrow, which cannot yet be
		// aliased into a local.
		if name, ok := c.fn.varIDNames[liveness.VarID(b.VarID)]; !ok || name != capture.Name {
			continue
		}
		// Primitives and literals have value semantics. Reassigning a captured
		// primitive inside a closure can't affect other variables that copied the
		// value, so alias tracking is unnecessary.
		if isValueType(bindingType(b)) {
			continue
		}
		enclosingVarID := liveness.VarID(b.VarID)
		c.fn.aliases.AddAlias(closureVarID, enclosingVarID, aliasMutability(capture.IsMutable))
		// The capture creates a (closure ← enclosing var) alias; check the transition it
		// induces. The cross-frame guard above guarantees enclosingVarID resolves in this
		// frame, so the helper's varIDToName(enclosingVarID) is capture.Name.
		c.checkTransitionsAgainst(
			[]liveness.VarID{enclosingVarID}, closureVarID,
			c.varIDToName(closureVarID), capture.IsMutable, enclosingStmt, node,
		)
	}
}

// trackAliasesForAssignment updates the alias tracker and checks mutability
// transitions for a variable reassignment (`b = expr`). Called only after the
// assignment's source/target constraint succeeded, so the types it reads are sound.
//
// enclosingStmt is the statement that contains the assignment, captured by
// inferAssign BEFORE it walks the RHS. It must NOT be re-read from c.fn.currentStmt
// here. Walking an RHS that itself contains statements re-enters inferStmt and
// overwrites currentStmt with an inner-branch statement, so by this point currentStmt
// no longer names the assignment's statement. A `b = if c { … } else { … }`, a match,
// or a block expression all trigger this. Reading the clobbered currentStmt would
// resolve a valid-but-wrong CFG StmtRef and run the liveness query at the wrong
// program point.
func (c *checker) trackAliasesForAssignment(target *ast.IdentExpr, rhs ast.Expr, targetType soltype.Type, enclosingStmt ast.Stmt) {
	if c.fn == nil || c.fn.aliases == nil || target.VarID <= 0 {
		return
	}
	targetVarID := liveness.VarID(target.VarID)
	targetMut := isMutableType(targetType)
	aliasMut := aliasMutability(targetMut)
	c.recordVarIDType(targetVarID, targetType)

	source := liveness.DetermineAliasSource(rhs)
	switch source.RootKind() {
	case liveness.AliasSourceVariable:
		// Check the transition before reassigning. The single-source reassign rewires
		// the target's membership, so checking first reads the pre-reassign alias state.
		sourceVarID := source.UniqueVarIDs()[0]
		c.checkTransitionsAgainst([]liveness.VarID{sourceVarID}, targetVarID, target.Name, targetMut, enclosingStmt, target)
		c.fn.aliases.Reassign(targetVarID, &sourceVarID, aliasMut)
	case liveness.AliasSourceMultiple:
		// Conditional aliasing: reassign to all sources BEFORE checking transitions so
		// alias state stays consistent regardless of whether errors are reported.
		sourceIDs := source.UniqueVarIDs()
		c.fn.aliases.ReassignMulti(targetVarID, sourceIDs, aliasMut)
		c.checkTransitionsAgainst(sourceIDs, targetVarID, target.Name, targetMut, enclosingStmt, target)
	case liveness.AliasSourceFresh, liveness.AliasSourceUnknown:
		c.fn.aliases.Reassign(targetVarID, nil, aliasMut)
	}
}

// checkGlobalWriteTransition checks the mutability transition a store into a
// module-level binding induces. The target binding is not a local, so the reassignment
// alias path above skips it, yet it outlives the function and aliases whatever the
// source holds. Storing a mutable borrow into an immutable global, or an immutable one
// into a mutable global, is therefore a mut↔immutable transition against a permanent,
// always-live target. It conflicts when the source stays live at the conflicting
// mutability after the store, WITHIN this body. slotType is the global binding's own
// type, whose mutability is the target side of the transition.
//
// This is an in-body check only. It does NOT make a store into a global sound: a borrow
// parameter stored into an immutable global escapes to 'static, but the caller may
// retain its own live mutable alias to the same value and mutate it after the call, so
// the immutable global observes a mutation. Catching that needs the call site to enforce
// the 'static borrow as unique, which is the borrow checker's job (#618 lifetime
// enforcement, #762 move semantics), not this pass.
//
// Run BEFORE constrainEscape so the source's own about-to-happen escape is not
// double-counted by the G2 escape query as a prior permanent alias.
func (c *checker) checkGlobalWriteTransition(target *ast.IdentExpr, rhs ast.Expr, slotType soltype.Type, enclosingStmt ast.Stmt) {
	if c.fn == nil || c.fn.aliases == nil {
		return
	}
	stmtRef, hasRef := c.fn.stmtToRef[enclosingStmt]
	if !hasRef {
		return
	}
	source := liveness.DetermineAliasSource(rhs)
	switch source.RootKind() {
	case liveness.AliasSourceVariable, liveness.AliasSourceMultiple:
		targetMut := isMutableType(slotType)
		for _, sourceVarID := range source.UniqueVarIDs() {
			c.checkMutabilityTransition(
				sourceVarID, 0,
				c.varIDToName(sourceVarID), target.Name,
				c.isSourceMutable(sourceVarID), targetMut, true, stmtRef, target,
			)
		}
	}
	// A fresh value has no aliasable source, so storing it into a global creates no
	// cross-binding mutability hazard.
}

// trackAliasesForPropAssignment merges alias sets for a property assignment
// `obj.prop = value`. When the RHS aliases a variable, the object's alias set and the
// RHS source's alias set are merged.
func (c *checker) trackAliasesForPropAssignment(lhs ast.Expr, rhs ast.Expr) {
	if c.fn == nil || c.fn.aliases == nil {
		return
	}
	objVarID := liveness.VarID(ast.RootObjectVarID(lhs))
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
// alias sets. It returns true if the source holds a mutable reference, and false when
// the source is absent from the tracker. Every decl and param seeds its source first
// through NewValue or AddAlias, so an unregistered source means no mutable alias is on
// record, which conservatively reads as immutable.
func (c *checker) isSourceMutable(sourceVarID liveness.VarID) bool {
	for _, s := range c.fn.aliases.GetAliasSets(sourceVarID) {
		if m, exists := s.Members[sourceVarID]; exists {
			return m == liveness.AliasMutable
		}
	}
	return false
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
// on c.fn. Ported from internal/checker/liveness_prepass.go. The rename pass writes
// VarIDs directly onto the body's AST nodes, so DetermineAliasSource and the
// downstream IdentPat/IdentExpr reads see them. paramTypes maps each parameter name to
// its soltype, the mutability the alias seeding records.
//
// Must be called after parameters are bound in scope (so outer-scope names resolve)
// but before the body is walked.
func (c *checker) runLivenessPrePass(scope *Scope, astParams []*ast.Param, paramTypes map[string]soltype.Type, body *ast.Block) {
	// Build outer bindings from the scope chain. Every value binding accessible from
	// the current scope gets a negative VarID so the rename pass can distinguish
	// local from non-local variables.
	outerBindings := c.collectOuterBindings(scope)

	// Allocate this body's local VarIDs from the module-wide counter so they are
	// unique across every body in the run, then advance it past them. UniqueVarCount
	// is the number of locals this body defined, so the next body starts just after.
	//
	// Every parameter is an explicit entry in astParams, including a method's `self`
	// receiver — `self` is written in the signature and is only implicit at the call
	// site. So the rename pass sees every parameter directly. RenameFrom's
	// extraParamNames hook, for a binding injected into a body without a signature
	// param, is unused here.
	firstID := liveness.VarID(c.varIDCounter)
	renameResult := liveness.RenameFrom(astParams, *body, outerBindings, firstID)
	c.varIDCounter += renameResult.UniqueVarCount

	cfg := liveness.BuildCFG(*body)
	livenessInfo := liveness.AnalyzeFunction(cfg)
	stmtToRef := liveness.BuildStmtToRef(cfg)

	// Initialize the alias tracker and seed each parameter leaf so aliases from
	// parameters are tracked and mutability transitions involving them are detected.
	// varIDTypes is the VarID → soltype bridge the `'static`-escape query reads in G2.
	// seedParamLeafAliases records each param leaf's type into it alongside the alias
	// mutability it derives from the same type.
	aliases := liveness.NewAliasTracker()
	varIDTypes := map[liveness.VarID]soltype.Type{}
	seedParamLeafAliases(astParams, paramTypes, aliases, varIDTypes)

	c.fn.liveness = livenessInfo
	c.fn.aliases = aliases
	c.fn.stmtToRef = stmtToRef
	c.fn.varIDNames = renameResult.VarIDNames
	c.fn.varIDTypes = varIDTypes
	// Retain the CFG and a fresh consume-site collector for the move engine (PR 5).
	// The branch-merged consumed lattice (liveness.AnalyzeMoves) joins over these
	// same blocks. PR 6 records consume sites into moveSites while walking the body.
	c.fn.cfg = cfg
	c.fn.moveSites = map[liveness.StmtRef]set.Set[liveness.VarID]{}
}

// seedParamLeafAliases walks each parameter pattern recursively and seeds the alias
// tracker with one alias set per leaf binding, reading each leaf's mutability from
// paramTypes so transitions involving the leaf are checked correctly. It also records
// each leaf's type into varIDTypes, the bridge the G2 `'static`-escape query reads.
//
// paramTypes is keyed by leaf name. An IdentPat parameter contributes the parameter
// itself, and a destructuring param (M4 E1) contributes one entry per leaf via
// bindPattern, so the lookup below resolves every leaf.
//
// KNOWN LIMITATION: a destructured leaf's type is a fresh inference variable at
// pre-pass time, before constraints are coalesced, so isMutableType sees a bare var
// rather than a RefType and a `mut` leaf still seeds AliasImmutable. Seeding a
// destructured `mut` leaf accurately needs the leaf's resolved type, which is not
// available until after the body walk. This is deferred with the rest of the
// destructuring mutability work.
func seedParamLeafAliases(astParams []*ast.Param, paramTypes map[string]soltype.Type, aliases *liveness.AliasTracker, varIDTypes map[liveness.VarID]soltype.Type) {
	for _, param := range astParams {
		ast.ForEachLeafBinding(param.Pattern, func(name string, varID int) {
			if varID <= 0 {
				return
			}
			mut := liveness.AliasImmutable
			if t, ok := paramTypes[name]; ok {
				if isMutableType(t) {
					mut = liveness.AliasMutable
				}
				// Record the leaf's type for the G2 escape query. An IdentPat param
				// binding is mono, so the body's reads instantiate this same pointer
				// and a lifetime it later escapes to 'static is visible through it. A
				// destructured leaf records a fresh var, which is not a RefType, so the
				// escape query returns escaped=false for it, matching the KNOWN
				// LIMITATION above.
				varIDTypes[liveness.VarID(varID)] = t
			}
			aliases.NewValue(liveness.VarID(varID), mut)
		})
	}
}

// collectOuterBindings walks the scope chain and collects all value binding names,
// assigning each a unique negative VarID so the rename pass can tell a non-local
// reference from a local. Names within each scope are sorted before assignment so the
// ids are deterministic across runs (Go map iteration order is randomized).
//
// The root of every chain is the shared, immutable prelude, which this would re-walk
// and re-sort on every function body's pre-pass. preludeOuterNames memoizes the
// prelude's sorted names so only the mutable scopes above it are re-collected each
// time. The old checker left this re-walk as a TODO(Phase 15.1). The module scope
// above the prelude is NOT cached. It grows as later SCC components bind, so a cached
// snapshot would be stale for a body inferred after it.
func (c *checker) collectOuterBindings(scope *Scope) map[string]liveness.VarID {
	bindings := make(map[string]liveness.VarID)
	nextID := liveness.VarID(-1)
	add := func(names []string) {
		for _, name := range names {
			if _, exists := bindings[name]; !exists {
				bindings[name] = nextID
				nextID--
			}
		}
	}

	for s := scope; s != nil; s = s.parent {
		if s.parent == nil {
			add(c.preludeOuterNames(s)) // immutable prelude root: collected once, reused
		} else {
			add(sortedValueNames(s))
		}
	}

	return bindings
}

// preludeOuterNames returns the prelude root scope's sorted value names, computing
// them once per root and caching the result on the checker. The prelude is built once
// and never mutated during a run, so the cache is always fresh.
func (c *checker) preludeOuterNames(root *Scope) []string {
	if c.preludeNamesRoot != root {
		c.preludeNames = sortedValueNames(root)
		c.preludeNamesRoot = root
	}
	return c.preludeNames
}

// sortedValueNames returns a scope's own value binding names in sorted order.
func sortedValueNames(s *Scope) []string {
	names := make([]string, 0, len(s.values))
	for name := range s.values {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// recordParamVarIDs copies each parameter leaf's rename-assigned VarID onto its
// scope binding (M4 G1), so a closure that captures the parameter resolves it to
// its alias set through trackCapturedAliases. It walks every leaf, so a
// destructuring parameter's leaves (M4 E1) are covered alongside a plain IdentPat
// parameter. Runs after the pre-pass, since the rename is what assigns the VarIDs.
func recordParamVarIDs(fnScope *Scope, params []*ast.Param) {
	for _, p := range params {
		ast.ForEachLeafBinding(p.Pattern, func(name string, varID int) {
			if varID <= 0 {
				return
			}
			if b, found := fnScope.GetValue(name); found {
				b.VarID = varID
				fnScope.defineValue(name, b)
			}
		})
	}
}

// trackDestructureLeaves wires a body-level destructuring `val`/`var`'s leaves
// (M4 E1) into the liveness machinery, the IdentPat path's per-leaf analogue. For
// each leaf it copies the rename-assigned VarID onto the binding — so a closure
// capturing the leaf resolves its alias set — and registers the leaf as a tracked
// value carrying its mutability, so a later mutation or reassignment through it is
// checked. A no-op outside a function body.
//
// Unlike trackAliasesForVarDecl it does not add an alias edge from each leaf to
// the initializer. A destructured leaf reads a PROJECTION of the initializer. For
// example `x` from `val {x} = p` is `p.x`, not `p`. That projection is not a
// variable the liveness graph names, so the leaf is registered as its own value.
// A leaf whose resolved type is a `mut` borrow still seeds AliasImmutable here,
// since the leaf's type is an unresolved inference variable at this point. That
// timing gap is the destructuring-mutability work deferred with the rest.
func (c *checker) trackDestructureLeaves(scope *Scope, pat ast.Pat) {
	ast.ForEachLeafBinding(pat, func(name string, varID int) {
		if varID <= 0 {
			return
		}
		b, found := scope.GetValue(name)
		if !found {
			return
		}
		b.VarID = varID
		scope.defineValue(name, b)
		if c.fn != nil && c.fn.aliases != nil {
			c.fn.aliases.NewValue(liveness.VarID(varID), aliasMutability(isMutableType(bindingType(b))))
		}
	})
}
