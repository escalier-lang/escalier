package compiler

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/codegen"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/type_system"
)

type ModuleOutput struct {
	JS        string
	SourceMap string
	DTS       string
}

type CompilerOutput struct {
	ParseErrors []*parser.Error
	TypeErrors  []checker.Error
	Modules     map[string]ModuleOutput
}

// CheckOutput contains the results of type-checking a package without codegen.
// Used by the LSP server for completions, hover, go-to-definition, etc.
type CheckOutput struct {
	// Lib results
	Module      *ast.Module            // parsed lib module (nil if no lib/ files)
	ModuleScope *checker.Scope         // scope after InferModule
	FileScopes  map[int]*checker.Scope // SourceID -> file scope (lib/ files)

	// Script results (bin/ files)
	Scripts      map[int]*ast.Script    // SourceID -> parsed script AST
	ScriptScopes map[int]*checker.Scope // SourceID -> script scope

	ParseErrors []*parser.Error
	TypeErrors  []checker.Error
}

// CheckPackage performs parsing and type-checking for a package (lib/ + bin/)
// without codegen. Returns ASTs, scopes, and errors needed by the LSP.
func CheckPackage(sources []*ast.Source) CheckOutput {
	libSources := []*ast.Source{}
	for _, src := range sources {
		if strings.HasPrefix(src.Path, "lib/") {
			libSources = append(libSources, src)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	output := CheckOutput{
		FileScopes:   map[int]*checker.Scope{},
		Scripts:      map[int]*ast.Script{},
		ScriptScopes: map[int]*checker.Scope{},
		ParseErrors:  []*parser.Error{},
		TypeErrors:   []checker.Error{},
	}

	var libNS *type_system.Namespace

	if len(libSources) > 0 {
		module, parseErrors := parser.ParseLibFiles(ctx, libSources)

		c := checker.NewChecker()
		inferCtx := checker.Context{
			Scope:      checker.Prelude(c),
			IsAsync:    false,
			IsPatMatch: false,
		}
		typeErrors := c.InferModule(inferCtx, module)

		libNS = inferCtx.Scope.Namespace

		output.Module = module
		output.ModuleScope = inferCtx.Scope
		output.FileScopes = c.FileScopes
		output.ParseErrors = append(output.ParseErrors, parseErrors...)
		output.TypeErrors = append(output.TypeErrors, typeErrors...)
	}

	// Check each bin/ script with the lib namespace injected.
	for _, src := range sources {
		if !strings.HasPrefix(src.Path, "bin/") {
			continue
		}
		scriptOutput := CheckBinScript(ctx, libNS, src)
		output.Scripts[src.ID] = scriptOutput.Script
		output.ScriptScopes[src.ID] = scriptOutput.Scope
		output.ParseErrors = append(output.ParseErrors, scriptOutput.ParseErrors...)
		output.TypeErrors = append(output.TypeErrors, scriptOutput.TypeErrors...)
	}

	return output
}

// BinScriptOutput contains the results of checking a single bin/ script.
type BinScriptOutput struct {
	Script      *ast.Script
	Scope       *checker.Scope
	ParseErrors []*parser.Error
	TypeErrors  []checker.Error
}

// CheckBinScript parses and type-checks a single bin/ script with the given
// lib namespace injected into the scope chain. If libNS is nil, the script
// is checked with only the prelude in scope.
func CheckBinScript(ctx context.Context, libNS *type_system.Namespace, src *ast.Source) BinScriptOutput {
	p := parser.NewParser(ctx, src)
	script, parseErrors := p.ParseScript()

	c := checker.NewChecker()
	scope := checker.Prelude(c)
	if libNS != nil {
		scope = scope.WithNewScopeAndNamespace(libNS)
	}
	inferCtx := checker.Context{
		Scope:      scope,
		IsAsync:    false,
		IsPatMatch: false,
	}
	scriptScope, typeErrors := c.InferScript(inferCtx, script)

	return BinScriptOutput{
		Script:      script,
		Scope:       scriptScope,
		ParseErrors: parseErrors,
		TypeErrors:  typeErrors,
	}
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
		Modules: map[string]ModuleOutput{
			"index": {
				JS:        jsOutput,
				SourceMap: sourceMap,
				DTS:       dtsOutput,
			},
		},
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

	output := CompilerOutput{
		ParseErrors: []*parser.Error{},
		TypeErrors:  []checker.Error{},
		Modules:     map[string]ModuleOutput{},
	}

	var libNS *type_system.Namespace

	if len(libSources) > 0 {
		inMod, parseErrors := parser.ParseLibFiles(ctx, libSources)
		depGraph := dep_graph.BuildDepGraph(inMod)

		c := checker.NewChecker()
		inferCtx := checker.Context{
			// We add a new scope here to avoid polluting the prelude scope.
			Scope:      checker.Prelude(c).WithNewScope(),
			IsAsync:    false,
			IsPatMatch: false,
		}
		typeErrors := c.InferDepGraph(inferCtx, depGraph)

		// No longer need MergeOverloadedFunctions - overloads are already grouped by BindingKey

		libNS = inferCtx.Scope.Namespace

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

		output.ParseErrors = append(output.ParseErrors, parseErrors...)
		output.TypeErrors = append(output.TypeErrors, typeErrors...)
		output.Modules["lib/index"] = ModuleOutput{
			JS:        jsOutput,
			SourceMap: sourceMap,
			DTS:       dtsOutput,
		}
	}

	// Compile each of the bin/ scripts, using the libNS as the base namespace.
	binSources := []*ast.Source{}
	for _, src := range sources {
		if strings.HasPrefix(src.Path, "bin/") {
			binSources = append(binSources, src)
		}
	}

	for _, src := range binSources {
		scriptOutput := CompileScript(libNS, src)
		output.ParseErrors = append(output.ParseErrors, scriptOutput.ParseErrors...)
		output.TypeErrors = append(output.TypeErrors, scriptOutput.TypeErrors...)

		ext := filepath.Ext(src.Path)
		name := src.Path[:len(src.Path)-len(ext)]
		output.Modules[name] = scriptOutput.Modules["bin/index"]
	}

	return output
}

// symbolCollector is a visitor that collects top-level library symbols used in the script
type symbolCollector struct {
	ast.DefaultVisitor
	libNS       *type_system.Namespace
	usedSymbols map[string]bool
}

func (v *symbolCollector) EnterExpr(e ast.Expr) bool {
	if ident, ok := e.(*ast.IdentExpr); ok {
		// Check if this identifier is a top-level symbol in libNS
		if _, exists := v.libNS.Values[ident.Name]; exists {
			v.usedSymbols[ident.Name] = true
		}
		if _, exists := v.libNS.GetNamespace(ident.Name); exists {
			v.usedSymbols[ident.Name] = true
		}
	}
	return true
}

// collectUsedLibSymbols walks the AST to find which top-level symbols from libNS are used
func collectUsedLibSymbols(script *ast.Script, libNS *type_system.Namespace) []string {
	if libNS == nil {
		return nil
	}

	visitor := &symbolCollector{
		libNS:       libNS,
		usedSymbols: make(map[string]bool),
	}

	// Walk the AST
	for _, stmt := range script.Stmts {
		stmt.Accept(visitor)
	}

	// Convert map to sorted slice
	result := make([]string, 0, len(visitor.usedSymbols))
	for symbol := range visitor.usedSymbols {
		result = append(result, symbol)
	}
	sort.Strings(result)
	return result
}

// TODO: Update this so that we inject an `import` statement at the start of
// each script source to import the `lib` namespace.
func CompileScript(libNS *type_system.Namespace, source *ast.Source) CompilerOutput {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	inMod, parseErrors := p.ParseScript()

	c := checker.NewChecker()
	scope := checker.Prelude(c)
	if libNS != nil {
		scope = scope.WithNewScopeAndNamespace(libNS)
	}
	inferCtx := checker.Context{
		Scope:      scope,
		IsAsync:    false,
		IsPatMatch: false,
	}
	_, typeErrors := c.InferScript(inferCtx, inMod)

	builder := &codegen.Builder{}
	jsMod := builder.BuildScript(inMod)

	// Collect used library symbols and add import statement if needed
	usedSymbols := collectUsedLibSymbols(inMod, libNS)
	if len(usedSymbols) > 0 {
		// Create an import declaration for the used symbols
		importDecl := codegen.NewImportDecl(usedSymbols, "../lib/index.js", nil)
		importStmt := &codegen.DeclStmt{
			Decl: importDecl,
			// span and source are nil, which is fine
		}
		// Prepend the import statement to the module
		jsMod.Stmts = append([]codegen.Stmt{importStmt}, jsMod.Stmts...)
	}

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

	baseName := strings.TrimSuffix(filepath.Base(source.Path), filepath.Ext(source.Path)) + ".js"
	jsFile := "./" + baseName
	sourceMap := codegen.GenerateSourceMap([]*ast.Source{source}, jsMod, jsFile)

	outmap := jsFile + ".map"
	jsOutput += "//# sourceMappingURL=" + outmap + "\n"

	// printer = codegen.NewPrinter()
	// dtsOutput := printer.PrintModule(dtsMod)
	dtsOutput := ""

	return CompilerOutput{
		ParseErrors: parseErrors,
		TypeErrors:  typeErrors,
		Modules: map[string]ModuleOutput{
			"bin/index": {
				JS:        jsOutput,
				SourceMap: sourceMap,
				DTS:       dtsOutput,
			},
		},
	}
}

// Assumes that the current working directory is the root of the package
func FindSourceFiles() ([]string, error) {
	// Find all .esc files in the lib directory
	var files []string
	_, err := os.Stat("lib")
	if !os.IsNotExist(err) {
		err = filepath.WalkDir("lib", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			// Check if it's a file and ends with .esc
			if !d.IsDir() && strings.HasSuffix(d.Name(), ".esc") {
				files = append(files, path)
			}

			return nil
		})

		if err != nil {
			fmt.Fprintln(os.Stderr, "failed to walk directory:", err)
			return nil, err
		}
	}

	_, err = os.Stat("bin")
	if !os.IsNotExist(err) {
		err = filepath.WalkDir("bin", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			// Check if it's a file and ends with .esc
			if !d.IsDir() && strings.HasSuffix(d.Name(), ".esc") {
				files = append(files, path)
			}

			return nil
		})

		if err != nil {
			fmt.Fprintln(os.Stderr, "failed to walk directory:", err)
			return nil, err
		}
	}

	return files, nil
}
