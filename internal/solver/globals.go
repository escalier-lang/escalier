package solver

import (
	"maps"
	"slices"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/tidwall/btree"
)

// globals.go adds the global declarations of an npm package's `.d.ts` files to the
// globals scope, where the entry module and every npm package read them. A run with
// Options.NoAmbientNames set keeps them out of the entry module's reach, as it does
// every ambient name. A `.d.ts` declares globals in two ways. A file with no
// top-level export declares only globals, and a module adds to them with
// `declare global { … }`. NodeModulesSource puts both in the Globals of the module it
// returns.
//
// inferPackage infers the globals in one module with the package's own declarations,
// since each may name the other. publishGlobals then copies the globals' bindings to
// the globals scope. A global whose name the globals scope already binds merges with
// that binding by these rules:
//
//   - An interface takes the members of the type its name binds as well as its own.
//     An interface with no members, no `extends` clause, and no type parameters
//     leaves the binding in place, since merging it adds nothing. So does an
//     interface whose type parameters differ in number from the binding's, which
//     reports InterfaceTypeParamMismatchError.
//   - A namespace adds each member whose name the bound namespace does not hold.
//   - Any other declaration leaves the binding in place, and so does an interface
//     whose name binds a class. The first declaration to claim a name keeps it, the
//     rule ambientPackages follows.

// globalsMerge is what the package under inference contributes to the globals scope.
type globalsMerge struct {
	// globals holds the global declarations claimGlobals claimed for the package.
	globals *ast.Module
	// interfaces holds every root interface declaration among the globals.
	interfaces set.Set[*ast.InterfaceDecl]
	// merged holds the name of every interface that took the members of the type the
	// globals scope binds under its name.
	merged set.Set[string]
}

// claimGlobals returns the global declarations of module that no earlier package has
// bound in this run, or nil when there are none, and records their files as bound. A
// file's globals bind once per run, however many packages reach the file.
//
// It leaves out each root interface that would merge with a globals-scope binding and
// add nothing to it, so a reference to that name reads the binding. noOpInterfaces
// names those interfaces.
func (c *checker) claimGlobals(module *ast.Module) *ast.Module {
	if module.Globals == nil {
		return nil
	}
	globals := module.Globals
	if c.boundGlobalFiles == nil {
		c.boundGlobalFiles = set.NewSet[string]()
	}
	unbound := func(decl ast.Decl) bool {
		return !c.boundGlobalFiles.Contains(globals.GetSourcePath(decl.Span().SourceID))
	}
	noOp := c.noOpInterfaces(globals, unbound)

	var namespaces btree.Map[string, *ast.Namespace]
	kept := 0
	globals.Namespaces.Scan(func(nsPath string, ns *ast.Namespace) bool {
		var decls []ast.Decl
		for _, decl := range ns.Decls {
			if !unbound(decl) {
				continue
			}
			if iface, isIface := decl.(*ast.InterfaceDecl); isIface && nsPath == "" && noOp.Contains(iface.Name.Name) {
				continue
			}
			decls = append(decls, decl)
		}
		kept += len(decls)
		namespaces.Set(nsPath, &ast.Namespace{Decls: decls, Exported: ns.Exported})
		return true
	})
	for _, source := range globals.Sources {
		c.boundGlobalFiles.Add(source.Path)
	}
	if kept == 0 {
		return nil
	}
	claimed := ast.NewModule(namespaces)
	claimed.Sources = globals.Sources
	return claimed
}

// noOpInterfaces returns the name of every root interface of globals that the globals
// scope already binds and whose declarations isEmptyInterface accepts. It counts only
// the declarations unbound accepts.
func (c *checker) noOpInterfaces(globals *ast.Module, unbound func(ast.Decl) bool) set.Set[string] {
	declared := set.NewSet[string]()
	nonEmpty := set.NewSet[string]()
	if root, held := globals.Namespaces.Get(""); held {
		for _, decl := range root.Decls {
			iface, isIface := decl.(*ast.InterfaceDecl)
			if !isIface || !unbound(decl) {
				continue
			}
			declared.Add(iface.Name.Name)
			if !isEmptyInterface(iface) {
				nonEmpty.Add(iface.Name.Name)
			}
		}
	}
	noOp := set.NewSet[string]()
	for name := range declared {
		if _, bound := c.globalsScope().GetType(name); bound && !nonEmpty.Contains(name) {
			noOp.Add(name)
		}
	}
	return noOp
}

// isEmptyInterface reports whether decl writes no members, no `extends` clause, and
// no type parameters.
func isEmptyInterface(decl *ast.InterfaceDecl) bool {
	return len(decl.Extends) == 0 && len(decl.TypeParams) == 0 &&
		(decl.TypeAnn == nil || len(decl.TypeAnn.Elems) == 0)
}

// withGlobals returns a module holding the declarations of both module and globals.
// In each namespace, the declarations of module come ahead of those of globals.
func withGlobals(module, globals *ast.Module) *ast.Module {
	var namespaces btree.Map[string, *ast.Namespace]
	module.Namespaces.Scan(func(nsPath string, ns *ast.Namespace) bool {
		namespaces.Set(nsPath, &ast.Namespace{Decls: slices.Clone(ns.Decls), Exported: ns.Exported})
		return true
	})
	globals.Namespaces.Scan(func(nsPath string, ns *ast.Namespace) bool {
		if held, ok := namespaces.Get(nsPath); ok {
			held.Decls = append(held.Decls, ns.Decls...)
			return true
		}
		namespaces.Set(nsPath, &ast.Namespace{Decls: ns.Decls, Exported: ns.Exported})
		return true
	})
	merged := ast.NewModuleWithFiles(namespaces, module.Files, module.Sources)
	merged.Comments = module.Comments
	return merged
}

// newGlobalsMerge returns the globalsMerge for globals, the declarations claimGlobals
// returned.
func newGlobalsMerge(globals *ast.Module) *globalsMerge {
	merge := &globalsMerge{
		globals:    globals,
		interfaces: set.NewSet[*ast.InterfaceDecl](),
		merged:     set.NewSet[string](),
	}
	if root, held := globals.Namespaces.Get(""); held {
		for _, decl := range root.Decls {
			if iface, isIface := decl.(*ast.InterfaceDecl); isIface {
				merge.interfaces.Add(iface)
			}
		}
	}
	return merge
}

// mergesWithGlobal reports whether the interface whose declarations are decls takes
// the members of the type the globals scope binds under name. That holds when one of
// decls is a global of the package under inference and the type is not a class. A
// name resolveTypeAnn builds a type for itself, such as `Generator`, never merges,
// since publishGlobals leaves it out.
func (c *checker) mergesWithGlobal(name string, decls []*ast.InterfaceDecl) bool {
	if c.globalsMerge == nil || builtinTypeNames.Contains(name) ||
		!slices.ContainsFunc(decls, c.globalsMerge.interfaces.Contains) {
		return false
	}
	b, bound := c.globalsScope().GetType(name)
	if !bound {
		return false
	}
	_, isClass := b.Type.(*soltype.ClassType)
	return !isClass
}

// publishGlobals copies the root bindings of the declarations in merge.globals from
// scope to the globals scope. scope is the scope the package inferred into. The copy
// follows the merge rules at the top of this file. A type that resolveTypeAnn builds
// itself, such as `Generator`, is left out, the way ambientBinder leaves it out.
func (c *checker) publishGlobals(scope *Scope, merge *globalsMerge) {
	target := c.globalsScope()
	values, types, namespaces := rootNames(merge.globals)
	for _, name := range slices.Sorted(maps.Keys(values)) {
		b, held := scope.OwnValue(name)
		if _, taken := target.GetValue(name); held && !taken {
			target.defineValue(name, b)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(types)) {
		b, held := scope.OwnType(name)
		if !held || builtinTypeNames.Contains(name) {
			continue
		}
		// A merged interface already holds the members of the binding it replaces.
		if _, taken := target.GetType(name); !taken || merge.merged.Contains(name) {
			target.defineType(name, b)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(namespaces)) {
		ns, held := scope.OwnNamespace(name)
		if !held {
			continue
		}
		if bound, owned := target.OwnNamespace(name); owned {
			addMissingMembers(bound, ns)
			continue
		}
		if _, taken := target.GetNamespace(name); !taken {
			target.defineNamespace(name, ns)
		}
	}
}

// rootNames returns the names module declares at its root, split into values, types,
// and namespaces. A namespace is named by the first segment of its path, so
// `namespace A.B` names `A`.
func rootNames(module *ast.Module) (values, types, namespaces set.Set[string]) {
	declared := topLevelNames(module)
	namespaces = set.NewSet[string]()
	module.Namespaces.Scan(func(nsPath string, _ *ast.Namespace) bool {
		if nsPath != "" {
			head, _, _ := strings.Cut(nsPath, ".")
			namespaces.Add(head)
		}
		return true
	})
	return declared.values, declared.types, namespaces
}

// addMissingMembers adds to into each value, type, and nested namespace of from whose
// name into does not hold.
func addMissingMembers(into, from *Namespace) {
	for name, b := range from.Values {
		if _, held := into.Values[name]; !held {
			into.Values[name] = b
		}
	}
	for name, b := range from.Types {
		if _, held := into.Types[name]; !held {
			into.Types[name] = b
		}
	}
	for name, nested := range from.Nested {
		if _, held := into.Nested[name]; !held {
			into.Nested[name] = nested
		}
	}
}
