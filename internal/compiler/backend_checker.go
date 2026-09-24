package compiler

import (
	"context"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// checkerBackend runs internal/checker. It is what every entry point uses unless
// CheckerEnvVar selects the solver. Its results leave codegenGap nil, since codegen
// reads the types this checker stamps onto the tree and the namespace it produces,
// so it drives both emitters.
type checkerBackend struct{}

// checkLib infers module through InferModule rather than the lower-level
// InferDepGraph, so each file's imports, `import "std:*"` included, are bound before
// any declaration is checked. InferModule returns the dep graph it built, which
// codegen emits from.
func (checkerBackend) checkLib(ctx context.Context, module *ast.Module) libResult {
	c := checker.NewChecker(ctx)
	inferCtx := checker.Context{
		// A child of the prelude, so the module's bindings do not land in it.
		Scope:      checker.Prelude(c).WithNewScope(),
		IsAsync:    false,
		IsPatMatch: false,
	}
	depGraph, typeErrors := c.InferModule(inferCtx, module)

	return libResult{
		lib:          &checkerLibScope{ns: inferCtx.Scope.Namespace},
		depGraph:     depGraph,
		dtsNamespace: inferCtx.Scope.Namespace,
		scope:        inferCtx.Scope,
		fileScopes:   c.FileScopes,
		diagnostics:  diagnostics(typeErrors),
	}
}

// checkScript infers script against the prelude alone.
func (checkerBackend) checkScript(ctx context.Context, script *ast.Script) scriptResult {
	c := checker.NewChecker(ctx)
	return inferScriptInScope(c, checker.Prelude(c), script)
}

// checkerLibScope is the library surface internal/checker produces: the namespace
// the module's scope accumulated.
type checkerLibScope struct {
	ns *type_system.Namespace
}

// checkScript infers script with the library namespace inserted between the prelude
// and the script's own scope, so the script reads the library's exports without an
// import.
func (l *checkerLibScope) checkScript(ctx context.Context, script *ast.Script) scriptResult {
	c := checker.NewChecker(ctx)
	return inferScriptInScope(c, checker.Prelude(c).WithNewScopeAndNamespace(l.ns), script)
}

// declaresTopLevel reads the namespace's own two maps. A name bound in an enclosing
// scope is not the library's, so neither map is walked past.
func (l *checkerLibScope) declaresTopLevel(name string) bool {
	if _, ok := l.ns.Values[name]; ok {
		return true
	}
	_, ok := l.ns.GetNamespace(name)
	return ok
}

// inferScriptInScope walks script in scope, the step both of this backend's script
// paths share.
func inferScriptInScope(c *checker.Checker, scope *checker.Scope, script *ast.Script) scriptResult {
	inferCtx := checker.Context{
		Scope:      scope,
		IsAsync:    false,
		IsPatMatch: false,
	}
	scriptScope, typeErrors := c.InferScript(inferCtx, script)
	return scriptResult{
		scope:       scriptScope,
		diagnostics: diagnostics(typeErrors),
	}
}
