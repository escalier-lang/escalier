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
	fnBuiltin := mkFn()
	userProject := map[string]*ModuleScope{
		"lodash": {
			Container: Container{
				Free: map[string]*Effective{
					"map": {Type: fnUser},
				},
				Children: map[string]ChildScope{},
			},
		},
	}
	builtin := map[string]*ModuleScope{
		"lodash": {
			Container: Container{
				Free: map[string]*Effective{
					"map":    {Type: fnBuiltin},
					"filter": {Type: fnBuiltin},
				},
				Children: map[string]ChildScope{},
			},
		},
	}
	out := Collapse(userProject, nil, builtin)
	mod := out["lodash"]
	require.NotNil(t, mod, "expected lodash in collapsed output")
	require.NotNil(t, mod.Free["map"])
	require.Same(t, fnUser, mod.Free["map"].Type)
	require.Equal(t, TierUserOverride, mod.Free["map"].Source)
	require.NotNil(t, mod.Free["filter"])
	require.Same(t, fnBuiltin, mod.Free["filter"].Type)
	require.Equal(t, TierBuiltinOverride, mod.Free["filter"].Source)
}

// TestCollapseChildShapeMismatchHigherTierWins exercises cross-tier
// shape conflict: the higher-precedence tier declares a child as one
// variant (e.g. namespace) and a lower tier declares the same name as
// a different variant (e.g. class). With the sum-type ChildScope, the
// shapes can't merge — higher tier wins wholesale, the lower tier's
// variant is silently dropped.
func TestCollapseChildShapeMismatchHigherTierWins(t *testing.T) {
	fnMethod := mkFn()
	// Higher tier: child "C" as a namespace.
	userProject := map[string]*ModuleScope{
		"m": {
			Container: Container{
				Free: map[string]*Effective{},
				Children: map[string]ChildScope{
					"C": &NamespaceScope{
						Container: Container{
							Free:     map[string]*Effective{},
							Children: map[string]ChildScope{},
						},
					},
				},
			},
		},
	}
	// Lower tier: child "C" as a class with an instance method.
	builtinInstance := NewMemberSet()
	builtinInstance.Methods["m"] = &Effective{Type: fnMethod}
	builtin := map[string]*ModuleScope{
		"m": {
			Container: Container{
				Free: map[string]*Effective{},
				Children: map[string]ChildScope{
					"C": &ClassScope{
						Instance: builtinInstance,
						Static:   NewMemberSet(),
					},
				},
			},
		},
	}
	out := Collapse(userProject, nil, builtin)
	mod := out["m"]
	require.NotNil(t, mod)
	// Higher tier wins: the merged child is a NamespaceScope. The
	// builtin's class methods are dropped — shapes do not merge.
	_, ok := mod.Children["C"].(*NamespaceScope)
	require.True(t, ok, "expected merged child to remain a *NamespaceScope (higher tier wins)")
}

// TestCollapseChildSameShapeAcrossTiers exercises the normal case:
// both tiers declare the child with the same variant. Per-slot
// precedence applies — higher tier wins per name, lower tier fills in
// any names the higher tier didn't supply.
func TestCollapseChildSameShapeAcrossTiers(t *testing.T) {
	fnHigh := mkFn()
	fnLow := mkFn()
	// Higher tier: class "C" with method "a".
	userProject := map[string]*ModuleScope{
		"m": {
			Container: Container{
				Free: map[string]*Effective{},
				Children: map[string]ChildScope{
					"C": &ClassScope{
						Instance: &MemberSet{
							Methods:    map[string]*Effective{"a": {Type: fnHigh}},
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
	// Lower tier: class "C" with method "b" (different name, fills in).
	builtin := map[string]*ModuleScope{
		"m": {
			Container: Container{
				Free: map[string]*Effective{},
				Children: map[string]ChildScope{
					"C": &ClassScope{
						Instance: &MemberSet{
							Methods:    map[string]*Effective{"b": {Type: fnLow}},
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
	out := Collapse(userProject, nil, builtin)
	cls, ok := out["m"].Children["C"].(*ClassScope)
	require.True(t, ok)
	require.Same(t, fnHigh, cls.Instance.Methods["a"].Type)
	require.Equal(t, TierUserOverride, cls.Instance.Methods["a"].Source)
	require.Same(t, fnLow, cls.Instance.Methods["b"].Type)
	require.Equal(t, TierBuiltinOverride, cls.Instance.Methods["b"].Source)
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
				Children: map[string]ChildScope{},
			},
		},
	}
	override := map[string]*ModuleScope{
		"": {
			Container: Container{
				Free: map[string]*Effective{
					"identity": {Type: overFn, Source: TierUserOverride},
				},
				Children: map[string]ChildScope{},
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
				Children: map[string]ChildScope{},
			},
		},
	}
	store, errs := Merge(original, nil)
	require.Empty(t, errs)
	got := store.Modules[""].Free["identity"]
	require.NotNil(t, got)
	require.Same(t, origFn, got.Type)
	// TierUserSource is the zero value of ResolutionTier, used here as
	// the "Source left unset" sentinel for original-only leaves —
	// Classify's lower tiers then decide the final tier.
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
				Children: map[string]ChildScope{
					"C": &ClassScope{
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
				Children: map[string]ChildScope{},
			},
			AllPure:     true,
			AllPureTier: OverrideTierUserProject,
		},
	}

	store, errs := Merge(original, override)
	require.Empty(t, errs)
	mergedC, ok := store.Modules[""].Children["C"].(*ClassScope)
	require.True(t, ok)
	got := mergedC.Instance.Methods["m"]
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
				Children: map[string]ChildScope{},
			},
		},
	}
	override := map[string]*ModuleScope{
		"": {
			Container: Container{
				Free:     map[string]*Effective{"bar": {Type: overFn, Source: TierUserOverride}},
				Children: map[string]ChildScope{},
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

// TestMergeUnknownMemberWhenParentOriginalAbsent: every Escalier
// library ships .d.ts for TS back-compat (requirements.md §F), so an
// override that targets a module with no original side is a caller
// bug (originals weren't pre-loaded) or a typo. Either way the
// override leaf is still emitted into the store, but ErrUnknownMember
// is reported so the caller notices.
func TestMergeUnknownMemberWhenParentOriginalAbsent(t *testing.T) {
	overFn := mkFn()
	override := map[string]*ModuleScope{
		"lodash": {
			Container: Container{
				Free:     map[string]*Effective{"map": {Type: overFn, Source: TierUserOverride}},
				Children: map[string]ChildScope{},
			},
		},
	}
	store, errs := Merge(nil, override)
	require.Len(t, errs, 1)
	_, ok := errs[0].(*ErrUnknownMember)
	require.True(t, ok, "expected *ErrUnknownMember; got %T", errs[0])
	require.NotNil(t, store.Modules["lodash"].Free["map"])
}

// TestMergeCtorOverrideReplacesOriginal: the constructor slot is single
// per class; an override Ctor replaces the original Ctor.
func TestMergeCtorOverrideReplacesOriginal(t *testing.T) {
	origCtor := mkFn()
	overCtor := mkFn()
	mkClass := func(ctor *type_system.FuncType) *ClassScope {
		return &ClassScope{
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
			Children: map[string]ChildScope{"C": mkClass(origCtor)},
		}},
	}
	override := map[string]*ModuleScope{
		"": {Container: Container{
			Free:     map[string]*Effective{},
			Children: map[string]ChildScope{"C": mkClass(overCtor)},
		}},
	}
	store, errs := Merge(original, override)
	require.Empty(t, errs)
	mergedC, ok := store.Modules[""].Children["C"].(*ClassScope)
	require.True(t, ok)
	got := mergedC.Instance.Ctor
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
			Children: map[string]ChildScope{},
		}},
	}
	override := map[string]*ModuleScope{
		"": {Container: Container{
			Free:     map[string]*Effective{"f": {Type: overFn, Source: TierUserOverride}},
			Children: map[string]ChildScope{},
		}},
	}
	store, errs := Merge(original, override)
	require.Len(t, errs, 1)
	_, ok := errs[0].(*ErrSignatureMismatch)
	require.True(t, ok, "expected *ErrSignatureMismatch; got %T", errs[0])
	require.Same(t, overFn, store.Modules[""].Free["f"].Type)
}

// TestMergeExplicitMutSelfOverrideWins: an override declares a method
// with `self` (bare receiver) against an original that declared the
// same method with `mut self` (MutType-wrapped receiver). Merge picks
// the override; the consistency check intentionally excludes SelfParam
// mode from equivalence (see consistency.go funcSignatureEquivalent —
// "that's the field the override is allowed to change"), so no error
// is raised. The bare receiver lands in the merged store.
//
// This is the canonical receiver-mutability refinement that the whole
// interop-mutability subsystem exists to support. If consistency
// equivalence ever starts checking SelfParam, this test fails — which
// is the invariant we want to lock in.
func TestMergeExplicitMutSelfOverrideWins(t *testing.T) {
	receiver := type_system.NewNumPrimType(nil)
	mutReceiver := type_system.NewMutType(nil, receiver)

	// Original-side method on class C: takes `mut self`.
	origMethod := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	origMethod.SelfParam = &type_system.FuncParam{Type: mutReceiver}

	// Override-side method on class C: takes plain `self` (bare
	// receiver, not wrapped in MutType). Same return type and arity.
	overMethod := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	overMethod.SelfParam = &type_system.FuncParam{Type: receiver}

	mkClassWithMethod := func(fn *type_system.FuncType, source ResolutionTier) *ClassScope {
		return &ClassScope{
			Instance: &MemberSet{
				Methods:    map[string]*Effective{"m": {Type: fn, Source: source}},
				Getters:    map[string]*Effective{},
				Setters:    map[string]*Effective{},
				Properties: map[string]*Effective{},
			},
			Static: NewMemberSet(),
		}
	}

	original := map[string]*ModuleScope{
		"": {Container: Container{
			Free:     map[string]*Effective{},
			Children: map[string]ChildScope{"C": mkClassWithMethod(origMethod, 0)},
		}},
	}
	override := map[string]*ModuleScope{
		"": {Container: Container{
			Free:     map[string]*Effective{},
			Children: map[string]ChildScope{"C": mkClassWithMethod(overMethod, TierUserOverride)},
		}},
	}

	store, errs := Merge(original, override)
	require.Empty(t, errs,
		"receiver-mutability override must not trip the consistency check")

	mergedC, ok := store.Modules[""].Children["C"].(*ClassScope)
	require.True(t, ok)
	got := mergedC.Instance.Methods["m"]
	require.NotNil(t, got)
	fn, ok := got.Type.(*type_system.FuncType)
	require.True(t, ok)
	require.NotNil(t, fn.SelfParam)
	_, stillMut := fn.SelfParam.Type.(*type_system.MutType)
	require.False(t, stillMut, "merged method must keep the override's bare receiver")
	require.Same(t, receiver, fn.SelfParam.Type)
}

// TestMergeShapeConflictBetweenOriginalAndOverride: when the original
// has a child as one variant (namespace) and the override has the same
// child as a different variant (class), the merge proceeds with the
// override's variant but reports *ErrShapeConflict so the caller can
// distinguish a shape clash from a duplicate-member collision.
func TestMergeShapeConflictBetweenOriginalAndOverride(t *testing.T) {
	original := map[string]*ModuleScope{
		"": {Container: Container{
			Free: map[string]*Effective{},
			Children: map[string]ChildScope{
				"C": &NamespaceScope{Container: Container{
					Free:     map[string]*Effective{},
					Children: map[string]ChildScope{},
				}},
			},
		}},
	}
	override := map[string]*ModuleScope{
		"": {Container: Container{
			Free: map[string]*Effective{},
			Children: map[string]ChildScope{
				"C": &ClassScope{Instance: NewMemberSet(), Static: NewMemberSet()},
			},
		}},
	}
	_, errs := Merge(original, override)
	require.Len(t, errs, 1)
	_, ok := errs[0].(*ErrShapeConflict)
	require.True(t, ok, "expected *ErrShapeConflict; got %T", errs[0])
}

// TestMergeNamespaceNestedOverrideMatchesOriginal: an override targets
// a function inside a nested namespace (e.g. `lodash.fp.map`). The
// merger must descend through Container.Children to the matching
// namespace child on the original side, replace the leaf, and not emit
// ErrUnknownMember.
func TestMergeNamespaceNestedOverrideMatchesOriginal(t *testing.T) {
	origFn := mkFn()
	overFn := mkFn()

	// Build a NamespaceScope with `map` as a free function in its Container.
	mkNamespaceWithMap := func(fn *type_system.FuncType, source ResolutionTier) *NamespaceScope {
		return &NamespaceScope{
			Container: Container{
				Free:     map[string]*Effective{"map": {Type: fn, Source: source}},
				Children: map[string]ChildScope{},
			},
		}
	}

	original := map[string]*ModuleScope{
		"lodash": {Container: Container{
			Free:     map[string]*Effective{},
			Children: map[string]ChildScope{"fp": mkNamespaceWithMap(origFn, 0)},
		}},
	}
	override := map[string]*ModuleScope{
		"lodash": {Container: Container{
			Free:     map[string]*Effective{},
			Children: map[string]ChildScope{"fp": mkNamespaceWithMap(overFn, TierUserOverride)},
		}},
	}

	store, errs := Merge(original, override)
	require.Empty(t, errs, "nested-namespace override should match the original at the same path")

	mergedFp, ok := store.Modules["lodash"].Children["fp"].(*NamespaceScope)
	require.True(t, ok, "merged namespace must remain shape-namespace, not class")
	got := mergedFp.Container.Free["map"]
	require.NotNil(t, got)
	require.Same(t, overFn, got.Type, "override should replace original at the nested path")
}

// mkPropClass returns a single-class ModuleScope with the given Effective
// installed as an instance property named `p`. Used by the property-type
// consistency tests below.
func mkPropClass(prop *Effective) *ClassScope {
	inst := NewMemberSet()
	inst.Properties["p"] = prop
	return &ClassScope{Instance: inst, Static: NewMemberSet()}
}

// TestMergePropertyTypeEqualNoError: identical property types on both
// sides merge cleanly (the dominant case — an override merely confirms
// what the .d.ts already says).
func TestMergePropertyTypeEqualNoError(t *testing.T) {
	num := type_system.NewNumPrimType(nil)
	original := map[string]*ModuleScope{
		"": {Container: Container{
			Free:     map[string]*Effective{},
			Children: map[string]ChildScope{"C": mkPropClass(&Effective{Type: num})},
		}},
	}
	override := map[string]*ModuleScope{
		"": {Container: Container{
			Free:     map[string]*Effective{},
			Children: map[string]ChildScope{"C": mkPropClass(&Effective{Type: type_system.NewNumPrimType(nil), Source: TierUserOverride})},
		}},
	}
	_, errs := Merge(original, override)
	require.Empty(t, errs)
}

// TestMergePropertyMutWrappingPermitted: an override that wraps the
// original type in Mut[…] is the canonical Group-B use case (a TS-side
// mutable container being given its real Mut[…] type). Permitted by
// propertyTypeEquivalent.
func TestMergePropertyMutWrappingPermitted(t *testing.T) {
	num := type_system.NewNumPrimType(nil)
	mutNum := type_system.NewMutType(nil, type_system.NewNumPrimType(nil))
	original := map[string]*ModuleScope{
		"": {Container: Container{
			Free:     map[string]*Effective{},
			Children: map[string]ChildScope{"C": mkPropClass(&Effective{Type: num})},
		}},
	}
	override := map[string]*ModuleScope{
		"": {Container: Container{
			Free:     map[string]*Effective{},
			Children: map[string]ChildScope{"C": mkPropClass(&Effective{Type: mutNum, Source: TierUserOverride})},
		}},
	}
	store, errs := Merge(original, override)
	require.Empty(t, errs, "override may add a Mut wrapper over the original property type")
	cls, ok := store.Modules[""].Children["C"].(*ClassScope)
	require.True(t, ok)
	require.Same(t, mutNum, cls.Instance.Properties["p"].Type)
}

// TestMergePropertyTighteningAnyPermitted: a TS-side `any` original is
// the precision-tightening case — overrides legitimately refine an
// `any` slot to a concrete shape. Permitted by propertyTypeEquivalent.
func TestMergePropertyTighteningAnyPermitted(t *testing.T) {
	anyT := type_system.NewAnyType(nil)
	num := type_system.NewNumPrimType(nil)
	original := map[string]*ModuleScope{
		"": {Container: Container{
			Free:     map[string]*Effective{},
			Children: map[string]ChildScope{"C": mkPropClass(&Effective{Type: anyT})},
		}},
	}
	override := map[string]*ModuleScope{
		"": {Container: Container{
			Free:     map[string]*Effective{},
			Children: map[string]ChildScope{"C": mkPropClass(&Effective{Type: num, Source: TierUserOverride})},
		}},
	}
	_, errs := Merge(original, override)
	require.Empty(t, errs, "override may tighten a TS-side `any` property to a concrete type")
}

// TestMergePropertyTypeMismatchSurfacesError: unrelated property shapes
// (brand narrowing string→UserId would land here too) trip the
// property-consistency rule. The override still wins in the store.
func TestMergePropertyTypeMismatchSurfacesError(t *testing.T) {
	num := type_system.NewNumPrimType(nil)
	str := type_system.NewStrPrimType(nil)
	original := map[string]*ModuleScope{
		"": {Container: Container{
			Free:     map[string]*Effective{"x": {Type: num}},
			Children: map[string]ChildScope{},
		}},
	}
	override := map[string]*ModuleScope{
		"": {Container: Container{
			Free:     map[string]*Effective{"x": {Type: str, Source: TierUserOverride}},
			Children: map[string]ChildScope{},
		}},
	}
	store, errs := Merge(original, override)
	require.Len(t, errs, 1)
	_, ok := errs[0].(*ErrPropertyTypeMismatch)
	require.True(t, ok, "expected *ErrPropertyTypeMismatch; got %T", errs[0])
	require.Same(t, str, store.Modules[""].Free["x"].Type)
}

// TestMergePropertyKindMismatchSurfacesError: function on one side and
// non-function on the other is a kind mismatch — the override is
// declaring something fundamentally different from the original.
// Reported as ErrPropertyTypeMismatch (not ErrSignatureMismatch — there
// is no signature shape to compare on the non-function side).
func TestMergePropertyKindMismatchSurfacesError(t *testing.T) {
	num := type_system.NewNumPrimType(nil)
	fn := mkFn()
	original := map[string]*ModuleScope{
		"": {Container: Container{
			Free:     map[string]*Effective{"x": {Type: fn}},
			Children: map[string]ChildScope{},
		}},
	}
	override := map[string]*ModuleScope{
		"": {Container: Container{
			Free:     map[string]*Effective{"x": {Type: num, Source: TierUserOverride}},
			Children: map[string]ChildScope{},
		}},
	}
	_, errs := Merge(original, override)
	require.Len(t, errs, 1)
	_, ok := errs[0].(*ErrPropertyTypeMismatch)
	require.True(t, ok, "function-vs-property kind mismatch should be *ErrPropertyTypeMismatch; got %T", errs[0])
}
