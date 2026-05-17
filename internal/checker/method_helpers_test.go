package checker

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/require"
)

// makeMethodObjAlias builds a TypeAlias whose Type is an ObjectType
// containing a single instance MethodElem. The method's FuncType has no
// SelfParam yet — the shape produced by .d.ts loading, which
// populateSelfParams should backfill.
func makeMethodObjAlias(methodName string) *type_system.TypeAlias {
	fn := type_system.NewFuncType(
		nil, nil, nil,
		type_system.NewNeverType(nil),
		type_system.NewNeverType(nil),
	)
	method := type_system.NewMethodElem(type_system.NewStrKey(methodName), fn)
	obj := type_system.NewObjectType(nil, []type_system.ObjTypeElem{method})
	return &type_system.TypeAlias{Type: obj}
}

// TestPopulateSelfParamsRecursesIntoNestedNamespaces pins that the
// prelude SelfParam backfill walks child namespaces. .d.ts can define
// types under a nested namespace (e.g. `Intl.Collator`); without
// recursion those types' methods would silently miss SelfParam,
// regressing receiver-lifetime flow for any namespaced lib type.
func TestPopulateSelfParamsRecursesIntoNestedNamespaces(t *testing.T) {
	t.Parallel()

	root := type_system.NewNamespace()
	child := type_system.NewNamespace()
	grandchild := type_system.NewNamespace()

	root.Types["Top"] = makeMethodObjAlias("topMethod")
	child.Types["Mid"] = makeMethodObjAlias("midMethod")
	grandchild.Types["Leaf"] = makeMethodObjAlias("leafMethod")

	require.NoError(t, child.SetNamespace("Grand", grandchild))
	require.NoError(t, root.SetNamespace("Child", child))

	populateSelfParams(root)

	check := func(alias *type_system.TypeAlias, methodName string) {
		obj := type_system.Prune(alias.Type).(*type_system.ObjectType)
		var fn *type_system.FuncType
		for _, elem := range obj.Elems {
			if me, ok := elem.(*type_system.MethodElem); ok && me.Name.Str == methodName {
				fn = me.Fn
				break
			}
		}
		require.NotNilf(t, fn, "method %q not found", methodName)
		require.NotNilf(t, fn.SelfParam, "SelfParam missing for %q", methodName)
		_, isMut := fn.SelfParam.Type.(*type_system.MutType)
		require.Truef(t, isMut, "expected default mut self for %q", methodName)
	}

	check(root.Types["Top"], "topMethod")
	check(child.Types["Mid"], "midMethod")
	check(grandchild.Types["Leaf"], "leafMethod")
}

// TestPopulateSelfParamsGetterSetterDefaults pins the polarity for
// accessor elements: getters default to non-mut self (reading state
// doesn't mutate) and setters default to mut self (assignment
// mutates). Defaulting getters to mut would hide every .d.ts getter
// not in mutabilityOverrides on a non-mut receiver, because
// isMemberVisible gates GetterElem on receiver mutability the same
// way it gates MethodElem.
func TestPopulateSelfParamsGetterSetterDefaults(t *testing.T) {
	t.Parallel()

	getterFn := type_system.NewFuncType(nil, nil, nil,
		type_system.NewNeverType(nil), type_system.NewNeverType(nil))
	setterFn := type_system.NewFuncType(nil, nil, nil,
		type_system.NewNeverType(nil), type_system.NewNeverType(nil))
	getter := type_system.NewGetterElem(type_system.NewStrKey("g"), getterFn)
	setter := type_system.NewSetterElem(type_system.NewStrKey("s"), setterFn)
	obj := type_system.NewObjectType(nil, []type_system.ObjTypeElem{getter, setter})

	root := type_system.NewNamespace()
	root.Types["T"] = &type_system.TypeAlias{Type: obj}

	populateSelfParams(root)

	require.NotNil(t, getterFn.SelfParam)
	_, getterIsMut := getterFn.SelfParam.Type.(*type_system.MutType)
	require.Falsef(t, getterIsMut, "getter should default to non-mut self")

	require.NotNil(t, setterFn.SelfParam)
	_, setterIsMut := setterFn.SelfParam.Type.(*type_system.MutType)
	require.Truef(t, setterIsMut, "setter should default to mut self")
}

// TestStripMutSelfFromMethods pins the second-pass behaviour that
// strips `mut self` from methods classified as non-mutating in the
// per-interface overrides table. The post-#612 default for MethodElem
// is `mut self`; without this pass any non-mutating method on a
// non-constructor-shaped class (Date, Function, Console, Body,
// Request, Response) would stay hidden by isMemberVisible on non-mut
// receivers.
func TestStripMutSelfFromMethods(t *testing.T) {
	t.Parallel()

	build := func(names ...string) (*type_system.ObjectType, map[string]*type_system.FuncType) {
		fns := make(map[string]*type_system.FuncType, len(names))
		elems := make([]type_system.ObjTypeElem, 0, len(names))
		for _, n := range names {
			fn := type_system.NewFuncType(nil, nil, nil,
				type_system.NewNeverType(nil), type_system.NewNeverType(nil))
			fn.SelfParam = type_system.NewSelfParam(
				type_system.NewTypeRefType(nil, "T", nil),
				true,
			)
			fns[n] = fn
			elems = append(elems, type_system.NewMethodElem(type_system.NewStrKey(n), fn))
		}
		return type_system.NewObjectType(nil, elems), fns
	}

	tests := []struct {
		name      string
		methods   []string
		overrides []string
		// expectedMut[name] = whether the receiver should be mut after the pass
		expectedMut map[string]bool
	}{
		{
			name:      "listed method has mut stripped, unlisted retains mut",
			methods:   []string{"getHours", "setHours"},
			overrides: []string{"getHours"},
			expectedMut: map[string]bool{
				"getHours": false,
				"setHours": true,
			},
		},
		{
			name:      "method absent from the type is harmless",
			methods:   []string{"unclassified"},
			overrides: []string{"other"},
			expectedMut: map[string]bool{
				"unclassified": true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			obj, fns := build(tc.methods...)
			stripMutSelfFromMethods(obj, set.FromSlice(tc.overrides))
			for name, wantMut := range tc.expectedMut {
				fn := fns[name]
				_, isMut := fn.SelfParam.Type.(*type_system.MutType)
				require.Equalf(t, wantMut, isMut,
					"method %q: want mut=%v, got mut=%v", name, wantMut, isMut)
			}
		})
	}
}
