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
	g, ok := generatorCarrier(c.ctx, carrier)
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

// generatorCarrier reads the generator a member access on t goes through.
//
// A receiver often stands for more than one generator. `val it = g()` types `it` as the call's
// fresh result var, and a single call to a polymorphic generator leaves that var with two
// lower bounds differing only in their fresh slot variables. Branching, as in `val it = if b
// { g() } else { h() }`, leaves one bound per branch. joinGenerators folds whatever the
// receiver stands for into the one generator every part has to satisfy, so all of these read
// their members off a single type.
//
// A receiver standing for anything besides generators is declined, so `if b { g() } else { 5 }`
// keeps the structural field-requirement path, where the non-generator part is rejected. A var
// with no lower bound at all is declined too, since nothing yet says it holds a generator. That
// is what an unannotated parameter is, so `fn f(it) { it.next() }` still infers the structural
// receiver `{next: fn () -> T0}`.
func generatorCarrier(c *Context, t soltype.Type) (*soltype.GeneratorType, bool) {
	parts, ok := generatorParts(t)
	if !ok || len(parts) == 0 {
		return nil, false
	}
	return joinGenerators(c, parts)
}

// generatorParts collects the generators a receiver stands for, reporting false as soon as it
// stands for anything else. A borrow is already peeled off by readCarrier before the receiver
// reaches here, so `mut Generator<…>` arrives as the bare generator.
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

// joinGenerators folds several generators into the one a member read goes through, the least
// generator every input is a subtype of. Yield, Ret, and Throws are covariant, so the join
// unions each of them. Next is contravariant, being the value a caller sends in, so the join
// intersects it instead. A value the caller sends has to be acceptable to every generator the
// receiver may hold. A single input is returned unchanged, which is the path an ordinary
// `val it = g()` takes.
//
// A sync and an async generator are unrelated under subtyping, so a receiver mixing the two
// has no join and is declined.
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
// carries `next` alone. The `return` and `throw` methods, which finish a generator early,
// need no new machinery beyond what next uses here and are not declared yet.
func (c *checker) generatorBody(g *soltype.GeneratorType) *soltype.ObjectType {
	return &soltype.ObjectType{Elems: []soltype.ObjTypeElem{c.generatorNext(g)}}
}

// generatorNext declares the `next` method that advances g, as an overload set of up to two
// arms. N below is g's Next slot, the type a `yield` expression in the body evaluates to and so
// the value a caller sends back in. iterationResult builds the shared result type.
//
//	next() -> {value: Y, done: false} | {value: R, done: true}
//	next(value: N) -> {value: Y, done: false} | {value: R, done: true}
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

// iterationResult builds the type an advance of g evaluates to, a reference to one of the
// built-in iterator-result aliases at g's Yield and Ret slots. registerIteratorResultAliases in
// prelude.go holds the three alias bodies, so the shape lives in one place and a caller may
// annotate against it by name.
//
// An advance either produces a yielded value or reports that the generator finished with its
// return value, and `IteratorResult<Y, R>` is the union of one arm per outcome, tagged by
// `done`. Naming the two arms keeps Y and R distinct. `gen fn g() { yield 1 return "done" }`
// advances to `IteratorResult<1, "done">`, where one flat `{value: Y | R, done: boolean}` object
// would merge both into `1 | "done"` and lose which value belongs to which outcome. Reducing a
// result to one arm by testing its `done` needs narrowing on a literal-tagged union, which does
// not reduce this union yet, so today a bare `r.value` reads the join of both arms.
//
// A generator that cannot reach one outcome names that outcome's arm alone rather than the
// union, which is a strictly more precise result and one an annotation can be written against.
// `gen fn g() { yield 1 throw "boom" }` has `never` in Ret, so it never finishes normally and
// advances to `IteratorYieldResult<1>`. A generator that only ever finishes has `never` in Yield
// and advances to `IteratorReturnResult<R>`. One that can do neither advances to `never`.
//
// Every reference is interned, since a member access mints one per read and constrain keys its
// cycle cache on pointer identity.
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
// question that decides whether generatorNext offers its no-argument arm. The trial runs under
// a throwaway probe, so t picks up no bound from being asked.
//
// A trial that succeeds only by recording a bound does not count as accepting. Such a trial
// says t is a variable the constraint would WIDEN to admit `undefined`, not a type that already
// admits it, and the probe throws that widening away. A declared type parameter is the case
// that matters. For `fn drive<T>(it: Generator<number, string, T>) { it.next() }`, T has to
// stand for any type its caller picks, so sending `undefined` is exactly what the gate exists
// to reject. Reading the trial's success alone would offer the no-argument arm there and let
// `drive(g())`, for a `g` whose body reads its sent value as a string, send `undefined` into
// that body.
//
// A declared parameter's var is an ordinary bounded inference var, and nothing at an access
// site tells the two apart — checkTypeParamsProducible decides that afterwards from the bounds
// the whole body recorded. So the mutation test also declines an ordinary unsolved variable,
// which costs `Generator<1, undefined, _>` whose body never reads its sent value the
// argumentless call. Writing that slot as `unknown` restores it.
func (c *checker) acceptsUndefined(t soltype.Type) bool {
	ok, mutated := c.ctx.trialMutatesBounds(&soltype.UndefinedType{}, t, newSeenPairs(), false)
	return ok && !mutated
}
