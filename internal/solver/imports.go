package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// imports.go binds what a file's `import` statements name.
//
// An import binds into the file's own scope, never into the module scope. Two
// files of one module import different packages under the same local name
// without colliding, and a name one file imports is unresolved in its sibling.

// bindFileImports mints a scope per file of module and binds that file's
// imports into it, returning the scopes keyed by source id.
//
// Every file scope is a child of scope, the module scope, so a file sees its
// own imports first and then every module-level declaration. The declarations
// are inferred afterwards into the module scope, which the file scopes already
// point at, so an import and a declaration are both visible to the file's code
// whatever order they were bound in.
func (c *checker) bindFileImports(scope *Scope, module *ast.Module) map[int]*Scope {
	fileScopes := map[int]*Scope{}
	for _, file := range module.Files {
		fileScope := scope.Child()
		fileScopes[file.SourceID] = fileScope
		for _, stmt := range file.Imports {
			c.errs = append(c.errs, c.bindImport(fileScope, stmt)...)
		}
	}
	// Assigned after the loop, not before it. Loading an imported package runs
	// this same function for that package, and the walk that follows reads
	// c.fileScopes; each level installs its own once its own imports are bound,
	// so the innermost load never leaves its map in place of the caller's.
	c.fileScopes = fileScopes
	return fileScopes
}

// bindImport loads the package an import names and binds it into fileScope.
//
// One binding shape: the package becomes a namespace under the name the
// statement binds it as, which is its alias when it wrote one and a name
// derived from the specifier otherwise. Members are reached through it.
func (c *checker) bindImport(fileScope *Scope, stmt *ast.ImportStmt) []SolverError {
	uri := stmt.PackageName
	if IsSchemePrefixedImport(uri) {
		return c.bindPseudoPackageImport(fileScope, stmt)
	}
	ns, errs := c.loadPackage(uri, stmt.Span())
	if ns == nil {
		// Either the load failed, and errs says why, or the URI is being loaded
		// further up this call chain. A cycle binds nothing and reports nothing:
		// the importing side is mid-load itself, and its own surface is what the
		// other side is waiting on.
		return errs
	}

	fileScope.defineNamespace(stmt.LocalName(), ns)
	return errs
}

// bindPseudoPackageImport binds what a `std:` / `web:` / `node:` import names.
//
// The URI is validated first and a malformed one binds nothing: there is no
// package to load, and reporting a second failure from the load would bury the
// diagnostic that says what to fix.
//
// One binding shape, the same one an npm import gets. The package becomes a
// namespace under the name the statement binds it as, so `import "std:date"`
// reads `date.Date` and every export of the package is reached the same way. A
// class is not lifted out of its package, because a binding that mixed a class
// with its package's other exports would answer to no declaration.
func (c *checker) bindPseudoPackageImport(fileScope *Scope, stmt *ast.ImportStmt) []SolverError {
	if errs := validateStdlibImport(stmt); len(errs) > 0 {
		return errs
	}

	uri := stmt.PackageName
	ns, errs := c.loadPackage(uri, stmt.Span())
	if ns == nil {
		return errs
	}

	// The name is the statement's, so `as` reaches a pseudo-package the same way
	// it reaches an npm one. A package name is already lowercase, so an import
	// with no alias binds what the URI spells.
	fileScope.defineNamespace(stmt.LocalName(), ns)
	return errs
}
