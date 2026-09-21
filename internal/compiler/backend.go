package compiler

import (
	"context"
	"os"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// Diagnostic is one type error, as everything downstream of the compiler reads it:
// a span and a message. internal/checker's Error and internal/solver's SolverError
// both satisfy it, so an output carries whichever checker ran without its readers
// having to know which one that was.
type Diagnostic interface {
	Span() ast.Span
	Message() string
}

// CheckerEnvVar names the environment variable that picks which type checker the
// compiler's entry points run.
//
// Set it to CheckerSolver to run internal/solver. Any other value, the variable
// being unset included, runs internal/checker, which is the default while the
// solver cutover is in progress. The variable is read on each entry point call, so
// a test can set it for one call with t.Setenv.
const CheckerEnvVar = "ESCALIER_CHECKER"

// CheckerSolver is the CheckerEnvVar value that selects internal/solver.
const CheckerSolver = "solver"

// selectBackend returns the checker CheckerEnvVar names.
func selectBackend() backend {
	if os.Getenv(CheckerEnvVar) == CheckerSolver {
		return solverBackend{}
	}
	return checkerBackend{}
}

// backend is the compiler's view of a type checker. Each entry point resolves one
// through selectBackend and drives it, so the choice is made in one place instead of
// at each construction site.
type backend interface {
	// checkLib infers a parsed lib/ module.
	checkLib(ctx context.Context, module *ast.Module) libResult
	// checkScript infers a parsed bin/ script with no library in scope, so it
	// resolves only what the prelude declares. checkScriptIn routes to it for a
	// package whose lib/ directory holds no files.
	checkScript(ctx context.Context, script *ast.Script) scriptResult
}

// LibScope is the surface a package's lib/ module declared, in whatever form the
// checker that produced it holds. It is the lib/ to bin/ seam: each bin/ script is
// checked as if the library's top-level declarations were already in scope, and the
// emitted script imports the ones it used.
//
// The two implementations wrap what their own checker produced. Neither converts to
// the other's type representation, which is what keeps the cutover from building a
// bridge back into type_system.
type LibScope interface {
	// checkScript infers a parsed bin/ script with this library in scope.
	checkScript(ctx context.Context, script *ast.Script) scriptResult
	// declaresTopLevel reports whether the library declares name at its top level,
	// as a value or as a namespace. CompileScript emits an import for each such name
	// a script mentions.
	declaresTopLevel(name string) bool
}

// libResult is what a backend produced for one lib/ module.
type libResult struct {
	// lib is the surface each of the package's bin/ scripts is checked against. It
	// is nil when the package has no lib/ files.
	lib LibScope
	// depGraph is the graph the run ordered the module's declarations by, which
	// codegen emits the library's JS from.
	depGraph *dep_graph.DepGraph
	// dtsNamespace is the namespace codegen renders the library's .d.ts from. It is
	// nil on the solver path, where rendering a soltype surface is still to come and
	// this cutover builds no bridge from soltype back to type_system.
	dtsNamespace *type_system.Namespace
	// scope and fileScopes are the LSP's view of the module. Both are nil on the
	// solver path, whose scopes have a different type; porting the LSP is a later
	// phase of the cutover.
	scope      *checker.Scope
	fileScopes map[int]*checker.Scope
	// diagnostics are the module's type errors.
	diagnostics []Diagnostic
}

// scriptResult is what a backend produced for one bin/ script.
type scriptResult struct {
	// scope is the LSP's view of the script, nil on the solver path for the reason
	// libResult.scope gives.
	scope *checker.Scope
	// diagnostics are the script's type errors.
	diagnostics []Diagnostic
}

// checkScriptIn infers script with lib in scope, or with the prelude alone when the
// package declared no library. A LibScope carries its own checker, so pairing a
// library surface with the backend that made it needs no check here.
func checkScriptIn(ctx context.Context, b backend, lib LibScope, script *ast.Script) scriptResult {
	if lib == nil {
		return b.checkScript(ctx, script)
	}
	return lib.checkScript(ctx, script)
}

// diagnostics widens a checker's own error slice to the Diagnostic interface. Go
// converts no slice element-wise, so the copy is written out.
func diagnostics[E Diagnostic](errs []E) []Diagnostic {
	out := make([]Diagnostic, len(errs))
	for i, e := range errs {
		out[i] = e
	}
	return out
}
