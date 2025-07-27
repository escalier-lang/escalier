package compiler

import (
	"context"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/codegen"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/parser"
)

type CompilerOutput struct {
	ParseErrors []*parser.Error
	TypeErrors  []checker.Error
	JS          string
	SourceMap   string
	DTS         string
}

func Compile(source *ast.Source) CompilerOutput {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	inMod, parseErrors := p.ParseScript()

	c := checker.NewChecker()
	inferCtx := checker.Context{
		Scope:      checker.Prelude(),
		IsAsync:    false,
		IsPatMatch: false,
	}
	_, typeErrors := c.InferScript(inferCtx, inMod)

	// namespace := scope.Namespace

	if len(parseErrors) > 0 {
		return CompilerOutput{
			JS:          "",
			DTS:         "",
			SourceMap:   "",
			ParseErrors: parseErrors,
			TypeErrors:  typeErrors,
		}
	}

	builder := &codegen.Builder{}
	jsMod := builder.BuildScript(inMod)
	var decls []ast.Decl
	for _, d := range inMod.Stmts {
		if ds, ok := d.(*ast.DeclStmt); ok {
			decls = append(decls, ds.Decl)
		}
	}

	// TODO: Create a separate version of BuildDefinitions that works with just
	// the decls slice instead of the dep_graph.
	// dtsMod := builder.BuildDefinitions(decls, namespace)

	printer := codegen.NewPrinter()
	jsOutput := printer.PrintModule(jsMod)

	jsFile := "./index.js"
	sourceMap := codegen.GenerateSourceMap([]*ast.Source{source}, jsMod, jsFile)

	outmap := "./index.js.map"
	jsOutput += "//# sourceMappingURL=" + outmap + "\n"

	printer = codegen.NewPrinter()
	// dtsOutput := printer.PrintModule(dtsMod)
	dtsOutput := ""

	return CompilerOutput{
		ParseErrors: parseErrors,
		TypeErrors:  typeErrors,
		JS:          jsOutput,
		SourceMap:   sourceMap,
		DTS:         dtsOutput,
	}
}

func CompileLib(sources []*ast.Source) CompilerOutput {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	inMod, parseErrors := parser.ParseLibFiles(ctx, sources)
	depGraph := dep_graph.BuildDepGraph(inMod)

	// Make sure there are no cycles in the dependency graph before proceeding.
	cycles := depGraph.FindCycles()
	if len(cycles) > 0 {
		return CompilerOutput{
			JS:          "",
			DTS:         "",
			SourceMap:   "",
			ParseErrors: nil,
			TypeErrors:  []checker.Error{checker.CyclicDependencyError{}},
		}
	}

	c := checker.NewChecker()
	inferCtx := checker.Context{
		Scope:      checker.Prelude(),
		IsAsync:    false,
		IsPatMatch: false,
	}
	moduleNS, typeErrors := c.InferDepGraph(inferCtx, depGraph)

	if len(parseErrors) > 0 {
		return CompilerOutput{
			JS:          "",
			DTS:         "",
			SourceMap:   "",
			ParseErrors: parseErrors,
			TypeErrors:  typeErrors,
		}
	}

	builder := &codegen.Builder{}
	jsMod := builder.BuildTopLevelDecls(depGraph)
	dtsMod := builder.BuildDefinitions(depGraph, moduleNS)

	printer := codegen.NewPrinter()
	jsOutput := printer.PrintModule(jsMod)

	jsFile := "./index.js"
	sourceMap := codegen.GenerateSourceMap(sources, jsMod, jsFile)

	outmap := "./index.js.map"
	jsOutput += "//# sourceMappingURL=" + outmap + "\n"

	printer = codegen.NewPrinter()
	dtsOutput := printer.PrintModule(dtsMod)

	return CompilerOutput{
		ParseErrors: parseErrors,
		TypeErrors:  typeErrors,
		JS:          jsOutput,
		SourceMap:   sourceMap,
		DTS:         dtsOutput,
	}
}
