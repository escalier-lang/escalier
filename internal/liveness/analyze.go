package liveness

import (
	"maps"

	"github.com/escalier-lang/escalier/internal/ast"
)

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
	n := len(stmts)

	// For Phase 3, everything is in a single block (blockID = 0).
	blockID := 0
	liveBefore := make([][]map[VarID]bool, 1)
	liveAfter := make([][]map[VarID]bool, 1)
	liveBefore[blockID] = make([]map[VarID]bool, n)
	liveAfter[blockID] = make([]map[VarID]bool, n)

	lastUse := make(map[VarID]StmtRef)

	if n == 0 {
		return &LivenessInfo{
			LiveBefore: liveBefore,
			LiveAfter:  liveAfter,
			LastUse:    lastUse,
		}
	}

	// Collect per-statement uses and defs.
	stmtUses := CollectUses(stmts)

	// Walk backward from the last statement.
	for i := n - 1; i >= 0; i-- {
		// LiveAfter for the last statement is empty; for others it's
		// LiveBefore of the next statement.
		if i == n-1 {
			liveAfter[blockID][i] = make(map[VarID]bool)
		} else {
			liveAfter[blockID][i] = maps.Clone(liveBefore[blockID][i+1])
		}

		// LiveBefore = (LiveAfter - Defs) ∪ Uses
		before := maps.Clone(liveAfter[blockID][i])

		// Remove definitions (kill set).
		for _, def := range stmtUses[i].Defs {
			delete(before, def)
		}

		// Add uses (gen set).
		for _, use := range stmtUses[i].Uses {
			before[use] = true
		}

		liveBefore[blockID][i] = before
	}

	// Compute LastUse: scan forward to find the last statement where each
	// variable appears in the use set.
	for i := range n {
		for _, use := range stmtUses[i].Uses {
			lastUse[use] = StmtRef{BlockID: blockID, StmtIdx: i}
		}
	}

	return &LivenessInfo{
		LiveBefore: liveBefore,
		LiveAfter:  liveAfter,
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
// After the fixed-point iteration converges, per-statement liveness is
// computed within each basic block using the block's LiveOut as the
// initial LiveAfter for the last statement (same backward walk as Phase 3).
func AnalyzeFunction(cfg *CFG) *LivenessInfo {
	numBlocks := len(cfg.Blocks)

	// Step 1: Compute per-block Uses (upward-exposed) and Defs, and
	// cache per-statement uses for the intra-block pass.
	blockUses := make([]map[VarID]bool, numBlocks)
	blockDefs := make([]map[VarID]bool, numBlocks)
	perStmtUses := make([][]StmtUses, numBlocks)

	for _, block := range cfg.Blocks {
		uses := make(map[VarID]bool)
		defs := make(map[VarID]bool)
		defined := make(map[VarID]bool)

		// ExtraDefs at the start of the block (e.g., loop variables,
		// if-let/match pattern bindings).
		for _, d := range block.ExtraDefs {
			defined[d] = true
			defs[d] = true
		}

		// Process statements in order.
		su := CollectUses(block.Stmts)
		perStmtUses[block.ID] = su

		for _, s := range su {
			for _, use := range s.Uses {
				if !defined[use] {
					uses[use] = true
				}
			}
			for _, def := range s.Defs {
				defined[def] = true
				defs[def] = true
			}
		}

		blockUses[block.ID] = uses
		blockDefs[block.ID] = defs
	}

	// Step 2: Initialize per-block LiveIn and LiveOut.
	blockLiveIn := make([]map[VarID]bool, numBlocks)
	blockLiveOut := make([]map[VarID]bool, numBlocks)
	for i := range numBlocks {
		blockLiveIn[i] = make(map[VarID]bool)
		blockLiveOut[i] = make(map[VarID]bool)
	}

	// Step 3: Fixed-point iteration. Process blocks in reverse order
	// (common heuristic for backward analysis).
	changed := true
	for changed {
		changed = false
		for i := numBlocks - 1; i >= 0; i-- {
			block := cfg.Blocks[i]

			// LiveOut[b] = ∪ LiveIn[s] for s in successors(b)
			newOut := make(map[VarID]bool)
			for _, succ := range block.Successors {
				for v := range blockLiveIn[succ.ID] {
					newOut[v] = true
				}
			}

			// LiveIn[b] = (LiveOut[b] - Defs[b]) ∪ Uses[b]
			newIn := make(map[VarID]bool)
			for v := range newOut {
				if !blockDefs[i][v] {
					newIn[v] = true
				}
			}
			for v := range blockUses[i] {
				newIn[v] = true
			}

			if !maps.Equal(blockLiveIn[i], newIn) || !maps.Equal(blockLiveOut[i], newOut) {
				changed = true
				blockLiveIn[i] = newIn
				blockLiveOut[i] = newOut
			}
		}
	}

	// Step 4: Compute per-statement liveness within each basic block.
	// Use the block's LiveOut as the initial LiveAfter for the last
	// statement, then walk backward (same algorithm as AnalyzeBlock).
	liveBefore := make([][]map[VarID]bool, numBlocks)
	liveAfter := make([][]map[VarID]bool, numBlocks)
	lastUse := make(map[VarID]StmtRef)

	for _, block := range cfg.Blocks {
		n := len(block.Stmts)
		liveBefore[block.ID] = make([]map[VarID]bool, n)
		liveAfter[block.ID] = make([]map[VarID]bool, n)

		if n == 0 {
			continue
		}

		su := perStmtUses[block.ID]

		// Walk backward from the last statement.
		for i := n - 1; i >= 0; i-- {
			var after map[VarID]bool
			if i == n-1 {
				after = maps.Clone(blockLiveOut[block.ID])
			} else {
				after = maps.Clone(liveBefore[block.ID][i+1])
			}
			liveAfter[block.ID][i] = after

			before := maps.Clone(after)
			for _, def := range su[i].Defs {
				delete(before, def)
			}
			for _, use := range su[i].Uses {
				before[use] = true
			}
			liveBefore[block.ID][i] = before
		}

		// Compute LastUse: scan forward within the block. With branching
		// control flow, a variable may be used in multiple branches; the
		// last block in iteration order wins. This is sufficient for
		// diagnostics but may need revisiting for drop/move placement.
		for i := range n {
			for _, use := range su[i].Uses {
				lastUse[use] = StmtRef{BlockID: block.ID, StmtIdx: i}
			}
		}
	}

	return &LivenessInfo{
		LiveBefore: liveBefore,
		LiveAfter:  liveAfter,
		LastUse:    lastUse,
	}
}


