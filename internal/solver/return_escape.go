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
// path through the local it names. A store into a caller-owned target is the exception. The
// caller reads the target only after the return, so a use of the local in the frame changes
// nothing it observes. The store's loan covers a later read through the target in the body,
// and the outflow check covers a second path that outlives the frame. See
// callerOwnedStoreAccepted. A consuming argument takes no such loan, and its bare borrow stays
// an escape. See componentMoveCovers.
//
// Two sites can each hand out a path to one local. Two writers are Rule 3 of
// planning/lifetimes/requirements.md and two readers never conflict, but a writer beside a
// reader lets one change what the other expects to hold still. MixedOutflowPathsError reports
// that pair.
//
// A returned value that reaches one local twice hands the caller two views of it. That is a
// hazard once a write can go through either, and reportSharedReturnPaths reports it. See
// return_shared_paths.go.
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

// MixedOutflowPathsError fires when a local leaves the frame at two sites that disagree about
// whether it can change. One hands the caller or a callee a mutable path to the local and the
// other an immutable one, so a write through the first changes data the second expects to hold
// still. Two mutable paths are Rule 3 of planning/lifetimes/requirements.md and are accepted,
// and so are two immutable ones. An owned path disagrees with any borrow, since its receiver can
// move or freeze the data the borrow reaches.
//
// SharedReturnPathsError covers the same pair inside one returned value. This one covers a pair
// split across two sites, such as a store and a return.
type MixedOutflowPathsError struct {
	// LocalName is the local reached twice, for the message.
	LocalName string
	// `Owned` says one of the two paths carries the local's data by value. The message then
	// names an owned path and a borrow rather than two borrows.
	Owned bool
	// `node` is the later of the two outgoing expressions, and `other` the earlier one.
	node  ast.Node
	other ast.Span
}

func (*MixedOutflowPathsError) isSolverError()        {}
func (e *MixedOutflowPathsError) Span() ast.Span      { return e.node.Span() }
func (e *MixedOutflowPathsError) Related() []ast.Span { return []ast.Span{e.other} }
func (e *MixedOutflowPathsError) Message() string {
	if e.Owned {
		return fmt.Sprintf("'%s' leaves the function both as an owned value and through a borrow", e.LocalName)
	}
	return fmt.Sprintf("'%s' leaves the function through a mutable path and an immutable one", e.LocalName)
}

// StoredBorrowAliasError fires when a store hands the caller a borrow of a local that a binding
// in the body still reaches through a borrow of the other mutability, and the body reads the
// stored path again. `val a = {peer: &mut b}; p.r = &b; a.peer.value = 5; p.r.value` writes b
// through a while p.r expects it to hold still.
type StoredBorrowAliasError struct {
	// Place names the stored local, and Alias the binding that still reaches it.
	Place string
	Alias string
	// StoredMut says whether a write can go through the stored borrow. The alias's borrow is
	// the other mutability.
	StoredMut bool
	node      ast.Node
}

func (*StoredBorrowAliasError) isSolverError()        {}
func (e *StoredBorrowAliasError) Span() ast.Span      { return e.node.Span() }
func (e *StoredBorrowAliasError) Related() []ast.Span { return nil }
func (e *StoredBorrowAliasError) Message() string {
	if e.StoredMut {
		return fmt.Sprintf("cannot store a mutable borrow of '%s' while '%s' still borrows it as immutable", e.Place, e.Alias)
	}
	return fmt.Sprintf("cannot store an immutable borrow of '%s' while '%s' still borrows it as mutable", e.Place, e.Alias)
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
	// callerOwned marks a store into a target the caller owns, a borrow parameter or a
	// borrowing receiver. The store recorded a loan the target holds while the body reads it.
	// callerOwnedStoreAccepted reads the flag to accept a bare borrow the loan covers.
	callerOwned bool
	// `reaches` lists the places the site carries out and whether a write can go through each.
	// A call store sets it, since what the callee stores comes from its signature rather than
	// from the argument expression. Every other site leaves it nil, and siteReaches reads the
	// expression instead.
	reaches []elementReach
}

// outflowPath is one path a site hands out of the frame to a local: the place it reaches,
// whether a write can go through it, and the outgoing expression it leaves through.
type outflowPath struct {
	place movePlace
	mut   bool
	owned bool
	node  ast.Node
}

// placeCopy is one place a binding takes its value from. dest is the binding that receives
// it and destPath the field of dest it lands in, empty for the whole binding. src is the place
// read, and expr the expression that reads it. `val a = {x: b}` records dest a, destPath [x],
// and src b.
type placeCopy struct {
	dest     liveness.VarID
	destPath []placeSeg
	src      movePlace
	expr     ast.Expr
}

// resolveComponentEscapes decides every recorded escape site once the body is fully walked,
// so the borrow-edge graph is complete and the consumed lattice `info` is known. A site
// whose outgoing value carries borrows of function-locals is one of four things:
//
//   - a self-contained connected-component move, which is allowed and consumes the
//     component's locals;
//   - a store into a caller-owned target whose loan keeps the stored borrow the only path to
//     the local, which is allowed;
//   - one of two sites that hand out a mutable and an immutable path to one local, which is
//     reported as that pair;
//   - an ordinary escape, which is reported.
//
// It returns `true` when it consumed any co-moved local, so the caller knows to recompute the
// lattice before the use-after-move scan reads it.
func (c *checker) resolveComponentEscapes(
	info *liveness.MoveInfo,
	flowBorrowGraph *flowBorrowGraph,
) bool {
	consumed := false
	outOfFrame := c.localsLeavingOutsideAReturn(flowBorrowGraph)
	// A local the non-return sites hand out through both a mutable and an immutable path is
	// reported once here. Every site carrying it is left to that report rather than adding an
	// escape on top.
	mixed := c.reportMixedOutflows(outOfFrame)
	// Return index to the owned type its borrows strip to. Committed after every site is decided.
	ownedReturns := map[int]soltype.Type{}
	for _, es := range c.fn.escapeSites {
		// The flow-sensitive borrow-edge graph at this site's program point. A borrow cleared by an
		// earlier reassignment is gone here, and one set on a reaching branch is joined in. Passed
		// explicitly to each escape helper so they read this per-point snapshot rather than the
		// whole-body eager graph.
		fieldBorrowGraph := flowBorrowGraph.fieldBorrowGraphBefore(es.stmtRef)
		escaping := c.siteEscaping(es, fieldBorrowGraph)
		reached := reachableLocals(escaping, fieldBorrowGraph)
		// A return that moves a local out carries no borrow, so it leaves escaping empty. Its
		// paths still count against a store of the same local, so they join `reached` here.
		var returned []elementReach
		if es.isReturn {
			returned = c.siteReaches(es, fieldBorrowGraph)
			for _, r := range returned {
				reached.Add(r.place.root)
			}
		}
		if slices.ContainsFunc(reached.ToSlice(), mixed.Contains) {
			continue
		}
		// A return ends the frame, so a borrow leaving through it alone is the only path to its
		// local. A local that also leaves through another site keeps that exemption as long as
		// both paths agree about whether it can change.
		exemptAsReturn := false
		alsoLeaves := false
		if es.isReturn {
			leaves, conflict := c.returnOutflowConflict(es.expr, returned, reached, outOfFrame)
			if conflict != nil {
				c.reportMixedOutflow(conflict, es.expr)
				continue
			}
			exemptAsReturn = true
			alsoLeaves = leaves
		}
		if escaping.Len() == 0 {
			continue
		}
		if c.componentMoveCovers(es, escaping, exemptAsReturn, info, fieldBorrowGraph) {
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
			if idx, graph, root, ok := c.returnCarrier(es.expr, fieldBorrowGraph); ok {
				// A returned value that reaches one local twice hands the caller two views of it,
				// which is a hazard as soon as a write can go through either.
				c.reportSharedReturnPaths(c.fn.returns[idx], root, graph, es.expr)
				// When the moved graph is a tree — every borrowed local reached exactly once with
				// no cycle — the return value is the sole owner of each node, so owning them in
				// the type is honest. The rewrites are collected here and committed together,
				// since one return left borrowed holds back the rest. A local that also leaves
				// through another site is not the return's alone, so the return keeps its
				// borrow type.
				if !alsoLeaves {
					if owned, ok := c.ownedReturnType(es.expr, idx, graph, root); ok {
						ownedReturns[idx] = owned
					}
				}
			}
			consumed = true
			continue
		}
		if es.callerOwned && c.callerOwnedStoreAccepted(es, escaping, info, fieldBorrowGraph) {
			continue
		}
		c.reportEscapingLocals(escaping, es.expr)
	}
	c.commitOwnedReturnTypes(ownedReturns)
	c.fn.escapeSites = nil
	return consumed
}

// siteEscaping returns the function-locals the site carries out directly. A site that lists its
// `reaches` carries their roots. Any other site carries what escapingLocalsOf finds in its
// expression.
func (c *checker) siteEscaping(es escapeSite, fieldBorrowGraph map[liveness.VarID][]fieldBorrow) set.Set[liveness.VarID] {
	if es.reaches == nil {
		return c.escapingLocalsOf(es.expr, fieldBorrowGraph)
	}
	out := set.NewSet[liveness.VarID]()
	for _, r := range es.reaches {
		out.Add(r.place.root)
	}
	return out
}

// siteReaches returns every path the site hands out to a local, with whether a write can go
// through it. A local the site reaches only by following every borrow edge of a binding it
// carries has no known path. After `val a = {x: {value: 1}, peer: &mut b}`, storing `&mut a.x`
// reaches b that way. Such a local is listed once each way, so it disagrees with any other path
// to the same local.
//
// A path to a binding also reaches every place whose data moved into that binding. After
// `val q = b`, returning q hands the caller b's data as well as q's.
func (c *checker) siteReaches(es escapeSite, fieldBorrowGraph map[liveness.VarID][]fieldBorrow) []elementReach {
	// Start from the paths with a known place and mutability. A call store lists them on the
	// site, and any other site derives them from its outgoing expression.
	reaches := es.reaches
	if reaches == nil {
		reaches = c.reachesOf(es.expr, fieldBorrowGraph)
	}
	origins := c.movedOrigins()
	for _, r := range slices.Clone(reaches) {
		for _, o := range originsOf(r.place, origins) {
			reaches = append(reaches, elementReach{place: o, mut: r.mut, owned: r.owned})
		}
	}
	known := set.NewSet[liveness.VarID]()
	for _, r := range reaches {
		known.Add(r.place.root)
	}
	// Every local the site carries out, directly or through the borrow graph, that no known
	// path covers is added as the whole local, once as a writer and once as a reader. Sorting
	// the IDs keeps the order of the added paths stable from run to run.
	ids := reachableLocals(c.siteEscaping(es, fieldBorrowGraph), fieldBorrowGraph).ToSlice()
	slices.Sort(ids)
	for _, id := range ids {
		if known.Contains(id) {
			continue
		}
		whole := movePlace{root: id}
		reaches = append(reaches, elementReach{place: whole, mut: true}, elementReach{place: whole, mut: false})
	}
	return reaches
}

// reachesOf returns the paths the outgoing expression e takes into function-locals. An object or
// tuple literal contributes each element's paths. A place or a borrow contributes the paths
// elementReferents finds for it. Any other expression, such as an `if`/`else`, contributes the
// paths of each borrow borrowsIn finds in it, so `if c { &mut b } else { &b }` reaches b once
// as a writer and once as a reader.
func (c *checker) reachesOf(e ast.Expr, fieldBorrowGraph map[liveness.VarID][]fieldBorrow) []elementReach {
	switch e := e.(type) {
	case *ast.TupleExpr:
		var out []elementReach
		for _, el := range e.Elems {
			out = append(out, c.reachesOf(el, fieldBorrowGraph)...)
		}
		return out
	case *ast.ObjectExpr:
		var out []elementReach
		for _, elem := range e.Elems {
			switch el := elem.(type) {
			case *ast.PropertyExpr:
				if el.Value != nil {
					out = append(out, c.reachesOf(el.Value, fieldBorrowGraph)...)
				} else if ident, ok := el.Name.(*ast.IdentExpr); ok {
					out = append(out, c.reachesOf(ident, fieldBorrowGraph)...)
				}
			case *ast.ObjSpreadExpr:
				out = append(out, c.reachesOf(el.Value, fieldBorrowGraph)...)
			}
		}
		return out
	}
	out := c.elementReferents(e, fieldBorrowGraph)
	if _, isBorrow := e.(*ast.BorrowExpr); !isBorrow {
		if _, isPlace := exprPlace(e); !isPlace {
			for _, b := range borrowsIn(e) {
				out = append(out, c.elementReferents(b, fieldBorrowGraph)...)
			}
		}
	}
	// A place the site moves out hands its data over by value. A parameter is left out, since
	// the caller already holds whatever a borrow parameter reaches.
	if c.fn.movedSources != nil && c.fn.movedSources.Contains(e) {
		if p, ok := c.localReferentPlace(e); ok {
			out = append(out, elementReach{place: p, mut: true, owned: true})
		}
	}
	return out
}

// movedOrigins maps each binding to the copies that moved data into it, keeping the copies in
// placeCopies that movedSources records as moves. A copy of a borrow moves nothing, and its
// borrow edges already say what it reaches.
func (c *checker) movedOrigins() map[liveness.VarID][]placeCopy {
	out := map[liveness.VarID][]placeCopy{}
	if c.fn.movedSources == nil {
		return out
	}
	for _, pc := range c.fn.placeCopies {
		if c.fn.movedSources.Contains(pc.expr) {
			out[pc.dest] = append(out[pc.dest], pc)
		}
	}
	return out
}

// originsOf returns every place whose data reaches the place p through a chain of moves. After
// `val q = b` and `val r = q`, the origins of r are q and b. A move lands in one field of its
// destination, so a path through a different field does not reach it. After
// `val a = {x: b, y: c}`, the origins of a.y are c alone.
func originsOf(p movePlace, origins map[liveness.VarID][]placeCopy) []movePlace {
	var out []movePlace
	seen := set.NewSet[ast.Expr]()
	pending := []movePlace{p}
	for len(pending) > 0 {
		at := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		for _, pc := range origins[at.root] {
			if seen.Contains(pc.expr) || !pathPrefixRelated(pc.destPath, at.path) {
				continue
			}
			seen.Add(pc.expr)
			out = append(out, pc.src)
			pending = append(pending, pc.src)
		}
	}
	return out
}

// localsLeavingOutsideAReturn returns the paths each function-local takes out of the frame
// somewhere other than a return: a field store into a parameter, a store a call makes into a
// caller-owned argument, or a consuming argument. Each hands a path to the local that outlives
// the frame, either to the caller's object or to the callee.
//
// A return's exemption rests on the frame being gone, so nothing can reach the local again. A
// local already on this list has a second path out, so the return holds an exemption only while
// the two paths agree about whether the local can change. In
//
//	fn f(p: &mut {r: &{value: number}}) -> &mut {value: number} {
//		val mut b = {value: 0}
//		p.r = &b
//		return &mut b
//	}
//
// the caller ends up holding b through the immutable p.r AND the mutable return, and a write
// through the return changes what p.r expects to hold still.
//
// Every site is scanned before any is decided, so a store written after the return in the source
// counts the same as one written before it.
func (c *checker) localsLeavingOutsideAReturn(flowBorrowGraph *flowBorrowGraph) map[liveness.VarID][]outflowPath {
	out := map[liveness.VarID][]outflowPath{}
	for _, es := range c.fn.escapeSites {
		if es.isReturn {
			continue
		}
		graph := flowBorrowGraph.fieldBorrowGraphBefore(es.stmtRef)
		for _, r := range c.siteReaches(es, graph) {
			id := r.place.root
			out[id] = append(out[id], outflowPath{place: r.place, mut: r.mut, owned: r.owned, node: es.expr})
		}
	}
	return out
}

// reportMixedOutflows reports each local that two non-return sites hand out with different
// mutability, and returns the locals it reported. Paths from one site are left to that site,
// since a single value reaching a local twice is reportSharedReturnPaths' concern for a return
// and the loan check's for anything else.
func (c *checker) reportMixedOutflows(outOfFrame map[liveness.VarID][]outflowPath) set.Set[liveness.VarID] {
	reported := set.NewSet[liveness.VarID]()
	ids := make([]liveness.VarID, 0, len(outOfFrame))
	for id := range outOfFrame {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if earlier, later, ok := firstMixedPair(outOfFrame[id], outOfFrame[id]); ok {
			c.reportMixedOutflow(&outflowConflict{id: id, other: earlier.node, owned: earlier.owned || later.owned}, later.node)
			reported.Add(id)
		}
	}
	return reported
}

// firstMixedPair returns the first pair of paths, one from `as` and one from `bs`, that leave through
// different expressions, reach overlapping data, and disagree. Two borrows disagree when one can
// write and the other cannot. An owned path disagrees with any borrow. Two owned paths are two
// moves of the local, which the use-after-move check reports when one path runs after the other.
// The pair comes back with the path that starts earlier in the source first.
func firstMixedPair(as, bs []outflowPath) (outflowPath, outflowPath, bool) {
	for _, a := range as {
		for _, b := range bs {
			if a.node == b.node || !placesOverlap(a.place, b.place) {
				continue
			}
			if a.owned == b.owned && (a.owned || a.mut == b.mut) {
				continue
			}
			if b.node.Span().Start.Offset < a.node.Span().Start.Offset {
				return b, a, true
			}
			return a, b, true
		}
	}
	return outflowPath{}, outflowPath{}, false
}

// reportMixedOutflow reports that the local conflict names leaves through blame and through
// `conflict.other` with paths that disagree. A borrow inside `blame` then takes no second diagnostic
// from the loan check.
func (c *checker) reportMixedOutflow(conflict *outflowConflict, blame ast.Node) {
	if e, ok := blame.(ast.Expr); ok {
		c.noteSharedPathBlame(e)
	}
	c.report(&MixedOutflowPathsError{
		LocalName: c.varIDToName(conflict.id), Owned: conflict.owned,
		node: blame, other: conflict.other.Span(),
	})
}

// outflowConflict is a local that leaves through two expressions with paths that disagree. `other`
// is the expression the earlier path leaves through, and `owned` says either path carries the
// local's data by value.
type outflowConflict struct {
	id    liveness.VarID
	other ast.Node
	owned bool
}

// returnOutflowConflict compares the return expression `ret` against the paths outOfFrame records
// for the locals it reaches. `reaches` is what `ret` hands out. `leaves` says some reached local also
// leaves through another site. `conflict` names the local and the other site when one of those
// paths disagrees with the return, and is nil otherwise.
func (c *checker) returnOutflowConflict(
	ret ast.Expr,
	reaches []elementReach,
	reached set.Set[liveness.VarID],
	outOfFrame map[liveness.VarID][]outflowPath,
) (bool, *outflowConflict) {
	var returned []outflowPath
	for _, r := range reaches {
		returned = append(returned, outflowPath{place: r.place, mut: r.mut, owned: r.owned, node: ret})
	}
	ids := reached.ToSlice()
	slices.Sort(ids)
	leaves := false
	for _, id := range ids {
		others, ok := outOfFrame[id]
		if !ok {
			continue
		}
		leaves = true
		if ours, other, mixed := firstMixedPair(returned, others); mixed {
			return true, &outflowConflict{id: id, other: other.node, owned: ours.owned || other.owned}
		}
	}
	return leaves, nil
}

// callerOwnedStoreAccepted reports whether a store into a caller-owned target leaves the stored
// borrow as the only path to each local it carries once the function returns. The caller reads
// the target only after the return, so what it sees is the locals as the body leaves them. A
// write in the body before then changes nothing the caller can observe. Three conditions make
// the store sound:
//
//   - Every local the store reaches has a loan the store recorded. The loan lasts while the
//     body can still read the target, so a write or move that a later read through the target
//     would see is weighed against it and reported there.
//   - A binding outside the stored locals that still reaches one of them through a borrow edge
//     agrees with the stored borrow about whether the local can change, or the body never
//     reads the target again. A loan names the place written at the borrow site, so it does
//     not see a write through such a binding. A disagreeing binding is reported here as a
//     StoredBorrowAliasError, and the store counts as accepted so no escape is reported too.
//   - Every other path the local leaves through agrees with the store. reportMixedOutflows and
//     returnOutflowConflict check that.
//
// A store inside a loop is accepted on the same terms. The loan check walks the body once, so a
// write early in the loop body is not weighed against the loan the previous iteration's store
// left behind. Every held loan shares that gap, which #1529 tracks.
func (c *checker) callerOwnedStoreAccepted(
	es escapeSite,
	escaping set.Set[liveness.VarID],
	info *liveness.MoveInfo,
	fieldBorrowGraph map[liveness.VarID][]fieldBorrow,
) bool {
	component := reachableLocals(escaping, fieldBorrowGraph)
	for _, id := range component.ToSlice() {
		if !c.hasCallerOwnedLoan(id, es.stmtRef) {
			return false
		}
	}
	if !c.storeTargetReadAfter(es.stmtRef) {
		return true
	}
	stored := map[liveness.VarID]bool{}
	for _, r := range c.siteReaches(es, fieldBorrowGraph) {
		stored[r.place.root] = r.mut
	}
	roots := make([]liveness.VarID, 0, len(fieldBorrowGraph))
	for root := range fieldBorrowGraph {
		roots = append(roots, root)
	}
	slices.Sort(roots)
	for _, root := range roots {
		if component.Contains(root) {
			continue
		}
		if info.StateBefore(es.stmtRef, root) == liveness.Moved {
			continue
		}
		if c.fn.liveness != nil && !c.fn.liveness.IsLiveAfter(es.stmtRef, root) {
			continue
		}
		for _, edge := range fieldBorrowGraph[root] {
			storedMut, ok := stored[edge.referent]
			if !ok || storedMut == edge.mut {
				continue
			}
			c.report(&StoredBorrowAliasError{
				Place: c.varIDToName(edge.referent), Alias: c.varIDToName(root),
				StoredMut: storedMut, node: es.expr,
			})
			return true
		}
	}
	return true
}

// storeTargetReadAfter reports whether the body reads the target of the caller-owned store at
// `ref` after it. The target is the binding that holds the loan the store recorded.
func (c *checker) storeTargetReadAfter(ref liveness.StmtRef) bool {
	if c.fn.liveness == nil {
		return true
	}
	return slices.ContainsFunc(c.fn.loans, func(l loan) bool {
		return l.callerOwned && l.ref == ref && c.fn.liveness.IsLiveAfter(ref, l.holder)
	})
}

// hasCallerOwnedLoan reports whether a store at `ref` recorded a loan of `id` held by a
// caller-owned target.
func (c *checker) hasCallerOwnedLoan(id liveness.VarID, ref liveness.StmtRef) bool {
	return slices.ContainsFunc(c.fn.loans, func(l loan) bool {
		return l.callerOwned && l.ref == ref && l.place.root == id
	})
}

// reachableLocals returns roots together with every local reachable from one of them through
// the borrow graph.
func reachableLocals(
	roots set.Set[liveness.VarID],
	fieldBorrowGraph map[liveness.VarID][]fieldBorrow,
) set.Set[liveness.VarID] {
	out := roots.Clone()
	pending := roots.ToSlice()
	for len(pending) > 0 {
		node := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		for _, edge := range fieldBorrowGraph[node] {
			if out.Contains(edge.referent) {
				continue
			}
			out.Add(edge.referent)
			pending = append(pending, edge.referent)
		}
	}
	return out
}

// componentMoveCovers reports whether the escape of es is a self-contained connected-component
// move rather than an ordinary escape. It holds when two conditions are met:
//
//   - `es` carries an owned aggregate, or it is a return exemptAsReturn admits. An owned
//     aggregate has internal edges for the move to re-anchor. A bare borrow has none, so away
//     from a return it stays an escape. A bare borrow is `&mut b`, a borrowed field, or a
//     borrow-typed binding.
//   - The component is self-contained: no live binding outside it borrows a node inside it.
//     The component is the outgoing value's root together with every local it transitively
//     borrows. A binding dead at ref does not count as an external reference, so a stray
//     unused borrow before a return does not block the move. The loop below explains what
//     "dead" covers.
//
// A return needs no aggregate because the frame does not survive it. Every local dies with the
// frame, so nothing in it reaches the value again. exemptAsReturn is `false` for a return whose
// locals also leave through another site with a path that disagrees about whether they can
// change.
//
// A store or a consuming argument leaves the frame running. The local a bare borrow names is
// still reachable from the frame, so the value is not the caller's alone, and the aggregate
// requirement stands. A store into a caller-owned target that fails it can still be accepted
// on the strength of its loan, which callerOwnedStoreAccepted decides.
//
// Two borrows of one local CAN leave together in a single returned value. The move accepts it,
// and ownedReturnType then declines to re-type it, so both stay borrowed. Two writers are Rule
// 3 and check. A writer beside a reader is what reportSharedReturnPaths reports.
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
	exemptAsReturn bool,
	info *liveness.MoveInfo,
	fieldBorrowGraph map[liveness.VarID][]fieldBorrow,
) bool {
	e, stmtRef := es.expr, es.stmtRef
	// Any site but an exempt return has to carry an owned aggregate.
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
	// refPath is the field path INSIDE referent the borrow reaches, empty for a borrow of the
	// whole binding. `val t = {p: &mut b.x}` records refPath [x] beside path [p]. It is what
	// separates two borrows of disjoint fields of one local from two borrows of the same field,
	// which reach the same data and are the pair reportSharedReturnPaths exists to catch.
	refPath []placeSeg
	// mut says a write can go through the borrow. `val a = {peer: &mut b}` records a mutable
	// edge and `val a = {peer: &b}` an immutable one. A borrow field carries its own
	// mutability, so an edge reached through another edge keeps its own answer.
	mut bool
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

// paramReferentOutlivesFrame reports whether `root` names a parameter whose referent belongs to
// the caller and so survives the call.
//
// Only a BORROW parameter does. `&T` and `&mut T` name data the caller still holds, so a borrow
// of a local written into one reaches the caller after the frame ends. An owned parameter,
// `mut T` or plain `T`, is MOVED into the frame. The caller gave up every handle at the call,
// and the value dies with the frame, so nothing written into it can outlive anything.
//
// A settled type answers directly. A `RefType` carrying a lifetime is a borrow. Anything else
// settled is owned: owned-immutable collapses to the bare inner, so a plain `T` parameter is
// recorded as its concrete type rather than wrapped, and owned-mutable is a `RefType` with no
// lifetime.
//
// An unsettled type keeps the stricter caller-owned answer. That covers a leaf the seed did not
// reach and an UNANNOTATED parameter, whose recorded type is an inference variable this reads
// as a leaf rather than following its bounds. So an unannotated owned parameter keeps reporting
// a store into it, where the same parameter written `mut T` does not.
func (c *checker) paramReferentOutlivesFrame(root liveness.VarID) bool {
	if c.fn == nil || !c.fn.paramVarIDs.Contains(root) {
		return false
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
		// Unsettled, so which kind it becomes is not known here. The stricter answer keeps a
		// store into it reported rather than silently skipped.
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
	p, ok := exprPlace(arg)
	if !ok || p.root <= 0 {
		return movePlace{}, false
	}
	if c.fn.paramVarIDs.Contains(p.root) {
		return movePlace{}, false
	}
	return p, true
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
// given field path, allocating the root's edge list on first use. mut says a write can go
// through the borrow. A duplicate edge with the same paths, referent and mutability is
// ignored, so repeated walks keep one copy rather than accumulating identical edges.
func (c *checker) addBorrowEdge(root liveness.VarID, path []placeSeg, referent liveness.VarID, refPath []placeSeg, mut bool) {
	fb := fieldBorrow{path: path, referent: referent, refPath: refPath, mut: mut}
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
		if src, ok := c.localReferentPlace(e.Arg); ok && src.root != root {
			c.addBorrowEdge(root, base, src.root, src.path, e.Mut)
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
			c.fn.placeCopies = append(c.fn.placeCopies, placeCopy{dest: root, destPath: base, src: p, expr: e})
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
		c.addBorrowEdge(root, appendPath(base, suffix), edge.referent, edge.refPath, edge.mut)
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

// recordCallerOwnedStore records a call store into a caller-owned target as an escape site.
// `reaches` lists the places the callee stores and whether a write can go through each, which the
// store's signature decides.
func (c *checker) recordCallerOwnedStore(e ast.Expr, stmtRef liveness.StmtRef, reaches []elementReach) {
	if c.fn == nil || e == nil {
		return
	}
	c.fn.escapeSites = append(c.fn.escapeSites, escapeSite{expr: e, stmtRef: stmtRef, callerOwned: true, reaches: reaches})
}

// checkParamFieldStoreEscape handles a field store `recv.f = source` into a BORROW parameter's
// field. The object that parameter names belongs to the caller and outlives the frame, so a
// value that borrows a local leaves the frame through the store. A store into a receiver that
// dies with the frame, a local or an owned parameter, is not tracked here.
//
// An owned aggregate source is recorded for the post-pass to weigh as a component move. A bare
// borrow source, such as `&mut b` or a borrowed field, takes a loan of each local it reaches.
// The parameter holds the loan while the body reads it, so the post-pass can accept the store
// as the only path to those locals.
func (c *checker) checkParamFieldStoreEscape(recv ast.Expr, field string, source ast.Expr, stmtRef liveness.StmtRef) {
	if c.fn == nil || c.fn.eagerBorrowGraph == nil {
		return
	}
	rp, ok := exprPlace(recv)
	if !ok || rp.root <= 0 || !c.paramReferentOutlivesFrame(rp.root) {
		return
	}
	base := appendSeg(rp.path, field)
	// The store repoints the field, so whatever an earlier store put there is unreachable
	// through it once every path out of the function runs this store. A loan from a store
	// before an `if` that holds this one still reaches the caller on the path that skips it.
	c.endLoansAtPostDominating(rp.root, base, stmtRef)
	if c.escapesAsOwnedCarrier(source, c.fn.eagerBorrowGraph) {
		c.recordEscapeSite(source, stmtRef)
		return
	}
	for _, r := range c.reachesOf(source, c.fn.eagerBorrowGraph) {
		c.recordStoreEdgeLoan(r.place, r.mut, rp.root, base, stmtRef, source)
	}
	c.fn.escapeSites = append(c.fn.escapeSites, escapeSite{expr: source, stmtRef: stmtRef, callerOwned: true})
}

// recordFieldStoreEdges records a borrow edge for a field store `recv.f = source` into a
// receiver that dies with the frame, a local or an owned parameter, rooted at recv's place
// extended by f. A store `b.peer = &mut d` records b → d at [peer], so a later flow-out of b
// finds the borrow of d. Such a store does not escape until b itself flows out, unlike a store
// into a BORROW parameter's field, which checkParamFieldStoreEscape hands to the caller. It is a
// strong update on the stored field's subtree: it clears the [f] subtree before recording, so
// a repoint `b.peer = &mut e` after
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
	if !ok || rp.root <= 0 || c.paramReferentOutlivesFrame(rp.root) {
		return
	}
	base := appendSeg(rp.path, field)
	c.clearEagerSubtree(rp.root, base)
	// The store repoints the field, so whatever it reached before is unreachable through it.
	// The loans at that field end with the edges, which is the same strong update on the same
	// subtree.
	c.endLoansAt(rp.root, base)
	c.recordBorrowSources(rp.root, base, source)
	c.flushBorrowDirty(stmtRef)
	// The receiver reaches the stored place from here on, so it holds a borrow of it that a
	// second borrow or a read has to respect. This is the field-store twin of the loan
	// recordCallStoreEdges derives from a call's store effect.
	if borrow, ok := source.(*ast.BorrowExpr); ok {
		if place, ok := loanPlace(borrow); ok {
			c.recordStoreEdgeLoan(place, borrow.Mut, rp.root, base, stmtRef, borrow)
		}
	}
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
