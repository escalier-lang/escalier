package checker

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/resolver"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// LoadReactTypes loads @types/react and injects types into the global scope.
// This function uses the existing loadClassifiedTypeScriptModule() infrastructure
// to parse and infer types from the React type definitions.
//
// If @types/react is not installed, an error is returned.
func (c *Checker) LoadReactTypes(ctx Context, sourceDir string) []Error {
	var errors []Error

	// 1. Resolve @types/react location
	reactTypesDir, err := resolver.ResolveTypesPackage("react", sourceDir)
	if err != nil {
		return []Error{&GenericError{
			message: "Could not find @types/react: " + err.Error(),
			span:    DEFAULT_SPAN,
		}}
	}

	// 2. Find entry point
	entryPoint, err := resolver.GetTypesEntryPoint(reactTypesDir)
	if err != nil {
		return []Error{&GenericError{
			message: "Could not find entry point for @types/react: " + err.Error(),
			span:    DEFAULT_SPAN,
		}}
	}

	fmt.Fprintf(os.Stderr, "Loading @types/react from %s\n", entryPoint)

	// 3. Check if already loaded (use PackageRegistry for caching)
	if pkgNs, found := c.PackageRegistry.Lookup(entryPoint); found {
		fmt.Fprintf(os.Stderr, "@types/react already loaded, injecting into scope\n")
		if err := c.injectReactTypes(ctx, pkgNs); err != nil {
			return []Error{&GenericError{
				message: "Failed to inject cached React types: " + err.Error(),
				span:    DEFAULT_SPAN,
			}}
		}
		return nil
	}

	// 4. Load referenced files first (global.d.ts)
	// The @types/react index.d.ts has: /// <reference path="global.d.ts" />
	globalDtsPath := filepath.Join(reactTypesDir, "global.d.ts")
	if _, statErr := os.Stat(globalDtsPath); statErr == nil {
		globalErrors := c.loadReactGlobalFile(globalDtsPath)
		errors = append(errors, globalErrors...)
	}

	// 5. Load and classify the main entry point using existing infrastructure
	loadResult, loadErr := loadClassifiedTypeScriptModule(entryPoint)
	if loadErr != nil {
		return []Error{&GenericError{
			message: "Could not load @types/react: " + loadErr.Error(),
			span:    DEFAULT_SPAN,
		}}
	}

	// 6. Process global augmentations (JSX namespace lives here)
	if loadResult.GlobalModule != nil {
		globalCtx := Context{
			Scope:      c.GlobalScope,
			IsAsync:    false,
			IsPatMatch: false,
		}
		globalErrors := c.InferModule(globalCtx, loadResult.GlobalModule)
		if len(globalErrors) > 0 {
			for _, err := range globalErrors {
				fmt.Fprintf(os.Stderr, "Global augmentation error in @types/react: %s\n", err.Message())
			}
			errors = append(errors, globalErrors...)
		}
	}

	// 7. Process internal declarations first (file-scoped types like Booleanish, NativeAnimationEvent, etc.)
	// These need to be in scope before package declarations are processed.
	var pkgNs *type_system.Namespace
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

	if loadResult.InternalModule != nil {
		internalErrors := c.InferModule(pkgCtx, loadResult.InternalModule)
		if len(internalErrors) > 0 {
			for _, err := range internalErrors {
				fmt.Fprintf(os.Stderr, "Internal module error in @types/react: %s\n", err.Message())
			}
			errors = append(errors, internalErrors...)
		}
	}

	// 8. Process package module (React namespace with FC, Component, etc.)
	if loadResult.PackageModule != nil {
		pkgErrors := c.InferModule(pkgCtx, loadResult.PackageModule)
		if len(pkgErrors) > 0 {
			for _, err := range pkgErrors {
				fmt.Fprintf(os.Stderr, "Package module error in @types/react: %s\n", err.Message())
			}
			errors = append(errors, pkgErrors...)
		}
	}

	// 9. Process named modules (e.g., declare module "react" { ... })
	// Check if there's a "react" named module that should be used as the package namespace
	if reactModule, ok := loadResult.NamedModules["react"]; ok {
		namedErrors := c.InferModule(pkgCtx, reactModule)
		if len(namedErrors) > 0 {
			for _, err := range namedErrors {
				fmt.Fprintf(os.Stderr, "Named module error in @types/react: %s\n", err.Message())
			}
			errors = append(errors, namedErrors...)
		}
	}

	// 10. Copy JSX namespace from React to the current scope
	// In @types/react, JSX is nested inside React (React.JSX), but the JSX type checker
	// expects it at the top level as "JSX". Copy it to the current scope's namespace.
	if jsxNs, ok := pkgNs.GetNamespace("JSX"); ok {
		if err := ctx.Scope.Namespace.SetNamespace("JSX", jsxNs); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to set JSX namespace in scope: %s\n", err.Error())
		}
	} else {
		fmt.Fprintf(os.Stderr, "Warning: JSX namespace not found in React package namespace\n")
	}

	// 11. Always register in PackageRegistry for caching (even if partially populated)
	// This prevents re-parsing on subsequent calls
	if regErr := c.PackageRegistry.Register(entryPoint, pkgNs); regErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to register @types/react: %s\n", regErr.Error())
	}

	// 12. Inject types into current scope
	if err := c.injectReactTypes(ctx, pkgNs); err != nil {
		errors = append(errors, &GenericError{
			message: "Failed to inject React types: " + err.Error(),
			span:    DEFAULT_SPAN,
		})
	}

	fmt.Fprintf(os.Stderr, "@types/react loaded successfully\n")
	return errors
}

// loadReactGlobalFile loads a referenced .d.ts file (like global.d.ts) into the global scope.
func (c *Checker) loadReactGlobalFile(filePath string) []Error {
	var errors []Error

	loadResult, loadErr := loadClassifiedTypeScriptModule(filePath)
	if loadErr != nil {
		return []Error{&GenericError{
			message: "Could not load React types file " + filePath + ": " + loadErr.Error(),
			span:    DEFAULT_SPAN,
		}}
	}

	// Process global declarations
	if loadResult.GlobalModule != nil {
		globalCtx := Context{
			Scope:      c.GlobalScope,
			IsAsync:    false,
			IsPatMatch: false,
		}
		globalErrors := c.InferModule(globalCtx, loadResult.GlobalModule)
		errors = append(errors, globalErrors...)
	}

	// Process package declarations (for global files, these are also globals)
	if loadResult.PackageModule != nil {
		globalCtx := Context{
			Scope:      c.GlobalScope,
			IsAsync:    false,
			IsPatMatch: false,
		}
		globalErrors := c.InferModule(globalCtx, loadResult.PackageModule)
		errors = append(errors, globalErrors...)
	}

	return errors
}

// injectReactTypes adds React types to the current scope.
// The React namespace is made available as a value (for React.createElement, etc.).
// The JSX namespace should already be in GlobalScope from global augmentations.
// Returns an error if the namespace cannot be injected.
func (c *Checker) injectReactTypes(ctx Context, pkgNs *type_system.Namespace) error {
	if pkgNs == nil {
		return fmt.Errorf("injectReactTypes: pkgNs is nil")
	}
	if ctx.Scope == nil {
		return fmt.Errorf("injectReactTypes: ctx.Scope is nil")
	}
	if ctx.Scope.Namespace == nil {
		return fmt.Errorf("injectReactTypes: ctx.Scope.Namespace is nil")
	}

	// Make React namespace available in the current scope
	if err := ctx.Scope.Namespace.SetNamespace("React", pkgNs); err != nil {
		return fmt.Errorf("could not inject React namespace: %w", err)
	}
	return nil
}

// JSXDetector implements ast.Visitor to detect JSX syntax in AST nodes.
// Using the Visitor pattern ensures we catch JSX nested in any expression,
// including ternaries, closures, and method chains.
type JSXDetector struct {
	ast.DefaultVisitor
	Found bool
}

// EnterExpr is called for each expression node during traversal.
// Returns false to stop traversal when JSX is found, true to continue.
func (d *JSXDetector) EnterExpr(e ast.Expr) bool {
	if d.Found {
		return false // Stop traversal once JSX is found
	}
	switch e.(type) {
	case *ast.JSXElementExpr, *ast.JSXFragmentExpr:
		d.Found = true
		return false // No need to traverse children
	}
	return true // Continue traversing children
}

// HasJSXSyntax checks if an AST module contains any JSX expressions.
// It iterates over all namespaces in the module and traverses each declaration.
func HasJSXSyntax(module *ast.Module) bool {
	detector := &JSXDetector{}

	// Iterate over all namespaces in the module
	module.Namespaces.Scan(func(name string, ns *ast.Namespace) bool {
		if detector.Found {
			return false // Stop scanning namespaces
		}
		// Traverse each declaration in the namespace
		for _, decl := range ns.Decls {
			decl.Accept(detector)
			if detector.Found {
				return false // Stop scanning namespaces
			}
		}
		return true // Continue scanning namespaces
	})

	return detector.Found
}

// HasJSXSyntaxInScript checks if an AST script contains any JSX expressions.
// It iterates over all statements in the script and traverses each one.
func HasJSXSyntaxInScript(script *ast.Script) bool {
	detector := &JSXDetector{}

	for _, stmt := range script.Stmts {
		stmt.Accept(detector)
		if detector.Found {
			return true
		}
	}

	return false
}
