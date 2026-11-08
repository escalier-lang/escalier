package solver

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
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

// shortFuncLifetimes returns the lifetimes retentionLifetimes finds in t other than 'static. A
// function whose lifetime is 'static outlives any closure that captures it. A nil t holds
// none.
func shortFuncLifetimes(t soltype.Type) []soltype.Lifetime {
	if t == nil {
		return nil
	}
	return slices.DeleteFunc(retentionLifetimes(t), isStaticFuncLt)
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
