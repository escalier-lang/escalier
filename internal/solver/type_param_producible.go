package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// checkTypeParamsProducible reports each declared type parameter of a body-carrying
// generic function that its body cannot produce for an arbitrary caller choice. A
// declared parameter's var is a bounded inference var, so checking the body's `return x`
// runs `constrain(number, T)`, which records `number` as a LOWER bound of `T` rather than
// rejecting it. Nothing else verifies that the body actually produces every `T` the caller
// could pick, so `fn make<T>(x: number) -> T { return x }` is accepted and its rendered
// `fn <T>(x: number) -> T` over-promises.
//
// A parameter is flagged only when BOTH hold, so a genuinely parametric body stays valid:
//   - it occurs in an OUTPUT position of the signature, since only an output parameter is
//     handed back to the caller. A concrete lower bound on an input-only parameter, such
//     as a reassignment `fn h<T>(mut x: T) { x = 5 }`, is never observed by the caller.
//   - the body forces a concrete floor onto it, a non-variable lower bound the caller did
//     not supply. `fn id<T>(x: T) -> T { return x }` records no concrete lower bound, so
//     it is not flagged.
//
// The two callers cover both surfaces that mint a generic function. inferFunc checks a
// standalone `fn make<T>(…)` declaration, and constrainInitAgainstAnnotation checks the
// annotation form `val f: fn<T>(…) = fn (…) {…}` against the fresh instance its body flows
// into. node supplies the blame span.
func (c *checker) checkTypeParamsProducible(node ast.Node, ft *soltype.FuncType) {
	if len(ft.TypeParams) == 0 {
		return
	}
	paramVars := set.NewSet[*soltype.TypeVarType]()
	for _, tp := range ft.TypeParams {
		paramVars.Add(tp.Var)
	}
	output := outputTypeVars(ft)
	for _, tp := range ft.TypeParams {
		if !output.Contains(tp.Var) {
			continue
		}
		if floor, ok := forcedConcreteFloor(tp.Var, paramVars); ok {
			c.report(&TypeParamNotProducibleError{Name: tp.Name, Floor: floor, Node: node})
		}
	}
}

// outputTypeVars collects the inference vars that occur in an OUTPUT position of a
// function signature, walking the return covariantly and each parameter contravariantly.
// A var inside a callback parameter's return `fn(cb: fn() -> T)` therefore reads as an
// input and is excluded. It walks only the signature's value positions, never the
// TypeParams binder list. FuncType.Accept visits each binder var at the call polarity,
// which would mark every parameter as output regardless of where it appears.
func outputTypeVars(ft *soltype.FuncType) set.Set[*soltype.TypeVarType] {
	pv := &polarityVarVisitor{out: set.NewSet[*soltype.TypeVarType]()}
	ft.Ret.Accept(pv, soltype.Positive)
	if ft.SelfParam != nil {
		ft.SelfParam.Type.Accept(pv, soltype.Negative)
	}
	for _, p := range ft.Params {
		p.Type.Accept(pv, soltype.Negative)
	}
	return pv.out
}

// polarityVarVisitor records every inference var it meets in POSITIVE position. It reads
// only the structural polarity a var occurs at, so it does not descend into a var's bound
// side-graph. A var is a leaf whose Accept visits it and stops. recordMutWriteView adds
// the write view of a `mut` borrow's inner, so a var written through a mut field is seen
// in both polarities like the occurrence visitors in simplify.go.
type polarityVarVisitor struct {
	out set.Set[*soltype.TypeVarType]
}

func (v *polarityVarVisitor) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	if recordMutWriteView(v, t, pol) {
		return soltype.EnterResult{}
	}
	if tv, ok := t.(*soltype.TypeVarType); ok {
		if pol == soltype.Positive {
			v.out.Add(tv)
		}
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (v *polarityVarVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// forcedConcreteFloor returns the concrete type the body forces as a lower bound of a
// type parameter, or ok=false when the parameter carries no concrete floor. It follows a
// lower bound that is an inference var into that var's own lower bounds, so the annotation
// form `val f: fn<T>(x: number) -> T = fn (x) { return x }` reaches `number` through the
// value's own param var that links the body to the annotation's `T`.
//
// A non-variable lower bound is a forced floor only when it mentions NONE of the
// function's declared type-param vars in paramVars. `number` mentions none, so the body
// produced a value with no dependence on the caller's choice. A `T | number` lower bound,
// from `fn<T>(x: T | number) -> T`, mentions `T`, so the returned value carries the
// caller's own `T` and is not a value the body forced. The seen set keeps a cyclic bound
// graph terminating.
func forcedConcreteFloor(root *soltype.TypeVarType, paramVars set.Set[*soltype.TypeVarType]) (soltype.Type, bool) {
	seen := set.NewSet[*soltype.TypeVarType]()
	var walk func(v *soltype.TypeVarType) (soltype.Type, bool)
	walk = func(v *soltype.TypeVarType) (soltype.Type, bool) {
		if seen.Contains(v) {
			return nil, false
		}
		seen.Add(v)
		for _, b := range v.LowerBounds {
			if bv, ok := b.(*soltype.TypeVarType); ok {
				if paramVars.Contains(bv) {
					continue // another declared parameter is a rigid boundary, not a forced floor
				}
				if floor, found := walk(bv); found {
					return floor, true
				}
				continue
			}
			if mentionsAnyVar(b, paramVars) {
				continue // the value depends on the caller's own type parameter, not forced
			}
			return b, true // a fully concrete lower bound: the body forced this value
		}
		return nil, false
	}
	return walk(root)
}

// mentionsAnyVar reports whether t structurally contains any of the given vars. It reads
// only the written shape, so it does not descend into a var's bound side-graph. A var is
// a leaf whose Accept visits it and stops.
func mentionsAnyVar(t soltype.Type, vars set.Set[*soltype.TypeVarType]) bool {
	v := &varMentionVisitor{vars: vars}
	t.Accept(v, soltype.Positive)
	return v.found
}

type varMentionVisitor struct {
	vars  set.Set[*soltype.TypeVarType]
	found bool
}

func (v *varMentionVisitor) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if tv, ok := t.(*soltype.TypeVarType); ok {
		if v.vars.Contains(tv) {
			v.found = true
		}
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (v *varMentionVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }
