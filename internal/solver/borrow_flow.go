package solver

import (
	"maps"
	"slices"

	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
)

// Flow-sensitive borrow-edge tracking. The borrow-edge graph records which function-locals
// each binding borrows. A single accumulate-only map cannot express that a `var`
// reassignment replaces the binding's referent or that a field store repoints one field, so
// the graph is computed per CFG program point instead.
//
// The dataflow layer models borrows with the classic gen/kill formulation. Each statement is
// characterized by the facts it generates and the facts it kills, and the forward transfer
// function is out = gen ∪ (in − kill). Here a fact is a borrow edge root → local. A statement
// that re-points a binding generates the binding's new edge set and kills its prior one. The
// kill is not tracked separately: analyzeBorrows replaces a root's whole edge set with the
// generated one, so recording the new edges implicitly kills the old. Branch merges union the
// incoming graphs per root, the meet for this may-borrow problem, so a borrow present on any
// path reaches the merge.
//
// Two structures track the same borrows at different granularities and for different
// consumers:
//
//   - eagerBorrowGraph (map[VarID][]fieldBorrow) is the eager, source-order-current graph, updated
//     by strong subtree replacement as the body is walked. copyPlaceEdges reads it to project a
//     source binding's edges, so it must hold the edge set as of the copy point. Being one
//     whole-body map, it cannot express that different CFG paths reach a point with different
//     borrow sets.
//   - borrowGens (map[StmtRef][]borrowAssign) is the flow-sensitive input to the dataflow,
//     recording per CFG statement position the new edge set the eager walk computed for each
//     binding that statement re-points. It is the "gen" set of the analysis, the borrow
//     counterpart of moveSites.
//
// How they relate:
//
//   - The eager walk maintains eagerBorrowGraph and, via flushBorrowDirty, snapshots each dirtied
//     root's current edge set into borrowGens keyed by the statement's CFG position.
//   - After the walk, analyzeBorrows folds borrowGens forward over the CFG to a fixed point,
//     producing the per-program-point graph. Each assignment replaces a root's whole edge set,
//     so a reassignment clears the prior referent.
//   - At each escape/flow-out site, resolveComponentEscapes reads that point's snapshot from the
//     flow-sensitive result and passes it to each escape helper, so they see the flow-sensitive
//     graph rather than the whole-body eager one.
//
// Branch handling. The eager walk is a source-order AST traversal, so it is not branch-scoped.
// inferIfElse and inferMatch walk each arm in order, and both arms mutate the one eagerBorrowGraph
// in place. The textually-later arm's strong update overwrites the earlier one, so after the
// construct the eager map holds only the last arm's edges. It cannot represent a per-path borrow
// set. For
//
//	if cond { a = &mut b } else { a = &mut c }
//
// the eager map ends holding a → c. Each arm's assignment was snapshotted into borrowGens at its
// own CFG position, so analyzeBorrows unions them at the join block to the correct a → {b, c}.
// Two things keep this sound:
//
//   - Unconditional kill. A reassignment to a non-borrow on one arm still marks its root dirty
//     even though it pruned nothing, emitting an explicit kill at that point. In
//     `if cond { a = seed } else { a = &mut c }` the then-arm's kill lets the join union "no edge"
//     with the else-arm's a → c. The same rule covers a borrow that reaches a reassignment only
//     through a loop back edge.
//   - Straight-line-only reads. The eager map's cross-branch value is read only by copyPlaceEdges
//     within straight-line code. At an escape or flow-out site the escape check reads the
//     flow-sensitive snapshot resolveComponentEscapes passes it, so a merge never relies on the
//     last-writer-wins eager state.

// borrowAssign is one statement's replacement of a binding's borrow edges: the binding root
// and the full edge set the eager walk computed for it at that statement. The dataflow
// replaces the root's whole edge set with these, so a repoint that leaves other fields
// untouched still carries them, since the eager map already merged the retained fields into
// the recorded set. For
//
//	val recv = {a: &mut x, b: &mut y}   // recv borrows [a]→x, [b]→y
//	recv.a = &mut z                     // repoints only field a
//
// the store's assignment carries {[a]→z, [b]→y}, not just the touched {[a]→z}: the eager map
// cleared [a]→x, added [a]→z, and left [b]→y alone, so the recorded set already holds the
// retained sibling. Replacing recv's whole set with it therefore keeps [b]→y.
type borrowAssign struct {
	root         liveness.VarID
	fieldBorrows []fieldBorrow
}

// markBorrowDirty flags root as having its fieldBorrow set in the eagerBorrowGraph changed
// during the current statement, so flushBorrowDirty emits an assignment for it.
func (c *checker) markBorrowDirty(root liveness.VarID) {
	if c.fn == nil || c.fn.borrowDirty == nil || root <= 0 {
		return
	}
	c.fn.borrowDirty.Add(root)
}

// clearEagerSubtree removes from root's eager edge set every edge whose path lies at or below
// base, so a `var` reassignment clears the binding wholly with an empty base and a field store
// clears only the stored field's subtree.
//
// A whole-binding reassignment (empty base) always marks root dirty, emitting a kill even when
// the eager map held no edge to prune. That unconditional kill is what lets a reassignment clear
// a referent reaching it only through a branch join or a loop back edge; the file docstring's
// branch-handling notes give the examples. Killing wholesale is sound because the reassignment
// replaces the binding's entire edge set.
//
// A field-store subtree update (non-empty base) marks root dirty only when it pruned an edge.
// Its dataflow assignment replaces root's WHOLE edge set with the source-order eager set, so an
// unconditional whole-root emit could drop a sibling field's back-edge borrow and miss a real
// escape. A store that adds a borrow is marked dirty separately by addBorrowEdge, and one that
// prunes a prior borrow is caught by `removed`; a store that changes no borrow emits nothing.
func (c *checker) clearEagerSubtree(root liveness.VarID, base []placeSeg) {
	if c.fn == nil || c.fn.eagerBorrowGraph == nil || root <= 0 {
		return
	}
	fieldBorrows := c.fn.eagerBorrowGraph[root]
	var kept []fieldBorrow
	removed := false
	for _, fb := range fieldBorrows {
		if pathHasPrefix(fb.path, base) {
			removed = true
			continue
		}
		kept = append(kept, fb)
	}
	if len(kept) == 0 {
		delete(c.fn.eagerBorrowGraph, root)
	} else {
		c.fn.eagerBorrowGraph[root] = kept
	}
	// Dirty when this pruned an edge, and unconditionally for a whole-binding
	// reassignment so it emits a kill even when nothing was pruned. See the docstring.
	if removed || len(base) == 0 {
		c.markBorrowDirty(root)
	}
}

// flushBorrowDirty appends to borrowGens[ref], for every root dirtied while recording the
// statement at ref, an assignment carrying that root's current eager fieldBorrow set. It then
// resets the dirty set for the next statement. A root cleared to no fieldBorrows still emits an
// empty assignment, which the dataflow applies as a kill of the prior referent.
func (c *checker) flushBorrowDirty(stmtRef liveness.StmtRef) {
	if c.fn == nil || c.fn.borrowDirty == nil || c.fn.borrowGens == nil {
		return
	}
	roots := c.fn.borrowDirty.ToSlice()
	slices.Sort(roots)
	for _, root := range roots {
		fieldBorrows := slices.Clone(c.fn.eagerBorrowGraph[root])
		c.fn.borrowGens[stmtRef] = append(
			c.fn.borrowGens[stmtRef],
			borrowAssign{root: root, fieldBorrows: fieldBorrows},
		)
	}
	c.fn.borrowDirty = set.NewSet[liveness.VarID]()
}

// flowBorrowGraph holds the per-program-point borrow-edge graph analyzeBorrows computed. before[b]
// [i] is the edge graph just before statement i of block b, joined across every path reaching
// it; blockIn[b] is the graph on entry to block b, the answer for a StmtRef whose index is the
// synthetic -1 entry position a decomposed decl points at.
type flowBorrowGraph struct {
	before  [][]map[liveness.VarID][]fieldBorrow
	blockIn []map[liveness.VarID][]fieldBorrow
}

// analyzeBorrows runs the forward fold described in the file docstring, producing the borrow-edge
// graph at every program point. It terminates because state only grows in the finite per-root edge
// lattice within a fixed binding set.
func (c *checker) analyzeBorrows() *flowBorrowGraph {
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
			newOut := applyBlockBorrows(newIn, block, gens, nil)
			if !borrowStateEqual(blockIn[block.ID], newIn) || !borrowStateEqual(blockOut[block.ID], newOut) {
				changed = true // keep looping until a fixed point is reached
				blockIn[block.ID] = newIn
				blockOut[block.ID] = newOut
			}
		}
	}

	// Once the fixed point is reached, replay each block's fold from its converged entry graph
	// to recover the per-statement "before" graphs edgesBefore serves. The fixed-point loop
	// above discards these intermediates and keeps only the exit graph, so the visit callback
	// captures the graph ahead of each statement as the same fold walks past it.
	before := make([][]map[liveness.VarID][]fieldBorrow, n)
	for _, block := range cfg.Blocks {
		stmtBefore := make([]map[liveness.VarID][]fieldBorrow, len(block.Stmts))
		applyBlockBorrows(
			blockIn[block.ID], block, gens,
			func(idx int, g map[liveness.VarID][]fieldBorrow) {
				stmtBefore[idx] = g
			},
		)
		before[block.ID] = stmtBefore
	}

	return &flowBorrowGraph{before: before, blockIn: blockIn}
}

// fieldBorrowGraphBefore returns the borrow-edge graph just before the statement at ref, joined across
// every path that reaches it. A StmtIdx of -1 reads the block's entry graph. An out-of-range
// ref yields an empty graph.
func (b *flowBorrowGraph) fieldBorrowGraphBefore(
	ref liveness.StmtRef,
) map[liveness.VarID][]fieldBorrow {
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
// exit graph, keeping a borrow that reaches through even one predecessor.
func joinBorrowPreds(
	block *liveness.BasicBlock,
	blockOut []map[liveness.VarID][]fieldBorrow,
) map[liveness.VarID][]fieldBorrow {
	in := map[liveness.VarID][]fieldBorrow{}
	for _, pred := range block.Predecessors {
		for root, edges := range blockOut[pred.ID] {
			in[root] = unionEdges(in[root], edges)
		}
	}
	return in
}

// applyBlockBorrows folds the block's assignments over in, in order — the synthetic -1 entry
// position first, then each statement — and returns the block's exit graph. When visit is
// non-nil it is called with the graph just before each statement idx, letting a caller capture
// the per-statement "before" snapshots without recomputing the fold. It leaves in unmodified,
// returning fresh graphs, since applyBorrowAssigns copies its input rather than mutating it.
func applyBlockBorrows(
	in map[liveness.VarID][]fieldBorrow,
	block *liveness.BasicBlock,
	gens map[liveness.StmtRef][]borrowAssign,
	visit func(stmtIdx int, before map[liveness.VarID][]fieldBorrow),
) map[liveness.VarID][]fieldBorrow {
	state := applyBorrowAssigns(in, gens[liveness.StmtRef{BlockID: block.ID, StmtIdx: -1}])
	for idx := range block.Stmts {
		if visit != nil {
			visit(idx, state)
		}
		state = applyBorrowAssigns(state, gens[liveness.StmtRef{BlockID: block.ID, StmtIdx: idx}])
	}
	return state
}

// applyBorrowAssigns returns a copy of state with each assignment's root re-pointed to that
// assignment's edge set, dropping the root's key when the new set is empty.
func applyBorrowAssigns(
	state map[liveness.VarID][]fieldBorrow,
	assigns []borrowAssign,
) map[liveness.VarID][]fieldBorrow {
	// state is never nil. Block entry graphs start as empty maps and joinBorrowPreds returns a
	// non-nil map, so maps.Clone yields a non-nil map that the assignment writes below require.
	out := maps.Clone(state)
	for _, a := range assigns {
		if len(a.fieldBorrows) == 0 {
			delete(out, a.root)
			continue
		}
		out[a.root] = slices.Clone(a.fieldBorrows)
	}
	return out
}

// unionEdges returns the edges in a or b with no duplicate path/referent pair, the join at a
// CFG merge.
func unionEdges(a, b []fieldBorrow) []fieldBorrow {
	out := slices.Clone(a)
	for _, fb := range b {
		if !containsFieldBorrow(out, fb) {
			out = append(out, fb)
		}
	}
	return out
}

// containsFieldBorrow reports whether edges holds one with the same path and referent as e.
func containsFieldBorrow(fieldBorrows []fieldBorrow, fb fieldBorrow) bool {
	for _, x := range fieldBorrows {
		if x.referent == fb.referent && slices.Equal(x.path, fb.path) {
			return true
		}
	}
	return false
}

// borrowStateEqual reports whether two graphs hold the same edge set for every root, the
// fixed-point convergence test.
func borrowStateEqual(a, b map[liveness.VarID][]fieldBorrow) bool {
	if len(a) != len(b) {
		return false
	}
	for root, ae := range a {
		be, ok := b[root]
		if !ok || !fieldBorrowsEqual(ae, be) {
			return false
		}
	}
	return true
}

// fieldBorrowsEqual reports whether two edge slices hold the same path/referent pairs regardless of
// order.
func fieldBorrowsEqual(a, b []fieldBorrow) bool {
	if len(a) != len(b) {
		return false
	}
	for _, e := range a {
		if !containsFieldBorrow(b, e) {
			return false
		}
	}
	return true
}
