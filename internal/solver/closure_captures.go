package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// This file records what a closure captures from the body it is written in. A closure that
// moves a local it captures takes that local when the closure is written, as a Rust `move`
// closure does. The closure owns the value from then on, and its body moves it out on the
// first call, so a call of the closure moves the binding it is stored in, as D2 decides.
//
//	fn f() {
//		val a: Array<number> = [1]
//		val g = fn() -> Array<number> { return a }  // moves `a` into `g`
//		val m = g()                                 // moves `g`
//		val n = g()                                 // ERROR: use of moved value 'g'
//	}

// recordClosureCaptures records each local of the current body that `closure` captures, for
// the scope the closure is written in:
//
//   - It emits the capture's ownership events. A closure that moves a module-level name its
//     module's function captures is a ModuleLevelMoveError, which the move event reports.
//   - It records the type of the local's binding in the event table's captureTypes.
//
// A capture whose type has value semantics records no more than its events, since a closure
// holding a primitive cannot see a later change to the binding it copied. A capture from a
// body further out records nothing, since this body's tables do not track it.
func (c *checker) recordClosureCaptures(scope *Scope, closure ast.Func) {
	// A closure written at module top level has no enclosing body. No body tables exist to
	// record its captures in.
	if c.fn == nil {
		return
	}

	// moved maps each captured name the closure's body moves to the expression that moves it.
	moved := c.capturesMovedBy[closure]

	for _, capture := range c.closureCaptures(closure) {
		// Resolve the captured name to its binding in the scope the closure is written in. A
		// name with no VarID is not a local the liveness pass tracks.
		b, found := scope.GetValue(capture.Name)
		if !found || b.VarID <= 0 {
			continue
		}

		// Skip a capture declared in a body further out. Each body numbers its own locals, so
		// the VarID of an outer local either is missing from this body's names or names a
		// different local here.
		root := liveness.VarID(b.VarID)
		if name, ok := c.fn.varIDNames[root]; !ok || name != capture.Name {
			continue
		}

		// Emit the ownership events for the capture. The dataflow pass reads them to check
		// the closure's borrow of the local against other uses of it, and to apply the move
		// of a local the closure's body moves.
		if eventRef, ok := c.eventStmtRef(); ok {
			c.emitCaptureFlows(closure, root, bindingDecl(b), bindingType(b), capture.IsMutable, moved[capture.Name], eventRef)
		}

		// A closure that captures a value such as a `number` holds its own copy. A later
		// change to the local cannot reach it, so nothing more is recorded.
		if isValueType(bindingType(b)) {
			continue
		}

		// The dataflow pass reads the type to decide whether a retained closure writes an
		// owned local.
		if t := c.eventTable(); t != nil {
			t.captureTypes[root] = bindingType(b)
		}
	}
}

// setClosureRetention gives ft, the type of `closure`, a retention lifetime when the closure
// holds a borrow, so its type reads `&'a fn`. The closure holds a borrow when it captures one
// of these from a body it is written in:
//
//   - A `var` binding, or a binding it writes. Each call reads or writes the binding itself.
//   - A binding whose value is not a value type, such as an object. The closure reads it
//     through a borrow.
//   - A value that holds a function with a retention lifetime, in a union, an object property,
//     or a type argument, as retentionLifetimes finds. That function's lifetime must outlive
//     the closure's.
//
// A binding of a value type in a `val` is copied into the closure, and a module-level binding
// lives for the whole program, so neither makes the closure hold a borrow. The lifetime is
// fresh at lvl. ft must be the closure's own type, not yet shared with any other type.
//
// When each borrow the closure holds is of a local of the body it is written in, the lifetime
// joins that body's frameClosures, for ownReturnedClosures to read.
func (c *checker) setClosureRetention(scope *Scope, lvl int, closure ast.Func, ft *soltype.FuncType) {
	retain := func() {
		if ft.Lt == nil {
			ft.Lt = c.ctx.freshLifetime(lvl)
		}
	}
	framed := c.fn != nil
	for _, capture := range c.closureCaptures(closure) {
		b, found := scope.GetValue(capture.Name)
		if !found || len(b.Schemes) != 1 || b.ModuleLevel || b.VarID <= 0 {
			continue
		}
		if b.Kind == ast.VarKind || capture.IsMutable || c.capturesByBorrow(bindingType(b)) {
			retain()
			// A returned closure takes a local it captures, but not the referent of a borrow
			// the local holds.
			_, borrowed := bindingType(b).(*soltype.RefType)
			framed = framed && !borrowed && c.isFrameLocal(capture.Name, b)
		}
		// Instantiating a generalized binding mints fresh variables, so a binding whose type
		// holds no retained function is skipped before that. Instantiation keeps the shape of
		// the type, so it cannot add one.
		if body := schemeBody(b.Schemes[0]); body == nil || len(retentionLifetimes(body)) == 0 {
			continue
		}
		for _, lt := range retentionLifetimes(c.bindingValue(lvl, b)) {
			retain()
			c.ctx.constrainLt(lt, ft.Lt)
			framed = false
		}
	}
	if lt, isVar := ft.Lt.(*soltype.LifetimeVar); isVar && framed {
		if c.fn.frameClosures == nil {
			c.fn.frameClosures = set.NewSet[*soltype.LifetimeVar]()
		}
		c.fn.frameClosures.Add(lt)
	}
}

// isFrameLocal reports whether b, the binding name resolves to, is a local of the body being
// walked rather than of a body further out. Each body numbers its own locals, so the VarID of
// an outer local either is missing from this body's names or names a different local here.
func (c *checker) isFrameLocal(name string, b ValueBinding) bool {
	local, ok := c.fn.varIDNames[liveness.VarID(b.VarID)]
	return ok && local == name
}

// noteFrameClosureRead adds the retention lifetime of t, the value a read of b resolved to, to
// the current body's frameClosures when b holds a closure whose lifetime is already there.
// Reading a generalized binding copies its type with fresh lifetimes, and the copy is the same
// closure.
func (c *checker) noteFrameClosureRead(b ValueBinding, t soltype.Type) {
	if c.fn == nil || c.fn.frameClosures == nil || len(b.Schemes) != 1 {
		return
	}
	held, isFn := schemeBody(b.Schemes[0]).(*soltype.FuncType)
	if !isFn {
		return
	}
	heldLt, isVar := held.Lt.(*soltype.LifetimeVar)
	if !isVar || !c.fn.frameClosures.Contains(heldLt) {
		return
	}
	if read, isFn := t.(*soltype.FuncType); isFn {
		if readLt, isVar := read.Lt.(*soltype.LifetimeVar); isVar {
			c.fn.frameClosures.Add(readLt)
		}
	}
}

// ownReturnedClosures drops the retention lifetime of each closure the current body returns
// whose lifetime is in the body's frameClosures. Such a closure borrows only locals of the
// body, and a returned closure takes what it captures, as F3 states, so it holds no borrow
// once it is returned. A closure returned through a local, as in `return f` after
// `val f = fn () { … }`, is reached through the lower bounds of the local's type. It must run
// before the returns are joined into the function's result.
func (c *checker) ownReturnedClosures() {
	if c.fn.frameClosures == nil {
		return
	}
	v := &returnedClosureOwner{frame: c.fn.frameClosures, vars: set.NewSet[*soltype.TypeVarType]()}
	for _, t := range c.fn.returns {
		if t != nil {
			t.Accept(v, soltype.Positive)
		}
	}
}

// returnedClosureOwner is the visitor ownReturnedClosures runs. It clears the retention
// lifetime of each closure type it reaches in place, since the closure's own type is the one
// the return holds.
type returnedClosureOwner struct {
	frame set.Set[*soltype.LifetimeVar]
	vars  set.Set[*soltype.TypeVarType]
}

func (v *returnedClosureOwner) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.TypeVarType:
		if !v.vars.Contains(t) {
			v.vars.Add(t)
			for _, b := range t.LowerBounds {
				b.Accept(v, pol)
			}
		}
	case *soltype.FuncType:
		if lt, isVar := t.Lt.(*soltype.LifetimeVar); isVar && v.frame.Contains(lt) {
			t.Lt = nil
		}
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (*returnedClosureOwner) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// capturesByBorrow reports whether a closure reads a captured `val` binding of type t through a
// borrow. A value type such as a primitive or a promise is copied into the closure instead. A
// function is a value type too, and a retention lifetime on its type decides whether it holds a
// borrow.
func (c *checker) capturesByBorrow(t soltype.Type) bool {
	switch t := t.(type) {
	case nil, *soltype.FuncType:
		return false
	case *soltype.ClassType:
		return t.Name != c.ctx.promiseClass
	}
	return !isValueType(t)
}

// movedCaptures maps the name of each local of the enclosing body that the current closure
// moves, wholly or in part, to the expression that moves it. A closure's captures are
// parameters of its body, so the closure moves one where its body's settled events move a
// place under it. Only a capture of a concrete owned type counts. The first move of a capture
// in the order the walk emitted them is the one returned. The current body must be a closure
// whose events are settled, since its captures are locals of the body that encloses it.
func (c *checker) movedCaptures() map[string]ast.Node {
	t := c.eventTable()
	if t == nil {
		return nil
	}

	var moved map[string]ast.Node
	for _, ev := range t.list() {
		mv, ok := ev.(*moveEvent)
		if !ok {
			continue
		}
		root := mv.place.root
		// A capture whose type is a bare type variable may hold a value type, so the closure
		// is not taken to move it.
		if !c.fn.captureVarIDs.Contains(root) || !c.isConcreteOwned(c.fn.varIDTypes[root]) {
			continue
		}
		name := c.fn.varIDNames[root]
		if _, seen := moved[name]; seen {
			continue
		}
		if moved == nil {
			moved = map[string]ast.Node{}
		}
		moved[name] = mv.node
	}
	return moved
}

// funcMovesCaptures reports whether decl declares a closure whose body moves a local it
// captures. decl is a script's `fn` declaration, or a `val`/`var` declaration whose initializer
// is the function expression.
func (c *checker) funcMovesCaptures(decl ast.Node) bool {
	if vd, ok := decl.(*ast.VarDecl); ok {
		decl = vd.Init
	}
	fn, ok := decl.(ast.Func)
	return ok && len(c.capturesMovedBy[fn]) > 0
}
