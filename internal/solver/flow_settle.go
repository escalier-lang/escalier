package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// This file decides the event kind of each flow. It classifies a flow's source and
// destination, holds the flows F3 has not settled yet, and settles them once the
// unannotated parameter, closure capture, or callee they wait on is known.

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
		return &borrowDest{borrow: borrowKindOf(b.mut), lt: b.lt}
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
// the place. settled is false when F3 has not settled the source or the destination. The
// event is then nil and wait names what the flow waits on.
//
// force settles every such flow the way the current solver treats what it cannot see: an
// unsettled parameter is read as it stands, a callee with no signature borrows its
// argument, and a closure capture settles from the holder the closure has then.
// forceCapture settles only that last case.
func (c *checker) classifyFlow(table *ownershipEvents, flow flowBase, t soltype.Type, dest flowDest, force, forceCapture bool) (ownershipEvent, flowWait, bool) {
	dest, wait, ok := c.settleDest(table, dest, flow.holder, force, force || forceCapture)
	if !ok {
		return nil, wait, false
	}

	src, wait, ok := c.sourceKindOf(flow.place, t, force)
	if !ok {
		return nil, wait, false
	}

	switch src {
	case valueSource:
		return &useEvent{eventBase: flow.eventBase, access: readAccess}, flowWait{}, true
	case borrowSource:
		if b, ok := dest.(*borrowDest); ok && b.reborrows {
			return &borrowEvent{flowBase: flow, borrow: b.borrow, lifetime: b.lt}, flowWait{}, true
		}
		return &copyEvent{flowBase: flow}, flowWait{}, true
	case ownedSource:
	}

	if b, ok := dest.(*borrowDest); ok {
		return &borrowEvent{flowBase: flow, borrow: b.borrow, lifetime: b.lt}, flowWait{}, true
	}
	return &moveEvent{flowBase: flow}, flowWait{}, true
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
		switch holder.target.(type) {
		case returnTarget, moduleTarget:
			return &ownedDest{}, flowWait{}, true
		case tempTarget, placeTarget:
			if !forceCapture {
				return dest, flowWait{capture: true}, false
			}

			if pt, ok := holder.target.(placeTarget); ok && table.escapes(pt.root) {
				return &ownedDest{}, flowWait{}, true
			}
		case argTarget, receiverTarget:
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
	if b, ok := c.borrowOfType(fp.Type); ok {
		return &borrowDest{borrow: borrowKindOf(b.mut), lt: b.lt, reborrows: reborrows}
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

	if _, ok := c.borrowOfType(t); ok {
		return borrowSource, flowWait{}, true
	}
	return ownedSource, flowWait{}, true
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

	if u, ok := c.expandAliasChain(t).(*soltype.UnionType); ok {
		for _, member := range u.Types {
			if !c.holdsValueType(member) {
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
		t := pf.sourceT
		if pf.source != nil {
			t = c.info.TypeOf(pf.source)
		}

		ev, next, settled := c.classifyFlow(pf.table, placeholder.flowBase, t, pf.dest, force, forceCapture)
		if !settled {
			c.addPendingFlow(pf, next)
			continue
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
