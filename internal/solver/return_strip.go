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
// locals, so the return value is their sole owner, and owning them in the type is honest.
// `return a` over `val a = {peer: &mut d}` returns `{peer: {value: number}}` rather than
// `{peer: &mut {value: number}}`, and `return &mut d` returns `{value: number}`.
//
// The owned form carries no mutability of its own. Ownership and mutability are separate in
// Escalier, and the binding decides: a caller writes `val mut r = f()` to take the value
// mutably and a plain `val r` to keep it frozen. Stamping `mut` on the return would hand every
// caller a mutable value whether or not they asked for one, where the default is immutability.
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

// returnStripFor returns the index of the return whose value is e and the owned type its
// borrows strip to, when e's reachable borrow graph is a tree. ok is false when e is not a
// return value, when the carrier is not a direct place, literal, or borrow, when the graph is
// not a tree, or when the walk changes nothing. snapshot is the flow-sensitive borrow-edge
// graph at this return's program point, which resolveComponentEscapes reads from the dataflow
// and passes in.
func (c *checker) returnStripFor(
	e ast.Expr,
	snapshot map[liveness.VarID][]fieldBorrow,
) (int, soltype.Type, bool) {
	idx := c.returnIndexOf(e)
	if idx < 0 {
		return 0, nil, false
	}
	graph, root, ok := c.carrierGraph(e, snapshot)
	if !ok {
		return 0, nil, false
	}
	if !isTreeReachable(graph, root) {
		return 0, nil, false
	}
	stripped := stripBorrowTree(c.fn.returns[idx], root, nil, graph)
	if stripped == c.fn.returns[idx] {
		return 0, nil, false
	}
	return idx, stripped, true
}

// applyReturnStrips writes the collected rewrites onto the function's return types, or writes
// none of them.
//
// A function's returns are unioned, and Escalier rejects a union that mixes an owned member
// with a borrowed one. Rewriting only some of them builds exactly that. In
//
//	fn f(p: &mut B, cond: boolean) {
//		val mut b = {value: 0}
//		if cond { return &mut b }
//		return p
//	}
//
// only the first return strips, since a parameter borrow carries no local edge. Owning that one
// and leaving `return p` borrowed would union `mut B` with `&'a mut B` and reject the whole
// function. Holding the rewrite back leaves both borrowed, which is uniform and checks.
func (c *checker) applyReturnStrips(strips map[int]soltype.Type) {
	if len(strips) == 0 {
		return
	}
	for i, t := range c.fn.returns {
		if _, rewritten := strips[i]; rewritten {
			continue
		}
		if ref, isRef := t.(*soltype.RefType); isRef && ref.Lt != nil {
			return
		}
	}
	for i, t := range strips {
		c.fn.returns[i] = t
	}
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

// carrierGraph returns the borrow-edge graph for e's borrows and the root its edges hang off,
// drawing from snapshot, the per-program-point graph. e's own type starts at that root, so the
// strip walks from the root path down.
//
//   - A whole-binding place is already a node in snapshot, so it returns snapshot and the
//     binding's own VarID, no cloning needed.
//   - An object literal, a tuple literal, and a `&mut b` borrow have no binding node, so each is
//     walked into a private clone of snapshot under a synthetic root.
//   - Any other carrier reports ok=false, a field read among them. `return a.peer` records the
//     property's type as a variable the evaluator settles later, so there is no RefType here to
//     rewrite. The component move still consumes the borrowed local; only the type keeps its
//     borrow.
func (c *checker) carrierGraph(e ast.Expr, snapshot map[liveness.VarID][]fieldBorrow) (map[liveness.VarID][]fieldBorrow, liveness.VarID, bool) {
	if p, ok := exprPlace(e); ok && p.root > 0 && len(p.path) == 0 {
		return snapshot, p.root, true
	}
	switch e.(type) {
	case *ast.ObjectExpr, *ast.TupleExpr, *ast.BorrowExpr:
		// Walk the literal into a private clone of snapshot under a synthetic root drawn from the
		// module-wide counter so it never collides with a binding. The clone goes through the
		// eagerBorrowGraph field because recordBorrowSources and addBorrowEdge write there; the
		// field is saved and restored so nothing outside sees the mutation. snapshot is non-nil
		// (fieldBorrowGraphBefore returns a non-nil map), so the clone is non-nil and those writes
		// cannot panic on a nil map.
		saved := c.fn.eagerBorrowGraph
		c.fn.eagerBorrowGraph = maps.Clone(snapshot)
		root := liveness.VarID(c.varIDCounter)
		c.varIDCounter++
		// The carrier has no binding, so nothing in the graph describes its borrows. Record the
		// edges it carries under the synthetic root, so returnStripFor can treat it like
		// a binding whose edges the eager walk had recorded. A `&mut b` borrow records one edge
		// at the root path, which is what makes `return &mut b` strip to b's owned type.
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
			// The owned form carries no mutability of its own. Ownership and mutability are
			// separate in Escalier: the binding decides, so a caller writes `val mut r = f()`
			// to take the value mutably and a plain `val r` to keep it frozen. Stamping `mut`
			// here would hand every caller a mutable value whether or not they asked, where the
			// default is immutability.
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
		// A residual object has no settled property list to walk, the same guard stripOwnedMut
		// applies. Its borrows are reached once the evaluator reduces it.
		if soltype.HasResidualElem(t.Elems) {
			return t
		}
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
