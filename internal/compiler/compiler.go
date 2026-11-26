package compiler

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/codegen"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/solver"
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
	Module *ast.Module // parsed lib module (nil if no lib/ files)
	// ModuleInfo holds the per-node records for the lib/ module. It is nil when the
	// package has no lib/ files.
	ModuleInfo *solver.Info
	// ModuleScope and FileScopes are the scope for the lib/ module and the import scope
	// for each lib/ file, keyed by SourceID. Both are nil when the package has no lib/
	// files.
	ModuleScope *solver.Scope
	FileScopes  map[int]*solver.Scope

	// Script results (bin/ files)
	Scripts map[int]*ast.Script // SourceID -> parsed script AST
	// ScriptInfos maps a bin/ script's SourceID to the per-node records for it.
	ScriptInfos map[int]*solver.Info
	// ScriptScopes maps a bin/ script's SourceID to the scope it was checked in.
	ScriptScopes map[int]*solver.Scope

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

// InfoFor returns internal/solver's per-node records for the file a SourceID names,
// or nil when the output holds none for it. A bin/ script has records of its own,
// and every lib/ file shares the module's.
func (o *CheckOutput) InfoFor(sourceID int) *solver.Info {
	if info, ok := o.ScriptInfos[sourceID]; ok {
		return info
	}
	if o.Module != nil && o.Module.GetSourcePath(sourceID) != "" {
		return o.ModuleInfo
	}
	return nil
}

// CheckLibOutput contains the results of type-checking lib/ files.
type CheckLibOutput struct {
	Module      *ast.Module           // parsed lib module (nil if no lib/ files)
	ModuleScope *solver.Scope         // scope after inference
	FileScopes  map[int]*solver.Scope // SourceID -> file scope (lib/ files)
	LibScope    LibScope              // lib surface for bin/ script checking
	Info        *solver.Info          // per-node records for the module
	ParseErrors []*parser.Error
	TypeErrors  []Diagnostic
}

// CheckLib parses and type-checks lib/ source files without codegen, as config says.
func CheckLib(ctx context.Context, libSources []*ast.Source, config PackageConfig) CheckLibOutput {
	if len(libSources) == 0 {
		return CheckLibOutput{
			FileScopes:  map[int]*solver.Scope{},
			ParseErrors: []*parser.Error{},
			TypeErrors:  []Diagnostic{},
		}
	}

	module, parseErrors := parser.ParseLibFiles(ctx, libSources)
	lib := newBackend(config).checkLib(ctx, module)

	return CheckLibOutput{
		Module:      module,
		ModuleScope: lib.scope,
		FileScopes:  lib.fileScopes,
		LibScope:    lib.lib,
		Info:        lib.info,
		ParseErrors: parseErrors,
		TypeErrors:  lib.diagnostics,
	}
}

// CheckPackage performs parsing and type-checking for a package (lib/ + bin/)
// without codegen, as config says. Returns ASTs, scopes, and errors needed by the LSP.
func CheckPackage(sources []*ast.Source, config PackageConfig) CheckOutput {
	libSources := []*ast.Source{}
	for _, src := range sources {
		if strings.HasPrefix(src.Path, "lib/") {
			libSources = append(libSources, src)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	libOutput := CheckLib(ctx, libSources, config)

	sourcesByID := make(map[int]*ast.Source, len(sources))
	for _, src := range sources {
		sourcesByID[src.ID] = src
	}

	output := CheckOutput{
		Module:       libOutput.Module,
		ModuleInfo:   libOutput.Info,
		ModuleScope:  libOutput.ModuleScope,
		FileScopes:   libOutput.FileScopes,
		Scripts:      map[int]*ast.Script{},
		ScriptInfos:  map[int]*solver.Info{},
		ScriptScopes: map[int]*solver.Scope{},
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
		scriptOutput := CheckBinScript(ctx, libOutput.LibScope, src, config)
		output.Scripts[src.ID] = scriptOutput.Script
		output.ScriptInfos[src.ID] = scriptOutput.Info
		output.ScriptScopes[src.ID] = scriptOutput.Scope
		output.ParseErrors = append(output.ParseErrors, scriptOutput.ParseErrors...)
		output.TypeErrors = append(output.TypeErrors, scriptOutput.TypeErrors...)
	}

	return output
}

// BinScriptOutput contains the results of checking a single bin/ script.
type BinScriptOutput struct {
	Script      *ast.Script
	Scope       *solver.Scope
	Info        *solver.Info
	ParseErrors []*parser.Error
	TypeErrors  []Diagnostic
}

// CheckBinScript parses and type-checks a single bin/ script with the given
// library surface in scope. If lib is nil, the script is checked as config says with only
// the prelude and the ambient builtins in scope.
func CheckBinScript(ctx context.Context, lib LibScope, src *ast.Source, config PackageConfig) BinScriptOutput {
	p := parser.NewParser(ctx, src)
	script, parseErrors := p.ParseScript()

	result := checkScriptIn(ctx, newBackend(config), lib, script)

	return BinScriptOutput{
		Script:      script,
		Scope:       result.scope,
		Info:        result.info,
		ParseErrors: parseErrors,
		TypeErrors:  result.diagnostics,
	}
}

// Compile compiles source as a script with only the prelude in scope. Its output
// holds one unit, `index`, whose JavaScript is named `./index.js`.
func Compile(source *ast.Source) CompilerOutput {
	// Compile's output is not installed as a command, so it has no `#!` line.
	return compileScript(newBackend(PackageConfig{}), nil, source, "index", "./index.js", false)
}

// CompilePackage compiles a package's lib/ and bin/ sources, checking them as config
// says. ReadPackageConfig reads config from the package's package.json.
func CompilePackage(sources []*ast.Source, config PackageConfig) CompilerOutput {
	// Compile everything in libs/ into a single .js and .d.ts file.
	libSources := []*ast.Source{}
	for _, src := range sources {
		if strings.HasPrefix(src.Path, "lib/") {
			libSources = append(libSources, src)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	selected := newBackend(config)

	output := CompilerOutput{
		ParseErrors: []*parser.Error{},
		TypeErrors:  []Diagnostic{},
		CompUnits:   map[string]CompUnitOutput{},
	}

	var libScope LibScope

	var step compileStep
	defer step.wrapPanic()

	if len(libSources) > 0 {
		libName := libUnitName(libSources)

		step.enter(libName, "parsing")
		inMod, parseErrors := parser.ParseLibFiles(ctx, libSources)

		step.enter(libName, "checking")
		lib := selected.checkLib(ctx, inMod)
		libScope = lib.lib

		output.ParseErrors = append(output.ParseErrors, parseErrors...)
		output.TypeErrors = append(output.TypeErrors, lib.diagnostics...)

		// A parse error leaves an error node in the tree where a declaration or type
		// annotation belongs, and codegen has no lowering for one. `buildTypeAnn`
		// panics with `unknown type: <error>` on reaching it. Codegen has no lowering
		// for an enum spread either, which the checker reports as unsupported.
		// Inference above tolerates both, so only the emit half is skipped and the
		// diagnostics still reach the caller. Any other type error needs no such skip.
		// The tree is well formed, so codegen runs and the caller decides whether to
		// keep its output.
		if len(parseErrors) == 0 && !reportsEnumSpread(lib.diagnostics) {
			// The graph comes from the run rather than a second call to
			// dep_graph.BuildDepGraph, so the emitter walks the declarations in the
			// order inference typed them.
			step.enter(libName, "generating JavaScript for")
			builder := codegen.NewBuilder(lib.jsTypes)
			jsMod := builder.BuildTopLevelDecls(lib.depGraph)

			printer := codegen.NewPrinter()
			jsOutput := printer.PrintModule(jsMod)

			step.enter(libName, "generating a source map for")
			jsFile := "./index.js"
			sourceMap, err := codegen.GenerateSourceMap(sources, jsMod, jsFile)
			if err != nil {
				output.TypeErrors = append(output.TypeErrors, &emitError{span: moduleSpan(inMod), err: err})
			}

			outmap := "./index.js.map"
			jsOutput += "//# sourceMappingURL=" + outmap + "\n"

			// A .d.ts is rendered from the library's type surface, so the run that
			// produced it carries the renderer.
			step.enter(libName, "generating type definitions for")
			dtsOutput := ""
			if lib.dts != nil {
				dtsMod := lib.dts.buildDefinitions(builder, lib.depGraph)
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
		// compileScript names its own phases, so this step names the script only for
		// a panic outside them.
		step.enter(src.Path, "compiling")
		// A bin/ script is the package's `bin` entry, which npm runs through its `#!` line.
		scriptOutput := compileScript(selected, libScope, src, "bin/index", scriptJSFile(src), true)
		output.ParseErrors = append(output.ParseErrors, scriptOutput.ParseErrors...)
		output.TypeErrors = append(output.TypeErrors, scriptOutput.TypeErrors...)

		// compileScript emits no unit for a script it skipped codegen for.
		if unit, emitted := scriptOutput.CompUnits["bin/index"]; emitted {
			ext := filepath.Ext(src.Path)
			name := src.Path[:len(src.Path)-len(ext)]
			output.CompUnits[name] = unit
		}
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
	script.Accept(visitor)

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
	// CompileScript compiles a bin/ script, which npm runs through its `#!` line.
	return compileScript(newBackend(PackageConfig{}), lib, source, "bin/index", scriptJSFile(source), true)
}

// scriptJSFile returns the path, relative to the build directory, of the JavaScript
// emitted for the script source, so `bin/main.esc` emits `./main.js`.
func scriptJSFile(source *ast.Source) string {
	return "./" + strings.TrimSuffix(filepath.Base(source.Path), filepath.Ext(source.Path)) + ".js"
}

// compileScript compiles source with lib in scope, using b to check it when lib is nil.
// The output holds one unit named unitName, whose JavaScript is named jsFile, or no
// unit when the script holds an enum spread. When shebang is true, the JavaScript
// starts with a `#!/usr/bin/env node` line.
func compileScript(b backend, lib LibScope, source *ast.Source, unitName, jsFile string, shebang bool) CompilerOutput {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var step compileStep
	defer step.wrapPanic()

	step.enter(source.Path, "parsing")
	p := parser.NewParser(ctx, source)
	inMod, parseErrors := p.ParseScript()

	step.enter(source.Path, "checking")
	result := checkScriptIn(ctx, b, lib, inMod)
	typeErrors := result.diagnostics

	// Codegen has no lowering for an enum spread, so a script holding one emits no unit.
	if reportsEnumSpread(typeErrors) {
		return CompilerOutput{
			ParseErrors: parseErrors,
			TypeErrors:  typeErrors,
			CompUnits:   map[string]CompUnitOutput{},
		}
	}

	step.enter(source.Path, "generating JavaScript for")
	builder := codegen.NewBuilder(result.jsTypes)
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
	// npm runs a package's `bin` entry through its `#!` line. POSIX shells
	// read it directly, and on Windows npm's .cmd shim picks the interpreter
	// from it, so a script without one fails as an installed command.
	if shebang {
		printer.PrintShebang("/usr/bin/env node")
	}
	jsOutput := printer.PrintModule(jsMod)

	step.enter(source.Path, "generating a source map for")
	sourceMap, err := codegen.GenerateSourceMap([]*ast.Source{source}, jsMod, jsFile)
	if err != nil {
		typeErrors = append(typeErrors, &emitError{span: scriptSpan(inMod), err: err})
	}

	outmap := jsFile + ".map"
	jsOutput += "//# sourceMappingURL=" + outmap + "\n"

	// printer = codegen.NewPrinter()
	// dtsOutput := printer.PrintModule(dtsMod)
	dtsOutput := ""

	return CompilerOutput{
		ParseErrors: parseErrors,
		TypeErrors:  typeErrors,
		CompUnits: map[string]CompUnitOutput{
			unitName: {
				JS:        jsOutput,
				SourceMap: sourceMap,
				DTS:       dtsOutput,
			},
		},
	}
}

// FindSourceFiles returns the paths of the .esc files under lib/ and bin/,
// relative to the current working directory, which must be the package root.
// The paths use forward slashes on every OS, such as "lib/foo/bar.esc", the
// form ast.Source.Path documents and CompilePackage matches against.
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
				files = append(files, filepath.ToSlash(path))
			}

			return nil
		})

		if err != nil {
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
				files = append(files, filepath.ToSlash(path))
			}

			return nil
		})

		if err != nil {
			return nil, err
		}
	}

	return files, nil
}
