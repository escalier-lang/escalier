package liveness

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// analyzeStmts walks a sequence of statements backward, computing per-statement
// LiveBefore and LiveAfter sets. initialLiveAfter is the set of variables live
// after the last statement (empty for a standalone block, or the block's LiveOut
// from fixed-point iteration for CFG-based analysis).
func analyzeStmts(stmtUses []StmtUses, initialLiveAfter set.Set[VarID]) ([]set.Set[VarID], []set.Set[VarID]) {
	n := len(stmtUses)
	liveBefore := make([]set.Set[VarID], n)
	liveAfter := make([]set.Set[VarID], n)

	if n == 0 {
		return liveBefore, liveAfter
	}

	for i := n - 1; i >= 0; i-- {
		var after set.Set[VarID]
		if i == n-1 {
			after = initialLiveAfter.Clone()
		} else {
			after = liveBefore[i+1].Clone()
		}
		liveAfter[i] = after

		// LiveBefore = (LiveAfter - Defs) ∪ Uses
		before := after.Clone()
		for _, def := range stmtUses[i].Defs {
			before.Remove(def)
		}
		for _, use := range stmtUses[i].Uses {
			before.Add(use)
		}
		liveBefore[i] = before
	}

	return liveBefore, liveAfter
}

// computeLastUse scans forward through per-statement uses, recording the last
// statement where each variable is used. Results are merged into lastUse; for
// CFG-based analysis, later blocks in iteration order overwrite earlier ones.
func computeLastUse(stmtUses []StmtUses, blockID int, lastUse map[VarID]StmtRef) {
	for i, su := range stmtUses {
		for _, use := range su.Uses {
			lastUse[use] = StmtRef{BlockID: blockID, StmtIdx: i}
		}
	}
}

// AnalyzeBlock computes liveness for a linear block of statements.
// For control-flow-aware analysis, use AnalyzeFunction with a CFG instead.
//
// The algorithm walks backward from the last statement:
//  1. Start with LiveAfter = {} for the last statement
//  2. For each statement, working backward:
//     LiveBefore[stmt] = (LiveAfter[stmt] - Defs[stmt]) ∪ Uses[stmt]
//     LiveAfter[prev] = LiveBefore[stmt]
//  3. A variable is "dead" at a point if it is not in LiveBefore or LiveAfter
//
// VarIDs are read directly from AST nodes (set by the rename pass in Phase 2).
func AnalyzeBlock(stmts []ast.Stmt) *LivenessInfo {
	// For Phase 3, everything is in a single block (blockID = 0).
	blockID := 0
	lastUse := make(map[VarID]StmtRef)

	// Collect per-statement uses and defs.
	stmtUses := CollectUses(stmts)

	before, after := analyzeStmts(stmtUses, set.NewSet[VarID]())

	computeLastUse(stmtUses, blockID, lastUse)

	return &LivenessInfo{
		LiveBefore: [][]set.Set[VarID]{before},
		LiveAfter:  [][]set.Set[VarID]{after},
		LastUse:    lastUse,
	}
}

// AnalyzeFunction computes liveness for a full function body with control
// flow. It uses the CFG from BuildCFG and performs standard backward
// dataflow analysis:
//
//	LiveOut[b] = ∪ { LiveIn[s] | s ∈ successors(b) }
//	LiveIn[b]  = (LiveOut[b] - Defs[b]) ∪ Uses[b]
//
// LiveOut[b] is the set of variables that are live at the exit of block b.
// A variable is live at b's exit if it is live at the entry of any successor
// block, so LiveOut is the union of all successors' LiveIn sets.
//
// LiveIn[b] is the set of variables that are live at the entry of block b.
// Starting from what's live at the exit (LiveOut), we remove variables that
// are defined in b (Defs) — they get a new value so the old one isn't needed
// — then add variables that are read in b before any definition (Uses, also
// called "upward-exposed uses").
//
// After the fixed-point iteration converges, per-statement liveness is
// computed within each basic block using the block's LiveOut as the
// initial LiveAfter for the last statement (same backward walk as
// analyzeStmts).
func AnalyzeFunction(cfg *CFG) *LivenessInfo {
	numBlocks := len(cfg.Blocks)

	// Step 1: Compute per-block Uses (upward-exposed) and Defs, and
	// cache per-statement uses for the intra-block pass. Block IDs are
	// sequential indices into cfg.Blocks (assigned by newBlock), so they
	// can be used directly to index into these slices.
	blockUses := make([]set.Set[VarID], numBlocks)
	blockDefs := make([]set.Set[VarID], numBlocks)
	perStmtUses := make([][]StmtUses, numBlocks)

	for _, block := range cfg.Blocks {
		uses := set.NewSet[VarID]()
		defs := set.NewSet[VarID]()
		defined := set.NewSet[VarID]()

		// ExtraDefs at the start of the block (e.g., loop variables,
		// if-let/match pattern bindings).
		for _, d := range block.ExtraDefs {
			defined.Add(d)
			defs.Add(d)
		}

		// Process statements in order.
		su := CollectUses(block.Stmts)
		perStmtUses[block.ID] = su

		for _, s := range su {
			for _, use := range s.Uses {
				if !defined.Contains(use) {
					uses.Add(use)
				}
			}
			for _, def := range s.Defs {
				defined.Add(def)
				defs.Add(def)
			}
		}

		blockUses[block.ID] = uses
		blockDefs[block.ID] = defs
	}

	// Step 2: Initialize per-block LiveIn and LiveOut.
	blockLiveIn := make([]set.Set[VarID], numBlocks)
	blockLiveOut := make([]set.Set[VarID], numBlocks)
	for i := range numBlocks {
		blockLiveIn[i] = set.NewSet[VarID]()
		blockLiveOut[i] = set.NewSet[VarID]()
	}

	// Step 3: Fixed-point iteration. Process blocks in reverse order
	// (common heuristic for backward analysis). Convergence is guaranteed
	// because LiveIn/LiveOut sets can only grow (the union and set-difference
	// operations are monotone over the finite lattice of VarID power sets).
	// In practice this converges in 2–3 passes; loops with back edges are the
	// main reason more than one pass is needed.
	changed := true
	for changed {
		changed = false
		for i := numBlocks - 1; i >= 0; i-- {
			block := cfg.Blocks[i]

			// LiveOut[b] = ∪ LiveIn[s] for s in successors(b)
			newOut := set.NewSet[VarID]()
			for _, succ := range block.Successors {
				for v := range blockLiveIn[succ.ID] {
					newOut.Add(v)
				}
			}

			// LiveIn[b] = (LiveOut[b] - Defs[b]) ∪ Uses[b]
			newIn := newOut.Difference(blockDefs[i]).Union(blockUses[i])

			if !blockLiveIn[i].Equals(newIn) || !blockLiveOut[i].Equals(newOut) {
				changed = true
				blockLiveIn[i] = newIn
				blockLiveOut[i] = newOut
			}
		}
	}

	// Step 4: Compute per-statement liveness within each basic block.
	// Use the block's LiveOut as the initial LiveAfter for the last
	// statement, then walk backward (same algorithm as AnalyzeBlock).
	liveBefore := make([][]set.Set[VarID], numBlocks)
	liveAfter := make([][]set.Set[VarID], numBlocks)
	lastUse := make(map[VarID]StmtRef)

	for _, block := range cfg.Blocks {
		liveBefore[block.ID], liveAfter[block.ID] = analyzeStmts(
			perStmtUses[block.ID], blockLiveOut[block.ID],
		)

		// Compute LastUse: with branching control flow, a variable may be
		// used in multiple branches; the last block in iteration order wins.
		// This is sufficient for diagnostics but may need revisiting for
		// drop/move placement.
		computeLastUse(perStmtUses[block.ID], block.ID, lastUse)
	}

	return &LivenessInfo{
		LiveBefore: liveBefore,
		LiveAfter:  liveAfter,
		LastUse:    lastUse,
	}
}
