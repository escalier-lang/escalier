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
// and the real Iterable/Iterator stdlib types that both land in M7. Until then M5
// resolves the element type STRUCTURALLY over the types the solver can
// represent — a tuple, the solver's stand-in for an array, and a union of
// tuples — and rejects everything else as non-iterable. See iterableElemType.
//
// A `for await` outside an `async fn` is a WALK rejection symmetric to
// AwaitOutsideAsyncError: the iterable and body are still walked so their own
// errors surface. A `for await` over any structural operand is rejected by the
// type rule, since no async iterable is representable yet. A sync iterable is not
// an AsyncIterable.
//
// The loop contributes Void to its enclosing block — a loop is a statement, not a
// value. The CFG builder already decomposes a ForInStmt into a header, a body
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

	// The loop body runs in its own scope so the loop variable is invisible after
	// the loop. bindPattern binds each leaf as a monomorphic, non-reassignable
	// binding — a loop variable is rebound by iteration, never by assignment — since
	// a leaf's ValueBinding is left at ValKind, the immutable default. A `for mut x`
	// still binds a mutable owned value, because bindPattern reads the `mut` marker
	// off the pattern and the tuple-element scrutinee is owned.
	bodyScope := scope.Child()
	c.bindPattern(bodyScope, lvl, s.Pattern, elem, nil)
	c.inferBlock(bodyScope, lvl, &s.Body)
	return &soltype.Void{}
}

// iterableElemType resolves the element type T yielded by iterating a value of
// type t, returning ok=false when t is not iterable in the current sense.
//
// For a `for await`, T must come from an AsyncIterable. No async iterable is
// representable in the solver yet — the real AsyncIterable stdlib type and the
// symbol-keyed protocol land in M7 — so a `for await` over any structural operand
// returns false, which is how a sync iterable is rejected by the type rule.
//
// For a sync `for`, the resolution is structural (see syncElemType): a tuple
// yields the union of its element types, a union yields the union of its
// branches' element types, and every other type is not iterable.
func (c *checker) iterableElemType(await bool, t soltype.Type) (soltype.Type, bool) {
	if await {
		return nil, false
	}
	return c.syncElemType(t)
}

// syncElemType resolves the element type of a synchronously-iterable value
// structurally. A borrow is peeled first — iterating `&xs` yields the same
// elements as `xs` — and an inference variable is coalesced to its structural
// lower-bound shape, the way inferMatch snapshots a variable scrutinee before
// inspecting it. A tuple yields the union of its elements, so `[1, 2, 3]` yields
// `1 | 2 | 3` and the empty tuple yields `never`. A union yields the union of its
// branches' element types, failing if any branch is not iterable. Every other
// type — a primitive, an object, a class instance without the M7 iterator
// protocol — is not iterable.
//
// An inexact tuple's trailing `...` tail is not resolved: the element union
// covers only the listed elements. The precise element type of the open tail
// needs the Array<T> the tuple approximates, which lands in M7.
func (c *checker) syncElemType(t soltype.Type) (soltype.Type, bool) {
	t = soltype.CarrierOf(t)
	if _, isVar := t.(*soltype.TypeVarType); isVar {
		t = soltype.CarrierOf(coalesce(t, soltype.Positive))
	}
	switch t := t.(type) {
	case *soltype.TupleType:
		return newUnion(c.ctx, t.Elems, false), true
	case *soltype.UnionType:
		elems := make([]soltype.Type, 0, len(t.Types))
		for _, branch := range t.Types {
			e, ok := c.syncElemType(branch)
			if !ok {
				return nil, false
			}
			elems = append(elems, e)
		}
		return newUnion(c.ctx, elems, false), true
	}
	return nil, false
}
