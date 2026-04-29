package liveness

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// formatAliasSets formats alias sets as a readable string for test assertions.
// Each set is shown as {member(mut), member(immut)} with members sorted by name.
// Multiple sets are separated by commas: [{a(mut), x(immut)}, {b(mut), x(immut)}]
// An empty result is shown as [].
func formatAliasSets(sets []*AliasSet, names map[VarID]string) string {
	if len(sets) == 0 {
		return "[]"
	}

	sorted := make([]*AliasSet, len(sets))
	copy(sorted, sets)
	slices.SortFunc(sorted, func(a, b *AliasSet) int {
		return int(a.ID) - int(b.ID)
	})

	var parts []string
	for _, set := range sorted {
		memberIDs := make([]VarID, 0, len(set.Members))
		for id := range set.Members {
			memberIDs = append(memberIDs, id)
		}
		slices.SortFunc(memberIDs, func(a, b VarID) int { return int(a) - int(b) })

		var members []string
		for _, id := range memberIDs {
			name := names[id]
			if name == "" {
				name = fmt.Sprintf("%d", id)
			}
			if set.Members[id] == AliasMutable {
				members = append(members, name+"(mut)")
			} else {
				members = append(members, name+"(immut)")
			}
		}
		parts = append(parts, "{"+strings.Join(members, ", ")+"}")
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func TestNewValue(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	names := map[VarID]string{1: "x"}

	tracker.NewValue(x, AliasImmutable)

	// x should be in its own set with the mutability it was created with
	require.Equal(t, "[{x(immut)}]", formatAliasSets(tracker.GetAliasSets(x), names))
}

func TestAddAlias(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	var y VarID = 2
	names := map[VarID]string{1: "x", 2: "y"}

	tracker.NewValue(x, AliasImmutable)
	tracker.AddAlias(y, x, AliasImmutable)

	// Both x and y should see the same set containing both of them
	require.Equal(t, "[{x(immut), y(immut)}]", formatAliasSets(tracker.GetAliasSets(x), names))
	require.Equal(t, "[{x(immut), y(immut)}]", formatAliasSets(tracker.GetAliasSets(y), names))
}

func TestAddAliasMutableToImmutable(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	var y VarID = 2
	names := map[VarID]string{1: "x", 2: "y"}

	tracker.NewValue(x, AliasImmutable)
	tracker.AddAlias(y, x, AliasMutable)

	// Each member keeps its own mutability: x stays immut, y is mut
	require.Equal(t, "[{x(immut), y(mut)}]", formatAliasSets(tracker.GetAliasSets(y), names))
}

func TestReassignToNewSource(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	var y VarID = 2
	var z VarID = 3
	names := map[VarID]string{1: "x", 2: "y", 3: "z"}

	tracker.NewValue(x, AliasImmutable)
	tracker.AddAlias(y, x, AliasImmutable)
	tracker.NewValue(z, AliasMutable)

	tracker.Reassign(y, &z, AliasMutable)

	// y should have left x's set (x is alone) and joined z's set
	require.Equal(t, "[{x(immut)}]", formatAliasSets(tracker.GetAliasSets(x), names))
	require.Equal(t, "[{y(mut), z(mut)}]", formatAliasSets(tracker.GetAliasSets(y), names))
}

func TestReassignToFreshValue(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	var y VarID = 2
	names := map[VarID]string{1: "x", 2: "y"}

	tracker.NewValue(x, AliasImmutable)
	tracker.AddAlias(y, x, AliasImmutable)

	tracker.Reassign(y, nil, AliasMutable)

	// y should have left x's set and gotten its own fresh set
	require.Equal(t, "[{x(immut)}]", formatAliasSets(tracker.GetAliasSets(x), names))
	require.Equal(t, "[{y(mut)}]", formatAliasSets(tracker.GetAliasSets(y), names))
}

func TestMergeAliasSets(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	var y VarID = 2
	names := map[VarID]string{1: "x", 2: "y"}

	tracker.NewValue(x, AliasImmutable)
	tracker.NewValue(y, AliasMutable)

	tracker.MergeAliasSets(x, y)

	// Both should now be in a single merged set, each keeping its mutability
	require.Equal(t, "[{x(immut), y(mut)}]", formatAliasSets(tracker.GetAliasSets(x), names))
	require.Equal(t, "[{x(immut), y(mut)}]", formatAliasSets(tracker.GetAliasSets(y), names))
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

func TestMergeAliasSetsPropagatesStaticFlags(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	var y VarID = 2

	// y's set has a `mut 'static` escape; x's set has none.
	tracker.NewValue(x, AliasMutable)
	tracker.NewValue(y, AliasMutable)
	tracker.MarkStatic(y, AliasMutable)

	require.True(t, tracker.GetAliasSets(y)[0].HasStaticMutAlias)

	// Merging y into x — x is the target. The static flag from y's set
	// must be preserved on the merged target.
	tracker.MergeAliasSets(x, y)

	sets := tracker.GetAliasSets(x)
	require.Len(t, sets, 1)
	require.True(t, sets[0].HasStaticMutAlias,
		"merged set should inherit HasStaticMutAlias from the source set")
}

func TestMergeAliasSetsPropagatesStaticImmFlag(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	var y VarID = 2

	tracker.NewValue(x, AliasImmutable)
	tracker.NewValue(y, AliasImmutable)
	tracker.MarkStatic(y, AliasImmutable)

	tracker.MergeAliasSets(x, y)

	sets := tracker.GetAliasSets(x)
	require.Len(t, sets, 1)
	require.True(t, sets[0].HasStaticImmAlias,
		"merged set should inherit HasStaticImmAlias from the source set")
}

func TestReassignMulti(t *testing.T) {
	tracker := NewAliasTracker()
	var a VarID = 1
	var b VarID = 2
	var x VarID = 3
	names := map[VarID]string{1: "a", 2: "b", 3: "x"}

	tracker.NewValue(a, AliasMutable)
	tracker.NewValue(b, AliasMutable)
	tracker.NewValue(x, AliasImmutable)

	// Reassign x to alias both a and b (conditional aliasing)
	tracker.ReassignMulti(x, []VarID{a, b}, AliasImmutable)

	// x should belong to two sets — one shared with a and one shared with b
	require.Equal(t, "[{a(mut), x(immut)}, {b(mut), x(immut)}]", formatAliasSets(tracker.GetAliasSets(x), names))
	// a and b should each see x in their set but not each other
	require.Equal(t, "[{a(mut), x(immut)}]", formatAliasSets(tracker.GetAliasSets(a), names))
	require.Equal(t, "[{b(mut), x(immut)}]", formatAliasSets(tracker.GetAliasSets(b), names))
}

func TestReassignMultiRemovesFromPreviousSets(t *testing.T) {
	tracker := NewAliasTracker()
	var a VarID = 1
	var b VarID = 2
	var c VarID = 3
	var x VarID = 4
	names := map[VarID]string{1: "a", 2: "b", 3: "c", 4: "x"}

	tracker.NewValue(a, AliasMutable)
	tracker.NewValue(b, AliasMutable)
	tracker.NewValue(c, AliasMutable)

	// First alias x with a
	tracker.AddAlias(x, a, AliasImmutable)
	require.Equal(t, "[{a(mut), x(immut)}]", formatAliasSets(tracker.GetAliasSets(a), names))

	// Now reassign x to alias b and c instead
	tracker.ReassignMulti(x, []VarID{b, c}, AliasImmutable)

	// x should have been removed from a's set
	require.Equal(t, "[{a(mut)}]", formatAliasSets(tracker.GetAliasSets(a), names))
	// x should now be in b's and c's sets
	require.Equal(t, "[{b(mut), x(immut)}, {c(mut), x(immut)}]", formatAliasSets(tracker.GetAliasSets(x), names))
}

func TestReassignMultiUntrackedSources(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	names := map[VarID]string{1: "x"}

	tracker.NewValue(x, AliasImmutable)

	// Sources 10 and 20 were never added to the tracker.
	tracker.ReassignMulti(x, []VarID{10, 20}, AliasImmutable)

	// x should fall back to a fresh set rather than being left untracked
	require.Equal(t, "[{x(immut)}]", formatAliasSets(tracker.GetAliasSets(x), names))
}

func TestReassignMultiEmptySources(t *testing.T) {
	tracker := NewAliasTracker()
	var a VarID = 1
	var x VarID = 2
	names := map[VarID]string{1: "a", 2: "x"}

	tracker.NewValue(a, AliasMutable)
	tracker.AddAlias(x, a, AliasImmutable)

	tracker.ReassignMulti(x, []VarID{}, AliasImmutable)

	// x should have left a's set and gotten a fresh set
	require.Equal(t, "[{a(mut)}]", formatAliasSets(tracker.GetAliasSets(a), names))
	require.Equal(t, "[{x(immut)}]", formatAliasSets(tracker.GetAliasSets(x), names))
}

func TestMultipleAliases(t *testing.T) {
	// val b = a; val c = a
	tracker := NewAliasTracker()
	var a VarID = 1
	var b VarID = 2
	var c VarID = 3
	names := map[VarID]string{1: "a", 2: "b", 3: "c"}

	tracker.NewValue(a, AliasImmutable)
	tracker.AddAlias(b, a, AliasImmutable)
	tracker.AddAlias(c, a, AliasImmutable)

	// All three should be in the same set
	require.Equal(t, "[{a(immut), b(immut), c(immut)}]", formatAliasSets(tracker.GetAliasSets(a), names))
}

func TestChainAlias(t *testing.T) {
	// val b = a; val c = b — transitive aliasing
	tracker := NewAliasTracker()
	var a VarID = 1
	var b VarID = 2
	var c VarID = 3
	names := map[VarID]string{1: "a", 2: "b", 3: "c"}

	tracker.NewValue(a, AliasImmutable)
	tracker.AddAlias(b, a, AliasImmutable)
	tracker.AddAlias(c, b, AliasImmutable)

	// c aliases b which aliases a, so all three end up in the same set
	require.Equal(t, "[{a(immut), b(immut), c(immut)}]", formatAliasSets(tracker.GetAliasSets(a), names))
}

func TestShadowing(t *testing.T) {
	// val x = a; val x = {y: 1} — second x (x2) gets a distinct VarID
	tracker := NewAliasTracker()
	var a VarID = 1
	var x1 VarID = 2
	var x2 VarID = 3
	names := map[VarID]string{1: "a", 2: "x1", 3: "x2"}

	tracker.NewValue(a, AliasImmutable)
	tracker.AddAlias(x1, a, AliasImmutable)
	tracker.NewValue(x2, AliasImmutable)

	// x1 stays in a's set; x2 (the shadow) gets its own fresh set
	require.Equal(t, "[{a(immut), x1(immut)}]", formatAliasSets(tracker.GetAliasSets(a), names))
	require.Equal(t, "[{x2(immut)}]", formatAliasSets(tracker.GetAliasSets(x2), names))
}

func TestMergeAliasSetsNoOp(t *testing.T) {
	tracker := NewAliasTracker()
	var x VarID = 1
	var y VarID = 2
	names := map[VarID]string{1: "x", 2: "y"}

	// y has no alias set — merge should be a no-op
	tracker.NewValue(x, AliasImmutable)
	tracker.MergeAliasSets(x, y)

	// x's set should be unchanged, y should still have no sets
	require.Equal(t, "[{x(immut)}]", formatAliasSets(tracker.GetAliasSets(x), names))
	require.Equal(t, "[]", formatAliasSets(tracker.GetAliasSets(y), names))
}
