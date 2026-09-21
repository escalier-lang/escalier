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
// the checker path are each a later phase of the cutover rather than an oversight,
// and this is the list to read before trusting anything it produces:
//
//   - The emitted JavaScript can be wrong where codegen reads a node's inferred
//     type, which only internal/checker stamps onto the tree. A constructor call
//     loses its `new`, a method reference loses its `.bind`, and an `if val` loses
//     its null guard, all without a diagnostic. Emitting JavaScript from the
//     solver's own results is the next phase.
//   - libResult.dtsNamespace is nil, so a package emits no .d.ts. Rendering a
//     soltype surface is a phase of its own, and this cutover builds no bridge from
//     soltype back to type_system.
//   - The diagnostics include every package the run loaded, the standard library's
//     own among them, so a package that is itself clean can still report the two
//     errors `std:prelude` currently carries. Clearing the library's own
//     diagnostics is the first phase of the cutover.
//   - libResult.scope, libResult.fileScopes, and scriptResult.scope are nil, because
//     the LSP reads internal/checker's scope type. Porting the LSP comes after the
//     compiler's default flips.
//
// internal/solver takes no context, so the deadline each entry point sets bounds
// nothing here. Each method names the parameter `_` to say so at the signature.
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
	}
}

// checkScript infers script against the prelude alone.
func (solverBackend) checkScript(_ context.Context, script *ast.Script) scriptResult {
	dir, dirErrs := solverStdlibDir(script.Span())
	_, _, errs := solver.InferScript(script, solver.StdlibSource(dir))
	return scriptResult{diagnostics: append(dirErrs, diagnostics(errs)...)}
}

// codegenGap reports that this path's emitted output is not yet trustworthy, so a
// caller reads it as a diagnostic rather than discovering it from output that looks
// finished. The two gaps it names are the first two in this file's type doc.
func (solverBackend) codegenGap(span ast.Span) Diagnostic {
	return &codegenGapError{span: span}
}

// codegenGapError reports that the checker driving codegen cannot yet supply
// everything the emitters read.
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
	return scriptResult{diagnostics: diagnostics(errs)}
}

// declaresTopLevel asks the module scope for a declaration of its own under name.
func (l *solverLibScope) declaresTopLevel(name string) bool {
	return l.module.DeclaresTopLevel(name)
}

// solverStdlibDir resolves the directory holding the standard library's `.esc`
// files, blaming span for a tree it cannot find.
//
// An unresolvable tree returns "", which resolves no package. The run then reports
// the prelude classes its own rules name as missing, so the diagnostic returned here
// is what says the cause is the tree rather than the code being checked.
func solverStdlibDir(span ast.Span) (string, []Diagnostic) {
	dir, err := stdlibdir.StdlibDir("")
	if err != nil {
		return "", []Diagnostic{&stdlibDirError{reason: err.Error(), span: span}}
	}
	return dir, nil
}

// moduleSpan returns a span in the module's first file, for a diagnostic about the
// module as a whole rather than about anything written in it. A module with no files
// has no source to name, and the zero span's source id of 0 is then wrong in a way
// no reader can act on, so it carries -1, the id the error printers treat as "no
// source".
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
