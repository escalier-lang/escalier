package checker

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
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
		assert.Falsef(t, isMut, "default non-mut for %q", methodName)
	}

	check(root.Types["Top"], "topMethod")
	check(child.Types["Mid"], "midMethod")
	check(grandchild.Types["Leaf"], "leafMethod")
}
