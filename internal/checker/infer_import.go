package checker

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
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

// LoadedPackage represents a loaded npm package with its resolved file path.
type LoadedPackage struct {
	Namespace *type_system.Namespace
	FilePath  string
}

// loadPathReferencedFile loads a file referenced via /// <reference path="..." />
// These files typically contain global interface definitions.
// The caller must have already called MarkInProgress(filePath) before calling this function.
// This function will update the registry with the loaded namespace when complete.
func (c *Checker) loadPathReferencedFile(filePath string) []Error {
	var errors []Error

	loadResult, loadErr := loadClassifiedTypeScriptModule(filePath)
	if loadErr != nil {
		// Remove the in-progress entry so later loads can retry and report the real failure.
		delete(c.PackageRegistry.packages, filePath)
		return []Error{&GenericError{
			message: "Could not load referenced file " + filePath + ": " + loadErr.Error(),
			span:    DEFAULT_SPAN,
		}}
	}

	// Create a namespace for this file's types
	// Types are processed into both this namespace (for registry lookup) and GlobalScope (for global access)
	fileNs := type_system.NewNamespace()
	fileScope := &Scope{
		Parent:    c.GlobalScope,
		Namespace: fileNs,
	}
	fileCtx := Context{
		Scope:      fileScope,
		IsAsync:    false,
		IsPatMatch: false,
	}

	// Process nested path references (/// <reference path="..." />)
	// These files may themselves reference other .d.ts files
	pathRefs, pathErr := parsePathReferenceDirectives(filePath)
	if pathErr == nil && len(pathRefs) > 0 {
		dtsDir := filepath.Dir(filePath)
		for _, ref := range pathRefs {
			refPath := filepath.Join(dtsDir, ref)
			// Check if already processed (avoid duplicates)
			if _, found := c.PackageRegistry.Lookup(refPath); !found {
				// Mark as in-progress to prevent duplicate inference from recursive or shared reference paths
				c.PackageRegistry.MarkInProgress(refPath)

				refErrors := c.loadPathReferencedFile(refPath)
				errors = append(errors, refErrors...)
			}
		}
	}

	// Process global declarations into both the file namespace and GlobalScope
	if loadResult.GlobalModule != nil {
		// First, process into the file namespace for registry lookup
		errors = append(errors, c.InferModule(fileCtx, loadResult.GlobalModule)...)

		// Also process into GlobalScope for global access
		globalCtx := Context{
			Scope:      c.GlobalScope,
			IsAsync:    false,
			IsPatMatch: false,
		}
		// Note: We ignore errors here since we already reported them from the fileCtx processing
		_ = c.InferModule(globalCtx, loadResult.GlobalModule)
	}

	// Process package declarations as globals (for files like global.d.ts)
	if loadResult.PackageModule != nil {
		// First, process into the file namespace for registry lookup
		errors = append(errors, c.InferModule(fileCtx, loadResult.PackageModule)...)

		// Also process into GlobalScope for global access
		globalCtx := Context{
			Scope:      c.GlobalScope,
			IsAsync:    false,
			IsPatMatch: false,
		}
		// Note: We ignore errors here since we already reported them from the fileCtx processing
		_ = c.InferModule(globalCtx, loadResult.PackageModule)
	}

	// Update the registry with the file's namespace (replacing the in-progress sentinel)
	if updateErr := c.PackageRegistry.Update(filePath, fileNs); updateErr != nil {
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
	loadResult, loadErr := loadClassifiedTypeScriptModule(dtsFilePath)
	if loadErr != nil {
		// Clean up sentinel so the package can be retried
		delete(c.PackageRegistry.packages, dtsFilePath) // Need to expose a Remove method
		return nil, []Error{&GenericError{
			message: "Could not load type definitions for module import: " + packageName,
			span:    span,
		}}
	}

	// Step 3.5: Process path references (/// <reference path="..." />)
	// Path references are supposed to appear before import statements, so we
	// process them before imports.
	// These files typically contain global interface definitions (e.g., global.d.ts)
	pathRefs, pathErr := parsePathReferenceDirectives(dtsFilePath)
	if pathErr == nil && len(pathRefs) > 0 {
		dtsDir := filepath.Dir(dtsFilePath)
		for _, ref := range pathRefs {
			refPath := filepath.Join(dtsDir, ref)
			// Check registry status:
			// - Has() returns true only for fully loaded packages
			// - IsInProgress() returns true for packages currently being loaded
			// Only load if not found AND not in-progress
			if !c.PackageRegistry.Has(refPath) && !c.PackageRegistry.IsInProgress(refPath) {
				// Mark as in-progress to prevent duplicate inference from recursive or shared reference paths
				c.PackageRegistry.MarkInProgress(refPath)

				refErrors := c.loadPathReferencedFile(refPath)
				errors = append(errors, refErrors...)
			}
		}
	}

	// Step 3.6: Process imports from the .d.ts file to load transitive dependencies
	// This creates a map of namespace alias -> namespace for imports like "import * as CSS from 'csstype'"
	importedNamespaces := make(map[string]*type_system.Namespace)
	for _, dtsImport := range loadResult.Imports {
		// Only handle namespace imports for now (import * as Alias from "pkg")
		if dtsImport.NamespaceAs != nil {
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
					// Dependency is in-progress (cycle) - skip it
					continue
				}
				importedNamespaces[dtsImport.NamespaceAs.Name] = filterExportedNamespace(depNs)
				continue
			}

			// Recursively load the dependency package using the resolved path directly
			// This avoids double resolution and ensures we use the correct path
			depPkg, depErrors := c.loadPackageFromPath(ctx, depTypesPath, dtsImport.From, DEFAULT_SPAN)
			errors = append(errors, depErrors...)
			if depPkg != nil && depPkg.Namespace != nil {
				importedNamespaces[dtsImport.NamespaceAs.Name] = filterExportedNamespace(depPkg.Namespace)
			}
		}
		// TODO: Handle named imports (import { A, B } from "pkg") if needed
	}

	// Step 4: Process global augmentations into GlobalScope
	if loadResult.GlobalModule != nil && c.GlobalScope != nil {
		globalCtx := Context{
			Scope:      c.GlobalScope,
			IsAsync:    false,
			IsPatMatch: false,
		}
		globalErrors := c.InferModule(globalCtx, loadResult.GlobalModule)
		errors = append(errors, globalErrors...)
	}

	// Step 5: Process named modules and register them
	// Track namespaces by module name so we can reuse them in step 6
	namedModuleNamespaces := make(map[string]*type_system.Namespace)
	for moduleName, namedModule := range loadResult.NamedModules {
		moduleNs := type_system.NewNamespace()
		moduleScope := &Scope{
			Parent:    c.GlobalScope,
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

	// Step 6: Determine which module to use as the package namespace
	var pkgNs *type_system.Namespace

	if loadResult.PackageModule != nil {
		// File has top-level exports - use PackageModule
		pkgNs = type_system.NewNamespace()

		// Add imported namespaces to the package namespace before inferring
		// This makes types like CSS.Properties available when resolving type references
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

		pkgErrors := c.InferModule(pkgCtx, loadResult.PackageModule)
		errors = append(errors, pkgErrors...)
	} else if ns, ok := namedModuleNamespaces[packageName]; ok {
		// Named module matching the package name - use the namespace from step 5
		pkgNs = ns
	} else if loadResult.GlobalModule != nil {
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
