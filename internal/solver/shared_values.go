package solver

import (
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

// sharedFlow is one place a value enters a local. Each local in targets receives the value
// source produces.
type sharedFlow struct {
	targets []liveness.VarID
	source  ast.Expr
}

// sharedFlowCollector gathers every sharedFlow of one function body. It stops at a nested
// function, whose body runs its own pre-pass.
//
// valueLocals holds each `val` or `var` binding with no annotation whose initializer is a
// primitive literal, such as `var n = 0`. Its type is that primitive, so every value it
// holds is a copy and never shared.
type sharedFlowCollector struct {
	ast.DefaultVisitor
	flows       []sharedFlow
	valueLocals set.Set[liveness.VarID]
}

func (v *sharedFlowCollector) add(targets []liveness.VarID, source ast.Expr) {
	if source != nil && len(targets) > 0 {
		v.flows = append(v.flows, sharedFlow{targets: targets, source: source})
	}
}

func (v *sharedFlowCollector) EnterDecl(d ast.Decl) bool {
	switch d := d.(type) {
	case *ast.VarDecl:
		v.add(patVarIDs(d.Pattern), d.Init)
		if pat, ok := d.Pattern.(*ast.IdentPat); ok && pat.VarID > 0 && d.TypeAnn == nil {
			if _, ok := d.Init.(*ast.LiteralExpr); ok {
				v.valueLocals.Add(liveness.VarID(pat.VarID))
			}
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

// collectSharedLocals sets the body's shared locals to the VarIDs of its bindings that may
// hold a shared value. It runs after the rename pass, so every binding in body carries its
// VarID, and before the body is walked. scope is the scope the body's parameters bind into.
//
// The set starts from the body's captures and grows to a fixpoint over every binding,
// assignment, loop variable, and pattern in body. A local joins it when any value flowing
// into it may be shared. Each flow is visited regardless of its position in the body, so a
// reassignment later in a loop still marks a local read earlier in the loop.
func (c *checker) collectSharedLocals(scope *Scope, body *ast.Block) {
	c.fn.sharedLocals = c.fn.captureVarIDs.Clone()
	c.fn.sharedCaptureWrites = set.NewSet[string]()
	collector := &sharedFlowCollector{valueLocals: set.NewSet[liveness.VarID]()}
	body.Accept(collector)
	for changed := true; changed; {
		changed = false
		for _, f := range collector.flows {
			if !c.mayYieldShared(scope, f.source) {
				continue
			}
			for _, id := range f.targets {
				if !collector.valueLocals.Contains(id) && c.markShared(id) {
					changed = true
				}
			}
		}
	}
}

// markShared adds id to the body's shared locals and reports whether it was not there yet.
// When id is a capture, it also records the captured name in the body's capture writes.
func (c *checker) markShared(id liveness.VarID) bool {
	if c.fn.captureVarIDs.Contains(id) {
		c.fn.sharedCaptureWrites.Add(c.fn.varIDNames[id])
	}
	if c.fn.sharedLocals.Contains(id) {
		return false
	}
	c.fn.sharedLocals.Add(id)
	return true
}

// markSharedFlow marks each local in targets as shared when source may yield a shared value.
// It is a no-op outside a function body.
func (c *checker) markSharedFlow(scope *Scope, targets []liveness.VarID, source ast.Expr) {
	if c.fn == nil || c.fn.sharedLocals == nil || !c.mayYieldShared(scope, source) {
		return
	}
	for _, id := range targets {
		c.markShared(id)
	}
}

// markSharedCaptureWrites marks, in the current body, each local that names records a
// closure of this body storing a shared value into. scope is the scope the closure is
// declared in. A name that resolves to no local of the current body is skipped.
func (c *checker) markSharedCaptureWrites(scope *Scope, names set.Set[string]) {
	if c.fn == nil || c.fn.sharedLocals == nil {
		return
	}
	for name := range names {
		b, found := scope.GetValue(name)
		if !found || b.VarID <= 0 {
			continue
		}
		id := liveness.VarID(b.VarID)
		if c.fn.varIDNames[id] == name {
			c.markShared(id)
		}
	}
}

// mayYieldShared reports whether the value e produces may be a shared value. It answers
// true when e may be one of these:
//
//   - a binding outside the frame, or a local in the body's shared locals;
//   - a field or element read off such a value;
//   - a literal holding such a value, directly or through a spread;
//   - a branch result of a conditional, match, `do`, or `try` that is such a value;
//   - a call of a function whose body returns such a value.
//
// A value that copies, such as a number, is never shared. Once e is walked its recorded type
// decides that. Before then, as in the pre-pass, only a binding outside the frame is known to
// copy, so a local read is judged by its flows alone. scope resolves a name outside the frame
// and may be nil, which leaves a call resolved only by the declaration the walk recorded.
func (c *checker) mayYieldShared(scope *Scope, e ast.Expr) bool {
	if e == nil {
		return false
	}
	if t := c.info.TypeOf(e); t != nil && !c.isOwnedMovable(t) {
		return false
	}
	switch e := e.(type) {
	case *ast.IdentExpr:
		if e.VarID > 0 {
			return c.fn != nil && c.fn.sharedLocals.Contains(liveness.VarID(e.VarID))
		}
		if t := c.outerBindingType(scope, e); t != nil && isValueType(t) {
			return false
		}
		return true
	case *ast.MemberExpr:
		return c.mayYieldShared(scope, e.Object)
	case *ast.IndexExpr:
		return c.mayYieldShared(scope, e.Object)
	case *ast.TypeCastExpr:
		return c.mayYieldShared(scope, e.Expr)
	case *ast.AwaitExpr:
		return c.mayYieldShared(scope, e.Arg)
	case *ast.ObjectExpr:
		for _, elem := range e.Elems {
			switch elem := elem.(type) {
			case *ast.PropertyExpr:
				if c.mayYieldShared(scope, elem.ValueExpr()) {
					return true
				}
			case *ast.ObjSpreadExpr:
				if c.spreadYieldsShared(scope, elem.Value) {
					return true
				}
			}
		}
		return false
	case *ast.TupleExpr:
		for _, elem := range e.Elems {
			if spread, ok := elem.(*ast.ArraySpreadExpr); ok {
				if c.spreadYieldsShared(scope, spread.Value) {
					return true
				}
				continue
			}
			if c.mayYieldShared(scope, elem) {
				return true
			}
		}
		return false
	case *ast.IfElseExpr:
		return c.mayYieldShared(scope, liveness.BlockResultExpr(e.Cons)) ||
			c.mayYieldShared(scope, liveness.BlockOrExprResultExpr(e.Alt))
	case *ast.IfValExpr:
		return c.mayYieldShared(scope, liveness.BlockResultExpr(e.Cons)) ||
			c.mayYieldShared(scope, liveness.BlockOrExprResultExpr(e.Alt))
	case *ast.MatchExpr:
		return c.anyCaseYieldsShared(scope, e.Cases)
	case *ast.DoExpr:
		return c.mayYieldShared(scope, liveness.BlockResultExpr(e.Body))
	case *ast.TryCatchExpr:
		return c.mayYieldShared(scope, liveness.BlockResultExpr(e.Try)) || c.anyCaseYieldsShared(scope, e.Catch)
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
func (c *checker) spreadYieldsShared(scope *Scope, src ast.Expr) bool {
	var t soltype.Type
	if ident, ok := src.(*ast.IdentExpr); ok &&
		(ident.VarID <= 0 || c.fn.captureVarIDs.Contains(liveness.VarID(ident.VarID))) {
		t = c.outerBindingType(scope, ident)
	} else {
		t = c.info.TypeOf(src)
	}
	if t != nil && c.holdsOnlyValues(t) {
		return false
	}
	return c.mayYieldShared(scope, src)
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
func (c *checker) anyCaseYieldsShared(scope *Scope, cases []*ast.MatchCase) bool {
	for _, mc := range cases {
		if c.mayYieldShared(scope, liveness.BlockOrExprResultExpr(&mc.Body)) {
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
	return c.sharedReturns.Contains(decl)
}
