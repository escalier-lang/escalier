package solver

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// This file holds the checker's half of the ownership events that ownership_events.go
// describes: the methods that emit events during the walk, settle provisional flows, and
// resolve each flow's destination, with the types and helpers only they use.

// firstEmit records that node emits its events in role at the CFG point at, and reports
// whether it had not done so before. root names the captured local when node is a closure.
// It returns false when no event table is open, so a caller that gets true can use
// c.fn.events.
func (c *checker) firstEmit(node ast.Node, role emitRole, at liveness.StmtRef, root liveness.VarID) bool {
	t := c.eventTable()
	if t == nil {
		return false
	}
	site := emitSite{node: node, role: role, at: at, root: root}
	if t.emitted.Contains(site) {
		return false
	}
	t.emitted.Add(site)
	return true
}

// eventTable returns the event table of the body being walked, or nil outside a body or
// when the body has no table.
func (c *checker) eventTable() *ownershipEvents {
	if c.fn == nil {
		return nil
	}
	return c.fn.events
}

// sourceKind classifies the value a flow takes from its source, the row of F2's table.
type sourceKind uint8

const (
	valueSource sourceKind = iota
	borrowSource
	ownedSource
)

// destOfType returns the destination a flow into a slot of type t has: a borrow
// destination when t is a borrow, and an owned one otherwise.
func (c *checker) destOfType(t soltype.Type) flowDest {
	if r, ok := c.borrowOf(t); ok {
		return &borrowDest{borrow: borrowKindOf(r.Mut), lt: r.Lt}
	}
	return &ownedDest{}
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
	t := c.eventTable()
	if t == nil {
		return -1
	}
	ev.at = at
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
	t := c.eventTable()
	if t == nil {
		return
	}
	for e != nil {
		t.subsumed.Add(e)
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
	if c.eventTable() == nil {
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
	if c.eventTable() == nil {
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
	if c.eventTable() == nil || source == nil {
		return
	}
	if results, branches := liveness.BranchResults(source); branches {
		for _, result := range results {
			// A branch whose result is an expression statement attaches to that statement.
			branchRef := ref
			if r, ok := c.fn.exprRefs[result]; ok {
				branchRef = r
			}
			c.emitFlow(result, holder, dest, branchRef)
		}
		return
	}
	switch s := source.(type) {
	case *ast.BorrowExpr, *ast.ObjectExpr, *ast.TupleExpr, *ast.FuncExpr:
		c.retargetTemp(s, holder)
		return
	}
	p, ok := c.exprPlace(source)
	if !ok {
		return
	}
	c.emitValueFlow(p, source, nil, source, holder, dest, ref)
}

// emitBorrow emits the borrow a written `&` or `&mut` expression takes of its operand's
// place, attached to the CFG point eventStmtRef returns. The borrow expression holds the borrow
// until a flow site retargets it. finishOwnershipEvents fills in the borrow's lifetime
// once the expression's type is recorded.
func (c *checker) emitBorrow(e *ast.BorrowExpr) {
	if c.eventTable() == nil {
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
	t := c.eventTable()
	if t == nil {
		return
	}
	events := t.events
	for i := range events {
		be, ok := events[i].node.(*ast.BorrowExpr)
		if !ok || events[i].kind != borrowEvent {
			continue
		}
		if r, isRef := c.info.TypeOf(be).(*soltype.RefType); isRef {
			events[i].lifetime = r.Lt
		}
	}
	c.settleFlowsWaitingOn(flowWait{capture: true, table: t}, false, true)
}

// emitTempFlow emits the flow of source into the element at path of the literal or
// closure value, which holds it until a flow site retargets it.
func (c *checker) emitTempFlow(source ast.Expr, value ast.Expr, path []placeSeg, dest flowDest, ref liveness.StmtRef) {
	if !c.firstEmit(source, elementRole, ref, 0) {
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
		if b, ok := dest.(*borrowDest); ok && b.reborrows {
			ev.borrow, ev.lifetime = b.borrow, b.lt
			return borrowEvent, flowWait{}, true
		}
		return copyEvent, flowWait{}, true
	case ownedSource:
	}
	if b, ok := dest.(*borrowDest); ok {
		ev.borrow, ev.lifetime = b.borrow, b.lt
		return borrowEvent, flowWait{}, true
	}
	return moveEvent, flowWait{}, true
}

// settleDest resolves a destination whose ownership is decided elsewhere to an owned,
// borrow, or inferred-return destination. ok is false when F3 has not decided it yet, and
// wait then names what it waits on. force and forceCapture are the flags classifyFlow
// describes.
func (c *checker) settleDest(table *ownershipEvents, dest flowDest, holder eventHolder, force, forceCapture bool) (flowDest, flowWait, bool) {
	switch d := dest.(type) {
	case *paramDest:
		if !force && c.unsettledParams.Contains(d.param) {
			return dest, flowWait{param: d.param}, false
		}
		settled := c.paramDestOf(d.param, d.reborrows)
		if b, ok := settled.(*borrowDest); ok && d.callLifetime != nil {
			settled = &borrowDest{borrow: b.borrow, lt: d.callLifetime, reborrows: b.reborrows}
		}
		return settled, flowWait{}, true
	case *calleeDest:
		if fn, ok := resolveFunc(c.info.TypeOf(d.call.Callee)); ok {
			if param, ok := argDest(d.call, fn, d.index); ok {
				return c.settleDest(table, param, holder, force, forceCapture)
			}
		}
		if !force {
			return dest, flowWait{callee: true}, false
		}
		return &borrowDest{borrow: immutableBorrow}, flowWait{}, true
	case *captureDest:
		// A closure that is returned or stored into module-level state owns what it
		// captures, and any other closure borrows it. A closure a local holds may still be
		// returned through that local, so it waits for the whole body.
		switch holder.kind {
		case returnHolder, moduleHolder:
			return &ownedDest{}, flowWait{}, true
		case tempHolder, placeHolder:
			if !forceCapture {
				return dest, flowWait{capture: true}, false
			}
			if holder.kind == placeHolder && table.escapes(holder.place.root) {
				return &ownedDest{}, flowWait{}, true
			}
		case argHolder, receiverHolder:
		}
		return &borrowDest{borrow: borrowKindOf(d.writes), reborrows: true}, flowWait{}, true
	case *ownedDest, *borrowDest, *inferredReturnDest:
	}
	return dest, flowWait{}, true
}

// paramDestOf returns the destination an argument passed to fp has. A borrow parameter
// borrows. A parameter paramTakesOwnership accepts takes ownership. Any other parameter is
// one F3 settled as a borrow, since its body neither moves it nor lets it escape.
func (c *checker) paramDestOf(fp *soltype.FuncParam, reborrows bool) flowDest {
	if r, ok := fp.Type.(*soltype.RefType); ok && r.Lt != nil {
		return &borrowDest{borrow: borrowKindOf(r.Mut), lt: r.Lt, reborrows: reborrows}
	}
	if c.paramTakesOwnership(fp) {
		return &ownedDest{}
	}
	return &borrowDest{borrow: immutableBorrow, reborrows: reborrows}
}

// paramTakesOwnership reports whether an argument passed to fp is owned by the callee. A
// rest parameter takes ownership when its element type is a concrete owned shape. Any other
// parameter takes ownership when its body moves it or its type is a concrete owned shape.
func (c *checker) paramTakesOwnership(fp *soltype.FuncParam) bool {
	if fp.Rest {
		elem, isArray := c.ctx.restSlotElem(fp.Type)
		return isArray && c.isConcreteOwned(elem)
	}
	return fp.Moved || c.isConcreteOwned(fp.Type)
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
		if _, owned := c.paramDestOf(fp, false).(*ownedDest); !owned {
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
	if obj, ok := objectThroughBorrow(t); ok {
		if prop, found := obj.Prop(name); found {
			return prop.Type
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
	if !c.firstEmit(closure, captureRole, ref, root) {
		return
	}
	// An unannotated parameter's binding renders as `never` until its body settles it, so
	// the capture reads the parameter's own type instead.
	if fp, ok := c.paramByVar[root]; ok {
		t = fp.Type
	}
	holder := eventHolder{kind: tempHolder, node: closure}
	var dest flowDest = &captureDest{writes: writes}
	if moves {
		dest = &ownedDest{}
	}
	c.emitValueFlow(movePlace{root: root}, nil, t, closure, holder, dest, ref)
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
	if id <= 0 || !c.firstEmit(vd, bindingRole, ref, 0) {
		return
	}
	var dest flowDest = &ownedDest{}
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
	if source == nil || !c.firstEmit(pat, patternRole, ref, 0) {
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
	if c.eventTable() == nil {
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
	obj, isObj := objectThroughBorrow(w.c.info.TypeOf(ident))
	if !isObj {
		w.leaf(ident, at, nil)
		return
	}
	for _, elem := range obj.Elems {
		if prop, isProp := elem.(*soltype.PropertyElem); isProp {
			w.leaf(ident, extendPlace(at, prop.Name), appendSeg(nil, prop.Name))
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
		dest = &borrowDest{borrow: borrowKindOf(r.Mut), lt: r.Lt}
	}
	if b, ok := dest.(*borrowDest); ok {
		b.reborrows = true
	}
	holder := eventHolder{kind: placeHolder, place: movePlace{root: id, path: path}}
	w.c.emitValueFlow(at, nil, sourceT, w.source, holder, dest, w.ref)
}

// emitAssignFlow emits the events of the assignment e, attached to ref: the flow of the
// source into the target, then the write of the target. A
// reassigned binding takes the ownership of its type. A field or element store takes the
// ownership of the field it writes when the receiver declares one, and is owned otherwise.
func (c *checker) emitAssignFlow(scope *Scope, e *ast.BinaryExpr, ref liveness.StmtRef) {
	if !c.firstEmit(e, assignRole, ref, 0) {
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
			return &ownedDest{}
		}
		object, name = t.Object, t.Prop.Name
	case *ast.IndexExpr:
		key, ok := constStringKey(t.Index)
		if !ok {
			return &ownedDest{}
		}
		object, name = t.Object, key
	}
	recv := c.info.TypeOf(object)
	if recv == nil {
		return &ownedDest{}
	}
	if prop, ok := c.lookupWriteField(name, recv); ok {
		return c.destOfType(prop.Type)
	}
	return &ownedDest{}
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
	if !c.firstEmit(e, callRole, ref, 0) {
		return
	}
	if recv, self := c.calleeReceiver(e.Callee); recv != nil && self != nil {
		// The method read is how the call reaches the receiver, so the receiver's flow
		// accounts for it.
		c.subsumePlaceChain(e.Callee)
		// self is the method's declared receiver, which every call of the method shares, so
		// its lifetime cannot tell one call's borrow from another's. Each call borrows the
		// receiver under a lifetime of its own.
		dest := &paramDest{param: self, reborrows: true, callLifetime: c.ctx.freshLifetime(lvl)}
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
		return &calleeDest{call: e, index: i}, true
	}
	fp, ok := paramForArg(fn, i)
	if !ok {
		return nil, false
	}
	return &paramDest{param: fp}, true
}

// emitReturnFlow emits the flow of the returned expression into the function's return,
// attached to ref. An annotated return takes the annotation's ownership. One without an
// annotation is an inferredReturnDest. An annotation that did not resolve is owned.
func (c *checker) emitReturnFlow(expr ast.Expr, ref liveness.StmtRef) {
	if !c.firstEmit(expr, returnRole, ref, 0) {
		return
	}
	var dest flowDest = &ownedDest{}
	switch {
	case c.fn.returnAnn != nil:
		dest = c.destOfType(c.fn.returnAnn)
	case !c.fn.returnAnnotated:
		dest = &inferredReturnDest{}
	}
	c.emitFlow(expr, eventHolder{kind: returnHolder}, dest, ref)
}
