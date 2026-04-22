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
	_, inXSet := xSets[0].Members[y]
	require.False(t, inXSet, "y should not be in x's alias set after reassignment")

	// y should be in z's alias set
	ySets := tracker.GetAliasSets(y)
	require.Len(t, ySets, 1)
	require.Equal(t, AliasMutable, ySets[0].Members[y])
	_, inYSet := ySets[0].Members[z]
	require.True(t, inYSet, "z should be in y's alias set")
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
	_, inXSet := xSets[0].Members[y]
	require.False(t, inXSet, "y should not be in x's alias set")
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
