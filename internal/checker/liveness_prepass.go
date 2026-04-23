package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
)

// runLivenessPrePass runs name resolution, CFG construction, and liveness
// analysis on a function body. It sets up the Context fields needed for
// alias tracking and mutability transition checking during type inference.
//
// This must be called after bindings have been added to the scope (so that
// outer-scope names can be resolved) but before statements are walked for
// type checking.
func (c *Checker) runLivenessPrePass(ctx *Context, astParams []*ast.Param, body *ast.Block) {
	// Build outer bindings from the scope chain. Every value binding
	// accessible from the current scope (excluding local params, which the
	// rename pass handles separately) gets a negative VarID so the rename
	// pass can distinguish local from non-local variables.
	outerBindings := collectOuterBindings(ctx.Scope)

	// Phase 2: Resolve names → VarIDs
	renameResult := liveness.Rename(astParams, *body, outerBindings)

	// Phase 3-4: Build CFG and run backward liveness analysis
	cfg := liveness.BuildCFG(*body)
	livenessInfo := liveness.AnalyzeFunction(cfg)

	// Build StmtToRef lookup
	stmtToRef := liveness.BuildStmtToRef(cfg)

	// Initialize alias tracker
	aliases := liveness.NewAliasTracker()

	// Set context fields
	ctx.Liveness = livenessInfo
	ctx.Aliases = aliases
	ctx.StmtToRef = stmtToRef
	ctx.VarIDNames = renameResult.VarIDNames
}

// collectOuterBindings walks the scope chain and collects all value binding
// names, assigning each a unique negative VarID. These are used by the
// rename pass to distinguish non-local references from unresolved names.
func collectOuterBindings(scope *Scope) map[string]liveness.VarID {
	bindings := make(map[string]liveness.VarID)
	nextID := liveness.VarID(-1)

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
