package solver

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
)

// Flow-sensitive borrow-edge tracking. The borrow-edge graph records which function-locals
// each binding borrows. A single accumulate-only map cannot express that a `var`
// reassignment replaces the binding's referent or that a field store repoints one field, so
// the graph is computed per CFG program point instead.
//
// The analysis has two halves that mirror the move engine's consumed lattice:
//
//   - While the body is walked, each statement's borrow recording updates the eager
//     borrowEdges map by strong subtree replacement and records the binding's new edge set
//     into borrowGens, keyed by the statement's CFG position. The eager map holds the
//     source-order-current state so copyPlaceEdges can project a source binding's edges.
//   - After the walk, analyzeBorrows folds borrowGens forward over the CFG. Each assignment
//     replaces a binding's whole edge set, so a reassignment clears the prior referent, and
//     branch merges join edge sets by union, so a borrow set on one branch reaches the merge.
//
// The escape check then reads the edge set at each flow-out site's program point rather than
// a single whole-body map.

// borrowAssign is one statement's replacement of a binding's borrow edges: the binding root
// and the full edge set the eager walk computed for it at that statement. The dataflow
// replaces the root's whole edge set with these, so a repoint that leaves other fields
// untouched still carries them, since the eager map already merged the retained fields into
// the recorded set.
type borrowAssign struct {
	root  liveness.VarID
	edges []fieldBorrow
}

// markBorrowDirty flags root as having its eager edge set changed during the current
// statement, so flushBorrowGens emits an assignment for it.
func (c *checker) markBorrowDirty(root liveness.VarID) {
	if c.fn == nil || c.fn.borrowDirty == nil || root <= 0 {
		return
	}
	c.fn.borrowDirty.Add(root)
}

// clearEagerSubtree removes from root's eager edge set every edge whose path lies at or below
// base, so a `var` reassignment clears the binding wholly with a nil base and a field store
// clears only the stored field's subtree. It marks root dirty when it removed an edge, so a
// reassignment away from a borrow emits an assignment that kills the prior referent in the
// dataflow, while a borrow-free binding that clears nothing emits no spurious assignment.
func (c *checker) clearEagerSubtree(root liveness.VarID, base []placeSeg) {
	if c.fn == nil || c.fn.borrowEdges == nil || root <= 0 {
		return
	}
	edges := c.fn.borrowEdges[root]
	kept := edges[:0:0]
	removed := false
	for _, e := range edges {
		if pathHasPrefix(e.path, base) {
			removed = true
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) == 0 {
		delete(c.fn.borrowEdges, root)
	} else {
		c.fn.borrowEdges[root] = kept
	}
	if removed {
		c.markBorrowDirty(root)
	}
}

// flushBorrowGens records, for every root dirtied while recording the statement at ref, an
// assignment carrying that root's current eager edge set. It then resets the dirty set for
// the next statement. A root cleared to no edges still emits an empty assignment, which the
// dataflow applies as a kill of the prior referent.
func (c *checker) flushBorrowGens(ref liveness.StmtRef) {
	if c.fn == nil || c.fn.borrowDirty == nil || c.fn.borrowGens == nil {
		return
	}
	roots := c.fn.borrowDirty.ToSlice()
	slices.Sort(roots)
	for _, root := range roots {
		edges := slices.Clone(c.fn.borrowEdges[root])
		c.fn.borrowGens[ref] = append(c.fn.borrowGens[ref], borrowAssign{root: root, edges: edges})
	}
	c.fn.borrowDirty = set.NewSet[liveness.VarID]()
}

// borrowInfo holds the per-program-point borrow-edge graph analyzeBorrows computed. before[b]
// [i] is the edge graph just before statement i of block b, joined across every path reaching
// it; blockIn[b] is the graph on entry to block b, the answer for a StmtRef whose index is the
// synthetic -1 entry position a decomposed decl points at.
type borrowInfo struct {
	before  [][]map[liveness.VarID][]fieldBorrow
	blockIn []map[liveness.VarID][]fieldBorrow
}

// analyzeBorrows folds borrowGens forward over the body's CFG to a fixed point, producing the
// borrow-edge graph at every program point. Each statement's assignments replace their roots'
// edge sets; branch merges union the incoming graphs per root. State only grows in the finite
// per-root edge lattice within a fixed binding set, so the iteration terminates.
func (c *checker) analyzeBorrows() *borrowInfo {
	cfg := c.fn.cfg
	gens := c.fn.borrowGens
	n := len(cfg.Blocks)

	blockIn := make([]map[liveness.VarID][]fieldBorrow, n)
	blockOut := make([]map[liveness.VarID][]fieldBorrow, n)
	for i := range n {
		blockIn[i] = map[liveness.VarID][]fieldBorrow{}
		blockOut[i] = map[liveness.VarID][]fieldBorrow{}
	}

	changed := true
	for changed {
		changed = false
		for _, block := range cfg.Blocks {
			newIn := joinBorrowPreds(block, blockOut)
			newOut := applyBlockBorrows(newIn, block, gens)
			if !borrowStateEqual(blockIn[block.ID], newIn) || !borrowStateEqual(blockOut[block.ID], newOut) {
				changed = true
				blockIn[block.ID] = newIn
				blockOut[block.ID] = newOut
			}
		}
	}

	before := make([][]map[liveness.VarID][]fieldBorrow, n)
	for _, block := range cfg.Blocks {
		m := len(block.Stmts)
		before[block.ID] = make([]map[liveness.VarID][]fieldBorrow, m)
		state := applyBorrowAssigns(blockIn[block.ID], borrowAssignsAt(gens, block.ID, -1))
		for idx := range m {
			before[block.ID][idx] = state
			state = applyBorrowAssigns(state, borrowAssignsAt(gens, block.ID, idx))
		}
	}

	return &borrowInfo{before: before, blockIn: blockIn}
}

// edgesBefore returns the borrow-edge graph just before the statement at ref, joined across
// every path that reaches it. A StmtIdx of -1 reads the block's entry graph. An out-of-range
// ref yields an empty graph.
func (b *borrowInfo) edgesBefore(ref liveness.StmtRef) map[liveness.VarID][]fieldBorrow {
	if ref.BlockID < 0 || ref.BlockID >= len(b.before) {
		return map[liveness.VarID][]fieldBorrow{}
	}
	if ref.StmtIdx < 0 {
		return b.blockIn[ref.BlockID]
	}
	block := b.before[ref.BlockID]
	if ref.StmtIdx >= len(block) {
		return map[liveness.VarID][]fieldBorrow{}
	}
	return block[ref.StmtIdx]
}

// joinBorrowPreds computes a block's entry graph as the per-root union of every predecessor's
// exit graph. A borrow that reaches through only one predecessor is kept, the may-reach
// direction that never drops a real escape.
func joinBorrowPreds(block *liveness.BasicBlock, blockOut []map[liveness.VarID][]fieldBorrow) map[liveness.VarID][]fieldBorrow {
	in := map[liveness.VarID][]fieldBorrow{}
	for _, pred := range block.Predecessors {
		for root, edges := range blockOut[pred.ID] {
			in[root] = unionEdges(in[root], edges)
		}
	}
	return in
}

// applyBlockBorrows folds every statement's assignments in the block, in order, over the entry
// graph — the synthetic -1 entry position first, then each real statement.
func applyBlockBorrows(in map[liveness.VarID][]fieldBorrow, block *liveness.BasicBlock, gens map[liveness.StmtRef][]borrowAssign) map[liveness.VarID][]fieldBorrow {
	state := applyBorrowAssigns(in, borrowAssignsAt(gens, block.ID, -1))
	for idx := range block.Stmts {
		state = applyBorrowAssigns(state, borrowAssignsAt(gens, block.ID, idx))
	}
	return state
}

// applyBorrowAssigns returns a copy of state with each assignment's root re-pointed to that
// assignment's edge set, dropping the root's key when the new set is empty.
func applyBorrowAssigns(state map[liveness.VarID][]fieldBorrow, assigns []borrowAssign) map[liveness.VarID][]fieldBorrow {
	out := cloneBorrowState(state)
	for _, a := range assigns {
		if len(a.edges) == 0 {
			delete(out, a.root)
			continue
		}
		out[a.root] = slices.Clone(a.edges)
	}
	return out
}

// borrowAssignsAt returns the assignments recorded at one program point, or nil when the point
// records none.
func borrowAssignsAt(gens map[liveness.StmtRef][]borrowAssign, blockID, stmtIdx int) []borrowAssign {
	return gens[liveness.StmtRef{BlockID: blockID, StmtIdx: stmtIdx}]
}

// unionEdges returns the edges in a or b with no duplicate path/referent pair, the join at a
// CFG merge.
func unionEdges(a, b []fieldBorrow) []fieldBorrow {
	out := slices.Clone(a)
	for _, e := range b {
		if !containsEdge(out, e) {
			out = append(out, e)
		}
	}
	return out
}

// containsEdge reports whether edges holds one with the same path and referent as e.
func containsEdge(edges []fieldBorrow, e fieldBorrow) bool {
	for _, x := range edges {
		if x.referent == e.referent && slices.Equal(x.path, e.path) {
			return true
		}
	}
	return false
}

// cloneBorrowState copies the graph one level deep: a fresh outer map whose edge slices are
// shared, since applyBorrowAssigns replaces a root's slice wholesale rather than mutating it.
func cloneBorrowState(m map[liveness.VarID][]fieldBorrow) map[liveness.VarID][]fieldBorrow {
	out := make(map[liveness.VarID][]fieldBorrow, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// borrowStateEqual reports whether two graphs hold the same edge set for every root, the
// fixed-point convergence test.
func borrowStateEqual(a, b map[liveness.VarID][]fieldBorrow) bool {
	if len(a) != len(b) {
		return false
	}
	for root, ae := range a {
		be, ok := b[root]
		if !ok || !edgeSetEqual(ae, be) {
			return false
		}
	}
	return true
}

// edgeSetEqual reports whether two edge slices hold the same path/referent pairs regardless of
// order.
func edgeSetEqual(a, b []fieldBorrow) bool {
	if len(a) != len(b) {
		return false
	}
	for _, e := range a {
		if !containsEdge(b, e) {
			return false
		}
	}
	return true
}
