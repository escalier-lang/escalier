package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- M6.5 PR6: check inferred lifetime bounds against declared ones ---
//
// A body-carrying function must prove every outlives relation its signature declares.
// A declared `<'a: 'b>` asserts 'a outlives 'b; the body's borrows, joins, and stores
// must make it hold. The inferred relation is read the same way the printer renders it,
// so a bound the signature declares and the printer would render are accepted together.
// A declared bound the inference does not prove is a LifetimeBoundNotSatisfiedError.

// firstBoundError returns the first LifetimeBoundNotSatisfiedError among errs, or nil.
func firstBoundError(errs []SolverError) *LifetimeBoundNotSatisfiedError {
	for _, e := range errs {
		if be, ok := e.(*LifetimeBoundNotSatisfiedError); ok {
			return be
		}
	}
	return nil
}

// A multi-source join declared with the bounds inference produces round-trips: each
// source outlives the join, so `<'a: 'c, 'b: 'c, 'c>` is exactly what the body proves.
// No LifetimeBoundNotSatisfiedError fires and the signature renders as written.
func TestInferDeclaredJoinBoundSatisfied(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn pick<'a: 'c, 'b: 'c, 'c>(p: &'a mut {x: number}, q: &'b mut {x: number}) -> &'c mut {x: number} {
			if true { return p } else { return q }
		}`)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a: 'c, 'b: 'c, 'c>(p: &'a mut {x: number}, q: &'b mut {x: number}) -> &'c mut {x: number}",
		values["pick"])
}

// A declared bound whose left lifetime borrows a parameter the body never relates is
// unfounded. `q` connects nothing, so the body proves nothing about 'b, and the declared
// 'a: 'b cannot hold.
func TestInferDeclaredBoundUnusedParamUnsatisfied(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f<'a: 'b>(p: &'a mut {x: number}, q: &'b mut {x: number}) -> &'a mut {x: number} {
			return p
		}`)
	be := firstBoundError(errs)
	require.NotNil(t, be, "expected a LifetimeBoundNotSatisfiedError; got %v", errs)
	require.Equal(t, "a", be.Sub)
	require.Equal(t, "b", be.Super)
	require.Equal(t,
		"declared lifetime bound 'a: 'b is not satisfied; the body does not make 'a outlive 'b",
		be.Message())
}

// A join relates each source to the join, not the reverse. The meet bound `<'c: 'a & 'b>`
// claims the join outlives both its sources, which the body does not prove, so one error
// fires per source the reversed bound names.
func TestInferDeclaredJoinBoundReversedUnsatisfied(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn pick<'a, 'b, 'c: 'a & 'b>(p: &'a mut {x: number}, q: &'b mut {x: number}) -> &'c mut {x: number} {
			if true { return p } else { return q }
		}`)
	var messages []string
	for _, e := range errs {
		if _, ok := e.(*LifetimeBoundNotSatisfiedError); ok {
			messages = append(messages, e.Message())
		}
	}
	require.Equal(t,
		[]string{
			"declared lifetime bound 'c: 'a is not satisfied; the body does not make 'c outlive 'a",
			"declared lifetime bound 'c: 'b is not satisfied; the body does not make 'c outlive 'b",
		},
		messages)
}

// A lifetime trivially outlives itself, so a self-bound `<'a: 'a>` always holds and
// fires no error.
func TestInferDeclaredSelfBoundSatisfied(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f<'a: 'a>(p: &'a mut {x: number}) -> &'a mut {x: number} { return p }`)
	require.Empty(t, errs)
}

// `<'a: 'static>` forces 'a to 'static, and only a 'static-forced lifetime outlives
// 'static. An ordinary borrow parameter is not forced to 'static, so the bound is
// unsatisfied.
func TestInferDeclaredStaticBoundUnsatisfied(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f<'a: 'static>(p: &'a mut {x: number}) -> &'a mut {x: number} { return p }`)
	be := firstBoundError(errs)
	require.NotNil(t, be, "expected a LifetimeBoundNotSatisfiedError; got %v", errs)
	require.Equal(t, "a", be.Sub)
	require.Equal(t, "static", be.Super)
	require.Equal(t,
		"declared lifetime bound 'a: 'static is not satisfied; the body does not make 'a outlive 'static",
		be.Message())
}

// A borrow stored into module-level storage escapes its region, forcing its lifetime to
// 'static. The body then proves the declared `<'a: 'static>`, so no error fires and the
// parameter renders under 'static.
func TestInferDeclaredStaticBoundSatisfiedByEscape(t *testing.T) {
	values, _, errs := inferSource(t, `
		var sink = {x: 0}
		fn cache<'a: 'static>(p: &'a mut {x: number}) { sink = p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: &'static mut {x: number}) -> void", values["cache"])
}

// A bare `<'a, 'b>` declares no bounds, so the check does nothing and the inferred join
// still renders its bounds. This guards the common inferred-render path from a spurious
// bound error.
func TestInferNoDeclaredBoundStillRendersInferred(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn pick<'a, 'b, 'c>(p: &'a mut {x: number}, q: &'b mut {x: number}) -> &'c mut {x: number} {
			if true { return p } else { return q }
		}`)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a: 'c, 'b: 'c, 'c>(p: &'a mut {x: number}, q: &'b mut {x: number}) -> &'c mut {x: number}",
		values["pick"])
}

// A nested borrow `&'a &'b &'c` collapses to `&'a` with 'c outliving 'b outliving 'a, so
// the graph proves 'c: 'a transitively even though 'b and 'c are elided from the render.
// A declared 'c: 'a is redundant and accepted, and the reduced signature still renders
// under the single 'a.
func TestInferDeclaredTransitiveBoundSatisfied(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f<'a, 'b, 'c: 'a>(p: &'a &'b &'c {x: number}) -> &'a {x: number} { return p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: &'a {x: number}) -> &'a {x: number}", values["f"])
}

// A lifetime with only a lower-bound 'static is not forced to 'static. A lower-bound
// 'static reads "'static outlives 'a", which is trivially true and proves nothing, so it
// must not satisfy a bound on 'a. Only an upper-bound 'static, the escape constraint 'a
// <: 'static, forces 'a to 'static. The check reads the escape set the bound set records
// from upper bounds, not forcedToStatic which reads both directions.
//
// The check runs directly because current surface syntax does not put a lower-bound
// 'static on a parameter lifetime. The store paths that would are deferred to M7.
func TestCheckDeclaredBoundLowerBoundStaticNotForced(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)
	c.namedLifetimes = map[string]*soltype.LifetimeVar{"a": a, "b": b}
	// constrainLt('static, 'a) records 'static as a lower bound of 'a, forcing nothing.
	c.ctx.constrainLt(soltype.Static, a)

	ft := &soltype.FuncType{
		Params: []*soltype.FuncParam{
			{Pattern: &soltype.IdentPat{Name: "p"}, Type: &soltype.RefType{Mut: true, Lt: a, Inner: c.freshAt(0)}},
			{Pattern: &soltype.IdentPat{Name: "q"}, Type: &soltype.RefType{Mut: true, Lt: b, Inner: c.freshAt(0)}},
		},
		Ret: &soltype.Void{},
	}
	// <'a: 'b>: nothing relates 'a to 'b, so the bound is unfounded.
	params := []*ast.LifetimeParam{
		ast.NewLifetimeParam("a", []*ast.LifetimeAnn{ast.NewLifetimeAnn("b", ast.Span{})}, ast.Span{}),
		ast.NewLifetimeParam("b", nil, ast.Span{}),
	}

	c.checkDeclaredLifetimeBounds(params, ft)

	require.Len(t, c.errs, 1, "a lower-bound 'static must not satisfy an unfounded bound")
	be, ok := c.errs[0].(*LifetimeBoundNotSatisfiedError)
	require.True(t, ok)
	require.Equal(t, "a", be.Sub)
	require.Equal(t, "b", be.Super)
}
