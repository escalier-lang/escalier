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
// exposes all its fields. A field return `return a.peer` follows only the edges at [peer]
// or beneath it, so it catches the escaping borrow of b. The disjoint field return `return
// a.data` follows the edges at [data], finds none, and is sound with no false positive.
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

// escapingLocalsOf returns the function-locals whose data e carries by value. A borrow of
// a local written directly in e contributes its referent, such as the `&mut b` in
// `{peer: &mut b}`. When e names a place — a whole binding `a` or a field `a.peer` — the
// edges under that place contribute the locals they transitively reach. The walk descends
// object and tuple literals but stops at call and nested-function boundaries, where a
// borrow is not part of the value e yields.
func (c *checker) escapingLocalsOf(e ast.Expr) set.Set[liveness.VarID] {
	out := set.NewSet[liveness.VarID]()
	if c.fn == nil || c.fn.borrowEdges == nil || e == nil {
		return out
	}
	c.collectEscaping(e, out)
	return out
}

// collectEscaping adds to out every function-local the expression e carries by value. A
// direct `&mut b` of a local contributes b. An object or tuple literal contributes the
// locals its elements carry. A place expression contributes the locals its edges reach,
// filtered to the place's field path. The walk stops at a call or nested-function
// boundary, where a borrow is consumed by the callee or belongs to the inner scope rather
// than flowing out through e.
func (c *checker) collectEscaping(e ast.Expr, out set.Set[liveness.VarID]) {
	switch e := e.(type) {
	case *ast.BorrowExpr:
		if referent, ok := c.isLocalReferent(e.Arg); ok {
			out.Add(referent)
		}
	case *ast.ObjectExpr:
		for _, elem := range e.Elems {
			if prop, ok := elem.(*ast.PropertyExpr); ok && prop.Value != nil {
				c.collectEscaping(prop.Value, out)
			}
		}
	case *ast.TupleExpr:
		for _, el := range e.Elems {
			c.collectEscaping(el, out)
		}
	case *ast.CallExpr, *ast.TaggedTemplateLitExpr, *ast.FuncExpr:
		return
	default:
		if p, ok := exprPlace(e); ok && p.root > 0 {
			c.collectBorrowedFrom(p.root, p.path, out, set.NewSet[liveness.VarID]())
		}
	}
}

// addBorrowEdge records that the binding root borrows the function-local referent at the
// given field path, allocating the root's edge list on first use. A duplicate edge — the
// same path and referent — is dropped, so repeated walks do not accumulate copies.
func (c *checker) addBorrowEdge(root liveness.VarID, path []placeSeg, referent liveness.VarID) {
	for _, e := range c.fn.borrowEdges[root] {
		if e.referent == referent && equalPath(e.path, path) {
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
// root at base, the field path reached so far. A direct `&mut b` of a local records an
// edge at base. An object literal property descends with base extended by the property
// name, and a tuple element descends at base unchanged, since a tuple index has no field
// segment. A place expression copies that place's edges, re-rooted under root at base. The
// walk stops at a call or nested-function boundary.
func (c *checker) walkBorrowSources(root liveness.VarID, base []placeSeg, e ast.Expr) {
	switch e := e.(type) {
	case *ast.BorrowExpr:
		if referent, ok := c.isLocalReferent(e.Arg); ok && referent != root {
			c.addBorrowEdge(root, base, referent)
		}
	case *ast.ObjectExpr:
		for _, elem := range e.Elems {
			prop, ok := elem.(*ast.PropertyExpr)
			if !ok || prop.Value == nil {
				continue
			}
			if name, ok := objKeyName(prop.Name); ok {
				c.walkBorrowSources(root, appendSeg(base, name), prop.Value)
			}
		}
	case *ast.TupleExpr:
		for _, el := range e.Elems {
			c.walkBorrowSources(root, base, el)
		}
	case *ast.CallExpr, *ast.TaggedTemplateLitExpr, *ast.FuncExpr:
		return
	default:
		// A place names another binding whose value e copies. Each of that binding's
		// edges whose path lies at or beneath the read place transfers to root, re-rooted
		// at base plus the part of the edge path below the read place.
		p, ok := exprPlace(e)
		if !ok || p.root <= 0 {
			return
		}
		for _, edge := range c.fn.borrowEdges[p.root] {
			if !pathHasPrefix(edge.path, p.path) {
				continue
			}
			if edge.referent == root {
				continue
			}
			c.addBorrowEdge(root, appendPath(base, edge.path[len(p.path):]), edge.referent)
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

// collectBorrowedFrom adds to out every function-local reachable from the place rooted at
// root through borrow edges. filter restricts the first hop to edges at or beneath the
// read place's field path, so a field read `a.peer` follows only the edges under [peer].
// Each borrowed local is then followed in full — a borrow exposes the whole referent — so
// deeper hops pass a nil filter. root is the carrier binding, the starting point, and is
// never added to out. The seen set terminates borrow cycles.
func (c *checker) collectBorrowedFrom(root liveness.VarID, filter []placeSeg, out, seen set.Set[liveness.VarID]) {
	if seen.Contains(root) {
		return
	}
	seen.Add(root)
	for _, edge := range c.fn.borrowEdges[root] {
		if filter != nil && !pathHasPrefix(edge.path, filter) {
			continue
		}
		out.Add(edge.referent)
		c.collectBorrowedFrom(edge.referent, nil, out, seen)
	}
}

// pathHasPrefix reports whether prefix is a prefix of path: every segment of prefix equals
// the segment at the same position in path. An empty prefix matches every path, so a
// whole-binding read follows every edge under the binding.
func pathHasPrefix(path, prefix []placeSeg) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

// equalPath reports whether two field paths are identical, segment for segment.
func equalPath(a, b []placeSeg) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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

