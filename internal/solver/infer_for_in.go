package solver

import (
	"slices"

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
			// Reading `it` in `declare val it: number` binds a fresh variable with `number`
			// as its lower bound, so the report names `number` rather than the variable.
			c.report(&NotIterableError{Iterable: s.Iterable, Type: reportedOperand(iterable), Await: s.IsAwait})
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

// reportedOperand returns the type a diagnostic names for an operand of type t. An inference
// variable is coalesced to the union of its lower bounds, keeping any borrow around it. A
// variable with no lower bound coalesces to `never`, a type the source did not write, so that
// variable is returned unchanged. Any other type is returned unchanged.
func reportedOperand(t soltype.Type) soltype.Type {
	if !carrierIsVar(t) {
		return t
	}
	if solved := coalesce(t, soltype.Positive); !isNeverType(soltype.CarrierOf(solved)) {
		return solved
	}
	return t
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
	c.raiseInto(lvl, site, describeSite("advancing", "advancing this iterator", site), raise)
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
	return c.protocolElem(t, true)
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
// A variadic tuple yields its fixed elements and its middle's element type, so
// `[number, ...Array<string>]` yields `number | string`.
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
		if shape, ok := c.ctx.shapeOf(t); ok && shape.rest != nil {
			elems := append(slices.Clone(shape.prefix), shape.rest)
			return newUnion(c.ctx, append(elems, shape.suffix...)), true
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
	return c.protocolElem(t, false)
}

// protocolElem returns the element type t yields through the iteration protocol, and
// false when t declares no such member. It is protocolIterator's first slot, and async
// picks the protocol the way it does there.
func (c *checker) protocolElem(t soltype.Type, async bool) (soltype.Type, bool) {
	typeArgs, found := c.protocolIterator(t, async)
	if !found || len(typeArgs) == 0 {
		return nil, false
	}
	return typeArgs[0], true
}

// protocolIterator returns the type arguments of the iterator t's protocol member hands back,
// and false when t declares no such member. The member is `[Symbol.asyncIterator]` when async
// is true and `[Symbol.iterator]` otherwise.
//
// It finds that member through protocolReturn and takes the arguments of what calling it
// evaluates to. `Array<T>` declares `[Symbol.iterator](&self) -> ArrayIterator<T>`, so the
// lookup lands on that method and the arguments are the one `T` in its return.
//
// The slots are read by position rather than by calling the member, which keeps this to a
// projection. `Iterator<T, TReturn, TNext>` fixes the order every iterator type in the tree
// writes, so slot 0 is the element, slot 1 what the iteration finishes with, and slot 2 what
// it accepts from a sent value. A shorter list states only the slots it carries. A member
// returning something with no type argument yields nothing readable and declines, so the
// operand is reported not iterable rather than iterating `unknown`.
func (c *checker) protocolIterator(t soltype.Type, async bool) ([]soltype.Type, bool) {
	symbol := soltype.IteratorSymbolMember
	if async {
		symbol = soltype.AsyncIteratorSymbolMember
	}
	ret, returns := c.protocolReturn(t, symbol, set.NewSet[string]())
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
// the ones the reference itself supplied.
//
// expandAliasChain walks the same chain and cannot serve here. It follows to the end and
// answers what the chain stands for, so an interface reference comes back as the object
// describing its members, which carries no arguments at all. What a slot read needs is
// the last nominal reference in the chain, which that answer has already discarded.
//
// A seen-set of names breaks a degenerate cycle such as `type A = B` over `type B = A`.
// The alias is recorded before it is expanded, so a chain returning to a name it already
// walked stops on the reference rather than looping.
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
// `[Symbol.iterator](&self) -> Iterator<T>`, and an object writes it as a property holding a
// function.
//
// Iteration calls the member with no arguments, so a signature demanding one does not
// answer and an overload set answers from the first arm that takes none. Reading arm zero
// regardless would let `[Symbol.iterator](&self, hint: string) -> Iterator<number>` beside
// `[Symbol.iterator](&self) -> Iterator<boolean>` report the element as `number`, where the
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

// protocolReturn returns what calling the member named by symbol on t with no arguments
// evaluates to, and false when t carries no such member or nullaryReturn declines it.
//
// t is normalized first: a borrow is peeled, since iterating `&xs` reads the same members as
// `xs`, and an inference variable is coalesced to its lower-bound shape. An alias is then
// followed to the end of its chain, so `type Nums = Array<number>` reads the member the
// class it names declares. What the chain ends at decides where the member is read:
//
//   - A class instance reads its body projected at the arguments it is reached at.
//   - An object type reads its own member list. An interface without an `extends` clause
//     expands to one.
//   - An intersection reads its arms in armsByPrecedence order and answers with the first
//     arm whose member nullaryReturn accepts.
//   - A primitive reads its wrapper class's body, so a `string` iterates through the
//     `[Symbol.iterator]` that `String` declares.
//
// Every other type carries no members and declines.
//
// That order serves an interface with an `extends` clause, so a member the interface declares
// is read over the one a parent supplies. `IterableIterator<T>` expands to
// `Iterator<T> & {[Symbol.iterator]() -> IterableIterator<T>}` and reads its own declaration.
// For an intersection written in source, every arm's member applies to the value, so the
// element any one arm reports is a supertype of what the value yields.
//
// seen holds the alias names expanded on the path from the operand to t, so an `extends`
// cycle stops rather than recursing forever. Each arm gets its own copy, since two arms
// can reach the same alias at different arguments.
func (c *checker) protocolReturn(t soltype.Type, symbol string, seen set.Set[string]) (soltype.Type, bool) {
	var member soltype.ObjTypeElem
	found := false
	switch t := c.expandAliasChainFrom(groundedCarrier(t), seen).(type) {
	case *soltype.ObjectType:
		member, found = t.ReadMember(symbol)
	case *soltype.ClassType:
		member, found = c.declaredClassMember(t, symbol)
	case *soltype.IntersectionType:
		for _, arm := range armsByPrecedence(t) {
			if ret, returns := c.protocolReturn(arm, symbol, seen.Clone()); returns {
				return ret, true
			}
		}
	default:
		if wrapper, ok := c.primitiveWrapper(t); ok {
			member, found = c.declaredClassMember(wrapper, symbol)
		}
	}
	if !found {
		return nil, false
	}
	return nullaryReturn(member)
}

// declaredClassMember returns the member named by symbol that ct's body declares, projected
// at ct's arguments.
func (c *checker) declaredClassMember(ct *soltype.ClassType, symbol string) (soltype.ObjTypeElem, bool) {
	body, ok := c.ctx.projectClassBody(ct)
	if !ok {
		return nil, false
	}
	return body.ReadMember(symbol)
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
