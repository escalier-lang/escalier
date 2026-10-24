package solver

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// A shared value is one that a binding outside the current function frame can also reach.
// Module-level bindings, import members, and the variables a closure captures from its
// enclosing body all hold shared values, as does any field read off one of them. Moving a
// shared value into a local leaves the outer binding pointing at the same object, so the
// local is not its sole owner. The owned-mutable upgrade must not grant it a mutable view.
//
//	val xs: Array<number> = [1]
//	fn f() {
//		val a = xs     // `a` holds the array `xs` names
//		val mut m = a  // must not make `m` a mutable view of it
//	}

// sharedFlow is one place a value enters a local. Each local in targets receives the value the
// flow carries. That value is the one source produces when source is non-nil. Otherwise it is
// the value of the local fromLocal, or a shared value outright when always is set.
type sharedFlow struct {
	targets   []liveness.VarID
	source    ast.Expr
	fromLocal liveness.VarID
	always    bool
}

// sharedState is one body's record of which of its bindings may hold a shared value.
//
//   - flows lists every place a value enters a local of the body.
//   - valueLocals holds each `val` or `var` binding with no annotation whose initializer is a
//     number, string, boolean, or bigint literal. Its type is that primitive, so every value
//     it holds is a copy and never shared.
//   - scope is the scope the body's parameters bind into, which resolves a name outside the
//     frame.
//   - locals holds every binding that may hold a shared value, starting from the captures.
//   - ownLocals holds every binding that may hold a shared value when no capture is counted
//     as shared. A closure reports it to its enclosing body, which decides for itself whether
//     each capture is shared.
type sharedState struct {
	flows       []sharedFlow
	valueLocals set.Set[liveness.VarID]
	scope       *Scope
	locals      set.Set[liveness.VarID]
	ownLocals   set.Set[liveness.VarID]
}

// sharedFlowCollector gathers every sharedFlow of one function body. It stops at a nested
// function, whose body runs its own pre-pass.
type sharedFlowCollector struct {
	ast.DefaultVisitor
	state *sharedState
}

func (v *sharedFlowCollector) add(targets []liveness.VarID, source ast.Expr) {
	if source != nil && len(targets) > 0 {
		v.state.flows = append(v.state.flows, sharedFlow{targets: targets, source: source})
	}
}

func (v *sharedFlowCollector) EnterDecl(d ast.Decl) bool {
	switch d := d.(type) {
	case *ast.VarDecl:
		v.add(patVarIDs(d.Pattern), d.Init)
		if pat, ok := d.Pattern.(*ast.IdentPat); ok && pat.VarID > 0 && d.TypeAnn == nil && isCopiedLiteral(d.Init) {
			v.state.valueLocals.Add(liveness.VarID(pat.VarID))
		}
	case *ast.FuncDecl:
		return false
	}
	return true
}

func (v *sharedFlowCollector) EnterStmt(s ast.Stmt) bool {
	if s, ok := s.(*ast.ForInStmt); ok {
		v.add(patVarIDs(s.Pattern), s.Iterable)
	}
	return true
}

func (v *sharedFlowCollector) EnterExpr(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.FuncExpr:
		return false
	case *ast.BinaryExpr:
		if e.Op == ast.Assign {
			v.add(placeRootVarIDs(e.Left), e.Right)
		}
	case *ast.IfValExpr:
		v.add(patVarIDs(e.Pattern), e.Target)
	case *ast.MatchExpr:
		for _, mc := range e.Cases {
			v.add(patVarIDs(mc.Pattern), e.Target)
		}
	}
	return true
}

// isCopiedLiteral reports whether e is a number, string, boolean, or bigint literal.
func isCopiedLiteral(e ast.Expr) bool {
	lit, ok := e.(*ast.LiteralExpr)
	if !ok {
		return false
	}
	switch lit.Lit.(type) {
	case *ast.NumLit, *ast.StrLit, *ast.BoolLit, *ast.BigIntLit:
		return true
	}
	return false
}

// patVarIDs returns the VarID of each leaf binding of pat that the rename pass numbered.
func patVarIDs(pat ast.Pat) []liveness.VarID {
	var ids []liveness.VarID
	ast.ForEachLeafBinding(pat, func(_ string, varID int) {
		if varID > 0 {
			ids = append(ids, liveness.VarID(varID))
		}
	})
	return ids
}

// placeRootVarIDs returns the VarID of the binding the assignment target lhs is rooted at, or
// nil when lhs names no place. A store into a field writes into the whole binding, so
// `a.inner = xs` yields `a`.
func placeRootVarIDs(lhs ast.Expr) []liveness.VarID {
	if p, ok := exprPlace(lhs); ok {
		return []liveness.VarID{p.root}
	}
	return nil
}

// collectSharedLocals builds the body's sharedState. It runs after the rename pass, so every
// binding in body carries its VarID, and before the body is walked. scope is the scope the
// body's parameters bind into.
//
// Each flow is visited regardless of its position in the body, so a reassignment later in a
// loop still marks a local read earlier in the loop.
func (c *checker) collectSharedLocals(scope *Scope, body *ast.Block) {
	state := &sharedState{
		valueLocals: set.NewSet[liveness.VarID](),
		scope:       scope,
		locals:      c.fn.captureVarIDs.Clone(),
		ownLocals:   set.NewSet[liveness.VarID](),
	}
	body.Accept(&sharedFlowCollector{state: state})
	c.fn.shared = state
	c.settleSharedLocals()
}

// settleSharedLocals grows the body's shared locals and own shared locals to a fixpoint over
// its flows. A set only grows, so a decision already taken against it stays valid.
func (c *checker) settleSharedLocals() {
	state := c.fn.shared
	c.sharedFixpoint(state.locals)
	c.sharedFixpoint(state.ownLocals)
}

// sharedFixpoint adds to shared every local some flow of the body may carry a shared value
// into, judging each flow's value against shared itself, until nothing more is added.
func (c *checker) sharedFixpoint(shared set.Set[liveness.VarID]) {
	state := c.fn.shared
	for changed := true; changed; {
		changed = false
		for _, f := range state.flows {
			if !c.flowCarriesShared(f, shared) {
				continue
			}
			for _, id := range f.targets {
				if !state.valueLocals.Contains(id) && !shared.Contains(id) {
					shared.Add(id)
					changed = true
				}
			}
		}
	}
}

// flowCarriesShared reports whether f may carry a shared value, judging a local by whether
// shared holds it.
func (c *checker) flowCarriesShared(f sharedFlow, shared set.Set[liveness.VarID]) bool {
	switch {
	case f.always:
		return true
	case f.source != nil:
		return c.mayYieldShared(c.fn.shared.scope, f.source, shared)
	default:
		return shared.Contains(f.fromLocal)
	}
}

// resettleSharedLocals settles the body's shared locals again when the walk shows that source,
// which flows into targets, may yield a shared value some target is not yet marked for. The
// walk knows what the pre-pass did not: the types of what it has walked, and the declaration
// of each function this body declares and calls. It is a no-op outside a function body.
func (c *checker) resettleSharedLocals(scope *Scope, targets []liveness.VarID, source ast.Expr) {
	if c.fn == nil || c.fn.shared == nil || len(targets) == 0 {
		return
	}
	state := c.fn.shared
	for _, shared := range []set.Set[liveness.VarID]{state.locals, state.ownLocals} {
		if !set.FromSlice(targets).IsSubset(shared) && c.mayYieldShared(scope, source, shared) {
			c.settleSharedLocals()
			return
		}
	}
}

// captureShare is what a closure reports to its enclosing body about one capture: the
// closure may store a shared value into it. always says the value is shared whatever the
// enclosing body holds. from names the other captures whose value the closure may store
// into it, so the capture is shared when any of them is.
type captureShare struct {
	name   string
	always bool
	from   []string
}

// captureShares returns what the current body reports to its enclosing body about each of
// its captures. Each fixpoint it runs starts from the body's own shared locals and counts
// one capture as shared, which shows the captures a value stored from that one reaches.
func (c *checker) captureShares() []captureShare {
	state := c.fn.shared
	if state == nil || c.fn.captureVarIDs.Len() == 0 {
		return nil
	}
	captures := c.fn.captureVarIDs.ToSlice()
	slices.Sort(captures)
	reached := map[liveness.VarID][]string{}
	for _, from := range captures {
		shared := state.ownLocals.Clone()
		shared.Add(from)
		c.sharedFixpoint(shared)
		for _, to := range captures {
			if to != from && shared.Contains(to) && !state.ownLocals.Contains(to) {
				reached[to] = append(reached[to], c.fn.varIDNames[from])
			}
		}
	}
	var shares []captureShare
	for _, id := range captures {
		always := state.ownLocals.Contains(id)
		if always || len(reached[id]) > 0 {
			shares = append(shares, captureShare{name: c.fn.varIDNames[id], always: always, from: reached[id]})
		}
	}
	return shares
}

// applyCaptureShares adds to the current body the flows a closure declared in scope reported
// through shares, then settles the body's shared locals. A name that resolves to no local of
// the current body is skipped.
func (c *checker) applyCaptureShares(scope *Scope, shares []captureShare) {
	if c.fn == nil || c.fn.shared == nil || len(shares) == 0 {
		return
	}
	for _, share := range shares {
		target, ok := c.bodyLocal(scope, share.name)
		if !ok {
			continue
		}
		targets := []liveness.VarID{target}
		if share.always {
			c.fn.shared.flows = append(c.fn.shared.flows, sharedFlow{targets: targets, always: true})
		}
		for _, name := range share.from {
			if from, ok := c.bodyLocal(scope, name); ok {
				c.fn.shared.flows = append(c.fn.shared.flows, sharedFlow{targets: targets, fromLocal: from})
			}
		}
	}
	c.settleSharedLocals()
}

// bodyLocal returns the VarID of the current body's binding that name resolves to in scope.
// ok is false when the name resolves to a binding of another body or to none.
func (c *checker) bodyLocal(scope *Scope, name string) (liveness.VarID, bool) {
	b, found := scope.GetValue(name)
	if !found || b.VarID <= 0 {
		return 0, false
	}
	id := liveness.VarID(b.VarID)
	return id, c.fn.varIDNames[id] == name
}

// isSharedLocal reports whether the current body's binding id may hold a shared value.
func (c *checker) isSharedLocal(id liveness.VarID) bool {
	return c.fn != nil && c.fn.shared != nil && c.fn.shared.locals.Contains(id)
}

// mayYieldShared reports whether the value e produces may be a shared value, judging a local
// of the current body by whether shared holds it. It answers true when e may be one of these:
//
//   - a binding outside the frame, or a local that shared holds;
//   - a field or element read off such a value;
//   - a literal holding such a value, directly or through a spread;
//   - a branch result of a conditional, match, `do`, or `try` that is such a value;
//   - a call of a function whose body returns such a value.
//
// A value that copies, such as a number, is never shared. Once e is walked its recorded type
// decides that. Before then, as in the pre-pass, only a binding outside the frame is known to
// copy, so a local read is judged by its flows alone. scope resolves a name outside the frame.
func (c *checker) mayYieldShared(scope *Scope, e ast.Expr, shared set.Set[liveness.VarID]) bool {
	if e == nil {
		return false
	}
	if t := c.info.TypeOf(e); t != nil && !c.isOwnedMovable(t) {
		return false
	}
	switch e := e.(type) {
	case *ast.IdentExpr:
		if e.VarID > 0 {
			return shared.Contains(liveness.VarID(e.VarID))
		}
		if t := c.outerBindingType(scope, e); t != nil && isValueType(t) {
			return false
		}
		return true
	case *ast.MemberExpr:
		return c.mayYieldShared(scope, e.Object, shared)
	case *ast.IndexExpr:
		return c.mayYieldShared(scope, e.Object, shared)
	case *ast.TypeCastExpr:
		return c.mayYieldShared(scope, e.Expr, shared)
	case *ast.AwaitExpr:
		return c.mayYieldShared(scope, e.Arg, shared)
	case *ast.ObjectExpr:
		for _, elem := range e.Elems {
			switch elem := elem.(type) {
			case *ast.PropertyExpr:
				if c.mayYieldShared(scope, elem.ValueExpr(), shared) {
					return true
				}
			case *ast.ObjSpreadExpr:
				if c.spreadYieldsShared(scope, elem.Value, shared) {
					return true
				}
			}
		}
		return false
	case *ast.TupleExpr:
		for _, elem := range e.Elems {
			if spread, ok := elem.(*ast.ArraySpreadExpr); ok {
				if c.spreadYieldsShared(scope, spread.Value, shared) {
					return true
				}
				continue
			}
			if c.mayYieldShared(scope, elem, shared) {
				return true
			}
		}
		return false
	case *ast.IfElseExpr:
		return c.mayYieldShared(scope, liveness.BlockResultExpr(e.Cons), shared) ||
			c.mayYieldShared(scope, liveness.BlockOrExprResultExpr(e.Alt), shared)
	case *ast.IfValExpr:
		return c.mayYieldShared(scope, liveness.BlockResultExpr(e.Cons), shared) ||
			c.mayYieldShared(scope, liveness.BlockOrExprResultExpr(e.Alt), shared)
	case *ast.MatchExpr:
		return c.anyCaseYieldsShared(scope, e.Cases, shared)
	case *ast.DoExpr:
		return c.mayYieldShared(scope, liveness.BlockResultExpr(e.Body), shared)
	case *ast.TryCatchExpr:
		return c.mayYieldShared(scope, liveness.BlockResultExpr(e.Try), shared) ||
			c.anyCaseYieldsShared(scope, e.Catch, shared)
	case *ast.CallExpr:
		return c.callReturnsShared(scope, e)
	}
	return false
}

// spreadYieldsShared reports whether spreading src into a literal may place a shared value in
// it. A spread copies src's elements or fields into the new literal, so it places a shared
// value only when src may be shared and some element or field of it is not a value type.
// src's type is read from scope when src names a binding outside the frame or a capture of
// one, and from the walk otherwise.
func (c *checker) spreadYieldsShared(scope *Scope, src ast.Expr, shared set.Set[liveness.VarID]) bool {
	var t soltype.Type
	if ident, ok := src.(*ast.IdentExpr); ok && (ident.VarID <= 0 || c.isCapture(ident)) {
		t = c.outerBindingType(scope, ident)
	} else {
		t = c.info.TypeOf(src)
	}
	if t != nil && c.holdsOnlyValues(t) {
		return false
	}
	return c.mayYieldShared(scope, src, shared)
}

// isCapture reports whether ident reads a variable the current body captures.
func (c *checker) isCapture(ident *ast.IdentExpr) bool {
	return c.fn != nil && c.fn.captureVarIDs.Contains(liveness.VarID(ident.VarID))
}

// holdsOnlyValues reports whether every element or field of t is a value type. It answers for
// an `Array<T>` instance, a tuple, and an object, and false for any other type.
func (c *checker) holdsOnlyValues(t soltype.Type) bool {
	t = c.expandAliasChain(t)
	if elem, ok := c.ctx.arrayElem(t); ok {
		return isValueType(elem)
	}
	switch t := t.(type) {
	case *soltype.TupleType:
		for _, elem := range t.Elems {
			if !isValueType(elem) {
				return false
			}
		}
		return true
	case *soltype.ObjectType:
		for _, elem := range t.Elems {
			prop, ok := elem.(*soltype.PropertyElem)
			if !ok || !isValueType(prop.Type) {
				return false
			}
		}
		return true
	}
	return false
}

// outerBindingType returns the type of the binding ident names in scope, or nil when scope is
// nil or holds no value binding by that name.
func (c *checker) outerBindingType(scope *Scope, ident *ast.IdentExpr) soltype.Type {
	if scope == nil {
		return nil
	}
	b, found := c.lookupValueBinding(scope, ident.Name)
	if !found {
		return nil
	}
	return bindingType(b)
}

// anyCaseYieldsShared reports whether the body of any case in cases may yield a shared value.
func (c *checker) anyCaseYieldsShared(scope *Scope, cases []*ast.MatchCase, shared set.Set[liveness.VarID]) bool {
	for _, mc := range cases {
		if c.mayYieldShared(scope, liveness.BlockOrExprResultExpr(&mc.Body), shared) {
			return true
		}
	}
	return false
}

// callReturnsShared reports whether call names, by a bare identifier, a function whose body
// returns a shared value. The callee resolves through the declaration the walk recorded for
// it, or through scope when the walk has not reached it and scope is non-nil. A callee
// reached any other way, such as a method or a namespace member, answers false.
func (c *checker) callReturnsShared(scope *Scope, call *ast.CallExpr) bool {
	ident, ok := call.Callee.(*ast.IdentExpr)
	if !ok {
		return false
	}
	if decl, ok := c.info.DeclOf(ident); ok {
		return c.funcReturnsShared(decl)
	}
	if scope == nil {
		return false
	}
	b, found := c.lookupValueBinding(scope, ident.Name)
	if !found {
		return false
	}
	for _, src := range b.Sources {
		if np, ok := src.(*ast.NodeProvenance); ok && c.funcReturnsShared(np.Node) {
			return true
		}
	}
	return false
}

// funcReturnsShared reports whether decl declares a function whose body returns a shared
// value. decl is a `fn` declaration, or a `val`/`var` declaration whose initializer is the
// function expression.
func (c *checker) funcReturnsShared(decl ast.Node) bool {
	if vd, ok := decl.(*ast.VarDecl); ok {
		if vd.Init == nil {
			return false
		}
		decl = vd.Init
	}
	return c.sharedReturns.Contains(decl) || c.librarySharedReturns.Contains(decl)
}
