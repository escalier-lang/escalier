package checker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/escalier-lang/escalier/internal/ast"
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

func resolveImport(ctx Context, importStmt *ast.ImportStmt) (string, Error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", &GenericError{message: "Could not get current working directory for import", span: importStmt.Span()}
	}

	packageJsonDir, found := findPackageJsonFile(cwd)

	if !found {
		return "", &GenericError{message: "Could not find package.json for import", span: importStmt.Span()}
	}

	moduleDir := filepath.Join(packageJsonDir, "node_modules", importStmt.PackageName)

	// Check if moduleDir is a symlink
	fileInfo, err := os.Lstat(moduleDir)

	if err != nil {
		return "", &GenericError{message: "Could not locate module for import: " + importStmt.PackageName, span: importStmt.Span()}
	}

	if fileInfo.Mode()&os.ModeSymlink != 0 {
		// Resolve the symlink
		resolvedPath, err := os.Readlink(moduleDir)
		if err != nil {
			return "", &GenericError{message: "Could not resolve symlink for module import: " + importStmt.PackageName, span: importStmt.Span()}
		}
		if filepath.IsAbs(resolvedPath) {
			moduleDir = resolvedPath
		} else {
			moduleDir = filepath.Join(packageJsonDir, "node_modules", resolvedPath)
		}
	}

	// Read package.json in moduleDir to find the main entry point
	pkgJsonPath := filepath.Join(moduleDir, "package.json")
	fmt.Fprintf(os.Stderr, "Reading package.json for module import at %s\n", pkgJsonPath)
	pkgJsonBytes, err := os.ReadFile(pkgJsonPath)
	if err != nil {
		return "", &GenericError{message: "Could not read package.json for module import: " + importStmt.PackageName, span: importStmt.Span()}
	}

	var pkgJsonMap map[string]any
	err = json.Unmarshal(pkgJsonBytes, &pkgJsonMap)
	if err != nil {
		return "", &GenericError{message: "Could not parse package.json for module import: " + importStmt.PackageName, span: importStmt.Span()}
	}

	if typesField, ok := pkgJsonMap["types"]; ok {
		typesStr, isString := typesField.(string)
		if !isString {
			return "", &GenericError{message: "Invalid types field in package.json for module import: " + importStmt.PackageName, span: importStmt.Span()}
		}
		// Use typesField as the entry point for type definitions
		return filepath.Join(moduleDir, typesStr), nil
	}

	return "", &GenericError{message: "No types field found in package.json for module import: " + importStmt.PackageName, span: importStmt.Span()}
}

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

	typeDefPath, err1 := resolveImport(ctx, importStmt)
	if err1 != nil {
		errors = append(errors, err1)
		return errors
	}

	fmt.Fprintf(os.Stderr, "Resolved import %s to type definitions at %s\n", importStmt.PackageName, typeDefPath)

	typeDefModuleMap, err2 := loadTypeScriptModule(typeDefPath)
	if err2 != nil {
		errors = append(errors, &GenericError{message: "Could not load type definitions for module import: " + importStmt.PackageName, span: importStmt.Span()})
		return errors
	}

	typeDefModule, ok := typeDefModuleMap[importStmt.PackageName]
	if !ok {
		globalModule, ok := typeDefModuleMap["global"]
		if !ok {
			errors = append(errors, &GenericError{message: "Type definitions for module import do not contain expected module: " + importStmt.PackageName, span: importStmt.Span()})
			return errors
		}

		fmt.Fprintf(os.Stderr, "globalModule = %#v\n", globalModule)
		fmt.Fprintf(os.Stderr, "Namespace count in global type definitions: %d\n", globalModule.Namespaces.Len())
		for _, ns := range globalModule.Namespaces.Keys() {
			fmt.Fprintf(os.Stderr, "Found global namespace in type definitions: %s\n", ns)
		}

		inferCtx := ctx.WithNewScope()
		inferErrors := c.InferModule(inferCtx, globalModule)
		if len(inferErrors) > 0 {
			errors = append(errors, inferErrors...)
		}

		for name := range inferCtx.Scope.Namespace.Values {
			fmt.Fprintf(os.Stderr, "Imported value from module %s: %s\n", importStmt.PackageName, name)
		}

		for _, specifier := range importStmt.Specifiers {
			if specifier.Name == "*" {
				ctx.Scope.Namespace.SetNamespace(specifier.Alias, inferCtx.Scope.Namespace)
			}
		}

		return errors
	}

	inferCtx := ctx.WithNewScope()
	inferErrors := c.InferModule(inferCtx, typeDefModule)
	if len(inferErrors) > 0 {
		errors = append(errors, inferErrors...)
	}

	for name := range inferCtx.Scope.Namespace.Values {
		fmt.Fprintf(os.Stderr, "Imported value from module %s: %s\n", importStmt.PackageName, name)
	}

	for _, specifier := range importStmt.Specifiers {
		if specifier.Name == "*" {
			ctx.Scope.Namespace.Namespaces[specifier.Alias] = inferCtx.Scope.Namespace
		}
	}

	return errors
}
