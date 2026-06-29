package solver

import (
	"fmt"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
)

// Escape forcing. A value flowing out of the function frame must not carry a borrow of a
// function-local, since the local does not outlive the frame. This pass reports such an
// escape at three sites: a `return`, a field store into a parameter, and a consuming
// argument.
//
// The rejection is conservative. The runtime is garbage-collected, so a returned value
// that references a local keeps it reachable rather than dangling. Returning a
// self-contained graph is therefore sound. A graph is self-contained when an owned value's
// internal `&mut` edges reach only locals that nothing outside the graph references. The
// connected-component move (PR 11) will allow that case by co-moving and consuming those
// locals, so the returned value's owner anchors them and exclusivity holds. Until it lands,
// any borrow of a local flowing out is rejected here. A bare `return &mut b` has no owned
// carrier and stays rejected even then, since the move re-anchors a graph's internal edges,
// not a borrow that is itself the returned value.
//
// A field-granular borrow-edge graph drives the check, over the move engine's borrow
// tracking rather than the lifetime sort. recordBorrowEdges records which locals each
// binding borrows, and at which field. At each site, escapingLocalsOf follows those edges
// and scans for direct borrows to find the locals the outgoing value carries.
//
// A borrow of a parameter is exempt. Its lifetime outlives the frame, so
// `fn (p: &mut {x}) -> &mut {x} { return p }` checks.
//
// An edge carries the field path within the binding that holds the borrow. `val a = {peer:
// &mut b}` records the edge a → b at path [peer], so a return discriminates by field. A
// whole-binding return `return a` follows every edge under a, since the returned value
// exposes all its fields. A field return `return a.peer` follows the edges on the [peer]
// path — at [peer], beneath it, or above it where `val a = &mut b` borrows all of b — so it
// catches the escaping borrow of b. The disjoint field return `return a.data` follows the
// edges on the [data] path, finds none, and is sound with no false positive.
//
// Edges are recorded at three sites: a `val`/`var` initializer, a `var` reassignment, and a
// destructuring leaf. The graph is accumulate-only and not flow-sensitive, so a
// reassignment adds edges without clearing the binding's earlier ones. This over-reports a
// borrow that a later reassignment replaced, the conservative direction that never misses a
// real escape. Clearing on reassignment soundly needs CFG-merge-joined edges, which the
// connected-component move builds.

// EscapingBorrowError fires when a value flowing out of the frame carries a borrow of a
// function-local. It blames the outgoing expression and names the escaping local.
type EscapingBorrowError struct {
	// LocalName is the borrowed local's name, for the message.
	LocalName string
	// node is the outgoing expression blamed for the escape: a return value, stored value,
	// or argument.
	node ast.Node
}

func (*EscapingBorrowError) isSolverError()        {}
func (e *EscapingBorrowError) Span() ast.Span      { return e.node.Span() }
func (e *EscapingBorrowError) Related() []ast.Span { return nil }
func (e *EscapingBorrowError) Message() string {
	return fmt.Sprintf("borrowed value '%s' does not live long enough to escape the function", e.LocalName)
}

// fieldBorrow is one borrow edge under a binding: the field path within the binding that
// holds the borrow, and the function-local the borrow refers to. The path is empty when
// the whole binding is the borrow, as in `var a = &mut b`, and names the field chain when
// a field holds it, as in `val a = {peer: &mut b}` recording path [peer].
type fieldBorrow struct {
	path     []placeSeg
	referent liveness.VarID
}

// borrowCollector gathers the BorrowExprs an expression carries by value, riding the
// shared AST visitor so it reaches a borrow nested in an object or tuple literal, a spread,
// or a control-flow carrier such as an if/else branch. It stops at a call or
// nested-function boundary, where a borrow is not part of the value the scanned expression
// yields.
type borrowCollector struct {
	*ast.DefaultVisitor
	out *[]*ast.BorrowExpr
}

func (v *borrowCollector) EnterExpr(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.BorrowExpr:
		*v.out = append(*v.out, e)
	case *ast.CallExpr, *ast.TaggedTemplateLitExpr, *ast.FuncExpr:
		// A borrow written as a call argument is consumed or borrowed by that call, not
		// carried out by its result, so `store(read(&mut b))` does not carry b. A borrow
		// inside a nested function belongs to that function's scope. A borrow a result or
		// closure genuinely captures is governed by the deferred closure-capture work.
		return false
	}
	return true
}

// borrowsIn returns every BorrowExpr the expression e carries by value, descending through
// object and tuple literals, spreads, and control-flow carriers but stopping at call and
// nested-function boundaries.
func borrowsIn(e ast.Expr) []*ast.BorrowExpr {
	var found []*ast.BorrowExpr
	e.Accept(&borrowCollector{DefaultVisitor: &ast.DefaultVisitor{}, out: &found})
	return found
}

// isLocalReferent reports whether the borrow operand names a function-local place, one
// rooted at a real binding that is not a parameter. A parameter referent is exempt, and a
// non-place operand names no tracked binding.
func (c *checker) isLocalReferent(arg ast.Expr) (liveness.VarID, bool) {
	p, ok := exprPlace(arg)
	if !ok || p.root <= 0 {
		return 0, false
	}
	if c.fn.paramVarIDs.Contains(p.root) {
		return 0, false
	}
	return p.root, true
}

// escapingLocalsOf returns the function-locals whose data e carries by value. Two sources
// contribute. First, a borrow of a local written anywhere in e, such as the `&mut b` in
// `{peer: &mut b}` or in an if/else branch, contributes its referent. borrowsIn finds these,
// descending carriers and stopping at call and nested-function boundaries. Second, when e
// names a place — a whole binding `a` or a field `a.peer` — the edges under that place
// contribute the locals they transitively reach, filtered to the place's field path so a
// field return follows only that field's edges.
func (c *checker) escapingLocalsOf(e ast.Expr) set.Set[liveness.VarID] {
	out := set.NewSet[liveness.VarID]()
	if c.fn == nil || c.fn.borrowEdges == nil || e == nil {
		return out
	}
	for _, b := range borrowsIn(e) {
		if referent, ok := c.isLocalReferent(b.Arg); ok {
			out.Add(referent)
		}
	}
	if p, ok := exprPlace(e); ok && p.root > 0 {
		c.collectBorrowedFrom(p.root, p.path, out, set.NewSet[liveness.VarID]())
	}
	return out
}

// addBorrowEdge records that the binding root borrows the function-local referent at the
// given field path, allocating the root's edge list on first use. A duplicate edge with
// the same path and referent is dropped, so repeated walks do not accumulate copies.
func (c *checker) addBorrowEdge(root liveness.VarID, path []placeSeg, referent liveness.VarID) {
	for _, e := range c.fn.borrowEdges[root] {
		if e.referent == referent && slices.Equal(e.path, path) {
			return
		}
	}
	c.fn.borrowEdges[root] = append(c.fn.borrowEdges[root], fieldBorrow{path: path, referent: referent})
}

// recordBorrowEdges records which function-locals the binding destVarID borrows and at
// which field. `val a = {peer: &mut b}` records a → b at path [peer], and a whole-binding
// move `val a2 = a` carries a's edges into a2 at the same paths.
func (c *checker) recordBorrowEdges(destVarID int, init ast.Expr) {
	if c.fn == nil || c.fn.borrowEdges == nil || destVarID <= 0 || init == nil {
		return
	}
	c.walkBorrowSources(liveness.VarID(destVarID), nil, init)
}

// walkBorrowSources records the borrow edges the expression e contributes to the binding
// root, at base, the field path reached so far. A direct `&mut b` of a local records an
// edge at base. An object property descends with base extended by the property name, while
// a tuple element and a spread descend at base unchanged, since neither refines the field
// path. A place expression copies that place's edges, re-rooted under root at base. Any
// other carrier, such as an if/else branch, contributes its inline borrows at base through
// borrowsIn. The walk stops at a call or nested-function boundary.
func (c *checker) walkBorrowSources(root liveness.VarID, base []placeSeg, e ast.Expr) {
	switch e := e.(type) {
	case *ast.BorrowExpr:
		if referent, ok := c.isLocalReferent(e.Arg); ok && referent != root {
			c.addBorrowEdge(root, base, referent)
		}
	case *ast.ObjectExpr:
		for _, elem := range e.Elems {
			switch el := elem.(type) {
			case *ast.PropertyExpr:
				if el.Value == nil {
					continue
				}
				if name, ok := objKeyName(el.Name); ok {
					c.walkBorrowSources(root, appendSeg(base, name), el.Value)
				} else {
					c.walkBorrowSources(root, base, el.Value)
				}
			case *ast.ObjSpreadExpr:
				c.walkBorrowSources(root, base, el.Value)
			}
		}
	case *ast.TupleExpr:
		for _, el := range e.Elems {
			c.walkBorrowSources(root, base, el)
		}
	case *ast.ArraySpreadExpr:
		c.walkBorrowSources(root, base, e.Value)
	case *ast.CallExpr, *ast.TaggedTemplateLitExpr, *ast.FuncExpr:
		return
	default:
		// A place names another binding whose value e copies. Each of that binding's edges
		// on the read place's field path transfers to root, re-rooted at base plus the part
		// of the edge path below the read place. An edge is on the path when it sits at the
		// read place, beneath it, or above it. An edge above the read place reaches the
		// whole binding, so the read projects entirely within it and the suffix is empty.
		if p, ok := exprPlace(e); ok && p.root > 0 {
			for _, edge := range c.fn.borrowEdges[p.root] {
				if !pathPrefixRelated(edge.path, p.path) || edge.referent == root {
					continue
				}
				var suffix []placeSeg
				if len(edge.path) > len(p.path) {
					suffix = edge.path[len(p.path):]
				}
				c.addBorrowEdge(root, appendPath(base, suffix), edge.referent)
			}
			return
		}
		// Any other carrier expression contributes its inline borrows of locals at base,
		// descending through it but stopping at call and nested-function boundaries.
		for _, b := range borrowsIn(e) {
			if referent, ok := c.isLocalReferent(b.Arg); ok && referent != root {
				c.addBorrowEdge(root, base, referent)
			}
		}
	}
}

// reportEscapingLocals reports an EscapingBorrowError for each escaping local, blaming the
// outgoing expression. Locals are reported in VarID order for deterministic diagnostics.
func (c *checker) reportEscapingLocals(escaping set.Set[liveness.VarID], blame ast.Node) {
	ids := escaping.ToSlice()
	slices.Sort(ids)
	for _, id := range ids {
		c.report(&EscapingBorrowError{LocalName: c.varIDToName(id), node: blame})
	}
}

// checkReturnEscape reports an escape for each function-local a returned value borrows.
// `return a` where a borrows b, `return &mut b`, and `return {peer: &mut b}` all escape b.
func (c *checker) checkReturnEscape(retExpr ast.Expr) {
	c.reportEscapingLocals(c.escapingLocalsOf(retExpr), retExpr)
}

// checkParamFieldStoreEscape handles a field store `recv.f = source`. Storing a value that borrows a
// local into a parameter's field escapes, since the parameter's object outlives the frame.
// A store into a local receiver does not escape and is not tracked.
func (c *checker) checkParamFieldStoreEscape(recv, source ast.Expr) {
	if c.fn == nil || c.fn.borrowEdges == nil {
		return
	}
	rp, ok := exprPlace(recv)
	if !ok || rp.root <= 0 || !c.fn.paramVarIDs.Contains(rp.root) {
		return
	}
	c.reportEscapingLocals(c.escapingLocalsOf(source), source)
}

// collectBorrowedFrom adds to out every function-local the read place rooted at root, with
// field path filter, exposes through borrow edges. The first hop keeps only edges on the
// filter path: an edge at [peer], beneath it at [peer, …], or above it at [] where a wholly
// borrows b. So a field read `a.peer` follows a → b on any of those but not a disjoint a → c
// at [data]. Each local the first hop reaches is then followed in full through
// collectAllFrom, since a borrow exposes the whole referent. root is the carrier binding,
// the starting point for the first hop. It is added to out only when a borrow cycle reaches
// back to it, which is sound: a local that borrows root, and itself escapes, carries root's
// data out too.
func (c *checker) collectBorrowedFrom(root liveness.VarID, filter []placeSeg, out, seen set.Set[liveness.VarID]) {
	for _, edge := range c.fn.borrowEdges[root] {
		if !pathPrefixRelated(edge.path, filter) {
			continue
		}
		out.Add(edge.referent)
		c.collectAllFrom(edge.referent, out, seen)
	}
}

// collectAllFrom adds to out every function-local reachable from node through borrow edges,
// following every edge regardless of field path, since reaching a binding through a borrow
// exposes all of it. The seen set terminates borrow cycles.
func (c *checker) collectAllFrom(node liveness.VarID, out, seen set.Set[liveness.VarID]) {
	if seen.Contains(node) {
		return
	}
	seen.Add(node)
	for _, edge := range c.fn.borrowEdges[node] {
		out.Add(edge.referent)
		c.collectAllFrom(edge.referent, out, seen)
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

