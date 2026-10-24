package solver

import (
	"fmt"
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
// flow carries. That value is the one source produces, or a shared value outright when source
// is nil.
type sharedFlow struct {
	targets []liveness.VarID
	source  ast.Expr
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
type sharedState struct {
	flows       []sharedFlow
	valueLocals set.Set[liveness.VarID]
	scope       *Scope
	locals      set.Set[liveness.VarID]
}

// sharedFlowCollector gathers every sharedFlow of one function body. It stops at a nested
// function, whose body runs its own collectSharedLocals.
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
	}
	body.Accept(&sharedFlowCollector{state: state})
	c.fn.shared = state
	c.settleSharedLocals()
}

// settleSharedLocals adds to the body's shared locals every local some flow may carry a shared
// value into, judging each flow against the set as it grows, until nothing more is added. The
// set only grows.
func (c *checker) settleSharedLocals() {
	state := c.fn.shared
	for changed := true; changed; {
		changed = false
		for _, f := range state.flows {
			if f.source != nil && !c.mayYieldShared(state.scope, f.source, state.locals) {
				continue
			}
			for _, id := range f.targets {
				if c.addSharedLocal(id) {
					changed = true
				}
			}
		}
	}
}

// addSharedLocal adds id to the body's shared locals and reports whether it was added. A
// local in valueLocals is never added.
func (c *checker) addSharedLocal(id liveness.VarID) bool {
	state := c.fn.shared
	if state.valueLocals.Contains(id) || state.locals.Contains(id) {
		return false
	}
	state.locals.Add(id)
	return true
}

// markSharedFlow adds each local in targets to the body's shared locals when source may yield
// a shared value judged from scope, then settles the set so every local reading one of them
// is added too. It is a no-op outside a function body.
func (c *checker) markSharedFlow(scope *Scope, targets []liveness.VarID, source ast.Expr) {
	if c.fn == nil || c.fn.shared == nil {
		return
	}
	state := c.fn.shared
	if !slices.ContainsFunc(targets, func(id liveness.VarID) bool {
		return !state.valueLocals.Contains(id) && !state.locals.Contains(id)
	}) {
		return
	}
	if !c.mayYieldShared(scope, source, state.locals) {
		return
	}
	for _, id := range targets {
		c.addSharedLocal(id)
	}
	c.settleSharedLocals()
}

// sharedCaptureStores returns the name of each capture of the current body that some flow
// may store a shared value into, in VarID order. Every capture counts as shared here, so a
// store of one capture into another is reported too.
func (c *checker) sharedCaptureStores() []string {
	state := c.fn.shared
	if state == nil {
		return nil
	}
	stored := set.NewSet[liveness.VarID]()
	for _, f := range state.flows {
		if f.source != nil && !c.mayYieldShared(state.scope, f.source, state.locals) {
			continue
		}
		for _, id := range f.targets {
			if c.fn.captureVarIDs.Contains(id) {
				stored.Add(id)
			}
		}
	}
	ids := stored.ToSlice()
	slices.Sort(ids)
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = c.fn.varIDNames[id]
	}
	return names
}

// markSharedCaptures adds to the current body's shared locals the binding each name in names
// resolves to in scope, then settles the set. A name that resolves to a binding of another
// body, or to none, is skipped.
func (c *checker) markSharedCaptures(scope *Scope, names []string) {
	if c.fn == nil || c.fn.shared == nil || len(names) == 0 {
		return
	}
	for _, name := range names {
		b, found := scope.GetValue(name)
		if !found || b.VarID <= 0 {
			continue
		}
		id := liveness.VarID(b.VarID)
		if c.fn.varIDNames[id] != name {
			continue
		}
		// A store inside a closure is a flow of this body that collectSharedLocals did not
		// see, so it joins the flows a later settle replays.
		c.fn.shared.flows = append(c.fn.shared.flows, sharedFlow{targets: []liveness.VarID{id}})
	}
	c.settleSharedLocals()
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
// decides that. Before then, as when collectSharedLocals runs, only a binding outside the
// frame is known to copy, so a local read is judged by its flows alone. scope resolves a name
// outside the frame.
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

// SharedOwnedArgError reports a shared value passed to a parameter that takes ownership of it.
// Passing an argument to an owned parameter moves it into the callee, and a shared value cannot
// move, since the binding outside the frame keeps reaching it.
type SharedOwnedArgError struct {
	// Arg spells the argument as source, `xs` or `o.items`, and is empty when the argument is
	// not a name or a chain of member reads off one.
	Arg  string
	node ast.Node
}

func (*SharedOwnedArgError) isSolverError()        {}
func (e *SharedOwnedArgError) Span() ast.Span      { return e.node.Span() }
func (e *SharedOwnedArgError) Related() []ast.Span { return nil }
func (e *SharedOwnedArgError) Message() string {
	if e.Arg == "" {
		return "cannot move a shared value into an owned parameter. Make the parameter a borrow and borrow the argument with `&`"
	}
	return fmt.Sprintf("cannot move shared value '%s' into an owned parameter. Make the parameter a borrow and pass `&%s`", e.Arg, e.Arg)
}

// rejectSharedArg reports a SharedOwnedArgError for arg when it may be a shared value judged
// from scope, and reports whether it did. param is the owned parameter type arg fills. It
// reports nothing outside a function body, or when param is owned-mutable, since the
// immutable→mutable upgrade already rejects a shared argument there.
func (c *checker) rejectSharedArg(scope *Scope, arg ast.Expr, param soltype.Type) bool {
	if c.fn == nil || c.fn.shared == nil || isOwnedMut(param) {
		return false
	}
	if !c.mayYieldShared(scope, arg, c.fn.shared.locals) {
		return false
	}
	name, _ := pathName(arg)
	c.report(&SharedOwnedArgError{Arg: name, node: arg})
	return true
}

// calleeFromNpmPackage reports whether the callee e resolved to fn is declared in an npm
// package's types. It reads the first of these the call has:
//
//   - the declaration fn was inferred from;
//   - the declaration the callee names, as a namespace member or a bare identifier;
//   - the class of a method call's receiver.
//
// A callee reached any other way, such as a method of an interface, answers false.
func (c *checker) calleeFromNpmPackage(e *ast.CallExpr, fn *soltype.FuncType) bool {
	packages := c.ctx.packages
	if packages == nil {
		return false
	}
	if origin, ok := c.prov[fn].(FromAST); ok {
		return packages.isNpmSource(origin.Node.Span().SourceID)
	}
	if decl, ok := c.info.NamespaceMemberDecl(e.Callee); ok {
		return packages.isNpmSource(decl.Span().SourceID)
	}
	if ident, ok := e.Callee.(*ast.IdentExpr); ok {
		if decl, ok := c.info.DeclOf(ident); ok {
			return packages.isNpmSource(decl.Span().SourceID)
		}
	}
	if object, _ := c.calleeReceiver(e.Callee); object != nil {
		if class, ok := c.classCarrier(peelBorrows(c.info.TypeOf(object))); ok {
			return packages.isNpmKey(class.Name)
		}
	}
	return false
}
