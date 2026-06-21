package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// InferScript infers a script — a source file whose top-level statements run in
// source order with function-body semantics, the bin/ counterpart to a library
// module. It returns the populated script Scope (a child of the prelude, so
// operators and the stdlib-type placeholders resolve through the parent), the Info
// side table, and any SolverErrors.
//
// A module and a script differ in how their top-level declarations relate.
// InferModule dependency-orders mutually-visible top-level declarations through the
// dep graph. A script is instead one straight-line body: its bindings are linear,
// and each statement sees only the ones before it, exactly as inside a function. So
// liveness, alias tracking, and the `mut` transition rules — which a module skips at
// top level because c.fn is nil there, making every transition entry point a no-op —
// all apply to a script's top-level statements.
//
// It mirrors the old checker's InferScript in internal/checker/infer_script.go:
//
//  1. wrap the script's statements in an ast.Block;
//  2. push a fresh funcCtx so c.fn is non-nil and the transition checker is live;
//  3. run runLivenessPrePass over that block to rename the body's variable nodes and
//     seed the alias/liveness tables;
//  4. walk the statements in source order through inferStmt.
//
// M4 shipped every building block this uses — the pre-pass, the per-body funcCtx, the
// alias tracker, and the transition checker. The only new code is this entry point
// that runs them over a script body. There is no new inference.
//
// The funcCtx carries no async flag, so a top-level `await` reports against the
// script node, matching a non-async function body. inferStmt routes a top-level
// `return` into the funcCtx's returns list, which the script never joins, so the
// return is accepted and discarded — the same no-op the old checker's inferStmt
// applies to a script-level return.
func InferScript(script *ast.Script) (*Scope, *Info, []SolverError) {
	c := newChecker()
	scope := sharedPrelude().Child()

	// A script's statements form one linear body, so give them the same per-body
	// context a function body gets. pushFuncCtx makes c.fn non-nil — the transition
	// checker keys off it, and runLivenessPrePass writes its liveness/alias state onto
	// it — and the prelude child is the outer scope the pre-pass resolves names
	// against. There is no enclosing function to restore, so the returned previous
	// context is discarded.
	scriptBody := &ast.Block{Stmts: script.Stmts, Span: script.Span()}
	c.pushFuncCtx(false, script)
	c.runLivenessPrePass(scope, nil, nil, scriptBody)

	// Walk at level 0, the script body's own level: inferVarDecl types each
	// initializer one level deeper and generalizes back to 0, so a top-level binding
	// is a generalized local exactly like one in a function body walked at its level.
	for _, stmt := range script.Stmts {
		c.inferStmt(scope, 0, stmt)
	}

	return scope, c.info, c.errs
}
