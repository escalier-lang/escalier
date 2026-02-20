package checker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/escalier-lang/escalier/internal/ast"
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

// getTypesFromPackageJson reads a package.json file and returns the types entry point path.
// Returns the full path to the types file, or an error if the package.json doesn't exist,
// can't be parsed, or doesn't have a types field.
func getTypesFromPackageJson(moduleDir string) (string, error) {
	pkgJsonPath := filepath.Join(moduleDir, "package.json")
	fmt.Fprintf(os.Stderr, "Reading package.json for module import at %s\n", pkgJsonPath)

	pkgJsonBytes, err := os.ReadFile(pkgJsonPath)
	if err != nil {
		return "", err
	}

	var pkgJsonMap map[string]any
	if err := json.Unmarshal(pkgJsonBytes, &pkgJsonMap); err != nil {
		return "", err
	}

	if typesField, ok := pkgJsonMap["types"]; ok {
		typesStr, isString := typesField.(string)
		if !isString {
			return "", fmt.Errorf("invalid types field")
		}
		return filepath.Join(moduleDir, typesStr), nil
	}

	return "", fmt.Errorf("no types field")
}

func resolveImport(ctx Context, importStmt *ast.ImportStmt) (string, Error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", &GenericError{message: "Could not get current working directory for import", span: importStmt.Span()}
	}

	packageJsonDir, found := findPackageJsonFile(cwd)
	if !found {
		return "", &GenericError{message: "Could not find package.json for import", span: importStmt.Span()}
	}

	// First, try to find types in the main package (node_modules/<pkg_name>)
	moduleDir := filepath.Join(packageJsonDir, "node_modules", importStmt.PackageName)
	if resolvedDir, err := resolveModuleDir(moduleDir); err == nil {
		if typesPath, err := getTypesFromPackageJson(resolvedDir); err == nil {
			return typesPath, nil
		}
	}

	// Fallback: try @types/<pkg_name> for packages that ship types separately
	// (e.g., @types/react for the react package)
	typesModuleDir := filepath.Join(packageJsonDir, "node_modules", "@types", importStmt.PackageName)
	if resolvedDir, err := resolveModuleDir(typesModuleDir); err == nil {
		if typesPath, err := getTypesFromPackageJson(resolvedDir); err == nil {
			return typesPath, nil
		}
	}

	return "", &GenericError{
		message: "Could not find types for module import: " + importStmt.PackageName +
			" (checked node_modules/" + importStmt.PackageName + " and node_modules/@types/" + importStmt.PackageName + ")",
		span: importStmt.Span(),
	}
}

// LoadedPackage represents a loaded npm package with its resolved file path.
type LoadedPackage struct {
	Namespace *type_system.Namespace
	FilePath  string
}

// loadPackageForImport loads a package by name, checking the registry first.
// If not in registry, loads from file system, infers into a namespace,
// registers it, and handles global augmentations.
//
// Returns the package namespace, resolved file path, and any errors.
func (c *Checker) loadPackageForImport(ctx Context, importStmt *ast.ImportStmt) (*LoadedPackage, []Error) {
	errors := []Error{}

	// Step 1: Resolve import to file path
	dtsFilePath, resolveErr := resolveImport(ctx, importStmt)
	if resolveErr != nil {
		return nil, []Error{resolveErr}
	}

	fmt.Fprintf(os.Stderr, "Resolved import %s to type definitions at %s\n",
		importStmt.PackageName, dtsFilePath)

	// Step 2: Check if already loaded (using file path as key)
	if pkgNs, found := c.PackageRegistry.Lookup(dtsFilePath); found {
		fmt.Fprintf(os.Stderr, "Package %s already loaded from %s\n",
			importStmt.PackageName, dtsFilePath)
		return &LoadedPackage{Namespace: pkgNs, FilePath: dtsFilePath}, nil
	}

	// Step 3: Load and classify the .d.ts file
	loadResult, loadErr := loadClassifiedTypeScriptModule(dtsFilePath)
	if loadErr != nil {
		return nil, []Error{&GenericError{
			message: "Could not load type definitions for module import: " + importStmt.PackageName,
			span:    importStmt.Span(),
		}}
	}

	// Step 4: Process global augmentations into GlobalScope
	if loadResult.GlobalModule != nil && c.GlobalScope != nil {
		globalCtx := Context{
			Scope:      c.GlobalScope,
			IsAsync:    false,
			IsPatMatch: false,
		}
		globalErrors := c.InferModule(globalCtx, loadResult.GlobalModule)
		if len(globalErrors) > 0 {
			for _, err := range globalErrors {
				fmt.Fprintf(os.Stderr, "Global augmentation error in %s: %s\n",
					dtsFilePath, err.Message())
			}
			// Surface global augmentation errors so users get diagnostics
			errors = append(errors, globalErrors...)
		}
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
			for _, err := range moduleErrors {
				fmt.Fprintf(os.Stderr, "Error inferring named module %s: %s\n",
					moduleName, err.Message())
			}
			errors = append(errors, moduleErrors...)
			continue
		}

		// Track the namespace for potential use in step 6
		namedModuleNamespaces[moduleName] = moduleNs

		// Register named module with a composite key: filePath + "#" + moduleName
		namedModuleKey := dtsFilePath + "#" + moduleName
		if regErr := c.PackageRegistry.Register(namedModuleKey, moduleNs); regErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to register named module %s: %s\n",
				moduleName, regErr.Error())
		}
	}

	// Step 6: Determine which module to use as the package namespace
	var pkgNs *type_system.Namespace

	if loadResult.PackageModule != nil {
		// File has top-level exports - use PackageModule
		pkgNs = type_system.NewNamespace()
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
	} else if ns, ok := namedModuleNamespaces[importStmt.PackageName]; ok {
		// Named module matching the package name - use the namespace from step 5
		pkgNs = ns
	} else if loadResult.GlobalModule != nil {
		// No top-level exports and no matching named module - use the global module
		// Global augmentations are already applied to c.GlobalScope in Step 4.
		// Use an empty namespace so we don't expose all globals as package exports.
		pkgNs = type_system.NewNamespace()
	} else {
		return nil, []Error{&GenericError{
			message: "Type definitions for module import do not contain expected module: " + importStmt.PackageName,
			span:    importStmt.Span(),
		}}
	}

	// Step 7: Register the package in the registry
	if regErr := c.PackageRegistry.Register(dtsFilePath, pkgNs); regErr != nil {
		// This shouldn't happen since we checked Lookup() above
		fmt.Fprintf(os.Stderr, "Warning: failed to register package %s: %s\n",
			importStmt.PackageName, regErr.Error())
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
			if err := ctx.Scope.Namespace.SetNamespace(specifier.Alias, pkgNs); err != nil {
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

			// Check for value binding
			if binding, ok := pkgNs.Values[specifier.Name]; ok {
				ctx.Scope.Namespace.Values[localName] = binding
				found = true
			}

			// Check for type binding
			if typeAlias, ok := pkgNs.Types[specifier.Name]; ok {
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
