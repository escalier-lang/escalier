package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
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

// sharedFlow is one place a value enters a local: the locals targets name each receive the
// value source produces.
type sharedFlow struct {
	targets []liveness.VarID
	source  ast.Expr
}

// sharedFlowCollector gathers every sharedFlow of one function body. It stops at a nested
// function, whose body runs its own pre-pass.
type sharedFlowCollector struct {
	ast.DefaultVisitor
	flows []sharedFlow
}

func (v *sharedFlowCollector) add(pat ast.Pat, source ast.Expr) {
	if source == nil {
		return
	}
	var targets []liveness.VarID
	ast.ForEachLeafBinding(pat, func(_ string, varID int) {
		if varID > 0 {
			targets = append(targets, liveness.VarID(varID))
		}
	})
	if len(targets) > 0 {
		v.flows = append(v.flows, sharedFlow{targets: targets, source: source})
	}
}

func (v *sharedFlowCollector) EnterDecl(d ast.Decl) bool {
	switch d := d.(type) {
	case *ast.VarDecl:
		v.add(d.Pattern, d.Init)
	case *ast.FuncDecl:
		return false
	}
	return true
}

func (v *sharedFlowCollector) EnterStmt(s ast.Stmt) bool {
	if s, ok := s.(*ast.ForInStmt); ok {
		v.add(s.Pattern, s.Iterable)
	}
	return true
}

func (v *sharedFlowCollector) EnterExpr(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.FuncExpr:
		return false
	case *ast.BinaryExpr:
		// A store into a field of a local puts the value inside that local, so the whole
		// local takes the flow. `a.inner = xs` makes `a.inner` a second name for `xs`.
		if e.Op == ast.Assign {
			if p, ok := exprPlace(e.Left); ok {
				v.flows = append(v.flows, sharedFlow{targets: []liveness.VarID{p.root}, source: e.Right})
			}
		}
	case *ast.IfValExpr:
		v.add(e.Pattern, e.Target)
	case *ast.MatchExpr:
		for _, mc := range e.Cases {
			v.add(mc.Pattern, e.Target)
		}
	}
	return true
}

// collectSharedLocals sets the body's shared locals to the VarIDs of its bindings that may hold
// a shared value.
// It runs after the rename pass, so every binding in body carries its VarID, and before the
// body is walked. scope is the scope the body's parameters bind into.
//
// The result starts from the body's captures and grows to a fixpoint over every binding,
// assignment, loop variable, and pattern in body. A local joins it when any value flowing
// into it may be shared. Each flow is visited regardless of its position in the body, so a
// reassignment later in a loop still marks a local read earlier in the loop.
func (c *checker) collectSharedLocals(scope *Scope, body *ast.Block) {
	c.fn.sharedLocals = c.fn.captureVarIDs.Clone()
	collector := &sharedFlowCollector{}
	body.Accept(collector)
	for changed := true; changed; {
		changed = false
		for _, f := range collector.flows {
			if !c.mayYieldShared(scope, f.source) {
				continue
			}
			for _, id := range f.targets {
				if !c.fn.sharedLocals.Contains(id) {
					c.fn.sharedLocals.Add(id)
					changed = true
				}
			}
		}
	}
}

// markSharedLeaves adds each leaf binding of pat to the body's shared locals when source may
// yield a shared value. It is a no-op outside a function body.
func (c *checker) markSharedLeaves(scope *Scope, pat ast.Pat, source ast.Expr) {
	if c.fn == nil || c.fn.sharedLocals == nil || source == nil || !c.mayYieldShared(scope, source) {
		return
	}
	ast.ForEachLeafBinding(pat, func(_ string, varID int) {
		if varID > 0 {
			c.fn.sharedLocals.Add(liveness.VarID(varID))
		}
	})
}

// mayYieldShared reports whether the value e produces may be a shared value. It answers
// true when e may be one of these:
//
//   - a binding outside the frame, or a local in the body's shared locals;
//   - a field or element read off such a value;
//   - a literal holding such a value;
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
		if scope != nil {
			if b, found := c.lookupValueBinding(scope, e.Name); found {
				if t := bindingType(b); t != nil && isValueType(t) {
					return false
				}
			}
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
				if c.mayYieldShared(scope, elem.Value) {
					return true
				}
			}
		}
		return false
	case *ast.TupleExpr:
		for _, elem := range e.Elems {
			if c.mayYieldShared(scope, elem) {
				return true
			}
		}
		return false
	case *ast.ArraySpreadExpr:
		return c.mayYieldShared(scope, e.Value)
	case *ast.IfElseExpr:
		return c.mayYieldShared(scope, blockResult(&e.Cons)) || c.mayYieldShared(scope, blockOrExprResult(e.Alt))
	case *ast.IfValExpr:
		return c.mayYieldShared(scope, blockResult(&e.Cons)) || c.mayYieldShared(scope, blockOrExprResult(e.Alt))
	case *ast.MatchExpr:
		return c.anyCaseYieldsShared(scope, e.Cases)
	case *ast.DoExpr:
		return c.mayYieldShared(scope, blockResult(&e.Body))
	case *ast.TryCatchExpr:
		return c.mayYieldShared(scope, blockResult(&e.Try)) || c.anyCaseYieldsShared(scope, e.Catch)
	case *ast.CallExpr:
		return c.callReturnsShared(scope, e)
	}
	return false
}

// anyCaseYieldsShared reports whether the body of any case in cases may yield a shared value.
func (c *checker) anyCaseYieldsShared(scope *Scope, cases []*ast.MatchCase) bool {
	for _, mc := range cases {
		if c.mayYieldShared(scope, blockOrExprResult(&mc.Body)) {
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

// blockResult returns the expression a value-position block produces, or nil when the block
// is empty or ends in a statement that is not an expression.
func blockResult(b *ast.Block) ast.Expr {
	if b == nil || len(b.Stmts) == 0 {
		return nil
	}
	if s, ok := b.Stmts[len(b.Stmts)-1].(*ast.ExprStmt); ok {
		return s.Expr
	}
	return nil
}

// blockOrExprResult returns the expression boe produces, or nil when boe is nil or its block
// produces none.
func blockOrExprResult(boe *ast.BlockOrExpr) ast.Expr {
	if boe == nil {
		return nil
	}
	if boe.Expr != nil {
		return boe.Expr
	}
	return blockResult(boe.Block)
}
