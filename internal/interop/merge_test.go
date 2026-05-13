package interop

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/type_system"
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
	if mod == nil {
		t.Fatal("expected lodash in collapsed output")
	}
	if got := mod.Free["map"]; got == nil || got.Type != fnUser {
		t.Fatalf("user-project entry should win; got %#v", got)
	}
	if got := mod.Free["map"]; got.Source != TierUserOverride {
		t.Fatalf("expected Source=TierUserOverride; got %v", got.Source)
	}
	if got := mod.Free["filter"]; got == nil || got.Type != fnShipped {
		t.Fatalf("shipped-only entry should survive; got %#v", got)
	}
	if got := mod.Free["filter"]; got.Source != TierShippedOverride {
		t.Fatalf("expected Source=TierShippedOverride; got %v", got.Source)
	}
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
	if len(errs) > 0 {
		t.Fatalf("unexpected merge errors: %v", errs)
	}
	got := store.Modules[""].Free["identity"]
	if got == nil || got.Type != overFn {
		t.Fatalf("override should replace original; got %#v", got)
	}
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
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	got := store.Modules[""].Free["identity"]
	if got == nil || got.Type != origFn {
		t.Fatalf("original should pass through; got %#v", got)
	}
	// TierUserSource is the zero value of ResolutionTier, used here as
	// the "unstamped" sentinel for original-only leaves — Classify's
	// lower tiers then decide the final tier.
	if got.Source != TierUserSource {
		t.Fatalf("expected unstamped Source (TierUserSource sentinel); got %v", got.Source)
	}
}
