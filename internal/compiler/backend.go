package compiler

import (
	"context"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/codegen"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/solver"
)

// Diagnostic is one type error, as everything downstream of the compiler reads it:
// a span and a message. internal/solver's SolverError satisfies it, as does the
// diagnostic the compiler reports when it cannot find the standard library.
type Diagnostic interface {
	Span() ast.Span
	Message() string
}

// newBackend returns the checker every entry point runs, configured by config.
func newBackend(config PackageConfig) backend {
	return solverBackend{options: solver.Options{NoAmbientNames: config.NoAmbientNames}}
}

// backend is the compiler's view of a type checker. Each entry point builds one through
// newBackend and drives it, so its configuration is set in one place instead of at each
// construction site.
type backend interface {
	// checkLib infers a parsed lib/ module.
	checkLib(ctx context.Context, module *ast.Module) libResult
	// checkScript infers a parsed bin/ script with no library in scope, so it
	// resolves only what the prelude declares. checkScriptIn routes to it for a
	// package whose lib/ directory holds no files.
	checkScript(ctx context.Context, script *ast.Script) scriptResult
}

// LibScope is the surface a package's lib/ module declared. It is the lib/ to bin/
// seam: each bin/ script is checked as if the library's top-level declarations were
// already in scope, and the emitted script imports the ones it used.
type LibScope interface {
	// checkScript infers a parsed bin/ script with this library in scope.
	checkScript(ctx context.Context, script *ast.Script) scriptResult
	// declaresTopLevel reports whether the library declares name at its top level,
	// as a value or as a namespace. CompileScript emits an import for each such name
	// a script mentions.
	declaresTopLevel(name string) bool
}

// dtsEmitter renders one library module's .d.ts from the type surface a run produced.
type dtsEmitter interface {
	// buildDefinitions emits the declarations of the module depGraph orders, so they
	// appear in the order inference typed them.
	buildDefinitions(b *codegen.Builder, depGraph *dep_graph.DepGraph) *codegen.Module
}

// libResult is what a backend produced for one lib/ module.
type libResult struct {
	// lib is the surface each of the package's bin/ scripts is checked against. It
	// is nil when the package has no lib/ files.
	lib LibScope
	// depGraph is the graph the run ordered the module's declarations by, which
	// codegen emits the library's JS from.
	depGraph *dep_graph.DepGraph
	// dts renders the library's .d.ts from the type surface this run produced. It is nil
	// when the run produced no surface to render.
	dts dtsEmitter
	// jsTypes answers what JavaScript emission asks about the types this run inferred.
	jsTypes codegen.JSTypes
	// info holds the run's per-node records for the module.
	info *solver.Info
	// scope and fileScopes are the run's module scope and per-file import scopes.
	scope      *solver.Scope
	fileScopes map[int]*solver.Scope
	// diagnostics are the module's type errors.
	diagnostics []Diagnostic
}

// scriptResult is what a backend produced for one bin/ script.
type scriptResult struct {
	// jsTypes is libResult.jsTypes for this script.
	jsTypes codegen.JSTypes
	// info is libResult.info for this script.
	info *solver.Info
	// scope is the run's scope for the script.
	scope *solver.Scope
	// diagnostics are the script's type errors.
	diagnostics []Diagnostic
}

// checkScriptIn infers script with lib in scope, or with the prelude alone when the
// package declared no library. A LibScope carries the run that produced it, so a script
// checked against one continues that run rather than starting one from b.
func checkScriptIn(ctx context.Context, b backend, lib LibScope, script *ast.Script) scriptResult {
	if lib == nil {
		return b.checkScript(ctx, script)
	}
	return lib.checkScript(ctx, script)
}

// diagnostics widens the solver's error slice to the Diagnostic interface. Go converts
// no slice element-wise, so the copy is written out.
func diagnostics[E Diagnostic](errs []E) []Diagnostic {
	out := make([]Diagnostic, len(errs))
	for i, e := range errs {
		out[i] = e
	}
	return out
}
