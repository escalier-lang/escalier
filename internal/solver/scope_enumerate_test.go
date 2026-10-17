package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestScopeEnumeration covers the exported reads a caller outside the package walks a
// scope chain with. A namespace whose members a scope binds under their own names
// counts as that scope's own, and a parent's bindings never do.
func TestScopeEnumeration(t *testing.T) {
	num := ValueBinding{Schemes: []TypeScheme{monoScheme(&soltype.PrimType{Prim: soltype.NumPrim})}}
	str := TypeBinding{Type: &soltype.PrimType{Prim: soltype.StrPrim}}

	root := NewScope()
	root.defineValue("outer", num)

	scope := root.Child()
	scope.defineValue("b", num)
	scope.defineType("T", str)
	scope.defineNamespace("ns", &Namespace{Name: "ns"})
	scope.defineUnprefixed(&Namespace{
		Values: map[string]ValueBinding{"a": num},
		Types:  map[string]TypeBinding{"U": str},
	})

	require.Same(t, root, scope.Parent())
	require.Nil(t, root.Parent())
	require.False(t, scope.IsBuiltin())
	require.True(t, NewPrelude().IsBuiltin())

	require.Equal(t, []string{"a", "b"}, scope.ValueNames())
	require.Equal(t, []string{"T", "U"}, scope.TypeNames())
	require.Equal(t, []string{"ns"}, scope.NamespaceNames())

	_, ok := scope.LocalValue("a")
	require.True(t, ok)
	_, ok = scope.LocalType("U")
	require.True(t, ok)
	_, ok = scope.LocalValue("outer")
	require.False(t, ok)
}

// TestScriptScopeChainBuiltins covers where a script run's chain turns builtin: at the
// script scope's parent, the ambient scope, and at every scope above it.
func TestScriptScopeChainBuiltins(t *testing.T) {
	scope, _, errs := InferScript(parseScript(t, "val x = 1"), testStdlibSource())
	require.Empty(t, errs)

	require.False(t, scope.IsBuiltin())
	for cur := scope.Parent(); cur != nil; cur = cur.Parent() {
		require.True(t, cur.IsBuiltin())
	}
}
