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

// preBindNamespaceDecls binds one empty Namespace per `namespace` block before the
// component walk, and returns each block paired with the object to fill. Binding
// the name first is what lets a member of the same module write `Foo.member`: the
// walk reads the binding while populateNamespaces is still to run, and the maps it
// reads are the ones that call fills through the same pointer.
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

// populateNamespaces fills each pre-bound Namespace with the bindings the walk
// placed under its qualified prefix. A nested block is filled through the same
// pointer its parent already holds, so `namespace a { namespace b { val x } }`
// gives `a.b.x`.
func (c *checker) populateNamespaces(scope *Scope, shells []*namespaceShell) {
	for _, sh := range shells {
		c.fillNamespace(scope, sh)
	}
}

func (c *checker) fillNamespace(scope *Scope, sh *namespaceShell) {
	out := sh.ns
	for _, decl := range sh.decls {
		for _, inner := range decl.Decls {
			if _, ok := inner.(*ast.NamespaceDecl); ok {
				continue
			}
			// Every other kind was keyed under `qname.member` and bound by the walk, so
			// its names are read back rather than re-inferred here. exportedNames answers
			// what a declaration introduces; the export flag gates a package's surface,
			// not what its own namespace holds.
			for _, name := range exportedNames(inner) {
				key := qualify(sh.qname, name)
				if b, found := scope.GetValue(key); found {
					out.Values[name] = b
				}
				if b, found := scope.GetType(key); found {
					out.Types[name] = b
				}
			}
		}
	}
	c.populateNamespaces(scope, sh.nested)
}
