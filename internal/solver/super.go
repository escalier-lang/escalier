package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// superCtx is what a `super(…)` call needs while a constructor body is walked. super is the
// class whose constructor the call runs, nil when the enclosing class declares no
// superclass. A nil superCtx means the walk is not inside a constructor at all.
type superCtx struct {
	super *soltype.ClassType
}

// inferSuperCall types a `super(…)` call. The call runs the superclass constructor and
// yields no value of its own, so it infers `undefined` whatever else it reports.
//
// The arguments are checked by constraining the superclass constructor against a call shape
// built from them, so a per-argument mismatch reads like the one a direct call to the
// superclass gives. A wrong argument count reports the arity of the two signatures instead
// of the too-few lint, which fires only on a direct call.
//
// The call shape returns the superclass instance the `extends` clause names, arguments and
// all, which is what fixes the superclass's type parameters at the arguments the edge
// declares. Under `class Dog extends Animal<string>`, `super(5)` then reports `5` against
// `string` rather than resolving `Animal<A>`'s `A` to `5` and checking clean.
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
		if ctor, ok := c.superConstructor(scope, lvl, c.superCtx.super); ok {
			c.constrain(e, ctor, &soltype.FuncType{Params: args, Ret: c.superCtx.super})
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

// superVarID is the synthetic binding the move dataflow tracks in place of "the superclass
// constructor has run". Nothing else is tracked, so the one id is the whole domain.
const superVarID = liveness.VarID(1)

// checkSuperCalls reports the rules a constructor body as a whole has to satisfy, once the
// body has been walked.
//
// A subclass constructor must run its superclass's exactly once on every path, before it
// touches `self`. Until it has, the members the class inherits do not exist, so a read sees
// nothing and a write lands on an object the superclass constructor may overwrite. JS
// enforces the same order at runtime by rejecting `this` before `super()`.
//
// "Every path" and "before" are decided by the same forward move-state dataflow that
// checkConstructorInit uses for definite assignment, over the same CFG. A `super(…)` is
// modeled as a move of one synthetic binding, so at any program point its state answers the
// question the rules ask:
//
//   - Moved means every reaching path called it. That is what the exit needs, and what a
//     mention of `self` needs.
//   - NotMoved means no reaching path called it. That is what a call site needs.
//   - MaybeMoved means some paths called it and others did not, which fails both.
//
// Branches therefore work the way JS allows: a call in each arm of an `if`/`else` leaves the
// join Moved and is accepted, while a call in an `if` alone leaves it MaybeMoved.
//
// A throwing path is exempt, the way it is for definite assignment. It never finishes
// constructing an instance, so it marks the call made rather than lowering the state at the
// exit join.
func (c *checker) checkSuperCalls(ctx *superCtx, ctor *ast.ConstructorElem) {
	if ctx.super == nil || ctor.Fn == nil || ctor.Fn.Body == nil {
		return
	}

	cfg := liveness.BuildCFG(*ctor.Fn.Body)
	col := &superCollector{gens: map[liveness.StmtRef]set.Set[liveness.VarID]{}}
	// Walk each CFG block's statements. The builder has already flattened control flow into
	// blocks, so the per-statement scan attributes every call and every mention of `self` to
	// the right program point without re-deriving the branch structure.
	for _, block := range cfg.Blocks {
		for idx, stmt := range block.Stmts {
			col.currentRef = liveness.StmtRef{BlockID: block.ID, StmtIdx: idx}
			stmt.Accept(col)
		}
	}
	info := liveness.AnalyzeMoves(cfg, col.gens)

	// StmtIdx -1 reads the exit block's entry state, the join over every predecessor.
	exitRef := liveness.StmtRef{BlockID: cfg.Exit.ID, StmtIdx: -1}
	switch info.StateBefore(exitRef, superVarID) {
	case liveness.NotMoved:
		// No path calls it. Every mention of `self` in the body is downstream of that one
		// fact, so reporting them too would bury it.
		c.report(&MissingSuperCallError{Class: ctx.super.Name, Node: ctor})
		return
	case liveness.MaybeMoved:
		c.report(&ConditionalSuperCallError{Class: ctx.super.Name, Node: ctor})
	}

	// A call some path reaches with the superclass constructor already run is a second call
	// on that path.
	for _, call := range col.calls {
		if info.StateBefore(call.ref, superVarID) != liveness.NotMoved {
			c.report(&MultipleSuperCallsError{Node: call.node})
		}
	}

	for _, use := range col.selfUses {
		if info.StateBefore(use.ref, superVarID) != liveness.Moved {
			c.report(&SuperCallAfterSelfError{Node: use.node})
		}
	}
}

// superSite is one `super(…)` call or one mention of `self` the collector recorded: the CFG
// point it sits at and the node to blame.
type superSite struct {
	ref  liveness.StmtRef
	node ast.Node
}

// superCollector walks a constructor body statement by statement, recording each `super(…)`
// as a "gen" of the synthetic binding at the current program point and each mention of
// `self` as a use. currentRef is set by the driver before each statement, so a call or
// mention nested in that statement's expression is attributed to it.
type superCollector struct {
	ast.DefaultVisitor
	currentRef liveness.StmtRef
	gens       map[liveness.StmtRef]set.Set[liveness.VarID]
	calls      []superSite
	selfUses   []superSite
}

// gen records that the superclass constructor has run as of the current statement.
func (col *superCollector) gen() {
	at := col.gens[col.currentRef]
	if at == nil {
		at = set.NewSet[liveness.VarID]()
		col.gens[col.currentRef] = at
	}
	at.Add(superVarID)
}

func (col *superCollector) EnterExpr(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.FuncExpr:
		// A nested closure has its own body and CFG, and its `self` does not run in the
		// constructor's straight-line flow, so do not descend into it.
		return false
	case *ast.SuperCallExpr:
		// The arguments run before the superclass constructor does, so scan them for
		// mentions of `self` first, then mark the call made.
		for _, arg := range e.Args {
			arg.Accept(col)
		}
		col.calls = append(col.calls, superSite{ref: col.currentRef, node: e})
		col.gen()
		return false
	case *ast.ThrowExpr:
		// A throwing path never completes construction, so it is exempt: mark the call made
		// so the throw does not lower the state at the exit join. The thrown value is still
		// scanned, since it runs before the throw.
		col.gen()
		if e.Arg != nil {
			e.Arg.Accept(col)
		}
		return false
	case *ast.IdentExpr:
		if e.Name == "self" {
			col.selfUses = append(col.selfUses, superSite{ref: col.currentRef, node: e})
		}
	}
	return true
}
