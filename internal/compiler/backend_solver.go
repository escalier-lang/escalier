package compiler

import (
	"context"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/codegen"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/soltype"
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
//     guard, all without a diagnostic. Tracked in #1673.
//   - The emitted .d.ts is rendered from soltype and does not yet match what the
//     checker path writes for the same source. Reconciling the two is tracked in
//     #1676, and #1697 through #1699 are renderer faults it will surface.
//   - The diagnostics include every package the run loaded, so a package that is
//     itself clean still reports the two errors `std:prelude` carries. Tracked in
//     #1664 for the errors themselves and #1696 for the file they are blamed on.
//   - libResult.scope, libResult.fileScopes, and scriptResult.scope are nil, because
//     the LSP reads internal/checker's scope type. Tracked in #1678 through #1682.
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
		dts:         &solverDts{module: result},
		diagnostics: append(dirErrs, diagnostics(result.Errors)...),
		codegenGap:  &codegenGapError{span: moduleSpan(module)},
	}
}

// checkScript infers script against the prelude alone.
func (solverBackend) checkScript(_ context.Context, script *ast.Script) scriptResult {
	dir, dirErrs := solverStdlibDir(scriptSpan(script))
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
		"a method reference, and an `if val` guard are emitted wrongly, and the .d.ts does not " +
		"yet match the one the old checker writes"
}

// solverDts renders the library's .d.ts from the module run, which holds the scope
// the declarations landed in and the registries their members are keyed in.
type solverDts struct {
	module *solver.ModuleResult
}

func (d *solverDts) buildDefinitions(b *codegen.Builder, depGraph *dep_graph.DepGraph) *codegen.Module {
	root := &solverDtsScope{module: d.module}
	return b.BuildDefinitionsFromSol(depGraph, root, solver.PreludeKeyPrefix())
}

// solverDtsScope reads the module's own top level, where a declaration a namespace
// block holds is keyed under its qualified name.
type solverDtsScope struct {
	module *solver.ModuleResult
}

func (s *solverDtsScope) ValueType(name string) (soltype.Type, bool) {
	binding, ok := s.module.Scope.OwnValue(name)
	if !ok {
		return nil, false
	}
	t := binding.DisplayType()
	return t, t != nil
}

func (s *solverDtsScope) DeclaredType(name string) (soltype.Type, []*soltype.TypeParam, bool) {
	binding, ok := s.module.Scope.OwnType(name)
	if !ok {
		return nil, nil, false
	}
	return declaredTypeFromSol(s.module, binding.Type)
}

func (s *solverDtsScope) Namespace(name string) (codegen.SolNamespace, bool) {
	ns, ok := s.module.Scope.OwnNamespace(name)
	if !ok {
		return nil, false
	}
	return &solverDtsNamespace{module: s.module, ns: ns}, true
}

// solverDtsNamespace reads one namespace's members, which it keys under their local
// names rather than the qualified ones the module scope uses.
type solverDtsNamespace struct {
	module *solver.ModuleResult
	ns     *solver.Namespace
}

func (s *solverDtsNamespace) ValueType(name string) (soltype.Type, bool) {
	binding, ok := s.ns.Values[localName(name)]
	if !ok {
		return nil, false
	}
	t := binding.DisplayType()
	return t, t != nil
}

func (s *solverDtsNamespace) DeclaredType(name string) (soltype.Type, []*soltype.TypeParam, bool) {
	binding, ok := s.ns.Types[localName(name)]
	if !ok {
		return nil, nil, false
	}
	return declaredTypeFromSol(s.module, binding.Type)
}

func (s *solverDtsNamespace) Namespace(name string) (codegen.SolNamespace, bool) {
	nested, ok := s.ns.Nested[localName(name)]
	if !ok {
		return nil, false
	}
	return &solverDtsNamespace{module: s.module, ns: nested}, true
}

// declaredTypeFromSol resolves what a type binding stands for. A class, enum,
// interface, or alias binds a handle carrying the declaration's qualified name, with
// the members registered under that name, so the handle is followed to them. Any other
// type already holds what it stands for and is returned as it is.
func declaredTypeFromSol(module *solver.ModuleResult, t soltype.Type) (soltype.Type, []*soltype.TypeParam, bool) {
	var qualifiedName string
	switch handle := t.(type) {
	case *soltype.ClassType:
		qualifiedName = handle.Name
	case *soltype.AliasType:
		qualifiedName = handle.Name
	default:
		return t, nil, t != nil
	}
	return module.TypeBody(qualifiedName)
}

// localName is the last segment of a qualified name, which is how a namespace keys its
// own members. A bare name has one segment and comes back unchanged.
func localName(name string) string {
	if dot := strings.LastIndex(name, "."); dot != -1 {
		return name[dot+1:]
	}
	return name
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
