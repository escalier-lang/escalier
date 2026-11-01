package liveness

import (
	"maps"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// MoveState is one binding's position in the consumed lattice — the per-binding
// move tracking the affine move engine joins at every CFG merge. A binding moves
// when an owned value flows out of it at a consume site (a return, a store, a
// consuming argument, an escaping capture). The three states answer "has this
// binding been moved on the paths that reach here":
//
//		            MaybeMoved
//		           /          \
//		     NotMoved          Moved
//
//	  - NotMoved: no reaching path moved it, so a use is allowed.
//	  - Moved: every reaching path moved it, so a use is an unconditional
//	    use-after-move.
//	  - MaybeMoved: some but not all reaching paths moved it, so a use is a
//	    conditional use-after-move.
//
// PR 6 reads these at each use, passing NotMoved and rejecting Moved and
// MaybeMoved. PR 5 only builds the analysis; it reports no user-facing errors.
type MoveState int

const (
	// NotMoved is the lattice bottom-left and the entry state for every binding.
	// A VarID never recorded as moved is NotMoved everywhere.
	NotMoved MoveState = iota
	// Moved means every reaching path moved the binding.
	Moved
	// MaybeMoved is the lattice top. It absorbs every join with a disagreeing
	// state, so once a binding is MaybeMoved it stays MaybeMoved downstream.
	MaybeMoved
)

func (s MoveState) String() string {
	switch s {
	case NotMoved:
		return "NotMoved"
	case Moved:
		return "Moved"
	case MaybeMoved:
		return "MaybeMoved"
	default:
		return "MoveState(?)"
	}
}

// JoinMoveState is the lattice join ⊔ applied at every CFG merge. Two edges that
// agree keep their shared state; two that disagree raise the result to
// MaybeMoved, which then absorbs everything. This is the table in the plan:
//
//	| ⊔          | NotMoved   | Moved      | MaybeMoved |
//	| NotMoved   | NotMoved   | MaybeMoved | MaybeMoved |
//	| Moved      | MaybeMoved | Moved      | MaybeMoved |
//	| MaybeMoved | MaybeMoved | MaybeMoved | MaybeMoved |
func JoinMoveState(a, b MoveState) MoveState {
	if a == b {
		return a
	}
	return MaybeMoved
}

// MoveInfo stores the consumed-lattice state computed for a function body, the
// forward-dataflow counterpart of LivenessInfo. State is indexed by basic block
// ID and statement index within the block. An absent VarID is NotMoved, so the
// maps store only the bindings that some path has moved.
type MoveInfo struct {
	// before[blockID][stmtIdx][v] is v's move state just before that statement,
	// joined across every path reaching the statement.
	before [][]map[VarID]MoveState
	// after[blockID][stmtIdx][v] is v's move state just after that statement —
	// before with the statement's own moves applied.
	after [][]map[VarID]MoveState
	// A DeclStmt whose initializer branches is decomposed during CFG construction,
	// so it occupies no real index in any block's statement list. BuildStmtToRef
	// instead assigns its StmtRef the index -1, a synthetic position meaning "block
	// entry, just before statement 0". A StateBefore or StateAfter call whose
	// ref.StmtIdx is -1 — a "-1 query" — asks for v's move state at that entry
	// position. blockIn and blockEntry hold the two answers such a query returns.
	//
	// blockIn[blockID][v] is v's state on entry to the block: the join of every
	// predecessor's blockOut, taken before any move recorded at the -1 position is
	// applied. StateBefore returns it for a -1 query.
	blockIn []map[VarID]MoveState
	// blockEntry[blockID][v] is blockIn after the moves recorded at the block's -1
	// position are applied, so it is the state just after that position. For a
	// non-empty block it equals before[blockID][0], the state before statement 0.
	// StateAfter returns it for a -1 query.
	blockEntry []map[VarID]MoveState
}

// AnalyzeMoves runs the forward move-state dataflow over a CFG. The moves map
// records, for each statement, the VarIDs that statement moves — the consume sites
// PR 6 collects while walking the body. A statement absent from the map moves nothing.
//
// The dataflow mirrors AnalyzeFunction's structure but runs FORWARD: liveness
// flows backward from uses, moves flow forward from consume sites.
//
//	MoveIn[b]  = ⊔ { MoveOut[p] | p ∈ predecessors(b) }
//	MoveOut[b] = MoveIn[b] with each statement of b applied in order
//
// A statement first resets each binding a `val` or `var` declaration in it declares to
// NotMoved, then sets each binding it moves to Moved. A declaration binds a fresh value, so a
// move of the binding on an earlier iteration of a loop does not reach the new one.
//
// MoveIn[entry] is empty (every binding NotMoved). The join at a merge block with
// disagreeing predecessors raises a binding to MaybeMoved. A loop back edge feeds
// the body's MoveOut into the header's MoveIn, so a binding moved inside the loop
// is MaybeMoved at the header — a use there is a conditional use-after-move. The
// fixed point converges because state only ever rises in the finite lattice.
//
// A StmtRef.StmtIdx of -1 is the synthetic position before a block's first
// statement. The moves map entries at index -1 are applied at block entry, before
// statement 0, matching how a decomposed DeclStmt's move would land.
func AnalyzeMoves(cfg *CFG, moves map[StmtRef]set.Set[VarID]) *MoveInfo {
	numBlocks := len(cfg.Blocks)

	declared := DeclaredBindings(cfg)

	blockIn := make([]map[VarID]MoveState, numBlocks)
	blockOut := make([]map[VarID]MoveState, numBlocks)
	for i := range numBlocks {
		blockIn[i] = map[VarID]MoveState{}
		blockOut[i] = map[VarID]MoveState{}
	}

	// Fixed-point iteration. State rises monotonically in the finite three-point
	// lattice, so this terminates. Forward block order is the heuristic for a
	// forward analysis, the mirror of AnalyzeFunction's reverse order for backward
	// liveness; back edges from loops are why more than one pass is needed.
	changed := true
	for changed {
		changed = false
		for i := range numBlocks {
			block := cfg.Blocks[i]

			newIn := joinPreds(block, blockOut)
			newOut := applyStmt(newIn, declared[StmtRef{BlockID: block.ID, StmtIdx: -1}], movesAt(moves, block.ID, -1))
			for idx := range block.Stmts {
				newOut = applyStmt(newOut, declared[StmtRef{BlockID: block.ID, StmtIdx: idx}], movesAt(moves, block.ID, idx))
			}

			if !equalMoveMap(blockIn[i], newIn) || !equalMoveMap(blockOut[i], newOut) {
				changed = true
				blockIn[i] = newIn
				blockOut[i] = newOut
			}
		}
	}

	// Per-statement state within each block: start from blockIn and walk forward,
	// applying each statement's moves in order. applyMoves returns a fresh map and
	// never mutates its input, so each map the cursor produces is immutable once
	// created. The slots can therefore store the reference directly — logically equal
	// points such as after[idx] and before[idx+1] share one map, which is safe because
	// nothing writes to these maps after AnalyzeMoves returns.
	before := make([][]map[VarID]MoveState, numBlocks)
	after := make([][]map[VarID]MoveState, numBlocks)
	blockEntry := make([]map[VarID]MoveState, numBlocks)
	for _, block := range cfg.Blocks {
		n := len(block.Stmts)
		before[block.ID] = make([]map[VarID]MoveState, n)
		after[block.ID] = make([]map[VarID]MoveState, n)

		// Apply any moves recorded at the synthetic -1 entry position first.
		state := applyStmt(blockIn[block.ID], declared[StmtRef{BlockID: block.ID, StmtIdx: -1}], movesAt(moves, block.ID, -1))
		blockEntry[block.ID] = state
		for idx := range n {
			before[block.ID][idx] = state
			state = applyStmt(state, declared[StmtRef{BlockID: block.ID, StmtIdx: idx}], movesAt(moves, block.ID, idx))
			after[block.ID][idx] = state
		}
	}

	return &MoveInfo{before: before, after: after, blockIn: blockIn, blockEntry: blockEntry}
}

// DeclaredBindings returns the point of each `val` or `var` declaration in cfg, mapped to the
// bindings it declares. A declaration whose initializer branches has no statement of its own,
// so its point is StmtIdx -1 of its join block, as BuildStmtToRef maps it. A block's ExtraDefs,
// such as a for-in loop's pattern bindings, are declared at StmtIdx -1 of that block too.
func DeclaredBindings(cfg *CFG) map[StmtRef][]VarID {
	out := map[StmtRef][]VarID{}
	add := func(stmt ast.Stmt, ref StmtRef) {
		decl, ok := stmt.(*ast.DeclStmt)
		if !ok {
			return
		}
		vd, ok := decl.Decl.(*ast.VarDecl)
		if !ok {
			return
		}
		c := &collector{ids: cfg.ids}
		c.collectPatDefs(vd.Pattern)
		if len(c.defs) > 0 {
			out[ref] = append(out[ref], c.defs...)
		}
	}
	for _, block := range cfg.Blocks {
		for idx, stmt := range block.Stmts {
			add(stmt, StmtRef{BlockID: block.ID, StmtIdx: idx})
		}
		if len(block.ExtraDefs) > 0 {
			entry := StmtRef{BlockID: block.ID, StmtIdx: -1}
			out[entry] = append(out[entry], block.ExtraDefs...)
		}
	}
	for _, ds := range cfg.decomposedStmts {
		add(ds.Stmt, StmtRef{BlockID: ds.Block.ID, StmtIdx: -1})
	}
	return out
}

// applyStmt returns a copy of state with each binding in declared reset to NotMoved, then
// each binding in moved set to Moved.
func applyStmt(state map[VarID]MoveState, declared []VarID, moved set.Set[VarID]) map[VarID]MoveState {
	if len(declared) == 0 {
		return applyMoves(state, moved)
	}
	out := cloneMoveMap(state)
	for _, v := range declared {
		delete(out, v)
	}
	return applyMoves(out, moved)
}

// joinPreds computes a block's entry state as the lattice join of every
// predecessor's exit state. A binding present in one predecessor's MoveOut but
// absent from another joins against NotMoved, which raises it to MaybeMoved — so
// the join must range over the union of all predecessor keys, not one at a time.
// A block with no predecessors yields the empty map, every binding NotMoved.
func joinPreds(block *BasicBlock, blockOut []map[VarID]MoveState) map[VarID]MoveState {
	in := map[VarID]MoveState{}
	keys := set.NewSet[VarID]()
	for _, pred := range block.Predecessors {
		for v := range blockOut[pred.ID] {
			keys.Add(v)
		}
	}
	for v := range keys {
		var joined MoveState
		for i, pred := range block.Predecessors {
			s := blockOut[pred.ID][v] // absent ⇒ NotMoved (the zero value)
			if i == 0 {
				joined = s
			} else {
				joined = JoinMoveState(joined, s)
			}
		}
		// Keep the map sparse: an absent key already means NotMoved, so never store it.
		if joined != NotMoved {
			in[v] = joined
		}
	}
	return in
}

// applyMoves returns a copy of state with each VarID in moved overridden to
// Moved. A move sets the binding to Moved regardless of its prior state, since a
// value cannot be un-moved; a double move along one path lands on Moved, the case
// PR 6 reads as an unconditional use-after-move.
func applyMoves(state map[VarID]MoveState, moved set.Set[VarID]) map[VarID]MoveState {
	out := cloneMoveMap(state)
	for v := range moved {
		out[v] = Moved
	}
	return out
}

// movesAt returns the VarIDs moved at one program point, or an empty set when the
// point moves nothing.
func movesAt(moves map[StmtRef]set.Set[VarID], blockID, stmtIdx int) set.Set[VarID] {
	if vs, ok := moves[StmtRef{BlockID: blockID, StmtIdx: stmtIdx}]; ok {
		return vs
	}
	return set.NewSet[VarID]()
}

func cloneMoveMap(m map[VarID]MoveState) map[VarID]MoveState {
	out := make(map[VarID]MoveState, len(m))
	maps.Copy(out, m)
	return out
}

// equalMoveMap reports whether two state maps assign the same MoveState to every
// VarID. A missing key is NotMoved, so a map carrying only NotMoved entries
// equals the empty map.
func equalMoveMap(a, b map[VarID]MoveState) bool {
	for k, va := range a {
		if vb, ok := b[k]; (!ok && va != NotMoved) || (ok && va != vb) {
			return false
		}
	}
	for k, vb := range b {
		if _, ok := a[k]; !ok && vb != NotMoved {
			return false
		}
	}
	return true
}

// StateBefore returns v's move state just before the statement at ref, joined
// across every path that reaches it. A StmtIdx of -1 reads the block's entry
// state, the position a decomposed DeclStmt's StmtRef points at. An out-of-range
// ref or an unrecorded binding is NotMoved.
func (m *MoveInfo) StateBefore(ref StmtRef, v VarID) MoveState {
	if ref.StmtIdx < 0 {
		return lookupMoveState(m.blockIn, ref.BlockID, v)
	}
	return lookupStmtState(m.before, ref, v)
}

// StateAfter returns v's move state just after the statement at ref — StateBefore
// with that statement's own moves applied. A StmtIdx of -1 reads block-entry
// semantics: the block's joined entry state with any synthetic -1 entry-position
// moves applied, the state just after that synthetic position. It does NOT equal
// StateBefore at -1 when an entry-position move is recorded, since StateBefore reads
// the entry state before that move.
func (m *MoveInfo) StateAfter(ref StmtRef, v VarID) MoveState {
	if ref.StmtIdx < 0 {
		return lookupMoveState(m.blockEntry, ref.BlockID, v)
	}
	return lookupStmtState(m.after, ref, v)
}

func lookupMoveState(blockMaps []map[VarID]MoveState, blockID int, v VarID) MoveState {
	if blockID < 0 || blockID >= len(blockMaps) {
		return NotMoved
	}
	return blockMaps[blockID][v]
}

func lookupStmtState(stmtMaps [][]map[VarID]MoveState, ref StmtRef, v VarID) MoveState {
	if ref.BlockID < 0 || ref.BlockID >= len(stmtMaps) {
		return NotMoved
	}
	block := stmtMaps[ref.BlockID]
	if ref.StmtIdx >= len(block) {
		return NotMoved
	}
	return block[ref.StmtIdx][v]
}
