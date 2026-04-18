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

	for _, stmt := range m.Stmts {
		c.checkTimeout()
		stmtErrors := c.inferStmt(ctx, stmt)
		errors = slices.Concat(errors, stmtErrors)
	}

	return scope, errors
}
