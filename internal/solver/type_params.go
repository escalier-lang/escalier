package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// resolveTypeParams resolves a `<…>` type-parameter list to soltype TypeParams in four passes,
// since a default and a bound need opposite visibility. Pass 1 mints one fresh var per parameter.
// Pass 2 resolves each parameter's default and then declares that parameter, so a default reads
// only the earlier siblings, the ones instantiation can substitute for it. Pass 3 resolves each
// constraint into its var's upper bound against the full list, so a forward `<T: U, U>`, a mutual
// `<T: U, U: T>`, and an F-bound `<T: Foo<T>>` all resolve. Pass 4 checks each default against
// its own parameter's constraint. The result stays in declaration order, and the alias, class,
// enum, and function-annotation paths all route through here.
func (c *checker) resolveTypeParams(scope *Scope, lvl int, params []*ast.TypeParam) []*soltype.TypeParam {
	out := make([]*soltype.TypeParam, len(params))
	// Pass 1: mint each parameter's var. Nothing is declared yet, so pass 2 controls which
	// siblings each default can see.
	for i, p := range params {
		out[i] = &soltype.TypeParam{Name: p.Name, Var: c.freshAt(lvl)}
	}
	// Pass 2: resolve each default under the names declared so far, then declare this
	// parameter. A default naming a later sibling or itself finds the name undeclared.
	for i, p := range params {
		if p.Default != nil && !c.reportDefaultForwardRef(params, i) {
			if dt, ok := c.resolveTypeAnn(scope, p.Default, lvl); ok {
				out[i].Default = dt
			}
		}
		scope.defineType(p.Name, TypeBinding{Type: out[i].Var})
	}
	// Pass 3: resolve each constraint into its var's upper bound, now that every sibling name
	// is in scope.
	for i, p := range params {
		if p.Constraint == nil {
			continue
		}
		if ct, ok := c.resolveTypeAnn(scope, p.Constraint, lvl); ok {
			c.ctx.addUpperBound(out[i].Var, ct)
			// Keep the declared constraint where later solving cannot overwrite it. The var's
			// upper-bound list grows as constraints flow in, so a reader that wants what the
			// source wrote reads this field instead.
			out[i].Constraint = ct
		}
	}
	// Pass 4: check each default against its own parameter's bound, so `<T: string = number>`
	// is rejected at the declaration. A default fills the argument at every use site that
	// omits it, so a default outside the bound would smuggle in an argument the bound forbids.
	// This runs as its own pass because a bound is not resolved until pass 3, after the
	// default it is compared against.
	for i, p := range params {
		if out[i].Default == nil || out[i].Constraint == nil {
			continue
		}
		// Trial the comparison under a probe rather than running it live, so any bound it
		// appends is rolled back. A default is a fully resolved type with nothing left to
		// infer, so a live comparison would gain it nothing. It would cost something when the
		// bound names a sibling parameter. Running `<T, U: T = number>` live compares number
		// against T's var and leaves T carrying number as a lower bound, a claim that T must
		// accept number that the source never wrote.
		c.blameConstraintErrors(p.Default, c.ctx.trialUnderProbe(out[i].Default, out[i].Constraint))
	}
	return out
}

// reportDefaultForwardRef reports each parameter params[i]'s default names that it may not,
// params[i] itself or one declared after it, and returns whether it reported. A reference fills an
// omitted argument from the default with the earlier arguments substituted in, as
// buildAliasInstance does for the `U = T` of `type Pair<T, U = T>`. A later or self reference has
// nothing to substitute, so it stays the declaration's own var, shared by every reference to the
// type. A rejected default is dropped, which leaves the parameter required. Neither instance
// builder reports the follow-on arity error in every case, which PR18's shared helper settles.
//
// One report per offending name, blaming its leftmost reference. Naming each one lets a default
// that reaches two later parameters be fixed in a single pass, while a name written twice reports
// once, since both references need the same fix.
func (c *checker) reportDefaultForwardRef(params []*ast.TypeParam, i int) bool {
	forbidden := set.NewSet[string]()
	for _, p := range params[i:] {
		forbidden.Add(p.Name)
	}
	var scan typeParamRefScan
	params[i].Default.Accept(&scan)
	reported := set.NewSet[string]()
	for _, ref := range scan.free {
		name := ast.QualIdentToString(ref.Name)
		if !forbidden.Contains(name) || reported.Contains(name) {
			continue
		}
		reported.Add(name)
		c.report(&TypeParamDefaultForwardRefError{Ref: ref, Param: params[i].Name, Target: name})
	}
	return reported.Len() > 0
}

// typeParamRefScan collects a type annotation's free references, the `Name` and `Name<…>`
// references that no binder written inside the annotation declares. It tracks the region each
// binder covers: a `fn <U>(…)` quantifier covers its function annotation, a mapped key covers the
// object annotation holding it, and an `infer U` covers its conditional's Extends and Then
// operands, matching where resolveCondTypeAnn declares the name.
type typeParamRefScan struct {
	ast.DefaultVisitor
	scopes []set.Set[string]     // one name set per region the walk is inside, innermost last
	free   []*ast.TypeRefTypeAnn // in traversal order, so a caller reports the leftmost
}

func (v *typeParamRefScan) EnterTypeAnn(t ast.TypeAnn) bool {
	switch n := t.(type) {
	case *ast.TypeRefTypeAnn:
		if !v.shadowed(ast.QualIdentToString(n.Name)) {
			v.free = append(v.free, n)
		}
	case *ast.FuncTypeAnn:
		names := set.NewSet[string]()
		for _, tp := range n.TypeParams {
			names.Add(tp.Name)
		}
		v.push(names)
	case *ast.ObjectTypeAnn:
		// A mapped type is an object element rather than a type annotation of its own, so the
		// walk reaches its key constraint, name, and value without entering the mapped node.
		// Read the key binder off the element here and scope it to the object annotation.
		names := set.NewSet[string]()
		for _, elem := range n.Elems {
			if mapped, ok := elem.(*ast.MappedTypeAnn); ok {
				names.Add(mapped.TypeParam.Name)
			}
		}
		v.push(names)
	case *ast.CondTypeAnn:
		// The four operands are walked here rather than by the shared descent, since an
		// `infer U` clause covers only two of them. Check reads no capture, and a capture
		// named again in Else is a free reference.
		n.Check.Accept(v)
		v.push(set.FromSlice(inferAnnNames(n.Extends)))
		n.Extends.Accept(v)
		n.Then.Accept(v)
		v.pop()
		n.Else.Accept(v)
		return false
	}
	return true
}

// ExitTypeAnn closes the region opened on entering a function or object annotation. A
// conditional pops its own region inside EnterTypeAnn, since it drives its operands itself.
func (v *typeParamRefScan) ExitTypeAnn(t ast.TypeAnn) {
	switch t.(type) {
	case *ast.FuncTypeAnn, *ast.ObjectTypeAnn:
		v.pop()
	}
}

func (v *typeParamRefScan) push(names set.Set[string]) {
	v.scopes = append(v.scopes, names)
}

func (v *typeParamRefScan) pop() {
	v.scopes = v.scopes[:len(v.scopes)-1]
}

// shadowed reports whether any region the walk is inside declares name.
func (v *typeParamRefScan) shadowed(name string) bool {
	for _, names := range v.scopes {
		if names.Contains(name) {
			return true
		}
	}
	return false
}

// typeArgRange returns the type-argument counts a reference to a generic class or alias may
// supply. required counts the parameters with no default, total counts them all, and any count
// between the two is accepted because each omitted trailing parameter is filled from its
// default. A count outside the range is an arity mismatch, which each caller reports under its
// own error type.
func typeArgRange(params []*soltype.TypeParam) (required, total int) {
	for _, p := range params {
		if p.Default == nil {
			required++
		}
	}
	return required, len(params)
}

// resolveTypeArgsWithDefaults produces one type argument per declared parameter, so a class or
// alias instance always carries a full argument list for substitution to zip against. Each
// parameter draws its argument from one of three sources:
//
//  1. The annotation the reference wrote at that position, resolved in scope.
//  2. The parameter's own default, for a parameter past the last written argument. The
//     arguments already resolved for earlier parameters are substituted into the default
//     first, so `<T, U = T>` referenced as `<number>` fills U with number.
//  3. A fresh var, when the written annotation does not resolve or the reference omitted a
//     parameter that has no default. Recovering keeps the argument list full. The omission
//     itself is reported by the caller's arity check.
func (c *checker) resolveTypeArgsWithDefaults(
	scope *Scope,
	params []*soltype.TypeParam,
	ref *ast.TypeRefTypeAnn,
	lvl int,
) []soltype.Type {
	got := len(ref.TypeArgs)
	args := make([]soltype.Type, len(params))
	for i := range params {
		switch {
		case i < got:
			if resolved, ok := c.resolveTypeAnn(scope, ref.TypeArgs[i], lvl); ok {
				args[i] = resolved
			} else {
				args[i] = c.freshAt(lvl)
			}
		case params[i].Default != nil:
			subst := newTypeSubst(params[:i], args[:i], nil, nil)
			args[i] = params[i].Default.Accept(subst, soltype.Positive)
		default:
			args[i] = c.freshAt(lvl)
		}
	}
	return args
}
