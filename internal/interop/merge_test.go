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

// TestMergeAllPureStripsMutSelf: @all_pure on the override module side
// rewrites instance methods that have only an original to drop their
// `mut self` receiver wrapping.
func TestMergeAllPureStripsMutSelf(t *testing.T) {
	receiver := type_system.NewNumPrimType(nil)
	mutReceiver := type_system.NewMutType(nil, receiver)
	origMethod := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	origMethod.SelfParam = &type_system.FuncParam{Type: mutReceiver}

	original := map[string]*ModuleScope{
		"": {
			Container: Container{
				Free: map[string]*Effective{},
				Children: map[string]*ChildScope{
					"C": {
						Container: Container{
							Free:     map[string]*Effective{},
							Children: map[string]*ChildScope{},
						},
						Instance: &MemberSet{
							Methods:    map[string]*Effective{"m": {Type: origMethod}},
							Getters:    map[string]*Effective{},
							Setters:    map[string]*Effective{},
							Properties: map[string]*Effective{},
						},
						Static: NewMemberSet(),
					},
				},
			},
		},
	}
	// Override module declares @all_pure but does not redeclare C.m.
	override := map[string]*ModuleScope{
		"": {
			Container: Container{
				Free:     map[string]*Effective{},
				Children: map[string]*ChildScope{},
			},
			AllPure:     true,
			AllPureTier: OverrideTierUserProject,
		},
	}

	store, errs := Merge(original, override)
	require.Empty(t, errs)
	got := store.Modules[""].Children["C"].Instance.Methods["m"]
	require.NotNil(t, got)
	fn, ok := got.Type.(*type_system.FuncType)
	require.True(t, ok, "expected merged method to remain a FuncType")
	require.NotNil(t, fn.SelfParam)
	_, stillMut := fn.SelfParam.Type.(*type_system.MutType)
	require.False(t, stillMut, "@all_pure must unwrap MutType from instance method receiver")
	require.Same(t, receiver, fn.SelfParam.Type)
	require.Equal(t, TierUserOverride, got.Source)
}

// TestMergeUnknownMemberWhenOverrideOnlyAndParentExists: an override
// adds a free name the original side does not declare, while the
// original parent (module) was pre-loaded — that is `ErrUnknownMember`.
func TestMergeUnknownMemberWhenOverrideOnlyAndParentExists(t *testing.T) {
	origFn := mkFn()
	overFn := mkFn()
	original := map[string]*ModuleScope{
		"": {
			Container: Container{
				Free:     map[string]*Effective{"foo": {Type: origFn}},
				Children: map[string]*ChildScope{},
			},
		},
	}
	override := map[string]*ModuleScope{
		"": {
			Container: Container{
				Free:     map[string]*Effective{"bar": {Type: overFn, Source: TierUserOverride}},
				Children: map[string]*ChildScope{},
			},
		},
	}
	store, errs := Merge(original, override)
	require.Len(t, errs, 1)
	_, ok := errs[0].(*ErrUnknownMember)
	require.True(t, ok, "expected *ErrUnknownMember; got %T", errs[0])

	// The override-only leaf is still emitted into the store so downstream
	// queries can find it; the error tells the user it has nothing to
	// compare against.
	require.NotNil(t, store.Modules[""].Free["bar"])
}

// TestMergeUnknownMemberSuppressedWhenParentOriginalAbsent: when the
// parent module wasn't pre-loaded on the original side, an override-only
// leaf is accepted silently (per §5.7).
func TestMergeUnknownMemberSuppressedWhenParentOriginalAbsent(t *testing.T) {
	overFn := mkFn()
	override := map[string]*ModuleScope{
		"lodash": {
			Container: Container{
				Free:     map[string]*Effective{"map": {Type: overFn, Source: TierUserOverride}},
				Children: map[string]*ChildScope{},
			},
		},
	}
	store, errs := Merge(nil, override)
	require.Empty(t, errs)
	require.NotNil(t, store.Modules["lodash"].Free["map"])
}

// TestMergeCtorOverrideReplacesOriginal: the constructor slot is single
// per class; an override Ctor replaces the original Ctor.
func TestMergeCtorOverrideReplacesOriginal(t *testing.T) {
	origCtor := mkFn()
	overCtor := mkFn()
	mkClass := func(ctor *type_system.FuncType) *ChildScope {
		return &ChildScope{
			Container: Container{
				Free:     map[string]*Effective{},
				Children: map[string]*ChildScope{},
			},
			Instance: &MemberSet{
				Methods:    map[string]*Effective{},
				Getters:    map[string]*Effective{},
				Setters:    map[string]*Effective{},
				Properties: map[string]*Effective{},
				Ctor:       &Effective{Type: ctor},
			},
			Static: NewMemberSet(),
		}
	}
	original := map[string]*ModuleScope{
		"": {Container: Container{
			Free:     map[string]*Effective{},
			Children: map[string]*ChildScope{"C": mkClass(origCtor)},
		}},
	}
	override := map[string]*ModuleScope{
		"": {Container: Container{
			Free:     map[string]*Effective{},
			Children: map[string]*ChildScope{"C": mkClass(overCtor)},
		}},
	}
	store, errs := Merge(original, override)
	require.Empty(t, errs)
	got := store.Modules[""].Children["C"].Instance.Ctor
	require.NotNil(t, got)
	require.Same(t, overCtor, got.Type)
}

// TestMergeFuncTypeMismatchSurfacesError: when both sides have a
// *FuncType at the same slot and signatures disagree, `Check` runs and
// returns *ErrSignatureMismatch, but the override still wins in the
// merged store.
func TestMergeFuncTypeMismatchSurfacesError(t *testing.T) {
	num := type_system.NewNumPrimType(nil)
	str := type_system.NewStrPrimType(nil)
	origFn := type_system.NewFuncType(
		nil, nil,
		[]*type_system.FuncParam{{Type: num}},
		num, nil,
	)
	overFn := type_system.NewFuncType(
		nil, nil,
		[]*type_system.FuncParam{{Type: num}, {Type: str}},
		num, nil,
	)
	original := map[string]*ModuleScope{
		"": {Container: Container{
			Free:     map[string]*Effective{"f": {Type: origFn}},
			Children: map[string]*ChildScope{},
		}},
	}
	override := map[string]*ModuleScope{
		"": {Container: Container{
			Free:     map[string]*Effective{"f": {Type: overFn, Source: TierUserOverride}},
			Children: map[string]*ChildScope{},
		}},
	}
	store, errs := Merge(original, override)
	require.Len(t, errs, 1)
	_, ok := errs[0].(*ErrSignatureMismatch)
	require.True(t, ok, "expected *ErrSignatureMismatch; got %T", errs[0])
	require.Same(t, overFn, store.Modules[""].Free["f"].Type)
}
