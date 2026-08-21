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
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// internalBundleName is the base name of the module holding every declaration in lib/. It is
// not the package entry point. `main` in package.json points at the public wrapper emitted as
// index.js, which re-exports the part of this bundle the source marked `export`. Scripts under
// bin/ are part of the package and import the bundle directly, so they reach members the
// package keeps to itself.
const internalBundleName = "internal"

type CompUnitOutput struct {
	JS        string
	SourceMap string
	DTS       string
}

type CompilerOutput struct {
	ParseErrors []*parser.Error
	TypeErrors  []checker.Error
	CompUnits   map[string]CompUnitOutput
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

// CheckLibOutput contains the results of type-checking lib/ files.
type CheckLibOutput struct {
	Module      *ast.Module            // parsed lib module (nil if no lib/ files)
	ModuleScope *checker.Scope         // scope after InferModule
	FileScopes  map[int]*checker.Scope // SourceID -> file scope (lib/ files)
	LibNS       *type_system.Namespace // lib namespace for bin/ script checking
	ParseErrors []*parser.Error
	TypeErrors  []checker.Error
}

// CheckLib parses and type-checks lib/ source files without codegen.
func CheckLib(ctx context.Context, libSources []*ast.Source) CheckLibOutput {
	if len(libSources) == 0 {
		return CheckLibOutput{
			FileScopes:  map[int]*checker.Scope{},
			ParseErrors: []*parser.Error{},
			TypeErrors:  []checker.Error{},
		}
	}

	module, parseErrors := parser.ParseLibFiles(ctx, libSources)

	c := checker.NewChecker(ctx)
	inferCtx := checker.Context{
		// Create a child scope to avoid polluting the prelude with lib bindings.
		Scope:      checker.Prelude(c).WithNewScope(),
		IsAsync:    false,
		IsPatMatch: false,
	}
	_, typeErrors := c.InferModule(inferCtx, module)

	return CheckLibOutput{
		Module:      module,
		ModuleScope: inferCtx.Scope,
		FileScopes:  c.FileScopes,
		LibNS:       inferCtx.Scope.Namespace,
		ParseErrors: parseErrors,
		TypeErrors:  typeErrors,
	}
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

	libOutput := CheckLib(ctx, libSources)

	output := CheckOutput{
		Module:       libOutput.Module,
		ModuleScope:  libOutput.ModuleScope,
		FileScopes:   libOutput.FileScopes,
		Scripts:      map[int]*ast.Script{},
		ScriptScopes: map[int]*checker.Scope{},
		ParseErrors:  libOutput.ParseErrors,
		TypeErrors:   libOutput.TypeErrors,
	}

	// Check each bin/ script with the lib namespace injected.
	for _, src := range sources {
		if !strings.HasPrefix(src.Path, "bin/") {
			continue
		}
		scriptOutput := CheckBinScript(ctx, libOutput.LibNS, src)
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

	c := checker.NewChecker(ctx)
	scope := checker.Prelude(c)
	if libNS != nil {
		// Insert the lib namespace between the prelude and the script scope
		// so bin/ scripts can access lib exports without an explicit import.
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	inMod, parseErrors := p.ParseScript()

	c := checker.NewChecker(ctx)
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
		CompUnits: map[string]CompUnitOutput{
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	output := CompilerOutput{
		ParseErrors: []*parser.Error{},
		TypeErrors:  []checker.Error{},
		CompUnits:   map[string]CompUnitOutput{},
	}

	var libNS *type_system.Namespace

	if len(libSources) > 0 {
		inMod, parseErrors := parser.ParseLibFiles(ctx, libSources)

		c := checker.NewChecker(ctx)
		inferCtx := checker.Context{
			// We add a new scope here to avoid polluting the prelude scope.
			Scope:      checker.Prelude(c).WithNewScope(),
			IsAsync:    false,
			IsPatMatch: false,
		}
		// InferModule (rather than the lower-level InferDepGraph) so
		// per-file imports — including pseudo-package `import "std:*"`
		// statements — are processed before declarations are checked.
		// It returns the dep_graph it builds in Phase 2; we reuse that
		// here for codegen instead of rebuilding from scratch.
		depGraph, typeErrors := c.InferModule(inferCtx, inMod)

		// No longer need MergeOverloadedFunctions - overloads are already grouped by BindingKey

		libNS = inferCtx.Scope.Namespace

		builder := &codegen.Builder{}
		internalMod := builder.BuildTopLevelDecls(depGraph)
		dtsMod := builder.BuildDefinitions(depGraph, libNS)

		printer := codegen.NewPrinter()
		internalJS := printer.PrintModule(internalMod)

		internalFile := "./" + internalBundleName + ".js"
		internalSourceMap := codegen.GenerateSourceMap(sources, internalMod, internalFile)
		internalJS += "//# sourceMappingURL=" + internalFile + ".map\n"

		// The public wrapper re-exports the internal bundle rather than holding a second copy
		// of the code, so a class has one definition and `instanceof` agrees across both entry
		// points.
		//
		// Every statement in it is generated, so it carries no source map. A package that
		// exports nothing needs no public entry point either, and the empty JS keeps the
		// file off disk.
		wrapperMod := codegen.BuildPublicWrapper(depGraph, internalFile)
		wrapperJS := ""
		if len(wrapperMod.Stmts) > 0 {
			printer = codegen.NewPrinter()
			wrapperJS = printer.PrintModule(wrapperMod)
		}

		printer = codegen.NewPrinter()
		dtsOutput := printer.PrintModule(dtsMod)

		output.ParseErrors = append(output.ParseErrors, parseErrors...)
		output.TypeErrors = append(output.TypeErrors, typeErrors...)
		output.CompUnits["lib/"+internalBundleName] = CompUnitOutput{
			JS:        internalJS,
			SourceMap: internalSourceMap,
			DTS:       "",
		}
		// The .d.ts sits beside the public entry point, which is what `main` in
		// package.json points at. It marks a declaration as exported when the source
		// marked it `export`, matching what the wrapper forwards.
		output.CompUnits["lib/index"] = CompUnitOutput{
			JS:        wrapperJS,
			SourceMap: "",
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
		output.CompUnits[name] = scriptOutput.CompUnits["bin/index"]
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	inMod, parseErrors := p.ParseScript()

	c := checker.NewChecker(ctx)
	scope := checker.Prelude(c)
	if libNS != nil {
		// Insert the lib namespace between the prelude and the script scope
		// so bin/ scripts can access lib exports without an explicit import.
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
		// A script is part of the package, so it imports the internal bundle and reaches
		// declarations the source did not mark `export`.
		importDecl := codegen.NewImportDecl(usedSymbols, "../lib/"+internalBundleName+".js", nil)
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
		CompUnits: map[string]CompUnitOutput{
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
