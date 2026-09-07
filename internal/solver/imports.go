package solver

import (
	"fmt"
	"strings"

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
// Three binding shapes, and the statement's specifiers pick between them. A
// bare `import "std:math"` binds the package as a namespace under its last URI
// segment. A `* as name` specifier binds it under that name. A named specifier
// binds one member, under its alias when it has one.
func (c *checker) bindImport(fileScope *Scope, stmt *ast.ImportStmt) []SolverError {
	uri := stmt.PackageName
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

	for _, spec := range stmt.Specifiers {
		if spec.Name == "*" {
			fileScope.defineNamespace(spec.Alias, ns)
			continue
		}
		local := spec.Alias
		if local == "" {
			local = spec.Name
		}
		bound := false
		if b, ok := ns.Values[spec.Name]; ok {
			fileScope.defineValue(local, b)
			bound = true
		}
		if b, ok := ns.Types[spec.Name]; ok {
			fileScope.defineType(local, b)
			bound = true
		}
		// A namespace is a third binding sort, so a specifier naming one binds it
		// alongside. An enum arrives as a type and a namespace at once, and both
		// halves are needed for `Color.Red()` to resolve.
		if nested, ok := ns.Nested[spec.Name]; ok {
			fileScope.defineNamespace(local, nested)
			bound = true
		}
		if !bound {
			errs = append(errs, &UnexportedMemberError{
				URI:    uri,
				Member: spec.Name,
				span:   spec.Span(),
			})
		}
	}
	return errs
}

// localName returns the name a bare import binds a package under: the last
// segment of its URI, so `std:math` binds `math` and a bare path binds its last
// component.
func localName(uri string) string {
	if _, pkg, ok := strings.Cut(uri, ":"); ok {
		uri = pkg
	}
	if i := strings.LastIndex(uri, "/"); i >= 0 {
		uri = uri[i+1:]
	}
	return uri
}

// UnexportedMemberError reports a named import of something the package does
// not export. A package's surface holds only its exported declarations, so a
// member that exists but is not exported reads the same as one that does not
// exist, which is what the message says.
type UnexportedMemberError struct {
	// URI is the package the import named.
	URI string
	// Member is the name the specifier asked for.
	Member string
	span   ast.Span
}

func (e *UnexportedMemberError) Message() string {
	return fmt.Sprintf("package %q exports no %q", e.URI, e.Member)
}
func (e *UnexportedMemberError) Span() ast.Span      { return e.span }
func (e *UnexportedMemberError) Related() []ast.Span { return nil }
func (e *UnexportedMemberError) isSolverError()      {}
