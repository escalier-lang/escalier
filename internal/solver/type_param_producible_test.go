package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestForcedConcreteFloorThroughABorrow covers a lower bound of a parameter `U` that borrows a
// variable `v` whose own lower bound is another parameter `T`. Only an immutable borrow of a
// variable in readVars is walked as `v`, which reaches `T` and forces nothing. Any other
// borrow is the floor itself.
func TestForcedConcreteFloorThroughABorrow(t *testing.T) {
	tests := []struct {
		name   string
		mut    bool
		isRead bool
		forced bool
	}{
		{name: "immutable borrow of a read variable", mut: false, isRead: true, forced: false},
		{name: "mutable borrow of a read variable", mut: true, isRead: true, forced: true},
		{name: "immutable borrow of another variable", mut: false, isRead: false, forced: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			param := &soltype.TypeVarType{ID: 1}
			root := &soltype.TypeVarType{ID: 2}
			v := &soltype.TypeVarType{ID: 3, LowerBounds: []soltype.Type{param}}
			borrow := &soltype.RefType{Mut: tt.mut, Lt: soltype.Static, Inner: v}
			root.LowerBounds = []soltype.Type{borrow}
			readVars := set.NewSet[*soltype.TypeVarType]()
			if tt.isRead {
				readVars.Add(v)
			}
			notAdmitted := func(soltype.Type) bool { return false }

			floor, forced := forcedConcreteFloor(root, notAdmitted, set.FromSlice([]*soltype.TypeVarType{param, root}), readVars)
			require.Equal(t, tt.forced, forced)
			if tt.forced {
				require.Same(t, soltype.Type(borrow), floor)
			}
		})
	}
}
