package solver

import (
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
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
// The binding shape is the FR5 rule. A package whose sole class is named after
// it binds that class under its own capitalization, so `import "std:array"`
// gives `Array` rather than `array.Array`. Every other package binds as a
// namespace under its lowercased package name.
func (c *checker) bindPseudoPackageImport(fileScope *Scope, stmt *ast.ImportStmt) []SolverError {
	if errs := validateStdlibImport(stmt); len(errs) > 0 {
		return errs
	}

	uri := stmt.PackageName
	ns, errs := c.loadPackage(uri, stmt.Span())
	if ns == nil {
		return errs
	}

	_, pkg, _ := splitScheme(uri)
	if className, ok := singleClassShortcut(ns, pkg); ok {
		fileScope.defineValue(className, ns.Values[className])
		fileScope.defineType(className, ns.Types[className])
	}
	// The namespace is bound whether or not the shortcut fired. A package pairs
	// its class with other exports — `std:array` ships `FlatArray` beside
	// `Array` — and binding only the class would leave those unreachable under
	// any name.
	//
	// FR5 asks for more than reachability: the other members belong on the class
	// binding itself, with a static of the same name winning. That merge is #1466.
	fileScope.defineNamespace(strings.ToLower(pkg), ns)
	return errs
}

// singleClassShortcut returns the class name a package binds directly under,
// and false when the package binds as a namespace instead.
//
// The shortcut fires when the package exports a class whose name matches its
// own case-insensitively. The name comes back in its own capitalization, so
// `std:array` binds `Array`.
//
// A value and a type under one name is not enough to go on. A package exporting
// `fn array` beside `type array` has both, and binding the function directly
// would shadow the namespace: a member access that finds a value resolves
// through that value's type and never reaches a namespace of the same name, so
// every other export would become unreachable. Only a `ClassType` binding says
// a class is what produced the pair.
func singleClassShortcut(ns *Namespace, pkg string) (string, bool) {
	for name := range ns.Values {
		if !strings.EqualFold(name, pkg) {
			continue
		}
		if b, hasType := ns.Types[name]; hasType {
			if _, isClass := b.Type.(*soltype.ClassType); isClass {
				return name, true
			}
		}
	}
	return "", false
}
