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
// A per-binding borrow-edge graph drives the check, over the move engine's borrow tracking
// rather than the lifetime sort. recordBorrowEdges records which locals each binding
// borrows. At each site, escapingLocalsOf follows those edges and scans for direct borrows
// to find the locals the outgoing value carries.
//
// A borrow of a parameter is exempt. Its lifetime outlives the frame, so
// `fn (p: &mut {x}) -> &mut {x} { return p }` checks.
//
// Edges and the checks are whole-binding granular. An edge is keyed by root binding, so it
// records that `a` borrows `b` without recording which field holds the borrow. The check
// follows edges only from a whole-binding return like `return a`, never a field return
// like `return a.peer`. Skipping field returns avoids a false positive. Following a root
// edge for the disjoint owned field in `return a.data` would wrongly flag `b`, which
// `a.data` does not borrow. The cost is a false negative. `return a.peer` returns the
// borrow field itself, and the check does not catch it. Resolving a field return to its
// own edge needs the per-field tracking deferred to PR 11.

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

// borrowCollector gathers the BorrowExprs an expression carries by value, riding the
// shared AST visitor so it reaches a borrow nested in an object or tuple literal. It stops
// at a call or nested-function boundary, where a borrow is not part of the value the
// scanned expression yields.
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
// object and tuple literals but stopping at call and nested-function boundaries.
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

// escapingLocalsOf returns the function-locals whose data e carries by value. A borrow of
// a local written directly in e contributes its referent, such as the `&mut b` in
// `{peer: &mut b}`. When e names a whole binding, the binding's borrow edges contribute the
// locals they reach. A field place is not followed, since the edge graph is keyed by root
// binding.
func (c *checker) escapingLocalsOf(e ast.Expr) set.Set[liveness.VarID] {
	out := set.NewSet[liveness.VarID]()
	if c.fn == nil || c.fn.borrowEdges == nil || e == nil {
		return out
	}
	// Borrows of locals written directly in e, such as the `&mut b` in `{peer: &mut b}`.
	for _, b := range borrowsIn(e) {
		if referent, ok := c.isLocalReferent(b.Arg); ok {
			out.Add(referent)
		}
	}
	// When e names a whole binding, the locals that binding transitively borrows. For `a`
	// bound by `val a = {peer: &mut b}`, this follows the a → b edge. A field place is
	// skipped by the len(p.path) == 0 guard, so `a.peer` follows nothing.
	if p, ok := exprPlace(e); ok && p.root > 0 && len(p.path) == 0 {
		c.collectBorrowedLocals(p.root, out)
	}
	return out
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

// addBorrowEdge records that the binding destRoot borrows the function-local referent,
// allocating the destination's edge set on first use.
func (c *checker) addBorrowEdge(destRoot, referent liveness.VarID) {
	edges := c.fn.borrowEdges[destRoot]
	if edges == nil {
		edges = set.NewSet[liveness.VarID]()
		c.fn.borrowEdges[destRoot] = edges
	}
	edges.Add(referent)
}

// recordBorrowEdges records which function-locals the binding destVarID borrows. `val a =
// {peer: &mut b}` records a → b, and a whole-binding move `val a2 = a` carries a's edges
// into a2.
func (c *checker) recordBorrowEdges(destVarID int, init ast.Expr) {
	if c.fn == nil || c.fn.borrowEdges == nil || destVarID <= 0 || init == nil {
		return
	}
	destRoot := liveness.VarID(destVarID)
	for _, referent := range c.escapingLocalsOf(init).ToSlice() {
		if referent != destRoot {
			c.addBorrowEdge(destRoot, referent)
		}
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

// collectBorrowedLocals adds to out every function-local reachable from root through
// borrow edges. root is the carrier binding, which moves out rather than escaping, so it
// is the starting point but never added to out. The seen set terminates borrow cycles.
func (c *checker) collectBorrowedLocals(root liveness.VarID, out set.Set[liveness.VarID]) {
	seen := set.NewSet[liveness.VarID]()
	var walk func(liveness.VarID)
	walk = func(node liveness.VarID) {
		if seen.Contains(node) {
			return
		}
		seen.Add(node)
		for _, referent := range c.fn.borrowEdges[node].ToSlice() {
			out.Add(referent)
			walk(referent)
		}
	}
	walk(root)
}
