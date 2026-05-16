package interop

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/require"
)

// TestConvertModuleWithOverrides_FlipsReceiverMut proves that
// ConvertModuleWithOverrides threads the override store all the way
// down to Classify for class methods, getters, and setters: a method
// that defaults to immutable by the name heuristic (`findItem`) gets
// flipped to `mut self` when the store carries a tier-4 entry with a
// MutType-wrapped receiver. This is the end-to-end wiring check for
// PR #609 — without it, the only proof that overrides are consulted
// lives in mutability_test.go, which calls Classify directly.
func TestConvertModuleWithOverrides_FlipsReceiverMut(t *testing.T) {
	input := `
declare class Foo {
	findItem(): number;
}
`
	dtsModule := parseModule(t, input)

	// Sanity check: without an override, findItem is immutable.
	noOverride, err := ConvertModule(dtsModule)
	require.NoError(t, err)
	require.False(t, methodReceiverMut(t, noOverride, "", "Foo", "findItem"),
		"baseline: findItem should default to immutable receiver")

	// Build a store with a tier-4 override that gives Foo.findItem a
	// `mut self` receiver. Module key "" matches the modulePath we pass
	// to ConvertModuleWithOverrides.
	receiver := type_system.NewNumPrimType(nil)
	mutReceiver := type_system.NewMutType(nil, receiver)
	fn := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	fn.SelfParam = &type_system.FuncParam{Type: mutReceiver}

	store := &OverrideStore{
		Modules: map[string]*ModuleScope{
			"": {
				Container: Container{
					Free: map[string]*Effective{},
					Children: map[string]ChildScope{
						"Foo": &ClassScope{
							Instance: &MemberSet{
								Methods: map[string]*Effective{
									"findItem": {Type: fn, Source: TierBuiltinOverride},
								},
								Getters:    map[string]*Effective{},
								Setters:    map[string]*Effective{},
								Properties: map[string]*Effective{},
							},
							Static: NewMemberSet(),
						},
					},
				},
			},
		},
	}

	withOverride, err := ConvertModuleWithOverrides(dtsModule, store, "")
	require.NoError(t, err)
	require.True(t, methodReceiverMut(t, withOverride, "", "Foo", "findItem"),
		"tier-4 override should flip findItem's receiver to mut self")
}

// TestConvertModuleWithOverrides_FlipsReceiverMut_NestedNamespace mirrors
// the root-namespace wiring test but for a class nested inside a
// `namespace` block. The override store keys the class by the qualified
// path "Outer.Inner.findItem"; pathForMember must build a Member-chain
// Owner (not a bare Ident) so OverrideStore.Resolve can walkChild
// through the NamespaceScope down to the ClassScope.
func TestConvertModuleWithOverrides_FlipsReceiverMut_NestedNamespace(t *testing.T) {
	input := `
declare namespace Outer {
	class Inner {
		findItem(): number;
	}
}
`
	dtsModule := parseModule(t, input)

	// Baseline: no override → name heuristic → non-mut.
	noOverride, err := ConvertModule(dtsModule)
	require.NoError(t, err)
	require.False(t, methodReceiverMut(t, noOverride, "Outer", "Inner", "findItem"),
		"baseline: findItem should default to immutable receiver")

	receiver := type_system.NewNumPrimType(nil)
	mutReceiver := type_system.NewMutType(nil, receiver)
	fn := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	fn.SelfParam = &type_system.FuncParam{Type: mutReceiver}

	store := &OverrideStore{
		Modules: map[string]*ModuleScope{
			"": {
				Container: Container{
					Free: map[string]*Effective{},
					Children: map[string]ChildScope{
						"Outer": &NamespaceScope{
							Container: Container{
								Free: map[string]*Effective{},
								Children: map[string]ChildScope{
									"Inner": &ClassScope{
										Instance: &MemberSet{
											Methods: map[string]*Effective{
												"findItem": {Type: fn, Source: TierBuiltinOverride},
											},
											Getters:    map[string]*Effective{},
											Setters:    map[string]*Effective{},
											Properties: map[string]*Effective{},
										},
										Static: NewMemberSet(),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	withOverride, err := ConvertModuleWithOverrides(dtsModule, store, "")
	require.NoError(t, err)
	require.True(t, methodReceiverMut(t, withOverride, "Outer", "Inner", "findItem"),
		"tier-4 override on Outer.Inner.findItem should flip the receiver to mut self")
}

// methodReceiverMut walks the namespace keyed by nsName ("" for the
// root namespace; "Outer.Inner" for nested namespaces, which
// processNamespace flattens into qualified keys), finds the named
// method on the named class, and returns its receiver's Mut flag.
func methodReceiverMut(t *testing.T, m *ast.Module, nsName, className, methodName string) bool {
	t.Helper()
	ns, ok := m.Namespaces.Get(nsName)
	require.True(t, ok, "namespace %q missing", nsName)
	for _, decl := range ns.Decls {
		cd, ok := decl.(*ast.ClassDecl)
		if !ok || cd.Name.Name != className {
			continue
		}
		for _, elem := range cd.Body {
			me, ok := elem.(*ast.MethodElem)
			if !ok {
				continue
			}
			keyIdent, ok := me.Name.(*ast.IdentExpr)
			if !ok || keyIdent.Name != methodName {
				continue
			}
			require.NotNil(t, me.Receiver, "method %s has no receiver", methodName)
			return me.Receiver.Mut
		}
		t.Fatalf("class %s has no method %s", className, methodName)
	}
	t.Fatalf("class %s not found in namespace %q", className, nsName)
	return false
}
