package interop

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// Extract walks a parsed override file's declarations alongside the
// checker-produced namespace and returns a per-module map of override
// ModuleScopes the file contributes to. The shape decisions (which
// MemberSet slot a member lands in, instance vs. static, namespace vs.
// class) come from the AST; the actual `type_system.Type` values come
// from the namespace.
//
// `globalNs` is the namespace the checker populated when inferring the
// override file's `override declare global { ... }` block contents
// (and any top-level sugar that desugars to it). It MAY be nil for
// files that only target named modules.
//
// `namedNs` maps module-specifier string → namespace, populated by the
// checker from each `override declare module "name" { ... }` block.
//
// Top-level sugar (`override declare class Foo { ... }` etc.) is
// expected to have been desugared by the parser per
// implementation_plan.md §2.2, so each top-level decl in `decls` is
// either a DeclareModuleDecl or a DeclareGlobalDecl whose Override()
// reports true. Anything else is ignored (returned as a parse-shape
// error in §5.5 step 4 — this extractor trusts its input).
func Extract(
	decls []ast.Decl,
	globalNs *type_system.Namespace,
	namedNs map[string]*type_system.Namespace,
	filePath string,
	tier OverrideTier,
) map[string]*ModuleScope {
	out := make(map[string]*ModuleScope)
	origin := Origin{Kind: OverrideFile, FilePath: filePath}

	for _, d := range decls {
		switch decl := d.(type) {
		case *ast.DeclareGlobalDecl:
			if !decl.Override() {
				continue
			}
			ms := ensureModule(out, "", origin)
			extractIntoContainer(decl.Decls, globalNs, &ms.Container, filePath, tier)
		case *ast.DeclareModuleDecl:
			if !decl.Override() || decl.Name == nil {
				continue
			}
			modName := decl.Name.Value
			ms := ensureModule(out, modName, origin)
			extractIntoContainer(decl.Decls, namedNs[modName], &ms.Container, filePath, tier)
		}
	}
	return out
}

// ensureModule returns the ModuleScope for `name`, creating it on first
// touch. When the module already exists, `origin` is ignored — the
// first-seen file's origin wins for Container.Origin. This is fine for
// diagnostics because each leaf carries its own Provenance; the
// Container.Origin is only used as a fallback when no leaf-level origin
// is available.
func ensureModule(out map[string]*ModuleScope, name string, origin Origin) *ModuleScope {
	if ms, ok := out[name]; ok {
		return ms
	}
	ms := &ModuleScope{
		Container: Container{
			Free:     make(map[string]*Effective),
			Children: make(map[string]*ChildScope),
			Origin:   origin,
		},
	}
	out[name] = ms
	return ms
}

// extractIntoContainer walks a list of declarations and places each
// into the appropriate Container/ChildScope slot, pulling typed values
// out of ns.
func extractIntoContainer(
	decls []ast.Decl,
	ns *type_system.Namespace,
	container *Container,
	filePath string,
	tier OverrideTier,
) {
	for _, d := range decls {
		switch decl := d.(type) {
		case *ast.FuncDecl:
			if decl.Name == nil {
				continue
			}
			eff := buildLeafFromValue(ns, decl.Name.Name, filePath, decl.Span(), tier)
			if eff != nil {
				container.Free[decl.Name.Name] = eff
			}
		case *ast.VarDecl:
			for _, name := range patternNames(decl.Pattern) {
				eff := buildLeafFromValue(ns, name, filePath, decl.Span(), tier)
				if eff != nil {
					container.Free[name] = eff
				}
			}
		case *ast.TypeDecl:
			if decl.Name == nil {
				continue
			}
			eff := buildLeafFromTypeAlias(ns, decl.Name.Name, filePath, decl.Span(), tier)
			if eff != nil {
				container.Free[decl.Name.Name] = eff
			}
		case *ast.ClassDecl:
			if decl.Name == nil {
				continue
			}
			child := buildClassChild(decl, ns, filePath, tier)
			if child != nil {
				container.Children[decl.Name.Name] = child
			}
		case *ast.InterfaceDecl:
			if decl.Name == nil {
				continue
			}
			child := buildInterfaceChild(decl, ns, filePath, tier)
			if child != nil {
				container.Children[decl.Name.Name] = child
			}
		case *ast.NamespaceDecl:
			if decl.Name == nil {
				continue
			}
			subNs, _ := nsLookupNamespace(ns, decl.Name.Name)
			child := &ChildScope{
				Container: Container{
					Free:     make(map[string]*Effective),
					Children: make(map[string]*ChildScope),
					Origin:   Origin{Kind: OverrideFile, FilePath: filePath, Span: decl.Span()},
				},
			}
			extractIntoContainer(decl.Decls, subNs, &child.Container, filePath, tier)
			container.Children[decl.Name.Name] = child
		}
	}
}

func buildLeafFromValue(ns *type_system.Namespace, name, filePath string, span ast.Span, tier OverrideTier) *Effective {
	if ns == nil {
		return nil
	}
	b, ok := ns.Values[name]
	if !ok || b == nil {
		return nil
	}
	return &Effective{
		Type:       b.Type,
		Source:     tier.ResolutionTierFor(),
		Provenance: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: span}},
		Tier:       tier,
	}
}

func buildLeafFromTypeAlias(ns *type_system.Namespace, name, filePath string, span ast.Span, tier OverrideTier) *Effective {
	if ns == nil {
		return nil
	}
	ta, ok := ns.Types[name]
	if !ok || ta == nil {
		return nil
	}
	return &Effective{
		Type:       ta.Type,
		Source:     tier.ResolutionTierFor(),
		Provenance: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: span}},
		Tier:       tier,
	}
}

// buildClassChild builds a ChildScope for a class declaration. Methods,
// getters, setters, property fields and the constructor are routed
// into the appropriate MemberSet slot; their types come from the class
// type recorded in `ns`.
//
// The class type lookup is best-effort: when the checker hasn't
// produced a typed object/class for the override (e.g. the override
// references a TypeScript symbol that wasn't pre-loaded), the leaf
// types fall back to nil and the merge will skip the consistency check
// for that slot.
func buildClassChild(decl *ast.ClassDecl, ns *type_system.Namespace, filePath string, tier OverrideTier) *ChildScope {
	origin := Origin{Kind: OverrideFile, FilePath: filePath, Span: decl.Span()}
	child := &ChildScope{
		Container: Container{
			Free:     make(map[string]*Effective),
			Children: make(map[string]*ChildScope),
			Origin:   origin,
		},
		Instance: NewMemberSet(),
		Static:   NewMemberSet(),
	}

	classType := lookupClassObject(ns, decl.Name.Name)

	// Static-side members are intentionally dropped until #interop-static
	// wires up static lookup. Recording them with Type=nil here would let
	// the merge step's "override wins" branch clobber the original's
	// typed static slot with a nil-typed entry, corrupting the store.
	// Dropping is silent for now — surfacing as a diagnostic is tracked
	// alongside the static-lookup work.
	for _, elem := range decl.Body {
		switch e := elem.(type) {
		case *ast.MethodElem:
			if e.Static {
				continue
			}
			name := CanonicalNameFromObjKey(e.Name)
			if name == "" {
				continue
			}
			child.Instance.Methods[name] = &Effective{
				Type:       lookupMethodType(classType, name, false),
				Source:     tier.ResolutionTierFor(),
				Provenance: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: e.Span()}},
				Tier:       tier,
			}
		case *ast.GetterElem:
			if e.Static {
				continue
			}
			name := CanonicalNameFromObjKey(e.Name)
			if name == "" {
				continue
			}
			child.Instance.Getters[name] = &Effective{
				Type:       lookupMethodType(classType, name, false),
				Source:     tier.ResolutionTierFor(),
				Provenance: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: e.Span()}},
				Tier:       tier,
			}
		case *ast.SetterElem:
			if e.Static {
				continue
			}
			name := CanonicalNameFromObjKey(e.Name)
			if name == "" {
				continue
			}
			child.Instance.Setters[name] = &Effective{
				Type:       lookupMethodType(classType, name, false),
				Source:     tier.ResolutionTierFor(),
				Provenance: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: e.Span()}},
				Tier:       tier,
			}
		case *ast.FieldElem:
			if e.Static {
				continue
			}
			name := CanonicalNameFromObjKey(e.Name)
			if name == "" {
				continue
			}
			child.Instance.Properties[name] = &Effective{
				Type:       lookupPropertyType(classType, name, false),
				Source:     tier.ResolutionTierFor(),
				Provenance: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: e.Span()}},
				Tier:       tier,
			}
		case *ast.ConstructorElem:
			eff := &Effective{
				Type:       lookupCtorType(classType),
				Source:     tier.ResolutionTierFor(),
				Provenance: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: e.Span()}},
				Tier:       tier,
			}
			child.Instance.Ctor = eff
		}
	}
	return child
}

// buildInterfaceChild mirrors buildClassChild for interfaces. Same
// slot routing; types come from the interface's object type in `ns`.
// Interfaces have no static side — only Instance is populated.
func buildInterfaceChild(decl *ast.InterfaceDecl, ns *type_system.Namespace, filePath string, tier OverrideTier) *ChildScope {
	origin := Origin{Kind: OverrideFile, FilePath: filePath, Span: decl.Span()}
	child := &ChildScope{
		Container: Container{
			Free:     make(map[string]*Effective),
			Children: make(map[string]*ChildScope),
			Origin:   origin,
		},
		Instance: NewMemberSet(),
	}
	if decl.TypeAnn == nil {
		return child
	}
	classType := lookupClassObject(ns, decl.Name.Name)
	for _, elem := range decl.TypeAnn.Elems {
		switch e := elem.(type) {
		case *ast.MethodTypeAnn:
			name := CanonicalNameFromObjKey(e.Name)
			if name == "" {
				continue
			}
			child.Instance.Methods[name] = &Effective{
				Type:       lookupMethodType(classType, name, false),
				Source:     tier.ResolutionTierFor(),
				Provenance: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: e.Span()}},
				Tier:       tier,
			}
		case *ast.GetterTypeAnn:
			name := CanonicalNameFromObjKey(e.Name)
			if name == "" {
				continue
			}
			child.Instance.Getters[name] = &Effective{
				Type:       lookupMethodType(classType, name, false),
				Source:     tier.ResolutionTierFor(),
				Provenance: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: e.Span()}},
				Tier:       tier,
			}
		case *ast.SetterTypeAnn:
			name := CanonicalNameFromObjKey(e.Name)
			if name == "" {
				continue
			}
			child.Instance.Setters[name] = &Effective{
				Type:       lookupMethodType(classType, name, false),
				Source:     tier.ResolutionTierFor(),
				Provenance: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: e.Span()}},
				Tier:       tier,
			}
		case *ast.PropertyTypeAnn:
			name := CanonicalNameFromObjKey(e.Name)
			if name == "" {
				continue
			}
			child.Instance.Properties[name] = &Effective{
				Type:       lookupPropertyType(classType, name, false),
				Source:     tier.ResolutionTierFor(),
				Provenance: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: e.Span()}},
				Tier:       tier,
			}
		}
	}
	return child
}

// nsLookupNamespace returns the sub-namespace bound to `name` in `ns`,
// or nil/false if not found.
func nsLookupNamespace(ns *type_system.Namespace, name string) (*type_system.Namespace, bool) {
	if ns == nil {
		return nil, false
	}
	sub, ok := ns.Namespaces[name]
	return sub, ok
}

// lookupClassObject returns the *type_system.ObjectType corresponding
// to the class/interface named `name` in `ns`, or nil if not found.
// Classes typically land in `ns.Types[name].Type` (the TypeAlias points
// at the instance shape) or in `ns.Values[name].Type` (the constructor
// function whose Return is the instance type). The shape used by
// downstream interop is implementation-specific; we try both.
func lookupClassObject(ns *type_system.Namespace, name string) *type_system.ObjectType {
	if ns == nil {
		return nil
	}
	if ta, ok := ns.Types[name]; ok && ta != nil {
		if obj := unwrapToObject(ta.Type); obj != nil {
			return obj
		}
	}
	if b, ok := ns.Values[name]; ok && b != nil {
		if fn, ok := b.Type.(*type_system.FuncType); ok && fn.Return != nil {
			if obj := unwrapToObject(fn.Return); obj != nil {
				return obj
			}
		}
	}
	return nil
}

func unwrapToObject(t type_system.Type) *type_system.ObjectType {
	for {
		switch x := t.(type) {
		case *type_system.ObjectType:
			return x
		case *type_system.TypeRefType:
			if x.TypeAlias == nil {
				return nil
			}
			t = x.TypeAlias.Type
		default:
			return nil
		}
	}
}

// lookupMethodType finds a method/getter/setter type on an ObjectType
// by canonical name. Returns nil if no matching element is present.
//
// TODO(#interop-static): static lookup is not yet wired — the class
// statics aren't reachable from the instance ObjectType, and the
// checker has no separate static-object representation here. Static
// callers receive nil and the consistency check is skipped on the
// static side until that gap is filled.
func lookupMethodType(obj *type_system.ObjectType, name string, static bool) type_system.Type {
	if obj == nil || static {
		return nil
	}
	for _, elem := range obj.Elems {
		t, n, ok := objElemMatch(elem)
		if !ok {
			continue
		}
		if n == name {
			return t
		}
	}
	return nil
}

func lookupPropertyType(obj *type_system.ObjectType, name string, static bool) type_system.Type {
	return lookupMethodType(obj, name, static)
}

func lookupCtorType(obj *type_system.ObjectType) type_system.Type {
	if obj == nil {
		return nil
	}
	for _, elem := range obj.Elems {
		if c, ok := elem.(*type_system.ConstructorElem); ok {
			return c.Fn
		}
	}
	return nil
}

// objElemMatch extracts (type, name, ok) from an object-type element.
// Names come from the ObjTypeKey carried on each element kind. Returns
// ok=false when the element has no addressable name (e.g.
// callable/constructor/index signature). All members on ObjectType are
// instance-side; static-side lookup is handled by lookupMethodType
// returning nil up front (see its TODO).
func objElemMatch(elem type_system.ObjTypeElem) (type_system.Type, string, bool) {
	switch e := elem.(type) {
	case *type_system.MethodElem:
		return e.Fn, e.Name.String(), true
	case *type_system.GetterElem:
		return e.Fn, e.Name.String(), true
	case *type_system.SetterElem:
		return e.Fn, e.Name.String(), true
	case *type_system.PropertyElem:
		return e.Value, e.Name.String(), true
	}
	return nil, "", false
}

// patternNames returns the surface identifier names a VarDecl pattern
// binds — the keys used to look up checker-produced bindings in `ns`.
// Recurses into destructuring patterns (object/tuple/rest) so nested
// IdentPat bindings aren't silently dropped.
//
// TODO: ExtractorPat and InstancePat aren't handled — they bind names
// from arbitrary class extractor signatures and aren't currently used
// at module scope in interop override files. LitPat and WildcardPat
// bind no names and are intentionally skipped.
func patternNames(p ast.Pat) []string {
	switch x := p.(type) {
	case *ast.IdentPat:
		return []string{x.Name}
	case *ast.ObjectPat:
		var names []string
		for _, elem := range x.Elems {
			switch e := elem.(type) {
			case *ast.ObjKeyValuePat:
				names = append(names, patternNames(e.Value)...)
			case *ast.ObjShorthandPat:
				if e.Key != nil {
					names = append(names, e.Key.Name)
				}
			case *ast.ObjRestPat:
				names = append(names, patternNames(e.Pattern)...)
			}
		}
		return names
	case *ast.TuplePat:
		var names []string
		for _, elem := range x.Elems {
			names = append(names, patternNames(elem)...)
		}
		return names
	case *ast.RestPat:
		return patternNames(x.Pattern)
	}
	return nil
}
