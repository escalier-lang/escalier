package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// superCtx is what a `super(…)` call needs while a constructor body is walked. super is the
// class whose constructor the call runs, nil when the enclosing class declares no
// superclass. calls collects every super call the body wrote, in walk order, so the rules
// that are about the body as a whole can be checked once the walk finishes.
type superCtx struct {
	super *soltype.ClassType
	calls []*ast.SuperCallExpr
}

// inferSuperCall types a `super(…)` call. The call runs the superclass constructor and
// yields no value of its own, so it infers `undefined` whatever else it reports.
//
// The arguments are checked by constraining the superclass constructor against a call shape
// built from them, which is how an ordinary call is checked, so arity and per-argument
// mismatches surface as the diagnostics a direct call to the superclass would give.
func (c *checker) inferSuperCall(scope *Scope, lvl int, e *ast.SuperCallExpr) soltype.Type {
	args := make([]*soltype.FuncParam, len(e.Args))
	for i, arg := range e.Args {
		args[i] = &soltype.FuncParam{Type: c.inferExpr(scope, lvl, arg)}
	}
	switch {
	case c.superCtx == nil:
		c.report(&SuperOutsideConstructorError{Node: e})
	case c.superCtx.super == nil:
		c.report(&SuperWithoutSuperclassError{Node: e})
	default:
		c.superCtx.calls = append(c.superCtx.calls, e)
		if ctor, ok := c.superConstructor(scope, lvl, c.superCtx.super); ok {
			c.constrain(e, ctor, &soltype.FuncType{Params: args, Ret: c.freshAt(lvl)})
		}
	}
	return &soltype.UndefinedType{}
}

// superConstructor returns the callable signature of a superclass's constructor, read off
// the class's value binding, which is the object `Animal(…)` calls through. It returns
// ok=false when the binding is absent or carries no constructor, which leaves the arguments
// unchecked rather than reported against a signature that was never found.
func (c *checker) superConstructor(scope *Scope, lvl int, super *soltype.ClassType) (*soltype.FuncType, bool) {
	binding, found := scope.GetValue(super.Name)
	if !found || len(binding.Schemes) == 0 {
		return nil, false
	}
	obj, ok := classValueCarrier(c.instantiate(binding.Schemes[0], lvl))
	if !ok {
		return nil, false
	}
	ctor, ok := obj.Constructor()
	if !ok {
		return nil, false
	}
	return ctor.Fn, true
}

// checkSuperCalls reports the rules a constructor body as a whole has to satisfy, once the
// body has been walked and every `super(…)` in it recorded.
//
// A subclass constructor must run its superclass's exactly once, before it touches `self`.
// Until it has, the members the class inherits do not exist, so a read sees nothing and a
// write lands on an object the superclass constructor may overwrite. JS enforces the same
// order at runtime by rejecting `this` before `super()`.
//
// The dominance rule is approximated by source position: the call has to be a statement of
// the constructor body itself, written before the first mention of `self`. A call nested
// inside an `if` reaches only some paths, so requiring the top level is what makes one
// written call mean one call on every path. This rejects a body that calls its superclass
// differently per branch, which is legal JS; splitting the arguments before the call covers
// the same ground.
func (c *checker) checkSuperCalls(ctx *superCtx, ctor *ast.ConstructorElem) {
	if ctx.super == nil {
		return
	}
	if len(ctx.calls) == 0 {
		c.report(&MissingSuperCallError{Class: ctx.super.Name, Node: ctor})
		return
	}
	if len(ctx.calls) > 1 {
		c.report(&MultipleSuperCallsError{Node: ctx.calls[1]})
		return
	}
	call := ctx.calls[0]
	if !isTopLevelStmt(ctor.Fn.Body, call) {
		c.report(&NestedSuperCallError{Node: call})
		return
	}
	if selfUse, found := firstSelfUse(ctor.Fn.Body); found && before(selfUse.Start, call.Span().Start) {
		c.report(&SuperCallAfterSelfError{Node: call, Self: selfUse})
	}
}

// before reports whether a comes earlier in the source than b.
func before(a, b ast.Location) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Column < b.Column
}

// isTopLevelStmt reports whether call is written as a statement of body itself rather than
// nested inside one of its statements.
func isTopLevelStmt(body *ast.Block, call *ast.SuperCallExpr) bool {
	if body == nil {
		return false
	}
	for _, stmt := range body.Stmts {
		if exprStmt, ok := stmt.(*ast.ExprStmt); ok && exprStmt.Expr == ast.Expr(call) {
			return true
		}
	}
	return false
}

// firstSelfUse returns the span of the earliest `self` the constructor body mentions.
func firstSelfUse(body *ast.Block) (ast.Span, bool) {
	if body == nil {
		return ast.Span{}, false
	}
	finder := &selfUseFinder{}
	for _, stmt := range body.Stmts {
		stmt.Accept(finder)
	}
	return finder.span, finder.found
}

// selfUseFinder records the earliest `self` identifier the walk reaches.
type selfUseFinder struct {
	ast.DefaultVisitor
	span  ast.Span
	found bool
}

func (v *selfUseFinder) EnterExpr(e ast.Expr) bool {
	if ident, ok := e.(*ast.IdentExpr); ok && ident.Name == "self" {
		if !v.found || before(e.Span().Start, v.span.Start) {
			v.span, v.found = e.Span(), true
		}
	}
	return true
}
