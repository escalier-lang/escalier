package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferForIn types a `for (x in xs)` / `for await (x in xs)` loop, binding the loop
// variable at the element type the operand yields.
//
// A sync loop reads that element through the operand's `[Symbol.iterator]`, and a
// `for await` through its `[Symbol.asyncIterator]`, so the two protocols stay apart and a
// sync iterable does not answer a `for await`. Two shapes resolve ahead of the lookup: a
// tuple, which is iterable by a rule no declaration states, and a generator, which the
// solver mints as a concrete rather than reaching through a declaration. See
// iterableElemType.
//
// A `for await` outside an `async fn` is a WALK rejection symmetric to
// AwaitOutsideAsyncError: the iterable and body are still walked so their own
// errors surface.
//
// The loop contributes `undefined` to its enclosing block, since a loop is a statement
// rather than a value. The CFG builder already decomposes a ForInStmt into a header, a body
// block carrying the loop-variable defs, and a back edge (liveness.processForIn),
// so the move and borrow-edge dataflow over the loop body — including across the
// back edge — is handled by the existing per-statement recording as inferBlock
// walks the body. This function adds no move/borrow wiring of its own.
func (c *checker) inferForIn(scope *Scope, lvl int, s *ast.ForInStmt) soltype.Type {
	awaitRejected := false
	if s.IsAwait && (c.fn == nil || !c.fn.async) {
		// The enclosing function is the one the user would mark `async`; nil at
		// module top-level, where Related() stays empty.
		var enclosing ast.Node
		if c.fn != nil {
			enclosing = c.fn.node
		}
		c.report(&ForAwaitOutsideAsyncError{Loop: s, EnclosingFn: enclosing})
		awaitRejected = true
	}

	iterable := c.inferExpr(scope, lvl, s.Iterable)
	// A loop advances what it iterates, so a generator's raise surfaces here rather than
	// where the generator was obtained. Constrain it into the enclosing sink, the way a
	// throwing call does, so iterating one needs a clause or a `try`.
	c.constrainIterationRaise(s.Iterable, iterable, lvl)
	elem, ok := c.iterableElemType(s.IsAwait, iterable)
	if !ok {
		// An iterable that already failed to infer is the ErrorType recovery
		// placeholder; it absorbs rather than cascading a second diagnostic, so a
		// `for x in <broken>` reports only the underlying error. A `for await`
		// already rejected by the walk likewise reports only that walk error — one
		// diagnostic per loop, mirroring how an await outside async surfaces only the
		// walk rejection.
		_, brokenIterable := soltype.CarrierOf(iterable).(*soltype.ErrorType)
		if !awaitRejected && !brokenIterable {
			c.report(&NotIterableError{Iterable: s.Iterable, Type: iterable, Await: s.IsAwait})
		}
		// Recover with the ErrorType placeholder so the loop variable does not leak
		// an unsolved inference variable, and so a pattern binding against it absorbs
		// rather than cascading a second diagnostic.
		elem = &soltype.ErrorType{}
	}

	// A `never` element type means no value can ever be bound to the loop variable,
	// so the body is statically unreachable — iterating an empty tuple runs it zero
	// times. Skip it: the loop contributes nothing and control falls through, so
	// `for x in []` leaves the enclosing function returning `undefined` rather than
	// folding an unreachable `return x` into its return type. Collecting that return
	// would type the function as `never`, which is unsound. `fn f(xs: []) { for x in xs
	// { return x } }` returns `undefined` at runtime.
	if _, unreachable := elem.(*soltype.NeverType); unreachable {
		return &soltype.UndefinedType{}
	}

	// The loop body runs in its own scope so the loop variable is invisible after
	// the loop. bindPattern binds each leaf as a monomorphic, non-reassignable
	// binding — a loop variable is rebound by iteration, never by assignment — since
	// a leaf's ValueBinding is left at ValKind, the immutable default. A `for mut x`
	// still binds a mutable owned value, because bindPattern reads the `mut` marker
	// off the pattern and the tuple-element scrutinee is owned.
	bodyScope := scope.Child()
	c.bindPattern(bodyScope, lvl, s.Pattern, elem, nil)
	c.inferBlock(bodyScope, lvl, &s.Body)
	return &soltype.UndefinedType{}
}

// constrainIterationRaise sends what advancing t may raise into the enclosing throws
// sink. Only a generator carries a raise type today, and a union of them raises whatever
// any branch does, so the walk mirrors the element-type walk. Anything else contributes
// nothing. It marks the body as raising for the same reason a throwing call does, so an
// unused-clause warning is not drawn against a clause this loop uses.
func (c *checker) constrainIterationRaise(site ast.Expr, t soltype.Type, lvl int) {
	raise, ok := c.iterationRaise(t)
	if !ok {
		return
	}
	c.constrain(site, raise, c.throwsSink(lvl))
	c.markRaised()
}

// iterationRaise returns what advancing t may raise, and whether anything can. A borrow
// is peeled and an inference variable coalesced first, the same normalization the
// element-type walk applies.
func (c *checker) iterationRaise(t soltype.Type) (soltype.Type, bool) {
	t = groundedCarrier(t)
	switch t := t.(type) {
	case *soltype.GeneratorType:
		if !t.Raises() {
			return nil, false
		}
		return t.Throws, true
	case *soltype.UnionType:
		raises := make([]soltype.Type, 0, len(t.Types))
		for _, branch := range t.Types {
			if r, ok := c.iterationRaise(branch); ok {
				raises = append(raises, r)
			}
		}
		if len(raises) == 0 {
			return nil, false
		}
		return newUnion(c.ctx, raises), true
	}
	return nil, false
}

// iterableElemType resolves the element type T yielded by iterating a value of type t,
// returning ok=false when t yields none. The two arms differ only in which protocol
// member they read, `[Symbol.asyncIterator]` for a `for await` and `[Symbol.iterator]`
// for a sync `for`.
func (c *checker) iterableElemType(await bool, t soltype.Type) (soltype.Type, bool) {
	if await {
		return c.asyncElemType(t)
	}
	return c.syncElemType(t)
}

// asyncElemType resolves the element type of an asynchronously-iterable value, the
// `for await` counterpart of syncElemType and the same walk. An async generator yields
// its Yield slot, and a union yields the union of its branches', failing when any branch
// is not async-iterable. Everything else reads `[Symbol.asyncIterator]`. A sync generator
// is not async-iterable, and neither is a tuple, so both are rejected here.
func (c *checker) asyncElemType(t soltype.Type) (soltype.Type, bool) {
	t = groundedCarrier(t)
	switch t := t.(type) {
	case *soltype.GeneratorType:
		if !t.Async {
			return nil, false
		}
		return t.Yield, true
	case *soltype.UnionType:
		elems := make([]soltype.Type, 0, len(t.Types))
		for _, branch := range t.Types {
			e, ok := c.asyncElemType(branch)
			if !ok {
				return nil, false
			}
			elems = append(elems, e)
		}
		return newUnion(c.ctx, elems), true
	}
	return c.protocolElem(t, soltype.AsyncIteratorSymbolMember)
}

// syncElemType resolves the element type of a synchronously-iterable value. A borrow is
// peeled first, since iterating `&xs` yields the same elements as `xs`, and an inference
// variable is coalesced to its structural lower-bound shape, the way inferMatch snapshots
// a variable scrutinee before inspecting it.
//
// Three shapes answer without a declaration. A tuple yields the union of its elements, so
// `[1, 2, 3]` yields `1 | 2 | 3` and the empty tuple yields `never`. A union yields the
// union of its branches' element types, failing if any branch is not iterable. A sync
// generator yields its Yield slot. Everything else reads `[Symbol.iterator]`, so a class
// or interface declaring the member yields what its own declaration says, and one
// declaring none is not iterable.
//
// An inexact tuple `[number, ...]` has an open tail of unknown additional elements, so its
// element type is the join of its listed elements with that unknown tail, which is `unknown`.
// The precise type of the tail needs the `Array<T>` the tuple approximates.
func (c *checker) syncElemType(t soltype.Type) (soltype.Type, bool) {
	t = groundedCarrier(t)
	switch t := t.(type) {
	case *soltype.GeneratorType:
		// A sync generator iterates its yields. An async one needs a `for await`, whose
		// arm lives in asyncElemType.
		if t.Async {
			return nil, false
		}
		return t.Yield, true
	case *soltype.TupleType:
		if t.Inexact {
			return &soltype.UnknownType{}, true
		}
		return newUnion(c.ctx, t.Elems), true
	case *soltype.UnionType:
		elems := make([]soltype.Type, 0, len(t.Types))
		for _, branch := range t.Types {
			e, ok := c.syncElemType(branch)
			if !ok {
				return nil, false
			}
			elems = append(elems, e)
		}
		return newUnion(c.ctx, elems), true
	}
	return c.protocolElem(t, soltype.IteratorSymbolMember)
}

// protocolElem returns the element type t yields through the iteration protocol, and
// false when t declares no such member. It is protocolIterator's first slot.
func (c *checker) protocolElem(t soltype.Type, symbol string) (soltype.Type, bool) {
	typeArgs, found := c.protocolIterator(t, symbol)
	if !found || len(typeArgs) == 0 {
		return nil, false
	}
	return typeArgs[0], true
}

// protocolIterator returns the type arguments of the iterator the member named by symbol
// hands back, and false when t declares no such member.
//
// It reads that member off t's member view and takes the arguments of what calling it
// evaluates to. `Array<T>` declares `[Symbol.iterator](self) -> ArrayIterator<T>`, so the
// lookup lands on that method and the arguments are the one `T` in its return.
//
// The slots are read by position rather than by calling the member, which keeps this to a
// projection. `Iterator<T, TReturn, TNext>` fixes the order every iterator type in the tree
// writes, so slot 0 is the element, slot 1 what the iteration finishes with, and slot 2 what
// it accepts from a sent value. A shorter list states only the slots it carries. A member
// returning something with no type argument yields nothing readable and declines, so the
// operand is reported not iterable rather than iterating `unknown`.
func (c *checker) protocolIterator(t soltype.Type, symbol string) ([]soltype.Type, bool) {
	body, viewed := c.iterationView(t)
	if !viewed {
		return nil, false
	}
	member, found := body.ReadMember(symbol)
	if !found {
		return nil, false
	}
	ret, returns := nullaryReturn(member)
	if !returns {
		return nil, false
	}
	return nominalTypeArgs(c.iteratorReference(ret))
}

// iteratorReference follows an alias that renames another nominal reference, so the slots
// are read in the order the iterator itself declares them. `type Flip<A, B> = Iterator<B, A>`
// permutes its arguments, and reading `Flip<number, string>` where it was written would
// call `number` the element where the iterator it names puts `string` there.
//
// It stops at an alias standing for anything else, which is what an interface is: an
// interface expands to the object describing its members, and the arguments to read are
// the ones the reference itself supplied. A seen-set breaks a degenerate cycle.
func (c *checker) iteratorReference(t soltype.Type) soltype.Type {
	seen := set.NewSet[string]()
	for {
		alias, isAlias := t.(*soltype.AliasType)
		if !isAlias || seen.Contains(alias.Name) {
			return t
		}
		seen.Add(alias.Name)
		switch expanded := c.ctx.expandAlias(alias).(type) {
		case *soltype.AliasType:
			t = expanded
		case *soltype.ClassType:
			return expanded
		default:
			return t
		}
	}
}

// nullaryReturn returns what calling member with no arguments evaluates to, covering the
// two ways the protocol member is written. A declaration writes it as a method,
// `[Symbol.iterator](self) -> Iterator<T>`, and an object writes it as a property holding a
// function.
//
// Iteration calls the member with no arguments, so a signature demanding one does not
// answer and an overload set answers from the first arm that takes none. Reading arm zero
// regardless would let `[Symbol.iterator](self, hint: string) -> Iterator<number>` beside
// `[Symbol.iterator](self) -> Iterator<boolean>` report the element as `number`, where the
// call a `for`-`in` makes selects the second arm. The receiver is not a parameter, since
// the parser peels `self` into SelfParam, so an instance member's own arity is zero.
//
// An optional member is declined. `[Symbol.iterator]?()` says the member may be absent,
// and iteration has no answer for a value that does not carry it, so accepting the
// declared return would type a loop the value cannot run.
func nullaryReturn(member soltype.ObjTypeElem) (soltype.Type, bool) {
	switch member := member.(type) {
	case *soltype.MethodElem:
		if member.Optional {
			return nil, false
		}
		for _, sig := range member.Signatures {
			if acceptsNoArguments(sig) {
				return sig.Ret, true
			}
		}
		return nil, false
	case *soltype.PropertyElem:
		fn, isFunc := member.Type.(*soltype.FuncType)
		if member.Optional || !isFunc || !acceptsNoArguments(fn) {
			return nil, false
		}
		return fn.Ret, true
	}
	return nil, false
}

// acceptsNoArguments reports whether sig can be called with none. An optional parameter and
// a rest parameter each bind zero arguments, so neither makes one required.
func acceptsNoArguments(sig *soltype.FuncType) bool {
	for _, param := range sig.Params {
		if !param.Optional && !param.Rest {
			return false
		}
	}
	return true
}

// iterationView returns the member list a protocol lookup reads t's `[Symbol.iterator]`
// off. A class instance projects its body with the arguments it is reached at, an
// interface reference expands to the object it stands for, and an object literal's type
// is already that object. Every other type carries no members and declines.
//
// The operand is normalized first, the same way the structural walk normalizes it: a
// borrow is peeled, since iterating `&xs` reads the same members as `xs`, and an
// inference variable is coalesced to its lower-bound shape. An alias is then followed to
// the end of its chain, so `type Nums = Array<number>` iterates the way the class it
// names does.
func (c *checker) iterationView(t soltype.Type) (*soltype.ObjectType, bool) {
	switch t := c.expandAliasChain(groundedCarrier(t)).(type) {
	case *soltype.ObjectType:
		return t, true
	case *soltype.ClassType:
		return c.ctx.projectClassBody(t)
	}
	return nil, false
}

// nominalTypeArgs returns the type arguments of a nominal reference, and false for a
// type carrying none.
func nominalTypeArgs(t soltype.Type) ([]soltype.Type, bool) {
	var typeArgs []soltype.Type
	switch t := t.(type) {
	case *soltype.ClassType:
		typeArgs = t.TypeArgs
	case *soltype.AliasType:
		typeArgs = t.TypeArgs
	default:
		return nil, false
	}
	if len(typeArgs) == 0 {
		return nil, false
	}
	return typeArgs, true
}
