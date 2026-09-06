package solver

import (
	"fmt"
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
// Three binding shapes, and the statement's specifiers pick between them. A
// bare `import "std:math"` binds the package as a namespace under its last URI
// segment. A `* as name` specifier binds it under that name. A named specifier
// binds one member, under its alias when it has one.
func (c *checker) bindImport(fileScope *Scope, stmt *ast.ImportStmt) []SolverError {
	uri := stmt.PackageName
	if IsSchemePrefixedImport(uri) {
		return c.bindPseudoPackageImport(fileScope, stmt)
	}
	// A named specifier is reported before the package is loaded. Nothing it
	// names can be bound, so loading first would only add whatever the package
	// has to say to a diagnostic the author has to act on either way.
	if errs := namedSpecifierErrors(uri, stmt); len(errs) > 0 {
		return errs
	}

	ns, errs := c.loadPackage(uri, stmt.Span())
	if ns == nil {
		// Either the load failed, and errs says why, or the URI is being loaded
		// further up this call chain. A cycle binds nothing and reports nothing:
		// the importing side is mid-load itself, and its own surface is what the
		// other side is waiting on.
		return errs
	}

	if stmt.Bare() {
		fileScope.defineNamespace(localName(uri), ns)
		return errs
	}

	// Only `* as name` reaches here, since every other specifier was reported.
	// It binds what the bare form binds, under the name the author chose.
	for _, spec := range stmt.Specifiers {
		fileScope.defineNamespace(spec.Alias, ns)
	}
	return errs
}

// namedSpecifierErrors reports every named specifier stmt writes, one each, so
// an import naming several members says so about all of them rather than about
// whichever came first.
func namedSpecifierErrors(uri string, stmt *ast.ImportStmt) []SolverError {
	var errs []SolverError
	for _, spec := range stmt.Specifiers {
		if spec.Name == "*" {
			continue
		}
		errs = append(errs, &NamedImportError{
			URI:    uri,
			Member: spec.Name,
			span:   spec.Span(),
		})
	}
	return errs
}

// localName returns the name a bare import binds a package under: the last
// segment of its specifier, so `lodash/fp` binds `fp`. A scheme-prefixed URI
// never reaches here, since bindImport diverts one to the pseudo-package path.
func localName(uri string) string {
	if _, pkg, ok := strings.Cut(uri, ":"); ok {
		uri = pkg
	}
	if i := strings.LastIndex(uri, "/"); i >= 0 {
		uri = uri[i+1:]
	}
	return uri
}

// NamedImportError reports an `import { name } from "..."`. Escalier has no
// named import. A package is bound as a namespace and its members are reached
// through it, so the message names the form that does work.
type NamedImportError struct {
	// URI is the package the import named.
	URI string
	// Member is the name the specifier asked for.
	Member string
	span   ast.Span
}

func (e *NamedImportError) Message() string {
	return fmt.Sprintf(
		"named imports are not supported; write `import %q` and reach %q as `%s.%s`",
		e.URI, e.Member, localName(e.URI), e.Member)
}
func (e *NamedImportError) Span() ast.Span      { return e.span }
func (e *NamedImportError) Related() []ast.Span { return nil }
func (e *NamedImportError) isSolverError()      {}

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
