package solver

import (
	"fmt"
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
// A URI already being loaded further up this call chain returns nil with no
// diagnostic. That is a cycle: A imports B, and B imports A while A's own
// surface is still being assembled. The importing side binds nothing and carries
// on, which is what terminates the walk.
//
// B therefore sees none of A. A cyclic pair does not type-check against each
// other's declarations; it finishes, and each name the cycle left unbound is
// reported where it is used. Breaking that limit is a later phase.
//
// The package is inferred under this run's Context, so a class it declares and
// a reference the importer writes to that class are one entry in one nominal
// registry. What keeps that safe is the URI on every key the load registers.
//
// A package that reports diagnostics of its own is still published. Its
// declarations are the best available answer for an importer, and re-loading it
// on the next import would report the same errors again.
func (c *checker) loadPackage(uri string, span ast.Span) (*Namespace, []SolverError) {
	if ns, found := c.packages.Lookup(uri); found {
		return ns, nil
	}
	if c.source == nil {
		return nil, []SolverError{&UnresolvedPackageError{
			URI:    uri,
			Reason: "this inference run was given no module source",
			span:   span,
		}}
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
	ns, pkgErrs := c.inferPackage(uri, module)
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
// The package's own declarations go into a scope of its own, a child of the
// prelude rather than of the importer, so nothing the importer declares is
// visible to it. pkgURI is set for the duration, which is what puts the URI on
// every key the walk registers.
func (c *checker) inferPackage(uri string, module *ast.Module) (*Namespace, []SolverError) {
	prevURI := c.pkgURI
	c.pkgURI = uri
	defer func() { c.pkgURI = prevURI }()

	// A package's diagnostics belong to the package, not to whatever the
	// importer was in the middle of. Collect them separately and hand them back.
	prevErrs := c.errs
	c.errs = nil

	scope := sharedPrelude().Child()
	c.bindFileImports(scope, module)
	c.inferDepGraph(scope, 0, module, dep_graph.BuildDepGraph(module))

	errs := c.errs
	c.errs = prevErrs
	return exportedSurface(uri, module, scope), errs
}

// exportedSurface copies the bindings of module's exported top-level
// declarations out of scope and into a namespace an importer can read.
//
// An unexported declaration is left behind, so a consumer cannot name it. That
// is the only thing separating a package's public surface from its internals:
// the whole module was inferred into one scope, and this walk is what narrows
// it.
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
// through two nested namespaces rather than through one dotted key, which is
// what a member access walks.
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
	case *ast.EnumDecl:
		if d.Name != nil {
			return []string{d.Name.Name}
		}
	}
	return nil
}

// patternNames returns every identifier a binding pattern introduces, in source
// order. An extractor and an instance pattern bind through their sub-patterns,
// which is why this reads the leaves through the shared walk rather than a
// traversal of its own.
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
// The package's own diagnostics are carried as text rather than as errors. Each
// one's span points into the package's source, whose ids belong to a different
// module than the importer's, so re-reporting them directly would blame the
// importing file at whatever offset the package's error happened to sit at.
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
