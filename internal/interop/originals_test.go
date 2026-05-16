package interop

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/require"
)

func TestBuildOriginalNil(t *testing.T) {
	ms := BuildOriginal(nil)
	require.NotNil(t, ms)
	require.Empty(t, ms.Free)
	require.Empty(t, ms.Children)
}

func TestBuildOriginalLiteralFreeEntries(t *testing.T) {
	// Plain type alias and plain value with different names: each
	// lands in Free.
	aliasT := type_system.NewNumPrimType(nil)
	valT := type_system.NewFuncType(nil, nil, nil, type_system.NewBoolPrimType(nil), nil)

	ns := type_system.NewNamespace()
	ns.Types["MyNum"] = &type_system.TypeAlias{Type: aliasT}
	ns.Values["doIt"] = &type_system.Binding{Type: valT}

	ms := BuildOriginal(ns)
	require.NotNil(t, ms.Free["MyNum"])
	require.Same(t, aliasT, ms.Free["MyNum"].Type)
	require.NotNil(t, ms.Free["doIt"])
	require.Same(t, valT, ms.Free["doIt"].Type)
}

func TestBuildOriginalFusesTSTrio(t *testing.T) {
	// interface Foo { bar(): number }
	instFn := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	instObj := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
		type_system.NewMethodElem(type_system.NewStrKey("bar"), instFn),
	})
	// interface FooConstructor { new (): Foo; isFoo(): boolean }
	ctorReturnRef := type_system.NewTypeRefType(nil, "Foo", nil)
	ctorElem := &type_system.ConstructorElem{Fn: type_system.NewFuncType(nil, nil, nil, ctorReturnRef, nil)}
	staticFn := type_system.NewFuncType(nil, nil, nil, type_system.NewBoolPrimType(nil), nil)
	staticObj := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
		ctorElem,
		type_system.NewMethodElem(type_system.NewStrKey("isFoo"), staticFn),
	})
	ctorAlias := &type_system.TypeAlias{Type: staticObj}

	ns := type_system.NewNamespace()
	ns.Types["Foo"] = &type_system.TypeAlias{Type: instObj}
	ns.Types["FooConstructor"] = ctorAlias
	ns.Values["Foo"] = &type_system.Binding{
		Type: type_system.NewTypeRefType(nil, "FooConstructor", ctorAlias),
	}

	ms := BuildOriginal(ns)

	// Trio fused into one class child; FooConstructor not surfaced as Free.
	require.NotContains(t, ms.Children, "FooConstructor")
	require.NotContains(t, ms.Free, "FooConstructor")
	require.NotContains(t, ms.Free, "Foo")

	cs, ok := ms.Children["Foo"].(*ClassScope)
	require.True(t, ok, "expected Children[Foo] to be *ClassScope")
	require.NotNil(t, cs.Instance.Methods["bar"])
	require.Same(t, instFn, cs.Instance.Methods["bar"].Type)
	require.NotNil(t, cs.Static.Methods["isFoo"])
	require.Same(t, staticFn, cs.Static.Methods["isFoo"].Type)
	require.NotNil(t, cs.Instance.Ctor)
	require.Same(t, ctorElem.Fn, cs.Instance.Ctor.Type)
}

func TestBuildOriginalNoTrioWhenValueIsNotTypeRef(t *testing.T) {
	// Types["Foo"] + Types["FooConstructor"] but Values["Foo"] is
	// some unrelated type: not a trio. Foo falls back to Free
	// (instance) and FooConstructor to Free (constructor side).
	instObj := type_system.NewObjectType(nil, nil)
	staticObj := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
		&type_system.ConstructorElem{Fn: type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)},
	})

	ns := type_system.NewNamespace()
	ns.Types["Foo"] = &type_system.TypeAlias{Type: instObj}
	ns.Types["FooConstructor"] = &type_system.TypeAlias{Type: staticObj}
	ns.Values["Foo"] = &type_system.Binding{Type: type_system.NewNumPrimType(nil)}

	ms := BuildOriginal(ns)
	_, classed := ms.Children["Foo"]
	require.False(t, classed, "expected no fusion when Values[Foo] is not a TypeRef")
	// Value wins on Free collision, so Free[Foo] is the value's type.
	require.NotNil(t, ms.Free["Foo"])
	require.NotNil(t, ms.Free["FooConstructor"])
}

func TestBuildOriginalEscalierStyleClass(t *testing.T) {
	// Escalier `class Bar { static doStatic(); inst() }`:
	// Types["Bar"] = instance ObjectType; Values["Bar"] = static
	// ObjectType containing ConstructorElem + statics. No
	// "BarConstructor" type alias.
	instFn := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	instObj := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
		type_system.NewMethodElem(type_system.NewStrKey("inst"), instFn),
	})
	staticFn := type_system.NewFuncType(nil, nil, nil, type_system.NewBoolPrimType(nil), nil)
	ctorFn := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	staticObj := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
		&type_system.ConstructorElem{Fn: ctorFn},
		type_system.NewMethodElem(type_system.NewStrKey("doStatic"), staticFn),
	})

	ns := type_system.NewNamespace()
	ns.Types["Bar"] = &type_system.TypeAlias{Type: instObj}
	ns.Values["Bar"] = &type_system.Binding{Type: staticObj}

	ms := BuildOriginal(ns)
	cs, ok := ms.Children["Bar"].(*ClassScope)
	require.True(t, ok)
	require.Same(t, instFn, cs.Instance.Methods["inst"].Type)
	require.Same(t, staticFn, cs.Static.Methods["doStatic"].Type)
	require.NotNil(t, cs.Instance.Ctor)
	require.Same(t, ctorFn, cs.Instance.Ctor.Type)
	require.NotContains(t, ms.Free, "Bar")
}

func TestBuildOriginalNoTrioWhenTypeRefAliasDiffers(t *testing.T) {
	// Types["Foo"], Types["FooConstructor"], and Values["Foo"] all
	// exist; the value side is a TypeRef whose Name matches
	// "FooConstructor" but whose TypeAlias points to an unrelated
	// alias. Trio fusion must not match — otherwise the static side
	// silently picks up the wrong ObjectType.
	instObj := type_system.NewObjectType(nil, nil)
	realCtorObj := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
		&type_system.ConstructorElem{Fn: type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)},
	})
	realCtorAlias := &type_system.TypeAlias{Type: realCtorObj}
	otherAlias := &type_system.TypeAlias{Type: type_system.NewObjectType(nil, nil)}

	ns := type_system.NewNamespace()
	ns.Types["Foo"] = &type_system.TypeAlias{Type: instObj}
	ns.Types["FooConstructor"] = realCtorAlias
	ns.Values["Foo"] = &type_system.Binding{
		Type: type_system.NewTypeRefType(nil, "FooConstructor", otherAlias),
	}

	ms := BuildOriginal(ns)
	_, classed := ms.Children["Foo"].(*ClassScope)
	require.False(t, classed, "expected no fusion when TypeRef.TypeAlias differs from Types[FooConstructor]")
}

func TestBuildOriginalSkipsNilTypedValueBinding(t *testing.T) {
	// A Binding with Type=nil must not produce a Free entry with
	// Type=nil. Downstream merge has an "override wins" branch that
	// would clobber typed slots with nil-typed entries, so originals
	// must drop nil-typed bindings on the floor.
	ns := type_system.NewNamespace()
	ns.Values["broken"] = &type_system.Binding{Type: nil}

	ms := BuildOriginal(ns)
	require.NotContains(t, ms.Free, "broken", "expected nil-typed binding to be skipped")
}

func TestBuildOriginalNestedNamespace(t *testing.T) {
	innerFn := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	inner := type_system.NewNamespace()
	inner.Values["leaf"] = &type_system.Binding{Type: innerFn}

	ns := type_system.NewNamespace()
	ns.Namespaces["Inner"] = inner

	ms := BuildOriginal(ns)
	nsc, ok := ms.Children["Inner"].(*NamespaceScope)
	require.True(t, ok, "expected nested namespace to land as *NamespaceScope")
	require.Same(t, innerFn, nsc.Container.Free["leaf"].Type)
}

func TestBuildOriginalNamespaceVsClassCoexist(t *testing.T) {
	// A class trio and a sub-namespace at sibling names should both
	// land in Children without interfering.
	instObj := type_system.NewObjectType(nil, nil)
	staticObj := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
		&type_system.ConstructorElem{Fn: type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)},
	})
	ctorAlias := &type_system.TypeAlias{Type: staticObj}

	ns := type_system.NewNamespace()
	ns.Types["C"] = &type_system.TypeAlias{Type: instObj}
	ns.Types["CConstructor"] = ctorAlias
	ns.Values["C"] = &type_system.Binding{
		Type: type_system.NewTypeRefType(nil, "CConstructor", ctorAlias),
	}
	ns.Namespaces["NS"] = type_system.NewNamespace()

	ms := BuildOriginal(ns)
	_, isClass := ms.Children["C"].(*ClassScope)
	require.True(t, isClass)
	_, isNs := ms.Children["NS"].(*NamespaceScope)
	require.True(t, isNs)
}

func TestBuildOriginalNoTrioWhenValueBindingMissing(t *testing.T) {
	// Types["Foo"] and Types["FooConstructor"] exist but Values["Foo"]
	// is absent. Trio fusion needs all three slots; without the value
	// binding the pair must fall back to literal Free entries rather
	// than silently fusing.
	instObj := type_system.NewObjectType(nil, nil)
	staticObj := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
		&type_system.ConstructorElem{Fn: type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)},
	})

	ns := type_system.NewNamespace()
	ns.Types["Foo"] = &type_system.TypeAlias{Type: instObj}
	ns.Types["FooConstructor"] = &type_system.TypeAlias{Type: staticObj}

	ms := BuildOriginal(ns)
	_, classed := ms.Children["Foo"].(*ClassScope)
	require.False(t, classed, "expected no fusion when Values[Foo] is absent")
	require.NotNil(t, ms.Free["Foo"], "expected Foo to fall through to Free")
	require.NotNil(t, ms.Free["FooConstructor"], "expected FooConstructor to fall through to Free")
}

func TestBuildOriginalNoTrioWhenBothObjectsUnwrapToNil(t *testing.T) {
	// Trio name-shape is present but neither type alias resolves to an
	// ObjectType (both are primitive aliases). Fusing would produce an
	// empty ClassScope and silently consume the bindings; the code
	// must back out and leave them as Free.
	ctorAlias := &type_system.TypeAlias{Type: type_system.NewNumPrimType(nil)}

	ns := type_system.NewNamespace()
	ns.Types["Foo"] = &type_system.TypeAlias{Type: type_system.NewNumPrimType(nil)}
	ns.Types["FooConstructor"] = ctorAlias
	ns.Values["Foo"] = &type_system.Binding{
		Type: type_system.NewTypeRefType(nil, "FooConstructor", ctorAlias),
	}

	ms := BuildOriginal(ns)
	_, classed := ms.Children["Foo"].(*ClassScope)
	require.False(t, classed, "expected no fusion when neither side unwraps to an ObjectType")
	require.NotNil(t, ms.Free["Foo"], "expected Foo to fall through to Free (value side wins)")
	require.NotNil(t, ms.Free["FooConstructor"], "expected FooConstructor to fall through to Free")
}

func TestBuildOriginalEscalierClassWithNonObjectTypeAlias(t *testing.T) {
	// Escalier-style class fusion runs even when Types[name] exists but
	// aliases a non-object type (the alias can't be the instance shape).
	// Per BuildOriginal's documented behavior, class shapes consume both
	// the type and value side at the shared name — the unrelated type
	// alias is intentionally not surfaced as a Free entry. This pins
	// that invariant so future "preserve the type entry" changes can't
	// silently shadow a class binding.
	ctorFn := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	staticFn := type_system.NewFuncType(nil, nil, nil, type_system.NewBoolPrimType(nil), nil)
	staticObj := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
		&type_system.ConstructorElem{Fn: ctorFn},
		type_system.NewMethodElem(type_system.NewStrKey("doStatic"), staticFn),
	})

	ns := type_system.NewNamespace()
	ns.Types["Bar"] = &type_system.TypeAlias{Type: type_system.NewNumPrimType(nil)}
	ns.Values["Bar"] = &type_system.Binding{Type: staticObj}

	ms := BuildOriginal(ns)
	cs, ok := ms.Children["Bar"].(*ClassScope)
	require.True(t, ok, "expected ClassScope when Values side carries a ConstructorElem")
	require.Empty(t, cs.Instance.Methods, "expected empty Instance when Types side doesn't unwrap to ObjectType")
	require.NotNil(t, cs.Static.Methods["doStatic"])
	require.NotContains(t, ms.Free, "Bar", "class fusion at this name consumes the type side too")
}
