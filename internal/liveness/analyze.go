package liveness

import (
	"maps"

	"github.com/escalier-lang/escalier/internal/ast"
)

// AnalyzeBlock computes liveness for a linear block of statements.
// This is the foundation — Phase 4 extends it to handle control flow
// via a CFG.
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

