package solver

import (
	"slices"
	"strings"

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

// setClosureRetention gives ft, the type of `closure`, a lifetime when the closure holds a
// borrow, so its type reads `'a fn`. The closure holds a borrow when it captures one of these
// from a body it is written in:
//
//   - A `var` binding, or a binding it writes. Each call reads or writes the binding itself.
//   - A binding whose value is not a value type, such as an object. The closure reads it
//     through a borrow.
//   - A value that holds a function with a lifetime other than 'static, in a union, an object
//     property, or a type argument, as shortFuncLifetimes finds. That function's lifetime must
//     outlive the closure's.
//
// A binding of a value type in a `val` is copied into the closure, and a module-level binding
// lives for the whole program, so neither makes the closure hold a borrow. The lifetime is
// fresh at lvl. ft must be the closure's own type, not yet shared with any other type.
//
// The lifetime joins closureBorrows with the first capture that holds a borrow, for a
// FuncKeptForStaticError to name.
//
// A `val` binding whose type is still a type variable, such as an unannotated parameter, may
// turn out to hold a value type or an object. The closure takes a lifetime for it, and the
// lifetime joins tentativeClosures with the variables still undecided, for
// settleTentativeFuncEscapes to read once they settle.
//
// When each borrow the closure holds is of a local of the body it is written in, the lifetime
// joins that body's frameClosures, for ownReturnedClosures to read.
func (c *checker) setClosureRetention(scope *Scope, lvl int, closure ast.Func, ft *soltype.FuncType) {
	// borrowed is the first capture that holds a borrow.
	var borrowed closureBorrow
	retain := func(name string, b ValueBinding) {
		if ft.Lt == nil {
			ft.Lt = c.ctx.freshLifetime(lvl)
			// Declaring the binding at module level ends the borrow only when its value is
			// neither a borrow nor a function that borrows, and holds no borrow from outside
			// the body.
			_, isLocal := bindingDecl(b).(*ast.VarDecl)
			value := schemeBody(b.Schemes[0])
			_, isRef := value.(*soltype.RefType)
			local := isLocal && !c.inScript() && c.fn != nil && !isRef &&
				len(shortFuncLifetimes(value)) == 0 && c.holdsOnlyFrameBorrows(value)
			borrowed = closureBorrow{name: name, local: local}
		}
	}
	framed := c.fn != nil
	// takeable says each capture the closure borrows is a `val` or `var` of the body it is
	// written in, rather than a parameter or a binding of a script's top level.
	takeable := c.fn != nil && !c.inScript()
	// direct says some capture is borrowed by the closure itself, rather than only through a
	// function the capture holds.
	direct := false
	// taken holds the locals the closure borrows, as closureBorrow describes.
	var taken takenLocals
	// definite says some capture holds a borrow whatever its type settles to, and undecided
	// holds the type variables of the other captures that may hold one.
	definite := false
	var undecided []*soltype.TypeVarType
	for _, capture := range c.closureCaptures(closure) {
		b, found := scope.GetValue(capture.Name)
		// A binding with no VarID and no declaration of its own, such as a global the
		// prelude declares, is not a local. A parameter read by another parameter's default
		// has a declaration but no VarID yet, since the body's locals are numbered later.
		if !found || len(b.Schemes) != 1 || b.ModuleLevel || (b.VarID <= 0 && bindingDecl(b) == nil) {
			continue
		}
		// The scheme's own type keeps an unsolved variable as a variable, where the binding's
		// display type would render it as `never`.
		held := schemeBody(b.Schemes[0])
		v, isVar := held.(*soltype.TypeVarType)
		readOnly := b.Kind != ast.VarKind && !capture.IsMutable
		byBorrow := c.capturesByBorrow(held)
		var heldLts []soltype.Lifetime
		if held != nil {
			heldLts = shortFuncLifetimes(held)
		}
		direct = direct || !readOnly || isVar || byBorrow
		if readOnly && isVar {
			undecided = append(undecided, v)
		} else if !readOnly || byBorrow {
			definite = true
		}
		retained := len(heldLts) > 0
		if !readOnly || isVar || retained || byBorrow {
			retain(capture.Name, b)
			// A returned closure takes a local it captures, but not what a borrow or a closure
			// inside the local borrows from outside the body.
			framed = framed && c.isFrameLocal(capture.Name, b) && c.holdsOnlyFrameBorrows(held)
			_, isVarDecl := bindingDecl(b).(*ast.VarDecl)
			takeable = takeable && isVarDecl
			if b.VarID > 0 {
				taken.roots = append(taken.roots, liveness.VarID(b.VarID))
				if b.Kind == ast.VarKind {
					taken.vars = append(taken.vars, liveness.VarID(b.VarID))
				}
			}
			// A closure the capture holds borrows its own locals, which go with it.
			for _, lt := range heldLts {
				if lv, isLtVar := lt.(*soltype.LifetimeVar); isLtVar {
					inner := c.closureBorrows[lv].taken
					taken.roots = append(taken.roots, inner.roots...)
					taken.vars = append(taken.vars, inner.vars...)
				}
			}
		}
		// Instantiating a generalized binding mints fresh variables, so a binding whose type
		// holds no function with a lifetime is skipped before that. Instantiation keeps the shape of
		// the type, so it cannot add one.
		if !retained {
			continue
		}
		for _, lt := range shortFuncLifetimes(c.bindingValue(lvl, b)) {
			retain(capture.Name, b)
			c.ctx.constrainLt(lt, ft.Lt)
			definite = true
		}
	}
	if lt, isVar := ft.Lt.(*soltype.LifetimeVar); isVar && borrowed.name != "" {
		borrowed.viaFuncs = !direct
		borrowed.takeable = takeable && framed
		borrowed.taken = taken
		if c.closureBorrows == nil {
			c.closureBorrows = map[*soltype.LifetimeVar]closureBorrow{}
		}
		c.closureBorrows[lt] = borrowed
	}
	if lt, isVar := ft.Lt.(*soltype.LifetimeVar); isVar && !definite && len(undecided) > 0 {
		if c.tentativeClosures == nil {
			c.tentativeClosures = map[*soltype.LifetimeVar][]*soltype.TypeVarType{}
		}
		c.tentativeClosures[lt] = undecided
	}
	if lt, isVar := ft.Lt.(*soltype.LifetimeVar); isVar && framed {
		if c.fn.frameClosures == nil {
			c.fn.frameClosures = set.NewSet[*soltype.LifetimeVar]()
		}
		c.fn.frameClosures.Add(lt)
	}
}

// closureBorrow is the capture that makes a closure hold a borrow. name is the captured
// binding's name. local says the binding is a `val` or `var` of a function body, which could be
// declared at module level instead. viaFuncs says the closure holds a borrow only through
// functions it captures, whose lifetimes bound the closure's from below. takeable says every
// capture the closure borrows is a `val` or `var` of the body it is written in that holds no
// borrow from outside that body, so a slot that keeps the closure for 'static can take those
// locals over, as moveTakenCaptures describes.
// taken lists the locals the closure borrows, including those a closure it captures borrows.
type closureBorrow struct {
	name     string
	local    bool
	viaFuncs bool
	takeable bool
	taken    takenLocals
}

// takenLocals lists the locals a closure borrows, which a slot that keeps the closure for
// 'static takes over. roots holds each one. vars holds the `var` bindings among them, which the
// closure reads or writes as bindings. Such a binding may hold a value type no loan tracks, so a
// takeover marks each one moved directly.
type takenLocals struct {
	roots []liveness.VarID
	vars  []liveness.VarID
}

// shortFuncLifetimes returns the lifetimes retentionLifetimes finds in t other than 'static. A
// function whose lifetime is 'static outlives any closure that captures it.
func shortFuncLifetimes(t soltype.Type) []soltype.Lifetime {
	return slices.DeleteFunc(retentionLifetimes(t), isStaticFuncLt)
}

// isFrameLocal reports whether b, the binding name resolves to, is a local of the body being
// walked rather than of a body further out. Each body numbers its own locals, so the VarID of
// an outer local either is missing from this body's names or names a different local here. A
// closure's body defines each of its captures as a parameter, so a capture is excluded too. A
// capture belongs to the body the closure is written in, and every call of the closure shares
// it.
func (c *checker) isFrameLocal(name string, b ValueBinding) bool {
	id := liveness.VarID(b.VarID)
	local, ok := c.fn.varIDNames[id]
	return ok && local == name && !c.fn.captureVarIDs.Contains(id)
}

// holdsOnlyFrameBorrows reports whether every borrow a value of type t holds is of a local of
// the current body. The walk reads each borrow, each function with a lifetime, and
// each class or alias lifetime argument in t, and the bounds of each type variable it reaches.
// It does not enter a function type, since a function's parameters and result are not values
// it holds. A function's lifetime must belong to a closure in the body's
// frameClosures, since a closure's lifetime does not record what it captures. Any other
// lifetime must satisfy isFrameLifetime. A borrow in a variable's upper bound says the value a
// caller supplies is a borrow, so it is never a borrow of a local.
func (c *checker) holdsOnlyFrameBorrows(t soltype.Type) bool {
	if t == nil {
		return true
	}
	v := &frameBorrowVisitor{c: c, ok: true, vars: set.NewSet[*soltype.TypeVarType]()}
	t.Accept(v, soltype.Positive)
	return v.ok
}

// frameBorrowVisitor is the visitor holdsOnlyFrameBorrows runs.
type frameBorrowVisitor struct {
	c    *checker
	ok   bool
	vars set.Set[*soltype.TypeVarType]
	// upper is set while the walk reads a variable's upper bounds.
	upper bool
}

func (v *frameBorrowVisitor) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.RefType:
		if t.Lt != nil && (v.upper || !v.c.isFrameLifetime(t.Lt)) {
			v.ok = false
		}
	case *soltype.FuncType:
		if t.Lt != nil {
			lt, isVar := t.Lt.(*soltype.LifetimeVar)
			v.ok = v.ok && !v.upper && isVar && v.c.fn.frameClosures != nil && v.c.fn.frameClosures.Contains(lt)
		}
		return soltype.EnterResult{SkipChildren: true}
	case *soltype.ClassType:
		v.lifetimeArgs(t.LifetimeArgs)
	case *soltype.AliasType:
		v.lifetimeArgs(t.LifetimeArgs)
	case *soltype.TypeVarType:
		if !v.vars.Contains(t) {
			v.vars.Add(t)
			for _, b := range t.LowerBounds {
				b.Accept(v, pol)
			}
			upper := v.upper
			v.upper = true
			for _, b := range t.UpperBounds {
				b.Accept(v, pol)
			}
			v.upper = upper
		}
	}
	return soltype.EnterResult{}
}

func (*frameBorrowVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// lifetimeArgs clears ok when one of lts is not a frame lifetime.
func (v *frameBorrowVisitor) lifetimeArgs(lts []soltype.Lifetime) {
	for _, lt := range lts {
		if !v.c.isFrameLifetime(lt) {
			v.ok = false
		}
	}
}

// isFrameLifetime reports whether lt belongs to the current body. It does when lt is a variable
// minted after the body's parameters resolved, and so is each of its lower bounds, read through
// their own lower bounds. A lifetime with a parameter's lifetime among its lower bounds borrows
// what the parameter borrows.
func (c *checker) isFrameLifetime(lt soltype.Lifetime) bool {
	seen := set.NewSet[*soltype.LifetimeVar]()
	pending := []soltype.Lifetime{lt}
	for len(pending) > 0 {
		cur := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		v, isVar := cur.(*soltype.LifetimeVar)
		if !isVar || v.ID < c.fn.bodyLtMark {
			return false
		}
		if seen.Contains(v) {
			continue
		}
		seen.Add(v)
		pending = append(pending, v.LowerBounds...)
	}
	return true
}

// noteFrameClosureRead adds the lifetime of t, the value a read of b resolved to, to
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

// ownReturnedClosures replaces each return of the current body with a copy in which every
// closure whose lifetime is in the body's frameClosures has no lifetime. Such a closure borrows
// only locals of the body, and a returned closure takes what it captures, as F3 states, so it
// holds no borrow once it is returned. A closure returned through a local, as in `return f`
// after `val f = fn () { … }`, is reached through the lower bounds of the local's type, and
// that return becomes the copies of those bounds. The closure's own type is left as it is,
// since the binding and the expression that made it still hold it. It must run before the
// returns are joined into the function's result.
func (c *checker) ownReturnedClosures() {
	if c.fn.frameClosures == nil {
		return
	}
	v := &returnedClosureOwner{frame: c.fn.frameClosures}
	for i, t := range c.fn.returns {
		if t != nil {
			c.fn.returns[i] = t.Accept(v, soltype.Positive)
		}
	}
}

// returnedClosureOwner is the visitor ownReturnedClosures runs.
type returnedClosureOwner struct {
	frame set.Set[*soltype.LifetimeVar]
}

func (v *returnedClosureOwner) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.TypeVarType:
		if owned, ok := v.ownedLowerBounds(t); ok {
			return soltype.EnterResult{Type: owned, SkipChildren: true}
		}
	case *soltype.FuncType:
		// A function's result is left as it is. A closure that a returned closure returns
		// borrows what the returned closure took, and every call of that closure shares it.
		return soltype.EnterResult{Type: v.owned(t), SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (*returnedClosureOwner) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// owned returns a copy of ft with no lifetime when its lifetime is in the frame, and ft
// otherwise.
func (v *returnedClosureOwner) owned(ft *soltype.FuncType) soltype.Type {
	lt, isVar := ft.Lt.(*soltype.LifetimeVar)
	if !isVar || !v.frame.Contains(lt) {
		return ft
	}
	copied := *ft
	copied.Lt = nil
	return &copied
}

// ownedLowerBounds returns the union of the owned copies of the functions that flow into tv,
// read through any variables among its lower bounds. It returns false when a lower bound is not
// a function, or when no lower bound is a closure in the frame, so tv stays as it is.
func (v *returnedClosureOwner) ownedLowerBounds(tv *soltype.TypeVarType) (soltype.Type, bool) {
	seen := set.NewSet[*soltype.TypeVarType]()
	pending := []*soltype.TypeVarType{tv}
	var parts []soltype.Type
	changed := false
	for len(pending) > 0 {
		cur := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen.Contains(cur) {
			continue
		}
		seen.Add(cur)
		for _, lb := range cur.LowerBounds {
			switch lb := lb.(type) {
			case *soltype.TypeVarType:
				pending = append(pending, lb)
			case *soltype.FuncType:
				owned := v.owned(lb)
				changed = changed || owned != soltype.Type(lb)
				parts = append(parts, owned)
			default:
				return nil, false
			}
		}
	}
	if !changed {
		return nil, false
	}
	if len(parts) == 1 {
		return parts[0], true
	}
	return newUnion(nil, parts), true
}

// capturesByBorrow reports whether a closure reads a captured `val` binding of type t through a
// borrow. A value type such as a primitive or a promise is copied into the closure instead. A
// function is a value type too, and the lifetime on its type decides whether it holds a
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

// sortFuncKeptForStatic decides err, a FuncKeptForStaticError whose prov and site are set, by
// reading where the lifetime of its source comes from, as funcLtSources finds:
//
//   - A source reaching only call-only parameters and signature lifetimes is reported by
//     checkCallOnlyParams or the signature's lifetime checks, so err is dropped.
//   - A source reaching a closure names the closure's first borrowing capture in err.
//   - A source reaching only closures in tentativeClosures holds a borrow only if a captured
//     binding settles to something other than a value type. err is held back for
//     settleTentativeFuncEscapes to decide once those settle.
//
// It returns whether err is reported now.
func (c *checker) sortFuncKeptForStatic(err *FuncKeptForStaticError) bool {
	src := c.funcLtSources(err.Sub.Lt)
	if len(src.closures) == 0 && !src.other {
		return false
	}
	err.source, err.keeper = c.funcKeptNames(err)
	if n, ok := c.prov.NodeFor(err.Super); ok {
		_, err.inferred = n.(*ast.FuncExpr)
	}
	for _, lt := range src.closures {
		if b, found := c.closureBorrows[lt]; found {
			err.borrowed, err.local = b.name, b.local
			break
		}
	}
	tentative := !src.other && len(src.closures) > 0
	for _, lt := range src.closures {
		if _, found := c.tentativeClosures[lt]; !found {
			tentative = false
		}
	}
	if tentative {
		c.heldFuncEscapes = append(c.heldFuncEscapes, heldFuncEscape{err: err, closures: src.closures})
		return false
	}
	if c.recordCaptureTakeover(err, src) {
		return false
	}
	return true
}

// recordCaptureTakeover reports whether the slot err names takes over the captures of each closure
// its source holds a borrow through, and records the takeover when it does. It does when the
// source holds a borrow only through closures written in the current body whose captures are
// all takeable, as closureBorrow describes, and the slot is a written type that an argument or
// an assigned value fills. err.inferred must be set.
//
// The value is added to the body's takeovers, and a store into a field gets its retainedStore
// here. The dataflow pass then moves each captured local into the closure at the store, so a
// later use of the local is a use of a moved value.
func (c *checker) recordCaptureTakeover(err *FuncKeptForStaticError, src funcLtSourceSet) bool {
	// A slot whose type an unannotated binding took from a closure keeps nothing for 'static.
	if c.fn == nil || err.inferred || src.other || len(src.closures) == 0 {
		return false
	}
	var taken takenLocals
	for _, lt := range src.closures {
		b, found := c.closureBorrows[lt]
		if !found || !b.takeable || c.fn.frameClosures == nil || !c.fn.frameClosures.Contains(lt) {
			return false
		}
		taken.roots = append(taken.roots, b.taken.roots...)
		taken.vars = append(taken.vars, b.taken.vars...)
	}
	value, keeper := c.keptValue(err)
	if keeper == nil {
		return false
	}
	if c.fn.takeovers == nil {
		c.fn.takeovers = map[ast.Expr]takenLocals{}
	}
	c.fn.takeovers[value] = taken
	// A call records a store for each argument its parameters keep. An assignment or a
	// declaration gets its store here, since a slot it fills keeps the closure from then on.
	var via string
	switch keeper := keeper.(type) {
	case *ast.BinaryExpr:
		via = "the slot"
		if text := placeText(keeper.Left); text != "" {
			via = closureVia(strings.Trim(text, "`"))
		}
	case *ast.VarDecl:
		via = "the slot"
		if ip, isIdent := keeper.Pattern.(*ast.IdentPat); isIdent {
			via = closureVia(ip.Name)
		}
	}
	if via != "" {
		if at, ok := c.eventStmtRef(); ok {
			c.noteRetainedClosureStore(retainedStore{via: via, expr: value, at: at, static: true})
		}
	}
	return true
}

// keptValue returns the value err's slot receives, an argument or an assigned or declared
// value, and the call, assignment, or declaration that receives it. It returns nil for both
// when keeperOf names neither the source's own node nor err's site. The source's node comes
// first, since a rest argument is checked with the whole call as the site, and that call can
// itself be an argument of another call.
func (c *checker) keptValue(err *FuncKeptForStaticError) (ast.Expr, ast.Node) {
	for _, n := range []ast.Node{c.nodeOfType(err.Sub), err.site} {
		if e, isExpr := n.(ast.Expr); isExpr {
			if k, found := c.keeperOf[e]; found {
				return e, k
			}
		}
	}
	return nil, nil
}

// noteKeeper records keeper, a call, an assignment, or a declaration, as what keeps value,
// one of its arguments, the value it assigns, or the value it initializes, in keeperOf.
func (c *checker) noteKeeper(value ast.Expr, keeper ast.Node) {
	if value == nil {
		return
	}
	if c.keeperOf == nil {
		c.keeperOf = map[ast.Expr]ast.Node{}
	}
	c.keeperOf[value] = keeper
}

// funcKeptNames returns the phrases err names its source and the slot that keeps it by. The
// source is "`show`" when the blamed expression reads the binding `show`, "this closure" when
// it is a closure, and "this function" otherwise. The blamed expression is the node of the
// source's type, or the site when that node is missing or in another source, as Span blames.
// The keeper is the callee of a call, the target of an assignment, or the binding a
// declaration initializes, and "" otherwise.
//
// A site that assigns is replaced by the value it assigns, so the error blames the value.
func (c *checker) funcKeptNames(err *FuncKeptForStaticError) (source, keeper string) {
	site := err.site
	// An argument or an assigned value is checked with the value as the site, so the call
	// or the assignment it belongs to keeps it.
	if _, k := c.keptValue(err); k != nil {
		site = k
	}
	switch site := site.(type) {
	case *ast.CallExpr:
		if name := calleeName(site.Callee); name != "" {
			keeper = "`" + name + "`"
		}
	case *ast.BinaryExpr:
		if site.Op == ast.Assign {
			keeper = placeText(site.Left)
			err.site = site.Right
		}
	case *ast.VarDecl:
		if ip, isIdent := site.Pattern.(*ast.IdentPat); isIdent {
			keeper = "`" + ip.Name + "`"
		}
	}
	blamed := err.site
	if n, ok := c.prov.NodeFor(err.Sub); ok && (err.site == nil || n.Span().SourceID == err.site.Span().SourceID) {
		blamed = n
	}
	source = "this function"
	switch n := blamed.(type) {
	case *ast.IdentExpr:
		source = "`" + n.Name + "`"
	case *ast.FuncExpr:
		source = "this closure"
	}
	return source, keeper
}

// nodeOfType returns the node t's provenance names, or nil when it names none.
func (c *checker) nodeOfType(t soltype.Type) ast.Node {
	if n, ok := c.prov.NodeFor(t); ok {
		return n
	}
	return nil
}

// placeText renders a place written as a name or a chain of property reads, as "`registry.cb`",
// and returns "" for any other expression.
func placeText(e ast.Expr) string {
	var parts []string
	for {
		switch cur := e.(type) {
		case *ast.IdentExpr:
			parts = append([]string{cur.Name}, parts...)
			return "`" + strings.Join(parts, ".") + "`"
		case *ast.MemberExpr:
			parts = append([]string{cur.Prop.Name}, parts...)
			e = cur.Object
		default:
			return ""
		}
	}
}

// heldFuncEscape is one FuncKeptForStaticError sortFuncKeptForStatic kept back. closures holds
// the lifetimes of the tentative closures its source holds a borrow through.
type heldFuncEscape struct {
	err      *FuncKeptForStaticError
	closures []*soltype.LifetimeVar
}

// settleTentativeFuncEscapes reports each held FuncKeptForStaticError unless every closure its
// source holds a borrow through has type variables that all settled to value types. Such a
// closure copies what it captures, so it holds no borrow. An error that repeats the message
// and span of one already reported is dropped. It also clears keeperOf. It must run once the
// enclosing component has solved, since a use later in the body can settle a variable.
func (c *checker) settleTentativeFuncEscapes() {
	held := c.heldFuncEscapes
	c.heldFuncEscapes = nil
	// Every FuncKeptForStaticError of the component named its keeper when it was reported.
	c.keeperOf = nil
	if len(held) == 0 {
		return
	}
	type diagnostic struct {
		span ast.Span
		msg  string
	}
	reported := set.NewSet[diagnostic]()
	for _, e := range c.errs {
		reported.Add(diagnostic{e.Span(), e.Message()})
	}
	for _, h := range held {
		if c.closuresSettled(h.closures) {
			continue
		}
		d := diagnostic{h.err.Span(), h.err.Message()}
		if reported.Contains(d) {
			continue
		}
		reported.Add(d)
		c.errs = append(c.errs, h.err)
	}
}

// closuresSettled reports whether every type variable of each closure in closures settled to a
// value type.
func (c *checker) closuresSettled(closures []*soltype.LifetimeVar) bool {
	for _, lt := range closures {
		for _, v := range c.tentativeClosures[lt] {
			// A variable no caller fills, as `val n = count()` holds, holds only the values
			// that flow into it, so its lower bounds decide it too.
			heldValues := !c.openVars.Contains(v) && c.holdsOnlyValueTypes(v, set.NewSet[*soltype.TypeVarType]())
			if !settlesToValueType(v) && !heldValues {
				return false
			}
		}
	}
	return true
}

// funcLtSourceSet is what funcLtSources found a function lifetime to come from.
type funcLtSourceSet struct {
	// closures holds the lifetimes of the closures reached, the ones closureBorrows or
	// tentativeClosures holds.
	closures []*soltype.LifetimeVar
	// other says a source was reached that is neither a closure, a call-only parameter, nor a
	// lifetime a signature names, such as the lifetime of a declared function's result.
	other bool
}

// funcLtSources reads where the function lifetime lt comes from. The walk follows the lifetime
// an instantiation copied, the lifetime an extrusion proxied, and each lower bound. It stops at
// the lifetime of a closure that borrows a capture itself, at a call-only parameter's lifetime,
// and at a lifetime a signature names. A lifetime with no lower bound, no proxied origin, and no
// copy left to read counts as other. 'static is no source.
func (c *checker) funcLtSources(lt soltype.Lifetime) funcLtSourceSet {
	var out funcLtSourceSet
	seen := set.NewSet[*soltype.LifetimeVar]()
	pending := []soltype.Lifetime{lt}
	for len(pending) > 0 {
		cur := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		v, isVar := cur.(*soltype.LifetimeVar)
		if !isVar {
			if !isStaticFuncLt(cur) {
				out.other = true
			}
			continue
		}
		if seen.Contains(v) {
			continue
		}
		seen.Add(v)
		// A closure that borrows only through functions it captures is read through to those
		// functions, so a call-only parameter it captures is reported by the parameter's check.
		cb, borrows := c.closureBorrows[v]
		_, tentative := c.tentativeClosures[v]
		if (borrows && !cb.viaFuncs) || tentative {
			out.closures = append(out.closures, v)
			continue
		}
		if c.callOnlyLts.Contains(v) || c.isSignatureLt(v) {
			continue
		}
		next := slices.Clone(v.LowerBounds)
		origin, proxied := c.ctx.ltProxyOrigin[v]
		if proxied {
			next = append(next, origin)
		}
		// A copy an instantiation made is the same lifetime as the one it copies, so only a
		// copy not yet read leads anywhere new.
		copies := 0
		for _, copied := range c.ctx.ltCopies[v] {
			if !seen.Contains(copied) {
				next = append(next, copied)
				copies++
			}
		}
		// A lifetime with no lower bound and no proxied origin is a source of its own, such as
		// a declared function's result lifetime, once its copies are read.
		if len(v.LowerBounds) == 0 && !proxied && copies == 0 {
			out.other = true
		}
		pending = append(pending, next...)
	}
	return out
}

// settlesToValueType reports whether every value v can hold is a value type, because one of
// its upper bounds, read through any variables they name, is a value type. A subtype of a
// primitive is a primitive, so the bound decides it. A lower bound does not, since a caller
// may still pass a value the body never saw.
func settlesToValueType(v *soltype.TypeVarType) bool {
	seen := set.NewSet[*soltype.TypeVarType]()
	pending := []*soltype.TypeVarType{v}
	for len(pending) > 0 {
		cur := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen.Contains(cur) {
			continue
		}
		seen.Add(cur)
		for _, ub := range cur.UpperBounds {
			if next, isVar := ub.(*soltype.TypeVarType); isVar {
				pending = append(pending, next)
			} else if isValueType(ub) {
				return true
			}
		}
	}
	return false
}
