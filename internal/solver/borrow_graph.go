package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// The borrow-edge graph records, for each binding of a body, the function-locals it borrows
// and the locals each closure it holds captures. `val a = {peer: &mut b}` records the edge
// a → b at path [peer], and `val f = fn () { b.v }` records the capture edge f → b. The
// dataflow pass reads the graph to find the closures a call runs and the locals a retained
// closure writes. See closureRunOf and noteRetainedClosureWrites in ownership_runs.go.
//
// Edges are recorded at these sites:
//
//   - a `val`/`var` initializer and a destructuring leaf;
//   - a `var` reassignment;
//   - a field or element store into a receiver that dies with the frame;
//   - a call whose signature stores an argument-borrow into another argument, which
//     borrow_store.go describes;
//   - a function declaration, and the capture edges body_prepass.go seeds for a recursive
//     closure and for each module function.
//
// The eager graph holds the edges as of the statement the walk is at. borrow_flow.go folds
// what each statement records into a graph for every program point.

// fieldBorrow is one borrow edge under a binding: the field path within the binding that
// holds the borrow, and the function-local the borrow refers to. The path is empty when
// the whole binding is the borrow, as in `var a = &mut b`, and names the field chain when
// a field holds it, as in `val a = {peer: &mut b}` recording path [peer].
type fieldBorrow struct {
	path     []placeSeg
	referent liveness.VarID
	// refPath is the field path INSIDE referent the borrow reaches, empty for a borrow of the
	// whole binding. `val t = {p: &mut b.x}` records refPath [x] beside path [p]. It is what
	// separates a borrow of one field of a local from a borrow of a disjoint field.
	refPath []placeSeg
	// mut says a write can go through the borrow. `val a = {peer: &mut b}` records a mutable
	// edge and `val a = {peer: &b}` an immutable one. A borrow field carries its own
	// mutability, so an edge reached through another edge keeps its own answer.
	mut bool
	// `capture` marks an edge recorded because a closure captures `referent`.
	// `val f = fn () { b.v }` records f → b with `capture` set.
	capture bool
}

// pathHasPrefix reports whether prefix is a prefix of full: every segment of prefix matches
// full at the same index, so an empty prefix matches any path and equal paths match.
func pathHasPrefix(full, prefix []placeSeg) bool {
	if len(prefix) > len(full) {
		return false
	}
	for i := range prefix {
		if prefix[i] != full[i] {
			return false
		}
	}
	return true
}

// borrowCollector gathers the BorrowExprs an expression carries by value, riding the
// shared AST visitor so it reaches a borrow nested in an object or tuple literal, a spread,
// or a control-flow carrier such as an if/else branch. It stops at a call or
// nested-function boundary, where a borrow is not part of the value the scanned expression
// yields.
type borrowCollector struct {
	*ast.DefaultVisitor
	out *[]*ast.BorrowExpr
	// `closures`, when set, gathers the closures the expression carries by value.
	closures *[]*ast.FuncExpr
}

func (v *borrowCollector) EnterExpr(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.BorrowExpr:
		*v.out = append(*v.out, e)
	case *ast.FuncExpr:
		// A borrow inside a nested function belongs to that function's scope. The closure
		// itself is part of the value, and what it captures is read from capturedLocals.
		if v.closures != nil {
			*v.closures = append(*v.closures, e)
		}
		return false
	case *ast.CallExpr, *ast.TaggedTemplateLitExpr:
		// A borrow written as a call argument is consumed or borrowed by that call, not
		// carried out by its result, so `store(read(&mut b))` does not carry b.
		return false
	}
	return true
}

// closuresIn returns every closure the expression e carries by value, descending the way
// borrowsIn does.
func closuresIn(e ast.Expr) []*ast.FuncExpr {
	var borrows []*ast.BorrowExpr
	var found []*ast.FuncExpr
	e.Accept(&borrowCollector{DefaultVisitor: &ast.DefaultVisitor{}, out: &borrows, closures: &found})
	return found
}

// borrowsIn returns every BorrowExpr the expression e carries by value, descending through
// object and tuple literals, spreads, and control-flow carriers but stopping at call and
// nested-function boundaries.
func borrowsIn(e ast.Expr) []*ast.BorrowExpr {
	var found []*ast.BorrowExpr
	e.Accept(&borrowCollector{DefaultVisitor: &ast.DefaultVisitor{}, out: &found})
	return found
}

// paramReferentOutlivesFrame reports whether `root` names a parameter whose referent belongs to
// the caller and so survives the call.
//
// Only a BORROW parameter does. `&T` and `&mut T` name data the caller still holds, so a borrow
// of a local written into one reaches the caller after the frame ends. An owned parameter,
// `p: T` or `mut p: T`, is MOVED into the frame. The caller gave up every handle at the call,
// and the value dies with the frame, so nothing written into it can outlive anything.
//
// A settled type answers directly. A `RefType` carrying a lifetime is a borrow. Anything else
// settled is owned: owned-immutable collapses to the bare inner, so a plain `T` parameter is
// recorded as its concrete type rather than wrapped, and owned-mutable is a `RefType` with no
// lifetime.
//
// An unsettled type keeps the stricter caller-owned answer. That covers a leaf the seed did not
// reach and an UNANNOTATED parameter, whose recorded type is an inference variable this reads
// as a leaf rather than following its bounds. So an unannotated owned parameter counts as
// caller-owned, where the same parameter written `mut T` does not.
func (c *checker) paramReferentOutlivesFrame(root liveness.VarID) bool {
	if c.fn == nil || !c.fn.paramVarIDs.Contains(root) {
		return false
	}
	// A closure borrows what it captures, so the enclosing body owns it.
	if c.fn.captureVarIDs.Contains(root) {
		return true
	}
	if c.fn.varIDTypes == nil {
		return true
	}
	t, ok := c.fn.varIDTypes[root]
	if !ok {
		return true
	}
	switch t := t.(type) {
	case *soltype.RefType:
		return t.Lt != nil
	case *soltype.TypeVarType:
		// Unsettled, so which kind it becomes is not known here. The stricter answer treats
		// its referent as the caller's.
		return true
	}
	return false
}

// isLocalReferent reports whether the borrow operand names a function-local place, one
// rooted at a real binding that is not a parameter. A parameter referent is exempt, and a
// non-place operand names no tracked binding.
func (c *checker) isLocalReferent(arg ast.Expr) (liveness.VarID, bool) {
	p, ok := c.localReferentPlace(arg)
	return p.root, ok
}

// localReferentPlace is isLocalReferent keeping the field path within the local, so a caller
// recording an edge can say which part of the referent the borrow reaches. `&mut b.x` gives
// root b and path [x].
func (c *checker) localReferentPlace(arg ast.Expr) (movePlace, bool) {
	p, ok := c.exprPlace(arg)
	if !ok || p.root <= 0 {
		return movePlace{}, false
	}
	if c.fn.paramVarIDs.Contains(p.root) {
		return movePlace{}, false
	}
	return p, true
}

// addBorrowEdge records that the binding root borrows the function-local referent at the
// given field path, allocating the root's edge list on first use. mut says a write can go
// through the borrow. A duplicate edge with the same paths, referent and mutability is
// ignored, so repeated walks keep one copy rather than accumulating identical edges.
func (c *checker) addBorrowEdge(root liveness.VarID, path []placeSeg, referent liveness.VarID, refPath []placeSeg, mut bool) {
	c.addEdge(root, fieldBorrow{path: path, referent: referent, refPath: refPath, mut: mut})
}

// addEdge records fb as an edge out of `root` in the eager graph, unless `root` already has it.
func (c *checker) addEdge(root liveness.VarID, fb fieldBorrow) {
	if containsFieldBorrow(c.fn.eagerBorrowGraph[root], fb) {
		return
	}
	c.fn.eagerBorrowGraph[root] = append(c.fn.eagerBorrowGraph[root], fb)
	c.markBorrowDirty(root)
}

// recordBorrowEdges records which function-locals the binding root borrows and at
// which field. `val a = {peer: &mut b}` records a → b at path [peer], and a whole-binding
// move `val a2 = a` carries a's edges into a2 at the same paths. A `var` reassignment reuses
// the same VarID, so recording clears the binding's prior edges first: `a = &mut e` after `a =
// &mut d` leaves only a → e. The caller flushes the dirtied roots into borrowGens once the
// statement's borrows are recorded.
func (c *checker) recordBorrowEdges(root liveness.VarID, init ast.Expr) {
	if c.fn == nil || c.fn.eagerBorrowGraph == nil || root <= 0 || init == nil {
		return
	}
	c.clearEagerSubtree(root, nil)
	c.recordBorrowSources(root, nil, init)
}

// addCaptureEdges records a capture edge from root at base to each place of the current body
// that closure captures, other than a place under root itself.
func (c *checker) addCaptureEdges(root liveness.VarID, base []placeSeg, closure ast.Func) {
	for _, captured := range c.fn.capturedLocals[closure] {
		if captured.root == root {
			continue
		}
		for _, place := range captured.places() {
			c.addEdge(root, fieldBorrow{path: base, referent: place.root, refPath: place.path, mut: captured.mut, capture: true})
		}
	}
}

// recordBorrowSources records the borrow edges the expression e contributes to the binding
// `root`, at `base`, the field path reached so far:
//
//   - A direct `&mut b` of a local records an edge at `base`.
//   - An object property descends with `base` extended by the property name.
//   - A tuple element and a spread descend at `base` unchanged. A field path is a chain of
//     named segments, and neither a tuple index nor a spread contributes one: a tuple index
//     is a number, not a field name, and a spread merges its source's fields without naming
//     them. The place model approximates a read of either to its container, so the borrow
//     stays attributed to `base`.
//   - A closure records a capture edge at `base` for each local it captures.
//   - A place expression copies that place's edges, re-rooted under `root` at `base`.
//   - Any other carrier, such as an `if`/`else` branch, contributes its inline borrows and the
//     captures of its inline closures at `base`.
//
// The walk stops at a call boundary and does not enter a closure's body.
func (c *checker) recordBorrowSources(root liveness.VarID, base []placeSeg, e ast.Expr) {
	switch e := e.(type) {
	case *ast.BorrowExpr:
		if src, ok := c.localReferentPlace(e.Arg); ok && src.root != root {
			c.addBorrowEdge(root, base, src.root, src.path, e.Mut)
		}
	case *ast.ObjectExpr:
		for _, elem := range e.Elems {
			switch el := elem.(type) {
			case *ast.PropertyExpr:
				if name, ok := objKeyName(el.Name); ok {
					c.recordBorrowSources(root, appendSeg(base, name), el.ValueExpr())
				} else {
					// A computed key names no static field segment, so the borrow can't
					// be addressed by a field path. Keep base, attributing the value to the
					// enclosing object conservatively.
					c.recordBorrowSources(root, base, el.ValueExpr())
				}
			case *ast.ObjSpreadExpr:
				c.recordBorrowSources(root, base, el.Value)
			}
		}
	case *ast.TupleExpr:
		for _, el := range e.Elems {
			c.recordBorrowSources(root, base, el)
		}
	case *ast.ArraySpreadExpr:
		c.recordBorrowSources(root, base, e.Value)
	case *ast.FuncExpr:
		c.addCaptureEdges(root, base, e)
	case *ast.CallExpr, *ast.TaggedTemplateLitExpr:
		return
	default:
		// A place names another binding whose value e copies, as in `val a2 = a` or `val c =
		// a.peer`. copyPlaceEdges transfers that binding's edges to root, re-rooted at base.
		if p, ok := c.exprPlace(e); ok && p.root > 0 {
			c.copyPlaceEdges(root, base, p)
			return
		}
		// Any other carrier expression contributes its inline borrows of locals at base, as
		// in the `if cond { &mut b } else { … }` of `val a = if cond { &mut b } else { … }`.
		// The walk descends through it but stops at call and nested-function boundaries.
		for _, b := range borrowsIn(e) {
			if src, ok := c.localReferentPlace(b.Arg); ok && src.root != root {
				c.addBorrowEdge(root, base, src.root, src.path, b.Mut)
			}
		}
		for _, closure := range closuresIn(e) {
			c.recordBorrowSources(root, base, closure)
		}
	}
}

// copyPlaceEdges transfers to the binding root the borrow edges of the source place src,
// re-rooted at base. Each of src's edges on src's field path — at it, beneath it, or above
// it where the source binding wholly borrows a local — transfers, re-rooted at base plus
// the part of the edge path below the read place. An edge above the read place reaches the
// whole binding, so the read projects entirely within it and the suffix is empty. It backs
// both a place initializer `val c = a.peer` and a shorthand property `{peer}`.
func (c *checker) copyPlaceEdges(root liveness.VarID, base []placeSeg, src movePlace) {
	for _, edge := range c.fn.eagerBorrowGraph[src.root] {
		// Skip an edge off the read place's field path, and one pointing back at root. The
		// self-edge guard matters because src's referent can be root: reassigning `b = a`
		// where a holds a borrow of b would otherwise copy a → b into b as a b → b loop.
		if !pathPrefixRelated(edge.path, src.path) || edge.referent == root {
			continue
		}
		var suffix []placeSeg
		if len(edge.path) > len(src.path) {
			suffix = edge.path[len(src.path):]
		}
		c.addEdge(root, fieldBorrow{path: appendPath(base, suffix), referent: edge.referent, refPath: edge.refPath, mut: edge.mut, capture: edge.capture})
	}
}

// recordPatternPlaceEdges records borrow edges for a destructuring whose initializer is a
// place, projecting the pattern over that place. `val {peer} = a` binds peer to a.peer, so
// peer inherits a's edges on the [peer] path. An identifier pattern inherits the whole
// place's edges; an object element extends the place by its key; a tuple element keeps the
// place, since a tuple index has no field segment and the read approximates to the
// container.
//
// It covers the destructuring patterns that bind a sub-place of the initializer. A rest
// element, an instance pattern, and an extractor pattern record nothing.
func (c *checker) recordPatternPlaceEdges(pat ast.Pat, src movePlace) {
	walk := patternPlaceWalk{
		leaf: func(node ast.Node, at movePlace) {
			id := c.varIDs.Of(node)
			if id > 0 {
				c.copyPlaceEdges(id, nil, at)
			}

			if e, ok := node.(*ast.ObjShorthandPat); ok && e.Default != nil {
				// The property may be absent, so the leaf can take the shorthand
				// default, such as `val {peer = &mut b} = obj`.
				c.recordBorrowEdges(id, e.Default)
			}
		},
		skipRests: true,
	}
	walk.walk(pat, src)
}

// recordFieldStoreEdges records a borrow edge for a field store `recv.f = source` into a
// receiver that dies with the frame, a local or an owned parameter, rooted at recv's place
// extended by f. A store `b.peer = &mut d` records b → d at [peer]. A store into a BORROW
// parameter's field records nothing, since the parameter's referent belongs to the caller. It
// is a strong update on the stored field's subtree. It clears the [f] subtree before recording,
// so a repoint `b.peer = &mut e` after `b.peer = &mut d` leaves only b → e at [peer] while a
// sibling edge b → x at [data] survives. It then flushes the dirtied root into borrowGens at
// stmtRef.
func (c *checker) recordFieldStoreEdges(
	recv ast.Expr,
	field string,
	source ast.Expr,
	stmtRef liveness.StmtRef,
) {
	if c.fn == nil || c.fn.eagerBorrowGraph == nil {
		return
	}
	rp, ok := c.exprPlace(recv)
	if !ok || rp.root <= 0 || c.paramReferentOutlivesFrame(rp.root) {
		return
	}
	base := appendSeg(rp.path, field)
	c.clearEagerSubtree(rp.root, base)
	c.recordBorrowSources(rp.root, base, source)
	c.flushBorrowDirty(stmtRef)
}

// recordElementStoreEdges records a borrow edge for an element store `recv[k] = source`
// into a receiver that dies with the frame, the element twin of recordFieldStoreEdges. A
// dynamic key names no field segment, so the edge is rooted at recv's own place. The
// update is weak: the store repoints one element the checker cannot name, so the edges
// the receiver's other elements hold stay recorded.
func (c *checker) recordElementStoreEdges(recv, source ast.Expr, stmtRef liveness.StmtRef) {
	if c.fn == nil || c.fn.eagerBorrowGraph == nil {
		return
	}
	rp, ok := c.exprPlace(recv)
	if !ok || rp.root <= 0 || c.paramReferentOutlivesFrame(rp.root) {
		return
	}
	c.recordBorrowSources(rp.root, rp.path, source)
	c.flushBorrowDirty(stmtRef)
}

// collectEdgesFrom appends to out every edge reachable from root, starting with root's edges
// on filter's field path and then following every edge of each referent reached. The first
// hop keeps an edge at the filter, beneath it, or above it where root wholly borrows a local,
// so a read of a.peer follows a → b at [peer] but not a → c at [data]. A local reached
// through a borrow exposes all of it, so every later hop follows every edge. The seen set
// terminates borrow cycles.
func collectEdgesFrom(
	root liveness.VarID,
	filter []placeSeg,
	seen set.Set[liveness.VarID],
	graph map[liveness.VarID][]fieldBorrow,
	out *[]fieldBorrow,
) {
	for _, edge := range graph[root] {
		if !pathPrefixRelated(edge.path, filter) {
			continue
		}
		*out = append(*out, edge)
		collectAllEdgesFrom(edge.referent, seen, graph, out)
	}
}

// collectAllEdgesFrom appends to out every edge reachable from node, following every edge
// regardless of field path.
func collectAllEdgesFrom(
	node liveness.VarID,
	seen set.Set[liveness.VarID],
	graph map[liveness.VarID][]fieldBorrow,
	out *[]fieldBorrow,
) {
	if seen.Contains(node) {
		return
	}
	seen.Add(node)
	for _, edge := range graph[node] {
		*out = append(*out, edge)
		collectAllEdgesFrom(edge.referent, seen, graph, out)
	}
}

// appendSeg returns base with one more named field segment appended, copying the path so
// sibling places built from the same base never share backing storage.
func appendSeg(base []placeSeg, name string) []placeSeg {
	out := make([]placeSeg, len(base)+1)
	copy(out, base)
	out[len(base)] = placeSeg{kind: namedSeg, name: name}
	return out
}

// appendPath returns base with the segments of suffix appended, copying so the result
// shares no backing storage with either input.
func appendPath(base, suffix []placeSeg) []placeSeg {
	out := make([]placeSeg, len(base)+len(suffix))
	copy(out, base)
	copy(out[len(base):], suffix)
	return out
}
