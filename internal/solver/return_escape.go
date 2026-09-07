package solver

import (
	"fmt"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Escape forcing. A value flowing out of the function frame may carry a borrow of a
// function-local only when nothing else can still reach that local. This pass decides three
// flow-out sites: a `return`, a field store into a parameter, and a consuming argument.
//
// The runtime is garbage collected, so a local a flowed-out value references stays alive
// rather than dangling. What makes an escape unsafe is a second live path to the same value,
// not the storage going away. So the rule asks whether the flowed-out value is the only way
// back to the locals it carries.
//
// A self-contained graph answers yes. A graph is self-contained when the locals its `&mut`
// edges reach are referenced by nothing outside it. The connected-component move re-anchors
// that case: the escape becomes a move of the whole component, every binding in it is
// consumed, and a later use of any of them is a use-after-move. See resolveComponentEscapes.
//
// A `return` answers yes on its own. The frame does not survive it, so every local dies and
// the borrow the caller receives is the only path left. A store or a consuming argument
// leaves the frame running, so a bare borrow flowing out either of those still has a second
// path through the local it names, and stays an escape. See componentMoveCovers.
//
// Two borrows of one local can leave together in a single returned value, which no rule here
// reports. #1263 covers that.
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
// &mut b}` records the edge a → b at path [peer], so a return discriminates by field:
//
//   - A whole-binding return `return a` follows every edge under a, since the returned value
//     exposes all of a's fields.
//   - A field return `return a.peer` follows the edges on the [peer] path. That covers an
//     edge at [peer], beneath it, or above it where `val a = &mut b` borrows all of b. So it
//     catches the escaping borrow of b.
//   - A disjoint field return `return a.data` follows the edges on the [data] path, finds
//     none, and is sound with no false positive.
//
// Edges are recorded at five sites:
//
//   - a `val`/`var` initializer,
//   - a `var` reassignment,
//   - a field store into a local receiver,
//   - a destructuring leaf,
//   - a call whose signature stores an argument-borrow into another argument. See
//     borrow_store.go for how a signature spells that store.
//
// The graph is flow-sensitive. Each recording strong-updates the binding, clearing the replaced
// subtree before adding the new edges. analyzeBorrows folds the per-statement edge sets
// forward over the CFG, joining them by union at branch merges. So a reassignment drops its prior
// referent, a repoint replaces one field's edge, and a borrow set on one branch still reaches the
// merge. See borrow_flow.go. resolveComponentEscapes reads the edge set at each flow-out site's
// program point through this graph.

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

// escapeSite is one value flowing out of the frame whose escape decision is deferred to
// resolveComponentEscapes. It holds the outgoing expression and the CFG point it leaves the
// frame at. The expression doubles as the diagnostic blame and as the source whose carried
// locals the post-pass computes.
type escapeSite struct {
	expr    ast.Expr
	stmtRef liveness.StmtRef
	// isReturn marks a value leaving through a `return`, the one flow-out whose frame is
	// gone afterwards. componentMoveCovers reads it to decide whether a bare borrow may
	// re-anchor: nothing in the frame can reach the value again, so the borrow the caller
	// receives is the only path to it.
	isReturn bool
}

// resolveComponentEscapes decides every recorded escape site once the body is fully walked,
// so the borrow-edge graph is complete and the consumed lattice `info` is known. A site
// whose outgoing value carries borrows of function-locals is either a self-contained
// connected-component move — allowed, co-moving and consuming the component's locals — or an
// ordinary escape, reported. It returns true when it consumed any co-moved local, so the
// caller knows to recompute the lattice before the use-after-move scan reads it.
func (c *checker) resolveComponentEscapes(
	info *liveness.MoveInfo,
	flowBorrowGraph *flowBorrowGraph,
) bool {
	consumed := false
	outOfFrame := c.localsLeavingOutsideAReturn(flowBorrowGraph)
	// Return index to the owned type its borrows strip to. Applied after every site is decided.
	strips := map[int]soltype.Type{}
	for _, es := range c.fn.escapeSites {
		// The flow-sensitive borrow-edge graph at this site's program point. A borrow cleared by an
		// earlier reassignment is gone here, and one set on a reaching branch is joined in. Passed
		// explicitly to each escape helper so they read this per-point snapshot rather than the
		// whole-body eager graph.
		fieldBorrowGraph := flowBorrowGraph.fieldBorrowGraphBefore(es.stmtRef)
		escaping := c.escapingLocalsOf(es.expr, fieldBorrowGraph)
		if escaping.Len() == 0 {
			continue
		}
		if c.componentMoveCovers(es, escaping, outOfFrame, info, fieldBorrowGraph) {
			// Co-move the component: consume every borrowed local, so a later use of any of
			// them is a use-after-move. The escaping value's own root is consumed at the flow
			// site already, so it is skipped here — a borrow cycle can route an edge back to
			// the root, and consuming it twice at one program point is a spurious double-move.
			var rootID liveness.VarID
			if p, ok := exprPlace(es.expr); ok {
				rootID = p.root
			}
			ids := escaping.ToSlice()
			slices.Sort(ids)
			for _, id := range ids {
				if id == rootID {
					continue
				}
				c.recordMove(id, es.expr, es.stmtRef)
			}
			// When the moved graph is a tree — every borrowed local reached exactly once with
			// no cycle — the return value is the sole owner of each node, so owning them in the
			// type is honest. The rewrites are collected here and applied together, since one
			// return left borrowed holds back the rest.
			if idx, stripped, ok := c.returnStripFor(es.expr, fieldBorrowGraph); ok {
				strips[idx] = stripped
			}
			consumed = true
			continue
		}
		c.reportEscapingLocals(escaping, es.expr)
	}
	c.applyReturnStrips(strips)
	c.fn.escapeSites = nil
	return consumed
}

// localsLeavingOutsideAReturn returns the function-locals that flow out somewhere other than a
// return: a field store into a parameter, or a consuming argument. Both hand a path to the local
// that outlives the frame, one to the caller's object and one to the callee.
//
// A return's exemption rests on the frame being gone, so nothing can reach the local again. That
// is false for a local already on this list, since the earlier flow-out left a path behind. In
//
//	fn f(p: mut {node: {peer: &mut B}}) {
//		val mut b = {value: 0}
//		p.node = {peer: &mut b}
//		return &mut b
//	}
//
// the caller ends up holding b through p.node.peer AND through the return, which is two live
// mutable paths to one value.
//
// Every site is scanned before any is decided, so a store written after the return in the source
// counts the same as one written before it.
func (c *checker) localsLeavingOutsideAReturn(flowBorrowGraph *flowBorrowGraph) set.Set[liveness.VarID] {
	out := set.NewSet[liveness.VarID]()
	for _, es := range c.fn.escapeSites {
		if es.isReturn {
			continue
		}
		graph := flowBorrowGraph.fieldBorrowGraphBefore(es.stmtRef)
		for _, id := range c.escapingLocalsOf(es.expr, graph).ToSlice() {
			out.Add(id)
		}
	}
	return out
}

// componentMoveCovers reports whether the escape of es is a self-contained connected-component
// move rather than an ordinary escape. It holds when two conditions are met:
//
//   - es carries an owned aggregate, or it is a return whose locals leave nowhere else. An
//     owned aggregate has internal edges for the move to re-anchor. A bare borrow has none, so
//     away from a return it stays an escape. A bare borrow is `&mut b`, a borrowed field, or a
//     borrow-typed binding. outOfFrame is what localsLeavingOutsideAReturn found.
//   - The component is self-contained: no live binding outside it borrows a node inside it.
//     The component is the outgoing value's root together with every local it transitively
//     borrows. A binding dead at ref does not count as an external reference, so a stray
//     unused borrow before a return does not block the move. The loop below explains what
//     "dead" covers.
//
// A return needs no aggregate because the frame does not survive it. Every local dies with the
// frame, so the borrow the caller receives is the only path left to the value. Handing ownership
// out then states what the caller actually holds. The outOfFrame test is what limits this to a
// local the frame does not also send out another way.
//
// A store or a consuming argument leaves the frame running. The local a bare borrow names is
// still reachable from the frame, so the value is not the caller's alone, and the aggregate
// requirement stands.
//
// Two borrows of one local CAN leave together in a single returned value. The move accepts it,
// and returnStripFor then declines to re-type it, so both stay borrowed. #1263 covers reporting
// that.
//
// The external-reference scan reads the same borrow-edge graph the escape check is built on,
// so it sees every alias the recording sites listed at the top of this file record. An alias
// formed by a path none of them covers is invisible here exactly as it is to the escape check.
// The store recorder covers a call that writes an argument-borrow into another argument or
// into a method's receiver, so `a.peers.push(&mut b)` records its edge once `Array` has a
// method surface to declare the store on. Until then the same call against a hand-written
// container records it; see borrow_store.go.
func (c *checker) componentMoveCovers(
	es escapeSite, escaping set.Set[liveness.VarID],
	outOfFrame set.Set[liveness.VarID],
	info *liveness.MoveInfo,
	fieldBorrowGraph map[liveness.VarID][]fieldBorrow,
) bool {
	e, stmtRef := es.expr, es.stmtRef
	// A return earns the exemption when none of the locals it carries leaves the frame
	// elsewhere. Any other site has to carry an owned aggregate.
	exemptAsReturn := es.isReturn && escaping.Intersection(outOfFrame).Len() == 0
	if !exemptAsReturn && !c.escapesAsOwnedCarrier(e, fieldBorrowGraph) {
		return false
	}
	component := escaping.Clone()
	if p, ok := exprPlace(e); ok && p.root > 0 {
		component.Add(p.root)
	}
	for root, edges := range fieldBorrowGraph {
		if component.Contains(root) {
			continue
		}
		// A binding that is dead at ref does not pin the component: its borrow of a component
		// node is never dereferenced again, so co-moving the node observes nothing. Dead means
		// moved before ref, or not live after it. At a return every local is dead — nothing
		// runs after — so only a parameter or a longer-lived store can pin a returned
		// component; a live external alias at a store or argument site still blocks the move.
		if info.StateBefore(stmtRef, root) == liveness.Moved {
			continue
		}
		if c.fn.liveness != nil && !c.fn.liveness.IsLiveAfter(stmtRef, root) {
			continue
		}
		for _, edge := range edges {
			// This live outside binding borrows a node inside the component, so the component
			// is not self-contained: some node is referenced from outside. The move cannot
			// apply, and the value escapes normally.
			if component.Contains(edge.referent) {
				return false
			}
		}
	}
	return true
}

// escapesAsOwnedCarrier reports whether the outgoing value e is an owned aggregate holding
// borrows of locals in its fields, rather than being a borrow itself. Only an owned aggregate
// has an internal graph to re-anchor. When e is itself a borrow — `&mut b`, a borrow-typed
// binding, a borrowed field — there is none, and away from a return it stays an escape. A
// return does not consult this at all, since the frame it leaves is gone. The recorded type
// of e cannot make this call: a field read auto-derefs a borrow field to its owned inner, so
// a borrow read back reads as owned. The borrow-edge graph drives the decision instead.
//
//   - A `&mut b` / `&b` expression is the borrow itself.
//   - A fresh object or tuple literal is an owned carrier; its borrows are nested fields.
//   - A place is an owned carrier unless a borrow edge sits at or above the read place, which
//     makes the read project onto a borrow. `return a` over a → b at [peer] reads the owned
//     object a, while `return a.peer` over the same edge reads the borrow, and `return a` over
//     a → b at [] reads a borrow-typed binding.
//   - Any other carrier — an if/match, a call result — is conservatively a bare borrow, so an
//     ambiguous outgoing value stays an escape rather than a speculative component move.
func (c *checker) escapesAsOwnedCarrier(
	e ast.Expr,
	fieldBorrowGraph map[liveness.VarID][]fieldBorrow,
) bool {
	switch e.(type) {
	case *ast.BorrowExpr:
		return false
	case *ast.ObjectExpr, *ast.TupleExpr:
		return true
	}
	p, ok := exprPlace(e)
	if !ok || p.root <= 0 {
		return false
	}
	for _, edge := range fieldBorrowGraph[p.root] {
		if pathHasPrefix(p.path, edge.path) {
			return false
		}
	}
	return true
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
// contribute:
//
//   - A borrow of a local written anywhere in e, such as the `&mut b` in `{peer: &mut b}` or
//     in an if/else branch. borrowsIn finds these, descending carriers and stopping at call
//     and nested-function boundaries.
//   - The edges under a place e names, a whole binding `a` or a field `a.peer`. They
//     contribute the locals they transitively reach, filtered to the place's field path so a
//     field return follows only that field's edges.
func (c *checker) escapingLocalsOf(
	e ast.Expr,
	fieldBorrowGraph map[liveness.VarID][]fieldBorrow,
) set.Set[liveness.VarID] {
	out := set.NewSet[liveness.VarID]()
	if c.fn == nil || fieldBorrowGraph == nil || e == nil {
		return out
	}
	for _, b := range borrowsIn(e) {
		if referent, ok := c.isLocalReferent(b.Arg); ok {
			out.Add(referent)
		}
	}
	if p, ok := exprPlace(e); ok && p.root > 0 {
		c.collectBorrowedFrom(p.root, p.path, out, set.NewSet[liveness.VarID](), fieldBorrowGraph)
	}
	return out
}

// addBorrowEdge records that the binding root borrows the function-local referent at the
// given field path, allocating the root's edge list on first use. A duplicate edge with
// the same path and referent is ignored, so repeated walks keep one copy rather than
// accumulating identical edges.
func (c *checker) addBorrowEdge(root liveness.VarID, path []placeSeg, referent liveness.VarID) {
	fb := fieldBorrow{path: path, referent: referent}
	if containsFieldBorrow(c.fn.eagerBorrowGraph[root], fb) {
		return
	}
	c.fn.eagerBorrowGraph[root] = append(c.fn.eagerBorrowGraph[root], fb)
	c.markBorrowDirty(root)
}

// recordBorrowEdges records which function-locals the binding destVarID borrows and at
// which field. `val a = {peer: &mut b}` records a → b at path [peer], and a whole-binding
// move `val a2 = a` carries a's edges into a2 at the same paths. A `var` reassignment reuses
// the same VarID, so recording clears the binding's prior edges first: `a = &mut e` after `a =
// &mut d` leaves only a → e. The caller flushes the dirtied roots into borrowGens once the
// statement's borrows are recorded.
func (c *checker) recordBorrowEdges(destVarID int, init ast.Expr) {
	if c.fn == nil || c.fn.eagerBorrowGraph == nil || destVarID <= 0 || init == nil {
		return
	}
	root := liveness.VarID(destVarID)
	c.clearEagerSubtree(root, nil)
	c.recordBorrowSources(root, nil, init)
}

// recordBorrowSources records the borrow edges the expression e contributes to the binding
// root, at base, the field path reached so far:
//
//   - A direct `&mut b` of a local records an edge at base.
//   - An object property descends with base extended by the property name.
//   - A tuple element and a spread descend at base unchanged. A field path is a chain of
//     named segments, and neither a tuple index nor a spread contributes one: a tuple index
//     is a number, not a field name, and a spread merges its source's fields without naming
//     them. The place model approximates a read of either to its container, so the borrow
//     stays attributed to base.
//   - A place expression copies that place's edges, re-rooted under root at base.
//   - Any other carrier, such as an if/else branch, contributes its inline borrows at base
//     through borrowsIn.
//
// The walk stops at a call or nested-function boundary.
func (c *checker) recordBorrowSources(root liveness.VarID, base []placeSeg, e ast.Expr) {
	switch e := e.(type) {
	case *ast.BorrowExpr:
		if referent, ok := c.isLocalReferent(e.Arg); ok && referent != root {
			c.addBorrowEdge(root, base, referent)
		}
	case *ast.ObjectExpr:
		for _, elem := range e.Elems {
			switch el := elem.(type) {
			case *ast.PropertyExpr:
				if el.Value != nil {
					if name, ok := objKeyName(el.Name); ok {
						c.recordBorrowSources(root, appendSeg(base, name), el.Value)
					} else {
						// A computed key names no static field segment, so the borrow can't
						// be addressed by a field path. Keep base, attributing the value to the
						// enclosing object conservatively.
						c.recordBorrowSources(root, base, el.Value)
					}
				} else if ident, ok := el.Name.(*ast.IdentExpr); ok && ident.VarID > 0 {
					// A shorthand property `{peer}` is `{peer: peer}`: the field peer holds
					// the value of the binding peer. objKeyName would give the field name,
					// but the value's edges are reached through the binding's VarID, so read
					// both the name and the VarID from the IdentExpr directly. A shorthand
					// key is always an identifier, never a computed or string key.
					c.copyPlaceEdges(root, appendSeg(base, ident.Name), movePlace{root: liveness.VarID(ident.VarID)})
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
	case *ast.CallExpr, *ast.TaggedTemplateLitExpr, *ast.FuncExpr:
		return
	default:
		// A place names another binding whose value e copies, as in `val a2 = a` or `val c =
		// a.peer`. copyPlaceEdges transfers that binding's edges to root, re-rooted at base.
		if p, ok := exprPlace(e); ok && p.root > 0 {
			c.copyPlaceEdges(root, base, p)
			return
		}
		// Any other carrier expression contributes its inline borrows of locals at base, as
		// in the `if cond { &mut b } else { … }` of `val a = if cond { &mut b } else { … }`.
		// The walk descends through it but stops at call and nested-function boundaries.
		for _, b := range borrowsIn(e) {
			if referent, ok := c.isLocalReferent(b.Arg); ok && referent != root {
				c.addBorrowEdge(root, base, referent)
			}
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
		c.addBorrowEdge(root, appendPath(base, suffix), edge.referent)
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
// pattern and an extractor pattern record nothing today; projecting them would extend this
// switch once they need borrow tracking.
func (c *checker) recordPatternPlaceEdges(pat ast.Pat, src movePlace) {
	switch pat := pat.(type) {
	case *ast.IdentPat:
		if pat.VarID > 0 {
			c.copyPlaceEdges(liveness.VarID(pat.VarID), nil, src)
		}
	case *ast.ObjectPat:
		for _, elem := range pat.Elems {
			switch e := elem.(type) {
			case *ast.ObjShorthandPat:
				if e.VarID > 0 {
					c.copyPlaceEdges(liveness.VarID(e.VarID), nil, extendPlace(src, e.Key.Name))
				}
				if e.Default != nil {
					// The property may be absent, so the leaf can take the shorthand
					// default, such as `val {peer = &mut b} = obj`.
					c.recordBorrowEdges(e.VarID, e.Default)
				}
			case *ast.ObjKeyValuePat:
				c.recordPatternPlaceEdges(e.Value, extendPlace(src, e.Key.Name))
			}
		}
	case *ast.TuplePat:
		for _, elem := range pat.Elems {
			c.recordPatternPlaceEdges(elem, src)
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

// recordEscapeSite defers the escape decision for a value flowing out of the frame to the
// post-pass, capturing the outgoing expression and the program point it flows out at. The
// post-pass needs the complete borrow-edge graph and the consumed lattice, neither of which
// is final mid-walk, so it cannot decide a self-contained component move inline.
func (c *checker) recordEscapeSite(e ast.Expr, stmtRef liveness.StmtRef) {
	c.recordEscapeSiteKind(e, stmtRef, false)
}

// recordEscapeSiteKind is recordEscapeSite with the site kind spelled out. isReturn marks a
// `return`, which resolveComponentEscapes decides under a weaker rule than a store or an
// argument.
func (c *checker) recordEscapeSiteKind(e ast.Expr, stmtRef liveness.StmtRef, isReturn bool) {
	if c.fn == nil || e == nil {
		return
	}
	c.fn.escapeSites = append(c.fn.escapeSites, escapeSite{expr: e, stmtRef: stmtRef, isReturn: isReturn})
}

// checkReturnEscape records the return value as an escape site. `return a` where a borrows
// b, `return &mut b`, and `return {peer: &mut b}` all carry a borrow of b out of the frame;
// resolveComponentEscapes later decides each as a component move or an escape.
func (c *checker) checkReturnEscape(retExpr ast.Expr, stmtRef liveness.StmtRef) {
	c.recordEscapeSiteKind(retExpr, stmtRef, true)
}

// checkParamFieldStoreEscape handles a field store `recv.f = source`. Storing a value that
// borrows a local into a parameter's field escapes, since the parameter's object outlives
// the frame. A store into a local receiver does not escape and is not tracked.
func (c *checker) checkParamFieldStoreEscape(recv, source ast.Expr, stmtRef liveness.StmtRef) {
	if c.fn == nil || c.fn.eagerBorrowGraph == nil {
		return
	}
	rp, ok := exprPlace(recv)
	if !ok || rp.root <= 0 || !c.fn.paramVarIDs.Contains(rp.root) {
		return
	}
	c.recordEscapeSite(source, stmtRef)
}

// recordFieldStoreEdges records a borrow edge for a field store `recv.f = source` into a
// LOCAL receiver, rooted at recv's place extended by f. A store `b.peer = &mut d` records b →
// d at [peer], so a later flow-out of b finds the borrow of d. The store into a local does
// not escape until b itself flows out, unlike the store into a parameter's field, which
// checkParamFieldStoreEscape reports at once. It is a strong update on the stored field's
// subtree: it clears the [f] subtree before recording, so a repoint `b.peer = &mut e` after
// `b.peer = &mut d` leaves only b → e at [peer] while a sibling edge b → x at [data] survives.
// It then flushes the dirtied root into borrowGens at stmtRef.
func (c *checker) recordFieldStoreEdges(
	recv ast.Expr,
	field string,
	source ast.Expr,
	stmtRef liveness.StmtRef,
) {
	if c.fn == nil || c.fn.eagerBorrowGraph == nil {
		return
	}
	rp, ok := exprPlace(recv)
	if !ok || rp.root <= 0 || c.fn.paramVarIDs.Contains(rp.root) {
		return
	}
	base := appendSeg(rp.path, field)
	c.clearEagerSubtree(rp.root, base)
	c.recordBorrowSources(rp.root, base, source)
	c.flushBorrowDirty(stmtRef)
}

// collectBorrowedFrom adds to out every function-local the read place rooted at root, with
// field path filter, exposes through borrow edges.
//
// The first hop keeps only edges on the filter path:
//
//   - at the filter, such as a → b at [peer] for a read of a.peer;
//   - beneath it, at [peer, …];
//   - above it, at [] where a wholly borrows b.
//
// So a read of a.peer follows a → b on any of those, but not a disjoint a → c at [data].
// Each local the first hop reaches is then followed in full through collectAllFrom, since a
// borrow exposes the whole referent.
//
// root is the starting point, not itself collected, except when a borrow cycle reaches back
// to it. Collecting it then is sound: a local that borrows root and itself escapes carries
// root's data out too.
func (c *checker) collectBorrowedFrom(
	root liveness.VarID,
	filter []placeSeg,
	out, seen set.Set[liveness.VarID],
	fieldBorrowGraph map[liveness.VarID][]fieldBorrow,
) {
	for _, edge := range fieldBorrowGraph[root] {
		if !pathPrefixRelated(edge.path, filter) {
			continue
		}
		out.Add(edge.referent)
		c.collectAllFrom(edge.referent, out, seen, fieldBorrowGraph)
	}
}

// collectAllFrom adds to out every function-local reachable from node through borrow edges,
// following every edge regardless of field path, since reaching a binding through a borrow
// exposes all of it. For node a with edges a → b at [peer] and a → c at [data], it collects
// both b and c, then walks each of their edges in turn. The seen set terminates borrow
// cycles.
func (c *checker) collectAllFrom(
	node liveness.VarID,
	out, seen set.Set[liveness.VarID],
	fieldBorrowGraph map[liveness.VarID][]fieldBorrow,
) {
	if seen.Contains(node) {
		return
	}
	seen.Add(node)
	for _, edge := range fieldBorrowGraph[node] {
		out.Add(edge.referent)
		c.collectAllFrom(edge.referent, out, seen, fieldBorrowGraph)
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
