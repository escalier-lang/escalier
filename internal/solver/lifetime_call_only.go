package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// callOnlyParam is a parameter of a body-carrying function whose written type is a function
// type with no lifetime, as `g` in `fn f(g: fn () -> undefined)`. Such a parameter is only
// called during the call, unless the signature ties its lifetime to the result. param is the
// parameter's index in the signature. ann is the parameter's annotation. ft is the copy of the
// parameter's type the body's binding holds, which shares the lifetime of the type the
// signature holds.
type callOnlyParam struct {
	name  string
	node  ast.Node
	param int
	// optional marks a parameter written `g?`.
	optional bool
	ann      *ast.FuncTypeAnn
	// lt is the lifetime of the parameter's function type.
	lt *soltype.LifetimeVar
	// outerMark is the lifetime counter before the function's parameters resolved, for a
	// function written inside another body. Every lifetime minted before it belongs to the enclosing body
	// or to the module. It is -1 for a function with no enclosing body.
	outerMark int
}

// isElidedFuncType reports whether ann is a function type written with no lifetime, as
// `fn () -> undefined` is. On a parameter, that form's lifetime ends with the call.
func isElidedFuncType(ann ast.TypeAnn) bool {
	fn, isFn := ann.(*ast.FuncTypeAnn)
	return isFn && fn.Lifetime == nil
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

// noteCallOnlyParam records lt, the lifetime of a call-only parameter, so that funcLtSources
// treats it as one and noteCallOnlyRead watches reads of values that hold it. It must run when
// the parameter is bound, before its function's body is walked.
func (c *checker) noteCallOnlyParam(lt *soltype.LifetimeVar) {
	if c.callOnlyDepth == nil {
		c.callOnlyDepth = map[*soltype.LifetimeVar]int{}
	}
	c.callOnlyDepth[lt] = len(c.deferredBodies)
}

// deferredRead is the first read of a call-only parameter inside an `async` or `gen` body
// entered after the parameter was bound. body is the phrase deferredBody returns for that body,
// and node is the read.
type deferredRead struct {
	body string
	node ast.Node
}

// noteCallOnlyRead records the read node, whose value resolved to t, for each call-only
// parameter whose lifetime t holds, when the read is inside an `async` or `gen` body entered
// after the parameter was bound. t holds the lifetime when a function in t has it, or has a
// lifetime bounded below by it, as a closure that captures the parameter has. So a read of
// the parameter itself counts, and so does a read of an object or array holding it or of a
// closure that calls it.
func (c *checker) noteCallOnlyRead(node ast.Node, t soltype.Type) {
	if len(c.callOnlyDepth) == 0 || len(c.deferredBodies) == 0 {
		return
	}
	for _, lt := range c.callOnlyLtsIn(t) {
		depth := c.callOnlyDepth[lt]
		if depth >= len(c.deferredBodies) {
			continue
		}
		if c.callOnlyDeferredRead == nil {
			c.callOnlyDeferredRead = map[*soltype.LifetimeVar]deferredRead{}
		}
		if _, recorded := c.callOnlyDeferredRead[lt]; !recorded {
			c.callOnlyDeferredRead[lt] = deferredRead{body: c.deferredBodies[depth], node: node}
		}
	}
}

// callOnlyLtsIn returns the lifetimes of call-only parameters that the functions in t hold.
// Each function's lifetime is read through its lower bounds, the origin it proxies, and the
// copies instantiation made of it.
func (c *checker) callOnlyLtsIn(t soltype.Type) []*soltype.LifetimeVar {
	var out []*soltype.LifetimeVar
	seen := set.NewSet[*soltype.LifetimeVar]()
	pending := shortFuncLifetimes(t)
	for len(pending) > 0 {
		v, isVar := pending[len(pending)-1].(*soltype.LifetimeVar)
		pending = pending[:len(pending)-1]
		if !isVar || seen.Contains(v) {
			continue
		}
		seen.Add(v)
		if _, callOnly := c.callOnlyDepth[v]; callOnly {
			out = append(out, v)
			continue
		}
		pending = append(pending, v.LowerBounds...)
		if origin, proxied := c.ctx.ltProxyOrigin[v]; proxied {
			pending = append(pending, origin)
		}
		for _, copied := range c.ctx.ltCopies[v] {
			pending = append(pending, copied)
		}
	}
	return out
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

// sigLifetimes holds the lifetimes a signature names, split by where they occur.
type sigLifetimes struct {
	// result holds the lifetimes in the return and throws types. A caller reads them from
	// the result, so a parameter whose lifetime outlives one is tied to the result.
	result map[*soltype.LifetimeVar]occPolarity
	// params holds the lifetimes in each parameter's type, indexed like the signature's
	// parameters, and in the receiver's type at index -1.
	params map[int]map[*soltype.LifetimeVar]occPolarity
}

// lifetimesOf returns the lifetimes sig names, split by where they occur.
func lifetimesOf(sig *soltype.FuncType) sigLifetimes {
	out := sigLifetimes{result: map[*soltype.LifetimeVar]occPolarity{}, params: map[int]map[*soltype.LifetimeVar]occPolarity{}}
	for _, t := range []soltype.Type{sig.Ret, sig.Throws} {
		if t == nil {
			continue
		}
		occ, _ := walkLtOcc(t, soltype.Positive)
		for lt, pol := range occ {
			out.result[lt] = pol
		}
	}
	if sig.SelfParam != nil {
		out.params[-1], _ = walkLtOcc(sig.SelfParam.Type, soltype.Negative)
	}
	for i, p := range sig.Params {
		out.params[i], _ = walkLtOcc(p.Type, soltype.Negative)
	}
	return out
}

// inOtherParam reports whether lt occurs in a parameter of the signature other than the one at
// index skip.
func (s sigLifetimes) inOtherParam(lt *soltype.LifetimeVar, skip int) bool {
	for i, occ := range s.params {
		if i == skip {
			continue
		}
		if _, found := occ[lt]; found {
			return true
		}
	}
	return false
}

// checkCallOnlyParams reports each queued parameter whose body keeps it past the call. A body
// keeps it past the call when it requires the parameter's lifetime to outlive one of these.
// Each item names the TestCallOnlyFunc cases that reach it.
//
//   - 'static. See "passed on to a static parameter" and "stored into a static field of a
//     borrowed parameter".
//   - A lifetime another parameter's type holds, since a caller reads its arguments after the
//     call. See "stored into a retained field of a borrowed parameter".
//   - A lifetime at a lower level than the parameter's, which belongs to the module or to an
//     enclosing body. See "stored into a retained field of an enclosing function's parameter".
//   - For a function written inside another body, a lifetime minted before its parameters
//     resolved. The enclosing body minted it, possibly at the same level. See "stored into a
//     retained field of an enclosing function's parameter".
//   - An elided lifetime, which a binding of the enclosing body holds in its display type. See
//     "stored into a binding of the enclosing body".
//
// A lifetime only the result's type holds ties the parameter to the result, which the signature
// shows, so it is not reported. See "returned" and "captured by a returned closure". A lifetime
// the result shares with another parameter is reported, since that parameter's argument holds
// it too. See "stored into a named-lifetime field of a parameter the result returns". Any other
// lifetime is one the body mints itself, or a parameter lifetime of a function inferred
// together with this one. The parameter's lifetime may outlive those.
//
// The body of an `async` or `gen` function can run after the call that starts it returns. So
// such a body keeps past the call each parameter it reads, whether the parameter is its own or
// one of an enclosing function. See "read by a generator", "read by an async function", and
// "read by an async closure".
//
// It runs once the enclosing component has solved, since a constraint a sibling's body records
// can reach the parameter's lifetime after this function's body is walked.
func (c *checker) checkCallOnlyParams() {
	pending := c.pendingCallOnly
	c.pendingCallOnly = nil
	sigLts := map[*soltype.FuncType]sigLifetimes{}
	for _, check := range pending {
		p := check.param
		lt := p.lt
		lts, walked := sigLts[check.sig]
		if !walked {
			lts = lifetimesOf(check.sig)
			sigLts[check.sig] = lts
		}
		outside := false
		static := false
		// keeper names a lifetime of this signature that another parameter holds and the
		// parameter must outlive, which the parameter could take instead of 'static.
		keeper := ""
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
				inOther := lts.inOtherParam(kept, p.param)
				if _, inResult := lts.result[kept]; inResult && !inOther {
					continue
				}
				if name, named := c.signatureLts[kept]; named && inOther && keeper == "" {
					keeper = name
				}
				outside = outside || inOther || kept.Level < lt.Level || kept.ID < p.outerMark
			}
		}
		if static || outside {
			// The lifetime bounds do not record where the body added them, so the error
			// blames the parameter.
			if static {
				keeper = ""
			}
			c.report(&CallOnlyFuncKeptError{Name: p.name, Optional: p.optional, Written: p.ann, Static: static, Keeper: keeper, Param: p.node})
		} else if read, isRead := c.callOnlyDeferredRead[lt]; isRead {
			c.report(&CallOnlyFuncKeptError{Name: p.name, Optional: p.optional, Written: p.ann, Deferred: read.body, Param: p.node, Read: read.node})
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
