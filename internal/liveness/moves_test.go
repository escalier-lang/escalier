package liveness

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/stretchr/testify/require"
)

// newBlock builds a CFG basic block with nStmts statement slots. AnalyzeMoves
// reads only len(Stmts) to size its per-statement arrays, so nil slots are fine.
func mvBlock(id, nStmts int) *BasicBlock {
	return &BasicBlock{ID: id, Stmts: make([]ast.Stmt, nStmts)}
}

// mvEdge wires from → to, populating both the successor and predecessor lists the
// dataflow reads.
func mvEdge(from, to *BasicBlock) {
	from.Successors = append(from.Successors, to)
	to.Predecessors = append(to.Predecessors, from)
}

// mvCFG assembles a CFG from blocks. Blocks must be passed with IDs equal to
// their index, matching BuildCFG's newBlock numbering.
func mvCFG(blocks ...*BasicBlock) *CFG {
	return &CFG{Entry: blocks[0], Exit: blocks[len(blocks)-1], Blocks: blocks}
}

// moves builds a moves map from each StmtRef to the VarIDs that statement moves,
// the input AnalyzeMoves takes.
func moves(entries map[StmtRef][]VarID) map[StmtRef]set.Set[VarID] {
	out := map[StmtRef]set.Set[VarID]{}
	for ref, vs := range entries {
		out[ref] = set.FromSlice(vs)
	}
	return out
}

func TestJoinMoveState(t *testing.T) {
	cases := []struct {
		a, b, want MoveState
	}{
		{NotMoved, NotMoved, NotMoved},
		{NotMoved, Moved, MaybeMoved},
		{Moved, NotMoved, MaybeMoved},
		{Moved, Moved, Moved},
		{NotMoved, MaybeMoved, MaybeMoved},
		{Moved, MaybeMoved, MaybeMoved},
		{MaybeMoved, MaybeMoved, MaybeMoved},
	}
	for _, c := range cases {
		require.Equal(t, c.want, JoinMoveState(c.a, c.b), "%v ⊔ %v", c.a, c.b)
		require.Equal(t, c.want, JoinMoveState(c.b, c.a), "%v ⊔ %v (commuted)", c.b, c.a)
	}
}

// An if/else where only the then-branch moves x leaves x MaybeMoved at the merge:
// one reaching path moved it, the other did not.
func TestMovesIfElseOneBranch(t *testing.T) {
	const x VarID = 1
	entry := mvBlock(0, 1) // cond
	then := mvBlock(1, 1)  // move x
	els := mvBlock(2, 0)
	merge := mvBlock(3, 1) // use x
	mvEdge(entry, then)
	mvEdge(entry, els)
	mvEdge(then, merge)
	mvEdge(els, merge)
	cfg := mvCFG(entry, then, els, merge)

	info := AnalyzeMoves(cfg, moves(map[StmtRef][]VarID{
		{BlockID: 1, StmtIdx: 0}: {x},
	}))

	require.Equal(t, Moved, info.StateAfter(StmtRef{BlockID: 1, StmtIdx: 0}, x))
	require.Equal(t, NotMoved, info.StateBefore(StmtRef{BlockID: 2, StmtIdx: 0}, x))
	require.Equal(t, MaybeMoved, info.StateBefore(StmtRef{BlockID: 3, StmtIdx: 0}, x))
}

// When both branches move x it is Moved at the merge: every reaching path moved it.
func TestMovesIfElseBothBranches(t *testing.T) {
	const x VarID = 1
	entry := mvBlock(0, 1)
	then := mvBlock(1, 1)
	els := mvBlock(2, 1)
	merge := mvBlock(3, 1)
	mvEdge(entry, then)
	mvEdge(entry, els)
	mvEdge(then, merge)
	mvEdge(els, merge)
	cfg := mvCFG(entry, then, els, merge)

	info := AnalyzeMoves(cfg, moves(map[StmtRef][]VarID{
		{BlockID: 1, StmtIdx: 0}: {x},
		{BlockID: 2, StmtIdx: 0}: {x},
	}))

	require.Equal(t, Moved, info.StateBefore(StmtRef{BlockID: 3, StmtIdx: 0}, x))
}

// A binding no path moves is NotMoved at the merge.
func TestMovesIfElseNeitherBranch(t *testing.T) {
	const x VarID = 1
	entry := mvBlock(0, 1)
	then := mvBlock(1, 1)
	els := mvBlock(2, 1)
	merge := mvBlock(3, 1)
	mvEdge(entry, then)
	mvEdge(entry, els)
	mvEdge(then, merge)
	mvEdge(els, merge)
	cfg := mvCFG(entry, then, els, merge)

	info := AnalyzeMoves(cfg, moves(nil))
	require.Equal(t, NotMoved, info.StateBefore(StmtRef{BlockID: 3, StmtIdx: 0}, x))
}

// A three-arm match: two arms move x, one does not, so x is MaybeMoved at the
// merge. When every arm moves it, x is Moved.
func TestMovesMatchArms(t *testing.T) {
	const x VarID = 1
	build := func(armsMoving []int) *MoveInfo {
		entry := mvBlock(0, 1)
		arm1 := mvBlock(1, 1)
		arm2 := mvBlock(2, 1)
		arm3 := mvBlock(3, 1)
		merge := mvBlock(4, 1)
		for _, arm := range []*BasicBlock{arm1, arm2, arm3} {
			mvEdge(entry, arm)
			mvEdge(arm, merge)
		}
		cfg := mvCFG(entry, arm1, arm2, arm3, merge)
		entries := map[StmtRef][]VarID{}
		for _, arm := range armsMoving {
			entries[StmtRef{BlockID: arm, StmtIdx: 0}] = []VarID{x}
		}
		return AnalyzeMoves(cfg, moves(entries))
	}

	twoOfThree := build([]int{1, 2})
	require.Equal(t, MaybeMoved, twoOfThree.StateBefore(StmtRef{BlockID: 4, StmtIdx: 0}, x))

	allThree := build([]int{1, 2, 3})
	require.Equal(t, Moved, allThree.StateBefore(StmtRef{BlockID: 4, StmtIdx: 0}, x))
}

// A loop body that moves x leaves x MaybeMoved at the loop header and at the exit:
// the body may run zero times (NotMoved) or at least once (Moved), and the back
// edge joins those into MaybeMoved.
func TestMovesLoopBackEdge(t *testing.T) {
	const x VarID = 1
	entry := mvBlock(0, 0)
	header := mvBlock(1, 1) // cond, use x
	body := mvBlock(2, 1)   // move x
	exit := mvBlock(3, 1)   // use x
	mvEdge(entry, header)
	mvEdge(header, body)
	mvEdge(header, exit)
	mvEdge(body, header) // back edge
	cfg := mvCFG(entry, header, body, exit)

	info := AnalyzeMoves(cfg, moves(map[StmtRef][]VarID{
		{BlockID: 2, StmtIdx: 0}: {x},
	}))

	require.Equal(t, MaybeMoved, info.StateBefore(StmtRef{BlockID: 1, StmtIdx: 0}, x))
	require.Equal(t, MaybeMoved, info.StateBefore(StmtRef{BlockID: 3, StmtIdx: 0}, x))
}

// Within one block, move state advances statement by statement: NotMoved before
// the move, Moved after it and at every later statement.
func TestMovesSequentialWithinBlock(t *testing.T) {
	const x VarID = 1
	b := mvBlock(0, 3) // s0: nothing, s1: move x, s2: use x
	cfg := mvCFG(b)

	info := AnalyzeMoves(cfg, moves(map[StmtRef][]VarID{
		{BlockID: 0, StmtIdx: 1}: {x},
	}))

	require.Equal(t, NotMoved, info.StateBefore(StmtRef{BlockID: 0, StmtIdx: 0}, x))
	require.Equal(t, NotMoved, info.StateBefore(StmtRef{BlockID: 0, StmtIdx: 1}, x))
	require.Equal(t, Moved, info.StateAfter(StmtRef{BlockID: 0, StmtIdx: 1}, x))
	require.Equal(t, Moved, info.StateBefore(StmtRef{BlockID: 0, StmtIdx: 2}, x))
}

// A second move along the same path overrides the first rather than failing the
// analysis: the state stays Moved, the unconditional use-after-move PR 6 reports.
func TestMovesDoubleMoveStaysMoved(t *testing.T) {
	const x VarID = 1
	b := mvBlock(0, 2)
	cfg := mvCFG(b)

	info := AnalyzeMoves(cfg, moves(map[StmtRef][]VarID{
		{BlockID: 0, StmtIdx: 0}: {x},
		{BlockID: 0, StmtIdx: 1}: {x},
	}))

	require.Equal(t, Moved, info.StateAfter(StmtRef{BlockID: 0, StmtIdx: 0}, x))
	require.Equal(t, Moved, info.StateAfter(StmtRef{BlockID: 0, StmtIdx: 1}, x))
}

// Independent bindings keep independent state: moving x along one branch does not
// disturb y, which no path moves.
func TestMovesIndependentBindings(t *testing.T) {
	const x, y VarID = 1, 2
	entry := mvBlock(0, 1)
	then := mvBlock(1, 1)
	els := mvBlock(2, 1)
	merge := mvBlock(3, 1)
	mvEdge(entry, then)
	mvEdge(entry, els)
	mvEdge(then, merge)
	mvEdge(els, merge)
	cfg := mvCFG(entry, then, els, merge)

	info := AnalyzeMoves(cfg, moves(map[StmtRef][]VarID{
		{BlockID: 1, StmtIdx: 0}: {x},
	}))

	require.Equal(t, MaybeMoved, info.StateBefore(StmtRef{BlockID: 3, StmtIdx: 0}, x))
	require.Equal(t, NotMoved, info.StateBefore(StmtRef{BlockID: 3, StmtIdx: 0}, y))
}

// A move recorded at the synthetic -1 entry position — where a decomposed
// DeclStmt's StmtRef points — takes effect at block entry, before statement 0.
func TestMovesSyntheticEntryPosition(t *testing.T) {
	const x VarID = 1
	b := mvBlock(0, 1)
	cfg := mvCFG(b)

	info := AnalyzeMoves(cfg, moves(map[StmtRef][]VarID{
		{BlockID: 0, StmtIdx: -1}: {x},
	}))

	// Before the -1 position the entry move has not yet applied; after it, and
	// before statement 0, it has.
	require.Equal(t, NotMoved, info.StateBefore(StmtRef{BlockID: 0, StmtIdx: -1}, x))
	require.Equal(t, Moved, info.StateAfter(StmtRef{BlockID: 0, StmtIdx: -1}, x))
	require.Equal(t, Moved, info.StateBefore(StmtRef{BlockID: 0, StmtIdx: 0}, x))
}
