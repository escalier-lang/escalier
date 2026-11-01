package solver

import (
	"slices"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// This file holds the checker methods that emit the ownership events ownership_events.go
// describes during the walk. It opens and closes each body's event table, emits reads and
// writes, and emits a flow at each syntax site that has one. flow_settle.go decides the
// kind of each flow.

// firstEmit records that node emits its events in role at the CFG point at, and reports
// whether it had not done so before. root names the captured local when node is a closure.
// It returns false when no event table is open, so a caller that gets true can use
// c.fn.events.
func (c *checker) firstEmit(node ast.Node, role emitRole, at liveness.StmtRef, root liveness.VarID) bool {
	t := c.eventTable()
	if t == nil {
		return false
	}

	site := emitSite{node: node, role: role, at: at, root: root}
	if t.emitted.Contains(site) {
		return false
	}

	t.emitted.Add(site)
	return true
}

// eventTable returns the event table of the body being walked, or nil outside a body or
// when the body has no table.
func (c *checker) eventTable() *ownershipEvents {
	if c.fn == nil {
		return nil
	}
	return c.fn.events
}

// startOwnershipEvents opens the event table of the body about to be walked and records
// it in Info under the body's function node. names maps the body's VarIDs to their names.
// cfg is the body's control-flow graph and live the liveness of its locals over cfg.
func (c *checker) startOwnershipEvents(names map[liveness.VarID]string, cfg *liveness.CFG, live *liveness.LivenessInfo) {
	t := newOwnershipEvents(names)
	t.cfg, t.live = cfg, live
	c.fn.events = t

	if c.info.ownership == nil {
		c.info.ownership = map[ast.Node]*ownershipEvents{}
	}

	c.info.ownership[c.fn.node] = t
}

// emitEvent appends ev to the current body's table, attached to the CFG node at, and
// returns its index. It returns -1 outside a body.
func (c *checker) emitEvent(ev ownershipEvent, at liveness.StmtRef) int {
	t := c.eventTable()
	if t == nil {
		return -1
	}

	ev.base().at = at
	t.events = append(t.events, ev)
	index := len(t.events) - 1

	if fe, ok := ev.(flowEvent); ok {
		if th, ok := fe.flow().holder.target.(tempTarget); ok {
			t.temps[th.node] = append(t.temps[th.node], index)
		}
	}

	return index
}

// subsumePlaceChain marks e, and each receiver of the member and index chain e roots, as
// read by an event other than a use.
func (c *checker) subsumePlaceChain(e ast.Expr) {
	t := c.eventTable()
	if t == nil {
		return
	}

	for _, x := range placeChain(e) {
		t.subsumed.Add(x)
	}
}

// exprStmtRefs maps the expression of each expression statement in cfg's blocks to the
// statement's CFG point.
func exprStmtRefs(cfg *liveness.CFG) map[ast.Expr]liveness.StmtRef {
	out := map[ast.Expr]liveness.StmtRef{}

	for _, block := range cfg.Blocks {
		for i, stmt := range block.Stmts {
			if es, ok := stmt.(*ast.ExprStmt); ok {
				out[es.Expr] = liveness.StmtRef{BlockID: block.ID, StmtIdx: i}
			}
		}
	}

	return out
}

// eventStmtRef returns the CFG point the events of the expression being walked attach to.
// That is the point attachEventsAt set, while c.fn.eventStmt is the statement it was set
// under, and c.fn.eventStmt's point otherwise. ok is false outside a body or when the point
// is unknown.
func (c *checker) eventStmtRef() (liveness.StmtRef, bool) {
	if c.fn == nil {
		return liveness.StmtRef{}, false
	}

	if c.fn.exprAt.set && c.fn.exprAt.stmt == c.fn.eventStmt {
		return c.fn.exprAt.ref, true
	}

	if c.fn.eventStmt == nil {
		return liveness.StmtRef{}, false
	}

	ref, ok := c.fn.stmtToRef[c.fn.eventStmt]
	return ref, ok
}

// attachEventsAt makes eventStmtRef return ref until the returned function restores the
// previous point. A statement with a CFG point inside the expression being inferred
// attaches its events to its own point while it is walked.
func (c *checker) attachEventsAt(ref liveness.StmtRef) func() {
	fn := c.fn
	saved := fn.exprAt
	fn.exprAt.stmt, fn.exprAt.ref, fn.exprAt.set = fn.eventStmt, ref, true
	return func() { fn.exprAt = saved }
}

// emitRead emits a read of the place e names, attached to the CFG point eventStmtRef
// returns. An expression that names no local place emits nothing.
func (c *checker) emitRead(e ast.Expr) {
	if c.eventTable() == nil {
		return
	}

	if ref, ok := c.eventStmtRef(); ok {
		c.emitReadAt(e, ref)
	}
}

// emitReadAt emits a read of the place e names, attached to ref. A read of e already
// emitted at ref emits nothing more.
func (c *checker) emitReadAt(e ast.Expr, ref liveness.StmtRef) {
	p, ok := c.exprPlace(e)
	if !ok || !c.firstEmit(e, readRole, ref, 0) {
		return
	}

	c.emitEvent(&useEvent{eventBase: eventBase{place: p, node: e}, access: readAccess, fromRead: true}, ref)
}

// emitWrite emits a write of the assignment target e, attached to ref. The receivers the
// target reads to reach the written place are part of the write.
func (c *checker) emitWrite(e ast.Expr, ref liveness.StmtRef) {
	if c.eventTable() == nil {
		return
	}

	c.subsumePlaceChain(e)
	if p, ok := c.exprPlace(e); ok {
		c.emitEvent(&useEvent{eventBase: eventBase{place: p, node: e}, access: writeAccess}, ref)
	}
}

// placeHolderOf returns the holder for the place e names. When e names no local place, it
// returns a holder whose tempTarget is e.
func (c *checker) placeHolderOf(e ast.Expr) eventHolder {
	if p, ok := c.exprPlace(e); ok {
		return eventHolder{target: placeTarget{root: p.root}, path: p.path}
	}
	return eventHolder{target: tempTarget{node: e}}
}

// emitFlow emits the events of the value source passes to holder, a destination
// described by dest, attached to ref:
//
//   - A place copies, borrows, or moves by F2, or records a provisional flow when F3 has
//     not settled the source or the destination.
//   - A written borrow, a literal, or a closure already emitted its borrow, elements, or
//     captures with itself as their holder, and those events move to holder. So did a call
//     whose result holds a borrow of an argument, as emitCallEnd describes, and a read of
//     that result passes them on too.
//   - A branching expression passes the value of each branch's tail.
//
// Any other source names no place and emits nothing here.
func (c *checker) emitFlow(source ast.Expr, holder eventHolder, dest flowDest, ref liveness.StmtRef) {
	if c.eventTable() == nil || source == nil {
		return
	}

	if results, branches := liveness.BranchResults(source); branches {
		for _, result := range results {
			// A branch whose result is an expression statement attaches to that statement.
			branchRef := ref
			if r, ok := c.fn.exprRefs[result]; ok {
				branchRef = r
			}

			c.emitFlow(result, holder, dest, branchRef)
		}

		return
	}

	switch s := source.(type) {
	case *ast.BorrowExpr:
		c.noteBorrowDest(s, dest)
		c.retargetTemp(s, holder, ref)
		return
	case *ast.ObjectExpr, *ast.TupleExpr, *ast.FuncExpr, *ast.CallExpr:
		c.retargetTemp(s, holder, ref)
		return
	}

	p, ok := c.exprPlace(source)
	if !ok {
		// A read of a call's result, as in `pick(&q).inner` or `await load(&q)`, holds what the
		// result borrows, unless the value read is a value type.
		if call, isCall := callResultOf(source); isCall && !c.holdsValueType(c.info.TypeOf(source)) {
			c.retargetTemp(call, holder, ref)
		}
		return
	}

	c.emitValueFlow(p, source, nil, source, holder, dest, ref)
}

// callResultOf returns the call whose result e reads: an `await` of the call, or a member or
// index chain rooted at it. ok is false for any other expression.
func callResultOf(e ast.Expr) (*ast.CallExpr, bool) {
	for {
		switch x := e.(type) {
		case *ast.CallExpr:
			return x, true
		case *ast.AwaitExpr:
			e = x.Arg
		case *ast.MemberExpr:
			e = x.Object
		case *ast.IndexExpr:
			e = x.Object
		default:
			return nil, false
		}
	}
}

// emitBorrow emits the borrow a written `&` or `&mut` expression takes of its operand's
// place, attached to the CFG point eventStmtRef returns. The borrow expression holds the borrow
// until a flow site retargets it. finishOwnershipEvents fills in the borrow's lifetime
// once the expression's type is recorded.
func (c *checker) emitBorrow(e *ast.BorrowExpr) {
	if c.eventTable() == nil {
		return
	}

	ref, ok := c.eventStmtRef()
	if !ok {
		return
	}

	p, ok := c.exprPlace(e.Arg)
	if !ok || !c.firstEmit(e, borrowRole, ref, 0) {
		return
	}

	c.subsumePlaceChain(e.Arg)
	c.emitEvent(&borrowEvent{
		flowBase: flowBase{
			eventBase: eventBase{place: p, node: e},
			holder:    eventHolder{target: tempTarget{node: e}},
		},
		borrow: borrowKindOf(e.Mut),
	}, ref)
}

// holdLiteralBorrows makes the written borrow e the holder of each borrow its operand holds
// when the operand is an object or tuple literal, as in `&{inner: &mut b}`. A flow site that
// receives e then receives them too. It must run after the operand is inferred.
func (c *checker) holdLiteralBorrows(e *ast.BorrowExpr) {
	switch e.Arg.(type) {
	case *ast.ObjectExpr, *ast.TupleExpr:
	default:
		return
	}

	if ref, ok := c.eventStmtRef(); ok && c.firstEmit(e, borrowRole, ref, 0) {
		c.retargetTemp(e.Arg, eventHolder{target: tempTarget{node: e}}, ref)
	}
}

// noteBorrowDest records dest as the destination of the borrow the written borrow e takes.
func (c *checker) noteBorrowDest(e *ast.BorrowExpr, dest flowDest) {
	t := c.fn.events
	for _, i := range t.temps[e] {
		if bev, ok := t.events[i].(*borrowEvent); ok && bev.node == e {
			bev.dest = dest
		}
	}
}

// finishOwnershipEvents completes the current body's table once its body is walked. Each
// written borrow takes the lifetime of its expression's type, then the destination
// borrowFromDest gives it. Then the table records each parameter whose referent the caller
// owns, as paramReferentOutlivesFrame decides it. Last, each closure capture settles, since
// the body is walked and every place that holds a closure is known.
func (c *checker) finishOwnershipEvents() {
	t := c.eventTable()
	if t == nil {
		return
	}

	for _, ev := range t.events {
		bev, ok := ev.(*borrowEvent)
		if !ok {
			continue
		}

		be, ok := bev.node.(*ast.BorrowExpr)
		if !ok {
			continue
		}

		if r, isRef := c.info.TypeOf(be).(*soltype.RefType); isRef {
			bev.lifetime = r.Lt
		}
		c.borrowFromDest(t, bev, false)
	}

	for _, id := range c.fn.paramVarIDs.ToSlice() {
		if c.paramReferentOutlivesFrame(id) {
			t.callerOwned.Add(id)
		}
	}

	c.settleFlowsWaitingOn(flowWait{capture: true, table: t}, false, true)
}

// borrowFromDest gives the written borrow bev the mutability and lifetime of the borrow
// destination it flows into, once that destination is settled. A `&` destination narrows a
// `&mut` borrow to `&`, the way a `&` parameter gives the callee a read-only view. A
// `'static` destination makes the borrow `'static`. Any other destination leaves bev as
// written. table is the event table that holds bev. force settles a destination F3 has not
// settled, the way classifyFlow's force flag describes. bev keeps a destination that is not
// settled, so a later call with force can apply it.
func (c *checker) borrowFromDest(table *ownershipEvents, bev *borrowEvent, force bool) {
	if bev.dest == nil {
		return
	}

	dest, _, settled := c.settleDest(table, bev.dest, bev.holder, force, force)
	if !settled {
		return
	}

	bev.dest = nil
	if b, toBorrow := dest.(*borrowDest); toBorrow {
		bev.borrow = b.narrow(bev.borrow)
		if soltype.IsStaticLifetime(b.lt) {
			bev.lifetime = b.lt
		}
	}
}

// emitTempFlow emits the flow of source into the element at path of the literal or
// closure value, which holds it until a flow site retargets it.
func (c *checker) emitTempFlow(source ast.Expr, value ast.Expr, path []placeSeg, dest flowDest, ref liveness.StmtRef) {
	if !c.firstEmit(source, elementRole, ref, 0) {
		return
	}

	holder := eventHolder{target: tempTarget{node: value}}.extend(path)
	c.emitFlow(source, holder, dest, ref)
}

// retargetTemp moves every event held by the temporary value to holder, keeping the
// path within the value. It also records each place whose value flowed into the temporary
// value as flowing into holder at the CFG point ref.
func (c *checker) retargetTemp(value ast.Node, holder eventHolder, ref liveness.StmtRef) {
	t := c.fn.events
	roots := t.tempRoots[value]
	delete(t.tempRoots, value)

	for _, root := range roots {
		t.noteFlowInto(root, holder, value, ref)
	}

	indices := t.temps[value]
	delete(t.temps, value)

	for _, i := range indices {
		// A provisional flow that settled into a use has no holder left to retarget.
		fe, ok := t.events[i].(flowEvent)
		if !ok {
			continue
		}

		fb := fe.flow()
		fb.holder = holder.extend(fb.holder.path)

		if th, ok := holder.target.(tempTarget); ok {
			t.temps[th.node] = append(t.temps[th.node], i)
		}
	}
}

// emitValueFlow emits the flow of the value at place p into holder, attached to ref. node
// is the expression to blame. source is the expression whose type the flow reads, or nil
// when sourceT holds that type. A flow F3 has not settled appends a provisional
// placeholder and waits in c.pendingFlows.
//
// A value type creates no holder of a borrow, so its flow is only a read of p. A source
// expression already emitted that read, and the flow emits nothing more. A flow with no
// source expression emits the read itself.
func (c *checker) emitValueFlow(p movePlace, source ast.Expr, sourceT soltype.Type, node ast.Node, holder eventHolder, dest flowDest, ref liveness.StmtRef) {
	if source != nil {
		sourceT = c.info.TypeOf(source)
	}

	// The escape check of a closure capture reads this record. A closure is a value type,
	// so it is recorded before the value-type case below returns.
	c.fn.events.noteFlowInto(p.root, holder, node, ref)

	flow := flowBase{eventBase: eventBase{place: p, node: node}, holder: holder}
	ev, wait, settled := c.classifyFlow(c.fn.events, flow, sourceT, dest, false, false)
	if use, ok := ev.(*useEvent); settled && ok {
		if source == nil {
			c.emitEvent(use, ref)
		}
		return
	}

	// The flow's event reads source, so the use the read emitted is dropped.
	if source != nil {
		c.subsumePlaceChain(source)
	}

	if !settled {
		ev = &provisionalEvent{flowBase: flow}
	}

	index := c.emitEvent(ev, ref)
	if !settled && index >= 0 {
		pf := provisionalFlow{table: c.fn.events, index: index, source: source, sourceT: sourceT, dest: dest}
		c.addPendingFlow(pf, wait)
	}
}

// emitCaptureFlows emits the capture of the local root, of type t, by closure, attached to
// ref. decl is the declaration of root's binding. writes marks a closure that writes the
// local. moveNode is the expression in the closure's body that moves the local, or nil when
// the body does not move it. The closure holds each capture until a flow site receives it.
//
// The closure captures each place capturedPaths finds under root, as settleDest decides for
// a capture. A closure that moves the local owns the place capturedMove returns instead,
// wherever the closure flows. In
//
//	val f = fn () { val q = p.a; return q.x }
//
// the closure moves `p.a` and leaves `p.b` usable.
func (c *checker) emitCaptureFlows(closure ast.Func, root liveness.VarID, decl ast.Node, t soltype.Type, writes bool, moveNode ast.Node, ref liveness.StmtRef) {
	if !c.firstEmit(closure, captureRole, ref, root) {
		return
	}

	// An unannotated parameter's binding renders as `never` until its body settles it, so
	// the capture reads the parameter's own type instead.
	if fp, ok := c.paramByVar[root]; ok {
		t = fp.Type
	}

	paths := c.capturedPaths(closure, decl)
	if moveNode != nil {
		paths = []capturedPath{c.capturedMove(paths, moveNode, decl)}
	}

	holder := eventHolder{target: tempTarget{node: closure}}
	for _, cp := range paths {
		var dest flowDest = &captureDest{writes: writes}
		if cp.moves {
			dest = &ownedDest{}
		}

		pathT := cp.t
		if len(cp.path) == 0 {
			pathT = t
		}

		c.emitValueFlow(movePlace{root: root, path: cp.path}, nil, pathT, closure, holder, dest, ref)
	}
}

// capturedPath is one place under a captured local that a closure captures. path is the
// field path under the local, and t is the type of the value at path. moves marks the place
// the closure's body moves.
type capturedPath struct {
	path  []placeSeg
	t     soltype.Type
	moves bool
}

// capturedPaths returns the places under the local declared by decl that closure's body
// reads or writes, in the order the body first reaches them. No two of the places overlap,
// since a place under another place returned is left out. The one place returned is the
// whole local when the body uses the local in a way capturedChain names no field for, or
// holds a nested function or JSX, whose uses of the local the walk does not name.
func (c *checker) capturedPaths(closure ast.Func, decl ast.Node) []capturedPath {
	t := c.fn.events
	f, ok := t.capturedPlaces[closure]
	if !ok {
		f = &capturedPlaceFinder{c: c, paths: map[ast.Node][]capturedPath{}, whole: set.NewSet[ast.Node]()}
		if body := closure.FuncBody(); body != nil {
			body.Accept(f)
		}
		t.capturedPlaces[closure] = f
	}

	found := f.paths[decl]
	if f.allWhole || f.whole.Contains(decl) || len(found) == 0 {
		return []capturedPath{{}}
	}

	var out []capturedPath
	for i, cp := range found {
		covered := slices.ContainsFunc(found, func(other capturedPath) bool {
			return len(other.path) < len(cp.path) && pathHasPrefix(cp.path, other.path)
		})
		repeated := slices.ContainsFunc(found[:i], func(other capturedPath) bool {
			return slices.Equal(other.path, cp.path)
		})
		if !covered && !repeated {
			out = append(out, cp)
		}
	}
	return out
}

// capturedMove returns the place, marked as moved, that a closure owns when its body moves
// the local declared by decl. paths are the places capturedPaths found under the local, and
// moveNode is the expression in the body that moves it. The place moveNode names is returned
// when it covers every place in paths. Otherwise the whole local is returned, since the body
// may move more than the one place moveNode names.
func (c *checker) capturedMove(paths []capturedPath, moveNode ast.Node, decl ast.Node) capturedPath {
	whole := capturedPath{moves: true}

	e, ok := moveNode.(ast.Expr)
	if !ok {
		return whole
	}

	cp, ok := c.capturedChain(e)
	if !ok || cp.decl != decl || cp.whole || cp.closed || len(cp.segs) == 0 {
		return whole
	}

	for _, p := range paths {
		if !pathHasPrefix(p.path, cp.segs) {
			return whole
		}
	}

	return capturedPath{path: cp.segs, t: c.info.TypeOf(cp.exprs[len(cp.exprs)-1]), moves: true}
}

// capturedPlaceFinder walks a closure's body for capturedPaths. paths maps the declaration of
// each local the body reaches through a field to the places it reaches. whole holds each
// declaration of a local the body uses whole. allWhole is set when the body holds a nested
// function or JSX, whose uses of a local the walk does not name.
type capturedPlaceFinder struct {
	ast.DefaultVisitor
	c        *checker
	paths    map[ast.Node][]capturedPath
	whole    set.Set[ast.Node]
	allWhole bool
}

func (v *capturedPlaceFinder) EnterExpr(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.FuncExpr, *ast.JSXElementExpr, *ast.JSXFragmentExpr:
		v.allWhole = true
		return false
	case *ast.IdentExpr, *ast.MemberExpr, *ast.IndexExpr:
		return v.recordChain(e)
	}

	return !v.allWhole
}

// EnterObjExprElem records the key of a shorthand property such as `{p}`, which reads the
// local p whole. ast.PropertyExpr does not visit that key as an expression.
func (v *capturedPlaceFinder) EnterObjExprElem(elem ast.ObjExprElem) bool {
	if prop, ok := elem.(*ast.PropertyExpr); ok && prop.Value == nil {
		if key, isIdent := prop.Name.(*ast.IdentExpr); isIdent {
			v.recordChain(key)
		}
	}
	return !v.allWhole
}

func (v *capturedPlaceFinder) EnterDecl(d ast.Decl) bool {
	if _, isFunc := d.(*ast.FuncDecl); isFunc {
		v.allWhole = true
		return false
	}
	return !v.allWhole
}

// recordChain records the place the identifier, member, or index chain e names, and
// reports whether the walk descends into e. A chain that names a field path stops the walk,
// since each link of it is part of the place. The walk still visits each index key of such a
// chain that is not a literal, as the `k.i` of `m[k.i]` with `k.i: 0` is, since the key
// reads another local. The walk descends into any other chain, whose dynamic index can read
// another local, as the `k.s` of `m[k.s]` does.
func (v *capturedPlaceFinder) recordChain(e ast.Expr) bool {
	cp, ok := v.c.capturedChain(e)
	if !ok {
		// A chain not rooted at a local can still read one in a dynamic index.
		return !v.allWhole
	}

	segs := cp.segs
	// A value type copies, so the closure reaches it through the nearest field above it
	// that holds a reference. A closure reading `p.x` with `x: number` sees later writes
	// through `p`.
	for len(segs) > 0 && v.c.holdsValueType(v.c.info.TypeOf(cp.exprs[len(segs)-1])) {
		segs = segs[:len(segs)-1]
	}

	if cp.whole || len(segs) == 0 {
		v.whole.Add(cp.decl)
	} else {
		v.paths[cp.decl] = append(v.paths[cp.decl], capturedPath{path: segs, t: v.c.info.TypeOf(cp.exprs[len(segs)-1])})
	}

	descend := (cp.whole || cp.closed) && !v.allWhole
	if !descend {
		for _, key := range cp.keys {
			key.Accept(v)
		}
	}
	return descend
}

// chainPlace is the place an identifier, member, or index chain names under a local. decl is
// the local's declaration, segs the field path, and exprs the expression of each segment of
// segs, outermost last. whole marks a chain that names the local but no field of it, such as
// one with a dynamic index. closed marks a chain whose next link reads something other than
// a property, such as a getter or a method, so segs ends before that link. keys lists each
// index key in segs that is an expression other than a literal, such as the `i` of `xs[i]`
// with `i: 0`.
type chainPlace struct {
	decl   ast.Node
	segs   []placeSeg
	exprs  []ast.Expr
	whole  bool
	closed bool
	keys   []ast.Expr
}

// capturedChain returns the place the chain e names. ok is false when e is not a chain
// rooted at an identifier whose declaration the walk recorded.
func (c *checker) capturedChain(e ast.Expr) (chainPlace, bool) {
	var object ast.Expr
	var name string
	// element marks an index that names one element of a receiver read by position, as
	// `xs[0]` and `xs["0"]` do off an array. The element is part of the place though the
	// receiver's type declares no property of that name.
	element := false
	switch e := e.(type) {
	case *ast.IdentExpr:
		decl, ok := c.info.DeclOf(e)
		return chainPlace{decl: decl}, ok
	case *ast.MemberExpr:
		object = e.Object
		if e.Prop != nil {
			name = e.Prop.Name
		}
	case *ast.IndexExpr:
		object = e.Object
		name, _ = c.indexSegment(e.Index)
		_, isIndex := canonicalIndex(name)
		recv := c.info.TypeOf(e.Object)
		element = isIndex && recv != nil && c.readsByPosition(recv)
	default:
		return chainPlace{}, false
	}

	cp, ok := c.capturedChain(object)
	switch {
	case !ok || cp.whole || cp.closed:
	case name == "":
		cp.whole = true
	case !element && !c.readsAsField(object, name):
		cp.closed = true
	default:
		cp.segs = appendPath(cp.segs, []placeSeg{{kind: namedSeg, name: name}})
		cp.exprs = append(slices.Clone(cp.exprs), e)
		if ix, isIndex := e.(*ast.IndexExpr); isIndex {
			if _, isLit := ix.Index.(*ast.LiteralExpr); !isLit {
				cp.keys = append(slices.Clone(cp.keys), ix.Index)
			}
		}
	}
	return cp, ok
}

// readsAsField reports whether a read of name off object reads a property the type of
// object declares. The member is looked up the way a read looks it up, and then the way a
// write does, which also finds an owned value through the variable a binding's read types
// as.
func (c *checker) readsAsField(object ast.Expr, name string) bool {
	recv := c.info.TypeOf(object)
	if recv == nil {
		return false
	}

	member, found := c.readMember(name, readCarrier(recv))
	if !found {
		member, _ = c.fieldWriteMember(name, recv)
	}
	_, isProp := member.(*soltype.PropertyElem)
	return isProp
}

// emitBindingFlow emits the flow of a `val` or `var` initializer into the binding whose
// VarID is id, attached to ref. A binding with an annotation takes the annotation's
// ownership. One without is owned, as F3 states.
func (c *checker) emitBindingFlow(vd *ast.VarDecl, id liveness.VarID, bindingT soltype.Type, ref liveness.StmtRef) {
	if id <= 0 || !c.firstEmit(vd, bindingRole, ref, 0) {
		return
	}

	var dest flowDest = &ownedDest{}
	if vd.TypeAnn != nil {
		dest = c.destOfType(bindingT)
	}

	c.emitFlow(vd.Init, eventHolder{target: placeTarget{root: id}}, dest, ref)
}

// emitPatternFlows emits the flow of each part pat binds out of the value of source,
// attached to ref. pat is the pattern of a destructuring `val`, a `match` arm, or an
// `if val`. Only a source that names a place, or a written borrow of one, passes parts of a
// place. A pattern that holds an element the binding walk rejected emits nothing. A part
// read through a borrow is borrowed again, since F4 makes a pattern over a borrow bind
// borrows. A tuple or extractor element flows out of the place extended by its position, as
// patternPlaceWalk.elements describes.
func (c *checker) emitPatternFlows(pat ast.Pat, source ast.Expr, ref liveness.StmtRef) {
	if source == nil || !c.firstEmit(pat, patternRole, ref, 0) {
		return
	}

	// The binding walk gave a rejected element no place to bind, so the pattern passes
	// nothing out of source, and source stays a plain read.
	if c.holdsRejectedPatElem(pat) {
		return
	}

	w := &patternEmitter{c: c, source: source, sourceT: c.info.TypeOf(source), ref: ref}
	w.sourceBorrow, w.sourceIsBorrow = c.borrowOfType(w.sourceT)

	// A pattern over a written borrow, as in `val {f: d} = &p`, reads the parts of the
	// borrowed place. The source's type is the borrow, so each leaf borrows its part of `p`.
	placeExpr := source
	if be, isBorrow := source.(*ast.BorrowExpr); isBorrow && w.sourceIsBorrow {
		placeExpr = be.Arg
	}

	src, ok := c.exprPlace(placeExpr)
	if !ok {
		return
	}
	walk := patternPlaceWalk{
		leaf:          func(node ast.Node, at movePlace) { w.leaf(node, at, nil, c.info.TypeOf(node)) },
		objectRest:    w.objectRest,
		elementPlaces: true,
		tuple: func(at movePlace, parts []placeSeg) {
			// Only a pattern over the source itself has a known tuple type to check against.
			covers := len(at.path) == len(src.path) && c.coversTuple(w.sourceT, parts)
			key := placeKey(at)
			prev, seen := c.fn.events.tupleParts[key]
			switch {
			case !covers || (seen && !slices.Equal(prev, parts)):
				c.fn.events.tupleParts[key] = nil
			case !seen:
				c.fn.events.tupleParts[key] = parts
			}
		},
	}
	walk.walk(pat, src)

	// A pattern that binds nothing, such as a literal arm, leaves the source's read alone.
	if w.bound {
		c.subsumePlaceChain(source)
	}
}

// holdsRejectedPatElem reports whether pat, or any pattern, element, or literal nested in
// it, is in c.rejectedPatElems.
func (c *checker) holdsRejectedPatElem(pat ast.Pat) bool {
	if c.rejectedPatElems == nil {
		return false
	}

	v := &rejectedPatFinder{rejected: c.rejectedPatElems}
	pat.Accept(v)
	return v.found
}

// rejectedPatFinder walks a pattern for holdsRejectedPatElem and sets found once it reaches
// a node in rejected. An object pattern's elements are not patterns, so EnterPat checks
// each of them on the object pattern's visit.
type rejectedPatFinder struct {
	ast.DefaultVisitor
	rejected set.Set[ast.Node]
	found    bool
}

func (v *rejectedPatFinder) EnterPat(p ast.Pat) bool {
	if v.rejected.Contains(p) {
		v.found = true
	}

	if obj, ok := p.(*ast.ObjectPat); ok {
		for _, elem := range obj.Elems {
			if v.rejected.Contains(elem) {
				v.found = true
			}
		}
	}

	return !v.found
}

func (v *rejectedPatFinder) EnterLit(l ast.Lit) bool {
	if v.rejected.Contains(l) {
		v.found = true
	}
	return !v.found
}

// coversTuple reports whether a tuple pattern whose elements are parts names every element
// of a value of type t. It is false unless t is a tuple type of fixed length. A pattern with
// no rest element covers the tuple when it has one part per element. A pattern with a rest
// element at position i covers it when the tuple has at least i elements.
func (c *checker) coversTuple(t soltype.Type, parts []placeSeg) bool {
	tuple, ok := c.expandAliasChain(t).(*soltype.TupleType)
	if !ok || tuple.Inexact || len(parts) == 0 {
		return false
	}
	for _, elem := range tuple.Elems {
		if _, spread := elem.(*soltype.RestSpreadType); spread {
			return false
		}
	}
	if last := parts[len(parts)-1].name; strings.HasSuffix(last, "...") {
		return len(tuple.Elems) >= len(parts)-1
	}
	return len(tuple.Elems) == len(parts)
}

// branchEntryRef returns the CFG point on entry to branch i of the `match`, `if val`, or
// `val … else` whose target is target, where the branch's pattern binds its names. The CFG
// builder runs the target in a statement of its own and adds an edge from that statement's
// block to each branch's block in order. A `match` adds its arms in source order, an
// `if val` adds its consequent first, and a `val … else` adds its `else` block first. The
// point is StmtIdx -1 of that branch's block. A branch the CFG builder gave no block of its own, such
// as an arm of a `match` in a `return`, runs at the point of the expression holding it,
// which siteRef names when hasSite is set. ok is false when neither point is known.
func (c *checker) branchEntryRef(target ast.Expr, i int, siteRef liveness.StmtRef, hasSite bool) (liveness.StmtRef, bool) {
	if c.eventTable() == nil {
		return liveness.StmtRef{}, false
	}

	if ref, ok := c.fn.exprRefs[target]; ok {
		if succ := c.fn.cfg.Blocks[ref.BlockID].Successors; i < len(succ) {
			return liveness.StmtRef{BlockID: succ[i].ID, StmtIdx: -1}, true
		}
	}

	return siteRef, hasSite
}

// patternEmitter emits the flows of the leaves a patternPlaceWalk reaches for
// emitPatternFlows. sourceT is the type of the source expression source. bound records
// whether the pattern binds any leaf.
type patternEmitter struct {
	c       *checker
	source  ast.Expr
	sourceT soltype.Type
	// sourceBorrow is the borrow sourceT is, when sourceIsBorrow is set.
	sourceBorrow   typeBorrow
	sourceIsBorrow bool
	ref            liveness.StmtRef
	bound          bool
}

// objectRest emits the flow of each part the object rest binding ident takes out of the
// place `at`, as forEachRestField names them for the binding's type. The fields the
// pattern names elsewhere flow only into their own leaves.
func (w *patternEmitter) objectRest(ident *ast.IdentPat, at movePlace) {
	forEachRestField(w.c.info.TypeOf(ident), at, func(src movePlace, destPath []placeSeg, partT soltype.Type) {
		w.leaf(ident, src, destPath, partT)
	})
}

// leaf emits the flow of the place `at` into the binding the pattern leaf node binds, at
// path within it. leafT is the type of the part the binding receives. A value-type leaf is
// a read. Any other leaf is borrowed when the source is a borrow, and takes its own type's
// ownership otherwise.
func (w *patternEmitter) leaf(node ast.Node, at movePlace, path []placeSeg, leafT soltype.Type) {
	id := w.c.varIDs.Of(node)
	if id <= 0 || leafT == nil {
		return
	}

	w.bound = true
	leafT = w.c.borrowBehindVar(leafT)
	sourceT := leafT
	dest := w.c.destOfType(leafT)
	if w.sourceIsBorrow && !w.c.holdsValueType(leafT) {
		// The leaf binds a borrow with the source's mutability, as D14 decides.
		sourceT = w.sourceT
		dest = &borrowDest{borrow: borrowKindOf(w.sourceBorrow.mut), lt: w.sourceBorrow.lt, reborrows: true}
	}

	holder := eventHolder{target: placeTarget{root: id}, path: path}
	w.c.emitValueFlow(at, nil, sourceT, w.source, holder, dest, w.ref)
}

// borrowBehindVar returns the borrow type a type variable holds when t is a variable whose
// lower bounds, followed through other variables, are one borrow type and value types such
// as `null`. It returns t for any other type, including a variable that also has an owned
// lower bound. The field `a`
// that `val {a} = o` binds out of `o: {a: &{x: number}}` types as a variable bounded below
// by `&{x: number}`, and this returns `&{x: number}`.
func (c *checker) borrowBehindVar(t soltype.Type) soltype.Type {
	if _, isVar := t.(*soltype.TypeVarType); !isVar {
		return t
	}

	seen := set.NewSet[*soltype.TypeVarType]()
	var found soltype.Type
	var walk func(soltype.Type) bool
	walk = func(t soltype.Type) bool {
		v, isVar := t.(*soltype.TypeVarType)
		if !isVar {
			// A value-type bound such as `null` holds no borrow and leaves the leaf a borrow.
			if c.holdsValueType(t) {
				return true
			}
			if _, ok := c.borrowOfType(t); !ok || (found != nil && !equalType(found, t)) {
				return false
			}
			found = t
			return true
		}

		if seen.Contains(v) {
			return true
		}

		seen.Add(v)
		for _, lb := range v.LowerBounds {
			if !walk(lb) {
				return false
			}
		}
		return true
	}

	if walk(t) && found != nil {
		return found
	}
	return t
}

// emitAssignFlow emits the events of the assignment e, attached to ref: the flow of the
// source into the target, then the write of the target. A
// reassigned binding takes the ownership of its type. A field or element store takes the
// ownership of the field it writes when the receiver declares one, and is owned otherwise.
func (c *checker) emitAssignFlow(scope *Scope, e *ast.BinaryExpr, ref liveness.StmtRef) {
	if !c.firstEmit(e, assignRole, ref, 0) {
		return
	}

	switch left := e.Left.(type) {
	case *ast.IdentExpr:
		b, found := c.lookupValueBinding(scope, left.Name)
		if !found || len(b.Schemes) == 0 {
			return
		}

		dest := c.destOfType(bindingType(b))

		// A module-level binding is a local of a function body that reaches it, but a store
		// into it outlives every local, so its holder is a moduleTarget.
		switch id := c.varIDs.Of(left); {
		case b.ModuleLevel:
			c.emitFlow(e.Right, eventHolder{target: moduleTarget{name: left.Name}}, dest, ref)
		case id > 0:
			c.emitFlow(e.Right, eventHolder{target: placeTarget{root: id}}, dest, ref)
		default:
			return
		}

		c.emitWrite(left, ref)
	case *ast.MemberExpr, *ast.IndexExpr:
		c.emitFlow(e.Right, c.placeHolderOf(left), c.storeDest(left), ref)
		c.emitWrite(left, ref)
	}
}

// storeDest returns the destination a field or element store into target has. A store
// into a field the receiver declares takes the field's ownership, and any other store is
// owned.
func (c *checker) storeDest(target ast.Expr) flowDest {
	var object ast.Expr
	var name string

	switch t := target.(type) {
	case *ast.MemberExpr:
		if t.Prop == nil {
			return &ownedDest{}
		}

		object, name = t.Object, t.Prop.Name
	case *ast.IndexExpr:
		key, ok := constStringKey(t.Index)
		if !ok {
			return &ownedDest{}
		}

		object, name = t.Object, key
	}

	recv := c.info.TypeOf(object)
	if recv == nil {
		return &ownedDest{}
	}

	member, _ := c.fieldWriteMember(name, recv)
	if prop, ok := member.(*soltype.PropertyElem); ok {
		return c.destOfType(prop.Type)
	}
	return &ownedDest{}
}

// noteCallee records that the expression e.Callee is the callee of the call e, so a
// consuming receiver read there flows into the call's receiver. It must run before the
// callee is inferred.
func (c *checker) noteCallee(e *ast.CallExpr) {
	if t := c.eventTable(); t != nil {
		t.callees[e.Callee] = e
	}
}

// emitReceiverFlow emits the flow of the receiver of the method call e into the method's
// `self`, attached to ref. lvl is the level of the call, at which a receiver borrow's
// lifetime is minted. It must run after the callee is inferred and before the arguments
// are, so the receiver's events come before every event an argument emits. A call whose
// receiver emitConsumedReceiver already moved emits nothing more.
func (c *checker) emitReceiverFlow(lvl int, e *ast.CallExpr, ref liveness.StmtRef) {
	recv, self := c.calleeReceiver(e.Callee)
	if recv == nil || self == nil || !c.firstEmit(e, receiverRole, ref, 0) {
		return
	}

	// The method read is how the call reaches the receiver, so the receiver's flow
	// accounts for it.
	c.subsumePlaceChain(e.Callee)

	// self is the method's declared receiver, which every call of the method shares, so
	// its lifetime cannot tell one call's borrow from another's. Each call borrows the
	// receiver under a lifetime of its own.
	dest := &paramDest{param: self, reborrows: true, callLifetime: c.ctx.freshLifetime(lvl)}
	c.emitFlow(recv, eventHolder{target: receiverTarget{call: e}}, dest, ref)
}

// emitConsumedReceiver emits the move of the receiver of access, a member access that
// reaches a consuming `self` or `mut self` member, attached to the CFG point eventStmtRef
// returns. When access is the callee of a call, the call's receiver holds the receiver.
// Otherwise the method value that access reads holds it, as in `val g = c.take`.
func (c *checker) emitConsumedReceiver(access ast.Expr, recv ast.Expr) {
	t := c.eventTable()
	if t == nil {
		return
	}

	ref, ok := c.eventStmtRef()
	if !ok {
		return
	}

	holder := eventHolder{target: tempTarget{node: access}}
	if call, isCallee := t.callees[access]; isCallee {
		if !c.firstEmit(call, receiverRole, ref, 0) {
			return
		}
		holder = eventHolder{target: receiverTarget{call: call}}
	} else if !c.firstEmit(access, receiverRole, ref, 0) {
		return
	}

	// The member read is how the access reaches the receiver, so the move accounts for it.
	c.subsumePlaceChain(access)
	c.emitFlow(recv, holder, &ownedDest{}, ref)
}

// emitArgFlows emits the flow of each argument of the call e into its parameter, attached
// to ref. fn is the signature the arguments were applied to, or nil when the call could
// not see one. An argument with no parameter to receive it, the surplus of a call with too
// many arguments, emits no flow.
func (c *checker) emitArgFlows(e *ast.CallExpr, fn *soltype.FuncType, ref liveness.StmtRef) {
	if !c.firstEmit(e, callRole, ref, 0) {
		return
	}

	for i, arg := range e.Args {
		dest, ok := argDest(e, fn, i)
		if !ok {
			continue
		}

		c.emitFlow(arg, eventHolder{target: argTarget{call: e, index: i}}, dest, ref)
	}
}

// emitCallEnd emits the events of the call e returning, attached to ref, after every event
// of its receiver and arguments. fn is the signature the arguments were applied to, or nil
// when the call could not see one. It emits a passEvent for each lifetime fn shares between
// a slot and a store target or the return type, as callStoreEdges and callReturnEdges find
// them, and then the call's callEvent.
//
// A store target is the place the target argument names, extended by the store's path, so
// `store(&mut a.inner, &mut b)` storing at [peer] passes the borrow of b to `a.inner.peer`. A
// target that names no place, such as an inline literal, takes nothing. A return edge passes
// to a tempTarget of e, which the flow site that receives the result retargets.
func (c *checker) emitCallEnd(e *ast.CallExpr, fn *soltype.FuncType, ref liveness.StmtRef) {
	if !c.firstEmit(e, callEndRole, ref, 0) {
		return
	}

	if fn != nil {
		recv, self := c.calleeReceiver(e.Callee)
		// One signature can share several lifetimes between the same two positions, and each
		// pair passes once. A return edge keys on the root 0, which no local has.
		seen := set.NewSet[passKey]()
		pass := func(param int, to movePlace) {
			for _, from := range argsOfParam(e, fn, param) {
				key := passKey{from: from, to: placeKey(to)}
				if _, ok := storeExprAt(e, recv, from); !ok || seen.Contains(key) {
					continue
				}
				seen.Add(key)

				holder := eventHolder{target: placeTarget{root: to.root}, path: to.path}
				if to.root == 0 {
					holder.target = tempTarget{node: e}
				}
				flow := flowBase{eventBase: eventBase{node: e}, holder: holder}
				c.emitEvent(&passEvent{flowBase: flow, from: callSlot{call: e, index: from}}, ref)
			}
		}

		for _, edge := range callStoreEdges(c.ctx, fn, self) {
			for _, i := range argsOfParam(e, fn, edge.target) {
				targetExpr, ok := storeExprAt(e, recv, i)
				if !ok {
					continue
				}

				target, isPlace := c.exprPlace(borrowOperand(targetExpr))
				if !isPlace || target.root <= 0 {
					continue
				}

				pass(edge.arg, movePlace{root: target.root, path: appendPath(target.path, edge.path)})
			}
		}

		for _, edge := range callReturnEdges(c.ctx, fn, self) {
			pass(edge.arg, movePlace{path: edge.path})
		}
	}

	c.emitEvent(&callEvent{eventBase: eventBase{node: e}, call: e}, ref)
}

// argsOfParam returns the positions of the arguments of the call e that the parameter at
// position param of fn receives. A rest parameter receives every argument from its own
// position on, and selfIndex names the receiver.
func argsOfParam(e *ast.CallExpr, fn *soltype.FuncType, param int) []int {
	if param < 0 || param >= len(fn.Params) || !fn.Params[param].Rest {
		return []int{param}
	}

	var out []int
	for i := param; i < len(e.Args); i++ {
		out = append(out, i)
	}
	return out
}

// passKey names one passEvent of a call for emitCallEnd: the slot it passes from, and the
// placeKey of the place it passes to.
type passKey struct {
	from int
	to   string
}

// argDest returns the destination argument i of the call e has. A rest parameter receives
// every argument from its position on. ok is false for an argument no parameter receives.
func argDest(e *ast.CallExpr, fn *soltype.FuncType, i int) (flowDest, bool) {
	if fn == nil {
		return &calleeDest{call: e, index: i}, true
	}

	fp, ok := paramForArg(fn, i)
	if !ok {
		return nil, false
	}

	return &paramDest{param: fp}, true
}

// emitReturnFlow emits the flow of the returned expression into the function's return,
// attached to ref. An annotated return takes the annotation's ownership. One without an
// annotation is an inferredReturnDest. An annotation that did not resolve is owned.
func (c *checker) emitReturnFlow(expr ast.Expr, ref liveness.StmtRef) {
	if !c.firstEmit(expr, returnRole, ref, 0) {
		return
	}

	var dest flowDest = &ownedDest{}
	switch {
	case c.fn.returnAnn != nil:
		dest = c.destOfType(c.fn.returnAnn)
	case !c.fn.returnAnnotated:
		dest = &inferredReturnDest{}
	}

	c.emitFlow(expr, eventHolder{target: returnTarget{}}, dest, ref)
}
