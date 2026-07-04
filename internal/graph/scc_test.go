package graph

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// adjSuccessors adapts an adjacency map to the successors function shape.
func adjSuccessors[T comparable](edges map[T][]T) func(T) []T {
	return func(v T) []T { return edges[v] }
}

// normalize sorts each component and then the outer list by first member so
// assertions don't depend on Tarjan's visit order for membership tests.
func normalize(sccs [][]int) [][]int {
	out := make([][]int, len(sccs))
	for i, scc := range sccs {
		s := append([]int(nil), scc...)
		sort.Ints(s)
		out[i] = s
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

func TestStronglyConnectedComponents(t *testing.T) {
	tests := []struct {
		name  string
		nodes []int
		edges map[int][]int
		want  [][]int
	}{
		{
			name:  "empty graph",
			nodes: nil,
			edges: nil,
			want:  [][]int{},
		},
		{
			name:  "single node no edges",
			nodes: []int{1},
			edges: map[int][]int{},
			want:  [][]int{{1}},
		},
		{
			name:  "self loop is its own component",
			nodes: []int{1},
			edges: map[int][]int{1: {1}},
			want:  [][]int{{1}},
		},
		{
			name:  "two-node cycle collapses to one component",
			nodes: []int{1, 2},
			edges: map[int][]int{1: {2}, 2: {1}},
			want:  [][]int{{1, 2}},
		},
		{
			name:  "acyclic chain yields three singletons",
			nodes: []int{1, 2, 3},
			edges: map[int][]int{1: {2}, 2: {3}},
			want:  [][]int{{1}, {2}, {3}},
		},
		{
			name:  "two disjoint cycles",
			nodes: []int{1, 2, 3, 4},
			edges: map[int][]int{1: {2}, 2: {1}, 3: {4}, 4: {3}},
			want:  [][]int{{1, 2}, {3, 4}},
		},
		{
			name:  "one-way importer of a cycle stays a singleton",
			nodes: []int{1, 2, 3},
			edges: map[int][]int{1: {2}, 2: {3}, 3: {2}},
			want:  [][]int{{1}, {2, 3}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StronglyConnectedComponents(tt.nodes, adjSuccessors(tt.edges))
			require.Equal(t, tt.want, normalize(got))
		})
	}
}

// TestStronglyConnectedComponents_ReverseTopologicalOrder pins the ordering
// contract: a component is emitted only after every component reachable from it.
// The graph 1 → 2 → {3 ↔ 4} must emit {3,4}, then {2}, then {1}.
func TestStronglyConnectedComponents_ReverseTopologicalOrder(t *testing.T) {
	edges := map[int][]int{1: {2}, 2: {3}, 3: {4}, 4: {3}}
	got := StronglyConnectedComponents([]int{1, 2, 3, 4}, adjSuccessors(edges))

	require.Len(t, got, 3)
	// {3,4} finishes first, then the singletons 2 and 1 in that order.
	first := append([]int(nil), got[0]...)
	sort.Ints(first)
	require.Equal(t, []int{3, 4}, first)
	require.Equal(t, []int{2}, got[1])
	require.Equal(t, []int{1}, got[2])
}

// TestStronglyConnectedComponents_TargetNotSeeded verifies a vertex reached only
// through successors but absent from nodes is still placed in a component. Here
// 2 is a successor of 1 but not in the seed list, yet both come back.
func TestStronglyConnectedComponents_TargetNotSeeded(t *testing.T) {
	edges := map[int][]int{1: {2}}
	got := StronglyConnectedComponents([]int{1}, adjSuccessors(edges))
	require.Equal(t, [][]int{{2}, {1}}, got)
}

// TestStronglyConnectedComponents_Strings exercises the helper on a non-int key
// type, matching the checker's string-URI usage.
func TestStronglyConnectedComponents_Strings(t *testing.T) {
	edges := map[string][]string{
		"a": {"b"},
		"b": {"a"},
		"c": {"a"},
	}
	got := StronglyConnectedComponents([]string{"a", "b", "c"}, adjSuccessors(edges))

	out := make([][]string, len(got))
	for i, scc := range got {
		s := append([]string(nil), scc...)
		sort.Strings(s)
		out[i] = s
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	require.Equal(t, [][]string{{"a", "b"}, {"c"}}, out)
}
