package solver

import (
	"cmp"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// This file is the dataflow pass of the checking model in
// planning/ownership_rules/requirements.md. It walks each body's control-flow graph forward
// and checks the ownership events ownership_events.go records against E1, E2, and E3:
//
//   - E1 rejects a use of a place after a move out of it, until the place is reassigned.
//   - E2 rejects an immutable path and a mutable path to one place that are live together.
//   - E3 rejects a move of a place while a borrow of it is live.
//
// A loan is one borrow event the pass tracks: the place borrowed, its kind, and the holders
// that keep it live. A loan is live at a point while one of its holders is used again later,
// which the body's liveness answers. A copy of a holder becomes a second holder. A loan held
// by a module-level binding, or one whose lifetime is `'static`, is live at every later point.
// A loan held by a call argument, a receiver, a return, or a temporary value lasts until the
// end of the statement that creates it.
//
// The pass runs only when the ownershipDataflow flag is set. Its diagnostics then take the
// place of the ones replacedByDataflow lists, so the diagnostics of the two checkers can be
// compared program by program.

// ownershipDataflowEnv is the environment variable that turns the dataflow pass on for every
// run, whatever Options says. The value 1 turns it on.
const ownershipDataflowEnv = "ESCALIER_OWNERSHIP_DATAFLOW"

// ownershipDataflowFromEnv reports whether ownershipDataflowEnv turns the dataflow pass on.
func ownershipDataflowFromEnv() bool {
	return os.Getenv(ownershipDataflowEnv) == "1"
}

// replacedByDataflow reports whether err is a diagnostic of E1, E2, or E3 that the dataflow
// pass reports in place of the current checks when the ownershipDataflow flag is set. A
// diagnostic that names a closure a call runs is E4's, which the pass does not check, so it
// stays.
func replacedByDataflow(err SolverError) bool {
	switch e := err.(type) {
	case *UseAfterMoveError:
		return e.Via == ""
	case *BorrowAliasError:
		return e.FirstVia == "" && e.SecondVia == ""
	case *MoveWhileBorrowedError:
		return e.Via == ""
	case *BorrowedValueWriteError, *MutabilityTransitionError:
		return true
	}
	return false
}

// runOwnershipDataflow checks every event table in c.info that it has not checked yet, when
// the ownershipDataflow flag is set. It drops each diagnostic replacedByDataflow lists from
// c.errs and appends the pass's own. It must run after settleAllFlows, since the pass reads
// only settled events.
func (c *checker) runOwnershipDataflow() {
	if !c.ownershipDataflow {
		return
	}

	var tables []*ownershipEvents
	for _, t := range c.info.ownership {
		if !t.checked && t.cfg != nil && t.live != nil {
			tables = append(tables, t)
		}
	}

	// The map's order is random, so the bodies are checked in source order to keep the
	// diagnostics in a stable order.
	slices.SortFunc(tables, func(a, b *ownershipEvents) int {
		return compareSpans(tableSpan(a), tableSpan(b))
	})

	var found []SolverError
	for _, t := range tables {
		t.checked = true
		found = append(found, checkBodyDataflow(t)...)
	}

	c.errs = slices.DeleteFunc(c.errs, replacedByDataflow)
	c.errs = append(c.errs, found...)
}

// tableSpan returns the span of the first event of t, or the zero span when t has none.
func tableSpan(t *ownershipEvents) ast.Span {
	for _, ev := range t.events {
		if n := ev.base().node; n != nil {
			return n.Span()
		}
	}
	return ast.Span{}
}

// compareSpans orders two spans by source, then by start offset.
func compareSpans(a, b ast.Span) int {
	return cmp.Or(cmp.Compare(a.SourceID, b.SourceID), cmp.Compare(a.Start.Offset, b.Start.Offset))
}

// movedPlace is a place a move consumed on some path reaching a point.
type movedPlace struct {
	place movePlace
	// node is the expression that moved the place.
	node ast.Node
	// every is true when every path reaching the point moved the place.
	every bool
}

// liveLoan is one borrow the pass tracks.
type liveLoan struct {
	ev *borrowEvent
	// holders maps the root of each local place that holds the borrow or a copy of it to the
	// expression of the flow that made it a holder.
	holders map[liveness.VarID]ast.Node
	// static marks a loan that stays live at every later point.
	static bool
	// statement marks a loan that lasts only until the end of the statement that created it.
	statement bool
}

// closure reports whether the loan is a closure's capture. E4 makes a capture count as
// used only while the closure runs, so between calls it conflicts only with a move.
func (l *liveLoan) closure() bool {
	_, ok := l.ev.node.(ast.Func)
	return ok
}

// dataflowState is what the pass knows at one program point: the places moved on some path
// reaching it, keyed by placeKey, and the loans that may still be live, keyed by their
// borrow event.
type dataflowState struct {
	moved map[string]movedPlace
	loans map[*borrowEvent]*liveLoan
}

func newDataflowState() *dataflowState {
	return &dataflowState{moved: map[string]movedPlace{}, loans: map[*borrowEvent]*liveLoan{}}
}

// clone returns a deep copy of s.
func (s *dataflowState) clone() *dataflowState {
	out := newDataflowState()
	for k, m := range s.moved {
		out.moved[k] = m
	}

	for k, l := range s.loans {
		cp := *l
		cp.holders = maps.Clone(l.holders)
		out.loans[k] = &cp
	}

	return out
}

// equals reports whether s and o hold the same moved places and loans.
func (s *dataflowState) equals(o *dataflowState) bool {
	if len(s.moved) != len(o.moved) || len(s.loans) != len(o.loans) {
		return false
	}

	for k, m := range s.moved {
		om, ok := o.moved[k]
		if !ok || om.every != m.every {
			return false
		}
	}

	for k, l := range s.loans {
		ol, ok := o.loans[k]
		if !ok || ol.static != l.static || !maps.Equal(ol.holders, l.holders) {
			return false
		}
	}

	return true
}

// joinStates returns the state on entry to a block whose predecessors end in states. A place
// is moved when some predecessor moved it, and moved on every path only when every
// predecessor moved it on every path. A loan is live when it is live after some predecessor.
func joinStates(states []*dataflowState) *dataflowState {
	out := newDataflowState()

	for _, s := range states {
		for k, m := range s.moved {
			if prev, ok := out.moved[k]; ok {
				prev.every = prev.every && m.every
				out.moved[k] = prev
				continue
			}

			out.moved[k] = m
		}

		for k, l := range s.loans {
			if prev, ok := out.loans[k]; ok {
				for h, n := range l.holders {
					if pn, held := prev.holders[h]; !held || laterNode(n, pn) {
						prev.holders[h] = n
					}
				}
				prev.static = prev.static || l.static
				continue
			}

			cp := *l
			cp.holders = maps.Clone(l.holders)
			out.loans[k] = &cp
		}
	}

	// A place some predecessor did not move is not moved on every path.
	for k, m := range out.moved {
		for _, s := range states {
			if _, ok := s.moved[k]; !ok {
				m.every = false
				out.moved[k] = m
				break
			}
		}
	}

	return out
}

// laterNode reports whether a starts after b in the source. A node in a different source
// than b counts as later.
func laterNode(a, b ast.Node) bool {
	as, bs := a.Span(), b.Span()
	return as.SourceID != bs.SourceID || as.Start.Offset > bs.Start.Offset
}

// bodyDataflow is the dataflow pass over one body.
type bodyDataflow struct {
	table *ownershipEvents
	// events maps each CFG point to the events attached to it, in emission order.
	events map[liveness.StmtRef][]ownershipEvent
	// points lists the CFG points of each block that have events, in program order.
	points map[int][]liveness.StmtRef
	// order maps each borrow event of the body to its index in the table.
	order map[*borrowEvent]int
	// errs collects the diagnostics of the reporting sweep. It is nil while the pass is
	// still iterating to a fixed point, and nothing is reported then.
	errs *[]SolverError
}

// checkBodyDataflow runs the dataflow pass over the body t records and returns the
// diagnostics it finds. It iterates a worklist of blocks until the state on entry to each
// block stops changing, then walks each reachable block once more from its fixed-point
// entry state to report.
func checkBodyDataflow(t *ownershipEvents) []SolverError {
	d := &bodyDataflow{table: t, events: t.byNode(), points: map[int][]liveness.StmtRef{}, order: map[*borrowEvent]int{}}
	for i, ev := range t.events {
		if b, ok := ev.(*borrowEvent); ok {
			d.order[b] = i
		}
	}

	for at := range d.events {
		d.points[at.BlockID] = append(d.points[at.BlockID], at)
	}

	for _, refs := range d.points {
		slices.SortFunc(refs, func(a, b liveness.StmtRef) int { return cmp.Compare(a.StmtIdx, b.StmtIdx) })
	}

	blocks := t.cfg.Blocks
	in := make([]*dataflowState, len(blocks))
	out := make([]*dataflowState, len(blocks))

	worklist := []*liveness.BasicBlock{t.cfg.Entry}
	queued := set.FromSlice([]int{t.cfg.Entry.ID})

	for len(worklist) > 0 {
		b := worklist[0]
		worklist = worklist[1:]
		queued.Remove(b.ID)

		in[b.ID] = d.entryState(b, out)
		s := in[b.ID].clone()
		d.transfer(b.ID, s)

		if out[b.ID] != nil && out[b.ID].equals(s) {
			continue
		}

		out[b.ID] = s
		for _, succ := range b.Successors {
			if !queued.Contains(succ.ID) {
				queued.Add(succ.ID)
				worklist = append(worklist, succ)
			}
		}
	}

	var errs []SolverError
	d.errs = &errs

	for _, b := range blocks {
		if in[b.ID] != nil {
			d.transfer(b.ID, in[b.ID].clone())
		}
	}

	return errs
}

// entryState returns the state on entry to b, the join of the states its predecessors end
// in. A predecessor the worklist has not reached yet contributes nothing.
func (d *bodyDataflow) entryState(b *liveness.BasicBlock, out []*dataflowState) *dataflowState {
	var preds []*dataflowState
	for _, p := range b.Predecessors {
		if out[p.ID] != nil {
			preds = append(preds, out[p.ID])
		}
	}

	return joinStates(preds)
}

// transfer applies the events of block to s, point by point.
func (d *bodyDataflow) transfer(block int, s *dataflowState) {
	for _, at := range d.points[block] {
		for _, ev := range d.events[at] {
			d.step(ev, at, s)
		}

		d.endStatement(at, s)
	}
}

// endStatement drops each loan that cannot be live after the point at: a loan that lasts
// only for its statement, and a loan no holder of which is used again.
func (d *bodyDataflow) endStatement(at liveness.StmtRef, s *dataflowState) {
	for k, l := range s.loans {
		if l.statement || !d.loanLive(l, at, false) {
			delete(s.loans, k)
		}
	}
}

// step applies one event to s, reporting what it breaks when d is reporting.
func (d *bodyDataflow) step(ev ownershipEvent, at liveness.StmtRef, s *dataflowState) {
	switch ev := ev.(type) {
	case *useEvent:
		if ev.access == writeAccess {
			d.write(ev, at, s)
			return
		}

		d.checkMoved(ev.place, ev.node, s)
	case *borrowEvent:
		d.borrow(ev, at, s)
	case *copyEvent:
		d.checkMoved(ev.place, ev.node, s)
		d.addHolder(ev.place.root, ev.holder, ev.node, s)
	case *moveEvent:
		d.move(ev, at, s)
	case *provisionalEvent:
		// settleAllFlows replaces every provisional event before the pass runs.
	}
}

// write applies a write of ev.place. Writing a moved place or a place under it reassigns it,
// which E1 allows, and the place is no longer moved. Writing under a moved place is a use of
// it.
//
// A write replaces the value at ev.place, so only a borrow of a place that holds ev.place
// observes it. In `p.x = 1`, a borrow of `p` sees the change. In `p = {x: 1}`, a borrow of
// `p` still reaches the old value, which nothing writes. So E2 rejects a write while an
// immutable borrow of a place strictly above ev.place is used again after the write. A
// borrow of ev.place or of a place under it reaches a value the write detaches, and the
// pass stops tracking it.
//
// A write through a dynamic index, such as `xs[i] = v`, has the container `xs` as its place.
// It writes into the container rather than replacing it, so it counts as a write strictly
// under its place.
//
// Writing a whole local, as in `c = &q`, also ends what c held before. c stops holding each
// loan a flow written before the assignment gave it. The flows of the assigned value are
// written after the assignment's target, so c keeps the loans they give it, such as the
// borrow of q. A flow in a loop that runs after the assignment on an earlier iteration is
// written after it too, so c keeps that loan, which can only report more.
func (d *bodyDataflow) write(ev *useEvent, at liveness.StmtRef, s *dataflowState) {
	depth := len(ev.place.path)
	dynamic := false
	if ie, ok := ev.node.(*ast.IndexExpr); ok {
		if _, constant := constStringKey(ie.Index); !constant {
			depth++
			dynamic = true
		}
	}

	for _, k := range slices.Sorted(maps.Keys(s.moved)) {
		m := s.moved[k]
		if m.place.root != ev.place.root || !pathPrefixRelated(m.place.path, ev.place.path) {
			continue
		}

		if len(m.place.path) < depth {
			d.reportMoved(m, ev.place, ev.node)
			continue
		}

		// A dynamic index may name an element other than the moved one, so the write
		// reassigns no moved place under the container.
		if !dynamic {
			delete(s.moved, k)
		}
	}

	reported := false
	for _, l := range d.sortedLoans(s) {
		if l.closure() || !placesOverlap(l.ev.place, ev.place) {
			continue
		}

		if len(l.ev.place.path) >= depth {
			// A dynamic index may name an element the borrow does not reach.
			if !dynamic {
				delete(s.loans, l.ev)
			}
			continue
		}

		// A borrow made for one statement, such as a call argument, has ended by the time the
		// statement writes, since an assignment writes after its source is evaluated.
		if reported || l.statement || l.ev.borrow != immutableBorrow || !d.loanLive(l, at, false) {
			continue
		}

		d.report(&BorrowedValueWriteError{Place: d.placeName(ev.place), write: ev.node, borrow: l.ev.node.Span()})
		reported = true
	}

	if len(ev.place.path) == 0 && !dynamic {
		for _, l := range s.loans {
			if n, held := l.holders[ev.place.root]; held && !laterNode(n, ev.node) {
				delete(l.holders, ev.place.root)
			}
		}
	}
}

// borrow applies the borrow ev. E1 rejects a borrow of a moved place. E2 rejects a borrow
// while a borrow of the other kind of an overlapping place is live. A closure capture is
// checked against neither, since E4 counts a capture as used only while the closure runs.
func (d *bodyDataflow) borrow(ev *borrowEvent, at liveness.StmtRef, s *dataflowState) {
	d.checkMoved(ev.place, ev.node, s)

	l := &liveLoan{ev: ev, holders: map[liveness.VarID]ast.Node{}}
	if !l.closure() {
		for _, first := range d.sortedLoans(s) {
			if first.closure() || first.ev.borrow == ev.borrow || !placesOverlap(first.ev.place, ev.place) {
				continue
			}

			if d.loanLive(first, at, true) {
				d.report(&BorrowAliasError{
					Place:      d.placeName(ev.place),
					FirstPlace: d.placeName(first.ev.place),
					FirstMut:   first.ev.borrow == mutableBorrow,
					SecondMut:  ev.borrow == mutableBorrow,
					node:       ev.node,
					first:      first.ev.node.Span(),
				})
				break
			}
		}
	}

	l.static = ev.lifetime != nil && soltype.IsStaticLifetime(ev.lifetime)
	s.loans[ev] = l
	d.holdLoan(l, ev.holder, ev.node)

	// A borrow through a holder keeps the holder's loans live while the new borrow is.
	d.addHolder(ev.place.root, ev.holder, ev.node, s)
}

// move applies the move ev. E1 rejects a move of a moved place, and E3 rejects a move while
// a borrow of an overlapping place is live.
func (d *bodyDataflow) move(ev *moveEvent, at liveness.StmtRef, s *dataflowState) {
	if !d.checkMoved(ev.place, ev.node, s) {
		for _, l := range d.sortedLoans(s) {
			if placesOverlap(l.ev.place, ev.place) && d.loanLive(l, at, true) {
				d.report(&MoveWhileBorrowedError{Place: d.placeName(ev.place), move: ev.node, borrow: l.ev.node.Span()})
				break
			}
		}
	}

	s.moved[placeKey(ev.place)] = movedPlace{place: ev.place, node: ev.node, every: true}

	// The holder now owns what the place held, including any borrow stored in it.
	d.addHolder(ev.place.root, ev.holder, ev.node, s)
}

// holdLoan records holder as a holder of l, through the flow whose expression is node.
func (d *bodyDataflow) holdLoan(l *liveLoan, holder eventHolder, node ast.Node) {
	switch tg := holder.target.(type) {
	case placeTarget:
		l.holders[tg.root] = node
	case moduleTarget:
		l.static = true
	case argTarget, receiverTarget, returnTarget, tempTarget:
		if len(l.holders) == 0 && !l.static {
			l.statement = true
		}
	}
}

// addHolder makes holder a holder of each loan root holds, since holder now reaches what
// root borrows. node is the expression of the flow.
func (d *bodyDataflow) addHolder(root liveness.VarID, holder eventHolder, node ast.Node, s *dataflowState) {
	for _, l := range s.loans {
		if _, held := l.holders[root]; held {
			d.holdLoan(l, holder, node)
		}
	}
}

// checkMoved reports a use of place at node when a place overlapping it is moved, and
// returns whether it reported.
func (d *bodyDataflow) checkMoved(place movePlace, node ast.Node, s *dataflowState) bool {
	for _, k := range slices.Sorted(maps.Keys(s.moved)) {
		m := s.moved[k]
		if m.place.root == place.root && pathPrefixRelated(m.place.path, place.path) {
			d.reportMoved(m, place, node)
			return true
		}
	}
	return false
}

// reportMoved reports the use of place at node after the move m. A use of a place that
// holds the moved place, such as `pair` after `pair.a` moved, is a use of a partially
// moved value.
func (d *bodyDataflow) reportMoved(m movedPlace, place movePlace, node ast.Node) {
	e := &UseAfterMoveError{Name: d.placeName(m.place), Conditional: !m.every, use: node, moveSite: m.node}
	if len(place.path) < len(m.place.path) {
		e.ReadName = d.placeName(place)
		e.Partial = true
	}
	d.report(e)
}

// loanLive reports whether l is live at the point at. before asks whether it is live on
// entry to the statement, which counts a holder the statement itself reads. Otherwise it
// asks whether it is live after the statement.
func (d *bodyDataflow) loanLive(l *liveLoan, at liveness.StmtRef, before bool) bool {
	if l.static || l.statement {
		return true
	}

	for h := range l.holders {
		if d.holderLive(h, at, before) {
			return true
		}
	}
	return false
}

// holderLive reports whether the local h is used again after the point at, or at it when
// before is set. A point outside the liveness tables is not live.
func (d *bodyDataflow) holderLive(h liveness.VarID, at liveness.StmtRef, before bool) bool {
	live := d.table.live
	if at.BlockID < 0 || at.BlockID >= len(live.LiveBefore) || at.StmtIdx >= len(live.LiveBefore[at.BlockID]) {
		return false
	}

	switch {
	case at.StmtIdx < 0:
		// The point before a block's first statement is live where the block's entry is.
		if d.liveOnEntry(at.BlockID, h, set.NewSet[int]()) {
			return true
		}
	case before:
		return live.IsLiveBefore(at, h)
	case live.IsLiveAfter(at, h):
		return true
	}

	// A branch of `c = if cond { &a } else { &b }` ends in a block of its own, and the join
	// block after it defines c. Liveness counts c as dead at the end of the branch, but the
	// branch's value flows into c, so c holds it while c is live after the join defines it.
	if at.StmtIdx != len(live.LiveBefore[at.BlockID])-1 {
		return false
	}

	for _, succ := range d.table.cfg.Blocks[at.BlockID].Successors {
		if slices.Contains(succ.ExtraDefs, h) && d.liveOnEntry(succ.ID, h, set.NewSet[int]()) {
			return true
		}
	}
	return false
}

// liveOnEntry reports whether the local h is live on entry to the block with ID block. A
// block with no statements has no liveness of its own, so h is live on entry to it when it
// is live on entry to one of its successors. seen holds the blocks already asked.
func (d *bodyDataflow) liveOnEntry(block int, h liveness.VarID, seen set.Set[int]) bool {
	if seen.Contains(block) {
		return false
	}
	seen.Add(block)

	if before := d.table.live.LiveBefore[block]; len(before) > 0 {
		return before[0].Contains(h)
	}

	for _, succ := range d.table.cfg.Blocks[block].Successors {
		if d.liveOnEntry(succ.ID, h, seen) {
			return true
		}
	}
	return false
}

// report records e when d is reporting.
func (d *bodyDataflow) report(e SolverError) {
	if d.errs != nil {
		*d.errs = append(*d.errs, e)
	}
}

// placeName renders p for a diagnostic as the root's name followed by its field path, the
// way the checker's renderPlace does.
func (d *bodyDataflow) placeName(p movePlace) string {
	var b strings.Builder
	if name, ok := d.table.names[p.root]; ok {
		b.WriteString(name)
	} else {
		fmt.Fprintf(&b, "var#%d", p.root)
	}

	for _, seg := range p.path {
		writePlaceSeg(&b, seg)
	}
	return b.String()
}

// sortedLoans returns the loans of s in the order their borrow events were emitted, so a
// report names the same loan on every run.
func (d *bodyDataflow) sortedLoans(s *dataflowState) []*liveLoan {
	out := make([]*liveLoan, 0, len(s.loans))
	for _, l := range s.loans {
		out = append(out, l)
	}

	slices.SortFunc(out, func(a, b *liveLoan) int { return cmp.Compare(d.order[a.ev], d.order[b.ev]) })
	return out
}
