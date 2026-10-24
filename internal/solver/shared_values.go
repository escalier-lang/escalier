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
//   - a call of a function whose body returns such a value;
//   - a call of a namespace member that readonlyViewCallee accepts, whatever its type.
//
// Apart from that last case, a value that copies, such as a number, is never shared. Once e
// is walked its recorded type decides that. Before then, as when collectSharedLocals runs,
// only a binding outside the frame is known to copy, so a local read is judged by its flows
// alone. scope resolves a name outside the frame.
func (c *checker) mayYieldShared(scope *Scope, e ast.Expr, shared set.Set[liveness.VarID]) bool {
	if e == nil {
		return false
	}
	// A call that hands out a readonly view is judged before its type. Given
	// `maybeMake(): readonly number[] | undefined`, the call `d.maybeMake()` has type
	// `Array<number> | undefined`, which isOwnedMovable declines. Yet `if val a = d.maybeMake()`
	// binds `a` to the package's own array.
	if call, ok := e.(*ast.CallExpr); ok && c.readonlyViewCallee(scope, call.Callee) {
		return true
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
// returns a shared value, or a local bound to a namespace member that readonlyViewCallee
// accepts. The callee resolves through the declaration the walk recorded for it, or through
// scope when the walk has not reached it and scope is non-nil. A callee reached any other
// way, such as a method or a namespace member, answers false.
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
// function expression. A `val`/`var` declaration whose initializer is a namespace member the
// walk recorded in readonlyViewCallees answers true as well, so `val mk = d.make` names what
// `d.make` does.
func (c *checker) funcReturnsShared(decl ast.Node) bool {
	if vd, ok := decl.(*ast.VarDecl); ok {
		if vd.Init == nil {
			return false
		}
		decl = vd.Init
	}
	return c.sharedReturns.Contains(decl) || c.librarySharedReturns.Contains(decl) ||
		c.readonlyViewCallees.Contains(decl) || c.libraryReadonlyViewCallees.Contains(decl)
}

// readonlyViewCallee reports whether callee is a namespace member whose binding
// bindingReturnsReadonlyView accepts. A callee the walk has read answers from
// readonlyViewCallees. One it has not resolves through scope when scope is non-nil, which
// reaches a member written as an identifier chain such as `d.make` or `d.ns.make`.
func (c *checker) readonlyViewCallee(scope *Scope, callee ast.Expr) bool {
	if c.readonlyViewCallees.Contains(callee) {
		return true
	}
	member, ok := callee.(*ast.MemberExpr)
	if scope == nil || !ok || member.Prop == nil {
		return false
	}
	ns, ok := c.namespaceOf(scope, member.Object)
	if !ok {
		return false
	}
	// The member resolves the way resolveNamespaceMember resolves it, where a nested
	// namespace of the same name wins over the value.
	if _, nested := ns.Nested[member.Prop.Name]; nested {
		return false
	}
	b, ok := ns.Values[member.Prop.Name]
	return ok && len(b.Schemes) > 0 && c.bindingReturnsReadonlyView(ns, b)
}

// namespaceOf returns the namespace e names in scope, and false when e names a value or
// nothing. e is an identifier or a member chain of identifiers, such as `d` or `d.ns`.
func (c *checker) namespaceOf(scope *Scope, e ast.Expr) (*Namespace, bool) {
	switch e := e.(type) {
	case *ast.IdentExpr:
		// The rename pass numbers only a local, and a local is a value.
		if e.VarID > 0 {
			return nil, false
		}
		// A value binding wins over a namespace of the same name, as resolveIdentPath reads it.
		if b, ok := c.lookupValueBinding(scope, e.Name); ok && len(b.Schemes) > 0 {
			return nil, false
		}
		return scope.GetNamespace(e.Name)
	case *ast.MemberExpr:
		if e.Prop == nil {
			return nil, false
		}
		outer, ok := c.namespaceOf(scope, e.Object)
		if !ok {
			return nil, false
		}
		nested, ok := outer.Nested[e.Prop.Name]
		return nested, ok
	}
	return nil, false
}

// bindingReturnsReadonlyView reports whether some declaration among b's sources is a function
// whose declared return type names a readonly collection, at any depth. b is a member of ns,
// and a reference to a type ns exports is followed into that type's declaration. Each
// overload arm is one source, so the answer covers every arm. A source is a `fn`
// declaration, or a `val` or `var` declaration annotated with a function type. The answer for
// each source is kept in readonlyViewDecls, so a source is walked once.
func (c *checker) bindingReturnsReadonlyView(ns *Namespace, b ValueBinding) bool {
	for _, src := range b.Sources {
		np, ok := src.(*ast.NodeProvenance)
		if !ok {
			continue
		}
		found, known := c.readonlyViewDecls[np.Node]
		if !known {
			found = namesReadonlyView(ns, declaredReturn(np.Node))
			c.readonlyViewDecls[np.Node] = found
		}
		if found {
			return true
		}
	}
	return false
}

// declaredReturn returns the return type annotation decl declares, or nil when it declares
// none. decl is a `fn` declaration, or a `val` or `var` declaration annotated with a function
// type.
func declaredReturn(decl ast.Node) ast.TypeAnn {
	switch decl := decl.(type) {
	case *ast.FuncDecl:
		return decl.Return
	case *ast.VarDecl:
		if fn, ok := decl.TypeAnn.(*ast.FuncTypeAnn); ok {
			return fn.Return
		}
	}
	return nil
}

// namesReadonlyView reports whether ta holds a type reference marked ReadonlyView. A
// reference by bare name to a type alias or interface ns exports is followed into its
// declaration. It does not look inside a function type, including the type of a method or a
// getter. A function produces its readonly collections when called, so ta's value does not
// hold them.
func namesReadonlyView(ns *Namespace, ta ast.TypeAnn) bool {
	if ta == nil {
		return false
	}
	v := &readonlyViewFinder{types: ns.Types, followed: set.NewSet[string]()}
	ta.Accept(v)
	return v.found
}

// readonlyViewFinder records whether the type annotation it walks holds a type reference
// marked ReadonlyView. It skips function types.
//
//   - types holds the types a bare reference resolves to.
//   - followed holds every name already followed into its declaration, which stops a cycle.
type readonlyViewFinder struct {
	ast.DefaultVisitor
	types    map[string]TypeBinding
	followed set.Set[string]
	found    bool
}

func (v *readonlyViewFinder) EnterTypeAnn(t ast.TypeAnn) bool {
	switch t := t.(type) {
	case *ast.TypeRefTypeAnn:
		if t.ReadonlyView {
			v.found = true
		} else if id, ok := t.Name.(*ast.Ident); ok {
			v.follow(id.Name)
		}
	case *ast.FuncTypeAnn:
		return false
	}
	return !v.found
}

// follow walks the declaration of the type alias or interface that name resolves to in
// v.types, once per name.
func (v *readonlyViewFinder) follow(name string) {
	b, ok := v.types[name]
	if !ok || v.followed.Contains(name) {
		return
	}
	v.followed.Add(name)
	for _, src := range b.Sources {
		np, ok := src.(*ast.NodeProvenance)
		if !ok {
			continue
		}
		switch decl := np.Node.(type) {
		case *ast.TypeDecl:
			decl.TypeAnn.Accept(v)
		case *ast.InterfaceDecl:
			decl.TypeAnn.Accept(v)
			for _, ext := range decl.Extends {
				ext.Accept(v)
			}
		}
	}
}
