package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/resolver"
	"github.com/escalier-lang/escalier/internal/set"
)

// imports.go binds what a file's `import` statements name.
//
// An import binds into the file's own scope, never into the module scope. Two
// files of one module import different packages under the same local name
// without colliding, and a name one file imports is unresolved in its sibling.

// bindFileImports mints a scope per file of module and binds that file's
// imports into it, returning the scopes keyed by source id.
//
// Each file scope is a child of the module scope, so a file sees its own
// imports first and every module-level declaration behind them. The
// declarations are inferred afterwards into the scope the file scopes already
// point at, so both are visible whatever order they were bound in.
func (c *checker) bindFileImports(scope *Scope, module *ast.Module) map[int]*Scope {
	// Read off the AST, since imports bind before the declarations are inferred
	// and the module scope is still empty. Saved and restored, because loading a
	// package runs this same function for that package.
	prevDeclared := c.moduleDeclared
	c.moduleDeclared = topLevelValueNames(module)
	defer func() { c.moduleDeclared = prevDeclared }()

	fileScopes := map[int]*Scope{}
	for _, file := range module.Files {
		fileScopes[file.SourceID] = scope.Child()
	}
	// Every pseudo-package import binds before any npm import. Binding an npm import
	// binds the globals scope first, and a pseudo-package the globals scope loads
	// reports its diagnostics with no span. Bound first, the pseudo-package reports
	// them at the import that names it.
	for _, file := range module.Files {
		for _, stmt := range file.Imports {
			if IsSchemePrefixedImport(stmt.PackageName) {
				c.errs = append(c.errs, c.bindImport(fileScopes[file.SourceID], stmt)...)
			}
		}
	}
	for _, file := range module.Files {
		for _, stmt := range file.Imports {
			if !IsSchemePrefixedImport(stmt.PackageName) {
				c.errs = append(c.errs, c.bindImport(fileScopes[file.SourceID], stmt)...)
			}
		}
	}
	// After the loops, not before. Loading an imported package reenters this
	// function, so assigning early would leave the innermost load's map in place
	// of this one's.
	c.fileScopes = fileScopes
	return fileScopes
}

// bindImport loads the package an import names and binds it into fileScope as a
// namespace, under the statement's alias or a name derived from the specifier.
func (c *checker) bindImport(fileScope *Scope, stmt *ast.ImportStmt) []SolverError {
	uri := stmt.PackageName
	if IsSchemePrefixedImport(uri) {
		return c.bindPseudoPackageImport(fileScope, stmt)
	}
	// A package's own imports belong to its own package.json, so only the entry
	// module's are checked against the project's. A specifier that names no package
	// is left to the load, which reports why.
	if c.pkgURI == "" && c.dependencies != nil && resolver.ValidateModuleName(uri) == nil &&
		!listedDependency(c.dependencies, uri) {
		return []SolverError{&UnlistedDependencyError{Package: resolver.PackageName(uri), span: stmt.Span()}}
	}
	// An npm package's declarations read the globals scope, so it has to be full
	// before the package infers.
	c.bindGlobals()
	ns, errs := c.loadPackage(uri, stmt.Span())
	if ns == nil {
		// Either the load failed, and errs says why, or the URI is mid-load further
		// up this call chain. A cycle binds nothing and reports nothing.
		return errs
	}

	c.bindImportedNamespace(fileScope, stmt.LocalName(), ns)
	return errs
}

// bindImportedNamespace binds ns under name in fileScope. It binds ns.AsValue under
// name as well, unless the module declares a value of that name.
func (c *checker) bindImportedNamespace(fileScope *Scope, name string, ns *Namespace) {
	// The file scope is a child of the module scope, so a value bound here would
	// shadow the module's own declaration. Binding the namespace alone keeps the
	// module's value in reach, the way a plain namespace import does.
	if c.moduleDeclared.Contains(name) {
		fileScope.defineNamespace(name, ns)
		return
	}
	// A package whose `.d.ts` writes `export = f` binds f's value too, so the file can
	// call `p(…)` as well as read `p.member`.
	fileScope.bindNamespace(name, ns)
}

// bindPseudoPackageImport binds what a `std:` / `web:` / `node:` import names.
//
// A malformed URI binds nothing, so the load's second failure does not bury the
// diagnostic saying what to fix.
//
// The binding is the shape an npm import gets: a namespace, so `import
// "std:date"` reads `date.Date`. A class is not lifted out of its package,
// since a binding mixing a class with its package's other exports would answer
// to no declaration. A value the package exports under the binding's own name is
// the binding's value as well, the way an npm package's `export =` value is.
func (c *checker) bindPseudoPackageImport(fileScope *Scope, stmt *ast.ImportStmt) []SolverError {
	if errs := validateStdlibImport(stmt); len(errs) > 0 {
		return errs
	}

	uri := stmt.PackageName
	// A member of the group being loaded is already in the merged module, so it is
	// bound from there rather than loaded again. The binding goes in this file's
	// scope under the name this file wrote, which is what lets two members import
	// packages whose URIs derive one name: `std:url` and `web:url` are two
	// namespaces, and each importer reaches the one it named.
	//
	// The namespace is empty at this point and the walk fills it through the same
	// pointer, the way a `namespace` block's is filled.
	//
	// The binding is the namespace alone, without the own-name value the path below
	// binds, since that value is not inferred yet. A pseudo-package holds declarations
	// with no bodies, so a member reads a sibling only in a type, which the namespace
	// answers. `console.Console` resolves here, and `console.log(…)` would not.
	if c.activeGroup.Contains(uri) {
		if ns, held := c.memberNamespaces[groupNamespace(uri)]; held {
			fileScope.defineNamespace(stmt.LocalName(), ns)
		}
		return nil
	}

	ns, errs := c.loadPackage(uri, stmt.Span())
	if ns == nil {
		return errs
	}

	// The statement's name, so `as` reaches a pseudo-package the way it reaches an
	// npm one. A package name is already lowercase, so a bare import binds what
	// the URI spells. A package exporting a value under that name binds it too, so
	// `import "std:console"` reads `console.log(…)` as well as `console.Console`.
	c.bindImportedNamespace(fileScope, stmt.LocalName(), ns)
	return errs
}

// topLevelValueNames returns every value name a module declares at its top level,
// exported or not. A class counts, since its name is also its constructor value.
func topLevelValueNames(module *ast.Module) set.Set[string] {
	declared := set.NewSet[string]()
	module.Namespaces.Scan(func(nsPath string, ns *ast.Namespace) bool {
		// Only the module's own top level. A declaration inside a namespace is
		// reached through it and collides with nothing bound bare.
		if nsPath != "" {
			return true
		}
		for _, decl := range ns.Decls {
			for _, name := range ast.DeclNames(decl) {
				switch decl.(type) {
				case *ast.VarDecl, *ast.FuncDecl, *ast.ClassDecl:
					declared.Add(name)
				}
			}
		}
		return true
	})
	return declared
}
