package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferForIn types a `for (x in xs)` / `for await (x in xs)` loop. The milestone
// desugars both forms to a protocol subtype check: a sync loop needs
// `xs <: Iterable<T>` and a `for await` needs `xs <: AsyncIterable<T>`, binding
// the loop variable at the element type T. The full protocol resolves T through
// the iterable's `[Symbol.iterator]()` method, which needs symbol-keyed members
// and the real Iterable/Iterator stdlib types that both land in M7. Until then
// the element type resolves STRUCTURALLY over the types the solver can
// represent — a tuple, the solver's stand-in for an array, a union of tuples,
// and a generator — and everything else is rejected as non-iterable. See
// iterableElemType.
//
// A `for await` outside an `async fn` is a WALK rejection symmetric to
// AwaitOutsideAsyncError: the iterable and body are still walked so their own
// errors surface. A `for await` accepts an AsyncGenerator, the one async iterable
// the solver can represent, and rejects every other operand by the type rule. A
// sync iterable is not an AsyncIterable.
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

// iterableElemType resolves the element type T yielded by iterating a value of
// type t, returning ok=false when t is not iterable in the current sense.
//
// For a `for await`, T must come from an AsyncIterable. The only one the solver can
// represent is an AsyncGenerator, whose Yield slot is its element type. The real stdlib
// type and the symbol-keyed protocol land with library ingestion, so every other
// operand returns false.
//
// For a sync `for`, the resolution is structural (see syncElemType): a tuple
// yields the union of its element types, a union yields the union of its
// branches' element types, a sync generator yields its Yield slot, and every
// other type is not iterable.
func (c *checker) iterableElemType(await bool, t soltype.Type) (soltype.Type, bool) {
	if await {
		return c.asyncElemType(t)
	}
	return c.syncElemType(t)
}

// asyncElemType resolves the element type of an asynchronously-iterable value, the
// `for await` counterpart of syncElemType and structurally the same walk. An async
// generator yields its Yield slot, and a union yields the union of its branches',
// failing when any branch is not async-iterable. A sync generator is not an
// AsyncIterable, and neither is a tuple, so both are rejected here.
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
	return nil, false
}

// syncElemType resolves the element type of a synchronously-iterable value
// structurally. A borrow is peeled first — iterating `&xs` yields the same
// elements as `xs` — and an inference variable is coalesced to its structural
// lower-bound shape, the way inferMatch snapshots a variable scrutinee before
// inspecting it. A tuple yields the union of its elements, so `[1, 2, 3]` yields
// `1 | 2 | 3` and the empty tuple yields `never`. A union yields the union of its
// branches' element types, failing if any branch is not iterable. A sync
// generator yields its Yield slot. Every other type — a primitive, an object, a
// class instance without the M7 iterator protocol — is not iterable.
//
// An inexact tuple `[number, ...]` has an open tail of unknown additional elements, so its
// element type is the join of its listed elements with that unknown tail, which is `unknown`.
// The precise type of the tail needs the Array<T> the tuple approximates, which lands in M7.
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
	return nil, false
}
