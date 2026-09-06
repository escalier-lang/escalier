package dts_to_esc

import (
	"fmt"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/set"
)

// js_globals.go answers whether an `@js("...")` argument names a real
// JavaScript runtime path. The answer is read off the pinned `.d.ts` set, which
// is also where every generated `@js` argument comes from, so the two halves
// cannot disagree about which globals exist.
//
// The check runs where the decorator is created rather than where the file is
// loaded. A bad target fails the `generate` run that would write it, instead of
// failing whoever later imports the package.

// jsGlobalAllowList names the `@js` targets Escalier adds that no
// `lib.*.d.ts` declares. `Symbol.customMatcher` is Escalier's own well-known
// symbol.
//
// The pinned set is not the whole web platform. `lib.dom.d.ts` omits APIs the
// specifications define, so a hand-written declaration for one names a target
// this walk does not find. Two answers fit: name it here, or read a second
// source such as the WebIDL a specification publishes, which `CollectJSGlobals`
// takes the same way it takes a `LibInput`.
var jsGlobalAllowList = set.FromSlice([]string{
	"Symbol.customMatcher",
})

// JSGlobals is the set of dotted paths that may appear as an `@js("...")`
// argument. A path is either a top-level runtime value such as `parseInt`, or
// one of its members such as `Math.sin`.
//
// Only value-level names are collected. A `@js` decorator lowers to a JS
// reference, so a name that exists solely as a type would lower to a
// `ReferenceError`. An interface reaches the set through the `declare var` that
// binds it, never on its own.
type JSGlobals struct {
	paths set.Set[string]
}

// CollectJSGlobals reads the runtime globals the pinned `.d.ts` set declares.
//
// Interface bodies merge across files, so the members of `Math` are gathered
// from every `interface Math` in the set before any `declare var Math: Math`
// is resolved against it. That is what carries `Math.f16round`, declared in
// `lib.esnext.float16.d.ts` and reachable only through the ES5 binding.
//
// The walk goes one level deep. No `@js` argument in the generated tree names
// a path with two dots, because a member of a member has no `.d.ts`
// declaration the converter routes.
func CollectJSGlobals(inputs []LibInput) JSGlobals {
	interfaces := map[string][]*dts_parser.InterfaceDecl{}
	indexInterfaces(inputs, interfaces)

	paths := jsGlobalAllowList.Clone()
	for _, input := range inputs {
		for _, stmt := range input.Module.Statements {
			collectGlobalPaths(stmt, interfaces, paths)
		}
	}
	return JSGlobals{paths: paths}
}

// indexInterfaces records every interface declaration in the set under its
// name. A name maps to several declarations because `.d.ts` merges interface
// bodies, both across files and inside a `declare global` block.
func indexInterfaces(inputs []LibInput, into map[string][]*dts_parser.InterfaceDecl) {
	var index func(stmts []dts_parser.Statement)
	index = func(stmts []dts_parser.Statement) {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *dts_parser.InterfaceDecl:
				if s.Name != nil {
					into[s.Name.Name] = append(into[s.Name.Name], s)
				}
			case *dts_parser.GlobalDecl:
				index(s.Statements)
			}
		}
	}
	for _, input := range inputs {
		index(input.Module.Statements)
	}
}

// collectGlobalPaths adds the runtime paths stmt declares to paths. A
// statement that declares no runtime value adds nothing.
func collectGlobalPaths(
	stmt dts_parser.Statement,
	interfaces map[string][]*dts_parser.InterfaceDecl,
	paths set.Set[string],
) {
	switch s := stmt.(type) {
	case *dts_parser.VarDecl:
		if s.Name != nil {
			paths.Add(s.Name.Name)
			for _, member := range boundMembers(s.TypeAnn, interfaces) {
				if name := interfaceMemberName(member); name != "" {
					paths.Add(s.Name.Name + "." + name)
				}
			}
		}

	case *dts_parser.FuncDecl:
		if s.Name != nil {
			paths.Add(s.Name.Name)
		}

	case *dts_parser.ClassDecl:
		if s.Name != nil {
			paths.Add(s.Name.Name)
			for _, member := range s.Members {
				if name := staticMemberName(member); name != "" {
					paths.Add(s.Name.Name + "." + name)
				}
			}
		}

	case *dts_parser.NamespaceDecl:
		if s.Name != nil {
			paths.Add(s.Name.Name)
			// A `declare namespace` holds its own declarations rather than an
			// interface body, so its members come from the statements inside it.
			// `Intl.Segmenter` and `WebAssembly.Table` are both this shape.
			for _, inner := range s.Statements {
				if name := namespaceMemberName(inner); name != "" {
					paths.Add(s.Name.Name + "." + name)
				}
			}
		}

	case *dts_parser.GlobalDecl:
		// A module-shaped lib file declares its globals inside `declare
		// global`, which is where the partitioner reads them from as well.
		// lib.esnext.iterator.d.ts is the one file in the pinned set shaped
		// this way.
		for _, inner := range s.Statements {
			collectGlobalPaths(inner, interfaces, paths)
		}
	}
}

// boundMembers returns the members of the type a `declare var` binds. A
// reference resolves through the merged interface bodies; an inline object
// type carries its members directly. Any other annotation binds a value with
// no member surface to name.
//
// An interface inherits the members of everything it extends, and a binding
// reaches those the same way it reaches the ones declared on the interface
// itself. `seen` stops a cyclic `extends` chain from recurring forever.
func boundMembers(
	ann dts_parser.TypeAnn,
	interfaces map[string][]*dts_parser.InterfaceDecl,
) []dts_parser.InterfaceMember {
	switch t := ann.(type) {
	case *dts_parser.TypeReference:
		return interfaceMembers(typeRefName(t), interfaces, set.NewSet[string]())
	case *dts_parser.ObjectType:
		return t.Members
	}
	return nil
}

// interfaceMembers returns every member the interface named name declares or
// inherits, across all of its merged declarations.
func interfaceMembers(
	name string,
	interfaces map[string][]*dts_parser.InterfaceDecl,
	seen set.Set[string],
) []dts_parser.InterfaceMember {
	if name == "" || seen.Contains(name) {
		return nil
	}
	seen.Add(name)

	var members []dts_parser.InterfaceMember
	for _, iface := range interfaces[name] {
		members = append(members, iface.Members...)
		for _, parent := range iface.Extends {
			// Only a reference names an interface to inherit from. The `.d.ts`
			// grammar allows nothing else in an extends clause, though the field is
			// typed loosely enough to hold one.
			if ref, ok := parent.(*dts_parser.TypeReference); ok {
				members = append(members, interfaceMembers(typeRefName(ref), interfaces, seen)...)
			}
		}
	}
	return members
}

// interfaceMemberName returns the plain-name key of an interface member, or ""
// for a member with no runtime path a decorator can name. A computed key, a
// call signature, and an index signature all fall in the second group.
func interfaceMemberName(member dts_parser.InterfaceMember) string {
	switch m := member.(type) {
	case *dts_parser.MethodSignature:
		return identName(m.Name)
	case *dts_parser.PropertySignature:
		return identName(m.Name)
	case *dts_parser.GetterSignature:
		return identName(m.Name)
	case *dts_parser.SetterSignature:
		return identName(m.Name)
	}
	return ""
}

// staticMemberName returns the plain-name key of a static class member, or ""
// for anything else. An instance member is reached through a receiver rather
// than through the class binding, so it names no path under the class.
func staticMemberName(member dts_parser.ClassMember) string {
	switch m := member.(type) {
	case *dts_parser.MethodDecl:
		if m.Modifiers.Static {
			return identName(m.Name)
		}
	case *dts_parser.PropertyDecl:
		if m.Modifiers.Static {
			return identName(m.Name)
		}
	case *dts_parser.GetterDecl:
		if m.Modifiers.Static {
			return identName(m.Name)
		}
	case *dts_parser.SetterDecl:
		if m.Modifiers.Static {
			return identName(m.Name)
		}
	}
	return ""
}

// namespaceMemberName returns the name a statement inside a `declare
// namespace` contributes to that namespace, or "" for a type-level statement.
func namespaceMemberName(stmt dts_parser.Statement) string {
	switch s := stmt.(type) {
	case *dts_parser.VarDecl:
		if s.Name != nil {
			return s.Name.Name
		}
	case *dts_parser.FuncDecl:
		if s.Name != nil {
			return s.Name.Name
		}
	case *dts_parser.ClassDecl:
		if s.Name != nil {
			return s.Name.Name
		}
	}
	return ""
}

// Contains reports whether target names a runtime path.
func (g JSGlobals) Contains(target string) bool {
	return g.paths.Contains(target)
}

// Len returns how many paths the set holds. A run that collected none has read
// no `.d.ts`, which is worth telling apart from a run where every target
// failed.
func (g JSGlobals) Len() int { return g.paths.Len() }

// explain says which half of a dotted target is unknown, so a reader does not
// have to guess which segment is wrong. A bare target has no halves and gets
// no detail.
func (g JSGlobals) explain(target string) string {
	prefix, member, dotted := strings.Cut(target, ".")
	if !dotted {
		return ""
	}
	if !g.paths.Contains(prefix) {
		return fmt.Sprintf(", and %q is not a known top-level global", prefix)
	}
	return fmt.Sprintf(", and %q has no known runtime member %q", prefix, member)
}

// JSTargetFinding is one `@js` argument that names no runtime path.
type JSTargetFinding struct {
	// Package is the URI of the package the declaration is written to.
	Package string
	// Decl is the declared name the decorator sits on.
	Decl string
	// Target is the decorator's argument.
	Target string
	// Detail says which half of a dotted target is unknown, and is empty
	// for a bare one.
	Detail string
}

func (f JSTargetFinding) String() string {
	return fmt.Sprintf("%s: `@js(%q)` on %q names no JS runtime global%s",
		f.Package, f.Target, f.Decl, f.Detail)
}

// ValidateJSTargets reports every `@js` argument in mods that names no runtime
// path in globals.
//
// A generated decorator is derived from the declaration it converts, so it
// passes by construction. What this catches is a hand-written one: an overlay
// file naming a global that the pinned `.d.ts` set does not declare, whether
// because it was mistyped or because the TypeScript pin moved under it.
//
// Findings come back sorted by package, then target, then declared name, so a
// failing run reads the same way twice over a map walk that does not.
func ValidateJSTargets(mods map[string]*StandaloneModule, globals JSGlobals) []JSTargetFinding {
	var findings []JSTargetFinding
	for uri, mod := range mods {
		for _, decl := range moduleDecls(mod.Module) {
			_, target, ok := ast.FindJsDecorator(decl)
			if !ok || globals.Contains(target) {
				continue
			}
			findings = append(findings, JSTargetFinding{
				Package: uri,
				Decl:    declLabel(decl),
				Target:  target,
				Detail:  globals.explain(target),
			})
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Package != findings[j].Package {
			return findings[i].Package < findings[j].Package
		}
		if findings[i].Target != findings[j].Target {
			return findings[i].Target < findings[j].Target
		}
		return findings[i].Decl < findings[j].Decl
	})
	return findings
}

// moduleDecls returns every top-level declaration of mod, across all its
// namespaces.
func moduleDecls(mod *ast.Module) []ast.Decl {
	var decls []ast.Decl
	mod.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		decls = append(decls, ns.Decls...)
		return true
	})
	return decls
}

// declLabel names decl for a finding, falling back to "<unnamed>" for a
// declaration with no single identifier such as a destructuring `VarDecl`.
func declLabel(decl ast.Decl) string {
	switch d := decl.(type) {
	case *ast.VarDecl:
		if id, ok := d.Pattern.(*ast.IdentPat); ok {
			return id.Name
		}
	case *ast.FuncDecl:
		if d.Name != nil {
			return d.Name.Name
		}
	case *ast.ClassDecl:
		if d.Name != nil {
			return d.Name.Name
		}
	}
	return "<unnamed>"
}
