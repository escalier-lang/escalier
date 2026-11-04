package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
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
//   - It emits the capture's ownership events.
//   - A module-level name a module's function captures cannot move, so a closure that moves
//     one is a ModuleLevelMoveError.
//   - A capture the closure does not move joins capturedLocals. A binding that holds the
//     closure takes a capture edge to the local in the borrow graph from there.
//
// A capture whose type has value semantics records no more than its events, since a closure
// holding a primitive cannot see a later change to the binding it copied. A capture from a
// body further out records nothing, since this body's tables do not track it.
func (c *checker) recordClosureCaptures(scope *Scope, closure ast.Func) {
	if c.fn == nil {
		return
	}
	_, hasRef := c.currentStmtRef()
	moved := c.capturesMovedBy[closure]
	for _, capture := range c.closureCaptures(closure) {
		b, found := scope.GetValue(capture.Name)
		if !found || b.VarID <= 0 {
			continue
		}
		root := liveness.VarID(b.VarID)
		if name, ok := c.fn.varIDNames[root]; !ok || name != capture.Name {
			continue
		}
		if eventRef, ok := c.eventStmtRef(); ok {
			c.emitCaptureFlows(closure, root, bindingDecl(b), bindingType(b), capture.IsMutable, moved[capture.Name], eventRef)
		}
		if isValueType(bindingType(b)) {
			continue
		}
		// The error blames the expression in the closure that moves the local.
		if moveNode, ok := moved[capture.Name]; ok {
			if c.fn.capturesModule && c.fn.captureVarIDs.Contains(root) {
				c.reportModuleLevelMove(moveNode)
				continue
			}
			// The closure takes the local where it is written, so no binding that holds the
			// closure reaches the local afterward. A closure inside a statement with no CFG
			// point of its own keeps the edge.
			if hasRef {
				continue
			}
		}
		if c.fn.capturedLocals == nil {
			c.fn.capturedLocals = map[ast.Func][]capturedLocal{}
		}
		local := capturedLocal{root: root, mut: capture.IsMutable, t: bindingType(b), paths: c.capturedFieldPaths(closure, bindingDecl(b))}
		c.fn.capturedLocals[closure] = append(c.fn.capturedLocals[closure], local)
	}
}

// capturedFieldPaths returns the field paths under the local declared by decl that closure
// reads or writes, as capturedPaths finds them. It returns nil when the closure captures the
// whole local, and when the body has no event table to find the paths with.
func (c *checker) capturedFieldPaths(closure ast.Func, decl ast.Node) [][]placeSeg {
	if c.eventTable() == nil {
		return nil
	}
	var out [][]placeSeg
	for _, cp := range c.capturedPaths(closure, decl) {
		if len(cp.path) == 0 {
			return nil
		}
		out = append(out, cp.path)
	}
	return out
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
