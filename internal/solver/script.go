package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// InferScript infers a script. A script is a source file whose top-level statements
// run in source order with function-body semantics, the bin/ counterpart to a
// library module. It returns the populated script Scope, the Info side table, and any
// SolverErrors. The returned scope is a child of the prelude, so operators and the
// stdlib-type placeholders resolve through the parent.
//
// A module and a script differ in how their top-level declarations relate. InferModule
// dependency-orders mutually-visible top-level declarations through the dep graph. A
// script is instead one straight-line body. Its bindings are linear, and each
// statement sees only the ones before it, exactly as inside a function. So liveness,
// alias tracking, and the `mut` transition rules all apply to a script's top-level
// statements. A module skips those at top level, where c.fn is nil and every
// transition entry point is a no-op.
//
// It mirrors the old checker's InferScript in internal/checker/infer_script.go:
//
//  1. wrap the script's statements in an ast.Block;
//  2. push a fresh funcCtx so c.fn is non-nil and the transition checker is live;
//  3. run runLivenessPrePass over that block to rename the body's variable nodes and
//     seed the alias/liveness tables;
//  4. walk the block in source order through inferBlock.
//
// This entry point reuses the pre-pass, the per-body funcCtx, the alias tracker, and
// the transition checker, running them over a script body. It adds no new inference.
//
// The pushed funcCtx carries no async flag and a nil node. A top-level `await` is
// rejected the same way it is at module top level. There is no enclosing function to
// mark `async`, because a script has none. inferStmt routes a top-level `return` into
// the funcCtx's returns list. The script never joins that list, so the return is
// accepted and discarded. The old checker's inferStmt applies the same no-op to a
// script-level return.
func InferScript(script *ast.Script) (*Scope, *Info, []SolverError) {
	c := newChecker()
	scope := sharedPrelude().Child()

	// A script's statements form one linear body, so give them the same per-body
	// context a function body gets. pushFuncCtx makes c.fn non-nil. The transition
	// checker keys off c.fn, and runLivenessPrePass writes its liveness and alias state
	// onto it. The node is nil because a script has no enclosing function. The prelude
	// child is the outer scope the pre-pass resolves names against. There is no
	// enclosing context to restore, so the returned previous one is discarded.
	scriptBody := &ast.Block{Stmts: script.Stmts, Span: script.Span()}
	c.pushFuncCtx(false, nil)
	c.runLivenessPrePass(scope, nil, nil, scriptBody)

	// Walk the body through inferBlock, the same source-order statement walker a
	// function body uses, at level 0. inferVarDecl types each initializer one level
	// deeper and generalizes back to 0, so a top-level binding is a generalized local
	// exactly like one in a function body walked at its level. inferBlock's tail value
	// and divergence flag are discarded, just as inferFunc discards them. A script,
	// like a function body, produces no value from its last statement.
	c.inferBlock(scope, 0, scriptBody)

	return scope, c.info, c.errs
}
