package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// resolveTypeParams resolves a `<…>` type-parameter list to soltype TypeParams in two
// passes, so a bound may reference any sibling parameter regardless of order. Pass 1 mints
// one fresh var per parameter and declares its name in scope. Pass 2 resolves each
// parameter's constraint into its var's upper bound and its default, reading names against
// the scope that now holds every sibling.
//
// Declaring every parameter before reading any bound is what lets a forward reference
// `<T: U, U>`, a mutual cycle `<T: U, U: T>`, an F-bound `<T: Foo<T>>`, and a mutual F-bound
// `<T: Cmp<U>, U: Cmp<T>>` all resolve. A single declare-and-resolve pass leaves a
// later-declared sibling undeclared when its bound is read, so the reference falls through to
// general type-ref resolution and reports `Unsupported: TypeRefTypeAnn`. Because every sibling
// is in scope up front, a true mutual cycle resolves that a topological sort of the parameters
// cannot order.
//
// A default is restricted where a bound is not. It may name only an earlier parameter. See
// reportDefaultForwardRef for why, and for what a rejected default leaves behind.
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
				// Keep the declared constraint where later solving cannot overwrite it. The
				// var's upper-bound list grows as constraints flow in, so a reader that wants
				// what the source wrote reads this field instead.
				out[i].Constraint = ct
			}
		}
		if p.Default != nil && !c.reportDefaultForwardRef(params, i) {
			if dt, ok := c.resolveTypeAnn(scope, p.Default, lvl); ok {
				out[i].Default = dt
			}
		}
	}
	return out
}

// reportDefaultForwardRef reports the first reference params[i]'s default makes to a parameter
// it may not name, meaning params[i] itself or one declared after it. It returns whether it
// reported.
//
// A reference that omits a trailing argument fills it from that parameter's default,
// substituting the arguments before it. buildAliasInstance does exactly that for the `U = T`
// of `type Pair<T, U = T>`. Only a parameter earlier in the list has an argument by then, so
// only an earlier one is replaced by that substitution.
//
// A later or self reference is left untouched, so it stays the declaration's own var, and
// every reference to the type shares that one var. In `type Pair<T = U, U = number> = {a: T, b: U}`,
// `T`'s default resolves to `U`'s var, so admitting it would let `val q: Pair = {a: "s", b: 3}`
// widen a second `val p: Pair = {a: 1, b: 2}` to `Pair<1 | "s", number>`. Rejecting the default
// keeps that var out of every instance.
//
// The caller drops a rejected default, which leaves the parameter required, so a reference
// that omits it reports an arity mismatch naming the omission.
//
// The restriction covers the default position alone. A bound may name any sibling, since
// `<T: U, U: T>` is a legal mutual F-bound and `<T: Foo<T>>` names the parameter being
// declared. Neither is substituted positionally, so neither needs the ordering a default does.
func (c *checker) reportDefaultForwardRef(params []*ast.TypeParam, i int) bool {
	forbidden := set.NewSet[string]()
	for _, p := range params[i:] {
		forbidden.Add(p.Name)
	}
	scan := typeParamRefScan{bound: set.NewSet[string]()}
	params[i].Default.Accept(&scan)
	for _, ref := range scan.refs {
		name := ast.QualIdentToString(ref.Name)
		if !forbidden.Contains(name) || scan.bound.Contains(name) {
			continue
		}
		c.report(&TypeParamDefaultForwardRefError{Ref: ref, Param: params[i].Name, Target: name})
		return true
	}
	return false
}

// typeParamRefScan walks a type annotation and records both the names it reads and the names
// it binds, so a caller can tell a reference to an enclosing `<…>` parameter from one a nested
// binder introduced.
//
// refs holds every `Name` and `Name<…>` reference in traversal order, so a caller reports the
// leftmost offending one. bound holds the name each binder inside the annotation introduces.
// There are three such binders: a nested `fn <U>(…)` quantifier, a mapped type's key binder
// written `K` in `{[K: Keys]: V}`, and a conditional's `infer U`. A reference to one of those
// reads the binder's own name rather than an enclosing parameter's.
//
// bound covers the whole annotation rather than each binder's own region, which errs toward
// silence. In `{a: fn <U>(x: U) -> U, b: U}` the `b: U` reads an enclosing `U`, and the scan
// suppresses it along with the two inside the nested quantifier.
type typeParamRefScan struct {
	ast.DefaultVisitor
	refs  []*ast.TypeRefTypeAnn
	bound set.Set[string]
}

func (v *typeParamRefScan) EnterTypeAnn(t ast.TypeAnn) bool {
	switch n := t.(type) {
	case *ast.TypeRefTypeAnn:
		v.refs = append(v.refs, n)
	case *ast.FuncTypeAnn:
		for _, tp := range n.TypeParams {
			v.bound.Add(tp.Name)
		}
	case *ast.ObjectTypeAnn:
		// A mapped type is an object element rather than a type annotation of its own, so the
		// walk reaches its key constraint, name, and value without entering the mapped node.
		// Read its key binder off the element here.
		for _, elem := range n.Elems {
			if mapped, ok := elem.(*ast.MappedTypeAnn); ok {
				v.bound.Add(mapped.TypeParam.Name)
			}
		}
	case *ast.InferTypeAnn:
		v.bound.Add(n.Name)
	}
	return true
}
