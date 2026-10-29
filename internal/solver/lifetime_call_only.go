package solver

import (
	"maps"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// callOnlyParam is a parameter of a body-carrying function whose written type is a function
// with no retention lifetime. Such a parameter is only called during the call. ft is the copy
// of that type the body's binding holds, and its Lt is the parameter's call lifetime once
// mintCallLifetimes has run. The call lifetime is the lifetime the body sees the parameter at,
// and it ends when the call returns.
type callOnlyParam struct {
	name string
	node ast.Node
	ft   *soltype.FuncType
	// mark is the lifetime counter before the call lifetime was minted. Every lifetime minted
	// earlier belongs to the signature or to something outside the function.
	mark int
}

// callOnlyCheck is one parameter waiting for checkCallOnlyParams. ret is the return type of
// the function that declares it.
type callOnlyCheck struct {
	param callOnlyParam
	ret   soltype.Type
}

// mintCallLifetimes sets each parameter's retention lifetime to a fresh lifetime at level lvl,
// its call lifetime. It must run once the whole signature is resolved and before the body is
// walked, so the lifetimes checkCallOnlyParams reads as outside the call are exactly the ones
// minted before it.
func (c *checker) mintCallLifetimes(lvl int, params []callOnlyParam) {
	mark := c.ctx.lifetimeCounter
	for i := range params {
		params[i].ft.Lt = c.ctx.freshLifetime(lvl)
		params[i].mark = mark
	}
}

// queueCallOnlyChecks queues a check of each parameter for checkCallOnlyParams. ret is the
// return type of the function that declares the parameters.
func (c *checker) queueCallOnlyChecks(params []callOnlyParam, ret soltype.Type) {
	for _, p := range params {
		c.pendingCallOnly = append(c.pendingCallOnly, callOnlyCheck{param: p, ret: ret})
	}
}

// checkCallOnlyParams reports each queued parameter whose body keeps it past the call. A body
// keeps it past the call when it requires the call lifetime to outlive one of these:
//
//   - 'static.
//   - A lifetime minted before the call lifetime, such as one in the signature or in an
//     enclosing body.
//   - A lifetime the function's return type holds, since the caller reads the result after
//     the call.
//
// A lifetime the body mints itself ends with the call at the latest, so the call lifetime may
// outlive it.
//
// It runs once the enclosing component has solved, since a constraint a sibling's body records
// can reach the call lifetime after this function's body is walked.
func (c *checker) checkCallOnlyParams() {
	pending := c.pendingCallOnly
	c.pendingCallOnly = nil
	if len(pending) == 0 {
		return
	}
	var seeds []*soltype.LifetimeVar
	returned := make([][]*soltype.LifetimeVar, len(pending))
	for i, check := range pending {
		seeds = append(seeds, check.param.ft.Lt.(*soltype.LifetimeVar))
		occ, _ := walkLtOcc(check.ret, soltype.Positive)
		// Sorted so the lifetime a violation is found against is the same on every run.
		returned[i] = slices.SortedFunc(maps.Keys(occ), func(a, b *soltype.LifetimeVar) int { return a.ID - b.ID })
		seeds = append(seeds, returned[i]...)
	}
	graph := buildLtBoundSet(occOf(seeds))
	ids := slices.Sorted(maps.Keys(graph.rep))
	for i, check := range pending {
		p := check.param
		lt := p.ft.Lt.(*soltype.LifetimeVar)
		if graph.static.Contains(graph.repOf(lt.ID)) {
			c.report(&CallOnlyFuncKeptError{Name: p.name, Static: true, Param: p.node})
			continue
		}
		kept := slices.ContainsFunc(returned[i], func(r *soltype.LifetimeVar) bool {
			return graph.implies(lt.ID, r.ID)
		}) || slices.ContainsFunc(ids, func(id int) bool {
			return id < p.mark && graph.implies(lt.ID, id)
		})
		if kept {
			c.report(&CallOnlyFuncKeptError{Name: p.name, Param: p.node})
		}
	}
}
