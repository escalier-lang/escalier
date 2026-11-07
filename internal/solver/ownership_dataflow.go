package solver

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// This file is the dataflow pass of the checking model in
// planning/ownership_rules/requirements.md. It checks the ownership events that
// ownership_events.go records against three rules:
//
//   - E1 rejects a use of a place after a move out of it, until the place is reassigned.
//   - E2 rejects an immutable path and a mutable path to one place that are live together.
//   - E3 rejects a move of a place while a borrow of it is live.
//
// E4 says when a closure's captures count as used. A closure holds a loan of each capture
// between calls, which conflicts only with a move. A call that runs the closure, or may run
// it, uses each capture during the call, which ownership_runs.go records as a runEvent.
//
// The pass uses these terms:
//
//   - A place is a local and a field path under it, such as `p.r`.
//   - A point is one statement of the body's control-flow graph, the CFG. Events attach to
//     points.
//   - A path is a way to reach the value at a place. A borrow makes a mutable or an immutable
//     path, and so does a copy of a borrow.
//   - A holder is a place that receives a borrow, such as the `c` of `val c = &p`. A call
//     argument, a receiver, a return, and a temporary value hold borrows too.
//   - A loan is one borrow the pass tracks. It records the place borrowed, the kind of the
//     borrow, and the holders that keep it live.
//   - A loan is live at a point while one of its holders is used again later. The body's
//     liveness answers that question.
//
// One walk of the CFG checks all three rules. The state at each point holds the places moved
// on some path that reaches the point, which E1 reads, and the loans that may still be live,
// which E2 and E3 read. checkBodyDataflow runs the walk in two steps:
//
//  1. It iterates a worklist of blocks forward until the state on entry to each block stops
//     changing. Nothing is reported in this step.
//  2. It walks each reachable block once more from its entry state and reports.
//
// Each event updates the state and checks the rules it can break:
//
//   - A read checks E1 against the moved places. A write checks E1, checks E2 against a live
//     immutable loan of a place above its target, and ends what its target held.
//   - A borrow checks E1, checks E2 against the live loans of the other kind, and adds a loan.
//   - A move checks E1, checks E3 against the live loans of an overlapping place, and marks
//     the place moved.
//   - A copy of a borrow makes its destination a second holder of each loan the source holds.
//     A copy into a destination of the other kind, or into a `'static` one, derives a loan
//     of its own instead, as copyBorrow describes.
//   - A copy of a function value makes its destination a holder of each closure's capture
//     the source holds, as copyValue describes.
//   - A passEvent gives the loans of a call argument to the store target or the result that
//     the callee's signature ties the argument to. A callEvent releases what the call's
//     arguments and receiver held.
//   - A runEvent uses the captures of each closure the call runs or may run, as `run`
//     describes.
//
// How long a loan lasts depends on its holders:
//
//   - A call argument or a receiver holds a loan until the call returns.
//   - A return or a temporary value holds a loan until the end of its statement.
//   - A module-level binding holds a loan at every later point, and so does a `'static`
//     lifetime. A borrow takes its kind and its `'static` lifetime from its destination, so
//     `track(&p)` with `track(v: &'static {x: number})` is `'static`.
//   - A local place holds a loan until a write replaces it. A write to `p.r` ends what `p.r`
//     held and leaves what `p.s` holds.
//   - The caller reads a place under a parameter whose referent the caller owns once the
//     body returns or suspends. Such a place holds a loan from the point it takes it to the
//     end of the body, unless a write replaces it.

// UseAfterMoveError is reported when a binding is read after an owned value has
// moved out of it. It blames the read and points its related span at the move site.
// Conditional records whether only some paths reaching the read moved the binding, so a
// diagnostic consumer can distinguish a definite use-after-move from a possible one.
type UseAfterMoveError struct {
	// Name is the moved place's source name, for the message — `pair.a` when the
	// field `pair.a` was consumed.
	Name string
	// ReadName is the read place's source name, set only for a partial-move read where
	// it differs from Name — `pair` when the whole object is read after `pair.a` moved.
	ReadName string
	// Partial is true when the read place is a strict ancestor of the moved place, so
	// the read exposes a field that moved out rather than the moved place itself. It
	// selects the partially-moved wording and pairs with ReadName.
	Partial bool
	// Conditional is true when the binding is moved on some but not all paths
	// reaching the use, and false when every reaching path moved it.
	Conditional bool
	// Via names the closure the call being blamed may run, when the read is that closure's
	// access to what it captures. It is a phrase closureVia or retainedVia returns, and is empty
	// otherwise.
	Via string
	// ViaRuns marks a closure named by Via that the call is sure to run, because the call's
	// callee names it.
	ViaRuns bool
	// ViaReads marks a closure named by Via that only reads the place. It is false for one
	// that writes it.
	ViaReads bool
	// use is the read being rejected; it self-blames from here.
	use ast.Node
	// moveSite is the expression that moved the binding, used for the related span. It
	// may be nil when the move names no expression.
	moveSite ast.Node
}

func (*UseAfterMoveError) isSolverError()   {}
func (e *UseAfterMoveError) Span() ast.Span { return e.use.Span() }
func (e *UseAfterMoveError) Related() []ast.Span {
	if e.moveSite == nil {
		return nil
	}
	return []ast.Span{e.moveSite.Span()}
}
func (e *UseAfterMoveError) Message() string {
	if e.Partial {
		return fmt.Sprintf("use of partially moved value '%s'; field '%s' was moved out", e.ReadName, e.Name)
	}
	if e.Via != "" {
		access := "writes"
		if e.ViaReads {
			access = "reads"
		}
		return fmt.Sprintf("this call %s %s, which %s '%s' after it was moved", runsVerb(e.ViaRuns), e.Via, access, e.Name)
	}
	return fmt.Sprintf("use of moved value '%s'", e.Name)
}

// runOwnershipDataflow checks every event table in c.info that it has not checked yet and
// appends the pass's diagnostics to c.errs. It must run after settleAllFlows, since the pass
// reads only settled events.
func (c *checker) runOwnershipDataflow() {
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

	// A closure's body is checked after the body it is written in, so its diagnostics are put
	// back in source order.
	slices.SortStableFunc(found, func(a, b SolverError) int { return compareSpans(a.Span(), b.Span()) })
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
	// `node` is the expression that moved the place.
	node ast.Node
	// `every` is true when every path reaching the point moved the place.
	every bool
	// closureBorrowed marks a move that E3 rejects against a closure's borrow of the place.
	// E4's `moved` example puts the error on the move, so a later call of the closure does
	// not report the moved capture again.
	closureBorrowed bool
	// `called` marks the binding of a closure that a call of it moved, as D2 decides. Only a
	// later call of the closure uses the binding.
	called bool
}

// heldPlace is one local place that holds a loan, and the expression of the flow that made
// the place a holder.
type heldPlace struct {
	place movePlace
	node  ast.Node
	// `own` marks a place whose value is the loan's borrow itself, rather than a value that
	// holds the borrow inside it. After `val a = &x`, `a` holds the loan of x this way. After
	// `val t = {p: &x}`, `t.p` holds it this way and t does not.
	own bool
}

// liveLoan is one borrow the pass tracks.
type liveLoan struct {
	// ev is the borrow event that took the loan. A loan a copy derives has an event of its
	// own, which derivedLoan makes.
	ev *borrowEvent
	// `holders` maps the placeKey of each local place that holds the borrow or a copy of it to
	// that place. `out.slot = a.peer` makes `out.slot` a holder of each loan `a.peer` holds,
	// and a later write to `out.slot` ends what that holder held.
	holders map[string]heldPlace
	// `static` marks a loan that stays live at every later point.
	static bool
	// `slots` holds each argument or receiver of a call that holds the loan. The callEvent of
	// that call releases the slot.
	slots set.Set[callSlot]
	// ownSlots holds each slot of `slots` whose value is the borrow itself, the way
	// heldPlace.own marks a place. The argument of `f(&x)` holds its loan this way, and so
	// does the argument of `f(a)` after `val a = &x`. The argument of `f({p: &x})` does not.
	ownSlots set.Set[callSlot]
	// `scoped` marks a loan that a return or a temporary value holds. It lasts until the end of
	// the statement. `returned` marks one a return holds, which the caller receives.
	scoped   bool
	returned bool
	// `reserved` is the method call whose `&mut self` receiver took the loan, until that call
	// starts. Evaluating the call's arguments only reads the receiver, so until then the loan
	// conflicts with no immutable path. callEnd checks the immutable paths still live when the
	// call starts. That lets `v.push(v.len())` read v while the outer call is pending.
	reserved *ast.CallExpr
	// `alias` marks a loan that records only that its holders reach ev.place through a copy of
	// a borrow no loan of the body covers, such as a copy of a parameter. It creates no new
	// path, so no rule checks against it. A later copy of one of its holders into a `'static`
	// destination or a destination of another kind derives a loan from it.
	alias bool
	// `through` marks a loan of a path through the borrow ev.place holds to what that borrow
	// reaches, which a copy of the borrow derives. Any other loan is of the storage at ev.place.
	through bool
	// `via` is the closure a call may run, for a loan that stands for that closure's write of
	// a capture during the call. It is nil for any other loan.
	via *closureRun
	// `seed` marks a closure's capture the body holds on entry, which seedCaptureLoans makes.
	// It records only what a call of the closure uses, so no rule checks against it.
	seed bool
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
	return &liveLoan{ev: ev, holders: map[string]heldPlace{}, slots: set.NewSet[callSlot](), ownSlots: set.NewSet[callSlot]()}
}

// clone returns a copy of l that shares no map with it.
func (l *liveLoan) clone() *liveLoan {
	cp := *l
	cp.holders = maps.Clone(l.holders)
	cp.slots = l.slots.Clone()
	cp.ownSlots = l.ownSlots.Clone()
	return &cp
}

// sameHolders reports whether l and o have the same holders, each made by the same flow and
// holding the borrow the same way.
func (l *liveLoan) sameHolders(o *liveLoan) bool {
	return maps.EqualFunc(l.holders, o.holders, func(a, b heldPlace) bool { return a.node == b.node && a.own == b.own })
}

// ownAt reports whether p is a holder of l whose value is l's borrow itself.
func (l *liveLoan) ownAt(p movePlace) bool {
	h, ok := l.holders[placeKey(p)]
	return ok && h.own
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
				prev.closureBorrowed = prev.closureBorrowed || m.closureBorrowed
				out.moved[k] = prev
				continue
			}

			out.moved[k] = m
		}

		for k, l := range s.loans {
			if prev, ok := out.loans[k]; ok {
				for k, h := range l.holders {
					ph, held := prev.holders[k]
					if !held {
						prev.holders[k] = h
						continue
					}
					// A place holds the borrow itself only when it does on every path.
					own := h.own && ph.own
					if laterNode(h.node, ph.node) {
						ph = h
					}
					ph.own = own
					prev.holders[k] = ph
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
	// `events` maps each CFG point to the events attached to it, in emission order.
	events map[liveness.StmtRef][]ownershipEvent
	// `points` lists the CFG points of each block that have events, in program order.
	points map[int][]liveness.StmtRef
	// `order` maps each borrow event of the body, and each borrow derivedLoan makes, to its
	// position in the order loans are reported in. A derived borrow sorts after the borrows
	// the table emits before its copy.
	order map[*borrowEvent]loanOrder
	// copyIndex maps each copy event of the body to its index in the table.
	copyIndex map[*copyEvent]int
	// `derived` holds the borrow event of each loan a copy derives, so each fixed-point
	// iteration derives the same loan.
	derived map[derivedKey]*borrowEvent
	// derivedFrom maps the borrow event of each loan a copy derives from another loan to
	// the origin derivedKey names for it.
	derivedFrom map[*borrowEvent]*borrowEvent
	// `errs` collects the diagnostics of the reporting sweep. It is nil while the pass is
	// still iterating to a fixed point, and nothing is reported then.
	errs *[]SolverError
	// `blamed` holds the span of each diagnostic in `errs`. One expression can emit several
	// events, such as the move of each field `val {f, g} = p` takes, and the expression is
	// reported once.
	blamed set.Set[ast.Span]
	// indexSegment is the checker's indexSegment, which decides whether an index adds a
	// segment to a place the way exprPlace decides it.
	indexSegment func(ast.Expr) (string, bool)
	// `suspends` holds each CFG point whose statement yields or awaits, where the caller runs
	// and can read what the body stored into a caller-owned place.
	suspends set.Set[liveness.StmtRef]
	// `declares` maps each CFG point whose statement is a `val` or `var` declaration to the
	// bindings it declares.
	declares map[liveness.StmtRef][]liveness.VarID
	// `reaches` caches, for each question placeReaches answers, whether a walk from the entry
	// of each block finds what it asks for, indexed by block ID.
	reaches map[reachKey][]bool
	// `escapes` caches the answers of rootEscapes.
	escapes map[liveness.VarID]bool
	// runIndex maps each runEvent of the body to its index in the table.
	runIndex map[*runEvent]int
	// runLoans holds the borrow event of each loan a call takes for a retained closure's
	// write, so each fixed-point iteration takes the same loan.
	runLoans map[runLoanKey]*borrowEvent
	// `captures` maps each closure written in the body to the events of its captures that
	// give it a value to hold. Each is a borrowEvent, or a valueCopyEvent of a captured local
	// whose value is a value type, such as another closure.
	captures map[ast.Func][]flowEvent
	// storesAt maps each CFG point to the retained stores made there.
	storesAt map[liveness.StmtRef][]*retainedStore
	// ownedMovable is the checker's isOwnedMovable, which decides whether a local a retained
	// closure writes is owned.
	ownedMovable func(soltype.Type) bool
	// writesGrew is set when noteStoreWrites adds a local to the `writes` of a retained store.
	writesGrew bool
	// capturedAt maps each local a closure of the body captures to the CFG points where a
	// closure captures it. Liveness does not count a capture as a use of the local.
	capturedAt map[liveness.VarID][]liveness.StmtRef
}

// runLoanKey names the loan a call takes for a retained closure's write of one local.
type runLoanKey struct {
	run  *runEvent
	root liveness.VarID
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
		derivedFrom:  map[*borrowEvent]*borrowEvent{},
		indexSegment: c.indexSegment,
		suspends:     suspendPoints(t.cfg),
		declares:     liveness.DeclaredBindings(t.cfg),
		reaches:      map[reachKey][]bool{},
		escapes:      map[liveness.VarID]bool{},
		runIndex:     map[*runEvent]int{},
		runLoans:     map[runLoanKey]*borrowEvent{},
		captures:     map[ast.Func][]flowEvent{},
		storesAt:     map[liveness.StmtRef][]*retainedStore{},
		ownedMovable: c.isOwnedMovable,
		capturedAt:   map[liveness.VarID][]liveness.StmtRef{},
	}
	for i, ev := range t.events {
		switch ev := ev.(type) {
		case *borrowEvent:
			d.order[ev] = loanOrder{event: i}
			if closure, ok := ev.node.(ast.Func); ok {
				d.captures[closure] = append(d.captures[closure], ev)
				d.capturedAt[ev.place.root] = append(d.capturedAt[ev.place.root], ev.at)
			}
		case *valueCopyEvent:
			if closure, ok := ev.node.(ast.Func); ok {
				d.captures[closure] = append(d.captures[closure], ev)
				d.capturedAt[ev.place.root] = append(d.capturedAt[ev.place.root], ev.at)
			}
		case *copyEvent:
			d.copyIndex[ev] = i
		case *runEvent:
			d.runIndex[ev] = i
		}
	}
	// A seed sorts before every loan the body's events take.
	for i, seed := range t.seeds {
		d.order[seed] = loanOrder{event: -1, derived: i}
	}
	for _, store := range t.retained {
		d.storesAt[store.at] = append(d.storesAt[store.at], store)
	}

	pointSet := set.NewSet[liveness.StmtRef]()
	for at := range d.events {
		pointSet.Add(at)
	}
	for at := range d.suspends {
		pointSet.Add(at)
	}
	for at := range d.declares {
		pointSet.Add(at)
	}
	for at := range d.storesAt {
		pointSet.Add(at)
	}
	for at := range pointSet {
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

		if out[b.ID] == nil || !out[b.ID].equals(s) {
			out[b.ID] = s
			for _, succ := range b.Successors {
				if !queued.Contains(succ.ID) {
					queued.Add(succ.ID)
					worklist = append(worklist, succ)
				}
			}
		}

		// A call reads the `writes` of each retained store it may run. When a store's writes
		// grew, each block the walk reached is walked again, so a call that ran before the
		// store's writes were known sees them.
		if len(worklist) == 0 && d.writesGrew {
			d.writesGrew = false
			for _, rb := range blocks {
				if in[rb.ID] != nil {
					queued.Add(rb.ID)
					worklist = append(worklist, rb)
				}
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
// in. A predecessor the worklist has not reached yet contributes nothing. The body's entry
// block also starts with a loan of each seed.
func (d *bodyDataflow) entryState(b *liveness.BasicBlock, out []*dataflowState) *dataflowState {
	var preds []*dataflowState
	for _, p := range b.Predecessors {
		if out[p.ID] != nil {
			preds = append(preds, out[p.ID])
		}
	}

	s := joinStates(preds)
	if b != d.table.cfg.Entry {
		return s
	}

	for _, seed := range d.table.seeds {
		if _, ok := s.loans[seed]; !ok {
			l := newLiveLoan(seed)
			l.seed = true
			d.holdLoan(l, seed.holder, seed.node, true)
			s.loans[seed] = l
		}
	}
	return s
}

// transfer applies the events of `block` to s, point by point.
func (d *bodyDataflow) transfer(block int, s *dataflowState) {
	for _, at := range d.points[block] {
		// A store reads what its value holds before the statement runs.
		for _, store := range d.storesAt[at] {
			d.noteStoreWrites(store, s)
		}

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
// `at`, and drops each loan that cannot be live after it. A loan stays while a holder is used
// again later, or while a caller-owned holder still holds it when the body returns. A
// closure's capture also stays while a later closure captures a holder, as capturedLater
// decides.
func (d *bodyDataflow) endStatement(at liveness.StmtRef, s *dataflowState) {
	for k, l := range s.loans {
		l.slots.Clear()
		l.ownSlots.Clear()
		l.scoped, l.returned, l.reserved = false, false, nil
		if !d.loanLive(l, at, false) && !d.loanEscapes(l, at, false) && !d.capturedLater(l, at) {
			delete(s.loans, k)
		}
	}
}

// capturedLater reports whether l is a closure's capture with a holder that a closure written
// at a point after `at` captures. That closure reaches the holder's closures. In
//
//	val f = fn () { p.x = 2 }
//	val g = fn () { f() }
//
// liveness counts f as dead after its declaration, and f's loan of p stays until g captures f.
func (d *bodyDataflow) capturedLater(l *liveLoan, at liveness.StmtRef) bool {
	if !l.closure() {
		return false
	}
	for _, h := range l.holders {
		for _, cp := range d.capturedAt[h.place.root] {
			if cp != at && stmtReaches(d.table.cfg, at, cp) {
				return true
			}
		}
	}
	return false
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
	case *valueCopyEvent:
		d.copyValue(ev, s)
	case *moveEvent:
		d.move(ev, at, s)
	case *passEvent:
		d.pass(ev, s)
	case *callEvent:
		d.callEnd(ev, at, s)
	case *runEvent:
		d.run(ev, at, s)
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
// immutable borrow of a place strictly above ev.place is used again after the write, or
// while the caller can reach such a borrow once the body returns. A
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
// A write that uses a moved place reports only E1.
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

		if len(m.place.path) < depth && !m.called {
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
		if moved || reported || l.alias || d.statementOnly(l) || l.ev.borrow != immutableBorrow || (!d.loanLive(l, at, false) && !d.loanEscapes(l, at, false)) {
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
	l.static = lifetimeEscapedToStatic(ev.lifetime)
	d.holdLoan(l, ev.holder, ev.node, true)

	// A mutable receiver only reads its place until the call starts, so callEnd checks it
	// then, as liveLoan.reserved describes.
	if rt, ok := ev.holder.target.(receiverTarget); ok && ev.borrow == mutableBorrow {
		l.reserved = rt.call
	} else {
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

// checkNewPath reports E2 when `node` makes the new path `fresh` of the given `kind` to
// `place`, and one of `loans` has the other kind, has a place that overlaps `place`, and is
// live together with `fresh`. The two are live together when both are live in the body, or
// when the caller can reach both once the body returns. `loans` lists the loans to check in
// the order sortedLoans returns them.
//
// A closure's capture is a path in the body only where usedWhereWritten says, since E4 counts
// a capture as used only while the closure runs. The caller can run a closure it reaches at
// any point after the body returns, so a capture of such a closure is a path at the caller.
// In `out.cb = fn () { p.x = 2 }; q.r = &p` with caller-owned `out` and q, the caller can
// call `out.cb` while it reads `q.r`, so `&p` breaks E2.
//
// A path a caller-owned place holds counts as live for the rest of the body, since the caller
// reads such a place once the body returns.
//
// A loan taken at the point `at` counts as live when its holders are live after that point,
// since a holder the statement defines is not live on entry to it. So
// `val t = {p: &mut b.x, q: &b.x}` reports E2 at `&b.x` when a later statement uses t, and
// reports nothing when no later statement uses t.
func (d *bodyDataflow) checkNewPath(place movePlace, kind borrowKind, node ast.Node, at liveness.StmtRef, loans []*liveLoan, fresh *liveLoan) {
	// Where `fresh` is live can take a walk of the CFG, so it is found once a loan needs it.
	found := false
	var local, escapes bool
	freshInBody := fresh.ev == nil || d.pathInBody(fresh, at)
	for _, first := range loans {
		firstInBody := d.pathInBody(first, at)
		if !(firstInBody || first.closure()) || first.seed || first.reserved != nil || first.ev.borrow == kind ||
			!placesOverlap(first.ev.place, place) {
			continue
		}

		if !found {
			local, escapes, found = freshInBody, d.loanEscapes(fresh, at, false), true
		}
		firstLocal := firstInBody && (d.loanLive(first, at, true) || (first.ev.at == at && d.loanLive(first, at, false)))
		if (local && firstLocal) || (escapes && d.loanEscapes(first, at, true)) {
			err := &BorrowAliasError{
				Place:      d.placeName(place),
				FirstPlace: d.placeName(first.ev.place),
				FirstMut:   first.ev.borrow == mutableBorrow,
				SecondMut:  kind == mutableBorrow,
				node:       node,
				first:      first.ev.node.Span(),
			}
			if first.via != nil {
				err.FirstVia, err.FirstRuns = first.via.via, first.via.runs
			}
			d.report(err)
			return
		}
	}
}

// pathInBody reports whether the rules check a path in the body against l at the point `at`.
// That is a loan liveLoan.checked accepts, or a closure's capture where usedWhereWritten says.
func (d *bodyDataflow) pathInBody(l *liveLoan, at liveness.StmtRef) bool {
	return l.checked() || d.usedWhereWritten(l, at)
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
// loan a place overlapping ev.place holds, as `val c = a` does after `val a = &x`. Such a
// copy into a call argument or receiver also takes a loan through the borrow at ev.place for
// its statement. A copy into a destination of the other kind, or into a `'static` one,
// instead derives loans of that kind and lifetime for the holder alone:
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
	d.holdLoan(path, ev.holder, ev.node, true)
	path.static = path.static || soltype.IsStaticLifetime(ev.lifetime)
	d.checkNewPath(ev.place, ev.borrow, ev.node, at, through, path)

	_, toModule := ev.holder.target.(moduleTarget)
	if ev.borrow == ev.source && !soltype.IsStaticLifetime(ev.lifetime) && !toModule {
		held := false
		for _, l := range loans {
			held = held || (l.heldAt(ev.place) && !l.closure())
		}

		d.addHolder(ev.place, ev.holder, ev.node, s)

		// A call argument is a new path through ev.place for the rest of its statement. In
		// `writeRead(m, m)` with `m: &mut {x: number}` and a `&` second parameter, the second
		// argument's `&` path then conflicts with the first's `&mut` one, as it does when the
		// parameters come in the other order.
		switch ev.holder.target.(type) {
		case argTarget, receiverTarget:
			d.derivedLoan(ev, nil, s)
			return
		}

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
			d.holdLoan(l, ev.holder, ev.node, l.ownAt(ev.place))
			continue
		}

		// A `'static` loan of l's place stays live, so the derived path conflicts with one of
		// the other kind even when the source never uses its own loan again. After
		// `val d: &'static mut {x: number} = &mut p`, the copy `val r: &{x: number} = d` makes
		// an immutable path to p while p has a `'static` mutable one.
		static := slices.DeleteFunc(slices.Clone(loans), func(o *liveLoan) bool {
			return !o.static || o.through != l.through
		})
		d.checkNewPath(l.ev.place, ev.borrow, ev.node, at, static, path)
		d.derivedLoan(ev, l, s)
	}
}

// derivedKey names a loan a copy derives: the copy, and the origin of the loan it derives
// from, or nil for the loan through the copied borrow. The origin of a loan derived from
// another loan is that loan's origin. The origin of any other loan is its own borrow event.
// A loan has the place of its origin, so a copy derives one loan from all loans that share
// an origin. That keeps the loans of a copy in a loop, such as `a = a` with a `'static`
// destination, from growing on each iteration.
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

// derivedLoan adds to s the loan the copy ev derives from the loan `from`, and returns it. A
// nil `from` derives the loan through the borrow at ev.place. The loan has the place of
// `from`, or ev.place when `from` is nil, and the kind and lifetime ev gives its holder.
// ev.holder holds it, as the borrow itself when `from` is nil or ev.place holds `from` as its
// borrow itself.
func (d *bodyDataflow) derivedLoan(ev *copyEvent, from *liveLoan, s *dataflowState) *liveLoan {
	key := derivedKey{copied: ev}
	place, through := ev.place, true
	if from != nil {
		key.from = from.ev
		if origin, derived := d.derivedFrom[from.ev]; derived {
			key.from = origin
		}
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
		if key.from != nil {
			d.derivedFrom[bev] = key.from
		}
		d.order[bev] = loanOrder{event: d.copyIndex[ev], derived: len(d.derived)}
	}

	l := newLiveLoan(bev)
	l.static, l.through = soltype.IsStaticLifetime(bev.lifetime), through
	s.loans[bev] = l
	d.holdLoan(l, ev.holder, ev.node, from == nil || from.ownAt(ev.place))
	return l
}

// copyValue applies the valueCopyEvent ev. The holder becomes a holder of each closure's
// capture held at ev.place or under it, at the same path under the holder. After
// `val g = f`, a call of g runs the closure f holds. A loan held above ev.place belongs to a
// value that ev.place lies inside, which the copy does not take.
func (d *bodyDataflow) copyValue(ev *valueCopyEvent, s *dataflowState) {
	for _, l := range s.loans {
		if !l.closure() {
			continue
		}

		// The loop below adds holders to l, so the holders under ev.place are found first.
		var under []heldPlace
		for _, h := range l.holders {
			if h.place.root == ev.place.root && pathHasPrefix(h.place.path, ev.place.path) {
				under = append(under, h)
			}
		}

		for _, h := range under {
			d.holdLoan(l, ev.holder.extend(h.place.path[len(ev.place.path):]), ev.node, h.own)
		}
	}
}

// move applies the move ev. E1 rejects a move of a moved place, and E3 rejects a move while
// a borrow of an overlapping place is live. A return that moves a closure's capture hands
// over the closure's own borrow of the place too, so that borrow is no conflict. A move
// ev.whileCaptured marks applies only while that borrow is live.
//
// A borrow the caller can reach once the body returns, such as one stored into a caller-owned
// place, is live at the caller and so for the rest of the body. E3 rejects every move of an
// overlapping place after such a borrow is taken. In `out.r = &p; val q = p`, the move of p
// into q is rejected whether or not q leaves the body.
//
// The error blames the first conflicting borrow sortedLoans returns that is not a retained
// closure's write during a call. Such a write is blamed only when nothing else conflicts, and
// the error then names the closure. The moved place records whether any conflicting borrow is
// a closure's capture, as movedPlace.closureBorrowed describes.
func (d *bodyDataflow) move(ev *moveEvent, at liveness.StmtRef, s *dataflowState) {
	if ev.whileCaptured && !d.capturesPlace(ev.closure, ev.place, s) {
		return
	}

	closureBorrowed := false
	if !d.checkMoved(ev.place, ev.node, s) {
		var blamed, via *liveLoan
		for _, l := range d.sortedLoans(s) {
			own := ev.closure != nil && l.ev.node == ev.closure
			if own || l.alias || l.seed || !placesOverlap(l.ev.place, ev.place) {
				continue
			}
			if !d.loanLive(l, at, true) && !d.loanEscapes(l, at, false) {
				continue
			}

			// Pick the borrow to blame. A loan whose `via` is set stands for the write that a
			// closure the call may run makes during the call. Such a loan is kept in `via` and is
			// blamed only when no ordinary borrow conflicts. An ordinary borrow is blamed first
			// because it points at the expression that took it. In
			//
			//	onClick(fn () { count.n = count.n + 1 })
			//	take(count)
			//
			// the only conflict is the closure's write, and the error reads "cannot move 'count'
			// in a call that may run the 'handler' that 'onClick' retains, which writes it".
			// Each case keeps the first loan of its kind in sortedLoans order.
			closureBorrowed = closureBorrowed || l.closure()
			switch {
			case l.via == nil && blamed == nil:
				blamed = l
			case l.via != nil && via == nil:
				via = l
			}
		}

		// Report at most one error for the move. The error for a closure's write names the
		// closure and says whether the call surely runs it.
		switch {
		case blamed != nil:
			d.report(&MoveWhileBorrowedError{
				Place:  d.placeName(ev.place),
				move:   ev.node,
				borrow: blamed.ev.node.Span(),
			})
		case via != nil:
			d.report(&MoveWhileBorrowedError{
				Place:   d.placeName(ev.place),
				Via:     via.via.via,
				ViaRuns: via.via.runs,
				move:    ev.node,
				borrow:  via.ev.node.Span(),
			})
		}
	}

	s.moved[placeKey(ev.place)] = movedPlace{place: ev.place, node: ev.node, every: true, closureBorrowed: closureBorrowed}
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
// tuple once every path has moved every element a tuple pattern names over it. place is the
// place just moved, and node is the expression that moved it. `val [x, y] = b` moves `b.0`
// and `b.1`, and then a later use of b reads a moved value rather than a partially moved one.
//
// An element that only some paths moved keeps its own move. Two paths that each move a
// different element leave the tuple partially moved on both, so merging their moves would
// report a whole move no path made. Merging only moves made on every path also lets the
// loop iteration in checkBodyDataflow converge. A move that only a loop's back edge carries
// is not made on every path into the loop, so it never decides whether a path merges.
func (d *bodyDataflow) mergeTupleMove(place movePlace, node ast.Node, s *dataflowState) {
	for len(place.path) > 0 {
		tuple := movePlace{root: place.root, path: place.path[:len(place.path)-1]}
		parts := d.table.tupleParts[placeKey(tuple)]
		if len(parts) == 0 {
			return
		}

		keys := make([]string, len(parts))
		for i, seg := range parts {
			keys[i] = placeKey(movePlace{root: tuple.root, path: append(slices.Clip(tuple.path), seg)})
			if m, moved := s.moved[keys[i]]; !moved || !m.every {
				return
			}
		}

		for _, k := range keys {
			delete(s.moved, k)
		}
		s.moved[placeKey(tuple)] = movedPlace{place: tuple, node: node, every: true}
		place = tuple
	}
}

// holdLoan records `holder` as a holder of l, through the flow whose expression is `node`.
// `own` marks a holder whose value is l's borrow itself, as heldPlace.own describes.
func (d *bodyDataflow) holdLoan(l *liveLoan, holder eventHolder, node ast.Node, own bool) {
	switch tg := holder.target.(type) {
	case placeTarget:
		p := movePlace{root: tg.root, path: holder.path}
		l.holders[placeKey(p)] = heldPlace{place: p, node: node, own: own}
	case moduleTarget:
		l.static = true
	case argTarget:
		l.holdSlot(callSlot{call: tg.call, index: tg.index}, own && len(holder.path) == 0)
	case receiverTarget:
		l.holdSlot(callSlot{call: tg.call, index: selfIndex}, own && len(holder.path) == 0)
	case returnTarget:
		l.scoped, l.returned = true, true
	case tempTarget:
		l.scoped = true
	}
}

// holdSlot records `slot` as a holder of l. `own` marks a slot whose value is l's borrow itself.
func (l *liveLoan) holdSlot(slot callSlot, own bool) {
	l.slots.Add(slot)
	if own {
		l.ownSlots.Add(slot)
	}
}

// addHolder makes `holder` a holder of each loan a place overlapping src holds, since
// `holder` now reaches what src borrows. `node` is the expression of the flow. A loan held
// under src lands at the same path under `holder`, so `val c = a` with a loan held at `a.peer` makes
// `c.peer` its holder. A holder at or under src keeps whether it holds the borrow itself. A
// holder above src holds the borrow whose referent src lies in, so `holder` does not hold that
// borrow itself.
func (d *bodyDataflow) addHolder(src movePlace, holder eventHolder, node ast.Node, s *dataflowState) {
	type heldUnder struct {
		rest []placeSeg
		own  bool
	}
	for _, l := range s.loans {
		var under []heldUnder
		for _, h := range l.holders {
			if placesOverlap(h.place, src) {
				under = append(under, heldUnder{
					rest: h.place.path[min(len(src.path), len(h.place.path)):],
					own:  h.own && len(h.place.path) >= len(src.path),
				})
			}
		}

		for _, u := range under {
			d.holdLoan(l, holder.extend(u.rest), node, u.own)
		}
	}
}

// pass applies the passEvent ev. Each loan the slot ev.from holds gains ev.holder as a holder.
// A passEvent whose `direct` is false skips each loan the slot holds as its own borrow, as
// liveLoan.ownSlots records, unless the slot also holds a mutable loan inside its value.
//
// A store through a target argument that names a borrow, such as the `a` of `store(a, &b)`
// after `val a = &mut x`, lands in the place that borrow reaches, so `x.peer` holds the loan
// too.
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
	// what the referent holds and not the referent itself. An immutable borrow the referent holds
	// is copied out whole. A mutable one cannot be copied, so the callee reborrows it through
	// the slot's own borrow, and the holder keeps that borrow too.
	skipOwn := !ev.direct
	for _, l := range s.loans {
		if skipOwn && l.slots.Contains(ev.from) && !l.ownSlots.Contains(ev.from) && l.ev.borrow == mutableBorrow {
			skipOwn = false
		}
	}

	for _, l := range s.loans {
		if !l.slots.Contains(ev.from) || (skipOwn && l.ownSlots.Contains(ev.from)) {
			continue
		}
		// The result or the store target receives what the callee hands back, which need not
		// be the borrow itself.
		for _, h := range holders {
			d.holdLoan(l, h, ev.node, false)
		}
	}
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
	// Step 1 checks each receiver this call reserved, scanning the loans in sorted order.
	// Most calls reserve no receiver, and those skip the sort.
	var loans []*liveLoan
	for _, l := range s.loans {
		if l.reserved == ev.call {
			loans = d.sortedLoans(s)
			break
		}
	}

	for _, r := range loans {
		if r.reserved != ev.call {
			continue
		}

		// The call starts here, so the receiver's mutable path now conflicts with an immutable
		// path like any other mutable loan.
		r.reserved = nil

		// `loans` is in the order sortedLoans returns, which follows the order the borrows were
		// taken. `after` turns true once the scan reaches r, so each loan from then on was
		// taken after r.
		after := false
		for _, m := range loans {
			after = after || m == r

			// Only a live immutable loan of an overlapping place conflicts, and an `alias` loan or
			// a closure's capture never does. A loan is live here when a call that has not
			// returned holds it, or when a holder is used later in the statement or after it.
			if m == r || !m.checked() || m.ev.borrow == mutableBorrow || !placesOverlap(m.ev.place, r.ev.place) ||
				(!d.loanLive(m, at, false) && !d.usedLaterAt(m, ev, at)) {
				continue
			}

			// The later of the two paths is the one blamed. One report per receiver is enough.
			first, second := m, r
			if after {
				first, second = r, m
			}
			d.report(&BorrowAliasError{
				Place:      d.placeName(second.ev.place),
				FirstPlace: d.placeName(first.ev.place),
				FirstMut:   first == r,
				SecondMut:  second == r,
				node:       second.ev.node,
				first:      first.ev.node.Span(),
			})
			break
		}
	}

	// Step 2 releases every slot of this call. A loan with no holder, no slot of another call,
	// and no hold that lasts to the end of the statement ends here.
	for k, l := range s.loans {
		for slot := range l.slots {
			if slot.call == ev.call {
				l.slots.Remove(slot)
				l.ownSlots.Remove(slot)
			}
		}
		if d.statementOnly(l) && !l.scoped && len(l.slots) == 0 {
			delete(s.loans, k)
		}
	}
}

// `run` applies the runEvent `ev`. The call uses each capture of each closure it runs or may
// run, as E4 states. A capture the closure writes is used as a mutable path, and one it only
// reads as an immutable path:
//
//   - E1 rejects the use of a moved capture.
//   - E2 rejects the path while a live loan of the other kind overlaps it.
//
// A closure that a retainedStore keeps reachable may run at the call, as storeReachable
// decides. The call uses each local it writes the same way, and takes a mutable loan of the
// local that lasts until the end of the statement. So E3 rejects a move of the local later
// in the statement, such as one of the call's own arguments. These loans are taken before the
// captures of the closures the callee and the arguments name are used.
//
// A call whose callee names a binding of a closure that moves one of its captures moves the
// binding, as D2 decides. A second call then uses a moved binding. The error blames the
// callee, or the whole call when an earlier call of the same statement moved the binding,
// as in `f() + f()`.
func (d *bodyDataflow) run(ev *runEvent, at liveness.StmtRef, s *dataflowState) {
	taken := set.NewSet[liveness.VarID]()
	for _, store := range d.table.retained {
		if store.call == ast.Node(ev.call) || !d.storeReachable(store, at) {
			continue
		}

		run := &closureRun{node: ev.call, via: store.via}
		for _, root := range store.writes {
			if taken.Contains(root) {
				continue
			}
			taken.Add(root)

			d.useCapture(ev, run, capturedLocal{root: root, mut: true}, true, at, s)
			l := d.runLoan(ev, root, run)
			s.loans[l.ev] = l
		}
	}

	for _, name := range ev.names {
		run, ok := d.closureRunOf(name, s)
		if !ok {
			continue
		}
		for _, local := range run.captures {
			d.useCapture(ev, run, local, false, at, s)
		}
	}

	if !ev.consumes {
		return
	}

	k := placeKey(ev.callee)
	if m, moved := s.moved[k]; moved && m.called {
		var blame ast.Node = ev.call.Callee
		if m.node != ev.call && slices.ContainsFunc(d.events[at], func(o ownershipEvent) bool {
			r, isRun := o.(*runEvent)
			return isRun && r.call == m.node
		}) {
			blame = ev.call
		}
		d.reportMoved(m, ev.callee, blame)
	}
	s.moved[k] = movedPlace{place: ev.callee, node: ev.call, every: true, called: true}
}

// storeReachable reports whether a call at the point `at` may run the closure `store` keeps.
// The call must come after the store. A closure kept for the rest of the program is reachable
// from every such call, and any other closure only while one of the store's holders is live.
func (d *bodyDataflow) storeReachable(store *retainedStore, at liveness.StmtRef) bool {
	if !stmtReaches(d.table.cfg, store.at, at) {
		return false
	}
	return store.static || slices.ContainsFunc(store.holders, func(h liveness.VarID) bool {
		return d.holderLive(h, at, true)
	})
}

// useCapture checks the use of the captured local by the closure `run` during the call of
// `ev`. The method `run` describes that use. `retained` marks a closure a retainedStore keeps
// reachable. A conflicting borrow is blamed this way:
//
//   - A borrow one of the call's own arguments takes blames that argument when the callee or
//     an argument names the closure.
//   - Such a borrow blames the whole call when a retainedStore keeps the closure.
//   - Any other borrow blames the expression that names the closure.
func (d *bodyDataflow) useCapture(ev *runEvent, run *closureRun, local capturedLocal, retained bool, at liveness.StmtRef, s *dataflowState) {
	places := local.places()
	for _, k := range slices.Sorted(maps.Keys(s.moved)) {
		m := s.moved[k]
		if m.called {
			continue
		}
		// A closure that captures only `p.b` does not read a moved `p.a`, so only a captured
		// place that overlaps the moved one counts as a use of it.
		i := slices.IndexFunc(places, func(p movePlace) bool { return placesOverlap(m.place, p) })
		if i < 0 {
			continue
		}

		if !m.closureBorrowed {
			e := d.movedError(m, places[i], run.node)
			e.Via, e.ViaRuns, e.ViaReads = run.via, run.runs, !local.mut
			d.report(e)
		}
		break
	}

	kind := immutableBorrow
	if local.mut {
		kind = mutableBorrow
	}

	for _, place := range places {
		for _, l := range d.sortedLoans(s) {
			if !l.checked() || l.reserved != nil || l.ev.borrow == kind || !placesOverlap(l.ev.place, place) ||
				!d.loanLive(l, at, true) {
				continue
			}

			if arg, ok := l.ev.holder.target.(argTarget); ok && arg.call == ev.call && !retained {
				d.report(&BorrowAliasError{
					Place:      d.placeName(l.ev.place),
					FirstPlace: d.placeName(place),
					FirstMut:   local.mut,
					SecondMut:  l.ev.borrow == mutableBorrow,
					FirstVia:   run.via,
					FirstRuns:  run.runs,
					node:       l.ev.node,
					first:      run.node.Span(),
				})
				continue
			}

			err := &BorrowAliasError{
				Place:      d.placeName(place),
				FirstPlace: d.placeName(l.ev.place),
				FirstMut:   l.ev.borrow == mutableBorrow,
				SecondMut:  local.mut,
				SecondVia:  run.via,
				SecondRuns: run.runs,
				node:       run.node,
				first:      l.ev.node.Span(),
			}
			if l.via != nil {
				err.FirstVia, err.FirstRuns = l.via.via, l.via.runs
			}
			d.report(err)
		}
	}
}

// runLoan returns the loan the call of `ev` takes for the write of the local `root` by the
// retained closure `run`. It lasts until the end of the statement.
func (d *bodyDataflow) runLoan(ev *runEvent, root liveness.VarID, run *closureRun) *liveLoan {
	key := runLoanKey{run: ev, root: root}
	bev, ok := d.runLoans[key]
	if !ok {
		bev = &borrowEvent{
			flowBase: flowBase{eventBase: eventBase{place: movePlace{root: root}, node: ev.call, at: ev.at}, holder: eventHolder{target: tempTarget{node: ev.call}}},
			borrow:   mutableBorrow,
		}
		d.runLoans[key] = bev
		d.order[bev] = loanOrder{event: d.runIndex[ev]}
	}

	l := newLiveLoan(bev)
	l.scoped, l.via = true, run
	return l
}

// usedWhereWritten reports whether the closure's capture `l` counts as a path at the point
// `at`. Writing a closure that noteBoundClosure did not record uses its captures for the
// statement it is written in, since the expression it sits in may call it there.
func (d *bodyDataflow) usedWhereWritten(l *liveLoan, at liveness.StmtRef) bool {
	closure, ok := l.ev.node.(ast.Func)
	return ok && !l.alias && l.ev.at == at && !d.table.boundClosures.Contains(closure)
}

// usedLaterAt reports whether an event after ev at the point `at` reads a place a holder of l
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

// checkMoved reports a use of `place` at `node` when a place overlapping it is moved, and
// returns whether it reported.
func (d *bodyDataflow) checkMoved(place movePlace, node ast.Node, s *dataflowState) bool {
	for _, k := range slices.Sorted(maps.Keys(s.moved)) {
		m := s.moved[k]
		if !m.called && m.place.root == place.root && pathPrefixRelated(m.place.path, place.path) {
			d.reportMoved(m, place, node)
			return true
		}
	}
	return false
}

// reportMoved reports the use of `place` at `node` after the move m. A use of a place that
// holds the moved place, such as `pair` after `pair.a` moved, is a use of a partially
// moved value.
func (d *bodyDataflow) reportMoved(m movedPlace, place movePlace, node ast.Node) {
	d.report(d.movedError(m, place, node))
}

// movedError returns the error reportMoved reports.
func (d *bodyDataflow) movedError(m movedPlace, place movePlace, node ast.Node) *UseAfterMoveError {
	e := &UseAfterMoveError{Name: d.placeName(m.place), Conditional: !m.every, use: node, moveSite: m.node}
	if len(place.path) < len(m.place.path) {
		e.ReadName = d.placeName(place)
		e.Partial = true
	}
	return e
}

// loanLive reports whether l is live at the point `at`. `before` asks whether it is live on
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

// heldPlaceLive reports whether the holder h is used again after the point `at`, or at it
// when `before` is set. A local holds its loans while liveness says the local is live. A
// caller-owned place holds them while placeReaches finds a later read of it, since the caller
// reads it only once the body returns or suspends.
func (d *bodyDataflow) heldPlaceLive(h heldPlace, at liveness.StmtRef, before bool) bool {
	if !d.holderLive(h.place.root, at, before) {
		return false
	}
	return !d.callerOwned(h) || d.placeReaches(h, at, before, false)
}

// loanEscapes reports whether the caller can reach l once the body returns, from the point
// `at` onward. `before` also counts the events at the point `at`. A `'static` loan escapes, and so
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

// rootEscapes reports whether the value of the local `root` flows into a return, a
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

// reachKey names one question placeReaches answers, whatever point it starts from.
type reachKey struct {
	place string
	node  ast.Node
	exit  bool
}

// placeReaches follows the CFG forward from the point `at` and reports what it finds for the
// holder h. With `exit` unset, it reports whether some path reads a place overlapping h before
// a write replaces h. With `exit` set, it reports whether some path reaches the end of the body
// without such a write. `before` starts the walk at the events of `at` itself.
//
// A write replaces h when it writes h or a place above it with no dynamic index, so
// `p.r = &e` replaces a holder at `p.r` and `p.r.x = 1` reads it. A write replaces h only
// when the flow that made h a holder does not come after it in the source, the way `write`
// ends holders. So in `p.r = if c { &b } else { &e }`, the write of `p.r` after each branch
// keeps the borrow that branch stored.
func (d *bodyDataflow) placeReaches(h heldPlace, at liveness.StmtRef, before, exit bool) bool {
	key := reachKey{place: placeKey(h.place), node: h.node, exit: exit}
	fromEntry, ok := d.reaches[key]
	if !ok {
		fromEntry = d.reachFromEntry(h, exit)
		d.reaches[key] = fromEntry
	}

	from := at.StmtIdx + 1
	if before {
		from = at.StmtIdx
	}
	if found, decided := d.scanForHolder(h, at.BlockID, from, exit); decided {
		return found
	}
	return d.reachAfterBlock(at.BlockID, fromEntry, exit)
}

// reachFromEntry returns, for each block ID, whether a walk of placeReaches for h that starts
// at the entry of that block finds what `exit` asks for. A block finds it when its own points
// decide so, or when they decide nothing and a successor finds it.
func (d *bodyDataflow) reachFromEntry(h heldPlace, exit bool) []bool {
	blocks := d.table.cfg.Blocks
	out := make([]bool, len(blocks))
	undecided := make([]bool, len(blocks))
	var worklist []*liveness.BasicBlock

	// Scan the points of each block on their own, from the block's entry. When `exit` is unset,
	// a block whose points read h finds it. When `exit` is set, the exit block finds it when its
	// points decide nothing, since the walk reaches the end of the body there. Each block that
	// finds it seeds the worklist.
	for _, b := range blocks {
		found, decided := d.scanForHolder(h, b.ID, math.MinInt, exit)
		undecided[b.ID] = !decided
		if found || (!decided && b == d.table.cfg.Exit && exit) {
			out[b.ID] = true
			worklist = append(worklist, b)
		}
	}

	// Spread each found answer backward along the CFG edges. A predecessor whose own points
	// decide nothing finds what its successor finds. A walk ends at the exit block, so the exit block
	// never takes the answer of a successor.
	for len(worklist) > 0 {
		b := worklist[len(worklist)-1]
		worklist = worklist[:len(worklist)-1]
		for _, pred := range b.Predecessors {
			if !out[pred.ID] && undecided[pred.ID] && pred != d.table.cfg.Exit {
				out[pred.ID] = true
				worklist = append(worklist, pred)
			}
		}
	}

	return out
}

// reachAfterBlock reports whether a walk of placeReaches that leaves the block with ID `block`
// without a decision finds what `exit` asks for. fromEntry is the answer of reachFromEntry.
func (d *bodyDataflow) reachAfterBlock(block int, fromEntry []bool, exit bool) bool {
	b := d.table.cfg.Blocks[block]
	if b == d.table.cfg.Exit {
		return exit
	}
	return slices.ContainsFunc(b.Successors, func(succ *liveness.BasicBlock) bool { return fromEntry[succ.ID] })
}

// scanForHolder walks the points of the block with ID `block` from the statement index `from`
// on, for the walk placeReaches makes for h. `decided` reports whether a point settles the
// answer, and `found` is that answer. A read of a place overlapping h settles it as found when
// `exit` is unset. A write that replaces h, as placeReaches describes, settles it as not found.
// A yield or an await settles it as found when h is caller-owned, since the caller runs there
// and can read h.
func (d *bodyDataflow) scanForHolder(h heldPlace, block, from int, exit bool) (found, decided bool) {
	for _, ref := range d.points[block] {
		if ref.StmtIdx < from {
			continue
		}
		if d.suspends.Contains(ref) && d.callerOwned(h) {
			return true, true
		}
		switch d.holderEffect(h, ref) {
		case 1:
			if !exit {
				return true, true
			}
		case -1:
			return false, true
		}
	}
	return false, false
}

// holderEffect returns 1 when the events at `ref` read h, -1 when they write a place that
// replaces h, and 0 otherwise. The first event that touches h decides.
func (d *bodyDataflow) holderEffect(h heldPlace, ref liveness.StmtRef) int {
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

// suspendPoints returns the CFG point of each statement of cfg that yields or awaits outside
// a nested function, and the point before the first statement of each block that
// BasicBlock.Suspends marks.
func suspendPoints(cfg *liveness.CFG) set.Set[liveness.StmtRef] {
	out := set.NewSet[liveness.StmtRef]()
	for _, b := range cfg.Blocks {
		if b.Suspends {
			out.Add(liveness.StmtRef{BlockID: b.ID, StmtIdx: -1})
		}
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

// suspendFinder walks a statement for suspendPoints and sets `found` at a yield or an await.
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

// holderLive reports whether the local h is used again after the point `at`, or at it
// when `before` is set. A point outside the liveness tables is not live.
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

// placeName renders p for a diagnostic as the root's name followed by its field path.
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
