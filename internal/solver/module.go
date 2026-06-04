package solver

import "github.com/escalier-lang/escalier/internal/ast"

// InferModule infers every top-level declaration in a single parsed module and
// returns the populated module Scope (a child of the prelude, so operators and
// the stdlib-type placeholders resolve through the parent), the Info side table,
// and any SolverErrors.
//
// PR-2 walks namespaces in namespace-key order and, within each, declarations in
// source order — with no dep_graph SCC ordering yet. So within a single
// namespace a forward reference (a decl that uses a name defined later) fails
// with UnknownIdentifierError, and across namespaces the visit order follows the
// sorted namespace key rather than source/file order. PR-5 replaces this loop
// with dep_graph SCC ordering, which makes both cases order-independent; the
// source-order loop is sufficient only for PR-2's single-namespace
// literal/identifier-initializer bar.
func InferModule(module *ast.Module) (*Scope, *Info, []SolverError) {
	c := newChecker()
	scope := sharedPrelude().Child()
	c.inferModule(scope, 0, module)
	return scope, c.info, c.errs
}

// inferModule iterates the module's namespaces (in sorted key order, per
// btree.Map.Scan) and, within each, its declarations in source order, typing
// each through inferDecl. The module Scope is mutated in place as bindings are
// introduced, so a decl sees every earlier decl's binding (the basis for the
// forward-reference limitation above).
func (c *checker) inferModule(scope *Scope, lvl int, module *ast.Module) {
	module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, d := range ns.Decls {
			c.inferDecl(scope, lvl, d)
		}
		return true
	})
}
