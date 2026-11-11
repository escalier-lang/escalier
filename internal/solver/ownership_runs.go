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
// walk records the names each call passes and the closures each retaining store keeps. The
// dataflow pass in ownership_dataflow.go finds the closures from the loans each name holds,
// and checks E1, E2, and E3 against the captures they use.

// runEvent marks the point of a call at which the call runs the closures its callee and
// arguments name. The dataflow pass also finds there each closure a retainedStore keeps
// reachable. The event's node is the call, and its place names nothing.
type runEvent struct {
	eventBase
	call *ast.CallExpr
	// `names` lists the callee when it names a place under a local, then each argument that
	// names one, in argument order. The dataflow pass finds the closures each place holds.
	names []closureName
	// `callee` is the place of the binding the callee names, when that binding holds a
	// closure whose body moves one of its captures. Each call of such a closure moves it, as
	// D2 decides, so a second call uses a moved binding. `consumes` is false for any other
	// call.
	callee   movePlace
	consumes bool
}

// closureName is the callee or an argument of a call, written as a place under a local, such
// as `f`, `o.cb`, or `o.list[0]`. The call runs each closure the callee's place holds, and
// may run each closure an argument's place holds.
type closureName struct {
	node  ast.Expr
	place movePlace
	// `runs` marks a callee whose place names one closure, which the call is sure to run.
	runs bool
}

// closureRun is one closure a call runs, or may run.
type closureRun struct {
	// `node` is the expression that names the closure, which a diagnostic blames.
	node ast.Node
	// `via` names the closure in a diagnostic. It is a phrase closureVia or retainedVia
	// returns.
	via string
	// `runs` marks a closure the call is sure to run, because the call's callee names it.
	runs bool
	// `captures` lists each local the closure captures, in VarID order.
	captures []capturedLocal
}

// retainedStore records a value that may carry a closure and stays reachable after its
// statement. The value is stored into a module-level binding, or passed to a parameter whose
// function type carries a retention lifetime.
type retainedStore struct {
	// `via` names the binding or parameter that keeps the closure. closureVia or retainedVia
	// produces it.
	via string
	// `expr` is the stored value.
	expr ast.Expr
	// `place` is the place `expr` names, and `named` reports whether it names one.
	place movePlace
	named bool
	// `closures` lists each closure written inside `expr`, as closuresIn returns them.
	closures []*ast.FuncExpr
	// `at` is the CFG point of the statement that stores `expr`.
	at liveness.StmtRef
	// `call` is the call whose parameter keeps the closure, and nil for a store into a
	// module-level binding.
	call ast.Node
	// `static` marks a closure kept for the rest of the program. Every call after the store may
	// run it.
	static bool
	// `holders` are the bindings that keep a closure reachable that is not kept for the rest
	// of the program. The holders are these bindings:
	//
	//   - each argument whose parameter type shares the retention lifetime
	//   - the receiver, when the type of `self` shares that lifetime
	//   - each binding the call's result or another holder flows into
	//
	// A call may run the closure only while one of them is live.
	holders []liveness.VarID
	// `writes` lists the owned locals the closures write, in VarID order. The dataflow pass
	// fills it at `at`, as noteStoreWrites describes.
	writes []liveness.VarID
}

// emitClosureRuns emits the runEvent of the call `e`, attached to `ref`. It must run once the
// arguments are inferred and before their flows are emitted.
func (c *checker) emitClosureRuns(e *ast.CallExpr, ref liveness.StmtRef) {
	if !c.firstEmit(e, runRole, ref, 0) {
		return
	}

	ev := &runEvent{eventBase: eventBase{node: e}, call: e}
	// The call runs the closure the callee's place holds. A callee read through a dynamic
	// index, such as `o.list[i]`, names the whole list. The call runs only one of the closures
	// the list holds, so it may run each of them.
	if name, ok := c.closureNameOf(e.Callee, true); ok {
		ev.names = append(ev.names, name)
	}
	for _, arg := range e.Args {
		// The callee may call an argument, or a closure the argument reaches, so the call may
		// run each closure the argument's place holds.
		if name, ok := c.closureNameOf(arg, false); ok {
			ev.names = append(ev.names, name)
		}
	}

	if ident, ok := e.Callee.(*ast.IdentExpr); ok && c.varIDs.Of(ident) > 0 {
		if decl, found := c.info.DeclOf(ident); found && c.funcMovesCaptures(decl) {
			ev.callee, ev.consumes = movePlace{root: c.varIDs.Of(ident)}, true
		}
	}

	c.emitEvent(ev, ref)
}

// closureNameOf returns `expr` as a closureName when it names a place under a local, as
// exprPlace finds it. `callee` marks the callee of the call. The closureName runs when
// `callee` is set and the chain holds no dynamic index. A dynamic index makes the place the
// container the index reads from, as in `o.list[i]`, whose place is `o.list`. ok is false
// when a link of the chain reads a method or a getter, as `xs.push` and `xs[i].size` do,
// since such a link reads no closure that the receiver holds.
func (c *checker) closureNameOf(expr ast.Expr, callee bool) (closureName, bool) {
	// A link after a dynamic index still reads a method or a field of the element, so each
	// named link is checked, not only those fieldChain reaches.
	for _, link := range chainOf(expr, c.indexSegment).links {
		if !link.dynamic && link.seg.name != "" && c.linkCloses(link) {
			return closureName{}, false
		}
	}
	place, dynamic, ok := c.chainPlace(expr)
	if !ok {
		return closureName{}, false
	}
	return closureName{node: expr, place: place, runs: callee && !dynamic}, true
}

// closureRunOf returns the run of the closures that the place `name` names holds in s, with
// the locals those closures capture. `ok` is false when the place holds no closure that
// captures a local of the body.
//
// A closure's loan of each capture is held at a place that overlaps the named one, whether
// that place was bound to the closure or copied from one that was. A local captured through
// several loans is listed once, as a write when any loan writes it. It covers the field paths
// the loans reach, or the whole local when any loan reaches all of it. A loan of a place
// under the named place's own local is left out.
//
// The run is sure only when the name is and each loan is held at the named place or under
// it. A loan held only at a place that encloses the named one may belong to a sibling of it.
// After `val v = view(&mut b)`, v holds the closures of both b.cb and b.other, so `v.cb()`
// may run each of them.
func (d *bodyDataflow) closureRunOf(name closureName, s *dataflowState) (*closureRun, bool) {
	// Group the closure loans by the local each one reaches. With
	// `val f = fn () { p.a = 1; log(p.b) }`, `f` holds two loans of `p`, a mutable one of `p.a`
	// and an immutable one of `p.b`. They merge into one capturedLocal for `p` that is mutable
	// and covers both paths. A loan with an empty path reaches the whole local, which `whole`
	// records.
	held := name.place
	captured := map[liveness.VarID]*capturedLocal{}
	whole := set.NewSet[liveness.VarID]()
	sure := name.runs
	for _, l := range d.sortedLoans(s) {
		root := l.ev.place.root
		if !l.closure() || root == held.root || !l.heldAt(held) {
			continue
		}
		sure = sure && l.heldWithin(held)
		local, seen := captured[root]
		if !seen {
			local = &capturedLocal{root: root}
			captured[root] = local
		}
		local.mut = local.mut || l.ev.borrow == mutableBorrow
		path := l.ev.place.path
		switch {
		case len(path) == 0:
			whole.Add(root)
		case !slices.ContainsFunc(local.paths, func(p []placeSeg) bool { return slices.Equal(p, path) }):
			local.paths = append(local.paths, path)
		}
	}
	if len(captured) == 0 {
		return nil, false
	}

	run := &closureRun{node: name.node, via: closureVia(d.placeName(held)), runs: sure}
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

// noteBoundClosure records `closure` as one whose capture is not used where it is written.
// One is the initializer of a local name. The captures of `val f = fn () { … }` are used
// at each call of `f` and each call `f` is passed to. A script's `fn`
// declaration is used the same way. Another is stored into a module-level binding, whose
// captures are used at each call after the store.
func (c *checker) noteBoundClosure(closure ast.Func) {
	if t := c.eventTable(); t != nil {
		t.boundClosures.Add(closure)
	}
}

// noteRetainedClosureStore adds `store` to the body's retained stores and returns it, with
// the place and the closures of its value filled in. It returns nil when the body has no event
// table.
func (c *checker) noteRetainedClosureStore(store retainedStore) *retainedStore {
	t := c.eventTable()
	if t == nil || t.cfg == nil {
		return nil
	}
	s := &store
	if p, ok := c.exprPlace(s.expr); ok && p.root > 0 {
		s.place, s.named = p, true
	}
	s.closures = closuresIn(s.expr)
	t.retained = append(t.retained, s)
	return s
}

// noteRetainedClosureArgs records each argument of the call `e` that fills a parameter `fn` keeps
// past the call. Each one becomes a retainedStore at `ref`. Such a parameter's type holds a function type
// with a retention lifetime, at its top or inside a union, an object field, or an array. A
// whole argument filling it is taken as kept, so each closure written in an object literal
// counts.
//
// A retention lifetime that is `'static`, or bound to outlive `'static`, keeps the closure for the
// rest of the program. Any other retention lifetime keeps it as long as a binding that shares
// the lifetime is live. When no binding shares it, the callee can keep the closure no longer
// than the call, and nothing is recorded.
func (c *checker) noteRetainedClosureArgs(e *ast.CallExpr, fn *soltype.FuncType, ref liveness.StmtRef) {
	// A body with no event table runs no dataflow pass, so nothing would read a store.
	t := c.eventTable()
	if t == nil {
		return
	}

	// recv is the receiver of a method call, and self is the method's `self` parameter. Both
	// are nil for a plain call. The receiver can be a holder of a kept closure.
	recv, self := c.calleeReceiver(e.Callee)

	for i, arg := range e.Args {
		// Find the parameter this argument fills. An argument past the last parameter fills
		// none, and neither does any argument after it.
		param, ok := paramForArg(fn, i)
		if !ok {
			break
		}

		// A parameter whose type holds no function with a lifetime records no store. A
		// function's lifetime is the one written ahead of `fn`, such as `'a` in
		// `handler: 'a fn () -> undefined`, or the one elision gives a `fn` written with none.
		lts := retentionLifetimes(param.Type)
		if len(lts) == 0 {
			continue
		}

		// Build the store. via is the phrase a diagnostic names the closure by, such as
		// `the 'handler' that 'onClick' retains`.
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

		// Split the retention lifetimes. One that is `'static` or outlives `'static` keeps the
		// closure for the rest of the program. A lifetime variable such as `'a` keeps it only
		// while some binding that shares `'a` is live, so it goes in shared.
		var shared []*soltype.LifetimeVar
		for _, lt := range lts {
			if lifetimeEscapedToStatic(lt) {
				store.static = true
			} else if lv, isVar := lt.(*soltype.LifetimeVar); isVar {
				shared = append(shared, lv)
			}
		}

		// Find what keeps a closure that is not kept for the rest of the program. In
		// `subscribe(e, fn () { … })` with `subscribe<'a>(emitter: &'a mut Emitter, handler:
		// 'a fn () -> undefined)`, the binding `e` shares `'a` and is a holder. A result type
		// that mentions `'a` keeps the closure too, so the binding the result flows into
		// becomes a holder later. With neither, the closure cannot outlive the call, and the
		// argument records no store.
		returned := false
		if !store.static {
			store.holders = c.retentionHolders(e, fn, recv, self, i, shared)
			returned = typeMentionsLifetime(c.ctx, fn.Ret, shared)
			if len(store.holders) == 0 && !returned {
				continue
			}
		}

		// Record the store. When the result keeps the closure, also map the call to the store
		// in retainedResults. noteRetainedHolder reads that map to add the binding the result
		// flows into as a holder.
		if s := c.noteRetainedClosureStore(store); s != nil && returned {
			t.retainedResults[e] = append(t.retainedResults[e], s)
		}
	}
}

// paramForArg returns the parameter of `fn` the argument at index `i` fills. Every argument past a
// trailing rest parameter fills that parameter. `ok` is false when no parameter takes the argument.
func paramForArg(fn *soltype.FuncType, i int) (*soltype.FuncParam, bool) {
	if i < len(fn.Params) {
		return fn.Params[i], true
	}
	if n := len(fn.Params); n > 0 && fn.Params[n-1].Rest {
		return fn.Params[n-1], true
	}
	return nil, false
}

// retentionHolders returns the bindings that share one of the lifetimes in `shared` with the
// parameter at index `skip` of the call `e`. They are the arguments, and the receiver `recv` whose
// parameter is `self`, that name a binding and whose parameter types mention one of those
// lifetimes. For an argument written as a borrow, such as `&mut em`, the holder is the borrowed
// binding, and only when the borrowed type mentions one of those lifetimes.
func (c *checker) retentionHolders(
	e *ast.CallExpr, fn *soltype.FuncType, recv ast.Expr, self *soltype.FuncParam, skip int, shared []*soltype.LifetimeVar,
) []liveness.VarID {
	// add records the binding expr names as a holder when the type t of the parameter it
	// fills mentions one of the shared lifetimes. Each binding is added once.
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

		// A parameter whose type does not mention a shared lifetime cannot keep the closure.
		if !typeMentionsLifetime(c.ctx, t, shared) {
			return
		}

		// Only an argument that names a binding, such as `e` or `e.inner`, has a root to
		// record. An argument such as a literal or a call result is not a holder.
		if p, ok := c.exprPlace(expr); ok && p.root > 0 && !slices.Contains(holders, p.root) {
			holders = append(holders, p.root)
		}
	}

	// The receiver of a method call is a holder when the method's `self` type shares the
	// lifetime. In `em.on(h)` the receiver `em` can keep `h`.
	if recv != nil && self != nil {
		add(recv, self.Type)
	}

	// Check every other argument. The argument at skip is the kept closure itself, so it
	// does not hold itself.
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

// noteRetainedHolder adds `holder` to the holders of each retained store whose closure the
// value `init` carries. `init` carries it when `init` is a call whose result keeps the closure, or
// a name that is already one of the store's holders. It must run once the binding `init` flows
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

// typeMentionsLifetime reports whether one of the lifetimes in `lts` occurs in `t`, as a borrow's
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
// `callee` or `param` leaves that name out.
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

// noteStoreWrites adds to the `writes` of `store` each owned local that a closure the stored
// value reaches in s writes, and sets writesGrew when one is new. s is the state before the
// statement that makes the store. The value reaches these closures:
//
//   - each closure written inside it, as in `onClick(fn () { count.n = count.n + 1 })`
//   - each closure the place it names holds, as in `val f = fn () { … }; onClick(f)`, which
//     also covers a copy such as `val g = f` and a closure chosen on a branch
//   - each closure a place those closures capture, or a place a loan held there borrows,
//     holds in turn, since running the stored closure can run that one
//
// A local the closures only read is left out, and so is a capture holding a borrow. The
// writes only grow, so the fixed-point iteration in checkBodyDataflow ends.
func (d *bodyDataflow) noteStoreWrites(store *retainedStore, s *dataflowState) {
	// `written` holds each local some closure writes. `next` lists the places whose loans
	// are still to be read, and `seen` the locals already read whole.
	written := set.NewSet[liveness.VarID]()
	var next []movePlace
	for _, closure := range store.closures {
		for _, capture := range d.captures[closure] {
			root := capture.base().place.root
			if b, ok := capture.(*borrowEvent); ok && b.borrow == mutableBorrow {
				written.Add(root)
			}
			next = append(next, movePlace{root: root})
		}
	}
	if store.named {
		next = append(next, store.place)
	}

	seen := set.NewSet[liveness.VarID]()
	for len(next) > 0 {
		p := next[len(next)-1]
		next = next[:len(next)-1]
		if len(p.path) == 0 {
			if seen.Contains(p.root) {
				continue
			}
			seen.Add(p.root)
		}

		// A loan held at p reaches its place. A closure's loan also says whether the closure
		// writes that place.
		for _, l := range s.loans {
			if !l.heldAt(p) {
				continue
			}
			if l.closure() && l.ev.borrow == mutableBorrow {
				written.Add(l.ev.place.root)
			}
			next = append(next, movePlace{root: l.ev.place.root})
		}
	}

	for root := range written {
		// A closure that only reads the local needs it unchanged only while the closure runs,
		// and the frame cannot write it then. A capture holding a borrow is not owned.
		if slices.Contains(store.writes, root) || !d.ownedMovable(d.table.captureTypes[root]) {
			continue
		}
		store.writes = append(store.writes, root)
		d.writesGrew = true
	}
	slices.Sort(store.writes)
}

// closureCollector gathers the closures an expression carries by value, riding the shared AST
// visitor so it reaches a closure nested in an object or tuple literal, a spread, or a
// control-flow carrier such as an if/else branch. It stops at a call boundary, where a closure
// is not part of the value the scanned expression yields, and does not enter a closure's body.
type closureCollector struct {
	*ast.DefaultVisitor
	found []*ast.FuncExpr
}

func (v *closureCollector) EnterExpr(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.FuncExpr:
		v.found = append(v.found, e)
		return false
	case *ast.CallExpr, *ast.TaggedTemplateLitExpr:
		// A closure written as a call argument goes to that call, and the call's result does
		// not carry it, so `store(wrap(fn () { … }))` carries no closure.
		return false
	}
	return true
}

// closuresIn returns every closure the expression e carries by value.
func closuresIn(e ast.Expr) []*ast.FuncExpr {
	v := &closureCollector{DefaultVisitor: &ast.DefaultVisitor{}}
	e.Accept(v)
	return v.found
}
