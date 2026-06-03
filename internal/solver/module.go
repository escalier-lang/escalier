package solver

import "github.com/escalier-lang/escalier/internal/ast"

// InferModule infers every top-level declaration in a single parsed module and
// returns the populated module Scope (a child of the prelude, so operators and
// the stdlib-type placeholders resolve through the parent), the Info side table,
// and any SolverErrors.
//
// PR-2 walks declarations in SOURCE ORDER — the order they appear within each
// namespace — with no dep_graph SCC ordering yet. A forward reference (a decl
// that uses a name defined later in the source) therefore fails with
// UnknownIdentifierError; PR-5 replaces this loop with dep_graph SCC ordering so
// out-of-order and recursive decls resolve. Source order is sufficient for
// PR-2's literal/identifier-initializer bar.
func InferModule(module *ast.Module) (*Scope, *Info, []SolverError) {
	c := newChecker()
	scope := NewPrelude().Child()
	c.inferModule(scope, 0, module)
	return scope, c.info, c.errs
}

// inferModule iterates the module's namespaces (in key order) and, within each,
// its declarations in source order, typing each through inferDecl. The module
// Scope is mutated in place as bindings are introduced, so a decl sees every
// earlier decl's binding (the basis for the forward-reference limitation above).
func (c *checker) inferModule(scope *Scope, lvl int, module *ast.Module) {
	module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, d := range ns.Decls {
			c.inferDecl(scope, lvl, d)
		}
		return true
	})
}
