package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// callOnlyParam is a parameter of a body-carrying function whose written type is a function
// with no retention lifetime. Such a parameter is only called during the call. ft is the copy
// of that type the body's binding holds. Its Lt is the parameter's call lifetime, a fresh
// lifetime at the level of the function's parameters that ends when the call returns.
type callOnlyParam struct {
	name string
	node ast.Node
	ft   *soltype.FuncType
	// outerMark is the lifetime counter before the function's parameters resolved, for a
	// function written inside another body. Every lifetime minted before it belongs to the enclosing body
	// or to the module. It is -1 for a function with no enclosing body.
	outerMark int
	// deferred is the phrase deferredBody returns for the declaring function. It is "" for a
	// function whose body runs only during the call.
	deferred string
}

// deferredBody returns the phrase an error names a function by when its body can run after the
// call that starts it returns, such as "a generator". It returns "" for any other function.
func deferredBody(sig ast.FuncSig) string {
	switch {
	case sig.Gen:
		return "a generator"
	case sig.Async:
		return "an async function"
	}
	return ""
}

// watchCallOnlyReads makes noteCallOnlyRead record each read of ft, the body type of a
// call-only parameter.
func (c *checker) watchCallOnlyReads(ft *soltype.FuncType) {
	if c.watchedCallOnly == nil {
		c.watchedCallOnly = set.NewSet[*soltype.FuncType]()
	}
	c.watchedCallOnly.Add(ft)
}

// noteCallOnlyRead records t, the value a name read resolved to, when watchCallOnlyReads
// watches it.
func (c *checker) noteCallOnlyRead(t soltype.Type) {
	ft, isFn := t.(*soltype.FuncType)
	if !isFn || !c.watchedCallOnly.Contains(ft) {
		return
	}
	if c.readCallOnly == nil {
		c.readCallOnly = set.NewSet[*soltype.FuncType]()
	}
	c.readCallOnly.Add(ft)
}

// callOnlyCheck is one parameter waiting for checkCallOnlyParams. sig is the type of the
// function that declares it.
type callOnlyCheck struct {
	param callOnlyParam
	sig   *soltype.FuncType
}

// queueCallOnlyChecks queues a check of each parameter for checkCallOnlyParams. sig is the type
// of the function that declares the parameters.
func (c *checker) queueCallOnlyChecks(params []callOnlyParam, sig *soltype.FuncType) {
	for _, p := range params {
		c.pendingCallOnly = append(c.pendingCallOnly, callOnlyCheck{param: p, sig: sig})
	}
}

// checkCallOnlyParams reports each queued parameter whose body keeps it past the call. A body
// keeps it past the call when it requires the call lifetime to outlive one of these. Each item
// names the TestCallOnlyFunc cases that reach it.
//
//   - 'static. See "passed on to a static parameter" and "stored into a static field of a
//     borrowed parameter".
//   - A lifetime the function's own type holds, since a caller reads its arguments and its
//     result after the call. See "returned" and "stored into a retained field of a borrowed
//     parameter".
//   - A lifetime at a lower level than the call lifetime, which belongs to the module or to an
//     enclosing body. See "stored into a retained field of an enclosing function's parameter".
//   - For a function written inside another body, a lifetime minted before its parameters
//     resolved. The enclosing body minted it, possibly at the same level. See "stored into a
//     retained field of an enclosing function's parameter".
//   - An elided lifetime, which a binding of the enclosing body holds in its display type. See
//     "stored into a binding of the enclosing body".
//
// Any other lifetime is one the body mints itself, or a parameter lifetime of a function
// inferred together with this one. The call lifetime may outlive those.
//
// The body of an `async` or `gen` function can run after the call returns, so such a body
// keeps each parameter it reads past the call. See "read by a generator" and "read by an async
// function".
//
// It runs once the enclosing component has solved, since a constraint a sibling's body records
// can reach the call lifetime after this function's body is walked.
func (c *checker) checkCallOnlyParams() {
	pending := c.pendingCallOnly
	c.pendingCallOnly = nil
	sigOcc := map[*soltype.FuncType]map[*soltype.LifetimeVar]occPolarity{}
	for _, check := range pending {
		p := check.param
		lt := p.ft.Lt.(*soltype.LifetimeVar)
		occ, walked := sigOcc[check.sig]
		if !walked {
			occ, _ = walkLtOcc(check.sig, soltype.Positive)
			sigOcc[check.sig] = occ
		}
		_, outside := occ[lt]
		static := false
		for _, kept := range outlivedBy(lt) {
			switch kept := kept.(type) {
			case *soltype.StaticLifetime:
				static = true
			case *soltype.AnonLifetime:
				// An elided lifetime is one a binding's display type kept in place of the
				// variable it came from, so what it stands for cannot be told apart from a
				// lifetime outside the call.
				outside = true
			case *soltype.LifetimeVar:
				_, inSig := occ[kept]
				outside = outside || inSig || kept.Level < lt.Level || kept.ID < p.outerMark
			}
		}
		switch {
		case static || outside:
			c.report(&CallOnlyFuncKeptError{Name: p.name, Static: static, Param: p.node})
		case p.deferred != "" && c.readCallOnly.Contains(p.ft):
			c.report(&CallOnlyFuncKeptError{Name: p.name, Deferred: p.deferred, Param: p.node})
		}
	}
}

// outlivedBy returns every lifetime lt is required to outlive, read through the upper bounds of
// lt and of each variable reached. lt itself is not included.
func outlivedBy(lt *soltype.LifetimeVar) []soltype.Lifetime {
	var out []soltype.Lifetime
	seen := set.NewSet[*soltype.LifetimeVar]()
	seen.Add(lt)
	pending := append([]soltype.Lifetime(nil), lt.UpperBounds...)
	for len(pending) > 0 {
		cur := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		v, isVar := cur.(*soltype.LifetimeVar)
		if isVar && seen.Contains(v) {
			continue
		}
		out = append(out, cur)
		if isVar {
			seen.Add(v)
			pending = append(pending, v.UpperBounds...)
		}
	}
	return out
}
