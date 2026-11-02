package solver

import (
	"cmp"
	"fmt"
	"maps"
	"math"
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
// A holder is the destination that receives a borrow, such as the `c` of `val c = &p`. A
// loan is one borrow event the pass tracks: the place borrowed, its kind, and the holders
// that keep it live. A loan is live at a point while one of its holders is used again later,
// which the body's liveness answers. A copy of a holder becomes a second holder. A copy into a
// destination of the other kind, or into a `'static` one, derives a loan of its own instead, as
// copyBorrow describes. A loan held by a module-level binding, or one whose lifetime is
// `'static`, is live at every later point. A borrow takes its kind and its `'static` lifetime
// from its destination, so `track(&p)` with `track(v: &'static {x: number})` is `'static`.
// A loan held by a call argument or a receiver lasts until the call returns. Before then, a
// passEvent can give it to the store target or the result that the callee's signature ties
// the argument to. A
// loan held by a return or a temporary value lasts until the end of the statement that
// creates it. A holder is a place, so a write to `p.r` ends what `p.r` held and leaves what
// `p.s` holds. A place under a parameter whose referent the caller owns is read by the
// caller once the body returns or suspends, so its loans count at those points too.
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
// diagnostic about a closure's captures during a call is E4's, which the pass does not
// check, so it stays. That covers one that names a closure a call runs, a second call of a
// closure that moves a capture, and a conflict with a closure's capture.
func replacedByDataflow(err SolverError) bool {
	switch e := err.(type) {
	case *UseAfterMoveError:
		return e.Via == "" && !e.closureCall
	case *BorrowAliasError:
		return e.FirstVia == "" && e.SecondVia == "" && !e.closure
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
		found = append(found, c.checkBodyDataflow(t)...)
	}

	c.errs = slices.DeleteFunc(c.errs, replacedByDataflow)
	// E4's `moved` example puts the error on the move, so a move the pass rejects is
	// reported once, there, and not again at a later call of the closure.
	c.errs = slices.DeleteFunc(c.errs, closureMoveReported(found))
	// The current checks name the binding that still holds the other borrow when a store
	// breaks E2, which says more than the pass's message about the same store. This runs
	// before the next filter, so a dropped error does not stand in for a frame-exit one.
	found = slices.DeleteFunc(found, storedAliasReported(c.errs))
	// L1 and E2 at the return replace the frame-exit check of a value's paths out of the
	// frame. A pair of paths the pass reports by E2 is reported once, by the pass. The
	// frame-exit check keeps each pair the pass does not report.
	c.errs = slices.DeleteFunc(c.errs, sharedPathsReported(found))
	// A closure's body is checked after the body it is written in, so its diagnostics are put
	// back in source order before the merge.
	slices.SortStableFunc(found, func(a, b SolverError) int { return compareSpans(a.Span(), b.Span()) })
	c.errs = mergeDataflowErrors(c.errs, found)
}

// mergeDataflowErrors returns errs with each diagnostic of found inserted before the first
// ownership diagnostic of the current checks that starts later in the source, as
// keptOwnershipDiagnostic names them. A diagnostic of found that no such diagnostic follows goes
// at the end. The current checks report these diagnostics in source order, so the pass's report
// takes the place among them that the current checks' report of the same rule had. In
//
//	take(x)
//	val f = fn () { readRead(&x, &x) }
//	f()
//
// the use of the moved x in the closure comes before the call of f with the pass on or off.
func mergeDataflowErrors(errs, found []SolverError) []SolverError {
	out := make([]SolverError, 0, len(errs)+len(found))
	next := 0
	for _, err := range errs {
		if keptOwnershipDiagnostic(err) {
			for next < len(found) && compareSpans(found[next].Span(), err.Span()) < 0 {
				out = append(out, found[next])
				next++
			}
		}
		out = append(out, err)
	}
	return append(out, found[next:]...)
}

// keptOwnershipDiagnostic reports whether err is an ownership diagnostic of the current checks
// that stays with the dataflow pass on. That is an E4 diagnostic, which names a closure a call
// runs, or a frame-exit diagnostic.
func keptOwnershipDiagnostic(err SolverError) bool {
	switch err.(type) {
	case *UseAfterMoveError, *BorrowAliasError, *MoveWhileBorrowedError:
		return !replacedByDataflow(err)
	case *MixedOutflowPathsError, *SharedReturnPathsError, *EscapingBorrowError, *StoredBorrowAliasError:
		return true
	}
	return false
}

// storedAliasReported returns a filter that matches a BorrowAliasError of the dataflow pass
// at the span of a StoredBorrowAliasError in errs.
func storedAliasReported(errs []SolverError) func(SolverError) bool {
	spans := set.NewSet[ast.Span]()
	for _, err := range errs {
		if e, ok := err.(*StoredBorrowAliasError); ok {
			spans.Add(e.Span())
		}
	}

	return func(err SolverError) bool {
		_, isAlias := err.(*BorrowAliasError)
		return isAlias && spans.Contains(err.Span())
	}
}

// sharedPathsReported returns a filter that matches a frame-exit diagnostic of the current
// checks when the dataflow pass reports the same conflict. found holds the pass's diagnostics.
// The filter matches a SharedReturnPathsError, or a MixedOutflowPathsError that is not Owned,
// when found holds a BorrowAliasError about the same local whose two places overlap the
// frame-exit diagnostic's two places, one each. The BorrowAliasError's span, or the span of a
// return its paths flow into, must lie inside the frame-exit diagnostic's span or its related
// span.
func sharedPathsReported(found []SolverError) func(SolverError) bool {
	reports := map[liveness.VarID][]*BorrowAliasError{}
	for _, err := range found {
		if e, ok := err.(*BorrowAliasError); ok && e.root != 0 {
			reports[e.root] = append(reports[e.root], e)
		}
	}
	if len(reports) == 0 {
		return func(SolverError) bool { return false }
	}

	return func(err SolverError) bool {
		var local liveness.VarID
		var places [2]movePlace
		switch e := err.(type) {
		case *SharedReturnPathsError:
			local, places = e.local, e.places
		case *MixedOutflowPathsError:
			if e.Owned {
				return false
			}
			local, places = e.local, e.places
		default:
			return false
		}

		for _, r := range reports[local] {
			// A conflict on another field of the same local, such as `b.x` beside a frame-exit
			// pair on `b.y`, is a separate conflict, so it does not match.
			if !pairsOverlap(r.places, places) {
				continue
			}

			for _, span := range append([]ast.Span{r.Span()}, r.exits...) {
				if err.Span().ContainsSpan(span) || slices.ContainsFunc(err.Related(), func(s ast.Span) bool { return s.ContainsSpan(span) }) {
					return true
				}
			}
		}
		return false
	}
}

// pairsOverlap reports whether each place of a overlaps a different place of b.
func pairsOverlap(a, b [2]movePlace) bool {
	return (placesOverlap(a[0], b[0]) && placesOverlap(a[1], b[1])) ||
		(placesOverlap(a[0], b[1]) && placesOverlap(a[1], b[0]))
}

// closureMoveReported returns a filter that matches each E4 diagnostic of the current checks
// about a capture whose move the dataflow pass rejects by E3 against a closure's borrow.
// found holds the pass's diagnostics. The filter matches a call that runs a closure after
// such a move, and such a move in a call that may run a closure.
func closureMoveReported(found []SolverError) func(SolverError) bool {
	moves := set.NewSet[ast.Node]()
	for _, err := range found {
		if e, ok := err.(*MoveWhileBorrowedError); ok && e.closure {
			moves.Add(e.move)
		}
	}

	return func(err SolverError) bool {
		switch e := err.(type) {
		case *UseAfterMoveError:
			return e.Via != "" && e.moveSite != nil && moves.Contains(e.moveSite)
		case *MoveWhileBorrowedError:
			return e.Via != "" && moves.Contains(e.move)
		}
		return false
	}
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

// heldPlace is one local place that holds a loan, and the expression of the flow that made
// the place a holder.
type heldPlace struct {
	place movePlace
	node  ast.Node
}

// liveLoan is one borrow the pass tracks.
type liveLoan struct {
	// ev is the borrow event that took the loan. A loan a copy derives has an event of its
	// own, which derivedLoan makes.
	ev *borrowEvent
	// holders maps the placeKey of each local place that holds the borrow or a copy of it to
	// that place. `out.slot = a.peer` makes `out.slot` a holder of each loan `a.peer` holds,
	// and a later write to `out.slot` ends what that holder held.
	holders map[string]heldPlace
	// static marks a loan that stays live at every later point.
	static bool
	// slots holds each argument or receiver of a call that holds the loan. The callEvent of
	// that call releases the slot.
	slots set.Set[callSlot]
	// scoped marks a loan that a return or a temporary value holds. It lasts until the end of
	// the statement. returned marks one a return holds, which the caller receives.
	scoped   bool
	returned bool
	// reserved is the method call whose `&mut self` receiver took the loan, until that call
	// starts. Evaluating the call's arguments only reads the receiver, so until then the loan
	// conflicts with no immutable path. callEnd checks the immutable paths still live when the
	// call starts. That lets `v.push(v.len())` read v while the outer call is pending.
	reserved *ast.CallExpr
	// alias marks a loan that records only that its holders reach ev.place through a copy of
	// a borrow no loan of the body covers, such as a copy of a parameter. It creates no new
	// path, so no rule checks against it. A later copy of one of its holders into a `'static`
	// destination or a destination of another kind derives a loan from it.
	alias bool
	// through marks a loan of a path through the borrow ev.place holds to what that borrow
	// reaches, which a copy of the borrow derives. Any other loan is of the storage at ev.place.
	through bool
}

// checked reports whether the rules check other operations against the loan. An alias
// loan creates no path, and E4 makes a closure's capture count as used only while the
// closure runs, so between calls it conflicts only with a move.
func (l *liveLoan) checked() bool {
	return !l.alias && !l.closure()
}

// closure reports whether the loan is a closure's capture. E4 makes a capture count as
// used only while the closure runs, so between calls it conflicts only with a move.
func (l *liveLoan) closure() bool {
	return isFuncNode(l.ev.node)
}

// heldAt reports whether a holder of l is a place that overlaps p, so that a read of p
// reaches what l borrows.
func (l *liveLoan) heldAt(p movePlace) bool {
	for _, h := range l.holders {
		if placesOverlap(h.place, p) {
			return true
		}
	}
	return false
}

// newLiveLoan returns a loan of ev that nothing holds yet.
func newLiveLoan(ev *borrowEvent) *liveLoan {
	return &liveLoan{ev: ev, holders: map[string]heldPlace{}, slots: set.NewSet[callSlot]()}
}

// clone returns a copy of l that shares no map with it.
func (l *liveLoan) clone() *liveLoan {
	cp := *l
	cp.holders = maps.Clone(l.holders)
	cp.slots = l.slots.Clone()
	return &cp
}

// sameHolders reports whether l and o have the same holders, each made by the same flow.
func (l *liveLoan) sameHolders(o *liveLoan) bool {
	return maps.EqualFunc(l.holders, o.holders, func(a, b heldPlace) bool { return a.node == b.node })
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
		out.loans[k] = l.clone()
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
		if !ok || ol.static != l.static || ol.alias != l.alias || !ol.sameHolders(l) {
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
				for k, h := range l.holders {
					if ph, held := prev.holders[k]; !held || laterNode(h.node, ph.node) {
						prev.holders[k] = h
					}
				}
				prev.static = prev.static || l.static
				continue
			}

			out.loans[k] = l.clone()
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
	// order maps each borrow event of the body, and each borrow derivedLoan makes, to its
	// position in the order loans are reported in. A derived borrow sorts after the borrows
	// the table emits before its copy.
	order map[*borrowEvent]loanOrder
	// copyIndex maps each copy event of the body to its index in the table.
	copyIndex map[*copyEvent]int
	// derived holds the borrow event of each loan a copy derives, so each fixed-point
	// iteration derives the same loan.
	derived map[derivedKey]*borrowEvent
	// errs collects the diagnostics of the reporting sweep. It is nil while the pass is
	// still iterating to a fixed point, and nothing is reported then.
	errs *[]SolverError
	// blamed holds the span of each diagnostic in errs. One expression can emit several
	// events, such as the move of each field `val {f, g} = p` takes, and the expression is
	// reported once.
	blamed set.Set[ast.Span]
	// indexSegment is the checker's indexSegment, which decides whether an index adds a
	// segment to a place the way exprPlace decides it.
	indexSegment func(ast.Expr) (string, bool)
	// suspends holds each CFG point whose statement yields or awaits, where the caller runs
	// and can read what the body stored into a caller-owned place.
	suspends set.Set[liveness.StmtRef]
	// declares maps each CFG point whose statement is a `val` or `var` declaration to the
	// bindings it declares.
	declares map[liveness.StmtRef][]liveness.VarID
	// reaches caches the answers of placeReaches.
	reaches map[reachKey]bool
	// escapes caches the answers of rootEscapes.
	escapes map[liveness.VarID]bool
}

// checkBodyDataflow runs the dataflow pass over the body t records and returns the
// diagnostics it finds. It iterates a worklist of blocks until the state on entry to each
// block stops changing, then walks each reachable block once more from its fixed-point
// entry state to report.
func (c *checker) checkBodyDataflow(t *ownershipEvents) []SolverError {
	d := &bodyDataflow{
		table:        t,
		events:       t.byNode(),
		points:       map[int][]liveness.StmtRef{},
		order:        map[*borrowEvent]loanOrder{},
		copyIndex:    map[*copyEvent]int{},
		derived:      map[derivedKey]*borrowEvent{},
		indexSegment: c.indexSegment,
		suspends:     suspendPoints(t.cfg),
		declares:     liveness.DeclaredBindings(t.cfg),
		reaches:      map[reachKey]bool{},
		escapes:      map[liveness.VarID]bool{},
	}
	for i, ev := range t.events {
		switch ev := ev.(type) {
		case *borrowEvent:
			d.order[ev] = loanOrder{event: i}
		case *copyEvent:
			d.copyIndex[ev] = i
		}
	}

	for at := range d.events {
		d.points[at.BlockID] = append(d.points[at.BlockID], at)
	}
	for at := range d.suspends {
		if _, ok := d.events[at]; !ok {
			d.points[at.BlockID] = append(d.points[at.BlockID], at)
		}
	}
	for at := range d.declares {
		if _, ok := d.events[at]; !ok && !d.suspends.Contains(at) {
			d.points[at.BlockID] = append(d.points[at.BlockID], at)
		}
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
	d.blamed = set.NewSet[ast.Span]()

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
		// A declaration binds a fresh value, so a move of the binding on an earlier iteration
		// of a loop does not reach it.
		for _, root := range d.declares[at] {
			for k, m := range s.moved {
				if m.place.root == root {
					delete(s.moved, k)
				}
			}
		}

		for _, ev := range d.events[at] {
			d.step(ev, at, s)
		}

		// The caller runs at a yield or an await, and can keep a copy of a borrow the body
		// stored into a caller-owned place. So such a loan stays live from then on.
		if d.suspends.Contains(at) {
			for _, l := range s.loans {
				for _, h := range l.holders {
					l.static = l.static || d.callerOwned(h)
				}
			}
		}

		d.endStatement(at, s)
	}
}

// endStatement releases what holds a loan only until the end of the statement at the point
// at, and drops each loan that cannot be live after it. A loan stays while a holder is used
// again later, or while a caller-owned holder still holds it when the body returns.
func (d *bodyDataflow) endStatement(at liveness.StmtRef, s *dataflowState) {
	for k, l := range s.loans {
		l.slots.Clear()
		l.scoped, l.returned, l.reserved = false, false, nil
		if !d.loanLive(l, at, false) && !d.loanEscapes(l, at, false) {
			delete(s.loans, k)
		}
	}
}

// callerOwned reports whether h is a place under a parameter whose referent the caller owns.
func (d *bodyDataflow) callerOwned(h heldPlace) bool {
	return d.table.callerOwned.Contains(h.place.root)
}

// step applies one event to s, and reports each rule the event breaks while d.errs is set.
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
		d.copyBorrow(ev, at, s)
	case *moveEvent:
		d.move(ev, at, s)
	case *passEvent:
		d.pass(ev, s)
	case *callEvent:
		d.callEnd(ev, at, s)
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
// A dynamic index in the target, such as the `[i]` of `xs[i] = v` or `xs[i].x = v` with
// `i: number`, stands for the whole container, so `xs[i].x` has the place `xs`. The depth of
// such a write counts every link of the target, so `xs[i].x` writes two levels below `xs`
// and conflicts with a live `&xs[0]`. A write through a dynamic index may reach an element
// other than the one a moved place or a borrow names, so it neither reassigns a moved place
// nor ends a borrow.
//
// A write that uses a moved place reports only E1, the way the use-after-move subsumes the
// conflict in the current checks.
//
// Writing a place also ends what the place and each place under it held before, so after
// `c = &q` or `p.r = &q` the written place stops holding each loan that a flow earlier in
// the source gave it. A sibling such as `p.s` keeps its loans. The assigned value appears
// after the target, so the target keeps the loans its flows give, such as the borrow of q.
// In a loop, a flow later in the source can run before the assignment on an earlier
// iteration. The target keeps the loan such a flow gave it, which errs toward reporting a
// conflict.
func (d *bodyDataflow) write(ev *useEvent, at liveness.StmtRef, s *dataflowState) {
	target, _ := ev.node.(ast.Expr)
	chain := placeChain(target)
	dynamic := slices.ContainsFunc(chain, d.isDynamicIndex)
	depth := len(ev.place.path)
	if dynamic {
		depth = len(chain) - 1
	}

	moved := false
	for _, k := range slices.Sorted(maps.Keys(s.moved)) {
		m := s.moved[k]
		if m.place.root != ev.place.root || !pathPrefixRelated(m.place.path, ev.place.path) {
			continue
		}

		if len(m.place.path) < depth {
			// The write blames the part of its target that names the moved place, so a write
			// to `p.x` after p moved blames `p`.
			d.reportMoved(m, ev.place, d.placeExprAt(target, len(m.place.path)))
			moved = true
			continue
		}

		// A dynamic index may name an element other than the moved one, so the write
		// reassigns no moved place under the container. A moved place the write only
		// overlaps stays moved, as the rest `t[1...]` does after a write to `t[1]`.
		if !dynamic && pathHasPrefix(m.place.path, ev.place.path) {
			delete(s.moved, k)
		}
	}

	reported := false
	for _, l := range d.sortedLoans(s) {
		if l.closure() || !placesOverlap(l.ev.place, ev.place) {
			continue
		}

		if len(l.ev.place.path) >= depth {
			// A dynamic index may name an element the borrow does not reach, and a borrow
			// the write only overlaps still reaches the elements the write leaves alone.
			if !dynamic && pathHasPrefix(l.ev.place.path, ev.place.path) {
				delete(s.loans, l.ev)
			}
			continue
		}

		// A borrow made for one statement, such as a call argument, has ended by the time the
		// statement writes, since an assignment writes after its source is evaluated.
		if moved || reported || l.alias || d.statementOnly(l) || l.ev.borrow != immutableBorrow || !d.loanLive(l, at, false) {
			continue
		}

		// The target names the place as written, so `xs[i].x` keeps the links its place drops.
		d.report(&BorrowedValueWriteError{
			Place:  writtenPlaceName(ev.node, d.placeName(ev.place)),
			write:  ev.node,
			borrow: l.ev.node.Span(),
		})
		reported = true
	}

	if !dynamic {
		for _, l := range s.loans {
			for k, h := range l.holders {
				if h.place.root == ev.place.root && pathHasPrefix(h.place.path, ev.place.path) && !laterNode(h.node, ev.node) {
					delete(l.holders, k)
				}
			}
		}
	}
}

// statementOnly reports whether only the current statement holds l, as a call argument or
// a temporary value does.
func (d *bodyDataflow) statementOnly(l *liveLoan) bool {
	return !l.static && len(l.holders) == 0
}

// borrow applies the borrow ev. E1 rejects a borrow of a moved place. E2 rejects a borrow
// while a borrow of the other kind of an overlapping place is live. A closure capture is
// checked against neither, since E4 counts a capture as used only while the closure runs.
func (d *bodyDataflow) borrow(ev *borrowEvent, at liveness.StmtRef, s *dataflowState) {
	// A written borrow `&a` of a moved place blames its operand `a`, the place it reads.
	use := ev.node
	if be, ok := ev.node.(*ast.BorrowExpr); ok {
		use = be.Arg
	}
	d.checkMoved(ev.place, use, s)

	loans := d.sortedLoans(s)
	l := newLiveLoan(ev)
	l.static = soltype.IsStaticLifetime(ev.lifetime)
	d.holdLoan(l, ev.holder, ev.node)

	// A mutable receiver only reads its place until the call starts, so callEnd checks it
	// then, as liveLoan.reserved describes.
	if rt, ok := ev.holder.target.(receiverTarget); ok && ev.borrow == mutableBorrow {
		l.reserved = rt.call
	} else if !l.closure() {
		d.checkNewPath(ev.place, ev.borrow, ev.node, at, loans, l)
	}
	s.loans[ev] = l

	// A borrow through a holder keeps the holder's loans live while the new borrow is.
	d.addHolder(ev.place, ev.holder, ev.node, s)
	if !l.static {
		return
	}

	// A `'static` borrow keeps them live at every later point. A closure stored into
	// module-level state through a local borrows its captures this way.
	for _, held := range s.loans {
		if held.heldAt(ev.place) {
			held.static = true
		}
	}
}

// liveInBody reports whether the new path l is live in the body right after the point at
// that makes it. A path a statement holds, or that a holder other than a caller-owned place
// holds, counts as live. A path only caller-owned places hold is live only while
// placeReaches finds a later read of one of them, since the caller reads such a place once
// the body returns.
func (d *bodyDataflow) liveInBody(l *liveLoan, at liveness.StmtRef) bool {
	if l.static || l.scoped || len(l.slots) > 0 || len(l.holders) == 0 {
		return true
	}
	for _, h := range l.holders {
		if !d.callerOwned(h) {
			return true
		}
	}
	return d.loanLive(l, at, false)
}

// checkNewPath reports E2 when node makes the new path fresh of the given kind to place, and
// one of loans has the other kind, has a place that overlaps place, and is live together
// with fresh. The two are live together when both are live in the body, or when the caller
// can reach both once the body returns. loans lists the loans to check in the order
// sortedLoans returns them.
//
// A loan taken at the point at counts as live when its holders are live after that point,
// since a holder the statement defines is not live on entry to it. So
// `val t = {p: &mut b.x, q: &b.x}` breaks E2 when t is used later.
func (d *bodyDataflow) checkNewPath(place movePlace, kind borrowKind, node ast.Node, at liveness.StmtRef, loans []*liveLoan, fresh *liveLoan) {
	// Where fresh is live can take a walk of the CFG, so it is found once a loan needs it.
	found := false
	var local, escapes bool
	for _, first := range loans {
		if !first.checked() || first.reserved != nil || first.ev.borrow == kind || !placesOverlap(first.ev.place, place) {
			continue
		}

		if !found {
			local, escapes, found = d.liveInBody(fresh, at), d.loanEscapes(fresh, at, false), true
		}
		firstLocal := d.loanLive(first, at, true) || (first.ev.at == at && d.loanLive(first, at, false))
		if (local && firstLocal) || (escapes && d.loanEscapes(first, at, true)) {
			d.report(&BorrowAliasError{
				Place:      d.placeName(place),
				FirstPlace: d.placeName(first.ev.place),
				FirstMut:   first.ev.borrow == mutableBorrow,
				SecondMut:  kind == mutableBorrow,
				root:       place.root,
				places:     [2]movePlace{place, first.ev.place},
				exits:      d.returnSpans(first, fresh),
				node:       node,
				first:      first.ev.node.Span(),
			})
			return
		}
	}
}

// returnSpans returns the span of each return the value of a holder of one of loans flows
// into, as returnsOf finds them.
func (d *bodyDataflow) returnSpans(loans ...*liveLoan) []ast.Span {
	seen := set.NewSet[ast.Span]()
	var out []ast.Span
	for _, l := range loans {
		for _, h := range l.holders {
			for _, site := range d.table.returnsOf(h.place.root) {
				if span := site.node.Span(); !seen.Contains(span) {
					seen.Add(span)
					out = append(out, span)
				}
			}
		}
	}
	return out
}

// copyBorrow applies the copy ev of a borrow. E1 rejects a copy of a moved place.
//
// The holder gets a path of kind ev.borrow through the borrow at ev.place, so E2 checks it
// against the loans through that borrow, the ones liveLoan.through marks. In
// `readWrite(m, m)` with `m: &mut {v: number}` and a `&` first parameter, the second argument
// makes a mutable path through m while the first holds an immutable one. A loan of the
// storage at ev.place, such as the `&mut h` of a later copy `val c = h.s`, reaches the slot
// and not what the slot points to, so the copy is not checked against it.
//
// A copy that keeps the borrow's kind and lifetime makes the holder a second holder of each
// loan a place overlapping ev.place holds, as `val c = a` does after `val a = &x`. A copy
// into a destination of the other kind, or into a `'static` one, instead derives loans of
// that kind and lifetime for the holder alone:
//
//   - one through the borrow at ev.place, so `sink = m` with a module-level sink derives a
//     `'static` mutable loan through m, and a later `&` path through m breaks E2.
//   - one from each loan such a place holds. After `val b = &mut a`, the copy
//     `val c: &{x: number} = b` gives c an immutable loan of a, so a write to a while c is
//     live breaks E2.
func (d *bodyDataflow) copyBorrow(ev *copyEvent, at liveness.StmtRef, s *dataflowState) {
	d.checkMoved(ev.place, ev.node, s)

	loans := d.sortedLoans(s)
	through := slices.DeleteFunc(slices.Clone(loans), func(l *liveLoan) bool { return !l.through })
	path := newLiveLoan(nil)
	d.holdLoan(path, ev.holder, ev.node)
	path.static = path.static || soltype.IsStaticLifetime(ev.lifetime)
	d.checkNewPath(ev.place, ev.borrow, ev.node, at, through, path)

	_, toModule := ev.holder.target.(moduleTarget)
	if ev.borrow == ev.source && !soltype.IsStaticLifetime(ev.lifetime) && !toModule {
		held := false
		for _, l := range loans {
			held = held || (l.heldAt(ev.place) && !l.closure())
		}

		d.addHolder(ev.place, ev.holder, ev.node, s)
		if held {
			return
		}

		// The holder reaches ev.place through a borrow no loan covers. An alias loan records
		// that, so a later narrowing or `'static` copy of the holder derives from ev.place.
		d.derivedLoan(ev, nil, s).alias = true
		return
	}

	d.derivedLoan(ev, nil, s)
	for _, l := range loans {
		if !l.heldAt(ev.place) {
			continue
		}

		// A closure's capture is used only while the closure runs, so the holder keeps it as
		// it is rather than taking a loan of another kind.
		if l.closure() {
			d.holdLoan(l, ev.holder, ev.node)
			continue
		}
		d.derivedLoan(ev, l, s)
	}
}

// derivedKey names a loan a copy derives: the copy, and the borrow event of the loan it
// derives from, or nil for the loan through the copied borrow.
type derivedKey struct {
	copied *copyEvent
	from   *borrowEvent
}

// loanOrder is the position of a loan in the order the pass reports loans in. event is the
// index in the table of the event that took the loan. derived is 0 for a loan a borrow event
// takes, and counts the loans a copy derives in the order the pass first derives them.
type loanOrder struct {
	event   int
	derived int
}

// derivedLoan adds to s the loan the copy ev derives from the loan from, and returns it. A nil
// from derives the loan through the borrow at ev.place. The loan has from's place, or ev.place
// when from is nil, and the kind and lifetime ev gives its holder. ev.holder holds it.
func (d *bodyDataflow) derivedLoan(ev *copyEvent, from *liveLoan, s *dataflowState) *liveLoan {
	key := derivedKey{copied: ev}
	place, through := ev.place, true
	if from != nil {
		key.from = from.ev
		place, through = from.ev.place, from.through
	}

	bev, ok := d.derived[key]
	if !ok {
		bev = &borrowEvent{
			flowBase: flowBase{eventBase: eventBase{place: place, node: ev.node, at: ev.at}, holder: ev.holder},
			borrow:   ev.borrow,
			lifetime: ev.lifetime,
		}
		d.derived[key] = bev
		d.order[bev] = loanOrder{event: d.copyIndex[ev], derived: len(d.derived)}
	}

	l := newLiveLoan(bev)
	l.static, l.through = soltype.IsStaticLifetime(bev.lifetime), through
	s.loans[bev] = l
	d.holdLoan(l, ev.holder, ev.node)
	return l
}

// move applies the move ev. E1 rejects a move of a moved place, and E3 rejects a move while
// a borrow of an overlapping place is live. A return that moves a closure's capture hands
// over the closure's own borrow of the place too, so that borrow is no conflict. A move
// ev.whileCaptured marks applies only while that borrow is live.
//
// The error blames the first conflicting borrow sortedLoans returns, and marks the move as
// conflicting with a closure when any conflicting borrow is a closure's capture.
func (d *bodyDataflow) move(ev *moveEvent, at liveness.StmtRef, s *dataflowState) {
	if ev.whileCaptured && !d.capturesPlace(ev.closure, ev.place, s) {
		return
	}

	if !d.checkMoved(ev.place, ev.node, s) {
		var err *MoveWhileBorrowedError
		for _, l := range d.sortedLoans(s) {
			own := ev.closure != nil && l.ev.node == ev.closure
			if own || l.alias || !placesOverlap(l.ev.place, ev.place) || !d.loanLive(l, at, true) {
				continue
			}

			if err == nil {
				err = &MoveWhileBorrowedError{Place: d.placeName(ev.place), move: ev.node, borrow: l.ev.node.Span()}
			}
			err.closure = err.closure || l.closure()
		}

		if err != nil {
			d.report(err)
		}
	}

	s.moved[placeKey(ev.place)] = movedPlace{place: ev.place, node: ev.node, every: true}
	d.mergeTupleMove(ev.place, ev.node, s)

	// The holder now owns what the place held, including any borrow stored in it.
	d.addHolder(ev.place, ev.holder, ev.node, s)
}

// capturesPlace reports whether s holds closure's own borrow of place.
func (d *bodyDataflow) capturesPlace(closure ast.Func, place movePlace, s *dataflowState) bool {
	for _, l := range s.loans {
		if l.ev.node == closure && placeKey(l.ev.place) == placeKey(place) {
			return true
		}
	}
	return false
}

// mergeTupleMove replaces the moves of the elements of a tuple with one move of the whole
// tuple once every element a tuple pattern names over it has moved. place is the place just
// moved, and node is the expression that moved it. `val [x, y] = b` moves `b.0` and `b.1`,
// and then a later use of b reads a moved value rather than a partially moved one. The
// tuple is moved on every path only when each of its elements is.
func (d *bodyDataflow) mergeTupleMove(place movePlace, node ast.Node, s *dataflowState) {
	for len(place.path) > 0 {
		tuple := movePlace{root: place.root, path: place.path[:len(place.path)-1]}
		parts := d.table.tupleParts[placeKey(tuple)]
		if len(parts) == 0 {
			return
		}

		keys := make([]string, len(parts))
		every := true
		for i, seg := range parts {
			keys[i] = placeKey(movePlace{root: tuple.root, path: append(slices.Clip(tuple.path), seg)})
			m, moved := s.moved[keys[i]]
			if !moved {
				return
			}
			every = every && m.every
		}

		for _, k := range keys {
			delete(s.moved, k)
		}
		s.moved[placeKey(tuple)] = movedPlace{place: tuple, node: node, every: every}
		place = tuple
	}
}

// holdLoan records holder as a holder of l, through the flow whose expression is node.
func (d *bodyDataflow) holdLoan(l *liveLoan, holder eventHolder, node ast.Node) {
	switch tg := holder.target.(type) {
	case placeTarget:
		p := movePlace{root: tg.root, path: holder.path}
		l.holders[placeKey(p)] = heldPlace{place: p, node: node}
	case moduleTarget:
		l.static = true
	case argTarget:
		l.slots.Add(callSlot{call: tg.call, index: tg.index})
	case receiverTarget:
		l.slots.Add(callSlot{call: tg.call, index: selfIndex})
	case returnTarget:
		l.scoped, l.returned = true, true
	case tempTarget:
		l.scoped = true
	}
}

// addHolder makes holder a holder of each loan a place overlapping src holds, since holder
// now reaches what src borrows. node is the expression of the flow. A loan held under src
// lands at the same path under holder, so `val c = a` with a loan held at `a.peer` makes
// `c.peer` its holder.
func (d *bodyDataflow) addHolder(src movePlace, holder eventHolder, node ast.Node, s *dataflowState) {
	for _, l := range s.loans {
		var under [][]placeSeg
		for _, h := range l.holders {
			if placesOverlap(h.place, src) {
				under = append(under, h.place.path[min(len(src.path), len(h.place.path)):])
			}
		}

		for _, rest := range under {
			d.holdLoan(l, holder.extend(rest), node)
		}
	}
}

// pass applies the passEvent ev. Each loan the slot ev.from holds gains ev.holder as a holder.
// A passEvent that is not direct skips the loan the slot's own borrow took, unless the slot
// also holds a mutable loan. A store through a target argument that names a borrow, such as
// the `a` of `store(a, &b)` after `val a = &mut x`, lands in the place that borrow reaches,
// so `x.peer` holds the loan too.
func (d *bodyDataflow) pass(ev *passEvent, s *dataflowState) {
	holders := []eventHolder{ev.holder}
	if pt, ok := ev.holder.target.(placeTarget); ok && ev.through != nil {
		for _, m := range s.loans {
			for _, h := range m.holders {
				if h.place.root != pt.root || !pathHasPrefix(ev.through, h.place.path) {
					continue
				}

				pointee := appendPath(m.ev.place.path, ev.holder.path[len(h.place.path):])
				holders = append(holders, eventHolder{target: placeTarget{root: m.ev.place.root}, path: pointee})
			}
		}
	}

	// The signature shares only a lifetime inside the slot's referent, so the holder reaches
	// what the referent holds and not the referent itself. A shared borrow the referent holds
	// is copied out whole. A mutable one cannot be copied, so the callee reborrows it through
	// the slot's own borrow, and the holder keeps that borrow too.
	skipOwn := !ev.direct
	for _, l := range s.loans {
		if skipOwn && l.slots.Contains(ev.from) && !holderIsSlot(l.ev.holder, ev.from) && l.ev.borrow == mutableBorrow {
			skipOwn = false
		}
	}

	for _, l := range s.loans {
		if !l.slots.Contains(ev.from) || (skipOwn && holderIsSlot(l.ev.holder, ev.from)) {
			continue
		}
		for _, h := range holders {
			d.holdLoan(l, h, ev.node)
		}
	}
}

// holderIsSlot reports whether h is the call slot itself, rather than a position inside it.
func holderIsSlot(h eventHolder, slot callSlot) bool {
	if len(h.path) > 0 {
		return false
	}
	switch tg := h.target.(type) {
	case argTarget:
		return callSlot{call: tg.call, index: tg.index} == slot
	case receiverTarget:
		return callSlot{call: tg.call, index: selfIndex} == slot
	}
	return false
}

// callEnd applies the callEvent ev in two steps:
//
//  1. A mutable receiver the call reserved takes effect. E2 rejects an immutable path to an
//     overlapping place that is still live. A path is still live when an argument of a call
//     that has not returned holds it, or when its holder is used after the call, later in
//     the statement or after it. So `val r = &v; v.push(r.len())` checks clean, since r is
//     dead once the arguments are evaluated. The later of the two paths is blamed, so
//     `p.writeRead(&p)` reports `&p`.
//  2. Each loan a slot of the call holds is released, and a loan nothing else holds ends.
func (d *bodyDataflow) callEnd(ev *callEvent, at liveness.StmtRef, s *dataflowState) {
	loans := d.sortedLoans(s)
	for _, r := range loans {
		if r.reserved != ev.call {
			continue
		}

		r.reserved = nil
		after := false
		for _, m := range loans {
			after = after || m == r
			if m == r || !m.checked() || m.ev.borrow == mutableBorrow || !placesOverlap(m.ev.place, r.ev.place) ||
				(!d.loanLive(m, at, false) && !d.usedLaterAt(m, ev, at)) {
				continue
			}

			// The later of the two paths is the one blamed.
			first, second := m, r
			if after {
				first, second = r, m
			}
			d.report(&BorrowAliasError{
				Place:      d.placeName(second.ev.place),
				FirstPlace: d.placeName(first.ev.place),
				FirstMut:   first == r,
				SecondMut:  second == r,
				root:       second.ev.place.root,
				node:       second.ev.node,
				first:      first.ev.node.Span(),
			})
			break
		}
	}

	for k, l := range s.loans {
		for slot := range l.slots {
			if slot.call == ev.call {
				l.slots.Remove(slot)
			}
		}
		if d.statementOnly(l) && !l.scoped && len(l.slots) == 0 {
			delete(s.loans, k)
		}
	}
}

// usedLaterAt reports whether an event after ev at the point at reads a place a holder of l
// overlaps, so l is used later in the same statement.
func (d *bodyDataflow) usedLaterAt(l *liveLoan, ev ownershipEvent, at liveness.StmtRef) bool {
	events := d.events[at]
	i := slices.Index(events, ev)
	if i < 0 {
		return false
	}

	for _, later := range events[i+1:] {
		p := later.base().place
		for _, h := range l.holders {
			if placesOverlap(h.place, p) {
				return true
			}
		}
	}
	return false
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
//
// A loan a statement holds, as a call argument or a temporary value, is live until the end
// of that statement. Any other loan is live while one of its holders is, as heldPlaceLive
// decides.
func (d *bodyDataflow) loanLive(l *liveLoan, at liveness.StmtRef, before bool) bool {
	if l.static || l.scoped || len(l.slots) > 0 {
		return true
	}

	for _, h := range l.holders {
		if d.heldPlaceLive(h, at, before) {
			return true
		}
	}
	return false
}

// heldPlaceLive reports whether the holder h is used again after the point at, or at it when
// before is set. A local holds its loans while liveness says the local is live. A
// caller-owned place holds them while placeReaches finds a later read of it, since the caller
// reads it only once the body returns or suspends.
func (d *bodyDataflow) heldPlaceLive(h heldPlace, at liveness.StmtRef, before bool) bool {
	if !d.holderLive(h.place.root, at, before) {
		return false
	}
	return !d.callerOwned(h) || d.placeReaches(h, at, before, false)
}

// loanEscapes reports whether the caller can reach l once the body returns, from the point
// at onward. before also counts the events at the point at. A 'static loan escapes, and so
// does one a return holds. A caller-owned holder escapes when placeReaches finds a path to the exit
// that no write replaces it on. Any other holder escapes while it is live and its value
// flows into a return, a module-level binding, or a caller-owned place, as rootEscapes
// decides.
func (d *bodyDataflow) loanEscapes(l *liveLoan, at liveness.StmtRef, before bool) bool {
	if l.static || l.returned {
		return true
	}

	for _, h := range l.holders {
		if d.callerOwned(h) {
			if d.placeReaches(h, at, before, true) {
				return true
			}
			continue
		}

		if d.rootEscapes(h.place.root) && d.heldPlaceLive(h, at, before) {
			return true
		}
	}
	return false
}

// rootEscapes reports whether the value of the local root flows into a return, a
// module-level binding, or a caller-owned place, directly or through other locals.
func (d *bodyDataflow) rootEscapes(root liveness.VarID) bool {
	if out, ok := d.escapes[root]; ok {
		return out
	}

	t := d.table
	out := t.storedFrom(root) || len(t.returnsOf(root)) > 0 ||
		slices.ContainsFunc(t.reachedFrom(root), t.callerOwned.Contains)
	d.escapes[root] = out
	return out
}

// reachKey names one question placeReaches answers.
type reachKey struct {
	place  string
	node   ast.Node
	at     liveness.StmtRef
	before bool
	exit   bool
}

// placeReaches follows the CFG forward from the point at and reports what it finds for the
// holder h. With exit unset, it reports whether some path reads a place overlapping h before
// a write replaces h. With exit set, it reports whether some path reaches the end of the body
// without such a write. before starts the walk at the events of at itself.
//
// A write replaces h when it writes h or a place above it with no dynamic index, so
// `p.r = &e` replaces a holder at `p.r` and `p.r.x = 1` reads it. A write replaces h only
// when the flow that made h a holder does not come after it in the source, the way write
// ends holders. So in `p.r = if c { &b } else { &e }`, the write of `p.r` after each branch
// keeps the borrow that branch stored.
func (d *bodyDataflow) placeReaches(h heldPlace, at liveness.StmtRef, before, exit bool) bool {
	key := reachKey{place: placeKey(h.place), node: h.node, at: at, before: before, exit: exit}
	if out, ok := d.reaches[key]; ok {
		return out
	}

	// effect returns 1 for a read of h, -1 for a write that replaces h, and 0 otherwise.
	effect := func(ref liveness.StmtRef) int {
		for _, ev := range d.events[ref] {
			p := ev.base().place
			if p.root != h.place.root || !pathPrefixRelated(p.path, h.place.path) {
				continue
			}

			use, isUse := ev.(*useEvent)
			if !isUse || use.access != writeAccess || !pathHasPrefix(h.place.path, p.path) {
				return 1
			}

			target, _ := use.node.(ast.Expr)
			if slices.ContainsFunc(placeChain(target), d.isDynamicIndex) {
				return 1
			}
			if !laterNode(h.node, use.node) {
				return -1
			}
		}
		return 0
	}

	seen := set.NewSet[int]()
	var walk func(block int, from int) bool
	walk = func(block int, from int) bool {
		for _, ref := range d.points[block] {
			if ref.StmtIdx < from {
				continue
			}
			switch effect(ref) {
			case 1:
				if !exit {
					return true
				}
			case -1:
				return false
			}
		}

		b := d.table.cfg.Blocks[block]
		if b == d.table.cfg.Exit {
			return exit
		}
		for _, succ := range b.Successors {
			if !seen.Contains(succ.ID) {
				seen.Add(succ.ID)
				if walk(succ.ID, math.MinInt) {
					return true
				}
			}
		}
		return false
	}

	from := at.StmtIdx + 1
	if before {
		from = at.StmtIdx
	}
	out := walk(at.BlockID, from)
	d.reaches[key] = out
	return out
}

// suspendPoints returns the CFG point of each statement of cfg that yields or awaits outside
// a nested function.
func suspendPoints(cfg *liveness.CFG) set.Set[liveness.StmtRef] {
	out := set.NewSet[liveness.StmtRef]()
	for _, b := range cfg.Blocks {
		for i, stmt := range b.Stmts {
			v := &suspendFinder{}
			stmt.Accept(v)
			if v.found {
				out.Add(liveness.StmtRef{BlockID: b.ID, StmtIdx: i})
			}
		}
	}
	return out
}

// suspendFinder walks a statement for suspendPoints and sets found at a yield or an await.
type suspendFinder struct {
	ast.DefaultVisitor
	found bool
}

func (v *suspendFinder) EnterExpr(e ast.Expr) bool {
	switch e.(type) {
	case *ast.YieldExpr, *ast.AwaitExpr:
		v.found = true
	case *ast.FuncExpr:
		return false
	}
	return !v.found
}

func (v *suspendFinder) EnterDecl(d ast.Decl) bool {
	_, isFunc := d.(*ast.FuncDecl)
	return !isFunc && !v.found
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

// placeChain returns the member and index chain e roots, from e itself down to its root.
func placeChain(e ast.Expr) []ast.Expr {
	var chain []ast.Expr
	for e != nil {
		chain = append(chain, e)
		switch x := e.(type) {
		case *ast.MemberExpr:
			e = x.Object
		case *ast.IndexExpr:
			e = x.Object
		default:
			e = nil
		}
	}
	return chain
}

// isDynamicIndex reports whether e is an index whose key exprPlace treats as dynamic, so
// that the index stands for its whole container. `xs[i]` with `i: number` is one, and `xs[0]`
// is not.
func (d *bodyDataflow) isDynamicIndex(e ast.Expr) bool {
	ix, ok := e.(*ast.IndexExpr)
	if !ok {
		return false
	}
	_, constant := d.indexSegment(ix.Index)
	return !constant
}

// placeExprAt returns the outermost expression of the member and index chain e roots that
// is depth links away from the root. A dynamic index reaches every place under its container,
// so the walk returns the first dynamic index it meets. For depth 0 that is `p` in `p.x`, and
// for any depth it is `xs[i]` in `xs[i].x` with `i: number`. It returns e when no expression
// of the chain is that deep.
func (d *bodyDataflow) placeExprAt(e ast.Expr, depth int) ast.Node {
	chain := placeChain(e)
	var found ast.Node = e
	for i := len(chain) - 1; i >= 0; i-- {
		if d.isDynamicIndex(chain[i]) {
			return chain[i]
		}
		n := len(chain) - 1 - i
		if n > depth {
			break
		}
		if n == depth {
			found = chain[i]
		}
	}
	return found
}

// report records e when d is reporting and no diagnostic is reported at e's span yet.
func (d *bodyDataflow) report(e SolverError) {
	if d.errs == nil || d.blamed.Contains(e.Span()) {
		return
	}
	d.blamed.Add(e.Span())
	*d.errs = append(*d.errs, e)
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

// sortedLoans returns the loans of s in the order loanOrder gives, so a report names the same
// loan on every run.
func (d *bodyDataflow) sortedLoans(s *dataflowState) []*liveLoan {
	out := make([]*liveLoan, 0, len(s.loans))
	for _, l := range s.loans {
		out = append(out, l)
	}

	slices.SortFunc(out, func(a, b *liveLoan) int {
		oa, ob := d.order[a.ev], d.order[b.ev]
		return cmp.Or(cmp.Compare(oa.event, ob.event), cmp.Compare(oa.derived, ob.derived))
	})
	return out
}
