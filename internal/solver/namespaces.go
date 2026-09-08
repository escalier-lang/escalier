package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// namespaces.go binds what a `namespace Foo { ... }` block names.
//
// The block itself declares nothing. dep_graph keys each member under
// `Foo.member`, the same qualified shape a directory-derived namespace produces,
// so the members are inferred and bound by the ordinary component walk. What is
// left is collecting them under one Namespace, which is what makes `Foo.member`
// resolve rather than reading as an unknown identifier.
//
// Only a written block reaches this file. A namespace whose prefix comes from a
// subdirectory of `lib/` gets no shell, so nothing binds the prefix and a sibling
// file cannot name it. #1494 covers extending the pre-bind pass to every non-empty
// prefix in module.Namespaces rather than only the ones a NamespaceDecl introduces.

// preBindNamespaceDecls binds one empty Namespace per `namespace` block before the
// component walk, and returns each block paired with the object to fill.
//
// Binding the name first is what lets a member of the same module write
// `Foo.member`. The walk reads that binding while populateNamespaces is still to
// run, and the maps it reads are the ones that call fills through the same
// pointer.
//
// Each block is marked handled, since it introduces no binding of its own and would
// otherwise be reported as a declaration the dep graph did not model.
func (c *checker) preBindNamespaceDecls(scope *Scope, module *ast.Module, handled set.Set[ast.Decl]) []*namespaceShell {
	target := c.declTarget(scope)
	// One qualified name may be declared by several blocks, the same way an
	// interface may. byName keeps one shell per name so the second block adds to
	// the first's Namespace rather than replacing it.
	byName := map[string]*namespaceShell{}
	var shells []*namespaceShell
	module.Namespaces.Scan(func(prefix string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			nsDecl, ok := decl.(*ast.NamespaceDecl)
			if !ok {
				continue
			}
			if sh := c.preBindOneNamespace(prefix, nsDecl, handled, byName); sh != nil {
				// A file importing a package under this name would write the same
				// `name.member` for two different namespaces. Report it rather than
				// picking one, since either choice makes the other unreachable and
				// nothing in the source says which was meant.
				if c.importBindsNamespace(sh.qname) {
					c.report(&NamespaceCollidesWithImportError{
						Name: sh.qname,
						span: nsDecl.Span(),
					})
				}
				// Bind under the qualified name, matching the keys the walk gives the
				// members, so a block in a directory-derived namespace does not collide
				// with a same-named block in a sibling directory.
				target.defineNamespace(sh.qname, sh.ns)
				shells = append(shells, sh)
			}
		}
		return true
	})
	return shells
}

// namespaceShell pairs a `namespace` block with the empty Namespace bound for it,
// which populateNamespaces fills once the walk has bound its members.
type namespaceShell struct {
	// decls are every block declaring this qualified name, in source order.
	decls  []*ast.NamespaceDecl
	ns     *Namespace
	qname  string
	nested []*namespaceShell
}

// preBindOneNamespace mints the empty Namespace for one block and recurses into the
// blocks nested inside it. It returns nil for a block with no name, which the parser
// produces only through error recovery.
func (c *checker) preBindOneNamespace(prefix string, decl *ast.NamespaceDecl, handled set.Set[ast.Decl], byName map[string]*namespaceShell) *namespaceShell {
	handled.Add(decl)
	if decl.Name == nil || decl.Name.Name == "" {
		return nil
	}
	qname := qualify(prefix, decl.Name.Name)

	// A second block of one name reuses the first's Namespace and records its own
	// declarations, so both blocks' members land in one binding.
	if existing, seen := byName[qname]; seen {
		existing.decls = append(existing.decls, decl)
		c.preBindNestedBlocks(qname, decl, handled, byName, existing)
		return nil
	}

	sh := &namespaceShell{decls: []*ast.NamespaceDecl{decl}, ns: newNamespace(qname), qname: qname}
	byName[qname] = sh
	c.preBindNestedBlocks(qname, decl, handled, byName, sh)
	return sh
}

// preBindNestedBlocks mints the Namespace for each block written inside decl and
// hangs it off the parent's Nested map, so `namespace a { namespace b { … } }`
// reaches b through a.
func (c *checker) preBindNestedBlocks(qname string, decl *ast.NamespaceDecl, handled set.Set[ast.Decl], byName map[string]*namespaceShell, parent *namespaceShell) {
	for _, inner := range decl.Decls {
		nested, ok := inner.(*ast.NamespaceDecl)
		if !ok {
			continue
		}
		if child := c.preBindOneNamespace(qname, nested, handled, byName); child != nil {
			parent.ns.Nested[nested.Name.Name] = child.ns
			parent.nested = append(parent.nested, child)
		}
	}
}

// refreshNamespaces fills every namespace pre-bound for the module under
// inference. It runs whenever new bindings have landed, so a member reached
// through `Foo.member` sees what the walk has bound so far.
//
// Re-scanning every shell per component costs O(members) each time, which is
// quadratic over a module of one component per member. #1493 replaces it with a
// prefix-to-shell index the walk pushes each binding into as it defines it.
func (c *checker) refreshNamespaces(scope *Scope) {
	if len(c.nsShells) == 0 {
		return
	}
	c.populateNamespaces(c.declTarget(scope), c.nsShells)
}

// populateNamespaces populates each pre-bound Namespace in shells.
func (c *checker) populateNamespaces(scope *Scope, shells []*namespaceShell) {
	for _, sh := range shells {
		c.populateNamespace(scope, sh)
	}
}

// populateNamespace copies the bindings the walk placed under sh's qualified
// prefix into the Namespace pre-bound for it, then does the same for the blocks
// nested inside it. A nested block is populated through the same pointer its
// parent already holds, so `namespace a { namespace b { val x } }` gives `a.b.x`.
func (c *checker) populateNamespace(scope *Scope, sh *namespaceShell) {
	out := sh.ns
	for _, decl := range sh.decls {
		for _, inner := range decl.Decls {
			if _, ok := inner.(*ast.NamespaceDecl); ok {
				continue
			}
			// Every other kind was keyed under `qname.member` and bound by the walk, so
			// its names are read back rather than re-inferred here. exportedNames answers
			// what a declaration introduces. Its export flag is not consulted, since that
			// flag gates a package's surface rather than what a block holds.
			for _, name := range exportedNames(inner) {
				key := qualify(sh.qname, name)
				if b, found := scope.GetValue(key); found {
					out.Values[name] = b
				}
				if b, found := scope.GetType(key); found {
					out.Types[name] = b
				} else if b, found := scope.GetType(qualify(packageKeyPrefix(c.pkgURI), key)); found {
					// A type declared inside a package registers under a key carrying the
					// package URI, which is what keeps two packages' same-named classes
					// apart. The member is re-keyed to its bare name here, the way
					// exportedSurface re-keys a package's top-level types.
					out.Types[name] = b
				}
			}
		}
	}
	c.populateNamespaces(scope, sh.nested)
}

// importBindsNamespace reports whether any file of the module being inferred binds
// name as an imported package. Imports are bound before the walk, so every one is
// in place by the time a block is pre-bound.
func (c *checker) importBindsNamespace(name string) bool {
	for _, fileScope := range c.fileScopes {
		if fileScope == nil {
			continue
		}
		if _, bound := fileScope.namespaces[name]; bound {
			return true
		}
	}
	return false
}

// NamespaceCollidesWithImportError reports a `namespace` block whose name a file
// of the same module also imports a package under. Both are reached by writing
// `name.member`, so one name would stand for two namespaces.
type NamespaceCollidesWithImportError struct {
	// Name is the name declared as a block and bound by an import.
	Name string
	span ast.Span
}

func (e *NamespaceCollidesWithImportError) Message() string {
	return "namespace " + e.Name + " has the name an import already binds; " +
		"rename the block or write `as` on the import"
}
func (e *NamespaceCollidesWithImportError) Span() ast.Span      { return e.span }
func (e *NamespaceCollidesWithImportError) Related() []ast.Span { return nil }
func (e *NamespaceCollidesWithImportError) isSolverError()      {}
