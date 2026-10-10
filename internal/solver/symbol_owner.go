package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
)

// symbol_owner.go picks the value whose path names the unique symbols an interface or a
// type alias declares.
//
// `interface I { readonly key: unique symbol }` declares a symbol no declaration path
// reaches on its own. TypeScript prints such a symbol through a value whose declared type
// is the interface, so after `declare val i: I` the symbol renders as `typeof i.key` and a
// key off it as `[i.key]`. This file finds that value, called the symbol owner below.
//
// A symbol's name is part of the reserved member name every key off it is stored under,
// so the name has to be final when the symbol is minted. The interface body is resolved,
// and its symbols minted, before any `val` that names the interface. The owner is
// therefore chosen from the source alone, before the dependency graph is walked.

// symbolOwners maps the name of each type key in g to the path of its symbol owner. The
// owner of a type is a top-level `val` whose annotation is a reference to that type, such
// as `declare val i: I`. The path is namespace-qualified, so a `val i: I` in namespace
// `ns` gives `ns.i`. A type no such `val` refers to has no entry.
//
// When several `val`s qualify, one at the module root wins over one in a namespace, and
// otherwise the first in source order wins. The `.d.ts` emitter writes `[i.key]` only
// through a root binding, so preferring one keeps every key it can write.
//
// Only a `val` binding a single name qualifies. A `var` may be reassigned, and a
// destructuring pattern binds no one name for the path to start from.
//
// The reference is resolved the way the dependency graph resolves it, through
// dep_graph.ResolveTypeKey. A type the graph does not declare, such as one an import
// brings in, has its symbols minted by another run and gets no owner here.
func symbolOwners(module *ast.Module, g *dep_graph.DepGraph) map[string]string {
	type candidate struct {
		decl *ast.VarDecl
		ns   string
	}
	// before reports whether a is a better owner than b.
	before := func(a, b candidate) bool {
		if (a.ns == "") != (b.ns == "") {
			return a.ns == ""
		}
		return declPosLess(module, a.decl, b.decl)
	}
	owners := map[string]string{}
	best := map[string]candidate{}
	for _, key := range g.AllBindings() {
		if !key.IsValueBinding() {
			continue
		}
		ns := g.GetNamespace(key)
		for _, d := range g.GetDecls(key) {
			decl, ok := d.(*ast.VarDecl)
			if !ok || decl.Kind != ast.ValKind {
				continue
			}
			ident, ok := decl.Pattern.(*ast.IdentPat)
			if !ok {
				continue
			}
			ref, ok := decl.TypeAnn.(*ast.TypeRefTypeAnn)
			if !ok {
				continue
			}
			typeKey, ok := g.ResolveTypeKey(ast.QualIdentToString(ref.Name), ns)
			if !ok {
				continue
			}
			typeName := typeKey.Name()
			this := candidate{decl: decl, ns: ns}
			if prev, seen := best[typeName]; seen && !before(this, prev) {
				continue
			}
			best[typeName] = this
			owners[typeName] = declScopeKey(ns, ident.Name)
		}
	}
	return owners
}
