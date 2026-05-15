package interop

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/require"
)

func mkFn() *type_system.FuncType {
	return type_system.NewFuncType(nil, nil, nil, nil, nil)
}

func TestCollapseShadowing(t *testing.T) {
	fnUser := mkFn()
	fnShipped := mkFn()
	userProject := map[string]*ModuleScope{
		"lodash": {
			Container: Container{
				Free: map[string]*Effective{
					"map": {Type: fnUser},
				},
				Children: map[string]*ChildScope{},
			},
		},
	}
	shipped := map[string]*ModuleScope{
		"lodash": {
			Container: Container{
				Free: map[string]*Effective{
					"map":    {Type: fnShipped},
					"filter": {Type: fnShipped},
				},
				Children: map[string]*ChildScope{},
			},
		},
	}
	out := Collapse(
		[]map[string]*ModuleScope{userProject, shipped},
		[]OverrideTier{OverrideTierUserProject, OverrideTierShipped},
	)
	mod := out["lodash"]
	require.NotNil(t, mod, "expected lodash in collapsed output")
	require.NotNil(t, mod.Free["map"])
	require.Same(t, fnUser, mod.Free["map"].Type)
	require.Equal(t, TierUserOverride, mod.Free["map"].Source)
	require.NotNil(t, mod.Free["filter"])
	require.Same(t, fnShipped, mod.Free["filter"].Type)
	require.Equal(t, TierShippedOverride, mod.Free["filter"].Source)
}

// TestCollapseChildMemberSetAcrossTiers exercises the case where an
// earlier (higher-precedence) tier introduces a child as a namespace
// (Instance/Static nil) and a later tier introduces the same child as a
// class with members. The later tier's members should still merge into
// the collapsed child rather than being silently dropped.
func TestCollapseChildMemberSetAcrossTiers(t *testing.T) {
	fnMethod := mkFn()
	// Higher tier: child "C" as a namespace.
	userProject := map[string]*ModuleScope{
		"m": {
			Container: Container{
				Free: map[string]*Effective{},
				Children: map[string]*ChildScope{
					"C": {
						Container: Container{
							Free:     map[string]*Effective{},
							Children: map[string]*ChildScope{},
						},
					},
				},
			},
		},
	}
	// Lower tier: child "C" as a class with an instance method.
	shippedInstance := NewMemberSet()
	shippedInstance.Methods["m"] = &Effective{Type: fnMethod}
	shipped := map[string]*ModuleScope{
		"m": {
			Container: Container{
				Free: map[string]*Effective{},
				Children: map[string]*ChildScope{
					"C": {
						Container: Container{
							Free:     map[string]*Effective{},
							Children: map[string]*ChildScope{},
						},
						Instance: shippedInstance,
					},
				},
			},
		},
	}
	out := Collapse(
		[]map[string]*ModuleScope{userProject, shipped},
		[]OverrideTier{OverrideTierUserProject, OverrideTierShipped},
	)
	mod := out["m"]
	require.NotNil(t, mod)
	child := mod.Children["C"]
	require.NotNil(t, child)
	require.NotNil(t, child.Instance, "Instance should be allocated when a later tier introduces members")
	require.NotNil(t, child.Instance.Methods["m"])
	require.Same(t, fnMethod, child.Instance.Methods["m"].Type)
	require.Equal(t, TierShippedOverride, child.Instance.Methods["m"].Source)
}

func TestMergeOverrideReplacesOriginal(t *testing.T) {
	origFn := mkFn()
	overFn := mkFn()
	original := map[string]*ModuleScope{
		"": {
			Container: Container{
				Free: map[string]*Effective{
					"identity": {Type: origFn},
				},
				Children: map[string]*ChildScope{},
			},
		},
	}
	override := map[string]*ModuleScope{
		"": {
			Container: Container{
				Free: map[string]*Effective{
					"identity": {Type: overFn, Source: TierUserOverride},
				},
				Children: map[string]*ChildScope{},
			},
		},
	}
	store, errs := Merge(original, override)
	require.Empty(t, errs)
	got := store.Modules[""].Free["identity"]
	require.NotNil(t, got)
	require.Same(t, overFn, got.Type)
}

func TestMergePassesThroughOriginalWithoutOverride(t *testing.T) {
	origFn := mkFn()
	original := map[string]*ModuleScope{
		"": {
			Container: Container{
				Free: map[string]*Effective{
					"identity": {Type: origFn},
				},
				Children: map[string]*ChildScope{},
			},
		},
	}
	store, errs := Merge(original, nil)
	require.Empty(t, errs)
	got := store.Modules[""].Free["identity"]
	require.NotNil(t, got)
	require.Same(t, origFn, got.Type)
	// TierUserSource is the zero value of ResolutionTier, used here as
	// the "unstamped" sentinel for original-only leaves — Classify's
	// lower tiers then decide the final tier.
	require.Equal(t, TierUserSource, got.Source)
}
