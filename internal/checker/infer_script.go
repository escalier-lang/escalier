package checker

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
)

func (c *Checker) InferScript(ctx Context, m *ast.Script) (*Scope, []Error) {
	clear(c.expandCache) // Reset cross-call expansion cache for this inference pass
	clear(c.substCache)  // Reset substitution cache for this inference pass
	clear(c.memberCache) // Reset per-member substitution cache for this inference pass
	errors := []Error{}
	ctx = ctx.WithNewScope()

	for _, stmt := range m.Stmts {
		if timeoutErrors := c.checkTimeout(); timeoutErrors != nil {
			return ctx.Scope, slices.Concat(errors, timeoutErrors)
		}
		stmtErrors := c.inferStmt(ctx, stmt)
		errors = slices.Concat(errors, stmtErrors)
	}

	return ctx.Scope, errors
}
