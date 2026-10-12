package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/soltype"
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
// owner of a type is a top-level value whose annotation is a reference to that type. Two
// kinds of value qualify:
//   - a `val` binding a single name, such as `declare val i: I`, whose path is `i`
//   - a `static readonly` class field named by an identifier, such as the `i` in
//     `declare class C { static readonly i: I }`, whose path is `C.i`
//
// The path is namespace-qualified, so a `val i: I` in namespace `ns` gives `ns.i`. A type
// no such value refers to has no entry.
//
// When several values qualify, one at the module root wins over one in a namespace, and
// otherwise the first in source order wins. The `.d.ts` emitter writes `[i.key]` only
// through a root binding, so preferring one keeps every key it can write.
//
// A `var` or a writable field may be reassigned, and a destructuring pattern binds no one
// name for the path to start from, so none of them qualifies. A private field has no path
// outside the class.
//
// The reference is resolved the way the dependency graph resolves it, through
// dep_graph.ResolveTypeKey. A type the graph does not declare, such as one an import
// brings in, has its symbols minted by another run and gets no owner here.
func symbolOwners(module *ast.Module, g *dep_graph.DepGraph) map[string]string {
	type candidate struct {
		span ast.Span
		ns   string
	}
	// before reports whether a is a better owner than b.
	before := func(a, b candidate) bool {
		if (a.ns == "") != (b.ns == "") {
			return a.ns == ""
		}
		ap, bp := module.GetSourcePath(a.span.SourceID), module.GetSourcePath(b.span.SourceID)
		if ap != bp {
			return ap < bp
		}
		return a.span.Start.Offset < b.span.Start.Offset
	}
	owners := map[string]string{}
	best := map[string]candidate{}
	consider := func(typeAnn ast.TypeAnn, ns string, span ast.Span, path string) {
		ref, ok := typeAnn.(*ast.TypeRefTypeAnn)
		if !ok {
			return
		}
		typeKey, ok := g.ResolveTypeKey(ast.QualIdentToString(ref.Name), ns)
		if !ok {
			return
		}
		typeName := typeKey.Name()
		this := candidate{span: span, ns: ns}
		if prev, seen := best[typeName]; seen && !before(this, prev) {
			return
		}
		best[typeName] = this
		owners[typeName] = path
	}
	for _, key := range g.AllBindings() {
		if !key.IsValueBinding() {
			continue
		}
		ns := g.GetNamespace(key)
		for _, d := range g.GetDecls(key) {
			switch decl := d.(type) {
			case *ast.VarDecl:
				if decl.Kind != ast.ValKind {
					continue
				}
				ident, ok := decl.Pattern.(*ast.IdentPat)
				if !ok {
					continue
				}
				consider(decl.TypeAnn, ns, decl.Span(), declScopeKey(ns, ident.Name))
			case *ast.ClassDecl:
				classPath := declScopeKey(ns, decl.Name.Name)
				for _, elem := range decl.Body {
					field, ok := elem.(*ast.FieldElem)
					if !ok || !field.Static || !field.Readonly || field.Private {
						continue
					}
					ident, ok := field.Name.(*ast.IdentExpr)
					if !ok {
						continue
					}
					consider(field.Type, ns, field.Span(), classPath+"."+ident.Name)
				}
			}
		}
	}
	return owners
}

// declaredSymbols returns the unique symbols the type annotations of module's top-level
// declarations minted, in source order. `info` holds the type each annotation resolved
// to. A symbol a declaration inside a function body or a `do` block mints is local to
// that block, and the walk never reaches it, since it enters no expression or block.
func declaredSymbols(module *ast.Module, info *Info) []*soltype.UniqueSymbolType {
	v := &declaredSymbolVisitor{info: info}
	module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, d := range ns.Decls {
			d.Accept(v)
		}
		return true
	})
	return v.symbols
}

// declaredSymbolVisitor collects the symbol each `unique symbol` annotation it reaches
// resolved to. An annotation in a position that names no one symbol resolved to
// `symbol` instead, and is passed over.
type declaredSymbolVisitor struct {
	ast.DefaultVisitor
	info    *Info
	symbols []*soltype.UniqueSymbolType
}

func (v *declaredSymbolVisitor) EnterExpr(ast.Expr) bool   { return false }
func (v *declaredSymbolVisitor) EnterBlock(ast.Block) bool { return false }

func (v *declaredSymbolVisitor) EnterTypeAnn(ta ast.TypeAnn) bool {
	if usa, ok := ta.(*ast.UniqueSymbolTypeAnn); ok {
		if sym, ok := v.info.TypeOf(usa).(*soltype.UniqueSymbolType); ok {
			v.symbols = append(v.symbols, sym)
		}
	}
	return true
}
