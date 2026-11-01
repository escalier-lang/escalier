package solver

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
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
func (c *checker) startOwnershipEvents(names map[liveness.VarID]string) {
	t := newOwnershipEvents(names)
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

	for e != nil {
		t.subsumed.Add(e)
		switch x := e.(type) {
		case *ast.MemberExpr:
			e = x.Object
		case *ast.IndexExpr:
			e = x.Object
		default:
			return
		}
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
//     captures with itself as their holder, and those events move to holder.
//   - A branching expression passes the value of each branch's tail.
//
// Any other source, such as a call result, names no place and emits nothing here.
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
	case *ast.BorrowExpr, *ast.ObjectExpr, *ast.TupleExpr, *ast.FuncExpr:
		c.retargetTemp(s, holder)
		return
	}

	p, ok := c.exprPlace(source)
	if !ok {
		return
	}

	c.emitValueFlow(p, source, nil, source, holder, dest, ref)
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

// finishOwnershipEvents completes the current body's table once its body is walked. Each
// written borrow takes the lifetime of its expression's type. Then each closure capture
// settles, since the body is walked and every place that holds a closure is known.
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
	}

	c.settleFlowsWaitingOn(flowWait{capture: true, table: t}, false, true)
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
// value as flowing into holder.
func (c *checker) retargetTemp(value ast.Expr, holder eventHolder) {
	t := c.fn.events
	roots := t.tempRoots[value]
	delete(t.tempRoots, value)

	for _, root := range roots {
		t.noteFlowInto(root, holder)
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
	c.fn.events.noteFlowInto(p.root, holder)

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

// emitCaptureFlow emits the capture of the local root, of type t, by closure, attached to
// ref. The closure holds the capture until a flow site receives it. writes marks a closure
// that writes the local. moves marks a closure whose body moves the local, which owns it
// wherever the closure flows.
func (c *checker) emitCaptureFlow(closure ast.Func, root liveness.VarID, t soltype.Type, writes, moves bool, ref liveness.StmtRef) {
	if !c.firstEmit(closure, captureRole, ref, root) {
		return
	}

	// An unannotated parameter's binding renders as `never` until its body settles it, so
	// the capture reads the parameter's own type instead.
	if fp, ok := c.paramByVar[root]; ok {
		t = fp.Type
	}

	holder := eventHolder{target: tempTarget{node: closure}}
	var dest flowDest = &captureDest{writes: writes}
	if moves {
		dest = &ownedDest{}
	}

	c.emitValueFlow(movePlace{root: root}, nil, t, closure, holder, dest, ref)
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
// `if val`. Only a source that names a place passes parts of a place. A part read through a
// borrow is borrowed again, since F4 makes a pattern over a borrow bind borrows. A tuple
// or extractor element flows out of the place extended by its position, as elements
// describes.
func (c *checker) emitPatternFlows(pat ast.Pat, source ast.Expr, ref liveness.StmtRef) {
	if source == nil || !c.firstEmit(pat, patternRole, ref, 0) {
		return
	}

	src, ok := c.exprPlace(source)
	if !ok {
		return
	}

	w := &patternEmitter{c: c, source: source, sourceT: c.info.TypeOf(source), ref: ref}
	w.walk(pat, src)

	// A pattern that binds nothing, such as a literal arm, leaves the source's read alone.
	if w.bound {
		c.subsumePlaceChain(source)
	}
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

// patternEmitter walks a pattern for emitPatternFlows. sourceT is the type of the source
// expression source. bound records whether the pattern binds any leaf.
type patternEmitter struct {
	c       *checker
	source  ast.Expr
	sourceT soltype.Type
	ref     liveness.StmtRef
	bound   bool
}

// walk emits the flow of each part pat binds out of the place `at`.
func (w *patternEmitter) walk(pat ast.Pat, at movePlace) {
	switch pat := pat.(type) {
	case *ast.IdentPat:
		w.leaf(pat, at, nil)
	case *ast.ObjectPat:
		for _, elem := range pat.Elems {
			switch e := elem.(type) {
			case *ast.ObjShorthandPat:
				w.leaf(e, extendPlace(at, e.Key.Name), nil)
			case *ast.ObjKeyValuePat:
				w.walk(e.Value, extendPlace(at, e.Key.Name))
			case *ast.ObjRestPat:
				w.objectRest(e.Pattern, at)
			}
		}
	case *ast.InstancePat:
		w.walk(pat.Object, at)
	case *ast.TuplePat:
		w.elements(pat.Elems, at)
	case *ast.ExtractorPat:
		w.elements(pat.Args, at)
	case *ast.RestPat:
		w.walk(pat.Pattern, at)
	}
}

// elements walks the element patterns of a tuple or extractor pattern. Element i takes the
// place `at` extended by the segment `i`. A rest element at position i takes the segment
// `i...`, which stands for every element from i on.
func (w *patternEmitter) elements(elems []ast.Pat, at movePlace) {
	for i, elem := range elems {
		if rest, ok := elem.(*ast.RestPat); ok {
			w.walk(rest.Pattern, extendPlace(at, strconv.Itoa(i)+"..."))
			continue
		}

		w.walk(elem, extendPlace(at, strconv.Itoa(i)))
	}
}

// objectRest walks the rest element `...rest` of an object pattern over the place `at`. A
// rest binding whose type is an object takes each field that type lists, so the fields the
// pattern names flow only into their own leaves. Any other rest binding takes the whole
// place.
func (w *patternEmitter) objectRest(pat ast.Pat, at movePlace) {
	ident, ok := pat.(*ast.IdentPat)
	if !ok {
		w.walk(pat, at)
		return
	}

	obj, isObj := objectThroughBorrow(w.c.info.TypeOf(ident))
	if !isObj {
		w.leaf(ident, at, nil)
		return
	}

	for _, elem := range obj.Elems {
		if prop, isProp := elem.(*soltype.PropertyElem); isProp {
			w.leaf(ident, extendPlace(at, prop.Name), appendSeg(nil, prop.Name))
		}
	}
}

// leaf emits the flow of the place `at` into the binding the pattern leaf node binds, at
// path within it. The leaf's type is the one the pattern walk recorded for node, or that
// type's field when path names one. A value-type leaf is a read. Any
// other leaf is borrowed when the source is a borrow, and takes its own type's ownership
// otherwise.
func (w *patternEmitter) leaf(node ast.Node, at movePlace, path []placeSeg) {
	id := w.c.varIDs.Of(node)
	leafT := w.c.info.TypeOf(node)
	if id <= 0 || leafT == nil {
		return
	}

	if len(path) > 0 {
		leafT = w.c.fieldType(leafT, path[0].name)
	}

	w.bound = true
	sourceT := leafT
	dest := w.c.destOfType(leafT)
	if r, ok := w.c.borrowOf(w.sourceT); ok && !w.c.holdsValueType(leafT) {
		// The leaf binds a borrow with the source's mutability, as D14 decides.
		sourceT = r
		dest = &borrowDest{borrow: borrowKindOf(r.Mut), lt: r.Lt}
	}

	if b, ok := dest.(*borrowDest); ok {
		b.reborrows = true
	}

	holder := eventHolder{target: placeTarget{root: id}, path: path}
	w.c.emitValueFlow(at, nil, sourceT, w.source, holder, dest, w.ref)
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

	if prop, ok := c.lookupWriteField(name, recv); ok {
		return c.destOfType(prop.Type)
	}
	return &ownedDest{}
}

// lookupWriteField returns the property named name a write through a receiver of type recv
// stores into, and false when the receiver declares none.
func (c *checker) lookupWriteField(name string, recv soltype.Type) (*soltype.PropertyElem, bool) {
	member, _ := c.writeMember(name, readCarrier(recv))
	prop, ok := member.(*soltype.PropertyElem)
	return prop, ok
}

// emitCallFlows emits the flows of the call e, attached to ref: the receiver of a method
// call into the method's `self`, then each argument into its parameter. fn is the
// signature the arguments were applied to, or nil when the call could not see one. An
// argument with no parameter to receive it, the surplus of a call with too many arguments,
// emits no flow. lvl is the level of the call, at which a receiver borrow's lifetime is
// minted.
func (c *checker) emitCallFlows(lvl int, e *ast.CallExpr, fn *soltype.FuncType, ref liveness.StmtRef) {
	if !c.firstEmit(e, callRole, ref, 0) {
		return
	}

	if recv, self := c.calleeReceiver(e.Callee); recv != nil && self != nil {
		// The method read is how the call reaches the receiver, so the receiver's flow
		// accounts for it.
		c.subsumePlaceChain(e.Callee)

		// self is the method's declared receiver, which every call of the method shares, so
		// its lifetime cannot tell one call's borrow from another's. Each call borrows the
		// receiver under a lifetime of its own.
		dest := &paramDest{param: self, reborrows: true, callLifetime: c.ctx.freshLifetime(lvl)}
		c.emitFlow(recv, eventHolder{target: receiverTarget{call: e}}, dest, ref)
	}

	for i, arg := range e.Args {
		dest, ok := argDest(e, fn, i)
		if !ok {
			continue
		}

		c.emitFlow(arg, eventHolder{target: argTarget{call: e, index: i}}, dest, ref)
	}
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
