package solver

import (
	"fmt"
	"sort"

	"github.com/escalier-lang/escalier/internal/ast"
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
// Every file scope is a child of scope, the module scope, so a file sees its
// own imports first and then every module-level declaration. The declarations
// are inferred afterwards into the module scope, which the file scopes already
// point at, so an import and a declaration are both visible to the file's code
// whatever order they were bound in.
func (c *checker) bindFileImports(scope *Scope, module *ast.Module) map[int]*Scope {
	// The module's own top-level names, read off the AST rather than the scope.
	// Imports bind before the declarations are inferred, so the module scope is
	// still empty here and only the AST says what the module declares. The
	// unprefixed core binding is what needs it, to leave a name alone rather than
	// shadow it. Saved and restored, since loading a package runs this same
	// function for that package.
	prevDeclared := c.moduleDeclared
	c.moduleDeclared = topLevelNames(module)
	defer func() { c.moduleDeclared = prevDeclared }()

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

	if uri == coreURI {
		errs = append(errs, c.bindCoreExports(fileScope, ns, stmt)...)
	}
	return errs
}

// coreURI is the one package whose exports an importer binds unprefixed.
//
// It still takes an import, which is the whole difference between it and the
// prelude. What it shares with the prelude is that a qualifier buys the reader
// nothing: `core` names no domain, and `core.Event` says less than `Event`.
// Twenty-one packages in the generated tree import it, more than any other.
//
// A package whose name does say something keeps its prefix.
// `error.RangeError` reads as one of the error classes.
const coreURI = "web:core"

// bindCoreExports binds each of the core package's exports into fileScope under
// its own name, beside the namespace binding that reaches the same members
// qualified.
//
// A name the importing module declares itself wins. An import binds into the
// file scope, which is a CHILD of the module scope, so an unguarded binding
// would shadow the module's own declaration — the reverse of what a reader
// expects, and the reverse of how the prelude behaves, since the prelude's
// layer is a parent. Reporting the collision beats picking either one.
func (c *checker) bindCoreExports(fileScope *Scope, ns *Namespace, stmt *ast.ImportStmt) []SolverError {
	var errs []SolverError
	for _, name := range sortedNames(ns) {
		if c.moduleDeclared.Contains(name) {
			errs = append(errs, &CoreImportShadowsDeclarationError{Name: name, span: stmt.Span()})
			continue
		}
		if b, held := ns.Values[name]; held {
			fileScope.defineValue(name, b)
		}
		if b, held := ns.Types[name]; held {
			fileScope.defineType(name, b)
		}
	}
	return errs
}

// sortedNames returns every name a namespace exports in either sort, sorted, so
// a diagnostic about one does not depend on map iteration.
func sortedNames(ns *Namespace) []string {
	seen := set.NewSet[string]()
	for name := range ns.Values {
		seen.Add(name)
	}
	for name := range ns.Types {
		seen.Add(name)
	}
	names := seen.ToSlice()
	sort.Strings(names)
	return names
}

// topLevelNames returns every name a module declares at its top level, exported
// or not. An unexported declaration still occupies the name in its own module.
func topLevelNames(module *ast.Module) set.Set[string] {
	names := set.NewSet[string]()
	module.Namespaces.Scan(func(nsPath string, ns *ast.Namespace) bool {
		// Only the module's own top level. A declaration inside a namespace is
		// reached through it and collides with nothing bound bare.
		if nsPath != "" {
			return true
		}
		for _, decl := range ns.Decls {
			for _, name := range exportedNames(decl) {
				names.Add(name)
			}
		}
		return true
	})
	return names
}

// CoreImportShadowsDeclarationError reports a name the core package exports
// unprefixed that the importing module already declares.
type CoreImportShadowsDeclarationError struct {
	Name string
	span ast.Span
}

func (e *CoreImportShadowsDeclarationError) Message() string {
	return fmt.Sprintf(
		"importing %q binds %s, which this module already declares; reach the imported "+
			"one as `core.%s` or rename the declaration",
		coreURI, e.Name, e.Name)
}
func (e *CoreImportShadowsDeclarationError) Span() ast.Span      { return e.span }
func (e *CoreImportShadowsDeclarationError) Related() []ast.Span { return nil }
func (e *CoreImportShadowsDeclarationError) isSolverError()      {}
