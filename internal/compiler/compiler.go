package compiler

import (
	"context"
	"strings"
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
		Scope:      checker.Prelude(c),
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
	// var decls []ast.Decl
	// for _, d := range inMod.Stmts {
	// 	if ds, ok := d.(*ast.DeclStmt); ok {
	// 		decls = append(decls, ds.Decl)
	// 	}
	// }

	// TODO: Create a separate version of BuildDefinitions that works with just
	// the decls slice instead of the dep_graph.
	// dtsMod := builder.BuildDefinitions(decls, namespace)

	printer := codegen.NewPrinter()
	jsOutput := printer.PrintModule(jsMod)

	jsFile := "./index.js"
	sourceMap := codegen.GenerateSourceMap([]*ast.Source{source}, jsMod, jsFile)

	outmap := "./index.js.map"
	jsOutput += "//# sourceMappingURL=" + outmap + "\n"

	// printer = codegen.NewPrinter()
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

func CompilePackage(sources []*ast.Source) CompilerOutput {
	// Compile everything in libs/ into a single .js and .d.ts file.
	libSources := []*ast.Source{}
	for _, src := range sources {
		if strings.HasPrefix(src.Path, "lib/") {
			libSources = append(libSources, src)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	inMod, parseErrors := parser.ParseLibFiles(ctx, libSources)
	depGraph := dep_graph.BuildDepGraph(inMod)

	c := checker.NewChecker()
	inferCtx := checker.Context{
		Scope:      checker.Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}
	libNS, typeErrors := c.InferDepGraph(inferCtx, depGraph)

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
	dtsMod := builder.BuildDefinitions(depGraph, libNS)

	printer := codegen.NewPrinter()
	jsOutput := printer.PrintModule(jsMod)

	jsFile := "./index.js"
	sourceMap := codegen.GenerateSourceMap(sources, jsMod, jsFile)

	outmap := "./index.js.map"
	jsOutput += "//# sourceMappingURL=" + outmap + "\n"

	printer = codegen.NewPrinter()
	dtsOutput := printer.PrintModule(dtsMod)

	// Compile each of the bin/ scripts, using the libNS as the base namespace.
	binSources := []*ast.Source{}
	for _, src := range sources {
		if strings.HasPrefix(src.Path, "bin/") {
			binSources = append(binSources, src)
		}
	}

	for _, src := range binSources {
		compileOutput := CompileScript(libNS, src)
		parseErrors = append(parseErrors, compileOutput.ParseErrors...)
		typeErrors = append(typeErrors, compileOutput.TypeErrors...)
	}

	return CompilerOutput{
		ParseErrors: parseErrors,
		TypeErrors:  typeErrors,
		JS:          jsOutput,
		SourceMap:   sourceMap,
		DTS:         dtsOutput,
	}
}

// TODO: Update this so that we inject an `import` statement at the start of
// each script source to import the `lib` namespace.
func CompileScript(libNS *type_system.Namespace, source *ast.Source) CompilerOutput {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	inMod, parseErrors := p.ParseScript()

	c := checker.NewChecker()
	inferCtx := checker.Context{
		Scope: &checker.Scope{
			Namespace: libNS,
		},
		IsAsync:    false,
		IsPatMatch: false,
	}
	_, typeErrors := c.InferScript(inferCtx, inMod)

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

	// printer = codegen.NewPrinter()
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
