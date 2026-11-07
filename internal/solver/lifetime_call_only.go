package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// callOnlyParam is a parameter of a body-carrying function whose written type is a function
// with no retention lifetime. Such a parameter is only called during the call. written is that
// type as the signature holds it. ft is the copy of it the body's binding holds. Its Lt is the
// parameter's call lifetime, a fresh lifetime at the level of the function's parameters that
// ends when the call returns.
type callOnlyParam struct {
	name    string
	node    ast.Node
	written *soltype.FuncType
	ft      *soltype.FuncType
	// outerMark is the lifetime counter before the function's parameters resolved, for a
	// function written inside another body. Every lifetime minted before it belongs to the enclosing body
	// or to the module. It is -1 for a function with no enclosing body.
	outerMark int
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

// watchCallOnlyReads makes noteCallOnlyRead record a read of ft, the body type of a call-only
// parameter, made inside an `async` or `gen` body entered after this call. It must run when the
// parameter is bound, before its function's body is walked.
func (c *checker) watchCallOnlyReads(ft *soltype.FuncType) {
	if c.callOnlyDepth == nil {
		c.callOnlyDepth = map[*soltype.FuncType]int{}
	}
	c.callOnlyDepth[ft] = len(c.deferredBodies)
}

// deferredRead is the first read of a call-only parameter inside an `async` or `gen` body
// entered after the parameter was bound. body is the phrase deferredBody returns for that body,
// and node is the read.
type deferredRead struct {
	body string
	node ast.Node
}

// noteCallOnlyRead records the read node, whose value resolved to t, when watchCallOnlyReads
// watches t and the read is inside an `async` or `gen` body entered after the parameter was
// bound.
func (c *checker) noteCallOnlyRead(node ast.Node, t soltype.Type) {
	ft, isFn := t.(*soltype.FuncType)
	if !isFn {
		return
	}
	depth, watched := c.callOnlyDepth[ft]
	if !watched || depth >= len(c.deferredBodies) {
		return
	}
	if c.callOnlyDeferredRead == nil {
		c.callOnlyDeferredRead = map[*soltype.FuncType]deferredRead{}
	}
	if _, recorded := c.callOnlyDeferredRead[ft]; !recorded {
		c.callOnlyDeferredRead[ft] = deferredRead{body: c.deferredBodies[depth], node: node}
	}
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
// The body of an `async` or `gen` function can run after the call that starts it returns. So
// such a body keeps past the call each parameter it reads, whether the parameter is its own or
// one of an enclosing function. See "read by a generator", "read by an async function", and
// "read by an async closure".
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
		if static || outside {
			// The lifetime bounds do not record where the body added them, so the error
			// blames the parameter.
			c.report(&CallOnlyFuncKeptError{Name: p.name, Written: p.written, Static: static, Param: p.node})
		} else if read, isRead := c.callOnlyDeferredRead[p.ft]; isRead {
			c.report(&CallOnlyFuncKeptError{Name: p.name, Written: p.written, Deferred: read.body, Param: p.node, Read: read.node})
		}
	}
}

// withRetentionLifetimes returns t with a retention lifetime on each function type that has
// none. mint returns the lifetime for one function type. The walk reaches function types through
// unions, intersections, tuple elements, object properties, and type arguments. It does not
// enter a function type, so a callback parameter inside one stays call-only. It skips an
// object's method signatures, since a method is not a value the object holds. t is not
// modified, and each copied node takes the provenance of the node it copies.
func (c *checker) withRetentionLifetimes(t soltype.Type, mint func() soltype.Lifetime) soltype.Type {
	return t.Accept(&retentionMinter{c: c, mint: mint, methods: set.NewSet[*soltype.FuncType]()}, soltype.Positive)
}

// bindingLifetime returns a function that mints the lifetime of a binding declared at lvl. A
// module-level binding lives for the rest of the program, so its lifetime is 'static. A binding
// in a body gets a fresh lifetime at lvl on each call.
func (c *checker) bindingLifetime(lvl int) func() soltype.Lifetime {
	if c.fn == nil {
		return func() soltype.Lifetime { return soltype.Static }
	}
	return func() soltype.Lifetime { return c.ctx.freshLifetime(lvl) }
}

// retentionMinter is the visitor withRetentionLifetimes runs.
type retentionMinter struct {
	c    *checker
	mint func() soltype.Lifetime
	// methods holds the signatures of each object's methods, constructors, and call
	// signatures, which the walk leaves unchanged.
	methods set.Set[*soltype.FuncType]
	// entered holds each node the walk is inside, innermost last, so ExitType can copy the
	// provenance of the node it rebuilt.
	entered []soltype.Type
}

func (m *retentionMinter) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	m.entered = append(m.entered, t)
	switch t := t.(type) {
	case *soltype.FuncType:
		if t.Lt != nil || m.methods.Contains(t) {
			return soltype.EnterResult{SkipChildren: true}
		}
		retained := *t
		retained.Lt = m.mint()
		return soltype.EnterResult{Type: &retained, SkipChildren: true}
	case *soltype.ObjectType:
		for _, elem := range t.Elems {
			switch elem := elem.(type) {
			case *soltype.MethodElem:
				m.skipSignatures(elem.Signatures)
			case *soltype.ConstructorElem:
				m.skipSignatures(elem.Signatures)
			case *soltype.CallableElem:
				m.skipSignatures(elem.Signatures)
			}
		}
	}
	return soltype.EnterResult{}
}

func (m *retentionMinter) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type {
	original := m.entered[len(m.entered)-1]
	m.entered = m.entered[:len(m.entered)-1]
	if t == original {
		return t
	}
	if origin, ok := m.c.prov.get(original); ok {
		if fromAST, isAST := origin.(FromAST); isAST {
			m.c.recordProv(t, fromAST.Node, fromAST.Kind)
		}
	}
	return t
}

// skipSignatures adds sigs to the signatures the walk leaves unchanged.
func (m *retentionMinter) skipSignatures(sigs []*soltype.FuncType) {
	for _, sig := range sigs {
		m.methods.Add(sig)
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
