package solver

import (
	"fmt"
	"maps"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// This file records the closure runs that E4 in planning/ownership_rules/requirements.md
// checks. A call runs a closure when its callee names one. A call may run a closure when an
// argument names one, or when module-level state or a retaining parameter keeps one reachable
// at the call. A call that runs a closure uses each capture of it. It uses a capture the
// closure writes as a mutable path, and one the closure only reads as an immutable path. The
// walk records the runs, and the dataflow pass in ownership_dataflow.go checks E1, E2, and E3
// against them.

// runEvent marks the point of a call at which the call runs the closures its callee and
// arguments name. The dataflow pass also finds there each closure a retainedStore keeps
// reachable. The event's node is the call, and its place names nothing.
type runEvent struct {
	eventBase
	call *ast.CallExpr
	// runs lists the closure the callee names, then each closure an argument names, in
	// argument order.
	runs []closureRun
	// callee is the place of the binding the callee names, when that binding holds a
	// closure whose body moves one of its captures. Each call of such a closure moves it, as
	// D2 decides, so a second call uses a moved binding. `consumes` is false for any other
	// call.
	callee   movePlace
	consumes bool
}

// closureRun is one closure a call runs, or may run.
type closureRun struct {
	// node is the expression that names the closure, which a diagnostic blames.
	node ast.Node
	// via names the closure in a diagnostic. It is a phrase closureVia or retainedVia
	// returns.
	via string
	// `runs` marks a closure the call is sure to run, because the call's callee names it.
	runs bool
	// captures lists each local the closure captures, in VarID order.
	captures []capturedLocal
}

// retainedStore records a value that may carry a closure and stays reachable after its
// statement. The value is stored into a module-level binding, or passed to a parameter whose
// function type carries a retention lifetime.
type retainedStore struct {
	// via names the binding or parameter that keeps the closure. closureVia or retainedVia
	// produces it.
	via string
	// expr is the stored value.
	expr ast.Expr
	// at is the CFG point of the statement that stores expr.
	at liveness.StmtRef
	// call is the call whose parameter keeps the closure, and nil for a store into a
	// module-level binding.
	call ast.Node
	// static marks a closure kept for the rest of the program. Every call after the store may
	// run it.
	static bool
	// holders are the bindings that keep a closure reachable that is not kept for the rest
	// of the program. The holders are these bindings:
	//
	//   - each argument whose parameter type shares the retention lifetime
	//   - the receiver, when the type of `self` shares that lifetime
	//   - each binding the call's result or another holder flows into
	//
	// A call may run the closure only while one of them is live.
	holders []liveness.VarID
	// writes lists the owned locals the closures write, in VarID order.
	// noteRetainedClosureWrites fills it once the body is walked.
	writes []liveness.VarID
}

// emitClosureRuns emits the runEvent of the call e, attached to ref. It must run once the
// arguments are inferred and before their flows are emitted.
func (c *checker) emitClosureRuns(e *ast.CallExpr, ref liveness.StmtRef) {
	if !c.firstEmit(e, runRole, ref, 0) {
		return
	}

	ev := &runEvent{eventBase: eventBase{node: e}, call: e}
	if run, ok := c.closureRunOf(e.Callee, true); ok {
		ev.runs = append(ev.runs, run)
	}
	for _, arg := range e.Args {
		if run, ok := c.closureRunOf(arg, false); ok {
			ev.runs = append(ev.runs, run)
		}
	}

	if ident, ok := e.Callee.(*ast.IdentExpr); ok && c.varIDs.Of(ident) > 0 {
		if decl, found := c.info.DeclOf(ident); found && c.funcMovesCaptures(decl) {
			ev.callee, ev.consumes = movePlace{root: c.varIDs.Of(ident)}, true
		}
	}

	c.emitEvent(ev, ref)
}

// closureRunOf returns the run of the closure expr names, with the locals the closure
// captures. ok is false when expr is not a name, or when the name holds no closure that
// captures a local of the body. runs marks a call that is sure to run the closure.
//
// The closure's captures are the capture edges the name holds in the borrow graph, whether
// the name was bound to the closure or copied from a name that was. A local captured through
// several edges is listed once, as a write when any edge writes it. It covers the field paths
// the edges reach, or the whole local when any edge reaches all of it.
func (c *checker) closureRunOf(expr ast.Expr, runs bool) (closureRun, bool) {
	e, ok := expr.(*ast.IdentExpr)
	if !ok || c.fn == nil || c.varIDs.Of(e) <= 0 {
		return closureRun{}, false
	}

	// Group the name's capture edges by the local each one reaches. With
	// `val f = fn () { p.a = 1; log(p.b) }`, f has two edges into p, a write at `.a` and a
	// read at `.b`. They merge into one capturedLocal for p that is mutable and covers both
	// paths. An edge with an empty path reaches the whole local, which `whole` records.
	captured := map[liveness.VarID]*capturedLocal{}
	whole := set.NewSet[liveness.VarID]()
	for _, edge := range c.fn.eagerBorrowGraph[c.varIDs.Of(e)] {
		if !edge.capture {
			continue
		}
		local, seen := captured[edge.referent]
		if !seen {
			local = &capturedLocal{root: edge.referent}
			captured[edge.referent] = local
		}
		local.mut = local.mut || edge.mut
		switch {
		case len(edge.refPath) == 0:
			whole.Add(edge.referent)
		case !slices.ContainsFunc(local.paths, func(p []placeSeg) bool { return slices.Equal(p, edge.refPath) }):
			local.paths = append(local.paths, edge.refPath)
		}
	}
	if len(captured) == 0 {
		return closureRun{}, false
	}

	run := closureRun{node: e, via: closureVia(e.Name), runs: runs}
	for _, root := range slices.Sorted(maps.Keys(captured)) {
		// A capture of the whole local covers every field path, so the paths are dropped.
		local := *captured[root]
		if whole.Contains(root) {
			local.paths = nil
		}
		run.captures = append(run.captures, local)
	}
	return run, true
}

// noteBoundClosure records closure as one whose capture is not used where it is written.
// One is the initializer of a local name. The captures of `val f = fn () { … }` are used
// at each call of f and each call f is passed to. A script's `fn`
// declaration is used the same way. Another is stored into a module-level binding, whose
// captures are used at each call after the store.
func (c *checker) noteBoundClosure(closure ast.Func) {
	if t := c.eventTable(); t != nil {
		t.boundClosures.Add(closure)
	}
}

// noteRetainedClosureStore adds store to the body's retained stores and returns it. It
// returns nil when the body has no event table.
func (c *checker) noteRetainedClosureStore(store retainedStore) *retainedStore {
	t := c.eventTable()
	if t == nil || t.cfg == nil {
		return nil
	}
	s := &store
	t.retained = append(t.retained, s)
	return s
}

// noteRetainedClosureArgs records each argument of the call e that fills a parameter fn keeps
// past the call. Each one becomes a retainedStore at ref. Such a parameter's type holds a function type
// with a retention lifetime, at its top or inside a union, an object field, or an array. A
// whole argument filling it is taken as kept, so each closure written in an object literal
// counts.
//
// A retention lifetime that is 'static, or bound to outlive 'static, keeps the closure for the
// rest of the program. Any other retention lifetime keeps it as long as a binding that shares
// the lifetime is live. When no binding shares it, the callee can keep the closure no longer
// than the call, and nothing is recorded.
func (c *checker) noteRetainedClosureArgs(e *ast.CallExpr, fn *soltype.FuncType, ref liveness.StmtRef) {
	t := c.eventTable()
	if t == nil {
		return
	}

	recv, self := c.calleeReceiver(e.Callee)
	for i, arg := range e.Args {
		param, ok := paramForArg(fn, i)
		if !ok {
			break
		}
		lts := retentionLifetimes(param.Type)
		if len(lts) == 0 {
			continue
		}
		paramName := ""
		if ip, isIdent := param.Pattern.(*soltype.IdentPat); isIdent {
			paramName = ip.Name
		}
		store := retainedStore{
			via:  retainedVia(paramName, calleeName(e.Callee)),
			expr: arg,
			at:   ref,
			call: e,
		}
		var shared []*soltype.LifetimeVar
		for _, lt := range lts {
			if lifetimeEscapedToStatic(lt) {
				store.static = true
			} else if lv, isVar := lt.(*soltype.LifetimeVar); isVar {
				shared = append(shared, lv)
			}
		}
		returned := false
		if !store.static {
			store.holders = c.retentionHolders(e, fn, recv, self, i, shared)
			returned = typeMentionsLifetime(c.ctx, fn.Ret, shared)
			if len(store.holders) == 0 && !returned {
				continue
			}
		}
		if s := c.noteRetainedClosureStore(store); s != nil && returned {
			t.retainedResults[e] = append(t.retainedResults[e], s)
		}
	}
}

// paramForArg returns the parameter of fn the argument at index i fills. Every argument past a
// trailing rest parameter fills that parameter. ok is false when no parameter takes the argument.
func paramForArg(fn *soltype.FuncType, i int) (*soltype.FuncParam, bool) {
	if i < len(fn.Params) {
		return fn.Params[i], true
	}
	if n := len(fn.Params); n > 0 && fn.Params[n-1].Rest {
		return fn.Params[n-1], true
	}
	return nil, false
}

// retentionHolders returns the bindings that share one of the lifetimes in shared with the
// parameter at index skip of the call e. They are the arguments, and the receiver recv whose
// parameter is self, that name a binding and whose parameter types mention one of those
// lifetimes. For an argument written as a borrow, such as `&mut em`, the holder is the borrowed
// binding, and only when the borrowed type mentions one of those lifetimes.
func (c *checker) retentionHolders(
	e *ast.CallExpr, fn *soltype.FuncType, recv ast.Expr, self *soltype.FuncParam, skip int, shared []*soltype.LifetimeVar,
) []liveness.VarID {
	var holders []liveness.VarID
	add := func(expr ast.Expr, t soltype.Type) {
		// A borrow written at the call ends with the statement, so only its referent can keep
		// the closure, and only when the referent's own type mentions the lifetime.
		if borrow, isBorrow := expr.(*ast.BorrowExpr); isBorrow {
			ref, isRef := t.(*soltype.RefType)
			if !isRef {
				return
			}
			expr, t = borrow.Arg, ref.Inner
		}
		if !typeMentionsLifetime(c.ctx, t, shared) {
			return
		}
		if p, ok := c.exprPlace(expr); ok && p.root > 0 && !slices.Contains(holders, p.root) {
			holders = append(holders, p.root)
		}
	}
	if recv != nil && self != nil {
		add(recv, self.Type)
	}
	for j, arg := range e.Args {
		if j == skip {
			continue
		}
		if param, ok := paramForArg(fn, j); ok {
			add(arg, param.Type)
		}
	}
	return holders
}

// noteRetainedHolder adds holder to the holders of each retained store whose closure the
// value init carries. init carries it when init is a call whose result keeps the closure, or
// a name that is already one of the store's holders. It must run once the binding init flows
// into is known.
func (c *checker) noteRetainedHolder(holder liveness.VarID, init ast.Expr) {
	t := c.eventTable()
	if t == nil || holder <= 0 || init == nil {
		return
	}
	for _, store := range t.retainedResults[init] {
		store.holders = append(store.holders, holder)
	}
	p, ok := c.exprPlace(init)
	if !ok || p.root <= 0 || p.root == holder {
		return
	}
	for _, store := range t.retained {
		if slices.Contains(store.holders, p.root) && !slices.Contains(store.holders, holder) {
			store.holders = append(store.holders, holder)
		}
	}
}

// typeMentionsLifetime reports whether one of the lifetimes in lts occurs in t, as a borrow's
// lifetime, a function's retention lifetime, or a class or alias lifetime argument.
func typeMentionsLifetime(ctx *Context, t soltype.Type, lts []*soltype.LifetimeVar) bool {
	if t == nil || len(lts) == 0 {
		return false
	}
	occ, _ := walkLtOcc(t, soltype.Positive)
	found, _ := lifetimeVarsIn(ctx, t)
	return slices.ContainsFunc(lts, func(lv *soltype.LifetimeVar) bool {
		_, inOcc := occ[lv]
		return inOcc || slices.Contains(found, lv)
	})
}

// retentionLifetimes returns the retention lifetime of each function type in t, reached through
// unions, intersections, object fields, and type arguments. A function type's own parameters
// and return are not searched, since a function there belongs to a callback's signature.
func retentionLifetimes(t soltype.Type) []soltype.Lifetime {
	v := &retentionVisitor{}
	t.Accept(v, soltype.Positive)
	return v.lts
}

// retentionVisitor collects the retention lifetimes retentionLifetimes returns.
type retentionVisitor struct {
	lts []soltype.Lifetime
}

func (v *retentionVisitor) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	ft, isFn := t.(*soltype.FuncType)
	if !isFn {
		return soltype.EnterResult{}
	}
	if ft.Lt != nil {
		v.lts = append(v.lts, ft.Lt)
	}
	return soltype.EnterResult{SkipChildren: true}
}

func (*retentionVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// calleeName returns the name a call's callee is written as, `onClick` for `onClick(h)` and `on`
// for `button.on(h)`, and "" for any other callee.
func calleeName(callee ast.Expr) string {
	switch callee := callee.(type) {
	case *ast.IdentExpr:
		return callee.Name
	case *ast.MemberExpr:
		return callee.Prop.Name
	}
	return ""
}

// closureVia returns the phrase a message names a closure by when the closure is reached
// through the binding `name`, as in `'sink'`.
func closureVia(name string) string {
	return fmt.Sprintf("'%s'", name)
}

// retainedVia returns the phrase a message names a closure by when the callee `callee` keeps
// it through its parameter `param`, as in `the 'handler' that 'onClick' retains`. An empty
// callee or param leaves that name out.
func retainedVia(param, callee string) string {
	held := "the function"
	if param != "" {
		held = fmt.Sprintf("the '%s'", param)
	}
	if callee == "" {
		return held + " the callee retains"
	}
	return fmt.Sprintf("%s that '%s' retains", held, callee)
}

// noteRetainedClosureWrites fills the writes of each retained store of the body. A store
// writes each owned local that a closure written inside the stored value writes, and each
// owned local that a capture edge the value reaches in the flow-sensitive borrow graph before
// the store writes. So a closure bound to a name, copied to another name, or chosen on a
// branch counts. A local the closures only read is left out, and so is a capture holding a
// borrow. It must run once the body is walked, so the borrow graph is complete.
func (c *checker) noteRetainedClosureWrites() {
	t := c.eventTable()
	if t == nil || len(t.retained) == 0 || c.fn == nil || c.fn.cfg == nil {
		return
	}
	flowGraph := c.analyzeBorrows()

	types := map[liveness.VarID]soltype.Type{}
	for _, locals := range c.fn.capturedLocals {
		for _, local := range locals {
			types[local.root] = local.t
		}
	}

	for _, store := range t.retained {
		graph := flowGraph.fieldBorrowGraphBefore(store.at)
		writes := map[liveness.VarID]bool{}
		var edges []fieldBorrow
		for _, closure := range closuresIn(store.expr) {
			for _, local := range c.fn.capturedLocals[closure] {
				writes[local.root] = writes[local.root] || local.mut
				collectAllEdgesFrom(local.root, set.NewSet[liveness.VarID](), graph, &edges)
			}
		}
		if p, ok := c.exprPlace(store.expr); ok && p.root > 0 {
			collectEdgesFrom(p.root, p.path, set.NewSet[liveness.VarID](), graph, &edges)
		}
		for _, edge := range edges {
			if edge.capture {
				writes[edge.referent] = writes[edge.referent] || edge.mut
			}
		}

		store.writes = nil
		for root, written := range writes {
			// A closure that only reads the local needs it unchanged only while the closure
			// runs, and the frame cannot write it then.
			if written && c.isOwnedMovable(types[root]) {
				store.writes = append(store.writes, root)
			}
		}
		slices.Sort(store.writes)
	}
}
