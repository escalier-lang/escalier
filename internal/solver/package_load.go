package solver

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
)

// package_load.go infers a package the entry module imports and keeps what it
// declared, so a name the importer writes resolves to a definition another
// module produced.

// loadPackage returns the exported surface of the package at uri, inferring it
// on first request and reading the registry on every later one.
//
// A URI already loading further up this call chain closes a cycle: nothing is
// bound and an ImportCycleError is reported, which is what terminates the walk.
// A package that reports diagnostics of its own is still published, since its
// declarations are the best answer an importer will get.
//
// The URI on every key a load registers is what lets packages share this run's
// Context without their declarations colliding.
func (c *checker) loadPackage(uri string, span ast.Span) (*Namespace, []SolverError) {
	if ns, found := c.packages.Lookup(uri); found {
		if ns == nil {
			// Found with a nil surface is the loading sentinel: this import
			// re-enters a package still being assembled.
			return nil, []SolverError{&ImportCycleError{
				Chain: c.cycleChain(uri),
				span:  span,
			}}
		}
		return ns, nil
	}
	if c.source == nil {
		return nil, []SolverError{&UnresolvedPackageError{
			URI:    uri,
			Reason: "this inference run was given no module source",
			span:   span,
		}}
	}

	// A package whose group has more than one member cannot load alone: whichever
	// went first would reach a name its sibling has not declared yet. The whole
	// group loads as one module and publishes a namespace each.
	if group, held := c.groups.GroupOf(uri); held && len(group) > 1 {
		errs := c.loadPackageGroup(sortedGroup(group), span)
		// The surface comes back whether or not the group reported. A package that
		// inferred with a diagnostic still declares its names, and binding nothing
		// would turn one error inside the group into an unbound-name error on every
		// reference in the importing file. The single-package path answers the same
		// way.
		ns, _ := c.packages.Lookup(uri)
		return ns, errs
	}

	module, path, err := c.source(uri)
	if err != nil {
		return nil, []SolverError{&UnresolvedPackageError{
			URI:    uri,
			Reason: err.Error(),
			span:   span,
		}}
	}

	c.packages.markLoading(uri, path)
	c.loadStack = append(c.loadStack, uri)
	ns, pkgErrs := c.inferPackage(uri, module)
	c.loadStack = c.loadStack[:len(c.loadStack)-1]
	c.packages.publish(uri, ns)
	if len(pkgErrs) > 0 {
		return ns, []SolverError{&PackageInferenceError{
			URI:      uri,
			Path:     path,
			Messages: messagesOf(pkgErrs),
			span:     span,
		}}
	}
	return ns, nil
}

// cycleChain returns the packages the cycle runs through, starting and ending at
// the re-entered URI: `pkg:a` importing `pkg:b` importing `pkg:a` gives a, b, a.
// A package loaded before the cycle opened is not part of the loop.
func (c *checker) cycleChain(uri string) []string {
	start := 0
	for i, loading := range c.loadStack {
		if loading == uri {
			start = i
			break
		}
	}
	chain := make([]string, 0, len(c.loadStack)-start+1)
	chain = append(chain, c.loadStack[start:]...)
	return append(chain, uri)
}

// ImportCycleError reports an import that closes a cycle between packages. It is
// reported where the cycle closes, so it reaches the entry module through the
// wrapping every package diagnostic takes, and one cycle gives one diagnostic
// rather than one per name the cycle left unbound.
type ImportCycleError struct {
	// Chain is the packages the cycle runs through, opening and closing on the
	// same URI.
	Chain []string
	span  ast.Span
}

func (e *ImportCycleError) Message() string {
	quoted := make([]string, 0, len(e.Chain))
	for _, uri := range e.Chain {
		quoted = append(quoted, strconv.Quote(uri))
	}
	return "import cycle: " + strings.Join(quoted, " -> ")
}
func (e *ImportCycleError) Span() ast.Span      { return e.span }
func (e *ImportCycleError) Related() []ast.Span { return nil }
func (e *ImportCycleError) isSolverError()      {}

// messagesOf renders each diagnostic's message.
func messagesOf(errs []SolverError) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Message())
	}
	return out
}

// inferPackage infers one package's module and returns the surface it exports.
//
// The package's declarations go into a child of the run's prelude scope rather
// than of the importer, so nothing the importer declares is visible to it while
// the prelude package's exports still are. pkgURI is set for the duration, which
// puts the URI on every key the walk registers.
func (c *checker) inferPackage(uri string, module *ast.Module) (*Namespace, []SolverError) {
	prevURI := c.pkgURI
	c.pkgURI = uri
	defer func() { c.pkgURI = prevURI }()

	// A package's diagnostics belong to the package, not to whatever the
	// importer was in the middle of. Collect them separately and hand them back.
	prevErrs := c.errs
	c.errs = nil

	scope := c.preludeScope().Child()
	c.bindFileImports(scope, module)
	c.inferDepGraph(scope, 0, module, dep_graph.BuildDepGraph(module))

	errs := c.errs
	c.errs = prevErrs
	return exportedSurface(uri, module, scope), errs
}

// exportedSurface copies the bindings of module's exported top-level
// declarations out of scope and into a namespace an importer can read. An
// unexported declaration is left behind: the whole module was inferred into one
// scope, and this walk is the only thing narrowing it to a public surface.
func exportedSurface(uri string, module *ast.Module, scope *Scope) *Namespace {
	surface := newNamespace(uri)
	module.Namespaces.Scan(func(nsPath string, ns *ast.Namespace) bool {
		target := surface
		if nsPath != "" {
			target = surface.nest(nsPath)
		}
		for _, decl := range ns.Decls {
			if !decl.Export() {
				continue
			}
			// A `namespace` block carries its whole Namespace onto the surface, since
			// its members are reached through it rather than named beside it.
			// exportedNames answers nothing for a block, so it is taken here instead.
			if nsDecl, isNS := decl.(*ast.NamespaceDecl); isNS {
				if nsDecl.Name != nil && nsDecl.Name.Name != "" {
					if bound, ok := scope.GetNamespace(qualify(nsPath, nsDecl.Name.Name)); ok {
						target.Nested[nsDecl.Name.Name] = exportedMembers(nsDecl, bound)
					}
				}
				continue
			}
			for _, name := range exportedNames(decl) {
				// A value binds under the plain namespace-qualified name the dep-graph
				// walk defined it under. A type binds under the key its registration
				// used, which carries the package URI as well. Both are re-keyed to the
				// bare name here, since an importer names a member without either
				// qualifier.
				if b, ok := scope.GetValue(qualify(nsPath, name)); ok {
					target.Values[name] = b
				}
				if b, ok := scope.GetType(qualify(packageKeyPrefix(uri), qualify(nsPath, name))); ok {
					target.Types[name] = b
				}
				// An enum binds its variant constructors under a namespace of its own
				// name. Without it the type arrives but `Color.Red()` names nothing.
				// Only an enum: any other declaration sharing a name with an unrelated
				// namespace would otherwise pick that namespace up.
				if _, isEnum := decl.(*ast.EnumDecl); isEnum {
					if enumNs, ok := scope.GetNamespace(name); ok {
						target.Nested[name] = enumNs
					}
				}
			}
		}
		return true
	})
	return surface
}

// newNamespace returns an empty namespace under the given name.
func newNamespace(name string) *Namespace {
	return &Namespace{
		Name:   name,
		Values: map[string]ValueBinding{},
		Types:  map[string]TypeBinding{},
		Nested: map[string]*Namespace{},
	}
}

// nest returns the namespace at a dotted path under root, creating each level
// that does not exist yet. A package declaring `Geo.Shapes.Point` reaches it
// through two nested namespaces, which is what a member access walks.
func (n *Namespace) nest(path string) *Namespace {
	cur := n
	for _, segment := range strings.Split(path, ".") {
		next, ok := cur.Nested[segment]
		if !ok {
			next = newNamespace(qualify(cur.Name, segment))
			cur.Nested[segment] = next
		}
		cur = next
	}
	return cur
}

// qualify joins a prefix and a name with a dot, returning the name alone when
// the prefix is empty.
func qualify(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// exportedNames returns the names a top-level declaration introduces. A `val`
// binding a pattern introduces one name per leaf, so a destructuring export
// carries every name it binds.
func exportedNames(decl ast.Decl) []string {
	switch d := decl.(type) {
	case *ast.VarDecl:
		return patternNames(d.Pattern)
	case *ast.FuncDecl:
		if d.Name != nil {
			return []string{d.Name.Name}
		}
	case *ast.ClassDecl:
		if d.Name != nil {
			return []string{d.Name.Name}
		}
	case *ast.TypeDecl:
		if d.Name != nil {
			return []string{d.Name.Name}
		}
	case *ast.InterfaceDecl:
		if d.Name != nil {
			return []string{d.Name.Name}
		}
	case *ast.EnumDecl:
		if d.Name != nil {
			return []string{d.Name.Name}
		}
	}
	return nil
}

// patternNames returns every identifier a binding pattern introduces, in source
// order. Extractor and instance patterns bind through their sub-patterns, so
// this reads the leaves through the shared walk rather than its own traversal.
func patternNames(pat ast.Pat) []string {
	var names []string
	ast.ForEachLeafBinding(pat, func(name string, _ int) {
		names = append(names, name)
	})
	return names
}

// PackageInferenceError reports that a package failed to type-check, on the
// import that pulled it in.
//
// The package's diagnostics are carried as text rather than as errors. Each span
// points into the package's source, whose ids belong to a different module, so
// re-reporting them directly would blame the importing file at an unrelated
// offset.
type PackageInferenceError struct {
	// URI is the package that failed.
	URI string
	// Path is the file the package was read from.
	Path string
	// Messages are the package's own diagnostics, in the order it reported them.
	Messages []string
	span     ast.Span
}

func (e *PackageInferenceError) Message() string {
	return fmt.Sprintf("package %q (%s) has %d error(s):\n  %s",
		e.URI, e.Path, len(e.Messages), strings.Join(e.Messages, "\n  "))
}
func (e *PackageInferenceError) Span() ast.Span      { return e.span }
func (e *PackageInferenceError) Related() []ast.Span { return nil }
func (e *PackageInferenceError) isSolverError()      {}

// UnresolvedPackageError reports an `import` whose URI no package answers.
type UnresolvedPackageError struct {
	// URI is the module specifier the import wrote.
	URI string
	// Reason says why the URI did not resolve, in the module source's words.
	Reason string
	span   ast.Span
}

func (e *UnresolvedPackageError) Message() string {
	return fmt.Sprintf("cannot resolve import %q: %s", e.URI, e.Reason)
}
func (e *UnresolvedPackageError) Span() ast.Span      { return e.span }
func (e *UnresolvedPackageError) Related() []ast.Span { return nil }
func (e *UnresolvedPackageError) isSolverError()      {}

// exportedMembers returns a copy of ns holding only what decl exports, recursing
// into the blocks written inside it.
//
// A block's own `export` says the block is part of the package's surface. It says
// nothing about the members inside it, each of which carries its own flag, so
// handing the bound Namespace over whole would publish a package's internals.
func exportedMembers(decl *ast.NamespaceDecl, ns *Namespace) *Namespace {
	out := newNamespace(ns.Name)
	for _, inner := range decl.Decls {
		if nested, isNS := inner.(*ast.NamespaceDecl); isNS {
			if !nested.Export() || nested.Name == nil || nested.Name.Name == "" {
				continue
			}
			if child, held := ns.Nested[nested.Name.Name]; held {
				out.Nested[nested.Name.Name] = exportedMembers(nested, child)
			}
			continue
		}
		if !inner.Export() {
			continue
		}
		for _, name := range exportedNames(inner) {
			if b, held := ns.Values[name]; held {
				out.Values[name] = b
			}
			if b, held := ns.Types[name]; held {
				out.Types[name] = b
			}
		}
	}
	return out
}
