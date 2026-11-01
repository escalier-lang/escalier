package solver

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// This file emits the ownership events of the checking model in
// planning/ownership_rules/requirements.md. Each event is attached to a node of the
// control-flow graph the liveness package builds for a body. A node is a statement,
// named by its liveness.StmtRef. The walk emits the events while it infers, since only
// the walk knows the type of each destination, and F2 needs that type to choose between
// a copy, a borrow, and a move.
//
// The holder of a copy, borrow, or move is the destination that receives the value, such
// as the binding `d` in `val d = c`. A flow of a value type, such as `val a = n` with
// `n: number`, makes no holder of a borrow, so it is recorded as a read of its source.
//
// A flow whose destination or source F3 has not settled yet is a provisional flow. The
// walk records a placeholder for it, and settleFlowsWaitingOn replaces the placeholder
// with the copy, borrow, or move F2 gives once F3 is settled. Three kinds of flow are
// provisional:
//
//   - A flow out of an unannotated parameter of a body still being walked. The body
//     decides whether the parameter is owned or a borrow.
//   - A flow into a parameter of a callee whose body is still being walked, or whose
//     signature the call cannot see yet.
//   - A closure capture. A closure that is returned or stored into module-level state
//     owns what it captures, and any other closure borrows it. A closure a local holds
//     may be returned through that local later, so the capture waits until the body
//     holding the closure is walked.

// ownershipEventKind is the kind of one ownership event.
type ownershipEventKind uint8

const (
	// copyEvent gives the holder the same value as the place, which stays usable.
	copyEvent ownershipEventKind = iota
	// borrowEvent gives the holder a new borrow of the place.
	borrowEvent
	// moveEvent gives the holder ownership of the place, which is consumed.
	moveEvent
	// useEvent reads or writes the place.
	useEvent
	// provisionalEvent holds the slot of a provisional flow until F3 settles it. No
	// event of this kind survives settleAllFlows.
	provisionalEvent
)

// borrowKind is the mutability of a borrow: `&` or `&mut`.
type borrowKind uint8

const (
	immutableBorrow borrowKind = iota
	mutableBorrow
)

// useAccess says whether a use event reads or writes its place.
type useAccess uint8

const (
	readAccess useAccess = iota
	writeAccess
)

// holderKind is the kind of destination that receives a flowing value.
type holderKind uint8

const (
	// placeHolder is a local binding or a field path rooted at one, such as `q` or `o.f`.
	placeHolder holderKind = iota
	// moduleHolder is a module-level binding, named by name.
	moduleHolder
	// argHolder is argument index of the call node.
	argHolder
	// receiverHolder is the receiver of the method call node.
	receiverHolder
	// returnHolder is the value the function returns.
	returnHolder
	// tempHolder is a value no flow site receives, such as the literal in
	// `({f: &p}).f`. node is that value's expression. A literal element or a closure
	// capture starts out held by the literal or closure, and the flow site that receives
	// the literal or closure retargets it to its own holder.
	tempHolder
)

// eventHolder is the destination that receives the value of a copy, borrow, or move.
// place holds the binding and field path of a placeHolder. For every other kind
// place.root is 0 and place.path is the field path within the destination, so the
// element `p` of `return {f: p}` is held at the path [f] of the return.
type eventHolder struct {
	kind  holderKind
	place movePlace
	name  string
	node  ast.Node
	index int
}

// extend returns h with the path segments appended to its place's path.
func (h eventHolder) extend(path []placeSeg) eventHolder {
	for _, seg := range path {
		h.place = extendPlace(h.place, seg.name)
	}
	return h
}

// ownershipEvent is one event of the checking model. place is the place a copy,
// borrow, or move takes its value from, or the place a use reads or writes. holder
// receives the value of a copy, borrow, or move. borrow and lifetime describe a borrow
// event's borrow, and lifetime is nil when the borrow's type carries none. access says
// what a use does. node is the expression to blame for the event. at is the CFG node
// the event is attached to. fromRead marks a use that reading the expression node emitted.
// A later event that reads the same expression replaces such a use.
type ownershipEvent struct {
	kind     ownershipEventKind
	place    movePlace
	holder   eventHolder
	borrow   borrowKind
	lifetime soltype.Lifetime
	access   useAccess
	node     ast.Node
	at       liveness.StmtRef
	fromRead bool
}

// ownershipEvents holds the ownership events of one body, in the order the walk emitted
// them. names maps each VarID of the body to its source name, for rendering.
type ownershipEvents struct {
	events []ownershipEvent
	names  map[liveness.VarID]string
	// subsumed holds each read expression whose value another event already accounts
	// for, such as the `c` of `val d = c`, whose copy event reads it. The use event that
	// reading a subsumed expression emitted is dropped when the table is read.
	subsumed set.Set[ast.Node]
	// emitted holds each expression whose events are emitted, with the CFG point they attach
	// to. The walk types a `match` guard once per path that reaches it, and the guard's
	// events are emitted once.
	emitted set.Set[emitSite]
	// temps maps the expression of each tempHolder to the indices of the events it holds.
	temps map[ast.Node][]int
	// escaping holds the root of each place whose value flows into a return or a
	// module-level binding. flowsInto maps the root of each place whose value flows into a
	// local place to the roots of those places. A closure held by a place that reaches
	// escaping through flowsInto outlives the frame. See escapes.
	escaping  set.Set[liveness.VarID]
	flowsInto map[liveness.VarID][]liveness.VarID
}

// escapes reports whether the value held by the place rooted at root flows into a return
// or a module-level binding, directly or through other local places.
func (t *ownershipEvents) escapes(root liveness.VarID) bool {
	seen := set.NewSet[liveness.VarID]()
	stack := []liveness.VarID{root}
	for len(stack) > 0 {
		r := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if t.escaping.Contains(r) {
			return true
		}
		if seen.Contains(r) {
			continue
		}
		seen.Add(r)
		stack = append(stack, t.flowsInto[r]...)
	}
	return false
}

// emitSite is one expression, the role it emits events in, and the CFG point they attach
// to. One expression can emit in two roles, such as the `q` of `{b: q}`, which is read and
// is also a literal element. root tells apart the captures one closure emits, and is 0 for
// any other expression.
type emitSite struct {
	node ast.Node
	role emitRole
	at   liveness.StmtRef
	root liveness.VarID
}

// emitRole is the role in which an expression emits events.
type emitRole uint8

const (
	readRole emitRole = iota
	borrowRole
	elementRole
	captureRole
	callRole
	assignRole
	bindingRole
	patternRole
	returnRole
)

// firstEmit records that node emits its events in role at the CFG point at, and reports
// whether it had not done so before. root names the captured local when node is a closure.
func (c *checker) firstEmit(node ast.Node, role emitRole, at liveness.StmtRef, root liveness.VarID) bool {
	site := emitSite{node: node, role: role, at: at, root: root}
	if c.fn.events.emitted.Contains(site) {
		return false
	}
	c.fn.events.emitted.Add(site)
	return true
}

// newOwnershipEvents returns an empty table whose places render through names.
func newOwnershipEvents(names map[liveness.VarID]string) *ownershipEvents {
	return &ownershipEvents{
		names:     names,
		subsumed:  set.NewSet[ast.Node](),
		emitted:   set.NewSet[emitSite](),
		temps:     map[ast.Node][]int{},
		escaping:  set.NewSet[liveness.VarID](),
		flowsInto: map[liveness.VarID][]liveness.VarID{},
	}
}

// list returns the table's events in emission order, without the use events of
// subsumed reads.
func (t *ownershipEvents) list() []ownershipEvent {
	out := make([]ownershipEvent, 0, len(t.events))
	for _, ev := range t.events {
		if ev.fromRead && t.subsumed.Contains(ev.node) {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// byNode returns the table's events grouped by the CFG node they are attached to. Each
// node's events keep their emission order.
func (t *ownershipEvents) byNode() map[liveness.StmtRef][]ownershipEvent {
	out := map[liveness.StmtRef][]ownershipEvent{}
	for _, ev := range t.list() {
		out[ev.at] = append(out[ev.at], ev)
	}
	return out
}

// sourceKind classifies the value a flow takes from its source, the row of F2's table.
type sourceKind uint8

const (
	valueSource sourceKind = iota
	borrowSource
	ownedSource
)

// destKind classifies a flow's destination, the column of F2's table.
type destKind uint8

const (
	// ownedDest takes ownership.
	ownedDest destKind = iota
	// borrowDest takes a borrow of the kind and lifetime flowDest names.
	borrowDest
	// inferredReturnDest is a return with no annotation. It takes ownership of an owned
	// value and carries a borrow on as one, as F3's `make` and `same` show.
	inferredReturnDest
	// paramDest is the parameter flowDest names, whose ownership the callee's body decides.
	paramDest
	// calleeDest is the argument index of the call flowDest names, whose callee's
	// signature the call could not see.
	calleeDest
	// captureDest is a closure capture. F3 decides it from where the closure flows.
	captureDest
)

// flowDest describes the destination of one flow site. borrow and lt describe a
// borrowDest. param names a paramDest's parameter, and call and index a calleeDest's
// argument. reborrows marks a destination that borrows a borrow source again rather than
// copying it, which F4 gives a method receiver and a pattern over a borrow. writes marks
// a captureDest whose closure writes the captured local. callLifetime, when set on a
// paramDest, is the lifetime a borrow into the parameter takes in place of the one the
// parameter declares.
type flowDest struct {
	kind         destKind
	borrow       borrowKind
	lt           soltype.Lifetime
	param        *soltype.FuncParam
	call         *ast.CallExpr
	index        int
	reborrows    bool
	writes       bool
	callLifetime soltype.Lifetime
}

// destOfType returns the destination a flow into a slot of type t has: a borrow
// destination when t is a borrow, and an owned one otherwise.
func (c *checker) destOfType(t soltype.Type) flowDest {
	if r, ok := c.borrowOf(t); ok {
		return flowDest{kind: borrowDest, borrow: borrowKindOf(r.Mut), lt: r.Lt}
	}
	return flowDest{kind: ownedDest}
}

// borrowOf returns the borrow t is. A union is a borrow when each member that is not a
// value type is a borrow, as `&{x: number} | undefined` is, and borrowOf returns its first
// such member.
func (c *checker) borrowOf(t soltype.Type) (*soltype.RefType, bool) {
	if r, ok := t.(*soltype.RefType); ok && r.Lt != nil {
		return r, true
	}
	u, ok := c.expandAliasChain(t).(*soltype.UnionType)
	if !ok {
		return nil, false
	}
	var first *soltype.RefType
	for _, member := range u.Types {
		if c.holdsValueType(member) {
			continue
		}
		r, isRef := member.(*soltype.RefType)
		if !isRef || r.Lt == nil {
			return nil, false
		}
		if first == nil {
			first = r
		}
	}
	return first, first != nil
}

// borrowKindOf returns `&mut` for a mutable borrow and `&` otherwise.
func borrowKindOf(mut bool) borrowKind {
	if mut {
		return mutableBorrow
	}
	return immutableBorrow
}

// provisionalFlow is a flow F3 has not settled yet. table holds the placeholder event at
// index. source is the source expression, read for its type once the flow settles. A
// closure capture and a destructured part have no source expression of their own, so
// source is nil and sourceT holds the type of the value that flows.
type provisionalFlow struct {
	table   *ownershipEvents
	index   int
	source  ast.Expr
	sourceT soltype.Type
	dest    flowDest
}

// flowWait names what a provisional flow waits on. param is an unannotated parameter whose
// body is still being walked. capture marks a closure capture that waits for the closure's
// flow site, and table is the table of the body the closure is written in. callee marks an
// argument whose callee's signature the call could not see, which waits for the module or
// script to be inferred.
type flowWait struct {
	param   *soltype.FuncParam
	capture bool
	table   *ownershipEvents
	callee  bool
}

// ownershipEventsOf returns the ownership events of the body of fn, or nil when no body
// of fn was walked. A script's top level is keyed by a nil fn.
func (i *Info) ownershipEventsOf(fn ast.Node) *ownershipEvents {
	return i.ownership[fn]
}

// startOwnershipEvents opens the event table of the body about to be walked and records
// it in Info under the body's function node. names maps the body's VarIDs to their names.
func (c *checker) startOwnershipEvents(names map[liveness.VarID]string) {
	t := newOwnershipEvents(names)
	c.fn.events = t
	if c.info.ownership == nil {
		c.info.ownership = map[ast.Node]*ownershipEvents{}
	}
	c.info.ownership[c.fn.node] = t
}

// emitEvent appends ev to the current body's table, attached to the CFG node at, and
// returns its index. It returns -1 outside a body.
func (c *checker) emitEvent(ev ownershipEvent, at liveness.StmtRef) int {
	if c.fn == nil || c.fn.events == nil {
		return -1
	}
	ev.at = at
	t := c.fn.events
	t.events = append(t.events, ev)
	index := len(t.events) - 1
	if ev.holder.kind == tempHolder {
		t.temps[ev.holder.node] = append(t.temps[ev.holder.node], index)
	}
	return index
}

// subsumePlaceChain marks e, and each receiver of the member and index chain e roots, as
// read by an event other than a use.
func (c *checker) subsumePlaceChain(e ast.Expr) {
	if c.fn == nil || c.fn.events == nil {
		return
	}
	for e != nil {
		c.fn.events.subsumed.Add(e)
		switch x := e.(type) {
		case *ast.MemberExpr:
			e = x.Object
		case *ast.IndexExpr:
			e = x.Object
		default:
			return
		}
	}
}

// exprStmtRefs maps the expression of each expression statement in cfg's blocks to the
// statement's CFG point.
func exprStmtRefs(cfg *liveness.CFG) map[ast.Expr]liveness.StmtRef {
	out := map[ast.Expr]liveness.StmtRef{}
	for _, block := range cfg.Blocks {
		for i, stmt := range block.Stmts {
			if es, ok := stmt.(*ast.ExprStmt); ok {
				out[es.Expr] = liveness.StmtRef{BlockID: block.ID, StmtIdx: i}
			}
		}
	}
	return out
}

// eventStmtRef returns the CFG point the events of the expression being walked attach to.
// That is the point attachEventsAt set, while c.fn.eventStmt is the statement it was set
// under, and c.fn.eventStmt's point otherwise. ok is false outside a body or when the point
// is unknown.
func (c *checker) eventStmtRef() (liveness.StmtRef, bool) {
	if c.fn == nil {
		return liveness.StmtRef{}, false
	}
	if c.fn.exprAt.set && c.fn.exprAt.stmt == c.fn.eventStmt {
		return c.fn.exprAt.ref, true
	}
	if c.fn.eventStmt == nil {
		return liveness.StmtRef{}, false
	}
	ref, ok := c.fn.stmtToRef[c.fn.eventStmt]
	return ref, ok
}

// attachEventsAt makes eventStmtRef return ref until the returned function restores the
// previous point. A statement with a CFG point inside the expression being inferred
// attaches its events to its own point while it is walked.
func (c *checker) attachEventsAt(ref liveness.StmtRef) func() {
	fn := c.fn
	saved := fn.exprAt
	fn.exprAt.stmt, fn.exprAt.ref, fn.exprAt.set = fn.eventStmt, ref, true
	return func() { fn.exprAt = saved }
}

// emitRead emits a read of the place e names, attached to the CFG point eventStmtRef
// returns. An expression that names no local place emits nothing.
func (c *checker) emitRead(e ast.Expr) {
	if c.fn == nil || c.fn.events == nil {
		return
	}
	if ref, ok := c.eventStmtRef(); ok {
		c.emitReadAt(e, ref)
	}
}

// emitReadAt emits a read of the place e names, attached to ref. A read of e already
// emitted at ref emits nothing more.
func (c *checker) emitReadAt(e ast.Expr, ref liveness.StmtRef) {
	p, ok := c.exprPlace(e)
	if !ok || !c.firstEmit(e, readRole, ref, 0) {
		return
	}
	c.emitEvent(ownershipEvent{kind: useEvent, place: p, access: readAccess, node: e, fromRead: true}, ref)
}

// emitWrite emits a write of the assignment target e, attached to ref. The receivers the
// target reads to reach the written place are part of the write.
func (c *checker) emitWrite(e ast.Expr, ref liveness.StmtRef) {
	if c.fn == nil || c.fn.events == nil {
		return
	}
	c.subsumePlaceChain(e)
	if p, ok := c.exprPlace(e); ok {
		c.emitEvent(ownershipEvent{kind: useEvent, place: p, access: writeAccess, node: e}, ref)
	}
}

// placeHolderOf returns the holder for the place e names, or a tempHolder for e when e
// names no local place.
func (c *checker) placeHolderOf(e ast.Expr) eventHolder {
	if p, ok := c.exprPlace(e); ok {
		return eventHolder{kind: placeHolder, place: p}
	}
	return eventHolder{kind: tempHolder, node: e}
}

// emitFlow emits the events of the value source passes to holder, a destination
// described by dest, attached to ref:
//
//   - A place copies, borrows, or moves by F2, or records a provisional flow when F3 has
//     not settled the source or the destination.
//   - A written borrow, a literal, or a closure already emitted its borrow, elements, or
//     captures with itself as their holder, and those events move to holder.
//   - A branching expression passes the value of each branch's tail.
//
// Any other source, such as a call result, names no place and emits nothing here.
func (c *checker) emitFlow(source ast.Expr, holder eventHolder, dest flowDest, ref liveness.StmtRef) {
	if c.fn == nil || c.fn.events == nil || source == nil {
		return
	}
	switch s := source.(type) {
	case *ast.BorrowExpr, *ast.ObjectExpr, *ast.TupleExpr, *ast.FuncExpr:
		c.retargetTemp(s, holder)
		return
	case *ast.IfElseExpr:
		c.emitBlockTailFlow(&s.Cons, holder, dest, ref)
		c.emitBlockOrExprFlow(s.Alt, holder, dest, ref)
		return
	case *ast.IfValExpr:
		c.emitBlockTailFlow(&s.Cons, holder, dest, ref)
		c.emitBlockOrExprFlow(s.Alt, holder, dest, ref)
		return
	case *ast.MatchExpr:
		for _, mc := range s.Cases {
			c.emitBlockOrExprFlow(&mc.Body, holder, dest, ref)
		}
		return
	case *ast.DoExpr:
		c.emitBlockTailFlow(&s.Body, holder, dest, ref)
		return
	case *ast.TryCatchExpr:
		c.emitBlockTailFlow(&s.Try, holder, dest, ref)
		for _, mc := range s.Catch {
			c.emitBlockOrExprFlow(&mc.Body, holder, dest, ref)
		}
		return
	}
	p, ok := c.exprPlace(source)
	if !ok {
		return
	}
	c.emitValueFlow(p, source, nil, source, holder, dest, ref)
}

// emitBlockTailFlow emits the flow of the value block b produces, its last statement when
// that is an expression, into holder. The flow attaches to the CFG node of that statement
// when it has one, and to ref otherwise.
func (c *checker) emitBlockTailFlow(b *ast.Block, holder eventHolder, dest flowDest, ref liveness.StmtRef) {
	if b == nil || len(b.Stmts) == 0 {
		return
	}
	last, ok := b.Stmts[len(b.Stmts)-1].(*ast.ExprStmt)
	if !ok {
		return
	}
	if tailRef, found := c.fn.stmtToRef[last]; found {
		ref = tailRef
	}
	c.emitFlow(last.Expr, holder, dest, ref)
}

// emitBlockOrExprFlow emits the flow of the value boe produces into holder.
func (c *checker) emitBlockOrExprFlow(boe *ast.BlockOrExpr, holder eventHolder, dest flowDest, ref liveness.StmtRef) {
	switch {
	case boe == nil:
	case boe.Block != nil:
		c.emitBlockTailFlow(boe.Block, holder, dest, ref)
	case boe.Expr != nil:
		if exprRef, ok := c.fn.exprRefs[boe.Expr]; ok {
			ref = exprRef
		}
		c.emitFlow(boe.Expr, holder, dest, ref)
	}
}

// emitBorrow emits the borrow a written `&` or `&mut` expression takes of its operand's
// place, attached to the CFG point eventStmtRef returns. The borrow expression holds the borrow
// until a flow site retargets it. finishOwnershipEvents fills in the borrow's lifetime
// once the expression's type is recorded.
func (c *checker) emitBorrow(e *ast.BorrowExpr) {
	if c.fn == nil || c.fn.events == nil {
		return
	}
	ref, ok := c.eventStmtRef()
	if !ok {
		return
	}
	p, ok := c.exprPlace(e.Arg)
	if !ok || !c.firstEmit(e, borrowRole, ref, 0) {
		return
	}
	c.subsumePlaceChain(e.Arg)
	c.emitEvent(ownershipEvent{
		kind:   borrowEvent,
		place:  p,
		holder: eventHolder{kind: tempHolder, node: e},
		borrow: borrowKindOf(e.Mut),
		node:   e,
	}, ref)
}

// finishOwnershipEvents completes the current body's table once its body is walked. Each
// written borrow takes the lifetime of its expression's type. Then each closure capture
// settles, since the body is walked and every place that holds a closure is known.
func (c *checker) finishOwnershipEvents() {
	if c.fn == nil || c.fn.events == nil {
		return
	}
	events := c.fn.events.events
	for i := range events {
		be, ok := events[i].node.(*ast.BorrowExpr)
		if !ok || events[i].kind != borrowEvent {
			continue
		}
		if r, isRef := c.info.TypeOf(be).(*soltype.RefType); isRef {
			events[i].lifetime = r.Lt
		}
	}
	c.settleFlowsWaitingOn(flowWait{capture: true, table: c.fn.events}, false, true)
}

// emitTempFlow emits the flow of source into the element at path of the literal or
// closure value, which holds it until a flow site retargets it.
func (c *checker) emitTempFlow(source ast.Expr, value ast.Expr, path []placeSeg, dest flowDest, ref liveness.StmtRef) {
	if c.fn == nil || c.fn.events == nil || !c.firstEmit(source, elementRole, ref, 0) {
		return
	}
	holder := eventHolder{kind: tempHolder, node: value}.extend(path)
	c.emitFlow(source, holder, dest, ref)
}

// retargetTemp moves every event held by the temporary value to holder, keeping the
// path within the value.
func (c *checker) retargetTemp(value ast.Expr, holder eventHolder) {
	t := c.fn.events
	indices := t.temps[value]
	delete(t.temps, value)
	for _, i := range indices {
		t.events[i].holder = holder.extend(t.events[i].holder.place.path)
		if holder.kind == tempHolder {
			t.temps[holder.node] = append(t.temps[holder.node], i)
		}
	}
}

// emitValueFlow emits the flow of the value at place p into holder, attached to ref. node
// is the expression to blame. source is the expression whose type the flow reads, or nil
// when sourceT holds that type. A flow F3 has not settled appends a provisional
// placeholder and waits in c.pendingFlows.
//
// A value type creates no holder of a borrow, so its flow is only a read of p. A source
// expression already emitted that read, and the flow emits nothing more. A flow with no
// source expression emits the read itself.
func (c *checker) emitValueFlow(p movePlace, source ast.Expr, sourceT soltype.Type, node ast.Node, holder eventHolder, dest flowDest, ref liveness.StmtRef) {
	if source != nil {
		sourceT = c.info.TypeOf(source)
	}
	// The escape check of a closure capture reads these records. A closure is a value type,
	// so they are recorded before the value-type case below returns.
	switch holder.kind {
	case returnHolder, moduleHolder:
		c.fn.events.escaping.Add(p.root)
	case placeHolder:
		c.fn.events.flowsInto[p.root] = append(c.fn.events.flowsInto[p.root], holder.place.root)
	case argHolder, receiverHolder, tempHolder:
	}
	ev := ownershipEvent{place: p, holder: holder, node: node}
	kind, wait, settled := c.classifyFlow(c.fn.events, p, sourceT, holder, dest, false, false, &ev)
	if settled && kind == useEvent {
		if source == nil {
			c.emitEvent(ownershipEvent{kind: useEvent, place: p, access: readAccess, node: node}, ref)
		}
		return
	}
	// The flow's event reads source, so the use the read emitted is dropped.
	if source != nil {
		c.subsumePlaceChain(source)
	}
	ev.kind = kind
	if !settled {
		ev.kind = provisionalEvent
	}
	index := c.emitEvent(ev, ref)
	if !settled && index >= 0 {
		pf := provisionalFlow{table: c.fn.events, index: index, source: source, sourceT: sourceT, dest: dest}
		c.addPendingFlow(pf, wait)
	}
}

// classifyFlow returns the event kind F2 gives the flow of place p, whose value has type
// t, into holder through dest. table holds the flow's event. It fills ev's borrow and
// lifetime for a borrow. A value
// type returns useEvent, since copying it is only a read of p. settled is false when F3
// has not settled the source or the destination, and wait then names what the flow waits
// on.
//
// force settles every such flow the way the current solver treats what it cannot see: an
// unsettled parameter is read as it stands, a callee with no signature borrows its
// argument, and a closure capture settles from the holder the closure has then.
// forceCapture settles only that last case.
func (c *checker) classifyFlow(table *ownershipEvents, p movePlace, t soltype.Type, holder eventHolder, dest flowDest, force, forceCapture bool, ev *ownershipEvent) (ownershipEventKind, flowWait, bool) {
	dest, wait, ok := c.settleDest(table, dest, holder, force, force || forceCapture)
	if !ok {
		return 0, wait, false
	}
	src, wait, ok := c.sourceKindOf(p, t, force)
	if !ok {
		return 0, wait, false
	}
	switch src {
	case valueSource:
		return useEvent, flowWait{}, true
	case borrowSource:
		if dest.kind == borrowDest && dest.reborrows {
			ev.borrow, ev.lifetime = dest.borrow, dest.lt
			return borrowEvent, flowWait{}, true
		}
		return copyEvent, flowWait{}, true
	case ownedSource:
	}
	if dest.kind == borrowDest {
		ev.borrow, ev.lifetime = dest.borrow, dest.lt
		return borrowEvent, flowWait{}, true
	}
	return moveEvent, flowWait{}, true
}

// settleDest resolves a destination whose ownership is decided elsewhere to an owned,
// borrow, or inferred-return destination. ok is false when F3 has not decided it yet, and
// wait then names what it waits on. force and forceCapture are the flags classifyFlow
// describes.
func (c *checker) settleDest(table *ownershipEvents, dest flowDest, holder eventHolder, force, forceCapture bool) (flowDest, flowWait, bool) {
	switch dest.kind {
	case paramDest:
		if !force && c.unsettledParams.Contains(dest.param) {
			return dest, flowWait{param: dest.param}, false
		}
		settled := c.paramDestOf(dest.param, dest.reborrows)
		if settled.kind == borrowDest && dest.callLifetime != nil {
			settled.lt = dest.callLifetime
		}
		return settled, flowWait{}, true
	case calleeDest:
		if fn := c.resolvedCallee(dest.call); fn != nil {
			if param, ok := argDest(dest.call, fn, dest.index); ok {
				return c.settleDest(table, param, holder, force, forceCapture)
			}
		}
		if !force {
			return dest, flowWait{callee: true}, false
		}
		return flowDest{kind: borrowDest, borrow: immutableBorrow}, flowWait{}, true
	case captureDest:
		// A closure that is returned or stored into module-level state owns what it
		// captures, and any other closure borrows it. A closure a local holds may still be
		// returned through that local, so it waits for the whole body.
		switch holder.kind {
		case returnHolder, moduleHolder:
			return flowDest{kind: ownedDest}, flowWait{}, true
		case tempHolder, placeHolder:
			if !forceCapture {
				return dest, flowWait{capture: true}, false
			}
			if holder.kind == placeHolder && table.escapes(holder.place.root) {
				return flowDest{kind: ownedDest}, flowWait{}, true
			}
		case argHolder, receiverHolder:
		}
		return flowDest{kind: borrowDest, borrow: borrowKindOf(dest.writes), reborrows: true}, flowWait{}, true
	case ownedDest, borrowDest, inferredReturnDest:
	}
	return dest, flowWait{}, true
}

// paramDestOf returns the destination an argument passed to fp has. A borrow parameter
// borrows. A parameter whose body moves it, or whose type is a concrete owned shape, takes
// ownership. A rest parameter takes ownership when its element type is a concrete owned
// shape. Any other parameter is one F3 settled as a borrow, since its body neither moves it
// nor lets it escape.
func (c *checker) paramDestOf(fp *soltype.FuncParam, reborrows bool) flowDest {
	if r, ok := fp.Type.(*soltype.RefType); ok && r.Lt != nil {
		return flowDest{kind: borrowDest, borrow: borrowKindOf(r.Mut), lt: r.Lt, reborrows: reborrows}
	}
	if fp.Rest {
		if elem, isArray := c.ctx.restSlotElem(fp.Type); isArray && c.isConcreteOwned(elem) {
			return flowDest{kind: ownedDest}
		}
		return flowDest{kind: borrowDest, borrow: immutableBorrow, reborrows: reborrows}
	}
	if fp.Moved || c.isConcreteOwned(fp.Type) {
		return flowDest{kind: ownedDest}
	}
	return flowDest{kind: borrowDest, borrow: immutableBorrow, reborrows: reborrows}
}

// resolvedCallee returns the signature the callee of call has once inference has run, or
// nil when its type is not a function. A callee whose type is a variable resolves through
// the first function among its lower bounds.
func (c *checker) resolvedCallee(call *ast.CallExpr) *soltype.FuncType {
	t := c.info.TypeOf(call.Callee)
	seen := set.NewSet[*soltype.TypeVarType]()
	for t != nil {
		switch x := t.(type) {
		case *soltype.FuncType:
			return x
		case *soltype.TypeVarType:
			if seen.Contains(x) {
				return nil
			}
			seen.Add(x)
			t = nil
			for _, lb := range x.LowerBounds {
				if _, isFn := lb.(*soltype.FuncType); isFn {
					t = lb
					break
				}
			}
		default:
			return nil
		}
	}
	return nil
}

// sourceKindOf classifies the value place p holds, whose type is t. ok is false when p is
// rooted at an unannotated parameter whose body is still being walked and force is unset,
// and wait then names that parameter. A value type is a value source whatever its root,
// so it is classified first. A place rooted at an unannotated parameter takes the parameter's
// ownership, which paramDestOf gives the caller's side of the same parameter. A field of an
// owned parameter, and every other place, is classified by its own type.
func (c *checker) sourceKindOf(p movePlace, t soltype.Type, force bool) (sourceKind, flowWait, bool) {
	if c.holdsValueType(t) {
		return valueSource, flowWait{}, true
	}
	if fp, isParam := c.paramByVar[p.root]; isParam {
		if !force && c.unsettledParams.Contains(fp) {
			return valueSource, flowWait{param: fp}, false
		}
		if c.paramDestOf(fp, false).kind != ownedDest {
			return borrowSource, flowWait{}, true
		}
		if len(p.path) == 0 {
			return ownedSource, flowWait{}, true
		}
	}
	if _, ok := c.borrowOf(t); ok {
		return borrowSource, flowWait{}, true
	}
	return ownedSource, flowWait{}, true
}

// fieldType returns the type of the field name of the object type t, looking through a
// borrow, or nil when t is not an object that lists the field.
func (c *checker) fieldType(t soltype.Type, name string) soltype.Type {
	if r, ok := t.(*soltype.RefType); ok {
		t = r.Inner
	}
	if obj, ok := t.(*soltype.ObjectType); ok {
		for _, elem := range obj.Elems {
			if prop, isProp := elem.(*soltype.PropertyElem); isProp && prop.Name == name {
				return prop.Type
			}
		}
	}
	return nil
}

// holdsValueType reports whether a value of type t is a value type, which a flow copies. A
// nil t counts as one, since it names no value a flow can move. A variable is a value type
// when every value it can hold is one. So is a union whose members all are, such as
// `number | undefined`, but not `{x: number} | null`.
func (c *checker) holdsValueType(t soltype.Type) bool {
	if t == nil {
		return true
	}
	if u, ok := c.expandAliasChain(t).(*soltype.UnionType); ok {
		for _, member := range u.Types {
			if !c.holdsValueType(member) {
				return false
			}
		}
		return true
	}
	if !c.isReferenceShaped(t) {
		return true
	}
	v, isVar := t.(*soltype.TypeVarType)
	return isVar && c.holdsOnlyValueTypes(v, set.NewSet[*soltype.TypeVarType]())
}

// addPendingFlow files pf under what it waits on. A capture waits under the table of the
// body its closure is written in.
func (c *checker) addPendingFlow(pf provisionalFlow, wait flowWait) {
	if wait.capture {
		wait.table = pf.table
	}
	if c.pendingFlows == nil {
		c.pendingFlows = map[flowWait][]provisionalFlow{}
	}
	c.pendingFlows[wait] = append(c.pendingFlows[wait], pf)
}

// settleFlowsWaitingOn replaces each provisional flow waiting on wait with its copy,
// borrow, or move event. A flow still blocked on something else waits on that instead.
// force and forceCapture are the flags classifyFlow describes.
func (c *checker) settleFlowsWaitingOn(wait flowWait, force, forceCapture bool) {
	flows := c.pendingFlows[wait]
	delete(c.pendingFlows, wait)
	for _, pf := range flows {
		ev := pf.table.events[pf.index]
		t := pf.sourceT
		if pf.source != nil {
			t = c.info.TypeOf(pf.source)
		}
		kind, next, settled := c.classifyFlow(pf.table, ev.place, t, ev.holder, pf.dest, force, forceCapture, &ev)
		if !settled {
			c.addPendingFlow(pf, next)
			continue
		}
		ev.kind = kind
		if kind == useEvent {
			ev.holder, ev.access = eventHolder{}, readAccess
		}
		pf.table.events[pf.index] = ev
	}
}

// settleAllFlows settles every provisional flow, the way classifyFlow's force flag
// describes. It runs once a module or script is inferred, when nothing is left to settle
// them.
func (c *checker) settleAllFlows() {
	for wait := range c.pendingFlows {
		c.settleFlowsWaitingOn(wait, true, true)
	}
}

// emitCaptureFlow emits the capture of the local root, of type t, by closure, attached to
// ref. The closure holds the capture until a flow site receives it. writes marks a closure
// that writes the local. moves marks a closure whose body moves the local, which owns it
// wherever the closure flows.
func (c *checker) emitCaptureFlow(closure ast.Func, root liveness.VarID, t soltype.Type, writes, moves bool, ref liveness.StmtRef) {
	if c.fn == nil || c.fn.events == nil || !c.firstEmit(closure, captureRole, ref, root) {
		return
	}
	// An unannotated parameter's binding renders as `never` until its body settles it, so
	// the capture reads the parameter's own type instead.
	if fp, ok := c.paramByVar[root]; ok {
		t = fp.Type
	}
	holder := eventHolder{kind: tempHolder, node: closure}
	dest := flowDest{kind: captureDest, writes: writes}
	if moves {
		dest = flowDest{kind: ownedDest}
	}
	c.emitValueFlow(movePlace{root: root}, nil, t, closure, holder, dest, ref)
}

// renderOwnershipEvents renders the events of t one per line, each prefixed with the CFG
// node it is attached to, as renderStmtRef writes it. A borrow's lifetime renders as
// `'static`, as `'_` when the borrow's type carries none or an elided one, or as `'<n>`
// numbering the other lifetimes in order of first appearance.
func renderOwnershipEvents(t *ownershipEvents) []string {
	ltNames := map[soltype.Lifetime]string{}
	var out []string
	for _, ev := range t.list() {
		out = append(out, fmt.Sprintf("%s: %s", renderStmtRef(ev.at), t.renderEvent(ev, ltNames)))
	}
	return out
}

// renderStmtRef renders a CFG node as `b<block>.<index>`, or as `b<block>.entry` for the
// synthetic position before the block's first statement, which liveness numbers -1.
func renderStmtRef(at liveness.StmtRef) string {
	if at.StmtIdx < 0 {
		return fmt.Sprintf("b%d.entry", at.BlockID)
	}
	return fmt.Sprintf("b%d.%d", at.BlockID, at.StmtIdx)
}

// renderEvent renders one event in the notation of the checking model. A name is in
// backticks and a keyword is bare, so `val c = &p` renders as borrow(&, `p`, `c`, '1).
func (t *ownershipEvents) renderEvent(ev ownershipEvent, ltNames map[soltype.Lifetime]string) string {
	place := t.renderPlace(ev.place)
	switch ev.kind {
	case copyEvent:
		return fmt.Sprintf("copy(%s, %s)", place, t.renderHolder(ev.holder))
	case moveEvent:
		return fmt.Sprintf("move(%s, %s)", place, t.renderHolder(ev.holder))
	case borrowEvent:
		kind := "&"
		if ev.borrow == mutableBorrow {
			kind = "&mut"
		}
		return fmt.Sprintf("borrow(%s, %s, %s, %s)", kind, place, t.renderHolder(ev.holder), renderLifetime(ev.lifetime, ltNames))
	case useEvent:
		access := "read"
		if ev.access == writeAccess {
			access = "write"
		}
		return fmt.Sprintf("use(%s, %s)", place, access)
	default:
		return fmt.Sprintf("provisional(%s, %s)", place, t.renderHolder(ev.holder))
	}
}

// renderLifetime renders lt for renderEvent, naming it in ltNames on first sight.
func renderLifetime(lt soltype.Lifetime, ltNames map[soltype.Lifetime]string) string {
	if _, anon := lt.(*soltype.AnonLifetime); lt == nil || anon {
		// An AnonLifetime is the display marker for an elided lifetime, which names nothing.
		return "'_"
	}
	if soltype.IsStaticLifetime(lt) {
		return "'static"
	}
	if name, ok := ltNames[lt]; ok {
		return name
	}
	name := fmt.Sprintf("'%d", len(ltNames)+1)
	ltNames[lt] = name
	return name
}

// renderPlace renders a place as its root's name followed by its field path, in
// backticks so a name reads apart from the keywords an event renders, such as `read` or
// `return`. A root with no name renders bare as `var#<id>`.
func (t *ownershipEvents) renderPlace(p movePlace) string {
	var b strings.Builder
	name, ok := t.names[p.root]
	if !ok {
		fmt.Fprintf(&b, "var#%d", p.root)
		writePath(&b, p.path)
		return b.String()
	}
	b.WriteByte('`')
	b.WriteString(name)
	writePath(&b, p.path)
	b.WriteByte('`')
	return b.String()
}

// renderHolder renders a holder: a place by name, a module-level binding by name, an
// argument as `arg(<callee>, <index>)`, a receiver as `arg(<method>, self)`, a return as
// `return`, and a temporary as `temp`. A path within the holder follows it. A name is in
// backticks, as renderPlace writes it, and a keyword is bare.
func (t *ownershipEvents) renderHolder(h eventHolder) string {
	var b strings.Builder
	switch h.kind {
	case placeHolder:
		return t.renderPlace(h.place)
	case moduleHolder:
		b.WriteByte('`')
		b.WriteString(h.name)
		writePath(&b, h.place.path)
		b.WriteByte('`')
		return b.String()
	case argHolder:
		fmt.Fprintf(&b, "arg(%s, %d)", renderCallee(h.node), h.index)
	case receiverHolder:
		fmt.Fprintf(&b, "arg(%s, self)", renderCallee(h.node))
	case returnHolder:
		b.WriteString("return")
	case tempHolder:
		b.WriteString("temp")
	}
	writePath(&b, h.place.path)
	return b.String()
}

// writePath appends each segment of path to b as `.name`.
func writePath(b *strings.Builder, path []placeSeg) {
	for _, seg := range path {
		b.WriteByte('.')
		b.WriteString(seg.name)
	}
}

// renderCallee names the callee of the call node for rendering, as calleeName does, in
// backticks. Any other callee renders as the bare keyword `call`.
func renderCallee(node ast.Node) string {
	if ce, ok := node.(*ast.CallExpr); ok {
		if name := calleeName(ce.Callee); name != "" {
			return "`" + name + "`"
		}
	}
	return "call"
}

// registerUnsettledParams records each unannotated parameter of the body about to be
// walked, and each leaf such a parameter's pattern binds, so a flow out of one waits until
// the body settles the parameter's ownership. sigParams are the
// declared parameters and params the FuncParams built from them, one for one.
func (c *checker) registerUnsettledParams(sigParams []*ast.Param, params []*soltype.FuncParam) {
	for i, p := range sigParams {
		if p.TypeAnn != nil || i >= len(params) {
			continue
		}
		// Each leaf a destructuring parameter binds takes the parameter's ownership.
		ast.ForEachLeafBinding(p.Pattern, func(_ string, leaf ast.Node) {
			id := c.varIDs.Of(leaf)
			if id <= 0 {
				return
			}
			if c.paramByVar == nil {
				c.paramByVar = map[liveness.VarID]*soltype.FuncParam{}
			}
			if c.unsettledParams == nil {
				c.unsettledParams = set.NewSet[*soltype.FuncParam]()
			}
			c.paramByVar[id] = params[i]
			c.unsettledParams.Add(params[i])
		})
	}
}

// settleParams marks the parameters registerUnsettledParams recorded for this body as
// settled, and settles each provisional flow waiting on one of them. It runs once the body
// has decided their ownership.
func (c *checker) settleParams(params []*soltype.FuncParam) {
	for _, fp := range params {
		if !c.unsettledParams.Contains(fp) {
			continue
		}
		c.unsettledParams.Remove(fp)
		c.settleFlowsWaitingOn(flowWait{param: fp}, false, false)
	}
}

// emitBindingFlow emits the flow of a `val` or `var` initializer into the binding whose
// VarID is id, attached to ref. A binding with an annotation takes the annotation's
// ownership. One without is owned, as F3 states.
func (c *checker) emitBindingFlow(vd *ast.VarDecl, id liveness.VarID, bindingT soltype.Type, ref liveness.StmtRef) {
	if id <= 0 || c.fn.events == nil || !c.firstEmit(vd, bindingRole, ref, 0) {
		return
	}
	dest := flowDest{kind: ownedDest}
	if vd.TypeAnn != nil {
		dest = c.destOfType(bindingT)
	}
	c.emitFlow(vd.Init, eventHolder{kind: placeHolder, place: movePlace{root: id}}, dest, ref)
}

// emitPatternFlows emits the flow of each part pat binds out of the value of source,
// attached to ref. pat is the pattern of a destructuring `val`, a `match` arm, or an
// `if val`. Only a source that names a place passes parts of a place. A part read through a
// borrow is borrowed again, since F4 makes a pattern over a borrow bind borrows. A tuple
// or extractor element flows out of the place extended by its position, as elements
// describes.
func (c *checker) emitPatternFlows(pat ast.Pat, source ast.Expr, ref liveness.StmtRef) {
	if c.fn == nil || c.fn.events == nil || source == nil || !c.firstEmit(pat, patternRole, ref, 0) {
		return
	}
	src, ok := c.exprPlace(source)
	if !ok {
		return
	}
	w := &patternEmitter{c: c, source: source, sourceT: c.info.TypeOf(source), ref: ref}
	w.walk(pat, src)
	// A pattern that binds nothing, such as a literal arm, leaves the source's read alone.
	if w.bound {
		c.subsumePlaceChain(source)
	}
}

// branchEntryRef returns the CFG point on entry to branch i of the `match`, `if val`, or
// `val … else` whose target is target, where the branch's pattern binds its names. The CFG
// builder runs the target in a statement of its own and adds an edge from that statement's
// block to each branch's block in order. A `match` adds its arms in source order, an
// `if val` adds its consequent first, and a `val … else` adds its `else` block first. The
// point is StmtIdx -1 of that branch's block. A branch the CFG builder gave no block of its own, such
// as an arm of a `match` in a `return`, runs at the point of the expression holding it,
// which siteRef names when hasSite is set. ok is false when neither point is known.
func (c *checker) branchEntryRef(target ast.Expr, i int, siteRef liveness.StmtRef, hasSite bool) (liveness.StmtRef, bool) {
	if c.fn == nil || c.fn.events == nil {
		return liveness.StmtRef{}, false
	}
	if ref, ok := c.fn.exprRefs[target]; ok {
		if succ := c.fn.cfg.Blocks[ref.BlockID].Successors; i < len(succ) {
			return liveness.StmtRef{BlockID: succ[i].ID, StmtIdx: -1}, true
		}
	}
	return siteRef, hasSite
}

// patternEmitter walks a pattern for emitPatternFlows. sourceT is the type of the source
// expression source. bound records whether the pattern binds any leaf.
type patternEmitter struct {
	c       *checker
	source  ast.Expr
	sourceT soltype.Type
	ref     liveness.StmtRef
	bound   bool
}

// walk emits the flow of each part pat binds out of the place `at`.
func (w *patternEmitter) walk(pat ast.Pat, at movePlace) {
	switch pat := pat.(type) {
	case *ast.IdentPat:
		w.leaf(pat, at, nil)
	case *ast.ObjectPat:
		for _, elem := range pat.Elems {
			switch e := elem.(type) {
			case *ast.ObjShorthandPat:
				w.leaf(e, extendPlace(at, e.Key.Name), nil)
			case *ast.ObjKeyValuePat:
				w.walk(e.Value, extendPlace(at, e.Key.Name))
			case *ast.ObjRestPat:
				w.objectRest(e.Pattern, at)
			}
		}
	case *ast.InstancePat:
		w.walk(pat.Object, at)
	case *ast.TuplePat:
		w.elements(pat.Elems, at)
	case *ast.ExtractorPat:
		w.elements(pat.Args, at)
	case *ast.RestPat:
		w.walk(pat.Pattern, at)
	}
}

// elements walks the element patterns of a tuple or extractor pattern. Element i takes the
// place `at` extended by the segment `i`. A rest element at position i takes the segment
// `i...`, which stands for every element from i on.
func (w *patternEmitter) elements(elems []ast.Pat, at movePlace) {
	for i, elem := range elems {
		if rest, ok := elem.(*ast.RestPat); ok {
			w.walk(rest.Pattern, extendPlace(at, strconv.Itoa(i)+"..."))
			continue
		}
		w.walk(elem, extendPlace(at, strconv.Itoa(i)))
	}
}

// objectRest walks the rest element `...rest` of an object pattern over the place `at`. A
// rest binding whose type is an object takes each field that type lists, so the fields the
// pattern names flow only into their own leaves. Any other rest binding takes the whole
// place.
func (w *patternEmitter) objectRest(pat ast.Pat, at movePlace) {
	ident, ok := pat.(*ast.IdentPat)
	if !ok {
		w.walk(pat, at)
		return
	}
	restT := w.c.info.TypeOf(ident)
	if r, isRef := restT.(*soltype.RefType); isRef {
		restT = r.Inner
	}
	obj, isObj := restT.(*soltype.ObjectType)
	if !isObj {
		w.leaf(ident, at, nil)
		return
	}
	for _, elem := range obj.Elems {
		if prop, isProp := elem.(*soltype.PropertyElem); isProp {
			w.leaf(ident, extendPlace(at, prop.Name), []placeSeg{{kind: namedSeg, name: prop.Name}})
		}
	}
}

// leaf emits the flow of the place `at` into the binding the pattern leaf node binds, at
// path within it. The leaf's type is the one the pattern walk recorded for node, or that
// type's field when path names one. A value-type leaf is a read. Any
// other leaf is borrowed when the source is a borrow, and takes its own type's ownership
// otherwise.
func (w *patternEmitter) leaf(node ast.Node, at movePlace, path []placeSeg) {
	id := w.c.varIDs.Of(node)
	leafT := w.c.info.TypeOf(node)
	if id <= 0 || leafT == nil {
		return
	}
	if len(path) > 0 {
		leafT = w.c.fieldType(leafT, path[0].name)
	}
	w.bound = true
	sourceT := leafT
	dest := w.c.destOfType(leafT)
	if r, ok := w.c.borrowOf(w.sourceT); ok && !w.c.holdsValueType(leafT) {
		// The leaf binds a borrow with the source's mutability, as D14 decides.
		sourceT = r
		dest = flowDest{kind: borrowDest, borrow: borrowKindOf(r.Mut), lt: r.Lt}
	}
	dest.reborrows = true
	holder := eventHolder{kind: placeHolder, place: movePlace{root: id, path: path}}
	w.c.emitValueFlow(at, nil, sourceT, w.source, holder, dest, w.ref)
}

// emitAssignFlow emits the events of the assignment e, attached to ref: the flow of the
// source into the target, then the write of the target. A
// reassigned binding takes the ownership of its type. A field or element store takes the
// ownership of the field it writes when the receiver declares one, and is owned otherwise.
func (c *checker) emitAssignFlow(scope *Scope, e *ast.BinaryExpr, ref liveness.StmtRef) {
	if c.fn == nil || c.fn.events == nil || !c.firstEmit(e, assignRole, ref, 0) {
		return
	}
	switch left := e.Left.(type) {
	case *ast.IdentExpr:
		b, found := c.lookupValueBinding(scope, left.Name)
		if !found || len(b.Schemes) == 0 {
			return
		}
		dest := c.destOfType(bindingType(b))
		// A module-level binding is a local of a function body that reaches it, but a store
		// into it outlives every local, so it is a moduleHolder.
		switch id := c.varIDs.Of(left); {
		case b.ModuleLevel:
			c.emitFlow(e.Right, eventHolder{kind: moduleHolder, name: left.Name}, dest, ref)
		case id > 0:
			c.emitFlow(e.Right, eventHolder{kind: placeHolder, place: movePlace{root: id}}, dest, ref)
		default:
			return
		}
		c.emitWrite(left, ref)
	case *ast.MemberExpr, *ast.IndexExpr:
		c.emitFlow(e.Right, c.placeHolderOf(left), c.storeDest(left), ref)
		c.emitWrite(left, ref)
	}
}

// storeDest returns the destination a field or element store into target has. A store
// into a field the receiver declares takes the field's ownership, and any other store is
// owned.
func (c *checker) storeDest(target ast.Expr) flowDest {
	var object ast.Expr
	var name string
	switch t := target.(type) {
	case *ast.MemberExpr:
		if t.Prop == nil {
			return flowDest{kind: ownedDest}
		}
		object, name = t.Object, t.Prop.Name
	case *ast.IndexExpr:
		key, ok := constStringKey(t.Index)
		if !ok {
			return flowDest{kind: ownedDest}
		}
		object, name = t.Object, key
	}
	recv := c.info.TypeOf(object)
	if recv == nil {
		return flowDest{kind: ownedDest}
	}
	if prop, ok := c.lookupWriteField(name, recv); ok {
		return c.destOfType(prop.Type)
	}
	return flowDest{kind: ownedDest}
}

// lookupWriteField returns the property named name a write through a receiver of type recv
// stores into, and false when the receiver declares none.
func (c *checker) lookupWriteField(name string, recv soltype.Type) (*soltype.PropertyElem, bool) {
	member, _ := c.writeMember(name, readCarrier(recv))
	prop, ok := member.(*soltype.PropertyElem)
	return prop, ok
}

// emitCallFlows emits the flows of the call e, attached to ref: the receiver of a method
// call into the method's `self`, then each argument into its parameter. fn is the
// signature the arguments were applied to, or nil when the call could not see one. An
// argument with no parameter to receive it, the surplus of a call with too many arguments,
// emits no flow. lvl is the level of the call, at which a receiver borrow's lifetime is
// minted.
func (c *checker) emitCallFlows(lvl int, e *ast.CallExpr, fn *soltype.FuncType, ref liveness.StmtRef) {
	if c.fn == nil || c.fn.events == nil || !c.firstEmit(e, callRole, ref, 0) {
		return
	}
	if recv, self := c.calleeReceiver(e.Callee); recv != nil && self != nil {
		// The method read is how the call reaches the receiver, so the receiver's flow
		// accounts for it.
		c.subsumePlaceChain(e.Callee)
		dest := flowDest{kind: paramDest, param: self, reborrows: true}
		// self is the method's declared receiver, which every call of the method shares, so
		// its lifetime cannot tell one call's borrow from another's. Each call borrows the
		// receiver under a lifetime of its own.
		dest.callLifetime = c.ctx.freshLifetime(lvl)
		c.emitFlow(recv, eventHolder{kind: receiverHolder, node: e}, dest, ref)
	}
	for i, arg := range e.Args {
		dest, ok := argDest(e, fn, i)
		if !ok {
			continue
		}
		c.emitFlow(arg, eventHolder{kind: argHolder, node: e, index: i}, dest, ref)
	}
}

// argDest returns the destination argument i of the call e has. A rest parameter receives
// every argument from its position on. ok is false for an argument no parameter receives.
func argDest(e *ast.CallExpr, fn *soltype.FuncType, i int) (flowDest, bool) {
	if fn == nil {
		return flowDest{kind: calleeDest, call: e, index: i}, true
	}
	n := len(fn.Params)
	switch {
	case i < n && !fn.Params[i].Rest:
		return flowDest{kind: paramDest, param: fn.Params[i]}, true
	case n > 0 && fn.Params[n-1].Rest && i >= n-1:
		return flowDest{kind: paramDest, param: fn.Params[n-1]}, true
	}
	return flowDest{}, false
}

// emitReturnFlow emits the flow of the returned expression into the function's return,
// attached to ref. An annotated return takes the annotation's ownership. One without an
// annotation is an inferredReturnDest. An annotation that did not resolve is owned.
func (c *checker) emitReturnFlow(expr ast.Expr, ref liveness.StmtRef) {
	if c.fn.events == nil || !c.firstEmit(expr, returnRole, ref, 0) {
		return
	}
	dest := flowDest{kind: ownedDest}
	switch {
	case c.fn.returnAnn != nil:
		dest = c.destOfType(c.fn.returnAnn)
	case !c.fn.returnAnnotated:
		dest = flowDest{kind: inferredReturnDest}
	}
	c.emitFlow(expr, eventHolder{kind: returnHolder}, dest, ref)
}
