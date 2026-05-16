package interop

import (
	"strings"

	"github.com/escalier-lang/escalier/internal/type_system"
)

// BuildOriginal walks a namespace and produces an "original-side"
// ModuleScope suitable for handing to Merge as a value in the
// `originals` map.
//
// Class shapes are recovered via name-based heuristics so that an
// override author writing `override declare class Foo { … }` lines up
// with one ClassScope on the original side regardless of how the
// original was declared:
//
//   - TS class-via-trio: `interface Foo { … }`,
//     `interface FooConstructor { new (…); /* statics */ }`,
//     `declare var Foo: FooConstructor`. These land in the namespace
//     as `Types["Foo"]`, `Types["FooConstructor"]`,
//     `Values["Foo"]: TypeRef("FooConstructor")`. Fused into one
//     ClassScope; `Types["FooConstructor"]` is consumed.
//   - Escalier-style class: `class Foo { … }` parks the instance
//     ObjectType under `Types["Foo"]` and a static-side ObjectType
//     (carrying ConstructorElem + statics) under `Values["Foo"]`.
//     Fused into one ClassScope with no extra sibling consumed.
//
// Anything that doesn't match a class heuristic falls back to literal
// mapping: type aliases and values go to `Container.Free` and
// sub-namespaces become NamespaceScope `Container.Children` (recursed).
//
// Limitation: Types and Values share the same `Container.Free` slot
// (5.13 Group C item — the type/value namespace split). When both a
// type alias and a value bind the same name, the value wins. Class
// shapes consume both sides so this only matters for non-class
// name-collisions.
//
// Origins carry Kind=OriginalDTS with no FilePath/Span — namespace
// entries don't retain their source location. Per-leaf provenance is
// recovered on the override side, which is what diagnostics surface.
//
// Trio fusion must run on the post-inference, post-merge namespace —
// after every `interface Foo { … }` sibling in the same module has
// been folded into a single `Types["Foo"]`. Running this incrementally
// per file would miss later augmentations.
func BuildOriginal(ns *type_system.Namespace) *ModuleScope {
	ms := &ModuleScope{
		Container: Container{
			Free:     make(map[string]*Effective),
			Children: make(map[string]ChildScope),
			Origin:   Origin{Kind: OriginalDTS},
		},
	}
	if ns == nil {
		return ms
	}
	fillContainer(&ms.Container, ns)
	return ms
}

// fillContainer populates `c` from the namespace, applying trio
// fusion before falling back to the literal mapping.
func fillContainer(c *Container, ns *type_system.Namespace) {
	// First pass: detect trios. `consumedTypes` records type-side
	// names already absorbed into a fused ClassScope so the
	// fall-through loop doesn't re-emit them as Free entries.
	consumedTypes := make(map[string]bool)
	consumedValues := make(map[string]bool)

	for name, ta := range ns.Types {
		if consumedTypes[name] {
			continue
		}
		if cs := tryFuseTrio(ns, name, ta, consumedTypes, consumedValues); cs != nil {
			c.Children[name] = cs
		}
	}

	// Escalier-style classes: Values[name] is an ObjectType carrying
	// a ConstructorElem. (Skip names already consumed by trio fusion
	// or by an earlier value-only Escalier-class pass.)
	for name, b := range ns.Values {
		if consumedValues[name] {
			continue
		}
		if b == nil || b.Type == nil {
			continue
		}
		if cs := tryFuseEscalierClass(ns, name, b.Type, consumedTypes, consumedValues); cs != nil {
			c.Children[name] = cs
		}
	}

	// Nested namespaces: recurse.
	for name, sub := range ns.Namespaces {
		if _, classed := c.Children[name]; classed {
			continue
		}
		nsChild := &NamespaceScope{
			Container: Container{
				Free:     make(map[string]*Effective),
				Children: make(map[string]ChildScope),
				Origin:   Origin{Kind: OriginalDTS},
			},
		}
		fillContainer(&nsChild.Container, sub)
		c.Children[name] = nsChild
	}

	// Fall-through: emit literal Free entries for everything not
	// consumed by class fusion. Values win on name collision (see
	// the type/value namespace caveat in BuildOriginal's doc).
	for name, b := range ns.Values {
		if consumedValues[name] {
			continue
		}
		if _, classed := c.Children[name]; classed {
			continue
		}
		if b == nil || b.Type == nil {
			continue
		}
		c.Free[name] = &Effective{
			Type:    b.Type,
			Origins: []Origin{{Kind: OriginalDTS}},
		}
	}
	for name, ta := range ns.Types {
		if consumedTypes[name] {
			continue
		}
		if _, present := c.Free[name]; present {
			continue
		}
		if _, classed := c.Children[name]; classed {
			continue
		}
		if ta == nil {
			continue
		}
		c.Free[name] = &Effective{
			Type:    ta.Type,
			Origins: []Origin{{Kind: OriginalDTS}},
		}
	}
}

// tryFuseTrio recognises the TS class-via-trio pattern at `name` and
// returns a ClassScope when matched; nil otherwise. On a match the
// type-side sibling (`<name>Constructor`) is recorded in
// consumedTypes and the value-side binding is recorded in
// consumedValues so the caller's fall-through doesn't re-emit them.
func tryFuseTrio(
	ns *type_system.Namespace,
	name string,
	instTA *type_system.TypeAlias,
	consumedTypes map[string]bool,
	consumedValues map[string]bool,
) *ClassScope {
	if instTA == nil {
		return nil
	}
	ctorName := name + "Constructor"
	ctorTA, ok := ns.Types[ctorName]
	if !ok || ctorTA == nil {
		return nil
	}
	b, ok := ns.Values[name]
	if !ok || b == nil {
		return nil
	}
	// Value side must be a TypeRef pointing at the Constructor alias.
	ref, ok := b.Type.(*type_system.TypeRefType)
	if !ok {
		return nil
	}
	if refName := qualIdentName(ref.Name); refName != ctorName {
		return nil
	}
	// Name match alone isn't enough — the TypeRef must resolve to the
	// same alias we'd consume from Types[ctorName]. Otherwise the
	// trio is spurious and we'd silently grab the wrong static side.
	if ref.TypeAlias != ctorTA {
		return nil
	}

	instObj := unwrapToObject(instTA.Type)
	staticObj := unwrapToObject(ctorTA.Type)
	if instObj == nil && staticObj == nil {
		return nil
	}

	consumedTypes[ctorName] = true
	consumedValues[name] = true
	return classScopeFromObjects(instObj, staticObj)
}

// tryFuseEscalierClass recognises the Escalier-style class shape:
// Values[name] is an ObjectType carrying a ConstructorElem. The
// instance side, when present, is read from Types[name].
//
// Skips names whose value side already participated in a trio fusion
// (in which case consumedValues[name] is set).
func tryFuseEscalierClass(
	ns *type_system.Namespace,
	name string,
	valType type_system.Type,
	consumedTypes map[string]bool,
	consumedValues map[string]bool,
) *ClassScope {
	staticObj, ok := valType.(*type_system.ObjectType)
	if !ok {
		return nil
	}
	hasCtor := false
	for _, e := range staticObj.Elems {
		if _, isCtor := e.(*type_system.ConstructorElem); isCtor {
			hasCtor = true
			break
		}
	}
	if !hasCtor {
		return nil
	}
	var instObj *type_system.ObjectType
	if ta, present := ns.Types[name]; present && ta != nil {
		instObj = unwrapToObject(ta.Type)
		if instObj != nil {
			consumedTypes[name] = true
		}
	}
	consumedValues[name] = true
	return classScopeFromObjects(instObj, staticObj)
}

// classScopeFromObjects builds a ClassScope by reading instance
// members from `inst` and static members from `static`. The Ctor slot
// is filled from the first ConstructorElem on `static`. Either side
// may be nil; the corresponding MemberSet is left empty.
func classScopeFromObjects(inst, static *type_system.ObjectType) *ClassScope {
	cs := &ClassScope{
		Origin:   Origin{Kind: OriginalDTS},
		Instance: NewMemberSet(),
		Static:   NewMemberSet(),
	}
	fillMemberSet(cs.Instance, inst)
	fillMemberSet(cs.Static, static)
	if static != nil {
		if t := lookupCtorType(static); t != nil {
			cs.Instance.Ctor = &Effective{
				Type:    t,
				Origins: []Origin{{Kind: OriginalDTS}},
			}
		}
	}
	return cs
}

// fillMemberSet copies named members of `obj` into `set`.
// ConstructorElem is handled by classScopeFromObjects.
func fillMemberSet(set *MemberSet, obj *type_system.ObjectType) {
	if obj == nil {
		return
	}
	for _, elem := range obj.Elems {
		switch e := elem.(type) {
		case *type_system.MethodElem:
			set.Methods[e.Name.String()] = leafEffective(e.Fn)
		case *type_system.GetterElem:
			set.Getters[e.Name.String()] = leafEffective(e.Fn)
		case *type_system.SetterElem:
			set.Setters[e.Name.String()] = leafEffective(e.Fn)
		case *type_system.PropertyElem:
			set.Properties[e.Name.String()] = leafEffective(e.Value)
		}
	}
}

func leafEffective(t type_system.Type) *Effective {
	return &Effective{
		Type:    t,
		Origins: []Origin{{Kind: OriginalDTS}},
	}
}

// qualIdentName flattens a QualIdent into its rightmost segment for
// trio detection. The TS trio uses simple identifiers so we don't
// need full path matching; if a future encoding introduces dotted
// constructor refs, the trailing segment is still what should equal
// the sibling Types key.
func qualIdentName(qi type_system.QualIdent) string {
	switch q := qi.(type) {
	case *type_system.Ident:
		return q.Name
	}
	s := type_system.QualIdentToString(qi)
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}
