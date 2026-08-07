package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// generatorMember resolves a member read off a generator receiver against the member list
// generatorBody builds. It is the GeneratorType twin of projectedMember.
//
// Like projectedMember it reports a miss here. Falling through to the structural
// `{name: fieldVar}` requirement in valueProp would constrain the receiver `<: object`, and
// constrain has no rule taking a generator to an object, so an unknown member would surface as
// `cannot constrain Generator <: object` instead of being named.
func (c *checker) generatorMember(lvl int, blame, provNode ast.Node, name string, carrier soltype.Type) (pathResult, bool) {
	g, ok := generatorCarrier(c.ctx, carrier)
	if !ok {
		return pathResult{}, false
	}
	body := c.generatorBody(g)
	member, found := body.ReadMember(name)
	if !found {
		// MissingPropertyError blames the variable in the requirement's property slot, so mint
		// one against provNode, the property identifier. The diagnostic then points at `.done`
		// in `it.done` rather than at the whole access, matching valueProp's structural path.
		// The variable carries no bounds and goes nowhere else.
		missing := c.freshAt(lvl)
		c.recordProv(missing, provNode, MemberAccess)
		err := &MissingPropertyError{Sub: body, Super: propReq(name, missing, false), Name: name}
		err.prov, err.site = c.prov, blame
		c.errs = append(c.errs, err)
		return pathResult{value: &soltype.ErrorType{}}, true
	}
	return c.memberValue(lvl, blame, member), true
}

// generatorCarrier reads the generator a member access on t goes through, folding the several a
// receiver may stand for into one. A single call to a polymorphic generator leaves two lower
// bounds differing only in their fresh slot variables, and `if b { g() } else { h() }` leaves
// one bound per branch.
//
// A receiver standing for anything besides generators is declined, so `if b { g() } else { 5 }`
// keeps the structural field-requirement path, where the non-generator part is rejected. A var
// with no lower bound is declined too, since nothing yet says it holds a generator. That is what
// an unannotated parameter is, so `fn f(it) { it.next() }` still infers `{next: fn () -> T0}`.
func generatorCarrier(c *Context, t soltype.Type) (*soltype.GeneratorType, bool) {
	parts, ok := generatorParts(t)
	if !ok || len(parts) == 0 {
		return nil, false
	}
	return joinGenerators(c, parts)
}

// generatorParts collects the generators a receiver stands for, reporting false as soon as it
// stands for anything else. readCarrier peels a borrow first, so `mut Generator<…>` arrives
// here bare.
func generatorParts(t soltype.Type) ([]*soltype.GeneratorType, bool) {
	switch t := t.(type) {
	case *soltype.GeneratorType:
		return []*soltype.GeneratorType{t}, true
	case *soltype.UnionType:
		parts := make([]*soltype.GeneratorType, 0, len(t.Types))
		for _, member := range t.Types {
			g, ok := member.(*soltype.GeneratorType)
			if !ok {
				return nil, false
			}
			parts = append(parts, g)
		}
		return parts, true
	case *soltype.TypeVarType:
		parts := make([]*soltype.GeneratorType, 0, len(t.LowerBounds))
		for _, lb := range t.LowerBounds {
			if lb == soltype.Type(t) {
				// A vacuous `v <: v` self-edge constrains nothing, so it says nothing about
				// what the receiver holds. readCarrier drops the same bound.
				continue
			}
			g, ok := lb.(*soltype.GeneratorType)
			if !ok {
				return nil, false
			}
			parts = append(parts, g)
		}
		return parts, true
	}
	return nil, false
}

// joinGenerators folds several generators into the least generator every input is a subtype of.
// Yield, Ret, and Throws are covariant, so the join unions each. Next is contravariant, being
// the value a caller sends in, so the join intersects it: what the caller sends has to be
// acceptable to every generator the receiver may hold. A sync and an async generator are
// unrelated under subtyping, so a receiver mixing the two is declined.
func joinGenerators(c *Context, gs []*soltype.GeneratorType) (*soltype.GeneratorType, bool) {
	if len(gs) == 1 {
		return gs[0], true
	}
	yields := make([]soltype.Type, len(gs))
	rets := make([]soltype.Type, len(gs))
	nexts := make([]soltype.Type, len(gs))
	throws := make([]soltype.Type, len(gs))
	for i, g := range gs {
		if g.Async != gs[0].Async {
			return nil, false
		}
		yields[i], rets[i], nexts[i] = g.Yield, g.Ret, g.Next
		throws[i] = g.ThrowsOrNever()
	}
	return &soltype.GeneratorType{
		Yield:  newUnion(c, yields, false),
		Ret:    newUnion(c, rets, false),
		Next:   newIntersection(c, nexts),
		Throws: newUnion(c, throws, false),
		Async:  gs[0].Async,
	}, true
}

// generatorBody builds the members a generator exposes to a caller driving it by hand. It
// carries `next` alone. The `return` and `throw` methods, which finish a generator early, are
// not declared yet and need no machinery beyond what `next` uses here.
func (c *checker) generatorBody(g *soltype.GeneratorType) *soltype.ObjectType {
	return &soltype.ObjectType{Elems: []soltype.ObjTypeElem{c.generatorNext(g)}}
}

// generatorNext declares the `next` method that advances g, as an overload set of up to two
// arms over the result iterationResult builds. N is g's Next slot, the value a caller sends in.
//
//	next() -> IteratorResult<Y, R>
//	next(value: N) -> IteratorResult<Y, R>
//
// Both arms carry g's raise, so driving a generator by hand handles what its body throws the way
// iterating it does.
//
// The no-argument arm is offered only when `undefined` is assignable to N, since omitting the
// argument sends `undefined` at runtime. An inferred N is `unknown` and accepts it. A declared
// narrow one such as `Generator<number, string, string>` does not, so that generator offers only
// the one-argument arm and a bare `it.next()` on it is reported as missing an argument. Making
// the parameter optional, `next(value?: N)`, would not close the hole, because an optional
// parameter lets `undefined` arrive for the same reason.
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
// A sync generator runs its body during the call, so the call is where the body's raise escapes.
// The signature's Throws is g's raise slot, which constrain's covariant throws rule records into
// the calling body's sink. A nil slot is the `never` shorthand and contributes nothing.
//
// An async generator returns a promise, and its body's raise surfaces as that promise's
// rejection. Carrying the slot in the promise's Err rather than on the signature means
// `it.next()` alone raises nothing and `await it.next()` reaches the awaiting body's sink.
func advanceSig(g *soltype.GeneratorType, result soltype.Type, params []*soltype.FuncParam) *soltype.FuncType {
	if g.Async {
		return &soltype.FuncType{Params: params, Ret: &soltype.PromiseType{Inner: result, Err: g.Throws}}
	}
	return &soltype.FuncType{Params: params, Ret: result, Throws: g.Throws}
}

// iterationResult builds the type an advance of g evaluates to, a reference to one of the
// built-in iterator-result aliases registerIteratorResultAliases defines in prelude.go.
//
// An advance either yields a value or reports the generator finished, and `IteratorResult<Y, R>`
// is one arm per outcome tagged by `done`. Two arms keep Y and R distinct, where one flat
// `{value: Y | R, done: boolean}` object would merge them. Reducing a result to one arm by
// testing its `done` needs narrowing on a literal-tagged union, which does not reduce this union
// yet, so a bare `r.value` reads the join of both arms.
//
// A generator that cannot reach an outcome names the reachable arm alone, which is more precise
// and still annotatable. `gen fn g() { yield 1 throw "boom" }` has `never` in Ret and advances
// to `IteratorYieldResult<1>`; one that only finishes advances to `IteratorReturnResult<R>`.
//
// References are interned, since a member access mints one per read and constrain keys its cycle
// cache on pointer identity.
func (c *checker) iterationResult(g *soltype.GeneratorType) soltype.Type {
	yields, returns := !isNeverType(g.Yield), !isNeverType(g.Ret)
	switch {
	case yields && returns:
		return c.iterationResultRef(iteratorResultName, g.Yield, g.Ret)
	case yields:
		return c.iterationResultRef(iteratorYieldResultName, g.Yield)
	case returns:
		return c.iterationResultRef(iteratorReturnResultName, g.Ret)
	}
	return &soltype.NeverType{}
}

// iterationResultRef builds an interned reference to one of the iterator-result aliases.
func (c *checker) iterationResultRef(name string, args ...soltype.Type) soltype.Type {
	return c.ctx.internAlias(&soltype.AliasType{Name: name, TypeArgs: args})
}

// acceptsUndefined reports whether `undefined` is assignable to t without narrowing t, the
// question that decides whether generatorNext offers its no-argument arm. The trial runs under a
// throwaway probe, so t picks up no bound from being asked.
//
// A trial that succeeds only by recording a bound does not count as accepting: it says t is a
// variable the constraint would WIDEN to admit `undefined`, not one that already admits it. A
// declared type parameter is the case that matters. In
// `fn drive<T>(it: Generator<number, string, T>) { it.next() }`, T stands for whatever the caller
// picks, so reading the trial's success alone would offer the no-argument arm and let
// `drive(g())` send `undefined` into a `g` whose body reads its sent value as a string.
//
// Nothing at an access site tells a declared parameter's var from an ordinary unsolved one, since
// checkTypeParamsProducible decides that afterwards from the bounds the whole body recorded. So
// the mutation test declines both, costing `Generator<1, undefined, _>` whose body never reads
// its sent value the argumentless call. Writing that slot as `unknown` restores it.
func (c *checker) acceptsUndefined(t soltype.Type) bool {
	ok, mutated := c.ctx.trialMutatesBounds(&soltype.UndefinedType{}, t, newSeenPairs(), false)
	return ok && !mutated
}
