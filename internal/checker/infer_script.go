package checker

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
)

func (c *Checker) InferScript(ctx Context, m *ast.Script) (scope *Scope, errors []Error) {
	defer recoverTimeout(&errors)
	clear(c.expandCache) // Reset cross-call expansion cache for this inference pass
	clear(c.substCache)  // Reset substitution cache for this inference pass
	clear(c.memberCache) // Reset per-member substitution cache for this inference pass
	ctx = ctx.WithNewScope()
	scope = ctx.Scope

	// Run the liveness pre-pass on top-level script code so that alias
	// tracking and mutability transition checking work the same as inside
	// function bodies.
	scriptBody := &ast.Block{Stmts: m.Stmts, Span: m.Span()}
	c.runLivenessPrePass(&ctx, nil, nil, scriptBody)

	for _, stmt := range m.Stmts {
		c.checkTimeout()
		stmtErrors := c.inferStmt(ctx, stmt)
		errors = slices.Concat(errors, stmtErrors)
	}

	return scope, errors
}
