package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// A function returns a value only when some finite value has its return type. Unguarded recursion
// takes that away. Here is the shape:
//
//	fn cons(x: number) { return {head: x, tail: cons(x)} }
//
// It infers `fn (x: number) -> {head: number, tail: μX0.{head: number, tail: X0}}`. The type is
// right, and that is what makes this a diagnostic gap rather than an inference bug. Every value of
// `μX0.{head: number, tail: X0}` is an infinite chain, so building the `tail` field calls `cons`
// again and the call overflows the stack instead of returning. There is no version of that program
// that does something useful.
//
// checkCanReturn is the value-level twin of checkProductive in productivity.go. That one rejects a
// type alias whose recursion emits no structure at all. This one rejects a function whose recursion
// emits structure forever with nothing to stop it.
//
// # What lets a value stop
//
// finitelyInhabited decides the question by reading the return of the coalesced signature. A μ-knot
// has a finite value when some path through its body reaches a leaf without reaching the binder.
// Four shapes provide such a path, and each one appears in ordinary code.
//
// A union arm that does not mention the binder is what a base case produces, so
// `μX0.({head: number, tail: undefined} | {head: number, tail: X0})` stops at the first arm.
//
// An optional property is never built when it is omitted, so `{head: T, tail?: List}` stops at an
// absent `tail`.
//
// A function type holds its body unevaluated, so `{value: number, rest: fn () -> X0}` stops at the
// thunk and recurses only when a caller forces `rest`.
//
// A `Promise` and a generator hold their payload unevaluated for the same reason. An async loop that
// yields to the event loop each lap is legitimate and must not be rejected:
//
//	async fn serve() {
//	    val req = await accept()
//	    handle(req)
//	    return serve()
//	}
//
// That is `fn () -> Promise<μX0.Promise<X0>>`, a real μ-knot and not a hang, since every `await`
// hands control back to the event loop.
//
// # What this does not cover
//
// `fn serve() { return serve() }` returns `never` and `fn serve() { serve() }` returns `void`.
// Neither infers a μ-knot, so neither reaches this check. Both are tight infinite loops that yield
// to nothing, the value-level twin of `type Bad = Bad`, and they want a diagnostic of their own.

// checkCanReturn reports every queued function that cannot return, and clears the queue so a later
// group starts empty.
//
// It cannot run inside inferFunc, because the cycle that ties the knot does not exist yet when a
// body finishes. A recursive call resolves through the binding var inferComponent pre-bound in phase
// 1, and that var carries no bounds until phase 2 constrains the finished function type into it,
// which happens after inferFunc has returned. Coalescing `fn f() { return {next: f()} }` the moment
// its body is walked yields `fn () -> {next: never}`, with no μ-knot to read. A mutually-recursive
// group needs the wait for a second reason on top: the cycle also runs through a sibling whose body
// has not been walked at all.
//
// The display type comes from coalesceScheme rather than plain coalesce, and it is built from the
// WHOLE function type rather than the return alone. coalesceScheme keeps a variable the caller
// chooses symbolic instead of inlining it to its bounds, and it recognizes such a variable by its
// occurring in both polarities. Only the parameter list shows the negative occurrence, which is why
// the return alone is not enough.
//
// Both parts are load-bearing. Plain coalesce renders an unannotated parameter as `never` wherever
// it reaches the return, and the union constructor drops a `never` arm, so this function would lose
// the base case it plainly has and be rejected:
//
//	fn f(x) {
//	    if cond() { return x }
//	    return {next: f(x)}
//	}
//
// It displays as `fn <T0>(x: T0) -> T0 | {next: T0}`, and the `T0` arm is what stops the recursion.
func (c *checker) checkCanReturn() {
	pending := c.pendingReturns
	c.pendingReturns = nil
	for _, p := range pending {
		display, isFunc := coalesceScheme(p.fn, p.genLevel).(*soltype.FuncType)
		if !isFunc || finitelyInhabited(display.Ret) {
			continue
		}
		c.report(&NonReturningRecursionError{Site: p.site, Name: p.name, Fn: display})
	}
}

// pendingReturn is one function body waiting for checkCanReturn.
//
// fn is the function's inferred type, still holding inference variables. genLevel is the level the
// binding this function belongs to generalizes at, so a variable minted above it becomes a
// quantified type parameter and a caller chooses what it stands for. site is the node the diagnostic
// points at. name is what the source called the function, empty when no name reached inferFunc.
type pendingReturn struct {
	site     ast.Node
	name     string
	fn       *soltype.FuncType
	genLevel int
}

// queueReturnCheck queues one body-carrying function for checkCanReturn. node is the *ast.FuncDecl
// or *ast.FuncExpr being typed and ft is the type inferFunc built for it.
//
// A `fn` declaration is blamed at its name. Every other function reaches inferFunc as a bare
// *ast.FuncExpr, so a lambda and a class member are blamed over their whole span. memberName is what
// the class member is called, empty for a lambda and for a `fn` declaration.
//
// A queued entry is rolled back with the probe that recorded it, mirroring the errs snapshot
// openProbe takes. A discarded trial must leave no diagnostic behind, and the queue turns into
// diagnostics after the trial is over.
func (c *checker) queueReturnCheck(node ast.Node, ft *soltype.FuncType, genLevel int, memberName string) {
	site, name := node, memberName
	if fd, isDecl := node.(*ast.FuncDecl); isDecl && fd.Name != nil {
		site, name = fd.Name, fd.Name.Name
	}
	if probe := c.ctx.probe; probe != nil {
		queueLen := len(c.pendingReturns)
		probe.onRollback(func() { c.pendingReturns = c.pendingReturns[:queueLen] })
	}
	c.pendingReturns = append(c.pendingReturns,
		pendingReturn{site: site, name: name, fn: ft, genLevel: genLevel})
}

// finitelyInhabited reports whether a finite value has type t. It reads a coalesced type, where a
// recursive position is a μ-knot rather than a cyclic inference variable, so it descends a finite
// tree.
//
// The walk visits only the positions that building a value has to fill. An object's optional
// property and a function type's return are skipped, since neither is built while the value around
// them is. Reaching a μ-binder means the construction came back to the knot with nothing skipped on
// the way, which is the one thing that makes this false.
//
// Every shape the walk cannot decide reads as inhabited, so an unfamiliar one never produces a
// diagnostic. Three reach it through the final arm. A quantified type parameter is one, since the
// caller chooses what it stands for and may choose something inhabited, which is what makes a
// parameter reaching the return count as a base case. `never` is another. Nothing has that type, but
// a function returning it diverges or throws rather than recursing, and
// `fn todo() -> never throws string { throw "todo" }` is a stub anyone may write. An alias reference
// is the third, since deciding one would mean expanding it.
//
// It is a recursive switch rather than a soltype visitor, which the CLAUDE.md convention would
// otherwise call for. Each kind folds its operands differently — an object needs every required
// member, a union needs one arm — and the enter/exit visitor carries no per-node synthesized value
// to fold. guardsEveryOperand in productivity.go is the same shape for the same reason.
func finitelyInhabited(t soltype.Type) bool {
	switch t := t.(type) {
	case *soltype.RecursiveVarType:
		// The construction came back to the knot, so this path builds one more level instead of
		// finishing.
		return false
	case *soltype.RecursiveType:
		return finitelyInhabited(t.Body)
	case *soltype.FuncType, *soltype.PromiseType, *soltype.GeneratorType:
		// A closure, a promise, and a generator each hold their payload unevaluated, so building one
		// runs none of the code that would produce that payload.
		return true
	case *soltype.ArrayType:
		// The empty array is a finite value of every array type.
		return true
	case *soltype.ObjectType:
		// Only a required property and a spread are read. A method, a getter, a setter, and a
		// constructor are function-valued, so they defer their bodies the way a property holding a
		// function type does. A mapped member is a shape the walk does not decide.
		for _, elem := range t.Elems {
			switch elem := elem.(type) {
			case *soltype.PropertyElem:
				// An omitted optional property is never built, so it cannot force another lap.
				if !elem.Optional && !finitelyInhabited(elem.Type) {
					return false
				}
			case *soltype.SpreadElem:
				// A `...A` merges A's own members in at this position, so what A requires this
				// object requires.
				if !finitelyInhabited(elem.Type) {
					return false
				}
			}
		}
		return true
	case *soltype.TupleType:
		// A tuple has a fixed arity, so every position is built.
		for _, elem := range t.Elems {
			if !finitelyInhabited(elem) {
				return false
			}
		}
		return true
	case *soltype.UnionType:
		// One arm that finishes is a base case, and the value takes that arm. An empty union is
		// `never`, which reads as inhabited for the reason above.
		if len(t.Types) == 0 {
			return true
		}
		for _, member := range t.Types {
			if finitelyInhabited(member) {
				return true
			}
		}
		return false
	case *soltype.IntersectionType:
		// A value of an intersection is a value of each member at once, so every member must finish.
		for _, member := range t.Types {
			if !finitelyInhabited(member) {
				return false
			}
		}
		return true
	case *soltype.RefType:
		// A borrow points at a value someone else built, so it finishes exactly when its pointee
		// does. No minting site leaves Inner nil, so reading that as inhabited is a guard against a
		// crash rather than a case with meaning of its own.
		if t.Inner == nil {
			return true
		}
		return finitelyInhabited(t.Inner)
	case *soltype.ExactnessType:
		// The marker sets exactness on its operand and adds no structure of its own.
		return finitelyInhabited(t.Operand)
	default:
		return true
	}
}
