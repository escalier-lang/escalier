package liveness

import "github.com/escalier-lang/escalier/internal/set"

// VarID uniquely identifies a variable within a function body.
// Sequential integer IDs are assigned during name resolution (Phase 2)
// and stored directly on AST nodes (IdentExpr.VarID, IdentPat.VarID,
// etc.). The rename pass sets these; liveness and alias analysis read
// them from the AST nodes.
//
// A VarID of 0 indicates an unresolved or unset ID. Local variable IDs
// start at 1. Non-local variables (module-level, prelude) are assigned
// IDs starting at -1 and counting down, so downstream phases can
// distinguish them: any VarID < 0 is non-local and should be ignored
// by liveness and alias analysis.
type VarID int

// StmtRef identifies a statement by its position in the CFG: the basic
// block it belongs to and its index within that block.
type StmtRef struct {
	BlockID int // index into CFG.Blocks
	StmtIdx int // index within BasicBlock.Stmts
}

// LivenessInfo stores the results of liveness analysis for a function body.
// Liveness sets are indexed by basic block ID and statement index within
// the block, avoiding the need to use AST spans as map keys.
type LivenessInfo struct {
	// LiveBefore[blockID][stmtIdx] is the set of variables that are live
	// just before the statement at that position.
	LiveBefore [][]set.Set[VarID]

	// LiveAfter[blockID][stmtIdx] is the set of variables that are live
	// just after the statement at that position.
	LiveAfter [][]set.Set[VarID]

	// LastUse maps each variable to the location of its last use.
	LastUse map[VarID]StmtRef
}

// IsLiveAfter returns whether the given variable is live after the
// statement at the given position.
func (l *LivenessInfo) IsLiveAfter(ref StmtRef, v VarID) bool {
	return l.LiveAfter[ref.BlockID][ref.StmtIdx].Contains(v)
}
