package checker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/interop"
	"github.com/escalier-lang/escalier/internal/resolver"
	"github.com/escalier-lang/escalier/internal/type_system"
)

func findPackageJsonFile(startDir string) (string, bool) {
	currentDir := startDir

	for {
		// Check if package.json exists in current directory
		packageJsonPath := filepath.Join(currentDir, "package.json")
		if _, err := os.Stat(packageJsonPath); err == nil {
			return currentDir, true
		}

		// Get parent directory
		parentDir := filepath.Dir(currentDir)

		// If we've reached the root, stop
		if parentDir == currentDir {
			break
		}

		currentDir = parentDir
	}

	return "", false
}

// resolveModuleDir resolves a module directory path, following symlinks if necessary.
// Returns the resolved directory path, or an error if the module doesn't exist or symlink resolution fails.
func resolveModuleDir(moduleDir string) (string, error) {
	fileInfo, err := os.Lstat(moduleDir)
	if err != nil {
		return "", err
	}

	if fileInfo.Mode()&os.ModeSymlink != 0 {
		resolvedPath, err := os.Readlink(moduleDir)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(resolvedPath) {
			return resolvedPath, nil
		}
		return filepath.Join(filepath.Dir(moduleDir), resolvedPath), nil
	}

	return moduleDir, nil
}

func resolveImport(ctx Context, importStmt *ast.ImportStmt) (string, Error) {
	// Derive the start directory from the source file path
	var startDir string
	if ctx.Module != nil {
		sourceID := importStmt.Span().SourceID
		if source, ok := ctx.Module.Sources[sourceID]; ok && source.Path != "" {
			startDir = filepath.Dir(source.Path)
		}
	}

	// Fallback to current working directory if source path not available
	if startDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", &GenericError{message: "Could not get current working directory for import", span: importStmt.Span()}
		}
		startDir = cwd
	}

	packageJsonDir, found := findPackageJsonFile(startDir)
	if !found {
		return "", &GenericError{message: "Could not find package.json for import", span: importStmt.Span()}
	}

	// First, try to find types in the main package (node_modules/<pkg_name>)
	moduleDir := filepath.Join(packageJsonDir, "node_modules", importStmt.PackageName)
	if resolvedDir, err := resolveModuleDir(moduleDir); err == nil {
		if typesPath, err := resolver.GetTypesEntryPoint(resolvedDir); err == nil {
			return typesPath, nil
		}
	}

	// Fallback: try @types/<pkg_name> for packages that ship types separately
	// (e.g., @types/react for the react package)
	typesModuleDir := filepath.Join(packageJsonDir, "node_modules", "@types", importStmt.PackageName)
	if resolvedDir, err := resolveModuleDir(typesModuleDir); err == nil {
		if typesPath, err := resolver.GetTypesEntryPoint(resolvedDir); err == nil {
			return typesPath, nil
		}
	}

	return "", &GenericError{
		message: "Could not find types for module import: " + importStmt.PackageName +
			" (checked node_modules/" + importStmt.PackageName + " and node_modules/@types/" + importStmt.PackageName + ")",
		span: importStmt.Span(),
	}
}

// resolveDtsImport resolves an import declaration from a .d.ts file to the types entry point.
// The sourceFilePath is the path of the .d.ts file containing the import.
func resolveDtsImport(sourceFilePath string, importDecl *dts_parser.ImportDecl) (string, error) {
	// Get the directory containing the source file
	sourceDir := filepath.Dir(sourceFilePath)

	// Walk up the directory tree looking for node_modules folders
	// This handles pnpm's nested structure where dependencies are in
	// sibling node_modules folders
	currentDir := sourceDir
	for {
		// Check if there's a node_modules folder here
		nodeModulesDir := filepath.Join(currentDir, "node_modules")
		if info, err := os.Stat(nodeModulesDir); err == nil && info.IsDir() {
			// Try to find the package in this node_modules
			moduleDir := filepath.Join(nodeModulesDir, importDecl.From)
			if resolvedDir, err := resolveModuleDir(moduleDir); err == nil {
				if typesPath, err := resolver.GetTypesEntryPoint(resolvedDir); err == nil {
					return typesPath, nil
				}
			}

			// Try @types/<pkg_name>
			typesModuleDir := filepath.Join(nodeModulesDir, "@types", importDecl.From)
			if resolvedDir, err := resolveModuleDir(typesModuleDir); err == nil {
				if typesPath, err := resolver.GetTypesEntryPoint(resolvedDir); err == nil {
					return typesPath, nil
				}
			}
		}

		// Move up to parent directory
		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			// Reached filesystem root
			break
		}
		currentDir = parentDir
	}

	return "", fmt.Errorf("could not find types for import %s", importDecl.From)
}

// ParsedTypeDef holds the result of loading and classifying a .d.ts file.
type ParsedTypeDef struct {
	// PackageModule is the AST module containing package declarations.
	// Contains both exported and non-exported declarations; the Export() method
	// on each declaration distinguishes them. nil if the file has no top-level exports.
	PackageModule *ast.Module

	// GlobalModule is the AST module containing global declarations.
	// This includes declarations from `declare global { ... }` blocks,
	// and all declarations if the file has no top-level exports.
	GlobalModule *ast.Module

	// NamedModules maps module names to their AST modules.
	// e.g., `declare module "lodash/fp" { ... }` creates an entry for "lodash/fp".
	// Contains both exported and non-exported declarations; the Export() method
	// on each declaration distinguishes them. Empty (never nil) if the file has
	// no named module declarations.
	NamedModules map[string]*ast.Module

	// Imports contains import declarations from the .d.ts file.
	// These need to be processed to load transitive dependencies.
	Imports []*dts_parser.ImportDecl

	// PathRefs contains paths from /// <reference path="..." /> directives.
	// These files need to be loaded before processing the main module.
	// Paths are relative to the directory containing the .d.ts file.
	PathRefs []string

	// NamedExports contains export { ... } statements.
	// Local exports (From == "") mark local declarations as exported.
	// Re-exports (From != "") need module loading to copy items from another module.
	NamedExports []*dts_parser.NamedExportStmt

	// ExportAllStmts contains export * from statements.
	// These re-export all items from another module.
	ExportAllStmts []*dts_parser.ExportAllStmt

	// ExportAsNamespace contains the export as namespace statement (if present).
	// Only one such statement is valid per file (UMD pattern).
	ExportAsNamespace *dts_parser.ExportAsNamespaceStmt
}

// parseTypeDef parses a .d.ts file and classifies its contents
// using the FileClassification system from dts_parser/classifier.go.
func parseTypeDef(filename string) (*ParsedTypeDef, error) {
	// Read the file
	contents, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading DTS file: %s\n", err.Error())
		return nil, err
	}

	source := &ast.Source{
		Path:     filename,
		Contents: string(contents),
		ID:       int(nextSourceID.Add(1)),
	}

	// Parse the module
	parser := dts_parser.NewDtsParser(source)
	dtsModule, parseErrors := parser.ParseModule()

	if len(parseErrors) > 0 {
		fmt.Fprintf(os.Stderr, "Errors parsing DTS module:\n")
		for _, parseErr := range parseErrors {
			fmt.Fprintf(os.Stderr, "- %s\n", parseErr)
		}
		return nil, fmt.Errorf("failed to parse DTS module %s: %d errors", filename, len(parseErrors))
	}

	// Classify the file using the FileClassification system
	classification := dts_parser.ClassifyDTSFile(dtsModule)

	// Parse path reference directives from the file content
	pathRefs := parsePathRefsFromContent(string(contents))

	result := &ParsedTypeDef{
		NamedModules:      make(map[string]*ast.Module),
		Imports:           classification.Imports,
		PathRefs:          pathRefs,
		NamedExports:      classification.NamedExports,
		ExportAllStmts:    classification.ExportAllStmts,
		ExportAsNamespace: classification.ExportAsNamespace,
	}

	// Process package declarations (both exported and non-exported)
	if len(classification.PackageDecls) > 0 {
		pkgDtsModule := &dts_parser.Module{
			Statements: classification.PackageDecls,
		}
		pkgAstModule, err := interop.ConvertModule(pkgDtsModule)
		if err != nil {
			return nil, fmt.Errorf("converting package declarations: %w", err)
		}
		pkgAstModule.Sources[source.ID] = source
		result.PackageModule = pkgAstModule
	}

	// Process global declarations
	if len(classification.GlobalDecls) > 0 {
		globalDtsModule := &dts_parser.Module{
			Statements: classification.GlobalDecls,
		}
		globalAstModule, err := interop.ConvertModule(globalDtsModule)
		if err != nil {
			return nil, fmt.Errorf("converting global declarations: %w", err)
		}
		globalAstModule.Sources[source.ID] = source
		result.GlobalModule = globalAstModule
	}

	// Process named modules
	for _, namedMod := range classification.NamedModules {
		namedDtsModule := &dts_parser.Module{
			Statements: namedMod.Decls,
		}
		namedAstModule, err := interop.ConvertModule(namedDtsModule)
		if err != nil {
			return nil, fmt.Errorf("converting named module %s: %w", namedMod.ModuleName, err)
		}
		namedAstModule.Sources[source.ID] = source
		result.NamedModules[namedMod.ModuleName] = namedAstModule
	}

	return result, nil
}

// LoadedPackage represents a loaded npm package with its resolved file path.
type LoadedPackage struct {
	Namespace *type_system.Namespace
	FilePath  string
}

// InferredPackage contains the results of processing a LoadedPackageResult.
type InferredPackage struct {
	// PkgNs is the package namespace with imported namespaces already added
	PkgNs *type_system.Namespace
	// PkgCtx is the context for inferring into PkgNs
	PkgCtx Context
}

// inferParsedTypeDef handles common .d.ts processing:
// 1. Loads path-referenced files
// 2. Loads transitive import dependencies
// 3. Creates package namespace with imported namespaces
// 4. Processes global augmentations into GlobalScope (with imports visible)
// 5. Infers PackageModule into the package namespace (if present)
// 6. Processes export statements (local exports, re-exports, export as namespace)
//
// Callers are responsible for:
// - Inferring NamedModules into the returned context (if needed)
// - Registering the namespace in PackageRegistry
// - Any package-specific post-processing
func (c *Checker) inferParsedTypeDef(
	ctx Context,
	dtsFilePath string,
	parsedTypeDef *ParsedTypeDef,
) (*InferredPackage, []Error) {
	var errors []Error

	// 1. Process path references (/// <reference path="..." />)
	for _, ref := range parsedTypeDef.PathRefs {
		refPath := filepath.Join(filepath.Dir(dtsFilePath), ref)
		if !c.PackageRegistry.Has(refPath) && !c.PackageRegistry.IsInProgress(refPath) {
			c.PackageRegistry.MarkInProgress(refPath)
			refErrors := c.loadPathReferencedFile(refPath)
			errors = append(errors, refErrors...)
		}
	}

	// 2. Process imports to load transitive dependencies
	// All imports are resolved and loaded (including side-effect imports like `import "foo"`),
	// but only namespace imports (`import * as X from "pkg"`) are bound to importedNamespaces.
	importedNamespaces := make(map[string]*type_system.Namespace)
	for _, dtsImport := range parsedTypeDef.Imports {
		depTypesPath, resolveErr := resolveDtsImport(dtsFilePath, dtsImport)
		if resolveErr != nil {
			errors = append(errors, &GenericError{
				message: fmt.Sprintf("Could not resolve import %s in %s: %s",
					dtsImport.From, dtsFilePath, resolveErr.Error()),
				span: DEFAULT_SPAN,
			})
			continue
		}

		// Check if already loaded (or in-progress)
		if depNs, found := c.PackageRegistry.Lookup(depTypesPath); found {
			if depNs == nil {
				continue // In-progress (cycle)
			}
			// Only bind to importedNamespaces for namespace imports
			if dtsImport.NamespaceAs != nil {
				importedNamespaces[dtsImport.NamespaceAs.Name] = filterExportedNamespace(depNs)
			}
			continue
		}

		// Load the dependency package
		depPkg, depErrors := c.loadPackageFromPath(ctx, depTypesPath, dtsImport.From, DEFAULT_SPAN)
		errors = append(errors, depErrors...)

		// Only bind to importedNamespaces for namespace imports
		if dtsImport.NamespaceAs != nil && depPkg != nil && depPkg.Namespace != nil {
			importedNamespaces[dtsImport.NamespaceAs.Name] = filterExportedNamespace(depPkg.Namespace)
		}
	}

	// 3. Create package namespace with imported namespaces
	// This must happen before GlobalModule inference so that imports are visible
	// to global augmentations that may reference imported types.
	pkgNs := type_system.NewNamespace()
	for alias, ns := range importedNamespaces {
		if err := pkgNs.SetNamespace(alias, ns); err != nil {
			errors = append(errors, &GenericError{
				message: fmt.Sprintf("Failed to add imported namespace %s: %s", alias, err.Error()),
				span:    DEFAULT_SPAN,
			})
		}
	}

	pkgScope := &Scope{
		Parent:    c.GlobalScope,
		Namespace: pkgNs,
	}
	pkgCtx := Context{
		Scope:      pkgScope,
		IsAsync:    false,
		IsPatMatch: false,
	}

	// 4. Process global augmentations into GlobalScope
	// Use a scope that has pkgScope as parent (so imports are visible for lookups)
	// but writes declarations to GlobalScope.Namespace.
	if parsedTypeDef.GlobalModule != nil && c.GlobalScope != nil {
		globalAugScope := &Scope{
			Parent:    pkgScope,                // imports visible through parent chain
			Namespace: c.GlobalScope.Namespace, // declarations go to global scope
		}
		globalCtx := Context{
			Scope:      globalAugScope,
			IsAsync:    false,
			IsPatMatch: false,
		}
		globalErrors := c.InferModule(globalCtx, parsedTypeDef.GlobalModule)
		errors = append(errors, globalErrors...)
	}

	// 5. Infer PackageModule into the package namespace (if present)
	if parsedTypeDef.PackageModule != nil {
		pkgErrors := c.InferModule(pkgCtx, parsedTypeDef.PackageModule)
		errors = append(errors, pkgErrors...)
	}

	// 6. Process export statements (local exports, re-exports, export as namespace)
	exportErrors := c.ProcessExportStatements(ctx, dtsFilePath, parsedTypeDef, pkgNs)
	errors = append(errors, exportErrors...)

	return &InferredPackage{PkgNs: pkgNs, PkgCtx: pkgCtx}, errors
}

// ProcessExportStatements handles export statements from a .d.ts file:
// - Local named exports: export { foo } - marks existing declarations as exported
// - Re-exports: export { foo } from "bar" - loads "bar" and copies specified items
// - Export all: export * from "bar" - loads "bar" and merges all exports
// - Export as namespace: export as namespace MyLib - UMD pattern, adds to global scope
func (c *Checker) ProcessExportStatements(
	ctx Context,
	dtsFilePath string,
	parsedTypeDef *ParsedTypeDef,
	pkgNs *type_system.Namespace,
) []Error {
	var errors []Error

	// Process named export statements (both local and re-exports)
	for _, namedExport := range parsedTypeDef.NamedExports {
		exportErrors := c.processNamedExport(ctx, dtsFilePath, pkgNs, namedExport)
		errors = append(errors, exportErrors...)
	}

	// Process export * from statements
	for _, exportAll := range parsedTypeDef.ExportAllStmts {
		allErrors := c.processExportAll(ctx, dtsFilePath, pkgNs, exportAll)
		errors = append(errors, allErrors...)
	}

	// Process export as namespace (UMD pattern)
	if parsedTypeDef.ExportAsNamespace != nil {
		umdErrors := c.processExportAsNamespace(pkgNs, parsedTypeDef.ExportAsNamespace)
		errors = append(errors, umdErrors...)
	}

	return errors
}

// processNamedExport handles named exports:
// - Local exports: `export { foo }` or `export { foo as bar }` (stmt.From == "")
// - Re-exports: `export { foo } from "bar"` or `export { foo as baz } from "bar"` (stmt.From != "")
func (c *Checker) processNamedExport(
	ctx Context,
	sourceFilePath string,
	pkgNs *type_system.Namespace,
	stmt *dts_parser.NamedExportStmt,
) []Error {
	var errors []Error
	var srcNs *type_system.Namespace

	if stmt.From == "" {
		// Local export: source is the package namespace itself
		srcNs = pkgNs
	} else {
		// Re-export: resolve and load the source module
		resolvedPath, resolveErr := c.resolveExportModulePath(sourceFilePath, stmt.From)
		if resolveErr != nil {
			errors = append(errors, &GenericError{
				message: fmt.Sprintf("Cannot resolve re-export from '%s': %s",
					stmt.From, resolveErr.Error()),
				span: stmt.Span(),
			})
			return errors
		}

		depPkg, depErrors := c.loadPackageFromPath(ctx, resolvedPath, stmt.From, stmt.Span())
		errors = append(errors, depErrors...)

		if depPkg == nil || depPkg.Namespace == nil {
			return errors
		}

		srcNs = filterExportedNamespace(depPkg.Namespace)
	}

	// Process each specifier
	for _, spec := range stmt.Specifiers {
		localName := spec.Local.Name
		exportedName := spec.Exported.Name

		found := false

		// Copy value bindings (skip for type-only exports)
		if !stmt.TypeOnly {
			if binding, ok := srcNs.Values[localName]; ok {
				newBinding := &type_system.Binding{
					Source:   binding.Source,
					Type:     binding.Type,
					Mutable:  binding.Mutable,
					Exported: true,
				}
				pkgNs.Values[exportedName] = newBinding
				found = true
			}
		}

		// Copy type bindings (always allowed for both regular and type-only exports)
		if typeAlias, ok := srcNs.Types[localName]; ok {
			newAlias := &type_system.TypeAlias{
				Type:       typeAlias.Type,
				TypeParams: typeAlias.TypeParams,
				Exported:   true,
			}
			pkgNs.Types[exportedName] = newAlias
			found = true
		}

		// Copy namespace bindings (skip for type-only exports)
		if !stmt.TypeOnly {
			if ns, ok := srcNs.Namespaces[localName]; ok {
				pkgNs.Namespaces[exportedName] = ns
				found = true
			}
		}

		if !found && !stmt.TypeOnly {
			var message string
			if stmt.From == "" {
				message = fmt.Sprintf("Cannot export '%s': not found in module", localName)
			} else {
				message = fmt.Sprintf("Module '%s' has no export named '%s'", stmt.From, localName)
			}
			errors = append(errors, &GenericError{
				message: message,
				span:    spec.Span(),
			})
		}
	}

	return errors
}

// processExportAll handles `export * from "bar"` or `export * as ns from "bar"`.
// For `export *`, it merges all exports from the source module.
// For `export * as ns`, it creates a namespace binding.
func (c *Checker) processExportAll(
	ctx Context,
	sourceFilePath string,
	pkgNs *type_system.Namespace,
	stmt *dts_parser.ExportAllStmt,
) []Error {
	var errors []Error

	// Resolve the module path
	resolvedPath, resolveErr := c.resolveExportModulePath(sourceFilePath, stmt.From)
	if resolveErr != nil {
		errors = append(errors, &GenericError{
			message: fmt.Sprintf("Cannot resolve export * from '%s': %s",
				stmt.From, resolveErr.Error()),
			span: stmt.Span(),
		})
		return errors
	}

	// Load the dependency
	depPkg, depErrors := c.loadPackageFromPath(ctx, resolvedPath, stmt.From, stmt.Span())
	errors = append(errors, depErrors...)

	if depPkg == nil || depPkg.Namespace == nil {
		return errors
	}

	depNs := filterExportedNamespace(depPkg.Namespace)

	if stmt.AsName != nil {
		// export * as ns from "module" - create namespace binding
		// Skip for type-only exports (namespaces are runtime constructs)
		if !stmt.TypeOnly {
			if err := pkgNs.SetNamespace(stmt.AsName.Name, depNs); err != nil {
				errors = append(errors, &GenericError{
					message: fmt.Sprintf("Cannot create namespace '%s': %s",
						stmt.AsName.Name, err.Error()),
					span: stmt.Span(),
				})
			}
		}
	} else {
		// export * from "module" - merge all exports
		// "First export wins" semantics: if a name already exists, we keep the first one.
		// This is by design - TypeScript makes ambiguous re-exports unavailable rather than
		// erroring at declaration time. Our approach is simpler: the first export wins,
		// which is consistent and avoids silent incorrect behavior. A more complete
		// implementation could track ambiguous names and error only when they're used.

		// Merge values (skip for type-only exports)
		if !stmt.TypeOnly {
			for name, binding := range depNs.Values {
				if _, exists := pkgNs.Values[name]; !exists {
					newBinding := &type_system.Binding{
						Source:   binding.Source,
						Type:     binding.Type,
						Mutable:  binding.Mutable,
						Exported: true,
					}
					pkgNs.Values[name] = newBinding
				}
			}
		}

		// Merge types (always allowed for both regular and type-only exports)
		for name, typeAlias := range depNs.Types {
			if _, exists := pkgNs.Types[name]; !exists {
				newAlias := &type_system.TypeAlias{
					Type:       typeAlias.Type,
					TypeParams: typeAlias.TypeParams,
					Exported:   true,
				}
				pkgNs.Types[name] = newAlias
			}
		}

		// Merge namespaces (skip for type-only exports)
		if !stmt.TypeOnly {
			for name, ns := range depNs.Namespaces {
				if _, exists := pkgNs.Namespaces[name]; !exists {
					pkgNs.Namespaces[name] = ns
				}
			}
		}
	}

	return errors
}

// processExportAsNamespace handles `export as namespace MyLib` (UMD pattern).
// This makes the package available as a global namespace.
func (c *Checker) processExportAsNamespace(
	pkgNs *type_system.Namespace,
	stmt *dts_parser.ExportAsNamespaceStmt,
) []Error {
	var errors []Error

	// UMD pattern: make the package available as a global namespace
	if c.GlobalScope != nil && c.GlobalScope.Namespace != nil {
		if err := c.GlobalScope.Namespace.SetNamespace(stmt.Name.Name, pkgNs); err != nil {
			errors = append(errors, &GenericError{
				message: fmt.Sprintf("Cannot create global namespace '%s': %s",
					stmt.Name.Name, err.Error()),
				span: stmt.Span(),
			})
		}
	}

	return errors
}

// resolveExportModulePath resolves a module path from an export statement.
// Handles both relative paths (./foo, ../bar) and package names (lodash).
func (c *Checker) resolveExportModulePath(sourceFilePath string, modulePath string) (string, error) {
	if isRelativeModulePath(modulePath) {
		// Relative path: resolve from source file directory
		return resolveRelativeDtsPath(sourceFilePath, modulePath), nil
	}

	// Package import: use existing resolution
	importDecl := &dts_parser.ImportDecl{From: modulePath}
	return resolveDtsImport(sourceFilePath, importDecl)
}

// isRelativeModulePath checks if a module path is relative (starts with ./ or ../)
func isRelativeModulePath(path string) bool {
	return strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../")
}

// resolveRelativeDtsPath resolves a relative .d.ts path from a source file.
// It tries multiple resolution strategies (file.d.ts, dir/index.d.ts) and returns
// the first path that exists. If no path exists, it returns the primary candidate
// path (file.d.ts) - the caller should handle the "file not found" case since
// the path might be pre-registered in the PackageRegistry.
func resolveRelativeDtsPath(sourceFilePath string, relativePath string) string {
	sourceDir := filepath.Dir(sourceFilePath)

	// Remove .js extension if present (TypeScript allows importing .js)
	relativePath = strings.TrimSuffix(relativePath, ".js")

	// Try with .d.ts extension first
	if !strings.HasSuffix(relativePath, ".d.ts") {
		dtsPath := filepath.Join(sourceDir, relativePath+".d.ts")
		if _, err := os.Stat(dtsPath); err == nil {
			return dtsPath
		}

		// Try as directory with index.d.ts
		indexPath := filepath.Join(sourceDir, relativePath, "index.d.ts")
		if _, err := os.Stat(indexPath); err == nil {
			return indexPath
		}

		// Return primary candidate path - caller will handle "not found"
		// (path might exist in PackageRegistry even if not on disk)
		return dtsPath
	}

	return filepath.Join(sourceDir, relativePath)
}

// loadPathReferencedFile loads a file referenced via /// <reference path="..." />
// These files typically contain global interface definitions.
// The caller must have already called MarkInProgress(filePath) before calling this function.
// This function will update the registry with the loaded namespace when complete.
func (c *Checker) loadPathReferencedFile(filePath string) []Error {
	var errors []Error

	parsedTypeDef, loadErr := parseTypeDef(filePath)
	if loadErr != nil {
		// Remove the in-progress entry so later loads can retry and report the real failure.
		delete(c.PackageRegistry.packages, filePath)
		return []Error{&GenericError{
			message: "Could not load referenced file " + filePath + ": " + loadErr.Error(),
			span:    DEFAULT_SPAN,
		}}
	}

	// Use inferParsedTypeDef to handle common processing:
	// - Path references (nested /// <reference path="..." />)
	// - Transitive imports
	// - GlobalModule into GlobalScope
	// - PackageModule into pkgNs
	ctx := Context{
		Scope:      c.GlobalScope,
		IsAsync:    false,
		IsPatMatch: false,
	}
	processed, processErrors := c.inferParsedTypeDef(ctx, filePath, parsedTypeDef)
	errors = append(errors, processErrors...)

	// Path-referenced files define global types, so also process PackageModule into GlobalScope.
	// (inferParsedTypeDef only processes PackageModule into pkgNs, not GlobalScope)
	if parsedTypeDef.PackageModule != nil && c.GlobalScope != nil {
		globalCtx := Context{
			Scope: &Scope{
				Parent:    processed.PkgCtx.Scope,
				Namespace: c.GlobalScope.Namespace,
			},
			IsAsync:    false,
			IsPatMatch: false,
		}
		globalErrors := c.InferModule(globalCtx, parsedTypeDef.PackageModule)
		errors = append(errors, globalErrors...)
	}

	// Update the registry with the file's namespace (replacing the in-progress sentinel)
	if updateErr := c.PackageRegistry.Update(filePath, processed.PkgNs); updateErr != nil {
		errors = append(errors, &GenericError{
			message: fmt.Sprintf("Failed to update package registry for %s: %s", filePath, updateErr.Error()),
			span:    DEFAULT_SPAN,
		})
	}

	return errors
}

// loadPackageForImport loads a package by name, checking the registry first.
// If not in registry, loads from file system, infers into a namespace,
// registers it, and handles global augmentations.
//
// Returns the package namespace, resolved file path, and any errors.
func (c *Checker) loadPackageForImport(ctx Context, importStmt *ast.ImportStmt) (*LoadedPackage, []Error) {
	// Step 1: Resolve import to file path
	dtsFilePath, resolveErr := resolveImport(ctx, importStmt)
	if resolveErr != nil {
		return nil, []Error{resolveErr}
	}

	fmt.Fprintf(os.Stderr, "Resolved import %s to type definitions at %s\n",
		importStmt.PackageName, dtsFilePath)

	return c.loadPackageFromPath(ctx, dtsFilePath, importStmt.PackageName, importStmt.Span())
}

// loadPackageFromPath loads a package from a resolved .d.ts file path.
// This is the internal helper that does the actual loading work.
// packageName is used for logging and named module lookup.
// span is used for error messages (can be DEFAULT_SPAN if not available).
func (c *Checker) loadPackageFromPath(ctx Context, dtsFilePath string, packageName string, span ast.Span) (*LoadedPackage, []Error) {
	errors := []Error{}

	// Step 2: Check if already loaded (using file path as key)
	if pkgNs, found := c.PackageRegistry.Lookup(dtsFilePath); found {
		if pkgNs == nil {
			// Package is in-progress (being loaded) - this is a cycle
			// Return nil to signal the cycle, but no error - cycles are expected
			return nil, nil
		}
		fmt.Fprintf(os.Stderr, "Package %s already loaded from %s\n",
			packageName, dtsFilePath)
		return &LoadedPackage{Namespace: pkgNs, FilePath: dtsFilePath}, nil
	}

	// Mark as "in-progress" before loading to prevent A→B→A recursion cycles
	// The sentinel will be replaced with the real namespace after loading via Update()
	c.PackageRegistry.MarkInProgress(dtsFilePath)

	// Step 3: Load and classify the .d.ts file
	parsedTypeDef, loadErr := parseTypeDef(dtsFilePath)
	if loadErr != nil {
		// Clean up sentinel so the package can be retried
		delete(c.PackageRegistry.packages, dtsFilePath) // Need to expose a Remove method
		return nil, []Error{&GenericError{
			message: "Could not load type definitions for module import: " + packageName,
			span:    span,
		}}
	}

	// Process common parts: path refs, imports, global module, package scope creation
	processed, processErrors := c.inferParsedTypeDef(ctx, dtsFilePath, parsedTypeDef)
	errors = append(errors, processErrors...)

	// Step 4: Process named modules and register them
	// Track namespaces by module name so we can reuse them in step 6
	// Named modules use processed.PkgCtx.Scope as parent so they can resolve
	// file-level imports (e.g., `import * as CSS from 'csstype'`).
	namedModuleNamespaces := make(map[string]*type_system.Namespace)
	for moduleName, namedModule := range parsedTypeDef.NamedModules {
		moduleNs := type_system.NewNamespace()
		moduleScope := &Scope{
			Parent:    processed.PkgCtx.Scope,
			Namespace: moduleNs,
		}
		moduleCtx := Context{
			Scope:      moduleScope,
			IsAsync:    false,
			IsPatMatch: false,
		}

		moduleErrors := c.InferModule(moduleCtx, namedModule)
		if len(moduleErrors) > 0 {
			errors = append(errors, moduleErrors...)
			continue
		}

		// Track the namespace for potential use in step 6
		namedModuleNamespaces[moduleName] = moduleNs

		// Register named module with a composite key: filePath + "#" + moduleName
		namedModuleKey := dtsFilePath + "#" + moduleName
		if regErr := c.PackageRegistry.Register(namedModuleKey, moduleNs); regErr != nil {
			errors = append(errors, &GenericError{
				message: fmt.Sprintf("Failed to register named module %s: %s", moduleName, regErr.Error()),
				span:    DEFAULT_SPAN,
			})
		}
	}

	// Step 5: Determine which module to use as the package namespace
	var pkgNs *type_system.Namespace

	// Check if the file has any module-level content (declarations or export statements)
	hasModuleContent := parsedTypeDef.PackageModule != nil ||
		len(parsedTypeDef.NamedExports) > 0 ||
		len(parsedTypeDef.ExportAllStmts) > 0 ||
		parsedTypeDef.ExportAsNamespace != nil

	if hasModuleContent {
		// File has top-level exports or re-exports - use the namespace from inferParsedTypeDef
		// (which already has imported namespaces added, PackageModule inferred, and exports processed)
		pkgNs = processed.PkgNs
	} else if ns, ok := namedModuleNamespaces[packageName]; ok {
		// Named module matching the package name - use the namespace from step 5
		pkgNs = ns
	} else if parsedTypeDef.GlobalModule != nil {
		// No top-level exports and no matching named module - use the global module
		// Global augmentations are already applied to c.GlobalScope in Step 4.
		// Use an empty namespace so we don't expose all globals as package exports.
		pkgNs = type_system.NewNamespace()
	} else {
		delete(c.PackageRegistry.packages, dtsFilePath)
		return nil, []Error{&GenericError{
			message: "Type definitions for module import do not contain expected module: " + packageName,
			span:    span,
		}}
	}

	// Step 7: Update the registry with the real namespace (replacing the sentinel)
	// Note: We marked as in-progress earlier to prevent cycles, now we update it
	if updateErr := c.PackageRegistry.Update(dtsFilePath, pkgNs); updateErr != nil {
		errors = append(errors, &GenericError{
			message: fmt.Sprintf("Failed to update package registry for %s: %s", packageName, updateErr.Error()),
			span:    span,
		})
	}

	return &LoadedPackage{Namespace: pkgNs, FilePath: dtsFilePath}, errors
}

// inferImport processes an import statement, loading the package and binding
// the imported symbols to the current scope.
//
// TypeScript modules come in a few different flavours:
//  1. no `export` statements or `module` declarations means all declarations are global
//  2. `module` declarations can appear in a type .d.ts file, they can either be named or global, e.g.
//     a. `declare module "modulename" { ... }`
//     b. `declare global { ... }`
//  3. `export` statements mean the .d.ts file is a module
//
// For type 2, we can use the name of the module declaration to determine the package
// name.  For type 3, we need to know what npm package the .d.ts file belongs to.
func (c *Checker) inferImport(ctx Context, importStmt *ast.ImportStmt) []Error {
	errors := []Error{}

	// First, check if the package is already registered in the PackageRegistry.
	// This allows for pre-loaded packages (e.g., for testing or caching).
	// Note: We check by package name first for backwards compatibility with tests
	// that register packages by name rather than file path.
	if pkgNs, found := c.PackageRegistry.Lookup(importStmt.PackageName); found {
		if pkgNs == nil {
			// Package is in-progress (cycle) - return without binding
			// This should be rare when looking up by package name
			return errors
		}
		errors = append(errors, c.bindImportSpecifiers(ctx, importStmt, pkgNs)...)
		return errors
	}

	// Load the package from file system (or registry by file path)
	loadedPkg, loadErrors := c.loadPackageForImport(ctx, importStmt)
	errors = append(errors, loadErrors...)
	if loadedPkg == nil || loadedPkg.Namespace == nil {
		if len(errors) == 0 {
			errors = append(errors, &GenericError{
				message: "Failed to load package: " + importStmt.PackageName,
				span:    importStmt.Span(),
			})
		}
		return errors
	}

	// Bind import specifiers to the current scope
	errors = append(errors, c.bindImportSpecifiers(ctx, importStmt, loadedPkg.Namespace)...)

	return errors
}

// bindImportSpecifiers processes import specifiers and binds them to the current scope.
// This handles both namespace imports (import * as alias) and named imports (import { name }).
func (c *Checker) bindImportSpecifiers(ctx Context, importStmt *ast.ImportStmt, pkgNs *type_system.Namespace) []Error {
	errors := []Error{}

	for _, specifier := range importStmt.Specifiers {
		if specifier.Name == "*" {
			// Namespace import: import * as alias from "pkg"
			// Filter to only include exported items
			filteredNs := filterExportedNamespace(pkgNs)
			if err := ctx.Scope.Namespace.SetNamespace(specifier.Alias, filteredNs); err != nil {
				errors = append(errors, &GenericError{
					message: fmt.Sprintf("Cannot bind namespace %q: %s", specifier.Alias, err.Error()),
					span:    importStmt.Span(),
				})
			}
		} else {
			// Named import: import { name } from "pkg" or import { name as alias } from "pkg"
			found := false
			localName := specifier.Name
			if specifier.Alias != "" {
				localName = specifier.Alias
			}

			// Check for value binding (only if exported)
			if binding, ok := pkgNs.Values[specifier.Name]; ok && binding.Exported {
				ctx.Scope.Namespace.Values[localName] = binding
				found = true
			}

			// Check for type binding (only if exported)
			if typeAlias, ok := pkgNs.Types[specifier.Name]; ok && typeAlias.Exported {
				ctx.Scope.Namespace.Types[localName] = typeAlias
				found = true
			}

			// Check for namespace binding
			if ns, ok := pkgNs.GetNamespace(specifier.Name); ok {
				if err := ctx.Scope.Namespace.SetNamespace(localName, ns); err != nil {
					errors = append(errors, &GenericError{
						message: fmt.Sprintf("Cannot bind namespace %q: %s", localName, err.Error()),
						span:    importStmt.Span(),
					})
				}
				found = true
			}

			if !found {
				errors = append(errors, &GenericError{
					message: fmt.Sprintf("Package %q has no export named %q",
						importStmt.PackageName, specifier.Name),
					span: importStmt.Span(),
				})
			}
		}
	}

	// Log imported specifiers for debugging
	for _, specifier := range importStmt.Specifiers {
		if specifier.Name == "*" {
			fmt.Fprintf(os.Stderr, "Imported namespace %q from module %s\n",
				specifier.Alias, importStmt.PackageName)
			continue
		}
		localName := specifier.Name
		if specifier.Alias != "" {
			localName = specifier.Alias
		}
		fmt.Fprintf(os.Stderr, "Imported %q as %q from module %s\n",
			specifier.Name, localName, importStmt.PackageName)
	}

	return errors
}

// filterExportedNamespace creates a new namespace containing only exported items from the original.
// This is used for namespace imports (import * as alias) to hide non-exported internals.
func filterExportedNamespace(ns *type_system.Namespace) *type_system.Namespace {
	filtered := type_system.NewNamespace()

	for name, binding := range ns.Values {
		if binding.Exported {
			filtered.Values[name] = binding
		}
	}

	for name, typeAlias := range ns.Types {
		if typeAlias.Exported {
			filtered.Types[name] = typeAlias
		}
	}

	// Recursively filter nested namespaces
	for name, subNs := range ns.Namespaces {
		filteredSub := filterExportedNamespace(subNs)
		// Only include if the filtered namespace has any items
		if len(filteredSub.Values) > 0 || len(filteredSub.Types) > 0 || len(filteredSub.Namespaces) > 0 {
			filtered.Namespaces[name] = filteredSub
		}
	}

	return filtered
}
