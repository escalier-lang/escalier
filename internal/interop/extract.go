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
// `filePath` is the locator for the override file being extracted. It
// is set on every Origin produced by this call (always with
// Kind=OverrideFile) and surfaced in diagnostics. See Origin.FilePath
// for the locator-string convention.
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
			declOrigin := origin
			declOrigin.Span = decl.Span()
			ms := ensureModule(out, "", declOrigin)
			extractIntoContainer(decl.Decls, globalNs, &ms.Container, filePath, tier)
		case *ast.DeclareModuleDecl:
			if !decl.Override() || decl.Name == nil {
				continue
			}
			modName := decl.Name.Value
			declOrigin := origin
			declOrigin.Span = decl.Span()
			ms := ensureModule(out, modName, declOrigin)
			extractIntoContainer(decl.Decls, namedNs[modName], &ms.Container, filePath, tier)
		}
	}
	return out
}

// ensureModule returns the ModuleScope for `name`, creating it on first
// touch. When the module already exists, `origin` is ignored — the
// first-seen file's origin wins for Container.Origin. This is fine for
// diagnostics because each leaf carries its own Origins; the
// Container.Origin is only used as a fallback when no leaf-level origin
// is available.
func ensureModule(out map[string]*ModuleScope, name string, origin Origin) *ModuleScope {
	if ms, ok := out[name]; ok {
		return ms
	}
	ms := &ModuleScope{
		Container: Container{
			Free:     make(map[string]*Effective),
			Children: make(map[string]ChildScope),
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
			for name := range ast.FindBindings(decl.Pattern) {
				eff := buildLeafFromValue(ns, name, filePath, decl.Span(), tier)
				if eff != nil {
					container.Free[name] = eff
				}
			}
		case *ast.TypeDecl:
			if decl.Name == nil {
				continue
			}
			// TODO(#interop-type-value-namespace): types and values live
			// in separate namespaces, so a module may legitimately declare
			// both `type Foo = …` and `val Foo = …` (or `class Foo { … }`)
			// with the same identifier. Container.Free keys by string,
			// which collapses the two into one slot — whichever loses the
			// race is silently dropped. Tracked in §5.13.
			eff := buildLeafFromTypeAlias(ns, decl.Name.Name, filePath, decl.Span(), tier)
			if eff != nil {
				container.Free[decl.Name.Name] = eff
			}
		case *ast.ClassDecl:
			if decl.Name == nil {
				continue
			}
			container.Children[decl.Name.Name] = buildClassChild(decl, ns, filePath, tier)
		case *ast.InterfaceDecl:
			if decl.Name == nil {
				continue
			}
			container.Children[decl.Name.Name] = buildInterfaceChild(decl, ns, filePath, tier)
		case *ast.NamespaceDecl:
			if decl.Name == nil {
				continue
			}
			subNs, _ := nsLookupNamespace(ns, decl.Name.Name)
			child := &NamespaceScope{
				Container: Container{
					Free:     make(map[string]*Effective),
					Children: make(map[string]ChildScope),
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
		Type:    b.Type,
		Source:  tier.ResolutionTierFor(),
		Origins: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: span}},
		Tier:    tier,
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
		Type:    ta.Type,
		Source:  tier.ResolutionTierFor(),
		Origins: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: span}},
		Tier:    tier,
	}
}

// buildClassChild builds a ChildScope for a class declaration. Methods,
// getters, setters, property fields and the constructor are routed
// into the appropriate MemberSet slot; their types come from the class
// type recorded in `ns`.
//
// The class type lookup is best-effort: if the checker couldn't
// produce a typed binding for the class itself (e.g. a member body
// references an unresolvable type, an annotation is malformed, or the
// enclosing module's inference failed wholesale), the leaf types fall
// back to nil and the merge will skip the consistency check for that
// slot. References to upstream base classes in `extends` clauses are
// not a concern — those resolve through the checker's outer scope,
// which already contains the .d.ts symbols (§5.2 sequencing).
func buildClassChild(decl *ast.ClassDecl, ns *type_system.Namespace, filePath string, tier OverrideTier) *ClassScope {
	origin := Origin{Kind: OverrideFile, FilePath: filePath, Span: decl.Span()}
	child := &ClassScope{
		Origin:   origin,
		Instance: NewMemberSet(),
		Static:   NewMemberSet(),
	}

	instanceType := lookupInstanceObject(ns, decl.Name.Name)
	staticType := lookupStaticObject(ns, decl.Name.Name)

	// Pick the ObjectType to read element types from based on the
	// elem's static modifier. Static lookup uses the Constructor-side
	// ObjectType (TS trio `FooConstructor` or an Escalier static
	// ObjectType under Values[name]).
	objFor := func(static bool) *type_system.ObjectType {
		if static {
			return staticType
		}
		return instanceType
	}
	setFor := func(static bool) *MemberSet {
		if static {
			return child.Static
		}
		return child.Instance
	}

	for _, elem := range decl.Body {
		switch e := elem.(type) {
		case *ast.MethodElem:
			name := CanonicalNameFromObjKey(e.Name)
			if name == "" {
				continue
			}
			setFor(e.Static).Methods[name] = &Effective{
				Type:    lookupObjElemType(objFor(e.Static), name),
				Source:  tier.ResolutionTierFor(),
				Origins: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: e.Span()}},
				Tier:    tier,
			}
		case *ast.GetterElem:
			name := CanonicalNameFromObjKey(e.Name)
			if name == "" {
				continue
			}
			setFor(e.Static).Getters[name] = &Effective{
				Type:    lookupObjElemType(objFor(e.Static), name),
				Source:  tier.ResolutionTierFor(),
				Origins: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: e.Span()}},
				Tier:    tier,
			}
		case *ast.SetterElem:
			name := CanonicalNameFromObjKey(e.Name)
			if name == "" {
				continue
			}
			setFor(e.Static).Setters[name] = &Effective{
				Type:    lookupObjElemType(objFor(e.Static), name),
				Source:  tier.ResolutionTierFor(),
				Origins: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: e.Span()}},
				Tier:    tier,
			}
		case *ast.FieldElem:
			name := CanonicalNameFromObjKey(e.Name)
			if name == "" {
				continue
			}
			setFor(e.Static).Properties[name] = &Effective{
				Type:    lookupObjElemType(objFor(e.Static), name),
				Source:  tier.ResolutionTierFor(),
				Origins: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: e.Span()}},
				Tier:    tier,
			}
		case *ast.ConstructorElem:
			eff := &Effective{
				Type:    lookupCtorType(staticType),
				Source:  tier.ResolutionTierFor(),
				Origins: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: e.Span()}},
				Tier:    tier,
			}
			child.Instance.Ctor = eff
		}
	}
	return child
}

// buildInterfaceChild mirrors buildClassChild for interfaces. Same
// slot routing; types come from the interface's object type in `ns`.
// Interfaces have no static side — only Instance is populated.
func buildInterfaceChild(decl *ast.InterfaceDecl, ns *type_system.Namespace, filePath string, tier OverrideTier) *InterfaceScope {
	origin := Origin{Kind: OverrideFile, FilePath: filePath, Span: decl.Span()}
	child := &InterfaceScope{
		Origin:   origin,
		Instance: NewMemberSet(),
	}
	if decl.TypeAnn == nil {
		return child
	}
	interfaceType := lookupInstanceObject(ns, decl.Name.Name)
	for _, elem := range decl.TypeAnn.Elems {
		switch e := elem.(type) {
		case *ast.MethodTypeAnn:
			name := CanonicalNameFromObjKey(e.Name)
			if name == "" {
				continue
			}
			child.Instance.Methods[name] = &Effective{
				Type:    lookupObjElemType(interfaceType, name),
				Source:  tier.ResolutionTierFor(),
				Origins: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: e.Span()}},
				Tier:    tier,
			}
		case *ast.GetterTypeAnn:
			name := CanonicalNameFromObjKey(e.Name)
			if name == "" {
				continue
			}
			child.Instance.Getters[name] = &Effective{
				Type:    lookupObjElemType(interfaceType, name),
				Source:  tier.ResolutionTierFor(),
				Origins: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: e.Span()}},
				Tier:    tier,
			}
		case *ast.SetterTypeAnn:
			name := CanonicalNameFromObjKey(e.Name)
			if name == "" {
				continue
			}
			child.Instance.Setters[name] = &Effective{
				Type:    lookupObjElemType(interfaceType, name),
				Source:  tier.ResolutionTierFor(),
				Origins: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: e.Span()}},
				Tier:    tier,
			}
		case *ast.PropertyTypeAnn:
			name := CanonicalNameFromObjKey(e.Name)
			if name == "" {
				continue
			}
			child.Instance.Properties[name] = &Effective{
				Type:    lookupObjElemType(interfaceType, name),
				Source:  tier.ResolutionTierFor(),
				Origins: []Origin{{Kind: OverrideFile, FilePath: filePath, Span: e.Span()}},
				Tier:    tier,
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

// lookupInstanceObject returns the *type_system.ObjectType for the
// instance side of the class/interface named `name` in `ns`, or nil
// if not found. Instance shapes typically land in `ns.Types[name].Type`
// (TypeAlias pointing at the instance ObjectType); as a fallback we
// also peel a constructor function under `ns.Values[name].Type` whose
// Return is the instance type. Static-side lookup lives in
// lookupStaticObject.
func lookupInstanceObject(ns *type_system.Namespace, name string) *type_system.ObjectType {
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

// lookupObjElemType finds a method, getter, setter, or property type on
// an ObjectType by canonical name. Returns nil if no matching element
// is present.
//
// Callers pass the instance-side or static-side ObjectType explicitly;
// instance shapes come from lookupInstanceObject (via Types[name]) and
// static shapes come from lookupStaticObject (via Values[name]).
func lookupObjElemType(obj *type_system.ObjectType, name string) type_system.Type {
	if obj == nil {
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

// lookupStaticObject returns the *ObjectType carrying the class's
// static side. The static ObjectType lives in `ns.Values[name].Type`
// in two shapes:
//
//   - Escalier `class Foo { … static bar() }`: Values["Foo"].Type is
//     the static ObjectType directly (also carries ConstructorElem).
//   - TS trio (`interface Foo` + `interface FooConstructor` +
//     `declare var Foo: FooConstructor`): Values["Foo"].Type is
//     TypeRef("FooConstructor") whose alias resolves to the same
//     shape.
//
// unwrapToObject handles both — peeling TypeRefType layers until it
// hits an ObjectType. Returns nil if the binding is absent or doesn't
// resolve to an object.
func lookupStaticObject(ns *type_system.Namespace, name string) *type_system.ObjectType {
	if ns == nil {
		return nil
	}
	b, ok := ns.Values[name]
	if !ok || b == nil {
		return nil
	}
	return unwrapToObject(b.Type)
}

// objElemMatch extracts (type, name, ok) from an object-type element.
// Names come from the ObjTypeKey carried on each element kind. Returns
// ok=false when the element has no addressable name (e.g.
// callable/constructor/index signature).
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
