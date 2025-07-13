package compiler

import (
	"context"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/codegen"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/type_system"
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
		Filename:   "input.esc",
		Scope:      checker.Prelude(),
		IsAsync:    false,
		IsPatMatch: false,
	}
	scope, typeErrors := c.InferScript(inferCtx, inMod)

	namespace := checker.Namespace{
		Values: make(map[checker.QualifiedIdent]*checker.Binding),
		Types:  make(map[checker.QualifiedIdent]*type_system.TypeAlias),
	}

	for name, binding := range scope.Values {
		namespace.Values[checker.QualifiedIdent(name)] = binding
	}
	for name, typeAlias := range scope.Types {
		namespace.Types[checker.QualifiedIdent(name)] = typeAlias
	}

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

	dtsMod := builder.BuildDefinitions(decls, namespace)

	printer := codegen.NewPrinter()
	jsOutput := printer.PrintModule(jsMod)

	jsFile := "./index.js"
	sourceMap := codegen.GenerateSourceMap([]*ast.Source{source}, jsMod, jsFile)

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

func CompileLib(sources []*ast.Source) CompilerOutput {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	inMod, parseErrors := parser.ParseLibFiles(ctx, sources)
	depGraph := dep_graph.BuildDepGraph(inMod)

	c := checker.NewChecker()
	inferCtx := checker.Context{
		Filename:   "input.esc",
		Scope:      checker.Prelude(),
		IsAsync:    false,
		IsPatMatch: false,
	}
	namespace, typeErrors := c.InferDepGraph(inferCtx, depGraph)

	if len(parseErrors) > 0 {
		return CompilerOutput{
			JS:          "",
			DTS:         "",
			SourceMap:   "",
			ParseErrors: parseErrors,
			TypeErrors:  typeErrors,
		}
	}

	components := depGraph.FindStronglyConnectedComponents(0)
	var decls []ast.Decl
	for _, component := range components {
		for _, declID := range component {
			decl, _ := depGraph.Decls.Get(declID)
			decls = append(decls, decl)
		}
	}

	builder := &codegen.Builder{}
	jsMod := builder.BuildDecls(decls)
	dtsMod := builder.BuildDefinitions(decls, namespace)

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
