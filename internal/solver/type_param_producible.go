package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// checkTypeParamsProducible flags a declared type parameter whose body cannot produce it
// for an arbitrary caller choice. A parameter's var is a bounded inference var, so a body
// `return x` with `x: number` records `number` as a LOWER bound of `T` rather than being
// rejected, and `fn make<T>(x: number) -> T { return x }` renders `fn <T>(x: number) -> T`,
// which over-promises. A parameter is flagged only when it occurs in an OUTPUT position
// and forcedConcreteFloor finds a concrete floor the body forced onto it; `fn id<T>(x: T)
// -> T` records none and stays valid. inferFunc calls this for a standalone declaration
// and constrainInitAgainstAnnotation for the annotation form; node supplies the blame span.
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

// outputTypeVars collects the inference vars in an OUTPUT position of a signature, walking
// the return covariantly and each parameter contravariantly. It skips the TypeParams
// binder list, which FuncType.Accept would otherwise visit at the call polarity.
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

// polarityVarVisitor records every inference var it meets in POSITIVE position, reading
// only structural polarity without descending into a var's bound side-graph.
// recordMutWriteView adds the write view of a `mut` borrow's inner, so a var written
// through a mut field is seen in both polarities like the occurrence visitors in simplify.go.
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

// forcedConcreteFloor returns the concrete type the body forces as a lower bound of a type
// parameter, following inference-var lower bounds transitively into their own bounds. A
// union lower bound splits into independent obligations, since `A | B <: T` needs both
// `A <: T` and `B <: T`, so an independently concrete branch such as the `number` in
// `T | number` is a forced floor even when a sibling branch is the caller's own `T`. A
// branch that mentions a declared type-param var carries the caller's choice and is not
// forced. The seen set keeps a cyclic bound graph terminating.
func forcedConcreteFloor(root *soltype.TypeVarType, paramVars set.Set[*soltype.TypeVarType]) (soltype.Type, bool) {
	seen := set.NewSet[*soltype.TypeVarType]()
	var walkVar func(v *soltype.TypeVarType) (soltype.Type, bool)
	var walkBound func(b soltype.Type) (soltype.Type, bool)
	walkBound = func(b soltype.Type) (soltype.Type, bool) {
		switch b := b.(type) {
		case *soltype.TypeVarType:
			if paramVars.Contains(b) {
				return nil, false // a declared parameter is a rigid boundary, not a forced floor
			}
			return walkVar(b)
		case *soltype.UnionType:
			// `A | B <: T` splits into `A <: T` and `B <: T`, so any branch that forces a
			// floor forces one on T.
			for _, m := range b.Types {
				if floor, found := walkBound(m); found {
					return floor, true
				}
			}
			return nil, false
		default:
			if mentionsAnyVar(b, paramVars) {
				return nil, false // the value depends on the caller's own type parameter
			}
			return b, true // a fully concrete lower bound: the body forced this value
		}
	}
	walkVar = func(v *soltype.TypeVarType) (soltype.Type, bool) {
		if seen.Contains(v) {
			return nil, false
		}
		seen.Add(v)
		for _, b := range v.LowerBounds {
			if floor, found := walkBound(b); found {
				return floor, true
			}
		}
		return nil, false
	}
	return walkVar(root)
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
