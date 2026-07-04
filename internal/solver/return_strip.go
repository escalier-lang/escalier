package solver

import (
	"maps"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Return borrow-stripping rewrites a returned borrow of a self-contained local graph into the
// owned pointee. The connected-component move has already re-anchored the nodes and consumed the
// locals, so the return value is their sole owner, and owning them in the type is honest. `return
// a` over `val a = {peer: &mut d}` then returns `{peer: {value: number}}` rather than `{peer:
// &mut {value: number}}`.
//
// The reachable shape from the return decides whether a borrow is stripped:
//
//   - Tree: every borrowed local is reached exactly once with no cycle. Strip every borrow to
//     its owned pointee, since the return value uniquely owns each node.
//   - Diamond or cycle: a node is reached through two or more paths, or sits on a cycle. Keep
//     the borrows. An owned tree cannot represent a shared node without duplicating it, which
//     would hide mutations across the shared paths.
//
// The carrier the return names decides whether stripping runs at all:
//
//   - A direct place or object/tuple literal is eligible.
//   - A call result such as `return id(a)` is left borrowed, since its borrows are hidden behind
//     the call boundary. This is sound. The component move already keeps the nodes alive.
//   - A parameter borrow is never stripped, since it carries no local edge.

// stripReturnBorrowsIfTree rewrites the return type of the return whose value is e to own the
// data when e's reachable borrow graph is a tree. It is a no-op when e is not a return value,
// when the carrier is not a direct place or literal, or when the graph is not a tree. The
// borrow-edge graph read is c.fn.eagerBorrowGraph, which resolveComponentEscapes has set to the
// snapshot at this return's program point.
func (c *checker) stripReturnBorrowsIfTree(e ast.Expr) {
	idx := c.returnIndexOf(e)
	if idx < 0 {
		return
	}
	graph, root, ok := c.carrierGraph(e)
	if !ok {
		return
	}
	if !isTreeReachable(graph, root) {
		return
	}
	c.fn.returns[idx] = stripBorrowTree(c.fn.returns[idx], root, nil, graph)
}

// returnIndexOf returns the index of the return whose operand is e, or -1 when e is not a
// recorded return value. The returns and returnExprs slices are parallel, so the index into
// returnExprs selects the return type to rewrite.
func (c *checker) returnIndexOf(e ast.Expr) int {
	for i, re := range c.fn.returnExprs {
		if re == e {
			return i
		}
	}
	return -1
}

// carrierGraph returns the borrow-edge graph and the carrier root for e's borrow fields. A
// whole-binding place is already a node in the graph, so it uses its own VarID as the root and
// returns c.fn.eagerBorrowGraph directly. That graph is the snapshot resolveComponentEscapes
// swapped in for this return's program point, so no cloning is needed. An object or tuple literal
// has no binding node, so it is walked into a private copy under a synthetic root. Any other
// carrier reports ok=false.
func (c *checker) carrierGraph(e ast.Expr) (map[liveness.VarID][]fieldBorrow, liveness.VarID, bool) {
	if p, ok := exprPlace(e); ok && p.root > 0 && len(p.path) == 0 {
		return c.fn.eagerBorrowGraph, p.root, true
	}
	switch e.(type) {
	case *ast.ObjectExpr, *ast.TupleExpr:
		// Walk the literal into a private copy so the shared snapshot is not mutated, using a
		// synthetic root drawn from the module-wide counter so it never collides with a binding.
		saved := c.fn.eagerBorrowGraph
		// saved is non-nil here. resolveComponentEscapes set eagerBorrowGraph to a non-nil
		// snapshot before return-stripping runs, so this clone is non-nil and the
		// recordBorrowSources writes below cannot panic on a nil map.
		c.fn.eagerBorrowGraph = maps.Clone(saved)
		root := liveness.VarID(c.varIDCounter)
		c.varIDCounter++
		// The literal has no binding, so nothing in the graph describes its borrows. Record the
		// edges it carries under the synthetic root, so stripReturnBorrowsIfTree can treat it like
		// a binding whose edges the eager walk had recorded.
		c.recordBorrowSources(root, nil, e)
		graph := c.fn.eagerBorrowGraph
		c.fn.eagerBorrowGraph = saved
		return graph, root, true
	}
	return nil, 0, false
}

// isTreeReachable reports whether the borrow graph reachable from root is a tree: every reached
// node is reached exactly once and no cycle routes back onto a reached node. It records each node
// it reaches in a seen set and recurses into it the first time. Reaching a node already seen — a
// shared node or a cycle's back edge — means the graph is not a tree, so it returns false at once
// without walking the rest. The recursion terminates because a node is descended only on its first
// reach.
func isTreeReachable(graph map[liveness.VarID][]fieldBorrow, root liveness.VarID) bool {
	seen := set.NewSet[liveness.VarID]()
	var visit func(node liveness.VarID) bool
	visit = func(node liveness.VarID) bool {
		for _, e := range graph[node] {
			if seen.Contains(e.referent) {
				// A second reach — a shared node or a cycle's back edge — breaks the tree.
				return false
			}
			seen.Add(e.referent)
			if !visit(e.referent) {
				return false
			}
		}
		return true
	}
	return visit(root)
}

// stripBorrowTree returns t with every borrow of a tracked local replaced by the borrow's
// owned pointee, following the borrow-edge graph in parallel with the type. root is the
// binding whose edges describe t's borrow fields, and path is the field path within root
// reached so far.
//
//   - A borrow RefType at path whose referent the graph names is stripped: the result is its
//     pointee, walked with root switched to the referent and the path reset, since the
//     referent's own fields are keyed under it. A borrow with no local edge, such as a
//     parameter borrow, is kept unchanged.
//   - An owned-mutable cell is rebuilt around its walked inner at the same root and path.
//   - An object descends each property at the path extended by the property name; a tuple
//     descends each element at the same path, since a tuple index contributes no field segment.
func stripBorrowTree(
	t soltype.Type,
	root liveness.VarID,
	path []placeSeg,
	graph map[liveness.VarID][]fieldBorrow,
) soltype.Type {
	switch t := t.(type) {
	case *soltype.RefType:
		if t.Lt != nil {
			referent, ok := findReferentAt(graph, root, path)
			if !ok {
				return t
			}
			return stripBorrowTree(t.Inner, referent, nil, graph)
		}
		// An owned-mutable cell rebuilds around its walked inner. The inner is an object or
		// tuple, so its strip stays a RefInner; keep the cell unchanged if that ever fails to
		// hold rather than panicking.
		inner, ok := stripBorrowTree(t.Inner, root, path, graph).(soltype.RefInner)
		if !ok {
			return t
		}
		return soltype.NewRef(t.Mut, nil, inner)
	case *soltype.ObjectType:
		elems := make([]soltype.ObjTypeElem, len(t.Elems))
		for i, e := range t.Elems {
			p := soltype.AsProperty(e)
			var propPath []placeSeg
			if isDotPlaceSegment(p.Name) {
				propPath = appendSeg(path, p.Name)
			} else {
				propPath = path
			}
			elems[i] = &soltype.PropertyElem{
				Name:     p.Name,
				Type:     stripBorrowTree(p.Type, root, propPath, graph),
				Optional: p.Optional,
				Readonly: p.Readonly,
			}
		}
		return &soltype.ObjectType{Elems: elems, Inexact: t.Inexact}
	case *soltype.TupleType:
		// Keep a tuple's borrows unstripped. Every tuple element's borrow is recorded at the
		// container path, since placeSeg has no tuple-index kind yet, so findReferentAt cannot
		// tell which element a same-path edge belongs to. Recursing each element at the shared
		// path would strip it with a sibling's referent and follow the wrong subgraph. Leaving
		// the tuple borrowed is sound: the component move still consumes the borrowed locals,
		// only the type is not rewritten to owned. Per-element stripping lands once a tuple
		// index yields its own place segment.
		return t
	default:
		return t
	}
}

// findReferentAt returns the local root's edge exactly at path, the borrow that field holds.
// A borrow field at path P records its edge at P, so the lookup is an exact path match. A path
// with no edge names no tracked borrow, so it reports ok=false and the borrow is kept.
//
// A parameter borrow is the common ok=false case. For a parameter p, `return {peer: &mut p}`
// leaves a RefType at [peer] in the return type, but recordBorrowSources added no edge there,
// since isLocalReferent skips a borrow of a parameter. So findReferentAt reports ok=false and the
// `&mut p` stays borrowed.
func findReferentAt(graph map[liveness.VarID][]fieldBorrow, root liveness.VarID, path []placeSeg) (liveness.VarID, bool) {
	for _, e := range graph[root] {
		if slices.Equal(e.path, path) {
			return e.referent, true
		}
	}
	return 0, false
}
