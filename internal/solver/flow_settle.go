package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// This file decides the event kind of each flow. It classifies a flow's source and
// destination, holds the flows whose destination is not settled yet, and settles them once
// the closure capture or callee they wait on is known.

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
	if b, ok := c.borrowOfType(t); ok {
		return &borrowDest{borrow: borrowKindOf(b.mut), lt: b.lt, writable: b.anyMut}
	}
	return &ownedDest{}
}

// borrowKindOf returns `&mut` for a mutable borrow and `&` otherwise.
func borrowKindOf(mut bool) borrowKind {
	if mut {
		return mutableBorrow
	}
	return immutableBorrow
}

// classifyFlow returns the event F2 gives flow, whose value has type t, through dest.
// The event carries flow's place, node, and CFG node. table holds the flow's event. A
// value type returns a read *useEvent with no holder, since copying it is only a read of
// the place. settled is false when the destination is not settled yet. The event is then
// nil and wait names what the flow waits on.
//
// force settles every such flow the way the current solver treats what it cannot see: a
// callee with no signature borrows its argument, and a closure capture settles from the
// holder the closure has then. forceCapture settles only that last case.
func (c *checker) classifyFlow(table *ownershipEvents, flow flowBase, t soltype.Type, dest flowDest, force, forceCapture bool) (ownershipEvent, flowWait, bool) {
	dest, wait, ok := c.settleDest(table, dest, flow.holder, force, force || forceCapture)
	if !ok {
		return nil, wait, false
	}

	switch c.sourceKindOf(t) {
	case valueSource:
		return &useEvent{eventBase: flow.eventBase, access: readAccess}, flowWait{}, true
	case borrowSource:
		b, toBorrow := dest.(*borrowDest)
		if toBorrow && b.reborrows {
			return &borrowEvent{flowBase: flow, borrow: b.borrow, lifetime: b.lt}, flowWait{}, true
		}

		kind := c.sourceBorrowKind(t)
		ev := &copyEvent{flowBase: flow, source: kind, borrow: kind}
		if toBorrow {
			ev.borrow = b.narrow(kind)
			ev.lifetime = b.lt
		}
		return ev, flowWait{}, true
	case ownedSource:
	}

	if b, ok := dest.(*borrowDest); ok {
		return &borrowEvent{flowBase: flow, borrow: b.borrow, lifetime: b.lt}, flowWait{}, true
	}
	return &moveEvent{flowBase: flow}, flowWait{}, true
}

// settleDest resolves a destination whose ownership is decided elsewhere to an owned,
// borrow, or inferred-return destination. ok is false when that is not decided yet, and
// wait then names what it waits on. force and forceCapture are the flags classifyFlow
// describes.
func (c *checker) settleDest(table *ownershipEvents, dest flowDest, holder eventHolder, force, forceCapture bool) (flowDest, flowWait, bool) {
	switch d := dest.(type) {
	case *paramDest:
		settled := c.paramDestOf(d.param, d.reborrows)
		if b, ok := settled.(*borrowDest); ok && d.callLifetime != nil {
			settled = &borrowDest{borrow: b.borrow, lt: d.callLifetime, reborrows: b.reborrows, writable: b.writable}
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
		// A closure stored into module-level state may run during any later call, so it
		// borrows what it captures for `'static`. A closure returned where it is written
		// outlives the frame and owns what it captures. Any other closure borrows what it
		// captures, and captureReturnMoves moves the capture at each return of a local
		// holding the closure. A closure a local holds may still be stored through that
		// local, so its capture waits for the whole body.
		static := &borrowDest{borrow: borrowKindOf(d.writes), lt: soltype.Static, reborrows: true}
		switch holder.target.(type) {
		case returnTarget:
			return &ownedDest{}, flowWait{}, true
		case moduleTarget:
			return static, flowWait{}, true
		case tempTarget, placeTarget:
			if !forceCapture {
				return dest, flowWait{capture: true}, false
			}

			if pt, ok := holder.target.(placeTarget); ok && table.storedFrom(pt.root) {
				return static, flowWait{}, true
			}
		case argTarget, receiverTarget:
		}

		return &borrowDest{borrow: borrowKindOf(d.writes), reborrows: true}, flowWait{}, true
	case *ownedDest, *borrowDest, *inferredReturnDest:
	}

	return dest, flowWait{}, true
}

// paramDestOf returns the destination an argument passed to fp has. A borrow parameter
// borrows. A parameter paramTakesOwnership accepts takes ownership. Any other parameter,
// such as one typed `unknown`, borrows immutably.
func (c *checker) paramDestOf(fp *soltype.FuncParam, reborrows bool) flowDest {
	if b, ok := c.borrowOfType(fp.Type); ok {
		return &borrowDest{borrow: borrowKindOf(b.mut), lt: b.lt, reborrows: reborrows, writable: b.anyMut}
	}

	if c.paramTakesOwnership(fp) {
		return &ownedDest{}
	}
	return &borrowDest{borrow: immutableBorrow, reborrows: reborrows}
}

// paramTakesOwnership reports whether an argument passed to fp is owned by the callee. A
// parameter isTypeArgParam accepts takes ownership, as the parameter of `fn id<T>(t: T) -> T`
// does in the call `id(xs)`. A rest parameter takes ownership when its element type is a
// concrete owned shape. Any other parameter takes ownership when its body moves it or its
// type is a concrete owned shape.
func (c *checker) paramTakesOwnership(fp *soltype.FuncParam) bool {
	if c.ctx.isTypeArgParam(fp) {
		return true
	}
	if fp.Rest {
		elem, isArray := c.ctx.restSlotElem(fp.Type)
		return isArray && c.isConcreteOwned(elem)
	}
	return fp.Moved || c.isConcreteOwned(fp.Type)
}

// sourceKindOf classifies a flow's source by its type t. A value type is a value source, a
// borrow is a borrow source, and any other type is an owned source.
func (c *checker) sourceKindOf(t soltype.Type) sourceKind {
	if c.holdsValueType(t) {
		return valueSource
	}

	if _, ok := c.borrowOfType(t); ok {
		return borrowSource
	}
	return ownedSource
}

// sourceBorrowKind returns the mutability of a borrow source of type t. A borrow whose type
// has a `&mut` member is mutable, so `&mut A | &A` is `&mut`.
func (c *checker) sourceBorrowKind(t soltype.Type) borrowKind {
	if b, ok := c.borrowOfType(t); ok {
		return borrowKindOf(b.anyMut)
	}
	return immutableBorrow
}

// holdsValueType reports whether a value of type t is a value type, which a flow copies. A
// nil t counts as one, since it names no value a flow can move. A variable is a value type
// when every value it can hold is one. So is a union whose members all are, such as
// `number | undefined`, but not `{x: number} | null`. Any other t is decided by
// isValueShaped.
func (c *checker) holdsValueType(t soltype.Type) bool {
	if t == nil {
		return true
	}

	// unionMembers flattens nested unions and aliases, and reads an alias that names itself
	// once, so `type Loop = Loop | number` yields `number` instead of expanding forever.
	if _, ok := c.expandAliasChain(t).(*soltype.UnionType); ok {
		for _, member := range c.unionMembers(t) {
			if !c.isValueShaped(member) {
				return false
			}
		}
		return true
	}

	return c.isValueShaped(t)
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
		placeholder := pf.table.events[pf.index].(*provisionalEvent)
		t := c.flowSourceType(pf)
		ev, next, settled := c.classifyFlow(pf.table, placeholder.flowBase, t, pf.dest, force, forceCapture)
		if !settled {
			c.addPendingFlow(pf, next)
			continue
		}

		pf.table.events[pf.index] = ev
		c.reportModuleLevelMove(pf.table, ev, pf.dest, t)
		if _, isCapture := pf.dest.(*captureDest); isCapture {
			c.captureReturnMoves(pf.table, placeholder.flowBase, t)
		}
	}
}

// captureReturnMoves appends to table a move of the captured place at each return of a local
// that holds the closure. capture is the settled capture's flow, and t is the type of the
// captured place. A capture whose place holds a borrow or a value type moves nothing, since
// F2 copies it into the owned return. Neither does the capture of a closure stored into
// module-level state, which keeps its `'static` borrow wherever else the closure goes.
func (c *checker) captureReturnMoves(table *ownershipEvents, capture flowBase, t soltype.Type) {
	pt, ok := capture.holder.target.(placeTarget)
	if !ok || table.storedFrom(pt.root) {
		return
	}

	closure, ok := capture.node.(ast.Func)
	if !ok {
		return
	}

	for _, site := range table.returnsOf(pt.root) {
		flow := flowBase{
			eventBase: eventBase{place: capture.place, node: site.node},
			holder:    eventHolder{target: returnTarget{}},
		}

		ev, _, _ := c.classifyFlow(table, flow, t, &ownedDest{}, true, true)
		if mv, isMove := ev.(*moveEvent); isMove {
			mv.closure = closure
			// A closure that never flows out of the local that holds it reaches a return of
			// that local only while the local holds the closure's borrow. Reassigning the
			// local drops that borrow, so such a return moves nothing.
			mv.whileCaptured = len(table.reachedFrom(pt.root)) == 1
			mv.at = site.at
			table.events = append(table.events, mv)
		}
	}
}

// settleAllFlows settles every provisional flow, and the destination of every written borrow
// finishOwnershipEvents could not settle, the way classifyFlow's force flag describes. It runs
// once a module or script is inferred, when nothing is left to settle them.
func (c *checker) settleAllFlows() {
	for wait := range c.pendingFlows {
		c.settleFlowsWaitingOn(wait, true, true)
	}

	for _, t := range c.info.ownership {
		for _, ev := range t.events {
			if bev, ok := ev.(*borrowEvent); ok {
				c.borrowFromDest(t, bev, true)
			}
		}
	}
}

// flowSourceType returns the type of the value the provisional flow pf passes. It is the type
// of pf's source expression, or sourceT when pf has none.
func (c *checker) flowSourceType(pf provisionalFlow) soltype.Type {
	if pf.source != nil {
		return c.info.TypeOf(pf.source)
	}
	return pf.sourceT
}
