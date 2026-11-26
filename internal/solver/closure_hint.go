package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferExprIn infers e where its value flows into a destination of type dest. When e is a
// function expression and dest is a function type, each parameter e leaves unannotated takes
// the type of dest's parameter at the same position, before e's body is checked.
func (c *checker) inferExprIn(scope *Scope, lvl int, e ast.Expr, dest soltype.Type) soltype.Type {
	if fe, ok := e.(*ast.FuncExpr); ok {
		if ft := contextFuncType(dest); ft != nil {
			c.closureHint, c.closureHintFor = ft, fe
		}
	}
	return c.inferExpr(scope, lvl, e)
}

// contextFuncType returns the function type t, or nil when t is not one. A function type that
// quantifies type or lifetime parameters returns nil too. A closure is checked against one with
// each binder held rigid, which a parameter type copied out of it would not be.
func contextFuncType(t soltype.Type) *soltype.FuncType {
	ft, ok := t.(*soltype.FuncType)
	if !ok || quantified(ft) {
		return nil
	}
	return ft
}

// takeClosureHint returns the function type node flows into, as inferExprIn recorded it, and
// clears the record. It returns nil when node is not the function expression the record names.
func (c *checker) takeClosureHint(node ast.Node) *soltype.FuncType {
	hint := c.closureHint
	if c.closureHintFor == nil || ast.Node(c.closureHintFor) != node {
		hint = nil
	}
	c.closureHint, c.closureHintFor = nil, nil
	return hint
}

// assignDestType returns the declared type of the place an assignment target names, read
// without inferring the target, or nil when no such type is at hand. A name bound to one
// monomorphic type gives that type, and a field of such a name gives the field's declared type.
func (c *checker) assignDestType(scope *Scope, target ast.Expr) soltype.Type {
	switch target := target.(type) {
	case *ast.IdentExpr:
		return c.monoBindingType(scope, target.Name)
	case *ast.MemberExpr:
		obj, ok := target.Object.(*ast.IdentExpr)
		if !ok || target.Prop == nil {
			return nil
		}
		recv := c.monoBindingType(scope, obj.Name)
		if recv == nil {
			return nil
		}
		if prop, ok := c.declaredField(recv, target.Prop.Name); ok {
			return prop.Type
		}
	}
	return nil
}

// monoBindingType returns the type of the value binding name, when the binding holds one
// monomorphic type, and nil otherwise.
func (c *checker) monoBindingType(scope *Scope, name string) soltype.Type {
	b, found := c.lookupValueBinding(scope, name)
	if !found || len(b.Schemes) != 1 {
		return nil
	}
	if m, ok := b.Schemes[0].(*MonoScheme); ok {
		return m.Ty
	}
	return nil
}

// callArgDests returns, for each of a call's argc arguments, the parameter type the argument
// flows into, or nil where none is known. A target with one signature gives that signature's
// parameter types. An overload set gives the parameter type every arm accepting argc arguments
// agrees on.
func (c *checker) callArgDests(lvl int, target callTarget, argc int) []soltype.Type {
	dests := make([]soltype.Type, argc)
	if target.fn != nil {
		for i := range dests {
			if i < len(target.fn.Params) && !target.fn.Params[i].Rest {
				dests[i] = target.fn.Params[i].Type
			}
		}
		return dests
	}
	if target.overloads == nil {
		return dests
	}
	var arms []*soltype.FuncType
	for _, scheme := range target.overloads.Schemes {
		arm, ok := c.instantiate(scheme, lvl).(*soltype.FuncType)
		if !ok {
			return dests
		}
		if requiredCount(arm) > argc || argc > len(arm.Params) && !hasRestParam(arm) {
			continue
		}
		arms = append(arms, arm)
	}
	for i := range dests {
		dests[i] = agreedParamType(arms, i)
	}
	return dests
}

// agreedParamType returns the type every arm declares for its parameter at index i, or nil
// when two arms differ there, one has no such parameter, or the type holds a variable. resolveOverload
// instantiates the arm it tries afresh, so a variable here would not be the one the call
// constrains.
func agreedParamType(arms []*soltype.FuncType, i int) soltype.Type {
	var agreed soltype.Type
	for _, arm := range arms {
		if quantified(arm) || i >= len(arm.Params) || arm.Params[i].Rest {
			return nil
		}
		t := arm.Params[i].Type
		if holdsTypeVar(t) {
			return nil
		}
		if agreed != nil && !soltype.Equal(agreed, t) {
			return nil
		}
		agreed = t
	}
	return agreed
}

// hasRestParam reports whether ft's last parameter is a rest parameter.
func hasRestParam(ft *soltype.FuncType) bool {
	return len(ft.Params) > 0 && ft.Params[len(ft.Params)-1].Rest
}

// holdsTypeVar reports whether t holds a type variable.
func holdsTypeVar(t soltype.Type) bool {
	finder := &typeVarFinder{}
	t.Accept(finder, soltype.Positive)
	return finder.found
}

// typeVarFinder records whether a type holds a type variable.
type typeVarFinder struct {
	found bool
}

func (v *typeVarFinder) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if _, ok := t.(*soltype.TypeVarType); ok {
		v.found = true
	}
	return soltype.EnterResult{}
}

func (v *typeVarFinder) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }
