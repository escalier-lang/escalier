package solver

import (
	"strings"

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
// A prefix a subdirectory or a merged package group introduced is bound too.
// Without one the flat qualified key `geo.x` is bound and carries the right type,
// but nothing binds `geo`, so a sibling file writing `geo.x` reads it as an
// unknown identifier.

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
func (c *checker) preBindNamespaceDecls(scope *Scope, module *ast.Module, handled set.Set[ast.Decl]) map[string]*Namespace {
	target := c.declTarget(scope)
	// One qualified name may be declared by several blocks, the same way an
	// interface may. byName keeps one Namespace per name so the second block adds
	// to the first rather than replacing it. It is also what the walk routes each
	// binding through, keyed by the prefix the binding carries.
	byName := map[string]*Namespace{}
	module.Namespaces.Scan(func(prefix string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			nsDecl, ok := decl.(*ast.NamespaceDecl)
			if !ok {
				continue
			}
			if bound := c.preBindOneNamespace(prefix, nsDecl, handled, byName); bound != nil {
				// A file importing a package under this name would write the same
				// `name.member` for two different namespaces. Report it rather than
				// picking one, since either choice makes the other unreachable and
				// nothing in the source says which was meant.
				if c.importBindsNamespace(bound.Name) {
					c.report(&NamespaceCollidesWithImportError{
						Name: bound.Name,
						span: nsDecl.Span(),
					})
				}
				// Bind under the qualified name, matching the keys the walk gives the
				// members, so a block in a directory-derived namespace does not collide
				// with a same-named block in a sibling directory.
				target.defineNamespace(bound.Name, bound)
			}
		}
		return true
	})
	c.preBindPathNamespaces(target, module, byName)
	return byName
}

// preBindPathNamespaces binds a Namespace for every prefix in module.Namespaces
// that no `namespace` block already covers.
//
// A prefix comes from a subdirectory of `lib/`, or from the synthetic path a
// merged package group parses each member under. Neither has a NamespaceDecl to
// key off, so neither reaches the block pass above.
//
// Each ancestor prefix is bound as well, hung off its parent, so the members
// of `geo.sub` are reached by writing `geo.sub.member`. The scan runs in sorted
// order, which reaches `geo` before `geo.sub`.
func (c *checker) preBindPathNamespaces(target *Scope, module *ast.Module, byName map[string]*Namespace) {
	module.Namespaces.Scan(func(prefix string, ns *ast.Namespace) bool {
		if prefix == "" {
			return true
		}
		var parent *Namespace
		walked := ""
		for _, segment := range strings.Split(prefix, ".") {
			walked = qualify(walked, segment)
			bound, seen := byName[walked]
			if !seen {
				// A closure member's Namespace is minted before the imports bind, so
				// that a member importing a sibling binds the object the walk then
				// fills. Indexing a nil map outside a group load yields nil.
				bound = c.memberNamespaces[walked]
				if bound == nil {
					bound = newNamespace(walked)
				}
				byName[walked] = bound
				target.defineNamespace(walked, bound)
				if parent != nil {
					parent.Nested[segment] = bound
				}
			}
			parent = bound
		}
		return true
	})
}

// preBindOneNamespace mints the empty Namespace for one block and recurses into the
// blocks nested inside it. It returns nil for a block with no name, which the parser
// produces only through error recovery.
func (c *checker) preBindOneNamespace(prefix string, decl *ast.NamespaceDecl, handled set.Set[ast.Decl], byName map[string]*Namespace) *Namespace {
	handled.Add(decl)
	if decl.Name == nil || decl.Name.Name == "" {
		return nil
	}
	qname := qualify(prefix, decl.Name.Name)

	// A second block of one name reuses the first's Namespace and records its own
	// declarations, so both blocks' members land in one binding.
	if existing, seen := byName[qname]; seen {
		c.preBindNestedBlocks(qname, decl, handled, byName, existing)
		return nil
	}

	ns := newNamespace(qname)
	byName[qname] = ns
	c.preBindNestedBlocks(qname, decl, handled, byName, ns)
	return ns
}

// preBindNestedBlocks mints the Namespace for each block written inside decl and
// hangs it off the parent's Nested map, so `namespace a { namespace b { … } }`
// reaches b through a.
func (c *checker) preBindNestedBlocks(qname string, decl *ast.NamespaceDecl, handled set.Set[ast.Decl], byName map[string]*Namespace, parent *Namespace) {
	for _, inner := range decl.Decls {
		nested, ok := inner.(*ast.NamespaceDecl)
		if !ok {
			continue
		}
		if child := c.preBindOneNamespace(qname, nested, handled, byName); child != nil {
			parent.Nested[nested.Name.Name] = child
		}
	}
}

// routeToNamespace places a binding the module scope just made into the namespace
// pre-bound for its prefix, so a member reached through `Foo.member` sees it as
// soon as the walk defines it.
//
// The alternative is re-scanning every namespace after each component, which costs
// O(members) each time and is quadratic over a module of one component per
// member.
//
// A name with no dot is a top-level binding and belongs to no namespace. A dotted
// name whose prefix names no namespace is left alone, which is how a member of a
// namespace this module does not declare passes through.
func (c *checker) routeToNamespace(scope *Scope, key string) {
	if len(c.nsIndex) == 0 {
		return
	}
	// A type declared inside a package registers under a key carrying the package
	// URI, which is what keeps two packages' same-named classes apart. That head
	// escapes the URI's own dots, so the first dot ends it and what follows is the
	// qualified name the namespace knows the member by.
	member := key
	if strings.HasPrefix(member, packageKeyHead) {
		_, rest, ok := strings.Cut(member, ".")
		if !ok {
			return
		}
		member = rest
	}
	dot := strings.LastIndex(member, ".")
	if dot < 0 {
		return
	}
	ns, held := c.nsIndex[member[:dot]]
	if !held {
		return
	}
	name := member[dot+1:]
	if b, found := scope.GetValue(key); found {
		ns.Values[name] = b
	}
	if b, found := scope.GetType(key); found {
		ns.Types[name] = b
	}
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
