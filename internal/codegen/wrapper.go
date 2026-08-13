package codegen

import (
	"slices"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/set"
)

// nsProjection is one namespace of the module, holding the parts of it that belong in the
// public wrapper. Members are the names the source marked `export`, and children are the
// namespaces nested inside this one. A namespace with neither is left out of the wrapper.
type nsProjection struct {
	members  set.Set[string]
	children map[string]*nsProjection
}

func newNsProjection() *nsProjection {
	return &nsProjection{
		members:  set.NewSet[string](),
		children: map[string]*nsProjection{},
	}
}

// child returns the projection for the namespace at path, creating a projection for it and
// for every namespace along the way. path is dotted and relative to p, so "utils.text" walks
// two levels down.
func (p *nsProjection) child(path string) *nsProjection {
	node := p
	for _, segment := range strings.Split(path, ".") {
		next, ok := node.children[segment]
		if !ok {
			next = newNsProjection()
			node.children[segment] = next
		}
		node = next
	}
	return node
}

// empty reports whether the namespace contributes nothing to the public wrapper. Emptiness is
// transitive, so a namespace holding only empty namespaces is itself empty.
func (p *nsProjection) empty() bool {
	if p.members.Len() > 0 {
		return false
	}
	for _, child := range p.children {
		if !child.empty() {
			return false
		}
	}
	return true
}

// keys returns the names the namespace contributes to the wrapper, sorted so the generated
// file is stable across builds. Members and non-empty child namespaces share one name space,
// matching how the internal bundle puts both on the same namespace object, so a name that is
// both appears once.
func (p *nsProjection) keys() []string {
	keys := p.members.Clone()
	for name, child := range p.children {
		if !child.empty() {
			keys.Add(name)
		}
	}
	sorted := keys.ToSlice()
	sort.Strings(sorted)
	return sorted
}

// entry renders one name the namespace contributes. path locates the namespace itself, so the
// `pub` member of namespace `geo` is reached by path ["geo"] and name "pub". A member reads
// straight out of the internal bundle, which the wrapper binds to alias, and a nested namespace
// becomes an object literal.
//
// A member wins over a child namespace spelled the same way, matching the internal bundle,
// where the member assignment runs after the namespace object is created and overwrites it.
func (p *nsProjection) entry(alias string, path []string, name string) Expr {
	memberPath := slices.Concat(path, []string{name})
	if p.members.Contains(name) {
		return internalRef(alias, memberPath)
	}
	return buildProjectionObject(p.children[name], alias, memberPath)
}

// BuildPublicWrapper builds the package's public entry point, a module that re-exports the
// part of the internal bundle at internalPath that the source marked `export`.
//
// The internal bundle exports every declaration and puts every namespace member on its
// namespace object, so a bin script in the same package can reach members the package keeps
// to itself. An external consumer imports this wrapper instead, which forwards the exported
// root declarations and rebuilds each namespace object from its exported members.
//
//	import * as internal from "./internal.js";
//	export { rootPub } from "./internal.js";
//	export const geo = {pub: internal.geo.pub};
//
// Each class is defined once, in the internal bundle, and both entry points name that one
// definition. An instance created through either entry point satisfies `instanceof` against
// the other, which pattern matching relies on.
func BuildPublicWrapper(depGraph *dep_graph.DepGraph, internalPath string) *Module {
	root := newNsProjection()

	for _, key := range depGraph.AllBindings() {
		if key.Kind() != dep_graph.DepKindValue || !exportedBinding(depGraph, key) {
			continue
		}

		nsName := depGraph.GetNamespace(key)
		memberName := key.Name()
		node := root
		if nsName != "" {
			node = root.child(nsName)
			memberName = strings.TrimPrefix(memberName, nsName+".")
		}
		node.members.Add(memberName)
	}

	var rootNames, nsNames []string
	for _, name := range root.keys() {
		if root.members.Contains(name) {
			rootNames = append(rootNames, name)
		} else {
			nsNames = append(nsNames, name)
		}
	}

	var stmts []Stmt

	// A root declaration is forwarded rather than copied, which keeps it a live binding. A
	// consumer reading `counter` after an exported function reassigned it sees the new
	// value, where `export const counter = internal.counter` would freeze the value this
	// module read as it loaded.
	if len(rootNames) > 0 {
		stmts = append(stmts, &DeclStmt{
			Decl:   NewReExportDecl(rootNames, internalPath, nil),
			span:   nil,
			source: nil,
		})
	}

	if len(nsNames) == 0 {
		return &Module{Stmts: stmts}
	}

	// A namespace object has to be rebuilt property by property, so the wrapper needs the
	// internal bundle under a local name to read those properties from.
	alias := wrapperAlias(nsNames)
	importStmt := &DeclStmt{
		Decl:   NewNamespaceImportDecl(alias, internalPath, nil),
		span:   nil,
		source: nil,
	}

	for _, name := range nsNames {
		decl := &VarDecl{
			Kind: ValKind,
			Decls: []*Declarator{{
				Pattern: NewIdentPat(name, nil, nil),
				TypeAnn: nil,
				Init:    root.entry(alias, nil, name),
			}},
			export:  true,
			declare: false,
			span:    nil,
			source:  nil,
		}
		stmts = append(stmts, &DeclStmt{Decl: decl, span: nil, source: nil})
	}

	return &Module{Stmts: slices.Concat([]Stmt{importStmt}, stmts)}
}

// wrapperAlias picks the local name the wrapper binds the internal bundle to. It is `internal`
// unless the wrapper declares a namespace object spelled that way, in which case underscores
// are appended until the name is free. Reusing the name would redeclare the binding and the
// entry point would fail to load.
//
// declared holds the names the wrapper introduces as bindings. A forwarded root declaration is
// not among them, because `export {x} from "./internal.js"` binds nothing locally.
func wrapperAlias(declared []string) string {
	taken := set.FromSlice(declared)
	alias := "internal"
	for taken.Contains(alias) {
		alias += "_"
	}
	return alias
}

// exportedBinding reports whether the binding belongs in the public wrapper. A binding is
// exported when at least one of the declarations behind it carries the `export` keyword.
// Overloaded functions and merged interfaces put several declarations under one key.
//
// An ambient declaration names something the runtime already provides, and the internal
// bundle emits no definition for it, so there is nothing for the wrapper to forward. The same
// goes for a declaration the bundle does not bind under the name the wrapper would name it by.
// Forwarding a name the bundle does not export is a link error that keeps the whole entry
// point from loading, not just that one binding.
func exportedBinding(depGraph *dep_graph.DepGraph, key dep_graph.BindingKey) bool {
	for _, decl := range depGraph.GetDecls(key) {
		if decl.Export() && !decl.Declare() && bindsUnderExpectedName(decl) {
			return true
		}
	}
	return false
}

// buildProjectionObject renders one namespace as an object literal. path locates the namespace
// in the internal bundle, so the `pub` member of namespace `geo` becomes the property
// `pub: internal.geo.pub`. Nested namespaces become nested object literals.
func buildProjectionObject(node *nsProjection, alias string, path []string) Expr {
	var elems []ObjExprElem
	for _, name := range node.keys() {
		elems = append(elems, NewPropertyExpr(
			NewIdentExpr(name, "", nil),
			node.entry(alias, path, name),
			nil,
		))
	}
	return NewObjectExpr(elems, nil)
}

// internalRef builds the member chain that reads a name out of the internal bundle, so alias
// "internal" and path ["geo", "pub"] become `internal.geo.pub`.
func internalRef(alias string, path []string) Expr {
	var expr Expr = NewIdentExpr(alias, "", nil)
	for _, segment := range path {
		expr = NewMemberExpr(expr, NewIdentifier(segment, nil), false, nil)
	}
	return expr
}
