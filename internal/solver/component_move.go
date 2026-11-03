package solver

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
)

// The connected-component move. A value that leaves the frame can carry borrows of other
// locals of the body, such as `return {peer: &mut b}` or a returned closure that captures b.
// When nothing else in the frame can still reach those locals, the value takes them along,
// and the move engine records a move of each one at the point the value leaves.
//
// The moves land in moveSites. markMovedParams reads them, so an unannotated parameter that a
// returned closure captures is moved by the body and each call moves the argument passed
// there. In
//
//	fn go(p) {
//		val f = fn () { return p.x }
//		return f
//	}
//
// the return moves p, so `go(o)` moves o. The move engine's use-after-move check reads the
// same moves. Both go with the move engine.

// flowOutSite is one value that leaves the frame, recorded while the body is walked and
// decided by recordComponentMoves once the walk is done. The sites are a `return`, a consuming
// argument, a setter write, and a store into a borrow parameter's field or element.
type flowOutSite struct {
	// expr is the value that leaves.
	expr ast.Expr
	// at is the statement it leaves at.
	at liveness.StmtRef
	// isReturn marks a `return`. The frame is gone after it, so the value need not be an
	// owned aggregate for its component to move. See componentMoveCovers.
	isReturn bool
}

// recordFlowOut records that the value e leaves the frame at the statement `at`, for
// recordComponentMoves to decide once the body is walked.
func (c *checker) recordFlowOut(e ast.Expr, at liveness.StmtRef, isReturn bool) {
	if c.fn == nil || e == nil {
		return
	}
	c.fn.flowOuts = append(c.fn.flowOuts, flowOutSite{expr: e, at: at, isReturn: isReturn})
}

// recordParamStoreFlowOut records the value source a store into the place under recv leaves
// the frame through, when recv names a borrow parameter. A store into a receiver that dies with
// the frame records nothing.
func (c *checker) recordParamStoreFlowOut(recv, source ast.Expr, at liveness.StmtRef) {
	if c.fn == nil {
		return
	}
	rp, ok := c.exprPlace(recv)
	if !ok || rp.root <= 0 || !c.paramReferentOutlivesFrame(rp.root) {
		return
	}
	c.recordFlowOut(source, at, false)
}

// recordComponentMoves records a move of each local that a value leaving the frame carries,
// for each site whose component componentMoveCovers accepts. It must run once the body is
// walked, so the borrow graph is complete. It returns `true` when it recorded a move, so the
// caller knows to recompute the move lattice.
func (c *checker) recordComponentMoves(info *liveness.MoveInfo) bool {
	if len(c.fn.flowOuts) == 0 {
		return false
	}
	flowGraph := c.analyzeBorrows()
	moved := false
	for _, site := range c.fn.flowOuts {
		graph := flowGraph.fieldBorrowGraphBefore(site.at)
		carried := c.carriedLocals(site.expr, graph)
		if carried.Len() == 0 || !c.componentMoveCovers(site, carried, info, graph) {
			continue
		}
		// The value's own root moves at the site already, so it is skipped here. A borrow cycle
		// can lead an edge back to the root, and moving it twice at one point would report a
		// spurious double move.
		var rootID liveness.VarID
		if p, ok := c.exprPlace(site.expr); ok {
			rootID = p.root
		}
		ids := carried.ToSlice()
		slices.Sort(ids)
		for _, id := range ids {
			if id != rootID {
				c.recordMove(id, site.expr, site.at)
			}
		}
		moved = true
	}
	c.fn.flowOuts = nil
	return moved
}

// carriedLocals returns the locals of the body whose data the value e carries. Three sources
// contribute:
//
//   - a borrow of a local written anywhere in e, such as the `&mut b` in `{peer: &mut b}`;
//   - each local a closure written in e captures;
//   - the locals the edges under the place e names reach. A read of a.peer follows only the
//     edges on the [peer] path, as collectEdgesFrom describes.
func (c *checker) carriedLocals(e ast.Expr, graph map[liveness.VarID][]fieldBorrow) set.Set[liveness.VarID] {
	out := set.NewSet[liveness.VarID]()
	for _, b := range borrowsIn(e) {
		if referent, ok := c.isLocalReferent(b.Arg); ok {
			out.Add(referent)
		}
	}
	for _, closure := range closuresIn(e) {
		for _, captured := range c.fn.capturedLocals[closure] {
			out.Add(captured.root)
		}
	}
	if p, ok := c.exprPlace(e); ok && p.root > 0 && !c.readsPlainValue(e, p, graph) {
		var edges []fieldBorrow
		collectEdgesFrom(p.root, p.path, set.NewSet[liveness.VarID](), graph, &edges)
		for _, edge := range edges {
			out.Add(edge.referent)
		}
	}
	return out
}

// readsPlainValue reports whether e, a read of the place p, copies a value type that reaches
// no local. With `r: &{x: number}`, the read `r.x` copies a number. A closure is a value type
// that still reaches what it captures, so a read of a binding with a capture edge in graph
// does not count.
func (c *checker) readsPlainValue(e ast.Expr, p movePlace, graph map[liveness.VarID][]fieldBorrow) bool {
	t := c.info.TypeOf(e)
	if t == nil || !c.holdsValueType(t) {
		return false
	}
	return !slices.ContainsFunc(graph[p.root], func(edge fieldBorrow) bool { return edge.capture })
}

// componentMoveCovers reports whether the value of site takes its component along. The
// component is the value's root together with the locals in `carried`. Two conditions must
// hold:
//
//   - The site is a return, or its value is an owned aggregate that escapesAsOwnedCarrier
//     accepts. A bare borrow such as `&mut b` leaving through a store or an argument leaves b
//     reachable from the frame, so it takes nothing along.
//   - No binding outside the component that is live after the site borrows a node inside it.
//     A binding moved before the site, or dead after it, does not count. After a return only
//     a parameter can still borrow into the component.
func (c *checker) componentMoveCovers(
	site flowOutSite,
	carried set.Set[liveness.VarID],
	info *liveness.MoveInfo,
	graph map[liveness.VarID][]fieldBorrow,
) bool {
	if !site.isReturn && !c.escapesAsOwnedCarrier(site.expr, graph) {
		return false
	}
	component := carried.Clone()
	if p, ok := c.exprPlace(site.expr); ok && p.root > 0 {
		component.Add(p.root)
	}
	for root, edges := range graph {
		if component.Contains(root) {
			continue
		}
		if info.StateBefore(site.at, root) == liveness.Moved {
			continue
		}
		if c.fn.liveness != nil && !c.fn.liveness.IsLiveAfter(site.at, root) {
			continue
		}
		if slices.ContainsFunc(edges, func(edge fieldBorrow) bool { return component.Contains(edge.referent) }) {
			return false
		}
	}
	return true
}

// escapesAsOwnedCarrier reports whether e is an owned aggregate that holds borrows in its
// fields, rather than a borrow itself. The recorded type of e cannot answer this, since a
// field read auto-derefs a borrow field to its owned inner. The borrow graph answers it:
//
//   - A `&mut b` or `&b` expression is a borrow.
//   - A fresh object or tuple literal is an owned aggregate.
//   - A place is an owned aggregate unless a borrow edge sits at or above the place read.
//     `a` over a → b at [peer] reads the owned object a, while `a.peer` over the same edge
//     reads the borrow, and so does `a` over a → b at [].
//   - Any other expression, such as an `if`/`else` or a call result, counts as a borrow.
func (c *checker) escapesAsOwnedCarrier(e ast.Expr, graph map[liveness.VarID][]fieldBorrow) bool {
	switch e.(type) {
	case *ast.BorrowExpr:
		return false
	case *ast.ObjectExpr, *ast.TupleExpr:
		return true
	}
	p, ok := c.exprPlace(e)
	if !ok || p.root <= 0 {
		return false
	}
	return !slices.ContainsFunc(graph[p.root], func(edge fieldBorrow) bool { return pathHasPrefix(p.path, edge.path) })
}
