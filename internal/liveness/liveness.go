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
//
// A StmtIdx of -1 represents a synthetic position before the first
// statement in a block (used for decomposed DeclStmts whose init was
// a branching expression). "Live after" this position means "live
// before the first statement in the block". For example:
//
//	val c: {x: number} = if cond { a } else { b }
//	a.x = 5  // ← first statement in the join block
//	c
//
// The DeclStmt for c is decomposed by the CFG builder into separate
// branches. Its StmtRef points to the join block with StmtIdx -1,
// so alias tracking checks liveness just before `a.x = 5`.
func (l *LivenessInfo) IsLiveAfter(ref StmtRef, v VarID) bool {
	if ref.StmtIdx < 0 {
		// Synthetic position before the block's first statement.
		if len(l.LiveBefore[ref.BlockID]) > 0 {
			return l.LiveBefore[ref.BlockID][0].Contains(v)
		}
		// Empty block: use LiveAfter of the last statement's block entry.
		// An empty block has no liveness data; fall back to false.
		return false
	}
	return l.LiveAfter[ref.BlockID][ref.StmtIdx].Contains(v)
}
