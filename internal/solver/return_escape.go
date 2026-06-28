package solver

import (
	"fmt"
	"sort"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
)

// Escape forcing (PR 15). A value flowing out of the function frame must not carry a
// borrow of a function-local binding, since a local dies when the frame returns and the
// borrow would dangle. The move engine consumes the flowed-out source but does not check
// the borrows it carries, so a value that borrows a local escapes with no error. This
// pass reports it at the three non-global value-flow-out sites:
//
//   - a `return`, where the value flows out to the caller;
//   - a field store into a parameter, where it flows into the caller's object;
//   - a consuming argument, where it flows into the callee.
//
// The check works over the move engine's own state rather than the lifetime sort, which
// is why it does not depend on the directional lifetime bounds slated for M6.5. A
// per-binding borrow-edge graph drives it. As each binding is walked, recordBorrowEdges
// records which function-locals it borrows: an explicit `&`/`&mut` of a local in its
// initializer, and the edges of a whole-binding move that carries a borrow forward. At
// each flow-out site, escapingLocalsOf follows those edges and scans for direct borrows
// to find the locals the outgoing value carries. Extending the graph through a field
// store into a local receiver is left to PR 11, which builds the connected-component
// graph through field mutations.
//
// A borrow of a parameter is exempt. It carries a caller-supplied lifetime that already
// outlives the frame, so `fn (p: &mut {x}) -> &mut {x} { return p }` still checks.
// recordBorrowEdges drops a parameter referent, so a local that only borrows a parameter
// records no edge and a flow-out through it raises no escape.
//
// Edges and the flow-out checks are whole-binding granular. A borrow held in a field
// that is moved or returned on its own, such as `return a.peer`, is not tracked, since
// the edge graph is keyed by root binding rather than by field place. That field-level
// precision is left to PR 11, which builds on PR 7's per-field tracking.

// EscapingBorrowError fires when a value flowing out of the frame carries a borrow of a
// function-local binding, which cannot outlive the frame. It blames the outgoing
// expression and names the escaping local.
type EscapingBorrowError struct {
	// LocalName is the borrowed local's source name, for the message.
	LocalName string
	// node is the outgoing expression — the returned value, stored value, or argument —
	// blamed for the escape.
	node ast.Node
}

func (*EscapingBorrowError) isSolverError()        {}
func (e *EscapingBorrowError) Span() ast.Span      { return e.node.Span() }
func (e *EscapingBorrowError) Related() []ast.Span { return nil }
func (e *EscapingBorrowError) Message() string {
	return fmt.Sprintf("borrowed value '%s' does not live long enough to escape the function", e.LocalName)
}

// borrowCollector gathers the BorrowExprs an expression carries by value. It rides the
// shared AST visitor so it reaches a borrow nested in an object or tuple literal — the
// `&mut b` inside `{peer: &mut b}` — without a hand-rolled walk. It stops at a call or
// nested function boundary, since a borrow there is not part of the value the scanned
// expression yields.
type borrowCollector struct {
	*ast.DefaultVisitor
	out *[]*ast.BorrowExpr
}

func (v *borrowCollector) EnterExpr(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.BorrowExpr:
		*v.out = append(*v.out, e)
	case *ast.CallExpr, *ast.TaggedTemplateLitExpr, *ast.FuncExpr:
		// Do not descend into a call or a nested function. A borrow written as a call
		// argument is consumed or borrowed by that call, not carried out by the value the
		// call yields, so `store(read(&mut b))` does not carry b. A borrow inside a nested
		// function body belongs to that function's scope, not this frame's, so resolving
		// it against this frame's bindings would be wrong. A borrow the call result or
		// closure genuinely captures is governed by the escaping-closure-capture work,
		// which is deferred.
		return false
	}
	return true
}

// borrowsIn returns every BorrowExpr the expression e carries by value, descending
// through object and tuple literals but stopping at call and nested-function boundaries.
func borrowsIn(e ast.Expr) []*ast.BorrowExpr {
	var found []*ast.BorrowExpr
	e.Accept(&borrowCollector{DefaultVisitor: &ast.DefaultVisitor{}, out: &found})
	return found
}

// isLocalReferent reports whether the borrow operand names a function-local binding's
// place: a place whose root is a real binding that is not a parameter. A parameter
// referent is exempt, since its lifetime outlives the frame. A non-place operand, such
// as a borrow of a call result, names no tracked binding and is dropped.
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

// escapingLocalsOf returns the function-local bindings whose data the expression e
// carries by value. Two shapes contribute: a borrow of a local written directly in e,
// such as the `&mut b` in `{peer: &mut b}`, and the borrow edges of a whole-binding
// place e names, such as the a → b edge behind `a`. A field place is not followed, since
// the edge graph is keyed by root binding. See the package doc.
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
	if p, ok := exprPlace(e); ok && p.root > 0 && len(p.path) == 0 {
		c.collectBorrowedLocals(p.root, out)
	}
	return out
}

// reportEscapingLocals reports an EscapingBorrowError for each escaping local, blaming
// the outgoing expression. The locals are reported in VarID order so the diagnostics are
// deterministic across the unordered set iteration.
func (c *checker) reportEscapingLocals(escaping set.Set[liveness.VarID], blame ast.Node) {
	ids := escaping.ToSlice()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
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

// recordBorrowEdges records, for the binding destVarID, which function-locals its
// initializer borrows. `val a = {peer: &mut b}` records a → b, and a whole-binding move
// `val a2 = a` carries a's edges into a2, so a later `return a` or `return a2` can find
// it borrows the local b.
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
// It is the return flow-out site: `return a` for an a that borrows local b, `return &mut
// b`, and `return {peer: &mut b}` all escape b.
func (c *checker) checkReturnEscape(retExpr ast.Expr) {
	c.reportEscapingLocals(c.escapingLocalsOf(retExpr), retExpr)
}

// checkStoreEscape handles a field store `recv.f = source`. Storing a value that borrows
// a local into a parameter's field escapes. The parameter's object outlives the frame,
// so the stored local would dangle in the caller. A store into a local receiver does not
// escape here. Extending the borrow graph through such a store is left to PR 11.
func (c *checker) checkStoreEscape(recv, source ast.Expr) {
	if c.fn == nil || c.fn.borrowEdges == nil {
		return
	}
	rp, ok := exprPlace(recv)
	if !ok || rp.root <= 0 || !c.fn.paramVarIDs.Contains(rp.root) {
		return
	}
	c.reportEscapingLocals(c.escapingLocalsOf(source), source)
}

// collectBorrowedLocals adds to out every function-local binding reachable from root
// through borrow edges. The carrier binding does not itself escape, since it moves out,
// so root is only the starting point and is never added to out. Only the locals it
// transitively borrows are collected. The seen set terminates the cyclic case, where
// two locals borrow each other.
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
