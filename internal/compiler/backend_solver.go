package compiler

import (
	"context"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/solver"
	"github.com/escalier-lang/escalier/internal/stdlibdir"
)

// solverBackend runs internal/solver. CheckerEnvVar selects it; nothing reaches it
// by default while the cutover is in progress.
//
// The path is a seam, not a finished compiler. Four things it does differently from
// the checker path are each a later phase of the cutover, and this is the list to
// read before trusting anything it produces:
//
//   - The emitted JavaScript can be wrong where codegen reads a node's inferred type,
//     which only internal/checker stamps onto the tree. A constructor call loses its
//     `new`, a method reference loses its `.bind`, and an `if val` loses its null
//     guard, all without a diagnostic.
//   - libResult.dtsNamespace is nil, so a package emits no .d.ts. Rendering a soltype
//     surface is its own phase, and this cutover builds no bridge from soltype back
//     to type_system.
//   - The diagnostics include every package the run loaded, so a package that is
//     itself clean still reports the two errors `std:prelude` carries.
//   - libResult.scope, libResult.fileScopes, and scriptResult.scope are nil, because
//     the LSP reads internal/checker's scope type.
//
// internal/solver takes no context, so each method names the parameter `_` to say the
// caller's deadline bounds nothing here.
type solverBackend struct{}

// checkLib infers module against the standard library tree on disk. The run resolves
// each `import "std:*"` through that tree, and the result carries the dep graph the
// walk ordered the module's declarations by.
func (solverBackend) checkLib(_ context.Context, module *ast.Module) libResult {
	dir, dirErrs := solverStdlibDir(moduleSpan(module))
	result := solver.InferModuleAgainstStdlib(module, dir)
	return libResult{
		lib:         &solverLibScope{module: result},
		depGraph:    result.DepGraph,
		diagnostics: append(dirErrs, diagnostics(result.Errors)...),
		codegenGap:  &codegenGapError{span: moduleSpan(module)},
	}
}

// checkScript infers script against the prelude alone.
func (solverBackend) checkScript(_ context.Context, script *ast.Script) scriptResult {
	dir, dirErrs := solverStdlibDir(script.Span())
	_, _, errs := solver.InferScript(script, solver.StdlibSource(dir))
	return scriptResult{
		diagnostics: append(dirErrs, diagnostics(errs)...),
		codegenGap:  &codegenGapError{span: scriptSpan(script)},
	}
}

// codegenGapError reports that this path's emitted output is not yet trustworthy, so
// a caller reads it as a diagnostic rather than discovering it from output that looks
// finished. The two gaps it names are the first two in this file's type doc.
type codegenGapError struct {
	span ast.Span
}

func (e *codegenGapError) Span() ast.Span { return e.span }

func (e *codegenGapError) Message() string {
	return CheckerEnvVar + "=" + CheckerSolver + " does not yet emit correct output for this file: " +
		"codegen reads types internal/checker stamps onto the tree, so a constructor call, " +
		"a method reference, and an `if val` guard are emitted wrongly, and no .d.ts is written"
}

// solverLibScope is the library surface internal/solver produces: the module run
// itself, since a script checked against it carries that run on.
type solverLibScope struct {
	module *solver.ModuleResult
}

// checkScript infers script against the library module's scope, so the script reads
// the library's top-level declarations without importing them.
func (l *solverLibScope) checkScript(_ context.Context, script *ast.Script) scriptResult {
	_, _, errs := solver.InferScriptInLib(script, l.module)
	return scriptResult{
		diagnostics: diagnostics(errs),
		codegenGap:  &codegenGapError{span: scriptSpan(script)},
	}
}

// declaresTopLevel asks the module scope for a declaration of its own under name.
func (l *solverLibScope) declaresTopLevel(name string) bool {
	return l.module.DeclaresTopLevel(name)
}

// solverStdlibDir resolves the directory holding the standard library's `.esc`
// files, blaming span for a tree it cannot find. An unresolvable tree returns "",
// which resolves no package, and the run then reports the prelude classes as missing.
// The diagnostic returned here is what says the cause is the tree rather than the
// code being checked.
func solverStdlibDir(span ast.Span) (string, []Diagnostic) {
	dir, err := stdlibdir.StdlibDir("")
	if err != nil {
		return "", []Diagnostic{&stdlibDirError{reason: err.Error(), span: span}}
	}
	return dir, nil
}

// scriptSpan returns a span naming the script's file, for a diagnostic about the
// whole file rather than about anything written in it.
func scriptSpan(script *ast.Script) ast.Span {
	return ast.Span{SourceID: script.Span().SourceID}
}

// moduleSpan returns a span in the module's first file, for a diagnostic about the
// module as a whole rather than about anything written in it. A module with no files
// has no source to name, so it carries -1, the id the error printers treat as "no
// source". A zero span would name source id 0, which is some other file.
func moduleSpan(module *ast.Module) ast.Span {
	if len(module.Files) == 0 {
		return ast.Span{SourceID: -1}
	}
	return ast.Span{SourceID: module.Files[0].SourceID}
}

// stdlibDirError reports that the standard library tree could not be found.
type stdlibDirError struct {
	reason string
	span   ast.Span
}

func (e *stdlibDirError) Span() ast.Span { return e.span }

func (e *stdlibDirError) Message() string {
	return "cannot find the standard library: " + e.reason
}
