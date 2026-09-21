package solver

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
)

// InferScript infers a script. A script is a source file whose top-level statements
// run in source order with function-body semantics, the bin/ counterpart to a
// library module. It returns the populated script Scope, the Info side table, and any
// SolverErrors. The returned scope is a child of the run's prelude scope, so the
// prelude package's exports resolve through the parent and the operator table
// through its parent in turn.
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
// M4 shipped every building block this uses: the pre-pass, the per-body funcCtx, the
// alias tracker, and the transition checker. The only new code is this entry point
// that runs them over a script body. There is no new inference.
//
// The pushed funcCtx carries no async flag and a nil node. A top-level `await` is
// rejected the same way it is at module top level. There is no enclosing function to
// mark `async`, because a script has none. inferStmt routes a top-level `return` into
// the funcCtx's returns list. The script never joins that list, so the return is
// accepted and discarded. The old checker's inferStmt applies the same no-op to a
// script-level return.
//
// source supplies `std:prelude` the way it does for a module, since a script's rules
// name the same `Array` and `Promise` a module's do.
func InferScript(script *ast.Script, source ModuleSource) (*Scope, *Info, []SolverError) {
	c := newChecker()
	c.source = source
	return c.inferScriptIn(c.preludeScope().Child(), script)
}

// InferScriptInLib infers script against the scope a library module's run produced,
// so the script reads that module's top-level declarations without importing them.
// It returns the same three results InferScript does, with the diagnostics covering
// the script alone rather than repeating the library's.
//
// This is the bin/ to lib/ seam. A package's lib/ files are one module and each of
// its bin/ files is a script checked as if the library's declarations were already
// in scope. The script scope is a child of lib.Scope, so a name the script does not
// declare itself resolves to the library's binding, and then to the prelude through
// lib.Scope's own parent.
//
// The script carries on the library's run rather than starting a fresh one. A class,
// enum, or alias the library declares resolves to a handle carrying a name, and the
// definition that name stands for lives in the run's Context. A second run would hold
// none of those definitions, so a script could name the library's `Point` but could
// read no member off it. Sharing the Context is what makes a library type usable from
// a script. It also numbers the two runs' inference variables apart and loads each
// imported package once. forScript builds the checker that does this.
//
// Two scripts checked against one library share that Context as well, so a constraint
// one script puts on a library binding's inference variable is visible to the next.
// The old checker has the same property: it hands each bin/ script the one
// type_system.Namespace the library produced.
//
// lib must be a result InferModuleWithSource or InferModuleAgainstStdlib returned.
// A script with no library to check against goes through InferScript instead, which
// parents it to the prelude directly.
func InferScriptInLib(script *ast.Script, lib *ModuleResult) (*Scope, *Info, []SolverError) {
	c := lib.run.forScript(scriptPkgURI(script))
	return c.inferScriptIn(lib.Scope.Child(), script)
}

// scriptPkgURI is the prefix the nominal registries key one script's declarations
// under. Every script checked against one library shares that run's registries, so
// each needs a prefix of its own for two scripts declaring the same class name to
// hold two definitions. The source id names the file, which is what distinguishes
// one bin/ script from another within a package.
func scriptPkgURI(script *ast.Script) string {
	return "script:" + strconv.Itoa(script.Span().SourceID)
}

// inferScriptIn walks script's statements into scope, the body both script entry
// points share. scope is the freshly created scope the script's own bindings land
// in, already parented to whatever the script resolves free names through.
func (c *checker) inferScriptIn(scope *Scope, script *ast.Script) (*Scope, *Info, []SolverError) {
	// A script's statements form one linear body, so give them the same per-body
	// context a function body gets. pushFuncCtx makes c.fn non-nil. The transition
	// checker keys off c.fn, and runLivenessPrePass writes its liveness and alias state
	// onto it. The node is nil because a script has no enclosing function. scope's
	// parent chain is what the pre-pass resolves outer names against. There is no
	// enclosing context to restore, so the returned previous one is discarded.
	scriptBody := &ast.Block{Stmts: script.Stmts, Span: script.Span()}
	c.pushFuncCtx(false, nil, 0)
	c.runLivenessPrePass(scope, nil, nil, scriptBody)

	// Walk the body through inferBlock, the same source-order statement walker a
	// function body uses, at level 0. inferVarDecl types each initializer one level
	// deeper and generalizes back to 0, so a top-level binding is a generalized local
	// exactly like one in a function body walked at its level. inferBlock's tail value
	// and divergence flag are discarded, just as inferFunc discards them. A script,
	// like a function body, produces no value from its last statement.
	c.inferBlock(scope, 0, scriptBody)
	// A script body runs with function-body semantics, so the move engine applies
	// here too. With the whole body walked, replay the recorded reads against the
	// consumed lattice to report use-after-move.
	c.checkUseAfterMoves()
	// Every class the script declares is inferred, so each superclass edge and body is
	// final. Check the members each subclass redeclares against the ones they override.
	c.checkQueuedInheritedMembers()
	// Every function expression the script wrote has been typed, so each one's return type is
	// final. Report each function whose return type no finite value inhabits.
	c.checkCanReturn()

	return scope, c.info, c.errs
}
