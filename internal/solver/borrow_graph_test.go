package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/stretchr/testify/require"
)

// TestCollectEdgesFromCyclicGraph covers collectEdgesFrom over a cyclic borrow-edge graph. A
// cycle where a borrows b and b borrows a is not expressible in source today, since it needs a
// recursive type alias for the mutually-referential carriers. So each case builds the graph
// directly and asserts that the walk terminates and reaches every node, the root included.
func TestCollectEdgesFromCyclicGraph(t *testing.T) {
	edge := func(referent liveness.VarID) fieldBorrow {
		return fieldBorrow{referent: referent}
	}
	tests := map[string]struct {
		graph map[liveness.VarID][]fieldBorrow
		root  liveness.VarID
		want  []liveness.VarID
	}{
		// a ⇄ b. Reaching b follows b's edge back to a.
		"TwoNodeCycle": {
			graph: map[liveness.VarID][]fieldBorrow{
				1: {edge(2)},
				2: {edge(1)},
			},
			root: 1,
			want: []liveness.VarID{1, 2},
		},
		// a → b → c → a. The `seen` set stops the walk at the third hop.
		"ThreeNodeCycle": {
			graph: map[liveness.VarID][]fieldBorrow{
				1: {edge(2)},
				2: {edge(3)},
				3: {edge(1)},
			},
			root: 1,
			want: []liveness.VarID{1, 2, 3},
		},
		// a → a and a → b. The self-edge is followed once, and b is still reached.
		"SelfLoopAndOnward": {
			graph: map[liveness.VarID][]fieldBorrow{
				1: {edge(1), edge(2)},
			},
			root: 1,
			want: []liveness.VarID{1, 2},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var edges []fieldBorrow
			collectEdgesFrom(tc.root, nil, set.NewSet[liveness.VarID](), tc.graph, &edges)
			reached := set.NewSet[liveness.VarID]()
			for _, e := range edges {
				reached.Add(e.referent)
			}
			require.ElementsMatch(t, tc.want, reached.ToSlice())
		})
	}
}
