package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// resolveTypeParams resolves a `<…>` type-parameter list to soltype TypeParams in two
// passes, so a bound or default may reference any sibling parameter regardless of order.
// Pass 1 mints one fresh var per parameter and declares its name in scope. Pass 2 resolves
// each parameter's constraint into its var's upper bound and its default, reading names
// against the scope that now holds every sibling.
//
// Declaring every parameter before reading any bound is what lets a forward reference
// `<T: U, U>`, a mutual cycle `<T: U, U: T>`, an F-bound `<T: Foo<T>>`, a mutual F-bound
// `<T: Cmp<U>, U: Cmp<T>>`, and a defaulting reference `<T = U, U>` all resolve. A single
// declare-and-resolve pass leaves a later-declared sibling undeclared when its bound is
// read, so the reference falls through to general type-ref resolution and reports
// `Unsupported: TypeRefTypeAnn`. Because every sibling is in scope up front, a true mutual
// cycle resolves that a topological sort of the parameters cannot order.
//
// The result stays in declaration order, so instantiation substitutes type arguments
// positionally. The class and, once generic-function inference lands, the function and
// method paths both route their `<…>` lists through here so bound resolution never forks.
func (c *checker) resolveTypeParams(scope *Scope, lvl int, params []*ast.TypeParam) []*soltype.TypeParam {
	out := make([]*soltype.TypeParam, len(params))
	// Pass 1: mint each parameter's var and declare its name, so a bound in pass 2 may
	// reference any sibling — earlier, later, itself, or mutually.
	for i, p := range params {
		v := c.freshAt(lvl)
		scope.defineType(p.Name, TypeBinding{Type: v})
		out[i] = &soltype.TypeParam{Name: p.Name, Var: v}
	}
	// Pass 2: resolve each constraint into its var's upper bound and each default, now that
	// every sibling name is in scope.
	for i, p := range params {
		if p.Constraint != nil {
			if ct, ok := c.resolveTypeAnn(scope, p.Constraint, lvl); ok {
				c.ctx.addUpperBound(out[i].Var, ct)
			}
		}
		if p.Default != nil {
			if dt, ok := c.resolveTypeAnn(scope, p.Default, lvl); ok {
				out[i].Default = dt
			}
		}
	}
	return out
}
