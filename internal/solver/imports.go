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
// Each file scope is a child of the module scope, so a file sees its own
// imports first and every module-level declaration behind them. The
// declarations are inferred afterwards into the scope the file scopes already
// point at, so both are visible whatever order they were bound in.
func (c *checker) bindFileImports(scope *Scope, module *ast.Module) map[int]*Scope {
	// Read off the AST, since imports bind before the declarations are inferred
	// and the module scope is still empty. Saved and restored, because loading a
	// package runs this same function for that package.
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
	// After the loop, not before. Loading an imported package reenters this
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
	ns, errs := c.loadPackage(uri, stmt.Span())
	if ns == nil {
		// Either the load failed, and errs says why, or the URI is mid-load further
		// up this call chain. A cycle binds nothing and reports nothing.
		return errs
	}

	fileScope.defineNamespace(stmt.LocalName(), ns)
	return errs
}

// bindPseudoPackageImport binds what a `std:` / `web:` / `node:` import names.
//
// A malformed URI binds nothing, so the load's second failure does not bury the
// diagnostic saying what to fix.
//
// The binding is the shape an npm import gets: a namespace, so `import
// "std:date"` reads `date.Date`. A class is not lifted out of its package,
// since a binding mixing a class with its package's other exports would answer
// to no declaration.
func (c *checker) bindPseudoPackageImport(fileScope *Scope, stmt *ast.ImportStmt) []SolverError {
	if errs := validateStdlibImport(stmt); len(errs) > 0 {
		return errs
	}

	uri := stmt.PackageName
	ns, errs := c.loadPackage(uri, stmt.Span())
	if ns == nil {
		return errs
	}

	// The statement's name, so `as` reaches a pseudo-package the way it reaches an
	// npm one. A package name is already lowercase, so a bare import binds what
	// the URI spells.
	fileScope.defineNamespace(stmt.LocalName(), ns)

	if uri == coreURI {
		errs = append(errs, c.bindCoreExports(fileScope, ns, stmt)...)
	}
	return errs
}

// coreURI is the one package whose exports an importer binds unprefixed. It
// still takes an import, which is the whole difference between it and the
// prelude.
//
// The qualifier is dropped because `core` names no domain, so `core.Event` says
// less than `Event`. A package whose name does say something keeps its prefix,
// `error.RangeError` being the shape that reads well.
const coreURI = "web:core"

// bindCoreExports binds each of the core package's exports into fileScope under
// its own name, beside the namespace binding that reaches them qualified.
//
// A name the importing module declares itself wins. The file scope is a CHILD
// of the module scope, so an unguarded binding would shadow the module's own
// declaration, the reverse of both what a reader expects and how the prelude
// behaves from its parent layer. Reporting the collision beats picking either.
func (c *checker) bindCoreExports(fileScope *Scope, ns *Namespace, stmt *ast.ImportStmt) []SolverError {
	var errs []SolverError
	shadowed := set.NewSet[string]()
	// A value and a type of one name are two bindings, so each is checked against
	// the declarations of its own sort. `web:core` exports the type-only
	// `EventInit`, and a module declaring a value of that name still reads the
	// type unprefixed.
	for _, name := range sortedNames(ns) {
		if b, held := ns.Values[name]; held {
			if c.moduleDeclared.values.Contains(name) {
				shadowed.Add(name)
			} else {
				fileScope.defineValue(name, b)
			}
		}
		if b, held := ns.Types[name]; held {
			if c.moduleDeclared.types.Contains(name) {
				shadowed.Add(name)
			} else {
				fileScope.defineType(name, b)
			}
		}
		if shadowed.Contains(name) {
			errs = append(errs, &CoreImportShadowsDeclarationError{
				Name: name, Binding: stmt.LocalName(), span: stmt.Span(),
			})
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

// declaredNames is the names a module declares, split by namespace. Escalier
// resolves a value and a type of one name separately, so a collision is per
// namespace too.
type declaredNames struct {
	values set.Set[string]
	types  set.Set[string]
}

// topLevelNames returns every name a module declares at its top level, exported
// or not, under the namespace it occupies. A class occupies both, since its name
// is the constructor value and the instance type.
func topLevelNames(module *ast.Module) declaredNames {
	declared := declaredNames{values: set.NewSet[string](), types: set.NewSet[string]()}
	module.Namespaces.Scan(func(nsPath string, ns *ast.Namespace) bool {
		// Only the module's own top level. A declaration inside a namespace is
		// reached through it and collides with nothing bound bare.
		if nsPath != "" {
			return true
		}
		for _, decl := range ns.Decls {
			for _, name := range ast.DeclNames(decl) {
				switch decl.(type) {
				case *ast.VarDecl, *ast.FuncDecl:
					declared.values.Add(name)
				case *ast.TypeDecl, *ast.InterfaceDecl, *ast.EnumDecl:
					declared.types.Add(name)
				case *ast.ClassDecl:
					declared.values.Add(name)
					declared.types.Add(name)
				}
			}
		}
		return true
	})
	return declared
}

// CoreImportShadowsDeclarationError reports a name the core package exports
// unprefixed that the importing module already declares.
type CoreImportShadowsDeclarationError struct {
	Name string
	// Binding is the name the import statement binds the package under, its alias
	// when it wrote one, so the remediation spells a qualified form that resolves.
	Binding string
	span    ast.Span
}

func (e *CoreImportShadowsDeclarationError) Message() string {
	return fmt.Sprintf(
		"importing %q binds %s, which this module already declares; reach the imported "+
			"one as `%s.%s` or rename the declaration",
		coreURI, e.Name, e.Binding, e.Name)
}
func (e *CoreImportShadowsDeclarationError) Span() ast.Span      { return e.span }
func (e *CoreImportShadowsDeclarationError) Related() []ast.Span { return nil }
func (e *CoreImportShadowsDeclarationError) isSolverError()      {}
