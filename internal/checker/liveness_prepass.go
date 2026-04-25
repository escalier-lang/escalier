package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// runLivenessPrePass runs name resolution, CFG construction, and liveness
// analysis on a function body. It sets up the Context fields needed for
// alias tracking and mutability transition checking during type inference.
//
// This must be called after bindings have been added to the scope (so that
// outer-scope names can be resolved) but before statements are walked for
// type checking.
func (c *Checker) runLivenessPrePass(ctx *Context, astParams []*ast.Param, paramBindings map[string]*type_system.Binding, body *ast.Block) {
	// Build outer bindings from the scope chain. Every value binding
	// accessible from the current scope (excluding local params, which the
	// rename pass handles separately) gets a negative VarID so the rename
	// pass can distinguish local from non-local variables.
	outerBindings := collectOuterBindings(ctx.Scope)

	// Compute extra param names: bindings in paramBindings that are not in
	// astParams (e.g. implicit 'self' in methods). These need positive VarIDs
	// so their uses in the body are tracked as local variables.
	//
	// Walk destructuring patterns recursively so that every leaf name bound
	// by an explicit pattern is recognized — otherwise the rename pass would
	// also re-define those names from paramBindings, leaving the
	// pattern-leaf's IdentPat.VarID stale relative to what the body's
	// IdentExpr.VarID resolves to.
	astParamNames := set.NewSet[string]()
	for _, p := range astParams {
		collectPatternBindingNames(p.Pattern, astParamNames)
	}
	var extraParamNames []string
	for name := range paramBindings {
		if !astParamNames.Contains(name) {
			extraParamNames = append(extraParamNames, name)
		}
	}

	// Resolve names → VarIDs
	renameResult := liveness.Rename(astParams, *body, outerBindings, extraParamNames...)

	// Build CFG and run backward liveness analysis
	cfg := liveness.BuildCFG(*body)
	livenessInfo := liveness.AnalyzeFunction(cfg)

	// Build StmtToRef lookup
	stmtToRef := liveness.BuildStmtToRef(cfg)

	// Initialize alias tracker and seed parameters so that aliases from
	// parameters are tracked and mutability transitions are detected.
	aliases := liveness.NewAliasTracker()
	for _, param := range astParams {
		if identPat, ok := param.Pattern.(*ast.IdentPat); ok && identPat.VarID > 0 {
			varID := liveness.VarID(identPat.VarID)
			// Determine mutability from the parameter's type binding in scope.
			mut := liveness.AliasImmutable
			if binding := ctx.Scope.Namespace.Values[identPat.Name]; binding != nil {
				if isMutableType(binding.Type) {
					mut = liveness.AliasMutable
				}
			}
			aliases.NewValue(varID, mut)
		}
	}
	// Seed extra params (e.g. 'self') into the alias tracker.
	for name, varID := range renameResult.ExtraParamVarIDs {
		mut := liveness.AliasImmutable
		if binding := paramBindings[name]; binding != nil {
			if isMutableType(binding.Type) {
				mut = liveness.AliasMutable
			}
		}
		aliases.NewValue(varID, mut)
	}

	// Set context fields
	ctx.Liveness = livenessInfo
	ctx.Aliases = aliases
	ctx.StmtToRef = stmtToRef
	ctx.VarIDNames = renameResult.VarIDNames
}

// collectOuterBindings walks the scope chain and collects all value binding
// names, assigning each a unique negative VarID. These are used by the
// rename pass to distinguish non-local references from unresolved names.
//
// TODO(Phase 15.1): This re-walks the entire scope chain (including the
// prelude) on every call. Cache the flattened bindings at the parent scope
// level so that only the current scope's bindings need to be added each time.
func collectOuterBindings(scope *Scope) map[string]liveness.VarID {
	bindings := make(map[string]liveness.VarID)
	nextID := liveness.VarID(-1)

	// Note: the loop starts from the current scope, which includes parameter
	// bindings that were already added. This is intentional — the rename pass's
	// define() overwrites the negative VarID with a positive one when it
	// processes the parameter, so the shadowing is handled correctly.
	for s := scope; s != nil; s = s.Parent {
		for name := range s.Namespace.Values {
			if _, exists := bindings[name]; !exists {
				bindings[name] = nextID
				nextID--
			}
		}
	}

	return bindings
}
