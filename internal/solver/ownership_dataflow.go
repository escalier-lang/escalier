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
// A holder is the destination that receives a borrow, such as the `c` of `val c = &p`. A
// loan is one borrow event the pass tracks: the place borrowed, its kind, and the holders
// that keep it live. A loan is live at a point while one of its holders is used again later,
// which the body's liveness answers. A copy of a holder becomes a second holder. A copy into a
// destination of the other kind, or into a `'static` one, derives a loan of its own instead, as
// copyBorrow describes. A loan held by a module-level binding, or one whose lifetime is
// `'static`, is live at every later point. A borrow takes its kind and its `'static` lifetime
// from its destination, so `track(&p)` with `track(v: &'static {x: number})` is `'static`.
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
	c.errs = append(c.errs, found...)
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

// liveLoan is one borrow the pass tracks.
type liveLoan struct {
	// ev is the borrow event that took the loan. A loan a copy derives has an event of its
	// own, which derivedLoan makes.
	ev *borrowEvent
	// holders maps the root of each local place that holds the borrow or a copy of it to the
	// expression of the flow that made it a holder.
	holders map[liveness.VarID]ast.Node
	// static marks a loan that stays live at every later point.
	static bool
	// statement marks a loan that lasts only until the end of the statement that created it.
	statement bool
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
		if !ok || ol.static != l.static || ol.alias != l.alias || !maps.Equal(ol.holders, l.holders) {
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
// Writing a whole local, as in `c = &q`, also ends what c held before. c stops holding each
// loan that a flow earlier in the source gave it. The assigned value appears after the
// target `c`, so c keeps the loans its flows give, such as the borrow of q. In a loop, a flow
// later in the source can run before the assignment on an earlier iteration. c keeps the
// loan such a flow gave it, which errs toward reporting a conflict.
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
		if moved || reported || l.alias || l.statement || l.ev.borrow != immutableBorrow || !d.loanLive(l, at, false) {
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
	// A written borrow `&a` of a moved place blames its operand `a`, the place it reads.
	use := ev.node
	if be, ok := ev.node.(*ast.BorrowExpr); ok {
		use = be.Arg
	}
	d.checkMoved(ev.place, use, s)

	l := &liveLoan{ev: ev, holders: map[liveness.VarID]ast.Node{}}
	if !l.closure() {
		d.checkNewPath(ev.place, ev.borrow, ev.node, at, d.sortedLoans(s))
	}

	l.static = soltype.IsStaticLifetime(ev.lifetime)
	s.loans[ev] = l
	d.holdLoan(l, ev.holder, ev.node)

	// A borrow through a holder keeps the holder's loans live while the new borrow is.
	d.addHolder(ev.place.root, ev.holder, ev.node, s)
	if !l.static {
		return
	}

	// A `'static` borrow keeps them live at every later point. A closure stored into
	// module-level state through a local borrows its captures this way.
	for _, held := range s.loans {
		if _, holds := held.holders[ev.place.root]; holds {
			held.static = true
		}
	}
}

// checkNewPath reports E2 when node makes a new path of the given kind to place while one of
// loans is live, has the other kind, and has a place that overlaps place. loans lists the
// loans to check in the order sortedLoans returns them.
func (d *bodyDataflow) checkNewPath(place movePlace, kind borrowKind, node ast.Node, at liveness.StmtRef, loans []*liveLoan) {
	for _, first := range loans {
		if !first.checked() || first.ev.borrow == kind || !placesOverlap(first.ev.place, place) {
			continue
		}

		if d.loanLive(first, at, true) {
			d.report(&BorrowAliasError{
				Place:      d.placeName(place),
				FirstPlace: d.placeName(first.ev.place),
				FirstMut:   first.ev.borrow == mutableBorrow,
				SecondMut:  kind == mutableBorrow,
				node:       node,
				first:      first.ev.node.Span(),
			})
			return
		}
	}
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
// loan ev.place's root holds, as `val c = a` does after `val a = &x`. A copy into a
// destination of the other kind, or into a `'static` one, instead derives loans of that
// kind and lifetime for the holder alone:
//
//   - one through the borrow at ev.place, so `sink = m` with a module-level sink derives a
//     `'static` mutable loan through m, and a later `&` path through m breaks E2.
//   - one from each loan the root holds. After `val b = &mut a`, the copy
//     `val c: &{x: number} = b` gives c an immutable loan of a, so a write to a while c is
//     live breaks E2.
func (d *bodyDataflow) copyBorrow(ev *copyEvent, at liveness.StmtRef, s *dataflowState) {
	d.checkMoved(ev.place, ev.node, s)

	loans := d.sortedLoans(s)
	through := slices.DeleteFunc(slices.Clone(loans), func(l *liveLoan) bool { return !l.through })
	d.checkNewPath(ev.place, ev.borrow, ev.node, at, through)

	_, toModule := ev.holder.target.(moduleTarget)
	if ev.borrow == ev.source && !soltype.IsStaticLifetime(ev.lifetime) && !toModule {
		held := false
		for _, l := range loans {
			_, holds := l.holders[ev.place.root]
			held = held || (holds && !l.closure())
		}

		d.addHolder(ev.place.root, ev.holder, ev.node, s)
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
		if _, holds := l.holders[ev.place.root]; !holds {
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

	l := &liveLoan{ev: bev, holders: map[liveness.VarID]ast.Node{}, static: soltype.IsStaticLifetime(bev.lifetime), through: through}
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
	d.addHolder(ev.place.root, ev.holder, ev.node, s)
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
