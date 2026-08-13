package codegen

import (
	"slices"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/set"
)

// internalAlias is the local name the public wrapper binds the internal bundle to, so a
// re-export reads `internal.rootPub`. It is a module-local binding in the generated file
// and never reaches a consumer.
const internalAlias = "internal"

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
// straight out of the internal bundle and a nested namespace becomes an object literal.
//
// A member wins over a child namespace spelled the same way, matching the internal bundle,
// where the member assignment runs after the namespace object is created and overwrites it.
func (p *nsProjection) entry(path []string, name string) Expr {
	memberPath := slices.Concat(path, []string{name})
	if p.members.Contains(name) {
		return internalRef(memberPath)
	}
	return buildProjectionObject(p.children[name], memberPath)
}

// BuildPublicWrapper builds the package's public entry point, a module that re-exports the
// part of the internal bundle at internalPath that the source marked `export`.
//
// The internal bundle exports every declaration and puts every namespace member on its
// namespace object, so a bin script in the same package can reach members the package keeps
// to itself. An external consumer imports this wrapper instead, which re-exports the exported
// root declarations and rebuilds each namespace object from its exported members.
//
//	import * as internal from "./internal.js";
//	export const geo = {pub: internal.geo.pub};
//	export const rootPub = internal.rootPub;
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

	stmts := buildProjectionExports(root)
	if len(stmts) == 0 {
		return &Module{Stmts: nil}
	}

	importStmt := &DeclStmt{
		Decl:   NewNamespaceImportDecl(internalAlias, internalPath, nil),
		span:   nil,
		source: nil,
	}
	return &Module{Stmts: append([]Stmt{importStmt}, stmts...)}
}

// exportedBinding reports whether the binding belongs in the public wrapper. A binding is
// exported when at least one of the declarations behind it carries the `export` keyword.
// Overloaded functions and merged interfaces put several declarations under one key.
//
// An ambient declaration names something the runtime already provides, and the internal
// bundle emits no definition for it, so there is nothing for the wrapper to re-export.
func exportedBinding(depGraph *dep_graph.DepGraph, key dep_graph.BindingKey) bool {
	for _, decl := range depGraph.GetDecls(key) {
		if decl.Export() && !decl.Declare() {
			return true
		}
	}
	return false
}

// buildProjectionExports emits one `export const` per entry of the module's root namespace.
// A root declaration is re-exported by name and a namespace is re-exported as an object
// literal holding its exported members.
func buildProjectionExports(root *nsProjection) []Stmt {
	var stmts []Stmt
	for _, name := range root.keys() {
		decl := &VarDecl{
			Kind: ValKind,
			Decls: []*Declarator{{
				Pattern: NewIdentPat(name, nil, nil),
				TypeAnn: nil,
				Init:    root.entry(nil, name),
			}},
			export:  true,
			declare: false,
			span:    nil,
			source:  nil,
		}
		stmts = append(stmts, &DeclStmt{Decl: decl, span: nil, source: nil})
	}
	return stmts
}

// buildProjectionObject renders one namespace as an object literal. path locates the namespace
// in the internal bundle, so the `pub` member of namespace `geo` becomes the property
// `pub: internal.geo.pub`. Nested namespaces become nested object literals.
func buildProjectionObject(node *nsProjection, path []string) Expr {
	var elems []ObjExprElem
	for _, name := range node.keys() {
		elems = append(elems, NewPropertyExpr(
			NewIdentExpr(name, "", nil),
			node.entry(path, name),
			nil,
		))
	}
	return NewObjectExpr(elems, nil)
}

// internalRef builds the member chain that reads a name out of the internal bundle, so the
// path ["geo", "pub"] becomes `internal.geo.pub`.
func internalRef(path []string) Expr {
	var expr Expr = NewIdentExpr(internalAlias, "", nil)
	for _, segment := range path {
		expr = NewMemberExpr(expr, NewIdentifier(segment, nil), false, nil)
	}
	return expr
}
