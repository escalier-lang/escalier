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
)

type CompUnitOutput struct {
	JS        string
	SourceMap string
	DTS       string
}

type CompilerOutput struct {
	ParseErrors []*parser.Error
	TypeErrors  []Diagnostic
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

	// LibScope is the library surface each bin/ script was checked against, nil
	// when the package has no lib/ files. The LSP caches it so a bin/ script that
	// changes on its own is re-checked without re-checking lib/.
	LibScope LibScope

	// Sources maps a SourceID to the file it names. A Span carries byte
	// offsets, so anything turning one into a line and column needs the text
	// it indexes into.
	Sources map[int]*ast.Source

	ParseErrors []*parser.Error
	TypeErrors  []Diagnostic
}

// SourceByID returns the file a SourceID names, or nil when the output holds
// no source for it.
func (o *CheckOutput) SourceByID(sourceID int) *ast.Source {
	return o.Sources[sourceID]
}

// CheckLibOutput contains the results of type-checking lib/ files.
type CheckLibOutput struct {
	Module      *ast.Module            // parsed lib module (nil if no lib/ files)
	ModuleScope *checker.Scope         // scope after InferModule
	FileScopes  map[int]*checker.Scope // SourceID -> file scope (lib/ files)
	LibScope    LibScope               // lib surface for bin/ script checking
	ParseErrors []*parser.Error
	TypeErrors  []Diagnostic
}

// CheckLib parses and type-checks lib/ source files without codegen.
func CheckLib(ctx context.Context, libSources []*ast.Source) CheckLibOutput {
	if len(libSources) == 0 {
		return CheckLibOutput{
			FileScopes:  map[int]*checker.Scope{},
			ParseErrors: []*parser.Error{},
			TypeErrors:  []Diagnostic{},
		}
	}

	module, parseErrors := parser.ParseLibFiles(ctx, libSources)
	lib := selectBackend().checkLib(ctx, module)

	return CheckLibOutput{
		Module:      module,
		ModuleScope: lib.scope,
		FileScopes:  lib.fileScopes,
		LibScope:    lib.lib,
		ParseErrors: parseErrors,
		TypeErrors:  lib.diagnostics,
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

	sourcesByID := make(map[int]*ast.Source, len(sources))
	for _, src := range sources {
		sourcesByID[src.ID] = src
	}

	output := CheckOutput{
		Module:       libOutput.Module,
		ModuleScope:  libOutput.ModuleScope,
		FileScopes:   libOutput.FileScopes,
		Scripts:      map[int]*ast.Script{},
		ScriptScopes: map[int]*checker.Scope{},
		LibScope:     libOutput.LibScope,
		Sources:      sourcesByID,
		ParseErrors:  libOutput.ParseErrors,
		TypeErrors:   libOutput.TypeErrors,
	}

	// Check each bin/ script with the library surface in scope.
	for _, src := range sources {
		if !strings.HasPrefix(src.Path, "bin/") {
			continue
		}
		scriptOutput := CheckBinScript(ctx, libOutput.LibScope, src)
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
	TypeErrors  []Diagnostic
}

// CheckBinScript parses and type-checks a single bin/ script with the given
// library surface in scope. If lib is nil, the script is checked with only the
// prelude in scope.
func CheckBinScript(ctx context.Context, lib LibScope, src *ast.Source) BinScriptOutput {
	p := parser.NewParser(ctx, src)
	script, parseErrors := p.ParseScript()

	result := checkScriptIn(ctx, selectBackend(), lib, script)

	return BinScriptOutput{
		Script:      script,
		Scope:       result.scope,
		ParseErrors: parseErrors,
		TypeErrors:  result.diagnostics,
	}
}

func Compile(source *ast.Source) CompilerOutput {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	inMod, parseErrors := p.ParseScript()

	typeErrors := selectBackend().checkScript(ctx, inMod).diagnostics

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

	selected := selectBackend()

	output := CompilerOutput{
		ParseErrors: []*parser.Error{},
		TypeErrors:  []Diagnostic{},
		CompUnits:   map[string]CompUnitOutput{},
	}

	var libScope LibScope

	if len(libSources) > 0 {
		inMod, parseErrors := parser.ParseLibFiles(ctx, libSources)

		lib := selected.checkLib(ctx, inMod)
		libScope = lib.lib

		output.ParseErrors = append(output.ParseErrors, parseErrors...)
		output.TypeErrors = append(output.TypeErrors, lib.diagnostics...)

		// A parse error leaves an error node in the tree where a declaration or type
		// annotation belongs, and codegen has no lowering for one. `buildTypeAnn`
		// panics with `unknown type: <error>` on reaching it. Inference above
		// tolerates those nodes, so only the emit half is skipped and the diagnostics
		// still reach the caller. A type error needs no such skip. The tree is well
		// formed, so codegen runs and the caller decides whether to keep its output.
		if len(parseErrors) == 0 {
			// The graph comes from the run rather than a second call to
			// dep_graph.BuildDepGraph, so the emitter walks the declarations in the
			// order inference typed them.
			builder := &codegen.Builder{}
			jsMod := builder.BuildTopLevelDecls(lib.depGraph)

			printer := codegen.NewPrinter()
			jsOutput := printer.PrintModule(jsMod)

			jsFile := "./index.js"
			sourceMap := codegen.GenerateSourceMap(sources, jsMod, jsFile)

			outmap := "./index.js.map"
			jsOutput += "//# sourceMappingURL=" + outmap + "\n"

			// A .d.ts is rendered from the library's type surface, which only the
			// old checker produces in the shape codegen reads. The solver path
			// leaves it empty until the declaration emitter lands.
			dtsOutput := ""
			if lib.dtsNamespace != nil {
				dtsMod := builder.BuildDefinitions(lib.depGraph, lib.dtsNamespace)
				printer = codegen.NewPrinter()
				dtsOutput = printer.PrintModule(dtsMod)
			}

			output.CompUnits["lib/index"] = CompUnitOutput{
				JS:        jsOutput,
				SourceMap: sourceMap,
				DTS:       dtsOutput,
			}
		}
	}

	// Compile each of the bin/ scripts, with the library surface in scope.
	binSources := []*ast.Source{}
	for _, src := range sources {
		if strings.HasPrefix(src.Path, "bin/") {
			binSources = append(binSources, src)
		}
	}

	for _, src := range binSources {
		scriptOutput := CompileScript(libScope, src)
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
	lib         LibScope
	usedSymbols map[string]bool
}

func (v *symbolCollector) EnterExpr(e ast.Expr) bool {
	if ident, ok := e.(*ast.IdentExpr); ok {
		if v.lib.declaresTopLevel(ident.Name) {
			v.usedSymbols[ident.Name] = true
		}
	}
	return true
}

// collectUsedLibSymbols walks the AST to find which of the library's top-level
// symbols the script uses
func collectUsedLibSymbols(script *ast.Script, lib LibScope) []string {
	if lib == nil {
		return nil
	}

	visitor := &symbolCollector{
		lib:         lib,
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
func CompileScript(lib LibScope, source *ast.Source) CompilerOutput {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	inMod, parseErrors := p.ParseScript()

	typeErrors := checkScriptIn(ctx, selectBackend(), lib, inMod).diagnostics

	builder := &codegen.Builder{}
	jsMod := builder.BuildScript(inMod)

	// Collect used library symbols and add import statement if needed
	usedSymbols := collectUsedLibSymbols(inMod, lib)
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
