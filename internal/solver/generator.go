package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// generatorMember resolves a member read off a generator receiver against the member list
// generatorBody builds. It is the GeneratorType twin of projectedMember, which does the same
// for a class instance.
//
// Like projectedMember it reports a miss here rather than letting it fall through to the
// structural `{name: fieldVar}` requirement in valueProp. That requirement constrains the
// receiver `<: object`, and constrain has no rule taking a generator to an object. A miss
// left to fall through would therefore surface as `cannot constrain Generator <: object`
// rather than naming the member that does not exist.
func (c *checker) generatorMember(lvl int, blame, provNode ast.Node, name string, carrier soltype.Type) (pathResult, bool) {
	g, ok := generatorCarrier(carrier)
	if !ok {
		return pathResult{}, false
	}
	body := c.generatorBody(g)
	member, found := body.ReadMember(name)
	if !found {
		// MissingPropertyError blames the variable standing in the requirement's property
		// slot, so mint one against provNode, the property identifier. The diagnostic then
		// points at `.done` in `it.done` rather than at the whole access, matching where the
		// structural field-requirement path in valueProp puts the same message. The variable
		// carries no bounds and goes nowhere else.
		missing := c.freshAt(lvl)
		c.recordProv(missing, provNode, MemberAccess)
		err := &MissingPropertyError{Sub: body, Super: propReq(name, missing, false), Name: name}
		err.prov, err.site = c.prov, blame
		c.errs = append(c.errs, err)
		return pathResult{value: &soltype.ErrorType{}}, true
	}
	return c.memberValue(lvl, blame, member), true
}

// generatorCarrier reads the generator type a receiver denotes: the type itself, or the
// single generator among an unresolved var's lower bounds. `val it = g()` types `it` as the
// call's fresh result var, so the var arm is what an ordinary `it.next()` goes through. It
// mirrors classCarrier and objectCarrier, and like them it declines a var whose bounds
// disagree, since there is no one member list to read in that case.
func generatorCarrier(t soltype.Type) (*soltype.GeneratorType, bool) {
	switch t := t.(type) {
	case *soltype.GeneratorType:
		return t, true
	case *soltype.TypeVarType:
		var found *soltype.GeneratorType
		for _, lb := range t.LowerBounds {
			g, ok := lb.(*soltype.GeneratorType)
			if !ok {
				continue
			}
			if found != nil && !equalType(found, g) {
				return nil, false
			}
			found = g
		}
		if found != nil {
			return found, true
		}
	}
	return nil, false
}

// generatorBody builds the members a generator exposes to a caller driving it by hand. It
// carries `next` alone. The `return` and `throw` methods, which finish a generator early,
// need no new machinery beyond what next uses here and are not declared yet.
func (c *checker) generatorBody(g *soltype.GeneratorType) *soltype.ObjectType {
	return &soltype.ObjectType{Elems: []soltype.ObjTypeElem{c.generatorNext(g)}}
}

// generatorNext declares the `next` method that advances g, as an overload set of up to two
// arms. Y, R, and N below are g's Yield, Ret, and Next slots. N is the type a `yield`
// expression in the body evaluates to, the value a caller sends back in.
//
//	next() -> {value: Y | R, done: boolean}
//	next(value: N) -> {value: Y | R, done: boolean}
//
// Both arms carry g's raise, so a caller that drives a generator by hand handles what its
// body throws the way an iterating caller does.
//
// The no-argument arm is offered only when `undefined` is assignable to N. Omitting the
// argument sends `undefined` at runtime, so an unconditional no-argument arm would let a body
// that reads its sent value as a string receive `undefined` instead. An inferred N is
// `unknown`, which accepts `undefined`, so the argumentless call stays available for every
// generator that does not declare a narrower N. A generator that declares one, such as
// `Generator<number, string, string>`, offers only the one-argument arm, and a bare
// `it.next()` on it is reported as a call missing an argument.
//
// Declaring the parameter optional, `next(value?: N)`, would not close that hole, because an
// optional parameter lets `undefined` arrive for exactly the same reason.
func (c *checker) generatorNext(g *soltype.GeneratorType) *soltype.MethodElem {
	result := c.iterationResult(g)
	sigs := make([]*soltype.FuncType, 0, 2)
	if c.acceptsUndefined(g.Next) {
		sigs = append(sigs, advanceSig(g, result, nil))
	}
	sigs = append(sigs, advanceSig(g, result, []*soltype.FuncParam{
		{Pattern: &soltype.IdentPat{Name: "value"}, Type: g.Next},
	}))
	return &soltype.MethodElem{Name: "next", Signatures: sigs}
}

// advanceSig builds one signature of a method that advances g and evaluates to result.
//
// A sync generator runs its body during the call, so the call is where the body's raise
// escapes. The signature's Throws is therefore g's own raise slot, and constrain's covariant
// throws rule records it into the calling body's sink. A nil slot is the shorthand for
// `never`, so a generator that cannot raise contributes nothing.
//
// An async generator returns a promise instead, and its body's raise surfaces as that
// promise's rejection. Putting the slot in the promise's Err rather than on the signature
// means `it.next()` alone raises nothing and `await it.next()` is what reaches the awaiting
// body's sink.
func advanceSig(g *soltype.GeneratorType, result soltype.Type, params []*soltype.FuncParam) *soltype.FuncType {
	if g.Async {
		return &soltype.FuncType{Params: params, Ret: &soltype.PromiseType{Inner: result, Err: g.Throws}}
	}
	return &soltype.FuncType{Params: params, Ret: result, Throws: g.Throws}
}

// iterationResult builds the object an advance of g evaluates to, `{value: Y | R, done:
// boolean}`. It stands in for the standard library's `IteratorResult<Y, R>`, one of the
// opaque prelude placeholders until library type ingestion lands. A running generator reports
// the type it yields and a finished one the type it returns, so the value slot is the union
// of both.
//
// The precise form is the discriminated union `{value: Y, done: false} | {value: R, done:
// true}`, which lets a reader recover Y alone after testing `done`. This wider object is a
// supertype of it, so reading a result through it is sound but coarser.
func (c *checker) iterationResult(g *soltype.GeneratorType) soltype.Type {
	return &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
		&soltype.PropertyElem{Name: "value", Type: newUnion(c.ctx, []soltype.Type{g.Yield, g.Ret}, false)},
		&soltype.PropertyElem{Name: "done", Type: &soltype.PrimType{Prim: soltype.BoolPrim}},
	}}
}

// acceptsUndefined reports whether `undefined` is assignable to t, the question that decides
// whether generatorNext offers its no-argument arm. The trial runs under a throwaway probe,
// so a t that is still an unsolved variable picks up no bound from being asked.
func (c *checker) acceptsUndefined(t soltype.Type) bool {
	ok, _ := c.ctx.trialMutatesBounds(&soltype.UndefinedType{}, t, newSeenPairs(), false)
	return ok
}
