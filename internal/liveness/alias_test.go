package liveness

import (
	"testing"
)

func TestNewAliasTracker(t *testing.T) {
	tracker := NewAliasTracker()
	if tracker == nil {
		t.Fatal("NewAliasTracker returned nil")
	}
	if len(tracker.Sets) != 0 {
		t.Errorf("expected 0 sets, got %d", len(tracker.Sets))
	}
	if len(tracker.VarToSets) != 0 {
		t.Errorf("expected 0 var-to-sets, got %d", len(tracker.VarToSets))
	}
}

func TestNewValue(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1

	tracker.NewValue(x, AliasImmutable)

	sets := tracker.GetAliasSets(x)
	if len(sets) != 1 {
		t.Fatalf("expected 1 alias set, got %d", len(sets))
	}
	if sets[0].Origin != x {
		t.Errorf("expected origin %d, got %d", x, sets[0].Origin)
	}
	if sets[0].Members[x] != AliasImmutable {
		t.Errorf("expected immutable, got %d", sets[0].Members[x])
	}
}

func TestNewValueMutable(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1

	tracker.NewValue(x, AliasMutable)

	sets := tracker.GetAliasSets(x)
	if len(sets) != 1 {
		t.Fatalf("expected 1 alias set, got %d", len(sets))
	}
	if sets[0].Members[x] != AliasMutable {
		t.Errorf("expected mutable, got %d", sets[0].Members[x])
	}
}

func TestAddAlias(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	var y VarID = 2

	tracker.NewValue(x, AliasImmutable)
	tracker.AddAlias(y, x, AliasImmutable)

	// Both x and y should be in the same alias set
	xSets := tracker.GetAliasSets(x)
	ySets := tracker.GetAliasSets(y)
	if len(xSets) != 1 || len(ySets) != 1 {
		t.Fatalf("expected 1 set each, got x=%d y=%d", len(xSets), len(ySets))
	}
	if xSets[0].ID != ySets[0].ID {
		t.Errorf("expected same set ID, got x=%d y=%d", xSets[0].ID, ySets[0].ID)
	}
	if len(xSets[0].Members) != 2 {
		t.Errorf("expected 2 members, got %d", len(xSets[0].Members))
	}
}

func TestAddAliasMutableToImmutable(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	var y VarID = 2

	tracker.NewValue(x, AliasImmutable)
	tracker.AddAlias(y, x, AliasMutable)

	sets := tracker.GetAliasSets(y)
	if sets[0].Members[x] != AliasImmutable {
		t.Errorf("x should remain immutable")
	}
	if sets[0].Members[y] != AliasMutable {
		t.Errorf("y should be mutable")
	}
}

func TestReassignToNewSource(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	var y VarID = 2
	var z VarID = 3

	tracker.NewValue(x, AliasImmutable)
	tracker.AddAlias(y, x, AliasImmutable)
	tracker.NewValue(z, AliasMutable)

	// Reassign y from x's set to z's set
	tracker.Reassign(y, &z, AliasMutable)

	// y should no longer be in x's alias set
	xSets := tracker.GetAliasSets(x)
	if len(xSets) != 1 {
		t.Fatalf("expected 1 set for x, got %d", len(xSets))
	}
	if _, ok := xSets[0].Members[y]; ok {
		t.Error("y should not be in x's alias set after reassignment")
	}

	// y should be in z's alias set
	ySets := tracker.GetAliasSets(y)
	if len(ySets) != 1 {
		t.Fatalf("expected 1 set for y, got %d", len(ySets))
	}
	if ySets[0].Members[y] != AliasMutable {
		t.Errorf("expected mutable, got %d", ySets[0].Members[y])
	}
	if _, ok := ySets[0].Members[z]; !ok {
		t.Error("z should be in y's alias set")
	}
}

func TestReassignToFreshValue(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	var y VarID = 2

	tracker.NewValue(x, AliasImmutable)
	tracker.AddAlias(y, x, AliasImmutable)

	// Reassign y to a fresh value (nil source)
	tracker.Reassign(y, nil, AliasMutable)

	// y should have its own fresh alias set
	ySets := tracker.GetAliasSets(y)
	if len(ySets) != 1 {
		t.Fatalf("expected 1 set for y, got %d", len(ySets))
	}
	if ySets[0].Origin != y {
		t.Errorf("expected origin y, got %d", ySets[0].Origin)
	}

	// x's alias set should not contain y
	xSets := tracker.GetAliasSets(x)
	if _, ok := xSets[0].Members[y]; ok {
		t.Error("y should not be in x's alias set")
	}
}

func TestMergeAliasSets(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	var y VarID = 2

	tracker.NewValue(x, AliasImmutable)
	tracker.NewValue(y, AliasMutable)

	tracker.MergeAliasSets(x, y)

	// After merge, x and y should share an alias set
	xSets := tracker.GetAliasSets(x)
	ySets := tracker.GetAliasSets(y)
	if len(xSets) != 1 || len(ySets) != 1 {
		t.Fatalf("expected 1 set each, got x=%d y=%d", len(xSets), len(ySets))
	}
	if xSets[0].ID != ySets[0].ID {
		t.Errorf("expected same set, got x=%d y=%d", xSets[0].ID, ySets[0].ID)
	}
	if len(xSets[0].Members) != 2 {
		t.Errorf("expected 2 members in merged set, got %d", len(xSets[0].Members))
	}
}

func TestGetAliasSetsEmpty(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1

	sets := tracker.GetAliasSets(x)
	if len(sets) != 0 {
		t.Errorf("expected 0 sets for untracked var, got %d", len(sets))
	}
}

func TestMergeAliasSetsNoDuplicateSetIDs(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	var y VarID = 2
	var z VarID = 3

	// x and y share a set, z has its own set
	tracker.NewValue(x, AliasImmutable)
	tracker.AddAlias(y, x, AliasImmutable)
	tracker.NewValue(z, AliasMutable)

	// Merge z's set into x/y's set
	tracker.MergeAliasSets(x, z)

	// y already belonged to targetID before the merge, so the replacement
	// of z's setID with targetID must not create a duplicate entry.
	for v, setIDs := range tracker.VarToSets {
		seen := make(map[SetID]bool)
		for _, id := range setIDs {
			if seen[id] {
				t.Errorf("VarToSets[%d] contains duplicate SetID %d", v, id)
			}
			seen[id] = true
		}
	}
}

func TestMergeAliasSetsNoOp(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	var y VarID = 2

	// y has no alias set - merge should be a no-op
	tracker.NewValue(x, AliasImmutable)
	tracker.MergeAliasSets(x, y)

	xSets := tracker.GetAliasSets(x)
	if len(xSets) != 1 {
		t.Fatalf("expected 1 set for x, got %d", len(xSets))
	}
	if len(xSets[0].Members) != 1 {
		t.Errorf("expected 1 member, got %d", len(xSets[0].Members))
	}
}
