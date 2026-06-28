package solver

import (
	"fmt"
	"sort"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
)

// Return-escape forcing (PR 15). A value flowing out of the function frame must not
// carry a borrow of a function-local binding, since a local dies when the frame
// returns and the borrow would dangle. The move engine consumes the returned source
// but does not check the borrows the returned value carries, so returning a binding
// that borrows a local escapes that local with no error. This pass reports it.
//
// The check works over the move engine's own state rather than the lifetime sort,
// which is why it does not depend on the directional lifetime bounds slated for M6.5.
// Two pieces drive it:
//
//   - recordBorrowEdges records, as each binding is walked, which function-local
//     bindings its initializer borrows through an explicit `&`/`&mut`. `val a = {peer:
//     &mut b}` records the edge a → b.
//   - checkReturnEscape, at each return, follows those edges from the returned binding
//     and scans the returned expression for direct borrows, reporting an escape for any
//     borrow of a non-parameter local it reaches.
//
// A borrow of a parameter is exempt. It carries a caller-supplied lifetime that already
// outlives the return, so `fn (p: &mut {x}) -> &mut {x} { return p }` still checks.
// recordBorrowEdges drops a parameter referent, so a local that only borrows a
// parameter records no edge and a return through it raises no escape.

// ReturnEscapeError fires when a returned value carries a borrow of a function-local
// binding, which cannot outlive the frame. It blames the returned expression and names
// the escaping local.
type ReturnEscapeError struct {
	// LocalName is the borrowed local's source name, for the message.
	LocalName string
	// node is the returned expression, blamed for the escape.
	node ast.Node
}

func (*ReturnEscapeError) isSolverError()        {}
func (e *ReturnEscapeError) Span() ast.Span      { return e.node.Span() }
func (e *ReturnEscapeError) Related() []ast.Span { return nil }
func (e *ReturnEscapeError) Message() string {
	return fmt.Sprintf("borrowed value '%s' does not live long enough to escape the function", e.LocalName)
}

// borrowCollector gathers every BorrowExpr in an expression subtree. It rides the
// shared AST visitor so it reaches a borrow nested in an object or tuple literal — the
// `&mut b` inside `{peer: &mut b}` — without a hand-rolled walk.
type borrowCollector struct {
	*ast.DefaultVisitor
	out *[]*ast.BorrowExpr
}

func (v *borrowCollector) EnterExpr(e ast.Expr) bool {
	if b, ok := e.(*ast.BorrowExpr); ok {
		*v.out = append(*v.out, b)
	}
	return true
}

// borrowsIn returns every BorrowExpr reachable in e, including those nested in object
// or tuple literals.
func borrowsIn(e ast.Expr) []*ast.BorrowExpr {
	var found []*ast.BorrowExpr
	e.Accept(&borrowCollector{DefaultVisitor: &ast.DefaultVisitor{}, out: &found})
	return found
}

// isLocalReferent reports whether the borrow operand names a function-local binding's
// place: a place whose root is a real binding that is not a parameter. A parameter
// referent is exempt, since its lifetime outlives the return. A non-place operand, such
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

// recordBorrowEdges records, for the binding destVarID, which function-local bindings it
// borrows. Two initializer shapes contribute an edge:
//
//   - An explicit `&`/`&mut` of a local. `val a = {peer: &mut b}` records a → b, so a
//     later `return a` can find that it carries a borrow of the local b. A borrow of a
//     parameter records no edge, per isLocalReferent.
//   - A whole-binding move of a carrier. `val a2 = a` moves a's value into a2, so a2
//     inherits a's borrow edges and `return a2` escapes the same local a would.
//
// Both are tracked at whole-binding granularity. A borrow held in a field that is then
// moved on its own, such as `val a2 = a.peer`, is not inherited, since the edge graph is
// keyed by root binding rather than by field place. That field-level precision is left
// to the field-granular tracking PR 11 builds on PR 7.
func (c *checker) recordBorrowEdges(destVarID int, init ast.Expr) {
	if c.fn == nil || c.fn.borrowEdges == nil || destVarID <= 0 || init == nil {
		return
	}
	destRoot := liveness.VarID(destVarID)
	for _, b := range borrowsIn(init) {
		if referent, ok := c.isLocalReferent(b.Arg); ok && referent != destRoot {
			c.addBorrowEdge(destRoot, referent)
		}
	}
	// A whole-binding move `val a2 = a` carries a's borrows into a2. Inherit only from a
	// whole-binding source, since a field move is not tracked at this granularity.
	if src, ok := exprPlace(init); ok && len(src.path) == 0 && src.root != destRoot {
		for _, referent := range c.fn.borrowEdges[src.root].ToSlice() {
			c.addBorrowEdge(destRoot, referent)
		}
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

// checkReturnEscape reports a ReturnEscapeError for each function-local binding a
// returned value borrows. It combines two sources of escaping referents: a borrow
// written directly in the returned expression, such as `return &mut b` or `return
// {peer: &mut b}`, and the borrow edges recorded for a returned binding, such as the a →
// b edge behind `return a`. The escaping locals are reported in VarID order so the
// diagnostics are deterministic.
//
// The edge graph is keyed by root binding, so the edges are followed only when the
// whole binding is returned, `return a`, not a single field, `return a.peer`. Following
// a root edge for a field return would over-report: a disjoint field `return a.other`
// shares a's root but carries none of its borrows. Field-granular return escape is left
// to PR 11, which tracks borrows per field place.
func (c *checker) checkReturnEscape(retExpr ast.Expr) {
	if c.fn == nil || c.fn.borrowEdges == nil || retExpr == nil {
		return
	}
	escaping := set.NewSet[liveness.VarID]()
	for _, b := range borrowsIn(retExpr) {
		if referent, ok := c.isLocalReferent(b.Arg); ok {
			escaping.Add(referent)
		}
	}
	if p, ok := exprPlace(retExpr); ok && p.root > 0 && len(p.path) == 0 {
		c.collectBorrowedLocals(p.root, escaping)
	}
	ids := escaping.ToSlice()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		c.report(&ReturnEscapeError{LocalName: c.varIDToName(id), node: retExpr})
	}
}

// collectBorrowedLocals adds to out every function-local binding reachable from root
// through borrow edges. A returned binding does not itself escape, since it moves out,
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
