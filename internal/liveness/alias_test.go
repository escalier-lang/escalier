package liveness

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewAliasTracker(t *testing.T) {
	tracker := NewAliasTracker()
	require.NotNil(t, tracker)
	require.Empty(t, tracker.Sets)
	require.Empty(t, tracker.VarToSets)
}

func TestNewValue(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1

	tracker.NewValue(x, AliasImmutable)

	sets := tracker.GetAliasSets(x)
	require.Len(t, sets, 1)
	require.Equal(t, x, sets[0].Origin)
	require.Equal(t, AliasImmutable, sets[0].Members[x])
}

func TestNewValueMutable(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1

	tracker.NewValue(x, AliasMutable)

	sets := tracker.GetAliasSets(x)
	require.Len(t, sets, 1)
	require.Equal(t, AliasMutable, sets[0].Members[x])
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
	require.Len(t, xSets, 1)
	require.Len(t, ySets, 1)
	require.Equal(t, xSets[0].ID, ySets[0].ID)
	require.Len(t, xSets[0].Members, 2)
}

func TestAddAliasMutableToImmutable(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	var y VarID = 2

	tracker.NewValue(x, AliasImmutable)
	tracker.AddAlias(y, x, AliasMutable)

	sets := tracker.GetAliasSets(y)
	require.Len(t, sets, 1)
	require.Equal(t, AliasImmutable, sets[0].Members[x], "x should remain immutable")
	require.Equal(t, AliasMutable, sets[0].Members[y], "y should be mutable")
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
	require.Len(t, xSets, 1)
	require.NotContains(t, xSets[0].Members, y, "y should not be in x's alias set after reassignment")

	// y should be in z's alias set
	ySets := tracker.GetAliasSets(y)
	require.Len(t, ySets, 1)
	require.Equal(t, AliasMutable, ySets[0].Members[y])
	require.Contains(t, ySets[0].Members, z, "z should be in y's alias set")
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
	require.Len(t, ySets, 1)
	require.Equal(t, y, ySets[0].Origin)

	// x's alias set should not contain y
	xSets := tracker.GetAliasSets(x)
	require.Len(t, xSets, 1)
	require.NotContains(t, xSets[0].Members, y, "y should not be in x's alias set")
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
	require.Len(t, xSets, 1)
	require.Len(t, ySets, 1)
	require.Equal(t, xSets[0].ID, ySets[0].ID)
	require.Len(t, xSets[0].Members, 2)
}

func TestGetAliasSetsEmpty(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1

	sets := tracker.GetAliasSets(x)
	require.Empty(t, sets)
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
			require.False(t, seen[id], "VarToSets[%d] contains duplicate SetID %d", v, id)
			seen[id] = true
		}
	}
}

func TestReassignMulti(t *testing.T) {
	tracker := NewAliasTracker()
	var a VarID = 1
	var b VarID = 2
	var x VarID = 3

	tracker.NewValue(a, AliasMutable)
	tracker.NewValue(b, AliasMutable)
	tracker.NewValue(x, AliasImmutable)

	// Capture x's original set ID before reassignment.
	originalSetID := tracker.GetAliasSets(x)[0].ID

	// Reassign x to alias both a and b (conditional aliasing)
	tracker.ReassignMulti(x, []VarID{a, b}, AliasImmutable)

	// x should be in both a's and b's alias sets
	xSets := tracker.GetAliasSets(x)
	require.Len(t, xSets, 2)

	aSets := tracker.GetAliasSets(a)
	require.Len(t, aSets, 1)
	require.Contains(t, aSets[0].Members, x)

	bSets := tracker.GetAliasSets(b)
	require.Len(t, bSets, 1)
	require.Contains(t, bSets[0].Members, x)

	// x's original fresh set should no longer contain x.
	originalSet := tracker.Sets[originalSetID]
	require.NotNil(t, originalSet)
	require.NotContains(t, originalSet.Members, x)
}

func TestReassignMultiRemovesFromPreviousSets(t *testing.T) {
	tracker := NewAliasTracker()
	var a VarID = 1
	var b VarID = 2
	var c VarID = 3
	var x VarID = 4

	tracker.NewValue(a, AliasMutable)
	tracker.NewValue(b, AliasMutable)
	tracker.NewValue(c, AliasMutable)

	// First alias x with a
	tracker.AddAlias(x, a, AliasImmutable)
	aSets := tracker.GetAliasSets(a)
	require.Len(t, aSets, 1)
	require.Contains(t, aSets[0].Members, x)

	// Now reassign x to alias b and c
	tracker.ReassignMulti(x, []VarID{b, c}, AliasImmutable)

	// x should no longer be in a's set
	aSets = tracker.GetAliasSets(a)
	require.Len(t, aSets, 1)
	require.NotContains(t, aSets[0].Members, x)

	// x should be in both b's and c's sets
	bSets := tracker.GetAliasSets(b)
	require.Len(t, bSets, 1)
	require.Contains(t, bSets[0].Members, x)

	cSets := tracker.GetAliasSets(c)
	require.Len(t, cSets, 1)
	require.Contains(t, cSets[0].Members, x)
}

func TestReassignMultiEmptySources(t *testing.T) {
	tracker := NewAliasTracker()
	var a VarID = 1
	var x VarID = 2

	tracker.NewValue(a, AliasMutable)
	tracker.AddAlias(x, a, AliasImmutable)

	// Reassigning with empty sources should create a fresh set (like Reassign with nil source).
	tracker.ReassignMulti(x, []VarID{}, AliasImmutable)

	// x should have been removed from a's set
	aSets := tracker.GetAliasSets(a)
	require.Len(t, aSets, 1)
	require.NotContains(t, aSets[0].Members, x)

	// x should have its own fresh set
	xSets := tracker.GetAliasSets(x)
	require.Len(t, xSets, 1)
	require.Contains(t, xSets[0].Members, x)
}

func TestReassignMultiUntrackedSources(t *testing.T) {
	// When all source VarIDs have no entries in VarToSets (i.e., they were
	// never tracked), ReassignMulti should fall back to creating a fresh set
	// so that v doesn't end up untracked.
	tracker := NewAliasTracker()
	var x VarID = 1

	tracker.NewValue(x, AliasImmutable)

	// Sources 10 and 20 were never added to the tracker.
	tracker.ReassignMulti(x, []VarID{10, 20}, AliasImmutable)

	// x should still be tracked — it should have a fresh alias set.
	xSets := tracker.GetAliasSets(x)
	require.Len(t, xSets, 1, "x should have a fresh alias set when all sources are untracked")
	require.Contains(t, xSets[0].Members, x)
}

func TestMergeAliasSetsNoOp(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	var y VarID = 2

	// y has no alias set - merge should be a no-op
	tracker.NewValue(x, AliasImmutable)
	tracker.MergeAliasSets(x, y)

	xSets := tracker.GetAliasSets(x)
	require.Len(t, xSets, 1)
	require.Len(t, xSets[0].Members, 1)
}
